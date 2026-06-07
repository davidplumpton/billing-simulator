# Query Lab DuckDB Decision

Status: accepted for `bd-hz2.2.1`

Decision date: 2026-06-07

## Decision

Do not embed DuckDB in the simulator runtime for the first local query lab.

Build the query lab as external SQL examples over generated CUR-like CSV exports and workspace review artifacts. DuckDB can be an optional learner tool, but it should not become a required Go module, build tag, bundled library, or runtime dependency unless a later ticket proves that in-app SQL execution is worth the packaging cost.

## Context

The simulator currently ships as one local Go binary backed by SQLite through `modernc.org/sqlite`. That keeps the app aligned with the README contract: no hosted services, no Node.js or npm, no separate database server, and deterministic local workspaces.

The official DuckDB Go client is a `database/sql` driver and is MIT licensed:

- https://duckdb.org/docs/stable/clients/go
- https://github.com/duckdb/duckdb-go
- https://duckdb.org/faq

That makes DuckDB compatible from a license perspective. The tradeoff is packaging and portability. The Go driver links DuckDB libraries into the binary by default, uses CGO, documents platform-specific build requirements, and requires extra work for unsupported or custom platforms. Those costs cut against the simulator's small local packaging goal for a feature that can already be taught through generated exports.

## Rationale

Keep the app dependency model stable. The current dependency surface is pure Go plus SQLite. Adding a CGO-backed analytical engine would expand local build requirements and cross-platform release checks before the query lab has proved it needs server-side SQL execution.

Keep core workflows independent from external tooling. Learners should still be able to run scenarios, generate bills, export CUR-like CSV files, and reconcile invoices without installing DuckDB. Query examples can be optional exercises once the export artifact exists.

Use the export boundary as the teaching boundary. The query lab is meant to connect UI reports to CUR-like data. Running examples against exported CSV files keeps that lesson explicit and avoids exposing internal SQLite schema as the learner-facing contract.

Leave a clear revisit point. If later work needs interactive in-app SQL, Parquet/FOCUS exports, or large-file analytical execution that SQLite cannot reasonably provide, revisit DuckDB as an optional build or packaged feature with dedicated release tests.

## Consequences

`bd-hz2.2.2` adds query examples that target exported CUR-like CSV files, not internal simulator tables. The first set covers linked-account totals, untagged spend, top usage types, invoice reconciliation, and allocated cost comparisons.

The app should continue to expose export generation, download, regeneration, and reconciliation through existing workspace-local routes. Query-lab examples can point learners from `/exports` and `/query-lab` to an external SQL command or SQL client.

`go.mod` and `go.sum` should not add `github.com/duckdb/duckdb-go/v2` for this phase.

## Fresh Workspace Exercise

1. Start the simulator with a project-local workspace and state file.
2. Launch a packaged scenario that closes a billing period, such as "First consolidated bill".
3. Open `/exports`, generate or regenerate a CUR CSV export, and download the generated file.
4. Open `/query-lab`, then run an optional external DuckDB CLI query against the CSV:

```sql
SELECT
  usage_account_id,
  ROUND(SUM(CAST(unblended_cost AS DOUBLE)), 2) AS unblended_cost_usd
FROM read_csv_auto('tmp/workspace/exports/<cur-file>.csv')
GROUP BY usage_account_id
ORDER BY unblended_cost_usd DESC;
```

This exercise proves the query lab can be practiced from a fresh workspace without embedding DuckDB in the app.

## Verification

For this decision ticket, verification is documentation and dependency-surface based:

- `go.mod` remains unchanged and does not require DuckDB.
- The query-lab decision is linked from `README.md`.
- `MIND_MAP.md` records that future query-lab work keeps DuckDB optional and external unless a later ticket changes the packaging decision.
