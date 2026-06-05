package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestCostAllocationTagRepositoryDiscoversInventoryAndActivationLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	resourceRepo := NewResourceUsageRepository(db)
	tagRepo := NewCostAllocationTagRepository(db)

	for _, request := range []ResourceCreateRequest{
		{
			ID:           "resource-tag-storefront",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonEC2",
			ResourceType: "ec2_instance",
			ResourceName: "Storefront web",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
		{
			ID:           "resource-tag-payments",
			AccountID:    "444455556666",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonEC2",
			ResourceType: "ec2_instance",
			ResourceName: "Payments web",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
	} {
		if _, err := resourceRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}

	for _, request := range []ResourceTagCreateRequest{
		{
			ID:         "tag-storefront-app",
			ResourceID: "resource-tag-storefront",
			Key:        "app",
			Value:      "storefront",
			AppliedAt:  "2026-02-01T00:00:00Z",
		},
		{
			ID:         "tag-payments-app",
			ResourceID: "resource-tag-payments",
			Key:        "app",
			Value:      "storefront",
			AppliedAt:  "2026-02-01T01:00:00Z",
		},
		{
			ID:         "tag-storefront-owner",
			ResourceID: "resource-tag-storefront",
			Key:        "owner",
			Value:      "web-platform",
			AppliedAt:  "2026-02-01T02:00:00Z",
		},
		{
			ID:         "tag-payments-owner-cased",
			ResourceID: "resource-tag-payments",
			Key:        "Owner",
			Value:      "payments-team",
			AppliedAt:  "2026-02-01T03:00:00Z",
		},
	} {
		if _, err := resourceRepo.AddTag(ctx, request); err != nil {
			t.Fatalf("AddTag(%s) error = %v", request.ID, err)
		}
	}

	result, err := tagRepo.RefreshDiscoveredTags(ctx, "2026-02-02T00:00:00Z")
	if err != nil {
		t.Fatalf("RefreshDiscoveredTags() error = %v", err)
	}
	if result.DiscoveredKeyCount != 3 || result.InventoryValueCount != 3 {
		t.Fatalf("refresh result = %+v, want 3 discovered keys and 3 key/value rows", result)
	}

	inventory, err := tagRepo.ListInventory(ctx)
	if err != nil {
		t.Fatalf("ListInventory() error = %v", err)
	}
	inventoryRows := costAllocationInventoryByKeyValue(inventory)
	appRow := inventoryRows["app=storefront"]
	if appRow.ResourceCount != 2 ||
		appRow.ActivationStatus != costAllocationTagStatusDiscovered ||
		appRow.FirstSeenAt != "2026-02-01T00:00:00Z" ||
		appRow.LastSeenAt != "2026-02-01T01:00:00Z" {
		t.Fatalf("app inventory row = %+v, want discovered storefront count across two resources", appRow)
	}
	if _, ok := inventoryRows["owner=web-platform"]; !ok {
		t.Fatalf("inventory rows = %+v, want lowercase owner key", inventoryRows)
	}
	if _, ok := inventoryRows["Owner=payments-team"]; !ok {
		t.Fatalf("inventory rows = %+v, want case-sensitive Owner key", inventoryRows)
	}

	discovered, err := tagRepo.ListDiscoveredKeys(ctx)
	if err != nil {
		t.Fatalf("ListDiscoveredKeys() error = %v", err)
	}
	discoveredKeys := costAllocationKeysByName(discovered)
	if discoveredKeys["app"].ActivationStatus != costAllocationTagStatusDiscovered ||
		discoveredKeys["Owner"].Key != "Owner" ||
		discoveredKeys["owner"].Key != "owner" {
		t.Fatalf("discovered keys = %+v, want discovered app plus case-distinct owner keys", discoveredKeys)
	}

	activated, err := tagRepo.ActivateTag(ctx, CostAllocationTagActivationRequest{
		ID:          "activate-app",
		Key:         "app",
		RequestedAt: "2026-02-02T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("ActivateTag() error = %v", err)
	}
	if activated.ActivationStatus != costAllocationTagStatusActive ||
		activated.ActivatedAt != "2026-02-02T00:00:00Z" ||
		activated.CostExplorerVisibleAt != "2026-02-03T00:00:00Z" ||
		activated.CURExportVisibleAt != "2026-02-03T00:00:00Z" {
		t.Fatalf("activated tag = %+v, want active with 24-hour billing visibility delay", activated)
	}

	active, err := tagRepo.ListActiveKeys(ctx)
	if err != nil {
		t.Fatalf("ListActiveKeys() error = %v", err)
	}
	if len(active) != 1 || active[0].Key != "app" {
		t.Fatalf("active keys = %+v, want app only", active)
	}
	visibleBefore, err := tagRepo.ListBillingVisibleKeys(ctx, "2026-02-02T23:59:59Z")
	if err != nil {
		t.Fatalf("ListBillingVisibleKeys(before) error = %v", err)
	}
	if len(visibleBefore) != 0 {
		t.Fatalf("visible keys before delay = %+v, want none", visibleBefore)
	}
	visibleAfter, err := tagRepo.ListBillingVisibleKeys(ctx, "2026-02-03T00:00:00Z")
	if err != nil {
		t.Fatalf("ListBillingVisibleKeys(after) error = %v", err)
	}
	if len(visibleAfter) != 1 || visibleAfter[0].Key != "app" {
		t.Fatalf("visible keys after delay = %+v, want app", visibleAfter)
	}

	deactivated, err := tagRepo.DeactivateTag(ctx, CostAllocationTagActivationRequest{
		ID:          "deactivate-app",
		Key:         "app",
		RequestedAt: "2026-02-04T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("DeactivateTag() error = %v", err)
	}
	if deactivated.ActivationStatus != costAllocationTagStatusDeactivated ||
		deactivated.DeactivatedAt != "2026-02-04T00:00:00Z" ||
		deactivated.CostExplorerVisibleAt != "" ||
		deactivated.CURExportVisibleAt != "" {
		t.Fatalf("deactivated tag = %+v, want inactive with cleared visibility timestamps", deactivated)
	}

	active, err = tagRepo.ListActiveKeys(ctx)
	if err != nil {
		t.Fatalf("ListActiveKeys(after deactivate) error = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active keys after deactivate = %+v, want none", active)
	}

	events, err := tagRepo.ListActivationEvents(ctx, "app")
	if err != nil {
		t.Fatalf("ListActivationEvents() error = %v", err)
	}
	if len(events) != 2 ||
		events[0].Action != costAllocationTagActionDeactivate ||
		events[1].Action != costAllocationTagActionActivate ||
		events[1].CostExplorerVisibleAt != "2026-02-03T00:00:00Z" {
		t.Fatalf("activation events = %+v, want deactivate then activate history", events)
	}
}

func TestCostAllocationTagRepositoryReportsSpendCoverage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)
	tagRepo := NewCostAllocationTagRepository(db)

	for _, request := range []ResourceCreateRequest{
		{
			ID:           "resource-coverage-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Tagged web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"owner": "platform",
			},
		},
		{
			ID:           "resource-coverage-assets",
			AccountID:    "444455556666",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Cased owner bucket",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"Owner": "platform",
			},
		},
		{
			ID:           "resource-coverage-batch",
			AccountID:    "444455556666",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Missing owner batch",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "payments",
			},
		},
	} {
		if _, err := usageRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}

	for _, request := range []UsageEventCreateRequest{
		{
			ID:                  "usage-coverage-web",
			ResourceID:          "resource-coverage-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-coverage-assets",
			ResourceID:          "resource-coverage-assets",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
		{
			ID:                  "usage-coverage-batch",
			ResourceID:          "resource-coverage-batch",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-03T00:00:00Z",
			UsageEndTime:        "2026-02-03T01:00:00Z",
			UsageQuantityMicros: 1_000_000,
			UsageUnit:           "Hours",
		},
	} {
		if _, err := usageRepo.RecordUsageEvent(ctx, request); err != nil {
			t.Fatalf("RecordUsageEvent(%s) error = %v", request.ID, err)
		}
	}
	if _, err := meteringRepo.GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if _, err := tagRepo.RefreshDiscoveredTags(ctx, "2026-02-04T00:00:00Z"); err != nil {
		t.Fatalf("RefreshDiscoveredTags() error = %v", err)
	}
	if _, err := tagRepo.ActivateTag(ctx, CostAllocationTagActivationRequest{
		ID:          "activate-owner-coverage",
		Key:         "owner",
		RequestedAt: "2026-02-04T00:00:00Z",
	}); err != nil {
		t.Fatalf("ActivateTag(owner) error = %v", err)
	}

	rows, err := tagRepo.ListCoverage(ctx, CostAllocationTagCoverageRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListCoverage() error = %v", err)
	}
	coverage := costAllocationCoverageByDimension(rows)

	owner := coverage["key|owner|owner"]
	if owner.ActivationStatus != costAllocationTagStatusActive ||
		owner.LineItemCount != 3 ||
		owner.ResourceCount != 3 ||
		owner.TotalCostMicros != 132_300 ||
		owner.TaggedCostMicros != 83_200 ||
		owner.UntaggedCostMicros != 41_600 ||
		owner.CaseMismatchCostMicros != 7_500 ||
		owner.TaggedResourceCount != 1 ||
		owner.UntaggedResourceCount != 1 ||
		owner.CaseMismatchResourceCount != 1 ||
		strings.Join(owner.CaseMismatchKeys, ",") != "Owner" {
		t.Fatalf("owner key coverage = %+v, want exact, untagged, and case-mismatched spend", owner)
	}

	account := coverage["account|owner|444455556666"]
	if account.TotalCostMicros != 49_100 ||
		account.TaggedCostMicros != 0 ||
		account.UntaggedCostMicros != 41_600 ||
		account.CaseMismatchCostMicros != 7_500 ||
		account.ResourceCount != 2 {
		t.Fatalf("owner account coverage = %+v, want cased plus untagged spend for account 444455556666", account)
	}

	ec2 := coverage["service|owner|AmazonEC2"]
	if ec2.TotalCostMicros != 124_800 ||
		ec2.TaggedCostMicros != 83_200 ||
		ec2.UntaggedCostMicros != 41_600 ||
		ec2.CaseMismatchCostMicros != 0 {
		t.Fatalf("owner EC2 coverage = %+v, want exact plus untagged EC2 spend", ec2)
	}

	app := coverage["key|app|app"]
	if app.TotalCostMicros != 132_300 ||
		app.TaggedCostMicros != 132_300 ||
		app.UntaggedCostMicros != 0 ||
		app.ResourceCount != 3 ||
		app.TaggedResourceCount != 3 {
		t.Fatalf("app key coverage = %+v, want fully tagged app spend", app)
	}
}

func TestCostAllocationTagRepositoryValidatesLifecycleRequests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	tagRepo := NewCostAllocationTagRepository(db)

	if _, err := tagRepo.RefreshDiscoveredTags(ctx, "not-a-timestamp"); err == nil || !strings.Contains(err.Error(), "must use RFC3339") {
		t.Fatalf("RefreshDiscoveredTags(invalid time) error = %v, want RFC3339 validation", err)
	}
	if _, err := tagRepo.ActivateTag(ctx, CostAllocationTagActivationRequest{Key: "app", RequestedAt: "2026-02-02T00:00:00Z"}); err == nil || !strings.Contains(err.Error(), "has not been discovered") {
		t.Fatalf("ActivateTag(undiscovered) error = %v, want undiscovered key validation", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cost_allocation_tag_keys (
		tag_key,
		first_seen_at,
		last_seen_at,
		discovered_at
	) VALUES (?, ?, ?, ?)`,
		"owner",
		"2026-02-01T00:00:00Z",
		"2026-02-01T00:00:00Z",
		"2026-02-02T00:00:00Z",
	); err != nil {
		t.Fatalf("insert discovered owner key: %v", err)
	}
	if _, err := tagRepo.DeactivateTag(ctx, CostAllocationTagActivationRequest{Key: "owner", RequestedAt: "2026-02-02T00:00:00Z"}); err == nil || !strings.Contains(err.Error(), "is not active") {
		t.Fatalf("DeactivateTag(inactive) error = %v, want active-state validation", err)
	}
	if _, err := tagRepo.ActivateTag(ctx, CostAllocationTagActivationRequest{Key: " "}); err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("ActivateTag(blank key) error = %v, want key validation", err)
	}
	if _, err := tagRepo.ActivateTag(ctx, CostAllocationTagActivationRequest{
		Key:         "app",
		EventSource: "scenario",
	}); err == nil || !strings.Contains(err.Error(), "scenario run ID is required") {
		t.Fatalf("ActivateTag(scenario without provenance) error = %v, want scenario provenance validation", err)
	}
	if _, err := tagRepo.ListActivationEvents(ctx, " "); err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("ListActivationEvents(blank) error = %v, want key validation", err)
	}
	if _, err := tagRepo.ListCoverage(ctx, CostAllocationTagCoverageRequest{
		BillingPeriodStart: "2026-03-01",
		BillingPeriodEnd:   "2026-02-01",
	}); err == nil || !strings.Contains(err.Error(), "billing period start must be before end") {
		t.Fatalf("ListCoverage(invalid period) error = %v, want date-range validation", err)
	}
}

func TestCostAllocationTagSchemaRejectsInvalidLifecycleRows(t *testing.T) {
	t.Parallel()

	db := openTestWorkspace(t)

	assertExecFails(t, db, `INSERT INTO cost_allocation_tag_keys (
		tag_key,
		first_seen_at,
		last_seen_at,
		discovered_at,
		activation_status
	) VALUES (?, ?, ?, ?, ?)`,
		"bad-active",
		"2026-02-01T00:00:00Z",
		"2026-02-01T00:00:00Z",
		"2026-02-02T00:00:00Z",
		costAllocationTagStatusActive,
	)
	assertExecFails(t, db, `INSERT INTO cost_allocation_tag_inventory (
		tag_key,
		tag_value,
		first_seen_at,
		last_seen_at,
		resource_count
	) VALUES (?, ?, ?, ?, ?)`,
		"missing-key",
		"storefront",
		"2026-02-01T00:00:00Z",
		"2026-02-01T00:00:00Z",
		1,
	)
	assertExecFails(t, db, `INSERT INTO cost_allocation_tag_activation_events (
		id,
		tag_key,
		action,
		requested_at,
		effective_at
	) VALUES (?, ?, ?, ?, ?)`,
		"event-invalid-action",
		"missing-key",
		"pause",
		"2026-02-02T00:00:00Z",
		"2026-02-02T00:00:00Z",
	)
}

func costAllocationInventoryByKeyValue(rows []CostAllocationTagInventoryRow) map[string]CostAllocationTagInventoryRow {
	byKeyValue := make(map[string]CostAllocationTagInventoryRow, len(rows))
	for _, row := range rows {
		byKeyValue[row.Key+"="+row.Value] = row
	}
	return byKeyValue
}

func costAllocationKeysByName(keys []CostAllocationTagKey) map[string]CostAllocationTagKey {
	byName := make(map[string]CostAllocationTagKey, len(keys))
	for _, key := range keys {
		byName[key.Key] = key
	}
	return byName
}

func costAllocationCoverageByDimension(rows []CostAllocationTagCoverageRow) map[string]CostAllocationTagCoverageRow {
	byDimension := make(map[string]CostAllocationTagCoverageRow, len(rows))
	for _, row := range rows {
		byDimension[row.Dimension+"|"+row.Key+"|"+row.DimensionValue] = row
	}
	return byDimension
}
