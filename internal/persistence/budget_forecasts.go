package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	budgetForecastSummaryListLimit   = 500
	budgetForecastScheduledScenario  = "scenario"
	budgetForecastScheduledGenerator = "generator"
)

// BudgetForecastRefreshRequest selects the month and clock time used to rebuild forecasts.
type BudgetForecastRefreshRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	CurrentTime        string
}

// BudgetForecastSummaryListRequest selects persisted budget forecast rows for one month.
type BudgetForecastSummaryListRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
}

// BudgetForecastSummary stores the persisted inputs and result for one budget forecast.
type BudgetForecastSummary struct {
	BudgetID                 string
	BillingPeriodStart       string
	BillingPeriodEnd         string
	CurrentTime              string
	ElapsedDays              int
	PeriodDays               int
	ActualCostMicros         int64
	RunRateForecastMicros    int64
	ScheduledEventCostMicros int64
	ForecastCostMicros       int64
	LineItemCount            int
	ScheduledUsageEventCount int
	CurrencyCode             string
	RefreshedAt              string
}

// BudgetForecastRefreshResult reports the forecast rows rebuilt for one period.
type BudgetForecastRefreshResult struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	CurrentTime        string
	Summaries          []BudgetForecastSummary
}

type budgetForecastPeriod struct {
	Start string
	End   string
	Days  int
}

type budgetScheduledForecastCost struct {
	CostMicros int64
	EventCount int
}

// RefreshForecastSummaries rebuilds persisted current-month forecast summaries for active budgets.
func (r BudgetRepository) RefreshForecastSummaries(ctx context.Context, request BudgetForecastRefreshRequest) (BudgetForecastRefreshResult, error) {
	if r.db == nil {
		return BudgetForecastRefreshResult{}, fmt.Errorf("database handle is required")
	}
	period, currentTime, err := r.resolveBudgetForecastRefreshRequest(ctx, request)
	if err != nil {
		return BudgetForecastRefreshResult{}, err
	}

	budgets, err := r.ListBudgets(ctx, BudgetListRequest{
		BillingPeriodStart: period.Start,
		BillingPeriodEnd:   period.End,
		Status:             defaultBudgetStatus,
		Limit:              defaultBudgetEvaluationListSize,
	})
	if err != nil {
		return BudgetForecastRefreshResult{}, err
	}

	scheduledCosts, err := r.scheduledForecastCostsByBudget(ctx, budgets, period, currentTime)
	if err != nil {
		return BudgetForecastRefreshResult{}, err
	}
	elapsedDays, err := budgetForecastElapsedDays(period, currentTime)
	if err != nil {
		return BudgetForecastRefreshResult{}, err
	}

	summaries := make([]BudgetForecastSummary, 0, len(budgets))
	for _, budget := range budgets {
		actualCostMicros, lineItemCount, err := r.actualCostForBudget(ctx, budget)
		if err != nil {
			return BudgetForecastRefreshResult{}, err
		}
		runRateForecastMicros := budgetRunRateForecastMicros(actualCostMicros, elapsedDays, period.Days)
		scheduledCost := scheduledCosts[budget.ID]
		summaries = append(summaries, BudgetForecastSummary{
			BudgetID:                 budget.ID,
			BillingPeriodStart:       period.Start,
			BillingPeriodEnd:         period.End,
			CurrentTime:              currentTime.Format(time.RFC3339),
			ElapsedDays:              elapsedDays,
			PeriodDays:               period.Days,
			ActualCostMicros:         actualCostMicros,
			RunRateForecastMicros:    runRateForecastMicros,
			ScheduledEventCostMicros: scheduledCost.CostMicros,
			ForecastCostMicros:       runRateForecastMicros + scheduledCost.CostMicros,
			LineItemCount:            lineItemCount,
			ScheduledUsageEventCount: scheduledCost.EventCount,
			CurrencyCode:             budget.CurrencyCode,
		})
	}

	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM budget_forecast_summaries
			 WHERE billing_period_start = ? AND billing_period_end = ?`,
			period.Start,
			period.End,
		); err != nil {
			return fmt.Errorf("clear budget forecast summaries: %w", err)
		}
		for _, summary := range summaries {
			if err := insertBudgetForecastSummary(ctx, tx, summary); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return BudgetForecastRefreshResult{}, err
	}

	persisted, err := r.ListForecastSummaries(ctx, BudgetForecastSummaryListRequest{
		BillingPeriodStart: period.Start,
		BillingPeriodEnd:   period.End,
	})
	if err != nil {
		return BudgetForecastRefreshResult{}, err
	}
	return BudgetForecastRefreshResult{
		BillingPeriodStart: period.Start,
		BillingPeriodEnd:   period.End,
		CurrentTime:        currentTime.Format(time.RFC3339),
		Summaries:          persisted,
	}, nil
}

// ListForecastSummaries returns persisted forecast summaries for one monthly budget period.
func (r BudgetRepository) ListForecastSummaries(ctx context.Context, request BudgetForecastSummaryListRequest) ([]BudgetForecastSummary, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeBudgetForecastSummaryListRequest(request)
	if err := validateMonthlyBudgetPeriod(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			budget_id,
			billing_period_start,
			billing_period_end,
			current_time_utc,
			elapsed_days,
			period_days,
			actual_cost_micros,
			run_rate_forecast_micros,
			scheduled_event_cost_micros,
			forecast_cost_micros,
			line_item_count,
			scheduled_usage_event_count,
			currency_code,
			refreshed_at
		 FROM budget_forecast_summaries
		 WHERE billing_period_start = ? AND billing_period_end = ?
		 ORDER BY forecast_cost_micros DESC, budget_id
		 LIMIT ?`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		budgetForecastSummaryListLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("list budget forecast summaries: %w", err)
	}
	defer rows.Close()

	var summaries []BudgetForecastSummary
	for rows.Next() {
		summary, err := scanBudgetForecastSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate budget forecast summaries: %w", err)
	}
	return summaries, nil
}

func (r BudgetRepository) resolveBudgetForecastRefreshRequest(ctx context.Context, request BudgetForecastRefreshRequest) (budgetForecastPeriod, time.Time, error) {
	request = normalizeBudgetForecastRefreshRequest(request)
	var currentTime time.Time
	var err error
	if request.CurrentTime == "" {
		clock, err := readSimulatorClock(ctx, r.db)
		if err != nil {
			return budgetForecastPeriod{}, time.Time{}, err
		}
		currentTime, err = parseSimulatorClockTime(clock.CurrentTime)
		if err != nil {
			return budgetForecastPeriod{}, time.Time{}, err
		}
		if request.BillingPeriodStart == "" && request.BillingPeriodEnd == "" {
			return budgetForecastPeriod{Start: clock.BillingPeriodStart, End: clock.BillingPeriodEnd, Days: clock.BillingPeriodDays}, currentTime, nil
		}
	} else {
		currentTime, err = parseSimulatorClockTime(request.CurrentTime)
		if err != nil {
			return budgetForecastPeriod{}, time.Time{}, err
		}
	}

	if request.BillingPeriodStart == "" && request.BillingPeriodEnd == "" {
		period, err := BillingPeriodForTime(currentTime)
		if err != nil {
			return budgetForecastPeriod{}, time.Time{}, err
		}
		return budgetForecastPeriod{Start: period.Start, End: period.End, Days: period.Days}, currentTime, nil
	}
	period, err := budgetForecastPeriodFromDates(request.BillingPeriodStart, request.BillingPeriodEnd)
	if err != nil {
		return budgetForecastPeriod{}, time.Time{}, err
	}
	return period, currentTime, nil
}

func (r BudgetRepository) scheduledForecastCostsByBudget(ctx context.Context, budgets []Budget, period budgetForecastPeriod, currentTime time.Time) (map[string]budgetScheduledForecastCost, error) {
	costs := map[string]budgetScheduledForecastCost{}
	if len(budgets) == 0 {
		return costs, nil
	}
	items, err := r.listScheduledForecastLineItems(ctx, period, currentTime)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return costs, nil
	}

	needsCostCategories := false
	for _, budget := range budgets {
		if budget.ScopeType == BudgetScopeCostCategory {
			needsCostCategories = true
			break
		}
	}
	var evaluator costCategoryPreviewEvaluator
	if needsCostCategories {
		evaluator, err = newCostCategoryEvaluator(ctx, r.db)
		if err != nil {
			return nil, err
		}
	}

	for _, item := range items {
		for _, budget := range budgets {
			matches, err := budgetForecastItemMatchesBudget(item, budget, evaluator)
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			cost := costs[budget.ID]
			cost.CostMicros += item.UnblendedCostMicros
			cost.EventCount++
			costs[budget.ID] = cost
		}
	}
	return costs, nil
}

func (r BudgetRepository) listScheduledForecastLineItems(ctx context.Context, period budgetForecastPeriod, currentTime time.Time) ([]BillLineItem, error) {
	periodStartTime := period.Start + "T00:00:00Z"
	periodEndTime := period.End + "T00:00:00Z"
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			u.id,
			u.resource_id,
			u.account_id,
			u.service_code,
			u.usage_type,
			u.operation,
			u.region_code,
			u.usage_start_time,
			u.usage_end_time,
			u.usage_quantity_micros,
			u.usage_unit,
			u.attributes_json,
			u.tag_snapshot_json,
			u.event_source,
			u.scenario_run_id,
			u.scenario_event_id,
			u.scenario_event_sequence,
			u.created_at
		 FROM usage_events u
		 LEFT JOIN bill_line_items b ON b.usage_event_id = u.id
		 WHERE b.id IS NULL
		   AND u.event_source IN (?, ?)
		   AND u.usage_start_time >= ?
		   AND u.usage_end_time <= ?
		   AND u.usage_end_time > ?
		 ORDER BY u.usage_start_time, u.id`,
		budgetForecastScheduledScenario,
		budgetForecastScheduledGenerator,
		periodStartTime,
		periodEndTime,
		currentTime.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("list scheduled forecast usage events: %w", err)
	}
	defer rows.Close()

	var items []BillLineItem
	for rows.Next() {
		event, err := scanUsageEvent(rows)
		if err != nil {
			return nil, err
		}
		item, err := r.forecastLineItemFromUsageEvent(ctx, event)
		if err != nil {
			return nil, err
		}
		if item.BillingPeriodStart == period.Start && item.BillingPeriodEnd == period.End {
			items = append(items, item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled forecast usage events: %w", err)
	}
	return items, nil
}

func (r BudgetRepository) forecastLineItemFromUsageEvent(ctx context.Context, event UsageEvent) (BillLineItem, error) {
	period, err := billingPeriodForUsageWindow(event.UsageStartTime, event.UsageEndTime)
	if err != nil {
		return BillLineItem{}, fmt.Errorf("forecast usage event %q: %w", event.ID, err)
	}
	lookup, err := NewPriceCatalogRepository(r.db).Lookup(ctx, PriceLookupRequest{
		ServiceCode:         event.ServiceCode,
		UsageType:           event.UsageType,
		Operation:           event.Operation,
		RegionCode:          event.RegionCode,
		UsageUnit:           event.UsageUnit,
		UsageQuantityMicros: event.UsageQuantityMicros,
		UsageDate:           period.UsageDate,
		BillingPeriodDays:   period.Days,
	})
	if err != nil {
		return BillLineItem{}, fmt.Errorf("forecast usage event %q: %w", event.ID, err)
	}
	payerAccountID, err := NewBillLineItemRepository(r.db).defaultPayerAccountID(ctx, event.AccountID)
	if err != nil {
		return BillLineItem{}, fmt.Errorf("forecast usage event %q payer: %w", event.ID, err)
	}
	if payerAccountID == "" {
		payerAccountID = event.AccountID
	}

	item := BillLineItem{
		ID:                    "forecast_" + event.ID,
		MeteringRecordID:      "forecast_meter_" + event.ID,
		UsageEventID:          event.ID,
		ResourceID:            event.ResourceID,
		BillingPeriodStart:    period.Start,
		BillingPeriodEnd:      period.End,
		BillingPeriodDays:     period.Days,
		PayerAccountID:        payerAccountID,
		UsageAccountID:        event.AccountID,
		ServiceCode:           event.ServiceCode,
		ServiceName:           lookup.Item.ServiceName,
		ProductFamily:         lookup.Item.ProductFamily,
		UsageType:             event.UsageType,
		Operation:             event.Operation,
		RegionCode:            event.RegionCode,
		LineItemType:          billLineItemTypeUsage,
		LineItemStatus:        billLineItemStatusEstimated,
		UsageStartTime:        event.UsageStartTime,
		UsageEndTime:          event.UsageEndTime,
		UsageQuantityMicros:   event.UsageQuantityMicros,
		UsageUnit:             event.UsageUnit,
		PricingUnit:           lookup.Item.Unit,
		PricingQuantityMicros: lookup.UsageQuantityMicros,
		UnblendedRateMicros:   lookup.Item.RateMicros,
		UnblendedCostMicros:   lookup.CostMicros,
		CurrencyCode:          lookup.Item.CurrencyCode,
		PriceCatalogSKU:       lookup.Item.SKU,
		PriceEffectiveDate:    lookup.Item.EffectiveDate,
		TagSnapshot:           normalizeStringMap(event.TagSnapshot),
		Description:           "Forecasted scheduled usage for " + event.ID,
	}
	if err := validateBillLineItem(item); err != nil {
		return BillLineItem{}, fmt.Errorf("forecast usage event %q: %w", event.ID, err)
	}
	return item, nil
}

func budgetForecastItemMatchesBudget(item BillLineItem, budget Budget, evaluator costCategoryPreviewEvaluator) (bool, error) {
	switch budget.ScopeType {
	case BudgetScopeAccount:
		return item.UsageAccountID == budget.ScopeValue, nil
	case BudgetScopeService:
		return item.ServiceCode == budget.ScopeValue || item.ServiceName == budget.ScopeValue, nil
	case BudgetScopeTag:
		return item.TagSnapshot[budget.ScopeKey] == budget.ScopeValue, nil
	case BudgetScopeCostCategory:
		assignment, err := evaluator.evaluateCategory(item, budget.ScopeKey, map[string]bool{})
		if err != nil {
			return false, err
		}
		return assignment.Value == budget.ScopeValue, nil
	default:
		return false, fmt.Errorf("unsupported budget scope %q", budget.ScopeType)
	}
}

func insertBudgetForecastSummary(ctx context.Context, tx *sql.Tx, summary BudgetForecastSummary) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO budget_forecast_summaries (
			budget_id,
			billing_period_start,
			billing_period_end,
			current_time_utc,
			elapsed_days,
			period_days,
			actual_cost_micros,
			run_rate_forecast_micros,
			scheduled_event_cost_micros,
			forecast_cost_micros,
			line_item_count,
			scheduled_usage_event_count,
			currency_code,
			refreshed_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
		summary.BudgetID,
		summary.BillingPeriodStart,
		summary.BillingPeriodEnd,
		summary.CurrentTime,
		summary.ElapsedDays,
		summary.PeriodDays,
		summary.ActualCostMicros,
		summary.RunRateForecastMicros,
		summary.ScheduledEventCostMicros,
		summary.ForecastCostMicros,
		summary.LineItemCount,
		summary.ScheduledUsageEventCount,
		summary.CurrencyCode,
	); err != nil {
		return fmt.Errorf("insert budget forecast summary for %q: %w", summary.BudgetID, err)
	}
	return nil
}

func (r BudgetRepository) budgetForecastSummaryMap(ctx context.Context, periodStart, periodEnd string) (map[string]BudgetForecastSummary, error) {
	summaries, err := r.ListForecastSummaries(ctx, BudgetForecastSummaryListRequest{
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
	})
	if err != nil {
		return nil, err
	}
	byBudgetID := map[string]BudgetForecastSummary{}
	for _, summary := range summaries {
		byBudgetID[summary.BudgetID] = summary
	}
	return byBudgetID, nil
}

func scanBudgetForecastSummary(row budgetForecastSummaryRow) (BudgetForecastSummary, error) {
	var summary BudgetForecastSummary
	if err := row.Scan(
		&summary.BudgetID,
		&summary.BillingPeriodStart,
		&summary.BillingPeriodEnd,
		&summary.CurrentTime,
		&summary.ElapsedDays,
		&summary.PeriodDays,
		&summary.ActualCostMicros,
		&summary.RunRateForecastMicros,
		&summary.ScheduledEventCostMicros,
		&summary.ForecastCostMicros,
		&summary.LineItemCount,
		&summary.ScheduledUsageEventCount,
		&summary.CurrencyCode,
		&summary.RefreshedAt,
	); err != nil {
		return BudgetForecastSummary{}, fmt.Errorf("scan budget forecast summary: %w", err)
	}
	return summary, nil
}

type budgetForecastSummaryRow interface {
	Scan(dest ...any) error
}

func normalizeBudgetForecastRefreshRequest(request BudgetForecastRefreshRequest) BudgetForecastRefreshRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.CurrentTime = strings.TrimSpace(request.CurrentTime)
	return request
}

func normalizeBudgetForecastSummaryListRequest(request BudgetForecastSummaryListRequest) BudgetForecastSummaryListRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	return request
}

func budgetForecastPeriodFromDates(periodStart, periodEnd string) (budgetForecastPeriod, error) {
	if err := validateMonthlyBudgetPeriod(periodStart, periodEnd); err != nil {
		return budgetForecastPeriod{}, err
	}
	start, err := time.Parse(time.DateOnly, periodStart)
	if err != nil {
		return budgetForecastPeriod{}, fmt.Errorf("forecast period start must use YYYY-MM-DD: %w", err)
	}
	end, err := time.Parse(time.DateOnly, periodEnd)
	if err != nil {
		return budgetForecastPeriod{}, fmt.Errorf("forecast period end must use YYYY-MM-DD: %w", err)
	}
	days := int(end.Sub(start).Hours() / 24)
	if days <= 0 {
		return budgetForecastPeriod{}, fmt.Errorf("forecast period days must be greater than zero")
	}
	return budgetForecastPeriod{Start: periodStart, End: periodEnd, Days: days}, nil
}

func budgetForecastElapsedDays(period budgetForecastPeriod, currentTime time.Time) (int, error) {
	periodStart, err := time.Parse(time.DateOnly, period.Start)
	if err != nil {
		return 0, fmt.Errorf("forecast period start must use YYYY-MM-DD: %w", err)
	}
	periodEnd, err := time.Parse(time.DateOnly, period.End)
	if err != nil {
		return 0, fmt.Errorf("forecast period end must use YYYY-MM-DD: %w", err)
	}
	currentTime = currentTime.UTC()
	if !currentTime.After(periodStart) {
		return 0, nil
	}
	if !currentTime.Before(periodEnd) {
		return period.Days, nil
	}
	elapsed := int(currentTime.Sub(periodStart) / (24 * time.Hour))
	if periodStart.AddDate(0, 0, elapsed).Before(currentTime) {
		elapsed++
	}
	if elapsed > period.Days {
		elapsed = period.Days
	}
	return elapsed, nil
}

func budgetRunRateForecastMicros(actualCostMicros int64, elapsedDays, periodDays int) int64 {
	if actualCostMicros <= 0 {
		return 0
	}
	if elapsedDays <= 0 || elapsedDays >= periodDays {
		return actualCostMicros
	}
	return divideAndRoundMicros(actualCostMicros*int64(periodDays), int64(elapsedDays))
}
