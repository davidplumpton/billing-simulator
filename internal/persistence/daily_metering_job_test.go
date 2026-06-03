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
	if first.MeteringRecordsCreated != 1 || first.BillLineItemsCreated != 2 || len(first.Summaries) != 2 {
		t.Fatalf("Run(first) = %+v, want one record, usage item, support item, and two summaries", first)
	}
	if first.Run.Trigger != DailyMeteringJobTriggerOnDemand ||
		first.Run.ClockTime != "2026-02-02T00:00:00Z" ||
		first.Run.PayerAccountID != "999988887777" ||
		first.Run.SummariesRefreshed != 2 ||
		first.Run.CompletedAt == "" {
		t.Fatalf("first job run = %+v, want on-demand audit metadata", first.Run)
	}
	firstEC2Summary := requireBillingPeriodSummary(t, first.Summaries, serviceAmazonEC2)
	if firstEC2Summary.LineItemStatus != billLineItemStatusEstimated ||
		firstEC2Summary.LineItemCount != 1 ||
		firstEC2Summary.UnblendedCostMicros != 83_200 {
		t.Fatalf("first EC2 summary = %+v, want one estimated EC2 item costing 83200 micros", firstEC2Summary)
	}
	firstSupportSummary := requireBillingPeriodSummary(t, first.Summaries, serviceAWSSupport)
	if firstSupportSummary.LineItemStatus != billLineItemStatusEstimated ||
		firstSupportSummary.LineItemCount != 1 ||
		firstSupportSummary.UnblendedCostMicros != supportBusinessMinimumCostMicros {
		t.Fatalf("first support summary = %+v, want one estimated Support minimum fee", firstSupportSummary)
	}

	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 10)
	if err != nil {
		t.Fatalf("ListBillLineItems(first) error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("bill line items after first run = %+v, want usage and support items", items)
	}
	if item := requireBillLineItemByService(t, items, serviceAmazonEC2); item.UsageEventID != "usage-daily-metering-ready" || item.LineItemStatus != billLineItemStatusEstimated {
		t.Fatalf("EC2 bill line item after first run = %+v, want ready estimated item", item)
	}
	if item := requireBillLineItemByService(t, items, serviceAWSSupport); item.LineItemType != billLineItemTypeFee || item.LineItemStatus != billLineItemStatusEstimated {
		t.Fatalf("Support bill line item after first run = %+v, want estimated fee", item)
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
	if second.MeteringRecordsCreated != 1 || second.BillLineItemsCreated != 1 || len(second.Summaries) != 2 {
		t.Fatalf("Run(second) = %+v, want second usage item, updated support, and refreshed summaries", second)
	}
	summary := requireBillingPeriodSummary(t, second.Summaries, serviceAmazonEC2)
	if summary.LineItemCount != 2 || summary.UnblendedCostMicros != 124_800 || summary.LineItemStatus != billLineItemStatusEstimated {
		t.Fatalf("refreshed EC2 summary = %+v, want two estimated items totaling 124800 micros", summary)
	}
	supportSummary := requireBillingPeriodSummary(t, second.Summaries, serviceAWSSupport)
	if supportSummary.LineItemCount != 1 || supportSummary.UnblendedCostMicros != supportBusinessMinimumCostMicros || supportSummary.LineItemStatus != billLineItemStatusEstimated {
		t.Fatalf("refreshed Support summary = %+v, want one estimated minimum fee", supportSummary)
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

func requireBillingPeriodSummary(t *testing.T, summaries []BillingPeriodServiceSummary, serviceCode string) BillingPeriodServiceSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.ServiceCode == serviceCode {
			return summary
		}
	}
	t.Fatalf("summaries = %+v, want service %s", summaries, serviceCode)
	return BillingPeriodServiceSummary{}
}

func requireBillLineItemByService(t *testing.T, items []BillLineItem, serviceCode string) BillLineItem {
	t.Helper()

	for _, item := range items {
		if item.ServiceCode == serviceCode {
			return item
		}
	}
	t.Fatalf("bill line items = %+v, want service %s", items, serviceCode)
	return BillLineItem{}
}
