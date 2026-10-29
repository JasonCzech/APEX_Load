# Completed recovery, evacuation & platform slice reviews — Sep–Oct 2026

Status: Historical. Consolidated on 2026-10-29 from completed scoped review records under `docs/qa/reviews/`. Original dates, validation outcomes, failed intermediate checks, source hashes and qualification limits are preserved intact below. Untracked raw evidence remains local at the cited paths. Use the [active review index](../../qa/reviews/README.md) for current capstone records.

## Records
- [Desktop save descriptor ownership review (`desktop-file-ownership-2026-09-28.md`)](#desktop-file-ownership-2026-09-28)
- [Diagnostics, collectors and preparation review — 2026-10-01 (`diagnostics-collectors-capacity-2026-10-01/REVIEW.md`)](#diagnostics-collectors-capacity-2026-10-01)
- [Evidence expansion — 2026-10-02 (`evidence-expansion-2026-10-02/REVIEW.md`)](#evidence-expansion-2026-10-02)
- [Recovery shutdown review (`feature-completion-recovery-shutdown-2026-10-03/README.md`)](#feature-completion-recovery-shutdown-2026-10-03)
- [Object activity and archive review — 2026-10-02 (`object-activity-2026-10-02/REVIEW.md`)](#object-activity-2026-10-02)
- [Evacuation completion (REC-A) (`roadmap-evacuation-completion-2026-10-25/README.md`)](#roadmap-evacuation-completion-2026-10-25)
- [Evacuation replacement restart (REC-A) (`roadmap-evacuation-replacement-restart-2026-10-24/README.md`)](#roadmap-evacuation-replacement-restart-2026-10-24)
- [Evacuation rollback (REC-A) (`roadmap-evacuation-rollback-2026-10-22/README.md`)](#roadmap-evacuation-rollback-2026-10-22)
- [Evacuation roll-forward (REC-A) (`roadmap-evacuation-rollforward-2026-10-24/README.md`)](#roadmap-evacuation-rollforward-2026-10-24)
- [Execution shutdown joins — 2026-10-21 (`roadmap-execution-shutdown-join-2026-10-21/README.md`)](#roadmap-execution-shutdown-join-2026-10-21)
- [Generator terminal cause (REC-A) (`roadmap-generator-terminal-cause-2026-10-24/README.md`)](#roadmap-generator-terminal-cause-2026-10-24)
- [Same-leader evacuation intervention — 2026-10-19 (`roadmap-same-leader-intervention-2026-10-19/README.md`)](#roadmap-same-leader-intervention-2026-10-19)

<!-- Original record: docs/qa/reviews/desktop-file-ownership-2026-09-28.md; local evidence kept outside git -->
<a id="desktop-file-ownership-2026-09-28"></a>

## Desktop save descriptor ownership review


The full Go run for the local recovery work exposed an intermittent `bad file
descriptor` during temporary-directory cleanup. Review identified a concrete
ownership bug in Unix save-file reuse: `acquireTempAt` wrapped a reusable file in
`os.File`, returned only its integer descriptor, and the caller created a second
wrapper. Garbage collection could close the first wrapper while the second was
writing, or later close a descriptor reused for another file.

The helper now transfers the original `*os.File` to its caller and wraps newly
created raw descriptors exactly once. Existing no-follow checks, inode identity
checks, publication rules, scrubbing and recovery-file retention remain in place.
Review of the surrounding Unix descriptor-return paths found no further instance
of this ownership pattern.

A regression forces garbage collection after acquisition and before writing,
checks both new and existing destinations, and confirms that the published inode
is the reused file. Both cases failed before the fix. Three complete desktop
suite repetitions passed in 3.320 seconds; thirty forced-GC repetitions passed
in 5.525 seconds (`/tmp/apex-desktop-fd-after.log` and
`/tmp/apex-desktop-fd-gc-repeated.log`). This verifies the native save operation;
no dashboard source or user workflow changed. The full repository suite subsequently passed
(`/tmp/apex-reviewed-local-fixes-full.log`).

<!-- Original record: docs/qa/reviews/diagnostics-collectors-capacity-2026-10-01/REVIEW.md; local evidence kept outside git -->
<a id="diagnostics-collectors-capacity-2026-10-01"></a>

## Diagnostics, collectors and preparation review — 2026-10-01


Scope: explainable statistical findings and error-pattern delivery; dependency-to-monitor interval drilldown; authenticated OTLP and pinned SSH collection; restricted JMX/PostgreSQL helpers; immutable preparation capacity policies. Unrelated browser-recorder changes in the shared checkout were preserved.

<a id="diagnostics-collectors-capacity-2026-10-01--findings-fixed"></a>
### Findings fixed

- Advisory notifications could inherit threshold-failure wording. Advisory explanations now survive notification routing and remain separate from stop decisions.
- Dependency links selected a metric without selecting its observation interval. Both API filters and dashboard overlays now use that UTC window. Mixed-quality evidence and bounded historical scans expose incomplete coverage.
- Latest dependency evidence was fetched only once. It now refreshes every five seconds without overlapping requests; historical windows remain fixed.
- OTLP sampling could miss delayed points or replay accepted points. Collection now includes retained late arrivals, with per-run acknowledgements persisted alongside pending batches and isolated across overlapping runs.
- OTLP HTTP success preceded durable receipt. The receiver now requires spool ownership and a recovered, atomically replaced and synced journal before acknowledging ingestion.
- Canonical metadata could expand accepted OTLP exports beyond the controller request limit. Collection now splits them into bounded batches, reserves room for renewed signed leases, and drains the retained remainder without waiting for another collection interval. The regression and collector race tests pass.
- Adapter units were inconsistent. Accepted scalar units are normalized; unsupported units and metric semantics are explicitly rejected, with rejected point counts.
- Missing credentials could block polling for every job on a collector. The controller now returns a signed unavailable status for the affected job while allowing other jobs to proceed.
- Required source credentials were not checked during preparation readiness. They are now resolved without target traffic before readiness, renewal and Start; unsupported credential-bearing packs are rejected.
- Fresh wall-clock buckets could span a pause. Qualification now also checks active-runtime advancement in live and archived baselines.
- The new endpoint lacked a pinned authorization fingerprint. Its project-viewer and results-read access contract is now included in the authorization snapshot.
- Archived statistical windows lost stale-observation qualification, and the finding cap could be exceeded by a multi-metric append. JSONL/Parquet summaries now retain the observation state and the cap is enforced exactly.
- Preparation capacity selection persisted across run changes. It now resets with the run, and the new selects expose explicit accessible labels.
- Ephemeral provider reconciliation could touch standing capacity. Explicit ephemeral plans isolate burst ownership and enforce ECS spot ceilings/on-demand floors; unsupported required policies fail.
- A standalone retry could lose a run's immutable capacity policy. Run-owned requests now require a new preparation/run instead of the standalone provisioning retry path.

<a id="diagnostics-collectors-capacity-2026-10-01--verification"></a>
### Verification

- Collector and provider unit/race checks pass, including real SSH transport with rejected host-key substitution, sealed credential identity/expiry checks, durable OTLP restart/deduplication, and ECS spot allocation/standing isolation.
- Real authenticated TLS JMX and PostgreSQL fixtures pass, including credential rejection and arbitrary query-pack rejection. The JDBC fixture verifies the pinned driver checksum before use.
- SQLite storage and PostgreSQL integration/migration checks pass.
- All 442 dashboard unit files / 4,663 tests pass on the stable source tree (`--maxWorkers=2 --testTimeout=30000`). Earlier broad runs exposed load-sensitive timeouts; isolated retries passed.
- Typecheck, lint, dashboard build and documentation checks pass. Generated API artifacts and the QA contract inventory are refreshed through repository generators.
- Fresh-controller dev-admin and local-auth browser checks pass for preparation policy submission/renewal/Start, Parquet workflow, dependency interval drilldown, and gRPC/WebSocket evidence (5 tests per profile).
- Full browser cycle ran both profiles: dev-admin 172 passed / 3 failed / 4 skipped; local-auth 94 passed / 1 failed / 1 skipped. The preparation failures used an earlier embedded bundle and pass against a fresh final build in both profiles. Live-metrics and library-assignment failures pass unchanged on a fresh-controller retry (3 passed / 1 profile-specific skip). No failed browser check remains unverified.
- Full Go suite passes: `APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -ldflags=-w -timeout 20m -p 1 ./...` exited successfully, including the API authorization snapshot and generated-contract checks. The final collector batch-bound fix also passes `go test -ldflags=-w -race -p 1 ./pkg/collectoragent`.

<a id="diagnostics-collectors-capacity-2026-10-01--acceptance-limits"></a>
### Acceptance limits

The helper intentionally supports fixed JVM attributes and a PostgreSQL connection-health query, not arbitrary JMX invocation or SQL. OTLP ingestion accepts bounded HTTP JSON gauges and cumulative monotonic sums; other metric semantics are explicitly rejected. Receiver retention is five minutes and 512 points per approved stream.

Ephemeral capacity requires a supported asynchronous capacity backend. Kubernetes/static targets retain existing-capacity preparation; unsupported ephemeral or required spot requests fail explicitly. Cloud-provider live acceptance across every runtime/target remains outstanding. Local deterministic tests do not establish cloud production readiness. The separately requested 10 GB Parquet benchmark remains skipped at the user's direction.

<a id="diagnostics-collectors-capacity-2026-10-01--final-review-outcome"></a>
### Final review outcome

Repeated review and fix passes found no remaining actionable findings in the implemented scope. Acceptance limits above remain explicit.

<!-- Original record: docs/qa/reviews/evidence-expansion-2026-10-02/REVIEW.md; local evidence kept outside git -->
<a id="evidence-expansion-2026-10-02"></a>

## Evidence expansion — 2026-10-02


This pass starts from published main `965675ff` and implements one bounded slice in each requested area: Event Hubs, protocol diagnostics, native analytics, authoring cleanup, composition fidelity, browser evidence, and telemetry reporting. Each slice receives tests and a repeated review/fix pass before its commit. Real Azure acceptance remains separate from offline SDK/contract verification; IBM MQ stays low priority and the 10 GB benchmark stays waived.

<a id="evidence-expansion-2026-10-02--event-hubs-exclusive-consumption"></a>
### Event Hubs exclusive consumption

Implemented an explicitly scoped fixed-partition consumer with a JSON-safe owner epoch, lazy construction, disabled prefetch, versioned generator capability and terminal SDK ownership-loss fencing. Observer and processor behavior retain their contracts. Review round 1 prevented exclusive mode from inheriting the observer consumer-group environment default. Round 2 prevented malformed internal exclusive configuration from falling back to SDK non-exclusive options and aligned the handwritten nullable TypeScript field with generated contracts. Final review found no further actionable issues. Default Event Hubs/DSL/engine tests, final targeted configuration tests, full real Event Hubs plus identity-policy race tests, dashboard typecheck and four script-contract unit tests passed. DSL/API/TypeScript artifacts were regenerated. API freshness and full repository/browser gates are recorded at pass completion. Live Azure epoch takeover remains an unverified acceptance item.

<a id="evidence-expansion-2026-10-02--grpc-client-call-analysis"></a>
### gRPC client-call analysis

Added qualified client-call message/byte rates, mean protobuf payload sizes and overlap-aware codec ratios to live/archive cards and exported diagnostic reports. These use retained cohort sums, preserve measured zero and unavailable measurements, and never claim server or run-wall throughput. Review also found the report detail budget could be exceeded by property rows; output now has an explicit 200-line cap and omission notice with a regression test. Calculation, protocol/gRPC, selected API/report tests, dashboard typecheck/lint and five utility tests passed. All three protocol diagnostic browser workflows passed in both dev-admin and local-auth profiles. Final review found no further actionable findings.

<a id="evidence-expansion-2026-10-02--native-retained-observation-rates"></a>
### Native retained observation rates

Implemented action/transaction observation, failure and success rates in source live intervals, merged buckets, grouped archives and saved archive comparisons. Saved panel authoring reopens the rate contract and displays observations/s. Review rejected rate requests without an explicit population, retained missing buckets as unavailable, required complete merged source-window coverage and used clipped bucket spans. Storage/API tests, 15 affected UI tests and real saved-panel browser workflows passed in both auth profiles. The initial API retry collided with asset regeneration and another retry with concurrent source changes; the final stable-source focused API gate passed. Final review found no further actionable findings. This is retained-observation rate analysis; no arbitrary formula execution or 10 GB scale acceptance is claimed.

<a id="evidence-expansion-2026-10-02--authoring-support-cleanup"></a>
### Authoring support cleanup

Added catalog-derived canonical action membership to the native authoring support matrix, corrected stale real Kafka seek/transaction guidance and included native Event Hubs observer/processor/exclusive readiness with honest build-tag and live-acceptance qualifications. Catalog parity and authorized viewer endpoint tests passed in the final stable-source focused API gate. Generated API/TypeScript action membership fields accompany this commit. Review confirmed that catalog membership is authoring guidance, while build/runtime/policy validation remains authoritative. No unproven compatibility adapter was removed. Final review found no further actionable findings.

<a id="evidence-expansion-2026-10-02--parallel-repeat-composition-fidelity"></a>
### Parallel repeat composition fidelity

Parallel branches now share DSL/runtime admission and support break/continue owned by a loop inside the same branch. New generator capability admission is enforced both at placement and assignment staging. Nested parallel and unowned loop control remain rejected. A real independent HTTP counter verifies early exit, repeat counts, skipped actions and cancellation. Review round 1 found and fixed a nil variable map inherited by an empty branch. DSL/engine/CloudTest affected race tests, RPC feature tests and placement tests passed; final review found no further actionable findings. CloudTest support guidance no longer says native parallel is unavailable, but proprietary Burst/repeat mapping remains fixture-blocked. Real customer export acceptance is not claimed.

<a id="evidence-expansion-2026-10-02--browser-resource-evidence"></a>
### Browser resource evidence

Added bounded top-50 resource inspection with exact action/failure filtering and page, capture-action time, method and device context. Network failure classification propagates from sanitized archive records into the report. The UI preserves explicit zero versus missing values and states that sample rows do not provide individual request-start timing or transport phases. Review corrected the new browser fixture's report-query matching and explicit Diagnostics navigation, and added the new workflow to the authenticated profile's test list. Thirteen component/archive UI tests and the corrected real-browser workflow passed in both auth profiles; the archive failure-attribution test passed in the focused API gate. Final review found no further actionable findings. Broad gate outcomes appear below.
<a id="evidence-expansion-2026-10-02--whole-run-telemetry-reporting"></a>
### Whole-run telemetry reporting

Saved baseline comparisons now retain whole-run health classification coverage, breach fractions, peak observed drop counters and maximum sample age separately from selected stable load windows. UI and HTML/CSV/DOCX/PDF exports preserve missing evidence and measured zero. Sampling cadence and threshold policies remain explicit qualifications; advisory telemetry does not change regression verdicts.

Review round 1 found cumulative drop counters were being added again on repeated interval publication; archive aggregation now retains their peak, with comparison warnings when counters decrease. Round 2 prevented raw counters without classifications from being reported healthy. Round 3 added explicit drop-counter availability so metadata produced for a legacy generator cannot imply zero loss. Regression tests cover repeated/reset counters, missing classification, legacy peers, measured zero, persisted comparison evidence and exported availability. Final targeted API race tests passed.

The broad browser review additionally found a run-name race in authoring: a scenario catalog response arriving after the operator edits the name overwrote it with the scenario default. A deterministic delayed-response component test reproduced the issue before the fix. Edited names and restored drafts now survive metadata loading; explicit scenario/project changes retain their reset behavior. All 78 run-setup tests passed, and the authenticated end-to-end workflow now asserts the created run name directly.

<a id="evidence-expansion-2026-10-02--repository-validation"></a>
### Repository validation

The final full `go test -p 1 -timeout 30m ./...` gate passed. Real Event Hubs and identity-policy race tests passed, as did the real Event Hubs/Kafka authoring support checks. Native DuckDB backend tests passed with `analytics_duckdb`, and the native analytics API rate/query/import/schema selection passed. Targeted composition and final API race gates passed. Dashboard typecheck, lint and all 455 unit-test files (4,747 tests) passed after the authoring fix. API/DSL/TypeScript contracts were regenerated, and documentation checks passed for 95 active Markdown files.

The first complete dashboard cycle identified the resource-evidence fixture issue and the authoring name race described above. Both were fixed rather than waived. A corrected complete dev-admin browser run passed 188 tests with four skips before the name fix. The final complete `npm run test:cycle` passed on the corrected build: 188 dev-admin browser tests passed with four skips, and 108 local-auth browser tests passed with one skip. The authenticated create/edit/launch/stop/archive/export/download workflow passed with the custom run name assertion. Final review after these fixes found no further actionable findings.

The initial seven slices introduced no storage migration; the additional review below adds schema release 26. Approved live Azure epoch takeover and processor acceptance, controlled real Kafka broker acceptance, measured native-platform release packaging, proprietary CloudTest fixtures and richer browser transport evidence remain backlog acceptance. The 10 GB benchmark remains explicitly waived; none of these gates is represented as passed.


<a id="evidence-expansion-2026-10-02--additional-review-and-fixes"></a>
### Additional review and fixes

The requested additional review covered all seven feature slices, with independent reviews for messaging/composition, native analytics/telemetry and browser/authoring. The final read-only review of the corrected code found no further actionable issues.

- Replay parallel branches could clone an empty variable map to nil and panic when a repeat wrote its loop index. Debugger stops and cancellation could also leave branch variables active during teardown. Writable branch/parent maps and one-time deferred restoration now preserve the parent on every exit. Independent HTTP counter tests cover break/continue, following loops, seeded variables and parent cleanup after interruption.
- Controller receipt/gap metadata could turn a legacy peer's absent telemetry into an apparent measured zero drop count. Cold assigned peers were also omitted from fleet qualification. Counter absence is now retained through live aggregation and fleet membership checks, generator JSONL/Parquet archives, archive merging, durable history and restart restoration. Snapshot overlays retain independently qualified counter evidence and the higher observed loss peak. Nullable schema release 26 preserves unknown availability in historical SQLite/PostgreSQL rows. Migration/restart tests distinguish legacy unknown, measured zero and observed positive loss. A final edge-case review also found that negative counters could qualify a synthetic zero or cancel valid positive loss during fleet merging. Qualification now excludes negative counters with an invalid-evidence warning; live/archive sums preserve valid positive evidence, reject invalid contributors and guard signed overflow. Boundary and mixed-validity tests cover these cases without mutating input records.
- Merged native rates could assign a whole long publication to a short bucket or count overlapping windows twice when establishing coverage. Rates now require per-source complete interval-union coverage and wholly contained, nonoverlapping windows. Integer nanosecond accumulation and rounded duration reconstruction avoid false missing coverage from floating-point conversions. Tests cover boundaries, overlaps, gaps, multiple sources, missing sources and exact fractional/nanosecond tiling.
- Real browser failed-request events lacked the failure classification used by the new filter. Observed HTTP errors now retain `http`, while failed requests without an HTTP error retain `transport`; no raw error text becomes a label. Archive disclosure accepts only fixed failure classes. Numeric HTTP status remains visible beside its classification. Real Chromium transport-close/HTTP failure tests and archive/report sanitization tests cover the producer and disclosure paths.
- Explicitly clearing a scenario in Schedules and selecting the same scenario again could leave the run name blank because the previous seed identity remained cached. Explicit selection changes now reset that identity while preserving edits during asynchronous metadata loading. Component and browser regressions cover clear/reselection and failure-resource inspection in both auth profiles.

Additional verification outcomes:

- The full stable-source `GOMAXPROCS=2 GOMEMLIMIT=2GiB go test -p 1 -timeout 30m ./...` passed after the replay, rate-coverage, persistence, browser and naming fixes, before the final negative/overflow counter guards. The subsequent focused API rate/availability/restore gate also passed before those guards. Final generator archive telemetry tests, including negative-counter availability, passed on the final source.
- Final API malformed-counter regressions and a refreshed API race gate remain unverified. The first retry reported a linker UUID-write failure from insufficient disk space. Two subsequent smaller-link attempts still ended with an API build failure before executing tests as free disk fell below 1 GiB. The successful earlier full Go gate is not represented as verification of these final guards. Independent final source reviews found no further actionable findings.
- Affected replay race tests and real Chromium failed-resource producer tests passed. SQLite schema/migration/history/restart coverage passed. The new PostgreSQL availability fixture compiled but skipped because `APEXLOAD_TEST_POSTGRES_URL` was unset; live PostgreSQL acceptance is not claimed.
- Dashboard typecheck, lint and 4,749 unit tests across 455 files passed. The complete local-auth browser profile passed 109 tests with one skip. An initial dev-admin archive download failed because the existing 1 GiB sanitized-artifact disk reserve was unavailable; the guard was preserved. After cleaning inactive task files, the corrected complete dev-admin profile passed 189 tests with four skips. Both profiles exercised scenario clear/reselection and failed browser resource evidence.
- API/DSL/TypeScript freshness and documentation checks passed; the final documentation check covers 95 active Markdown files. Completed browser output and inactive task build binaries were cleaned while retaining validation logs and the initial disk-failure trace. Shared Go cache cleanup was rejected by automatic approval review and specific user approval remains pending; the shared cache was not cleared.

The live Azure/broker acceptance limits and the waived 10 GB benchmark remain unchanged.

<a id="evidence-expansion-2026-10-02--subsequent-prerequisite-verification"></a>
### Subsequent prerequisite verification

The [feature completion R0 record](INTEGRATION_AND_CODEBASE_REVIEWS_2026-10.md#feature-completion-r0-2026-10-02) subsequently cleared the final malformed-counter API/race and actual PostgreSQL release-26 migration gaps above against immutable snapshot `025d5ed9`. Earlier disk failures and skipped PostgreSQL attempts remain historical outcomes. This does not claim acceptance of subsequent completion-program implementation or external provider/scale gates.

<!-- Original record: docs/qa/reviews/feature-completion-recovery-shutdown-2026-10-03/README.md; local evidence kept outside git -->
<a id="feature-completion-recovery-shutdown-2026-10-03"></a>

## Recovery shutdown review


Status: source review clean; affected normal/race and the disk-failure repeat pass. The subsequent Service Bus slice completed the full repository gate, as linked below. This is a shared lifecycle correction discovered during the Kafka repository gate, not a new provider qualification.

<a id="feature-completion-recovery-shutdown-2026-10-03--findings-and-changes"></a>
### Findings and changes

- Stopping an AfterFunc timer did not join a callback already executing durable credential recovery. Recovery scans and timer callbacks now share a mutex-fenced admission count and drain channel; Server.Shutdown waits before dependent teardown. Deadline expiry reports retained work and never fabricates cancellation of a noncooperative provider/store call.
- The execution-plan governance test admitted an actual long-running HTTP load engine and closed its database without joining it. It now uses an execution-context-owned backend, joins fixture work, and asserts zero requests on an independent local target. Shared timeslot fixtures drain the server before database closure.
- The first affected check exposed a terminal capacity fixture whose in-memory run remained RUNNING and unassigned after its durable record had completed in proj-a. Its runtime fixture now records the same completed state and project; terminal persistence and capacity-release assertions remain intact. The repeat passes.

- The full repeat also exposed three additional synthetic fixtures: dependency snapshots unnecessarily marked a backend-free run active; monitoring ingestion needed a terminal fixture cleanup; a two-sided capacity fixture omitted its project and retained a running memory state after terminal persistence. Their assertions remain unchanged.
- The explicit manual provider-recovery test raced the independently scheduled 25 ms automatic recovery coordinator. Its first Start now suppresses that coordinator for this manual-retry case; the separate no-second-request automatic recovery regression remains intact. Independent source re-review found no remaining issue in these four test-only corrections.

<a id="feature-completion-recovery-shutdown-2026-10-03--validation"></a>
### Validation

Affected normal repeat passed (6.918 s). The first failed check is retained alongside its corrected repeat and raw log hashes. Root and independent source reviews, including the terminal fixture correction, found no further issue. Affected race passed (75.671 s), including retained ownership at the shutdown deadline. The first full repository Go check completed with one API linker ENOSPC failure; all other packages passed. Its raw failure is retained. The full repeat used `-count=1 -ldflags="-s -w"` to avoid the oversized result cache and reduce linker scratch; this preserves all package/test assertions. Its API phase failed with 90 top-level cases: the four fixture issues described above and widespread temporary-directory/database/archive reserve failures when shared host disk space was exhausted. A later CloudTest linker also failed for insufficient disk. The command completed with exit 1; all remaining packages, including storage and the scripts, passed. The corrected affected normal suite passed (12.873 s), including all four corrected fixtures and the preserved automatic-recovery regression; the corrected race attempt failed at the external linker with ENOSPC before executing tests; the cached corrected repeat passed (79.991 s), including the four fixture corrections and preserved automatic recovery. The final full command completed with exit 1: its API phase took 631.755 s and had 17 failed top-level cases (23 failed subtests), all traced to exhausted temporary/database space or the unchanged 1 GiB archive reserve. Every other repository package passed. After reclaiming completed task-owned race outputs, all 17 failed API cases passed together at the normal assertions and reserve (21.642 s). No source change was needed for that repeat. This combined evidence does not represent a successful single full-suite command; that merge gate remains open for a host with sufficient sustained disk headroom. Initial documentation check passed (108 active Markdown documents). No dashboard or public schema changed.

Only the 10 GB import benchmark remains waived; this correction does not close pending provider, browser or native-platform acceptance. Eight superseded task-only API test archives, twelve superseded production objects and two old unused test-input result-cache logs were removed after identity and active-use checks, reclaiming 7,302,383,977 bytes (about 6.8 GiB). Two completed task-owned race outputs were then removed after exact header/build-ID/stat/open-owner checks and an independent Go action-key audit proved the active non-race graph could not use them; this reclaimed another 854,335,640 bytes (about 815 MiB), for 8,156,719,617 bytes total. No race consumer was pending; a later changed-source race rebuild remains intentional. The active repeat disables result caching; compiled entries still needed by current work, dependencies and shared caches were preserved. Final build-cache cleanup remains deferred while later program slices need the caches.

The subsequent [Service Bus slice](MESSAGING_2026-10.md#feature-completion-servicebus-management-2026-10-04) completed a clean full `go test ./...` gate on the combined source, including these shutdown corrections. Original disk-limited runs above remain retained as failures.

<!-- Original record: docs/qa/reviews/object-activity-2026-10-02/REVIEW.md; local evidence kept outside git -->
<a id="object-activity-2026-10-02"></a>

## Object activity and archive review — 2026-10-02


The object activity and archive implementation was reviewed, corrected, and
reviewed again. The final source review found no additional actionable issues.
Verification results are recorded below.

<a id="object-activity-2026-10-02--findings-corrected"></a>
### Findings corrected

- A delayed successful modification or archive event could clear the deleted
  marker. Only successful creation and deletion now establish object existence.
- Committed queued and accepted changes were missing from attribution summaries.
  Summaries now include committed asynchronous outcomes and normalize legacy
  project snapshots.
- Moving between objects or projects could retain the previous history page.
  History pagination now resets with its scope.
- Restoration could replay missed schedules when no scheduler observed the
  archive interval, including controller downtime. A durable restoration boundary
  now skips overdue scenario and execution-plan occurrences and advances to future
  scheduling. Reusing an object ID does not inherit another incarnation's boundary.
- Run starts could create duplicate history across request and background paths,
  or collide when an ID was reused. Canonical start identities now include the run
  incarnation and distinguish queue admission from execution admission.
- Parent-object history omitted schedule changes, monitor bindings, object
  repository bindings, and runtime settings. These changes now record the parent
  object and a safe section name. Unsaved import previews do not produce committed
  object history.
- PostgreSQL timestamp text changes with the session time zone. Comparing text
  could hide archive state and bypass edit or execution blocking. PostgreSQL now
  compares incarnation timestamps as instants; SQLite retains exact comparisons.
  A real-database regression reproduced the defect before the fix and verifies
  reads, lists, idempotence, admission, restoration, and schedule boundaries across
  three session time zones after the fix.

Regression tests cover each correction. The final review also checked disclosure
boundaries, project and resource permissions, archive concurrency, migration
compatibility, and continued operation of active runs.

<a id="object-activity-2026-10-02--verification"></a>
### Verification

- Focused API and SQLite activity/archive regressions: passed.
- PostgreSQL 16 activity/archive regressions, including time-zone transitions:
  passed; disposable database stopped after verification.
- Dashboard component regressions: passed.
- Full `go test ./... -timeout=20m`: passed with 23,217 test cases passing,
  17 skipped cases, and 117 packages passing (eight packages have no test files).
- Full dashboard `npm run test:cycle`: passed. Typecheck, lint, 4,697 unit tests,
  production build, 179 dev-admin workflows, and 100 local-auth workflows passed.
  Five dev-admin cases and one local-auth case were skipped for their configured
  auth profile or unavailable private fixture.
- Archive/restore, execution blocking, deleted history, audit warnings, and viewer
  permissions: passed in their applicable auth profiles.
- Rendered deleted-object Activity history: inspected in both auth profiles.
- Diff whitespace checks: passed.

The review/fix cycle is complete with no outstanding findings from the final
review or verification. Workflow test controllers and the disposable PostgreSQL
container were cleaned up.

<a id="object-activity-2026-10-02--integration-with-main-before-publication"></a>
### Integration with main before publication

The reviewed feature was integrated with main at `f03a09e6`. Existing migrations
through version 26 were preserved, and the activity and restoration migrations
were appended as versions 27 and 28. Main's newer run annotations now produce
parent-run modification history in both live requests and legacy audit backfill;
annotation deletion preserves the run's creator and existence history.

Verification of the integrated source passed:

- Full Go suite: 23,720 passing cases, 18 skipped cases, and 117 passing packages
  (six packages have no test files).
- Dashboard typecheck, lint, all 4,768 unit tests, and production build.
- Affected Playwright workflows: 16 passing dev-admin cases with three auth-profile
  skips, and 19 passing local-auth cases.
- PostgreSQL 16 archive/activity tests, including time-zone transitions.
- API permission contract matrix, OpenAPI, dashboard API types, and DSL schema
  freshness; annotation migration regressions and diff whitespace checks.

The integrated source review and verification have no outstanding findings.
Disposable browser controllers and the PostgreSQL container were cleaned up.

<!-- Original record: docs/qa/reviews/roadmap-evacuation-completion-2026-10-25/README.md; local evidence kept outside git -->
<a id="roadmap-evacuation-completion-2026-10-25"></a>

## Evacuation completion (REC-A)


Status: implemented. Qualified locally only, with fake and in-process generators. Three review rounds. Round 1 found no High, five Medium and several Low findings, and round 2 no High, two Medium and several Low; all are fixed. Round 3 found no High or Medium, so review stopped there; its Low findings are recorded, not fixed (see Review).

The [rollback](#roadmap-evacuation-rollback-2026-10-22), [roll-forward](#roadmap-evacuation-rollforward-2026-10-24), [generator terminal cause](#roadmap-generator-terminal-cause-2026-10-24) and [replacement restart](#roadmap-evacuation-replacement-restart-2026-10-24) records left three local gaps in REC-A. This slice closes them:

1. a run that finished while its generator still finalized the archive held every recovery, because the adoption answer could not say so;
2. live tables a lost lease term of the same process left in memory were never finalized durably, and their memory and budget stayed counted;
3. several REC-A exit cases were not exercised: a capability mismatch, no spare capacity, a stop during a roll-forward, a pause across a second leader change, and refusal by older controllers.

Deployed-cluster qualification is out of scope.

<a id="roadmap-evacuation-completion-2026-10-25--contract"></a>
### Contract

<a id="roadmap-evacuation-completion-2026-10-25--a-completion-still-finalizing-its-terminal-outcome"></a>
#### A completion still finalizing its terminal outcome

- **Wire (additive).** `RunAdoptionResult` gains `finalizing` (field 7, `bool`); the client maps it to `RunAdoptionResult.Finalizing`. A field was chosen over a new `RunAdoptionOutcome` value:
  - with a field, the outcome keeps the value it had before: `ALREADY_STOPPED` when the run's supervision tombstone exists (a supervised run that completed naturally, was stopped or failed leaves one in the tests), `UNKNOWN_TEST` otherwise. An older controller ignores the field and behaves exactly as before;
  - an older controller would treat an unknown outcome value as a refusal everywhere, including the restart's absence proof, which accepts `UNKNOWN_TEST`. A new value would therefore have changed its behaviour.
- **When a generator sets it** (`run_finalizing.go`). The adoption names a run with no assignment and no terminal tombstone yet, and the run's finalizing mark matches the adoption's prior generation and supervision token. Another lineage is told nothing.
- **The guarantee behind the flag: the assignment is gone and every execution ended.** Only `finishStoppedTestWithState` sets the mark, under `s.mu`. Each of its callers has already removed the assignment and seen every execution end:
  - `finishExecutionMatching`, for the last group: each group's callback runs after its engine's `RunAt` (or pooled owner) returned;
  - the completion timer of an assignment without executions;
  - `finishDetachedFencedTest`, after `waitForGeneratorExecutions` (controller stop, fence, lease expiry, failure, staged expiry).

  A drain that never ends escalates to self-termination and is never marked. The mark records the assignment's generation and token hash, and `markRunFinalizingLocked` refuses while an assignment of that generation is still in place. `completeStoppingTest` clears it after the terminal tombstone is published. Until then the stopping barrier keeps the test ID from being assigned again.
- **A narrow window stays conservative.** Between a caller releasing `s.mu` after removing the assignment and the mark, a claim reads the earlier answer and the run is held, as before this slice. A generator restart during finalization loses the mark, so a claim reads `UNKNOWN_TEST`, as before.
- **Test seam.** `WithArchiveFinalizationGate` calls a test's gate after the mark and before archive finalization; production generators leave it unset.
- **Controller wait** (`run_adoption_finalizing.go`). `claimRunAdoptions` runs `awaitFinalizingRunAdoptions` for each generator after its first claim:
  - while any answer is flagged, it claims again every 100 ms at the generation it already holds. Such a claim can only confirm (`adoptRunsLocked` with `rebind` false), and it carries only the finalizing runs' adoptions;
  - only flagged answers are replaced. A run the first claim refused or drained keeps that first answer, which is the one the recoveries judge;
  - a failed re-claim keeps the earlier answers and is tried again;
  - each re-claim's context ends with the wait, so a re-claim that hangs cannot hold the pass past it;
  - the wait first gives up the generator's claim slot, so no other generator's first claim, which carries its adoptions, queues behind it;
  - the wait is bounded by `defaultRunAdoptionFinalizingWait` (5 s; `distributedExecutionBackend.runAdoptionFinalizingWait` in tests) and by a third of what is left of the claim context (Phase A's 15 s budget and the lease), so the later steps that share that context (client retention, the roll-forward's commits, the restart's preparation and the capacity transfer) keep most of it. That is well inside the supervision TTL the first claim gave every adopted run (at least 30 s).

  Every caller gets the wait: Phase A (ordinary adoption, committed promotion, rollback, roll-forward, restart) and Phase B's stop proof (`confirmAdoptedRunStopped`). The interrupted-evacuation fence recovery claims no adoption, so it never waits.
- **The published outcome is judged by the existing paths.** A `DURATION_ELAPSED` completion at no less than the expectation is natural. A controller stop, an early end or a short duration is an unrecorded completion and holds the run. A `FAILED` run stays a problem, and so does `ALREADY_STOPPED` without a terminal state. Ordinary adoption takes the same wait, so a participant that finished during the gap is read as completed rather than failing the run with `ALREADY_STOPPED`; no test drives that path separately.
- **Still finalizing when the wait ends.** The answer keeps its earlier outcome and the flag, and the run is held as before. The restart's absence proof (`runEvacuationRestartAbsence`) refuses such an answer even when its outcome is `UNKNOWN_TEST`: the outcome the generator will publish is unknown.
- **Older peers and no new setting.** An older generator never sets the flag, so the controller makes no extra claim. An older controller ignores it. There is no setting and no journal change.

<a id="roadmap-evacuation-completion-2026-10-25--live-tables-of-a-lost-lease-term"></a>
#### Live tables of a lost lease term

- **Release** (`live_tables_lost_term.go`, `releaseLostTermLiveTables`). When this process acquires a new lease term, it removes from memory every run's live tables restored or prepared under another term:
  - when: in `reloadAndReconcile` right after the active reload, and again at the start of Phase A. That covers deployments without run adoption, and Phase A callers;
  - what: the tables are marked closed and leave `tr.liveTables`, so they no longer count against the run, project or controller budget, and a candidate's restore in Phase A never counts them. Their own budget field is left alone, so a restore in progress still deducts it from the recovery reservation;
  - kept: a summary already released after its finalization holds no budget and stays for the run view. The run view of a released run reads storage: `unavailable` for an active record, `closed` for a closed one.
- **No lost-term writer can write.** Its live clients, checkpoints and finalization check the table's term and refuse once the term ended. Storage requires the writer to hold the lease term (`beginLiveTableTx`). After the new term's restore, the owner of every table row is the new term (`ClaimLiveTable`).
- **Durable finalization under the new term** (`finishUnrestoredLiveTables`). A terminal writer (`finishLiveTables`) that finds no tables of its term in memory finalizes from storage, when all of these hold:
  - the controller runs with the durable execution fence;
  - the run has live data pools;
  - the writer holds the registered run object and the current term's fence. A lost-term writer, which holds the old object or the old fence, does nothing.

  It releases any ended term's tables still registered, then:
  - **checks storage** (`liveTablesAwaitingFinalization`): something is left if a durable table is still active, or closed without a finalization result, or a project-table attachment of the run is still active. Tables kept only in memory are skipped, not a reason to stop;
  - **restores** the tables under its own term for finalization only (`restoreRunLiveTables` with `finalizeOnly`): it claims each durable table and skips tables kept only in memory (the run view and the archive summary still list them, as unavailable, from their storage records); it does not require a table a binding names but storage never initialized, which a lease lost while the run launched leaves behind; it takes the run's active project-table attachments as they are (`collectUnrestoredProjectAttachments`) instead of re-attaching them; it registers the tables closed, so they never serve; and it finalizes nothing itself;
  - **registers** the restored tables only if its term is still this process's term, checked under `liveTablesMu`. A restore of either kind whose term ended during its reads registers nothing and fails, so it never replaces the tables a newer term restored; the attempt's retry then ends with the term;
  - **finalizes** them with the terminal status, which also detaches the project-table attachments. Attestation and fence recovery write `FAILED`, so a `discard` table is discarded and any other is recorded `pending` with `run_not_complete` for explicit recovery. A table the lost term had closed keeps its closed snapshot and is finalized the same way.

  Every failure, the restore's and the finalization's, returns to one retry under `liveTableRetryKey` with the current term; the retry ends with the term. A restore failure no retry can change (`liveTableRestoreUnrecoverable`: a stored table no binding names, or for an adoption restore an incomplete manifest, a run budget the bindings exceed, a binding mismatch, invalid metadata, corrupt recovery state, the recovery byte limit) ends the attempt without a retry and leaves the tables as storage has them; each attempt would otherwise claim and read the tables again every second for the whole term.
- **Tables kept only in memory.** A `memory` table exists only for a run that fails on controller loss. No other term can claim it, and storage holds no content for it. It is released, its record stays as the lost term left it, and nothing is finalized or retried for it; a durable table of the same run is finalized as above.
- **Project-table attachments** of a held run are detached when the run ends, even when the run has no run-scoped tables, so the project table can quiesce and be evicted. Independently, the project-table maintenance sweep detaches the attachments of any run that is no longer active.
- **A successor in another process** has no tables in memory. Its terminal write of a held run now takes the same path, which closes the earlier gap: live tables were never finalized durably for runs not adopted after a failover.

<a id="roadmap-evacuation-completion-2026-10-25--rec-a-exit-cases"></a>
#### REC-A exit cases

- **Capability mismatch.**
  - At the roll-forward: a replacement whose generator predates run adoption cannot be adopted, and its claim proves nothing about what it runs. The run is held, and with the restart on, its share is not restarted ("unreachable, which does not prove it stopped").
  - A replacement without the duration control (`AdjustDuration` unimplemented) cannot be shortened to the commit target. The run is held and its capacity is not committed.
  - At the restart, from a `source_fenced` journal or the roll-forward fallback: a spare without `evacuation_assignment_state_v1` is never given the share. The run is held with "no healthy generator with sufficient capacity".
  - An adopted replacement is not checked again for protocol capability: it already runs the share.
- **No spare capacity.** A roll-forward needs none: the replacement's reservation is journaled, and a `replacement_committing` journal rolls forward with no spare generator in the fleet. A replacement not adopted at `replacement_committing` or `rolling_forward` (from an earlier successor's stored intent) falls back to a restart. With no spare generator, or a spare without room, the restart holds the run under its intent and reserves nothing.
- **A stop during a roll-forward.** Phase A runs inside acquisition reconcile, before admission, so an operator's stop is refused as not admitted (`errNotExecutionLeader`) and the roll-forward completes. Retried after admission, before Phase B, the stop makes the installed run `STOPPING` and reaches the survivor and the replacement at the new generation with the rotated token. The test constructs the admission state itself (`reconciling`), because it calls Phase A directly rather than through the lease's acquisition callback. A stop made durable before the failover leaves the run `STOPPING`, which is never eligible (the roll-forward record's fail-closed test).
- **A pause across a second leader change.** A paused run's roll-forward begun by a successor that was lost, after its intent or after its claim, is rolled forward by the next term from the stored `rolling_forward` intent. The run stays `PAUSED`. A durable pause state that no longer matches the staged one is held. The fake survivor and replacement check proof chains as generators do, so after the first successor's claim only an adoption naming that successor's generation and token is accepted.
- **Rolling-version refusal, simulated.** `runEvacuationReadableVersion()` (6 unless a test sets the atomic `runEvacuationReadableOverride`) gates the three journal version checks. Lowering it reads a journal with every version check of an older release; the rest of that release's code is not reproduced. A version 4, 5 or 6 journal read by every older reader (versions 3 to 5) is:
  - not an adoption candidate and not an interrupted evacuation;
  - never promoted;
  - held `STOPPING` with the interrupted distributed stop reason, its journal unchanged and no generator claimed.

  Read by its own release, each is recognized.
- **No defect found.** These cases needed fake-generator knobs (`noAdoptionSupport`, `adjustErr`, graceful stops) and a heartbeat feature filter for the in-process spare. The only production change they needed is the `runEvacuationReadableVersion` seam, which leaves behaviour unchanged at its default. Round 1 found that the first version of the stop test passed for the wrong reason: Phase B failed the run on the fakes' missing metrics streams before the stop, and the stops it checked were Phase B's. The test now stops the run Phase A installed and asserts `STOPPING`.

<a id="roadmap-evacuation-completion-2026-10-25--invariants"></a>
### Invariants

- **A finalizing answer is never taken as an outcome.** It only starts a bounded wait. When the wait ends, the run is judged on the published outcome, or held.
- **A finalizing answer is truthful.** It is given only when the generator removed the assignment and every execution ended, for that exact lineage.
- **Nothing changes for older peers.** The outcome field carries the same value as before.
- **A run's live tables are never written by two terms.** Only the current term's registered writer restores or finalizes them, after claiming them in storage, and the lost term is refused in process and by storage.
- **Released live tables keep their durable state.** Their journal and checkpoint stay exactly as the lost term left them, until the new term finalizes them.
- **Strict-fail defaults.** No setting was added, and every refusal holds the run as before.

<a id="roadmap-evacuation-completion-2026-10-25--validation"></a>
### Validation

- **`TestAdoptionReportsARunStillFinalizing`** (`pkg/distributed/generatorrpc`, real executing runs; the archive finalizer slots are held). Each case is supervised, so its earlier outcome is pinned to `ALREADY_STOPPED`:
  - a run whose duration elapsed, one a controller stopped and a failed one each reach the window with no assignment and no execution left;
  - a successor's claim, and a claim of the same generation, read the flag with the earlier outcome `ALREADY_STOPPED`, no terminal state and no cause;
  - another supervision token reads no flag, and a chained proof (the lineage only in an older proof) reads it;
  - decoded with a `RunAdoptionResult` schema built without field 7 (`protodesc`, `dynamicpb`), as an older controller reads it, the answer has the same outcome and keeps the flag as an unknown field;
  - after the release, the mark clears and a claim reads the published outcome, cause and duration.
- **`TestDrainingStopIsNotReportedFinalizing`**: a graceful stop removes the assignment while the VUser's request is still in flight at a held target. A claim made then reads no flag, and the test checks the request had not ended. Once the request ends, the run is finalizing, and then terminal with `CONTROLLER_STOP`.
- **`TestRunFinalizingMarkRequiresTheExactLineage`**: no mark while the assignment of that generation is in place; another generation or token is not finalizing; only its own generation clears it; a run without a supervision token is never finalizing.
- **`TestEvacuationRecoveryWaitsForAFinalizingCompletion`** (`pkg/api`, fake generators):
  - a roll-forward replacement and a survivor that finished naturally, and a rollback source that answered `UNKNOWN_TEST` while finalizing, are each asked again (same generation, only that run's adoption) and rolled forward or back with the participant completed. The swapped checkpoint records it; the survivor adopted by the first claim is not asked again;
  - a replacement whose published outcome is a controller stop is held as stopped by a controller;
  - a replacement or rollback source still finalizing when a 200 to 300 ms wait ends is held with the finalizing detail, after re-queries, within the bound;
  - every claim, first and re-query, is at Phase A's generation (the fake records each claim's generation);
  - an older generator that answers `ALREADY_STOPPED` without the flag is asked once and held.
- **`TestEvacuationRestartRefusesAStillFinalizingReplacement`** and **`TestRunEvacuationRestartAbsenceRefusesFinalizing`**: a replacement whose `UNKNOWN_TEST` answer is still flagged after the wait is not restarted, and the run is held with the reason. Without the flag, `UNKNOWN_TEST` is still absence proof.
- **`TestAwaitFinalizingRunAdoptionsReplacesOnlyFinalizingAnswers`**: two runs on one generator, one finalizing and one `REJECTED`. Re-queries carry only the finalizing run, its published completion replaces its answer, and the refused run keeps its first answer, although a later claim would read it differently.
- **`TestFinalizingWaitDoesNotHoldAClaimSlot`**: seventeen generators, one more than the claim slots, all still finalizing. Each re-query blocks until every generator's first claim arrived; none waits in vain, which is only possible if a waiting generator gave up its slot.
- **`TestFailedRequeryIsTriedAgain`**: the second claim fails; the third, at the same generation, publishes the completion, which replaces the flagged answer.
- **`TestFinalizingWaitLeavesTheClaimContextMostOfItsTime`**: with a 10 s wait configured and 1.5 s left in the claim context, the wait ends within a third of it.
- **`TestFinalizingWaitBound`**: `finalizingWait` is 5 s by default, the configured value without a deadline, and between 400 and 500 ms with 1.5 s left. The "still finalizing" cases also bound the elapsed time (2 s) and the number of claims (8).
- **`TestHungRequeryEndsWithTheWait`**: a re-query that hangs until its context ends, with a 10 s backend timeout, holds the pass less than 1 s past a 300 ms wait.
- **`TestEvacuationRollForwardWaitsForARealFinalizingReplacement`** (in-process replacement, 500 ms start barrier, 10 s RPC timeout; the generator's archive finalization gate holds the window). A committed replacement whose staged duration ran out has no active VUser and answers the successor's first claim `ALREADY_STOPPED` with the flag:
  - **published within the wait**: released after that answer, the re-query reads `ALREADY_COMPLETED`, `DURATION_ELAPSED`, at the commit target, and the run rolls forward with the replacement completed;
  - **not published within a 500 ms wait**: the run is held with the finalizing detail;
  - in both, every claim was at Phase A's generation, and the generator's epoch is still that generation afterwards.
- **`TestLostTermLiveTablesOfAHeldRun`** (attestation and fence recovery). A durable live table is prepared and served by the lost term; its run has a version 4 `source_fenced` journal and is fenced to the lost owner. Then, through `reloadAndReconcile`:
  - the run is held `STOPPING` on a new object;
  - the lost term's tables leave memory, the budget in use drops to zero, the lost object is closed, and its client refuses;
  - storage is unchanged: active, owned by the lost term, with its revision, and not finalized;
  - a lost-term writer holding the old object leaves the run's row `STOPPING` and the table's record unchanged, and leaves no retry; so does that unregistered object when it carries the current term's fence, and the registered object while it carries the lost term's fence;
  - the attestation, or the newer term's fence recovery, leaves the table closed and owned by the new term, with a `discard` finalization, and no retry.
- **`TestLostTermLiveTablesClosedBeforeTheirFinalization`**: the lost term closed its table, checkpoint and record included, and was lost before the finalization. The new term restores and finalizes it, and leaves no retry.
- **`TestUnrestoredLiveTableFinalizationFailureInsideTheRetry`**: the same closed table; the first attempt fails before the restore, and on the retry the finalization fails once. The one retry goes on and ends with the table finalized under the new term.
- **`TestLostTermDurableTableBesideAMemoryTable`**: a fail-policy run with a durable and a memory table. The durable one is finalized under the new term; the memory one stays active under the lost owner.
- **`TestLostTermProjectTableAttachmentIsDetached`**: a held run bound only to a project table. After the attestation its attachment is detached and settled.
- **`TestUnrecoverableUnrestoredLiveTablesAreNotRetried`**: two stored tables and a run that names only one; the attestation leaves no retry and both tables as storage has them.
- **`TestUnrestoredLiveTablesWithAnIncompleteManifest`**: a binding whose table storage never initialized; the table that exists is finalized under the new term.
- **`TestUnrestoredLiveTableRestoreOfAnEndedTermIsNotRegistered`**: a test barrier after the restore's reads makes the same leadership object lose the lease and win it again, and registers the newer term's tables. The ended term's restore fails as expired, the newer tables stay registered, the budget counts only theirs, and the ended term's next attempt gives up.
- **`TestFinalizeOnlyRestoreNeverServes`**: tables restored only to be finalized refuse a client request before any finalization.
- **`TestLostTermDurableTableBesideAMemoryTable`** also reads the run view and the archive summary after the attestation: the durable table is closed and the memory table is listed as unavailable.
- **`TestLostTermLiveTablesAlreadyFinalized`**: the lost term finalized its table completely. The attestation neither restores nor claims it again.
- **`TestUnrestoredLiveTableRetryEndsWithItsTerm`** (on a store that embeds the SQLite store, so it recovers for real; it waits for the asynchronous loss before the new term, and passed in 20 of 20 fresh processes): a finalization that fails while storage cannot list the tables is retried. The same leadership object then loses the lease and wins it again under a newer term, and storage recovers. The retry ends, an attempt of the ended term gives up without touching storage, and nothing is claimed or finalized. (A first version swapped the runner's leadership object, which the race detector flagged against the running retry; production never swaps it.)
- **`TestLostTermLiveTablesOfAnUndecodableCheckpoint`**: the predecessor's pinned-secret ciphertext is unusable, so Phase A holds the run before any restore and claims no generator. The tables are released, and the attestation finalizes them under the new term.
- **`TestLostTermLiveTablesOfAnAdoptedRun`**: a roll-forward candidate's tables are restored under the new term. The lost term's object is gone, and the budget in use is unchanged, not doubled.
- **`TestLostTermMemoryLiveTablesAreOnlyReleased`**: a fail-policy run's memory table, through `reloadAndReconcile` with run adoption unavailable, is released. After the attestation its record stays active under the lost owner with no finalization, and nothing is retried.
- **`TestReleaseLostTermLiveTablesKeepsTheCurrentTerm`**: only the other term's live tables go; the current term's tables and a released summary stay, and the budget counts the current term's.
- **`TestEvacuationExitCapabilityMismatch`**: a roll-forward replacement that predates run adoption (also with the restart on), one without the duration control, and a spare without the evacuation assignment state at the restart and at the roll-forward fallback. Each holds the run with its reason; nothing is assigned to the incapable spare, and no capacity is committed for the unshortened replacement.
- **`TestEvacuationExitNoSpareCapacity`**: a `replacement_committing` roll-forward with no spare generator completes. At `replacement_committing`, and at a stored `rolling_forward` intent taken over by another controller, a replacement that is not adopted, with no spare generator or a spare without room, is held under the restart intent with the reason, and the spare gets no assignment or reservation.
- **`TestStopRequestedWhileTheRollForwardRuns`**: as described in the Contract.
- **`TestPausedRollForwardAcrossASecondLeaderChange`**: after the intent or after the claim, the second successor rolls the durably paused run forward with the first successor's token proof, and the run stays `PAUSED`. A durable resume since staging is no candidate and is held, with its journal unchanged.
- **`TestOlderControllersHoldNewerJournals`**: version 4 `staged`, version 5 `replacement_committing` and a version 6 restart intent, read by every older reader and by their own release, as described in the Contract.
- **`TestEachJournalVersionCheckRefusesANewerVersion`**: each of the three version checks, on its own, accepts its versions at this release and at their own reader, and refuses them one reader older.
- **Existing suites**: the rollback, roll-forward, restart, terminal cause, adoption, continuity and live-table tests pass. The roll-forward fixture's assertion gained a status parameter (`assertRolledForwardWithStatus`).
- **Mutation pass** (`go test -overlay`): see Mutations.
- **Gates**:
  - `go build ./...`, `gofmt` and `golangci-lint run ./pkg/api/... ./pkg/distributed/...` (0 issues);
  - `go generate ./...` with no drift, and the proto regenerated with protoc 29.3 and protoc-gen-go 1.36.11, identical on a second run;
  - `go test ./pkg/distributed/...`;
  - the full `pkg/api` suite with a 45 m timeout, in twelve shards of one test binary: only the six `TestReplayArtifact*` tests already known to fail on macOS fail;
  - the new tests under `-race -count=2 -shuffle=on`;
  - `go test ./pkg/storage -run LiveTable`, and with `-tags postgres_integration` against the gate PostgreSQL container when it is reachable;
  - `python3 scripts/check-docs.py`.

  `pkg/engine` is untouched and was not run.

<a id="roadmap-evacuation-completion-2026-10-25--mutations"></a>
#### Mutations

Applied with `go test -overlay` against the tests above. A mutant that failed to build was rewritten. Before review: 32 caught, 3 survived (below).

- **Caught, generator:**
  - the mark never matching; the token check dropped; the generation check dropped;
  - the mark set while the assignment of that generation is in place;
  - the mark never cleared;
  - the mark set at a stop's detach, while executions still drain;
  - `finishStoppedTestWithState` not marking;
  - the finalizing answer changing its outcome;
  - the flag never set;
  - the client dropping the flag (by the in-process roll-forward test).
- **Caught, controller wait:**
  - no wait at all;
  - re-queries carrying every adoption rather than only the finalizing ones;
  - an unbounded wait;
  - published answers not merged;
  - the wait keeping its claim slot;
  - the restart taking a still-finalizing `UNKNOWN_TEST` as absence proof.
- **Caught, live tables:**
  - no release; a release of the current term's tables; a release of finalized summaries;
  - the writer check dropped (a lost-term writer restored and finalized the tables);
  - no unrestored finalization at all;
  - a table kept only in memory treated as finalizable, and finalized tables treated as pending (after `TestLostTermLiveTablesAlreadyFinalized` was added);
  - the release dropped from Phase A, and from the acquisition reload;
  - the closing tables a restore finalized retried again (after `TestLostTermLiveTablesClosedBeforeTheirFinalization` checked for no retry);
  - a retry that goes on after its term ended (after `TestUnrestoredLiveTableRetryEndsWithItsTerm` was added).
- **Caught, exit cases:**
  - the version gate dropped from the three journal version checks;
  - the reshard placing a share on a generator without the evacuation assignment state;
  - the adoption-support check dropped;
  - the roll-forward's pause-state check dropped;
  - both admission checks of the stop path dropped.
- **Survived, and the code was simplified instead.**
  - The mark in `finishExecutionMatching`'s locked section, and in the timer path: `finishStoppedTestWithState` marks the same run moments later, so only the narrow window described under Remaining told them apart. Both were removed.
  - A finalizing clause in `selectAdoptionPriorLocked`: a chained proof already finds the lineage through the supervision tombstone. It was removed.
- **Survived and kept.** Dropping only the first of the stop path's two admission checks is equivalent, because the second check refuses the same stop.
- **Overstated before review.** The version-gate mutant dropped the gate from all three checks at once; only `supportedRunEvacuationCheckpointVersion` did any work in those tests. `TestEachJournalVersionCheckRefusesANewerVersion` now pins each check.

After review round 1, 24 mutants, all caught:
- **new:**
  - re-queries at generation + 1;
  - a failed re-query ending the wait;
  - no cap on the wait;
  - a failed finalization after the restore reported as success;
  - a memory table stopping the precheck, and the finalization-only restore not skipping memory tables;
  - the precheck ignoring active project-table attachments, and no attachments collected;
  - an unrecoverable restore retried;
  - the writer check without its fence half;
  - the adoption intent without the earlier successors' proofs;
  - the gate dropped from each of the three version checks on its own;
- **re-run on the reworked code:** every live-table release, writer, unrestored-finalization, precheck and ended-term mutant above, and the wait keeping its claim slot.

After review round 2, 12 mutants, all caught (the wrong-token mutant was also run against "after intent" alone, where it survives, as intended):
- registration without the term check;
- finalize-only tables registered open (after `TestFinalizeOnlyRestoreNeverServes` was added);
- an incomplete manifest refused in finalize-only mode, and any manifest accepted;
- memory tables left out of the archive summary, and out of the run view;
- re-queries on the claim context instead of the wait's;
- a cap of a half instead of a third, and the configured wait ignored;
- the writer check without its registered-object half;
- a memory table counted as pending (by `TestLostTermMemoryLiveTablesAreOnlyReleased`);
- an adoption whose proof of the first successor carries the wrong token: caught only by the "after claim" case of `TestPausedRollForwardAcrossASecondLeaderChange`, where the fake holds the run under that successor's token, and not by "after intent". This is the evidence that the EX-M2 fix gives "after claim" its own meaning.

<a id="roadmap-evacuation-completion-2026-10-25--review"></a>
### Review

Round 1 found no High issue, five Medium and fourteen distinct Low (fifteen listed; tests-L3 repeats FIN-L1). All are fixed:
- **LT-M1, a failed finalization inside a retry was lost** (reproduced by the reviewer). The restore's own recovery finalization queued its retry under the key the outer retry already held, so it was refused, and the outer attempt returned success. Fixed: the unrestored path restores for finalization only, finalizes nothing inside the restore, and returns every failure to its one retry. `TestUnrestoredLiveTableFinalizationFailureInsideTheRetry`.
- **LT-M2, a memory table kept its durable sibling from being finalized** (reproduced). Fixed: the precheck and the finalization-only restore skip memory tables. `TestLostTermDurableTableBesideAMemoryTable`.
- **LT-M3, project-table attachments of a run the new term never restored were not detached** at the run's end. Fixed: active attachments count as pending, and the finalization-only restore collects and detaches them, with no run tables at all. `TestLostTermProjectTableAttachmentIsDetached`. The maintenance sweep, which detaches attachments of runs no longer active, was a backstop already.
- **FIN-M1, re-queries were not checked for their generation.** Fixed: both fakes record each claim's generation, the tests require Phase A's, and the in-process test checks the generator's epoch afterwards.
- **EX-M2, the "after claim" case added nothing** because the fake checked no proof chain. Fixed: the fake can track the lineage it holds the run under and accept only adoptions whose proofs name it.
- **Low:**
  - **FIN-L1, a failed re-claim was untested:** `TestFailedRequeryIsTriedAgain`.
  - **FIN-L2, the wait could starve later Phase A steps:** capped at a third of what is left of the claim context; `TestFinalizingWaitLeavesTheClaimContextMostOfItsTime`.
  - **LT-L1, unrecoverable restores retried every second:** they now end the attempt; `TestUnrecoverableUnrestoredLiveTablesAreNotRetried`.
  - **LT-L2, the failing store could not recover:** it embeds the SQLite store.
  - **Tests-L1, the earlier outcome was not pinned:** pinned to `ALREADY_STOPPED`.
  - **Tests-L2, the older-controller decode used the new schema:** it decodes with a schema built without field 7.
  - **Tests-L3, a failed re-claim:** same as FIN-L1.
  - **Tests-L4, the writer check's fence half was untested:** a case with the registered object carrying the lost term's fence.
  - **Tests-L5, the budget zeroing:** removed. The remaining defensive branches are commented as defence in depth; the "restored by an earlier attempt" branch is exercised by `TestUnrestoredLiveTableFinalizationFailureInsideTheRetry`.
  - **Tests-L6, the rollback still-finalizing case:** asserts the re-queries, their generation and the time bound.
  - **Tests-L7, the slot test's timing margin:** replaced with a structural check.
  - **Tests-L8, the stop test:** asserts `STOPPING` exactly, and states that it constructs the admission state. It also fixed the test, which had passed on Phase B's stops (see Contract).
  - **Tests-L9, the version seam:** an atomic override behind `runEvacuationReadableVersion()`.
  - **Tests-L10, the version mutation claim:** corrected, and each check pinned.
  - **Tests-L11, record wording:** the roll-forward and restart records' Remaining, the spec, the plan and this record's lost-writer and mutation-count claims are corrected.

Round 2 found no High issue, two Medium and eight Low. All are fixed:
- **CS-M1, a finalize-only restore whose term ended mid-restore registered its tables over a newer term's** (reproduced). Fixed: a restore registers only while its term is current, checked under `liveTablesMu`, and finalize-only tables are registered closed. `TestUnrestoredLiveTableRestoreOfAnEndedTermIsNotRegistered`, `TestFinalizeOnlyRestoreNeverServes`.
- **CS-M2, `TestUnrestoredLiveTableRetryEndsWithItsTerm` flaked** (5 of 20 processes): the loss is handled asynchronously, so the term read empty right after `tick()`. Fixed: a shared `loseAndWinAgain` helper ticks until the newer term is visible; 20 of 20 fresh processes pass.
- **Low:**
  - **CS-L3, the run view hid a finalize-only run's memory table:** skipped records are kept on the run and listed as unavailable in the view and the archive summary.
  - **CS-L4, a hung re-query was not bounded by the wait:** each re-query's context ends with the wait; `TestHungRequeryEndsWithTheWait`.
  - **CS-L5, a data race on `releasedAt`:** read under `liveTablesMu` (`liveTablesReleased`).
  - **Design note, incomplete manifests:** finalize-only mode finalizes the tables that exist; `TestUnrestoredLiveTablesWithAnIncompleteManifest`.
  - **TD-L1, the wait bound was not pinned:** `TestFinalizingWaitBound`, and elapsed and claim-count bounds in the "still finalizing" cases.
  - **TD-L2, the registered-object half of the writer check was untested:** a case with the unregistered lost object carrying the current fence.
  - **TD-L3, stale comments:** the closed-table test now says the terminal writer finalizes.
  - **TD-L4, Validation overclaims:** the `UNKNOWN_TEST` alternative is dropped, and the EX-M2 evidence is the wrong-token mutant above.
  - **TD-L5, the memory-table test missed a mutant:** it asserts nothing is restored after the attestation.

Round 3 found no High or Medium, so the review stopped after it. Its code reviewer confirmed the round 2 fixes: the term check under `liveTablesMu` takes only the lease's own locks, which are never held while `liveTablesMu` or `tr.mu` is taken, so it cannot deadlock; finalize-only tables registered closed are finalized normally and refuse clients; a partial manifest finalizes only the run's own tables through the lease-checked claim. It ran the related `pkg/api` and `generatorrpc` tests, the new tests under `-race -count=2 -shuffle=on`, and the storage live-table tests on SQLite and PostgreSQL. Its tests reviewer caught all twelve round 2 mutants and ran both term-change tests 25 of 25 times one at a time and 48 of 48 with twelve processes at once; the still-finalizing cases stayed far inside their bounds under `-race` (316–335 ms against 2 s, two or three claims against eight). Their Low findings are recorded, not fixed:
- **A skipped memory table is always shown `unavailable`.** The run view (`handlers_live_tables.go`) and the archive summary (`live_tables_archive.go`) list a finalize-only run's skipped memory records as `unavailable` with no finalization, even when storage holds them `closed` with a finalization, which the storage-backed view would show. The frozen archive and report keep the `unavailable` state. Fix: take the state from the record and read its finalization during the finalize-only restore.
- **The lost-term release trusts the term it is given.** `releaseLostTermLiveTables` does not re-check that the caller's term is still current under `liveTablesMu`; a full lose-and-win cycle between `finishUnrestoredLiveTables` capturing the term and the call would drop the current term's tables. Reaching it needs that cycle inside one lock wait with the object and fence checks lining up. Fix: check `executionLeaseTermLocallyCurrent(term)` inside the function.
- **`TestFinalizingWaitBound` compares with the default constant**, so a changed default (for example 30 s) passes; the "5 s by default" claim above is not pinned.
- **The registration term check is tested for the finalize-only restore only.** Limiting it to finalize-only survives; an ended adoption restore registering is untested, though the claim above covers both kinds.
- **The ended-term test's `"owner expired"` match also matches the finalization error** the no-check mutant returns; the mutant is still caught by the next assertion.
- **Record wording.** The 10 s backend timeout in the hung re-query bullet does not matter, because each re-claim is capped at 2 s by `maxGeneratorEpochKeeperTransit`; the test catches the mutant because 2 s exceeds its 1 s bound. The `TestLostTermDurableTableBesideAMemoryTable` bullet appears twice in Validation. An unnamed stored table is detected only by count, so in finalize-only mode one binding without a stored table and one stored table no binding names balance out and the unnamed table would be finalized. The comment "at most four requeries" means four claims.

<a id="roadmap-evacuation-completion-2026-10-25--remaining"></a>
### Remaining

- **Review.** Three rounds ran; round 3's Low findings are recorded under Review.
- **Deployed qualification.** REC-A's exit still requires failure injection on a deployed cluster at every journal boundary, worker or node partition, late old-owner return and independent target counters. These tests use fake and in-process generators.
- **Finalization longer than the wait.** An archive that takes more than 5 s to finalize, or a generator that restarts while finalizing, still holds the run, as before.
- **The mark's window.** A claim between a caller releasing `s.mu` and the mark still reads the earlier answer and holds the run.
- **Unrestored live tables that cannot be restored.** A restore that fails for a reason no retry can change (a stored table no binding names, corrupt state, a binding mismatch, the recovery byte limit) ends without a retry, and its tables stay as storage has them until an operator acts. Failures that may pass (the controller or project budget, a key service, storage) are retried with backoff until the term ends, as other live-table finalizations are. Tables kept only in memory are released but never finalized; their records stay active under the lost owner.
- **The wait shares Phase A's context.** It takes at most a third of what is left; many generators finalizing at once still each take their share in parallel, so a slow fleet can leave less time for the later steps than before this slice.
- **A stop between Phase A and the restart on a run with no survivor.** Unchanged: it finds no generator to stop, so the run stays `STOPPING` until a newer term fences it or an administrator attests, although this term proved nothing runs it ([restart record](#roadmap-evacuation-replacement-restart-2026-10-24)). Finishing it from that proof needs the intent replaced and the share's reservation released by the stop path, and is not implemented.
- **Older-reader simulation.** The rolling-version tests lower the version checks only. The rest of an older release's code is not reproduced, and a real older binary is not run.
- **Worker or node partition, and live tables at a restart.** Neither is exercised. A restart of a run with live tables restores them as a candidate does, but no test drives it.

<!-- Original record: docs/qa/reviews/roadmap-evacuation-replacement-restart-2026-10-24/README.md; local evidence kept outside git -->
<a id="roadmap-evacuation-replacement-restart-2026-10-24"></a>

## Evacuation replacement restart (REC-A)


Status: implemented behind an opt-in. Qualified locally only, with fake and in-process generators. Three review rounds. Round 1 found no High issue; its seven Medium and eleven Low findings are fixed or recorded below, as are round 2's one Medium and ten Low findings. Round 3 found no High or Medium, so review stopped there; its six Low findings were then fixed, and a review of those fixes found no High or Medium and four Low findings, recorded under Review.

The [rollback](#roadmap-evacuation-rollback-2026-10-22) and [roll-forward](#roadmap-evacuation-rollforward-2026-10-24) records left one REC-A case open: the missing replacement. The source of an evacuation was fenced, its replacement never committed or no longer runs the moved groups, and that share of the load runs nowhere. A successor held such a run `STOPPING` for attestation. This slice lets it restart the share on a new replacement instead, for the eligible stateless-HTTP workload, once it has proven that neither the source nor the old replacement can run it.

<a id="roadmap-evacuation-replacement-restart-2026-10-24--contract"></a>
### Contract

- **Opt-in.**
  - `APEXLOAD_EVACUATION_REPLACEMENT_RESTART` (`WithEvacuationReplacementRestart`) is off by default.
  - It takes effect only together with `APEXLOAD_EVACUATION_ROLLBACK_RECOVERY`, `APEXLOAD_EVACUATION_ROLLFORWARD_RECOVERY` and run adoption.
  - The evacuating leader still writes version 5 journals. Only a successor writes version 6, a superset of version 5:
    - **the restart intent**, phase `replacement_restarting`. It records `RestartGeneration`, `RestartOwnerID`, `RestartStartedAt`, the planned adoption checkpoint (`RestartCheckpoint`) and `RestartTargetDuration`, the logical duration the moved share may run to at most. `Prior` is unchanged;
    - **the restart's own journal**: an ordinary evacuation journal (`prepared` → `staged` → `source_fenced` → `replacement_committing` → `replacement_committed`) that moves the share from the old replacement to a new one under the successor's generation. Its prior is the successor runtime's checkpoint, its `SourceAbsentAt` marks a source proven absent rather than stopped, and it carries `RestartTargetDuration`.
  - Both carry `RestartExcluded`, the targets a restart must never give the share: the run's archive targets and every earlier source (`runEvacuationRestartExcluded`). It is durable, so a fenced source whose archive client a later term could not reach stays excluded.
  - Restart fields belong to version 6 only, and the intent's fields to `replacement_restarting` only (`interruptedEvacuationTargets`).
- **Older peers.**
  - A controller that knows only versions 3 to 5 cannot read a version 6 journal: `decodeRunContinuityCheckpoint` rejects it and the older evacuation reader does not recognize version 6. It keeps the run `STOPPING` through its interrupted distributed stop path.
  - A controller of this release with the setting off holds every version 6 journal fail-closed: the intent, and the restart's own journal in every phase but `replacement_committed`, which is promoted as for versions 3 to 5. A newer term then fences the participants and records the run `FAILED`.
  - Enable the setting only once every replica reads version 6.
- **Eligibility** (`evacuationRestartGuard`, `runEvacuationRestartPlan`). A run qualifies only when:
  - all three settings are on and run adoption is enabled;
  - the run is `RUNNING` or `PAUSED`, with the continue failover policy, on the distributed backend;
  - the journal passes `interruptedEvacuationTargets` and is one of:
    - a version 5 or 6 journal held at `source_fenced`: its leader writes `replacement_committing` before `CommitWork`, so the replacement never committed;
    - a version 6 restart intent;
    - a restart's own journal at `prepared` or `staged`: its new replacement never committed;
  - or, after the epoch claim, a version 5 or 6 `replacement_committing` or `rolling_forward` journal whose replacement was not adopted (the **roll-forward fallback**, below);
  - the journal is not abandoned, its predecessor lineage was armed, and its source is not a prior completed target;
  - the evacuation has exactly one replacement target, no surviving participant's shards changed, and the capacity journal is valid;
  - the moved share has a journaled logical end: the shortest of `RestartTargetDuration`, `CommitTargetDuration` and the staged offset plus staged remaining duration;
  - the run records its start and its duration;
  - the moved groups pass `runEvacuationRollForwardWorkloadEligible`: no iteration termination, no distributed rendezvous, `groupAllowsGeneratorLossReshard` and `groupAllowsStatelessDataReshard`. These are the generator-loss reshard's rules, so browser, gRPC streaming, WebSocket, Java, SAP GUI, Event Hubs, JMeter-messaging and stateful-data groups stay out.

  A restartable journal that is refused is reconciled `STOPPING` as before, and the held reason adds `the moved share was not restarted: <reason>`.
- **Phase A** (`adoptOrphanedRuns`), in order:
  1. The adoption checkpoint is the next topology, with the missing share still on the old replacement, under the predecessor's lineage (`runEvacuationRestartCheckpoint`). The old replacement is added to its archive targets only when `CommitWork` may have reached it (`CommittingAt`). A restart intent supplies the checkpoint with the previous successor's intent.
  2. `persistRunEvacuationRestartIntent` writes the version 6 intent through the lease-held compare-and-swap before any generator sees the rotated token. A failed write returns an error, so Phase A is retried.
  3. The batched epoch claim adopts the survivors and carries the adoption to the old replacement. A restart journal's source that was never fenced is claimed too, without the run. The fenced original source is never claimed.
  4. `prepareRunEvacuationRestart` requires proof that the old replacement runs nothing of the run: its generator acknowledged the claim and answered `REJECTED` (the claim drained any staged assignment), `UNKNOWN_TEST`, `ALREADY_COMPLETED`, or `ALREADY_STOPPED` with a terminal state. An unreachable generator, a missing answer, `ALREADY_STOPPED` without a terminal state (the run may still be draining) and `ADOPTED` (it still runs) are refusals. No survivor may report a completion the prior lineage never recorded (`unexpectedRunEvacuationCompletions`, shared with both recoveries). A restart journal's unfenced source must have acknowledged its claim.
  5. The moved share's journaled reservation is confirmed by the old replacement at the new epoch and committed (`commitRunEvacuationRollForwardCapacity`), then `adoptPodCapacity` transfers it with the survivors' and standbys'. It is reused as the share's source peak for the new placement; a reservation that cannot be confirmed holds the run.
  6. The survivors are installed under the kept intent; the run is not re-armed. The old replacement takes no part; the intent's `RestartExcluded` and the checkpoint's archive targets are excluded from placement.
- **Roll-forward fallback.** A roll-forward whose single replacement was not adopted, and whose claim proved it runs nothing (the same answers as above; a completion counts unless it is the replacement's natural or prior completion), becomes a restart when no other problem was found. The `rolling_forward` intent is replaced by a restart intent of the same term and planned checkpoint, with the roll-forward fields cleared. Otherwise the roll-forward fails as before, and its held reason says why the share was not restarted.
- **Phase B** (`resumeAdoptedRun`, before any metrics stream is opened), `restartEvacuationReplacement`:
  1. The survivors' supervision is secured, if Phase A had not.
  2. `evacuateGeneratorFor` restarts the share through the generator-loss reshard, with a restart mode:
     - **placement** among the configured and allowed targets that do not take part in the run, never the old replacement or an excluded target, with the reshard's capacity, health and capability checks. Every moved group goes to one generator, which must have capacity for all of them together; otherwise the group is refused. A refused group reserves nothing, and the refusal is the held reason;
     - **capacity** from the share's reservation (`reservePodEvacuation`);
     - **journal**: the restart's own journal replaces the intent before any replacement is staged (`beginRunEvacuationRestartCheckpoint`). It then advances as an evacuation does, except that the source is not stopped: this term's claim already proved it runs nothing;
     - **duration**: the share is given `min(the run's remaining duration by its durable clock, journaled end − steady elapsed)`, refreshed before `CommitWork`; `StartedAt`, `CumulativePause` and `PausedAt` are durable, so pauses count. The committed target never exceeds the journaled end;
     - **pause**: a paused run's share is staged paused;
     - **VUsers and data**: the moved shard keeps its ordinal, count and VUser ID offset, as the reshard does. Its VUsers start fresh with their initial per-VUser parameter and data positions, which the stateless rules make safe; the ramp is the reshard's remaining ramp;
     - **metrics**: the new replacement's stream is left to Phase B, which opens it with the survivors'; live-table channels are opened by the reshard, as for any evacuation.
  3. Whatever generator took the share joins the adopted generators at once, so Phase B streams its metrics and every later stop or failure reaches it. The reshard records `RestartedVUsers` and `RestartedGroups` in the run's latest failover before it writes the plain checkpoint; a successor that rolls forward or promotes a restart's own journal records them in the lineage it installs (`withRunEvacuationRestartRecorded`). Phase B then resends every goal throughput share (`resyncGoalThroughputCaps`), as the end of an evacuation does, because the restarted share was assigned uncapped.
  4. A refusal while the intent, or the restart's own `prepared` or `staged` journal after a clean staging rollback, is still in place holds the run through `failRunEvacuationRecovery`: the hold is written first, then the runtime Phase A installed is cancelled, then the survivors are force-stopped with the rotated token. The cancelled runtime, `remoteFinalizing` and the evacuation-uncertain reason make the run eligible for attestation.
  5. An uncertain restart (after the new replacement may have committed) is left to the reshard's own same-leader fail-closed path.
  6. A stop that wins the race owns the run: Phase B continues so its finalizer gets every adopted generator's stream. A run with no survivor and no restarted share has nothing to resume.
  7. Once the restart's journal was replaced, nothing can hold the run under it. Any failure then (a defensive case: more or fewer than one new replacement) stops every adopted generator with proof (`failResumedAdoptedRunWithStopProof`), as any Phase B failure does.
- **Terms.** `successorGeneration` includes `RestartGeneration`, so the intent's term never fences what it adopted; `resumeInterruptedRunEvacuations` also skips any evacuation journal of the current term, such as a held restart's. A newer term fences the survivors, the old replacement and any new replacement, never the durably fenced source, and records the run `FAILED`.
- **Archive and results.** The old replacement keeps an archive target only if `CommitWork` may have reached it; the fenced source keeps its archive target; a restart's absent source gains none it did not have (`runEvacuationArchiveTargets`). The failover record's restarted share appears in the report's controller-failover section (HTML and PDF note, CSV `restarted_vusers` and `restarted_groups`) and in the baseline comparison warning.
- **No storage or protocol change.** The journal is JSON in the existing continuity column; no proto change was needed.

<a id="roadmap-evacuation-replacement-restart-2026-10-24--invariants"></a>
### Invariants

- **The source and the old replacement cannot run the share.** The source is durably fenced (or, for a restart's journal, proven absent and claimed again while unfenced). The old replacement's acknowledged claim at the successor's generation drained any lower-generation assignment it did not adopt, and its answer shows none is live. An unreachable old replacement is never taken as proof.
- **Old and new never run concurrently.** The new replacement is staged and committed under the successor's generation only after both proofs. A late predecessor's journal writes and capacity changes fail the lease-held compare-and-swap; its generation and token are refused by the generators that the successor claimed.
- **No duplicate start.** Every journal boundary is resumable or fail-closed: an intent is restarted again; a restart journal before `replacement_committing` is drained by the next claim and restarted elsewhere; `replacement_committing` rolls forward (or restarts again if the new replacement was not adopted); `replacement_committed` is promoted.
- **No replayed initialization** of surviving work: survivors are adopted, never restarted. Only the share that runs nowhere starts fresh.
- **No resurrected stopped run.** `STOPPING` runs are not eligible, and a stop that wins the race owns the run.
- **No extended duration.** The share's logical end never exceeds the journaled end or the run's durable duration.
- **Missing capacity is explicit.** No spare generator, or no capacity on one, holds the run with a reason that names it; nothing is overcommitted.
- **Strict-fail defaults.** With the setting off, every journal keeps its previous behaviour.

<a id="roadmap-evacuation-replacement-restart-2026-10-24--validation"></a>
### Validation

All tests are in `pkg/api/run_evacuation_restart_test.go`. The old replacement and the survivor are fake generators unless stated; the new replacement is an in-process generator (`newRestartGenerator`) whose heartbeat reports configured pod capacity when the ledger is used.

- **`TestEvacuationRestartFromSourceFencedJournal`**: a running run, a paused run, an activated standby and a run without a capacity ledger. The candidate stays `RUNNING`; Phase A writes the intent, adopts the survivor, claims the old replacement once (with the adoption, no stop), confirms and commits the moved share's reservation at the new epoch, and never claims the fenced source. Phase B assigns the share once, under the new term and rotated token, paused when the run is, with offset plus duration no longer than the journaled end; the journal becomes the plain checkpoint of the new topology; the failover records the restart; the comparison warning says so. Capacity ends `releasing` for the fenced source (old term) and the old replacement (new term), and `committed` for the survivor and the new replacement (new term).
- **`TestEvacuationRestartAfterAFailedRollForward`**: a `replacement_committing` journal whose replacement answers `UNKNOWN_TEST` (also with a committed reservation), `REJECTED` (an activated standby that lost its staged work), or a controller-stop completion. The roll-forward becomes a restart intent with the roll-forward fields cleared; the old replacement gets no duration change; the share is bounded by the commit target; the old replacement keeps an archive.
- **`TestEvacuationRestartRefusalsHoldTheRun`**:
  - before any claim, each refusal reconciles `STOPPING` with its reason and no generator is claimed: the setting off, iteration termination, `STOPPING`, no run start, no journaled share duration, the fail failover policy, distributed rendezvous, cursor-stateful data and a stateful protocol;
  - at the claim, each holds the run under the intent, eligible for attestation, with the survivor force-stopped at the new term with the rotated token and the reservation unchanged: an unreachable old replacement, one that still runs (also force-stopped), one still stopping, and a survivor stopped by a controller;
  - when the restart runs, each holds the run (status durable `STOPPING` before the first survivor stop), cancels the runtime, keeps the share's reservation committed under the new term and leaves no new reservation, and no same-term recovery is queued: no spare capacity, no spare generator, the share's time ran out, and a new replacement that refuses supervision after staging (held under the restart's own `staged` journal; its staged assignment was accepted at the new term, and after the rollback the generator refuses both a supervision claim and a commit of it).
- **`TestEvacuationRestartFallbackRefusalsHoldTheRollForward`**: an unreachable or still-stopping replacement, the setting off and no run start keep the failed roll-forward's hold; except with the setting off, which holds with the replacement's `UNKNOWN_TEST` answer as before, the reason says why the share was not restarted.
- **`TestEvacuationRestartAcrossAnotherLeaderChange`**: the first successor runs in its own runner and is lost after its intent, after adopting the survivors, after the restart journal began, after staging the new replacement, after the restart fenced its source (a paused run with a durable 30 s cumulative pause), before `CommitWork` and after `CommitWork`. The `source_fenced` boundary is constructed: the first successor is parked once staged, and the test writes its source fence. The second controller rebuilds the run from the store record (status, clock, pause state), as a reload does; the paused share resumes at the run's steady offset, not its wall-clock age, and within the journaled end. A second controller on the same store, with a newer term, then restarts the share (on the first spare generator if no restart journal was written, otherwise on another one, the staged one drained by its claim) or, after `CommitWork`, rolls the restart forward and starts nothing again. Its claim carries the first successor's token proof, or fences a restart's absent source without the run; a source the restart's journal fenced is not claimed again. Where the abandoned replacement held a staged assignment (staged, source fenced, before `CommitWork`), the test itself then sends the first successor's supervision claim and `CommitWork` (its generation and rotated token) to it, and the generator refuses both. After the restart journal began the replacement never held the assignment, so the test sends no commit there; the late `AssignWork` below covers that boundary. The first successor then returns. The recorder keeps every `AssignWork`, `ClaimRunSupervision` and `CommitWork` with its error and refusal message, and the test requires at least one late call at the first successor's generation: `AssignWork` after the journal began, `ClaimRunSupervision` after staging, `CommitWork` before committing. Each must be refused by the generator itself (a gRPC status or a declining answer, not a transport failure), for its execution generation. Every stop the first successor sends the survivor on the way out carries its own generation; at "before `CommitWork`" the survivor is an in-process generator, which refuses that stop and keeps its three active VUsers. No commit is accepted on the abandoned replacement, its journal writes and capacity commit are refused by the lease, and the checkpoint, the reservation and the running share are unchanged. After `CommitWork`, the rolled-forward lineage records the restart.
- **`TestEvacuationRestartAgainstRealGenerator`**: the old replacement is an in-process generator. A staged replacement is drained by the claim and refuses the predecessor's commit; a committed replacement (500 ms start barrier) whose process restarted answers `UNKNOWN_TEST` and the roll-forward falls back to a restart. In both, the new replacement runs five VUsers and refuses the predecessor's duration change and stop.
- **`TestEvacuationRestartResumesThroughPhaseB`**: the survivor and the new replacement are in-process generators. Phase A secures the survivor under the intent; `resumeAdoptedRuns` restarts the share and opens both metrics streams; the survivor refuses the predecessor's generation.
- **`TestRestartJournalRejectsContradictions`**: restart fields on version 5 or `source_fenced`; plan refusals (abandoned, unarmed lineage, completed source, changed survivor, invalid claim, no share duration, a negative offset, two replacements); every contradiction of an intent (version, terms, owner, start, share duration, roll-forward and rollback terms, source fence, commit, staged targets, planned checkpoint topology, archive, lineage, token and newest intent). A valid intent is refused by rollback and roll-forward and by the adoption decoder.
- **`TestRestartJournalWithTheSettingOffIsHeld`** and **`TestRestartCommittingJournalWithTheSettingOffIsHeld`**: an intent, and a restart's `replacement_committing` journal, reaching a term with the setting off are held. For the intent, the newer term fences the survivor and the old replacement, never the source, and records `FAILED`; the committing journal is checked only for the hold.
- **`TestEvacuationRestartWithoutASurvivor`**: a run the source alone ran, ordinary, loss-tolerant and goal-oriented, through `resumeAdoptedRuns` with the new replacement in process: Phase A adopts nothing, Phase B restarts the share (with the per-run lease for the loss-tolerant run), secures it and streams its metrics. The goal run's restarted share receives its throughput cap at the new term. This test found that the reshard dereferenced a nil client map when a restart had no survivor.
- **`TestEvacuationRestartMovesEveryGroupToOneReplacement`**: a source with two groups (8 VUsers). Two spare generators of 10 VUsers: both groups go to one. Two of 6: the restart is refused with its reason, holds the run, and assigns nothing. gen-d wins the first group and then reports no room while gen-e has room for both: the restart is refused rather than moving the second group to gen-e.
- **`TestEvacuationRestartLeavesANaturallyCompletedReplacement`**: a replacement whose duration ran out (`DURATION_ELAPSED` at the commit target) rolls forward completed; nothing restarts.
- **`TestEvacuationRestartFromAStoredRollingForwardIntent`**: an earlier successor left a `rolling_forward` intent; the next term finds the replacement gone, restarts its share with the earlier term's token proof.
- **`TestEvacuationRestartNeverReturnsToAnUnreachableFencedSource`**: the fenced source was unreachable when the first successor installed the run and dropped out of its archive targets; the second successor, with the source back as an idle in-process generator first in the fleet's order, restarts from the first one's restart journal and never offers it the share.
- **`TestRunEvacuationRestartTargetTakesTheShortestEnd`**, and **`TestControllerFailoverReportSectionRendersAllFormats`**, which now covers a restarted share in HTML, PDF, CSV and the comparison warning.
- **`TestEvacuationRestartHoldsAnUnreachableAbsentSource`**: a restart's `prepared` journal whose absent source the next term cannot reach is held under that term's intent; the restart's journal also refuses a share duration longer than its intent carries.
- **`TestEvacuationRestartLeavesARacingStopToTheStop`** and **`TestVersionSixCommittedRestartIsPromoted`** (the absent source gains no archive target).
- **`TestEvacuationRestartHandsTheNewReplacementToARacingStop`**: a stop marked as the restart writes its plain checkpoint leaves the new replacement adopted, so Phase B streams it for the stop.
- **Race detector**: the restart tests pass under `-race`. The first `-race` run reported a data race in `TestEvacuationRestartResumesThroughPhaseB`: the fixture listed `gen-c` twice among the backend's targets, and Phase B started two metrics consumers for one stream. The fixture now lists each target once.
- **Existing suites**: rollback, roll-forward, terminal cause, recovery and adoption cases are unchanged and pass; their fixtures were extended (extra fleet targets, a backend timeout, a second moved group, a source-only topology).
- **In-process fixtures** use a 10 s generator RPC timeout; the generators `newRestartGenerator` builds dial with 5 s, while the old replacement of `runRealGeneratorRestart` uses the shared `newTestGeneratorRPCClient`, which dials with 1 s. Fakes keep a 1 s timeout. Every runner a restart test creates ends its runtimes before the store closes (`stopRunnerRuntimes`): it joins the supervision keeper, cancels the metrics lifecycle, the run's context and the runner. Metrics consumers have no done signal: one whose receive fails after the cancellation returns without writing, but one already past that check can still process one batch it had received.
- **Mutation passes** (`go test -overlay`, the restart tests above): thirty mutations, each caught:
  - refusals: the setting check, the workload check, the run-clock check, the journaled-duration check, the held reason, the `ALREADY_STOPPED` terminal-state requirement, an unreachable old replacement, an old replacement that still runs, a survivor's unrecorded completion, a restart's unproven absent source, the fallback's proof and unreachable checks, and the Phase B hold on a refusal;
  - fences and journal: no intent before the claim, an intent that keeps the roll-forward fields, the restart field contradictions, the restart journal shape, the version 6 roll-forward setting check, an unclaimed absent source, a stopped-again source, retired targets offered for placement, the intent's duration bound in the restart's journal, and Phase B skipping the restart;
  - capacity, duration and results: an uncommitted share reservation, refusals that reserve before refusing, the duration cap, the old replacement's archive, the absent source's archive, a held runtime left renewing, and a same-term recovery queued for a held restart.

  The first pass had two survivors, the absent source's fence result and the intent's duration bound in the restart journal; `TestEvacuationRestartHoldsAnUnreachableAbsentSource` was added for both. Four mutations first failed to build and were rewritten; one batch was rerun after the disk filled.

  After review round 1, fifteen more, ten caught: placing groups on more than one generator, ignoring the capacity already placed on the restart's generator, no goal-cap resync, exclusions not carried in the journal, the failover record dropped by the reshard, by a promotion and by a roll-forward of a restart's journal, a restart's journal begun from another source, the longest instead of the shortest journaled end, and the policy check (the last three after `TestRunEvacuationRestartTargetTakesTheShortestEnd`, a source check in `TestEvacuationRestartHoldsAnUnreachableAbsentSource` and a fail-policy refusal case were added). Five survive and are untested:
  - accepting more than one new replacement as success: placement can no longer produce one;
  - dropping the distributed-backend check: every fixture is distributed;
  - dropping Phase B's securing of survivors: Phase A always secures them first, so the branch is defensive;
  - opening the new replacement's metrics stream in the reshard (`metricsParent = nil`) and dropping the pre-commit duration refresh: the first only duplicates a stream Phase B replaces, the second only matters when staging outlasts the share's time.

  After review round 2, five more, each caught: dropping the rule that keeps every group on the restart's generator, not adopting the new replacement, adopting it only after the status check (by `TestEvacuationRestartHandsTheNewReplacementToARacingStop`), exclusions not carried in the journal (now against a real idle source), and the plan's refusal of a replacement the prior lineage records completed. Further defensive branches stay untested: the stop-with-proof failure after the journal is replaced (unreachable while placement takes one generator), `failRunEvacuationRestart` stopping restarted generators too, the group de-duplication in `withRestartedShare`, and treating any number of new replacements as success.

  After review round 3, five more (one masked). Caught:
  - comparing the planned and prior completed targets, dropped from `validRestartCheckpoint` (`TestRestartJournalRejectsContradictions`) and from `validRollForwardCheckpoint` (`TestRollingForwardJournalRejectsContradictions`);
  - a generator that accepts mutations at an older execution generation: the late `AssignWork` after the journal began is accepted, and the late `CommitWork` before committing is refused for another reason than its generation;
  - a generator that grants run supervision to an older generation and another lease owner: the late supervision claims after staging are refused only because the lease expired, not for their generation.

  Masked: a generator that grants supervision to an older generation while it still checks the lease owner survives, because the returning first successor is another owner and is refused by that check.

  Not mutated: skipping the re-arm after a restart installs (the re-arm is refused by the kept journal anyway, so dropping the skip only adds a warning), the live-table client exclusion in Phase B, the stop race with no survivor, and the panic handler of a restart's Phase A.

<a id="roadmap-evacuation-replacement-restart-2026-10-24--review"></a>
### Review

Round 1 found no High issue, seven Medium and eleven Low. All Medium findings are fixed:
- **M1, placement split across generators.** Per-group placement could put the moved groups on two new generators; the reshard then succeeded, replaced the journal, and the restart's single-replacement check held a run that no longer had a journal, without stopping either generator. Fixed: a restart places every moved group on one generator with room for all of them, and refuses before reserving otherwise. Any failure once the journal is replaced now stops every adopted generator with proof, and whatever took the share is adopted before any status check.
- **M2, no goal cap.** The restarted share started uncapped. Phase B now resends every goal share; the goal-run case tests it.
- **M3, weak duplicate-start check.** The late-commit assertion could not fire and the zero-VUser waits were true at once. The recorder now keeps each accepted commit; the test asserts the first successor's late commit was sent and refused, and that the abandoned replacement accepted none.
- **M4, natural completion untested.** Added `TestEvacuationRestartLeavesANaturallyCompletedReplacement`.
- **M5, stored `rolling_forward` untested.** Added `TestEvacuationRestartFromAStoredRollingForwardIntent`.
- **M6, paused leader change not from durable state.** The fixture now stores the run's status, start, duration, pause time and a 30 s cumulative pause; the second controller rebuilds the run from the store record, and the test asserts the offset excludes the pause.
- **M7, report output untested.** The report-section test now covers the restart note in every format; the HTML report gained the note.

Low findings:
- **L1** fixed: `RestartExcluded` is durable in the intent and the restart's journal (`TestEvacuationRestartNeverReturnsToAnUnreachableFencedSource`, which reproduces the reported case).
- **L2** fixed: the reshard records the restarted share before writing the checkpoint, and a successor that rolls forward or promotes a restart's journal records it from the journal.
- **L3** fixed: restarted targets join the adopted generators before the status checks.
- **L4**, a stop between Phase A and the restart on a run with no survivor, which finds no generator to stop: recorded under Remaining, in the bullet starting "A stop between Phase A and the restart".
- **L5** recorded: the `REJECTED` answer for a run that already belongs to the claimed generation cannot occur here (only this term assigns at its generation, never to the old replacement), and the answer carries no generation to tell it apart; noted at `runEvacuationRestartAbsence`.
- **L6** fixed: 10 s RPC timeout for in-process fixtures and a 5 s dial for `newRestartGenerator` (round 2 corrected the claim that every in-process dial was raised).
- **L7** fixed: resumed runs are cancelled and their keepers joined before the store closes (round 2 replaced a fixed sleep with `stopRunnerRuntimes`).
- **L8** fixed: the fake's methods sit with its type; the capacity server's comment matches its stub.
- **L9** fixed: the constructed `source_fenced` boundary is stated; a fenced absent source is checked not to be claimed again, and the committed boundary checks the ledger after the late return.
- **L10** fixed: comment and record wording, the stale roll-forward record lines, the spec and the node-failure plan's table row.
- **L11**: the listed guards were mutated; see Validation for which are caught and which are untested.

Round 2 found no High issue, one Medium and ten Low. All are fixed or recorded:
- **M-a, round 1's M3 still open at three boundaries.** The first successor's late call there was `AssignWork` or `ClaimRunSupervision`, which no assertion read, and staged work has no active VUsers. Fixed: the recorder keeps each `AssignWork` and `ClaimRunSupervision` with its outcome, the test asserts the parked call was refused at the first successor's generation, and it sends the first successor's supervision claim and commit to the abandoned replacement itself and asserts the commit is refused. The same commit check covers the staged replacement a refused supervision rolled back.
- **L-a** fixed: a case where only another generator could take the second group pins the one-generator rule.
- **L-b** fixed: `endResumedRun` joins the keeper instead of sleeping; its comment and this record's L7 line are corrected.
- **L-c** fixed: every runner a restart test creates, including those `takeOver` replaces, ends its runtimes in cleanup.
- **L-d** fixed: the fenced source is an idle in-process generator with capacity in `TestEvacuationRestartNeverReturnsToAnUnreachableFencedSource`, so the assertion shows it was excluded from placement.
- **L-e** fixed: the Validation entries above (fail policy, the setting-off reason, the setting-off fence, existing fixtures, the dial, L4).
- **L-f** fixed: the spec names version 5 and 6 `source_fenced` journals and says that a `replacement_committing` restart journal whose new replacement was not adopted restarts again.
- **L1** fixed: `TestEvacuationRestartHandsTheNewReplacementToARacingStop`; the remaining defensive survivors are listed under Validation.
- **L2** fixed: the CSV joins restarted groups with `"; "`, as the archive source groups are.
- **L4** fixed: `runEvacuationRestartPlan` refuses a replacement the prior lineage records completed.
- **L5** recorded under Remaining, in the bullet starting "A share whose time ran out". Round 2's code-safety Lows were numbered L1 to L5; its L3, a note that `endResumedRun`'s comment overstated what it waits for, is the same issue as L-b and is fixed with it.

Round 3 found no High or Medium, so the review stopped after it. Its code reviewer re-ran the restart, evacuation, adoption and failover tests (and the restart tests under `-race`), found no regression to the round 1 and 2 fixes, and confirmed the L4 refusal cannot refuse a legitimate restart: a target recorded complete is never placed as a replacement. Its tests reviewer confirmed the M-a fix by mutation: a generator accepting lower-generation mutations, a stale supervision claim or a stale commit, and a controller skipping the staging rollback after a refused supervision, each fail the boundaries they should. A goroutine probe found no supervision keeper left after the restart tests (13 with `stopRunnerRuntimes` disabled). Its six Low findings were fixed afterwards:
- **A stored intent's planned completed targets were not compared with the prior's.** `runEvacuationRestartPlan` checks `guard.Prior.CompletedTargets`, but Phase A skips claiming the planned checkpoint's completed targets. Fixed: `validRestartCheckpoint` and `validRollForwardCheckpoint` require the two lists to be equal. Neither intent can legitimately differ: both are planned before the claim from the prior lineage (a roll-forward records natural completions only in the plain checkpoint that replaces the journal). New refusal cases in both contradiction tests.
- **Late stops to the survivor were unasserted.** Fixed: every stop the fake survivor receives after the takeover must carry the first successor's generation, at least one must arrive at each boundary with a restart journal, and at "before `CommitWork`" the survivor is an in-process generator that keeps its active VUsers after the late return.
- **The late-call check read only a boolean and only the last call.** Fixed: each call is recorded with its error and refusal message; the test requires at least one late call at the first successor's generation and checks every one, each refused by the generator itself for its execution generation.
- **The test's own claim results were discarded, and at "after the restart journal began" its own commit proved nothing.** Fixed: the test's own supervision claims and commits are asserted refused by the generator, after a refused supervision too; at "after the restart journal began" it sends none, and the record says that boundary rests on the late `AssignWork` check.
- **A zero-VUser wait was true at once** for staged work. Removed.
- **`stopRunnerRuntimes` did not wait for metrics consumers.** They have no done signal; its comment and the Validation entry now say that one already past its context check can process one more batch.

A review of these fixes found no High or Medium. It confirmed that the new completed-target comparisons cannot refuse a legitimate journal, including those written by the earlier roll-forward and terminal-cause releases: every writer clones `Prior`'s list, a resumed `rolling_forward` journal returns its stored checkpoint unchanged, and natural completions go only into the plain checkpoint that replaces the journal; a crafted journal that differs is held `STOPPING` as contradictory. Its Low findings are recorded, not fixed:
- **A parked late call's deadline keeps running.** The backend sets each call's deadline (2 s for commit and supervision) before the recording client parks it, so a test stalled past it would see `DeadlineExceeded`, which the strengthened check rejects as not refused by the generator. Measured parking is 100–200 ms under `-race`. Fix: re-derive the call's context after the park hook.
- **Late-call failure messages print the error as an address**, because `%+v` on `restartCall` does not call `Error()` on its unexported field. Fix: a `String()` method.
- **The in-process survivor's late stops are refused by the rotated run token, not by its generation**, and at "before `CommitWork`" no assertion shows a stop arrived; the comments and the "at least one must arrive" wording above overstate both. Fix: count the survivor's `StopTest` calls and reword.
- **The mutation list first said six after round 3**; it lists five (one masked), now corrected.

<a id="roadmap-evacuation-replacement-restart-2026-10-24--remaining"></a>
### Remaining

- **Review.** Three rounds and a review of the round 3 fixes ran; every finding is fixed or recorded under Review.
- **Deployed qualification.** REC-A's exit still requires failure injection on a deployed cluster at every journal boundary, worker or node partition, late old-owner return and independent target counters. These tests use fake and in-process generators.
- **Same-target restart.** The share always moves to another generator; the old replacement is never reused, even when it is healthy and its reservation is still provable. Reusing it would need a fresh assignment for a run its generator already retains an outcome or tombstone for.
- **One replacement only.** An evacuation whose moved groups went to more than one replacement is held, as before.
- **A replacement whose generator process restarted** answers `UNKNOWN_TEST` with a new pod-capacity incarnation. With pod reservations enabled its journaled reservation cannot be confirmed, so the run is held. Without them it restarts (the in-process test).
- **An interrupted restart's absent source** keeps its committed reservation until the run ends, when the restart was lost before fencing its source; a later restart moves on from the new replacement's reservation.
- **Not exercised:** a loss-tolerant run with a survivor, live tables, worker recovery placement with standbys, a stop racing the restart on a run with no survivor, a panic while preparing a restart, Phase B securing failures before the restart, and the dashboard's run view, which shows no restart note (the HTML, PDF and CSV reports do). Updated 2026-10-25: the [evacuation completion slice](#roadmap-evacuation-completion-2026-10-25) adds two restart cases this list did not name, a spare without the evacuation assignment state and no spare capacity at the roll-forward fallback; the items above stay unexercised.
- **A stop between Phase A and the restart on a run with no survivor** finds no generator to stop (`stopTestContext` returns `errDistributedGeneratorTargetUnknown`), so the run stays `STOPPING` until a newer term fences it or an administrator attests, although this term proved nothing runs it. Finishing it from that proof (terminal stop, the share's reservation released, the intent replaced) is not implemented. Still open after the [evacuation completion slice](#roadmap-evacuation-completion-2026-10-25), which records it.
- **A share whose time ran out holds the run.** When the run's remaining duration has run out by the restart, the restart refuses and holds the run, force-stopping the survivors even if they all finished naturally; after attestation or a newer term's fence the run ends `FAILED` rather than `COMPLETE`.
- **A fenced source unreachable at a re-arm** loses its archive target, as for any adopted run; `RestartExcluded` only keeps it out of placement.
- **A controller lost between reserving the new replacement's capacity and writing the restart's journal** leaves a pending reservation no journal records, as an ordinary evacuation lost at the same point does. The next term does not know it; if its restart picks the same generator, the reservation is refused and the run held.
- **Duration measured by the controller.** The remaining duration comes from the run's durable clock, as for any generator-loss reshard. A generator whose logical clock started after the controller's records its end at most that skew later than the survivors'.
- **Pause at the restart.** A pause or resume requested during Phase B waits for the restart, which holds the topology gate; one requested after it reaches the new replacement as any participant. A pause request that lands between Phase A and the restart is covered by the staged pause state only through the run's durable status.

<!-- Original record: docs/qa/reviews/roadmap-evacuation-rollback-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-evacuation-rollback-2026-10-22"></a>

## Evacuation rollback (REC-A)


Status: implemented behind an opt-in. Qualified locally only.

<a id="roadmap-evacuation-rollback-2026-10-22--contract"></a>
### Contract

- **Opt-in.**
  - `APEXLOAD_EVACUATION_ROLLBACK_RECOVERY` (`WithEvacuationRollbackRecovery`) is off by default.
  - When on, a new evacuation journal is version 4. It adds the replacement capacity claims (`CapacityClaims`) and the idle standby rows they activated (`StandbyPreImages`).
  - When off, journals stay version 3, exactly as before.
- **Older peers.**
  - An older controller cannot read a version 4 journal: `decodeRunContinuityCheckpoint` rejects it, and older releases only recognize versions 2 and 3.
  - Such a controller keeps the run `STOPPING` through its interrupted distributed stop path.
  - Enable the setting only once every replica reads version 4.
- **Eligibility** (`evacuationRollbackGuard`, `runEvacuationRollbackPlan`). A run qualifies only when:
  - the setting is on and run adoption is enabled;
  - the run is `RUNNING` or `PAUSED`, with the continue failover policy, on the distributed backend;
  - the journal passes `interruptedEvacuationTargets`, is version 4, is in phase `prepared`, `staged` or `rolling_back`, and is not abandoned (`AbandonedAt`);
  - the source is not a prior completed target;
  - no surviving participant's shards changed;
  - every capacity claim and pre-image matches the journal's replacement targets, term and frozen identity.

  Anything else keeps the fail-closed path: `source_fenced`, `replacement_committed` without promotion, abandoned journals, `STOPPING`, and the fail policy.
- **Successor rollback** (Phase A, `adoptOrphanedRuns`), in order:
  1. The journal's prior checkpoint, whose shards equal `BeforeShards`, becomes the adoption checkpoint.
  2. `persistRunEvacuationRollbackIntent` writes phase `rolling_back`, with the planned adoption intent inside the journal, before any generator sees the rotated token.
  3. The same batched epoch claim adopts the prior participants and claims each replacement with no adoption for the run. That claim drains the staged replacement assignment before it is acknowledged.
  4. `completeRunEvacuationRollback` requires every replacement claim to be acknowledged, the source to be adopted, not completed, and no participant completed outside `Prior.CompletedTargets`.
  5. `rollbackRunEvacuationCapacity` then runs under the successor's term:
     - It releases the replacement reservation, or the activated standby reservation, and confirms the release with the generator.
     - For a standby, it then reserves the pre-image idle again and confirms it before committing it.
     - Each step resumes from the ledger it finds, including a standby the predecessor already returned idle.
  6. Only then is the journal replaced by the plain prior checkpoint, and adoption continues as usual.
  7. `adoptPodCapacity` skips, and `AdoptPodCapacity` accepts, an idle standby already reserved under the successor's term.
- **Failures stay fail-closed.**
  - The journal is replaced only after a complete rollback. That needs:
    - every prior participant adopted, the source adopted rather than completed, and no participant completed outside `Prior.CompletedTargets`;
    - every replacement claim acknowledged;
    - every capacity step confirmed.
  - On any problem, `failRunEvacuationRollback`:
    - keeps the `rolling_back` journal;
    - first writes the run `STOPPING` with the evacuation-uncertain reason, and installs no runtime;
    - then force-stops each prior participant this term adopted, with the rotated token, and adds any stop that is not acknowledged to the held reason.

    Updated 2026-10-24: the hold now precedes the stops (previously the stops came first), and a participant that reports a completed run the prior lineage never recorded fails the rollback. Both came from the [roll-forward review](#roadmap-evacuation-rollforward-2026-10-24--review), as did two reload rules that apply to rollback holds too: a reload keeps a hold written in the current term over a row read before the write, and replaces a run object left fenced to a lease term this process lost.
  - A later reader therefore never sees a `RUNNING` row with a plain checkpoint after a failed rollback.
  - That term's fail-closed recovery (`recoverInterruptedRunEvacuation`, `resumeInterruptedRunEvacuations`) does not fence at or below `RollbackGeneration`, because the epoch claim of a term that adopted the prior topology does not drain what it adopted.
  - A newer term fences every participant and records the failure. An administrator attestation also resolves it, because the run has no runtime.
- **Clean same-leader rollback** (`restoreRunEvacuationPriorCheckpoint`):
  - Applies when the leader that wrote a version 4 `prepared` or `staged` journal positively rolls its staging back before the source fence:
    - staging failed and its rollback succeeded;
    - replacement lease establishment failed;
    - the replacement supervision claim failed;
    - the evacuation became stale.
  - That leader restores the checkpoint the journal replaced, or no checkpoint if the run was unarmed. Without this, a later successor would roll back to a topology from before any VUser change made afterwards.
  - Version 3 journals keep their earlier behaviour.
- **Other failures before anything was adopted**, and panics, also hold the run `STOPPING` (`holdInterruptedRunEvacuation`). That covers:
  - a rollback candidate whose prior checkpoint cannot be used before any claim, for example a failed live-state restore or an invalid adoption intent;
  - a panic before the journal is replaced, which goes to `failRunEvacuationRollback`;
  - a panic while handling a failed rollback, which `isolateRunAdoption` catches for any run still under an evacuation journal.

  None of these binds an ordinary adoption failure, which would make the run terminal while its participants may still run.
- **Dead journals left by this leader.** A journal can survive because the restore or marker write failed after a clean rollback, or because the staging rollback itself errored. The next re-arm of the run, such as a VUser scale, is refused by an unresolved journal. `writeRunContinuity` therefore marks a version 4 `prepared` or `staged` journal of the same term and owner abandoned. A re-arm never overlaps an evacuation of the run, so such a journal is dead, and its prior topology is about to go stale.
- **Clean same-leader rollback, ordering.** The leader returns the evacuation's capacity first. The journal holds the standby pre-images, so it is replaced only once the capacity is back:
  - The leader checks the ledger itself (`runEvacuationCapacityReturned`), because an ordinary release reports no error.
  - If the capacity is not back, the journal is marked abandoned at once (`markRunEvacuationCheckpointAbandoned`). A successor never rolls back to an abandoned journal and holds the run `STOPPING`, as for a version 3 journal.
  - The deferred capacity rollback then retries. If the ledger then shows the capacity returned, `finishRestore` restores the prior checkpoint.
  - While any journal remains, the run can neither start another evacuation (`beginRunEvacuationCheckpoint`) nor re-arm its checkpoint.
- **A new term after `rolling_back`** rolls back again, and its source adoption carries the earlier term's token proof.

<a id="roadmap-evacuation-rollback-2026-10-22--validation"></a>
### Validation

- **`TestEvacuationJournalVersionFollowsRollbackRecovery`** (`pkg/api`):
  - version 3 with no capacity when the setting is off, and version 4 with claims when it is on;
  - progress keeps the version it was written with;
  - an adoption reader rejects both versions.
- **`TestEvacuationRollbackReturnsRunToPriorTopology`** covers `prepared` and `staged`, each with an ordinary replacement and with an activated standby:
  - reconcile leaves the run `RUNNING`;
  - the source alone is adopted from the old term;
  - the replacement receives one epoch claim with no adoption, and is not a participant;
  - the durable checkpoint is the prior topology under the new term;
  - the source capacity is adopted;
  - an ordinary replacement reservation is released at the new epoch;
  - a standby is released and reserved idle again, at the pre-image's revision, under the new term.
- **`TestEvacuationRollbackKeepsFailClosedPaths`**: with the setting off, a `source_fenced` journal, a `STOPPING` run or the fail policy, the run is no adoption candidate and reconciles to `STOPPING`.
- **`TestEvacuationRollbackPlanRejectsUnsafeJournals`**: rejects a version 3 journal, a completed source, a changed survivor, a claim off the replacement targets, a committed claim, a claim from another term, an unclaimed pre-image, a pre-image that is not idle, and a changed pre-image budget.
- **`TestEvacuationRollbackUnfencedReplacementKeepsJournal`**:
  - an unreachable replacement keeps the `rolling_back` journal with its rollback generation and leaves the replacement capacity unchanged;
  - the adopted source is force-stopped at that term;
  - the run is durably `STOPPING`, with the evacuation-uncertain reason and no runtime, and is eligible for attestation;
  - the same term's recovery refuses to fence;
  - a newer term fences the source and the replacement and records the run `FAILED`.
- **`TestEvacuationRollbackResumesAfterAnotherLeaderChange`**: a successor lost right after writing its `rolling_back` intent leaves the run `RUNNING`. The next term rolls it back, its source adoption carries the first term's token proof, and the standby ends idle under the newer term.
- **`TestEvacuationRollbackFailsWhenSourceAlreadyCompleted`**:
  - a completed source keeps the journal, and the run is held `STOPPING` without stopping that source again;
  - the replacement capacity is unchanged;
  - a later successor does not treat the run as an ordinary adoption candidate.
- **`TestEvacuationRollbackCapacityFailureKeepsJournal`**: an unconfirmed release keeps the journal, holds the run `STOPPING` and leaves the reservation `releasing`.
- **`TestEvacuationRollbackAcceptsPredecessorRestoredStandby`**: a standby the predecessor already returned idle, committed or pending, is accepted. It is never released, and it ends committed under the new term.
- **`TestRestoredRunEvacuationStandbyMatchesFrozenIdentity`**: a newer idle revision is accepted, and changed peaks, incarnation, budget, demand, sharing or attempt are rejected.
- **`TestEvacuationRollbackUnusablePriorCheckpointHoldsRun`**: an out-of-order prior adoption intent holds the run `STOPPING` with its journal unchanged, and no generator is claimed.
- **`TestEvacuationRollbackFailurePanicHoldsRun`**: a panic while force-stopping after a failed rollback still holds the run `STOPPING` under its `rolling_back` journal.
- **`TestEvacuationRollbackPanicAfterSwapIsOrdinaryFailure`**: a panic after the journal was replaced takes the ordinary adoption-failure path. There is no evacuation-uncertain hold and no force stop in that term.
- **`TestEvacuationRollbackPanicKeepsJournal`**: a panic in the capacity rollback keeps the journal, force-stops the adopted source and holds the run `STOPPING` for attestation.
- **`TestCleanSameLeaderRollbackRestoresPriorCheckpoint`**:
  - an armed run gets back its prior checkpoint (the same topology and arming time), and the run can be re-armed with new VUser counts afterwards;
  - an unarmed run returns to no checkpoint.
- **`TestCleanRollbackLeavesOtherJournals`**: version 3, `source_fenced`, other-term and other-owner journals are not replaced. The owner check in `restoreRunEvacuationPriorCheckpoint` is defence in depth: the store's lease-fenced swap already refuses another owner, so removing the check does not fail this test.
- **`TestVersionFourJournalCarriesFailoverHistory`**: a version 4 journal still supplies the run's failover history.
- **`TestEvacuationRollbackRefusesChangedLedger`** covers seven changes, each of which keeps the journal and holds the run `STOPPING`. None of the six ledger changes reaches the generator's capacity RPC. The swap failure happens after the capacity was returned. The cases:
  - a replacement row that changed since the journal, or was committed;
  - a predecessor-restored standby from a newer term;
  - a replacement row without peaks;
  - a predecessor-restored standby with another incarnation;
  - a standby left `releasing`;
  - a final journal swap that fails.
- **`TestEvacuationRollbackResumesPredecessorRelease`**: a replacement reservation the predecessor left `releasing` is released at the new epoch, and one it already deleted needs no request.
- **`TestRollbackRunEvacuationCapacityRefusesUnprovableTargets`**: refuses a replacement client without the capacity RPC, and a ledger reservation the journal did not record.
- **`TestEvacuationRollbackAdoptsPausedRun`**: a paused run is rolled back and stays paused.
- **`TestFailedEvacuationRollbackIsNotRetriedInItsTerm`**: `resumeInterruptedRunEvacuations` queues no same-term recovery for a failed rollback.
- **`TestFailedEvacuationRollbackWaitsWhenTermIsUnknown`**: a replica that cannot read its term queues no recovery either. This is a regression check, not one that catches a mutation: an unreadable term reads as generation 0, which already fails the comparison with `RollbackGeneration`.
- **`TestRollingBackJournalRejectsContradictions`**: each contradiction in a `rolling_back` journal is refused by both the fail-closed recovery and the rollback plan. The contradictions are version 3, the rollback term, owner and start, the staged targets, the source fence and the replacement commit.
- **`TestRunEvacuationCapacityReturnedReadsTheLedger`**:
  - a pending or `releasing` replacement reservation is not returned, and a deleted one is;
  - a pending idle standby is not returned, and a committed idle one is;
  - a committed row that is still activated, or has another incarnation or budget, is not the returned standby;
  - a released standby is not returned idle.
- **`TestAbandonedEvacuationJournalIsNeverRolledBack`**:
  - the abandoned marker is durable and is not applied twice;
  - the rollback plan refuses the journal, while the fail-closed participants remain;
  - restoring the prior checkpoint still accepts an abandoned journal. The capacity gate lives in `evacuateGenerator`; see Remaining.
- **`TestReArmMarksADeadSameTermJournalAbandoned`**: a refused re-arm marks a same-term version 4 `prepared` or `staged` journal abandoned, and leaves version 3 and other-term journals unchanged. The owner check in `markRunEvacuationCheckpointAbandoned` is defence in depth: the store's lease-fenced swap already refuses another owner, so this test cannot distinguish it.
- **`TestVersionFourCommittedEvacuationIsPromoted`**: a version 4 `replacement_committed` journal is promoted and its replacement adopted, as for version 3.
- **`TestAdoptPodCapacityAcceptsCurrentTermStandby`** (`pkg/storage`, and the PostgreSQL integration test): adoption is refused while a standby reserved under the new term is still pending, and succeeds once it is committed. The existing `TestAdoptPodCapacityRequiresEveryStandby` still refuses an old-term standby that was left out.

<a id="roadmap-evacuation-rollback-2026-10-22--review"></a>
### Review

Round 1 used two reviewers, one on safety and one on the tests and docs.
- **High:** after a problem such as a completed source, the journal was still replaced while the run stayed `RUNNING` on disk until Phase B. A successor in between could adopt the plain checkpoint and finalize the run `COMPLETE`. Fixed: the journal is replaced only after a complete rollback, and a failed rollback is held `STOPPING` at once.
- **Medium:** an unfenced replacement bound an adoption failure whose stop proof could not read the journal. The run was refused both recovery in that term and administrator attestation. Fixed: no runtime is installed, the adopted participants are force-stopped, and the run is eligible for attestation.
- The tests-and-docs reviewer was stopped before it reported, because its snapshot predated those fixes. Its scope moved to round 2.

Round 2 used two fresh reviewers. The safety reviewer confirmed both round-1 fixes and found:
- **Medium:** a version 4 journal left by a clean same-leader rollback kept a prior topology that went stale as VUsers changed. Fixed by `restoreRunEvacuationPriorCheckpoint`.
- **Low:** a panic before the journal was replaced bound an ordinary adoption failure, which installed a runtime and blocked attestation. Fixed with a rollback-specific recovery.
- **Low:** failover history was not loaded from version 4 journals. Fixed in `setContinuityCheckpointLocked`.
The tests-and-docs reviewer of round 2 found weak tests: three mutations of safety checks survived. Its findings and the fixes:
- **Tests added:** changed replacement and standby rows, the capacity-journal validations (each asserting its error), predecessor release states, unprovable targets, a paused run, the journal swap failure, the same-term skip, and version 4 promotion. The fail-closed cases now check `evacuationRollbackGuard` directly. All three mutations now fail the tests.
- **Storage:** the exemption now also requires the standby to be committed, with a negative test for a pending one.
- **Remaining:** now lists the real-generator drain proof and the write failures that are still untested.
- **Docs:** the README.md entry names its prerequisites and the attestation, and the spec no longer contradicts itself.
- **Nit:** a missing claim result now has its own message. `resumeInterruptedRunEvacuations` also skips a `rolling_back` journal when the current term cannot be read.

Round 3 used two fresh reviewers. The safety reviewer confirmed the round-2 fixes and found:
- **Medium:** a rollback candidate whose prior checkpoint could not be used was marked `FAILED` by `failRunAdoption`. Before this change it was held `STOPPING`. Fixed: it is held `STOPPING`.
- **Low:** a panic inside the rollback failure handling reached `failRunAdoption`. Fixed in `isolateRunAdoption`.
- **Low:** a clean same-leader rollback replaced the journal before returning the capacity, which could lose the standby pre-image. Fixed: capacity is returned first, and the journal is kept if that fails.

A mutation pass after round 3 removed seven safety checks in `run_evacuation_rollback.go`. Tests were added for the two that survived and have their own effect: the replacement-row state and the standby generation bound. The round-4 reviewer then found three more surviving mutations, listed under round 4 below. The force-stop mutation did not compile, but its effect is asserted by `assertFailedRollbackHeld`.

Round 4 used two fresh reviewers. The safety reviewer confirmed the round-3 fixes and found two Low issues, both in the same-leader journal restore when capacity is not returned:
- **Stale journal on a failed capacity return.** A capacity return that failed kept the journal forever, so its prior topology could go stale while the run was scaled.
- **Silent ordinary release.** An ordinary release reports no error, so the journal could be replaced while the replacement reservation remained.

The fix:
- the restore now checks the ledger itself (`runEvacuationCapacityReturned`);
- it retries after the deferred capacity rollback;
- otherwise it marks the journal abandoned (`AbandonedAt`, via `markRunEvacuationCheckpointAbandoned`). A successor never rolls back to an abandoned journal, and it stays on the fail-closed path.

The tests-and-docs reviewer found three surviving mutations: the standby budget against its claim, a claim without an incarnation, and the whole `rolling_back` consistency check. It also found several weak assertions and README overstatements. All of these are fixed:
- **New plan cases.** One changes the budget and demand together; one blanks the claim's incarnation.
- **`TestRollingBackJournalRejectsContradictions`.** Each contradiction is refused by both the fail-closed recovery and the rollback plan.
- **Durable checks in `TestEvacuationRollbackKeepsFailClosedPaths`.** It now checks the stored status, reason and unchanged journal.
- **`TestEvacuationRollbackResumesAfterAnotherLeaderChange`.** It reads the stored `RUNNING` status. It also checks the replacement fence and the final plain checkpoint.
- **`TestEvacuationRollbackRefusesChangedLedger`.** It now checks for no capacity RPC on every ledger change.
- **More identity cases** in `TestRestoredRunEvacuationStandbyMatchesFrozenIdentity`.
- **`TestFailedEvacuationRollbackWaitsWhenTermIsUnknown`.** A regression check for the unreadable-term skip; see Validation.
- **`TestRunEvacuationCapacityReturnedReadsTheLedger` and `TestAbandonedEvacuationJournalIsNeverRolledBack`.** They cover the safety fixes.

Round 5 used two fresh reviewers. The safety reviewer confirmed the round-4 fixes and found one Low issue. A journal could still survive unmarked when the restore or the abandoned-marker write itself failed, or when the staging rollback errored, and a later scale then made its topology stale. Fixed: `writeRunContinuity` now marks such a journal abandoned on the refused re-arm (`TestReArmMarksADeadSameTermJournalAbandoned`). Version 3 and other-term journals are left unchanged.

The round-5 tests-and-docs reviewer found nine issues, all fixed:
- **Tests.**
  - The ledger-return test now covers the standby's identity.
  - The swap-failure case asserts the capacity was returned.
  - The unreadable-term test is described as a regression check.
- **Naming.** The restore functions were renamed, from `abandon…` to `restoreRunEvacuationPriorCheckpoint`, `restoreGuard` and `finishRestore`.
- **Docs.** The abandoned-journal behaviour is added to this contract, the README.md entry and the spec. The four round-4 tests are listed under Validation. The abandon wiring inside `evacuateGenerator` is listed under Remaining.

Round 6 used two fresh reviewers. The safety reviewer found nothing: no caller of the re-arm can overlap an evacuation of the same run in the same term, and the marking can only remove rollback eligibility. The tests-and-docs reviewer found five Low or Nit issues, all fixed:
- the re-arm test now covers a `staged` journal;
- the marker's owner check is described as defence in depth;
- the order of the abandoned marker and the deferred restore is corrected here and in the spec;
- an overstated test claim is corrected;
- two leftover names from the rename are changed.

Round 7 used one tests-and-docs reviewer, because the code was unchanged since the clean round-6 safety review. It found one Low and three Nits, all fixed:
- `TestEvacuationRollbackPanicAfterSwapIsOrdinaryFailure` covers the switch back to ordinary panic handling once the journal is replaced;
- two code comments now give both reasons a journal is marked abandoned;
- the README.md entry and the spec now mention re-arm marking;
- a sentence that contradicted itself is reworded.

Round 8 used one tests-and-docs reviewer. It found nothing: both of its mutations were caught, and the README, the README.md entry and the spec match the code.

Gates on the reviewed code (`dd88b5f54`):
- the full `pkg/api` suite;
- the PostgreSQL storage suite (`-tags postgres_integration`), including `TestPodCapacityPostgresAtomicPlacement` with `checkAdoptCurrentTermStandbyPodCapacity`.

<a id="roadmap-evacuation-rollback-2026-10-22--remaining"></a>
### Remaining

- **Failure injection.** REC-A's exit still requires failure injection at every journal boundary, worker or node partition, late old-owner return, and independent target counters. These tests use fake generators.
- **Not covered.**
  - a restart of a missing replacement for the eligible HTTP workload. Updated 2026-10-24: covered behind a further opt-in by the [replacement restart slice](#roadmap-evacuation-replacement-restart-2026-10-24), which restarts the moved share of a `source_fenced` journal on a new replacement;
  - replay of source-fenced work, beyond that restart.
- **Drain before release is not proven against a real generator.** That the replacement's epoch claim drains its staged assignment before the capacity release is accepted rests on the generator's existing epoch semantics. These tests use fake generators that accept every release.
- **The same-leader journal restore** (`restoreRunEvacuationPriorCheckpoint`) and the abandoned marker are tested directly. Inside `evacuateGenerator`, these have no integration test:
  - the four call sites;
  - the capacity-first ordering;
  - marking the journal abandoned when the capacity is not returned;
  - the deferred retry and restore (`finishRestore`).
- **Some write failures are untested.** A failed `rolling_back` intent write aborts that Phase A attempt, and Phase A is retried. A failed `STOPPING` write in `failRunEvacuationRollback` is retried in the background. The intent write failure has no dedicated test here; the failed `STOPPING` write and its retry are exercised by the roll-forward record's `TestEvacuationRecoveryHoldsUnrecordedCompletion` and `TestInterruptedRunEvacuationHoldRetry`, which share `failRunEvacuationRecovery`.
- **A completed source, or a participant with a completion outside `Prior.CompletedTargets`, fails the rollback and holds the run `STOPPING`.** That happens even when it finished naturally, for example the source after a same-leader rollback, because a controller stop ends the same way on the generator. Updated 2026-10-24: a current generator now reports why a run ended, and a source or survivor whose duration ran out, at no less than the run's durable duration, no longer fails the rollback; it is recorded completed in the restored checkpoint ([generator terminal cause](#roadmap-generator-terminal-cause-2026-10-24)). Other completions still hold the run. Updated 2026-10-25: a source or survivor whose generator still finalizes the archive when the successor claims it is asked again within a bounded wait, and judged on the outcome it then publishes ([evacuation completion](#roadmap-evacuation-completion-2026-10-25)).
- **A failed rollback** leaves the run `STOPPING`, holding its capacity and timeslot, until a newer term fences it or an administrator attests that its generators stopped. That includes an unfenced replacement, a completed source, a participant with an unrecorded completion and an unconfirmed capacity step.

<!-- Original record: docs/qa/reviews/roadmap-evacuation-rollforward-2026-10-24/README.md; local evidence kept outside git -->
<a id="roadmap-evacuation-rollforward-2026-10-24"></a>

## Evacuation roll-forward (REC-A)


Status: implemented behind an opt-in. Qualified locally only. Thirteen review rounds; round 13 found no High or Medium issue, and its three Low items are recorded below, not fixed.

<a id="roadmap-evacuation-rollforward-2026-10-24--contract"></a>
### Contract

- **Opt-in.**
  - `APEXLOAD_EVACUATION_ROLLFORWARD_RECOVERY` (`WithEvacuationRollForwardRecovery`) is off by default.
  - It takes effect only together with `APEXLOAD_EVACUATION_ROLLBACK_RECOVERY` and run adoption.
  - With both settings on, a new evacuation journal is version 5, a superset of version 4:
    - `markRunEvacuationStaged` also records the duration offset, active duration and pause state AssignWork staged each replacement with (`StagedSteadyElapsed`, `StagedRemaining`, `StagedPaused`);
    - a new phase, `replacement_committing`, is written immediately before `CommitWork`. It records `CommittingAt` and `CommitTargetDuration`, the logical duration the replacement ends up with: what the leader sends in `AdjustDuration` right after `CommitWork`, or the staged duration when it sends none. The mark refuses a target longer than the staged duration;
    - `replacement_committed` advances from `replacement_committing`. Versions 3 and 4 still advance from `source_fenced`.
  - Otherwise journals stay version 4 or 3, exactly as before. Their JSON gains no fields.
- **Older peers.**
  - A controller that knows only versions 3 and 4 cannot read a version 5 journal: `decodeRunContinuityCheckpoint` rejects it, and the older evacuation reader does not recognize version 5.
  - Such a controller keeps the run `STOPPING` through its interrupted distributed stop path.
  - Enable the setting only once every replica reads version 5.
- **Commit intent before `CommitWork`.** If the `replacement_committing` write fails, `CommitWork` is never sent and the existing uncertain path stops the evacuation's participants.
- **Eligibility** (`evacuationRollForwardGuard`, `runEvacuationRollForwardPlan`). A run qualifies only when:
  - both settings are on and run adoption is enabled;
  - the run is `RUNNING` or `PAUSED`, with the continue failover policy, on the distributed backend;
  - the run's durable pause state equals `StagedPaused`;
  - the journal passes `interruptedEvacuationTargets`, is version 5, is in phase `replacement_committing` or `rolling_forward`, and is not abandoned;
  - the predecessor's lineage was armed (`Prior.ArmedAt`) and the source is not a prior completed target, as for a committed promotion;
  - no surviving participant's shards changed (`runEvacuationSurvivorsUnchanged`, shared with the rollback plan);
  - the capacity journal passes `validateRunEvacuationCapacityJournal`;
  - `0 < CommitTargetDuration ≤ StagedSteadyElapsed + StagedRemaining`;
  - each evacuated group is still part of the run and passes the workload refusals of `evacuateGenerator`: no iteration termination, no distributed rendezvous, `groupAllowsGeneratorLossReshard` and `groupAllowsStatelessDataReshard`.

  Anything else keeps the fail-closed path, including `source_fenced` journals of every version. A version 5 journal held at `source_fenced` is reconciled `STOPPING` with a reason that says the replacement never received `CommitWork`.
- **Successor roll-forward** (Phase A, `adoptOrphanedRuns`), in order:
  1. The adoption checkpoint is the predecessor's lineage over the next topology, with the fenced source kept for archive collection only, as a committed promotion builds it (`runEvacuationRollForwardCheckpoint`). A `rolling_forward` journal supplies it with the previous successor's intent.
  2. `persistRunEvacuationRollForwardIntent` writes phase `rolling_forward` through the lease-held compare-and-swap before any generator sees the rotated token. It records `RollForwardGeneration`, `RollForwardOwnerID`, `RollForwardStartedAt` and the planned checkpoint (`RollForwardCheckpoint`). `Prior` is unchanged. A failed write returns an error, so Phase A is retried.
  3. The batched epoch claim carries the adoption, with the prior proof chain, to the survivors and the replacement. The source is not claimed: it is durably fenced.
  4. Every replacement must be adopted. `REJECTED` (staged work), `UNKNOWN_TEST`, `ALREADY_STOPPED`, `ALREADY_COMPLETED` and an unreachable replacement fail the roll-forward. The claim that refused the adoption drained a staged assignment. `ALREADY_COMPLETED` is no proof of a commit: a generator ends any stop, including a stop of a staged assignment that never committed, with a completed run, as the rollback already assumes for a completed source. No participant may report a completed run the prior lineage never recorded (`unexpectedRunEvacuationCompletions`, shared with the rollback). A survivor stopped by the lost leader's uncertain path, or by a failed earlier successor, ends the same way, so its completion cannot show its share ran out. Completions recorded in `Prior.CompletedTargets` before the evacuation are trusted as before.
  5. `completeRunEvacuationRollForward` sends `AdjustDuration` with `CommitTargetDuration`, the successor's generation and the rotated token, to each replacement, and requires `Accepted`. The target is absolute, so a repeat is a no-op and an elapsed one ends the replacement's engines at once.
  6. `commitRunEvacuationRollForwardCapacity` checks each journaled replacement reservation against the journal, ignoring only state and owner term. A `pending` row is confirmed by the adopted replacement at the new epoch and then committed under the successor's lease. A row already committed, by the predecessor or by an earlier successor term, is left as it is. `adoptPodCapacity` then confirms and transfers every live and standby reservation of the run.
  7. Only then is the journal replaced by the plain checkpoint, and `installAdoptedRun` resumes the run on the next topology.
- **Failures stay fail-closed.**
  - Any problem before the journal is replaced goes to `failRunEvacuationRollForward`, which shares `failRunEvacuationRecovery` with the rollback:
    - it keeps the `rolling_forward` journal;
    - it first holds the run `STOPPING` with the evacuation-uncertain reason, and installs no runtime, so an administrator can attest;
    - only then does it force-stop each survivor and replacement this term adopted, with the rotated token. If the hold write fails, it is retried in the background and the stops are still sent. A stop that is not acknowledged is added to the held reason through the same hold.
  - Holding first matters because a stopped assignment later reports a completed run: a durable `RUNNING` row must never outlive the stops. If the controller is lost after the hold, a newer term fences what it adopted. This ordering, the one `failClosedUncertainEvacuation` uses, applies to the rollback too.
  - A panic before the swap is handled the same way. A panic while handling a failure is caught by `isolateRunAdoption`, which holds a journaled run. A panic after the swap takes the ordinary adoption-failure path.
  - That term's fail-closed recovery does not fence at or below `RollForwardGeneration`; `successorGeneration` covers both successor paths. A newer term fences the survivors and the replacement, never the durably fenced source, and records the run `FAILED`.
- **The hold and its retry** (`interruptedRunEvacuationHold`).
  - Each run has one hold retry, owned by the hold registry (`TestRunner.evacuationHolds`, `evacuationHoldRetrying`) rather than the terminal-transition retry. It writes the run's latest registered hold until none is left, so neither a later reason of the same hold nor a later current hold for the run, such as one from `isolateRunAdoption`, is lost. Starting and ending the retry are both decided under the registry lock, so a hold registered while a retry ends always gets one.
  - A hold is unregistered once its latest reason is written, once the run is terminal (for example after an administrator attestation) or its journal is gone, when the run is removed, and when the retry ends on shutdown. The retry never loops on a write whose term has ended or whose run is terminal; a claim refused in a current term is retried (see Remaining).
  - The retry does not wait for admission. Every hold is decided inside acquisition reconcile (Phase A), before admission; the terminal-transition retry, which needs admission, exited there before its first attempt.
  - Each hold carries the term passed by the code that decided it, and its single term gate is the fence claim with that term (`claimTestExecutionFenceWithTerm`). A refused claim drops the hold only when that term is no longer this process's term; the next term then decides the run from its journal, and a participant this term stopped reports a completion its prior lineage never recorded, which holds the run. A claim refused while the term is still current, for example by another owner's fence, keeps the hold registered and retries it.
  - Every hold of a run writes under the run's one write lock (`evacuationHoldWriteLock`), shared by all its hold objects, which spans reading the reason and writing it, and writes only while it is still the run's latest registered hold. A retry that read an earlier reason, or a write of a hold a later hold replaced, therefore cannot land after the later write.
  - A hold of an ended term, or of a run object that is no longer registered, never displaces a registered hold of the run (`register`): it could not be written, and a current hold it displaced would then write nothing. It is dropped and logged. Such a hold registers only when the run has no hold, and is then dropped at its own term and object gates.
- **Lease lost and won again by the same process.**
  - **Reload replaces lost-term objects.** A reload replaces any active run object that has no live runtime and is still fenced to a lease term this process lost (`lostTermExecutionFence`), instead of reusing it, and restores its runtime as for any new object. Teardown after a lost lease leaves such objects behind, and a goroutine of the lost term may still hold one; it must never write under the new term once the new term claims the run. The fresh object is claimed normally by Phase A, a hold, the fence recovery or an attestation, while the terminal writers require the registered object for the row, so a lost-term writer writes no row. A terminal run needs no fence and keeps its object, so a lease change does not re-hydrate every finished run.
  - **Phase A clearing.** Phase A also clears a stale fence on every run it selects, which has no live runtime, and leaves the fence empty while it claims the generators without holding the runner lock; the first hold or install then claims the term. Unlike `installRunAdoptionHandlesLocked`, it claims nothing itself. Because the reload before Phase A already replaced lost-term objects, no lost-term goroutine holds a selected object; the clearing only matters for an object that did not pass through that reload. Any other caller of `claimTestExecutionFence` during that window is an API or admission-time operation, and the replica is not admitted during Phase A.
  - **Reload keeps a newer hold.** A reload that read the row before a hold wrote `STOPPING` keeps the newer in-memory hold when the row is `RUNNING` or `PAUSED` under the same journal, as it already does for a pending stop proof, so the run stays attestable. Only a hold that `persist` wrote in the current term qualifies (`evacuationHoldOwner`, stamped after the durable write). A `STOPPING` set in memory without such a write, for example by an uncertain evacuation whose save failed, follows the row. In its own term that uncertain path keeps its runtime handles, which the existing pending-stop-proof rule covers, so a run object like this is only met after a lease change, where the next term's Phase A decides it.
- **Live-table finalization** (`finishLiveTables` for terminal writers, `finalizeRecoveredLiveTables` for tables a restore found already closing). Live tables are keyed by run ID, so a writer of another term must never finalize them. In order:
  1. **Owner check** (`liveTablesFinalizableBy`, terminal writers only). A writer with a fence finalizes only tables whose term owner equals that fence; this is what lets a writer whose object a same-term reload replaced still finalize them, since no other writer will. A writer with no fence must hold the registered object.
  2. **Ended term gives up.** Tables whose term is no longer this process's term can never be finalized here again, so the finalizer (`liveTableFinalizer`) returns at once, and so does any retry (the recovery's first attempt instead fails at `finalizeRunLiveTables`' term check, and its retry ends). It also returns once the run's tables were replaced.
  3. **Finalize the checked tables** (`finalizeRunLiveTables`), never whatever a later lookup by run ID finds, so tables a newer term restores meanwhile are never touched.
  4. **Retry until replaced.** A failure is retried under a key of run and term owner (`liveTableRetryKey`), so a retry left by an earlier term never takes the current term's slot. The retry repeats steps 2 and 3.

  The owner check covers every terminal writer, including local runs' artifact finalization. A recovery retry queued during Phase A usually exits before its first attempt, because the terminal-transition retry needs admission; that predates this slice.
- **A new term after `rolling_forward`** rolls forward again from the recorded intent. Its adoption carries the earlier term's token proof, the duration adjustment is repeated, and reservations the earlier term committed or transferred are accepted.
- **Rollback accepts version 5.** `runEvacuationRollbackPlan`, the same-leader journal restore and the abandoned marker accept version 4 or 5 journals in the phases they did before. The shared failure handling, the unrecorded-completion rule, the hold and its retry, and the reload rules above apply to rollback holds too.
- **No storage change.** `TransitionPodCapacity` changes only a row's state, never its owner term (`pkg/storage/pod_capacity.go`). A pending replacement row committed by the successor therefore keeps the predecessor's term, and the existing `AdoptPodCapacity` transfers it. No current-term exemption was needed.

<a id="roadmap-evacuation-rollforward-2026-10-24--invariants"></a>
### Invariants

- **Old and replacement never run concurrently.** The source was positively fenced before `replacement_committing` was written, it is never adopted, and it is excluded from every later participant set. A late predecessor write fails the lease-held compare-and-swap, and generators refuse its generation and token once the successor adopted the run.
- **No replayed initialization.** Adoption moves existing executions only. A replacement that never committed is drained, not restarted. Updated 2026-10-24: with `APEXLOAD_EVACUATION_REPLACEMENT_RESTART`, its share is then restarted fresh on another generator ([replacement restart](#roadmap-evacuation-replacement-restart-2026-10-24)); adoption itself still never restarts work.
- **No resurrected stopped run.** A `STOPPING` run is not eligible.
- **No extended duration.** The successor sends the journaled target, which never exceeds the staged duration, before the journal is replaced.
- **Pause is preserved.** The durable pause state must equal the staged one.

<a id="roadmap-evacuation-rollforward-2026-10-24--validation"></a>
### Validation

- **`TestEvacuationJournalVersionFiveFollowsRollForwardRecovery`** (`pkg/api`): version 3 with both settings off and with roll-forward alone, version 4 with rollback alone, version 5 with both. Only version 5 records the staged timing and the commit intent; older journals carry none of the new fields and skip the intent. The intent refuses a zero or longer target, and version 5 cannot be committed without it. `decodeRunContinuityCheckpoint` rejects the journal. That an older release's evacuation reader does not recognize version 5 follows from its fixed version list and is not tested here.
- **`TestVersionFiveJournalKeepsRollbackBehavior`**: a version 5 `prepared` or `staged` journal is rolled back by a successor, and its leader marks it abandoned and restores the prior checkpoint, as for version 4.
- **`TestEvacuationRollForwardAdoptsCommittedReplacement`** covers an ordinary replacement and an activated standby, a pending and a committed reservation, and a running and a paused run:
  - reconcile leaves the status unchanged, and so does the roll-forward;
  - the survivor and the replacement are adopted from the old term, and the source is neither claimed nor a participant but keeps its archive transport;
  - the replacement receives one `AdjustDuration` with the journaled target, the new term and the rotated token; the survivor's duration is not touched;
  - the durable checkpoint is the next topology under the new term, with the source archived;
  - the replacement and survivor capacity is committed under the new term, and the source's `releasing` row is unchanged;
  - a pending replacement row is confirmed before it is committed and again when transferred; a committed one only when transferred.
- **`TestEvacuationRollForwardHoldsUncommittedReplacement`**: `REJECTED`, `UNKNOWN_TEST`, `ALREADY_STOPPED` and an unreachable replacement keep the `rolling_forward` journal and hold the run `STOPPING` for attestation; the adopted survivor is force-stopped at the new term with the rotated token, the replacement receives no duration or capacity request, and its reservation stays pending under the old term.
- **`TestEvacuationRollForwardHoldsCompletedReplacement`** covers both ways a never-committed or stopped replacement reports a completed run:
  - the predecessor's uncertain path stopped the still-staged replacement after its commit intent: the run is held, the replacement gets no duration or capacity request, and its reservation stays pending;
  - a first successor failed after adopting, could not write its hold, and still stopped everything it adopted, leaving a `RUNNING` row under `rolling_forward`: the next term sees every target completed and holds the run instead of finishing it.
- **`TestEvacuationRecoveryHoldsUnrecordedCompletion`**, for roll-forward and rollback:
  - a survivor the lost leader stopped reports a completed run while the other participant is adopted: the run is held, and the adopted participant is force-stopped with the rotated token;
  - a first successor fails, cannot write its hold, stops the survivor and loses the other participant's stop: the next term holds the run instead of continuing without the survivor's shard;
  - a survivor recorded completed before the evacuation is trusted, and the run rolls forward or back as before.
- **`TestInterruptedRunEvacuationHoldRetry`**:
  - when both hold writes failed, the queued retry writes the later reason with the unacknowledged stop;
  - when a hold of an ended term occupies the run's retry slot, the retry writes the newer term's hold instead;
  - an ended term's hold alone is dropped, not written;
  - a hold that failed during acquisition reconcile, before admission, is written once the store recovers, while the replica is still reconciling;
  - when a later hold of the run is made while the retry writes an earlier hold, the later hold waits for the run's write lock and its reason ends durable and in memory;
  - a write of a hold that a later, already written hold replaced writes nothing;
  - a stale hold made after the current term's failed hold, with an ended term, the run's old object, or both, does not displace it, and the current reason ends durable and in memory;
  - when the hold gains a later reason while the retry writes an earlier one, the retry goes on until it has written the later one;
  - a claim refused while the term is current, by another owner's fence, keeps the hold registered and the run `RUNNING` until the fence is released, then writes it;
  - a retry ends once the run is terminal, its journal is gone, its hold was forgotten (`forgetEvacuationHold`) or the runner is shutting down, even while writes still fail.
- **`TestFailedEvacuationRecoveryHoldsBeforeStopping`**: for a failed roll-forward and a failed rollback, the durable status is already `STOPPING` when the first force stop is sent. Its stale-fence cases set the fence on the fixture's object directly; in production the reload replaces such an object first, so these cases cover the defensive clearing in Phase A. Moving the hold after the stops fails only this test.
- **`TestReloadKeepsANewerEvacuationHold`**:
  - a reload whose listing still shows the held run `RUNNING` or `PAUSED` under a roll-forward journal, or `RUNNING` under a rollback journal, each a candidate itself, keeps the in-memory `STOPPING` hold of this term on the same object, and an attestation then succeeds;
  - a row under another journal wins;
  - a `STOPPING` never written in this term, as left by an uncertain evacuation whose save failed, follows the row;
  - so does that `STOPPING` with a hold whose own write failed: the hold stamps the run only after its durable write;
  - a terminal run object fenced to a lost term is reused, not replaced;
  - the same left by a lost term is replaced with the row's `RUNNING` run, restored with its runtime (group specifications from the stored compiled scripts), and Phase A then holds it, because the participants report completions, and an attestation succeeds.
- **`TestLostTermRunRollsForwardAfterReloadAndReconcile`**: in production order, `reloadAndReconcile` (with its active-only reload) replaces an object fenced to a lost term, restores its runtime, and the run then rolls forward normally.
- **`TestTerminalWriterFinalizesOnlyItsTermsLiveTables`**:
  - a run whose object a same-term reload replaced still finalizes its live tables on its own terminal write;
  - a writer of an ended term neither finalizes its tables nor leaves a retry, and leaves tables a newer term restored untouched;
  - when a newer term takes the lease and restores the run's tables between the ownership check and the finalization (a test barrier, `afterLiveTableFinalizeCheck`), neither the checked tables of the ended term nor the newer term's are finalized, and the ended term's retry ends;
  - a retry of an earlier term holding its own slot does not drop the current term's retry, and the current retry ends once the tables are replaced. The test first asserts that the two terms' keys differ.
- **`TestRecoveredLiveTablesFinalization`**: a failed recovery finalization is retried under its term key and ends without touching the tables that replaced it. For an ended term, the first recovery attempt fails with "owner expired" and leaves the tables open, and its retry ends instead of failing on the expired owner every second.
- **`TestHeldRunAfterTheLeaseIsWonAgain`**: a hold written in one term, after the runner gets a new leadership object for a newer term (per-term fence tokens, as a lease won again) and reloads, is attested, or fenced by the newer term's recovery, which records the run `FAILED`. A lost-term writer still holding the old object changes neither the run's row nor its live tables, and the attested run's own terminal write then finalizes them.
- **`TestEvacuationRollForwardKeepsFailClosedPaths`**: version 3, 4 and 5 `source_fenced` journals, an abandoned journal, `STOPPING`, the fail policy, either setting off, a pause or resume since staging, a stateful-protocol, distributed-rendezvous, cursor-stateful-data or unknown evacuated group, and iteration termination. None is a candidate; each reconciles `STOPPING` with its journal unchanged and no generator claim. The version 5 `source_fenced` reason says the replacement never committed.
- **`TestEvacuationRollForwardPlanRejectsUnsafeJournals`**: version 4, a journal without a commit intent, an abandoned journal, an unarmed lineage, a completed source, a changed survivor, an invalid capacity claim, and a missing, longer, zero-remainder or negative-offset duration.
- **`TestRollingForwardJournalRejectsContradictions`**: a `replacement_committing` journal's participants exclude the source. Each contradiction in a `replacement_committing` or `rolling_forward` journal is refused by both the fail-closed recovery and the roll-forward plan: the version, the commit intent, the source fence, a replacement commit, the staged targets, a successor term in the wrong phase, the roll-forward term, owner and start, a rollback term, and the planned checkpoint's topology, archived source, lineage owner and term, token and newest intent. A `rolling_forward` journal is never rolled back.
- **`TestEvacuationRollForwardInterruptionBoundaries`**: a controller lost before `CommitWork` (the replacement is still staged) is held; one lost after `CommitWork` and before the capacity commit, after the capacity commit and before `AdjustDuration`, or after `AdjustDuration` and before the committed mark is rolled forward, repeating the adjustment where it had already been applied.
- **`TestEvacuationRollForwardFailuresKeepJournal`**: a rejected duration adjustment, an unconfirmed capacity request, a changed replacement row, a pending row from another term, a committed row from an older term than the journal's, a failed journal swap and a panic before the swap each keep the journal and hold the run `STOPPING`, with the adopted survivor and replacement force-stopped. Only the failed swap happens after the capacity was transferred.
- **`TestCompleteRunEvacuationRollForwardRequiresTheReplacement`**: the completion step itself refuses a replacement this term did not adopt and leaves the journal and the capacity unchanged.
- **`TestCommitRunEvacuationRollForwardCapacityRefusesUnprovableRows`**: a replacement holding a reservation the journal never recorded, a replacement that cannot confirm capacity, a missing capacity ledger and a lost reservation are each refused before any capacity request.
- **`TestEvacuationRollForwardUnusableNextCheckpointHoldsRun`**: a next checkpoint whose adoption intent cannot be planned holds the run `STOPPING` with its journal unchanged, and no generator is claimed.
- **`TestRollingForwardJournalWithTheSettingOffIsHeld`**: a `rolling_forward` journal reaching a term with the setting off is no candidate, reconciles `STOPPING`, and that term fences the survivor and the replacement, never the source, and records the run `FAILED`.
- **`TestVersionFiveCommittedEvacuationIsPromoted`**: a version 5 `replacement_committed` journal is promoted and its survivor and replacement adopted, with no further duration change, as for versions 3 and 4.
- **`TestEvacuationRollForwardIntentWriteFailureRetries`**: a failed `rolling_forward` write returns an error before any generator is claimed and leaves the journal and the `RUNNING` row unchanged; the same term's retry rolls forward.
- **`TestEvacuationRollForwardFailurePanicHoldsRun`**: a panic while force-stopping after a failed roll-forward still holds the run `STOPPING` under its `rolling_forward` journal.
- **`TestEvacuationRollForwardPanicAfterSwapIsOrdinaryFailure`**: a panic after the journal was replaced takes the ordinary adoption-failure path, with no evacuation-uncertain hold and no force stop.
- **`TestEvacuationRollForwardResumesAfterAnotherLeaderChange`**: a successor lost right after its intent, or after its claim, duration adjustment and capacity transfer, leaves the run `RUNNING`. The next term rolls it forward with the first term's token proof, repeats the adjustment, and ends with the capacity under the newer term.
- **`TestFailedRollForwardIsNotFencedInItsTerm`**: no same-term recovery is queued, and a newer term fences the survivor and the replacement, never the source, and records the run `FAILED`.
- **`TestEvacuationRollForwardRefusesLateOldOwner`**: inside the window between the successor's `rolling_forward` intent and its journal swap (during the epoch claim), and again after the roll-forward, the predecessor's committed mark, a raw journal write under its lease and a capacity commit under its lease all fail, and nothing changes. The committed mark is refused because the journal changed; the raw write and the capacity commit are refused by the lease.
- **`TestEvacuationRollForwardAgainstRealGenerator`** (in-process generator service for the replacement):
  - a staged replacement is refused, drained by the epoch claim, and no longer accepts a duration change;
  - a staged replacement the predecessor stopped reports a completed run to the successor's claim, and the run is held;
  - a committed replacement is adopted, refuses the predecessor's generation and token, keeps running, and accepts the journaled duration again with the rotated token;
  - a loss-tolerant replacement orphaned by a lapsed epoch, whose per-run lease the generator cleared, is rolled forward the same way: the duration adjustment does not need that lease. The old epoch's TTL (2 s) covers the setup, and the test then polls until the generator refuses the old generation as expired instead of sleeping. The orphan transition itself is not observed directly.
- **`TestEvacuationRollForwardExpiredDeadlineCompletes`** (in-process generator): a journaled target that already elapsed for the replacement still rolls forward, and the replacement's engines end at once.
- **`TestDistributedEvacuationRollForwardJournal`** drives a real version 5 evacuation through `evacuateGenerator` against an in-process generator:
  - `CommitWork` and the duration adjustment observe a durable `replacement_committing` journal with the paused staged timing, and the adjustment equals the journaled target;
  - a failed `replacement_committing` write never sends `CommitWork`;
  - a failed committed mark after `CommitWork` takes the uncertain path.
- **Existing tests** keep passing: the rollback suite's same-term check now matches the shared "recovery generation" wording, and its force stops are now checked for the rotated token.
- **Mutation passes.** Twenty mutations were each caught after round 1:
  - the first pass removed eleven checks (pause, workload, capacity confirmation, armed lineage, row identity, replacement adoption, capacity transfer, pending row term, planned checkpoint validation, the shared successor term, the intent's duration bound). The two that first survived gained `TestCompleteRunEvacuationRollForwardRequiresTheReplacement` and the "pending row from another term" case;
  - after round 1, those eleven were re-run, plus the rendezvous and data refusals, the unrecorded and lost reservation checks, the next-checkpoint hold, the completed-replacement refusal, the committed-row lower bound, removal of the hold, and moving the hold after the stops.

  Not mutated separately: the error branch of the rendezvous inspection, and the version check in `runEvacuationReplacementNeverCommitted`.

  After round 2, six more mutations, applied with `go test -overlay` rather than by editing source, were each caught: the unrecorded-completion check in the roll-forward and in the rollback, the hold retry's terminal check, its journal check, a second hold that does not share the first, and the roll-forward's not-adopted check.

  After round 3, three more were caught: a retry that writes its own hold rather than the run's latest, a term check that needs both owner and generation to change, and the not-adopted check again. The per-hold write lock is not covered by a test: the interleaving it prevents is not reproducible without a scheduling hook (covered since round 11, when the per-run write lock replaced it; see below).

  After round 4, four more were caught: a retry that waits for admission, a retry that ends after writing an earlier reason, dropping both the term check and the term-bound fence claim, and the not-adopted check. `completeRunEvacuationRollForward` now never sends a duration change to a replacement without an adopted client, so `TestCompleteRunEvacuationRollForwardRequiresTheReplacement` fails that last mutation on its problem-text assertion, without a panic.

  After round 5 the separate term pre-check is gone; the term-bound claim is the one gate. Four more were caught: Phase A not clearing a stale fence, a refused claim that always drops the hold, one that never drops it, and the reload not keeping the newer hold. Switching the claim to `claimTestExecutionFence` is also caught, by the "hold of an ended term" subtest.

  After round 6, five more were caught: a reload that keeps an unwritten `STOPPING` (no term stamp check), one that ignores the journal, one that keeps the hold only for a `RUNNING` listing, one that reuses a lost-term object, and the reload not keeping the newer hold at all.

  After round 7, five more were caught: never stamping the hold, stamping it before its write, skipping the pre-check that replaces a lost-term object (the write-lock re-check still replaced it, but without restoring its runtime), finalizing live tables for an unregistered object, and replacing a terminal run.

  After round 8, five more were caught: no live-table owner check, no replaced-tables check in the retry, a registration-only check (the round-7 behavior), never finalizing, and skipping the reload pre-check under `reloadAndReconcile`. The no-fence branch of `liveTablesFinalizableBy` has no dedicated test: replacing it with always-true or always-false survives.

  After round 9, three more were caught: finalizing by a fresh lookup instead of the checked tables, a per-run retry key, and an ended term's writer that still tries. Dropping the retry's replaced-tables check is also caught.

  After round 10, five more were caught, pinning the recovery path: a per-run recovery key, a recovery retry that looks the tables up again, no recovery retry, and an ended term that does not give up are each caught, as is a terminal writer that looks the tables up again (now against the newer-term race). The first recovery attempt, made right after the restore registered the tables, is equivalent to a lookup and is not mutated.

  After round 11, two more were caught: a hold write without the run's shared write lock, and a superseded hold that still writes.

  After round 12, three more were caught: a stale hold that always registers, and a staleness check that looks only at the term or only at the run object.

<a id="roadmap-evacuation-rollforward-2026-10-24--review"></a>
### Review

Round 1 found two High safety issues, five Medium test and docs issues, and four Low ones. All are fixed:
- **High: a completed replacement was accepted as committed.** On the generator, any controller stop ends in a completed run, including a stop of a staged assignment that never committed, and adoption then reports `ALREADY_COMPLETED`. Two paths reached it: the predecessor's uncertain path stopping a still-staged replacement after its commit intent, and a failed successor whose hold write failed after it stopped everything it adopted, leaving a `RUNNING` row. A later successor rolled forward with the shard missing, or reported a stopped run as running. Fixed: every replacement must be adopted, and the commit of a completed replacement's capacity without confirmation is gone. `TestEvacuationRollForwardHoldsCompletedReplacement` covers both paths, and the real-generator test shows a stopped staged replacement reporting a completed run.
- **High: force stops preceded the hold.** `failRunEvacuationRecovery`, shared with the rollback, stopped the adopted assignments before writing `STOPPING`. Fixed: the hold is written first, as `failClosedUncertainEvacuation` does, and the stops are sent even if it fails. `TestFailedEvacuationRecoveryHoldsBeforeStopping` checks the durable status at the first stop for both paths.
- **Medium: surviving mutations and missing cases.**
  - The rendezvous and cursor-stateful data refusals had no test: two fail-closed cases were added.
  - Capacity-ledger refusals were untested, including an unrecorded reservation whose check survived deletion: `TestCommitRunEvacuationRollForwardCapacityRefusesUnprovableRows`.
  - The unusable-next-checkpoint hold survived deletion: `TestEvacuationRollForwardUnusableNextCheckpointHoldsRun`.
  - Force stops were not checked for the rotated token: the fake records each stop's token, and both hold assertions require the token of the adopting claim.
  - A `rolling_forward` journal with the setting off and a version 5 committed promotion were untested: `TestRollingForwardJournalWithTheSettingOffIsHeld` and `TestVersionFiveCommittedEvacuationIsPromoted`. Remaining and the spec now list the REC-A exit cases still unexercised at this phase.
- **Low.**
  - A never-failing version check was removed, and the claim about older readers narrowed to what is tested.
  - The late old owner is now also tried inside the window between the intent and the swap.
  - The loss-tolerant subtest no longer races its setup against a 400 ms lapse: it uses a 2 s TTL and polls for the lapsed generation.
  - The committed-row lower bound has its own case.

After the fixes, every mutation in the Validation list is caught.

Round 2 found one High safety issue, one Low code issue, and test and docs gaps. All are fixed:
- **High: a stopped survivor reported a completed run, and the recovery continued without its shard.** Every controller stop ends in a completed run on the generator, so a survivor stopped by the lost leader's uncertain path, or force-stopped by a failed earlier successor whose hold write failed, looked finished. Roll-forward and rollback then completed `RUNNING` without its load, and its reservation was skipped. Fixed: both now fail closed on any participant that completed outside the prior lineage's recorded completions. `TestEvacuationRecoveryHoldsUnrecordedCompletion` covers both paths for both recoveries, plus a genuinely prior-completed survivor.
- **Low: the second hold could be lost or loop.** A retry queued for the first hold write dropped the second hold, losing the unacknowledged-stop reason, and a retry kept failing on a fenced write after an attestation. Fixed: both writes go through one hold whose retry writes the latest reason, and a retry ends once the run is terminal or its journal is gone (`TestInterruptedRunEvacuationHoldRetry`).
- **Docs.**
  - The rollback record now describes hold-first, with a dated note; its untested-write item points to the roll-forward tests.
  - The doc comments now say the run is held, then force-stopped.
  - Remaining and the spec list rolling-version refusal and pause across a second leader change.
  - The node-failure plan notes that a completed replacement proves no commit.
  - The held-reason claim is now true.
- **Tests.**
  - A data race in the failed-successor test (a background hold retry against the fixture's leadership swap) is gone: the fault store uses an atomic flag, and the test waits for the retry to end before the lease changes hands.
  - The claim-window hook no longer calls `t.Fatal` off the test goroutine.

Round 3 found no High or code Medium issue. Its Low safety findings and docs and test issues are fixed:
- **Low: a retry could overwrite a later reason.** `persist` read the reason once before writing, so a queued retry could overwrite the second hold's direct write. Fixed: a per-hold write lock spans the read and the write.
- **Low: one retry slot per run, not per hold.** A second hold object for the run, from `isolateRunAdoption` or from a newer term in the same process while an older term's retry still held the slot, lost its retry. Fixed: the retry writes the run's latest registered hold, and a hold is bound to its term and dropped once that term ends. Covered by the two new `TestInterruptedRunEvacuationHoldRetry` subtests.
- **Docs.**
  - The review status, the background-retry test reference and the mutation counts are corrected.
  - The node-failure plan now says a completed replacement does not prove a commit, and that any unrecorded completion holds the run.
  - The rollback record adds the unrecorded-completion rule to its steps and Remaining.
  - Remaining here describes the completion ambiguity precisely and merges the untested exit cases into one list.
  - The README entry notes that a survivor finishing during failover holds the run.
- **Tests.**
  - The not-adopted completion test asserts rather than panics.
  - The rollback branch of `TestEvacuationRecoveryHoldsUnrecordedCompletion` counts stops at the new term, checks the completed survivor got none, and checks attestation eligibility and the same-term recovery refusal.

Round 4 found one High issue and five Low ones, all fixed:
- **High: the hold retry never ran in production.** It was queued on the terminal-transition retry, which runs only while the replica is admitted. Every hold is decided in Phase A, inside acquisition reconcile, so the retry exited before its first attempt, and a run whose hold write failed stayed `RUNNING` with force-stopped participants until a leader change. Fixed: the hold registry owns a per-run retry that does not wait for admission and is gated on the hold's own term. The "during acquisition reconcile" subtest of `TestInterruptedRunEvacuationHoldRetry` covers it. The other retries queued around adoption, for adopted-run stop proofs (`adoption-resume-stop:`, `adoption-stop:`) and for interrupted-evacuation fences (`evacuation-guard:`), are queued from `resumeAdoptedRuns` in the admission callback, after reconcile, so they do not share the cause.
- **Low.**
  - The hold's term is now passed in by the code that decided it, not read at creation, so an unreadable claim can no longer disable the term check.
  - The fence is claimed with the hold's term, not the current one. (Round 5 narrowed the drop on a refused claim to an ended term.)
  - A registry entry is forgotten when the retry ends, and when a run is removed (`tests.go`, `persistence.go`, `timeslot_reservation.go`).
  - A retry that wrote an earlier reason while a later one arrived now goes on: the registry owns the retry and it ends only once no hold is left (the "reason changed during a retry write" subtest).
  - Docs: the mutation note no longer relies on a panic; the rollback record says a completion outside the prior lineage fails the rollback and holds the run; the README entry matches the record's completion wording.

Round 5 found one High issue, which both reviewers reproduced, and six Low ones. All are fixed:
- **High: a hold was dropped after the process lost and won its lease again.** Teardown after a lost lease leaves the run's object fenced to the old term, the reload keeps that object for a `RUNNING` row, and the hold is written before adoption installs the run. The term-bound claim was refused by that stale fence, and every refused claim dropped the hold, so the run stayed `RUNNING` with its participants stopped, and could not be attested. Fixed: Phase A clears the stale fence of every run it selects, and a refused claim drops the hold only once its term has ended. The stale-fence cases of `TestFailedEvacuationRecoveryHoldsBeforeStopping` and the "claim refused in a current term" subtest cover both.
- **Low.**
  - A reload racing the hold retry could put the in-memory status back to `RUNNING`; it now keeps the newer hold (`TestReloadKeepsANewerEvacuationHold`).
  - The shutdown exit of the retry has a subtest.
  - The "removed run" subtest is renamed "forgotten hold", and Remaining explains why the removal call sites only free the entry sooner.
  - The term check is now a single gate, and the mutation notes say what stays untested.
  - Docs: the review status and the round-4 mutation note are corrected.

Round 6 found two High issues, which the reviewer probed, and seven Low ones. All are fixed or recorded:
- **High: the reload's hold rule applied to a `STOPPING` never written.** An uncertain evacuation sets `STOPPING` in memory before its save; when the save failed and the lease was lost and won again, the reload kept that `STOPPING` over the `RUNNING` row, Phase A skipped the run, and its stale fence blocked attestation and the fence recovery. Fixed: only a hold written by `persist` in the current term is kept (`evacuationHoldOwner`), and a lost-term object is replaced.
- **High: a held run could not be attested after the lease was won again.** The reload reused the object still fenced to the lost term, and attestation and the fence recovery claim with the current term, which a stale fence refuses. Fixed: the reload replaces any run object with no runtime that is fenced to a lost term (`TestHeldRunAfterTheLeaseIsWonAgain`).
- **Low.**
  - Lost-term writers: the replacement keeps them off the new object; the generic terminal writer is recorded under Remaining.
  - Docs: the mutation note, the retry contract, the bounds of a refused-claim retry, the merged hold-before-stop bullet and the Phase A clearing are corrected.
  - The reload rule's journal and `PAUSED` conditions have tests.

Round 7 found no High or Medium issue. Its Low findings are fixed:
- **Live tables.** A lost-term writer could finalize the registered run's live tables, which are keyed by run ID, before the terminal writer's pointer check. The check now comes first (`TestHeldRunAfterTheLeaseIsWonAgain`).
- **Restore after replacement.** Skipping the replacement pre-check went unnoticed, because the fixture's groups had no stored scripts. They now do, and the "lost term" case checks the runtime was restored.
- **Stamp after the write.** A new "hold write failed" case checks the stamp is set only by a durable write.
- **Wording.** "Phase A decides" now says when that happens, and the stale-fence and new-leadership wording is precise.
- **Production order.** `TestLostTermRunRollsForwardAfterReloadAndReconcile` runs the replacement through `reloadAndReconcile`.
- **Terminal runs.** The replacement now excludes terminal runs, so a lease change no longer re-hydrates every finished run; a case checks a terminal run's object is reused.

Round 8 found no High issue, one Medium test gap and three Low findings. All are fixed:
- **Medium: nothing asserted that a registered run finalizes its live tables.** The attestation subtest now asserts it.
- **Low.**
  - **Regression.** The round-7 registration check skipped live-table finalization when a same-term reload had replaced the finishing run's object, leaving its tables attached. The check now lives in `finishLiveTables`, compares the writer's fence with the tables' term owner, and also guards its retry and local runs.
  - **Restore check.** The production-order test now asserts the runtime restore.
  - **Rollback.** The rollback record notes that the reload and replacement rules also apply to rollback holds, and a rollback case was added to the reload test.

Round 9 found no High issue, one Medium race, which was reproduced, and four Low findings. All are fixed or recorded:
- **Medium: finalization looked the tables up again.** `finishLiveTables` checked ownership on the tables it found, then `finalizeLiveTables` looked them up again by run ID. Tables a newer term restored in between were finalized despite the check. Fixed: `finalizeRunLiveTables` finalizes the checked tables; the test barrier case reproduces the race.
- **Low.**
  - The ended-term live-table case now asserts that no retry is left; such a writer gives up at once.
  - Live-table retries are keyed by run and term owner, so an earlier term's retry no longer drops the current one's.
  - Tables the new term never restores are recorded under Remaining.
  - Docs: the mutation paragraphs are in order, the round-8 count matches, and the reload test says "a candidate" for both journals.

Round 10 found no High or Medium issue. Its Low findings are fixed:
- **Recovery retry.** The recovery finalization's retry now shares the terminal writer's finalizer (`liveTableFinalizer`), so it ends for an ended term or replaced tables and finalizes exactly the tables it was given. `finalizeRecoveredLiveTables` is tested directly.
- **Race test.** The race test now moves the lease to a newer term at its barrier, as in production.
- **Retry keys.** The earlier-term retry test first checks the two terms' keys differ.
- **Contract.** The Contract is reorganized: failures, the hold and its retry, lease lost and won again, and live-table finalization in its step order. The rollback bullet no longer says rollback is unchanged.

Round 11 found no High or Medium issue. Its Low findings are fixed:
- **Safety: a queued retry of an earlier hold could overwrite a later hold of the run.** The write lock belonged to each hold object, so holds of one run were not serialized: a later hold, for example from `isolateRunAdoption`, could be written and unregistered while the earlier hold's retry write was in flight, which then landed with the older reason. Fixed: one write lock per run, and a hold writes only while it is the run's latest. Two new `TestInterruptedRunEvacuationHoldRetry` cases cover both.
- **Test.** The ended-term recovery case now asserts the owner-expired refusal and that the tables stay open.
- **Docs.** The mutation paragraphs are in order with counts, and the live-table steps are precise.

Round 12: the safety reviewer found nothing. The tests and docs reviewer found one Medium issue, which it verified with a scratch test, and two Low ones. All are fixed:
- **Medium: a current hold was lost to a later stale hold.** A hold registered unconditionally by run ID, and since round 11 only the latest hold writes. When the current term's hold failed to write, a lost-term goroutine still holding the run's old object could register its own hold, and the current hold then wrote nothing, while the stale one was dropped at its own gates. The run stayed `RUNNING` with its participants force-stopped. Fixed: a hold of an ended term or of an unregistered run object never displaces a registered hold (`register`). Covered by the new "stale hold after the current hold" cases.
- **Low.** The round-3 mutation note now points to the round-11 coverage, and the recovered live-table entry says what the ended term's first attempt does.

Round 13 was the last cycle: by agreement, review stopped once a round found no High or Medium issue. Neither reviewer did. The safety reviewer confirmed the registration rule never refuses or drops a current hold (a term that reads ended never reads current again, and a reload never leaves the run briefly unregistered) and that the check-and-swap loop cannot livelock. Three Low items are left open:
- The refusal log always says the current term's hold stands, even when the registered hold is itself stale (`register`).
- Two branches of `register` are not pinned by a test: refusing a stale hold only when another hold exists, and allowing a re-hold of the same object. Both are harmless (a stale hold that registers is dropped at its own gates), and the loop's retry branch needs a scheduling hook to test.
- The Contract's "registers only when the run has no hold" omits that a re-hold of the same hold object, as in `failRunEvacuationRecovery`'s second hold, registers whatever its term; read it as "no other hold".

<a id="roadmap-evacuation-rollforward-2026-10-24--remaining"></a>
### Remaining

- **Review.** Rounds 1 to 13 have run; see Review. Round 13's Low items stay open (below).
- **Live tables the new term never restores.** Phase A restores a run's live tables only for an adoption candidate. A run the new term holds fail-closed, or whose checkpoint it cannot decode, keeps the lost term's in-memory tables registered: the new term's terminal writer for it (attestation or fence recovery) is refused by the owner check, nothing finalizes those tables durably, and their memory and budget stay counted until the process restarts. Neither a terminal writer nor a recovery retry of an ended term retries them any more. Durable finalization of live tables for runs not adopted after a failover predates this slice; it is not addressed here. Resolved on 2026-10-25 by the [evacuation completion slice](#roadmap-evacuation-completion-2026-10-25): a new term releases the lost term's tables from memory and budget as it acquires the lease, and its terminal writer for such a run restores the durable tables under its own term and finalizes them. Tables kept only in memory are released, not finalized.
- **Deployed qualification.** REC-A's exit still requires failure injection on a deployed cluster at every journal boundary, worker or node partition, late old-owner return and independent target counters. These tests use fake and in-process generators.
- **REC-A exit cases untested at this phase.** Updated 2026-10-25: items marked resolved are exercised locally by the [evacuation completion slice](#roadmap-evacuation-completion-2026-10-25).
  - a capability mismatch between the replacement and the run (resolved 2026-10-25);
  - no spare capacity at the `replacement_committing` or `rolling_forward` phase (resolved 2026-10-25);
  - the run's own deadline elapsing before the successor on a deployed cluster (exercised only with fake generators in the `staged`, `rolling_back`, `replacement_committing` and `rolling_forward` phases and an in-process replacement, not the `prepared` phase or an in-process survivor or source, since the [generator terminal cause slice](#roadmap-generator-terminal-cause-2026-10-24));
  - a stop requested while the roll-forward runs (resolved 2026-10-25: refused until admission opens, then it owns the installed run);
  - a worker or node partition;
  - rolling-version refusal by older controllers during an upgrade (resolved 2026-10-25, simulated by lowering the journal version checks rather than by running an older release);
  - pause at the `rolling_forward` phase across a second leader change (resolved 2026-10-25).
- **Not covered.**
  - a restart of a missing replacement for the eligible HTTP workload. Updated 2026-10-24: covered behind `APEXLOAD_EVACUATION_REPLACEMENT_RESTART` by the [replacement restart slice](#roadmap-evacuation-replacement-restart-2026-10-24); a roll-forward whose replacement was not adopted and provably runs nothing then restarts its share instead of holding the run;
  - replay of work whose replacement never committed: such a run is held `STOPPING` unless that restart is enabled and its proofs hold.
- **Duration during the gap.** Between `CommitWork` and the successor's adjustment, the replacement runs on its staged duration. In its own logical clock it never exceeds the configured duration, but if the gap outlasts the fleet deadline it can run up to the staging time past it before the adjustment ends it.
- **Completion is ambiguous.** Resolved on 2026-10-24 for causes reported by current generators by the [generator terminal cause slice](#roadmap-generator-terminal-cause-2026-10-24): a participant whose generator reports that its duration ran out, at no less than the durable expectation, no longer holds the run, and a naturally completed replacement rolls forward. A completion from an older generator, an older archive or a pruned tombstone, a controller stop, executions that ended early, or a shorter duration still holds the run for attestation.
- **A failed roll-forward** leaves the run `STOPPING`, holding its capacity and timeslot, until a newer term fences it or an administrator attests that its generators stopped. That includes a replacement that never committed, unless the [replacement restart](#roadmap-evacuation-replacement-restart-2026-10-24) is enabled and proves it runs nothing, in which case its share is restarted instead.
- **Write failures.** A failed `STOPPING` write in `failRunEvacuationRecovery` is retried in the background by the hold registry's retry. `TestEvacuationRollForwardHoldsCompletedReplacement` shows the stops are still sent and the next term holds the run, and `TestInterruptedRunEvacuationHoldRetry` covers the background retry.
- **Removal paths.** `forgetEvacuationHold` is called where a run is removed (`tests.go`, `persistence.go`, `timeslot_reservation.go`), but only the function itself is tested, not each call site. Those calls only free the entry sooner: `persist` already drops a hold whose run object is no longer registered (`tr.tests[test.ID] != test`), which ends the retry.
- **A claim refused in a current term** retries with backoff up to one second until the fence is released, the term ends, the run becomes terminal, its journal is gone, the run is removed or the runner shuts down. No production path is known to produce such a fence once the reload and Phase A have cleared lost-term fences; the subtest sets one directly.
- **Lost-term writers on a reused object.** The reload replaces active lost-term objects, so this slice's paths never hand a lost-term object to a new term. Adoption's install, which predates this slice, clears a fence the same way and relies on the same reload. The generic terminal writer (`completeTestRunUnderArchiveFence`) still writes with whatever fence the object carries at write time; a goroutine that kept a reused object across a lease change, if any path still allows one, would write under the newer term. It is not changed here because every run's completion goes through it.
- **Loss-tolerant runs.** The duration adjustment is sent before `secureAdoptedRuns` renews the per-run leases, because the generator's duration control does not require that lease (`planDurationWithTokenLocked`). The in-process test waits for the real epoch to lapse, so it does not use a controlled clock.

<!-- Original record: docs/qa/reviews/roadmap-execution-shutdown-join-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-execution-shutdown-join-2026-10-21"></a>

## Execution shutdown joins — 2026-10-21


A local execution driver can publish terminal status before finishing its archive writes. Previously, non-HA shutdown could return while those writes still used the archive directory. CI exposed this through the CI idempotency fixture: its runs outlived shutdown and a later temporary-directory cleanup failed.

Run drivers now retain physical ownership through backend completion and panic finalizers. Shutdown closes admission, requests stops, preserves the existing HA handoff, then waits for entered drivers and terminal retries before dependent component teardown. An expired shutdown deadline reports an error without erasing an outstanding worker. A driver scheduled after shutdown is refused; an admitted INITIALIZING run is synchronously aborted by the existing stop transition.

The 10,000-request iteration accounting regression keeps its exact request count and now allows two minutes on shared race-detector hosts. Its previous 30-second deadline tested incidental runner throughput rather than iteration completeness.

<a id="roadmap-execution-shutdown-join-2026-10-21--validation-and-review"></a>
### Validation and review

Go 1.26.10 full regression suite passed: 35,931 passing tests/subtests, 38 explicit skips and no failures. Applied-source focused race checks passed across three repetitions (36 passing tests/subtests). The original private HAR import stable fields match the previous baseline. Tested source hashes and base commit remained unchanged. The isolated prototype also passed admission, blocked-finalizer, deadline, HA handoff and acknowledgement checks before application.

Review checked admission/counter lock ordering, panic/defer ownership, shutdown deadline behavior, terminal retry registration and continue-policy HA handoff. The retry wait conservatively includes registered workers sleeping for at most the existing one-second retry delay; it does not discard an in-flight operation to shorten shutdown. Raw logs remain local under `/private/tmp/apex-shutdown-join/`.

Final source and test review found no further issues after validation. Documentation and whitespace checks passed before commit.

<!-- Original record: docs/qa/reviews/roadmap-generator-terminal-cause-2026-10-24/README.md; local evidence kept outside git -->
<a id="roadmap-generator-terminal-cause-2026-10-24"></a>

## Generator terminal cause (REC-A)


Status: implemented. Qualified locally only, with fake and in-process generators. Two review rounds: round 1's Medium and Low findings are fixed; round 2 found no High or Medium, so review stopped there and its Low findings are recorded (see Review).

The [roll-forward record](#roadmap-evacuation-rollforward-2026-10-24) left one conservative gap: a generator ends a controller stop and a run whose duration ran out the same way, `COMPLETE`, so the evacuation recoveries held every run in which any participant finished during the failover. This slice lets a generator say why a run ended, and lets the two recoveries trust a completion whose duration ran out.

<a id="roadmap-generator-terminal-cause-2026-10-24--contract"></a>
### Contract

- **Wire (additive).** `RunAdoptionResult` gains `terminal_cause` (`RunTerminalCause`: `UNSPECIFIED`, `CONTROLLER_STOP`, `DURATION_ELAPSED`, `EXECUTIONS_ENDED_EARLY`) and `completed_logical_duration` (`google.protobuf.Duration`). Both are set only for a `COMPLETE` terminal state, and the duration only for `DURATION_ELAPSED`. The client maps them to `RunAdoptionResult.TerminalCause` and `CompletedDuration`. An older controller ignores them; an older generator leaves them unset, which reads as `UNSPECIFIED`.
- **Who decides the cause (generator).** The first lifecycle transition that removes the assignment, under `s.mu`, records it on the assignment (`run_terminal_cause.go`):
  - `StopTest` (`planStopLocked`): `CONTROLLER_STOP`, also for a staged assignment and for one whose engines already reached their deadline;
  - the last group finishing without error (`finishExecutionMatching`): `DURATION_ELAPSED` only if every group that finished had an engine that reached its duration deadline, otherwise `EXECUTIONS_ENDED_EARLY` (iteration termination, every VUser finishing first, no engine);
  - the completion timer, armed only without engine execution (`WithExecution` unset): `DURATION_ELAPSED` only for a committed, unpaused assignment whose own timer token fired (`markTimerTerminalCause`);
  - fences, lease expiry, shutdown, staged expiry, failed pause, runtime containment and a failed archive finalization end the run `FAILED`, and a `FAILED` outcome carries no cause.
- **Deadline proof (engine).** `MultiGroupEngine.DurationDeadline` reports whether the duration deadline closed load admission, and the operator duration (`baseTargetDuration`) then in effect. It is set at the deadline's linearization point under the engine lock, at the three places the deadline is checked (on entry, after the pre-load baseline, and the deadline loop), so it counts only active time and a pause holds it. `markDurationDeadlineLocked` records nothing once the launch is closed, so a stop (including a graceful stop latched before the run), iteration termination or an execution goal that closed the launch first never sets it, even if the deadline then passes while the run drains. A duration control cannot land after the deadline, which closes them.
- **Logical duration.** `completed_logical_duration` is in `DurationAdjustment.target_duration` terms: the engine's target plus the group's `durationOffset` (a late replacement's elapsed steady time), and the shortest of the groups. It is exact to the nanosecond, so it compares exactly with the controller's journaled target. The synthetic timer path (`markTimerTerminalCause`) reports `assignedTestDuration` instead, which is the assignment's logical target and is not offset-adjusted per group; production generators always run engines (`WithExecution`), so that path never reaches a controller in production.
- **Retention.** The outcome travels beside the terminal tombstone (`terminalMetricsTombstone.outcome`) and in two top-level archive-manifest fields, `terminal_cause` and `completed_logical_duration_ns`. It is never added to `MetricsBatch`: `loadRetainedGeneratorArchive` decodes the manifest's embedded terminal batch with `DiscardUnknown: false`, so a downgraded generator would quarantine the archive. The manifest itself is decoded with plain `encoding/json`, so an older generator ignores the new fields. A restart restores the outcome (`manifestTerminalOutcome`); a manifest without the fields, with an unknown cause, with a non-`COMPLETE` status, or with `DURATION_ELAPSED` and no positive duration restores `UNSPECIFIED`.
- **Which completions recovery trusts (controller).** Phase A keeps each `ALREADY_COMPLETED` result beside `completed` (`completions`). Inside the already gated rollback and roll-forward paths only, with no new setting, a completion is natural (`runEvacuationNaturalCompletions`) when its state is `COMPLETE`, its cause `DURATION_ELAPSED`, and its logical duration is at least the durable expectation (`runEvacuationExpectedDuration`): the journal's `CommitTargetDuration` for a replacement, the run's durable `Duration` for a survivor or the source. A duration change is persisted before it is sent, so the durable duration is never longer than what a generator was told, except after an extension the lost leader never delivered, which then holds the run. Every other completion keeps today's hold, and its reason names why (`runEvacuationCompletionDoubt`): stopped by a controller, no terminal cause, executions ended early, an unknown cause, or a duration short of the expected one, for example one a late old owner shortened.
- **Roll-forward** (`completeRunEvacuationRollForward`):
  - `unexpectedRunEvacuationCompletions` skips natural completions; a replacement counts if adopted or naturally completed;
  - `AdjustDuration` goes only to adopted replacements;
  - capacity (`commitRunEvacuationRollForwardCapacity`): a naturally completed replacement's `pending` (or half-released `releasing`) row at the journal's generation is released through its retained client (`releaseRunEvacuationReplacementClaim`), a row an earlier term already released is accepted as gone, a row at any other generation is refused, and a committed row is left, as for any completed participant. A generator reports the completion as soon as its terminal tombstone is published, but refuses the release (`PodCapacityStillDrainingMessage`) until its stop completion closes (after the tombstone, a fence write); the release is retried up to 20 times, 250 ms apart, within the claim's deadline, and a release still refused holds the run. The retry lives in the shared `releaseRunEvacuationReplacementClaim`, so a rollback releasing a fenced staged replacement retries the same way;
  - before the journal swap, every natural completion is added to the plain checkpoint's `CompletedTargets`, so a later successor does not depend on the generator's pruneable tombstone. A run whose every participant ran out installs with no adopted participant, and `resumeAdoptedRun` completes it from the retained archives as it already does.
- **Rollback** (`completeRunEvacuationRollback`): a naturally completed source no longer fails the rollback, and natural survivors are not unexpected. Both are added to the `CompletedTargets` of the prior checkpoint the rollback swaps in for the journal; the adoption then re-arms the run with them completed, as for any completed participant.
- **Ordinary adoption and committed promotion** are unchanged; Phase A only logs each completed generator's cause and duration.
- **No journal version.** Nothing about the journal changes. Version 3 and 4 `source_fenced` journals lack the version 5 fields, so they stay permanently fail-closed: the run is held `STOPPING` until a newer term fences its participants or an administrator attests that its generators stopped (`POST /api/v1/tests/{id}/interrupted-stop/resolve`).

<a id="roadmap-generator-terminal-cause-2026-10-24--invariants"></a>
### Invariants

- **A stop is never read as a finished run.** Whoever removes the assignment first under `s.mu` owns the cause; a stop that wins reports `CONTROLLER_STOP` even if the deadline had passed, and a timer or engine completion that wins leaves a later stop nothing to change.
- **No shortened share is trusted.** A completion below the durable expectation holds the run.
- **Unknown means stop.** An older generator, an older archive or a pruned tombstone gives no cause, or no result at all, and the run is held as before.
- **Older peers.** Old controllers ignore the new result fields; a downgraded generator ignores the new manifest fields and still decodes the embedded terminal batch.

<a id="roadmap-generator-terminal-cause-2026-10-24--validation"></a>
### Validation

- **`TestDurationDeadlineReportsOnlyAnElapsedDuration`** (`pkg/engine`): the deadline reports the configured and the adjusted duration; a pause that outlasts a 2 s duration holds it (the run ends only after its active duration plus the pause); a hard stop, a graceful stop and iteration termination report none.
- **`TestDurationDeadlineAroundThePreBaseline`**: each of the three marks is reached: a stop during a 2 s pre-load baseline reports no deadline; a deadline that falls a nanosecond after a 50 ms baseline is recorded by the check after the baseline, before the loop's first tick and before any load; a `RunAt` whose start lies past the whole window is recorded on entry. A graceful stop latched before a run past its window, or before a run without a baseline, records nothing, and neither does a mark on a closed launch.
- **`TestRunTerminalCauseTellsAStopFromAnElapsedDuration`** (`pkg/distributed/generatorrpc`, real executing runs): elapsed duration, controller stop, staged stop, iteration termination (`EXECUTIONS_ENDED_EARLY`), a failure (`FAILED`, no cause), a duration adjustment then natural end (the adjusted target), a duration offset (offset plus target) and a pause that outlasts a 2 s duration. Each is checked in the retained tombstone and in a successor's adoption result.
- **`TestRunTerminalCauseStopRacingTheTimer`** (synthetic generator): a stop then the timer, the timer then a stop, a timer that fires while paused, a completion without timer ownership, and 40 concurrent stop/timer races. In each race the stop's acknowledgement shows whether it removed the assignment (only then does it count pending VUsers), and the outcome is the stop's if it did and the timer's otherwise.
- **`TestRunTerminalCauseSurvivesRestart`**: an elapsed and a stopped run keep their outcome across a generator restart through the manifest; the embedded terminal batch carries neither field; a struct with only the older fields decodes the new manifest.
- **`TestManifestTerminalOutcomeDefaultsToUnspecified`**: an archive finalized without the fields restores `UNSPECIFIED`, as do an unknown cause, a `FAILED` status, and an elapsed duration without a positive length.
- **`TestAssignmentTerminalOutcome`**: only `COMPLETE` carries a cause, an elapsed cause with no measured duration degrades to `EXECUTIONS_ENDED_EARLY`, the shortest group's logical duration is kept, and a group without an engine deadline ends the assignment early.
- **`TestFailedArchiveFinalizationDropsTheTerminalCause`**: a failed archive finalization ends the run `FAILED` and retains no cause.
- **`TestRunEvacuationCompletionDoubt`** (`pkg/api`): only an `ALREADY_COMPLETED`, `COMPLETE`, `DURATION_ELAPSED` result at no less than a positive expectation is trusted; one nanosecond short, no expectation, a `FAILED` state, an `ALREADY_STOPPED` outcome and every other cause are not.
- **`TestEvacuationRecoveryTrustsANaturalCompletion`** (`pkg/api`, fake generators):
  - a naturally completed replacement rolls forward with a pending reservation (released at the new term), a committed reservation (left), an activated standby (released), and a duration above the commit target; it gets no duration adjustment and is in the `CompletedTargets` of the checkpoint that replaced the journal, captured at the swap (`journalSwapCaptureStore`), before the adoption re-arms the run;
  - every participant natural: the run installs with no adopted participant;
  - a natural survivor, for roll-forward and for rollback, also checked at the swap;
  - a natural source: the rollback fences and releases the replacement and records the source completed, in the durable and in-memory checkpoint.
- **`TestEvacuationRecoveryHoldsACompletionWithoutItsDuration`**: `CONTROLLER_STOP`, `UNSPECIFIED`, `EXECUTIONS_ENDED_EARLY`, an unknown cause and a shorter duration (a late old owner) hold the run, each for a roll-forward replacement, a roll-forward survivor and a rollback source, with the reason naming the cause; a replacement 1 ms short of its commit target is held, a survivor that ran to the replacement's commit target but not to the run's duration is held, and so is a replacement whose capacity release the generator does not confirm (its row stays `releasing`).
- **`TestEvacuationRecoveryTrustsANaturalCompletion`** also releases a naturally completed replacement's reservation after the generator refused it twice as still draining; **`TestEvacuationRecoveryHoldsACompletionWithoutItsDuration`** holds the run after the bounded retries (three in the test) are refused, with the row left `releasing`.
- **`TestCommitRunEvacuationRollForwardCapacityRefusesUnprovableRows`** refuses a naturally completed replacement's pending row from another term without any capacity request.
- **`TestNaturalSourceAcrossAnotherRollbackLeaderChange`**: a rollback successor lost after releasing the staged replacement's reservation and before replacing its `rolling_back` journal (hold write and stops failing) leaves the run `RUNNING`; the next term rolls back again and records the naturally completed source.
- **`TestNaturalCompletionAcrossAnotherLeaderChange`**: a successor lost after releasing the completed replacement's reservation and before the journal swap (its hold write and stops also failing) leaves the run `RUNNING` under `rolling_forward`; the next term rolls forward without releasing again. If the generator pruned the outcome before the next term asked (`UNKNOWN_TEST`), the run is held.
- **`TestEvacuationRollForwardHoldsCompletedReplacement`** and **`TestEvacuationRecoveryHoldsUnrecordedCompletion`** now model current generators: stopped participants report `CONTROLLER_STOP`, and an `UNSPECIFIED` replacement is still held.
- **`TestEvacuationRollForwardAgainstRealGenerator`** gains two in-process modes: a committed replacement whose staged duration ran out before the successor rolls forward with the replacement completed, and a committed replacement the predecessor stopped is held as stopped by a controller. The existing stopped-staged mode now reports the same cause.

<a id="roadmap-generator-terminal-cause-2026-10-24--review"></a>
### Review

Round 1 found no High issue, one Medium test gap and eight Low items. All are fixed:
- **Medium: two of the engine's three deadline marks were untested,** and a mark at the post-baseline check on a launch a stop had closed survived mutation; the generator masked it only because the stop records its cause first. Fixed: `markDurationDeadlineLocked` now records nothing once the launch is closed, which also covers a graceful stop latched before the run and a stop whose drain outlasts the deadline, and `TestDurationDeadlineAroundThePreBaseline` reaches each mark.
- **Low (safety): release while the generator finishes the terminal transition.** The adoption reports `ALREADY_COMPLETED` once the tombstone is published, but the generator refuses the capacity release until its stop completion is closed, so the run could be held. Fixed: a bounded retry on `PodCapacityStillDrainingMessage`, with tests for success and for exhaustion.
- **Low (safety, docs): a completion still finalizing its archive answers `UNKNOWN_TEST`.** Pre-existing; recorded under Remaining.
- **Low: a natural replacement's row from another term** had no test; added.
- **Low: a rollback assignment with no effect.** The rollback set the candidate checkpoint's completed targets after its swap, which nothing read; removed, and the code comment and this record corrected.
- **Low: the race test claim.** The concurrent race test now identifies the winner from the stop's acknowledgement.
- **Low: docs.** The run's own deadline elapsing before the successor is now listed as exercised with fake and in-process generators only, in the roll-forward record and the spec; a natural source across a lost rollback successor gained a test.
- **Low: timing.** Both pause tests use a 2 s duration with a pause that outlasts it.
- **Low: the synthetic timer's duration** is now described as not offset-adjusted.

Round 2 (tests and records) found no High or Medium. It re-ran the round-1 tests under `-race` and shuffled order and caught every round-1 mutation again; only the two intended fake-generator cases reach the release retry, with a 1 ms delay. Its wording Lows were fixed in place (comments and records only): the finalizing-completion answer is `ALREADY_STOPPED` rather than `UNKNOWN_TEST`, the draining window is the stop completion after the tombstone rather than archive finalization, and rollback shares the release retry. The rest are recorded, not fixed:
- **No generator-side test pins the draining message** (`pod_capacity.go`): if the stopping-state rejection changed, the retry would silently stop firing and runs would be held as before.
- **Flake risk in `TestDurationDeadlineAroundThePreBaseline/stop_during_the_baseline`**: a hard `Stop()` before `RunAt` sets up its contexts is not latched, so on a starved runner the stop after a fixed 100 ms could be lost; polling for `baseCtx` before stopping would close it.
- **The deadline proof is slightly loose**: `GracefulStop` closes the launch under the lock but cancels `loopCtx` after unlocking, and the deadline loop can mark in that gap. Generators mask it (only detached drains call `GracefulStop`, after `CONTROLLER_STOP` is recorded); no test drives a drain that outlasts the deadline.
- **REC-A exit coverage**: the deadline-elapsed cases cover the `staged`, `rolling_back`, `replacement_committing` and `rolling_forward` phases with fakes and the replacement in-process; the `prepared` phase and an in-process survivor or source whose duration elapsed are not covered.

Mutations after round 1, applied with `go test -overlay`, are caught: no mark on entry, no mark after the baseline, no launch gate in the mark, the loop never marking, a natural row at another generation released, and the release retried never, without bound, or on any refusal (the last after the "release unconfirmed" case began counting release requests). A mark after the baseline made unconditional is now equivalent to the code, since the gate decides, so the condition was removed.

<a id="roadmap-generator-terminal-cause-2026-10-24--remaining"></a>
### Remaining

- **Review.** Rounds 1 and 2 ran; see Review.
- **A completion still finalizing its archive.** Between removing the assignment and publishing the terminal tombstone (archive finalization included), a successor's adoption finds no terminal state and the run is held. For a natural completion the answer is `ALREADY_STOPPED` (its supervision tombstone is already written by `clearRunSupervisionLocked`), and `UNKNOWN_TEST` only without supervision. This predates the slice; reporting a finalizing outcome would need a new adoption outcome. Resolved on 2026-10-25 by the [evacuation completion slice](#roadmap-evacuation-completion-2026-10-25): the generator flags such an answer with an additive `finalizing` field (the outcome keeps its earlier value for older controllers), and a successor waits boundedly for the published outcome; a run still finalizing when the wait ends is held as before.
- **Mutation pass.** Thirty mutations, applied with `go test -overlay`, were run against the tests above. All are caught:
  - generator: no stop cause, the last group always elapsed, the offset ignored, the timer's ownership or pause check dropped, the manifest status check dropped, no manifest outcome written, no outcome restored after a restart, no cause in the adoption result, a cause on a non-`COMPLETE` outcome, the longest rather than the shortest group kept, the ended-early flag ignored, and an archive failure that keeps the outcome (the last three after `TestAssignmentTerminalOutcome` gained its fold cases and `TestFailedArchiveFinalizationDropsTheTerminalCause` was added);
  - engine: a deadline always reported, and the deadline loop never marking it;
  - controller: no duration comparison, a controller stop trusted, the expectation always the run's duration or always the commit target, a natural source still failing the rollback, either swapped checkpoint omitting natural completions, an adjustment required of a natural replacement, a natural pending row not released, an already released row refused, Phase A recording no completions, the unexpected-completion filter ignoring natural completions, and the state, outcome and zero-expectation checks dropped (the last three after `TestRunEvacuationCompletionDoubt` was added).

  Two mutations survived and the code was simplified instead: the engine's guard against marking the deadline twice (it is marked at most once per run, so the guard was removed), and a `!natural` term in the roll-forward's not-adopted check that the completed-target condition already implied.
- **Deployed qualification** of REC-A, as listed in the roll-forward record, is unchanged.
- **Completions from older generators or pruned tombstones** still hold the run; a mixed-version fleet gets the benefit only from upgraded generators.
- **A survivor that was an earlier evacuation's replacement** runs to that evacuation's commit target, slightly below the run's duration, so a natural completion of it during a later evacuation is held as short.
- **The synthetic timer** keeps running through a pause; it reports `DURATION_ELAPSED` only when unpaused at expiry, and a pause and resume before expiry is not modeled. Production generators always run engines (`WithExecution`).
- **A stop after the deadline.** A stop that removes the assignment while the engines drain after their deadline reports `CONTROLLER_STOP`, so the run is held, conservatively.

<!-- Original record: docs/qa/reviews/roadmap-same-leader-intervention-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-same-leader-intervention-2026-10-19"></a>

## Same-leader evacuation intervention — 2026-10-19


A REC-B slice from the [implementation spec](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#rec-b--bound-same-leader-terminal-cleanup-without-weakening-fences). Validation completed October 20.

A canceled current-owner evacuation with a durable journal now has a bounded
stop retry budget: the initial round plus five retry rounds. Exhaustion persists
a bounded, scrubbed intervention diagnostic before retiring the worker. Failed
persistence retries without issuing more stop rounds. The run stays STOPPING
and retains its clients and capacity while termination remains unproven.

The existing system-administrator endpoint accepts this explicitly eligible
same-leader state. Its required all-processes-stopped attestation is the proof;
controller cancellation is only eligibility. A distinct durable proof prefix
preserves truthful ownership history and old successor prefixes remain readable.
FAILED/proof persistence precedes closing transports and archive/capacity
reconciliation. Original causes and incomplete archive evidence survive. Held
joint-evidence buffers whose writer has ended are released with dropped-evidence
accounting; missing evidence is not converted into a successful archive.

The [operator procedure](../../ENTERPRISE_PLATFORM_RUNBOOK.md#resolving-an-unproven-generator-evacuation)
is bundled in Help. Old controllers cannot use the new proof shortcut; retain
its durable record for a capable cleanup owner. Ordinary terminal/stale-runtime
cleanup still stops uncommitted participants. No global epoch is advanced and
no heartbeat, lease timeout or pod-name reuse is accepted as process death.

<a id="roadmap-same-leader-intervention-2026-10-19--validation-and-review"></a>
### Validation and review

- The full standard Go suite passed: 168 tested packages and 17,638 top-level
  tests; 36 top-level skips are retained in the log. Ten packages have no tests.
- After the evidence-buffer review fix, the full API suite passed again on the
  final production source: 6,412 top-level tests, 14 top-level skips.
- The final targeted recovery run passed all 18 top-level tests. It includes
  the subsequently added UTF-8/redaction diagnostic case, exact retry budgets,
  failed persistence, lost ownership, authorization, repeated cleanup and
  recovery after a crash immediately following durable attestation.
- Final targeted Help unit tests passed (51 tests). Type checking, lint, documentation
  links and the embedded dashboard build passed. The final full dashboard cycle passed 8,008 unit tests across 716 files,
  321 dev-admin browser workflows (6 expected skips) and 260 local-auth
  workflows (1 expected skip).

The validation summary (`evidence/validation-summary.json`), compressed logs,
source hashes (`evidence/source-hashes.json`), controller hashes, runtime metadata
and review rounds (`evidence/review.txt`) retain the tested local cohort.
Commands and cohort notes (`docs/qa/reviews/roadmap-same-leader-intervention-2026-10-19/evidence/commands.md`) distinguish each gate. The first
broad attempt used incompatible `-trimpath` paths for source fixtures and was
stopped; its log is preserved separately. It also exposed a stale generated QA
spec-file count, fixed in the preceding scenario commit. That attempt is not a
successful full-suite result. The standard broad run predates the final buffer
fix; the subsequent full API run covers that fix. Only the additional Go diagnostic
test changed after the final API binary compiled, and it passed in the targeted
run. Browser verification then exposed an administrator Help reload race: pending
permissions redirected the requested article before access resolved. The Help page
now keeps that URL pending without rendering protected content; confirmed denial
and errors still fall back, and public Help stays available. Unit and real-response
delay browser regressions cover those states. The Help bundle was rebuilt after
Go validation finished. A first complete dashboard attempt passed all units and
321 dev-admin workflows (6 expected skips), then its required main-fetch guard
stopped before building local-auth: main had advanced. The two upstream chart/history
commits were integrated without conflicts; 187 focused tests passed. The repeated
full dashboard cycle validates the final UI/bundle on that integrated tree.

Review fixed archive/topology lock ordering, stable bounded diagnostics,
terminal evidence-buffer retention and the Help permission-loading race. The
first browser attempt also contained an incorrect test assertion: repository-only
review references intentionally stay plain text in bundled Help. Its failure and
the subsequent real reload failure are retained separately. Final review found no unresolved issues.

The private ZIP's actual multipart HAR import remains documented in the
[scenario review](AUTHORING_AND_IMPORT_SLICES_2026-10.md#roadmap-scenario-prerequisite-2026-10-19). This slice
changes no importer or source-capture payload, and no customer capture was replayed.

<a id="roadmap-same-leader-intervention-2026-10-19--remaining-scope"></a>
### Remaining scope

This implements the journaled current-owner intervention boundary. Unjournaled
cleanup, earlier-phase resumable adoption, independent deployed fault trials and
the rest of the recovery program remain separately open. Local tests do not
certify multi-host recovery. Session caches remain in use for subsequent slices.
