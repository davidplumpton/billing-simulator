package app

import "net/http"

type overviewHandler struct{}

// newOverviewHandler builds the static learner introduction page.
func newOverviewHandler() overviewHandler {
	return overviewHandler{}
}

// handleOverview renders the first-stop guide without requiring workspace data.
func (h overviewHandler) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}

	renderPage(w, http.StatusOK, pageLayoutOptions{
		Title:     "Overview - AWS Billing Simulator",
		ActiveNav: "overview",
		MainClass: "overview-page",
	}, overviewPageTemplate, nil, "render overview page")
}

var overviewPageTemplate = newPageTemplate("overview-page", `<div class="page-heading">
	<div>
		<h1>Simulator Overview</h1>
		<p>A first stop for learning how synthetic Organizations, usage, bills, invoices, reports, and scenario labs fit together.</p>
	</div>
	<div class="page-actions">
		<a class="button-link" href="/workspaces">Open Workspace</a>
		<a class="button-link secondary" href="/scenarios">Scenario Labs</a>
		<a class="button-link secondary" href="/resources">Resource Lab</a>
	</div>
</div>

<section class="overview-safety" aria-labelledby="overview-safety-title">
	<div>
		<h2 id="overview-safety-title">Local Synthetic Training</h2>
		<p>The simulator runs locally with deterministic teaching data. It does not use AWS credentials, does not process real payments, does not create tax-valid invoices, and does not provide authoritative AWS pricing advice.</p>
	</div>
	<div class="overview-safety-grid">
		<span>No AWS credentials</span>
		<span>No real payments</span>
		<span>Not tax-valid invoices</span>
		<span>Synthetic pricing</span>
	</div>
</section>

<section aria-labelledby="overview-flow-title">
	<div class="section-heading">
		<div>
			<h2 id="overview-flow-title">Core Interaction Flow</h2>
			<p>Most labs follow the same billing chain, with reports and allocation tools reading from the line items created along the way.</p>
			<p class="overview-flow-summary">In short: organization/accounts create visibility context; resources produce usage; metering/pricing creates bill line items; closes issue bills/invoices; payments modify invoice state; tags and Cost Categories affect reporting/allocation; exports/query lab consume generated billing data; scenarios seed repeatable labs.</p>
		</div>
	</div>
	<div class="overview-flow">
		<div class="overview-flow-step">
			<span>1</span>
			<div>
				<strong>Organization and accounts create visibility context.</strong>
				<p><a href="/organization">Organizations</a> model payer accounts, member accounts, OUs, and the viewing scope used by billing pages.</p>
			</div>
		</div>
		<div class="overview-flow-step">
			<span>2</span>
			<div>
				<strong>Resources produce usage.</strong>
				<p><a href="/resources">Resources</a> hold synthetic workloads, tags, usage events, daily metering controls, and month-end close actions.</p>
			</div>
		</div>
		<div class="overview-flow-step">
			<span>3</span>
			<div>
				<strong>Metering and pricing create bill line items.</strong>
				<p>The billing pipeline turns usage into priced line items, service summaries, support rows, bills, and invoice obligations.</p>
			</div>
		</div>
		<div class="overview-flow-step">
			<span>4</span>
			<div>
				<strong>Bills, invoices, and payments show financial state.</strong>
				<p><a href="/bills">Bills</a>, <a href="/invoices">Invoices</a>, and <a href="/payments">Payments</a> explain close results, invoice documents, failed collection, retries, and payment history.</p>
			</div>
		</div>
		<div class="overview-flow-step">
			<span>5</span>
			<div>
				<strong>Tags and Cost Categories change allocation.</strong>
				<p><a href="/tags">Tags</a> and <a href="/cost-categories">Cost Categories</a> teach activation delays, missing coverage, ordered rules, and split-charge allocation.</p>
			</div>
		</div>
		<div class="overview-flow-step">
			<span>6</span>
			<div>
				<strong>Reports, budgets, exports, and Query Lab consume billing data.</strong>
				<p><a href="/cost-explorer">Cost Explorer</a>, <a href="/budgets">Budgets</a>, <a href="/exports">Exports</a>, and <a href="/query-lab">Query Lab</a> use generated billing rows for analysis and reconciliation.</p>
			</div>
		</div>
	</div>
</section>

<section aria-labelledby="overview-workflows-title">
	<div class="section-heading">
		<div>
			<h2 id="overview-workflows-title">Available Workflows</h2>
			<p>Each area is usable on its own, and scenario labs combine them into repeatable exercises.</p>
		</div>
	</div>
	<div class="overview-card-grid">
		<a class="overview-card" href="/organization">
			<strong>Organizations</strong>
			<span>Explore the AnyCompany Retail payer, OUs, linked accounts, lifecycle actions, and billing visibility links.</span>
		</a>
		<a class="overview-card" href="/resources">
			<strong>Resources and Usage</strong>
			<span>Create resources, tag them, record usage, generate deterministic activity, meter usage, and close periods.</span>
		</a>
		<a class="overview-card" href="/bills">
			<strong>Bills, Invoices, Payments</strong>
			<span>Review bill totals, invoice documents, CSV evidence, failed payments, retries, collections, and method fixes.</span>
		</a>
		<a class="overview-card" href="/scenarios">
			<strong>Scenarios</strong>
			<span>Launch packaged labs, resume objectives, review feedback, reset to a seed, clone workspaces, and archive evidence.</span>
		</a>
		<a class="overview-card" href="/tags">
			<strong>Tags and Cost Categories</strong>
			<span>Activate discovered tags, inspect untagged spend, preview category rules, and compare split-charge allocation.</span>
		</a>
		<a class="overview-card" href="/cost-explorer">
			<strong>Cost Explorer and Budgets</strong>
			<span>Build grouped reports, save report definitions, inspect line items, track actuals, forecasts, and alert history.</span>
		</a>
		<a class="overview-card" href="/exports">
			<strong>Exports and Query Lab</strong>
			<span>Generate CUR-like CSV files, reconcile exports to bills, and run external SQL examples over local data.</span>
		</a>
	</div>
</section>

<section aria-labelledby="overview-start-title">
	<div class="section-heading">
		<div>
			<h2 id="overview-start-title">Safe Starting Paths</h2>
			<p>Choose the path that matches how much existing workspace state you want to keep.</p>
		</div>
	</div>
	<div class="overview-start-grid">
		<div class="overview-start-item">
			<strong>Open or create a workspace</strong>
			<p>Use <a href="/workspaces">Workspaces</a> when you want a local database directory for durable practice.</p>
		</div>
		<div class="overview-start-item">
			<strong>Launch a packaged scenario</strong>
			<p>Use <a href="/scenarios">Scenarios</a> to seed a repeatable lab with objectives, checks, and feedback.</p>
		</div>
		<div class="overview-start-item">
			<strong>Reset a scenario to seed</strong>
			<p>Scenario reset rebuilds the current workspace database around the selected lab seed, so use it when you want the original exercise state again.</p>
		</div>
		<div class="overview-start-item">
			<strong>Clone before experimenting</strong>
			<p>Workspace clone copies the active workspace and switches the session to the copy, preserving the original for comparison.</p>
		</div>
		<div class="overview-start-item">
			<strong>Start from a fresh workspace</strong>
			<p>Create a new workspace path when you want clean seed data and no scenario or practice history.</p>
		</div>
	</div>
</section>
`)
