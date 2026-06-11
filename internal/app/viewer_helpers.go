package app

import (
	"net/url"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
)

func billsViewerRoleSelect(selected string) uiSelectFieldView {
	return viewerRoleSelect(selected, "All viewers")
}

func exportsViewerRoleSelect(selected string) uiSelectFieldView {
	return viewerRoleSelect(selected, "Default viewer")
}

func viewerRoleSelect(selected, defaultLabel string) uiSelectFieldView {
	options := []uiSelectOptionView{
		{Value: "", Label: defaultLabel},
		{Value: billingvisibility.RoleManagementAccount.String(), Label: "Management"},
		{Value: billingvisibility.RoleFinance.String(), Label: "Finance"},
		{Value: billingvisibility.RoleMemberAccount.String(), Label: "Member"},
		{Value: billingvisibility.RoleInstructor.String(), Label: "Instructor"},
	}
	for idx := range options {
		options[idx].Selected = options[idx].Value == selected
	}
	return uiSelectFieldView{
		Label:   "Viewer Role",
		Name:    "viewer_role",
		Options: options,
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
