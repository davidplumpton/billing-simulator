package app

import "net/http"

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
	FreshStartButton     uiSubmitButtonView
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
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
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
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderWorkspaces(w, http.StatusOK, "", flashFromQuery(r))
}

// handleOpenWorkspace opens an existing workspace or creates a fresh one at the submitted path.
func (h workspaceHandler) handleOpenWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
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

// handleStartFreshWorkspace creates a new clean workspace without requiring a typed path.
func (h workspaceHandler) handleStartFreshWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	workspacePath, err := h.workspace.NewFreshWorkspacePath()
	if err != nil {
		h.renderWorkspaces(w, http.StatusInternalServerError, err.Error(), "")
		return
	}
	if err := h.workspace.Open(r.Context(), workspacePath); err != nil {
		h.renderWorkspaces(w, http.StatusBadRequest, err.Error(), "")
		return
	}
	flash := "Started new experience in " + h.workspace.CurrentPath()
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
		FreshStartButton:     uiSubmitButton("Start New Experience"),
	}

	renderPage(w, status, pageLayoutOptions{
		Title:     "Workspaces - Billing Simulator",
		ActiveNav: "workspaces",
	}, workspacePageTemplate, data, "render workspace page")
}

// newWorkspaceMux routes requests through the current workspace session.
func newWorkspaceMux(workspace *workspaceSession) http.Handler {
	mux := http.NewServeMux()
	registerWorkspaceRoutes(mux, workspace)
	registerAppRoutes(mux, workspaceAppRouteHandlers(workspace))
	return workspaceLeaseMiddleware(workspace, mux)
}

// registerWorkspaceRoutes installs workspace lifecycle routes that do not belong to the direct app mux.
func registerWorkspaceRoutes(mux *http.ServeMux, workspace *workspaceSession) {
	workspaces := newWorkspaceHandler(workspace)
	mux.HandleFunc("/", workspaces.handleRoot)
	mux.HandleFunc("/workspaces", workspaces.handleWorkspaces)
	mux.HandleFunc("/workspaces/open", workspaces.handleOpenWorkspace)
	mux.HandleFunc("/workspaces/start", workspaces.handleStartFreshWorkspace)
}

// workspaceLeaseMiddleware keeps DB-backed request handlers from racing with workspace swaps.
func workspaceLeaseMiddleware(workspace *workspaceSession, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !workspaceRouteUsesActiveDB(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		release := workspace.BeginRequest()
		defer release()
		next.ServeHTTP(w, r)
	})
}

// workspaceRouteUsesActiveDB identifies routes whose handlers may keep the active DB for request work.
func workspaceRouteUsesActiveDB(path string) bool {
	switch path {
	case "/", "/workspaces", "/workspaces/open", "/workspaces/start":
		return false
	}
	if usesActiveDB, ok := appRouteUsesActiveDB(path); ok {
		return usesActiveDB
	}
	return true
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
			<div class="workspace-start">
				<h3>Start New Experience</h3>
				<p>Creates and opens a clean local workspace with the AnyCompany seed, no scenario runs, and no practice history.</p>
				<form method="post" action="/workspaces/start" class="workspace-start-form">
					{{template "ui.submit-button" .FreshStartButton}}
				</form>
			</div>
			<form method="post" action="/workspaces/open" class="workspace-form">
				{{template "ui.input-field" .WorkspacePathField}}
				{{template "ui.submit-button" .SubmitButton}}
			</form>
		</section>
`)
