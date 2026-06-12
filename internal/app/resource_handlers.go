package app

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"aws-billing-simulator/internal/persistence"
)

const (
	defaultUsageAccountID         = "111122223333"
	defaultUsageStartLocal        = "2026-02-01T00:00"
	defaultUsageEndLocal          = "2026-02-01T01:00"
	defaultUsageStartRFC3339      = "2026-02-01T00:00:00Z"
	defaultUsageEndRFC3339        = "2026-02-01T01:00:00Z"
	defaultGenerationStartDate    = "2026-02-01"
	defaultUsageGenerationDaySpan = 3
	defaultClockAdvanceAmount     = 1
)

type resourceLabHandler struct {
	db        *sql.DB
	resources persistence.ResourceUsageRepository
	billing   resourceBillingWorkflow
}

type resourcePreset struct {
	Key          string
	Label        string
	ServiceCode  string
	ServiceName  string
	ResourceType string
	DefaultSize  string
	DefaultName  string
	Attributes   map[string]string
}

type usagePreset struct {
	Key         string
	Label       string
	ServiceCode string
	UsageType   string
	Operation   string
	RegionCode  string
	Unit        string
}

type usageGenerationPreset struct {
	Key   persistence.UsageGenerationPattern
	Label string
}

type clockAdvanceUnitView struct {
	Key   persistence.SimulatorClockAdvanceUnit
	Label string
}

type resourceFormDefaults struct {
	UsageStartLocal     string
	UsageEndLocal       string
	UsageStartRFC3339   string
	UsageEndRFC3339     string
	GenerationStartDate string
}

type resourcePageData struct {
	WorkspaceReady             bool
	Flash                      string
	Error                      string
	Filters                    resourceFilterView
	Notices                    []uiNoticeView
	WorkspaceEmptyState        uiEmptyStateView
	ClockCurrentTime           string
	ClockBillingPeriod         string
	ClockAmountField           uiInputFieldView
	ClockUnitField             uiSelectFieldView
	ClockSubmitButton          uiSubmitButtonView
	DefaultClockAdvanceAmount  int
	DefaultAccountID           string
	DefaultPayerAccountID      string
	DefaultUsageStart          string
	DefaultUsageEnd            string
	DefaultGenerationStartDate string
	DefaultGenerationDays      int
	ResourcePresets            []resourcePreset
	RegionOptions              []string
	StatusOptions              []string
	UsagePresets               []usagePreset
	UsageGenerationPresets     []usageGenerationPreset
	ClockAdvanceUnits          []clockAdvanceUnitView
	Resources                  []resourceView
	UsageEvents                []usageEventView
	MeteringRecords            []meteringRecordView
	BillLineItems              []billLineItemView
	BillingPeriodSummaries     []billingPeriodSummaryView
	DailyMeteringJobRuns       []dailyMeteringJobRunView
	MonthEndCloses             []monthEndCloseView
	IssuedBills                []issuedBillView
	CatalogItems               []catalogItemView
	Tables                     resourceTablesView
}

type resourceTablesView struct {
	Inventory              uiTableView
	RecentUsage            uiTableView
	BillingPeriodSummaries uiTableView
	DailyMeteringJobRuns   uiTableView
	MonthEndCloses         uiTableView
	IssuedBills            uiTableView
	MeteringRecords        uiTableView
	BillLineItems          uiTableView
	PriceDimensions        uiTableView
}

type resourceFilterView struct {
	AccountID   string
	ServiceCode string
	HasFilters  bool
	ApplyButton uiSubmitButtonView
	ClearPath   string
}

type resourceView struct {
	ID               string
	Name             string
	AccountID        string
	RegionCode       string
	ServiceCode      string
	ResourceType     string
	Size             string
	Status           string
	CreatedAt        string
	UsageEventCount  int
	LastUsageEndTime string
	Tags             []keyValueView
	Attributes       []keyValueView
}

type usageEventView struct {
	ID                 string
	ResourceID         string
	ResourceName       string
	AccountID          string
	ServiceCode        string
	UsageType          string
	Operation          string
	RegionCode         string
	Window             string
	Quantity           string
	Unit               string
	EstimatedCost      string
	BillableDimensions string
	Tags               []keyValueView
}

type meteringRecordView struct {
	ResourceName       string
	AccountID          string
	ServiceCode        string
	BillableDimensions string
	Window             string
	Quantity           string
	Unit               string
	Tags               []keyValueView
}

type billLineItemView struct {
	ResourceName     string
	Period           string
	Status           string
	PayerAccountID   string
	UsageAccountID   string
	ServiceCode      string
	Description      string
	PricingQuantity  string
	PricingUnit      string
	UnblendedRate    string
	UnblendedCost    string
	PriceCatalogSKU  string
	PriceEffectiveOn string
	Tags             []keyValueView
}

type billingPeriodSummaryView struct {
	Period         string
	PayerAccountID string
	UsageAccountID string
	ServiceCode    string
	Status         string
	LineItemCount  int
	Cost           string
	RefreshedAt    string
}

type dailyMeteringJobRunView struct {
	ID                     string
	Trigger                string
	ClockTime              string
	PayerAccountID         string
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	SummariesRefreshed     int
	CompletedAt            string
}

type monthEndCloseView struct {
	ID                     string
	Period                 string
	PayerAccountID         string
	Status                 string
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	FinalizedLineItems     int
	FinalizedCost          string
	SummariesRefreshed     int
	ClosedAt               string
}

type issuedBillView struct {
	ID               string
	Period           string
	PayerAccountID   string
	BillState        string
	LineItemCount    int
	UsageCharge      string
	Credits          string
	Refunds          string
	Tax              string
	Total            string
	InvoiceID        string
	InvoiceStatus    string
	InvoiceAmountDue string
	InvoiceDate      string
	InvoiceDueDate   string
}

type catalogItemView struct {
	ServiceCode        string
	UsageType          string
	Operation          string
	RegionCode         string
	Unit               string
	UnitRate           string
	PeriodEstimate     string
	BillableDimensions string
}

func newResourceLabHandler(db *sql.DB) resourceLabHandler {
	return resourceLabHandler{
		db:        db,
		resources: persistence.NewResourceUsageRepository(db),
		billing:   newResourceBillingWorkflow(db),
	}
}

func (h resourceLabHandler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	http.Redirect(w, r, "/resources", http.StatusSeeOther)
}

func (h resourceLabHandler) handleResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderResources(w, r, http.StatusOK, "", flashFromQuery(r))
}

func (h resourceLabHandler) handleCreateResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before creating resources.", "")
		return
	}
	defaults, err := h.resourceFormDefaults(r.Context())
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	request, err := resourceCreateRequestFromForm(r, defaults)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	resource, err := h.resources.CreateResource(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape("Created "+displayResourceName(resource)), http.StatusSeeOther)
}

func (h resourceLabHandler) handleAddTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before adding tags.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderResources(w, r, http.StatusBadRequest, "parse tag form: "+err.Error(), "")
		return
	}
	tag, err := h.resources.AddTag(r.Context(), persistence.ResourceTagCreateRequest{
		ResourceID: r.PostForm.Get("resource_id"),
		Key:        r.PostForm.Get("tag_key"),
		Value:      r.PostForm.Get("tag_value"),
	})
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape("Added tag "+tag.Key), http.StatusSeeOther)
}

func (h resourceLabHandler) handleRecordUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before recording usage.", "")
		return
	}
	defaults, err := h.resourceFormDefaults(r.Context())
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	request, err := usageEventCreateRequestFromForm(r, defaults)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	event, err := h.resources.RecordUsageEvent(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape("Recorded "+formatQuantityMicros(event.UsageQuantityMicros)+" "+event.UsageUnit), http.StatusSeeOther)
}

func (h resourceLabHandler) handleGenerateUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before generating usage.", "")
		return
	}
	defaults, err := h.resourceFormDefaults(r.Context())
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	request, err := usageGenerationRequestFromForm(r, defaults)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.resources.GenerateUsage(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(usageGenerationFlash(result)), http.StatusSeeOther)
}

// usageGenerationFlash summarizes whether deterministic generation inserted or reused rows.
func usageGenerationFlash(result persistence.UsageGenerationResult) string {
	resourceName := displayResourceName(result.Resource)
	switch {
	case result.EventsCreated > 0 && result.EventsReused > 0:
		return "Generated " + strconv.Itoa(result.EventsCreated) + " new usage events and reused " + strconv.Itoa(result.EventsReused) + " existing usage events for " + resourceName
	case result.EventsCreated > 0:
		return "Generated " + strconv.Itoa(result.EventsCreated) + " usage events for " + resourceName
	case result.EventsReused > 0:
		return "Reused " + strconv.Itoa(result.EventsReused) + " existing usage events for " + resourceName
	default:
		return "No usage events generated for " + resourceName
	}
}

func (h resourceLabHandler) renderResources(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	defaults := defaultResourceFormDefaults()
	data := resourcePageData{
		WorkspaceReady:             h.db != nil,
		Flash:                      flashMessage,
		Error:                      errorMessage,
		Filters:                    resourceFilterFromRequest(r),
		WorkspaceEmptyState:        uiWorkspaceRequiredState(),
		DefaultClockAdvanceAmount:  defaultClockAdvanceAmount,
		DefaultAccountID:           defaultUsageAccountID,
		DefaultPayerAccountID:      persistence.AnyCompanyRetailManagementAccountID,
		DefaultUsageStart:          defaults.UsageStartLocal,
		DefaultUsageEnd:            defaults.UsageEndLocal,
		DefaultGenerationStartDate: defaults.GenerationStartDate,
		DefaultGenerationDays:      defaultUsageGenerationDaySpan,
		ResourcePresets:            resourcePresets(),
		RegionOptions:              []string{"us-east-1", "global"},
		StatusOptions:              []string{"active", "planned", "stopped", "deleted"},
		UsagePresets:               usagePresets(),
		UsageGenerationPresets:     usageGenerationPresets(),
		ClockAdvanceUnits:          clockAdvanceUnitOptions(),
		Tables:                     resourceTables(),
	}
	if h.db != nil {
		if err := h.loadResourcePageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	applyResourceFilters(&data)
	data.Notices = uiNotices(data.Flash, data.Error)
	data.ClockAmountField = uiInputField("Amount", "clock_advance_amount", strconv.Itoa(data.DefaultClockAdvanceAmount), true)
	data.ClockAmountField.InputMode = "numeric"
	data.ClockUnitField = clockAdvanceUnitSelectField(data.ClockAdvanceUnits)
	data.ClockSubmitButton = uiSubmitButton("Advance Clock")

	if wantsPageFragment(r, "resources") {
		renderPageFragment(w, status, resourcePageTemplate, "resources.refresh", data, "render resources fragment")
		return
	}

	renderPage(w, status, pageLayoutOptions{
		Title:     "Resources - AWS Billing Simulator",
		ActiveNav: "resources",
	}, resourcePageTemplate, data, "render resource page")
}

func (h resourceLabHandler) loadResourcePageData(ctx context.Context, data *resourcePageData) error {
	clock, err := h.billing.loadClockContext(ctx, data)
	if err != nil {
		return err
	}

	resourceSummaries, err := h.resources.ListResources(ctx)
	if err != nil {
		return err
	}
	resourceNames := map[string]string{}
	for _, summary := range resourceSummaries {
		view := resourceViewFromSummary(summary)
		resourceNames[view.ID] = view.Name
		data.Resources = append(data.Resources, view)
	}

	usageEvents, err := h.resources.ListUsageEvents(ctx, 25)
	if err != nil {
		return err
	}
	for _, event := range usageEvents {
		data.UsageEvents = append(data.UsageEvents, h.usageEventView(ctx, event, resourceNames[event.ResourceID]))
	}

	return h.billing.loadWorkflowData(ctx, data, resourceNames, clock)
}
