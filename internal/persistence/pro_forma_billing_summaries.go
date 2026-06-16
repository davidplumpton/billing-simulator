package persistence

import (
	"context"
	"fmt"
)

// ListBillingGroupSummaries aggregates generated and custom pro forma rows by group.
func (r ProFormaBillingRepository) ListBillingGroupSummaries(ctx context.Context, request ProFormaSummaryRequest) ([]ProFormaBillingGroupSummary, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaSummaryRequest(request)
	if err := validateProFormaSummaryRequest(request); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(
		ctx,
		`WITH generated AS (
			SELECT
				li.billing_group_id,
				g.name AS billing_group_name,
				li.pricing_plan_id,
				p.name AS pricing_plan_name,
				li.billing_period_start,
				li.billing_period_end,
				li.payer_account_id,
				li.currency_code,
				COUNT(*) AS source_line_item_count,
				COALESCE(SUM(li.source_cost_micros), 0) AS source_cost_micros,
				COALESCE(SUM(li.pro_forma_cost_micros), 0) AS pro_forma_cost_micros,
				COALESCE(SUM(li.adjustment_micros), 0) AS adjustment_micros
			FROM pro_forma_line_items li
			JOIN pro_forma_billing_groups g ON g.id = li.billing_group_id
			JOIN pro_forma_pricing_plans p ON p.id = li.pricing_plan_id
			WHERE li.billing_period_start = ?
			  AND li.billing_period_end = ?
			  AND (? = '' OR li.billing_group_id = ?)
			GROUP BY
				li.billing_group_id,
				g.name,
				li.pricing_plan_id,
				p.name,
				li.billing_period_start,
				li.billing_period_end,
				li.payer_account_id,
				li.currency_code
		),
		custom AS (
			SELECT
				ci.billing_group_id,
				g.name AS billing_group_name,
				g.pricing_plan_id,
				p.name AS pricing_plan_name,
				ci.billing_period_start,
				ci.billing_period_end,
				g.payer_account_id,
				ci.currency_code,
				COUNT(*) AS custom_line_item_count,
				COALESCE(SUM(ci.amount_micros), 0) AS custom_amount_micros
			FROM pro_forma_custom_line_items ci
			JOIN pro_forma_billing_groups g ON g.id = ci.billing_group_id
			JOIN pro_forma_pricing_plans p ON p.id = g.pricing_plan_id
			WHERE ci.billing_period_start = ?
			  AND ci.billing_period_end = ?
			  AND (? = '' OR ci.billing_group_id = ?)
			GROUP BY
				ci.billing_group_id,
				g.name,
				g.pricing_plan_id,
				p.name,
				ci.billing_period_start,
				ci.billing_period_end,
				g.payer_account_id,
				ci.currency_code
		),
		summary_keys AS (
			SELECT
				billing_group_id,
				billing_group_name,
				pricing_plan_id,
				pricing_plan_name,
				billing_period_start,
				billing_period_end,
				payer_account_id,
				currency_code
			FROM generated
			UNION
			SELECT
				billing_group_id,
				billing_group_name,
				pricing_plan_id,
				pricing_plan_name,
				billing_period_start,
				billing_period_end,
				payer_account_id,
				currency_code
			FROM custom
		)
		SELECT
			k.billing_group_id,
			k.billing_group_name,
			k.pricing_plan_id,
			k.pricing_plan_name,
			k.billing_period_start,
			k.billing_period_end,
			k.payer_account_id,
			k.currency_code,
			COALESCE(g.source_line_item_count, 0),
			COALESCE(c.custom_line_item_count, 0),
			COALESCE(g.source_cost_micros, 0),
			COALESCE(c.custom_amount_micros, 0),
			COALESCE(g.pro_forma_cost_micros, 0) + COALESCE(c.custom_amount_micros, 0),
			COALESCE(g.adjustment_micros, 0) + COALESCE(c.custom_amount_micros, 0)
		FROM summary_keys k
		LEFT JOIN generated g ON g.billing_group_id = k.billing_group_id
			AND g.pricing_plan_id = k.pricing_plan_id
			AND g.billing_period_start = k.billing_period_start
			AND g.billing_period_end = k.billing_period_end
			AND g.payer_account_id = k.payer_account_id
			AND g.currency_code = k.currency_code
		LEFT JOIN custom c ON c.billing_group_id = k.billing_group_id
			AND c.pricing_plan_id = k.pricing_plan_id
			AND c.billing_period_start = k.billing_period_start
			AND c.billing_period_end = k.billing_period_end
			AND c.payer_account_id = k.payer_account_id
			AND c.currency_code = k.currency_code
		ORDER BY k.billing_group_name, k.currency_code`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingGroupID,
		request.BillingGroupID,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingGroupID,
		request.BillingGroupID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pro forma billing group summaries: %w", err)
	}
	defer rows.Close()
	var summaries []ProFormaBillingGroupSummary
	for rows.Next() {
		var summary ProFormaBillingGroupSummary
		if err := rows.Scan(
			&summary.BillingGroupID,
			&summary.BillingGroupName,
			&summary.PricingPlanID,
			&summary.PricingPlanName,
			&summary.BillingPeriodStart,
			&summary.BillingPeriodEnd,
			&summary.PayerAccountID,
			&summary.CurrencyCode,
			&summary.SourceLineItemCount,
			&summary.CustomLineItemCount,
			&summary.SourceCostMicros,
			&summary.CustomAmountMicros,
			&summary.ProFormaCostMicros,
			&summary.AdjustmentMicros,
		); err != nil {
			return nil, fmt.Errorf("scan pro forma billing group summary: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma billing group summaries: %w", err)
	}
	return summaries, nil
}

// ListLineItems returns generated pro forma rows for one reporting period.
func (r ProFormaBillingRepository) ListLineItems(ctx context.Context, request ProFormaLineItemListRequest) ([]ProFormaLineItem, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaLineItemListRequest(request)
	if err := validateProFormaLineItemListRequest(request); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(
		ctx,
		proFormaLineItemSelectSQL+`
		 WHERE li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND (? = '' OR li.billing_group_id = ?)
		 ORDER BY li.created_at DESC, li.id DESC
		 LIMIT ?`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingGroupID,
		request.BillingGroupID,
		request.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list pro forma line items: %w", err)
	}
	defer rows.Close()
	var items []ProFormaLineItem
	for rows.Next() {
		item, err := scanProFormaLineItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma line items: %w", err)
	}
	return items, nil
}

// ListCustomLineItems returns manual pro forma adjustments for one reporting period.
func (r ProFormaBillingRepository) ListCustomLineItems(ctx context.Context, request ProFormaCustomLineItemListRequest) ([]ProFormaCustomLineItem, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaCustomLineItemListRequest(request)
	if err := validateProFormaCustomLineItemListRequest(request); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(
		ctx,
		proFormaCustomLineItemSelectSQL+`
		 WHERE ci.billing_period_start = ?
		   AND ci.billing_period_end = ?
		   AND (? = '' OR ci.billing_group_id = ?)
		 ORDER BY ci.created_at DESC, ci.id DESC
		 LIMIT ?`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingGroupID,
		request.BillingGroupID,
		request.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list pro forma custom line items: %w", err)
	}
	defer rows.Close()
	var items []ProFormaCustomLineItem
	for rows.Next() {
		item, err := scanProFormaCustomLineItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma custom line items: %w", err)
	}
	return items, nil
}
