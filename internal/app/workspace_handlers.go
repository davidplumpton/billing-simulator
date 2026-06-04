package app

import (
	"bytes"
	"fmt"
	"html/template"
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
	}

	var body bytes.Buffer
	if err := workspacePageTemplate.Execute(&body, data); err != nil {
		http.Error(w, "render workspace page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}

// newWorkspaceMux routes requests through the current workspace session.
func newWorkspaceMux(workspace *workspaceSession) http.Handler {
	workspaces := newWorkspaceHandler(workspace)
	mux := http.NewServeMux()
	mux.HandleFunc("/", workspaces.handleRoot)
	mux.HandleFunc("/workspaces", workspaces.handleWorkspaces)
	mux.HandleFunc("/workspaces/open", workspaces.handleOpenWorkspace)
	mux.HandleFunc("/assets/app.css", resourceRoute(workspace, func(h resourceLabHandler, w http.ResponseWriter, r *http.Request) {
		h.handleStylesheet(w, r)
	}))
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
	mux.HandleFunc("/bills", func(w http.ResponseWriter, r *http.Request) {
		newBillsHandler(workspace.DB()).handleBills(w, r)
	})
	mux.HandleFunc("/invoices/", func(w http.ResponseWriter, r *http.Request) {
		newBillsHandler(workspace.DB()).handleInvoice(w, r)
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

var workspacePageTemplate = template.Must(template.New("workspace-page").Parse(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Workspaces - AWS Billing Simulator</title>
	<link rel="stylesheet" href="/assets/app.css">
</head>
<body>
	<header class="topbar">
		<div class="brand">AWS Billing Simulator</div>
		<nav aria-label="Primary">
			<a class="active" href="/workspaces">Workspaces</a>
			<a href="/resources">Resources</a>
			<span>Tags</span>
			<span>Cost Explorer</span>
			<a href="/bills">Bills</a>
			<span>Scenarios</span>
		</nav>
	</header>

	<main class="page">
		<div class="page-heading">
			<div>
				<h1>Workspaces</h1>
			</div>
		</div>

		{{if .Flash}}<div class="notice success">{{.Flash}}</div>{{end}}
		{{if .Error}}<div class="notice error">{{.Error}}</div>{{end}}

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
				<label>Workspace Directory
					<input name="workspace_path" value="{{.SuggestedPath}}" required>
				</label>
				<button type="submit">Open or Create Workspace</button>
			</form>
		</section>
	</main>
</body>
</html>
`))
