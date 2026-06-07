package app

import (
	"bytes"
	"html/template"
	"net/http"
)

type pageLayoutOptions struct {
	Title     string
	ActiveNav string
	MainClass string
}

type pageLayoutData struct {
	Title     string
	MainClass string
	NavItems  []pageNavItem
	Body      template.HTML
}

type pageNavItem struct {
	Key    string
	Label  string
	Path   string
	Active bool
}

// renderPage wraps trusted page content in the shared browser document shell.
func renderPage(w http.ResponseWriter, status int, options pageLayoutOptions, content *template.Template, data any, renderContext string) {
	var body bytes.Buffer
	if err := content.Execute(&body, data); err != nil {
		http.Error(w, renderContext+": "+err.Error(), http.StatusInternalServerError)
		return
	}

	title := options.Title
	if title == "" {
		title = "AWS Billing Simulator"
	}
	layoutData := pageLayoutData{
		Title:     title,
		MainClass: options.MainClass,
		NavItems:  pageNavItems(options.ActiveNav),
		Body:      template.HTML(body.String()),
	}

	var page bytes.Buffer
	if err := pageLayoutTemplate.Execute(&page, layoutData); err != nil {
		http.Error(w, "render page layout: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = page.WriteTo(w)
}

// renderPageFragment writes one trusted template block for fetch-based page refreshes.
func renderPageFragment(w http.ResponseWriter, status int, content *template.Template, fragmentName string, data any, renderContext string) {
	var body bytes.Buffer
	if err := content.ExecuteTemplate(&body, fragmentName, data); err != nil {
		http.Error(w, renderContext+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = body.WriteTo(w)
}

// serveAppStylesheet serves the embedded no-build stylesheet shared by all pages.
func serveAppStylesheet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}

	stylesheet, err := appAssets.ReadFile("assets/app.css")
	if err != nil {
		http.Error(w, "read stylesheet: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(stylesheet)
	}
}

// serveAppScript serves the embedded vanilla progressive-enhancement script.
func serveAppScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}

	script, err := appAssets.ReadFile("assets/app.js")
	if err != nil {
		http.Error(w, "read script: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(script)
	}
}

// wantsPageFragment matches the named fragment requested by the browser enhancer.
func wantsPageFragment(r *http.Request, fragmentName string) bool {
	return r.Header.Get("X-AWS-Billing-Simulator-Fragment") == fragmentName
}

// pageNavItems centralizes the shared top navigation and active-page state.
func pageNavItems(active string) []pageNavItem {
	items := []pageNavItem{
		{Key: "workspaces", Label: "Workspaces", Path: "/workspaces"},
		{Key: "organization", Label: "Organization", Path: "/organization"},
		{Key: "resources", Label: "Resources", Path: "/resources"},
		{Key: "tags", Label: "Tags", Path: "/tags"},
		{Key: "cost-categories", Label: "Cost Categories", Path: "/cost-categories"},
		{Key: "cost-explorer", Label: "Cost Explorer", Path: "/cost-explorer"},
		{Key: "budgets", Label: "Budgets", Path: "/budgets"},
		{Key: "bills", Label: "Bills", Path: "/bills"},
		{Key: "exports", Label: "Exports", Path: "/exports"},
		{Key: "payments", Label: "Payments", Path: "/payments"},
		{Key: "scenarios", Label: "Scenarios", Path: "/scenarios"},
	}
	for idx := range items {
		items[idx].Active = items[idx].Key == active
	}
	return items
}

var pageLayoutTemplate = mustEmbeddedTemplate("page-layout", "layout.html")
