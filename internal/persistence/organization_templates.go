package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const anyCompanyRetailSeedTimestamp = "2026-01-01T00:00:00Z"

// OrganizationResetResult reports the organization seed rows restored by a template reset.
type OrganizationResetResult struct {
	TemplateKey          string
	OrganizationID       string
	RootsReset           int
	UnitsReset           int
	AccountsReset        int
	AccountTagsReset     int
	LifecycleEventsReset int
}

type organizationTemplateSeed struct {
	Organization Organization
	Units        []OrganizationUnit
	Accounts     []organizationTemplateAccount
	AccountTags  []organizationTemplateAccountTag
}

type organizationTemplateAccount struct {
	ID           string
	ParentUnitID string
	Name         string
	Email        string
	AccountType  string
	Status       AccountStatus
	SortOrder    int
}

type organizationTemplateAccountTag struct {
	AccountID string
	Key       string
	Value     string
}

// ResetOrganizationTemplate restores a known seed organization without changing usage, billing, report, or export tables.
func (r OrganizationRepository) ResetOrganizationTemplate(ctx context.Context, templateKey string) (OrganizationResetResult, error) {
	if r.db == nil {
		return OrganizationResetResult{}, fmt.Errorf("database handle is required")
	}
	templateKey = strings.TrimSpace(templateKey)
	if templateKey == "" {
		return OrganizationResetResult{}, fmt.Errorf("organization template key is required")
	}
	seed, ok := organizationTemplateSeedForKey(templateKey)
	if !ok {
		return OrganizationResetResult{}, fmt.Errorf("organization template %q is not available for reset", templateKey)
	}

	result := OrganizationResetResult{
		TemplateKey:          seed.Organization.TemplateKey,
		OrganizationID:       seed.Organization.ID,
		UnitsReset:           len(seed.Units),
		AccountsReset:        len(seed.Accounts),
		AccountTagsReset:     len(seed.AccountTags),
		LifecycleEventsReset: len(seed.Accounts),
	}
	for _, unit := range seed.Units {
		if unit.ParentUnitID == "" {
			result.RootsReset++
		}
	}

	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if err := clearOrganizationTemplateRows(ctx, tx, seed.Organization.ID); err != nil {
			return err
		}
		if err := insertOrganizationTemplateSeed(ctx, tx, seed); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return OrganizationResetResult{}, err
	}
	return result, nil
}

func clearOrganizationTemplateRows(ctx context.Context, tx *sql.Tx, organizationID string) error {
	deletes := []struct {
		label string
		query string
	}{
		{
			label: "account tags",
			query: `DELETE FROM account_tags
					 WHERE account_id IN (
						SELECT id FROM accounts WHERE organization_id = ?
					 )`,
		},
		{
			label: "account lifecycle events",
			query: `DELETE FROM account_lifecycle_events WHERE organization_id = ?`,
		},
		{
			label: "accounts",
			query: `DELETE FROM accounts WHERE organization_id = ?`,
		},
		{
			label: "organization roots",
			query: `DELETE FROM organization_roots WHERE organization_id = ?`,
		},
	}
	for _, del := range deletes {
		if _, err := tx.ExecContext(ctx, del.query, organizationID); err != nil {
			return fmt.Errorf("clear %s for organization %q: %w", del.label, organizationID, err)
		}
	}
	return deleteOrganizationUnitsForReset(ctx, tx, organizationID)
}

func deleteOrganizationUnitsForReset(ctx context.Context, tx *sql.Tx, organizationID string) error {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT id
		   FROM organization_units
		  WHERE organization_id = ?
		  ORDER BY length(path) DESC, path DESC`,
		organizationID,
	)
	if err != nil {
		return fmt.Errorf("list organization units for reset %q: %w", organizationID, err)
	}
	defer rows.Close()

	var unitIDs []string
	for rows.Next() {
		var unitID string
		if err := rows.Scan(&unitID); err != nil {
			return fmt.Errorf("scan organization unit for reset: %w", err)
		}
		unitIDs = append(unitIDs, unitID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate organization units for reset %q: %w", organizationID, err)
	}
	for _, unitID := range unitIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM organization_units WHERE id = ?`, unitID); err != nil {
			return fmt.Errorf("delete organization unit %q for reset: %w", unitID, err)
		}
	}
	return nil
}

func insertOrganizationTemplateSeed(ctx context.Context, tx *sql.Tx, seed organizationTemplateSeed) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO organizations (id, template_key, name, management_account_id, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
			template_key = excluded.template_key,
			name = excluded.name,
			management_account_id = excluded.management_account_id,
			created_at = excluded.created_at`,
		seed.Organization.ID,
		seed.Organization.TemplateKey,
		seed.Organization.Name,
		seed.Organization.ManagementAccountID,
		seed.Organization.CreatedAt,
	); err != nil {
		return fmt.Errorf("upsert organization template %q: %w", seed.Organization.TemplateKey, err)
	}

	for _, unit := range seed.Units {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO organization_units (
				id,
				organization_id,
				parent_unit_id,
				name,
				path,
				sort_order,
				created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			unit.ID,
			unit.OrganizationID,
			nullStringArg(unit.ParentUnitID),
			unit.Name,
			unit.Path,
			unit.SortOrder,
			unit.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert organization unit %q: %w", unit.ID, err)
		}
		if unit.ParentUnitID == "" {
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO organization_roots (id, organization_id, name, path, sort_order, created_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				unit.ID,
				unit.OrganizationID,
				unit.Name,
				unit.Path,
				unit.SortOrder,
				unit.CreatedAt,
			); err != nil {
				return fmt.Errorf("insert organization root %q: %w", unit.ID, err)
			}
		}
	}

	for _, account := range seed.Accounts {
		isManagementAccount := 0
		payerAccountID := seed.Organization.ManagementAccountID
		billingVisibilityRole := "member-account"
		if account.AccountType == accountTypeManagement {
			isManagementAccount = 1
			payerAccountID = account.ID
			billingVisibilityRole = "management-account"
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO accounts (
				id,
				organization_id,
				parent_unit_id,
				name,
				email,
				account_type,
				status,
				created_at,
				joined_at,
				left_at,
				payment_responsibility,
				payer_account_id,
				billing_visibility_role,
				sort_order,
				is_management_account
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
			account.ID,
			seed.Organization.ID,
			account.ParentUnitID,
			account.Name,
			account.Email,
			account.AccountType,
			account.Status,
			anyCompanyRetailSeedTimestamp,
			anyCompanyRetailSeedTimestamp,
			"management_account",
			payerAccountID,
			billingVisibilityRole,
			account.SortOrder,
			isManagementAccount,
		); err != nil {
			return fmt.Errorf("insert organization account %q: %w", account.ID, err)
		}
		if err := insertOrganizationTemplateLifecycleBaseline(ctx, tx, seed.Organization.ID, account); err != nil {
			return err
		}
	}

	for _, tag := range seed.AccountTags {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO account_tags (id, account_id, tag_key, tag_value, applied_at, event_source)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			organizationTemplateAccountTagID(tag),
			tag.AccountID,
			tag.Key,
			tag.Value,
			anyCompanyRetailSeedTimestamp,
			"system",
		); err != nil {
			return fmt.Errorf("insert account tag %q/%q: %w", tag.AccountID, tag.Key, err)
		}
	}
	return nil
}

func insertOrganizationTemplateLifecycleBaseline(ctx context.Context, tx *sql.Tx, organizationID string, account organizationTemplateAccount) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO account_lifecycle_events (
			id,
			organization_id,
			account_id,
			event_type,
			previous_parent_unit_id,
			new_parent_unit_id,
			previous_status,
			new_status,
			effective_at,
			created_at,
			event_source
		) VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, ?, ?, ?)`,
		"acctevt_"+account.ID+"_created",
		organizationID,
		account.ID,
		AccountLifecycleEventCreated,
		account.ParentUnitID,
		account.Status,
		anyCompanyRetailSeedTimestamp,
		anyCompanyRetailSeedTimestamp,
		"system",
	)
	if err != nil {
		return fmt.Errorf("insert lifecycle baseline for account %q: %w", account.ID, err)
	}
	return nil
}

func organizationTemplateAccountTagID(tag organizationTemplateAccountTag) string {
	return "acct_tag_" + tag.AccountID + "_" + strings.ReplaceAll(tag.Key, "-", "_")
}

func organizationTemplateSeedForKey(templateKey string) (organizationTemplateSeed, bool) {
	if !IsAnyCompanyRetailTemplate(templateKey) {
		return organizationTemplateSeed{}, false
	}
	return anyCompanyRetailTemplateSeed(), true
}

// anyCompanyRetailTemplateSeed is the reset-template copy of the AnyCompany fixture.
// When changing it, update the seed migrations and TestAnyCompanyRetailMigrationSeedMatchesResetTemplate together.
func anyCompanyRetailTemplateSeed() organizationTemplateSeed {
	organization := Organization{
		ID:                  AnyCompanyRetailOrganizationID,
		TemplateKey:         AnyCompanyRetailTemplateKey,
		Name:                "AnyCompany Retail",
		ManagementAccountID: AnyCompanyRetailManagementAccountID,
		CreatedAt:           anyCompanyRetailSeedTimestamp,
	}
	return organizationTemplateSeed{
		Organization: organization,
		Units: []OrganizationUnit{
			{ID: "ou_anycompany_root", OrganizationID: organization.ID, Name: "Root", Path: "Root", SortOrder: 0, CreatedAt: anyCompanyRetailSeedTimestamp},
			{ID: "ou_anycompany_security", OrganizationID: organization.ID, ParentUnitID: "ou_anycompany_root", Name: "Security", Path: "Root/Security", SortOrder: 10, CreatedAt: anyCompanyRetailSeedTimestamp},
			{ID: "ou_anycompany_infrastructure", OrganizationID: organization.ID, ParentUnitID: "ou_anycompany_root", Name: "Infrastructure", Path: "Root/Infrastructure", SortOrder: 20, CreatedAt: anyCompanyRetailSeedTimestamp},
			{ID: "ou_anycompany_sandbox", OrganizationID: organization.ID, ParentUnitID: "ou_anycompany_root", Name: "Sandbox", Path: "Root/Sandbox", SortOrder: 30, CreatedAt: anyCompanyRetailSeedTimestamp},
			{ID: "ou_anycompany_workloads", OrganizationID: organization.ID, ParentUnitID: "ou_anycompany_root", Name: "Workloads", Path: "Root/Workloads", SortOrder: 40, CreatedAt: anyCompanyRetailSeedTimestamp},
			{ID: "ou_anycompany_suspended", OrganizationID: organization.ID, ParentUnitID: "ou_anycompany_root", Name: "Suspended", Path: "Root/Suspended", SortOrder: 50, CreatedAt: anyCompanyRetailSeedTimestamp},
		},
		Accounts: []organizationTemplateAccount{
			{ID: AnyCompanyRetailManagementAccountID, ParentUnitID: "ou_anycompany_root", Name: "Management", Email: "management@anycompany.example", AccountType: accountTypeManagement, Status: AccountStatusActive, SortOrder: 0},
			{ID: "000011112222", ParentUnitID: "ou_anycompany_security", Name: "Log Archive", Email: "log-archive@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 10},
			{ID: "000011112223", ParentUnitID: "ou_anycompany_security", Name: "Audit", Email: "audit@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 20},
			{ID: "222233334444", ParentUnitID: "ou_anycompany_infrastructure", Name: "Shared Networking", Email: "shared-networking@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 30},
			{ID: "222233334445", ParentUnitID: "ou_anycompany_infrastructure", Name: "Platform Services", Email: "platform-services@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 40},
			{ID: "333344445555", ParentUnitID: "ou_anycompany_sandbox", Name: "Developer Sandbox 1", Email: "developer-sandbox-1@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 50},
			{ID: "333344445556", ParentUnitID: "ou_anycompany_sandbox", Name: "Developer Sandbox 2", Email: "developer-sandbox-2@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 60},
			{ID: "111122223332", ParentUnitID: "ou_anycompany_workloads", Name: "Storefront Dev", Email: "storefront-dev@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 70},
			{ID: "111122223333", ParentUnitID: "ou_anycompany_workloads", Name: "Storefront Prod", Email: "storefront-prod@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 80},
			{ID: "444455556665", ParentUnitID: "ou_anycompany_workloads", Name: "Payments Dev", Email: "payments-dev@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 90},
			{ID: "444455556666", ParentUnitID: "ou_anycompany_workloads", Name: "Payments Prod", Email: "payments-prod@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 100},
			{ID: "555566667777", ParentUnitID: "ou_anycompany_workloads", Name: "Analytics Prod", Email: "analytics-prod@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusActive, SortOrder: 110},
			{ID: "666677778888", ParentUnitID: "ou_anycompany_suspended", Name: "Deprecated Prototype", Email: "deprecated-prototype@anycompany.example", AccountType: accountTypeMember, Status: AccountStatusSuspended, SortOrder: 120},
		},
		AccountTags: anyCompanyRetailTemplateAccountTags(),
	}
}

func anyCompanyRetailTemplateAccountTags() []organizationTemplateAccountTag {
	return []organizationTemplateAccountTag{
		{AccountID: AnyCompanyRetailManagementAccountID, Key: accountTagKeyOwner, Value: "finance-operations"},
		{AccountID: AnyCompanyRetailManagementAccountID, Key: accountTagKeyCostCenter, Value: "1000-corporate"},
		{AccountID: AnyCompanyRetailManagementAccountID, Key: accountTagKeyProduct, Value: "shared-services"},
		{AccountID: AnyCompanyRetailManagementAccountID, Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: AnyCompanyRetailManagementAccountID, Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "000011112222", Key: accountTagKeyOwner, Value: "security-platform"},
		{AccountID: "000011112222", Key: accountTagKeyCostCenter, Value: "2100-security"},
		{AccountID: "000011112222", Key: accountTagKeyProduct, Value: "security"},
		{AccountID: "000011112222", Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: "000011112222", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "000011112223", Key: accountTagKeyOwner, Value: "security-platform"},
		{AccountID: "000011112223", Key: accountTagKeyCostCenter, Value: "2100-security"},
		{AccountID: "000011112223", Key: accountTagKeyProduct, Value: "security"},
		{AccountID: "000011112223", Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: "000011112223", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "222233334444", Key: accountTagKeyOwner, Value: "network-platform"},
		{AccountID: "222233334444", Key: accountTagKeyCostCenter, Value: "2200-platform"},
		{AccountID: "222233334444", Key: accountTagKeyProduct, Value: "shared-networking"},
		{AccountID: "222233334444", Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: "222233334444", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "222233334445", Key: accountTagKeyOwner, Value: "platform-engineering"},
		{AccountID: "222233334445", Key: accountTagKeyCostCenter, Value: "2200-platform"},
		{AccountID: "222233334445", Key: accountTagKeyProduct, Value: "platform-services"},
		{AccountID: "222233334445", Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: "222233334445", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "333344445555", Key: accountTagKeyOwner, Value: "developer-enablement"},
		{AccountID: "333344445555", Key: accountTagKeyCostCenter, Value: "3300-sandbox"},
		{AccountID: "333344445555", Key: accountTagKeyProduct, Value: "sandbox"},
		{AccountID: "333344445555", Key: accountTagKeyEnvironment, Value: "sandbox"},
		{AccountID: "333344445555", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "333344445556", Key: accountTagKeyOwner, Value: "developer-enablement"},
		{AccountID: "333344445556", Key: accountTagKeyCostCenter, Value: "3300-sandbox"},
		{AccountID: "333344445556", Key: accountTagKeyProduct, Value: "sandbox"},
		{AccountID: "333344445556", Key: accountTagKeyEnvironment, Value: "sandbox"},
		{AccountID: "333344445556", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "111122223332", Key: accountTagKeyOwner, Value: "storefront-team"},
		{AccountID: "111122223332", Key: accountTagKeyCostCenter, Value: "4100-storefront"},
		{AccountID: "111122223332", Key: accountTagKeyProduct, Value: "storefront"},
		{AccountID: "111122223332", Key: accountTagKeyEnvironment, Value: "development"},
		{AccountID: "111122223332", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "111122223333", Key: accountTagKeyOwner, Value: "storefront-team"},
		{AccountID: "111122223333", Key: accountTagKeyCostCenter, Value: "4100-storefront"},
		{AccountID: "111122223333", Key: accountTagKeyProduct, Value: "storefront"},
		{AccountID: "111122223333", Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: "111122223333", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "444455556665", Key: accountTagKeyOwner, Value: "payments-team"},
		{AccountID: "444455556665", Key: accountTagKeyCostCenter, Value: "4200-payments"},
		{AccountID: "444455556665", Key: accountTagKeyProduct, Value: "payments"},
		{AccountID: "444455556665", Key: accountTagKeyEnvironment, Value: "development"},
		{AccountID: "444455556665", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "444455556666", Key: accountTagKeyOwner, Value: "payments-team"},
		{AccountID: "444455556666", Key: accountTagKeyCostCenter, Value: "4200-payments"},
		{AccountID: "444455556666", Key: accountTagKeyProduct, Value: "payments"},
		{AccountID: "444455556666", Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: "444455556666", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "555566667777", Key: accountTagKeyOwner, Value: "data-platform"},
		{AccountID: "555566667777", Key: accountTagKeyCostCenter, Value: "4300-analytics"},
		{AccountID: "555566667777", Key: accountTagKeyProduct, Value: "analytics"},
		{AccountID: "555566667777", Key: accountTagKeyEnvironment, Value: "production"},
		{AccountID: "555566667777", Key: accountTagKeyLifecycle, Value: "active"},
		{AccountID: "666677778888", Key: accountTagKeyOwner, Value: "innovation-lab"},
		{AccountID: "666677778888", Key: accountTagKeyCostCenter, Value: "9900-deprecated"},
		{AccountID: "666677778888", Key: accountTagKeyProduct, Value: "prototype"},
		{AccountID: "666677778888", Key: accountTagKeyEnvironment, Value: "retired"},
		{AccountID: "666677778888", Key: accountTagKeyLifecycle, Value: "deprecated"},
	}
}
