package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestResourceUsageSchemaSupportsResourceTagsUsageAndScenarioProvenance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	insertTestResource(t, db, "resource-ec2-1")
	if _, err := db.ExecContext(ctx, `INSERT INTO resource_tags (
		id,
		resource_id,
		tag_key,
		tag_value,
		applied_at,
		event_source,
		scenario_run_id,
		scenario_event_id,
		scenario_event_sequence
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"tag-1",
		"resource-ec2-1",
		"app",
		"storefront",
		"2026-02-01T00:00:00Z",
		"scenario",
		"scenario-run-1",
		"event-tag-app",
		2,
	); err != nil {
		t.Fatalf("insert resource tag: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_events (
		id,
		resource_id,
		account_id,
		service_code,
		usage_type,
		operation,
		region_code,
		usage_start_time,
		usage_end_time,
		usage_quantity_micros,
		usage_unit,
		attributes_json,
		tag_snapshot_json,
		event_source,
		scenario_run_id,
		scenario_event_id,
		scenario_event_sequence
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"usage-1",
		"resource-ec2-1",
		"111122223333",
		"AmazonEC2",
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		"2026-02-01T00:00:00Z",
		"2026-02-01T01:00:00Z",
		int64(1_000_000),
		"Hours",
		`{"state":"running","instance_type":"t3.medium"}`,
		`{"app":"storefront"}`,
		"scenario",
		"scenario-run-1",
		"event-usage-hour",
		3,
	); err != nil {
		t.Fatalf("insert usage event: %v", err)
	}

	var resourceInstanceType, usageInstanceType, usageTagApp, tagScenarioRunID string
	var usageQuantityMicros int64
	if err := db.QueryRowContext(ctx, `SELECT
		json_extract(r.attributes_json, '$.instance_type'),
		json_extract(u.attributes_json, '$.instance_type'),
		json_extract(u.tag_snapshot_json, '$.app'),
		u.usage_quantity_micros,
		t.scenario_run_id
	FROM usage_events u
	JOIN resources r ON r.id = u.resource_id
	JOIN resource_tags t ON t.resource_id = r.id AND t.tag_key = 'app'
	WHERE u.id = ?`,
		"usage-1",
	).Scan(
		&resourceInstanceType,
		&usageInstanceType,
		&usageTagApp,
		&usageQuantityMicros,
		&tagScenarioRunID,
	); err != nil {
		t.Fatalf("read joined resource usage event: %v", err)
	}
	if resourceInstanceType != "t3.medium" || usageInstanceType != "t3.medium" || usageTagApp != "storefront" {
		t.Fatalf("resource/usage JSON values = %q/%q/%q, want t3.medium/t3.medium/storefront", resourceInstanceType, usageInstanceType, usageTagApp)
	}
	if usageQuantityMicros != 1_000_000 {
		t.Fatalf("usage_quantity_micros = %d, want 1000000", usageQuantityMicros)
	}
	if tagScenarioRunID != "scenario-run-1" {
		t.Fatalf("tag scenario_run_id = %q, want scenario-run-1", tagScenarioRunID)
	}
}

func TestResourceUsageSchemaRejectsInvalidRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	insertTestResource(t, db, "resource-ec2-1")

	assertExecFails(t, db, `INSERT INTO resources (
		id,
		account_id,
		region_code,
		service_code,
		resource_type,
		status,
		attributes_json
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"resource-invalid-json",
		"111122223333",
		"us-east-1",
		"AmazonEC2",
		"ec2_instance",
		"active",
		`[]`,
	)
	assertExecFails(t, db, `INSERT INTO resources (
		id,
		account_id,
		region_code,
		service_code,
		resource_type,
		status,
		event_source
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"resource-missing-provenance",
		"111122223333",
		"us-east-1",
		"AmazonEC2",
		"ec2_instance",
		"active",
		"scenario",
	)

	if _, err := db.ExecContext(ctx, `INSERT INTO resource_tags (
		id,
		resource_id,
		tag_key,
		tag_value,
		applied_at
	) VALUES (?, ?, ?, ?, ?)`,
		"tag-1",
		"resource-ec2-1",
		"owner",
		"platform",
		"2026-02-01T00:00:00Z",
	); err != nil {
		t.Fatalf("insert initial resource tag: %v", err)
	}
	assertExecFails(t, db, `INSERT INTO resource_tags (
		id,
		resource_id,
		tag_key,
		tag_value,
		applied_at
	) VALUES (?, ?, ?, ?, ?)`,
		"tag-duplicate",
		"resource-ec2-1",
		"owner",
		"finance",
		"2026-02-01T01:00:00Z",
	)

	assertExecFails(t, db, `INSERT INTO usage_events (
		id,
		resource_id,
		account_id,
		service_code,
		usage_type,
		operation,
		region_code,
		usage_start_time,
		usage_end_time,
		usage_quantity_micros,
		usage_unit
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"usage-missing-resource",
		"resource-does-not-exist",
		"111122223333",
		"AmazonEC2",
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		"2026-02-01T00:00:00Z",
		"2026-02-01T01:00:00Z",
		int64(1_000_000),
		"Hours",
	)
	assertExecFails(t, db, `INSERT INTO usage_events (
		id,
		resource_id,
		account_id,
		service_code,
		usage_type,
		operation,
		region_code,
		usage_start_time,
		usage_end_time,
		usage_quantity_micros,
		usage_unit
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"usage-zero-quantity",
		"resource-ec2-1",
		"111122223333",
		"AmazonEC2",
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		"2026-02-01T00:00:00Z",
		"2026-02-01T01:00:00Z",
		int64(0),
		"Hours",
	)
	assertExecFails(t, db, `INSERT INTO usage_events (
		id,
		resource_id,
		account_id,
		service_code,
		usage_type,
		operation,
		region_code,
		usage_start_time,
		usage_end_time,
		usage_quantity_micros,
		usage_unit
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"usage-invalid-window",
		"resource-ec2-1",
		"111122223333",
		"AmazonEC2",
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		"2026-02-01T02:00:00Z",
		"2026-02-01T01:00:00Z",
		int64(1_000_000),
		"Hours",
	)
}

func insertTestResource(t *testing.T, db *sql.DB, id string) {
	t.Helper()

	if _, err := db.ExecContext(context.Background(), `INSERT INTO resources (
		id,
		account_id,
		region_code,
		service_code,
		resource_type,
		resource_name,
		status,
		started_at,
		attributes_json,
		event_source,
		scenario_run_id,
		scenario_event_id,
		scenario_event_sequence
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		"111122223333",
		"us-east-1",
		"AmazonEC2",
		"ec2_instance",
		"Storefront web",
		"active",
		"2026-02-01T00:00:00Z",
		`{"instance_type":"t3.medium","operating_system":"linux","tenancy":"shared"}`,
		"scenario",
		"scenario-run-1",
		"event-create-ec2",
		1,
	); err != nil {
		t.Fatalf("insert test resource: %v", err)
	}
}

func assertExecFails(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()

	_, err := db.ExecContext(context.Background(), query, args...)
	if err == nil {
		t.Fatalf("ExecContext(%s) error = nil, want error", strings.TrimSpace(query))
	}
}
