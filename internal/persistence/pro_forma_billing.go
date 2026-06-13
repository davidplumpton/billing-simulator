package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	proFormaStatusActive             = "active"
	proFormaDefaultCurrency          = "USD"
	proFormaDefaultMultiplierBPS int = 10_000
	proFormaMaxMultiplierBPS     int = 1_000_000
	defaultProFormaLineItemLimit     = 50
	maxProFormaLineItemLimit         = 200
)

// ProFormaPricingPlan groups custom internal rates used for showback views.
type ProFormaPricingPlan struct {
	ID           string
	Name         string
	Description  string
	CurrencyCode string
	Status       string
	RuleCount    int
	CreatedAt    string
	UpdatedAt    string
}

// ProFormaPricingRule applies one service-level internal rate multiplier.
type ProFormaPricingRule struct {
	ID                        string
	PricingPlanID             string
	PricingPlanName           string
	ServiceCode               string
	RateMultiplierBasisPoints int
	Description               string
	Status                    string
	CreatedAt                 string
	UpdatedAt                 string
}

// ProFormaBillingGroup assigns usage accounts to one pro forma pricing plan.
type ProFormaBillingGroup struct {
	ID              string
	Name            string
	Description     string
	PayerAccountID  string
	PricingPlanID   string
	PricingPlanName string
	Status          string
	AccountCount    int
	CreatedAt       string
	UpdatedAt       string
}

// ProFormaBillingGroupAccount stores one usage-account membership.
type ProFormaBillingGroupAccount struct {
	ID             string
	BillingGroupID string
	AccountID      string
	CreatedAt      string
}

// ProFormaLineItem stores one internal showback row derived from a bill line item.
type ProFormaLineItem struct {
	ID                        string
	SourceBillLineItemID      string
	BillingGroupID            string
	BillingGroupName          string
	PricingPlanID             string
	PricingPlanName           string
	PricingRuleID             string
	BillingPeriodStart        string
	BillingPeriodEnd          string
	PayerAccountID            string
	UsageAccountID            string
	ServiceCode               string
	ServiceName               string
	UsageType                 string
	LineItemStatus            string
	CurrencyCode              string
	SourceRateMicros          int64
	SourceCostMicros          int64
	RateMultiplierBasisPoints int
	ProFormaRateMicros        int64
	ProFormaCostMicros        int64
	AdjustmentMicros          int64
	CreatedAt                 string
	UpdatedAt                 string
}

// ProFormaRefreshRequest selects source bill line items for pro forma regeneration.
type ProFormaRefreshRequest struct {
	BillingGroupID     string
	PayerAccountID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
}

// ProFormaRefreshResult reports the rows rebuilt by a pro forma refresh.
type ProFormaRefreshResult struct {
	BillingGroupsRefreshed int
	SourceLineItems        int
	ProFormaLineItems      int
	SourceCostMicros       int64
	ProFormaCostMicros     int64
	AdjustmentMicros       int64
}

// ProFormaSummaryRequest filters comparison summaries by period and group.
type ProFormaSummaryRequest struct {
	BillingGroupID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
}

// ProFormaBillingGroupSummary compares payable source cost to internal pro forma cost.
type ProFormaBillingGroupSummary struct {
	BillingGroupID      string
	BillingGroupName    string
	PricingPlanID       string
	PricingPlanName     string
	BillingPeriodStart  string
	BillingPeriodEnd    string
	PayerAccountID      string
	CurrencyCode        string
	SourceLineItemCount int
	SourceCostMicros    int64
	ProFormaCostMicros  int64
	AdjustmentMicros    int64
}

// ProFormaPricingPlanCreateRequest describes a new internal pricing plan.
type ProFormaPricingPlanCreateRequest struct {
	ID           string
	Name         string
	Description  string
	CurrencyCode string
	Status       string
}

// ProFormaPricingRuleCreateRequest describes one service-level custom rate.
type ProFormaPricingRuleCreateRequest struct {
	ID                        string
	PricingPlanID             string
	ServiceCode               string
	RateMultiplierBasisPoints int
	Description               string
	Status                    string
}

// ProFormaBillingGroupCreateRequest describes a new billing group.
type ProFormaBillingGroupCreateRequest struct {
	ID             string
	Name           string
	Description    string
	PayerAccountID string
	PricingPlanID  string
	Status         string
}

// ProFormaBillingGroupAccountCreateRequest describes one account assignment.
type ProFormaBillingGroupAccountCreateRequest struct {
	ID             string
	BillingGroupID string
	AccountID      string
}

// ProFormaLineItemListRequest filters persisted pro forma rows for display.
type ProFormaLineItemListRequest struct {
	BillingGroupID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
	Limit              int
}

// ProFormaBillingRepository manages pro forma billing groups, pricing plans, and generated rows.
type ProFormaBillingRepository struct {
	db *sql.DB
}

// NewProFormaBillingRepository creates a repository backed by a workspace database.
func NewProFormaBillingRepository(db *sql.DB) ProFormaBillingRepository {
	return ProFormaBillingRepository{db: db}
}

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

// RefreshLineItems rebuilds internal showback rows from current bill line items.
func (r ProFormaBillingRepository) RefreshLineItems(ctx context.Context, request ProFormaRefreshRequest) (ProFormaRefreshResult, error) {
	if r.db == nil {
		return ProFormaRefreshResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaRefreshRequest(request)
	if err := validateProFormaRefreshRequest(request); err != nil {
		return ProFormaRefreshResult{}, err
	}
	var result ProFormaRefreshResult
	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		sources, err := listProFormaSourceLineItems(ctx, tx, request)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM pro_forma_line_items
			 WHERE billing_period_start = ?
			   AND billing_period_end = ?
			   AND (? = '' OR billing_group_id = ?)
			   AND (? = '' OR payer_account_id = ?)`,
			request.BillingPeriodStart,
			request.BillingPeriodEnd,
			request.BillingGroupID,
			request.BillingGroupID,
			request.PayerAccountID,
			request.PayerAccountID,
		); err != nil {
			return fmt.Errorf("clear pro forma line items: %w", err)
		}
		seenGroups := map[string]bool{}
		for _, source := range sources {
			proFormaRate, err := multiplyMicrosByBasisPoints(source.SourceRateMicros, source.RateMultiplierBasisPoints)
			if err != nil {
				return fmt.Errorf("calculate pro forma rate for %s: %w", source.SourceBillLineItemID, err)
			}
			proFormaCost, err := multiplyMicrosByBasisPoints(source.SourceCostMicros, source.RateMultiplierBasisPoints)
			if err != nil {
				return fmt.Errorf("calculate pro forma cost for %s: %w", source.SourceBillLineItemID, err)
			}
			id, err := newRepositoryID("pfli")
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO pro_forma_line_items (
					id,
					source_bill_line_item_id,
					billing_group_id,
					pricing_plan_id,
					pricing_rule_id,
					billing_period_start,
					billing_period_end,
					payer_account_id,
					usage_account_id,
					service_code,
					service_name,
					usage_type,
					line_item_status,
					currency_code,
					source_rate_micros,
					source_cost_micros,
					rate_multiplier_basis_points,
					pro_forma_rate_micros,
					pro_forma_cost_micros,
					adjustment_micros
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id,
				source.SourceBillLineItemID,
				source.BillingGroupID,
				source.PricingPlanID,
				nullStringArg(source.PricingRuleID),
				source.BillingPeriodStart,
				source.BillingPeriodEnd,
				source.PayerAccountID,
				source.UsageAccountID,
				source.ServiceCode,
				source.ServiceName,
				source.UsageType,
				source.LineItemStatus,
				source.CurrencyCode,
				source.SourceRateMicros,
				source.SourceCostMicros,
				source.RateMultiplierBasisPoints,
				proFormaRate,
				proFormaCost,
				proFormaCost-source.SourceCostMicros,
			); err != nil {
				return fmt.Errorf("insert pro forma line item for %s: %w", source.SourceBillLineItemID, err)
			}
			seenGroups[source.BillingGroupID] = true
			result.SourceLineItems++
			result.ProFormaLineItems++
			result.SourceCostMicros += source.SourceCostMicros
			result.ProFormaCostMicros += proFormaCost
			result.AdjustmentMicros += proFormaCost - source.SourceCostMicros
		}
		result.BillingGroupsRefreshed = len(seenGroups)
		return nil
	}); err != nil {
		return ProFormaRefreshResult{}, err
	}
	return result, nil
}

// ListBillingGroupSummaries aggregates generated pro forma rows by group.
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
		`SELECT
			li.billing_group_id,
			g.name,
			li.pricing_plan_id,
			p.name,
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.currency_code,
			COUNT(*),
			COALESCE(SUM(li.source_cost_micros), 0),
			COALESCE(SUM(li.pro_forma_cost_micros), 0),
			COALESCE(SUM(li.adjustment_micros), 0)
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
		 ORDER BY g.name, li.currency_code`,
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
			&summary.SourceCostMicros,
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

type proFormaSourceLineItem struct {
	SourceBillLineItemID      string
	BillingGroupID            string
	PricingPlanID             string
	PricingRuleID             string
	BillingPeriodStart        string
	BillingPeriodEnd          string
	PayerAccountID            string
	UsageAccountID            string
	ServiceCode               string
	ServiceName               string
	UsageType                 string
	LineItemStatus            string
	CurrencyCode              string
	SourceRateMicros          int64
	SourceCostMicros          int64
	RateMultiplierBasisPoints int
}

func listProFormaSourceLineItems(ctx context.Context, tx *sql.Tx, request ProFormaRefreshRequest) ([]proFormaSourceLineItem, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT
			li.id,
			g.id,
			p.id,
			COALESCE(r.id, ''),
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.usage_type,
			li.line_item_status,
			li.currency_code,
			li.unblended_rate_micros,
			li.unblended_cost_micros,
			COALESCE(r.rate_multiplier_basis_points, ?)
		 FROM bill_line_items li
		 JOIN pro_forma_billing_group_accounts a ON a.account_id = li.usage_account_id
		 JOIN pro_forma_billing_groups g ON g.id = a.billing_group_id
		 JOIN pro_forma_pricing_plans p ON p.id = g.pricing_plan_id
		 LEFT JOIN pro_forma_pricing_rules r ON r.pricing_plan_id = p.id
		   AND r.service_code = li.service_code
		   AND r.status = ?
		 WHERE li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.payer_account_id = g.payer_account_id
		   AND g.status = ?
		   AND p.status = ?
		   AND (? = '' OR g.id = ?)
		   AND (? = '' OR li.payer_account_id = ?)
		 ORDER BY g.name, li.id`,
		proFormaDefaultMultiplierBPS,
		proFormaStatusActive,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		proFormaStatusActive,
		proFormaStatusActive,
		request.BillingGroupID,
		request.BillingGroupID,
		request.PayerAccountID,
		request.PayerAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pro forma source line items: %w", err)
	}
	defer rows.Close()
	var sources []proFormaSourceLineItem
	for rows.Next() {
		var source proFormaSourceLineItem
		if err := rows.Scan(
			&source.SourceBillLineItemID,
			&source.BillingGroupID,
			&source.PricingPlanID,
			&source.PricingRuleID,
			&source.BillingPeriodStart,
			&source.BillingPeriodEnd,
			&source.PayerAccountID,
			&source.UsageAccountID,
			&source.ServiceCode,
			&source.ServiceName,
			&source.UsageType,
			&source.LineItemStatus,
			&source.CurrencyCode,
			&source.SourceRateMicros,
			&source.SourceCostMicros,
			&source.RateMultiplierBasisPoints,
		); err != nil {
			return nil, fmt.Errorf("scan pro forma source line item: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma source line items: %w", err)
	}
	return sources, nil
}

func multiplyMicrosByBasisPoints(value int64, basisPoints int) (int64, error) {
	if value < 0 {
		return 0, fmt.Errorf("micros value cannot be negative")
	}
	if basisPoints <= 0 {
		return 0, fmt.Errorf("basis points must be positive")
	}
	numerator := big.NewInt(value)
	numerator.Mul(numerator, big.NewInt(int64(basisPoints)))
	numerator.Add(numerator, big.NewInt(5_000))
	numerator.Div(numerator, big.NewInt(10_000))
	if !numerator.IsInt64() {
		return 0, fmt.Errorf("scaled micros value is too large")
	}
	return numerator.Int64(), nil
}

func validateProFormaAccountExists(ctx context.Context, db *sql.DB, accountID string) error {
	var found string
	err := db.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id = ?`, accountID).Scan(&found)
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("account %q does not exist", accountID)
	}
	return fmt.Errorf("validate pro forma account %q: %w", accountID, err)
}

func normalizeProFormaPricingPlanCreateRequest(request ProFormaPricingPlanCreateRequest) ProFormaPricingPlanCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.CurrencyCode = strings.ToUpper(strings.TrimSpace(request.CurrencyCode))
	if request.CurrencyCode == "" {
		request.CurrencyCode = proFormaDefaultCurrency
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = proFormaStatusActive
	}
	return request
}

func validateProFormaPricingPlanCreateRequest(request ProFormaPricingPlanCreateRequest) error {
	if request.Name == "" {
		return fmt.Errorf("pro forma pricing plan name is required")
	}
	if len(request.CurrencyCode) != 3 {
		return fmt.Errorf("pro forma pricing plan currency code must be three characters")
	}
	if !isProFormaStatus(request.Status) {
		return fmt.Errorf("unsupported pro forma pricing plan status %q", request.Status)
	}
	return nil
}

func normalizeProFormaPricingRuleCreateRequest(request ProFormaPricingRuleCreateRequest) ProFormaPricingRuleCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.PricingPlanID = strings.TrimSpace(request.PricingPlanID)
	request.ServiceCode = strings.TrimSpace(request.ServiceCode)
	request.Description = strings.TrimSpace(request.Description)
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = proFormaStatusActive
	}
	return request
}

func validateProFormaPricingRuleCreateRequest(request ProFormaPricingRuleCreateRequest) error {
	if request.PricingPlanID == "" {
		return fmt.Errorf("pro forma pricing plan ID is required")
	}
	if request.ServiceCode == "" {
		return fmt.Errorf("service code is required")
	}
	if request.RateMultiplierBasisPoints <= 0 || request.RateMultiplierBasisPoints > proFormaMaxMultiplierBPS {
		return fmt.Errorf("rate multiplier basis points must be greater than zero and at most %d", proFormaMaxMultiplierBPS)
	}
	if !isProFormaStatus(request.Status) {
		return fmt.Errorf("unsupported pro forma pricing rule status %q", request.Status)
	}
	return nil
}

func normalizeProFormaBillingGroupCreateRequest(request ProFormaBillingGroupCreateRequest) ProFormaBillingGroupCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	if request.PayerAccountID == "" {
		request.PayerAccountID = AnyCompanyRetailManagementAccountID
	}
	request.PricingPlanID = strings.TrimSpace(request.PricingPlanID)
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = proFormaStatusActive
	}
	return request
}

func validateProFormaBillingGroupCreateRequest(request ProFormaBillingGroupCreateRequest) error {
	if request.Name == "" {
		return fmt.Errorf("pro forma billing group name is required")
	}
	if err := validateOrganizationAccountID("payer account ID", request.PayerAccountID); err != nil {
		return err
	}
	if request.PricingPlanID == "" {
		return fmt.Errorf("pro forma pricing plan ID is required")
	}
	if !isProFormaStatus(request.Status) {
		return fmt.Errorf("unsupported pro forma billing group status %q", request.Status)
	}
	return nil
}

func normalizeProFormaBillingGroupAccountCreateRequest(request ProFormaBillingGroupAccountCreateRequest) ProFormaBillingGroupAccountCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.AccountID = strings.TrimSpace(request.AccountID)
	return request
}

func validateProFormaBillingGroupAccountCreateRequest(request ProFormaBillingGroupAccountCreateRequest) error {
	if request.BillingGroupID == "" {
		return fmt.Errorf("pro forma billing group ID is required")
	}
	return validateOrganizationAccountID("account ID", request.AccountID)
}

func normalizeProFormaRefreshRequest(request ProFormaRefreshRequest) ProFormaRefreshRequest {
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	return request
}

func validateProFormaRefreshRequest(request ProFormaRefreshRequest) error {
	if err := validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return err
	}
	if request.PayerAccountID != "" {
		return validateOrganizationAccountID("payer account ID", request.PayerAccountID)
	}
	return nil
}

func normalizeProFormaSummaryRequest(request ProFormaSummaryRequest) ProFormaSummaryRequest {
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	return request
}

func validateProFormaSummaryRequest(request ProFormaSummaryRequest) error {
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func normalizeProFormaLineItemListRequest(request ProFormaLineItemListRequest) ProFormaLineItemListRequest {
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	if request.Limit <= 0 {
		request.Limit = defaultProFormaLineItemLimit
	}
	if request.Limit > maxProFormaLineItemLimit {
		request.Limit = maxProFormaLineItemLimit
	}
	return request
}

func validateProFormaLineItemListRequest(request ProFormaLineItemListRequest) error {
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func isProFormaStatus(status string) bool {
	switch status {
	case "active", "archived":
		return true
	default:
		return false
	}
}

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
