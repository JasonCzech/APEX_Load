# Feature completion Wave 1 — 2026-10-06

Status: implemented on `claude/feature-completion-wave1` and reviewed: each work package through independent review rounds until no finding stayed open, then once across the whole wave. Every finding is fixed, documented below, or accepted as a listed residual. The requirements these packages advance remain open; nothing here promotes support.

Wave 1 delivers eleven work packages of the [feature completion specification](../../../FEATURE_COMPLETION_SPEC.md); F1 (root-cause analysis v2) landed before it. Schema releases 43–48 are additive: `authoring-mutation-receipts`, `feature-evaluation-jobs`, `feature-acceptance-records`, `evaluation-job-request-digest-attempts`, `feature-acceptance-record-sequence` and `evaluation-job-result-bytes`.

## Work packages

| WP | Requirement | What landed | Commits |
| --- | --- | --- | --- |
| A1 | AUTH-1 | `GET /api/v1/scenarios/{id}?view=authoring` projection with a strong `scenario-authoring-v1` ETag; create and save responses carry the committed baseline | ea63ded97, 2a108ffae |
| A2 | AUTH-1 | Optional strong `If-Match` on scenario PUT and every secondary scenario writer, compared in the write transaction (412), and the opt-in per-project strict policy (428); durable source admission | cd78e3e82, c58d51e79 |
| A4 | AUTH-1 | `Idempotency-Key` on ordinary script and scenario creates through `authoring_mutation_receipts`; a deleted resource is tombstoned and never recreated | 2a71bfd4b, c58d51e79 |
| F2 | §11 | Shared leased evaluation-job runner (root-cause analysis moved onto it) and `/api/v1/analysis/evaluations`; no evaluation kind is registered yet | 7ab31c791, 8befd13e9 |
| F3 | §11 | Revisioned analysis policies of five kinds; project defaults pinned at Start with `applied: false` | 1d1033835, 36081a593 |
| F4 | §11 | Append-only feature acceptance records, status API and `apexctl qa acceptance --require-supported` | b04ef80d7, 9ab387e22, e9be8f645 |
| B2 | NA-1 | Precomputed summary imports with a declared statistic and window | 94ce4b9b6, 9ab26ef51 |
| B3 | NA-1 | Histogram imports, a distribution table, merges of identical bound sets and interpolated percentiles | 1b65aff58, 9ab26ef51 |
| C1 | TEL-2 | Per-process source epochs with exact or lower-bound publication loss, archived in `telemetry_sources.jsonl` | ff36d4cad, 8ed6d4e69 |
| D1 | BE-1 | Request evidence version 3: request-to-document association with cache and initiator provenance | 5d7f5efb9, 4cf01ecdf |
| D3 | BE-3 | Generator-wide browser evidence budget for native HAR writers and browser contexts | e01e5e187, 557a30ddf |

Later review rounds fixed findings across packages in 4595faf9e, b6f6a65e3, c1be22b18 and 6dfffd62d, and the cross-cutting review in 8d0028bb2.

## Review

Each package was reviewed by an independent agent that read a frozen snapshot of the implementation commit against its specification. Fix commits were then re-reviewed, each round against the previous round's findings and anything the fix introduced, until no finding of any severity stayed open without a fix or a documented reason. The first round raised 95 findings, 8 of them high. The second confirmed most fixes and found 28 new defects (6 medium) plus partly resolved items and missing tests. The third found 14 (2 medium, one of them an older defect in runtime credential recovery that the new create path made reachable). The fourth found one medium defect (the third-round fix had overwritten two real-browser watchdog tests; they were restored) and seven low ones, among them a PostgreSQL-only race in which a refused delete could withdraw the tombstone of a resource a concurrent delete removed, a 4-byte PostgreSQL column that broke the credential recovery fingerprint for fractional group SLAs, and a manifest size estimate that could refuse older stored manifests. The fifth found every package clean apart from low findings, among them recovery keeping a whole credential attempt when the stored scenario still referenced only one of its credentials; those were fixed, and a late re-review confirmed the telemetry-source package, whose earlier fixes were tests only. A final review read the whole wave for cross-package interactions and the program's shared rules: it found one high, eight medium and twelve low defects. The high one: in the shipped Linux images the evidence budget never measured the Playwright driver or browsers (BusyBox `ps` abbreviates resident memory and the controller image has no `ps`); memory is now read from `/proc`. The medium ones: saving project settings in the dashboard silently cleared the strict scenario-save policy; that policy blocks the dashboard's scenario editors (now documented); the policy routes' contract omitted their required `project_id`; an initiating origin escaped runtime-secret scrubbing; a session closed by the watchdog failed every action at once for the rest of the run; and the lifecycle guide, the name-column summary dashboard path and this record's residual list were incomplete. Two verification rounds of those fixes found the `/proc` sampling too costly per process (now one read per process) and a few documentation inaccuracies, all fixed. The review also prompted fixing an older defect: the native worker sized partition metadata by its own short handles while the controller's manifest carries full object keys.

Notable corrections from review:

- Resource-to-document association no longer compares the renderer's time origin with the browser's wall clock. A document reports its token to the evidence session's own binding, and the execution context that reported it identifies the loader, all on one ordered CDP session. A probe against Chromium confirmed the event order the design relies on.
- A browser session refused at memory admission retries at iteration boundaries instead of failing every action for the rest of the run.
- A broad acceptance pass can no longer restore a narrower subject that a broad revoke withdrew.
- Cumulative histogram windows are differenced across every file of an import, and an instant two files disagree on is a duplicate rather than a silent cross-series delta.
- The native worker sizes partition metadata against the controller's manifest, keyed by full object keys, rather than against its own short handles; metadata near the 1 MB limit that the worker accepted could fail an import, and now drops identities instead.
- A scenario create returns the tag of its own transaction, its lost commit acknowledgement no longer rolls back provisioned credentials, and the credential recovery fingerprint matches what storage returns.

## Documented deviations and open work

- **Authoring:** the dashboard's scenario editors send no `If-Match` yet, so a project with `require_scenario_save_if_match` cannot save, delete or rebind scenarios from the dashboard until editor conflict recovery (A3) lands; a project update that omits the policy keeps it. Receipts are claimed before the create and adopted after it rather than written in the create's transaction, and their request digests use a dedicated key that is not envelope-wrapped. An adapter store without durable revisions refuses conditions and the strict policy with 503. The audit marker is `save_mode: legacy_unconditioned`. Queued revise-copy idempotency, dashboard intent keys and scenario editor conflict recovery (A3, A6) remain.
- **Evaluation jobs:** the `apexctl` evaluations CLI, typed definition id/revision, a shared evaluation budgeter, per-kind permissions (every evaluation route currently needs only `results:read`), hashed result artifacts and a separate publishing state arrive with the first registered kinds.
- **Policies:** pins are recorded but not enforced (`applied: false`); scenario-level policy selection waits for the consumers.
- **Acceptance records:** one `binary_sha256` per record; capability-ledger wiring, demotion when evidence is deleted, review-receipt ingestion and a dashboard surface remain.
- **Summaries and histograms:** window columns, golden timezone/DST fixtures and display-only reading of summaries without a declared statistic remain; controllers older than this release must not rewrite mappings that declare them. Bound sets must match exactly to merge.
- **Telemetry sources:** per-interval gap ranges and observation windows, cumulative counter retention and stage distributions remain (C3 and later). Older generators count as `legacy_epochs`; a terminal batch restored after a generator restart replays under the new process's epoch, unsequenced. The TEL-2 exit cases (a restart opens a second epoch; retried and duplicated publications count once) are proven at ledger, store and broker level, not by restarting a real generator mid-run.
- **Browser evidence:** service-worker provenance through CDP, one shared 30-second close and sanitization deadline, the 256 in-flight request cap, flush-time quota and metadata-flood tests, journey censoring, counting retained artifacts against run quotas, and reporting budget counters to the controller remain. WebKit association depends on Resource Timing reaching the controller before the request completes. Init-phase actions on a refused context fail that VUser, and a session closed by the watchdog or its HAR size cap ends its VUser at the next iteration boundary unless a new user per iteration replaces the session. Memory is read from `/proc` on Linux, but its measurement has not been qualified in the shipped images with real browsers, and the 128 MiB per-context floor predates document evidence's second CDP session per page.

## Accepted residuals

Reviewers raised these; each is low severity, fails safe, and stays open:

- **Authoring:** a winning delete renews its tombstone after its commit rather than inside its transaction, so between the commit and the renewal a racing refused delete can briefly withdraw it, and a failed renewal is only logged. A first create attempt that did not commit, retried with runtime credentials, waits out the provisioning journal's lease (400) before it can run again.
- **Browser evidence:** a popup's or new window's first document, and one committed while the evidence session's install waits behind another blocking call such as a dialog, may stay unassociated (never wrongly associated).
- **Tests accepted without a dedicated case:** the C1 consumer-level generator restart and terminal retry (structural, single observation site), an API-level evaluation-job completion fence (storage-level on both databases), the HAR close-context wiring (one call site; retirement is unit-tested), and caller-level mixed summary lineage (guarded by exact slice equality).

## Gates

Final gates on 8d0028bb2 (macOS arm64, Go 1.26.5, Node 22, PostgreSQL 16 in a local container), each a full run:

| Gate | Command | Result |
| --- | --- | --- |
| API | `go test ./pkg/api/ -count=1` | pass (679 s) |
| Storage | `go test ./pkg/storage/ -count=1` | pass |
| Analysis | `go test ./pkg/analysis/... -count=1` | pass |
| DuckDB engine | `go test -tags analytics_duckdb ./pkg/analysis/duckdb/ -count=1` | pass |
| API with DuckDB | `go test -tags analytics_duckdb ./pkg/api/ -run 'Analysis\|Histogram\|Summary\|Capabilit'` | pass |
| Protocols, including real Chromium and WebKit | `go test ./pkg/protocol/... -count=1` | pass |
| CLI, distributed, engine, QA docs | `go test ./cmd/apexctl ./pkg/distributed/... ./pkg/engine/... ./docs/qa -count=1` | pass |
| PostgreSQL storage | `go test -tags postgres_integration ./pkg/storage/ -run Postgres` | pass |
| PostgreSQL API | `go test -tags postgres_integration ./pkg/api/ -run Postgres` | pass |
| Dashboard | `npx vitest run` | 6,207 passed |
| Docs | `python3 scripts/check-docs.py` | pass |

The `/proc` process sampling was also run inside a `postgres:16-alpine` container (BusyBox userland): its tests pass and 200 scans take about 30 ms. Browser end-to-end workflows (`npm run test:cycle`) and the HA, RCA real-run and live-provider opt-in suites were not run for this wave.

## Retained failures

- The second-round review found that `TestImportedHistogramQueriesExplainUnanswerableRequests` failed in the default (non-DuckDB) build after the B3 fix; targeted runs had not selected it. It now expects the bound limit only from a build with the analytics engine, and every later round ran the full default `pkg/api` suite before committing.
- The first full API run after the second-round fixes failed three structural tests (lock-held durable-write analysis, the scenario writer inventory and the DELETE ETag contract); the code was adjusted to the repository's idioms and the suite then passed.
- `TestWebKitAssociatesOnlyThroughUniqueResourceTiming` failed once under full-package load because no WebKit Resource Timing entry beat its request's completion; the test now retries a fresh session up to three times while still failing any wrong join.
