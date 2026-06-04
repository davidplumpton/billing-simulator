package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

// defaultBillingPayerAccountID resolves the payer learners should see before they override forms.
func defaultBillingPayerAccountID(ctx context.Context, db *sql.DB, usageAccountID string) (string, error) {
	fallback := persistence.AnyCompanyRetailManagementAccountID
	if db == nil {
		return fallback, nil
	}

	organizations := persistence.NewOrganizationRepository(db)
	usageAccountID = strings.TrimSpace(usageAccountID)
	if usageAccountID != "" {
		account, err := organizations.GetAccount(ctx, usageAccountID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("resolve payer for usage account %q: %w", usageAccountID, err)
		}
		if strings.TrimSpace(account.PayerAccountID) != "" {
			return strings.TrimSpace(account.PayerAccountID), nil
		}
	}

	organization, err := organizations.GetOrganizationByTemplate(ctx, persistence.AnyCompanyRetailTemplateKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fallback, nil
		}
		return "", fmt.Errorf("resolve default organization payer: %w", err)
	}
	if strings.TrimSpace(organization.ManagementAccountID) != "" {
		return strings.TrimSpace(organization.ManagementAccountID), nil
	}
	return fallback, nil
}

// payerAccountIDOrDefault preserves explicit payer overrides and fills blank form submissions.
func payerAccountIDOrDefault(ctx context.Context, db *sql.DB, payerAccountID string) (string, error) {
	payerAccountID = strings.TrimSpace(payerAccountID)
	if payerAccountID != "" {
		return payerAccountID, nil
	}
	return defaultBillingPayerAccountID(ctx, db, defaultUsageAccountID)
}
