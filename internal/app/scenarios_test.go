package app

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestScenariosUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/scenarios")
	if err != nil {
		t.Fatalf("GET /scenarios without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenarios - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/scenarios">Scenarios</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL + "/scenarios/editor")
	if err != nil {
		t.Fatalf("GET /scenarios/editor without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/editor without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenario Editor - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/scenarios">Scenarios</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/editor without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/scenarios/editor/validate", url.Values{"scenario_document": {"name: Draft"}})
	if err != nil {
		t.Fatalf("POST /scenarios/editor/validate without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /scenarios/editor/validate without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before validating scenario drafts.") {
		t.Fatalf("POST /scenarios/editor/validate without workspace missing workspace message: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/scenarios/launch", url.Values{"scenario_key": {"first-consolidated-bill"}})
	if err != nil {
		t.Fatalf("POST /scenarios/launch without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /scenarios/launch without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before launching scenarios.") {
		t.Fatalf("POST /scenarios/launch without workspace missing workspace message: %s", body)
	}

	for _, action := range []struct {
		path string
		form url.Values
		want string
	}{
		{
			path: "/scenarios/reset",
			form: url.Values{"scenario_key": {"first-consolidated-bill"}},
			want: "Open a workspace before resetting scenarios.",
		},
		{
			path: "/scenarios/clone",
			form: url.Values{"clone_workspace_path": {filepath.Join(t.TempDir(), "clone")}},
			want: "Open a workspace before cloning scenarios.",
		},
		{
			path: "/scenarios/archive",
			form: url.Values{"scenario_run_id": {"run_missing"}},
			want: "Open a workspace before archiving scenarios.",
		},
	} {
		resp, err = client.PostForm(server.URL+action.path, action.form)
		if err != nil {
			t.Fatalf("POST %s without workspace error = %v", action.path, err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("POST %s without workspace status = %d, want %d; body=%s", action.path, resp.StatusCode, http.StatusServiceUnavailable, body)
		}
		if !strings.Contains(body, action.want) {
			t.Fatalf("POST %s without workspace missing %q: %s", action.path, action.want, body)
		}
	}
}

func TestScenarioEditorValidationPreviewWorksInFreshWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "scenario-editor-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClientWithTimeout(3 * time.Second)

	resp, err := client.Get(server.URL() + "/scenarios/editor")
	if err != nil {
		t.Fatalf("GET /scenarios/editor error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/editor status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenario Editor - AWS Billing Simulator</title>`,
		`action="/scenarios/editor/validate"`,
		"Scenario YAML",
		"Validation Preview",
		"name: Draft scenario",
		"Validate Draft",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/editor body missing %q: %s", want, body)
		}
	}

	validDraft := `
name: Browser-authored YAML scenario
clock:
  start: 2026-03-01
organization_template: anycompany-retail
events:
  - id: create-browser-web
    day: 1
    action: create_resource
    account: Storefront Prod
    service: Amazon EC2
    resource: browser-web
    resource_type: ec2_instance
    region: us-east-1
    tags:
      app: storefront
  - id: browser-web-hours
    day: 2
    action: add_usage
    account: Storefront Prod
    service: Amazon EC2
    resource: browser-web
    amount_hours: 12
checks:
  - type: saved_report_exists
    report_name: Browser spend review
`
	resp, err = client.PostForm(server.URL()+"/scenarios/editor/validate", url.Values{
		"scenario_document": {validDraft},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/editor/validate valid draft error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/editor/validate valid draft status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Scenario draft is valid.",
		"Valid",
		"Browser-authored YAML scenario",
		"2026-03-01",
		"2 events",
		"1 check",
		"create-browser-web",
		"browser-web-hours",
		"add_usage",
		"Day 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /scenarios/editor/validate valid body missing %q: %s", want, body)
		}
	}

	invalidDraft := `
name: ""
clock:
  start: March 2026
organization_template: anycompany-retail
events:
  - id: missing-quantity
    day: 1
    action: add_usage
    account: Storefront Prod
    service: Amazon EC2
`
	resp, err = client.PostForm(server.URL()+"/scenarios/editor/validate", url.Values{
		"scenario_document": {invalidDraft},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/editor/validate invalid draft error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/editor/validate invalid draft status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Invalid",
		"name is required",
		"clock.start must use YYYY-MM-DD",
		"events[0] must include amount_gb, amount_hours, quantity, or quantity_micros",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /scenarios/editor/validate invalid body missing %q: %s", want, body)
		}
	}

	var runCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs`).Scan(&runCount); err != nil {
		t.Fatalf("count scenario runs after editor validation: %v", err)
	}
	if runCount != 0 {
		t.Fatalf("scenario run count after editor validation = %d, want 0", runCount)
	}
}

func TestScenarioFeedbackReportUsesPersistedLearnerEvidence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "scenario-feedback-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	runID := "scenario-run-feedback"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO scenario_runs (
			id,
			definition_name,
			organization_template,
			random_seed,
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
			started_at,
			completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID,
		"Feedback Fixture",
		persistence.AnyCompanyRetailTemplateKey,
		7,
		"succeeded",
		"2026-03-01",
		"meter-march",
		2,
		2,
		1,
		3,
		3,
		5,
		1,
		"2026-03-01T00:00:00Z",
		"2026-04-01T00:00:00Z",
	); err != nil {
		t.Fatalf("insert scenario run: %v", err)
	}
	progressRepo := persistence.NewScenarioLearnerProgressRepository(db)
	if _, err := progressRepo.StartRun(ctx, persistence.ScenarioLearnerProgressStartRequest{
		ScenarioRunID:    runID,
		DefinitionName:   "Feedback Fixture",
		Objective:        "Investigate billing evidence",
		CurrentObjective: "Run scenario actions",
		ActionsTotal:     2,
		ChecksTotal:      1,
		StartedAt:        "2026-03-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if _, err := progressRepo.RecordAction(ctx, persistence.ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  runID,
		ActionID:       "meter-march",
		ActionSequence: 1,
		ActionType:     "run_daily_metering",
		ActionStatus:   persistence.ScenarioLearnerActionStatusCompleted,
		CompletedAt:    "2026-03-31T00:00:00Z",
		Evidence:       "metering_records=3 bill_line_items=5",
	}); err != nil {
		t.Fatalf("RecordAction() error = %v", err)
	}
	if _, err := progressRepo.RecordAction(ctx, persistence.ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  runID,
		ActionID:       "close-march",
		ActionSequence: 2,
		ActionType:     "close_billing_period",
		ActionStatus:   persistence.ScenarioLearnerActionStatusCompleted,
		CompletedAt:    "2026-04-01T00:00:00Z",
		Evidence:       "bill=bill-feedback",
	}); err != nil {
		t.Fatalf("RecordAction(close) error = %v", err)
	}
	if _, err := progressRepo.CompleteRun(ctx, persistence.ScenarioLearnerRunCompleteRequest{
		ScenarioRunID:         runID,
		RunStatus:             "succeeded",
		CurrentObjectiveState: persistence.ScenarioProgressStateInProgress,
		CurrentObjective:      "Run scenario assessment checks",
		CompletedAt:           "2026-04-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}
	if _, err := progressRepo.RecordCheckResults(ctx, persistence.ScenarioLearnerCheckResultRecordRequest{
		ScenarioRunID: runID,
		EvaluatedAt:   "2026-04-01T01:00:00Z",
		Results: []persistence.ScenarioLearnerCheckResult{{
			CheckID:       "check-top-driver",
			CheckSequence: 1,
			CheckType:     "identifies_top_driver",
			Status:        "passed",
			Expected:      "Amazon EC2",
			Actual:        "Amazon EC2 cost_micros=1230000",
			Message:       "Amazon EC2 is the top cost driver",
		}},
	}); err != nil {
		t.Fatalf("RecordCheckResults() error = %v", err)
	}

	client := appTestHTTPClientWithTimeout(3 * time.Second)
	resp, err := client.Get(server.URL() + "/scenarios")
	if err != nil {
		t.Fatalf("GET /scenarios error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, scenarioFeedbackPath(runID)) {
		t.Fatalf("GET /scenarios body missing feedback report link: %s", body)
	}

	resp, err = client.Get(server.URL() + scenarioFeedbackPath(runID))
	if err != nil {
		t.Fatalf("GET /scenarios/feedback error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/feedback status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenario Feedback - AWS Billing Simulator</title>`,
		"Learner Feedback",
		"Feedback Fixture",
		"This run applied 2 of 2 scenario events",
		"scenario_learner_actions",
		"Run Daily Metering",
		"Converted eligible usage events into metering records and estimated bill line items.",
		"metering_records, bill_line_items",
		"Estimated billing turns usage into metered and priced line items before month end.",
		"Identifies Top Driver",
		"Amazon EC2 cost_micros=1230000",
		"Cost Explorer-style grouping identifies the dominant service or usage driver in bill line items.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/feedback body missing %q: %s", want, body)
		}
	}
}

func TestScenariosListingAndLaunchUIWorksInFreshWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "scenarios-ui-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClientWithTimeout(3 * time.Second)

	resp, err := client.Get(server.URL() + "/scenarios")
	if err != nil {
		t.Fatalf("GET /scenarios error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenarios - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/scenarios">Scenarios</a>`,
		"Available Scenarios",
		"First consolidated bill",
		"Missing Tags",
		"Payment Failure",
		"Shared Networking allocation",
		"Forecast and Budget Alert",
		"Savings Plan coverage",
		"Find the untagged data-transfer spike",
		"Objective",
		"Estimated Duration",
		"Phase 1",
		"Phase 2",
		"Phase 3",
		`action="/scenarios/launch"`,
		`name="scenario_key" value="first-consolidated-bill"`,
		`name="scenario_key" value="missing-tags"`,
		`name="scenario_key" value="payment-failure"`,
		`name="scenario_key" value="shared-networking-allocation"`,
		`name="scenario_key" value="forecast-budget-alert"`,
		`name="scenario_key" value="savings-plan-coverage"`,
		`name="scenario_key" value="untagged-data-transfer-spike"`,
		"Start Lab",
		"Recent Runs",
		"No scenario runs",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/launch", url.Values{
		"scenario_key": {"first-consolidated-bill"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/launch error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/launch final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Launched First consolidated bill: 8/8 events succeeded, 1 bill issued",
		"Start New Run",
		"Resume in Bills",
		`action="/scenarios/reset"`,
		`action="/scenarios/clone"`,
		`action="/scenarios/archive"`,
		"Reset to Seed",
		"Clone Workspace",
		"Archive Review Bundle",
		"Feedback Report",
		`/scenarios/feedback?scenario_run_id=`,
		"Succeeded",
		"Completed",
		"8/8 actions",
		"8/8",
		"Recent Runs",
		"close-march",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /scenarios/launch body missing %q: %s", want, body)
		}
	}

	var runID, status, progressState string
	var eventsSucceeded, billsIssued int
	if err := db.QueryRowContext(ctx, `
		SELECT id, status, events_succeeded, bills_issued
		FROM scenario_runs
		WHERE definition_name = ?
	`, "First consolidated bill").Scan(&runID, &status, &eventsSucceeded, &billsIssued); err != nil {
		t.Fatalf("read launched scenario run: %v", err)
	}
	if status != "succeeded" || eventsSucceeded != 8 || billsIssued != 1 {
		t.Fatalf("scenario run audit = %q/%d/%d, want succeeded/8/1", status, eventsSucceeded, billsIssued)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT current_objective_state
		FROM scenario_learner_progress
		WHERE scenario_run_id = ?
	`, runID).Scan(&progressState); err != nil {
		t.Fatalf("read launched scenario learner progress: %v", err)
	}
	if progressState != persistence.ScenarioProgressStateCompleted {
		t.Fatalf("scenario learner progress state = %q, want completed", progressState)
	}

	resp, err = client.Get(server.URL() + scenarioFeedbackPath(runID))
	if err != nil {
		t.Fatalf("GET /scenarios/feedback packaged run error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/feedback packaged run status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"First consolidated bill",
		"This run applied 8 of 8 scenario events",
		"Create Account",
		"Close Billing Period",
		"Final bills and invoices tie payer obligations back to immutable source line items.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/feedback packaged run body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/archive", url.Values{
		"scenario_run_id": {runID},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/archive error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/archive final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Archived review bundle to") || !strings.Contains(body, "with 2 export files") {
		t.Fatalf("POST /scenarios/archive body missing archive confirmation: %s", body)
	}
	archiveDir := filepath.Join(cfg.WorkspacePath, "review-archives")
	archiveEntries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive directory: %v", err)
	}
	if len(archiveEntries) != 1 {
		t.Fatalf("archive entries = %d, want 1", len(archiveEntries))
	}
	archivePath := filepath.Join(archiveDir, archiveEntries[0].Name())
	archiveReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open review archive: %v", err)
	}
	defer archiveReader.Close()
	archiveNames := map[string]bool{}
	var manifestJSON []byte
	curExportCount := 0
	reconciliationCount := 0
	for _, file := range archiveReader.File {
		archiveNames[file.Name] = true
		if file.Name == "manifest.json" {
			manifestReader, err := file.Open()
			if err != nil {
				t.Fatalf("open archive manifest: %v", err)
			}
			manifestJSON, err = io.ReadAll(manifestReader)
			if closeErr := manifestReader.Close(); closeErr != nil {
				t.Fatalf("close archive manifest: %v", closeErr)
			}
			if err != nil {
				t.Fatalf("read archive manifest: %v", err)
			}
		}
		if strings.HasSuffix(file.Name, "-cur.csv") {
			curExportCount++
		}
		if strings.HasSuffix(file.Name, "-reconciliation.json") {
			reconciliationCount++
		}
	}
	if !archiveNames["manifest.json"] || !archiveNames["workspace/simulator.db"] || !archiveNames["feedback-report.json"] || curExportCount != 1 || reconciliationCount != 1 {
		t.Fatalf("archive entries = %+v, want manifest, feedback report, database, one CUR CSV, and one reconciliation JSON", archiveNames)
	}
	if len(manifestJSON) == 0 {
		t.Fatal("archive manifest was empty")
	}
	if strings.Contains(string(manifestJSON), cfg.WorkspacePath) || strings.Contains(string(manifestJSON), root) {
		t.Fatalf("archive manifest leaked local workspace root %q: %s", root, manifestJSON)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("parse archive manifest: %v; body=%s", err, manifestJSON)
	}
	if _, ok := manifest["workspace_path"]; ok {
		t.Fatalf("archive manifest included deprecated workspace_path field: %s", manifestJSON)
	}
	workspaceLabel, ok := manifest["workspace_label"].(string)
	if !ok || workspaceLabel == "" {
		t.Fatalf("archive manifest workspace_label = %#v, want non-empty string", manifest["workspace_label"])
	}
	if strings.Contains(workspaceLabel, string(os.PathSeparator)) {
		t.Fatalf("archive manifest workspace_label contains path separator: %q", workspaceLabel)
	}
	if manifest["database_path"] != "workspace/simulator.db" || manifest["feedback_report_path"] != "feedback-report.json" {
		t.Fatalf("archive manifest paths = database:%#v feedback:%#v, want stable archive-relative paths", manifest["database_path"], manifest["feedback_report_path"])
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/reset", url.Values{
		"scenario_key": {"first-consolidated-bill"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/reset error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/reset final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Reset First consolidated bill to seed: 8/8 events succeeded, 1 bill issued") {
		t.Fatalf("POST /scenarios/reset body missing reset confirmation: %s", body)
	}
	resetDB := server.workspace.DB()
	if resetDB == nil {
		t.Fatal("workspace database is nil after reset")
	}
	var runCount int
	if err := resetDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs WHERE definition_name = ?`, "First consolidated bill").Scan(&runCount); err != nil {
		t.Fatalf("count scenario reset runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("scenario run count after reset = %d, want 1", runCount)
	}
	if err := resetDB.QueryRowContext(ctx, `SELECT current_objective_state FROM scenario_learner_progress WHERE definition_name = ?`, "First consolidated bill").Scan(&progressState); err != nil {
		t.Fatalf("read scenario reset learner progress: %v", err)
	}
	if progressState != persistence.ScenarioProgressStateCompleted {
		t.Fatalf("scenario reset learner progress state = %q, want completed", progressState)
	}

	clonePath := filepath.Join(root, "scenario-clone-workspace")
	resp, err = client.PostForm(server.URL()+"/scenarios/clone", url.Values{
		"clone_workspace_path": {clonePath},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/clone error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/clone final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Cloned workspace to "+clonePath) || !strings.Contains(body, "Recent Runs") {
		t.Fatalf("POST /scenarios/clone body missing clone confirmation or scenario page: %s", body)
	}
	if got := server.workspace.CurrentPath(); got != clonePath {
		t.Fatalf("current workspace path after clone = %q, want %q", got, clonePath)
	}
	if _, err := os.Stat(persistence.WorkspaceDBPath(clonePath)); err != nil {
		t.Fatalf("cloned workspace database missing: %v", err)
	}
	clonedDB := server.workspace.DB()
	if clonedDB == nil {
		t.Fatal("workspace database is nil after clone")
	}
	if err := clonedDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs WHERE definition_name = ?`, "First consolidated bill").Scan(&runCount); err != nil {
		t.Fatalf("count cloned scenario runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("cloned scenario run count = %d, want 1", runCount)
	}
	if err := clonedDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_learner_progress WHERE definition_name = ?`, "First consolidated bill").Scan(&runCount); err != nil {
		t.Fatalf("count cloned scenario learner progress: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("cloned scenario learner progress count = %d, want 1", runCount)
	}
}

func TestScenarioLaunchReportsClosedPeriodConflictBeforePartialSetup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "closed-period-scenario-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClientWithTimeout(3 * time.Second)

	resp, err := client.PostForm(server.URL()+"/scenarios/launch", url.Values{
		"scenario_key": {"first-consolidated-bill"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/launch first consolidated bill error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/launch first consolidated bill status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/launch", url.Values{
		"scenario_key": {"payment-failure"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/launch payment failure error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /scenarios/launch payment failure status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	wantMessage := "Cannot price March 2026 usage because billing period 2026-03-01 to 2026-04-01 is already closed for payer 999988887777. Reset or clone the workspace before launching this scenario."
	if !strings.Contains(body, wantMessage) {
		t.Fatalf("POST /scenarios/launch body missing closed-period message: %s", body)
	}
	for _, leaked := range []string{"constraint failed", "1811", "billing period is closed for payer"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("POST /scenarios/launch body leaked raw trigger detail %q: %s", leaked, body)
		}
	}

	var runID, status, errorMessage, progressState string
	if err := db.QueryRowContext(ctx, `
		SELECT id, status, error_message
		FROM scenario_runs
		WHERE definition_name = ?
	`, "Payment Failure").Scan(&runID, &status, &errorMessage); err != nil {
		t.Fatalf("read failed payment scenario run: %v", err)
	}
	if status != "failed" || !strings.Contains(errorMessage, wantMessage) {
		t.Fatalf("failed payment run = %q/%q, want learner-facing failed run", status, errorMessage)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT current_objective_state
		FROM scenario_learner_progress
		WHERE scenario_run_id = ?
	`, runID).Scan(&progressState); err != nil {
		t.Fatalf("read failed payment learner progress: %v", err)
	}
	if progressState != persistence.ScenarioProgressStateFailed {
		t.Fatalf("failed payment progress state = %q, want failed", progressState)
	}
	for _, table := range []string{"scenario_run_events", "resources", "usage_events"} {
		var count int
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE scenario_run_id = ?`, table), runID).Scan(&count); err != nil {
			t.Fatalf("count %s for failed run: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows for failed run = %d, want none", table, count)
		}
	}
}
