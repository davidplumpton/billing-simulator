package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type costExplorerHandler struct {
	db           *sql.DB
	explorer     persistence.CostExplorerRepository
	savedReports persistence.SavedReportRepository
	categories   persistence.CostCategoryRepository
	clock        persistence.SimulatorClockRepository
}

type costExplorerPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Builder             costExplorerBuilderView
	Result              costExplorerResultView
	SavedReports        []costExplorerSavedReportView
	HasResult           bool
	Group1TypeOptions   []uiSelectOptionView
	Group2TypeOptions   []uiSelectOptionView
	MetricOptions       []uiSelectOptionView
	GranularityOptions  []uiSelectOptionView
	ChartTypeOptions    []uiSelectOptionView
	OwnerRoleOptions    []uiSelectOptionView
	GroupKeyOptions     []string
	Tables              costExplorerTablesView
}

type costExplorerBuilderView struct {
	SavedReportID       string
	ReportName          string
	Description         string
	OwnerAccountID      string
	OwnerRole           string
	DateRangeStart      string
	DateRangeEnd        string
	Granularity         string
	Metric              string
	ChartType           string
	ServiceValues       string
	LinkedAccountValues string
	RegionValues        string
	UsageTypeValues     string
	LineItemTypeValues  string
	TagKey              string
	TagValues           string
	CostCategoryKey     string
	CostCategoryValues  string
	Group1Type          string
	Group1Key           string
	Group2Type          string
	Group2Key           string
	HasFilters          bool
}

type costExplorerResultView struct {
	DateRangeStart string
	DateRangeEnd   string
	Granularity    string
	Metric         string
	MetricLabel    string
	ChartType      string
	CSVPath        string
	Chart          costExplorerChartView
	Rows           []costExplorerResultRowView
	StateCards     []costExplorerStateCardView
}

type costExplorerChartView struct {
	Title       string
	Type        string
	MetricLabel string
	HasRows     bool
	HasChart    bool
	Width       int
	Height      int
	PlotX       int
	PlotY       int
	PlotWidth   int
	PlotHeight  int
	YAxisLabel  string
	ZeroY       int
	Ticks       []costExplorerChartTickView
	XLabels     []costExplorerChartAxisLabelView
	Lines       []costExplorerChartLineView
	Bars        []costExplorerChartBarView
	Legend      []costExplorerChartLegendView
}

type costExplorerChartTickView struct {
	Y     int
	Label string
}

type costExplorerChartAxisLabelView struct {
	X     int
	Label string
}

type costExplorerChartLineView struct {
	Label  string
	Color  string
	Points string
	Nodes  []costExplorerChartPointView
}

type costExplorerChartPointView struct {
	X          int
	Y          int
	Period     string
	Label      string
	ValueLabel string
}

type costExplorerChartBarView struct {
	X          int
	Y          int
	Width      int
	Height     int
	Color      string
	Period     string
	Label      string
	ValueLabel string
}

type costExplorerChartLegendView struct {
	Label string
	Color string
}

type costExplorerStateCardView struct {
	Label string
	Value string
}

type costExplorerResultRowView struct {
	PeriodStart   string
	PeriodEnd     string
	Group1        string
	Group2        string
	DrilldownPath string
	MetricValue   string
	Usage         string
	Cost          string
	LineItems     int
	CurrencyCode  string
}

type costExplorerLineItemsPageData struct {
	WorkspaceReady      bool
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	BackPath            string
	CSVPath             string
	Period              string
	Groups              []string
	StateCards          []costExplorerStateCardView
	LineItems           []costExplorerLineItemView
	Tables              costExplorerLineItemsTablesView
}

type costExplorerLineItemView struct {
	ID             string
	Resource       string
	ResourceID     string
	Period         string
	Status         string
	PayerAccountID string
	UsageAccountID string
	Service        string
	ServiceCode    string
	LineItemType   string
	RegionCode     string
	UsageType      string
	Operation      string
	Window         string
	Quantity       string
	Rate           string
	Cost           string
	CurrencyCode   string
	Description    string
	Tags           []keyValueView
}

type costExplorerLineItemsTablesView struct {
	LineItems uiTableView
}

type costExplorerSavedReportView struct {
	ID             string
	Name           string
	Description    string
	OwnerAccountID string
	OwnerRole      string
	Owner          string
	DateRange      string
	Granularity    string
	Metric         string
	ChartType      string
	LastRun        string
	LoadPath       string
	Selected       bool
}

type costExplorerTablesView struct {
	Results      uiTableView
	SavedReports uiTableView
}

// costExplorerRequestError marks user-supplied report-builder input errors.
type costExplorerRequestError struct {
	err error
}

func (e costExplorerRequestError) Error() string {
	return e.err.Error()
}

func (e costExplorerRequestError) Unwrap() error {
	return e.err
}

// newCostExplorerHandler builds the repositories needed for report-builder workflows.
func newCostExplorerHandler(db *sql.DB) costExplorerHandler {
	return costExplorerHandler{
		db:           db,
		explorer:     persistence.NewCostExplorerRepository(db),
		savedReports: persistence.NewSavedReportRepository(db),
		categories:   persistence.NewCostCategoryRepository(db),
		clock:        persistence.NewSimulatorClockRepository(db),
	}
}

// handleCostExplorer renders the report builder and runs the current query definition.
func (h costExplorerHandler) handleCostExplorer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderCostExplorer(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleCostExplorerResultsCSV exports the current aggregate report rows as CSV.
func (h costExplorerHandler) handleCostExplorerResultsCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	builder, err := h.builderFromRequest(r.Context(), r)
	if err != nil {
		http.Error(w, "export Cost Explorer CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	result, err := h.queryFromBuilder(r.Context(), builder)
	if err != nil {
		http.Error(w, "export Cost Explorer CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	body, err := costExplorerResultsCSVBytes(result, builder)
	if err != nil {
		http.Error(w, "export Cost Explorer CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+costExplorerResultsCSVFilename(builder)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleCostExplorerLineItems renders the source bill line items for one aggregate row.
func (h costExplorerHandler) handleCostExplorerLineItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderCostExplorerLineItems(w, r, http.StatusOK, "")
}

// handleSaveCostExplorerReport creates or updates a saved report from the builder fields.
func (h costExplorerHandler) handleSaveCostExplorerReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderCostExplorer(w, r, http.StatusServiceUnavailable, "Open a workspace before saving Cost Explorer reports.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, "parse Cost Explorer report form: "+err.Error(), "")
		return
	}
	builder, err := costExplorerBuilderFromValues(r.PostForm, costExplorerDefaultBuilder())
	if err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	filters, err := costExplorerFiltersFromBuilder(builder)
	if err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	groupings, err := costExplorerGroupingsFromBuilder(builder)
	if err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}

	if builder.SavedReportID != "" {
		ownerScope := costExplorerSavedReportOwnerScopeFromBuilder(builder)
		if _, err := h.savedReports.GetForOwner(r.Context(), builder.SavedReportID, ownerScope.OwnerAccountID, ownerScope.OwnerRole); err != nil {
			h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
			return
		}
		report, err := h.savedReports.Update(r.Context(), persistence.SavedReportUpdateRequest{
			ID:             builder.SavedReportID,
			Name:           builder.ReportName,
			Description:    builder.Description,
			OwnerAccountID: builder.OwnerAccountID,
			OwnerRole:      builder.OwnerRole,
			DateRangeStart: builder.DateRangeStart,
			DateRangeEnd:   builder.DateRangeEnd,
			Granularity:    builder.Granularity,
			Filters:        filters,
			Groupings:      groupings,
			Metrics:        []string{builder.Metric},
			ChartType:      builder.ChartType,
		})
		if err != nil {
			h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
			return
		}
		http.Redirect(w, r, costExplorerSavedReportPath(report, "Updated saved report "+report.Name), http.StatusSeeOther)
		return
	}

	report, err := h.savedReports.Create(r.Context(), persistence.SavedReportCreateRequest{
		Name:           builder.ReportName,
		Description:    builder.Description,
		OwnerAccountID: builder.OwnerAccountID,
		OwnerRole:      builder.OwnerRole,
		DateRangeStart: builder.DateRangeStart,
		DateRangeEnd:   builder.DateRangeEnd,
		Granularity:    builder.Granularity,
		Filters:        filters,
		Groupings:      groupings,
		Metrics:        []string{builder.Metric},
		ChartType:      builder.ChartType,
	})
	if err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, costExplorerSavedReportPath(report, "Saved report "+report.Name), http.StatusSeeOther)
}

// handleRunCostExplorerReport executes a persisted saved report and records its latest run metadata.
func (h costExplorerHandler) handleRunCostExplorerReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderCostExplorer(w, r, http.StatusServiceUnavailable, "Open a workspace before running saved Cost Explorer reports.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, "parse Cost Explorer saved report run form: "+err.Error(), "")
		return
	}
	savedReportID := strings.TrimSpace(r.PostForm.Get("saved_report_id"))
	if savedReportID == "" {
		h.renderCostExplorer(w, r, http.StatusBadRequest, "saved report ID is required", "")
		return
	}
	ownerScope, err := costExplorerSavedReportOwnerScopeFromValues(r.PostForm, costExplorerDefaultBuilder())
	if err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}

	ctx := r.Context()
	clock, err := h.clock.Get(ctx)
	if err != nil {
		h.renderCostExplorer(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	report, err := h.savedReports.GetForOwner(ctx, savedReportID, ownerScope.OwnerAccountID, ownerScope.OwnerRole)
	if err != nil {
		h.renderCostExplorer(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	builder := costExplorerBuilderFromSavedReport(report, costExplorerDefaultBuilder())
	result, err := h.queryFromBuilder(ctx, builder)
	if err != nil {
		if _, recordErr := h.savedReports.RecordLastRun(ctx, persistence.SavedReportRunUpdate{
			ID:           report.ID,
			RunAt:        clock.CurrentTime,
			Status:       "failed",
			ErrorMessage: err.Error(),
		}); recordErr != nil {
			h.renderCostExplorer(w, r, http.StatusInternalServerError, recordErr.Error(), "")
			return
		}
		http.Redirect(w, r, costExplorerSavedReportPath(report, "Saved report "+report.Name+" failed"), http.StatusSeeOther)
		return
	}
	if _, err := h.savedReports.RecordLastRun(ctx, persistence.SavedReportRunUpdate{
		ID:                       report.ID,
		RunAt:                    clock.CurrentTime,
		Status:                   "succeeded",
		RowCount:                 len(result.Rows),
		TotalUnblendedCostMicros: result.TotalUnblendedCostMicros,
	}); err != nil {
		h.renderCostExplorer(w, r, http.StatusInternalServerError, err.Error(), "")
		return
	}
	http.Redirect(w, r, costExplorerSavedReportPath(report, "Ran saved report "+report.Name), http.StatusSeeOther)
}

// renderCostExplorerLineItems builds the drilldown page for one Cost Explorer result row.
func (h costExplorerHandler) renderCostExplorerLineItems(w http.ResponseWriter, r *http.Request, status int, errorMessage string) {
	data := costExplorerLineItemsPageData{
		WorkspaceReady:      h.db != nil,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		BackPath:            "/cost-explorer",
		Tables:              costExplorerLineItemsTables(),
		StateCards:          costExplorerErrorStateCards(),
	}
	if h.db != nil && errorMessage == "" {
		if err := h.loadCostExplorerLineItemsPageData(r.Context(), r, &data); err != nil {
			status = http.StatusBadRequest
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices("", data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Cost Explorer Bill Line Items - AWS Billing Simulator",
		ActiveNav: "cost-explorer",
	}, costExplorerLineItemsPageTemplate, data, "render Cost Explorer bill line items page")
}

// renderCostExplorer builds the Cost Explorer report-builder page from the open workspace.
func (h costExplorerHandler) renderCostExplorer(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := costExplorerPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Builder:             costExplorerDefaultBuilder(),
		Group1TypeOptions:   costExplorerGroupTypeOptions("dimension"),
		Group2TypeOptions:   costExplorerGroupTypeOptions(""),
		MetricOptions:       costExplorerMetricOptions(""),
		GranularityOptions:  costExplorerGranularityOptions(""),
		ChartTypeOptions:    costExplorerChartTypeOptions(""),
		OwnerRoleOptions:    costExplorerOwnerRoleOptions(""),
		GroupKeyOptions:     costExplorerBaseGroupKeyOptions(),
		Tables:              costExplorerTables(),
	}
	if h.db != nil {
		if err := h.loadCostExplorerPageData(r.Context(), r, &data); err != nil {
			status = http.StatusInternalServerError
			var requestErr costExplorerRequestError
			if errors.As(err, &requestErr) {
				status = http.StatusBadRequest
			}
			data.Error = err.Error()
		}
	}
	if errorMessage != "" {
		data.Error = errorMessage
	}
	data.Notices = uiNotices(data.Flash, data.Error)

	if wantsPageFragment(r, "cost-explorer") {
		renderPageFragment(w, status, costExplorerPageTemplate, "cost-explorer.refresh", data, "render Cost Explorer fragment")
		return
	}
	renderPage(w, status, pageLayoutOptions{
		Title:     "Cost Explorer - AWS Billing Simulator",
		ActiveNav: "cost-explorer",
	}, costExplorerPageTemplate, data, "render Cost Explorer page")
}

// loadCostExplorerLineItemsPageData reads source bill line items for a linked report row.
func (h costExplorerHandler) loadCostExplorerLineItemsPageData(ctx context.Context, r *http.Request, data *costExplorerLineItemsPageData) error {
	builder, err := h.builderFromRequest(ctx, r)
	if err != nil {
		return err
	}
	queryRequest, err := costExplorerQueryRequestFromBuilder(builder)
	if err != nil {
		return err
	}
	periodStart := firstValue(r.URL.Query(), "period_start")
	periodEnd := firstValue(r.URL.Query(), "period_end")
	if periodStart == "" || periodEnd == "" {
		return fmt.Errorf("Cost Explorer drilldown period is required")
	}
	groupValues, err := costExplorerDrilldownGroupValuesFromValues(r.URL.Query(), queryRequest.Groupings)
	if err != nil {
		return err
	}
	items, err := h.explorer.ListLineItems(ctx, persistence.CostExplorerLineItemRequest{
		Query:           queryRequest,
		TimePeriodStart: periodStart,
		TimePeriodEnd:   periodEnd,
		GroupValues:     groupValues,
		Limit:           100,
	})
	if err != nil {
		return err
	}

	data.BackPath = costExplorerPath(builder)
	data.CSVPath = costExplorerResultsCSVPath(builder)
	data.Period = periodStart + " to " + periodEnd
	for _, group := range groupValues {
		data.Groups = append(data.Groups, costExplorerGroupLabel(group))
	}
	var usageMicros, costMicros int64
	for _, item := range items {
		usageMicros += item.UsageQuantityMicros
		costMicros += item.UnblendedCostMicros
		data.LineItems = append(data.LineItems, costExplorerLineItemViewFromItem(item))
	}
	data.StateCards = []costExplorerStateCardView{
		{Label: "Line Items", Value: fmt.Sprintf("%d", len(items))},
		{Label: "Unblended Cost", Value: formatUSDMicros(costMicros)},
		{Label: "Usage Quantity", Value: formatQuantityMicros(usageMicros)},
		{Label: "Period", Value: periodStart},
	}
	return nil
}

// loadCostExplorerPageData reads saved reports, category choices, and the current query result.
func (h costExplorerHandler) loadCostExplorerPageData(ctx context.Context, r *http.Request, data *costExplorerPageData) error {
	defaults, err := h.defaultBuilder(ctx)
	if err != nil {
		return err
	}
	ownerScope, err := costExplorerSavedReportOwnerScopeFromValues(r.URL.Query(), defaults)
	if err != nil {
		return err
	}
	savedReports, err := h.savedReports.List(ctx, ownerScope.listRequest(100))
	if err != nil {
		return err
	}
	selectedReport, hasSelectedReport, err := h.selectedSavedReport(ctx, r, ownerScope, savedReports)
	if err != nil {
		return err
	}
	if hasSelectedReport && !costExplorerRequestHasBuilderFields(r) {
		data.Builder = costExplorerBuilderFromSavedReport(selectedReport, defaults)
	} else {
		builder, err := costExplorerBuilderFromValues(r.URL.Query(), defaults)
		if err != nil {
			return err
		}
		data.Builder = builder
	}
	data.Builder.HasFilters = costExplorerBuilderHasFilters(data.Builder)
	data.Group1TypeOptions = costExplorerGroupTypeOptions(data.Builder.Group1Type)
	data.Group2TypeOptions = costExplorerGroupTypeOptions(data.Builder.Group2Type)
	data.MetricOptions = costExplorerMetricOptions(data.Builder.Metric)
	data.GranularityOptions = costExplorerGranularityOptions(data.Builder.Granularity)
	data.ChartTypeOptions = costExplorerChartTypeOptions(data.Builder.ChartType)
	data.OwnerRoleOptions = costExplorerOwnerRoleOptions(data.Builder.OwnerRole)

	categoryOptions, err := h.costCategoryGroupKeys(ctx)
	if err != nil {
		return err
	}
	data.GroupKeyOptions = append(data.GroupKeyOptions, categoryOptions...)
	for _, report := range savedReports {
		data.SavedReports = append(data.SavedReports, costExplorerSavedReportViewFromReport(report, data.Builder.SavedReportID))
	}
	if data.Builder.SavedReportID != "" && data.Builder.ReportName == "" && hasSelectedReport {
		data.Builder.ReportName = selectedReport.Name
		data.Builder.Description = selectedReport.Description
	}

	result, err := h.queryFromBuilder(ctx, data.Builder)
	if err != nil {
		data.HasResult = false
		data.Result = costExplorerResultView{
			DateRangeStart: data.Builder.DateRangeStart,
			DateRangeEnd:   data.Builder.DateRangeEnd,
			Granularity:    data.Builder.Granularity,
			Metric:         data.Builder.Metric,
			MetricLabel:    costExplorerMetricLabel(data.Builder.Metric),
			ChartType:      data.Builder.ChartType,
			StateCards:     costExplorerErrorStateCards(),
		}
		if data.Error == "" {
			data.Error = err.Error()
		}
		return nil
	}
	data.Result = costExplorerResultViewFromResult(result, data.Builder)
	data.HasResult = true
	return nil
}

// defaultBuilder derives the current billing window defaults from the simulator clock.
func (h costExplorerHandler) defaultBuilder(ctx context.Context) (costExplorerBuilderView, error) {
	builder := costExplorerDefaultBuilder()
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return costExplorerBuilderView{}, err
	}
	builder.DateRangeStart = clock.BillingPeriodStart
	builder.DateRangeEnd = clock.BillingPeriodEnd
	return builder, nil
}

// selectedSavedReport loads the selected saved report from the already listed reports.
func (h costExplorerHandler) selectedSavedReport(ctx context.Context, r *http.Request, ownerScope costExplorerSavedReportOwnerScope, reports []persistence.SavedReport) (persistence.SavedReport, bool, error) {
	selectedID := strings.TrimSpace(r.URL.Query().Get("saved_report_id"))
	if selectedID == "" {
		return persistence.SavedReport{}, false, nil
	}
	for _, report := range reports {
		if report.ID == selectedID {
			return report, true, nil
		}
	}
	report, err := h.savedReports.GetForOwner(ctx, selectedID, ownerScope.OwnerAccountID, ownerScope.OwnerRole)
	if err != nil {
		return persistence.SavedReport{}, false, costExplorerRequestError{err: err}
	}
	return report, true, nil
}

// builderFromRequest resolves explicit builder fields or an unloaded saved report ID.
func (h costExplorerHandler) builderFromRequest(ctx context.Context, r *http.Request) (costExplorerBuilderView, error) {
	defaults, err := h.defaultBuilder(ctx)
	if err != nil {
		return costExplorerBuilderView{}, err
	}
	selectedID := strings.TrimSpace(r.URL.Query().Get("saved_report_id"))
	if selectedID != "" && !costExplorerRequestHasBuilderFields(r) {
		ownerScope, err := costExplorerSavedReportOwnerScopeFromValues(r.URL.Query(), defaults)
		if err != nil {
			return costExplorerBuilderView{}, err
		}
		report, err := h.savedReports.GetForOwner(ctx, selectedID, ownerScope.OwnerAccountID, ownerScope.OwnerRole)
		if err != nil {
			return costExplorerBuilderView{}, err
		}
		return costExplorerBuilderFromSavedReport(report, defaults), nil
	}
	return costExplorerBuilderFromValues(r.URL.Query(), defaults)
}

// costCategoryGroupKeys returns Cost Category names that can be typed into grouping/filter keys.
func (h costExplorerHandler) costCategoryGroupKeys(ctx context.Context) ([]string, error) {
	categories, err := h.categories.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(categories))
	for _, category := range categories {
		keys = append(keys, category.Name)
	}
	return keys, nil
}

// queryFromBuilder converts the form model to the repository query request.
func (h costExplorerHandler) queryFromBuilder(ctx context.Context, builder costExplorerBuilderView) (persistence.CostExplorerQueryResult, error) {
	request, err := costExplorerQueryRequestFromBuilder(builder)
	if err != nil {
		return persistence.CostExplorerQueryResult{}, err
	}
	return h.explorer.Query(ctx, request)
}

func costExplorerQueryRequestFromBuilder(builder costExplorerBuilderView) (persistence.CostExplorerQueryRequest, error) {
	filters, err := costExplorerFiltersFromBuilder(builder)
	if err != nil {
		return persistence.CostExplorerQueryRequest{}, err
	}
	groupings, err := costExplorerGroupingsFromBuilder(builder)
	if err != nil {
		return persistence.CostExplorerQueryRequest{}, err
	}
	return persistence.CostExplorerQueryRequest{
		DateRangeStart: builder.DateRangeStart,
		DateRangeEnd:   builder.DateRangeEnd,
		Granularity:    builder.Granularity,
		Filters:        filters,
		Groupings:      groupings,
	}, nil
}

func costExplorerDefaultBuilder() costExplorerBuilderView {
	return costExplorerBuilderView{
		OwnerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "monthly",
		Metric:         "unblended_cost",
		ChartType:      "table",
		Group1Type:     "dimension",
		Group1Key:      "service",
	}
}

type costExplorerSavedReportOwnerScope struct {
	OwnerAccountID string
	OwnerRole      string
}

// costExplorerSavedReportOwnerScopeFromValues derives the simulated saved-report shelf from request values.
func costExplorerSavedReportOwnerScopeFromValues(values url.Values, defaults costExplorerBuilderView) (costExplorerSavedReportOwnerScope, error) {
	builder, err := costExplorerBuilderFromValues(values, defaults)
	if err != nil {
		return costExplorerSavedReportOwnerScope{}, err
	}
	return costExplorerSavedReportOwnerScopeFromBuilder(builder), nil
}

// costExplorerSavedReportOwnerScopeFromBuilder extracts the owner shelf from a normalized builder.
func costExplorerSavedReportOwnerScopeFromBuilder(builder costExplorerBuilderView) costExplorerSavedReportOwnerScope {
	return costExplorerSavedReportOwnerScope{
		OwnerAccountID: strings.TrimSpace(builder.OwnerAccountID),
		OwnerRole:      strings.TrimSpace(builder.OwnerRole),
	}
}

// listRequest converts the UI owner shelf into the persistence list filter.
func (s costExplorerSavedReportOwnerScope) listRequest(limit int) persistence.SavedReportListRequest {
	return persistence.SavedReportListRequest{
		OwnerAccountID: s.OwnerAccountID,
		OwnerRole:      s.OwnerRole,
		Limit:          limit,
	}
}

func costExplorerBuilderFromValues(values url.Values, defaults costExplorerBuilderView) (costExplorerBuilderView, error) {
	builder := defaults
	builder.SavedReportID = firstValue(values, "saved_report_id")
	builder.ReportName = firstValue(values, "report_name")
	builder.Description = firstValue(values, "description")
	builder.OwnerAccountID = defaultString(firstValue(values, "owner_account_id"), builder.OwnerAccountID)
	builder.OwnerRole = defaultString(firstValue(values, "owner_role"), builder.OwnerRole)
	builder.DateRangeStart = defaultString(firstValue(values, "date_range_start"), builder.DateRangeStart)
	builder.DateRangeEnd = defaultString(firstValue(values, "date_range_end"), builder.DateRangeEnd)
	builder.Granularity = defaultString(firstValue(values, "granularity"), builder.Granularity)
	builder.Metric = defaultString(firstValue(values, "metric"), builder.Metric)
	builder.ChartType = defaultString(firstValue(values, "chart_type"), builder.ChartType)
	builder.ServiceValues = firstValue(values, "service_values")
	builder.LinkedAccountValues = firstValue(values, "linked_account_values")
	builder.RegionValues = firstValue(values, "region_values")
	builder.UsageTypeValues = firstValue(values, "usage_type_values")
	builder.LineItemTypeValues = firstValue(values, "line_item_type_values")
	builder.TagKey = firstValue(values, "tag_key")
	builder.TagValues = firstValue(values, "tag_values")
	builder.CostCategoryKey = firstValue(values, "cost_category_key")
	builder.CostCategoryValues = firstValue(values, "cost_category_values")
	builder.Group1Type = defaultString(firstValue(values, "group_1_type"), builder.Group1Type)
	builder.Group1Key = defaultString(firstValue(values, "group_1_key"), builder.Group1Key)
	builder.Group2Type = firstValue(values, "group_2_type")
	builder.Group2Key = firstValue(values, "group_2_key")
	if builder.Metric == "" {
		builder.Metric = "unblended_cost"
	}
	if builder.ChartType == "" {
		builder.ChartType = "table"
	}
	return builder, nil
}

func costExplorerBuilderFromSavedReport(report persistence.SavedReport, defaults costExplorerBuilderView) costExplorerBuilderView {
	builder := defaults
	builder.SavedReportID = report.ID
	builder.ReportName = report.Name
	builder.Description = report.Description
	builder.OwnerAccountID = report.OwnerAccountID
	builder.OwnerRole = report.OwnerRole
	builder.DateRangeStart = report.DateRangeStart
	builder.DateRangeEnd = report.DateRangeEnd
	builder.Granularity = report.Granularity
	if len(report.Metrics) > 0 {
		builder.Metric = report.Metrics[0]
	}
	builder.ChartType = report.ChartType
	builder.ServiceValues = strings.Join(report.Filters["service"], ", ")
	builder.LinkedAccountValues = strings.Join(report.Filters["linked_account"], ", ")
	builder.RegionValues = strings.Join(report.Filters["region"], ", ")
	builder.UsageTypeValues = strings.Join(report.Filters["usage_type"], ", ")
	builder.LineItemTypeValues = strings.Join(report.Filters["line_item_type"], ", ")
	for key, values := range report.Filters {
		if strings.HasPrefix(key, "tag:") {
			builder.TagKey = strings.TrimPrefix(key, "tag:")
			builder.TagValues = strings.Join(values, ", ")
		}
		if strings.HasPrefix(key, "cost_category:") {
			builder.CostCategoryKey = strings.TrimPrefix(key, "cost_category:")
			builder.CostCategoryValues = strings.Join(values, ", ")
		}
	}
	if len(report.Groupings) > 0 {
		builder.Group1Type = report.Groupings[0].Type
		builder.Group1Key = report.Groupings[0].Key
	}
	if len(report.Groupings) > 1 {
		builder.Group2Type = report.Groupings[1].Type
		builder.Group2Key = report.Groupings[1].Key
	}
	return builder
}

func costExplorerRequestHasBuilderFields(r *http.Request) bool {
	query := r.URL.Query()
	for _, key := range []string{
		"report_name",
		"date_range_start",
		"date_range_end",
		"granularity",
		"metric",
		"service_values",
		"linked_account_values",
		"region_values",
		"usage_type_values",
		"line_item_type_values",
		"tag_key",
		"tag_values",
		"cost_category_key",
		"cost_category_values",
		"group_1_type",
		"group_1_key",
		"group_2_type",
		"group_2_key",
	} {
		if _, ok := query[key]; ok {
			return true
		}
	}
	return false
}

func costExplorerBuilderHasFilters(builder costExplorerBuilderView) bool {
	return builder.ServiceValues != "" ||
		builder.LinkedAccountValues != "" ||
		builder.RegionValues != "" ||
		builder.UsageTypeValues != "" ||
		builder.LineItemTypeValues != "" ||
		builder.TagKey != "" ||
		builder.TagValues != "" ||
		builder.CostCategoryKey != "" ||
		builder.CostCategoryValues != ""
}

func costExplorerFiltersFromBuilder(builder costExplorerBuilderView) (map[string][]string, error) {
	filters := map[string][]string{}
	addFilterValues(filters, "service", builder.ServiceValues)
	addFilterValues(filters, "linked_account", builder.LinkedAccountValues)
	addFilterValues(filters, "region", builder.RegionValues)
	addFilterValues(filters, "usage_type", builder.UsageTypeValues)
	addFilterValues(filters, "line_item_type", builder.LineItemTypeValues)
	tagValues := splitRuleValues(builder.TagValues)
	if builder.TagKey != "" || len(tagValues) > 0 {
		if builder.TagKey == "" {
			return nil, fmt.Errorf("tag filter key is required when tag values are set")
		}
		if len(tagValues) == 0 {
			return nil, fmt.Errorf("tag filter values are required when tag key is set")
		}
		filters["tag:"+builder.TagKey] = tagValues
	}
	categoryValues := splitRuleValues(builder.CostCategoryValues)
	if builder.CostCategoryKey != "" || len(categoryValues) > 0 {
		if builder.CostCategoryKey == "" {
			return nil, fmt.Errorf("Cost Category filter key is required when values are set")
		}
		if len(categoryValues) == 0 {
			return nil, fmt.Errorf("Cost Category filter values are required when key is set")
		}
		filters["cost_category:"+builder.CostCategoryKey] = categoryValues
	}
	return filters, nil
}

func costExplorerGroupingsFromBuilder(builder costExplorerBuilderView) ([]persistence.CostExplorerGrouping, error) {
	groupings := []persistence.CostExplorerGrouping{}
	for idx, input := range []struct {
		groupType string
		key       string
	}{
		{builder.Group1Type, builder.Group1Key},
		{builder.Group2Type, builder.Group2Key},
	} {
		groupType := strings.TrimSpace(input.groupType)
		key := strings.TrimSpace(input.key)
		if groupType == "" {
			continue
		}
		if key == "" {
			return nil, fmt.Errorf("group %d key is required", idx+1)
		}
		groupings = append(groupings, persistence.CostExplorerGrouping{Type: groupType, Key: key})
	}
	return groupings, nil
}

func costExplorerDrilldownGroupValuesFromValues(values url.Values, groupings []persistence.CostExplorerGrouping) ([]persistence.CostExplorerGroupValue, error) {
	groupValues := make([]persistence.CostExplorerGroupValue, 0, len(groupings))
	for i, grouping := range groupings {
		value := firstValue(values, fmt.Sprintf("group_%d_value", i+1))
		if value == "" {
			return nil, fmt.Errorf("Cost Explorer drilldown group %d value is required", i+1)
		}
		groupValues = append(groupValues, persistence.CostExplorerGroupValue{
			Type:  grouping.Type,
			Key:   grouping.Key,
			Value: value,
		})
	}
	return groupValues, nil
}

func addFilterValues(filters map[string][]string, key, raw string) {
	values := splitRuleValues(raw)
	if len(values) > 0 {
		filters[key] = values
	}
}

func costExplorerBuilderQueryValues(builder costExplorerBuilderView) url.Values {
	values := url.Values{}
	setQueryValue := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			values.Set(key, value)
		}
	}
	setQueryValue("saved_report_id", builder.SavedReportID)
	setQueryValue("report_name", builder.ReportName)
	setQueryValue("description", builder.Description)
	setQueryValue("owner_account_id", builder.OwnerAccountID)
	setQueryValue("owner_role", builder.OwnerRole)
	setQueryValue("date_range_start", builder.DateRangeStart)
	setQueryValue("date_range_end", builder.DateRangeEnd)
	setQueryValue("granularity", builder.Granularity)
	setQueryValue("metric", builder.Metric)
	setQueryValue("chart_type", builder.ChartType)
	setQueryValue("service_values", builder.ServiceValues)
	setQueryValue("linked_account_values", builder.LinkedAccountValues)
	setQueryValue("region_values", builder.RegionValues)
	setQueryValue("usage_type_values", builder.UsageTypeValues)
	setQueryValue("line_item_type_values", builder.LineItemTypeValues)
	setQueryValue("tag_key", builder.TagKey)
	setQueryValue("tag_values", builder.TagValues)
	setQueryValue("cost_category_key", builder.CostCategoryKey)
	setQueryValue("cost_category_values", builder.CostCategoryValues)
	setQueryValue("group_1_type", builder.Group1Type)
	setQueryValue("group_1_key", builder.Group1Key)
	setQueryValue("group_2_type", builder.Group2Type)
	setQueryValue("group_2_key", builder.Group2Key)
	values.Set("run", "1")
	return values
}

func costExplorerPath(builder costExplorerBuilderView) string {
	return "/cost-explorer?" + costExplorerBuilderQueryValues(builder).Encode()
}

func costExplorerResultsCSVPath(builder costExplorerBuilderView) string {
	return "/cost-explorer/results.csv?" + costExplorerBuilderQueryValues(builder).Encode()
}

func costExplorerDrilldownPath(builder costExplorerBuilderView, row persistence.CostExplorerQueryRow) string {
	values := costExplorerBuilderQueryValues(builder)
	values.Set("period_start", row.TimePeriodStart)
	values.Set("period_end", row.TimePeriodEnd)
	for i, group := range row.GroupValues {
		values.Set(fmt.Sprintf("group_%d_value", i+1), group.Value)
	}
	return "/cost-explorer/line-items?" + values.Encode()
}

func costExplorerResultViewFromResult(result persistence.CostExplorerQueryResult, builder costExplorerBuilderView) costExplorerResultView {
	view := costExplorerResultView{
		DateRangeStart: result.DateRangeStart,
		DateRangeEnd:   result.DateRangeEnd,
		Granularity:    result.Granularity,
		Metric:         builder.Metric,
		MetricLabel:    costExplorerMetricLabel(builder.Metric),
		ChartType:      builder.ChartType,
		CSVPath:        costExplorerResultsCSVPath(builder),
		StateCards: []costExplorerStateCardView{
			{Label: "Rows", Value: fmt.Sprintf("%d", len(result.Rows))},
			{Label: "Line Items", Value: fmt.Sprintf("%d", result.TotalLineItemCount)},
			{Label: "Unblended Cost", Value: formatUSDMicros(result.TotalUnblendedCostMicros)},
			{Label: "Usage Quantity", Value: formatQuantityMicros(result.TotalUsageQuantityMicros)},
		},
	}
	for _, row := range result.Rows {
		view.Rows = append(view.Rows, costExplorerResultRowViewFromRow(row, builder))
	}
	view.Chart = costExplorerChartViewFromResult(result, builder.Metric, builder.ChartType)
	return view
}

func costExplorerResultRowViewFromRow(row persistence.CostExplorerQueryRow, builder costExplorerBuilderView) costExplorerResultRowView {
	groups := make([]string, 0, len(row.GroupValues))
	for _, group := range row.GroupValues {
		groups = append(groups, costExplorerGroupLabel(group))
	}
	group1 := "All spend"
	group2 := "None"
	if len(groups) > 0 {
		group1 = groups[0]
	}
	if len(groups) > 1 {
		group2 = groups[1]
	}
	return costExplorerResultRowView{
		PeriodStart:   row.TimePeriodStart,
		PeriodEnd:     row.TimePeriodEnd,
		Group1:        group1,
		Group2:        group2,
		DrilldownPath: costExplorerDrilldownPath(builder, row),
		MetricValue:   costExplorerMetricValue(builder.Metric, row),
		Usage:         formatQuantityMicros(row.UsageQuantityMicros),
		Cost:          formatUSDMicros(row.UnblendedCostMicros),
		LineItems:     row.LineItemCount,
		CurrencyCode:  row.CurrencyCode,
	}
}

func costExplorerResultsCSVBytes(result persistence.CostExplorerQueryResult, builder costExplorerBuilderView) ([]byte, error) {
	var body bytes.Buffer
	writer := csv.NewWriter(&body)
	if err := writer.Write(costExplorerResultsCSVHeader()); err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		if err := writer.Write(costExplorerResultsCSVRecord(result, builder, row)); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func costExplorerResultsCSVHeader() []string {
	return []string{
		"date_range_start",
		"date_range_end",
		"granularity",
		"metric",
		"period_start",
		"period_end",
		"group_1_type",
		"group_1_key",
		"group_1_value",
		"group_2_type",
		"group_2_key",
		"group_2_value",
		"metric_value",
		"usage_quantity",
		"unblended_cost",
		"line_item_count",
		"currency_code",
	}
}

func costExplorerResultsCSVRecord(result persistence.CostExplorerQueryResult, builder costExplorerBuilderView, row persistence.CostExplorerQueryRow) []string {
	group1 := costExplorerCSVGroup(row, 0)
	group2 := costExplorerCSVGroup(row, 1)
	return []string{
		result.DateRangeStart,
		result.DateRangeEnd,
		result.Granularity,
		builder.Metric,
		row.TimePeriodStart,
		row.TimePeriodEnd,
		group1.Type,
		group1.Key,
		group1.Value,
		group2.Type,
		group2.Key,
		group2.Value,
		costExplorerMetricCSVValue(builder.Metric, row),
		formatMicrosDecimal(row.UsageQuantityMicros),
		formatMicrosDecimal(row.UnblendedCostMicros),
		fmt.Sprintf("%d", row.LineItemCount),
		row.CurrencyCode,
	}
}

func costExplorerCSVGroup(row persistence.CostExplorerQueryRow, index int) persistence.CostExplorerGroupValue {
	if index >= 0 && index < len(row.GroupValues) {
		return row.GroupValues[index]
	}
	return persistence.CostExplorerGroupValue{}
}

func costExplorerMetricCSVValue(metric string, row persistence.CostExplorerQueryRow) string {
	switch metric {
	case "usage_quantity":
		return formatMicrosDecimal(row.UsageQuantityMicros)
	default:
		return formatMicrosDecimal(row.UnblendedCostMicros)
	}
}

func costExplorerResultsCSVFilename(builder costExplorerBuilderView) string {
	name := strings.TrimSpace(builder.ReportName)
	if name == "" {
		name = "cost-explorer-report"
	}
	var safe strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('-')
		}
	}
	filename := strings.Trim(safe.String(), "-")
	if filename == "" {
		filename = "cost-explorer-report"
	}
	return filename + ".csv"
}

func costExplorerLineItemViewFromItem(item persistence.BillLineItem) costExplorerLineItemView {
	resource := strings.TrimSpace(item.ResourceID)
	if resource == "" {
		resource = "Period level"
	}
	return costExplorerLineItemView{
		ID:             item.ID,
		Resource:       resource,
		ResourceID:     item.ResourceID,
		Period:         item.BillingPeriodStart + " to " + item.BillingPeriodEnd,
		Status:         item.LineItemStatus,
		PayerAccountID: item.PayerAccountID,
		UsageAccountID: item.UsageAccountID,
		Service:        item.ServiceName,
		ServiceCode:    item.ServiceCode,
		LineItemType:   item.LineItemType,
		RegionCode:     item.RegionCode,
		UsageType:      item.UsageType,
		Operation:      item.Operation,
		Window:         item.UsageStartTime + " to " + item.UsageEndTime,
		Quantity:       formatQuantityMicros(item.PricingQuantityMicros) + " " + item.PricingUnit,
		Rate:           formatUSDMicros(item.UnblendedRateMicros) + "/" + item.PricingUnit,
		Cost:           formatUSDMicros(item.UnblendedCostMicros),
		CurrencyCode:   item.CurrencyCode,
		Description:    item.Description,
		Tags:           keyValueViews(item.TagSnapshot),
	}
}

const (
	costExplorerChartWidth      = 760
	costExplorerChartHeight     = 300
	costExplorerChartPlotX      = 58
	costExplorerChartPlotY      = 28
	costExplorerChartPlotWidth  = 650
	costExplorerChartPlotHeight = 194
)

var costExplorerChartColors = []string{
	"#0f766e",
	"#2563eb",
	"#b45309",
	"#7c3aed",
	"#b42318",
	"#147d3f",
	"#4b5563",
	"#0e7490",
}

type costExplorerChartSeries struct {
	Label  string
	Color  string
	Values map[string]int64
}

// costExplorerChartViewFromResult converts aggregate report rows into server-rendered SVG primitives.
func costExplorerChartViewFromResult(result persistence.CostExplorerQueryResult, metric, chartType string) costExplorerChartView {
	chart := costExplorerChartView{
		Title:       "Cost Explorer report chart",
		Type:        chartType,
		MetricLabel: costExplorerMetricLabel(metric),
		HasRows:     len(result.Rows) > 0,
		Width:       costExplorerChartWidth,
		Height:      costExplorerChartHeight,
		PlotX:       costExplorerChartPlotX,
		PlotY:       costExplorerChartPlotY,
		PlotWidth:   costExplorerChartPlotWidth,
		PlotHeight:  costExplorerChartPlotHeight,
		ZeroY:       costExplorerChartPlotY + costExplorerChartPlotHeight,
	}
	if len(result.Rows) == 0 || chartType == "table" {
		return chart
	}
	if chartType != "line" && chartType != "bar" && chartType != "stacked_bar" {
		return chart
	}

	buckets, series := costExplorerChartBucketsAndSeries(result, metric)
	if len(buckets) == 0 || len(series) == 0 {
		return chart
	}
	stacked := chartType == "stacked_bar"
	maxValue := costExplorerChartMaxValue(buckets, series, stacked)
	if maxValue <= 0 {
		maxValue = 1
	}

	chart.HasChart = true
	chart.YAxisLabel = "Max " + costExplorerChartValueLabel(metric, maxValue)
	chart.Ticks = costExplorerChartTicks(maxValue, metric)
	chart.XLabels = costExplorerChartXLabels(buckets)
	chart.Legend = costExplorerChartLegend(series)
	switch chartType {
	case "line":
		chart.Lines = costExplorerChartLines(buckets, series, metric, maxValue)
	case "bar":
		chart.Bars = costExplorerChartBars(buckets, series, metric, maxValue, false)
	case "stacked_bar":
		chart.Bars = costExplorerChartBars(buckets, series, metric, maxValue, true)
	}
	return chart
}

// costExplorerChartBucketsAndSeries keeps report bucket and grouping order stable for chart rendering.
func costExplorerChartBucketsAndSeries(result persistence.CostExplorerQueryResult, metric string) ([]string, []costExplorerChartSeries) {
	buckets := []string{}
	bucketSeen := map[string]bool{}
	series := []costExplorerChartSeries{}
	seriesIndex := map[string]int{}
	for _, row := range result.Rows {
		if !bucketSeen[row.TimePeriodStart] {
			bucketSeen[row.TimePeriodStart] = true
			buckets = append(buckets, row.TimePeriodStart)
		}
		label := costExplorerChartSeriesLabel(row)
		index, ok := seriesIndex[label]
		if !ok {
			index = len(series)
			seriesIndex[label] = index
			series = append(series, costExplorerChartSeries{
				Label:  label,
				Color:  costExplorerChartColors[index%len(costExplorerChartColors)],
				Values: map[string]int64{},
			})
		}
		series[index].Values[row.TimePeriodStart] += costExplorerMetricMicros(metric, row)
	}
	return buckets, series
}

// costExplorerChartSeriesLabel formats one grouping combination for legends and tooltips.
func costExplorerChartSeriesLabel(row persistence.CostExplorerQueryRow) string {
	if len(row.GroupValues) == 0 {
		return "All spend"
	}
	labels := make([]string, 0, len(row.GroupValues))
	for _, group := range row.GroupValues {
		labels = append(labels, costExplorerGroupLabel(group))
	}
	return strings.Join(labels, " / ")
}

// costExplorerMetricMicros returns the raw metric value used for chart scaling.
func costExplorerMetricMicros(metric string, row persistence.CostExplorerQueryRow) int64 {
	switch metric {
	case "usage_quantity":
		return row.UsageQuantityMicros
	default:
		return row.UnblendedCostMicros
	}
}

// costExplorerChartMaxValue finds the scale ceiling for grouped or stacked charts.
func costExplorerChartMaxValue(buckets []string, series []costExplorerChartSeries, stacked bool) int64 {
	var maxValue int64
	for _, bucket := range buckets {
		var bucketTotal int64
		for _, item := range series {
			value := item.Values[bucket]
			if value < 0 {
				value = 0
			}
			if stacked {
				bucketTotal += value
				continue
			}
			if value > maxValue {
				maxValue = value
			}
		}
		if stacked && bucketTotal > maxValue {
			maxValue = bucketTotal
		}
	}
	return maxValue
}

// costExplorerChartTicks renders a compact vertical scale with top, midpoint, and zero labels.
func costExplorerChartTicks(maxValue int64, metric string) []costExplorerChartTickView {
	mid := maxValue / 2
	if mid == 0 && maxValue > 1 {
		mid = 1
	}
	values := []int64{maxValue, mid, 0}
	ticks := make([]costExplorerChartTickView, 0, len(values))
	seen := map[int64]bool{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		ticks = append(ticks, costExplorerChartTickView{
			Y:     costExplorerChartY(value, maxValue),
			Label: costExplorerChartValueLabel(metric, value),
		})
	}
	return ticks
}

// costExplorerChartXLabels chooses stable bucket labels without crowding daily charts.
func costExplorerChartXLabels(buckets []string) []costExplorerChartAxisLabelView {
	if len(buckets) == 0 {
		return nil
	}
	step := 1
	if len(buckets) > 8 {
		step = (len(buckets) + 6) / 7
	}
	labels := []costExplorerChartAxisLabelView{}
	for index, bucket := range buckets {
		if index%step != 0 && index != len(buckets)-1 {
			continue
		}
		labels = append(labels, costExplorerChartAxisLabelView{
			X:     costExplorerChartX(index, len(buckets)),
			Label: costExplorerChartPeriodLabel(bucket),
		})
	}
	return labels
}

// costExplorerChartLegend maps series colors to labels for learners comparing groups.
func costExplorerChartLegend(series []costExplorerChartSeries) []costExplorerChartLegendView {
	legend := make([]costExplorerChartLegendView, 0, len(series))
	for _, item := range series {
		legend = append(legend, costExplorerChartLegendView{
			Label: item.Label,
			Color: item.Color,
		})
	}
	return legend
}

// costExplorerChartLines renders line chart polylines and point tooltips.
func costExplorerChartLines(buckets []string, series []costExplorerChartSeries, metric string, maxValue int64) []costExplorerChartLineView {
	lines := make([]costExplorerChartLineView, 0, len(series))
	for _, item := range series {
		nodes := make([]costExplorerChartPointView, 0, len(buckets))
		pointParts := make([]string, 0, len(buckets))
		for bucketIndex, bucket := range buckets {
			value := item.Values[bucket]
			x := costExplorerChartX(bucketIndex, len(buckets))
			y := costExplorerChartY(value, maxValue)
			nodes = append(nodes, costExplorerChartPointView{
				X:          x,
				Y:          y,
				Period:     bucket,
				Label:      item.Label,
				ValueLabel: costExplorerChartValueLabel(metric, value),
			})
			pointParts = append(pointParts, fmt.Sprintf("%d,%d", x, y))
		}
		lines = append(lines, costExplorerChartLineView{
			Label:  item.Label,
			Color:  item.Color,
			Points: strings.Join(pointParts, " "),
			Nodes:  nodes,
		})
	}
	return lines
}

// costExplorerChartBars renders grouped or stacked bars with one tooltip per visible segment.
func costExplorerChartBars(buckets []string, series []costExplorerChartSeries, metric string, maxValue int64, stacked bool) []costExplorerChartBarView {
	if len(buckets) == 0 {
		return nil
	}
	bucketWidth := costExplorerChartPlotWidth / len(buckets)
	if bucketWidth < 1 {
		bucketWidth = 1
	}
	bars := []costExplorerChartBarView{}
	for bucketIndex, bucket := range buckets {
		bucketStart := costExplorerChartPlotX + bucketIndex*bucketWidth
		if stacked {
			barWidth := clampInt(bucketWidth-14, 8, 54)
			x := bucketStart + (bucketWidth-barWidth)/2
			cumulative := int64(0)
			for _, item := range series {
				value := item.Values[bucket]
				if value < 0 {
					value = 0
				}
				if value == 0 {
					continue
				}
				next := cumulative + value
				y := costExplorerChartY(next, maxValue)
				previousY := costExplorerChartY(cumulative, maxValue)
				bars = append(bars, costExplorerChartBarView{
					X:          x,
					Y:          y,
					Width:      barWidth,
					Height:     previousY - y,
					Color:      item.Color,
					Period:     bucket,
					Label:      item.Label,
					ValueLabel: costExplorerChartValueLabel(metric, value),
				})
				cumulative = next
			}
			continue
		}

		availableWidth := bucketWidth - 12
		if availableWidth < 8 {
			availableWidth = 8
		}
		barWidth := clampInt(availableWidth/len(series), 4, 34)
		totalWidth := barWidth * len(series)
		x := bucketStart + (bucketWidth-totalWidth)/2
		for seriesIndex, item := range series {
			value := item.Values[bucket]
			if value < 0 {
				value = 0
			}
			y := costExplorerChartY(value, maxValue)
			height := costExplorerChartPlotY + costExplorerChartPlotHeight - y
			if value > 0 && height < 2 {
				height = 2
				y = costExplorerChartPlotY + costExplorerChartPlotHeight - height
			}
			bars = append(bars, costExplorerChartBarView{
				X:          x + seriesIndex*barWidth,
				Y:          y,
				Width:      barWidth,
				Height:     height,
				Color:      item.Color,
				Period:     bucket,
				Label:      item.Label,
				ValueLabel: costExplorerChartValueLabel(metric, value),
			})
		}
	}
	return bars
}

// costExplorerChartX maps a bucket index to the horizontal plot coordinate.
func costExplorerChartX(index, bucketCount int) int {
	if bucketCount <= 1 {
		return costExplorerChartPlotX + costExplorerChartPlotWidth/2
	}
	return costExplorerChartPlotX + (index*costExplorerChartPlotWidth)/(bucketCount-1)
}

// costExplorerChartY maps a metric value to the vertical plot coordinate.
func costExplorerChartY(value, maxValue int64) int {
	if value < 0 {
		value = 0
	}
	if maxValue <= 0 {
		maxValue = 1
	}
	scaled := int((value*int64(costExplorerChartPlotHeight) + maxValue/2) / maxValue)
	if scaled > costExplorerChartPlotHeight {
		scaled = costExplorerChartPlotHeight
	}
	return costExplorerChartPlotY + costExplorerChartPlotHeight - scaled
}

// costExplorerChartValueLabel formats chart values with the selected metric's display rules.
func costExplorerChartValueLabel(metric string, value int64) string {
	switch metric {
	case "usage_quantity":
		return formatQuantityMicros(value)
	default:
		return formatUSDMicros(value)
	}
}

// costExplorerChartPeriodLabel shortens ISO bucket labels for the SVG axis.
func costExplorerChartPeriodLabel(bucket string) string {
	if len(bucket) >= len("2006-01-02T15:04") && strings.Contains(bucket, "T") {
		return bucket[5:10] + " " + bucket[11:16]
	}
	if len(bucket) >= len("2006-01-02") {
		return bucket[5:10]
	}
	return bucket
}

// clampInt limits chart dimensions so tiny and wide result sets stay legible.
func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func costExplorerErrorStateCards() []costExplorerStateCardView {
	return []costExplorerStateCardView{
		{Label: "Rows", Value: "0"},
		{Label: "Line Items", Value: "0"},
		{Label: "Unblended Cost", Value: "$0.00"},
		{Label: "Usage Quantity", Value: "0"},
	}
}

func costExplorerSavedReportViewFromReport(report persistence.SavedReport, selectedID string) costExplorerSavedReportView {
	lastRun := report.LastRunStatus
	if report.LastRunAt != "" {
		lastRun += " " + report.LastRunAt
	}
	return costExplorerSavedReportView{
		ID:             report.ID,
		Name:           report.Name,
		Description:    report.Description,
		OwnerAccountID: report.OwnerAccountID,
		OwnerRole:      report.OwnerRole,
		Owner:          report.OwnerRole + " / " + report.OwnerAccountID,
		DateRange:      report.DateRangeStart + " to " + report.DateRangeEnd,
		Granularity:    report.Granularity,
		Metric:         strings.Join(report.Metrics, ", "),
		ChartType:      report.ChartType,
		LastRun:        lastRun,
		LoadPath:       costExplorerSavedReportPath(report, ""),
		Selected:       report.ID == selectedID,
	}
}

// costExplorerSavedReportPath builds a report load URL that preserves the simulated owner context.
func costExplorerSavedReportPath(report persistence.SavedReport, flashMessage string) string {
	values := url.Values{}
	values.Set("saved_report_id", report.ID)
	values.Set("owner_account_id", report.OwnerAccountID)
	values.Set("owner_role", report.OwnerRole)
	if flashMessage != "" {
		values.Set("flash", flashMessage)
	}
	return "/cost-explorer?" + values.Encode()
}

func costExplorerGroupLabel(group persistence.CostExplorerGroupValue) string {
	prefix := group.Type
	switch group.Type {
	case "dimension":
		prefix = costExplorerDimensionLabel(group.Key)
	case "tag":
		prefix = "tag:" + group.Key
	case "cost_category":
		prefix = "Cost Category:" + group.Key
	}
	return prefix + "=" + group.Value
}

func costExplorerMetricValue(metric string, row persistence.CostExplorerQueryRow) string {
	switch metric {
	case "usage_quantity":
		return formatQuantityMicros(row.UsageQuantityMicros)
	default:
		return formatUSDMicros(row.UnblendedCostMicros)
	}
}

func costExplorerMetricLabel(metric string) string {
	switch metric {
	case "usage_quantity":
		return "Usage Quantity"
	case "unblended_cost":
		return "Unblended Cost"
	default:
		return metric
	}
}

func costExplorerDimensionLabel(key string) string {
	switch key {
	case "service":
		return "Service"
	case "linked_account":
		return "Linked Account"
	case "region":
		return "Region"
	case "usage_type":
		return "Usage Type"
	case "line_item_type":
		return "Line Item Type"
	default:
		return key
	}
}

func costExplorerGroupTypeOptions(selected string) []uiSelectOptionView {
	options := []uiSelectOptionView{
		{Value: "", Label: "None"},
		{Value: "dimension", Label: "Dimension"},
		{Value: "tag", Label: "Tag"},
		{Value: "cost_category", Label: "Cost Category"},
	}
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
	}
	return options
}

func costExplorerMetricOptions(selected string) []uiSelectOptionView {
	if selected == "" {
		selected = "unblended_cost"
	}
	options := []uiSelectOptionView{
		{Value: "unblended_cost", Label: "Unblended Cost"},
		{Value: "usage_quantity", Label: "Usage Quantity"},
	}
	return selectOptionsWithSelected(options, selected)
}

func costExplorerGranularityOptions(selected string) []uiSelectOptionView {
	if selected == "" {
		selected = "monthly"
	}
	options := []uiSelectOptionView{
		{Value: "monthly", Label: "Monthly"},
		{Value: "daily", Label: "Daily"},
		{Value: "hourly", Label: "Hourly"},
	}
	return selectOptionsWithSelected(options, selected)
}

func costExplorerChartTypeOptions(selected string) []uiSelectOptionView {
	if selected == "" {
		selected = "table"
	}
	options := []uiSelectOptionView{
		{Value: "table", Label: "Table"},
		{Value: "line", Label: "Line"},
		{Value: "bar", Label: "Bar"},
		{Value: "stacked_bar", Label: "Stacked Bar"},
	}
	return selectOptionsWithSelected(options, selected)
}

func costExplorerOwnerRoleOptions(selected string) []uiSelectOptionView {
	if selected == "" {
		selected = "management-account"
	}
	options := []uiSelectOptionView{
		{Value: "management-account", Label: "Management"},
		{Value: "finance", Label: "Finance"},
		{Value: "member-account", Label: "Member"},
		{Value: "instructor", Label: "Instructor"},
	}
	return selectOptionsWithSelected(options, selected)
}

func selectOptionsWithSelected(options []uiSelectOptionView, selected string) []uiSelectOptionView {
	found := false
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
		found = found || options[idx].Selected
	}
	if selected != "" && !found {
		options = append(options, uiSelectOptionView{Value: selected, Label: selected, Selected: true})
	}
	return options
}

func costExplorerBaseGroupKeyOptions() []string {
	return []string{"service", "linked_account", "region", "usage_type", "line_item_type", "app", "owner", "product", "environment"}
}

func costExplorerTables() costExplorerTablesView {
	return costExplorerTablesView{
		Results:      uiTable(uiTableHeaders("Period Start", "Period End", "Group 1", "Group 2", "Metric", "Usage", "Cost", "Items", "Currency", "Drilldown"), "No report rows"),
		SavedReports: uiTable(uiTableHeaders("Report", "Owner", "Range", "Granularity", "Metric", "Chart", "Last Run", "Action"), "No saved reports"),
	}
}

func costExplorerLineItemsTables() costExplorerLineItemsTablesView {
	return costExplorerLineItemsTablesView{
		LineItems: uiTable(uiTableHeaders("Line Item", "Resource", "Period", "Accounts", "Service", "Usage", "Window", "Quantity", "Rate", "Cost", "Tags"), "No source bill line items"),
	}
}

func firstValue(values url.Values, key string) string {
	if rawValues, ok := values[key]; ok && len(rawValues) > 0 {
		return strings.TrimSpace(rawValues[0])
	}
	return ""
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

var costExplorerPageTemplate = newPageTemplate("cost-explorer-page", `<div class="page-heading">
			<div>
				<h1>Cost Explorer</h1>
			</div>
		</div>

		<div id="cost-explorer-refresh" data-partial-surface="cost-explorer">
			{{template "cost-explorer.refresh" .}}
		</div>

{{define "cost-explorer.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="report-toolbar">
				<form method="get" action="/cost-explorer" class="saved-report-picker">
					<input type="hidden" name="owner_account_id" value="{{.Builder.OwnerAccountID}}">
					<input type="hidden" name="owner_role" value="{{.Builder.OwnerRole}}">
					<label>Saved Report
						<select name="saved_report_id">
							<option value="">Custom report</option>
							{{range .SavedReports}}<option value="{{.ID}}"{{if .Selected}} selected{{end}}>{{.Name}}</option>{{end}}
						</select>
					</label>
					<button type="submit">Load Report</button>
				</form>
				<div class="page-actions">
					<a class="button-link secondary" href="/resources">Resources</a>
					<a class="button-link secondary" href="/cost-categories">Cost Categories</a>
					<a class="button-link" href="/cost-explorer">New Report</a>
				</div>
			</section>

			<form method="get" action="/cost-explorer" class="report-builder-form">
				<input type="hidden" name="saved_report_id" value="{{.Builder.SavedReportID}}">
				<div class="builder-grid">
					<section class="panel builder-panel">
						<h2>Report Definition</h2>
						<div class="fields">
							<label class="form-row">Name
								<input name="report_name" value="{{.Builder.ReportName}}">
							</label>
							<label class="form-row">Owner Account
								<input name="owner_account_id" value="{{.Builder.OwnerAccountID}}" required>
							</label>
							<label class="form-row">Owner Role
								<select name="owner_role" required>
									{{range .OwnerRoleOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Description
								<input name="description" value="{{.Builder.Description}}">
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Time and Metric</h2>
						<div class="fields">
							<label class="form-row">Start Date
								<input type="date" name="date_range_start" value="{{.Builder.DateRangeStart}}" required>
							</label>
							<label class="form-row">End Date
								<input type="date" name="date_range_end" value="{{.Builder.DateRangeEnd}}" required>
							</label>
							<label class="form-row">Granularity
								<select name="granularity" required>
									{{range .GranularityOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Metric
								<select name="metric" required>
									{{range .MetricOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Chart
								<select name="chart_type" required>
									{{range .ChartTypeOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Filters</h2>
						<div class="fields">
							<label class="form-row">Service Values
								<input name="service_values" value="{{.Builder.ServiceValues}}">
							</label>
							<label class="form-row">Linked Accounts
								<input name="linked_account_values" value="{{.Builder.LinkedAccountValues}}">
							</label>
							<label class="form-row">Regions
								<input name="region_values" value="{{.Builder.RegionValues}}">
							</label>
							<label class="form-row">Usage Types
								<input name="usage_type_values" value="{{.Builder.UsageTypeValues}}">
							</label>
							<label class="form-row">Line Item Types
								<input name="line_item_type_values" value="{{.Builder.LineItemTypeValues}}">
							</label>
							<label class="form-row">Tag Key
								<input name="tag_key" value="{{.Builder.TagKey}}" list="cost-explorer-group-keys">
							</label>
							<label class="form-row">Tag Values
								<input name="tag_values" value="{{.Builder.TagValues}}">
							</label>
							<label class="form-row">Cost Category
								<input name="cost_category_key" value="{{.Builder.CostCategoryKey}}" list="cost-explorer-group-keys">
							</label>
							<label class="form-row">Category Values
								<input name="cost_category_values" value="{{.Builder.CostCategoryValues}}">
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Group By</h2>
						<div class="fields">
							<label class="form-row">Group 1 Type
								<select name="group_1_type">
									{{range .Group1TypeOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Group 1 Key
								<input name="group_1_key" value="{{.Builder.Group1Key}}" list="cost-explorer-group-keys">
							</label>
							<label class="form-row">Group 2 Type
								<select name="group_2_type">
									{{range .Group2TypeOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Group 2 Key
								<input name="group_2_key" value="{{.Builder.Group2Key}}" list="cost-explorer-group-keys">
							</label>
						</div>
						<div class="form-actions">
							<button type="submit" name="run" value="1">Run Report</button>
							<button type="submit" formmethod="post" formaction="/cost-explorer/reports/save">Save Report</button>
						</div>
					</section>
				</div>
				<datalist id="cost-explorer-group-keys">
					{{range .GroupKeyOptions}}<option value="{{.}}"></option>{{end}}
				</datalist>
			</form>

			<section class="state-grid" aria-label="Cost Explorer totals">
				{{range .Result.StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Report Results</h2>
					<span>{{.Result.MetricLabel}} / {{.Result.Granularity}} / {{.Result.ChartType}}</span>
					{{if .Result.CSVPath}}<a class="button-link secondary" href="{{.Result.CSVPath}}">CSV</a>{{end}}
				</div>
				{{if .Result.Chart.HasChart}}
					<div class="report-chart-panel" aria-label="Report chart">
						<div class="report-chart-heading">
							<div>
								<strong>{{.Result.Chart.MetricLabel}}</strong>
								<small>{{.Result.DateRangeStart}} to {{.Result.DateRangeEnd}} - {{.Result.Chart.YAxisLabel}}</small>
							</div>
							<div class="chart-legend">
								{{range .Result.Chart.Legend}}
									<span><i style="background: {{.Color}}"></i>{{.Label}}</span>
								{{end}}
							</div>
						</div>
						<svg class="report-chart report-chart-{{.Result.Chart.Type}}" viewBox="0 0 {{.Result.Chart.Width}} {{.Result.Chart.Height}}" role="img" aria-labelledby="cost-explorer-chart-title">
							<title id="cost-explorer-chart-title">{{.Result.Chart.Title}}</title>
							<rect class="chart-plot" x="{{.Result.Chart.PlotX}}" y="{{.Result.Chart.PlotY}}" width="{{.Result.Chart.PlotWidth}}" height="{{.Result.Chart.PlotHeight}}"></rect>
							{{range .Result.Chart.Ticks}}
								<line class="chart-gridline" x1="58" y1="{{.Y}}" x2="708" y2="{{.Y}}"></line>
								<text class="chart-y-label" x="48" y="{{.Y}}">{{.Label}}</text>
							{{end}}
							<line class="chart-axis" x1="58" y1="{{.Result.Chart.ZeroY}}" x2="708" y2="{{.Result.Chart.ZeroY}}"></line>
							{{range .Result.Chart.Bars}}
								<rect class="chart-bar" x="{{.X}}" y="{{.Y}}" width="{{.Width}}" height="{{.Height}}" fill="{{.Color}}">
									<title>{{.Period}} - {{.Label}} - {{.ValueLabel}}</title>
								</rect>
							{{end}}
							{{range .Result.Chart.Lines}}
								<polyline class="chart-line" points="{{.Points}}" stroke="{{.Color}}"></polyline>
								{{$lineColor := .Color}}
								{{range .Nodes}}
									<circle class="chart-point" cx="{{.X}}" cy="{{.Y}}" r="3.5" fill="{{$lineColor}}">
										<title>{{.Period}} - {{.Label}} - {{.ValueLabel}}</title>
									</circle>
								{{end}}
							{{end}}
							{{range .Result.Chart.XLabels}}
								<text class="chart-x-label" x="{{.X}}" y="252">{{.Label}}</text>
							{{end}}
						</svg>
					</div>
				{{end}}
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Results}}
						<tbody>
							{{range .Result.Rows}}
								<tr>
									<td>{{.PeriodStart}}</td>
									<td>{{.PeriodEnd}}</td>
									<td><span class="status">{{.Group1}}</span></td>
									<td><span class="status">{{.Group2}}</span></td>
									<td><strong>{{.MetricValue}}</strong></td>
									<td>{{.Usage}}</td>
									<td>{{.Cost}}</td>
									<td>{{.LineItems}}</td>
									<td>{{.CurrencyCode}}</td>
									<td><a class="button-link secondary" href="{{.DrilldownPath}}">Line Items</a></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Results}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Saved Reports</h2>
					<span>{{len .SavedReports}} reports</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.SavedReports}}
						<tbody>
							{{range .SavedReports}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.Owner}}</td>
									<td>{{.DateRange}}</td>
									<td>{{.Granularity}}</td>
									<td>{{.Metric}}</td>
									<td>{{.ChartType}}</td>
									<td>{{.LastRun}}</td>
									<td>
										<div class="inline-actions compact-actions">
											{{if .Selected}}<span class="status">Loaded</span>{{else}}<a class="button-link secondary" href="{{.LoadPath}}">Load</a>{{end}}
											<form method="post" action="/cost-explorer/reports/run">
												<input type="hidden" name="saved_report_id" value="{{.ID}}">
												<input type="hidden" name="owner_account_id" value="{{.OwnerAccountID}}">
												<input type="hidden" name="owner_role" value="{{.OwnerRole}}">
												<button type="submit">Run</button>
											</form>
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.SavedReports}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}
`)

var costExplorerLineItemsPageTemplate = newPageTemplate("cost-explorer-line-items-page", `<div class="page-heading">
			<div>
				<h1>Cost Explorer Bill Line Items</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="{{.BackPath}}">Report</a>
				{{if .CSVPath}}<a class="button-link secondary" href="{{.CSVPath}}">CSV</a>{{end}}
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="report-toolbar">
				<div>
					<strong>{{.Period}}</strong>
					{{range .Groups}}<small>{{.}}</small>{{end}}
				</div>
			</section>

			<section class="state-grid" aria-label="Cost Explorer bill line item totals">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Source Line Items</h2>
					<span>{{len .LineItems}} items</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LineItems}}
						<tbody>
							{{range .LineItems}}
								<tr>
									<td><strong>{{.ID}}</strong><small>{{.LineItemType}} {{.Status}}</small></td>
									<td><strong>{{.Resource}}</strong>{{if .ResourceID}}<small>{{.ResourceID}}</small>{{end}}</td>
									<td>{{.Period}}</td>
									<td><strong>{{.PayerAccountID}}</strong><small>{{.UsageAccountID}}</small></td>
									<td><strong>{{.Service}}</strong><small>{{.ServiceCode}} {{.RegionCode}}</small></td>
									<td><code>{{.UsageType}}</code><small>{{.Operation}}</small><small>{{.Description}}</small></td>
									<td>{{.Window}}</td>
									<td>{{.Quantity}}</td>
									<td>{{.Rate}}</td>
									<td><strong>{{.Cost}}</strong><small>{{.CurrencyCode}}</small></td>
									<td>{{template "cost-explorer.tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.LineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}

{{define "cost-explorer.tags"}}
	{{if .}}
		<div class="tags">
			{{range .}}<span>{{.Key}}={{.Value}}</span>{{end}}
		</div>
	{{else}}
		<span class="muted">untagged</span>
	{{end}}
{{end}}
`)
