package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// BudgetScopeAccount limits spend checks to one linked usage account.
	BudgetScopeAccount = "account"
	// BudgetScopeService limits spend checks to one billing service code or name.
	BudgetScopeService = "service"
	// BudgetScopeTag limits spend checks to one resource tag key/value pair.
	BudgetScopeTag = "tag"
	// BudgetScopeCostCategory limits spend checks to one Cost Category value.
	BudgetScopeCostCategory = "cost_category"

	// BudgetThresholdTypeActual compares current billed spend to the threshold amount.
	BudgetThresholdTypeActual = "actual"
	// BudgetThresholdTypeForecast compares forecasted spend to the threshold amount.
	BudgetThresholdTypeForecast = "forecast"
)

const (
	defaultBudgetStatus             = "active"
	defaultBudgetCurrencyCode       = "USD"
	maxBudgetThresholdBasisPoints   = 100000
	budgetThresholdBasisPointDenom  = 10000
	defaultBudgetEvaluationListSize = 100
)

// Budget stores one monthly cost guardrail and the scope it applies to.
type Budget struct {
	ID                 string
	Name               string
	Description        string
	BillingPeriodStart string
	BillingPeriodEnd   string
	BudgetAmountMicros int64
	CurrencyCode       string
	ScopeType          string
	ScopeKey           string
	ScopeValue         string
	Status             string
	CreatedAt          string
	UpdatedAt          string
	Thresholds         []BudgetThreshold
}

// BudgetThreshold stores one actual or forecast threshold for a budget.
type BudgetThreshold struct {
	ID                   string
	BudgetID             string
	ThresholdType        string
	ThresholdBasisPoints int
	CreatedAt            string
	UpdatedAt            string
}

// BudgetThresholdCreateRequest describes one threshold to save with a budget.
type BudgetThresholdCreateRequest struct {
	ID                   string
	ThresholdType        string
	ThresholdBasisPoints int
}

// BudgetCreateRequest describes a learner-created monthly budget.
type BudgetCreateRequest struct {
	ID                 string
	Name               string
	Description        string
	BillingPeriodStart string
	BillingPeriodEnd   string
	BudgetAmountMicros int64
	CurrencyCode       string
	ScopeType          string
	ScopeKey           string
	ScopeValue         string
	Status             string
	Thresholds         []BudgetThresholdCreateRequest
}

// BudgetListRequest filters saved budgets for list and evaluation pages.
type BudgetListRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	Status             string
	Limit              int
}

// BudgetEvaluationRequest selects the period and optional forecast amounts for threshold checks.
type BudgetEvaluationRequest struct {
	BillingPeriodStart           string
	BillingPeriodEnd             string
	ForecastCostMicrosByBudgetID map[string]int64
}

// BudgetEvaluation reports actual/forecast spend and threshold status for one budget.
type BudgetEvaluation struct {
	Budget             Budget
	BillingPeriodStart string
	BillingPeriodEnd   string
	CurrencyCode       string
	LineItemCount      int
	ActualCostMicros   int64
	ForecastCostMicros int64
	ThresholdChecks    []BudgetThresholdCheck
}

// BudgetThresholdCheck reports whether one threshold is currently crossed.
type BudgetThresholdCheck struct {
	ThresholdID            string
	ThresholdType          string
	ThresholdBasisPoints   int
	ThresholdAmountMicros  int64
	SpendMicros            int64
	RemainingCostMicros    int64
	PercentUsedBasisPoints int64
	Breached               bool
}

// BudgetRepository manages monthly budget definitions and threshold checks.
type BudgetRepository struct {
	db *sql.DB
}

// NewBudgetRepository creates a repository backed by a workspace database.
func NewBudgetRepository(db *sql.DB) BudgetRepository {
	return BudgetRepository{db: db}
}

// CreateBudget saves a monthly budget and its actual/forecast thresholds.
func (r BudgetRepository) CreateBudget(ctx context.Context, request BudgetCreateRequest) (Budget, error) {
	if r.db == nil {
		return Budget{}, fmt.Errorf("database handle is required")
	}
	request = normalizeBudgetCreateRequest(request)
	if err := validateBudgetCreateRequest(request); err != nil {
		return Budget{}, err
	}
	if err := r.resolveBudgetScope(ctx, &request); err != nil {
		return Budget{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("bud")
		if err != nil {
			return Budget{}, err
		}
		request.ID = id
	}
	for i := range request.Thresholds {
		if request.Thresholds[i].ID == "" {
			id, err := newRepositoryID("budt")
			if err != nil {
				return Budget{}, err
			}
			request.Thresholds[i].ID = id
		}
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO budgets (
				id,
				name,
				description,
				billing_period_start,
				billing_period_end,
				budget_amount_micros,
				currency_code,
				scope_type,
				scope_key,
				scope_value,
				status
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.ID,
			request.Name,
			request.Description,
			request.BillingPeriodStart,
			request.BillingPeriodEnd,
			request.BudgetAmountMicros,
			request.CurrencyCode,
			request.ScopeType,
			nullStringArg(request.ScopeKey),
			request.ScopeValue,
			request.Status,
		); err != nil {
			return fmt.Errorf("insert budget %q: %w", request.Name, err)
		}
		for _, threshold := range request.Thresholds {
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO budget_thresholds (
					id,
					budget_id,
					threshold_type,
					threshold_basis_points
				) VALUES (?, ?, ?, ?)`,
				threshold.ID,
				request.ID,
				threshold.ThresholdType,
				threshold.ThresholdBasisPoints,
			); err != nil {
				return fmt.Errorf("insert budget threshold %q %d: %w", threshold.ThresholdType, threshold.ThresholdBasisPoints, err)
			}
		}
		return nil
	}); err != nil {
		return Budget{}, err
	}
	return r.GetBudget(ctx, request.ID)
}

// GetBudget loads one budget by ID with its thresholds.
func (r BudgetRepository) GetBudget(ctx context.Context, id string) (Budget, error) {
	if r.db == nil {
		return Budget{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Budget{}, fmt.Errorf("budget ID is required")
	}
	budget, err := scanBudget(r.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			name,
			description,
			billing_period_start,
			billing_period_end,
			budget_amount_micros,
			currency_code,
			scope_type,
			scope_key,
			scope_value,
			status,
			created_at,
			updated_at
		 FROM budgets
		 WHERE id = ?`,
		id,
	))
	if err != nil {
		return Budget{}, err
	}
	budget.Thresholds, err = r.listBudgetThresholds(ctx, budget.ID)
	if err != nil {
		return Budget{}, err
	}
	return budget, nil
}

// ListBudgets returns budgets in period/name order with thresholds attached.
func (r BudgetRepository) ListBudgets(ctx context.Context, request BudgetListRequest) ([]Budget, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeBudgetListRequest(request)
	if request.BillingPeriodStart != "" || request.BillingPeriodEnd != "" {
		if err := validateMonthlyBudgetPeriod(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
			return nil, err
		}
	}
	if request.Status != "" && !isBudgetStatus(request.Status) {
		return nil, fmt.Errorf("unsupported budget status %q", request.Status)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = defaultBudgetEvaluationListSize
	}

	where := []string{"1 = 1"}
	args := []any{}
	if request.BillingPeriodStart != "" {
		where = append(where, "billing_period_start = ?")
		args = append(args, request.BillingPeriodStart)
	}
	if request.BillingPeriodEnd != "" {
		where = append(where, "billing_period_end = ?")
		args = append(args, request.BillingPeriodEnd)
	}
	if request.Status != "" {
		where = append(where, "status = ?")
		args = append(args, request.Status)
	}
	args = append(args, limit)

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			name,
			description,
			billing_period_start,
			billing_period_end,
			budget_amount_micros,
			currency_code,
			scope_type,
			scope_key,
			scope_value,
			status,
			created_at,
			updated_at
		 FROM budgets
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY billing_period_start DESC, lower(name), id
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list budgets: %w", err)
	}
	defer rows.Close()

	var budgets []Budget
	for rows.Next() {
		budget, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		budget.Thresholds, err = r.listBudgetThresholds(ctx, budget.ID)
		if err != nil {
			return nil, err
		}
		budgets = append(budgets, budget)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate budgets: %w", err)
	}
	return budgets, nil
}

// EvaluateBudgets calculates actual and forecast threshold status for active budgets in a period.
func (r BudgetRepository) EvaluateBudgets(ctx context.Context, request BudgetEvaluationRequest) ([]BudgetEvaluation, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeBudgetEvaluationRequest(request)
	if err := validateBudgetEvaluationRequest(request); err != nil {
		return nil, err
	}

	budgets, err := r.ListBudgets(ctx, BudgetListRequest{
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
		Status:             defaultBudgetStatus,
		Limit:              defaultBudgetEvaluationListSize,
	})
	if err != nil {
		return nil, err
	}

	forecastSummaries, err := r.budgetForecastSummaryMap(ctx, request.BillingPeriodStart, request.BillingPeriodEnd)
	if err != nil {
		return nil, err
	}

	evaluations := make([]BudgetEvaluation, 0, len(budgets))
	for _, budget := range budgets {
		actualCostMicros, lineItemCount, err := r.actualCostForBudget(ctx, budget)
		if err != nil {
			return nil, err
		}
		forecastCostMicros := actualCostMicros
		if summary, ok := forecastSummaries[budget.ID]; ok {
			forecastCostMicros = summary.ForecastCostMicros
		}
		if forecast, ok := request.ForecastCostMicrosByBudgetID[budget.ID]; ok {
			forecastCostMicros = forecast
		}
		evaluation := BudgetEvaluation{
			Budget:             budget,
			BillingPeriodStart: request.BillingPeriodStart,
			BillingPeriodEnd:   request.BillingPeriodEnd,
			CurrencyCode:       budget.CurrencyCode,
			LineItemCount:      lineItemCount,
			ActualCostMicros:   actualCostMicros,
			ForecastCostMicros: forecastCostMicros,
		}
		for _, threshold := range budget.Thresholds {
			evaluation.ThresholdChecks = append(evaluation.ThresholdChecks, budgetThresholdCheck(budget, threshold, actualCostMicros, forecastCostMicros))
		}
		evaluations = append(evaluations, evaluation)
	}
	return evaluations, nil
}

func (r BudgetRepository) resolveBudgetScope(ctx context.Context, request *BudgetCreateRequest) error {
	switch request.ScopeType {
	case BudgetScopeAccount, BudgetScopeService:
		request.ScopeKey = ""
	case BudgetScopeTag:
	case BudgetScopeCostCategory:
		category, err := resolveCostExplorerCostCategory(ctx, r.db, request.ScopeKey)
		if err != nil {
			return err
		}
		request.ScopeKey = category.ID
	default:
		return fmt.Errorf("unsupported budget scope %q", request.ScopeType)
	}
	return nil
}

func (r BudgetRepository) listBudgetThresholds(ctx context.Context, budgetID string) ([]BudgetThreshold, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			budget_id,
			threshold_type,
			threshold_basis_points,
			created_at,
			updated_at
		 FROM budget_thresholds
		 WHERE budget_id = ?
		 ORDER BY
			CASE threshold_type WHEN 'actual' THEN 1 WHEN 'forecast' THEN 2 ELSE 3 END,
			threshold_basis_points,
			id`,
		budgetID,
	)
	if err != nil {
		return nil, fmt.Errorf("list budget thresholds for %q: %w", budgetID, err)
	}
	defer rows.Close()

	var thresholds []BudgetThreshold
	for rows.Next() {
		var threshold BudgetThreshold
		if err := rows.Scan(
			&threshold.ID,
			&threshold.BudgetID,
			&threshold.ThresholdType,
			&threshold.ThresholdBasisPoints,
			&threshold.CreatedAt,
			&threshold.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan budget threshold: %w", err)
		}
		thresholds = append(thresholds, threshold)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate budget thresholds: %w", err)
	}
	return thresholds, nil
}

func (r BudgetRepository) actualCostForBudget(ctx context.Context, budget Budget) (int64, int, error) {
	condition, args, err := budgetScopeCondition(budget)
	if err != nil {
		return 0, 0, err
	}
	queryArgs := []any{
		budget.BillingPeriodStart,
		budget.BillingPeriodEnd,
		budget.CurrencyCode,
	}
	queryArgs = append(queryArgs, args...)

	var lineItemCount int
	var costMicros int64
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT
			COUNT(*),
			COALESCE(SUM(bli.unblended_cost_micros), 0)
		 FROM bill_line_items bli
		 WHERE bli.billing_period_start = ?
		   AND bli.billing_period_end = ?
		   AND bli.currency_code = ?
		   AND `+condition,
		queryArgs...,
	).Scan(&lineItemCount, &costMicros); err != nil {
		return 0, 0, fmt.Errorf("evaluate budget %q spend: %w", budget.ID, err)
	}
	return costMicros, lineItemCount, nil
}

func budgetScopeCondition(budget Budget) (string, []any, error) {
	switch budget.ScopeType {
	case BudgetScopeAccount:
		return "bli.usage_account_id = ?", []any{budget.ScopeValue}, nil
	case BudgetScopeService:
		return "(bli.service_code = ? OR bli.service_name = ?)", []any{budget.ScopeValue, budget.ScopeValue}, nil
	case BudgetScopeTag:
		return `EXISTS (
			SELECT 1
			FROM json_each(bli.tag_snapshot_json) j
			WHERE j.key = ?
			  AND CAST(j.value AS TEXT) = ?
		)`, []any{budget.ScopeKey, budget.ScopeValue}, nil
	case BudgetScopeCostCategory:
		return `EXISTS (
			SELECT 1
			FROM cost_category_line_item_assignments a
			WHERE a.line_item_id = bli.id
			  AND a.cost_category_id = ?
			  AND a.assigned_value = ?
		)`, []any{budget.ScopeKey, budget.ScopeValue}, nil
	default:
		return "", nil, fmt.Errorf("unsupported budget scope %q", budget.ScopeType)
	}
}

func budgetThresholdCheck(budget Budget, threshold BudgetThreshold, actualCostMicros, forecastCostMicros int64) BudgetThresholdCheck {
	thresholdAmountMicros := budgetThresholdAmountMicros(budget.BudgetAmountMicros, threshold.ThresholdBasisPoints)
	spendMicros := actualCostMicros
	if threshold.ThresholdType == BudgetThresholdTypeForecast {
		spendMicros = forecastCostMicros
	}
	remainingCostMicros := thresholdAmountMicros - spendMicros
	if remainingCostMicros < 0 {
		remainingCostMicros = 0
	}
	return BudgetThresholdCheck{
		ThresholdID:            threshold.ID,
		ThresholdType:          threshold.ThresholdType,
		ThresholdBasisPoints:   threshold.ThresholdBasisPoints,
		ThresholdAmountMicros:  thresholdAmountMicros,
		SpendMicros:            spendMicros,
		RemainingCostMicros:    remainingCostMicros,
		PercentUsedBasisPoints: budgetPercentUsedBasisPoints(spendMicros, budget.BudgetAmountMicros),
		Breached:               spendMicros >= thresholdAmountMicros,
	}
}

func budgetThresholdAmountMicros(budgetAmountMicros int64, thresholdBasisPoints int) int64 {
	if budgetAmountMicros <= 0 || thresholdBasisPoints <= 0 {
		return 0
	}
	product := budgetAmountMicros * int64(thresholdBasisPoints)
	return (product + budgetThresholdBasisPointDenom - 1) / budgetThresholdBasisPointDenom
}

func budgetPercentUsedBasisPoints(spendMicros, budgetAmountMicros int64) int64 {
	if spendMicros <= 0 || budgetAmountMicros <= 0 {
		return 0
	}
	return (spendMicros*budgetThresholdBasisPointDenom + budgetAmountMicros/2) / budgetAmountMicros
}

func normalizeBudgetCreateRequest(request BudgetCreateRequest) BudgetCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.CurrencyCode = strings.ToUpper(strings.TrimSpace(request.CurrencyCode))
	if request.CurrencyCode == "" {
		request.CurrencyCode = defaultBudgetCurrencyCode
	}
	request.ScopeType = strings.TrimSpace(request.ScopeType)
	request.ScopeKey = strings.TrimSpace(request.ScopeKey)
	request.ScopeValue = strings.TrimSpace(request.ScopeValue)
	if request.ScopeType == BudgetScopeAccount || request.ScopeType == BudgetScopeService {
		request.ScopeKey = ""
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = defaultBudgetStatus
	}
	for i := range request.Thresholds {
		request.Thresholds[i].ID = strings.TrimSpace(request.Thresholds[i].ID)
		request.Thresholds[i].ThresholdType = strings.TrimSpace(request.Thresholds[i].ThresholdType)
	}
	sort.SliceStable(request.Thresholds, func(i, j int) bool {
		if request.Thresholds[i].ThresholdType == request.Thresholds[j].ThresholdType {
			return request.Thresholds[i].ThresholdBasisPoints < request.Thresholds[j].ThresholdBasisPoints
		}
		return request.Thresholds[i].ThresholdType < request.Thresholds[j].ThresholdType
	})
	return request
}

func normalizeBudgetListRequest(request BudgetListRequest) BudgetListRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.Status = strings.TrimSpace(request.Status)
	return request
}

func normalizeBudgetEvaluationRequest(request BudgetEvaluationRequest) BudgetEvaluationRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	if request.ForecastCostMicrosByBudgetID == nil {
		request.ForecastCostMicrosByBudgetID = map[string]int64{}
	}
	return request
}

func validateBudgetCreateRequest(request BudgetCreateRequest) error {
	if request.Name == "" {
		return fmt.Errorf("budget name is required")
	}
	if err := validateMonthlyBudgetPeriod(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return err
	}
	if request.BudgetAmountMicros <= 0 {
		return fmt.Errorf("budget amount must be greater than zero")
	}
	if len(request.CurrencyCode) != 3 {
		return fmt.Errorf("budget currency code must be three characters")
	}
	if !isBudgetStatus(request.Status) {
		return fmt.Errorf("unsupported budget status %q", request.Status)
	}
	if err := validateBudgetScope(request.ScopeType, request.ScopeKey, request.ScopeValue); err != nil {
		return err
	}
	if len(request.Thresholds) == 0 {
		return fmt.Errorf("budget needs at least one threshold")
	}
	seenThresholds := map[string]bool{}
	for _, threshold := range request.Thresholds {
		if err := validateBudgetThresholdCreateRequest(threshold); err != nil {
			return err
		}
		key := threshold.ThresholdType + ":" + fmt.Sprintf("%d", threshold.ThresholdBasisPoints)
		if seenThresholds[key] {
			return fmt.Errorf("budget threshold %s %d is duplicated", threshold.ThresholdType, threshold.ThresholdBasisPoints)
		}
		seenThresholds[key] = true
	}
	return nil
}

func validateBudgetThresholdCreateRequest(request BudgetThresholdCreateRequest) error {
	if request.ThresholdType != BudgetThresholdTypeActual && request.ThresholdType != BudgetThresholdTypeForecast {
		return fmt.Errorf("unsupported budget threshold type %q", request.ThresholdType)
	}
	if request.ThresholdBasisPoints <= 0 {
		return fmt.Errorf("budget threshold basis points must be greater than zero")
	}
	if request.ThresholdBasisPoints > maxBudgetThresholdBasisPoints {
		return fmt.Errorf("budget threshold basis points must be %d or fewer", maxBudgetThresholdBasisPoints)
	}
	return nil
}

func validateBudgetEvaluationRequest(request BudgetEvaluationRequest) error {
	if err := validateMonthlyBudgetPeriod(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return err
	}
	for id, forecastCostMicros := range request.ForecastCostMicrosByBudgetID {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("budget forecast ID is required")
		}
		if forecastCostMicros < 0 {
			return fmt.Errorf("budget forecast cost for %q must be non-negative", id)
		}
	}
	return nil
}

func validateMonthlyBudgetPeriod(periodStart, periodEnd string) error {
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return err
	}
	start, err := time.Parse(time.DateOnly, periodStart)
	if err != nil {
		return fmt.Errorf("budget period start must use YYYY-MM-DD: %w", err)
	}
	end, err := time.Parse(time.DateOnly, periodEnd)
	if err != nil {
		return fmt.Errorf("budget period end must use YYYY-MM-DD: %w", err)
	}
	if start.Day() != 1 || !start.AddDate(0, 1, 0).Equal(end) {
		return fmt.Errorf("budget period must cover exactly one UTC calendar month")
	}
	return nil
}

func validateBudgetScope(scopeType, scopeKey, scopeValue string) error {
	if scopeValue == "" {
		return fmt.Errorf("budget scope value is required")
	}
	switch scopeType {
	case BudgetScopeAccount, BudgetScopeService:
		if scopeKey != "" {
			return fmt.Errorf("budget scope key is only supported for tag and Cost Category scopes")
		}
	case BudgetScopeTag, BudgetScopeCostCategory:
		if scopeKey == "" {
			return fmt.Errorf("budget scope key is required for %s scope", scopeType)
		}
	default:
		return fmt.Errorf("unsupported budget scope %q", scopeType)
	}
	return nil
}

func isBudgetStatus(status string) bool {
	return status == defaultBudgetStatus || status == "archived"
}

type budgetRowScanner interface {
	Scan(dest ...any) error
}

func scanBudget(row budgetRowScanner) (Budget, error) {
	var budget Budget
	var scopeKey sql.NullString
	if err := row.Scan(
		&budget.ID,
		&budget.Name,
		&budget.Description,
		&budget.BillingPeriodStart,
		&budget.BillingPeriodEnd,
		&budget.BudgetAmountMicros,
		&budget.CurrencyCode,
		&budget.ScopeType,
		&scopeKey,
		&budget.ScopeValue,
		&budget.Status,
		&budget.CreatedAt,
		&budget.UpdatedAt,
	); err != nil {
		return Budget{}, err
	}
	budget.ScopeKey = nullStringValue(scopeKey)
	return budget, nil
}
