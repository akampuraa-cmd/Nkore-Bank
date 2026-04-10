package interest

import (
	"time"

	"github.com/shopspring/decimal"
)

type DayCountConvention string

const (
	Actual365 DayCountConvention = "ACTUAL_365"
	Thirty360 DayCountConvention = "THIRTY_360"
)

type InterestAccrual struct {
	ID                 string
	AccountID          string
	AccrualDate        time.Time
	Principal          decimal.Decimal
	Rate               decimal.Decimal
	AccruedAmount      decimal.Decimal
	DayCountConvention DayCountConvention
	Posted             bool
	CreatedAt          time.Time
}

type InterestRate struct {
	ID            string
	ProductType   string
	Rate          decimal.Decimal
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	CreatedAt     time.Time
}
