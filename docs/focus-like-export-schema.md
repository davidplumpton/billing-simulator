# FOCUS-Like Export Schema

The simulator can generate a local FOCUS-like CSV export from durable bill line items. The export uses FOCUS-style PascalCase column names for common FinOps dimensions and metrics, plus simulator-specific `x_Simulator...` columns for provenance that does not belong in a standard provider-neutral column.

This is a teaching export, not a FOCUS conformance claim. The selected target specification is FOCUS v1.4: https://focus.finops.org/focus-specification/v1-4/

## Conformance Boundary

The simulator labels the CSV as `FOCUS-like` and stores `target_focus_spec_version=1.4` in generated export metadata. The export maps synthetic line items into FOCUS-style Cost and Usage columns where the simulator has matching concepts, and marks simulator-only provenance fields with the `x_Simulator...` extension prefix.

The simulator does not claim strict FOCUS v1.4 conformance. Metadata sidecars use `conformance_claim=not_conformant` because this phase does not export every FOCUS v1.4 dataset, metadata document, conditional column, or advanced billing concept needed for formal conformance.

## Access Paths

- Direct download: `/exports/focus.csv`
- Stored generation: `POST /exports/generate-focus`
- Stored file type: `focus_csv`
- Stored metadata sidecar type: `focus_metadata_json`

Both paths accept the same filters as the CUR-like CSV export:

- `billing_period_start`
- `billing_period_end`
- `payer_account_id`
- `usage_account_id`
- `line_item_status`
- `limit`
- `viewer_role`
- `viewer_account_id`

Member viewers receive only their visible usage-account rows. Payer bill IDs, invoice IDs, and payer-scoped support rows are hidden from member-scoped FOCUS-like exports.

## Metadata Sidecar

Stored FOCUS CSV generation writes a JSON sidecar next to the CSV in the workspace export inventory. The sidecar filename is derived from the CSV filename with `-metadata.json`, and it is protected by the same export visibility rules as the CSV.

The sidecar includes:

- `schema`, `schema_version`, `target_focus_spec_version`, and `target_focus_spec_url`
- `dataset=Cost and Usage`
- source export filename, generated time, source bill ID when visible, and row count
- visibility scope and whether document identifiers were hidden
- an explicit `not_conformant` claim with the reason
- validator-oriented context for external FOCUS Validator experiments
- column classifications for FOCUS-mapped fields and simulator extension fields
- unsupported FOCUS v1.4 requirements that remain outside the current simulator phase

## Column Mapping

| Export column | Simulator source |
| --- | --- |
| `x_SimulatorExportGeneratedAt` | Simulator clock at generation time |
| `x_SimulatorSourceBillId` | Source bill ID, hidden for member-scoped exports |
| `x_SimulatorLineItemId` | `bill_line_items.id` |
| `x_SimulatorSchema` | Local schema marker |
| `BillingAccountId` | `bill_line_items.payer_account_id` |
| `BillingAccountName` | Payer account name |
| `BillingAccountType` | Payer/management account classification |
| `BillingCurrency` | `bill_line_items.currency_code` |
| `BillingPeriodStart`, `BillingPeriodEnd` | Bill line-item period |
| `ChargeCategory` | Simulator line item type |
| `ChargeDescription` | Bill line-item description |
| `ChargePeriodStart`, `ChargePeriodEnd` | Usage window |
| `ConsumedQuantity`, `ConsumedUnit` | Raw usage quantity and unit |
| `EffectiveCost` | Current unblended cost |
| `InvoiceId` | Invoice document ID, hidden for member-scoped exports |
| `InvoiceIssuer` | Invoice seller snapshot |
| `ListCost`, `ListUnitPrice` | Current synthetic list/unblended values |
| `PricingCategory` | Standard/fee classification |
| `PricingQuantity`, `PricingUnit` | Priced quantity and unit |
| `Provider`, `Publisher` | Synthetic seller/provider values |
| `RegionId` | Region code |
| `ResourceId`, `ResourceName`, `ResourceType` | Resource lineage when available |
| `ServiceCategory`, `ServiceName` | Derived category and service name |
| `SkuId`, `SkuMeter` | Synthetic price SKU and usage type |
| `SubAccountId`, `SubAccountName`, `SubAccountType` | Usage account context |
| `Tags` | Usage-window tag snapshot JSON |
| `x_SimulatorCostCategories` | Persisted Cost Category assignment JSON |
| `x_SimulatorProductFamily` | Synthetic price product family |
| `x_SimulatorUsageType`, `x_SimulatorOperation` | Simulator usage identifiers |

## Differences From AWS Data Exports

- The export is generated from synthetic simulator data and local SQLite state only.
- It does not include AWS account credentials, real pricing freshness, real invoices, taxes, credits, Savings Plans, Reserved Instances, or amortized cost data.
- It maps current unblended synthetic costs into `EffectiveCost`, `ListCost`, and `ListUnitPrice` because the simulator has no discount instruments in this phase.
- It uses simulator-specific custom columns for bill IDs, Cost Categories, schema marker, and source line-item IDs.
- It includes a simulator metadata sidecar for validation context, but it does not produce formal FOCUS validator pass/fail output.
