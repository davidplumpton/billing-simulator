package scenario

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestRunnerAppliesScenarioEventsDeterministically(t *testing.T) {
	t.Parallel()

	first := runScenarioFixture(t)
	second := runScenarioFixture(t)

	if first.result.Run.ID == "" || first.result.Run.ID != second.result.Run.ID {
		t.Fatalf("scenario run IDs = %q/%q, want stable nonblank ID", first.result.Run.ID, second.result.Run.ID)
	}
	if first.s3ResourceID == "" || first.s3ResourceID != second.s3ResourceID {
		t.Fatalf("S3 resource IDs = %q/%q, want deterministic resource ID", first.s3ResourceID, second.s3ResourceID)
	}
	if first.dataTransferUsageID == "" || first.dataTransferUsageID != second.dataTransferUsageID {
		t.Fatalf("data transfer usage IDs = %q/%q, want deterministic usage ID", first.dataTransferUsageID, second.dataTransferUsageID)
	}
}

func TestPackagedScenarioSeedsParse(t *testing.T) {
	t.Parallel()

	keys, err := SeedDefinitionKeys()
	if err != nil {
		t.Fatalf("SeedDefinitionKeys() error = %v", err)
	}
	if !containsScenarioSeedKey(keys, UntaggedDataTransferSpikeSeedKey) {
		t.Fatalf("SeedDefinitionKeys() = %v, want %q present", keys, UntaggedDataTransferSpikeSeedKey)
	}
	definition, err := LoadSeedDefinition(UntaggedDataTransferSpikeSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition() error = %v", err)
	}
	if definition.Name != "Find the untagged data-transfer spike" || len(definition.Events) != 5 {
		t.Fatalf("packaged scenario definition = %+v, want MVP data-transfer spike fixture", definition)
	}
}

func containsScenarioSeedKey(keys []string, want string) bool {
	for _, key := range keys {
		if key == want {
			return true
		}
	}
	return false
}

func TestRunnerAllowsSameDefinitionRerunInOneWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition := parseScenarioDefinitionForTest(t, `{
		"name": "Rerunnable explicit resource scenario",
		"clock": {"start": "2026-03-01"},
		"organization_template": "anycompany-retail",
		"random_seed": 7,
		"events": [
			{
				"id": "create-assets",
				"day": 1,
				"action": "create_resource",
				"account": "Storefront Prod",
				"service": "Amazon S3",
				"resource_id": "scenario-assets",
				"resource": "s3://scenario-assets"
			},
			{
				"id": "generate-assets",
				"day": 2,
				"action": "generate_usage",
				"resource_id": "scenario-assets",
				"pattern": "storage_growth",
				"days": 1
			}
		]
	}`)

	runner := NewRunner(db)
	first, err := runner.Run(ctx, definition)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	second, err := runner.Run(ctx, definition)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	if first.Run.ID == "" || second.Run.ID == "" || first.Run.ID == second.Run.ID {
		t.Fatalf("scenario run IDs = %q/%q, want distinct durable attempts", first.Run.ID, second.Run.ID)
	}
	if first.ResourcesCreated != 1 || first.UsageEventsCreated != 1 || second.ResourcesCreated != 1 || second.UsageEventsCreated != 1 {
		t.Fatalf("run counts = first %+v second %+v, want one resource and one usage event per attempt", first, second)
	}

	var runCount, eventCount, resourceCount, usageCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs WHERE definition_name = ?`, definition.Name).Scan(&runCount); err != nil {
		t.Fatalf("count scenario runs: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_run_events WHERE scenario_event_id = 'generate-assets'`).Scan(&eventCount); err != nil {
		t.Fatalf("count scenario run events: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM resources WHERE scenario_event_id = 'create-assets'`).Scan(&resourceCount); err != nil {
		t.Fatalf("count scenario resources: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE scenario_event_id = 'generate-assets'`).Scan(&usageCount); err != nil {
		t.Fatalf("count scenario usage events: %v", err)
	}
	if runCount != 2 || eventCount != 2 || resourceCount != 2 || usageCount != 2 {
		t.Fatalf("audit/domain counts = runs:%d events:%d resources:%d usage:%d, want all 2", runCount, eventCount, resourceCount, usageCount)
	}
}

func TestRunnerDistinguishesSameHeaderDefinitionsWithDifferentBodies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	firstDefinition := parseScenarioDefinitionForTest(t, sameHeaderScenarioDefinition("1"))
	secondDefinition := parseScenarioDefinitionForTest(t, sameHeaderScenarioDefinition("2"))
	if scenarioRunID(firstDefinition, 1) == scenarioRunID(secondDefinition, 1) {
		t.Fatalf("first-attempt run IDs matched for different definition bodies: %q", scenarioRunID(firstDefinition, 1))
	}

	runner := NewRunner(db)
	first, err := runner.Run(ctx, firstDefinition)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	second, err := runner.Run(ctx, secondDefinition)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if first.Run.ID == second.Run.ID {
		t.Fatalf("scenario run IDs = %q/%q, want different IDs for changed event bodies", first.Run.ID, second.Run.ID)
	}

	var runCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs WHERE definition_name = ?`, firstDefinition.Name).Scan(&runCount); err != nil {
		t.Fatalf("count same-header scenario runs: %v", err)
	}
	if runCount != 2 {
		t.Fatalf("same-header scenario runs = %d, want 2 durable audit rows", runCount)
	}
}

func TestRunnerRecordsFailedExecutionEventAndRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := ParseDefinitionBytes([]byte(`{
		"name": "Missing resource scenario",
		"clock": {"start": "2026-03-01"},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "generate-missing",
				"day": 1,
				"action": "generate_usage",
				"resource": "s3://missing-assets",
				"pattern": "storage_growth",
				"days": 1
			}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseDefinitionBytes() error = %v", err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err == nil || !strings.Contains(err.Error(), "was not created before generate_usage") {
		t.Fatalf("Run() error = %v, want missing resource execution error", err)
	}
	if result.Run.Status != scenarioRunStatusFailed || result.Run.CurrentEventID != "generate-missing" {
		t.Fatalf("failed run = %+v, want failed status at generate-missing", result.Run)
	}

	var runStatus, eventStatus, errorMessage string
	if err := db.QueryRowContext(ctx, `SELECT status, error_message FROM scenario_runs WHERE id = ?`, result.Run.ID).Scan(&runStatus, &errorMessage); err != nil {
		t.Fatalf("read failed scenario run: %v", err)
	}
	if runStatus != scenarioRunStatusFailed || !strings.Contains(errorMessage, "was not created before generate_usage") {
		t.Fatalf("persisted failed run = %q/%q, want failed missing-resource message", runStatus, errorMessage)
	}
	if err := db.QueryRowContext(ctx, `SELECT status, error_message FROM scenario_run_events WHERE scenario_run_id = ? AND scenario_event_id = ?`, result.Run.ID, "generate-missing").Scan(&eventStatus, &errorMessage); err != nil {
		t.Fatalf("read failed scenario event: %v", err)
	}
	if eventStatus != scenarioRunStatusFailed || !strings.Contains(errorMessage, "was not created before generate_usage") {
		t.Fatalf("persisted failed event = %q/%q, want failed missing-resource message", eventStatus, errorMessage)
	}
}

type scenarioFixtureResult struct {
	result              RunResult
	s3ResourceID        string
	dataTransferUsageID string
}

func runScenarioFixture(t *testing.T) scenarioFixtureResult {
	t.Helper()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(UntaggedDataTransferSpikeSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition() error = %v", err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Run.Status != scenarioRunStatusSucceeded ||
		result.Run.EventsSucceeded != 5 ||
		result.ResourcesCreated != 2 ||
		result.UsageEventsCreated != 3 ||
		result.BillsIssued != 1 {
		t.Fatalf("Run() result = %+v, want successful scenario with resource, usage, and bill counts", result)
	}

	var runStatus string
	var eventsSucceeded, resourcesCreated, usageEventsCreated, billsIssued int
	if err := db.QueryRowContext(ctx, `SELECT status, events_succeeded, resources_created, usage_events_created, bills_issued FROM scenario_runs WHERE id = ?`, result.Run.ID).Scan(&runStatus, &eventsSucceeded, &resourcesCreated, &usageEventsCreated, &billsIssued); err != nil {
		t.Fatalf("read scenario run audit: %v", err)
	}
	if runStatus != scenarioRunStatusSucceeded || eventsSucceeded != 5 || resourcesCreated != 2 || usageEventsCreated != 3 || billsIssued != 1 {
		t.Fatalf("scenario run audit = %q/%d/%d/%d/%d, want succeeded/5/2/3/1", runStatus, eventsSucceeded, resourcesCreated, usageEventsCreated, billsIssued)
	}

	var succeededEvents int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_run_events WHERE scenario_run_id = ? AND status = 'succeeded'`, result.Run.ID).Scan(&succeededEvents); err != nil {
		t.Fatalf("count scenario events: %v", err)
	}
	if succeededEvents != 5 {
		t.Fatalf("succeeded scenario events = %d, want 5", succeededEvents)
	}

	var s3ResourceID, s3AccountID, s3EventSource, s3ScenarioRunID, s3ScenarioEventID string
	if err := db.QueryRowContext(ctx, `SELECT id, account_id, event_source, scenario_run_id, scenario_event_id FROM resources WHERE scenario_run_id = ? AND scenario_event_id = ?`, result.Run.ID, "create-assets").Scan(&s3ResourceID, &s3AccountID, &s3EventSource, &s3ScenarioRunID, &s3ScenarioEventID); err != nil {
		t.Fatalf("read scenario-created S3 resource: %v", err)
	}
	if s3AccountID != "111122223333" || s3EventSource != "scenario" || s3ScenarioRunID != result.Run.ID || s3ScenarioEventID != "create-assets" {
		t.Fatalf("S3 resource audit = %q/%q/%q/%q, want AnyCompany Storefront Prod scenario lineage", s3AccountID, s3EventSource, s3ScenarioRunID, s3ScenarioEventID)
	}

	var generatedUsageCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE scenario_run_id = ? AND scenario_event_id = ? AND event_source = 'scenario'`, result.Run.ID, "generate-assets").Scan(&generatedUsageCount); err != nil {
		t.Fatalf("count generated scenario usage: %v", err)
	}
	if generatedUsageCount != 2 {
		t.Fatalf("generated scenario usage count = %d, want 2", generatedUsageCount)
	}

	var dataTransferUsageID, dataTransferAccountID, dataTransferServiceCode, dataTransferUsageType, dataTransferOperation, dataTransferRegionCode string
	var dataTransferQuantityMicros int64
	if err := db.QueryRowContext(ctx, `SELECT id, account_id, service_code, usage_type, operation, region_code, usage_quantity_micros FROM usage_events WHERE scenario_run_id = ? AND scenario_event_id = ?`, result.Run.ID, "data-transfer-spike").Scan(&dataTransferUsageID, &dataTransferAccountID, &dataTransferServiceCode, &dataTransferUsageType, &dataTransferOperation, &dataTransferRegionCode, &dataTransferQuantityMicros); err != nil {
		t.Fatalf("read data transfer usage: %v", err)
	}
	if dataTransferAccountID != "222233334444" ||
		dataTransferServiceCode != "AWSDataTransfer" ||
		dataTransferUsageType != "data-transfer-out-internet-gb" ||
		dataTransferOperation != "DataTransferOut" ||
		dataTransferRegionCode != "global" ||
		dataTransferQuantityMicros != 4_000_000_000 {
		t.Fatalf("data transfer usage = %q/%q/%q/%q/%q/%d, want shared-networking AWSDataTransfer 4000 GB", dataTransferAccountID, dataTransferServiceCode, dataTransferUsageType, dataTransferOperation, dataTransferRegionCode, dataTransferQuantityMicros)
	}

	var closeBillID string
	if err := db.QueryRowContext(ctx, `SELECT bill_id FROM scenario_run_events WHERE scenario_run_id = ? AND scenario_event_id = ?`, result.Run.ID, "close-march").Scan(&closeBillID); err != nil {
		t.Fatalf("read close event bill ID: %v", err)
	}
	if closeBillID == "" {
		t.Fatal("close event bill ID is blank, want issued bill lineage")
	}

	return scenarioFixtureResult{
		result:              result,
		s3ResourceID:        s3ResourceID,
		dataTransferUsageID: dataTransferUsageID,
	}
}

func parseScenarioDefinitionForTest(t *testing.T, raw string) Definition {
	t.Helper()

	definition, err := ParseDefinitionBytes([]byte(raw))
	if err != nil {
		t.Fatalf("ParseDefinitionBytes() error = %v", err)
	}
	return definition
}

func sameHeaderScenarioDefinition(amountGB string) string {
	return `{
		"name": "Same header changed body scenario",
		"clock": {"start": "2026-03-01"},
		"organization_template": "anycompany-retail",
		"random_seed": 12,
		"events": [
			{
				"id": "data-transfer",
				"day": 1,
				"action": "add_usage",
				"account": "Shared Networking",
				"service": "AWS Data Transfer",
				"amount_gb": ` + amountGB + `
			}
		]
	}`
}

func openScenarioTestWorkspace(t *testing.T) *sql.DB {
	t.Helper()

	db, err := persistence.OpenWorkspace(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return db
}
