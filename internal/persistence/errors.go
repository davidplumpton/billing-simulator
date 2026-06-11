package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrClosedBillingPeriod marks attempts to mutate data protected by a finalized billing period.
	ErrClosedBillingPeriod = errors.New("closed billing period")

	// ErrCostCategoryNotFound marks lookup failures where no Cost Category matches the request.
	ErrCostCategoryNotFound = errors.New("cost category not found")

	// ErrCostCategoryRuleNotFound marks lookup failures where no Cost Category rule matches the request.
	ErrCostCategoryRuleNotFound = errors.New("cost category rule not found")

	// ErrSavedReportNotFound marks lookup failures where no saved Cost Explorer report matches the request.
	ErrSavedReportNotFound = errors.New("saved report not found")
)

type domainError struct {
	message string
	target  error
	cause   error
}

func (e domainError) Error() string {
	return e.message
}

func (e domainError) Unwrap() error {
	if e.cause == nil {
		return e.target
	}
	return errors.Join(e.target, e.cause)
}

func domainErrorf(target error, format string, args ...any) error {
	return domainError{
		message: fmt.Sprintf(format, args...),
		target:  target,
	}
}

func domainErrorWithCausef(target, cause error, format string, args ...any) error {
	return domainError{
		message: fmt.Sprintf(format, args...),
		target:  target,
		cause:   cause,
	}
}

type billingPeriodCloseQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// closedBillingPeriodMutationError classifies failed payer-period writes without depending on SQLite trigger text.
func closedBillingPeriodMutationError(ctx context.Context, q billingPeriodCloseQuerier, periodStart, periodEnd, payerAccountID string, cause error) error {
	if cause == nil {
		return nil
	}
	closed, err := billingPeriodClosedForMutation(ctx, q, periodStart, periodEnd, payerAccountID)
	if err != nil || !closed {
		return cause
	}
	return domainErrorWithCausef(
		ErrClosedBillingPeriod,
		cause,
		"billing period %s to %s is closed for payer %s",
		periodStart,
		periodEnd,
		payerAccountID,
	)
}

func billingPeriodClosedForMutation(ctx context.Context, q billingPeriodCloseQuerier, periodStart, periodEnd, payerAccountID string) (bool, error) {
	periodStart = strings.TrimSpace(periodStart)
	periodEnd = strings.TrimSpace(periodEnd)
	payerAccountID = strings.TrimSpace(payerAccountID)
	if periodStart == "" || periodEnd == "" || payerAccountID == "" {
		return false, nil
	}

	var found int
	err := q.QueryRowContext(ctx, `SELECT 1
		FROM billing_period_closes
		WHERE billing_period_start = ?
		  AND billing_period_end = ?
		  AND payer_account_id = ?
		  AND status = ?
		LIMIT 1`,
		periodStart,
		periodEnd,
		payerAccountID,
		billingPeriodCloseStatusClosed,
	).Scan(&found)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("check closed billing period %s to %s payer %s: %w", periodStart, periodEnd, payerAccountID, err)
}
