package app

import (
	"context"

	"aws-billing-simulator/internal/persistence"
)

// anyCompanyRetailActiveMemberAccountLabels loads active member-account labels for billing form pickers.
func anyCompanyRetailActiveMemberAccountLabels(ctx context.Context, organization persistence.OrganizationRepository) (map[string]string, error) {
	tenant, err := organization.GetOrganizationByTemplate(ctx, persistence.AnyCompanyRetailTemplateKey)
	if err != nil {
		return nil, err
	}
	accounts, err := organization.ListAccounts(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	for _, account := range accounts {
		if account.IsManagementAccount || account.Status != persistence.AccountStatusActive {
			continue
		}
		labels[account.ID] = account.Name + " (" + account.ID + ")"
	}
	return labels, nil
}
