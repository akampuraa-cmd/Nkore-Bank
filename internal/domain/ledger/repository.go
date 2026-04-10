package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/outbox"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

// Repository handles general-ledger persistence against PostgreSQL.
type Repository struct {
	db *database.DB
}

// NewRepository creates a new ledger repository.
func NewRepository(db *database.DB) *Repository {
	return &Repository{db: db}
}

// CreateGLAccount inserts a new GL account within the provided transaction.
func (r *Repository) CreateGLAccount(ctx context.Context, tx *sql.Tx, acct *GLAccount) error {
	if acct.ID == "" {
		acct.ID = uuid.New().String()
	}
	acct.CreatedAt = time.Now().UTC()

	query := `INSERT INTO gl_accounts (id, code, name, account_class, normal_balance, parent_id, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := tx.ExecContext(ctx, query,
		acct.ID, acct.Code, acct.Name, string(acct.AccountClass),
		string(acct.NormalBalance), acct.ParentID, acct.IsActive, acct.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("repository: create gl account: %w", err)
	}
	return nil
}

// GetGLAccount retrieves a GL account by its code.
func (r *Repository) GetGLAccount(ctx context.Context, code string) (*GLAccount, error) {
	query := `SELECT id, code, name, account_class, normal_balance, parent_id, is_active, created_at
		FROM gl_accounts WHERE code = $1`

	var acct GLAccount
	err := r.db.Pool.QueryRowContext(ctx, query, code).Scan(
		&acct.ID, &acct.Code, &acct.Name, &acct.AccountClass,
		&acct.NormalBalance, &acct.ParentID, &acct.IsActive, &acct.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("repository: get gl account: %w", err)
	}
	return &acct, nil
}

// ListGLAccounts returns all GL accounts ordered by code.
func (r *Repository) ListGLAccounts(ctx context.Context) ([]*GLAccount, error) {
	query := `SELECT id, code, name, account_class, normal_balance, parent_id, is_active, created_at
		FROM gl_accounts ORDER BY code`

	rows, err := r.db.Pool.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repository: list gl accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*GLAccount
	for rows.Next() {
		var acct GLAccount
		if err := rows.Scan(
			&acct.ID, &acct.Code, &acct.Name, &acct.AccountClass,
			&acct.NormalBalance, &acct.ParentID, &acct.IsActive, &acct.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repository: scan gl account: %w", err)
		}
		accounts = append(accounts, &acct)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: iterate gl accounts: %w", err)
	}
	return accounts, nil
}

// CreateJournalEntry inserts a journal entry within the provided transaction.
// The idempotency_key column has a UNIQUE constraint to prevent double-posting.
func (r *Repository) CreateJournalEntry(ctx context.Context, tx *sql.Tx, je *JournalEntry) error {
	if je.ID == "" {
		je.ID = uuid.New().String()
	}
	je.PostedAt = time.Now().UTC()

	query := `INSERT INTO journal_entries (id, reference_number, description, posted_by, posted_at, fiscal_period, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := tx.ExecContext(ctx, query,
		je.ID, je.ReferenceNumber, je.Description, je.PostedBy,
		je.PostedAt, je.FiscalPeriod, je.IdempotencyKey,
	)
	if err != nil {
		return fmt.Errorf("repository: create journal entry: %w", err)
	}
	return nil
}

// CreateGLEntry inserts an immutable GL entry within the provided transaction.
// GL entries are INSERT-ONLY; updates and deletes are prohibited at the DB level.
func (r *Repository) CreateGLEntry(ctx context.Context, tx *sql.Tx, entry *GLEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	entry.CreatedAt = time.Now().UTC()

	query := `INSERT INTO gl_entries (id, journal_entry_id, gl_account_id, amount, entry_type, effective_date, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := tx.ExecContext(ctx, query,
		entry.ID, entry.JournalEntryID, entry.GLAccountID,
		entry.Amount, entry.EntryType, entry.EffectiveDate, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("repository: create gl entry: %w", err)
	}
	return nil
}

// GetTrialBalance aggregates balances from gl_entries grouped by GL account,
// respecting the normal_balance direction for each account.
func (r *Repository) GetTrialBalance(ctx context.Context) ([]*GLBalanceResponse, error) {
	query := `SELECT
		gla.id,
		gla.code,
		gla.name,
		COALESCE(SUM(
			CASE
				WHEN gla.normal_balance = 'DEBIT'  AND gle.entry_type = 'DEBIT'  THEN  gle.amount
				WHEN gla.normal_balance = 'DEBIT'  AND gle.entry_type = 'CREDIT' THEN -gle.amount
				WHEN gla.normal_balance = 'CREDIT' AND gle.entry_type = 'CREDIT' THEN  gle.amount
				WHEN gla.normal_balance = 'CREDIT' AND gle.entry_type = 'DEBIT'  THEN -gle.amount
				ELSE 0
			END
		), 0) AS balance,
		gla.account_class::text
		FROM gl_accounts gla
		LEFT JOIN gl_entries gle ON gle.gl_account_id = gla.id
		GROUP BY gla.id, gla.code, gla.name, gla.account_class
		ORDER BY gla.code`

	rows, err := r.db.Pool.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repository: get trial balance: %w", err)
	}
	defer rows.Close()

	var balances []*GLBalanceResponse
	for rows.Next() {
		var b GLBalanceResponse
		if err := rows.Scan(&b.GLAccountID, &b.Code, &b.Name, &b.Balance, &b.AccountClass); err != nil {
			return nil, fmt.Errorf("repository: scan trial balance: %w", err)
		}
		balances = append(balances, &b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: iterate trial balance: %w", err)
	}
	return balances, nil
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
