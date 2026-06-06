# AWS Billing Simulator

AWS Billing Simulator is a local-first training environment for learning AWS billing, cost allocation, consolidated billing, invoice, and FinOps workflows without using real AWS accounts, credentials, payment methods, or spend.

The project is being created because the most important billing lessons are difficult to practice safely in a real AWS organization. Real billing consoles require sensitive management-account access, real invoices, delayed reporting data, cost allocation tag activation, payment configuration, and live charges. This simulator makes those concepts deterministic, inspectable, and repeatable in a local workspace.

The product direction is described in [aws-billing-simulator-proposal.md](aws-billing-simulator-proposal.md). The implementation is intentionally not an AWS console clone; it preserves AWS billing mental models while using synthetic data and a standalone UI.

## Who It Is For

- Developers who deploy AWS resources but rarely see consolidated billing.
- Solutions architects designing account structures and tagging standards.
- Platform engineers building landing zones and shared-service models.
- FinOps analysts learning reporting, allocation, invoices, and reconciliation.
- Instructors and consultants who need repeatable billing labs without exposing real customer data.

## What It Teaches

The simulator is designed around billing consequences, not infrastructure behavior. Learners should be able to experiment with questions like:

- How do management accounts, member accounts, OUs, and payer accounts affect billing visibility?
- Why do tags not automatically appear in billing reports?
- How do synthetic resource usage events become metering records, line items, bills, and invoices?
- What changes when a workload is split by environment, product, or shared-service account?
- How do open-period estimated charges differ from closed-period finalized bills?
- How can invoices be reconciled back to bill line items?

## Current Features

The current codebase already includes these working foundations:

- A single Go command, `billing-sim`, that serves a local browser UI.
- Local-only HTTP binding to `127.0.0.1` or `localhost`.
- SQLite workspace directories with embedded migrations and WAL-mode persistence.
- A workspace selector that opens or creates a local `simulator.db` workspace and remembers the last workspace path.
- Embedded server-rendered HTML templates, CSS, and vanilla JavaScript partial-refresh behavior.
- An AnyCompany Retail seed organization with management account, OUs, member accounts, and a suspended account.
- Organization tree UI with account directory, account detail panels, billing/resource drilldown links, and lifecycle forms for creating, moving, suspending, and closing accounts.
- Synthetic price catalog data for EC2, EBS, S3, Lambda, RDS, NAT Gateway, CloudWatch Logs, data transfer, AWS Support, and AWS Marketplace examples.
- Resource lab UI for creating synthetic resources, adding tags, recording usage, generating deterministic usage, advancing the simulator clock, running daily metering, and closing billing periods.
- Billing pipeline persistence for usage events, metering records, priced bill line items, billing-period service summaries, support charges, month-end closes, issued bills, invoice obligations, and invoice documents.
- Bills UI with bill state summaries, charge breakdowns, resource-level charge rows, reconciliation data, printable invoice pages, and invoice line-item CSV export.
- Scenario JSON parsing and execution for deterministic lab setup, including packaged "First consolidated bill", "Missing Tags", and "Find the untagged data-transfer spike" scenario seeds.
- Cost allocation tag manager UI with discovered key/value coverage, spend and resource coverage by tag key/account/service, untagged and case-mismatched spend, activation, deactivation, 24-hour pending visibility timing, and usage-window tag snapshots.
- Cost Category rule persistence, preview UI, and line-item assignment snapshots for ordered account, service, region, usage-type, line-item-type, tag, and existing-category matches, including unmatched spend, rule-order effects, open-period refreshes, and finalized-period history.
- Cost Category split-charge rule persistence and allocation comparison UI for even, fixed-share, and proportional allocation methods, with deterministic rounding, source-line audit rows, raw/category/split/total cost views, unallocated residuals, open-period refreshes, and finalized-period protection.
- Cost Explorer query aggregation over priced line items with date range, granularity, dimension, tag, and Cost Category filters plus up to two groupings.
- Cost Explorer derived summary tables for daily cost, monthly account/service spend, tag coverage, and Cost Category value rollups after billing changes.
- Cost Explorer report builder UI with date range, granularity, metric, filter, group-by, saved report selection, saved definition editing, server-rendered line/bar/stacked-bar charts, dense result tables, aggregate-row bill line item drilldowns, and report result CSV export.
- Monthly budget definitions scoped by linked account, service, tag, or Cost Category with actual and forecast threshold checks backed by persisted simple month-end forecasts from elapsed-day run rate and scheduled scenario usage, plus in-app alert notification history for breached thresholds.
- Billing visibility policy modeling for management-account, member-account, finance, and instructor personas.
- Payment setup persistence for seller-of-record profiles, payer bill-to profiles, card/ACH/invoice-remittance/Advance Pay method types, default method selection, and invoice seller/bill-to snapshotting.

Some implemented pieces are persistence or policy foundations that do not yet have a full browser workflow. The backlog tracks those UI and reporting steps.

## Planned Product Areas

The broader MVP and later phases include:

- Payment state workflows, past-due handling, and remediation labs.
- CUR-like and FOCUS-style exports plus a query lab.
- Shared-cost lab workflows and Billing Conductor-style pro forma views.
- Instructor-authored scenarios, grading checks, and assessment review workflows.
- More advanced billing fidelity such as credits, taxes, Savings Plans, Reserved Instances, blended rates, and amortized views.

## Architecture

The simulator is intentionally small and local:

- `cmd/billing-sim` is the CLI entry point.
- `internal/app` owns configuration, local server startup, workspace selection, routes, templates, browser assets, and UI handlers.
- `internal/persistence` owns SQLite workspace opening, migrations, repositories, synthetic price data, billing math, metering, bills, invoices, tags, saved reports, and organization data.
- `internal/scenario` owns scenario definition parsing, seed loading, execution, and scenario audit records.
- `internal/billingvisibility` owns simulated billing access roles and policy decisions.

Core use does not require Node.js, npm, React, PostgreSQL, Electron, hosted services, AWS credentials, or payment integrations.

## Running Locally

Requirements:

- Go 1.24 or newer compatible with the module in [go.mod](go.mod).

Start the app with an explicit local port, workspace, and app state file:

```bash
GOCACHE="$PWD/tmp/go-build-cache" go run ./cmd/billing-sim -http 127.0.0.1:8080 -workspace ./tmp/workspace -state ./tmp/state.json -browser=false
```

Then open:

```text
http://127.0.0.1:8080/
```

You can also run without `-workspace`; the app will show the workspace selector first. By default, the CLI opens the browser and stores the last workspace path in the per-user app state file. Agents and automated smoke runs should always pass a project-local `-state` path such as `./tmp/state.json` when opening workspaces.

Useful flags:

```bash
go run ./cmd/billing-sim -help
```

## Development

Run the main quality gates:

```bash
go test ./...
go build ./...
```

This project uses `br` for issue tracking and `jj` for version control. Common tracker commands:

```bash
br ready
br show <id>
br update <id> --status in_progress
br close <id>
br sync --flush-only
```

`br stats` is useful as a dashboard check, but the installed tracker may still print a stale footer that says to use `bd list`, and its Recent Activity issue counters may show zero despite recent `.beads/issues.jsonl` changes. In this repository, ignore those external CLI hints and use `br list`, `br ready`, `br show`, and `br count` for issue details.

The local agent workflow and repository rules are documented in [AGENTS.md](AGENTS.md). Project context and session notes are indexed in [MIND_MAP.md](MIND_MAP.md).

## Safety Boundaries

AWS Billing Simulator is synthetic training software. It does not process real payments, does not produce tax-valid invoices, does not connect to AWS for core use, and should not be treated as authoritative pricing advice. The built-in catalog is synthetic and deterministic so learners can focus on billing behavior and reconciliation.
