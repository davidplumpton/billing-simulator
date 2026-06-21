package scenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

// startScenarioRun reserves the audit header and learner progress row before domain mutations begin.
func (r Runner) startScenarioRun(ctx context.Context, definition Definition, startTime time.Time) (ScenarioRun, error) {
	catalogCompatibility, err := persistence.NewPriceCatalogRepository(r.db).ScenarioCompatibility(ctx, startTime.Format(time.RFC3339))
	if err != nil {
		return ScenarioRun{}, fmt.Errorf("resolve scenario price catalog compatibility: %w", err)
	}
	run := ScenarioRun{
		DefinitionName:                   definition.Name,
		OrganizationTemplate:             definition.OrganizationTemplate,
		RandomSeed:                       definition.RandomSeed,
		Status:                           scenarioRunStatusRunning,
		ClockStart:                       startTime.Format(time.RFC3339),
		EventsTotal:                      len(definition.Events),
		PriceCatalogID:                   catalogCompatibility.Catalog.ID,
		PriceCatalogSourceURL:            catalogCompatibility.Catalog.SourceURL,
		PriceCatalogFetchDate:            catalogCompatibility.Catalog.FetchDate,
		PriceCatalogEffectiveDate:        catalogCompatibility.Catalog.EffectiveDate,
		PriceCatalogSupportedRegions:     strings.Join(catalogCompatibility.Catalog.SupportedRegions, ","),
		PriceCatalogCompatibilityKey:     catalogCompatibility.Catalog.CompatibilityKey,
		PriceCatalogCompatibilityStatus:  catalogCompatibility.Status,
		PriceCatalogCompatibilityMessage: catalogCompatibility.Message,
	}
	run, err = r.createScenarioRun(ctx, run, definition)
	if err != nil {
		return ScenarioRun{}, err
	}
	if _, err := r.progress.StartRun(ctx, persistence.ScenarioLearnerProgressStartRequest{
		ScenarioRunID:    run.ID,
		DefinitionName:   definition.Name,
		Objective:        definition.Name,
		CurrentObjective: initialScenarioProgressObjective(definition),
		ActionsTotal:     len(definition.Events),
		ChecksTotal:      len(definition.Checks),
		StartedAt:        run.ClockStart,
	}); err != nil {
		return ScenarioRun{}, err
	}
	return run, nil
}

// prepareScenarioWorkspace resets scenario-owned workspace state and pins the clock to the scenario start.
func (r Runner) prepareScenarioWorkspace(ctx context.Context, definition Definition, startTime time.Time) error {
	if _, err := r.organization.ResetOrganizationTemplate(ctx, definition.OrganizationTemplate); err != nil {
		return fmt.Errorf("reset scenario organization template: %w", err)
	}
	if _, err := r.clock.Set(ctx, startTime.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("set scenario start clock: %w", err)
	}
	return nil
}

// newScenarioExecutionState initializes alias tracking for the apply path.
func newScenarioExecutionState(runID string, definition Definition, startTime time.Time) scenarioExecutionState {
	return scenarioExecutionState{
		runID:                runID,
		definition:           definition,
		startTime:            startTime,
		accountAliasesByKey:  map[string]string{},
		resourceAliasesByKey: map[string]string{},
		categoryAliasesByKey: map[string]string{},
	}
}

// recordScenarioEventOutcome persists event audit and learner progress rows for one attempted action.
func (r Runner) recordScenarioEventOutcome(ctx context.Context, event Event, eventAudit ScenarioRunEvent, applyErr error) error {
	if applyErr != nil {
		if insertErr := r.insertScenarioRunEvent(ctx, eventAudit); insertErr != nil {
			applyErr = fmt.Errorf("%w; record failed scenario event: %v", applyErr, insertErr)
		}
		if progressErr := r.recordLearnerProgressAction(ctx, eventAudit); progressErr != nil {
			applyErr = fmt.Errorf("%w; record failed learner action: %v", applyErr, progressErr)
		}
		return applyErr
	}
	if err := r.insertScenarioRunEvent(ctx, eventAudit); err != nil {
		return fmt.Errorf("record scenario event %q: %w", event.ID, err)
	}
	if err := r.recordLearnerProgressAction(ctx, eventAudit); err != nil {
		return fmt.Errorf("record scenario learner action %q: %w", event.ID, err)
	}
	return nil
}

// completeSuccessfulRun writes final audit totals and transitions learner progress to the next objective.
func (r Runner) completeSuccessfulRun(ctx context.Context, result *RunResult, definition Definition) error {
	result.Run.Status = scenarioRunStatusSucceeded
	result.copyEventCountsToRun()
	if err := r.completeScenarioRun(ctx, result.Run); err != nil {
		return err
	}
	completedAt, err := r.learnerProgressCompletionAt(ctx, *result)
	if err != nil {
		return err
	}
	if err := r.completeLearnerProgress(ctx, result.Run, definition, completedAt); err != nil {
		return err
	}
	return nil
}

// addEventAudit accumulates per-event counts into the in-memory run result.
func (result *RunResult) addEventAudit(eventAudit ScenarioRunEvent) {
	result.Events = append(result.Events, eventAudit)
	result.ResourcesCreated += eventAudit.ResourcesCreated
	result.UsageEventsCreated += eventAudit.UsageEventsCreated
	result.MeteringRecordsCreated += eventAudit.MeteringRecordsCreated
	result.BillLineItemsCreated += eventAudit.BillLineItemsCreated
	result.BillsIssued += eventAudit.BillsIssued
	result.Run.CurrentEventID = eventAudit.ScenarioEventID
}

// copyEventCountsToRun stores accumulated event totals on the durable run header.
func (result *RunResult) copyEventCountsToRun() {
	result.Run.ResourcesCreated = result.ResourcesCreated
	result.Run.UsageEventsCreated = result.UsageEventsCreated
	result.Run.MeteringRecordsCreated = result.MeteringRecordsCreated
	result.Run.BillLineItemsCreated = result.BillLineItemsCreated
	result.Run.BillsIssued = result.BillsIssued
}

// createScenarioRun reserves a durable per-attempt run ID before events mutate workspace state.
func (r Runner) createScenarioRun(ctx context.Context, run ScenarioRun, definition Definition) (ScenarioRun, error) {
	for attempt := 1; attempt <= maxScenarioRunIDAttempts; attempt++ {
		candidate := run
		candidate.ID = scenarioRunID(definition, attempt)
		exists, err := r.scenarioRunExists(ctx, candidate.ID)
		if err != nil {
			return ScenarioRun{}, err
		}
		if exists {
			continue
		}
		if err := r.insertScenarioRun(ctx, candidate); err != nil {
			return ScenarioRun{}, err
		}
		return candidate, nil
	}
	return ScenarioRun{}, fmt.Errorf("scenario run ID attempts exhausted for definition %q", definition.Name)
}

// scenarioRunExists checks whether an audit run ID is already reserved in this workspace.
func (r Runner) scenarioRunExists(ctx context.Context, runID string) (bool, error) {
	var existing string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM scenario_runs WHERE id = ?`, runID).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("check scenario run %q: %w", runID, err)
}

// insertScenarioRun persists the scenario run audit header.
func (r Runner) insertScenarioRun(ctx context.Context, run ScenarioRun) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO scenario_runs (
			id,
			definition_name,
			organization_template,
			random_seed,
			status,
			clock_start,
			events_total,
			price_catalog_id,
			price_catalog_source_url,
			price_catalog_fetch_date,
			price_catalog_effective_date,
			price_catalog_supported_regions,
			price_catalog_compatibility_key,
			price_catalog_compatibility_status,
			price_catalog_compatibility_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID,
		run.DefinitionName,
		run.OrganizationTemplate,
		run.RandomSeed,
		run.Status,
		run.ClockStart,
		run.EventsTotal,
		run.PriceCatalogID,
		run.PriceCatalogSourceURL,
		run.PriceCatalogFetchDate,
		run.PriceCatalogEffectiveDate,
		run.PriceCatalogSupportedRegions,
		run.PriceCatalogCompatibilityKey,
		run.PriceCatalogCompatibilityStatus,
		run.PriceCatalogCompatibilityMessage,
	)
	if err != nil {
		return fmt.Errorf("insert scenario run %q: %w", run.ID, err)
	}
	return nil
}

// insertScenarioRunEvent persists one scenario event audit row.
func (r Runner) insertScenarioRunEvent(ctx context.Context, event ScenarioRunEvent) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO scenario_run_events (
			id,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence,
			action,
			scheduled_at,
			status,
			resource_id,
			usage_event_id,
			generated_usage_event_count,
			metering_records_created,
			bill_line_items_created,
			bill_id,
			error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.ScenarioRunID,
		event.ScenarioEventID,
		event.ScenarioEventSequence,
		string(event.Action),
		event.ScheduledAt,
		event.Status,
		event.ResourceID,
		event.UsageEventID,
		event.GeneratedUsageEventCount,
		event.MeteringRecordsCreated,
		event.BillLineItemsCreated,
		event.BillID,
		event.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("insert scenario event %q: %w", event.ScenarioEventID, err)
	}
	return nil
}

// completeScenarioRun writes final status, counts, and error text for the audit header.
func (r Runner) completeScenarioRun(ctx context.Context, run ScenarioRun) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE scenario_runs
		 SET status = ?,
			current_event_id = ?,
			events_succeeded = ?,
			resources_created = ?,
			usage_events_created = ?,
			metering_records_created = ?,
			bill_line_items_created = ?,
			bills_issued = ?,
			error_message = ?,
			completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		run.Status,
		run.CurrentEventID,
		run.EventsSucceeded,
		run.ResourcesCreated,
		run.UsageEventsCreated,
		run.MeteringRecordsCreated,
		run.BillLineItemsCreated,
		run.BillsIssued,
		run.ErrorMessage,
		run.ID,
	)
	if err != nil {
		return fmt.Errorf("complete scenario run %q: %w", run.ID, err)
	}
	return nil
}

// recordLearnerProgressAction mirrors an event audit row into learner progress history.
func (r Runner) recordLearnerProgressAction(ctx context.Context, event ScenarioRunEvent) error {
	status := persistence.ScenarioLearnerActionStatusCompleted
	if event.Status == scenarioRunStatusFailed {
		status = persistence.ScenarioLearnerActionStatusFailed
	}
	_, err := r.progress.RecordAction(ctx, persistence.ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  event.ScenarioRunID,
		ActionID:       event.ScenarioEventID,
		ActionSequence: event.ScenarioEventSequence,
		ActionType:     string(event.Action),
		ActionStatus:   status,
		CompletedAt:    event.ScheduledAt,
		Evidence:       scenarioLearnerActionEvidence(event),
		ErrorMessage:   event.ErrorMessage,
	})
	return err
}

// completeLearnerProgress marks scenario setup done or points the learner at assessment checks.
func (r Runner) completeLearnerProgress(ctx context.Context, run ScenarioRun, definition Definition, completedAt string) error {
	state := persistence.ScenarioProgressStateCompleted
	currentObjective := "Scenario setup complete"
	if len(definition.Checks) > 0 {
		state = persistence.ScenarioProgressStateInProgress
		currentObjective = "Run scenario assessment checks"
	}
	if _, err := r.progress.CompleteRun(ctx, persistence.ScenarioLearnerRunCompleteRequest{
		ScenarioRunID:         run.ID,
		RunStatus:             run.Status,
		CurrentObjectiveState: state,
		CurrentObjective:      currentObjective,
		CompletedAt:           completedAt,
	}); err != nil {
		return fmt.Errorf("complete scenario learner progress %q: %w", run.ID, err)
	}
	return nil
}

// learnerProgressCompletionAt returns a deterministic scenario timestamp for progress completion.
func (r Runner) learnerProgressCompletionAt(ctx context.Context, result RunResult) (string, error) {
	if len(result.Events) == 0 {
		return result.Run.ClockStart, nil
	}
	clock, err := r.clock.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("read scenario learner progress completion time: %w", err)
	}
	if currentTime := strings.TrimSpace(clock.CurrentTime); currentTime != "" {
		return currentTime, nil
	}
	return result.learnerProgressFallbackCompletionAt(), nil
}

// learnerProgressFallbackCompletionAt uses audit timestamps when the simulator clock has no value.
func (result RunResult) learnerProgressFallbackCompletionAt() string {
	for idx := len(result.Events) - 1; idx >= 0; idx-- {
		if scheduledAt := strings.TrimSpace(result.Events[idx].ScheduledAt); scheduledAt != "" {
			return scheduledAt
		}
	}
	return result.Run.ClockStart
}

// failRun finalizes audit and learner progress rows for a failed scenario attempt.
func (r Runner) failRun(ctx context.Context, result RunResult, currentEventID string, runErr error) (RunResult, error) {
	result.Run.Status = scenarioRunStatusFailed
	result.Run.CurrentEventID = currentEventID
	result.copyEventCountsToRun()
	result.Run.ErrorMessage = runErr.Error()
	if err := r.completeScenarioRun(ctx, result.Run); err != nil {
		return result, fmt.Errorf("%w; complete failed scenario run: %v", runErr, err)
	}
	completedAt, progressTimeErr := r.learnerProgressCompletionAt(ctx, result)
	if progressTimeErr != nil {
		return result, fmt.Errorf("%w; resolve failed learner progress completion time: %v", runErr, progressTimeErr)
	}
	if _, err := r.progress.CompleteRun(ctx, persistence.ScenarioLearnerRunCompleteRequest{
		ScenarioRunID:         result.Run.ID,
		RunStatus:             result.Run.Status,
		CurrentObjectiveState: persistence.ScenarioProgressStateFailed,
		CurrentObjective:      "Resolve scenario setup failure",
		CompletedAt:           completedAt,
	}); err != nil {
		return result, fmt.Errorf("%w; complete failed learner progress: %v", runErr, err)
	}
	return result, runErr
}

func failedScenarioRunEvent(runID string, event Event, scheduledAt time.Time, err error) ScenarioRunEvent {
	return ScenarioRunEvent{
		ID:                    scenarioRunEventID(runID, event),
		ScenarioRunID:         runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
		Action:                event.Action,
		ScheduledAt:           scheduledAt.Format(time.RFC3339),
		Status:                scenarioRunStatusFailed,
		ErrorMessage:          err.Error(),
	}
}

func failScenarioRunEvent(event ScenarioRunEvent, err error) (ScenarioRunEvent, error) {
	event.Status = scenarioRunStatusFailed
	event.ErrorMessage = err.Error()
	return event, err
}

func initialScenarioProgressObjective(definition Definition) string {
	if len(definition.Events) > 0 {
		return "Complete scenario action " + definition.Events[0].ID
	}
	if len(definition.Checks) > 0 {
		return "Run scenario assessment checks"
	}
	return definition.Name
}

func scenarioLearnerActionEvidence(event ScenarioRunEvent) string {
	parts := []string{}
	if event.ResourceID != "" {
		parts = append(parts, "resource="+event.ResourceID)
	}
	if event.UsageEventID != "" {
		parts = append(parts, "usage_event="+event.UsageEventID)
	}
	if event.GeneratedUsageEventCount > 0 {
		parts = append(parts, "generated_usage_events="+strconv.Itoa(event.GeneratedUsageEventCount))
	}
	if event.MeteringRecordsCreated > 0 {
		parts = append(parts, "metering_records="+strconv.Itoa(event.MeteringRecordsCreated))
	}
	if event.BillLineItemsCreated > 0 {
		parts = append(parts, "bill_line_items="+strconv.Itoa(event.BillLineItemsCreated))
	}
	if event.BillID != "" {
		parts = append(parts, "bill="+event.BillID)
	}
	if len(parts) == 0 {
		return string(event.Action)
	}
	return strings.Join(parts, ", ")
}
