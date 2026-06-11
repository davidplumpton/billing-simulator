package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
	"aws-billing-simulator/internal/persistence"
)

type viewerPolicyResolution struct {
	Policy billingvisibility.Policy
	Scoped bool
}

// viewerPolicyResolveOptions describes workflow-specific viewer defaulting and access checks.
type viewerPolicyResolveOptions struct {
	AllowUnscoped bool
	DefaultRole   billingvisibility.Role
	RequiredView  billingvisibility.View
	PermissionErr func(billingvisibility.Policy) error
}

// resolveViewerPolicy turns simulated viewer controls into the shared billing visibility policy.
func resolveViewerPolicy(ctx context.Context, db *sql.DB, viewer exportViewerFields, options viewerPolicyResolveOptions) (viewerPolicyResolution, error) {
	roleValue := strings.TrimSpace(viewer.Role)
	accountID := strings.TrimSpace(viewer.AccountID)
	if roleValue == "" && accountID == "" && options.AllowUnscoped {
		return viewerPolicyResolution{}, nil
	}
	if roleValue == "" && accountID != "" {
		return viewerPolicyResolution{}, fmt.Errorf("viewer role is required when viewer account ID is set")
	}
	if roleValue == "" && options.DefaultRole != "" {
		roleValue = options.DefaultRole.String()
	}
	role, err := billingvisibility.ParseRole(roleValue)
	if err != nil {
		return viewerPolicyResolution{}, err
	}
	managementAccountID, err := defaultBillingPayerAccountID(ctx, db, "")
	if err != nil {
		return viewerPolicyResolution{}, err
	}
	if (role == billingvisibility.RoleManagementAccount || role == billingvisibility.RoleFinance) && accountID == "" {
		accountID = managementAccountID
	}
	policy, err := billingvisibility.PolicyForViewer(billingvisibility.Viewer{
		Role:                role,
		AccountID:           accountID,
		ManagementAccountID: managementAccountID,
	})
	if err != nil {
		return viewerPolicyResolution{}, err
	}
	if options.RequiredView != "" && !policy.AllowsView(options.RequiredView) {
		if options.PermissionErr != nil {
			return viewerPolicyResolution{}, options.PermissionErr(policy)
		}
		return viewerPolicyResolution{}, fmt.Errorf("billing role %q cannot view %s", policy.Role, options.RequiredView)
	}
	return viewerPolicyResolution{Policy: policy, Scoped: true}, nil
}

// billingVisibilityFilterFromPolicy converts a resolved policy into repository row constraints.
func billingVisibilityFilterFromPolicy(policy billingvisibility.Policy) persistence.BillingVisibilityFilter {
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		return persistence.BillingVisibilityFilter{PayerAccountID: payerAccountID}
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		return persistence.BillingVisibilityFilter{UsageAccountID: usageAccountID}
	}
	return persistence.BillingVisibilityFilter{}
}

// viewerRoleSelectField builds the shared simulated-viewer role picker.
func viewerRoleSelectField(selected, defaultLabel string) uiSelectFieldView {
	options := append([]uiSelectOptionView{{Value: "", Label: defaultLabel}}, viewerRoleOptions()...)
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
	}
	return uiSelectFieldView{
		Label:   "Viewer Role",
		Name:    "viewer_role",
		Options: options,
	}
}

// viewerAccountIDField builds the shared simulated-viewer account input.
func viewerAccountIDField(accountID string) uiInputFieldView {
	return uiInputField("Viewer Account ID", "viewer_account_id", strings.TrimSpace(accountID), false)
}

// viewerRoleOptions returns the simulator roles in the same order across billing workflows.
func viewerRoleOptions() []uiSelectOptionView {
	return []uiSelectOptionView{
		{Value: billingvisibility.RoleManagementAccount.String(), Label: "Management"},
		{Value: billingvisibility.RoleFinance.String(), Label: "Finance"},
		{Value: billingvisibility.RoleMemberAccount.String(), Label: "Member"},
		{Value: billingvisibility.RoleInstructor.String(), Label: "Instructor"},
	}
}

type exportViewerFields struct {
	Role      string
	AccountID string
}

func exportViewerFieldsFromValues(values url.Values) exportViewerFields {
	return exportViewerFields{
		Role:      strings.TrimSpace(values.Get("viewer_role")),
		AccountID: strings.TrimSpace(values.Get("viewer_account_id")),
	}
}

func exportViewerFieldsFromBillsFilter(filter billsFilterView) exportViewerFields {
	return exportViewerFields{
		Role:      strings.TrimSpace(filter.ViewerRole),
		AccountID: strings.TrimSpace(filter.ViewerAccountID),
	}
}

func (v exportViewerFields) appendToValues(values url.Values) {
	if v.Role != "" {
		values.Set("viewer_role", v.Role)
	}
	if v.AccountID != "" {
		values.Set("viewer_account_id", v.AccountID)
	}
}

func exportsPathWithViewer(viewer exportViewerFields, flash string) string {
	values := url.Values{}
	viewer.appendToValues(values)
	appendQueryValue(values, "flash", flash)
	if len(values) == 0 {
		return "/exports"
	}
	return "/exports?" + values.Encode()
}

func billsPathWithExportViewer(viewer exportViewerFields) string {
	values := url.Values{}
	appendQueryValue(values, "viewer_role", viewer.Role)
	appendQueryValue(values, "viewer_account_id", viewer.AccountID)
	if len(values) == 0 {
		return "/bills"
	}
	return "/bills?" + values.Encode()
}

// paymentsPathWithViewer preserves simulated viewer fields after payment actions.
func paymentsPathWithViewer(viewer exportViewerFields, flash string) string {
	values := url.Values{}
	viewer.appendToValues(values)
	appendQueryValue(values, "flash", flash)
	encoded := values.Encode()
	if encoded == "" {
		return "/payments"
	}
	return "/payments?" + encoded
}
