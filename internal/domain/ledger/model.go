package ledger

import (
	"time"

	"github.com/shopspring/decimal"
)

type AccountClass string

const (
	ClassAsset     AccountClass = "ASSET"
	ClassLiability AccountClass = "LIABILITY"
	ClassEquity    AccountClass = "EQUITY"
	ClassRevenue   AccountClass = "REVENUE"
	ClassExpense   AccountClass = "EXPENSE"
)

type NormalBalance string

const (
	NormalDebit  NormalBalance = "DEBIT"
	NormalCredit NormalBalance = "CREDIT"
)

type GLAccount struct {
	ID            string
	Code          string
	Name          string
	AccountClass  AccountClass
	NormalBalance NormalBalance
	ParentID      *string
	IsActive      bool
	CreatedAt     time.Time
}

type JournalEntry struct {
	ID              string
	ReferenceNumber string
	Description     string
	PostedBy        string
	PostedAt        time.Time
	FiscalPeriod    string
	IdempotencyKey  string
}

type GLEntry struct {
	ID             string
	JournalEntryID string
	GLAccountID    string
	Amount         decimal.Decimal
	EntryType      string
	EffectiveDate  time.Time
	CreatedAt      time.Time
}

type GLBalanceResponse struct {
	GLAccountID  string
	Code         string
	Name         string
	Balance      string
	AccountClass string
}
