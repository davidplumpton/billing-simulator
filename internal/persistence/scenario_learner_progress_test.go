package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestScenarioLearnerProgressRepositoryRecordsActionsAndChecks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	insertScenarioLearnerProgressRun(t, db, "scenario-run-progress-1", "Progress Scenario")
	repo := NewScenarioLearnerProgressRepository(db)

	progress, err := repo.StartRun(ctx, ScenarioLearnerProgressStartRequest{
		ScenarioRunID:    "scenario-run-progress-1",
		DefinitionName:   "Progress Scenario",
		Objective:        "Investigate a billing spike",
		CurrentObjective: "Complete scenario action create-assets",
		ActionsTotal:     2,
		ChecksTotal:      2,
		StartedAt:        "2026-03-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if progress.CurrentObjectiveState != ScenarioProgressStateInProgress ||
		progress.ActionsTotal != 2 ||
		progress.ChecksTotal != 2 ||
		progress.StartedAt != "2026-03-01T00:00:00Z" {
		t.Fatalf("started progress = %+v, want in-progress header", progress)
	}

	progress, err = repo.RecordAction(ctx, ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  "scenario-run-progress-1",
		ActionID:       "create-assets",
		ActionSequence: 1,
		ActionType:     "create_resource",
		ActionStatus:   ScenarioLearnerActionStatusCompleted,
		CompletedAt:    "2026-03-02T00:00:00Z",
		Evidence:       "resource=scenario-assets",
	})
	if err != nil {
		t.Fatalf("RecordAction(completed) error = %v", err)
	}
	if progress.ActionsCompleted != 1 {
		t.Fatalf("actions completed after first action = %d, want 1", progress.ActionsCompleted)
	}

	progress, err = repo.RecordAction(ctx, ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  "scenario-run-progress-1",
		ActionID:       "close-march",
		ActionSequence: 2,
		ActionType:     "close_billing_period",
		ActionStatus:   ScenarioLearnerActionStatusCompleted,
		CompletedAt:    "2026-03-31T00:00:00Z",
		Evidence:       "bill=bill-scn-progress",
	})
	if err != nil {
		t.Fatalf("RecordAction(close) error = %v", err)
	}
	if progress.ActionsCompleted != 2 {
		t.Fatalf("actions completed after close = %d, want 2", progress.ActionsCompleted)
	}

	progress, err = repo.CompleteRun(ctx, ScenarioLearnerRunCompleteRequest{
		ScenarioRunID:         "scenario-run-progress-1",
		RunStatus:             "succeeded",
		CurrentObjectiveState: ScenarioProgressStateInProgress,
		CurrentObjective:      "Run scenario assessment checks",
		CompletedAt:           "2026-04-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}
	if progress.CurrentObjectiveState != ScenarioProgressStateInProgress ||
		progress.CurrentObjective != "Run scenario assessment checks" ||
		progress.CompletedAt != "2026-04-01T00:00:00Z" {
		t.Fatalf("completed run progress = %+v, want awaiting checks", progress)
	}

	progress, err = repo.RecordCheckResults(ctx, ScenarioLearnerCheckResultRecordRequest{
		ScenarioRunID: "scenario-run-progress-1",
		EvaluatedAt:   "2026-04-01T01:00:00Z",
		Results: []ScenarioLearnerCheckResult{
			{
				CheckID:       "check-report",
				CheckSequence: 1,
				CheckType:     "saved_report_exists",
				Status:        "passed",
				Expected:      "Billing spike report",
				Actual:        "saved-report-1",
				Message:       "saved report exists",
			},
			{
				CheckID:       "check-driver",
				CheckSequence: 2,
				CheckType:     "identifies_top_driver",
				Status:        "failed",
				Expected:      "AWS Data Transfer",
				Actual:        "Amazon EC2",
				Message:       "Amazon EC2 is the top driver",
			},
		},
	})
	if err != nil {
		t.Fatalf("RecordCheckResults() error = %v", err)
	}
	if progress.CurrentObjectiveState != ScenarioProgressStateNeedsReview ||
		progress.ChecksPassed != 1 ||
		progress.ChecksFailed != 1 ||
		progress.CurrentObjective != "Review failed scenario checks" {
		t.Fatalf("check progress = %+v, want needs-review check summary", progress)
	}

	actions, err := repo.ListActions(ctx, "scenario-run-progress-1")
	if err != nil {
		t.Fatalf("ListActions() error = %v", err)
	}
	if len(actions) != 2 ||
		actions[0].ActionID != "create-assets" ||
		actions[1].Evidence != "bill=bill-scn-progress" {
		t.Fatalf("actions = %+v, want recorded action evidence in order", actions)
	}

	checks, err := repo.ListCheckResults(ctx, "scenario-run-progress-1")
	if err != nil {
		t.Fatalf("ListCheckResults() error = %v", err)
	}
	if len(checks) != 2 ||
		checks[0].CheckID != "check-report" ||
		checks[0].EvaluatedAt != "2026-04-01T01:00:00Z" ||
		checks[1].Status != "failed" {
		t.Fatalf("checks = %+v, want recorded check evidence in order", checks)
	}

	progress, err = repo.RecordCheckResults(ctx, ScenarioLearnerCheckResultRecordRequest{
		ScenarioRunID: "scenario-run-progress-1",
		EvaluatedAt:   "2026-04-01T02:00:00Z",
		Results: []ScenarioLearnerCheckResult{
			{
				CheckID:       "check-report",
				CheckSequence: 1,
				CheckType:     "saved_report_exists",
				Status:        "passed",
			},
			{
				CheckID:       "check-driver",
				CheckSequence: 2,
				CheckType:     "identifies_top_driver",
				Status:        "passed",
			},
		},
	})
	if err != nil {
		t.Fatalf("RecordCheckResults(replaced) error = %v", err)
	}
	if progress.CurrentObjectiveState != ScenarioProgressStateCompleted ||
		progress.ChecksPassed != 2 ||
		progress.ChecksFailed != 0 {
		t.Fatalf("replaced check progress = %+v, want completed all-passed state", progress)
	}
}

func TestScenarioLearnerProgressRepositoryValidatesRequests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewScenarioLearnerProgressRepository(openTestWorkspace(t))

	if _, err := repo.StartRun(ctx, ScenarioLearnerProgressStartRequest{}); err == nil || !strings.Contains(err.Error(), "scenario run ID is required") {
		t.Fatalf("StartRun(blank) error = %v, want scenario run ID validation", err)
	}
	if _, err := repo.StartRun(ctx, ScenarioLearnerProgressStartRequest{
		ScenarioRunID:  "run",
		DefinitionName: "Scenario",
	}); err == nil || !strings.Contains(err.Error(), "start time is required") {
		t.Fatalf("StartRun(blank time) error = %v, want required timestamp validation", err)
	}
	if _, err := repo.RecordAction(ctx, ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  "run",
		ActionID:       "action",
		ActionSequence: 1,
		ActionType:     "create_resource",
		ActionStatus:   "unknown",
	}); err == nil || !strings.Contains(err.Error(), "action status") {
		t.Fatalf("RecordAction(unknown status) error = %v, want status validation", err)
	}
	if _, err := repo.RecordAction(ctx, ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  "run",
		ActionID:       "action",
		ActionSequence: 1,
		ActionType:     "create_resource",
		ActionStatus:   ScenarioLearnerActionStatusCompleted,
	}); err == nil || !strings.Contains(err.Error(), "completion time is required") {
		t.Fatalf("RecordAction(blank time) error = %v, want required timestamp validation", err)
	}
	if _, err := repo.CompleteRun(ctx, ScenarioLearnerRunCompleteRequest{
		ScenarioRunID: "run",
		RunStatus:     ScenarioProgressStateCompleted,
	}); err == nil || !strings.Contains(err.Error(), "completion time is required") {
		t.Fatalf("CompleteRun(blank time) error = %v, want required timestamp validation", err)
	}
	if _, err := repo.RecordCheckResults(ctx, ScenarioLearnerCheckResultRecordRequest{
		ScenarioRunID: "run",
		Results: []ScenarioLearnerCheckResult{{
			CheckID:       "check",
			CheckSequence: 1,
			CheckType:     "saved_report_exists",
			Status:        "unknown",
		}},
	}); err == nil || !strings.Contains(err.Error(), "check status") {
		t.Fatalf("RecordCheckResults(unknown status) error = %v, want status validation", err)
	}
	if _, err := repo.RecordCheckResults(ctx, ScenarioLearnerCheckResultRecordRequest{
		ScenarioRunID: "run",
		Results: []ScenarioLearnerCheckResult{{
			CheckID:       "check",
			CheckSequence: 1,
			CheckType:     "saved_report_exists",
			Status:        "passed",
		}},
	}); err == nil || !strings.Contains(err.Error(), "evaluation time is required") {
		t.Fatalf("RecordCheckResults(blank time) error = %v, want required timestamp validation", err)
	}
}

func insertScenarioLearnerProgressRun(t *testing.T, db *sql.DB, runID, definitionName string) {
	t.Helper()

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO scenario_runs (
			id,
			definition_name,
			organization_template,
			random_seed,
			status,
			clock_start,
			events_total
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		runID,
		definitionName,
		AnyCompanyRetailTemplateKey,
		1,
		"running",
		"2026-03-01T00:00:00Z",
		2,
	); err != nil {
		t.Fatalf("insert scenario run: %v", err)
	}
}
