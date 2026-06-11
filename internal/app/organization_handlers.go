package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

const defaultOrganizationLifecycleEffectiveAt = "2026-02-01T00:00:00Z"

type organizationHandler struct {
	db            *sql.DB
	organizations persistence.OrganizationRepository
	clock         persistence.SimulatorClockRepository
}

type organizationPageData struct {
	WorkspaceReady      bool
	Flash               string
	Error               string
	Notices             []uiNoticeView
	WorkspaceEmptyState uiEmptyStateView
	Organization        organizationHeaderView
	Summary             organizationSummaryView
	ClockCurrentTime    string
	ClockBillingPeriod  string
	DefaultEffectiveAt  string
	SuggestedAccountID  string
	UnitOptions         []organizationSelectOptionView
	AccountOptions      []organizationSelectOptionView
	LifecycleEvents     []organizationLifecycleEventView
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
	Owner                 string
	CostCenter            string
	Product               string
	Environment           string
	Lifecycle             string
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

type organizationSelectOptionView struct {
	Value string
	Label string
}

type organizationLifecycleEventView struct {
	Account        string
	Event          string
	ParentMovement string
	StatusChange   string
	EffectiveAt    string
	Source         string
}

type organizationTablesView struct {
	Accounts        uiTableView
	LifecycleEvents uiTableView
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
		clock:         persistence.NewSimulatorClockRepository(db),
	}
}

// handleOrganization serves the read-only organization hierarchy and account detail view.
func (h organizationHandler) handleOrganization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	h.renderOrganization(w, r, http.StatusOK, "", flashFromQuery(r))
}

// handleCreateAccount creates a member account in the selected OU.
func (h organizationHandler) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderOrganization(w, r, http.StatusServiceUnavailable, "Open a workspace before creating accounts.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, "parse account form: "+err.Error(), "")
		return
	}
	effectiveAt, err := h.lifecycleEffectiveAtFromForm(r.Context(), r)
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.organizations.CreateAccount(r.Context(), persistence.AccountCreateRequest{
		ID:             r.PostForm.Get("account_id"),
		OrganizationID: r.PostForm.Get("organization_id"),
		ParentUnitID:   r.PostForm.Get("parent_unit_id"),
		Name:           r.PostForm.Get("account_name"),
		Email:          r.PostForm.Get("account_email"),
		EffectiveAt:    effectiveAt,
	})
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/organization?flash="+urlQueryEscape("Created account "+result.Account.Name), http.StatusSeeOther)
}

// handleMoveAccount moves a member account to another OU.
func (h organizationHandler) handleMoveAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderOrganization(w, r, http.StatusServiceUnavailable, "Open a workspace before moving accounts.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, "parse account move form: "+err.Error(), "")
		return
	}
	effectiveAt, err := h.lifecycleEffectiveAtFromForm(r.Context(), r)
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.organizations.MoveAccount(r.Context(), persistence.AccountMoveRequest{
		AccountID:    r.PostForm.Get("account_id"),
		ParentUnitID: r.PostForm.Get("parent_unit_id"),
		EffectiveAt:  effectiveAt,
	})
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/organization?flash="+urlQueryEscape("Moved "+result.Account.Name+" to "+result.Account.OUPath), http.StatusSeeOther)
}

// handleSuspendAccount suspends an active member account.
func (h organizationHandler) handleSuspendAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderOrganization(w, r, http.StatusServiceUnavailable, "Open a workspace before suspending accounts.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, "parse account suspension form: "+err.Error(), "")
		return
	}
	effectiveAt, err := h.lifecycleEffectiveAtFromForm(r.Context(), r)
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.organizations.SuspendAccount(r.Context(), persistence.AccountSuspendRequest{
		AccountID:   r.PostForm.Get("account_id"),
		EffectiveAt: effectiveAt,
	})
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/organization?flash="+urlQueryEscape("Suspended "+result.Account.Name), http.StatusSeeOther)
}

// handleCloseAccount closes a member account and stores the effective left_at time.
func (h organizationHandler) handleCloseAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		h.renderOrganization(w, r, http.StatusServiceUnavailable, "Open a workspace before closing accounts.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, "parse account close form: "+err.Error(), "")
		return
	}
	effectiveAt, err := h.lifecycleEffectiveAtFromForm(r.Context(), r)
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	result, err := h.organizations.CloseAccount(r.Context(), persistence.AccountCloseRequest{
		AccountID:   r.PostForm.Get("account_id"),
		EffectiveAt: effectiveAt,
	})
	if err != nil {
		h.renderOrganization(w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/organization?flash="+urlQueryEscape("Closed "+result.Account.Name), http.StatusSeeOther)
}

// renderOrganization loads page data and wraps it in the shared browser layout.
func (h organizationHandler) renderOrganization(w http.ResponseWriter, r *http.Request, status int, errorMessage, flashMessage string) {
	data := organizationPageData{
		WorkspaceReady:      h.db != nil,
		Flash:               flashMessage,
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
	data.Notices = uiNotices(data.Flash, data.Error)

	renderPage(w, status, pageLayoutOptions{
		Title:     "Organization - AWS Billing Simulator",
		ActiveNav: "organization",
	}, organizationPageTemplate, data, "render organization page")
}

// loadOrganizationPageData assembles the seeded organization into tree and account views.
func (h organizationHandler) loadOrganizationPageData(ctx context.Context, data *organizationPageData) error {
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return err
	}
	applyOrganizationClockToPageData(data, clock)

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
	events, err := h.organizations.ListAccountLifecycleEvents(ctx, organization.ID, 50)
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
	data.UnitOptions = organizationUnitOptions(units)
	data.AccountOptions = organizationAccountOptions(accounts)
	data.SuggestedAccountID = organizationSuggestedAccountID(accounts)
	data.Tree = organizationTreeItems(roots, units, accounts)
	data.Accounts = make([]organizationAccountView, 0, len(accounts))
	for _, account := range accounts {
		data.Accounts = append(data.Accounts, organizationAccountViewFromAccount(account))
	}
	data.LifecycleEvents = organizationLifecycleEventViews(events, accounts, units)
	return nil
}

// organizationTables defines the shared dense-table metadata for account detail rows.
func organizationTables() organizationTablesView {
	return organizationTablesView{
		Accounts:        uiTable(uiTableHeaders("Account", "OU", "Owner", "Product", "Status", "Payer", "Billing Role", "Links"), "No accounts"),
		LifecycleEvents: uiTable(uiTableHeaders("Account", "Event", "OU", "Status", "Effective", "Source"), "No lifecycle events"),
	}
}

func (h organizationHandler) lifecycleEffectiveAtFromForm(ctx context.Context, r *http.Request) (string, error) {
	defaultTime := defaultOrganizationLifecycleEffectiveAt
	if h.db != nil {
		clock, err := h.clock.Get(ctx)
		if err != nil {
			return "", err
		}
		defaultTime = clock.CurrentTime
	}
	return parseFormTimestamp(r.PostForm.Get("effective_at"), defaultTime)
}

func applyOrganizationClockToPageData(data *organizationPageData, clock persistence.SimulatorClock) {
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf("%s to %s (%d days)", clock.BillingPeriodStart, clock.BillingPeriodEnd, clock.BillingPeriodDays)
	parsed, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, defaultOrganizationLifecycleEffectiveAt)
		if err != nil {
			return
		}
	}
	data.DefaultEffectiveAt = parsed.UTC().Truncate(time.Minute).Format("2006-01-02T15:04")
}

func organizationUnitOptions(units []persistence.OrganizationUnit) []organizationSelectOptionView {
	options := make([]organizationSelectOptionView, 0, len(units))
	for _, unit := range units {
		options = append(options, organizationSelectOptionView{
			Value: unit.ID,
			Label: unit.Path,
		})
	}
	return options
}

func organizationAccountOptions(accounts []persistence.OrganizationAccount) []organizationSelectOptionView {
	options := make([]organizationSelectOptionView, 0, len(accounts))
	for _, account := range accounts {
		if account.IsManagementAccount || account.Status == persistence.AccountStatusClosed {
			continue
		}
		options = append(options, organizationSelectOptionView{
			Value: account.ID,
			Label: account.Name + " - " + account.ID + " - " + account.OUPath,
		})
	}
	return options
}

func organizationSuggestedAccountID(accounts []persistence.OrganizationAccount) string {
	used := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		used[account.ID] = struct{}{}
	}
	for offset := 1; offset < 1000; offset++ {
		accountID := fmt.Sprintf("%012d", 777788889000+offset)
		if _, exists := used[accountID]; !exists {
			return accountID
		}
	}
	return ""
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
		Owner:                 accountMetadataLabel(account.Owner),
		CostCenter:            accountMetadataLabel(account.CostCenter),
		Product:               accountMetadataLabel(account.Product),
		Environment:           accountMetadataLabel(account.Environment),
		Lifecycle:             accountMetadataLabel(account.Lifecycle),
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

func accountMetadataLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Not tagged"
	}
	return value
}

func organizationLifecycleEventViews(events []persistence.AccountLifecycleEvent, accounts []persistence.OrganizationAccount, units []persistence.OrganizationUnit) []organizationLifecycleEventView {
	accountLabels := make(map[string]string, len(accounts))
	for _, account := range accounts {
		accountLabels[account.ID] = account.Name + " - " + account.ID
	}
	unitLabels := make(map[string]string, len(units))
	for _, unit := range units {
		unitLabels[unit.ID] = unit.Path
	}

	views := make([]organizationLifecycleEventView, 0, len(events))
	for _, event := range events {
		accountLabel := event.AccountID
		if label, ok := accountLabels[event.AccountID]; ok {
			accountLabel = label
		}
		views = append(views, organizationLifecycleEventView{
			Account:        accountLabel,
			Event:          titleLabel(string(event.EventType)),
			ParentMovement: organizationLifecycleParentMovement(event, unitLabels),
			StatusChange:   organizationLifecycleStatusChange(event),
			EffectiveAt:    event.EffectiveAt,
			Source:         titleLabel(event.EventSource),
		})
	}
	return views
}

func organizationLifecycleParentMovement(event persistence.AccountLifecycleEvent, unitLabels map[string]string) string {
	previous := organizationUnitLabel(event.PreviousParentUnitID, unitLabels)
	next := organizationUnitLabel(event.NewParentUnitID, unitLabels)
	if previous == "" {
		return next
	}
	if next == "" || previous == next {
		return previous
	}
	return previous + " -> " + next
}

func organizationLifecycleStatusChange(event persistence.AccountLifecycleEvent) string {
	previous := titleLabel(string(event.PreviousStatus))
	next := titleLabel(string(event.NewStatus))
	if previous == "" {
		return next
	}
	if next == "" || previous == next {
		return previous
	}
	return previous + " -> " + next
}

func organizationUnitLabel(unitID string, unitLabels map[string]string) string {
	if unitID == "" {
		return ""
	}
	if label, ok := unitLabels[unitID]; ok {
		return label
	}
	return unitID
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

// organizationResourcePath builds the account-scoped resource filter link.
func organizationResourcePath(accountID string) string {
	query := url.Values{}
	query.Set("account_id", accountID)
	return "/resources?" + query.Encode()
}

// organizationBillsPath builds the billing view link scoped to payer and usage account.
func organizationBillsPath(account persistence.OrganizationAccount) string {
	query := url.Values{}
	query.Set("payer_account_id", account.PayerAccountID)
	if account.IsManagementAccount {
		query.Set("viewer_role", "management-account")
		query.Set("viewer_account_id", account.ID)
	} else {
		query.Set("usage_account_id", account.ID)
		query.Set("viewer_role", "member-account")
		query.Set("viewer_account_id", account.ID)
	}
	return "/bills?" + query.Encode()
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

			<section class="clock-strip">
				<div>
					<h2>Simulator Clock</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
			</section>

			<section class="form-grid">
				<form method="post" action="/organization/accounts/create" class="panel">
					<h2>Create Account</h2>
					<input type="hidden" name="organization_id" value="{{.Organization.OrganizationID}}">
					<div class="fields">
						<label>Account ID
							<input name="account_id" value="{{.SuggestedAccountID}}" inputmode="numeric" required>
						</label>
						<label>OU
							<select name="parent_unit_id" required>
								{{range .UnitOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Name
							<input name="account_name" value="Workload Expansion" required>
						</label>
						<label>Email
							<input name="account_email" value="workload-expansion@anycompany.example" required>
						</label>
						<label class="wide">Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Create Account</button>
				</form>

				<form method="post" action="/organization/accounts/move" class="panel compact">
					<h2>Move Account</h2>
					<div class="fields">
						<label>Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Target OU
							<select name="parent_unit_id" required>
								{{range .UnitOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Move Account</button>
				</form>

				<form method="post" action="/organization/accounts/suspend" class="panel compact">
					<h2>Suspend Account</h2>
					<div class="fields">
						<label>Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Suspend Account</button>
				</form>

				<form method="post" action="/organization/accounts/close" class="panel compact">
					<h2>Close Account</h2>
					<div class="fields">
						<label>Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Close Account</button>
				</form>
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
									<span>Owner</span>
									<strong>{{.Owner}}</strong>
								</div>
								<div class="detail-list">
									<span>Cost Center</span>
									<strong>{{.CostCenter}}</strong>
								</div>
								<div class="detail-list">
									<span>Product</span>
									<strong>{{.Product}}</strong>
								</div>
								<div class="detail-list">
									<span>Environment</span>
									<strong>{{.Environment}}</strong>
								</div>
								<div class="detail-list">
									<span>Lifecycle</span>
									<strong>{{.Lifecycle}}</strong>
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
									<td>{{.Owner}}<small>{{.CostCenter}}</small></td>
									<td><strong>{{.Product}}</strong><small>{{.Environment}} - {{.Lifecycle}}</small></td>
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

			<section>
				<div class="section-heading">
					<h2>Lifecycle History</h2>
					<span>{{len .LifecycleEvents}} events</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LifecycleEvents}}
						<tbody>
							{{range .LifecycleEvents}}
								<tr>
									<td><strong>{{.Account}}</strong></td>
									<td>{{.Event}}</td>
									<td>{{.ParentMovement}}</td>
									<td>{{.StatusChange}}</td>
									<td>{{.EffectiveAt}}</td>
									<td>{{.Source}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.LifecycleEvents}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
