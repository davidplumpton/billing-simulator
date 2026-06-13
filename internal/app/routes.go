package app

import (
	"database/sql"
	"net/http"
	"strings"
)

type appRouteHandlers struct {
	overview       func() overviewHandler
	organization   func() organizationHandler
	resourceLab    func() resourceLabHandler
	tags           func() costAllocationTagsHandler
	costCategories func() costCategoriesHandler
	savingsPlans   func() savingsPlanHandler
	proForma       func() proFormaHandler
	costExplorer   func() costExplorerHandler
	anomalies      func() anomalyHandler
	exports        func() exportsHandler
	queryLab       func() queryLabHandler
	budgets        func() budgetHandler
	bills          func() billsHandler
	payments       func() paymentsHandler
	scenarios      func() scenarioHandler
}

type appRouteDefinition struct {
	pattern      string
	samplePath   string
	usesActiveDB bool
	allowed      []string
	handler      func(appRouteHandlers) http.HandlerFunc
}

var appRouteDefinitions = []appRouteDefinition{
	{
		pattern: "/assets/app.css", allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(appRouteHandlers) http.HandlerFunc { return serveAppStylesheet },
	},
	{
		pattern: "/assets/app.js", allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(appRouteHandlers) http.HandlerFunc { return serveAppScript },
	},
	{
		pattern: "/overview", allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.overview().handleOverview(w, r)
			}
		},
	},
	{
		pattern: "/organization", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.organization().handleOrganization(w, r)
			}
		},
	},
	{
		pattern: "/organization/accounts/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.organization().handleCreateAccount(w, r)
			}
		},
	},
	{
		pattern: "/organization/accounts/move", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.organization().handleMoveAccount(w, r)
			}
		},
	},
	{
		pattern: "/organization/accounts/suspend", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.organization().handleSuspendAccount(w, r)
			}
		},
	},
	{
		pattern: "/organization/accounts/close", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.organization().handleCloseAccount(w, r)
			}
		},
	},
	{
		pattern: "/resources", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleResources(w, r)
			}
		},
	},
	{
		pattern: "/resources/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleCreateResource(w, r)
			}
		},
	},
	{
		pattern: "/resources/tags", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleAddTag(w, r)
			}
		},
	},
	{
		pattern: "/resources/usage", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleRecordUsage(w, r)
			}
		},
	},
	{
		pattern: "/resources/generate", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleGenerateUsage(w, r)
			}
		},
	},
	{
		pattern: "/resources/billing-pipeline", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleRunBillingPipeline(w, r)
			}
		},
	},
	{
		pattern: "/resources/daily-metering", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleRunDailyMeteringJob(w, r)
			}
		},
	},
	{
		pattern: "/resources/month-close", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleRunMonthEndClose(w, r)
			}
		},
	},
	{
		pattern: "/clock/advance", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.resourceLab().handleAdvanceClock(w, r)
			}
		},
	},
	{
		pattern: "/tags", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.tags().handleTags(w, r)
			}
		},
	},
	{
		pattern: "/tags/refresh", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.tags().handleRefreshDiscovery(w, r)
			}
		},
	},
	{
		pattern: "/tags/activate", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.tags().handleActivateTag(w, r)
			}
		},
	},
	{
		pattern: "/tags/deactivate", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.tags().handleDeactivateTag(w, r)
			}
		},
	},
	{
		pattern: "/cost-categories", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costCategories().handleCostCategories(w, r)
			}
		},
	},
	{
		pattern: "/cost-categories/categories/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costCategories().handleCreateCostCategory(w, r)
			}
		},
	},
	{
		pattern: "/cost-categories/rules/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costCategories().handleCreateCostCategoryRule(w, r)
			}
		},
	},
	{
		pattern: "/cost-categories/splits/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costCategories().handleCreateCostCategorySplitRule(w, r)
			}
		},
	},
	{
		pattern: "/savings-plans", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.savingsPlans().handleSavingsPlans(w, r)
			}
		},
	},
	{
		pattern: "/savings-plans/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.savingsPlans().handleCreateSavingsPlan(w, r)
			}
		},
	},
	{
		pattern: "/pro-forma", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.proForma().handleProForma(w, r)
			}
		},
	},
	{
		pattern: "/pro-forma/pricing-plans/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.proForma().handleCreatePricingPlan(w, r)
			}
		},
	},
	{
		pattern: "/pro-forma/pricing-rules/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.proForma().handleCreatePricingRule(w, r)
			}
		},
	},
	{
		pattern: "/pro-forma/billing-groups/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.proForma().handleCreateBillingGroup(w, r)
			}
		},
	},
	{
		pattern: "/pro-forma/accounts/assign", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.proForma().handleAssignAccount(w, r)
			}
		},
	},
	{
		pattern: "/pro-forma/refresh", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.proForma().handleRefreshLineItems(w, r)
			}
		},
	},
	{
		pattern: "/pro-forma/custom-line-items/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.proForma().handleCreateCustomLineItem(w, r)
			}
		},
	},
	{
		pattern: "/cost-explorer", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costExplorer().handleCostExplorer(w, r)
			}
		},
	},
	{
		pattern: "/cost-explorer/results.csv", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costExplorer().handleCostExplorerResultsCSV(w, r)
			}
		},
	},
	{
		pattern: "/cost-explorer/line-items", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costExplorer().handleCostExplorerLineItems(w, r)
			}
		},
	},
	{
		pattern: "/cost-explorer/reports/save", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costExplorer().handleSaveCostExplorerReport(w, r)
			}
		},
	},
	{
		pattern: "/cost-explorer/reports/run", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.costExplorer().handleRunCostExplorerReport(w, r)
			}
		},
	},
	{
		pattern: "/anomalies", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.anomalies().handleAnomalies(w, r)
			}
		},
	},
	{
		pattern: "/anomalies/refresh", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.anomalies().handleRefreshAnomalies(w, r)
			}
		},
	},
	{
		pattern: "/exports", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleExports(w, r)
			}
		},
	},
	{
		pattern: "/exports/files/", samplePath: "/exports/files/sample.csv", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleExportFileDownload(w, r)
			}
		},
	},
	{
		pattern: "/exports/regenerate", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleRegenerateExport(w, r)
			}
		},
	},
	{
		pattern: "/exports/generate-cur", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleGenerateCURCSVExport(w, r)
			}
		},
	},
	{
		pattern: "/exports/generate-focus", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleGenerateFOCUSCSVExport(w, r)
			}
		},
	},
	{
		pattern: "/exports/cur.csv", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleCURCSV(w, r)
			}
		},
	},
	{
		pattern: "/exports/focus.csv", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleFOCUSCSV(w, r)
			}
		},
	},
	{
		pattern: "/exports/reconciliation", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.exports().handleCURReconciliation(w, r)
			}
		},
	},
	{
		pattern: "/query-lab", allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.queryLab().handleQueryLab(w, r)
			}
		},
	},
	{
		pattern: "/budgets", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.budgets().handleBudgets(w, r)
			}
		},
	},
	{
		pattern: "/budgets/create", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.budgets().handleCreateBudget(w, r)
			}
		},
	},
	{
		pattern: "/budgets/refresh", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.budgets().handleRefreshBudgets(w, r)
			}
		},
	},
	{
		pattern: "/bills", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.bills().handleBills(w, r)
			}
		},
	},
	{
		pattern: "/invoices", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.bills().handleInvoiceIndex(w, r)
			}
		},
	},
	{
		pattern: "/invoices/", samplePath: "/invoices/SIM-INV-MISSING", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.bills().handleInvoice(w, r)
			}
		},
	},
	{
		pattern: "/payments", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.payments().handlePayments(w, r)
			}
		},
	},
	{
		pattern: "/payments/action", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.payments().handlePaymentAction(w, r)
			}
		},
	},
	{
		pattern: "/scenarios", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleScenarios(w, r)
			}
		},
	},
	{
		pattern: "/scenarios/feedback", usesActiveDB: true, allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleScenarioFeedback(w, r)
			}
		},
	},
	{
		pattern: "/scenarios/editor", allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleScenarioEditor(w, r)
			}
		},
	},
	{
		pattern: "/scenarios/editor/validate", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleValidateScenarioEditor(w, r)
			}
		},
	},
	{
		pattern: "/scenarios/launch", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleLaunchScenario(w, r)
			}
		},
	},
	{
		pattern: "/scenarios/reset", allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleResetScenario(w, r)
			}
		},
	},
	{
		pattern: "/scenarios/clone", allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleCloneWorkspace(w, r)
			}
		},
	},
	{
		pattern: "/scenarios/archive", usesActiveDB: true, allowed: []string{http.MethodPost},
		handler: func(h appRouteHandlers) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				h.scenarios().handleArchiveScenario(w, r)
			}
		},
	},
	{
		pattern: "/healthz", allowed: []string{http.MethodGet, http.MethodHead},
		handler: func(appRouteHandlers) http.HandlerFunc { return handleHealthCheck },
	},
}

// registerAppRoutes installs every non-workspace app route from the shared route table.
func registerAppRoutes(mux *http.ServeMux, handlers appRouteHandlers) {
	for _, route := range appRouteDefinitions {
		mux.HandleFunc(route.pattern, route.handler(handlers))
	}
}

// directAppRouteHandlers builds fixed-DB handlers for focused tests and direct handler checks.
func directAppRouteHandlers(db *sql.DB) appRouteHandlers {
	overview := newOverviewHandler()
	organization := newOrganizationHandler(db)
	resourceLab := newResourceLabHandler(db)
	tags := newCostAllocationTagsHandler(db)
	costCategories := newCostCategoriesHandler(db)
	savingsPlans := newSavingsPlanHandler(db)
	proForma := newProFormaHandler(db)
	costExplorer := newCostExplorerHandler(db)
	anomalies := newAnomalyHandler(db)
	exports := newExportsHandler(db)
	queryLab := newQueryLabHandler()
	budgets := newBudgetHandler(db)
	bills := newBillsHandler(db)
	payments := newPaymentsHandler(db)
	scenarios := newScenarioHandler(db)

	return appRouteHandlers{
		overview:       func() overviewHandler { return overview },
		organization:   func() organizationHandler { return organization },
		resourceLab:    func() resourceLabHandler { return resourceLab },
		tags:           func() costAllocationTagsHandler { return tags },
		costCategories: func() costCategoriesHandler { return costCategories },
		savingsPlans:   func() savingsPlanHandler { return savingsPlans },
		proForma:       func() proFormaHandler { return proForma },
		costExplorer:   func() costExplorerHandler { return costExplorer },
		anomalies:      func() anomalyHandler { return anomalies },
		exports:        func() exportsHandler { return exports },
		queryLab:       func() queryLabHandler { return queryLab },
		budgets:        func() budgetHandler { return budgets },
		bills:          func() billsHandler { return bills },
		payments:       func() paymentsHandler { return payments },
		scenarios:      func() scenarioHandler { return scenarios },
	}
}

// workspaceAppRouteHandlers builds request-time handlers against the currently open workspace.
func workspaceAppRouteHandlers(workspace *workspaceSession) appRouteHandlers {
	overview := newOverviewHandler()

	return appRouteHandlers{
		overview:       func() overviewHandler { return overview },
		organization:   func() organizationHandler { return newOrganizationHandler(workspaceDB(workspace)) },
		resourceLab:    func() resourceLabHandler { return newResourceLabHandler(workspaceDB(workspace)) },
		tags:           func() costAllocationTagsHandler { return newCostAllocationTagsHandler(workspaceDB(workspace)) },
		costCategories: func() costCategoriesHandler { return newCostCategoriesHandler(workspaceDB(workspace)) },
		savingsPlans:   func() savingsPlanHandler { return newSavingsPlanHandler(workspaceDB(workspace)) },
		proForma:       func() proFormaHandler { return newProFormaHandler(workspaceDB(workspace)) },
		costExplorer:   func() costExplorerHandler { return newCostExplorerHandler(workspaceDB(workspace)) },
		anomalies:      func() anomalyHandler { return newAnomalyHandler(workspaceDB(workspace)) },
		exports: func() exportsHandler {
			return newWorkspaceExportsHandler(workspaceDB(workspace), workspaceCurrentPath(workspace))
		},
		queryLab:  func() queryLabHandler { return newWorkspaceQueryLabHandler(workspaceCurrentPath(workspace)) },
		budgets:   func() budgetHandler { return newBudgetHandler(workspaceDB(workspace)) },
		bills:     func() billsHandler { return newBillsHandler(workspaceDB(workspace)) },
		payments:  func() paymentsHandler { return newPaymentsHandler(workspaceDB(workspace)) },
		scenarios: func() scenarioHandler { return newWorkspaceScenarioHandler(workspace) },
	}
}

// appRouteUsesActiveDB reports whether a registered app route needs the workspace lease.
func appRouteUsesActiveDB(path string) (bool, bool) {
	for _, route := range appRouteDefinitions {
		if route.matches(path) {
			return route.usesActiveDB, true
		}
	}
	return false, false
}

// probePath returns a concrete path that exercises the route pattern in tests.
func (route appRouteDefinition) probePath() string {
	if route.samplePath != "" {
		return route.samplePath
	}
	return route.pattern
}

// disallowedMethod returns one method that should trigger the route's Allow response.
func (route appRouteDefinition) disallowedMethod() string {
	for _, method := range route.allowed {
		if method == http.MethodPost {
			return http.MethodGet
		}
	}
	return http.MethodPost
}

// allowHeader returns the canonical Allow header expected from the route's method guard.
func (route appRouteDefinition) allowHeader() string {
	return strings.Join(route.allowed, ", ")
}

// matches applies the same subtree behavior as http.ServeMux slash patterns.
func (route appRouteDefinition) matches(path string) bool {
	if path == route.pattern {
		return true
	}
	return strings.HasSuffix(route.pattern, "/") && strings.HasPrefix(path, route.pattern)
}

// workspaceDB safely reads the current database from a possibly absent workspace session.
func workspaceDB(workspace *workspaceSession) *sql.DB {
	if workspace == nil {
		return nil
	}
	return workspace.DB()
}

// workspaceCurrentPath safely reads the current path from a possibly absent workspace session.
func workspaceCurrentPath(workspace *workspaceSession) string {
	if workspace == nil {
		return ""
	}
	return workspace.CurrentPath()
}
