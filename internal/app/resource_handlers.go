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

var resourcePageTemplate = newPageTemplate("resource-page", `<div class="page-heading">
			<div>
				<h1>Resources</h1>
			</div>
		</div>

		<div id="resources-refresh" data-partial-surface="resources">
			{{template "resources.refresh" .}}
		</div>

{{define "resources.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="filter-bar" aria-label="Resource filters">
				<form method="get" action="/resources" class="filter-form" data-partial-form="resources" data-partial-target="#resources-refresh" data-partial-auto="true">
					<label>Account ID
						<input name="account_id" value="{{.Filters.AccountID}}">
					</label>
					<label>Service
						<input name="service_code" value="{{.Filters.ServiceCode}}">
					</label>
					{{template "ui.submit-button" .Filters.ApplyButton}}
					{{if .Filters.HasFilters}}<a class="button-link secondary" href="{{.Filters.ClearPath}}">Clear</a>{{end}}
				</form>
			</section>

			<section class="clock-strip">
				<div>
					<h2>Simulator Clock</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
				<form method="post" action="/clock/advance" class="clock-form">
					{{template "ui.input-field" .ClockAmountField}}
					{{template "ui.select-field" .ClockUnitField}}
					{{template "ui.submit-button" .ClockSubmitButton}}
				</form>
			</section>

			<section class="form-grid">
				<form method="post" action="/resources/create" class="panel">
					<h2>Create Resource</h2>
					<div class="fields">
						<label>Account ID
							<input name="account_id" value="{{.DefaultAccountID}}" required>
						</label>
						<label>Region
							<select name="region_code">
								{{range .RegionOptions}}<option value="{{.}}">{{.}}</option>{{end}}
							</select>
						</label>
						<label>Service
							<select name="service_preset">
								{{range .ResourcePresets}}<option value="{{.Key}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Size
							<input name="size" value="t3.medium" required>
						</label>
						<label>Name
							<input name="resource_name" value="Storefront web">
						</label>
						<label>Lifecycle
							<select name="status">
								{{range .StatusOptions}}<option value="{{.}}">{{.}}</option>{{end}}
							</select>
						</label>
						<label>Started At
							<input type="datetime-local" name="started_at" value="{{.DefaultUsageStart}}">
						</label>
						<label class="wide">Tags
							<textarea name="tags" rows="3">app=storefront
owner=web-platform</textarea>
						</label>
					</div>
					<button type="submit">Create Resource</button>
				</form>

				<form method="post" action="/resources/usage" class="panel">
					<h2>Generate Usage</h2>
					<div class="fields">
						<label>Resource
							<select name="resource_id" required>
								{{range .Resources}}<option value="{{.ID}}">{{.Name}} - {{.ServiceCode}}</option>{{end}}
							</select>
						</label>
						<label>Usage
							<select name="usage_preset">
								{{range .UsagePresets}}<option value="{{.Key}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Quantity
							<input name="quantity" value="1" inputmode="decimal" required>
						</label>
						<label>Start
							<input type="datetime-local" name="usage_start_time" value="{{.DefaultUsageStart}}">
						</label>
						<label>End
							<input type="datetime-local" name="usage_end_time" value="{{.DefaultUsageEnd}}">
						</label>
					</div>
					<button type="submit">Generate Usage</button>
				</form>

				<form method="post" action="/resources/generate" class="panel compact">
					<h2>Generate Pattern</h2>
					<div class="fields">
						<label>Resource
							<select name="resource_id" required>
								{{range .Resources}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
							</select>
						</label>
						<label>Pattern
							<select name="generation_pattern">
								{{range .UsageGenerationPresets}}<option value="{{.Key}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Start Date
							<input type="date" name="generation_start_date" value="{{.DefaultGenerationStartDate}}">
						</label>
						<label>Days
							<input name="generation_days" value="{{.DefaultGenerationDays}}" inputmode="numeric" required>
						</label>
					</div>
					<button type="submit">Generate Pattern</button>
				</form>

				<form method="post" action="/resources/tags" class="panel compact">
					<h2>Add Tag</h2>
					<div class="fields">
						<label>Resource
							<select name="resource_id" required>
								{{range .Resources}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
							</select>
						</label>
						<label>Key
							<input name="tag_key" required>
						</label>
						<label>Value
							<input name="tag_value">
						</label>
					</div>
					<button type="submit">Add Tag</button>
				</form>

				<form method="post" action="/resources/billing-pipeline" class="panel compact">
					<h2>Price Usage</h2>
					<div class="fields">
						<label>Payer Account ID
							<input name="payer_account_id" value="{{.DefaultPayerAccountID}}">
						</label>
					</div>
					<button type="submit">Run Billing Pipeline</button>
				</form>

				<form method="post" action="/resources/daily-metering" class="panel compact">
					<h2>Daily Metering</h2>
					<div class="fields">
						<label>Payer Account ID
							<input name="payer_account_id" value="{{.DefaultPayerAccountID}}">
						</label>
					</div>
					<button type="submit">Run Daily Metering</button>
				</form>

				<form method="post" action="/resources/month-close" class="panel compact">
					<h2>Month-End Close</h2>
					<div class="fields">
						<label>Payer Account ID
							<input name="payer_account_id" value="{{.DefaultPayerAccountID}}">
						</label>
						<label>Invoice Due Days
							<input name="invoice_due_days" value="14" inputmode="numeric" required>
						</label>
					</div>
					<button type="submit">Close Previous Period</button>
				</form>
			</section>

			<section>
				<div class="section-heading">
					<h2>Inventory</h2>
					<span>{{len .Resources}} resources</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Inventory}}
						<tbody>
							{{range .Resources}}
								<tr>
									<td><strong>{{.Name}}</strong><small>{{.ResourceType}}</small></td>
									<td>{{.AccountID}}</td>
									<td>{{.ServiceCode}}</td>
									<td>{{.RegionCode}}</td>
									<td>{{.Size}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{template "tags" .Tags}}</td>
									<td>{{.UsageEventCount}}{{if .LastUsageEndTime}}<small>{{.LastUsageEndTime}}</small>{{end}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Inventory}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Recent Usage</h2>
					<span>{{len .UsageEvents}} events</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.RecentUsage}}
						<tbody>
							{{range .UsageEvents}}
								<tr>
									<td><strong>{{.ResourceName}}</strong><small>{{.AccountID}}</small></td>
									<td><code>{{.BillableDimensions}}</code></td>
									<td>{{.Window}}</td>
									<td>{{.Quantity}} {{.Unit}}</td>
									<td>{{.EstimatedCost}}</td>
									<td>{{template "tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.RecentUsage}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Current Billing Summary</h2>
					<span>{{len .BillingPeriodSummaries}} summaries</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillingPeriodSummaries}}
						<tbody>
							{{range .BillingPeriodSummaries}}
								<tr>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.UsageAccountID}}</td>
									<td>{{.ServiceCode}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.LineItemCount}}</td>
									<td>{{.Cost}}</td>
									<td>{{.RefreshedAt}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillingPeriodSummaries}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Daily Metering Jobs</h2>
					<span>{{len .DailyMeteringJobRuns}} runs</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.DailyMeteringJobRuns}}
						<tbody>
							{{range .DailyMeteringJobRuns}}
								<tr>
									<td><strong>{{.CompletedAt}}</strong><small>{{.ID}}</small></td>
									<td>{{.Trigger}}</td>
									<td>{{.ClockTime}}</td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.MeteringRecordsCreated}}</td>
									<td>{{.BillLineItemsCreated}}</td>
									<td>{{.SummariesRefreshed}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.DailyMeteringJobRuns}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Closed Billing Periods</h2>
					<span>{{len .MonthEndCloses}} closes</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.MonthEndCloses}}
						<tbody>
							{{range .MonthEndCloses}}
								<tr>
									<td><strong>{{.ClosedAt}}</strong><small>{{.ID}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.MeteringRecordsCreated}}</td>
									<td>{{.FinalizedLineItems}}<small>{{.BillLineItemsCreated}} new</small></td>
									<td>{{.FinalizedCost}}</td>
									<td>{{.SummariesRefreshed}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.MonthEndCloses}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Issued Bills</h2>
					<span>{{len .IssuedBills}} bills</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.IssuedBills}}
						<tbody>
							{{range .IssuedBills}}
								<tr>
									<td><strong>{{.ID}}</strong><small>{{.InvoiceID}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td><span class="status">{{.BillState}}</span></td>
									<td>{{.LineItemCount}}</td>
									<td>{{.UsageCharge}}<small>Credits {{.Credits}} / refunds {{.Refunds}}</small></td>
									<td>{{.Tax}}</td>
									<td><strong>{{.Total}}</strong></td>
									<td><span class="status">{{.InvoiceStatus}}</span><small>{{.InvoiceAmountDue}}</small></td>
									<td>{{.InvoiceDueDate}}<small>{{.InvoiceDate}}</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.IssuedBills}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Metering Records</h2>
					<span>{{len .MeteringRecords}} records</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.MeteringRecords}}
						<tbody>
							{{range .MeteringRecords}}
								<tr>
									<td><strong>{{.ResourceName}}</strong><small>{{.AccountID}}</small></td>
									<td><code>{{.BillableDimensions}}</code></td>
									<td>{{.Window}}</td>
									<td>{{.Quantity}} {{.Unit}}</td>
									<td>{{template "tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.MeteringRecords}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Bill Line Items</h2>
					<span>{{len .BillLineItems}} items</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillLineItems}}
						<tbody>
							{{range .BillLineItems}}
								<tr>
									<td><strong>{{.ResourceName}}</strong><small>{{.PriceCatalogSKU}} @ {{.PriceEffectiveOn}}</small></td>
									<td>{{.Period}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td><strong>{{.PayerAccountID}}</strong><small>{{.UsageAccountID}}</small></td>
									<td>{{.ServiceCode}}</td>
									<td>{{.Description}}</td>
									<td>{{.PricingQuantity}} {{.PricingUnit}}</td>
									<td>{{.UnblendedRate}}</td>
									<td>{{.UnblendedCost}}</td>
									<td>{{template "tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillLineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Price Dimensions</h2>
					<span>{{len .CatalogItems}} rates</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PriceDimensions}}
						<tbody>
							{{range .CatalogItems}}
								<tr>
									<td>{{.ServiceCode}}</td>
									<td><code>{{.BillableDimensions}}</code></td>
									<td>{{.Unit}}</td>
									<td>{{.UnitRate}}</td>
									<td>{{.PeriodEstimate}}</td>
								</tr>
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}

{{define "tags"}}
	{{if .}}
		<div class="tags">
			{{range .}}<span>{{.Key}}={{.Value}}</span>{{end}}
		</div>
	{{else}}
		<span class="muted">untagged</span>
	{{end}}
{{end}}
`)
