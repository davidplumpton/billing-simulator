package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultMonthEndCloseLimit      = 10
	maxMonthEndCloseLimit          = 50
	defaultInvoiceObligationDueDay = 14
	maxInvoiceObligationDueDay     = 90
	billingPeriodCloseStatusClosed = "closed"
	billStateIssued                = "issued"
	invoiceObligationStatusDue     = "due"
	defaultBillCurrencyCode        = "USD"
)

// MonthEndCloseRequest identifies the payer and optional period to close.
type MonthEndCloseRequest struct {
	PayerAccountID string
	PeriodStart    string
	PeriodEnd      string
	InvoiceDueDays int
}

// BillingPeriodClose stores the audit snapshot for one closed payer billing period.
type BillingPeriodClose struct {
	ID                     string
	BillingPeriodStart     string
	BillingPeriodEnd       string
	PayerAccountID         string
	Status                 string
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	FinalizedLineItemCount int
	FinalizedCostMicros    int64
	CurrencyCode           string
	SummariesRefreshed     int
	ClosedAt               string
}

// Bill stores the issued bill totals derived from finalized bill line items.
type Bill struct {
	ID                 string
	CloseID            string
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	BillState          string
	CurrencyCode       string
	LineItemCount      int
	UsageChargeMicros  int64
	CreditMicros       int64
	RefundMicros       int64
	TaxMicros          int64
	TotalMicros        int64
	IssuedAt           string
	CreatedAt          string
}

// InvoiceObligation stores the payment obligation prepared when a bill is issued.
type InvoiceObligation struct {
	ID               string
	BillID           string
	InvoiceID        string
	Status           string
	AmountDueMicros  int64
	AmountPaidMicros int64
	CurrencyCode     string
	InvoiceDate      string
	DueDate          string
	CreatedAt        string
	UpdatedAt        string
}

// BillWithInvoiceObligation combines an issued bill with its current payment obligation.
type BillWithInvoiceObligation struct {
	Bill       Bill
	Obligation InvoiceObligation
}

// MonthEndCloseResult reports all durable artifacts produced or returned for a close.
type MonthEndCloseResult struct {
	Close                  BillingPeriodClose
	Bill                   Bill
	InvoiceObligation      InvoiceObligation
	InvoiceDocument        InvoiceDocument
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	FinalizedLineItems     int
	Summaries              []BillingPeriodServiceSummary
}

// MonthEndCloseRepository finalizes monthly billing periods into issued bills.
type MonthEndCloseRepository struct {
	db        *sql.DB
	clock     SimulatorClockRepository
	metering  MeteringRepository
	lineItems BillLineItemRepository
	dailyJobs DailyMeteringJobRepository
	support   SupportChargeRepository
}

// NewMonthEndCloseRepository creates a month-end close repository backed by a workspace database.
func NewMonthEndCloseRepository(db *sql.DB) MonthEndCloseRepository {
	return MonthEndCloseRepository{
		db:        db,
		clock:     NewSimulatorClockRepository(db),
		metering:  NewMeteringRepository(db),
		lineItems: NewBillLineItemRepository(db),
		dailyJobs: NewDailyMeteringJobRepository(db),
		support:   NewSupportChargeRepository(db),
	}
}

// ClosePreviousPeriod finalizes the requested period, defaulting to the completed period before the simulator clock.
func (r MonthEndCloseRepository) ClosePreviousPeriod(ctx context.Context, request MonthEndCloseRequest) (MonthEndCloseResult, error) {
	if r.db == nil {
		return MonthEndCloseResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeMonthEndCloseRequest(request)
	if err := validateMonthEndCloseRequest(request); err != nil {
		return MonthEndCloseResult{}, err
	}

	clock, err := r.clock.Get(ctx)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	period, err := resolveMonthEndClosePeriod(clock, request)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	request.PeriodStart = period.Start
	request.PeriodEnd = period.End
	if err := validateMonthEndCloseClock(clock, period); err != nil {
		return MonthEndCloseResult{}, err
	}

	if close, found, err := r.findCloseByPeriodPayer(ctx, request.PeriodStart, request.PeriodEnd, request.PayerAccountID); err != nil {
		return MonthEndCloseResult{}, err
	} else if found {
		return r.resultForClose(ctx, close, 0, 0)
	}

	throughTime := periodEndAsRFC3339(period)
	meteringResult, err := r.metering.GenerateMeteringRecordsThrough(ctx, throughTime)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	if err := rejectPayerMismatchedLineItems(ctx, r.db, request); err != nil {
		return MonthEndCloseResult{}, err
	}
	lineItemResult, err := r.lineItems.GenerateBillLineItemsThrough(ctx, BillLineItemGenerationRequest{
		PayerAccountID: request.PayerAccountID,
		LineItemStatus: billLineItemStatusEstimated,
	}, throughTime)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	supportCatalogItem, err := r.support.supportCatalogItem(ctx, period)
	if err != nil {
		return MonthEndCloseResult{}, err
	}

	var close BillingPeriodClose
	var summariesRefreshed int
	var supportResult SupportChargeGenerationResult
	createdClose := false
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		existing, found, err := findCloseByPeriodPayer(ctx, tx, request.PeriodStart, request.PeriodEnd, request.PayerAccountID)
		if err != nil {
			return err
		}
		if found {
			close = existing
			return nil
		}
		if err := rejectCrossPeriodLineItems(ctx, tx, request); err != nil {
			return err
		}
		if err := rejectPayerMismatchedLineItems(ctx, tx, request); err != nil {
			return err
		}
		if _, err := finalizeEstimatedLineItems(ctx, tx, request); err != nil {
			return err
		}
		supportResult, err = r.support.generateSupportChargesInTx(ctx, tx, SupportChargeGenerationRequest{
			PayerAccountID: request.PayerAccountID,
			PeriodStart:    request.PeriodStart,
			PeriodEnd:      request.PeriodEnd,
			LineItemStatus: billLineItemStatusFinal,
		}, period, supportCatalogItem)
		if err != nil {
			return err
		}
		if _, err := refreshCostCategoryAssignmentsInTx(ctx, tx, request.PeriodStart, request.PeriodEnd); err != nil {
			return err
		}
		aggregate, err := aggregateFinalBill(ctx, tx, request)
		if err != nil {
			return err
		}
		summariesRefreshed, err = refreshBillingPeriodServiceSummariesForClose(ctx, tx, request.PeriodStart, request.PeriodEnd)
		if err != nil {
			return err
		}
		if _, err := refreshCostExplorerSummariesInTx(ctx, tx, request.PeriodStart, request.PeriodEnd); err != nil {
			return err
		}

		close = billingPeriodCloseFromAggregate(request, meteringResult.RecordsCreated, lineItemResult.ItemsCreated+supportResult.ItemsCreated, summariesRefreshed, aggregate)
		if err := insertBillingPeriodClose(ctx, tx, close); err != nil {
			return err
		}
		bill := billFromAggregate(close, aggregate)
		if err := insertBill(ctx, tx, bill); err != nil {
			return err
		}
		obligation := invoiceObligationFromBill(bill, request.InvoiceDueDays)
		if err := insertInvoiceObligation(ctx, tx, obligation); err != nil {
			return err
		}
		invoiceProfile, err := invoiceDocumentProfileForPayer(ctx, tx, bill.PayerAccountID, bill.CurrencyCode)
		if err != nil {
			return err
		}
		document, err := invoiceDocumentFromBillWithProfile(bill, obligation, invoiceProfile)
		if err != nil {
			return err
		}
		if err := insertInvoiceDocument(ctx, tx, document); err != nil {
			return err
		}
		createdClose = true
		return nil
	})
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	if !createdClose {
		return r.resultForClose(ctx, close, 0, 0)
	}
	storedClose, found, err := r.findCloseByPeriodPayer(ctx, request.PeriodStart, request.PeriodEnd, request.PayerAccountID)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	if !found {
		return MonthEndCloseResult{}, fmt.Errorf("month-end close was not persisted for %s to %s payer %s", request.PeriodStart, request.PeriodEnd, request.PayerAccountID)
	}
	return r.resultForClose(ctx, storedClose, meteringResult.RecordsCreated, lineItemResult.ItemsCreated+supportResult.ItemsCreated)
}

// ListRecentCloses reads recent period-close audit records in newest-first order.
func (r MonthEndCloseRepository) ListRecentCloses(ctx context.Context, limit int) ([]BillingPeriodClose, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if limit <= 0 {
		limit = defaultMonthEndCloseLimit
	}
	if limit > maxMonthEndCloseLimit {
		limit = maxMonthEndCloseLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			status,
			metering_records_created,
			bill_line_items_created,
			finalized_line_item_count,
			finalized_cost_micros,
			currency_code,
			summaries_refreshed,
			closed_at
		 FROM billing_period_closes
		 ORDER BY closed_at DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list billing period closes: %w", err)
	}
	defer rows.Close()

	var closes []BillingPeriodClose
	for rows.Next() {
		close, err := scanBillingPeriodClose(rows)
		if err != nil {
			return nil, err
		}
		closes = append(closes, close)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing period closes: %w", err)
	}
	return closes, nil
}

// ListIssuedBills reads recent issued bills with their invoice obligations.
func (r MonthEndCloseRepository) ListIssuedBills(ctx context.Context, limit int) ([]BillWithInvoiceObligation, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if limit <= 0 {
		limit = defaultMonthEndCloseLimit
	}
	if limit > maxMonthEndCloseLimit {
		limit = maxMonthEndCloseLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			b.id,
			b.close_id,
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
			b.issued_at,
			b.created_at,
			o.id,
			o.bill_id,
			o.invoice_id,
			o.status,
			o.amount_due_micros,
			o.amount_paid_micros,
			o.currency_code,
			o.invoice_date,
			o.due_date,
			o.created_at,
			o.updated_at
		 FROM bills b
		 JOIN invoice_obligations o ON o.bill_id = b.id
		 ORDER BY b.issued_at DESC, b.id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list issued bills: %w", err)
	}
	defer rows.Close()

	var bills []BillWithInvoiceObligation
	for rows.Next() {
		bill, obligation, err := scanBillWithInvoiceObligation(rows)
		if err != nil {
			return nil, err
		}
		bills = append(bills, BillWithInvoiceObligation{Bill: bill, Obligation: obligation})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issued bills: %w", err)
	}
	return bills, nil
}

func (r MonthEndCloseRepository) findCloseByPeriodPayer(ctx context.Context, periodStart, periodEnd, payerAccountID string) (BillingPeriodClose, bool, error) {
	return findCloseByPeriodPayer(ctx, r.db, periodStart, periodEnd, payerAccountID)
}

func (r MonthEndCloseRepository) resultForClose(ctx context.Context, close BillingPeriodClose, meteringRecordsCreated, billLineItemsCreated int) (MonthEndCloseResult, error) {
	bill, err := r.getBillByCloseID(ctx, close.ID)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	obligation, err := r.getInvoiceObligationByBillID(ctx, bill.ID)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	document, err := NewInvoiceDocumentRepository(r.db).GetByBillID(ctx, bill.ID)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	summaries, err := r.dailyJobs.ListBillingPeriodServiceSummaries(ctx, close.BillingPeriodStart, close.BillingPeriodEnd)
	if err != nil {
		return MonthEndCloseResult{}, err
	}
	return MonthEndCloseResult{
		Close:                  close,
		Bill:                   bill,
		InvoiceObligation:      obligation,
		InvoiceDocument:        document,
		MeteringRecordsCreated: meteringRecordsCreated,
		BillLineItemsCreated:   billLineItemsCreated,
		FinalizedLineItems:     close.FinalizedLineItemCount,
		Summaries:              summaries,
	}, nil
}

func (r MonthEndCloseRepository) getBillByCloseID(ctx context.Context, closeID string) (Bill, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			close_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			bill_state,
			currency_code,
			line_item_count,
			usage_charge_micros,
			credit_micros,
			refund_micros,
			tax_micros,
			total_micros,
			issued_at,
			created_at
		 FROM bills
		 WHERE close_id = ?`,
		closeID,
	)
	bill, err := scanBill(row)
	if err != nil {
		return Bill{}, fmt.Errorf("get bill for close %q: %w", closeID, err)
	}
	return bill, nil
}

func (r MonthEndCloseRepository) getInvoiceObligationByBillID(ctx context.Context, billID string) (InvoiceObligation, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			bill_id,
			invoice_id,
			status,
			amount_due_micros,
			amount_paid_micros,
			currency_code,
			invoice_date,
			due_date,
			created_at,
			updated_at
		 FROM invoice_obligations
		 WHERE bill_id = ?`,
		billID,
	)
	obligation, err := scanInvoiceObligation(row)
	if err != nil {
		return InvoiceObligation{}, fmt.Errorf("get invoice obligation for bill %q: %w", billID, err)
	}
	return obligation, nil
}

type monthEndCloseQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func findCloseByPeriodPayer(ctx context.Context, q monthEndCloseQuerier, periodStart, periodEnd, payerAccountID string) (BillingPeriodClose, bool, error) {
	row := q.QueryRowContext(
		ctx,
		`SELECT
			id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			status,
			metering_records_created,
			bill_line_items_created,
			finalized_line_item_count,
			finalized_cost_micros,
			currency_code,
			summaries_refreshed,
			closed_at
		 FROM billing_period_closes
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?`,
		periodStart,
		periodEnd,
		payerAccountID,
	)
	close, err := scanBillingPeriodClose(row)
	if err != nil {
		if errMatchesNoRows(err) {
			return BillingPeriodClose{}, false, nil
		}
		return BillingPeriodClose{}, false, err
	}
	return close, true, nil
}

type billAggregate struct {
	LineItemCount     int
	UsageChargeMicros int64
	CreditMicros      int64
	RefundMicros      int64
	TaxMicros         int64
	TotalMicros       int64
	CurrencyCode      string
}

func normalizeMonthEndCloseRequest(request MonthEndCloseRequest) MonthEndCloseRequest {
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.PeriodStart = strings.TrimSpace(request.PeriodStart)
	request.PeriodEnd = strings.TrimSpace(request.PeriodEnd)
	if request.InvoiceDueDays == 0 {
		request.InvoiceDueDays = defaultInvoiceObligationDueDay
	}
	return request
}

func validateMonthEndCloseRequest(request MonthEndCloseRequest) error {
	if request.PayerAccountID == "" {
		return fmt.Errorf("payer account ID is required")
	}
	if (request.PeriodStart == "") != (request.PeriodEnd == "") {
		return fmt.Errorf("billing period start and end must be provided together")
	}
	if request.InvoiceDueDays <= 0 {
		return fmt.Errorf("invoice due days must be greater than zero")
	}
	if request.InvoiceDueDays > maxInvoiceObligationDueDay {
		return fmt.Errorf("invoice due days must be %d or fewer", maxInvoiceObligationDueDay)
	}
	return nil
}

func resolveMonthEndClosePeriod(clock SimulatorClock, request MonthEndCloseRequest) (BillingPeriod, error) {
	if request.PeriodStart != "" {
		return billingPeriodFromDateRange(request.PeriodStart, request.PeriodEnd)
	}
	currentPeriodStart, err := time.Parse(time.DateOnly, clock.BillingPeriodStart)
	if err != nil {
		return BillingPeriod{}, fmt.Errorf("parse current billing period start: %w", err)
	}
	return BillingPeriodForTime(currentPeriodStart.AddDate(0, -1, 0))
}

func billingPeriodFromDateRange(periodStart, periodEnd string) (BillingPeriod, error) {
	start, err := time.Parse(time.DateOnly, periodStart)
	if err != nil {
		return BillingPeriod{}, fmt.Errorf("billing period start must use YYYY-MM-DD: %w", err)
	}
	end, err := time.Parse(time.DateOnly, periodEnd)
	if err != nil {
		return BillingPeriod{}, fmt.Errorf("billing period end must use YYYY-MM-DD: %w", err)
	}
	if !start.Before(end) {
		return BillingPeriod{}, fmt.Errorf("billing period start must be before end")
	}
	period, err := BillingPeriodForTime(start)
	if err != nil {
		return BillingPeriod{}, err
	}
	if period.Start != periodStart || period.End != periodEnd {
		return BillingPeriod{}, fmt.Errorf("month-end close period must match one UTC calendar billing period")
	}
	return period, nil
}

func validateMonthEndCloseClock(clock SimulatorClock, period BillingPeriod) error {
	currentTime, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return fmt.Errorf("parse simulator clock: %w", err)
	}
	periodEnd, err := time.Parse(time.DateOnly, period.End)
	if err != nil {
		return fmt.Errorf("parse billing period end: %w", err)
	}
	if currentTime.UTC().Before(periodEnd.UTC()) {
		return fmt.Errorf("billing period %s to %s has not ended at simulator clock %s", period.Start, period.End, clock.CurrentTime)
	}
	return nil
}

func periodEndAsRFC3339(period BillingPeriod) string {
	end, err := time.Parse(time.DateOnly, period.End)
	if err != nil {
		return period.End + "T00:00:00Z"
	}
	return end.UTC().Format(time.RFC3339)
}

func rejectCrossPeriodLineItems(ctx context.Context, tx *sql.Tx, request MonthEndCloseRequest) error {
	periodStartTime := request.PeriodStart + "T00:00:00Z"
	periodEndTime := request.PeriodEnd + "T00:00:00Z"
	var id string
	err := tx.QueryRowContext(
		ctx,
		`SELECT id
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		   AND (usage_start_time < ? OR usage_end_time > ?)
		 ORDER BY usage_start_time, id
		 LIMIT 1`,
		request.PeriodStart,
		request.PeriodEnd,
		request.PayerAccountID,
		periodStartTime,
		periodEndTime,
	).Scan(&id)
	if err == nil {
		return fmt.Errorf("bill line item %q crosses billing period %s to %s and cannot be finalized", id, request.PeriodStart, request.PeriodEnd)
	}
	if err == sql.ErrNoRows {
		return nil
	}
	return fmt.Errorf("check cross-period bill line items: %w", err)
}

type monthEndCloseQueryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rejectPayerMismatchedLineItems prevents closing a consolidated payer while usage was already priced to another payer.
func rejectPayerMismatchedLineItems(ctx context.Context, db monthEndCloseQueryRower, request MonthEndCloseRequest) error {
	var id, existingPayerAccountID, usageAccountID string
	err := db.QueryRowContext(
		ctx,
		`SELECT
			li.id,
			li.payer_account_id,
			li.usage_account_id
		 FROM bill_line_items li
		 LEFT JOIN accounts usage_account ON usage_account.id = li.usage_account_id
		 WHERE li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.line_item_type = ?
		   AND li.payer_account_id <> ?
		   AND (
			li.usage_account_id = ?
			OR usage_account.payer_account_id = ?
			OR usage_account.id IS NULL
		   )
		 ORDER BY li.usage_start_time, li.id
		 LIMIT 1`,
		request.PeriodStart,
		request.PeriodEnd,
		billLineItemTypeUsage,
		request.PayerAccountID,
		request.PayerAccountID,
		request.PayerAccountID,
	).Scan(&id, &existingPayerAccountID, &usageAccountID)
	if err == nil {
		return fmt.Errorf(
			"payer-mismatched bill line item %q for usage account %s is priced to payer %s, but month-end close requested payer %s for %s to %s; close with the existing payer or reprice usage before closing",
			id,
			usageAccountID,
			existingPayerAccountID,
			request.PayerAccountID,
			request.PeriodStart,
			request.PeriodEnd,
		)
	}
	if err == sql.ErrNoRows {
		return nil
	}
	return fmt.Errorf("check payer-mismatched bill line items: %w", err)
}

func finalizeEstimatedLineItems(ctx context.Context, tx *sql.Tx, request MonthEndCloseRequest) (int, error) {
	result, err := tx.ExecContext(
		ctx,
		`UPDATE bill_line_items
		 SET line_item_status = ?
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		   AND line_item_status = ?`,
		billLineItemStatusFinal,
		request.PeriodStart,
		request.PeriodEnd,
		request.PayerAccountID,
		billLineItemStatusEstimated,
	)
	if err != nil {
		return 0, fmt.Errorf("finalize bill line items: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read finalized bill line item count: %w", err)
	}
	return int(rowsAffected), nil
}

func aggregateFinalBill(ctx context.Context, tx *sql.Tx, request MonthEndCloseRequest) (billAggregate, error) {
	var aggregate billAggregate
	var minCurrency, maxCurrency sql.NullString
	var feeMicros int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN line_item_type = 'Usage' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Fee' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Credit' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Refund' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Tax' THEN unblended_cost_micros ELSE 0 END), 0),
			MIN(currency_code),
			MAX(currency_code)
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		   AND line_item_status = ?`,
		request.PeriodStart,
		request.PeriodEnd,
		request.PayerAccountID,
		billLineItemStatusFinal,
	).Scan(
		&aggregate.LineItemCount,
		&aggregate.UsageChargeMicros,
		&feeMicros,
		&aggregate.CreditMicros,
		&aggregate.RefundMicros,
		&aggregate.TaxMicros,
		&minCurrency,
		&maxCurrency,
	); err != nil {
		return billAggregate{}, fmt.Errorf("aggregate finalized bill line items: %w", err)
	}
	if minCurrency.Valid != maxCurrency.Valid || (minCurrency.Valid && minCurrency.String != maxCurrency.String) {
		return billAggregate{}, fmt.Errorf("month-end close does not support mixed bill currencies")
	}
	if minCurrency.Valid {
		aggregate.CurrencyCode = minCurrency.String
	} else {
		aggregate.CurrencyCode = defaultBillCurrencyCode
	}
	aggregate.UsageChargeMicros += feeMicros
	aggregate.TotalMicros = aggregate.UsageChargeMicros + aggregate.TaxMicros - aggregate.CreditMicros - aggregate.RefundMicros
	if aggregate.TotalMicros < 0 {
		aggregate.TotalMicros = 0
	}
	return aggregate, nil
}

func refreshBillingPeriodServiceSummariesForClose(ctx context.Context, tx *sql.Tx, periodStart, periodEnd string) (int, error) {
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM billing_period_service_summaries
		 WHERE billing_period_start = ? AND billing_period_end = ?`,
		periodStart,
		periodEnd,
	); err != nil {
		return 0, fmt.Errorf("clear billing period service summaries: %w", err)
	}
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO billing_period_service_summaries (
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			service_code,
			line_item_status,
			currency_code,
			line_item_count,
			unblended_cost_micros,
			refreshed_at
		 )
		 SELECT
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			service_code,
			line_item_status,
			currency_code,
			COUNT(*),
			COALESCE(SUM(unblended_cost_micros), 0),
			strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 FROM bill_line_items
		 WHERE billing_period_start = ? AND billing_period_end = ?
		 GROUP BY
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			service_code,
			line_item_status,
			currency_code`,
		periodStart,
		periodEnd,
	)
	if err != nil {
		return 0, fmt.Errorf("refresh billing period service summaries: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read refreshed billing summary count: %w", err)
	}
	return int(rowsAffected), nil
}

func billingPeriodCloseFromAggregate(request MonthEndCloseRequest, meteringRecordsCreated, billLineItemsCreated, summariesRefreshed int, aggregate billAggregate) BillingPeriodClose {
	return BillingPeriodClose{
		ID:                     billingPeriodCloseID(request.PeriodStart, request.PeriodEnd, request.PayerAccountID),
		BillingPeriodStart:     request.PeriodStart,
		BillingPeriodEnd:       request.PeriodEnd,
		PayerAccountID:         request.PayerAccountID,
		Status:                 billingPeriodCloseStatusClosed,
		MeteringRecordsCreated: meteringRecordsCreated,
		BillLineItemsCreated:   billLineItemsCreated,
		FinalizedLineItemCount: aggregate.LineItemCount,
		FinalizedCostMicros:    aggregate.TotalMicros,
		CurrencyCode:           aggregate.CurrencyCode,
		SummariesRefreshed:     summariesRefreshed,
	}
}

func billFromAggregate(close BillingPeriodClose, aggregate billAggregate) Bill {
	return Bill{
		ID:                 billID(close.BillingPeriodStart, close.BillingPeriodEnd, close.PayerAccountID, aggregate.CurrencyCode),
		CloseID:            close.ID,
		BillingPeriodStart: close.BillingPeriodStart,
		BillingPeriodEnd:   close.BillingPeriodEnd,
		PayerAccountID:     close.PayerAccountID,
		BillState:          billStateIssued,
		CurrencyCode:       aggregate.CurrencyCode,
		LineItemCount:      aggregate.LineItemCount,
		UsageChargeMicros:  aggregate.UsageChargeMicros,
		CreditMicros:       aggregate.CreditMicros,
		RefundMicros:       aggregate.RefundMicros,
		TaxMicros:          aggregate.TaxMicros,
		TotalMicros:        aggregate.TotalMicros,
	}
}

func invoiceObligationFromBill(bill Bill, dueDays int) InvoiceObligation {
	invoiceDate := bill.BillingPeriodEnd
	dueDate := invoiceDate
	parsed, err := time.Parse(time.DateOnly, invoiceDate)
	if err == nil {
		dueDate = parsed.AddDate(0, 0, dueDays).Format(time.DateOnly)
	}
	return InvoiceObligation{
		ID:               invoiceObligationID(bill.ID),
		BillID:           bill.ID,
		InvoiceID:        invoiceID(bill),
		Status:           invoiceObligationStatusDue,
		AmountDueMicros:  bill.TotalMicros,
		AmountPaidMicros: 0,
		CurrencyCode:     bill.CurrencyCode,
		InvoiceDate:      invoiceDate,
		DueDate:          dueDate,
	}
}

func insertBillingPeriodClose(ctx context.Context, tx *sql.Tx, close BillingPeriodClose) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO billing_period_closes (
			id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			status,
			metering_records_created,
			bill_line_items_created,
			finalized_line_item_count,
			finalized_cost_micros,
			currency_code,
			summaries_refreshed
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		close.ID,
		close.BillingPeriodStart,
		close.BillingPeriodEnd,
		close.PayerAccountID,
		close.Status,
		close.MeteringRecordsCreated,
		close.BillLineItemsCreated,
		close.FinalizedLineItemCount,
		close.FinalizedCostMicros,
		close.CurrencyCode,
		close.SummariesRefreshed,
	); err != nil {
		return fmt.Errorf("insert billing period close: %w", err)
	}
	return nil
}

func insertBill(ctx context.Context, tx *sql.Tx, bill Bill) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO bills (
			id,
			close_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			bill_state,
			currency_code,
			line_item_count,
			usage_charge_micros,
			credit_micros,
			refund_micros,
			tax_micros,
			total_micros
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bill.ID,
		bill.CloseID,
		bill.BillingPeriodStart,
		bill.BillingPeriodEnd,
		bill.PayerAccountID,
		bill.BillState,
		bill.CurrencyCode,
		bill.LineItemCount,
		bill.UsageChargeMicros,
		bill.CreditMicros,
		bill.RefundMicros,
		bill.TaxMicros,
		bill.TotalMicros,
	); err != nil {
		return fmt.Errorf("insert bill: %w", err)
	}
	return nil
}

func insertInvoiceObligation(ctx context.Context, tx *sql.Tx, obligation InvoiceObligation) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO invoice_obligations (
			id,
			bill_id,
			invoice_id,
			status,
			amount_due_micros,
			amount_paid_micros,
			currency_code,
			invoice_date,
			due_date
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		obligation.ID,
		obligation.BillID,
		obligation.InvoiceID,
		obligation.Status,
		obligation.AmountDueMicros,
		obligation.AmountPaidMicros,
		obligation.CurrencyCode,
		obligation.InvoiceDate,
		obligation.DueDate,
	); err != nil {
		return fmt.Errorf("insert invoice obligation: %w", err)
	}
	return nil
}

type billingPeriodCloseRow interface {
	Scan(dest ...any) error
}

func scanBillingPeriodClose(row billingPeriodCloseRow) (BillingPeriodClose, error) {
	var close BillingPeriodClose
	if err := row.Scan(
		&close.ID,
		&close.BillingPeriodStart,
		&close.BillingPeriodEnd,
		&close.PayerAccountID,
		&close.Status,
		&close.MeteringRecordsCreated,
		&close.BillLineItemsCreated,
		&close.FinalizedLineItemCount,
		&close.FinalizedCostMicros,
		&close.CurrencyCode,
		&close.SummariesRefreshed,
		&close.ClosedAt,
	); err != nil {
		return BillingPeriodClose{}, fmt.Errorf("scan billing period close: %w", err)
	}
	return close, nil
}

type billRow interface {
	Scan(dest ...any) error
}

func scanBill(row billRow) (Bill, error) {
	var bill Bill
	if err := row.Scan(
		&bill.ID,
		&bill.CloseID,
		&bill.BillingPeriodStart,
		&bill.BillingPeriodEnd,
		&bill.PayerAccountID,
		&bill.BillState,
		&bill.CurrencyCode,
		&bill.LineItemCount,
		&bill.UsageChargeMicros,
		&bill.CreditMicros,
		&bill.RefundMicros,
		&bill.TaxMicros,
		&bill.TotalMicros,
		&bill.IssuedAt,
		&bill.CreatedAt,
	); err != nil {
		return Bill{}, fmt.Errorf("scan bill: %w", err)
	}
	return bill, nil
}

type invoiceObligationRow interface {
	Scan(dest ...any) error
}

func scanInvoiceObligation(row invoiceObligationRow) (InvoiceObligation, error) {
	var obligation InvoiceObligation
	if err := row.Scan(
		&obligation.ID,
		&obligation.BillID,
		&obligation.InvoiceID,
		&obligation.Status,
		&obligation.AmountDueMicros,
		&obligation.AmountPaidMicros,
		&obligation.CurrencyCode,
		&obligation.InvoiceDate,
		&obligation.DueDate,
		&obligation.CreatedAt,
		&obligation.UpdatedAt,
	); err != nil {
		return InvoiceObligation{}, fmt.Errorf("scan invoice obligation: %w", err)
	}
	return obligation, nil
}

func scanBillWithInvoiceObligation(row invoiceObligationRow) (Bill, InvoiceObligation, error) {
	var bill Bill
	var obligation InvoiceObligation
	if err := row.Scan(
		&bill.ID,
		&bill.CloseID,
		&bill.BillingPeriodStart,
		&bill.BillingPeriodEnd,
		&bill.PayerAccountID,
		&bill.BillState,
		&bill.CurrencyCode,
		&bill.LineItemCount,
		&bill.UsageChargeMicros,
		&bill.CreditMicros,
		&bill.RefundMicros,
		&bill.TaxMicros,
		&bill.TotalMicros,
		&bill.IssuedAt,
		&bill.CreatedAt,
		&obligation.ID,
		&obligation.BillID,
		&obligation.InvoiceID,
		&obligation.Status,
		&obligation.AmountDueMicros,
		&obligation.AmountPaidMicros,
		&obligation.CurrencyCode,
		&obligation.InvoiceDate,
		&obligation.DueDate,
		&obligation.CreatedAt,
		&obligation.UpdatedAt,
	); err != nil {
		return Bill{}, InvoiceObligation{}, fmt.Errorf("scan issued bill: %w", err)
	}
	return bill, obligation, nil
}

func errMatchesNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func billingPeriodCloseID(periodStart, periodEnd, payerAccountID string) string {
	return stableBillingID("bpc", periodStart, periodEnd, payerAccountID)
}

func billID(periodStart, periodEnd, payerAccountID, currencyCode string) string {
	return stableBillingID("bill", periodStart, periodEnd, payerAccountID, currencyCode)
}

func invoiceObligationID(billID string) string {
	return stableBillingID("iob", billID)
}

func stableBillingID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:8])
}

func invoiceID(bill Bill) string {
	period := strings.ReplaceAll(bill.BillingPeriodStart[:7], "-", "")
	sum := sha256.Sum256([]byte(bill.ID))
	return "SIM-INV-" + period + "-" + strings.ToUpper(hex.EncodeToString(sum[:3]))
}
