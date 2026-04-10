package transaction

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

// Repository handles transaction persistence against PostgreSQL.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new transaction repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// generateReferenceNumber returns "TXN" + YYYYMMDDHHmmss + 6 random digits.
func generateReferenceNumber() (string, error) {
	ts := time.Now().UTC().Format("20060102150405")
	digits := make([]byte, 6)
	for i := range digits {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("generate reference number: %w", err)
		}
		digits[i] = byte('0') + byte(n.Int64())
	}
	return "TXN" + ts + string(digits), nil
}

// GetByIdempotencyKey returns an existing transaction for the given key, or nil
// if none exists. This supports idempotent request handling.
func (r *Repository) GetByIdempotencyKey(ctx context.Context, key string) (*Transaction, error) {
	query := `SELECT id, idempotency_key, type, status, reference_number, description, created_at, settled_at
		FROM transactions WHERE idempotency_key = $1`

	var txn Transaction
	err := r.db.Pool.QueryRowContext(ctx, query, key).Scan(
		&txn.ID, &txn.IdempotencyKey, &txn.Type, &txn.Status,
		&txn.ReferenceNumber, &txn.Description, &txn.CreatedAt, &txn.SettledAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("repository: get by idempotency key: %w", err)
	}
	return &txn, nil
}

// Create inserts a new transaction record within the provided database transaction.
func (r *Repository) Create(ctx context.Context, tx *sql.Tx, txn *Transaction) error {
	if txn.ID == "" {
		txn.ID = uuid.New().String()
	}
	if txn.ReferenceNumber == "" {
		ref, err := generateReferenceNumber()
		if err != nil {
			return fmt.Errorf("repository: create transaction: %w", err)
		}
		txn.ReferenceNumber = ref
	}
	txn.CreatedAt = time.Now().UTC()

	query := `INSERT INTO transactions (id, idempotency_key, type, status, reference_number, description, created_at, settled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := tx.ExecContext(ctx, query,
		txn.ID, txn.IdempotencyKey, string(txn.Type), string(txn.Status),
		txn.ReferenceNumber, txn.Description, txn.CreatedAt, txn.SettledAt,
	)
	if err != nil {
		return fmt.Errorf("repository: create transaction: %w", err)
	}
	return nil
}

// CreateEntry inserts an immutable transaction entry within the provided
// database transaction. Entries are never updated after creation.
func (r *Repository) CreateEntry(ctx context.Context, tx *sql.Tx, entry *TransactionEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	entry.CreatedAt = time.Now().UTC()

	query := `INSERT INTO transaction_entries (id, transaction_id, account_id, entry_type, amount, running_balance, currency, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := tx.ExecContext(ctx, query,
		entry.ID, entry.TransactionID, entry.AccountID, string(entry.EntryType),
		entry.Amount, entry.RunningBalance, entry.Currency, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("repository: create entry: %w", err)
	}
	return nil
}

// UpdateStatus sets the transaction status and, when settling, the settled_at
// timestamp. This is performed within the provided database transaction.
func (r *Repository) UpdateStatus(ctx context.Context, tx *sql.Tx, txID string, status TransactionStatus) error {
	var err error
	if status == TxSettled {
		now := time.Now().UTC()
		_, err = tx.ExecContext(ctx,
			`UPDATE transactions SET status = $1, settled_at = $2 WHERE id = $3`,
			string(status), now, txID,
		)
	} else {
		_, err = tx.ExecContext(ctx,
			`UPDATE transactions SET status = $1 WHERE id = $2`,
			string(status), txID,
		)
	}
	if err != nil {
		return fmt.Errorf("repository: update status: %w", err)
	}
	return nil
}

// GetByID retrieves a transaction and all its entries by the transaction ID.
func (r *Repository) GetByID(ctx context.Context, id string) (*Transaction, []*TransactionEntry, error) {
	query := `SELECT id, idempotency_key, type, status, reference_number, description, created_at, settled_at
		FROM transactions WHERE id = $1`

	var txn Transaction
	err := r.db.Pool.QueryRowContext(ctx, query, id).Scan(
		&txn.ID, &txn.IdempotencyKey, &txn.Type, &txn.Status,
		&txn.ReferenceNumber, &txn.Description, &txn.CreatedAt, &txn.SettledAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("repository: transaction not found")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("repository: get transaction by id: %w", err)
	}

	entries, err := r.GetEntriesByTransactionID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return &txn, entries, nil
}

// GetEntriesByTransactionID returns all entries for the given transaction.
func (r *Repository) GetEntriesByTransactionID(ctx context.Context, txID string) ([]*TransactionEntry, error) {
	query := `SELECT id, transaction_id, account_id, entry_type, amount, running_balance, currency, created_at
		FROM transaction_entries WHERE transaction_id = $1 ORDER BY created_at`

	rows, err := r.db.Pool.QueryContext(ctx, query, txID)
	if err != nil {
		return nil, fmt.Errorf("repository: get entries: %w", err)
	}
	defer rows.Close()

	var entries []*TransactionEntry
	for rows.Next() {
		var e TransactionEntry
		if err := rows.Scan(
			&e.ID, &e.TransactionID, &e.AccountID, &e.EntryType,
			&e.Amount, &e.RunningBalance, &e.Currency, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repository: scan entry: %w", err)
		}
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: iterate entries: %w", err)
	}
	return entries, nil
}

// GetAccountStatement returns a paginated list of entries for the given account
// within the specified time range, ordered by creation time descending.
func (r *Repository) GetAccountStatement(ctx context.Context, accountID string, from, to time.Time, limit, offset int) ([]*TransactionEntry, error) {
	query := `SELECT id, transaction_id, account_id, entry_type, amount, running_balance, currency, created_at
		FROM transaction_entries
		WHERE account_id = $1 AND created_at >= $2 AND created_at <= $3
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`

	rows, err := r.db.Pool.QueryContext(ctx, query, accountID, from, to, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("repository: get account statement: %w", err)
	}
	defer rows.Close()

	var entries []*TransactionEntry
	for rows.Next() {
		var e TransactionEntry
		if err := rows.Scan(
			&e.ID, &e.TransactionID, &e.AccountID, &e.EntryType,
			&e.Amount, &e.RunningBalance, &e.Currency, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repository: scan statement entry: %w", err)
		}
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: iterate statement entries: %w", err)
	}
	return entries, nil
}

// GetCurrentBalance computes the current balance for an account as the sum of
// all CREDIT entries minus the sum of all DEBIT entries. The query uses
// SELECT … FOR UPDATE to acquire a pessimistic lock within the transaction.
func (r *Repository) GetCurrentBalance(ctx context.Context, tx *sql.Tx, accountID string) (decimal.Decimal, error) {
	query := `SELECT COALESCE(
		SUM(CASE WHEN entry_type = 'CREDIT' THEN amount ELSE decimal '0' END) -
		SUM(CASE WHEN entry_type = 'DEBIT' THEN amount ELSE decimal '0' END),
		decimal '0')
		FROM transaction_entries WHERE account_id = $1`

	var balance decimal.Decimal
	dest := []any{&balance}
	if err := database.SelectForUpdate(ctx, tx, query, dest, accountID); err != nil {
		if err.Error() == "database: select for update: sql: no rows in result set" {
			return decimal.Zero, nil
		}
		return decimal.Zero, fmt.Errorf("repository: get current balance: %w", err)
	}
	return balance, nil
}

// InsertOutboxEvent persists a transactional outbox event within the provided
// database transaction.
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
