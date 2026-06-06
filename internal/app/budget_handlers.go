package app

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type budgetHandler struct {
	db         *sql.DB
	budgets    persistence.BudgetRepository
	clock      persistence.SimulatorClockRepository
	categories persistence.CostCategoryRepository
}

type budgetsPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Form                budgetFormView
	ScopeTypeOptions    []uiSelectOptionView
	CategoryOptions     []string
	StateCards          []budgetStateCardView
	ThresholdRows       []budgetThresholdRowView
	Tables              budgetsTablesView
}

type budgetFormView struct {
	Name               string
	Description        string
	BillingPeriodStart string
	BillingPeriodEnd   string
	Amount             string
	ScopeType          string
	ScopeKey           string
	ScopeValue         string
	ActualThreshold    string
	ForecastThreshold  string
}

type budgetStateCardView struct {
	Label string
	Value string
}

type budgetThresholdRowView struct {
	BudgetName       string
	Scope            string
	Period           string
	BudgetAmount     string
	Metric           string
	ThresholdPercent string
	ThresholdAmount  string
	Spend            string
	PercentUsed      string
	Remaining        string
	LineItems        int
	Status           string
	StatusClass      string
}

type budgetsTablesView struct {
	Thresholds uiTableView
}

// newBudgetHandler builds the server-rendered budgets workflow.
func newBudgetHandler(db *sql.DB) budgetHandler {
	return budgetHandler{
		db:         db,
		budgets:    persistence.NewBudgetRepository(db),
		clock:      persistence.NewSimulatorClockRepository(db),
		categories: persistence.NewCostCategoryRepository(db),
	}
}

// handleBudgets renders current monthly budget threshold checks.
func (h budgetHandler) handleBudgets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderBudgets(w, r, http.StatusOK, budgetFormView{}, "", flashFromQuery(r))
}

// handleCreateBudget persists one monthly budget definition from the browser form.
func (h budgetHandler) handleCreateBudget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderBudgets(w, r, http.StatusServiceUnavailable, budgetFormView{}, "Open a workspace before creating budgets.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderBudgets(w, r, http.StatusBadRequest, budgetFormView{}, "parse budget form: "+err.Error(), "")
		return
	}
	form := budgetFormFromValues(r.PostForm)
	request, err := budgetCreateRequestFromForm(form)
	if err != nil {
		h.renderBudgets(w, r, http.StatusBadRequest, form, err.Error(), "")
		return
	}
	budget, err := h.budgets.CreateBudget(r.Context(), request)
	if err != nil {
		h.renderBudgets(w, r, http.StatusBadRequest, form, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/budgets?flash="+urlQueryEscape("Created budget "+budget.Name), http.StatusSeeOther)
}

func (h budgetHandler) renderBudgets(w http.ResponseWriter, r *http.Request, status int, form budgetFormView, errorMessage, flashMessage string) {
	data := budgetsPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		Notices:             uiNotices(flashMessage, errorMessage),
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Form:                form,
		Tables:              budgetsTables(),
	}
	if data.Form.Name == "" && data.Form.Amount == "" {
		data.Form = h.defaultBudgetForm(r)
	}
	data.ScopeTypeOptions = budgetScopeTypeOptions(data.Form.ScopeType)

	if h.db != nil {
		categories, err := h.categories.ListCategories(r.Context())
		if err != nil && data.Error == "" {
			data.Error = err.Error()
			data.Notices = uiNotices(data.Flash, data.Error)
		}
		for _, category := range categories {
			data.CategoryOptions = append(data.CategoryOptions, category.Name)
		}

		evaluations, err := h.budgets.EvaluateBudgets(r.Context(), persistence.BudgetEvaluationRequest{
			BillingPeriodStart: data.Form.BillingPeriodStart,
			BillingPeriodEnd:   data.Form.BillingPeriodEnd,
		})
		if err != nil && data.Error == "" {
			data.Error = err.Error()
			data.Notices = uiNotices(data.Flash, data.Error)
		}
		data.ThresholdRows = budgetThresholdRowsFromEvaluations(evaluations, categories)
		data.StateCards = budgetStateCards(evaluations)
	}

	renderPage(w, status, pageLayoutOptions{
		Title:     "Budgets - AWS Billing Simulator",
		ActiveNav: "budgets",
	}, budgetsPageTemplate, data, "render budgets page")
}

func (h budgetHandler) defaultBudgetForm(r *http.Request) budgetFormView {
	form := budgetFormView{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		Amount:             "0.10",
		ScopeType:          persistence.BudgetScopeAccount,
		ScopeValue:         defaultUsageAccountID,
		ActualThreshold:    "80",
		ForecastThreshold:  "100",
	}
	if h.db == nil {
		return form
	}
	clock, err := h.clock.Get(r.Context())
	if err == nil {
		form.BillingPeriodStart = clock.BillingPeriodStart
		form.BillingPeriodEnd = clock.BillingPeriodEnd
	}
	return form
}

func budgetCreateRequestFromForm(form budgetFormView) (persistence.BudgetCreateRequest, error) {
	amountMicros, err := parseBudgetAmountMicros(form.Amount)
	if err != nil {
		return persistence.BudgetCreateRequest{}, err
	}
	thresholds := []persistence.BudgetThresholdCreateRequest{}
	actualThreshold, err := parseBudgetThresholdBasisPoints(form.ActualThreshold)
	if err != nil {
		return persistence.BudgetCreateRequest{}, fmt.Errorf("actual threshold: %w", err)
	}
	thresholds = append(thresholds, persistence.BudgetThresholdCreateRequest{
		ThresholdType:        persistence.BudgetThresholdTypeActual,
		ThresholdBasisPoints: actualThreshold,
	})
	forecastThreshold, err := parseBudgetThresholdBasisPoints(form.ForecastThreshold)
	if err != nil {
		return persistence.BudgetCreateRequest{}, fmt.Errorf("forecast threshold: %w", err)
	}
	thresholds = append(thresholds, persistence.BudgetThresholdCreateRequest{
		ThresholdType:        persistence.BudgetThresholdTypeForecast,
		ThresholdBasisPoints: forecastThreshold,
	})

	return persistence.BudgetCreateRequest{
		Name:               form.Name,
		Description:        form.Description,
		BillingPeriodStart: form.BillingPeriodStart,
		BillingPeriodEnd:   form.BillingPeriodEnd,
		BudgetAmountMicros: amountMicros,
		ScopeType:          form.ScopeType,
		ScopeKey:           form.ScopeKey,
		ScopeValue:         form.ScopeValue,
		Thresholds:         thresholds,
	}, nil
}

func budgetFormFromValues(values url.Values) budgetFormView {
	return budgetFormView{
		Name:               firstValue(values, "name"),
		Description:        firstValue(values, "description"),
		BillingPeriodStart: firstValue(values, "billing_period_start"),
		BillingPeriodEnd:   firstValue(values, "billing_period_end"),
		Amount:             firstValue(values, "amount"),
		ScopeType:          firstValue(values, "scope_type"),
		ScopeKey:           firstValue(values, "scope_key"),
		ScopeValue:         firstValue(values, "scope_value"),
		ActualThreshold:    firstValue(values, "actual_threshold"),
		ForecastThreshold:  firstValue(values, "forecast_threshold"),
	}
}

func budgetThresholdRowsFromEvaluations(evaluations []persistence.BudgetEvaluation, categories []persistence.CostCategory) []budgetThresholdRowView {
	categoryNames := map[string]string{}
	for _, category := range categories {
		categoryNames[category.ID] = category.Name
	}
	rows := []budgetThresholdRowView{}
	for _, evaluation := range evaluations {
		scope := budgetScopeLabel(evaluation.Budget, categoryNames)
		for _, check := range evaluation.ThresholdChecks {
			rows = append(rows, budgetThresholdRowView{
				BudgetName:       evaluation.Budget.Name,
				Scope:            scope,
				Period:           evaluation.BillingPeriodStart + " to " + evaluation.BillingPeriodEnd,
				BudgetAmount:     formatUSDMicros(evaluation.Budget.BudgetAmountMicros),
				Metric:           budgetThresholdTypeLabel(check.ThresholdType),
				ThresholdPercent: formatBudgetPercentBasisPoints(int64(check.ThresholdBasisPoints)),
				ThresholdAmount:  formatUSDMicros(check.ThresholdAmountMicros),
				Spend:            formatUSDMicros(check.SpendMicros),
				PercentUsed:      formatBudgetPercentBasisPoints(check.PercentUsedBasisPoints),
				Remaining:        formatUSDMicros(check.RemainingCostMicros),
				LineItems:        evaluation.LineItemCount,
				Status:           budgetThresholdStatus(check.Breached),
				StatusClass:      budgetThresholdStatusClass(check.Breached),
			})
		}
	}
	return rows
}

func budgetStateCards(evaluations []persistence.BudgetEvaluation) []budgetStateCardView {
	breached := 0
	checks := 0
	for _, evaluation := range evaluations {
		for _, check := range evaluation.ThresholdChecks {
			checks++
			if check.Breached {
				breached++
			}
		}
	}
	return []budgetStateCardView{
		{Label: "Budgets", Value: strconv.Itoa(len(evaluations))},
		{Label: "Threshold Checks", Value: strconv.Itoa(checks)},
		{Label: "Breached", Value: strconv.Itoa(breached)},
	}
}

func budgetScopeLabel(budget persistence.Budget, categoryNames map[string]string) string {
	switch budget.ScopeType {
	case persistence.BudgetScopeAccount:
		return "Account " + budget.ScopeValue
	case persistence.BudgetScopeService:
		return "Service " + budget.ScopeValue
	case persistence.BudgetScopeTag:
		return "Tag " + budget.ScopeKey + "=" + budget.ScopeValue
	case persistence.BudgetScopeCostCategory:
		categoryName := categoryNames[budget.ScopeKey]
		if categoryName == "" {
			categoryName = budget.ScopeKey
		}
		return "Cost Category " + categoryName + "=" + budget.ScopeValue
	default:
		return budget.ScopeType + " " + budget.ScopeValue
	}
}

func budgetThresholdTypeLabel(thresholdType string) string {
	switch thresholdType {
	case persistence.BudgetThresholdTypeActual:
		return "Actual"
	case persistence.BudgetThresholdTypeForecast:
		return "Forecast"
	default:
		return thresholdType
	}
}

func budgetThresholdStatus(breached bool) string {
	if breached {
		return "Breached"
	}
	return "OK"
}

func budgetThresholdStatusClass(breached bool) string {
	if breached {
		return "status-deactivated"
	}
	return ""
}

func budgetScopeTypeOptions(selected string) []uiSelectOptionView {
	if selected == "" {
		selected = persistence.BudgetScopeAccount
	}
	return selectOptionsWithSelected([]uiSelectOptionView{
		{Value: persistence.BudgetScopeAccount, Label: "Account"},
		{Value: persistence.BudgetScopeService, Label: "Service"},
		{Value: persistence.BudgetScopeTag, Label: "Tag"},
		{Value: persistence.BudgetScopeCostCategory, Label: "Cost Category"},
	}, selected)
}

func parseBudgetAmountMicros(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "$", ""), ",", ""))
	if value == "" {
		return 0, fmt.Errorf("budget amount is required")
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("budget amount must be numeric: %w", err)
	}
	if amount <= 0 {
		return 0, fmt.Errorf("budget amount must be greater than zero")
	}
	micros := math.Round(amount * 1_000_000)
	if micros > float64(math.MaxInt64) {
		return 0, fmt.Errorf("budget amount is too large")
	}
	return int64(micros), nil
}

func parseBudgetThresholdBasisPoints(value string) (int, error) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
	if value == "" {
		return 0, fmt.Errorf("threshold percent is required")
	}
	percent, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("threshold percent must be numeric: %w", err)
	}
	if percent <= 0 {
		return 0, fmt.Errorf("threshold percent must be greater than zero")
	}
	basisPoints := math.Round(percent * 100)
	if basisPoints > 100000 {
		return 0, fmt.Errorf("threshold percent is too large")
	}
	return int(basisPoints), nil
}

func formatBudgetPercentBasisPoints(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	whole := value / 100
	fraction := value % 100
	if fraction == 0 {
		return fmt.Sprintf("%s%d%%", sign, whole)
	}
	fractionText := strings.TrimRight(fmt.Sprintf("%02d", fraction), "0")
	return fmt.Sprintf("%s%d.%s%%", sign, whole, fractionText)
}

func budgetsTables() budgetsTablesView {
	return budgetsTablesView{
		Thresholds: uiTable(uiTableHeaders("Budget", "Scope", "Period", "Amount", "Metric", "Threshold", "Spend", "Used", "Remaining", "Items", "Status"), "No budget threshold checks"),
	}
}

var budgetsPageTemplate = newPageTemplate("budgets-page", `<div class="page-heading">
			<div>
				<h1>Budgets</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="/cost-explorer">Cost Explorer</a>
				<a class="button-link secondary" href="/resources">Resources</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="state-grid" aria-label="Budget totals">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<form method="post" action="/budgets/create" class="report-builder-form">
				<div class="builder-grid">
					<section class="panel builder-panel">
						<h2>Budget Definition</h2>
						<div class="fields">
							<label class="form-row">Name
								<input name="name" value="{{.Form.Name}}" required>
							</label>
							<label class="form-row">Amount
								<input name="amount" value="{{.Form.Amount}}" inputmode="decimal" required>
							</label>
							<label class="form-row wide">Description
								<input name="description" value="{{.Form.Description}}">
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Month and Scope</h2>
						<div class="fields">
							<label class="form-row">Start Date
								<input type="date" name="billing_period_start" value="{{.Form.BillingPeriodStart}}" required>
							</label>
							<label class="form-row">End Date
								<input type="date" name="billing_period_end" value="{{.Form.BillingPeriodEnd}}" required>
							</label>
							<label class="form-row">Scope Type
								<select name="scope_type" required>
									{{range .ScopeTypeOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Scope Key
								<input name="scope_key" value="{{.Form.ScopeKey}}" list="budget-scope-keys">
							</label>
							<label class="form-row wide">Scope Value
								<input name="scope_value" value="{{.Form.ScopeValue}}" required>
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Thresholds</h2>
						<div class="fields">
							<label class="form-row">Actual Percent
								<input name="actual_threshold" value="{{.Form.ActualThreshold}}" inputmode="decimal" required>
							</label>
							<label class="form-row">Forecast Percent
								<input name="forecast_threshold" value="{{.Form.ForecastThreshold}}" inputmode="decimal" required>
							</label>
						</div>
						<div class="form-actions">
							<button type="submit">Create Budget</button>
						</div>
					</section>
				</div>
				<datalist id="budget-scope-keys">
					<option value="app"></option>
					<option value="owner"></option>
					<option value="product"></option>
					<option value="environment"></option>
					{{range .CategoryOptions}}<option value="{{.}}"></option>{{end}}
				</datalist>
			</form>

			<section>
				<div class="section-heading">
					<h2>Threshold Checks</h2>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Thresholds}}
						<tbody>
							{{range .ThresholdRows}}
								<tr>
									<td><strong>{{.BudgetName}}</strong></td>
									<td>{{.Scope}}</td>
									<td>{{.Period}}</td>
									<td>{{.BudgetAmount}}</td>
									<td>{{.Metric}}</td>
									<td>{{.ThresholdPercent}} / {{.ThresholdAmount}}</td>
									<td>{{.Spend}}</td>
									<td>{{.PercentUsed}}</td>
									<td>{{.Remaining}}</td>
									<td>{{.LineItems}}</td>
									<td><span class="status {{.StatusClass}}">{{.Status}}</span></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Thresholds}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
