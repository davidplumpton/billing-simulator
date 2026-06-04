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

func TestRunnerRecordsFailedEventAndRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := ParseDefinitionBytes([]byte(`{
		"name": "Unsupported service scenario",
		"clock": {"start": "2026-03-01"},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "create-unknown",
				"day": 1,
				"action": "create_resource",
				"account": "Storefront Prod",
				"service": "Unknown Service",
				"resource": "Unsupported"
			}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseDefinitionBytes() error = %v", err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Run() error = %v, want unsupported service error", err)
	}
	if result.Run.Status != scenarioRunStatusFailed || result.Run.CurrentEventID != "create-unknown" {
		t.Fatalf("failed run = %+v, want failed status at create-unknown", result.Run)
	}

	var runStatus, eventStatus, errorMessage string
	if err := db.QueryRowContext(ctx, `SELECT status, error_message FROM scenario_runs WHERE id = ?`, result.Run.ID).Scan(&runStatus, &errorMessage); err != nil {
		t.Fatalf("read failed scenario run: %v", err)
	}
	if runStatus != scenarioRunStatusFailed || !strings.Contains(errorMessage, "not supported") {
		t.Fatalf("persisted failed run = %q/%q, want failed unsupported-service message", runStatus, errorMessage)
	}
	if err := db.QueryRowContext(ctx, `SELECT status, error_message FROM scenario_run_events WHERE scenario_run_id = ? AND scenario_event_id = ?`, result.Run.ID, "create-unknown").Scan(&eventStatus, &errorMessage); err != nil {
		t.Fatalf("read failed scenario event: %v", err)
	}
	if eventStatus != scenarioRunStatusFailed || !strings.Contains(errorMessage, "not supported") {
		t.Fatalf("persisted failed event = %q/%q, want failed unsupported-service message", eventStatus, errorMessage)
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
	definition, err := ParseDefinitionBytes([]byte(`{
		"name": "Find the untagged data-transfer spike",
		"clock": {"start": "2026-03-01"},
		"organization_template": "anycompany-retail",
		"random_seed": 42,
		"events": [
			{
				"id": "create-assets",
				"day": 3,
				"action": "create_resource",
				"account": "Storefront Prod",
				"service": "Amazon S3",
				"resource": "s3://storefront-assets",
				"resource_type": "bucket",
				"region": "us-east-1",
				"tags": {
					"app": "storefront",
					"env": "prod",
					"owner": "web-platform"
				},
				"attributes": {
					"storage_class": "standard"
				}
			},
			{
				"id": "generate-assets",
				"day": 4,
				"action": "generate_usage",
				"resource": "s3://storefront-assets",
				"pattern": "storage_growth",
				"days": 2
			},
			{
				"id": "data-transfer-spike",
				"day": 12,
				"action": "add_usage",
				"service": "AWS Data Transfer",
				"account": "Shared Networking",
				"amount_gb": 4000,
				"tags": {}
			},
			{
				"id": "meter-march",
				"day": 32,
				"action": "run_daily_metering",
				"payer_account": "Management"
			},
			{
				"id": "close-march",
				"day": 33,
				"action": "close_billing_period",
				"payer_account": "Management",
				"billing_period_start": "2026-03-01",
				"billing_period_end": "2026-04-01"
			}
		]
	}`))
	if err != nil {
		t.Fatalf("ParseDefinitionBytes() error = %v", err)
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
