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
	return template.Must(template.Must(template.New(name).Parse(sharedUITemplateText)).Parse(text))
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

var sharedUITemplateText = `
{{define "ui.notices"}}
	{{range .}}
		{{if .Message}}<div class="notice {{.Kind}}">{{.Message}}</div>{{end}}
	{{end}}
{{end}}

{{define "ui.empty-state"}}
	<section class="empty">
		<h2>{{.Title}}</h2>
		<p>{{.Message}}</p>
		{{if .HasAction}}<a class="button-link" href="{{.Action.Href}}">{{.Action.Label}}</a>{{end}}
	</section>
{{end}}

{{define "ui.action-bar"}}
	{{if .Actions}}
		<div class="page-actions">
			{{range .Actions}}<a class="button-link" href="{{.Href}}">{{.Label}}</a>{{end}}
		</div>
	{{end}}
{{end}}

{{define "ui.input-field"}}
	<label class="form-row{{if .Class}} {{.Class}}{{end}}">{{.Label}}
		<input{{if .Type}} type="{{.Type}}"{{end}} name="{{.Name}}" value="{{.Value}}"{{if .InputMode}} inputmode="{{.InputMode}}"{{end}}{{if .Required}} required{{end}}>
	</label>
{{end}}

{{define "ui.select-field"}}
	<label class="form-row{{if .Class}} {{.Class}}{{end}}">{{.Label}}
		<select name="{{.Name}}"{{if .Required}} required{{end}}>
			{{range .Options}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
		</select>
	</label>
{{end}}

{{define "ui.submit-button"}}
	<button type="submit">{{.Label}}</button>
{{end}}

{{define "ui.dense-table-head"}}
	<thead>
		<tr>
			{{range .Headers}}<th>{{.}}</th>{{end}}
		</tr>
	</thead>
{{end}}

{{define "ui.dense-table-empty-row"}}
	<tr><td colspan="{{.ColumnCount}}" class="empty-cell">{{.EmptyMessage}}</td></tr>
{{end}}
`
