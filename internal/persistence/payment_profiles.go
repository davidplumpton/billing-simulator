package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	paymentSellerProfileStatusActive   = "active"
	paymentSellerProfileStatusInactive = "inactive"

	paymentProfileStatusActive   = "active"
	paymentProfileStatusInactive = "inactive"

	paymentMethodTypeCard              = "card"
	paymentMethodTypeACH               = "ach"
	paymentMethodTypeInvoiceRemittance = "invoice_remittance"
	paymentMethodTypeAdvancePayBalance = "advance_pay_balance"

	paymentMethodStatusActive   = "active"
	paymentMethodStatusInactive = "inactive"
	paymentMethodStatusFailed   = "failed"
	paymentMethodStatusExpired  = "expired"
)

// PaymentSellerProfile stores the seller-of-record and remittance fields used by payment profiles.
type PaymentSellerProfile struct {
	ID                     string
	SellerOfRecord         string
	SellerAddress          string
	SellerTaxRegistration  string
	RemittanceInstructions string
	CurrencyCode           string
	Status                 string
	CreatedAt              string
	UpdatedAt              string
}

// PaymentProfile stores payer-specific bill-to details and default profile selection.
type PaymentProfile struct {
	ID                    string
	PayerAccountID        string
	SellerProfileID       string
	ProfileName           string
	BillToName            string
	BillToEmail           string
	BillToAddress         string
	BillToTaxRegistration string
	CurrencyCode          string
	Status                string
	IsDefault             bool
	CreatedAt             string
	UpdatedAt             string
}

// PaymentMethod stores one simulated payment instrument or remittance option.
type PaymentMethod struct {
	ID                      string
	PaymentProfileID        string
	MethodType              string
	DisplayName             string
	Status                  string
	IsDefault               bool
	CurrencyCode            string
	CardBrand               string
	AccountLast4            string
	ExpirationMonth         int
	ExpirationYear          int
	BankName                string
	RemittanceDestination   string
	AdvancePayBalanceMicros int64
	FailureReason           string
	CreatedAt               string
	UpdatedAt               string
}

// PaymentProfileDetails combines a profile with its seller and selected default method.
type PaymentProfileDetails struct {
	Profile          PaymentProfile
	SellerProfile    PaymentSellerProfile
	DefaultMethod    PaymentMethod
	HasDefaultMethod bool
}

// PaymentSellerProfileCreateRequest describes a seller-of-record profile to persist.
type PaymentSellerProfileCreateRequest struct {
	ID                     string
	SellerOfRecord         string
	SellerAddress          string
	SellerTaxRegistration  string
	RemittanceInstructions string
	CurrencyCode           string
	Status                 string
}

// PaymentProfileCreateRequest describes payer bill-to settings and default selection.
type PaymentProfileCreateRequest struct {
	ID                    string
	PayerAccountID        string
	SellerProfileID       string
	ProfileName           string
	BillToName            string
	BillToEmail           string
	BillToAddress         string
	BillToTaxRegistration string
	CurrencyCode          string
	Status                string
	IsDefault             bool
}

// PaymentMethodCreateRequest describes one card, ACH, invoice remittance, or Advance Pay balance method.
type PaymentMethodCreateRequest struct {
	ID                      string
	PaymentProfileID        string
	MethodType              string
	DisplayName             string
	Status                  string
	IsDefault               bool
	CurrencyCode            string
	CardBrand               string
	AccountLast4            string
	ExpirationMonth         int
	ExpirationYear          int
	BankName                string
	RemittanceDestination   string
	AdvancePayBalanceMicros int64
	FailureReason           string
}

// PaymentProfileRepository manages simulated payment setup data for invoice and payment workflows.
type PaymentProfileRepository struct {
	db *sql.DB
}

// NewPaymentProfileRepository creates a payment profile repository backed by a workspace database.
func NewPaymentProfileRepository(db *sql.DB) PaymentProfileRepository {
	return PaymentProfileRepository{db: db}
}

// CreateSellerProfile persists a seller-of-record profile for synthetic invoices and remittance.
func (r PaymentProfileRepository) CreateSellerProfile(ctx context.Context, request PaymentSellerProfileCreateRequest) (PaymentSellerProfile, error) {
	if r.db == nil {
		return PaymentSellerProfile{}, fmt.Errorf("database handle is required")
	}
	request = normalizePaymentSellerProfileCreateRequest(request)
	if err := validatePaymentSellerProfileCreateRequest(request); err != nil {
		return PaymentSellerProfile{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("seller")
		if err != nil {
			return PaymentSellerProfile{}, err
		}
		request.ID = id
	}

	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO payment_seller_profiles (
			id,
			seller_of_record,
			seller_address,
			seller_tax_registration,
			remittance_instructions,
			currency_code,
			status
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		request.ID,
		request.SellerOfRecord,
		request.SellerAddress,
		request.SellerTaxRegistration,
		request.RemittanceInstructions,
		request.CurrencyCode,
		request.Status,
	); err != nil {
		return PaymentSellerProfile{}, fmt.Errorf("insert payment seller profile %q: %w", request.ID, err)
	}
	return r.GetSellerProfile(ctx, request.ID)
}

// GetSellerProfile loads one seller-of-record profile by ID.
func (r PaymentProfileRepository) GetSellerProfile(ctx context.Context, id string) (PaymentSellerProfile, error) {
	if r.db == nil {
		return PaymentSellerProfile{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return PaymentSellerProfile{}, fmt.Errorf("payment seller profile ID is required")
	}
	profile, err := getPaymentSellerProfileByID(ctx, r.db, id)
	if err != nil {
		return PaymentSellerProfile{}, fmt.Errorf("get payment seller profile %q: %w", id, err)
	}
	return profile, nil
}

// ListSellerProfiles returns seller-of-record profiles in stable display order.
func (r PaymentProfileRepository) ListSellerProfiles(ctx context.Context) ([]PaymentSellerProfile, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	rows, err := r.db.QueryContext(
		ctx,
		paymentSellerProfileSelectSQL+`
		 ORDER BY seller_of_record, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list payment seller profiles: %w", err)
	}
	defer rows.Close()

	var profiles []PaymentSellerProfile
	for rows.Next() {
		profile, err := scanPaymentSellerProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment seller profiles: %w", err)
	}
	return profiles, nil
}

// CreatePaymentProfile persists payer bill-to settings and selects a default profile when appropriate.
func (r PaymentProfileRepository) CreatePaymentProfile(ctx context.Context, request PaymentProfileCreateRequest) (PaymentProfile, error) {
	if r.db == nil {
		return PaymentProfile{}, fmt.Errorf("database handle is required")
	}
	request = normalizePaymentProfileCreateRequest(request)
	if err := validatePaymentProfileCreateRequest(request); err != nil {
		return PaymentProfile{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("payprof")
		if err != nil {
			return PaymentProfile{}, err
		}
		request.ID = id
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if err := validatePaymentProfileSeller(ctx, tx, request.SellerProfileID, request.CurrencyCode); err != nil {
			return err
		}
		isDefault := request.IsDefault
		if !isDefault && request.Status == paymentProfileStatusActive {
			hasDefault, err := paymentProfileDefaultExists(ctx, tx, request.PayerAccountID, request.CurrencyCode)
			if err != nil {
				return err
			}
			isDefault = !hasDefault
		}
		if isDefault {
			if request.Status != paymentProfileStatusActive {
				return fmt.Errorf("default payment profile must be active")
			}
			if err := clearDefaultPaymentProfiles(ctx, tx, request.PayerAccountID, request.CurrencyCode); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO payment_profiles (
				id,
				payer_account_id,
				seller_profile_id,
				profile_name,
				bill_to_name,
				bill_to_email,
				bill_to_address,
				bill_to_tax_registration,
				currency_code,
				status,
				is_default
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.ID,
			request.PayerAccountID,
			request.SellerProfileID,
			request.ProfileName,
			request.BillToName,
			request.BillToEmail,
			request.BillToAddress,
			request.BillToTaxRegistration,
			request.CurrencyCode,
			request.Status,
			boolInt(isDefault),
		); err != nil {
			return fmt.Errorf("insert payment profile %q: %w", request.ID, err)
		}
		return nil
	}); err != nil {
		return PaymentProfile{}, err
	}
	return r.GetPaymentProfile(ctx, request.ID)
}

// GetPaymentProfile loads one payer payment profile by ID.
func (r PaymentProfileRepository) GetPaymentProfile(ctx context.Context, id string) (PaymentProfile, error) {
	if r.db == nil {
		return PaymentProfile{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return PaymentProfile{}, fmt.Errorf("payment profile ID is required")
	}
	profile, err := getPaymentProfileByID(ctx, r.db, id)
	if err != nil {
		return PaymentProfile{}, fmt.Errorf("get payment profile %q: %w", id, err)
	}
	return profile, nil
}

// ListPaymentProfiles returns payment profiles for one payer account in default-first order.
func (r PaymentProfileRepository) ListPaymentProfiles(ctx context.Context, payerAccountID string) ([]PaymentProfile, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	payerAccountID = strings.TrimSpace(payerAccountID)
	if payerAccountID == "" {
		return nil, fmt.Errorf("payer account ID is required")
	}
	rows, err := r.db.QueryContext(
		ctx,
		paymentProfileSelectSQL+`
		 WHERE payer_account_id = ?
		 ORDER BY is_default DESC, profile_name, id`,
		payerAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list payment profiles for payer %q: %w", payerAccountID, err)
	}
	defer rows.Close()

	var profiles []PaymentProfile
	for rows.Next() {
		profile, err := scanPaymentProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment profiles for payer %q: %w", payerAccountID, err)
	}
	return profiles, nil
}

// GetDefaultPaymentProfileForPayer loads the active default profile and default method for a payer/currency pair.
func (r PaymentProfileRepository) GetDefaultPaymentProfileForPayer(ctx context.Context, payerAccountID, currencyCode string) (PaymentProfileDetails, bool, error) {
	if r.db == nil {
		return PaymentProfileDetails{}, false, fmt.Errorf("database handle is required")
	}
	payerAccountID = strings.TrimSpace(payerAccountID)
	currencyCode = normalizePaymentCurrencyCode(currencyCode)
	if payerAccountID == "" {
		return PaymentProfileDetails{}, false, fmt.Errorf("payer account ID is required")
	}
	if currencyCode == "" {
		return PaymentProfileDetails{}, false, fmt.Errorf("payment profile currency code is required")
	}
	profile, found, err := getDefaultPaymentProfileForPayer(ctx, r.db, payerAccountID, currencyCode)
	if err != nil || !found {
		return PaymentProfileDetails{}, found, err
	}
	seller, err := getPaymentSellerProfileByID(ctx, r.db, profile.SellerProfileID)
	if err != nil {
		return PaymentProfileDetails{}, false, fmt.Errorf("get seller profile for default payment profile %q: %w", profile.ID, err)
	}
	if seller.Status != paymentSellerProfileStatusActive {
		return PaymentProfileDetails{}, false, fmt.Errorf("default payment profile seller must be active")
	}
	if seller.CurrencyCode != profile.CurrencyCode {
		return PaymentProfileDetails{}, false, fmt.Errorf("default payment profile currency %q must match seller profile currency %q", profile.CurrencyCode, seller.CurrencyCode)
	}
	method, hasMethod, err := getDefaultPaymentMethodForProfile(ctx, r.db, profile.ID)
	if err != nil {
		return PaymentProfileDetails{}, false, err
	}
	return PaymentProfileDetails{
		Profile:          profile,
		SellerProfile:    seller,
		DefaultMethod:    method,
		HasDefaultMethod: hasMethod,
	}, true, nil
}

// SetDefaultPaymentProfile makes one active profile the default for its payer and currency.
func (r PaymentProfileRepository) SetDefaultPaymentProfile(ctx context.Context, profileID string) (PaymentProfile, error) {
	if r.db == nil {
		return PaymentProfile{}, fmt.Errorf("database handle is required")
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return PaymentProfile{}, fmt.Errorf("payment profile ID is required")
	}
	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		profile, err := getPaymentProfileByID(ctx, tx, profileID)
		if err != nil {
			return err
		}
		if profile.Status != paymentProfileStatusActive {
			return fmt.Errorf("default payment profile must be active")
		}
		if err := validatePaymentProfileSeller(ctx, tx, profile.SellerProfileID, profile.CurrencyCode); err != nil {
			return err
		}
		if err := clearDefaultPaymentProfiles(ctx, tx, profile.PayerAccountID, profile.CurrencyCode); err != nil {
			return err
		}
		result, err := tx.ExecContext(
			ctx,
			`UPDATE payment_profiles
			 SET is_default = 1,
			     updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			 WHERE id = ?`,
			profile.ID,
		)
		if err != nil {
			return fmt.Errorf("set default payment profile %q: %w", profile.ID, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read default payment profile update count: %w", err)
		}
		if rowsAffected != 1 {
			return fmt.Errorf("payment profile %q was not updated", profile.ID)
		}
		return nil
	}); err != nil {
		return PaymentProfile{}, err
	}
	return r.GetPaymentProfile(ctx, profileID)
}

// CreatePaymentMethod persists one supported payment method and maintains single-default selection.
func (r PaymentProfileRepository) CreatePaymentMethod(ctx context.Context, request PaymentMethodCreateRequest) (PaymentMethod, error) {
	if r.db == nil {
		return PaymentMethod{}, fmt.Errorf("database handle is required")
	}
	request = normalizePaymentMethodCreateRequest(request)
	if request.PaymentProfileID == "" {
		return PaymentMethod{}, fmt.Errorf("payment profile ID is required")
	}
	profile, err := getPaymentProfileByID(ctx, r.db, request.PaymentProfileID)
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("get payment profile for method: %w", err)
	}
	if request.CurrencyCode == "" {
		request.CurrencyCode = profile.CurrencyCode
	}
	if err := validatePaymentMethodCreateRequest(request); err != nil {
		return PaymentMethod{}, err
	}
	if request.CurrencyCode != profile.CurrencyCode {
		return PaymentMethod{}, fmt.Errorf("payment method currency %q must match profile currency %q", request.CurrencyCode, profile.CurrencyCode)
	}
	if request.ID == "" {
		id, err := newRepositoryID("paymeth")
		if err != nil {
			return PaymentMethod{}, err
		}
		request.ID = id
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		isDefault := request.IsDefault
		if !isDefault && request.Status == paymentMethodStatusActive {
			hasDefault, err := paymentMethodDefaultExists(ctx, tx, request.PaymentProfileID)
			if err != nil {
				return err
			}
			isDefault = !hasDefault
		}
		if isDefault {
			if request.Status != paymentMethodStatusActive {
				return fmt.Errorf("default payment method must be active")
			}
			if err := clearDefaultPaymentMethods(ctx, tx, request.PaymentProfileID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO payment_methods (
				id,
				payment_profile_id,
				method_type,
				display_name,
				status,
				is_default,
				currency_code,
				card_brand,
				account_last4,
				expiration_month,
				expiration_year,
				bank_name,
				remittance_destination,
				advance_pay_balance_micros,
				failure_reason
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.ID,
			request.PaymentProfileID,
			request.MethodType,
			request.DisplayName,
			request.Status,
			boolInt(isDefault),
			request.CurrencyCode,
			request.CardBrand,
			request.AccountLast4,
			nullIntArg(request.ExpirationMonth),
			nullIntArg(request.ExpirationYear),
			request.BankName,
			request.RemittanceDestination,
			request.AdvancePayBalanceMicros,
			request.FailureReason,
		); err != nil {
			return fmt.Errorf("insert payment method %q: %w", request.ID, err)
		}
		return nil
	}); err != nil {
		return PaymentMethod{}, err
	}
	return r.GetPaymentMethod(ctx, request.ID)
}

// GetPaymentMethod loads one simulated payment method by ID.
func (r PaymentProfileRepository) GetPaymentMethod(ctx context.Context, id string) (PaymentMethod, error) {
	if r.db == nil {
		return PaymentMethod{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return PaymentMethod{}, fmt.Errorf("payment method ID is required")
	}
	method, err := getPaymentMethodByID(ctx, r.db, id)
	if err != nil {
		return PaymentMethod{}, fmt.Errorf("get payment method %q: %w", id, err)
	}
	return method, nil
}

// ListPaymentMethods returns methods for one payment profile in default-first order.
func (r PaymentProfileRepository) ListPaymentMethods(ctx context.Context, paymentProfileID string) ([]PaymentMethod, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	paymentProfileID = strings.TrimSpace(paymentProfileID)
	if paymentProfileID == "" {
		return nil, fmt.Errorf("payment profile ID is required")
	}
	rows, err := r.db.QueryContext(
		ctx,
		paymentMethodSelectSQL+`
		 WHERE payment_profile_id = ?
		 ORDER BY is_default DESC, method_type, display_name, id`,
		paymentProfileID,
	)
	if err != nil {
		return nil, fmt.Errorf("list payment methods for profile %q: %w", paymentProfileID, err)
	}
	defer rows.Close()

	var methods []PaymentMethod
	for rows.Next() {
		method, err := scanPaymentMethod(rows)
		if err != nil {
			return nil, err
		}
		methods = append(methods, method)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment methods for profile %q: %w", paymentProfileID, err)
	}
	return methods, nil
}

// SetDefaultPaymentMethod makes one active method the default for its payment profile.
func (r PaymentProfileRepository) SetDefaultPaymentMethod(ctx context.Context, methodID string) (PaymentMethod, error) {
	if r.db == nil {
		return PaymentMethod{}, fmt.Errorf("database handle is required")
	}
	methodID = strings.TrimSpace(methodID)
	if methodID == "" {
		return PaymentMethod{}, fmt.Errorf("payment method ID is required")
	}
	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		method, err := getPaymentMethodByID(ctx, tx, methodID)
		if err != nil {
			return err
		}
		profile, err := getPaymentProfileByID(ctx, tx, method.PaymentProfileID)
		if err != nil {
			return err
		}
		if profile.Status != paymentProfileStatusActive {
			return fmt.Errorf("default payment method profile must be active")
		}
		if method.Status != paymentMethodStatusActive {
			return fmt.Errorf("default payment method must be active")
		}
		if err := clearDefaultPaymentMethods(ctx, tx, method.PaymentProfileID); err != nil {
			return err
		}
		result, err := tx.ExecContext(
			ctx,
			`UPDATE payment_methods
			 SET is_default = 1,
			     updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			 WHERE id = ?`,
			method.ID,
		)
		if err != nil {
			return fmt.Errorf("set default payment method %q: %w", method.ID, err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read default payment method update count: %w", err)
		}
		if rowsAffected != 1 {
			return fmt.Errorf("payment method %q was not updated", method.ID)
		}
		return nil
	}); err != nil {
		return PaymentMethod{}, err
	}
	return r.GetPaymentMethod(ctx, methodID)
}

type paymentProfileQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type paymentProfileExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func getPaymentSellerProfileByID(ctx context.Context, q paymentProfileQueryer, id string) (PaymentSellerProfile, error) {
	return scanPaymentSellerProfile(q.QueryRowContext(
		ctx,
		paymentSellerProfileSelectSQL+`
		 WHERE id = ?`,
		id,
	))
}

func getPaymentProfileByID(ctx context.Context, q paymentProfileQueryer, id string) (PaymentProfile, error) {
	return scanPaymentProfile(q.QueryRowContext(
		ctx,
		paymentProfileSelectSQL+`
		 WHERE id = ?`,
		id,
	))
}

func getPaymentMethodByID(ctx context.Context, q paymentProfileQueryer, id string) (PaymentMethod, error) {
	return scanPaymentMethod(q.QueryRowContext(
		ctx,
		paymentMethodSelectSQL+`
		 WHERE id = ?`,
		id,
	))
}

func getDefaultPaymentProfileForPayer(ctx context.Context, q paymentProfileQueryer, payerAccountID, currencyCode string) (PaymentProfile, bool, error) {
	profile, err := scanPaymentProfile(q.QueryRowContext(
		ctx,
		paymentProfileSelectSQL+`
		 WHERE payer_account_id = ?
		   AND currency_code = ?
		   AND status = ?
		   AND is_default = 1`,
		payerAccountID,
		currencyCode,
		paymentProfileStatusActive,
	))
	if err != nil {
		if errMatchesNoRows(err) {
			return PaymentProfile{}, false, nil
		}
		return PaymentProfile{}, false, fmt.Errorf("get default payment profile for payer %q: %w", payerAccountID, err)
	}
	return profile, true, nil
}

func getDefaultPaymentMethodForProfile(ctx context.Context, q paymentProfileQueryer, paymentProfileID string) (PaymentMethod, bool, error) {
	method, err := scanPaymentMethod(q.QueryRowContext(
		ctx,
		paymentMethodSelectSQL+`
		 WHERE payment_profile_id = ?
		   AND status = ?
		   AND is_default = 1`,
		paymentProfileID,
		paymentMethodStatusActive,
	))
	if err != nil {
		if errMatchesNoRows(err) {
			return PaymentMethod{}, false, nil
		}
		return PaymentMethod{}, false, fmt.Errorf("get default payment method for profile %q: %w", paymentProfileID, err)
	}
	return method, true, nil
}

func validatePaymentProfileSeller(ctx context.Context, q paymentProfileQueryer, sellerProfileID, currencyCode string) error {
	seller, err := getPaymentSellerProfileByID(ctx, q, sellerProfileID)
	if err != nil {
		return fmt.Errorf("get seller profile for payment profile: %w", err)
	}
	if seller.Status != paymentSellerProfileStatusActive {
		return fmt.Errorf("payment profile seller must be active")
	}
	if seller.CurrencyCode != currencyCode {
		return fmt.Errorf("payment profile currency %q must match seller profile currency %q", currencyCode, seller.CurrencyCode)
	}
	return nil
}

func paymentProfileDefaultExists(ctx context.Context, q paymentProfileQueryer, payerAccountID, currencyCode string) (bool, error) {
	var count int
	if err := q.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM payment_profiles
		 WHERE payer_account_id = ?
		   AND currency_code = ?
		   AND status = ?
		   AND is_default = 1`,
		payerAccountID,
		currencyCode,
		paymentProfileStatusActive,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check default payment profile for payer %q: %w", payerAccountID, err)
	}
	return count > 0, nil
}

func paymentMethodDefaultExists(ctx context.Context, q paymentProfileQueryer, paymentProfileID string) (bool, error) {
	var count int
	if err := q.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM payment_methods
		 WHERE payment_profile_id = ?
		   AND status = ?
		   AND is_default = 1`,
		paymentProfileID,
		paymentMethodStatusActive,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check default payment method for profile %q: %w", paymentProfileID, err)
	}
	return count > 0, nil
}

func clearDefaultPaymentProfiles(ctx context.Context, q paymentProfileExecer, payerAccountID, currencyCode string) error {
	if _, err := q.ExecContext(
		ctx,
		`UPDATE payment_profiles
		 SET is_default = 0,
		     updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE payer_account_id = ?
		   AND currency_code = ?
		   AND is_default = 1`,
		payerAccountID,
		currencyCode,
	); err != nil {
		return fmt.Errorf("clear default payment profiles for payer %q: %w", payerAccountID, err)
	}
	return nil
}

func clearDefaultPaymentMethods(ctx context.Context, q paymentProfileExecer, paymentProfileID string) error {
	if _, err := q.ExecContext(
		ctx,
		`UPDATE payment_methods
		 SET is_default = 0,
		     updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE payment_profile_id = ?
		   AND is_default = 1`,
		paymentProfileID,
	); err != nil {
		return fmt.Errorf("clear default payment methods for profile %q: %w", paymentProfileID, err)
	}
	return nil
}

func invoiceDocumentProfileForPayer(ctx context.Context, q paymentProfileQueryer, payerAccountID, currencyCode string) (invoiceDocumentProfileFields, error) {
	payerAccountID = strings.TrimSpace(payerAccountID)
	currencyCode = normalizePaymentCurrencyCode(currencyCode)
	if payerAccountID == "" || currencyCode == "" {
		return defaultInvoiceDocumentProfileFields(), nil
	}
	var fields invoiceDocumentProfileFields
	err := q.QueryRowContext(
		ctx,
		`SELECT
			s.seller_of_record,
			s.seller_address,
			s.seller_tax_registration,
			p.bill_to_name,
			p.bill_to_email,
			p.bill_to_address,
			p.bill_to_tax_registration
		 FROM payment_profiles p
		 JOIN payment_seller_profiles s ON s.id = p.seller_profile_id
		 WHERE p.payer_account_id = ?
		   AND p.currency_code = ?
		   AND p.status = ?
		   AND p.is_default = 1
		   AND s.status = ?
		 LIMIT 1`,
		payerAccountID,
		currencyCode,
		paymentProfileStatusActive,
		paymentSellerProfileStatusActive,
	).Scan(
		&fields.SellerOfRecord,
		&fields.SellerAddress,
		&fields.SellerTaxRegistration,
		&fields.BillToName,
		&fields.BillToEmail,
		&fields.BillToAddress,
		&fields.BillToTaxRegistration,
	)
	if err != nil {
		if errMatchesNoRows(err) {
			return defaultInvoiceDocumentProfileFields(), nil
		}
		return invoiceDocumentProfileFields{}, fmt.Errorf("get invoice payment profile for payer %q: %w", payerAccountID, err)
	}
	return fields.withDefaults(), nil
}

func normalizePaymentSellerProfileCreateRequest(request PaymentSellerProfileCreateRequest) PaymentSellerProfileCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.SellerOfRecord = strings.TrimSpace(request.SellerOfRecord)
	request.SellerAddress = strings.TrimSpace(request.SellerAddress)
	request.SellerTaxRegistration = strings.TrimSpace(request.SellerTaxRegistration)
	request.RemittanceInstructions = strings.TrimSpace(request.RemittanceInstructions)
	request.CurrencyCode = normalizePaymentCurrencyCode(request.CurrencyCode)
	if request.CurrencyCode == "" {
		request.CurrencyCode = defaultBillCurrencyCode
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = paymentSellerProfileStatusActive
	}
	return request
}

func validatePaymentSellerProfileCreateRequest(request PaymentSellerProfileCreateRequest) error {
	if request.SellerOfRecord == "" {
		return fmt.Errorf("seller of record is required")
	}
	if request.SellerAddress == "" {
		return fmt.Errorf("seller address is required")
	}
	if len(request.CurrencyCode) != 3 {
		return fmt.Errorf("seller profile currency code must be three characters")
	}
	switch request.Status {
	case paymentSellerProfileStatusActive, paymentSellerProfileStatusInactive:
		return nil
	default:
		return fmt.Errorf("unsupported seller profile status %q", request.Status)
	}
}

func normalizePaymentProfileCreateRequest(request PaymentProfileCreateRequest) PaymentProfileCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.SellerProfileID = strings.TrimSpace(request.SellerProfileID)
	request.ProfileName = strings.TrimSpace(request.ProfileName)
	request.BillToName = strings.TrimSpace(request.BillToName)
	request.BillToEmail = strings.TrimSpace(request.BillToEmail)
	request.BillToAddress = strings.TrimSpace(request.BillToAddress)
	request.BillToTaxRegistration = strings.TrimSpace(request.BillToTaxRegistration)
	request.CurrencyCode = normalizePaymentCurrencyCode(request.CurrencyCode)
	if request.CurrencyCode == "" {
		request.CurrencyCode = defaultBillCurrencyCode
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = paymentProfileStatusActive
	}
	return request
}

func validatePaymentProfileCreateRequest(request PaymentProfileCreateRequest) error {
	if request.PayerAccountID == "" {
		return fmt.Errorf("payer account ID is required")
	}
	if request.SellerProfileID == "" {
		return fmt.Errorf("seller profile ID is required")
	}
	if request.ProfileName == "" {
		return fmt.Errorf("payment profile name is required")
	}
	if request.BillToName == "" {
		return fmt.Errorf("bill-to name is required")
	}
	if request.BillToEmail == "" {
		return fmt.Errorf("bill-to email is required")
	}
	if request.BillToAddress == "" {
		return fmt.Errorf("bill-to address is required")
	}
	if len(request.CurrencyCode) != 3 {
		return fmt.Errorf("payment profile currency code must be three characters")
	}
	switch request.Status {
	case paymentProfileStatusActive, paymentProfileStatusInactive:
	default:
		return fmt.Errorf("unsupported payment profile status %q", request.Status)
	}
	if request.IsDefault && request.Status != paymentProfileStatusActive {
		return fmt.Errorf("default payment profile must be active")
	}
	return nil
}

func normalizePaymentMethodCreateRequest(request PaymentMethodCreateRequest) PaymentMethodCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.PaymentProfileID = strings.TrimSpace(request.PaymentProfileID)
	request.MethodType = strings.TrimSpace(request.MethodType)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = paymentMethodStatusActive
	}
	request.CurrencyCode = normalizePaymentCurrencyCode(request.CurrencyCode)
	request.CardBrand = strings.TrimSpace(request.CardBrand)
	request.AccountLast4 = strings.TrimSpace(request.AccountLast4)
	request.BankName = strings.TrimSpace(request.BankName)
	request.RemittanceDestination = strings.TrimSpace(request.RemittanceDestination)
	request.FailureReason = strings.TrimSpace(request.FailureReason)
	return request
}

func validatePaymentMethodCreateRequest(request PaymentMethodCreateRequest) error {
	if request.PaymentProfileID == "" {
		return fmt.Errorf("payment profile ID is required")
	}
	if request.DisplayName == "" {
		return fmt.Errorf("payment method display name is required")
	}
	if len(request.CurrencyCode) != 3 {
		return fmt.Errorf("payment method currency code must be three characters")
	}
	switch request.Status {
	case paymentMethodStatusActive, paymentMethodStatusInactive, paymentMethodStatusFailed, paymentMethodStatusExpired:
	default:
		return fmt.Errorf("unsupported payment method status %q", request.Status)
	}
	if request.IsDefault && request.Status != paymentMethodStatusActive {
		return fmt.Errorf("default payment method must be active")
	}
	switch request.MethodType {
	case paymentMethodTypeCard:
		if request.CardBrand == "" {
			return fmt.Errorf("card brand is required")
		}
		if !validPaymentLast4(request.AccountLast4) {
			return fmt.Errorf("card last4 must be four digits")
		}
		if request.ExpirationMonth < 1 || request.ExpirationMonth > 12 || request.ExpirationYear < 2000 {
			return fmt.Errorf("card expiration month and year are required")
		}
	case paymentMethodTypeACH:
		if request.BankName == "" {
			return fmt.Errorf("ACH bank name is required")
		}
		if !validPaymentLast4(request.AccountLast4) {
			return fmt.Errorf("ACH last4 must be four digits")
		}
	case paymentMethodTypeInvoiceRemittance:
		if request.RemittanceDestination == "" {
			return fmt.Errorf("invoice remittance destination is required")
		}
	case paymentMethodTypeAdvancePayBalance:
		if request.AdvancePayBalanceMicros < 0 {
			return fmt.Errorf("Advance Pay balance must be non-negative")
		}
	default:
		return fmt.Errorf("unsupported payment method type %q", request.MethodType)
	}
	return nil
}

func validPaymentLast4(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func normalizePaymentCurrencyCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

type paymentSellerProfileRow interface {
	Scan(dest ...any) error
}

func scanPaymentSellerProfile(row paymentSellerProfileRow) (PaymentSellerProfile, error) {
	var profile PaymentSellerProfile
	if err := row.Scan(
		&profile.ID,
		&profile.SellerOfRecord,
		&profile.SellerAddress,
		&profile.SellerTaxRegistration,
		&profile.RemittanceInstructions,
		&profile.CurrencyCode,
		&profile.Status,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	); err != nil {
		return PaymentSellerProfile{}, fmt.Errorf("scan payment seller profile: %w", err)
	}
	return profile, nil
}

type paymentProfileRow interface {
	Scan(dest ...any) error
}

func scanPaymentProfile(row paymentProfileRow) (PaymentProfile, error) {
	var profile PaymentProfile
	var isDefault int
	if err := row.Scan(
		&profile.ID,
		&profile.PayerAccountID,
		&profile.SellerProfileID,
		&profile.ProfileName,
		&profile.BillToName,
		&profile.BillToEmail,
		&profile.BillToAddress,
		&profile.BillToTaxRegistration,
		&profile.CurrencyCode,
		&profile.Status,
		&isDefault,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	); err != nil {
		return PaymentProfile{}, fmt.Errorf("scan payment profile: %w", err)
	}
	profile.IsDefault = isDefault == 1
	return profile, nil
}

type paymentMethodRow interface {
	Scan(dest ...any) error
}

func scanPaymentMethod(row paymentMethodRow) (PaymentMethod, error) {
	var method PaymentMethod
	var isDefault int
	var expirationMonth, expirationYear sql.NullInt64
	if err := row.Scan(
		&method.ID,
		&method.PaymentProfileID,
		&method.MethodType,
		&method.DisplayName,
		&method.Status,
		&isDefault,
		&method.CurrencyCode,
		&method.CardBrand,
		&method.AccountLast4,
		&expirationMonth,
		&expirationYear,
		&method.BankName,
		&method.RemittanceDestination,
		&method.AdvancePayBalanceMicros,
		&method.FailureReason,
		&method.CreatedAt,
		&method.UpdatedAt,
	); err != nil {
		return PaymentMethod{}, fmt.Errorf("scan payment method: %w", err)
	}
	method.IsDefault = isDefault == 1
	method.ExpirationMonth = nullIntValue(expirationMonth)
	method.ExpirationYear = nullIntValue(expirationYear)
	return method, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

const paymentSellerProfileSelectSQL = `SELECT
			id,
			seller_of_record,
			seller_address,
			seller_tax_registration,
			remittance_instructions,
			currency_code,
			status,
			created_at,
			updated_at
		 FROM payment_seller_profiles`

const paymentProfileSelectSQL = `SELECT
			id,
			payer_account_id,
			seller_profile_id,
			profile_name,
			bill_to_name,
			bill_to_email,
			bill_to_address,
			bill_to_tax_registration,
			currency_code,
			status,
			is_default,
			created_at,
			updated_at
		 FROM payment_profiles`

const paymentMethodSelectSQL = `SELECT
			id,
			payment_profile_id,
			method_type,
			display_name,
			status,
			is_default,
			currency_code,
			card_brand,
			account_last4,
			expiration_month,
			expiration_year,
			bank_name,
			remittance_destination,
			advance_pay_balance_micros,
			failure_reason,
			created_at,
			updated_at
		 FROM payment_methods`
