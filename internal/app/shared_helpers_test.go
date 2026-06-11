package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestMethodNotAllowedWritesConsistentResponse(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	methodNotAllowed(recorder, http.MethodGet, http.MethodHead)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if got := recorder.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if got := recorder.Body.String(); got != "method not allowed\n" {
		t.Fatalf("body = %q, want method not allowed line", got)
	}
}

func TestSharedFormattingHelpers(t *testing.T) {
	t.Parallel()

	quantityCases := []struct {
		name  string
		value int64
		want  string
	}{
		{name: "whole", value: 2_000_000, want: "2"},
		{name: "fraction", value: 1_500_000, want: "1.5"},
		{name: "micro", value: 1, want: "0.000001"},
	}
	for _, tc := range quantityCases {
		t.Run("quantity "+tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatQuantityMicros(tc.value); got != tc.want {
				t.Fatalf("formatQuantityMicros(%d) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}

	usdCases := []struct {
		name  string
		value int64
		want  string
	}{
		{name: "zero", value: 0, want: "$0.00"},
		{name: "whole", value: 12_000_000, want: "$12.00"},
		{name: "cents", value: 12_340_000, want: "$12.34"},
		{name: "micros", value: 12_345_678, want: "$12.345678"},
		{name: "negative", value: -1_500_000, want: "-$1.50"},
	}
	for _, tc := range usdCases {
		t.Run("usd "+tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatUSDMicros(tc.value); got != tc.want {
				t.Fatalf("formatUSDMicros(%d) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestSharedDisplayHelpers(t *testing.T) {
	t.Parallel()

	if got := displayOptionalValue("  "); got != "none" {
		t.Fatalf("displayOptionalValue(blank) = %q, want none", got)
	}
	if got := displayOptionalValue("  Example  "); got != "Example" {
		t.Fatalf("displayOptionalValue(value) = %q, want trimmed value", got)
	}
	if got := titleLabel("past_due-member"); got != "Past Due Member" {
		t.Fatalf("titleLabel = %q, want Past Due Member", got)
	}

	views := keyValueViews(map[string]string{"zeta": "last", "alpha": "first"})
	if len(views) != 2 {
		t.Fatalf("keyValueViews length = %d, want 2", len(views))
	}
	if views[0] != (keyValueView{Key: "alpha", Value: "first"}) || views[1] != (keyValueView{Key: "zeta", Value: "last"}) {
		t.Fatalf("keyValueViews sorted = %#v", views)
	}
	if got := keyValueViews(nil); got != nil {
		t.Fatalf("keyValueViews(nil) = %#v, want nil", got)
	}
}

func TestSharedQueryHelpers(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/resources?flash=%20Saved%20", nil)
	if got := flashFromQuery(request); got != "Saved" {
		t.Fatalf("flashFromQuery = %q, want Saved", got)
	}
	if got := urlQueryEscape("Created acct/a b"); got != "Created%20acct%2Fa%20b" {
		t.Fatalf("urlQueryEscape = %q, want escaped spaces and slash", got)
	}

	values := url.Values{}
	appendQueryValue(values, "blank", "  ")
	appendQueryValue(values, "name", " AnyCompany ")
	if _, ok := values["blank"]; ok {
		t.Fatalf("appendQueryValue stored blank value: %#v", values)
	}
	if got := values.Get("name"); got != "AnyCompany" {
		t.Fatalf("appendQueryValue = %q, want trimmed value", got)
	}
}

func TestViewerHelpersPreserveScope(t *testing.T) {
	t.Parallel()

	viewer := exportViewerFieldsFromValues(url.Values{
		"viewer_role":       {" member-account "},
		"viewer_account_id": {" 111122223333 "},
	})
	if viewer != (exportViewerFields{Role: "member-account", AccountID: "111122223333"}) {
		t.Fatalf("exportViewerFieldsFromValues = %#v", viewer)
	}

	billsSelect := billsViewerRoleSelect("")
	if len(billsSelect.Options) == 0 || billsSelect.Options[0].Label != "All viewers" {
		t.Fatalf("billsViewerRoleSelect first option = %#v", billsSelect.Options)
	}
	exportsSelect := exportsViewerRoleSelect("member-account")
	if len(exportsSelect.Options) == 0 || exportsSelect.Options[0].Label != "Default viewer" {
		t.Fatalf("exportsViewerRoleSelect first option = %#v", exportsSelect.Options)
	}
	if !selectHasSelectedValue(exportsSelect, "member-account") {
		t.Fatalf("exportsViewerRoleSelect did not select member-account: %#v", exportsSelect.Options)
	}

	assertPathQuery(t, exportsPathWithViewer(viewer, "Done saved"), "/exports", map[string]string{
		"flash":             "Done saved",
		"viewer_role":       "member-account",
		"viewer_account_id": "111122223333",
	})
	assertPathQuery(t, billsPathWithExportViewer(viewer), "/bills", map[string]string{
		"viewer_role":       "member-account",
		"viewer_account_id": "111122223333",
	})
	assertPathQuery(t, paymentsPathWithViewer(viewer, "Retry queued"), "/payments", map[string]string{
		"flash":             "Retry queued",
		"viewer_role":       "member-account",
		"viewer_account_id": "111122223333",
	})
}

func TestExportPathHelpersPreserveRequestAndViewerScope(t *testing.T) {
	t.Parallel()

	viewer := exportViewerFields{Role: "finance", AccountID: "999988887777"}
	assertPathQuery(t, exportFileDownloadPathWithViewer("cur export.csv", viewer), "/exports/files/cur export.csv", map[string]string{
		"viewer_role":       "finance",
		"viewer_account_id": "999988887777",
	})
	if filename, ok := exportFileDownloadFilenameFromPath(exportFileDownloadPath("cur export.csv")); !ok || filename != "cur export.csv" {
		t.Fatalf("exportFileDownloadFilenameFromPath = %q, %v; want cur export.csv, true", filename, ok)
	}

	assertPathQuery(t, curCSVExportPathWithViewer(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-02-28",
		PayerAccountID:     "999988887777",
		LineItemStatus:     "final",
		Limit:              25,
	}, viewer), "/exports/cur.csv", map[string]string{
		"billing_period_start": "2026-02-01",
		"billing_period_end":   "2026-02-28",
		"payer_account_id":     "999988887777",
		"line_item_status":     "final",
		"limit":                "25",
		"viewer_role":          "finance",
		"viewer_account_id":    "999988887777",
	})

	assertPathQuery(t, curExportReconciliationPathWithViewer(persistence.CURExportReconciliationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-02-28",
		PayerAccountID:     "999988887777",
		UsageAccountID:     "111122223333",
		LineItemStatus:     "estimated",
	}, viewer), "/exports/reconciliation", map[string]string{
		"billing_period_start": "2026-02-01",
		"billing_period_end":   "2026-02-28",
		"payer_account_id":     "999988887777",
		"usage_account_id":     "111122223333",
		"line_item_status":     "estimated",
		"viewer_role":          "finance",
		"viewer_account_id":    "999988887777",
	})
}

func selectHasSelectedValue(field uiSelectFieldView, value string) bool {
	for _, option := range field.Options {
		if option.Value == value && option.Selected {
			return true
		}
	}
	return false
}

func assertPathQuery(t *testing.T, rawPath, wantPath string, wantQuery map[string]string) {
	t.Helper()

	parsed, err := url.Parse(rawPath)
	if err != nil {
		t.Fatalf("parse %q: %v", rawPath, err)
	}
	if parsed.Path != wantPath {
		t.Fatalf("path = %q, want %q from %q", parsed.Path, wantPath, rawPath)
	}
	query := parsed.Query()
	for key, want := range wantQuery {
		if got := query.Get(key); got != want {
			t.Fatalf("query[%s] = %q, want %q from %q", key, got, want, rawPath)
		}
	}
	for key := range query {
		if _, ok := wantQuery[key]; !ok && strings.TrimSpace(query.Get(key)) != "" {
			t.Fatalf("unexpected query[%s] = %q from %q", key, query.Get(key), rawPath)
		}
	}
}
