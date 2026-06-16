package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func validateProFormaAccountExists(ctx context.Context, db *sql.DB, accountID string) error {
	var found string
	err := db.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id = ?`, accountID).Scan(&found)
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("account %q does not exist", accountID)
	}
	return fmt.Errorf("validate pro forma account %q: %w", accountID, err)
}

func normalizeProFormaPricingPlanCreateRequest(request ProFormaPricingPlanCreateRequest) ProFormaPricingPlanCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.CurrencyCode = strings.ToUpper(strings.TrimSpace(request.CurrencyCode))
	if request.CurrencyCode == "" {
		request.CurrencyCode = proFormaDefaultCurrency
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = proFormaStatusActive
	}
	return request
}

func validateProFormaPricingPlanCreateRequest(request ProFormaPricingPlanCreateRequest) error {
	if request.Name == "" {
		return fmt.Errorf("pro forma pricing plan name is required")
	}
	if len(request.CurrencyCode) != 3 {
		return fmt.Errorf("pro forma pricing plan currency code must be three characters")
	}
	if !isProFormaStatus(request.Status) {
		return fmt.Errorf("unsupported pro forma pricing plan status %q", request.Status)
	}
	return nil
}

func normalizeProFormaPricingRuleCreateRequest(request ProFormaPricingRuleCreateRequest) ProFormaPricingRuleCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.PricingPlanID = strings.TrimSpace(request.PricingPlanID)
	request.ServiceCode = strings.TrimSpace(request.ServiceCode)
	request.Description = strings.TrimSpace(request.Description)
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = proFormaStatusActive
	}
	return request
}

func validateProFormaPricingRuleCreateRequest(request ProFormaPricingRuleCreateRequest) error {
	if request.PricingPlanID == "" {
		return fmt.Errorf("pro forma pricing plan ID is required")
	}
	if request.ServiceCode == "" {
		return fmt.Errorf("service code is required")
	}
	if request.RateMultiplierBasisPoints <= 0 || request.RateMultiplierBasisPoints > proFormaMaxMultiplierBPS {
		return fmt.Errorf("rate multiplier basis points must be greater than zero and at most %d", proFormaMaxMultiplierBPS)
	}
	if !isProFormaStatus(request.Status) {
		return fmt.Errorf("unsupported pro forma pricing rule status %q", request.Status)
	}
	return nil
}

func normalizeProFormaBillingGroupCreateRequest(request ProFormaBillingGroupCreateRequest) ProFormaBillingGroupCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	if request.PayerAccountID == "" {
		request.PayerAccountID = AnyCompanyRetailManagementAccountID
	}
	request.PricingPlanID = strings.TrimSpace(request.PricingPlanID)
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = proFormaStatusActive
	}
	return request
}

func validateProFormaBillingGroupCreateRequest(request ProFormaBillingGroupCreateRequest) error {
	if request.Name == "" {
		return fmt.Errorf("pro forma billing group name is required")
	}
	if err := validateOrganizationAccountID("payer account ID", request.PayerAccountID); err != nil {
		return err
	}
	if request.PricingPlanID == "" {
		return fmt.Errorf("pro forma pricing plan ID is required")
	}
	if !isProFormaStatus(request.Status) {
		return fmt.Errorf("unsupported pro forma billing group status %q", request.Status)
	}
	return nil
}

func normalizeProFormaBillingGroupAccountCreateRequest(request ProFormaBillingGroupAccountCreateRequest) ProFormaBillingGroupAccountCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.AccountID = strings.TrimSpace(request.AccountID)
	return request
}

func validateProFormaBillingGroupAccountCreateRequest(request ProFormaBillingGroupAccountCreateRequest) error {
	if request.BillingGroupID == "" {
		return fmt.Errorf("pro forma billing group ID is required")
	}
	return validateOrganizationAccountID("account ID", request.AccountID)
}

func normalizeProFormaCustomLineItemCreateRequest(request ProFormaCustomLineItemCreateRequest) ProFormaCustomLineItemCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.LineItemType = strings.ToLower(strings.TrimSpace(request.LineItemType))
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.CurrencyCode = strings.ToUpper(strings.TrimSpace(request.CurrencyCode))
	if request.CurrencyCode == "" {
		request.CurrencyCode = proFormaDefaultCurrency
	}
	return request
}

func validateProFormaCustomLineItemCreateRequest(request ProFormaCustomLineItemCreateRequest) error {
	if request.BillingGroupID == "" {
		return fmt.Errorf("pro forma billing group ID is required")
	}
	if err := validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return err
	}
	if !isProFormaCustomLineItemType(request.LineItemType) {
		return fmt.Errorf("unsupported pro forma custom line item type %q", request.LineItemType)
	}
	if request.Name == "" {
		return fmt.Errorf("pro forma custom line item name is required")
	}
	if len(request.CurrencyCode) != 3 {
		return fmt.Errorf("pro forma custom line item currency code must be three characters")
	}
	switch request.LineItemType {
	case ProFormaCustomLineItemTypeFee, ProFormaCustomLineItemTypeMarkup:
		if request.AmountMicros <= 0 {
			return fmt.Errorf("pro forma %s amount must be greater than zero", request.LineItemType)
		}
	case ProFormaCustomLineItemTypeCredit:
		if request.AmountMicros >= 0 {
			return fmt.Errorf("pro forma credit amount must be less than zero")
		}
	case ProFormaCustomLineItemTypeAnnotation:
		if request.AmountMicros != 0 {
			return fmt.Errorf("pro forma annotation amount must be zero")
		}
	}
	return nil
}

func normalizeProFormaRefreshRequest(request ProFormaRefreshRequest) ProFormaRefreshRequest {
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	return request
}

func validateProFormaRefreshRequest(request ProFormaRefreshRequest) error {
	if err := validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return err
	}
	if request.PayerAccountID != "" {
		return validateOrganizationAccountID("payer account ID", request.PayerAccountID)
	}
	return nil
}

func normalizeProFormaSummaryRequest(request ProFormaSummaryRequest) ProFormaSummaryRequest {
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	return request
}

func validateProFormaSummaryRequest(request ProFormaSummaryRequest) error {
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func normalizeProFormaLineItemListRequest(request ProFormaLineItemListRequest) ProFormaLineItemListRequest {
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	if request.Limit <= 0 {
		request.Limit = defaultProFormaLineItemLimit
	}
	if request.Limit > maxProFormaLineItemLimit {
		request.Limit = maxProFormaLineItemLimit
	}
	return request
}

func validateProFormaLineItemListRequest(request ProFormaLineItemListRequest) error {
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func normalizeProFormaCustomLineItemListRequest(request ProFormaCustomLineItemListRequest) ProFormaCustomLineItemListRequest {
	request.BillingGroupID = strings.TrimSpace(request.BillingGroupID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	if request.Limit <= 0 {
		request.Limit = defaultProFormaLineItemLimit
	}
	if request.Limit > maxProFormaLineItemLimit {
		request.Limit = maxProFormaLineItemLimit
	}
	return request
}

func validateProFormaCustomLineItemListRequest(request ProFormaCustomLineItemListRequest) error {
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func isProFormaStatus(status string) bool {
	switch status {
	case "active", "archived":
		return true
	default:
		return false
	}
}

func isProFormaCustomLineItemType(lineItemType string) bool {
	switch lineItemType {
	case ProFormaCustomLineItemTypeFee, ProFormaCustomLineItemTypeCredit, ProFormaCustomLineItemTypeMarkup, ProFormaCustomLineItemTypeAnnotation:
		return true
	default:
		return false
	}
}
