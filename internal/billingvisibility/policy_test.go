package billingvisibility

import (
	"strings"
	"testing"
)

func TestPolicyForViewerDefinesManagementMemberFinanceAndInstructorViews(t *testing.T) {
	t.Parallel()

	management, err := PolicyForViewer(Viewer{
		Role:      RoleManagementAccount,
		AccountID: " 999988887777 ",
	})
	if err != nil {
		t.Fatalf("PolicyForViewer(management) error = %v", err)
	}
	if management.Type != PolicyTypeManagementConsolidated ||
		management.AccountScope != AccountScopePayerAccount ||
		management.PayerAccountID != "999988887777" ||
		management.DocumentAccess != DocumentAccessFinancial {
		t.Fatalf("management policy = %+v, want consolidated payer policy", management)
	}
	if !management.AllowsBillingRow(BillingRow{PayerAccountID: "999988887777", UsageAccountID: "111122223333"}) ||
		!management.AllowsBillingRow(BillingRow{PayerAccountID: "999988887777", UsageAccountID: "222233334444"}) ||
		management.AllowsBillingRow(BillingRow{PayerAccountID: "000000000000", UsageAccountID: "111122223333"}) {
		t.Fatalf("management row access did not match payer-account consolidated visibility")
	}
	if !management.AllowsView(ViewInvoices) ||
		!management.AllowsView(ViewPayments) ||
		!management.AllowsView(ViewBillingAccess) ||
		management.AllowsView(ViewScenarioReview) {
		t.Fatalf("management view access = %+v, want invoices, payments, billing access, and no scenario review", management)
	}
	if payerAccountID, ok := management.PayerAccountFilter(); !ok || payerAccountID != "999988887777" {
		t.Fatalf("management payer filter = %q, %v", payerAccountID, ok)
	}

	member, err := PolicyForViewer(Viewer{
		Role:      RoleMemberAccount,
		AccountID: "111122223333",
	})
	if err != nil {
		t.Fatalf("PolicyForViewer(member) error = %v", err)
	}
	if member.Type != PolicyTypeMemberInformational ||
		member.AccountScope != AccountScopeUsageAccount ||
		member.UsageAccountID != "111122223333" ||
		member.DocumentAccess != DocumentAccessInformational {
		t.Fatalf("member policy = %+v, want account-scoped informational policy", member)
	}
	if !member.AllowsBillingRow(BillingRow{PayerAccountID: "999988887777", UsageAccountID: "111122223333"}) ||
		member.AllowsBillingRow(BillingRow{PayerAccountID: "999988887777", UsageAccountID: "222233334444"}) ||
		member.AllowsPayerAccount("999988887777") {
		t.Fatalf("member row access did not match usage-account informational visibility")
	}
	if !member.AllowsView(ViewBills) ||
		!member.AllowsView(ViewCostExplorer) ||
		!member.AllowsView(ViewExports) ||
		member.AllowsView(ViewInvoices) ||
		member.AllowsView(ViewPayments) ||
		member.AllowsView(ViewBillingAccess) {
		t.Fatalf("member view access = %+v, want own bills/reports/exports only", member)
	}
	if usageAccountID, ok := member.UsageAccountFilter(); !ok || usageAccountID != "111122223333" {
		t.Fatalf("member usage filter = %q, %v", usageAccountID, ok)
	}

	finance, err := PolicyForViewer(Viewer{
		Role:                RoleFinance,
		ManagementAccountID: "999988887777",
	})
	if err != nil {
		t.Fatalf("PolicyForViewer(finance) error = %v", err)
	}
	if finance.Type != PolicyTypeFinanceConsolidated ||
		!finance.AllowsBillingRow(BillingRow{PayerAccountID: "999988887777", UsageAccountID: "222233334444"}) ||
		!finance.AllowsView(ViewInvoices) ||
		!finance.AllowsView(ViewPayments) ||
		finance.AllowsView(ViewBillingAccess) ||
		finance.AllowsView(ViewScenarioReview) {
		t.Fatalf("finance policy = %+v, want consolidated financial access without admin/scenario views", finance)
	}

	instructor, err := PolicyForViewer(Viewer{Role: RoleInstructor})
	if err != nil {
		t.Fatalf("PolicyForViewer(instructor) error = %v", err)
	}
	if instructor.Type != PolicyTypeInstructorAllAccounts ||
		instructor.AccountScope != AccountScopeAllAccounts ||
		!instructor.AllowsBillingRow(BillingRow{PayerAccountID: "999988887777", UsageAccountID: "111122223333"}) ||
		!instructor.AllowsBillingRow(BillingRow{PayerAccountID: "000000000000", UsageAccountID: "222233334444"}) ||
		!instructor.AllowsView(ViewScenarioReview) ||
		!instructor.AllowsView(ViewBillingAccess) {
		t.Fatalf("instructor policy = %+v, want all-account instructor visibility", instructor)
	}
	if payerAccountID, ok := instructor.PayerAccountFilter(); ok || payerAccountID != "" {
		t.Fatalf("instructor payer filter = %q, %v, want no payer filter", payerAccountID, ok)
	}
}

func TestPolicyForViewerRejectsUnsupportedOrUnderspecifiedRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		viewer  Viewer
		wantErr string
	}{
		{
			name:    "unknown role",
			viewer:  Viewer{Role: Role("platform-engineer"), AccountID: "111122223333"},
			wantErr: "unsupported billing role",
		},
		{
			name:    "management account missing account",
			viewer:  Viewer{Role: RoleManagementAccount},
			wantErr: "management-account billing role requires account_id",
		},
		{
			name:    "member account missing account",
			viewer:  Viewer{Role: RoleMemberAccount},
			wantErr: "member-account billing role requires account_id",
		},
		{
			name:    "finance missing management account",
			viewer:  Viewer{Role: RoleFinance},
			wantErr: "finance billing role requires management_account_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := PolicyForViewer(tt.viewer)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PolicyForViewer() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseRoleTrimsStableSerializedNames(t *testing.T) {
	t.Parallel()

	role, err := ParseRole(" member-account ")
	if err != nil {
		t.Fatalf("ParseRole() error = %v", err)
	}
	if role != RoleMemberAccount || role.String() != "member-account" || !role.Valid() {
		t.Fatalf("role = %q, want stable member-account role", role)
	}
}
