# Committed-history publication review, 2026-10-04

Status: Publication reviewed; full implementation program remains paused.

This merge publishes the 13 completed feature commits through `d8d6c1ca311d395e677a5ced3d5981c3685dc68a` alongside the 20 newer main commits through `f2ebdfeb2076e76dedba944021e057079f05f0ed`. The isolated checkout excludes the paused, uncommitted identity and disclosure drafts.

## Composition corrections

- Preserve published protobuf field 43 for run correlation origins; assign runtime resource claims to field 44. Preserve published database releases 27–30 and append feature releases 31–34.
- Preserve provider provenance alongside analytical population/derivation metadata, and refuse provider attribution at generic ingestion and native worker boundaries.
- Keep durable management polling, sealed credentials, asynchronous provider collection, shared collector retry limits and complete paginated job discovery together.
- Limit the 32 management assignment slots to typed management jobs. Preserve 256 concurrent generic jobs, bounded pages, legacy rotation and required management admission.
- Preserve both provider and management archive artifacts, accepted counts, reconciliation and explicit incomplete coverage. Apply the shared evidence budget without failing the performance archive.
- Respect actual caller deadlines during collection-cache cleanup, including when context timer publication lags. Preserve the bounded busy-source retry for live callers.
- Restore binary upload API inference after the owned-reader refactor; regenerate Swagger and TypeScript contracts.
- Compose monitoring controls and provider configuration, chart export selections, sidebar layout, and bounded editor parsing. Fix the wrapped-script range lookup using the completed syntax tree.

Independent source review and re-review found no further actionable publication issues after these corrections.

## Actual verification

| Gate | Observed result |
| --- | --- |
| Full Go package lane | 123 packages passed; API reported four composition findings. Every reported API finding passed its corrective repeats below. The entire package lane was not rerun after corrections. |
| Corrected API checks | Default 37/37 and race 37/37 selected top-level tests passed; no skips or source/embedded-input changes. |
| Paging and management capacity | Default 10/10 and race 10/10 passed, including actual 256-job pagination and mixed-family admission. |
| Final PostgreSQL checks | 18/18 passed, no skips; published ledgers, migrations, foreign keys, management/native/upload ownership and concurrent initialization covered. |
| Core race lane | Analysis, default DuckDB, engine, generator RPC and collector passed. Storage reported the paging regression; corrective race checks passed. |
| Dashboard | Typecheck, lint, all 5,394 unit tests across 501 files and build passed after the parser correction. Typecheck/build repeated after the final generated contract. |
| Browser workflows | 30 dev-admin and 30 local-auth workflows passed against freshly built local controllers. |
| Native DuckDB | Complete `analytics_duckdb` package suite passed on local darwin/arm64 with 512 MiB memory and 128 MiB spill settings. |
| Generated contracts | DSL/API generation passed; final OpenAPI upload generation and freshness checks passed. |

Go lanes use the pinned 1.26.5 toolchain, isolated task cache, bounded parallelism and an API compile profile without DWARF. Native qualification on other platforms and large-import benchmarks remain part of the paused program.

[Raw receipts and logs](raw/api-final.json) retain both the original broad failures and the successful corrections. [Full Go output](raw/full-go-before-corrections.log), [core race output](raw/core-race-before-corrections.log), [corrected API default](raw/api-final-default.log), [corrected API race](raw/api-final-race.log), [PostgreSQL](raw/postgres-final.json), [browser profiles](raw/browser.json), [dashboard](raw/client-corrected.json), and [native analytics](raw/native.json) describe the executed scope and resource profiles.
