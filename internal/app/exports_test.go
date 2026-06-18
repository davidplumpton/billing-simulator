package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestCURCSVExportFilenameIncludesRequestVariantDimensions(t *testing.T) {
	t.Parallel()

	base := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
	})
	if want := "cur-2026-02-01-2026-03-01-payer-999988887777-usage-all-accounts-status-all-statuses-limit-default.csv"; base != want {
		t.Fatalf("curCSVExportFilename(base) = %q, want %q", base, want)
	}

	filtered := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Limit:              25,
	})
	if want := "cur-2026-02-01-2026-03-01-payer-999988887777-usage-111122223333-status-final-limit-25.csv"; filtered != want {
		t.Fatalf("curCSVExportFilename(filtered) = %q, want %q", filtered, want)
	}
	if filtered == base {
		t.Fatal("curCSVExportFilename() collapsed filtered and unfiltered exports to the same filename")
	}

	memberScoped := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Visibility:         persistence.BillingVisibilityFilter{UsageAccountID: "111122223333"},
		Limit:              25,
	})
	if want := "cur-2026-02-01-2026-03-01-payer-999988887777-usage-111122223333-status-final-limit-25-visibility-usage-111122223333.csv"; memberScoped != want {
		t.Fatalf("curCSVExportFilename(member scoped) = %q, want %q", memberScoped, want)
	}
	if memberScoped == filtered {
		t.Fatal("curCSVExportFilename() collapsed member-scoped and management-scoped usage exports to the same filename")
	}
}

func TestDirectCSVExportHEADSkipsCSVRendering(t *testing.T) {
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

	query := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
	}
	tests := []struct {
		name         string
		target       string
		handle       func(exportsHandler, http.ResponseWriter, *http.Request)
		wantFilename string
	}{
		{
			name:         "CUR CSV",
			target:       "/exports/cur.csv?" + query.Encode(),
			handle:       exportsHandler.handleCURCSV,
			wantFilename: "cur-2026-02-01-2026-03-01-payer-999988887777-usage-all-accounts-status-all-statuses-limit-default.csv",
		},
		{
			name:         "FOCUS CSV",
			target:       "/exports/focus.csv?" + query.Encode(),
			handle:       exportsHandler.handleFOCUSCSV,
			wantFilename: "focus-2026-02-01-2026-03-01-payer-999988887777-usage-all-accounts-status-all-statuses-limit-default.csv",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newExportsHandler(db)
			handler.cur = failingCSVExportRepository{t: t}
			req := httptest.NewRequest(http.MethodHead, tc.target, nil)
			recorder := httptest.NewRecorder()
			tc.handle(handler, recorder, req)
			resp := recorder.Result()
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("read HEAD %s body: %v", tc.target, err)
				}
				t.Fatalf("HEAD %s status = %d, want %d; body=%s", tc.target, resp.StatusCode, http.StatusOK, body)
			}
			if recorder.Body.Len() != 0 {
				t.Fatalf("HEAD %s wrote %d response body bytes, want none", tc.target, recorder.Body.Len())
			}
			if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
				t.Fatalf("HEAD %s content type = %q, want text/csv", tc.target, contentType)
			}
			if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, tc.wantFilename) {
				t.Fatalf("HEAD %s content disposition = %q, want filename %q", tc.target, disposition, tc.wantFilename)
			}
		})
	}
}

func TestFOCUSCSVExportDownloadAndStoredGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspacePath := t.TempDir()
	db, err := persistence.OpenWorkspace(ctx, workspacePath)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-focus-export-ui",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "FOCUS export UI web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"app": "storefront",
		},
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-focus-export-ui",
		ResourceID:          "resource-focus-export-ui",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock close time) error = %v", err)
	}
	closeResult, err := persistence.NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
		PayerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		InvoiceDueDays: 14,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-02T09:30:00Z"); err != nil {
		t.Fatalf("Set(clock export time) error = %v", err)
	}

	server := httptest.NewServer(newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	exportRepo := persistence.NewExportFileRepository(db, workspacePath)

	query := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
	}
	focusRequest := persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
	}
	exportFilename := focusCSVExportFilename(focusRequest)
	metadataFilename := focusCSVMetadataFilename(focusRequest)
	assertExportNotStored := func(filename string) {
		t.Helper()
		if _, err := exportRepo.GetByFilename(ctx, filename); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetByFilename(%s) after direct GET error = %v, want sql.ErrNoRows", filename, err)
		}
		if _, err := os.Stat(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filename)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%s) after direct GET error = %v, want missing file", filename, err)
		}
	}

	resp, err := client.Get(server.URL + "/exports/focus.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /exports/focus.csv error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/focus.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /exports/focus.csv content type = %q, want text/csv", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, exportFilename) {
		t.Fatalf("GET /exports/focus.csv content disposition = %q, want FOCUS filename", disposition)
	}
	assertHEADDownloadMatchesGET(t, newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}), "/exports/focus.csv?"+query.Encode(), resp.Header, "Content-Type", "Content-Disposition")
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read FOCUS CSV response: %v\n%s", err, body)
	}
	initialCSVBody := body
	if len(records) != 3 {
		t.Fatalf("FOCUS CSV response records = %d (%+v), want header plus usage and support rows", len(records), records)
	}
	if got := strings.Join(records[0][:4], ","); got != "x_SimulatorExportGeneratedAt,x_SimulatorSourceBillId,x_SimulatorLineItemId,x_SimulatorSchema" {
		t.Fatalf("FOCUS CSV header prefix = %q, want simulator metadata columns", got)
	}
	usage := requireCSVResponseRecord(t, records, "ResourceId", "resource-focus-export-ui")
	for column, want := range map[string]string{
		"x_SimulatorSourceBillId": closeResult.Bill.ID,
		"BillingAccountId":        persistence.AnyCompanyRetailManagementAccountID,
		"SubAccountId":            "111122223333",
		"InvoiceId":               closeResult.InvoiceObligation.InvoiceID,
		"EffectiveCost":           "0.083200",
		"ResourceName":            "FOCUS export UI web",
		"Tags":                    `{"app":"storefront"}`,
	} {
		if got := usage[csvResponseColumnIndex(t, records[0], column)]; got != want {
			t.Fatalf("FOCUS CSV usage column %s = %q, want %q in %v", column, got, want, usage)
		}
	}
	assertExportNotStored(exportFilename)
	assertExportNotStored(metadataFilename)

	resp, err = client.PostForm(server.URL+"/exports/generate-focus", query)
	if err != nil {
		t.Fatalf("POST /exports/generate-focus error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-focus final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated "+exportFilename+" from 2 source rows") ||
		!strings.Contains(body, "Generate FOCUS Export") ||
		!strings.Contains(body, "FOCUS CSV") ||
		!strings.Contains(body, "FOCUS Metadata JSON") {
		t.Fatalf("POST /exports/generate-focus body missing stored FOCUS export state: %s", body)
	}
	exportRecord, err := exportRepo.GetByFilename(ctx, exportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(FOCUS export) error = %v", err)
	}
	if exportRecord.ExportType != persistence.ExportFileTypeFOCUSCSV ||
		exportRecord.GenerationParameters["schema"] != "FOCUS-like" ||
		exportRecord.GenerationParameters["target_focus_spec_version"] != persistence.FOCUSTargetSpecificationVersion ||
		exportRecord.GenerationParameters["focus_dataset"] != persistence.FOCUSTargetDataset ||
		exportRecord.GenerationParameters["conformance_claim"] != persistence.FOCUSConformanceClaim ||
		exportRecord.GenerationParameters["source_bill_id"] != closeResult.Bill.ID ||
		exportRecord.GenerationParameters["rows_written"] != "2" {
		t.Fatalf("stored FOCUS export metadata = %+v, want FOCUS schema and source metadata", exportRecord)
	}
	exportContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), exportFilename))
	if err != nil {
		t.Fatalf("read stored FOCUS CSV export: %v", err)
	}
	if string(exportContent) != initialCSVBody {
		t.Fatalf("stored FOCUS CSV export differs from direct response:\nfile=%s\nbody=%s", exportContent, initialCSVBody)
	}
	metadataRecord, err := exportRepo.GetByFilename(ctx, metadataFilename)
	if err != nil {
		t.Fatalf("GetByFilename(FOCUS metadata export) error = %v", err)
	}
	if metadataRecord.ExportType != persistence.ExportFileTypeFOCUSMetadataJSON ||
		metadataRecord.GenerationParameters["source_export_filename"] != exportFilename ||
		metadataRecord.GenerationParameters["target_focus_spec_version"] != persistence.FOCUSTargetSpecificationVersion ||
		metadataRecord.GenerationParameters["validator_expected_result"] != persistence.FOCUSConformanceClaim ||
		metadataRecord.GenerationParameters["source_bill_id"] != closeResult.Bill.ID {
		t.Fatalf("stored FOCUS metadata record = %+v, want v1.4 validator sidecar metadata", metadataRecord)
	}
	metadataContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), metadataFilename))
	if err != nil {
		t.Fatalf("read stored FOCUS metadata JSON: %v", err)
	}
	var metadata persistence.FOCUSCSVExportMetadata
	if err := json.Unmarshal(metadataContent, &metadata); err != nil {
		t.Fatalf("decode stored FOCUS metadata JSON: %v\n%s", err, metadataContent)
	}
	if metadata.TargetFOCUSSpecVersion != persistence.FOCUSTargetSpecificationVersion ||
		metadata.Dataset != persistence.FOCUSTargetDataset ||
		metadata.Conformance.Claim != persistence.FOCUSConformanceClaim ||
		metadata.Validator.ExpectedResult != persistence.FOCUSConformanceClaim ||
		metadata.Visibility.Scope != "payer-account" ||
		metadata.Visibility.AccountID != persistence.AnyCompanyRetailManagementAccountID ||
		metadata.Visibility.DocumentIdentifiersHidden ||
		metadata.SourceBillID != closeResult.Bill.ID ||
		metadata.SourceExportFilename != exportFilename {
		t.Fatalf("stored FOCUS metadata JSON = %+v, want payer-scoped v1.4 conformance boundary", metadata)
	}
	metadataDownloadPath := exportFileDownloadPath(metadataFilename)
	resp, err = client.Get(server.URL + metadataDownloadPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", metadataDownloadPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", metadataDownloadPath, resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("GET %s content type = %q, want application/json", metadataDownloadPath, contentType)
	}
	metadataChecksum := sha256.Sum256(metadataContent)
	if got := resp.Header.Get("X-Simulator-Export-Checksum"); got != hex.EncodeToString(metadataChecksum[:]) {
		t.Fatalf("GET %s checksum header = %q, want metadata checksum", metadataDownloadPath, got)
	}
	assertStoredExportHEADMatchesGET(t, newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}), metadataDownloadPath)

	memberQuery := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
		"line_item_status":     {"final"},
		"viewer_role":          {"member-account"},
		"viewer_account_id":    {"111122223333"},
	}
	memberRequest := persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Visibility:         persistence.BillingVisibilityFilter{UsageAccountID: "111122223333"},
	}
	memberFilename := focusCSVExportFilename(memberRequest)
	memberMetadataFilename := focusCSVMetadataFilename(memberRequest)
	resp, err = client.Get(server.URL + "/exports/focus.csv?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/focus.csv member error = %v", err)
	}
	memberBody := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/focus.csv member status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, memberBody)
	}
	memberRecords, err := csv.NewReader(strings.NewReader(memberBody)).ReadAll()
	if err != nil {
		t.Fatalf("read member FOCUS CSV response: %v\n%s", err, memberBody)
	}
	if len(memberRecords) != 2 {
		t.Fatalf("member FOCUS CSV records = %d (%+v), want header plus one visible row", len(memberRecords), memberRecords)
	}
	memberUsage := requireCSVResponseRecord(t, memberRecords, "SubAccountId", "111122223333")
	if got := memberUsage[csvResponseColumnIndex(t, memberRecords[0], "InvoiceId")]; got != "" {
		t.Fatalf("member FOCUS InvoiceId = %q, want hidden payer document", got)
	}
	if strings.Contains(memberBody, "AWS Support") || strings.Contains(memberBody, closeResult.Bill.ID) {
		t.Fatalf("member FOCUS export leaked payer-scoped data: %s", memberBody)
	}
	assertExportNotStored(memberFilename)
	assertExportNotStored(memberMetadataFilename)
	resp, err = client.PostForm(server.URL+"/exports/generate-focus", memberQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-focus member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-focus member final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated "+memberFilename+" from 1 source rows") ||
		strings.Contains(body, exportFilename) {
		t.Fatalf("POST /exports/generate-focus member body = %s, want scoped FOCUS export only", body)
	}
	memberRecord, err := exportRepo.GetByFilename(ctx, memberFilename)
	if err != nil {
		t.Fatalf("GetByFilename(member FOCUS export) error = %v", err)
	}
	if memberRecord.ExportType != persistence.ExportFileTypeFOCUSCSV ||
		memberRecord.GenerationParameters["visibility_scope"] != "usage-account" ||
		memberRecord.GenerationParameters["source_bill_id"] != "" ||
		memberRecord.GenerationParameters["rows_written"] != "1" {
		t.Fatalf("member FOCUS export metadata = %+v, want member-scoped metadata without payer document", memberRecord)
	}
	memberMetadataContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), memberMetadataFilename))
	if err != nil {
		t.Fatalf("read member FOCUS metadata JSON: %v", err)
	}
	if strings.Contains(string(memberMetadataContent), closeResult.Bill.ID) ||
		strings.Contains(string(memberMetadataContent), closeResult.InvoiceObligation.InvoiceID) {
		t.Fatalf("member FOCUS metadata leaked payer documents: %s", memberMetadataContent)
	}
	var memberMetadata persistence.FOCUSCSVExportMetadata
	if err := json.Unmarshal(memberMetadataContent, &memberMetadata); err != nil {
		t.Fatalf("decode member FOCUS metadata JSON: %v\n%s", err, memberMetadataContent)
	}
	if memberMetadata.SourceBillID != "" ||
		memberMetadata.SourceExportFilename != memberFilename ||
		memberMetadata.Visibility.Scope != "usage-account" ||
		memberMetadata.Visibility.AccountID != "111122223333" ||
		!memberMetadata.Visibility.DocumentIdentifiersHidden {
		t.Fatalf("member FOCUS metadata JSON = %+v, want usage-scoped sidecar without payer documents", memberMetadata)
	}
	assertStoredExportHEADMatchesGET(t, newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}), exportFileDownloadPathWithViewer(memberMetadataFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))
}

func TestCURCSVExportDownloadIncludesBillMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspacePath := t.TempDir()
	db, err := persistence.OpenWorkspace(ctx, workspacePath)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-cur-export-ui",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "CUR export UI web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"app": "storefront",
		},
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-cur-export-ui",
		ResourceID:          "resource-cur-export-ui",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock close time) error = %v", err)
	}
	closeResult, err := persistence.NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
		PayerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		InvoiceDueDays: 14,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-02T09:30:00Z"); err != nil {
		t.Fatalf("Set(clock export time) error = %v", err)
	}

	server := httptest.NewServer(newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}))
	t.Cleanup(server.Close)
	client := server.Client()

	query := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
	}
	exportFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
	})
	exportRepo := persistence.NewExportFileRepository(db, workspacePath)
	assertExportNotStored := func(filename string) {
		t.Helper()
		if _, err := exportRepo.GetByFilename(ctx, filename); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetByFilename(%s) after direct GET error = %v, want sql.ErrNoRows", filename, err)
		}
		if _, err := os.Stat(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filename)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%s) after direct GET error = %v, want missing file", filename, err)
		}
	}
	resp, err := client.Get(server.URL + "/exports/cur.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/cur.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /exports/cur.csv content type = %q, want text/csv", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, exportFilename) {
		t.Fatalf("GET /exports/cur.csv content disposition = %q, want CUR filename", disposition)
	}
	assertHEADDownloadMatchesGET(t, newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}), "/exports/cur.csv?"+query.Encode(), resp.Header, "Content-Type", "Content-Disposition")
	if storedFilename := resp.Header.Get("X-Simulator-Export-Filename"); storedFilename != "" {
		t.Fatalf("GET /exports/cur.csv stored filename header = %q, want no persisted export header", storedFilename)
	}

	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read CUR CSV response: %v\n%s", err, body)
	}
	initialCSVBody := body
	if len(records) != 3 {
		t.Fatalf("CUR CSV response records = %d (%+v), want header plus usage and support rows", len(records), records)
	}
	if got := strings.Join(records[0][:3], ","); got != "export_generated_at,source_bill_id,line_item_id" {
		t.Fatalf("CUR CSV header prefix = %q, want metadata then line_item_id", got)
	}
	assertExportNotStored(exportFilename)
	checksum := sha256.Sum256([]byte(body))
	wantChecksum := hex.EncodeToString(checksum[:])
	if got := resp.Header.Get("X-Simulator-Export-Checksum"); got != "" {
		t.Fatalf("GET /exports/cur.csv checksum header = %q, want no persisted export checksum", got)
	}
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", query)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/exports" {
		t.Fatalf("POST /exports/generate-cur final path = %q, want /exports", got)
	}
	if !strings.Contains(body, "Generated "+exportFilename+" from 2 source rows") {
		t.Fatalf("POST /exports/generate-cur body missing flash: %s", body)
	}
	exportContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), exportFilename))
	if err != nil {
		t.Fatalf("read stored CUR CSV export: %v", err)
	}
	if string(exportContent) != initialCSVBody {
		t.Fatalf("stored CUR CSV export differs from direct response:\nfile=%s\nbody=%s", exportContent, initialCSVBody)
	}
	exportRecord, err := exportRepo.GetByFilename(ctx, exportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(CUR export) error = %v", err)
	}
	if exportRecord.ExportType != persistence.ExportFileTypeCURCSV ||
		exportRecord.BillingPeriodStart != "2026-02-01" ||
		exportRecord.BillingPeriodEnd != "2026-03-01" ||
		exportRecord.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID ||
		exportRecord.UsageAccountID != "" ||
		exportRecord.SizeBytes != int64(len(initialCSVBody)) ||
		exportRecord.ChecksumSHA256 != wantChecksum ||
		exportRecord.GenerationParameters["generated_at"] != "2026-03-02T09:30:00Z" ||
		exportRecord.GenerationParameters["source_bill_id"] != closeResult.Bill.ID ||
		exportRecord.GenerationParameters["rows_written"] != "2" {
		t.Fatalf("stored CUR export metadata = %+v, want response metadata", exportRecord)
	}

	filteredQuery := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
		"usage_account_id":     {"111122223333"},
		"line_item_status":     {"final"},
		"limit":                {"1"},
	}
	filteredExportFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Limit:              1,
	})
	resp, err = client.Get(server.URL + "/exports/cur.csv?" + filteredQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/cur.csv filtered status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	filteredCSVBody := body
	if storedFilename := resp.Header.Get("X-Simulator-Export-Filename"); storedFilename != "" {
		t.Fatalf("GET /exports/cur.csv filtered stored filename header = %q, want no persisted export header", storedFilename)
	}
	if filteredExportFilename == exportFilename {
		t.Fatalf("filtered export filename = base filename %q, want distinct request variants", filteredExportFilename)
	}
	filteredRecords, err := csv.NewReader(strings.NewReader(filteredCSVBody)).ReadAll()
	if err != nil {
		t.Fatalf("read filtered CUR CSV response: %v\n%s", err, filteredCSVBody)
	}
	if len(filteredRecords) != 2 {
		t.Fatalf("filtered CUR CSV records = %d (%+v), want header plus one usage row", len(filteredRecords), filteredRecords)
	}
	if filteredCSVBody == initialCSVBody {
		t.Fatalf("filtered CUR CSV body matched all-account body; filename variants should represent different content")
	}
	assertExportNotStored(filteredExportFilename)
	filteredChecksum := sha256.Sum256([]byte(filteredCSVBody))
	wantFilteredChecksum := hex.EncodeToString(filteredChecksum[:])
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", filteredQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur filtered final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated "+filteredExportFilename+" from 1 source rows") {
		t.Fatalf("POST /exports/generate-cur filtered body missing flash: %s", body)
	}
	filteredExportContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filteredExportFilename))
	if err != nil {
		t.Fatalf("read filtered stored CUR CSV export: %v", err)
	}
	if string(filteredExportContent) != filteredCSVBody {
		t.Fatalf("filtered stored CUR CSV export differs from response:\nfile=%s\nbody=%s", filteredExportContent, filteredCSVBody)
	}
	filteredExportRecord, err := exportRepo.GetByFilename(ctx, filteredExportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(filtered CUR export) error = %v", err)
	}
	if filteredExportRecord.ExportType != persistence.ExportFileTypeCURCSV ||
		filteredExportRecord.BillingPeriodStart != "2026-02-01" ||
		filteredExportRecord.BillingPeriodEnd != "2026-03-01" ||
		filteredExportRecord.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID ||
		filteredExportRecord.UsageAccountID != "111122223333" ||
		filteredExportRecord.SizeBytes != int64(len(filteredCSVBody)) ||
		filteredExportRecord.ChecksumSHA256 != wantFilteredChecksum ||
		filteredExportRecord.GenerationParameters["usage_account_id"] != "111122223333" ||
		filteredExportRecord.GenerationParameters["line_item_status"] != "final" ||
		filteredExportRecord.GenerationParameters["limit"] != "1" ||
		filteredExportRecord.GenerationParameters["rows_written"] != "1" {
		t.Fatalf("filtered CUR export metadata = %+v, want request-specific metadata", filteredExportRecord)
	}
	storedExports, err := exportRepo.List(ctx, persistence.ExportFileListRequest{
		ExportType:         persistence.ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
	})
	if err != nil {
		t.Fatalf("List(CUR export variants) error = %v", err)
	}
	if len(storedExports) != 2 {
		t.Fatalf("List(CUR export variants) returned %d rows: %+v, want base and filtered exports", len(storedExports), storedExports)
	}
	storedExportNames := map[string]bool{}
	for _, storedExport := range storedExports {
		storedExportNames[storedExport.Filename] = true
	}
	if !storedExportNames[exportFilename] || !storedExportNames[filteredExportFilename] {
		t.Fatalf("stored export filenames = %+v, want %q and %q", storedExportNames, exportFilename, filteredExportFilename)
	}
	for _, row := range []struct {
		filename  string
		createdAt string
		updatedAt string
	}{
		{filename: exportFilename, createdAt: "2000-01-01T00:00:00.000Z", updatedAt: "2000-01-01T00:00:00.000Z"},
		{filename: filteredExportFilename, createdAt: "2001-01-01T00:00:00.000Z", updatedAt: "2001-01-01T00:00:00.000Z"},
	} {
		if _, err := db.ExecContext(ctx, `UPDATE workspace_export_files SET created_at = ?, updated_at = ? WHERE filename = ?`, row.createdAt, row.updatedAt, row.filename); err != nil {
			t.Fatalf("set deterministic export timestamps for %s: %v", row.filename, err)
		}
	}

	resp, err = client.Get(server.URL + "/exports")
	if err != nil {
		t.Fatalf("GET /exports error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Generated Exports",
		"Generate CUR Export",
		"2 files",
		"recently updated first",
		exportFilename,
		filteredExportFilename,
		"Download",
		"Regenerate",
		"Reconcile",
		closeResult.Bill.ID,
		"2 rows",
		"1 rows",
		"usage 111122223333",
		"final",
		shortChecksum(wantChecksum),
		shortChecksum(wantFilteredChecksum),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports body missing %q: %s", want, body)
		}
	}
	baseExportIndex := strings.Index(body, exportFilename)
	filteredExportIndex := strings.Index(body, filteredExportFilename)
	if baseExportIndex == -1 || filteredExportIndex == -1 || filteredExportIndex > baseExportIndex {
		t.Fatalf("GET /exports order put base index %d and filtered index %d, want filtered newer export before base export: %s", baseExportIndex, filteredExportIndex, body)
	}

	downloadPath := exportFileDownloadPath(exportFilename)
	resp, err = client.Get(server.URL + downloadPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", downloadPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", downloadPath, resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET %s content type = %q, want text/csv", downloadPath, contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, exportFilename) {
		t.Fatalf("GET %s content disposition = %q, want stored filename", downloadPath, disposition)
	}
	if got := resp.Header.Get("X-Simulator-Export-Checksum"); got != wantChecksum {
		t.Fatalf("GET %s checksum header = %q, want %q", downloadPath, got, wantChecksum)
	}
	if body != initialCSVBody {
		t.Fatalf("GET %s body differs from generated CSV:\ndownload=%s\ninitial=%s", downloadPath, body, initialCSVBody)
	}
	assertStoredExportHEADMatchesGET(t, newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}), downloadPath)

	filteredDownloadPath := exportFileDownloadPath(filteredExportFilename)
	resp, err = client.Get(server.URL + filteredDownloadPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", filteredDownloadPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", filteredDownloadPath, resp.StatusCode, http.StatusOK, body)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, filteredExportFilename) {
		t.Fatalf("GET %s content disposition = %q, want filtered stored filename", filteredDownloadPath, disposition)
	}
	if got := resp.Header.Get("X-Simulator-Export-Checksum"); got != wantFilteredChecksum {
		t.Fatalf("GET %s checksum header = %q, want %q", filteredDownloadPath, got, wantFilteredChecksum)
	}
	if body != filteredCSVBody {
		t.Fatalf("GET %s body differs from generated filtered CSV:\ndownload=%s\ninitial=%s", filteredDownloadPath, body, filteredCSVBody)
	}

	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-02T10:15:00Z"); err != nil {
		t.Fatalf("Set(clock regeneration time) error = %v", err)
	}
	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename": {exportFilename},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/regenerate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/exports" {
		t.Fatalf("POST /exports/regenerate final path = %q, want /exports", got)
	}
	if !strings.Contains(body, "Regenerated "+exportFilename+" from 2 source rows") {
		t.Fatalf("POST /exports/regenerate body missing flash: %s", body)
	}
	exportRecord, err = persistence.NewExportFileRepository(db, workspacePath).GetByFilename(ctx, exportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(regenerated CUR export) error = %v", err)
	}
	if exportRecord.GenerationParameters["generated_at"] != "2026-03-02T10:15:00Z" ||
		exportRecord.GenerationParameters["rows_written"] != "2" {
		t.Fatalf("regenerated CUR export metadata = %+v, want refreshed generation parameters", exportRecord)
	}
	regeneratedContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), exportFilename))
	if err != nil {
		t.Fatalf("read regenerated CUR CSV export: %v", err)
	}
	if !strings.Contains(string(regeneratedContent), "2026-03-02T10:15:00Z") {
		t.Fatalf("regenerated CUR CSV missing refreshed generated_at: %s", regeneratedContent)
	}
	resp, err = client.Get(server.URL + "/exports")
	if err != nil {
		t.Fatalf("GET /exports after regeneration error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports after regeneration status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	baseExportIndex = strings.Index(body, exportFilename)
	filteredExportIndex = strings.Index(body, filteredExportFilename)
	if baseExportIndex == -1 || filteredExportIndex == -1 || baseExportIndex > filteredExportIndex {
		t.Fatalf("GET /exports after regeneration put base index %d and filtered index %d, want regenerated base export first: %s", baseExportIndex, filteredExportIndex, body)
	}

	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-02T10:45:00Z"); err != nil {
		t.Fatalf("Set(clock filtered regeneration time) error = %v", err)
	}
	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename": {filteredExportFilename},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/regenerate filtered final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Regenerated "+filteredExportFilename+" from 1 source rows") {
		t.Fatalf("POST /exports/regenerate filtered body missing flash: %s", body)
	}
	filteredExportRecord, err = persistence.NewExportFileRepository(db, workspacePath).GetByFilename(ctx, filteredExportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(regenerated filtered CUR export) error = %v", err)
	}
	if filteredExportRecord.GenerationParameters["generated_at"] != "2026-03-02T10:45:00Z" ||
		filteredExportRecord.GenerationParameters["rows_written"] != "1" ||
		filteredExportRecord.GenerationParameters["usage_account_id"] != "111122223333" ||
		filteredExportRecord.GenerationParameters["line_item_status"] != "final" ||
		filteredExportRecord.GenerationParameters["limit"] != "1" {
		t.Fatalf("regenerated filtered CUR export metadata = %+v, want preserved request dimensions and refreshed result metadata", filteredExportRecord)
	}
	filteredRegeneratedContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filteredExportFilename))
	if err != nil {
		t.Fatalf("read regenerated filtered CUR CSV export: %v", err)
	}
	if !strings.Contains(string(filteredRegeneratedContent), "2026-03-02T10:45:00Z") {
		t.Fatalf("regenerated filtered CUR CSV missing refreshed generated_at: %s", filteredRegeneratedContent)
	}
	baseContentAfterFilteredRegeneration, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), exportFilename))
	if err != nil {
		t.Fatalf("read base CUR CSV export after filtered regeneration: %v", err)
	}
	if !strings.Contains(string(baseContentAfterFilteredRegeneration), "2026-03-02T10:15:00Z") ||
		strings.Contains(string(baseContentAfterFilteredRegeneration), "2026-03-02T10:45:00Z") {
		t.Fatalf("filtered regeneration overwrote base export content: %s", baseContentAfterFilteredRegeneration)
	}

	usage := requireCSVResponseRecord(t, records, "resource_id", "resource-cur-export-ui")
	for column, want := range map[string]string{
		"export_generated_at": "2026-03-02T09:30:00Z",
		"source_bill_id":      closeResult.Bill.ID,
		"payer_account_id":    persistence.AnyCompanyRetailManagementAccountID,
		"usage_account_id":    "111122223333",
		"usage_amount":        "2.000000",
		"unblended_cost":      "0.083200",
		"tags_json":           `{"app":"storefront"}`,
	} {
		if got := usage[csvResponseColumnIndex(t, records[0], column)]; got != want {
			t.Fatalf("CUR CSV usage column %s = %q, want %q in %v", column, got, want, usage)
		}
	}

	support := requireCSVResponseRecord(t, records, "service_code", "AWSSupport")
	if got := support[csvResponseColumnIndex(t, records[0], "source_bill_id")]; got != closeResult.Bill.ID {
		t.Fatalf("CUR CSV support source_bill_id = %q, want %q", got, closeResult.Bill.ID)
	}
	if got := support[csvResponseColumnIndex(t, records[0], "line_item_type")]; got != "Fee" {
		t.Fatalf("CUR CSV support line_item_type = %q, want Fee", got)
	}

	query.Set("line_item_status", "final")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Export Reconciliation",
		"Bill and Invoice Comparison",
		"balanced",
		"CUR CSV",
		closeResult.Bill.ID,
		closeResult.InvoiceObligation.InvoiceID,
		"CUR-like CSV",
		"$1.0832",
		"$0.00",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation body missing %q: %s", want, body)
		}
	}

	query.Set("usage_account_id", "111122223333")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation filtered status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"excluded-lines",
		"111122223333",
		"final",
		"$0.0832",
		"$1.00",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation filtered body missing %q: %s", want, body)
		}
	}

	limitedReconciliationQuery := url.Values{}
	for key, values := range query {
		limitedReconciliationQuery[key] = values
	}
	limitedReconciliationQuery.Set("limit", "1")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + limitedReconciliationQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation limited error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation limited status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"excluded-lines",
		`href="/exports/cur.csv?`,
		"limit=1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation limited body missing %q: %s", want, body)
		}
	}

	viewerOnlyReconciliationQuery := url.Values{
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	}
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + viewerOnlyReconciliationQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation viewer-only filter error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /exports/reconciliation viewer-only filter status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	for _, want := range []string{
		`href="/exports/reconciliation">Clear</a>`,
		`name="viewer_account_id" value="111122223333"`,
		`value="member-account" selected`,
		"CUR-like export reconciliation billing period start and end are required",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation viewer-only filter body missing %q: %s", want, body)
		}
	}

	memberQuery := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
		"line_item_status":     {"final"},
		"limit":                {"2"},
		"viewer_role":          {"member-account"},
		"viewer_account_id":    {"111122223333"},
	}
	memberExportFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Visibility:         persistence.BillingVisibilityFilter{UsageAccountID: "111122223333"},
		Limit:              2,
	})
	resp, err = client.Get(server.URL + "/exports/cur.csv?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/cur.csv member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if storedFilename := resp.Header.Get("X-Simulator-Export-Filename"); storedFilename != "" {
		t.Fatalf("GET /exports/cur.csv member viewer stored filename = %q, want no persisted export header", storedFilename)
	}
	memberCSVBody := body
	memberRecords, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read member CUR CSV response: %v\n%s", err, body)
	}
	if len(memberRecords) != 2 {
		t.Fatalf("member CUR CSV records = %d (%+v), want header plus own usage row", len(memberRecords), memberRecords)
	}
	memberUsage := requireCSVResponseRecord(t, memberRecords, "usage_account_id", "111122223333")
	if got := memberUsage[csvResponseColumnIndex(t, memberRecords[0], "source_bill_id")]; got != "" {
		t.Fatalf("member CUR CSV source_bill_id = %q, want payer document hidden", got)
	}
	if strings.Contains(body, "AWSSupport") || strings.Contains(body, "999988887777,999988887777") {
		t.Fatalf("member CUR CSV leaked payer-scoped support row: %s", body)
	}
	assertExportNotStored(memberExportFilename)
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", memberQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur member viewer final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Query().Get("viewer_role"); got != "member-account" {
		t.Fatalf("POST /exports/generate-cur member viewer final viewer_role = %q, want preserved member-account", got)
	}
	if !strings.Contains(body, "Generated "+memberExportFilename+" from 1 source rows") ||
		strings.Contains(body, exportFilename) {
		t.Fatalf("POST /exports/generate-cur member viewer body = %s, want scoped flash and no all-account export", body)
	}
	memberExportContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), memberExportFilename))
	if err != nil {
		t.Fatalf("read member stored CUR CSV export: %v", err)
	}
	if string(memberExportContent) != memberCSVBody {
		t.Fatalf("member stored CUR CSV export differs from direct response:\nfile=%s\nbody=%s", memberExportContent, memberCSVBody)
	}
	memberRecord, err := exportRepo.GetByFilename(ctx, memberExportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(member CUR export) error = %v", err)
	}
	if memberRecord.UsageAccountID != "111122223333" ||
		memberRecord.GenerationParameters["usage_account_id"] != "111122223333" ||
		memberRecord.GenerationParameters["visibility_scope"] != "usage-account" ||
		memberRecord.GenerationParameters["visibility_account_id"] != "111122223333" ||
		memberRecord.GenerationParameters["source_bill_id"] != "" ||
		memberRecord.GenerationParameters["rows_written"] != "1" {
		t.Fatalf("member CUR export metadata = %+v, want member-scoped export without payer bill ID", memberRecord)
	}

	managementSameShapeQuery := url.Values{}
	for key, values := range memberQuery {
		managementSameShapeQuery[key] = values
	}
	managementSameShapeQuery.Del("viewer_role")
	managementSameShapeQuery.Del("viewer_account_id")
	managementSameShapeQuery.Set("usage_account_id", "111122223333")
	managementSameShapeFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Limit:              2,
	})
	if managementSameShapeFilename == memberExportFilename {
		t.Fatalf("management and member stored CUR export filenames both = %q, want visibility-scoped variants", memberExportFilename)
	}
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", managementSameShapeQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur matching management export error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur matching management export status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated "+managementSameShapeFilename+" from 1 source rows") {
		t.Fatalf("POST /exports/generate-cur matching management export body missing flash: %s", body)
	}
	managementSameShapeContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), managementSameShapeFilename))
	if err != nil {
		t.Fatalf("read matching management CUR CSV export: %v", err)
	}
	if !strings.Contains(string(managementSameShapeContent), closeResult.Bill.ID) {
		t.Fatalf("matching management CUR CSV export missing payer bill ID metadata: %s", managementSameShapeContent)
	}
	memberExportContentAfterManagement, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), memberExportFilename))
	if err != nil {
		t.Fatalf("read member CUR CSV export after matching management export: %v", err)
	}
	if string(memberExportContentAfterManagement) != memberCSVBody {
		t.Fatalf("matching management export overwrote member-scoped export:\nmember=%s\nwant=%s", memberExportContentAfterManagement, memberCSVBody)
	}

	crossAccountMemberQuery := url.Values{}
	for key, values := range memberQuery {
		crossAccountMemberQuery[key] = values
	}
	crossAccountMemberQuery.Set("usage_account_id", "444455556666")
	resp, err = client.Get(server.URL + "/exports/cur.csv?" + crossAccountMemberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv member cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /exports/cur.csv member cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", crossAccountMemberQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur member cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /exports/generate-cur member cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	for _, want := range []string{
		`<option value="member-account" selected>Member</option>`,
		`name="viewer_account_id" value="111122223333"`,
		`name="usage_account_id" value="444455556666"`,
		`href="/bills?viewer_account_id=111122223333&amp;viewer_role=member-account"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /exports/generate-cur member cross-account error body missing preserved viewer context %q: %s", want, body)
		}
	}

	memberListQuery := url.Values{
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	}
	resp, err = client.Get(server.URL + "/exports?" + memberListQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, memberExportFilename) {
		t.Fatalf("GET /exports member viewer missing own usage-account export: %s", body)
	}
	if strings.Contains(body, exportFilename) ||
		strings.Contains(body, filteredExportFilename) ||
		strings.Contains(body, managementSameShapeFilename) ||
		strings.Contains(body, "usage all accounts") {
		t.Fatalf("GET /exports member viewer leaked broader export: %s", body)
	}

	resp, err = client.Get(server.URL + exportFileDownloadPathWithViewer(exportFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))
	if err != nil {
		t.Fatalf("GET all-account export as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET all-account export as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.Get(server.URL + exportFileDownloadPathWithViewer(managementSameShapeFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))
	if err != nil {
		t.Fatalf("GET matching management export as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET matching management export as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.Get(server.URL + exportFileDownloadPathWithViewer(memberExportFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))
	if err != nil {
		t.Fatalf("GET member export as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET member export as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "AWSSupport") ||
		strings.Contains(body, closeResult.Bill.ID) ||
		!strings.Contains(body, "resource-cur-export-ui") {
		t.Fatalf("GET member export as member body = %s, want own usage row only", body)
	}
	assertStoredExportHEADMatchesGET(t, newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}), exportFileDownloadPathWithViewer(memberExportFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))

	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename":          {exportFilename},
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate all-account as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /exports/regenerate all-account as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename":          {memberExportFilename},
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate member export error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/regenerate member export final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Query().Get("viewer_role"); got != "member-account" {
		t.Fatalf("POST /exports/regenerate member export final viewer_role = %q, want preserved member-account", got)
	}
	if !strings.Contains(body, "Regenerated "+memberExportFilename+" from 1 source rows") ||
		strings.Contains(body, exportFilename) {
		t.Fatalf("POST /exports/regenerate member export body = %s, want scoped flash and no all-account export", body)
	}

	memberReconciliationQuery := url.Values{}
	for key, values := range memberQuery {
		memberReconciliationQuery[key] = values
	}
	memberReconciliationQuery.Del("limit")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + memberReconciliationQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"balanced",
		"111122223333",
		"$0.0832",
		"visible-line-items",
		"not-available",
		"viewer_role=member-account",
		`href="/exports/reconciliation">Clear</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation member viewer body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{"$1.0832", "$1.00", "AWSSupport"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /exports/reconciliation member viewer leaked %q: %s", leaked, body)
		}
	}

	resp, err = client.Get(server.URL + "/exports/reconciliation?" + crossAccountMemberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation member cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /exports/reconciliation member cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
}

// failingCSVExportRepository fails tests if a direct HEAD handler reaches CSV rendering.
type failingCSVExportRepository struct {
	t *testing.T
}

func (r failingCSVExportRepository) WriteCSVExport(context.Context, io.Writer, persistence.CURCSVExportRequest) (persistence.CURCSVExportResult, error) {
	r.t.Helper()
	r.t.Fatal("WriteCSVExport was called for a HEAD request")
	return persistence.CURCSVExportResult{}, nil
}

func (r failingCSVExportRepository) WriteFOCUSCSVExport(context.Context, io.Writer, persistence.CURCSVExportRequest) (persistence.CURCSVExportResult, error) {
	r.t.Helper()
	r.t.Fatal("WriteFOCUSCSVExport was called for a HEAD request")
	return persistence.CURCSVExportResult{}, nil
}

func (r failingCSVExportRepository) GetReconciliationReport(context.Context, persistence.CURExportReconciliationRequest) (persistence.CURExportReconciliationReport, error) {
	r.t.Helper()
	r.t.Fatal("GetReconciliationReport was called by a direct CSV handler")
	return persistence.CURExportReconciliationReport{}, nil
}
