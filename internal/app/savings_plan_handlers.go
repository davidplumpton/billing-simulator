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
	"time"

	"aws-billing-simulator/internal/persistence"
)

type savingsPlanHandler struct {
	db           *sql.DB
	savingsPlans persistence.SavingsPlanRepository
	clock        persistence.SimulatorClockRepository
	organization persistence.OrganizationRepository
}

type savingsPlanPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	ClockCurrentTime    string
	BillingPeriod       string
	DefaultPayerAccount string
	DefaultOwnerAccount string
	DefaultTermStart    string
	DefaultTermEnd      string
	AccountOptions      []uiSelectOptionView
	SharingScopeOptions []uiSelectOptionView
	StateCards          []savingsPlanStateCardView
	Purchases           []savingsPlanPurchaseView
	GeneratedSources    []savingsPlanSourceView
	Tables              savingsPlanTablesView
}

type savingsPlanStateCardView struct {
	Label string
	Value string
}

type savingsPlanPurchaseView struct {
	ID               string
	Description      string
	PayerAccountID   string
	OwnerAccountID   string
	PlanType         string
	Scope            string
	Reference        string
	Term             string
	HourlyCommitment string
	UpfrontFee       string
	Status           string
	PriceLineage     string
	GeneratedRows    int
}

type savingsPlanSourceView struct {
	SavingsPlanID   string
	Kind            string
	GeneratedRow    string
	GeneratedMeta   string
	GeneratedCost   string
	CoveredSource   string
	CoveredMeta     string
	CoveredCost     string
	AmortizedCost   string
	SourceAvailable bool
}

type savingsPlanTablesView struct {
	Purchases        uiTableView
	GeneratedSources uiTableView
}

// newSavingsPlanHandler builds repositories for simplified Compute Savings Plan workflows.
func newSavingsPlanHandler(db *sql.DB) savingsPlanHandler {
	return savingsPlanHandler{
		db:           db,
		savingsPlans: persistence.NewSavingsPlanRepository(db),
		clock:        persistence.NewSimulatorClockRepository(db),
		organization: persistence.NewOrganizationRepository(db),
	}
}

// handleSavingsPlans renders Savings Plan purchases and generated coverage rows.
func (h savingsPlanHandler) handleSavingsPlans(w http.ResponseWriter, r *http.Request) {
	h.renderSavingsPlans(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleCreateSavingsPlan stores one simplified Compute Savings Plan purchase.
func (h savingsPlanHandler) handleCreateSavingsPlan(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		h.renderSavingsPlans(w, r, http.StatusServiceUnavailable, "Open a workspace before creating Savings Plans.", "")
		return
	}
	request, err := savingsPlanCreateRequestFromForm(r)
	if err != nil {
		h.renderSavingsPlans(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	purchase, err := h.savingsPlans.CreatePurchase(r.Context(), request)
	if err != nil {
		h.renderSavingsPlans(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	redirectSavingsPlans(w, r, "Created Savings Plan "+purchase.ID)
}

// renderSavingsPlans prepares the browser page and partial-refresh payload.
func (h savingsPlanHandler) renderSavingsPlans(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := savingsPlanPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		DefaultPayerAccount: persistence.AnyCompanyRetailManagementAccountID,
		DefaultOwnerAccount: defaultUsageAccountID,
		SharingScopeOptions: savingsPlanSharingScopeOptions(),
		Tables:              savingsPlanTables(),
	}
	if h.db != nil {
		if err := h.loadSavingsPlanPageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices(data.Flash, data.Error)

	if wantsPageFragment(r, "savings-plans") {
		renderPageFragment(w, status, savingsPlanPageTemplate, "savings-plans.refresh", data, "render savings plans fragment")
		return
	}
	renderPage(w, status, pageLayoutOptions{
		Title:     "Savings Plans - Billing Simulator",
		ActiveNav: "savings-plans",
	}, savingsPlanPageTemplate, data, "render savings plans page")
}

// loadSavingsPlanPageData reads purchases, coverage links, clock defaults, and account labels.
func (h savingsPlanHandler) loadSavingsPlanPageData(ctx context.Context, data *savingsPlanPageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	data.ClockCurrentTime = clock.CurrentTime
	data.BillingPeriod = fmt.Sprintf("%s to %s", clock.BillingPeriodStart, clock.BillingPeriodEnd)
	data.DefaultTermStart, data.DefaultTermEnd = defaultSavingsPlanTermWindow(clock)
	payer, err := defaultBillingPayerAccountID(ctx, h.db, defaultUsageAccountID)
	if err != nil {
		return err
	}
	data.DefaultPayerAccount = payer

	labels, err := h.accountLabels(ctx)
	if err != nil {
		return err
	}
	data.AccountOptions = savingsPlanAccountOptions(labels, data.DefaultOwnerAccount)

	purchases, err := h.savingsPlans.ListPurchases(ctx)
	if err != nil {
		return err
	}
	details, err := h.savingsPlans.ListLineItemSourceDetails(ctx, "")
	if err != nil {
		return err
	}
	sourceCounts := map[string]int{}
	var generatedFees, generatedNegations, coveredCost, amortizedCost int64
	for _, detail := range details {
		sourceCounts[detail.SavingsPlanID]++
		switch detail.LineItemKind {
		case "upfront_fee", "recurring_fee":
			generatedFees += detail.GeneratedCostMicros
		case "negation":
			generatedNegations += detail.GeneratedCostMicros
			coveredCost += detail.CoveredCostMicros
			amortizedCost += detail.AmortizedCommitmentCostMicros
		}
		data.GeneratedSources = append(data.GeneratedSources, savingsPlanSourceViewFromDetail(detail))
	}
	var hourlyCommitment int64
	for _, purchase := range purchases {
		hourlyCommitment += purchase.HourlyCommitmentMicros
		data.Purchases = append(data.Purchases, savingsPlanPurchaseViewFromPurchase(purchase, sourceCounts[purchase.ID]))
	}
	data.StateCards = []savingsPlanStateCardView{
		{Label: "Purchases", Value: strconv.Itoa(len(purchases))},
		{Label: "Hourly Commitment", Value: formatUSDMicros(hourlyCommitment)},
		{Label: "Generated Fees", Value: formatUSDMicros(generatedFees)},
		{Label: "Negations", Value: formatUSDMicros(-generatedNegations)},
		{Label: "Covered Usage", Value: formatUSDMicros(coveredCost)},
		{Label: "Amortized Source Cost", Value: formatUSDMicros(amortizedCost)},
	}
	return nil
}

func (h savingsPlanHandler) accountLabels(ctx context.Context) (map[string]string, error) {
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

func savingsPlanCreateRequestFromForm(r *http.Request) (persistence.SavingsPlanPurchaseCreateRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.SavingsPlanPurchaseCreateRequest{}, fmt.Errorf("parse savings plan form: %w", err)
	}
	termStart, err := parseFormTimestamp(r.PostForm.Get("term_start_time"), "")
	if err != nil {
		return persistence.SavingsPlanPurchaseCreateRequest{}, fmt.Errorf("savings plan term start %w", err)
	}
	termEnd, err := parseFormTimestamp(r.PostForm.Get("term_end_time"), "")
	if err != nil {
		return persistence.SavingsPlanPurchaseCreateRequest{}, fmt.Errorf("savings plan term end %w", err)
	}
	hourlyCommitmentMicros, err := parseSavingsPlanPositiveUSDMicros(r.PostForm.Get("hourly_commitment_usd"), "hourly commitment")
	if err != nil {
		return persistence.SavingsPlanPurchaseCreateRequest{}, err
	}
	upfrontFeeMicros, err := parseSavingsPlanOptionalUSDMicros(r.PostForm.Get("upfront_fee_usd"), "upfront fee")
	if err != nil {
		return persistence.SavingsPlanPurchaseCreateRequest{}, err
	}
	return persistence.SavingsPlanPurchaseCreateRequest{
		PayerAccountID:         r.PostForm.Get("payer_account_id"),
		OwnerAccountID:         r.PostForm.Get("owner_account_id"),
		ReferenceUsageType:     r.PostForm.Get("reference_usage_type"),
		Operation:              "RunInstances",
		RegionCode:             r.PostForm.Get("region_code"),
		SharingScope:           r.PostForm.Get("sharing_scope"),
		TermStartTime:          termStart,
		TermEndTime:            termEnd,
		HourlyCommitmentMicros: hourlyCommitmentMicros,
		UpfrontFeeMicros:       upfrontFeeMicros,
		CurrencyCode:           "USD",
		Status:                 persistence.SavingsPlanStatusActive,
		Description:            r.PostForm.Get("description"),
	}, nil
}

func parseSavingsPlanPositiveUSDMicros(value, label string) (int64, error) {
	return parsePositiveDecimalScaled(normalizeSavingsPlanUSD(value), positiveDecimalScaleOptions{
		RequiredMessage: label + " is required",
		NumericMessage:  label + " must be numeric",
		FiniteMessage:   label + " must be finite",
		PositiveMessage: label + " must be greater than zero",
		TooLargeMessage: label + " is too large",
		Scale:           1_000_000,
		MaxScaled:       float64(math.MaxInt64),
	})
}

func parseSavingsPlanOptionalUSDMicros(value, label string) (int64, error) {
	return parseOptionalNonNegativeDecimalScaled(normalizeSavingsPlanUSD(value), optionalNonNegativeDecimalScaleOptions{
		NumericMessage:  label + " must be numeric",
		FiniteMessage:   label + " must be finite",
		NegativeMessage: label + " cannot be negative",
		TooLargeMessage: label + " is too large",
		Scale:           1_000_000,
		MaxScaled:       float64(math.MaxInt64),
	})
}

func normalizeSavingsPlanUSD(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "$", ""), ",", ""))
}

func redirectSavingsPlans(w http.ResponseWriter, r *http.Request, flash string) {
	http.Redirect(w, r, "/savings-plans?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

func defaultSavingsPlanTermWindow(clock persistence.SimulatorClock) (string, string) {
	start, err := time.Parse(time.DateOnly, clock.BillingPeriodStart)
	if err != nil {
		return "2026-02-01T00:00", "2026-03-01T00:00"
	}
	return start.UTC().Format("2006-01-02T15:04"), start.UTC().AddDate(0, 1, 0).Format("2006-01-02T15:04")
}

func savingsPlanAccountOptions(labels map[string]string, selectedAccountID string) []uiSelectOptionView {
	ids := make([]string, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	options := make([]uiSelectOptionView, 0, len(ids))
	for _, id := range ids {
		options = append(options, uiSelectOptionView{
			Value:    id,
			Label:    labels[id],
			Selected: id == selectedAccountID,
		})
	}
	return options
}

func savingsPlanSharingScopeOptions() []uiSelectOptionView {
	return []uiSelectOptionView{
		{Value: persistence.SavingsPlanSharingScopeOrganization, Label: "Organization"},
		{Value: persistence.SavingsPlanSharingScopeOwnerAccount, Label: "Owner account only"},
	}
}

func savingsPlanPurchaseViewFromPurchase(purchase persistence.SavingsPlanPurchase, generatedRows int) savingsPlanPurchaseView {
	return savingsPlanPurchaseView{
		ID:               purchase.ID,
		Description:      purchase.Description,
		PayerAccountID:   purchase.PayerAccountID,
		OwnerAccountID:   purchase.OwnerAccountID,
		PlanType:         titleLabel(purchase.PlanType),
		Scope:            savingsPlanScopeLabel(purchase.SharingScope),
		Reference:        billableDimensions(purchase.ServiceCode, purchase.ReferenceUsageType, purchase.Operation, purchase.RegionCode),
		Term:             purchase.TermStartTime + " to " + purchase.TermEndTime,
		HourlyCommitment: formatUSDMicros(purchase.HourlyCommitmentMicros) + "/hr",
		UpfrontFee:       formatUSDMicros(purchase.UpfrontFeeMicros),
		Status:           purchase.Status,
		PriceLineage:     purchase.PriceCatalogSKU + " effective " + purchase.PriceEffectiveDate,
		GeneratedRows:    generatedRows,
	}
}

func savingsPlanSourceViewFromDetail(detail persistence.SavingsPlanLineItemSourceDetail) savingsPlanSourceView {
	source := savingsPlanSourceView{
		SavingsPlanID: detail.SavingsPlanID,
		Kind:          savingsPlanKindLabel(detail.LineItemKind),
		GeneratedRow:  detail.GeneratedDescription,
		GeneratedMeta: strings.Join(nonEmptyStrings(detail.GeneratedLineItemType, detail.GeneratedOperation, detail.GeneratedStatus), " / "),
		GeneratedCost: savingsPlanGeneratedCost(detail),
		CoveredCost:   formatUSDMicros(detail.CoveredCostMicros),
		AmortizedCost: formatUSDMicros(detail.AmortizedCommitmentCostMicros),
	}
	if detail.SourceBillLineItemID != "" {
		source.SourceAvailable = true
		source.CoveredSource = detail.SourceDescription
		source.CoveredMeta = fmt.Sprintf(
			"%s / %s to %s / %s %s",
			detail.SourceUsageAccountID,
			detail.SourceUsageStartTime,
			detail.SourceUsageEndTime,
			formatQuantityMicros(detail.CoveredQuantityMicros),
			detail.SourcePricingUnit,
		)
	}
	return source
}

func savingsPlanGeneratedCost(detail persistence.SavingsPlanLineItemSourceDetail) string {
	if detail.GeneratedLineItemType == "Credit" {
		return formatUSDMicros(-detail.GeneratedCostMicros)
	}
	return formatUSDMicros(detail.GeneratedCostMicros)
}

func savingsPlanKindLabel(kind string) string {
	switch kind {
	case "upfront_fee":
		return "Upfront Fee"
	case "recurring_fee":
		return "Recurring Fee"
	case "negation":
		return "Negation"
	default:
		return titleLabel(kind)
	}
}

func savingsPlanScopeLabel(scope string) string {
	switch scope {
	case persistence.SavingsPlanSharingScopeOrganization:
		return "Organization"
	case persistence.SavingsPlanSharingScopeOwnerAccount:
		return "Owner account only"
	default:
		return scope
	}
}

func nonEmptyStrings(values ...string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func savingsPlanTables() savingsPlanTablesView {
	return savingsPlanTablesView{
		Purchases:        uiTable(uiTableHeaders("Purchase", "Payer", "Owner", "Reference", "Term", "Commitment", "Status", "Rows"), "No Savings Plan purchases"),
		GeneratedSources: uiTable(uiTableHeaders("Savings Plan", "Generated Row", "Generated Cost", "Covered Source", "Covered Cost", "Amortized Cost"), "No generated Savings Plan rows"),
	}
}
