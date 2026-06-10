package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
	"aws-billing-simulator/internal/scenario"
)

func TestOrganizationHierarchyEditorFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	workspacePath := filepath.Join(root, "organization-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	client := appTestHTTPClient()
	resp, err := client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Workspace Required") || !strings.Contains(body, `href="/workspaces"`) {
		t.Fatalf("GET /organization without workspace missing workspace empty state: %s", body)
	}

	resp, err = client.PostForm(server.URL()+"/workspaces/open", url.Values{
		"workspace_path": {workspacePath},
	})
	if err != nil {
		t.Fatalf("POST /workspaces/open error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /workspaces/open final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/resources" {
		t.Fatalf("POST /workspaces/open final path = %q, want /resources", got)
	}
	if _, err := os.Stat(persistence.WorkspaceDBPath(workspacePath)); err != nil {
		t.Fatalf("workspace database was not created: %v", err)
	}

	resp, err = client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization with workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization with workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Organization - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/organization">Organization</a>`,
		"AnyCompany Retail",
		"Root - ou_anycompany_root",
		"Storefront Prod",
		"Deprecated Prototype",
		"storefront-team",
		"4100-storefront",
		"shared-networking",
		"9900-deprecated",
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		`action="/organization/accounts/create"`,
		`action="/organization/accounts/move"`,
		`action="/organization/accounts/suspend"`,
		`action="/organization/accounts/close"`,
		`href="/resources?account_id=111122223333"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=111122223333&amp;viewer_account_id=111122223333&amp;viewer_role=member-account"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization with workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/create", url.Values{
		"organization_id": {persistence.AnyCompanyRetailOrganizationID},
		"account_id":      {"777788889901"},
		"parent_unit_id":  {"ou_anycompany_sandbox"},
		"account_name":    {"Feature Lab Account"},
		"account_email":   {"feature-lab@anycompany.example"},
		"effective_at":    {"2026-02-02T00:00"},
	})
	if err != nil {
		t.Fatalf("POST create account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Created account Feature Lab Account") {
		t.Fatalf("POST create account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/move", url.Values{
		"account_id":     {"777788889901"},
		"parent_unit_id": {"ou_anycompany_workloads"},
		"effective_at":   {"2026-02-05T00:00"},
	})
	if err != nil {
		t.Fatalf("POST move account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Moved Feature Lab Account to Root/Workloads") {
		t.Fatalf("POST move account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/suspend", url.Values{
		"account_id":   {"777788889901"},
		"effective_at": {"2026-02-10T00:00"},
	})
	if err != nil {
		t.Fatalf("POST suspend account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Suspended Feature Lab Account") {
		t.Fatalf("POST suspend account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/close", url.Values{
		"account_id":   {"777788889901"},
		"effective_at": {"2026-02-15T00:00"},
	})
	if err != nil {
		t.Fatalf("POST close account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST close account status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Closed Feature Lab Account",
		"14 accounts",
		"12 active, 1 suspended, 1 closed",
		"Feature Lab Account",
		"Root/Workloads",
		`<span class="status status-closed">Closed</span>`,
		"17 events",
		"Suspended -&gt; Closed",
		`href="/resources?account_id=777788889901"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=777788889901&amp;viewer_account_id=777788889901&amp;viewer_role=member-account"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST close account body missing %q: %s", want, body)
		}
	}

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("workspace database is nil after organization workflow")
	}
	repo := persistence.NewOrganizationRepository(db)
	account, err := repo.GetAccount(ctx, "777788889901")
	if err != nil {
		t.Fatalf("GetAccount(created) error = %v", err)
	}
	if account.Status != persistence.AccountStatusClosed ||
		account.OUPath != "Root/Workloads" ||
		account.LeftAt != "2026-02-15T00:00:00Z" {
		t.Fatalf("created account after browser workflow = %+v, want closed in Workloads with left_at", account)
	}
	events, err := repo.ListAccountLifecycleEvents(ctx, persistence.AnyCompanyRetailOrganizationID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() error = %v", err)
	}
	if count := organizationLifecycleEventCountForAccount(events, "777788889901"); count != 4 {
		t.Fatalf("created account lifecycle event count = %d, want 4", count)
	}
}

func TestAnyCompanySeedOrganizationFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	workspacePath := filepath.Join(root, "anycompany-seed-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StatePath = filepath.Join(root, "state.json")
	cfg.WorkspacePath = workspacePath
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	repo := persistence.NewOrganizationRepository(db)
	client := appTestHTTPClient()

	resp, err := client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"AnyCompany Retail",
		"anycompany-retail",
		"999988887777",
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		"Storefront Prod",
		"Root/Workloads",
		"storefront-team",
		"4100-storefront",
		"Deprecated Prototype",
		"9900-deprecated",
		`href="/resources?account_id=111122223333"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization seed body missing %q: %s", want, body)
		}
	}

	organization, err := repo.GetOrganizationByTemplate(ctx, persistence.AnyCompanyRetailTemplateKey)
	if err != nil {
		t.Fatalf("GetOrganizationByTemplate() error = %v", err)
	}
	if organization.ID != persistence.AnyCompanyRetailOrganizationID ||
		organization.ManagementAccountID != persistence.AnyCompanyRetailManagementAccountID {
		t.Fatalf("organization = %+v, want AnyCompany Retail seed identifiers", organization)
	}
	accounts, err := repo.ListAccounts(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if len(accounts) != 13 {
		t.Fatalf("seed account count = %d, want 13", len(accounts))
	}
	tags, err := repo.ListAccountTags(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListAccountTags() error = %v", err)
	}
	if len(tags) != 65 {
		t.Fatalf("seed account tag count = %d, want 65", len(tags))
	}
	events, err := repo.ListAccountLifecycleEvents(ctx, organization.ID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() error = %v", err)
	}
	if len(events) != 13 {
		t.Fatalf("seed lifecycle event count = %d, want 13", len(events))
	}

	if _, err := repo.CreateAccount(ctx, persistence.AccountCreateRequest{
		ID:             "777788889902",
		OrganizationID: organization.ID,
		ParentUnitID:   "ou_anycompany_sandbox",
		Name:           "Seed Drift Account",
		Email:          "seed-drift@anycompany.example",
		EffectiveAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateAccount(drift) error = %v", err)
	}
	if _, err := repo.MoveAccount(ctx, persistence.AccountMoveRequest{
		AccountID:    "111122223333",
		ParentUnitID: "ou_anycompany_sandbox",
		EffectiveAt:  "2026-02-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("MoveAccount(Storefront Prod drift) error = %v", err)
	}
	if _, err := repo.SuspendAccount(ctx, persistence.AccountSuspendRequest{
		AccountID:   "111122223333",
		EffectiveAt: "2026-02-03T00:00:00Z",
	}); err != nil {
		t.Fatalf("SuspendAccount(Storefront Prod drift) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE account_tags SET tag_value = ? WHERE account_id = ? AND tag_key = ?`, "drifted-owner", "111122223333", "owner"); err != nil {
		t.Fatalf("update account tag drift: %v", err)
	}

	resp, err = client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization after drift error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization after drift status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Seed Drift Account") ||
		!strings.Contains(body, "14 accounts") ||
		!strings.Contains(body, "drifted-owner") {
		t.Fatalf("GET /organization after drift missing visible drift: %s", body)
	}

	definition, err := scenario.LoadSeedDefinition(scenario.UntaggedDataTransferSpikeSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition() error = %v", err)
	}
	result, err := scenario.NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(packaged scenario) error = %v", err)
	}
	if result.Run.Status != "succeeded" ||
		result.ResourcesCreated != 2 ||
		result.UsageEventsCreated != 3 ||
		result.BillsIssued != 1 {
		t.Fatalf("Run(packaged scenario) result = %+v, want successful AnyCompany lab execution", result)
	}

	resp, err = client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization after scenario reset error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization after scenario reset status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"AnyCompany Retail",
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		"Storefront Prod",
		"Root/Workloads",
		"storefront-team",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization after scenario reset missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Seed Drift Account") || strings.Contains(body, "drifted-owner") {
		t.Fatalf("GET /organization after scenario reset retained drift: %s", body)
	}
	storefrontProd, err := repo.GetAccount(ctx, "111122223333")
	if err != nil {
		t.Fatalf("GetAccount(Storefront Prod) after reset error = %v", err)
	}
	if storefrontProd.ParentUnitID != "ou_anycompany_workloads" ||
		storefrontProd.Status != persistence.AccountStatusActive ||
		storefrontProd.Owner != "storefront-team" ||
		storefrontProd.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID {
		t.Fatalf("Storefront Prod after scenario reset = %+v, want seed OU/status/owner/payer", storefrontProd)
	}
}

// TestOrganizationAccountSimulationEpicWorksInFreshWorkspace keeps the parent epic guarded across its browser-facing surfaces.
func TestOrganizationAccountSimulationEpicWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "organization-epic-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClient()

	resp, err := client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"AnyCompany Retail",
		"999988887777",
		"Storefront Prod",
		"Root/Workloads",
		"storefront-team",
		"4100-storefront",
		"12 active, 1 suspended, 0 closed",
		`action="/organization/accounts/create"`,
		`action="/organization/accounts/move"`,
		`action="/organization/accounts/suspend"`,
		`action="/organization/accounts/close"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=111122223333&amp;viewer_account_id=111122223333&amp;viewer_role=member-account"`,
		`href="/bills?payer_account_id=999988887777&amp;viewer_account_id=999988887777&amp;viewer_role=management-account">Bills</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL() + "/resources")
	if err != nil {
		t.Fatalf("GET /resources error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `name="account_id" value="111122223333"`) {
		t.Fatalf("GET /resources body missing Storefront Prod usage account default: %s", body)
	}
	if count := strings.Count(body, `name="payer_account_id" value="999988887777"`); count != 3 {
		t.Fatalf("GET /resources management payer defaults = %d, want 3: %s", count, body)
	}
	if strings.Contains(body, `name="payer_account_id" value="111122223333"`) {
		t.Fatalf("GET /resources still defaults payer forms to member account: %s", body)
	}

	seedFilterableUsage(t, ctx, db)

	var managementLineItems int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items WHERE payer_account_id = ?`, persistence.AnyCompanyRetailManagementAccountID).Scan(&managementLineItems); err != nil {
		t.Fatalf("count management bill_line_items: %v", err)
	}
	if managementLineItems != 2 {
		t.Fatalf("management bill_line_items = %d, want 2", managementLineItems)
	}

	resp, err = client.Get(server.URL() + "/bills?viewer_role=management-account&viewer_account_id=999988887777")
	if err != nil {
		t.Fatalf("GET /bills management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="management-account" selected>Management</option>`,
		`name="viewer_account_id" value="999988887777"`,
		"Filter web",
		"Filter bucket",
		"111122223333",
		"222233334444",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills management viewer body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL() + "/bills?viewer_role=member-account&viewer_account_id=111122223333")
	if err != nil {
		t.Fatalf("GET /bills member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="member-account" selected>Member</option>`,
		`name="viewer_account_id" value="111122223333"`,
		"Filter web",
		"111122223333",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills member viewer body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{
		"Filter bucket",
		"222233334444",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /bills member viewer leaked %q: %s", leaked, body)
		}
	}
}

func TestOrganizationUIRendersHierarchyAndBillingLinks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/organization")
	if err != nil {
		t.Fatalf("GET /organization error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	for _, want := range []string{
		"<h1>Organization</h1>",
		"AnyCompany Retail",
		"Management account",
		"Root - ou_anycompany_root",
		"Security - ou_anycompany_security",
		"Workloads - ou_anycompany_workloads",
		"Storefront Prod",
		"Deprecated Prototype",
		"Cost Center",
		"storefront-team",
		"4100-storefront",
		"shared-networking",
		"9900-deprecated",
		`<span class="status status-suspended">Suspended</span>`,
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		"Account Detail",
		"Billing Role",
		"Simulator Clock",
		`name="effective_at" value="2026-02-01T00:00"`,
		`action="/organization/accounts/create"`,
		"Create Account",
		"Move Account",
		"Suspend Account",
		"Close Account",
		"Lifecycle History",
		"13 events",
		`href="/resources?account_id=111122223333"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=111122223333&amp;viewer_account_id=111122223333&amp;viewer_role=member-account"`,
		`href="/bills?payer_account_id=999988887777&amp;viewer_account_id=999988887777&amp;viewer_role=management-account">Bills</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization body missing %q: %s", want, body)
		}
	}
}

func TestOrganizationUIAccountLifecycleWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.PostForm(server.URL+"/organization/accounts/create", url.Values{
		"organization_id": {persistence.AnyCompanyRetailOrganizationID},
		"account_id":      {"777788889997"},
		"parent_unit_id":  {"ou_anycompany_sandbox"},
		"account_name":    {"Partner Integration"},
		"account_email":   {"partner-integration@anycompany.example"},
		"effective_at":    {"2026-02-01T00:00"},
	})
	if err != nil {
		t.Fatalf("POST create account error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Created account Partner Integration") {
		t.Fatalf("POST create account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL+"/organization/accounts/move", url.Values{
		"account_id":     {"777788889997"},
		"parent_unit_id": {"ou_anycompany_workloads"},
		"effective_at":   {"2026-02-05T00:00"},
	})
	if err != nil {
		t.Fatalf("POST move account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Moved Partner Integration to Root/Workloads") {
		t.Fatalf("POST move account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL+"/organization/accounts/suspend", url.Values{
		"account_id":   {"777788889997"},
		"effective_at": {"2026-02-10T00:00"},
	})
	if err != nil {
		t.Fatalf("POST suspend account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Suspended Partner Integration") {
		t.Fatalf("POST suspend account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL+"/organization/accounts/close", url.Values{
		"account_id":   {"777788889997"},
		"effective_at": {"2026-02-15T00:00"},
	})
	if err != nil {
		t.Fatalf("POST close account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST close account status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Closed Partner Integration",
		"14 accounts",
		"12 active, 1 suspended, 1 closed",
		"Partner Integration",
		"Root/Workloads",
		`<span class="status status-closed">Closed</span>`,
		"17 events",
		"Suspended -&gt; Closed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST close account body missing %q: %s", want, body)
		}
	}

	repo := persistence.NewOrganizationRepository(db)
	account, err := repo.GetAccount(ctx, "777788889997")
	if err != nil {
		t.Fatalf("GetAccount(created) error = %v", err)
	}
	if account.Status != persistence.AccountStatusClosed ||
		account.OUPath != "Root/Workloads" ||
		account.LeftAt != "2026-02-15T00:00:00Z" {
		t.Fatalf("created account after UI workflow = %+v, want closed in Workloads with left_at", account)
	}
	events, err := repo.ListAccountLifecycleEvents(ctx, persistence.AnyCompanyRetailOrganizationID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() error = %v", err)
	}
	if count := organizationLifecycleEventCountForAccount(events, "777788889997"); count != 4 {
		t.Fatalf("created account lifecycle event count = %d, want 4", count)
	}
}

func TestBillingVisibilityModelFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "billing-visibility-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	seedFilterableUsage(t, context.Background(), db)

	client := appTestHTTPClient()
	tests := []struct {
		name  string
		path  string
		wants []string
		leaks []string
	}{
		{
			name: "management consolidated viewer sees linked account spend",
			path: "/bills?viewer_role=management-account&viewer_account_id=999988887777",
			wants: []string{
				`<option value="management-account" selected>Management</option>`,
				`name="viewer_account_id" value="999988887777"`,
				"Filter web",
				"Filter bucket",
				"Amazon EC2",
				"Amazon S3",
				"111122223333",
				"222233334444",
			},
		},
		{
			name: "finance viewer defaults to management payer scope",
			path: "/bills?viewer_role=finance",
			wants: []string{
				`<option value="finance" selected>Finance</option>`,
				"Filter web",
				"Filter bucket",
				"Amazon EC2",
				"Amazon S3",
				"111122223333",
				"222233334444",
			},
		},
		{
			name: "member viewer sees only its own informational charges",
			path: "/bills?viewer_role=member-account&viewer_account_id=111122223333",
			wants: []string{
				`<option value="member-account" selected>Member</option>`,
				`name="viewer_account_id" value="111122223333"`,
				"Filter web",
				"Amazon EC2",
				"111122223333",
			},
			leaks: []string{
				"Filter bucket",
				"Amazon S3",
				"222233334444",
			},
		},
		{
			name: "instructor viewer sees all local training data",
			path: "/bills?viewer_role=instructor",
			wants: []string{
				`<option value="instructor" selected>Instructor</option>`,
				"Filter web",
				"Filter bucket",
				"Amazon EC2",
				"Amazon S3",
				"111122223333",
				"222233334444",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(server.URL() + tt.path)
			if err != nil {
				t.Fatalf("GET %s error = %v", tt.path, err)
			}
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d; body=%s", tt.path, resp.StatusCode, http.StatusOK, body)
			}
			for _, want := range tt.wants {
				if !strings.Contains(body, want) {
					t.Fatalf("GET %s body missing %q: %s", tt.path, want, body)
				}
			}
			for _, leaked := range tt.leaks {
				if strings.Contains(body, leaked) {
					t.Fatalf("GET %s body leaked %q: %s", tt.path, leaked, body)
				}
			}
		})
	}
}
