package account

import (
	"time"

	"github.com/shopspring/decimal"
)

type AccountType string

const (
	AccountTypeDDA     AccountType = "DDA"
	AccountTypeSavings AccountType = "SAVINGS"
	AccountTypeLoan    AccountType = "LOAN"
)

type AccountStatus string

const (
	StatusActive  AccountStatus = "ACTIVE"
	StatusFrozen  AccountStatus = "FROZEN"
	StatusClosed  AccountStatus = "CLOSED"
	StatusDormant AccountStatus = "DORMANT"
)

type KYCStatus string

const (
	KYCPending  KYCStatus = "PENDING"
	KYCVerified KYCStatus = "VERIFIED"
	KYCFailed   KYCStatus = "FAILED"
)

type Customer struct {
	ID           string
	FirstNameEnc string
	LastNameEnc  string
	EmailEnc     string
	PhoneEnc     string
	SSNHash      string
	KYCStatus    KYCStatus
	RiskScore    int
	CreatedAt    time.Time
}

type Account struct {
	ID            string
	AccountNumber string
	CustomerID    string
	AccountType   AccountType
	Currency      string
	Status        AccountStatus
	DailyLimit    decimal.Decimal
	Version       int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type AccountHold struct {
	ID         string
	AccountID  string
	HoldType   string
	Amount     decimal.Decimal
	ExpiresAt  *time.Time
	ReleasedAt *time.Time
	CreatedAt  time.Time
}

// Request/Response DTOs

type CreateAccountRequest struct {
	CustomerID  string
	AccountType string
	Currency    string
	DailyLimit  string
}

type CreateAccountResponse struct {
	ID            string
	AccountNumber string
	Status        string
}

type AccountBalanceResponse struct {
	AccountID        string
	AccountNumber    string
	AvailableBalance string
	CurrentBalance   string
	HoldAmount       string
}
