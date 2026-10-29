# QA Review & Qualification Records

Status: Active

This directory retains active capstone qualification records, foundational evaluation ledgers, and the local broker fixture (`import-fixture-matrix-2026-09-12/brokers.compose.yaml`) used by [`scripts/run-local-broker-integration.py`](../../../scripts/run-local-broker-integration.py). Completed slice-by-slice and wave review narratives are consolidated into themed historical ledgers under [`docs/archive/qa-reviews/`](../../archive/qa-reviews/).

Per [`AGENTS.md`](../../../AGENTS.md), review records under `docs/qa/reviews/` and `docs/archive/qa-reviews/` are Markdown only (apart from `import-fixture-matrix-2026-09-12/brokers.compose.yaml`). Raw evidence (screenshots, Playwright traces, test logs, JSON captures, archives) stays untracked on local disk or attached to CI runs and must be cited as code-span paths rather than Markdown links.

## Active Capstone & Foundational Records

| Record | Date | Scope |
| --- | --- | --- |
| [roadmap-int-1-2026-10-28/README.md](roadmap-int-1-2026-10-28/README.md) | 2026-10-28 | End-to-end non-Messaging roadmap integration gate (`INT-1`) across `REC`, `AUTH`, `PROTO`, `NA`, `TEL`, `BE`, and `BI` tracks |
| [roadmap-rec-c-local-faults-2026-10-28/README.md](roadmap-rec-c-local-faults-2026-10-28/README.md) | 2026-10-28 | `REC-C1` repeatable 3-repetition integrated local fault qualification (7 failover/evacuation scenarios × 3 reps = 21 `-race` runs reconciled against `cmd/apexfault-target`) |
| [roadmap-na-d1-local-macos-2026-10-28/README.md](roadmap-na-d1-local-macos-2026-10-28/README.md) | 2026-10-28 | `NA-C3` physical writer ownership completion (`_analysis/source.v1`, staging reclamation, admin writer attestation) & `NA-D1` local `darwin/arm64` native DuckDB reference fault trials |
| [node-failure-readiness-2026-09-27.md](node-failure-readiness-2026-09-27.md) | 2026-09-27 | Foundational node-failure readiness ledger and local fault harness baseline referenced by [`NODE_FAILURE_READINESS_PLAN.md`](../../NODE_FAILURE_READINESS_PLAN.md) and [`scripts/fault_harness/README.md`](../../../scripts/fault_harness/README.md) |
| [evaluations-2026.md](evaluations-2026.md) | 2026-09-24 | Foundational Data Pools and LoadRunner runtime-settings parity evaluations |
| `import-fixture-matrix-2026-09-12/brokers.compose.yaml` | 2026-09-12 | Disposable local broker Compose definition consumed by [`scripts/run-local-broker-integration.py`](../../../scripts/run-local-broker-integration.py) (see [`LOCAL_BROKER_CHECKS.md`](../LOCAL_BROKER_CHECKS.md)) |

## Consolidated Historical Review Ledgers (`docs/archive/qa-reviews/`)

| Archive Ledger | Contents |
| --- | --- |
| [AUDITS_2026-09.md](../../archive/qa-reviews/AUDITS_2026-09.md) | September 2026 UX, accessibility, dashboard, scenario-builder, and import audits |
| [MESSAGING_2026-10.md](../../archive/qa-reviews/MESSAGING_2026-10.md) | Early-October Kafka, Event Hubs, Service Bus, and Solace provider/management slice reviews |
| [ANALYTICS_2026-10.md](../../archive/qa-reviews/ANALYTICS_2026-10.md) | Early-October shared analytical contract, partition publication, and live/grouped overlay reviews |
| [ANALYTICS_SLICES_2026-10.md](../../archive/qa-reviews/ANALYTICS_SLICES_2026-10.md) | October 2026 native analytics, DuckDB worker, joint projection, byte catalog, precision, and writer-ownership slice reviews (22 records) |
| [AUTHORING_2026-10.md](../../archive/qa-reviews/AUTHORING_2026-10.md) | Early-October authoring goals persistence and revision-safety reviews |
| [AUTHORING_AND_IMPORT_SLICES_2026-10.md](../../archive/qa-reviews/AUTHORING_AND_IMPORT_SLICES_2026-10.md) | September–October 2026 authoring fidelity, scenario revision/admission, lifecycle property matrix, validation context, and multi-format import reviews (28 records) |
| [BROWSER_2026-10.md](../../archive/qa-reviews/BROWSER_2026-10.md) | Early-October browser availability, document evidence, and HAR import compatibility reviews |
| [BROWSER_SLICES_2026-10.md](../../archive/qa-reviews/BROWSER_SLICES_2026-10.md) | October 2026 browser document/interaction/resource evidence, native HAR ownership, replay recovery, TruClient, and browser intelligence slice reviews (83 records) |
| [TELEMETRY_AND_PROTOCOL_SLICES_2026-10.md](../../archive/qa-reviews/TELEMETRY_AND_PROTOCOL_SLICES_2026-10.md) | September–October 2026 telemetry policy receipts/segments, render population, Java HTTP representations, and controlled-endpoint protocol reviews (25 records) |
| [RECOVERY_AND_PLATFORM_SLICES_2026-10.md](../../archive/qa-reviews/RECOVERY_AND_PLATFORM_SLICES_2026-10.md) | September–October 2026 generator evacuation rollback/rollforward, same-leader intervention, terminal-cause, desktop ownership, and collector/capacity reviews (12 records) |
| [INTEGRATION_2026-10.md](../../archive/qa-reviews/INTEGRATION_2026-10.md) | Early-October committed publication and composed qualification reviews |
| [INTEGRATION_AND_CODEBASE_REVIEWS_2026-10.md](../../archive/qa-reviews/INTEGRATION_AND_CODEBASE_REVIEWS_2026-10.md) | October 2026 Wave 0–5 delivery reviews, full-codebase/monthly audits, peer-live verification, and E2E isolation reviews (22 records) |
