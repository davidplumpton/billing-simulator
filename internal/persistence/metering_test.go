package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestMeteringRepositoryGeneratesNormalizedRecordsFromUsageEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-ec2-metered-1",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Metered web",
		Status:       "active",
		Tags: map[string]string{
			"app":   "storefront",
			"owner": "platform",
		},
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	event, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-meter-ec2-hours",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: int64(2_000_000),
		UsageUnit:           "Hours",
	})
	if err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}

	result, err := meteringRepo.GenerateMeteringRecords(ctx)
	if err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if result.RecordsCreated != 1 || len(result.Records) != 1 {
		t.Fatalf("GenerateMeteringRecords() = %+v, want one created record", result)
	}
	record := result.Records[0]
	if record.UsageEventID != event.ID || record.ResourceID != resource.ID {
		t.Fatalf("metering record lineage = %+v, want usage event and resource IDs", record)
	}
	if record.AccountID != "111122223333" || record.RegionCode != "us-east-1" || record.ServiceCode != "AmazonEC2" {
		t.Fatalf("metering record billing dimensions = %+v, want event account/region/service", record)
	}
	if record.UsageType != "instance-hours:t3.medium" || record.Operation != "RunInstances" {
		t.Fatalf("metering record price dimensions = %+v, want usage type and operation", record)
	}
	if record.UsageQuantityMicros != 2_000_000 || record.UsageUnit != "Hours" {
		t.Fatalf("metering record amount = %d %s, want 2000000 Hours", record.UsageQuantityMicros, record.UsageUnit)
	}
	if record.UsageStartTime != "2026-02-01T00:00:00Z" || record.UsageEndTime != "2026-02-01T02:00:00Z" {
		t.Fatalf("metering record time bounds = %s/%s, want usage event bounds", record.UsageStartTime, record.UsageEndTime)
	}
	if record.TagSnapshot["app"] != "storefront" || record.TagSnapshot["owner"] != "platform" {
		t.Fatalf("metering tag snapshot = %+v, want usage tag snapshot", record.TagSnapshot)
	}

	records, err := meteringRepo.ListMeteringRecords(ctx, 10)
	if err != nil {
		t.Fatalf("ListMeteringRecords() error = %v", err)
	}
	if len(records) != 1 || records[0].UsageEventID != event.ID || records[0].CreatedAt == "" {
		t.Fatalf("ListMeteringRecords() = %+v, want persisted metering record", records)
	}
}

func TestMeteringRepositoryIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-s3-metered-1",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonS3",
		ResourceType: "s3_bucket",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-meter-s3-put",
		ResourceID:          resource.ID,
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		UsageStartTime:      "2026-02-02T00:00:00Z",
		UsageEndTime:        "2026-02-03T00:00:00Z",
		UsageQuantityMicros: int64(12_000_000_000),
		UsageUnit:           "Requests",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}

	first, err := meteringRepo.GenerateMeteringRecords(ctx)
	if err != nil {
		t.Fatalf("first GenerateMeteringRecords() error = %v", err)
	}
	second, err := meteringRepo.GenerateMeteringRecords(ctx)
	if err != nil {
		t.Fatalf("second GenerateMeteringRecords() error = %v", err)
	}
	if first.RecordsCreated != 1 || second.RecordsCreated != 0 {
		t.Fatalf("metering run created counts = %d/%d, want 1/0", first.RecordsCreated, second.RecordsCreated)
	}

	records, err := meteringRepo.ListMeteringRecords(ctx, 10)
	if err != nil {
		t.Fatalf("ListMeteringRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("metering record count = %d, want 1", len(records))
	}
}

func TestMeteringRepositorySurfacesInvalidUsageEventTimestamps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	insertTestResource(t, db, "resource-invalid-metering-1")

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
		usage_unit
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"usage-invalid-metering-time",
		"resource-invalid-metering-1",
		"111122223333",
		"AmazonEC2",
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		"not-a-timestamp",
		"still-not-a-timestamp",
		int64(1_000_000),
		"Hours",
	); err != nil {
		t.Fatalf("insert invalid timestamp usage event: %v", err)
	}

	_, err := NewMeteringRepository(db).GenerateMeteringRecords(ctx)
	if err == nil || !strings.Contains(err.Error(), "start time must use RFC3339") {
		t.Fatalf("GenerateMeteringRecords() error = %v, want timestamp validation error", err)
	}
}
