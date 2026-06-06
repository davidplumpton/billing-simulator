package app

import (
	"fmt"
	"net/http"
)

type workspaceHandler struct {
	workspace *workspaceSession
}

type workspacePageData struct {
	WorkspaceReady       bool
	CurrentWorkspacePath string
	LastWorkspacePath    string
	SuggestedPath        string
	Flash                string
	Error                string
	Notices              []uiNoticeView
	WorkspacePathField   uiInputFieldView
	SubmitButton         uiSubmitButtonView
}

// newWorkspaceHandler builds the server-rendered workspace lifecycle page.
func newWorkspaceHandler(workspace *workspaceSession) workspaceHandler {
	return workspaceHandler{workspace: workspace}
}

// handleRoot sends learners to the active lab or the workspace selector.
func (h workspaceHandler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if h.workspace.DB() == nil {
		http.Redirect(w, r, "/workspaces", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/resources", http.StatusSeeOther)
}

// handleWorkspaces renders the create/open workspace form.
func (h workspaceHandler) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderWorkspaces(w, http.StatusOK, "", flashFromQuery(r))
}

// handleOpenWorkspace opens an existing workspace or creates a fresh one at the submitted path.
func (h workspaceHandler) handleOpenWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderWorkspaces(w, http.StatusBadRequest, "parse workspace form: "+err.Error(), "")
		return
	}
	workspacePath := r.PostForm.Get("workspace_path")
	if err := h.workspace.Open(r.Context(), workspacePath); err != nil {
		h.renderWorkspaces(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := "Opened workspace " + h.workspace.CurrentPath()
	http.Redirect(w, r, "/resources?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// renderWorkspaces renders the workspace lifecycle page with current session state.
func (h workspaceHandler) renderWorkspaces(w http.ResponseWriter, status int, errorMessage, flashMessage string) {
	currentPath := h.workspace.CurrentPath()
	lastPath := h.workspace.LastPath()
	suggestedPath := currentPath
	if suggestedPath == "" {
		suggestedPath = lastPath
	}
	data := workspacePageData{
		WorkspaceReady:       h.workspace.DB() != nil,
		CurrentWorkspacePath: currentPath,
		LastWorkspacePath:    lastPath,
		SuggestedPath:        suggestedPath,
		Flash:                flashMessage,
		Error:                errorMessage,
		Notices:              uiNotices(flashMessage, errorMessage),
		WorkspacePathField:   uiInputField("Workspace Directory", "workspace_path", suggestedPath, true),
		SubmitButton:         uiSubmitButton("Open or Create Workspace"),
	}

	renderPage(w, status, pageLayoutOptions{
		Title:     "Workspaces - AWS Billing Simulator",
		ActiveNav: "workspaces",
	}, workspacePageTemplate, data, "render workspace page")
}

// newWorkspaceMux routes requests through the current workspace session.
func newWorkspaceMux(workspace *workspaceSession) http.Handler {
	workspaces := newWorkspaceHandler(workspace)
	mux := http.NewServeMux()
	mux.HandleFunc("/", workspaces.handleRoot)
	mux.HandleFunc("/workspaces", workspaces.handleWorkspaces)
	mux.HandleFunc("/workspaces/open", workspaces.handleOpenWorkspace)
	mux.HandleFunc("/assets/app.css", serveAppStylesheet)
	mux.HandleFunc("/assets/app.js", serveAppScript)
	mux.HandleFunc("/organization", func(w http.ResponseWriter, r *http.Request) {
		newOrganizationHandler(workspace.DB()).handleOrganization(w, r)
	})
	mux.HandleFunc("/organization/accounts/create", func(w http.ResponseWriter, r *http.Request) {
		newOrganizationHandler(workspace.DB()).handleCreateAccount(w, r)
	})
	mux.HandleFunc("/organization/accounts/move", func(w http.ResponseWriter, r *http.Request) {
		newOrganizationHandler(workspace.DB()).handleMoveAccount(w, r)
	})
	mux.HandleFunc("/organization/accounts/suspend", func(w http.ResponseWriter, r *http.Request) {
		newOrganizationHandler(workspace.DB()).handleSuspendAccount(w, r)
	})
	mux.HandleFunc("/organization/accounts/close", func(w http.ResponseWriter, r *http.Request) {
		newOrganizationHandler(workspace.DB()).handleCloseAccount(w, r)
	})
	mux.HandleFunc("/resources", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleResources(w, r)
	}))
	mux.HandleFunc("/resources/create", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleCreateResource(w, r)
	}))
	mux.HandleFunc("/resources/tags", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleAddTag(w, r)
	}))
	mux.HandleFunc("/resources/usage", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleRecordUsage(w, r)
	}))
	mux.HandleFunc("/resources/generate", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleGenerateUsage(w, r)
	}))
	mux.HandleFunc("/resources/billing-pipeline", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleRunBillingPipeline(w, r)
	}))
	mux.HandleFunc("/resources/daily-metering", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleRunDailyMeteringJob(w, r)
	}))
	mux.HandleFunc("/resources/month-close", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleRunMonthEndClose(w, r)
	}))
	mux.HandleFunc("/clock/advance", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleAdvanceClock(w, r)
	}))
	mux.HandleFunc("/tags", func(w http.ResponseWriter, r *http.Request) {
		newCostAllocationTagsHandler(workspace.DB()).handleTags(w, r)
	})
	mux.HandleFunc("/tags/refresh", func(w http.ResponseWriter, r *http.Request) {
		newCostAllocationTagsHandler(workspace.DB()).handleRefreshDiscovery(w, r)
	})
	mux.HandleFunc("/tags/activate", func(w http.ResponseWriter, r *http.Request) {
		newCostAllocationTagsHandler(workspace.DB()).handleActivateTag(w, r)
	})
	mux.HandleFunc("/tags/deactivate", func(w http.ResponseWriter, r *http.Request) {
		newCostAllocationTagsHandler(workspace.DB()).handleDeactivateTag(w, r)
	})
	mux.HandleFunc("/cost-categories", func(w http.ResponseWriter, r *http.Request) {
		newCostCategoriesHandler(workspace.DB()).handleCostCategories(w, r)
	})
	mux.HandleFunc("/cost-categories/categories/create", func(w http.ResponseWriter, r *http.Request) {
		newCostCategoriesHandler(workspace.DB()).handleCreateCostCategory(w, r)
	})
	mux.HandleFunc("/cost-categories/rules/create", func(w http.ResponseWriter, r *http.Request) {
		newCostCategoriesHandler(workspace.DB()).handleCreateCostCategoryRule(w, r)
	})
	mux.HandleFunc("/cost-categories/splits/create", func(w http.ResponseWriter, r *http.Request) {
		newCostCategoriesHandler(workspace.DB()).handleCreateCostCategorySplitRule(w, r)
	})
	mux.HandleFunc("/cost-explorer", func(w http.ResponseWriter, r *http.Request) {
		newCostExplorerHandler(workspace.DB()).handleCostExplorer(w, r)
	})
	mux.HandleFunc("/cost-explorer/results.csv", func(w http.ResponseWriter, r *http.Request) {
		newCostExplorerHandler(workspace.DB()).handleCostExplorerResultsCSV(w, r)
	})
	mux.HandleFunc("/cost-explorer/line-items", func(w http.ResponseWriter, r *http.Request) {
		newCostExplorerHandler(workspace.DB()).handleCostExplorerLineItems(w, r)
	})
	mux.HandleFunc("/cost-explorer/reports/save", func(w http.ResponseWriter, r *http.Request) {
		newCostExplorerHandler(workspace.DB()).handleSaveCostExplorerReport(w, r)
	})
	mux.HandleFunc("/cost-explorer/reports/run", func(w http.ResponseWriter, r *http.Request) {
		newCostExplorerHandler(workspace.DB()).handleRunCostExplorerReport(w, r)
	})
	mux.HandleFunc("/exports/cur.csv", func(w http.ResponseWriter, r *http.Request) {
		newExportsHandler(workspace.DB()).handleCURCSV(w, r)
	})
	mux.HandleFunc("/exports/reconciliation", func(w http.ResponseWriter, r *http.Request) {
		newExportsHandler(workspace.DB()).handleCURReconciliation(w, r)
	})
	mux.HandleFunc("/budgets", func(w http.ResponseWriter, r *http.Request) {
		newBudgetHandler(workspace.DB()).handleBudgets(w, r)
	})
	mux.HandleFunc("/budgets/create", func(w http.ResponseWriter, r *http.Request) {
		newBudgetHandler(workspace.DB()).handleCreateBudget(w, r)
	})
	mux.HandleFunc("/budgets/refresh", func(w http.ResponseWriter, r *http.Request) {
		newBudgetHandler(workspace.DB()).handleRefreshBudgets(w, r)
	})
	mux.HandleFunc("/bills", func(w http.ResponseWriter, r *http.Request) {
		newBillsHandler(workspace.DB()).handleBills(w, r)
	})
	mux.HandleFunc("/invoices", func(w http.ResponseWriter, r *http.Request) {
		newBillsHandler(workspace.DB()).handleInvoiceIndex(w, r)
	})
	mux.HandleFunc("/invoices/", func(w http.ResponseWriter, r *http.Request) {
		newBillsHandler(workspace.DB()).handleInvoice(w, r)
	})
	mux.HandleFunc("/payments", func(w http.ResponseWriter, r *http.Request) {
		newPaymentsHandler(workspace.DB()).handlePayments(w, r)
	})
	mux.HandleFunc("/payments/action", func(w http.ResponseWriter, r *http.Request) {
		newPaymentsHandler(workspace.DB()).handlePaymentAction(w, r)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	return mux
}

// resourceRoute adapts resource handlers to the database currently open in the workspace session.
func resourceRoute(workspace *workspaceSession, handle func(resourceLabHandler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle(newResourceLabHandler(workspace.DB()), w, r)
	}
}

var workspacePageTemplate = newPageTemplate("workspace-page", `<div class="page-heading">
			<div>
				<h1>Workspaces</h1>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		<section class="panel workspace-panel">
			<h2>Workspace</h2>
			{{if .WorkspaceReady}}
				<div class="detail-list">
					<span>Open</span>
					<strong>{{.CurrentWorkspacePath}}</strong>
				</div>
			{{else}}
				<div class="detail-list">
					<span>Status</span>
					<strong>No workspace open</strong>
				</div>
			{{end}}
			{{if .LastWorkspacePath}}
				<div class="detail-list">
					<span>Last Used</span>
					<strong>{{.LastWorkspacePath}}</strong>
				</div>
			{{end}}
			<form method="post" action="/workspaces/open" class="workspace-form">
				{{template "ui.input-field" .WorkspacePathField}}
				{{template "ui.submit-button" .SubmitButton}}
			</form>
		</section>
`)
