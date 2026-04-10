package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/outbox"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/cache"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

// Sentinel errors for the account domain.
var (
	ErrAccountNotFound   = errors.New("account not found")
	ErrVersionConflict   = errors.New("account version conflict")
	ErrAccountNotActive  = errors.New("account is not active")
	ErrNonZeroBalance    = errors.New("account has non-zero balance")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrInvalidInput      = errors.New("invalid input")
	ErrHoldNotFound      = errors.New("hold not found or already released")
)

// Service implements account business logic.
type Service struct {
	repo  *Repository
	db    *database.DB
	cache *cache.Client
}

// NewService creates a new account service.
func NewService(repo *Repository, db *database.DB, cache *cache.Client) *Service {
	return &Service{repo: repo, db: db, cache: cache}
}

var validCurrencies = map[string]bool{
	"USD": true, "EUR": true, "GBP": true,
	"UGX": true, "KES": true, "TZS": true,
}

var validAccountTypes = map[string]bool{
	string(AccountTypeDDA):     true,
	string(AccountTypeSavings): true,
	string(AccountTypeLoan):    true,
}

// CreateAccount validates the request, persists a new account inside a
// transaction together with a transactional outbox event, and returns the
// creation response.
func (s *Service) CreateAccount(ctx context.Context, req *CreateAccountRequest) (*CreateAccountResponse, error) {
	if req.CustomerID == "" {
		return nil, fmt.Errorf("%w: customer_id is required", ErrInvalidInput)
	}
	if !validAccountTypes[req.AccountType] {
		return nil, fmt.Errorf("%w: invalid account type", ErrInvalidInput)
	}
	if !validCurrencies[req.Currency] {
		return nil, fmt.Errorf("%w: invalid currency", ErrInvalidInput)
	}

	dailyLimit, err := decimal.NewFromString(req.DailyLimit)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid daily_limit", ErrInvalidInput)
	}

	acct := &Account{
		CustomerID:  req.CustomerID,
		AccountType: AccountType(req.AccountType),
		Currency:    req.Currency,
		DailyLimit:  dailyLimit,
	}

	err = s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		if err := s.repo.Create(ctx, tx, acct); err != nil {
			return fmt.Errorf("create account: %w", err)
		}

		payload, err := json.Marshal(acct)
		if err != nil {
			return fmt.Errorf("marshal account payload: %w", err)
		}

		event := &outbox.OutboxEvent{
			AggregateType: "Account",
			AggregateID:   acct.ID,
			EventType:     "AccountCreated",
			Payload:       payload,
		}
		if err := s.repo.InsertOutboxEvent(ctx, tx, event); err != nil {
			return fmt.Errorf("insert outbox event: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("service: create account: %w", err)
	}

	return &CreateAccountResponse{
		ID:            acct.ID,
		AccountNumber: acct.AccountNumber,
		Status:        string(acct.Status),
	}, nil
}

// GetAccount returns an account, checking the cache before hitting the database.
func (s *Service) GetAccount(ctx context.Context, id string) (*Account, error) {
	cacheKey := "account:" + id
	cached, err := s.cache.Get(ctx, cacheKey)
	if err == nil && cached != "" {
		var acct Account
		if err := json.Unmarshal([]byte(cached), &acct); err == nil {
			return &acct, nil
		}
	}

	acct, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: get account: %w", err)
	}

	if data, err := json.Marshal(acct); err == nil {
		_ = s.cache.Set(ctx, cacheKey, string(data), 5*time.Minute)
	}

	return acct, nil
}

// GetBalance returns the computed balance details for an account.
func (s *Service) GetBalance(ctx context.Context, accountID string) (*AccountBalanceResponse, error) {
	acct, err := s.repo.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("service: get balance: %w", err)
	}

	currentBalance, availableBalance, holdAmount, err := s.repo.GetBalance(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("service: get balance: %w", err)
	}

	return &AccountBalanceResponse{
		AccountID:        acct.ID,
		AccountNumber:    acct.AccountNumber,
		AvailableBalance: availableBalance.StringFixed(2),
		CurrentBalance:   currentBalance.StringFixed(2),
		HoldAmount:       holdAmount.StringFixed(2),
	}, nil
}

// FreezeAccount transitions an active account to the FROZEN status using
// optimistic locking and emits an AccountFrozen outbox event.
func (s *Service) FreezeAccount(ctx context.Context, id string) error {
	err := s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		var acct Account
		dest := []any{
			&acct.ID, &acct.AccountNumber, &acct.CustomerID, &acct.AccountType,
			&acct.Currency, &acct.Status, &acct.DailyLimit, &acct.Version,
			&acct.CreatedAt, &acct.UpdatedAt,
		}
		query := `SELECT id, account_number, customer_id, account_type, currency,
			status, daily_limit, version, created_at, updated_at
			FROM accounts WHERE id = $1`
		if err := database.SelectForUpdate(ctx, tx, query, dest, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAccountNotFound
			}
			return fmt.Errorf("select for update: %w", err)
		}

		if acct.Status != StatusActive {
			return ErrAccountNotActive
		}

		if err := s.repo.UpdateStatus(ctx, tx, id, StatusFrozen, acct.Version); err != nil {
			return fmt.Errorf("update status: %w", err)
		}

		payload, _ := json.Marshal(map[string]string{
			"account_id": id,
			"new_status": string(StatusFrozen),
		})
		event := &outbox.OutboxEvent{
			AggregateType: "Account",
			AggregateID:   id,
			EventType:     "AccountFrozen",
			Payload:       payload,
		}
		return s.repo.InsertOutboxEvent(ctx, tx, event)
	})
	if err != nil {
		return fmt.Errorf("service: freeze account: %w", err)
	}

	_ = s.cache.Delete(ctx, "account:"+id)
	return nil
}

// CloseAccount verifies the account has a zero balance, transitions it to
// CLOSED, and emits an AccountClosed outbox event.
func (s *Service) CloseAccount(ctx context.Context, id string) error {
	currentBalance, _, _, err := s.repo.GetBalance(ctx, id)
	if err != nil {
		return fmt.Errorf("service: close account: %w", err)
	}
	if !currentBalance.IsZero() {
		return fmt.Errorf("service: close account: %w", ErrNonZeroBalance)
	}

	err = s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		var acct Account
		dest := []any{
			&acct.ID, &acct.AccountNumber, &acct.CustomerID, &acct.AccountType,
			&acct.Currency, &acct.Status, &acct.DailyLimit, &acct.Version,
			&acct.CreatedAt, &acct.UpdatedAt,
		}
		query := `SELECT id, account_number, customer_id, account_type, currency,
			status, daily_limit, version, created_at, updated_at
			FROM accounts WHERE id = $1`
		if err := database.SelectForUpdate(ctx, tx, query, dest, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrAccountNotFound
			}
			return fmt.Errorf("select for update: %w", err)
		}

		if err := s.repo.UpdateStatus(ctx, tx, id, StatusClosed, acct.Version); err != nil {
			return fmt.Errorf("update status: %w", err)
		}

		payload, _ := json.Marshal(map[string]string{
			"account_id": id,
			"new_status": string(StatusClosed),
		})
		event := &outbox.OutboxEvent{
			AggregateType: "Account",
			AggregateID:   id,
			EventType:     "AccountClosed",
			Payload:       payload,
		}
		return s.repo.InsertOutboxEvent(ctx, tx, event)
	})
	if err != nil {
		return fmt.Errorf("service: close account: %w", err)
	}

	_ = s.cache.Delete(ctx, "account:"+id)
	return nil
}

// PlaceHold validates sufficient available funds and creates a hold within a
// transaction, emitting a HoldPlaced outbox event.
func (s *Service) PlaceHold(ctx context.Context, accountID, holdType string, amount decimal.Decimal) error {
	err := s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		_, availableBalance, _, err := s.repo.GetBalance(ctx, accountID)
		if err != nil {
			return fmt.Errorf("get balance: %w", err)
		}
		if availableBalance.LessThan(amount) {
			return ErrInsufficientFunds
		}

		hold := &AccountHold{
			AccountID: accountID,
			HoldType:  holdType,
			Amount:    amount,
		}
		if err := s.repo.CreateHold(ctx, tx, hold); err != nil {
			return fmt.Errorf("create hold: %w", err)
		}

		payload, _ := json.Marshal(map[string]string{
			"account_id": accountID,
			"hold_id":    hold.ID,
			"hold_type":  holdType,
			"amount":     amount.String(),
		})
		event := &outbox.OutboxEvent{
			AggregateType: "Account",
			AggregateID:   accountID,
			EventType:     "HoldPlaced",
			Payload:       payload,
		}
		return s.repo.InsertOutboxEvent(ctx, tx, event)
	})
	if err != nil {
		return fmt.Errorf("service: place hold: %w", err)
	}
	return nil
}
