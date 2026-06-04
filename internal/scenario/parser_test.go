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
