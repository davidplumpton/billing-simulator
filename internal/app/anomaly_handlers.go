package app

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

type anomalyHandler struct {
	db        *sql.DB
	anomalies persistence.CostAnomalyRepository
	clock     persistence.SimulatorClockRepository
}

type anomaliesPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Form                anomalyFormView
	StateCards          []anomalyStateCardView
	AlertRows           []anomalyAlertRowView
	Tables              anomaliesTablesView
}

type anomalyFormView struct {
	BillingPeriodStart  string
	BillingPeriodEnd    string
	BaselinePeriodStart string
	BaselinePeriodEnd   string
	ThresholdPercent    string
	MinimumCurrentCost  string
}

type anomalyStateCardView struct {
	Label string
	Value string
}

type anomalyAlertRowView struct {
	Dimension       string
	Scope           string
	Period          string
	CurrentCost     string
	BaselineCost    string
	Increase        string
	CurrentBaseline string
	LineItems       string
	Kind            string
	KindClass       string
	FirstDetected   string
	LastObserved    string
	Message         string
}

type anomaliesTablesView struct {
	Alerts uiTableView
}

// newAnomalyHandler builds the server-rendered cost anomaly workflow.
func newAnomalyHandler(db *sql.DB) anomalyHandler {
	return anomalyHandler{
		db:        db,
		anomalies: persistence.NewCostAnomalyRepository(db),
		clock:     persistence.NewSimulatorClockRepository(db),
	}
}

// handleAnomalies renders persisted cost anomaly alerts for the selected comparison.
func (h anomalyHandler) handleAnomalies(w http.ResponseWriter, r *http.Request) {
	h.renderAnomalies(w, r, http.StatusOK, anomalyFormView{}, "", flashFromQuery(r))
}

// handleRefreshAnomalies recomputes cost anomaly alerts after an explicit learner action.
func (h anomalyHandler) handleRefreshAnomalies(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		h.renderAnomalies(w, r, http.StatusServiceUnavailable, anomalyFormView{}, "Open a workspace before refreshing anomaly alerts.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderAnomalies(w, r, http.StatusBadRequest, anomalyFormView{}, "parse anomaly form: "+err.Error(), "")
		return
	}
	form := anomalyFormFromValues(r.PostForm)
	request, err := costAnomalyRefreshRequestFromForm(form)
	if err != nil {
		h.renderAnomalies(w, r, http.StatusBadRequest, form, err.Error(), "")
		return
	}
	result, err := h.anomalies.RefreshAlerts(r.Context(), request)
	if err != nil {
		h.renderAnomalies(w, r, http.StatusBadRequest, form, err.Error(), "")
		return
	}

	values := anomalyQueryValuesFromForm(form)
	values.Set("flash", fmt.Sprintf("Refreshed %d cost anomaly alerts", len(result.Alerts)))
	http.Redirect(w, r, "/anomalies?"+values.Encode(), http.StatusSeeOther)
}

func (h anomalyHandler) renderAnomalies(w http.ResponseWriter, r *http.Request, status int, form anomalyFormView, errorMessage, flashMessage string) {
	data := anomaliesPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		Notices:             uiNotices(flashMessage, errorMessage),
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Form:                form,
		Tables:              anomaliesTables(),
	}
	if data.Form.BillingPeriodStart == "" && data.Form.BillingPeriodEnd == "" {
		data.Form = h.defaultAnomalyForm(r)
	}

	if h.db != nil {
		alerts, err := h.anomalies.ListAlerts(r.Context(), costAnomalyListRequestFromForm(data.Form))
		if err != nil && data.Error == "" {
			data.Error = err.Error()
			data.Notices = uiNotices(data.Flash, data.Error)
		}
		data.AlertRows = anomalyAlertRowsFromAlerts(alerts)
		data.StateCards = anomalyStateCards(alerts, data.Form)
	}

	renderPage(w, status, pageLayoutOptions{
		Title:     "Anomalies - Billing Simulator",
		ActiveNav: "anomalies",
	}, anomaliesPageTemplate, data, "render anomalies page")
}

func (h anomalyHandler) defaultAnomalyForm(r *http.Request) anomalyFormView {
	form := anomalyFormFromValues(r.URL.Query())
	if form.BillingPeriodStart == "" {
		form.BillingPeriodStart = "2026-02-01"
	}
	if form.BillingPeriodEnd == "" {
		form.BillingPeriodEnd = "2026-03-01"
	}
	if h.db != nil && r.URL.Query().Get("billing_period_start") == "" && r.URL.Query().Get("billing_period_end") == "" {
		clock, err := h.clock.Get(r.Context())
		if err == nil {
			form.BillingPeriodStart = clock.BillingPeriodStart
			form.BillingPeriodEnd = clock.BillingPeriodEnd
		}
	}
	if form.BaselinePeriodStart == "" || form.BaselinePeriodEnd == "" {
		form.BaselinePeriodStart, form.BaselinePeriodEnd = defaultAnomalyBaselinePeriod(form.BillingPeriodStart, form.BillingPeriodEnd)
	}
	if form.ThresholdPercent == "" {
		form.ThresholdPercent = formatBudgetPercentBasisPoints(int64(persistence.DefaultCostAnomalyThresholdBasisPoints))
	}
	if form.MinimumCurrentCost == "" {
		form.MinimumCurrentCost = formatUSDMicros(persistence.DefaultCostAnomalyMinimumCurrentCostMicros)
	}
	return form
}

func anomalyFormFromValues(values url.Values) anomalyFormView {
	return anomalyFormView{
		BillingPeriodStart:  strings.TrimSpace(values.Get("billing_period_start")),
		BillingPeriodEnd:    strings.TrimSpace(values.Get("billing_period_end")),
		BaselinePeriodStart: strings.TrimSpace(values.Get("baseline_period_start")),
		BaselinePeriodEnd:   strings.TrimSpace(values.Get("baseline_period_end")),
		ThresholdPercent:    strings.TrimSpace(values.Get("threshold_percent")),
		MinimumCurrentCost:  strings.TrimSpace(values.Get("minimum_current_cost")),
	}
}

func costAnomalyRefreshRequestFromForm(form anomalyFormView) (persistence.CostAnomalyRefreshRequest, error) {
	threshold, err := parseBudgetThresholdBasisPoints(form.ThresholdPercent)
	if err != nil {
		return persistence.CostAnomalyRefreshRequest{}, fmt.Errorf("spike threshold: %w", err)
	}
	minimum, err := parseBudgetAmountMicros(form.MinimumCurrentCost)
	if err != nil {
		return persistence.CostAnomalyRefreshRequest{}, fmt.Errorf("minimum current cost: %w", err)
	}
	return persistence.CostAnomalyRefreshRequest{
		BillingPeriodStart:       form.BillingPeriodStart,
		BillingPeriodEnd:         form.BillingPeriodEnd,
		BaselinePeriodStart:      form.BaselinePeriodStart,
		BaselinePeriodEnd:        form.BaselinePeriodEnd,
		ThresholdBasisPoints:     threshold,
		MinimumCurrentCostMicros: minimum,
	}, nil
}

func costAnomalyListRequestFromForm(form anomalyFormView) persistence.CostAnomalyListRequest {
	return persistence.CostAnomalyListRequest{
		BillingPeriodStart:  form.BillingPeriodStart,
		BillingPeriodEnd:    form.BillingPeriodEnd,
		BaselinePeriodStart: form.BaselinePeriodStart,
		BaselinePeriodEnd:   form.BaselinePeriodEnd,
	}
}

func anomalyQueryValuesFromForm(form anomalyFormView) url.Values {
	values := url.Values{}
	appendQueryValue(values, "billing_period_start", form.BillingPeriodStart)
	appendQueryValue(values, "billing_period_end", form.BillingPeriodEnd)
	appendQueryValue(values, "baseline_period_start", form.BaselinePeriodStart)
	appendQueryValue(values, "baseline_period_end", form.BaselinePeriodEnd)
	appendQueryValue(values, "threshold_percent", form.ThresholdPercent)
	appendQueryValue(values, "minimum_current_cost", form.MinimumCurrentCost)
	return values
}

func anomalyAlertRowsFromAlerts(alerts []persistence.CostAnomalyAlert) []anomalyAlertRowView {
	rows := make([]anomalyAlertRowView, 0, len(alerts))
	for _, alert := range alerts {
		rows = append(rows, anomalyAlertRowView{
			Dimension:       anomalyDimensionLabel(alert.DimensionType),
			Scope:           anomalyScopeLabel(alert),
			Period:          alert.BillingPeriodStart + " to " + alert.BillingPeriodEnd,
			CurrentCost:     formatUSDMicros(alert.CurrentCostMicros),
			BaselineCost:    formatUSDMicros(alert.BaselineCostMicros),
			Increase:        formatUSDMicros(alert.IncreaseCostMicros),
			CurrentBaseline: anomalyBasisPointsLabel(alert),
			LineItems:       strconv.Itoa(alert.CurrentLineItemCount) + " / " + strconv.Itoa(alert.BaselineLineItemCount),
			Kind:            anomalyKindLabel(alert.SpikeKind),
			KindClass:       anomalyKindClass(alert.SpikeKind),
			FirstDetected:   alert.FirstDetectedAt,
			LastObserved:    alert.LastObservedAt,
			Message:         alert.Message,
		})
	}
	return rows
}

func anomalyStateCards(alerts []persistence.CostAnomalyAlert, form anomalyFormView) []anomalyStateCardView {
	totalIncreaseMicros := int64(0)
	newSpend := 0
	for _, alert := range alerts {
		totalIncreaseMicros += alert.IncreaseCostMicros
		if alert.SpikeKind == persistence.CostAnomalySpikeNewSpend {
			newSpend++
		}
	}
	return []anomalyStateCardView{
		{Label: "Alerts", Value: strconv.Itoa(len(alerts))},
		{Label: "New Spend", Value: strconv.Itoa(newSpend)},
		{Label: "Spike Spend", Value: formatUSDMicros(totalIncreaseMicros)},
		{Label: "Threshold", Value: form.ThresholdPercent},
	}
}

func anomalyDimensionLabel(dimensionType string) string {
	switch dimensionType {
	case persistence.CostAnomalyDimensionService:
		return "Service"
	case persistence.CostAnomalyDimensionAccount:
		return "Account"
	case persistence.CostAnomalyDimensionTag:
		return "Tag"
	case persistence.CostAnomalyDimensionCostCategory:
		return "Cost Category"
	default:
		return dimensionType
	}
}

func anomalyScopeLabel(alert persistence.CostAnomalyAlert) string {
	switch alert.DimensionType {
	case persistence.CostAnomalyDimensionService:
		return alert.DimensionLabel
	case persistence.CostAnomalyDimensionAccount:
		return alert.DimensionLabel + " (" + alert.DimensionValue + ")"
	default:
		return alert.DimensionLabel
	}
}

func anomalyBasisPointsLabel(alert persistence.CostAnomalyAlert) string {
	if alert.SpikeKind == persistence.CostAnomalySpikeNewSpend {
		return "New"
	}
	return formatBudgetPercentBasisPoints(alert.CurrentCostBasisPoints)
}

func anomalyKindLabel(kind string) string {
	switch kind {
	case persistence.CostAnomalySpikeNewSpend:
		return "New spend"
	case persistence.CostAnomalySpikeIncrease:
		return "Spike"
	default:
		return kind
	}
}

func anomalyKindClass(kind string) string {
	if kind == persistence.CostAnomalySpikeNewSpend {
		return "status-pending"
	}
	return "status-deactivated"
}

func defaultAnomalyBaselinePeriod(periodStart, periodEnd string) (string, string) {
	start, startErr := time.Parse(time.DateOnly, strings.TrimSpace(periodStart))
	end, endErr := time.Parse(time.DateOnly, strings.TrimSpace(periodEnd))
	if startErr != nil || endErr != nil || !start.Before(end) {
		return "", ""
	}
	if start.Day() == 1 && start.AddDate(0, 1, 0).Equal(end) {
		return start.AddDate(0, -1, 0).Format(time.DateOnly), start.Format(time.DateOnly)
	}
	durationDays := int(end.Sub(start).Hours() / 24)
	if durationDays <= 0 {
		durationDays = 1
	}
	return start.AddDate(0, 0, -durationDays).Format(time.DateOnly), start.Format(time.DateOnly)
}

func anomaliesTables() anomaliesTablesView {
	return anomaliesTablesView{
		Alerts: uiTable(uiTableHeaders("Dimension", "Scope", "Period", "Current", "Previous", "Increase", "Current/Previous", "Items", "Kind", "First Seen", "Last Seen", "Message"), "No cost anomaly alerts"),
	}
}

var anomaliesPageTemplate = newPageTemplate("anomalies-page", `<div class="page-heading">
			<div>
				<h1>Anomalies</h1>
			</div>
			<div class="page-actions">
				<form method="post" action="/anomalies/refresh" class="page-action-form">
					<input type="hidden" name="billing_period_start" value="{{.Form.BillingPeriodStart}}">
					<input type="hidden" name="billing_period_end" value="{{.Form.BillingPeriodEnd}}">
					<input type="hidden" name="baseline_period_start" value="{{.Form.BaselinePeriodStart}}">
					<input type="hidden" name="baseline_period_end" value="{{.Form.BaselinePeriodEnd}}">
					<input type="hidden" name="threshold_percent" value="{{.Form.ThresholdPercent}}">
					<input type="hidden" name="minimum_current_cost" value="{{.Form.MinimumCurrentCost}}">
					<button type="submit">Refresh Alerts</button>
				</form>
				<a class="button-link secondary" href="/cost-explorer">Cost Explorer</a>
				<a class="button-link secondary" href="/budgets">Budgets</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="state-grid" aria-label="Anomaly totals">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<form method="get" action="/anomalies" class="filter-bar" aria-label="Anomaly comparison">
				<label>Current Start
					<input type="date" name="billing_period_start" value="{{.Form.BillingPeriodStart}}" required>
				</label>
				<label>Current End
					<input type="date" name="billing_period_end" value="{{.Form.BillingPeriodEnd}}" required>
				</label>
				<label>Baseline Start
					<input type="date" name="baseline_period_start" value="{{.Form.BaselinePeriodStart}}" required>
				</label>
				<label>Baseline End
					<input type="date" name="baseline_period_end" value="{{.Form.BaselinePeriodEnd}}" required>
				</label>
				<label>Spike Percent
					<input name="threshold_percent" value="{{.Form.ThresholdPercent}}" inputmode="decimal" required>
				</label>
				<label>Minimum Cost
					<input name="minimum_current_cost" value="{{.Form.MinimumCurrentCost}}" inputmode="decimal" required>
				</label>
				<button type="submit">Apply</button>
			</form>

			<section>
				<div class="section-heading">
					<h2>Cost Anomaly Alerts</h2>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Alerts}}
						<tbody>
							{{range .AlertRows}}
								<tr>
									<td>{{.Dimension}}</td>
									<td><strong>{{.Scope}}</strong></td>
									<td>{{.Period}}</td>
									<td>{{.CurrentCost}}</td>
									<td>{{.BaselineCost}}</td>
									<td>{{.Increase}}</td>
									<td>{{.CurrentBaseline}}</td>
									<td>{{.LineItems}}</td>
									<td><span class="status {{.KindClass}}">{{.Kind}}</span></td>
									<td>{{.FirstDetected}}</td>
									<td>{{.LastObserved}}</td>
									<td>{{.Message}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Alerts}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
