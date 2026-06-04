package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	// AnyCompanyRetailTemplateKey is the stable scenario and seed-data template identifier.
	AnyCompanyRetailTemplateKey = "anycompany-retail"

	// AnyCompanyRetailOrganizationID is the durable organization ID seeded in fresh workspaces.
	AnyCompanyRetailOrganizationID = "org_anycompany_retail"

	// AnyCompanyRetailManagementAccountID is the payer account for the seeded organization.
	AnyCompanyRetailManagementAccountID = "999988887777"
)

const (
	accountTypeManagement = "management"
	accountTypeMember     = "member"
)

// AccountStatus names the supported organization account lifecycle states.
type AccountStatus string

const (
	AccountStatusActive    AccountStatus = "active"
	AccountStatusSuspended AccountStatus = "suspended"
	AccountStatusClosed    AccountStatus = "closed"
)

type anyCompanyRetailAccountReference struct {
	Name string
	ID   string
}

var anyCompanyRetailAccountReferences = []anyCompanyRetailAccountReference{
	{Name: "Management", ID: AnyCompanyRetailManagementAccountID},
	{Name: "Management Account", ID: AnyCompanyRetailManagementAccountID},
	{Name: "Log Archive", ID: "000011112222"},
	{Name: "Audit", ID: "000011112223"},
	{Name: "Shared Networking", ID: "222233334444"},
	{Name: "Platform Services", ID: "222233334445"},
	{Name: "Developer Sandbox 1", ID: "333344445555"},
	{Name: "Developer Sandbox 2", ID: "333344445556"},
	{Name: "Storefront Dev", ID: "111122223332"},
	{Name: "Storefront Prod", ID: "111122223333"},
	{Name: "Payments Dev", ID: "444455556665"},
	{Name: "Payments Prod", ID: "444455556666"},
	{Name: "Analytics Prod", ID: "555566667777"},
	{Name: "Deprecated Prototype", ID: "666677778888"},
}

// Organization stores one simulated AWS Organizations root.
type Organization struct {
	ID                  string
	TemplateKey         string
	Name                string
	ManagementAccountID string
	CreatedAt           string
}

// OrganizationRoot stores the top-level root node for an organization tree.
type OrganizationRoot struct {
	ID             string
	OrganizationID string
	Name           string
	Path           string
	SortOrder      int
	CreatedAt      string
}

// OrganizationUnit stores one OU in the simulated organization tree.
type OrganizationUnit struct {
	ID             string
	OrganizationID string
	ParentUnitID   string
	Name           string
	Path           string
	SortOrder      int
	CreatedAt      string
}

// OrganizationAccount stores one management or member account in the organization.
type OrganizationAccount struct {
	ID                    string
	OrganizationID        string
	ParentUnitID          string
	OUPath                string
	Name                  string
	Email                 string
	AccountType           string
	Status                AccountStatus
	CreatedAt             string
	JoinedAt              string
	LeftAt                string
	PaymentResponsibility string
	PayerAccountID        string
	BillingVisibilityRole string
	IsManagementAccount   bool
	SortOrder             int
}

// OrganizationRepository reads the simulated organization hierarchy from a workspace database.
type OrganizationRepository struct {
	db *sql.DB
}

// NewOrganizationRepository creates a repository backed by a workspace database.
func NewOrganizationRepository(db *sql.DB) OrganizationRepository {
	return OrganizationRepository{db: db}
}

// GetOrganizationByTemplate reads the organization seeded for a template key.
func (r OrganizationRepository) GetOrganizationByTemplate(ctx context.Context, templateKey string) (Organization, error) {
	if r.db == nil {
		return Organization{}, fmt.Errorf("database handle is required")
	}
	templateKey = strings.TrimSpace(templateKey)
	if templateKey == "" {
		return Organization{}, fmt.Errorf("organization template key is required")
	}

	var organization Organization
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT id, template_key, name, management_account_id, created_at
		   FROM organizations
		  WHERE template_key = ?`,
		templateKey,
	).Scan(
		&organization.ID,
		&organization.TemplateKey,
		&organization.Name,
		&organization.ManagementAccountID,
		&organization.CreatedAt,
	); err != nil {
		return Organization{}, fmt.Errorf("get organization template %q: %w", templateKey, err)
	}
	return organization, nil
}

// ListRoots returns the top-level root nodes for an organization in deterministic order.
func (r OrganizationRepository) ListRoots(ctx context.Context, organizationID string) ([]OrganizationRoot, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, fmt.Errorf("organization ID is required")
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, organization_id, name, path, sort_order, created_at
		   FROM organization_roots
		  WHERE organization_id = ?
		  ORDER BY sort_order, path`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list organization roots for %q: %w", organizationID, err)
	}
	defer rows.Close()

	var roots []OrganizationRoot
	for rows.Next() {
		root, err := scanOrganizationRoot(rows)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization roots for %q: %w", organizationID, err)
	}
	return roots, nil
}

// ListUnits returns all OUs for an organization in deterministic tree order.
func (r OrganizationRepository) ListUnits(ctx context.Context, organizationID string) ([]OrganizationUnit, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, fmt.Errorf("organization ID is required")
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, organization_id, parent_unit_id, name, path, sort_order, created_at
		   FROM organization_units
		  WHERE organization_id = ?
		  ORDER BY sort_order, path`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list organization units for %q: %w", organizationID, err)
	}
	defer rows.Close()

	var units []OrganizationUnit
	for rows.Next() {
		unit, err := scanOrganizationUnit(rows)
		if err != nil {
			return nil, err
		}
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization units for %q: %w", organizationID, err)
	}
	return units, nil
}

// ListAccounts returns all accounts for an organization in deterministic fixture order.
func (r OrganizationRepository) ListAccounts(ctx context.Context, organizationID string) ([]OrganizationAccount, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, fmt.Errorf("organization ID is required")
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id,
				organization_id,
				parent_unit_id,
				ou_path,
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
				is_management_account,
				sort_order
		   FROM organization_account_hierarchy
		  WHERE organization_id = ?
		  ORDER BY sort_order, name`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list organization accounts for %q: %w", organizationID, err)
	}
	defer rows.Close()

	var accounts []OrganizationAccount
	for rows.Next() {
		account, err := scanOrganizationAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organization accounts for %q: %w", organizationID, err)
	}
	return accounts, nil
}

// GetAccount reads one organization account by its stable account ID.
func (r OrganizationRepository) GetAccount(ctx context.Context, accountID string) (OrganizationAccount, error) {
	if r.db == nil {
		return OrganizationAccount{}, fmt.Errorf("database handle is required")
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return OrganizationAccount{}, fmt.Errorf("account ID is required")
	}

	account, err := scanOrganizationAccount(r.db.QueryRowContext(
		ctx,
		`SELECT id,
				organization_id,
				parent_unit_id,
				ou_path,
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
				is_management_account,
				sort_order
		   FROM organization_account_hierarchy
		  WHERE id = ?`,
		accountID,
	))
	if err != nil {
		return OrganizationAccount{}, fmt.Errorf("get organization account %q: %w", accountID, err)
	}
	return account, nil
}

// IsAnyCompanyRetailTemplate reports whether a template key names the seeded AnyCompany fixture.
func IsAnyCompanyRetailTemplate(templateKey string) bool {
	return organizationLookupKey(templateKey) == organizationLookupKey(AnyCompanyRetailTemplateKey)
}

// AnyCompanyRetailAccountIDForName resolves fixture account names and common aliases to stable account IDs.
func AnyCompanyRetailAccountIDForName(name string) (string, bool) {
	key := organizationLookupKey(name)
	if key == "" {
		return "", false
	}
	for _, account := range anyCompanyRetailAccountReferences {
		if organizationLookupKey(account.Name) == key {
			return account.ID, true
		}
	}
	return "", false
}

// AnyCompanyRetailAccountNames returns accepted account names and aliases for scenario validation messages.
func AnyCompanyRetailAccountNames() []string {
	names := make([]string, 0, len(anyCompanyRetailAccountReferences))
	for _, account := range anyCompanyRetailAccountReferences {
		names = append(names, account.Name)
	}
	return names
}

type organizationRootRow interface {
	Scan(dest ...any) error
}

func scanOrganizationRoot(row organizationRootRow) (OrganizationRoot, error) {
	var root OrganizationRoot
	if err := row.Scan(
		&root.ID,
		&root.OrganizationID,
		&root.Name,
		&root.Path,
		&root.SortOrder,
		&root.CreatedAt,
	); err != nil {
		return OrganizationRoot{}, fmt.Errorf("scan organization root: %w", err)
	}
	return root, nil
}

type organizationUnitRow interface {
	Scan(dest ...any) error
}

func scanOrganizationUnit(row organizationUnitRow) (OrganizationUnit, error) {
	var unit OrganizationUnit
	var parentUnitID sql.NullString
	if err := row.Scan(
		&unit.ID,
		&unit.OrganizationID,
		&parentUnitID,
		&unit.Name,
		&unit.Path,
		&unit.SortOrder,
		&unit.CreatedAt,
	); err != nil {
		return OrganizationUnit{}, fmt.Errorf("scan organization unit: %w", err)
	}
	unit.ParentUnitID = nullStringValue(parentUnitID)
	return unit, nil
}

type organizationAccountRow interface {
	Scan(dest ...any) error
}

func scanOrganizationAccount(row organizationAccountRow) (OrganizationAccount, error) {
	var account OrganizationAccount
	var leftAt sql.NullString
	var isManagementAccount int
	if err := row.Scan(
		&account.ID,
		&account.OrganizationID,
		&account.ParentUnitID,
		&account.OUPath,
		&account.Name,
		&account.Email,
		&account.AccountType,
		&account.Status,
		&account.CreatedAt,
		&account.JoinedAt,
		&leftAt,
		&account.PaymentResponsibility,
		&account.PayerAccountID,
		&account.BillingVisibilityRole,
		&isManagementAccount,
		&account.SortOrder,
	); err != nil {
		return OrganizationAccount{}, fmt.Errorf("scan organization account: %w", err)
	}
	account.LeftAt = nullStringValue(leftAt)
	account.IsManagementAccount = isManagementAccount == 1
	return account, nil
}

func organizationLookupKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
