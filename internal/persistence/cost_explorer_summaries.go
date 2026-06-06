package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CostExplorerSummaryRefreshResult reports how many derived Cost Explorer rows were rebuilt.
type CostExplorerSummaryRefreshResult struct {
	DailyCostRows             int
	MonthlyAccountServiceRows int
	TagCoverageRows           int
	CostCategoryRows          int
}

type costExplorerSummaryStore interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type costExplorerSummaryPeriodRef struct {
	Start string
	End   string
}

// RefreshSummariesForBillingPeriod rebuilds materialized Cost Explorer summaries for one period.
func (r CostExplorerRepository) RefreshSummariesForBillingPeriod(ctx context.Context, periodStart, periodEnd string) (CostExplorerSummaryRefreshResult, error) {
	if r.db == nil {
		return CostExplorerSummaryRefreshResult{}, fmt.Errorf("database handle is required")
	}
	periodStart = strings.TrimSpace(periodStart)
	periodEnd = strings.TrimSpace(periodEnd)
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return CostExplorerSummaryRefreshResult{}, err
	}

	var result CostExplorerSummaryRefreshResult
	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		result, err = refreshCostExplorerSummariesInTx(ctx, tx, periodStart, periodEnd)
		return err
	})
	if err != nil {
		return CostExplorerSummaryRefreshResult{}, err
	}
	return result, nil
}

func refreshCostExplorerSummariesInTx(ctx context.Context, store costExplorerSummaryStore, periodStart, periodEnd string) (CostExplorerSummaryRefreshResult, error) {
	periodStart = strings.TrimSpace(periodStart)
	periodEnd = strings.TrimSpace(periodEnd)
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return CostExplorerSummaryRefreshResult{}, err
	}

	var result CostExplorerSummaryRefreshResult
	dailyRows, err := refreshDailyCostSummaryInTx(ctx, store, periodStart, periodEnd)
	if err != nil {
		return CostExplorerSummaryRefreshResult{}, err
	}
	result.DailyCostRows = dailyRows

	monthlyRows, err := refreshMonthlyAccountServiceSummaryInTx(ctx, store, periodStart, periodEnd)
	if err != nil {
		return CostExplorerSummaryRefreshResult{}, err
	}
	result.MonthlyAccountServiceRows = monthlyRows

	tagRows, err := refreshTagCoverageSummaryInTx(ctx, store, periodStart, periodEnd)
	if err != nil {
		return CostExplorerSummaryRefreshResult{}, err
	}
	result.TagCoverageRows = tagRows

	categoryRows, err := refreshCostExplorerCostCategorySummaryInTx(ctx, store, periodStart, periodEnd)
	if err != nil {
		return CostExplorerSummaryRefreshResult{}, err
	}
	result.CostCategoryRows = categoryRows
	return result, nil
}

func refreshDailyCostSummaryInTx(ctx context.Context, store costExplorerSummaryStore, periodStart, periodEnd string) (int, error) {
	if _, err := store.ExecContext(
		ctx,
		`DELETE FROM daily_cost_summary
		 WHERE billing_period_start = ? AND billing_period_end = ?`,
		periodStart,
		periodEnd,
	); err != nil {
		return 0, fmt.Errorf("clear daily cost summary: %w", err)
	}
	result, err := store.ExecContext(
		ctx,
		`INSERT INTO daily_cost_summary (
			billing_period_start,
			billing_period_end,
			usage_date,
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			line_item_type,
			line_item_status,
			currency_code,
			line_item_count,
			usage_quantity_micros,
			unblended_cost_micros,
			refreshed_at
		 )
		 SELECT
			billing_period_start,
			billing_period_end,
			substr(usage_start_time, 1, 10),
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			line_item_type,
			line_item_status,
			currency_code,
			COUNT(*),
			COALESCE(SUM(usage_quantity_micros), 0),
			COALESCE(SUM(unblended_cost_micros), 0),
			strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 FROM bill_line_items
		 WHERE billing_period_start = ? AND billing_period_end = ?
		 GROUP BY
			billing_period_start,
			billing_period_end,
			substr(usage_start_time, 1, 10),
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			line_item_type,
			line_item_status,
			currency_code`,
		periodStart,
		periodEnd,
	)
	if err != nil {
		return 0, fmt.Errorf("refresh daily cost summary: %w", err)
	}
	return costExplorerSummaryRowsAffected(result, "daily cost summary")
}

func refreshMonthlyAccountServiceSummaryInTx(ctx context.Context, store costExplorerSummaryStore, periodStart, periodEnd string) (int, error) {
	if _, err := store.ExecContext(
		ctx,
		`DELETE FROM monthly_account_service_summary
		 WHERE billing_period_start = ? AND billing_period_end = ?`,
		periodStart,
		periodEnd,
	); err != nil {
		return 0, fmt.Errorf("clear monthly account service summary: %w", err)
	}
	result, err := store.ExecContext(
		ctx,
		`INSERT INTO monthly_account_service_summary (
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			line_item_type,
			line_item_status,
			currency_code,
			line_item_count,
			usage_quantity_micros,
			unblended_cost_micros,
			refreshed_at
		 )
		 SELECT
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			line_item_type,
			line_item_status,
			currency_code,
			COUNT(*),
			COALESCE(SUM(usage_quantity_micros), 0),
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
			service_name,
			line_item_type,
			line_item_status,
			currency_code`,
		periodStart,
		periodEnd,
	)
	if err != nil {
		return 0, fmt.Errorf("refresh monthly account service summary: %w", err)
	}
	return costExplorerSummaryRowsAffected(result, "monthly account service summary")
}

func refreshTagCoverageSummaryInTx(ctx context.Context, store costExplorerSummaryStore, periodStart, periodEnd string) (int, error) {
	if _, err := store.ExecContext(
		ctx,
		`DELETE FROM tag_coverage_summary
		 WHERE billing_period_start = ? AND billing_period_end = ?`,
		periodStart,
		periodEnd,
	); err != nil {
		return 0, fmt.Errorf("clear tag coverage summary: %w", err)
	}

	keys, err := listCostExplorerSummaryTagKeys(ctx, store)
	if err != nil {
		return 0, err
	}
	items, err := listCostExplorerSummaryCoverageLineItems(ctx, store, periodStart, periodEnd)
	if err != nil {
		return 0, err
	}
	rows := costExplorerSummaryCoverageRows(keys, items)
	for _, row := range rows {
		caseMismatchKeys := row.CaseMismatchKeys
		if caseMismatchKeys == nil {
			caseMismatchKeys = []string{}
		}
		caseMismatchKeysJSON, err := json.Marshal(caseMismatchKeys)
		if err != nil {
			return 0, fmt.Errorf("marshal tag coverage case-mismatch keys for %q: %w", row.Key, err)
		}
		if _, err := store.ExecContext(
			ctx,
			`INSERT INTO tag_coverage_summary (
				billing_period_start,
				billing_period_end,
				tag_key,
				dimension,
				dimension_value,
				dimension_label,
				activation_status,
				cost_explorer_visible_at,
				currency_code,
				line_item_count,
				resource_count,
				tagged_line_item_count,
				tagged_resource_count,
				untagged_line_item_count,
				untagged_resource_count,
				case_mismatch_line_item_count,
				case_mismatch_resource_count,
				total_cost_micros,
				tagged_cost_micros,
				untagged_cost_micros,
				case_mismatch_cost_micros,
				case_mismatch_keys_json,
				refreshed_at
			 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
			periodStart,
			periodEnd,
			row.Key,
			row.Dimension,
			row.DimensionValue,
			row.DimensionLabel,
			row.ActivationStatus,
			nullStringArg(row.CostExplorerVisibleAt),
			row.CurrencyCode,
			row.LineItemCount,
			row.ResourceCount,
			row.TaggedLineItemCount,
			row.TaggedResourceCount,
			row.UntaggedLineItemCount,
			row.UntaggedResourceCount,
			row.CaseMismatchLineItemCount,
			row.CaseMismatchResourceCount,
			row.TotalCostMicros,
			row.TaggedCostMicros,
			row.UntaggedCostMicros,
			row.CaseMismatchCostMicros,
			string(caseMismatchKeysJSON),
		); err != nil {
			return 0, fmt.Errorf("insert tag coverage summary for %q: %w", row.Key, err)
		}
	}
	return len(rows), nil
}

func refreshCostExplorerCostCategorySummaryInTx(ctx context.Context, store costExplorerSummaryStore, periodStart, periodEnd string) (int, error) {
	if _, err := store.ExecContext(
		ctx,
		`DELETE FROM cost_category_summary
		 WHERE billing_period_start = ? AND billing_period_end = ?`,
		periodStart,
		periodEnd,
	); err != nil {
		return 0, fmt.Errorf("clear cost category summary: %w", err)
	}
	result, err := store.ExecContext(
		ctx,
		`INSERT INTO cost_category_summary (
			billing_period_start,
			billing_period_end,
			cost_category_id,
			cost_category_name,
			assigned_value,
			payer_account_id,
			usage_account_id,
			line_item_status,
			currency_code,
			line_item_count,
			unblended_cost_micros,
			refreshed_at
		 )
		 SELECT
			billing_period_start,
			billing_period_end,
			cost_category_id,
			cost_category_name,
			assigned_value,
			payer_account_id,
			usage_account_id,
			line_item_status,
			currency_code,
			COUNT(*),
			COALESCE(SUM(unblended_cost_micros), 0),
			strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 FROM cost_category_line_item_assignments
		 WHERE billing_period_start = ? AND billing_period_end = ?
		 GROUP BY
			billing_period_start,
			billing_period_end,
			cost_category_id,
			cost_category_name,
			assigned_value,
			payer_account_id,
			usage_account_id,
			line_item_status,
			currency_code`,
		periodStart,
		periodEnd,
	)
	if err != nil {
		return 0, fmt.Errorf("refresh cost category summary: %w", err)
	}
	return costExplorerSummaryRowsAffected(result, "cost category summary")
}

func refreshCostExplorerCostCategorySummariesForPeriodsInTx(ctx context.Context, store costExplorerSummaryStore, periods map[costExplorerSummaryPeriodRef]bool) (int, error) {
	periodRefs := make([]costExplorerSummaryPeriodRef, 0, len(periods))
	for period := range periods {
		if period.Start != "" && period.End != "" {
			periodRefs = append(periodRefs, period)
		}
	}
	sort.Slice(periodRefs, func(i, j int) bool {
		if periodRefs[i].Start != periodRefs[j].Start {
			return periodRefs[i].Start < periodRefs[j].Start
		}
		return periodRefs[i].End < periodRefs[j].End
	})

	rows := 0
	for _, period := range periodRefs {
		refreshed, err := refreshCostExplorerCostCategorySummaryInTx(ctx, store, period.Start, period.End)
		if err != nil {
			return 0, err
		}
		rows += refreshed
	}
	return rows, nil
}

func listCostExplorerSummaryTagKeys(ctx context.Context, store costExplorerSummaryStore) ([]CostAllocationTagKey, error) {
	rows, err := store.QueryContext(
		ctx,
		`SELECT
			tag_key,
			tag_type,
			first_seen_at,
			last_seen_at,
			discovered_at,
			activation_status,
			activated_at,
			deactivated_at,
			cost_explorer_visible_at,
			cur_export_visible_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		 FROM cost_allocation_tag_keys
		 ORDER BY lower(tag_key), tag_key`,
	)
	if err != nil {
		return nil, fmt.Errorf("list Cost Explorer summary tag keys: %w", err)
	}
	defer rows.Close()
	return scanCostAllocationTagKeys(rows)
}

func listCostExplorerSummaryCoverageLineItems(ctx context.Context, store costExplorerSummaryStore, periodStart, periodEnd string) ([]costAllocationCoverageLineItem, error) {
	rows, err := store.QueryContext(
		ctx,
		`SELECT
			id,
			resource_id,
			usage_account_id,
			service_code,
			service_name,
			currency_code,
			unblended_cost_micros,
			tag_snapshot_json
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		 ORDER BY usage_start_time, id`,
		periodStart,
		periodEnd,
	)
	if err != nil {
		return nil, fmt.Errorf("list Cost Explorer summary coverage line items: %w", err)
	}
	defer rows.Close()

	var items []costAllocationCoverageLineItem
	for rows.Next() {
		var item costAllocationCoverageLineItem
		var resourceID sql.NullString
		var tagSnapshotJSON string
		if err := rows.Scan(
			&item.ID,
			&resourceID,
			&item.UsageAccountID,
			&item.ServiceCode,
			&item.ServiceName,
			&item.CurrencyCode,
			&item.UnblendedCostMicros,
			&tagSnapshotJSON,
		); err != nil {
			return nil, fmt.Errorf("scan Cost Explorer summary coverage line item: %w", err)
		}
		item.ResourceID = nullStringValue(resourceID)
		var err error
		item.TagSnapshot, err = unmarshalStringMap(tagSnapshotJSON)
		if err != nil {
			return nil, fmt.Errorf("decode summary coverage tag snapshot for line item %q: %w", item.ID, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Cost Explorer summary coverage line items: %w", err)
	}
	return items, nil
}

func costExplorerSummaryCoverageRows(keys []CostAllocationTagKey, items []costAllocationCoverageLineItem) []CostAllocationTagCoverageRow {
	accumulators := map[string]*costAllocationCoverageAccumulator{}
	ensureAccumulator := func(key CostAllocationTagKey, dimension, dimensionValue, dimensionLabel string) *costAllocationCoverageAccumulator {
		accumulatorKey := costAllocationCoverageAccumulatorKey(key.Key, dimension, dimensionValue)
		accumulator := accumulators[accumulatorKey]
		if accumulator == nil {
			accumulator = newCostAllocationCoverageAccumulator(CostAllocationTagCoverageRow{
				Key:                   key.Key,
				Dimension:             dimension,
				DimensionValue:        dimensionValue,
				DimensionLabel:        dimensionLabel,
				ActivationStatus:      key.ActivationStatus,
				CostExplorerVisibleAt: key.CostExplorerVisibleAt,
			})
			accumulators[accumulatorKey] = accumulator
		}
		return accumulator
	}

	for _, key := range keys {
		ensureAccumulator(key, CostAllocationCoverageDimensionKey, key.Key, "All billed spend")
	}
	for _, key := range keys {
		for _, item := range items {
			exactMatch, caseMismatchKeys := costAllocationTagCoverageMatch(key.Key, item.TagSnapshot)
			ensureAccumulator(key, CostAllocationCoverageDimensionKey, key.Key, "All billed spend").add(item, exactMatch, caseMismatchKeys)
			ensureAccumulator(key, CostAllocationCoverageDimensionAccount, item.UsageAccountID, item.UsageAccountID).add(item, exactMatch, caseMismatchKeys)
			serviceLabel := item.ServiceName
			if serviceLabel == "" {
				serviceLabel = item.ServiceCode
			}
			ensureAccumulator(key, CostAllocationCoverageDimensionService, item.ServiceCode, serviceLabel).add(item, exactMatch, caseMismatchKeys)
		}
	}

	rows := make([]CostAllocationTagCoverageRow, 0, len(accumulators))
	for _, accumulator := range accumulators {
		rows = append(rows, accumulator.rowValue())
	}
	sortCostAllocationCoverageRows(rows)
	return rows
}

func costExplorerSummaryPeriodRefsForLineItems(items []BillLineItem) []costExplorerSummaryPeriodRef {
	seen := map[costExplorerSummaryPeriodRef]bool{}
	for _, item := range items {
		if item.BillingPeriodStart == "" || item.BillingPeriodEnd == "" {
			continue
		}
		seen[costExplorerSummaryPeriodRef{Start: item.BillingPeriodStart, End: item.BillingPeriodEnd}] = true
	}
	periods := make([]costExplorerSummaryPeriodRef, 0, len(seen))
	for period := range seen {
		periods = append(periods, period)
	}
	sort.Slice(periods, func(i, j int) bool {
		if periods[i].Start != periods[j].Start {
			return periods[i].Start < periods[j].Start
		}
		return periods[i].End < periods[j].End
	})
	return periods
}

func costExplorerSummaryRowsAffected(result sql.Result, label string) (int, error) {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read refreshed %s count: %w", label, err)
	}
	return int(rowsAffected), nil
}
