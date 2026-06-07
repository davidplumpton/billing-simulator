package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

const maxDefinitionBytes = 1024 * 1024

// Definition is a parsed scenario DSL document ready for runner validation.
type Definition struct {
	Name                 string  `json:"name"`
	Clock                Clock   `json:"clock"`
	OrganizationTemplate string  `json:"organization_template"`
	RandomSeed           int64   `json:"random_seed,omitempty"`
	Events               []Event `json:"events,omitempty"`
	Checks               []Check `json:"checks,omitempty"`
}

// Clock defines the starting point for deterministic scenario execution.
type Clock struct {
	Start string `json:"start"`
}

// EventAction identifies a supported scenario event operation.
type EventAction string

const (
	// EventActionCreateAccount creates a simulated member account in the organization.
	EventActionCreateAccount EventAction = "create_account"

	// EventActionCreateResource creates a synthetic resource in an account.
	EventActionCreateResource EventAction = "create_resource"

	// EventActionAddUsage records one explicit usage event.
	EventActionAddUsage EventAction = "add_usage"

	// EventActionGenerateUsage runs a deterministic usage generator pattern.
	EventActionGenerateUsage EventAction = "generate_usage"

	// EventActionAdvanceClock moves the simulator clock by a fixed amount.
	EventActionAdvanceClock EventAction = "advance_clock"

	// EventActionRunDailyMetering runs estimated metering through the current clock.
	EventActionRunDailyMetering EventAction = "run_daily_metering"

	// EventActionCloseBillingPeriod finalizes a completed billing period.
	EventActionCloseBillingPeriod EventAction = "close_billing_period"

	// EventActionIssueBill represents an explicit bill issuance step.
	EventActionIssueBill EventAction = "issue_bill"

	// EventActionRefreshCostAllocationTags rebuilds billing-side tag discovery from resource tags.
	EventActionRefreshCostAllocationTags EventAction = "refresh_cost_allocation_tags"

	// EventActionActivateCostAllocationTag activates one discovered resource tag key for billing reports.
	EventActionActivateCostAllocationTag EventAction = "activate_cost_allocation_tag"

	// EventActionCreateCostCategory creates or reuses one learner-facing Cost Category dimension.
	EventActionCreateCostCategory EventAction = "create_cost_category"

	// EventActionCreateCostCategoryRule creates or reuses one ordered Cost Category rule.
	EventActionCreateCostCategoryRule EventAction = "create_cost_category_rule"

	// EventActionCreateCostCategorySplitRule creates or reuses one Cost Category split-charge rule.
	EventActionCreateCostCategorySplitRule EventAction = "create_cost_category_split_rule"

	// EventActionCreatePaymentMethod creates a simulated payment method for a payer profile.
	EventActionCreatePaymentMethod EventAction = "create_payment_method"

	// EventActionSchedulePayment moves the latest or named invoice obligation into scheduled collection.
	EventActionSchedulePayment EventAction = "schedule_payment"

	// EventActionProcessPayment moves the latest or named invoice obligation into processing.
	EventActionProcessPayment EventAction = "process_payment"

	// EventActionFailPayment records a failed collection attempt for the latest or named invoice obligation.
	EventActionFailPayment EventAction = "fail_payment"

	// EventActionMarkPaymentDue returns the latest or named invoice obligation to due state.
	EventActionMarkPaymentDue EventAction = "mark_payment_due"

	// EventActionMarkPaymentPastDue moves the latest or named invoice obligation past due.
	EventActionMarkPaymentPastDue EventAction = "mark_payment_past_due"

	// EventActionCollectPayment applies simulated funds to the latest or named invoice obligation.
	EventActionCollectPayment EventAction = "collect_payment"

	// EventActionCreateBudget creates or reuses a monthly budget definition for budget labs.
	EventActionCreateBudget EventAction = "create_budget"

	// EventActionRefreshBudgetForecasts refreshes budget forecast summaries and alert notifications.
	EventActionRefreshBudgetForecasts EventAction = "refresh_budget_forecasts"

	// EventActionCreateSavedReport creates or updates a Cost Explorer saved report starter definition.
	EventActionCreateSavedReport EventAction = "create_saved_report"
)

// Event describes one ordered resource, usage, clock, or billing operation.
type Event struct {
	ID                      string              `json:"id,omitempty"`
	Sequence                int                 `json:"-"`
	Day                     int                 `json:"day,omitempty"`
	At                      string              `json:"at,omitempty"`
	Action                  EventAction         `json:"action"`
	Account                 string              `json:"account,omitempty"`
	AccountID               string              `json:"account_id,omitempty"`
	AccountEmail            string              `json:"account_email,omitempty"`
	OrganizationID          string              `json:"organization_id,omitempty"`
	ParentUnitID            string              `json:"parent_unit_id,omitempty"`
	PayerAccount            string              `json:"payer_account,omitempty"`
	PayerAccountID          string              `json:"payer_account_id,omitempty"`
	Service                 string              `json:"service,omitempty"`
	ServiceCode             string              `json:"service_code,omitempty"`
	Resource                string              `json:"resource,omitempty"`
	ResourceID              string              `json:"resource_id,omitempty"`
	ResourceType            string              `json:"resource_type,omitempty"`
	Region                  string              `json:"region,omitempty"`
	Status                  string              `json:"status,omitempty"`
	TagKey                  string              `json:"tag_key,omitempty"`
	Tags                    map[string]string   `json:"tags,omitempty"`
	Attributes              map[string]string   `json:"attributes,omitempty"`
	UsageType               string              `json:"usage_type,omitempty"`
	Operation               string              `json:"operation,omitempty"`
	UsageStartAt            string              `json:"usage_start_at,omitempty"`
	UsageEndAt              string              `json:"usage_end_at,omitempty"`
	Amount                  int                 `json:"amount,omitempty"`
	AmountGB                *json.Number        `json:"amount_gb,omitempty"`
	AmountHours             *json.Number        `json:"amount_hours,omitempty"`
	Quantity                *json.Number        `json:"quantity,omitempty"`
	QuantityMicros          int64               `json:"quantity_micros,omitempty"`
	Unit                    string              `json:"unit,omitempty"`
	Pattern                 string              `json:"pattern,omitempty"`
	Days                    int                 `json:"days,omitempty"`
	BillingPeriodStart      string              `json:"billing_period_start,omitempty"`
	BillingPeriodEnd        string              `json:"billing_period_end,omitempty"`
	Category                string              `json:"category,omitempty"`
	CategoryID              string              `json:"category_id,omitempty"`
	DefaultValue            string              `json:"default_value,omitempty"`
	Description             string              `json:"description,omitempty"`
	RuleOrder               int                 `json:"rule_order,omitempty"`
	Value                   string              `json:"value,omitempty"`
	MatchType               string              `json:"match_type,omitempty"`
	Dimension               string              `json:"dimension,omitempty"`
	Operator                string              `json:"operator,omitempty"`
	Values                  []string            `json:"values,omitempty"`
	ReferencedCategory      string              `json:"referenced_category,omitempty"`
	ReferencedCategoryID    string              `json:"referenced_category_id,omitempty"`
	SourceValue             string              `json:"source_value,omitempty"`
	Method                  string              `json:"method,omitempty"`
	Targets                 []SplitChargeTarget `json:"targets,omitempty"`
	InvoiceObligationID     string              `json:"invoice_obligation_id,omitempty"`
	PaymentProfileID        string              `json:"payment_profile_id,omitempty"`
	PaymentMethodID         string              `json:"payment_method_id,omitempty"`
	MethodType              string              `json:"method_type,omitempty"`
	DisplayName             string              `json:"display_name,omitempty"`
	CurrencyCode            string              `json:"currency_code,omitempty"`
	IsDefault               bool                `json:"is_default,omitempty"`
	CardBrand               string              `json:"card_brand,omitempty"`
	AccountLast4            string              `json:"account_last4,omitempty"`
	ExpirationMonth         int                 `json:"expiration_month,omitempty"`
	ExpirationYear          int                 `json:"expiration_year,omitempty"`
	BankName                string              `json:"bank_name,omitempty"`
	RemittanceDestination   string              `json:"remittance_destination,omitempty"`
	AdvancePayBalanceMicros int64               `json:"advance_pay_balance_micros,omitempty"`
	FailureReason           string              `json:"failure_reason,omitempty"`
	Reason                  string              `json:"reason,omitempty"`
	AmountMicros            int64               `json:"amount_micros,omitempty"`
	BudgetID                string              `json:"budget_id,omitempty"`
	BudgetName              string              `json:"budget_name,omitempty"`
	BudgetAmountMicros      int64               `json:"budget_amount_micros,omitempty"`
	ScopeType               string              `json:"scope_type,omitempty"`
	ScopeKey                string              `json:"scope_key,omitempty"`
	ScopeValue              string              `json:"scope_value,omitempty"`
	Thresholds              []BudgetThreshold   `json:"thresholds,omitempty"`
	ReportID                string              `json:"report_id,omitempty"`
	ReportName              string              `json:"report_name,omitempty"`
	OwnerAccount            string              `json:"owner_account,omitempty"`
	OwnerAccountID          string              `json:"owner_account_id,omitempty"`
	OwnerRole               string              `json:"owner_role,omitempty"`
	DateRangeStart          string              `json:"date_range_start,omitempty"`
	DateRangeEnd            string              `json:"date_range_end,omitempty"`
	Granularity             string              `json:"granularity,omitempty"`
	Filters                 map[string][]string `json:"filters,omitempty"`
	Groupings               []ReportGrouping    `json:"groupings,omitempty"`
	Metrics                 []string            `json:"metrics,omitempty"`
	ChartType               string              `json:"chart_type,omitempty"`
}

// SplitChargeTarget describes one scenario-authored split-charge allocation target.
type SplitChargeTarget struct {
	Value            string `json:"value"`
	FixedShareMicros int    `json:"fixed_share_micros,omitempty"`
}

// BudgetThreshold describes one scenario-authored budget threshold.
type BudgetThreshold struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type"`
	BasisPoints int    `json:"basis_points"`
}

// ReportGrouping describes one scenario-authored saved report grouping.
type ReportGrouping struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

// CheckType identifies an expected learner outcome.
type CheckType string

const (
	// CheckTypeSavedReportExists expects the learner to save a named report.
	CheckTypeSavedReportExists CheckType = "saved_report_exists"

	// CheckTypeIdentifiesTopDriver expects the learner to name the dominant cost driver.
	CheckTypeIdentifiesTopDriver CheckType = "identifies_top_driver"

	// CheckTypeCostCategoryRuleCreated expects a named cost category rule.
	CheckTypeCostCategoryRuleCreated CheckType = "cost_category_rule_created"
)

// Check describes one expected learner outcome in a scenario definition.
type Check struct {
	ID              string            `json:"id,omitempty"`
	Sequence        int               `json:"-"`
	Type            CheckType         `json:"type"`
	ReportName      string            `json:"report_name,omitempty"`
	ExpectedService string            `json:"expected_service,omitempty"`
	Category        string            `json:"category,omitempty"`
	Account         string            `json:"account,omitempty"`
	Service         string            `json:"service,omitempty"`
	Status          string            `json:"status,omitempty"`
	ExpectedValue   *json.Number      `json:"expected_value,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
}

// ValidationError reports all schema and semantic problems found in a definition.
type ValidationError struct {
	Problems []string
}

// Error returns a compact summary of scenario definition validation problems.
func (e ValidationError) Error() string {
	return "scenario definition invalid: " + strings.Join(e.Problems, "; ")
}

// ParseDefinition parses a JSON or YAML scenario definition from a bounded reader.
func ParseDefinition(r io.Reader) (Definition, error) {
	if r == nil {
		return Definition{}, fmt.Errorf("scenario definition reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxDefinitionBytes+1))
	if err != nil {
		return Definition{}, fmt.Errorf("read scenario definition: %w", err)
	}
	if len(data) > maxDefinitionBytes {
		return Definition{}, fmt.Errorf("scenario definition must be %d bytes or smaller", maxDefinitionBytes)
	}
	return ParseDefinitionBytes(data)
}

// ParseDefinitionBytes parses a JSON or YAML scenario definition from memory.
func ParseDefinitionBytes(data []byte) (Definition, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return Definition{}, fmt.Errorf("scenario definition is required")
	}
	switch data[0] {
	case '{':
		return parseDefinitionJSONBytes(data)
	case '[':
		return Definition{}, fmt.Errorf("scenario definition must be a JSON object or YAML mapping")
	default:
		jsonData, err := parseDefinitionYAMLBytes(data)
		if err != nil {
			return Definition{}, fmt.Errorf("parse scenario definition YAML: %w", err)
		}
		return parseDefinitionJSONBytes(jsonData)
	}
}

func parseDefinitionJSONBytes(data []byte) (Definition, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()

	var definition Definition
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, fmt.Errorf("parse scenario definition JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Definition{}, fmt.Errorf("parse scenario definition JSON: multiple documents are not supported")
	}
	return normalizeAndValidate(definition)
}

func normalizeAndValidate(definition Definition) (Definition, error) {
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Clock.Start = strings.TrimSpace(definition.Clock.Start)
	definition.OrganizationTemplate = strings.TrimSpace(definition.OrganizationTemplate)

	var problems validationProblems
	if definition.Name == "" {
		problems.add("name is required")
	}
	if definition.Clock.Start == "" {
		problems.add("clock.start is required")
	} else if _, err := time.Parse(time.DateOnly, definition.Clock.Start); err != nil {
		problems.add("clock.start must use YYYY-MM-DD")
	}
	if definition.OrganizationTemplate == "" {
		problems.add("organization_template is required")
	}
	if definition.RandomSeed < 0 {
		problems.add("random_seed must be zero or greater")
	}
	if len(definition.Events) == 0 && len(definition.Checks) == 0 {
		problems.add("events or checks are required")
	}

	eventIDs := map[string]int{}
	for i := range definition.Events {
		event := normalizeEvent(definition.Events[i], i)
		definition.Events[i] = event
		validateEvent(event, i, &problems)
		if previous, ok := eventIDs[event.ID]; ok {
			problems.add("events[%d].id duplicates events[%d].id %q", i, previous, event.ID)
		}
		eventIDs[event.ID] = i
	}

	checkIDs := map[string]int{}
	for i := range definition.Checks {
		check := normalizeCheck(definition.Checks[i], i)
		definition.Checks[i] = check
		validateCheck(check, i, &problems)
		if previous, ok := checkIDs[check.ID]; ok {
			problems.add("checks[%d].id duplicates checks[%d].id %q", i, previous, check.ID)
		}
		checkIDs[check.ID] = i
	}

	validateScenarioSemantics(definition, &problems)

	if len(problems) > 0 {
		return Definition{}, ValidationError{Problems: problems}
	}
	return definition, nil
}

func normalizeEvent(event Event, index int) Event {
	event.ID = strings.TrimSpace(event.ID)
	if event.ID == "" {
		event.ID = fmt.Sprintf("event-%03d", index+1)
	}
	event.Sequence = index + 1
	event.At = strings.TrimSpace(event.At)
	event.Action = EventAction(strings.ToLower(strings.TrimSpace(string(event.Action))))
	event.Account = strings.TrimSpace(event.Account)
	event.AccountID = strings.TrimSpace(event.AccountID)
	event.AccountEmail = strings.TrimSpace(event.AccountEmail)
	event.OrganizationID = strings.TrimSpace(event.OrganizationID)
	event.ParentUnitID = strings.TrimSpace(event.ParentUnitID)
	event.PayerAccount = strings.TrimSpace(event.PayerAccount)
	event.PayerAccountID = strings.TrimSpace(event.PayerAccountID)
	event.Service = strings.TrimSpace(event.Service)
	event.ServiceCode = strings.TrimSpace(event.ServiceCode)
	event.Resource = strings.TrimSpace(event.Resource)
	event.ResourceID = strings.TrimSpace(event.ResourceID)
	event.ResourceType = strings.TrimSpace(event.ResourceType)
	event.Region = strings.TrimSpace(event.Region)
	event.Status = strings.TrimSpace(event.Status)
	event.TagKey = strings.TrimSpace(event.TagKey)
	event.UsageType = strings.TrimSpace(event.UsageType)
	event.Operation = strings.TrimSpace(event.Operation)
	event.UsageStartAt = strings.TrimSpace(event.UsageStartAt)
	event.UsageEndAt = strings.TrimSpace(event.UsageEndAt)
	event.Unit = strings.TrimSpace(event.Unit)
	event.Pattern = strings.TrimSpace(event.Pattern)
	event.BillingPeriodStart = strings.TrimSpace(event.BillingPeriodStart)
	event.BillingPeriodEnd = strings.TrimSpace(event.BillingPeriodEnd)
	event.Category = strings.TrimSpace(event.Category)
	event.CategoryID = strings.TrimSpace(event.CategoryID)
	event.DefaultValue = strings.TrimSpace(event.DefaultValue)
	event.Description = strings.TrimSpace(event.Description)
	event.Value = strings.TrimSpace(event.Value)
	event.MatchType = strings.TrimSpace(event.MatchType)
	event.Dimension = strings.TrimSpace(event.Dimension)
	event.Operator = strings.TrimSpace(event.Operator)
	event.ReferencedCategory = strings.TrimSpace(event.ReferencedCategory)
	event.ReferencedCategoryID = strings.TrimSpace(event.ReferencedCategoryID)
	event.SourceValue = strings.TrimSpace(event.SourceValue)
	event.Method = strings.TrimSpace(event.Method)
	event.InvoiceObligationID = strings.TrimSpace(event.InvoiceObligationID)
	event.PaymentProfileID = strings.TrimSpace(event.PaymentProfileID)
	event.PaymentMethodID = strings.TrimSpace(event.PaymentMethodID)
	event.MethodType = strings.TrimSpace(event.MethodType)
	event.DisplayName = strings.TrimSpace(event.DisplayName)
	event.CurrencyCode = strings.ToUpper(strings.TrimSpace(event.CurrencyCode))
	event.CardBrand = strings.TrimSpace(event.CardBrand)
	event.AccountLast4 = strings.TrimSpace(event.AccountLast4)
	event.BankName = strings.TrimSpace(event.BankName)
	event.RemittanceDestination = strings.TrimSpace(event.RemittanceDestination)
	event.FailureReason = strings.TrimSpace(event.FailureReason)
	event.Reason = strings.TrimSpace(event.Reason)
	event.BudgetID = strings.TrimSpace(event.BudgetID)
	event.BudgetName = strings.TrimSpace(event.BudgetName)
	event.ScopeType = strings.TrimSpace(event.ScopeType)
	event.ScopeKey = strings.TrimSpace(event.ScopeKey)
	event.ScopeValue = strings.TrimSpace(event.ScopeValue)
	event.ReportID = strings.TrimSpace(event.ReportID)
	event.ReportName = strings.TrimSpace(event.ReportName)
	event.OwnerAccount = strings.TrimSpace(event.OwnerAccount)
	event.OwnerAccountID = strings.TrimSpace(event.OwnerAccountID)
	event.OwnerRole = strings.TrimSpace(event.OwnerRole)
	event.DateRangeStart = strings.TrimSpace(event.DateRangeStart)
	event.DateRangeEnd = strings.TrimSpace(event.DateRangeEnd)
	event.Granularity = strings.TrimSpace(event.Granularity)
	event.ChartType = strings.TrimSpace(event.ChartType)
	event.Tags = normalizeStringMap(event.Tags)
	event.Attributes = normalizeStringMap(event.Attributes)
	event.Filters = normalizeStringListMap(event.Filters)
	for i := range event.Values {
		event.Values[i] = strings.TrimSpace(event.Values[i])
	}
	for i := range event.Targets {
		event.Targets[i].Value = strings.TrimSpace(event.Targets[i].Value)
	}
	for i := range event.Thresholds {
		event.Thresholds[i].ID = strings.TrimSpace(event.Thresholds[i].ID)
		event.Thresholds[i].Type = strings.TrimSpace(event.Thresholds[i].Type)
	}
	for i := range event.Groupings {
		event.Groupings[i].Type = strings.TrimSpace(event.Groupings[i].Type)
		event.Groupings[i].Key = strings.TrimSpace(event.Groupings[i].Key)
	}
	event.Metrics = normalizeStringList(event.Metrics)
	return event
}

func normalizeCheck(check Check, index int) Check {
	check.ID = strings.TrimSpace(check.ID)
	if check.ID == "" {
		check.ID = fmt.Sprintf("check-%03d", index+1)
	}
	check.Sequence = index + 1
	check.Type = CheckType(strings.ToLower(strings.TrimSpace(string(check.Type))))
	check.ReportName = strings.TrimSpace(check.ReportName)
	check.ExpectedService = strings.TrimSpace(check.ExpectedService)
	check.Category = strings.TrimSpace(check.Category)
	check.Account = strings.TrimSpace(check.Account)
	check.Service = strings.TrimSpace(check.Service)
	check.Status = strings.TrimSpace(check.Status)
	check.Tags = normalizeStringMap(check.Tags)
	return check
}

func validateEvent(event Event, index int, problems *validationProblems) {
	path := fmt.Sprintf("events[%d]", index)
	validateEventSchedule(path, event, problems)
	validateScenarioTagMap(path+".tags", event.Tags, problems)
	validateStringMap(path+".attributes", event.Attributes, problems)
	validateOptionalDate(path+".billing_period_start", event.BillingPeriodStart, problems)
	validateOptionalDate(path+".billing_period_end", event.BillingPeriodEnd, problems)
	validateOptionalDate(path+".date_range_start", event.DateRangeStart, problems)
	validateOptionalDate(path+".date_range_end", event.DateRangeEnd, problems)
	validateOptionalTimestamp(path+".usage_start_at", event.UsageStartAt, problems)
	validateOptionalTimestamp(path+".usage_end_at", event.UsageEndAt, problems)
	if event.BillingPeriodStart != "" && event.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_end is required when billing_period_start is set", path)
	}
	if event.BillingPeriodEnd != "" && event.BillingPeriodStart == "" {
		problems.add("%s.billing_period_start is required when billing_period_end is set", path)
	}
	if event.DateRangeStart != "" && event.DateRangeEnd == "" {
		problems.add("%s.date_range_end is required when date_range_start is set", path)
	}
	if event.DateRangeEnd != "" && event.DateRangeStart == "" {
		problems.add("%s.date_range_start is required when date_range_end is set", path)
	}
	validateScenarioUsageWindow(path, event, problems)

	switch event.Action {
	case "":
		problems.add("%s.action is required", path)
	case EventActionCreateAccount:
		validateCreateAccountEvent(path, event, problems)
	case EventActionCreateResource:
		validateCreateResourceEvent(path, event, problems)
	case EventActionAddUsage:
		validateAddUsageEvent(path, event, problems)
	case EventActionGenerateUsage:
		validateGenerateUsageEvent(path, event, problems)
	case EventActionAdvanceClock:
		validateAdvanceClockEvent(path, event, problems)
	case EventActionRunDailyMetering, EventActionCloseBillingPeriod, EventActionIssueBill:
		validateBillingEvent(path, event, problems)
	case EventActionRefreshCostAllocationTags:
		// The scheduled timestamp is enough; resource tags are discovered from workspace state.
	case EventActionActivateCostAllocationTag:
		validateCostAllocationTagEvent(path, event, problems)
	case EventActionCreateCostCategory:
		validateCreateCostCategoryEvent(path, event, problems)
	case EventActionCreateCostCategoryRule:
		validateCreateCostCategoryRuleEvent(path, event, problems)
	case EventActionCreateCostCategorySplitRule:
		validateCreateCostCategorySplitRuleEvent(path, event, problems)
	case EventActionCreatePaymentMethod:
		validateCreatePaymentMethodEvent(path, event, problems)
	case EventActionSchedulePayment,
		EventActionProcessPayment,
		EventActionFailPayment,
		EventActionMarkPaymentDue,
		EventActionMarkPaymentPastDue,
		EventActionCollectPayment:
		validatePaymentLifecycleEvent(path, event, problems)
	case EventActionCreateBudget:
		validateCreateBudgetEvent(path, event, problems)
	case EventActionRefreshBudgetForecasts:
		// Optional billing_period_start/end narrow the refresh; otherwise the simulator clock drives it.
	case EventActionCreateSavedReport:
		validateCreateSavedReportEvent(path, event, problems)
	default:
		problems.add("%s.action %q is not supported", path, event.Action)
	}
}

func validateEventSchedule(path string, event Event, problems *validationProblems) {
	hasDay := event.Day != 0
	hasAt := event.At != ""
	if !hasDay && !hasAt {
		problems.add("%s.day or %s.at is required", path, path)
	}
	if hasDay && event.Day <= 0 {
		problems.add("%s.day must be greater than zero", path)
	}
	if hasDay && hasAt {
		problems.add("%s must not set both day and at", path)
	}
	if hasAt {
		validateScenarioTimestamp(path+".at", event.At, problems)
	}
}

// validateCreateAccountEvent checks the author-provided fields needed to add a member account.
func validateCreateAccountEvent(path string, event Event, problems *validationProblems) {
	if event.AccountID == "" {
		problems.add("%s.account_id is required for create_account", path)
	}
	if event.Account == "" {
		problems.add("%s.account is required for create_account", path)
	}
	if event.AccountEmail == "" {
		problems.add("%s.account_email is required for create_account", path)
	}
	if event.ParentUnitID == "" {
		problems.add("%s.parent_unit_id is required for create_account", path)
	}
}

func validateCreateResourceEvent(path string, event Event, problems *validationProblems) {
	if event.Account == "" && event.AccountID == "" {
		problems.add("%s.account or %s.account_id is required for create_resource", path, path)
	}
	if event.Service == "" && event.ServiceCode == "" {
		problems.add("%s.service or %s.service_code is required for create_resource", path, path)
	}
	if event.Resource == "" && event.ResourceID == "" {
		problems.add("%s.resource or %s.resource_id is required for create_resource", path, path)
	}
}

func validateAddUsageEvent(path string, event Event, problems *validationProblems) {
	if event.Account == "" && event.AccountID == "" {
		problems.add("%s.account or %s.account_id is required for add_usage", path, path)
	}
	if event.Service == "" && event.ServiceCode == "" {
		problems.add("%s.service or %s.service_code is required for add_usage", path, path)
	}
	hasQuantity := false
	hasQuantity = validatePositiveNumber(path+".amount_gb", event.AmountGB, problems) || hasQuantity
	hasQuantity = validatePositiveNumber(path+".amount_hours", event.AmountHours, problems) || hasQuantity
	hasQuantity = validatePositiveNumber(path+".quantity", event.Quantity, problems) || hasQuantity
	if event.QuantityMicros < 0 {
		problems.add("%s.quantity_micros must be greater than zero", path)
	}
	if event.QuantityMicros > 0 {
		hasQuantity = true
		if event.Unit == "" {
			problems.add("%s.unit is required when quantity_micros is set", path)
		}
	}
	if !hasQuantity {
		problems.add("%s must include amount_gb, amount_hours, quantity, or quantity_micros", path)
	}
	if event.Quantity != nil && event.Unit == "" {
		problems.add("%s.unit is required when quantity is set", path)
	}
}

func validateGenerateUsageEvent(path string, event Event, problems *validationProblems) {
	if event.Resource == "" && event.ResourceID == "" {
		problems.add("%s.resource or %s.resource_id is required for generate_usage", path, path)
	}
	if event.Pattern == "" {
		problems.add("%s.pattern is required for generate_usage", path)
	}
	if event.Days <= 0 {
		problems.add("%s.days must be greater than zero for generate_usage", path)
	}
}

func validateAdvanceClockEvent(path string, event Event, problems *validationProblems) {
	if event.Amount <= 0 {
		problems.add("%s.amount must be greater than zero for advance_clock", path)
	}
	switch event.Unit {
	case "hours", "days", "billing_periods":
	default:
		problems.add("%s.unit must be hours, days, or billing_periods for advance_clock", path)
	}
}

func validateBillingEvent(path string, event Event, problems *validationProblems) {
	if event.PayerAccount == "" && event.PayerAccountID == "" {
		problems.add("%s.payer_account or %s.payer_account_id is required for %s", path, path, event.Action)
	}
}

// validateCostAllocationTagEvent checks the tag key named by a billing tag lifecycle event.
func validateCostAllocationTagEvent(path string, event Event, problems *validationProblems) {
	if event.TagKey == "" {
		problems.add("%s.tag_key is required for %s", path, event.Action)
		return
	}
	validateScenarioTagKey(path+".tag_key", event.TagKey, problems)
}

// validateCreateCostCategoryEvent checks the dimension fields needed for scenario-authored allocation labs.
func validateCreateCostCategoryEvent(path string, event Event, problems *validationProblems) {
	if event.Category == "" {
		problems.add("%s.category is required for create_cost_category", path)
	}
}

// validateCreateCostCategoryRuleEvent checks the single-condition rule shape supported by the scenario DSL.
func validateCreateCostCategoryRuleEvent(path string, event Event, problems *validationProblems) {
	if event.Category == "" && event.CategoryID == "" {
		problems.add("%s.category or %s.category_id is required for create_cost_category_rule", path, path)
	}
	if event.RuleOrder <= 0 {
		problems.add("%s.rule_order must be greater than zero for create_cost_category_rule", path)
	}
	if event.Value == "" {
		problems.add("%s.value is required for create_cost_category_rule", path)
	}
	validateCostCategoryRuleConditionEvent(path, event, problems)
}

func validateCostCategoryRuleConditionEvent(path string, event Event, problems *validationProblems) {
	switch event.Dimension {
	case persistence.CostCategoryRuleMatchAccount,
		persistence.CostCategoryRuleMatchService,
		persistence.CostCategoryRuleMatchRegion,
		persistence.CostCategoryRuleMatchUsageType,
		persistence.CostCategoryRuleMatchLineItemType,
		persistence.CostCategoryRuleMatchTag,
		persistence.CostCategoryRuleMatchCostCategory:
	default:
		problems.add("%s.dimension %q is not supported for create_cost_category_rule", path, event.Dimension)
	}
	switch event.Operator {
	case "", persistence.CostCategoryRuleOperatorIn, persistence.CostCategoryRuleOperatorNotIn:
	default:
		problems.add("%s.operator %q is not supported for create_cost_category_rule", path, event.Operator)
	}
	if event.Dimension == persistence.CostCategoryRuleMatchTag {
		if event.TagKey == "" {
			problems.add("%s.tag_key is required for tag cost category rules", path)
		} else {
			validateScenarioTagKey(path+".tag_key", event.TagKey, problems)
		}
	}
	if event.Dimension == persistence.CostCategoryRuleMatchCostCategory && event.ReferencedCategory == "" && event.ReferencedCategoryID == "" {
		problems.add("%s.referenced_category or %s.referenced_category_id is required for cost_category rule conditions", path, path)
	}
	if len(event.Values) == 0 {
		problems.add("%s.values is required for create_cost_category_rule", path)
	}
	validateScenarioStringList(path+".values", event.Values, problems)
}

// validateCreateCostCategorySplitRuleEvent checks target and method fields for scenario split-charge rules.
func validateCreateCostCategorySplitRuleEvent(path string, event Event, problems *validationProblems) {
	if event.Category == "" && event.CategoryID == "" {
		problems.add("%s.category or %s.category_id is required for create_cost_category_split_rule", path, path)
	}
	if event.SourceValue == "" {
		problems.add("%s.source_value is required for create_cost_category_split_rule", path)
	}
	switch event.Method {
	case persistence.CostCategorySplitMethodEven,
		persistence.CostCategorySplitMethodFixed,
		persistence.CostCategorySplitMethodProportional:
	default:
		problems.add("%s.method %q is not supported for create_cost_category_split_rule", path, event.Method)
	}
	if len(event.Targets) < 2 {
		problems.add("%s.targets must include at least two target values for create_cost_category_split_rule", path)
	}
	seen := map[string]bool{}
	fixedShareSum := 0
	for i, target := range event.Targets {
		targetPath := fmt.Sprintf("%s.targets[%d]", path, i)
		if target.Value == "" {
			problems.add("%s.value is required", targetPath)
		}
		if target.Value != "" && target.Value == event.SourceValue {
			problems.add("%s.value must not match source_value %q", targetPath, event.SourceValue)
		}
		if target.Value != "" {
			if seen[target.Value] {
				problems.add("%s.value %q is duplicated", targetPath, target.Value)
			}
			seen[target.Value] = true
		}
		if target.FixedShareMicros < 0 {
			problems.add("%s.fixed_share_micros must be zero or greater", targetPath)
		}
		if event.Method == persistence.CostCategorySplitMethodFixed {
			if target.FixedShareMicros <= 0 {
				problems.add("%s.fixed_share_micros must be greater than zero for fixed split rules", targetPath)
			}
			fixedShareSum += target.FixedShareMicros
		} else if target.FixedShareMicros != 0 {
			problems.add("%s.fixed_share_micros is only valid for fixed split rules", targetPath)
		}
	}
	if event.Method == persistence.CostCategorySplitMethodFixed && fixedShareSum != 1_000_000 {
		problems.add("%s.targets fixed_share_micros sum to %d, want 1000000", path, fixedShareSum)
	}
}

// validateCreatePaymentMethodEvent checks payment-method fields used by payment remediation labs.
func validateCreatePaymentMethodEvent(path string, event Event, problems *validationProblems) {
	if event.PaymentProfileID == "" && event.PayerAccount == "" && event.PayerAccountID == "" {
		problems.add("%s.payment_profile_id or %s.payer_account is required for create_payment_method", path, path)
	}
	if event.MethodType == "" {
		problems.add("%s.method_type is required for create_payment_method", path)
	}
	if event.DisplayName == "" {
		problems.add("%s.display_name is required for create_payment_method", path)
	}
	if event.CurrencyCode != "" && len(event.CurrencyCode) != 3 {
		problems.add("%s.currency_code must be three characters", path)
	}
	if event.AdvancePayBalanceMicros < 0 {
		problems.add("%s.advance_pay_balance_micros must be zero or greater", path)
	}
	switch event.MethodType {
	case "card":
		if event.CardBrand == "" {
			problems.add("%s.card_brand is required for card payment methods", path)
		}
		if event.AccountLast4 == "" {
			problems.add("%s.account_last4 is required for card payment methods", path)
		}
		if event.ExpirationMonth < 1 || event.ExpirationMonth > 12 || event.ExpirationYear < 2000 {
			problems.add("%s.expiration_month and %s.expiration_year are required for card payment methods", path, path)
		}
	case "ach":
		if event.BankName == "" {
			problems.add("%s.bank_name is required for ACH payment methods", path)
		}
		if event.AccountLast4 == "" {
			problems.add("%s.account_last4 is required for ACH payment methods", path)
		}
	case "invoice_remittance":
		if event.RemittanceDestination == "" {
			problems.add("%s.remittance_destination is required for invoice remittance methods", path)
		}
	case "advance_pay_balance":
	default:
		problems.add("%s.method_type %q is not supported for create_payment_method", path, event.MethodType)
	}
}

// validatePaymentLifecycleEvent keeps payment transition fixtures explicit about collected amounts.
func validatePaymentLifecycleEvent(path string, event Event, problems *validationProblems) {
	if event.AmountMicros < 0 {
		problems.add("%s.amount_micros must be zero or greater", path)
	}
	if event.Action == EventActionCollectPayment && event.AmountMicros <= 0 {
		problems.add("%s.amount_micros must be greater than zero for collect_payment", path)
	}
}

// validateCreateBudgetEvent checks the budget fields used by forecast and alert labs.
func validateCreateBudgetEvent(path string, event Event, problems *validationProblems) {
	if event.BudgetName == "" {
		problems.add("%s.budget_name is required for create_budget", path)
	}
	if event.BillingPeriodStart == "" || event.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_start and %s.billing_period_end are required for create_budget", path, path)
	}
	if event.BudgetAmountMicros <= 0 {
		problems.add("%s.budget_amount_micros must be greater than zero for create_budget", path)
	}
	switch event.ScopeType {
	case persistence.BudgetScopeAccount, persistence.BudgetScopeService:
		if event.ScopeKey != "" {
			problems.add("%s.scope_key is only supported for tag and Cost Category budgets", path)
		}
	case persistence.BudgetScopeTag, persistence.BudgetScopeCostCategory:
		if event.ScopeKey == "" {
			problems.add("%s.scope_key is required for %s budgets", path, event.ScopeType)
		}
	default:
		problems.add("%s.scope_type %q is not supported for create_budget", path, event.ScopeType)
	}
	if event.ScopeValue == "" {
		problems.add("%s.scope_value is required for create_budget", path)
	}
	if len(event.Thresholds) == 0 {
		problems.add("%s.thresholds needs at least one threshold for create_budget", path)
	}
	seen := map[string]bool{}
	for i, threshold := range event.Thresholds {
		thresholdPath := fmt.Sprintf("%s.thresholds[%d]", path, i)
		switch threshold.Type {
		case persistence.BudgetThresholdTypeActual, persistence.BudgetThresholdTypeForecast:
		default:
			problems.add("%s.type %q is not supported", thresholdPath, threshold.Type)
		}
		if threshold.BasisPoints <= 0 {
			problems.add("%s.basis_points must be greater than zero", thresholdPath)
		}
		if threshold.BasisPoints > 100000 {
			problems.add("%s.basis_points must be 100000 or fewer", thresholdPath)
		}
		key := threshold.Type + ":" + strconv.Itoa(threshold.BasisPoints)
		if seen[key] {
			problems.add("%s duplicates threshold %q", thresholdPath, key)
		}
		seen[key] = true
	}
}

// validateCreateSavedReportEvent checks the saved report starter fields used by scenario labs.
func validateCreateSavedReportEvent(path string, event Event, problems *validationProblems) {
	if event.ReportName == "" {
		problems.add("%s.report_name is required for create_saved_report", path)
	}
	if event.OwnerAccount == "" && event.OwnerAccountID == "" {
		problems.add("%s.owner_account or %s.owner_account_id is required for create_saved_report", path, path)
	}
	if event.DateRangeStart == "" || event.DateRangeEnd == "" {
		problems.add("%s.date_range_start and %s.date_range_end are required for create_saved_report", path, path)
	}
	if event.DateRangeStart != "" && event.DateRangeEnd != "" {
		start, startOK := parseScenarioDateOnly(event.DateRangeStart)
		end, endOK := parseScenarioDateOnly(event.DateRangeEnd)
		if startOK && endOK && !start.Before(end) {
			problems.add("%s.date_range_start must be before date_range_end", path)
		}
	}
	switch event.OwnerRole {
	case "", "management-account", "member-account", "finance", "instructor":
	default:
		problems.add("%s.owner_role %q is not supported for create_saved_report", path, event.OwnerRole)
	}
	switch event.Granularity {
	case "", "hourly", "daily", "monthly":
	default:
		problems.add("%s.granularity %q is not supported for create_saved_report", path, event.Granularity)
	}
	switch event.ChartType {
	case "", "table", "line", "bar", "stacked_bar":
	default:
		problems.add("%s.chart_type %q is not supported for create_saved_report", path, event.ChartType)
	}
	validateScenarioStringListMap(path+".filters", event.Filters, problems)
	if len(event.Groupings) > 2 {
		problems.add("%s.groupings supports at most two values", path)
	}
	seenGroupings := map[string]bool{}
	for i, grouping := range event.Groupings {
		groupPath := fmt.Sprintf("%s.groupings[%d]", path, i)
		switch grouping.Type {
		case "dimension", "tag", "cost_category":
		default:
			problems.add("%s.type %q is not supported", groupPath, grouping.Type)
		}
		if grouping.Key == "" {
			problems.add("%s.key is required", groupPath)
		}
		key := grouping.Type + ":" + grouping.Key
		if seenGroupings[key] {
			problems.add("%s duplicates grouping %q", groupPath, key)
		}
		seenGroupings[key] = true
	}
	validateSavedReportMetrics(path+".metrics", event.Metrics, problems)
}

func validateCheck(check Check, index int, problems *validationProblems) {
	path := fmt.Sprintf("checks[%d]", index)
	validateScenarioTagMap(path+".tags", check.Tags, problems)
	validatePositiveNumber(path+".expected_value", check.ExpectedValue, problems)

	switch check.Type {
	case "":
		problems.add("%s.type is required", path)
	case CheckTypeSavedReportExists:
		if check.ReportName == "" {
			problems.add("%s.report_name is required for saved_report_exists", path)
		}
	case CheckTypeIdentifiesTopDriver:
		if check.ExpectedService == "" {
			problems.add("%s.expected_service is required for identifies_top_driver", path)
		}
	case CheckTypeCostCategoryRuleCreated:
		if check.Category == "" {
			problems.add("%s.category is required for cost_category_rule_created", path)
		}
	default:
		problems.add("%s.type %q is not supported", path, check.Type)
	}
}

func validateScenarioSemantics(definition Definition, problems *validationProblems) {
	startTime, hasStart := scenarioDefinitionStart(definition.Clock.Start)
	createdAccounts := map[string]string{}
	for i, event := range definition.Events {
		path := fmt.Sprintf("events[%d]", i)
		validateScenarioEventAccountReferences(path, definition.OrganizationTemplate, event, createdAccounts, problems)
		validateScenarioEventService(path, event, problems)
		validateScenarioEventTimeWindow(path, startTime, hasStart, event, problems)
		validateScenarioBillingPeriodWindow(path, event, problems)
		rememberScenarioCreatedAccount(createdAccounts, event)
	}

	for i, check := range definition.Checks {
		path := fmt.Sprintf("checks[%d]", i)
		validateScenarioAccountReference(path+".account", definition.OrganizationTemplate, "", check.Account, createdAccounts, problems)
		validateScenarioCheckService(path+".expected_service", check.ExpectedService, problems)
		validateScenarioCheckService(path+".service", check.Service, problems)
	}
}

func validateScenarioEventAccountReferences(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	switch event.Action {
	case EventActionCreateResource, EventActionAddUsage:
		validateScenarioAccountReference(path+".account", organizationTemplate, event.AccountID, event.Account, createdAccounts, problems)
	case EventActionRunDailyMetering, EventActionCloseBillingPeriod, EventActionIssueBill:
		validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
	case EventActionCreateSavedReport:
		validateScenarioAccountReference(path+".owner_account", organizationTemplate, event.OwnerAccountID, event.OwnerAccount, createdAccounts, problems)
	default:
		validateScenarioAccountReference(path+".account", organizationTemplate, event.AccountID, event.Account, createdAccounts, problems)
		validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
	}
}

func validateScenarioAccountReference(path, organizationTemplate, explicitID, name string, createdAccounts map[string]string, problems *validationProblems) {
	if strings.TrimSpace(explicitID) != "" || strings.TrimSpace(name) == "" {
		return
	}
	if accountID := createdAccounts[scenarioLookupKey(name)]; accountID != "" {
		return
	}
	if !persistence.IsAnyCompanyRetailTemplate(organizationTemplate) {
		return
	}
	if _, ok := persistence.AnyCompanyRetailAccountIDForName(name); ok {
		return
	}
	problems.add("%s %q is not in organization_template %q; use account_id or one of: %s", path, name, organizationTemplate, strings.Join(persistence.AnyCompanyRetailAccountNames(), ", "))
}

// rememberScenarioCreatedAccount makes earlier create_account events available to later references.
func rememberScenarioCreatedAccount(createdAccounts map[string]string, event Event) {
	if event.Action != EventActionCreateAccount || strings.TrimSpace(event.AccountID) == "" {
		return
	}
	for _, alias := range []string{event.Account, event.AccountID, event.ID} {
		key := scenarioLookupKey(alias)
		if key != "" {
			createdAccounts[key] = event.AccountID
		}
	}
}

func validateScenarioEventService(path string, event Event, problems *validationProblems) {
	switch event.Action {
	case EventActionCreateResource, EventActionAddUsage:
	default:
		return
	}
	if event.Service == "" && event.ServiceCode == "" {
		return
	}
	if _, err := scenarioServiceDefaultsForEvent(event); err != nil {
		problems.add("%s service %q is not supported; use one of: %s, or set service_code with usage_type, operation, and unit for custom usage", path, chooseFirst(event.Service, event.ServiceCode), supportedScenarioServiceList())
	}
}

func validateScenarioCheckService(path, value string, problems *validationProblems) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if scenarioServiceCodeForName(value) != "" {
		return
	}
	if _, ok := scenarioServiceDefaultsByCode()[strings.TrimSpace(value)]; ok {
		return
	}
	problems.add("%s %q is not supported; use one of: %s", path, value, supportedScenarioServiceList())
}

func validateScenarioEventTimeWindow(path string, startTime time.Time, hasStart bool, event Event, problems *validationProblems) {
	if !hasStart {
		return
	}
	scheduledAt, err := scheduledEventTime(startTime, event)
	if err != nil {
		return
	}
	if scheduledAt.Before(startTime) {
		problems.add("%s schedules at %s before clock.start %s", path, scheduledAt.Format(time.RFC3339), startTime.Format(time.DateOnly))
	}
}

func validateScenarioBillingPeriodWindow(path string, event Event, problems *validationProblems) {
	if event.BillingPeriodStart == "" || event.BillingPeriodEnd == "" {
		return
	}
	start, startOK := parseScenarioDateOnly(event.BillingPeriodStart)
	end, endOK := parseScenarioDateOnly(event.BillingPeriodEnd)
	if !startOK || !endOK {
		return
	}
	if !start.Before(end) {
		problems.add("%s.billing_period_start must be before billing_period_end", path)
		return
	}
	if start.Day() != 1 || end.Day() != 1 || !start.AddDate(0, 1, 0).Equal(end) {
		problems.add("%s billing period must cover exactly one UTC calendar month", path)
	}
}

func scenarioDefinitionStart(value string) (time.Time, bool) {
	parsed, ok := parseScenarioDateOnly(value)
	if !ok {
		return time.Time{}, false
	}
	return parsed, true
}

func parseScenarioDateOnly(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), true
}

func validateScenarioTimestamp(path, value string, problems *validationProblems) {
	if _, err := time.Parse(time.DateOnly, value); err == nil {
		return
	}
	if _, err := time.Parse(time.RFC3339, value); err == nil {
		return
	}
	problems.add("%s must use YYYY-MM-DD or RFC3339", path)
}

func validateOptionalDate(path, value string, problems *validationProblems) {
	if value == "" {
		return
	}
	if _, err := time.Parse(time.DateOnly, value); err != nil {
		problems.add("%s must use YYYY-MM-DD", path)
	}
}

func validateOptionalTimestamp(path, value string, problems *validationProblems) {
	if value == "" {
		return
	}
	validateScenarioTimestamp(path, value, problems)
}

func validateScenarioUsageWindow(path string, event Event, problems *validationProblems) {
	if event.UsageStartAt == "" && event.UsageEndAt == "" {
		return
	}
	if event.Action != EventActionAddUsage {
		problems.add("%s.usage_start_at and %s.usage_end_at are only supported for add_usage", path, path)
		return
	}
	if event.UsageStartAt == "" || event.UsageEndAt == "" {
		problems.add("%s.usage_start_at and %s.usage_end_at must be set together", path, path)
		return
	}
	start, startErr := parseScenarioEventTime(event.UsageStartAt)
	end, endErr := parseScenarioEventTime(event.UsageEndAt)
	if startErr == nil && endErr == nil && !start.Before(end) {
		problems.add("%s.usage_start_at must be before usage_end_at", path)
	}
}

func validatePositiveNumber(path string, number *json.Number, problems *validationProblems) bool {
	if number == nil {
		return false
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsInf(value, 0) || value <= 0 {
		problems.add("%s must be greater than zero", path)
		return false
	}
	return true
}

func validateStringMap(path string, values map[string]string, problems *validationProblems) {
	for key := range values {
		if strings.TrimSpace(key) == "" {
			problems.add("%s key is required", path)
		}
	}
}

func validateScenarioStringList(path string, values []string, problems *validationProblems) {
	seen := map[string]bool{}
	for i, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			problems.add("%s[%d] is required", path, i)
			continue
		}
		if seen[value] {
			problems.add("%s[%d] %q is duplicated", path, i, value)
		}
		seen[value] = true
	}
}

func validateScenarioStringListMap(path string, values map[string][]string, problems *validationProblems) {
	for key, list := range values {
		if strings.TrimSpace(key) == "" {
			problems.add("%s key is required", path)
		}
		if len(list) == 0 {
			problems.add("%s.%s needs at least one value", path, key)
		}
		validateScenarioStringList(path+"."+key, list, problems)
	}
}

func validateSavedReportMetrics(path string, values []string, problems *validationProblems) {
	seen := map[string]bool{}
	for i, value := range values {
		if value == "" {
			problems.add("%s[%d] is required", path, i)
			continue
		}
		switch value {
		case "unblended_cost", "blended_cost", "amortized_cost", "usage_quantity", "net_cost":
		default:
			problems.add("%s[%d] %q is not supported", path, i, value)
		}
		if seen[value] {
			problems.add("%s[%d] %q is duplicated", path, i, value)
		}
		seen[value] = true
	}
}

func validateScenarioTagMap(path string, values map[string]string, problems *validationProblems) {
	for key := range values {
		validateScenarioTagKey(path, key, problems)
	}
}

func validateScenarioTagKey(path, key string, problems *validationProblems) {
	key = strings.TrimSpace(key)
	if key == "" {
		problems.add("%s key is required", path)
		return
	}
	if len(key) > 128 {
		problems.add("%s key %q must be 128 bytes or fewer", path, key)
	}
	if strings.HasPrefix(strings.ToLower(key), "aws:") {
		problems.add("%s key %q must not start with aws:", path, key)
	}
	for _, char := range key {
		if isScenarioTagKeyRune(char) {
			continue
		}
		problems.add("%s key %q may contain letters, numbers, spaces, and + - = . _ : / @", path, key)
		return
	}
}

func isScenarioTagKeyRune(char rune) bool {
	if char >= 'a' && char <= 'z' {
		return true
	}
	if char >= 'A' && char <= 'Z' {
		return true
	}
	if char >= '0' && char <= '9' {
		return true
	}
	switch char {
	case ' ', '+', '-', '=', '.', '_', ':', '/', '@':
		return true
	default:
		return false
	}
}

func normalizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	normalized := make(map[string]string, len(values))
	for key, value := range values {
		normalized[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return normalized
}

func normalizeStringListMap(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return map[string][]string{}
	}
	normalized := make(map[string][]string, len(values))
	for key, list := range values {
		normalized[strings.TrimSpace(key)] = normalizeStringList(list)
	}
	return normalized
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		normalized = append(normalized, strings.TrimSpace(value))
	}
	return normalized
}

type validationProblems []string

func (p *validationProblems) add(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

func supportedScenarioServiceList() string {
	names := make([]string, 0, len(scenarioServiceDefaultsByCode()))
	for _, defaults := range scenarioServiceDefaultsByCode() {
		names = append(names, defaults.ServiceName)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
