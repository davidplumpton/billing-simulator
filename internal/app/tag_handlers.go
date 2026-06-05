package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

const (
	costAllocationTagStatusDiscovered  = "discovered"
	costAllocationTagStatusActive      = "active"
	costAllocationTagStatusDeactivated = "deactivated"
)

type costAllocationTagsHandler struct {
	db    *sql.DB
	tags  persistence.CostAllocationTagRepository
	clock persistence.SimulatorClockRepository
}

type tagsPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	ClockCurrentTime    string
	ClockBillingPeriod  string
	StateCards          []tagStateCardView
	TagKeys             []tagKeyView
	Inventory           []tagInventoryView
	ActivationEvents    []tagActivationEventView
	Tables              tagTablesView
}

type tagStateCardView struct {
	Label string
	Value string
}

type tagKeyView struct {
	Key                   string
	Type                  string
	ResourceCount         int
	ValueCount            int
	Values                []string
	ActivationStatus      string
	StatusClass           string
	VisibilityText        string
	VisibilityClass       string
	FirstSeenAt           string
	LastSeenAt            string
	DiscoveredAt          string
	ActivatedAt           string
	DeactivatedAt         string
	CostExplorerVisibleAt string
	CURExportVisibleAt    string
	CanActivate           bool
	CanDeactivate         bool
}

type tagInventoryView struct {
	Key              string
	Value            string
	ResourceCount    int
	FirstSeenAt      string
	LastSeenAt       string
	ActivationStatus string
	StatusClass      string
	VisibilityText   string
}

type tagActivationEventView struct {
	Key                   string
	Action                string
	RequestedAt           string
	EffectiveAt           string
	CostExplorerVisibleAt string
	CURExportVisibleAt    string
	EventSource           string
}

type tagTablesView struct {
	TagKeys          uiTableView
	Inventory        uiTableView
	ActivationEvents uiTableView
}

type tagCoverageSummary struct {
	ResourceCount int
	ValueCount    int
	Values        []string
}

// newCostAllocationTagsHandler builds the repositories for the tag manager workflow.
func newCostAllocationTagsHandler(db *sql.DB) costAllocationTagsHandler {
	return costAllocationTagsHandler{
		db:    db,
		tags:  persistence.NewCostAllocationTagRepository(db),
		clock: persistence.NewSimulatorClockRepository(db),
	}
}

// handleTags renders discovered resource-tag keys and billing activation state.
func (h costAllocationTagsHandler) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderTags(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleActivateTag marks a discovered resource tag key active for billing reports.
func (h costAllocationTagsHandler) handleActivateTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderTags(w, r, http.StatusServiceUnavailable, "Open a workspace before activating cost allocation tags.", "")
		return
	}
	request, err := h.tagActivationRequestFromForm(r)
	if err != nil {
		h.renderTags(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	key, err := h.tags.ActivateTag(r.Context(), request)
	if err != nil {
		h.renderTags(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := "Activated " + key.Key + " for cost allocation; Cost Explorer visibility is pending until " + key.CostExplorerVisibleAt
	http.Redirect(w, r, "/tags?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleDeactivateTag removes one active tag key from billing report visibility.
func (h costAllocationTagsHandler) handleDeactivateTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		h.renderTags(w, r, http.StatusServiceUnavailable, "Open a workspace before deactivating cost allocation tags.", "")
		return
	}
	request, err := h.tagActivationRequestFromForm(r)
	if err != nil {
		h.renderTags(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	key, err := h.tags.DeactivateTag(r.Context(), request)
	if err != nil {
		h.renderTags(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/tags?flash="+urlQueryEscape("Deactivated "+key.Key+" for cost allocation"), http.StatusSeeOther)
}

// renderTags builds the tag manager page from current resource tags and lifecycle history.
func (h costAllocationTagsHandler) renderTags(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := tagsPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Tables:              tagTables(),
	}
	if h.db != nil {
		if err := h.loadTagsPageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices(data.Flash, data.Error)

	if wantsPageFragment(r, "tags") {
		renderPageFragment(w, status, tagManagerPageTemplate, "tags.refresh", data, "render tags fragment")
		return
	}

	renderPage(w, status, pageLayoutOptions{
		Title:     "Tags - AWS Billing Simulator",
		ActiveNav: "tags",
	}, tagManagerPageTemplate, data, "render tags page")
}

// loadTagsPageData refreshes derived tag discovery and prepares the manager view model.
func (h costAllocationTagsHandler) loadTagsPageData(ctx context.Context, data *tagsPageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf("%s to %s (%d days)", clock.BillingPeriodStart, clock.BillingPeriodEnd, clock.BillingPeriodDays)

	if _, err := h.tags.RefreshDiscoveredTags(ctx, clock.CurrentTime); err != nil {
		return err
	}
	keys, err := h.tags.ListDiscoveredKeys(ctx)
	if err != nil {
		return err
	}
	inventory, err := h.tags.ListInventory(ctx)
	if err != nil {
		return err
	}

	coverage := tagCoverageByKey(inventory)
	currentTime := parseTagManagerTime(clock.CurrentTime)
	visibleCount := 0
	pendingCount := 0
	activeCount := 0
	for _, key := range keys {
		summary := coverage[key.Key]
		view := tagKeyViewFromKey(key, summary, currentTime)
		if view.ActivationStatus == costAllocationTagStatusActive {
			activeCount++
			if view.VisibilityClass == "status-pending" {
				pendingCount++
			} else {
				visibleCount++
			}
		}
		data.TagKeys = append(data.TagKeys, view)
		events, err := h.tags.ListActivationEvents(ctx, key.Key)
		if err != nil {
			return err
		}
		for _, event := range events {
			data.ActivationEvents = append(data.ActivationEvents, tagActivationEventViewFromEvent(event))
		}
	}
	sort.SliceStable(data.ActivationEvents, func(i, j int) bool {
		if data.ActivationEvents[i].RequestedAt == data.ActivationEvents[j].RequestedAt {
			return data.ActivationEvents[i].Key < data.ActivationEvents[j].Key
		}
		return data.ActivationEvents[i].RequestedAt > data.ActivationEvents[j].RequestedAt
	})

	for _, row := range inventory {
		data.Inventory = append(data.Inventory, tagInventoryViewFromRow(row, currentTime))
	}
	data.StateCards = tagStateCards(len(keys), activeCount, pendingCount, visibleCount)
	return nil
}

// tagActivationRequestFromForm reads the tag key and stamps it with simulator time.
func (h costAllocationTagsHandler) tagActivationRequestFromForm(r *http.Request) (persistence.CostAllocationTagActivationRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.CostAllocationTagActivationRequest{}, fmt.Errorf("parse cost allocation tag form: %w", err)
	}
	clock, err := h.clock.Get(r.Context())
	if err != nil {
		return persistence.CostAllocationTagActivationRequest{}, err
	}
	return persistence.CostAllocationTagActivationRequest{
		Key:         r.PostForm.Get("tag_key"),
		RequestedAt: clock.CurrentTime,
	}, nil
}

// tagCoverageByKey aggregates key/value inventory into per-key resource coverage.
func tagCoverageByKey(rows []persistence.CostAllocationTagInventoryRow) map[string]tagCoverageSummary {
	summaries := map[string]tagCoverageSummary{}
	for _, row := range rows {
		summary := summaries[row.Key]
		summary.ResourceCount += row.ResourceCount
		summary.ValueCount++
		summary.Values = append(summary.Values, row.Value)
		summaries[row.Key] = summary
	}
	for key, summary := range summaries {
		sort.Strings(summary.Values)
		summaries[key] = summary
	}
	return summaries
}

// tagKeyViewFromKey combines lifecycle state with current inventory coverage.
func tagKeyViewFromKey(key persistence.CostAllocationTagKey, coverage tagCoverageSummary, currentTime time.Time) tagKeyView {
	visibilityText, visibilityClass := tagVisibilityState(key.ActivationStatus, key.CostExplorerVisibleAt, currentTime)
	statusClass := tagStatusClass(key.ActivationStatus)
	if visibilityClass != "" {
		statusClass = visibilityClass
	}
	return tagKeyView{
		Key:                   key.Key,
		Type:                  key.Type,
		ResourceCount:         coverage.ResourceCount,
		ValueCount:            coverage.ValueCount,
		Values:                coverage.Values,
		ActivationStatus:      key.ActivationStatus,
		StatusClass:           statusClass,
		VisibilityText:        visibilityText,
		VisibilityClass:       visibilityClass,
		FirstSeenAt:           key.FirstSeenAt,
		LastSeenAt:            key.LastSeenAt,
		DiscoveredAt:          key.DiscoveredAt,
		ActivatedAt:           key.ActivatedAt,
		DeactivatedAt:         key.DeactivatedAt,
		CostExplorerVisibleAt: key.CostExplorerVisibleAt,
		CURExportVisibleAt:    key.CURExportVisibleAt,
		CanActivate:           key.ActivationStatus != costAllocationTagStatusActive,
		CanDeactivate:         key.ActivationStatus == costAllocationTagStatusActive,
	}
}

// tagInventoryViewFromRow prepares one key/value coverage row for display.
func tagInventoryViewFromRow(row persistence.CostAllocationTagInventoryRow, currentTime time.Time) tagInventoryView {
	visibilityText, visibilityClass := tagVisibilityState(row.ActivationStatus, row.CostExplorerVisibleAt, currentTime)
	statusClass := tagStatusClass(row.ActivationStatus)
	if visibilityClass != "" {
		statusClass = visibilityClass
	}
	return tagInventoryView{
		Key:              row.Key,
		Value:            row.Value,
		ResourceCount:    row.ResourceCount,
		FirstSeenAt:      row.FirstSeenAt,
		LastSeenAt:       row.LastSeenAt,
		ActivationStatus: row.ActivationStatus,
		StatusClass:      statusClass,
		VisibilityText:   visibilityText,
	}
}

// tagActivationEventViewFromEvent adapts repository lifecycle events for tables.
func tagActivationEventViewFromEvent(event persistence.CostAllocationTagActivationEvent) tagActivationEventView {
	return tagActivationEventView{
		Key:                   event.Key,
		Action:                event.Action,
		RequestedAt:           event.RequestedAt,
		EffectiveAt:           event.EffectiveAt,
		CostExplorerVisibleAt: event.CostExplorerVisibleAt,
		CURExportVisibleAt:    event.CURExportVisibleAt,
		EventSource:           event.EventSource,
	}
}

// tagVisibilityState returns learner-facing billing visibility for one tag key.
func tagVisibilityState(status, visibleAt string, currentTime time.Time) (string, string) {
	switch status {
	case costAllocationTagStatusActive:
		if visibleAt == "" {
			return "Active for future reports", ""
		}
		visibleTime := parseTagManagerTime(visibleAt)
		if !visibleTime.IsZero() && !currentTime.IsZero() && currentTime.Before(visibleTime) {
			return "Pending until " + visibleAt, "status-pending"
		}
		return "Visible since " + visibleAt, ""
	case costAllocationTagStatusDeactivated:
		return "Not visible after deactivation", "status-deactivated"
	default:
		return "Not activated", "status-discovered"
	}
}

// tagStatusClass maps lifecycle states to shared status badge CSS classes.
func tagStatusClass(status string) string {
	switch status {
	case costAllocationTagStatusActive:
		return ""
	case costAllocationTagStatusDeactivated:
		return "status-deactivated"
	default:
		return "status-discovered"
	}
}

// parseTagManagerTime parses RFC3339 timestamps for visibility comparisons.
func parseTagManagerTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// tagStateCards summarizes the activation pipeline above the tag tables.
func tagStateCards(discoveredCount, activeCount, pendingCount, visibleCount int) []tagStateCardView {
	return []tagStateCardView{
		{Label: "Discovered Keys", Value: fmt.Sprintf("%d", discoveredCount)},
		{Label: "Active Keys", Value: fmt.Sprintf("%d", activeCount)},
		{Label: "Pending Visibility", Value: fmt.Sprintf("%d", pendingCount)},
		{Label: "Billing Visible", Value: fmt.Sprintf("%d", visibleCount)},
	}
}

// tagTables defines shared table metadata for the cost allocation tag manager.
func tagTables() tagTablesView {
	return tagTablesView{
		TagKeys:          uiTable(uiTableHeaders("Tag Key", "Coverage", "Status", "Billing Visibility", "Seen", "Actions"), "No resource tag keys discovered"),
		Inventory:        uiTable(uiTableHeaders("Tag Key", "Value", "Resources", "First Seen", "Last Seen", "Billing State"), "No tag values discovered"),
		ActivationEvents: uiTable(uiTableHeaders("Event", "Tag Key", "Requested", "Effective", "Billing Visibility", "Source"), "No activation events"),
	}
}

var tagManagerPageTemplate = newPageTemplate("tag-manager-page", `<div class="page-heading">
			<div>
				<h1>Tags</h1>
			</div>
		</div>

		<div id="tags-refresh" data-partial-surface="tags">
			{{template "tags.refresh" .}}
		</div>

{{define "tags.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Cost Allocation Tag Manager</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
				<div class="page-actions">
					<a class="button-link secondary" href="/resources">Resources</a>
					<a class="button-link" href="/tags">Refresh Discovery</a>
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

			<section>
				<div class="section-heading">
					<h2>Tag Key Coverage</h2>
					<span>{{len .TagKeys}} keys</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.TagKeys}}
						<tbody>
							{{range .TagKeys}}
								<tr>
									<td><strong>{{.Key}}</strong><small>{{.Type}}</small></td>
									<td>
										{{.ResourceCount}} resources<small>{{.ValueCount}} values</small>
										{{if .Values}}<div class="tags">{{range .Values}}<span>{{.}}</span>{{end}}</div>{{end}}
									</td>
									<td><span class="status {{.StatusClass}}">{{.ActivationStatus}}</span></td>
									<td>{{.VisibilityText}}{{if .CostExplorerVisibleAt}}<small>Cost Explorer {{.CostExplorerVisibleAt}}</small>{{end}}</td>
									<td>{{.FirstSeenAt}}<small>discovered {{.DiscoveredAt}}</small></td>
									<td>
										<div class="inline-actions">
											{{if .CanActivate}}
												<form method="post" action="/tags/activate">
													<input type="hidden" name="tag_key" value="{{.Key}}">
													<button type="submit">Activate</button>
												</form>
											{{end}}
											{{if .CanDeactivate}}
												<form method="post" action="/tags/deactivate">
													<input type="hidden" name="tag_key" value="{{.Key}}">
													<button type="submit" class="secondary">Deactivate</button>
												</form>
											{{end}}
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.TagKeys}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Discovered Values</h2>
					<span>{{len .Inventory}} values</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Inventory}}
						<tbody>
							{{range .Inventory}}
								<tr>
									<td><strong>{{.Key}}</strong></td>
									<td>{{.Value}}</td>
									<td>{{.ResourceCount}}</td>
									<td>{{.FirstSeenAt}}</td>
									<td>{{.LastSeenAt}}</td>
									<td><span class="status {{.StatusClass}}">{{.VisibilityText}}</span></td>
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
					<h2>Activation History</h2>
					<span>{{len .ActivationEvents}} events</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.ActivationEvents}}
						<tbody>
							{{range .ActivationEvents}}
								<tr>
									<td><span class="status">{{.Action}}</span></td>
									<td><strong>{{.Key}}</strong></td>
									<td>{{.RequestedAt}}</td>
									<td>{{.EffectiveAt}}</td>
									<td>{{if .CostExplorerVisibleAt}}Cost Explorer {{.CostExplorerVisibleAt}}{{else}}Not visible{{end}}</td>
									<td>{{.EventSource}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.ActivationEvents}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}
`)
