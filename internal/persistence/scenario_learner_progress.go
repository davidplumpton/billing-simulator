package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	// ScenarioProgressStateInProgress marks a run whose next learner objective is still open.
	ScenarioProgressStateInProgress = "in_progress"

	// ScenarioProgressStateCompleted marks a run whose scenario actions and checks are complete.
	ScenarioProgressStateCompleted = "completed"

	// ScenarioProgressStateNeedsReview marks a run with completed actions but failed checks.
	ScenarioProgressStateNeedsReview = "needs_review"

	// ScenarioProgressStateFailed marks a run that failed before completing scenario setup.
	ScenarioProgressStateFailed = "failed"

	// ScenarioLearnerActionStatusCompleted marks a completed learner-visible action.
	ScenarioLearnerActionStatusCompleted = "completed"

	// ScenarioLearnerActionStatusFailed marks a learner-visible action that stopped with an error.
	ScenarioLearnerActionStatusFailed = "failed"
)

// ScenarioLearnerProgress stores the current progress snapshot for one scenario attempt.
type ScenarioLearnerProgress struct {
	ScenarioRunID         string
	DefinitionName        string
	Objective             string
	CurrentObjectiveState string
	CurrentObjective      string
	ActionsTotal          int
	ActionsCompleted      int
	ChecksTotal           int
	ChecksPassed          int
	ChecksFailed          int
	StartedAt             string
	UpdatedAt             string
	CompletedAt           string
}

// ScenarioLearnerAction stores one completed or failed action for a scenario attempt.
type ScenarioLearnerAction struct {
	ID             string
	ScenarioRunID  string
	ActionID       string
	ActionSequence int
	ActionType     string
	ActionStatus   string
	CompletedAt    string
	Evidence       string
	ErrorMessage   string
}

// ScenarioLearnerCheckResult stores the latest evidence for one assessment check.
type ScenarioLearnerCheckResult struct {
	ID            string
	ScenarioRunID string
	CheckID       string
	CheckSequence int
	CheckType     string
	Status        string
	Expected      string
	Actual        string
	Message       string
	EvaluatedAt   string
}

// ScenarioLearnerProgressStartRequest reserves the progress row for a scenario run.
type ScenarioLearnerProgressStartRequest struct {
	ScenarioRunID    string
	DefinitionName   string
	Objective        string
	CurrentObjective string
	ActionsTotal     int
	ChecksTotal      int
	StartedAt        string
}

// ScenarioLearnerActionRecordRequest records one action outcome for a scenario run.
type ScenarioLearnerActionRecordRequest struct {
	ScenarioRunID  string
	ActionID       string
	ActionSequence int
	ActionType     string
	ActionStatus   string
	CompletedAt    string
	Evidence       string
	ErrorMessage   string
}

// ScenarioLearnerRunCompleteRequest updates the current objective state after a run stops.
type ScenarioLearnerRunCompleteRequest struct {
	ScenarioRunID         string
	RunStatus             string
	CurrentObjective      string
	CurrentObjectiveState string
	CompletedAt           string
}

// ScenarioLearnerCheckResultRecordRequest replaces the latest check evidence for one scenario run.
type ScenarioLearnerCheckResultRecordRequest struct {
	ScenarioRunID string
	EvaluatedAt   string
	Results       []ScenarioLearnerCheckResult
}

// ScenarioLearnerProgressRepository manages persisted learner progress for scenario attempts.
type ScenarioLearnerProgressRepository struct {
	db *sql.DB
}

// NewScenarioLearnerProgressRepository creates a repository backed by a workspace database.
func NewScenarioLearnerProgressRepository(db *sql.DB) ScenarioLearnerProgressRepository {
	return ScenarioLearnerProgressRepository{db: db}
}

// StartRun creates the progress header for a scenario run before scenario events mutate state.
func (r ScenarioLearnerProgressRepository) StartRun(ctx context.Context, request ScenarioLearnerProgressStartRequest) (ScenarioLearnerProgress, error) {
	if r.db == nil {
		return ScenarioLearnerProgress{}, fmt.Errorf("database handle is required")
	}
	request = normalizeScenarioLearnerProgressStartRequest(request)
	if err := validateScenarioLearnerProgressStartRequest(request); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	if request.CurrentObjective == "" {
		request.CurrentObjective = "Run scenario actions"
	}
	_, startedAt, err := requiredRepositoryTimestamp("scenario learner progress start time", request.StartedAt)
	if err != nil {
		return ScenarioLearnerProgress{}, err
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO scenario_learner_progress (
			scenario_run_id,
			definition_name,
			objective,
			current_objective_state,
			current_objective,
			actions_total,
			checks_total,
			started_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scenario_run_id) DO UPDATE SET
			definition_name = excluded.definition_name,
			objective = excluded.objective,
			current_objective_state = excluded.current_objective_state,
			current_objective = excluded.current_objective,
			actions_total = excluded.actions_total,
			actions_completed = 0,
			checks_total = excluded.checks_total,
			checks_passed = 0,
			checks_failed = 0,
			started_at = excluded.started_at,
			updated_at = excluded.updated_at,
			completed_at = NULL`,
		request.ScenarioRunID,
		request.DefinitionName,
		request.Objective,
		ScenarioProgressStateInProgress,
		request.CurrentObjective,
		request.ActionsTotal,
		request.ChecksTotal,
		startedAt,
		startedAt,
	)
	if err != nil {
		return ScenarioLearnerProgress{}, fmt.Errorf("start scenario learner progress %q: %w", request.ScenarioRunID, err)
	}
	return r.Get(ctx, request.ScenarioRunID)
}

// RecordAction records a completed or failed action and refreshes the completed action count.
func (r ScenarioLearnerProgressRepository) RecordAction(ctx context.Context, request ScenarioLearnerActionRecordRequest) (ScenarioLearnerProgress, error) {
	if r.db == nil {
		return ScenarioLearnerProgress{}, fmt.Errorf("database handle is required")
	}
	request = normalizeScenarioLearnerActionRecordRequest(request)
	if err := validateScenarioLearnerActionRecordRequest(request); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	_, completedAt, err := requiredRepositoryTimestamp("scenario learner action completion time", request.CompletedAt)
	if err != nil {
		return ScenarioLearnerProgress{}, err
	}
	id, err := newRepositoryID("scnact")
	if err != nil {
		return ScenarioLearnerProgress{}, err
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO scenario_learner_actions (
				id,
				scenario_run_id,
				action_id,
				action_sequence,
				action_type,
				action_status,
				completed_at,
				evidence,
				error_message
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(scenario_run_id, action_id) DO UPDATE SET
				action_sequence = excluded.action_sequence,
				action_type = excluded.action_type,
				action_status = excluded.action_status,
				completed_at = excluded.completed_at,
				evidence = excluded.evidence,
				error_message = excluded.error_message`,
			id,
			request.ScenarioRunID,
			request.ActionID,
			request.ActionSequence,
			request.ActionType,
			request.ActionStatus,
			completedAt,
			request.Evidence,
			request.ErrorMessage,
		); err != nil {
			return fmt.Errorf("record scenario learner action %q: %w", request.ActionID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE scenario_learner_progress
			   SET actions_completed = (
					SELECT COUNT(*)
					  FROM scenario_learner_actions
					 WHERE scenario_run_id = ?
					   AND action_status = ?
			       ),
			       updated_at = ?
			 WHERE scenario_run_id = ?`,
			request.ScenarioRunID,
			ScenarioLearnerActionStatusCompleted,
			completedAt,
			request.ScenarioRunID,
		); err != nil {
			return fmt.Errorf("refresh scenario learner action count %q: %w", request.ScenarioRunID, err)
		}
		return nil
	}); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	return r.Get(ctx, request.ScenarioRunID)
}

// CompleteRun updates the current objective state when scenario execution finishes or fails.
func (r ScenarioLearnerProgressRepository) CompleteRun(ctx context.Context, request ScenarioLearnerRunCompleteRequest) (ScenarioLearnerProgress, error) {
	if r.db == nil {
		return ScenarioLearnerProgress{}, fmt.Errorf("database handle is required")
	}
	request = normalizeScenarioLearnerRunCompleteRequest(request)
	if err := validateScenarioLearnerRunCompleteRequest(request); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	_, completedAt, err := requiredRepositoryTimestamp("scenario learner progress completion time", request.CompletedAt)
	if err != nil {
		return ScenarioLearnerProgress{}, err
	}
	if request.CurrentObjectiveState == "" {
		if request.RunStatus == ScenarioProgressStateFailed {
			request.CurrentObjectiveState = ScenarioProgressStateFailed
		} else {
			request.CurrentObjectiveState = ScenarioProgressStateCompleted
		}
	}
	if request.CurrentObjective == "" {
		request.CurrentObjective = defaultScenarioLearnerCurrentObjective(request.CurrentObjectiveState)
	}

	result, err := r.db.ExecContext(ctx, `
		UPDATE scenario_learner_progress
		   SET current_objective_state = ?,
		       current_objective = ?,
		       updated_at = ?,
		       completed_at = ?
		 WHERE scenario_run_id = ?`,
		request.CurrentObjectiveState,
		request.CurrentObjective,
		completedAt,
		completedAt,
		request.ScenarioRunID,
	)
	if err != nil {
		return ScenarioLearnerProgress{}, fmt.Errorf("complete scenario learner progress %q: %w", request.ScenarioRunID, err)
	}
	if err := ensureRowsAffected(result, "scenario learner progress", request.ScenarioRunID); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	return r.Get(ctx, request.ScenarioRunID)
}

// RecordCheckResults replaces the latest check evidence and updates objective state.
func (r ScenarioLearnerProgressRepository) RecordCheckResults(ctx context.Context, request ScenarioLearnerCheckResultRecordRequest) (ScenarioLearnerProgress, error) {
	if r.db == nil {
		return ScenarioLearnerProgress{}, fmt.Errorf("database handle is required")
	}
	request = normalizeScenarioLearnerCheckResultRecordRequest(request)
	if err := validateScenarioLearnerCheckResultRecordRequest(request); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	_, evaluatedAt, err := requiredRepositoryTimestamp("scenario learner check evaluation time", request.EvaluatedAt)
	if err != nil {
		return ScenarioLearnerProgress{}, err
	}

	checksPassed := 0
	checksFailed := 0
	for _, result := range request.Results {
		if result.Status == "passed" {
			checksPassed++
		} else {
			checksFailed++
		}
	}
	state := ScenarioProgressStateCompleted
	currentObjective := "All scenario checks passed"
	if checksFailed > 0 {
		state = ScenarioProgressStateNeedsReview
		currentObjective = "Review failed scenario checks"
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM scenario_learner_check_results WHERE scenario_run_id = ?`, request.ScenarioRunID); err != nil {
			return fmt.Errorf("clear scenario learner check results %q: %w", request.ScenarioRunID, err)
		}
		for _, result := range request.Results {
			id, err := newRepositoryID("scnchk")
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO scenario_learner_check_results (
					id,
					scenario_run_id,
					check_id,
					check_sequence,
					check_type,
					status,
					expected,
					actual,
					message,
					evaluated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id,
				request.ScenarioRunID,
				result.CheckID,
				result.CheckSequence,
				result.CheckType,
				result.Status,
				result.Expected,
				result.Actual,
				result.Message,
				evaluatedAt,
			); err != nil {
				return fmt.Errorf("record scenario learner check result %q: %w", result.CheckID, err)
			}
		}
		update, err := tx.ExecContext(ctx, `
			UPDATE scenario_learner_progress
			   SET checks_total = ?,
			       checks_passed = ?,
			       checks_failed = ?,
			       current_objective_state = ?,
			       current_objective = ?,
			       updated_at = ?,
			       completed_at = ?
			 WHERE scenario_run_id = ?`,
			len(request.Results),
			checksPassed,
			checksFailed,
			state,
			currentObjective,
			evaluatedAt,
			evaluatedAt,
			request.ScenarioRunID,
		)
		if err != nil {
			return fmt.Errorf("update scenario learner check summary %q: %w", request.ScenarioRunID, err)
		}
		return ensureRowsAffected(update, "scenario learner progress", request.ScenarioRunID)
	}); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	return r.Get(ctx, request.ScenarioRunID)
}

// Get reads the progress snapshot for one scenario run.
func (r ScenarioLearnerProgressRepository) Get(ctx context.Context, scenarioRunID string) (ScenarioLearnerProgress, error) {
	if r.db == nil {
		return ScenarioLearnerProgress{}, fmt.Errorf("database handle is required")
	}
	scenarioRunID = strings.TrimSpace(scenarioRunID)
	if scenarioRunID == "" {
		return ScenarioLearnerProgress{}, fmt.Errorf("scenario run ID is required")
	}
	progress, err := scanScenarioLearnerProgress(r.db.QueryRowContext(ctx, scenarioLearnerProgressSelectSQL+` WHERE scenario_run_id = ?`, scenarioRunID))
	if err != nil {
		return ScenarioLearnerProgress{}, err
	}
	return progress, nil
}

// ListActions returns learner-visible action outcomes in scenario order.
func (r ScenarioLearnerProgressRepository) ListActions(ctx context.Context, scenarioRunID string) ([]ScenarioLearnerAction, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	scenarioRunID = strings.TrimSpace(scenarioRunID)
	if scenarioRunID == "" {
		return nil, fmt.Errorf("scenario run ID is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id,
		       scenario_run_id,
		       action_id,
		       action_sequence,
		       action_type,
		       action_status,
		       completed_at,
		       evidence,
		       error_message
		  FROM scenario_learner_actions
		 WHERE scenario_run_id = ?
		 ORDER BY action_sequence, action_id`, scenarioRunID)
	if err != nil {
		return nil, fmt.Errorf("list scenario learner actions %q: %w", scenarioRunID, err)
	}
	defer rows.Close()

	actions := []ScenarioLearnerAction{}
	for rows.Next() {
		action, err := scanScenarioLearnerAction(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scenario learner actions %q: %w", scenarioRunID, err)
	}
	return actions, nil
}

// ListCheckResults returns latest check evidence in scenario definition order.
func (r ScenarioLearnerProgressRepository) ListCheckResults(ctx context.Context, scenarioRunID string) ([]ScenarioLearnerCheckResult, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	scenarioRunID = strings.TrimSpace(scenarioRunID)
	if scenarioRunID == "" {
		return nil, fmt.Errorf("scenario run ID is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id,
		       scenario_run_id,
		       check_id,
		       check_sequence,
		       check_type,
		       status,
		       expected,
		       actual,
		       message,
		       evaluated_at
		  FROM scenario_learner_check_results
		 WHERE scenario_run_id = ?
		 ORDER BY check_sequence, check_id`, scenarioRunID)
	if err != nil {
		return nil, fmt.Errorf("list scenario learner check results %q: %w", scenarioRunID, err)
	}
	defer rows.Close()

	results := []ScenarioLearnerCheckResult{}
	for rows.Next() {
		result, err := scanScenarioLearnerCheckResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scenario learner check results %q: %w", scenarioRunID, err)
	}
	return results, nil
}

const scenarioLearnerProgressSelectSQL = `
	SELECT scenario_run_id,
	       definition_name,
	       objective,
	       current_objective_state,
	       current_objective,
	       actions_total,
	       actions_completed,
	       checks_total,
	       checks_passed,
	       checks_failed,
	       started_at,
	       updated_at,
	       completed_at
	  FROM scenario_learner_progress`

type scenarioLearnerProgressRow interface {
	Scan(dest ...any) error
}

func scanScenarioLearnerProgress(row scenarioLearnerProgressRow) (ScenarioLearnerProgress, error) {
	var progress ScenarioLearnerProgress
	var completedAt sql.NullString
	if err := row.Scan(
		&progress.ScenarioRunID,
		&progress.DefinitionName,
		&progress.Objective,
		&progress.CurrentObjectiveState,
		&progress.CurrentObjective,
		&progress.ActionsTotal,
		&progress.ActionsCompleted,
		&progress.ChecksTotal,
		&progress.ChecksPassed,
		&progress.ChecksFailed,
		&progress.StartedAt,
		&progress.UpdatedAt,
		&completedAt,
	); err != nil {
		return ScenarioLearnerProgress{}, err
	}
	progress.CompletedAt = nullStringValue(completedAt)
	return progress, nil
}

func scanScenarioLearnerAction(row scenarioLearnerProgressRow) (ScenarioLearnerAction, error) {
	var action ScenarioLearnerAction
	if err := row.Scan(
		&action.ID,
		&action.ScenarioRunID,
		&action.ActionID,
		&action.ActionSequence,
		&action.ActionType,
		&action.ActionStatus,
		&action.CompletedAt,
		&action.Evidence,
		&action.ErrorMessage,
	); err != nil {
		return ScenarioLearnerAction{}, fmt.Errorf("scan scenario learner action: %w", err)
	}
	return action, nil
}

func scanScenarioLearnerCheckResult(row scenarioLearnerProgressRow) (ScenarioLearnerCheckResult, error) {
	var result ScenarioLearnerCheckResult
	if err := row.Scan(
		&result.ID,
		&result.ScenarioRunID,
		&result.CheckID,
		&result.CheckSequence,
		&result.CheckType,
		&result.Status,
		&result.Expected,
		&result.Actual,
		&result.Message,
		&result.EvaluatedAt,
	); err != nil {
		return ScenarioLearnerCheckResult{}, fmt.Errorf("scan scenario learner check result: %w", err)
	}
	return result, nil
}

func normalizeScenarioLearnerProgressStartRequest(request ScenarioLearnerProgressStartRequest) ScenarioLearnerProgressStartRequest {
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.DefinitionName = strings.TrimSpace(request.DefinitionName)
	request.Objective = strings.TrimSpace(request.Objective)
	request.CurrentObjective = strings.TrimSpace(request.CurrentObjective)
	request.StartedAt = strings.TrimSpace(request.StartedAt)
	return request
}

func validateScenarioLearnerProgressStartRequest(request ScenarioLearnerProgressStartRequest) error {
	if request.ScenarioRunID == "" {
		return fmt.Errorf("scenario run ID is required")
	}
	if request.DefinitionName == "" {
		return fmt.Errorf("scenario definition name is required")
	}
	if request.ActionsTotal < 0 {
		return fmt.Errorf("scenario learner action total must be non-negative")
	}
	if request.ChecksTotal < 0 {
		return fmt.Errorf("scenario learner check total must be non-negative")
	}
	return nil
}

func normalizeScenarioLearnerActionRecordRequest(request ScenarioLearnerActionRecordRequest) ScenarioLearnerActionRecordRequest {
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ActionID = strings.TrimSpace(request.ActionID)
	request.ActionType = strings.TrimSpace(request.ActionType)
	request.ActionStatus = strings.TrimSpace(request.ActionStatus)
	request.CompletedAt = strings.TrimSpace(request.CompletedAt)
	request.Evidence = strings.TrimSpace(request.Evidence)
	request.ErrorMessage = strings.TrimSpace(request.ErrorMessage)
	return request
}

func validateScenarioLearnerActionRecordRequest(request ScenarioLearnerActionRecordRequest) error {
	if request.ScenarioRunID == "" {
		return fmt.Errorf("scenario run ID is required")
	}
	if request.ActionID == "" {
		return fmt.Errorf("scenario learner action ID is required")
	}
	if request.ActionSequence <= 0 {
		return fmt.Errorf("scenario learner action sequence must be positive")
	}
	if request.ActionType == "" {
		return fmt.Errorf("scenario learner action type is required")
	}
	switch request.ActionStatus {
	case ScenarioLearnerActionStatusCompleted, ScenarioLearnerActionStatusFailed:
	default:
		return fmt.Errorf("scenario learner action status %q is not supported", request.ActionStatus)
	}
	return nil
}

func normalizeScenarioLearnerRunCompleteRequest(request ScenarioLearnerRunCompleteRequest) ScenarioLearnerRunCompleteRequest {
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.RunStatus = strings.TrimSpace(request.RunStatus)
	request.CurrentObjective = strings.TrimSpace(request.CurrentObjective)
	request.CurrentObjectiveState = strings.TrimSpace(request.CurrentObjectiveState)
	request.CompletedAt = strings.TrimSpace(request.CompletedAt)
	return request
}

func validateScenarioLearnerRunCompleteRequest(request ScenarioLearnerRunCompleteRequest) error {
	if request.ScenarioRunID == "" {
		return fmt.Errorf("scenario run ID is required")
	}
	if request.RunStatus == "" {
		return fmt.Errorf("scenario run status is required")
	}
	if request.CurrentObjectiveState != "" {
		if err := validateScenarioLearnerProgressState(request.CurrentObjectiveState); err != nil {
			return err
		}
	}
	return nil
}

func normalizeScenarioLearnerCheckResultRecordRequest(request ScenarioLearnerCheckResultRecordRequest) ScenarioLearnerCheckResultRecordRequest {
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.EvaluatedAt = strings.TrimSpace(request.EvaluatedAt)
	for idx := range request.Results {
		request.Results[idx].ScenarioRunID = strings.TrimSpace(request.Results[idx].ScenarioRunID)
		request.Results[idx].CheckID = strings.TrimSpace(request.Results[idx].CheckID)
		request.Results[idx].CheckType = strings.TrimSpace(request.Results[idx].CheckType)
		request.Results[idx].Status = strings.TrimSpace(request.Results[idx].Status)
		request.Results[idx].Expected = strings.TrimSpace(request.Results[idx].Expected)
		request.Results[idx].Actual = strings.TrimSpace(request.Results[idx].Actual)
		request.Results[idx].Message = strings.TrimSpace(request.Results[idx].Message)
		request.Results[idx].EvaluatedAt = strings.TrimSpace(request.Results[idx].EvaluatedAt)
	}
	return request
}

func validateScenarioLearnerCheckResultRecordRequest(request ScenarioLearnerCheckResultRecordRequest) error {
	if request.ScenarioRunID == "" {
		return fmt.Errorf("scenario run ID is required")
	}
	for _, result := range request.Results {
		if result.CheckID == "" {
			return fmt.Errorf("scenario learner check ID is required")
		}
		if result.CheckSequence <= 0 {
			return fmt.Errorf("scenario learner check sequence must be positive")
		}
		if result.CheckType == "" {
			return fmt.Errorf("scenario learner check type is required")
		}
		switch result.Status {
		case "passed", "failed":
		default:
			return fmt.Errorf("scenario learner check status %q is not supported", result.Status)
		}
	}
	return nil
}

func validateScenarioLearnerProgressState(state string) error {
	switch state {
	case ScenarioProgressStateInProgress, ScenarioProgressStateCompleted, ScenarioProgressStateNeedsReview, ScenarioProgressStateFailed:
		return nil
	default:
		return fmt.Errorf("scenario learner progress state %q is not supported", state)
	}
}

func defaultScenarioLearnerCurrentObjective(state string) string {
	switch state {
	case ScenarioProgressStateCompleted:
		return "Scenario complete"
	case ScenarioProgressStateNeedsReview:
		return "Review failed scenario checks"
	case ScenarioProgressStateFailed:
		return "Resolve scenario setup failure"
	default:
		return "Continue scenario workflow"
	}
}

func ensureRowsAffected(result sql.Result, label, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s update result for %q: %w", label, id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%s %q not found", label, id)
	}
	return nil
}
