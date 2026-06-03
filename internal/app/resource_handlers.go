package app

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

const (
	defaultAccountID              = "111122223333"
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
	catalog   persistence.PriceCatalogRepository
	metering  persistence.MeteringRepository
	lineItems persistence.BillLineItemRepository
	clock     persistence.SimulatorClockRepository
	dailyJobs persistence.DailyMeteringJobRepository
	monthEnd  persistence.MonthEndCloseRepository
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
	ClockCurrentTime           string
	ClockBillingPeriod         string
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

type keyValueView struct {
	Key   string
	Value string
}

func newResourceLabHandler(db *sql.DB) resourceLabHandler {
	return resourceLabHandler{
		db:        db,
		resources: persistence.NewResourceUsageRepository(db),
		catalog:   persistence.NewPriceCatalogRepository(db),
		metering:  persistence.NewMeteringRepository(db),
		lineItems: persistence.NewBillLineItemRepository(db),
		clock:     persistence.NewSimulatorClockRepository(db),
		dailyJobs: persistence.NewDailyMeteringJobRepository(db),
		monthEnd:  persistence.NewMonthEndCloseRepository(db),
	}
}

func (h resourceLabHandler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	http.Redirect(w, r, "/resources", http.StatusSeeOther)
}

func (h resourceLabHandler) handleStylesheet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	fmt.Fprint(w, resourceLabCSS)
}

func (h resourceLabHandler) handleResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderResources(w, r, http.StatusOK, "", flashFromQuery(r))
}

func (h resourceLabHandler) handleCreateResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
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
		methodNotAllowed(w)
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
		methodNotAllowed(w)
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
		methodNotAllowed(w)
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
	http.Redirect(
		w,
		r,
		"/resources?flash="+urlQueryEscape("Generated "+strconv.Itoa(len(result.Events))+" usage events for "+displayResourceName(result.Resource)),
		http.StatusSeeOther,
	)
}

// handleRunBillingPipeline converts pending usage into metering records and priced bill line items.
func (h resourceLabHandler) handleRunBillingPipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before pricing usage.", "")
		return
	}
	request, err := billingPipelineRequestFromForm(r)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	meteringResult, err := h.metering.GenerateMeteringRecords(r.Context())
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	lineItemResult, err := h.lineItems.GenerateBillLineItems(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	clock, err := h.clock.Get(r.Context())
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	summaries, err := h.dailyJobs.RefreshBillingPeriodServiceSummaries(r.Context(), clock.BillingPeriodStart, clock.BillingPeriodEnd)
	if err != nil {
		h.renderResources(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Created %d metering records and %d bill line items; refreshed %d summaries",
		meteringResult.RecordsCreated,
		lineItemResult.ItemsCreated,
		len(summaries),
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleRunDailyMeteringJob runs clock-bounded metering and refreshes current-period summaries.
func (h resourceLabHandler) handleRunDailyMeteringJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before running daily metering.", "")
		return
	}
	request, err := dailyMeteringJobRequestFromForm(r, persistence.DailyMeteringJobTriggerOnDemand)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.dailyJobs.Run(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Daily metering created %d metering records, %d bill line items, and refreshed %d summaries",
		result.MeteringRecordsCreated,
		result.BillLineItemsCreated,
		len(result.Summaries),
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleRunMonthEndClose finalizes the completed billing period before the current simulator clock.
func (h resourceLabHandler) handleRunMonthEndClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before closing a billing period.", "")
		return
	}
	request, err := monthEndCloseRequestFromForm(r)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.monthEnd.ClosePreviousPeriod(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Month-end close finalized %d line items into bill %s for %s",
		result.FinalizedLineItems,
		result.Bill.ID,
		formatUSDMicros(result.Bill.TotalMicros),
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleAdvanceClock applies a learner-triggered deterministic time change.
func (h resourceLabHandler) handleAdvanceClock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderResources(w, r, http.StatusServiceUnavailable, "Open a workspace before advancing the clock.", "")
		return
	}
	request, err := clockAdvanceRequestFromForm(r)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	clock, err := h.clock.Advance(r.Context(), request)
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.dailyJobs.Run(r.Context(), persistence.DailyMeteringJobRequest{
		Trigger:        persistence.DailyMeteringJobTriggerClockAdvance,
		PayerAccountID: defaultAccountID,
	})
	if err != nil {
		h.renderResources(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := fmt.Sprintf(
		"Advanced clock to %s; daily metering created %d metering records and %d bill line items",
		clock.CurrentTime,
		result.MeteringRecordsCreated,
		result.BillLineItemsCreated,
	)
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

func (h resourceLabHandler) renderResources(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	defaults := defaultResourceFormDefaults()
	data := resourcePageData{
		WorkspaceReady:             h.db != nil,
		Flash:                      flashMessage,
		Error:                      errorMessage,
		DefaultClockAdvanceAmount:  defaultClockAdvanceAmount,
		DefaultAccountID:           defaultAccountID,
		DefaultPayerAccountID:      defaultAccountID,
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
	}
	if h.db != nil {
		if err := h.loadResourcePageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}

	var body bytes.Buffer
	if err := resourcePageTemplate.Execute(&body, data); err != nil {
		http.Error(w, "render resource page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}

func (h resourceLabHandler) loadResourcePageData(ctx context.Context, data *resourcePageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	applyClockToResourcePageData(data, clock)

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

	meteringRecords, err := h.metering.ListMeteringRecords(ctx, 25)
	if err != nil {
		return err
	}
	for _, record := range meteringRecords {
		data.MeteringRecords = append(data.MeteringRecords, meteringRecordViewFromRecord(record, resourceNames[record.ResourceID]))
	}

	billLineItems, err := h.lineItems.ListBillLineItems(ctx, 25)
	if err != nil {
		return err
	}
	for _, item := range billLineItems {
		data.BillLineItems = append(data.BillLineItems, billLineItemViewFromItem(item, resourceNames[item.ResourceID]))
	}

	summaries, err := h.dailyJobs.ListBillingPeriodServiceSummaries(ctx, clock.BillingPeriodStart, clock.BillingPeriodEnd)
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		data.BillingPeriodSummaries = append(data.BillingPeriodSummaries, billingPeriodSummaryViewFromSummary(summary))
	}

	runs, err := h.dailyJobs.ListRuns(ctx, 10)
	if err != nil {
		return err
	}
	for _, run := range runs {
		data.DailyMeteringJobRuns = append(data.DailyMeteringJobRuns, dailyMeteringJobRunViewFromRun(run))
	}

	closes, err := h.monthEnd.ListRecentCloses(ctx, 10)
	if err != nil {
		return err
	}
	for _, close := range closes {
		data.MonthEndCloses = append(data.MonthEndCloses, monthEndCloseViewFromClose(close))
	}

	issuedBills, err := h.monthEnd.ListIssuedBills(ctx, 10)
	if err != nil {
		return err
	}
	for _, issuedBill := range issuedBills {
		data.IssuedBills = append(data.IssuedBills, issuedBillViewFromBill(issuedBill))
	}

	catalogItems, err := h.catalog.List(ctx)
	if err != nil {
		return err
	}
	for _, item := range catalogItems {
		data.CatalogItems = append(data.CatalogItems, catalogItemViewFromCatalog(item, clock.BillingPeriodDays))
	}
	return nil
}

func (h resourceLabHandler) resourceFormDefaults(ctx context.Context) (resourceFormDefaults, error) {
	defaults := defaultResourceFormDefaults()
	if h.db == nil {
		return defaults, nil
	}
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return resourceFormDefaults{}, err
	}
	parsed, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return resourceFormDefaults{}, fmt.Errorf("parse simulator clock: %w", err)
	}
	return resourceFormDefaultsForTime(parsed), nil
}

func resourceCreateRequestFromForm(r *http.Request, defaults resourceFormDefaults) (persistence.ResourceCreateRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.ResourceCreateRequest{}, fmt.Errorf("parse resource form: %w", err)
	}
	preset, ok := resourcePresetByKey(r.PostForm.Get("service_preset"))
	if !ok {
		return persistence.ResourceCreateRequest{}, fmt.Errorf("unknown resource service preset")
	}
	status := strings.TrimSpace(r.PostForm.Get("status"))
	startedAt := ""
	if status != "planned" {
		parsed, err := parseFormTimestamp(r.PostForm.Get("started_at"), defaults.UsageStartRFC3339)
		if err != nil {
			return persistence.ResourceCreateRequest{}, err
		}
		startedAt = parsed
	}

	resourceName := strings.TrimSpace(r.PostForm.Get("resource_name"))
	if resourceName == "" {
		resourceName = preset.DefaultName
	}
	size := strings.TrimSpace(r.PostForm.Get("size"))
	if size == "" {
		size = preset.DefaultSize
	}
	attributes := copyStringMap(preset.Attributes)
	attributes["size"] = size
	attributes["display_service"] = preset.ServiceName

	tags, err := parseTagsText(r.PostForm.Get("tags"))
	if err != nil {
		return persistence.ResourceCreateRequest{}, err
	}

	return persistence.ResourceCreateRequest{
		AccountID:    r.PostForm.Get("account_id"),
		RegionCode:   r.PostForm.Get("region_code"),
		ServiceCode:  preset.ServiceCode,
		ResourceType: preset.ResourceType,
		ResourceName: resourceName,
		Status:       status,
		StartedAt:    startedAt,
		Attributes:   attributes,
		Tags:         tags,
	}, nil
}

func usageEventCreateRequestFromForm(r *http.Request, defaults resourceFormDefaults) (persistence.UsageEventCreateRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.UsageEventCreateRequest{}, fmt.Errorf("parse usage form: %w", err)
	}
	preset, ok := usagePresetByKey(r.PostForm.Get("usage_preset"))
	if !ok {
		return persistence.UsageEventCreateRequest{}, fmt.Errorf("unknown usage preset")
	}
	start, err := parseFormTimestamp(r.PostForm.Get("usage_start_time"), defaults.UsageStartRFC3339)
	if err != nil {
		return persistence.UsageEventCreateRequest{}, err
	}
	end, err := parseFormTimestamp(r.PostForm.Get("usage_end_time"), defaults.UsageEndRFC3339)
	if err != nil {
		return persistence.UsageEventCreateRequest{}, err
	}
	quantityMicros, err := parseQuantityMicros(r.PostForm.Get("quantity"))
	if err != nil {
		return persistence.UsageEventCreateRequest{}, err
	}

	return persistence.UsageEventCreateRequest{
		ResourceID:          r.PostForm.Get("resource_id"),
		ServiceCode:         preset.ServiceCode,
		UsageType:           preset.UsageType,
		Operation:           preset.Operation,
		RegionCode:          preset.RegionCode,
		UsageStartTime:      start,
		UsageEndTime:        end,
		UsageQuantityMicros: quantityMicros,
		UsageUnit:           preset.Unit,
		Attributes: map[string]string{
			"generation": preset.Label,
		},
	}, nil
}

func usageGenerationRequestFromForm(r *http.Request, defaults resourceFormDefaults) (persistence.UsageGenerationRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.UsageGenerationRequest{}, fmt.Errorf("parse usage generation form: %w", err)
	}
	startDate := strings.TrimSpace(r.PostForm.Get("generation_start_date"))
	if startDate == "" {
		startDate = defaults.GenerationStartDate
	}
	days := defaultUsageGenerationDaySpan
	if rawDays := strings.TrimSpace(r.PostForm.Get("generation_days")); rawDays != "" {
		parsedDays, err := strconv.Atoi(rawDays)
		if err != nil {
			return persistence.UsageGenerationRequest{}, fmt.Errorf("generation days must be a whole number: %w", err)
		}
		days = parsedDays
	}

	return persistence.UsageGenerationRequest{
		ResourceID: r.PostForm.Get("resource_id"),
		Pattern:    persistence.UsageGenerationPattern(r.PostForm.Get("generation_pattern")),
		StartDate:  startDate,
		Days:       days,
	}, nil
}

func clockAdvanceRequestFromForm(r *http.Request) (persistence.SimulatorClockAdvanceRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.SimulatorClockAdvanceRequest{}, fmt.Errorf("parse clock form: %w", err)
	}
	amount := defaultClockAdvanceAmount
	if rawAmount := strings.TrimSpace(r.PostForm.Get("clock_advance_amount")); rawAmount != "" {
		parsedAmount, err := strconv.Atoi(rawAmount)
		if err != nil {
			return persistence.SimulatorClockAdvanceRequest{}, fmt.Errorf("clock advance amount must be a whole number: %w", err)
		}
		amount = parsedAmount
	}
	return persistence.SimulatorClockAdvanceRequest{
		Amount: amount,
		Unit:   persistence.SimulatorClockAdvanceUnit(r.PostForm.Get("clock_advance_unit")),
	}, nil
}

func billingPipelineRequestFromForm(r *http.Request) (persistence.BillLineItemGenerationRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.BillLineItemGenerationRequest{}, fmt.Errorf("parse billing pipeline form: %w", err)
	}
	return persistence.BillLineItemGenerationRequest{
		PayerAccountID: r.PostForm.Get("payer_account_id"),
	}, nil
}

func dailyMeteringJobRequestFromForm(r *http.Request, trigger persistence.DailyMeteringJobTrigger) (persistence.DailyMeteringJobRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.DailyMeteringJobRequest{}, fmt.Errorf("parse daily metering form: %w", err)
	}
	return persistence.DailyMeteringJobRequest{
		Trigger:        trigger,
		PayerAccountID: r.PostForm.Get("payer_account_id"),
	}, nil
}

func monthEndCloseRequestFromForm(r *http.Request) (persistence.MonthEndCloseRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.MonthEndCloseRequest{}, fmt.Errorf("parse month-end close form: %w", err)
	}
	dueDays := 0
	if rawDueDays := strings.TrimSpace(r.PostForm.Get("invoice_due_days")); rawDueDays != "" {
		parsedDueDays, err := strconv.Atoi(rawDueDays)
		if err != nil {
			return persistence.MonthEndCloseRequest{}, fmt.Errorf("invoice due days must be a whole number: %w", err)
		}
		dueDays = parsedDueDays
	}
	return persistence.MonthEndCloseRequest{
		PayerAccountID: r.PostForm.Get("payer_account_id"),
		InvoiceDueDays: dueDays,
	}, nil
}

func resourceViewFromSummary(summary persistence.ResourceSummary) resourceView {
	resource := summary.Resource
	name := displayResourceName(resource)
	return resourceView{
		ID:               resource.ID,
		Name:             name,
		AccountID:        resource.AccountID,
		RegionCode:       resource.RegionCode,
		ServiceCode:      resource.ServiceCode,
		ResourceType:     resource.ResourceType,
		Size:             resource.Attributes["size"],
		Status:           resource.Status,
		CreatedAt:        resource.CreatedAt,
		UsageEventCount:  summary.UsageEventCount,
		LastUsageEndTime: summary.LastUsageEndTime,
		Tags:             keyValueViews(summary.ActiveTags),
		Attributes:       keyValueViews(resource.Attributes),
	}
}

func meteringRecordViewFromRecord(record persistence.MeteringRecord, resourceName string) meteringRecordView {
	if resourceName == "" {
		resourceName = record.ResourceID
	}
	return meteringRecordView{
		ResourceName:       resourceName,
		AccountID:          record.AccountID,
		BillableDimensions: billableDimensions(record.ServiceCode, record.UsageType, record.Operation, record.RegionCode),
		Window:             record.UsageStartTime + " to " + record.UsageEndTime,
		Quantity:           formatQuantityMicros(record.UsageQuantityMicros),
		Unit:               record.UsageUnit,
		Tags:               keyValueViews(record.TagSnapshot),
	}
}

func billLineItemViewFromItem(item persistence.BillLineItem, resourceName string) billLineItemView {
	if resourceName == "" {
		resourceName = item.ResourceID
	}
	if resourceName == "" {
		resourceName = item.ServiceName
	}
	return billLineItemView{
		ResourceName:     resourceName,
		Period:           item.BillingPeriodStart + " to " + item.BillingPeriodEnd,
		Status:           item.LineItemStatus,
		PayerAccountID:   item.PayerAccountID,
		UsageAccountID:   item.UsageAccountID,
		ServiceCode:      item.ServiceCode,
		Description:      item.Description,
		PricingQuantity:  formatQuantityMicros(item.PricingQuantityMicros),
		PricingUnit:      item.PricingUnit,
		UnblendedRate:    formatUSDMicros(item.UnblendedRateMicros),
		UnblendedCost:    formatUSDMicros(item.UnblendedCostMicros),
		PriceCatalogSKU:  item.PriceCatalogSKU,
		PriceEffectiveOn: item.PriceEffectiveDate,
		Tags:             keyValueViews(item.TagSnapshot),
	}
}

func billingPeriodSummaryViewFromSummary(summary persistence.BillingPeriodServiceSummary) billingPeriodSummaryView {
	return billingPeriodSummaryView{
		Period:         summary.BillingPeriodStart + " to " + summary.BillingPeriodEnd,
		PayerAccountID: summary.PayerAccountID,
		UsageAccountID: summary.UsageAccountID,
		ServiceCode:    summary.ServiceCode,
		Status:         summary.LineItemStatus,
		LineItemCount:  summary.LineItemCount,
		Cost:           formatUSDMicros(summary.UnblendedCostMicros),
		RefreshedAt:    summary.RefreshedAt,
	}
}

func dailyMeteringJobRunViewFromRun(run persistence.DailyMeteringJobRun) dailyMeteringJobRunView {
	return dailyMeteringJobRunView{
		ID:                     run.ID,
		Trigger:                string(run.Trigger),
		ClockTime:              run.ClockTime,
		PayerAccountID:         run.PayerAccountID,
		MeteringRecordsCreated: run.MeteringRecordsCreated,
		BillLineItemsCreated:   run.BillLineItemsCreated,
		SummariesRefreshed:     run.SummariesRefreshed,
		CompletedAt:            run.CompletedAt,
	}
}

func monthEndCloseViewFromClose(close persistence.BillingPeriodClose) monthEndCloseView {
	return monthEndCloseView{
		ID:                     close.ID,
		Period:                 close.BillingPeriodStart + " to " + close.BillingPeriodEnd,
		PayerAccountID:         close.PayerAccountID,
		Status:                 close.Status,
		MeteringRecordsCreated: close.MeteringRecordsCreated,
		BillLineItemsCreated:   close.BillLineItemsCreated,
		FinalizedLineItems:     close.FinalizedLineItemCount,
		FinalizedCost:          formatUSDMicros(close.FinalizedCostMicros),
		SummariesRefreshed:     close.SummariesRefreshed,
		ClosedAt:               close.ClosedAt,
	}
}

func issuedBillViewFromBill(issued persistence.BillWithInvoiceObligation) issuedBillView {
	return issuedBillView{
		ID:               issued.Bill.ID,
		Period:           issued.Bill.BillingPeriodStart + " to " + issued.Bill.BillingPeriodEnd,
		PayerAccountID:   issued.Bill.PayerAccountID,
		BillState:        issued.Bill.BillState,
		LineItemCount:    issued.Bill.LineItemCount,
		UsageCharge:      formatUSDMicros(issued.Bill.UsageChargeMicros),
		Credits:          formatUSDMicros(issued.Bill.CreditMicros),
		Refunds:          formatUSDMicros(issued.Bill.RefundMicros),
		Tax:              formatUSDMicros(issued.Bill.TaxMicros),
		Total:            formatUSDMicros(issued.Bill.TotalMicros),
		InvoiceID:        issued.Obligation.InvoiceID,
		InvoiceStatus:    issued.Obligation.Status,
		InvoiceAmountDue: formatUSDMicros(issued.Obligation.AmountDueMicros),
		InvoiceDate:      issued.Obligation.InvoiceDate,
		InvoiceDueDate:   issued.Obligation.DueDate,
	}
}

func (h resourceLabHandler) usageEventView(ctx context.Context, event persistence.UsageEvent, resourceName string) usageEventView {
	if resourceName == "" {
		resourceName = event.ResourceID
	}
	costEstimate := "unpriced"
	usageDate, billingPeriodDays, ok := usageEstimatePeriod(event.UsageStartTime)
	if ok {
		lookupResult, err := h.catalog.Lookup(ctx, persistence.PriceLookupRequest{
			ServiceCode:         event.ServiceCode,
			UsageType:           event.UsageType,
			Operation:           event.Operation,
			RegionCode:          event.RegionCode,
			UsageUnit:           event.UsageUnit,
			UsageQuantityMicros: event.UsageQuantityMicros,
			UsageDate:           usageDate,
			BillingPeriodDays:   billingPeriodDays,
		})
		if err == nil {
			costEstimate = formatUSDMicros(lookupResult.CostMicros)
		}
	}

	return usageEventView{
		ID:                 event.ID,
		ResourceID:         event.ResourceID,
		ResourceName:       resourceName,
		AccountID:          event.AccountID,
		ServiceCode:        event.ServiceCode,
		UsageType:          event.UsageType,
		Operation:          event.Operation,
		RegionCode:         event.RegionCode,
		Window:             event.UsageStartTime + " to " + event.UsageEndTime,
		Quantity:           formatQuantityMicros(event.UsageQuantityMicros),
		Unit:               event.UsageUnit,
		EstimatedCost:      costEstimate,
		BillableDimensions: billableDimensions(event.ServiceCode, event.UsageType, event.Operation, event.RegionCode),
		Tags:               keyValueViews(event.TagSnapshot),
	}
}

func catalogItemViewFromCatalog(item persistence.PriceCatalogItem, billingPeriodDays int) catalogItemView {
	periodEstimate := ""
	if strings.Contains(strings.ToLower(item.Unit), "hour") {
		periodEstimate = "24h " + formatUSDMicros(item.RateMicros*24)
	}
	if strings.EqualFold(item.Unit, "GBMonth") && billingPeriodDays > 0 {
		periodEstimate = "100 GB-day " + formatUSDMicros(divideAndRoundInt64(item.RateMicros*100, int64(billingPeriodDays)))
	}
	return catalogItemView{
		ServiceCode:        item.ServiceCode,
		UsageType:          item.UsageType,
		Operation:          item.Operation,
		RegionCode:         item.RegionCode,
		Unit:               item.Unit,
		UnitRate:           formatUSDMicros(item.RateMicros),
		PeriodEstimate:     periodEstimate,
		BillableDimensions: billableDimensions(item.ServiceCode, item.UsageType, item.Operation, item.RegionCode),
	}
}

func resourcePresets() []resourcePreset {
	return []resourcePreset{
		{
			Key:          "ec2_t3_medium",
			Label:        "Amazon EC2 / t3.medium",
			ServiceCode:  "AmazonEC2",
			ServiceName:  "Amazon EC2",
			ResourceType: "ec2_instance",
			DefaultSize:  "t3.medium",
			DefaultName:  "Storefront web",
			Attributes:   map[string]string{"instance_type": "t3.medium", "operating_system": "linux", "tenancy": "shared"},
		},
		{
			Key:          "s3_standard",
			Label:        "Amazon S3 / Standard bucket",
			ServiceCode:  "AmazonS3",
			ServiceName:  "Amazon S3",
			ResourceType: "s3_bucket",
			DefaultSize:  "standard",
			DefaultName:  "storefront-assets",
			Attributes:   map[string]string{"storage_class": "standard"},
		},
		{
			Key:          "rds_t3_medium",
			Label:        "Amazon RDS / db.t3.medium",
			ServiceCode:  "AmazonRDS",
			ServiceName:  "Amazon RDS",
			ResourceType: "rds_instance",
			DefaultSize:  "db.t3.medium",
			DefaultName:  "Orders database",
			Attributes:   map[string]string{"instance_class": "db.t3.medium", "engine": "postgres"},
		},
		{
			Key:          "nat_gateway",
			Label:        "NAT Gateway / shared gateway",
			ServiceCode:  "AmazonVPCNATGateway",
			ServiceName:  "NAT Gateway",
			ResourceType: "nat_gateway",
			DefaultSize:  "standard",
			DefaultName:  "Shared egress",
			Attributes:   map[string]string{"network_role": "egress"},
		},
		{
			Key:          "lambda_512",
			Label:        "AWS Lambda / 512 MB function",
			ServiceCode:  "AWSLambda",
			ServiceName:  "AWS Lambda",
			ResourceType: "lambda_function",
			DefaultSize:  "512 MB",
			DefaultName:  "Image processor",
			Attributes:   map[string]string{"memory_mb": "512", "runtime": "go"},
		},
		{
			Key:          "data_transfer_path",
			Label:        "AWS Data Transfer / internet path",
			ServiceCode:  "AWSDataTransfer",
			ServiceName:  "AWS Data Transfer",
			ResourceType: "data_transfer_path",
			DefaultSize:  "internet",
			DefaultName:  "Internet egress path",
			Attributes:   map[string]string{"path": "internet"},
		},
	}
}

func usagePresets() []usagePreset {
	return []usagePreset{
		{Key: "ec2_hours", Label: "EC2 instance hours", ServiceCode: "AmazonEC2", UsageType: "instance-hours:t3.medium", Operation: "RunInstances", Unit: "Hours"},
		{Key: "s3_storage", Label: "S3 storage GB-days", ServiceCode: "AmazonS3", UsageType: "storage:standard-gb-month", Operation: "StandardStorage", Unit: "GBDay"},
		{Key: "s3_put", Label: "S3 PUT requests", ServiceCode: "AmazonS3", UsageType: "requests:put-1k", Operation: "PutObject", Unit: "Request"},
		{Key: "s3_get", Label: "S3 GET requests", ServiceCode: "AmazonS3", UsageType: "requests:get-1k", Operation: "GetObject", Unit: "Request"},
		{Key: "lambda_requests", Label: "Lambda requests", ServiceCode: "AWSLambda", UsageType: "requests:lambda-1m", Operation: "Invoke", Unit: "Request"},
		{Key: "lambda_gb_seconds", Label: "Lambda GB-seconds", ServiceCode: "AWSLambda", UsageType: "compute:lambda-gb-second", Operation: "Invoke", Unit: "GBSecond"},
		{Key: "rds_hours", Label: "RDS instance hours", ServiceCode: "AmazonRDS", UsageType: "instance-hours:db.t3.medium", Operation: "CreateDBInstance", Unit: "Hours"},
		{Key: "rds_storage", Label: "RDS storage GB-days", ServiceCode: "AmazonRDS", UsageType: "storage:rds-gp3-gb-month", Operation: "DatabaseStorage", Unit: "GBDay"},
		{Key: "nat_hours", Label: "NAT Gateway hours", ServiceCode: "AmazonVPCNATGateway", UsageType: "nat-gateway-hours", Operation: "NatGateway", Unit: "Hours"},
		{Key: "nat_data", Label: "NAT Gateway GB processed", ServiceCode: "AmazonVPCNATGateway", UsageType: "nat-gateway-data-processed-gb", Operation: "NatGatewayDataProcessing", Unit: "GB"},
		{Key: "data_transfer_out", Label: "Internet data transfer GB", ServiceCode: "AWSDataTransfer", UsageType: "data-transfer-out-internet-gb", Operation: "DataTransferOut", RegionCode: "global", Unit: "GB"},
	}
}

func usageGenerationPresets() []usageGenerationPreset {
	options := persistence.UsageGenerationPatternOptions()
	presets := make([]usageGenerationPreset, 0, len(options))
	for _, option := range options {
		presets = append(presets, usageGenerationPreset{
			Key:   option.Key,
			Label: option.Label,
		})
	}
	return presets
}

func clockAdvanceUnitOptions() []clockAdvanceUnitView {
	return []clockAdvanceUnitView{
		{Key: persistence.SimulatorClockAdvanceHours, Label: "Hours"},
		{Key: persistence.SimulatorClockAdvanceDays, Label: "Days"},
		{Key: persistence.SimulatorClockAdvanceBillingPeriods, Label: "Billing Periods"},
	}
}

func defaultResourceFormDefaults() resourceFormDefaults {
	start, err := time.Parse(time.RFC3339, defaultUsageStartRFC3339)
	if err != nil {
		return resourceFormDefaults{
			UsageStartLocal:     defaultUsageStartLocal,
			UsageEndLocal:       defaultUsageEndLocal,
			UsageStartRFC3339:   defaultUsageStartRFC3339,
			UsageEndRFC3339:     defaultUsageEndRFC3339,
			GenerationStartDate: defaultGenerationStartDate,
		}
	}
	return resourceFormDefaultsForTime(start)
}

func resourceFormDefaultsForTime(value time.Time) resourceFormDefaults {
	start := value.UTC().Truncate(time.Minute)
	end := start.Add(time.Hour)
	return resourceFormDefaults{
		UsageStartLocal:     start.Format("2006-01-02T15:04"),
		UsageEndLocal:       end.Format("2006-01-02T15:04"),
		UsageStartRFC3339:   start.Format(time.RFC3339),
		UsageEndRFC3339:     end.Format(time.RFC3339),
		GenerationStartDate: start.Format(time.DateOnly),
	}
}

func applyClockToResourcePageData(data *resourcePageData, clock persistence.SimulatorClock) {
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf(
		"%s to %s (%d days)",
		clock.BillingPeriodStart,
		clock.BillingPeriodEnd,
		clock.BillingPeriodDays,
	)
	parsed, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return
	}
	defaults := resourceFormDefaultsForTime(parsed)
	data.DefaultUsageStart = defaults.UsageStartLocal
	data.DefaultUsageEnd = defaults.UsageEndLocal
	data.DefaultGenerationStartDate = defaults.GenerationStartDate
}

func resourcePresetByKey(key string) (resourcePreset, bool) {
	key = strings.TrimSpace(key)
	for _, preset := range resourcePresets() {
		if preset.Key == key {
			return preset, true
		}
	}
	return resourcePreset{}, false
}

func usagePresetByKey(key string) (usagePreset, bool) {
	key = strings.TrimSpace(key)
	for _, preset := range usagePresets() {
		if preset.Key == key {
			return preset, true
		}
	}
	return usagePreset{}, false
}

func parseTagsText(raw string) (map[string]string, error) {
	tags := map[string]string{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ','
	}) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			key, value, ok = strings.Cut(part, ":")
		}
		if !ok {
			return nil, fmt.Errorf("tag %q must use key=value", part)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("tag key is required")
		}
		if _, exists := tags[key]; exists {
			return nil, fmt.Errorf("duplicate tag key %q", key)
		}
		tags[key] = strings.TrimSpace(value)
	}
	return tags, nil
}

func parseFormTimestamp(value, defaultValue string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultValue
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339), nil
	}
	parsed, err := time.Parse("2006-01-02T15:04", value)
	if err != nil {
		return "", fmt.Errorf("time must use YYYY-MM-DDTHH:MM or RFC3339: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func parseQuantityMicros(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("usage quantity is required")
	}
	quantity, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("usage quantity must be numeric: %w", err)
	}
	if quantity <= 0 {
		return 0, fmt.Errorf("usage quantity must be greater than zero")
	}
	quantityMicros := math.Round(quantity * 1_000_000)
	if quantityMicros > float64(math.MaxInt64) {
		return 0, fmt.Errorf("usage quantity is too large")
	}
	return int64(quantityMicros), nil
}

func keyValueViews(values map[string]string) []keyValueView {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	views := make([]keyValueView, 0, len(keys))
	for _, key := range keys {
		views = append(views, keyValueView{Key: key, Value: values[key]})
	}
	return views
}

func copyStringMap(values map[string]string) map[string]string {
	copied := map[string]string{}
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func displayResourceName(resource persistence.Resource) string {
	if strings.TrimSpace(resource.ResourceName) != "" {
		return resource.ResourceName
	}
	return resource.ID
}

// usageEstimatePeriod returns the lookup date and calendar-month days used for UI-only price estimates.
func usageEstimatePeriod(value string) (string, int, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return "", 0, false
	}
	period, err := persistence.BillingPeriodForTime(parsed)
	if err != nil {
		return "", 0, false
	}
	return parsed.UTC().Format(time.DateOnly), period.Days, true
}

func billableDimensions(serviceCode, usageType, operation, regionCode string) string {
	return serviceCode + " / " + usageType + " / " + operation + " / " + regionCode
}

func formatQuantityMicros(value int64) string {
	if value%1_000_000 == 0 {
		return strconv.FormatInt(value/1_000_000, 10)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", float64(value)/1_000_000), "0"), ".")
}

func formatUSDMicros(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	whole := value / 1_000_000
	fraction := value % 1_000_000
	formatted := fmt.Sprintf("%s$%d.%06d", sign, whole, fraction)
	formatted = strings.TrimRight(formatted, "0")
	if strings.HasSuffix(formatted, ".") {
		formatted += "00"
	}
	return formatted
}

func divideAndRoundInt64(value, divisor int64) int64 {
	quotient := value / divisor
	remainder := value % divisor
	if remainder*2 >= divisor {
		return quotient + 1
	}
	return quotient
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusMethodNotAllowed)
	fmt.Fprintln(w, "method not allowed")
}

func flashFromQuery(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("flash"))
}

func urlQueryEscape(value string) string {
	return strings.ReplaceAll(template.URLQueryEscaper(value), "+", "%20")
}

var resourcePageTemplate = template.Must(template.New("resource-page").Parse(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>AWS Billing Simulator</title>
	<link rel="stylesheet" href="/assets/app.css">
</head>
<body>
	<header class="topbar">
		<div class="brand">AWS Billing Simulator</div>
		<nav aria-label="Primary">
			<a class="active" href="/resources">Resources</a>
			<span>Tags</span>
			<span>Cost Explorer</span>
			<span>Bills</span>
			<span>Scenarios</span>
		</nav>
	</header>

	<main class="page">
		<div class="page-heading">
			<div>
				<h1>Resources</h1>
			</div>
		</div>

		{{if .Flash}}<div class="notice success">{{.Flash}}</div>{{end}}
		{{if .Error}}<div class="notice error">{{.Error}}</div>{{end}}

		{{if not .WorkspaceReady}}
			<section class="empty">
				<h2>Workspace Required</h2>
				<p>No workspace is open.</p>
			</section>
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Simulator Clock</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
				<form method="post" action="/clock/advance" class="clock-form">
					<label>Amount
						<input name="clock_advance_amount" value="{{.DefaultClockAdvanceAmount}}" inputmode="numeric" required>
					</label>
					<label>Unit
						<select name="clock_advance_unit">
							{{range .ClockAdvanceUnits}}<option value="{{.Key}}">{{.Label}}</option>{{end}}
						</select>
					</label>
					<button type="submit">Advance Clock</button>
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
					<table>
						<thead>
							<tr>
								<th>Name</th>
								<th>Account</th>
								<th>Service</th>
								<th>Region</th>
								<th>Size</th>
								<th>Status</th>
								<th>Tags</th>
								<th>Usage</th>
							</tr>
						</thead>
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
								<tr><td colspan="8" class="empty-cell">No resources</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Resource</th>
								<th>Billable Dimensions</th>
								<th>Window</th>
								<th>Quantity</th>
								<th>Estimated Cost</th>
								<th>Tags Snapshot</th>
							</tr>
						</thead>
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
								<tr><td colspan="6" class="empty-cell">No usage events</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Period</th>
								<th>Payer</th>
								<th>Usage Account</th>
								<th>Service</th>
								<th>Status</th>
								<th>Items</th>
								<th>Cost</th>
								<th>Refreshed</th>
							</tr>
						</thead>
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
								<tr><td colspan="8" class="empty-cell">No billing summary</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Completed</th>
								<th>Trigger</th>
								<th>Clock</th>
								<th>Payer</th>
								<th>Metering</th>
								<th>Line Items</th>
								<th>Summaries</th>
							</tr>
						</thead>
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
								<tr><td colspan="7" class="empty-cell">No daily metering jobs</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Closed</th>
								<th>Period</th>
								<th>Payer</th>
								<th>Status</th>
								<th>Metering</th>
								<th>Line Items</th>
								<th>Final Cost</th>
								<th>Summaries</th>
							</tr>
						</thead>
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
								<tr><td colspan="8" class="empty-cell">No closed billing periods</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Bill</th>
								<th>Period</th>
								<th>Payer</th>
								<th>State</th>
								<th>Items</th>
								<th>Charges</th>
								<th>Tax</th>
								<th>Total</th>
								<th>Invoice</th>
								<th>Due</th>
							</tr>
						</thead>
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
								<tr><td colspan="10" class="empty-cell">No issued bills</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Resource</th>
								<th>Billable Dimensions</th>
								<th>Window</th>
								<th>Quantity</th>
								<th>Tags Snapshot</th>
							</tr>
						</thead>
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
								<tr><td colspan="5" class="empty-cell">No metering records</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Resource</th>
								<th>Period</th>
								<th>Status</th>
								<th>Accounts</th>
								<th>Service</th>
								<th>Description</th>
								<th>Usage</th>
								<th>Rate</th>
								<th>Cost</th>
								<th>Tags Snapshot</th>
							</tr>
						</thead>
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
								<tr><td colspan="10" class="empty-cell">No bill line items</td></tr>
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
					<table>
						<thead>
							<tr>
								<th>Service</th>
								<th>Billable Dimensions</th>
								<th>Unit</th>
								<th>Rate</th>
								<th>Estimate</th>
							</tr>
						</thead>
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
	</main>
</body>
</html>

{{define "tags"}}
	{{if .}}
		<div class="tags">
			{{range .}}<span>{{.Key}}={{.Value}}</span>{{end}}
		</div>
	{{else}}
		<span class="muted">untagged</span>
	{{end}}
{{end}}
`))

const resourceLabCSS = `
:root {
	color-scheme: light;
	--bg: #f7f8fa;
	--surface: #ffffff;
	--surface-soft: #eef6f5;
	--line: #d7dde2;
	--text: #172026;
	--muted: #66717b;
	--accent: #0f766e;
	--accent-strong: #0b5f59;
	--danger: #b42318;
	--success: #147d3f;
}

* {
	box-sizing: border-box;
}

body {
	margin: 0;
	background: var(--bg);
	color: var(--text);
	font: 14px/1.45 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}

.topbar {
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 24px;
	min-height: 56px;
	padding: 0 28px;
	background: var(--surface);
	border-bottom: 1px solid var(--line);
}

.brand {
	font-weight: 700;
}

nav {
	display: flex;
	align-items: center;
	gap: 8px;
	color: var(--muted);
	white-space: nowrap;
}

nav a,
nav span {
	padding: 6px 8px;
	border-radius: 6px;
	color: inherit;
	text-decoration: none;
}

nav .active {
	background: var(--surface-soft);
	color: var(--accent-strong);
}

.page {
	width: min(1380px, calc(100vw - 32px));
	margin: 0 auto;
	padding: 28px 0 48px;
}

.page-heading,
.section-heading {
	display: flex;
	align-items: flex-end;
	justify-content: space-between;
	gap: 20px;
	margin-bottom: 14px;
}

h1,
h2,
p {
	margin: 0;
}

h1 {
	font-size: 28px;
	line-height: 1.15;
}

h2 {
	font-size: 17px;
	line-height: 1.2;
}

p,
.section-heading span,
small,
.muted {
	color: var(--muted);
}

small {
	display: block;
	margin-top: 2px;
}

.notice {
	margin: 16px 0;
	padding: 10px 12px;
	border: 1px solid var(--line);
	border-radius: 6px;
	background: var(--surface);
}

.notice.success {
	border-color: #a7d7b6;
	color: var(--success);
}

.notice.error {
	border-color: #f0b4ac;
	color: var(--danger);
}

.form-grid {
	display: grid;
	grid-template-columns: minmax(360px, 1.3fr) repeat(3, minmax(240px, 1fr));
	gap: 16px;
	margin: 20px 0 30px;
	align-items: start;
}

.clock-strip {
	display: flex;
	align-items: end;
	justify-content: space-between;
	gap: 18px;
	margin: 20px 0 16px;
	padding: 16px;
	background: var(--surface);
	border: 1px solid var(--line);
	border-radius: 6px;
}

.clock-strip strong {
	display: block;
	margin-top: 8px;
	font-size: 18px;
}

.clock-form {
	display: grid;
	grid-template-columns: 110px 180px auto;
	gap: 12px;
	align-items: end;
	min-width: min(100%, 460px);
}

.panel,
.empty {
	background: var(--surface);
	border: 1px solid var(--line);
	border-radius: 6px;
	padding: 16px;
}

.panel h2 {
	margin-bottom: 14px;
}

.fields {
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	gap: 12px;
}

.compact .fields {
	grid-template-columns: 1fr;
}

label {
	display: grid;
	gap: 5px;
	color: var(--muted);
	font-size: 12px;
	font-weight: 650;
	text-transform: uppercase;
}

.wide {
	grid-column: 1 / -1;
}

input,
select,
textarea {
	width: 100%;
	min-height: 36px;
	border: 1px solid var(--line);
	border-radius: 6px;
	background: #ffffff;
	color: var(--text);
	padding: 7px 9px;
	font: inherit;
}

textarea {
	resize: vertical;
}

button {
	margin-top: 14px;
	min-height: 36px;
	border: 0;
	border-radius: 6px;
	background: var(--accent);
	color: #ffffff;
	padding: 8px 12px;
	font: inherit;
	font-weight: 700;
	cursor: pointer;
}

button:hover {
	background: var(--accent-strong);
}

section {
	margin-top: 26px;
}

.table-wrap {
	overflow-x: auto;
	background: var(--surface);
	border: 1px solid var(--line);
	border-radius: 6px;
}

table {
	width: 100%;
	border-collapse: collapse;
	min-width: 920px;
}

th,
td {
	padding: 10px 12px;
	border-bottom: 1px solid var(--line);
	text-align: left;
	vertical-align: top;
}

th {
	background: #f1f4f6;
	color: #42515d;
	font-size: 12px;
	text-transform: uppercase;
}

tr:last-child td {
	border-bottom: 0;
}

code {
	font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
	font-size: 12px;
	white-space: normal;
}

.tags {
	display: flex;
	flex-wrap: wrap;
	gap: 4px;
}

.tags span,
.status {
	display: inline-flex;
	align-items: center;
	min-height: 24px;
	border-radius: 6px;
	background: var(--surface-soft);
	color: var(--accent-strong);
	padding: 2px 7px;
	font-size: 12px;
	font-weight: 650;
}

.empty-cell {
	color: var(--muted);
	text-align: center;
}

@media (max-width: 980px) {
	.topbar,
	.clock-strip {
		align-items: flex-start;
		flex-direction: column;
	}

	.topbar {
		padding: 14px 18px;
	}

	nav {
		flex-wrap: wrap;
		white-space: normal;
	}

	.form-grid,
	.fields,
	.clock-form {
		grid-template-columns: 1fr;
	}

	.page-heading,
	.section-heading {
		align-items: flex-start;
		flex-direction: column;
		gap: 6px;
	}
}
`
