package persistence

import "fmt"

type proFormaPricingPlanRow interface {
	Scan(dest ...any) error
}

type proFormaPricingRuleRow interface {
	Scan(dest ...any) error
}

type proFormaBillingGroupRow interface {
	Scan(dest ...any) error
}

type proFormaBillingGroupAccountRow interface {
	Scan(dest ...any) error
}

type proFormaLineItemRow interface {
	Scan(dest ...any) error
}

type proFormaCustomLineItemRow interface {
	Scan(dest ...any) error
}

func scanProFormaPricingPlan(row proFormaPricingPlanRow) (ProFormaPricingPlan, error) {
	var plan ProFormaPricingPlan
	if err := row.Scan(
		&plan.ID,
		&plan.Name,
		&plan.Description,
		&plan.CurrencyCode,
		&plan.Status,
		&plan.RuleCount,
		&plan.CreatedAt,
		&plan.UpdatedAt,
	); err != nil {
		return ProFormaPricingPlan{}, fmt.Errorf("scan pro forma pricing plan: %w", err)
	}
	return plan, nil
}

func scanProFormaPricingRule(row proFormaPricingRuleRow) (ProFormaPricingRule, error) {
	var rule ProFormaPricingRule
	if err := row.Scan(
		&rule.ID,
		&rule.PricingPlanID,
		&rule.PricingPlanName,
		&rule.ServiceCode,
		&rule.RateMultiplierBasisPoints,
		&rule.Description,
		&rule.Status,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	); err != nil {
		return ProFormaPricingRule{}, fmt.Errorf("scan pro forma pricing rule: %w", err)
	}
	return rule, nil
}

func scanProFormaBillingGroup(row proFormaBillingGroupRow) (ProFormaBillingGroup, error) {
	var group ProFormaBillingGroup
	if err := row.Scan(
		&group.ID,
		&group.Name,
		&group.Description,
		&group.PayerAccountID,
		&group.PricingPlanID,
		&group.PricingPlanName,
		&group.Status,
		&group.AccountCount,
		&group.CreatedAt,
		&group.UpdatedAt,
	); err != nil {
		return ProFormaBillingGroup{}, fmt.Errorf("scan pro forma billing group: %w", err)
	}
	return group, nil
}

func scanProFormaBillingGroupAccount(row proFormaBillingGroupAccountRow) (ProFormaBillingGroupAccount, error) {
	var assignment ProFormaBillingGroupAccount
	if err := row.Scan(
		&assignment.ID,
		&assignment.BillingGroupID,
		&assignment.AccountID,
		&assignment.CreatedAt,
	); err != nil {
		return ProFormaBillingGroupAccount{}, fmt.Errorf("scan pro forma billing group account: %w", err)
	}
	return assignment, nil
}

func scanProFormaLineItem(row proFormaLineItemRow) (ProFormaLineItem, error) {
	var item ProFormaLineItem
	if err := row.Scan(
		&item.ID,
		&item.SourceBillLineItemID,
		&item.BillingGroupID,
		&item.BillingGroupName,
		&item.PricingPlanID,
		&item.PricingPlanName,
		&item.PricingRuleID,
		&item.BillingPeriodStart,
		&item.BillingPeriodEnd,
		&item.PayerAccountID,
		&item.UsageAccountID,
		&item.ServiceCode,
		&item.ServiceName,
		&item.UsageType,
		&item.LineItemStatus,
		&item.CurrencyCode,
		&item.SourceRateMicros,
		&item.SourceCostMicros,
		&item.RateMultiplierBasisPoints,
		&item.ProFormaRateMicros,
		&item.ProFormaCostMicros,
		&item.AdjustmentMicros,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return ProFormaLineItem{}, fmt.Errorf("scan pro forma line item: %w", err)
	}
	return item, nil
}

func scanProFormaCustomLineItem(row proFormaCustomLineItemRow) (ProFormaCustomLineItem, error) {
	var item ProFormaCustomLineItem
	if err := row.Scan(
		&item.ID,
		&item.BillingGroupID,
		&item.BillingGroupName,
		&item.PricingPlanID,
		&item.PricingPlanName,
		&item.BillingPeriodStart,
		&item.BillingPeriodEnd,
		&item.PayerAccountID,
		&item.LineItemType,
		&item.Name,
		&item.Description,
		&item.CurrencyCode,
		&item.AmountMicros,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return ProFormaCustomLineItem{}, fmt.Errorf("scan pro forma custom line item: %w", err)
	}
	return item, nil
}

const proFormaPricingPlanSelectSQL = `SELECT
	p.id,
	p.name,
	p.description,
	p.currency_code,
	p.status,
	COUNT(r.id) AS rule_count,
	p.created_at,
	p.updated_at
 FROM pro_forma_pricing_plans p
 LEFT JOIN pro_forma_pricing_rules r ON r.pricing_plan_id = p.id`

const proFormaPricingPlanGroupBySQL = ` GROUP BY p.id,
	p.name,
	p.description,
	p.currency_code,
	p.status,
	p.created_at,
	p.updated_at`

const proFormaPricingRuleSelectSQL = `SELECT
	r.id,
	r.pricing_plan_id,
	p.name,
	r.service_code,
	r.rate_multiplier_basis_points,
	r.description,
	r.status,
	r.created_at,
	r.updated_at
 FROM pro_forma_pricing_rules r
 JOIN pro_forma_pricing_plans p ON p.id = r.pricing_plan_id`

const proFormaBillingGroupSelectSQL = `SELECT
	g.id,
	g.name,
	g.description,
	g.payer_account_id,
	g.pricing_plan_id,
	p.name,
	g.status,
	COUNT(a.id) AS account_count,
	g.created_at,
	g.updated_at
 FROM pro_forma_billing_groups g
 JOIN pro_forma_pricing_plans p ON p.id = g.pricing_plan_id
 LEFT JOIN pro_forma_billing_group_accounts a ON a.billing_group_id = g.id`

const proFormaBillingGroupGroupBySQL = ` GROUP BY g.id,
	g.name,
	g.description,
	g.payer_account_id,
	g.pricing_plan_id,
	p.name,
	g.status,
	g.created_at,
	g.updated_at`

const proFormaLineItemSelectSQL = `SELECT
	li.id,
	li.source_bill_line_item_id,
	li.billing_group_id,
	g.name,
	li.pricing_plan_id,
	p.name,
	COALESCE(li.pricing_rule_id, ''),
	li.billing_period_start,
	li.billing_period_end,
	li.payer_account_id,
	li.usage_account_id,
	li.service_code,
	li.service_name,
	li.usage_type,
	li.line_item_status,
	li.currency_code,
	li.source_rate_micros,
	li.source_cost_micros,
	li.rate_multiplier_basis_points,
	li.pro_forma_rate_micros,
	li.pro_forma_cost_micros,
	li.adjustment_micros,
	li.created_at,
	li.updated_at
 FROM pro_forma_line_items li
 JOIN pro_forma_billing_groups g ON g.id = li.billing_group_id
 JOIN pro_forma_pricing_plans p ON p.id = li.pricing_plan_id`

const proFormaCustomLineItemSelectSQL = `SELECT
	ci.id,
	ci.billing_group_id,
	g.name,
	g.pricing_plan_id,
	p.name,
	ci.billing_period_start,
	ci.billing_period_end,
	g.payer_account_id,
	ci.line_item_type,
	ci.name,
	ci.description,
	ci.currency_code,
	ci.amount_micros,
	ci.created_at,
	ci.updated_at
 FROM pro_forma_custom_line_items ci
 JOIN pro_forma_billing_groups g ON g.id = ci.billing_group_id
 JOIN pro_forma_pricing_plans p ON p.id = g.pricing_plan_id`
