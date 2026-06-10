package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type costCategoriesHandler struct {
	db           *sql.DB
	categories   persistence.CostCategoryRepository
	splitCharges persistence.CostCategorySplitChargeRepository
	clock        persistence.SimulatorClockRepository
}

type costCategoriesPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	ClockCurrentTime    string
	ClockBillingPeriod  string
	SelectedCategoryID  string
	SelectedCategory    string
	NextRuleOrder       int
	StateCards          []costCategoryStateCardView
	CategoryOptions     []uiSelectOptionView
	DimensionOptions    []uiSelectOptionView
	OperatorOptions     []uiSelectOptionView
	SplitMethodOptions  []uiSelectOptionView
	Categories          []costCategoryView
	RuleEffects         []costCategoryRuleEffectView
	LineItems           []costCategoryLineItemView
	SplitRules          []costCategorySplitRuleView
	AllocationRows      []costCategoryAllocationComparisonRowView
	HasMoreLineItems    bool
	Tables              costCategoryTablesView
}

type costCategoryStateCardView struct {
	Label string
	Value string
}

type costCategoryView struct {
	ID           string
	Name         string
	Description  string
	DefaultValue string
	Status       string
	RuleCount    int
	Selected     bool
}

type costCategoryRuleEffectView struct {
	Order         int
	Value         string
	Description   string
	Conditions    []string
	MatchedItems  int
	MatchedSpend  string
	ShadowedItems int
	ShadowedSpend string
}

type costCategoryLineItemView struct {
	ID            string
	ResourceID    string
	AccountID     string
	Service       string
	UsageType     string
	RegionCode    string
	Status        string
	Cost          string
	BeforeValue   string
	PreviewValue  string
	MatchedRule   string
	ShadowedRules []string
	Tags          []string
}

type costCategorySplitRuleView struct {
	ID            string
	SourceValue   string
	Method        string
	Description   string
	Status        string
	TargetSummary string
}

type costCategoryAllocationComparisonRowView struct {
	Value                         string
	PayerAccountID                string
	CurrencyCode                  string
	LineItems                     int
	SourceLineItems               int
	Allocations                   int
	RawCost                       string
	CategoryCost                  string
	SplitAmount                   string
	TotalAllocatedCost            string
	UnallocatedResidual           string
	UnallocatedResidualCostMicros int64
	RawActivity                   string
	SourceActivity                string
	AllocationActivity            string
}

type costCategoryTablesView struct {
	Categories           uiTableView
	RuleEffects          uiTableView
	SplitRules           uiTableView
	AllocationComparison uiTableView
	LineItems            uiTableView
}

// newCostCategoriesHandler builds the repositories for Cost Category preview workflows.
func newCostCategoriesHandler(db *sql.DB) costCategoriesHandler {
	return costCategoriesHandler{
		db:           db,
		categories:   persistence.NewCostCategoryRepository(db),
		splitCharges: persistence.NewCostCategorySplitChargeRepository(db),
		clock:        persistence.NewSimulatorClockRepository(db),
	}
}

// handleCostCategories renders category rules and their current-period preview.
func (h costCategoriesHandler) handleCostCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderCostCategories(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleCreateCostCategory creates one previewable business category.
func (h costCategoriesHandler) handleCreateCostCategory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderCostCategories(w, r, http.StatusServiceUnavailable, "Open a workspace before creating cost categories.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderCostCategories(w, r, http.StatusBadRequest, "parse cost category form: "+err.Error(), "")
		return
	}
	category, err := h.categories.CreateCategory(r.Context(), persistence.CostCategoryCreateRequest{
		Name:         r.PostForm.Get("name"),
		Description:  r.PostForm.Get("description"),
		DefaultValue: r.PostForm.Get("default_value"),
	})
	if err != nil {
		h.renderCostCategories(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectCostCategory(w, r, category.ID, "Created cost category "+category.Name)
}

// handleCreateCostCategoryRule creates one ordered rule and immediately refreshes the preview.
func (h costCategoriesHandler) handleCreateCostCategoryRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderCostCategories(w, r, http.StatusServiceUnavailable, "Open a workspace before creating cost category rules.", "")
		return
	}
	request, err := h.costCategoryRuleRequestFromForm(r)
	if err != nil {
		h.renderCostCategories(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	rule, err := h.categories.CreateRule(r.Context(), request)
	if err != nil {
		h.renderCostCategories(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectCostCategory(w, r, rule.CostCategoryID, fmt.Sprintf("Created rule %d for %s", rule.RuleOrder, rule.CostCategoryName))
}

// handleCreateCostCategorySplitRule creates one split-charge rule for the selected category.
func (h costCategoriesHandler) handleCreateCostCategorySplitRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderCostCategories(w, r, http.StatusServiceUnavailable, "Open a workspace before creating split-charge rules.", "")
		return
	}
	request, err := h.costCategorySplitRuleRequestFromForm(r)
	if err != nil {
		h.renderCostCategories(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	rule, err := h.splitCharges.CreateRule(r.Context(), request)
	if err != nil {
		h.renderCostCategories(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectCostCategory(w, r, rule.CostCategoryID, fmt.Sprintf("Created split rule for %s", rule.SourceValue))
}

// renderCostCategories builds the Cost Category preview page from the open workspace.
func (h costCategoriesHandler) renderCostCategories(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := costCategoriesPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Tables:              costCategoryTables(),
		DimensionOptions:    costCategoryDimensionOptions(""),
		OperatorOptions:     costCategoryOperatorOptions(""),
		SplitMethodOptions:  costCategorySplitMethodOptions(""),
		NextRuleOrder:       10,
	}
	if h.db != nil {
		if err := h.loadCostCategoriesPageData(r.Context(), r, &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices(data.Flash, data.Error)

	if wantsPageFragment(r, "cost-categories") {
		renderPageFragment(w, status, costCategoriesPageTemplate, "cost-categories.refresh", data, "render cost categories fragment")
		return
	}
	renderPage(w, status, pageLayoutOptions{
		Title:     "Cost Categories - AWS Billing Simulator",
		ActiveNav: "cost-categories",
	}, costCategoriesPageTemplate, data, "render cost categories page")
}

// loadCostCategoriesPageData prepares current-period preview data and form defaults.
func (h costCategoriesHandler) loadCostCategoriesPageData(ctx context.Context, r *http.Request, data *costCategoriesPageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf("%s to %s (%d days)", clock.BillingPeriodStart, clock.BillingPeriodEnd, clock.BillingPeriodDays)

	categories, err := h.categories.ListCategories(ctx)
	if err != nil {
		return err
	}
	selectedID := strings.TrimSpace(r.URL.Query().Get("category_id"))
	if selectedID == "" && len(categories) > 0 {
		selectedID = categories[0].ID
	}
	data.SelectedCategoryID = selectedID

	for _, category := range categories {
		rules, err := h.categories.ListRules(ctx, category.ID)
		if err != nil {
			return err
		}
		selected := category.ID == selectedID
		if selected {
			data.SelectedCategory = category.Name
			data.NextRuleOrder = nextCostCategoryRuleOrder(rules)
		}
		data.CategoryOptions = append(data.CategoryOptions, uiSelectOptionView{
			Value:    category.ID,
			Label:    category.Name,
			Selected: selected,
		})
		data.Categories = append(data.Categories, costCategoryView{
			ID:           category.ID,
			Name:         category.Name,
			Description:  category.Description,
			DefaultValue: category.DefaultValue,
			Status:       category.Status,
			RuleCount:    len(rules),
			Selected:     selected,
		})
	}

	if selectedID == "" {
		data.StateCards = costCategoryStateCards(persistence.CostCategoryPreview{})
		return nil
	}
	preview, err := h.categories.PreviewCategory(ctx, persistence.CostCategoryPreviewRequest{
		CostCategoryID:     selectedID,
		BillingPeriodStart: clock.BillingPeriodStart,
		BillingPeriodEnd:   clock.BillingPeriodEnd,
		LineItemLimit:      100,
	})
	if err != nil {
		return err
	}
	data.StateCards = costCategoryStateCards(preview)
	for _, summary := range preview.RuleSummaries {
		data.RuleEffects = append(data.RuleEffects, costCategoryRuleEffectViewFromSummary(summary))
	}
	for _, item := range preview.LineItems {
		data.LineItems = append(data.LineItems, costCategoryLineItemViewFromPreview(item))
	}
	data.HasMoreLineItems = preview.HasMoreLineItems

	splitRules, err := h.splitCharges.ListRules(ctx, selectedID)
	if err != nil {
		return err
	}
	for _, rule := range splitRules {
		data.SplitRules = append(data.SplitRules, costCategorySplitRuleViewFromRule(rule))
	}
	comparison, err := h.splitCharges.CompareAllocations(ctx, persistence.CostCategorySplitChargeComparisonRequest{
		CostCategoryID:     selectedID,
		BillingPeriodStart: clock.BillingPeriodStart,
		BillingPeriodEnd:   clock.BillingPeriodEnd,
	})
	if err != nil {
		return err
	}
	for _, row := range comparison.Rows {
		data.AllocationRows = append(data.AllocationRows, costCategoryAllocationComparisonRowViewFromRow(row))
	}
	return nil
}

// costCategoryRuleRequestFromForm converts the compact one-condition rule form into the repository request.
func (h costCategoriesHandler) costCategoryRuleRequestFromForm(r *http.Request) (persistence.CostCategoryRuleCreateRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.CostCategoryRuleCreateRequest{}, fmt.Errorf("parse cost category rule form: %w", err)
	}
	ruleOrder, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("rule_order")))
	if err != nil {
		return persistence.CostCategoryRuleCreateRequest{}, fmt.Errorf("rule order must be a number")
	}
	condition := persistence.CostCategoryRuleCondition{
		ConditionOrder: 1,
		Dimension:      strings.TrimSpace(r.PostForm.Get("dimension")),
		Operator:       strings.TrimSpace(r.PostForm.Get("operator")),
		Values:         splitRuleValues(r.PostForm.Get("values")),
	}
	if condition.Dimension == persistence.CostCategoryRuleMatchTag {
		condition.TagKey = r.PostForm.Get("tag_key")
	}
	if condition.Dimension == persistence.CostCategoryRuleMatchCostCategory {
		condition.CostCategoryID = r.PostForm.Get("referenced_category_id")
	}
	return persistence.CostCategoryRuleCreateRequest{
		CostCategoryID: r.PostForm.Get("category_id"),
		RuleOrder:      ruleOrder,
		Value:          r.PostForm.Get("value"),
		Description:    r.PostForm.Get("description"),
		Conditions:     []persistence.CostCategoryRuleCondition{condition},
	}, nil
}

// costCategorySplitRuleRequestFromForm converts the browser form into ordered split targets.
func (h costCategoriesHandler) costCategorySplitRuleRequestFromForm(r *http.Request) (persistence.CostCategorySplitChargeRuleCreateRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.CostCategorySplitChargeRuleCreateRequest{}, fmt.Errorf("parse cost category split rule form: %w", err)
	}
	method := strings.TrimSpace(r.PostForm.Get("method"))
	fixedShares, err := splitFixedShareMicros(r.PostForm.Get("fixed_share_micros"))
	if err != nil {
		return persistence.CostCategorySplitChargeRuleCreateRequest{}, err
	}
	if method != persistence.CostCategorySplitMethodFixed && len(fixedShares) > 0 {
		return persistence.CostCategorySplitChargeRuleCreateRequest{}, fmt.Errorf("fixed shares only apply to fixed split rules")
	}
	targetValues := splitRuleValues(r.PostForm.Get("target_values"))
	targets := make([]persistence.CostCategorySplitChargeTargetCreateRequest, 0, len(targetValues))
	targetSeen := map[string]bool{}
	for i, value := range targetValues {
		targetSeen[value] = true
		targets = append(targets, persistence.CostCategorySplitChargeTargetCreateRequest{
			TargetOrder:      (i + 1) * 10,
			TargetValue:      value,
			FixedShareMicros: fixedShares[value],
		})
	}
	for value := range fixedShares {
		if !targetSeen[value] {
			return persistence.CostCategorySplitChargeRuleCreateRequest{}, fmt.Errorf("fixed share target %s is not listed as a target value", value)
		}
	}
	return persistence.CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: r.PostForm.Get("category_id"),
		SourceValue:    r.PostForm.Get("source_value"),
		Method:         method,
		Description:    r.PostForm.Get("description"),
		Targets:        targets,
	}, nil
}

func redirectCostCategory(w http.ResponseWriter, r *http.Request, categoryID, flash string) {
	query := "?flash=" + urlQueryEscape(flash)
	if strings.TrimSpace(categoryID) != "" {
		query = "?category_id=" + urlQueryEscape(categoryID) + "&flash=" + urlQueryEscape(flash)
	}
	http.Redirect(w, r, "/cost-categories"+query, http.StatusSeeOther)
}

func splitRuleValues(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func splitFixedShareMicros(raw string) (map[string]int, error) {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	shares := map[string]int{}
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		value, shareText, ok := strings.Cut(token, "=")
		if !ok {
			return nil, fmt.Errorf("fixed shares must use value=micros entries")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("fixed share value is required")
		}
		share, err := strconv.Atoi(strings.TrimSpace(shareText))
		if err != nil {
			return nil, fmt.Errorf("fixed share for %s must be a number", value)
		}
		shares[value] = share
	}
	return shares, nil
}

func nextCostCategoryRuleOrder(rules []persistence.CostCategoryRule) int {
	next := 10
	for _, rule := range rules {
		if rule.RuleOrder >= next {
			next = rule.RuleOrder + 10
		}
	}
	return next
}

func costCategoryStateCards(preview persistence.CostCategoryPreview) []costCategoryStateCardView {
	return []costCategoryStateCardView{
		{Label: "Line Items", Value: fmt.Sprintf("%d", preview.TotalLineItemCount)},
		{Label: "Matched Spend", Value: formatUSDMicros(preview.MatchedCostMicros)},
		{Label: "Unmatched Spend", Value: formatUSDMicros(preview.UnmatchedCostMicros)},
		{Label: "Coverage", Value: formatCoveragePercent(preview.MatchedCostMicros, preview.TotalCostMicros)},
	}
}

func costCategoryRuleEffectViewFromSummary(summary persistence.CostCategoryPreviewRuleSummary) costCategoryRuleEffectView {
	return costCategoryRuleEffectView{
		Order:         summary.RuleOrder,
		Value:         summary.Value,
		Description:   summary.Description,
		Conditions:    summary.ConditionDescriptions,
		MatchedItems:  summary.MatchedLineItemCount,
		MatchedSpend:  formatUSDMicros(summary.MatchedCostMicros),
		ShadowedItems: summary.ShadowedLineItemCount,
		ShadowedSpend: formatUSDMicros(summary.ShadowedCostMicros),
	}
}

func costCategoryLineItemViewFromPreview(item persistence.CostCategoryPreviewLineItem) costCategoryLineItemView {
	service := item.ServiceName
	if service == "" {
		service = item.ServiceCode
	}
	matchedRule := "No rule"
	if item.MatchedRuleID != "" {
		matchedRule = fmt.Sprintf("%d %s", item.MatchedRuleOrder, item.MatchedRuleValue)
	}
	shadowed := make([]string, 0, len(item.ShadowedRules))
	for _, rule := range item.ShadowedRules {
		shadowed = append(shadowed, fmt.Sprintf("%d %s", rule.RuleOrder, rule.Value))
	}
	return costCategoryLineItemView{
		ID:            item.ID,
		ResourceID:    item.ResourceID,
		AccountID:     item.UsageAccountID,
		Service:       service,
		UsageType:     item.UsageType,
		RegionCode:    item.RegionCode,
		Status:        item.LineItemStatus,
		Cost:          formatUSDMicros(item.CostMicros),
		BeforeValue:   item.BeforeValue,
		PreviewValue:  item.PreviewValue,
		MatchedRule:   matchedRule,
		ShadowedRules: shadowed,
		Tags:          costCategoryPreviewTags(item.TagSnapshot),
	}
}

func costCategorySplitRuleViewFromRule(rule persistence.CostCategorySplitChargeRule) costCategorySplitRuleView {
	targets := make([]string, 0, len(rule.Targets))
	for _, target := range rule.Targets {
		label := target.TargetValue
		if rule.Method == persistence.CostCategorySplitMethodFixed {
			label = fmt.Sprintf("%s %s", target.TargetValue, formatFixedShareMicros(target.FixedShareMicros))
		}
		targets = append(targets, label)
	}
	return costCategorySplitRuleView{
		ID:            rule.ID,
		SourceValue:   rule.SourceValue,
		Method:        costCategorySplitMethodLabel(rule.Method),
		Description:   rule.Description,
		Status:        rule.Status,
		TargetSummary: strings.Join(targets, ", "),
	}
}

func costCategoryAllocationComparisonRowViewFromRow(row persistence.CostCategorySplitChargeComparisonRow) costCategoryAllocationComparisonRowView {
	return costCategoryAllocationComparisonRowView{
		Value:                         row.Value,
		PayerAccountID:                row.PayerAccountID,
		CurrencyCode:                  row.CurrencyCode,
		LineItems:                     row.LineItemCount,
		SourceLineItems:               row.SourceLineItemCount,
		Allocations:                   row.AllocationCount,
		RawCost:                       formatUSDMicros(row.RawCostMicros),
		CategoryCost:                  formatUSDMicros(row.CategoryCostMicros),
		SplitAmount:                   formatUSDMicros(row.NetSplitCostMicros),
		TotalAllocatedCost:            formatUSDMicros(row.TotalAllocatedCostMicros),
		UnallocatedResidual:           formatUSDMicros(row.UnallocatedResidualCostMicros),
		UnallocatedResidualCostMicros: row.UnallocatedResidualCostMicros,
		RawActivity:                   formatCountLabel(row.LineItemCount, "raw line item", "raw line items"),
		SourceActivity:                formatCountLabel(row.SourceLineItemCount, "source line item", "source line items"),
		AllocationActivity:            formatCountLabel(row.AllocationCount, "split allocation", "split allocations"),
	}
}

func costCategoryPreviewTags(snapshot map[string]string) []string {
	tags := make([]string, 0, len(snapshot))
	for key, value := range snapshot {
		tags = append(tags, key+"="+value)
	}
	sort.Strings(tags)
	return tags
}

func costCategoryDimensionOptions(selected string) []uiSelectOptionView {
	options := []struct {
		value string
		label string
	}{
		{persistence.CostCategoryRuleMatchAccount, "Account"},
		{persistence.CostCategoryRuleMatchService, "Service"},
		{persistence.CostCategoryRuleMatchRegion, "Region"},
		{persistence.CostCategoryRuleMatchUsageType, "Usage Type"},
		{persistence.CostCategoryRuleMatchLineItemType, "Line Item Type"},
		{persistence.CostCategoryRuleMatchTag, "Tag"},
		{persistence.CostCategoryRuleMatchCostCategory, "Cost Category"},
	}
	views := make([]uiSelectOptionView, 0, len(options))
	for _, option := range options {
		views = append(views, uiSelectOptionView{
			Value:    option.value,
			Label:    option.label,
			Selected: option.value == selected,
		})
	}
	return views
}

func costCategoryOperatorOptions(selected string) []uiSelectOptionView {
	if selected == "" {
		selected = persistence.CostCategoryRuleOperatorIn
	}
	options := []struct {
		value string
		label string
	}{
		{persistence.CostCategoryRuleOperatorIn, "In"},
		{persistence.CostCategoryRuleOperatorNotIn, "Not In"},
	}
	views := make([]uiSelectOptionView, 0, len(options))
	for _, option := range options {
		views = append(views, uiSelectOptionView{
			Value:    option.value,
			Label:    option.label,
			Selected: option.value == selected,
		})
	}
	return views
}

func costCategorySplitMethodOptions(selected string) []uiSelectOptionView {
	if selected == "" {
		selected = persistence.CostCategorySplitMethodEven
	}
	options := []struct {
		value string
		label string
	}{
		{persistence.CostCategorySplitMethodEven, "Even"},
		{persistence.CostCategorySplitMethodFixed, "Fixed"},
		{persistence.CostCategorySplitMethodProportional, "Proportional"},
	}
	views := make([]uiSelectOptionView, 0, len(options))
	for _, option := range options {
		views = append(views, uiSelectOptionView{
			Value:    option.value,
			Label:    option.label,
			Selected: option.value == selected,
		})
	}
	return views
}

func costCategorySplitMethodLabel(method string) string {
	for _, option := range costCategorySplitMethodOptions(method) {
		if option.Value == method {
			return option.Label
		}
	}
	return method
}

func formatFixedShareMicros(value int) string {
	whole := value / 10_000
	fraction := value % 10_000
	if fraction == 0 {
		return fmt.Sprintf("%d%%", whole)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%04d", whole, fraction), "0"), ".") + "%"
}

func formatCountLabel(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func costCategoryTables() costCategoryTablesView {
	return costCategoryTablesView{
		Categories:           uiTable(uiTableHeaders("Category", "Default", "Status", "Rules", "Preview"), "No cost categories"),
		RuleEffects:          uiTable(uiTableHeaders("Order", "Value", "Conditions", "First Match", "Shadowed"), "No rules for the selected category"),
		SplitRules:           uiTable(uiTableHeaders("Source", "Method", "Targets", "Status"), "No split-charge rules for the selected category"),
		AllocationComparison: uiTable(uiTableHeaders("Value", "Raw Cost", "Category Cost", "Split Amount", "Total Allocated", "Unallocated Residual", "Activity"), "No assigned costs in the current billing period"),
		LineItems:            uiTable(uiTableHeaders("Line Item", "Service", "Cost", "Before", "Preview", "Rule", "Tags"), "No line items in the current billing period"),
	}
}

var costCategoriesPageTemplate = newPageTemplate("cost-categories-page", `<div class="page-heading">
			<div>
				<h1>Cost Categories</h1>
			</div>
		</div>

		<div id="cost-categories-refresh" data-partial-surface="cost-categories">
			{{template "cost-categories.refresh" .}}
		</div>

{{define "cost-categories.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Cost Category Preview</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
				<div class="page-actions">
					<a class="button-link secondary" href="/resources">Resources</a>
					<a class="button-link secondary" href="/tags">Tags</a>
					<a class="button-link" href="/cost-categories{{if .SelectedCategoryID}}?category_id={{.SelectedCategoryID}}{{end}}">Refresh Preview</a>
				</div>
			</section>

			<section class="state-grid">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<section class="form-grid">
				<form method="post" action="/cost-categories/categories/create" class="panel compact">
					<h2>New Category</h2>
					<label class="form-row">Name
						<input name="name" required>
					</label>
					<label class="form-row">Default Value
						<input name="default_value" value="Uncategorized" required>
					</label>
					<label class="form-row">Description
						<input name="description">
					</label>
					<button type="submit">Create Category</button>
				</form>

				{{if .CategoryOptions}}
					<form method="post" action="/cost-categories/rules/create" class="panel compact">
						<h2>New Rule</h2>
						<label class="form-row">Category
							<select name="category_id" required>
								{{range .CategoryOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Order
							<input name="rule_order" value="{{.NextRuleOrder}}" inputmode="numeric" required>
						</label>
						<label class="form-row">Value
							<input name="value" required>
						</label>
						<label class="form-row">Dimension
							<select name="dimension" required>
								{{range .DimensionOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Operator
							<select name="operator" required>
								{{range .OperatorOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Values
							<input name="values" required>
						</label>
						<label class="form-row">Tag Key
							<input name="tag_key">
						</label>
						<label class="form-row">Referenced Category
							<select name="referenced_category_id">
								<option value=""></option>
								{{range .CategoryOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Create Rule</button>
					</form>

					<form method="post" action="/cost-categories/splits/create" class="panel compact">
						<h2>New Split Rule</h2>
						<label class="form-row">Category
							<select name="category_id" required>
								{{range .CategoryOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Source Value
							<input name="source_value" required>
						</label>
						<label class="form-row">Method
							<select name="method" required>
								{{range .SplitMethodOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Target Values
							<textarea name="target_values" rows="3" required></textarea>
						</label>
						<label class="form-row">Fixed Shares
							<textarea name="fixed_share_micros" rows="3"></textarea>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Create Split Rule</button>
					</form>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Categories</h2>
					<span>{{len .Categories}} categories</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Categories}}
						<tbody>
							{{range .Categories}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{.DefaultValue}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.RuleCount}}</td>
									<td>
										{{if .Selected}}<span class="status">Selected</span>{{else}}<a class="button-link secondary" href="/cost-categories?category_id={{.ID}}">Preview</a>{{end}}
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Categories}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Rule Order Effects</h2>
					<span>{{.SelectedCategory}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.RuleEffects}}
						<tbody>
							{{range .RuleEffects}}
								<tr>
									<td>{{.Order}}</td>
									<td><strong>{{.Value}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{range .Conditions}}<small>{{.}}</small>{{end}}</td>
									<td>{{.MatchedSpend}}<small>{{.MatchedItems}} line items</small></td>
									<td>{{.ShadowedSpend}}<small>{{.ShadowedItems}} line items</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.RuleEffects}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Split Charge Rules</h2>
					<span>{{.SelectedCategory}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.SplitRules}}
						<tbody>
							{{range .SplitRules}}
								<tr>
									<td><strong>{{.SourceValue}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.Method}}</td>
									<td>{{.TargetSummary}}</td>
									<td><span class="status">{{.Status}}</span></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.SplitRules}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Allocation Comparison</h2>
					<span>{{len .AllocationRows}} values</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.AllocationComparison}}
						<tbody>
							{{range .AllocationRows}}
								<tr>
									<td><strong>{{.Value}}</strong><small>{{.PayerAccountID}} / {{.CurrencyCode}}</small></td>
									<td>{{.RawCost}}</td>
									<td>{{.CategoryCost}}</td>
									<td>{{.SplitAmount}}</td>
									<td><strong>{{.TotalAllocatedCost}}</strong></td>
									<td>{{if .UnallocatedResidualCostMicros}}<strong>{{.UnallocatedResidual}}</strong>{{else}}{{.UnallocatedResidual}}{{end}}</td>
									<td><small>{{.RawActivity}}</small><small>{{.SourceActivity}}</small><small>{{.AllocationActivity}}</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.AllocationComparison}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Line Item Preview</h2>
					<span>{{len .LineItems}} rows{{if .HasMoreLineItems}} shown{{end}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LineItems}}
						<tbody>
							{{range .LineItems}}
								<tr>
									<td><strong>{{.ResourceID}}</strong><small>{{.ID}}</small><small>{{.AccountID}} / {{.RegionCode}}</small></td>
									<td>{{.Service}}<small>{{.UsageType}}</small><small>{{.Status}}</small></td>
									<td>{{.Cost}}</td>
									<td>{{.BeforeValue}}</td>
									<td><strong>{{.PreviewValue}}</strong></td>
									<td>
										{{.MatchedRule}}
										{{if .ShadowedRules}}<div class="tags">{{range .ShadowedRules}}<span>{{.}}</span>{{end}}</div>{{end}}
									</td>
									<td>{{if .Tags}}<div class="tags">{{range .Tags}}<span>{{.}}</span>{{end}}</div>{{end}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.LineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}
`)
