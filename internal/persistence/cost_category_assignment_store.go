package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type costCategoryAssignmentStore interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RefreshAssignmentsForOpenPeriods rebuilds Cost Category snapshots for every non-finalized line item.
func (r CostCategoryRepository) RefreshAssignmentsForOpenPeriods(ctx context.Context) (CostCategoryAssignmentRefreshResult, error) {
	if r.db == nil {
		return CostCategoryAssignmentRefreshResult{}, fmt.Errorf("database handle is required")
	}
	var result CostCategoryAssignmentRefreshResult
	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		result, err = refreshCostCategoryAssignmentsInTx(ctx, tx, "", "")
		return err
	})
	if err != nil {
		return CostCategoryAssignmentRefreshResult{}, err
	}
	return result, nil
}

// RefreshAssignmentsForBillingPeriod rebuilds Cost Category snapshots for one open billing period.
func (r CostCategoryRepository) RefreshAssignmentsForBillingPeriod(ctx context.Context, periodStart, periodEnd string) (CostCategoryAssignmentRefreshResult, error) {
	if r.db == nil {
		return CostCategoryAssignmentRefreshResult{}, fmt.Errorf("database handle is required")
	}
	periodStart = strings.TrimSpace(periodStart)
	periodEnd = strings.TrimSpace(periodEnd)
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return CostCategoryAssignmentRefreshResult{}, err
	}

	var result CostCategoryAssignmentRefreshResult
	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		result, err = refreshCostCategoryAssignmentsInTx(ctx, tx, periodStart, periodEnd)
		return err
	})
	if err != nil {
		return CostCategoryAssignmentRefreshResult{}, err
	}
	return result, nil
}

// ListLineItemAssignments reads persisted Cost Category assignments for reporting and tests.
func (r CostCategoryRepository) ListLineItemAssignments(ctx context.Context, request CostCategoryAssignmentListRequest) ([]CostCategoryLineItemAssignment, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeCostCategoryAssignmentListRequest(request)
	if err := validateCostCategoryAssignmentListRequest(request); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			line_item_id,
			cost_category_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			line_item_status,
			cost_category_name,
			category_default_value,
			assigned_value,
			assignment_source,
			matched_rule_id,
			matched_rule_order,
			matched_rule_value,
			currency_code,
			unblended_cost_micros,
			created_at,
			updated_at
		 FROM cost_category_line_item_assignments
		 WHERE (? = '' OR billing_period_start = ?)
		   AND (? = '' OR billing_period_end = ?)
		   AND (? = '' OR cost_category_id = ?)
		   AND (? = '' OR line_item_id = ?)
		 ORDER BY billing_period_start, billing_period_end, line_item_id, lower(cost_category_name), cost_category_id
		 LIMIT ?`,
		request.BillingPeriodStart,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingPeriodEnd,
		request.CostCategoryID,
		request.CostCategoryID,
		request.LineItemID,
		request.LineItemID,
		request.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category line item assignments: %w", err)
	}
	defer rows.Close()

	var assignments []CostCategoryLineItemAssignment
	for rows.Next() {
		assignment, err := scanCostCategoryLineItemAssignment(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category line item assignments: %w", err)
	}
	return assignments, nil
}

func refreshCostCategoryAssignmentsInTx(ctx context.Context, tx costCategoryAssignmentStore, periodStart, periodEnd string) (CostCategoryAssignmentRefreshResult, error) {
	evaluator, err := newCostCategoryEvaluator(ctx, tx)
	if err != nil {
		return CostCategoryAssignmentRefreshResult{}, err
	}
	result := CostCategoryAssignmentRefreshResult{
		CategoriesEvaluated: len(evaluator.categories),
	}

	items, err := listOpenCostCategoryAssignmentLineItems(ctx, tx, periodStart, periodEnd)
	if err != nil {
		return CostCategoryAssignmentRefreshResult{}, err
	}
	summaryPeriods := map[costExplorerSummaryPeriodRef]bool{}
	if periodStart != "" && periodEnd != "" {
		summaryPeriods[costExplorerSummaryPeriodRef{Start: periodStart, End: periodEnd}] = true
	}
	if len(items) == 0 {
		if _, err := refreshCostExplorerCostCategorySummariesForPeriodsInTx(ctx, tx, summaryPeriods); err != nil {
			return CostCategoryAssignmentRefreshResult{}, err
		}
		return result, nil
	}
	result.LineItemsEvaluated = len(items)

	periods := map[costExplorerSummaryPeriodRef]bool{}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `DELETE FROM cost_category_line_item_assignments WHERE line_item_id = ?`, item.ID); err != nil {
			return CostCategoryAssignmentRefreshResult{}, fmt.Errorf("clear cost category assignments for line item %q: %w", item.ID, err)
		}
		period := costExplorerSummaryPeriodRef{Start: item.BillingPeriodStart, End: item.BillingPeriodEnd}
		periods[period] = true
		summaryPeriods[period] = true
		for _, category := range evaluator.orderedCategories() {
			assignment, err := evaluator.evaluateCategory(item, category.ID, map[string]bool{})
			if err != nil {
				return CostCategoryAssignmentRefreshResult{}, err
			}
			if err := insertCostCategoryLineItemAssignment(ctx, tx, item, category, assignment); err != nil {
				return CostCategoryAssignmentRefreshResult{}, err
			}
			result.AssignmentsRefreshed++
		}
	}
	result.BillingPeriodsRefreshed = len(periods)
	if _, err := refreshCostCategorySplitAllocationsInTx(ctx, tx, periodStart, periodEnd); err != nil {
		return CostCategoryAssignmentRefreshResult{}, err
	}
	if _, err := refreshCostExplorerCostCategorySummariesForPeriodsInTx(ctx, tx, summaryPeriods); err != nil {
		return CostCategoryAssignmentRefreshResult{}, err
	}
	return result, nil
}

func listOpenCostCategoryAssignmentLineItems(ctx context.Context, q costCategoryQueryer, periodStart, periodEnd string) ([]BillLineItem, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT
			li.id,
			li.metering_record_id,
			li.usage_event_id,
			li.resource_id,
			li.billing_period_start,
			li.billing_period_end,
			li.billing_period_days,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.product_family,
			li.usage_type,
			li.operation,
			li.region_code,
			li.line_item_type,
			li.line_item_status,
			li.usage_start_time,
			li.usage_end_time,
			li.usage_quantity_micros,
			li.usage_unit,
			li.pricing_unit,
			li.pricing_quantity_micros,
			li.unblended_rate_micros,
			li.unblended_cost_micros,
			li.currency_code,
			li.price_catalog_sku,
			li.price_effective_date,
			li.tag_snapshot_json,
			li.description,
			li.created_at
		 FROM bill_line_items li
		 WHERE (? = '' OR li.billing_period_start = ?)
		   AND (? = '' OR li.billing_period_end = ?)
		   AND NOT EXISTS (
			SELECT 1
			FROM billing_period_closes c
			WHERE c.billing_period_start = li.billing_period_start
			  AND c.billing_period_end = li.billing_period_end
			  AND c.payer_account_id = li.payer_account_id
			  AND c.status = ?
		   )
		 ORDER BY li.billing_period_start, li.billing_period_end, li.usage_start_time, li.id`,
		periodStart,
		periodStart,
		periodEnd,
		periodEnd,
		billingPeriodCloseStatusClosed,
	)
	if err != nil {
		return nil, fmt.Errorf("list open cost category assignment line items: %w", err)
	}
	defer rows.Close()

	var items []BillLineItem
	for rows.Next() {
		item, err := scanBillLineItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open cost category assignment line items: %w", err)
	}
	return items, nil
}

func insertCostCategoryLineItemAssignment(ctx context.Context, tx costCategoryAssignmentStore, item BillLineItem, category CostCategory, assignment costCategoryPreviewAssignment) error {
	source := costCategoryAssignmentSourceDefault
	if assignment.Matched {
		source = costCategoryAssignmentSourceRule
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO cost_category_line_item_assignments (
			line_item_id,
			cost_category_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			line_item_status,
			cost_category_name,
			category_default_value,
			assigned_value,
			assignment_source,
			matched_rule_id,
			matched_rule_order,
			matched_rule_value,
			currency_code,
			unblended_cost_micros
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		category.ID,
		item.BillingPeriodStart,
		item.BillingPeriodEnd,
		item.PayerAccountID,
		item.UsageAccountID,
		item.LineItemStatus,
		category.Name,
		category.DefaultValue,
		assignment.Value,
		source,
		nullStringArg(assignment.RuleID),
		nullIntArg(assignment.RuleOrder),
		nullStringArg(assignment.ValueForMatchedRule()),
		item.CurrencyCode,
		item.UnblendedCostMicros,
	); err != nil {
		return fmt.Errorf("insert cost category assignment for line item %q category %q: %w", item.ID, category.Name, err)
	}
	return nil
}

func scanCostCategoryLineItemAssignment(row costCategoryRow) (CostCategoryLineItemAssignment, error) {
	var assignment CostCategoryLineItemAssignment
	var matchedRuleID, matchedRuleValue sql.NullString
	var matchedRuleOrder sql.NullInt64
	if err := row.Scan(
		&assignment.LineItemID,
		&assignment.CostCategoryID,
		&assignment.BillingPeriodStart,
		&assignment.BillingPeriodEnd,
		&assignment.PayerAccountID,
		&assignment.UsageAccountID,
		&assignment.LineItemStatus,
		&assignment.CostCategoryName,
		&assignment.CategoryDefaultValue,
		&assignment.AssignedValue,
		&assignment.AssignmentSource,
		&matchedRuleID,
		&matchedRuleOrder,
		&matchedRuleValue,
		&assignment.CurrencyCode,
		&assignment.UnblendedCostMicros,
		&assignment.CreatedAt,
		&assignment.UpdatedAt,
	); err != nil {
		return CostCategoryLineItemAssignment{}, fmt.Errorf("scan cost category line item assignment: %w", err)
	}
	assignment.MatchedRuleID = nullStringValue(matchedRuleID)
	if matchedRuleOrder.Valid {
		assignment.MatchedRuleOrder = int(matchedRuleOrder.Int64)
	}
	assignment.MatchedRuleValue = nullStringValue(matchedRuleValue)
	return assignment, nil
}

func normalizeCostCategoryAssignmentListRequest(request CostCategoryAssignmentListRequest) CostCategoryAssignmentListRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.CostCategoryID = strings.TrimSpace(request.CostCategoryID)
	request.LineItemID = strings.TrimSpace(request.LineItemID)
	if request.Limit <= 0 {
		request.Limit = defaultCostCategoryAssignmentLimit
	}
	if request.Limit > maxCostCategoryAssignmentLimit {
		request.Limit = maxCostCategoryAssignmentLimit
	}
	return request
}

func validateCostCategoryAssignmentListRequest(request CostCategoryAssignmentListRequest) error {
	if request.BillingPeriodStart == "" && request.BillingPeriodEnd == "" {
		return nil
	}
	if request.BillingPeriodStart == "" || request.BillingPeriodEnd == "" {
		return fmt.Errorf("billing period start and end are both required when filtering assignments by period")
	}
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}
