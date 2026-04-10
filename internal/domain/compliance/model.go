package compliance

import (
	"time"

	"github.com/shopspring/decimal"
)

type AlertType string

const (
	AlertCTR         AlertType = "CTR"
	AlertSAR         AlertType = "SAR"
	AlertVelocity    AlertType = "VELOCITY"
	AlertStructuring AlertType = "STRUCTURING"
)

type AlertSeverity string

const (
	SeverityLow      AlertSeverity = "LOW"
	SeverityMedium   AlertSeverity = "MEDIUM"
	SeverityHigh     AlertSeverity = "HIGH"
	SeverityCritical AlertSeverity = "CRITICAL"
)

type AlertStatus string

const (
	AlertOpen          AlertStatus = "OPEN"
	AlertInvestigating AlertStatus = "INVESTIGATING"
	AlertEscalated     AlertStatus = "ESCALATED"
	AlertClosed        AlertStatus = "CLOSED"
	AlertFiled         AlertStatus = "FILED"
)

type AMLAlert struct {
	ID             string
	AccountID      string
	AlertType      AlertType
	Severity       AlertSeverity
	TransactionIDs []string
	TotalAmount    decimal.Decimal
	Status         AlertStatus
	AssignedTo     *string
	CreatedAt      time.Time
	ResolvedAt     *time.Time
}

type TransactionVelocity struct {
	AccountID   string
	WindowStart time.Time
	WindowEnd   time.Time
	TxCount     int
	TotalAmount decimal.Decimal
	CreatedAt   time.Time
}

// CTRThreshold is $10,000 in minor units (cents * 100 for DECIMAL(19,4)).
const CTRThreshold = 10000_0000
