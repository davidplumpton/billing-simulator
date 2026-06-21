package app

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
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
	h.renderProForma(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleCreatePricingPlan creates an internal pricing plan.
func (h proFormaHandler) handleCreatePricingPlan(w http.ResponseWriter, r *http.Request) {
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
	accountLabels, err := anyCompanyRetailActiveMemberAccountLabels(ctx, h.organization)
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
	data.AccountOptions = selectOptionsFromLabels(accountLabels, "")

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
