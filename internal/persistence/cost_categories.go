package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	defaultCostCategoryValue                = "Uncategorized"
	defaultCostCategoryStatus               = "active"
	defaultCostCategoryMatchType            = "all"
	defaultCostCategoryPreviewLineItemLimit = 100
	maxCostCategoryPreviewLineItemLimit     = 500
	defaultCostCategoryAssignmentLimit      = 100
	maxCostCategoryAssignmentLimit          = 500
	costCategoryAssignmentSourceRule        = "rule"
	costCategoryAssignmentSourceDefault     = "default"

	// CostCategoryRuleMatchAccount matches usage-account identifiers on bill line items.
	CostCategoryRuleMatchAccount = "account"
	// CostCategoryRuleMatchService matches billing service codes or names.
	CostCategoryRuleMatchService = "service"
	// CostCategoryRuleMatchRegion matches billing region codes.
	CostCategoryRuleMatchRegion = "region"
	// CostCategoryRuleMatchUsageType matches billing usage type values.
	CostCategoryRuleMatchUsageType = "usage_type"
	// CostCategoryRuleMatchLineItemType matches billing line item type values.
	CostCategoryRuleMatchLineItemType = "line_item_type"
	// CostCategoryRuleMatchTag matches one activated or discovered resource tag key.
	CostCategoryRuleMatchTag = "tag"
	// CostCategoryRuleMatchCostCategory matches values assigned by another cost category.
	CostCategoryRuleMatchCostCategory = "cost_category"

	// CostCategoryRuleOperatorIn includes line items whose dimension is one of the listed values.
	CostCategoryRuleOperatorIn = "in"
	// CostCategoryRuleOperatorNotIn excludes line items whose dimension is one of the listed values.
	CostCategoryRuleOperatorNotIn = "not_in"
)

// CostCategory stores a learner-defined business dimension such as Product or Environment.
type CostCategory struct {
	ID           string
	Name         string
	Description  string
	DefaultValue string
	Status       string
	CreatedAt    string
	UpdatedAt    string
}

// CostCategoryRule stores one ordered category assignment rule and its match conditions.
type CostCategoryRule struct {
	ID               string
	CostCategoryID   string
	CostCategoryName string
	RuleOrder        int
	Value            string
	Description      string
	MatchType        string
	Conditions       []CostCategoryRuleCondition
	CreatedAt        string
	UpdatedAt        string
}

// CostCategoryRuleCondition stores one dimension predicate inside an ordered rule.
type CostCategoryRuleCondition struct {
	ID               string
	RuleID           string
	ConditionOrder   int
	Dimension        string
	Operator         string
	TagKey           string
	CostCategoryID   string
	CostCategoryName string
	Values           []string
	CreatedAt        string
}

// CostCategoryCreateRequest describes a new business dimension for cost assignment.
type CostCategoryCreateRequest struct {
	ID           string
	Name         string
	Description  string
	DefaultValue string
	Status       string
}

// CostCategoryRuleCreateRequest describes a new ordered rule for one cost category.
type CostCategoryRuleCreateRequest struct {
	ID               string
	CostCategoryID   string
	CostCategoryName string
	RuleOrder        int
	Value            string
	Description      string
	MatchType        string
	Conditions       []CostCategoryRuleCondition
}

// CostCategoryPreviewRequest selects the category, period, and sample size used for previewing assignments.
type CostCategoryPreviewRequest struct {
	CostCategoryID     string
	CostCategoryName   string
	BillingPeriodStart string
	BillingPeriodEnd   string
	LineItemLimit      int
}

// CostCategoryPreview shows how ordered rules would assign one category without persisting assignments.
type CostCategoryPreview struct {
	Category               CostCategory
	BillingPeriodStart     string
	BillingPeriodEnd       string
	CurrencyCode           string
	TotalLineItemCount     int
	MatchedLineItemCount   int
	UnmatchedLineItemCount int
	TotalCostMicros        int64
	MatchedCostMicros      int64
	UnmatchedCostMicros    int64
	RuleSummaries          []CostCategoryPreviewRuleSummary
	LineItems              []CostCategoryPreviewLineItem
	HasMoreLineItems       bool
}

// CostCategoryPreviewRuleSummary reports first-match and shadowed match effects for one ordered rule.
type CostCategoryPreviewRuleSummary struct {
	RuleID                string
	RuleOrder             int
	Value                 string
	Description           string
	MatchedLineItemCount  int
	MatchedCostMicros     int64
	ShadowedLineItemCount int
	ShadowedCostMicros    int64
	ConditionDescriptions []string
}

// CostCategoryPreviewLineItem shows the before-and-after assignment for one billed line item.
type CostCategoryPreviewLineItem struct {
	ID               string
	ResourceID       string
	PayerAccountID   string
	UsageAccountID   string
	ServiceCode      string
	ServiceName      string
	UsageType        string
	LineItemType     string
	LineItemStatus   string
	RegionCode       string
	UsageStartTime   string
	UsageEndTime     string
	CurrencyCode     string
	CostMicros       int64
	BeforeValue      string
	PreviewValue     string
	MatchedRuleID    string
	MatchedRuleOrder int
	MatchedRuleValue string
	ShadowedRules    []CostCategoryPreviewShadowedRule
	TagSnapshot      map[string]string
}

// CostCategoryPreviewShadowedRule names a later matching rule hidden by first-match ordering.
type CostCategoryPreviewShadowedRule struct {
	RuleID    string
	RuleOrder int
	Value     string
}

// CostCategoryLineItemAssignment stores one applied category value for one bill line item.
type CostCategoryLineItemAssignment struct {
	LineItemID           string
	CostCategoryID       string
	BillingPeriodStart   string
	BillingPeriodEnd     string
	PayerAccountID       string
	UsageAccountID       string
	LineItemStatus       string
	CostCategoryName     string
	CategoryDefaultValue string
	AssignedValue        string
	AssignmentSource     string
	MatchedRuleID        string
	MatchedRuleOrder     int
	MatchedRuleValue     string
	CurrencyCode         string
	UnblendedCostMicros  int64
	CreatedAt            string
	UpdatedAt            string
}

// CostCategoryAssignmentRefreshResult reports how many open-period assignments were rebuilt.
type CostCategoryAssignmentRefreshResult struct {
	BillingPeriodsRefreshed int
	LineItemsEvaluated      int
	CategoriesEvaluated     int
	AssignmentsRefreshed    int
}

// CostCategoryAssignmentListRequest filters persisted line-item assignments.
type CostCategoryAssignmentListRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	CostCategoryID     string
	LineItemID         string
	Limit              int
}

// CostCategoryRepository manages cost category dimensions and ordered rules.
type CostCategoryRepository struct {
	db *sql.DB
}

// NewCostCategoryRepository creates a repository backed by a workspace database.
func NewCostCategoryRepository(db *sql.DB) CostCategoryRepository {
	return CostCategoryRepository{db: db}
}

// CreateCategory saves a new active cost category dimension.
func (r CostCategoryRepository) CreateCategory(ctx context.Context, request CostCategoryCreateRequest) (CostCategory, error) {
	if r.db == nil {
		return CostCategory{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostCategoryCreateRequest(request)
	if err := validateCostCategoryCreateRequest(request); err != nil {
		return CostCategory{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("cc")
		if err != nil {
			return CostCategory{}, err
		}
		request.ID = id
	}
	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO cost_categories (
				id,
				name,
				description,
				default_value,
				status
			) VALUES (?, ?, ?, ?, ?)`,
			request.ID,
			request.Name,
			request.Description,
			request.DefaultValue,
			request.Status,
		); err != nil {
			return fmt.Errorf("insert cost category %q: %w", request.Name, err)
		}
		_, err := refreshCostCategoryAssignmentsInTx(ctx, tx, "", "")
		return err
	}); err != nil {
		return CostCategory{}, err
	}
	return r.GetCategory(ctx, request.ID)
}

// GetCategory loads one cost category by ID.
func (r CostCategoryRepository) GetCategory(ctx context.Context, id string) (CostCategory, error) {
	if r.db == nil {
		return CostCategory{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return CostCategory{}, fmt.Errorf("cost category ID is required")
	}
	return scanCostCategory(r.db.QueryRowContext(
		ctx,
		`SELECT id, name, description, default_value, status, created_at, updated_at
		 FROM cost_categories
		 WHERE id = ?`,
		id,
	))
}

// GetCategoryByName loads one cost category by its learner-facing name.
func (r CostCategoryRepository) GetCategoryByName(ctx context.Context, name string) (CostCategory, error) {
	if r.db == nil {
		return CostCategory{}, fmt.Errorf("database handle is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return CostCategory{}, fmt.Errorf("cost category name is required")
	}
	return scanCostCategory(r.db.QueryRowContext(
		ctx,
		`SELECT id, name, description, default_value, status, created_at, updated_at
		 FROM cost_categories
		 WHERE lower(name) = lower(?)`,
		name,
	))
}

// ListCategories returns cost categories in learner-facing name order.
func (r CostCategoryRepository) ListCategories(ctx context.Context) ([]CostCategory, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	return listCostCategories(ctx, r.db)
}

func listCostCategories(ctx context.Context, q costCategoryQueryer) ([]CostCategory, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT id, name, description, default_value, status, created_at, updated_at
		 FROM cost_categories
		 ORDER BY lower(name), id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost categories: %w", err)
	}
	defer rows.Close()

	var categories []CostCategory
	for rows.Next() {
		category, err := scanCostCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost categories: %w", err)
	}
	return categories, nil
}

// CreateRule saves one ordered assignment rule and its conditions.
func (r CostCategoryRepository) CreateRule(ctx context.Context, request CostCategoryRuleCreateRequest) (CostCategoryRule, error) {
	if r.db == nil {
		return CostCategoryRule{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostCategoryRuleCreateRequest(request)
	if err := validateCostCategoryRuleCreateRequest(request); err != nil {
		return CostCategoryRule{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("ccr")
		if err != nil {
			return CostCategoryRule{}, err
		}
		request.ID = id
	}
	for i := range request.Conditions {
		if request.Conditions[i].ID == "" {
			id, err := newRepositoryID("ccrc")
			if err != nil {
				return CostCategoryRule{}, err
			}
			request.Conditions[i].ID = id
		}
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		categoryID, err := resolveCostCategoryID(ctx, tx, request.CostCategoryID, request.CostCategoryName)
		if err != nil {
			return err
		}
		request.CostCategoryID = categoryID
		for i := range request.Conditions {
			if request.Conditions[i].Dimension != CostCategoryRuleMatchCostCategory {
				continue
			}
			conditionCategoryID, err := resolveCostCategoryID(ctx, tx, request.Conditions[i].CostCategoryID, request.Conditions[i].CostCategoryName)
			if err != nil {
				return err
			}
			request.Conditions[i].CostCategoryID = conditionCategoryID
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO cost_category_rules (
				id,
				cost_category_id,
				rule_order,
				value,
				description,
				match_type
			) VALUES (?, ?, ?, ?, ?, ?)`,
			request.ID,
			request.CostCategoryID,
			request.RuleOrder,
			request.Value,
			request.Description,
			request.MatchType,
		); err != nil {
			return fmt.Errorf("insert cost category rule %q: %w", request.ID, err)
		}

		for _, condition := range request.Conditions {
			valuesJSON, err := json.Marshal(condition.Values)
			if err != nil {
				return fmt.Errorf("marshal cost category rule condition values: %w", err)
			}
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO cost_category_rule_conditions (
					id,
					rule_id,
					condition_order,
					dimension,
					operator,
					tag_key,
					cost_category_id,
					values_json
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				condition.ID,
				request.ID,
				condition.ConditionOrder,
				condition.Dimension,
				condition.Operator,
				nullStringArg(condition.TagKey),
				nullStringArg(condition.CostCategoryID),
				string(valuesJSON),
			); err != nil {
				return fmt.Errorf("insert cost category rule condition %q: %w", condition.ID, err)
			}
		}
		_, err = refreshCostCategoryAssignmentsInTx(ctx, tx, "", "")
		return nil
	}); err != nil {
		return CostCategoryRule{}, err
	}
	return r.GetRule(ctx, request.ID)
}

// GetRule loads one rule with ordered conditions.
func (r CostCategoryRepository) GetRule(ctx context.Context, id string) (CostCategoryRule, error) {
	if r.db == nil {
		return CostCategoryRule{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return CostCategoryRule{}, fmt.Errorf("cost category rule ID is required")
	}
	rule, err := scanCostCategoryRule(r.db.QueryRowContext(
		ctx,
		`SELECT r.id,
		        r.cost_category_id,
		        c.name,
		        r.rule_order,
		        r.value,
		        r.description,
		        r.match_type,
		        r.created_at,
		        r.updated_at
		 FROM cost_category_rules r
		 JOIN cost_categories c ON c.id = r.cost_category_id
		 WHERE r.id = ?`,
		id,
	))
	if err != nil {
		return CostCategoryRule{}, err
	}
	conditions, err := r.listRuleConditions(ctx, rule.ID)
	if err != nil {
		return CostCategoryRule{}, err
	}
	rule.Conditions = conditions
	return rule, nil
}

// ListRules returns ordered rules for one cost category.
func (r CostCategoryRepository) ListRules(ctx context.Context, costCategoryID string) ([]CostCategoryRule, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	costCategoryID = strings.TrimSpace(costCategoryID)
	if costCategoryID == "" {
		return nil, fmt.Errorf("cost category ID is required")
	}
	return listCostCategoryRules(ctx, r.db, costCategoryID)
}

func listCostCategoryRules(ctx context.Context, q costCategoryQueryer, costCategoryID string) ([]CostCategoryRule, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT r.id,
		        r.cost_category_id,
		        c.name,
		        r.rule_order,
		        r.value,
		        r.description,
		        r.match_type,
		        r.created_at,
		        r.updated_at
		 FROM cost_category_rules r
		 JOIN cost_categories c ON c.id = r.cost_category_id
		 WHERE r.cost_category_id = ?
		 ORDER BY r.rule_order, r.id`,
		costCategoryID,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category rules: %w", err)
	}
	defer rows.Close()

	var rules []CostCategoryRule
	for rows.Next() {
		rule, err := scanCostCategoryRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category rules: %w", err)
	}
	for i := range rules {
		conditions, err := listCostCategoryRuleConditions(ctx, q, rules[i].ID)
		if err != nil {
			return nil, err
		}
		rules[i].Conditions = conditions
	}
	return rules, nil
}

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

func (r CostCategoryRepository) newCostCategoryPreviewEvaluator(ctx context.Context) (costCategoryPreviewEvaluator, error) {
	return newCostCategoryEvaluator(ctx, r.db)
}

func newCostCategoryEvaluator(ctx context.Context, q costCategoryQueryer) (costCategoryPreviewEvaluator, error) {
	categories, err := listCostCategories(ctx, q)
	if err != nil {
		return costCategoryPreviewEvaluator{}, err
	}
	evaluator := costCategoryPreviewEvaluator{
		categories:      map[string]CostCategory{},
		rulesByCategory: map[string][]CostCategoryRule{},
	}
	for _, category := range categories {
		evaluator.categories[category.ID] = category
		rules, err := listCostCategoryRules(ctx, q, category.ID)
		if err != nil {
			return costCategoryPreviewEvaluator{}, err
		}
		evaluator.rulesByCategory[category.ID] = rules
	}
	return evaluator, nil
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
	if len(items) == 0 {
		return result, nil
	}
	result.LineItemsEvaluated = len(items)

	periods := map[string]bool{}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `DELETE FROM cost_category_line_item_assignments WHERE line_item_id = ?`, item.ID); err != nil {
			return CostCategoryAssignmentRefreshResult{}, fmt.Errorf("clear cost category assignments for line item %q: %w", item.ID, err)
		}
		periods[item.BillingPeriodStart+"|"+item.BillingPeriodEnd] = true
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

func (r CostCategoryRepository) listRuleConditions(ctx context.Context, ruleID string) ([]CostCategoryRuleCondition, error) {
	return listCostCategoryRuleConditions(ctx, r.db, ruleID)
}

func listCostCategoryRuleConditions(ctx context.Context, q costCategoryQueryer, ruleID string) ([]CostCategoryRuleCondition, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT rc.id,
		        rc.rule_id,
		        rc.condition_order,
		        rc.dimension,
		        rc.operator,
		        COALESCE(rc.tag_key, ''),
		        COALESCE(rc.cost_category_id, ''),
		        COALESCE(c.name, ''),
		        rc.values_json,
		        rc.created_at
		 FROM cost_category_rule_conditions rc
		 LEFT JOIN cost_categories c ON c.id = rc.cost_category_id
		 WHERE rc.rule_id = ?
		 ORDER BY rc.condition_order, rc.id`,
		ruleID,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category rule conditions: %w", err)
	}
	defer rows.Close()

	var conditions []CostCategoryRuleCondition
	for rows.Next() {
		condition, err := scanCostCategoryRuleCondition(rows)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, condition)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category rule conditions: %w", err)
	}
	return conditions, nil
}

type costCategoryRow interface {
	Scan(dest ...any) error
}

type costCategoryQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type costCategoryAssignmentStore interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type costCategoryQueryable interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanCostCategory(row costCategoryRow) (CostCategory, error) {
	var category CostCategory
	if err := row.Scan(
		&category.ID,
		&category.Name,
		&category.Description,
		&category.DefaultValue,
		&category.Status,
		&category.CreatedAt,
		&category.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return CostCategory{}, fmt.Errorf("cost category not found")
		}
		return CostCategory{}, fmt.Errorf("scan cost category: %w", err)
	}
	return category, nil
}

func scanCostCategoryRule(row costCategoryRow) (CostCategoryRule, error) {
	var rule CostCategoryRule
	if err := row.Scan(
		&rule.ID,
		&rule.CostCategoryID,
		&rule.CostCategoryName,
		&rule.RuleOrder,
		&rule.Value,
		&rule.Description,
		&rule.MatchType,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return CostCategoryRule{}, fmt.Errorf("cost category rule not found")
		}
		return CostCategoryRule{}, fmt.Errorf("scan cost category rule: %w", err)
	}
	return rule, nil
}

func scanCostCategoryRuleCondition(row costCategoryRow) (CostCategoryRuleCondition, error) {
	var condition CostCategoryRuleCondition
	var valuesJSON string
	if err := row.Scan(
		&condition.ID,
		&condition.RuleID,
		&condition.ConditionOrder,
		&condition.Dimension,
		&condition.Operator,
		&condition.TagKey,
		&condition.CostCategoryID,
		&condition.CostCategoryName,
		&valuesJSON,
		&condition.CreatedAt,
	); err != nil {
		return CostCategoryRuleCondition{}, fmt.Errorf("scan cost category rule condition: %w", err)
	}
	if err := json.Unmarshal([]byte(valuesJSON), &condition.Values); err != nil {
		return CostCategoryRuleCondition{}, fmt.Errorf("decode cost category rule condition values: %w", err)
	}
	return condition, nil
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

func resolveCostCategoryID(ctx context.Context, q costCategoryQueryable, id, name string) (string, error) {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" && name == "" {
		return "", fmt.Errorf("cost category ID or name is required")
	}
	var resolvedID string
	var resolvedName string
	switch {
	case id != "":
		if err := q.QueryRowContext(ctx, `SELECT id, name FROM cost_categories WHERE id = ?`, id).Scan(&resolvedID, &resolvedName); err != nil {
			if err == sql.ErrNoRows {
				return "", fmt.Errorf("cost category %q does not exist", id)
			}
			return "", fmt.Errorf("resolve cost category %q: %w", id, err)
		}
		if name != "" && !strings.EqualFold(name, resolvedName) {
			return "", fmt.Errorf("cost category %q does not match name %q", id, name)
		}
	default:
		if err := q.QueryRowContext(ctx, `SELECT id, name FROM cost_categories WHERE lower(name) = lower(?)`, name).Scan(&resolvedID, &resolvedName); err != nil {
			if err == sql.ErrNoRows {
				return "", fmt.Errorf("cost category %q does not exist", name)
			}
			return "", fmt.Errorf("resolve cost category %q: %w", name, err)
		}
	}
	return resolvedID, nil
}

func normalizeCostCategoryCreateRequest(request CostCategoryCreateRequest) CostCategoryCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.DefaultValue = strings.TrimSpace(request.DefaultValue)
	request.Status = strings.TrimSpace(request.Status)
	if request.DefaultValue == "" {
		request.DefaultValue = defaultCostCategoryValue
	}
	if request.Status == "" {
		request.Status = defaultCostCategoryStatus
	}
	return request
}

func validateCostCategoryCreateRequest(request CostCategoryCreateRequest) error {
	if request.Name == "" {
		return fmt.Errorf("cost category name is required")
	}
	if request.DefaultValue == "" {
		return fmt.Errorf("cost category default value is required")
	}
	switch request.Status {
	case "active", "archived":
		return nil
	default:
		return fmt.Errorf("cost category status %q is not supported", request.Status)
	}
}

func normalizeCostCategoryRuleCreateRequest(request CostCategoryRuleCreateRequest) CostCategoryRuleCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.CostCategoryID = strings.TrimSpace(request.CostCategoryID)
	request.CostCategoryName = strings.TrimSpace(request.CostCategoryName)
	request.Value = strings.TrimSpace(request.Value)
	request.Description = strings.TrimSpace(request.Description)
	request.MatchType = strings.TrimSpace(request.MatchType)
	if request.MatchType == "" {
		request.MatchType = defaultCostCategoryMatchType
	}
	for i := range request.Conditions {
		condition := request.Conditions[i]
		condition.ID = strings.TrimSpace(condition.ID)
		condition.RuleID = strings.TrimSpace(condition.RuleID)
		condition.Dimension = strings.TrimSpace(condition.Dimension)
		condition.Operator = strings.TrimSpace(condition.Operator)
		condition.TagKey = strings.TrimSpace(condition.TagKey)
		condition.CostCategoryID = strings.TrimSpace(condition.CostCategoryID)
		condition.CostCategoryName = strings.TrimSpace(condition.CostCategoryName)
		if condition.ConditionOrder == 0 {
			condition.ConditionOrder = i + 1
		}
		if condition.Operator == "" {
			condition.Operator = CostCategoryRuleOperatorIn
		}
		for valueIndex := range condition.Values {
			condition.Values[valueIndex] = strings.TrimSpace(condition.Values[valueIndex])
		}
		request.Conditions[i] = condition
	}
	return request
}

func validateCostCategoryRuleCreateRequest(request CostCategoryRuleCreateRequest) error {
	if request.CostCategoryID == "" && request.CostCategoryName == "" {
		return fmt.Errorf("cost category ID or name is required")
	}
	if request.RuleOrder <= 0 {
		return fmt.Errorf("cost category rule order must be positive")
	}
	if request.Value == "" {
		return fmt.Errorf("cost category rule value is required")
	}
	if request.MatchType != defaultCostCategoryMatchType {
		return fmt.Errorf("cost category rule match type %q is not supported", request.MatchType)
	}
	if len(request.Conditions) == 0 {
		return fmt.Errorf("cost category rule needs at least one condition")
	}

	orders := map[int]bool{}
	for i, condition := range request.Conditions {
		if condition.ConditionOrder <= 0 {
			return fmt.Errorf("cost category rule condition %d order must be positive", i)
		}
		if orders[condition.ConditionOrder] {
			return fmt.Errorf("cost category rule condition order %d is duplicated", condition.ConditionOrder)
		}
		orders[condition.ConditionOrder] = true
		if err := validateCostCategoryRuleCondition(i, condition); err != nil {
			return err
		}
	}
	return nil
}

func validateCostCategoryRuleCondition(index int, condition CostCategoryRuleCondition) error {
	switch condition.Dimension {
	case CostCategoryRuleMatchAccount,
		CostCategoryRuleMatchService,
		CostCategoryRuleMatchRegion,
		CostCategoryRuleMatchUsageType,
		CostCategoryRuleMatchLineItemType,
		CostCategoryRuleMatchTag,
		CostCategoryRuleMatchCostCategory:
	default:
		return fmt.Errorf("cost category rule condition %d dimension %q is not supported", index, condition.Dimension)
	}
	switch condition.Operator {
	case CostCategoryRuleOperatorIn, CostCategoryRuleOperatorNotIn:
	default:
		return fmt.Errorf("cost category rule condition %d operator %q is not supported", index, condition.Operator)
	}
	if condition.Dimension == CostCategoryRuleMatchTag && condition.TagKey == "" {
		return fmt.Errorf("cost category rule condition %d tag key is required", index)
	}
	if condition.Dimension != CostCategoryRuleMatchTag && condition.TagKey != "" {
		return fmt.Errorf("cost category rule condition %d tag key is only valid for tag conditions", index)
	}
	if condition.Dimension == CostCategoryRuleMatchCostCategory && condition.CostCategoryID == "" && condition.CostCategoryName == "" {
		return fmt.Errorf("cost category rule condition %d referenced cost category is required", index)
	}
	if condition.Dimension != CostCategoryRuleMatchCostCategory && (condition.CostCategoryID != "" || condition.CostCategoryName != "") {
		return fmt.Errorf("cost category rule condition %d referenced cost category is only valid for cost category conditions", index)
	}
	if len(condition.Values) == 0 {
		return fmt.Errorf("cost category rule condition %d needs at least one value", index)
	}
	seen := map[string]bool{}
	for _, value := range condition.Values {
		if value == "" {
			return fmt.Errorf("cost category rule condition %d value is required", index)
		}
		if seen[value] {
			return fmt.Errorf("cost category rule condition %d has duplicate value %q", index, value)
		}
		seen[value] = true
	}
	return nil
}

type costCategoryPreviewEvaluator struct {
	categories      map[string]CostCategory
	rulesByCategory map[string][]CostCategoryRule
}

type costCategoryPreviewAssignment struct {
	Value     string
	RuleID    string
	RuleOrder int
	Matched   bool
}

func (a costCategoryPreviewAssignment) ValueForMatchedRule() string {
	if !a.Matched {
		return ""
	}
	return a.Value
}

func (e costCategoryPreviewEvaluator) orderedCategories() []CostCategory {
	categories := make([]CostCategory, 0, len(e.categories))
	for _, category := range e.categories {
		categories = append(categories, category)
	}
	sort.Slice(categories, func(i, j int) bool {
		left := strings.ToLower(categories[i].Name)
		right := strings.ToLower(categories[j].Name)
		if left == right {
			return categories[i].ID < categories[j].ID
		}
		return left < right
	})
	return categories
}

func (e costCategoryPreviewEvaluator) evaluateCategory(item BillLineItem, categoryID string, stack map[string]bool) (costCategoryPreviewAssignment, error) {
	category, ok := e.categories[categoryID]
	if !ok {
		return costCategoryPreviewAssignment{}, fmt.Errorf("cost category %q is not loaded for preview", categoryID)
	}
	matches, err := e.matchingRules(item, categoryID, stack)
	if err != nil {
		return costCategoryPreviewAssignment{}, err
	}
	if len(matches) == 0 {
		return costCategoryPreviewAssignment{Value: category.DefaultValue}, nil
	}
	return costCategoryPreviewAssignment{
		Value:     matches[0].Value,
		RuleID:    matches[0].ID,
		RuleOrder: matches[0].RuleOrder,
		Matched:   true,
	}, nil
}

func (e costCategoryPreviewEvaluator) matchingRules(item BillLineItem, categoryID string, stack map[string]bool) ([]CostCategoryRule, error) {
	if stack[categoryID] {
		categoryName := categoryID
		if category, ok := e.categories[categoryID]; ok {
			categoryName = category.Name
		}
		return nil, fmt.Errorf("cost category rule reference cycle includes %q", categoryName)
	}
	if _, ok := e.categories[categoryID]; !ok {
		return nil, fmt.Errorf("cost category %q is not loaded for preview", categoryID)
	}
	stack[categoryID] = true
	defer delete(stack, categoryID)

	var matches []CostCategoryRule
	for _, rule := range e.rulesByCategory[categoryID] {
		matched, err := e.ruleMatches(item, rule, stack)
		if err != nil {
			return nil, err
		}
		if matched {
			matches = append(matches, rule)
		}
	}
	return matches, nil
}

func (e costCategoryPreviewEvaluator) ruleMatches(item BillLineItem, rule CostCategoryRule, stack map[string]bool) (bool, error) {
	for _, condition := range rule.Conditions {
		matched, err := e.conditionMatches(item, condition, stack)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func (e costCategoryPreviewEvaluator) conditionMatches(item BillLineItem, condition CostCategoryRuleCondition, stack map[string]bool) (bool, error) {
	actualValues, err := e.conditionActualValues(item, condition, stack)
	if err != nil {
		return false, err
	}
	matched := costCategoryPreviewAnyValueMatches(actualValues, condition.Values)
	switch condition.Operator {
	case CostCategoryRuleOperatorNotIn:
		return !matched, nil
	default:
		return matched, nil
	}
}

func (e costCategoryPreviewEvaluator) conditionActualValues(item BillLineItem, condition CostCategoryRuleCondition, stack map[string]bool) ([]string, error) {
	switch condition.Dimension {
	case CostCategoryRuleMatchAccount:
		return []string{item.UsageAccountID}, nil
	case CostCategoryRuleMatchService:
		return uniqueNonEmptyStrings(item.ServiceCode, item.ServiceName), nil
	case CostCategoryRuleMatchRegion:
		return []string{item.RegionCode}, nil
	case CostCategoryRuleMatchUsageType:
		return []string{item.UsageType}, nil
	case CostCategoryRuleMatchLineItemType:
		return []string{item.LineItemType}, nil
	case CostCategoryRuleMatchTag:
		value, ok := item.TagSnapshot[condition.TagKey]
		if !ok {
			return nil, nil
		}
		return []string{value}, nil
	case CostCategoryRuleMatchCostCategory:
		assignment, err := e.evaluateCategory(item, condition.CostCategoryID, stack)
		if err != nil {
			return nil, err
		}
		return []string{assignment.Value}, nil
	default:
		return nil, fmt.Errorf("cost category rule condition dimension %q is not supported", condition.Dimension)
	}
}

func costCategoryPreviewAnyValueMatches(actualValues, expectedValues []string) bool {
	for _, actual := range actualValues {
		actual = strings.TrimSpace(actual)
		if actual == "" {
			continue
		}
		for _, expected := range expectedValues {
			if actual == strings.TrimSpace(expected) {
				return true
			}
		}
	}
	return false
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

func uniqueNonEmptyStrings(values ...string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}
