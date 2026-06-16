package persistence

import "database/sql"

const (
	proFormaStatusActive             = "active"
	proFormaDefaultCurrency          = "USD"
	proFormaDefaultMultiplierBPS int = 10_000
	proFormaMaxMultiplierBPS     int = 1_000_000
	defaultProFormaLineItemLimit     = 50
	maxProFormaLineItemLimit         = 200
)

const (
	ProFormaCustomLineItemTypeFee        = "fee"
	ProFormaCustomLineItemTypeCredit     = "credit"
	ProFormaCustomLineItemTypeMarkup     = "markup"
	ProFormaCustomLineItemTypeAnnotation = "annotation"
)

// ProFormaPricingPlan groups custom internal rates used for showback views.
type ProFormaPricingPlan struct {
	ID           string
	Name         string
	Description  string
	CurrencyCode string
	Status       string
	RuleCount    int
	CreatedAt    string
	UpdatedAt    string
}

// ProFormaPricingRule applies one service-level internal rate multiplier.
type ProFormaPricingRule struct {
	ID                        string
	PricingPlanID             string
	PricingPlanName           string
	ServiceCode               string
	RateMultiplierBasisPoints int
	Description               string
	Status                    string
	CreatedAt                 string
	UpdatedAt                 string
}

// ProFormaBillingGroup assigns usage accounts to one pro forma pricing plan.
type ProFormaBillingGroup struct {
	ID              string
	Name            string
	Description     string
	PayerAccountID  string
	PricingPlanID   string
	PricingPlanName string
	Status          string
	AccountCount    int
	CreatedAt       string
	UpdatedAt       string
}

// ProFormaBillingGroupAccount stores one usage-account membership.
type ProFormaBillingGroupAccount struct {
	ID             string
	BillingGroupID string
	AccountID      string
	CreatedAt      string
}

// ProFormaLineItem stores one internal showback row derived from a bill line item.
type ProFormaLineItem struct {
	ID                        string
	SourceBillLineItemID      string
	BillingGroupID            string
	BillingGroupName          string
	PricingPlanID             string
	PricingPlanName           string
	PricingRuleID             string
	BillingPeriodStart        string
	BillingPeriodEnd          string
	PayerAccountID            string
	UsageAccountID            string
	ServiceCode               string
	ServiceName               string
	UsageType                 string
	LineItemStatus            string
	CurrencyCode              string
	SourceRateMicros          int64
	SourceCostMicros          int64
	RateMultiplierBasisPoints int
	ProFormaRateMicros        int64
	ProFormaCostMicros        int64
	AdjustmentMicros          int64
	CreatedAt                 string
	UpdatedAt                 string
}

// ProFormaCustomLineItem stores one manual pro forma adjustment row.
type ProFormaCustomLineItem struct {
	ID                 string
	BillingGroupID     string
	BillingGroupName   string
	PricingPlanID      string
	PricingPlanName    string
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	LineItemType       string
	Name               string
	Description        string
	CurrencyCode       string
	AmountMicros       int64
	CreatedAt          string
	UpdatedAt          string
}

// ProFormaRefreshRequest selects source bill line items for pro forma regeneration.
type ProFormaRefreshRequest struct {
	BillingGroupID     string
	PayerAccountID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
}

// ProFormaRefreshResult reports the rows rebuilt by a pro forma refresh.
type ProFormaRefreshResult struct {
	BillingGroupsRefreshed int
	SourceLineItems        int
	ProFormaLineItems      int
	SourceCostMicros       int64
	ProFormaCostMicros     int64
	AdjustmentMicros       int64
}

// ProFormaSummaryRequest filters comparison summaries by period and group.
type ProFormaSummaryRequest struct {
	BillingGroupID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
}

// ProFormaBillingGroupSummary compares payable source cost to internal pro forma cost.
type ProFormaBillingGroupSummary struct {
	BillingGroupID      string
	BillingGroupName    string
	PricingPlanID       string
	PricingPlanName     string
	BillingPeriodStart  string
	BillingPeriodEnd    string
	PayerAccountID      string
	CurrencyCode        string
	SourceLineItemCount int
	CustomLineItemCount int
	SourceCostMicros    int64
	CustomAmountMicros  int64
	ProFormaCostMicros  int64
	AdjustmentMicros    int64
}

// ProFormaPricingPlanCreateRequest describes a new internal pricing plan.
type ProFormaPricingPlanCreateRequest struct {
	ID           string
	Name         string
	Description  string
	CurrencyCode string
	Status       string
}

// ProFormaPricingRuleCreateRequest describes one service-level custom rate.
type ProFormaPricingRuleCreateRequest struct {
	ID                        string
	PricingPlanID             string
	ServiceCode               string
	RateMultiplierBasisPoints int
	Description               string
	Status                    string
}

// ProFormaBillingGroupCreateRequest describes a new billing group.
type ProFormaBillingGroupCreateRequest struct {
	ID             string
	Name           string
	Description    string
	PayerAccountID string
	PricingPlanID  string
	Status         string
}

// ProFormaBillingGroupAccountCreateRequest describes one account assignment.
type ProFormaBillingGroupAccountCreateRequest struct {
	ID             string
	BillingGroupID string
	AccountID      string
}

// ProFormaCustomLineItemCreateRequest describes one manual pro forma adjustment.
type ProFormaCustomLineItemCreateRequest struct {
	ID                 string
	BillingGroupID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
	LineItemType       string
	Name               string
	Description        string
	CurrencyCode       string
	AmountMicros       int64
}

// ProFormaLineItemListRequest filters persisted pro forma rows for display.
type ProFormaLineItemListRequest struct {
	BillingGroupID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
	Limit              int
}

// ProFormaCustomLineItemListRequest filters manual custom rows for display.
type ProFormaCustomLineItemListRequest struct {
	BillingGroupID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
	Limit              int
}

// ProFormaBillingRepository manages pro forma billing groups, pricing plans, and generated rows.
type ProFormaBillingRepository struct {
	db *sql.DB
}

// NewProFormaBillingRepository creates a repository backed by a workspace database.
func NewProFormaBillingRepository(db *sql.DB) ProFormaBillingRepository {
	return ProFormaBillingRepository{db: db}
}
