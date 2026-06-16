package persistence

import (
	"context"
	"fmt"
	"strings"
)

// CreatePricingPlan stores a new internal pricing plan.
func (r ProFormaBillingRepository) CreatePricingPlan(ctx context.Context, request ProFormaPricingPlanCreateRequest) (ProFormaPricingPlan, error) {
	if r.db == nil {
		return ProFormaPricingPlan{}, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaPricingPlanCreateRequest(request)
	if err := validateProFormaPricingPlanCreateRequest(request); err != nil {
		return ProFormaPricingPlan{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("pfplan")
		if err != nil {
			return ProFormaPricingPlan{}, err
		}
		request.ID = id
	}
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO pro_forma_pricing_plans (
			id,
			name,
			description,
			currency_code,
			status
		) VALUES (?, ?, ?, ?, ?)`,
		request.ID,
		request.Name,
		request.Description,
		request.CurrencyCode,
		request.Status,
	); err != nil {
		return ProFormaPricingPlan{}, fmt.Errorf("insert pro forma pricing plan %q: %w", request.Name, err)
	}
	return r.GetPricingPlan(ctx, request.ID)
}

// CreatePricingRule stores or replaces one service-level multiplier for a pricing plan.
func (r ProFormaBillingRepository) CreatePricingRule(ctx context.Context, request ProFormaPricingRuleCreateRequest) (ProFormaPricingRule, error) {
	if r.db == nil {
		return ProFormaPricingRule{}, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaPricingRuleCreateRequest(request)
	if err := validateProFormaPricingRuleCreateRequest(request); err != nil {
		return ProFormaPricingRule{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("pfrule")
		if err != nil {
			return ProFormaPricingRule{}, err
		}
		request.ID = id
	}
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO pro_forma_pricing_rules (
			id,
			pricing_plan_id,
			service_code,
			rate_multiplier_basis_points,
			description,
			status
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (pricing_plan_id, service_code) DO UPDATE SET
			rate_multiplier_basis_points = excluded.rate_multiplier_basis_points,
			description = excluded.description,
			status = excluded.status,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		request.ID,
		request.PricingPlanID,
		request.ServiceCode,
		request.RateMultiplierBasisPoints,
		request.Description,
		request.Status,
	); err != nil {
		return ProFormaPricingRule{}, fmt.Errorf("insert pro forma pricing rule for %q: %w", request.ServiceCode, err)
	}
	return r.GetPricingRuleForService(ctx, request.PricingPlanID, request.ServiceCode)
}

// CreateBillingGroup stores a pro forma billing group attached to a pricing plan.
func (r ProFormaBillingRepository) CreateBillingGroup(ctx context.Context, request ProFormaBillingGroupCreateRequest) (ProFormaBillingGroup, error) {
	if r.db == nil {
		return ProFormaBillingGroup{}, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaBillingGroupCreateRequest(request)
	if err := validateProFormaBillingGroupCreateRequest(request); err != nil {
		return ProFormaBillingGroup{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("pfgroup")
		if err != nil {
			return ProFormaBillingGroup{}, err
		}
		request.ID = id
	}
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO pro_forma_billing_groups (
			id,
			name,
			description,
			payer_account_id,
			pricing_plan_id,
			status
		) VALUES (?, ?, ?, ?, ?, ?)`,
		request.ID,
		request.Name,
		request.Description,
		request.PayerAccountID,
		request.PricingPlanID,
		request.Status,
	); err != nil {
		return ProFormaBillingGroup{}, fmt.Errorf("insert pro forma billing group %q: %w", request.Name, err)
	}
	return r.GetBillingGroup(ctx, request.ID)
}

// AssignAccountToGroup attaches one usage account to one pro forma billing group.
func (r ProFormaBillingRepository) AssignAccountToGroup(ctx context.Context, request ProFormaBillingGroupAccountCreateRequest) (ProFormaBillingGroupAccount, error) {
	if r.db == nil {
		return ProFormaBillingGroupAccount{}, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaBillingGroupAccountCreateRequest(request)
	if err := validateProFormaBillingGroupAccountCreateRequest(request); err != nil {
		return ProFormaBillingGroupAccount{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("pfacct")
		if err != nil {
			return ProFormaBillingGroupAccount{}, err
		}
		request.ID = id
	}
	if err := validateProFormaAccountExists(ctx, r.db, request.AccountID); err != nil {
		return ProFormaBillingGroupAccount{}, err
	}
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO pro_forma_billing_group_accounts (
			id,
			billing_group_id,
			account_id
		) VALUES (?, ?, ?)`,
		request.ID,
		request.BillingGroupID,
		request.AccountID,
	); err != nil {
		return ProFormaBillingGroupAccount{}, fmt.Errorf("assign account %q to pro forma billing group: %w", request.AccountID, err)
	}
	return r.getBillingGroupAccount(ctx, request.ID)
}

// CreateCustomLineItem stores one manual fee, credit, markup, or annotation.
func (r ProFormaBillingRepository) CreateCustomLineItem(ctx context.Context, request ProFormaCustomLineItemCreateRequest) (ProFormaCustomLineItem, error) {
	if r.db == nil {
		return ProFormaCustomLineItem{}, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaCustomLineItemCreateRequest(request)
	if err := validateProFormaCustomLineItemCreateRequest(request); err != nil {
		return ProFormaCustomLineItem{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("pfcustom")
		if err != nil {
			return ProFormaCustomLineItem{}, err
		}
		request.ID = id
	}
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO pro_forma_custom_line_items (
			id,
			billing_group_id,
			billing_period_start,
			billing_period_end,
			line_item_type,
			name,
			description,
			currency_code,
			amount_micros
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ID,
		request.BillingGroupID,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.LineItemType,
		request.Name,
		request.Description,
		request.CurrencyCode,
		request.AmountMicros,
	); err != nil {
		return ProFormaCustomLineItem{}, fmt.Errorf("insert pro forma custom line item %q: %w", request.Name, err)
	}
	return r.GetCustomLineItem(ctx, request.ID)
}

// GetPricingPlan reads one pricing plan.
func (r ProFormaBillingRepository) GetPricingPlan(ctx context.Context, id string) (ProFormaPricingPlan, error) {
	if r.db == nil {
		return ProFormaPricingPlan{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ProFormaPricingPlan{}, fmt.Errorf("pro forma pricing plan ID is required")
	}
	plan, err := scanProFormaPricingPlan(r.db.QueryRowContext(ctx, proFormaPricingPlanSelectSQL+` WHERE p.id = ?`+proFormaPricingPlanGroupBySQL, id))
	if err != nil {
		return ProFormaPricingPlan{}, err
	}
	return plan, nil
}

// GetPricingRuleForService reads one service rule from a pricing plan.
func (r ProFormaBillingRepository) GetPricingRuleForService(ctx context.Context, pricingPlanID, serviceCode string) (ProFormaPricingRule, error) {
	if r.db == nil {
		return ProFormaPricingRule{}, fmt.Errorf("database handle is required")
	}
	pricingPlanID = strings.TrimSpace(pricingPlanID)
	serviceCode = strings.TrimSpace(serviceCode)
	if pricingPlanID == "" {
		return ProFormaPricingRule{}, fmt.Errorf("pro forma pricing plan ID is required")
	}
	if serviceCode == "" {
		return ProFormaPricingRule{}, fmt.Errorf("service code is required")
	}
	rule, err := scanProFormaPricingRule(r.db.QueryRowContext(
		ctx,
		proFormaPricingRuleSelectSQL+` WHERE r.pricing_plan_id = ? AND r.service_code = ?`,
		pricingPlanID,
		serviceCode,
	))
	if err != nil {
		return ProFormaPricingRule{}, err
	}
	return rule, nil
}

// GetBillingGroup reads one billing group.
func (r ProFormaBillingRepository) GetBillingGroup(ctx context.Context, id string) (ProFormaBillingGroup, error) {
	if r.db == nil {
		return ProFormaBillingGroup{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ProFormaBillingGroup{}, fmt.Errorf("pro forma billing group ID is required")
	}
	group, err := scanProFormaBillingGroup(r.db.QueryRowContext(ctx, proFormaBillingGroupSelectSQL+` WHERE g.id = ?`+proFormaBillingGroupGroupBySQL, id))
	if err != nil {
		return ProFormaBillingGroup{}, err
	}
	return group, nil
}

// GetCustomLineItem reads one manual pro forma adjustment row.
func (r ProFormaBillingRepository) GetCustomLineItem(ctx context.Context, id string) (ProFormaCustomLineItem, error) {
	if r.db == nil {
		return ProFormaCustomLineItem{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ProFormaCustomLineItem{}, fmt.Errorf("pro forma custom line item ID is required")
	}
	item, err := scanProFormaCustomLineItem(r.db.QueryRowContext(ctx, proFormaCustomLineItemSelectSQL+` WHERE ci.id = ?`, id))
	if err != nil {
		return ProFormaCustomLineItem{}, err
	}
	return item, nil
}

// ListPricingPlans returns pricing plans with rule counts.
func (r ProFormaBillingRepository) ListPricingPlans(ctx context.Context) ([]ProFormaPricingPlan, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	rows, err := r.db.QueryContext(ctx, proFormaPricingPlanSelectSQL+proFormaPricingPlanGroupBySQL+` ORDER BY p.name`)
	if err != nil {
		return nil, fmt.Errorf("list pro forma pricing plans: %w", err)
	}
	defer rows.Close()
	var plans []ProFormaPricingPlan
	for rows.Next() {
		plan, err := scanProFormaPricingPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma pricing plans: %w", err)
	}
	return plans, nil
}

// ListPricingRules returns rules for one pricing plan or all plans.
func (r ProFormaBillingRepository) ListPricingRules(ctx context.Context, pricingPlanID string) ([]ProFormaPricingRule, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	pricingPlanID = strings.TrimSpace(pricingPlanID)
	rows, err := r.db.QueryContext(
		ctx,
		proFormaPricingRuleSelectSQL+`
		 WHERE (? = '' OR r.pricing_plan_id = ?)
		 ORDER BY p.name, r.service_code`,
		pricingPlanID,
		pricingPlanID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pro forma pricing rules: %w", err)
	}
	defer rows.Close()
	var rules []ProFormaPricingRule
	for rows.Next() {
		rule, err := scanProFormaPricingRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma pricing rules: %w", err)
	}
	return rules, nil
}

// ListBillingGroups returns billing groups with assigned account counts.
func (r ProFormaBillingRepository) ListBillingGroups(ctx context.Context) ([]ProFormaBillingGroup, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	rows, err := r.db.QueryContext(ctx, proFormaBillingGroupSelectSQL+proFormaBillingGroupGroupBySQL+` ORDER BY g.name`)
	if err != nil {
		return nil, fmt.Errorf("list pro forma billing groups: %w", err)
	}
	defer rows.Close()
	var groups []ProFormaBillingGroup
	for rows.Next() {
		group, err := scanProFormaBillingGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma billing groups: %w", err)
	}
	return groups, nil
}

// ListBillingGroupAccounts returns account assignments for one group or all groups.
func (r ProFormaBillingRepository) ListBillingGroupAccounts(ctx context.Context, billingGroupID string) ([]ProFormaBillingGroupAccount, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	billingGroupID = strings.TrimSpace(billingGroupID)
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id,
		        billing_group_id,
		        account_id,
		        created_at
		 FROM pro_forma_billing_group_accounts
		 WHERE (? = '' OR billing_group_id = ?)
		 ORDER BY billing_group_id, account_id`,
		billingGroupID,
		billingGroupID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pro forma billing group accounts: %w", err)
	}
	defer rows.Close()
	var assignments []ProFormaBillingGroupAccount
	for rows.Next() {
		assignment, err := scanProFormaBillingGroupAccount(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma billing group accounts: %w", err)
	}
	return assignments, nil
}

func (r ProFormaBillingRepository) getBillingGroupAccount(ctx context.Context, id string) (ProFormaBillingGroupAccount, error) {
	assignment, err := scanProFormaBillingGroupAccount(r.db.QueryRowContext(
		ctx,
		`SELECT id,
		        billing_group_id,
		        account_id,
		        created_at
		 FROM pro_forma_billing_group_accounts
		 WHERE id = ?`,
		id,
	))
	if err != nil {
		return ProFormaBillingGroupAccount{}, err
	}
	return assignment, nil
}
