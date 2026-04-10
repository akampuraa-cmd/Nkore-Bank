package compliance

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

const (
	// velocityWindowHours is the sliding window for velocity checks.
	velocityWindowHours = 24
	// velocityCountThreshold triggers a VELOCITY alert when exceeded.
	velocityCountThreshold = 10
	// velocityAmountThreshold in the same units as CTRThreshold.
	velocityAmountThreshold = 50000_0000
	// structuringMargin defines "just under" CTRThreshold (90% of threshold).
	structuringMargin = 9000_0000
	// structuringMinCount is the minimum number of near-threshold transactions
	// within the window to trigger a STRUCTURING alert.
	structuringMinCount = 3
)

// Service implements AML/compliance business logic.
type Service struct {
	db *database.DB
}

// NewService creates a new compliance service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

// ScreenTransaction checks a transaction against AML rules and creates alerts
// when thresholds are breached.
func (s *Service) ScreenTransaction(ctx context.Context, accountID string, amount decimal.Decimal, txIDs []string) error {
	now := time.Now().UTC()

	// Rule 1: Currency Transaction Report — single transaction >= $10,000.
	ctrThreshold := decimal.NewFromInt(CTRThreshold).Div(decimal.NewFromInt(10000))
	if amount.GreaterThanOrEqual(ctrThreshold) {
		if err := s.createAlert(ctx, accountID, AlertCTR, SeverityHigh, txIDs, amount, now); err != nil {
			return fmt.Errorf("screen transaction: ctr alert: %w", err)
		}
	}

	// Query recent transaction activity in the velocity window.
	windowStart := now.Add(-velocityWindowHours * time.Hour)
	txCount, totalAmount, err := s.getVelocity(ctx, accountID, windowStart, now)
	if err != nil {
		return fmt.Errorf("screen transaction: velocity query: %w", err)
	}

	// Rule 2: Velocity — too many transactions or cumulative amount too high.
	velocityAmtThreshold := decimal.NewFromInt(velocityAmountThreshold).Div(decimal.NewFromInt(10000))
	if txCount > velocityCountThreshold || totalAmount.GreaterThan(velocityAmtThreshold) {
		if err := s.createAlert(ctx, accountID, AlertVelocity, SeverityMedium, txIDs, totalAmount, now); err != nil {
			return fmt.Errorf("screen transaction: velocity alert: %w", err)
		}
	}

	// Rule 3: Structuring — multiple transactions just under the CTR threshold.
	structuringMin := decimal.NewFromInt(structuringMargin).Div(decimal.NewFromInt(10000))
	nearThresholdCount, err := s.getNearThresholdCount(ctx, accountID, windowStart, now, structuringMin, ctrThreshold)
	if err != nil {
		return fmt.Errorf("screen transaction: structuring query: %w", err)
	}
	if nearThresholdCount >= structuringMinCount {
		if err := s.createAlert(ctx, accountID, AlertStructuring, SeverityCritical, txIDs, totalAmount, now); err != nil {
			return fmt.Errorf("screen transaction: structuring alert: %w", err)
		}
	}

	return nil
}

// GetAlerts returns alerts filtered by status.
func (s *Service) GetAlerts(ctx context.Context, status AlertStatus) ([]*AMLAlert, error) {
	query := `SELECT id, account_id, alert_type, severity, transaction_ids, total_amount, status, assigned_to, created_at, resolved_at
		FROM aml_alerts WHERE status = $1
		ORDER BY created_at DESC`

	rows, err := s.db.Pool.QueryContext(ctx, query, string(status))
	if err != nil {
		return nil, fmt.Errorf("service: get alerts: %w", err)
	}
	defer rows.Close()

	var alerts []*AMLAlert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("service: iterate alerts: %w", err)
	}
	return alerts, nil
}

// UpdateAlertStatus transitions an alert's status and assigns it to an investigator.
func (s *Service) UpdateAlertStatus(ctx context.Context, alertID string, status AlertStatus, assignedTo string) error {
	query := `UPDATE aml_alerts SET status = $1, assigned_to = $2, updated_at = $3, resolved_at = $4
		WHERE id = $5`

	now := time.Now().UTC()
	var resolvedAt *time.Time
	if status == AlertClosed || status == AlertFiled {
		resolvedAt = &now
	}

	var assignedToPtr *string
	if assignedTo != "" {
		assignedToPtr = &assignedTo
	}

	result, err := s.db.Pool.ExecContext(ctx, query, string(status), assignedToPtr, now, resolvedAt, alertID)
	if err != nil {
		return fmt.Errorf("service: update alert status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("service: update alert status rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("service: alert not found: %s", alertID)
	}
	return nil
}

// --- internal helpers ---

func (s *Service) createAlert(ctx context.Context, accountID string, alertType AlertType, severity AlertSeverity, txIDs []string, totalAmount decimal.Decimal, now time.Time) error {
	id := uuid.New().String()
	query := `INSERT INTO aml_alerts (id, account_id, alert_type, severity, transaction_ids, total_amount, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := s.db.Pool.ExecContext(ctx, query,
		id, accountID, string(alertType), string(severity),
		pq.Array(txIDs), totalAmount, string(AlertOpen), now, now,
	)
	if err != nil {
		return fmt.Errorf("create alert: %w", err)
	}
	return nil
}

func (s *Service) getVelocity(ctx context.Context, accountID string, windowStart, windowEnd time.Time) (int, decimal.Decimal, error) {
	query := `SELECT COUNT(*), COALESCE(SUM(amount), 0)
		FROM transaction_entries
		WHERE account_id = $1 AND created_at >= $2 AND created_at <= $3`

	var count int
	var total decimal.Decimal
	err := s.db.Pool.QueryRowContext(ctx, query, accountID, windowStart, windowEnd).Scan(&count, &total)
	if err != nil {
		return 0, decimal.Zero, fmt.Errorf("get velocity: %w", err)
	}
	return count, total, nil
}

func (s *Service) getNearThresholdCount(ctx context.Context, accountID string, windowStart, windowEnd time.Time, minAmount, maxAmount decimal.Decimal) (int, error) {
	query := `SELECT COUNT(*)
		FROM transaction_entries
		WHERE account_id = $1
		AND created_at >= $2 AND created_at <= $3
		AND amount >= $4 AND amount < $5`

	var count int
	err := s.db.Pool.QueryRowContext(ctx, query, accountID, windowStart, windowEnd, minAmount, maxAmount).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get near threshold count: %w", err)
	}
	return count, nil
}

// scanAlert scans a single AMLAlert from a row set.
func scanAlert(rows interface{ Scan(dest ...any) error }) (*AMLAlert, error) {
	var a AMLAlert
	var txIDs pq.StringArray
	err := rows.Scan(
		&a.ID, &a.AccountID, &a.AlertType, &a.Severity,
		&txIDs, &a.TotalAmount, &a.Status, &a.AssignedTo,
		&a.CreatedAt, &a.ResolvedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan alert: %w", err)
	}
	a.TransactionIDs = []string(txIDs)
	return &a, nil
}
