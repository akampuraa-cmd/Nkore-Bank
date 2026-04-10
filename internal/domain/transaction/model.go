package transaction

import (
	"time"

	"github.com/shopspring/decimal"
)

type TransactionType string

const (
	TxDeposit    TransactionType = "DEPOSIT"
	TxWithdrawal TransactionType = "WITHDRAWAL"
	TxTransfer   TransactionType = "TRANSFER"
	TxFee        TransactionType = "FEE"
	TxInterest   TransactionType = "INTEREST"
	TxReversal   TransactionType = "REVERSAL"
)

type TransactionStatus string

const (
	TxPending  TransactionStatus = "PENDING"
	TxSettled  TransactionStatus = "SETTLED"
	TxFailed   TransactionStatus = "FAILED"
	TxReversed TransactionStatus = "REVERSED"
)

type EntryType string

const (
	EntryDebit  EntryType = "DEBIT"
	EntryCredit EntryType = "CREDIT"
)

type Transaction struct {
	ID              string
	IdempotencyKey  string
	Type            TransactionType
	Status          TransactionStatus
	ReferenceNumber string
	Description     string
	CreatedAt       time.Time
	SettledAt       *time.Time
}

type TransactionEntry struct {
	ID             string
	TransactionID  string
	AccountID      string
	EntryType      EntryType
	Amount         decimal.Decimal
	RunningBalance decimal.Decimal
	Currency       string
	CreatedAt      time.Time
}

// Request/Response DTOs

type DepositRequest struct {
	AccountID      string
	Amount         string
	Currency       string
	Description    string
	IdempotencyKey string
}

type WithdrawalRequest struct {
	AccountID      string
	Amount         string
	Currency       string
	Description    string
	IdempotencyKey string
}

type TransferRequest struct {
	FromAccountID  string
	ToAccountID    string
	Amount         string
	Currency       string
	Description    string
	IdempotencyKey string
}

type TransactionResponse struct {
	ID              string
	ReferenceNumber string
	Status          string
	Type            string
	Entries         []EntryResponse
}

type EntryResponse struct {
	AccountID      string
	EntryType      string
	Amount         string
	RunningBalance string
	Currency       string
}
