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
	defaultBillChargeLimit       = 50
	maxBillChargeLimit           = 200
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

// BillChargeBreakdownRequest configures service/account charge breakdown rows.
type BillChargeBreakdownRequest struct {
	Limit int
}

// BillChargeBreakdowns groups service/account totals and resource drilldown rows.
type BillChargeBreakdowns struct {
	Summaries []BillChargeSummary
	Resources []BillResourceChargeSummary
}

// BillChargeSummary stores one service/account/region/usage-type charge rollup.
type BillChargeSummary struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	UsageAccountID     string
	ServiceCode        string
	ServiceName        string
	RegionCode         string
	UsageType          string
	LineItemStatus     string
	CurrencyCode       string
	LineItemCount      int
	ResourceCount      int
	ChargeMicros       int64
	CreditMicros       int64
	RefundMicros       int64
	TaxMicros          int64
	TotalMicros        int64
	UpdatedAt          string
}

// BillResourceChargeSummary stores a resource-level drilldown row for charge rollups.
type BillResourceChargeSummary struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	UsageAccountID     string
	ServiceCode        string
	ServiceName        string
	RegionCode         string
	UsageType          string
	LineItemStatus     string
	CurrencyCode       string
	ResourceID         string
	ResourceName       string
	Description        string
	LineItemCount      int
	ChargeMicros       int64
	CreditMicros       int64
	RefundMicros       int64
	TaxMicros          int64
	TotalMicros        int64
	UpdatedAt          string
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

// ListChargeBreakdowns returns charge rollups and resource drilldowns from source line items.
func (r BillsRepository) ListChargeBreakdowns(ctx context.Context, request BillChargeBreakdownRequest) (BillChargeBreakdowns, error) {
	if r.db == nil {
		return BillChargeBreakdowns{}, fmt.Errorf("database handle is required")
	}
	request = normalizeBillChargeBreakdownRequest(request)

	summaries, err := r.listBillChargeSummaries(ctx, request.Limit)
	if err != nil {
		return BillChargeBreakdowns{}, err
	}
	resources, err := r.listBillResourceChargeSummaries(ctx, request.Limit)
	if err != nil {
		return BillChargeBreakdowns{}, err
	}
	return BillChargeBreakdowns{
		Summaries: summaries,
		Resources: resources,
	}, nil
}

func (r BillsRepository) listBillChargeSummaries(ctx context.Context, limit int) ([]BillChargeSummary, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.region_code,
			li.usage_type,
			li.line_item_status,
			li.currency_code,
			COUNT(*),
			COUNT(DISTINCT CASE
				WHEN li.resource_id IS NULL OR trim(li.resource_id) = '' THEN NULL
				ELSE li.resource_id
			END),
			COALESCE(SUM(CASE WHEN li.line_item_type IN ('Usage', 'Fee') THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Credit' THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Refund' THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Tax' THEN li.unblended_cost_micros ELSE 0 END), 0),
			MAX(li.created_at)
		 FROM bill_line_items li
		 GROUP BY
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.region_code,
			li.usage_type,
			li.line_item_status,
			li.currency_code
		 ORDER BY
			li.billing_period_start DESC,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.region_code,
			li.usage_type,
			li.line_item_status,
			li.currency_code
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list bill charge summaries: %w", err)
	}
	defer rows.Close()

	var summaries []BillChargeSummary
	for rows.Next() {
		summary, err := scanBillChargeSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bill charge summaries: %w", err)
	}
	return summaries, nil
}

func (r BillsRepository) listBillResourceChargeSummaries(ctx context.Context, limit int) ([]BillResourceChargeSummary, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.region_code,
			li.usage_type,
			li.line_item_status,
			li.currency_code,
			COALESCE(li.resource_id, ''),
			COALESCE(NULLIF(r.resource_name, ''), ''),
			MAX(li.description),
			COUNT(*),
			COALESCE(SUM(CASE WHEN li.line_item_type IN ('Usage', 'Fee') THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Credit' THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Refund' THEN li.unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN li.line_item_type = 'Tax' THEN li.unblended_cost_micros ELSE 0 END), 0),
			MAX(li.created_at)
		 FROM bill_line_items li
		 LEFT JOIN resources r ON r.id = li.resource_id
		 GROUP BY
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.region_code,
			li.usage_type,
			li.line_item_status,
			li.currency_code,
			li.resource_id,
			r.resource_name
		 ORDER BY
			li.billing_period_start DESC,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.region_code,
			li.usage_type,
			li.resource_id,
			li.line_item_status,
			li.currency_code
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list bill resource charge summaries: %w", err)
	}
	defer rows.Close()

	var summaries []BillResourceChargeSummary
	for rows.Next() {
		summary, err := scanBillResourceChargeSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bill resource charge summaries: %w", err)
	}
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

type billChargeSummaryRow interface {
	Scan(dest ...any) error
}

func scanBillChargeSummary(row billChargeSummaryRow) (BillChargeSummary, error) {
	var summary BillChargeSummary
	if err := row.Scan(
		&summary.BillingPeriodStart,
		&summary.BillingPeriodEnd,
		&summary.PayerAccountID,
		&summary.UsageAccountID,
		&summary.ServiceCode,
		&summary.ServiceName,
		&summary.RegionCode,
		&summary.UsageType,
		&summary.LineItemStatus,
		&summary.CurrencyCode,
		&summary.LineItemCount,
		&summary.ResourceCount,
		&summary.ChargeMicros,
		&summary.CreditMicros,
		&summary.RefundMicros,
		&summary.TaxMicros,
		&summary.UpdatedAt,
	); err != nil {
		return BillChargeSummary{}, fmt.Errorf("scan bill charge summary: %w", err)
	}
	summary.TotalMicros = billChargeTotalMicros(summary.ChargeMicros, summary.CreditMicros, summary.RefundMicros, summary.TaxMicros)
	return summary, nil
}

func scanBillResourceChargeSummary(row billChargeSummaryRow) (BillResourceChargeSummary, error) {
	var summary BillResourceChargeSummary
	if err := row.Scan(
		&summary.BillingPeriodStart,
		&summary.BillingPeriodEnd,
		&summary.PayerAccountID,
		&summary.UsageAccountID,
		&summary.ServiceCode,
		&summary.ServiceName,
		&summary.RegionCode,
		&summary.UsageType,
		&summary.LineItemStatus,
		&summary.CurrencyCode,
		&summary.ResourceID,
		&summary.ResourceName,
		&summary.Description,
		&summary.LineItemCount,
		&summary.ChargeMicros,
		&summary.CreditMicros,
		&summary.RefundMicros,
		&summary.TaxMicros,
		&summary.UpdatedAt,
	); err != nil {
		return BillResourceChargeSummary{}, fmt.Errorf("scan bill resource charge summary: %w", err)
	}
	summary.TotalMicros = billChargeTotalMicros(summary.ChargeMicros, summary.CreditMicros, summary.RefundMicros, summary.TaxMicros)
	return summary, nil
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

func normalizeBillChargeBreakdownRequest(request BillChargeBreakdownRequest) BillChargeBreakdownRequest {
	if request.Limit <= 0 {
		request.Limit = defaultBillChargeLimit
	}
	if request.Limit > maxBillChargeLimit {
		request.Limit = maxBillChargeLimit
	}
	return request
}

func billStateSummaryTotalMicros(summary BillStateSummary) int64 {
	total := summary.UsageChargeMicros + summary.TaxMicros - summary.CreditMicros - summary.RefundMicros
	if total < 0 {
		return 0
	}
	return total
}

func billChargeTotalMicros(chargeMicros, creditMicros, refundMicros, taxMicros int64) int64 {
	total := chargeMicros + taxMicros - creditMicros - refundMicros
	if total < 0 {
		return 0
	}
	return total
}

func billStateSummaryID(state, periodStart, periodEnd, payerAccountID, currencyCode string) string {
	return stableBillingID("bsv", state, periodStart, periodEnd, payerAccountID, currencyCode)
}
