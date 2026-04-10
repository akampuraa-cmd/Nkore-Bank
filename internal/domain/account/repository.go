package account

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/outbox"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

// Repository handles account persistence against PostgreSQL.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new account repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// generateAccountNumber returns "NKB" followed by 10 cryptographically random digits.
func generateAccountNumber() (string, error) {
	digits := make([]byte, 10)
	for i := range digits {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("generate account number: %w", err)
		}
		digits[i] = byte('0') + byte(n.Int64())
	}
	return "NKB" + string(digits), nil
}

// Create inserts a new account within the provided transaction.
// It generates a UUID and a random account number before persisting.
func (r *Repository) Create(ctx context.Context, tx *sql.Tx, acct *Account) error {
	acctNum, err := generateAccountNumber()
	if err != nil {
		return fmt.Errorf("repository: create account: %w", err)
	}

	acct.ID = uuid.New().String()
	acct.AccountNumber = acctNum
	acct.Status = StatusActive
	acct.Version = 1
	acct.CreatedAt = time.Now().UTC()
	acct.UpdatedAt = acct.CreatedAt

	query := `INSERT INTO accounts (id, account_number, customer_id, account_type, currency, status, daily_limit, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err = tx.ExecContext(ctx, query,
		acct.ID, acct.AccountNumber, acct.CustomerID, string(acct.AccountType),
		acct.Currency, string(acct.Status), acct.DailyLimit, acct.Version,
		acct.CreatedAt, acct.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("repository: create account: %w", err)
	}
	return nil
}

// GetByID retrieves a single account by its primary key.
func (r *Repository) GetByID(ctx context.Context, id string) (*Account, error) {
	query := `SELECT id, account_number, customer_id, account_type, currency,
		status, daily_limit, version, created_at, updated_at
		FROM accounts WHERE id = $1`

	var acct Account
	err := r.db.Pool.QueryRowContext(ctx, query, id).Scan(
		&acct.ID, &acct.AccountNumber, &acct.CustomerID, &acct.AccountType,
		&acct.Currency, &acct.Status, &acct.DailyLimit, &acct.Version,
		&acct.CreatedAt, &acct.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("repository: get account by id: %w", err)
	}
	return &acct, nil
}

// GetByAccountNumber retrieves an account by its human-readable account number.
func (r *Repository) GetByAccountNumber(ctx context.Context, accountNumber string) (*Account, error) {
	query := `SELECT id, account_number, customer_id, account_type, currency,
		status, daily_limit, version, created_at, updated_at
		FROM accounts WHERE account_number = $1`

	var acct Account
	err := r.db.Pool.QueryRowContext(ctx, query, accountNumber).Scan(
		&acct.ID, &acct.AccountNumber, &acct.CustomerID, &acct.AccountType,
		&acct.Currency, &acct.Status, &acct.DailyLimit, &acct.Version,
		&acct.CreatedAt, &acct.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("repository: get account by number: %w", err)
	}
	return &acct, nil
}

// GetByCustomerID returns every account owned by the given customer.
func (r *Repository) GetByCustomerID(ctx context.Context, customerID string) ([]*Account, error) {
	query := `SELECT id, account_number, customer_id, account_type, currency,
		status, daily_limit, version, created_at, updated_at
		FROM accounts WHERE customer_id = $1`

	rows, err := r.db.Pool.QueryContext(ctx, query, customerID)
	if err != nil {
		return nil, fmt.Errorf("repository: get accounts by customer: %w", err)
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		var acct Account
		if err := rows.Scan(
			&acct.ID, &acct.AccountNumber, &acct.CustomerID, &acct.AccountType,
			&acct.Currency, &acct.Status, &acct.DailyLimit, &acct.Version,
			&acct.CreatedAt, &acct.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("repository: scan account: %w", err)
		}
		accounts = append(accounts, &acct)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: iterate accounts: %w", err)
	}
	return accounts, nil
}

// UpdateStatus changes an account's status using optimistic locking.
// Returns ErrVersionConflict when the row's current version does not match.
func (r *Repository) UpdateStatus(ctx context.Context, tx *sql.Tx, id string, status AccountStatus, version int) error {
	query := `UPDATE accounts SET status = $1, version = version + 1, updated_at = $2
		WHERE id = $3 AND version = $4`

	result, err := tx.ExecContext(ctx, query, string(status), time.Now().UTC(), id, version)
	if err != nil {
		return fmt.Errorf("repository: update account status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: update account status rows affected: %w", err)
	}
	if n == 0 {
		return ErrVersionConflict
	}
	return nil
}

// GetBalance computes the current balance from transaction_entries,
// the outstanding hold amount, and the available balance (current − holds).
func (r *Repository) GetBalance(ctx context.Context, accountID string) (currentBalance, availableBalance, holdAmount decimal.Decimal, err error) {
	query := `SELECT
		COALESCE((SELECT SUM(amount) FROM transaction_entries WHERE account_id = $1), 0),
		COALESCE((SELECT SUM(amount) FROM account_holds
			WHERE account_id = $1 AND released_at IS NULL
			AND (expires_at IS NULL OR expires_at > NOW())), 0)`

	var cb, ha decimal.Decimal
	if err := r.db.Pool.QueryRowContext(ctx, query, accountID).Scan(&cb, &ha); err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("repository: get balance: %w", err)
	}
	return cb, cb.Sub(ha), ha, nil
}

// CreateHold inserts a new account hold within the provided transaction.
func (r *Repository) CreateHold(ctx context.Context, tx *sql.Tx, hold *AccountHold) error {
	hold.ID = uuid.New().String()
	hold.CreatedAt = time.Now().UTC()

	query := `INSERT INTO account_holds (id, account_id, hold_type, amount, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := tx.ExecContext(ctx, query,
		hold.ID, hold.AccountID, hold.HoldType, hold.Amount, hold.ExpiresAt, hold.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("repository: create hold: %w", err)
	}
	return nil
}

// ReleaseHold marks an existing hold as released by setting released_at = NOW().
func (r *Repository) ReleaseHold(ctx context.Context, tx *sql.Tx, holdID string) error {
	query := `UPDATE account_holds SET released_at = NOW() WHERE id = $1 AND released_at IS NULL`

	result, err := tx.ExecContext(ctx, query, holdID)
	if err != nil {
		return fmt.Errorf("repository: release hold: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("repository: release hold rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("repository: release hold: %w", ErrHoldNotFound)
	}
	return nil
}

// InsertOutboxEvent persists a transactional outbox event within the provided transaction.
func (r *Repository) InsertOutboxEvent(ctx context.Context, tx *sql.Tx, event *outbox.OutboxEvent) error {
	event.ID = uuid.New().String()
	event.CreatedAt = time.Now().UTC()

	query := `INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, created_at, retry_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := tx.ExecContext(ctx, query,
		event.ID, event.AggregateType, event.AggregateID, event.EventType,
		event.Payload, event.CreatedAt, 0,
	)
	if err != nil {
		return fmt.Errorf("repository: insert outbox event: %w", err)
	}
	return nil
}
