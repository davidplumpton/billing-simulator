package persistence

import (
	"fmt"
	"time"
)

// BillingPeriod describes the simulator's UTC calendar-month billing window.
type BillingPeriod struct {
	Start string
	End   string
	Days  int
}

// BillingPeriodForTime returns the UTC month window containing the given time.
func BillingPeriodForTime(value time.Time) (BillingPeriod, error) {
	value = value.UTC()
	periodStart := time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	days := int(periodEnd.Sub(periodStart).Hours() / 24)
	if days <= 0 {
		return BillingPeriod{}, fmt.Errorf("billing period days must be greater than zero")
	}
	return BillingPeriod{
		Start: periodStart.Format(time.DateOnly),
		End:   periodEnd.Format(time.DateOnly),
		Days:  days,
	}, nil
}
