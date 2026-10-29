# Completed analytics & RCA slice reviews — October 2026

Status: Historical. Consolidated on 2026-10-29 from completed scoped review records under `docs/qa/reviews/`. Original dates, validation outcomes, failed intermediate checks, source hashes and qualification limits are preserved intact below. Untracked raw evidence remains local at the cited paths. Use the [active review index](../../qa/reviews/README.md) for current capstone records.

## Records
- [Analysis workflows implementation review (`analysis-workflows-2026-10-01/REVIEW.md`)](#analysis-workflows-2026-10-01)
- [Authoring, native panels, cloud collectors and messaging — 2026-10-01 (`authoring-analytics-cloud-2026-10-01/REVIEW.md`)](#authoring-analytics-cloud-2026-10-01)
- [Imported metric identities and closed analytical requests (`feature-completion-native-identity-2026-10-04/README.md`)](#feature-completion-native-identity-2026-10-04)
- [Native partition-pruning slice contract (`feature-completion-native-partitions-2026-10-05/CONTRACT.md`)](#feature-completion-native-partitions-2026-10-05-contract)
- [Canonical native query window decision — 2026-10-05 (`feature-completion-native-partitions-2026-10-05/PRECISION_DECISION.md`)](#feature-completion-native-partitions-2026-10-05-precision-decision)
- [Portable measurement series identity — scoped qualification (`feature-completion-native-rca-identity-2026-10-05/README.md`)](#feature-completion-native-rca-identity-2026-10-05)
- [Durable uploads and complete dataset deletion (`feature-completion-native-upload-2026-10-04/README.md`)](#feature-completion-native-upload-2026-10-04)
- [Native worker and durable publication foundation (`feature-completion-native-worker-2026-10-04/README.md`)](#feature-completion-native-worker-2026-10-04)
- [Roadmap implementation: local-wall timestamp qualification (`roadmap-analytics-local-wall-times-2026-10-19/README.md`)](#roadmap-analytics-local-wall-times-2026-10-19)
- [Roadmap implementation: native timestamp source offsets (`roadmap-analytics-timestamp-offsets-2026-10-19/README.md`)](#roadmap-analytics-timestamp-offsets-2026-10-19)
- [Native browser document failure evidence — 2026-10-20 (`roadmap-browser-native-failure-2026-10-20/README.md`)](#roadmap-browser-native-failure-2026-10-20)
- [Distribution and pending-joint evidence bounds — 2026-10-20 (`roadmap-joint-bounds-2026-10-20/README.md`)](#roadmap-joint-bounds-2026-10-20)
- [Joint evidence through every entry point — 2026-10-21 (`roadmap-joint-entry-points-2026-10-21/README.md`)](#roadmap-joint-entry-points-2026-10-21)
- [Joint evidence ingestion budgets — 2026-10-22 (`roadmap-joint-ingestion-budgets-2026-10-22/README.md`)](#roadmap-joint-ingestion-budgets-2026-10-22)
- [Mapping-preserving reassociation — 2026-10-20 (`roadmap-mapping-association-2026-10-20/README.md`)](#roadmap-mapping-association-2026-10-20)
- [Native accepted rejections and histogram preview — 2026-10-21 (`roadmap-native-accepted-rejections-2026-10-21/README.md`)](#roadmap-native-accepted-rejections-2026-10-21)
- [HTTP byte evidence — 2026-10-22 (`roadmap-native-byte-evidence-2026-10-22/README.md`)](#roadmap-native-byte-evidence-2026-10-22)
- [Native import failure provenance — 2026-10-21 (`roadmap-native-failure-provenance-2026-10-21/README.md`)](#roadmap-native-failure-provenance-2026-10-21)
- [Native population reconciliation golden — 2026-10-21 (`roadmap-native-population-golden-2026-10-21/README.md`)](#roadmap-native-population-golden-2026-10-21)
- [Native decimal precision consent review — 2026-10-20 (`roadmap-native-precision-2026-10-20/README.md`)](#roadmap-native-precision-2026-10-20)
- [Native physical-writer ownership across restart and HA — 2026-10-25 (`roadmap-native-writer-ownership-2026-10-25/README.md`)](#roadmap-native-writer-ownership-2026-10-25)
- [Native partition precedence — 2026-10-20 (`roadmap-partition-precedence-2026-10-20/README.md`)](#roadmap-partition-precedence-2026-10-20)

<!-- Original record: docs/qa/reviews/analysis-workflows-2026-10-01/REVIEW.md; local evidence kept outside git -->
<a id="analysis-workflows-2026-10-01"></a>

## Analysis workflows implementation review


Date: 2026-10-01. Branch: `codex/analysis-workflows`.

<a id="analysis-workflows-2026-10-01--scope"></a>
### Scope

The changes add run-bound Linux/Prometheus monitoring, deeper gRPC and WebSocket
diagnostics, durable preparation, and native Parquet import/query/comparison
workflows. New analytical collection, preparation, and imports require explicit
project flags. The capability ledger retains partial status where broader
acceptance or implementation is outstanding.

<a id="analysis-workflows-2026-10-01--review-and-corrections"></a>
### Review and corrections

Review covered authorization, source and preparation revision races, durable
publication, restart/cancellation, artifact ownership and deletion, bounded
queries, metric semantics, protocol measurement availability, and UI state.
Corrections include:

- Bind analytical artifacts to a durable storage identity, so a replica with a
  different archive configuration cannot delete or publish evidence silently.
- Retain deletion retries when metadata reads fail, and compare cleanup deadlines
  in UTC regardless of the caller's timezone.
- Preserve preparation monitor snapshots, reject stale revisions, and allow
  cancellation and audit reads after disabling new preparation.
- Keep read-only comparison endpoints available to authorized viewers with the
  correct service-account scope; enforce bounded request contracts.
- Preserve all saved comparison selectors and the executed query when saving.
  Keep chart labels tied to returned evidence after editing the query form.
- Qualify protocol throughput baselines against retained sample coverage and
  preserve unavailable measurements separately from measured zero.
- Preserve single-owner collector spools, source identity, and sample retry
  deduplication across restart.
- Correct asynchronous UI state, keyboard navigation expectations, chart label
  clipping, concurrent process-fence test synchronization, and a CLI cancellation
  test that depended on a five-millisecond scheduling window.

Regression tests accompany the fixes. The final chart checks inspect label
bounds in a real browser, rather than relying on screenshots alone.

<a id="analysis-workflows-2026-10-01--verified-local-checks"></a>
### Verified local checks

- PostgreSQL 16.15 migration, concurrent initialization, preparation consumption,
  monitor batch deduplication, restart recovery, mapping history, and cleanup
  integration tests passed against a disposable localhost instance.
- Dashboard typecheck, lint, production build, and all 4,658 unit tests passed.
- CGO-free analytics capability tests passed; generator and collector dependency
  graphs exclude DuckDB even with the analytics build tag.
- Documentation and whitespace checks passed.

- Native analytics checks passed for the shared analysis/DuckDB packages,
  controller API, storage and CLI, including import/rejection artifacts,
  preparation, monitoring and qualified protocol baselines.
- Targeted race checks passed for analysis, collectors, gRPC/WebSocket,
  storage, generator RPC, and the API. Final API checks include cleanup,
  preparation and recovery; collector checks include spool restart/locking.
- The corrected CLI cancellation regression passed 25 consecutive executions.
- The final affected Playwright workflows passed in both profiles: 18 dev-admin
  and 10 local-auth tests. The broader dashboard cycle also ran: its local-auth
  profile passed 94 tests with one skip; the two dev-admin assertion failures
  were corrected and are included in the passing final affected run.

- The final full `go test -ldflags=-w -p 1 ./...` gate passed. Passing package
  results were reused on the final aggregate run after the CLI correction;
  the preceding full API execution passed in 475 seconds.

The final review pass found no further actionable issues in the implemented
changes. This is a review conclusion for this diff, not a claim that the broader
roadmap or unexecuted acceptance environments are complete.

Key reproducible checks:

```sh
go test -ldflags=-w -p 1 ./...
go test -ldflags=-w -p 1 -tags analytics_duckdb ./pkg/analysis/... ./pkg/api ./pkg/storage ./cmd/apexctl -run 'Test(Analysis|Native|Parquet|Container|Preparation|Workflow|RunMonitor|BaselineProtocol|Lagged|Bucket)'
GOMAXPROCS=4 go test -race -ldflags=-w -p 1 ./pkg/api -run 'Test(Analysis|RunMonitor|Preparation|BaselineProtocol|InterruptedEvacuationRecoveryRequiresEveryProcessFence)'
go test -ldflags=-w -p 1 ./cmd/apexctl -run '^TestExecuteControllerCIAbortsOnCancellation$' -count=25
npm --prefix dashboard run typecheck
npm --prefix dashboard run lint
npm --prefix dashboard test
npm --prefix dashboard run build
APEXLOAD_E2E_PROFILE=dev-admin scripts/run-e2e.sh workflow-analysis-preparation.spec.ts workflow-protocol-diagnostics.spec.ts workflow-monitoring.spec.ts tier0-scenario-builder.spec.ts
APEXLOAD_E2E_PROFILE=local-auth scripts/run-e2e.sh workflow-analysis-preparation.spec.ts workflow-protocol-diagnostics.spec.ts workflow-monitoring.spec.ts
python3 scripts/check-docs.py
```

PostgreSQL checks require `APEXLOAD_TEST_POSTGRES_URL` pointing to a disposable
PostgreSQL 16 database and `-tags postgres_integration` on `./pkg/storage`, with
`TestPostgres(SchedulerLeaseGenerationMigration|ConcurrentInitialization|InitializationLockLifecycle|AnalysisWorkflows)` selected.

<a id="analysis-workflows-2026-10-01--acceptance-limits"></a>
### Acceptance limits

The user explicitly skipped the 10 GB compressed-input benchmark. No large-input
throughput, peak RSS, spill usage, or telemetry-lag result is claimed.

Live acceptance across every Kubernetes, Azure and ECS target, container image
execution, and the cross-platform release matrix were not executed locally.
The Docker daemon did not respond, so PostgreSQL testing used a verified,
disposable native runtime instead. CI includes the native packaging and database
checks, but its remote results are not asserted here.

Additional monitor adapters (SSH, OTLP metrics ingestion, JMX/JDBC, CloudWatch,
Azure Monitor), complete dependency intelligence, and qualified statistical
anomaly delivery remain follow-on scope, as documented in the roadmap.

<!-- Original record: docs/qa/reviews/authoring-analytics-cloud-2026-10-01/REVIEW.md; local evidence kept outside git -->
<a id="authoring-analytics-cloud-2026-10-01"></a>

## Authoring, native panels, cloud collectors and messaging — 2026-10-01


Status: implemented slices reviewed and validated; broader roadmap scope remains open. Branch: `codex/authoring-analytics-cloud`, based on `7bf711ea`. The original shared checkout is preserved.

<a id="authoring-analytics-cloud-2026-10-01--delivered-scope"></a>
### Delivered scope

- Script save handoffs without a returned body fetch the canonical saved script instead of reusing an unrelated draft. Scenario group reseeding retains transaction response-time SLAs.
- Project editors can create saved native run metric line, latest-value and table panels. Viewers can select saved project dashboards. Missing observations remain missing; source interval percentiles are never averaged. Unsupported dimension/filter/overlay contracts render an explicit unavailable state.
- CloudWatch and Azure Monitor collectors use signed run assignments, local operator-owned target/query approvals, bounded queries/responses and scoped cloud identities. AWS account identity is checked before metrics access. Cloud summaries retain source timestamps, units, statistic identity and missing values. Accepted points reuse durable batch acknowledgement and deduplication. These adapters remain preview pending live cloud acceptance.
- Explicit finite response-property categories propagate through action metrics, generator RPC, live tables, JSONL/Parquet artifacts, archive filtering and reports. Capability negotiation prevents old peers silently dropping requested property metrics. Promotion follows redaction and replaces adapter-provided labels.
- Kafka and Event Hubs distinguish observed zero lag from unavailable lag. Exact transaction rules can evaluate maximum consumer age and offset backlog from complete archived evidence; incomplete observations remain unscored. Event Hubs deployment defaults preserve explicit script settings.

<a id="authoring-analytics-cloud-2026-10-01--review-fixes"></a>
### Review fixes

- Fixed browser test types and separated native projection helpers from React components for lint compliance.
- Updated authoring regression fixtures to verify the canonical stored script, not stale draft reuse.
- Preserved prior saved metric aliases and made unsupported native queries explicit. Visual review also added metric/unit labels to saved panels and exports.
- Added stable accessible names to native dropdowns after browser verification exposed option text in their labels.
- Connected property promotion to the canonical script runtime view, in addition to scenario overrides, after browser verification found the missing entry point.
- Kept cloud deduplication retention separate from the existing OTLP window.
- Preserved observed-zero lag availability through generator protobuf transport.
- Added CloudWatch signed-request/account-mismatch fixtures; aligned transport headers with the documented JSON API and rejected provider-incompatible unit names.
- Prevented empty or failed cloud queries from bypassing the configured collection interval.
- Preserved the first complete cloud minute when execution begins exactly at a minute boundary.

<a id="authoring-analytics-cloud-2026-10-01--follow-up-review-cycle"></a>
### Follow-up review cycle

- Prevented provider-null cloud buckets from entering the acknowledgement identity set, so delayed numeric values (including zero) remain collectable during the lookback window. Added collection and acknowledgement regression coverage for both cloud packs.
- Normalized Azure timestamps to UTC before duplicate detection; equivalent instants with different offsets are rejected.
- Restored WebSocket diagnostics in the shared generator telemetry clone, with measured-zero and deep-copy regression coverage.
- Re-reviewed the fixes and surrounding collection, serialization, property aggregation and messaging-rule paths; no further actionable findings remained in the reviewed scope.
- Post-fix validation passed: collector and generator RPC race tests, the full Go suite (`APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -ldflags=-w -timeout 20m -p 1 ./...`), documentation checks and `git diff --check`. Dashboard code was unchanged in this follow-up cycle; its earlier validation remains recorded below.

<a id="authoring-analytics-cloud-2026-10-01--verification"></a>
### Verification

- Full repository Go suite passed: `APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -ldflags=-w -timeout 20m -p 1 ./...`, including API/generated-contract, engine, generator RPC, SQLite storage and packaging tests.
- Collector, Kafka and Event Hubs race tests passed; collector tests were rerun after the final polling/window fixes. Focused property, archive, distributed and messaging regressions passed.
- Dashboard typecheck, lint and all 4,672 unit tests passed. The final build and focused lint passed after the metric-label change.
- `npm run test:cycle` exercised the full browser selection: dev-admin had 175 passes, 4 existing skips and the two newly added workflow failures; local-auth had 95 passes, 1 existing skip and the same two failures. Those findings were fixed. The complete affected analysis/preparation and script-settings files then passed all 8 tests in each profile. After the final metric-label change, all 4 analysis/preparation tests passed again in each profile. No test retries were used.
- Documentation checks and `git diff --check` passed. Native-panel screenshots were inspected and retained below.
- Final review found no additional actionable issues in this implemented scope. Live-cloud acceptance and the broader items below are not covered by these local results.

Screenshots: dev-admin native panel (`native-panel-dev-admin.png`), local-auth native panel (`native-panel-local-auth.png`).

<a id="authoring-analytics-cloud-2026-10-01--remaining-roadmap-scope-and-acceptance-limits"></a>
### Remaining roadmap scope and acceptance limits

This is a set of bounded implementation slices, not closure of every broad roadmap item. Remaining work includes the broader authoring/scenario orchestration consolidation; native dimension/filter/monitor/imported overlay authoring; property timelines and saved comparisons; partition/subscription messaging dashboards, full processor/checkpoint ownership and provider-wide acknowledgement telemetry. Live CloudWatch/Azure Monitor acceptance requires approved cloud environments. The previously waived 10 GB import benchmark was not run.

Cloud API references: [CloudWatch GetMetricData](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricData.html), [CloudWatch API reference](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/acw-api.pdf), [Azure Monitor metrics](https://learn.microsoft.com/en-us/rest/api/monitor/metrics/list?view=rest-monitor-2023-10-01).

<!-- Original record: docs/qa/reviews/feature-completion-native-identity-2026-10-04/README.md; local evidence kept outside git -->
<a id="feature-completion-native-identity-2026-10-04"></a>

## Imported metric identities and closed analytical requests


Status: fresh composed-source local verification passed; the slice is not yet committed. The full completion program remains active.

Mapping version 2 retains source, host, service and at most 16 exact named dimensions, including selected nested scalar columns. Versioned canonical artifacts and typed supervised IPC preserve identity. Queries require one compatible metric identity before aggregation or rates, and admit the complete selected quality/value population before calculating a result. Missing evidence stays missing; stale, skewed, dropped, unknown or inconsistent quality is refused. Historical v1 artifacts use an explicit adapter. A pinned v2 query requires canonical v2 evidence and cannot silently use that adapter.

New mapping, saved-definition and query writes use a bounded closed JSON grammar. Unsupported options, aliases, duplicate decoded keys, malformed Unicode, null and trailing data are refused before typed publication. Requested foreign project/dataset scope fails before analytical work. Approved scalar paths, exact escaped dimension keys and quoted values cannot supply SQL expressions. Input snapshots are owned before hashing/staging; current project, revision, association, deadline and physical ownership barriers remain in force.

The dashboard maps identity columns and provides separate comparison selectors. Empty dimension values and special names such as __proto__, slash, tilde, quotes and Unicode retain their exact meaning. Saved-view validation completes before setters run; later unexecuted controls cannot alter displayed or saved result identity. CSV retains the executed selection and identity. Current dashboard saved bounds preserve exact milliseconds and refuse finer nonzero precision atomically. Empty host/service text currently omits that filter, so explicit empty host/service selection remains a subsequent analytics slice. Original artifacts are unchanged. Real CLI transport oracles cover mapping, series, both-side comparison and saved resources without rewriting their JSON.

Review and actual validation corrections: added the canonical documentation index entry; replaced the Go-whitespace control-character regex with its exact codepoint predicate; added post-await project/operation guards; fixed Pause owner retirement and stale finalizers; preserved saved fractional milliseconds and refused finer unsupported bounds; refused invalid calendar rollover before Date construction; moved native Unicode fixture cases into subtests that join retained outputs; and added a real refusal-before-release/admission-after-join regression. The native strict-query fixture needed finite import metadata. Browser testing found the empty-first-file undefined-equality dereference, now guarded by an actual finite descriptor and covered by component regressions. Exact select-label tests incorrectly included option text; their role selectors preserve all fixtures and assertions. A final native review found v2 queries accepting adapted/explicit v1; the version fence and real legacy-compatibility/refusal regression fix this. That regression initially expected an internal message through sanitized IPC; it now asserts the public typed error and no results. No production resource, ownership or disclosure guard was relaxed.

Actual initial generation passed (216.841 seconds). Complete default analysis/DuckDB tests and races passed (64.329 / 72.862). The initial native package failed on the retained fixture cases; corrected complete native tests passed (88.683), and race passed (174.037). Affected default API passed (165.691). The first native API race failed on the strict fixture metadata and a staging-admission refusal under then-low disk space. The exact cause of that admission refusal was not independently measured, so it is not presented as proven disk exhaustion. The corrected complete native version-fence test initially failed only on its internal-message expectation. Final complete native tests passed (84.573) and race passed (179.559). The actual affected native API race passed (294.847) with 11,470,626,816 free disk bytes recorded before the sequence. Local checks declare 512 MiB native memory and 128 MiB spill, keeping production defaults unchanged; task-only API no-DWARF compilation is not debugger/native-platform qualification.

Initial frontend typecheck passed (104.030), then lint failed and prevented unit launch. Corrected typecheck/lint and 45 affected units passed; final typecheck/lint and all 47 affected units passed (48.500 / 20.805 / 1.973). The full dashboard cycle passed all 4,919 units and its build, then both browser profiles failed the same two workflow cases: dev-admin 198 passed, four skipped, two failed; local-auth 118 passed, one skipped, two failed. The skips retain their original role/private-fixture reasons. A corrected fresh build passed (47.229 seconds) and all 14 affected workflows passed in each auth profile (68.335 / 133.956 seconds including controller builds). Visual inspection then found stretched buttons and clipped comparison screenshots; the reviewed layout and geometry/hit-test correction requires a fresh affected repeat. The compatibility review also found that older controllers could silently ignore identity fields; explicit capability negotiation and its refusal tests are being verified. The API generation and standard/native-race capability gates passed (39.244 / 224.807 / 220.580 seconds). Independent review also found saved imported selectors on v1/unknown metadata bypassing the first client fence; admission now accounts for the actual selector presence before setters, and retained result saves are refused after capability removal. Legacy managed-run monitor selectors remain available. The first capability/layout client typecheck failed on four Object.hasOwn calls (153.783 seconds). After their same-semantics ES target correction, the second typecheck failed on NodeList iteration in the browser geometry helper (235.360 seconds); Array.from preserves all containment/hit-test/height assertions. The third repeat declares a 2 GiB Node heap to limit pressure on this shared host. The third repeat passed typecheck (55.337 seconds) and lint (34.424), then failed one of 68 affected units because the legacy managed-run fixture omitted the existing empty transaction field. The exact expected fixture was corrected without changing the production selection or assertion. All 68 affected units then passed (18.587 seconds) and the fresh dashboard build passed (152.549). The next dev-admin browser attempt failed at controller linking because the linker reported no disk space; no browser tests executed and local-auth was not launched. Final client/browser verification remains pending. CLI preflight was also required to prevent older decoders ignoring explicit imported selectors: it retains original request bytes, resolves authorized dataset identities/projects, and refuses absent or insufficient versions before requested operations; legacy v1 and managed-run selectors remain usable. Whole CLI normal/race verification remains pending. A successful affected repeat must not be described as a fresh full-cycle pass.

Real SQLite identity persistence passed normally/race (14.178 / 19.846). The actual postgres_integration race passed against PostgreSQL 16.15 (97.591), including nested/special-name v2 import mappings and saved-history checks after closing/reopening the actual store. Exact CLI forwarding tests passed normally/race (44.397 / 64.175), with five cases in each; no empty or skipped commands count as passes. Protected PostgreSQL connection material is excluded from retained evidence.

Independent disclosure review found that mapping credential-named columns as analytical identities could expose preview-suppressed values through normalized series. Shared finite-path admission now refuses those columns before new normalization and before existing-dataset series/comparison artifact access. It retains protected originals and ordinary identity values without rewriting or merging cohorts. Matching dashboard admission and new model/native/router tests are undergoing verification. No unexecuted source fixture is presented as acceptance. A task-owned embedded-index cache audit proved 26 obsolete API archives inconsistent with the freshly built index; exact inode, modification time, size and hash were checked before deletion, reclaiming 3,153,466,856 bytes. Dependencies, current artifacts and shared caches were retained.

The slice preserves the supervised native worker and upload/deletion foundations, published as schema 33 and 34 after main composition. Historical draft receipts used the earlier private numbering. It does not close summary/histogram statistics, partition pruning, broader derived operations, process-restart/HA/remote-backend (same-host restart recovery was later added by the [writer ownership slice](#roadmap-native-writer-ownership-2026-10-25)) or five actual packaged native platforms. The mandatory declared 1 GiB reference import remains open; only 10 GB is waived. Full repository verification from earlier slices is historical, not a fresh all-package result for this slice. Final cache cleanup remains deferred while the full implementation and follow-up checks need these caches. Only task-owned provably obsolete archives were removed; external free-space changes are not claimed as our reclaimed space.

Recovery source inventory (`evidence/RECOVERY_SOURCE_INPUTS.json`) and historical raw evidence (`evidence/RAW_EVIDENCE.json`) retain actual failures, repeats and adoption receipts. The earlier exact-source receipt did not survive the reboot; historical checks do not certify the recovered final draft. Fresh composed-source verification is recorded separately below. Protected PostgreSQL connection material and browser trace bundles are excluded.


<a id="feature-completion-native-identity-2026-10-04--resumed-composition-and-verification"></a>
### Resumed composition and verification

The paused draft was preserved as recovery object `b7ed75ef49f69a7c418733102284f746917744b1` and ported using its exact three-way delta onto published main `5ea8047980f3ac5a66f5b78a96edb26d52c14fa6`. The original dirty worktree and its index remain intact. Generated contracts are rebuilt from the composed source. Main's provider evidence, Dynatrace implementation, exact-window refusal, CLI provider modes and released schema identities remain authoritative.

Fresh backend and repository checks pass. The corrected 11 affected workflows and the fresh complete dashboard cycle pass in each auth profile. This is local verification of this slice, not a full release acceptance record; the open native scale, platform and broader analytics gates above remain open.


The resumed review/fix/review cycles corrected lost imported named selectors on saved reopen, ignored archive identity selectors, CLI safe-remap refusal and ignored commit JSON, and Parquet ASCII identifier ambiguity. The dashboard protected-column vocabulary also now mirrors current main's `apitoken` classification. Exact archive filters retain empty missing populations rather than broadening or manufacturing zero. Physical top-level, nested and canonical Parquet fixtures verify collision refusal; distinct non-ASCII case remains supported. The pinned DuckDB folding oracle passed. [DuckDB identifier semantics](https://duckdb.org/docs/current/sql/dialect/keywords_and_identifiers.html) describe the ASCII folding used by the guard.

Fresh completed gates so far: SQLite identity normal/race (106.561 / 128.625 seconds including cold builds), full CLI normal/race (131.260 / 180.566), actual PostgreSQL 16.15 `^TestPostgres` races (127.869), complete default analysis/DuckDB normal/race (49.003 / 67.664), complete corrected native analysis/DuckDB normal/race (107.879 / 236.224), affected default API (100.638), worker-backed affected native API race (383.350), generation (37.982), and documentation checks (0.436). Times are helper wall durations, not benchmarks. PostgreSQL qualification here is the exact retained selector, not every tagged test or a cloud-provider claim.

The first affected frontend repeat failed one exact-JSON fixture while 122 tests passed; retaining the executed receipt's key order fixes that assertion, and all 123 tests then passed. The first full dashboard cycle stopped at a duplicate timestamp property in a test fixture. The corrected fixture passed typecheck/lint. Complete native normal/race initially failed the positive physical Unicode preview because the pinned legacy test writer injected invalid zero length/precision/scale metadata; the fixture now omits irrelevant annotations and asserts the physical footer. Production guards, case refusal, exact source equality and original-byte assertions are unchanged. The focused and complete native repeats passed. Failed logs remain retained.

The final full dashboard repeat passed typecheck/lint, all 5,544 units in 508 files and the production build. The full Go repository gate passed (1,717.829 seconds including cold builds; `pkg/api` 853.058 seconds). The final CLI race repeat also passed (96.717 seconds) after the shared physical schema guard. The first browser attempt completed dev-admin with 230 passes, five declared skips and two failures. Its old-build local-auth run was interrupted after those findings were fixed; partial results are retained and do not qualify that profile. The final fresh full dashboard cycle passed both complete auth profiles against the corrected source; the historical failed and interrupted attempts remain separate. Frozen final source hashes (`evidence/SOURCE_INPUTS_FINAL.json`), initial inventory (`evidence/SOURCE_INPUTS_INITIAL.json`), frozen embedded dashboard hashes (`evidence/EMBEDDED_DASHBOARD_FINAL.json`), and actual PostgreSQL fixture (`evidence/POSTGRES_FIXTURE.json`) separate current-source verification from historical receipts.

The actual browser review found a narrow-container overflow in imported identity filters and an incorrect matcher target for disabled native options. The layout now wraps and bounds every control; the browser fixture retains center hit-testing and adds long valid labels and child overflow checks. Native option checks assert the exact disabled property. A forced invalid protected selection must produce the full mapping disclosure refusal, disable validation/save and issue no mapping/save/commit requests; restoring the previous safe selection clears the refusal. All 123 affected units, typecheck and targeted lint passed. Independent review of these fixes found no further actionable issue. The final fresh full cycle passed the browser acceptance gate for this slice in both auth profiles.

The narrow-screen correction now uses a zero-minimum grid track and zero-minimum immediate children, preserving every viewport geometry, center hit-test and button-height assertion. Fresh typecheck, targeted lint and build pass. All 11 affected workflows pass in dev-admin and local-auth (24.738 / 103.365 seconds including helper work); the final fresh complete cycle passed typecheck, whole lint, all 5,544 units in 508 files, its build and both complete browser profiles. Independent final source review found no further actionable issue. The earlier failed and interrupted cycles remain retained; they are not relabeled as passes. The earlier source inventory included a disposable tsbuildinfo file and predates the final UI fixes; the separately named frozen inventories exclude caches and retain the exact final source/build bytes.

The final complete `npm run test:cycle` exited 0. Dev-admin completed 232 passed and five declared skips (8.7 minutes); local-auth completed 163 passed and one declared skip (24.2 minutes). There were no browser failures. The complete helper wall duration was 2,944.142 seconds; profile durations are the rounded Playwright summaries, not benchmark measurements. The final raw cycle log (`evidence/resume-frontend-full-cycle-final.log`) and hashed command/environment receipt (`evidence/resume-frontend-full-cycle-final.json`) retain the actual result. The six skips retain their role/private-fixture reasons and do not count as executed acceptance. Source remained frozen during this cycle; this documentation update does not expand the capability claims or close the explicitly open acceptance gates.

<!-- Original record: docs/qa/reviews/feature-completion-native-partitions-2026-10-05/CONTRACT.md; local evidence kept outside git -->
<a id="feature-completion-native-partitions-2026-10-05-contract"></a>

## Native partition-pruning slice contract


Prepared read-only against `codex/native-partition-pruning` at `ce9106e76d49721e49f7091711ba536b25c63872`, 2026-10-05. This is a proposed implementation boundary for parent review. No repository source was changed and no tests or native jobs were launched. The owned worktree was clean when inspected.

<a id="feature-completion-native-partitions-2026-10-05-contract--existing-behavior-and-concrete-seams"></a>
### Existing behavior and concrete seams

- `pkg/analysis/duckdb/native.go:237` normalizes one immutable Parquet object per original-file/mapped-measure pair, up to 1,024 outputs. Mapping v1/v2 preserves selected scalar identities; long-table `NameColumn` can put several metric names in one object. Normalized rows include missing values with `quality=missing`; rejected rows are separate artifacts. Summary scalars are admitted by mappings; distributions/histograms are not a new operation in this slice.
- `pkg/api/analysis_attempt_pipeline.go` already copies originals, checks normalized-output hashes, joins native/output owners, seals physical quiescence, and publishes through `PublishAnalysisImportAttempt`. `pkg/storage/analysis_attempts.go` owns the exact output plan, input digest, project/import/revision/lease fences and uncertain-commit receipt. `AnalysisImport` stores normalized object keys/hashes, but not authoritative sizes, row counts, time bounds or partition categories.
- `pkg/api/analysis_comparison_pipeline.go:selectAnalysisDatasetParts` currently chooses by ordinal modulo mapping-measure count, then stats every selected object. It has no producer-verified time/identity proof. The comparison plan key is only `(dataset, metric)`; it must change before selector-dependent pruning. The current HTTP comparison request shares one From/To window, but different left/right filters already exist, and the lower-level query carries its own window.
- `pkg/api/analysis_queries.go:queryAnalysisDataset` stages complete selected objects and verifies their SHA-256 before passing approved handles to the contained child. Native identity/schema/quality admission occurs across the whole selected population. SQL is generated from typed operations; no arbitrary SQL/path contract is exposed.
- `childNativeSeries` applies `[From, To)` before counter `lag`. It has **no out-of-window predecessor lookback**. The first in-window counter sample is missing; resets, duplicate instants and missing values invalidate the affected bucket. Its rate lineage explicitly remains epoch-unqualified. This slice must retain that algorithm, rather than introduce an undocumented boundary improvement.
- A native query with no selected rows currently returns `[]`; it does not synthesize measured zero or identity-bearing missing buckets. Once actual selected rows establish identity/kind/unit, gaps become explicit missing buckets. Pruning must preserve this distinction.
- Existing metadata budgets are 2 MiB per import and 4 MiB per attempt journal. Worker IPC is version 2 with 8 MiB frames, 1,024 inputs, 2,048 outputs. Default resource controls remain one heavy pipeline, two threads, at most 2 GiB DuckDB memory and 8 GiB spill, 15-minute normalization, one-minute API queries, combined-RSS/headroom watchdogs, private descriptor ownership and existing disk admission.

<a id="feature-completion-native-partitions-2026-10-05-contract--recommended-first-vertical-typed-inline-immutable-manifest"></a>
### Recommended first vertical: typed inline immutable manifest

Use a versioned `NormalizedPartitionManifest` in the **existing authoritative AnalysisImport payload and its published attempt receipt**, not a second metadata owner. Root approved this inline approach during review preparation. An inline manifest is sufficient for NA-2's durable manifest and enables selection before *any normalized-object Stat/Attributes/Read*. It avoids adding a new JSON output role or physical object namespace. The manifest is durable metadata, not a DuckDB cache. The separate-artifact alternative below is informational and is not the approved slice.

The manifest has: contract version; producer/algorithm version; project and dataset; source/mapping fingerprint and mapping version; canonical measurement schema version and identity version; a canonical digest; and exactly one entry per normalized object. Each entry contains its exact owned key, SHA-256, byte size, physical row/accepted-measurement count, valid/missing counts, optional inclusive UTC min/max over **all** rows, and bounded explicit completeness states for metric and full identity. Counts use checked arithmetic. Empty files have zero counts and no range. Nonempty files must have a finite ordered range when time pruning is advertised. Sizes are at least a physical Parquet header/footer and remain subject to the current 20 GiB geometry bounds.

The minimum useful proof is conservative homogeneous categories:

- A metric proof is either `constant` with the exact bounded metric value, or `mixed/unknown`. A constant wide mapping is not by itself the proof: verify the actual produced object.
- Full identity is either `constant` with source, host, service and the exact complete canonical dimension object (including absent vs present-empty keys), or `mixed/unknown`. Do not concatenate ambiguously delimited values or reconstruct dimensions from filenames. Independently varying marginal columns must not be treated as a measured joint tuple.
- Kind, canonical unit, schema and identity versions are explicit. They qualify supported operations; they are not implicit query filters. A query that otherwise selects two units, kinds or full identities must still trigger existing native refusal, not silently lose one variant through pruning.
- Missing values participate in row counts, categories and time ranges. `valid_count=0` is **not** a reason to prune a nonempty all-missing file. Unknown/unsupported quality cannot be turned into an empty healthy result. Newly produced rows should only contain the existing valid/missing semantics; a producer that sees other states refuses publication.

Bound entries to 1,024 and the encoded manifest to at most 1 MiB **and** the remaining existing import/journal budgets. This is a **total** encoded bound, not 1 MiB per entry or a product of independently admitted dimensions. Preflight the full normalize IPC response (8 MiB including existing outputs/rejection samples), entire import/result-digest encoding (2 MiB including files/chunks/mapping/output hashes/manifest), and complete journal/receipt encoding (4 MiB including the published import and journal fields). Count JSON escaping and object/list overhead before allocating clones or encoding; reserve enough room for required structural fields before optional identity summaries. Use bounded typed encoding/decoding before allocations; reject unknown fields, duplicate object keys, duplicate identity dimension keys, malformed UTF-8, unsupported contract versions, impossible counts/ranges, arithmetic overflow and digest discrepancies. If category cardinality/value encoding exceeds the remaining global summary budget, mark that **whole** category proof unknown and retain the object. Never truncate an object list, time range, rows or values. If required manifest structure itself cannot fit, fail the new import explicitly while retaining originals/rejection provenance; no partial ready dataset.

The digest covers a deterministic canonical encoding excluding the digest field itself. Publication/result digest additionally covers the entire manifest and ties it to the exact output-key/hash population and mapping/input fingerprint. Ready/cancel/deleting/deleted saves retain the immutable manifest; association/name changes cannot replace it. Absent legacy manifests remain readable through full-object semantic admission. A present invalid/unsupported/tampered manifest returns a bounded explicit error before object I/O; do not downgrade corrupt metadata to a successful empty query.

<a id="feature-completion-native-partitions-2026-10-05-contract--producer-ipc-and-publication"></a>
### Producer, IPC and publication

Compute partition proof from the actual immutable normalized output under the pinned contained worker, after COPY/file sync and before publication. A bounded approved aggregate over each installed output measures count/quality/range and detects whether metric/full identity are homogeneous. It must never materialize an unbounded DISTINCT set. Decode any retained dimension object using the shared normalized-dimension validator and existing canonical schema/case-binding guard. Time ranges cover all accepted rows, including null metric values.

Extend typed normalize output metadata with the partition summary, tied to its existing output handle/hash/size/row count. Only normalized outputs carry it; rejection outputs cannot claim categories. Parent result validation checks per-output identity, rows/count sums, schema/mapping versions, all bounds and encoded IPC size. Parent already verifies physical hashes/rows: require summary provenance from that exact installed output and retain its immutable descriptor identity until copy completes. Parent assembles owned artifact keys, confirms the remote/local write digest matches the worker output digest, and builds the canonical inline manifest. No parent raw SQL and no caller-supplied summary acceptance.

The attempt claim advertises a new required-manifest producer version for **new attempts**, leaving old journals readable. Extend input/publication/result digests and `ValidateAnalysisPublication` to require the manifest for new claims, exact one-to-one key/hash correspondence, reconciled accepted counts, and matching input/mapping fingerprint. Only the owned publication method can add it. Extend `analysisImportOwnedOutputsEqual`, initial-output refusal, ready-state immutability and normalizing-output equality so generic Save cannot inject, remove or replace it. Claim/retry must explicitly clear the prior manifest and its digest before replacing normalized output plans; otherwise a prior proof can accidentally authorize new outputs. A manifest/version set on the new claim identifies required producer behavior, not completed statistics. Old attempt resumption cannot invent a proof. A retry creates a new owned attempt/manifest and publishes atomically or remains invisible. Publication replay/Resolve must compare a digest that includes the manifest, so equal files with different claimed ranges/identities cannot resolve as the same committed outcome.

Manifest retention/recovery is part of this metadata owner: include it in published journal receipts, uncertain-commit resolution digest matching, obsolete/retry-state transitions, migration/JSON compatibility, and ready/deleting/deleted immutability tests. Inline metadata requires no additional physical deletion key, but dataset/project deletion must still revoke results and not allow a late manifest-bearing writer to resurrect a ready import. Keep the existing exact artifact cleanup and physical-writer seals unchanged.

If root chooses a separate artifact instead: pre-plan an exact `manifest-<attempt>.json` key and JSON quota at Claim; write canonical parent-built bytes through existing artifact IO; store its key/size/hash plus bounded authoritative selection copy; include it in all owner/result digests, journal known-key/obsolete/quiesced cleanup sets and direct deletion keys. Add a precise manifest-key validator distinct from the normalized-Parquet regex. Verify artifact bytes/digest before trusting the copy, with its bounded IO charged to the same pipeline. Do not broaden all Parquet roles to arbitrary JSON or treat metadata deletion as physical cleanup proof. This adds enough lifecycle surface to merit a separate reviewed decision.

<a id="feature-completion-native-partitions-2026-10-05-contract--query-selection-contract"></a>
### Query selection contract

Replace ordinal selection with `Plan(datasetSnapshot, effective SeriesQuery)`. Bind the effective identity version and merge named transaction/protocol/group filters into dimension selectors **before** making a plan. Validate the query and immutable manifest before any normalized object stat/download. For legacy/no-manifest input, keep every normalized object; existing hash/schema/quality/measurement guards remain authoritative. No inferred size/category from object names or source directories.

Exclude an object only with producer-qualified proof of no contribution:

1. Zero physical rows.
2. A constant metric differs from requested metric.
3. Inclusive max timestamp `< From` or inclusive min timestamp `>= To` for the existing half-open window.
4. A constant full identity disagrees with any explicit source/host/service/dimension selector. Missing a requested dimension differs from present-empty; omission of a query filter means any value and cannot select only the default tuple.

Canonical timestamp precision is part of admissibility. Normalization emits `TIMESTAMPTZ` via casts/AT TIME ZONE/to_timestamp. Pinned local driver `v2.10506.0` README (TIMESTAMP vs TIMESTAMP_TZ), C binding header and table_udf_test show microsecond storage; the pinned test casts `.123456789` to `.123456000`. Derive bounds from the produced timestamp column, never source nanoseconds or formatted filename dates. Require explicit UTC (`Z`) canonical encoding, valid years 1–9999 and nanoseconds divisible by 1,000; reject noncanonical claimed bounds rather than rounding them into a pruning proof. For empty objects use absent bounds, not zero sentinels. `SeriesQuery` currently accepts nanosecond From/To and embeds RFC3339Nano literals that DuckDB casts to TIMESTAMPTZ. **Disable time pruning if either requested boundary is not microsecond-aligned**; still apply safe metric/full-identity pruning and let the unchanged native query adjudicate the timestamp window. Comparing a physical max `.123456` with raw Go From `.123456789` would incorrectly drop a row that the native cast admits. Do not change all query timestamps, bucket origins or rounding behavior as an incidental optimization. Add real positive/negative-epoch, non-UTC input and +/-1 ns physical boundary cases before expanding this precision contract.

Mixed/unknown categories remain selected. Do not filter by valid count, kind/unit not requested, counter monotonicity, percentile availability or unsupported-quality flags. The selected native query must still perform complete population admission and reject incompatible/malformed evidence. The manifest cannot waive physical hash verification for selected objects or replace semantic validation. Pruned corruption outside an independently proved selection does not invent selected evidence; selected corrupt objects always fail.

For current counter rates, select the same `[From, To)` population; no extra lookback or dropped predecessor *inside* that window. Test adjacent samples spanning selected files, resets, duplicated timestamps across files and missing predecessor rows. Any future lookback is a separately versioned operation: its explicit effective scan From/To must be in the plan key and must include all predecessor candidates, never a guessed nearest partition. Quality and multi-identity admission for a future lookback need a separate contract.

Key retained comparison plans by full effective immutable `SeriesQuery` fingerprint (project/dataset, metric, identity version, source/host/service, sorted exact dimensions, From/To UTC, aggregation, bucket size and algorithm/catalog version), plus dataset/artifact/manifest revision/digest. Do not use `(dataset,metric)`. Build plans for both sides independently; only exact equal fingerprints may share metadata. Charge both staging copies, even when identical, and the largest sequential child as current geometry requires. Different filters/versions/window bounds cannot borrow another side's reservation. Snapshot the manifest deeply, keep at most two plans, and recheck existing project/import state/revision/association fences before serving even a zero-selected result.

Qualified manifest byte sizes determine admission without Stats. Download only selected objects, enforce exact byte size (not merely <= cap), sync/close, verify SHA-256, then hand approved descriptors to native. Legacy paths may still Stat under existing pipeline IO ownership. Include selected/pruned object count and exact manifest declared bytes in bounded explain diagnostics; label those as object-transfer evidence, distinct from DuckDB physical scan bytes. Unknown sizes cannot be advertised as measured avoided bytes. No persistent cache/reuse optimization is required in this first implementation.

Projection/filter pushdown remains generated approved SQL. Refactor a shared statement builder only if necessary to test the **exact** production normalization/quality/identity/aggregate statements. No new runtime SQL API. Keep all columns needed by quality, identity, kind/unit/version and rate admission even when the chart needs only value/time. Show genuine EXPLAIN/EXPLAIN ANALYZE operators from the pinned native engine for predicate/projection pushdown and row-group scan reduction where supported. Do not infer physical scan bytes from output size or a planner helper count; retain actual available engine profiling fields with truthful unavailable markers.

<a id="feature-completion-native-partitions-2026-10-05-contract--ownership-split-and-files"></a>
### Ownership split and files

1. Shared contract owner: new `pkg/analysis/partition_manifest.go` and adjacent tests; shared versioned structures, canonical digest, bounded validation, constant/mixed semantics and pure safe selection. No new top-level package.
2. Native/IPC owner: `pkg/analysis/duckdb/types.go`, `native.go`, `worker_contract.go`, `worker_result.go`, `worker_encoding.go`, `worker_native_executor.go`, worker-engine mapping and adjacent native/wire tests. Extend controlled output summary, not arbitrary operations.
3. Durable metadata owner: `pkg/storage/analysis_imports.go`, `analysis_attempts.go`, `analysis_cleanup.go` only if lifecycle fields change, and adjacent SQLite/PostgreSQL publication/immutability/recovery tests. Use existing JSON payloads/CAS; no separate metadata database or SQL table necessary.
4. API staging/planner owner: `pkg/api/analysis_comparison_pipeline.go`, `analysis_queries.go`, `analysis_attempt_pipeline.go`, `analysis_imported_identity.go`, query authority/deletion only where required, and adjacent tests. No monitor/run semantics change.
5. Root integration/evidence owner: API/TypeScript generation if public metadata/diagnostics are exposed, inventory/docs/status, reproducible fixture runner and QA receipts. CLI/dashboard controls can remain unchanged for transparent pruning; exposing optimization capability must distinguish verified object selection from measured scan/scale qualification.

Do not let native and storage owners independently rename manifest fields or producer versions. Review the contract first, then implement one agreed shared type; native and storage work may parallelize after that shared interface freezes. Integration source must freeze before meaningful native/default/race/migration gates.

<a id="feature-completion-native-partitions-2026-10-05-contract--meaningful-acceptance-and-review-sequence"></a>
### Meaningful acceptance and review sequence

Unit/property tests: conservative selection never excludes a generated contributing row; exact half-open time boundaries; empty vs all-missing; constant/mixed and absent/present-empty dimensions; non-ASCII identities; bounded unknown fallback; duplicate/missing/reordered artifacts; count overflow and malformed digest/version/range; immutable snapshot mutation; full-query comparison key separation. These tests prove contracts, not DuckDB/provider performance.

Physical native golden fixtures: real Parquet long/wide/nested identity inputs, distinct hosts/source/service/dimensions, several timestamps per row group/file, empty outputs, missing values, resets/duplicate counter instants across file boundaries, scalar summaries and unsupported percentile requests. Normalize through the actual worker; independently reconcile output hash/size/schema/rows/statistics; query pruned vs forced full-scan over the same immutable dataset and require byte-equivalent points, quality, lineage and errors. Differing left/right identity queries on one dataset/metric must each receive correct files and geometry. Both query windows/counter first-missing behavior must agree.

Object I/O proof: instrument the real artifact boundary or controlled blob service for exact Stat/Attributes/Read calls; excluded objects are never touched, selected hashes/sizes are checked, zero selection performs no normalized object I/O, legacy selects all. Fail access to an excluded object to demonstrate no download; fail selected object access to demonstrate explicit refusal. Do not replace internal ownership/storage with a mock for the only acceptance evidence. Cross-project, concurrent deletion/reassociation, late cancellation, stale publication lease, worker-result tampering, uncertain commit and retry/HA takeover must include the manifest and retain existing physical-cleanup proof.

Actual pushdown proof: use pinned DuckDB Go `v2.10506.0` / bindings `v0.10506.0`; run EXPLAIN ANALYZE on production-generated statements under approved test-only native access, record projections/filter operators and actual available file/row-group/operator scan fields. Test wide originals containing a substantial unrelated column so normalization projection is observable. A helper string asserting a WHERE clause is insufficient. If a metric is unavailable from the engine's profiler, mark it unavailable rather than deriving it from artifact sizes. Audit diagnostics for paths/credentials; retained public evidence uses hashes and sanitized identifiers only.

Mandatory measured scale gate remains **1 GiB compressed**, multi-file and actual on-disk size checked, not repeated highly compressible rows whose logical size is 1 GiB. Use deterministic seeded bounded generation with metric/time/full-identity partitions, a separately hashed fixture manifest, exact independent rows/count/sum/percentile/reset/missing oracle, and at least 32 GiB admitted scratch on a documented 4 vCPU/16 GiB host. Record fixture/original/normalized hashes; normalization/uncached/repeated/cold-replica query durations and selected workload P95; selected/pruned object and bytes plus actual engine scan evidence; combined RSS/spill; cancellation/worker crash/disk exhaustion/contention; and simultaneous reference live-telemetry lag. Existing limits must remain active. No controller crash, unexplained loss, headroom breach or sustained configured telemetry-SLA breach is acceptable. P95 target five seconds is proposed and must be measured; exact source/build/tool/platform/limits and failed attempts remain in immutable receipts.

The current host reports about 82 GiB free on the shared volume; that is a point-in-time disk observation, not reserved benchmark capacity. CPU/RAM read via sysctl was denied by this sandbox. Root must perform actual admission/hardware verification without disturbing other active workers before scheduling the mandatory fixture. No benchmark has run in this prep task. The 10 GB benchmark remains waived only; packaged five-platform controller/desktop execution, richer histogram/summary operations, arbitrary directory partition-value precedence and native release breadth remain separate open gates.

After source changes: affected shared/native/API/storage tests; targeted meaningful races; real SQLite/PostgreSQL publication/cleanup/integration; generated API/TS freshness if changed; documentation checks; full composed `go test ./...`; actual native golden/pushdown and 1 GiB measured gate; independent review/fix/review with final-source hashes. An incomplete scale/provider/platform gate must stay open in backlog/capability text rather than certify the full NA-2/NA-6 program.

<!-- Original record: docs/qa/reviews/feature-completion-native-partitions-2026-10-05/PRECISION_DECISION.md; local evidence kept outside git -->
<a id="feature-completion-native-partitions-2026-10-05-precision-decision"></a>

## Canonical native query window decision — 2026-10-05


The initial slice contract preserved existing counter boundary rules and proposed disabling time pruning for raw submicrosecond windows. Physical native review found a pre-existing seam: SQL cast timestamps to microseconds while gap filling used the original nanosecond origin. A valid submicrosecond query could therefore erase actual values into missing buckets or add an extra endpoint bucket. Keeping that seam would make full-scan/pruned parity misleading.

The corrective boundary is explicit and tested. The contained child obtains the actual SQL-cast bounds, reports them through a private typed proof, and fills only that grid. The parent validates bounds, count, precision and deadline. Dataset API queries/comparisons and managed overlays disclose one UTC microsecond window, align both sides before bucketing, and preserve original source timestamps and run start. Native fixtures cover positive and pre-epoch timestamps, offsets and collapsed windows against actual SQL casts. The public API refuses a collapsed effective window; the lower-level native operation retains its bounded empty result.

This does not add counter predecessor lookback, prorate incomplete publication rates, average percentile scalars or change raw [from,to) versus merged-publication (from,to] cohort membership. Whole-publication coverage remains authoritative. Artifact selection is separate from physical scan-byte/RSS/scale claims. The 1 GiB and five-platform acceptance remain open.

<!-- Original record: docs/qa/reviews/feature-completion-native-rca-identity-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-native-rca-identity-2026-10-05"></a>

## Portable measurement series identity — scoped qualification


This backend prerequisite supplies one versioned, full SHA-256 identity for compatible logical measurement series and declared observation grids. It factors the existing measurement/provider comparisons through the same typed projection while retaining their exact equality, ignored fields and allocation behavior. Values, run/dataset ownership and clocks do not change identity; units, complete derivation, population/version and provider provenance do. UTF-8, structured field boundaries, explicit modes and bounded encoding protect the identity from ambiguous input.

Normal and race package checks (`evidence/candidate-inventory.json`) pass. The adjacent tests independently compare each point/provider field against the previous comparators, verify zero allocations in hot comparisons, and cover nil/empty containers, NUL boundaries, invalid UTF-8, escaped-label bounds, exact grid limits, concurrent determinism and input nonmutation. Legacy or unadapted measurements remain unqualified. Documentation and whitespace checks (`evidence/identity-docs-and-diff-check-1.json`) also pass. The independent parent review (`evidence/parent-review.json`) found no additional issues in this four-file scope. The evidence index (`evidence/evidence-index.json`) pins the retained raw logs, candidate and source observations.

The confirmed in-progress engine is `claude/rca-correlation-start`. The adapter plan (`docs/qa/reviews/feature-completion-native-rca-identity-2026-10-05/evidence/rca-adapter-plan.md`) reuses its existing analysis/detection/graph and durable evidence/job contracts. Its source inventory (`evidence/rca-adapter-source-inventory.json`) records read-only working-file observations, not an atomic or accepted engine release. No protected engine or primary source was changed.

Identity alone grants neither project authorization nor baseline/coverage qualification. This slice adds no runtime RCA/native adapter wiring, public capability, API, IPC, metric catalog or artifact-schema change. Authoritative engine adoption, compatible native adapters and the composed full Go gate remain required before merge. Native 1 GiB/reference-host, platform, restart/HA and provider acceptance remain open; only the 10 GB gate was waived.

<!-- Original record: docs/qa/reviews/feature-completion-native-upload-2026-10-04/README.md; local evidence kept outside git -->
<a id="feature-completion-native-upload-2026-10-04"></a>

## Durable uploads and complete dataset deletion


Status: implemented and reviewed as a durable upload and deletion slice. The review/fix/review cycle has no remaining actionable findings in this slice; the full completion program remains in progress.

The schema-30 upload journal records an immutable writer identity, exact object/temporary keys, scope/revision/offset/hash and physical completion before accepted bytes advance the durable upload cursor. Complete publishes the physical seal and cursor in the same transaction. Request cancellation, deadlines, panics, Goexit and failed Close outcomes cannot supply physical closure. Bounded parents retain ambiguous outcomes and their exact reconciliation state across a same-process Server replacement; a replacement cannot redirect the SQL or artifact namespace.

Deletion first revokes the dataset and requires durable proof that every normalization and upload writer has quiesced. It validates the complete bounded exact key plan before I/O, resolves opaque accepted chunk keys from durable metadata and preserves historical key decoding. It joins whole dataset namespace deletion before a terminal successful response, including closed abandoned object and temporary siblings absent from the accepted cursor. The storage destination is captured and checked before metadata mutation; substitution during a blocked metadata call returns pending without touching either namespace.

Review findings and corrections: unused handler import found by actual generation; Goexit retained as unknown independently of panic; a lifecycle fixture incorrectly expected cleanup readiness while its dataset was still live; actual DELETE204 retained the source because deletion recomputed a legacy filename; whole namespace deletion was needed to remove closed abandoned/temporary objects; empty-chunk file IDs needed finite canonical validation before key allocation; storage destination needed pinning before blocked metadata work; and the backend-barrier fixture needed the actual `_analysis` filesystem prefix. The readiness, filesystem-prefix and default-build fixture corrections changed no production readiness or storage semantics. The first default-build deletion regression attempted native upload under an unavailable build and correctly received 501. The corrected fixture asserts this refusal and unchanged cursor, then creates a physically closed accepted object through the real durable writer API; the native build retains the actual HTTP upload. Both exercise actual router deletion and require accepted, abandoned and temporary files to disappear.

The first generation failed in 12.218 seconds. Its gate record and unused-import diagnosis are retained; the original raw log was inadvertently overwritten by the successful repeat, so there is no claim to preserve that missing raw output. The complete tagless storage and targeted tagless storage race commands checked SQLite, despite having a PostgreSQL environment variable. The later explicit `postgres_integration` race command actually compiled and passed PostgreSQL 16.15 migration/integration tests, including the two-store upload authority and invisible BEGIN commit/rollback fences. These invocations are reported separately.

The initial affected default API tests passed; the first actual native API race failed on the live-state readiness fixture. After that fixture correction, the actual native lifecycle found retained source bytes after DELETE204, which drove the production deletion corrections. Both failures remain evidence. Dashboard generated contracts passed typecheck and lint (63.969 / 31.215 seconds). The first corrected default API attempt failed only on the unavailable-build fixture; its failure is retained. Final affected default API passed (98.443 seconds), actual native API race passed (232.640 seconds), and API/TypeScript generation passed (30.094 seconds). The complete tagless storage and storage race checks passed, as did the focused upload storage race. The actual tagged PostgreSQL integration race passed (75.202 seconds), with no skipped PostgreSQL tests. Thirteen affected analysis/preparation/dashboard browser workflows passed in each auth profile (63.755 / 125.227 seconds including fresh controller builds). Query/chart and saved-panel screenshots were inspected in both profiles. Those browser API fixtures verify the dashboard workflow; the separate native API tests exercise real Parquet normalization and physical deletion. Final documentation verification is recorded with the committed evidence.

Legacy uploads lacking an authoritative writer version remain pending and retain bytes. This slice does not prove process-restart recovery, controller HA takeover, remote object-store qualification, five actual packaged native platforms, or the mandatory declared 1 GiB benchmark. The later [writer ownership slice](#roadmap-native-writer-ownership-2026-10-25) adds process-restart recovery of upload writers and normalization attempts for a successor on the same host and in the same PID namespace; cross-host HA takeover and proof that a remote commit cannot still land remain open, and those datasets stay explicitly deletion-pending. Only the 10 GB benchmark is waived. Cache cleanup remains deferred while the authorized full implementation and follow-up checks still depend on the caches.

The ordinary Go build remains unavailable for native upload; no capability guard was relaxed. Local race checks declare the bounded 512 MiB DuckDB / 128 MiB spill profile and use the task-only API no-DWARF compiler profile, without changing production memory defaults. SQLite and actual tagged PostgreSQL checks cover schema-30 migration and writer authority; native API checks cover final source/normalized cleanup and backend substitution barriers. Full repository validation from the preceding foundation is historical, not a fresh all-package pass for this slice.

Exact source inputs (`evidence/SOURCE_INPUTS.json`) and raw evidence (`evidence/RAW_EVIDENCE.json`) retain adoption receipts, actual failed attempts and successful repeats. The initial missing generation raw output is explicitly excluded from preservation claims. Protected PostgreSQL credentials and connection material are excluded.

<!-- Original record: docs/qa/reviews/feature-completion-native-worker-2026-10-04/README.md; local evidence kept outside git -->
<a id="feature-completion-native-worker-2026-10-04"></a>

## Native worker and durable publication foundation


Status: implemented and reviewed as a local foundation slice. The review/fix/review cycle has no remaining actionable findings in this slice. This slice introduces supervised native execution, whole-pipeline reservation, bounded typed IPC, exact descriptor ownership, schema-29 attempts and publication/deletion reconciliation. It does not close the native analytics completion track.

Review corrections include overflow-checked disk admission; exact optional-output equality; inherited Unix command-pipe poller registration; retained writer-quiescence proof; resource observation after sole process wait; exact parent-bound resource profiles; build-profile linkage; transient/ambiguous SQL acknowledgement retention; final query/preview metadata and actual deadline admission; and immutable startup SQL ownership. The worker now binds its already-owned command pipe as stdin before process Start, eliminating an otherwise implicit /dev/null descriptor and its unchecked internal close. An actual child checks stdin identity and complete physical join/release. The Windows inherited-command Close regression is included in native CI selection. Foreign compilation is not native runtime qualification.

The actual API lifecycle found that its registered staging root contains the nested worker executable, copied input, output and spill. The parent quota now uses the exact already-reserved combined geometry, with current admitted expansion checked under the pipeline mutex. Separate role limits, headroom and RSS checks remain. Quota observation permits disappearance only of enumerated nested scratch entries; missing roots, replacement, symlink, I/O and Close failures remain errors. Disappearance supplies no process or descriptor proof. A regression copies the actual production executable and runs the real child below registered staging.

The same run found that physical pipeline completion can precede the API retry record's final map retirement. Tests now await the exact terminal record channel. The native lifecycle passes preview, normalization, series, comparison, association and overlays. Legacy uploader deletion is intentionally pending: cancellation hides the dataset, retains source bytes and queues cleanup until physical-writer proof exists. The upload-ownership successor must deliver final deletion; this foundation does not waive it.

Operator profiles retain two threads and maximum 2 GiB engine memory / 8 GiB spill. Earlier local native API checks used 256 MiB / 128 MiB; final race/API and packaging checks declare 512 MiB / 128 MiB. Neither profile bypasses process/container RSS, scratch admission or publication fences. Compiled native availability, accepted configuration and platform qualification are separate.

The task-only API no-DWARF Go compiler profile preserves normal executable stack/race information but does not qualify release debugger/native-C packaging. Failed attempts are retained in the evidence set. Narrow scratch recovery removed only proven obsolete archives from this task's private cache, retaining current outputs, production artifacts, dependencies, deliverables and shared caches. Final cache cleanup remains deferred until the full requested program no longer needs the caches.

Mandatory acceptance still open: five actual packaged native platforms and fresh install/uninstall; Darwin loaded-image/hard-tree authority; protected restart/HA writer recovery beyond the same-host controller-exit proof later added by the [writer ownership slice](#roadmap-native-writer-ownership-2026-10-25) (cross-host HA, container restarts, worker staging reclamation); durable upload and remote writer qualification (remote commits stay unproven there); resource/performance instrumentation; and the declared 1 GiB reference import. Only the 10 GB benchmark is waived. The local PostgreSQL 16.15 fixture is private and disposable; protected connection material is excluded from this record.

The full default Go run passed API (997.584 seconds), storage, all command/protocol/distributed/analysis packages and the remaining repository packages, except one cancellation regression in engine. Its real 30 ms sleep overslept under scheduling load and invalidated a test-only 15–70 ms assumption. The isolated regression now uses Go 1.26 `testing/synctest`, preserving the exact production code and original elapsed-work assertions. Twenty normal repeats, the complete engine package, and twenty race repeats pass. The initial full-suite failure remains in raw evidence (`evidence/RAW_EVIDENCE.json`); this record does not claim that failed invocation passed.

Post-correction complete default DuckDB, actual native DuckDB race and actual native API race pass. Actual private PostgreSQL 16.15 integration and native/analysis/concurrent-initialization race checks passed before the final process-fixture-only changes. The generator/collector dependency-exclusion check and CGO-free unavailable profile passed. Dashboard typecheck, lint, 27 adjacent unit tests and production build passed. The host controller native build passed; all four invalid worker startup cases returned exit 78 without filesystem changes. Affected analysis/preparation/dashboard browser workflows passed all 13 tests in each auth profile. Query/chart and saved-panel screenshots were inspected; Parquet browser API fixtures are distinct from the actual native API lifecycle tests. The initial packaged desktop build passed, but its invalid-mode startup gate failed with exit 2. Exact diagnosis found a runtime netpoll fatal which could also race to exit 78: role validation occurred after adopting and closing foreign runtime descriptors. The production correction validates every raw Unix role before any wrapper, flag mutation or Close; the final corrected controller and desktop builds and all silent invalid-mode cases passed. The actual role regression also proves invalid descriptors and flags remain unchanged and the runtime poller continues operating. The startup gate now refuses unexpected stdout/stderr even with exit 78 and retains failed-case evidence. Its five unit tests passed after a sandbox-only localhost-sentinel refusal was rerun with authorized local access.

Final correction gates: complete default DuckDB passed (45.616 seconds); actual native DuckDB race passed (134.423 seconds); actual native controller build and strict startup passed (41.183 / 1.631 seconds); host packaged native desktop build and strict startup passed (219.487 / 2.045 seconds); documentation checks passed. The corrected source compiles with CGO disabled on Linux amd64/arm64, macOS amd64 and Windows amd64; the host CGO-free test suite passed (41.171 seconds). These foreign builds are source/link checks only.

Exact source and embedded-dashboard hashes, build profile and final executable hashes are retained in source inputs (`evidence/SOURCE_INPUTS.json`) and packaged binaries (`evidence/PACKAGED_BINARIES.json`). Raw evidence (`evidence/RAW_EVIDENCE.json`) preserves every recorded failed attempt and successful repeat, including the initially failed desktop startup and its fatal-output diagnosis. Source review of the final descriptor validation and corrected child/cleanup fixtures was clean in three independent bounded reads. Browser tests were unchanged by the final worker-only correction.

<!-- Original record: docs/qa/reviews/roadmap-analytics-local-wall-times-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-analytics-local-wall-times-2026-10-19"></a>

## Roadmap implementation: local-wall timestamp qualification


Date: 2026-10-19. Scope: a bounded [NA-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics) timestamp-correctness slice following `dd397bbd7`.

<a id="roadmap-analytics-local-wall-times-2026-10-19--contract"></a>
### Contract

New local-wall normalization requires one unique instant in the selected source timezone. A compiled-in native guard checks calendar components against the timezone's intervals and requires agreement with the native conversion. Repeated times, skipped times/dates, timezone-database disagreement and incomplete bounded resolution are rejected. The search is limited to 32 intervals within 48 hours either side and offsets within ±24 hours; it never guesses a one-hour transition. Explicit-offset and timezone-aware absolute inputs retain their instant. Historical canonical artifacts and query readers are not rewritten.

Raw, summary and histogram paths share this guard. Default refusal publishes no output. Explicit acceptance of rejected rows preserves the existing file/measure/source-row rejection artifacts and source hashes. Mapping/API versions, worker isolation, disabled external access, memory/spill limits and output ownership remain unchanged. The helper is a fixed compiled-in function, not caller-supplied SQL or an extension download.

<a id="roadmap-analytics-local-wall-times-2026-10-19--validation-and-review"></a>
### Validation and review

Completed checks:

- Full native analytics suite and focused race checks passed, including raw/summary/histogram refusal, typed absolute-time compatibility, historical non-hour transitions, source non-mutation and bounded traversal.
- Four existing native API import/rejection-download/summary/histogram workflows passed. Generated acceptance inventory, 257-file documentation checks, help ownership/fragment checks and whitespace checks passed.
- The required integration of upstream `609bf1b1a` was conflict-free. The integrated dashboard passed typecheck, lint, 99 import/help unit tests and 14 telemetry unit tests. The four affected browser workflow files passed in both profiles: dev-admin 19 passed/one profile skip; local-auth 19 passed. Dashboard assets were generated before Go API builds. The workflow script closed its controllers and browsers. Raw frontend logs are gzip-compressed without changing their bytes. All 1,145 generated static import/entry-point references resolve; the 308 built files have retained hashes and are committed separately from source changes.

Review added half-hour gaps, repeated dates, source hashes and timezone-aware inputs under a local mapping. The final source/test review found no further actionable issues. The initial integrated full Go suite exposed a separate [coordinated-start clock-domain defect](TELEMETRY_AND_PROTOCOL_SLICES_2026-10.md#roadmap-coordinated-published-clock-2026-10-19) in two real-generator adoption tests. Its failure trace is retained with that correction. The corrected full-suite run (`../roadmap-coordinated-published-clock-2026-10-19/evidence/corrected-full-go-results.json`) passed all 168 tested packages with 10 skipped/no-test packages and no failures. The final scoped review found no further actionable issues. Local tests are not qualification of Linux/Windows native release lanes, reference workloads or multi-host recovery; those environments are unavailable. DECIMAL disclosure, partition precedence and other NA-A requirements remain open.

<!-- Original record: docs/qa/reviews/roadmap-analytics-timestamp-offsets-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-analytics-timestamp-offsets-2026-10-19"></a>

## Roadmap implementation: native timestamp source offsets


Date: 2026-10-19. Scope: a bounded [NA-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics) timestamp-correctness slice. Base: `dd1e8308f`.

<a id="roadmap-analytics-timestamp-offsets-2026-10-19--contract"></a>
### Contract

`timestamp_offset` normalization requires a timezone-aware Parquet timestamp or a parsable clock-bearing text value with an explicit Z/numeric offset. Offset-free text, DATE and naive TIMESTAMP values are rejected instead of inheriting the worker's UTC session setting. Local-wall `timestamp` mappings refuse offset text, including short numeric offsets, instead of dropping the supplied offset. Existing mapping versions and canonical readers remain intact; historical artifacts are not rewritten.

The native fixture contains both occurrences of a repeated local clock time with different explicit offsets in one file. Independent UTC expectations preserve both instants. Other fixtures cross spring/fall DST changes with unambiguous local timestamps and verify conversion per row. This does not qualify ambiguous or nonexistent local-wall inputs: that refusal contract remains a subsequent NA-A item, along with partition precedence, DECIMAL disclosure and schema-drift work.

Rejections use the existing bounded file/measure/source-row summaries and durable rejected-row artifacts on explicit acceptance. Default refusal publishes no output. Source bytes are SHA-256 checked before and after normalization. Worker isolation, memory/spill bounds, cancellation and output ownership are unchanged.

<a id="roadmap-analytics-timestamp-offsets-2026-10-19--validation-and-review"></a>
### Validation and review

Validation passed:

- Full default Go suite: 168 packages passed, 10 packages skipped/no test files, no failures. The compressed raw JSON trace and package summary are retained; this command permits normal Go test caching.
- Fresh native analytics suite (`analytics_duckdb`), timestamp race checks, and four native API import/rejection/summary/histogram workflows passed.
- Generated analytics acceptance inventory passed. Documentation checks passed for 255 active Markdown files, help guide ownership/fragment checks passed, and `git diff --check` passed. This slice changes no dashboard or embedded-help source.

Review added typed DATE/TIMESTAMP refusals and whitespace-before-offset coverage. The pinned parser refuses the spaced-offset fixture; it remains a rejected row rather than a guessed instant. The initial failure and failed parser-fixture expectation are retained alongside final runs. Review also registered the new tests in the generated acceptance inventory. After these fixes, the final source, test, documentation and scoped-change review found no further actionable findings.

Evidence commands and environment (`evidence/commands.txt`), full Go package results (`evidence/full-go-package-results.json`), and SHA-256 manifest (`evidence/SHA256SUMS`) identify the validation records.

The unavailable reference hosts and native Linux/Windows runners leave release-scale and platform gates pending. These tests exercise the installed macOS arm64 native library only. The existing decimal and other legacy goldens are compatibility tests, not qualification of the remaining revised requirements.

<!-- Original record: docs/qa/reviews/roadmap-browser-native-failure-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-native-failure-2026-10-20"></a>

## Native browser document failure evidence — 2026-10-20


This BE-A qualification slice exercises external native page closure in Chromium and WebKit (default viewports plus Pixel 8 and iPhone 15 Pro emulation respectively) and a genuine default-viewport Chromium renderer crash through its CDP crash command. It uses managed browser sessions and controlled local pages; no synthetic lifecycle event substitutes for the native close/crash callback.

<a id="roadmap-browser-native-failure-2026-10-20--scope-and-contracts"></a>
### Scope and contracts

The fixture verifies configured mobile/touch flags and retained profile identity, independently reads the native page viewport and pixel ratio, then observes five document metrics, including a completed FCP and unfinished CLS/INP. Native failure must preserve completed records exactly, censor unresolved observations without values or eligibility, and retain source/document/lifecycle/window identity. An external page close may legitimately finalize LCP at its native hidden boundary. No other unfinished metric is promoted to final by that exception. Checks reject duplicate or additional measurement identities and require later managed teardown to leave the population unchanged. The crash-command worker is joined during cleanup even when an assertion fails.

The native page probe records `navigator.maxTouchPoints` independently. WebKit reports zero for the iPhone emulation cohort on this host despite its configured touch flag. This slice does not qualify native touch-device capability or touch dispatch.

No production capture, archive, import or analysis contract changes in this slice. The generated [browser acceptance inventory](../../qa/BROWSER_ACCEPTANCE.md) lists each test's precise engine scope. Chromium's CDP renderer-crash result is not a WebKit renderer-crash claim.

<a id="roadmap-browser-native-failure-2026-10-20--integration-and-validation"></a>
### Integration and validation

PR #259 (`0eadba7de`) was integrated with the previously committed map-cohort work. Source merges had no conflicts; generated dashboard assets conflicted and were rebuilt from the combined sources. The first asset restore refused unmerged rename entries; rebuilding and staging the generated output resolved them. Other worktrees had no pending or branch-only changes in the relevant browser lifecycle files.

The final full Go suite passed 168 packages with no failures (10 package skips/no-test packages); conditional test skips are listed in the validation summary (`evidence/validation-summary.json`). The log contains the final native-page oracle and both mobile-profile cases. The 25-case native engine/profile matrix also passed five repetitions independently. Native page reads matched the configured viewport and pixel ratio; WebKit's observed zero touch points remains disclosed above.

Dashboard build/typecheck, full lint, 225 focused tests and bundle budgets passed. Browser workflows passed 15 dev-admin tests with one skip and 16 local-auth tests, including in-app Help navigation, reloads and the map cohort contract. Four embedded Help articles exactly match their source. The private HAR import passed with all stable fields equal to the prior slice: 803 requests, 476 actions and zero DSL issues. Only the historically randomized generated-script hash is excluded from comparison. Capture contents and target hosts are not included or replayed.

The initial native close test incorrectly required LCP censoring even when the native hidden boundary legitimately finalized it. The first inventory attempt redeclared an existing Chromium scope. The first full suite found a stale generated QA dashboard (91 versus 92 tracked Playwright specs). An added mobile oracle initially assumed positive native touch points in WebKit. Each failed attempt is retained with its correction in the review record (`evidence/review-rounds.json`); the final gates above supersede those attempts. Unavailable touch capability is now distinct from zero. The final source review found no remaining issues.

Source and asset hashes (`evidence/tested-hashes.json`), commands and integration inputs (`evidence/commands.json`), native observations (`evidence/native-summary.json`) and evidence checksums (`SHA256SUMS`) identify the tested work. Only private build caches were cleaned; shared caches were left for parallel work. Unrelated primary-checkout files retained their original hashes.

<a id="roadmap-browser-native-failure-2026-10-20--remaining-qualification"></a>
### Remaining qualification

This is local macOS evidence for the logged installed browser versions. Genuine BFCache restoration, browser-process loss, WebKit renderer crash, Linux/Windows native lanes, real device sessions, stable authored step identity, actual dispatch timing, HAR replay durability and the remaining BE/BI gates stay open. It does not certify an actual iPhone/iPad or Android session, another host or a published density target. The existing synthetic BFCache fixture remains explicitly synthetic.

<!-- Original record: docs/qa/reviews/roadmap-joint-bounds-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-joint-bounds-2026-10-20"></a>

## Distribution and pending-joint evidence bounds — 2026-10-20


This [NA-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-b--bounded-joint-evidence-across-execution-entry-points) closes a pending-queue admission hole: a single publication exceeding the entire byte allowance is refused before useful earlier windows are evicted. Its omission increments the existing dropped-publication count used to mark joint archive coverage truncated. The 1,024-publication and 64 MiB limits are unchanged.

Duration-sketch cloning and protobuf distribution copies apply their existing 4,096-bin and 1,024-centroid validity limits before retaining oversized arrays. Oversized evidence becomes unavailable, not a shortened valid distribution or measured zero. Source inputs remain unmodified; valid boundary copies remain independent. The [owning analytics contract](../../NATIVE_ANALYTICS.md) retains the distinction between bounded copying/retention and the broader category, execution-entry-point and recovery work still pending.

Received joint protobuf publications now pass cardinality, canonical-key, projection, scalar and distribution-shape checks before retention. The 64 MiB joint-field encoded budget includes protobuf tags and length prefixes. Malformed joint evidence remains explicitly unavailable; independent action counters survive. Omitted tuples remain incomplete even when a peer omits that flag. This is a bound on retained joint fields, separate from transport limits and the pending queue.

<a id="roadmap-joint-bounds-2026-10-20--validation-and-review"></a>
### Validation and review

- Applied engine/gRPC race checks: 22 top-level tests and 26 subtests passed, including wire-envelope admission, copy limits and compatible fallback distributions.
- Applied joint API checks: 25 top-level tests and 0 subtests passed, including queue bounds, retained earlier windows and visible omission accounting.
- Full Go suite: 168 packages, 17,684 top-level tests and 17,385 subtests passed. All production code came from the installed checkout; the only external overlay supplied the private HAR regression.
- The attached ZIP retained its original hash and matching aggregate import result: HTTP 200, 803 requests, 476 actions and zero DSL issues. No replay or converted-content persistence.
- Generated analytics inventory, documentation links, both Help profiles and evidence checksums passed. No dashboard source or embedded Help content changes in this slice; the committed assets were verified by hash and exercised through rebuilt controllers.
- Exact source/asset/controller hashes, failed original-copy regressions, passing draft checks and the distinct combined RSS/joint draft are retained in the evidence bundle (`SHA256SUMS`).

Identical focused regressions against the preceding source reproduce the unbounded distribution copies. Failed baseline evidence is retained separately from passing patched checks. Final applied-source and staged review found no remaining actionable findings after checking exact encoded-envelope accounting, canonical tuple/shape admission, explicit source refusal, copy independence, fallback compatibility and queue-loss accounting.

These checks do not establish reference-host analytics performance or qualify unavailable Linux/Windows runners. No broader NA requirement is promoted to complete.

<!-- Original record: docs/qa/reviews/roadmap-joint-entry-points-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-joint-entry-points-2026-10-21"></a>

## Joint evidence through every entry point — 2026-10-21


This [NA-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-b--bounded-joint-evidence-across-execution-entry-points) carries the joint evidence v4 projection beyond direct test creation, which until now was the only way to set one. The contract is documented in the [native analytics guide](../../NATIVE_ANALYTICS.md).

<a id="roadmap-joint-entry-points-2026-10-21--what-changed"></a>
### What changed

- **Scenarios.** A scenario stores a `joint_evidence` projection (schema v73, `scenarios.joint_evidence_json`), returned by the scenario APIs and the authoring projection, carried by clones and library bundles, and part of the scenario's authored content: changing it alone mints a new content revision.
- **Every scenario entry point.** A run created from a scenario captures the scenario's projection unless its request names another. Direct runs, schedule occurrences and execution-plan entries all build their runs this way, so they share it.
- **CI runs.** A CI run has no scenario, so `POST /api/v1/ci/runs` accepts `joint_evidence`. It is validated on the request and recorded with the run's idempotency metadata: a retry under the same key that names another projection is refused (409).
- **Promoted categories only.** A `property:<category>` must be one a run's scripts promote (`results.property_analytics`, after the scenario's and the group's runtime overrides), or the engine's own `browser_iteration` (cold/warm). Scenario saves, runtime-settings changes applied to a scenario's groups and run creates refuse any other, naming it. A revised copy is checked like any run, except that it may keep a projection its source run was admitted with before this rule (one the source's own groups do not promote either), so such a queued run can still be revised. A script edited to drop a promotion leaves the scenario's later runs refused with the category named until the scenario or the script is updated; there is no per-run opt-out of an inherited projection.
- **Dashboard.** The scenario builder's run settings (Advanced settings) gain **Joint evidence**: a population (off, actions or business transactions), up to three dimensions and the property categories the scenario's groups promote, resolved as the controller resolves them (plus `browser_iteration` for browser groups). An empty projection or an unpromoted category blocks the save with a stated reason and opens the panel; a chosen category that lost its promotion is marked. Save and run payloads carry the projection, and checkpoints keep it. The revise-copy modal shows the source run's projection, which the copy inherits, and warns when its group edits stop promoting the kept category.

<a id="roadmap-joint-entry-points-2026-10-21--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| `TestJointEvidenceCarriesThroughEveryScenarioEntryPoint`: one scenario fixture; a direct run, a run naming its own projection, an execution-plan entry and a materialized schedule occurrence, each checked for the run's projection and the projection frozen into its provenance | Passed |
| `TestJointEvidenceCarriesThroughCIRuns`: projection on a CI run, a conflicting retry under the same key, an invalid projection | Passed |
| `TestJointEvidenceAdmitsOnlyPromotedPropertyCategories`: scenario and run refusals, promotion by a runtime override | Passed |
| `TestScenarioJointEvidenceIsAuthoredContent` on SQLite and PostgreSQL (`-tags postgres_integration`) | Passed |
| Dashboard: `jointEvidenceModel.test.ts`, the scenario-assembly round trip and the TestHub save test | Passed |

<a id="roadmap-joint-entry-points-2026-10-21--review"></a>
### Review

Two reviewers (backend and dashboard) read scratch snapshots only:

- Round 4: dashboard confirmed its fix; two nits, fixed: the warning claimed a refusal the controller may not make (an unchanged group's snapshot can still promote the category), and this README did not mention the warning.
- Round 3: backend found none. Dashboard found one low, fixed: the revise modal judged promotions against the library's current scripts, while the controller compiles a copy's unchanged groups from the source run's script snapshots, so its notice could misreport after a library edit. It now warns only when the modal's own group edits stop a promotion.
- Round 2: both confirmed their fixes. Backend found one low, fixed: the revised-copy exemption ignored the copy's own groups, so a copy that dropped a promotion its source had could keep the projection. Dashboard found one low and one nit, fixed: the revise modal gave no warning when its groups stopped promoting the kept category, and `browser_iteration` was labelled as a raw property and could be marked "Not promoted".
- Round 1: backend three medium and one low, dashboard one medium and four lower, all fixed. Backend: the engine's own `browser_iteration` category was refused; copying runtime settings between a scenario's groups could drop a promotion its projection used; the guide's older "Limits" paragraph contradicted the new contract; a revised copy of a run admitted before the rule could no longer be created. Dashboard: the revise modal hid the inherited projection; a blocked save did not open the panel; the checkpoint parser accepted a malformed projection; the fieldset's label hid its legend and the error was announced twice; tests covered only the happy path.

<a id="roadmap-joint-entry-points-2026-10-21--remaining"></a>
### Remaining

Validating category, cardinality, encoded-byte and distribution budgets at ingestion beyond the existing preflight; persisting expected source identities and publication/window ownership so recovery reconstructs the exact population or exposes lost batches; and the remaining exit fixtures (delayed, missing and duplicate sources, changed generations, overflow, pause boundaries, restart and cold HA adoption, and archive-versus-native agreement on independently counted tuples).

<!-- Original record: docs/qa/reviews/roadmap-joint-ingestion-budgets-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-joint-ingestion-budgets-2026-10-22"></a>

## Joint evidence ingestion budgets — 2026-10-22


This [NA-B slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-b--bounded-joint-evidence-across-execution-entry-points) bounds joint evidence v4 where samples are ingested, before the per-window state and the published envelope are built.

<a id="roadmap-joint-ingestion-budgets-2026-10-22--contract"></a>
### Contract

- **Category and cardinality.** A projected `property:<category>` must be promoted (see the [entry-point slice](#roadmap-joint-entry-points-2026-10-21)).
  - Each script that promotes a category approves 1 to 16 values (`dsl.ValidatePropertyAnalytics`).
  - Before a sample reaches the joint accumulator (`promotedPropertyDimensions`), any other observed value counts as `other` and an absent one as `missing`.
  - Samples from groups whose scripts do not promote the category carry the empty value.
  - A run's values for a category are therefore bounded by the union of its promoting scripts' lists plus those three. The existing limits of 512 tuples per publication, 256-byte labels and 1 KiB keys bound the window's tuples.
- **Distribution state.** A joint tuple and the window's other bucket now count only what joint evidence publishes: count and errors (`mergeJointSample`). The duration distribution and the average's total are recorded beside them.
  - Previously each tuple ran the full action merge (`mergeActionResult`). That built per-tuple server and Java telemetry summaries, transport timings, byte totals, duration bounds and correlation samples, none of which joint evidence published. That state stays with the marginal action metrics.
  - Each distribution keeps the existing limits: a t-digest of at most `engine.TDigestMaxCentroids` (1,024) centroids and a sketch of at most `engine.DurationSketchMaxBuckets` (4,096) bins.
- **Encoded bytes.** A generator publishes each tuple with only the joint fields (`jointAggregateFromEngine`): name, count, errors, average, P50/P90/P95/P99, digest and sketch.
  - Previously tuples went through the generic action conversion, which also copied exemplars, messaging and Java HTTP summaries, transport timings and bytes.
  - The worst case is 512 tuples with 1 KiB keys plus the other bucket, each with a full digest and sketch, under the longest projected dimensions and with the widest scalars. That joint part encodes to 51,052,433 bytes (about 48.7 MiB, 76.1% of the controller's 64 MiB joint envelope).
  - The controller's joint check therefore never refuses a valid publication whole for its size. The existing wire preflight still refuses malformed or oversized peers, and the transport's per-message limit applies to the whole batch separately.
- The digest limit is now one exported constant (`engine.TDigestMaxCentroids`), used everywhere it had been a literal (engine copy and validation, both protobuf conversions and the joint wire admission). The key and label limits are exported (`engine.JointMaxKeyBytes`, `engine.JointMaxLabelBytes`).

<a id="roadmap-joint-ingestion-budgets-2026-10-22--validation"></a>
### Validation

- `TestJointLargestPublicationFitsTheEnvelope`:
  - builds the worst case from the exported limits through the real conversion (`jointAggregatesFromEngine`), the generator's group merge (`mergeMetricsAggregateSets`) and protobuf;
  - asserts the encoded size is within `maxJointEncodedBytes` and that `admitJointPublicationProto` admits it;
  - shares one full-size distribution across the wire tuples, so it runs without allocating 513 copies.

  If any of those limits rises past the envelope, the test fails.
- `TestJointAggregateCarriesOnlyJointFields` asserts that a tuple publishes only joint fields, before and after the group merge, even when its engine metrics carry diagnostics.
- `TestJointAccumulatorKeepsOnlyPublishedState` asserts that a sample's diagnostics, timings, bytes, duration bounds and correlation identifiers do not become per-tuple state. Count, errors, average and both distributions stay exact.

Gates on the final tree:
- `go vet` and golangci-lint (0 issues) on the changed packages;
- the `pkg/engine` and `pkg/distributed/...` suites, and the joint tests under `-race`;
- `go test ./docs/qa`;
- the full `pkg/api` suite. Its only failure was the swagger freshness check of a separate, uncommitted slice; this slice changes no API type.

One unrelated `pkg/distributed/generatorrpc` failure was not captured. It happened in a run that shared the host with other suites, and two later full runs passed. Repeating that package inside one process (`-count=3`) fails `TestMessagingQuiescenceAdvertisementRequiresAvailableSupportedRuntime`, which registers a protocol globally. That test predates this slice.

<a id="roadmap-joint-ingestion-budgets-2026-10-22--review"></a>
### Review

Separate reviewers read scratch snapshots.

- Round 2 found no high or medium issues; fixed: one remaining centroid literal (`tdigestSnapshotFromProto`), a stale test comment, and a key construction in the worst-case test that would panic rather than fail if the limits changed.
- Round 1 found no high or medium issues. Fixed:
  - The worst-case test now goes through the generator's group merge, is built from exported limits rather than literals, and shares one distribution to bound its memory under `-race`.
  - The cardinality claim was per script, not per run.
  - The envelope claim now names the joint check and separates the transport limit.
  - Duration bounds were kept per tuple but never published, so they are now dropped.
  - A promoted property dimension now has the longest admitted name in the worst case.
  - Units are consistent.

<a id="roadmap-joint-ingestion-budgets-2026-10-22--remaining"></a>
### Remaining

Durable source-window ownership and recovery remain open, as the [roadmap NA-B status](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-b--bounded-joint-evidence-across-execution-entry-points) describes. These checks bound memory and encoding by construction and by test. They do not measure reference-host throughput, which belongs to TEL-C and NA-D.

<!-- Original record: docs/qa/reviews/roadmap-mapping-association-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-mapping-association-2026-10-20"></a>

## Mapping-preserving reassociation — 2026-10-20


This [NA-A slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics) adds a negotiated association contract for retained identity, summary and histogram mappings. It preserves the existing legacy PUT response and revision/CAS owner. The new PATCH method is rejected by older route tables before they can rewrite metadata; the dashboard requires the exact capability and validates the returned mapping, revision and ownership before adoption. It never falls back to PUT after a rejected PATCH.

The [minimum-controller and rolling-upgrade barrier](../../NATIVE_ANALYTICS.md) requires every serving controller and writer to support this contract before these datasets are reassociated. Capability reads alone do not certify the entire pool. Older clients using PUT against older controllers remain unsafe; previously discarded metadata requires re-import. Precision-consent workflows and advanced mapping authoring gates are separate open work.

<a id="roadmap-mapping-association-2026-10-20--validation-and-review"></a>
### Validation and review

- Full Go regression passed 168 packages, 17,686 top-level tests and 17,386 subtests, including the private HAR import through a test-only overlay.
- The attached ZIP retains its original hash and aggregate result: HTTP 200, 803 requests, 476 actions and zero DSL issues. No target replay or converted-content persistence.
- Native mapping-preservation tests and race checks, route authorization/CAS refusals, generated API/TypeScript/access contracts, analytics inventory and documentation QA passed.
- Dashboard typecheck, lint, all 718 unit-test files (8,022 tests), build and both complete workflow profiles were exercised. The first dev-admin run had 321 passes, six declared skips and five new selector failures; all five passed after the accessible-role locator fix in both profiles. The complete local-auth run passed with the fix. In-app Help is covered in both profiles. Original failures, repeat results and exact source/asset/controller hashes remain in the evidence bundle (`SHA256SUMS`).
- Original-component regressions, unavailable-resource fixture results and subsequent passing drafts remain separate from the final installed-source evidence.

The native fixture uses real upload, normalization, immutable output metadata and SQL publication under a declared 256 MiB memory / 128 MiB spill profile. The first default-spill reservation was correctly refused on this host and remains retained; this is not default-resource or reference-host qualification. Browser fixtures exercise real workspace navigation/authentication with controlled peer responses; the native tests verify actual persisted mappings and outputs.

Final applied-source and staged review found no remaining actionable findings after correcting missing-metadata fallback, strengthening ownership/authorization refusals and verifying negotiated responses without a legacy-route fallback.

<!-- Original record: docs/qa/reviews/roadmap-native-accepted-rejections-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-native-accepted-rejections-2026-10-21"></a>

## Native accepted rejections and histogram preview — 2026-10-21


This [NA-A slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics) closes two API-level reconciliation gaps that the [population golden](#roadmap-native-population-golden-2026-10-21) left open.

<a id="roadmap-native-accepted-rejections-2026-10-21--what-changed"></a>
### What changed

- **Accepted rejections.** `TestAnalysisAcceptanceAcceptedRejectionsReconcile` uploads a source's exact bytes, in which some latency values are not numbers, and checks its preview (row count and first timestamp). It commits with rejected measurements accepted, then checks these against DuckDB's own SQL over the source, under the engine's contract (a finite number is a measurement, a null a missing sample, anything else rejected):
  - the accepted and rejected counts;
  - the upload digest;
  - the rejection download: exactly the rejected source rows, each once, attributed to the one file and measure, with the engine's fixed reason;
  - each minute's queried `count` and `sum` over the readable rows, with every series response reporting the rejected count.
- **Histogram preview.** The histogram golden, renamed `TestAnalysisHistogramImportPreviewCommitQueryReconcile`, now checks the file preview's row count and that the first scrape's sample rows hold its source `requests` values (the text `le` column reads as redacted) before it maps, commits and queries.
- **Inventory.** The [analytics acceptance inventory](../../qa/ANALYTICS_ACCEPTANCE.md) lists the new goldens under nulls, resets, rejection provenance, preview/commit/query reconciliation, source non-mutation and raw statistics, and follows the rename.
- **Spec.** The NA-A status now names version 1 and version 2 mappings committed and queried through the API (`TestNativePartitionQueriesPreserveFullScanQualityLineageAndIndependentComparisonPlans` for version 2), and saved mappings that stay readable without being silently rewritten (`TestHistoricalSavedMappingReadsRemainCompatibleButCannotWriteUnsupportedOptions`), and leaves only external qualification open.

<a id="roadmap-native-accepted-rejections-2026-10-21--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| `CGO_ENABLED=1 go test -tags analytics_duckdb ./pkg/api -run 'TestAnalysisAcceptance\|TestAnalysisHistogramImportPreviewCommitQueryReconcile'` | Passed |
| `go test ./docs/qa` (inventory and dashboard freshness) | Passed |
| Documentation checks | Passed |

No product code changed. A separate commit regenerated the QA dashboard, whose API contract count had been stale since the validation-context route was added.

<a id="roadmap-native-accepted-rejections-2026-10-21--review"></a>
### Review

One reviewer read scratch snapshots only:

- Round 1: six findings, all fixed. The two medium ones: the histogram preview's first-bin check could never run, because the text `le` column is redacted in previews; and the spec cited a test whose version 2 commit fails as proof that a version 2 dataset answers a historical query. The low ones: the rejection download could list a row twice and still pass, the inventory listed a preview step the test lacked, the oracle counted nulls as rejected and accepted non-finite values, and the staged diff carried the worktree's unrelated bundle state.
- Round 2: all six confirmed fixed; one optional nit applied (the accepted count now counts nulls, which the engine accepts as missing samples).

<a id="roadmap-native-accepted-rejections-2026-10-21--remaining"></a>
### Remaining

External native and reference-host qualification remains open for NA-A.

<!-- Original record: docs/qa/reviews/roadmap-native-byte-evidence-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-native-byte-evidence-2026-10-22"></a>

## HTTP byte evidence — 2026-10-22


This [NA-C slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-c--complete-workflows-and-durable-lifecycle) gives measured `bytes_sent` and `bytes_received` a bounded query profile.

<a id="roadmap-native-byte-evidence-2026-10-22--starting-point"></a>
### Starting point

- **No executable query.** The catalog declared both metrics with an `interval_byte_totals` requirement, but no query profile ran them.
- **Ambiguous zero.** The archive kept each request's byte counts, but a zero there could mean either a measured zero or a protocol that counts no bytes.
- **Different meanings per protocol.** HTTP counted body bytes, gRPC its messages' JSON representation, WebSocket message payloads, database SQL text and Java VUser stdout.
- **HTTP itself was inconsistent.**
  - A response from the wire counted its compressed body, while a cache hit counted the decoded body it stored.
  - A request refused before sending counted its body as sent.
  - A step that dispatched no request (a check) looked like a zero-byte request.

<a id="roadmap-native-byte-evidence-2026-10-22--contract"></a>
### Contract

**HTTP adapter.** `ByteEvidence` on the result is set by the adapter itself:
- `none` on every HTTP result, until the action dispatches its request or serves it from the cache;
- `http_body.v1` (`protocol.ByteEvidenceHTTPBody`) from that point on.

The counts follow its definition: message body bytes on the wire, after transfer decoding and before content decoding.
- **Sent:** the dispatched request's body, once, plus the body of a digest-auth replay that completed (a failed replay is not counted).
  - Nothing for a fresh cache hit, or a request that failed before its headers were written.
  - A request that failed after its headers were written counts its whole body.
  - A redirect's re-sent body and an engine retry's earlier attempts are not counted.
- **Received:** the response body plus embedded resources and meta-refresh follow-ups. Nothing for a response served from the cache, whether fresh or after a 304.

These counts change for every consumer of HTTP results' byte counts: live metrics, reports and transaction SLA byte rules. Server telemetry's request and response payload sizes keep their earlier basis: compressed for a response read from the wire, the decoded stored body for one served from the cache, and the prepared request body even when it was not sent.

**Archive writers.** Both the controller's and the generator's writers keep the adapter's evidence (`protocol.ByteEvidence`), in JSONL and Parquet.
- They keep none on a business transaction, whose bytes are its actions' sums.
- Other protocols' adapters set none. SOAP and Java HTTP wrap the HTTP adapter and keep its label, but their actions are outside the population. gRPC counts its messages' JSON representation (the payload bytes are in its diagnostics), and a WebSocket close counts its payload even when the write failed.
- **Kept through the engine's evidence clear.** When the engine clears a result's public evidence (failed credential redaction, concurrent groups), a result carrying byte evidence keeps it and its constant protocol. Every other dependency type is still cleared, because an action parameter can shape it (JMS).
- **Kept through meta refresh.** A meta-refresh follow-up refused before sending keeps the page's label.
- A binary from before this slice sets nothing, so its requests stay unavailable.

**Catalog** (`byteMetricDefinitions`, population `byte`):
- `bytes_sent` and `bytes_received` take `sum` per bucket, or `avg`, `min`, `max`, `p50`, `p90`, `p95` and `p99` per request (nearest rank), in bytes.
- `bytes_sent_rate` and `bytes_received_rate` take `rate`: a bucket's sum over its duration, in bytes/s.
- They declare no derivation, baseline or correlation, and require `ByteEvidenceRequirement`.
- One profile executes them: `archive_selection//byte/exact`. It allows one exact `action` filter, no grouping or property, and a comparison run (`validateNativeByteQuery`).

**Execution** (`buildNativeByteSeries`). The population is HTTP requests in the window, optionally of one action, bucketed by request start.
- **Outside the population:** a step marked `none` (counted as `no_request`), a same-named action of another protocol, and a business transaction.
- **Unscored bucket:** a request without byte evidence, or with a negative count, makes its bucket unscored for every aggregation (`byte_evidence_unavailable`, `invalid_byte_count`), and the exclusion is counted.
- **Sums and rates** need a complete bucket:
  - every request measured;
  - every source collected (a local run has no source coverage and counts as collected; a distributed run needs `required` coverage);
  - the bucket ended by the run's execution completion.

  Otherwise the bucket is unscored (`incomplete_bucket`). A sum past 2^53 − 1 is unscored (`exact_sum_limit`).
- **Distributions** read every request of an evidenced bucket. When the collection is incomplete they still read them, with a warning.
- **Empty and zero:** a bucket without requests has no value; a request that sent no body is a measured zero.
- **Limits:** the existing limits apply: 100,000 raw observations and 1,000 identities.

**Dashboard.**
- Panel authoring offers **Archived HTTP body bytes** for archive panels only, with the four metrics and their aggregations, an action filter only, and help text about these bytes.
- The archive panel states their meaning. It shows each bucket's reason, for the current and the baseline run, in the table and the export, and offers no transaction drill-down for this population.

**CLI.** `apexctl analysis run-query` posts the same query.

<a id="roadmap-native-byte-evidence-2026-10-22--validation"></a>
### Validation

- **`TestHTTPByteEvidenceMeasuresWireBodies`** pins the adapter's meaning against a live server:
  - a gzip body counts its compressed length, and a fresh cache hit of it counts nothing;
  - a response served after a 304 counts nothing, while its first response counts its body;
  - a POST body counts as sent;
  - a refused dial sends nothing;
  - an action refused before dispatch is `none`.

  `TestHTMLStepsResolveAgainstTheCurrentPage` checks that an `image_check` step is `none`.
- **`TestNativeByteSeriesSumsRatesAndDistributions`** checks sums, rates, the per-request minimum, maximum, average and percentiles, the action filter and empty buckets. Its expected values are worked by hand from the fixture. The fixture includes a measured zero, a same-named gRPC action, a business transaction, a `none` step and a legacy request.
- **`TestNativeByteSeriesInvalidCountsAndExactSums`** covers `invalid_byte_count` and `exact_sum_limit`.
- **`TestClearedEvidenceKeepsProtocolAndByteEvidence`** (`pkg/engine`) covers the evidence clear: byte evidence and HTTP survive, and a JMS dependency type is still cleared.
- **`TestMetaRefreshKeepsThePagesByteEvidence`** covers merging a refreshing page into its follow-up, including a follow-up refused before sending.
- **`TestNativeByteSeriesTotalsNeedCompleteBuckets`** covers an uncollected source, best-effort coverage, and a bucket past the run's completion.
- **`TestNativeByteSeriesRoute`** runs `POST /tests/{id}/archive/native-series` with a comparison run, and checks that an unsupported filter is refused.
- **`TestRunArchiveWriterStampsHTTPByteEvidence`** covers the controller writer: HTTP, `none`, a business transaction and another protocol. It also covers the merge of generator archives (`mergeRemoteArchiveTransactions`) and the Parquet row conversion.
- **`TestGeneratorArchiveStampsHTTPByteEvidence`** covers the generator writer in the same way.
- **`TestByteEvidenceIsWhatTheAdapterSet`**, **`TestByteMetricsAdvertiseTheArchiveProfile`** and **`TestNativeByteQueryValidation`** cover the label, the catalog and the query contract.
- **Dashboard tests:**
  - `saves an HTTP body byte panel with only an action filter (NA-C)`;
  - `states HTTP body bytes with their unit and unscored reasons, without a latency inspector (NA-C)`;
  - `says why a byte comparison bucket is unscored, in the table and the export (NA-C)`.

<a id="roadmap-native-byte-evidence-2026-10-22--review"></a>
### Review

Round 3 found that keeping the dependency type through the evidence clear was broader than needed, and that some wording was inexact. Both are fixed.

Round 2 found these, all fixed:
- the engine's evidence clear removed the labels;
- a meta-refresh follow-up could relabel the page `none`;
- payload telemetry and failed digest replays were documented wrongly;
- the export reported empty buckets as measured.

Round 1 used two reviewers, one on semantics and one on consumers. They found that:
- the claimed "after content decoding" meaning was wrong, and cache hits counted a different basis;
- steps that dispatched no request were stamped as measured zeros;
- sent bytes followed no stated rule;
- the dashboard hid byte reasons in comparisons and exports.

They also found these, all fixed in code or docs:
- start-time bucketing and hard-stop omissions were undocumented;
- the rolling-upgrade behaviour was undocumented;
- invalid counts had no reason of their own;
- comments were stale;
- some paths were untested.

<a id="roadmap-native-byte-evidence-2026-10-22--remaining"></a>
### Remaining

- **Live byte queries.** Interval byte totals cannot give a distribution.
- **gRPC and WebSocket byte semantics.**
- **Throughput by arrival time.** Requests are bucketed by start.
- **Unlabelled byte counts.** The archive's dataset CSV export, transaction SLA byte rules and the transaction drill-down still read byte counts without the evidence label.
- **The rest of NA-C:** saved-panel revisions and pins, streaming CLI workflows, and the physical-writer ownership left open by the [writer ownership slice](#roadmap-native-writer-ownership-2026-10-25).

<!-- Original record: docs/qa/reviews/roadmap-native-failure-provenance-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-native-failure-provenance-2026-10-21"></a>

## Native import failure provenance — 2026-10-21


This [NA-A slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics) makes the two import failures a person can act on name their file, instead of the generic "Normalization failed; inspect mapping, schema, rejection and resource evidence." The contract is documented in the [native analytics guide](../../NATIVE_ANALYTICS.md#import-and-compare-parquet).

<a id="roadmap-native-failure-provenance-2026-10-21--what-changed"></a>
### What changed

- **Schema drift.** Before any row is read, every source file must hold each column the mapping reads (`ParquetMapping.MappedColumns`: the timestamp, identity columns, then each measure's value, name, sample and histogram columns) as a scalar column. A file that lacks one, holds it as a list or repeated field, or holds a group of fields there fails the whole import. The stored error names the file and the column, for example `File "b.parquet" does not match the mapping: it has no column "latency".` Nothing is accepted.
- **Rejected rows.** When rows had a measurement rejected and the import did not accept rejected measurements, the stored error names the file, the measure, the count and the first five zero-based source row ordinals. Source values are never included.
- **Across the contained worker.** The worker still returns only fixed codes. Two new codes, `schema_mismatch` and `rejected_rows`, carry indices only: the file index into the request's inputs, the mapped-column or measure index into the request's mapping, a fixed reason, a count and ascending row ordinals. The parent refuses a payload that is out of range, has the wrong code, or does not belong to a normalization, and rejected rows unless the normalization declined rejections, with a count within the file's known rows and exactly min(count, 5) ascending row ordinals within them. Column and measure names are shown as the schema preview shows them. The controller turns the indices into the file's import path and the mapping's own column or measure name.
- **Storage.** `FailAnalysisImportAttempt` takes a reason; an empty one keeps the generic message, as every other failure (engine, resource, reconciliation) still does.

<a id="roadmap-native-failure-provenance-2026-10-21--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| Mapping unit tests (`TestMappedColumns…`, `TestSchemaMismatch…`, `TestRejectedRows…`), including a group holding a list and an escaped `.` in a component | Passed |
| Worker contract tests for both payloads (in range, code pairing, operation scope, acceptance, input row bounds, ordering, exact first rows) | Passed |
| Native golden `TestNativeAcceptanceSchemaDriftAcrossFiles`: absent column, DuckDB list and struct columns, rejected text rows, numeric text | Passed |
| API acceptance through the real worker `TestAnalysisAcceptanceDuplicateAndDriftedFiles`: the stored error for each case, including a long table's missing name column | Passed |
| Full untagged `go test ./pkg/api` | Passed (758 s) |
| Untagged `pkg/analysis/...` and `pkg/storage`; tagged analytics suite (`pkg/analysis/...` and `pkg/api -run 'Test(Analysis\|Native\|Parquet\|Container\|Lagged\|Bucket\|Worker)'`) | Passed |

<a id="roadmap-native-failure-provenance-2026-10-21--review"></a>
### Review

One reviewer read scratch snapshots only:

- Round 1: five findings, all fixed. The medium one: the worker accepted rejected rows the engine cannot report (for a normalization that accepts rejections, beyond the file's rows, or with too few ordinals). The low ones: a group with a list among its fields read as a list, column paths were not escaped as in the schema preview, the duplicate-file sentence overclaimed, and no test covered a group column or a mapped column other than the value.
- Rounds 2–5: one or two low findings each, all fixed: the escaped path was escaped twice by `%q`; the mapping's own quotes, line breaks, separators and bidi controls could split or reorder the message; this record lagged the code.
- Round 6: no findings.

<a id="roadmap-native-failure-provenance-2026-10-21--remaining"></a>
### Remaining

Engine, resource and reconciliation failures keep the generic message. The other NA-A gates (counter resets, raw/summary/histogram reconciliation goldens, external qualification) remain open.

<!-- Original record: docs/qa/reviews/roadmap-native-population-golden-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-native-population-golden-2026-10-21"></a>

## Native population reconciliation golden — 2026-10-21


This [NA-A slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics) adds a raw-observation and counter golden through the import API. Summaries already reconcile across preview, commit and query (`TestAnalysisSummaryImportPreviewCommitQueryReconcile`), and histograms across commit and query (`TestAnalysisHistogramImportCommitQueryReconcile` (since renamed `TestAnalysisHistogramImportPreviewCommitQueryReconcile`)).

<a id="roadmap-native-population-golden-2026-10-21--what-changed"></a>
### What changed

- **Golden.** `TestAnalysisAcceptanceRawAndCounterPopulationsReconcile` (`pkg/api/analysis_acceptance_populations_native_test.go`) imports two Parquet files through the real worker:
  - a raw latency observation with nulls in one minute only;
  - a cumulative counter rising by irregular steps, which restarts in the second file and again inside it.
- **What it checks.**
  - Each file's preview row count and first reading.
  - The accepted measurement count, with no rejections.
  - The recorded digest of each upload, and after commit, that each stored original holds the source's exact bytes.
  - Per-minute `count`, `sum`, `min`, `max`, `avg`, `p50` and `p95` of the observation. These are compared with DuckDB's own SQL over the source files, which is independent of the normalization and series code. Percentiles use DuckDB's linear interpolation (`quantile_cont`), the definition the engine uses. The minute holding nulls must read as missing for every aggregation.
  - The counter's per-minute `delta`. The values must equal the sources' differences of closing (last) readings, with `no_prior_sample`, `counter_reset` and `missing_counter_sample` where the contract puts them. Irregular steps make a regression to opening or lowest readings fail (checked against a temporary control).
- **Guide.** [Native analytics](../../NATIVE_ANALYTICS.md) now states that a null scalar measurement is a missing sample, so its whole bucket, count included, reads as missing. A histogram whose bin counts are all null is missing, and one with only some null is rejected.
- **Helper.** The acceptance import helper takes checks to run before the mapping is saved (here, the previews).

<a id="roadmap-native-population-golden-2026-10-21--validation"></a>
### Validation

| Gate | Result |
| --- | --- |
| `CGO_ENABLED=1 go test -tags analytics_duckdb ./pkg/api -run 'TestAnalysisAcceptance'` | Passed |
| Documentation checks | Passed |

No product code changed.

<a id="roadmap-native-population-golden-2026-10-21--review"></a>
### Review

One reviewer read scratch snapshots only:

- Round 1: five findings, all fixed. The two medium ones: a counter rising by a constant step could not tell closing readings from opening ones, and the spec claimed the whole exit list was covered locally when most goldens run at the engine and some items are untested. The low ones: the digest check proved the upload, not the stored original; the oracle shares DuckDB's percentile definition; the null-sample sentence was too general for histograms.
- Round 2: no findings beyond three wording nits, applied.

<a id="roadmap-native-population-golden-2026-10-21--remaining"></a>
### Remaining

Mapping layouts, decimals beyond double precision, conflicting files, partition precedence and daylight-saving times are qualified at the engine rather than through the API. Accepted rejections reconciled through query, older mappings read through the API, histogram preview, and external native and reference-host qualification remain open for NA-A.

<!-- Original record: docs/qa/reviews/roadmap-native-precision-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-native-precision-2026-10-20"></a>

## Native decimal precision consent review — 2026-10-20


This slice implements source-bound DECIMAL conversion consent within
[NA-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics).
The [operating contract](../../NATIVE_ANALYTICS.md#decimal-precision-review)
and [generated acceptance inventory](../../qa/ANALYTICS_ACCEPTANCE.md) describe its
supported scope. Other analytics and external qualification gates remain open.

<a id="roadmap-native-precision-2026-10-20--delivered-contract"></a>
### Delivered contract

Mapping version 3 adds an explicit reject/allow-rounding policy while retaining
identity schema 2. The bounded, read-only review inspects every immutable uploaded
file and identifies potentially lossy mapped DECIMAL columns from their precision
and scale. It does not invent a count of changed rows. The complete source set,
canonical mapping, project/import identity and current revision bind consent.
The worker re-derives the review from verified source handles before producing
outputs. Unconsented new lossy normalization fails; retained legacy artifacts
remain readable. Source files are unchanged.

SQLite and PostgreSQL persist the accepted review digests and consent revision.
Attempt inputs and publication fences preserve that receipt across recovery;
mapping edits invalidate it, while successful normalization and run reassociation
retain it. CLI and dashboard negotiate the precision capability, retain explicit
choices and refuse unsupported versions. The dashboard requires a current review
and explicit acknowledgement of a permitted risk. Saved mapping templates never
carry source-specific acknowledgement.

Bounds are 1,024 files, 64 measures, 1,024 file/measure pairs, 64 affected file/column
pairs and 64 KiB of review JSON. Source schemas are validated one at a time;
only bounded risks and source hashes remain in memory. Refusal returns no partial
review. API publication rechecks project authority, import revision, cancellation and state.
The minimum-controller upgrade barrier remains required; an old binary cannot be
made safe by a new client's capability check alone.

<a id="roadmap-native-precision-2026-10-20--validation-and-review"></a>
### Validation and review

- Final backend full Go: 168 packages, 17,767 top-level tests and 17,717 subtests passed; 33 top-level and 5 nested environment/feature skips remain skips. The API package completed in 963 seconds with `GOMAXPROCS=4`, `GOGC=100` and the unchanged 20-minute timeout.
- Native worker: 164 top-level tests and 293 subtests passed, including real DECIMAL conversion, exact-consent refusal, immutable input hashes and identity-v2 queries. Native API behavior passed; its generated response-schema finding was fixed and verified separately and by the final full Go gate.
- Race coverage passed for analysis, CLI and affected storage paths. An unnecessarily broad storage race run was deliberately interrupted; its log is retained and is not a passing package result. The replacement affected-storage run passed 49 top-level tests and 147 subtests.
- Actual PostgreSQL attempt/consent coverage passed (1 top-level test, 9 subtests). The earlier untagged invocation ran no PostgreSQL tests and is not counted as coverage.
- Dashboard typecheck, lint and 8,041 unit tests passed. Final analytics and Help browser workflows passed dev-admin 29 tests (1 skip) and local-auth 30 tests. Both profiles' narrow-layout screenshots were visually inspected, including a 207-character column name.
- The attached private HAR imported through the real endpoint with the same aggregate results as the preceding Java slice: 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, 4 warnings, zero DSL issues and 264 protected requirements. The original ZIP hash is unchanged. The randomized script hash is excluded from comparison. Neither target replay nor private payload retention occurred.

The broad dashboard cycle originally found a new select-locator mismatch and
three local-auth import-persistence failures. The locator was corrected. All
JMeter/LoadRunner workflow tests passed the diagnostic rerun (20 tests, 1 skip),
but that alone does not establish the original persistence failures' cause.
A diagnostic full local-auth rerun retained controller logs and reproduced the
import failures. The attestation errors explicitly report exhaustion of the
protected import store’s 128-session capacity across the shared test controller.
The JMeter publication failure has no recorded inner cause and remains unresolved.
The diagnostic run was interrupted when the shared disk filled; this record does
not claim a clean full dashboard cycle. Production import limits are unchanged.

Other review findings corrected before final validation: missing histogram-bound
precision disclosure, stale consent restoration, nested generated column schemas,
a stale generated authorization matrix, an omitted Help fragment and long-path
layout overflow, and simultaneous retention of every source schema. Streaming
review tests preserve the exact prior review JSON/digest, freeze the mapping,
discard schema aliases, and refuse incomplete, duplicate or failed cohorts. The
first native rerun hit the disk resource guard; that failure and the scoped cleanup
record remain retained. Original failure logs and screenshots remain separate from the
successful repetitions. A later full Go run under `GOMAXPROCS=2`/`GOGC=30` reached the aggregate API
package timeout without an earlier assertion failure; its failed result remains
separate from the passing final backend run. The staged review also found that
the embedded Help text predated the final streaming paragraph. Dashboard assets
were regenerated and checked for exact equality with the Markdown source. The
final assets passed static-serving/generated-contract tests (10 top-level, 8
subtests) and repeated analytics/Help browser checks: dev-admin 29 passed/1 skip,
local-auth 30 passed. Both profiles’ final screenshots were visually inspected.
The next precision-scope review found no actionable issues. The broader browser
gate and external qualifications above remain open.

<a id="roadmap-native-precision-2026-10-20--parallel-work-and-limits"></a>
### Parallel work and limits

A read-only audit of all 30 other registered worktrees found no implementation of
the precision-consent contracts. Older outstanding authoring/browser/telemetry
commits were patch-equivalent to changes already on main. The active `pr254`
worktree separately owns telemetry recovery/notification, Java diagnostics, HAR
limits, authoring and timestamp fixes. This slice does not copy or overwrite its
uncommitted work; those changes need integration when published. The unrelated
primary checkout remains untouched.

This is local darwin/arm64 qualification. The four native packaging lanes,
independent reference host, 1 GiB benchmark, remote writer/HA lifecycle, real
TruClient exports and multi-host recovery remain pending where unavailable.
No parent acceptance gate is promoted by these local results. Build caches remain
in use by follow-up work; shared caches are not pruned during parallel activity.

<a id="roadmap-native-precision-2026-10-20--evidence"></a>
### Evidence

The validation summary (`evidence/validation-summary.json`) distinguishes passing
checks, failed attempts and interrupted diagnostic runs. The
review rounds (`evidence/source-review-rounds.json`) record findings and repairs.
The worktree audit (`evidence/worktree-audit-precision-overlap-recheck.json`) records
the exact checked heads and contract search; the
patch-equivalence check (`evidence/worktree-audit-patch-equivalence.json`) identifies
older work already incorporated into main. The
HAR comparison (`evidence/private-har-final-profile-comparison.json`) preserves the
private-capture regression result without its payload.

The final backend command (`evidence/run-final-profile-validation.sh`) requires the
private capture and test-overlay paths from the operator. Original unsuccessful
logs remain compressed beside successful runs. They are not acceptance evidence
for the gates they failed. `SHA256SUMS` covers the retained record.

<!-- Original record: docs/qa/reviews/roadmap-native-writer-ownership-2026-10-25/README.md; local evidence kept outside git -->
<a id="roadmap-native-writer-ownership-2026-10-25"></a>

## Native physical-writer ownership across restart and HA — 2026-10-25


This [NA-C slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-c--complete-workflows-and-durable-lifecycle) implements the first part of "Finish durable physical-writer ownership across controller restart and remote backends." A controller now records which process writes each import's artifacts. When that process dies, a controller on the same host and in the same PID namespace proves the exit and resumes the import's lifecycle. HA takeover is covered only for a replica that shares that PID namespace; a replica on another host never judges.

**Deployments without restart recovery.** Recovery needs a successor in the dead controller's PID namespace: a process restarted in place under systemd, launchd, a service manager or the desktop app. The Helm chart (no `shareProcessNamespace`) and docker compose restart the controller in a new container, which is a new PID namespace, so those deployments get no restart recovery from this slice: their interrupted work stays owned and their datasets deletion-pending, reported as `unjudgeable_writers`.

Every case it cannot prove keeps its ownership, and its dataset stays explicitly deletion-pending. The operator view is in the [native analytics guide](../../NATIVE_ANALYTICS.md#import-and-compare-parquet).

<a id="roadmap-native-writer-ownership-2026-10-25--starting-point"></a>
### Starting point

**Writer generations and their fences.** Two kinds of physical writer generation existed, each with exact, generation-unique keys:
- **Normalization attempt.** Minted by `ClaimAnalysisImportAttempt` into `analysis_attempt_journal`. Its identity is the attempt ID and import revision, and its planned keys are `normalized-<attempt>-part|reject-….parquet`.
- **Upload chunk writer.** Minted by `BeginAnalysisUploadWriter` into `analysis_upload_writers`. Its keys are `chunk-<file>-<offset>-<sha>-<writer>` and `upload-<writer>.tmp`.

Three fences protected them:
- **Publication fence.** `PublishAnalysisImportAttempt` publishes only under the project lock. It requires the matching owner, revision, input digest, a live SQL lease, the absolute expiry and `WritersQuiesced`. An ambiguous commit is decided by the journal's publication digest.
- **Cleanup queue.** `analysis_cleanup_jobs` deletes a generation's keys only after that generation's own physical proof. It runs on the analytical backend pinned by `analysis_storage_identity_v1`.
- **Dataset namespace removal.** This waits for every generation of the dataset.

**Proof was process-local.** The only physical proof came from the writer's own process:
- `ConfirmAnalysisAttemptWritersQuiesced`, after the pipeline's exact joins;
- `CompleteAnalysisUploadWriter`, after the writer's `Close` or `Sync` returned nil.

Their retry records (`nativePendingWriterPipelines`, `nativePendingPublicationPipelines`, `analysisUploadOwners`) and the heavy-work gate (`nativePipelineGate`, one pipeline per process) lived in memory. The code says so: "they are not proof of cross-process or cross-host writer termination."

**What a controller crash, restart or HA takeover left behind, by boundary:**

| Boundary | State left behind | Before this slice |
| --- | --- | --- |
| Upload, before completion | A `pending` writer row, never physically closed | Its offset was blocked forever (`ErrAnalysisUploadInFlight`). The dataset could never be deleted. |
| Upload, after completion | An accepted writer and its cursor, in one transaction | Nothing pending. |
| Normalization, before writer quiescence | Import `normalizing` with a lease of at most 1 minute. The attempt was not quiesced. | After the lease expired, a commit could start a new generation with new keys. The old attempt's cleanup and the dataset's deletion stayed blocked forever, and its private staging under `_analysis-cache` leaked. |
| After quiescence, before publication | The attempt quiesced, but the import still `normalizing` | The import stayed `normalizing` until its lease expired. |
| Artifact commit and publication | One SQL transaction, with the journal as receipt | The journal decided it; nothing was lost. |
| Cleanup | A durable, idempotent job | It ran again. Its keys are generation-unique, so a late delete can only hit its own generation. |
| Cold HA replica | Same as a restart | The replica could not tell a dead controller from a live, partitioned one, so everything stayed retained. A lease expiry allowed a retry, never cleanup, as the spec requires. |

**What the backends guarantee about a write the controller can no longer observe:**
- **Local filesystem** (`os.Root`: write a temp file, `Sync`, `Rename`, sync the directory). Every write is a synchronous system call of the writing process, so once that process has exited nothing more can be written.
- **S3, Azure Blob and GCS** (gocloud `blob`). An object becomes visible only when its writer commits. Incomplete multipart uploads, uncommitted blocks and unfinalized resumable sessions never appear as objects. A commit request that the process had fully sent before dying can still be applied afterwards, and no provider bounds that delay. Process exit therefore proves that no new request will be issued, but not that no write can still land.
- **A `Close` error** is retained as unjoined in the process, which is correct.

<a id="roadmap-native-writer-ownership-2026-10-25--contract"></a>
### Contract

**Writer incarnation.** Claim and Begin record the incarnation of their writer in the same transaction that mints the generation, before any artifact I/O.
- The incarnation is a `procidentity.Identity` of the controller process: machine, boot, PID namespace, PID and kernel start time.
- It is stored as JSON fields: `incarnation` and `termination` on the attempt journal, `Incarnation` and `Termination` on the upload writer. There is no SQL schema change.
- The API binds the identity through `storage.WithAnalysisWriterIncarnation`, in `handleAnalysisCommit` and `runAnalysisUploadChunk`. A context without one mints a generation that no exit proof can retire.
- `pkg/internal/procidentity` is the reviewed HAR crash-reclamation proof, moved out of `pkg/protocol/browser`, which now delegates to it.

**Exit proof** (`procidentity.Gone`). A recorded process is gone only when:
- the same identified machine has booted again; or
- within the same boot, on the same machine identity and in the same PID namespace, no process has that PID with that start time.

A process on another machine, boot or namespace is never judged; the machine identity is compared in the same-boot case too, so a boot value two hosts share (the Windows boot counter, VMs cloned from a memory snapshot) cannot let one host's process table answer for another's. Platform rules:
- **Linux.** A missing `/proc` entry counts as gone only when `kill(pid, 0)` returns `ESRCH`, because `hidepid` and `ProtectProc` hide live processes. A `/proc` that does not describe this process's own PID namespace makes the identity unavailable. The time namespace, when the kernel has one, is part of the namespace, because start times are counted in it. Without a machine-id the random per-boot `boot_id` alone scopes the lookup.
- **Windows.** Without a `MachineGuid` and host name the identity is unavailable, because the boot sequence number is a small counter other hosts share.
- **macOS.** The per-boot session UUID scopes the lookup; with no machine identity an earlier boot proves nothing.
- A process restored by checkpoint/restore (CRIU) is unsupported.

These rules also apply to HAR crash reclamation, which uses the same package.

**Exit scope.** A controller judges with its own identity and the pinned backend. It never judges its own process. It grants no scope while the current backend differs from `analysis_storage_identity_v1`, and with no identity of its own. A filesystem backend counts as local only when `localfs.IsLocal` places its artifact directory, `<archive root>/_analysis` (the directory every write, read and cleanup opens with `os.OpenRoot`), on an allowlisted local filesystem. The directory is opened and judged through that handle (`fstatfs` on Linux and macOS; on Windows `GetFinalPathNameByHandle`, so a directory link or junction to a share is judged by its target, and `GetVolumeInformationByHandle` for the filesystem name). The allowlist is:
- **Linux:** ext2/3/4, XFS, Btrfs, F2FS, bcachefs, ZFS, tmpfs, ramfs or overlayfs. The 32-bit `f_type` is read unsigned.
- **macOS:** APFS or HFS with `MNT_LOCAL`.
- **Windows:** a fixed drive formatted NTFS or ReFS, not reached through UNC, by the same policy as `winfs.ValidateVolume(driveType, fs, "NTFS", "REFS")`. A WinFsp mount (rclone, sshfs-win) reports a fixed drive with its own filesystem name and is refused.

NFS, SMB/CIFS, EFS, Azure Files, Ceph, GlusterFS, 9p, virtiofs, every FUSE filesystem and every unlisted local type can complete a write after its process is reaped, or are not qualified, so a filesystem backend there is judged like an object store. Only the `_analysis` directory itself is judged; a filesystem mounted below it is not seen, and the operator guide forbids one. A probe that fails grants no scope for that sweep: it is retried, never recorded as remote.

**The judge never creates the artifact directory.** Writers create `_analysis` on their first artifact write (`analysisObjectWrite`, mode 0700), as before this slice. The exit scope (the sweep and an upload retry's exit proof) and DELETE never create it; DELETE's status judges through `analysisWriterHere` (`procidentity.Judgeable`), not the exit scope. A missing `_analysis` fails the locality probe, so that pass grants no scope and judges nothing, and a later pass retries; an upload retry that meets it logs a rate-limited warning naming the directory and the workaround. Cleanup keeps its existing rule that a missing `_analysis` holds nothing to delete, and never creates it either. Two consequences are listed in Remaining: a fresh backend's first writer that died before any write created `_analysis` stays owned until a later write or the operator creates it, and the existing behaviour on an unmounted share is unchanged. Round 4 let the sweep create the directory and round 5 replaced that with a marker-bound root; both were dropped (Review rounds 5 and 6).

**Effects of a proven exit:**

| Generation | Filesystem backend on a local filesystem | S3, Azure Blob, GCS, or a network/FUSE filesystem |
| --- | --- | --- |
| Attempt still owning a `normalizing`, unpublished import | `WritersQuiesced` and a termination record. In the same transaction the import becomes `failed` ("the controller running it exited; commit the import again to retry"), its revision advances, its owner is cleared and the attempt's cleanup is queued. | The same failure and termination with `remote_commit_unproven`. `WritersQuiesced` stays false. |
| Attempt its own process had already quiesced, still owning a `normalizing`, unpublished import | A termination record, and the same failure | A termination record without `remote_commit_unproven` (its writes were already proven stopped), and the same failure |
| Attempt otherwise unquiesced (failed, replaced, cancelled or deleted) | Becomes quiesced, so its exact cleanup plan runs | Records `remote_commit_unproven`; its cleanup stays blocked |
| Unclosed upload writer | Becomes `abandoned` and physically closed, with a termination record. Its exact temporary and object keys are queued for cleanup. | Becomes `abandoned` with a termination record and `remote_commit_unproven`, but not closed. Begin may reuse its offset with a new writer and new keys; its cleanup stays blocked. |
| Published generation, closed upload writer, or quiesced attempt that owns no normalizing import | Unchanged | Unchanged |

**Where proofs run:**
- **Sweep.** Every replica sweeps a bounded inventory at startup and about once a minute (`reconcileAnalysisWriterIncarnationsOnce`): at most 400 candidates per pass. Permanently blocked (remote-unproven, unjudgeable) and not-yet-due cleanup jobs stay in the inventory, so a backlog is worked through at about 400 outstanding generations a minute per replica. The inventory pages, by key, through the owners of normalizing imports, then through attempts and upload writers held by the cleanup queue.
- **Retried upload.** When an upload retry finds its offset blocked, the controller resolves that exact offset and retries Begin once.
- **Locking.** An unlocked snapshot first settles every generation this host cannot judge, and every settled generation that no longer owns its import. Only a possible change takes the project lock, where everything is decided again.

**Deletion-pending answer.** DELETE of a dataset that is still owned answers 503 with this body:
- `code: analysis_dataset_deletion_pending`;
- `pending` counts:
  - `unproven_writers`: recorded where this host can judge it (`procidentity.Judgeable`: the same machine, boot and PID namespace, or an earlier boot of the same identified machine), so it may settle: it closes, or this host proves its exit at a later sweep. Under `hidepid`, a PID reused by another user's process stays undecidable, so such a writer can stay here;
  - `unjudgeable_writers`: recorded where this host cannot judge it: another machine, or another boot or PID namespace of this one. It resolves only if it is still running and closes, or a controller that can judge it proves its exit. A restarted container never can; a replaced pod can only if its host name and machine ID match its predecessor's, which the machine identity requirement forbids for distinct hosts;
  - `unrecorded_writers`: journalled before incarnations, or rewritten by an older controller;
  - `remote_commits_unproven`;
  - `legacy_writers`: a legacy upload or owner without a journal, or an unplanned output;
- `retryable`: some writer may still resolve without an intervention (unproven, unjudgeable or unrecorded);
- `Retry-After: 60` only while this host itself may settle an unproven writer.

Earlier, a 503 here came in the generic redacted envelope. The import stays `cancelled` or `deleting`. A repeated DELETE of an already revoked import only re-reads this status and retries the physical deletion: it no longer saves the import again, which re-queued every cleanup job 20 minutes later each time.

**Rolling upgrade.**
- **Reading.** New controllers read rows without the new fields; those generations are unrecorded and stay retained.
- **Begin at an older controller.** Older controllers ignore the fields and keep blocking Begin on any unclosed writer, which fails closed.
- **Older rewrites.** An older controller that rewrites a row drops the fields:
  - claiming again, failing, cancelling or completing drops the incarnation, so that generation becomes unrecorded;
  - a remote termination it drops is reported as unrecorded.

  Each of these fails safe.

<a id="roadmap-native-writer-ownership-2026-10-25--invariants"></a>
### Invariants

1. No generation is quiesced or closed, and no import is failed, without a durable termination record proving that exactly its recorded process exited, judged by a different process that can judge it (`procidentity.Judgeable`: the same machine, boot and PID namespace, or the same identified machine after a reboot). The locked transaction decides again before any write.
2. A replica never judges its own process, an unrecorded generation, a legacy sentinel, or a process on another machine, boot or PID namespace. A cold replica on another host therefore keeps everything owned.
3. A remote exit retires the generation from the workflow, but its cleanup and its dataset's deletion stay blocked (`remote_commit_unproven`).
4. The proof that fails an owned import is written in the same transaction that advances its revision and clears its owner. A late `Publish`, `Fail` or quiescence seal from the exited generation cannot adopt the import.
5. A retired upload writer is `abandoned`: a late `Complete` cannot accept it or advance the cursor, which references only the replacement's key.
6. A cleanup plan holds only its generation's own keys, plus obsolete keys of generations already proven quiesced. A retry never plans its own live keys. Plans run only on the pinned backend.
7. A dataset namespace is removed only when every generation is quiesced or closed. Legacy, unrecorded and remote-unproven writers keep the dataset explicitly deletion-pending.
8. A process-local close callback, a cancellation and a lease expiry are never proof. A lease expiry still allows only a new generation.
9. Only a filesystem backend on an allowlisted local filesystem treats an exit as proof that no later write can land; every other backend keeps `remote_commit_unproven`.
10. A filesystem exit is judged only against an existing `_analysis`. The exit scope (sweep, upload retry), DELETE and cleanup never create it; only an artifact write does.

<a id="roadmap-native-writer-ownership-2026-10-25--validation"></a>
### Validation

**Storage** (`exerciseAnalysisWriterOwnership`, run on SQLite with two stores and on PostgreSQL 16 with two independently opened stores, `TestPostgresAnalysisWriterOwnershipRestartAndReplica`). Controller A mints generations and exits; B, a restart or a replica, judges A. The suite injects that exit:
- **Upload, before completion.** For the filesystem and for remote:
  - a live, self or other-host writer keeps its offset;
  - a stale cursor revision is refused;
  - no cleanup job exists while the writer is live, and one is queued for exactly that writer when its exit is proven;
  - after A's proven exit the writer is retired, and a replacement writer gets new keys;
  - a late `Complete(accepted)` cannot advance the cursor;
  - the cursor references only the replacement's key;
  - the cleanup plan holds exactly the exited writer's temporary and object keys;
  - without a late Complete, remote keeps cleanup blocked and the cancelled dataset deletion-pending.
- **Upload, after completion.** Not judged again; the cursor is unchanged.
- **Unrecorded (legacy) upload writer.** Never retired; it keeps blocking.
- **Normalization, before quiescence.** For the filesystem and for remote:
  - no cleanup job exists while the attempt is live, and the attempt's job is queued with the failure;
  - the import fails with the reason, its revision advanced and its owner cleared;
  - a repeated resolution changes nothing;
  - a late `Publish` and a late `Fail` are refused;
  - the cleanup plan holds exactly the attempt's own keys (filesystem) or is blocked (remote);
  - the retry is a new generation whose keys are disjoint, whose cleanup never touches its own keys, and which its predecessor's exit cannot judge.
- **After quiescence, before publication.** The import fails, the termination is recorded without `remote_commit_unproven` even under a remote scope, and a late publication is refused.
- **After publication**, including an ambiguous commit reply. Not judged again; the journal receipt decides, and the ready revision is unchanged.
- **Lease expired, live re-claim, then exit proven** (the common restart path), for the filesystem and for remote: A's lease expires, live C claims again, and A's exit is then proven. A is proven (and, on the filesystem, quiesced) while C's owner and revision are unchanged.
- **Cancellation while in flight.** Before proof the dataset is not ready. After proof, the filesystem becomes ready and remote does not, and a late publication is refused.
- **Live, self and unrecorded attempts** stay owned, and a scope without a judge is refused.
- **Deletion-pending reasons**, each with its exact count, judged from host A:
  - running, unrecorded, remote and pre-journal legacy writers;
  - running, unrecorded and remote upload writers;
  - a writer recorded on another host's boot (unjudgeable);
  - a published output whose journal is gone (unplanned, so legacy).
- **Pending deletion classification**: `Resolvable` and `ResolvesHere` for each reason alone and in combination.
- **Candidate inventory** pages through both phases, finds owners, cleanup-held attempts and upload writers, and refuses malformed cursors and page sizes.
- **`TestAnalysisUploadWriterTerminationValidation`** covers the row validation and the incarnation bounds.

**API** (default build, `pkg/api`). Each test that proves a fake exit installs B with no exits before minting A's generations, and proves A's exit only just before its explicit sweep. The server's own background sweep, which `NewServer` starts, therefore cannot act first. The upload-retry and real-process tests need no such ordering: until they install B, the background sweep judges with the real process identity, which cannot decide their recorded identities. B's filesystem probe answers "local" without looking (`assumeLocalFilesystem`), and the real-process test installs the real identity and exit judge with that same fixed probe, so only the locality tests (`NetworkFilesystemIsNotLocal`, `ArtifactDirectoryIsNeverCreatedByTheJudge`) depend on the probe's answer.
- **`TestAnalysisWriterOwnershipRestartRetiresExitedGenerationsOnFilesystem`.** A died mid-normalization, mid-deletion and mid-upload; C is still alive.
  - Before the sweep, DELETE is explicitly pending and no output is deleted.
  - A repeated DELETE leaves every cleanup job's schedule unchanged.
  - After the sweep, the interrupted import is `failed` with its reason over GET.
  - The exited attempt's cleanup removes only its own output.
  - Both deletions return 204 and remove the exited objects.
  - C's import and output are untouched, and C's DELETE after the sweep is pending (`unproven_writers: 1`).
- **`TestAnalysisWriterOwnershipRemoteBackendsRetainUnprovenCommits`**, for each of `s3`, `azure_blob` and `gcs`:
  - B's filesystem probe calls every root local, so only the backend kind keeps the scope non-local;
  - the interrupted import becomes `failed` for a retry (the default build cannot commit again; re-commit is covered on the filesystem by the storage retry test);
  - the attempt and dataset cleanup jobs run against a memblob bucket, kept open across cleanup owners, that holds objects which "landed late" at the exited generations' keys;
  - nothing is deleted;
  - DELETE stays pending with `remote_commits_unproven: 1`, `retryable: false` and no `Retry-After`.
- **`TestAnalysisWriterOwnershipNetworkFilesystemIsNotLocal`**:
  - a filesystem backend whose probe reports a network mount retires an exited attempt like an object store: the import fails, DELETE stays pending with `remote_commits_unproven: 1`, and the output is kept;
  - a probe that fails grants no scope at all: the sweep leaves the exited attempt `normalizing` and records no verdict, so a transient error cannot make a local filesystem permanently "remote";
  - the real probe on the test's temporary directory is local;
  - an unknown identity grants no scope on a valid backend.
- **`TestAnalysisWriterOwnershipRealProcessExitAndColdReplica`** uses the real judge:
  - a generation recorded for a real helper process, which lives until its stdin closes, is untouched while that process runs, and is retired once it is killed and reaped;
  - the dataset then deletes and its output is removed;
  - a generation recorded with this node's machine and boot but another PID namespace (a restarted container, another pod on the node) stays `normalizing`, and DELETE answers `unjudgeable_writers: 1` with no `Retry-After`.
- **`TestAnalysisWriterOwnershipUploadRetryResumesAfterExit`**:
  - an unproven writer keeps its offset;
  - a proven one lets the retry resume, through the real HTTP PUT in the native profile and through the same helper and Begin path in the default build;
  - only the replacement's key enters the cursor, the replacement writer records B as its incarnation (in both builds, so the native run checks the retried Begin's own binding), and the exited writer's temporary object is cleaned.
- **`TestAnalysisWriterOwnershipWarnsWhenItCannotJudge`**: a controller without a process identity lists the candidates and logs, rate-limited, that it cannot judge them.
- **`TestAnalysisWriterOwnershipArtifactDirectoryIsNeverCreatedByTheJudge`** (replaces round 5's `ArtifactRootIsProvable`), on a fresh filesystem backend with no `_analysis`:
  - A begins an offset-0 upload writer and dies before writing; the exit scope fails with `fs.ErrNotExist`, and the upload retry's exit proof resolves nothing and logs the missing-directory warning;
  - B's Begin at that offset gets `ErrAnalysisUploadInFlight`, and DELETE answers 503 `analysis_dataset_deletion_pending` with `unproven_writers: 1`, `retryable: true` and `Retry-After` (`requireDeletionPending`);
  - with DELETE's cleanup queued the sweep has a candidate: it warns that it cannot judge, the writer stays unproven and a second DELETE answers the same;
  - none of these created `_analysis`;
  - another dataset's write creates it (mode 0700, not checked on Windows); the next sweep closes A's writer, DELETE returns 204, and every probe received `<root>/_analysis`.
  - The server's background sweep may run the probe too, so the probe's record is locked; the test renames nothing.
- **`TestAnalysisWriterOwnershipCleanupNeverCreatesTheArtifactDirectory`**: on a fresh filesystem backend, a DELETE that returns 204 and the queued dataset cleanup job both treat the missing `_analysis` as nothing to delete, and neither creates it.
- **`TestAnalysisWriterOwnershipDefaultRuntimeIsTheRealProcess`**: the production runtime of a server without an override. It is known on macOS, Linux and Windows; its identity and host are this process's; its judge calls this process and a live child alive and the reaped child exited; and its probe answers a missing path with `fs.ErrNotExist`, not "local".
- **`TestAnalysisWriterOwnershipSweepWrapsOnce`**: a pass that begins mid-inventory, after another pass moved the shared cursor, wraps once and still reaches the normalizing owners.
- **`TestAnalysisWriterOwnershipRequiresThePinnedBackend`**: a substituted backend grants no scope. An unknown identity, even with a valid identity value and a judge that proves every exit, binds nothing and judges nothing.
- **Background-sweep timing** (`evidence/tick-delay.log`):
  - an overlay delaying the loop's first tick passes every ownership test at each of 0, 1, 2, 3, 4, 5, 7, 10, 15, 25 and 50 ms. Each delay is one test-binary run with `-test.count=5`, so the evidence shows one `PASS` line per delay. In round 3 this rerun first failed at 1 ms; that found the non-wrapping sweep, now fixed and covered by `SweepWrapsOnce`;
  - an overlay running the writer sweep on every tick, every millisecond, passes all eleven ownership tests five times each, including round 7's `CleanupNeverCreatesTheArtifactDirectory`, round 6's `ArtifactDirectoryIsNeverCreatedByTheJudge` and round 5's `DefaultRuntimeIsTheRealProcess` (per-test counts in the evidence; the twelfth entry is the helper process's skip). Regenerated on the round-7 code.

**Native** (`analytics_duckdb`, 512 MiB / 128 MiB). `TestAnalysisWriterOwnershipNativePipelineRecordsItsIncarnation` runs the real HTTP upload, mapping, commit and DuckDB normalization:
- both the upload writer and the attempt record this process's identity;
- a sweep and an all-exited judgement do not touch the published dataset;
- DELETE returns 204.

**procidentity and localfs** (`evidence/procidentity-linux.log`):
- **`TestGoneDecidesOnlyProvableExits`**, a table that includes:
  - a reused PID, another namespace and a missing lookup;
  - an unknown machine's earlier boot;
  - the same boot on another or an unidentified machine;
  - macOS's machine-less same boot;
  - `Judgeable`, which mirrors `Gone`'s scoping: the same machine, boot and namespace, or an earlier boot of the same identified machine; not another PID namespace, another machine or an unknown machine's earlier boot.
- **`TestExitedJudgesLiveAndReapedProcesses`**, run on macOS and on Linux (a static test binary in the local Alpine image, as root and as `nobody`). It, and the API real-process and native-pipeline tests, fail rather than skip when no identity is available on macOS, Linux or Windows.
- **`TestDarwinHostIdentity`**: macOS `readHost` succeeds with namespace `darwin` and no machine identity. Its boot equals `kern.bootsessionuuid` and differs from `kern.uuid` (the kernel build UUID every Mac on one build shares), and launchd's start time is read.
- **Linux unit tests with an injected kernel** (they run in the ordinary Linux CI suite):
  - `TestProcessStartNeedsESRCHForAMissingEntry` (`EPERM`, nil and `ESRCH`);
  - `TestReadHostRefusesAForeignProcAndScopesTimeNamespaces`, including an unreadable time namespace, which is an error rather than "none";
  - `TestReadHostPairsMachineIDWithHostName`, with an injected host name: `machine-1@host-a`; no host name gives no machine identity; the `/var/lib/dbus/machine-id` fallback; `/etc/machine-id` taking precedence; an empty `boot_id` is an error;
  - malformed `/proc/<pid>/stat` lines (too few fields, no command bracket, a non-numeric start time) are errors.
- **`TestProcessStartRefusesHiddenProcesses`**, run on Linux with `/proc` remounted `hidepid=2` (a privileged local container, test as `nobody`).
- **localfs:**
  - `TestIsLocalOnTheTemporaryDirectory`, on macOS and Linux.
  - **Shared TMPDIR.** Only the tests about locality probe the real TMPDIR, and they fail, not skip, when it is on a shared or user-space filesystem (a rootless FUSE overlay, a network home, a self-hosted runner such as `apexload-ci-unprivileged` with such a TMPDIR): `TestIsLocalOnTheTemporaryDirectory` and the API tests `NetworkFilesystemIsNotLocal` (its "temporary directory is local" assertion) and `ArtifactDirectoryIsNeverCreatedByTheJudge`. Every other ownership test uses a fixed "local" probe. `evidence/shared-tmpdir-linux.log` shows exactly these three failing, and everything else passing, with TMPDIR on virtiofs in the Linux container, and all passing on a local `/tmp`. The runner provisioning list in `docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md` now requires a local TMPDIR and the job's own `/proc`.
  - `TestWindowsVolumeLocalIsFixedNTFSOrReFS`, portable: a fixed NTFS or ReFS drive only; a WinFsp mount, exFAT, FAT32, and removable, network or RAM drives are not local.
  - The Linux magic table also feeds 32-bit `f_type` values to `magicOf`, and the package tests ran as a 32-bit ARM (`GOARCH=arm`) binary in the container.
  - `TestIsLocalRefusesASharedFilesystem`, on a Docker Desktop bind mount (virtiofs) and on a symbolic link to it from a local directory, in the Linux container. It skips unless `LOCALFS_EXPECT_SHARED` is set.
  - Allowlist tables that run in ordinary CI: `TestLocalMagicTypesAreAnAllowlist` on Linux (NFS, SMB, SMB2, CIFS, FUSE, 9p, Ceph, virtiofs and unknown types are not local), `TestDarwinLocalNeedsALocalAllowlistedMount` on macOS (NFS, SMB, AFP, WebDAV, FUSE types and unflagged mounts are not local), and the portable `TestIsUNCRecognizesNetworkShares`.
- **CI lanes.**
  - The Windows HAR step of `.github/workflows/desktop.yml` runs `pkg/internal/procidentity` (including `TestWindowsHostIdentity` and `TestWindowsProcessStart`) and `pkg/internal/localfs`.
  - The desktop native-analytics step runs both packages on every desktop leg: Linux amd64, Linux arm64, macOS and Windows.
  - Both lines are pinned by `scripts/client_package_workflow_test.go`; removing either is caught. For the native step, the check also requires the desktop job's `runs-on: ${{ matrix.os }}`, exactly the four matrix legs from `include` alone, no `if:` or `continue-on-error` on the job or step, one such step, a script that starts with `set -euo pipefail`, and before the identity command only comments and plain `go test` lines (no shell operator, redirection, substitution, double quote or backslash outside single quotes, and no unterminated quote). Its self-test inserts each bypass and requires the refusal to name it.
  - The same native step runs the `TestAnalysisWriterOwnership*` API tests on Windows, Linux and ARM64 too, where the runner can compile `pkg/api`. None of the Windows runs has been executed yet: locally the Windows code only compiles.
- The browser HAR identity and reclamation tests pass.

**Mutations** (`go test -overlay`, `evidence/mutations.txt`). After round 7 there are 117 recorded mutants: 59 from round 1, 14 from round 2, 11 from round 3, 8 from round 4, 8 from round 5, 8 from round 6 and 9 from round 7. Round 5's ten marker mutants (X1 to X10) were removed with the marker in round 6. Four are obsolete, because the code they mutated no longer exists: F2 (survived), V1 and V2 (round 4's directory creation in the scope, removed in round 5), and K2 (round 5's command check, replaced in round 6 and re-targeted as Y7). Of the 113 applicable mutants, 110 are caught and three survivors are equivalent. R1 and T9, first caught by the removed `ProbesTheArtifactDirectory`, were re-run on the round-7 code and are caught by `ArtifactDirectoryIsNeverCreatedByTheJudge` (and T9 also by `NetworkFilesystemIsNotLocal`); Y8 was re-targeted at the round-7 operator set and is still caught.

| Guard mutated | Caught |
| --- | --- |
| Judge self, judge without exit proof, remote treated as quiesced (attempt) or closed (writer) | Yes |
| Exited owner keeps its import, failure without a revision advance, owner and revision both ignored in the owned check (G12) | Yes |
| Quiesced owner failed without a termination record, or recorded as remote-unproven (M3) | Yes |
| Cleanup not queued for an exited attempt or upload writer (M5) | Yes |
| Exited writer still blocks its offset, filesystem proof without closure, self-proof accepted, unbounded identity | Yes |
| Claim or Begin records no incarnation (storage); commit or upload binds none (native API) | Yes |
| Inventory never reaches the cleanup queue, snapshot never escalates, settled owner never escalates | Yes |
| Every pending reason miscounted (remote attempt and upload writer, unrecorded, unjudgeable, unplanned output), readiness ignoring unrecorded or unjudgeable writers, `Resolvable` dropping unrecorded or adding legacy | Yes |
| Remote backend probed as a filesystem, probe error ignored, every filesystem local, backend pin not required, unknown identity granting a scope or binding | Yes |
| Sweep skipping upload writers, generic 503, offset never resolved, retry after exit removed, `retryable` always, `Retry-After` always, every writer judged as this host's, repeated DELETE re-queuing cleanup | Yes |
| `procidentity`: earlier boot of an unknown machine, another PID namespace, reused PID, same boot on another machine (H1); P7 (recorded in round 1 against `SameHost`, since replaced) is the same-boot machine check that `Judgeable` now holds | Yes |
| Linux-only, in the container: `ESRCH` rule (P4), time namespace (P6), foreign `/proc` (P8), filesystem allowlist (F1L) | Yes |
| S8: an abandoned writer that is not closed no longer blocks Begin | Equivalent. Such a writer arises only when the import is cancelled or deleted, its project is deleted, or a normalizing import is claimed again (each through `analysisUploadQueueImportTx`, or with the import already past `uploading`). Begin is then refused by the import's state, or by the missing project, before the blocking check. |
| G1, G2: the owned check ignores the owner, or the revision | Equivalent alone: each of the two guards alone keeps a replaced owner from failing its successor's import. Removing both (G12) is caught by the lease-expiry test. |
| F2: `IsLocal` skips its existence check | Obsolete (not counted) after round 2: `IsLocal` now opens the directory on every platform, so a missing path fails the open itself. The Windows behaviour is unverified until the Windows lane runs `TestIsLocalOnTheTemporaryDirectory`. |
| Round 2: probing the archive root instead of `_analysis` (R1); a silent no-identity sweep (R2); `Judgeable` ignoring the PID namespace, at the unit, storage and API levels (R3, R3s, R3a; R3s re-detects the unit mutant, because the storage test's host judge calls `procidentity.Judgeable`); `Judgeable` refusing an earlier boot (R4); a boot-only API judge (R5); the retried Begin binding no incarnation (R6, native); the darwin rule ignoring `MNT_LOCAL` (R8); plain and extended UNC paths not recognized (R9, R9b) | Yes |
| Round 3: darwin `readHost` always failing, at the unit and API levels (T6, T6a); darwin recording a machine identity (T7); the Windows volume policy ignoring the filesystem name (T8); a probe error recorded as a remote verdict (T9); a pass that does not wrap (W1) | Yes |
| Round 3, Linux-only in the container: the `@hostname` suffix dropped (T1), the empty-host-name guard dropped (T2), the dbus machine-id fallback dropped (T3), the empty-`boot_id` guard dropped (T4), `f_type` sign-extended on 32-bit (T5) | Yes |
| Round 4 (harness `evidence/mutate-r4.py`): a missing `_analysis` not created before probing, or created with a mode other than the writer's 0700 (V1, V2; renamed from U1 and U2, which round 1 already used); the darwin boot read from `kern.uuid` (V3); a Windows identity accepted without `MachineGuid` or without a host name (V4, V5); the workflow check ignoring a step's `if:` or `continue-on-error` (V6, V7); `magicOf` without `~uint32`, caught by the linux/s390x cross-compile (V8). V1 and V2 tested round 4's directory creation in the scope, which round 5 removed; they are obsolete and not counted, and Y1 checks the inverse on the round-6 code. | Yes (V1, V2 obsolete) |
| Round 5 (harness `evidence/mutate-r5.py`): the default runtime's probe calling everything local (D1), its judge calling everything exited (D2), or having no identity (D3); `composeWindowsHost` accepting a value read with an error (C1, C2); the workflow check skipping the matrix legs (K1), accepting a weakened command (K2, on the round-5 check) or skipping the job keys (K3). D1 to D3, K1 and K3 were re-run on the round-6 code and are still caught; K2 is obsolete and not counted. | Yes (K2 obsolete) |
| Round 6 (harness `evidence/mutate-r6.py`): the judge creating a missing `_analysis` (Y1); a failed probe granting a remote scope (Y2); the workflow check accepting a duplicated step, `found == 0` for `found != 1` (Y3, the review's K6), a matrix `exclude:` (Y4), any line before the command (Y5), a script without strict mode (Y6) or a commented-out command (Y7, K2 on the round-6 check), and treating shell operators as a plain `go test` (Y8) | Yes |
| Round 7 (harness `evidence/mutate-r7.py`): the sweep creating `_analysis` once it has candidates (Q1, the review's CS-L1 mutant); cleanup's root open creating `_analysis` first (Q2, the review's Z5); no missing-directory warning (Q3); the workflow check accepting a backslash continuation (Q4), ignoring `runs-on` (Q5), accepting an unterminated quote (Q6), accepting the command before strict mode (Q7), or accepting `;` (Q8) or `&` (Q9) in a `go test` line | Yes |
| Round 2, Linux-only in the container: NFS added to the allowlist (R7); an unreadable time namespace ignored (R10); the stat field-count bound lowered (R11, caught by an index panic in the test) | Yes |

**Backends.**
- **Local filesystem:** exercised for real, as are the local-filesystem probe and a virtiofs mount.
- **S3, Azure Blob and GCS:** exercised through the repository's existing fakes: memblob behind `analysisCleanupPhysicalOps`, with their configuration validated.
- No real S3, Azurite or GCS emulator was run (see Remaining).

**Gates (after round 7).** CI runs the full suites on push, so round 7 did not repeat them all locally. Regenerated on the round-7 code: `gates-static.log`, `storage-sqlite.log`, `storage-postgres-full.log`, `race.log`, `api-native-analysis.log` and `tick-delay.log`, plus a final build, gofmt, the `TestAnalysisWriterOwnership*` tests and `scripts`. Still from round 6: the `pkg/api` shards, `browser.log`, `procidentity-linux.log` and `shared-tmpdir-linux.log` (round 7 changed neither `procidentity` nor `localfs`); `mutations-linux.txt` is from round 4.
- **Static** (`evidence/gates-static.log`):
  - `gofmt`, `go build ./...` and builds of `pkg/...` and `cmd/...` for linux, windows and darwin are clean;
  - `pkg/internal/localfs` and `pkg/internal/procidentity` also build for linux 386, arm, amd64, arm64, s390x, ppc64le and riscv64 and for freebsd. On 32-bit Linux the existing `pkg/internal/diskspace` and `pkg/protocol/jms` do not build; they are unchanged from the base;
  - `golangci-lint run` reports 0 issues on `pkg/api`, `pkg/storage`, `pkg/internal/...`, `pkg/protocol/browser` and `scripts`, and again for linux, windows and darwin on `pkg/api`, `procidentity` and `localfs`. With the native tag it reports one `rowserrcheck` in `pkg/api/analysis_acceptance_populations_native_test.go`, a file unchanged from the base, and none in changed files;
  - `go generate ./...` shows no drift;
  - `scripts/check-docs.py` and `scripts/ci/validate-github-actions.py` pass.
- **Storage:**
  - full `pkg/storage`, all of `pkg/internal/...` and `scripts` on SQLite passed (`evidence/storage-sqlite.log`);
  - full `pkg/storage` on PostgreSQL 16 with `-tags postgres_integration` passed, running 109 PostgreSQL tests with none skipped (282 s, `evidence/storage-postgres-full.log`).
- **`pkg/api`:**
  - full untagged `pkg/api` on the round-6 code, run in nine test-name shards plus its fuzz seed and example (`evidence/api-shard-*.log`), passed except the six `TestReplayArtifact*` tests in shard R, which already fail on macOS;
  - the native analytics selection with `-tags analytics_duckdb` and 512 MiB / 128 MiB passed (153 s, `evidence/api-native-analysis.log`).
- **`-race -count=2 -shuffle=on`** passed with no race reports (`evidence/race.log`) on:
  - the new storage tests, `procidentity` and `localfs`, and the `scripts` workflow-step check;
  - the PostgreSQL ownership suite;
  - the new and adjacent API upload, deletion and cleanup tests (`TestAnalysisWriterOwnership`, `TestAnalysisDurableUpload`, `TestAnalysisDatasetDeletion`, `TestAnalysisUploadCursor`, `TestAnalysisCleanup`), in the default and the native build.
- **Other:** full `pkg/internal/...` and `scripts` passed with storage, and full `pkg/protocol/browser` passed on the round-6 code (`evidence/browser.log`). The Linux container runs of `procidentity` (as root, as `nobody` and under `hidepid=2`), of `localfs` (on virtiofs) and of both packages as 32-bit ARM binaries are in `evidence/procidentity-linux.log`. The shared-TMPDIR proof on the round-6 code is in `evidence/shared-tmpdir-linux.log`: the Linux `pkg/api` writer-ownership tests and `localfs`, run with TMPDIR on a writable virtiofs bind mount and then on the container's local `/tmp`.
- **Concurrent writer during round 4.** For part of round 4 an earlier instance of this work was still running gates in the same worktree. Its logs and mutation harness were removed or replaced; rounds 5 and 6 regenerated every log with a single writer.
- The embedded dashboard bundle, which carries the Help copies of the edited guides, was not rebuilt.

<a id="roadmap-native-writer-ownership-2026-10-25--review"></a>
### Review

<a id="roadmap-native-writer-ownership-2026-10-25--review-round-1"></a>
#### Review round 1

| Finding | Handling |
| --- | --- |
| **H1** (High). A Windows replica could judge a live writer on another Windows host as exited: the boot counter is shared, and `Gone` compared the machine only for an earlier boot. | `Gone` now requires the same machine identity before a same-boot PID lookup too, and `SameHost` adds the same rule. Windows refuses an identity without `MachineGuid` and a host name. New table cases cover the same boot on another or an unidentified machine (mutants P5 and P7 are caught). The Windows boot value is unchanged: changing its format would make every identity recorded by an earlier binary read as an earlier boot of the same machine, which judges it exited. The machine match closes the shared-counter case and the same-boot clone case. |
| **H2** (High). The API tests raced the server's own background sweep. | Each test now installs B with no exits before minting generations, and proves A's exit only just before its explicit sweep; the pinned-backend test changes the backend before proving. Verified with a first-tick delay overlay at 0 to 50 ms and an every-millisecond sweep overlay (`evidence/tick-delay.log`). |
| **M1.** A network or FUSE "filesystem" backend was treated as local. | New `pkg/internal/localfs` allowlist probe, injected through the runtime. A probe that fails or reports a non-local root yields `remote_commit_unproven`. Tested with an injected probe, the real probe and a real virtiofs mount (mutants A1, A1b, A1c, F1L). |
| **M2.** The moved Windows tests no longer ran in CI. | The Windows step of `desktop.yml` now runs `pkg/internal/procidentity` and `pkg/internal/localfs`; the workflow validator and the `scripts` tests pass. |
| **M3.** The record claimed a proof for every failed import, but an already-quiesced owner was failed without one. | Failing an owned import now always writes a termination record, without `remote_commit_unproven` when the attempt was already quiesced. The effects table has a row for it. Fixing the mutant also unmasked the attempt outcome's `RemoteCommitUnproven` (S21, S22). |
| **M4.** The most common restart path was untested. | New lease-expiry test: A's lease expires, C claims again, then A's exit is proven. A is proven while C's owner and revision are unchanged. G12 is caught; G1 and G2 alone are equivalent (see Mutations). |
| **M5.** Cleanup queuing was not asserted. | The exited attempt's and the exited upload writer's jobs are checked in the real queue table, absent before and present after (S25, S26). The API test compares `ListAnalysisCleanupJobs` across a repeated DELETE. |
| **M6.** Pending reasons and the retryable rule were untested. | New cases: a remote upload writer, an unplanned output, a writer on another host, and `Resolvable`/`ResolvesHere` for each reason (U1, D1, D2, D3). |
| **M7.** The Linux `ESRCH` rule and `/proc/self` check had no CI coverage. | The stat reader, link reader and kill probe are injectable. Unit tests in the ordinary Linux suite cover a hidden entry (`EPERM`), `ESRCH`, a visible entry and a foreign `/proc` (P4, P8). |
| **M8.** `retryable: true` with `Retry-After` could be permanent. | New `unjudgeable_writers` reason for writers recorded on another host, boot or PID namespace. `retryable` now means "may still resolve", and `Retry-After` is set only while this host will settle an `unproven_writers` writer. A repeated DELETE of a cancelled or deleting import no longer saves it again or re-queues cleanup. The guide, spec and record state that HA takeover is covered only for a replica in the same PID namespace (A8, A9, A10). |
| **L1.** Linux time namespaces shift start times. | The time namespace joins `Namespace` when present (P6). CRIU-restored controllers are documented as unsupported. |
| **L2.** The sweep was silent when it could not act. | Inventory failures, resolution errors and scope refusals (missing identity, unpinned backend) log a Warn, at most every 10 minutes per server. |
| **L3.** The A6 "equivalent" survivor was killable. | The pinned-backend and network-filesystem tests use a runtime with a valid identity, `known: false` and an always-true judge; A6 and A6b are caught. |
| **L4.** Record and test corrections. | S8's reason now lists every path that abandons an unclosed writer. The mutation count matches its evidence, and the Linux container runs and P4 have logs. "The import is retried" is reworded. C's pending DELETE is described as after the sweep. The remote test's memblob is wrapped with a no-op `Close`. The guide restores the native worker's restart wording. Worker-failure injection and failure during cleanup are listed under Remaining. |

<a id="roadmap-native-writer-ownership-2026-10-25--review-round-2"></a>
#### Review round 2

| Finding | Handling |
| --- | --- |
| **M-1.** The locality probe checked the archive root, while artifacts are written one level down in `_analysis`, through links and mount points. | The scope probes `<root>/_analysis` through the new `analysisArtifactDirectory` helper. That helper now also builds the path for every analysis write, read, size read and cleanup, so the probe cannot drift from them. `IsLocal` opens the directory and judges the handle: `fstatfs` on Linux and macOS, `GetFinalPathNameByHandle` on Windows, so a link or junction to a share is judged by its target. A missing directory is not local. Tests: the API probe-path test, and a symbolic link into virtiofs in the Linux container (R1). Recording locality at Claim/Begin is listed under Remaining. |
| **M-2.** The rate-limited warning never fired for a controller with no identity. | The sweep no longer returns early without an identity: it lists candidates and, when there are any, logs that it cannot judge them. The cursor keeps the last warning for the new test (R2). |
| **M-3.** Nothing tested that `SameHost` compares the PID namespace. | Replaced by `Judgeable` (see L-1), with a table case for another PID namespace. New tests: a storage deletion-pending case with host A's machine and boot in another namespace (`UnjudgeableWriters: 1`), and the API cold-replica identity, which is now this node's machine and boot in another namespace (R3, R3s, R3a). |
| **L-1.** `ResolvesHere` and `unjudgeable_writers` disagreed with `Gone` about an earlier boot. | New `procidentity.Judgeable`, which mirrors `Gone`'s scoping (an earlier boot of the same identified machine is judgeable), used by the API's here-function (R4, R5). |
| **L-2.** Docs. | The guide states the distinct machine-id/host-name requirement and that Helm and compose deployments get no restart recovery. Its misattributed colon and its cross-reference are fixed, and `unrecorded_writers` there and in the storage comment now include a controller with no identity. The HAR guide's Windows section requires `MachineGuid` and a host name, and its proof rule states the same-boot machine match and the Linux time namespace. The native-upload record says "same host and PID namespace". The intro of this record states the Helm and compose scope. |
| **L-3.** HAR records from the earlier binary on Linux cannot be reclaimed until reboot. | Documented in the HAR guide as a fail-safe limitation: those generations stay until the machine reboots or they are removed by hand. A pid-only identity is not accepted: the judge cannot know which time namespace the recording process ran in, so start times could be compared across an offset. |
| **L-4.** The retried upload's incarnation binding was unasserted. | Both branches now read the replacement writer's journal and require `Incarnation == B`; the native run checks the retried Begin itself (R6). |
| **L-5.** The local-filesystem allowlist had no negative test in CI. | Table tests for the Linux magic map and the darwin flag/type rule, and a portable UNC classifier test (R7, R8, R9, R9b). |
| **L-6.** The new Windows workflow line was not pinned. | `scripts/client_package_workflow_test.go` pins both the Windows HAR line and the new native-step line, which runs the two packages on every desktop leg, macOS included. Removing either line was checked to fail the test. |
| **L-7.** Record accuracy. | The API ordering note, the probe-failure claim, the tick-delay description, F2 (now obsolete, Windows unverified), the CI lanes that run the ownership tests and have never run on Windows, and the shared-TMPDIR failure mode are all corrected above. |
| **L-8.** Two Linux guards were untested. | Injected-kernel tests for an unreadable time namespace and for malformed stat lines (R10, R11). |

<a id="roadmap-native-writer-ownership-2026-10-25--review-round-3"></a>
#### Review round 3

| Finding | Handling |
| --- | --- |
| **CS-M1.** On Windows a user-space filesystem (WinFsp, as rclone mounts by default) passed as local. | After the final path, the filesystem name is read from the same handle (`GetVolumeInformationByHandle`). The new portable `windowsVolumeLocal` admits only a fixed NTFS or ReFS drive, the policy of `winfs.ValidateVolume(…, "NTFS", "REFS")`. Table test `TestWindowsVolumeLocalIsFixedNTFSOrReFS` (T8). |
| **TD-M1.** The Linux machine-id and host-name pairing had no test. | `os.Hostname` is injectable. `TestReadHostPairsMachineIDWithHostName` asserts `machine-1@host-a`, no machine identity without a host name, the dbus fallback and its precedence, and that an empty `boot_id` is an error (T1 to T4, in the container). |
| **TD-M2.** The macOS identity path was tested only through tests that skip. | New `TestDarwinHostIdentity` (namespace `darwin`, a non-empty boot of at least 32 characters, no machine identity; round 4 pins the boot to `kern.bootsessionuuid`). `TestExitedJudgesLiveAndReapedProcesses`, the API real-process test and the native-pipeline test fail rather than skip without an identity on macOS, Linux and Windows (T6, T6a, T7). |
| **CS-L1.** A failed probe became a permanent remote verdict. | A probe error (including a missing `_analysis` before its first write) now grants no scope for that sweep, and records nothing. Only a definite not-local answer records `remote_commit_unproven` (T9). |
| **CS-L2.** A mount below `_analysis` is not seen. | Narrowed instead: the package comment, the record and the guide judge only the `_analysis` directory itself, and the guide forbids mounting another filesystem below it. |
| **CS-L3.** `unproven_writers` with `Retry-After` can promise a judgement that never comes. | Documented: `unproven_writers` and `Retry-After` now say the writer "may settle". Under `hidepid`, a dead writer's PID reused by another user's process stays undecidable. Recording the uid is listed under Remaining. |
| **CS-L4, TD-L1.** Docs. | The guide has a machine identity requirement for every host sharing the SQL store, naming the baked machine-id with a fixed host name case (compose `hostname:`, pod `spec.hostname`, StatefulSets), memory-snapshot clones and macOS clones. It copies the local-filesystem allowlist and the remote treatment of everything else (vfat, exFAT, ntfs3, MS-DOS, removable or RAM drives, a missing directory, a failed probe). Every place that said only "the same machine, boot and PID namespace" can judge now includes an earlier boot of the same identified machine: record, guide, spec and the storage comment. The spec says `<root>/_analysis`. The replaced-pod condition was stated, but inverted in the guide; round 4 corrects it (M1). |
| **CS-L5.** The sweep is not "within about a minute" at scale. | Reworded: about a minute per 400 outstanding generations per replica, with permanently blocked and not-yet-due jobs staying in the inventory. The tick-delay rerun separately found that a pass did not wrap, so a pass could end without reaching the normalizing owners. It now wraps once (W1, `SweepWrapsOnce`). |
| **TD-L2.** Mutation table labels. | P7 is relabelled as `Judgeable`'s same-boot machine check, and R3s is noted as re-detecting the unit mutant. |
| **TD-L3.** The every-millisecond evidence predated two tests. | Regenerated with per-test counts for the nine ownership tests (the tenth entry is the helper process's skip). The delay series was widened to 0–50 ms at `-test.count=5`. |
| **TD-L4.** The native step header was not pinned. | `scripts/client_package_workflow_test.go` pins the whole matrix step, from `- name: Native analytics contracts and controller build` with `shell: bash` and `env:` and no `if:`, through the identity and filesystem tests. Inserting an `if:` was checked to fail the test. |
| **TD-L5.** The shared-TMPDIR note was incomplete. | Every test that relies on the real probe is now listed, with the runner cases. |
| **TD-L6.** 32-bit Linux misread filesystem types. | `magicOf` reads `f_type` as unsigned 32-bit. A table case feeds the 32-bit values, and the package ran as a 32-bit ARM binary in the container (T5). |

<a id="roadmap-native-writer-ownership-2026-10-25--review-round-4"></a>
#### Review round 4

Round 4 found two Medium findings and eight Low ones; both reviewers' remaining findings were Low.

| Finding | Handling |
| --- | --- |
| **M1.** The guide inverted the replaced-pod condition ("judged only when its host name or machine ID differs"). | The guide now says that a replacement pod on another node can judge its predecessor only when its host name and machine ID both match (a baked machine-id with a fixed host name), which the machine identity requirement forbids for distinct hosts, and that a replacement on the same node never can, because it runs in a new PID namespace. The round 3 CS-L4 entry records the inversion and its correction. |
| **M2.** Five API tests depended on TMPDIR being a local filesystem, and only this record said so. | `actAsWriterB` uses a fixed "local" probe (`assumeLocalFilesystem`), and the real-process test installs the real identity and exit judge with that same probe. The real probe stays only in `TestIsLocalOnTheTemporaryDirectory` and the API locality tests `NetworkFilesystemIsNotLocal` and `ProbesTheArtifactDirectory`. The runner provisioning list in `docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md` now requires a TMPDIR on a local allow-listed filesystem and a `/proc` of the job's own PID namespace, with `/proc/self/ns/time` readable. On the final code, with TMPDIR on virtiofs in the Linux container, exactly those three tests fail and every other one passes; on a local `/tmp` all pass (`evidence/shared-tmpdir-linux.log`). `ProbesTheArtifactDirectory` was renamed `ArtifactRootIsProvable` in round 5 and replaced by `ArtifactDirectoryIsNeverCreatedByTheJudge` in round 6. |
| **L1.** A fresh filesystem backend could stay stuck forever when the first writer died before its `MkdirAll`. | A probe that finds the artifact directory missing creates it as the writer would (mode 0700) and probes again. `ProbesTheArtifactDirectory` now begins with A's pending writer and no `_analysis`: the retried offset resolves, the directory exists with mode 0700, the writer is closed and the upload resumes (V1, V2). Superseded in round 5 (M-A): the scope no longer creates a directory. Round 6 dropped round 5's marker too; the stuck first writer is a documented gap with an operator workaround (Remaining). |
| **L2.** The build broke on linux/s390x. | `magicOf` accepts `~uint32` as well; the two packages cross-compile for s390x, ppc64le and riscv64 too (V8). |
| **L3.** `SweepWrapsOnce` was in the local-TMPDIR list. | Removed: it passes on virtiofs. The list now names only the three locality tests. |
| **L4.** `TestDarwinHostIdentity` did not pin the boot source. | The test requires the boot to equal `kern.bootsessionuuid` and to differ from `kern.uuid`; the record no longer calls the length check "a boot session UUID" (V3). |
| **L5.** The workflow pin did not stop a trailing `if:` or `continue-on-error:`. | `TestDesktopNativeAnalyticsStepRunsUnconditionallyOnEveryLeg` parses the workflow YAML and refuses either key on the desktop job or the native step. `TestDesktopNativeAnalyticsStepCheckCatchesConditions` inserts each key after the step's name and after its last `run` line, and requires all four insertions to be caught (V6, V7). |
| **L6.** The Windows `MachineGuid` and host-name refusal had no negative test. | `readHost` on Windows now calls a portable `composeWindowsHost`, whose table test refuses a missing or empty `MachineGuid`, a missing or empty host name, and an empty boot (V4, V5). |
| **L7.** A storage contract comment omitted the same-machine condition. | The comment now reads: the same identified machine booted again, or the same boot, machine identity and PID namespace. |
| **L8.** TD-L3 claimed "all ten ownership tests". | Corrected to the nine ownership tests; the tenth entry is the helper process's skip. |

<a id="roadmap-native-writer-ownership-2026-10-25--review-round-5"></a>
#### Review round 5

Round 5 found three Medium findings and five Low ones.

| Finding | Handling |
| --- | --- |
| **M-A.** Creating a missing `_analysis` turned an unmounted share into a "local" verdict, and cleanup reported a missing root as nothing to delete. | Replaced round 4's directory creation in the scope with a provable root: a create-only marker whose random ID is bound in SQL (compare-and-set via insert-if-absent, no schema change). Writers bind it at import creation, before `Claim` and before `Begin`. The judge only verifies; cleanup jobs and DELETE require it and answer 503 `analysis_artifact_root_unproven` otherwise. Legacy roots are adopted by a writer or cleanup, never by the judge, and an empty unbound directory only by a writer. Binding at mint time makes the fresh-backend stuck writer impossible, so the sweep never creates a root; this deviates deliberately from the suggestion that it may, for the legacy-unmounted reason in the contract. Tests `ArtifactRootIsProvable`, `LegacyArtifactRootNeedsAdoption` and, in the native build, `ImportCreationBindsTheArtifactRoot`; two existing cleanup tests now bind the root in their fixtures, as import creation does. The storage suite's `artifact root binding is compare-and-set` races two stores binding different IDs (both see one winner, later binds never replace it) on SQLite and PostgreSQL. The Remaining overclaim about remounts is corrected (X1 to X10). **Superseded in round 6:** the user dropped the marker; the scope still never creates a directory. |
| **M-B.** The mode check failed on Windows. | The directory check runs everywhere; the 0700 mode only where `runtime.GOOS != "windows"`. |
| **M-C.** The default production runtime had no test. | `DefaultRuntimeIsTheRealProcess` checks the runtime a server without an override uses: its identity and host, the real judge against this process and a live then reaped child, and the probe on a missing path (D1 to D3). |
| **L-1.** The workflow check had gaps. | It now also refuses a missing or changed matrix leg (`strategy.matrix.include[].os`) and requires the exact command line. The self-test inserts `if:` and `continue-on-error` on the job, after the step's name and after its last key before the next step. It also drops a leg, comments the command out and appends `\|\| true`. Every refusal must name what it caught (K1 to K3). |
| **L-2.** `composeWindowsHost` error-only paths. | Cases for a `MachineGuid` and a host name read together with an error (C1, C2). |
| **L-3.** `tick-delay.log` predated round 4. | Regenerated on the round-5 code. |
| **L-4.** Duplicate mutant ID `U1`. | Round 4's set is renamed V1 to V8 here, in `evidence/mutations.txt` and in `evidence/mutate-r4.py`. |
| **L-5.** Provisioning doc and record wording. | The runner requirements name `RealProcessExitAndColdReplica` and `DefaultRuntimeIsTheRealProcess` as needing the job's own process identity, list `/proc/sys/kernel/random/boot_id`, and say only the locality tests depend on the probe's answer; the record says the same. |

<a id="roadmap-native-writer-ownership-2026-10-25--review-round-6"></a>
#### Review round 6

Round 6 found one High and three Mediums, all from round 5's marker, and five Lows.

| Finding | Handling |
| --- | --- |
| **High.** A filesystem without hard links (exFAT, FAT, gcsfuse, s3fs, blobfuse2, rclone, Azure Files over SMB) refused every import: the create-only marker needs `link`. | **User decision: drop the marker.** `analysis_artifact_root.go`, the storage methods `AnalysisArtifactRoot` and `BindAnalysisArtifactRoot`, the `analysis_artifact_root_v1` setting, the root checks at import creation, commit/`Claim`, upload `Begin`, cleanup and DELETE, and the `analysis_artifact_root_unproven` 503 are removed, with their tests (`ArtifactRootIsProvable`, `LegacyArtifactRootNeedsAdoption`, `ImportCreationBindsTheArtifactRoot`, the storage compare-and-set subtest, the cleanup fixtures' bindings) and mutants (X1 to X10). The regenerated store overlay has no root methods. The rule is the one from before round 4's L1, without the sweep's directory creation (Contract, invariant 10). |
| **Medium.** Legacy roots were adopted through monitor writes. | Gone with the marker. |
| **Medium.** A wrong binding (an unmounted share at upgrade, a moved volume) had no recovery path. | Gone with the marker; the underlying unmounted-share gap is documented as pre-existing, with the operator rule (Remaining). |
| **Medium.** Several marker guards were untested. | Gone with the marker. The remaining rule is tested by `ArtifactDirectoryIsNeverCreatedByTheJudge` (Y1, Y2). |
| **Low.** A stale test name in `analysis_writer_ownership_test.go`'s probe comment. | Now names `ArtifactDirectoryIsNeverCreatedByTheJudge`. |
| **Low.** The workflow check's step count could be weakened to `found == 0` (K6). | The self-test duplicates the step and requires "2 native analytics steps" (Y3). |
| **Low.** An `exit 0` or `set +e` before the command line was not caught. | The script must start with `set -euo pipefail`, and before the identity command it may hold only comments and plain `go test` lines (no shell operator, substitution or `exit` outside single quotes). Self-test cases for `exit 0`, `set +e` and dropped strict mode (Y5, Y6, Y8). |
| **Low.** A matrix `exclude:` was not caught. | The matrix may hold only `include`; a self-test case adds an `exclude` (Y4). |
| **Low.** A possible Windows flake: a test renamed `_analysis` while the server's background loops ran. | No remaining test renames `_analysis`. The new test only creates it through a write, and locks its probe record against the background sweep. |

<a id="roadmap-native-writer-ownership-2026-10-25--review-round-7"></a>
#### Review round 7

Round 7 found no High or Medium findings, only Lows. All were fixed, and under the user's rule (a round with only Lows is fixed without another review) the review loop ends here.

| Finding | Handling |
| --- | --- |
| **CS-L1.** The sweep was never run while it had a candidate and `_analysis` was missing, so a sweep that created the directory survived. | After DELETE queues the stuck dataset's cleanup, `ArtifactDirectoryIsNeverCreatedByTheJudge` runs the sweep and requires its "cannot be judged" warning, the writer still unproven (a second DELETE answers the same) and `_analysis` still absent (Q1). |
| **CS-L2.** The stuck-first-upload docs named the wrong answer and overstated the gap. | `docs/NATIVE_ANALYTICS.md` and Remaining now name the 409 "upload cursor was not committed; reload durable cursor", say that while the import is `uploading` only a retry of that offset judges the writer and the sweep reaches it only after DELETE queues its cleanup, and that any artifact write, including a monitor artifact (`writeMonitorArtifact`), creates `_analysis`. |
| **CS-L3.** No operator signal. | `resolveExitedAnalysisUploadOffset` logs a warning, rate-limited on its own every 10 minutes, naming the directory and the workaround when the scope fails with `fs.ErrNotExist` (Q3). DELETE's answer is documented; the optional `Retry-After` change was not made. |
| **TD-L1.** Workflow check bypasses. | `plainGoTest` refuses a backslash outside single quotes, so `-args \` cannot join the identity command onto a previous line, and the desktop job's `runs-on` must be `${{ matrix.os }}`. Self-test cases for both (Q4, Q5). |
| **TD-L2.** Untested workflow guards. | Self-test cases for `go test ./x; set +e`, `go test ./x && set +e`, an unterminated quote and the identity line before strict mode (Q6 to Q9). The `exit` substring check is dropped: with operators banned, an `exit` cannot follow a `go test` on its line. |
| **TD-L3.** Cleanup never creating `_analysis` was untested. | `CleanupNeverCreatesTheArtifactDirectory`: a DELETE that returns 204 and the dataset cleanup job on a backend with no `_analysis` leave it absent (Q2, the review's Z5). |
| **TD-L4.** The mutation count overclaimed. | V1, V2 and K2 are marked obsolete with F2; R1 and T9 were re-run and cite their current catching tests. The review's suggested totals (105 applicable, 102 caught) counted F2 as applicable; with all four obsolete mutants excluded the counts are 104 applicable and 101 caught before this round, 113 and 110 after it. |
| **TD-L5.** Wording. | Contract and invariant 10 say the exit scope (sweep, upload retry), DELETE and cleanup never create `_analysis`, with DELETE's status going through `analysisWriterHere`; the guide says the writer is judged at the client's next retry or, after DELETE, at the next sweep (about a minute); DELETE's answer (`unproven_writers: 1`, `retryable: true`, `Retry-After: 60`) is documented and asserted with `requireDeletionPending`. |

<a id="roadmap-native-writer-ownership-2026-10-25--merge-with-be-b-linux-crash-qualification"></a>
### Merge with BE-B Linux crash qualification

Main gained [PR #287](https://github.com/JasonCzech/APEX_Load/pull/287), whose `57fe3e0ac` treats a Linux process in the zombie (`Z`) or dead (`X`, `x`) state as exited when reclaiming HAR working generations (an orphaned Playwright driver reparented to a PID-1 generator container without `--init`). This slice moved that code to `pkg/internal/procidentity`, so the merge keeps one parser, `procidentity.ParseLinuxStatStart`, used by `processStart`. Analysis writer ownership inherits the rule: a zombie writer process counts as exited. That is safe for the local-filesystem proof, because a zombie makes no further system calls and has closed every descriptor, so no later write of its can land; a malformed stat record still fails closed. `TestParseLinuxStatStartTreatsZombieAndDeadAsExited` (portable) and be-b3's `TestParseLinuxProcStatStartTreatsZombieAndDeadStatesAsExited` (now calling the shared parser) cover it. Merge checks: build, vet (darwin and linux), `procidentity`, `localfs`, the HAR tests in `pkg/protocol/browser` and the `TestAnalysisWriterOwnership*` tests; the full suites run in CI.

<a id="roadmap-native-writer-ownership-2026-10-25--remaining"></a>
### Remaining

- **Remote backends need a commit fence or an intervention.** A dataset with `remote_commits_unproven` (including a filesystem backend on a network or FUSE mount) stays deletion-pending. The next step is one of:
  - a provider-side fence, such as create-only writes (S3 `If-None-Match`, GCS `ifGenerationMatch=0`, Azure `If-None-Match: *`) plus retained tombstones at an exited generation's keys;
  - an explicit administrator attestation for analysis datasets, reusing the evacuation intervention pattern (REC-B).
- **No analysis intervention path exists.** Legacy writers (pre-journal uploads, unknown owners, unplanned outputs) remain deletion-pending as before, now with a reason in the DELETE answer.
- **Hosts that cannot judge.** These keep their work owned and report `unjudgeable_writers`:
  - an HA replica on another host;
  - a replaced Kubernetes pod and a restarted container, which gets a new PID namespace (this includes docker compose restarts);
  - macOS after a reboot, which has no machine identity.

  The next step is a kernel-released file lock per incarnation on a verified local filesystem, which survives PID namespaces on one kernel. A lease, a heartbeat or a NotReady status remain non-proof.
- **Heavy-work reservations and staging.** After a restart, the native worker relies on kernel containment: Linux `Pdeathsig`, and the Windows job object's kill-on-close. Darwin has none, which is the existing "Darwin hard-tree authority" item. The exited attempt's private staging directory under `_analysis-cache` is still never removed. The next step is to record the worker's incarnation through a pipeline start hook, then reclaim staging once both the controller's and the worker's exits are proven.
- **Shared original copies.** `original-<file>.parquet` is written by every attempt of an import. A late remote copy from a lost attempt can therefore overwrite the retry's copy, with identical bytes. The next step is per-attempt originals in the journal's planned keys.
- **PID reuse under hidepid.** An identity does not record the uid, so a dead writer's PID reused by another user's process under `hidepid` stays undecidable: `kill` answers `EPERM`. Recording the uid would let a different uid prove the PID taken.
- **The stuck first upload on a fresh filesystem backend.** Where nothing has been written yet, an upload writer that died after `Begin` and before its first write stays owned, because no judge creates `_analysis`. The client's retry of that offset answers 409 "upload cursor was not committed; reload durable cursor" and logs a rate-limited warning naming the directory and the workaround. While the import is `uploading` only a retry of that offset judges the writer; the sweep reaches it only once DELETE has queued the dataset's cleanup. DELETE answers 503 `analysis_dataset_deletion_pending` with `unproven_writers: 1`, `retryable: true` and `Retry-After: 60`. Any later artifact write creates `_analysis` (an upload, a normalization output or a monitor artifact via `writeMonitorArtifact`, so the gap closes at the first write of any kind); the writer is then judged at the client's next retry of that offset or, after DELETE, at the next sweep (about a minute). Operator workaround: create `<archive root>/_analysis` on the mounted volume, mode 0700, owned by the controller's user (`docs/NATIVE_ANALYTICS.md` says so).
- **Unmounted archive shares (pre-existing, not closed here).** A path does not say which volume it names: with an NFS, SMB or EFS share unmounted, writers create `_analysis` on the local mount point and write there, and cleanup treats a missing `_analysis` as nothing to delete. Operators must start the controller only after the archive volume is mounted (`docs/NATIVE_ANALYTICS.md`).
- **Locality at mint time.** Locality is judged when the exit is proven, against whatever filesystem `_analysis` is on then; a generation does not record the volume or the locality of the directory it wrote. A directory moved from a network filesystem onto a local one between the write and the proof is judged local. Binding the archive volume's identity (and its locality) at `Claim`/`Begin` would close this and the two items above. Round 5's alternative, a hard-link marker whose ID was bound in SQL, was rejected in round 6: it refused every import on filesystems without hard links (exFAT, FAT, gcsfuse, s3fs, blobfuse2, rclone, Azure Files over SMB) and needed a rebind and recovery path of its own.
- **Earlier-binary HAR identities on Linux.** They hold the PID namespace without the time namespace, so on a kernel with time namespaces they are reclaimed only after a reboot or by hand.
- **Live upload writers.** A pending writer of an upload that is still `uploading` is resolved only when its offset is retried, or after cancellation.
- **Memory-snapshot clones without a machine-id.** On Linux without `/etc/machine-id`, two VMs cloned from one memory snapshot share `boot_id`, the PID namespace and an empty machine identity. Hosts sharing analytical storage must have distinct machine-id and host-name pairs, as the HAR guide already requires.
- **Real emulators.** None was run. SeaweedFS, the repository's S3 fixture image, needs path-style addressing for a loopback endpoint, which the archive bucket URL does not set.
- **NA-C exit items not covered:**
  - native worker failure injection, with the controller alive;
  - failure during cleanup (a crash between an object delete and the job's completion);
  - deployed multi-host failure injection;
  - the dashboard items: revision conflicts, input deletion while a view is open, and both auth profiles. This slice changes no dashboard code; the dashboard shows the 503's `error` text.

<!-- Original record: docs/qa/reviews/roadmap-partition-precedence-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-partition-precedence-2026-10-20"></a>

## Native partition precedence — 2026-10-20


This [NA-A qualification slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#na-a--deterministic-import-semantics) locks down the existing v1/v2 contract without changing normalization: physical columns provide timestamps and measurements; an explicit mapping chooses a constant metric name or a physical name column, and choosing both is rejected. Hive-style directory labels remain source provenance and cannot override those selections or create schema columns.

A native fixture places immutable Parquet bytes beneath conflicting timestamp, value, metric, source, region and additional directory labels. Direct native and contained preview retain physical numeric values. Contained normalization and query preserve both constant/column metric names, v2 source/dimension identities and independently known sums. Original and nested source hashes remain equal before and after the workflow.

<a id="roadmap-partition-precedence-2026-10-20--validation-and-review"></a>
### Validation and review

The installed native analytics suite passed 2 packages, 315 top-level tests and 782 subtests, including the contained v1/v2 precedence fixture. The generated analytics inventory, complete documentation QA package and documentation link checks passed. This test-and-guide slice changes no production or embedded Help content; the preceding committed assets remain unchanged. Exact commands, source/asset hashes and raw logs are retained in the evidence bundle (`SHA256SUMS`).

The same test fails in both mapping versions against a temporary control that enables Hive inference for native preview: it observes directory timestamp 1 and value 999 in place of the physical timestamp 1700000040 and value 7. This intentionally changed control is separate from a prior-product regression failure. Its hashes and failing log are retained. Final applied-source and staged review found no remaining actionable findings.

No source format, mapping version or generated contract changes. DECIMAL precision disclosure, broader schema drift and external native/reference qualification remain separate pending requirements. The [owning guide](../../NATIVE_ANALYTICS.md) preserves the supported interpretation.
