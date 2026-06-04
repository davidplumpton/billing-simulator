package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestResourceUsageRepositoryCreatesResourceTagsAndUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	resource, err := repo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-ec2-ui-1",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Storefront web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Attributes: map[string]string{
			"instance_type": "t3.medium",
			"size":          "t3.medium",
		},
		Tags: map[string]string{
			"app":   "storefront",
			"owner": "web-platform",
		},
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if resource.ID != "resource-ec2-ui-1" || resource.Attributes["size"] != "t3.medium" {
		t.Fatalf("created resource = %+v, want fixed ID with size attribute", resource)
	}

	tag, err := repo.AddTag(ctx, ResourceTagCreateRequest{
		ID:         "tag-env-prod",
		ResourceID: resource.ID,
		Key:        "env",
		Value:      "prod",
		AppliedAt:  "2026-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("AddTag() error = %v", err)
	}
	if tag.Key != "env" || tag.Value != "prod" || tag.ResourceID != resource.ID {
		t.Fatalf("added tag = %+v, want env=prod on %s", tag, resource.ID)
	}

	event, err := repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-ec2-hours-1",
		ResourceID:          resource.ID,
		ServiceCode:         "AmazonEC2",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: int64(2_000_000),
		UsageUnit:           "Hours",
		Attributes: map[string]string{
			"generation": "EC2 instance hours",
		},
	})
	if err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if event.AccountID != "111122223333" || event.ServiceCode != "AmazonEC2" || event.RegionCode != "us-east-1" {
		t.Fatalf("usage event inherited dimensions = %+v, want account/service/region from resource", event)
	}
	if event.TagSnapshot["app"] != "storefront" || event.TagSnapshot["owner"] != "web-platform" || event.TagSnapshot["env"] != "prod" {
		t.Fatalf("usage tag snapshot = %+v, want active resource tags", event.TagSnapshot)
	}

	summaries, err := repo.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("resource summary count = %d, want 1", len(summaries))
	}
	if summaries[0].UsageEventCount != 1 || summaries[0].ActiveTags["env"] != "prod" {
		t.Fatalf("resource summary = %+v, want usage count and active tags", summaries[0])
	}

	events, err := repo.ListUsageEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListUsageEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].ID != "usage-ec2-hours-1" {
		t.Fatalf("usage events = %+v, want generated event", events)
	}
}

func TestResourceUsageRepositorySnapshotsTagsAtUsageStart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	resource, err := repo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-tag-history",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Tagged history",
		Status:       "active",
		StartedAt:    "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}

	if _, err := repo.AddTag(ctx, ResourceTagCreateRequest{
		ID:         "tag-owner-existing",
		ResourceID: resource.ID,
		Key:        "owner",
		Value:      "platform",
		AppliedAt:  "2026-01-15T00:00:00Z",
	}); err != nil {
		t.Fatalf("AddTag(owner) error = %v", err)
	}
	if _, err := repo.AddTag(ctx, ResourceTagCreateRequest{
		ID:         "tag-env-future",
		ResourceID: resource.ID,
		Key:        "env",
		Value:      "prod",
		AppliedAt:  "2026-02-01T00:30:00Z",
	}); err != nil {
		t.Fatalf("AddTag(env) error = %v", err)
	}
	if _, err := repo.AddTag(ctx, ResourceTagCreateRequest{
		ID:         "tag-legacy-removed",
		ResourceID: resource.ID,
		Key:        "legacy",
		Value:      "migration",
		AppliedAt:  "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("AddTag(legacy) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE resource_tags SET removed_at = ? WHERE id = ?`, "2026-01-31T23:00:00Z", "tag-legacy-removed"); err != nil {
		t.Fatalf("mark legacy tag removed: %v", err)
	}

	event, err := repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-tag-history",
		ResourceID:          resource.ID,
		ServiceCode:         "AmazonEC2",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T01:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	})
	if err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if event.TagSnapshot["owner"] != "platform" {
		t.Fatalf("usage tag snapshot = %+v, want owner active at usage start", event.TagSnapshot)
	}
	if _, ok := event.TagSnapshot["env"]; ok {
		t.Fatalf("usage tag snapshot = %+v, did not want tag applied after usage start", event.TagSnapshot)
	}
	if _, ok := event.TagSnapshot["legacy"]; ok {
		t.Fatalf("usage tag snapshot = %+v, did not want tag removed before usage start", event.TagSnapshot)
	}

	summaries, err := repo.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ActiveTags["env"] != "prod" || summaries[0].ActiveTags["legacy"] != "" {
		t.Fatalf("resource summary active tags = %+v, want current env tag without removed legacy tag", summaries)
	}
}

func TestResourceUsageRepositoryRecordsScenarioProvenance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	resource, err := repo.CreateResource(ctx, ResourceCreateRequest{
		ID:                    "resource-scenario-ec2",
		AccountID:             "111122223333",
		RegionCode:            "us-east-1",
		ServiceCode:           "AmazonEC2",
		ResourceType:          "ec2_instance",
		ResourceName:          "Scenario web",
		Status:                "active",
		StartedAt:             "2026-03-01T00:00:00Z",
		EventSource:           "scenario",
		ScenarioRunID:         "scenario-run-1",
		ScenarioEventID:       "create-web",
		ScenarioEventSequence: 1,
		Tags: map[string]string{
			"app": "storefront",
		},
	})
	if err != nil {
		t.Fatalf("CreateResource(scenario) error = %v", err)
	}
	if resource.EventSource != "scenario" ||
		resource.ScenarioRunID != "scenario-run-1" ||
		resource.ScenarioEventID != "create-web" ||
		resource.ScenarioEventSequence != 1 {
		t.Fatalf("resource provenance = %+v, want scenario run/event metadata", resource)
	}

	event, err := repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                    "usage-scenario-ec2",
		ResourceID:            resource.ID,
		ServiceCode:           "AmazonEC2",
		UsageType:             "instance-hours:t3.medium",
		Operation:             "RunInstances",
		UsageStartTime:        "2026-03-01T00:00:00Z",
		UsageEndTime:          "2026-03-02T00:00:00Z",
		UsageQuantityMicros:   24_000_000,
		UsageUnit:             "Hours",
		EventSource:           "scenario",
		ScenarioRunID:         "scenario-run-1",
		ScenarioEventID:       "usage-web",
		ScenarioEventSequence: 2,
	})
	if err != nil {
		t.Fatalf("RecordUsageEvent(scenario) error = %v", err)
	}
	if event.EventSource != "scenario" ||
		event.ScenarioRunID != "scenario-run-1" ||
		event.ScenarioEventID != "usage-web" ||
		event.ScenarioEventSequence != 2 ||
		event.TagSnapshot["app"] != "storefront" {
		t.Fatalf("usage event provenance/tag snapshot = %+v, want scenario metadata and active tag", event)
	}

	var tagRunID, tagEventID string
	var tagEventSequence int
	if err := db.QueryRowContext(ctx, `SELECT scenario_run_id, scenario_event_id, scenario_event_sequence FROM resource_tags WHERE resource_id = ? AND tag_key = 'app'`, resource.ID).Scan(&tagRunID, &tagEventID, &tagEventSequence); err != nil {
		t.Fatalf("read scenario resource tag provenance: %v", err)
	}
	if tagRunID != "scenario-run-1" || tagEventID != "create-web" || tagEventSequence != 1 {
		t.Fatalf("tag provenance = %q/%q/%d, want create-web scenario metadata", tagRunID, tagEventID, tagEventSequence)
	}

	_, err = repo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-missing-provenance",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		EventSource:  "scenario",
	})
	if err == nil || !strings.Contains(err.Error(), "scenario run ID is required") {
		t.Fatalf("CreateResource(missing scenario IDs) error = %v, want scenario provenance validation", err)
	}
}

func TestResourceUsageRepositoryRejectsInvalidUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	if _, err := repo.CreateResource(ctx, ResourceCreateRequest{
		AccountID:    "",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		Status:       "active",
	}); err == nil {
		t.Fatal("CreateResource(blank account) error = nil, want validation error")
	}

	resource, err := repo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-ec2-ui-2",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}

	_, err = repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ResourceID:          resource.ID,
		ServiceCode:         "AmazonS3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T01:00:00Z",
		UsageQuantityMicros: int64(1_000_000),
		UsageUnit:           "Request",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match resource service") {
		t.Fatalf("RecordUsageEvent(service mismatch) error = %v, want mismatch error", err)
	}

	_, err = repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ResourceID:          resource.ID,
		ServiceCode:         "AmazonEC2",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T01:00:00Z",
		UsageEndTime:        "2026-02-01T01:00:00Z",
		UsageQuantityMicros: int64(1_000_000),
		UsageUnit:           "Hours",
	})
	if err == nil || !strings.Contains(err.Error(), "before end time") {
		t.Fatalf("RecordUsageEvent(invalid window) error = %v, want window error", err)
	}
}

func TestResourceUsageRepositoryRejectsUsageOutsideResourceLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	tests := []struct {
		name        string
		resource    ResourceCreateRequest
		usageStart  string
		usageEnd    string
		wantMessage string
	}{
		{
			name: "planned resource",
			resource: ResourceCreateRequest{
				ID:           "resource-lifecycle-planned",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  "AmazonEC2",
				ResourceType: "ec2_instance",
				Status:       "planned",
			},
			usageStart:  "2026-02-01T00:00:00Z",
			usageEnd:    "2026-02-01T01:00:00Z",
			wantMessage: "planned",
		},
		{
			name: "before started_at",
			resource: ResourceCreateRequest{
				ID:           "resource-lifecycle-before-start",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  "AmazonEC2",
				ResourceType: "ec2_instance",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
			},
			usageStart:  "2026-01-31T23:00:00Z",
			usageEnd:    "2026-02-01T01:00:00Z",
			wantMessage: "starts before resource",
		},
		{
			name: "after stopped_at",
			resource: ResourceCreateRequest{
				ID:           "resource-lifecycle-after-stop",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  "AmazonEC2",
				ResourceType: "ec2_instance",
				Status:       "stopped",
				StartedAt:    "2026-02-01T00:00:00Z",
				StoppedAt:    "2026-02-01T02:00:00Z",
			},
			usageStart:  "2026-02-01T01:00:00Z",
			usageEnd:    "2026-02-01T03:00:00Z",
			wantMessage: "stopped_at",
		},
		{
			name: "after deleted_at",
			resource: ResourceCreateRequest{
				ID:           "resource-lifecycle-after-delete",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  "AmazonEC2",
				ResourceType: "ec2_instance",
				Status:       "deleted",
				StartedAt:    "2026-02-01T00:00:00Z",
				DeletedAt:    "2026-02-01T02:00:00Z",
			},
			usageStart:  "2026-02-01T01:00:00Z",
			usageEnd:    "2026-02-01T03:00:00Z",
			wantMessage: "deleted_at",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			resource, err := repo.CreateResource(ctx, tt.resource)
			if err != nil {
				t.Fatalf("CreateResource() error = %v", err)
			}

			_, err = repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
				ResourceID:          resource.ID,
				ServiceCode:         "AmazonEC2",
				UsageType:           "instance-hours:t3.medium",
				Operation:           "RunInstances",
				UsageStartTime:      tt.usageStart,
				UsageEndTime:        tt.usageEnd,
				UsageQuantityMicros: 1_000_000,
				UsageUnit:           "Hours",
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("RecordUsageEvent() error = %v, want message containing %q", err, tt.wantMessage)
			}
		})
	}
}

func TestResourceUsageRepositoryAllowsUsageThroughStopTime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	resource, err := repo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-lifecycle-through-stop",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		Status:       "stopped",
		StartedAt:    "2026-02-01T00:00:00Z",
		StoppedAt:    "2026-02-01T02:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}

	event, err := repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-lifecycle-through-stop",
		ResourceID:          resource.ID,
		ServiceCode:         "AmazonEC2",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	})
	if err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if event.UsageEndTime != resource.StoppedAt {
		t.Fatalf("usage end time = %q, want bounded stop time %q", event.UsageEndTime, resource.StoppedAt)
	}
}
