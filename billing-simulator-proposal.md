# Billing Simulator: Specification and Proposal

Research date: 2026-06-02

## Executive Summary

A billing simulator for AWS cost and billing concepts is practical and valuable if it is positioned as a training and experimentation environment, not as a perfect clone of AWS internal billing. The core gap is real: developers and architects are often expected to design account structures, tagging standards, consolidated billing arrangements, budgets, and chargeback models, but the real AWS billing console is gated by management-account permissions, real payment methods, actual spend, organizational policies, billing delays, and month-end invoice timing.

The proposed product is a standalone, single-user simulated AWS Billing and Cost Management environment. It should run as one local application and expose a local browser UI where learners can create organizations, OUs, and accounts; configure tags and cost categories; generate realistic usage; inspect Cost Explorer-style outputs; close a billing month; receive invoice-like bills; and practice payment and remediation workflows. It should use synthetic organizations and charges by default, with an optional import path from AWS Price List data for more realistic rates.

The best MVP is not "simulate every AWS service." The best MVP is "simulate the billing consequences of multi-account design choices." That means a curated service catalog, realistic billing line items, Cost Explorer filtering/grouping, CUR/FOCUS-style exports, invoices, payment states, budgets, and guided scenarios.

## Why This Matters

AWS Billing and Cost Management is a broad suite covering bill retrieval and payment, cost analysis, cost organization, budgeting, planning, savings, commitments, billing transfer, IAM access, Organizations, and the AWS Price List API. AWS documents the surface as a place to "set up your billing, retrieve and pay invoices, and analyze, organize, plan, and optimize your costs" and describes how larger organizations consolidate charges across multiple AWS accounts using AWS Organizations, allocate costs with tags and cost categories, and export data for downstream analysis.

The educational problem is that most of those workflows are hard to experience safely:

- A real organization needs a management account that pays member-account charges.
- Billing and Cost Management access is sensitive; IAM users and roles cannot access the console by default unless billing access is enabled.
- Cost allocation tags must be applied and activated, and tag visibility can lag.
- Cost Explorer and CUR data are not instant; data typically updates on daily cadences and is finalized after the billing period closes.
- Invoices and payment workflows require real billing identity, tax, currency, and payment-method setup.
- Errors are expensive: a learner can incur real charges while trying to understand billing behavior.

Existing AWS Cloud Financial Management training exists, but most hands-on billing labs are constrained. For example, third-party labs advertise conceptual Cost Explorer/Budgets coverage because service control policies often restrict real billing operations. AWS SimuLearn and AWS Skill Builder provide broad cloud simulations and CFM courses, but there is still room for a purpose-built, high-fidelity billing sandbox focused on consolidated billing, cost attribution, invoices, and payments.

## Research Basis

### AWS Billing and Organizations

AWS Organizations consolidated billing centralizes payment: every organization has a management account that pays charges for member accounts. AWS lists benefits including one bill, charge tracking across accounts, combined usage for volume discounts, Reserved Instance discounts, and Savings Plans sharing, with no additional fee.

Important nuance for the simulator: member account bills are informational. Discounts can be applied and reallocated at the management account level, which means learners must see both individual account charge detail and consolidated effects.

### Bills, Invoices, and Payments

AWS issues a PDF invoice at the end of a monthly billing period or for some one-time fees. The Bills page exposes monthly chargeable costs, service details, AWS Marketplace purchases, pending estimated charges for open periods, issued invoices for closed periods, charges by service, charges by account, invoices, savings, and taxes.

For payments, AWS charges the default payment method automatically at the start of the month. If payment fails, users can update the payment method and pay from the Payments page. Payment tables include due, past due, scheduled, and processing states, and payment history is visible from the console.

Public invoice examples show a recognizable invoice structure:

- Service provider and remittance note
- Account number
- Bill-to address
- Invoice number and invoice date
- Amount due and due date
- Billing period
- Summary of charges, credits, tax, and total
- Detail rows by AWS service

The simulator should generate synthetic invoices with this structure, but it should avoid copying AWS invoice design exactly or using real customer data.

### Cost Explorer

Cost Explorer provides visual cost and usage analysis, filtering, grouping, custom reports, forecasts, and API access. AWS says Cost Explorer updates cost data at least once every 24 hours. The API supports filtering by dimensions, tags, and cost categories, and grouping by up to two dimensions, tag keys, or cost categories.

This is ideal for simulation because a learning environment can expose the same mental model:

- Time range
- Granularity: daily, monthly, optionally hourly
- Metrics: unblended cost, blended cost, usage quantity, amortized cost, net cost
- Dimensions: service, linked account, region, usage type, record type, legal entity, invoice entity
- Tags: application, owner, environment, cost center, data classification
- Cost categories: team, business unit, product, shared services

### Cost Allocation Tags

AWS cost allocation tags require both tagging resources and activating tags in Billing and Cost Management. AWS distinguishes AWS-generated and user-defined tags. After activation, tags can be used to organize costs in reports and Cost Explorer. AWS notes that tags can take up to 24 hours to appear in the Billing and Cost Management console and that only the management account in an organization, or standalone accounts, can access cost allocation tag management.

Simulator implication: tag activation should be modeled separately from resource tagging. This is one of the best learning opportunities because learners commonly assume tags appear in billing automatically.

### Cost Categories and Split Charges

AWS Cost Categories map cost and usage information to business structures. They appear in Cost Explorer, AWS Budgets, CUR, and Cost Anomaly Detection. Rules can group costs by accounts, cost allocation tags, charge type, service, region, usage type, billing entity, or other cost categories. AWS also supports split charge rules for shared costs, with proportional, fixed, and even split allocation methods.

Simulator implication: cost categories are the bridge from "AWS-native billing" to "internal showback." The simulator should let users compare raw AWS cost, categorized cost, and allocated cost.

### CUR, CUR 2.0, Data Exports, and FOCUS

AWS Cost and Usage Reports contain granular cost and usage line items. AWS Data Exports now supports CUR 2.0 and FOCUS exports. CUR fields include `lineItem/UsageAccountId`, `lineItem/ProductCode`, `lineItem/UsageType`, `lineItem/UsageAmount`, `lineItem/UnblendedRate`, `lineItem/UnblendedCost`, `lineItem/BlendedRate`, `lineItem/BlendedCost`, `lineItem/LineItemType`, `lineItem/ResourceId`, timestamps, currency, and legal entity.

Data Exports let users select columns, filter rows, rename columns, store exports in S3, and use consistent schemas. FOCUS 1.2 with AWS columns is now part of AWS Data Exports, making it a good target for a simulator export format.

Simulator implication: the app should produce CSV/JSON/Parquet-like exports, even if actual Parquet generation is deferred. A learner should be able to query cost data like a FinOps practitioner would.

### Billing Conductor and Pro Forma Billing

AWS Billing Conductor creates a second version of billing data for showback and chargeback. Pro forma billing data differs from the standard AWS bill and does not change what is actually payable to AWS. Billing groups can apply pricing plans, pricing rules, and custom line items. AWS documents that pro forma and billable billing data exist in separate domains.

Simulator implication: advanced scenarios should include "standard billable cost" versus "internal pro forma cost" so learners can reason about margins, markups, shared costs, and internal allocation.

## Proposal

Build a web application called Billing Simulator. It should feel like an AWS-adjacent training console without pretending to be AWS. The application should provide a simulated organization, a billing engine, cost analytics, invoice generation, and payment-state workflows.

The simulator should answer questions like:

- What happens to the bill if I split workloads into one account per environment versus one account per product?
- What does the management account see that a member account cannot see?
- Why do tags not appear in Cost Explorer immediately?
- How do account tags, resource tags, and cost categories differ?
- How do shared NAT Gateway, data transfer, support, and platform costs get allocated?
- What is the difference between unblended, blended, net, amortized, and pro forma cost?
- What does a consolidated invoice contain?
- What happens when payment fails, a card expires, an invoice is past due, or credits are applied?
- How does a CUR-style export reconcile with the bill?
- What views should a finance user, platform engineer, product owner, and member-account owner each see?

## Product Principles

1. Teach concepts by making learners operate the system.
2. Preserve AWS mental models, but do not copy AWS UI or brand assets.
3. Make all billing consequences deterministic and inspectable.
4. Prefer realistic line items over realistic infrastructure.
5. Separate usage generation from billing calculation so instructors can create scenarios.
6. Make time controllable: learners can advance hours, days, and month-end close.
7. Include mistakes: missing tags, late tag activation, shared costs, discount surprises, payment failures, and stale Cost Explorer data.
8. Export data for analysis so the simulator is useful beyond the UI.
9. Keep the implementation local-first: no hosted service should be required for core use.
10. Avoid Node.js, npm, and frontend build systems unless a later feature clearly justifies them.

## Target Users

### Primary Learners

- Developers who deploy AWS resources but rarely see the consolidated bill.
- Solutions architects designing account structures and tagging policies.
- Platform engineers designing landing zones and shared-service models.
- FinOps analysts learning AWS-native reporting tools.
- Engineering managers and product owners learning cost accountability.

### Secondary Users

- Instructors building labs.
- Consultants teaching multi-account strategy.
- Internal cloud enablement teams onboarding engineering groups.
- Tool vendors demonstrating FinOps workflows without exposing real customer bills.

## Learning Outcomes

After completing the core simulator curriculum, a learner should be able to:

- Explain management account, member account, OU, and consolidated billing relationships.
- Design a basic multi-account structure for sandbox, staging, production, security, and shared services.
- Predict how account boundaries affect cost visibility, chargeback, and governance.
- Apply resource tags, account tags, and cost allocation tag activation correctly.
- Build Cost Explorer-style reports grouped by service, linked account, region, tag, and cost category.
- Interpret invoice summary, charges by service, charges by account, credits, taxes, and payment status.
- Reconcile bill totals to CUR-style line items within expected rounding and timing rules.
- Identify untagged spend and shared charges.
- Configure budgets and respond to actual and forecast alerts.
- Explain when Cost Categories or Billing Conductor-like pro forma views are appropriate.

## Scope

### In Scope for MVP

- Simulated AWS Organizations hierarchy: root, OUs, management account, member accounts.
- Account creation, suspension, joining, leaving, and billing access controls.
- Curated usage catalog for 8 to 12 billing-relevant services:
  - Amazon EC2 instance hours
  - Amazon EBS volume GB-months
  - Amazon S3 storage, requests, retrieval, and data transfer
  - AWS Lambda requests and GB-seconds
  - Amazon RDS instance hours and storage
  - NAT Gateway hourly and data processing charges
  - CloudWatch logs ingestion and storage
  - Data transfer out and inter-region data transfer
  - AWS Support as a percentage or tiered charge
  - AWS Marketplace subscription as a non-AWS service provider example
- Resource creation with tags.
- Cost allocation tag activation and delayed visibility.
- Daily usage generation and monthly billing close.
- Bills page:
  - Summary
  - Charges by service
  - Charges by account
  - Invoices
  - Savings and credits
  - Taxes
- Cost Explorer-style UI:
  - Date range
  - Granularity
  - Group by up to two dimensions
  - Filters
  - Saved reports
  - Forecast
- Budget alerts:
  - Actual threshold
  - Forecast threshold
  - Email/SNS simulation
- Invoice generation:
  - Synthetic PDF or HTML invoice
  - CSV detailed charges
- Payments workflow:
  - Payment methods
  - Payment profiles
  - Auto charge at month start
  - Payment failed, due, past due, processing, paid states
- CUR-like CSV export.
- Instructor-authored scenarios and grading checks.

### In Scope for Later Phases

- Reserved Instances and Savings Plans.
- Blended versus unblended rates.
- Amortized versus invoiced cost.
- Cost Categories with split charge rules.
- Billing Conductor-style pro forma billing groups.
- Billing transfer between simulated organizations.
- FOCUS export.
- Cost Anomaly Detection.
- Free Tier plans and credits.
- Purchase orders, tax registration, seller-of-record differences.
- AWS Pricing API import and price snapshot management.
- Natural language report prompts.
- Multi-tenant class management.

### Explicit Non-Goals

- Do not reproduce the exact AWS console UI.
- Do not process real payments.
- Do not claim invoice outputs are valid tax invoices.
- Do not provide authoritative pricing advice unless using current AWS Price List snapshots and clearly labeling them.
- Do not require real AWS credentials for the core learning experience.
- Do not simulate infrastructure behavior in detail; focus on billable usage and reporting.
- Do not require PostgreSQL, Node.js, npm, or a public web server for the standalone tool.

## Simulation Model

### Time

The simulator should use a controllable clock.

Time modes:

- Realtime: one simulated day per real day.
- Accelerated: one simulated day per minute.
- Manual: instructor or learner advances time.
- Month-end close: closes current month, finalizes line items, emits invoice, starts payment cycle.

Data freshness should be intentionally modeled:

- Resource usage appears immediately in resource inventory.
- Estimated charges appear in the Bills page after the next simulated metering run.
- Cost Explorer updates on a simulated daily cadence.
- CUR export updates cumulatively during the month and finalizes after invoice close.
- Activated tags appear in billing reports only after a delay.

### Organization

Core entities:

- Organization
- Organization root
- Organizational unit
- Account
- Management account
- Member account
- Billing transfer account, later phase
- Billing group, later phase
- Permission set or role

Account attributes:

- Account ID
- Account name
- Email
- OU path
- Account status: active, suspended, closed
- Created date
- Joined organization date
- Left organization date
- Payment responsibility: management account, standalone, transferred
- Tags
- Billing visibility policy

Recommended starter organization:

```text
AnyCompany Retail
  Root
    Security OU
      Log Archive
      Audit
    Infrastructure OU
      Shared Networking
      Platform Services
    Sandbox OU
      Developer Sandbox 1
      Developer Sandbox 2
    Workloads OU
      Storefront Dev
      Storefront Prod
      Payments Dev
      Payments Prod
      Analytics Prod
    Suspended OU
      Deprecated Prototype
```

This structure intentionally gives learners shared infrastructure, multiple products, multiple environments, and one suspended account.

### Usage

Usage is generated from resources and scenario events.

Resource examples:

- EC2 instance: account, region, instance type, OS, tenancy, launch time, stop time, tags.
- EBS volume: account, region, size GB, type, provisioned IOPS, attached instance, tags.
- S3 bucket: account, region, storage class, GB stored by day, request counts, retrieval GB, data transfer GB, tags.
- NAT Gateway: account, region, hours active, processed GB, tags.
- RDS database: account, region, instance class, engine, storage GB, multi-AZ flag, tags.
- Lambda function: account, region, requests, duration, memory MB, tags.

Usage events become billable line items:

```text
usage_event -> metering_record -> priced_line_item -> bill_line_item -> invoice_line
```

### Pricing

MVP should use a curated, versioned price catalog. Later phases can import current AWS Price List API data.

Price catalog fields:

- SKU
- Service code
- Product family
- Usage type
- Operation
- Region
- Unit
- Rate
- Currency
- Effective date
- Price source: synthetic, AWS Price List snapshot, instructor override
- Notes

Pricing formulas:

- EC2 instance hours: instance-hours * hourly rate
- EBS storage: GB-month prorated by daily GB * monthly rate / days in month
- S3 storage: GB-month by storage class
- S3 requests: request count / 1,000 or 1,000,000 * request rate
- Data transfer: GB * transfer tier rate
- NAT Gateway: gateway-hours * hourly rate + processed GB * processing rate
- Lambda: requests * request rate + GB-seconds * duration rate
- Support: percentage of eligible monthly charges with minimums
- Credits: negative line items applied according to credit rules
- Tax: configurable percentage by invoice entity and address

MVP should compute:

- Unblended rate
- Unblended cost
- Usage amount
- Currency
- Line item type: Usage, Credit, Tax, Fee, Refund
- Usage account
- Payer account

Later phases add:

- Blended rates
- Net unblended cost
- Amortized cost
- RI and Savings Plans fees, covered usage, negations, and recurring charges
- Volume tiering
- Free Tier credits

### Tags

Resource tags exist immediately on resources, but billing use requires activation.

Tag states:

- Not present
- Present on resource
- Discovered by billing
- Activated for cost allocation
- Visible in Cost Explorer
- Visible in CUR export

Recommended mandatory tags:

- `app`
- `env`
- `owner`
- `cost-center`
- `data-classification`

The simulator should include common tag problems:

- Different key casing: `Owner` versus `owner`
- Missing tags
- Tags applied after usage began
- Tag activated mid-month
- Untaggable charges such as support or some data-transfer charges
- Shared resource with one tag but multiple beneficiaries

### Cost Categories

Cost Categories map raw line items to internal business dimensions.

Rule examples:

- Accounts `Storefront Dev` and `Storefront Prod` -> Product = Storefront
- Tag `app=payments` -> Product = Payments
- Services `AWS Support`, `AWS Data Transfer`, `NAT Gateway` in shared account -> Shared Platform
- Charge type `Credit` -> Credits

Split charge examples:

- Shared Platform split proportionally across products by raw spend.
- Security accounts split evenly across all production accounts.
- Enterprise Support split fixed: 50 percent production, 30 percent analytics, 20 percent sandbox.

The UI should show:

- Raw cost
- Categorized cost
- Split amount
- Total allocated cost
- Unallocated cost

### Bills and Invoices

Bill states:

- Open: estimated current-month charges.
- Pending close: month ended, final metering running.
- Issued: invoice generated.
- Adjusted: credits/refunds/taxes changed after issue.
- Paid: payment complete.
- Past due: payment not complete by due date.

Invoice fields:

- Invoice ID
- Invoice date
- Billing period
- Due date
- Seller of record
- Payer account
- Bill-to profile
- Currency
- Summary: charges, credits, refunds, tax, total
- Detail by service
- Detail by account for consolidated bills
- Payment status
- Download links: PDF, CSV, CUR export

The simulator should support multiple invoice documents per month when service provider or tax profile differs, because AWS Billing documentation refers to service providers/seller-of-record differences and payment profiles.

### Payments

Payment method types:

- Card
- ACH/direct debit
- Invoice remittance
- Advance Pay balance
- Credit memo/unapplied funds

Payment states:

- Scheduled
- Processing
- Succeeded
- Failed
- Due
- Past due
- Partially paid
- Refunded

Failure scenarios:

- Expired card
- Card authorization failed
- ACH processing delay
- Insufficient remittance information
- Unapplied funds not matched to invoice
- Payment profile missing for seller of record

Payments should never touch real processors. The simulator should model state transitions only.

## User Experience Specification

### 1. Scenario Dashboard

Purpose: entry point for learners and instructors.

Features:

- Current simulated date and billing period.
- Organization spend summary.
- Month-to-date estimate.
- Forecasted month-end spend.
- Top services.
- Top accounts.
- Budget alerts.
- Unallocated cost percentage.
- Outstanding invoices.
- Active scenario objectives.
- Advance-time controls.

### 2. Organization Designer

Purpose: experiment with account and OU structures.

Features:

- Tree view of root, OUs, and accounts.
- Account detail panel.
- Create, move, suspend, and close accounts.
- Configure management account.
- Account tags.
- Billing access policy for each account.
- Scenario warnings:
  - "This account has no owner tag."
  - "This shared account has production and sandbox usage mixed."
  - "Member account cannot pay its own consolidated bill."

Learning checks:

- Place accounts into appropriate OUs.
- Separate production from sandbox.
- Identify who pays the bill.
- Predict which user can see which billing data.

### 3. Resource and Usage Lab

Purpose: incur costs safely.

Features:

- Create synthetic resources.
- Choose account, region, service, size, and tags.
- Start/stop resources.
- Generate traffic, storage growth, requests, and data transfer.
- Inject usage spikes.
- Display estimated hourly/daily cost.
- Show billable dimensions that will appear in line items.

Scenario examples:

- Launch EC2 in dev without owner tag.
- Add a large S3 bucket in analytics.
- Route all workload traffic through shared NAT Gateway.
- Create an expensive data transfer path.
- Leave an RDS instance running over the weekend.

### 4. Tag Manager

Purpose: teach the difference between resource tags and cost allocation tags.

Features:

- Tag inventory by key and value.
- Coverage report by spend, resource count, and account.
- Activate/deactivate cost allocation tags.
- Simulated 24-hour activation delay.
- Case-sensitivity warnings.
- Backfill experiment: show historical limitations and changed allocation over time.

Key view:

```text
Tag key          Resource coverage   Spend coverage   Billing status
app              91%                 78%              Active
owner            84%                 64%              Active
cost-center      71%                 53%              Pending activation
Owner            12%                 18%              Not active
```

### 5. Cost Explorer

Purpose: query and visualize costs.

Features:

- Date range selector.
- Granularity: daily, monthly, optional hourly.
- Metrics:
  - Unblended cost
  - Blended cost, later phase
  - Amortized cost, later phase
  - Usage quantity
  - Net cost, later phase
- Filters:
  - Service
  - Linked account
  - Region
  - Usage type
  - Charge type/record type
  - Tag
  - Cost category
- Group by one or two dimensions.
- Chart and table views.
- Save report.
- Download CSV.
- Forecast.
- Simulated natural language prompts, later phase.

Required saved reports:

- Monthly cost by service.
- Monthly cost by linked account.
- Daily cost by service.
- Untagged spend by service.
- Cost by product and environment.
- Shared services allocation.
- Marketplace charges.

### 6. Bills

Purpose: teach invoice and monthly bill mechanics.

Tabs:

- Summary
- Charges by service
- Charges by account
- Invoices
- Savings and credits
- Taxes
- Exports

Key behaviors:

- Current month is estimated and pending.
- Closed months have issued invoices.
- Management account sees consolidated charges.
- Member account sees its own informational bill according to visibility policy.
- Charges can drill down from service -> region -> usage type -> account/resource.
- Invoice total reconciles to final bill line items.

### 7. Payments

Purpose: teach the financial lifecycle after invoice issuance.

Features:

- Payment account summary.
- Outstanding balance.
- Unapplied funds.
- Payments due.
- Payment profiles by seller of record.
- Payment method manager.
- Complete payment workflow.
- Transaction history.
- Failure resolution flow.

Scenarios:

- Default card fails after invoice close.
- ACH payment takes several simulated days.
- Payment profile missing for AWS Marketplace seller.
- Credit memo exists but is unapplied.
- Invoice becomes past due.

### 8. Cost Categories

Purpose: map AWS charges to business structures.

Features:

- Category builder.
- Rule order.
- Preview matched line items.
- Unallocated bucket.
- Split charge configuration.
- Before/after allocation table.
- Export category results.

Scenario:

Learner must allocate shared platform costs across Storefront, Payments, and Analytics. They compare even split, fixed split, and proportional split, then justify the result.

### 9. Exports and Query Lab

Purpose: bridge UI exploration to data analysis.

Exports:

- CUR-like CSV.
- CUR 2.0-like JSON/CSV.
- FOCUS-like CSV, later phase.
- Cost Explorer API response JSON.
- Invoice CSV.

Query lab:

- Built-in SQL-lite query environment.
- Example queries:
  - Total by linked account.
  - Untagged cost by service.
  - Top 10 usage types.
  - Reconcile invoice total to line items.
  - Compare raw and allocated product cost.

### 10. Instructor Console

Purpose: create repeatable exercises.

Features:

- Scenario editor.
- Seed organization templates.
- Usage event scripts.
- Expected outcome checks.
- Learner progress.
- Reset scenario.
- Export learner report.

Scenario DSL example:

```yaml
name: "Find the untagged data-transfer spike"
clock:
  start: "2026-03-01"
organization_template: "anycompany-retail"
events:
  - day: 3
    action: create_resource
    account: "Storefront Prod"
    service: "Amazon S3"
    resource: "s3://storefront-assets"
    tags:
      app: "storefront"
      env: "prod"
      owner: "web-platform"
  - day: 12
    action: add_usage
    service: "AWS Data Transfer"
    account: "Shared Networking"
    amount_gb: 4000
    tags: {}
checks:
  - type: saved_report_exists
    report_name: "Untagged spend by service"
  - type: identifies_top_driver
    expected_service: "AWS Data Transfer"
  - type: cost_category_rule_created
    category: "Product"
```

## Data Model

### Main Tables

```text
organizations
organization_units
accounts
users
roles
billing_visibility_policies

resources
resource_tags
account_tags
cost_allocation_tag_keys
tag_activation_events

usage_events
metering_records
price_catalog_items
pricing_rules
bill_line_items

cost_categories
cost_category_rules
cost_category_assignments
split_charge_rules
allocated_cost_line_items

budgets
budget_thresholds
budget_notifications
anomalies

billing_periods
bills
invoices
invoice_documents
payment_methods
payment_profiles
payments
credits
tax_profiles

exports
saved_reports
scenario_templates
scenario_runs
scenario_checks
```

### Bill Line Item Fields

MVP should include these fields because they map well to CUR and Cost Explorer:

```text
id
billing_period_start
billing_period_end
payer_account_id
usage_account_id
account_name
service_code
service_name
product_code
region
availability_zone
usage_type
operation
line_item_type
resource_id
usage_start_time
usage_end_time
usage_amount
usage_unit
unblended_rate
unblended_cost
currency
legal_entity
invoice_entity
tags_json
cost_categories_json
description
```

Later phase fields:

```text
blended_rate
blended_cost
net_unblended_rate
net_unblended_cost
amortized_cost
reservation_arn
savings_plan_arn
pricing_term
discount_id
tax_type
```

## Local HTTP and API Specification

The simulator does not need to implement AWS APIs exactly, and it does not need a public API. It should expose local HTTP handlers for the server-rendered UI, downloads, tests, and optional automation. API-shaped endpoints should resemble AWS billing concepts where that helps learning and implementation clarity.

### Organization

```http
GET /api/orgs/{orgId}/tree
POST /api/orgs/{orgId}/accounts
PATCH /api/accounts/{accountId}
POST /api/accounts/{accountId}/move
POST /api/accounts/{accountId}/suspend
```

### Usage

```http
POST /api/resources
PATCH /api/resources/{resourceId}
POST /api/resources/{resourceId}/usage
POST /api/scenarios/{scenarioId}/events
```

### Billing Engine

```http
POST /api/clock/advance
POST /api/billing/run-metering
POST /api/billing/close-period
GET /api/bills?period=2026-03
GET /api/bills/{billId}
GET /api/invoices/{invoiceId}
```

### Cost Explorer-Compatible Subset

```http
POST /api/cost-explorer/get-cost-and-usage
POST /api/cost-explorer/get-tags
POST /api/cost-explorer/get-dimension-values
GET /api/cost-explorer/saved-reports
POST /api/cost-explorer/saved-reports
```

Example request:

```json
{
  "timePeriod": {
    "start": "2026-03-01",
    "end": "2026-04-01"
  },
  "granularity": "DAILY",
  "metrics": ["UnblendedCost"],
  "groupBy": [
    { "type": "DIMENSION", "key": "SERVICE" },
    { "type": "TAG", "key": "app" }
  ],
  "filter": {
    "dimensions": {
      "key": "LINKED_ACCOUNT",
      "values": ["210987654321"]
    }
  }
}
```

### Tags and Cost Categories

```http
GET /api/tags
POST /api/cost-allocation-tags/{tagKey}/activate
POST /api/cost-allocation-tags/{tagKey}/deactivate
GET /api/cost-categories
POST /api/cost-categories
PATCH /api/cost-categories/{categoryId}
POST /api/cost-categories/{categoryId}/preview
```

### Payments

```http
GET /api/payments/summary
GET /api/payments/due
POST /api/payments/{invoiceId}/complete
POST /api/payment-methods
POST /api/payment-profiles
POST /api/payments/{paymentId}/simulate-failure
```

### Exports

```http
POST /api/exports
GET /api/exports/{exportId}
GET /api/exports/{exportId}/download
```

## Pricing Accuracy Strategy

Pricing is the largest realism risk. AWS service pricing is broad, changes over time, and includes service-specific details that are not appropriate for a small MVP. The simulator should use three fidelity levels.

### Level 1: Synthetic Prices

Use stable fictional rates that are close enough to teach relationships. This is best for early MVP and classroom consistency.

Benefits:

- Deterministic tests.
- No dependency on AWS price changes.
- Easy to explain.
- Avoids accidentally presenting stale prices as real.

Tradeoff:

- Learners cannot use the numbers as real estimates.

### Level 2: AWS Price List Snapshot

Import selected service prices from AWS Price List Bulk API into a versioned catalog. AWS documents service index and service-specific price list URLs for JSON/CSV offer files.

Benefits:

- More realistic rates.
- Useful for advanced architects.

Tradeoffs:

- Requires refresh pipeline.
- Requires service-specific mapping from product attributes to simplified resources.
- Still misses private discounts, taxes, support, bundled discounts, RI/SP effects, and local seller-of-record details.

### Level 3: Instructor/Organization Overrides

Allow instructors to set contract-like discounts, custom rates, credits, and support plans.

Benefits:

- Enables realistic enterprise simulations.
- Supports Billing Conductor-style showback and chargeback.

Tradeoff:

- More configuration complexity.

Recommendation: ship Level 1 first, build Level 2 as an importer, and use Level 3 for enterprise/training customization.

## Example Scenario Set

### Scenario 1: First Consolidated Bill

Learner creates a management account, adds three member accounts, generates usage, closes the month, and explains why the management account receives the invoice.

Concepts:

- Management account
- Member accounts
- Consolidated bill
- Charges by account
- Invoice issuance

### Scenario 2: Missing Tags

Learner launches resources with inconsistent tags. They activate `owner` and `app` tags, wait for the simulated delay, and build a Cost Explorer report showing untagged spend.

Concepts:

- Resource tags versus cost allocation tags
- Activation delay
- Untagged spend
- Case sensitivity

### Scenario 3: Shared Networking

Shared NAT Gateway and data transfer charges appear in the infrastructure account. Learner creates a Cost Category and split charge rule to allocate cost across products.

Concepts:

- Shared costs
- Cost Categories
- Split charge methods
- Raw versus allocated cost

### Scenario 4: Payment Failure

After month-end invoice issuance, the default card fails. Learner updates payment method, retries payment, and verifies payment history.

Concepts:

- Invoice due
- Payment method
- Failed payment
- Past due state
- Transaction history

### Scenario 5: Forecast and Budget Alert

An S3 data transfer spike pushes forecast above budget. Learner identifies the cost driver and changes the workload or budget ownership.

Concepts:

- Forecast threshold
- Budget alert
- Cost Explorer drilldown
- Owner accountability

### Scenario 6: Showback Versus AWS Bill

Learner configures internal pro forma rates for two billing groups and compares internal showback with standard billable AWS cost.

Concepts:

- Pro forma billing
- Billing groups
- Custom line items
- Margin
- Non-reconciliation with actual AWS bill

## Assessment and Feedback

The simulator should score operational behavior, not memorization.

Assessment examples:

- Correctly identifies payer account.
- Creates required accounts and OUs.
- Activates cost allocation tags before report generation.
- Produces a saved report with expected filters and grouping.
- Reduces unallocated spend below target threshold.
- Allocates shared costs according to scenario policy.
- Reconciles invoice total to line items.
- Resolves payment failure before due date.
- Explains why a member account cannot see all organization cost data.

Feedback should show:

- What the learner did.
- What billing behavior changed.
- What data source proves the answer.
- What would happen in real AWS.

## Technical Architecture

### Recommended Stack

For a standalone single-user tool, the preferred stack is Go, SQLite, and a local server-rendered web UI.

Recommended implementation:

- Runtime: one Go binary.
- UI: local browser UI served from `127.0.0.1`.
- Rendering: Go `html/template` initially; `templ` is a good later option if component ergonomics become important.
- Styling: static CSS embedded with `go:embed`.
- Interactivity: plain HTML forms and small vanilla JavaScript; optionally vendor htmx as a single static file for partial-page updates.
- Backend: Go `net/http` plus a small router if useful.
- Database: SQLite with migrations and write-ahead logging.
- Query layer: explicit SQLite summary tables for Cost Explorer-style reports.
- Analytics extension: DuckDB later for CUR/FOCUS-style query lab workflows.
- Jobs: in-process job loop for metering, billing close, export generation, and forecasts.
- PDF generation: server-side HTML-to-PDF, or HTML invoice first with PDF export later.
- CSV/JSON exports: local HTTP download endpoints.
- Scenario engine: YAML/JSON scenario definitions with deterministic random seeds.
- Packaging: single binary plus optional workspace database files.

This stack deliberately avoids Node.js, npm, React, Electron, PostgreSQL, and a public web service. The simulator benefits from web UI ergonomics for tables, forms, charts, and drilldowns, but it does not need a JavaScript application build chain to get those benefits.

### Local Application Model

The app should start a local HTTP server bound to `127.0.0.1`, open the default browser, and store all data in a local workspace directory. Each scenario run can have its own SQLite database file.

Recommended local paths:

```text
workspace/
  simulator.db
  exports/
    cur/
    invoices/
    reports/
  scenarios/
  price-catalogs/
```

Recommended startup behavior:

- Create or open a workspace.
- Run database migrations.
- Enable SQLite WAL mode and a busy timeout.
- Start the local HTTP server.
- Open the browser to the scenario dashboard.
- Save the selected workspace path for the next launch.

The one-database-per-workspace model makes reset, backup, scenario cloning, grading, and reproducibility straightforward. A learner or instructor can archive an entire run by copying the workspace directory.

### UI Architecture

The UI should be server-rendered HTML with progressively enhanced interactions.

Recommended patterns:

- Full-page navigation for major areas: Dashboard, Organization, Resources, Tags, Cost Explorer, Bills, Payments, Exports, Scenarios.
- HTML forms for create/update actions.
- Server-rendered tables for bills, line items, tags, accounts, invoices, and payments.
- Query parameters for report state so Cost Explorer views are bookmarkable.
- Partial HTML fragments for table filters, report previews, chart refreshes, and scenario checks.
- Inline SVG charts generated server-side for MVP, with a small vendored chart library considered only if SVG generation becomes limiting.
- Static CSS with a restrained console-like visual style.

Avoiding npm means the UI should not depend on bundlers, package registries, or generated frontend assets. Any third-party browser library should be vendored directly, versioned in the repository, and easy to remove.

### SQLite Strategy

SQLite is the right default for a standalone single-user simulator.

Implementation guidelines:

- Use WAL mode for better read/write concurrency.
- Set `busy_timeout` to avoid immediate lock failures during short writes.
- Keep write transactions short.
- Run metering and month-end close through one serialized job loop.
- Treat bill line items as append-only for finalized periods.
- Store normalized dimensions for billing reports rather than relying only on JSON.
- Use JSON fields for flexible tags and cost-category snapshots where useful.
- Use explicit summary tables rather than PostgreSQL-style materialized views.

Recommended summary tables:

```text
daily_cost_summary
monthly_account_service_summary
tag_coverage_summary
cost_category_assignment_summary
invoice_reconciliation_summary
budget_forecast_summary
```

SQLite is sufficient because this is not a high-concurrency hosted system. The simulator's expensive operations are deterministic batch calculations over local data, and those can be scheduled and tested directly.

### Billing Engine Design

Use event sourcing for usage and deterministic calculation.

Pipeline:

```text
scenario/resource events
  -> usage_events
  -> metering_records
  -> price_catalog lookup
  -> bill_line_items
  -> cost category assignments
  -> allocated cost lines
  -> bill summaries
  -> invoice documents
  -> payment obligations
  -> exports
```

Important property: every displayed number should trace back to line items. This allows learners to debug billing logic and gives tests a stable target.

### Query Layer

Cost Explorer queries should aggregate from `bill_line_items` plus optional allocation views.

Suggested SQLite summary tables:

- `daily_cost_summary`
- `monthly_account_service_summary`
- `tag_coverage_summary`
- `cost_category_assignment_summary`
- `invoice_reconciliation_summary`
- `budget_forecast_summary`

The raw line-item tables should remain the source of truth. Summary tables are derived data and can be refreshed after metering, tag activation, cost-category changes, and month-end close.

### Forecasting

MVP forecast can be simple:

- Current month-to-date spend / elapsed days * days in month
- Scenario-aware forecast for scheduled usage events
- Optional confidence band from previous months

Later phase:

- Exponential smoothing
- Seasonal weekly patterns
- Anomaly-aware forecast exclusions

### Testing

The billing engine needs strong tests because small math errors undermine trust.

Test areas:

- Proration by day/hour.
- Month boundary behavior.
- Tag activation delay.
- Cost category rule order.
- Split charge allocation and rounding.
- Invoice total reconciliation.
- Payment state transitions.
- Export schema consistency.
- Permission-based data visibility.

## MVP Build Plan

### Phase 0: Design Prototype - 1 to 2 weeks

Deliverables:

- Static HTML/CSS prototype for dashboard, organization, resources, Cost Explorer, Bills, Payments.
- Synthetic price catalog for 8 services.
- Seed organization and 3 scenarios.
- Invoice HTML mock.
- SQLite schema and migration plan finalized.
- Local workspace layout finalized.

Exit criteria:

- A reviewer can understand the complete learning loop from account design to payment.
- Prototype screens can be served locally without Node.js or npm.

### Phase 1: Core Simulator - 4 to 6 weeks

Deliverables:

- Single Go binary that starts a local `127.0.0.1` server.
- SQLite workspace database with migrations.
- Organization designer.
- Resource and usage generator.
- Billing engine for curated services.
- Bills page.
- Cost Explorer-style query UI.
- Tag manager with activation delay.
- Month-end close.
- Synthetic invoice generation.
- CSV export.
- Explicit summary tables for common Cost Explorer reports.

Exit criteria:

- Learner can complete Scenario 1 and Scenario 2 end to end.
- Bill totals reconcile to line items.
- Cost Explorer saved reports work for service, account, region, and tag groupings.
- App runs locally without PostgreSQL, Node.js, npm, or external services.

### Phase 2: FinOps Workflows - 4 to 6 weeks

Deliverables:

- Budgets and alerts.
- Cost Categories.
- Split charges.
- Payment workflows.
- Instructor scenario authoring.
- Grading checks.
- More seeded scenarios.

Exit criteria:

- Learner can complete shared-cost allocation and payment-failure labs.
- Instructor can create a new scenario without code changes.
- Scenario run can be reset, cloned, exported, and archived by copying local workspace data.

### Phase 3: Advanced Billing - 8 to 12 weeks

Deliverables:

- AWS Price List snapshot importer.
- CUR 2.0 and FOCUS-like exports.
- Optional DuckDB-backed query lab for exported billing data.
- Reserved Instances and Savings Plans basics.
- Blended/unblended/net/amortized cost.
- Billing Conductor-style pro forma groups.
- Billing transfer simulation.
- Cost anomaly detection.

Exit criteria:

- Advanced learners can compare actual, allocated, and pro forma cost views.
- Export data supports realistic FinOps analysis exercises.

## Practicality Assessment

### What Is Highly Practical

- Simulating organizations, accounts, tags, usage, line items, bills, invoices, and payments.
- Building Cost Explorer-style grouping/filtering for a subset of dimensions.
- Generating realistic invoices and reports.
- Teaching tagging delays and cost allocation mistakes.
- Producing CUR-like exports.
- Creating repeatable classroom scenarios.
- Packaging the MVP as a standalone local Go/SQLite application.
- Resetting, cloning, and archiving scenario runs as local workspace data.

### What Is Moderately Practical

- Importing AWS Price List data for selected services.
- Modeling blended rates and discounts.
- Modeling Savings Plans and Reserved Instances.
- Modeling taxes and seller-of-record differences.
- Modeling AWS Marketplace charges.
- Adding hosted multi-user classrooms or shared instructor dashboards.

These are feasible, but they require careful scoping and clear labels.

### What Is Impractical for MVP

- Exact reproduction of AWS billing across all services.
- Full tax compliance by country.
- Exact private pricing, enterprise discounts, or contract terms.
- Exact AWS invoice rendering.
- Real payment processing.
- Full AWS Billing Conductor parity.

### Overall Verdict

This is a strong product idea if the simulator is designed as a learning and reasoning tool. It should not compete with Cost Explorer, CUR, or commercial FinOps tools. Its unique value is that it lets people safely experience the billing lifecycle before they are responsible for a real one.

The highest-value first release is a deterministic multi-account billing sandbox with tags, Cost Explorer reports, bills, invoices, and payments. Advanced pricing fidelity can come later.

## Design and Visual Reference Notes

Use these as references for layout and field coverage, not as assets to copy directly.

- AWS Billing Console blog screenshots show Billing Dashboard, Free Tier Report, and Bills Page examples.
- AWS cost allocation tag documentation includes a partial cost allocation report screenshot with tag keys as columns.
- AWS Cost Categories blog screenshots show split charge creation and allocated cost output.
- Public AWS invoice PDF examples show invoice structure, service provider, account number, bill-to fields, invoice number/date, amount due, billing period, summary, and detail-by-service rows.
- AWS Billing and Cost Management documentation lists Bills page sections and payment/invoice concepts that should inform the simulator's UI.

Recommended visual style:

- Console-like, dense, and work-focused.
- Clear tables with drilldowns.
- Minimal decoration.
- Visible data lineage: totals should link to line items.
- Avoid AWS trademarks in product name, navigation labels, icons, or copied screenshots unless permission is obtained.

## Source Links

- AWS Billing and Cost Management overview: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/billing-what-is.html
- Consolidated billing for AWS Organizations: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/consolidated-billing.html
- AWS Bills and invoices: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/getting-viewing-bill.html
- Shorter PDF invoice options and invoice sections: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/consolidated-invoice-summary-options.html
- Making payments: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/manage-making-a-payment.html
- Payment summary and payment history: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/view-payment-info.html
- Payment profiles: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/manage-paymentprofiles.html
- AWS Cost Explorer product page: https://aws.amazon.com/aws-cost-management/aws-cost-explorer/
- Cost Explorer API `get_cost_and_usage`: https://docs.aws.amazon.com/boto3/latest/reference/services/ce/client/get_cost_and_usage.html
- Cost allocation tags: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/cost-alloc-tags.html
- AWS Cost Categories: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/manage-cost-categories.html
- Split charges in Cost Categories: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/splitcharge-cost-categories.html
- AWS Budgets best practices: https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-best-practices.html
- AWS CUR overview: https://docs.aws.amazon.com/cur/latest/userguide/what-is-cur.html
- AWS CUR line item columns: https://docs.aws.amazon.com/cur/latest/userguide/Lineitem-columns.html
- AWS Data Exports: https://docs.aws.amazon.com/cur/latest/userguide/what-is-data-exports.html
- AWS Price List API overview: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/price-changes.html
- AWS Price List Bulk API file access: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-the-aws-price-list-bulk-api-fetching-price-list-files-manually.html
- AWS Control Tower multi-account strategy: https://docs.aws.amazon.com/controltower/latest/userguide/aws-multi-account-landing-zone.html
- AWS Well-Architected Cost Optimization, Cloud Financial Management: https://docs.aws.amazon.com/wellarchitected/latest/cost-optimization-pillar/practice-cloud-financial-management.html
- AWS Billing Conductor and pro forma billing: https://docs.aws.amazon.com/billingconductor/latest/userguide/understanding-proforma.html
- AWS Billing Conductor overview: https://docs.aws.amazon.com/billingconductor/latest/userguide/what-is-billingconductor.html
- AWS Billing Console getting started blog with screenshots: https://aws.amazon.com/blogs/aws-cloud-financial-management/back-to-basics-getting-started-with-the-billing-console/
- AWS Cost Categories fundamentals blog with screenshots: https://aws.amazon.com/blogs/aws-cloud-financial-management/improve-cost-visibility-and-observability-with-aws-cost-categories-part-1-fundamentals-and-basic-grouping-techniques/
- AWS Cloud Financial Management digital training courses: https://aws.amazon.com/blogs/aws-cloud-financial-management/new-cloud-financial-management-digital-training-courses/
- AWS SimuLearn: https://aws.amazon.com/training/digital/aws-simulearn/
- Example third-party billing lab with conceptual restrictions: https://www.certipass.io/en/labs/e5534452-bff8-4250-8d5a-7dd7369e2d64
- Public sample AWS invoice PDF: https://www.noction.com/wp-content/uploads/2017/09/AWS_invoice.pdf
- FOCUS specification home: https://focus.finops.org/
- Go `net/http` package: https://pkg.go.dev/net/http
- Go `html/template` package: https://pkg.go.dev/html/template
- Go `embed` package: https://pkg.go.dev/embed
- SQLite write-ahead logging: https://www.sqlite.org/wal.html
- SQLite JSON functions: https://www.sqlite.org/json1.html
- SQLite generated columns: https://www.sqlite.org/gencol.html
- templ, Go-native HTML templates: https://templ.guide/
- htmx documentation: https://htmx.org/docs/
- DuckDB documentation: https://duckdb.org/docs/
