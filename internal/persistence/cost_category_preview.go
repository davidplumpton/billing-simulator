package persistence

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// PreviewCategory evaluates ordered rules against bill line items without writing assignments.
func (r CostCategoryRepository) PreviewCategory(ctx context.Context, request CostCategoryPreviewRequest) (CostCategoryPreview, error) {
	if r.db == nil {
		return CostCategoryPreview{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostCategoryPreviewRequest(request)
	if err := validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return CostCategoryPreview{}, err
	}
	categoryID, err := resolveCostCategoryID(ctx, r.db, request.CostCategoryID, request.CostCategoryName)
	if err != nil {
		return CostCategoryPreview{}, err
	}
	category, err := r.GetCategory(ctx, categoryID)
	if err != nil {
		return CostCategoryPreview{}, err
	}
	evaluator, err := r.newCostCategoryPreviewEvaluator(ctx)
	if err != nil {
		return CostCategoryPreview{}, err
	}
	items, err := r.listCostCategoryPreviewLineItems(ctx, request)
	if err != nil {
		return CostCategoryPreview{}, err
	}

	targetRules := evaluator.rulesByCategory[category.ID]
	preview := CostCategoryPreview{
		Category:           category,
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
		CurrencyCode:       "USD",
		RuleSummaries:      make([]CostCategoryPreviewRuleSummary, 0, len(targetRules)),
	}
	ruleSummaryIndex := map[string]int{}
	for _, rule := range targetRules {
		ruleSummaryIndex[rule.ID] = len(preview.RuleSummaries)
		preview.RuleSummaries = append(preview.RuleSummaries, CostCategoryPreviewRuleSummary{
			RuleID:                rule.ID,
			RuleOrder:             rule.RuleOrder,
			Value:                 rule.Value,
			Description:           rule.Description,
			ConditionDescriptions: costCategoryRuleConditionDescriptions(rule.Conditions),
		})
	}

	for _, item := range items {
		matchingRules, err := evaluator.matchingRules(item, category.ID, map[string]bool{})
		if err != nil {
			return CostCategoryPreview{}, err
		}

		preview.TotalLineItemCount++
		preview.TotalCostMicros += item.UnblendedCostMicros
		preview.CurrencyCode = mergeCostCategoryPreviewCurrency(preview.CurrencyCode, item.CurrencyCode)

		lineItem := CostCategoryPreviewLineItem{
			ID:             item.ID,
			ResourceID:     item.ResourceID,
			PayerAccountID: item.PayerAccountID,
			UsageAccountID: item.UsageAccountID,
			ServiceCode:    item.ServiceCode,
			ServiceName:    item.ServiceName,
			UsageType:      item.UsageType,
			LineItemType:   item.LineItemType,
			LineItemStatus: item.LineItemStatus,
			RegionCode:     item.RegionCode,
			UsageStartTime: item.UsageStartTime,
			UsageEndTime:   item.UsageEndTime,
			CurrencyCode:   item.CurrencyCode,
			CostMicros:     item.UnblendedCostMicros,
			BeforeValue:    category.DefaultValue,
			PreviewValue:   category.DefaultValue,
			TagSnapshot:    normalizeStringMap(item.TagSnapshot),
		}

		if len(matchingRules) == 0 {
			preview.UnmatchedLineItemCount++
			preview.UnmatchedCostMicros += item.UnblendedCostMicros
		} else {
			firstMatch := matchingRules[0]
			lineItem.PreviewValue = firstMatch.Value
			lineItem.MatchedRuleID = firstMatch.ID
			lineItem.MatchedRuleOrder = firstMatch.RuleOrder
			lineItem.MatchedRuleValue = firstMatch.Value
			preview.MatchedLineItemCount++
			preview.MatchedCostMicros += item.UnblendedCostMicros
			if idx, ok := ruleSummaryIndex[firstMatch.ID]; ok {
				preview.RuleSummaries[idx].MatchedLineItemCount++
				preview.RuleSummaries[idx].MatchedCostMicros += item.UnblendedCostMicros
			}
			for _, shadowedRule := range matchingRules[1:] {
				lineItem.ShadowedRules = append(lineItem.ShadowedRules, CostCategoryPreviewShadowedRule{
					RuleID:    shadowedRule.ID,
					RuleOrder: shadowedRule.RuleOrder,
					Value:     shadowedRule.Value,
				})
				if idx, ok := ruleSummaryIndex[shadowedRule.ID]; ok {
					preview.RuleSummaries[idx].ShadowedLineItemCount++
					preview.RuleSummaries[idx].ShadowedCostMicros += item.UnblendedCostMicros
				}
			}
		}

		if len(preview.LineItems) < request.LineItemLimit {
			preview.LineItems = append(preview.LineItems, lineItem)
		} else {
			preview.HasMoreLineItems = true
		}
	}
	return preview, nil
}

func (r CostCategoryRepository) listCostCategoryPreviewLineItems(ctx context.Context, request CostCategoryPreviewRequest) ([]BillLineItem, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			metering_record_id,
			usage_event_id,
			resource_id,
			billing_period_start,
			billing_period_end,
			billing_period_days,
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			line_item_type,
			line_item_status,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			pricing_unit,
			pricing_quantity_micros,
			unblended_rate_micros,
			unblended_cost_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			tag_snapshot_json,
			description,
			created_at
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		 ORDER BY usage_start_time, id`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category preview line items: %w", err)
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
		return nil, fmt.Errorf("iterate cost category preview line items: %w", err)
	}
	return items, nil
}

func normalizeCostCategoryPreviewRequest(request CostCategoryPreviewRequest) CostCategoryPreviewRequest {
	request.CostCategoryID = strings.TrimSpace(request.CostCategoryID)
	request.CostCategoryName = strings.TrimSpace(request.CostCategoryName)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	if request.LineItemLimit <= 0 {
		request.LineItemLimit = defaultCostCategoryPreviewLineItemLimit
	}
	if request.LineItemLimit > maxCostCategoryPreviewLineItemLimit {
		request.LineItemLimit = maxCostCategoryPreviewLineItemLimit
	}
	return request
}

func mergeCostCategoryPreviewCurrency(current, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return next
	}
	if current != next {
		return "mixed"
	}
	return current
}

func costCategoryRuleConditionDescriptions(conditions []CostCategoryRuleCondition) []string {
	descriptions := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		descriptions = append(descriptions, costCategoryRuleConditionDescription(condition))
	}
	return descriptions
}

func costCategoryRuleConditionDescription(condition CostCategoryRuleCondition) string {
	dimension := condition.Dimension
	switch condition.Dimension {
	case CostCategoryRuleMatchAccount:
		dimension = "account"
	case CostCategoryRuleMatchService:
		dimension = "service"
	case CostCategoryRuleMatchRegion:
		dimension = "region"
	case CostCategoryRuleMatchUsageType:
		dimension = "usage type"
	case CostCategoryRuleMatchLineItemType:
		dimension = "line item type"
	case CostCategoryRuleMatchTag:
		dimension = "tag " + condition.TagKey
	case CostCategoryRuleMatchCostCategory:
		dimension = "cost category " + condition.CostCategoryName
	}
	operator := "is"
	if condition.Operator == CostCategoryRuleOperatorNotIn {
		operator = "is not"
	}
	values := append([]string(nil), condition.Values...)
	sort.Strings(values)
	return dimension + " " + operator + " " + strings.Join(values, ", ")
}
