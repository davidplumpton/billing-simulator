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
	defaultBillReconcileLimit    = 25
	maxBillReconcileLimit        = 100
	billStateOpen                = "open"
	billStatePendingClose        = "pending-close"
	billReconcileStatusBalanced  = "balanced"
	billReconcileStatusResidual  = "residual"
)

// BillStateSummaryRequest configures the bill-period state rows returned for the UI.
type BillStateSummaryRequest struct {
	Limit                 int
	DefaultPayerAccountID string
	Visibility            BillingVisibilityFilter
}

// BillingVisibilityFilter constrains billing read models to the simulated viewer's account scope.
type BillingVisibilityFilter struct {
	PayerAccountID string
	UsageAccountID string
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
	Limit      int
	Visibility BillingVisibilityFilter
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

// BillReconciliationRequest configures persisted-bill to source-line-item reconciliation rows.
type BillReconciliationRequest struct {
	Limit      int
	Visibility BillingVisibilityFilter
}

// BillReconciliation compares one persisted bill with a fresh aggregate of its final source line items.
type BillReconciliation struct {
	BillID                    string
	BillingPeriodStart        string
	BillingPeriodEnd          string
	PayerAccountID            string
	BillState                 string
	Status                    string
	CurrencyCode              string
	BillLineItemCount         int
	SourceLineItemCount       int
	LineItemCountResidual     int
	BillUsageChargeMicros     int64
	SourceUsageChargeMicros   int64
	UsageChargeResidualMicros int64
	BillCreditMicros          int64
	SourceCreditMicros        int64
	CreditResidualMicros      int64
	BillRefundMicros          int64
	SourceRefundMicros        int64
	RefundResidualMicros      int64
	BillTaxMicros             int64
	SourceTaxMicros           int64
	TaxResidualMicros         int64
	BillTotalMicros           int64
	SourceTotalMicros         int64
	TotalResidualMicros       int64
	UpdatedAt                 string
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

	openSummaries, err := r.listOpenBillStateSummaries(ctx, clock, request)
	if err != nil {
		return nil, err
	}
	if len(openSummaries) == 0 && request.DefaultPayerAccountID != "" && request.Visibility.allowsPayerAccount(request.DefaultPayerAccountID) {
		openSummaries = append(openSummaries, emptyOpenBillStateSummary(clock, request.DefaultPayerAccountID))
	}

	pendingSummaries, err := r.listPendingCloseBillStateSummaries(ctx, clock, currentTime, request)
	if err != nil {
		return nil, err
	}
	issuedSummaries, err := r.listIssuedBillStateSummaries(ctx, request)
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

	summaries, err := r.listBillChargeSummaries(ctx, request)
	if err != nil {
		return BillChargeBreakdowns{}, err
	}
	resources, err := r.listBillResourceChargeSummaries(ctx, request)
	if err != nil {
		return BillChargeBreakdowns{}, err
	}
	return BillChargeBreakdowns{
		Summaries: summaries,
		Resources: resources,
	}, nil
}

// ListBillReconciliations compares stored bills to final bill line items for the same period, payer, and currency.
func (r BillsRepository) ListBillReconciliations(ctx context.Context, request BillReconciliationRequest) ([]BillReconciliation, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeBillReconciliationRequest(request)
	if request.Visibility.UsageAccountID != "" {
		return nil, nil
	}

	clauses, args := billPayerVisibilityClauses("b", request.Visibility)
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, "\n   AND ")
	}
	args = append([]any{billLineItemStatusFinal}, args...)
	args = append(args, request.Limit)
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
			COALESCE(src.line_item_count, 0),
			COALESCE(src.usage_charge_micros, 0),
			COALESCE(src.credit_micros, 0),
			COALESCE(src.refund_micros, 0),
			COALESCE(src.tax_micros, 0),
			b.issued_at
		 FROM bills b
		 LEFT JOIN (
			SELECT
				billing_period_start,
				billing_period_end,
				payer_account_id,
				currency_code,
				COUNT(*) AS line_item_count,
				COALESCE(SUM(CASE WHEN line_item_type IN ('Usage', 'Fee') THEN unblended_cost_micros ELSE 0 END), 0) AS usage_charge_micros,
				COALESCE(SUM(CASE WHEN line_item_type = 'Credit' THEN unblended_cost_micros ELSE 0 END), 0) AS credit_micros,
				COALESCE(SUM(CASE WHEN line_item_type = 'Refund' THEN unblended_cost_micros ELSE 0 END), 0) AS refund_micros,
				COALESCE(SUM(CASE WHEN line_item_type = 'Tax' THEN unblended_cost_micros ELSE 0 END), 0) AS tax_micros
			 FROM bill_line_items
			 WHERE line_item_status = ?
			 GROUP BY
				billing_period_start,
				billing_period_end,
				payer_account_id,
				currency_code
		 ) src ON src.billing_period_start = b.billing_period_start
			AND src.billing_period_end = b.billing_period_end
			AND src.payer_account_id = b.payer_account_id
			AND src.currency_code = b.currency_code
		`+whereSQL+`
		 ORDER BY b.billing_period_start DESC, b.issued_at DESC, b.id DESC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list bill reconciliations: %w", err)
	}
	defer rows.Close()

	var reconciliations []BillReconciliation
	for rows.Next() {
		reconciliation, err := scanBillReconciliation(rows)
		if err != nil {
			return nil, err
		}
		reconciliations = append(reconciliations, reconciliation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bill reconciliations: %w", err)
	}
	return reconciliations, nil
}

func (r BillsRepository) listBillChargeSummaries(ctx context.Context, request BillChargeBreakdownRequest) ([]BillChargeSummary, error) {
	clauses, args := billLineItemVisibilityClauses("li", request.Visibility)
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, "\n   AND ")
	}
	args = append(args, request.Limit)
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
		`+whereSQL+`
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
		args...,
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

func (r BillsRepository) listBillResourceChargeSummaries(ctx context.Context, request BillChargeBreakdownRequest) ([]BillResourceChargeSummary, error) {
	clauses, args := billLineItemVisibilityClauses("li", request.Visibility)
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, "\n   AND ")
	}
	args = append(args, request.Limit)
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
		`+whereSQL+`
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
		args...,
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

func (r BillsRepository) listOpenBillStateSummaries(ctx context.Context, clock SimulatorClock, request BillStateSummaryRequest) ([]BillStateSummary, error) {
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
		request,
	)
}

func (r BillsRepository) listPendingCloseBillStateSummaries(ctx context.Context, clock SimulatorClock, currentTime time.Time, request BillStateSummaryRequest) ([]BillStateSummary, error) {
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
		request,
	)
}

func (r BillsRepository) listLineItemBillStateSummaries(ctx context.Context, state, predicate string, args []any, request BillStateSummaryRequest) ([]BillStateSummary, error) {
	clauses := []string{"(" + predicate + ")"}
	visibilityClauses, visibilityArgs := billLineItemVisibilityClauses("li", request.Visibility)
	clauses = append(clauses, visibilityClauses...)
	args = append(args, visibilityArgs...)
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
		 WHERE ` + strings.Join(clauses, "\n   AND ") + `
		 GROUP BY
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.currency_code
		 ORDER BY li.billing_period_start DESC, li.payer_account_id, li.currency_code
		 LIMIT ?`
	args = append(args, request.Limit)
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

func (r BillsRepository) listIssuedBillStateSummaries(ctx context.Context, request BillStateSummaryRequest) ([]BillStateSummary, error) {
	if request.Visibility.UsageAccountID != "" {
		return r.listLineItemBillStateSummaries(
			ctx,
			billStateIssued,
			`li.line_item_status = ?
			   AND EXISTS (
				SELECT 1
				FROM billing_period_closes c
				WHERE c.billing_period_start = li.billing_period_start
				  AND c.billing_period_end = li.billing_period_end
				  AND c.payer_account_id = li.payer_account_id
				  AND c.status = 'closed'
			   )`,
			[]any{billLineItemStatusFinal},
			request,
		)
	}
	clauses, args := billPayerVisibilityClauses("b", request.Visibility)
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, "\n   AND ")
	}
	args = append(args, request.Limit)
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
			COALESCE(ps.status, CASE o.status WHEN 'paid' THEN 'succeeded' ELSE o.status END) AS status,
			COALESCE(ps.amount_due_micros, o.amount_due_micros) AS amount_due_micros,
			COALESCE(ps.amount_paid_micros, o.amount_paid_micros) AS amount_paid_micros,
			o.invoice_date,
			o.due_date,
			COALESCE(ps.updated_at, o.updated_at) AS updated_at
		 FROM bills b
		 JOIN invoice_obligations o ON o.bill_id = b.id
		 LEFT JOIN invoice_payment_states ps ON ps.invoice_obligation_id = o.id
		`+whereSQL+`
		 ORDER BY b.billing_period_start DESC, b.issued_at DESC, b.id DESC
		 LIMIT ?`,
		args...,
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
	request.Visibility = normalizeBillingVisibilityFilter(request.Visibility)
	return request
}

func normalizeBillChargeBreakdownRequest(request BillChargeBreakdownRequest) BillChargeBreakdownRequest {
	if request.Limit <= 0 {
		request.Limit = defaultBillChargeLimit
	}
	if request.Limit > maxBillChargeLimit {
		request.Limit = maxBillChargeLimit
	}
	request.Visibility = normalizeBillingVisibilityFilter(request.Visibility)
	return request
}

func normalizeBillReconciliationRequest(request BillReconciliationRequest) BillReconciliationRequest {
	if request.Limit <= 0 {
		request.Limit = defaultBillReconcileLimit
	}
	if request.Limit > maxBillReconcileLimit {
		request.Limit = maxBillReconcileLimit
	}
	request.Visibility = normalizeBillingVisibilityFilter(request.Visibility)
	return request
}

func normalizeBillingVisibilityFilter(filter BillingVisibilityFilter) BillingVisibilityFilter {
	filter.PayerAccountID = strings.TrimSpace(filter.PayerAccountID)
	filter.UsageAccountID = strings.TrimSpace(filter.UsageAccountID)
	return filter
}

func (filter BillingVisibilityFilter) allowsPayerAccount(accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false
	}
	if filter.PayerAccountID == "" {
		return true
	}
	return accountID == filter.PayerAccountID
}

func billLineItemVisibilityClauses(alias string, filter BillingVisibilityFilter) ([]string, []any) {
	filter = normalizeBillingVisibilityFilter(filter)
	prefix := strings.TrimSpace(alias)
	if prefix != "" {
		prefix += "."
	}
	clauses := []string{}
	args := []any{}
	if filter.PayerAccountID != "" {
		clauses = append(clauses, prefix+"payer_account_id = ?")
		args = append(args, filter.PayerAccountID)
	}
	if filter.UsageAccountID != "" {
		clauses = append(clauses, prefix+"usage_account_id = ?")
		args = append(args, filter.UsageAccountID)
	}
	return clauses, args
}

func billPayerVisibilityClauses(alias string, filter BillingVisibilityFilter) ([]string, []any) {
	filter = normalizeBillingVisibilityFilter(filter)
	prefix := strings.TrimSpace(alias)
	if prefix != "" {
		prefix += "."
	}
	if filter.PayerAccountID == "" {
		return nil, nil
	}
	return []string{prefix + "payer_account_id = ?"}, []any{filter.PayerAccountID}
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

func scanBillReconciliation(row billChargeSummaryRow) (BillReconciliation, error) {
	var reconciliation BillReconciliation
	if err := row.Scan(
		&reconciliation.BillID,
		&reconciliation.BillingPeriodStart,
		&reconciliation.BillingPeriodEnd,
		&reconciliation.PayerAccountID,
		&reconciliation.BillState,
		&reconciliation.CurrencyCode,
		&reconciliation.BillLineItemCount,
		&reconciliation.BillUsageChargeMicros,
		&reconciliation.BillCreditMicros,
		&reconciliation.BillRefundMicros,
		&reconciliation.BillTaxMicros,
		&reconciliation.BillTotalMicros,
		&reconciliation.SourceLineItemCount,
		&reconciliation.SourceUsageChargeMicros,
		&reconciliation.SourceCreditMicros,
		&reconciliation.SourceRefundMicros,
		&reconciliation.SourceTaxMicros,
		&reconciliation.UpdatedAt,
	); err != nil {
		return BillReconciliation{}, fmt.Errorf("scan bill reconciliation: %w", err)
	}
	reconciliation.SourceTotalMicros = billChargeTotalMicros(
		reconciliation.SourceUsageChargeMicros,
		reconciliation.SourceCreditMicros,
		reconciliation.SourceRefundMicros,
		reconciliation.SourceTaxMicros,
	)
	reconciliation.LineItemCountResidual = reconciliation.BillLineItemCount - reconciliation.SourceLineItemCount
	reconciliation.UsageChargeResidualMicros = reconciliation.BillUsageChargeMicros - reconciliation.SourceUsageChargeMicros
	reconciliation.CreditResidualMicros = reconciliation.BillCreditMicros - reconciliation.SourceCreditMicros
	reconciliation.RefundResidualMicros = reconciliation.BillRefundMicros - reconciliation.SourceRefundMicros
	reconciliation.TaxResidualMicros = reconciliation.BillTaxMicros - reconciliation.SourceTaxMicros
	reconciliation.TotalResidualMicros = reconciliation.BillTotalMicros - reconciliation.SourceTotalMicros
	reconciliation.Status = billReconciliationStatus(reconciliation)
	return reconciliation, nil
}

func billReconciliationStatus(reconciliation BillReconciliation) string {
	if reconciliation.LineItemCountResidual == 0 &&
		reconciliation.UsageChargeResidualMicros == 0 &&
		reconciliation.CreditResidualMicros == 0 &&
		reconciliation.RefundResidualMicros == 0 &&
		reconciliation.TaxResidualMicros == 0 &&
		reconciliation.TotalResidualMicros == 0 {
		return billReconcileStatusBalanced
	}
	return billReconcileStatusResidual
}
