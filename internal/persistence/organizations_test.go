package persistence

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestAnyCompanyRetailOrganizationFixtureSeeded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewOrganizationRepository(db)

	organization, err := repo.GetOrganizationByTemplate(ctx, AnyCompanyRetailTemplateKey)
	if err != nil {
		t.Fatalf("GetOrganizationByTemplate() error = %v", err)
	}
	if organization.ID != AnyCompanyRetailOrganizationID ||
		organization.Name != "AnyCompany Retail" ||
		organization.ManagementAccountID != AnyCompanyRetailManagementAccountID ||
		organization.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("organization = %+v, want AnyCompany Retail seed header", organization)
	}

	roots, err := repo.ListRoots(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListRoots() error = %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("root count = %d, want 1: %+v", len(roots), roots)
	}
	root := roots[0]
	if root.ID != "ou_anycompany_root" ||
		root.OrganizationID != organization.ID ||
		root.Name != "Root" ||
		root.Path != "Root" ||
		root.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("root = %+v, want AnyCompany root seeded from hierarchy", root)
	}

	units, err := repo.ListUnits(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListUnits() error = %v", err)
	}
	unitPaths := organizationUnitPathByID(units)
	wantUnitPaths := map[string]string{
		"ou_anycompany_root":           "Root",
		"ou_anycompany_security":       "Root/Security",
		"ou_anycompany_infrastructure": "Root/Infrastructure",
		"ou_anycompany_sandbox":        "Root/Sandbox",
		"ou_anycompany_workloads":      "Root/Workloads",
		"ou_anycompany_suspended":      "Root/Suspended",
	}
	if !reflect.DeepEqual(unitPaths, wantUnitPaths) {
		t.Fatalf("unit paths = %#v, want %#v", unitPaths, wantUnitPaths)
	}

	accounts, err := repo.ListAccounts(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if len(accounts) != 13 {
		t.Fatalf("account count = %d, want 13: %v", len(accounts), organizationAccountNames(accounts))
	}
	byName := organizationAccountsByName(accounts)

	assertSeedAccount(t, byName["Management"], "999988887777", "Root", accountTypeManagement, "active")
	assertSeedAccount(t, byName["Log Archive"], "000011112222", "Root/Security", accountTypeMember, "active")
	assertSeedAccount(t, byName["Audit"], "000011112223", "Root/Security", accountTypeMember, "active")
	assertSeedAccount(t, byName["Shared Networking"], "222233334444", "Root/Infrastructure", accountTypeMember, "active")
	assertSeedAccount(t, byName["Platform Services"], "222233334445", "Root/Infrastructure", accountTypeMember, "active")
	assertSeedAccount(t, byName["Developer Sandbox 1"], "333344445555", "Root/Sandbox", accountTypeMember, "active")
	assertSeedAccount(t, byName["Developer Sandbox 2"], "333344445556", "Root/Sandbox", accountTypeMember, "active")
	assertSeedAccount(t, byName["Storefront Dev"], "111122223332", "Root/Workloads", accountTypeMember, "active")
	assertSeedAccount(t, byName["Storefront Prod"], "111122223333", "Root/Workloads", accountTypeMember, "active")
	assertSeedAccount(t, byName["Payments Dev"], "444455556665", "Root/Workloads", accountTypeMember, "active")
	assertSeedAccount(t, byName["Payments Prod"], "444455556666", "Root/Workloads", accountTypeMember, "active")
	assertSeedAccount(t, byName["Analytics Prod"], "555566667777", "Root/Workloads", accountTypeMember, "active")
	assertSeedAccount(t, byName["Deprecated Prototype"], "666677778888", "Root/Suspended", accountTypeMember, "suspended")

	tags, err := repo.ListAccountTags(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListAccountTags() error = %v", err)
	}
	if len(tags) != 65 {
		t.Fatalf("active account tag count = %d, want 65", len(tags))
	}
	assertSeedAccountMetadata(t, byName["Management"], "finance-operations", "1000-corporate", "shared-services", "production", "active")
	assertSeedAccountMetadata(t, byName["Storefront Prod"], "storefront-team", "4100-storefront", "storefront", "production", "active")
	assertSeedAccountMetadata(t, byName["Payments Dev"], "payments-team", "4200-payments", "payments", "development", "active")
	assertSeedAccountMetadata(t, byName["Deprecated Prototype"], "innovation-lab", "9900-deprecated", "prototype", "retired", "deprecated")

	storefrontProd, err := repo.GetAccount(ctx, "111122223333")
	if err != nil {
		t.Fatalf("GetAccount(Storefront Prod) error = %v", err)
	}
	if storefrontProd.Name != "Storefront Prod" || storefrontProd.PayerAccountID != AnyCompanyRetailManagementAccountID {
		t.Fatalf("Storefront Prod = %+v, want member account paid by management", storefrontProd)
	}
	assertSeedAccountMetadata(t, storefrontProd, "storefront-team", "4100-storefront", "storefront", "production", "active")
}

func TestAnyCompanyRetailAccountReferencesMatchFixture(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Management":           "999988887777",
		"Management Account":   "999988887777",
		"Storefront Prod":      "111122223333",
		"shared-networking":    "222233334444",
		"Deprecated Prototype": "666677778888",
	}
	for name, wantID := range cases {
		accountID, ok := AnyCompanyRetailAccountIDForName(name)
		if !ok || accountID != wantID {
			t.Fatalf("AnyCompanyRetailAccountIDForName(%q) = %q, %v, want %q, true", name, accountID, ok, wantID)
		}
	}

	if accountID, ok := AnyCompanyRetailAccountIDForName("Unknown Account"); ok || accountID != "" {
		t.Fatalf("AnyCompanyRetailAccountIDForName(unknown) = %q, %v, want blank false", accountID, ok)
	}
}

func TestOrganizationRepositoryRejectsBlankInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewOrganizationRepository(openTestWorkspace(t))

	if _, err := repo.GetOrganizationByTemplate(ctx, " "); err == nil {
		t.Fatal("GetOrganizationByTemplate(blank) error = nil, want validation error")
	}
	if _, err := repo.ListRoots(ctx, " "); err == nil {
		t.Fatal("ListRoots(blank) error = nil, want validation error")
	}
	if _, err := repo.ListUnits(ctx, ""); err == nil {
		t.Fatal("ListUnits(blank) error = nil, want validation error")
	}
	if _, err := repo.ListAccounts(ctx, "\t"); err == nil {
		t.Fatal("ListAccounts(blank) error = nil, want validation error")
	}
	if _, err := repo.GetAccount(ctx, ""); err == nil {
		t.Fatal("GetAccount(blank) error = nil, want validation error")
	}
	if _, err := repo.ListAccountTags(ctx, " "); err == nil {
		t.Fatal("ListAccountTags(blank) error = nil, want validation error")
	}
}

func TestOrganizationRepositoryReportsMissingRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewOrganizationRepository(openTestWorkspace(t))

	_, err := repo.GetOrganizationByTemplate(ctx, "missing-template")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetOrganizationByTemplate(missing) error = %v, want sql.ErrNoRows", err)
	}
	_, err = repo.GetAccount(ctx, "000000000000")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetAccount(missing) error = %v, want sql.ErrNoRows", err)
	}
}

func TestOrganizationSchemaRejectsMismatchedManagementFlag(t *testing.T) {
	t.Parallel()

	db := openTestWorkspace(t)
	assertExecFails(t, db, `INSERT INTO accounts (
		id,
		organization_id,
		parent_unit_id,
		name,
		email,
		account_type,
		status,
		created_at,
		joined_at,
		payment_responsibility,
		payer_account_id,
		billing_visibility_role,
		sort_order,
		is_management_account
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"777788889999",
		AnyCompanyRetailOrganizationID,
		"ou_anycompany_sandbox",
		"Flag Mismatch",
		"flag-mismatch@anycompany.example",
		accountTypeMember,
		AccountStatusActive,
		"2026-01-01T00:00:00Z",
		"2026-01-01T00:00:00Z",
		"management_account",
		AnyCompanyRetailManagementAccountID,
		"member-account",
		130,
		1,
	)
}

func TestAccountLifecycleMigrationBackfillsSeedEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := NewOrganizationRepository(openTestWorkspace(t))

	events, err := repo.ListAccountLifecycleEvents(ctx, AnyCompanyRetailOrganizationID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() error = %v", err)
	}
	if len(events) != 13 {
		t.Fatalf("seed lifecycle event count = %d, want 13", len(events))
	}
	byAccount := accountLifecycleEventsByAccount(events)
	management := byAccount[AnyCompanyRetailManagementAccountID][0]
	if management.EventType != AccountLifecycleEventCreated ||
		management.NewParentUnitID != "ou_anycompany_root" ||
		management.NewStatus != AccountStatusActive ||
		management.EffectiveAt != "2026-01-01T00:00:00Z" ||
		management.EventSource != "system" {
		t.Fatalf("management lifecycle event = %+v, want seeded creation baseline", management)
	}
	deprecated := byAccount["666677778888"][0]
	if deprecated.EventType != AccountLifecycleEventCreated || deprecated.NewStatus != AccountStatusSuspended {
		t.Fatalf("deprecated account lifecycle event = %+v, want suspended baseline", deprecated)
	}
}

func TestOrganizationRepositoryAccountLifecycleOperationsRecordHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewOrganizationRepository(db)

	created, err := repo.CreateAccount(ctx, AccountCreateRequest{
		ID:             "777788889999",
		OrganizationID: AnyCompanyRetailOrganizationID,
		ParentUnitID:   "ou_anycompany_sandbox",
		Name:           "Partner Integration",
		Email:          "partner-integration@anycompany.example",
		EffectiveAt:    "2026-02-01T00:00:00+13:00",
	})
	if err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	if created.Account.Status != AccountStatusActive ||
		created.Account.OUPath != "Root/Sandbox" ||
		created.Account.PayerAccountID != AnyCompanyRetailManagementAccountID ||
		created.Account.IsManagementAccount {
		t.Fatalf("created account = %+v, want active member in Sandbox billed to management", created.Account)
	}
	if created.Event.EventType != AccountLifecycleEventCreated ||
		created.Event.NewParentUnitID != "ou_anycompany_sandbox" ||
		created.Event.NewStatus != AccountStatusActive ||
		created.Event.EffectiveAt != "2026-01-31T11:00:00Z" {
		t.Fatalf("created lifecycle event = %+v, want canonical UTC creation event", created.Event)
	}

	moved, err := repo.MoveAccount(ctx, AccountMoveRequest{
		AccountID:    "777788889999",
		ParentUnitID: "ou_anycompany_workloads",
		EffectiveAt:  "2026-02-05T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("MoveAccount() error = %v", err)
	}
	if moved.Account.OUPath != "Root/Workloads" {
		t.Fatalf("moved account OUPath = %q, want Root/Workloads", moved.Account.OUPath)
	}
	if moved.Event.EventType != AccountLifecycleEventMoved ||
		moved.Event.PreviousParentUnitID != "ou_anycompany_sandbox" ||
		moved.Event.NewParentUnitID != "ou_anycompany_workloads" ||
		moved.Event.PreviousStatus != AccountStatusActive ||
		moved.Event.NewStatus != AccountStatusActive {
		t.Fatalf("move lifecycle event = %+v, want OU transfer with active status", moved.Event)
	}

	suspended, err := repo.SuspendAccount(ctx, AccountSuspendRequest{
		AccountID:   "777788889999",
		EffectiveAt: "2026-02-10T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("SuspendAccount() error = %v", err)
	}
	if suspended.Account.Status != AccountStatusSuspended || suspended.Account.LeftAt != "" {
		t.Fatalf("suspended account = %+v, want suspended membership without left_at", suspended.Account)
	}
	if suspended.Event.EventType != AccountLifecycleEventSuspended ||
		suspended.Event.PreviousStatus != AccountStatusActive ||
		suspended.Event.NewStatus != AccountStatusSuspended {
		t.Fatalf("suspend lifecycle event = %+v, want active to suspended", suspended.Event)
	}

	closed, err := repo.CloseAccount(ctx, AccountCloseRequest{
		AccountID:   "777788889999",
		EffectiveAt: "2026-02-15T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CloseAccount() error = %v", err)
	}
	if closed.Account.Status != AccountStatusClosed || closed.Account.LeftAt != "2026-02-15T00:00:00Z" {
		t.Fatalf("closed account = %+v, want closed membership with left_at", closed.Account)
	}
	if closed.Event.EventType != AccountLifecycleEventClosed ||
		closed.Event.PreviousStatus != AccountStatusSuspended ||
		closed.Event.NewStatus != AccountStatusClosed {
		t.Fatalf("close lifecycle event = %+v, want suspended to closed", closed.Event)
	}

	events, err := repo.ListAccountLifecycleEvents(ctx, AnyCompanyRetailOrganizationID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() after operations error = %v", err)
	}
	accountEvents := accountLifecycleEventsByAccount(events)["777788889999"]
	if len(accountEvents) != 4 {
		t.Fatalf("new account lifecycle event count = %d, want 4: %+v", len(accountEvents), accountEvents)
	}
	if accountEvents[0].EventType != AccountLifecycleEventClosed ||
		accountEvents[1].EventType != AccountLifecycleEventSuspended ||
		accountEvents[2].EventType != AccountLifecycleEventMoved ||
		accountEvents[3].EventType != AccountLifecycleEventCreated {
		t.Fatalf("new account event order = %+v, want newest-first close/suspend/move/create", accountEvents)
	}
}

func TestOrganizationRepositoryResetTemplateRestoresAnyCompanySeed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewOrganizationRepository(db)

	if _, err := repo.CreateAccount(ctx, AccountCreateRequest{
		ID:             "777788889999",
		OrganizationID: AnyCompanyRetailOrganizationID,
		ParentUnitID:   "ou_anycompany_sandbox",
		Name:           "Partner Integration",
		Email:          "partner-integration@anycompany.example",
		EffectiveAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}
	if _, err := repo.MoveAccount(ctx, AccountMoveRequest{
		AccountID:    "111122223333",
		ParentUnitID: "ou_anycompany_sandbox",
		EffectiveAt:  "2026-02-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("MoveAccount(Storefront Prod) error = %v", err)
	}
	if _, err := repo.SuspendAccount(ctx, AccountSuspendRequest{
		AccountID:   "111122223333",
		EffectiveAt: "2026-02-03T00:00:00Z",
	}); err != nil {
		t.Fatalf("SuspendAccount(Storefront Prod) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE organizations SET name = ? WHERE id = ?`, "Changed Retail", AnyCompanyRetailOrganizationID); err != nil {
		t.Fatalf("update organization name: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE organization_units SET name = ?, path = ? WHERE id = ?`, "Changed Workloads", "Root/Changed Workloads", "ou_anycompany_workloads"); err != nil {
		t.Fatalf("update organization unit: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE account_tags SET tag_value = ? WHERE account_id = ? AND tag_key = ?`, "changed-owner", "111122223333", accountTagKeyOwner); err != nil {
		t.Fatalf("update account tag: %v", err)
	}
	if _, err := NewResourceUsageRepository(db).CreateResource(ctx, ResourceCreateRequest{
		ID:           "preserved-reset-resource",
		AccountID:    "777788889999",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonS3",
		ResourceType: "bucket",
		ResourceName: "scenario-reset-preserved-resource",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := NewSavedReportRepository(db).Create(ctx, SavedReportCreateRequest{
		ID:             "saved-reset-report",
		Name:           "Reset Safety",
		OwnerAccountID: "777788889999",
		OwnerRole:      "member-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings:      []SavedReportGrouping{{Type: "dimension", Key: "service"}},
		Metrics:        []string{"unblended_cost"},
		ChartType:      "table",
	}); err != nil {
		t.Fatalf("Create(saved report) error = %v", err)
	}

	reset, err := repo.ResetOrganizationTemplate(ctx, AnyCompanyRetailTemplateKey)
	if err != nil {
		t.Fatalf("ResetOrganizationTemplate() error = %v", err)
	}
	if reset.OrganizationID != AnyCompanyRetailOrganizationID ||
		reset.RootsReset != 1 ||
		reset.UnitsReset != 6 ||
		reset.AccountsReset != 13 ||
		reset.AccountTagsReset != 65 ||
		reset.LifecycleEventsReset != 13 {
		t.Fatalf("reset result = %+v, want AnyCompany seed row counts", reset)
	}

	organization, err := repo.GetOrganizationByTemplate(ctx, AnyCompanyRetailTemplateKey)
	if err != nil {
		t.Fatalf("GetOrganizationByTemplate() after reset error = %v", err)
	}
	if organization.Name != "AnyCompany Retail" || organization.ManagementAccountID != AnyCompanyRetailManagementAccountID {
		t.Fatalf("organization after reset = %+v, want AnyCompany seed header", organization)
	}
	units, err := repo.ListUnits(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListUnits() after reset error = %v", err)
	}
	if got := organizationUnitPathByID(units)["ou_anycompany_workloads"]; got != "Root/Workloads" {
		t.Fatalf("workloads OU path after reset = %q, want Root/Workloads", got)
	}
	accounts, err := repo.ListAccounts(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListAccounts() after reset error = %v", err)
	}
	byName := organizationAccountsByName(accounts)
	if _, ok := byName["Partner Integration"]; ok {
		t.Fatalf("reset retained ad hoc account: %v", organizationAccountNames(accounts))
	}
	assertSeedAccount(t, byName["Storefront Prod"], "111122223333", "Root/Workloads", accountTypeMember, "active")
	assertSeedAccountMetadata(t, byName["Storefront Prod"], "storefront-team", "4100-storefront", "storefront", "production", "active")

	events, err := repo.ListAccountLifecycleEvents(ctx, AnyCompanyRetailOrganizationID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() after reset error = %v", err)
	}
	if len(events) != 13 {
		t.Fatalf("lifecycle event count after reset = %d, want 13", len(events))
	}
	if _, ok := accountLifecycleEventsByAccount(events)["777788889999"]; ok {
		t.Fatalf("reset retained ad hoc account lifecycle event: %+v", events)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM resources WHERE id = ?`, "preserved-reset-resource"); got != 1 {
		t.Fatalf("preserved resource count = %d, want 1", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM saved_reports WHERE id = ?`, "saved-reset-report"); got != 1 {
		t.Fatalf("preserved saved report count = %d, want 1", got)
	}
}

func TestOrganizationRepositoryAccountLifecycleValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewOrganizationRepository(db)

	if _, err := repo.MoveAccount(ctx, AccountMoveRequest{
		AccountID:    AnyCompanyRetailManagementAccountID,
		ParentUnitID: "ou_anycompany_sandbox",
		EffectiveAt:  "2026-02-01T00:00:00Z",
	}); err == nil || !strings.Contains(err.Error(), "management account") {
		t.Fatalf("MoveAccount(management) error = %v, want management guard", err)
	}
	if _, err := repo.SuspendAccount(ctx, AccountSuspendRequest{
		AccountID:   "666677778888",
		EffectiveAt: "2026-02-01T00:00:00Z",
	}); err == nil || !strings.Contains(err.Error(), "must be active") {
		t.Fatalf("SuspendAccount(already suspended) error = %v, want active-state guard", err)
	}
	if _, err := repo.CloseAccount(ctx, AccountCloseRequest{
		AccountID:   "111122223333",
		EffectiveAt: "2026-01-01T00:00:00Z",
	}); err == nil || !strings.Contains(err.Error(), "after joined_at") {
		t.Fatalf("CloseAccount(joined_at boundary) error = %v, want left_at ordering guard", err)
	}

	if _, err := repo.CreateAccount(ctx, AccountCreateRequest{
		ID:             "777788889990",
		OrganizationID: AnyCompanyRetailOrganizationID,
		ParentUnitID:   "ou_anycompany_sandbox",
		Name:           "Late Ordering",
		Email:          "late-ordering@anycompany.example",
		EffectiveAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateAccount(ordering fixture) error = %v", err)
	}
	if _, err := repo.MoveAccount(ctx, AccountMoveRequest{
		AccountID:    "777788889990",
		ParentUnitID: "ou_anycompany_workloads",
		EffectiveAt:  "2026-02-10T00:00:00Z",
	}); err != nil {
		t.Fatalf("MoveAccount(ordering fixture) error = %v", err)
	}
	if _, err := repo.SuspendAccount(ctx, AccountSuspendRequest{
		AccountID:   "777788889990",
		EffectiveAt: "2026-02-09T00:00:00Z",
	}); err == nil || !strings.Contains(err.Error(), "latest event") {
		t.Fatalf("SuspendAccount(before latest event) error = %v, want lifecycle ordering guard", err)
	}
	if _, err := repo.MoveAccount(ctx, AccountMoveRequest{
		AccountID:    "777788889990",
		ParentUnitID: "ou_anycompany_workloads",
		EffectiveAt:  "2026-02-11T00:00:00Z",
	}); err == nil || !strings.Contains(err.Error(), "already belongs") {
		t.Fatalf("MoveAccount(same OU) error = %v, want same-OU guard", err)
	}
}

func assertSeedAccount(t *testing.T, account OrganizationAccount, wantID, wantUnitPath, wantType, wantStatus string) {
	t.Helper()

	if account.ID != wantID {
		t.Fatalf("%s account ID = %q, want %q", account.Name, account.ID, wantID)
	}
	if account.OrganizationID != AnyCompanyRetailOrganizationID {
		t.Fatalf("%s organization ID = %q, want %q", account.Name, account.OrganizationID, AnyCompanyRetailOrganizationID)
	}
	if account.CreatedAt != "2026-01-01T00:00:00Z" || account.JoinedAt != "2026-01-01T00:00:00Z" || account.LeftAt != "" {
		t.Fatalf("%s dates = created %q joined %q left %q, want deterministic active membership dates", account.Name, account.CreatedAt, account.JoinedAt, account.LeftAt)
	}
	if account.AccountType != wantType || string(account.Status) != wantStatus {
		t.Fatalf("%s type/status = %q/%q, want %q/%q", account.Name, account.AccountType, account.Status, wantType, wantStatus)
	}
	wantManagementFlag := wantType == accountTypeManagement
	if account.IsManagementAccount != wantManagementFlag {
		t.Fatalf("%s IsManagementAccount = %v, want %v", account.Name, account.IsManagementAccount, wantManagementFlag)
	}
	if account.PaymentResponsibility != "management_account" {
		t.Fatalf("%s payment responsibility = %q, want management_account", account.Name, account.PaymentResponsibility)
	}
	if account.AccountType == accountTypeManagement {
		if account.PayerAccountID != account.ID || account.BillingVisibilityRole != "management-account" {
			t.Fatalf("%s payer/role = %q/%q, want self management role", account.Name, account.PayerAccountID, account.BillingVisibilityRole)
		}
	} else if account.PayerAccountID != AnyCompanyRetailManagementAccountID || account.BillingVisibilityRole != "member-account" {
		t.Fatalf("%s payer/role = %q/%q, want management payer and member role", account.Name, account.PayerAccountID, account.BillingVisibilityRole)
	}

	if account.OUPath != wantUnitPath {
		t.Fatalf("%s OUPath = %q, want %q", account.Name, account.OUPath, wantUnitPath)
	}
	if unitPath := anyCompanyRetailUnitPath(account.ParentUnitID); unitPath != account.OUPath {
		t.Fatalf("%s parent unit path lookup = %q, want repository OUPath %q", account.Name, unitPath, account.OUPath)
	}
}

func assertSeedAccountMetadata(t *testing.T, account OrganizationAccount, wantOwner, wantCostCenter, wantProduct, wantEnvironment, wantLifecycle string) {
	t.Helper()

	if account.Owner != wantOwner ||
		account.CostCenter != wantCostCenter ||
		account.Product != wantProduct ||
		account.Environment != wantEnvironment ||
		account.Lifecycle != wantLifecycle {
		t.Fatalf("%s metadata = owner %q cost-center %q product %q environment %q lifecycle %q, want %q/%q/%q/%q/%q",
			account.Name,
			account.Owner,
			account.CostCenter,
			account.Product,
			account.Environment,
			account.Lifecycle,
			wantOwner,
			wantCostCenter,
			wantProduct,
			wantEnvironment,
			wantLifecycle,
		)
	}
	for key, want := range map[string]string{
		accountTagKeyOwner:       wantOwner,
		accountTagKeyCostCenter:  wantCostCenter,
		accountTagKeyProduct:     wantProduct,
		accountTagKeyEnvironment: wantEnvironment,
		accountTagKeyLifecycle:   wantLifecycle,
	} {
		if got := account.Tags[key]; got != want {
			t.Fatalf("%s tag %q = %q, want %q", account.Name, key, got, want)
		}
	}
}

func anyCompanyRetailUnitPath(unitID string) string {
	switch unitID {
	case "ou_anycompany_root":
		return "Root"
	case "ou_anycompany_security":
		return "Root/Security"
	case "ou_anycompany_infrastructure":
		return "Root/Infrastructure"
	case "ou_anycompany_sandbox":
		return "Root/Sandbox"
	case "ou_anycompany_workloads":
		return "Root/Workloads"
	case "ou_anycompany_suspended":
		return "Root/Suspended"
	default:
		return ""
	}
}

func organizationUnitPathByID(units []OrganizationUnit) map[string]string {
	paths := make(map[string]string, len(units))
	for _, unit := range units {
		paths[unit.ID] = unit.Path
	}
	return paths
}

func organizationAccountsByName(accounts []OrganizationAccount) map[string]OrganizationAccount {
	byName := make(map[string]OrganizationAccount, len(accounts))
	for _, account := range accounts {
		byName[account.Name] = account
	}
	return byName
}

func organizationAccountNames(accounts []OrganizationAccount) []string {
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		names = append(names, account.Name)
	}
	sort.Strings(names)
	return names
}

func accountLifecycleEventsByAccount(events []AccountLifecycleEvent) map[string][]AccountLifecycleEvent {
	byAccount := make(map[string][]AccountLifecycleEvent, len(events))
	for _, event := range events {
		byAccount[event.AccountID] = append(byAccount[event.AccountID], event)
	}
	return byAccount
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var count int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows with %q: %v", query, err)
	}
	return count
}
