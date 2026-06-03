package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	defaultBillStateSummaryLimit = 25
	maxBillStateSummaryLimit     = 100
	billStateOpen                = "open"
	billStatePendingClose        = "pending-close"
)

// BillStateSummaryRequest configures the bill-period state rows returned for the UI.
type BillStateSummaryRequest struct {
	Limit                 int
	DefaultPayerAccountID string
}

// BillStateSummary stores one visible bill-period state with charge totals.
type BillStateSummary struct {
	ID                      string
	BillingPeriodStart      string
	BillingPeriodEnd        string
	PayerAccountID          string
	BillState               string
	CurrencyCode            string
	LineItemCount           int
	UsageChargeMicros       int64
	CreditMicros            int64
	RefundMicros            int64
	TaxMicros               int64
	TotalMicros             int64
	InvoiceID               string
	InvoiceStatus           string
	InvoiceAmountDueMicros  int64
	InvoiceAmountPaidMicros int64
	InvoiceDate             string
	InvoiceDueDate          string
	UpdatedAt               string
}

// BillsRepository reads derived and issued bill summaries for the Bills page.
type BillsRepository struct {
	db    *sql.DB
	clock SimulatorClockRepository
}

// NewBillsRepository creates a bills repository backed by a workspace database.
func NewBillsRepository(db *sql.DB) BillsRepository {
	return BillsRepository{
		db:    db,
		clock: NewSimulatorClockRepository(db),
	}
}

// ListBillStateSummaries returns current, pending-close, and persisted bill states.
func (r BillsRepository) ListBillStateSummaries(ctx context.Context, request BillStateSummaryRequest) ([]BillStateSummary, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeBillStateSummaryRequest(request)
	clock, err := r.clock.Get(ctx)
	if err != nil {
		return nil, err
	}
	currentTime, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return nil, fmt.Errorf("parse simulator clock for bill summaries: %w", err)
	}

	openSummaries, err := r.listOpenBillStateSummaries(ctx, clock, request.Limit)
	if err != nil {
		return nil, err
	}
	if len(openSummaries) == 0 && request.DefaultPayerAccountID != "" {
		openSummaries = append(openSummaries, emptyOpenBillStateSummary(clock, request.DefaultPayerAccountID))
	}

	pendingSummaries, err := r.listPendingCloseBillStateSummaries(ctx, clock, currentTime, request.Limit)
	if err != nil {
		return nil, err
	}
	issuedSummaries, err := r.listIssuedBillStateSummaries(ctx, request.Limit)
	if err != nil {
		return nil, err
	}

	summaries := make([]BillStateSummary, 0, len(openSummaries)+len(pendingSummaries)+len(issuedSummaries))
	summaries = append(summaries, openSummaries...)
	summaries = append(summaries, pendingSummaries...)
	summaries = append(summaries, issuedSummaries...)
	return summaries, nil
}

func (r BillsRepository) listOpenBillStateSummaries(ctx context.Context, clock SimulatorClock, limit int) ([]BillStateSummary, error) {
	return r.listLineItemBillStateSummaries(
		ctx,
		billStateOpen,
		`li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND NOT EXISTS (
			SELECT 1
			FROM billing_period_closes c
			WHERE c.billing_period_start = li.billing_period_start
			  AND c.billing_period_end = li.billing_period_end
			  AND c.payer_account_id = li.payer_account_id
			  AND c.status = 'closed'
		   )`,
		[]any{clock.BillingPeriodStart, clock.BillingPeriodEnd},
		limit,
	)
}

func (r BillsRepository) listPendingCloseBillStateSummaries(ctx context.Context, clock SimulatorClock, currentTime time.Time, limit int) ([]BillStateSummary, error) {
	return r.listLineItemBillStateSummaries(
		ctx,
		billStatePendingClose,
		`li.billing_period_end <= ?
		   AND NOT (li.billing_period_start = ? AND li.billing_period_end = ?)
		   AND NOT EXISTS (
			SELECT 1
			FROM billing_period_closes c
			WHERE c.billing_period_start = li.billing_period_start
			  AND c.billing_period_end = li.billing_period_end
			  AND c.payer_account_id = li.payer_account_id
			  AND c.status = 'closed'
		   )`,
		[]any{currentTime.UTC().Format(time.DateOnly), clock.BillingPeriodStart, clock.BillingPeriodEnd},
		limit,
	)
}

func (r BillsRepository) listLineItemBillStateSummaries(ctx context.Context, state, predicate string, args []any, limit int) ([]BillStateSummary, error) {
	query := `SELECT
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.currency_code,
			COUNT(*),
			COALESCE(SUM(CASE WHEN li.line_item_type IN ('Usage', 'Fee') THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Credit' THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Refund' THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Tax' THEN li.unblended_cost_micros ELSE 0 END), 0),
			MAX(li.created_at)
		 FROM bill_line_items li
		 WHERE ` + predicate + `
		 GROUP BY
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.currency_code
		 ORDER BY li.billing_period_start DESC, li.payer_account_id, li.currency_code
		 LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s bill summaries: %w", state, err)
	}
	defer rows.Close()

	var summaries []BillStateSummary
	for rows.Next() {
		summary, err := scanLineItemBillStateSummary(rows, state)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s bill summaries: %w", state, err)
	}
	return summaries, nil
}

func (r BillsRepository) listIssuedBillStateSummaries(ctx context.Context, limit int) ([]BillStateSummary, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			b.id,
			b.billing_period_start,
			b.billing_period_end,
			b.payer_account_id,
			b.bill_state,
			b.currency_code,
			b.line_item_count,
			b.usage_charge_micros,
			b.credit_micros,
			b.refund_micros,
			b.tax_micros,
			b.total_micros,
			o.invoice_id,
			o.status,
			o.amount_due_micros,
			o.amount_paid_micros,
			o.invoice_date,
			o.due_date,
			o.updated_at
		 FROM bills b
		 JOIN invoice_obligations o ON o.bill_id = b.id
		 ORDER BY b.billing_period_start DESC, b.issued_at DESC, b.id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list stored bill summaries: %w", err)
	}
	defer rows.Close()

	var summaries []BillStateSummary
	for rows.Next() {
		var summary BillStateSummary
		if err := rows.Scan(
			&summary.ID,
			&summary.BillingPeriodStart,
			&summary.BillingPeriodEnd,
			&summary.PayerAccountID,
			&summary.BillState,
			&summary.CurrencyCode,
			&summary.LineItemCount,
			&summary.UsageChargeMicros,
			&summary.CreditMicros,
			&summary.RefundMicros,
			&summary.TaxMicros,
			&summary.TotalMicros,
			&summary.InvoiceID,
			&summary.InvoiceStatus,
			&summary.InvoiceAmountDueMicros,
			&summary.InvoiceAmountPaidMicros,
			&summary.InvoiceDate,
			&summary.InvoiceDueDate,
			&summary.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan stored bill summary: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stored bill summaries: %w", err)
	}
	return summaries, nil
}

type lineItemBillSummaryRow interface {
	Scan(dest ...any) error
}

func scanLineItemBillStateSummary(row lineItemBillSummaryRow, state string) (BillStateSummary, error) {
	var summary BillStateSummary
	if err := row.Scan(
		&summary.BillingPeriodStart,
		&summary.BillingPeriodEnd,
		&summary.PayerAccountID,
		&summary.CurrencyCode,
		&summary.LineItemCount,
		&summary.UsageChargeMicros,
		&summary.CreditMicros,
		&summary.RefundMicros,
		&summary.TaxMicros,
		&summary.UpdatedAt,
	); err != nil {
		return BillStateSummary{}, fmt.Errorf("scan %s bill summary: %w", state, err)
	}
	summary.ID = billStateSummaryID(state, summary.BillingPeriodStart, summary.BillingPeriodEnd, summary.PayerAccountID, summary.CurrencyCode)
	summary.BillState = state
	summary.TotalMicros = billStateSummaryTotalMicros(summary)
	return summary, nil
}

func emptyOpenBillStateSummary(clock SimulatorClock, payerAccountID string) BillStateSummary {
	return BillStateSummary{
		ID:                 billStateSummaryID(billStateOpen, clock.BillingPeriodStart, clock.BillingPeriodEnd, payerAccountID, defaultBillCurrencyCode),
		BillingPeriodStart: clock.BillingPeriodStart,
		BillingPeriodEnd:   clock.BillingPeriodEnd,
		PayerAccountID:     payerAccountID,
		BillState:          billStateOpen,
		CurrencyCode:       defaultBillCurrencyCode,
		UpdatedAt:          clock.UpdatedAt,
	}
}

func normalizeBillStateSummaryRequest(request BillStateSummaryRequest) BillStateSummaryRequest {
	if request.Limit <= 0 {
		request.Limit = defaultBillStateSummaryLimit
	}
	if request.Limit > maxBillStateSummaryLimit {
		request.Limit = maxBillStateSummaryLimit
	}
	request.DefaultPayerAccountID = strings.TrimSpace(request.DefaultPayerAccountID)
	return request
}

func billStateSummaryTotalMicros(summary BillStateSummary) int64 {
	total := summary.UsageChargeMicros + summary.TaxMicros - summary.CreditMicros - summary.RefundMicros
	if total < 0 {
		return 0
	}
	return total
}

func billStateSummaryID(state, periodStart, periodEnd, payerAccountID, currencyCode string) string {
	return stableBillingID("bsv", state, periodStart, periodEnd, payerAccountID, currencyCode)
}
