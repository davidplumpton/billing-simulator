package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestPaymentProfileRepositoryListsSeededDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPaymentProfileRepository(db)

	sellers, err := repo.ListSellerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSellerProfiles() error = %v", err)
	}
	if len(sellers) != 1 ||
		sellers[0].ID != "seller_aws_billing_simulator" ||
		sellers[0].SellerOfRecord != defaultInvoiceSellerOfRecord ||
		sellers[0].Status != paymentSellerProfileStatusActive {
		t.Fatalf("seeded seller profiles = %+v, want simulator seller", sellers)
	}

	profiles, err := repo.ListPaymentProfiles(ctx, AnyCompanyRetailManagementAccountID)
	if err != nil {
		t.Fatalf("ListPaymentProfiles() error = %v", err)
	}
	if len(profiles) != 1 ||
		profiles[0].ID != "payprof_anycompany_retail_management" ||
		!profiles[0].IsDefault ||
		profiles[0].BillToName != defaultInvoiceBillToName {
		t.Fatalf("seeded payment profiles = %+v, want default AnyCompany Retail profile", profiles)
	}

	methods, err := repo.ListPaymentMethods(ctx, profiles[0].ID)
	if err != nil {
		t.Fatalf("ListPaymentMethods() error = %v", err)
	}
	if len(methods) != 1 ||
		methods[0].MethodType != paymentMethodTypeInvoiceRemittance ||
		!methods[0].IsDefault ||
		methods[0].RemittanceDestination == "" {
		t.Fatalf("seeded payment methods = %+v, want default invoice remittance", methods)
	}

	details, found, err := repo.GetDefaultPaymentProfileForPayer(ctx, AnyCompanyRetailManagementAccountID, "usd")
	if err != nil {
		t.Fatalf("GetDefaultPaymentProfileForPayer() error = %v", err)
	}
	if !found ||
		details.Profile.ID != profiles[0].ID ||
		details.SellerProfile.ID != sellers[0].ID ||
		!details.HasDefaultMethod ||
		details.DefaultMethod.ID != methods[0].ID {
		t.Fatalf("default payment profile details = %+v found %v, want seeded profile and method", details, found)
	}
}

func TestPaymentProfileRepositoryCreatesSupportedMethodsAndSwitchesDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPaymentProfileRepository(db)
	seller := createPaymentSellerProfileForTest(t, ctx, repo, "seller_supported_methods")
	profile, err := repo.CreatePaymentProfile(ctx, PaymentProfileCreateRequest{
		ID:              "payprof_supported_methods",
		PayerAccountID:  "111122223333",
		SellerProfileID: seller.ID,
		ProfileName:     "Storefront payment profile",
		BillToName:      "Storefront Team",
		BillToEmail:     "storefront-billing@anycompany.example",
		BillToAddress:   "200 AnyCompany Way, Example City",
	})
	if err != nil {
		t.Fatalf("CreatePaymentProfile() error = %v", err)
	}
	if !profile.IsDefault || profile.CurrencyCode != defaultBillCurrencyCode {
		t.Fatalf("created payment profile = %+v, want auto-default USD profile", profile)
	}

	card, err := repo.CreatePaymentMethod(ctx, PaymentMethodCreateRequest{
		ID:               "paymeth_supported_card",
		PaymentProfileID: profile.ID,
		MethodType:       paymentMethodTypeCard,
		DisplayName:      "Corporate Visa 4242",
		IsDefault:        true,
		CardBrand:        "Visa",
		AccountLast4:     "4242",
		ExpirationMonth:  12,
		ExpirationYear:   2030,
	})
	if err != nil {
		t.Fatalf("CreatePaymentMethod(card) error = %v", err)
	}
	ach, err := repo.CreatePaymentMethod(ctx, PaymentMethodCreateRequest{
		ID:               "paymeth_supported_ach",
		PaymentProfileID: profile.ID,
		MethodType:       paymentMethodTypeACH,
		DisplayName:      "Operations ACH",
		BankName:         "AnyCompany Bank",
		AccountLast4:     "6789",
	})
	if err != nil {
		t.Fatalf("CreatePaymentMethod(ACH) error = %v", err)
	}
	remittance, err := repo.CreatePaymentMethod(ctx, PaymentMethodCreateRequest{
		ID:                    "paymeth_supported_remittance",
		PaymentProfileID:      profile.ID,
		MethodType:            paymentMethodTypeInvoiceRemittance,
		DisplayName:           "Invoice remittance",
		RemittanceDestination: "Synthetic remittance desk",
	})
	if err != nil {
		t.Fatalf("CreatePaymentMethod(invoice remittance) error = %v", err)
	}
	advancePay, err := repo.CreatePaymentMethod(ctx, PaymentMethodCreateRequest{
		ID:                      "paymeth_supported_advance_pay",
		PaymentProfileID:        profile.ID,
		MethodType:              paymentMethodTypeAdvancePayBalance,
		DisplayName:             "Advance Pay balance",
		AdvancePayBalanceMicros: 5_000_000,
	})
	if err != nil {
		t.Fatalf("CreatePaymentMethod(Advance Pay) error = %v", err)
	}

	methods, err := repo.ListPaymentMethods(ctx, profile.ID)
	if err != nil {
		t.Fatalf("ListPaymentMethods() error = %v", err)
	}
	if len(methods) != 4 ||
		methods[0].ID != card.ID ||
		!methods[0].IsDefault ||
		requirePaymentMethodByID(t, methods, remittance.ID).MethodType != paymentMethodTypeInvoiceRemittance ||
		requirePaymentMethodByID(t, methods, advancePay.ID).AdvancePayBalanceMicros != 5_000_000 {
		t.Fatalf("payment methods = %+v, want card default plus ACH/remittance/Advance Pay", methods)
	}

	newDefault, err := repo.SetDefaultPaymentMethod(ctx, ach.ID)
	if err != nil {
		t.Fatalf("SetDefaultPaymentMethod() error = %v", err)
	}
	if !newDefault.IsDefault {
		t.Fatalf("SetDefaultPaymentMethod() = %+v, want ACH default", newDefault)
	}
	methods, err = repo.ListPaymentMethods(ctx, profile.ID)
	if err != nil {
		t.Fatalf("ListPaymentMethods(after default switch) error = %v", err)
	}
	if methods[0].ID != ach.ID || countDefaultPaymentMethods(methods) != 1 {
		t.Fatalf("payment methods after default switch = %+v, want ACH as only default", methods)
	}

	details, found, err := repo.GetDefaultPaymentProfileForPayer(ctx, profile.PayerAccountID, profile.CurrencyCode)
	if err != nil {
		t.Fatalf("GetDefaultPaymentProfileForPayer() error = %v", err)
	}
	if !found || details.Profile.ID != profile.ID || !details.HasDefaultMethod || details.DefaultMethod.ID != ach.ID {
		t.Fatalf("default payment details = %+v found %v, want profile with ACH default", details, found)
	}
}

func TestPaymentProfileRepositoryRejectsInvalidMethods(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPaymentProfileRepository(db)
	seller := createPaymentSellerProfileForTest(t, ctx, repo, "seller_invalid_methods")
	profile, err := repo.CreatePaymentProfile(ctx, PaymentProfileCreateRequest{
		ID:              "payprof_invalid_methods",
		PayerAccountID:  "222233334444",
		SellerProfileID: seller.ID,
		ProfileName:     "Invalid method profile",
		BillToName:      "Shared Networking",
		BillToEmail:     "shared-networking-billing@anycompany.example",
		BillToAddress:   "300 AnyCompany Way, Example City",
	})
	if err != nil {
		t.Fatalf("CreatePaymentProfile() error = %v", err)
	}

	_, err = repo.CreatePaymentMethod(ctx, PaymentMethodCreateRequest{
		PaymentProfileID: profile.ID,
		MethodType:       paymentMethodTypeCard,
		DisplayName:      "Broken card",
		CardBrand:        "Visa",
		ExpirationMonth:  10,
		ExpirationYear:   2030,
	})
	if err == nil || !strings.Contains(err.Error(), "card last4") {
		t.Fatalf("CreatePaymentMethod(invalid card) error = %v, want card last4 validation", err)
	}

	_, err = repo.CreatePaymentMethod(ctx, PaymentMethodCreateRequest{
		PaymentProfileID:      profile.ID,
		MethodType:            paymentMethodTypeInvoiceRemittance,
		DisplayName:           "Inactive remittance",
		Status:                paymentMethodStatusInactive,
		IsDefault:             true,
		RemittanceDestination: "Synthetic remittance desk",
	})
	if err == nil || !strings.Contains(err.Error(), "default payment method must be active") {
		t.Fatalf("CreatePaymentMethod(inactive default) error = %v, want default active validation", err)
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO payment_methods (
			id,
			payment_profile_id,
			method_type,
			display_name,
			status,
			is_default,
			currency_code,
			remittance_destination
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"paymeth_invalid_schema_default",
		profile.ID,
		paymentMethodTypeInvoiceRemittance,
		"Schema invalid default",
		paymentMethodStatusInactive,
		1,
		defaultBillCurrencyCode,
		"Synthetic remittance desk",
	); err == nil {
		t.Fatal("direct insert inactive default method error = nil, want schema rejection")
	}
}

func TestInvoiceDocumentSnapshotsDefaultPaymentProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	profileRepo := NewPaymentProfileRepository(db)
	seller, err := profileRepo.CreateSellerProfile(ctx, PaymentSellerProfileCreateRequest{
		ID:                    "seller_invoice_snapshot",
		SellerOfRecord:        "AnyCompany Seller Lab",
		SellerAddress:         "1 Seller Profile Way, Example City",
		SellerTaxRegistration: "SELLER-TAX-1",
	})
	if err != nil {
		t.Fatalf("CreateSellerProfile() error = %v", err)
	}
	profile, err := profileRepo.CreatePaymentProfile(ctx, PaymentProfileCreateRequest{
		ID:                    "payprof_invoice_snapshot",
		PayerAccountID:        AnyCompanyRetailManagementAccountID,
		SellerProfileID:       seller.ID,
		ProfileName:           "Invoice snapshot profile",
		BillToName:            "AnyCompany Finance",
		BillToEmail:           "finops@anycompany.example",
		BillToAddress:         "400 AnyCompany Way, Example City",
		BillToTaxRegistration: "BILLTO-TAX-1",
		IsDefault:             true,
	})
	if err != nil {
		t.Fatalf("CreatePaymentProfile() error = %v", err)
	}
	if !profile.IsDefault {
		t.Fatalf("custom payment profile = %+v, want default", profile)
	}

	usageRepo := NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-payment-profile-invoice",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-payment-profile-invoice",
		ResourceID:          "resource-payment-profile-invoice",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T01:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	result, err := NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: AnyCompanyRetailManagementAccountID,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}
	document := result.InvoiceDocument
	if document.SellerOfRecord != seller.SellerOfRecord ||
		document.SellerAddress != seller.SellerAddress ||
		document.SellerTaxRegistration != seller.SellerTaxRegistration ||
		document.BillToName != profile.BillToName ||
		document.BillToEmail != profile.BillToEmail ||
		document.BillToAddress != profile.BillToAddress ||
		document.BillToTaxRegistration != profile.BillToTaxRegistration {
		t.Fatalf("invoice document profile fields = %+v, want seller %+v and profile %+v", document, seller, profile)
	}
}

func createPaymentSellerProfileForTest(t *testing.T, ctx context.Context, repo PaymentProfileRepository, id string) PaymentSellerProfile {
	t.Helper()

	seller, err := repo.CreateSellerProfile(ctx, PaymentSellerProfileCreateRequest{
		ID:                     id,
		SellerOfRecord:         id + " seller",
		SellerAddress:          "100 Seller Way, Example City",
		RemittanceInstructions: "Synthetic remittance only",
	})
	if err != nil {
		t.Fatalf("CreateSellerProfile(%s) error = %v", id, err)
	}
	return seller
}

func requirePaymentMethodByID(t *testing.T, methods []PaymentMethod, id string) PaymentMethod {
	t.Helper()

	for _, method := range methods {
		if method.ID == id {
			return method
		}
	}
	t.Fatalf("payment methods = %+v, want method %s", methods, id)
	return PaymentMethod{}
}

func countDefaultPaymentMethods(methods []PaymentMethod) int {
	var count int
	for _, method := range methods {
		if method.IsDefault {
			count++
		}
	}
	return count
}
