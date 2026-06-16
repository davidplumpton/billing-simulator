package app

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type proFormaHandler struct {
	db           *sql.DB
	proForma     persistence.ProFormaBillingRepository
	clock        persistence.SimulatorClockRepository
	organization persistence.OrganizationRepository
}

type proFormaPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	ClockCurrentTime    string
	BillingPeriodStart  string
	BillingPeriodEnd    string
	StateCards          []proFormaStateCardView
	PricingPlans        []proFormaPricingPlanView
	PricingRules        []proFormaPricingRuleView
	BillingGroups       []proFormaBillingGroupView
	AccountAssignments  []proFormaAccountAssignmentView
	Summaries           []proFormaSummaryView
	LineItems           []proFormaLineItemView
	CustomLineItems     []proFormaCustomLineItemView
	PricingPlanOptions  []uiSelectOptionView
	BillingGroupOptions []uiSelectOptionView
	AccountOptions      []uiSelectOptionView
	CustomTypeOptions   []uiSelectOptionView
	Tables              proFormaTablesView
}

type proFormaStateCardView struct {
	Label string
	Value string
}

type proFormaPricingPlanView struct {
	ID           string
	Name         string
	Description  string
	CurrencyCode string
	Status       string
	RuleCount    int
}

type proFormaPricingRuleView struct {
	PricingPlanName string
	ServiceCode     string
	Multiplier      string
	Description     string
	Status          string
}

type proFormaBillingGroupView struct {
	ID              string
	Name            string
	Description     string
	PayerAccountID  string
	PricingPlanName string
	Status          string
	AccountCount    int
}

type proFormaAccountAssignmentView struct {
	BillingGroupName string
	AccountID        string
	AccountLabel     string
}

type proFormaSummaryView struct {
	BillingGroupName   string
	PricingPlanName    string
	Period             string
	PayerAccountID     string
	CurrencyCode       string
	SourceLineItems    int
	CustomLineItems    int
	SourceCost         string
	CustomAmount       string
	ProFormaCost       string
	Adjustment         string
	AdjustmentMicros   int64
	SourceActivityText string
	CustomActivityText string
}

type proFormaLineItemView struct {
	BillingGroupName string
	Service          string
	UsageType        string
	AccountID        string
	SourceCost       string
	ProFormaCost     string
	Adjustment       string
	Multiplier       string
	Status           string
	SourceID         string
}

type proFormaCustomLineItemView struct {
	BillingGroupName string
	Type             string
	Name             string
	Description      string
	Period           string
	Amount           string
	AmountMicros     int64
}

type proFormaTablesView struct {
	PricingPlans       uiTableView
	PricingRules       uiTableView
	BillingGroups      uiTableView
	AccountAssignments uiTableView
	Summaries          uiTableView
	LineItems          uiTableView
	CustomLineItems    uiTableView
}

// newProFormaHandler builds the repositories for pro forma billing workflows.
func newProFormaHandler(db *sql.DB) proFormaHandler {
	return proFormaHandler{
		db:           db,
		proForma:     persistence.NewProFormaBillingRepository(db),
		clock:        persistence.NewSimulatorClockRepository(db),
		organization: persistence.NewOrganizationRepository(db),
	}
}

// handleProForma renders pricing plans, billing groups, and generated showback rows.
func (h proFormaHandler) handleProForma(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderProForma(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleCreatePricingPlan creates an internal pricing plan.
func (h proFormaHandler) handleCreatePricingPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderProForma(w, r, http.StatusServiceUnavailable, "Open a workspace before creating pro forma pricing plans.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, "parse pricing plan form: "+err.Error(), "")
		return
	}
	plan, err := h.proForma.CreatePricingPlan(r.Context(), persistence.ProFormaPricingPlanCreateRequest{
		Name:        r.PostForm.Get("name"),
		Description: r.PostForm.Get("description"),
	})
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectProForma(w, r, "Created pricing plan "+plan.Name)
}

// handleCreatePricingRule adds one service multiplier to an internal pricing plan.
func (h proFormaHandler) handleCreatePricingRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderProForma(w, r, http.StatusServiceUnavailable, "Open a workspace before creating pro forma pricing rules.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, "parse pricing rule form: "+err.Error(), "")
		return
	}
	multiplier, err := parseProFormaMultiplierBasisPoints(r.PostForm.Get("multiplier_percent"))
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	rule, err := h.proForma.CreatePricingRule(r.Context(), persistence.ProFormaPricingRuleCreateRequest{
		PricingPlanID:             r.PostForm.Get("pricing_plan_id"),
		ServiceCode:               r.PostForm.Get("service_code"),
		RateMultiplierBasisPoints: multiplier,
		Description:               r.PostForm.Get("description"),
	})
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectProForma(w, r, fmt.Sprintf("Saved %s multiplier for %s", formatBasisPointsPercent(rule.RateMultiplierBasisPoints), rule.ServiceCode))
}

// handleCreateBillingGroup creates one pro forma billing group.
func (h proFormaHandler) handleCreateBillingGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderProForma(w, r, http.StatusServiceUnavailable, "Open a workspace before creating pro forma billing groups.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, "parse billing group form: "+err.Error(), "")
		return
	}
	group, err := h.proForma.CreateBillingGroup(r.Context(), persistence.ProFormaBillingGroupCreateRequest{
		Name:           r.PostForm.Get("name"),
		Description:    r.PostForm.Get("description"),
		PayerAccountID: r.PostForm.Get("payer_account_id"),
		PricingPlanID:  r.PostForm.Get("pricing_plan_id"),
	})
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectProForma(w, r, "Created billing group "+group.Name)
}

// handleAssignAccount assigns one usage account to a pro forma billing group.
func (h proFormaHandler) handleAssignAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderProForma(w, r, http.StatusServiceUnavailable, "Open a workspace before assigning pro forma accounts.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, "parse account assignment form: "+err.Error(), "")
		return
	}
	assignment, err := h.proForma.AssignAccountToGroup(r.Context(), persistence.ProFormaBillingGroupAccountCreateRequest{
		BillingGroupID: r.PostForm.Get("billing_group_id"),
		AccountID:      r.PostForm.Get("account_id"),
	})
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectProForma(w, r, "Assigned account "+assignment.AccountID)
}

// handleRefreshLineItems rebuilds generated pro forma rows for a selected period.
func (h proFormaHandler) handleRefreshLineItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderProForma(w, r, http.StatusServiceUnavailable, "Open a workspace before refreshing pro forma rows.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, "parse pro forma refresh form: "+err.Error(), "")
		return
	}
	result, err := h.proForma.RefreshLineItems(r.Context(), persistence.ProFormaRefreshRequest{
		BillingGroupID:     r.PostForm.Get("billing_group_id"),
		PayerAccountID:     r.PostForm.Get("payer_account_id"),
		BillingPeriodStart: r.PostForm.Get("billing_period_start"),
		BillingPeriodEnd:   r.PostForm.Get("billing_period_end"),
	})
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectProForma(w, r, fmt.Sprintf("Refreshed %d pro forma rows", result.ProFormaLineItems))
}

// handleCreateCustomLineItem adds one manual pro forma fee, credit, markup, or annotation.
func (h proFormaHandler) handleCreateCustomLineItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderProForma(w, r, http.StatusServiceUnavailable, "Open a workspace before creating pro forma custom line items.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, "parse custom line item form: "+err.Error(), "")
		return
	}
	lineItemType := strings.ToLower(strings.TrimSpace(r.PostForm.Get("line_item_type")))
	amountMicros, err := parseProFormaCustomAmountMicros(lineItemType, r.PostForm.Get("amount_usd"))
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	item, err := h.proForma.CreateCustomLineItem(r.Context(), persistence.ProFormaCustomLineItemCreateRequest{
		BillingGroupID:     r.PostForm.Get("billing_group_id"),
		BillingPeriodStart: r.PostForm.Get("billing_period_start"),
		BillingPeriodEnd:   r.PostForm.Get("billing_period_end"),
		LineItemType:       lineItemType,
		Name:               r.PostForm.Get("name"),
		Description:        r.PostForm.Get("description"),
		AmountMicros:       amountMicros,
	})
	if err != nil {
		h.renderProForma(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectProForma(w, r, "Added custom "+formatProFormaCustomLineItemType(item.LineItemType)+" "+item.Name)
}

// renderProForma builds the pro forma billing page.
func (h proFormaHandler) renderProForma(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := proFormaPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Tables:              proFormaTables(),
	}
	if h.db != nil {
		if err := h.loadProFormaPageData(r, &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices(data.Flash, data.Error)

	if wantsPageFragment(r, "pro-forma") {
		renderPageFragment(w, status, proFormaPageTemplate, "pro-forma.refresh", data, "render pro forma fragment")
		return
	}
	renderPage(w, status, pageLayoutOptions{
		Title:     "Pro Forma - Billing Simulator",
		ActiveNav: "pro-forma",
	}, proFormaPageTemplate, data, "render pro forma page")
}

// loadProFormaPageData reads current plans, groups, and generated showback rows.
func (h proFormaHandler) loadProFormaPageData(r *http.Request, data *proFormaPageData) error {
	ctx := r.Context()
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	data.ClockCurrentTime = clock.CurrentTime
	data.BillingPeriodStart = strings.TrimSpace(r.URL.Query().Get("billing_period_start"))
	data.BillingPeriodEnd = strings.TrimSpace(r.URL.Query().Get("billing_period_end"))
	if data.BillingPeriodStart == "" {
		data.BillingPeriodStart = clock.BillingPeriodStart
	}
	if data.BillingPeriodEnd == "" {
		data.BillingPeriodEnd = clock.BillingPeriodEnd
	}
	data.CustomTypeOptions = proFormaCustomLineItemTypeOptions()

	plans, err := h.proForma.ListPricingPlans(ctx)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		data.PricingPlans = append(data.PricingPlans, proFormaPricingPlanViewFromPlan(plan))
		data.PricingPlanOptions = append(data.PricingPlanOptions, uiSelectOptionView{
			Value: plan.ID,
			Label: plan.Name,
		})
	}
	rules, err := h.proForma.ListPricingRules(ctx, "")
	if err != nil {
		return err
	}
	for _, rule := range rules {
		data.PricingRules = append(data.PricingRules, proFormaPricingRuleViewFromRule(rule))
	}

	groups, err := h.proForma.ListBillingGroups(ctx)
	if err != nil {
		return err
	}
	groupNames := map[string]string{}
	for _, group := range groups {
		groupNames[group.ID] = group.Name
		data.BillingGroups = append(data.BillingGroups, proFormaBillingGroupViewFromGroup(group))
		data.BillingGroupOptions = append(data.BillingGroupOptions, uiSelectOptionView{
			Value: group.ID,
			Label: group.Name,
		})
	}
	assignments, err := h.proForma.ListBillingGroupAccounts(ctx, "")
	if err != nil {
		return err
	}
	accountLabels, err := h.accountLabels(ctx)
	if err != nil {
		return err
	}
	for _, assignment := range assignments {
		data.AccountAssignments = append(data.AccountAssignments, proFormaAccountAssignmentView{
			BillingGroupName: groupNames[assignment.BillingGroupID],
			AccountID:        assignment.AccountID,
			AccountLabel:     accountLabels[assignment.AccountID],
		})
	}
	data.AccountOptions = proFormaAccountOptions(accountLabels)

	summaries, err := h.proForma.ListBillingGroupSummaries(ctx, persistence.ProFormaSummaryRequest{
		BillingPeriodStart: data.BillingPeriodStart,
		BillingPeriodEnd:   data.BillingPeriodEnd,
	})
	if err != nil {
		return err
	}
	var sourceTotal, customTotal, proFormaTotal, adjustmentTotal int64
	var sourceRows, customRows int
	for _, summary := range summaries {
		data.Summaries = append(data.Summaries, proFormaSummaryViewFromSummary(summary))
		sourceTotal += summary.SourceCostMicros
		customTotal += summary.CustomAmountMicros
		proFormaTotal += summary.ProFormaCostMicros
		adjustmentTotal += summary.AdjustmentMicros
		sourceRows += summary.SourceLineItemCount
		customRows += summary.CustomLineItemCount
	}
	items, err := h.proForma.ListLineItems(ctx, persistence.ProFormaLineItemListRequest{
		BillingPeriodStart: data.BillingPeriodStart,
		BillingPeriodEnd:   data.BillingPeriodEnd,
		Limit:              50,
	})
	if err != nil {
		return err
	}
	for _, item := range items {
		data.LineItems = append(data.LineItems, proFormaLineItemViewFromItem(item))
	}
	customItems, err := h.proForma.ListCustomLineItems(ctx, persistence.ProFormaCustomLineItemListRequest{
		BillingPeriodStart: data.BillingPeriodStart,
		BillingPeriodEnd:   data.BillingPeriodEnd,
		Limit:              50,
	})
	if err != nil {
		return err
	}
	for _, item := range customItems {
		data.CustomLineItems = append(data.CustomLineItems, proFormaCustomLineItemViewFromItem(item))
	}
	data.StateCards = []proFormaStateCardView{
		{Label: "Pricing Plans", Value: strconv.Itoa(len(plans))},
		{Label: "Billing Groups", Value: strconv.Itoa(len(groups))},
		{Label: "Source Cost", Value: formatUSDMicros(sourceTotal)},
		{Label: "Custom Items", Value: strconv.Itoa(customRows)},
		{Label: "Custom Amount", Value: formatUSDMicros(customTotal)},
		{Label: "Pro Forma Cost", Value: formatUSDMicros(proFormaTotal)},
		{Label: "Adjustment", Value: formatUSDMicros(adjustmentTotal)},
		{Label: "Rows", Value: strconv.Itoa(sourceRows)},
	}
	return nil
}

func (h proFormaHandler) accountLabels(ctx context.Context) (map[string]string, error) {
	organization, err := h.organization.GetOrganizationByTemplate(ctx, persistence.AnyCompanyRetailTemplateKey)
	if err != nil {
		return nil, err
	}
	accounts, err := h.organization.ListAccounts(ctx, organization.ID)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for _, account := range accounts {
		if account.Status == persistence.AccountStatusClosed || account.IsManagementAccount {
			continue
		}
		labels[account.ID] = account.Name + " (" + account.ID + ")"
	}
	return labels, nil
}

func redirectProForma(w http.ResponseWriter, r *http.Request, flash string) {
	http.Redirect(w, r, "/pro-forma?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

func proFormaPricingPlanViewFromPlan(plan persistence.ProFormaPricingPlan) proFormaPricingPlanView {
	return proFormaPricingPlanView{
		ID:           plan.ID,
		Name:         plan.Name,
		Description:  plan.Description,
		CurrencyCode: plan.CurrencyCode,
		Status:       plan.Status,
		RuleCount:    plan.RuleCount,
	}
}

func proFormaPricingRuleViewFromRule(rule persistence.ProFormaPricingRule) proFormaPricingRuleView {
	return proFormaPricingRuleView{
		PricingPlanName: rule.PricingPlanName,
		ServiceCode:     rule.ServiceCode,
		Multiplier:      formatBasisPointsPercent(rule.RateMultiplierBasisPoints),
		Description:     rule.Description,
		Status:          rule.Status,
	}
}

func proFormaBillingGroupViewFromGroup(group persistence.ProFormaBillingGroup) proFormaBillingGroupView {
	return proFormaBillingGroupView{
		ID:              group.ID,
		Name:            group.Name,
		Description:     group.Description,
		PayerAccountID:  group.PayerAccountID,
		PricingPlanName: group.PricingPlanName,
		Status:          group.Status,
		AccountCount:    group.AccountCount,
	}
}

func proFormaSummaryViewFromSummary(summary persistence.ProFormaBillingGroupSummary) proFormaSummaryView {
	return proFormaSummaryView{
		BillingGroupName:   summary.BillingGroupName,
		PricingPlanName:    summary.PricingPlanName,
		Period:             summary.BillingPeriodStart + " to " + summary.BillingPeriodEnd,
		PayerAccountID:     summary.PayerAccountID,
		CurrencyCode:       summary.CurrencyCode,
		SourceLineItems:    summary.SourceLineItemCount,
		CustomLineItems:    summary.CustomLineItemCount,
		SourceCost:         formatUSDMicros(summary.SourceCostMicros),
		CustomAmount:       formatUSDMicros(summary.CustomAmountMicros),
		ProFormaCost:       formatUSDMicros(summary.ProFormaCostMicros),
		Adjustment:         formatUSDMicros(summary.AdjustmentMicros),
		AdjustmentMicros:   summary.AdjustmentMicros,
		SourceActivityText: formatCountLabel(summary.SourceLineItemCount, "source line item", "source line items"),
		CustomActivityText: formatCountLabel(summary.CustomLineItemCount, "custom item", "custom items"),
	}
}

func proFormaLineItemViewFromItem(item persistence.ProFormaLineItem) proFormaLineItemView {
	service := item.ServiceName
	if service == "" {
		service = item.ServiceCode
	}
	return proFormaLineItemView{
		BillingGroupName: item.BillingGroupName,
		Service:          service,
		UsageType:        item.UsageType,
		AccountID:        item.UsageAccountID,
		SourceCost:       formatUSDMicros(item.SourceCostMicros),
		ProFormaCost:     formatUSDMicros(item.ProFormaCostMicros),
		Adjustment:       formatUSDMicros(item.AdjustmentMicros),
		Multiplier:       formatBasisPointsPercent(item.RateMultiplierBasisPoints),
		Status:           item.LineItemStatus,
		SourceID:         item.SourceBillLineItemID,
	}
}

func proFormaCustomLineItemViewFromItem(item persistence.ProFormaCustomLineItem) proFormaCustomLineItemView {
	return proFormaCustomLineItemView{
		BillingGroupName: item.BillingGroupName,
		Type:             formatProFormaCustomLineItemType(item.LineItemType),
		Name:             item.Name,
		Description:      item.Description,
		Period:           item.BillingPeriodStart + " to " + item.BillingPeriodEnd,
		Amount:           formatUSDMicros(item.AmountMicros),
		AmountMicros:     item.AmountMicros,
	}
}

func proFormaAccountOptions(labels map[string]string) []uiSelectOptionView {
	ids := make([]string, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	options := make([]uiSelectOptionView, 0, len(ids))
	for _, id := range ids {
		options = append(options, uiSelectOptionView{
			Value: id,
			Label: labels[id],
		})
	}
	return options
}

func parseProFormaMultiplierBasisPoints(value string) (int, error) {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "%"))
	parsed, err := parsePositiveDecimalScaled(value, positiveDecimalScaleOptions{
		RequiredMessage: "rate multiplier percent is required",
		NumericMessage:  "rate multiplier percent must be numeric",
		FiniteMessage:   "rate multiplier percent must be finite",
		PositiveMessage: "rate multiplier percent must be greater than zero",
		TooLargeMessage: "rate multiplier percent is too large",
		Scale:           100,
		MaxScaled:       1_000_000,
	})
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

func parseProFormaCustomAmountMicros(lineItemType, value string) (int64, error) {
	lineItemType = strings.ToLower(strings.TrimSpace(lineItemType))
	if lineItemType == persistence.ProFormaCustomLineItemTypeAnnotation {
		return 0, nil
	}
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "$", ""), ",", ""))
	amountMicros, err := parsePositiveDecimalScaled(value, positiveDecimalScaleOptions{
		RequiredMessage: "custom line item amount is required",
		NumericMessage:  "custom line item amount must be numeric",
		FiniteMessage:   "custom line item amount must be finite",
		PositiveMessage: "custom line item amount must be greater than zero",
		TooLargeMessage: "custom line item amount is too large",
		Scale:           1_000_000,
		MaxScaled:       float64(math.MaxInt64),
	})
	if err != nil {
		return 0, err
	}
	if lineItemType == persistence.ProFormaCustomLineItemTypeCredit {
		return -amountMicros, nil
	}
	return amountMicros, nil
}

func formatBasisPointsPercent(value int) string {
	whole := value / 100
	fraction := value % 100
	if fraction == 0 {
		return fmt.Sprintf("%d%%", whole)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%02d", whole, fraction), "0"), ".") + "%"
}

func formatProFormaCustomLineItemType(lineItemType string) string {
	switch lineItemType {
	case persistence.ProFormaCustomLineItemTypeFee:
		return "Fee"
	case persistence.ProFormaCustomLineItemTypeCredit:
		return "Credit"
	case persistence.ProFormaCustomLineItemTypeMarkup:
		return "Markup"
	case persistence.ProFormaCustomLineItemTypeAnnotation:
		return "Annotation"
	default:
		return lineItemType
	}
}

func proFormaCustomLineItemTypeOptions() []uiSelectOptionView {
	return []uiSelectOptionView{
		{Value: persistence.ProFormaCustomLineItemTypeFee, Label: "Fee"},
		{Value: persistence.ProFormaCustomLineItemTypeMarkup, Label: "Markup"},
		{Value: persistence.ProFormaCustomLineItemTypeCredit, Label: "Credit"},
		{Value: persistence.ProFormaCustomLineItemTypeAnnotation, Label: "Annotation"},
	}
}

func proFormaTables() proFormaTablesView {
	return proFormaTablesView{
		PricingPlans:       uiTable(uiTableHeaders("Plan", "Currency", "Status", "Rules"), "No pricing plans"),
		PricingRules:       uiTable(uiTableHeaders("Plan", "Service", "Multiplier", "Status"), "No pricing rules"),
		BillingGroups:      uiTable(uiTableHeaders("Group", "Payer", "Plan", "Accounts", "Status"), "No billing groups"),
		AccountAssignments: uiTable(uiTableHeaders("Group", "Account"), "No assigned accounts"),
		Summaries:          uiTable(uiTableHeaders("Group", "Period", "Source Cost", "Custom Amount", "Pro Forma Cost", "Adjustment", "Activity"), "No pro forma rows for the selected period"),
		LineItems:          uiTable(uiTableHeaders("Group", "Service", "Account", "Source Cost", "Pro Forma Cost", "Adjustment", "Multiplier"), "No pro forma line items"),
		CustomLineItems:    uiTable(uiTableHeaders("Group", "Type", "Name", "Amount", "Period"), "No custom line items"),
	}
}

var proFormaPageTemplate = newPageTemplate("pro-forma-page", `<div class="page-heading">
			<div>
				<h1>Pro Forma</h1>
			</div>
		</div>

		<div id="pro-forma-refresh" data-partial-surface="pro-forma">
			{{template "pro-forma.refresh" .}}
		</div>

{{define "pro-forma.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Internal Showback</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.BillingPeriodStart}} to {{.BillingPeriodEnd}}</small>
				</div>
				<div class="page-actions">
					<a class="button-link secondary" href="/cost-categories">Cost Categories</a>
					<a class="button-link secondary" href="/bills">Bills</a>
					<a class="button-link" href="/pro-forma">Refresh</a>
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
				<form method="post" action="/pro-forma/pricing-plans/create" class="panel compact">
					<h2>New Pricing Plan</h2>
					<label class="form-row">Name
						<input name="name" required>
					</label>
					<label class="form-row">Description
						<input name="description">
					</label>
					<button type="submit">Create Plan</button>
				</form>

				{{if .PricingPlanOptions}}
					<form method="post" action="/pro-forma/pricing-rules/create" class="panel compact">
						<h2>Service Rate</h2>
						<label class="form-row">Plan
							<select name="pricing_plan_id" required>
								{{range .PricingPlanOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Service Code
							<input name="service_code" value="AmazonEC2" required>
						</label>
						<label class="form-row">Multiplier %
							<input name="multiplier_percent" value="100" inputmode="decimal" required>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Save Rate</button>
					</form>

					<form method="post" action="/pro-forma/billing-groups/create" class="panel compact">
						<h2>New Billing Group</h2>
						<label class="form-row">Name
							<input name="name" required>
						</label>
						<label class="form-row">Payer Account
							<input name="payer_account_id" value="999988887777" required>
						</label>
						<label class="form-row">Plan
							<select name="pricing_plan_id" required>
								{{range .PricingPlanOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Create Group</button>
					</form>
				{{end}}

				{{if and .BillingGroupOptions .AccountOptions}}
					<form method="post" action="/pro-forma/accounts/assign" class="panel compact">
						<h2>Assign Account</h2>
						<label class="form-row">Group
							<select name="billing_group_id" required>
								{{range .BillingGroupOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<button type="submit">Assign Account</button>
					</form>

					<form method="post" action="/pro-forma/refresh" class="panel compact">
						<h2>Refresh Rows</h2>
						<label class="form-row">Group
							<select name="billing_group_id">
								<option value="">All groups</option>
								{{range .BillingGroupOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Period Start
							<input name="billing_period_start" value="{{.BillingPeriodStart}}" required>
						</label>
						<label class="form-row">Period End
							<input name="billing_period_end" value="{{.BillingPeriodEnd}}" required>
						</label>
						<label class="form-row">Payer Account
							<input name="payer_account_id" value="999988887777">
						</label>
						<button type="submit">Refresh Rows</button>
					</form>

					<form method="post" action="/pro-forma/custom-line-items/create" class="panel compact">
						<h2>Custom Item</h2>
						<label class="form-row">Group
							<select name="billing_group_id" required>
								{{range .BillingGroupOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Type
							<select name="line_item_type" required>
								{{range .CustomTypeOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Name
							<input name="name" required>
						</label>
						<label class="form-row">Amount USD
							<input name="amount_usd" value="0.00" inputmode="decimal">
						</label>
						<label class="form-row">Period Start
							<input name="billing_period_start" value="{{.BillingPeriodStart}}" required>
						</label>
						<label class="form-row">Period End
							<input name="billing_period_end" value="{{.BillingPeriodEnd}}" required>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Add Item</button>
					</form>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Pricing Plans</h2>
					<span>{{len .PricingPlans}} plans</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PricingPlans}}
						<tbody>
							{{range .PricingPlans}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.CurrencyCode}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.RuleCount}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PricingPlans}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Pricing Rules</h2>
					<span>{{len .PricingRules}} rules</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PricingRules}}
						<tbody>
							{{range .PricingRules}}
								<tr>
									<td>{{.PricingPlanName}}</td>
									<td><strong>{{.ServiceCode}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{.Multiplier}}</td>
									<td><span class="status">{{.Status}}</span></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PricingRules}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Billing Groups</h2>
					<span>{{len .BillingGroups}} groups</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillingGroups}}
						<tbody>
							{{range .BillingGroups}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.PricingPlanName}}</td>
									<td>{{.AccountCount}}</td>
									<td><span class="status">{{.Status}}</span></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillingGroups}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Account Assignments</h2>
					<span>{{len .AccountAssignments}} accounts</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.AccountAssignments}}
						<tbody>
							{{range .AccountAssignments}}
								<tr>
									<td>{{.BillingGroupName}}</td>
									<td><strong>{{.AccountLabel}}</strong><small>{{.AccountID}}</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.AccountAssignments}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Showback Summary</h2>
					<span>{{len .Summaries}} groups</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Summaries}}
						<tbody>
							{{range .Summaries}}
								<tr>
									<td><strong>{{.BillingGroupName}}</strong><small>{{.PricingPlanName}}</small><small>{{.PayerAccountID}} / {{.CurrencyCode}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.SourceCost}}</td>
									<td>{{if .CustomLineItems}}<strong>{{.CustomAmount}}</strong>{{else}}{{.CustomAmount}}{{end}}</td>
									<td><strong>{{.ProFormaCost}}</strong></td>
									<td>{{if .AdjustmentMicros}}<strong>{{.Adjustment}}</strong>{{else}}{{.Adjustment}}{{end}}</td>
									<td>{{.SourceActivityText}} / {{.CustomActivityText}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Summaries}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Custom Items</h2>
					<span>{{len .CustomLineItems}} items</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.CustomLineItems}}
						<tbody>
							{{range .CustomLineItems}}
								<tr>
									<td>{{.BillingGroupName}}</td>
									<td><span class="status">{{.Type}}</span></td>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{if .AmountMicros}}<strong>{{.Amount}}</strong>{{else}}{{.Amount}}{{end}}</td>
									<td>{{.Period}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.CustomLineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Generated Rows</h2>
					<span>{{len .LineItems}} rows</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LineItems}}
						<tbody>
							{{range .LineItems}}
								<tr>
									<td><strong>{{.BillingGroupName}}</strong><small>{{.SourceID}}</small></td>
									<td>{{.Service}}<small>{{.UsageType}}</small><small>{{.Status}}</small></td>
									<td>{{.AccountID}}</td>
									<td>{{.SourceCost}}</td>
									<td><strong>{{.ProFormaCost}}</strong></td>
									<td>{{.Adjustment}}</td>
									<td>{{.Multiplier}}</td>
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
