package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/akampuraa-cmd/Nkore-Bank/internal/outbox"
	"github.com/akampuraa-cmd/Nkore-Bank/internal/platform/database"
)

// Sentinel errors for the ledger domain.
var (
	ErrUnbalancedEntry   = errors.New("journal entry is unbalanced: debits must equal credits")
	ErrTrialImbalance    = errors.New("trial balance imbalance: assets != liabilities + equity")
	ErrNoEntries         = errors.New("journal entry must have at least one GL entry")
	ErrGLAccountNotFound = errors.New("GL account not found")
)

// Service implements general-ledger business logic.
type Service struct {
	repo *Repository
	db   *database.DB
}

// NewService creates a new ledger service.
func NewService(repo *Repository, db *database.DB) *Service {
	return &Service{repo: repo, db: db}
}

// PostJournalEntry validates that debits equal credits, then atomically creates
// the journal entry, all GL entries, and an outbox event inside a transaction.
func (s *Service) PostJournalEntry(ctx context.Context, je *JournalEntry, entries []*GLEntry) error {
	if len(entries) == 0 {
		return ErrNoEntries
	}

	// Validate debits == credits.
	var totalDebits, totalCredits decimal.Decimal
	for _, e := range entries {
		switch e.EntryType {
		case string(NormalDebit):
			totalDebits = totalDebits.Add(e.Amount)
		case string(NormalCredit):
			totalCredits = totalCredits.Add(e.Amount)
		}
	}
	if !totalDebits.Equal(totalCredits) {
		return fmt.Errorf("%w: debits=%s credits=%s", ErrUnbalancedEntry, totalDebits.String(), totalCredits.String())
	}

	return s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
		if err := s.repo.CreateJournalEntry(ctx, tx, je); err != nil {
			return fmt.Errorf("post journal entry: %w", err)
		}

		for _, entry := range entries {
			entry.JournalEntryID = je.ID
			if err := s.repo.CreateGLEntry(ctx, tx, entry); err != nil {
				return fmt.Errorf("post gl entry: %w", err)
			}
		}

		payload, err := json.Marshal(map[string]string{
			"journal_entry_id": je.ID,
			"reference_number": je.ReferenceNumber,
			"total_debits":     totalDebits.String(),
			"total_credits":    totalCredits.String(),
		})
		if err != nil {
			return fmt.Errorf("marshal outbox payload: %w", err)
		}

		event := &outbox.OutboxEvent{
			AggregateType: "JournalEntry",
			AggregateID:   je.ID,
			EventType:     "JournalEntryPosted",
			Payload:       payload,
		}
		if err := s.repo.InsertOutboxEvent(ctx, tx, event); err != nil {
			return fmt.Errorf("insert outbox event: %w", err)
		}

		return nil
	})
}

// GetTrialBalance retrieves the trial balance and verifies the accounting equation:
// Assets = Liabilities + Equity.
func (s *Service) GetTrialBalance(ctx context.Context) ([]*GLBalanceResponse, error) {
	balances, err := s.repo.GetTrialBalance(ctx)
	if err != nil {
		return nil, fmt.Errorf("service: get trial balance: %w", err)
	}

	var assets, liabilities, equity decimal.Decimal
	for _, b := range balances {
		bal, parseErr := decimal.NewFromString(b.Balance)
		if parseErr != nil {
			continue
		}
		switch AccountClass(b.AccountClass) {
		case ClassAsset:
			assets = assets.Add(bal)
		case ClassLiability:
			liabilities = liabilities.Add(bal)
		case ClassEquity:
			equity = equity.Add(bal)
		}
	}

	if !assets.Equal(liabilities.Add(equity)) {
		return balances, fmt.Errorf("%w: assets=%s liabilities+equity=%s",
			ErrTrialImbalance, assets.String(), liabilities.Add(equity).String())
	}

	return balances, nil
}

// SeedChartOfAccounts creates the standard banking GL accounts if they do not
// already exist. Each account is created in its own transaction.
func (s *Service) SeedChartOfAccounts(ctx context.Context) error {
	accounts := []GLAccount{
		{Code: "1000", Name: "Cash", AccountClass: ClassAsset, NormalBalance: NormalDebit, IsActive: true},
		{Code: "1100", Name: "Loans Receivable", AccountClass: ClassAsset, NormalBalance: NormalDebit, IsActive: true},
		{Code: "2000", Name: "Customer Deposits", AccountClass: ClassLiability, NormalBalance: NormalCredit, IsActive: true},
		{Code: "2100", Name: "Accrued Interest Payable", AccountClass: ClassLiability, NormalBalance: NormalCredit, IsActive: true},
		{Code: "3000", Name: "Retained Earnings", AccountClass: ClassEquity, NormalBalance: NormalCredit, IsActive: true},
		{Code: "4000", Name: "Interest Income", AccountClass: ClassRevenue, NormalBalance: NormalCredit, IsActive: true},
		{Code: "4100", Name: "Fee Income", AccountClass: ClassRevenue, NormalBalance: NormalCredit, IsActive: true},
		{Code: "5000", Name: "Interest Expense", AccountClass: ClassExpense, NormalBalance: NormalDebit, IsActive: true},
		{Code: "5100", Name: "Operating Expense", AccountClass: ClassExpense, NormalBalance: NormalDebit, IsActive: true},
	}

	for i := range accounts {
		acct := &accounts[i]

		// Skip if already exists.
		existing, err := s.repo.GetGLAccount(ctx, acct.Code)
		if err != nil {
			return fmt.Errorf("seed chart of accounts: check %s: %w", acct.Code, err)
		}
		if existing != nil {
			continue
		}

		err = s.db.RunInTx(ctx, nil, func(tx *sql.Tx) error {
			return s.repo.CreateGLAccount(ctx, tx, acct)
		})
		if err != nil {
			return fmt.Errorf("seed chart of accounts: create %s: %w", acct.Code, err)
		}
	}

	now := time.Now().UTC()
	_ = now // seeding complete
	return nil
}
