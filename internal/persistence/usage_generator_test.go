package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

type wantedGeneratedEvent struct {
	Index          int
	UsageType      string
	Operation      string
	RegionCode     string
	QuantityMicros int64
	UsageUnit      string
}

func TestResourceUsageRepositoryGeneratesDeterministicUsagePatterns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)
	catalog := NewPriceCatalogRepository(db)

	tests := []struct {
		name       string
		resource   ResourceCreateRequest
		pattern    UsageGenerationPattern
		days       int
		wantCount  int
		wantEvents []wantedGeneratedEvent
	}{
		{
			name: "daily EC2 instance hours",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-ec2",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonEC2,
				ResourceType: "ec2_instance",
				ResourceName: "Generated EC2",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
				Tags:         map[string]string{"app": "storefront", "owner": "web-platform"},
			},
			pattern:   UsageGenerationDailyInstanceHours,
			days:      2,
			wantCount: 2,
			wantEvents: []wantedGeneratedEvent{
				{Index: 0, UsageType: "instance-hours:t3.medium", Operation: "RunInstances", RegionCode: "us-east-1", QuantityMicros: int64(24_000_000), UsageUnit: "Hours"},
			},
		},
		{
			name: "S3 storage growth",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-s3-storage",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonS3,
				ResourceType: "s3_bucket",
				ResourceName: "Generated S3 storage",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
				Tags:         map[string]string{"app": "analytics", "owner": "data-platform"},
			},
			pattern:   UsageGenerationStorageGrowth,
			days:      3,
			wantCount: 3,
			wantEvents: []wantedGeneratedEvent{
				{Index: 0, UsageType: "storage:standard-gb-month", Operation: "StandardStorage", RegionCode: "us-east-1", QuantityMicros: int64(100_000_000), UsageUnit: "GBDay"},
				{Index: 2, UsageType: "storage:standard-gb-month", Operation: "StandardStorage", RegionCode: "us-east-1", QuantityMicros: int64(150_000_000), UsageUnit: "GBDay"},
			},
		},
		{
			name: "S3 requests",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-s3-requests",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonS3,
				ResourceType: "s3_bucket",
				ResourceName: "Generated S3 requests",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
				Tags:         map[string]string{"app": "storefront", "owner": "web-platform"},
			},
			pattern:   UsageGenerationRequests,
			days:      2,
			wantCount: 4,
			wantEvents: []wantedGeneratedEvent{
				{Index: 0, UsageType: "requests:put-1k", Operation: "PutObject", RegionCode: "us-east-1", QuantityMicros: int64(12_000_000_000), UsageUnit: "Request"},
				{Index: 3, UsageType: "requests:get-1k", Operation: "GetObject", RegionCode: "us-east-1", QuantityMicros: int64(270_000_000_000), UsageUnit: "Request"},
			},
		},
		{
			name: "Lambda execution",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-lambda",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAWSLambda,
				ResourceType: "lambda_function",
				ResourceName: "Generated Lambda",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
				Tags:         map[string]string{"app": "images", "owner": "serverless-team"},
			},
			pattern:   UsageGenerationLambdaExecution,
			days:      2,
			wantCount: 4,
			wantEvents: []wantedGeneratedEvent{
				{Index: 0, UsageType: "requests:lambda-1m", Operation: "Invoke", RegionCode: "us-east-1", QuantityMicros: int64(800_000_000_000), UsageUnit: "Request"},
				{Index: 1, UsageType: "compute:lambda-gb-second", Operation: "Invoke", RegionCode: "us-east-1", QuantityMicros: int64(80_000_000_000), UsageUnit: "GBSecond"},
			},
		},
		{
			name: "NAT traffic",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-nat",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonVPCNATGateway,
				ResourceType: "nat_gateway",
				ResourceName: "Generated NAT",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
				Tags:         map[string]string{"app": "shared-platform", "owner": "networking"},
			},
			pattern:   UsageGenerationNATTraffic,
			days:      2,
			wantCount: 4,
			wantEvents: []wantedGeneratedEvent{
				{Index: 0, UsageType: "nat-gateway-hours", Operation: "NatGateway", RegionCode: "us-east-1", QuantityMicros: int64(24_000_000), UsageUnit: "Hours"},
				{Index: 3, UsageType: "nat-gateway-data-processed-gb", Operation: "NatGatewayDataProcessing", RegionCode: "us-east-1", QuantityMicros: int64(100_000_000), UsageUnit: "GB"},
			},
		},
		{
			name: "data transfer spikes",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-data-transfer",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAWSDataTransfer,
				ResourceType: "data_transfer_path",
				ResourceName: "Generated transfer path",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
				Tags:         map[string]string{"app": "storefront", "owner": "networking"},
			},
			pattern:   UsageGenerationDataTransferSpikes,
			days:      3,
			wantCount: 3,
			wantEvents: []wantedGeneratedEvent{
				{Index: 1, UsageType: "data-transfer-out-internet-gb", Operation: "DataTransferOut", RegionCode: "global", QuantityMicros: int64(750_000_000), UsageUnit: "GB"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource, err := repo.CreateResource(ctx, tt.resource)
			if err != nil {
				t.Fatalf("CreateResource() error = %v", err)
			}

			request := UsageGenerationRequest{
				ResourceID: resource.ID,
				Pattern:    tt.pattern,
				StartDate:  "2026-02-01",
				Days:       tt.days,
			}
			first, err := repo.GenerateUsage(ctx, request)
			if err != nil {
				t.Fatalf("GenerateUsage() error = %v", err)
			}
			if len(first.Events) != tt.wantCount {
				t.Fatalf("GenerateUsage() event count = %d, want %d", len(first.Events), tt.wantCount)
			}
			if first.EventsCreated != tt.wantCount || first.EventsReused != 0 {
				t.Fatalf("GenerateUsage() created/reused = %d/%d, want %d/0", first.EventsCreated, first.EventsReused, tt.wantCount)
			}
			if first.Resource.ID != resource.ID {
				t.Fatalf("GenerateUsage() resource ID = %q, want %q", first.Resource.ID, resource.ID)
			}

			second, err := repo.GenerateUsage(ctx, request)
			if err != nil {
				t.Fatalf("GenerateUsage() repeat error = %v", err)
			}
			assertSameUsageEventIDs(t, second.Events, first.Events)
			if second.EventsCreated != 0 || second.EventsReused != tt.wantCount {
				t.Fatalf("GenerateUsage() repeat created/reused = %d/%d, want 0/%d", second.EventsCreated, second.EventsReused, tt.wantCount)
			}
			if count := countUsageEventsForResource(t, db, resource.ID); count != tt.wantCount {
				t.Fatalf("usage event count after repeat = %d, want %d", count, tt.wantCount)
			}

			for _, event := range first.Events {
				if event.EventSource != "generator" {
					t.Fatalf("generated event source = %q, want generator", event.EventSource)
				}
				if event.TagSnapshot["owner"] != tt.resource.Tags["owner"] {
					t.Fatalf("generated event tag snapshot = %+v, want owner tag", event.TagSnapshot)
				}
				if event.Attributes["generator_pattern"] != string(tt.pattern) {
					t.Fatalf("generated event attributes = %+v, want generator pattern %q", event.Attributes, tt.pattern)
				}
				if _, err := catalog.Lookup(ctx, PriceLookupRequest{
					ServiceCode:         event.ServiceCode,
					UsageType:           event.UsageType,
					Operation:           event.Operation,
					RegionCode:          event.RegionCode,
					UsageUnit:           event.UsageUnit,
					UsageQuantityMicros: event.UsageQuantityMicros,
					UsageDate:           event.UsageStartTime[:len("2006-01-02")],
					BillingPeriodDays:   30,
				}); err != nil {
					t.Fatalf("Lookup() for generated event %+v error = %v", event, err)
				}
			}

			for _, want := range tt.wantEvents {
				assertGeneratedEvent(t, first.Events[want.Index], want)
			}
		})
	}
}

func TestResourceUsageRepositoryRejectsUnsupportedUsageGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	resource, err := repo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-generator-unsupported",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonS3,
		ResourceType: "s3_bucket",
		ResourceName: "Unsupported generation",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}

	_, err = repo.GenerateUsage(ctx, UsageGenerationRequest{
		ResourceID: resource.ID,
		Pattern:    UsageGenerationLambdaExecution,
		StartDate:  "2026-02-01",
		Days:       1,
	})
	if err == nil || !strings.Contains(err.Error(), "does not support resource service") {
		t.Fatalf("GenerateUsage(unsupported service) error = %v, want service support error", err)
	}

	_, err = repo.GenerateUsage(ctx, UsageGenerationRequest{
		ResourceID: resource.ID,
		Pattern:    UsageGenerationStorageGrowth,
		StartDate:  "2026-02-01",
		Days:       32,
	})
	if err == nil || !strings.Contains(err.Error(), "31 or fewer") {
		t.Fatalf("GenerateUsage(too many days) error = %v, want day limit error", err)
	}
}

func TestResourceUsageRepositoryRejectsGeneratedUsageOutsideResourceLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewResourceUsageRepository(db)

	tests := []struct {
		name        string
		resource    ResourceCreateRequest
		startDate   string
		days        int
		wantMessage string
	}{
		{
			name: "planned resource",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-lifecycle-planned",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonEC2,
				ResourceType: "ec2_instance",
				Status:       "planned",
			},
			startDate:   "2026-02-01",
			days:        1,
			wantMessage: "planned",
		},
		{
			name: "before started_at",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-lifecycle-before-start",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonEC2,
				ResourceType: "ec2_instance",
				Status:       "active",
				StartedAt:    "2026-02-02T00:00:00Z",
			},
			startDate:   "2026-02-01",
			days:        1,
			wantMessage: "starts before resource",
		},
		{
			name: "after stopped_at",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-lifecycle-after-stop",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonEC2,
				ResourceType: "ec2_instance",
				Status:       "stopped",
				StartedAt:    "2026-02-01T00:00:00Z",
				StoppedAt:    "2026-02-02T00:00:00Z",
			},
			startDate:   "2026-02-01",
			days:        2,
			wantMessage: "stopped_at",
		},
		{
			name: "after deleted_at",
			resource: ResourceCreateRequest{
				ID:           "resource-generator-lifecycle-after-delete",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonEC2,
				ResourceType: "ec2_instance",
				Status:       "deleted",
				StartedAt:    "2026-02-01T00:00:00Z",
				DeletedAt:    "2026-02-02T00:00:00Z",
			},
			startDate:   "2026-02-01",
			days:        2,
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

			_, err = repo.GenerateUsage(ctx, UsageGenerationRequest{
				ResourceID: resource.ID,
				Pattern:    UsageGenerationDailyInstanceHours,
				StartDate:  tt.startDate,
				Days:       tt.days,
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("GenerateUsage() error = %v, want message containing %q", err, tt.wantMessage)
			}
			if count := countUsageEventsForResource(t, db, resource.ID); count != 0 {
				t.Fatalf("usage event count after rejected generation = %d, want 0", count)
			}
		})
	}
}

func assertGeneratedEvent(t *testing.T, event UsageEvent, want wantedGeneratedEvent) {
	t.Helper()
	if event.UsageType != want.UsageType ||
		event.Operation != want.Operation ||
		event.RegionCode != want.RegionCode ||
		event.UsageQuantityMicros != want.QuantityMicros ||
		event.UsageUnit != want.UsageUnit {
		t.Fatalf("generated event[%d] = %+v, want usage_type=%q operation=%q region=%q quantity=%d unit=%q",
			want.Index,
			event,
			want.UsageType,
			want.Operation,
			want.RegionCode,
			want.QuantityMicros,
			want.UsageUnit,
		)
	}
}

func assertSameUsageEventIDs(t *testing.T, got, want []UsageEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("usage event count = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].ID != want[i].ID {
			t.Fatalf("usage event ID[%d] = %q, want %q", i, got[i].ID, want[i].ID)
		}
	}
}

func countUsageEventsForResource(t *testing.T, db *sql.DB, resourceID string) int {
	t.Helper()

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM usage_events WHERE resource_id = ?`, resourceID).Scan(&count); err != nil {
		t.Fatalf("count usage events for %q: %v", resourceID, err)
	}
	return count
}
