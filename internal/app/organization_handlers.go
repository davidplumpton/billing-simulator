package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type organizationHandler struct {
	db            *sql.DB
	organizations persistence.OrganizationRepository
}

type organizationPageData struct {
	WorkspaceReady      bool
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Organization        organizationHeaderView
	Summary             organizationSummaryView
	Tree                []organizationTreeItemView
	Accounts            []organizationAccountView
	Tables              organizationTablesView
}

type organizationHeaderView struct {
	Name                string
	TemplateKey         string
	OrganizationID      string
	ManagementAccountID string
	CreatedAt           string
}

type organizationSummaryView struct {
	RootCount      int
	UnitCount      int
	AccountCount   int
	ActiveCount    int
	SuspendedCount int
	ClosedCount    int
}

type organizationTreeItemView struct {
	KindLabel      string
	KindClass      string
	DepthClass     string
	Name           string
	ID             string
	Detail         string
	Status         string
	StatusClass    string
	ResourcePath   string
	BillsPath      string
	IsAccount      bool
	HasBillingLink bool
}

type organizationAccountView struct {
	Name                  string
	AccountID             string
	Email                 string
	OUPath                string
	AccountType           string
	Status                string
	StatusClass           string
	JoinedAt              string
	LeftAt                string
	PaymentResponsibility string
	PayerAccountID        string
	BillingVisibilityRole string
	IsManagementAccount   bool
	ResourcePath          string
	BillsPath             string
}

type organizationTablesView struct {
	Accounts uiTableView
}

type organizationTreeChild struct {
	kind      string
	sortOrder int
	name      string
	unit      persistence.OrganizationUnit
	account   persistence.OrganizationAccount
}

// newOrganizationHandler builds the repositories needed for the organization page.
func newOrganizationHandler(db *sql.DB) organizationHandler {
	return organizationHandler{
		db:            db,
		organizations: persistence.NewOrganizationRepository(db),
	}
}

// handleOrganization serves the read-only organization hierarchy and account detail view.
func (h organizationHandler) handleOrganization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	h.renderOrganization(w, r, http.StatusOK, "")
}

// renderOrganization loads page data and wraps it in the shared browser layout.
func (h organizationHandler) renderOrganization(w http.ResponseWriter, r *http.Request, status int, errorMessage string) {
	data := organizationPageData{
		WorkspaceReady:      h.db != nil,
		Error:               errorMessage,
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
		Tables:              organizationTables(),
	}
	if h.db != nil {
		if err := h.loadOrganizationPageData(r.Context(), &data); err != nil {
			status = http.StatusInternalServerError
			data.Error = err.Error()
		}
	}
	data.Notices = uiNotices("", data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Organization - AWS Billing Simulator",
		ActiveNav: "organization",
	}, organizationPageTemplate, data, "render organization page")
}

// loadOrganizationPageData assembles the seeded organization into tree and account views.
func (h organizationHandler) loadOrganizationPageData(ctx context.Context, data *organizationPageData) error {
	organization, err := h.organizations.GetOrganizationByTemplate(ctx, persistence.AnyCompanyRetailTemplateKey)
	if err != nil {
		return err
	}
	roots, err := h.organizations.ListRoots(ctx, organization.ID)
	if err != nil {
		return err
	}
	units, err := h.organizations.ListUnits(ctx, organization.ID)
	if err != nil {
		return err
	}
	accounts, err := h.organizations.ListAccounts(ctx, organization.ID)
	if err != nil {
		return err
	}

	data.Organization = organizationHeaderView{
		Name:                organization.Name,
		TemplateKey:         organization.TemplateKey,
		OrganizationID:      organization.ID,
		ManagementAccountID: organization.ManagementAccountID,
		CreatedAt:           organization.CreatedAt,
	}
	data.Summary = organizationSummaryFromRows(roots, units, accounts)
	data.Tree = organizationTreeItems(roots, units, accounts)
	data.Accounts = make([]organizationAccountView, 0, len(accounts))
	for _, account := range accounts {
		data.Accounts = append(data.Accounts, organizationAccountViewFromAccount(account))
	}
	return nil
}

// organizationTables defines the shared dense-table metadata for account detail rows.
func organizationTables() organizationTablesView {
	return organizationTablesView{
		Accounts: uiTable(uiTableHeaders("Account", "OU", "Status", "Payer", "Billing Role", "Links"), "No accounts"),
	}
}

// organizationSummaryFromRows counts root, OU, and account lifecycle states for top cards.
func organizationSummaryFromRows(roots []persistence.OrganizationRoot, units []persistence.OrganizationUnit, accounts []persistence.OrganizationAccount) organizationSummaryView {
	summary := organizationSummaryView{
		RootCount:    len(roots),
		AccountCount: len(accounts),
	}
	rootIDs := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		rootIDs[root.ID] = struct{}{}
	}
	for _, unit := range units {
		if _, isRoot := rootIDs[unit.ID]; !isRoot {
			summary.UnitCount++
		}
	}
	for _, account := range accounts {
		switch account.Status {
		case persistence.AccountStatusActive:
			summary.ActiveCount++
		case persistence.AccountStatusSuspended:
			summary.SuspendedCount++
		case persistence.AccountStatusClosed:
			summary.ClosedCount++
		}
	}
	return summary
}

// organizationTreeItems flattens root, OU, and account rows into a stable indented tree.
func organizationTreeItems(roots []persistence.OrganizationRoot, units []persistence.OrganizationUnit, accounts []persistence.OrganizationAccount) []organizationTreeItemView {
	unitsByParent := make(map[string][]persistence.OrganizationUnit)
	for _, unit := range units {
		if unit.ParentUnitID != "" {
			unitsByParent[unit.ParentUnitID] = append(unitsByParent[unit.ParentUnitID], unit)
		}
	}
	accountsByParent := make(map[string][]persistence.OrganizationAccount)
	for _, account := range accounts {
		accountsByParent[account.ParentUnitID] = append(accountsByParent[account.ParentUnitID], account)
	}

	items := make([]organizationTreeItemView, 0, len(units)+len(accounts))
	for _, root := range roots {
		items = append(items, organizationTreeItemView{
			KindLabel:  "Root",
			KindClass:  "org-kind-root",
			DepthClass: "depth-0",
			Name:       root.Name,
			ID:         root.ID,
			Detail:     root.Path,
		})
		items = appendOrganizationTreeChildren(items, root.ID, 1, unitsByParent, accountsByParent)
	}
	return items
}

// appendOrganizationTreeChildren appends children sorted by fixture sort order and name.
func appendOrganizationTreeChildren(items []organizationTreeItemView, parentUnitID string, depth int, unitsByParent map[string][]persistence.OrganizationUnit, accountsByParent map[string][]persistence.OrganizationAccount) []organizationTreeItemView {
	children := make([]organizationTreeChild, 0, len(unitsByParent[parentUnitID])+len(accountsByParent[parentUnitID]))
	for _, unit := range unitsByParent[parentUnitID] {
		children = append(children, organizationTreeChild{
			kind:      "unit",
			sortOrder: unit.SortOrder,
			name:      unit.Name,
			unit:      unit,
		})
	}
	for _, account := range accountsByParent[parentUnitID] {
		children = append(children, organizationTreeChild{
			kind:      "account",
			sortOrder: account.SortOrder,
			name:      account.Name,
			account:   account,
		})
	}
	sort.SliceStable(children, func(left, right int) bool {
		if children[left].sortOrder != children[right].sortOrder {
			return children[left].sortOrder < children[right].sortOrder
		}
		if children[left].kind != children[right].kind {
			return children[left].kind < children[right].kind
		}
		return children[left].name < children[right].name
	})

	for _, child := range children {
		if child.kind == "account" {
			account := organizationAccountViewFromAccount(child.account)
			items = append(items, organizationTreeItemView{
				KindLabel:      "Account",
				KindClass:      "org-kind-account",
				DepthClass:     organizationDepthClass(depth),
				Name:           account.Name,
				ID:             account.AccountID,
				Detail:         account.AccountType,
				Status:         account.Status,
				StatusClass:    account.StatusClass,
				ResourcePath:   account.ResourcePath,
				BillsPath:      account.BillsPath,
				IsAccount:      true,
				HasBillingLink: account.BillsPath != "",
			})
			continue
		}
		items = append(items, organizationTreeItemView{
			KindLabel:  "OU",
			KindClass:  "org-kind-ou",
			DepthClass: organizationDepthClass(depth),
			Name:       child.unit.Name,
			ID:         child.unit.ID,
			Detail:     child.unit.Path,
		})
		items = appendOrganizationTreeChildren(items, child.unit.ID, depth+1, unitsByParent, accountsByParent)
	}
	return items
}

// organizationAccountViewFromAccount prepares account lifecycle, billing, and page links.
func organizationAccountViewFromAccount(account persistence.OrganizationAccount) organizationAccountView {
	accountType := "Member"
	if account.IsManagementAccount {
		accountType = "Management"
	}
	status := titleLabel(string(account.Status))
	return organizationAccountView{
		Name:                  account.Name,
		AccountID:             account.ID,
		Email:                 account.Email,
		OUPath:                account.OUPath,
		AccountType:           accountType,
		Status:                status,
		StatusClass:           "status-" + string(account.Status),
		JoinedAt:              account.JoinedAt,
		LeftAt:                account.LeftAt,
		PaymentResponsibility: titleLabel(account.PaymentResponsibility),
		PayerAccountID:        account.PayerAccountID,
		BillingVisibilityRole: titleLabel(account.BillingVisibilityRole),
		IsManagementAccount:   account.IsManagementAccount,
		ResourcePath:          organizationResourcePath(account.ID),
		BillsPath:             organizationBillsPath(account),
	}
}

// organizationDepthClass limits tree indentation classes to the levels used by the UI.
func organizationDepthClass(depth int) string {
	if depth < 0 {
		depth = 0
	}
	if depth > 4 {
		depth = 4
	}
	return "depth-" + string(rune('0'+depth))
}

// organizationResourcePath builds the account-scoped resource link for future filtering.
func organizationResourcePath(accountID string) string {
	query := url.Values{}
	query.Set("account_id", accountID)
	return "/resources?" + query.Encode()
}

// organizationBillsPath builds the billing view link scoped to payer and usage account.
func organizationBillsPath(account persistence.OrganizationAccount) string {
	query := url.Values{}
	query.Set("payer_account_id", account.PayerAccountID)
	if !account.IsManagementAccount {
		query.Set("usage_account_id", account.ID)
	}
	return "/bills?" + query.Encode()
}

// titleLabel converts stored enum tokens into compact human-readable labels.
func titleLabel(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	words := strings.Fields(value)
	for idx, word := range words {
		if word == "" {
			continue
		}
		words[idx] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

var organizationPageTemplate = newPageTemplate("organization-page", `<div class="page-heading">
			<div>
				<h1>Organization</h1>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="organization-hero">
				<div>
					<h2>{{.Organization.Name}}</h2>
					<strong>{{.Organization.ManagementAccountID}}</strong>
					<small>Management account</small>
				</div>
				<div class="organization-meta">
					<div>
						<span>Template</span>
						<strong>{{.Organization.TemplateKey}}</strong>
					</div>
					<div>
						<span>Organization ID</span>
						<strong>{{.Organization.OrganizationID}}</strong>
					</div>
					<div>
						<span>Created</span>
						<strong>{{.Organization.CreatedAt}}</strong>
					</div>
				</div>
			</section>

			<section class="organization-summary-grid">
				<div class="state-card">
					<span>Roots</span>
					<strong>{{.Summary.RootCount}}</strong>
				</div>
				<div class="state-card">
					<span>OUs</span>
					<strong>{{.Summary.UnitCount}}</strong>
				</div>
				<div class="state-card">
					<span>Accounts</span>
					<strong>{{.Summary.AccountCount}}</strong>
				</div>
				<div class="state-card">
					<span>Suspended</span>
					<strong>{{.Summary.SuspendedCount}}</strong>
				</div>
			</section>

			<section class="organization-layout">
				<div class="panel organization-tree-panel">
					<h2>Hierarchy</h2>
					<div class="organization-tree" role="tree">
						{{range .Tree}}
							<div class="org-tree-row {{.DepthClass}} {{.KindClass}}" role="treeitem">
								<span class="org-tree-kind">{{.KindLabel}}</span>
								<div>
									<strong>{{.Name}}</strong>
									<small>{{.Detail}}{{if .ID}} - {{.ID}}{{end}}</small>
								</div>
								{{if .Status}}<span class="status {{.StatusClass}}">{{.Status}}</span>{{end}}
								{{if .HasBillingLink}}<a href="{{.BillsPath}}">Bills</a>{{end}}
							</div>
						{{end}}
					</div>
				</div>

				<div>
					<div class="section-heading">
						<h2>Account Detail</h2>
						<span>{{len .Accounts}} accounts</span>
					</div>
					<div class="account-panel-grid">
						{{range .Accounts}}
							<article class="account-panel">
								<div class="account-panel-header">
									<div>
										<h3>{{.Name}}</h3>
										<small>{{.AccountID}}</small>
									</div>
									<span class="status {{.StatusClass}}">{{.Status}}</span>
								</div>
								<div class="detail-list">
									<span>OU Path</span>
									<strong>{{.OUPath}}</strong>
								</div>
								<div class="detail-list">
									<span>Billing Role</span>
									<strong>{{.BillingVisibilityRole}}</strong>
								</div>
								<div class="detail-list">
									<span>Payer</span>
									<strong>{{.PayerAccountID}}</strong>
								</div>
								<div class="detail-list">
									<span>Email</span>
									<strong>{{.Email}}</strong>
								</div>
								<div class="account-actions">
									<a class="button-link" href="{{.ResourcePath}}">Resources</a>
									<a class="button-link" href="{{.BillsPath}}">Bills</a>
								</div>
							</article>
						{{end}}
					</div>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Account Directory</h2>
					<span>{{.Summary.ActiveCount}} active, {{.Summary.SuspendedCount}} suspended, {{.Summary.ClosedCount}} closed</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Accounts}}
						<tbody>
							{{range .Accounts}}
								<tr>
									<td><strong>{{.Name}}</strong><small>{{.AccountID}} - {{.AccountType}}</small></td>
									<td>{{.OUPath}}</td>
									<td><span class="status {{.StatusClass}}">{{.Status}}</span></td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.BillingVisibilityRole}}</td>
									<td><a href="{{.ResourcePath}}">Resources</a> - <a href="{{.BillsPath}}">Bills</a></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Accounts}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
