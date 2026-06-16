package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

type resourceBillingWorkflow struct {
	db        *sql.DB
	catalog   persistence.PriceCatalogRepository
	metering  persistence.MeteringRepository
	lineItems persistence.BillLineItemRepository
	clock     persistence.SimulatorClockRepository
	dailyJobs persistence.DailyMeteringJobRepository
	monthEnd  persistence.MonthEndCloseRepository
}

// newResourceBillingWorkflow groups the billing pipeline repositories used by the Resource Lab.
func newResourceBillingWorkflow(db *sql.DB) resourceBillingWorkflow {
	return resourceBillingWorkflow{
		db:        db,
		catalog:   persistence.NewPriceCatalogRepository(db),
		metering:  persistence.NewMeteringRepository(db),
		lineItems: persistence.NewBillLineItemRepository(db),
		clock:     persistence.NewSimulatorClockRepository(db),
		dailyJobs: persistence.NewDailyMeteringJobRepository(db),
		monthEnd:  persistence.NewMonthEndCloseRepository(db),
	}
}

// handleRunBillingPipeline converts pending usage into metering records and priced bill line items.
func (h resourceLabHandler) handleRunBillingPipeline(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before pricing usage.", "")
		return
	}
	request, err := billingPipelineRequestFromForm(r)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	request.PayerAccountID, err = payerAccountIDOrDefault(r.Context(), h.db, request.PayerAccountID)
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	meteringResult, err := h.billing.metering.GenerateMeteringRecords(r.Context())
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	lineItemResult, err := h.billing.lineItems.GenerateBillLineItems(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	clock, err := h.billing.clock.Get(r.Context())
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	summaries, err := h.billing.dailyJobs.RefreshBillingPeriodServiceSummaries(r.Context(), clock.BillingPeriodStart, clock.BillingPeriodEnd)
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Created %d metering records and %d bill line items; refreshed %d summaries",
		meteringResult.RecordsCreated,
		lineItemResult.ItemsCreated,
		len(summaries),
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleRunDailyMeteringJob runs clock-bounded metering and refreshes current-period summaries.
func (h resourceLabHandler) handleRunDailyMeteringJob(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before running daily metering.", "")
		return
	}
	request, err := dailyMeteringJobRequestFromForm(r, persistence.DailyMeteringJobTriggerOnDemand)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	request.PayerAccountID, err = payerAccountIDOrDefault(r.Context(), h.db, request.PayerAccountID)
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	result, err := h.billing.dailyJobs.Run(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Daily metering created %d metering records, %d bill line items, and refreshed %d summaries",
		result.MeteringRecordsCreated,
		result.BillLineItemsCreated,
		len(result.Summaries),
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleRunMonthEndClose finalizes the completed billing period before the current simulator clock.
func (h resourceLabHandler) handleRunMonthEndClose(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before closing a billing period.", "")
		return
	}
	request, err := monthEndCloseRequestFromForm(r)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	request.PayerAccountID, err = payerAccountIDOrDefault(r.Context(), h.db, request.PayerAccountID)
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	result, err := h.billing.monthEnd.ClosePreviousPeriod(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Month-end close finalized %d line items into bill %s for %s",
		result.FinalizedLineItems,
		result.Bill.ID,
		formatUSDMicros(result.Bill.TotalMicros),
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleAdvanceClock applies a learner-triggered deterministic time change.
func (h resourceLabHandler) handleAdvanceClock(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before advancing the clock.", "")
		return
	}
	request, err := clockAdvanceRequestFromForm(r)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	clock, err := h.billing.clock.Advance(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	defaultPayerAccountID, err := defaultBillingPayerAccountID(r.Context(), h.db, defaultUsageAccountID)
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	result, err := h.billing.dailyJobs.Run(r.Context(), persistence.DailyMeteringJobRequest{
		Trigger:        persistence.DailyMeteringJobTriggerClockAdvance,
		PayerAccountID: defaultPayerAccountID,
	})
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Advanced clock to %s; daily metering created %d metering records and %d bill line items",
		clock.CurrentTime,
		result.MeteringRecordsCreated,
		result.BillLineItemsCreated,
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// loadClockContext prepares the Resource Lab clock and default payer view fields.
func (w resourceBillingWorkflow) loadClockContext(ctx context.Context, data *resourcePageData) (persistence.SimulatorClock, error) {
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, w.db, defaultUsageAccountID)
	if err != nil {
		return persistence.SimulatorClock{}, err
	}
	data.DefaultPayerAccountID = defaultPayerAccountID

	clock, err := w.clock.Get(ctx)
	if err != nil {
		return persistence.SimulatorClock{}, err
	}
	applyClockToResourcePageData(data, clock)
	return clock, nil
}

// loadWorkflowData appends billing pipeline, close, invoice, and price catalog rows to the page model.
func (w resourceBillingWorkflow) loadWorkflowData(ctx context.Context, data *resourcePageData, resourceNames map[string]string, clock persistence.SimulatorClock) error {
	meteringRecords, err := w.metering.ListMeteringRecords(ctx, 25)
	if err != nil {
		return err
	}
	for _, record := range meteringRecords {
		data.MeteringRecords = append(data.MeteringRecords, meteringRecordViewFromRecord(record, resourceNames[record.ResourceID]))
	}

	billLineItems, err := w.lineItems.ListBillLineItems(ctx, 25)
	if err != nil {
		return err
	}
	for _, item := range billLineItems {
		data.BillLineItems = append(data.BillLineItems, billLineItemViewFromItem(item, resourceNames[item.ResourceID]))
	}

	summaries, err := w.dailyJobs.ListBillingPeriodServiceSummaries(ctx, clock.BillingPeriodStart, clock.BillingPeriodEnd)
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		data.BillingPeriodSummaries = append(data.BillingPeriodSummaries, billingPeriodSummaryViewFromSummary(summary))
	}

	runs, err := w.dailyJobs.ListRuns(ctx, 10)
	if err != nil {
		return err
	}
	for _, run := range runs {
		data.DailyMeteringJobRuns = append(data.DailyMeteringJobRuns, dailyMeteringJobRunViewFromRun(run))
	}

	closes, err := w.monthEnd.ListRecentCloses(ctx, 10)
	if err != nil {
		return err
	}
	for _, close := range closes {
		data.MonthEndCloses = append(data.MonthEndCloses, monthEndCloseViewFromClose(close))
	}

	issuedBills, err := w.monthEnd.ListIssuedBills(ctx, 10)
	if err != nil {
		return err
	}
	for _, issuedBill := range issuedBills {
		data.IssuedBills = append(data.IssuedBills, issuedBillViewFromBill(issuedBill))
	}

	catalogItems, err := w.catalog.List(ctx)
	if err != nil {
		return err
	}
	for _, item := range catalogItems {
		data.CatalogItems = append(data.CatalogItems, catalogItemViewFromCatalog(item, clock.BillingPeriodDays))
	}
	return nil
}

// clockAdvanceRequestFromForm parses the simulator clock control form.
func clockAdvanceRequestFromForm(r *http.Request) (persistence.SimulatorClockAdvanceRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.SimulatorClockAdvanceRequest{}, fmt.Errorf("parse clock form: %w", err)
	}
	amount := defaultClockAdvanceAmount
	if rawAmount := strings.TrimSpace(r.PostForm.Get("clock_advance_amount")); rawAmount != "" {
		parsedAmount, err := strconv.Atoi(rawAmount)
		if err != nil {
			return persistence.SimulatorClockAdvanceRequest{}, fmt.Errorf("clock advance amount must be a whole number: %w", err)
		}
		amount = parsedAmount
	}
	return persistence.SimulatorClockAdvanceRequest{
		Amount: amount,
		Unit:   persistence.SimulatorClockAdvanceUnit(r.PostForm.Get("clock_advance_unit")),
	}, nil
}

// billingPipelineRequestFromForm parses the manual pricing workflow form.
func billingPipelineRequestFromForm(r *http.Request) (persistence.BillLineItemGenerationRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.BillLineItemGenerationRequest{}, fmt.Errorf("parse billing pipeline form: %w", err)
	}
	return persistence.BillLineItemGenerationRequest{
		PayerAccountID: r.PostForm.Get("payer_account_id"),
	}, nil
}

// dailyMeteringJobRequestFromForm parses the on-demand daily metering workflow form.
func dailyMeteringJobRequestFromForm(r *http.Request, trigger persistence.DailyMeteringJobTrigger) (persistence.DailyMeteringJobRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.DailyMeteringJobRequest{}, fmt.Errorf("parse daily metering form: %w", err)
	}
	return persistence.DailyMeteringJobRequest{
		Trigger:        trigger,
		PayerAccountID: r.PostForm.Get("payer_account_id"),
	}, nil
}

// monthEndCloseRequestFromForm parses the previous-period close workflow form.
func monthEndCloseRequestFromForm(r *http.Request) (persistence.MonthEndCloseRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.MonthEndCloseRequest{}, fmt.Errorf("parse month-end close form: %w", err)
	}
	dueDays := 0
	if rawDueDays := strings.TrimSpace(r.PostForm.Get("invoice_due_days")); rawDueDays != "" {
		parsedDueDays, err := strconv.Atoi(rawDueDays)
		if err != nil {
			return persistence.MonthEndCloseRequest{}, fmt.Errorf("invoice due days must be a whole number: %w", err)
		}
		dueDays = parsedDueDays
	}
	return persistence.MonthEndCloseRequest{
		PayerAccountID: r.PostForm.Get("payer_account_id"),
		InvoiceDueDays: dueDays,
	}, nil
}

// clockAdvanceUnitOptions lists simulator clock increments shown on the Resource Lab page.
func clockAdvanceUnitOptions() []clockAdvanceUnitView {
	return []clockAdvanceUnitView{
		{Key: persistence.SimulatorClockAdvanceHours, Label: "Hours"},
		{Key: persistence.SimulatorClockAdvanceDays, Label: "Days"},
		{Key: persistence.SimulatorClockAdvanceBillingPeriods, Label: "Billing Periods"},
	}
}

// applyClockToResourcePageData projects simulator clock state into Resource Lab defaults.
func applyClockToResourcePageData(data *resourcePageData, clock persistence.SimulatorClock) {
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf(
		"%s to %s (%d days)",
		clock.BillingPeriodStart,
		clock.BillingPeriodEnd,
		clock.BillingPeriodDays,
	)
	parsed, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return
	}
	defaults := resourceFormDefaultsForTime(parsed)
	data.DefaultUsageStart = defaults.UsageStartLocal
	data.DefaultUsageEnd = defaults.UsageEndLocal
	data.DefaultGenerationStartDate = defaults.GenerationStartDate
}
