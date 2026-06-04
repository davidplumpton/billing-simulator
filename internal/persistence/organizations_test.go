package persistence

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
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

	storefrontProd, err := repo.GetAccount(ctx, "111122223333")
	if err != nil {
		t.Fatalf("GetAccount(Storefront Prod) error = %v", err)
	}
	if storefrontProd.Name != "Storefront Prod" || storefrontProd.PayerAccountID != AnyCompanyRetailManagementAccountID {
		t.Fatalf("Storefront Prod = %+v, want member account paid by management", storefrontProd)
	}
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
