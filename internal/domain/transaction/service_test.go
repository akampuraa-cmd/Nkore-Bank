package transaction

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestValidateDepositRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *DepositRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: &DepositRequest{
				AccountID:      "acc-123",
				Amount:         "100.50",
				Currency:       "USD",
				Description:    "Test deposit",
				IdempotencyKey: "key-1",
			},
			wantErr: false,
		},
		{
			name: "missing account_id",
			req: &DepositRequest{
				Amount:         "100.00",
				Currency:       "USD",
				IdempotencyKey: "key-2",
			},
			wantErr: true,
		},
		{
			name: "missing amount",
			req: &DepositRequest{
				AccountID:      "acc-123",
				Currency:       "USD",
				IdempotencyKey: "key-3",
			},
			wantErr: true,
		},
		{
			name: "negative amount",
			req: &DepositRequest{
				AccountID:      "acc-123",
				Amount:         "-50.00",
				Currency:       "USD",
				IdempotencyKey: "key-4",
			},
			wantErr: true,
		},
		{
			name: "zero amount",
			req: &DepositRequest{
				AccountID:      "acc-123",
				Amount:         "0",
				Currency:       "USD",
				IdempotencyKey: "key-5",
			},
			wantErr: true,
		},
		{
			name: "invalid amount format",
			req: &DepositRequest{
				AccountID:      "acc-123",
				Amount:         "not-a-number",
				Currency:       "USD",
				IdempotencyKey: "key-6",
			},
			wantErr: true,
		},
		{
			name: "missing currency",
			req: &DepositRequest{
				AccountID:      "acc-123",
				Amount:         "100.00",
				IdempotencyKey: "key-7",
			},
			wantErr: true,
		},
		{
			name: "missing idempotency_key",
			req: &DepositRequest{
				AccountID: "acc-123",
				Amount:    "100.00",
				Currency:  "USD",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDepositRequest(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDepositRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateWithdrawalRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *WithdrawalRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: &WithdrawalRequest{
				AccountID:      "acc-123",
				Amount:         "50.00",
				Currency:       "USD",
				IdempotencyKey: "key-1",
			},
			wantErr: false,
		},
		{
			name: "missing account_id",
			req: &WithdrawalRequest{
				Amount:         "50.00",
				Currency:       "USD",
				IdempotencyKey: "key-2",
			},
			wantErr: true,
		},
		{
			name: "zero amount",
			req: &WithdrawalRequest{
				AccountID:      "acc-123",
				Amount:         "0.00",
				Currency:       "USD",
				IdempotencyKey: "key-3",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWithdrawalRequest(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWithdrawalRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTransferRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *TransferRequest
		wantErr bool
	}{
		{
			name: "valid transfer",
			req: &TransferRequest{
				FromAccountID:  "acc-1",
				ToAccountID:    "acc-2",
				Amount:         "250.00",
				Currency:       "USD",
				IdempotencyKey: "key-1",
			},
			wantErr: false,
		},
		{
			name: "same account transfer",
			req: &TransferRequest{
				FromAccountID:  "acc-1",
				ToAccountID:    "acc-1",
				Amount:         "100.00",
				Currency:       "USD",
				IdempotencyKey: "key-2",
			},
			wantErr: true,
		},
		{
			name: "missing from_account_id",
			req: &TransferRequest{
				ToAccountID:    "acc-2",
				Amount:         "100.00",
				Currency:       "USD",
				IdempotencyKey: "key-3",
			},
			wantErr: true,
		},
		{
			name: "missing to_account_id",
			req: &TransferRequest{
				FromAccountID:  "acc-1",
				Amount:         "100.00",
				Currency:       "USD",
				IdempotencyKey: "key-4",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTransferRequest(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTransferRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildResponse(t *testing.T) {
	txn := &Transaction{
		ID:              "tx-001",
		IdempotencyKey:  "idem-001",
		Type:            TxDeposit,
		Status:          TxSettled,
		ReferenceNumber: "TXN20240101000001",
		Description:     "Test deposit",
	}

	entries := []*TransactionEntry{
		{
			ID:             "entry-1",
			TransactionID:  "tx-001",
			AccountID:      "acc-1",
			EntryType:      EntryCredit,
			Amount:         decimal.NewFromInt(10000),
			RunningBalance: decimal.NewFromInt(10000),
			Currency:       "USD",
		},
		{
			ID:            "entry-2",
			TransactionID: "tx-001",
			AccountID:     "gl-cash",
			EntryType:     EntryDebit,
			Amount:        decimal.NewFromInt(10000),
			Currency:      "USD",
		},
	}

	resp := buildResponse(txn, entries)

	if resp.ID != "tx-001" {
		t.Errorf("expected ID tx-001, got %s", resp.ID)
	}
	if resp.Status != string(TxSettled) {
		t.Errorf("expected status SETTLED, got %s", resp.Status)
	}
	if resp.Type != string(TxDeposit) {
		t.Errorf("expected type DEPOSIT, got %s", resp.Type)
	}
	if len(resp.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.Entries[0].EntryType != string(EntryCredit) {
		t.Errorf("expected first entry CREDIT, got %s", resp.Entries[0].EntryType)
	}
}

// TestDecimalPrecision verifies that monetary calculations never lose precision.
func TestDecimalPrecision(t *testing.T) {
	// $10.99 represented precisely
	amount := decimal.NewFromFloat(10.99)
	expected := "10.99"
	if amount.StringFixed(2) != expected {
		t.Errorf("precision loss: expected %s, got %s", expected, amount.StringFixed(2))
	}

	// Verify adding small amounts doesn't lose precision
	sum := decimal.Zero
	increment := decimal.NewFromFloat(0.01)
	for i := 0; i < 1000; i++ {
		sum = sum.Add(increment)
	}
	if sum.StringFixed(2) != "10.00" {
		t.Errorf("accumulated precision loss: expected 10.00, got %s", sum.StringFixed(2))
	}

	// Verify interest calculation precision
	principal := decimal.NewFromFloat(100000.00)
	rate := decimal.NewFromFloat(0.0525) // 5.25%
	days := decimal.NewFromInt(365)
	dailyInterest := principal.Mul(rate).Div(days)
	// Should be ~14.3835616438...
	if dailyInterest.Round(4).String() != "14.3836" {
		t.Errorf("interest precision: expected 14.3836, got %s", dailyInterest.Round(4).String())
	}
}
