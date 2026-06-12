package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
	"aws-billing-simulator/internal/persistence"
)

func (h exportsHandler) exportPolicyFromValues(ctx context.Context, values url.Values) (billingvisibility.Policy, error) {
	resolution, err := resolveViewerPolicy(ctx, h.db, exportViewerFieldsFromValues(values), viewerPolicyResolveOptions{
		DefaultRole:  billingvisibility.RoleManagementAccount,
		RequiredView: billingvisibility.ViewExports,
		PermissionErr: func(policy billingvisibility.Policy) error {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot view exports", policy.Role)}
		},
	})
	if err != nil {
		return billingvisibility.Policy{}, err
	}
	return resolution.Policy, nil
}

func (h exportsHandler) scopedExportFileListRequest(ctx context.Context, request persistence.ExportFileListRequest, policy billingvisibility.Policy) (persistence.ExportFileListRequest, error) {
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return persistence.ExportFileListRequest{}, err
	}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		if request.PayerAccountID != "" && request.PayerAccountID != payerAccountID {
			return persistence.ExportFileListRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot list exports for payer account %q", policy.Role, request.PayerAccountID)}
		}
		request.PayerAccountID = payerAccountID
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		if request.PayerAccountID != "" && defaultPayerAccountID != "" && request.PayerAccountID != defaultPayerAccountID {
			return persistence.ExportFileListRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot list exports for payer account %q", policy.Role, request.PayerAccountID)}
		}
		if request.UsageAccountID != "" && request.UsageAccountID != usageAccountID {
			return persistence.ExportFileListRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot list exports for usage account %q", policy.Role, request.UsageAccountID)}
		}
		if request.PayerAccountID == "" {
			request.PayerAccountID = defaultPayerAccountID
		}
		request.UsageAccountID = usageAccountID
	}
	return request, nil
}

func (h exportsHandler) scopedCURCSVExportRequest(ctx context.Context, request persistence.CURCSVExportRequest, policy billingvisibility.Policy) (persistence.CURCSVExportRequest, error) {
	defaultPayerAccountID, err := defaultBillingPayerAccountID(ctx, h.db, "")
	if err != nil {
		return persistence.CURCSVExportRequest{}, err
	}
	request.Visibility.PayerAccountID = strings.TrimSpace(request.Visibility.PayerAccountID)
	request.Visibility.UsageAccountID = strings.TrimSpace(request.Visibility.UsageAccountID)
	if request.Visibility.PayerAccountID != "" && request.Visibility.UsageAccountID != "" {
		return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("CUR export visibility cannot be scoped to both payer and usage accounts")}
	}
	if request.Visibility.UsageAccountID != "" {
		if !policy.AllowsUsageAccount(request.Visibility.UsageAccountID) {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export usage account %q", policy.Role, request.Visibility.UsageAccountID)}
		}
		if request.UsageAccountID != "" && request.UsageAccountID != request.Visibility.UsageAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export usage account %q", policy.Role, request.UsageAccountID)}
		}
		request.UsageAccountID = request.Visibility.UsageAccountID
		request.Visibility = persistence.BillingVisibilityFilter{UsageAccountID: request.Visibility.UsageAccountID}
	}
	if request.Visibility.PayerAccountID != "" {
		if !policy.AllowsPayerAccount(request.Visibility.PayerAccountID) {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.Visibility.PayerAccountID)}
		}
		if request.PayerAccountID != "" && request.PayerAccountID != request.Visibility.PayerAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.PayerAccountID)}
		}
		request.PayerAccountID = request.Visibility.PayerAccountID
		request.Visibility = persistence.BillingVisibilityFilter{PayerAccountID: request.Visibility.PayerAccountID}
	}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		if request.PayerAccountID != "" && request.PayerAccountID != payerAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.PayerAccountID)}
		}
		request.PayerAccountID = payerAccountID
		if request.Visibility.UsageAccountID == "" {
			request.Visibility = persistence.BillingVisibilityFilter{PayerAccountID: payerAccountID}
		}
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		if request.Visibility.PayerAccountID != "" {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.Visibility.PayerAccountID)}
		}
		if request.PayerAccountID != "" && defaultPayerAccountID != "" && request.PayerAccountID != defaultPayerAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export payer account %q", policy.Role, request.PayerAccountID)}
		}
		if request.UsageAccountID != "" && request.UsageAccountID != usageAccountID {
			return persistence.CURCSVExportRequest{}, exportAccessError{err: fmt.Errorf("billing role %q cannot export usage account %q", policy.Role, request.UsageAccountID)}
		}
		if request.PayerAccountID == "" {
			request.PayerAccountID = defaultPayerAccountID
		}
		request.UsageAccountID = usageAccountID
		request.Visibility = persistence.BillingVisibilityFilter{UsageAccountID: usageAccountID}
	}
	return request, nil
}

func (h exportsHandler) scopedCURExportReconciliationRequest(ctx context.Context, request persistence.CURExportReconciliationRequest, policy billingvisibility.Policy) (persistence.CURExportReconciliationRequest, error) {
	csvRequest, err := h.scopedCURCSVExportRequest(ctx, persistence.CURCSVExportRequest{
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
		PayerAccountID:     request.PayerAccountID,
		UsageAccountID:     request.UsageAccountID,
		LineItemStatus:     request.LineItemStatus,
		Limit:              request.Limit,
	}, policy)
	if err != nil {
		return persistence.CURExportReconciliationRequest{}, err
	}
	request.PayerAccountID = csvRequest.PayerAccountID
	request.UsageAccountID = csvRequest.UsageAccountID
	request.Visibility = csvRequest.Visibility
	return request, nil
}

// visibleExportFilesForPolicy removes stored files whose generation scope is broader than the viewer can inspect.
func visibleExportFilesForPolicy(files []persistence.ExportFile, policy billingvisibility.Policy) []persistence.ExportFile {
	visible := make([]persistence.ExportFile, 0, len(files))
	for _, file := range files {
		if err := ensureExportFileVisibleToPolicy(policy, file); err == nil {
			visible = append(visible, file)
		}
	}
	return visible
}

func ensureExportFileVisibleToPolicy(policy billingvisibility.Policy, file persistence.ExportFile) error {
	if !policy.AllowsView(billingvisibility.ViewExports) {
		return exportAccessError{err: fmt.Errorf("billing role %q cannot view exports", policy.Role)}
	}
	if payerAccountID, ok := policy.PayerAccountFilter(); ok {
		if file.PayerAccountID != payerAccountID {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access export for payer account %q", policy.Role, file.PayerAccountID)}
		}
		return nil
	}
	if usageAccountID, ok := policy.UsageAccountFilter(); ok {
		if file.UsageAccountID == "" {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access all-account exports", policy.Role)}
		}
		if file.UsageAccountID != usageAccountID {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access export for usage account %q", policy.Role, file.UsageAccountID)}
		}
		scope, accountID, err := exportFileVisibilityScope(file)
		if err != nil {
			return exportAccessError{err: err}
		}
		if scope != exportVisibilityScopeUsageAccount || accountID != usageAccountID {
			return exportAccessError{err: fmt.Errorf("billing role %q cannot access export generated outside usage account scope %q", policy.Role, usageAccountID)}
		}
		return nil
	}
	return nil
}
