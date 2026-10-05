# Service Bus management review

Status: final collector, durable SQLite/API, full Go, targeted API race and dashboard verification passed. No live Azure qualification is claimed.

## Behavior and boundaries

The executable `servicebus-runtime` pack reads only fixed entity descriptions for a finite, explicitly approved namespace/inventory using a pinned shared-key Manage principal. Numeric routing, protected immutable CA trust, fixed port 443, no DNS/proxy/redirect/retry expansion, source-wide minimum 30 s cadence and independent socket/TLS/GET activity admission preserve the existing signed authority and physical ownership contract. Missing/deferred/unsupported populations remain explicit; exact observed decimal strings retain zero. This management pack does not enable data-plane transactions.

Source authoring separates saved approval from the unsaved draft, supports hierarchical queues/topic plus single-segment subscriptions, preserves revision conflicts, and retains historical ten-second source definitions for reads/deletion while requiring current cadence for a new revision. The shared Kafka reader rejects foreign Service Bus transport settings.

The adopted drafts received independent bounded source reviews of fixed configuration, SAS issuance, finite response decoding, physical transport ownership, scheduler authority, durable cadence and client workflows. The final readiness/cadence re-review is clean. Two final product findings were fixed and re-reviewed:

- Signed Service Bus jobs now enforce the same minimum thirty-second interval at the collector's first admission boundary; previously the later observer rejected an undersized interval. A signed malformed/old-issuer table tests all three management packs around their boundaries.
- The local-auth identity card and sign-out action escaped the CSS-narrowed sidebar. The narrow layout now contains both, with real browser geometry assertions and inspected screenshots.

The first two new API cohort attempts failed because the test fixture advertised an unkeyed destination and then used the legacy host-only readiness helper. The real collector already advertised its enrolled key. The corrected fixture exercises the actual HTTP readiness headers, rejects unkeyed/wrong-key approval, verifies the sealed key/principal, rotates the secret and verifies durable duplicate-batch/cohort receipts. These were fixture corrections, not a product readiness change.

## Validation

Final collector normal passed (11.976 s) and race passed (14.132 s) after the admission fix. Full SQLite storage passed (97.543 s). The first storage race run exhausted temporary/database storage; the repeat passed (105.426 s) after exact task-owned superseded object cleanup. The final new actual API cohort passed (1.530 s).

The final full `npm run test:cycle` passed: typecheck, lint, 462 unit files/4,832 tests, a fresh embedded build, 198 dev-admin browser workflows (4 existing skips) and 118 local-auth workflows (1 existing skip). The six new desktop/narrow source/approval screenshots were inspected; no further finding in those views. This includes the added signed-in sidebar bounds assertion.

The first full cycle had two dev-admin archive-download timeouts; their traces showed HTTP 500, but the old controller log was not retained, so the precise cause is unproven. The independently repeated lifecycle/archive workflows passed (3 tests, 12.9 s) with full controller logs retained, and both downloads passed again in the final full cycle. An initial repeat selected no tests because the title regex used a slash instead of the existing space; it is not acceptance evidence. Original fixture/cycle failures are retained, rather than counted as passes.

Full `go test ./...` passed, including the API package (500.816 s), SQLite storage (44.113 s), generated-contract freshness and documentation tests. Final targeted API race passed; the final standalone documentation check passed. The completed logs and screenshot hashes are in [the retained evidence manifest](retained-evidence-final.json); compressed logs preserve exact raw hashes. The local-auth controller snapshot is an observation captured before fixture teardown, not a complete shutdown log.

Docker daemon availability timed out in a bounded five-second read-only check. Actual PostgreSQL 16 cadence integration is pending; source fixtures do not establish it. Live Azure counters, tiers and Entra identities remain unqualified.

Root uses a task-only package-specific API compiler profile (`-gcflags=github.com/jasonczech/apexload/pkg/api=-dwarf=false`, with stripped test linking where applicable) to reduce scratch demand. Independent pinned Go1.26.5 source inspection confirms this removes debugger metadata while preserving runtime PC/file/line/inline tables, panic/Callers and Go race instrumentation/symbolization. It does not qualify debugger/native-C symbolization or release packaging. No test assertions or production resource reserves are changed.

During this slice, nine exact task-owned obsolete DWARF API archives were pruned after independent profile/source and open-file checks, reclaiming 1,165,252,940 bytes; current API/dependency objects and another chat’s separate cache were preserved. [Inspection](obsolete-api-cache-inspection.json) and [prune receipt](obsolete-api-cache-prune.json) retain the exact identities. Final cache cleanup remains deferred while later program slices need the caches. The 10 GB benchmark waiver does not waive native 1 GiB, platform or live-provider acceptance.
