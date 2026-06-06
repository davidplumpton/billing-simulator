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
)

// Event describes one ordered resource, usage, clock, or billing operation.
type Event struct {
	ID                   string              `json:"id,omitempty"`
	Sequence             int                 `json:"-"`
	Day                  int                 `json:"day,omitempty"`
	At                   string              `json:"at,omitempty"`
	Action               EventAction         `json:"action"`
	Account              string              `json:"account,omitempty"`
	AccountID            string              `json:"account_id,omitempty"`
	AccountEmail         string              `json:"account_email,omitempty"`
	OrganizationID       string              `json:"organization_id,omitempty"`
	ParentUnitID         string              `json:"parent_unit_id,omitempty"`
	PayerAccount         string              `json:"payer_account,omitempty"`
	PayerAccountID       string              `json:"payer_account_id,omitempty"`
	Service              string              `json:"service,omitempty"`
	ServiceCode          string              `json:"service_code,omitempty"`
	Resource             string              `json:"resource,omitempty"`
	ResourceID           string              `json:"resource_id,omitempty"`
	ResourceType         string              `json:"resource_type,omitempty"`
	Region               string              `json:"region,omitempty"`
	Status               string              `json:"status,omitempty"`
	TagKey               string              `json:"tag_key,omitempty"`
	Tags                 map[string]string   `json:"tags,omitempty"`
	Attributes           map[string]string   `json:"attributes,omitempty"`
	UsageType            string              `json:"usage_type,omitempty"`
	Operation            string              `json:"operation,omitempty"`
	Amount               int                 `json:"amount,omitempty"`
	AmountGB             *json.Number        `json:"amount_gb,omitempty"`
	AmountHours          *json.Number        `json:"amount_hours,omitempty"`
	Quantity             *json.Number        `json:"quantity,omitempty"`
	QuantityMicros       int64               `json:"quantity_micros,omitempty"`
	Unit                 string              `json:"unit,omitempty"`
	Pattern              string              `json:"pattern,omitempty"`
	Days                 int                 `json:"days,omitempty"`
	BillingPeriodStart   string              `json:"billing_period_start,omitempty"`
	BillingPeriodEnd     string              `json:"billing_period_end,omitempty"`
	Category             string              `json:"category,omitempty"`
	CategoryID           string              `json:"category_id,omitempty"`
	DefaultValue         string              `json:"default_value,omitempty"`
	Description          string              `json:"description,omitempty"`
	RuleOrder            int                 `json:"rule_order,omitempty"`
	Value                string              `json:"value,omitempty"`
	MatchType            string              `json:"match_type,omitempty"`
	Dimension            string              `json:"dimension,omitempty"`
	Operator             string              `json:"operator,omitempty"`
	Values               []string            `json:"values,omitempty"`
	ReferencedCategory   string              `json:"referenced_category,omitempty"`
	ReferencedCategoryID string              `json:"referenced_category_id,omitempty"`
	SourceValue          string              `json:"source_value,omitempty"`
	Method               string              `json:"method,omitempty"`
	Targets              []SplitChargeTarget `json:"targets,omitempty"`
}

// SplitChargeTarget describes one scenario-authored split-charge allocation target.
type SplitChargeTarget struct {
	Value            string `json:"value"`
	FixedShareMicros int    `json:"fixed_share_micros,omitempty"`
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

// ParseDefinition parses a JSON scenario definition from a bounded reader.
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

// ParseDefinitionBytes parses a JSON scenario definition from memory.
func ParseDefinitionBytes(data []byte) (Definition, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return Definition{}, fmt.Errorf("scenario definition is required")
	}
	if data[0] != '{' {
		return Definition{}, fmt.Errorf("scenario definition must be a JSON object")
	}

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
	event.Tags = normalizeStringMap(event.Tags)
	event.Attributes = normalizeStringMap(event.Attributes)
	for i := range event.Values {
		event.Values[i] = strings.TrimSpace(event.Values[i])
	}
	for i := range event.Targets {
		event.Targets[i].Value = strings.TrimSpace(event.Targets[i].Value)
	}
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
	if event.BillingPeriodStart != "" && event.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_end is required when billing_period_start is set", path)
	}
	if event.BillingPeriodEnd != "" && event.BillingPeriodStart == "" {
		problems.add("%s.billing_period_start is required when billing_period_end is set", path)
	}

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
