package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func seedCostCategoryPreviewSpend(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	usageRepo := persistence.NewResourceUsageRepository(db)
	for _, request := range []persistence.ResourceCreateRequest{
		{
			ID:           "resource-cost-category-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonEC2",
			ResourceType: "ec2_instance",
			ResourceName: "Cost category web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		{
			ID:           "resource-cost-category-bucket",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonS3",
			ResourceType: "s3_bucket",
			ResourceName: "Cost category bucket",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
	} {
		if _, err := usageRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}
	for _, request := range []persistence.UsageEventCreateRequest{
		{
			ID:                  "usage-cost-category-web",
			ResourceID:          "resource-cost-category-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-cost-category-bucket",
			ResourceID:          "resource-cost-category-bucket",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	} {
		if _, err := usageRepo.RecordUsageEvent(ctx, request); err != nil {
			t.Fatalf("RecordUsageEvent(%s) error = %v", request.ID, err)
		}
	}
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
}

func readCostCategoryID(t *testing.T, db *sql.DB, name string) string {
	t.Helper()

	var id string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM cost_categories WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("read cost category %q ID: %v", name, err)
	}
	return id
}

func readBudgetGeneratedStateFingerprint(t *testing.T, ctx context.Context, db *sql.DB, periodStart, periodEnd string) string {
	t.Helper()

	repo := persistence.NewBudgetRepository(db)
	forecasts, err := repo.ListForecastSummaries(ctx, persistence.BudgetForecastSummaryListRequest{
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
	})
	if err != nil {
		t.Fatalf("ListForecastSummaries(%s to %s) error = %v", periodStart, periodEnd, err)
	}
	alerts, err := repo.ListAlertNotifications(ctx, persistence.BudgetAlertNotificationListRequest{
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
	})
	if err != nil {
		t.Fatalf("ListAlertNotifications(%s to %s) error = %v", periodStart, periodEnd, err)
	}
	return fmt.Sprintf("forecasts=%#v\nalerts=%#v", forecasts, alerts)
}

// requireCostCategoryAssignmentByLineItem returns the persisted category assignment for one billed line item.
func requireCostCategoryAssignmentByLineItem(t *testing.T, assignments []persistence.CostCategoryLineItemAssignment, lineItemID string) persistence.CostCategoryLineItemAssignment {
	t.Helper()

	for _, assignment := range assignments {
		if assignment.LineItemID == lineItemID {
			return assignment
		}
	}
	t.Fatalf("cost category assignments = %+v, want line item %q", assignments, lineItemID)
	return persistence.CostCategoryLineItemAssignment{}
}

func postClockAdvance(t *testing.T, client *http.Client, serverURL, amount, unit string) string {
	t.Helper()

	resp, err := client.PostForm(serverURL+"/clock/advance", url.Values{
		"clock_advance_amount": {amount},
		"clock_advance_unit":   {unit},
	})
	if err != nil {
		t.Fatalf("POST /clock/advance error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /clock/advance final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	return body
}

// postTagDiscoveryRefresh runs the explicit tag discovery action and returns the rendered manager page.
func postTagDiscoveryRefresh(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()

	resp, err := client.PostForm(serverURL+"/tags/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /tags/refresh error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tags/refresh final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	return body
}

// readCostAllocationTagDiscoveryCounts reports persisted discovery rows without triggering refresh logic.
func readCostAllocationTagDiscoveryCounts(t *testing.T, ctx context.Context, db *sql.DB) (int, int) {
	t.Helper()

	var keyCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cost_allocation_tag_keys`).Scan(&keyCount); err != nil {
		t.Fatalf("count cost_allocation_tag_keys: %v", err)
	}
	var valueCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cost_allocation_tag_inventory`).Scan(&valueCount); err != nil {
		t.Fatalf("count cost_allocation_tag_inventory: %v", err)
	}
	return keyCount, valueCount
}

func readResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}

// assertHEADDownloadMatchesGET verifies HEAD download routes keep GET headers without writing body bytes.
func assertHEADDownloadMatchesGET(t *testing.T, handler http.Handler, target string, getHeader http.Header, headerNames ...string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodHead, target, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read HEAD %s body: %v", target, err)
		}
		t.Fatalf("HEAD %s status = %d, want %d; body=%s", target, resp.StatusCode, http.StatusOK, body)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("HEAD %s wrote %d response body bytes, want none", target, recorder.Body.Len())
	}
	for _, name := range headerNames {
		if got, want := resp.Header.Get(name), getHeader.Get(name); got != want {
			t.Fatalf("HEAD %s %s = %q, want GET header %q", target, name, got, want)
		}
	}
}

func requireCSVResponseRecord(t *testing.T, records [][]string, column, value string) []string {
	t.Helper()

	index := csvResponseColumnIndex(t, records[0], column)
	for _, record := range records[1:] {
		if record[index] == value {
			return record
		}
	}
	t.Fatalf("CSV response records = %+v, want %s=%q", records, column, value)
	return nil
}

func csvResponseColumnIndex(t *testing.T, header []string, column string) int {
	t.Helper()

	for idx, name := range header {
		if name == column {
			return idx
		}
	}
	t.Fatalf("CSV response header = %+v, missing %q", header, column)
	return -1
}

func readOnlyResourceID(t *testing.T, db *sql.DB) string {
	t.Helper()

	var resourceID string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM resources LIMIT 1`).Scan(&resourceID); err != nil {
		t.Fatalf("read resource ID: %v", err)
	}
	return resourceID
}

// readOnlyResourceIDByName finds one test-created resource without mutating workspace state.
func readOnlyResourceIDByName(t *testing.T, db *sql.DB, resourceName string) string {
	t.Helper()

	var resourceID string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM resources WHERE resource_name = ?`, resourceName).Scan(&resourceID); err != nil {
		t.Fatalf("read resource ID for %q: %v", resourceName, err)
	}
	return resourceID
}

func organizationLifecycleEventCountForAccount(events []persistence.AccountLifecycleEvent, accountID string) int {
	count := 0
	for _, event := range events {
		if event.AccountID == accountID {
			count++
		}
	}
	return count
}

func seedFilterableUsage(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-filter-ec2",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Filter web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(EC2) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-filter-ec2",
		ResourceID:          "resource-filter-ec2",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T01:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(EC2) error = %v", err)
	}
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-filter-s3",
		AccountID:    "222233334444",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonS3",
		ResourceType: "s3_bucket",
		ResourceName: "Filter bucket",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(S3) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-filter-s3",
		ResourceID:          "resource-filter-s3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		UsageStartTime:      "2026-02-02T00:00:00Z",
		UsageEndTime:        "2026-02-03T00:00:00Z",
		UsageQuantityMicros: 1_500_000_000,
		UsageUnit:           "Request",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(S3) error = %v", err)
	}
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
}

func insertBillsUIStoredBillState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	periodStart string,
	periodEnd string,
	payerAccountID string,
	billState string,
	invoiceStatus string,
	usageChargeMicros int64,
	creditMicros int64,
	refundMicros int64,
	taxMicros int64,
) {
	t.Helper()

	totalMicros := usageChargeMicros + taxMicros - creditMicros - refundMicros
	if totalMicros < 0 {
		totalMicros = 0
	}
	periodKey := strings.ReplaceAll(periodStart, "-", "")
	stateKey := strings.ReplaceAll(billState, "_", "-")
	amountDueMicros := totalMicros
	amountPaidMicros := int64(0)
	if invoiceStatus == "paid" {
		amountDueMicros = 0
		amountPaidMicros = totalMicros
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO billing_period_closes (
			id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			status,
			metering_records_created,
			bill_line_items_created,
			finalized_line_item_count,
			finalized_cost_micros,
			currency_code,
			summaries_refreshed
		) VALUES (?, ?, ?, ?, 'closed', 0, 0, 1, ?, 'USD', 0)`,
		"close-ui-"+periodKey+"-"+stateKey,
		periodStart,
		periodEnd,
		payerAccountID,
		totalMicros,
	); err != nil {
		t.Fatalf("insert billing_period_closes: %v", err)
	}
	billID := "bill-ui-" + periodKey + "-" + stateKey
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO bills (
			id,
			close_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			bill_state,
			currency_code,
			line_item_count,
			usage_charge_micros,
			credit_micros,
			refund_micros,
			tax_micros,
			total_micros
		) VALUES (?, ?, ?, ?, ?, ?, 'USD', 1, ?, ?, ?, ?, ?)`,
		billID,
		"close-ui-"+periodKey+"-"+stateKey,
		periodStart,
		periodEnd,
		payerAccountID,
		billState,
		usageChargeMicros,
		creditMicros,
		refundMicros,
		taxMicros,
		totalMicros,
	); err != nil {
		t.Fatalf("insert bills: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO invoice_obligations (
			id,
			bill_id,
			invoice_id,
			status,
			amount_due_micros,
			amount_paid_micros,
			currency_code,
			invoice_date,
			due_date
		) VALUES (?, ?, ?, ?, ?, ?, 'USD', ?, ?)`,
		"iob-ui-"+periodKey+"-"+stateKey,
		billID,
		"SIM-INV-"+strings.ReplaceAll(periodStart[:7], "-", "")+"-"+strings.ToUpper(stateKey),
		invoiceStatus,
		amountDueMicros,
		amountPaidMicros,
		periodEnd,
		periodEnd,
	); err != nil {
		t.Fatalf("insert invoice_obligations: %v", err)
	}
}
