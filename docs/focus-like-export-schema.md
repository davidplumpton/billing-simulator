# FOCUS-Like Export Schema

The simulator can generate a local FOCUS-like CSV export from durable bill line items. The export uses FOCUS-style PascalCase column names for common FinOps dimensions and metrics, plus simulator-specific `x_Simulator...` columns for provenance that does not belong in a standard provider-neutral column.

This is a teaching export, not a FOCUS conformance claim. The public FOCUS specification is versioned and currently lists v1.4 as the latest version on the FOCUS site: https://focus.finops.org/focus-specification/v1-4/

## Access Paths

- Direct download: `/exports/focus.csv`
- Stored generation: `POST /exports/generate-focus`
- Stored file type: `focus_csv`

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
- It does not include FOCUS metadata documents or validator output.
