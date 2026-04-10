package transaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/domain/account"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/outbox"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// Sentinel errors for the transaction domain.
var (
	ErrInvalidInput      = errors.New("invalid input")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrAccountNotActive  = errors.New("account is not active")
	ErrSameAccount       = errors.New("source and destination accounts must differ")
)

// Internal general-ledger account ID for the bank's cash position.
const glCashAccountID = "GL_CASH"

var tracer = otel.Tracer("nkore-bank/transaction")

// Service orchestrates transaction operations with double-entry bookkeeping.
type Service struct {
	repo    *Repository
	acctRepo *account.Repository
	db      *database.DB
	metrics *telemetry.BankingMetrics
}

// NewService creates a new transaction service.
func NewService(repo *Repository, acctRepo *account.Repository, db *database.DB, metrics *telemetry.BankingMetrics) *Service {
	return &Service{
		repo:    repo,
		acctRepo: acctRepo,
		db:      db,
		metrics: metrics,
	}
}

// Deposit processes a deposit into the given account with double-entry
// bookkeeping: DEBIT the bank's cash GL, CREDIT the customer account.
func (s *Service) Deposit(ctx context.Context, req *DepositRequest) (*TransactionResponse, error) {
	ctx, span := telemetry.StartSpan(ctx, tracer, "transaction.Deposit",
		attribute.String("account_id", req.AccountID),
	)
	defer span.End()

	if err := validateDepositRequest(req); err != nil {
		return nil, err
	}

	// Idempotency check.
	existing, err := s.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("deposit: idempotency check: %w", err)
	}
	if existing != nil {
		return s.fetchResponse(ctx, existing.ID)
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid amount format", ErrInvalidInput)
	}

	// Verify account exists and is active.
	acct, err := s.acctRepo.GetByID(ctx, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("deposit: %w", err)
	}
	if acct.Status != account.StatusActive {
		return nil, ErrAccountNotActive
	}

	var txnID string
	err = s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		// Pessimistic lock: get current balance with SELECT FOR UPDATE.
		currentBalance, lockErr := s.repo.GetCurrentBalance(ctx, tx, req.AccountID)
		if lockErr != nil {
			return fmt.Errorf("deposit: lock balance: %w", lockErr)
		}

		newBalance := currentBalance.Add(amount)

		// Create transaction record in PENDING state.
		txn := &Transaction{
			IdempotencyKey: req.IdempotencyKey,
			Type:           TxDeposit,
			Status:         TxPending,
			Description:    req.Description,
		}
		if err := s.repo.Create(ctx, tx, txn); err != nil {
			return fmt.Errorf("deposit: create transaction: %w", err)
		}
		txnID = txn.ID

		// Double-entry: DEBIT the cash GL (asset increase).
		debitEntry := &TransactionEntry{
			TransactionID:  txn.ID,
			AccountID:      glCashAccountID,
			EntryType:      EntryDebit,
			Amount:         amount,
			RunningBalance: decimal.Zero, // GL running balance not tracked per-entry.
			Currency:       req.Currency,
		}
		if err := s.repo.CreateEntry(ctx, tx, debitEntry); err != nil {
			return fmt.Errorf("deposit: create debit entry: %w", err)
		}

		// Double-entry: CREDIT the customer account (liability increase).
		creditEntry := &TransactionEntry{
			TransactionID:  txn.ID,
			AccountID:      req.AccountID,
			EntryType:      EntryCredit,
			Amount:         amount,
			RunningBalance: newBalance,
			Currency:       req.Currency,
		}
		if err := s.repo.CreateEntry(ctx, tx, creditEntry); err != nil {
			return fmt.Errorf("deposit: create credit entry: %w", err)
		}

		// Settle the transaction.
		if err := s.repo.UpdateStatus(ctx, tx, txn.ID, TxSettled); err != nil {
			return fmt.Errorf("deposit: settle: %w", err)
		}

		// Outbox event for downstream consumers.
		payload, _ := json.Marshal(map[string]string{
			"transaction_id": txn.ID,
			"account_id":     req.AccountID,
			"amount":         amount.String(),
			"currency":       req.Currency,
			"type":           string(TxDeposit),
		})
		if err := s.repo.InsertOutboxEvent(ctx, tx, &outbox.OutboxEvent{
			AggregateType: "Transaction",
			AggregateID:   txn.ID,
			EventType:     "TransactionSettled",
			Payload:       payload,
		}); err != nil {
			return fmt.Errorf("deposit: outbox event: %w", err)
		}

		return nil
	})
	if err != nil {
		s.recordMetrics(ctx, TxDeposit, "failed", amount)
		return nil, err
	}

	s.recordMetrics(ctx, TxDeposit, "settled", amount)
	return s.fetchResponse(ctx, txnID)
}

// Withdraw processes a withdrawal from the given account with double-entry
// bookkeeping: DEBIT the customer account, CREDIT the bank's cash GL.
func (s *Service) Withdraw(ctx context.Context, req *WithdrawalRequest) (*TransactionResponse, error) {
	ctx, span := telemetry.StartSpan(ctx, tracer, "transaction.Withdraw",
		attribute.String("account_id", req.AccountID),
	)
	defer span.End()

	if err := validateWithdrawalRequest(req); err != nil {
		return nil, err
	}

	existing, err := s.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("withdraw: idempotency check: %w", err)
	}
	if existing != nil {
		return s.fetchResponse(ctx, existing.ID)
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid amount format", ErrInvalidInput)
	}

	acct, err := s.acctRepo.GetByID(ctx, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("withdraw: %w", err)
	}
	if acct.Status != account.StatusActive {
		return nil, ErrAccountNotActive
	}

	var txnID string
	err = s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		currentBalance, lockErr := s.repo.GetCurrentBalance(ctx, tx, req.AccountID)
		if lockErr != nil {
			return fmt.Errorf("withdraw: lock balance: %w", lockErr)
		}

		if currentBalance.LessThan(amount) {
			return ErrInsufficientFunds
		}

		newBalance := currentBalance.Sub(amount)

		txn := &Transaction{
			IdempotencyKey: req.IdempotencyKey,
			Type:           TxWithdrawal,
			Status:         TxPending,
			Description:    req.Description,
		}
		if err := s.repo.Create(ctx, tx, txn); err != nil {
			return fmt.Errorf("withdraw: create transaction: %w", err)
		}
		txnID = txn.ID

		// DEBIT customer account (balance decreases).
		debitEntry := &TransactionEntry{
			TransactionID:  txn.ID,
			AccountID:      req.AccountID,
			EntryType:      EntryDebit,
			Amount:         amount,
			RunningBalance: newBalance,
			Currency:       req.Currency,
		}
		if err := s.repo.CreateEntry(ctx, tx, debitEntry); err != nil {
			return fmt.Errorf("withdraw: create debit entry: %w", err)
		}

		// CREDIT cash GL (asset decrease).
		creditEntry := &TransactionEntry{
			TransactionID:  txn.ID,
			AccountID:      glCashAccountID,
			EntryType:      EntryCredit,
			Amount:         amount,
			RunningBalance: decimal.Zero,
			Currency:       req.Currency,
		}
		if err := s.repo.CreateEntry(ctx, tx, creditEntry); err != nil {
			return fmt.Errorf("withdraw: create credit entry: %w", err)
		}

		if err := s.repo.UpdateStatus(ctx, tx, txn.ID, TxSettled); err != nil {
			return fmt.Errorf("withdraw: settle: %w", err)
		}

		payload, _ := json.Marshal(map[string]string{
			"transaction_id": txn.ID,
			"account_id":     req.AccountID,
			"amount":         amount.String(),
			"currency":       req.Currency,
			"type":           string(TxWithdrawal),
		})
		if err := s.repo.InsertOutboxEvent(ctx, tx, &outbox.OutboxEvent{
			AggregateType: "Transaction",
			AggregateID:   txn.ID,
			EventType:     "TransactionSettled",
			Payload:       payload,
		}); err != nil {
			return fmt.Errorf("withdraw: outbox event: %w", err)
		}

		return nil
	})
	if err != nil {
		s.recordMetrics(ctx, TxWithdrawal, "failed", amount)
		return nil, err
	}

	s.recordMetrics(ctx, TxWithdrawal, "settled", amount)
	return s.fetchResponse(ctx, txnID)
}

// Transfer moves funds between two accounts with double-entry bookkeeping:
// DEBIT the source account, CREDIT the destination account.
// Accounts are locked in lexicographic order to prevent deadlocks.
func (s *Service) Transfer(ctx context.Context, req *TransferRequest) (*TransactionResponse, error) {
	ctx, span := telemetry.StartSpan(ctx, tracer, "transaction.Transfer",
		attribute.String("from_account", req.FromAccountID),
		attribute.String("to_account", req.ToAccountID),
	)
	defer span.End()

	if err := validateTransferRequest(req); err != nil {
		return nil, err
	}

	existing, err := s.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("transfer: idempotency check: %w", err)
	}
	if existing != nil {
		return s.fetchResponse(ctx, existing.ID)
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid amount format", ErrInvalidInput)
	}

	// Verify both accounts exist and are active.
	fromAcct, err := s.acctRepo.GetByID(ctx, req.FromAccountID)
	if err != nil {
		return nil, fmt.Errorf("transfer: source account: %w", err)
	}
	if fromAcct.Status != account.StatusActive {
		return nil, fmt.Errorf("source %w", ErrAccountNotActive)
	}

	toAcct, err := s.acctRepo.GetByID(ctx, req.ToAccountID)
	if err != nil {
		return nil, fmt.Errorf("transfer: destination account: %w", err)
	}
	if toAcct.Status != account.StatusActive {
		return nil, fmt.Errorf("destination %w", ErrAccountNotActive)
	}

	var txnID string
	err = s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		// Lock accounts in consistent lexicographic order to prevent deadlocks.
		first, second := req.FromAccountID, req.ToAccountID
		if first > second {
			first, second = second, first
		}

		firstBal, lockErr := s.repo.GetCurrentBalance(ctx, tx, first)
		if lockErr != nil {
			return fmt.Errorf("transfer: lock first account: %w", lockErr)
		}
		secondBal, lockErr := s.repo.GetCurrentBalance(ctx, tx, second)
		if lockErr != nil {
			return fmt.Errorf("transfer: lock second account: %w", lockErr)
		}

		// Map balances back to source / destination.
		var sourceBalance, destBalance decimal.Decimal
		if first == req.FromAccountID {
			sourceBalance, destBalance = firstBal, secondBal
		} else {
			sourceBalance, destBalance = secondBal, firstBal
		}

		if sourceBalance.LessThan(amount) {
			return ErrInsufficientFunds
		}

		newSourceBalance := sourceBalance.Sub(amount)
		newDestBalance := destBalance.Add(amount)

		txn := &Transaction{
			IdempotencyKey: req.IdempotencyKey,
			Type:           TxTransfer,
			Status:         TxPending,
			Description:    req.Description,
		}
		if err := s.repo.Create(ctx, tx, txn); err != nil {
			return fmt.Errorf("transfer: create transaction: %w", err)
		}
		txnID = txn.ID

		// DEBIT source account (balance decreases).
		debitEntry := &TransactionEntry{
			TransactionID:  txn.ID,
			AccountID:      req.FromAccountID,
			EntryType:      EntryDebit,
			Amount:         amount,
			RunningBalance: newSourceBalance,
			Currency:       req.Currency,
		}
		if err := s.repo.CreateEntry(ctx, tx, debitEntry); err != nil {
			return fmt.Errorf("transfer: create debit entry: %w", err)
		}

		// CREDIT destination account (balance increases).
		creditEntry := &TransactionEntry{
			TransactionID:  txn.ID,
			AccountID:      req.ToAccountID,
			EntryType:      EntryCredit,
			Amount:         amount,
			RunningBalance: newDestBalance,
			Currency:       req.Currency,
		}
		if err := s.repo.CreateEntry(ctx, tx, creditEntry); err != nil {
			return fmt.Errorf("transfer: create credit entry: %w", err)
		}

		if err := s.repo.UpdateStatus(ctx, tx, txn.ID, TxSettled); err != nil {
			return fmt.Errorf("transfer: settle: %w", err)
		}

		payload, _ := json.Marshal(map[string]string{
			"transaction_id":     txn.ID,
			"from_account_id":    req.FromAccountID,
			"to_account_id":      req.ToAccountID,
			"amount":             amount.String(),
			"currency":           req.Currency,
			"type":               string(TxTransfer),
		})
		if err := s.repo.InsertOutboxEvent(ctx, tx, &outbox.OutboxEvent{
			AggregateType: "Transaction",
			AggregateID:   txn.ID,
			EventType:     "TransferCompleted",
			Payload:       payload,
		}); err != nil {
			return fmt.Errorf("transfer: outbox event: %w", err)
		}

		return nil
	})
	if err != nil {
		s.recordMetrics(ctx, TxTransfer, "failed", amount)
		return nil, err
	}

	s.recordMetrics(ctx, TxTransfer, "settled", amount)
	return s.fetchResponse(ctx, txnID)
}

// GetTransaction retrieves a transaction and its entries by ID.
func (s *Service) GetTransaction(ctx context.Context, id string) (*TransactionResponse, error) {
	ctx, span := telemetry.StartSpan(ctx, tracer, "transaction.GetTransaction",
		attribute.String("transaction_id", id),
	)
	defer span.End()

	return s.fetchResponse(ctx, id)
}

// GetStatement returns a paginated account statement for the given time range.
func (s *Service) GetStatement(ctx context.Context, accountID string, from, to time.Time, page, pageSize int) ([]*TransactionEntry, error) {
	ctx, span := telemetry.StartSpan(ctx, tracer, "transaction.GetStatement",
		attribute.String("account_id", accountID),
	)
	defer span.End()

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}

	offset := (page - 1) * pageSize
	return s.repo.GetAccountStatement(ctx, accountID, from, to, pageSize, offset)
}

// fetchResponse loads a transaction with its entries and builds the response DTO.
func (s *Service) fetchResponse(ctx context.Context, txnID string) (*TransactionResponse, error) {
	txn, entries, err := s.repo.GetByID(ctx, txnID)
	if err != nil {
		return nil, fmt.Errorf("fetch response: %w", err)
	}
	return buildResponse(txn, entries), nil
}

// buildResponse maps domain objects to the API response DTO.
func buildResponse(txn *Transaction, entries []*TransactionEntry) *TransactionResponse {
	resp := &TransactionResponse{
		ID:              txn.ID,
		ReferenceNumber: txn.ReferenceNumber,
		Status:          string(txn.Status),
		Type:            string(txn.Type),
	}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, EntryResponse{
			AccountID:      e.AccountID,
			EntryType:      string(e.EntryType),
			Amount:         e.Amount.String(),
			RunningBalance: e.RunningBalance.String(),
			Currency:       e.Currency,
		})
	}
	return resp
}

// recordMetrics increments Prometheus counters for the transaction.
func (s *Service) recordMetrics(ctx context.Context, txType TransactionType, status string, amount decimal.Decimal) {
	if s.metrics == nil {
		return
	}
	attrs := otelmetric.WithAttributes(
		attribute.String("type", string(txType)),
		attribute.String("status", status),
	)
	s.metrics.TransactionCount.Add(ctx, 1, attrs)
	amtFloat, _ := amount.Float64()
	s.metrics.TransactionAmountTotal.Add(ctx, amtFloat, attrs)
}

// ---------- Validation helpers ----------

func validateDepositRequest(req *DepositRequest) error {
	if req.AccountID == "" {
		return fmt.Errorf("%w: account_id is required", ErrInvalidInput)
	}
	if req.Amount == "" {
		return fmt.Errorf("%w: amount is required", ErrInvalidInput)
	}
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil || amt.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidInput)
	}
	if req.Currency == "" {
		return fmt.Errorf("%w: currency is required", ErrInvalidInput)
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency_key is required", ErrInvalidInput)
	}
	return nil
}

func validateWithdrawalRequest(req *WithdrawalRequest) error {
	if req.AccountID == "" {
		return fmt.Errorf("%w: account_id is required", ErrInvalidInput)
	}
	if req.Amount == "" {
		return fmt.Errorf("%w: amount is required", ErrInvalidInput)
	}
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil || amt.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidInput)
	}
	if req.Currency == "" {
		return fmt.Errorf("%w: currency is required", ErrInvalidInput)
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency_key is required", ErrInvalidInput)
	}
	return nil
}

func validateTransferRequest(req *TransferRequest) error {
	if req.FromAccountID == "" {
		return fmt.Errorf("%w: from_account_id is required", ErrInvalidInput)
	}
	if req.ToAccountID == "" {
		return fmt.Errorf("%w: to_account_id is required", ErrInvalidInput)
	}
	if req.FromAccountID == req.ToAccountID {
		return ErrSameAccount
	}
	if req.Amount == "" {
		return fmt.Errorf("%w: amount is required", ErrInvalidInput)
	}
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil || amt.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidInput)
	}
	if req.Currency == "" {
		return fmt.Errorf("%w: currency is required", ErrInvalidInput)
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency_key is required", ErrInvalidInput)
	}
	return nil
}
