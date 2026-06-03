package persistence

import (
	"context"
	"testing"
)

func TestDailyMeteringJobRunsThroughSimulatorClockAndRefreshesSummaries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	clockRepo := NewSimulatorClockRepository(db)
	jobRepo := NewDailyMeteringJobRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-daily-metering-ec2",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-daily-metering-ready",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(ready) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-daily-metering-future",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-02T23:00:00Z",
		UsageEndTime:        "2026-02-03T00:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(future) error = %v", err)
	}
	if _, err := clockRepo.Set(ctx, "2026-02-02T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	first, err := jobRepo.Run(ctx, DailyMeteringJobRequest{
		Trigger:        DailyMeteringJobTriggerOnDemand,
		PayerAccountID: "999988887777",
	})
	if err != nil {
		t.Fatalf("Run(first) error = %v", err)
	}
	if first.MeteringRecordsCreated != 1 || first.BillLineItemsCreated != 1 || len(first.Summaries) != 1 {
		t.Fatalf("Run(first) = %+v, want one record, one item, one summary", first)
	}
	if first.Run.Trigger != DailyMeteringJobTriggerOnDemand ||
		first.Run.ClockTime != "2026-02-02T00:00:00Z" ||
		first.Run.PayerAccountID != "999988887777" ||
		first.Run.SummariesRefreshed != 1 ||
		first.Run.CompletedAt == "" {
		t.Fatalf("first job run = %+v, want on-demand audit metadata", first.Run)
	}
	firstSummary := first.Summaries[0]
	if firstSummary.LineItemStatus != billLineItemStatusEstimated ||
		firstSummary.LineItemCount != 1 ||
		firstSummary.UnblendedCostMicros != 83_200 ||
		firstSummary.ServiceCode != serviceAmazonEC2 {
		t.Fatalf("first summary = %+v, want one estimated EC2 item costing 83200 micros", firstSummary)
	}

	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 10)
	if err != nil {
		t.Fatalf("ListBillLineItems(first) error = %v", err)
	}
	if len(items) != 1 || items[0].UsageEventID != "usage-daily-metering-ready" || items[0].LineItemStatus != billLineItemStatusEstimated {
		t.Fatalf("bill line items after first run = %+v, want only ready estimated item", items)
	}

	if _, err := clockRepo.Set(ctx, "2026-02-03T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock second) error = %v", err)
	}
	second, err := jobRepo.Run(ctx, DailyMeteringJobRequest{
		Trigger:        DailyMeteringJobTriggerClockAdvance,
		PayerAccountID: "999988887777",
	})
	if err != nil {
		t.Fatalf("Run(second) error = %v", err)
	}
	if second.MeteringRecordsCreated != 1 || second.BillLineItemsCreated != 1 || len(second.Summaries) != 1 {
		t.Fatalf("Run(second) = %+v, want second record/item and refreshed summary", second)
	}
	summary := second.Summaries[0]
	if summary.LineItemCount != 2 || summary.UnblendedCostMicros != 124_800 || summary.LineItemStatus != billLineItemStatusEstimated {
		t.Fatalf("refreshed summary = %+v, want two estimated items totaling 124800 micros", summary)
	}

	runs, err := jobRepo.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("ListRuns() = %+v, want two runs", runs)
	}
	seenTriggers := map[DailyMeteringJobTrigger]bool{}
	for _, run := range runs {
		seenTriggers[run.Trigger] = true
	}
	if !seenTriggers[DailyMeteringJobTriggerClockAdvance] || !seenTriggers[DailyMeteringJobTriggerOnDemand] {
		t.Fatalf("ListRuns() = %+v, want clock-advance and on-demand runs", runs)
	}
}
