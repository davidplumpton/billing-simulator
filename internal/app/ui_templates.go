package app

import "html/template"

type uiNoticeView struct {
	Kind    string
	Message string
}

type uiActionLinkView struct {
	Label string
	Href  string
}

type uiActionBarView struct {
	Actions []uiActionLinkView
}

type uiEmptyStateView struct {
	Title     string
	Message   string
	Action    uiActionLinkView
	HasAction bool
}

type uiInputFieldView struct {
	Label     string
	Name      string
	Value     string
	Type      string
	InputMode string
	Class     string
	Required  bool
}

type uiSelectOptionView struct {
	Value    string
	Label    string
	Selected bool
}

type uiSelectFieldView struct {
	Label    string
	Name     string
	Class    string
	Required bool
	Options  []uiSelectOptionView
}

type uiSubmitButtonView struct {
	Label string
}

type uiTableView struct {
	Headers      []string
	ColumnCount  int
	EmptyMessage string
}

// newPageTemplate attaches shared UI partials before parsing one page template.
func newPageTemplate(name, text string) *template.Template {
	return template.Must(mustEmbeddedTemplateSet(name, "shared.html").Parse(text))
}

// uiNotices returns the ordered flash and validation messages for page chrome.
func uiNotices(flashMessage, errorMessage string) []uiNoticeView {
	notices := make([]uiNoticeView, 0, 2)
	if flashMessage != "" {
		notices = append(notices, uiNoticeView{Kind: "success", Message: flashMessage})
	}
	if errorMessage != "" {
		notices = append(notices, uiNoticeView{Kind: "error", Message: errorMessage})
	}
	return notices
}

// uiWorkspaceRequiredState returns the shared empty state shown before a workspace is open.
func uiWorkspaceRequiredState() uiEmptyStateView {
	return uiEmptyStateView{
		Title:     "Workspace Required",
		Message:   "No workspace is open.",
		Action:    uiActionLink("Open Workspace", "/workspaces"),
		HasAction: true,
	}
}

// uiActionLink creates one link for a shared page action bar or empty state.
func uiActionLink(label, href string) uiActionLinkView {
	return uiActionLinkView{Label: label, Href: href}
}

// uiActionBar groups links into the shared page-level action bar partial.
func uiActionBar(actions ...uiActionLinkView) uiActionBarView {
	return uiActionBarView{Actions: actions}
}

// uiInputField creates a reusable label and input row for server-rendered forms.
func uiInputField(label, name, value string, required bool) uiInputFieldView {
	return uiInputFieldView{
		Label:    label,
		Name:     name,
		Value:    value,
		Required: required,
	}
}

// uiSubmitButton returns the shared submit button data used by form partials.
func uiSubmitButton(label string) uiSubmitButtonView {
	return uiSubmitButtonView{Label: label}
}

// uiTable returns shared dense-table header and empty-row metadata.
func uiTable(headers []string, emptyMessage string) uiTableView {
	return uiTableView{
		Headers:      headers,
		ColumnCount:  len(headers),
		EmptyMessage: emptyMessage,
	}
}

// uiTableHeaders keeps table definitions compact at call sites.
func uiTableHeaders(headers ...string) []string {
	return headers
}
