package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
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
	NewReportPath       string
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
	LineItemsLabel      string
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
	h.renderCostExplorer(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleCostExplorerResultsCSV exports the current aggregate report rows as CSV.
func (h costExplorerHandler) handleCostExplorerResultsCSV(w http.ResponseWriter, r *http.Request) {
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
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+costExplorerResultsCSVFilename(builder)+`"`)
		w.WriteHeader(http.StatusOK)
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
	h.renderCostExplorerLineItems(w, r, http.StatusOK, "")
}

// handleSaveCostExplorerReport creates or updates a saved report from the builder fields.
func (h costExplorerHandler) handleSaveCostExplorerReport(w http.ResponseWriter, r *http.Request) {
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
			Metric:       builder.Metric,
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
		Metric:                   builder.Metric,
		MetricTotalMicros:        costExplorerResultMetricTotalMicros(builder.Metric, result),
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
		Title:     "Cost Explorer Bill Line Items - Billing Simulator",
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
		NewReportPath:       "/cost-explorer",
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
		Title:     "Cost Explorer - Billing Simulator",
		ActiveNav: "cost-explorer",
	}, costExplorerPageTemplate, data, "render Cost Explorer page")
}

// loadCostExplorerLineItemsPageData reads source bill line items for a linked report row.
func (h costExplorerHandler) loadCostExplorerLineItemsPageData(ctx context.Context, r *http.Request, data *costExplorerLineItemsPageData) error {
	builder, err := h.builderFromRequest(ctx, r)
	if err != nil {
		return err
	}
	queryRequest, err := h.scopedCostExplorerQueryRequest(ctx, builder)
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
	result, err := h.explorer.ListLineItems(ctx, persistence.CostExplorerLineItemRequest{
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
	for _, item := range result.Items {
		data.LineItems = append(data.LineItems, costExplorerLineItemViewFromItem(item))
	}
	data.LineItemsLabel = limitedTableLabel(len(result.Items), result.TotalLineItemCount, "item", "items")
	data.StateCards = []costExplorerStateCardView{
		{Label: "Line Items", Value: fmt.Sprintf("%d", result.TotalLineItemCount)},
		{Label: costExplorerMetricLabel(queryRequest.Metric), Value: costExplorerLineItemMetricValue(queryRequest.Metric, result)},
		{Label: "Usage Quantity", Value: formatQuantityMicros(result.TotalUsageQuantityMicros)},
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
	data.NewReportPath = costExplorerNewReportPath(data.Builder)

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
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return costExplorerBuilderView{}, err
	}
	if defaultPayerAccountID != "" {
		builder.OwnerAccountID = defaultPayerAccountID
	}
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
	request, err := h.scopedCostExplorerQueryRequest(ctx, builder)
	if err != nil {
		return persistence.CostExplorerQueryResult{}, err
	}
	return h.explorer.Query(ctx, request)
}

// scopedCostExplorerQueryRequest applies simulated billing visibility to every Cost Explorer read path.
func (h costExplorerHandler) scopedCostExplorerQueryRequest(ctx context.Context, builder costExplorerBuilderView) (persistence.CostExplorerQueryRequest, error) {
	request, err := costExplorerQueryRequestFromBuilder(builder)
	if err != nil {
		return persistence.CostExplorerQueryRequest{}, err
	}
	visibility, err := h.costExplorerVisibilityFilter(ctx, builder)
	if err != nil {
		return persistence.CostExplorerQueryRequest{}, err
	}
	request.Visibility = visibility
	return request, nil
}

// costExplorerVisibilityFilter resolves the report owner controls into row-level billing constraints.
func (h costExplorerHandler) costExplorerVisibilityFilter(ctx context.Context, builder costExplorerBuilderView) (persistence.BillingVisibilityFilter, error) {
	resolution, err := resolveViewerPolicy(ctx, h.db, exportViewerFields{
		Role:      builder.OwnerRole,
		AccountID: builder.OwnerAccountID,
	}, viewerPolicyResolveOptions{
		RequiredView: billingvisibility.ViewCostExplorer,
		PermissionErr: func(policy billingvisibility.Policy) error {
			return fmt.Errorf("billing role %q cannot view Cost Explorer", policy.Role)
		},
	})
	if err != nil {
		return persistence.BillingVisibilityFilter{}, err
	}
	return billingVisibilityFilterFromPolicy(resolution.Policy), nil
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
			{Label: costExplorerMetricLabel(builder.Metric), Value: costExplorerResultMetricValue(builder.Metric, result)},
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
		if report.LastRunStatus == "succeeded" {
			lastRun += " / " + costExplorerMetricLabel(report.LastRunMetric) + " " + costExplorerRunMetricTotalValue(report.LastRunMetric, report.LastRunMetricTotalMicros)
		}
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
	case "blended_cost":
		return formatUSDMicros(row.BlendedCostMicros)
	case "net_cost":
		return formatUSDMicros(row.NetCostMicros)
	case "amortized_cost":
		return formatUSDMicros(row.AmortizedCostMicros)
	default:
		return formatUSDMicros(row.UnblendedCostMicros)
	}
}

// costExplorerResultMetricValue formats the selected aggregate metric for report state cards.
func costExplorerResultMetricValue(metric string, result persistence.CostExplorerQueryResult) string {
	switch metric {
	case "usage_quantity":
		return formatQuantityMicros(result.TotalUsageQuantityMicros)
	case "blended_cost":
		return formatUSDMicros(result.TotalBlendedCostMicros)
	case "net_cost":
		return formatUSDMicros(result.TotalNetCostMicros)
	case "amortized_cost":
		return formatUSDMicros(result.TotalAmortizedCostMicros)
	default:
		return formatUSDMicros(result.TotalUnblendedCostMicros)
	}
}

// costExplorerResultMetricTotalMicros returns the persisted raw total for the selected report metric.
func costExplorerResultMetricTotalMicros(metric string, result persistence.CostExplorerQueryResult) int64 {
	switch metric {
	case "usage_quantity":
		return result.TotalUsageQuantityMicros
	case "blended_cost":
		return result.TotalBlendedCostMicros
	case "net_cost":
		return result.TotalNetCostMicros
	case "amortized_cost":
		return result.TotalAmortizedCostMicros
	default:
		return result.TotalUnblendedCostMicros
	}
}

// costExplorerRunMetricTotalValue formats persisted saved-report run totals with metric-specific units.
func costExplorerRunMetricTotalValue(metric string, totalMicros int64) string {
	if metric == "usage_quantity" {
		return formatQuantityMicros(totalMicros)
	}
	return formatUSDMicros(totalMicros)
}

// costExplorerLineItemMetricValue formats the selected drilldown metric from complete row totals.
func costExplorerLineItemMetricValue(metric string, result persistence.CostExplorerLineItemResult) string {
	switch metric {
	case "usage_quantity":
		return formatQuantityMicros(result.TotalUsageQuantityMicros)
	case "blended_cost":
		return formatUSDMicros(result.TotalBlendedCostMicros)
	case "net_cost":
		return formatUSDMicros(result.TotalNetCostMicros)
	case "amortized_cost":
		return formatUSDMicros(result.TotalAmortizedCostMicros)
	default:
		return formatUSDMicros(result.TotalUnblendedCostMicros)
	}
}

func costExplorerMetricLabel(metric string) string {
	switch metric {
	case "usage_quantity":
		return "Usage Quantity"
	case "unblended_cost":
		return "Unblended Cost"
	case "blended_cost":
		return "Blended Cost"
	case "net_cost":
		return "Net Cost"
	case "amortized_cost":
		return "Amortized Cost"
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
		{Value: "blended_cost", Label: "Blended Cost"},
		{Value: "net_cost", Label: "Net Cost"},
		{Value: "amortized_cost", Label: "Amortized Cost"},
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
	return selectOptionsWithSelected(viewerRoleOptions(), selected)
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
