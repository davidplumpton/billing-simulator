package app

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

const queryLabCSVPathPlaceholder = "/path/to/export.csv"

type queryLabHandler struct {
	workspacePath string
}

type queryLabPageData struct {
	Actions       uiActionBarView
	CSVPath       string
	CSVPathHint   string
	SchemaColumns []string
	Examples      []queryLabExampleView
}

type queryLabExampleView struct {
	Title    string
	Scenario string
	SQL      string
}

// newQueryLabHandler builds the dependency-free CUR CSV query examples page.
func newQueryLabHandler() queryLabHandler {
	return queryLabHandler{}
}

// newWorkspaceQueryLabHandler lets generated export filenames resolve to local workspace CSV paths.
func newWorkspaceQueryLabHandler(workspacePath string) queryLabHandler {
	return queryLabHandler{workspacePath: strings.TrimSpace(workspacePath)}
}

// handleQueryLab renders example SQL that learners can run against downloaded CUR CSV exports.
func (h queryLabHandler) handleQueryLab(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}

	csvPath, selected := queryLabCSVPathFromValues(r.URL.Query(), h.workspacePath)
	data := queryLabPageData{
		Actions:       uiActionBar(uiActionLink("Exports", "/exports"), uiActionLink("Scenarios", "/scenarios")),
		CSVPath:       csvPath,
		CSVPathHint:   queryLabCSVPathHint(selected),
		SchemaColumns: persistence.CURCSVExportColumns(),
		Examples:      queryLabExamples(csvPath),
	}
	renderPage(w, http.StatusOK, pageLayoutOptions{
		Title:     "Query Lab - AWS Billing Simulator",
		ActiveNav: "query-lab",
	}, queryLabPageTemplate, data, "render query lab page")
}

func queryLabCSVPathFromValues(values url.Values, workspacePath string) (string, bool) {
	if csvPath := strings.TrimSpace(values.Get("csv_path")); csvPath != "" {
		return csvPath, true
	}
	filename := strings.TrimSpace(values.Get("export_filename"))
	if filename == "" || strings.TrimSpace(workspacePath) == "" || !safeQueryLabExportFilename(filename) {
		return queryLabCSVPathPlaceholder, false
	}
	return filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filename), true
}

func queryLabCSVPathHint(selected bool) string {
	if selected {
		return "The examples below use this CSV path."
	}
	return "Generate or select a CUR CSV export, then replace this path in any example."
}

func safeQueryLabExportFilename(filename string) bool {
	if filename == "" || filename == "." || filename == ".." || filename != filepath.Base(filename) || strings.ContainsAny(filename, `/\`) {
		return false
	}
	if len(filename) > 200 {
		return false
	}
	for _, r := range filename {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' ||
			r == '_' ||
			r == '.' {
			continue
		}
		return false
	}
	return true
}

func queryLabExamples(csvPath string) []queryLabExampleView {
	csvPath = queryLabSQLString(csvPath)
	return []queryLabExampleView{
		{
			Title:    "Linked Account Totals",
			Scenario: "Use any closed-period CUR CSV export to compare usage-account charges.",
			SQL: `WITH cur AS (
  SELECT * FROM read_csv_auto('` + csvPath + `')
)
SELECT
  usage_account_id,
  MIN(account_name) AS account_name,
  ROUND(SUM(CAST(unblended_cost AS DOUBLE)), 2) AS unblended_cost_usd
FROM cur
GROUP BY usage_account_id
ORDER BY unblended_cost_usd DESC;`,
		},
		{
			Title:    "Untagged Spend",
			Scenario: "Run after a Missing Tags or data-transfer-spike lab export to find spend with no owner tag.",
			SQL: `WITH cur AS (
  SELECT * FROM read_csv_auto('` + csvPath + `')
)
SELECT
  usage_account_id,
  service_code,
  usage_type,
  COUNT(*) AS line_items,
  ROUND(SUM(CAST(unblended_cost AS DOUBLE)), 2) AS untagged_cost_usd
FROM cur
WHERE json_extract_string(tags_json, '$.owner') IS NULL
GROUP BY usage_account_id, service_code, usage_type
ORDER BY untagged_cost_usd DESC;`,
		},
		{
			Title:    "Top Usage Types",
			Scenario: "Use the largest rows to connect Cost Explorer summaries back to detailed usage drivers.",
			SQL: `WITH cur AS (
  SELECT * FROM read_csv_auto('` + csvPath + `')
)
SELECT
  service_code,
  usage_type,
  operation,
  usage_unit,
  ROUND(SUM(CAST(usage_amount AS DOUBLE)), 2) AS total_usage,
  ROUND(SUM(CAST(unblended_cost AS DOUBLE)), 2) AS unblended_cost_usd
FROM cur
GROUP BY service_code, usage_type, operation, usage_unit
ORDER BY unblended_cost_usd DESC
LIMIT 10;`,
		},
		{
			Title:    "Invoice Reconciliation",
			Scenario: "Compare these totals with the export reconciliation page and printable invoice totals.",
			SQL: `WITH cur AS (
  SELECT * FROM read_csv_auto('` + csvPath + `')
),
typed AS (
  SELECT
    source_bill_id,
    billing_period_start,
    billing_period_end,
    invoice_entity,
    line_item_type,
    CAST(unblended_cost AS DOUBLE) AS cost
  FROM cur
)
SELECT
  source_bill_id,
  billing_period_start,
  billing_period_end,
  invoice_entity,
  COUNT(*) AS line_items,
  ROUND(SUM(CASE WHEN line_item_type IN ('Usage', 'Fee') THEN cost ELSE 0 END), 2) AS charges_usd,
  ROUND(SUM(CASE WHEN line_item_type = 'Credit' THEN cost ELSE 0 END), 2) AS credits_usd,
  ROUND(SUM(CASE WHEN line_item_type = 'Refund' THEN cost ELSE 0 END), 2) AS refunds_usd,
  ROUND(SUM(CASE WHEN line_item_type = 'Tax' THEN cost ELSE 0 END), 2) AS tax_usd,
  ROUND(SUM(cost), 2) AS export_total_usd
FROM typed
GROUP BY source_bill_id, billing_period_start, billing_period_end, invoice_entity
ORDER BY billing_period_start, source_bill_id;`,
		},
		{
			Title:    "Allocated Cost Comparison",
			Scenario: "Run after the Shared Networking allocation lab to compare raw Product category spend with a 60/40 shared-networking allocation.",
			SQL: `WITH cur AS (
  SELECT * FROM read_csv_auto('` + csvPath + `')
),
raw AS (
  SELECT
    COALESCE(json_extract_string(cost_categories_json, '$.Product'), 'Unallocated') AS product,
    SUM(CAST(unblended_cost AS DOUBLE)) AS raw_cost_usd
  FROM cur
  GROUP BY product
),
shared AS (
  SELECT COALESCE(SUM(raw_cost_usd), 0) AS shared_cost_usd
  FROM raw
  WHERE product = 'Shared Networking'
),
allocation AS (
  SELECT product, raw_cost_usd AS allocated_cost_usd
  FROM raw
  WHERE product <> 'Shared Networking'
  UNION ALL SELECT 'Storefront', shared_cost_usd * 0.60 FROM shared
  UNION ALL SELECT 'Payments', shared_cost_usd * 0.40 FROM shared
  UNION ALL SELECT 'Shared Networking', 0 FROM shared
)
SELECT
  COALESCE(raw.product, allocation.product) AS product,
  ROUND(COALESCE(raw.raw_cost_usd, 0), 2) AS raw_cost_usd,
  ROUND(SUM(allocation.allocated_cost_usd), 2) AS allocated_cost_usd,
  ROUND(SUM(allocation.allocated_cost_usd) - COALESCE(raw.raw_cost_usd, 0), 2) AS allocation_delta_usd
FROM allocation
FULL OUTER JOIN raw ON raw.product = allocation.product
GROUP BY COALESCE(raw.product, allocation.product), raw.raw_cost_usd
ORDER BY allocated_cost_usd DESC;`,
		},
	}
}

func queryLabSQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

var queryLabPageTemplate = newPageTemplate("query-lab-page", `<div class="page-heading">
			<div>
				<h1>Query Lab</h1>
			</div>
			{{template "ui.action-bar" .Actions}}
		</div>

		<section class="clock-strip query-lab-start">
			<div>
				<h2>CSV Source</h2>
				<strong>{{.CSVPath}}</strong>
				<small>{{.CSVPathHint}}</small>
			</div>
			<div class="detail-list">
				<span>Optional Engine</span>
				<strong>DuckDB CLI or another SQL client that can read CSV files</strong>
				<small>No simulator dependency or embedded query runtime is required.</small>
			</div>
		</section>

		<section>
			<div class="section-heading">
				<h2>CUR CSV Columns</h2>
				<span>{{len .SchemaColumns}} stable export fields</span>
			</div>
			<div class="schema-chip-list">
				{{range .SchemaColumns}}<code>{{.}}</code>{{end}}
			</div>
		</section>

		<section>
			<div class="section-heading">
				<h2>Examples</h2>
				<span>External SQL over generated exports</span>
			</div>
			<div class="query-lab-grid">
				{{range .Examples}}
					<article class="query-example">
						<div class="query-example-heading">
							<h3>{{.Title}}</h3>
							<small>{{.Scenario}}</small>
						</div>
						<pre class="sql-block"><code>{{.SQL}}</code></pre>
					</article>
				{{end}}
			</div>
		</section>
`)
