package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
		return err
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
			return CostCategory{}, domainErrorf(ErrCostCategoryNotFound, "cost category not found")
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
			return CostCategoryRule{}, domainErrorf(ErrCostCategoryRuleNotFound, "cost category rule not found")
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
				return "", domainErrorf(ErrCostCategoryNotFound, "cost category %q does not exist", id)
			}
			return "", fmt.Errorf("resolve cost category %q: %w", id, err)
		}
		if name != "" && !strings.EqualFold(name, resolvedName) {
			return "", fmt.Errorf("cost category %q does not match name %q", id, name)
		}
	default:
		if err := q.QueryRowContext(ctx, `SELECT id, name FROM cost_categories WHERE lower(name) = lower(?)`, name).Scan(&resolvedID, &resolvedName); err != nil {
			if err == sql.ErrNoRows {
				return "", domainErrorf(ErrCostCategoryNotFound, "cost category %q does not exist", name)
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
