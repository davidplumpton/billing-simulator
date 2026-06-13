package app

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestQueryLabPageShowsCURCSVExamples(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/query-lab")
	if err != nil {
		t.Fatalf("GET /query-lab error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /query-lab status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Query Lab",
		`href="/exports"`,
		`href="/scenarios"`,
		"/path/to/export.csv",
		"Linked Account Totals",
		"Untagged Spend",
		"Top Usage Types",
		"Invoice Reconciliation",
		"Allocated Cost Comparison",
		"read_csv_auto",
		"json_extract_string(tags_json",
		"json_extract_string(cost_categories_json",
		"source_bill_id",
		"Shared Networking",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /query-lab body missing %q: %s", want, body)
		}
	}
	for _, column := range persistence.CURCSVExportColumns() {
		if !strings.Contains(body, column) {
			t.Fatalf("GET /query-lab body missing CUR CSV column %q: %s", column, body)
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
	if !strings.Contains(body, `href="/query-lab"`) {
		t.Fatalf("GET /exports body missing query-lab action: %s", body)
	}
}

func TestQueryLabPageUsesSelectedCSVPath(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	selectedPath := "/tmp/generated exports/cur-selected.csv"
	resp, err := client.Get(server.URL + queryLabPathForCSVPath(selectedPath))
	if err != nil {
		t.Fatalf("GET /query-lab selected path error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /query-lab selected path status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		selectedPath,
		html.EscapeString("read_csv_auto('" + selectedPath + "')"),
		"The examples below use this CSV path.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /query-lab selected path body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, queryLabCSVPathPlaceholder) {
		t.Fatalf("GET /query-lab selected path still rendered placeholder: %s", body)
	}
}

func TestQueryLabUsesGeneratedCURExportFilenameFromExportsTable(t *testing.T) {
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

	exportRepo := persistence.NewExportFileRepository(db, workspacePath)
	curFilename := "cur-query-lab.csv"
	if _, err := exportRepo.Write(ctx, persistence.ExportFileWriteRequest{
		Filename:           curFilename,
		ExportType:         persistence.ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		Content:            []byte("export_generated_at,source_bill_id\n"),
	}); err != nil {
		t.Fatalf("Write(CUR export) error = %v", err)
	}
	focusFilename := "focus-query-lab.csv"
	if _, err := exportRepo.Write(ctx, persistence.ExportFileWriteRequest{
		Filename:           focusFilename,
		ExportType:         persistence.ExportFileTypeFOCUSCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		Content:            []byte("x_SimulatorExportGeneratedAt,BillingAccountId\n"),
	}); err != nil {
		t.Fatalf("Write(FOCUS export) error = %v", err)
	}

	server := httptest.NewServer(newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/exports")
	if err != nil {
		t.Fatalf("GET /exports error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	curQueryLabPath := queryLabPathForExportFilename(curFilename)
	if !strings.Contains(body, `href="`+curQueryLabPath+`"`) {
		t.Fatalf("GET /exports body missing CUR Query Lab link %q: %s", curQueryLabPath, body)
	}
	if strings.Contains(body, "export_filename="+url.QueryEscape(focusFilename)) {
		t.Fatalf("GET /exports body added Query Lab link for FOCUS export %q: %s", focusFilename, body)
	}

	resp, err = client.Get(server.URL + curQueryLabPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", curQueryLabPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", curQueryLabPath, resp.StatusCode, http.StatusOK, body)
	}
	selectedPath := filepath.Join(persistence.WorkspaceExportsPath(workspacePath), curFilename)
	for _, want := range []string{
		selectedPath,
		html.EscapeString("read_csv_auto('" + selectedPath + "')"),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET %s body missing %q: %s", curQueryLabPath, want, body)
		}
	}
}
