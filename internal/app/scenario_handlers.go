package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/scenario"
)

type scenarioHandler struct {
	db        *sql.DB
	workspace *workspaceSession
}

type scenariosPageData struct {
	WorkspaceReady       bool
	WorkspaceActionReady bool
	Error                string
	Notices              []uiNoticeView
	WorkspaceEmptyState  uiEmptyStateView
	Scenarios            []scenarioCardView
	RecentRuns           []scenarioRunView
	Tables               scenarioTablesView
}

type scenarioEditorPageData struct {
	WorkspaceReady      bool
	WorkspaceEmptyState uiEmptyStateView
	Notices             []uiNoticeView
	Draft               string
	Preview             scenarioEditorPreviewView
}

type scenarioEditorPreviewView struct {
	HasResult            bool
	Valid                bool
	Status               string
	StatusClass          string
	Name                 string
	ClockStart           string
	OrganizationTemplate string
	RandomSeed           string
	EventCount           string
	CheckCount           string
	SimulatedDuration    string
	Events               []scenarioEditorEventView
	Problems             []string
}

type scenarioEditorEventView struct {
	Sequence string
	ID       string
	Action   string
	Schedule string
	Target   string
}

type scenarioTablesView struct {
	RecentRuns uiTableView
}

type scenarioCardView struct {
	Key               string
	Name              string
	Phase             string
	Objective         string
	EstimatedDuration string
	SimulatedDuration string
	EventCount        int
	CheckCount        int
	StartLabel        string
	ResumeLabel       string
	ResumePath        string
	ClonePath         string
	HasLastRun        bool
	LastRun           scenarioRunView
}

type scenarioRunView struct {
	ID               string
	DefinitionName   string
	Status           string
	StatusClass      string
	ClockStart       string
	CurrentEventID   string
	Events           string
	ResourcesCreated string
	UsageEvents      string
	MeteringRecords  string
	BillLineItems    string
	BillsIssued      string
	StartedAt        string
	CompletedAt      string
	ErrorMessage     string
}

type scenarioCatalogMetadata struct {
	Phase             string
	Objective         string
	EstimatedDuration string
	ResumeLabel       string
	ResumePath        string
}

type scenarioRunAudit struct {
	ID                     string
	DefinitionName         string
	Status                 string
	ClockStart             string
	CurrentEventID         string
	EventsTotal            int
	EventsSucceeded        int
	ResourcesCreated       int
	UsageEventsCreated     int
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	BillsIssued            int
	ErrorMessage           string
	StartedAt              string
	CompletedAt            string
}

// newScenarioHandler builds the server-rendered instructor scenario surface.
func newScenarioHandler(db *sql.DB) scenarioHandler {
	return scenarioHandler{db: db}
}

// newWorkspaceScenarioHandler connects scenario actions to the current workspace session.
func newWorkspaceScenarioHandler(workspace *workspaceSession) scenarioHandler {
	if workspace == nil {
		return scenarioHandler{}
	}
	return scenarioHandler{db: workspace.DB(), workspace: workspace}
}

// handleScenarios renders packaged scenario definitions and recent run attempts.
func (h scenarioHandler) handleScenarios(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderScenarios(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleScenarioEditor renders the local draft editor for scenario definitions.
func (h scenarioHandler) handleScenarioEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderScenarioEditor(w, http.StatusOK, "", "", scenarioEditorDefaultDraft(), scenarioEditorPreviewView{})
}

// handleValidateScenarioEditor validates a draft scenario without launching it.
func (h scenarioHandler) handleValidateScenarioEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		http.Error(w, "Open a workspace before validating scenario drafts.", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderScenarioEditor(w, http.StatusBadRequest, "parse scenario editor form: "+err.Error(), "", scenarioEditorDefaultDraft(), scenarioEditorPreviewView{})
		return
	}

	draft := r.PostForm.Get("scenario_document")
	definition, err := scenario.ParseDefinition(strings.NewReader(draft))
	if err != nil {
		h.renderScenarioEditor(w, http.StatusOK, "", "", draft, scenarioEditorPreviewFromError(err))
		return
	}
	h.renderScenarioEditor(w, http.StatusOK, "", "Scenario draft is valid.", draft, scenarioEditorPreviewFromDefinition(definition))
}

// handleLaunchScenario runs one packaged scenario seed and records its durable audit rows.
func (h scenarioHandler) handleLaunchScenario(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		http.Error(w, "Open a workspace before launching scenarios.", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "parse scenario launch form: "+err.Error(), "")
		return
	}

	key := strings.TrimSpace(r.PostForm.Get("scenario_key"))
	definition, err := scenario.LoadSeedDefinition(key)
	if err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "launch scenario: "+err.Error(), "")
		return
	}
	result, err := scenario.NewRunner(h.db).Run(r.Context(), definition)
	if err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "launch scenario: "+err.Error(), "")
		return
	}

	flash := fmt.Sprintf(
		"Launched %s: %d/%d events succeeded, %s",
		result.Run.DefinitionName,
		result.Run.EventsSucceeded,
		result.Run.EventsTotal,
		scenarioBillsIssuedLabel(result.Run.BillsIssued),
	)
	http.Redirect(w, r, "/scenarios?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// renderScenarios prepares page view data without mutating scenario state.
func (h scenarioHandler) renderScenarios(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := scenariosPageData{
		WorkspaceReady:       h.db != nil,
		WorkspaceActionReady: h.db != nil && h.currentWorkspacePath() != "",
		Error:                errorMessage,
		WorkspaceEmptyState:  uiWorkspaceRequiredState(),
		Tables: scenarioTablesView{
			RecentRuns: uiTable(uiTableHeaders("Scenario", "Status", "Events", "Resources", "Usage", "Bills", "Current Event", "Completed"), "No scenario runs"),
		},
	}
	if h.db != nil {
		scenarios, err := h.loadScenarioCatalog(r.Context())
		if err != nil {
			status = http.StatusInternalServerError
			data.Error = "load scenarios: " + err.Error()
		} else {
			data.Scenarios = scenarios
		}

		recentRuns, err := h.loadRecentScenarioRuns(r.Context(), 10)
		if err != nil {
			status = http.StatusInternalServerError
			if data.Error == "" {
				data.Error = "load scenario runs: " + err.Error()
			}
		} else {
			data.RecentRuns = recentRuns
		}
	}
	data.Notices = uiNotices(flashMessage, data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Scenarios - AWS Billing Simulator",
		ActiveNav: "scenarios",
	}, scenariosPageTemplate, data, "render scenarios page")
}

// renderScenarioEditor prepares the scenario authoring preview page without mutating workspace state.
func (h scenarioHandler) renderScenarioEditor(w http.ResponseWriter, status int, errorMessage, flashMessage, draft string, preview scenarioEditorPreviewView) {
	data := scenarioEditorPageData{
		WorkspaceReady:      h.db != nil,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Notices:             uiNotices(flashMessage, errorMessage),
		Draft:               draft,
		Preview:             preview,
	}
	renderPage(w, status, pageLayoutOptions{
		Title:     "Scenario Editor - AWS Billing Simulator",
		ActiveNav: "scenarios",
	}, scenarioEditorPageTemplate, data, "render scenario editor page")
}

// loadScenarioCatalog combines embedded seed definitions with app-level lab metadata.
func (h scenarioHandler) loadScenarioCatalog(ctx context.Context) ([]scenarioCardView, error) {
	keys, err := scenario.SeedDefinitionKeys()
	if err != nil {
		return nil, err
	}

	metadata := scenarioCatalog()
	cards := make([]scenarioCardView, 0, len(keys))
	for _, key := range keys {
		definition, err := scenario.LoadSeedDefinition(key)
		if err != nil {
			return nil, err
		}
		meta := metadata[key]
		if meta.Phase == "" {
			meta = defaultScenarioMetadata()
		}

		card := scenarioCardView{
			Key:               key,
			Name:              definition.Name,
			Phase:             meta.Phase,
			Objective:         meta.Objective,
			EstimatedDuration: meta.EstimatedDuration,
			SimulatedDuration: scenarioSimulatedDuration(definition),
			EventCount:        len(definition.Events),
			CheckCount:        len(definition.Checks),
			StartLabel:        "Start Lab",
			ResumeLabel:       meta.ResumeLabel,
			ResumePath:        meta.ResumePath,
			ClonePath:         defaultScenarioClonePath(h.currentWorkspacePath(), key),
		}
		run, err := h.latestScenarioRun(ctx, definition.Name)
		if err != nil {
			return nil, err
		}
		if run.ID != "" {
			card.HasLastRun = true
			card.LastRun = scenarioRunViewFromAudit(run)
			card.StartLabel = "Start New Run"
		}
		cards = append(cards, card)
	}
	return cards, nil
}

// latestScenarioRun returns the most recent durable run for one scenario definition.
func (h scenarioHandler) latestScenarioRun(ctx context.Context, definitionName string) (scenarioRunAudit, error) {
	run, err := scanScenarioRun(h.db.QueryRowContext(ctx, `
		SELECT id,
		       definition_name,
		       status,
		       clock_start,
		       current_event_id,
		       events_total,
		       events_succeeded,
		       resources_created,
		       usage_events_created,
		       metering_records_created,
		       bill_line_items_created,
		       bills_issued,
		       error_message,
		       started_at,
		       completed_at
		  FROM scenario_runs
		 WHERE definition_name = ?
		 ORDER BY started_at DESC, id DESC
		 LIMIT 1
	`, definitionName).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return scenarioRunAudit{}, nil
	}
	return run, err
}

// loadRecentScenarioRuns returns the newest scenario attempts for the summary table.
func (h scenarioHandler) loadRecentScenarioRuns(ctx context.Context, limit int) ([]scenarioRunView, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT id,
		       definition_name,
		       status,
		       clock_start,
		       current_event_id,
		       events_total,
		       events_succeeded,
		       resources_created,
		       usage_events_created,
		       metering_records_created,
		       bill_line_items_created,
		       bills_issued,
		       error_message,
		       started_at,
		       completed_at
		  FROM scenario_runs
		 ORDER BY started_at DESC, id DESC
		 LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := []scenarioRunView{}
	for rows.Next() {
		run, err := scanScenarioRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		runs = append(runs, scenarioRunViewFromAudit(run))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

// scanScenarioRun maps a scenario_runs row into a compact audit value.
func scanScenarioRun(scan func(dest ...any) error) (scenarioRunAudit, error) {
	var run scenarioRunAudit
	var completedAt sql.NullString
	if err := scan(
		&run.ID,
		&run.DefinitionName,
		&run.Status,
		&run.ClockStart,
		&run.CurrentEventID,
		&run.EventsTotal,
		&run.EventsSucceeded,
		&run.ResourcesCreated,
		&run.UsageEventsCreated,
		&run.MeteringRecordsCreated,
		&run.BillLineItemsCreated,
		&run.BillsIssued,
		&run.ErrorMessage,
		&run.StartedAt,
		&completedAt,
	); err != nil {
		return scenarioRunAudit{}, err
	}
	if completedAt.Valid {
		run.CompletedAt = completedAt.String
	}
	return run, nil
}

// scenarioRunViewFromAudit formats one scenario run for dense browser tables.
func scenarioRunViewFromAudit(run scenarioRunAudit) scenarioRunView {
	return scenarioRunView{
		ID:               run.ID,
		DefinitionName:   run.DefinitionName,
		Status:           titleLabel(run.Status),
		StatusClass:      scenarioStatusClass(run.Status),
		ClockStart:       run.ClockStart,
		CurrentEventID:   run.CurrentEventID,
		Events:           fmt.Sprintf("%d/%d", run.EventsSucceeded, run.EventsTotal),
		ResourcesCreated: strconv.Itoa(run.ResourcesCreated),
		UsageEvents:      strconv.Itoa(run.UsageEventsCreated),
		MeteringRecords:  strconv.Itoa(run.MeteringRecordsCreated),
		BillLineItems:    strconv.Itoa(run.BillLineItemsCreated),
		BillsIssued:      strconv.Itoa(run.BillsIssued),
		StartedAt:        run.StartedAt,
		CompletedAt:      run.CompletedAt,
		ErrorMessage:     run.ErrorMessage,
	}
}

// scenarioStatusClass maps scenario run states to shared status pill classes.
func scenarioStatusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded":
		return "status-succeeded"
	case "failed":
		return "status-failed"
	case "running":
		return "status-running"
	default:
		return ""
	}
}

// scenarioSimulatedDuration summarizes the event window encoded by a seed definition.
func scenarioSimulatedDuration(definition scenario.Definition) string {
	maxDay := 0
	for _, event := range definition.Events {
		if event.Day > maxDay {
			maxDay = event.Day
		}
	}
	if maxDay > 0 {
		return fmt.Sprintf("%d simulated days", maxDay)
	}
	return fmt.Sprintf("%d events", len(definition.Events))
}

// scenarioBillsIssuedLabel renders a compact bill count phrase for launch flashes.
func scenarioBillsIssuedLabel(count int) string {
	if count == 1 {
		return "1 bill issued"
	}
	return fmt.Sprintf("%d bills issued", count)
}

// scenarioCatalog stores UI-only metadata for the current embedded scenario seeds.
func scenarioCatalog() map[string]scenarioCatalogMetadata {
	return map[string]scenarioCatalogMetadata{
		scenario.FirstConsolidatedBillSeedKey: {
			Phase:             "Phase 1",
			Objective:         "Close a first consolidated bill and reconcile generated resources to bill and invoice rows.",
			EstimatedDuration: "20 min",
			ResumeLabel:       "Resume in Bills",
			ResumePath:        "/bills?viewer_role=management-account&viewer_account_id=999988887777",
		},
		scenario.MissingTagsSeedKey: {
			Phase:             "Phase 2",
			Objective:         "Investigate missing and case-mismatched allocation tags before activating billing visibility.",
			EstimatedDuration: "25 min",
			ResumeLabel:       "Resume in Tags",
			ResumePath:        "/tags",
		},
		scenario.SharedNetworkingAllocationSeedKey: {
			Phase:             "Phase 2",
			Objective:         "Allocate shared NAT Gateway and data-transfer costs from Shared Networking to product teams.",
			EstimatedDuration: "30 min",
			ResumeLabel:       "Resume in Cost Categories",
			ResumePath:        "/cost-categories",
		},
		scenario.PaymentFailureSeedKey: {
			Phase:             "Phase 2",
			Objective:         "Review an issued invoice after the default card fails and retry collection from the due state.",
			EstimatedDuration: "20 min",
			ResumeLabel:       "Resume in Payments",
			ResumePath:        "/payments",
		},
		scenario.ForecastBudgetAlertSeedKey: {
			Phase:             "Phase 2",
			Objective:         "Investigate a Storefront forecast breach, trace the EC2 driver, and decide ownership from the budget alert.",
			EstimatedDuration: "25 min",
			ResumeLabel:       "Resume in Cost Explorer",
			ResumePath:        "/cost-explorer?saved_report_id=saved-report-scn-storefront-forecast-drilldown",
		},
		scenario.UntaggedDataTransferSpikeSeedKey: {
			Phase:             "Phase 2",
			Objective:         "Find an untagged data-transfer spike and trace the cost through billed line items.",
			EstimatedDuration: "20 min",
			ResumeLabel:       "Resume in Cost Explorer",
			ResumePath:        "/cost-explorer?date_range_start=2026-03-01&date_range_end=2026-04-01&granularity=monthly&metric=unblended_cost&chart_type=table&group_1_type=dimension&group_1_key=service&run=1",
		},
	}
}

// defaultScenarioMetadata keeps future packaged seeds visible before curated copy is added.
func defaultScenarioMetadata() scenarioCatalogMetadata {
	return scenarioCatalogMetadata{
		Phase:             "Scenario Lab",
		Objective:         "Run the packaged workspace setup and continue the lab in the simulator.",
		EstimatedDuration: "15 min",
		ResumeLabel:       "Resume",
		ResumePath:        "/resources",
	}
}

// scenarioEditorDefaultDraft returns a minimal valid YAML draft for local authoring.
func scenarioEditorDefaultDraft() string {
	return strings.TrimSpace(`name: Draft scenario
clock:
  start: 2026-03-01
organization_template: anycompany-retail
random_seed: 1
events:
  - id: create-draft-resource
    day: 1
    action: create_resource
    account: Storefront Prod
    service: Amazon EC2
    resource: draft-web
    resource_type: ec2_instance
    region: us-east-1
    tags:
      app: storefront
      env: dev
    attributes:
      instance_type: t3.medium
  - id: draft-web-hours
    day: 2
    action: add_usage
    account: Storefront Prod
    service: Amazon EC2
    resource: draft-web
    amount_hours: 4
checks:
  - id: review-spend
    type: saved_report_exists
    report_name: Draft spend review`) + "\n"
}

// scenarioEditorPreviewFromDefinition summarizes a successfully parsed scenario draft.
func scenarioEditorPreviewFromDefinition(definition scenario.Definition) scenarioEditorPreviewView {
	events := make([]scenarioEditorEventView, 0, len(definition.Events))
	for _, event := range definition.Events {
		events = append(events, scenarioEditorEventView{
			Sequence: strconv.Itoa(event.Sequence),
			ID:       event.ID,
			Action:   string(event.Action),
			Schedule: scenarioEditorEventSchedule(event),
			Target:   scenarioEditorEventTarget(event),
		})
	}
	randomSeed := ""
	if definition.RandomSeed != 0 {
		randomSeed = strconv.FormatInt(definition.RandomSeed, 10)
	}
	return scenarioEditorPreviewView{
		HasResult:            true,
		Valid:                true,
		Status:               "Valid",
		StatusClass:          "status-succeeded",
		Name:                 definition.Name,
		ClockStart:           definition.Clock.Start,
		OrganizationTemplate: definition.OrganizationTemplate,
		RandomSeed:           randomSeed,
		EventCount:           scenarioCountLabel(len(definition.Events), "event", "events"),
		CheckCount:           scenarioCountLabel(len(definition.Checks), "check", "checks"),
		SimulatedDuration:    scenarioSimulatedDuration(definition),
		Events:               events,
	}
}

// scenarioEditorPreviewFromError keeps parser and validation failures visible beside the draft.
func scenarioEditorPreviewFromError(err error) scenarioEditorPreviewView {
	problems := []string{err.Error()}
	var validationErr scenario.ValidationError
	if errors.As(err, &validationErr) {
		problems = append([]string{}, validationErr.Problems...)
	}
	return scenarioEditorPreviewView{
		HasResult:   true,
		Valid:       false,
		Status:      "Invalid",
		StatusClass: "status-failed",
		Problems:    problems,
	}
}

func scenarioEditorEventSchedule(event scenario.Event) string {
	if event.At != "" {
		return event.At
	}
	if event.Day > 0 {
		return "Day " + strconv.Itoa(event.Day)
	}
	return "-"
}

func scenarioEditorEventTarget(event scenario.Event) string {
	for _, value := range []string{
		event.Resource,
		event.ResourceID,
		event.Account,
		event.AccountID,
		event.Category,
		event.BudgetName,
		event.ReportName,
		event.PayerAccount,
		event.PayerAccountID,
	} {
		if value != "" {
			return value
		}
	}
	return "-"
}

func scenarioCountLabel(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}

var scenarioEditorPageTemplate = newPageTemplate("scenario-editor-page", `<div class="page-heading">
			<div>
				<h1>Scenario Editor</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="/scenarios">Scenarios</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="scenario-editor-layout">
				<form method="post" action="/scenarios/editor/validate" class="panel scenario-editor-form">
					<div class="section-heading">
						<h2>Draft</h2>
						<span>YAML</span>
					</div>
					<label class="form-row">Scenario YAML
						<textarea name="scenario_document" class="scenario-editor-textarea" spellcheck="false" required>{{.Draft}}</textarea>
					</label>
					<div class="inline-actions">
						<button type="submit">Validate Draft</button>
						<a class="button-link secondary" href="/scenarios">Cancel</a>
					</div>
				</form>

				<section class="panel scenario-editor-preview">
					<div class="section-heading">
						<h2>Validation Preview</h2>
						{{if .Preview.HasResult}}<span class="status {{.Preview.StatusClass}}">{{.Preview.Status}}</span>{{end}}
					</div>
					{{if .Preview.HasResult}}
						{{if .Preview.Valid}}
							<div class="detail-list">
								<span>Name</span>
								<strong>{{.Preview.Name}}</strong>
							</div>
							<div class="scenario-editor-summary">
								<div class="detail-list">
									<span>Clock Start</span>
									<strong>{{.Preview.ClockStart}}</strong>
								</div>
								<div class="detail-list">
									<span>Template</span>
									<strong>{{.Preview.OrganizationTemplate}}</strong>
								</div>
								<div class="detail-list">
									<span>Events</span>
									<strong>{{.Preview.EventCount}}</strong>
								</div>
								<div class="detail-list">
									<span>Checks</span>
									<strong>{{.Preview.CheckCount}}</strong>
								</div>
								<div class="detail-list">
									<span>Simulated Window</span>
									<strong>{{.Preview.SimulatedDuration}}</strong>
								</div>
								{{if .Preview.RandomSeed}}
									<div class="detail-list">
										<span>Random Seed</span>
										<strong>{{.Preview.RandomSeed}}</strong>
									</div>
								{{end}}
							</div>
							<div class="table-wrap scenario-editor-events">
								<table class="dense-table">
									<thead>
										<tr>
											<th>#</th>
											<th>Event</th>
											<th>Action</th>
											<th>Schedule</th>
											<th>Target</th>
										</tr>
									</thead>
									<tbody>
										{{range .Preview.Events}}
											<tr>
												<td>{{.Sequence}}</td>
												<td><strong>{{.ID}}</strong></td>
												<td>{{.Action}}</td>
												<td>{{.Schedule}}</td>
												<td>{{.Target}}</td>
											</tr>
										{{else}}
											<tr><td colspan="5">No events</td></tr>
										{{end}}
									</tbody>
								</table>
							</div>
						{{else}}
							<ul class="validation-list">
								{{range .Preview.Problems}}
									<li>{{.}}</li>
								{{end}}
							</ul>
						{{end}}
					{{else}}
						<section class="empty compact-empty">
							<h2>No Preview</h2>
						</section>
					{{end}}
				</section>
			</section>
		{{end}}
`)

var scenariosPageTemplate = newPageTemplate("scenarios-page", `<div class="page-heading">
			<div>
				<h1>Scenarios</h1>
			</div>
			{{if .WorkspaceReady}}
				<div class="page-actions">
					<a class="button-link secondary" href="/scenarios/editor">Scenario Editor</a>
				</div>
			{{end}}
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section>
				<div class="section-heading">
					<h2>Available Scenarios</h2>
					<span>{{len .Scenarios}} labs</span>
				</div>
				<div class="scenario-grid">
					{{range .Scenarios}}
						<article class="scenario-card">
							<div class="scenario-card-header">
								<div>
									<h3>{{.Name}}</h3>
									<small>{{.Key}}</small>
								</div>
								<span class="status">{{.Phase}}</span>
							</div>
							<div class="detail-list">
								<span>Objective</span>
								<strong>{{.Objective}}</strong>
							</div>
							<div class="scenario-meta-grid">
								<div class="detail-list">
									<span>Estimated Duration</span>
									<strong>{{.EstimatedDuration}}</strong>
								</div>
								<div class="detail-list">
									<span>Simulated Window</span>
									<strong>{{.SimulatedDuration}}</strong>
								</div>
								<div class="detail-list">
									<span>Events</span>
									<strong>{{.EventCount}}</strong>
									{{if .CheckCount}}<small>{{.CheckCount}} checks</small>{{end}}
								</div>
							</div>
							{{if .HasLastRun}}
								<div class="detail-list scenario-run-detail">
									<span>Last Run</span>
									<strong><span class="status {{.LastRun.StatusClass}}">{{.LastRun.Status}}</span> {{.LastRun.Events}} events</strong>
									<small>{{.LastRun.ID}}</small>
									{{if .LastRun.ErrorMessage}}<small>{{.LastRun.ErrorMessage}}</small>{{end}}
								</div>
							{{end}}
							<div class="scenario-actions">
								<form method="post" action="/scenarios/launch">
									<input type="hidden" name="scenario_key" value="{{.Key}}">
									<button type="submit">{{.StartLabel}}</button>
								</form>
								{{if .HasLastRun}}<a class="button-link secondary" href="{{.ResumePath}}">{{.ResumeLabel}}</a>{{end}}
							</div>
							{{if and .HasLastRun $.WorkspaceActionReady}}
								<div class="scenario-management-actions">
									<form method="post" action="/scenarios/reset">
										<input type="hidden" name="scenario_key" value="{{.Key}}">
										<button type="submit">Reset to Seed</button>
									</form>
									<form method="post" action="/scenarios/clone" class="scenario-clone-form">
										<label class="form-row">Clone Workspace Path
											<input name="clone_workspace_path" value="{{.ClonePath}}" required>
										</label>
										<button type="submit">Clone Workspace</button>
									</form>
									<form method="post" action="/scenarios/archive">
										<input type="hidden" name="scenario_run_id" value="{{.LastRun.ID}}">
										<button type="submit">Archive Review Bundle</button>
									</form>
								</div>
							{{end}}
						</article>
					{{else}}
						<section class="empty">
							<h2>No Scenarios</h2>
							<p>No packaged scenarios are available.</p>
						</section>
					{{end}}
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Recent Runs</h2>
					<span>{{len .RecentRuns}} attempts</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.RecentRuns}}
						<tbody>
							{{range .RecentRuns}}
								<tr>
									<td><strong>{{.DefinitionName}}</strong><small>{{.ID}}</small></td>
									<td><span class="status {{.StatusClass}}">{{.Status}}</span></td>
									<td>{{.Events}}</td>
									<td>{{.ResourcesCreated}}</td>
									<td>{{.UsageEvents}}</td>
									<td>{{.BillsIssued}}</td>
									<td>{{.CurrentEventID}}</td>
									<td>{{if .CompletedAt}}{{.CompletedAt}}{{else}}{{.StartedAt}}{{end}}{{if .ErrorMessage}}<small>{{.ErrorMessage}}</small>{{end}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.RecentRuns}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
