package interest

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

var (
	daysActual365 = decimal.NewFromInt(365)
	daysThirty360 = decimal.NewFromInt(360)
)

// Service implements interest accrual and posting business logic.
type Service struct {
	db *database.DB
}

// NewService creates a new interest service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// accountBalance holds the data needed for daily accrual calculations.
type accountBalance struct {
	AccountID   string
	Balance     decimal.Decimal
	ProductType string
}

// AccrueDaily calculates and records daily interest for all active SAVINGS accounts.
// All arithmetic uses decimal.Decimal — float64 is never used for monetary values.
func (s *Service) AccrueDaily(ctx context.Context, date time.Time) error {
	// 1. Query all active SAVINGS accounts with their current balances.
	accounts, err := s.getActiveSavingsBalances(ctx)
	if err != nil {
		return fmt.Errorf("accrue daily: %w", err)
	}

	for _, acct := range accounts {
		// 2. Get the current interest rate for the product type.
		rate, convention, err := s.getCurrentRate(ctx, acct.ProductType, date)
		if err != nil {
			return fmt.Errorf("accrue daily: get rate for %s: %w", acct.AccountID, err)
		}
		if rate == nil {
			continue // no active rate for this product
		}

		// 3. Calculate daily interest based on day-count convention.
		dailyRate := s.calculateDailyRate(*rate, convention)
		accruedAmount := acct.Balance.Mul(dailyRate)

		// Round to 4 decimal places (DECIMAL(19,4)).
		accruedAmount = accruedAmount.Round(4)

		if accruedAmount.IsZero() {
			continue
		}

		// 4. Insert interest_accrual record.
		accrual := &InterestAccrual{
			ID:                 uuid.New().String(),
			AccountID:          acct.AccountID,
			AccrualDate:        date,
			Principal:          acct.Balance,
			Rate:               *rate,
			AccruedAmount:      accruedAmount,
			DayCountConvention: convention,
			Posted:             false,
			CreatedAt:          time.Now().UTC(),
		}

		if err := s.insertAccrual(ctx, accrual); err != nil {
			return fmt.Errorf("accrue daily: insert accrual for %s: %w", acct.AccountID, err)
		}
	}

	return nil
}

// PostAccruedInterest collects all unposted accruals through the given date, creates
// INTEREST transactions crediting each customer account, and marks accruals as posted.
// Everything executes in a single database transaction.
func (s *Service) PostAccruedInterest(ctx context.Context, throughDate time.Time) error {
	return s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		// 1. Get all unposted accruals through the date.
		accruals, err := s.getUnpostedAccruals(ctx, tx, throughDate)
		if err != nil {
			return fmt.Errorf("post accrued interest: %w", err)
		}

		for _, accrual := range accruals {
			// 2. Create an INTEREST transaction crediting the customer account.
			txnID := uuid.New().String()
			ref := "INT" + accrual.AccrualDate.Format("20060102") + txnID[:8]
			now := time.Now().UTC()

			insertTxn := `INSERT INTO transactions (id, idempotency_key, transaction_type, status, reference_number, description, created_at, settled_at)
				VALUES ($1, $2, 'INTEREST', 'SETTLED', $3, $4, $5, $6)`

			_, err := tx.ExecContext(ctx, insertTxn,
				txnID, uuid.New().String(), ref,
				fmt.Sprintf("Interest posting for %s on %s", accrual.AccountID, accrual.AccrualDate.Format("2006-01-02")),
				now, now,
			)
			if err != nil {
				return fmt.Errorf("post accrued interest: create transaction: %w", err)
			}

			// CREDIT entry for the customer account.
			entryID := uuid.New().String()
			insertEntry := `INSERT INTO transaction_entries (id, transaction_id, account_id, entry_type, amount, running_balance, currency, created_at)
				VALUES ($1, $2, $3, 'CREDIT', $4, $5, 'UGX', $6)`

			_, err = tx.ExecContext(ctx, insertEntry,
				entryID, txnID, accrual.AccountID, accrual.AccruedAmount,
				decimal.Zero, now,
			)
			if err != nil {
				return fmt.Errorf("post accrued interest: create entry: %w", err)
			}

			// DEBIT entry for the interest expense GL.
			debitEntryID := uuid.New().String()
			_, err = tx.ExecContext(ctx, insertEntry,
				debitEntryID, txnID, "GL_INTEREST_EXPENSE", accrual.AccruedAmount,
				decimal.Zero, now,
			)
			if err != nil {
				return fmt.Errorf("post accrued interest: create debit entry: %w", err)
			}

			// 3. Mark accrual as posted.
			updateAccrual := `UPDATE interest_accrual SET posted = TRUE WHERE id = $1`
			_, err = tx.ExecContext(ctx, updateAccrual, accrual.ID)
			if err != nil {
				return fmt.Errorf("post accrued interest: mark posted: %w", err)
			}
		}

		return nil
	})
}

// --- internal helpers ---

func (s *Service) getActiveSavingsBalances(ctx context.Context) ([]accountBalance, error) {
	query := `SELECT a.id, a.account_type,
		COALESCE(
			SUM(CASE WHEN te.entry_type = 'CREDIT' THEN te.amount ELSE decimal '0' END) -
			SUM(CASE WHEN te.entry_type = 'DEBIT' THEN te.amount ELSE decimal '0' END),
			decimal '0'
		) AS balance
		FROM accounts a
		LEFT JOIN transaction_entries te ON te.account_id = a.id
		WHERE a.status = 'ACTIVE' AND a.account_type = 'SAVINGS'
		GROUP BY a.id, a.account_type`

	rows, err := s.db.Pool.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get active savings balances: %w", err)
	}
	defer rows.Close()

	var accounts []accountBalance
	for rows.Next() {
		var ab accountBalance
		if err := rows.Scan(&ab.AccountID, &ab.ProductType, &ab.Balance); err != nil {
			return nil, fmt.Errorf("scan account balance: %w", err)
		}
		if ab.Balance.IsPositive() {
			accounts = append(accounts, ab)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account balances: %w", err)
	}
	return accounts, nil
}

func (s *Service) getCurrentRate(ctx context.Context, productType string, date time.Time) (*decimal.Decimal, DayCountConvention, error) {
	query := `SELECT rate FROM interest_rates
		WHERE product_type = $1
		AND effective_from <= $2
		AND (effective_to IS NULL OR effective_to >= $2)
		ORDER BY effective_from DESC
		LIMIT 1`

	var rate decimal.Decimal
	err := s.db.Pool.QueryRowContext(ctx, query, productType, date).Scan(&rate)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get current rate: %w", err)
	}

	// Default to ACTUAL_365 for savings products.
	return &rate, Actual365, nil
}

func (s *Service) calculateDailyRate(annualRate decimal.Decimal, convention DayCountConvention) decimal.Decimal {
	switch convention {
	case Thirty360:
		return annualRate.Div(daysThirty360)
	default: // Actual365
		return annualRate.Div(daysActual365)
	}
}

func (s *Service) insertAccrual(ctx context.Context, accrual *InterestAccrual) error {
	query := `INSERT INTO interest_accrual (id, account_id, accrual_date, principal, rate, day_count_convention, accrued_amount, posted, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := s.db.Pool.ExecContext(ctx, query,
		accrual.ID, accrual.AccountID, accrual.AccrualDate,
		accrual.Principal, accrual.Rate, string(accrual.DayCountConvention),
		accrual.AccruedAmount, accrual.Posted, accrual.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert accrual: %w", err)
	}
	return nil
}

func (s *Service) getUnpostedAccruals(ctx context.Context, tx *sql.Tx, throughDate time.Time) ([]*InterestAccrual, error) {
	query := `SELECT id, account_id, accrual_date, principal, rate, accrued_amount, day_count_convention, posted, created_at
		FROM interest_accrual
		WHERE posted = FALSE AND accrual_date <= $1
		ORDER BY accrual_date, account_id`

	rows, err := tx.QueryContext(ctx, query, throughDate)
	if err != nil {
		return nil, fmt.Errorf("get unposted accruals: %w", err)
	}
	defer rows.Close()

	var accruals []*InterestAccrual
	for rows.Next() {
		var a InterestAccrual
		if err := rows.Scan(
			&a.ID, &a.AccountID, &a.AccrualDate, &a.Principal,
			&a.Rate, &a.AccruedAmount, &a.DayCountConvention, &a.Posted, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan accrual: %w", err)
		}
		accruals = append(accruals, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accruals: %w", err)
	}
	return accruals, nil
}
