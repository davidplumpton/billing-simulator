package app

import (
	"net/http"
	"net/http/httptest"
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
