package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultDailyMeteringJobRunLimit = 10
	maxDailyMeteringJobRunLimit     = 50
	dailyMeteringJobStatusSucceeded = "succeeded"
	defaultJobRunPayerAccountID     = "usage_account"
)

// DailyMeteringJobTrigger identifies why a daily metering job was started.
type DailyMeteringJobTrigger string

const (
	// DailyMeteringJobTriggerOnDemand is used for learner-triggered metering runs.
	DailyMeteringJobTriggerOnDemand DailyMeteringJobTrigger = "on_demand"

	// DailyMeteringJobTriggerClockAdvance is used when a simulator clock advance runs metering.
	DailyMeteringJobTriggerClockAdvance DailyMeteringJobTrigger = "clock_advance"
)

// DailyMeteringJobRequest configures one clock-bounded daily metering run.
type DailyMeteringJobRequest struct {
	Trigger        DailyMeteringJobTrigger
	PayerAccountID string
}

// DailyMeteringJobRun stores the persisted audit record for one daily metering run.
type DailyMeteringJobRun struct {
	ID                     string
	Trigger                DailyMeteringJobTrigger
	Status                 string
	ClockTime              string
	BillingPeriodStart     string
	BillingPeriodEnd       string
	PayerAccountID         string
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	SummariesRefreshed     int
	StartedAt              string
	CompletedAt            string
}

// BillingPeriodServiceSummary stores one refreshed service/account cost rollup for a period.
type BillingPeriodServiceSummary struct {
	BillingPeriodStart  string
	BillingPeriodEnd    string
	PayerAccountID      string
	UsageAccountID      string
	ServiceCode         string
	LineItemStatus      string
	CurrencyCode        string
	LineItemCount       int
	UnblendedCostMicros int64
	RefreshedAt         string
}

// DailyMeteringJobResult reports the work completed by one daily metering job.
type DailyMeteringJobResult struct {
	Run                    DailyMeteringJobRun
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	Summaries              []BillingPeriodServiceSummary
}

// DailyMeteringJobRepository runs clock-bounded daily metering and refreshes billing summaries.
type DailyMeteringJobRepository struct {
	db        *sql.DB
	clock     SimulatorClockRepository
	metering  MeteringRepository
	lineItems BillLineItemRepository
	support   SupportChargeRepository
}

// NewDailyMeteringJobRepository creates a daily metering job repository backed by a workspace database.
func NewDailyMeteringJobRepository(db *sql.DB) DailyMeteringJobRepository {
	return DailyMeteringJobRepository{
		db:        db,
		clock:     NewSimulatorClockRepository(db),
		metering:  NewMeteringRepository(db),
		lineItems: NewBillLineItemRepository(db),
		support:   NewSupportChargeRepository(db),
	}
}

// Run executes one daily metering job through the current simulator clock time.
func (r DailyMeteringJobRepository) Run(ctx context.Context, request DailyMeteringJobRequest) (DailyMeteringJobResult, error) {
	if r.db == nil {
		return DailyMeteringJobResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeDailyMeteringJobRequest(request)
	if err := validateDailyMeteringJobRequest(request); err != nil {
		return DailyMeteringJobResult{}, err
	}

	clock, err := r.clock.Get(ctx)
	if err != nil {
		return DailyMeteringJobResult{}, err
	}
	meteringResult, err := r.metering.GenerateMeteringRecordsThrough(ctx, clock.CurrentTime)
	if err != nil {
		return DailyMeteringJobResult{}, err
	}
	lineItemResult, err := r.lineItems.GenerateBillLineItemsThrough(ctx, BillLineItemGenerationRequest{
		PayerAccountID: request.PayerAccountID,
		LineItemStatus: billLineItemStatusEstimated,
	}, clock.CurrentTime)
	if err != nil {
		return DailyMeteringJobResult{}, err
	}

	periodRefs := billingPeriodRefsForDailyJob(clock, lineItemResult.Items)
	var supportItems []BillLineItem
	var supportItemsCreated int
	for _, period := range periodRefs {
		supportResult, err := r.support.GenerateSupportCharges(ctx, SupportChargeGenerationRequest{
			PayerAccountID: request.PayerAccountID,
			PeriodStart:    period.Start,
			PeriodEnd:      period.End,
			LineItemStatus: billLineItemStatusEstimated,
		})
		if err != nil {
			return DailyMeteringJobResult{}, err
		}
		supportItemsCreated += supportResult.ItemsCreated
		supportItems = append(supportItems, supportResult.Items...)
	}

	var summaries []BillingPeriodServiceSummary
	for _, period := range billingPeriodRefsForDailyJob(clock, append(lineItemResult.Items, supportItems...)) {
		refreshed, err := r.RefreshBillingPeriodServiceSummaries(ctx, period.Start, period.End)
		if err != nil {
			return DailyMeteringJobResult{}, err
		}
		summaries = append(summaries, refreshed...)
	}
	billLineItemsCreated := lineItemResult.ItemsCreated + supportItemsCreated
	run, err := r.insertRun(ctx, request, clock, meteringResult.RecordsCreated, billLineItemsCreated, len(summaries))
	if err != nil {
		return DailyMeteringJobResult{}, err
	}
	return DailyMeteringJobResult{
		Run:                    run,
		MeteringRecordsCreated: meteringResult.RecordsCreated,
		BillLineItemsCreated:   billLineItemsCreated,
		Summaries:              summaries,
	}, nil
}

// RefreshBillingPeriodServiceSummaries rebuilds derived service cost summaries for one billing period.
func (r DailyMeteringJobRepository) RefreshBillingPeriodServiceSummaries(ctx context.Context, periodStart, periodEnd string) ([]BillingPeriodServiceSummary, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return nil, err
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM billing_period_service_summaries
			 WHERE billing_period_start = ? AND billing_period_end = ?`,
			periodStart,
			periodEnd,
		); err != nil {
			return fmt.Errorf("clear billing period service summaries: %w", err)
		}
		if _, err := tx.ExecContext(
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
		); err != nil {
			return fmt.Errorf("refresh billing period service summaries: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return r.ListBillingPeriodServiceSummaries(ctx, periodStart, periodEnd)
}

// ListBillingPeriodServiceSummaries reads refreshed service summaries for a billing period.
func (r DailyMeteringJobRepository) ListBillingPeriodServiceSummaries(ctx context.Context, periodStart, periodEnd string) ([]BillingPeriodServiceSummary, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
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
		 FROM billing_period_service_summaries
		 WHERE billing_period_start = ? AND billing_period_end = ?
		 ORDER BY payer_account_id, usage_account_id, service_code, line_item_status, currency_code`,
		periodStart,
		periodEnd,
	)
	if err != nil {
		return nil, fmt.Errorf("list billing period service summaries: %w", err)
	}
	defer rows.Close()

	var summaries []BillingPeriodServiceSummary
	for rows.Next() {
		summary, err := scanBillingPeriodServiceSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate billing period service summaries: %w", err)
	}
	return summaries, nil
}

// ListRuns reads recent daily metering job runs in newest-first order.
func (r DailyMeteringJobRepository) ListRuns(ctx context.Context, limit int) ([]DailyMeteringJobRun, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if limit <= 0 {
		limit = defaultDailyMeteringJobRunLimit
	}
	if limit > maxDailyMeteringJobRunLimit {
		limit = maxDailyMeteringJobRunLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			trigger_source,
			status,
			clock_time_utc,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			metering_records_created,
			bill_line_items_created,
			summaries_refreshed,
			started_at,
			completed_at
		 FROM daily_metering_job_runs
		 ORDER BY completed_at DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list daily metering job runs: %w", err)
	}
	defer rows.Close()

	var runs []DailyMeteringJobRun
	for rows.Next() {
		run, err := scanDailyMeteringJobRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily metering job runs: %w", err)
	}
	return runs, nil
}

func (r DailyMeteringJobRepository) insertRun(
	ctx context.Context,
	request DailyMeteringJobRequest,
	clock SimulatorClock,
	meteringRecordsCreated int,
	billLineItemsCreated int,
	summariesRefreshed int,
) (DailyMeteringJobRun, error) {
	id, err := newRepositoryID("job")
	if err != nil {
		return DailyMeteringJobRun{}, err
	}
	if request.PayerAccountID == "" {
		request.PayerAccountID = defaultJobRunPayerAccountID
	}
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO daily_metering_job_runs (
			id,
			trigger_source,
			status,
			clock_time_utc,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			metering_records_created,
			bill_line_items_created,
			summaries_refreshed
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		string(request.Trigger),
		dailyMeteringJobStatusSucceeded,
		clock.CurrentTime,
		clock.BillingPeriodStart,
		clock.BillingPeriodEnd,
		request.PayerAccountID,
		meteringRecordsCreated,
		billLineItemsCreated,
		summariesRefreshed,
	); err != nil {
		return DailyMeteringJobRun{}, fmt.Errorf("insert daily metering job run: %w", err)
	}
	return r.getRun(ctx, id)
}

func (r DailyMeteringJobRepository) getRun(ctx context.Context, id string) (DailyMeteringJobRun, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			trigger_source,
			status,
			clock_time_utc,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			metering_records_created,
			bill_line_items_created,
			summaries_refreshed,
			started_at,
			completed_at
		 FROM daily_metering_job_runs
		 WHERE id = ?`,
		id,
	)
	run, err := scanDailyMeteringJobRun(row)
	if err != nil {
		return DailyMeteringJobRun{}, fmt.Errorf("get daily metering job run %q: %w", id, err)
	}
	return run, nil
}

type dailyMeteringJobRunRow interface {
	Scan(dest ...any) error
}

func scanDailyMeteringJobRun(row dailyMeteringJobRunRow) (DailyMeteringJobRun, error) {
	var run DailyMeteringJobRun
	if err := row.Scan(
		&run.ID,
		&run.Trigger,
		&run.Status,
		&run.ClockTime,
		&run.BillingPeriodStart,
		&run.BillingPeriodEnd,
		&run.PayerAccountID,
		&run.MeteringRecordsCreated,
		&run.BillLineItemsCreated,
		&run.SummariesRefreshed,
		&run.StartedAt,
		&run.CompletedAt,
	); err != nil {
		return DailyMeteringJobRun{}, fmt.Errorf("scan daily metering job run: %w", err)
	}
	return run, nil
}

type billingPeriodServiceSummaryRow interface {
	Scan(dest ...any) error
}

func scanBillingPeriodServiceSummary(row billingPeriodServiceSummaryRow) (BillingPeriodServiceSummary, error) {
	var summary BillingPeriodServiceSummary
	if err := row.Scan(
		&summary.BillingPeriodStart,
		&summary.BillingPeriodEnd,
		&summary.PayerAccountID,
		&summary.UsageAccountID,
		&summary.ServiceCode,
		&summary.LineItemStatus,
		&summary.CurrencyCode,
		&summary.LineItemCount,
		&summary.UnblendedCostMicros,
		&summary.RefreshedAt,
	); err != nil {
		return BillingPeriodServiceSummary{}, fmt.Errorf("scan billing period service summary: %w", err)
	}
	return summary, nil
}

func normalizeDailyMeteringJobRequest(request DailyMeteringJobRequest) DailyMeteringJobRequest {
	if request.Trigger == "" {
		request.Trigger = DailyMeteringJobTriggerOnDemand
	}
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	return request
}

func validateDailyMeteringJobRequest(request DailyMeteringJobRequest) error {
	switch request.Trigger {
	case DailyMeteringJobTriggerOnDemand, DailyMeteringJobTriggerClockAdvance:
		return nil
	default:
		return fmt.Errorf("unsupported daily metering job trigger %q", request.Trigger)
	}
}

func validateBillingPeriodDateRange(periodStart, periodEnd string) error {
	start, err := time.Parse(time.DateOnly, strings.TrimSpace(periodStart))
	if err != nil {
		return fmt.Errorf("billing period start must use YYYY-MM-DD: %w", err)
	}
	end, err := time.Parse(time.DateOnly, strings.TrimSpace(periodEnd))
	if err != nil {
		return fmt.Errorf("billing period end must use YYYY-MM-DD: %w", err)
	}
	if !start.Before(end) {
		return fmt.Errorf("billing period start must be before end")
	}
	return nil
}

type billingPeriodRef struct {
	Start string
	End   string
}

func billingPeriodRefsForDailyJob(clock SimulatorClock, items []BillLineItem) []billingPeriodRef {
	refs := map[string]billingPeriodRef{}
	addBillingPeriodRef(refs, clock.BillingPeriodStart, clock.BillingPeriodEnd)
	for _, item := range items {
		addBillingPeriodRef(refs, item.BillingPeriodStart, item.BillingPeriodEnd)
	}

	ordered := make([]billingPeriodRef, 0, len(refs))
	for _, ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Start == ordered[j].Start {
			return ordered[i].End < ordered[j].End
		}
		return ordered[i].Start < ordered[j].Start
	})
	return ordered
}

func addBillingPeriodRef(refs map[string]billingPeriodRef, periodStart, periodEnd string) {
	periodStart = strings.TrimSpace(periodStart)
	periodEnd = strings.TrimSpace(periodEnd)
	if periodStart == "" || periodEnd == "" {
		return
	}
	key := periodStart + "\x00" + periodEnd
	refs[key] = billingPeriodRef{Start: periodStart, End: periodEnd}
}
