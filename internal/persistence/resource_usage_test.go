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
