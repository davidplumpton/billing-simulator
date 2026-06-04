// Package billingvisibility defines simulated billing access roles and policies.
package billingvisibility

import (
	"fmt"
	"strings"
)

// Role identifies a simulated viewer persona for billing data.
type Role string

const (
	// RoleManagementAccount represents the payer account for consolidated billing.
	RoleManagementAccount Role = "management-account"

	// RoleMemberAccount represents an account-scoped viewer of informational billing data.
	RoleMemberAccount Role = "member-account"

	// RoleFinance represents a finance persona that can view organization billing records.
	RoleFinance Role = "finance"

	// RoleInstructor represents a scenario author or reviewer with all simulated data visible.
	RoleInstructor Role = "instructor"
)

// ParseRole normalizes and validates a role name from UI, scenario, or persistence input.
func ParseRole(value string) (Role, error) {
	role := Role(strings.TrimSpace(value))
	if !role.Valid() {
		return "", fmt.Errorf("unsupported billing role %q", value)
	}
	return role, nil
}

// String returns the stable serialized role name.
func (r Role) String() string {
	return string(r)
}

// Valid reports whether the role is one of the simulator's billing personas.
func (r Role) Valid() bool {
	switch r {
	case RoleManagementAccount, RoleMemberAccount, RoleFinance, RoleInstructor:
		return true
	default:
		return false
	}
}

// PolicyType identifies the named visibility policy attached to a viewer.
type PolicyType string

const (
	// PolicyTypeManagementConsolidated allows payer-account consolidated billing views.
	PolicyTypeManagementConsolidated PolicyType = "management-account-consolidated"

	// PolicyTypeMemberInformational allows account-scoped informational billing views.
	PolicyTypeMemberInformational PolicyType = "member-account-informational"

	// PolicyTypeFinanceConsolidated allows finance access to payer-account billing views.
	PolicyTypeFinanceConsolidated PolicyType = "finance-consolidated"

	// PolicyTypeInstructorAllAccounts allows instructor access to every simulated account.
	PolicyTypeInstructorAllAccounts PolicyType = "instructor-all-accounts"
)

// AccountScope describes which account columns should constrain billing rows.
type AccountScope string

const (
	// AccountScopePayerAccount scopes rows by payer_account_id while allowing linked accounts underneath.
	AccountScopePayerAccount AccountScope = "payer-account"

	// AccountScopeUsageAccount scopes rows by usage_account_id for member-account informational views.
	AccountScopeUsageAccount AccountScope = "usage-account"

	// AccountScopeAllAccounts allows every account row in the local training workspace.
	AccountScopeAllAccounts AccountScope = "all-accounts"
)

// DocumentAccess describes which bill and invoice documents a policy can inspect.
type DocumentAccess string

const (
	// DocumentAccessNone blocks bill and invoice documents.
	DocumentAccessNone DocumentAccess = "none"

	// DocumentAccessInformational allows non-paying member-account bill views.
	DocumentAccessInformational DocumentAccess = "informational"

	// DocumentAccessFinancial allows issued invoices, obligations, and payment views.
	DocumentAccessFinancial DocumentAccess = "financial"
)

// View identifies a billing surface that may be enabled or hidden by policy.
type View string

const (
	// ViewBills is the monthly bills and bill-state surface.
	ViewBills View = "bills"

	// ViewChargeBreakdown is the service, account, usage-type, and resource drilldown surface.
	ViewChargeBreakdown View = "charge-breakdown"

	// ViewCostExplorer is the Cost Explorer-style analysis surface.
	ViewCostExplorer View = "cost-explorer"

	// ViewInvoices is the issued invoice document surface.
	ViewInvoices View = "invoices"

	// ViewPayments is the invoice obligation and payment-state surface.
	ViewPayments View = "payments"

	// ViewExports is the billing export surface.
	ViewExports View = "exports"

	// ViewBillingAccess is the organization billing-access policy configuration surface.
	ViewBillingAccess View = "billing-access"

	// ViewScenarioReview is the instructor-only scenario review and grading surface.
	ViewScenarioReview View = "scenario-review"
)

// Viewer describes the simulated principal asking to see billing data.
type Viewer struct {
	Role                Role
	AccountID           string
	ManagementAccountID string
}

// Policy is the normalized billing visibility contract used by UI and repositories.
type Policy struct {
	Type                   PolicyType
	Role                   Role
	AccountScope           AccountScope
	PayerAccountID         string
	UsageAccountID         string
	DocumentAccess         DocumentAccess
	CanExportCostData      bool
	CanManagePayments      bool
	CanManageBillingAccess bool
	CanReviewScenarios     bool
}

// BillingRow carries the account columns needed to test bill, report, and export visibility.
type BillingRow struct {
	PayerAccountID string
	UsageAccountID string
}

// PolicyForViewer builds the default billing visibility policy for a simulated viewer.
func PolicyForViewer(viewer Viewer) (Policy, error) {
	role, err := ParseRole(viewer.Role.String())
	if err != nil {
		return Policy{}, err
	}

	accountID := strings.TrimSpace(viewer.AccountID)
	managementAccountID := strings.TrimSpace(viewer.ManagementAccountID)

	switch role {
	case RoleManagementAccount:
		if accountID == "" {
			return Policy{}, fmt.Errorf("management-account billing role requires account_id")
		}
		return Policy{
			Type:                   PolicyTypeManagementConsolidated,
			Role:                   role,
			AccountScope:           AccountScopePayerAccount,
			PayerAccountID:         accountID,
			DocumentAccess:         DocumentAccessFinancial,
			CanExportCostData:      true,
			CanManagePayments:      true,
			CanManageBillingAccess: true,
		}, nil
	case RoleMemberAccount:
		if accountID == "" {
			return Policy{}, fmt.Errorf("member-account billing role requires account_id")
		}
		return Policy{
			Type:              PolicyTypeMemberInformational,
			Role:              role,
			AccountScope:      AccountScopeUsageAccount,
			UsageAccountID:    accountID,
			DocumentAccess:    DocumentAccessInformational,
			CanExportCostData: true,
		}, nil
	case RoleFinance:
		payerAccountID := managementAccountID
		if payerAccountID == "" {
			payerAccountID = accountID
		}
		if payerAccountID == "" {
			return Policy{}, fmt.Errorf("finance billing role requires management_account_id")
		}
		return Policy{
			Type:              PolicyTypeFinanceConsolidated,
			Role:              role,
			AccountScope:      AccountScopePayerAccount,
			PayerAccountID:    payerAccountID,
			DocumentAccess:    DocumentAccessFinancial,
			CanExportCostData: true,
			CanManagePayments: true,
		}, nil
	case RoleInstructor:
		return Policy{
			Type:                   PolicyTypeInstructorAllAccounts,
			Role:                   role,
			AccountScope:           AccountScopeAllAccounts,
			DocumentAccess:         DocumentAccessFinancial,
			CanExportCostData:      true,
			CanManagePayments:      true,
			CanManageBillingAccess: true,
			CanReviewScenarios:     true,
		}, nil
	default:
		return Policy{}, fmt.Errorf("unsupported billing role %q", role)
	}
}

// AllowsView reports whether the policy can open a billing surface before row filtering.
func (p Policy) AllowsView(view View) bool {
	switch view {
	case ViewBills, ViewChargeBreakdown, ViewCostExplorer:
		return p.AccountScope != ""
	case ViewInvoices:
		return p.DocumentAccess == DocumentAccessFinancial
	case ViewPayments:
		return p.CanManagePayments
	case ViewExports:
		return p.CanExportCostData
	case ViewBillingAccess:
		return p.CanManageBillingAccess
	case ViewScenarioReview:
		return p.CanReviewScenarios
	default:
		return false
	}
}

// AllowsBillingRow reports whether a billing row is visible under this policy.
func (p Policy) AllowsBillingRow(row BillingRow) bool {
	payerAccountID := strings.TrimSpace(row.PayerAccountID)
	usageAccountID := strings.TrimSpace(row.UsageAccountID)

	switch p.AccountScope {
	case AccountScopeAllAccounts:
		return payerAccountID != "" || usageAccountID != ""
	case AccountScopePayerAccount:
		return p.PayerAccountID != "" && payerAccountID == p.PayerAccountID
	case AccountScopeUsageAccount:
		return p.UsageAccountID != "" && usageAccountID == p.UsageAccountID
	default:
		return false
	}
}

// AllowsPayerAccount reports whether payer-level billing records for accountID are visible.
func (p Policy) AllowsPayerAccount(accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false
	}
	switch p.AccountScope {
	case AccountScopeAllAccounts:
		return true
	case AccountScopePayerAccount:
		return accountID == p.PayerAccountID
	default:
		return false
	}
}

// AllowsUsageAccount reports whether usage-level billing records for accountID are visible.
func (p Policy) AllowsUsageAccount(accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false
	}
	switch p.AccountScope {
	case AccountScopeAllAccounts, AccountScopePayerAccount:
		return true
	case AccountScopeUsageAccount:
		return accountID == p.UsageAccountID
	default:
		return false
	}
}

// PayerAccountFilter returns the SQL-ready payer filter required by payer-scoped policies.
func (p Policy) PayerAccountFilter() (string, bool) {
	if p.AccountScope != AccountScopePayerAccount || p.PayerAccountID == "" {
		return "", false
	}
	return p.PayerAccountID, true
}

// UsageAccountFilter returns the SQL-ready usage-account filter required by member policies.
func (p Policy) UsageAccountFilter() (string, bool) {
	if p.AccountScope != AccountScopeUsageAccount || p.UsageAccountID == "" {
		return "", false
	}
	return p.UsageAccountID, true
}
