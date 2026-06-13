package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestCostExplorerUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/cost-explorer")
	if err != nil {
		t.Fatalf("GET /cost-explorer without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Cost Explorer - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/save", url.Values{"report_name": {"Spend"}})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /cost-explorer/reports/save without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before saving Cost Explorer reports.") {
		t.Fatalf("POST /cost-explorer/reports/save without workspace missing workspace message: %s", body)
	}
}

func TestCostExplorerReportBuilderWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	seedCostCategoryPreviewSpend(t, ctx, db)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/cost-explorer")
	if err != nil {
		t.Fatalf("GET /cost-explorer error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Report Definition",
		"Time and Metric",
		"Filters",
		"Group By",
		"Run Report",
		"Save Report",
		"No saved reports",
		`<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer body missing %q: %s", want, body)
		}
	}

	query := url.Values{
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"daily"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"line"},
		"service_values":   {"Amazon EC2"},
		"tag_key":          {"app"},
		"tag_values":       {"storefront"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"service"},
		"group_2_type":     {"tag"},
		"group_2_key":      {"app"},
		"run":              {"1"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer filtered report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer filtered report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Report Results",
		`class="report-chart report-chart-line"`,
		`<polyline class="chart-line"`,
		`<circle class="chart-point"`,
		"Period Start",
		"Group 1",
		"Group 2",
		"Service=AmazonEC2",
		"tag:app=storefront",
		"$0.0832",
		"Unblended Cost",
		"/cost-explorer/results.csv?",
		"/cost-explorer/line-items?",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer filtered report missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL + "/cost-explorer/results.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /cost-explorer/results.csv content type = %q, want text/csv", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, "cost-explorer-report.csv") {
		t.Fatalf("GET /cost-explorer/results.csv content disposition = %q, want report filename", disposition)
	}
	assertHEADDownloadMatchesGET(t, newMux(db), "/cost-explorer/results.csv?"+query.Encode(), resp.Header, "Content-Type", "Content-Disposition")
	for _, want := range []string{
		"date_range_start,date_range_end,granularity,metric,period_start",
		"2026-02-01,2026-03-01,daily,unblended_cost,2026-02-01,2026-02-02,dimension,service,AmazonEC2,tag,app,storefront,0.083200,2.000000,0.083200,0.083200,0.083200,0.083200,1,USD",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv body missing %q: %s", want, body)
		}
	}

	drilldownQuery := url.Values{}
	for key, values := range query {
		drilldownQuery[key] = values
	}
	drilldownQuery.Set("period_start", "2026-02-01")
	drilldownQuery.Set("period_end", "2026-02-02")
	drilldownQuery.Set("group_1_value", "AmazonEC2")
	drilldownQuery.Set("group_2_value", "storefront")
	resp, err = client.Get(server.URL + "/cost-explorer/line-items?" + drilldownQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/line-items error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/line-items status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Cost Explorer Bill Line Items",
		"Source Line Items",
		"resource-cost-category-web",
		"Amazon EC2",
		"instance-hours:t3.medium",
		"$0.0832",
		"tag:app=storefront",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/line-items body missing %q: %s", want, body)
		}
	}

	stackedQuery := url.Values{
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"monthly"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"stacked_bar"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"service"},
		"run":              {"1"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + stackedQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer stacked report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer stacked report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`class="report-chart report-chart-stacked_bar"`,
		`<rect class="chart-bar"`,
		"Service=AmazonEC2",
		"Service=AmazonS3",
		"Max $0.0907",
		"2026-02-01",
		"2026-03-01",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer stacked report missing %q: %s", want, body)
		}
	}

	saveForm := url.Values{}
	for key, values := range query {
		saveForm[key] = values
	}
	saveForm.Set("report_name", "Storefront EC2 daily")
	saveForm.Set("description", "Browser-created report definition")
	saveForm.Set("owner_account_id", persistence.AnyCompanyRetailManagementAccountID)
	saveForm.Set("owner_role", "management-account")

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/save", saveForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Saved report Storefront EC2 daily",
		"Storefront EC2 daily",
		"Browser-created report definition",
		"Loaded",
		"line",
		"Saved Reports",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/save body missing %q: %s", want, body)
		}
	}

	report, err := persistence.NewSavedReportRepository(db).GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "management-account", "Storefront EC2 daily")
	if err != nil {
		t.Fatalf("GetByName(saved report) error = %v", err)
	}
	if report.DateRangeStart != "2026-02-01" ||
		report.DateRangeEnd != "2026-03-01" ||
		report.Granularity != "daily" ||
		report.ChartType != "line" ||
		len(report.Groupings) != 2 ||
		report.Groupings[0] != (persistence.SavedReportGrouping{Type: "dimension", Key: "service"}) ||
		report.Groupings[1] != (persistence.SavedReportGrouping{Type: "tag", Key: "app"}) ||
		report.Filters["service"][0] != "Amazon EC2" ||
		report.Filters["tag:app"][0] != "storefront" {
		t.Fatalf("saved report definition = %+v, want browser report filters and groupings", report)
	}
}

func TestCostExplorerAdvancedCostMetricsWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	seedSavingsPlanWorkflowUsage(t, ctx, db)
	if _, err := persistence.NewSavingsPlanRepository(db).CreatePurchase(ctx, persistence.SavingsPlanPurchaseCreateRequest{
		ID:                     "sp-cost-explorer-ui",
		PayerAccountID:         persistence.AnyCompanyRetailManagementAccountID,
		OwnerAccountID:         "111122223333",
		ReferenceUsageType:     "instance-hours:t3.medium",
		RegionCode:             "us-east-1",
		SharingScope:           persistence.SavingsPlanSharingScopeOrganization,
		TermStartTime:          "2026-02-01T00:00:00Z",
		TermEndTime:            "2026-02-01T03:00:00Z",
		HourlyCommitmentMicros: 100_000,
		UpfrontFeeMicros:       90_000,
		Description:            "Cost Explorer UI Savings Plan",
	}); err != nil {
		t.Fatalf("CreatePurchase(Savings Plan) error = %v", err)
	}
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	query := url.Values{
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"monthly"},
		"metric":           {"amortized_cost"},
		"chart_type":       {"table"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"linked_account"},
		"run":              {"1"},
	}
	resp, err := client.Get(server.URL + "/cost-explorer?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer amortized report error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer amortized report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="blended_cost">Blended Cost</option>`,
		`<option value="net_cost">Net Cost</option>`,
		`<option value="amortized_cost" selected>Amortized Cost</option>`,
		"Report Results",
		"Amortized Cost",
		"$0.39",
		"Linked Account=111122223333",
		"Linked Account=555566667777",
		"$0.28184",
		"$0.10816",
		"$0.5564",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer amortized report missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL + "/cost-explorer/results.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv amortized report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv amortized report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"usage_quantity,unblended_cost,blended_cost,net_cost,amortized_cost,line_item_count",
		"monthly,amortized_cost,2026-02-01,2026-03-01,dimension,linked_account,111122223333",
		"0.556400,0.390000,0.390000,0.281840,4,USD",
		"0.166400,0.000000,0.000000,0.108160,2,USD",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv amortized report missing %q: %s", want, body)
		}
	}
}

func TestCostExplorerSavedReportsAreScopedByOwnerContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	savedReportRepo := persistence.NewSavedReportRepository(db)
	managementReport, err := savedReportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-ui-management-scope",
		Name:           "Management saved report",
		Description:    "Management owner only",
		OwnerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings:      []persistence.SavedReportGrouping{{Type: "dimension", Key: "service"}},
	})
	if err != nil {
		t.Fatalf("Create(management saved report) error = %v", err)
	}
	memberReport, err := savedReportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-ui-member-scope",
		Name:           "Member saved report",
		Description:    "Member owner only",
		OwnerAccountID: "111122223333",
		OwnerRole:      "member-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings:      []persistence.SavedReportGrouping{{Type: "dimension", Key: "service"}},
	})
	if err != nil {
		t.Fatalf("Create(member saved report) error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	managementQuery := url.Values{
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
	}
	resp, err := client.Get(server.URL + "/cost-explorer?" + managementQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer management owner error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer management owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Management saved report") || strings.Contains(body, "Member saved report") {
		t.Fatalf("management saved-report shelf body = %s, want only management report", body)
	}

	memberQuery := url.Values{
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer member owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Member saved report") ||
		strings.Contains(body, "Management saved report") ||
		!strings.Contains(body, `name="owner_account_id" value="111122223333"`) ||
		!strings.Contains(body, `name="owner_role" value="member-account"`) {
		t.Fatalf("member saved-report shelf body = %s, want only member report and owner context fields", body)
	}

	memberLoadQuery := url.Values{
		"saved_report_id":  {memberReport.ID},
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + memberLoadQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer member saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer member saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Member saved report") ||
		!strings.Contains(body, "Loaded") ||
		strings.Contains(body, "Management saved report") {
		t.Fatalf("member saved-report load body = %s, want only loaded member report", body)
	}
	memberNewReportHref := `/cost-explorer?owner_account_id=111122223333&amp;owner_role=member-account`
	if !strings.Contains(body, `<a class="button-link" href="`+memberNewReportHref+`">New Report</a>`) {
		t.Fatalf("member saved-report load missing scoped New Report link %q: %s", memberNewReportHref, body)
	}

	resp, err = client.Get(server.URL + "/cost-explorer?owner_account_id=111122223333&owner_role=member-account")
	if err != nil {
		t.Fatalf("GET /cost-explorer member new-report path error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer member new-report path status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`name="owner_account_id" value="111122223333"`,
		`<option value="member-account" selected>Member</option>`,
		`<input type="hidden" name="saved_report_id" value="">`,
		`<input name="report_name" value="">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer member new-report path body missing %q: %s", want, body)
		}
	}

	crossOwnerLoadQuery := url.Values{
		"saved_report_id":  {memberReport.ID},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + crossOwnerLoadQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer cross-owner saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /cost-explorer cross-owner saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	if strings.Contains(body, "Member saved report") {
		t.Fatalf("cross-owner saved-report load leaked member report: %s", body)
	}

	crossOwnerUpdate := url.Values{
		"saved_report_id":  {memberReport.ID},
		"report_name":      {"Member saved report takeover"},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"monthly"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"table"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"service"},
	}
	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/save", crossOwnerUpdate)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save cross-owner update error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /cost-explorer/reports/save cross-owner update status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	reloadedMemberReport, err := savedReportRepo.Get(ctx, memberReport.ID)
	if err != nil {
		t.Fatalf("Get(member report after cross-owner update) error = %v", err)
	}
	if reloadedMemberReport.Name != memberReport.Name || reloadedMemberReport.OwnerAccountID != memberReport.OwnerAccountID || reloadedMemberReport.OwnerRole != memberReport.OwnerRole {
		t.Fatalf("member report after cross-owner update = %+v, want unchanged %+v", reloadedMemberReport, memberReport)
	}

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/run", url.Values{
		"saved_report_id":  {memberReport.ID},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
	})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/run cross-owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /cost-explorer/reports/run cross-owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	reloadedMemberReport, err = savedReportRepo.Get(ctx, memberReport.ID)
	if err != nil {
		t.Fatalf("Get(member report after cross-owner run) error = %v", err)
	}
	if reloadedMemberReport.LastRunStatus != "never_run" || reloadedMemberReport.LastRunAt != "" {
		t.Fatalf("member report after cross-owner run = %+v, want no run metadata", reloadedMemberReport)
	}
	if managementReport.ID == memberReport.ID {
		t.Fatalf("test reports share ID: management=%q member=%q", managementReport.ID, memberReport.ID)
	}
}

func TestCostExplorerQueriesAreScopedByOwnerPolicy(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	seedFilterableUsage(t, ctx, db)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	memberQuery := url.Values{
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"monthly"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"table"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"linked_account"},
		"run":              {"1"},
	}
	resp, err := client.Get(server.URL + "/cost-explorer?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer member owner error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`name="owner_account_id" value="111122223333"`,
		`<option value="member-account" selected>Member</option>`,
		"Linked Account=111122223333",
		"$0.0416",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer member owner body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{
		"Linked Account=222233334444",
		"$0.0491",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /cost-explorer member owner leaked %q: %s", leaked, body)
		}
	}

	resp, err = client.Get(server.URL + "/cost-explorer/results.csv?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv member owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"date_range_start,date_range_end,granularity,metric,period_start",
		"2026-02-01,2026-03-01,monthly,unblended_cost,2026-02-01,2026-03-01,dimension,linked_account,111122223333",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv member owner body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "222233334444") {
		t.Fatalf("GET /cost-explorer/results.csv member owner leaked other account: %s", body)
	}

	drilldownQuery := url.Values{}
	for key, values := range memberQuery {
		drilldownQuery[key] = values
	}
	drilldownQuery.Set("period_start", "2026-02-01")
	drilldownQuery.Set("period_end", "2026-03-01")
	drilldownQuery.Set("group_1_value", "222233334444")
	resp, err = client.Get(server.URL + "/cost-explorer/line-items?" + drilldownQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/line-items member owner cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/line-items member owner cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "Filter bucket") || strings.Contains(body, "resource-filter-s3") || strings.Contains(body, "Amazon S3") {
		t.Fatalf("GET /cost-explorer/line-items member owner leaked cross-account line item: %s", body)
	}

	reportRepo := persistence.NewSavedReportRepository(db)
	report, err := reportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-member-policy-scope",
		Name:           "Member policy scope",
		OwnerAccountID: "111122223333",
		OwnerRole:      "member-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings:      []persistence.SavedReportGrouping{{Type: "dimension", Key: "linked_account"}},
	})
	if err != nil {
		t.Fatalf("Create(member policy scoped report) error = %v", err)
	}
	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/run", url.Values{
		"saved_report_id":  {report.ID},
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
	})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/run member owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/run member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	reloadedReport, err := reportRepo.Get(ctx, report.ID)
	if err != nil {
		t.Fatalf("Get(member policy scoped report after run) error = %v", err)
	}
	if reloadedReport.LastRunStatus != "succeeded" ||
		reloadedReport.LastRunRowCount != 1 ||
		reloadedReport.LastRunTotalUnblendedCostMicros != 41_600 {
		t.Fatalf("member report run metadata = %+v, want one scoped row totaling EC2 spend", reloadedReport)
	}
}

// TestCostExplorerReportUIFeatureWorksInFreshWorkspace keeps bd-1of.2 guarded through the browser-facing report builder, charts, saved reports, CSV, and drilldowns.
func TestCostExplorerReportUIFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "cost-explorer-report-feature-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClient()

	resp, err := client.Get(server.URL() + "/cost-explorer")
	if err != nil {
		t.Fatalf("GET /cost-explorer error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Cost Explorer - AWS Billing Simulator</title>`,
		"Report Definition",
		"Time and Metric",
		"Filters",
		"Group By",
		"Run Report",
		"Save Report",
		"No saved reports",
		`<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer body missing %q: %s", want, body)
		}
	}

	createResource := func(name, accountID, appValue string) string {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/create", url.Values{
			"account_id":     {accountID},
			"region_code":    {"us-east-1"},
			"service_preset": {"ec2_t3_medium"},
			"size":           {"t3.medium"},
			"resource_name":  {name},
			"status":         {"active"},
			"started_at":     {"2026-02-01T00:00"},
			"tags":           {"app=" + appValue + "\nowner=retail-finops"},
		})
		if err != nil {
			t.Fatalf("POST /resources/create %s error = %v", name, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/create %s final status = %d, want %d; body=%s", name, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, name) || !strings.Contains(body, "app="+appValue) {
			t.Fatalf("resource create response for %s missing resource/tag: %s", name, body)
		}
		return readOnlyResourceIDByName(t, db, name)
	}

	generateUsage := func(resourceID, days string) {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/generate", url.Values{
			"resource_id":           {resourceID},
			"generation_pattern":    {string(persistence.UsageGenerationDailyInstanceHours)},
			"generation_start_date": {"2026-02-01"},
			"generation_days":       {days},
		})
		if err != nil {
			t.Fatalf("POST /resources/generate %s error = %v", resourceID, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/generate %s final status = %d, want %d; body=%s", resourceID, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "Generated "+days+" usage events") ||
			!strings.Contains(body, "instance-hours:t3.medium") ||
			!strings.Contains(body, "app=") {
			t.Fatalf("generator response for %s missing usage/tag snapshot: %s", resourceID, body)
		}
	}

	storefrontResourceID := createResource("Feature report storefront web", "111122223333", "storefront")
	paymentsResourceID := createResource("Feature report payments web", "444455556666", "payments")
	generateUsage(storefrontResourceID, "2")
	generateUsage(paymentsResourceID, "1")

	body = postClockAdvance(t, &client, server.URL(), "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 3 metering records and 4 bill line items") ||
		!strings.Contains(body, "AWSSupport") {
		t.Fatalf("clock advance response missing Cost Explorer report line items: %s", body)
	}

	query := url.Values{
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"daily"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"line"},
		"service_values":   {"Amazon EC2"},
		"tag_key":          {"app"},
		"tag_values":       {"storefront, payments"},
		"group_1_type":     {"tag"},
		"group_1_key":      {"app"},
		"run":              {"1"},
	}
	resp, err = client.Get(server.URL() + "/cost-explorer?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Report Results",
		`class="report-chart report-chart-line"`,
		`<polyline class="chart-line"`,
		`<circle class="chart-point"`,
		"tag:app=payments",
		"tag:app=storefront",
		"$0.9984",
		"Period Start",
		"Line Items",
		"/cost-explorer/results.csv?",
		"/cost-explorer/line-items?",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer report body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL() + "/cost-explorer/results.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /cost-explorer/results.csv content type = %q, want text/csv", contentType)
	}
	for _, want := range []string{
		"date_range_start,date_range_end,granularity,metric,period_start",
		"tag,app,payments,,,,0.998400,24.000000,0.998400,0.998400,0.998400,0.998400,1,USD",
		"tag,app,storefront,,,,0.998400,24.000000,0.998400,0.998400,0.998400,0.998400,1,USD",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv body missing %q: %s", want, body)
		}
	}

	drilldownQuery := url.Values{}
	for key, values := range query {
		drilldownQuery[key] = values
	}
	drilldownQuery.Set("period_start", "2026-02-01")
	drilldownQuery.Set("period_end", "2026-02-02")
	drilldownQuery.Set("group_1_value", "storefront")
	resp, err = client.Get(server.URL() + "/cost-explorer/line-items?" + drilldownQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/line-items error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/line-items status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Cost Explorer Bill Line Items",
		"Source Line Items",
		storefrontResourceID,
		"Amazon EC2",
		"instance-hours:t3.medium",
		"$0.9984",
		"tag:app=storefront",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/line-items body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, paymentsResourceID) {
		t.Fatalf("GET /cost-explorer/line-items leaked payments resource into storefront drilldown: %s", body)
	}

	saveForm := url.Values{}
	for key, values := range query {
		saveForm[key] = values
	}
	saveForm.Set("report_name", "Daily App EC2 Spend")
	saveForm.Set("description", "Fresh-workspace report UI close-out")
	saveForm.Set("owner_account_id", persistence.AnyCompanyRetailManagementAccountID)
	saveForm.Set("owner_role", "management-account")

	resp, err = client.PostForm(server.URL()+"/cost-explorer/reports/save", saveForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Saved report Daily App EC2 Spend",
		"Daily App EC2 Spend",
		"Fresh-workspace report UI close-out",
		"Loaded",
		"line",
		"Saved Reports",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/save body missing %q: %s", want, body)
		}
	}

	savedReportRepo := persistence.NewSavedReportRepository(db)
	report, err := savedReportRepo.GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "management-account", "Daily App EC2 Spend")
	if err != nil {
		t.Fatalf("GetByName(saved report) error = %v", err)
	}
	if report.DateRangeStart != "2026-02-01" ||
		report.DateRangeEnd != "2026-03-01" ||
		report.Granularity != "daily" ||
		report.ChartType != "line" ||
		len(report.Metrics) != 1 ||
		report.Metrics[0] != "unblended_cost" ||
		len(report.Groupings) != 1 ||
		report.Groupings[0] != (persistence.SavedReportGrouping{Type: "tag", Key: "app"}) ||
		report.Filters["service"][0] != "Amazon EC2" ||
		len(report.Filters["tag:app"]) != 2 {
		t.Fatalf("saved report definition = %+v, want daily app EC2 report definition", report)
	}

	resp, err = client.Get(server.URL() + "/cost-explorer?saved_report_id=" + url.QueryEscape(report.ID))
	if err != nil {
		t.Fatalf("GET /cost-explorer saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Daily App EC2 Spend",
		"Loaded",
		`class="report-chart report-chart-line"`,
		"tag:app=payments",
		"tag:app=storefront",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer saved report body missing %q: %s", want, body)
		}
	}

	updateForm := url.Values{}
	for key, values := range saveForm {
		updateForm[key] = values
	}
	updateForm.Set("saved_report_id", report.ID)
	updateForm.Set("description", "Updated fresh-workspace report definition")
	updateForm.Set("metric", "usage_quantity")
	updateForm.Set("chart_type", "bar")

	resp, err = client.PostForm(server.URL()+"/cost-explorer/reports/save", updateForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save update error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save update final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Updated saved report Daily App EC2 Spend",
		"Updated fresh-workspace report definition",
		`class="report-chart report-chart-bar"`,
		`<rect class="chart-bar"`,
		"<strong>24</strong>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/save update body missing %q: %s", want, body)
		}
	}

	updatedReport, err := savedReportRepo.Get(ctx, report.ID)
	if err != nil {
		t.Fatalf("Get(updated saved report) error = %v", err)
	}
	if updatedReport.Description != "Updated fresh-workspace report definition" ||
		updatedReport.ChartType != "bar" ||
		len(updatedReport.Metrics) != 1 ||
		updatedReport.Metrics[0] != "usage_quantity" {
		t.Fatalf("updated saved report = %+v, want edited usage bar definition", updatedReport)
	}

	resp, err = client.Get(server.URL() + "/cost-explorer/results.csv?saved_report_id=" + url.QueryEscape(report.ID))
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, "Daily-App-EC2-Spend.csv") {
		t.Fatalf("GET /cost-explorer/results.csv saved report content disposition = %q, want saved report filename", disposition)
	}
	for _, want := range []string{
		"daily,usage_quantity",
		"tag,app,payments,,,,24.000000,24.000000,0.998400,0.998400,0.998400,0.998400,1,USD",
		"tag,app,storefront,,,,24.000000,24.000000,0.998400,0.998400,0.998400,0.998400,1,USD",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv saved report body missing %q: %s", want, body)
		}
	}
}

func TestCostExplorerSavedReportRunRecordsLastRunMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close(workspace) error = %v", err)
		}
	})
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-02-03T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	usageRepo := persistence.NewResourceUsageRepository(db)
	resource, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-saved-report-run",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Saved report run web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags:         map[string]string{"app": "storefront"},
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-saved-report-run",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		RegionCode:          "us-east-1",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	saveForm := url.Values{
		"report_name":      {"Saved report run metadata"},
		"description":      {"Focused saved-report run test"},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"daily"},
		"metric":           {"usage_quantity"},
		"chart_type":       {"table"},
		"service_values":   {"Amazon EC2"},
		"tag_key":          {"app"},
		"tag_values":       {"storefront"},
		"group_1_type":     {"tag"},
		"group_1_key":      {"app"},
	}
	resp, err := client.PostForm(server.URL+"/cost-explorer/reports/save", saveForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Saved report run metadata") || !strings.Contains(body, "never_run") {
		t.Fatalf("saved report create response missing loaded never-run report: %s", body)
	}

	savedReportRepo := persistence.NewSavedReportRepository(db)
	report, err := savedReportRepo.GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "management-account", "Saved report run metadata")
	if err != nil {
		t.Fatalf("GetByName(saved report) error = %v", err)
	}
	if report.LastRunStatus != "never_run" || report.LastRunAt != "" || report.LastRunRowCount != 0 || report.LastRunTotalUnblendedCostMicros != 0 {
		t.Fatalf("saved report after create = %+v, want no run metadata before explicit POST run", report)
	}

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/run", url.Values{
		"saved_report_id": {report.ID},
	})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/run error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/run status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Ran saved report Saved report run metadata",
		"Saved report run metadata",
		"succeeded 2026-02-03T00:00:00Z",
		"Usage Quantity 2",
		"tag:app=storefront",
		"$0.0832",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/run body missing %q: %s", want, body)
		}
	}

	ranReport, err := savedReportRepo.Get(ctx, report.ID)
	if err != nil {
		t.Fatalf("Get(ran saved report) error = %v", err)
	}
	if ranReport.LastRunStatus != "succeeded" ||
		ranReport.LastRunAt != "2026-02-03T00:00:00Z" ||
		ranReport.LastRunRowCount != 1 ||
		ranReport.LastRunTotalUnblendedCostMicros != 83_200 ||
		ranReport.LastRunMetric != "usage_quantity" ||
		ranReport.LastRunMetricTotalMicros != 2_000_000 ||
		ranReport.LastRunError != "" {
		t.Fatalf("saved report last-run metadata = %+v, want successful UI run summary", ranReport)
	}
}

// TestCostExplorerQueryEngineFeatureWorksInFreshWorkspace keeps bd-1of.1 guarded through the browser-facing billing setup and repository query surfaces.
func TestCostExplorerQueryEngineFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "cost-explorer-query-feature-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClient()

	createResource := func(name, accountID, appValue string) string {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/create", url.Values{
			"account_id":     {accountID},
			"region_code":    {"us-east-1"},
			"service_preset": {"ec2_t3_medium"},
			"size":           {"t3.medium"},
			"resource_name":  {name},
			"status":         {"active"},
			"started_at":     {"2026-02-01T00:00"},
			"tags":           {"app=" + appValue + "\nowner=retail-finops"},
		})
		if err != nil {
			t.Fatalf("POST /resources/create %s error = %v", name, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/create %s final status = %d, want %d; body=%s", name, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, name) || !strings.Contains(body, "app="+appValue) {
			t.Fatalf("resource create response for %s missing resource/tag: %s", name, body)
		}
		return readOnlyResourceIDByName(t, db, name)
	}

	generateUsage := func(resourceID, days string) {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/generate", url.Values{
			"resource_id":           {resourceID},
			"generation_pattern":    {string(persistence.UsageGenerationDailyInstanceHours)},
			"generation_start_date": {"2026-02-01"},
			"generation_days":       {days},
		})
		if err != nil {
			t.Fatalf("POST /resources/generate %s error = %v", resourceID, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/generate %s final status = %d, want %d; body=%s", resourceID, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "Generated "+days+" usage events") ||
			!strings.Contains(body, "instance-hours:t3.medium") ||
			!strings.Contains(body, "app=") {
			t.Fatalf("generator response for %s missing usage/tag snapshot: %s", resourceID, body)
		}
	}

	storefrontResourceID := createResource("Feature explorer storefront web", "111122223333", "storefront")
	paymentsResourceID := createResource("Feature explorer payments web", "444455556666", "payments")
	generateUsage(storefrontResourceID, "2")
	generateUsage(paymentsResourceID, "1")

	body := postClockAdvance(t, &client, server.URL(), "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 3 metering records and 4 bill line items") ||
		!strings.Contains(body, "AWSSupport") {
		t.Fatalf("clock advance response missing Cost Explorer feature line items: %s", body)
	}

	resp, err := client.PostForm(server.URL()+"/cost-categories/categories/create", url.Values{
		"name":          {"Product"},
		"default_value": {"Unmapped"},
		"description":   {"Cost Explorer query feature product grouping"},
	})
	if err != nil {
		t.Fatalf("POST create Product category error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product category final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	productID := readCostCategoryID(t, db, "Product")

	for _, form := range []url.Values{
		{
			"category_id": {productID},
			"rule_order":  {"10"},
			"value":       {"Storefront"},
			"dimension":   {persistence.CostCategoryRuleMatchTag},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"storefront"},
			"tag_key":     {"app"},
			"description": {"Storefront application tag"},
		},
		{
			"category_id": {productID},
			"rule_order":  {"20"},
			"value":       {"Payments"},
			"dimension":   {persistence.CostCategoryRuleMatchTag},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"payments"},
			"tag_key":     {"app"},
			"description": {"Payments application tag"},
		},
		{
			"category_id": {productID},
			"rule_order":  {"30"},
			"value":       {"Shared Platform"},
			"dimension":   {persistence.CostCategoryRuleMatchService},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"AWSSupport"},
			"description": {"Support is a shared platform category"},
		},
	} {
		resp, err = client.PostForm(server.URL()+"/cost-categories/rules/create", form)
		if err != nil {
			t.Fatalf("POST create Product rule %s error = %v", form.Get("value"), err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST create Product rule %s final status = %d, want %d; body=%s", form.Get("value"), resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "Created rule") || !strings.Contains(body, form.Get("value")) {
			t.Fatalf("POST create Product rule %s body missing confirmation: %s", form.Get("value"), body)
		}
	}

	costExplorerRepo := persistence.NewCostExplorerRepository(db)
	reportRequest := persistence.CostExplorerQueryRequest{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "daily",
		Filters: map[string][]string{
			"service": {"Amazon EC2"},
			"tag:app": {"storefront"},
		},
		Groupings: []persistence.CostExplorerGrouping{
			{Type: "dimension", Key: "service"},
			{Type: "tag", Key: "app"},
		},
	}
	result, err := costExplorerRepo.Query(ctx, reportRequest)
	if err != nil {
		t.Fatalf("Query(storefront saved report request) error = %v", err)
	}
	if result.TotalLineItemCount != 2 ||
		result.TotalUsageQuantityMicros != 48_000_000 ||
		result.TotalUnblendedCostMicros != 1_996_800 ||
		len(result.Rows) != 2 {
		t.Fatalf("storefront query result = %+v, want two daily EC2 storefront rows totaling 1996800 micros", result)
	}
	for i, row := range result.Rows {
		wantDate := "2026-02-01"
		if i == 1 {
			wantDate = "2026-02-02"
		}
		if row.TimePeriodStart != wantDate ||
			row.UsageQuantityMicros != 24_000_000 ||
			row.UnblendedCostMicros != 998_400 ||
			row.LineItemCount != 1 ||
			len(row.GroupValues) != 2 ||
			row.GroupValues[0] != (persistence.CostExplorerGroupValue{Type: "dimension", Key: "service", Value: "AmazonEC2"}) ||
			row.GroupValues[1] != (persistence.CostExplorerGroupValue{Type: "tag", Key: "app", Value: "storefront"}) {
			t.Fatalf("storefront query row %d = %+v, want one daily EC2 storefront row", i, row)
		}
	}

	categoryResult, err := costExplorerRepo.Query(ctx, persistence.CostExplorerQueryRequest{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "monthly",
		Filters: map[string][]string{
			"cost_category:Product": {"Storefront"},
		},
		Groupings: []persistence.CostExplorerGrouping{
			{Type: "cost_category", Key: "Product"},
			{Type: "dimension", Key: "linked_account"},
		},
	})
	if err != nil {
		t.Fatalf("Query(Product cost category) error = %v", err)
	}
	if categoryResult.TotalLineItemCount != 2 ||
		categoryResult.TotalUnblendedCostMicros != 1_996_800 ||
		len(categoryResult.Rows) != 1 {
		t.Fatalf("Product category query result = %+v, want Storefront EC2 rollup", categoryResult)
	}
	categoryRow := categoryResult.Rows[0]
	if categoryRow.TimePeriodStart != "2026-02-01" ||
		len(categoryRow.GroupValues) != 2 ||
		categoryRow.GroupValues[0] != (persistence.CostExplorerGroupValue{Type: "cost_category", Key: "Product", Value: "Storefront"}) ||
		categoryRow.GroupValues[1] != (persistence.CostExplorerGroupValue{Type: "dimension", Key: "linked_account", Value: "111122223333"}) {
		t.Fatalf("Product category query row = %+v, want Storefront linked-account grouping", categoryRow)
	}

	var monthlyLineItems int
	var monthlyUsageMicros, monthlyCostMicros int64
	if err := db.QueryRowContext(ctx, `
		SELECT line_item_count, usage_quantity_micros, unblended_cost_micros
		FROM monthly_account_service_summary
		WHERE billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
		  AND usage_account_id = '111122223333'
		  AND service_code = 'AmazonEC2'
		  AND line_item_status = 'estimated'
	`).Scan(&monthlyLineItems, &monthlyUsageMicros, &monthlyCostMicros); err != nil {
		t.Fatalf("read Cost Explorer monthly account service summary: %v", err)
	}
	if monthlyLineItems != 2 || monthlyUsageMicros != 48_000_000 || monthlyCostMicros != 1_996_800 {
		t.Fatalf("monthly summary = lines %d usage %d cost %d, want storefront EC2 totals", monthlyLineItems, monthlyUsageMicros, monthlyCostMicros)
	}

	var categoryLineItems int
	var categoryCostMicros int64
	if err := db.QueryRowContext(ctx, `
		SELECT line_item_count, unblended_cost_micros
		FROM cost_category_summary
		WHERE billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
		  AND cost_category_id = ?
		  AND assigned_value = 'Storefront'
	`, productID).Scan(&categoryLineItems, &categoryCostMicros); err != nil {
		t.Fatalf("read Cost Explorer cost category summary: %v", err)
	}
	if categoryLineItems != 2 || categoryCostMicros != 1_996_800 {
		t.Fatalf("cost category summary = lines %d cost %d, want Storefront summary totals", categoryLineItems, categoryCostMicros)
	}

	savedReportRepo := persistence.NewSavedReportRepository(db)
	savedReport, err := savedReportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-cost-explorer-feature",
		Name:           "Daily storefront EC2 cost",
		Description:    "bd-1of.1 feature smoke report",
		OwnerAccountID: "999988887777",
		OwnerRole:      "management-account",
		DateRangeStart: reportRequest.DateRangeStart,
		DateRangeEnd:   reportRequest.DateRangeEnd,
		Granularity:    reportRequest.Granularity,
		Filters:        reportRequest.Filters,
		Groupings:      reportRequest.Groupings,
		Metrics:        []string{"unblended_cost", "usage_quantity"},
		ChartType:      "line",
	})
	if err != nil {
		t.Fatalf("Create(saved report) error = %v", err)
	}
	savedResult, err := costExplorerRepo.Query(ctx, persistence.CostExplorerQueryRequest{
		DateRangeStart: savedReport.DateRangeStart,
		DateRangeEnd:   savedReport.DateRangeEnd,
		Granularity:    savedReport.Granularity,
		Filters:        savedReport.Filters,
		Groupings:      savedReport.Groupings,
	})
	if err != nil {
		t.Fatalf("Query(saved report definition) error = %v", err)
	}
	if savedResult.TotalUnblendedCostMicros != result.TotalUnblendedCostMicros ||
		savedResult.TotalLineItemCount != result.TotalLineItemCount {
		t.Fatalf("saved report query = %+v, want same totals as direct query %+v", savedResult, result)
	}

	ranReport, err := savedReportRepo.RecordLastRun(ctx, persistence.SavedReportRunUpdate{
		ID:                       savedReport.ID,
		RunAt:                    "2026-02-03T00:00:00Z",
		Status:                   "succeeded",
		RowCount:                 len(savedResult.Rows),
		TotalUnblendedCostMicros: savedResult.TotalUnblendedCostMicros,
		Metric:                   savedReport.Metrics[0],
		MetricTotalMicros:        savedResult.TotalUnblendedCostMicros,
	})
	if err != nil {
		t.Fatalf("RecordLastRun(saved report) error = %v", err)
	}
	if ranReport.LastRunStatus != "succeeded" ||
		ranReport.LastRunRowCount != 2 ||
		ranReport.LastRunTotalUnblendedCostMicros != 1_996_800 ||
		ranReport.LastRunMetric != "unblended_cost" ||
		ranReport.LastRunMetricTotalMicros != 1_996_800 ||
		ranReport.LastRunAt != "2026-02-03T00:00:00Z" {
		t.Fatalf("saved report last-run metadata = %+v, want successful query metadata", ranReport)
	}
}
