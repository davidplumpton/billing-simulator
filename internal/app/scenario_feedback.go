package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type scenarioFeedbackPageData struct {
	WorkspaceReady      bool
	WorkspaceEmptyState uiEmptyStateView
	Notices             []uiNoticeView
	Report              scenarioFeedbackReportView
	Tables              scenarioFeedbackTablesView
}

type scenarioFeedbackTablesView struct {
	Actions uiTableView
	Checks  uiTableView
}

type scenarioFeedbackReportView struct {
	HasReport        bool
	ScenarioRunID    string
	DefinitionName   string
	Status           string
	StatusClass      string
	ProgressState    string
	ProgressClass    string
	CurrentObjective string
	Summary          string
	EvidenceSummary  string
	ConceptSummary   string
	StartedAt        string
	CompletedAt      string
	Actions          []scenarioFeedbackActionView
	Checks           []scenarioFeedbackCheckView
	BackPath         string
}

type scenarioFeedbackActionView struct {
	Sequence       string
	ActionID       string
	ActionType     string
	Status         string
	StatusClass    string
	CompletedAt    string
	WhatChanged    string
	Evidence       string
	DataSource     string
	BillingConcept string
	ErrorMessage   string
}

type scenarioFeedbackCheckView struct {
	Sequence       string
	CheckID        string
	CheckType      string
	Status         string
	StatusClass    string
	Expected       string
	Actual         string
	Message        string
	DataSource     string
	BillingConcept string
	EvaluatedAt    string
}

// handleScenarioFeedback renders the learner-facing evidence report for one scenario run.
func (h scenarioHandler) handleScenarioFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	runID := strings.TrimSpace(r.URL.Query().Get("scenario_run_id"))
	h.renderScenarioFeedback(w, r, runID)
}

func (h scenarioHandler) renderScenarioFeedback(w http.ResponseWriter, r *http.Request, runID string) {
	status := http.StatusOK
	errorMessage := ""
	data := scenarioFeedbackPageData{
		WorkspaceReady:      h.db != nil,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Tables: scenarioFeedbackTablesView{
			Actions: uiTable(uiTableHeaders("Seq", "Action", "Status", "What Changed", "Evidence", "Data Source", "AWS Mapping"), "No learner actions recorded"),
			Checks:  uiTable(uiTableHeaders("Seq", "Check", "Status", "Expected", "Actual", "Data Source", "AWS Mapping"), "No assessment checks recorded"),
		},
	}
	if h.db != nil {
		if runID == "" {
			status = http.StatusBadRequest
			errorMessage = "scenario_run_id is required"
		} else {
			report, err := h.loadScenarioFeedbackReport(r.Context(), runID)
			if err != nil {
				status = http.StatusInternalServerError
				if errors.Is(err, sql.ErrNoRows) {
					status = http.StatusNotFound
				}
				errorMessage = "load feedback report: " + err.Error()
			} else {
				data.Report = report
			}
		}
	}
	data.Notices = uiNotices("", errorMessage)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Scenario Feedback - Billing Simulator",
		ActiveNav: "scenarios",
	}, scenarioFeedbackPageTemplate, data, "render scenario feedback page")
}

func (h scenarioHandler) loadScenarioFeedbackReport(ctx context.Context, runID string) (scenarioFeedbackReportView, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return scenarioFeedbackReportView{}, fmt.Errorf("scenario run ID is required")
	}
	run, err := h.scenarioRunByID(ctx, runID)
	if err != nil {
		return scenarioFeedbackReportView{}, err
	}

	progressRepo := persistence.NewScenarioLearnerProgressRepository(h.db)
	progress, err := progressRepo.Get(ctx, runID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return scenarioFeedbackReportView{}, err
	}
	actions, err := progressRepo.ListActions(ctx, runID)
	if err != nil {
		return scenarioFeedbackReportView{}, err
	}
	checks, err := progressRepo.ListCheckResults(ctx, runID)
	if err != nil {
		return scenarioFeedbackReportView{}, err
	}
	return scenarioFeedbackReportFromAudit(run, progress, actions, checks), nil
}

func scenarioFeedbackReportFromAudit(run scenarioRunAudit, progress persistence.ScenarioLearnerProgress, actions []persistence.ScenarioLearnerAction, checks []persistence.ScenarioLearnerCheckResult) scenarioFeedbackReportView {
	completedAt := run.CompletedAt
	if completedAt == "" {
		completedAt = run.StartedAt
	}
	report := scenarioFeedbackReportView{
		HasReport:       true,
		ScenarioRunID:   run.ID,
		DefinitionName:  run.DefinitionName,
		Status:          titleLabel(run.Status),
		StatusClass:     scenarioStatusClass(run.Status),
		Summary:         scenarioFeedbackSummary(run),
		EvidenceSummary: "scenario_runs, scenario_run_events, scenario_learner_actions, and scenario_learner_check_results hold the audit trail for this run.",
		ConceptSummary:  "The simulator maps scenario actions to AWS billing concepts by following usage from organization/account scope into metering, line items, bills, invoices, reporting dimensions, and payment state.",
		StartedAt:       run.StartedAt,
		CompletedAt:     completedAt,
		BackPath:        "/scenarios",
		Actions:         make([]scenarioFeedbackActionView, 0, len(actions)),
		Checks:          make([]scenarioFeedbackCheckView, 0, len(checks)),
	}
	if progress.ScenarioRunID != "" {
		report.ProgressState = titleLabel(progress.CurrentObjectiveState)
		report.ProgressClass = scenarioProgressStatusClass(progress.CurrentObjectiveState)
		report.CurrentObjective = progress.CurrentObjective
	}
	for _, action := range actions {
		report.Actions = append(report.Actions, scenarioFeedbackActionViewFromProgress(action))
	}
	for _, check := range checks {
		report.Checks = append(report.Checks, scenarioFeedbackCheckViewFromResult(check))
	}
	return report
}

func scenarioFeedbackSummary(run scenarioRunAudit) string {
	return fmt.Sprintf(
		"This run applied %d of %d scenario events, created %d resources, recorded %d usage events, generated %d metering records, priced %d bill line items, and issued %d bills.",
		run.EventsSucceeded,
		run.EventsTotal,
		run.ResourcesCreated,
		run.UsageEventsCreated,
		run.MeteringRecordsCreated,
		run.BillLineItemsCreated,
		run.BillsIssued,
	)
}

func scenarioFeedbackActionViewFromProgress(action persistence.ScenarioLearnerAction) scenarioFeedbackActionView {
	actionType := strings.ToLower(strings.TrimSpace(action.ActionType))
	return scenarioFeedbackActionView{
		Sequence:       strconv.Itoa(action.ActionSequence),
		ActionID:       action.ActionID,
		ActionType:     titleLabel(action.ActionType),
		Status:         titleLabel(action.ActionStatus),
		StatusClass:    scenarioProgressStatusClass(action.ActionStatus),
		CompletedAt:    action.CompletedAt,
		WhatChanged:    scenarioActionWhatChanged(actionType),
		Evidence:       displayOrDash(action.Evidence),
		DataSource:     scenarioActionDataSource(actionType),
		BillingConcept: scenarioActionBillingConcept(actionType),
		ErrorMessage:   action.ErrorMessage,
	}
}

func scenarioFeedbackCheckViewFromResult(check persistence.ScenarioLearnerCheckResult) scenarioFeedbackCheckView {
	checkType := strings.ToLower(strings.TrimSpace(check.CheckType))
	return scenarioFeedbackCheckView{
		Sequence:       strconv.Itoa(check.CheckSequence),
		CheckID:        check.CheckID,
		CheckType:      titleLabel(check.CheckType),
		Status:         titleLabel(check.Status),
		StatusClass:    scenarioCheckStatusClass(check.Status),
		Expected:       displayOrDash(check.Expected),
		Actual:         displayOrDash(check.Actual),
		Message:        check.Message,
		DataSource:     scenarioCheckDataSource(checkType),
		BillingConcept: scenarioCheckBillingConcept(checkType),
		EvaluatedAt:    check.EvaluatedAt,
	}
}

func scenarioCheckStatusClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "passed":
		return "status-succeeded"
	case "failed":
		return "status-failed"
	default:
		return ""
	}
}

func scenarioFeedbackPath(runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ""
	}
	return "/scenarios/feedback?scenario_run_id=" + urlQueryEscape(runID)
}

func scenarioActionWhatChanged(actionType string) string {
	switch actionType {
	case "create_account":
		return "Added or reused a linked account in the synthetic organization."
	case "create_resource":
		return "Created or reused a billable resource in a usage account."
	case "add_usage":
		return "Recorded explicit usage for a resource in the simulated billing period."
	case "generate_usage":
		return "Generated deterministic usage events across the scenario window."
	case "advance_clock":
		return "Moved simulator time to the next scenario event boundary."
	case "run_daily_metering":
		return "Converted eligible usage events into metering records and estimated bill line items."
	case "close_billing_period":
		return "Finalized the billing period, protected final line items, and issued bill and invoice records."
	case "issue_bill":
		return "Issued bill and invoice artifacts for the payer account."
	case "refresh_cost_allocation_tags":
		return "Rebuilt the billing-side discovered tag inventory from resource tags."
	case "activate_cost_allocation_tag":
		return "Moved a discovered tag key into active or pending billing-report visibility."
	case "create_cost_category":
		return "Created or reused a business Cost Category dimension."
	case "create_cost_category_rule":
		return "Created or reused an ordered Cost Category classification rule."
	case "create_cost_category_split_rule":
		return "Created or reused a Cost Category split-charge allocation rule."
	case "create_budget":
		return "Created or reused a monthly budget guardrail with actual and forecast thresholds."
	case "refresh_budget_forecasts":
		return "Recomputed budget forecast summaries and in-app alert notifications for the billing period."
	case "create_savings_plan":
		return "Created a simplified Compute Savings Plan commitment for estimated billing coverage."
	case "create_saved_report":
		return "Created or updated a Cost Explorer saved report definition for the lab drilldown."
	case "create_payment_method":
		return "Created a simulated payer payment method."
	case "schedule_payment", "process_payment", "fail_payment", "mark_payment_due":
		return "Moved the latest invoice obligation through the simulated payment lifecycle."
	case "mark_payment_past_due":
		return "Moved the latest invoice obligation into past-due remediation."
	case "collect_payment":
		return "Applied simulated funds to the latest invoice obligation and updated remaining balance."
	default:
		return "Recorded a scenario action outcome."
	}
}

func scenarioActionDataSource(actionType string) string {
	switch actionType {
	case "create_account":
		return "accounts, organization_account_hierarchy, account_lifecycle_events"
	case "create_resource":
		return "resources, resource_tags"
	case "add_usage", "generate_usage":
		return "resources, usage_events"
	case "advance_clock":
		return "simulator_clock"
	case "run_daily_metering":
		return "metering_records, bill_line_items"
	case "close_billing_period", "issue_bill":
		return "bills, bill_line_items, invoice_obligations, invoice_documents"
	case "refresh_cost_allocation_tags", "activate_cost_allocation_tag":
		return "cost_allocation_tag_keys, cost_allocation_tag_inventory, cost_allocation_tag_activation_events"
	case "create_cost_category":
		return "cost_categories"
	case "create_cost_category_rule":
		return "cost_category_rules, cost_category_line_item_assignments"
	case "create_cost_category_split_rule":
		return "cost_category_split_charge_rules, cost_category_split_charge_allocations"
	case "create_budget":
		return "budgets, budget_thresholds"
	case "refresh_budget_forecasts":
		return "budget_forecast_summaries, budget_alert_notifications"
	case "create_savings_plan":
		return "savings_plan_purchases, savings_plan_line_item_sources, bill_line_items"
	case "create_saved_report":
		return "saved_reports"
	case "create_payment_method":
		return "payment_profiles, payment_methods"
	case "schedule_payment", "process_payment", "fail_payment", "mark_payment_due", "mark_payment_past_due", "collect_payment":
		return "invoice_obligations, invoice_payment_states, invoice_payment_events"
	default:
		return "scenario_run_events"
	}
}

func scenarioActionBillingConcept(actionType string) string {
	switch actionType {
	case "create_account":
		return "AWS Organizations account structure determines usage ownership and payer visibility."
	case "create_resource":
		return "Billable resources create the inventory that later accrues metered usage."
	case "add_usage", "generate_usage":
		return "Usage quantities are the raw input that pricing converts into billed charges."
	case "advance_clock":
		return "Billing reports are time-windowed, so the effective clock controls open-period visibility."
	case "run_daily_metering":
		return "Estimated billing turns usage into metered and priced line items before month end."
	case "close_billing_period", "issue_bill":
		return "Final bills and invoices tie payer obligations back to immutable source line items."
	case "refresh_cost_allocation_tags", "activate_cost_allocation_tag":
		return "Cost allocation tags must be discovered and activated before they appear in billing reports."
	case "create_cost_category", "create_cost_category_rule", "create_cost_category_split_rule":
		return "Cost Categories classify and allocate spend through ordered business rules."
	case "create_budget":
		return "Budgets compare actual and forecast spend against learner-defined thresholds for a billing period."
	case "refresh_budget_forecasts":
		return "Budget forecasts estimate open-period spend and alert notifications surface threshold breaches before month end."
	case "create_savings_plan":
		return "Savings Plans add commitment fees and coverage negations that reports reconcile back to source usage."
	case "create_saved_report":
		return "Cost Explorer saved reports preserve reusable grouping, filter, metric, and chart choices for spend analysis."
	case "create_payment_method", "schedule_payment", "process_payment", "fail_payment", "mark_payment_due", "mark_payment_past_due", "collect_payment":
		return "Payment lifecycle changes invoice collection state without changing underlying usage charges."
	default:
		return "Scenario audit rows make the billing lab reproducible and inspectable."
	}
}

func scenarioCheckDataSource(checkType string) string {
	switch checkType {
	case "saved_report_exists":
		return "saved_reports"
	case "identifies_top_driver":
		return "bill_line_items"
	case "cost_allocation_tag_activated":
		return "cost_allocation_tag_keys"
	case "cost_category_rule_created":
		return "cost_categories, cost_category_rules"
	case "bill_reconciled":
		return "bills, bill_line_items"
	case "payment_status":
		return "invoice_obligations, invoice_payment_states, invoice_payment_events"
	default:
		return "scenario_learner_check_results"
	}
}

func scenarioCheckBillingConcept(checkType string) string {
	switch checkType {
	case "saved_report_exists":
		return "Cost Explorer saved reports preserve the analysis definition learners used to inspect spend."
	case "identifies_top_driver":
		return "Cost Explorer-style grouping identifies the dominant service or usage driver in bill line items."
	case "cost_allocation_tag_activated":
		return "Activated cost allocation tags become available for allocation and reporting after visibility delay."
	case "cost_category_rule_created":
		return "Cost Category rules map raw account, service, and tag dimensions into business labels."
	case "bill_reconciled":
		return "Bill reconciliation proves invoice totals tie back to final billed line items."
	case "payment_status":
		return "Invoice payment status reflects collection progress and failure remediation."
	default:
		return "Assessment evidence connects a learner outcome to persisted simulator state."
	}
}

func displayOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

var scenarioFeedbackPageTemplate = newPageTemplate("scenario-feedback-page", `<div class="page-heading">
			<div>
				<h1>Learner Feedback</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="/scenarios">Scenarios</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else if not .Report.HasReport}}
			<section class="empty">
				<h2>No Feedback Report</h2>
				<p>No scenario run is selected.</p>
			</section>
		{{else}}
			<section>
				<div class="section-heading">
					<h2>{{.Report.DefinitionName}}</h2>
					<span>{{.Report.ScenarioRunID}}</span>
				</div>
				<div class="state-grid scenario-feedback-state">
					<div class="state-card">
						<span>Run Status</span>
						<strong><span class="status {{.Report.StatusClass}}">{{.Report.Status}}</span></strong>
					</div>
					<div class="state-card">
						<span>Progress</span>
						<strong>{{if .Report.ProgressState}}<span class="status {{.Report.ProgressClass}}">{{.Report.ProgressState}}</span>{{else}}-{{end}}</strong>
					</div>
					<div class="state-card">
						<span>Actions</span>
						<strong>{{len .Report.Actions}}</strong>
					</div>
					<div class="state-card">
						<span>Checks</span>
						<strong>{{len .Report.Checks}}</strong>
					</div>
					<div class="state-card">
						<span>Started</span>
						<strong>{{.Report.StartedAt}}</strong>
					</div>
					<div class="state-card">
						<span>Completed</span>
						<strong>{{.Report.CompletedAt}}</strong>
					</div>
				</div>
			</section>

			<section class="panel scenario-feedback-report">
				<div class="section-heading">
					<h2>What Changed</h2>
				</div>
				<div class="scenario-feedback-copy">
					<p>{{.Report.Summary}}</p>
					<p>{{.Report.EvidenceSummary}}</p>
					<p>{{.Report.ConceptSummary}}</p>
					{{if .Report.CurrentObjective}}<p>{{.Report.CurrentObjective}}</p>{{end}}
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Action Evidence</h2>
					<span>{{len .Report.Actions}} rows</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table scenario-feedback-table">
						{{template "ui.dense-table-head" .Tables.Actions}}
						<tbody>
							{{range .Report.Actions}}
								<tr>
									<td>{{.Sequence}}</td>
									<td><strong>{{.ActionType}}</strong><small>{{.ActionID}}</small></td>
									<td><span class="status {{.StatusClass}}">{{.Status}}</span>{{if .ErrorMessage}}<small>{{.ErrorMessage}}</small>{{end}}</td>
									<td>{{.WhatChanged}}</td>
									<td>{{.Evidence}}</td>
									<td>{{.DataSource}}</td>
									<td>{{.BillingConcept}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Actions}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Assessment Evidence</h2>
					<span>{{len .Report.Checks}} rows</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table scenario-feedback-table">
						{{template "ui.dense-table-head" .Tables.Checks}}
						<tbody>
							{{range .Report.Checks}}
								<tr>
									<td>{{.Sequence}}</td>
									<td><strong>{{.CheckType}}</strong><small>{{.CheckID}}</small>{{if .Message}}<small>{{.Message}}</small>{{end}}</td>
									<td><span class="status {{.StatusClass}}">{{.Status}}</span></td>
									<td>{{.Expected}}</td>
									<td>{{.Actual}}</td>
									<td>{{.DataSource}}</td>
									<td>{{.BillingConcept}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Checks}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
