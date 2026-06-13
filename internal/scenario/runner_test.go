package scenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestRunnerErrorClassificationUsesDomainSentinels(t *testing.T) {
	t.Parallel()

	if !isClosedPeriodPricingFailure(fmt.Errorf("daily metering failed: %w", persistence.ErrClosedBillingPeriod)) {
		t.Fatal("isClosedPeriodPricingFailure(wrapped sentinel) = false, want true")
	}
	if isClosedPeriodPricingFailure(errors.New("billing period is closed for payer")) {
		t.Fatal("isClosedPeriodPricingFailure(raw trigger text) = true, want false")
	}
	if !isMissingCostCategory(fmt.Errorf("load category: %w", persistence.ErrCostCategoryNotFound)) {
		t.Fatal("isMissingCostCategory(wrapped sentinel) = false, want true")
	}
	if isMissingCostCategory(errors.New("cost category not found")) {
		t.Fatal("isMissingCostCategory(plain text) = true, want false")
	}
}

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
	if !containsScenarioSeedKey(keys, FirstConsolidatedBillSeedKey) {
		t.Fatalf("SeedDefinitionKeys() = %v, want %q present", keys, FirstConsolidatedBillSeedKey)
	}
	if !containsScenarioSeedKey(keys, MissingTagsSeedKey) {
		t.Fatalf("SeedDefinitionKeys() = %v, want %q present", keys, MissingTagsSeedKey)
	}
	if !containsScenarioSeedKey(keys, SharedNetworkingAllocationSeedKey) {
		t.Fatalf("SeedDefinitionKeys() = %v, want %q present", keys, SharedNetworkingAllocationSeedKey)
	}
	if !containsScenarioSeedKey(keys, PaymentFailureSeedKey) {
		t.Fatalf("SeedDefinitionKeys() = %v, want %q present", keys, PaymentFailureSeedKey)
	}
	if !containsScenarioSeedKey(keys, ForecastBudgetAlertSeedKey) {
		t.Fatalf("SeedDefinitionKeys() = %v, want %q present", keys, ForecastBudgetAlertSeedKey)
	}
	definition, err := LoadSeedDefinition(UntaggedDataTransferSpikeSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition() error = %v", err)
	}
	if definition.Name != "Find the untagged data-transfer spike" || len(definition.Events) != 5 {
		t.Fatalf("packaged scenario definition = %+v, want MVP data-transfer spike fixture", definition)
	}
	definition, err = LoadSeedDefinition(FirstConsolidatedBillSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", FirstConsolidatedBillSeedKey, err)
	}
	if definition.Name != "First consolidated bill" || len(definition.Events) != 8 || definition.Events[0].Action != EventActionCreateAccount {
		t.Fatalf("first consolidated bill definition = %+v, want account-creation lab fixture", definition)
	}
	definition, err = LoadSeedDefinition(MissingTagsSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", MissingTagsSeedKey, err)
	}
	if definition.Name != "Missing Tags" ||
		len(definition.Events) != 10 ||
		definition.Events[8].Action != EventActionRefreshCostAllocationTags ||
		definition.Events[9].Action != EventActionActivateCostAllocationTag ||
		definition.Events[9].TagKey != "owner" {
		t.Fatalf("missing tags definition = %+v, want cost allocation tag lab fixture", definition)
	}
	definition, err = LoadSeedDefinition(SharedNetworkingAllocationSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", SharedNetworkingAllocationSeedKey, err)
	}
	if definition.Name != "Shared Networking allocation" ||
		len(definition.Events) != 13 ||
		len(definition.Checks) != 2 ||
		definition.Events[8].Action != EventActionCreateCostCategory ||
		definition.Events[9].Action != EventActionCreateCostCategoryRule ||
		definition.Events[12].Action != EventActionCreateCostCategorySplitRule {
		t.Fatalf("shared networking allocation definition = %+v, want Cost Category split lab fixture", definition)
	}
	definition, err = LoadSeedDefinition(PaymentFailureSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", PaymentFailureSeedKey, err)
	}
	if definition.Name != "Payment Failure" ||
		len(definition.Events) != 9 ||
		definition.Events[0].Action != EventActionCreatePaymentMethod ||
		definition.Events[5].Action != EventActionSchedulePayment ||
		definition.Events[7].Action != EventActionFailPayment ||
		definition.Events[8].Action != EventActionMarkPaymentDue {
		t.Fatalf("payment failure definition = %+v, want failed-payment lab fixture", definition)
	}
	definition, err = LoadSeedDefinition(ForecastBudgetAlertSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}
	if definition.Name != "Forecast and Budget Alert" ||
		len(definition.Events) != 7 ||
		len(definition.Checks) != 2 ||
		definition.Events[3].UsageStartAt != "2026-02-20T00:00:00Z" ||
		definition.Events[4].Action != EventActionCreateBudget ||
		definition.Events[5].Action != EventActionRefreshBudgetForecasts ||
		definition.Events[6].Action != EventActionCreateSavedReport {
		t.Fatalf("forecast budget alert definition = %+v, want budget forecast lab fixture", definition)
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

func TestRunnerSnapshotsPriceCatalogManifestOnScenarioRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition := parseScenarioDefinitionForTest(t, `{
		"name": "Catalog snapshot scenario",
		"clock": {"start": "2026-02-01"},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "advance",
				"day": 1,
				"action": "advance_clock",
				"amount": 1,
				"unit": "days"
			}
		]
	}`)

	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var catalogID, sourceURL, fetchDate, effectiveDate, regions, compatibilityKey, status, message string
	if err := db.QueryRowContext(ctx, `
		SELECT price_catalog_id,
		       price_catalog_source_url,
		       price_catalog_fetch_date,
		       price_catalog_effective_date,
		       price_catalog_supported_regions,
		       price_catalog_compatibility_key,
		       price_catalog_compatibility_status,
		       price_catalog_compatibility_message
		  FROM scenario_runs
		 WHERE id = ?
	`, result.Run.ID).Scan(&catalogID, &sourceURL, &fetchDate, &effectiveDate, &regions, &compatibilityKey, &status, &message); err != nil {
		t.Fatalf("read scenario run price catalog snapshot: %v", err)
	}
	if catalogID != persistence.SyntheticPriceCatalogID ||
		sourceURL != persistence.SyntheticPriceCatalogSourceURL ||
		fetchDate != persistence.SyntheticPriceCatalogFetchDate ||
		effectiveDate != persistence.SyntheticPriceCatalogEffectiveDate ||
		regions != "global,us-east-1" ||
		compatibilityKey != "scenario-v1" ||
		status != persistence.PriceCatalogCompatibilityCompatible ||
		!strings.Contains(message, "packaged scenario services") {
		t.Fatalf("scenario run catalog snapshot = %q/%q/%q/%q/%q/%q/%q/%q, want synthetic compatibility", catalogID, sourceURL, fetchDate, effectiveDate, regions, compatibilityKey, status, message)
	}
}

func TestRunnerMatchesExistingSavedReportByOwnerRole(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	reportRepo := persistence.NewSavedReportRepository(db)
	managementReport, err := reportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-existing-management-shelf",
		Name:           "Payer overlap report",
		Description:    "Original management shelf report",
		OwnerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("Create(management report) error = %v", err)
	}
	definition := parseScenarioDefinitionForTest(t, `{
		"name": "Finance saved report shelf scenario",
		"clock": {"start": "2026-02-01"},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "create-finance-report",
				"day": 1,
				"action": "create_saved_report",
				"report_name": "Payer overlap report",
				"description": "Scenario finance shelf report",
				"owner_account": "Management",
				"owner_role": "finance",
				"date_range_start": "2026-02-01",
				"date_range_end": "2026-03-01",
				"groupings": [{"type": "dimension", "key": "service"}]
			}
		]
	}`)

	if _, err := NewRunner(db).Run(ctx, definition); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	financeReport, err := reportRepo.GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "finance", "payer overlap report")
	if err != nil {
		t.Fatalf("GetByName(finance report) error = %v", err)
	}
	if financeReport.ID == managementReport.ID || financeReport.OwnerRole != "finance" {
		t.Fatalf("finance report = %+v, want distinct finance-owned saved report", financeReport)
	}
	managementAfterRun, err := reportRepo.Get(ctx, managementReport.ID)
	if err != nil {
		t.Fatalf("Get(management report) error = %v", err)
	}
	if managementAfterRun.Description != "Original management shelf report" {
		t.Fatalf("management report = %+v, want untouched by finance scenario", managementAfterRun)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM saved_reports
		WHERE owner_account_id = ?
		  AND lower(name) = lower(?)`,
		persistence.AnyCompanyRetailManagementAccountID,
		"Payer overlap report"); got != 2 {
		t.Fatalf("same-name payer reports = %d, want management and finance shelves", got)
	}

	if _, err := NewRunner(db).Run(ctx, definition); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	financeAfterRerun, err := reportRepo.GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "finance", "payer overlap report")
	if err != nil {
		t.Fatalf("GetByName(finance rerun report) error = %v", err)
	}
	if financeAfterRerun.ID != financeReport.ID {
		t.Fatalf("finance report ID after rerun = %q, want existing %q rewritten", financeAfterRerun.ID, financeReport.ID)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM saved_reports
		WHERE owner_account_id = ?
		  AND lower(name) = lower(?)`,
		persistence.AnyCompanyRetailManagementAccountID,
		"Payer overlap report"); got != 2 {
		t.Fatalf("same-name payer reports after rerun = %d, want no duplicate finance report", got)
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

func TestRunnerAppliesFirstConsolidatedBillSeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(FirstConsolidatedBillSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", FirstConsolidatedBillSeedKey, err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(first consolidated bill) error = %v", err)
	}
	if result.Run.Status != scenarioRunStatusSucceeded ||
		result.Run.EventsSucceeded != 8 ||
		result.ResourcesCreated != 3 ||
		result.UsageEventsCreated != 5 ||
		result.BillsIssued != 1 {
		t.Fatalf("Run() result = %+v, want successful consolidated bill lab counts", result)
	}

	createdAccount, err := persistence.NewOrganizationRepository(db).GetAccount(ctx, "777788889902")
	if err != nil {
		t.Fatalf("GetAccount(Returns Expansion) error = %v", err)
	}
	if createdAccount.Name != "Returns Expansion" ||
		createdAccount.ParentUnitID != "ou_anycompany_workloads" ||
		createdAccount.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID ||
		createdAccount.BillingVisibilityRole != "member-account" ||
		createdAccount.Status != persistence.AccountStatusActive {
		t.Fatalf("created account = %+v, want active workload member account paid by management", createdAccount)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM account_lifecycle_events
		WHERE account_id = ?
		  AND event_type = 'created'
		  AND event_source = 'scenario'
		  AND scenario_run_id = ?
		  AND scenario_event_id = ?`, "777788889902", result.Run.ID, "create-returns-account"); got != 1 {
		t.Fatalf("scenario account lifecycle rows = %d, want 1 created row with run lineage", got)
	}

	var billID, billState, invoiceID string
	var billLineItemCount int
	var billTotalMicros int64
	if err := db.QueryRowContext(ctx, `SELECT
			b.id,
			b.bill_state,
			b.line_item_count,
			b.total_micros,
			o.invoice_id
		FROM bills b
		JOIN invoice_obligations o ON o.bill_id = b.id
		WHERE b.billing_period_start = ?
		  AND b.billing_period_end = ?
		  AND b.payer_account_id = ?`,
		"2026-03-01",
		"2026-04-01",
		persistence.AnyCompanyRetailManagementAccountID,
	).Scan(&billID, &billState, &billLineItemCount, &billTotalMicros, &invoiceID); err != nil {
		t.Fatalf("read issued consolidated bill: %v", err)
	}
	if billID == "" || invoiceID == "" || billState != "issued" || billLineItemCount == 0 || billTotalMicros == 0 {
		t.Fatalf("bill/invoice = %q/%q/%q/%d/%d, want issued nonzero consolidated bill", billID, invoiceID, billState, billLineItemCount, billTotalMicros)
	}

	document, err := persistence.NewInvoiceDocumentRepository(db).GetByBillID(ctx, billID)
	if err != nil {
		t.Fatalf("GetByBillID(%q) error = %v", billID, err)
	}
	if document.InvoiceID != invoiceID ||
		document.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID ||
		document.LineItemCount != billLineItemCount ||
		document.TotalMicros != billTotalMicros {
		t.Fatalf("invoice document = %+v, want durable invoice matching bill %q", document, billID)
	}

	for _, accountID := range []string{"777788889902", "111122223333", "222233334444"} {
		var lineItemCount int
		var totalMicros int64
		if err := db.QueryRowContext(ctx, `SELECT
				COUNT(*),
				COALESCE(SUM(unblended_cost_micros), 0)
			FROM bill_line_items
			WHERE billing_period_start = ?
			  AND billing_period_end = ?
			  AND payer_account_id = ?
			  AND usage_account_id = ?
			  AND line_item_status = 'final'`,
			"2026-03-01",
			"2026-04-01",
			persistence.AnyCompanyRetailManagementAccountID,
			accountID,
		).Scan(&lineItemCount, &totalMicros); err != nil {
			t.Fatalf("read final line items for usage account %s: %v", accountID, err)
		}
		if lineItemCount == 0 || totalMicros == 0 {
			t.Fatalf("usage account %s final bill lines = %d/%d, want nonzero consolidated charges", accountID, lineItemCount, totalMicros)
		}
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM bill_line_items
		WHERE billing_period_start = ?
		  AND billing_period_end = ?
		  AND line_item_status = 'final'
		  AND payer_account_id <> ?`,
		"2026-03-01",
		"2026-04-01",
		persistence.AnyCompanyRetailManagementAccountID); got != 0 {
		t.Fatalf("final line items with non-management payer = %d, want all charges consolidated under management", got)
	}
}

func TestRunnerAppliesMissingTagsSeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(MissingTagsSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", MissingTagsSeedKey, err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(missing tags) error = %v", err)
	}
	if result.Run.Status != scenarioRunStatusSucceeded ||
		result.Run.EventsSucceeded != 10 ||
		result.ResourcesCreated != 4 ||
		result.UsageEventsCreated != 4 ||
		result.MeteringRecordsCreated != 4 ||
		result.BillLineItemsCreated != 5 ||
		result.BillsIssued != 0 {
		t.Fatalf("Run() result = %+v, want successful missing tags lab counts", result)
	}

	tagRepo := persistence.NewCostAllocationTagRepository(db)
	keys, err := tagRepo.ListDiscoveredKeys(ctx)
	if err != nil {
		t.Fatalf("ListDiscoveredKeys() error = %v", err)
	}
	discovered := costAllocationKeysByNameForScenario(keys)
	owner := discovered["owner"]
	if owner.Key != "owner" ||
		owner.ActivationStatus != "active" ||
		owner.ActivatedAt != "2026-03-30T00:00:00Z" ||
		owner.CostExplorerVisibleAt != "2026-03-31T00:00:00Z" ||
		owner.ScenarioRunID != result.Run.ID ||
		owner.ScenarioEventID != "activate-owner-tag" ||
		owner.ScenarioEventSequence != 10 {
		t.Fatalf("owner key = %+v, want active scenario-owned key pending 24-hour visibility", owner)
	}
	if discovered["Owner"].Key != "Owner" || discovered["Owner"].ActivationStatus != "discovered" {
		t.Fatalf("Owner key = %+v, want case-distinct discovered key for mismatch lesson", discovered["Owner"])
	}

	visibleBefore, err := tagRepo.ListBillingVisibleKeys(ctx, "2026-03-30T23:59:59Z")
	if err != nil {
		t.Fatalf("ListBillingVisibleKeys(before) error = %v", err)
	}
	if len(visibleBefore) != 0 {
		t.Fatalf("visible keys before delay = %+v, want none", visibleBefore)
	}
	visibleAfter, err := tagRepo.ListBillingVisibleKeys(ctx, "2026-03-31T00:00:00Z")
	if err != nil {
		t.Fatalf("ListBillingVisibleKeys(after) error = %v", err)
	}
	if len(visibleAfter) != 1 || visibleAfter[0].Key != "owner" {
		t.Fatalf("visible keys after delay = %+v, want owner", visibleAfter)
	}

	events, err := tagRepo.ListActivationEvents(ctx, "owner")
	if err != nil {
		t.Fatalf("ListActivationEvents(owner) error = %v", err)
	}
	if len(events) != 1 ||
		events[0].Action != "activate" ||
		events[0].EventSource != "scenario" ||
		events[0].ScenarioRunID != result.Run.ID ||
		events[0].ScenarioEventID != "activate-owner-tag" ||
		events[0].ScenarioEventSequence != 10 {
		t.Fatalf("owner activation events = %+v, want one scenario activation event", events)
	}

	coverageRows, err := tagRepo.ListCoverage(ctx, persistence.CostAllocationTagCoverageRequest{
		BillingPeriodStart: "2026-03-01",
		BillingPeriodEnd:   "2026-04-01",
	})
	if err != nil {
		t.Fatalf("ListCoverage() error = %v", err)
	}
	ownerCoverage := scenarioTagCoverageRow(coverageRows, "owner", persistence.CostAllocationCoverageDimensionKey, "owner")
	if ownerCoverage.ActivationStatus != "active" ||
		ownerCoverage.LineItemCount != 5 ||
		ownerCoverage.ResourceCount != 4 ||
		ownerCoverage.TaggedLineItemCount != 1 ||
		ownerCoverage.TaggedResourceCount != 1 ||
		ownerCoverage.CaseMismatchLineItemCount != 1 ||
		ownerCoverage.CaseMismatchResourceCount != 1 ||
		strings.Join(ownerCoverage.CaseMismatchKeys, ",") != "Owner" ||
		ownerCoverage.UntaggedLineItemCount != 3 ||
		ownerCoverage.UntaggedResourceCount != 2 ||
		ownerCoverage.TaggedCostMicros == 0 ||
		ownerCoverage.CaseMismatchCostMicros == 0 ||
		ownerCoverage.UntaggedCostMicros == 0 {
		t.Fatalf("owner coverage = %+v, want exact, case-mismatched, and missing owner spend", ownerCoverage)
	}

	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM bill_line_items
		WHERE billing_period_start = ?
		  AND billing_period_end = ?
		  AND resource_id IS NOT NULL
		  AND json_extract(tag_snapshot_json, '$.owner') IS NULL
		  AND json_extract(tag_snapshot_json, '$.Owner') IS NULL`,
		"2026-03-01",
		"2026-04-01"); got != 2 {
		t.Fatalf("missing owner line items = %d, want analytics and data-transfer rows", got)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM bill_line_items
		WHERE billing_period_start = ?
		  AND billing_period_end = ?
		  AND json_extract(tag_snapshot_json, '$.Owner') IS NOT NULL`,
		"2026-03-01",
		"2026-04-01"); got != 1 {
		t.Fatalf("case-mismatched Owner line items = %d, want payments row", got)
	}
}

func TestRunnerAppliesSharedNetworkingAllocationSeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(SharedNetworkingAllocationSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", SharedNetworkingAllocationSeedKey, err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(shared networking allocation) error = %v", err)
	}
	if result.Run.Status != scenarioRunStatusSucceeded ||
		result.Run.EventsSucceeded != 13 ||
		result.ResourcesCreated != 4 ||
		result.UsageEventsCreated != 4 ||
		result.MeteringRecordsCreated != 4 ||
		result.BillLineItemsCreated != 5 ||
		result.BillsIssued != 0 {
		t.Fatalf("Run() result = %+v, want successful shared networking allocation lab counts", result)
	}

	categoryRepo := persistence.NewCostCategoryRepository(db)
	product, err := categoryRepo.GetCategoryByName(ctx, "Product")
	if err != nil {
		t.Fatalf("GetCategoryByName(Product) error = %v", err)
	}
	if product.DefaultValue != "Unallocated" {
		t.Fatalf("Product category = %+v, want Unallocated default", product)
	}
	rules, err := categoryRepo.ListRules(ctx, product.ID)
	if err != nil {
		t.Fatalf("ListRules(Product) error = %v", err)
	}
	if len(rules) != 3 ||
		rules[0].Value != "Storefront" ||
		rules[1].Value != "Payments" ||
		rules[2].Value != "Shared Networking" ||
		rules[2].Conditions[0].Dimension != persistence.CostCategoryRuleMatchAccount ||
		strings.Join(rules[2].Conditions[0].Values, ",") != "222233334444" {
		t.Fatalf("Product rules = %+v, want Storefront, Payments, and Shared Networking account rules", rules)
	}

	var sourceCostMicros int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(unblended_cost_micros), 0)
		FROM cost_category_line_item_assignments
		WHERE billing_period_start = ?
		  AND billing_period_end = ?
		  AND cost_category_id = ?
		  AND assigned_value = ?`,
		"2026-03-01",
		"2026-04-01",
		product.ID,
		"Shared Networking",
	).Scan(&sourceCostMicros); err != nil {
		t.Fatalf("read Shared Networking assigned source cost: %v", err)
	}
	if sourceCostMicros == 0 {
		t.Fatal("Shared Networking assigned source cost = 0, want NAT Gateway and data-transfer spend")
	}
	for _, serviceCode := range []string{"AmazonVPCNATGateway", "AWSDataTransfer"} {
		if got := countScenarioRows(t, db, `SELECT COUNT(*)
			FROM cost_category_line_item_assignments a
			JOIN bill_line_items li ON li.id = a.line_item_id
			WHERE a.billing_period_start = ?
			  AND a.billing_period_end = ?
			  AND a.cost_category_id = ?
			  AND a.assigned_value = ?
			  AND li.service_code = ?`,
			"2026-03-01",
			"2026-04-01",
			product.ID,
			"Shared Networking",
			serviceCode); got != 1 {
			t.Fatalf("Shared Networking assigned %s rows = %d, want 1", serviceCode, got)
		}
	}

	splitRepo := persistence.NewCostCategorySplitChargeRepository(db)
	splitRules, err := splitRepo.ListRules(ctx, product.ID)
	if err != nil {
		t.Fatalf("ListRules(Product split) error = %v", err)
	}
	if len(splitRules) != 1 ||
		splitRules[0].SourceValue != "Shared Networking" ||
		splitRules[0].Method != persistence.CostCategorySplitMethodFixed ||
		len(splitRules[0].Targets) != 2 {
		t.Fatalf("split rules = %+v, want one fixed Shared Networking rule", splitRules)
	}

	comparison, err := splitRepo.CompareAllocations(ctx, persistence.CostCategorySplitChargeComparisonRequest{
		CostCategoryID:     product.ID,
		BillingPeriodStart: "2026-03-01",
		BillingPeriodEnd:   "2026-04-01",
	})
	if err != nil {
		t.Fatalf("CompareAllocations(Product) error = %v", err)
	}
	if comparison.SplitOutCostMicros != sourceCostMicros ||
		comparison.SplitInCostMicros != sourceCostMicros ||
		comparison.UnallocatedResidualCostMicros != 0 {
		t.Fatalf("comparison = %+v, want fully reallocated Shared Networking source cost %d", comparison, sourceCostMicros)
	}
	storefront := scenarioSplitComparisonRow(comparison.Rows, "Storefront")
	payments := scenarioSplitComparisonRow(comparison.Rows, "Payments")
	shared := scenarioSplitComparisonRow(comparison.Rows, "Shared Networking")
	if storefront.SplitInCostMicros != sourceCostMicros*6/10 ||
		payments.SplitInCostMicros != sourceCostMicros*4/10 ||
		shared.SplitOutCostMicros != sourceCostMicros ||
		shared.TotalAllocatedCostMicros != 0 {
		t.Fatalf("split rows = storefront %+v payments %+v shared %+v, want 60/40 target allocation and zero shared total", storefront, payments, shared)
	}
}

func TestRunnerAppliesPaymentFailureSeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(PaymentFailureSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", PaymentFailureSeedKey, err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(payment failure) error = %v", err)
	}
	if result.Run.Status != scenarioRunStatusSucceeded ||
		result.Run.EventsSucceeded != 9 ||
		result.ResourcesCreated != 1 ||
		result.UsageEventsCreated != 1 ||
		result.MeteringRecordsCreated != 1 ||
		result.BillLineItemsCreated != 2 ||
		result.BillsIssued != 1 {
		t.Fatalf("Run() result = %+v, want successful payment failure lab counts", result)
	}

	var paymentMethodStatus string
	var paymentMethodDefault int
	if err := db.QueryRowContext(ctx, `SELECT status, is_default
		FROM payment_methods
		WHERE display_name = ?
		  AND account_last4 = ?`,
		"Default corporate card",
		"4242",
	).Scan(&paymentMethodStatus, &paymentMethodDefault); err != nil {
		t.Fatalf("read scenario default payment method: %v", err)
	}
	if paymentMethodStatus != "active" || paymentMethodDefault != 1 {
		t.Fatalf("scenario payment method = %q/%d, want active default", paymentMethodStatus, paymentMethodDefault)
	}

	var billID, billState, obligationID, invoiceID, paymentStatus string
	var amountDueMicros int64
	if err := db.QueryRowContext(ctx, `SELECT
			b.id,
			b.bill_state,
			o.id,
			o.invoice_id,
			ps.status,
			ps.amount_due_micros
		FROM bills b
		JOIN invoice_obligations o ON o.bill_id = b.id
		JOIN invoice_payment_states ps ON ps.invoice_obligation_id = o.id
		WHERE b.billing_period_start = ?
		  AND b.billing_period_end = ?
		  AND b.payer_account_id = ?`,
		"2026-03-01",
		"2026-04-01",
		persistence.AnyCompanyRetailManagementAccountID,
	).Scan(&billID, &billState, &obligationID, &invoiceID, &paymentStatus, &amountDueMicros); err != nil {
		t.Fatalf("read payment failure invoice state: %v", err)
	}
	if billID == "" || obligationID == "" || invoiceID == "" ||
		billState != "issued" ||
		paymentStatus != "due" ||
		amountDueMicros <= 0 {
		t.Fatalf("invoice state = bill %q/%q obligation %q invoice %q payment %q due %d, want issued bill with due payment after failed retry setup", billID, billState, obligationID, invoiceID, paymentStatus, amountDueMicros)
	}

	events, err := persistence.NewPaymentLifecycleRepository(db).ListEvents(ctx, obligationID, 10)
	if err != nil {
		t.Fatalf("ListEvents(%q) error = %v", obligationID, err)
	}
	transitionReasons := map[string]string{}
	for _, event := range events {
		transitionReasons[event.TransitionKind] = event.Reason
	}
	for _, transition := range []string{"created", "scheduled", "processing", "failed", "due"} {
		if _, ok := transitionReasons[transition]; !ok {
			t.Fatalf("payment history transitions = %+v, want %q", transitionReasons, transition)
		}
	}
	if !strings.Contains(transitionReasons["failed"], "Default corporate card 4242 was declined") {
		t.Fatalf("failed payment reason = %q, want default-card decline detail", transitionReasons["failed"])
	}
	if !strings.Contains(transitionReasons["due"], "learner retry") {
		t.Fatalf("due payment reason = %q, want retry guidance", transitionReasons["due"])
	}
}

func TestRunnerPreflightsClosedBillingPeriodBeforeScenarioMutations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	firstDefinition, err := LoadSeedDefinition(FirstConsolidatedBillSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", FirstConsolidatedBillSeedKey, err)
	}
	if _, err := NewRunner(db).Run(ctx, firstDefinition); err != nil {
		t.Fatalf("Run(first consolidated bill) error = %v", err)
	}

	paymentDefinition, err := LoadSeedDefinition(PaymentFailureSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", PaymentFailureSeedKey, err)
	}
	result, err := NewRunner(db).Run(ctx, paymentDefinition)
	if err == nil {
		t.Fatal("Run(payment failure after closed March) error = nil, want preflight conflict")
	}
	wantMessage := "Cannot price March 2026 usage because billing period 2026-03-01 to 2026-04-01 is already closed for payer 999988887777. Reset or clone the workspace before launching this scenario."
	if !strings.Contains(err.Error(), wantMessage) {
		t.Fatalf("Run() error = %q, want learner-facing closed-period message", err.Error())
	}
	for _, leaked := range []string{"constraint failed", "1811", "billing period is closed for payer"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("Run() error = %q, should not expose raw trigger detail %q", err.Error(), leaked)
		}
	}
	if result.Run.Status != scenarioRunStatusFailed ||
		result.Run.CurrentEventID != "meter-payment-failure-month" ||
		result.Run.EventsSucceeded != 0 ||
		result.ResourcesCreated != 0 ||
		result.UsageEventsCreated != 0 ||
		result.MeteringRecordsCreated != 0 ||
		result.BillLineItemsCreated != 0 {
		t.Fatalf("failed run = %+v, want preflight failure before scenario mutations", result.Run)
	}

	var runStatus, runError, progressState string
	if err := db.QueryRowContext(ctx, `SELECT status, error_message FROM scenario_runs WHERE id = ?`, result.Run.ID).Scan(&runStatus, &runError); err != nil {
		t.Fatalf("read failed scenario run: %v", err)
	}
	if runStatus != scenarioRunStatusFailed || !strings.Contains(runError, wantMessage) {
		t.Fatalf("persisted run = %q/%q, want failed learner-facing preflight message", runStatus, runError)
	}
	if err := db.QueryRowContext(ctx, `SELECT current_objective_state FROM scenario_learner_progress WHERE scenario_run_id = ?`, result.Run.ID).Scan(&progressState); err != nil {
		t.Fatalf("read failed learner progress: %v", err)
	}
	if progressState != persistence.ScenarioProgressStateFailed {
		t.Fatalf("failed learner progress state = %q, want failed", progressState)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*) FROM scenario_run_events WHERE scenario_run_id = ?`, result.Run.ID); got != 0 {
		t.Fatalf("failed run scenario events = %d, want preflight failure before event execution", got)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*) FROM resources WHERE scenario_run_id = ?`, result.Run.ID); got != 0 {
		t.Fatalf("failed run resources = %d, want none", got)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*) FROM usage_events WHERE scenario_run_id = ?`, result.Run.ID); got != 0 {
		t.Fatalf("failed run usage events = %d, want none", got)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM metering_records m
		JOIN usage_events u ON u.id = m.usage_event_id
		WHERE u.scenario_run_id = ?`, result.Run.ID); got != 0 {
		t.Fatalf("failed run metering records = %d, want none", got)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM bill_line_items li
		JOIN usage_events u ON u.id = li.usage_event_id
		WHERE u.scenario_run_id = ?`, result.Run.ID); got != 0 {
		t.Fatalf("failed run bill line items = %d, want none", got)
	}
}

func TestRunnerAppliesForecastBudgetAlertSeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(ForecastBudgetAlertSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}

	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(forecast budget alert) error = %v", err)
	}
	if result.Run.Status != scenarioRunStatusSucceeded ||
		result.Run.EventsSucceeded != 7 ||
		result.ResourcesCreated != 1 ||
		result.UsageEventsCreated != 2 ||
		result.MeteringRecordsCreated != 1 ||
		result.BillLineItemsCreated != 2 ||
		result.BillsIssued != 0 {
		t.Fatalf("Run() result = %+v, want successful forecast budget alert lab counts", result)
	}

	budgetRepo := persistence.NewBudgetRepository(db)
	budget, err := budgetRepo.GetBudget(ctx, "budget-scn-storefront-forecast-alert")
	if err != nil {
		t.Fatalf("GetBudget(forecast alert) error = %v", err)
	}
	if budget.Name != "Storefront February forecast guardrail" ||
		budget.BillingPeriodStart != "2026-02-01" ||
		budget.BillingPeriodEnd != "2026-03-01" ||
		budget.BudgetAmountMicros != 3_000_000 ||
		budget.ScopeType != persistence.BudgetScopeAccount ||
		budget.ScopeValue != "111122223333" ||
		len(budget.Thresholds) != 2 {
		t.Fatalf("budget = %+v, want Storefront account forecast guardrail", budget)
	}

	summaries, err := budgetRepo.ListForecastSummaries(ctx, persistence.BudgetForecastSummaryListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListForecastSummaries() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("forecast summaries = %+v, want one Storefront summary", summaries)
	}
	summary := summaries[0]
	if summary.BudgetID != budget.ID ||
		summary.CurrentTime != "2026-02-11T00:00:00Z" ||
		summary.ElapsedDays != 10 ||
		summary.PeriodDays != 28 ||
		summary.ActualCostMicros != 998_400 ||
		summary.RunRateForecastMicros != 2_795_520 ||
		summary.ScheduledEventCostMicros != 499_200 ||
		summary.ForecastCostMicros != 3_294_720 ||
		summary.LineItemCount != 1 ||
		summary.ScheduledUsageEventCount != 1 {
		t.Fatalf("forecast summary = %+v, want actual run-rate plus scheduled future EC2 usage", summary)
	}

	evaluations, err := budgetRepo.EvaluateBudgets(ctx, persistence.BudgetEvaluationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("EvaluateBudgets() error = %v", err)
	}
	if len(evaluations) != 1 {
		t.Fatalf("budget evaluations = %+v, want one Storefront budget", evaluations)
	}
	checks := evaluations[0].ThresholdChecks
	if len(checks) != 2 ||
		checks[0].ThresholdType != persistence.BudgetThresholdTypeActual ||
		checks[0].Breached ||
		checks[1].ThresholdType != persistence.BudgetThresholdTypeForecast ||
		!checks[1].Breached ||
		checks[1].SpendMicros != 3_294_720 {
		t.Fatalf("threshold checks = %+v, want actual OK and forecast breached", checks)
	}

	alerts, err := budgetRepo.ListAlertNotifications(ctx, persistence.BudgetAlertNotificationListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListAlertNotifications() error = %v", err)
	}
	if len(alerts) != 1 ||
		alerts[0].BudgetID != budget.ID ||
		alerts[0].ThresholdType != persistence.BudgetThresholdTypeForecast ||
		alerts[0].SpendMicros != 3_294_720 ||
		!strings.Contains(alerts[0].Message, "forecast threshold crossed") {
		t.Fatalf("alert notifications = %+v, want one forecast breach alert", alerts)
	}

	report, err := persistence.NewSavedReportRepository(db).Get(ctx, "saved-report-scn-storefront-forecast-drilldown")
	if err != nil {
		t.Fatalf("Get(saved report) error = %v", err)
	}
	if report.Name != "Storefront forecast spike drilldown" ||
		report.OwnerAccountID != persistence.AnyCompanyRetailManagementAccountID ||
		report.OwnerRole != "management-account" ||
		report.DateRangeStart != "2026-02-01" ||
		report.DateRangeEnd != "2026-03-01" ||
		strings.Join(report.Filters["linked_account"], ",") != "111122223333" ||
		len(report.Groupings) != 2 ||
		report.Groupings[0].Key != "service" ||
		report.Groupings[1].Key != "usage_type" {
		t.Fatalf("saved report = %+v, want Storefront service/usage-type drilldown", report)
	}

	if got := countScenarioRows(t, db, `SELECT COUNT(*)
		FROM usage_events u
		LEFT JOIN bill_line_items b ON b.usage_event_id = u.id
		WHERE u.scenario_event_id = ?
		  AND u.usage_start_time = ?
		  AND b.id IS NULL`,
		"scheduled-storefront-scale-up",
		"2026-02-20T00:00:00Z"); got != 1 {
		t.Fatalf("unmetered scheduled forecast usage rows = %d, want 1", got)
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
	progressRepo := persistence.NewScenarioLearnerProgressRepository(db)
	progress, err := progressRepo.Get(ctx, result.Run.ID)
	if err != nil {
		t.Fatalf("Get(failed progress) error = %v", err)
	}
	if progress.CurrentObjectiveState != persistence.ScenarioProgressStateFailed ||
		progress.ActionsCompleted != 0 ||
		progress.CurrentObjective != "Resolve scenario setup failure" {
		t.Fatalf("failed progress = %+v, want failed objective state", progress)
	}
	actions, err := progressRepo.ListActions(ctx, result.Run.ID)
	if err != nil {
		t.Fatalf("ListActions(failed) error = %v", err)
	}
	if len(actions) != 1 ||
		actions[0].ActionID != "generate-missing" ||
		actions[0].ActionStatus != persistence.ScenarioLearnerActionStatusFailed ||
		!strings.Contains(actions[0].ErrorMessage, "was not created before generate_usage") {
		t.Fatalf("failed progress actions = %+v, want failed generate-missing action", actions)
	}
}

func TestRunnerResetsOrganizationTemplateBeforeEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	organizationRepo := persistence.NewOrganizationRepository(db)
	if _, err := organizationRepo.CreateAccount(ctx, persistence.AccountCreateRequest{
		ID:             "777788889999",
		OrganizationID: persistence.AnyCompanyRetailOrganizationID,
		ParentUnitID:   "ou_anycompany_sandbox",
		Name:           "Scenario Drift Account",
		Email:          "scenario-drift@anycompany.example",
		EffectiveAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	if _, err := organizationRepo.MoveAccount(ctx, persistence.AccountMoveRequest{
		AccountID:    "111122223333",
		ParentUnitID: "ou_anycompany_sandbox",
		EffectiveAt:  "2026-02-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("MoveAccount(Storefront Prod) error = %v", err)
	}
	if _, err := organizationRepo.SuspendAccount(ctx, persistence.AccountSuspendRequest{
		AccountID:   "111122223333",
		EffectiveAt: "2026-02-03T00:00:00Z",
	}); err != nil {
		t.Fatalf("SuspendAccount(Storefront Prod) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE account_tags SET tag_value = ? WHERE account_id = ? AND tag_key = ?`, "drifted-owner", "111122223333", "owner"); err != nil {
		t.Fatalf("update account tag drift: %v", err)
	}
	if _, err := persistence.NewResourceUsageRepository(db).CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "preserved-runner-reset-resource",
		AccountID:    "777788889999",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonS3",
		ResourceType: "bucket",
		ResourceName: "runner-reset-preserved-resource",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}

	definition := parseScenarioDefinitionForTest(t, `{
		"name": "Reset organization before events",
		"clock": {"start": "2026-03-01"},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "storefront-usage",
				"day": 1,
				"action": "add_usage",
				"account": "Storefront Prod",
				"service": "Amazon S3",
				"amount_gb": 1
			}
		]
	}`)
	result, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Run.Status != scenarioRunStatusSucceeded || result.ResourcesCreated != 1 || result.UsageEventsCreated != 1 {
		t.Fatalf("Run() result = %+v, want successful usage after organization reset", result)
	}

	accounts, err := organizationRepo.ListAccounts(ctx, persistence.AnyCompanyRetailOrganizationID)
	if err != nil {
		t.Fatalf("ListAccounts() after scenario reset error = %v", err)
	}
	if len(accounts) != 13 {
		t.Fatalf("account count after scenario reset = %d, want 13", len(accounts))
	}
	byName := scenarioAccountsByName(accounts)
	if _, ok := byName["Scenario Drift Account"]; ok {
		t.Fatalf("scenario reset retained drift account: %+v", accounts)
	}
	storefrontProd := byName["Storefront Prod"]
	if storefrontProd.ParentUnitID != "ou_anycompany_workloads" ||
		storefrontProd.Status != persistence.AccountStatusActive ||
		storefrontProd.Owner != "storefront-team" {
		t.Fatalf("Storefront Prod after scenario reset = %+v, want seed OU/status/account tags", storefrontProd)
	}
	if got := countScenarioRows(t, db, `SELECT COUNT(*) FROM resources WHERE id = ?`, "preserved-runner-reset-resource"); got != 1 {
		t.Fatalf("preserved resource count = %d, want 1", got)
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

	progressRepo := persistence.NewScenarioLearnerProgressRepository(db)
	progress, err := progressRepo.Get(ctx, result.Run.ID)
	if err != nil {
		t.Fatalf("Get(progress) error = %v", err)
	}
	wantProgressState := persistence.ScenarioProgressStateCompleted
	if len(definition.Checks) > 0 {
		wantProgressState = persistence.ScenarioProgressStateInProgress
	}
	if progress.CurrentObjectiveState != wantProgressState ||
		progress.ActionsTotal != len(definition.Events) ||
		progress.ActionsCompleted != len(definition.Events) ||
		progress.ChecksTotal != len(definition.Checks) {
		t.Fatalf("scenario progress = %+v, want completed actions and state %q", progress, wantProgressState)
	}
	progressActions, err := progressRepo.ListActions(ctx, result.Run.ID)
	if err != nil {
		t.Fatalf("ListActions(progress) error = %v", err)
	}
	if len(progressActions) != len(definition.Events) ||
		progressActions[0].ActionID != "create-assets" ||
		!strings.Contains(progressActions[len(progressActions)-1].Evidence, "bill=") {
		t.Fatalf("scenario progress actions = %+v, want action evidence in scenario order", progressActions)
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

func scenarioAccountsByName(accounts []persistence.OrganizationAccount) map[string]persistence.OrganizationAccount {
	byName := make(map[string]persistence.OrganizationAccount, len(accounts))
	for _, account := range accounts {
		byName[account.Name] = account
	}
	return byName
}

func costAllocationKeysByNameForScenario(keys []persistence.CostAllocationTagKey) map[string]persistence.CostAllocationTagKey {
	byName := make(map[string]persistence.CostAllocationTagKey, len(keys))
	for _, key := range keys {
		byName[key.Key] = key
	}
	return byName
}

func scenarioTagCoverageRow(rows []persistence.CostAllocationTagCoverageRow, key, dimension, dimensionValue string) persistence.CostAllocationTagCoverageRow {
	for _, row := range rows {
		if row.Key == key && row.Dimension == dimension && row.DimensionValue == dimensionValue {
			return row
		}
	}
	return persistence.CostAllocationTagCoverageRow{}
}

func scenarioSplitComparisonRow(rows []persistence.CostCategorySplitChargeComparisonRow, value string) persistence.CostCategorySplitChargeComparisonRow {
	for _, row := range rows {
		if row.Value == value {
			return row
		}
	}
	return persistence.CostCategorySplitChargeComparisonRow{}
}

func countScenarioRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var count int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows with %q: %v", query, err)
	}
	return count
}
