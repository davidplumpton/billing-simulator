package scenario

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDefinitionParsesScenarioJSON(t *testing.T) {
	raw := []byte(`{
		"name": "Find the untagged data-transfer spike",
		"clock": {
			"start": "2026-03-01"
		},
		"organization_template": "anycompany-retail",
		"random_seed": 42,
		"events": [
			{
				"day": 3,
				"action": "create_resource",
				"account": " Storefront Prod ",
				"service": "Amazon S3",
				"resource": "s3://storefront-assets",
				"resource_type": "bucket",
				"region": "us-east-1",
				"tags": {
					" app ": " storefront ",
					"env": "prod",
					"owner": "web-platform"
				},
				"attributes": {
					"storage_class": " standard "
				}
			},
			{
				"id": "data-transfer-spike",
				"day": 12,
				"action": "add_usage",
				"service": "AWS Data Transfer",
				"account": "Shared Networking",
				"amount_gb": 4000,
				"tags": {}
			},
			{
				"at": "2026-03-15T00:00:00Z",
				"action": "generate_usage",
				"resource_id": "res-storefront-assets",
				"pattern": "storage_growth",
				"days": 5
			},
			{
				"day": 32,
				"action": "run_daily_metering",
				"payer_account": "Management"
			},
			{
				"day": 33,
				"action": "close_billing_period",
				"payer_account_id": "payer-001",
				"billing_period_start": "2026-03-01",
				"billing_period_end": "2026-04-01"
			}
		],
		"checks": [
			{
				"type": "saved_report_exists",
				"report_name": "Untagged spend by service"
			},
			{
				"type": "identifies_top_driver",
				"expected_service": "AWS Data Transfer"
			},
			{
				"type": "cost_category_rule_created",
				"category": "Product"
			}
		]
	}`)

	definition, err := ParseDefinitionBytes(raw)
	if err != nil {
		t.Fatalf("ParseDefinitionBytes returned error: %v", err)
	}

	if definition.Name != "Find the untagged data-transfer spike" {
		t.Fatalf("definition name = %q", definition.Name)
	}
	if definition.Clock.Start != "2026-03-01" {
		t.Fatalf("clock start = %q", definition.Clock.Start)
	}
	if definition.OrganizationTemplate != "anycompany-retail" {
		t.Fatalf("organization template = %q", definition.OrganizationTemplate)
	}
	if definition.RandomSeed != 42 {
		t.Fatalf("random seed = %d", definition.RandomSeed)
	}
	if len(definition.Events) != 5 {
		t.Fatalf("event count = %d", len(definition.Events))
	}
	firstEvent := definition.Events[0]
	if firstEvent.ID != "event-001" || firstEvent.Sequence != 1 {
		t.Fatalf("first event lineage = %q/%d", firstEvent.ID, firstEvent.Sequence)
	}
	if firstEvent.Account != "Storefront Prod" {
		t.Fatalf("first event account = %q", firstEvent.Account)
	}
	if firstEvent.Tags["app"] != "storefront" {
		t.Fatalf("first event app tag = %q", firstEvent.Tags["app"])
	}
	if firstEvent.Attributes["storage_class"] != "standard" {
		t.Fatalf("first event storage_class attribute = %q", firstEvent.Attributes["storage_class"])
	}
	secondEvent := definition.Events[1]
	if secondEvent.ID != "data-transfer-spike" || secondEvent.Sequence != 2 {
		t.Fatalf("second event lineage = %q/%d", secondEvent.ID, secondEvent.Sequence)
	}
	if secondEvent.AmountGB == nil || secondEvent.AmountGB.String() != "4000" {
		t.Fatalf("second event amount_gb = %#v", secondEvent.AmountGB)
	}
	if definition.Events[4].BillingPeriodStart != "2026-03-01" {
		t.Fatalf("close event billing period start = %q", definition.Events[4].BillingPeriodStart)
	}
	if len(definition.Checks) != 3 {
		t.Fatalf("check count = %d", len(definition.Checks))
	}
	if definition.Checks[0].ID != "check-001" || definition.Checks[0].Sequence != 1 {
		t.Fatalf("first check lineage = %q/%d", definition.Checks[0].ID, definition.Checks[0].Sequence)
	}
}

func TestParseDefinitionParsesScenarioYAML(t *testing.T) {
	raw := strings.NewReader(`
name: YAML allocation lab
clock:
  start: 2026-03-01
organization_template: anycompany-retail
random_seed: 7
events:
  - id: create-yaml-web
    day: 1
    action: create_resource
    account: Storefront Prod
    service: Amazon EC2
    resource: yaml-web
    resource_type: ec2_instance
    region: us-east-1
    tags: {app: storefront, env: prod}
    attributes:
      instance_type: t3.medium
  - id: yaml-web-hours
    day: 2
    action: add_usage
    account: Storefront Prod
    service: Amazon EC2
    resource: yaml-web
    amount_hours: 8
checks:
  - type: saved_report_exists
    report_name: YAML spend review
`)

	definition, err := ParseDefinition(raw)
	if err != nil {
		t.Fatalf("ParseDefinition(YAML) returned error: %v", err)
	}
	if definition.Name != "YAML allocation lab" ||
		definition.Clock.Start != "2026-03-01" ||
		definition.OrganizationTemplate != "anycompany-retail" ||
		definition.RandomSeed != 7 {
		t.Fatalf("YAML definition header = %+v", definition)
	}
	if len(definition.Events) != 2 || definition.Events[0].Sequence != 1 || definition.Events[1].Sequence != 2 {
		t.Fatalf("YAML events = %+v, want two sequenced events", definition.Events)
	}
	if definition.Events[0].Tags["app"] != "storefront" || definition.Events[0].Attributes["instance_type"] != "t3.medium" {
		t.Fatalf("YAML resource maps = tags:%+v attributes:%+v", definition.Events[0].Tags, definition.Events[0].Attributes)
	}
	if definition.Events[1].AmountHours == nil || definition.Events[1].AmountHours.String() != "8" {
		t.Fatalf("YAML amount_hours = %#v, want json number 8", definition.Events[1].AmountHours)
	}
	if len(definition.Checks) != 1 || definition.Checks[0].ID != "check-001" {
		t.Fatalf("YAML checks = %+v, want normalized check", definition.Checks)
	}
}

func TestParseDefinitionParsesAssessmentCheckFields(t *testing.T) {
	raw := []byte(`{
		"name": "Assessment check fields",
		"clock": {
			"start": "2026-03-01"
		},
		"organization_template": "anycompany-retail",
		"checks": [
			{
				"id": "tag-active",
				"type": "cost_allocation_tag_activated",
				"tag_key": " owner ",
				"status": " active "
			},
			{
				"id": "bill-balanced",
				"type": "bill_reconciled",
				"payer_account": " Management ",
				"billing_period_start": "2026-03-01",
				"billing_period_end": "2026-04-01"
			},
			{
				"id": "payment-due",
				"type": "payment_status",
				"payer_account_id": " 000011112222 ",
				"status": " due "
			}
		]
	}`)

	definition, err := ParseDefinitionBytes(raw)
	if err != nil {
		t.Fatalf("ParseDefinitionBytes returned error: %v", err)
	}
	if len(definition.Checks) != 3 {
		t.Fatalf("check count = %d", len(definition.Checks))
	}
	if definition.Checks[0].TagKey != "owner" || definition.Checks[0].Status != "active" {
		t.Fatalf("tag check = %+v, want trimmed tag key and normalized status", definition.Checks[0])
	}
	if definition.Checks[1].PayerAccount != "Management" ||
		definition.Checks[1].BillingPeriodStart != "2026-03-01" ||
		definition.Checks[1].BillingPeriodEnd != "2026-04-01" {
		t.Fatalf("bill check = %+v, want payer and period fields", definition.Checks[1])
	}
	if definition.Checks[2].PayerAccountID != "000011112222" || definition.Checks[2].Status != "due" {
		t.Fatalf("payment check = %+v, want payer account ID and status", definition.Checks[2])
	}
}

func TestParseDefinitionRejectsInvalidScenario(t *testing.T) {
	raw := []byte(`{
		"name": " ",
		"clock": {
			"start": "March 2026"
		},
		"organization_template": "",
		"random_seed": -1,
		"events": [
			{
				"day": 0,
				"action": "create_resource",
				"service": "Amazon S3",
				"resource": "s3://storefront-assets",
				"tags": {
					" ": "missing-key"
				}
			},
			{
				"day": 2,
				"at": "2026-03-02",
				"action": "add_usage",
				"account": "Shared Networking",
				"service": "AWS Data Transfer",
				"amount_gb": -1
			},
			{
				"day": 3,
				"action": "unsupported_action"
			}
		],
		"checks": [
			{
				"type": "saved_report_exists"
			},
			{
				"type": "unknown_check"
			}
		]
	}`)

	_, err := ParseDefinitionBytes(raw)
	if err == nil {
		t.Fatal("ParseDefinitionBytes succeeded, want validation error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want ValidationError: %v", err, err)
	}
	assertErrorContains(t, err, "name is required")
	assertErrorContains(t, err, "clock.start must use YYYY-MM-DD")
	assertErrorContains(t, err, "organization_template is required")
	assertErrorContains(t, err, "random_seed must be zero or greater")
	assertErrorContains(t, err, "events[0].day or events[0].at is required")
	assertErrorContains(t, err, "events[0].tags key is required")
	assertErrorContains(t, err, "events[0].account or events[0].account_id is required for create_resource")
	assertErrorContains(t, err, "events[1] must not set both day and at")
	assertErrorContains(t, err, "events[1].amount_gb must be greater than zero")
	assertErrorContains(t, err, "events[2].action \"unsupported_action\" is not supported")
	assertErrorContains(t, err, "checks[0].report_name is required for saved_report_exists")
	assertErrorContains(t, err, "checks[1].type \"unknown_check\" is not supported")
}

func TestParseDefinitionRejectsInvalidAssessmentChecks(t *testing.T) {
	raw := []byte(`{
		"name": "Broken assessment checks",
		"clock": {
			"start": "2026-03-01"
		},
		"organization_template": "anycompany-retail",
		"checks": [
			{
				"type": "cost_allocation_tag_activated"
			},
			{
				"type": "bill_reconciled",
				"billing_period_start": "2026-03-01",
				"status": "unknown"
			},
			{
				"type": "payment_status"
			}
		]
	}`)

	_, err := ParseDefinitionBytes(raw)
	if err == nil {
		t.Fatal("ParseDefinitionBytes succeeded, want validation error")
	}
	assertErrorContains(t, err, "checks[0].tag_key is required for cost_allocation_tag_activated")
	assertErrorContains(t, err, "checks[1].billing_period_end is required when billing_period_start is set")
	assertErrorContains(t, err, `checks[1].status "unknown" is not supported for bill_reconciled`)
	assertErrorContains(t, err, "checks[2].status is required for payment_status")
}

func TestParseDefinitionRejectsInvalidScenarioYAML(t *testing.T) {
	raw := strings.NewReader(`
name: ""
clock:
  start: March 2026
organization_template: ""
random_seed: -1
events:
  - id: missing-usage-quantity
    day: 1
    action: add_usage
    account: Ghost Account
    service: Amazon EC2
checks:
  - type: saved_report_exists
`)

	_, err := ParseDefinition(raw)
	if err == nil {
		t.Fatal("ParseDefinition(YAML) succeeded, want validation error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want ValidationError: %v", err, err)
	}
	assertErrorContains(t, err, "name is required")
	assertErrorContains(t, err, "clock.start must use YYYY-MM-DD")
	assertErrorContains(t, err, "organization_template is required")
	assertErrorContains(t, err, "random_seed must be zero or greater")
	assertErrorContains(t, err, "events[0] must include amount_gb, amount_hours, quantity, or quantity_micros")
	assertErrorContains(t, err, "checks[0].report_name is required for saved_report_exists")
}

func TestParseDefinitionValidatesCostAllocationTagEvents(t *testing.T) {
	raw := []byte(`{
		"name": "Broken tag lifecycle fixture",
		"clock": {
			"start": "2026-03-01"
		},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "discover-tags",
				"day": 1,
				"action": "refresh_cost_allocation_tags"
			},
			{
				"id": "missing-tag-key",
				"day": 2,
				"action": "activate_cost_allocation_tag"
			},
			{
				"id": "bad-tag-key",
				"day": 3,
				"action": "activate_cost_allocation_tag",
				"tag_key": "aws:owner"
			}
		]
	}`)

	_, err := ParseDefinitionBytes(raw)
	if err == nil {
		t.Fatal("ParseDefinitionBytes succeeded, want cost allocation tag validation errors")
	}
	assertErrorContains(t, err, "events[1].tag_key is required for activate_cost_allocation_tag")
	assertErrorContains(t, err, `events[2].tag_key key "aws:owner" must not start with aws:`)
}

func TestParseDefinitionValidatesCostCategoryEvents(t *testing.T) {
	raw := []byte(`{
		"name": "Broken allocation fixture",
		"clock": {
			"start": "2026-03-01"
		},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "missing-category",
				"day": 1,
				"action": "create_cost_category"
			},
			{
				"id": "bad-rule",
				"day": 2,
				"action": "create_cost_category_rule",
				"category": "Product",
				"rule_order": 0,
				"value": "Shared",
				"dimension": "tag",
				"values": []
			},
			{
				"id": "bad-split",
				"day": 3,
				"action": "create_cost_category_split_rule",
				"category": "Product",
				"source_value": "Shared",
				"method": "fixed",
				"targets": [
					{"value": "Storefront", "fixed_share_micros": 700000},
					{"value": "Storefront", "fixed_share_micros": 200000}
				]
			}
		]
	}`)

	_, err := ParseDefinitionBytes(raw)
	if err == nil {
		t.Fatal("ParseDefinitionBytes succeeded, want cost category validation errors")
	}
	assertErrorContains(t, err, "events[0].category is required for create_cost_category")
	assertErrorContains(t, err, "events[1].rule_order must be greater than zero for create_cost_category_rule")
	assertErrorContains(t, err, "events[1].tag_key is required for tag cost category rules")
	assertErrorContains(t, err, "events[1].values is required for create_cost_category_rule")
	assertErrorContains(t, err, `events[2].targets[1].value "Storefront" is duplicated`)
	assertErrorContains(t, err, "events[2].targets fixed_share_micros sum to 900000, want 1000000")
}

func TestParseDefinitionValidatesPaymentLifecycleEvents(t *testing.T) {
	raw := []byte(`{
		"name": "Broken payment lifecycle fixture",
		"clock": {
			"start": "2026-03-01"
		},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "bad-failure-amount",
				"day": 1,
				"action": "fail_payment",
				"amount_micros": -1
			},
			{
				"id": "missing-collect-amount",
				"day": 2,
				"action": "collect_payment"
			}
		]
	}`)

	_, err := ParseDefinitionBytes(raw)
	if err == nil {
		t.Fatal("ParseDefinitionBytes succeeded, want payment lifecycle validation errors")
	}
	assertErrorContains(t, err, "events[0].amount_micros must be zero or greater")
	assertErrorContains(t, err, "events[1].amount_micros must be greater than zero for collect_payment")
}

func TestParseDefinitionValidatesBudgetAndReportEvents(t *testing.T) {
	raw := []byte(`{
		"name": "Broken budget fixture",
		"clock": {
			"start": "2026-02-01"
		},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "bad-window",
				"day": 1,
				"action": "add_usage",
				"account": "Storefront Prod",
				"service": "Amazon EC2",
				"amount_hours": 1,
				"usage_start_at": "2026-02-10T00:00:00Z",
				"usage_end_at": "2026-02-09T00:00:00Z"
			},
			{
				"id": "bad-budget",
				"day": 2,
				"action": "create_budget",
				"budget_amount_micros": -1,
				"scope_type": "bad-scope",
				"thresholds": [
					{"type": "forecast", "basis_points": 0},
					{"type": "forecast", "basis_points": 0}
				]
			},
			{
				"id": "bad-report",
				"day": 3,
				"action": "create_saved_report",
				"owner_account": "Ghost Account",
				"date_range_start": "2026-03-01",
				"date_range_end": "2026-02-01",
				"granularity": "weekly",
				"chart_type": "pie",
				"filters": {
					"service": ["Amazon EC2", "Amazon EC2"]
				},
				"groupings": [
					{"type": "dimension", "key": "service"},
					{"type": "dimension", "key": "service"},
					{"type": "bad", "key": ""}
				],
				"metrics": ["bogus"]
			}
		]
	}`)

	_, err := ParseDefinitionBytes(raw)
	if err == nil {
		t.Fatal("ParseDefinitionBytes succeeded, want budget/report validation errors")
	}
	assertErrorContains(t, err, "events[0].usage_start_at must be before usage_end_at")
	assertErrorContains(t, err, "events[1].budget_name is required for create_budget")
	assertErrorContains(t, err, "events[1].billing_period_start and events[1].billing_period_end are required for create_budget")
	assertErrorContains(t, err, "events[1].budget_amount_micros must be greater than zero for create_budget")
	assertErrorContains(t, err, `events[1].scope_type "bad-scope" is not supported for create_budget`)
	assertErrorContains(t, err, "events[1].scope_value is required for create_budget")
	assertErrorContains(t, err, "events[1].thresholds[0].basis_points must be greater than zero")
	assertErrorContains(t, err, `events[1].thresholds[1] duplicates threshold "forecast:0"`)
	assertErrorContains(t, err, "events[2].report_name is required for create_saved_report")
	assertErrorContains(t, err, "events[2].date_range_start must be before date_range_end")
	assertErrorContains(t, err, `events[2].granularity "weekly" is not supported for create_saved_report`)
	assertErrorContains(t, err, `events[2].chart_type "pie" is not supported for create_saved_report`)
	assertErrorContains(t, err, `events[2].filters.service[1] "Amazon EC2" is duplicated`)
	assertErrorContains(t, err, "events[2].groupings supports at most two values")
	assertErrorContains(t, err, `events[2].groupings[1] duplicates grouping "dimension:service"`)
	assertErrorContains(t, err, `events[2].groupings[2].type "bad" is not supported`)
	assertErrorContains(t, err, "events[2].groupings[2].key is required")
	assertErrorContains(t, err, `events[2].metrics[0] "bogus" is not supported`)
	assertErrorContains(t, err, `events[2].owner_account "Ghost Account" is not in organization_template "anycompany-retail"`)
}

func TestParseDefinitionReportsActionableScenarioErrors(t *testing.T) {
	raw := []byte(`{
		"name": "Broken authoring fixture",
		"clock": {
			"start": "2026-03-01"
		},
		"organization_template": "anycompany-retail",
		"events": [
			{
				"id": "unknown-account",
				"day": 1,
				"action": "create_resource",
				"account": "Ghost Account",
				"service": "Amazon S3",
				"resource": "s3://ghost-assets",
				"tags": {
					"aws:owner": "system",
					"owner#": "platform"
				}
			},
			{
				"id": "unsupported-service",
				"day": 2,
				"action": "add_usage",
				"account": "Storefront Prod",
				"service": "Imaginary Compute",
				"amount_gb": 1
			},
			{
				"id": "before-start",
				"at": "2026-02-28",
				"action": "run_daily_metering",
				"payer_account": "Management"
			},
			{
				"id": "bad-period",
				"day": 3,
				"action": "close_billing_period",
				"payer_account": "Nobody",
				"billing_period_start": "2026-04-01",
				"billing_period_end": "2026-03-01"
			}
		],
		"checks": [
			{
				"id": "unsupported-check",
				"type": "manually_review_console"
			},
			{
				"id": "unsupported-check-service",
				"type": "identifies_top_driver",
				"expected_service": "Imaginary Compute"
			}
		]
	}`)

	_, err := ParseDefinitionBytes(raw)
	if err == nil {
		t.Fatal("ParseDefinitionBytes succeeded, want actionable validation error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want ValidationError: %v", err, err)
	}
	assertErrorContains(t, err, `events[0].account "Ghost Account" is not in organization_template "anycompany-retail"`)
	assertErrorContains(t, err, `events[0].tags key "aws:owner" must not start with aws:`)
	assertErrorContains(t, err, `events[0].tags key "owner#" may contain letters`)
	assertErrorContains(t, err, `events[1] service "Imaginary Compute" is not supported`)
	assertErrorContains(t, err, `events[2] schedules at 2026-02-28T00:00:00Z before clock.start 2026-03-01`)
	assertErrorContains(t, err, `events[3].payer_account "Nobody" is not in organization_template "anycompany-retail"`)
	assertErrorContains(t, err, `events[3].billing_period_start must be before billing_period_end`)
	assertErrorContains(t, err, `checks[0].type "manually_review_console" is not supported`)
	assertErrorContains(t, err, `checks[1].expected_service "Imaginary Compute" is not supported`)
}

func TestParseDefinitionRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	_, err := ParseDefinitionBytes([]byte(`{"name":"bad","title":"unknown"}`))
	if err == nil {
		t.Fatal("ParseDefinitionBytes accepted an unknown field")
	}
	assertErrorContains(t, err, `unknown field "title"`)

	_, err = ParseDefinitionBytes([]byte(`{
		"name": "One",
		"clock": {"start": "2026-03-01"},
		"organization_template": "anycompany-retail",
		"checks": [{"type": "saved_report_exists", "report_name": "Spend"}]
	} {}`))
	if err == nil {
		t.Fatal("ParseDefinitionBytes accepted multiple documents")
	}
	assertErrorContains(t, err, "multiple documents are not supported")
}

func TestParseDefinitionRejectsNonJSONObject(t *testing.T) {
	_, err := ParseDefinition(strings.NewReader(`[{"name":"array"}]`))
	if err == nil {
		t.Fatal("ParseDefinition accepted a non-object document")
	}
	assertErrorContains(t, err, "must be a JSON object")
}

func assertErrorContains(t *testing.T, err error, fragment string) {
	t.Helper()
	if !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error %q does not contain %q", err.Error(), fragment)
	}
}
