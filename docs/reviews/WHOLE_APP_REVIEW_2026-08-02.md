# Whole-Application Review (Partial — 18 of 38 areas) — 2026-08-02

**Branch:** main @ dc05008ab (clean tree, == origin/main) · **Scope achieved:** 23 of 38 planned areas (~435k of ~620k first-party LOC) — the ENTIRE Go backend and ops surface: the pkg/api control plane (fully partitioned), the recording/replay pipeline, all importer families, pkg/engine, pkg/storage, pkg/distributed, the transform + data-protection libraries, the full pkg/protocol tree, and CLI/controller/deployments/mcp (nine areas added 2026-08-03). Remaining: the dashboard frontend (9 areas) and 5 cross-cutting sweeps. The run was descoped mid-flight due to session token limits; findings below cover only the listed areas.
**Method:** 23 area finders over a git-archive snapshot of HEAD; 165 raw findings → 164 after dedup → **162 confirmed** (138 defects + 24 opportunities) / 2 refuted. Lean adversarial verification (token-constrained): 1 independent verifier per claimed-Critical/High finding and per suspected regression, batched single-vote verification for Medium/Low and opportunities. Prior-findings ledger (316 entries from the July reviews) tags rediscoveries instead of double-reporting them.

## Remediation status

<!-- REMEDIATION: rewritten after the fix phase completes -->
_Not yet remediated. Critical/High fix phase pending user go-ahead (token budget)._

## Coverage — what this review did and did not examine

**Covered (finders + verification):** AREA-01 api-execution-core, AREA-02 api-scheduling-plans-capacity, AREA-03 api-results-archive-reporting, AREA-04 api-import-orchestration, AREA-05 api-distributed-fleet, AREA-06 api-scripts-authoring-dsl, AREA-07 api-tests-scenarios-datapools, AREA-08 api-platform-core, AREA-09 api-integrations-and-library, AREA-10 recording-replay-pipeline, AREA-11 convert-enterprisearchive-java, AREA-12 convert-enterprisearchive-core, AREA-13 convert-bruno-requestimport-credentialpolicy, AREA-14 convert-api-spec-and-datasource-importers, AREA-16 engine-core, AREA-19 distributed-data-plane, AREA-20 storage-core-identity-content, AREA-21 storage-execution-import-dialect, AREA-22 import-transforms-and-data-protection-libs, AREA-18 java-and-messaging-stack, AREA-15 convert-perftool-importers, AREA-17 protocol-web-and-transports, AREA-23 cli-controller-deployments (the last nine added 2026-08-03 — the complete Go backend and ops surface).

**NOT covered** (descoped when the session hit token limits): the entire dashboard frontend (9 areas) and 5 of the 6 cross-cutting sweeps (concurrency, generated-freshness, test-quality, docs-accuracy, dependency-upgrade regressions; storage-dialect parity was substantially covered inside AREA-21). A follow-up pass is needed for whole-app coverage; the finder area map is preserved in the session plan.

## Baseline — all green

Captured before any change, against the exact HEAD bytes (which equal `origin/main`, so CI's full `gotestsum -- -race -shuffle=on -count=1 ./...` run at this commit also applies):

- `go build ./...` — clean · `go vet ./...` — clean
- dashboard `npm run typecheck` — clean · `npm run lint` — 0 errors (8 pre-existing warnings)
- dashboard `npm test` (vitest) — **274 files / 2782 tests passed** in 27s
- Toolchains: Go 1.26.5, Node 24 (CI uses 26), JDK 26, Playwright chromium

## Verdict

**138 confirmed defects: 0 critical, 17 high, 70 medium, 51 low** (+24 verified opportunities) across the covered areas. 21 previously-reported-still-open rediscoveries; 26 suspected regressions of previously-fixed findings.

Headline themes:

1. **Silent fidelity loss across the importer families** — the largest cluster, 5 Highs. Enterprise-archive: `web_set_user` basic auth wiped when a runtime config is present, `web_global_verification` checks wiped by wholesale WebRuntime replacement, repeated ITEMDATA form fields dropped with nondeterministic ordering. JMeter: sampler-scoped ResponseAssertions and timers silently dropped (`jmeter/converter.go:713`), and `enabled="false"` ignored so disabled elements import as live (`converter.go:448`). Around them: trailing registrations dropped, zip basename-collision misresolution, scanner truncation, the multi-`.jmx` ZIP importing only the first plan, TruClient double-importing function bodies. k6 and recorderjson are the honest exceptions.

2. **False success / wrong results at runtime** — SOAP fault detection is silently disabled by the default response-body discard, so faulted calls report as passing (`soap/soap.go:170`) — the same false-success family as the July Java `lr.AUTO` defect, now in the SOAP transport.

3. **Distributed results integrity** — confirmed from both sides of the RPC seam: the controller-side archive merge concatenates per-generator interval summaries so fleet percentiles are wrong; the generator side replays the last window's aggregates as fresh data on no-new-evidence ticks (`generatorrpc/service.go:2157`, regression-tagged); and `tolerate_generator_loss` escalates a dead generator into a whole-fleet stop.

4. **Over-eager credential masking destroying legitimate data** — 4 Highs spanning API, engine, and transforms: `formSecretPattern` on JSON bodies deletes everything after a matched secret value (`paramize/secrets.go:19`), the runtime secret matcher admits arbitrarily short/common values (`redaction.go:297`), the matcher set is recompiled 2–3× per action per VUser (`imported_credential_runtime.go:167`), and `runtime_mask.go` treats any value containing `@` as a credential.

5. **Deletion FK integrity** — schedule-launched tests cannot be deleted (`deleteTestGraphTx`, regression-tagged), projects with direct-import receipts cannot be deleted (`deleteProjectGraph` misses `direct_import_receipts`, found independently by both storage finders), and deletion orphans `powerbi_publishes` rows.

6. **Deployment/ops correctness** — the Helm import-only Bruno mode writes encrypted artifacts to an `emptyDir` while mounting the required PVC at an unused path, so artifacts silently vanish on pod restart (`helm/apexload/templates/controller.yaml:196`); plus the known-open onprem GOMEMLIMIT/MAX_VUSERS sizing items.

7. **Integration write-safety** — bulk GitHub sync ignores the configured `pr` strategy and commits directly to the default branch, and skips the drift check so it silently overwrites remote edits.

8. **O(n²) accumulated-state broadcasts** — debug-session progress re-serializes the entire accumulated session per event (regression-tagged); browser-replay progress embeds the full event list per broadcast.

9. **Locks held across I/O, platform-wide stalls** — launch-window admission and capacity fair-share sorting run SQL under `tr.mu`, remote-run handlers hold an RLock across cross-controller HTTP, and project deletion stalls every mutating request platform-wide.

10. **Time/accounting correctness** — DST fall-back double-fires schedules, usage accounting bills full duration for stopped runs, conflict-state schedules durably rewritten every 30s.

By contrast, the 2026-08-03 additions verified substantial strengths across the backend: `pkg/engine` hot paths; generatorrpc epoch fencing and two-phase assignment; `pkg/storage` lock ordering, CAS fences, and dialect guards; the transform/data-protection libraries where July's hardening held; the java/messaging stack with zero High findings (new `pkg/javarunner`/`pkg/javasidecar` assessed exceptionally well engineered); k6/recorderjson import honesty; the web transports' resource discipline; and the `apexctl` CI gate path (distinct exit codes, bounded retries, evidence persisted before aborts) plus `pkg/kubeops`/`pkg/internal`/`pkg/perfevidence`. See the per-area appendix.

## Suspected regressions of previously-fixed findings

_Caveat: the "regression" tag comes from a file-level match against the ledger of previously-fixed findings — it means "a confirmed defect in a file where a July review previously fixed something," not necessarily the same defect recurring. Check the verification evidence per finding._

- **#21 Gradual ramps persist a full test row (incl. every compiled script) per single-VUser step ** — `pkg/api/testexecution.go:2130`
- **#22 Usage accounting bills configured duration for every retained test, including runs that st** — `pkg/api/usage_accounting.go:245`
- **#89 broadcastEvent reads live ManagedTest fields after tr.mu is released, racing with locked w** — `pkg/api/testexecution.go:2662`
- **#23 Fair-share pending sort issues a reservation SQL query per sort comparison under tr.mu on ** — `pkg/api/capacity.go:556`
- **#90 Run-calendar truncation keeps an arbitrary append-order subset instead of the events neare** — `pkg/api/run_calendar.go:269`
- **#93 Single-slot global Bruno inspect semaphore holds its exclusive section across the request-** — `pkg/api/handlers_bruno_import.go:232`
- **#3 Every debug progress broadcast serializes the entire accumulated session, making replay co** — `pkg/api/script_debug_manager.go:1379`
- **#99 Replay-session start endpoint lacks the credential-preserving-import echo special case its** — `pkg/api/handlers_script_debug.go:181`
- **#100 Replay progress never refreshes session activity, so an actively-running or breakpoint-pau** — `pkg/api/script_debug_manager.go:664`
- **#36 Any value containing '@' is classified as a credential, breaking email-valued parameters a** — `pkg/api/runtime_mask.go:87`
- **#102 Data-pool update mixes PATCH and PUT semantics: archived_at is absent-preserving but omitt** — `pkg/api/handlers_data_pools.go:262`
- **#46 Every browser-replay progress broadcast embeds the full accumulated event list (O(n^2) CPU** — `pkg/api/browser_replay_manager.go:1112`
- **#47 Synchronous classic replay endpoint exceeds the server's fixed 60s WriteTimeout and loses ** — `pkg/api/handlers_replay.go:172`
- **#45 Browser-recording list summaries and WS broadcasts always report 0 network requests, 0 web** — `pkg/api/browser_recording_manager.go:1176`
- **#107 Marker handler swallows JSON body decode errors, producing misleading validation errors** — `pkg/api/handlers_browser_recorder.go:537`
- **#49 Live import pays for a discarded classification pass, including a duplicate full runtime-r** — `pkg/convert/enterprisearchive/java_import.go:165`
- **#55 Native inspection re-parses YAML O(requests x ancestors x passes) and runs the full candid** — `pkg/convert/bruno/inspect.go:167`
- **#13 No-new-evidence ticks replay the last window's aggregates and transactions as fresh data** — `pkg/distributed/generatorrpc/service.go:2157`
- **#78 Per-group publishers each archive a full cross-group interval summary, duplicating window...** — `pkg/distributed/generatorrpc/service.go:1646`
- **#15 Schedule-launched tests cannot be deleted: deleteTestGraphTx trips tests(id) foreign keys** — `pkg/storage/sqlite_tests.go:1390`
- **#80 powerbi_publishes rows are orphaned forever: no delete path on test or project deletion** — `pkg/storage/sqlite_tests.go:1323`
- **#83 HAR scrub destroys base64-encoded response content while leaving encoding flag intact** — `pkg/scrub/scrub.go:1484`
- **#70 CDP sessions leak on every window switch / popup activation** — `pkg/protocol/browser/runtime_context.go:629`
- **#124 b.pwContext read without lock races concurrent close paths** — `pkg/protocol/browser/runtime_context.go:396`
- **#125 HTTP regex extractor without a capture group reports 'no match' though the pattern matched** — `pkg/protocol/http/extract.go:1064`
- **#86 apexctl run silently ignores malformed and equals-form flags, silently disabling SLA gate...** — `cmd/apexctl/main.go:263`

## Previously reported, still open

Rediscovered by this review; counted in the totals but tagged rather than re-reported as new:

- **#24 Conflict/failed-blocked schedules are durably rewritten with a revision bump on every 30s ** — `pkg/api/scheduler.go:2203`
- **#25 DST fall-back: schedules firing inside the repeated local hour fire twice, ~60 minutes apa** — `pkg/api/scheduler.go:589`
- **#94 java_vuser launch gating performs a fleet-metadata DB read and placement simulation under ** — `pkg/api/distributed_execution_backend.go:641`
- **#95 Startup-failure cleanup stops assigned generators sequentially, so a wide-fleet failure ca** — `pkg/api/distributed_execution_backend.go:736`
- **#38 Run manifest 'initial configuration' is read from the continuously rewritten durable recor** — `pkg/api/test_manifest.go:284`
- **#103 Manifest configuration maps StartedAt from the stored record's ScheduledStartAt** — `pkg/api/test_manifest.go:375`
- **#41 Failed requests permanently burn their Idempotency-Key for the TTL** — `pkg/api/middleware.go:307`
- **#48 Proxy recorder silently drops requests once caps are hit — no warning, counter, or event** — `pkg/recorder/recorder.go:1251`
- **#114 orderReplayOps comparator is not transitive, so produce-before-consume ordering can silent** — `pkg/convert/asyncapi/converter.go:657`
- **#O2 Schedule CRUD, approval resolution, and timeslot bind hold the global runner write lock ac** — `pkg/api/scheduler.go:2548`
- **#O4 Parallelize per-target script distribution and shard assignment during distributed startup** — `pkg/api/distributed_execution_backend.go:939`
- **#O7 User listing for scoped admins is an O(users x bindings) N+1 over the store** — `pkg/api/handlers_auth.go:1673`
- **#O9 Every browser-replay GET takes the manager write lock and runs a full prune sweep** — `pkg/api/browser_replay_manager.go:794`
- **#O10 Retire or quarantine the dead native Java lanes and their pipeline-reconstruction tests** — `pkg/convert/enterprisearchive/java_import.go:622`
- **#O14 matchHAR re-parses URLs O(refs x entries) times** — `pkg/convert/salesforce/analyzer.go:440`
- **#11 Runtime secret matcher admits arbitrarily short/common values (all cookie values, auth us...** — `pkg/engine/redaction.go:297`
- **#68 scrubRuntimeSecretText treats pre-existing NUL bytes in evidence as matched secret placeh...** — `pkg/engine/redaction.go:943`
- **#69 JWT-shaped regex redacts ordinary dotted hostnames/identifiers in result metadata and met...** — `pkg/engine/redaction.go:101`
- **#O17 Dead helper addTokensForElapsed and one-interval Resume hiccup in TokenBucket.SetPaused** — `pkg/engine/pacing.go:341`
- **#O19 rewriteSQL substring translation is a latent Postgres-parity trap** — `pkg/storage/sqlite.go:377`
- **#71 SSE streams silently killed by the default 60s client timeout despite a larger validated ...** — `pkg/protocol/http/http.go:536`

## Refuted (do not fix)

- ~~Duplicate session-ID create misreported as capacity exhaustion~~ (`pkg/storage/sqlite_protected_source_sessions.go:23`, AREA-21) — Scenario unreachable: session IDs are minted server-side (uuid.New per request, handlers_project_secrets.go:560); no retry can reuse an id.
- ~~BuildJavaVuserSourceManifest nulls the entire manifest silently on any non-canonical or ca~~ (`pkg/convert/enterprisearchive/java_source_manifest.go:44`, AREA-11) — Fail-closed: import_promotion_gating.go:147-151 blocks approval with an explicit missing-manifest blocker; no silent weakening.

# Confirmed findings

## Critical (0)

## High (17)

#### 1. Distributed archive merge concatenates per-generator interval summaries; report computes f
**Where:** `pkg/api/distributed_archive.go:568` · **Area:** AREA-03 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

stageRemoteArchive merges remote bundles by byte-concatenating each generator's interval_summaries.jsonl (mergeRemoteArchiveDataset, pkg/api/distributed_archive.go:568-616; writeMergedRemoteArchiveFiles:548-566). Generators write per-generator, per-window values (TPS = own successes/window, ActiveVUsers = own VUs, TotalTx = per-window count — pkg/distributed/generatorrpc/service.go:1656-1690, archiveTotalsFromBatch:2766-2782), and distributed runs archive only via stageRemoteArchive (pkg/api/distributed_stream.go:1490,1516 — no controller-side fleet recorder). buildRunArchiveReport then takes Overview.PeakTPS and PeakActiveVUsers as the MAX over these per-generator rows (pkg/api/archive_report.go:423-425, 435-437), appends SummaryTimeline in file order (archive_report.go:439-450) producing a non-monotonic multi-sweep timeline, and archiveSummaryBucketIndex/archiveSummaryBucketSeconds binary-search and diff adjacent timestamps assuming sorted order (pkg/api/archive_loaders.go:598-623), which is violated at every bundle boundary — corrupting per-action PeakTPS bucket attribution (archive_report.go:106-119), action/group-filtered interval summaries (archive_loaders.go:394-459), and load-comparison bucketing (pkg/api/load_comparison.go:364).

**Failure scenario:** A 4-generator distributed run sustains 250 TPS and 500 VUs per generator (fleet: 1000 TPS, 2000 VUs). The merged archive stores gen1's full timeline, then gen2's, etc. The archived report — the permanent record — shows Peak TPS ~250 and Peak Active VUsers ~500 (quoted in the executive summary, PDF, release trend, and default annotations), the summary-timeline SVG draws four overlapping time sweeps (svgLineChart plots by array index, pkg/api/report_charts.go:60-93), per-action Peak TPS attributes transactions to arbitrary buckets via sort.Search over unsorted timestamps, and the timeline's total_transactions column carries per-window semantics where single-node archives are cumulative. Live dashboards (fleet-summing fixed 2026-06-20) and the archive disagree by ~Nx on every multi-generator run, silently.

**Suggested fix:** At merge time (writeMergedRemoteArchiveFiles), group interval summaries across bundles by truncated timestamp and emit one fleet row per interval: sum TPS/ActiveVUsers/window counts, recompute ErrorPct, merge per-group metrics, carry cumulative TotalTx/TotalErrors; sort output by timestamp. Alternatively sort-and-aggregate at read time keyed on manifest schema version.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # V014 — CONFIRMED (high)

Chain, all snapshot-verified:

1. Distributed finalize archives ONLY via stageRemoteArchive (pkg/api/distributed_stream.go:1490,1516); grep of distributed_stream.go/distributed_execution_backend.go shows no controller-side recorder — runArtifacts.recordLiveMetrics (pkg/api/execution_backend.go:417-424) is the local backend's path.
2. Each generator records per-generator interval rows: batch built from local s.assignments (generatorrpc/service.go:1801-1826 metricsBatchLocked/ForAssignment), then RecordIntervalSummary with TPS=own totalSuccess/window, ActiveVUsers=own engine stats (service.go:1656-1682).
3. Merge is pure byte concatenation per bundle in target order — mergeRemoteArchiveDataset (pkg/api/distributed_archive.go:568-616) via writeMergedRemoteArchiveFiles (:548-566); manifest just sums counts (:508). No sort, no per-timestamp aggregation anywhere in stageRemoteArchive (:39-127).
4. Loaders never sort: readAllArchiveSummaries (archive_loaders.go:708-712) streams file order; buildRunArchiveReport takes PeakTPS/PeakActiveVUsers as max over rows and appends SummaryTimeline in file order (archive_report.go:412-451).
5. archiveSummaryBucketIndex sort.Search + adjacent-timestamp bucket seconds assume ascending timestamps (archive_loaders.go:598-623) — violated at every bundle boundary, corrupting filtered/per-action summaries (:378-459).

Refutation hunt: no aggregation helper, no schema-version gate, distributed_archive_test.go fixtures are single-bundle/trivial (lines 172, 491) — nothing pins fleet aggregation. Fleet peaks understated ~Nx on every multi-generator run; permanent archived record silently disagrees with live dashboards.

</details>


#### 2. tolerate_generator_loss cannot survive a dead generator: crash escalates to whole-fleet st
**Where:** `pkg/api/distributed_resilience.go:390` · **Area:** AREA-05 · **Category:** fault-tolerance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The automatic generator-loss path (tolerateMetricsStreamLoss, pkg/api/distributed_resilience.go:1132-1151) calls EvacuateGenerator, which after staging the replacement REQUIRES the lost source to acknowledge an exact-generation StopTest (distributed_resilience.go:390-410 via stopEvacuationClient at 909-931, which treats every gRPC error including Unavailable as failure). A generator that is actually down (pod OOM-killed, node drained/decommissioned — the primary loss mode the flag advertises) can never ACK, so the evacuation always lands in failClosedUncertainEvacuationWithTopologyRelease (965-1047): the run is forced to STOPPING with remoteFinalizing=true, ALL healthy generators are force-stopped, and because stopEvacuationParticipants (943-951) includes the dead source (participants built from runClientsByTarget at 394/933-941) the terminal retry enqueued at 1032-1046 (terminal_retry.go:20-41, gRPC errors are retryable, <=1s backoff cap) re-attempts forever and finalizeFailClosedEvacuation never runs — the run never reaches a terminal status.

**Failure scenario:** Distributed run with assignment.tolerate_generator_loss=true across gen-a/gen-b plus spare gen-c (active, advertises evacuation_assignment_state_v1, has capacity). Kill gen-a's pod (kubectl delete node / OOM). gen-a's metrics stream errors -> consumeMetrics -> failDistributedTestOnStreamError -> tolerateMetricsStreamLoss -> EvacuateGenerator: replacement staged and committed-ready on gen-c, then StopTestAtGeneration to dead gen-a fails 3x with Unavailable -> uncertain -> run set STOPPING with error 'generator evacuation state is uncertain: stop evacuated source gen-a: ...', gen-b and gen-c force-stopped (all load ends), and the evacuation terminal retry re-sends stops every ~1s indefinitely because gen-a never acknowledges. The run stays in STOPPING (non-terminal, blocking its slot and showing active in the UI) until the same address becomes reachable again or the controller restarts — indefinitely for a decommissioned generator. Expected per the feature: the run continues on gen-c; the flag makes the outcome strictly worse than having it off (which yields an immediate clean FAILED).

**Suggested fix:** For an unreachable source, replace the ACK requirement with the existing epoch fence: record the source's claim-TTL deadline, wait it out (or probe until dial fails N consecutive times past the deadline), then treat the source as fenced-by-expiry and commit the replacement. Separately, make the fail-closed cleanup terminalize once every REACHABLE participant has acknowledged, tracking the unreachable one in a reconciliation list instead of blocking the terminal transition on it.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # V025 — CONFIRMED (high)

Trigger: dead generator's metrics stream errors -> distributed_stream.go:610/1204 failDistributedTestOnStreamError -> :1211 tolerateMetricsStreamLoss -> distributed_resilience.go:1144 EvacuateGenerator. Staging touches only replacements/survivors (applyEvacuationShardAdjustments:697-788 excludes evacuatedTarget), so with a healthy spare it succeeds. Fence: :390-400 requires the DEAD source to ACK stopEvacuationClient (:909-931 — 3 attempts, every gRPC error incl. Unavailable = failure; stopGeneratorTest distributed_epoch.go:125-134 has no unreachable-tolerance); source-client-nil branch :401-409 is equally uncertain. Either way -> failClosedUncertainEvacuationWithTopologyRelease (:965): sets STOPPING + remoteFinalizing (:984-986), then stopEvacuationParticipants (:1013, :943-951) over participants built from runClientsByTarget (:394, :933-941) — includes the dead source — so stopErr != nil, and :1032-1045 enqueues a terminal retry re-running the same participant stops; terminal_retry.go:35+44-56 classifies gRPC transport errors retryable -> loops until shutdown; finalizeFailClosedEvacuation (:1050, the only FAILED transition) never runs. Healthy generators are force-stopped; re-entry blocked by remoteFinalizing (distributed_stream.go:1217). Refuters hunted: no NotFound/Unavailable tolerance, no TTL-expiry terminalization (comment :1029-1031 explicitly rejects it). Tests only cover ACK-capable sources (distributed_resilience_test.go:700-704 "first fail-closed retry then acknowledges"). With flag off, path :1214-1232 yields immediate clean FAILED — flag makes the advertised scenario strictly worse.

</details>


#### 3. Every debug progress broadcast serializes the entire accumulated session, making replay co
**Where:** `pkg/api/script_debug_manager.go:1379` · **Area:** AREA-06 · **Category:** performance-scalability · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

broadcast() (script_debug_manager.go:1375-1406) puts a full session.snapshot() into message["session"] for every message type, and snapshotLocked (1502-1561) deep-copies ALL accumulated Events, Logs, and RawOutput. handleReplayProgress (843-891) broadcasts per executed leaf (script_debug_action_started + action_completed/failed + script_debug_event), BeforeAction broadcasts per action (1936-1943), and the engine emits one event per executed leaf including every loop iteration (pkg/engine/replay.go:987-1005, EventIndex = len(report.Events)-1, append-only). appendLogLocked (1585-1609) has no entry cap and at the default info level attaches formatDebugEventRaw output (2261-2305) containing full request headers, body, and extractions to BOTH Logs[].Raw and RawOutput. Hub.BroadcastJSON (pkg/api/websocket.go:292-299) marshals unconditionally. Net effect: for n executed leaves, ~3n broadcasts each O(n) in size — O(n²) marshal CPU, allocation, and WebSocket bytes — plus per-session memory holding every wire body twice, retained for the 2h terminal TTL across up to 250 sessions (replay_session_retention.go:17-19).

**Failure scenario:** Operator starts a debug session (POST /api/v1/scripts/{id}/debug/sessions) on an imported LR script whose http_sequence containers hold ~3,000 requests (typical enterprise-archive import), or on a script with a control_loop of a few thousand iterations. Each executed leaf appends a full raw request/response block to Logs and RawOutput and triggers ~3 broadcasts that re-marshal every prior event and log line. With ~2 KB average event+log payload, the final broadcasts are ~15-20 MB each and total marshaled traffic is tens of GB: controller CPU pins on json.Marshal, the WebSocket/dashboard becomes unusable, and the interactive debugger — a headline workflow for imported-script migration — effectively hangs with no workaround (broadcasting cannot be disabled). The terminal session then holds all bodies in RAM for 2 hours.

**Suggested fix:** Broadcast deltas: omit the full session payload from per-event/per-action messages (the event/action fields already carry the delta) and send the full snapshot only on session_started/terminal messages or on explicit GET. Cap Logs/RawOutput (ring buffer, e.g. 5,000 entries / 32 MB) and gate formatDebugEventRaw raw-body attachment behind debug/trace log level.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # V032 — confirmed (high)

Full chain established at HEAD (snapshot dc05008ab), all attempts to refute failed.

1. Production wiring: manager built with the server hub (pkg/api/script_debug_manager.go:149, pkg/api/server.go:411); debug worker sets OnProgress -> handleReplayProgress (script_debug_manager.go:418-419); POST /api/v1/scripts/debug/sessions exercised in handlers_replay_test.go:503.
2. Per executed leaf the engine publishes ActionCompleted/Failed + EventRun (pkg/engine/replay.go:982-1004, EventIndex=len(report.Events)-1, append-only across loop iterations); BeforeAction adds "script_debug_action" (script_debug_manager.go:1937). handleReplayProgress broadcasts each (852/859/888) — ~3 broadcasts/leaf.
3. broadcast() puts a full session.snapshot() into message["session"] for EVERY message type (1379, 1387); snapshotLocked deep-copies ALL accumulated Events/Logs/RawOutput (1542-1553). Grep found NO cap/ring-buffer in the file.
4. appendLogLocked (1585-1609) appends unbounded; default LogLevel "info" (341 + normalizeDebugLogLevel default, 2221-2228) admits the per-leaf info-level entry whose Raw = formatDebugEventRaw (879) with full headers/body/extractions (2261-2305), stored twice (Logs[].Raw + RawOutput line, 1604-1608). event.Body is the full scrubbed request body, untruncated (replay.go:1185).
5. Hub.BroadcastJSON marshals unconditionally (pkg/api/websocket.go:292-299). Terminal retention 2h TTL / 250 sessions (pkg/api/replay_session_retention.go:17-19).

Net: O(n²) marshal bytes/CPU over n leaves + unbounded per-session raw-body retention. Prior-report items 37/38 (UNCOMMITTED_REVIEW_2026-07-22.md:668-688) are different defects (terminal-broadcast suppression on lease loss; ObserveCorrelationEvidence lock contention) — no regression link.

</details>


#### 4. Bulk GitHub sync push never implements the 'pr' strategy: direct commits even when project
**Where:** `pkg/api/github_sync_service.go:150` · **Area:** AREA-09 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

runPush (pkg/api/github_sync_service.go:150-214) defaults strategy to "pr" (lines 156-158) and passes it only to enforceGitHubProjectPolicy (line 176), which rejects strategy=="commit" alone when RequirePullRequest is set (pkg/api/github_enterprise.go:364-371). The actual write is always a direct PutFile onto job.Branch (line 190) — there is no CreateBranch/CreatePull anywhere in runPush — so a bulk push job labeled 'pr' passes the required-PR policy check and then commits straight to the target branch. The single-script push handler (pkg/api/github_handlers.go:456-487) implements the PR flow correctly, so the two push paths diverge.

**Failure scenario:** Admin enables a project GitHub policy with Enabled=true, RequirePullRequest=true, BulkSyncEnabled=true. An API caller POSTs /api/v1/integrations/github/sync-jobs {kind:"push", strategy:"pr"} (or omits strategy — it defaults to "pr"). enforceGitHubProjectPolicy passes because only "commit" is rejected, then every selected script is committed directly to the protected target branch via the Contents API; no PR branch or pull request is created. The job returns status "complete" with Updated counts, so the caller believes the PR workflow ran. The admin-mandated review gate is silently bypassed and the branch history now contains direct commits.

**Suggested fix:** In runPush, when strategy=="pr": resolve base head SHA, create an apexload/<job-id> branch once per job, PutFile all selected scripts onto it, then open a single PR to job.Branch (mirroring handlePushScriptGitHub); alternatively reject kind=push jobs whose effective strategy is "pr" with a clear 'bulk PR push not supported' error instead of silently direct-committing.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # V054 — CONFIRMED (high)

Chain, all at snapshot HEAD dc05008ab:

1. Route wired: POST /api/v1/integrations/github/sync-jobs registered (pkg/api/server.go:1059) with editor-level access (pkg/api/route_catalog.go:187) → handleCreateGitHubSyncJob (pkg/api/github_enterprise.go:667). Request accepts Kind ("push", line 678) and Strategy (line 664) with no strategy-value validation; passes Strategy through to syncService.Run (line 720).
2. Run dispatches kind=push → runPush with input.Strategy (pkg/api/github_sync_service.go:68).
3. runPush defaults empty strategy to "pr" (github_sync_service.go:156-158); strategy is used ONLY in the policy check at line 176. enforceGitHubProjectPolicy rejects only strategy=="commit" under RequirePullRequest (pkg/api/github_enterprise.go:368-370), so "pr" passes.
4. The write is an unconditional direct PutFile onto job.Branch (github_sync_service.go:190). grep confirms zero CreateBranch/CreatePull/GetBranchHeadSHA calls anywhere in github_sync_service.go — no PR flow exists.
5. Divergence proven: single-script push implements strategy "pr" correctly — GetBranchHeadSHA/CreateBranch/PutFile-to-prBranch/CreatePull (pkg/api/github_handlers.go:456-487).
6. Job completes with status "complete"/Updated counts (github_sync_service.go:71-76,211), so the caller believes the PR workflow ran.

No refuting guard found: BulkSyncEnabled check (github_enterprise.go:701-703) only blocks when bulk sync is disabled; handler-level policy check (line 705) has the same commit-only rejection. RequirePullRequest is silently bypassed on the bulk path — direct commits land on the target branch. Failure scenario as stated is accurate.

</details>


#### 5. web_global_verification content checks silently wiped by wholesale WebRuntime replacement
**Where:** `pkg/convert/enterprisearchive/config_merge.go:60` · **Area:** AREA-12 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Convert appends web_global_verification checks to Script.Config.WebRuntime.ContentChecks during the loop (converter.go:348), but mergeScriptConfig (called at converter.go:839) replaces dst.WebRuntime entirely with the cfg-derived WebRuntime whenever the latter is non-empty (config_merge.go:59-61). Stock VuGen default.cfg always contains [ModemSpeed] with both toggles off, which parseSourceModemSpeedSection maps to NetworkSimulationMode="max" (runtime_config.go:3388-3391), making src.WebRuntime non-empty for essentially every real archive. web_global_verification is not in the reapply pass, so its checks (and other loop-written web_runtime attributes such as web_set_user.host and web_add_filter compatibility rows) are lost while the source review still reports status=converted targeting config.web_runtime.content_checks (converter.go:349).

**Failure scenario:** Import a Web archive whose script registers web_global_verification("Text=FATAL ERROR","Fail=Found") and which carries a stock default.cfg. The imported script has zero content checks, so every replay PASSES even when the application serves the error text on every page — silently wrong verdicts on all runs of that script, with the import-review table asserting the check was converted.

**Suggested fix:** Make mergeScriptConfig merge WebRuntime additively (append ContentChecks, union AdditionalAttributes, keep cfg values for network/streaming/DFE), or capture the loop-collected ContentChecks/attributes before the merge and re-append them afterward as the reapply pass does for HTTP controls.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # V074 — CONFIRMED (high)

Full causal chain established statically at HEAD:

1. Loop appends checks: converter.go:346-351 — `web_global_verification` appends to `result.Script.Config.WebRuntime.ContentChecks` and records source-review status "converted" targeting config.web_runtime.content_checks.
2. Production wiring: zip.go:157-164 — ConvertZip resolves default.cfg → `opts.RuntimeSettings = runtimeCfg.ToScriptConfig()`; zip.go:367-370 — web http/soap path calls `Convert(parsedActions, opts)`.
3. Trigger realism: runtime_config.go:527 wires `parseSourceModemSpeedSection`; runtime_config.go:3388-3391 maps both-toggles-off [ModemSpeed] → `NetworkSimulationMode="max"`; golden fixture testdata/golden_web_http_html_vugen24/default.cfg:139-143 shows this stock section (`EnableCustomModemSpeed=0`, `EnableModemSpeed=0`).
4. Non-empty src: runtime_config.go:821-846 — any NetworkSimulationMode populates `config.WebRuntime`, so isEmptyWebRuntimeConfig (config_merge.go:155-157) is false.
5. Wipe: converter.go:838-839 merge runs AFTER the loop; config_merge.go:59-61 does wholesale `dst.WebRuntime = src.WebRuntime` — no field-level merge.
6. No guard: reapply pass source_runtime.go:274-288 has no `web_global_verification` case; grep shows no other production code re-appending ContentChecks.
7. Tests don't pin the combo: converter_test.go:1866-1968 asserts the check survives only with a default.cfg lacking WebRuntime sections ([WEB]+[ThinkTime] only).

Consequence follows: stock-cfg web archives lose all global content checks silently while the import review claims "converted" — replays false-PASS on error pages. No runtime test needed; chain unambiguous.

</details>


#### 6. web_set_user basic auth silently wiped whenever the archive has a runtime cfg
**Where:** `pkg/convert/enterprisearchive/converter.go:836` · **Area:** AREA-12 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

In Convert, applySourceWebSetUser (source_runtime.go:59-65) writes web_set_user credentials into Script.Config.HTTP.Auth during the function loop, but converter.go:836 then replaces Config.HTTP wholesale with *opts.HTTPConfig (always non-nil in ConvertZip when any cfg resolves, zip.go:158-162), and the post-merge reapply pass reapplySourceRuntimeControlConfig (source_runtime.go:270-289) has no web_set_user case even though the loop adds web_set_user to sourceRuntimeControls (converter.go:580). The auth is never restored; the source-review mapping still reports web_set_user as status=converted targeting config.http.auth (converter.go:582).

**Failure scenario:** Import any real VuGen Web archive whose Action.c calls web_set_user("user","pass","host:port") and which contains a default.cfg (they all do). The generated script has empty config.http.auth, the import-review table claims the call was converted to config.http.auth, and replay receives 401s on every authenticated request with no reviewable explanation.

**Suggested fix:** Add a web_set_user case to reapplySourceRuntimeControlConfig that re-runs the config-only part of applySourceWebSetUser (auth + web_set_user.host attribute) after the HTTP replace/merge, mirroring how web_set_timeout is reapplied.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # V073 — CONFIRMED (high)

Full causal chain established statically in snapshot (HEAD dc05008ab):

1. Inside `Convert` (converter.go:142), the function loop handles `web_set_user` at converter.go:579-584: `applySourceWebSetUser` (source_runtime.go:40-71) writes `result.Script.Config.HTTP.Auth = dsl.AuthConfig{basic,user,pass}` at source_runtime.go:59-65, appends fn to `sourceRuntimeControls` (converter.go:580), and records a source-review mapping status=converted target=config.http.auth (converter.go:582).
2. Later in the same `Convert`, converter.go:835-837 wholesale-replaces `result.Script.Config.HTTP = *opts.HTTPConfig` when non-nil — wiping Auth.
3. Production path: `ConvertZip` sets `opts.HTTPConfig = runtimeCfg.ToHTTPConfig()` whenever a runtime cfg resolves (zip.go:157-162); real LR archives ship default.cfg. `ToHTTPConfig` (runtime_config.go:879) populates Auth only from a cfg `[Authentication] UserName` (runtime_config.go:986-991), normally absent — so the replacement carries empty Auth.
4. Reapply loop (converter.go:841-843) → `reapplySourceRuntimeControlConfig` switch (source_runtime.go:270-289) covers lr_continue_on_error / web_cache_cleanup / web_set_sockets_option / web_set_timeout / web_set_option-family only — no `web_set_user` case. Grep: the ONLY write to `HTTP.Auth` in the package is source_runtime.go:60 (source_runtime.go:526 is metadata redaction only, not a restore).
5. Guard hunt: mergeScriptConfig (config_merge.go:51-53) replaces HTTP only from cfg-derived src — no restore. Existing test `TestConvertMapsWebSetUserToHTTPAuth` (converter_test.go:831) uses empty ConvertOptions, so the wipe path is untested.

Consequence follows: empty config.http.auth, review table falsely says converted → replay 401s. No runtime test needed; chain is complete.

</details>


#### 7. ITEMDATA form bodies drop repeated field names and have nondeterministic field order
**Where:** `pkg/convert/enterprisearchive/parser.go:896` · **Area:** AREA-12 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

extractItemData builds web_submit_data bodies via formData := make(map[string]string) keyed by field name, so LR's repeated Name= entries (multi-select, checkbox groups) collapse to the last value, and the body is assembled by ranging over the map (parser.go:927-931), so field order is randomized per process. This is the primary path, not just legacy: the AST parser routes through the same parseFunctionBlock (ast_parser.go:180) and convertAction uses fn.Body directly (action_convert.go:102-113).

**Failure scenario:** Import a recorded web_submit_data with a checkbox group (Name=color repeated three times). The converted request posts only the last value — the server receives a different form submission than was recorded, silently, on every replay. Independently, re-importing the identical archive produces byte-different bodies (field order shuffles), breaking import reproducibility and any diff-based review.

**Suggested fix:** Collect ITEMDATA pairs into an ordered []struct{Name,Value} from fn.CallArgs (order is already deterministic there), keep duplicates, and join in source order; drop the map entirely.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # V075 — CONFIRMED (high)

Chain, all at snapshot HEAD dc05008ab:

1. **Defect code**: pkg/convert/enterprisearchive/parser.go:897 `formData := make(map[string]string)`; parser.go:920 `formData[currentName] = arg.Value` — a repeated `Name=` (checkbox group / multi-select, standard VuGen recording) overwrites the prior value, so only the last duplicate survives. parser.go:927-931 builds the body by `for k, v := range formData` — Go map iteration order is deliberately randomized, so field order differs per process.
2. **Reachability**: parser.go:416 dispatches `web_submit_data`/`web_submit_form` → parseWebSubmitData (parser.go:657-670), which calls extractItemData at parser.go:669. Both production parse routes hit it: regex path parser.go:298 and AST path ast_parser.go:180 share parseFunctionBlock.
3. **Consumption**: action_convert.go:102-112 copies `fn.Body` into `action.Body` verbatim (only C-unescape + param-ref rewrite; no rebuild, no sort/canonicalization).
4. **No refuting guards**: grep ITEMDATA — extractItemData is the sole body builder; tests (web_http_functions_test.go:143, converter_test.go) use only single-field ITEMDATA so nothing pins order or duplicate handling.

Consequence follows: replayed POST silently posts only the last duplicate value; re-imports produce byte-different bodies. Static chain is language-guaranteed; no runtime test needed.

</details>


#### 8. Sampler-scoped ResponseAssertion and timers silently dropped in JMeter import
**Where:** `pkg/convert/jmeter/converter.go:713` · **Area:** AREA-15 · **Category:** silent-drop · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

walkThreadGroup never descends into HTTPSamplerProxy children (guard at converter.go:713), convertHTTPSampler only parses extractor children via parseSamplerExtractors (converter_extractors.go:16-63, extractor element types only), and diagnoseJMeterChildren treats ResponseAssertion/ConstantTimer as 'known supported' (converter_diagnostics.go:148-149,182-189) so it emits no diagnostic. A ResponseAssertion or timer nested under a sampler's hashTree — the default placement when adding an assertion or per-request think time in the JMeter GUI — is dropped with no assertion/think-time on the action, no warning, no unsupported entry, and no stats increment.

**Failure scenario:** User imports a JMX where 'Login' has a child Response Assertion checking the body for 'Welcome' (added via right-click > Add > Assertions in JMeter). The imported action has zero assertions and the import reports no warning; with IncludeAssertions a generic status_code 200 is added, masking the loss. Load runs pass while the app serves an error page with HTTP 200 — silently wrong pass/fail results across every run of the imported script.

**Suggested fix:** In the HTTPSamplerProxy case, walk sampler children for ResponseAssertion (append parseResponseAssertions output to the action) and timer elements (set per-action think time), mirroring jmeterSamplerScopedHeaders; anything else non-extractor should produce a needs_review diagnostic instead of silence.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # P01 — Sampler-scoped ResponseAssertion and timers silently dropped (CONFIRMED, high)

Full causal chain verified statically at HEAD.

1. Tree shape: buildTree (pkg/convert/jmeter/converter_xml.go:58-73) attaches a sampler's following hashTree contents as the HTTPSamplerProxy treeNode's Children — exactly where the JMeter GUI puts per-sampler assertions/timers.
2. Drop: walkThreadGroup's recursion guard (converter.go:713) `child.Element.Name != "HTTPSamplerProxy"` never descends into sampler children, so the ResponseAssertion case (converter.go:513-516, sole caller of parseResponseAssertions) and timer case (507-512) never fire for them.
3. No sampler-side pickup: convertHTTPSampler builds Assertions solely from scope-level ctx.assertions (converter_sampler.go:79) and ThinkTime from ctx.timerMs (129-132); sampler children are parsed only by parseSamplerExtractors (converter_extractors.go:16-63, extractor elements only) and jmeterSamplerScopedHeaders (HeaderManager only, converter.go:798).
4. Silence: diagnoseJMeterChildren (converter.go:546) → diagnoseJMeterElement returns false with no diagnostic because isKnownJMeterSupported includes ResponseAssertion and all six timer types (converter_diagnostics.go:148-149, 182-185); no unsupported entry, no source mapping, no stats increment.
5. Masking: IncludeAssertions adds generic status_code 200 (converter_sampler.go:126-128).

Tests pin nothing opposite: both fixtures (converter_test.go:58-67, 353-375) place assertions/timers only as scope-level siblings. Consequence (silently wrong pass/fail on imported runs) follows; severity high stands — same class as the sampler HeaderManager silent-drop already fixed per the comment at converter.go:517-523.

</details>


#### 9. JMeter enabled="false" attribute ignored — disabled elements imported as live
**Where:** `pkg/convert/jmeter/converter.go:448` · **Area:** AREA-15 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

No code in pkg/convert/jmeter ever reads the JMX 'enabled' attribute (grep over the package finds only the unrelated CookiesEnabled bool at converter.go:460). walkThreadGroup converts every child regardless, so samplers, thread groups, controllers, CSV configs, headers and assertions that the author explicitly disabled in the JMeter GUI are imported as executable actions/config.

**Failure scenario:** A plan contains a disabled 'DELETE /admin/reset' sampler kept for debugging (enabled="false" — JMeter skips it). Import converts it into a live action; the first load run replays the destructive request against the target environment. Conversely a disabled ThreadGroup becomes a whole extra imported script, inflating load.

**Suggested fix:** At the top of the walkThreadGroup loop (and in findTreeNodes for thread groups), skip elements with enabled="false" and record an Info diagnostic + source mapping with status 'ignored' so conservation stats stay honest.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # P02 — JMeter enabled="false" ignored — CONFIRMED (high)

Claim verified statically at every hop.

1. **Parse keeps but never reads the attribute.** converter_xml.go:31-33 copies all XML attrs into `node.Attrs`; `buildTree` (converter_xml.go:58-72) and `findTreeNodes` (:75-84) have no enabled filter. Package-wide grep for "enabled" hits only the unrelated `CookiesEnabled` literal at converter.go:459-460 — no code path reads `Attrs["enabled"]`.
2. **Conversion converts everything.** `walkThreadGroup` (converter.go:444-716) switches on `child.Element.Name` with no enabled check; disabled HeaderManager/CSVDataSet/assertions/samplers all convert. Disabled ThreadGroups are picked up by `findTreeNodes(trees, "ThreadGroup")` at converter.go:203 and become full scripts (converter.go:279).
3. **Production reachable.** pkg/api/handlers_import_jmeter.go:29 and :52 call `jmeter.ConvertJMX` on raw JMX bytes; no preprocessing strips disabled elements.
4. **Real JMeter semantics.** The GUI Toggle/Disable writes `enabled="false"`; JMeter's HashTree compile (convertSubTree) removes disabled elements and their subtrees before running. Keeping disabled debug samplers in a plan is common practice.
5. **No refuting guards.** residual_review/disposition passes key on element names, not attributes; all test fixtures use enabled="true" (converter_test.go et al.), so nothing pins the correct behavior.

Failure scenario (disabled destructive sampler replayed; disabled ThreadGroup inflating load) follows directly. Severity high stands.

</details>


#### 10. Full runtime-secret matcher recompiled 2-3x per action per VUser with no change detection
**Where:** `pkg/engine/imported_credential_runtime.go:167` · **Area:** AREA-16 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

publishRuntimeCredentialRedaction (imported_credential_runtime.go:167-201) is invoked with no unchanged-set short-circuit by replaceRuntimeCredentialScopes (136-165). For any cookie-bearing HTTP script it runs at least twice per action: stageRuntimeCredentialSessionSnapshot on every Execute result (vuser_execute.go:73-86; the HTTP adapter sets RuntimeCredentialValuesSnapshot on every result, pkg/protocol/http/http.go:222-227) and commitRuntimeCredentialSessionSnapshot at every resolveAction (vuser_resolve.go:198), plus a third publish per action from extendRuntimeRedactionForResolvedAction for Basic-auth/import-preserving scripts (resolved_imported_credential_redaction.go:30). Each publish calls withSecretValueRedaction(nil, values) which re-expands every value into up to 64 encoded variants (runtimeSecretRedactionVariants, redaction.go:481-567: multiple base64/URL/JSON/HTML/hex encodings), re-sorts, and builds a fresh strings.Replacer whose trie is rebuilt lazily on the next scrub. The group-level matcher cache (runtimeSecretValueSetKey/attachRuntimeSecretRedaction, group.go:280-296) exists but is never applied to these per-VUser publishes.

**Failure scenario:** A typical LR-imported web script with 3 session cookies at 1,000 VUsers x 10 req/s: ~20,000 publishes/sec, each doing ~192 variant encodings plus Replacer construction and trie build (~100-300us CPU) => several cores of pure redaction-bookkeeping overhead, reducing achievable load per generator pod. Worst case: a SUT rotating a CSRF/session token per response fills the 512-entry session history (imported_credential_runtime.go:18); every stage/commit then expands ~32,000 variants and builds a multi-MB trie per action, collapsing per-VUser throughput to a handful of requests/second and pinning generator CPU -- reported test results then measure the generator, not the SUT.

**Suggested fix:** In replaceRuntimeCredentialScopes, compare the normalized per-scope value slice against the current one and return early when unchanged (covers the stable-cookie steady state); additionally memoize compiled overlays keyed by runtimeSecretValueSetKey(values) per VUser so stage/commit of the same set reuses the Replacer.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # E02 — Full runtime-secret matcher recompiled 2-3x per action per VUser — CONFIRMED (high)

Every hop verified in snapshot (HEAD dc05008ab):

1. No change detection: `replaceRuntimeCredentialScopes` (pkg/engine/imported_credential_runtime.go:136-165) never compares `prospective` to the existing scope map; line 160 always calls `publishRuntimeCredentialRedaction`, which calls `withRuntimeSecretValueOverlay` (line 193) → `withSecretValueRedaction(nil, values)` (redaction.go:366). With config==nil, `matcherReady` is false (redaction.go:234-235) so any non-empty value set fully recompiles: `runtimeSecretRedactionVariants` expands up to 64 encodings per value (redaction.go:481-567) and a fresh `strings.NewReplacer` is built (redaction.go:344), trie rebuilt lazily on the next scrub.
2. Frequency: HTTP `Execute` sets `RuntimeCredentialValuesSnapshot=true` on every result when a boundedCookieJar exists (pkg/protocol/http/http.go:219-227) → `stageRuntimeCredentialSessionSnapshot` per consumed result (vuser_execute.go:73-86); `commitRuntimeCredentialSessionSnapshot` fires at every next `resolveAction` once staged (vuser_resolve.go:198). Third publish: `extendRuntimeRedactionForResolvedAction` runs on every executeAction (vuser_execute.go:687) and calls `replaceRuntimeCredentialScope` unconditionally (resolved_imported_credential_redaction.go:30) — even with empty action values it republishes, recompiling the non-empty session-cookie set, so 3 publishes/action for ANY cookie-bearing script (finding understated this).
3. The set-key cache (group.go:280-296) runs only at engine construction; per-VUser publishes have no memoization. Empty-value scripts short-circuit cheaply (redaction.go:363), so the cost is correctly scoped to cookie/credential-bearing scripts. Worst case bounded only by 32MB pattern / 101k value limits (redaction.go:42-44), so 512-history rotation makes per-action compiles MB-scale. Structural chain fully established; per-publish µs figures are estimates but the multi-core steady-state overhead on a generator hot path follows. High stands for a load-generation product.

</details>


#### 11. Runtime secret matcher admits arbitrarily short/common values (all cookie values, auth us...
**Where:** `pkg/engine/redaction.go:297` · **Area:** AREA-16 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

compileRuntimeSecretReplacement/addVariant (redaction.go:297-345) admits any non-empty string as an exact-match redaction value with no minimum length or sensitivity filter, and the sources feeding it over-collect: the HTTP adapter snapshots EVERY cookie value in the jar on every Execute (pkg/protocol/http/http.go:222-226, cookie_jar.go:126-144, no name filter) into stageRuntimeCredentialSessionSnapshot (vuser_execute.go:73-86, imported_credential_runtime.go:239-256), and resolvedActionCredentialValues registers Basic-auth usernames for ALL scripts (resolved_imported_credential_redaction.go:54-57, 319-334). scrubRuntimeSecretText then substring-replaces these values in every retained field, including ActionName/Group/DependencyTarget/ErrorMessage via scrubSensitiveResultMetadata (redaction.go:691-725), which runs before Metrics.Record (vuser_execute.go:48-61), so metrics dimension keys themselves are rewritten.

**Failure scenario:** SUT sets an ordinary short cookie, e.g. Set-Cookie: locale=en or cookieconsent=true. From the next action on, the strings "en" / "true" are runtime secrets: action name "open_home" is recorded as "op[REDACTED]_home" in ByAction/ByTransaction metrics, response-body evidence '"success":true' becomes '"success":[REDACTED]', and every URL/error message containing the substring is rewritten -- silently wrong labels and destroyed diagnosis evidence for the entire run. Same mechanism with Basic-auth username "admin": every occurrence of "admin" (paths like /admin/, action names) in retained evidence is masked. If a group name contains such a substring, RecordTransactionResult attributes totals under the scrubbed name while MultiGroupRunResult reads GroupRunTotals under the configured name (group.go:675), so the terminal group result reports zero transactions.

**Suggested fix:** Impose a minimum secret length (e.g. >= 8 bytes) for exact-match runtime values and their variants in compileRuntimeSecretReplacement; filter cookie snapshot collection to sensitive-named cookies (or values above the length floor); stop registering bare usernames (keep user:password composites and encoded Basic payloads).

<details><summary>Verification evidence</summary>

**confirmed @ high:** # E01 — CONFIRMED (high)

Full causal chain established at HEAD (snapshot dc05008ab), no refuting guard found:

1. Cookie jar is on by default (`cookiesEnabled := true`, pkg/protocol/http/config.go:598) and `runtimeCredentialValuesSnapshot` returns EVERY current cookie value with no name/length filter (pkg/protocol/http/cookie_jar.go:126-144). `HTTP.Execute` attaches it to every result via defer (http.go:219-227).
2. Engine consumes it unconditionally: recordResult → consumeRuntimeCredentialResultValues (pkg/engine/vuser_execute.go:27,73-86) → stageRuntimeCredentialSessionSnapshot → publishRuntimeCredentialRedaction (imported_credential_runtime.go:239-256,167-201); `uniqueSortedRuntimeCredentialValues` drops only "" (219-232).
3. Matcher admits any non-empty value: `addVariant` rejects only ""/"[REDACTED]" (redaction.go:296-318); raw values mandatory (322-326); `runtimeSecretPlaceholder` comment even anticipates "one-byte secret" (418-423). No minimum-length floor exists anywhere (grep across all three files).
4. Scrub is plain substring `strings.Replacer` (redaction.go:935-949, no word boundary) applied to ActionName/Group/DependencyTarget/ErrorMessage etc. (scrubSensitiveResultMetadata 691-725, called at 645) BEFORE Metrics.Record/RecordTransactionResult (vuser_execute.go:48-61); metrics key on scrubbed r.ActionName (metrics.go:616,651) while MultiGroupRunResult reads GroupRunTotals by the configured group name (group.go:676) — mismatch → zero totals sub-claim holds.
5. Basic-auth username registered unconditionally, hand-authored scripts included (resolved_imported_credential_redaction.go:54-56,319; collector.add has no floor, imported_credential_redaction.go:375-378).

So `Set-Cookie: locale=en` makes "en" a runtime secret: "open_home" → "op[REDACTED]_home" in metric dimensions and all retained evidence. Prior-report item (response body retention, PERF_REVIEW_2026-07-06) is a different concern — not a regression of it.

</details>


#### 12. SOAP fault detection silently disabled by default response-body discard
**Where:** `pkg/protocol/soap/soap.go:170` · **Area:** AREA-17 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

soap.Execute inspects result.ResponseBody for faults (soap.go:170 detectSOAPFault(result.ResponseBody)) after delegating to http.Execute, but http.go:747-749 clears ResponseBody on success whenever action.DiscardResponseBodyOnSuccess is set — and the engine sets that flag for every action unless log level is 'extended' WITH the 'data_returned' option (pkg/engine/vuser_resolve.go:221-223, pkg/engine/vuser_execute.go:128-137), i.e. in the default load-run configuration. A SOAP fault returned with HTTP 2xx (common for stacks that do not follow the 500 rule, and for doc/literal business faults) therefore reaches detectSOAPFault as an empty string: soap_fault_code/soap_fault_category extractions and ServerTelemetry fault fields are never populated, and soap_fault_fail_on_fault never fires, so the sample is reported as PASS.

**Failure scenario:** Script config: soap_fault_fail_on_fault=true, log config left at default (not extended/data_returned). Target service returns HTTP 200 with a soapenv:Fault body (e.g. business fault). http.Execute marks Success=true and discards the body; detectSOAPFault("") returns false; the run reports the transaction green and no soap_fault_code is extracted. Every fault of this shape in the whole run is silently counted as success — poisoned results.

**Suggested fix:** In soap.Execute, clear prepared.DiscardResponseBodyOnSuccess before delegating and re-apply the discard (result.ResponseBody = "") after fault detection/extractions when the original flag was set and the sample stayed successful.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # W01 — SOAP fault detection disabled by default body discard: CONFIRMED (high)

Every hop verified at HEAD (snapshot dc05008ab), no refuting guard found.

1. Wiring: soap registered via `protocol.Register("soap", ...)` (pkg/protocol/soap/soap.go:21-25); engine dispatches via `protocol.Get(script.Protocol)` (pkg/engine/engine.go:178,314) → `vu.Proto.Execute` (pkg/engine/vuser_execute.go:457).
2. Flag default: resolveAction sets `DiscardResponseBodyOnSuccess = !logConfigRetainsSuccessfulResponseBody(...)` unconditionally, no soap exception (pkg/engine/vuser_resolve.go:221-223, 259); retention requires level=extended AND option data_returned (pkg/engine/vuser_execute.go:128-137) — so default config discards.
3. Propagation: `prepared := *action` copies the flag; soap.Execute never clears it (pkg/protocol/soap/soap.go:93-131, full read).
4. Discard: `result.ResponseBody` set only when `!result.Success || !DiscardResponseBodyOnSuccess` (pkg/protocol/http/http.go:747-749). HTTP 200 + fault body → Success=true → body empty.
5. Detection: soap.go:170 `detectSOAPFault(result.ResponseBody)`; detectSOAPFault("") fails `looksLikeSOAPEnvelope` → false (soap.go:678-685). The >=500 fallback (soap.go:188) never fires for 2xx faults; 5xx faults are unaffected (Success already false retains body).
6. No soap test exercises DiscardResponseBodyOnSuccess (grep: only http_test.go hits).

Consequence follows: with failOnFault=true and default logging, 200-status soapenv:Fault samples report PASS, soap_fault_code/category and ServerTelemetry fault fields never populate. High: silent false-success in the default configuration.

</details>


#### 13. No-new-evidence ticks replay the last window's aggregates and transactions as fresh data
**Where:** `pkg/distributed/generatorrpc/service.go:2157` · **Area:** AREA-19 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

collectExecutionMetrics (service.go:2157-2167, plus the early path at 2120-2127) substitutes exec.lastMetrics.transactions and exec.lastMetrics.aggregates whenever a 1s drain produces no evidence (metricsAggregateSetHasEvidence false), keeping the previous WindowMs (2160-2162). The replayed set is published with a fresh TimestampUnixMs (service.go:1976), stored back into assignment.groupAggregates/groupTransactions (1642-1644), and recordMetricsBatch writes a NEW interval-summary row per tick with nonzero TPS = staleCount/window and TotalTx = stale count (service.go:1669-1681). Nothing marks the batch as replayed, and the archive gains one phantom row per second of idle.

**Failure scenario:** A script with 30 transactions completing in ~3s followed by a 60s think-time loop (or a run that is paused, or a SUT that hangs so completions stop entirely): every subsequent 1s tick republishes the last active window. The live dashboard shows flat, healthy TPS at the pre-stall rate while zero transactions complete — a hung SUT is indistinguishable from a healthy one — and the generator archive's summaries.jsonl accrues one phantom row per second (60 rows x 30 tx during one think loop) with nonzero TPS/TotalTx, so archived TPS-over-time graphs and any consumer summing TotalTx across interval rows report traffic that never happened.

**Suggested fix:** Restrict the lastMetrics substitution to the terminal snapshot (completeExecutionCallbacks path) or publish empty aggregates with a zero-count window on periodic no-evidence ticks; alternatively add a replayed/no-evidence flag to MetricsBatch and skip RecordIntervalSummary when set.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # S01 — No-new-evidence ticks replay last window as fresh data — CONFIRMED (high)

Chain verified at every hop in the snapshot (HEAD dc05008ab):

1. Publisher ticks every publishInterval=1s unconditionally (service.go:769, 2082-2089), per group execution.
2. Idle window: DrainWindow returns nil aggregate when no completions (service.go:581-584); finalize(nil) yields empty set (398-401); mergeSnapshotSideChannelAggregates adds only network-profile/browser channels (2387-2394), so a plain-HTTP idle tick fails metricsAggregateSetHasEvidence (2171-2173) and takes the replay branch: previous transactions/aggregates/WindowMs substituted (2157-2163). Early path 2120-2127 replays the whole snapshot when engine snapshot is also absent.
3. Replayed set stored into assignment.groupTransactions/groupAggregates (1642-1644); batch built with fresh TimestampUnixMs=time.Now() (1976) and FleetMetrics from the replayed aggregates (1841, 1988).
4. recordMetricsBatch writes a new interval-summary row per tick: TPS=staleSuccess/staleWindow, TotalTx=stale count (1669-1681, totals via archiveTotalsFromBatch 2766-2771); RecordIntervalSummary appends unconditionally, no dedup (archive.go:853-870).
5. No replay marker: grep of api/proto/generator.proto for replay/stale/no_evidence — nothing; no tests reference lastMetrics or the evidence check.

Refutations hunted: no guard, flag, dedup, or pinning test found. Prior-report matches (heartbeat pong, RuntimeCapabilityProvider) are different symbols — not a regression of those. Consequence follows: hung/paused/think-time runs show sustained phantom TPS live and accrue one phantom archive row per second.

</details>


#### 14. Projects with direct-import receipts cannot be deleted: direct_import_receipts missing fr...
**Where:** `pkg/storage/sqlite_projects.go:1361` · **Area:** AREA-20 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

deleteProjectGraphTx (pkg/storage/sqlite_projects.go:1223-1374) enumerates ~40 child tables but never deletes direct_import_receipts, whose project_id column is declared NOT NULL REFERENCES projects(id) (pkg/storage/sqlite_schema.go:409). Completed receipts are retained forever as the idempotency anchor (CompleteDirectImportReceipt at pkg/storage/direct_import_receipts.go:179-224 flips state to 'complete'; the only delete path is AbandonDirectImportReceipt at :225-239, which only removes pending rows owned by the abandoning token). With foreign keys enforced on both dialects (pkg/storage/sqlite.go:167; postgres_foreign_keys.go derives the same edge), the final DELETE FROM projects WHERE id = ? at sqlite_projects.go:1361 violates the FK and rolls back the entire project deletion.

**Failure scenario:** Import a JMeter plan (pkg/api/handlers_import_jmeter.go uses the direct-import receipt flow) or save a browser-recorder capture (pkg/api/handlers_browser_recorder.go:1240+) into project P; the commit leaves a state='complete' direct_import_receipts row referencing P. Later call DELETE /projects/{P} (or the lease-held variant): DeleteProject/DeleteProjectIfLeaseHeld runs deleteProjectGraphTx, DELETE FROM projects fails with 'FOREIGN KEY constraint failed', the transaction (including all the child-table deletions) rolls back, and the API returns a 500. The project is permanently undeletable.

**Suggested fix:** Add {`DELETE FROM direct_import_receipts WHERE project_id = ?`, []any{id}} to the deletes slice in deleteProjectGraphTx (anywhere before the final DELETE FROM projects; the imports section around line 1335 fits). Also worth a follow-up retention policy for completed receipts, which currently grow without bound.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # S08 — direct_import_receipts missing from deleteProjectGraphTx — CONFIRMED

Schema: pkg/storage/sqlite_schema.go:407-421 declares `direct_import_receipts.project_id TEXT NOT NULL REFERENCES projects(id)` with no ON DELETE clause. FK enforcement is on for SQLite (pkg/storage/sqlite.go:167 `_pragma=foreign_keys(ON)`), and postgres_foreign_keys.go:29,183 derives the same constraint (NO ACTION default) for Postgres.

Delete graph: deleteProjectGraphTx (pkg/storage/sqlite_projects.go:1223-1374) enumerates ~40 child tables ending with `DELETE FROM projects WHERE id = ?` (line 1361); `direct_import_receipts` appears nowhere in the file. The only DELETE on that table anywhere is AbandonDirectImportReceipt (pkg/storage/direct_import_receipts.go:231-234), scoped to `state='pending' AND owner_token=?`; CompleteDirectImportReceipt (:212) is an UPDATE that retains the row as state='complete'. No sweeper/retention path exists (repo-wide grep).

Trigger is production-real: pkg/api/direct_import_idempotency.go:99/:308 claims+completes receipts, invoked by handlers_import_jmeter.go (:462,:550) and handlers_browser_recorder.go (:1240+); SQLiteStore implements DirectImportReceiptStore, so the seam is active. deleteProjectGraphTx is reached from DeleteProject (:1180) and DeleteProjectIfLeaseHeld (:1214). With an immediate (non-deferred) FK, the final DELETE FROM projects fails, rolling back the tx — the project becomes undeletable. Severity high stands.

</details>


#### 15. Schedule-launched tests cannot be deleted: deleteTestGraphTx trips tests(id) foreign keys
**Where:** `pkg/storage/sqlite_tests.go:1390` · **Area:** AREA-20 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

deleteTestGraphTx (pkg/storage/sqlite_tests.go:1323-1394) deletes the tests row without clearing scenario_schedule_occurrences.test_id or scenario_schedules.last_run_test_id, both declared as FOREIGN KEY ... REFERENCES tests(id) (pkg/storage/sqlite_schema.go:151 and :130). The scheduler writes both pointers when it materializes an occurrence (pkg/storage/sqlite_schedules.go:668) and promotes it (pkg/storage/sqlite_scenario_occurrence_lifecycle.go:106-124 sets last_run_test_id and keeps occurrence.test_id in every lifecycle branch; nothing ever nulls them). Foreign keys are enforced on SQLite via _pragma=foreign_keys(ON) (pkg/storage/sqlite.go:167) and on Postgres via the FK graph derived from the same schema (pkg/storage/postgres_foreign_keys.go:33-49), so DELETE FROM tests fails and the whole delete transaction rolls back. Project deletion knows about both references and handles them (sqlite_projects.go:1305-1306), proving the per-test path was simply never updated.

**Failure scenario:** Create a scenario schedule, let it launch one run, wait for the run to finish, then delete that run via DELETE /tests/{id} (pkg/api/handlers.go:522 -> runner.DeleteTestWithError -> store.DeleteTest -> deleteTestGraphTx). The final DELETE FROM tests violates the scenario_schedule_occurrences.test_id (and scenario_schedules.last_run_test_id) foreign keys, the transaction rolls back, and the API returns 500 'failed to delete test: FOREIGN KEY constraint failed'. Every test ever launched by a schedule is permanently undeletable until its schedule (and occurrence history) is destroyed first.

**Suggested fix:** In deleteTestGraphTx, before DELETE FROM tests: UPDATE scenario_schedules SET last_run_test_id = NULL WHERE last_run_test_id = ?; and UPDATE scenario_schedule_occurrences SET test_id = NULL WHERE test_id = ? (or DELETE the occurrence rows if calendar history for deleted runs is unwanted — project deletion deletes them). Mirror the exact statements already used in deleteProjectGraphTx.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # S07 — CONFIRMED (high)

FK edges are real: sqlite_schema.go:130 `FOREIGN KEY(last_run_test_id) REFERENCES tests(id)` (scenario_schedules) and :151 `FOREIGN KEY(test_id) REFERENCES tests(id)` (scenario_schedule_occurrences). Enforcement is on for SQLite via `_pragma=foreign_keys(ON)` (sqlite.go:167, sqliteDSN).

Pointers are written and never cleared per-test: occurrence rows insert test_id (sqlite_schedules.go:668-673); on promotion the "started" branch sets `scenario_schedules.last_run_test_id = testID` (sqlite_scenario_occurrence_lifecycle.go:106-109) and every lifecycle branch keeps occurrence.test_id (WHERE test_id = ?, never SET NULL, :120-186). Grep confirms the only NULL-ing of last_run_test_id / occurrence deletion by test is in deleteProjectGraphTx (sqlite_projects.go:1305-1306) — proving the edges are known but omitted from the per-test path.

deleteTestGraphTx (sqlite_tests.go:1323-1394) deletes 9 child tables then `DELETE FROM tests` at :1390 with no schedule/occurrence handling; both callers (DeleteTest :1233, DeleteTestIfLeaseHeld :1288) roll back on error. Reachable in production: DELETE /tests/{id} → handlers.go:547 → DeleteTestWithError (api/tests.go:1145) → deleteStoredTest (api/persistence.go:1545/1554) → deleteTestGraphTx; FK violation → 500.

Prior-report check: the 2026-07-16 fix to deleteTestGraphTx concerns github_check_links/intents (guard + deletes present at :1325-1380) — a different FK edge, not a regression of that fix.

</details>


#### 16. formSecretPattern applied to JSON bodies deletes everything after a matched secret value
**Where:** `pkg/convert/paramize/secrets.go:19` · **Area:** AREA-22 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

formSecretPattern (secrets.go:19) uses value class [^&\s]+ which includes quotes/braces, and unlike the data-param scanner (dataparams.go:117 branches on JSON prefix) it is applied unconditionally to every body by both SanitizeBodySecrets (secrets.go:49-56) and collectBodySecrets (secrets.go:141-159). When a JSON string value embeds '&<secretfield>=' (e.g. a callback URL '"redirect_url":"https://app/cb?code=1&token=xyz"'), the match's value group greedily consumes the rest of a compact JSON body up to the next '&' or whitespace, and the replacement (match[1]+template, dropping match[3]) deletes that tail — destroying the JSON structure and unrelated sibling fields. The analogous pkg/scrub pattern was hardened with a [^&\s;#"'] value class in July (scrub.go:46, known-fixed 'JSON-body scrub pattern'); this sibling in paramize was not.

**Failure scenario:** Sample importer with sanitization enabled ingests a recording whose JSON body contains an OAuth-style URL '...&token=...' inside a string field. SanitizeBodySecrets truncates the body mid-string: valid JSON becomes an unterminated fragment, non-secret fields after the token are silently deleted, and the stored/previewed script body no longer parses or replays. The same corruption occurs if a user accepts the secret suggestion produced from the same match in the review UI.

**Suggested fix:** Mirror the two scrub hardenings: (1) exclude quote/structural bytes from the value class ([^&\s"'<>]+ or the scrub.go:46 class), and (2) only run formSecretPattern when the trimmed body does not start with '{' or '[', matching collectBodyParams' branch in dataparams.go:117.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # T01 — formSecretPattern JSON-body tail deletion — CONFIRMED (high)

Regex at pkg/convert/paramize/secrets.go:19: `(?i)((?:^|[&\s])(...|token|...)=)([^&\s]+)` — value class stops only at `&`/whitespace, so in a compact JSON body `{"redirect_url":"https://app/cb?code=1&token=xyz9","state":"keep"}` the `&token=` inside the string matches and group 3 greedily consumes `xyz9","state":"keep"}` to end-of-body. SanitizeBodySecrets (secrets.go:49-55) replaces the whole match with `match[1]+"{{secret.redacted}}"`, deleting the tail; no JSON-format branch exists (contrast dataparams' JSON prefix branch, and pkg/scrub/scrub.go:46 whose hardened class `[^&\s;#"']+` was never mirrored). isWholeSecretTemplate cannot rescue (tail is not a template).

Reachability: only production callers are pkg/convert/sample/correlate_redact.go:641 (catch-all over entire preserved source-review file content), :729 (addBody — fed request bodies at :666 and response bodies at :682), :764. converter.go:239 invokes redactSourceReviewSecrets unconditionally on every sample import — no flag guards it. Suggestion path: collectBodySecrets (secrets.go:145-158) emits the over-greedy match[0] as SearchText with tail-dropping ReplaceText, so accepting the secret suggestion corrupts the executable body via ApplySuggestions.

Nuance: automatic corruption hits preserved source-review files; executable-body corruption needs user acceptance of the suggestion. Silent destruction of stored import artifacts on a default-on path — high stands.

</details>


#### 17. Helm import-only Bruno mode writes encrypted artifacts to emptyDir while mounting the req...
**Where:** `deployments/helm/apexload/templates/controller.yaml:196` · **Area:** AREA-23 · **Category:** helm-kustomize-parity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

With bruno.importEnabled=true and bruno.enabled=false, controller.yaml:196 sets APEXLOAD_BRUNO_ARTIFACT_ROOT via `ternary .Values.bruno.artifactRoot "/var/lib/apexload/bruno-artifacts" .Values.bruno.enabled`, which resolves to /var/lib/apexload/bruno-artifacts — inside the `data` emptyDir mounted at /var/lib/apexload (controller.yaml:230-231, 263-265) — while the chart simultaneously requires bruno.artifactPersistence.existingClaim (controller.yaml:272) and mounts that PVC at .Values.bruno.artifactRoot=/var/lib/apexload-bruno (controller.yaml:236-237, values.yaml:139), a path the controller then never uses. pkg/api/handlers_bruno_import.go:2949-2965 uses this env as the root of the durable encrypted content-addressed artifact store, and values.yaml:134-135 states import requires protected artifact retention.

**Failure scenario:** Operator enables Bruno import only (importEnabled=true, enabled=false), supplies the required RWX claim, imports collections for weeks; a controller pod reschedule/upgrade discards the emptyDir, so every imported Bruno script's encrypted source artifact is gone — export, inspect, differential-proof, and later execution enablement against those scripts fail permanently while the paid-for PVC sits empty.

**Suggested fix:** Make the env value unconditionally {{ .Values.bruno.artifactRoot }} so import-only mode uses the mounted PVC, or (if import-only is meant to be PVC-free) drop the mount/required-claim for importEnabled-only and document the retention downgrade.

<details><summary>Verification evidence</summary>

**confirmed @ high:** # C01 — Confirmed (high)

Every hop verified at HEAD in the snapshot. controller.yaml:194-197 sets APEXLOAD_BRUNO_ARTIFACT_ROOT via `ternary .Values.bruno.artifactRoot "/var/lib/apexload/bruno-artifacts" .Values.bruno.enabled`; Sprig ternary is (trueVal, falseVal, cond), so importEnabled=true + enabled=false yields /var/lib/apexload/bruno-artifacts. That path sits under the `data` volume mounted at /var/lib/apexload (controller.yaml:230-231), which is an emptyDir (controller.yaml:263-265). Meanwhile the same importEnabled condition requires bruno.artifactPersistence.existingClaim (controller.yaml:269-272) and mounts that PVC at bruno.artifactRoot, whose default is /var/lib/apexload-bruno (values.yaml:139) — a path the app never reads in this mode. Consumption chain: cmd/controller/main.go:393-395 reads the env → main.go:139 BrunoArtifactDir → pkg/api/server.go:458 brunoArtifactDir → handlers_bruno_import.go:2949-2965 as RootDir of the encrypted content-addressed artifact store (resolver registered at 2970-2972 for later inspect/export). Import-only mode is real: configmap.yaml:63 sets APEXLOAD_BRUNO_IMPORT_ENABLED from importEnabled, gating handleBrunoImport (handlers_bruno_import.go:228). values.yaml:134-135 states import requires protected artifact retention. Pod reschedule discards the emptyDir → encrypted artifacts unrecoverable while the required PVC sits empty. No refuting guard (no subPath, no alternate persistence of artifact content) found.

</details>


## Medium (70)

#### 18. Launch-window admission performs many storage queries while holding tr.mu
**Where:** `pkg/api/launch_preflight.go:564` · **Area:** AREA-01 · **Category:** performance-lock-contention · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

launchWindowConflictsLocked runs with tr.mu held (write lock on the StartTest/promotion/resume/scale path via evaluateStartGatesLocked at pkg/api/testexecution.go:578-589, read lock in PreflightLaunch at pkg/api/launch_preflight.go:102-168) and performs storage I/O inside the lock: launchCapacityEventsLocked calls tr.scenarioStore.ListAllScenarioScheduleOccurrences() (full-table read, launch_preflight.go:608), boundTimeslotBoundariesInWindowLocked does a range query (pkg/api/timeslot_reservation.go:739-756), and the per-sample loop calls launchReservationConflictAtLocked -> boundTimeslotsActiveAtLocked -> ListCapacityReservationsByTimeRange once per sample instant (launch_preflight.go:572-585, timeslot_reservation.go:712-731); sample count grows with overlapping calendar events (launchSampleTimes, launch_preflight.go:743-781). launchTimeslotRevalidationErrorLocked (timeslot_reservation.go:552-583) and ValidateScenarioLoadModel's data-pool reads under RLock (pkg/api/load_model_validation.go:36-78, 338-352) are further instances. This is the repo's documented 'locks held across DB I/O' defect class.

**Failure scenario:** Enterprise deployment on PostgreSQL (2-5 ms per query RTT) with ~15 running/scheduled runs and a few capacity reservations: a StartTest evaluates ~2+2x15 sample instants, i.e. ~34 sequential DB queries plus the occurrence table scan, holding tr.mu (write) for 100-200 ms. Every gradual-ramp step and every ResumeTest/AdjustDuration repeats the same sweep (vuserScaleProposalLocked -> runtimeAdjustmentWindowErrorLocked -> launchWindowConflictsLocked, pkg/api/testexecution.go:2065-2078, 2174-2195). During each sweep, all API reads (GetTest/ListTests use RLock) and all metric-publish locking stall; with two concurrent ramps the dashboard visibly freezes in bursts for the whole ramp duration. A slow/failing PG node stretches each hold to seconds.

**Suggested fix:** Snapshot the needed durable inputs (occurrences, reservations in window) once per admission attempt BEFORE taking tr.mu, pass them into the locked evaluation as values; keep only the in-memory event assembly under the lock. Cache ListCapacityReservationsByTimeRange for the whole window and filter per-sample in memory instead of re-querying per sample.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V001 — CONFIRMED (medium)

launchWindowConflictsLocked (pkg/api/launch_preflight.go:545-587) runs under tr.mu: write lock via beginScriptShakeoutIfNeeded (pkg/api/testexecution.go:647-648) -> preflightStartBlocksLockedWithExpectedTerm:639 -> evaluateStartGatesLocked:579 -> launchStartWindowErrorLocked (launch_preflight.go:1103-1113); read lock in PreflightLaunch (launch_preflight.go:102-168). Inside the lock: launchCapacityEventsLocked calls scenarioStore.ListAllScenarioScheduleOccurrences() (launch_preflight.go:608); boundTimeslotBoundariesInWindowLocked issues a range query (timeslot_reservation.go:743); the per-sample loop (launch_preflight.go:572-574) calls launchCapacityConflictsAtLocked:837 -> launchReservationConflictAtLocked:844 -> boundTimeslotsActiveAtLocked -> ListCapacityReservationsByTimeRange (timeslot_reservation.go:718) once per sample; sample count grows with events/reservations (launchSampleTimes:743-781). Resume/adjust/ramp repeat via vuserScaleProposalLocked:2076 -> runtimeAdjustmentWindowErrorLocked:2190. Causal chain holds; severity medium appropriate.

</details>


#### 19. Local goal controller uses a stale groupVUsers map, misreporting current VUsers and silent
**Where:** `pkg/api/metrics.go:796` · **Area:** AREA-01 · **Category:** correctness-state-divergence · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

broadcastMetrics captures groupVUsers once when the run enters RUNNING (pkg/api/metrics.go:796-801) and thereafter the map is updated ONLY by the goal controller's own successful scales (pkg/api/testexecution.go:3062). An operator ScaleVUsers call updates test.Groups and the engine but never this map, so evaluateGoalControllerLocked computes currentTotalVUsers from stale values (metrics.go:891) and applyGoalControllerUpdateWithScaler both (a) plans absolute targets from the stale distribution and skips groups whose stale value equals the computed target (testexecution.go:3036-3037), and (b) writes the stale values back into test.Groups[i].VUsers for every group in the map after any scale round (testexecution.go:3072-3076), making the durable record diverge from the engine's actual VUser count. The distributed path already rebuilds a fresh map via liveGroupVUsers (pkg/api/distributed_stream.go:1050, helper at pkg/api/goal_controller.go:405-420); the local ticker does not.

**Failure scenario:** Local goal_oriented run with groups A(50), B(50), C(50). Operator scales C to 80 via POST /tests/{id}/scale (permitted while RUNNING; no scenario-mode gate in vuserScaleProposalLocked). Engine now runs 180 VUsers; test.Groups says 80. On the next TPS shortfall the controller computes currentTotal=150 from the stale map, distributes new targets; C's target comes out equal to its stale 50 so the loop skips scaling C, then the write-back loop sets test.Groups[C].VUsers=50 while the engine still runs 80. Durable record, TotalVUsers-based capacity accounting, and a restart-rehydrated copy now disagree with the actual fleet by 30 VUsers, and GoalStatus.CurrentTotalVUsers shows 150 while 180 run. If C's target differs from 50, the controller instead silently reverts the operator's 80 back toward the stale-based distribution.

**Suggested fix:** In broadcastMetrics, rebuild the map each tick with liveGroupVUsers(test, live) (as the distributed consumer does) before evaluateGoalControllerLocked, and drop the unconditional write-back loop in applyGoalControllerUpdateWithScaler for groups not scaled in this round (or read current values from test.Groups under the lock instead of the closure map).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V003 — CONFIRMED (medium)

broadcastMetrics captures groupVUsers once from test.Groups at run start (pkg/api/metrics.go:796-801) and the ticker loop (814-839) never rebuilds it. Only controller scales update it (pkg/api/testexecution.go:3062). Operator scale is permitted on goal runs — vuserScaleProposalLocked (testexecution.go:2011-2079) has no ScenarioMode gate — and writes test.Groups[groupIndex] durably (reserveVUserScaleLocked:2123-2130) without touching the closure map. Next goal round: evaluateGoalControllerLocked computes currentTotalVUsers from the stale map (metrics.go:891); applyGoalControllerUpdateWithScaler skips groups whose stale value equals target (testexecution.go:3037) and writes stale values back into test.Groups for every mapped group (3072-3076), reverting the operator's value; GoalStatus totals also stale (3088). liveGroupVUsers exists (goal_controller.go:405-420) and is used only by distributed_stream.go:1050. Confirmed medium.

</details>


#### 20. Boot hydrates full interval history for every stored test into memory with no aggregate bo
**Where:** `pkg/api/persistence.go:424` · **Area:** AREA-01 · **Category:** performance-memory · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

loadStoredTests iterates every durable test row (ListTests("", nil), pkg/api/persistence.go:402-428) and calls loadStoredTestHistory for each, which loads up to maxManagedTestHistory=3600 decoded LiveMetrics points capped at maxManagedTestHistoryBytes=64 MiB PER TEST (persistence.go:1003-1038, pkg/api/runner.go:1045-1046) and keeps them resident on the ManagedTest for the process lifetime. There is no global byte budget and no lazy loading, so controller RSS and boot time grow linearly with total retained run history across all runs (terminal runs included). The HA reload path repeats this for every newly-seen identity (persistence.go:504-511).

**Failure scenario:** An enterprise controller retains 300 completed runs averaging 1800 history points of ~5 KB each (rich Transactions/GroupMetrics maps): boot decodes and pins ~2.7 GB of history JSON-derived structures (plus Go overhead) before serving traffic, taking minutes of N+1 GetHistoryLimit queries; on a memory-limited pod (e.g. 2 GiB K8s limit) the controller OOM-kills in a crash loop after enough runs accumulate, with no configuration to bound it short of deleting runs.

**Suggested fix:** Hydrate history lazily on first read for terminal runs (keep only the latest point for LiveMetrics display at boot), or add a global history byte budget that evicts oldest terminal-run histories LRU-style; the archive already holds the durable copy.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V005 — CONFIRMED (medium)

loadStoredTests (pkg/api/persistence.go:402-428) iterates every row from ListTests("", nil) and calls tr.loadStoredTestHistory(managedTest) at line 424 for every stored test regardless of status; history stays resident on the ManagedTest in tr.tests (line 426) for process lifetime. loadStoredTestHistory (persistence.go:1003-1038) loads up to maxManagedTestHistory=3600 points per test (one GetHistoryLimit query per test — N+1), bounded only per-test by maxManagedTestHistoryBytes=64 MiB (pkg/api/runner.go:1045-1046; trimming in appendManagedTestHistoryWithLimits/trimManagedTestHistoryToLimits, pkg/api/metrics.go:546-594 — per-test accounting only). Grep found no global byte budget, no lazy load, and no retention pruning of stored tests. HA reload repeats hydration for new identities (persistence.go:504-505). Aggregate memory grows linearly with retained runs; medium stands.

</details>


#### 21. Gradual ramps persist a full test row (incl. every compiled script) per single-VUser step 
**Where:** `pkg/api/testexecution.go:2130` · **Area:** AREA-01 · **Category:** performance-persistence · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

runVUserScaleRamp issues scaleVUsersNow once per single-VUser step (pkg/api/testexecution.go:1656-1677: steps = |delta|, nextTarget = current + direction*step). Each step takes tr.mu.Lock (testexecution.go:1866) and inside the lock reserveVUserScaleLocked calls tr.saveManagedTestRecord (testexecution.go:2130), which serializes the ENTIRE test row: buildManagedTestRecord -> encodeStoredGroups (pkg/api/persistence.go:1416, pkg/api/stored_decode.go:25-41) clones every group's full compiled RuntimeScript AND BaseScript via a JSON marshal+unmarshal round trip (stored_decode.go:43-56), and the storage layer marshals the groups again on write. Each step additionally repeats the full admission sweep (quota, timeslot revalidation DB read, launch-window sweep -- vuserScaleProposalLocked, testexecution.go:2059-2078). The same per-step cost applies to stage-schedule ramps and goal-controller convergence, since both route through scaleVUsersNow.

**Failure scenario:** A LoadRunner-imported script compiles to ~3 MB of JSON (large imports are a known real case in this repo) and the test has 2 groups. A ramp_up stage from 100 to 600 VUsers over 5 minutes produces 500 steps; each step performs roughly 3 full serializations x 2 scripts (~18 MB of JSON work) plus a 6-12 MB durable row write, all while holding tr.mu exclusively. Every ~600 ms the write lock is held for tens-to-hundreds of milliseconds, dashboard reads stall in bursts for the entire ramp, and SQLite writes ~3-6 GB of WAL churn for one ramp. With PostgreSQL the durable write adds network RTT to every step and the ramp falls behind its schedule.

**Suggested fix:** Persist ramp progress coarsely: reserve the ramp's final target durably once at ramp start (capacity-safe because reservation is the max), and update currentVUsers in memory per step, persisting at completion/cancel. Alternatively add a narrow UpdateTestGroupVUsers persistence op that writes only the vusers columns, skipping encodeStoredGroups; move the durable write outside tr.mu using the existing fence token.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V002 — CONFIRMED (medium)

Chain verified statically in snapshot (HEAD dc05008ab):

1. Per-step loop: pkg/api/testexecution.go:1642-1678 — `steps := AbsInt(delta)`, one `tr.scaleVUsersNow(id, group, nextTarget, false)` per single-VUser step. Reachable in production: `ScaleVUsersWithOptions` with RampDuration>0 (testexecution.go:1416-1424) → `startVUserScaleRamp` → goroutine at :1630; stage-schedule ramps at :1807; goal controller per-update at :3051.
2. Lock span: `scaleVUsersNowWithAdmissionGuard` takes `tr.mu.Lock()` at testexecution.go:1866; `reserveVUserScaleLocked` (:2096) runs inside it and calls `tr.saveManagedTestRecord(test)` at :2130 — durable DB write under the exclusive global mutex; unlock at :1887.
3. Full-row serialization per step: persistence.go:1422 `saveManagedTestRecord` → `buildManagedTestRecord` (:1373) → `encodeStoredGroups` (stored_decode.go:25-41) JSON-round-trips every group's full RuntimeScript AND BaseScript (`cloneScriptForStoredGroup`, :43-56); storage marshals groups_json again (pkg/storage/sqlite_tests.go:245). ~3 marshals of each script per step; `SaveTestIfLeaseHeld` fencing path writes the same full row.
4. Per-step admission repeat: `vuserScaleProposalLocked` (testexecution.go:2051-2080) reruns quota, `launchTimeslotRevalidationErrorLocked` (timeslot_reservation.go:552 — `GetCapacityReservation` DB read when a timeslot is bound), and `runtimeAdjustmentWindowErrorLocked` conflict sweep (testexecution.go:2174-2195).

Refuting guards hunted, none found: no coalescing/dirty-flag/async persistence; no narrow vusers-only update op. Prior item W17 (WEEK_REVIEW:215) is a PauseTest LiveMetrics data race — unrelated; not a regression. Consequence (O(steps) full-row writes + lock-held serialization, ramp elongation, WAL churn proportional to script size) follows; correctness is preserved, so medium stands.

</details>


#### 22. Usage accounting bills configured duration for every retained test, including runs that st
**Where:** `pkg/api/usage_accounting.go:245` · **Area:** AREA-01 · **Category:** data-integrity-accounting · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

usageAccountingFromTests computes vuser_seconds as TotalVUsers x usageDurationSeconds(test.Duration) (pkg/api/usage_accounting.go:244-249), where test.Duration is the CONFIGURED duration, not actual execution time (actual is derivable from StartedAt/EndedAt/CumulativePause but unused, usage_accounting.go:440-446). The input set is listOperationalTestSnapshots (pkg/api/tests.go:512-528), which returns every retained test in every status, so QUEUED/SCHEDULED/PENDING runs and runs FAILED before start contribute full configured usage to Totals/ByProject/ByUser. These figures are exported as CSV, persisted as durable snapshots on every terminal run and on the cadence timer (pkg/api/persistence.go:1660-1705), and served by /usage endpoints -- i.e., permanently recorded wrong accounting data.

**Failure scenario:** A project schedules an 8-hour soak at 1000 VUsers; the operator aborts it after 10 minutes. Usage reports and every subsequently persisted snapshot attribute 8h x 1000 = 28.8M vuser_seconds to the project (~48x the true ~0.6M). Meanwhile five QUEUED tests awaiting capacity add another 5 x 28.8M vuser_seconds to the same totals despite zero execution. A chargeback/capacity report built from the CSV or the stored snapshots over-bills the project by orders of magnitude, and because snapshots are durable the error cannot be recomputed away later.

**Suggested fix:** For terminal runs use actual active time: EndedAt.Sub(StartedAt) - CumulativePause (fall back to configured only when StartedAt is nil); exclude tests that never reached RUNNING/INITIALIZING from vuser_seconds (keep them visible via StatusCounts), or add a separate reserved_vuser_seconds bucket.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V004 — CONFIRMED (medium)

Chain established at every hop (snapshot = HEAD dc05008ab):

1. Tests enter tr.tests at creation with status QUEUED/RESERVED/SCHEDULED and configured TotalVUsers/Duration populated (pkg/api/testcreation.go:146-157, 551-553).
2. listOperationalTestSnapshots returns every test with NO status filter (pkg/api/tests.go:512-528).
3. usageAccountingFromTests computes vuserSeconds = TotalVUsers * usageDurationSeconds(test.Duration) unconditionally (pkg/api/usage_accounting.go:244-245); status feeds only StatusCounts (:249, :432-438). usageDurationSeconds uses only the configured time.Duration (:440-446). ManagedTest carries StartedAt/EndedAt/CumulativePause (pkg/api/runner.go:333+) but usage math never reads them; grep shows test.Duration is reassigned only by the AdjustDuration API paths (testexecution.go:2336-2529), never reset to actual elapsed on early stop.
4. Durably persisted: captureUsageSnapshotForTerminalRun (persistence.go:1660) called at testexecution.go:3334; saveUsageAccountingSnapshotFromTests writes JSON snapshot to usageStore (persistence.go:1687-1706); also cadence timer. Served via GET /api/v1/system/usage, /usage/export, /usage/snapshots (route_catalog_characterization_test.go:451-453).
5. handlers_usage_test.go:116-141 pins that a FAILED 30s-configured test contributes 150 vuser-seconds (5 VU x 30s) — confirming failed runs bill configured duration.

Refutation hunts (all negative): no status gate, no Duration rewrite on stop, no doc defining vuser_seconds as reserved capacity. Prior W41 (usageArchiveBytesCache staleness) is a different, still-fixed defect (usage_accounting.go:453-463) — not a regression.

Severity stays medium: durable wrong accounting figures (up to ~48x over-attribution per the scenario), but reporting/chargeback data, not security or direct monetary billing.

</details>


#### 23. Fair-share pending sort issues a reservation SQL query per sort comparison under tr.mu on 
**Where:** `pkg/api/capacity.go:556` · **Area:** AREA-02 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

sortPendingTestsForFairShareLocked (pkg/api/capacity.go:556-577) recomputes pendingFairShareRankLocked for BOTH elements on every sort.Slice comparison; each rank calls boundTimeslotsActiveAtLocked (pkg/api/capacity.go:589) which executes reservationStore.ListCapacityReservationsByTimeRange — a real SQL query (pkg/api/timeslot_reservation.go:712-731) — plus three O(len(tr.tests)) scans (capacity.go:596-597, 604-617). The sort runs inside refreshPendingQueueMetadataLocked (capacity.go:646-670), which additionally re-runs evaluateQuotaBlockLocked per pending test (another timeslot SQL + up to two projectStore.GetProject queries each, capacity.go:179-217). All of this executes while holding tr.mu as a WRITE lock, and it is triggered from read endpoints: refreshReadMetadataForTestList/refreshReadMetadataForTest call it whenever any test is QUEUED/PENDING (pkg/api/tests.go:718, 741, plus ~8 more list call sites), and from nextPromotablePendingTestLocked (pkg/api/testexecution.go:3776).

**Failure scenario:** 50 tests held PENDING on quota while dashboards poll GET /tests: each poll performs ~O(P log P)≈300 comparisons × 2 ranks ≈ 600 ListCapacityReservationsByTimeRange queries plus ~50 quota evaluations (~700+ SQL round trips) with the global runner write lock held. On PostgreSQL at ~1ms/query the lock is held ~0.7s per poll, serializing every other API request; with several viewers polling every few seconds the control plane effectively stalls, and cost grows superlinearly with queue depth. Secondary wrongness: each rank re-reads time.Now() and the DB, so comparator results can change mid-sort, violating strict weak ordering and yielding an arbitrary (unfair) queue order exactly when a reservation window opens during the sort.

**Suggested fix:** Decorate-sort-undecorate: fetch active bound timeslots ONCE per refresh, precompute pendingFairShareRank for each pending test into a slice, sort the decorated slice, and pass the shared activeSlots/time.Now() into rank computation. Optionally move the pending-queue metadata refresh off the read path (event-driven on state changes) so GET /tests never takes the write lock.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V008 — CONFIRMED (medium)

Full causal chain established at snapshot HEAD dc05008ab:

1. GET /api/v1/tests (pkg/api/server.go:734) → handleListTests (pkg/api/handlers.go:94,115) → listTestSummaryPage (pkg/api/tests.go:545) → refreshReadMetadataForTestList (tests.go:547); ~8 more list entry points (tests.go:484,496,514,614,639,663,685; library_bundle.go:97) also call it.
2. refreshReadMetadataForTestList (tests.go:724-747): if ANY test is QUEUED/PENDING it takes tr.mu.Lock() (WRITE lock, tests.go:739) and calls refreshPendingQueueMetadataLocked (tests.go:741).
3. refreshPendingQueueMetadataLocked (capacity.go:646-670) → sortPendingTestsForFairShareLocked (capacity.go:660); sort.Slice comparator (capacity.go:557-559) recomputes pendingFairShareRankLocked for BOTH elements per comparison — no memoization.
4. Each rank (capacity.go:579-602) calls time.Now() (585) + boundTimeslotsActiveAtLocked (589) → reservationStore.ListCapacityReservationsByTimeRange (timeslot_reservation.go:718) → direct SQL, no cache (pkg/storage/sqlite_timeslots.go:103-117); reservationStore is always the SQL store in production (runner.go:1262), so the query runs even with zero reservations configured. Plus 3 O(len(tr.tests)) scans per rank (capacity.go:596-597,604-617).
5. Then per pending test evaluateQuotaBlockLocked (capacity.go:663) → projectStore.GetProject SQL ×2 (capacity.go:452,467; sqlite_projects.go:1082) + evaluateReservedCapacityBlockLocked → another boundTimeslotsActiveAtLocked SQL (capacity.go:263).

50 pending → ~2×n·log n ≈ 500-600 SQL queries + ~150 more, all under the global write lock, per poll — the stated stall on Postgres (~1ms RTT) follows. Time.Now()+fresh DB per rank also makes the comparator non-strict-weak-ordered (arbitrary order, no panic). Refuting guards hunted: none (no memoization, no store cache, guard only skips when zero QUEUED/PENDING). Prior items W42/NF-capacity-blank concern the capacity inventory cache — different symbol, not a regression.

</details>


#### 24. Conflict/failed-blocked schedules are durably rewritten with a revision bump on every 30s 
**Where:** `pkg/api/scheduler.go:2203` · **Area:** AREA-02 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

reconcileScenarioScheduleOccurrenceLocked's no-change early-return (pkg/api/scheduler.go:2203-2219) covers only reason codes approval_required/approval_rejected/approval_state_inconsistent. An occurrence blocked with schedule_conflict, timeslot_unavailable, environment_unavailable, or missed_run_requires_decision — or a failed occurrence — falls through to lines 2220-2276, which set LastOccurrence/UpdatedAt/LastError/NextRequiredAction and call saveScenarioScheduleForSweepLocked unconditionally. Because those branches never advance NextRunAt past the blocked plannedAt, the schedule stays due and this repeats on EVERY sweep (defaultScenarioSchedulerSweepInterval=30s, scheduler.go:81). UpdateScenarioScheduleIfRevision always writes revision=expected+1 (pkg/storage/sqlite_schedules.go:249-264), so the schedule row churns and its revision inflates indefinitely. The execution-plan side already has the missing idempotence guard for its equivalent holds (pkg/api/execution_plan_scheduler.go:1035-1038 and 799-802), proving the scenario-side omission is an oversight, not a design choice.

**Failure scenario:** A schedule references an archived release → sweep records a blocked schedule_conflict occurrence. Left over a weekend, the schedule row is rewritten ~2,880 times/day with revision +1 per sweep (~8.6k revisions, plus constant UpdatedAt churn making 'last modified' meaningless). When the operator opens the schedule to fix it, the PUT requires If-Match/expected_revision (pkg/api/schedule_preconditions.go:11-43, enforced at pkg/api/handlers_scenarios.go:278); any read→edit→submit cycle that spans a 30s sweep boundary gets 409 'changed concurrently' — the exact operator trying to unblock the schedule is repeatedly rejected by the scheduler's own no-op rewrites.

**Suggested fix:** Extend the 2203 guard to every blocked/failed reason: when schedule.LastOccurrence matches the current occurrence (ID+Revision) AND LastError/NextRequiredAction already reflect the mapped values, return handled without saving — mirroring the exec-plan approval-hold guard.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V009 — CONFIRMED (medium)

Sweep runs every 30s (pkg/api/scheduler.go:81,150) and processes any schedule with NextRunAt<=now (scheduler.go:346-350) via runScenarioScheduleLocked (399) -> reconcileScenarioScheduleOccurrenceLocked (1620). The no-change guard (2203-2218) covers only approval_required/approval_rejected/approval_state_inconsistent; blocked schedule_conflict/environment_unavailable/missed_run_requires_decision and failed occurrences fall through (2220-2276): UpdatedAt=now, LastError/NextRequiredAction set, NextRunAt never advanced, then saveScenarioScheduleForSweepLocked (2276) -> UpdateScenarioScheduleIfRevision, which always writes revision=expected+1 (pkg/storage/sqlite_schedules.go:249-264). Schedule stays due, so this repeats indefinitely. PUT edits require If-Match/expected_revision (pkg/api/schedule_preconditions.go:11-43; handlers_scenarios.go:278) so mid-edit sweeps 409 the operator. Exec-plan sibling has the guard (execution_plan_scheduler.go:799-802,1034-1038). Prior-report 78-81 are unrelated perf items (minute-scan/LoadLocation), still open — no regression.

</details>


#### 25. DST fall-back: schedules firing inside the repeated local hour fire twice, ~60 minutes apa
**Where:** `pkg/api/scheduler.go:589` · **Area:** AREA-02 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

nextCronRun matches cron fields against local wall time and returns the earliest matching UTC instant (pkg/api/scheduler.go:589-624). On a DST fall-back day both UTC instants of the repeated local hour satisfy the same cron fields. After the first materialization the schedule advances via nextScenarioScheduleRun(schedule, now) (scheduler.go:1797-1815; reconcile path 2231-2242) or nextCronRun(.., advanceFrom=plannedAt) for execution-plan schedules (pkg/api/execution_plan_scheduler.go:1098-1110), and the search — being strictly-after — lands on the SECOND instant of the same local time one hour later. Occurrence dedup keys on (scheduleID, plannedAt) via GetScenarioScheduleOccurrenceByScheduleAndPlannedAt, and the two fires have different plannedAt values, so nothing dedups them. The comment at scheduler.go:592-595 ('during fall-back the earliest matching UTC instant wins') describes only a single evaluation; the test suite pins only that first instant (pkg/api/runner_test.go:6052-6059) and never exercises the advance across the transition.

**Failure scenario:** Schedule '30 1 * * *' timezone America/Chicago on 2026-11-01 (fall-back at 02:00 CDT = 07:00 UTC): sweep materializes the run at 06:30 UTC (01:30 CDT), advance-from-now finds 07:30 UTC (01:30 CST) matching the same fields → NextRunAt=07:30 UTC → a second, duplicate nightly run fires one hour after the first and overlaps it, double-consuming quota/reserved capacity and producing two occurrence/run records for one scheduled day. Applies equally to execution-plan schedules through the shared evaluator.

**Suggested fix:** In nextCronRun (or in the advance callers), reject a candidate whose local wall-clock time AND local calendar date equal the previous fire's — i.e., for non-wildcard hour fields, skip the second occurrence of a repeated local time. Alternatively advance from plannedAt converted to local, +1 local minute, re-converted after the transition.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V010 — CONFIRMED (medium)

nextCronRun (pkg/api/scheduler.go:589-624) iterates UTC minutes but matches cron fields against local wall time with no repeated-hour guard; on fall-back both UTC instants of the repeated local hour match. After the first fire the scenario path advances via nextScenarioScheduleRun(schedule, now) — a strictly-after search (scheduler.go:1797-1815, 554-561) — which lands on the second instant (e.g. cron "30 1" America/Chicago 2026-11-01: 06:30Z then 07:30Z, both local 01:30). Exec-plan path advances from plannedAt with the same result (execution_plan_scheduler.go:1101-1110). Occurrence dedup keys on (scheduleID, plannedAt) (scheduler.go:365-381,1965), and the two fires have different plannedAt, so a duplicate run materializes. Contradicts the code's own "earliest matching UTC instant wins" comment (592-594). Test pins only the first instant (runner_test.go:6052-6059); no advance-across-transition coverage. Prior-report 78-81 are perf items — different issue, no regression.

</details>


#### 26. GET /archive/manifest re-decodes and re-scrubs the entire transaction archive on every req
**Where:** `pkg/api/handlers_archive.go:45` · **Area:** AREA-03 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleGetRunArchiveManifest calls sanitizedRunArchiveManifest (pkg/api/handlers_archive.go:45), which builds a full sanitize plan: buildSanitizedRunArchivePlan runs writeSanitizedArchiveTransactions to io.Discard over the whole transactions.jsonl — JSON-decoding, scrubbing, and re-encoding every record — solely to compute the sanitized artifact's count/size/SHA (pkg/api/archive_export_sanitize.go:37-67, pkg/api/archive_transaction_sanitize.go:36-83). Only the raw manifest is cached (cachedRunArchiveManifest); the sanitize pass has no cache keyed on the manifest hash, so every manifest fetch repeats an O(archive-size) scan bounded only at 2M records/512MiB.

**Failure scenario:** A run archives 2M transactions (~500MB JSONL). Each time a viewer opens the run's archive view, GET /api/v1/tests/{id}/archive/manifest decodes+scrubs+re-encodes all 2M records (tens of seconds of CPU plus 500MB of file I/O) before returning a few KB of metadata; five concurrent viewers quintuple the load. The archive is immutable, so every pass after the first computes an identical result.

**Suggested fix:** Cache the sanitized stats/masked manifest keyed by runArchiveReportCacheKey (ManifestSHA256 + counts) in a small LRU alongside runArchiveManifestCache; invalidate through forgetRunArchiveCache.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V016 — CONFIRMED (medium)

Chain verified. `handleGetRunArchiveManifest` calls `sanitizedRunArchiveManifest` per request (pkg/api/handlers_archive.go:45), which always runs `buildSanitizedRunArchivePlan` → `writeSanitizedArchiveTransactions(ctx, record, manifest, io.Discard)` (pkg/api/archive_export_sanitize.go:37, 141-147) purely to compute sanitized count/size/SHA. That function JSON-decodes, scrubs, and re-encodes every transaction record (pkg/api/archive_transaction_sanitize.go:58-74), budgeted at 2M records / 512MiB (archive_loaders.go:29-30). No cache of the sanitize result exists: grep for "sanitiz" in pkg/api/archive_report_cache.go is empty; only the raw manifest is cached (`runArchiveManifestCache`, :169, 270-277). Archives are immutable, so every manifest GET repeats an identical O(archive-size) scan. Medium severity fits: heavy per-request CPU/IO on a metadata endpoint, no correctness impact.

</details>


#### 27. Every JSON report GET rebuilds full reports for all sibling runs in the release against a 
**Where:** `pkg/api/handlers_archive.go:528` · **Area:** AREA-03 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleGetRunArchiveReport's JSON path unconditionally calls attachReleaseTrend (pkg/api/handlers_archive.go:528), which runs collectReleaseTrendRuns → cachedRunArchiveReport for every archived run in the release (pkg/api/handlers_release_trend.go:39-56, 68-83). runArchiveReportCache holds only 16 entries / 128MiB (pkg/api/archive_report_cache.go:16-18), so a release with more than 16 archived runs thrashes the LRU: each report view re-streams multiple sibling archives end-to-end (buildRunArchiveReport scans up to 2M transactions per sibling), and all N cloned reports are held in memory for the trend computation. handleGetRunArchiveReportsBatch (:595-597) repeats this per batched ID.

**Failure scenario:** A release accumulates 30 archived runs of ~1M transactions each. Opening any one run's report (GET /tests/{id}/archive/report — the dashboard results data source) triggers ≥14 cache-miss full-archive report builds (~14M transaction decodes) before responding; every subsequent view of a different run in the release repeats the thrash. Report latency degrades from milliseconds to minutes and pins controller CPU/disk, even though the caller wanted one run plus a two-point trend.

**Suggested fix:** Persist or cache a compact per-run trend point (the ~10 scalar fields trendPointFromRun reads) keyed by manifest hash, or restrict attachReleaseTrend to the current run plus its immediate predecessor; alternatively size the report cache to release cardinality.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V017 — CONFIRMED (medium)

Chain verified. The JSON report path unconditionally calls `s.attachReleaseTrend(report)` (pkg/api/handlers_archive.go:528; comment at :525-526 says "unconditionally"), and the batch path repeats it per ID (:596). `attachReleaseTrend` → `collectReleaseTrendRuns` builds `cachedRunArchiveReport` for every archived run in the release (pkg/api/handlers_release_trend.go:39-56, 68-83); the code's own comment (:66-67) admits it "collects and rebuilds every archived report in the release". `runArchiveReportCache` holds 16 entries / 128MiB (pkg/api/archive_report_cache.go:15,17,168), so a release with >16 archived runs LRU-thrashes: each cache miss re-runs `buildRunArchiveReport`'s full-archive scan, and all N reports are held in `runs` during trend computation. Profile-gated variant `attachReleaseTrendForProfile` (:88-93) exists but is not used on the JSON path. Medium: severe latency/CPU on large releases, functionally correct.

</details>


#### 28. Application Load Comparison hard-fails when any run exceeds 500k transactions and holds ev
**Where:** `pkg/api/handlers_load_comparison.go:76` · **Area:** AREA-03 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

collectApplicationLoadComparisonRuns calls readAllArchiveTransactions for every archived release run of the application (pkg/api/handlers_load_comparison.go:76) and propagates its error (:77-79), and readAllArchiveRecordsContext returns a hard error at maxArchiveAnalysisRecords=500_000 records or 128MiB (pkg/api/archive_loaders.go:22-23, 749-751). All runs' decoded []TransactionEvent slices are retained simultaneously in the runs slice until buildLoadComparison finishes, even though the computation only needs per-interval aggregates.

**Failure scenario:** An application has one release-associated archived run of 600k transactions (≈170 TPS for 1 hour — routine for a load-testing platform). GET /api/v1/applications/{id}/load-comparison returns 500 'archive exceeds bounded analysis limit (500000 records or 134217728 bytes)' for the entire application, with no indication of which run is at fault; the Load Comparison view stays dead until the user guesses and excludes the offending run via test_id filters. Below the limit, 15 archived runs near the cap decode to multi-GB of resident TransactionEvent structs in a single request.

**Suggested fix:** Stream transactions per run with forEachArchiveTransactionContext (2M-record budget) feeding the interval accumulators directly, dropping full-slice retention; degrade per-run (skip + annotate) instead of failing the whole endpoint.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V015 — CONFIRMED (medium)

Chain verified. `collectApplicationLoadComparisonRuns` calls `readAllArchiveTransactions(record)` for every release-associated run (pkg/api/handlers_load_comparison.go:76) and returns the error (:77-80), which the handler converts to a 500 for the whole endpoint (:20-23). `readAllArchiveTransactions` → `readAllArchiveRecords` → `readAllArchiveRecordsContext` (pkg/api/archive_loaders.go:691-694, 720-724), which hard-errors at `maxArchiveAnalysisRecords=500_000` / `maxArchiveAnalysisBytes=128<<20` (:22-23, 749-751) with no per-run skip. Full decoded `[]TransactionEvent` slices for all runs are retained in `runs` (handlers_load_comparison.go:103-112) until `buildLoadComparison` returns (:25). One over-limit run kills the entire application's Load Comparison view; the error names no run. Severity medium is apt: feature-breaking at routine perf-test scale but confined to this endpoint.

</details>


#### 29. One active SLA waiver marks every release breach as waived — scope and creation time are i
**Where:** `pkg/api/release_sla_governance.go:233` · **Area:** AREA-03 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

buildReleaseSLAGovernance counts active waivers (pkg/api/release_sla_governance.go:163-168) and then, if any exist, sets Summary.WaivedBreaches = Summary.FailedRuns and leaves UnwaivedBreaches at 0 (:233-237), driving the release gate status to 'waived' instead of 'failed' (:238-251). The waiver's persisted Scope field (accepted from the API at :60-65, stored and returned) is never consulted, and breaches from runs completed after the waiver was created are equally treated as waived. Runs whose archive report is unreadable are conservatively counted as breached (:197-211) and are then also silently waived.

**Failure scenario:** An analyst creates a waiver for one historical breach (scope 'test-123', reason 'known infra blip'). A week later a new run in the same release breaches its SLA badly. GET /api/v1/releases/{id}/sla-governance reports status='waived', UnwaivedBreaches=0, and next action 'Review waiver expiration and audit trail before promotion' — the governance gate presents the new, never-reviewed breach as already risk-accepted, so the release can be promoted on stale paper.

**Suggested fix:** Evaluate waivers per breach: match Scope ('release' vs a specific test id) and require waiver.CreatedAt (or an explicit coverage window) to be at/after the breaching run's completion; count uncovered breaches as unwaived.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V018 — CONFIRMED (medium)

Chain verified. `buildReleaseSLAGovernance` counts waivers only by `Status == "active"` (pkg/api/release_sla_governance.go:163-168) — `Scope` and `CreatedAt` never inspected. Then the all-or-nothing assignment: `if activeWaivers > 0 { WaivedBreaches = FailedRuns }` else `UnwaivedBreaches = FailedRuns` (:233-237), so one active waiver zeroes UnwaivedBreaches and flips status to "waived" with next-action "Review waiver expiration..." (:245-247). `Scope` is accepted from the API (:87-90, default "release"), persisted (:122), and round-tripped by `listReleaseSLAWaivers` (sqlite path via `ListReleaseSLAWaivers`, in-memory :287-310) but evaluated nowhere. Breaches occurring after waiver creation are equally covered, and unreadable-archive runs conservatively marked breached (:197-211) are then silently waived too. Medium is fair: governance display/gate misrepresentation, not an execution blocker.

</details>


#### 30. JMeter ZIP with multiple .jmx files silently imports only the alphabetically first plan
**Where:** `pkg/api/handlers_import_jmeter.go:191` · **Area:** AREA-04 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

readJMeterZipImport (pkg/api/handlers_import_jmeter.go:172-192) collects every .jmx entry into jmxFiles, sorts the names, and returns only names[0]; the other test plans are discarded with no warning, diagnostic, or unsupported-element entry — neither handleImportJMeter (line 23) nor writeJMeterImportResponse (line 323) mentions the ignored files anywhere in the response.

**Failure scenario:** A user exports two JMeter test plans and uploads plans.zip containing checkout.jmx and login.jmx plus shared CSVs to POST /scripts/import/jmeter. Only checkout.jmx (alphabetically first) is converted; the response reports a successful import with stats/preflight for that one plan and zero mention of login.jmx. The user saves, believing the whole upload imported, and discovers the missing scenario only when the login flow is absent from load tests.

**Suggested fix:** Either convert every .jmx into the existing multi-script response shape (result.Scripts already supports it), or keep single-plan behavior but append a warning listing the ignored .jmx entries (and reject when >1 unless a `jmx_entry` form value selects one).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V021 — CONFIRMED (medium)

readJMeterZipImport (pkg/api/handlers_import_jmeter.go:172-192) collects every `.jmx` entry into `jmxFiles`, then `sort.Strings(names); selected := names[0]; return jmxFiles[selected], selected, sidecars, nil` — all other plans are discarded. The caller (lines 121-130) replaces `data`/`fileName` with the single selected entry; no warning is appended anywhere. `result.Warnings` (lines 334, 341) comes solely from the converter (ConvertJMX), and a grep of the handler shows no "multiple"/"ignored" surfacing of unselected .jmx entries. ZIP path is reachable via isJMeterZipUpload (line 146-150, .zip extension or zip content-type). Silent partial import with a success response matches the claimed failure scenario; medium severity holds.

</details>


#### 31. Merge re-import replaces a same-named CSV data source wholesale when its inline rows are e
**Where:** `pkg/api/openapi_import_commit.go:272` · **Area:** AREA-04 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

mergeCSVDataSource returns `imported, imported.Columns` whenever parseCSVInline(existing.InlineData) yields no header (pkg/api/openapi_import_commit.go:271-274), and mergeImportDataSources then overwrites the existing entry in place (line 253-255). A user-customized source whose data lives outside InlineData — Path/StorageRef file binding, or rows deliberately cleared — loses Strategy, Delimiter, Path, StorageRef and every other field to the fresh import's placeholder. Simultaneously diffImportByKey reports `PreservedDataSources = len(existing.DataSources)` and emits 'Preserved N existing data source(s)' (lines 55-64), so the dry-run and commit diff affirmatively misstate what the merge did. This is the documented merge contract's failure mode ('existing seed rows... preserved', line 237-240) and matches the area's re-import-wipes-user-edits defect class.

**Failure scenario:** User imports an OpenAPI spec producing csv source `users` with inline placeholder rows; via the editor they point it at a governed uploaded file (Path/StorageRef set, InlineData cleared, Strategy=unique). Later they re-import the updated spec with mode=replace, replace_mode=merge_by_operation_id. The committed script's `users` source is now the import's inline placeholder: file binding and unique strategy silently gone, next run iterates fabricated sample rows instead of real test data, while the commit response diff says 'Preserved 1 existing data source(s)'. Verified reachable: handleImportCommit line 946 calls spec.merge → mergeImportScriptByKey line 126 → mergeImportDataSources; dsl.DataSource (pkg/dsl/types.go:745-759) supports Path/StorageRef on csv-typed sources. Assumed (not verified end-to-end): the editor permits clearing InlineData on a csv-typed source while keeping the name; if it always retypes to 'file', the type guard at line 268 makes the wipe unreachable and this drops to low.

**Suggested fix:** When the existing source has no parseable inline header but is non-empty in other respects (Path/StorageRef set, or Strategy/Columns configured), return `existing` unchanged (optionally merging Columns metadata) instead of adopting the imported source; only adopt wholesale when the existing source is entirely default. Make the diff report replaced sources honestly.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V023 — CONFIRMED (medium)

mergeCSVDataSource (pkg/api/openapi_import_commit.go:267-274): both sources csv-typed, `parseCSVInline(existing.InlineData)` yields empty header → `return imported, imported.Columns`, replacing the struct wholesale via mergeImportDataSources (lines 252-254), losing Path/StorageRef/Strategy/Delimiter. The finding's open assumption is resolved: csv+empty-InlineData+Path is a legitimate runtime configuration — pkg/engine/data.go:292-298 falls back to `fileasset.OpenPath(src.Path)` when InlineData is blank (test at pkg/engine/data_test.go:562 exercises csv+Path). Reachable: import_commit_service.go:946 `spec.merge` → mergeOpenAPIImportScript:79 → mergeImportScriptByKey:126. Meanwhile diffImportByKey (lines 55-65) sets PreservedDataSources = len(existing.DataSources) unconditionally and emits "Preserved N existing data source(s)", misstating the replacement. Medium holds.

</details>


#### 32. Fleet-alert webhooks fire only when a client polls GET /api/v1/fleet/alerts, and concurren
**Where:** `pkg/api/handlers_fleet.go:112` · **Area:** AREA-05 · **Category:** reliability-notifications · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

routeFleetAlertNotifications is invoked solely from handleGetFleetAlerts (pkg/api/handlers_fleet.go:112-117); no background evaluator exists (only other buildFleetAlerts caller is the ack handler at 145). So configured fleet_alert webhooks never fire unless a user/dashboard happens to poll the endpoint — an unwatched controller sends no alert for an unreachable/saturated generator. Additionally routeFleetAlertNotifications (443-495) performs an unsynchronized read-modify-write of the 'fleet_alert_notification_state' setting: two concurrent GETs both load the state, both see an alert as un-routed, both Dispatch, and the second SaveSetting clobbers the first, so multi-viewer dashboards produce duplicate webhook notifications per new alert.

**Failure scenario:** Ops team configures a Slack webhook for fleet_alert and closes the dashboard overnight. A generator goes unreachable at 02:00; no notification is ever sent because nothing polls /fleet/alerts (defeats the purpose of webhook alerting). Conversely during the day, 3 open dashboards poll concurrently when a new saturation alert appears; each request observes Routed missing and dispatches, delivering the same alert 2-3 times to Slack.

**Suggested fix:** Move routing into the existing generator-inventory refresh cadence (leader-gated), and guard the routed-state read-modify-write with a mutex or the CAS setting primitive already used for remote-run links.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V027 — CONFIRMED (medium)

routeFleetAlertNotifications is called only from handleGetFleetAlerts (pkg/api/handlers_fleet.go:115); repo-wide grep shows no background caller, and buildFleetAlerts is only invoked from the GET (113) and ack (145) handlers — so fleet_alert webhooks (supported event, notifications.go:52) never fire without a client poll. The routed-state read-modify-write is unsynchronized: loadFleetAlertNotificationState (447), Dispatch (484), SaveSetting (490) with no mutex/CAS. Concurrent GETs both see an alert un-routed and both dispatch; sender-side there is no dedupe — notificationDedupeKey (notifications.go:434-453) includes event.Timestamp (time.Now per dispatch, handlers_fleet.go:533), so even receiver-side idempotency keys differ across the duplicate dispatches. Both claimed behaviors hold.

</details>


#### 33. Remote-run handlers hold remoteConfigMu.RLock across multi-call cross-controller HTTP, sta
**Where:** `pkg/api/remote_runs.go:88` · **Area:** AREA-05 · **Category:** concurrency-lock-scope · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleCreateRemoteRun (pkg/api/remote_runs.go:88-89), handleGetRemoteRun (227-228) and handleRemoteRunAction (260-261) take s.remoteConfigMu.RLock with defer and then perform a chain of outbound HTTP calls — one pushRemoteWorkspaceItem per referenced scenario/script (each a remoteLibraryImport POST), the test create, an optional start, and a status refresh — each bounded only by defaultLibraryRemoteJSONTimeout of 30s (pkg/api/library_federation.go:27). handleLibraryUpdateRemote/handleLibraryDeleteRemote take the write lock (pkg/api/library_handlers.go:279, 369). Go's RWMutex blocks new readers once a writer is waiting, so a single slow remote peer freezes every endpoint that touches remoteConfigMu for the duration of the in-flight chain.

**Failure scenario:** A federated peer accepts TCP but stalls responses (overloaded or half-dead LB). A user creates a remote run for a scenario with 3 scripts: the handler holds the read lock through 4-6 HTTP calls x 30s timeout (2-3 minutes). An admin then saves remote-workspace settings -> write Lock blocks; every subsequent remote-workspace/remote-run request (all RLock) queues behind the writer. Dashboard pages backed by these endpoints hang for minutes; more concurrent creates extend the freeze indefinitely. Expected: config reads snapshot the remote value (LibraryRemote is already copied by value) and release the lock before any network I/O.

**Suggested fix:** Resolve freshLibraryRemote under a short RLock, release, then run the remote workflow on the copied LibraryRemote value (authorizeRemoteRequest already revalidates transport per request). Re-take the lock only if config must be re-read.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V026 — CONFIRMED (medium)

RLock+defer spans entire handlers: pkg/api/remote_runs.go:88-89 (create), 227-228 (get), 260-261 (action). Inside the lock: per-item pushRemoteWorkspaceItem loop (remote_runs.go:358 via prepareRemoteRunCreateRequest at 103), remoteJSON create POST (125), optional start (192), status refresh (200) — each capped only by libraryRemoteJSONTimeout, default 30s (pkg/api/library_federation.go:27; applied in library_remote.go:250-252). Writers take remoteConfigMu.Lock: library_handlers.go:279 (update), 369 (delete). Go RWMutex blocks new readers once a writer waits, so one stalled peer plus one config save freezes all remoteConfigMu RLock endpoints (library_handlers.go:428/482/512/579/657 and remote_runs). server.go:89-93 documents holding the lock for the full outbound operation as intentional (cleanup-intent stability), but the multi-minute cross-endpoint starvation stands; LibraryRemote is copied by value so a snapshot-then-release fix is feasible.

</details>


#### 34. Object-repository bind-from-script rebinds nested control-flow actions by name while the a
**Where:** `pkg/api/handlers_object_repository.go:395` · **Area:** AREA-06 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

objectRepositoryObjectsFromScript (handlers_object_repository.go:386-453) and ambiguousObjectNames (348-381) scan ONLY top-level script.Init/Actions/Teardown (no recursion), but bindScriptToObjectRepository (301-330) recursively walks Control.Steps/Control.Else and sets object_ref on every name match. A nested action that shares a name with a top-level action but targets a different element is never seen by the ambiguity check, so both get bound to the one repository object built from the top-level action's locator. resolveActionObjectRef (object_ref_resolver.go:169) then overwrites the nested action's inline Selector with the repository primary at script-preparation time, so the nested action executes against the wrong element — exactly the mis-binding the code comment at 389-394 says the ambiguity exclusion exists to prevent.

**Failure scenario:** A TruClient/recorder-imported browser script has top-level action "Click Continue" (selector A) and, inside a control_if branch, another step named "Click Continue" with a different selector B (a different Continue button). Operator calls POST /api/v1/scripts/object-repositories/from-script with bind_script=true. The repo object "Click Continue" is built from selector A only; binding walks the nested branch and sets object_ref on the nested step too. On the next replay/load run, resolveObjectRefs rewrites the nested step's selector B to A, and the script silently clicks the wrong element inside the branch — corrupted behavior with no warning, since the nested action had an inline selector so degradation paths never fire.

**Suggested fix:** Make the three scans share one traversal scope: either recurse collect() and ambiguousObjectNames() through Control.Steps/Else (preferred, matches bind), or restrict bindScriptToObjectRepository to top-level actions. Sharing dslwalk.WalkActions for all three removes the drift permanently.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V033 — CONFIRMED (medium)

Scope drift verified. `ambiguousObjectNames` (pkg/api/handlers_object_repository.go:348-381) and `collect` in `objectRepositoryObjectsFromScript` (:396-451) iterate only top-level Init/Actions/Teardown slices — neither recurses into `action.Control`. But `bindScriptToObjectRepository` (:301-330) walks `action.Control.Steps` and `.Else` recursively (:320-323) and stamps `object_ref` on every name match. The bind path is live: handler calls it when `req.BindScript` (:249-251) and persists via UpdateScript. At preparation, `resolveActionObjectRefs` also recurses into Control (pkg/api/object_ref_resolver.go:135-138) and `resolveActionObjectRef` unconditionally overwrites `action.Selector = primary` (:169) on successful lookup — degrade paths fire only on lookup failure, so a nested same-named action with a different inline selector is silently re-targeted, exactly what the comment at :389-394 says the ambiguity exclusion prevents.

</details>


#### 35. Command Center iterations edit always 400s for groups with a create-time iteration overrid
**Where:** `pkg/api/handlers_test_group_settings.go:624` · **Area:** AREA-07 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

UpdateTestGroupRuntimeSettings builds the recompile entry with Iterations: cloneIntPtr(oldGroup.IterationOverride) (pkg/api/handlers_test_group_settings.go:624) while putting the user's new iterations value into entry.RuntimeSettings via replaceRuntimeFields (handlers_test_group_settings.go:598). compileManagedTestGroupForModeWithRevisionLocked then rejects any request where group.Iterations != nil and the runtime-settings iteration value differs (pkg/api/testcreation.go:1562-1565), so a group whose iterations came from the scenario per-script Iterations field (createGroupEntriesFromStoredScenario, testcreation.go:1037 -> IterationOverride at testcreation.go:1637) can never have its iterations changed from the Command Center dialog.

**Failure scenario:** Author a scenario with a script group whose Iterations = 10 (the standard scenario-editor iterations field). Create a queued run from it. In Command Center, right-click the group -> Runtime Settings, change Iterations to 20, Save. The dashboard sends fields:["iterations"], runtime_settings:{iterations:20} (GroupRuntimeSettingsDialog.tsx builds the payload from the group override layer); the backend merges it, then compile fails with HTTP 400 'group[0]: iterations conflicts with runtime_settings.run_logic.iterations' — a message referencing a conflict the user never authored. There is no path to change iterations for such a group from the run view; the pre-start in-place edit feature is broken for the most common iterations-authoring path. Workaround: revise the queued run through the scenario editor.

**Suggested fix:** When normalizedFields contains 'iterations', derive the new override from the merged runtime settings and pass it as entry.Iterations (and drop the compact alias from merged), or clear entry.Iterations so the runtime-settings layer is authoritative for this edit. Also surface IterationOverride in the view's layer attribution.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V038 — CONFIRMED (medium)

Chain verified. Scenario per-script Iterations becomes CreateGroupEntry.Iterations (pkg/api/testcreation.go:1036) and compiles to TestGroup.IterationOverride with IterationOverrideKnown=true (testcreation.go:1637-1638); at create time there is no conflict because scenario RuntimeSettings carries no iteration value. UpdateTestGroupRuntimeSettings always rebuilds the entry with Iterations=cloneIntPtr(oldGroup.IterationOverride) (pkg/api/handlers_test_group_settings.go:624) while replaceRuntimeFields("iterations") writes the user's new value into merged.Iterations (pkg/api/handlers_scenario_runtime.go:433-434, 477-498; stored group settings lack RunLogic since setRuntimeSettingsIteration only touches executionRuntimeSettings, testcreation.go:1689-1691, and stored settings come from protectedGroupSettings at 1795). Recompile (handlers_test_group_settings.go:630) then hits the conflict check: group.Iterations(old)=10 vs runtimeSettingsIterationValue(merged)=20 → error (testcreation.go:1562-1565) → request fails. No handler path updates entry.Iterations for the "iterations" field. Dialog exposes iterations as an editable field (GroupRuntimeSettingsDialog.tsx:34,86-87).

</details>


#### 36. Any value containing '@' is classified as a credential, breaking email-valued parameters a
**Where:** `pkg/api/runtime_mask.go:87` · **Area:** AREA-07 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

sensitiveEnvValue treats any string that maskURLUserinfo alters as credential-bearing, and maskURLUserinfo (runtime_mask.go:274-294) redacts EVERY string containing '@' whose url.Parse result has no User field — which includes plain email addresses ('alice@example.com' parses as an opaque path) and URLs with '@' in the path. The comment (runtime_mask.go:84-88) justifies fail-closed handling for scheme-less user:password@host strings, but the rule over-matches passwordless emails. Downstream, rejectCredentialShapedParameterEdits (handlers_test_group_settings.go:1038-1043) refuses parameter adds/edits whose values are 'credential-shaped', projectTestGroupParameters (handlers_test_group_settings.go:1266-1275) renders such values as [REDACTED], and protectRuntimeOverrideSecretsWithOptions (runtime_overrides.go:127) converts such Env values into governed {{secret.*}} references whose provisioning requires a writable project secret store (testcreation.go:687-692).

**Failure scenario:** Scenario A: an operator opens the Command Center parameters dialog for a queued run and adds a login-users parameter with values ['alice@example.com','bob@example.com'] (the canonical load-test user list). The update is rejected with 400 'parameter X contains a credential-shaped value; manage credential values through secret bindings', and existing email value lists display as [REDACTED], making them uninspectable and uneditable from the run view. Scenario B: a test or scenario is created with runtime Env override SUPPORT_CONTACT=ops@example.com on a deployment without a writable encrypted secret store; protectRuntimeOverrideSecrets projects the email into the runtime-secret map and CreateTest fails with 'runtime credentials were entered, but the configured project secret provider cannot create governed values' — a hard create failure for a plain data value. With a writable store, a spurious governed secret APEX_RUNTIME_OVERRIDE_G*_* containing an email is silently created plus audit events. No unit test pins email behavior (pkg/api/runtime_env_mask_test.go covers none of these cases).

**Suggested fix:** Narrow the scheme-less fail-closed rule to strings matching user:password@host shape (colon before the '@' in the first segment, or a parseable authority), leaving bare emails (local@domain with no colon) unclassified; keep the URL-with-userinfo and query-credential rules unchanged.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V039 — CONFIRMED (medium)

Chain, all at snapshot HEAD dc05008ab:

1. pkg/api/runtime_mask.go:274-294 `maskURLUserinfo`: when `url.Parse` succeeds and `parsed.User == nil`, any string containing '@' returns `scrub.Redacted` (lines 286-289). Verified empirically with `go run` in scratchpad: `url.Parse("alice@example.com")` → err=nil, User=nil; same for `https://api.example.com/users/@me`. So bare emails are altered.
2. runtime_mask.go:87-89 `sensitiveEnvValue` returns true whenever `maskURLUserinfo(trimmed) != trimmed` — emails classified credential-shaped. No email allowlist exists (grep -i email in runtime_mask.go/runtime_overrides.go: zero hits).
3. Scenario A wiring: handlers_test_group_settings.go:893 calls `rejectCredentialShapedParameterEdits` in the Command Center group-parameters update path; :1038-1043 rejects any new/changed value where `sensitiveEnvValue` → 400 for email lists. Read side :1266-1273 projects such values via `projectSensitiveScriptValue` (uninspectable), and reconcile (:888) makes them uneditable.
4. Scenario B wiring: runtime_overrides.go:127 `protect("env."+key, ..., sensitiveEnvValue(value))` projects email Env values into the runtime-secret map; called from testcreation.go:509/1680/1684; testcreation.go:687-692 hard-fails create without a writable+rollback-capable secret store.
5. No refuting test: pkg/api/runtime_env_mask_test.go has no bare-email case.

Prior-report check: W11/PERF-1 concerned missing history masking in the same file (mask not applied) — a different defect; this is over-classification. Not a regression of those fixes.

</details>


#### 37. Schedule-health endpoint materializes the entire unbounded occurrence table (and force-rel
**Where:** `pkg/api/scenario.go:1040` · **Area:** AREA-07 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

ScenarioScheduleHealth calls store.ListAllScenarioScheduleOccurrences() (pkg/api/scenario.go:1040), which is 'SELECT ... ORDER BY planned_at DESC' with no LIMIT (pkg/storage/sqlite_schedules.go:972-979), decoding every occurrence row ever created just to increment per-status counters. scenario_schedule_occurrences has no automatic retention (only the manual single-occurrence delete at scenario.go:960), so the table grows one row per planned tick per schedule forever. The same method also calls refreshScenariosForHARead(true) (scenario.go:1013), and the force flag bypasses the 250 ms throttle (scenario.go:560-561), so on HA deployments every health call additionally reloads all stored scripts and scenarios. The dashboard Monitoring page polls this endpoint every 5 seconds (dashboard/src/components/MonitoringPage.tsx:687).

**Failure scenario:** A deployment runs four recurring schedules on 15-minute crons for a year (~140k occurrence rows). An operator leaves the Monitoring page open: every 5 seconds the controller executes a full-table SELECT, allocates and JSON-decodes 140k ScenarioScheduleOccurrence structs, and (in HA) re-reads every script and scenario from the shared store. Health-endpoint latency and controller CPU/GC grow linearly with schedule history and never recover, degrading the whole controller while the run calendar card is visible; there is no retention mechanism to stop the growth.

**Suggested fix:** Add a CountScenarioScheduleOccurrencesByStatus storage method (GROUP BY status) and use it in ScenarioScheduleHealth; drop the force flag from the health path; add an occurrence retention policy or scheduled prune.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V040 — CONFIRMED (medium)

Chain verified. GET /api/v1/schedules/health (pkg/api/route_registry_scheduling.go:47) → handleScenarioScheduleHealth (pkg/api/handlers_scenarios.go:389) → schedulingDomainService().Health (pkg/api/scheduling_service.go:91) → TestRunner.ScenarioScheduleHealth (pkg/api/scenario.go:1012). It calls refreshScenariosForHARead(true) (scenario.go:1013); force=true bypasses the 250ms throttle (scenario.go:560-563), reloading all scripts+scenarios — HA-only (early return when tr.leadership==nil, scenario.go:555). It then materializes every occurrence row via ListAllScenarioScheduleOccurrences (scenario.go:1040) — unbounded `SELECT ... ORDER BY planned_at DESC` (pkg/storage/sqlite_schedules.go:972-979) — solely to increment StatusCounts (scenario.go:1041-1045). Only per-schedule/single-occurrence deletes exist (sqlite_schedules.go:536, 1014); no bulk retention, so rows accumulate per tick (SaveScenarioScheduleOccurrence, scenario.go:702). MonitoringPage fetches schedule health in refresh (MonitoringPage.tsx:617-644) on setInterval(refresh, 5000) (line 687).

</details>


#### 38. Run manifest 'initial configuration' is read from the continuously rewritten durable recor
**Where:** `pkg/api/test_manifest.go:284` · **Area:** AREA-07 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

initialManifestConfiguration sources the manifest's initial_configuration from tr.storedTestRecord(test.ID) (test_manifest.go:284-286), but the durable TestRecord is rewritten with the CURRENT test state on every lifecycle mutation — VUser scaling sets test.TotalVUsers/Groups and calls saveManagedTestRecord (pkg/api/testexecution.go:1963-1968), goal adjustments persist (testexecution.go:1488-1495), and there are ~34 saveManagedTestRecord call sites. For any test without an archived manifest (i.e. every live or pre-archive run), initial_configuration therefore equals current_configuration after any mid-run change, defeating the manifest's before/after comparison exactly when an operator inspects what changed mid-run.

**Failure scenario:** Start a run with group VUsers=100. Mid-run, scale the group to 300 (persisted via saveManagedTestRecord). GET /api/v1/tests/{id}/manifest while the run is live: both initial_configuration.total_vusers and current_configuration.total_vusers report 300; the manifest claims the run was created at 300 VUsers. The runtime_changes audit timeline still shows the scale event, contradicting the initial section in the same document — silent wrongness in an evidence artifact.

**Suggested fix:** Persist an immutable creation-time configuration snapshot (either a dedicated column written once at create, or reuse the runtime-secret journal pattern) and have initialManifestConfiguration prefer it; fall back to the current-record behavior with the existing warning only for legacy rows.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V041 — CONFIRMED (medium)

Chain verified. For any non-archived run (source="live", pkg/api/test_manifest.go:230), initialManifestConfiguration reads tr.storedTestRecord(test.ID) — the single current durable row — and projects it as initial_configuration (test_manifest.go:284-286). That row is rewritten with CURRENT state on every persist: buildManagedTestRecord serializes live Groups/TotalVUsers/GoalConfig/RuntimeSettings/Status (pkg/api/persistence.go:1373-1419), and mid-run VUser scaling both sets TotalVUsers=capacityVUsersForGroups and calls saveManagedTestRecord (pkg/api/testexecution.go:2158-2161 rollback path; 1963-1967 fail-closed path; 35 save sites total). So after a mid-run scale, initial_configuration equals current_configuration, contradicting the runtime_changes timeline in the same manifest. The fallback warning at test_manifest.go:291 shows the design assumed the record held initial state. PRIOR-REPORT CHECK: M6/TierC-5 (DATA_POOLS_EVALUATION_2026-07-30.md:81,70) concern missing pool provenance fields in the manifest — a different defect, and both are still "open", so this is not a regression of a fixed item.

</details>


#### 39. Redis AllowRateLimit INCR/EXPIRE gap can permanently lock out a key
**Where:** `pkg/api/cache_backend.go:642` · **Area:** AREA-08 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

redisCacheBackend.AllowRateLimit increments the counter and only sets the TTL when the returned count is exactly 1 (cache_backend.go:650-657); if the process crashes or the connection drops between INCR and EXPIRE (or EXPIRE errors), the key persists with no TTL, subsequent requests see count != 1 and never set an expiry, so the counter grows monotonically and once it passes the limit every request for that key is rejected forever until someone manually deletes the redis key.

**Failure scenario:** APEXLOAD_API_KEY_RATE_LIMIT_PER_MINUTE=600 is configured with a redis cache backend. A controller pod is OOM-killed between Incr and Expire for service-account:ci-bot. The key now has TTL=-1; every subsequent request increments it and after ten minutes of normal CI traffic count exceeds 600 permanently — from then on every ci-bot request returns 429 'service account rate limit exceeded' indefinitely across all replicas, until an operator manually runs DEL on the key in redis.

**Suggested fix:** Replace the two-step with a Lua script (INCR; if result == 1 or PTTL < 0 then PEXPIRE) mirroring redisLoadOrStoreValueScript, or self-heal by checking PTTL when count > 1 and re-arming a missing expiry.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V049 — CONFIRMED (medium)

redisCacheBackend.AllowRateLimit (pkg/api/cache_backend.go:642-664): Incr at 650, then `if count == 1 { Expire }` at 654-658 — the classic non-atomic fixed-window gap. A crash/connection drop between INCR and EXPIRE (or an EXPIRE error, which returns after the increment already landed) leaves the key with no TTL; every later call sees count != 1 so no expiry is ever re-armed, and once count exceeds the limit the key rejects forever. No PTTL self-heal exists, while sibling LoadOrStoreValue uses an atomic Lua script (607-614), proving the pattern is available in-tree. Consumers with no recovery path: enforceServiceAccountRateLimit → 429 (middleware.go:284-305) and per-IP OIDC login limiting (auth_oidc.go uses the same primitive). Low-probability trigger, high impact — medium is right.

</details>


#### 40. Project and test deletion orphan powerbi_publishes rows forever
**Where:** `pkg/api/handlers_projects.go:552` · **Area:** AREA-08 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleDeleteProject (pkg/api/handlers_projects.go:552) deletes the project graph via deleteProjectGraphTx (pkg/storage/sqlite_projects.go:1289-1364), and handleDeleteTest via deleteTestGraphTx (pkg/storage/sqlite_tests.go:1323-1394); neither cascade contains a DELETE for powerbi_publishes, whose rows carry NOT NULL test_id and project_id (pkg/storage/sqlite_schema.go:685-700), and a repo-wide grep shows no other DELETE for that table anywhere — so publish records outlive both their test and their project permanently.

**Failure scenario:** Publish a completed run to Power BI (creates a powerbi_publishes row keyed by test_id/project_id), then delete the test or the whole project. The publish row survives with dangling foreign references. Over months of project churn in a deployment using Power BI publishing, the table accumulates unbounded orphan rows; any listing/dedupe query keyed by test_id or dataset (idx_powerbi_publishes_dataset) returns records for tests and projects that no longer exist, and the force-republish dedupe can match a stale orphan from a previous, deleted incarnation of a re-imported test id.

**Suggested fix:** Add `DELETE FROM powerbi_publishes WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)` to deleteProjectGraphTx and `DELETE FROM powerbi_publishes WHERE test_id = ?` to deleteTestGraphTx; audit direct_import_receipts for the same gap (it has lease-scoped deletes but verify project deletion coverage).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V046 — CONFIRMED (medium)

powerbi_publishes DDL carries NOT NULL test_id and project_id (pkg/storage/sqlite_schema.go:685-703). deleteProjectGraphTx's ~40-entry delete list (pkg/storage/sqlite_projects.go:1293-1364) has no powerbi_publishes statement, and deleteTestGraphTx (pkg/storage/sqlite_tests.go:1323-1394) deletes test_history/test_notes/run_archives/etc. but not powerbi_publishes. Repo-wide grep shows only two files reference the table — sqlite_schema.go and sqlite_powerbi.go — and sqlite_powerbi.go contains no DELETE at all, so no retention/prune path exists anywhere. Rows therefore survive both test and project deletion permanently, accumulating orphans with dangling foreign references exactly as claimed. Severity medium is appropriate: data-integrity/unbounded-growth, no security impact.

</details>


#### 41. Failed requests permanently burn their Idempotency-Key for the TTL
**Where:** `pkg/api/middleware.go:307` · **Area:** AREA-08 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

enforceIdempotencyKey reserves the key in the coordination store BEFORE the handler runs and has no release/rollback path, so a request that fails validation (400), authorization at the handler level, or the operation itself (500) still consumes the key for configuredIdempotencyKeyTTL (default 15 min, middleware.go:353-355); a client retrying with the same key — the standard idempotency retry contract — receives 409 'duplicate idempotency key' even though nothing was ever created, and even on success a replay returns 409 instead of the original resource (except the three carved-out routes at middleware.go:314-317).

**Failure scenario:** A CI pipeline POSTs /api/v1/tests with Idempotency-Key K; the controller returns a transient 500 (store busy) without creating the test. The pipeline retries with K per standard semantics and gets 409 'duplicate idempotency key' on every attempt for 15 minutes; the run is never created and the error message actively misleads the operator into believing it was. The same happens after any 400 (typo fixed, key reused).

**Suggested fix:** Wrap the handler with a status-observing responseWriter and delete/expire the reservation when the response status is >= 400 (at-most-once is only needed for success); or reserve only after the handler reports success. Document that replay-of-response is not provided outside the carved-out durable routes, and change the 409 message to say the key may correspond to an in-flight or failed request.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V048 — CONFIRMED (medium)

enforceIdempotencyKey (pkg/api/middleware.go:307-342) calls store.ReserveIdempotencyKey (line 331) BEFORE next.ServeHTTP in both authMiddleware (line 204-215) and projectScopedAuthMiddleware (line 268-272); !reserved always yields 409 "duplicate idempotency key" (lines 337-339). The IdempotencyStore interface (cache_backend.go:78) exposes only Reserve — grep finds no Release/Delete API; memory backend is set-if-absent (cache_backend.go:345-358), redis is SetNX with TTL (600-605). So a 400/500 first attempt still burns the key for configuredIdempotencyKeyTTL (default 15 min, middleware.go:353-355) and retries get a misleading 409. Only three routes are carved out (314-317). The prior-report match (api-auth-rbac-2, middleware duplication, status open) is a different issue — not a regression. Medium confirmed.

</details>


#### 42. Project deletion stalls every mutating request platform-wide, unbounded
**Where:** `pkg/api/project_mutation_gate.go:15` · **Area:** AREA-08 · **Category:** concurrency · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

projectMutationAdmission takes projectMutationMu.RLock for the FULL duration of every mutating request (including reading a slow client's multi-hundred-MB upload body, since the gate wraps the mux at pkg/api/server.go:553 and handlers read bodies inside), while handleDeleteProject (pkg/api/handlers_projects.go:401) holds the write side across external secret-provider discovery (network I/O, handlers_projects.go:417), listing up to 10000 outbox jobs, acquiring per-test archive fences, the durable graph delete, and per-archive physical object-store cleanup (handlers_projects.go:582-585) — and Go's writer-preferring RWMutex blocks ALL new readers the moment the writer starts waiting.

**Failure scenario:** A user is 3 minutes into uploading a 200 MB Bruno import over a slow link (holding an RLock). An admin deletes a project with 150 archived runs on Azure blob storage. The DELETE blocks waiting for the upload; from that instant every mutating request across ALL projects (test starts, stops, scale, script saves) queues behind the pending writer; requests exceed the 60 s WriteTimeout and fail platform-wide. After the upload completes, the deletion itself holds the write lock through secret discovery and 150 blob deletions (potentially minutes with retries/timeouts), extending the global write outage.

**Suggested fix:** Shrink the write-side critical section to just the tombstone install + durable graph delete: move externalsecret discovery before the lock, and move physical archive cleanup after releasing projectMutationMu (the beginProjectDeletion tombstone plus the per-test archive fences already prevent new work on the deleted project). Alternatively acquire the RLock in handlers after the body is fully read, or replace the global gate with a per-project striped lock keyed by resolved project id.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V047 — CONFIRMED (medium)

projectMutationMu is sync.RWMutex (pkg/api/server.go:114). projectMutationAdmission holds RLock via defer across next.ServeHTTP for every POST/PUT/PATCH/DELETE (pkg/api/project_mutation_gate.go:21-23); it wraps the mux inside bodyLimitMiddleware (pkg/api/server.go:553), which only installs MaxBytesReader (middleware.go:625-634), so slow body reads occur under the RLock. handleDeleteProject takes the write side for its entire body (handlers_projects.go:401-402), covering discoverProjectSecretMetadata external-provider I/O (line 418), durable graph delete, and the per-archive cleanupQueuedArchiveUnderFence object-store loop (lines 583-586). Go's writer-preferring RWMutex blocks all new readers once the writer waits, so every mutating request platform-wide queues against WriteTimeout=60s (server.go:562). The gate comment declares global serialization intentional, but the unbounded write-side critical section is not. Medium stands.

</details>


#### 43. Same-origin check ignores X-Forwarded-Proto, 403s mutations behind TLS proxies
**Where:** `pkg/api/security.go:70` · **Area:** AREA-08 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

evaluateOriginPolicy derives requestScheme solely from r.TLS (security.go:71-74), so behind a TLS-terminating ingress a browser's Origin 'https://host' never matches the plain-HTTP request scheme, same-origin detection fails, and with no AllowedOrigins configured corsMiddleware rejects every non-safe-method request with 403 'request origin is not allowed' (middleware.go:492-495) and the websocket upgrade CheckOrigin (websocket.go:66-75) refuses the connection — even though securityHeadersMiddleware (middleware.go:671) already trusts X-Forwarded-Proto for HSTS, so the codebase is internally inconsistent about proxy awareness.

**Failure scenario:** Deploy the controller on Kubernetes behind an nginx ingress that terminates TLS (the canonical K8s-native topology) with APEXLOAD_ALLOWED_ORIGINS unset. The dashboard loads fine (GETs are safe methods) but every mutation — create test, start test, save script — returns 403 'request origin is not allowed', and live telemetry websockets fail the upgrade. The platform appears read-only; the operator must discover that the fix is adding their own external origin to the allow-list, which nothing in the error message suggests.

**Suggested fix:** In evaluateOriginPolicy, honor X-Forwarded-Proto (gated on the same trusted-proxy conditions used elsewhere, or at minimum when r.TLS == nil and the header says https) before comparing schemes; alternatively compare host-only for the same-origin shortcut and treat an https-Origin-over-http mismatch as same-origin when X-Forwarded-Proto=https.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V051 — CONFIRMED (medium)

evaluateOriginPolicy (pkg/api/security.go:71-78) sets requestScheme="https" only when r.TLS != nil or r.URL.Scheme is set (always empty on server requests; no rewrite anywhere in pkg/api). Behind a TLS-terminating proxy, Origin "https://host" fails strings.EqualFold(parsed.Scheme, "http") at line 78; with no allow-list, lines 82-101 return not-allowed. corsMiddleware then 403s every non-safe method (middleware.go:492-495 — browsers send Origin on all POSTs, even same-origin), and the WS upgrade uses the same policy (websocket.go:66-75). Inconsistency confirmed: HSTS (middleware.go:671) and powerbi_publish.go:69 both trust X-Forwarded-Proto. Workaround exists (APEXLOAD_ALLOWED_ORIGINS), so medium, not high.

</details>


#### 44. Bulk sync push skips the stored-link drift check and silently overwrites remote edits
**Where:** `pkg/api/github_sync_service.go:185` · **Area:** AREA-09 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

runPush fetches the file's CURRENT remote blob sha (pkg/api/github_sync_service.go:185-189) and passes it to PutFile (line 190), so the write always succeeds over whatever is on the branch. The single-script push path loads the stored GitHubScriptLink and returns 409 when remoteSHA != link.RemoteBlobSHA (pkg/api/github_handlers.go:433-442: "remote file has changed since the last sync; pull and review before pushing"). Bulk push never consults the link store before writing (it only writes links after, lines 195-210), so remote-side edits made since the last sync are clobbered without any conflict signal.

**Failure scenario:** Script X was previously pushed to repo file scripts/x.json (link.RemoteBlobSHA=S1). A teammate edits scripts/x.json directly on GitHub (blob now S2). An operator runs a bulk sync push job for the project. runPush reads the current sha S2 and PutFiles the local envelope over it; the teammate's edits disappear from the branch tip with no conflict reported — the job records Updated=1, status complete. Pushing the same script through the per-script endpoint would have returned 409. The commit is recoverable from git history but the overwrite is silent.

**Suggested fix:** Before PutFile in runPush, load the script's GitHubScriptLink; if a link exists with a RemoteBlobSHA/LastCommitSHA and the live remote sha differs, count a Conflict for that script (mirroring the single-push 409 semantics) instead of overwriting; optionally add a force flag on the job request.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V055 — CONFIRMED (medium)

Bulk push path: `pkg/api/github_sync_service.go` runPush (150-214) fetches the file's CURRENT remote sha (185-189) and passes it straight to PutFile (190), so the write always succeeds over whatever is on the branch; the link store is only written AFTER the overwrite (195-210) and is never consulted before it. By contrast the single-script push handler loads the stored link and returns 409 on drift (`pkg/api/github_handlers.go:433-442`, message "remote file has changed since the last sync; pull and review before pushing"). runPush is reachable via the sync-job handler (`pkg/api/github_enterprise.go:711-712`, kind="push"). Remote edits made since last sync are silently clobbered by a bulk job that reports Updated/complete. Recoverable from git history, so medium stands.

</details>


#### 45. Browser-recording list summaries and WS broadcasts always report 0 network requests, 0 web
**Where:** `pkg/api/browser_recording_manager.go:1176` · **Area:** AREA-10 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

summaryLocked (pkg/api/browser_recording_manager.go:1176-1203) copies NetworkRequestCount, WebSocketCount, HARAvailable, and StorageStateReady from the session struct, but nothing ever writes those struct fields — they are only computed on the fly onto a clone in fullSnapshot (1217-1226; the only assignments in the package). List (handlers_browser_recorder.go:332-339) and every broadcast (broadcast at browser_recording_manager.go:1108-1124) use summary(), so GET /browser-recorder/sessions and all browser_recording_* WebSocket messages permanently carry zeros/false for these fields even for stopped sessions holding a HAR and hundreds of network events. The dashboard renders this: RecordingStudio's "Recent Browser Recordings" list shows session.network_request_count from listBrowserRecordings (dashboard/src/components/RecordingStudio.tsx:1106,1112,2805).

**Failure scenario:** Record a 300-request browser session with HAR capture on, stop it. GET /api/v1/browser-recorder/sessions returns network_request_count:0, websocket_count:0, har_available:false for that session; the Recording Studio recovery list shows "0 requests", so an operator reasonably concludes the capture failed and deletes a recording that actually contains a full HAR; the browser_recording_stopped broadcast likewise tells WS consumers no HAR exists.

**Suggested fix:** Fold artifact-derived stats into the session struct when finishStop/forceStopSession install session.final (and on EventNavigation/network events during recording), or make summary() consult the artifact the way fullSnapshot does for terminal sessions.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V061 — CONFIRMED (medium)

Chain, all at snapshot HEAD dc05008ab:

1. `summaryLocked` (pkg/api/browser_recording_manager.go:1176-1203) copies `NetworkRequestCount` (1191), `WebSocketCount` (1192), `HARAvailable` (1200), `StorageStateReady` (1201) from the session struct.
2. Grep over the whole snapshot (non-test): the ONLY assignments to those four fields are onto the clone in `fullSnapshot` (1219-1220, 1225-1226) and pass-through copies in pkg/api/browser_recording_sanitize.go:34-44. The live event path `handleEvent` (996-1058) increments StepCount/NavigationCount/WarningCount/MarkerCount but never the four fields; `finishStop` (885-917) installs `session.final` without folding artifact counts, then broadcasts `browser_recording_stopped` via `broadcast` (1108-1124) which calls `session.summary()`. `forceStopSession` (942) broadcasts via summary too. So summaries permanently carry 0/0/false/false.
3. `List` (334-353) returns `session.summary()`; handler pkg/api/handlers_browser_recorder.go:334 exposes it as GET sessions list.
4. Dashboard renders it: dashboard/src/components/RecordingStudio.tsx:1106 (listBrowserRecordings) and :2805 `{session.network_request_count || 0} requests`.

Mitigations found: single-session GET/stop responses use `fullSnapshot` (correct), and the list still shows a nonzero step count — so "operator concludes capture failed and deletes it" is overstated; the misreporting itself is real. Prior item W44 (docs/reviews/WEEK_REVIEW_2026-07-02_to_2026-07-07.md:442) is late graceful-stop artifact recovery — a different defect, not a regression.

</details>


#### 46. Every browser-replay progress broadcast embeds the full accumulated event list (O(n^2) CPU
**Where:** `pkg/api/browser_replay_manager.go:1112` · **Area:** AREA-10 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

BrowserReplayManager.broadcast (pkg/api/browser_replay_manager.go:1108-1132) builds message["session"] from session.snapshot(); snapshotLocked (1161-1166) deep-clones every element of session.Events (headers maps, console/network entries, DOM summaries via cloneBrowserReplayEvent at 1225-1263). handleReplayProgress (1045-1070) appends each new event to session.Events and then broadcasts, so the k-th progress event clones and JSON-marshals all k prior events (hub.BroadcastJSON marshals synchronously, pkg/api/websocket.go:292-299). The marshal runs inside the engine's OnProgress callback (blocking the replay between actions) and inside m.mu.RLock via withCurrentWorkerSession (1007-1026), while Get/Stop/Pause paths take the full m.mu.Lock (794, 827) and queue behind it.

**Failure scenario:** A browser replay of a converted recording with ~1,000 replayed actions/events: event 1,000's WebSocket message carries a session payload with all 1,000 deep-cloned events; cumulative work is ~500k event clones plus ~n^2/2 bytes serialized per connected WS client (easily hundreds of MB for event payloads a few KB each). The replay itself slows progressively because the marshal runs in the OnProgress path between actions, and REST polls of GET /browser-replay/sessions/{id} stall behind the broadcast's m.mu.RLock whenever a writer is queued.

**Suggested fix:** For type browser_replay_event, broadcast a slim session summary (status, indices, counters) plus only the new event — the event is already sent as message["event"] — and let clients that missed messages re-fetch via GET (getForProject already returns the full event list). Alternatively cap Events in the broadcast payload to the last N.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V059 — CONFIRMED (medium)

Full causal chain established at HEAD (snapshot dc05008ab):

1. Production wiring: server.go:420 constructs NewBrowserReplayManager(hub) with the live WS hub.
2. Per-action emission: pkg/engine/replay.go:998 (and :521 for finalizations) publishes a synchronous ReplayProgressEventRun per replayed action; publishReplayProgress (replay.go:2089-2094) is a bare synchronous callback.
3. pkg/api/browser_replay_manager.go:386-388 wires OnProgress → handleReplayProgress, so the work below runs inline in the replay hot path.
4. handleReplayProgress (1045-1070) appends the cloned event to session.Events (1056-1062, no cap anywhere — only reset at :170) then calls m.broadcast (1065), all inside withCurrentWorkerSession which holds m.mu.RLock (1011-1012).
5. broadcast (1108-1132): sessionPayload := session.snapshot() (1112) → snapshotLocked (1140-1168) deep-clones EVERY accumulated event (1161-1166) via cloneBrowserReplayEvent (1225-1263: headers/console/network/DOM/snapshot maps). Payload embedded as message["session"] (1119); Events carries JSON tag `events,omitempty` (struct line 46) so it serializes on the wire.
6. hub.BroadcastJSON marshals synchronously in the caller (pkg/api/websocket.go:292-298).
7. getForProject/pauseForProject take full m.mu.Lock (794, 827), queueing behind the RLock-held broadcast — matching the lock-contention claim.

So event k clones+marshals all k prior events → O(n²) cumulative CPU/bytes; a ~1,000-event replay is realistic. No throttle, cap, or degrade path found.

Prior-report check: UNCOMMITTED_REVIEW items 11/12 addressed lock placement of the completion broadcast and lease release — different defects; this payload-growth issue is distinct, not a regression of a fixed item.

</details>


#### 47. Synchronous classic replay endpoint exceeds the server's fixed 60s WriteTimeout and loses 
**Where:** `pkg/api/handlers_replay.go:172` · **Area:** AREA-10 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

handleReplayRequest (pkg/api/handlers_replay.go:121-217) runs engine.ReplayScript inline and only writes the JSON report when the replay finishes (writeJSON at 210). The main API server sets a fixed WriteTimeout of 60s (pkg/api/server.go:562), and unlike artifact handlers this path never calls extendArtifactWriteDeadline (used at e.g. handlers_recorder.go:408). Go arms the connection write deadline when the request header is read, so any replay whose wall time exceeds ~60s completes server-side but the response write fails.

**Failure scenario:** POST /api/v1/scripts/{id}/replay with body {"ignore_think_time": false} on a recorded script carrying 90s of cumulative think time (or any replay against a slow environment taking >60s): the replay runs to completion, self-heal repository writes at line 200 are applied, then writeJSON fails against the expired connection deadline — the caller (CI or dashboard) sees a connection reset with no report and no replay_session_id it can use, and a natural retry re-executes the entire replay against the target system.

**Suggested fix:** Call an extendReplayWriteDeadline (http.ResponseController.SetWriteDeadline) at the top of handleReplayRequest sized to the replay budget, or document/enforce a replay wall-clock budget below the server timeout and return 504 with the replay_session_id so the WS stream remains usable.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V060 — CONFIRMED (medium)

Chain, all hops verified in snapshot (HEAD dc05008ab):

1. Production wiring: `POST /api/v1/scripts/{id}/replay` → `handleReplayScript` (pkg/api/server.go:876) → `handleReplayRequest` (pkg/api/handlers_replay.go:118,121), served by `s.httpServer.Serve` (server.go:1341).
2. Server has fixed `WriteTimeout: 60 * time.Second` (server.go:562); Go arms the per-request write deadline before the handler runs, so a handler exceeding ~60s cannot flush its response.
3. Replay runs inline with no wall-clock cap: `executionOperationContext(context.Background())` (handlers_replay.go:156) only adds leadership-epoch cancellation, no deadline (runner.go:1372-1380); `replayScriptDefinitionContext` → `engine.ReplayScript` (script.go:905, pkg/engine/replay.go:270) — no timeout; grep found no `http.TimeoutHandler` in pkg/api.
4. No deadline extension on this path: `extendArtifactWriteDeadline`/`SetWriteDeadline` appear only in archive/recorder/downloads/bruno/browser_recorder handlers (handlers_archive.go:266 et al.), never handlers_replay.go; `writeJSON` (handlers.go:1569-1573) is a plain encoder.
5. Trigger realistic: `ignore_think_time:false` is honored (handlers_replay.go:152-155); think-time-honoring or slow-target replays routinely exceed 60s. Self-heal repo writes land first (line 200), then `writeJSON` (210) fails against the expired deadline — report and session ID lost; retry re-executes the replay.

Mitigations keeping it medium: default ignoreThinkTime=true; WS progress broadcast (line 188) and caller-supplied ReplaySessionID partially recoverable. Prior items 15/34 (reserveReplaySessionID / lease renewal) are unrelated concerns — not a regression.

</details>


#### 48. Proxy recorder silently drops requests once caps are hit — no warning, counter, or event
**Where:** `pkg/recorder/recorder.go:1251` · **Area:** AREA-10 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

recordRequestWithOptions returns 0 without any signal when len(s.Requests) >= maxRecordedRequests (pkg/recorder/recorder.go:1251-1253) and when the per-session byte budget is exceeded (1358-1361); appendWebSocketMessage silently stops at its frame/byte caps (1403-1407). recorder.Session has no warnings field and no dropped counter, no SessionEvent is emitted, and the converter's warnings (pkg/recorder/converter.go) only cover correlation/static/websocket cases — so a capped recording converts to a script and exports a HAR that simply end mid-flow with zero indication anywhere in the API or UI.

**Failure scenario:** An operator records a chatty SPA (background polling every second) over a long workflow and crosses 10,000 captured requests; the final login/checkout steps of the workflow are silently absent from the session. ConvertSessionWithOptions produces a script missing those endpoints and ExportHAR omits them, with no warning in the conversion response, the session JSON, or the UI — the operator only discovers the gap when replay of the generated script never reaches the tail of the flow.

**Suggested fix:** Add DroppedRequests/DroppedWebSocketMessages counters (and a boolean truncated flag) to Session, increment them on every capped return, emit one warning-style SessionEvent on first drop, and surface the counters in sanitizeRecorderSessionForResponse plus a conversion warning in ConvertSessionWithOptions.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V062 — CONFIRMED (medium)

pkg/recorder/recorder.go:1251-1253 returns 0 when `len(s.Requests) >= maxRecordedRequests` (10,000; :35), and :1358-1361 returns 0 when the request exceeds `maxRecordedSessionBytes` (512MiB; :39) headroom — no counter increment, no event, no field set. appendWebSocketMessage (:1404-1407) likewise returns silently at frame/byte caps. Session struct (:113-135) has no warnings/dropped/truncated field; only per-body truncation flags exist. Converter warnings (pkg/recorder/converter.go:65-203) cover correlation/static/WS-metadata only — none reflect cap truncation, so scripts/HAR end mid-flow with zero indication. Prior-report check: PERF_REVIEW_2026-07-06 item 32 was unbounded RAM retention; these caps are its remedy, and the missing signaling is a new follow-on gap, not a regression.

</details>


#### 49. Live import pays for a discarded classification pass, including a duplicate full runtime-r
**Where:** `pkg/convert/enterprisearchive/java_import.go:165` · **Area:** AREA-11 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

ConvertJava (java_import.go:152-224) calls classifyJavaArchive (java_classify.go:140) whose result is stored in state.profile (java_import.go:181) and never read on the live path — the only consumer of state.profile is the test-only JMS lane (java_idiom_registry.go:120). classifyJavaArchive internally calls javaProgramHasUnknownLR (java_classify.go:209-211), which runs a complete second buildJavaVuserRuntimeReachability(program) in addition to ConvertJava's own build at java_import.go:166, and javaExecutableEvidence/javaLooksLike* perform full protocol-receiver scans. Separately, newJavaEvidenceIndex(program) — which rescans every class source with javaCodeOnly for nested-type indexing (java_classify.go:1216-1218) — is constructed ~9 times per import (java_import.go:162, 166 via builder, 1472 guard, 4794 count, 4871 mappings, 5129 mapping index, 330 runner sources, 508 inheritance blockers, java_classify.go:1064 evidence).

**Failure scenario:** Import a large Java archive near the governed budgets (32 MiB source, ~65k statements, overload work near the 2M-comparison ceiling): the archive's reachability/overload analysis executes twice and the per-class source scans execute ~9 times, roughly doubling-or-worse import latency for zero output difference, since profile, review families, and the unknown-LR flag never influence the combined draft, its stats, or its blockers.

**Suggested fix:** Drop the classifyJavaArchive call from ConvertJava (or gate it behind the lanes that consume it), and thread one shared *javaEvidenceIndex plus the single runtimeReachability through mappable-count, mapping emission, mapping-index, runner-source, and inheritance-blocker passes.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V067 — CONFIRMED (medium, performance)

Live entry: zip.go:229 calls ConvertJava. When overloadWork.blocker=="" (the normal case), java_import.go:165 runs classifyJavaArchive and :166 runs buildJavaVuserRuntimeReachability. The profile is stored once at java_import.go:181 (`state := &javaImportState{... profile: profile}`) and the local is never used again in ConvertJava.

Discarded result: grep of the package shows the ONLY non-test read of `state.profile` is java_idiom_registry.go:120 (mapJavaJMSStatement). That mapper runs only via javaStatementMapperRegistry at java_import.go:5506 inside convertJavaStatement, whose sole non-recursive caller is java_import.go:706 inside convertJavaStatementLane — which has no production caller (only java_native_analysis_test.go:146; mergeJavaLaneResults likewise only java_native_analysis_test.go:161). So classification never affects live output.

Duplicate pass: classifyJavaArchive (java_classify.go:175) calls javaProgramHasUnknownLR, which at java_classify.go:210 runs a full buildJavaVuserRuntimeReachability — no memoization (fresh builder + newJavaEvidenceIndex, java_import.go:2484-2507) — duplicating ConvertJava's own build at :166. classifyJavaArchive also pays javaExecutableEvidence (java_classify.go:141 → :1064 index build).

Index rebuilds: 9 non-test newJavaEvidenceIndex sites (java_import.go:162, 330, 508, 1472, 2489, 4794, 4871, 5129; java_classify.go:1064), each rescanning every class with javaCodeOnly + regex (java_classify.go:1208-1218); callers at java_import.go:141/156/186/192/206/252/479/1438 are all on the live path.

Prior-report check: UNCOMMITTED_REVIEW_2026-07-22.md items 5 (lifecycle-scan false blockers) and 42 (fidelity counters) are correctness issues, unrelated — not a regression.

Mitigant noted: overload guard (java_import.go:164) fails closed before the expensive passes, so duplication applies only to archives under the 2M-comparison ceiling — exactly the large-but-admitted archives the scenario describes. Severity medium stands.

</details>


#### 50. splitJavaTopLevel treats comparison/shift operators as generics, merging call arguments
**Where:** `pkg/convert/enterprisearchive/java_model.go:534` · **Area:** AREA-11 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

splitJavaTopLevel (java_model.go:502-550) increments angleDepth for any '<' whenever findJavaGenericAngleEnd (java_model.go:552-592) finds a later '>', so an argument list like 'count < MAX, retries > 0' (or 'a << 2, b >> 3') is returned as ONE argument instead of two. javaCallArguments (java_import.go:1360-1377) feeds this into javaResolvedEvidenceHelperCalls (java_classify.go:4104-4119) and resolveConstructions (java_import.go:4480-4509), where javaMethodAcceptsArgumentCount (java_import.go:2063-2070) then rejects the real 2-parameter helper/constructor by arity.

**Failure scenario:** Actions.java: action() calls 'doWork(count < MAX_COUNT, retries > 0);' where private void doWork(boolean, boolean) contains lr.start_transaction/lr.end_transaction. The merged single argument makes javaHelperOverloadsForArguments return zero candidates, so the reachability builder never walks doWork: javaProgramTransactionStats (java_import.go:4546-4586) reports no transactions (the draft's emits_business_transactions metadata stays false, so the engine keeps the automatic iteration transaction while the real script also emits Checkout at runtime — double-counted transactions), recordJavaProgramTransactionNames misses the declared name, and javaVuserRunnerSemanticBlockers (java_import.go:5010-5053) fails to flag review-only lr.* calls inside doWork, weakening the promotion gate (fail-open). The mirror case 'new Helper(a < b, c > d)' hits java_import.go:4503-4509 and raises a FALSE 'did not resolve to exactly one archived constructor' promotion blocker (fail-closed nuisance).

**Suggested fix:** In splitJavaTopLevel, only enter angle-tracking when the '<' can open a generic type argument: previous non-space byte is an identifier byte and the candidate span between '<' and the matched '>' contains only type-argument-legal characters (identifiers, dots, commas, whitespace, ?, extends/super, nested <>, []). Otherwise treat '<' as an operator. Add the same guard for '<<'/'>>' by skipping doubled brackets.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V066 — CONFIRMED (medium)

java_model.go:533-536: on `<` at depth 0, angleDepth++ whenever findJavaGenericAngleEnd finds any balanced later `>`; findJavaGenericAngleEnd (552-592) only counts brackets, no type-position check. Traced `count < MAX, retries > 0`: the comma is consumed at angleDepth==1, so splitJavaArgs returns ONE merged argument; `a << 2, b >> 3` likewise. javaCallArguments (java_import.go:1360-1377) returns this list; javaHelperOverloadsForArguments filters via javaMethodAcceptsArgumentCount (java_import.go:2063-2070, exact ParameterCount match), so a real 2-param helper yields zero candidates and addCandidates (java_classify.go:4043-4073) appends nothing — the reachability walk misses the helper (fail-open for transaction stats/semantic blockers). Constructor mirror proven at java_import.go:4503-4509: len(constructors)!=1 raises the false "did not resolve to exactly one archived constructor" blocker.

</details>


#### 51. Registrations after the last request are silently dropped with overcounted stats and no re
**Where:** `pkg/convert/enterprisearchive/converter.go:802` · **Area:** AREA-12 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

pendingExtractors/pendingAssertions/pendingHeaders/pendingSourceMappings are attached only when a subsequent request converts (converter.go:655-738); the loop ends at converter.go:802 with no flush, so a web_reg_save_param/web_reg_find/web_add_header after the final request vanishes entirely. Stats.Extractors and NativeFunctions were already incremented at registration time (converter.go:272-273, recordSourceFunctionSupport Executable), so the fidelity report overstates conversion, and the queued source-review mapping is lost, violating the package's own no-silent-drop discipline.

**Failure scenario:** A common LR cross-iteration correlation pattern — web_reg_save_param at the end of Action.c capturing a token consumed at the top of the next iteration — imports with the extractor silently discarded. The earlier request still references {{var.next_token}}, which never gets populated; replay sends an empty/unresolved token and the correlation failure has no importer-side explanation because the registration is absent from the review table.

**Suggested fix:** After the loop, flush leftover pending items: either attach them to the first request of the same phase (LR next-iteration semantics) with an explanatory note, or emit review_only mappings plus a warning and decrement the optimistic stats.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V076 — CONFIRMED (medium)

pendingExtractors/pendingAssertions/pendingHeaders/pendingSourceMappings are declared at converter.go:204-209 and consumed ONLY inside the converted-request branch: extractors/assertions/headers attach at converter.go:655-676, queued mappings flush at converter.go:722-738. Grep over converter.go shows no other reference; the function loop ends at converter.go:802 and post-loop code (804-817, 844) never flushes leftovers. Meanwhile web_reg_save_param increments Stats.Extractors/ContributorFunctions and records Executable support at registration time (converter.go:271-273; headers likewise at 357-363). So a registration after the last converted request is dropped with no warning, no source-review row (its mapping dies in pendingSourceMappings), and overstated stats. pruneUnusedWholeBodyExtractors (converter.go:844) only adjusts attached whole-body extractors, not this. Chain holds.

</details>


#### 52. Parameter and data-source emission order is nondeterministic; shared data-file config is f
**Where:** `pkg/convert/enterprisearchive/parameters.go:33` · **Area:** AREA-12 · **Category:** determinism · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

ParseParameterFile ranges over the sections map (parameters.go:33), so script.Parameters and script.DataSources order differs between imports of the identical archive. When two parameter sections reference the same data file with different delimiter/column settings, dataSourceByFile dedupes by basename (parameters.go:126-133) and whichever section the map iteration visits first defines the DataSource's delimiter/columns — a nondeterministic tie-break that can select the wrong delimiter for the other parameter.

**Failure scenario:** Re-importing the same archive produces scripts whose parameters/data_sources arrays are ordered differently (spurious diffs in review and versioning). With two .prm sections pointing at the same data.dat but declaring delimiter="," and delimiter="\t", some imports parse the file as CSV and others as TSV, changing every resolved column value fed to runs.

**Suggested fix:** Sort section names before iterating and make the shared-data-file tie-break deterministic (or emit a warning when two sections declare conflicting delimiters for one file).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V077 — CONFIRMED (medium)

ParseParameterFile iterates `for sectionName, values := range sections` (parameters.go:33) over the plain map returned by ParseConfigSections (parser.go:937-983, `map[string]map[string]string`, no key ordering). Parameters/DataSources are appended in that randomized order and the sole caller appends them verbatim with no sort (zip.go:545-555); `grep sort.` finds nothing in parameters.go. Shared-file tie-break: dataSourceByFile is keyed by lowercase basename and first-seen-wins (parameters.go:126-131) — buildParameterDataSource runs once, baking that section's delimiter into DataSource.Delimiter/columns (parameters.go:201-219). Two sections referencing one data file with different delimiters therefore get a map-iteration-order winner, and array order differs between identical imports. Chain holds.

</details>


#### 53. Legacy fallback scanner silently truncates: bufio ErrTooLong unchecked and in-string ");" 
**Where:** `pkg/convert/enterprisearchive/parser.go:310` · **Area:** AREA-12 · **Category:** error-handling · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

parseActionFileLegacy uses a default bufio.Scanner (64KiB token cap) and never checks scanner.Err() before returning nil error (parser.go:240-310), so a single source line over 64KiB stops the scan mid-file and every later function is dropped; ParseConfigSections/ParseUSRFile deliberately raise this limit for exactly this reason (parser.go:941-949) but the action-file scanner does not. Additionally the block terminator check strings.Contains(trimmed, ");") at parser.go:296 fires inside string literals, so a body line containing ");" truncates that call's parsed arguments.

**Failure scenario:** A VuGen script with a vendor macro (AST parse fails, normal per the fallback design) and a recorded one-line 100KB JSON body early in Action.c: the legacy scanner recovers only the functions before that line and returns success; the remaining ~30 requests are absent from the draft. The only signal is the generic 'syntax_error' parse gate from the AST failure, which misattributes the loss and gives the reviewer no hint that recovery itself was truncated. If the AST parse succeeds with zero functions while legacy truncates, there is no gate at all.

**Suggested fix:** Raise the scanner buffer as ParseConfigSections does, and record a distinct CSourceParseFailure kind (e.g. line_too_long / scan_truncated) when scanner.Err() != nil; make the block terminator string-aware.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V078 — CONFIRMED (medium)

parseActionFileLegacy builds a default bufio.Scanner (parser.go:240) with no Buffer() call — 64KiB token cap — and returns `parsed, nil` (parser.go:310) without ever checking scanner.Err(), so ErrTooLong on one long line silently ends the scan and drops all later functions. Contrast: ParseConfigSections deliberately raises the limit for this exact reason (parser.go:939-949). Legacy is the designated fallback whenever cc.Parse rejects the file (parser.go:170-201, comment cites vendor macros); partial recovery returns success (parser.go:199-201), and when astErr==nil with zero AST functions no syntax_error gate is added at all (gate only appended under astErr!=nil, parser.go:184-188). Terminator check `strings.Contains(trimmed, ");")` (parser.go:296) runs on comment-stripped raw text — stripCComments preserves string-literal contents — so a `);` inside a recorded body string truncates that call. Chain holds.

</details>


#### 54. findFile basename fallback picks an arbitrary file when basenames collide
**Where:** `pkg/convert/enterprisearchive/zip.go:1430` · **Area:** AREA-12 · **Category:** determinism · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

findFile's final fallback ranges over the files map matching by basename (zip.go:1430-1435) and returns the first hit, so with two archive entries sharing a basename (e.g. data/users.dat and backup/users.dat) the parameter-file and data-file resolution (zip.go:541, parameters.go:125, runtime_credentials.go:831) selects a nondeterministic winner per import, unlike findRuntimeArchiveFile which fails closed on ambiguity (zip.go:1494-1505).

**Failure scenario:** An archive contains users.dat at the root and an older backup/users.dat. Some imports of the same archive inline the stale backup rows into the data source, so runs consume different test data depending on which import happened to win the map iteration — with no warning that the reference was ambiguous.

**Suggested fix:** Sort candidates and either prefer the shallowest/exact-directory match deterministically or surface an ambiguity warning, mirroring findRuntimeArchiveFile.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V079 — CONFIRMED (medium)

findFile (zip.go:1418-1437): after a direct hit (1421) and a case-insensitive full-path scan (1424-1428), the basename fallback at zip.go:1431-1435 ranges over the unordered `files` map and returns the first entry whose basename matches — with `users.dat` and `backup/users.dat` both present, the winner is Go map-iteration random per import, and no ambiguity warning exists. Contrast standard confirmed: findRuntimeArchiveFile sorts candidates (zip.go:1465-1468) and fails closed on >1 basename matches (zip.go:1499-1505), proving colliding basenames are an anticipated archive shape. Affected callers verified: parameter-file content lookup (zip.go:541) and data-file resolution feeding DataSource.InlineData (parameters.go:125,134), so nondeterministically stale data rows can be inlined silently. Chain holds.

</details>


#### 55. Native inspection re-parses YAML O(requests x ancestors x passes) and runs the full candid
**Where:** `pkg/convert/bruno/inspect.go:167` · **Area:** AREA-13 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

Inspect in native mode executes nativeRequestScripts twice (inspect.go:168 discards everything but requirements; inspect.go:239 rebuilds candidates), each call re-validates every YAML file (validateNativeYAMLFiles parses each file twice at native.go:376-386, also run at inspect.go:58), and parses folder/collection layers per request with no memoization (native.go:295-311 parses each ancestor folder.yml once per request; validateNativeEffectiveCredentialShapes at native.go:150-199 does the same full pass again). classifyFileRole (bundle.go:346-367) additionally runs yaml.Unmarshal on every request-shaped .yml each time it is consulted, and it is consulted per file in sanitizeFiles, sanitizeNativePolicyHeaders, sanitizeNativeJSONBodies, sanitizeNativeXMLBodies, sanitizeNativeURLCredentials, inspectCollection, buildManifest (twice), validateNoUnprojectedNativeCredentialTemplates, and both validateNativeYAMLFiles passes — roughly 15-20 full parses per request file plus ~3 x requests x depth folder parses per import.

**Failure scenario:** Uploading a real OpenCollection archive with ~3,000 request files nested 3-4 folders deep (each folder.yml carrying vars/headers) makes POST /import inspect perform on the order of 60k-100k redundant yaml.Unmarshal calls (3k x 4 ancestors x 3 passes for folders plus ~15-20 parses per request file), turning an import that could be a few hundred milliseconds into tens of seconds of CPU on the controller and risking client/API timeouts; memory churn doubles because the entire sanitize-project-finalize pipeline runs twice per candidate.

**Suggested fix:** Parse each file once into a shared per-Inspect cache (path -> parsed layer / role / yaml.Node) threaded through nativeRequestScripts, validateNativeEffectiveCredentialShapes, and the sanitize passes; short-circuit the second nativeRequestScripts call when the first pass produced no projected variable requirements (files unchanged), and cache classifyFileRole per (path,hash).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V083 — CONFIRMED (medium)

Chain (all snapshot, repo-relative):
1. Reachable: pkg/api/handlers_bruno_import.go:227-251 handleInspectBrunoImport → brunoconvert.Inspect; native_requests mode wired (line 268); flag defaults enabled (line 228).
2. Double build: inspect.go:167-179 first nativeRequestScripts (only requirements kept) and inspect.go:238-250 second full call — both unconditional in native mode, so buildNativeCandidate pipeline runs 2x per request.
3. validateNativeYAMLFiles runs 3x (inspect.go:58 + native.go:211 inside each nativeRequestScripts); each pass calls classifyFileRole twice per file (native.go:376,384) plus yaml.Node unmarshal (native.go:391-393).
4. Per-request ancestor folder re-parse, no memoization: native.go:178-192 and native.go:295-309 (latter x2); parseNativeLayer unmarshals fresh every call (native.go:589-593). Grep for cache/memo in pkg/convert/bruno: none.
5. classifyFileRole falls through to isBrunoRequestContent yaml.Unmarshal per call (bundle.go:362; inspect.go:1928-1929); ~12 non-test call sites confirmed by grep across sanitize/manifest/inspect passes.
6. Scale allowed: types.go:31,33 MaxFiles=100_000, MaxExpandedBytes=32MB. Amplifier: single-slot semaphore brunoInspectConcurrency (handlers_bruno_import.go:149,232-238) head-of-line blocks all other Bruno inspects during a slow one.
Bounded by 32MB expanded size and import-time-only, so medium stands. Prior items SECRETLIST-1/SANITIZE-ABORT-1 are unrelated (secret semantics, not perf) — not a regression.

</details>


#### 56. Composed URL credential scrub targets use post-http.params query indexes against the autho
**Where:** `pkg/convert/bruno/native_url_sanitize.go:321` · **Area:** AREA-13 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

rewriteNativeURLTemplates (pkg/convert/bruno/native_url_sanitize.go:298-335) assigns pointer '/http/url/query/<index>/value' and scrub target {Kind:'url_query_value', Index:<index>} from the query string AFTER applyNativeHTTPParams (pkg/convert/bruno/native.go:818-876) has removed every raw-query pair whose name is managed by an http.params entry (including disabled ones) and re-appended enabled params at the end. applyNativeProjectedVariableSecrets (pkg/convert/bruno/native_sanitize.go:143-288) later applies that index via setNativeURLQueryValue (native_url_sanitize.go:442-480) to the AUTHORED /http/url string in the source file, whose '&'-split positions still include the removed pairs, so the index addresses the wrong pair whenever a managed/disabled pair precedes the credential pair in the authored query.

**Failure scenario:** Native import (output_mode=native_requests) of a request file with http.url 'https://api.test/login?debug=1&token=abc-{{tokenSuffix}}', http.params [{name: debug, type: query, value: '1', disabled: true}], and vars tokenSuffix: '12345'. Candidate-time query becomes 'token=abc-{{tokenSuffix}}' (index 0), so the composed-credential requirement records url_query_value Index 0. During Inspect's variable scrub, setNativeURLQueryValue rewrites index 0 of the AUTHORED query — 'debug=1' — so the stored sanitized source bundle becomes '?debug={{secret.LOGIN_QUERY_TOKEN_x}}&token=abc-{{tokenSuffix}}': the innocuous debug parameter now carries the token secret reference while the real credential pair keeps its authored composition. The manifest, collection view, and durable protected artifact are all rebuilt from these corrupted bytes and import succeeds silently. If the mis-indexed authored pair happens to have no '=' (e.g. '?flag&token=...'), the same flow instead hard-fails the whole import with 'URL query scrub index 0 has no value'. A secondary effect: the requirement ID/name embed the shifted index, so adding or removing an unrelated http.params entry churns the stable identity of an untouched query credential.

**Suggested fix:** Record scrub targets for raw-query credentials in the AUTHORED index space: compute the authored-parts index (or carry the authored offset through applyNativeHTTPParams by tagging surviving parts with their source index) and use that for both the '/http/url/query/N/value' identity and the url_query_value scrub Index. Alternatively, make setNativeURLQueryValue match by decoded pair name + occurrence rather than positional index.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V082 — CONFIRMED (medium)

Chain verified. pkg/convert/bruno/native.go:1537-1547: rawTarget passes through applyNativeHTTPParams BEFORE rewriteNativeURLTemplates. native.go:805-807/819-822: paramNames includes every query-type http.params entry (Disabled checked only at 840, not when building paramNames); lines 824-838 drop authored raw-query pairs with managed keys; 839-876 re-append enabled params at the end. native_url_sanitize.go:298-334: query index is computed on this composed string and recorded as scrub target {Pointer:"/http/url", Kind:"url_query_value", Index:index}. native_sanitize.go:226-248 applies the plan to the source file's document (authored /http/url, unmarshalled from file.Data), and setNativeURLQueryValue (native_url_sanitize.go:464-475) rewrites parts[index] of the AUTHORED query positionally; inspect.go:180-207 then rebuilds bundle/manifest/collection from the corrupted bytes. Requirement flows through nativeProjectedVariableRequirements (native_sanitize.go:31-33) because scrubTargets is non-empty. A managed/disabled pair preceding the credential pair shifts the index → wrong pair scrubbed, or hard-fail at native_url_sanitize.go:469-471 when the mis-indexed pair has no '='. No test mixes http.params with raw-query credentials.

</details>


#### 57. Empty-body captured events get a message_received assertion that deterministically fails
**Where:** `pkg/convert/eventhubscapture/eventhubscapture.go:128` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Convert counts empty-body capture records (eventhubscapture.go:94-96, warned at 141-143) but still attaches Assertions=[{Type: message_received}] to every produce action unconditionally (line 128). The eventhubs runtime evaluates produce assertions against the sent event (pkg/protocol/eventhubs/eventhubs.go:244 evaluateAssertions(action.Assertions, result, ev) where ev is the produced event), and message_received fails on len(ev.Body)==0 (pkg/protocol/eventhubs/helpers.go:253-255: "assertion message_received failed: empty event"). So the send succeeds but the action is scored failed.

**Failure scenario:** Import a Capture Avro file containing properties-only events (empty Body is legal in Event Hubs and appears in real captures; the importer even counts and warns about them). Each such event becomes an eventhubs_produce action whose message_received assertion fails on every iteration, in mock and real mode alike, so the imported script reports failed transactions for events that were sent successfully — inflating error rate and potentially tripping error-rate SLA gates.

**Suggested fix:** Skip the message_received assertion (or emit no assertion) for actions whose imported body is empty; alternatively use an assertion that verifies the send outcome rather than body non-emptiness for produce actions.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V089 — CONFIRMED (medium)

pkg/convert/eventhubscapture/eventhubscapture.go counts empty bodies (94-96) and warns (141-143) but attaches `Assertions=[{Type:"message_received"}]` to every produce action unconditionally (line 128); empty body yields action.Body="" (line 116). At runtime, EventHubs.produce (pkg/protocol/eventhubs/eventhubs.go:220-244) builds the outgoing event via buildEvent (392-423, Body=action.Body verbatim), sends it successfully, then returns evaluateAssertions(action.Assertions, result, ev) on the SENT event (line 244). evaluateAssertions (helpers.go:250-256) fails message_received when len(ev.Body)==0: "assertion message_received failed: empty event". So legally-empty properties-only capture events send fine but score failed every iteration. Chain fully verified; confirmed as claimed.

</details>


#### 58. Wildcard response keys like "2XX" parse as status 2 and emit an always-failing assertion
**Where:** `pkg/convert/openapi/converter.go:2426` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

firstSuccessStatus (pkg/convert/openapi/converter.go:2420-2437) accepts any 3-byte key starting with '2' and parses it with fmt.Sscanf(key, "%d", &status). Sscanf does not require consuming the whole input, so the OpenAPI 3.x wildcard range key "2XX" (and "2xx") yields status=2 with a nil error (verified empirically: key="2XX" -> n=1, err=nil, status=2). buildAction (line 1346-1350) then emits Assertion{Type: status_code, Expected: 2}, and successResponseSchema (replay_helpers.go:30-37) looks up responses["2"], which misses.

**Failure scenario:** Import an OpenAPI 3.x spec whose operations declare only wildcard response ranges (responses: {"2XX": ...}) — a shape emitted by several spec generators. Every converted action carries assertion status_code Expected=2; at replay the real status (200/201/...) never equals 2, so every request in the script is scored as a failed transaction on every run even though the server behaved correctly.

**Suggested fix:** In firstSuccessStatus, treat keys matching 2XX/2xx as status 200 for lookup purposes but either skip the exact status_code assertion (fall back to a 200-299 status_code_range assertion) or verify the Sscanf consumed all three digits (require key to be all digits before parsing). Also make successResponseSchema consult the wildcard key.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V088 — CONFIRMED (medium)

firstSuccessStatus (pkg/convert/openapi/converter.go:2420-2437) accepts any key with len==3 && key[0]=='2' (line 2426), then `fmt.Sscanf(key, "%d", &status)`. Sscanf does not require full-input consumption: on "2XX"/"2xx" it fills status=2 with err==nil (one verb, one operand matched), so the function returns (2, true). buildAction (converter.go:1333, 1346-1350) emits Assertion{Type:"status_code", Expected: 2}, which can never match a real 2xx response. successResponseSchema (replay_helpers.go:30-40) looks up responses["2"], missing the "2XX" entry (falls back to "default" or nil). Grep confirms no wildcard normalization anywhere in pkg/convert/openapi and firstSuccessStatus has a single caller. Every action from a wildcard-only spec fails replay. Confirmed.

</details>


#### 59. RAML converter leaves {param} URI templates unresolved and asserts status 200 on every met
**Where:** `pkg/convert/raml/converter.go:170` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

walkResource (raml/converter.go:148-193) emits actions with Target set to the raw RAML resource path — URI parameters like /users/{userId} are never substituted, parameterized, or even parsed (there is no uriParameters handling anywhere in the package), and baseUri templates like https://api.example.com/{version} likewise pass through (line 83). Every action also gets a hard-coded Assertion{Type: status_code, Expected: 200} (line 170) regardless of the method or the responses the RAML declares. Unlike every sibling importer, no requestimport finalization or import-review gate marks these actions as needing work.

**Failure scenario:** Import a typical RAML spec with /users/{userId}: get and a POST /users declaring a 201 response. The generated GET sends the literal path /users/{userId} (engine templates are {{...}}, so the braces go out on the wire) and receives a 404, and the POST that correctly returns 201 fails its hard-coded 200 assertion — every affected action fails replay with no review gate explaining why.

**Suggested fix:** Rewrite {param} segments to {{data.raml.param}} (or {{var.param}}) with a generated data source seeded from uriParameters examples/defaults, mirroring the OpenAPI templatePath approach; derive the expected status from the method's declared responses (first 2xx) instead of hard-coding 200; expand baseUri {version} from the RAML version field.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V090 — CONFIRMED (medium)

walkResource (pkg/convert/raml/converter.go:148-193) sets `Target: resourcePath` verbatim (line 166) and a hard-coded `status_code Expected: 200` assertion on every method (line 170). Grep over the package (converter.go, support.go only) finds zero hits for uriParameters, requestimport, importreview, or templatePath — no {param} substitution, no per-action review gate. baseUri passes through unexpanded (line 83). So GET /users/{userId} ships literal braces on the wire and a 201-returning POST fails its 200 assertion. Mitigation noted: support.go:17-19 statically documents "URI parameters are preserved as template literals" and the 200 assertion — but that is a support-matrix note, not an import diagnostic or gate, so replay still fails deterministically. Confirmed at medium.

</details>


#### 60. Doc-literal SOAP envelopes ignore schema targetNamespace and elementFormDefault
**Where:** `pkg/convert/wsdl/converter.go:1214` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

soapEnvelope (pkg/convert/wsdl/converter.go:1213-1222) binds the single tns prefix to FirstNonEmpty(op.BodyNamespace, doc.TargetNamespace) and renderXSDFields (pkg/convert/wsdl/converter.go:1496-1544) qualifies every body/header element, at every depth, as <tns:...>. The xsdSchema struct captures targetNamespace (line 116) and elementFormDefault (line 117) but neither field is read anywhere in the package (grep-verified). For doc-literal bindings soap:body has no namespace attribute, so tns is always the WSDL definitions targetNamespace even when the input element is declared in a schema with a different targetNamespace, and child elements are namespace-qualified even when elementFormDefault is unqualified (the XSD default). RPC and encoded operations are execution-gated (credential_projection.go:66-85) but resolved doc-literal bodies ship ungated as 'converted'.

**Failure scenario:** Import a common enterprise WSDL whose <types> schema declares targetNamespace="http://types.example.com" (different from the definitions targetNamespace) or omits elementFormDefault (unqualified default). Every generated doc-literal request wraps payload elements in the wrong namespace (or wrongly qualifies children), so on replay a validating SOAP server rejects each operation with an element-not-expected SOAP fault; with the default FaultHandling.FailOnFault=true every action in the imported script fails on every run until the operator hand-rewrites the envelopes.

**Suggested fix:** Track the declaring xsdSchema for each resolved element/type and emit its targetNamespace as the element's namespace (extra xmlns binding per schema namespace); honor elementFormDefault=unqualified by emitting local (unprefixed) child elements. Alternatively, when the element's declaring schema targetNamespace differs from the tns binding or elementFormDefault is unqualified, attach the existing wsdl_incomplete_xsd_type-style execution review gate instead of shipping the body as converted.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V087 — CONFIRMED (medium)

soapEnvelope (pkg/convert/wsdl/converter.go:1213-1222) binds the single `tns` prefix to FirstNonEmpty(op.BodyNamespace, doc.TargetNamespace, "urn:service") — doc-literal soap:body carries no namespace attr, so tns is always the WSDL definitions targetNamespace. renderXSDFields (converter.go:1496-1544) emits `<tns:name>` for every child at every depth (lines 1516, 1519, 1526, 1533, 1541) unconditionally. xsdSchema.TargetNamespace/ElementFormDefault (converter.go:116-117) are decoded but grep shows no other reference in the package — only definitions.TargetNamespace (97/637/654/1214) is used. wsdlOperationReviewFindings (credential_projection.go:53-87) gates only unresolved-binding, rpc-style, and encoded-use; resolved doc-literal bodies ship ungated. Wrong-namespace/wrongly-qualified envelopes fault on validating servers. Confirmed as claimed.

</details>


#### 61. DevWeb WebSocket/gRPC steps lose source position — grouped by verb and sorted before earl...
**Where:** `pkg/convert/devweb/converter.go:536` · **Area:** AREA-15 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

All WebSocket and gRPC steps are emitted with StartLine=block.StartLine (converter.go:550, 572, 584, 595, 618 — the FindAllStringSubmatch loops have no offsets), and each verb is scanned in a separate pass (news, opens, sends, receives, closes, invokes). sortParsedSteps orders by StartLine only (diagnostics.go:220-228), so ws/grpc steps sort ahead of HTTP requests that appear earlier in the source, and multi-exchange WS flows collapse into all-sends-then-all-receives regardless of authored order. Pure-WS scripts import as executable (no review gate covers bare method calls), so the scrambled order replays.

**Failure scenario:** DevWeb script: HTTP login request (line 12), then ws connect (line 20), send('subscribe'), receive(), send('ack'), receive(), close(). Import orders: connect, send, send, receive, receive, close, then the login request. Replay opens the socket before authenticating and sends both messages before reading any reply — the server drops the connection and every VU errors, even though the import reported all steps converted.

**Suggested fix:** Use FindAllStringSubmatchIndex for the ws/grpc patterns, derive StartLine via devwebLine(block, offset) like WebRequest does, and merge all verbs into one offset-ordered scan.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # P08 — CONFIRMED (medium)

All WS/gRPC steps carry StartLine=block.StartLine (pkg/convert/devweb/converter.go:550, 572, 584, 595, 618 — FindAllStringSubmatch, no offsets), while HTTP WebRequest steps get true lines via devwebLine (converter.go:474). sortParsedSteps (diagnostics.go:220-228) orders by StartLine only (strict >, so equal keys keep append order), placing the whole WS/gRPC cluster at the block's first line — ahead of HTTP requests authored earlier — and preserving the per-verb grouped append order (connects, then sends, then receives, then closes; converter.go:537-596), destroying authored send/receive interleaving. No review gate covers bare ws.send()/receive(): reviewOnlyPatterns (converter.go:109-140) matches assignments/control flow only, and `const ws = new load.WebSocket` is explicitly excluded (diagnostics.go:57-65), so a clean pure-WS script imports executable with scrambled order.

</details>


#### 62. Every DevWeb load.thinkTime() clobbers the same last step — only the final value survives
**Where:** `pkg/convert/devweb/converter.go:620` · **Area:** AREA-15 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The thinkTime loop (converter.go:620-628) iterates all matches and each assigns steps[len(steps)-1].Action.ThinkTime — the same 'last parsed step' index every iteration. Intermediate think times are overwritten (only the last call's value survives, attached to one step), and because ws/grpc scans run earlier, that last step is often not even the source-order predecessor of any thinkTime call. Stats.ThinkTimes still counts every occurrence, so the completeness guard (263-265) reports them as converted.

**Failure scenario:** Script: request A; load.thinkTime(3); request B; load.thinkTime(5). Source pacing = 3s after A + 5s after B. Imported script: A has no think time, B gets 5s (the 3s vanishes) — and with the 1000ms config default filling gaps, the replayed pacing is unrelated to the authored load shape while stats report 2 think times converted.

**Suggested fix:** Match thinkTime with FindAllStringSubmatchIndex and attach each value to the step with the greatest StartOffset below the match offset (after switching steps to offset tracking).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # P09 — CONFIRMED (medium)

The thinkTime loop (pkg/convert/devweb/converter.go:620-628) iterates all thinkTimePattern matches; no step is appended inside the loop, so steps[len(steps)-1] is the same fixed index every iteration — each match overwrites the previous ThinkTime and only the final value survives, attached to a single step. That target is the last-appended step from the earlier per-verb scans (often a grpc/ws step, not the source-order predecessor), and assignment happens before sortParsedSteps (630) reorders. stats.ThinkTimes++ counts every occurrence (622), so the completeness-gap formula (converter.go:263-265) subtracts all ThinkTimes and reports no gap. Script-level default fixed 1000ms (converter.go:195-197, firstPositive(opts.DefaultThinkTimeMs, 1000)) fills the wiped values. Chain holds statically.

</details>


#### 63. JMeter CSV recycle/stopThread/shareMode/ignoreFirstLine semantics dropped with no diagnos...
**Where:** `pkg/convert/jmeter/converter_csv_timers.go:34` · **Area:** AREA-15 · **Category:** silent-drop · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

parseCSVDataSet reads only filename/variableNames/delimiter/randomOrder; grep confirms 'recycle', 'stopThread', 'shareMode' and 'ignoreFirstLine' appear nowhere in pkg/convert/jmeter. recycle=false+stopThread=true (finite-data, stop-at-EOF) imports as an endlessly recycling sequential source; shareMode 'Current thread' (per-VU iterator) becomes the platform default sharing; and ignoreFirstLine=true with explicit variableNames yields FirstRowHeader=false (line 38 computes it solely from missing variableNames) so the header row replays as a data row. None of these produce a warning or needs_review disposition, violating the importer conservation rule.

**Failure scenario:** A capacity test uses a 1000-row voucher CSV with recycle=false/stopThread=true so each voucher is consumed once. Imported, the pool recycles: VUs re-submit already-used vouchers, the app returns rejections that the (also-dropped) assertions no longer catch, and the run measures a workload the source plan never described. Separately, ignoreFirstLine=true sends a literal 'username,password' request row once per cycle.

**Suggested fix:** Map stopThread/recycle=false to Strategy 'unique'/'pop' where semantics fit, honor ignoreFirstLine via FirstRowHeader, and emit a Warning diagnostic for shareMode values the data-pool model cannot express.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # P06 — CONFIRMED (medium)

parseCSVDataSet (converter_csv_timers.go:34-61) reads only filename, variableNames, delimiter and randomOrder; FirstRowHeader is computed solely as `len(variableNames) == 0` (line 38). A case-insensitive grep for recycle/stopThread/shareMode/ignoreFirstLine over pkg/convert/jmeter returns nothing, so recycle=false/stopThread=true (stop at EOF), shareMode, and ignoreFirstLine-with-explicit-variableNames are all dropped. No diagnostic exists: the CSVDataSet case in walkThreadGroup (converter.go:500-506) emits none, and CSVDataSet is in isKnownJMeterSupported (converter_diagnostics.go:184), so diagnoseJMeterChildren stays silent too. Strategy only ever becomes sequential/random (lines 47-50). Semantics silently diverge from the source plan with no warning or needs_review disposition — confirmed at claimed medium.

</details>


#### 64. CSVDataSet inside Transaction/Loop/If/While/OnceOnly controllers dropped from DataSources...
**Where:** `pkg/convert/jmeter/converter.go:551` · **Area:** AREA-15 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

TransactionController (converter.go:551 `next := *ctx`) and collectJMeterChildActions (converter.go:723 `next := *ctx`) walk controller children with a struct copy of the context; the CSVDataSet case (converter.go:500-506) appends to that copy's csvSources, which is discarded on return. Only the top-level ctx.csvSources is attached to the script (converter.go:309-311). Stats.CSVSources and Summary.CSVSourcesDiscovered are still incremented via the shared result, so the import summary claims the CSV was discovered while script.DataSources omits it.

**Failure scenario:** Plan puts 'users.csv' CSV Data Set Config under a Transaction Controller wrapping the login flow. Import lists users.csv in CSVSourcesDiscovered, but the script has no matching DataSource; login actions target /login?user={{var.username}} which never binds, so every VU sends the literal template (or fails template validation) while the import summary looks clean.

**Suggested fix:** Make csvSources a pointer (like requestReports) or hoist accumulation to a slice owned by ConvertJMX so controller-scoped CSV configs land in script.DataSources.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # P03 — CONFIRMED (medium)

convertContext.csvSources is a plain `[]dsl.DataSource` field (converter.go:121). TransactionController recursion copies the context by value (`next := *ctx`, converter.go:551) and collectJMeterChildActions — used for Loop/If/While/OnceOnly children — does the same (converter.go:723), so the CSVDataSet case's append (`ctx.csvSources = append(...)`, converter.go:503) mutates only the discarded copy. Only the top-level ctx.csvSources reaches script.DataSources (converter.go:309-311). Meanwhile Stats.CSVSources++ (converter.go:505) goes to the shared result, and Summary.CSVSourcesDiscovered is built from the shared csvNames map (converter.go:355, populated at 504), so the summary reports the CSV as discovered while the script has no matching DataSource. ThreadGroup/TestPlan recursion (547-548) passes ctx by pointer, so only controller-nested CSVs are lost. Causal chain fully holds.

</details>


#### 65. Sampler-scoped HeaderManager appends instead of overriding — both header values sent on t...
**Where:** `pkg/convert/jmeter/converter.go:524` · **Area:** AREA-15 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

jmeterSamplerScopedHeaders output is appended after scope headers (converter.go:527) and convertHTTPSampler copies all entries into action.HeaderList (converter_sampler.go:82-86). JMeter merges HeaderManagers with closest-scope replacement (one occurrence, local value wins), but the runtime preserves action-list duplicates (pkg/protocol/http/capabilities.go:548 filterHeaderList(headerList, nil)) and applies them via Header.Add (http.go:1044-1050); only Host/User-Agent duplicates are rejected. A sampler-level override of Authorization, Content-Type or X-Api-Key therefore sends BOTH the thread-level and sampler-level values as repeated header fields.

**Failure scenario:** Thread group HeaderManager sets 'Authorization: Bearer A'; one sampler carries its own HeaderManager with 'Authorization: Bearer B' to hit a different service. JMeter sends only B; the imported script sends two Authorization headers (A then B) — most servers read the first or reject the request, so that step authenticates wrongly under load while the import reports both headers as converted.

**Suggested fix:** When merging sampler-scoped headers, remove same-named (case-insensitive) entries from the inherited list before appending, matching JMeter's replace-on-merge semantics.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # P04 — CONFIRMED (medium)

Converter side: jmeterSamplerScopedHeaders collects sampler-child HeaderManager entries (converter.go:798-813) and they are appended after inherited scope headers with no same-name replacement (`next.headers = append(append(dsl.HeaderList(nil), ctx.headers...), scoped...)`, converter.go:527). convertHTTPSampler copies every entry into action.HeaderList (converter_sampler.go:82-86); only the summary map dedups. Runtime side: effectiveAuthoredHeaderList keeps action HeaderList duplicates (`filterHeaderList(headerList, nil)`, capabilities.go:547,553), validation treats only Host/User-Agent as singletons (capabilities.go:224,247,297-298), and each entry is applied via req.Header.Add (http.go:246,331,1044-1049). So a sampler-level Authorization override sends both values, unlike JMeter's closest-scope replacement (HeaderManager.merge replaces same-named headers). Confirmed at claimed medium.

</details>


#### 66. JMeter TestAction pause applied to every subsequent sampler, not just the next one
**Where:** `pkg/convert/jmeter/converter.go:464` · **Area:** AREA-15 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The TestAction case sets ctx.timerMs (converter.go:466) and the diagnostic claims the pause 'was mapped to think time on the next converted sampler', but ctx.timerMs is never reset after consumption — convertHTTPSampler applies it to every sampler while it stays nonzero (converter_sampler.go:129-132). A one-shot flow-control pause thus becomes per-request think time for the remainder of the scope. Relatedly, scope timers are position-dependent (samplers before the timer element get nothing, while JMeter applies scope timers to all samplers) and multiple timers overwrite last-wins instead of summing.

**Failure scenario:** Plan has a Flow Control Action pausing 10s once at the top of an iteration followed by 20 samplers. JMeter adds 10s per iteration; the imported script adds 10s after every one of the 20 actions (+200s/iteration), silently collapsing throughput to a fraction of the source plan's load shape.

**Suggested fix:** Track TestAction pauses in a separate one-shot field cleared after the next converted sampler (or emit a dedicated wait action at that position); keep scope timers persistent but document/warn about position dependence.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # P05 — CONFIRMED (medium)

TestAction case sets `ctx.timerMs = pauseMs` and emits an Info diagnostic explicitly claiming the pause "was mapped to think time on the next converted sampler" (converter.go:465-470). But grep over pkg/convert/jmeter shows timerMs is only ever written at converter.go:466 (TestAction) and converter.go:508 (scope timers, last-wins overwrite) and read at converter_sampler.go:129-132, where `if ctx.timerMs > 0` attaches a fixed ThinkTime to the action — there is no reset after consumption anywhere in the package. Since ctx is a pointer through walkThreadGroup, every subsequent sampler in the scope (and controller children via context copies) inherits the pause, turning a one-shot Flow Control pause into per-request think time. Confirmed at claimed medium (load-shape distortion, diagnostic actively misleading).

</details>


#### 67. TruClient function bodies imported twice when called from inside another action
**Where:** `pkg/convert/trueclient/converter.go:191` · **Area:** AREA-15 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

collectActionSequence gathers every 'Call Action' step recursively, including those inside action definition bodies (converter.go:191-212), and executionSteps appends flattenActionBlock output for each collected name (157-168). flattenExecutableSteps already inlines nested Call Action targets into the caller's flattened block (235-243), so a helper action invoked from within a run-logic-referenced action contributes its steps twice: once inlined in the caller, once again as its own appended sequence entry at the end.

**Failure scenario:** Archive: Run Block calls 'PrimaryAction'; PrimaryAction's steps include Call Action 'LoginHelper' (a TruClient function). sequence = [PrimaryAction, LoginHelper]; the imported script contains PrimaryAction's flow with LoginHelper inlined AND a second copy of LoginHelper's navigate/fill/click steps appended after it — every iteration logs in twice, doubling that flow's load and corrupting step order, with no warning.

**Suggested fix:** Collect the call sequence only from structural run-logic blocks (steps whose ancestors are not action definitions), or subtract names already reachable via inline expansion before appending.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # P07 — CONFIRMED (medium)

collectActionSequence (pkg/convert/trueclient/converter.go:191-212) recurses via visit() into every child step with no exclusion for steps inside action-definition bodies (Action=="action"), collecting every "Call Action" name. executionSteps (157-164) then appends flattenActionBlock output once per collected name. Independently, flattenExecutableSteps (234-243) already inlines a nested Call Action's target block into the caller's flattened output. So a helper invoked from within a run-referenced action is emitted twice: inlined inside the caller AND again as its own sequence entry. The recursion stack (214-224) only guards cycles within one flatten pass, not cross-entry duplication. No dedup or definition-body exclusion exists. Causal chain holds statically.

</details>


#### 68. scrubRuntimeSecretText treats pre-existing NUL bytes in evidence as matched secret placeh...
**Where:** `pkg/engine/redaction.go:943` · **Area:** AREA-16 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

scrubRuntimeSecretText marks positions as redacted wherever the replaced candidate has byte 0 (redaction.go:943-949). Evidence that already contains 0x00 bytes trips this check even when no secret matched: the original NUL survives the Replacer, candidate[index]==0 sets matched=true, and expandRuntimeSecretPlaceholders (959-991) then rewrites every NUL run as "[REDACTED]" or same-width '*' runs. Any VUser with an active runtime matcher (which, per pkg/protocol/http/http.go:222-227, is every HTTP VUser once a cookie is set) corrupts such evidence.

**Failure scenario:** A load script downloads a binary asset (PDF/protobuf/gzip) and the request fails; the body is retained as ResponseBody/Snapshot evidence. Because the body contains 0x00 bytes and the VUser holds any runtime secret (a single session cookie suffices), every NUL run is replaced with '[REDACTED]' (length-changing) or asterisks -- the archived evidence no longer matches what the SUT returned, misleading failure diagnosis, while the operator believes only credentials were masked.

**Suggested fix:** Before masking, record positions of pre-existing NUL bytes in the original and exclude them from the matched-span union (or use a sentinel byte guaranteed absent by first checking strings.ContainsRune(original, 0) and falling back to a two-pass exact-match scan for such inputs).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # E03 — CONFIRMED (medium)

Placeholders are NUL runs: runtimeSecretPlaceholder = strings.Repeat("\x00", len) (pkg/engine/redaction.go:418-423). In scrubRuntimeSecretText (redaction.go:934-949), `candidate := replacer.Replace(original)` leaves pre-existing 0x00 bytes untouched; the loop marks any `candidate[index]==0` as matched (943-948), so evidence that already contains NULs sets matched=true with no secret hit, and expandRuntimeSecretPlaceholders (959-991) rewrites every NUL run as "[REDACTED]" or same-width '*'. Gate hasRuntimeSecretReplacers (408-416) is satisfied for any VUser with staged cookie values. Binary bodies reach this path: Snapshot.ResponseBody/HTML etc. via scrubSensitiveTextWithConfig (redaction.go:670-677, 905/914) and result.ResponseBody via scrubSensitiveResultWithOptions (redaction.go:648). Prior-report item ("response body retention", PERF_REVIEW_2026-07-06) is a different, retention-policy concern — not a regression of it.

</details>


#### 69. JWT-shaped regex redacts ordinary dotted hostnames/identifiers in result metadata and met...
**Where:** `pkg/engine/redaction.go:101` · **Area:** AREA-16 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

payloadRedactionPatterns includes \b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b (redaction.go:101) intended for JWTs, but it matches any three consecutive dot-separated segments of 8+ [A-Za-z0-9_-] characters -- i.e. most internal FQDNs and many dotted metric/action identifiers. Because scrubSensitiveResultMetadata (redaction.go:691-725) applies all payload patterns to ActionName, DependencyTarget, PageURL, CorrelationID etc. before metrics recording (vuser_execute.go:48-61), those non-credential values are destroyed in ByDependencyTarget/ByAction dimensions and archives.

**Failure scenario:** A JDBC/HTTP dependency target "internal-api.payments-svc.cluster1.local" (segments 12/12/8 chars) is rewritten to "[REDACTED].local": the Command Center dependency breakdown shows a single "[REDACTED].local" bucket instead of per-host rows, and multiple distinct hosts with this shape collapse into one dimension, silently merging their latency/error stats. Dotted action names from importers (e.g. "checkout_flow.submit_payment.confirm_page") suffer the same fate in ByTransaction.

**Suggested fix:** Tighten the JWT heuristic: require at least one segment to contain both upper and lower case or a base64url '=' / typical JWT header prefix (eyJ), or require segments of >= 16 chars, and/or exempt strings that parse as hostnames (all-lowercase with hyphens).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # E04 — CONFIRMED (medium)

Pattern verified at pkg/engine/redaction.go:100-103: `\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b` → "[REDACTED]", with no eyJ/case/entropy guard. It matches any three consecutive dot-separated 8+ char segments; for "internal-api.payments-svc.cluster1.local" the first three segments (12/12/8) match, `\b` holds at '1'|'.', yielding "[REDACTED].local". scrubSensitiveTextWithConfig applies all payloadRedactionPatterns unconditionally, even with nil config (redaction.go:906-908). ActionName/CorrelationID/DependencyTarget are scrubbed by scrubSensitiveResultMetadata (redaction.go:692,697,700), called from scrubSensitiveResultWithOptions (redaction.go:645) before Metrics.Record (pkg/engine/vuser_execute.go:49-60), and ByDependencyTarget keys on the scrubbed value (pkg/engine/metrics.go:663-676), so distinct hosts collapse into one "[REDACTED].local" bucket. Not related to the prior "response body retention" item; no regression.

</details>


#### 70. CDP sessions leak on every window switch / popup activation
**Where:** `pkg/protocol/browser/runtime_context.go:629` · **Area:** AREA-17 · **Category:** resource-leak · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

installCDPSession (runtime_context.go:614-635) calls browserContext.NewCDPSession(page) and overwrites b.pwCDP without detaching the previous session; registerPage(page, true) calls it on EVERY activation — every browser_switch_window resolution path (frame_actions.go:648,658,668,677,687) and every popup activation — even when the page is already known. Detach happens only for the final b.pwCDP at close() (lifecycle.go:10-19). When chromium + (clientMetricsEnabled or cpuThrottleRate>1 or network simulation) is active, each switch creates a fresh CDP session (with Network.enable re-sent when shaping is on) that stays attached to a live page for the whole browser session.

**Failure scenario:** A soak scenario on chromium with client metrics or network shaping enabled alternates between two windows via browser_switch_window each iteration. Each iteration attaches one more CDP session per switch to the same long-lived pages; over hours, thousands of undetached sessions accumulate in the playwright-go connection registry and in the Chromium process (each with its own event routing, and Network domain enabled), growing memory in both the generator and the browser and multiplying CDP event traffic until the pod degrades.

**Suggested fix:** In installCDPSession, detach the previous b.pwCDP (if any) before storing the new one, and skip creating a session when the activated page is already the current page with a live session.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # W02 — CDP sessions leak on window switch / popup activation — CONFIRMED (medium)

Chain verified at HEAD (snapshot dc05008ab):

1. `pkg/protocol/browser/runtime_context.go:614-635` — `installCDPSession` calls `browserContext.NewCDPSession(page)` (line 621, always a fresh session; playwright-go has no memoization) and overwrites `b.pwCDP` (628-630) with no `Detach` of the prior session.
2. Reachable: `registerPage(page, true)` invokes it on every activation (runtime_context.go:395-397), including for already-known pages; every `trySwitchWindow` resolution path calls `registerPage(...,true)` (frame_actions.go:648,658,668,677,687), plus setup (runtime_context.go:91), open (action_dispatch.go:263), and popup activation when `autoActivatePopups` (runtime_context.go:165).
3. Gate is realistic: chromium + (clientMetrics config.go:180-185, cpu throttle, or network shaping) — runtime_context.go:618.
4. No refuting guard: sole `Detach` is close() on the final `b.pwCDP` (lifecycle.go:10-19). Browser lives for the whole VU (engine.go:356 teardown deferred after all iterations); per-iteration reset only with `new_user_per_iteration` (vuser.go:434). Pages survive switches, so orphaned sessions stay attached; with shaping, each got `Network.enable` (runtime_context.go:649), multiplying event traffic.

Prior-report check: WEEK_REVIEW_2026-07-02 W49 was a popup auto-activation *behavior* change, not this leak — not a regression.

</details>


#### 71. SSE streams silently killed by the default 60s client timeout despite a larger validated ...
**Where:** `pkg/protocol/http/http.go:536` · **Area:** AREA-17 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

readSSEResponse honors params.stream_timeout_ms up to MaxConfiguredTimeout = 1h (config.go:47; validated at http.go:793-799), but the shared http.Client carries Timeout = requestTimeout, defaulting to 60s (config.go:156), which net/http applies through the entire body read. The per-action override only applies when action.TimeoutMs > 0 (http.go:410-412). So an SSE action with stream_timeout_ms=300000 and no explicit request/action timeout is cancelled at 60s: the body closes mid-stream and the sample fails with 'request timeout: context deadline exceeded' even though the stream was healthy and within its declared window.

**Failure scenario:** Author configures an SSE action with stream_timeout_ms=300000 (5 min) and max_events=0, leaving config.http.request_timeout_ms unset. Every VUser's SSE sample errors at exactly 60s with a timeout message, producing a run full of failed samples and misleading P99s; the author's declared stream window is silently ignored.

**Suggested fix:** When shouldReadSSE(params) (or params.stream is set), raise the per-request client Timeout to at least stream_timeout_ms (plus headroom) unless the author explicitly set a smaller action timeout; or validate/warn when stream_timeout_ms exceeds the effective request timeout.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # W04 — SSE stream killed by default 60s client timeout — CONFIRMED (medium)

config.go:156 requestTimeout defaults 60s; http.go:158-162 client.Timeout = h.requestTimeout; http.go:409-412 the per-request copy only raises Timeout when action.TimeoutMs > 0 — no SSE-aware adjustment anywhere (grep: only line 411 touches client.Timeout). stream_timeout_ms is validated up to MaxConfiguredTimeout=1h (http.go:793-799, config.go:47) and honored inside readSSEResponse (sse.go:77-96). net/http Client.Timeout covers body reads, so at 60s the read errors; sse.go:205-214 deliberately classifies Client.Timeout as a premature read timeout and returns it even when events were collected; http.go:584-586 then fails the sample. So stream_timeout_ms=300000 with defaults fails every sample at 60s. Prior-report item (work-limit hard-fail deliberate-nonfix) is a different branch — no match, no regression.

</details>


#### 72. Extractor on_miss contract implemented inconsistently: ws/grpc ignore it, network always ...
**Where:** `pkg/protocol/websocket/websocket.go:272` · **Area:** AREA-17 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The DSL validates on_miss ("", ignore, empty, fail) and mode (all) for extractors of every protocol family (pkg/dsl/validate.go:757-780 — the source/type allowlist is skipped for websocket/grpc/network but on_miss/mode are checked and accepted), and protocol.Extractor documents fail semantics (pkg/protocol/protocol.go:553-554). But websocket.receive (websocket.go:271-279) and grpc.applyResponseChecks (grpc.go:391-400) silently ignore extraction errors — on_miss:"fail" never fails, on_miss:"empty" never assigns "", mode:"all" is unsupported — while network.runNetworkExtractors (network.go:625-635) fails the whole sample on ANY miss, even with the default on_miss (ignore).

**Failure scenario:** Scenario A: ws_receive with extractor {name: token, on_miss: fail}; the server message lacks the field. The action reports success, the variable stays unset, and the next ws_send transmits the literal '{{var.token}}' — a downstream 4xx blamed on the wrong step. Scenario B: a network dns/tcp action has an optional extractor with default on_miss; a miss fails the whole sample, inflating error rates for a run where the author explicitly chose lenient semantics.

**Suggested fix:** Implement on_miss handling (fail/empty/ignore) in websocket.receive, grpc.applyResponseChecks, and network.runNetworkExtractors mirroring http.go's semantics; alternatively reject unsupported on_miss/mode values at DSL validation for these protocols.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # W03 — on_miss contract inconsistent (ws/grpc ignore, network always fails) — CONFIRMED (medium)

pkg/dsl/validate.go:768-777 validates OnMiss (""/ignore/fail/empty) and Mode (""/all) for every protocol — only the source/type allowlist is HTTP-scoped (validate.go:744-750). protocol.go:553-554 documents "fail: fail the request". HTTP implements it (http.go:659,674,697 + on_miss tests). But websocket.go:271-279 does `if err == nil { store }` — extraction errors silently dropped, so on_miss:"fail" never fails and "empty" never assigns; grpc.go:391-400 is the identical pattern. network.go:625-635 (runNetworkExtractors) returns the error string on ANY miss, failing the sample even with default on_miss=ignore. grep confirms zero OnMiss references in websocket/grpc/network packages. Failure scenarios (unset var sent literally; inflated network error rates) follow directly. Severity medium stands.

</details>


#### 73. websocket_reconnect does not cover connections discarded by a failed receive
**Where:** `pkg/protocol/websocket/websocket.go:206` · **Area:** AREA-17 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

receive() discards the connection on ANY read error, including an ordinary read-deadline timeout (websocket.go:259-262 -> discardConn sets w.conn=nil, correct because gorilla read errors are sticky). But send() and ping() only attempt reconnection when writeMessage/WriteControl fails on an existing conn (websocket.go:214-225, 294-306); when currentConn() finds w.conn==nil they return 'websocket connection is not open' immediately (websocket.go:206-208, 330-337) even though websocket_reconnect defaults to true (websocket.go:69) and lastTarget/lastHeaders are retained precisely for reconnection (websocket.go:349-385).

**Failure scenario:** Polling script: ws_connect once, then loop {ws_send, ws_receive(timeout_ms=5000)}. The server is slow once and the receive times out; the conn is discarded. Every subsequent ws_send in every remaining iteration fails with 'websocket connection is not open' for the rest of the VUser's session, despite reconnect=true — one transient timeout converts into a permanently failing VUser without an explicit ws_connect in the loop.

**Suggested fix:** In send/ping (and optionally receive), when currentConn reports no connection and w.reconnect is true and lastTarget is set, call reconnectConn instead of failing.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # W05 — reconnect does not cover conn discarded by failed receive — CONFIRMED (medium)

websocket.go:259-262: receive() calls discardConn on ANY ReadMessage error, including an ordinary read-deadline timeout (deadline set at 243-251), nil-ing w.conn (387-394). send() (206-209) and ping() (289-292) call currentConn(), which returns "websocket connection is not open" when w.conn==nil (330-337) — returned immediately with no reconnect attempt. The reconnect path (reconnectConn, 349-385, using retained lastTarget/lastHeaders from 196-197) is only reachable when writeMessage/WriteControl fails on an existing conn (214-225, 294-306), despite websocket_reconnect defaulting true (line 69). So one receive timeout permanently fails all later ws_send/ws_ping for the VUser unless the script re-issues ws_connect. Minor caveat: same code path also applies after deliberate ws_close (312-316), so a fix must distinguish the two. Medium stands.

</details>


#### 74. Sidecar batch validation misreports timeout exhaustion as target_validation_failed
**Where:** `pkg/javasidecar/server.go:512` · **Area:** AREA-18 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleValidate (server.go:490-518) validates every target sequentially — each an independent JVM launch via runHarness — under one shared bounded context (default 30s via boundedTimeout at server.go:478). When that budget expires mid-batch, the remaining targets' runHarness outcomes fail with a context error but are recorded indistinguishably as `target_validation_failed` (server.go:513-517). The caller preflightSidecar (pkg/protocol/java_vuser/java_vuser.go:1605-1609) passes TimeoutMS = cfg.ActionTimeout (default 30s, java_vuser.go:38) for the WHOLE batch and fails the entire preflight with the misleading per-target code (java_vuser.go:1616-1625).

**Failure scenario:** A multi-class LR Java import (importer splits into 10+ Actions classes) runs runtime preflight against the sidecar. JVM spin-up is ~2-4s per target on a cold node, so targets after ~8-12 exceed the 30s batch budget. Preflight reports 'java_vuser sidecar validation failed (target_validation_failed at index 9)' — the operator debugs a perfectly valid class instead of raising the timeout, and the import is blocked on a phantom validation failure.

**Suggested fix:** In handleValidate, check ctx.Err() after each target and emit a distinct issue code (e.g. validation_timeout with the index reached) instead of target_validation_failed; optionally scale the effective budget by len(targets) up to MaximumExecutionTimeout, and have preflightSidecar surface the distinct code.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # J02 — CONFIRMED (medium)

handleValidate (pkg/javasidecar/server.go:468-521) builds one ctx from boundedTimeout(request.TimeoutMS, DefaultExecutionTimeout=30s; types.go:26) at server.go:490 and runs prepareRuntime, verifyHarness, then every target's runHarness (line 512, one JVM launch each) under that shared budget. runProcess sets outcome.success=false with errorCode from contextErrorCode(ctx) = "execution_timeout" on deadline expiry (server.go:1719-1722, 2025-2026), but handleValidate discards errorCode and records every failure as `target_validation_failed` (server.go:513-517). Caller preflightSidecar passes TimeoutMS = cfg.ActionTimeout (java_vuser.go:1608; default 30s at java_vuser.go:38/466) for the whole batch and surfaces the first issue verbatim (java_vuser.go:1617-1622). Timeout exhaustion mid-batch is thus reported as a per-target validation failure. Confirmed.

</details>


#### 75. Real Kafka adapter silently ignores explicit partition targeting and partition strategy
**Where:** `pkg/protocol/kafka/realclient_franz.go:387` · **Area:** AREA-18 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

kafka.go buildRecord (kafka.go:886) sets rec.Partition via resolvePartition (kafka.go:904-926: explicit params.partition, else key-hash/round-robin per cfg.PartitionStrategy) and the mock honors it, but franzClient.Produce (realclient_franz.go:383-392) only copies rec.Partition into the kgo.Record when >0 and newClient (realclient_franz.go:163-259) never sets kgo.RecordPartitioner; franz-go v1.21.5 defaults to UniformBytesPartitioner (config.go:666 in the dependency), which ignores Record.Partition — only ManualPartitioner (partitioner.go:121) reads it. Additionally the `> 0` guard makes partition 0 unaddressable even if a manual partitioner were configured.

**Failure scenario:** A script imported from LoadRunner/JMeter that pins records to a specific partition (params.partition = 2) — or relies on kafka_partition_strategy round_robin/key-hash — validates green against the mock broker (which routes via resolvePartition), then runs against a real cluster where every record is routed by franz's uniform-bytes partitioner instead. Per-partition ordering assumptions are silently violated; a partition_equals assertion fails intermittently depending on where the sticky partitioner lands, and without an assertion the misrouting is completely silent (extractions report the actual partition, masking the ignored intent).

**Suggested fix:** In newClient, install a wrapper partitioner: if the record carries a non-negative explicit partition (track intent with a sentinel, e.g. Partition >= 0 plus an explicit-flag header or a dedicated field on kafkaRecord), delegate to kgo.ManualPartitioner semantics, else fall back to the default. Alternatively reject params.partition on the real adapter loudly (matching the existing per-action consumer_group fail-loud precedent at realclient_franz.go:412-414).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # J01 — CONFIRMED (medium)

kafka.go:886 buildRecord sets Partition via resolvePartition (kafka.go:904-926: explicit `partition` param wins, else round-robin/key-hash per cfg.PartitionStrategy). The mock honors it (broker.go:239 returns recordMeta with rec.Partition). The real adapter does not: franzClient.Produce (realclient_franz.go:387-389) copies rec.Partition into kgo.Record only when >0 (partition 0 unaddressable), and newClient (realclient_franz.go:163-259) never sets kgo.RecordPartitioner — grep over pkg/protocol/kafka shows zero RecordPartitioner/ManualPartitioner uses. go.mod pins franz-go v1.21.5, whose default UniformBytesPartitioner ignores Record.Partition; only kgo.ManualPartitioner reads it. So explicit targeting and strategy are silently dropped on real clusters while validating green on the mock. Causal chain holds.

</details>


#### 76. Archived interval summaries drop the available t-digest, forcing the wrong fleet-percenti...
**Where:** `pkg/distributed/generatorrpc/archive.go:120` · **Area:** AREA-19 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

archiveIntervalSummary (archive.go:120-134) persists only scalar P50/P90/P95/P99. At record time the fleet t-digest is literally in hand: recordMetricsBatch computes those scalars from batch.FleetMetrics.DurationDigest via quantileFromMetricAggregate (service.go:1677-1680, 2784-2793) and then discards the digest. The live stream (MetricAggregate.duration_digest, generator.proto:271) and the manifest TerminalMetrics tombstone (archive.go:182-200) both ship merge-capable digests, so the archive interval path is the one seam where percentile evidence is reduced to non-mergeable scalars — which is exactly where the parallel review confirmed the pkg/api distributed archive merge concatenates per-generator rows and produces wrong fleet percentiles.

**Failure scenario:** A 2-generator distributed run where generator A serves fast responses (P99 50ms) and generator B is CPU-throttled (P99 900ms): each generator archives correct per-pod scalar percentiles, but the controller-side fleet merge over concatenated scalar rows cannot compute a true fleet P99 for any interval — every distributed archived run's fleet percentile time series is wrong, and no amount of controller-side fixing can recover the distribution because the digests were dropped here.

**Suggested fix:** Add an optional serialized fleet TDigestSnapshot column/field to archiveIntervalSummary (JSONL + parquet schema), populated from batch.FleetMetrics.DurationDigest in recordMetricsBatch; bump archiveSchemaVersion so the controller merge can detect digest-capable archives.

<details><summary>Verification evidence</summary>

**downgraded @ medium:** # S02 verification — downgraded (high → medium)

Mechanics confirmed at every hop. `archiveIntervalSummary` carries only scalar P50/P90/P95/P99 (pkg/distributed/generatorrpc/archive.go:120-134). At record time the fleet digest is in hand and discarded: recordMetricsBatch computes the four scalars via quantileFromMetricAggregate(batch.FleetMetrics,...) (service.go:1669-1681), which reconstructs a TDigest from the proto snapshot per call (service.go:2784-2793). The production distributed finalize path (distributed_stream.go:1516 → stageRemoteArchive, distributed_archive.go:39-124) merges per-generator interval_summaries.jsonl by raw byte concatenation (distributed_archive.go:548-609), and consumers use rows as-is with no cross-generator aggregation — report SummaryTimeline emits each row's P95Ms (archive_report.go:422-450); the summaries API only downsamples (archive_loaders.go:1111-1131). So per-interval percentile series for distributed archives are interleaved per-pod values, not fleet percentiles.

However the "irrecoverable / every fleet percentile wrong" framing is refuted: the generator archive records every raw transaction with DurationMs (archive.go:710-748), transactions.jsonl is merged too (distributed_archive.go:553), and the report's whole-run Overview P50-P99 are already recomputed fleet-correct from those raw rows (archive_report.go:84, 245-248). Per-interval fleet percentiles are controller-recoverable from timestamped raw transactions without the digest. Real but bounded data-integrity defect: medium.

</details>


#### 77. Coordinated start barrier trusts generator wall clocks: skew either skews the fleet ramp ...
**Where:** `pkg/distributed/generatorrpc/execution_control.go:365` · **Area:** AREA-19 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

CommitWork evaluates the controller-chosen start_at_unix_nano against the generator's local clock: a barrier not after local now is rejected outright ('coordinated start barrier has already elapsed', execution_control.go:366-370), one beyond the stage TTL is rejected as too far in the future (371-373), and waitForCoordinatedExecutionStart (485-498) sleeps time.Until(startAt) on the local clock. There is no clock-offset estimation even though the heartbeat exchange carries both sides' timestamps (service.go:1213-1215 replies with max(local, ping timestamp), acknowledging skew exists). A generator whose clock is ahead of the controller by more than the controller's lead time hard-fails the whole fleet commit; smaller skews silently stagger the 'coordinated' ramp across generators.

**Failure scenario:** A generator node with +3s NTP drift receives a fleet commit whose barrier is 2s in the future: that generator returns rejected, the distributed start fails on an otherwise healthy fleet, and the error message gives the operator no hint that clock skew is the cause. With -1.5s drift the generator instead starts 1.5s late, so the distributed ramp the barrier exists to align is skewed by exactly the node drift.

**Suggested fix:** Estimate per-generator clock offset from the heartbeat exchange (or echo the controller's now alongside start_at) and apply the offset when translating the barrier to a local deadline; include local-vs-barrier delta in the rejection message.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # S05 — CONFIRMED (medium)

Controller picks the barrier from its own clock: startAt = time.Now().Add(distributedCommitStartLead(...)) (pkg/api/distributed_stream.go:195); lead is ackWindow (≤2s) + 0.5-1s margin (pkg/api/distributed_execution_backend.go:1155-1179), so ~2.5-3s. Generator evaluates it against generator-local time.Now() (execution_control.go:430 passes time.Now(); 366-373 rejects "already elapsed" if !startAt.After(now) or "too far in the future" beyond stage TTL), and waitForCoordinatedExecutionStart sleeps local time.Until(startAt) (485-498). Grep confirms no clock-offset/skew estimation anywhere in generatorrpc, though heartbeat pong clamps TimestampUnixMs to max(local, ping) (service.go:1215), acknowledging skew. So drift > ~2.5s lead hard-rejects the fleet commit; smaller drift staggers the ramp by the node offset. Causal chain holds.

</details>


#### 78. Per-group publishers each archive a full cross-group interval summary, duplicating window...
**Where:** `pkg/distributed/generatorrpc/service.go:1646` · **Area:** AREA-19 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

publishMetricsWithAggregates runs once per group per tick (runExecutionPublisher is started per group, execution_control.go:467-474), and each call builds batch = metricsBatchLocked(testID) which merges ALL groups' latest window aggregates (service.go:1841, mergeMetricsAggregateSets 2490-2526) and then records an interval summary from the merged fleet totals (service.go:1650, 1669-1681). On a pod hosting G groups this produces G summary rows per second, each containing every group's latest window counts, so each group's window appears in ~G archived rows.

**Failure scenario:** A test with 3 groups on one generator, each completing 100 tx/s: the archive gains 3 rows/sec, each with TotalTx around 300. Summing TotalTx over the summaries (run-total reconstruction, error-count rollups, CSV exports) yields around 900/s — 3x reality; even interval graphs get 3 near-duplicate points per second whose TotalTx cannot be interpreted as per-interval deltas. Single-group tests are unaffected, which is why this survives the common case.

**Suggested fix:** Record interval summaries from one owner per test (e.g., only the group that triggered the publish contributes its own window, or a single per-test summary ticker), or add group_name to the summary row so each row carries only the publishing group's window.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # S04 — Per-group publishers each archive a cross-group interval summary — CONFIRMED (medium)

Full chain verified statically at HEAD snapshot:

1. One publisher per group: execution_control.go:375-384 builds one executionStartEvent per groupName; :456-459 applies them; :467-474 each event spawns runExecutionPublisher. So G groups on a pod → G publisher goroutines.
2. Each ticks at publishInterval = time.Second (execution_control.go:769; service.go:2082-2089) → publishExecutionSnapshot → publishMetricsForExecution (service.go:2102, 1585-1590) → publishMetricsWithAggregates.
3. That call stores only the caller group's window (service.go:1644) but builds batch := metricsBatchLocked(testID) which merges ALL groups: service.go:1823-1841, mergeMetricsAggregateSets (2503-2517) folds every group's retained Fleet aggregate into one.
4. recordMetricsBatch (service.go:1650, 1669-1681) writes RecordIntervalSummary with TotalTx/TotalErrors from the merged FleetMetrics (archiveTotalsFromBatch, service.go:2766-2771) via the single per-test writer (1647).
5. No refuting guard: archive.go:853-870 RecordIntervalSummary encodes unconditionally, no timestamp/window dedupe, IntervalSummaryCount++ per call. The comment at service.go:1842-1851 confirms the summary is intentionally fed from the cross-group batch.

Net: G rows/sec, each carrying every group's latest window → each window duplicated ~G times; summing TotalTx across rows over-counts ~Gx. Single-group runs unaffected → medium stands. Prior-report items (heartbeat pong, RuntimeCapabilityProvider) are unrelated symbols; not a regression.

</details>


#### 79. mergeGeneratorStats sums process-wide gauges across co-located groups, over-reporting res...
**Where:** `pkg/distributed/generatorrpc/stats.go:106` · **Area:** AREA-19 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Each group execution's stats come from its own engine snapshot, and SystemResources measures the whole generator process (pkg/engine/sys_metrics.go:106-131: rc.proc.CPUPercent, NumThreads, NumFDs, runtime.ReadMemStats, runtime.NumGoroutine). metricsBatchForAssignment merges per-group stats with mergeGeneratorStats (service.go:1823-1828), which ADDS CPUPercent, MemoryBytes, GoroutineCount, ThreadCount, OpenFDCount, GCCountDelta, GCPauseMs, HeapAllocMB, HeapInUseMB, ContainerCPULimitCores, ContainerCPUThrottledSecondsRate, ContainerMemoryWorkingSetMB, ContainerMemoryLimitMB, TCPEstablished, TCPTimeWait (stats.go:107-140). Every group's snapshot describes the SAME process, so a test with G groups on one pod reports ~G x the true process CPU/memory/threads/FDs and counts each GC G times. The code already uses max() for other host-wide values (CPUUserPct, Load1/5/15, ContainerCPUUsagePct), showing the summed fields are an inconsistency, not a policy.

**Failure scenario:** A scenario with 4 groups assigned to one generator pod running at 60% real process CPU and 2GB RSS publishes batches claiming ~240% CPU and ~8GB memory; RecordResourceSample archives the inflated values (service.go:1661), generator health graphs and archived resource timelines are wrong for every multi-group distributed run, and any threshold/auto-stop or capacity logic keyed on generator CPU/memory can false-trigger.

**Suggested fix:** In mergeGeneratorStats, treat process/host-wide gauges (CPUPercent, MemoryBytes, heap, threads, FDs, goroutines, GC deltas, container limits/working set, TCP counts) as max() or last-sample, keeping only ActiveVUsers (and true per-group counters, if any) additive.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # S03 — CONFIRMED (medium)

mergeGeneratorStats (pkg/distributed/generatorrpc/stats.go:106-145) sums CPUPercent, MemoryBytes, ThreadCount, OpenFDCount, GoroutineCount, GCCountDelta, GCPauseMs, HeapAllocMB/InUseMB, ContainerCPULimitCores, ContainerMemoryWorkingSetMB/LimitMB, TCPEstablished/TimeWait while using maxFloat64 for CPUUserPct/Load/ContainerCPUUsagePct — confirming the inconsistency. Each group's stats come from generatorStatsFromResources (stats.go:17-18: CPUPercent=CPUProcessPct, MemoryBytes=process RSS) fed by engine.SystemResources measuring the whole process (pkg/engine/sys_metrics.go:106-121: rc.proc, runtime.ReadMemStats). Per-group snapshots are stored (service.go:2195) and summed per batch (service.go:1822-1824), so G co-located groups report ~G× real process resources; RecordResourceSample archives the inflated values (service.go:1661). No downstream normalization found.

</details>


#### 80. powerbi_publishes rows are orphaned forever: no delete path on test or project deletion
**Where:** `pkg/storage/sqlite_tests.go:1323` · **Area:** AREA-20 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

The powerbi_publishes table (pkg/storage/sqlite_schema.go:685-702) stores test_id and project_id without foreign keys, and no code anywhere deletes from it: sqlite_powerbi.go exposes only Save/UpdateStatus/GetLatest/List (pkg/storage/sqlite_powerbi.go:27-111), deleteTestGraphTx (sqlite_tests.go:1323-1394) and deleteProjectGraphTx (sqlite_projects.go:1293-1362) both omit it, and there is no retention sweep. Deleting a test or project strands its publish audit rows permanently.

**Failure scenario:** Publish run results to Power BI (creates a powerbi_publishes row keyed by test_id), then delete the test or its project. The publish rows survive as orphans: (1) the table grows without bound across the controller's lifetime as runs are published and pruned; (2) because dedup/re-publish logic anchors on GetLatestPowerBIPublishForTest/GetLatestPowerBIPublishForDataset (sqlite_powerbi.go:91-110), a future test that reuses a deterministic test ID (CI materialization keys produce deterministic IDs) observes the deleted run's publish state and can wrongly skip or misreport a publish.

**Suggested fix:** Add `DELETE FROM powerbi_publishes WHERE test_id = ?` to deleteTestGraphTx and `DELETE FROM powerbi_publishes WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)` to deleteProjectGraphTx, next to the other test-scoped audit rows (github_check_links pattern).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # S09 — powerbi_publishes orphaned forever: CONFIRMED (medium)

Snapshot HEAD dc05008ab, paths repo-relative.

1. Table has no FK edges: pkg/storage/sqlite_schema.go:685-702 defines powerbi_publishes with test_id/project_id TEXT NOT NULL and no REFERENCES clause (contrast the preceding table at :683 which references projects(id)). Because postgres_foreign_keys.go derives FKs from this same schema, neither dialect enforces anything — deletes succeed silently, leaving orphans.
2. No delete path anywhere: grep across the snapshot for "powerbi_publishes" hits only sqlite_schema.go (create + two indexes) and sqlite_powerbi.go (INSERT :40, UPDATE :69, SELECTs :92/:102/:115). No `DELETE FROM powerbi_publishes` exists in any .go file; no retention sweep.
3. Both delete graphs omit it: deleteTestGraphTx (pkg/storage/sqlite_tests.go:1323-1394; tail read directly — test_history, test_notes, interval summaries, grafana snapshots, github check links/intents, run_archives, archive jobs, then tests) and deleteProjectGraphTx (sqlite_projects.go) never mention the table (grep-verified).
4. Consequence real: rows accumulate forever; dedup anchors on GetLatestPowerBIPublishForTest (pkg/api/powerbi_publish.go:274 → powerBIPublishConflict :139-160 returns 409 unless force=true), and test-ID reuse is realistic — CI runs pass RequestedTestID/MaterializationKey (pkg/api/ci_runs.go:357, honored at testcreation.go:83), so a rematerialized run with a reused ID inherits the deleted run's publish state and false-conflicts.

Prior-report check: the "deleteTestGraphTx fixed 2026-07-16" item predates the powerbi_publishes table (added with Power BI publishing 2026-07-23), so this is a new omission, not a regression of that fix. Severity medium stands: silent unbounded growth plus a plausible-but-narrow dedup misfire, no broken headline workflow.

</details>


#### 81. Data-param SearchText 'key=value' has no left boundary; applying a short-key suggestion r...
**Where:** `pkg/convert/paramize/dataparams.go:107` · **Area:** AREA-22 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

collectQueryParams (dataparams.go:107) and the form branch (dataparams.go:168) record SearchText as key+"="+value with no leading delimiter, and ApplySuggestions performs plain substring replacement over the whole field via strings.NewReplacer (apply.go:545-552, replaceOutsideTemplates). 'id=1' is a substring of 'user_id=1', so applying only the 'id' suggestion on a target '?user_id=1&id=1' also rewrites the tail of the unrelated user_id parameter. The overlap validator (apply.go:146-191) only compares literals between two *selected* suggestions — selecting both fails closed with an error, but selecting just the short-key suggestion silently corrupts the other parameter.

**Failure scenario:** Import produces data-param suggestions for '?session_id=7&id=7' (both keys pass LooksParameterName, same recorded value). The reviewer selects only the 'id' suggestion. Apply rewrites the target to '?session_id={{var.id}}&id={{var.id}}': session_id is silently bound to the id data column, so at runtime every VUser sends its id-column value as session_id — wrong data with no diagnostic, and the diff (truncated to 80 chars) can easily hide it.

**Suggested fix:** Anchor replacements: store the preceding delimiter in SearchText (e.g. '?id=1'/'&id=1' per occurrence like formSecretPattern does with its (?:^|[&\s]) group), or validate each selected suggestion's literal against the field for embedded longer-key occurrences (reuse the existing mergedLiteralOccurs machinery) and fail closed.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # T02 — CONFIRMED (medium)

pkg/convert/paramize/dataparams.go:106-107 records SearchText as `key+"="+value` with no leading `?`/`&` anchor (form branch identical at :167-168). Application path: apply.go:435-436 groups literals per field, :469-473 `replaceInFieldValues` calls `replaceOutsideTemplates` (:522-568), which builds `strings.NewReplacer` over raw literals and replaces every occurrence in the field — no key-boundary awareness, so `id=1` matches inside `user_id=1`, rewriting the unrelated key's value. The overlap validator (apply.go:164-190) only compares literals among other *selected* suggestions via `owners[key]`; a single selected suggestion is never checked against surrounding field text. `LooksParameterName` (heuristics.go:32-39) accepts exact `id` and `_id` suffixes, so both suggestions are genuinely emitted. Causal chain holds end-to-end.

</details>


#### 82. JMeter __counter/__threadNum translate to {{var.*}} variables nothing defines, reported a...
**Where:** `pkg/convert/tmpl/functions.go:77` · **Area:** AREA-22 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

translateJMeterFunction maps ${__counter(...)} to {{var.jmeter_counter}} (functions.go:77-82) and ${__threadNum} to {{var.jmeter_thread_num}} (functions.go:84) and returns ok=true, so no diagnostic is emitted (the not-ok path at functions.go:30 is the only warning). Grep over the whole pkg tree shows no other reference to jmeter_counter/jmeter_thread_num — no engine builtin, no materialized parameter — so the imported script references variables that are never set. Untranslatable functions at least warn and keep the original token; these silently claim a successful translation that produces a wrong value at replay.

**Failure scenario:** A JMeter plan uses ${__counter(TRUE,idx)} in a request parameter to generate unique IDs. Import succeeds with zero diagnostics for that token; the generated script sends whatever the engine substitutes for an undefined {{var.idx}} (empty string or the literal template) on every iteration — duplicate/invalid IDs at load-test time, with nothing in the import review pointing at the cause.

**Suggested fix:** Either synthesize a real counter (materialize a parameter/extractor or engine builtin) or return ok=false so the shared 'left intact + warning' path applies; at minimum emit addWarning for counter/threadNum like the other approximations.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # T04 — CONFIRMED (medium)

pkg/convert/tmpl/functions.go:77-84: `__counter` → `{{var.jmeter_counter}}` (or sanitized arg name) and `__threadnum` → `{{var.jmeter_thread_num}}`, both returning ok=true; the only warning path is the !ok branch at functions.go:30. Grep across pkg/internal/cmd/dashboard for `jmeter_counter|jmeter_thread_num` matches only functions.go and its test — no engine builtin, importer materialization, or parameter defines them; pkg/convert/jmeter has no counter handling. At replay, pkg/engine/vuser_resolve.go:670-678 fails the `var.` lookup, and vuser_resolve.go:541-554 then keeps the literal token in the request and tracks unresolved refs only for secret./file./param. — var.* misses are fully silent. Import reports success; runtime sends the literal `{{var.jmeter_counter}}` with no diagnostic anywhere. Confirmed.

</details>


#### 83. HAR scrub destroys base64-encoded response content while leaving encoding flag intact
**Where:** `pkg/scrub/scrub.go:1484` · **Area:** AREA-22 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

scrubHAREntry (scrub.go:1484-1488) runs cp.value over response content.text without checking content.encoding == "base64". A base64 body is one long [A-Za-z0-9+/=]{24,} run with letters, digits, and >=4.0 shannon entropy, so highEntropyToken + looksSensitiveToken (scrub.go:75, 1048-1054, 1544-1562) replace it — sometimes partially, leaving invalid base64 — with [REDACTED], while the sibling "encoding":"base64" field is preserved. Every binary response (images, fonts, protobuf) in every scrubbed HAR is destroyed and no longer decodes, contradicting redactBareTokens' own stated contract (scrub.go:1038-1040: 'keeping scrubbed artifacts usable as evidence and re-import inputs').

**Failure scenario:** A browser-recorder session records a page with images; scrub-on-write replaces each base64 content.text with [REDACTED] (or a partial '[REDACTED]<residue>') while encoding stays 'base64'. Any consumer that decodes content per the HAR spec (viewer preview, replay-fidelity comparison, re-import) gets a decode error or garbage for every binary entry of every recording — silent, systematic loss of all binary evidence rather than targeted secret removal.

**Suggested fix:** In scrubHAREntry, when content.encoding is 'base64' either skip the value pass, or decode → scrub → re-encode, or replace the whole content object with a deliberate redaction marker (empty text plus a _scrubbed annotation) so the encoding contract is not silently violated. At minimum drop the encoding field when text is redacted.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # T03 — HAR scrub destroys base64 response content — CONFIRMED (medium)

Chain verified at HEAD. `scrubHAREntry` (pkg/scrub/scrub.go:1484-1488) runs `cp.value` on `response.content.text` unconditionally; grep shows no read of `content["encoding"]` anywhere in the package (only the `encoding/json` import matches). `cp.value` calls `redactBareTokens` (scrub.go:228 → 1041-1054), which applies `highEntropyToken` (`\b[A-Za-z0-9+/=_-]{24,}\b`, line 75) gated by `looksSensitiveToken` (1544-1562: len>=24, letter+digit, entropy>=4.0). Empirical check (scratch Python, same formulas): even the 96-char 1x1-PNG base64 has entropy 4.39 with letters+digits → redacted, and the `\b` regex matches span [0,94), producing `[REDACTED]==` — invalid base64 exactly as claimed; 4KB binary bodies hit entropy 6.0. Production reachability: `scrub.HAR` with default Policy is called on the persistence/import paths at pkg/protocol/browser/har_capture.go:301 (Playwright HAR scrub-on-write; Playwright emits base64 binary content), pkg/convert/har/converter.go:73, pkg/api/handlers_recorder.go:441, pkg/api/handlers_browser_recorder.go:1737 (DevTools HAR uploads use `encoding:"base64"`). No test pins base64 content preservation. Prior-report check: WEEK_REVIEW_2026-07-02's entropy/JWT fixes added the hostname/low-entropy gates — distinct issue, not a regression; base64 binary legitimately passes those gates. Data-integrity defect, not security → medium stands.

</details>


#### 84. apexctl run --api polls forever with no overall deadline
**Where:** `cmd/apexctl/api_client.go:272` · **Area:** AREA-23 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

pollTest (cmd/apexctl/api_client.go:269-332) loops on a fixed 2s time.Sleep with no context, no overall timeout, and no flag to bound it; maxPollErrors=5 only bounds consecutive transport errors, so a controller that keeps answering 200 with a non-terminal status keeps the CLI alive indefinitely. `apexctl run` exposes no --timeout flag (parseRunArgs, main.go:253-360), unlike `apexctl wait` (30m default) and `apexctl ci run` (hard timeout + abort).

**Failure scenario:** A distributed run wedges non-terminal on the controller (e.g. generator fleet lost mid-run and the run stays RUNNING); a pipeline invoking `apexctl run --api ... --junit out.xml` hangs until the CI job's own wall-clock limit kills it, producing no JUnit evidence at all instead of a failed gate.

**Suggested fix:** Add a --timeout flag (default e.g. duration+30m) to run, thread a context into pollTest, and on expiry write the JUnit/JSON evidence with an execution error before exiting non-zero.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # C04 — `apexctl run --api` polls forever — CONFIRMED (medium)

cmd/apexctl/api_client.go:269-332: pollTest is an unbounded `for {}` with `time.Sleep(2s)`, no context/deadline; maxPollErrors=5 (line 267) only bounds *consecutive transport errors* and resets to 0 on any 200 (line ~292). Loop exits only on status COMPLETE/FAILED/ABORTED, so a run wedged in RUNNING keeps the CLI alive indefinitely. Called from the run --api path at api_client.go:172. parseRunArgs (main.go:253-360 case list: --script/--api/--api-key/--bearer-token/--base-url/--env/--format/--junit/--vusers/--duration/--ramp-up/SLA flags) has no --timeout. Contrast: `wait` defaults 30m (commands.go:29,63-67) and `ci run` has a hard timeout + ctx (ci.go:215,433). Causal chain holds.

</details>


#### 85. apexctl wait fails the CI gate on a single transient status-poll error
**Where:** `cmd/apexctl/api_client.go:364` · **Area:** AREA-23 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

WaitForTest (cmd/apexctl/api_client.go:357-379) returns immediately on any GetTestStatus error — no retry tolerance — while the sibling pollTest tolerates 5 consecutive errors (api_client.go:267, 283-290). One transient 502/timeout from an ingress during a potentially 30-minute wait aborts the command, and waitCmd (cmd/apexctl/commands.go:206-213) exits 1, the same exit code as a genuine SLA breach.

**Failure scenario:** A rolling ingress-controller restart drops one poll mid-wait; the pipeline's `apexctl wait --test <id> --junit results.xml` exits 1 with an ExecutionError JUnit while the load test is actually still running and ultimately passes — a false CI failure and an orphaned run nobody is watching.

**Suggested fix:** Apply the same consecutive-error tolerance (and ideally backoff) used by pollTest inside WaitForTest before giving up.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # C05 — `apexctl wait` fails on one transient poll error — CONFIRMED (medium)

cmd/apexctl/api_client.go:357-379: inside WaitForTest's loop, `test, err := c.GetTestStatus(testID); if err != nil { return nil, err }` — any single error (transient 502/timeout surfaces as error from GetTestStatus, api_client.go:337-354 returns error on any non-200) aborts immediately, with no retry counter. Sibling pollTest tolerates 5 consecutive errors (api_client.go:267,283-292), proving the intended pattern. waitCmd (commands.go:134-216) then hits `if runErr != nil { ... os.Exit(1) }` (~lines 206-213) — the same exit 1 as a genuine SLA breach — after writing an error JUnit. One dropped poll during a 30-minute wait therefore produces a false CI failure while the run continues. Causal chain holds.

</details>


#### 86. apexctl run silently ignores malformed and equals-form flags, silently disabling SLA gate...
**Where:** `cmd/apexctl/main.go:263` · **Area:** AREA-23 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

parseRunArgs (cmd/apexctl/main.go:253-360) is a hand-rolled switch over exact tokens: `--vusers=100` / `--p95-latency-ms=800` (equals form) match no case and are silently dropped; a malformed SLA value (`--p95-latency-ms 800ms`) fails strconv.ParseFloat at main.go:325 and is silently dropped; and `--duration 5min` at main.go:315 discards the ParseDuration error while overwriting the 60s default with 0. There is no unknown-flag error and no requireSLA backstop on this path (unlike ci.go, which validates everything).

**Failure scenario:** A pipeline runs `apexctl run --script s.json --p95-latency-ms=800 --max-error-pct=2 --junit out.xml`; both thresholds are silently ignored, resolveGoalConfig returns nil, the JUnit reports sla skipped ('no SLA thresholds configured'), and the process exits 0 — the performance gate is green with no SLA ever evaluated. The `--duration 5min` variant fails loudly locally (engine.New rejects Duration<=0 at pkg/engine/engine.go:125) but in --api mode sends duration "0s" to the controller.

**Suggested fix:** Replace parseRunArgs/parseCtlArgs/parseArchiveVerifyArgs with flag.FlagSet parsing (as ci.go already does), which handles --k=v, rejects unknown flags, and surfaces value-parse errors; exit 2 on any parse failure.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # C02 — apexctl run silently ignores malformed/equals-form flags — CONFIRMED (medium)

Chain verified at snapshot HEAD dc05008ab:

1. **Code exists / reachable**: `apexctlCommandHandlers["run"] = runTest` (cmd/apexctl/main.go:22) → `parseRunArgs(os.Args[2:])` (main.go:74). parseRunArgs (main.go:253-360) is an exact-token switch with **no default case**: `--p95-latency-ms=800` (equals form) matches no case and is silently dropped; malformed values discard errors — `strconv.ParseFloat` err ignored (main.go:325-328, 332, 339, 346, 353), and `r.duration, _ = time.ParseDuration(args[i])` (main.go:315) overwrites the 60s default with 0 on error.
2. **Consequence follows**: with all threshold pointers nil, `buildGoalConfigFromArgs` returns nil (sla.go:23-26); absent a script-level GoalConfig, `resolveGoalConfig` is nil, `evaluateLocalSLA` returns nil (sla.go:134-136). JUnit then emits testcase "sla" as **Skipped: "no SLA thresholds configured"** (junit.go:126-135), not a failure; text/JSON paths exit 0 when slaSummary is nil and runErr nil (main.go:181-184, 225-231). Gate is green with zero SLA evaluation.
3. **No refuting guard found**: no unknown-flag error, no requireSLA backstop on this path (unlike ci.go's FlagSet parsing).

**Prior-report check**: the 2026-07-16 fixed item (L2, "partial-evidence SLA output", Evaluated=false+Breached=true display) is a different defect in the same file — NOT a regression of this finding.

Severity medium stands: silent CI-gate disablement, but requires equals-form/malformed flag usage.

</details>


#### 87. Helm generator DNS target hardcodes the apexload namespace while everything else is names...
**Where:** `deployments/helm/apexload/values.yaml:48` · **Area:** AREA-23 · **Category:** helm-kustomize-parity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

values.yaml:48 defaults dataPlatform.generators to dns://apexload-generator.apexload.svc.cluster.local:9090 (literal 'apexload' namespace) and configmap.yaml:45 emits it verbatim, while the neighboring TLS server-name on configmap.yaml:52 is correctly templated with {{ include "apexload.namespace" . }} — so installing the chart with namespace.name != apexload leaves the controller dialing a service in the wrong namespace even though every other resource follows the configured namespace.

**Failure scenario:** Operator installs with namespace.name=perf-tools; controller starts cleanly (config validation passes), generator discovery resolves nothing (or a foreign cluster's apexload namespace), every distributed run fails to stage with 'no generators available', and the operator hunts through gRPC/TLS settings because the templated server-name looks right.

**Suggested fix:** Template the default in configmap.yaml: when .Values.dataPlatform.generators is empty, emit dns://apexload-generator.{{ include "apexload.namespace" . }}.svc.cluster.local:9090; keep the value override for external fleets.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # C06 — Helm generators URL hardcodes apexload namespace — CONFIRMED (medium)

deployments/helm/apexload/values.yaml:48: `generators: dns://apexload-generator.apexload.svc.cluster.local:9090` (literal namespace); values.yaml:1-3 makes namespace.name configurable (default apexload). templates/configmap.yaml:45 emits `{{ .Values.dataPlatform.generators | quote }}` verbatim, while configmap.yaml:52 and certificates.yaml:55-56 correctly template `{{ include "apexload.namespace" . }}` (helper = `default .Release.Namespace .Values.namespace.name`, _helpers.tpl:1-3). The generator Service (templates/generator.yaml:10-11) is named apexload-generator in the templated namespace. No other values/overlay file supplies dataPlatform.generators (chart contains only values.yaml + values.schema.json). Installing with namespace.name != apexload leaves the controller dialing the wrong namespace by default. Confirmed at medium.

</details>


## Low (51)

#### 88. collectorMetricStore never evicts snapshots for deleted collectors
**Where:** `pkg/api/collector_metrics.go:59` · **Area:** AREA-01 · **Category:** resource-leak · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

collectorMetricStore only has put/get (pkg/api/collector_metrics.go:59-70) and the only writers are the heartbeat handler (collector_metrics.go:157) and reader (line 180) -- verified there is no delete call anywhere (pkg/api/server.go:127,441 are the only other references). Deleting a monitoring collector removes its durable row but its last in-memory snapshot (up to maxCollectorHeartbeatBytes=256 KiB of decoded packs, collector_metrics.go:28-31) stays in the map until process restart, so memory grows monotonically with collector churn.

**Failure scenario:** A team provisions ephemeral collectors from CI (one per pipeline run, deleted afterwards) that each push a full 64-pack heartbeat before deletion. Over months, thousands of dead collector snapshots (~100-250 KB each) accumulate in s.collectorMetrics, adding hundreds of MB of unreclaimable heap to a long-lived controller until it is restarted.

**Suggested fix:** Add a delete(id) method and call it from the collector-deletion handler; optionally sweep entries whose ReceivedAt is older than a retention window as defense in depth.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V007 — CONFIRMED (low)

collectorMetricStore (pkg/api/collector_metrics.go:50-70) exposes only put and get; no delete method exists. Full-repo grep for collectorMetrics shows exactly four non-test references: server.go:127 (field), server.go:441 (init), collector_metrics.go:157 (put in heartbeat handler), collector_metrics.go:180 (get in read handler). handleDeleteMonitoringCollector (pkg/api/handlers_monitoring_collectors.go:153-179) deletes the durable row via DeleteMonitoringCollectorFromProject but never evicts the in-memory snapshot, so a deleted collector's last heartbeat payload (bounded per collector by maxCollectorHeartbeatBytes=256 KiB, collector_metrics.go:28) remains in s.collectorMetrics.snapshots until process restart. Memory grows monotonically with collector churn. Leak is real but slow and bounded per entry; low severity appropriate.

</details>


#### 89. broadcastEvent reads live ManagedTest fields after tr.mu is released, racing with locked w
**Where:** `pkg/api/testexecution.go:2662` · **Area:** AREA-01 · **Category:** concurrency-data-race · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

Several lifecycle paths release tr.mu and then call tr.broadcastEvent(eventType, test) with the live pointer -- test_paused at pkg/api/testexecution.go:2662, test_resumed at 2816, test_aborted at 1094, test_failed at 3243 -- and broadcastEvent reads test.Status/test.Name/test.ProjectID without any lock (pkg/api/metrics.go:957-980). A concurrent request mutating the same test under tr.mu (e.g. an immediate ResumeTest writing Status at testexecution.go:2774, or attribution writes at persistence.go:1462) races with those unlocked reads. Other sites already avoid this by snapshotting under the lock (cloneManagedTestFieldsLocked at testexecution.go:3189; stoppingSnapshot at 1181-1183), so the remaining raw-pointer sites are inconsistent stragglers.

**Failure scenario:** Operator pauses a test; while the pause path is between tr.mu.Unlock (testexecution.go:2660) and the broadcastEvent field reads, a second request resumes the test and writes test.Status='RUNNING' under tr.mu. go test -race (or a race-enabled staging build) reports a data race on ManagedTest.Status, failing CI; in the worst theoretical case the unsynchronized string read is torn and the WebSocket event carries a corrupt status.

**Suggested fix:** Capture an event snapshot (cloneManagedTestFieldsLocked or a small value struct of ID/Name/ProjectID/Status) before unlocking at each remaining raw-pointer broadcast site, mirroring the test_started path.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V006 — confirmed (low), trigger corrected

Chain: PauseTest writes test.Status="PAUSED" under tr.mu (pkg/api/testexecution.go:2638), unlocks at 2660, then broadcastEvent("test_paused", test) at 2662 reads test.Status/Name/ProjectID with NO lock (pkg/api/metrics.go:957-968). Same pattern at 2816 (resumed), 3243 (failed, unlock at 3238), 3336/3347/3349 (unlock at 3323).

The finding's named racing writers are refuted: ResumeTest (2774) cannot race — PauseTest holds lockTestLifecycleControl (acquired 2572, deferred release 2576 runs AFTER the broadcast) and ResumeTest blocks on the same per-test lock at 2674. Attribution writes (persistence.go:1462) touch CreatedBySubject/DisplayName — fields broadcastEvent never reads; not the same memory location, so no race.

But a real unguarded writer exists: the local run goroutine (execution_backend.go:165) calls completeTestRunUnderArchiveFenceWithPendingPromotion WITHOUT the lifecycle lock; it writes test.Status=finalStatus under tr.mu (testexecution.go:3309), and PAUSED is not excluded by the terminal-status switch (3291-3294). So a run finishing while a pause broadcast is in flight is a genuine -race-detectable read/write race on ManagedTest.Status. Consequence: race-detector CI failure / stale status in the event — benign in practice. Prior W17 (WEEK_REVIEW line 215) was LiveMetrics in-place mutation — different defect, not a regression.

</details>


#### 90. Run-calendar truncation keeps an arbitrary append-order subset instead of the events neare
**Where:** `pkg/api/run_calendar.go:269` · **Area:** AREA-02 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

BuildRunCalendar enforces maxEvents inside appendEvent during per-source iteration (materialized occurrences → ad-hoc tests → scenario projections → plan occurrences → plan projections) and only sorts by StartAt afterwards (pkg/api/run_calendar.go:269-278, 351-356). When the window holds more events than the cap (runCalendarMaxEvents=2000 or a smaller ?limit, line 247-250), the surviving subset is determined by source order and snapshot iteration order, not by time — later sources (execution-plan projections) are dropped wholesale while every earlier-source event survives.

**Failure scenario:** A busy project requests a 30-day window containing 1,900 materialized occurrences plus 400 projected execution-plan runs: the response holds all 1,900 occurrences and only ~100 of the other events with Truncated=true; the calendar renders later days as empty even though scheduled plan runs exist there, and a client passing limit=200 receives an essentially arbitrary 200 events.

**Suggested fix:** Accumulate all overlapping events (bounded by the existing per-cron projection caps), sort by StartAt, then truncate to maxEvents so the kept subset is the earliest events in the window.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V011 — CONFIRMED (low)

Chain (all snapshot @ dc05008ab):
1. pkg/api/run_calendar.go:269-278 — `appendEvent` returns without appending once `len(events) >= maxEvents` (sets `truncated=true`); cap = `runCalendarMaxEvents=2000` (line 17) or a smaller `?limit` (lines 247-250).
2. Sources are appended strictly sequentially: materialized occurrences (280-292) → ad-hoc tests (294-302) → scenario-schedule projections (304-319) → plan occurrences (331-337) → plan projections (338-349). Truncation therefore drops later sources wholesale while every earlier-source event in the window survives.
3. Sort by StartAt happens only AFTER truncation (351-356) — nothing reorders before the cap is applied; no other guard, retry, or time-nearest selection exists in the function.
4. Reachable in production: GET /api/v1/run-calendar registered at pkg/api/route_registry_scheduling.go:50-51 → handleGetRunCalendar parses `limit` from the query (run_calendar.go:152-157) and calls BuildRunCalendar (162, via scheduling_service.go:138-139).
5. Realistic trigger: `runCalendarMaxProjectedPerCron=1000` (line 18) lets two busy cron schedules alone exceed 2000, and any client-supplied small `?limit` hits the cap trivially — kept subset is append/iteration order, not the window's earliest events.

Mitigant: `Truncated=true` is surfaced (line 370), so clients know the list is incomplete — consequence is misleading-but-flagged display, consistent with low severity.

Prior-report check: W18 (windowed occurrence DB query, fixed) and PERF-27 (manifest parse per request, open) are different issues — not a regression.

</details>


#### 91. Browser diagnostics download fails outright past 5,000 events instead of returning a trunc
**Where:** `pkg/api/archive_browser.go:52` · **Area:** AREA-03 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

buildRunArchiveBrowserDiagnosticsContext returns an error once a run has more than maxBrowserDiagnosticEvents=5000 browser transactions (pkg/api/archive_browser.go:42, 52-53) or 128MiB of payload (:121-124), so handleDownloadRunArchiveBrowserDiagnostics responds 500 with no partial bundle — the limit converts a large-but-valid archive into a completely unretrievable diagnostics set via this endpoint.

**Failure scenario:** A 2-hour run with browser canaries emitting ~1 diagnostic transaction/second archives ~7,200 browser events. GET /api/v1/tests/{id}/archive/browser-diagnostics always returns 500 'browser diagnostics exceed bounded event limit of 5000'; the user gets zero screenshots/HTML/console evidence from the endpoint even though every event is individually small and the archive is intact.

**Suggested fix:** Stop collecting at the cap instead of erroring: mark bundle.Overview (e.g. TruncatedEvents count) and return the bounded bundle; prioritize failed transactions if truncating.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V020 — CONFIRMED (low)

pkg/api/archive_browser.go:42-53: once len(bundle.Events) >= maxBrowserDiagnosticEvents (5,000) the per-transaction callback returns fmt.Errorf — not a stop sentinel — and :121-124 does the same at the 128MiB payload cap. archive_loaders.go:505-507 propagates any callback error, so buildRunArchiveBrowserDiagnosticsContext returns nil, err (:127-129); no partial bundle survives. handlers_archive.go:243-253 wraps that into writeError 500 on GET .../archive/browser-diagnostics. A run with >5,000 browser-diagnostic transactions therefore always 500s on this endpoint with zero artifacts, though the archive itself is intact and other views remain. Bounded-limit-errors-instead-of-truncates; scope limited to one download endpoint — severity low.

</details>


#### 92. SOAP fault timeline bucket permanently drops by-code (or by-category) counts after a first
**Where:** `pkg/api/archive_report_aggregate.go:383` · **Area:** AREA-03 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

mergeArchiveSOAPFaultTimelineBucket nils a bucket's ByCode/ByCategory map whenever it is empty after a merge (pkg/api/archive_report_aggregate.go:383-388). Because incrementArchiveCount is a no-op on a nil map (:351-356), a minute bucket whose first fault carries only a category (empty code) sets ByCode=nil forever: every later fault code in that minute is silently uncounted while bucket.Total still increments, and vice versa for ByCategory.

**Failure scenario:** Within one minute a run records fault #1 with SOAPFaultCategory='client' and no code, then fault #2 with SOAPFaultCode='soap:Server'. The report's soap_fault_timeline bucket shows total=2, by_category={client:1}, and by_code omitted entirely — the soap:Server code disappears from the timeline breakdown (the run-level BySOAPFaultCode map is unaffected, so timeline and totals visibly disagree).

**Suggested fix:** Keep the maps non-nil during accumulation and nil empty maps once at finalize (where SOAPFaultTimeline is copied out), mirroring finalizeArchiveSOAPPayloadTimelineBucket.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V019 — CONFIRMED (low)

pkg/api/archive_report_aggregate.go:371-388: a new minute bucket is created with non-nil ByCode/ByCategory, but after every merge lines 383-388 set any empty map to nil. incrementArchiveCount (:351-356) early-returns when the map is nil. So a first fault with category-only (code=="", passes the :368 guard) leaves ByCode empty, which is nil'd; a later same-minute fault with a code increments nothing while bucket.Total++ (:380) still runs. No repair downstream: archive_report.go:327-338 merely copies buckets into report.SOAPFaultTimeline, and archive.go:490-491 tags ByCode `omitempty`, so by_code vanishes from JSON while total disagrees. Merge is invoked per transaction (archive_report.go:133). Chain holds; report-display inaccuracy only — severity low.

</details>


#### 93. Single-slot global Bruno inspect semaphore holds its exclusive section across the request-
**Where:** `pkg/api/handlers_bruno_import.go:232` · **Area:** AREA-04 · **Category:** performance-scalability · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

brunoInspectConcurrency is a fixed capacity-1 channel (pkg/api/handlers_bruno_import.go:149) acquired at the top of handleInspectBrunoImport (lines 232-238) BEFORE readBrunoInspectInput reads the request body (line 240) and before brunoconvert.Inspect (line 251) and the two encrypted artifact PutBytes writes (lines 405-414). The exclusive section therefore includes the client's upload over the network (limit 272 MiB per brunoImportMaxBodyBytes at pkg/api/route_catalog.go:603), so one slow-uplink client serializes every other Bruno inspection on the whole controller for the duration of their upload; queued requests wait indefinitely (no timeout, only client-context cancel).

**Failure scenario:** User A starts inspecting a 200 MB Bruno collection over a 10 Mbps uplink (~3 minutes of body transfer). User B's 2 KB single-.bru inspect request blocks in the semaphore select for those minutes with no server-side timeout, appearing hung in the dashboard; with three queued users the delays compound. The comparable enterprise-archive gate allows 2 concurrent conversions and returns 429 when busy (handlers_import_enterprise.go:22-35), while Bruno inspect both serializes to 1 and queues silently.

**Suggested fix:** Read and buffer the (already MaxBytesReader-bounded) body before acquiring the slot, so the exclusive section covers only conversion/artifact publication; optionally raise capacity to a small configurable N and fail fast with 429+Retry-After like acquireEnterpriseImportSlot instead of unbounded queuing.

<details><summary>Verification evidence</summary>

**downgraded @ low:** # V022 — Bruno inspect semaphore held across body read: DOWNGRADED (medium → low)

Code facts confirmed: `brunoInspectConcurrency = make(chan struct{}, 1)` (pkg/api/handlers_bruno_import.go:149); slot acquired at handlers_bruno_import.go:232-238 BEFORE `readBrunoInspectInput` fully reads the body (line 240 → :2976-3010, decodeRequestBody/io.ReadAll) and held via defer through `brunoconvert.Inspect` (:251) and both PutBytes writes; waiters queue in `select` with no 429 (contrast enterprise gate: cap 2 + immediate 429, handlers_import_enterprise.go:22-35). Route is production-wired (pkg/api/server.go:910); global-role editors skip the pre-handler ProjectLookup body read (middleware.go:379-381), and multipart resolution peeks only 256KiB (access_resolvers.go:1150-1158), so the upload genuinely occurs inside the critical section. Limit constant is at middleware.go:603 (finding miscited route_catalog.go:603).

Refuting guard the finding missed: the API server sets `ReadTimeout: 15 * time.Second` (pkg/api/server.go:561), which in Go bounds reading the ENTIRE request including body. A 200MB/10Mbps ~3-minute upload fails ~15s in; the handler errors and releases the slot. So one slow uplink can hold the slot ≤~15s, not minutes, and waits are not "indefinite". Residual real defect: serialization across conversion+artifact I/O with silent queuing, and waiters queued >15s hit their own expired read deadline and fail body reads with an opaque error instead of a 429. Not a regression of prior AUTHZ-1/SYNC-MERGE-1 (different symbols/issues).

</details>


#### 94. java_vuser launch gating performs a fleet-metadata DB read and placement simulation under 
**Where:** `pkg/api/distributed_execution_backend.go:641` · **Area:** AREA-05 · **Category:** performance-lock-scope · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

enrichGeneratorTargetSpecsWithLabels executes tr.fleetStore.ListGeneratorMetadata(0,0) — an unbounded full-table SQLite read (confirmed limit=0 means no LIMIT in pkg/storage/sqlite_fleet.go:458-468) — and is called from distributedJavaVuserGroupRunnerAvailableLocked (pkg/api/external_runner_gating.go:514) and distributedJavaVuserRunnerAvailableLocked (490), which run while the caller holds the global TestRunner mutex (call sites: testexecution.go:564, testcreation.go:575, launch_preflight.go:128, testexecution.go:3585). Blocking DB I/O plus a full placement simulation (chooseGeneratorShardsForGroup) inside tr.mu stalls every other API request that touches runner state for the duration of the query.

**Failure scenario:** Controller under load with SQLite on slow/contended disk (checkpoint in progress) and a few hundred generator-metadata rows. A user creates/starts a java_vuser test; the preflight holds tr.mu through the metadata query and placement simulation (tens to hundreds of ms). During that window every list-tests/live-metrics/status API call blocks on tr.mu, producing visible latency spikes correlated with java test launches. This is the repo's known locks-over-DB-I/O antipattern.

**Suggested fix:** Cache the labels-by-target index with a short TTL (like liveInventory) refreshed outside tr.mu, or snapshot the needed test fields and run the gating check before acquiring tr.mu.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V029 — CONFIRMED (low)

enrichGeneratorTargetSpecsWithLabels calls tr.fleetStore.ListGeneratorMetadata(0,0) (distributed_execution_backend.go:641); SQLite ListGeneratorMetadata adds LIMIT only when limit>0 (sqlite_fleet.go:458-468), so limit=0 is a full-table read. distributedJavaVuserGroupRunnerAvailableLocked calls it unconditionally (external_runner_gating.go:514) plus chooseGeneratorShardsForGroup placement simulation (551-561); reached via javaVuserRunnerLaunchConflictsForManagedTestLocked (240). All call sites hold tr.mu: evaluateStartGatesLocked contract "caller must hold tr.mu" (testexecution.go:544-549, call at 564), buildManagedTestLocked under tr.mu.RLock (launch_preflight.go:110-128; testcreation.go:575), and pending promotion holds the write lock explicitly (testexecution.go:3560-3590). Prior-report check: PERF_REVIEW_2026-07-06.md item 65 is a different defect (Run() serial dial/probe/script distribution under a shared 10s deadline, line 534), not this DB-read-under-tr.mu gating path — no regression, not a duplicate. Low confirmed (java_vuser launches only).

</details>


#### 95. Startup-failure cleanup stops assigned generators sequentially, so a wide-fleet failure ca
**Where:** `pkg/api/distributed_execution_backend.go:736` · **Area:** AREA-05 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

The cleanup closure in Run() iterates startedTargets sequentially, issuing stopGeneratorTest with a fresh b.timeout (default 10s) context per target (pkg/api/distributed_execution_backend.go:736-747). Every other stop fanout in this file is parallel (stopTestContext:1339-1360, forceStopDistributedTargets:568-584, commit:1209-1228). With up to maxDistributedTargetsPerRun=256 targets, a late startup failure (e.g. metrics-stream open failure at 1038-1042) during a fleet-wide network incident performs up to 256 sequential 10s-timeout stops (~43 minutes) before failTestBeforeRunAndLog marks the test FAILED.

**Failure scenario:** 200-generator run; all AssignWork calls succeed, then the network partitions before metrics streams open. failStartup -> cleanup(true) issues 200 sequential StopTest RPCs that each burn the full 10s deadline against unreachable generators. The test remains visibly INITIALIZING (not FAILED) for ~33 minutes, cannot be meaningfully interacted with, and holds its capacity accounting the whole time. Expected: the same bounded parallel fanout used by stopTestContext, completing within ~1-2 timeouts.

**Suggested fix:** Reuse the parallel stop-fanout pattern (goroutine per target, buffered results channel) inside cleanup, or cap total cleanup wall time with one shared deadline.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V030 — CONFIRMED (low)

Cleanup closure (pkg/api/distributed_execution_backend.go:736-747) iterates `startedTargets` in a plain sequential `for`, each iteration creating a fresh `context.WithTimeout(context.Background(), b.timeout)` (default 10s, lines 245-246) for `stopGeneratorTest`. `context.Background()` means the stop is not cancelable by executionCtx, so each unreachable generator burns up to the full timeout. `startedTargets` is populated per assigned shard target before AssignWork (line 981, deliberate ambiguous-commit tracking per comment 977-980), up to maxDistributedTargetsPerRun=256 (line 21). A late failure — e.g. metrics-stream open at 1038-1041 or prepare at 1046-1049 — triggers failStartup, which runs cleanup(true) BEFORE failTestBeforeRunAndLog (756-759), so the test stays INITIALIZING for O(N×10s). Contrast: stopTestContext fanout is goroutine-per-target parallel (1333-1360). Prior PERF_REVIEW item 65 (status open) covers the serial placement/distribute path — discovery is now worker-pool parallel (767-815) — but not this cleanup loop; distinct issue, not a regression.

</details>


#### 96. Evacuate endpoint pre-applies draining+cordon, then reports Applied=false on refusal while
**Where:** `pkg/api/handlers_fleet_evacuate.go:93` · **Area:** AREA-05 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleEvacuateGenerator marks the generator draining and persists the metadata cordon BEFORE computing evacuation impacts (pkg/api/handlers_fleet_evacuate.go:93-102). When EvacuateGenerator refuses (stateful protocol, no survivor capacity, pre-baseline window), the handler returns 409 with Applied=false and an error (117-125), but the drain state and cordon metadata remain applied — the response contradicts the actual state change, and the generator silently stops receiving new placements until an operator notices and uncordons.

**Failure scenario:** Operator POSTs /fleet/generators/gen-a/evacuate (not dry-run) while gen-a hosts a java_vuser group. Evacuation is refused ('java_vuser VUsers are stateful...'), response says applied=false. gen-a is nevertheless now draining+cordoned; subsequent runs skip it and fleet capacity views show it excluded, though the operator reasonably concluded from applied=false that nothing changed.

**Suggested fix:** Either run a dry-run evaluation first and only drain when at least one impact is applicable, or keep the pre-drain but report it honestly (e.g. applied_drain=true, evacuated=false) and document that a refused evacuation leaves the generator draining.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V028 — CONFIRMED (low)

Non-dry-run path pre-applies state: handlers_fleet_evacuate.go:93-101 sets drain "draining" (setGeneratorLifecycleState) and persists cordon metadata (syncGeneratorMetadataCordon) before any impact evaluation. EvacuateGenerator returns an error whenever any group is refused (distributed_resilience.go:334-336, "generator evacuation refused: ..."), including java_vuser/browser/sap_gui stateful groups (49-53 via 242-244), pre-baseline window (255-257), and no-survivor-capacity (267-272). On that error the handler sets response.Applied=false and returns 409 (handlers_fleet_evacuate.go:117-124) with no revert of the drain state or cordon metadata — nothing else in the file or handler path uncordons. So the response asserts nothing was applied while the generator persists draining+cordoned. API-contract mismatch confirmed; low severity appropriate (operator-recoverable via uncordon).

</details>


#### 97. isAIGeneratedDraft matches the bare substring "ai" in generated_by, misclassifying manual 
**Where:** `pkg/api/authoring_ai_guardrails.go:57` · **Area:** AREA-06 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

isAIGeneratedDraft (authoring_ai_guardrails.go:55-63) returns true when generated_by merely CONTAINS "ai" (or import source contains "llm"). generated_by is a free-form client-suppliable field (dsl.Script.GeneratedBy); values like "maintainer-tool", "main-suite-gen", or "aisha" trip the check. A misclassified draft gets an AIGuardrailReport whose diff_review and replay_before_save items are "blocked" (buildAIGuardrailReport:25-41), which drives AuthoringHealth to blocked and makes handleApproveAuthoringSession return 400 with the validation payload (handlers_authoring_sessions.go:542-561), so the session cannot be approved without attaching bogus AI evidence.

**Failure scenario:** A team's script generator stamps generated_by="maintainer-tool" (contains "ai") on a hand-maintained HTTP script. The authoring session shows ai_guardrails blockers ("AI-generated drafts require human diff-review evidence"), approval returns 400, and the operator must forge preserved diff_reviewed/replay_validated metadata to promote a script that was never AI-generated.

**Suggested fix:** Match structured markers only: generated_from == "ai_generated", import source == "ai_generated", or generated_by having the exact "apexload-ai-" prefix — drop the bare substring checks.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V036 — CONFIRMED (low)

pkg/api/authoring_ai_guardrails.go:57: `strings.Contains(generatedBy, "ai")` — bare substring; "maintainer-tool" (m-ai-ntainer) trips it. `GeneratedBy` is client-suppliable free text (pkg/dsl/types.go:79, json `generated_by`). Once misclassified, `buildAIGuardrailReport` marks diff_review/replay_before_save "blocked" absent preserved evidence (:28-29, aiGuardrailItem fail→blocked :48). `buildAuthoringHealth` maps aiGuardrails blocked>0 to Status "blocked" (pkg/api/authoring_capabilities.go:139-140). `handleApproveAuthoringSession` rejects with 400 when `AuthoringHealth.Status == "blocked"` (pkg/api/handlers_authoring_sessions.go:542-561). Chain complete: a manual script whose generator stamp merely contains "ai" cannot be approved without forging AI evidence. Low severity fits — workaround is renaming generated_by; no data loss.

</details>


#### 98. Capabilities endpoint reports the ai_generated authoring source as "planned" while the AI 
**Where:** `pkg/api/authoring_capabilities.go:533` · **Area:** AREA-06 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The authoring capability catalog marks Source "ai_generated" with Status "planned" and a note that "Generation guardrails are not complete" (authoring_capabilities.go, ai_generated entry near the file tail), while POST /api/v1/ai/drafts is fully implemented (handleAIDraftGeneration, ai_assistance.go:374-434, creates real authoring sessions) and GET /api/v1/ai/authoring/contract reports Status "active" with guardrails enforced (ai_assistance.go:265). Two server-owned discovery endpoints disagree about the same feature's availability, so a client that honors the capabilities contract hides or disables a working workflow.

**Failure scenario:** An external tool (or the dashboard's import-surface gating) reads /scripts/authoring/capabilities to decide which acquisition sources to offer, sees ai_generated=planned, and hides AI draft generation; users conclude the documented AI workflow is unavailable even though POST /api/v1/ai/drafts works and is audited as active.

**Suggested fix:** Update the ai_generated capability entry to active (or derive its status from the same policy check enforceAIPolicy uses) and drop the stale note.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V037 — CONFIRMED (low)

Contradiction verified between two served discovery endpoints. Capabilities catalog: pkg/api/authoring_capabilities.go:531-549 — Source "ai_generated", `Status: "planned"` (:534) with note "Generation guardrails are not complete" (:548); served at GET /api/v1/scripts/authoring/capabilities (pkg/api/server.go:854). AI contract: `buildAIAuthoringContract` returns `Status: "active"` (pkg/api/ai_assistance.go:265), served via handleAIAuthoringContract (:257-259). The workflow is genuinely live: POST /api/v1/ai/drafts routed (server.go:934, route_catalog.go:212) to handleAIDraftGeneration (ai_assistance.go:374+) which builds drafts and authoring sessions; guardrails are in fact complete (buildAIGuardrailReport + approval gate at handlers_authoring_sessions.go:542), so the "planned"/note text is stale. Low: discovery-metadata inconsistency only.

</details>


#### 99. Replay-session start endpoint lacks the credential-preserving-import echo special case its
**Where:** `pkg/api/handlers_script_debug.go:181` · **Area:** AREA-06 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

handleStartScriptDebugScript (handlers_script_debug.go:110-129) detects that a request body script is the unchanged disclosure projection of a credential-preserving import (matchesStoredScriptDisclosureProjection) and substitutes the durable server-owned snapshot with trustedPreservedImport=true. handleStartReplaySessionScript (181-198) — the sibling endpoint for the same body shape — omits that check: when req.Script is present it always takes the untrusted path, where prepareTransientExecutableScript (handlers_replay.go:801-810) re-projects the already-masked script and discoverSecretBindingRequirements (secret_bindings.go:110-124) raises binding requirements for every [REDACTED] sentinel, failing the request with secret-bindings-required. Identical request bodies therefore succeed on the debug endpoint and 4xx on the replay endpoint. handleReplayScript (handlers_replay.go:101-112, out of scope) has the same gap.

**Failure scenario:** Operator opens a credential-preserving imported script (e.g. Bruno/LR import that retained credentials), makes no changes, and triggers a replay through a client that echoes the editor's GET copy in the request body (the same client behavior the debug endpoint explicitly accommodates). POST /scripts/{id}/replay-sessions returns 4xx demanding secret bindings for values that are actually present in the durable script, while the identical body on POST /scripts/{id}/debug/sessions replays fine. The user is misled into re-entering or binding credentials that already exist; replay of unchanged imports appears broken depending on which button they used.

**Suggested fix:** Extract the isCredentialPreservingImport + matchesStoredScriptDisclosureProjection substitution from handleStartScriptDebugScript into a shared helper and apply it in handleStartReplaySessionScript (and handleReplayScript).

<details><summary>Verification evidence</summary>

**downgraded @ low:** # V034 — verdict: downgraded (defect real, trigger narrower than claimed)

Chain confirmed statically. pkg/api/handlers_script_debug.go:118-126: debug endpoint substitutes the durable snapshot when `isCredentialPreservingImport(stored.Script) && matchesStoredScriptDisclosureProjection(script, stored.Script)`, setting trustedPreservedImport=true. Sibling handleStartReplaySessionScript (handlers_script_debug.go:181-192) omits this: req.Script present -> trusted stays false. Both funnel into handleStartScriptDebugRequest -> prepareTransientExecutableScript (handlers_script_debug.go:211-215; handlers_replay.go:773-810), whose untrusted branch runs maskScriptSecrets + discoverSecretBindingRequirements -> writeSecretBindingsRequired 4xx (handlers_replay.go:801-808). discoverRedactedValues raises one requirement per [REDACTED] occurrence (secret_bindings.go:208-235), and the GET projection of a preserving import contains sentinels (handlers_script_debug_test.go:966-968). Route wired: server.go:877 POST /api/v1/scripts/{id}/replay/sessions; documented (apidocs/api-endpoint-catalog.json:4248). matchesStoredScriptDisclosureProjection called nowhere else (grep). Test coverage pins only the debug endpoint (TestSavedImportedScriptDebugUsesDurableCredentialsForUnchangedProjection).

Refuting evidence on trigger: the dashboard never calls this endpoint — ScriptEditor.tsx:2767 routes both debug AND replay through /scripts/{id}/debug/sessions (the fixed endpoint) with the script echo (scriptEditorUtils.ts:730-756 always includes `script`); replayScript hook (useScriptApi.ts:509) is unused outside tests (grep). So "which button they used" scenario is wrong for the shipped UI; only external REST clients that echo the GET projection hit the 4xx, and the failure is fail-closed with an actionable requirements list. handleReplayScript (handlers_replay.go:101-112) shares the gap, also dashboard-unreached. Not a regression of prior fixed item 8 (java_vuser/session.go — unrelated file/subject).

</details>


#### 100. Replay progress never refreshes session activity, so an actively-running or breakpoint-pau
**Where:** `pkg/api/script_debug_manager.go:664` · **Area:** AREA-06 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

cancelIdleRunningSessionLocked (script_debug_manager.go:664-677) stops Running/Paused sessions when now-lastActivityAt exceeds retention.idleCancel (default 30m, replay_session_retention.go:18). lastActivityAt is refreshed only by REST accessors (getForProject:951, updateStatus:1143, stopForProject:1202, etc.); the replay progress path (handleReplayProgress:828-904), BeforeAction/AfterAction pause loops, and correlation-evidence observation never touch it. A session that is demonstrably alive — streaming script_debug_event broadcasts or parked at a breakpoint the user is inspecting — is cancelled by the janitor (StartJanitor ticks at most every minute) with "Debugger session stopped after idle timeout" unless some client happens to poll a REST endpoint.

**Failure scenario:** Operator starts a browser-protocol debug replay honoring recorded think times (ignore_think_time=false) whose single-VU pass takes ~45 minutes, and watches exclusively via the WebSocket push feed (no REST polling). At the 30-minute mark the janitor's cancelIdleRunningSessionLocked stops the session and cancels the worker: the replay aborts mid-flow with status stopped/"idle timeout" despite continuous progress events, losing the run and its correlation evidence (rejectCorrelationEvidenceLocked fires on stop). Same for a session paused at a breakpoint while the operator inspects state for >30 minutes.

**Suggested fix:** Treat engine progress as activity: set session.lastActivityAt = clock() inside handleReplayProgress (cheap, already under session.mu), or exempt sessions whose CurrentEventIndex advanced since the last janitor sweep.

<details><summary>Verification evidence</summary>

**downgraded @ low:** # V035 — verdict: DOWNGRADED (medium → low)

Mechanism confirmed at HEAD:
- pkg/api/script_debug_manager.go:664-677 cancelIdleRunningSessionLocked stops Running/Paused sessions when now−lastActivityAt > retention.idleCancel (default 30m, pkg/api/replay_session_retention.go:18; janitor wired pkg/api/server.go:1379, tick ≤1m via replaySessionJanitorInterval:277).
- grep confirms lastActivityAt is written only at lines 349, 671, 951, 993, 1081, 1143, 1202, 1264, 1301 — all REST accessors. handleReplayProgress (828-904) and broadcast (1375-1406) never touch it; WS auth uses LookupProjectID which by design "returns the current capability owner without extending the session's idle lifetime" (906-908).
- Tests (handlers_script_debug_test.go:654,690) pin idle-cancel of Running/Paused sessions as intended reclamation.

Refuting guard that narrows impact: the shipped dashboard polls GET /api/v1/replay/sessions/{id} every 1.2s while a debug session is active (dashboard/src/components/ScriptEditor.tsx:3455-3562), which hits handleGetScriptDebugSession (server.go:949) → getForProject → lastActivityAt=now (script_debug_manager.go:951). So the headline scenario (operator watching the shipped UI loses a 45-min run / breakpoint inspection) cannot occur with the debugger panel open. Only custom WebSocket-only API consumers, or runs left after closing the editor, hit the mid-run stop.

Prior items 37/38 (UNCOMMITTED_REVIEW_2026-07-22.md:664,678) are different defects — not a regression.

</details>


#### 101. Application/release 'optimistic' updates use a server-loaded token, so stale renames silen
**Where:** `pkg/api/handlers_applications.go:102` · **Area:** AREA-07 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleUpdateApplication calls UpdateApplicationIfUnchanged(application, application.UpdatedAt) where application (and its UpdatedAt) was loaded inside the same handler (handlers_applications.go:81, 102); handleUpdateRelease does the same (handlers_applications.go:301, 322). The CAS therefore only guards the microseconds inside the handler, not the client's read-modify-write window — precisely the anti-pattern the data-pool handler documents and forbids ('Substituting the freshly loaded value here would only guard the in-handler window and let a stale editor silently overwrite changes', handlers_data_pools.go:289-291). The API contract is inconsistent: pools require the client token (400 without it), applications/releases ignore client staleness entirely.

**Failure scenario:** Operator A and operator B both load application 'Payments'. A renames it to 'Payments-EU' (persisted). B, still holding the old view, renames it to 'Payments-v2'; B's PUT reloads the row server-side, passes its own fresh UpdatedAt to the CAS, and silently overwrites A's rename — last-write-wins despite the storage layer supporting real optimistic concurrency.

**Suggested fix:** Accept updated_at in the request bodies (required, like data pools) and pass the client token to UpdateApplicationIfUnchanged/UpdateReleaseIfUnchanged, returning 409 on mismatch.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V044 — CONFIRMED (low)

handleUpdateApplication loads the row server-side (pkg/api/handlers_applications.go:81) and its request struct accepts only `Name` (lines 86-88) — no client concurrency token exists in the API. Line 102 then calls `UpdateApplicationIfUnchanged(application, application.UpdatedAt)`, passing the freshly loaded timestamp, so the CAS only guards the microseconds between line 81 and 102, not the client's read-modify-write window. handleUpdateRelease is identical (load at 301, self-token CAS at 322, Name-only body at 306-308). handlers_data_pools.go:288-294 documents this exact substitution as the forbidden anti-pattern and requires the editor's `updated_at` (400 if zero, line 291-293). A stale editor's rename therefore silently wins (last-write-wins) despite storage supporting real optimistic concurrency. Impact limited to name fields → severity low stands.

</details>


#### 102. Data-pool update mixes PATCH and PUT semantics: archived_at is absent-preserving but omitt
**Where:** `pkg/api/handlers_data_pools.go:262` · **Area:** AREA-07 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

handleUpdateDataPool deliberately uses json.RawMessage for archived_at so an absent field preserves state (handlers_data_pools.go:253-256), but unconditionally assigns existing.Description = req.Description and existing.Schema = req.Schema (handlers_data_pools.go:263-264), so a request that omits those fields clears them. The endpoint therefore invites PATCH-style requests (that is the documented archived_at contract) while silently destroying sibling metadata for any field other than archived_at — the exact partial-update wipe class this repo has hit before. The dashboard happens to be safe because it re-sends the full object (dashboard/src/components/ApplicationRepositoryPage.tsx:610-618).

**Failure scenario:** An API client renames a pool with PUT /data-pools/{id} {"name":"NewName","updated_at":"<token>"} (omitting description/schema, as the archived_at contract suggests is safe). The update succeeds and the pool's description and column-schema metadata are silently erased; the CAS token does not protect against it because the request is not stale.

**Suggested fix:** Make description/schema absent-preserving with the same RawMessage (or pointer) technique used for archived_at, or document the endpoint as strict full-replace and drop the archived_at absence special case.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V043 — CONFIRMED (low)

Chain, all at HEAD dc05008ab:

1. Code exists: pkg/api/handlers_data_pools.go:248-264 — request struct uses plain `Description string` / `Schema map[string]string` and unconditionally assigns `existing.Description = req.Description; existing.Schema = req.Schema` (262-264), while `ArchivedAt` is `json.RawMessage` with an explicit absent-preserving branch (253-256, 265-276).
2. Reachable: pkg/api/server.go:788 registers `PUT /api/v1/data-pools/{id}` → handleUpdateDataPool in production wiring.
3. Realistic trigger: the published OpenAPI contract requires only `name`+`updated_at` (pkg/api/openapi_contract_infer.go:901) and documents archived_at as "Omit to preserve the current archive state" (openapi_contract_infer.go:246-255), so a contract-conformant client may send only {name, updated_at}.
4. Consequence: pkg/storage/sqlite_datapools.go:136-137 writes name, description, schema_json, archived_at unconditionally → description persisted as "" and schema_json as "null". Fresh `updated_at` passes the CAS check (sqlite_datapools.go:127-129), so nothing blocks the wipe.
5. Blast radius limited: dashboard/src/components/ApplicationRepositoryPage.tsx:610-618 re-fetches and re-sends the full object, so only external API clients are exposed — severity low is right. Test handlers_data_pools_review_fixes_test.go:968-978 pins absent-archived_at preservation (reinforcing PATCH expectations); no test pins full-replace for description/schema.

Prior items C3 (row list limit) / C7 (CAS token) are different issues — not a regression; this is the residual inconsistency left by the archived_at fix.

</details>


#### 103. Manifest configuration maps StartedAt from the stored record's ScheduledStartAt
**Where:** `pkg/api/test_manifest.go:375` · **Area:** AREA-07 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

manifestConfigurationFromTestRecord sets StartedAt: cloneTimePtr(record.ScheduledStartAt) (test_manifest.go:375). storage.TestRecord has no actual started-at field (StartTime holds CreatedAt per managedTestFromRecord at pkg/api/persistence.go:722 and buildManagedTestRecord at persistence.go:1396), so the scheduled launch time is presented as the start time.

**Failure scenario:** Create a run scheduled for 09:00 tomorrow. GET the test manifest today: initial_configuration.started_at reports tomorrow 09:00 although the run has never started; conversely an immediately-started run reports started_at null in the record-sourced configuration even while running. Manifest consumers comparing started_at across sections get inconsistent values.

**Suggested fix:** Emit ScheduledStartAt into its own field (or omit StartedAt from the record-sourced projection); if a durable started-at is wanted, add it to TestRecord.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V042 — CONFIRMED (low)

Chain verified. manifestConfigurationFromTestRecord sets StartedAt: cloneTimePtr(record.ScheduledStartAt) (pkg/api/test_manifest.go:375). storage.TestRecord has no started-at field (pkg/storage/storage.go:1174-1219: only StartTime, ScheduledStartAt, PendingSince, EndTime), and StartTime actually holds CreatedAt in both directions (buildManagedTestRecord, pkg/api/persistence.go:1396: StartTime: test.CreatedAt; managedTestFromRecord, persistence.go:~722: CreatedAt: record.StartTime). The parallel projection from the in-memory test uses the real test.StartedAt (test_manifest.go:352), so the two configuration sections of the same manifest disagree: a scheduled-not-yet-started run reports its future scheduled time as started_at in the record-sourced section, and an immediately started run (nil ScheduledStartAt) reports started_at null while running. Low severity is right — display/consistency wrongness in one manifest field, no state corruption. PRIOR-REPORT CHECK: M6/TierC-5 are pool-provenance gaps in the same file, unrelated and still open — not a regression.

</details>


#### 104. Default in-memory cache backend never evicts expired entries
**Where:** `pkg/api/cache_backend.go:345` · **Area:** AREA-08 · **Category:** unbounded-growth · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

memoryCacheBackend has no sweep goroutine: expired entries in m.entries are physically removed only when the SAME key is read via getJSON (cache_backend.go:483-487) or re-reserved, and m.rateLimits buckets are never deleted at all (cache_backend.go:390-395 only overwrites the same key) — so idempotency reservations (one per unique actor|method|path|Idempotency-Key, middleware.go:330) and atomic values accumulate forever on the default 'memory' backend.

**Failure scenario:** A long-running single-node controller serves automation whose HTTP client attaches a fresh UUID Idempotency-Key to every mutation (a common client-library default). Each mutation permanently adds one ~150-byte map entry; at 5 mutations/sec that is ~65 MB/day of unreclaimable map growth plus GC pressure, degrading a controller that runs for weeks. Per-IP OIDC login rate buckets from internet-facing scans accumulate the same way.

**Suggested fix:** Add a lazy sweep: on every Nth write (or a time-gated check inside the existing mutex), iterate and delete entries/buckets whose expiry/resetAt has passed; the maps are already fully guarded by m.mu so a periodic inline sweep is trivial.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V050 — CONFIRMED (low)

newMemoryCacheBackend (pkg/api/cache_backend.go:284-295) only allocates maps; no sweep/janitor goroutine exists anywhere in the file (grep for sweep/janitor/evict: none). ReserveIdempotencyKey (345-358) overwrites an expired entry only for the SAME key; a unique key never seen again stays in m.entries forever. getJSON (483-487) deletes expired entries only on a read of that same key. AllowRateLimit (390-395) resets a bucket in place but never deletes any m.rateLimits key, so per-IP buckets (auth_oidc.go:616, ci_auth.go:359) accumulate unboundedly. Memory is the default backend (cache_backend.go:152-156, 168-171), and the idempotency middleware creates one entry per unique actor|method|path|key scope (middleware.go:331). Unbounded growth claim holds; low severity appropriate.

</details>


#### 105. Remote probe persists remote-supplied Capabilities into the shared settings row without it
**Where:** `pkg/api/library_handlers.go:460` · **Area:** AREA-09 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleLibraryProbeRemote stores the remote's LibraryCapabilitiesResponse verbatim on the remote record (pkg/api/library_handlers.go:460) and persistLibraryRemotes serializes all remotes — including Capabilities — into the single library.remotes.v1 setting (pkg/api/library_federation.go:330-371). The only bound on the capabilities payload is decodeLibraryRemoteJSON's shared 128MB cap (pkg/api/library_federation.go:33, pkg/api/library_remote.go:168-180), which is sized for bundle exports, not a capabilities probe.

**Failure scenario:** A misbehaving or malicious federated remote returns a capabilities document with a 50MB ItemKinds/Operations array. Probe succeeds, the blob is written into library.remotes.v1, and from then on every loadLibraryState/loadLibraryLinksState caller (every remote list, import, link mutation) unmarshals tens of MB from the settings table, degrading all federation endpoints; with a CAS-less store the oversized row is rewritten on every remote mutation.

**Suggested fix:** Decode probe responses with a small dedicated limit (e.g. 64KB) and/or validate ItemKinds/Operations lengths before storing; store only the recognized fields.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V057 — CONFIRMED (low)

Chain verified: handleLibraryProbeRemote stores the remote-supplied capabilities verbatim (`pkg/api/library_handlers.go:460` `stored.Capabilities = caps`) then persists (464). remoteLibraryCapabilities (`pkg/api/library_remote.go:21-27`) decodes via remoteJSON → decodeLibraryRemoteJSON, whose only bound is the shared 128MB cap (`pkg/api/library_federation.go:33`; `library_remote.go:169-174`) sized for bundle exports. LibraryRemote.Capabilities is a serialized field (`library_federation.go:174` json:"capabilities,omitempty"), and persistLibraryRemotes marshals every remote incl. Capabilities into the single library.remotes.v1 setting (330-371; APIKey is cleared, Capabilities is not). loadLibraryState re-unmarshals the whole row on every call with no raw-unchanged short-circuit (230-268) and handlers call it per request (e.g. 431/442). Minor overstatement: a CAS path exists (356-363). Low severity appropriate.

</details>


#### 106. PowerBI row-push retry can duplicate rows under the SAME publish_id, defeating the documen
**Where:** `pkg/api/powerbi_client.go:116` · **Area:** AREA-09 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

doJSON (pkg/api/powerbi_client.go:116-171) retries up to 3 times on transport errors (lines 134-141) and on 408/429/5xx/401 (line 162). pushRows (lines 276-293) sends the rows POST through this retry loop with no idempotency mechanism (the Power BI push API has none), so a request that Power BI applied but whose response was lost (client timeout, connection reset after write) is re-sent and the same rows land twice with the SAME publish_id. Every documented duplicate-mitigation in this feature — powerBIPublishConflict's messages (pkg/api/powerbi_publish.go:139-160) and the force-republish flow — assumes duplicates are distinguishable by publish_id ("reports must keep only the latest publish_id per run"); intra-publish retry duplicates are invisible to that filter.

**Failure scenario:** A publish of a run with 300 transaction rows POSTs the single chunk; Power BI ingests it but the response exceeds the 30s client timeout (or a 500 is returned after the rows were committed). doJSON retries and the second POST succeeds. The dataset now holds 600 transaction rows carrying one publish_id; the local publish record says Complete with 300 rows. Executive reports that filter to the latest publish_id per run double-count every transaction/error/TPS aggregate for that run, silently, and neither force-republish detection nor the conflict messages can identify it.

**Suggested fix:** For the rows POST specifically, do not retry on transport errors/5xx after the request body may have been consumed (fail the publish as partial so the operator uses the documented force-republish path with a fresh publish_id); alternatively add a per-chunk sequence column (publish_id + chunk_index) so duplicated chunks are detectable in reports, and document the filter.

<details><summary>Verification evidence</summary>

**downgraded @ low:** # V056 — DOWNGRADED to low

Technical chain is real: `pkg/api/powerbi_client.go` doJSON retries up to 3x on transport errors (134-141) and on retryable statuses incl. 5xx/401 (162-168); pushRows (276-293) sends the rows POST through it with no idempotency token, so a chunk applied server-side but whose response was lost can land twice under the same publish_id. BUT the finding's framing — "defeating the documented dedupe", invisible/undocumented — is contradicted by `docs/POWERBI_INTEGRATION.md:82`, which explicitly documents this exact case: "Row delivery is at-least-once: a retried chunk whose first attempt actually landed ... can duplicate rows *within* one publish_id", calls it inherent/rare/bounded to one chunk, and names the recreate procedure as remediation. A documented, deliberate tradeoff with a recovery path, not a silent medium data-integrity defect → low.

</details>


#### 107. Marker handler swallows JSON body decode errors, producing misleading validation errors
**Where:** `pkg/api/handlers_browser_recorder.go:537` · **Area:** AREA-10 · **Category:** error-handling · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

handleBrowserRecordingMarker uses a bare `json.NewDecoder(r.Body).Decode(&req)` with the error discarded (pkg/api/handlers_browser_recorder.go:537), unlike every sibling handler which uses decodeJSONBody/decodeOptionalJSONBody. A syntactically invalid body proceeds with zero-valued fields, so the caller gets a downstream domain error instead of a 400 about malformed JSON, and any oversized/streaming protections in decodeJSONBody are bypassed for this one route.

**Failure scenario:** POST /api/v1/browser-recorder/sessions/{id}/transactions/start with body `{"name": "Checkout"` (missing brace): instead of a JSON-syntax 400, the API returns 400 "transaction name is required", sending the client author debugging the wrong thing; the same body on /notes returns "note text is required".

**Suggested fix:** Replace the bare Decode with `if !decodeOptionalJSONBody(w, r, &req) { return }` to match the other browser-recorder handlers.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V063 — CONFIRMED (low)

Chain (all cites snapshot @ dc05008ab, repo-relative):

1. `handleBrowserRecordingMarker` declares `req` then calls `json.NewDecoder(r.Body).Decode(&req)` with the error discarded — pkg/api/handlers_browser_recorder.go:531-538 (finding cited :537; bare Decode is :538).
2. Production wiring: POST routes `/api/v1/browser-recorder/sessions/{id}/transactions/start|end` and `/notes` → these handlers at pkg/api/server.go:986-989 (bridge variant pkg/api/browser_recording_bridge.go:190-193).
3. Malformed body leaves `req` zero-valued; `AddMarker` then returns `"transaction name is required"` (pkg/recorder/browser/events.go:630-632) or `"note text is required"` (events.go:656-658), which the handler maps to 400 (handlers_browser_recorder.go:545-551) — a misleading domain error instead of a JSON-syntax 400.
4. Contrast: siblings use `decodeJSONBody`/`decodeOptionalJSONBody` (handlers_browser_recorder.go:270,399,439,662,1014,1069,...), whose failure path is 400 "invalid request body: ..." (pkg/api/handlers.go:1580-1586).

Extra: on `transactions/end` with an open transaction, malformed JSON yields zero Name → the top open transaction is silently ended with 200 (events.go:639-644). Minor overstatement: `decodeJSONBody` itself has no size cap, so "oversized protections bypassed" is not established — immaterial.

Prior-report W2 ("storage-state copy", fixed) is a different symbol/defect — not a regression.
Commands: grep for handlers/helpers + ranged sed reads; no test needed (static chain complete).

</details>


#### 108. browser_ws_wait match hints are built from scrubbed WS payloads and can never match at rep
**Where:** `pkg/recorder/browser/convert.go:749` · **Area:** AREA-10 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

recordWSFrame scrubs captured WebSocket payloads before storage (pkg/recorder/browser/recorder.go:1794-1803: `text = r.opts.Scrubber(text)`, wired to scrub.Value by the manager at pkg/api/browser_recording_manager.go:299), and websocketWaitAction then uses that stored payload as the browser_ws_wait `match` value with match_mode contains (pkg/recorder/browser/convert.go:746-755). When the scrubber rewrote secret-looking content (tokens, session ids) the emitted match string contains the redaction marker, which the live server will never send, so the generated wait always exhausts its 10s timeout.

**Failure scenario:** Record with capture_ws_payloads=true against an app whose server frames carry `{"access_token":"eyJ..."}`; scrub rewrites the token in the stored frame; conversion emits browser_ws_wait with match containing the redacted form; every replay of the converted script stalls 10s on that action and the wait times out even though the application behaves identically to the recording.

**Suggested fix:** Detect the scrub redaction marker in the stored payload and either skip the match param (fall back to count/direction wait) or derive the hint from a stable structural fragment (e.g. the first JSON key names) instead of the value bytes.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V064 — CONFIRMED (low)

Chain verified. pkg/recorder/browser/recorder.go:1795-1798 scrubs the frame text before storage (`text = r.opts.Scrubber(text)`); the manager wires that to `scrub.Value` (pkg/api/browser_recording_manager.go:299), which replaces secrets with `[REDACTED]` (pkg/credential/credential.go:27) while keeping JSON valid. Conversion builds timeline entries from stored received-frame payloads (pkg/recorder/browser/convert.go:574-588), and websocketWaitAction (:746-751) emits `match` = truncateString(webSocketPayloadMatchHint(payload),160) with match_mode "contains" and timeout_ms 10000. A live server never sends the literal `[REDACTED]` marker, so a hint whose first 160 chars include a redacted value cannot match and the wait burns its 10s timeout. Minor scope caveat: only frames whose scrubbed region falls inside the 160-char hint are affected — consistent with severity low.

</details>


#### 109. Translated outer loop containing an untranslated nested loop splits into two loop blocks, 
**Where:** `pkg/convert/enterprisearchive/control_flow_import.go:375` · **Area:** AREA-11 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

structureFlowActions groups consecutive actions by exact loopID (control_flow_import.go:375), and tagSourceFlowMetadata only translates single-loop contexts (len(fn.Loops)==1, control_flow_import.go:250). Actions inside a nested loop carry loopID "outer>>inner" while outer-only siblings carry "outer", so one source loop body [A, inner-loop, B] imports as control_loop{A} xN, inner actions once (warned), control_loop{B} xN — two separate wrappers instead of one.

**Failure scenario:** C source: for (i=0;i<3;i++){ web_url("A"); for(j=0;j<atoi(lr_eval_string("{n}"));j++){ web_url("I"); } web_url("B"); }. Import executes A,A,A,I,B,B,B instead of A,I*,B repeated 3 times. Request counts for A/B are preserved and the inner loop's single-pass warning fires, but no warning explains that the outer loop was split into two blocks, so session-order-dependent servers (or correlation between A and B within one pass) behave differently from the source while the operator believes only the inner loop diverged.

**Suggested fix:** When a translated loop run is interrupted by actions whose loopID chain has the same first frame (prefix match on the outer ID), either extend the wrapper to include them as once-per-pass steps with a review tag, or emit an explicit warning that the outer loop was split into multiple blocks.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V069 — CONFIRMED (low)

tagSourceFlowMetadata stamps loopID as the full frame join (control_flow_import.go:249) and only sets loopExpr/loopCount when len(fn.Loops)==1 (line 250). So in [A, inner-loop I, B], A/B carry loopID "outer" with a count, I carries "outer>>inner" with neither. structureFlowActions groups by exact loopID equality (line 375): the run for A terminates at I, producing control_loop{A} xN; I is emitted unwrapped (runs once); B starts a NEW group at line 375 → second wrapper control_loop{B} xN. Execution becomes A,A,A,I,B,B,B instead of (A,I,B)x3. The only warning emitted is the line 271-273 "enclosed calls run once" notice for the nested display; nothing mentions the outer loop being split into two blocks. Chain holds; ordering divergence with partial warning → low.

</details>


#### 110. sourceControlArgs breaks positional integrity on empty arguments (C drops trailing args, J
**Where:** `pkg/convert/enterprisearchive/source_runtime.go:567` · **Area:** AREA-11 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

parseSourceRuntimeControl (parser.go:471-476) skips empty cleaned arguments but keeps original indices, leaving gaps in fn.Args (arg1, arg3 with no arg2); sourceControlArgs (source_runtime.go:569-577) iterates arg1..argN and BREAKS at the first missing key, silently dropping every argument after an empty one. The Java path (javaLRParsedFunction, java_import.go:5786-5790) stores empty strings instead, and sourceControlArgs skips them, SHIFTING later arguments into earlier positions.

**Failure scenario:** C action file contains web_set_user("bob", "", "auth.example.com:443"); — Args = {arg1:"bob", arg3:"auth.example.com:443"}. sourceControlArgs returns ["bob"], so applySourceWebSetUser (source_runtime.go:40-71) sets auth username only and the host mapping plus its preserved source_authentication evidence lose the host silently (no warning, no review flag). In a Java Vuser, lr-routed equivalents with an empty literal middle argument instead shift: position-based consumers read the third argument as the second (e.g. host consumed as password).

**Suggested fix:** Preserve positional slots: have parseSourceRuntimeControl store empty args (or have sourceControlArgs continue past present-but-empty argN keys and stop only on a truly absent key), and keep empty strings in the returned slice so consumers index positionally.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V068 — CONFIRMED (low)

C path: web_set_user routes to parseSourceRuntimeControl (parser.go:440,451). parser.go:471-476 skips empty cleaned args (`""` literal cleans to "" via cleanConfigValue, parser.go:616-622/1286-1298) but keeps 1-based indices, leaving Args={arg1,arg3}. sourceControlArgs (source_runtime.go:569-577) breaks at the first absent argN key, so `web_set_user("bob","","h:443")` returns ["bob"]; the CallArgs/RawArgs fallbacks (578-594) are skipped because args is non-empty. applySourceWebSetUser (source_runtime.go:40-71) then never sets host — silent loss, no warning. Java path: javaLRParsedFunction (java_import.go:5786-5790) stores empty strings for every position, and source_runtime.go:574-576 skips present-but-empty values, shifting later args left for positional consumers. Both halves hold; edge-case input, so low.

</details>


#### 111. Any parsed call name starting with "sql" classifies content as database ahead of web reque
**Where:** `pkg/convert/enterprisearchive/protocol_detection.go:321` · **Area:** AREA-12 · **Category:** protocol-detection · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

profileFromContent checks containsFunctionPrefix(counts, "sql") before counting web request functions (protocol_detection.go:321-329), so one custom C helper call named e.g. sqlEscape(...) or sqlite_log(...) in an otherwise pure web script makes the content profile 'database'. With a Web .usr this creates a metadata/content conflict that demotes an entirely convertible script to review-only (DetectProtocol:201-206); without a .usr the script is routed to ConvertDatabase.

**Failure scenario:** A Web HTTP/HTML archive whose Action.c calls a local helper sqlEscape(buf) once, plus 50 web_url calls, imports as review-only ('Metadata and content disagree') instead of executable — the headline import path is gated on a false conflict; the operator must manually confirm the protocol.

**Suggested fix:** Drop the bare 'sql' prefix (keep lrd_/lr_db), or check request evidence first and require multiple database-family signals before overriding it.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V081 — CONFIRMED (low)

protocol_detection.go:321-324: `containsFunctionPrefix(counts, "sql")` (prefix match over lowercased parsed call names, 608-615) returns databaseProfile before web request evidence is consulted at 329 (`requestCount`). Custom helper names do reach counts: flattenParsedFunctions (587-596) copies all ParsedFunctions unfiltered; the AST parser's expression-statement path (ast_parser.go:152-188) parses any statement-level call via reFuncCall (`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`, parser.go:127) with unknown names kept by the default case (parser.go:456-459) — so `sqlEscape(buf);` or `sqlite3_exec(...)` yields a "sql"-prefixed name. With Web .usr metadata, compatibleProfiles(web_http_html/http vs database/database) is false (541-561) → conflict (176) → review-only demotion (201-206). Confirmed at low.

</details>


#### 112. Explicit https:// proxy URLs on ports 3128/8080/8888 silently rewritten to http://
**Where:** `pkg/convert/enterprisearchive/runtime_config.go:4046` · **Area:** AREA-12 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

normalizeRuntimeProxyURL rewrites any https-scheme proxy URL whose port is 3128, 8080, or 8888 to http:// (runtime_config.go:4052-4059). That is a reasonable guess for endpoints synthesized from ProxyHTTPSHost/Port (where 'HTTPS' means proxied traffic, not TLS-to-proxy), but it also applies to an operator-authored ProxyServer=https://proxy.corp:8080 value, silently changing the transport to cleartext with no runtime issue or review note.

**Failure scenario:** A cfg declares ProxyServer=https://tls-proxy.corp:8080 where the proxy genuinely speaks TLS. The imported config silently becomes http://tls-proxy.corp:8080; replay attempts cleartext against a TLS listener and every request fails through the proxy, with the import claiming the proxy mapped cleanly.

**Suggested fix:** Restrict the rewrite to endpoints built from host/port fields (buildRuntimeProxyEndpoint) and leave explicit ProxyServer schemes untouched, or emit a proxy RuntimeIssue noting the scheme normalization.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V080 — CONFIRMED (low)

runtime_config.go:3674-3675 reads the operator-authored `ProxyServer`/`ProxyURL`/`Server` value directly into rc.ProxyURL; only when empty does it fall back to host/port synthesis (3677-3679). Line 3680 then unconditionally applies normalizeRuntimeProxyURL to whichever value won. normalizeRuntimeProxyURL (4046-4062) parses the URL and, when the scheme is https and Port() is "3128", "8080", or "8888" (4052-4057), rewrites the scheme to http and returns — the function returns only a string, emits no RuntimeMigrationIssue, and no caller adds one for this rewrite. So an explicit `ProxyServer=https://tls-proxy.corp:8080` is silently converted to cleartext, exactly as claimed. Severity low is fair: TLS-to-proxy is uncommon, but the failure (replay through proxy breaks) is silent at import.

</details>


#### 113. removeConsumedLiteralScriptParameters drops parameters still referenced by non-credential 
**Where:** `pkg/convert/requestimport/script_parameter_credentials.go:150` · **Area:** AREA-13 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

ReconcileProtectedScriptCredentialSources (script_parameter_credentials.go:106-155) calls removeConsumedLiteralScriptParameters (invoked at line 152, removal loop at lines 150-162), which deletes every script parameter whose name appears in a requirement's Value/SourceVariableNames without checking whether other, non-credential surfaces of the script still reference {{param.NAME}}; MaterializeSourceVariables only substitutes the parameter inside credential-bearing scalars, so any remaining reference in a non-sensitive field is left dangling after the parameter is removed.

**Failure scenario:** An imported script defines parameter password=['hunter2'] (single-value list) consumed into a protected requirement via auth.password governance; the same parameter is also referenced by a non-sensitive body field, e.g. "note": "prefix-{{param.password}}-suffix" (key 'note' is not credential vocabulary, and the value has no name=value shape so composedCredentialName does not govern it). After ReconcileProtectedScriptCredentialSources the parameter is deleted from script.Parameters, so replay interpolates the dangling {{param.password}} in the body as a literal/empty string instead of 'hunter2' — the request body silently changes on the wire with no finding or gate recording the alteration.

**Suggested fix:** Before deleting a consumed parameter, scan the projected script (marshal + collectParameterReferences) for remaining {{param.NAME}} references; if any remain outside governed surfaces, keep the parameter but redact its Values to credential.Redacted (matching redactCredentialSourceVariables) and emit a review finding instead of removing it.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V084 — CONFIRMED (low)

Code-level claim verified. pkg/convert/requestimport/script_credential_projection.go:118-134 builds `consumed` from requirement Value refs + SourceVariableNames; the kept-filter at 150-162 deletes every parameter whose name is consumed, with no scan of the projected script for residual {{param.NAME}} references on non-credential surfaces. credentialpolicy/projection.go:607-616 documents MaterializeSourceVariables applies only inside credential-bearing scalars, and body governance is name-vocabulary/composed name=value driven (projection.go:1308-1311, 1349-1376), so a non-sensitive field like note:"prefix-{{param.password}}-suffix" is neither governed nor materialized — its reference dangles after deletion. Reachability caveat: ReconcileProtectedScriptCredentialSources (script_parameter_credentials.go:106-155, sole call site of the removal at :152) has NO production callers in the snapshot — only script_credential_projection_test.go:1082 — so the wire-level failure is latent in an exported API, supporting low severity.

</details>


#### 114. orderReplayOps comparator is not transitive, so produce-before-consume ordering can silent
**Where:** `pkg/convert/asyncapi/converter.go:657` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

orderReplayOps (asyncapi/converter.go:637-663) sorts with a comparator that returns kindRank ordering only when a.topic == b.topic and false for any cross-topic pair. That relation is a partial order, and sort.SliceStable does not totalize it: when a same-topic produce and consume are separated by an op on another topic, the pair is never compared and the consume stays first. Verified empirically: input [consume(X), produce(Y), produce(X)] sorts to [consume(X), produce(Y), produce(X)] — the intended produce-before-consume invariant for topic X does not hold.

**Failure scenario:** AsyncAPI 2.x doc with channels sorted as: 'alpha' (subscribe, kafka binding topic X), 'beta' (publish topic Y), 'gamma' (publish, kafka binding topic X). The generated script runs kafka_consume on X before the kafka_produce that would create the message; on first iteration against the mock broker (or an empty real topic, even with the auto-set auto_offset_reset=earliest) the consume finds nothing and its message_received assertion fails, so the freshly imported script fails its first run for ordering reasons the importer claims to have fixed.

**Suggested fix:** Make the sort key total: group ops by topic first (e.g. sort by (targetProtocol, first-appearance index of topic, kindRank)) or do an explicit stable partition per topic instead of relying on a partial-order comparator.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V091 — CONFIRMED (low)

pkg/convert/asyncapi/converter.go:649-661: comparator orders by targetProtocol, then kindRank only when `a.topic == b.topic`, else returns false — a partial order, not a strict weak ordering. Static trace of Go's stable sort (insertion sort for small n): input [consume(X), produce(Y), produce(X)] → less(produceY,consumeX)=false, less(produceX,produceY)=false → no swaps, so consume(X) stays before produce(X), violating the produce-before-consume intent stated in the comment at 654-656 and enforced by kindRank (637-648). Reachable: called at converter.go:320 in the main conversion flow. Prior-report cross-ref (collectOperations, REFACTORING_OPPORTUNITIES_2026-06-27, status open) is an unrelated duplication note, not a fixed item — no regression. Severity low is appropriate (first-run assertion failure, easily diagnosed).

</details>


#### 115. allOf flattening drops member required lists (and scalar facets), silencing response body 
**Where:** `pkg/convert/openapi/converter.go:1163` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

resolveSchema's allOf merge (openapi/converter.go:1160-1177) copies only member Properties and (first non-empty) Type into the merged schema; member Required lists, Enum, Format, Example and Items are discarded. responseBodyAssertions (converter.go:1655-1680) reads resolved.Required to derive body_contains assertions, so any success-response schema composed via allOf — the standard base-plus-extension modeling pattern — produces zero body assertions even when members declare required properties. Sample bodies are unaffected (properties are merged), so this matches the 'allOf flattening dropping fields' defect class only for required/facets.

**Failure scenario:** Spec whose 200 response schema is allOf: [ $ref BaseEntity (required: [id]), {properties: {...}} ]. The importer advertises assertion generation, but the converted action gets only the status_code assertion — the documented body_contains assertions for required properties silently never materialize, weakening every imported script's validation without any diagnostic saying so.

**Suggested fix:** Union member Required (deduplicated) into merged.Required during the allOf merge; optionally also inherit Example/Enum/Format from the first member that defines them.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V092 — CONFIRMED (low)

pkg/convert/openapi/converter.go:1160-1177: allOf branch builds `merged := s`, clears AllOf, and copies from each resolved member only `Properties` (1169-1171) and first non-empty `Type` (1172-1174). Member `Required` (plus Enum/Format/Example/Items) is dropped. Consumer chain: replay_helpers.go:62 resolves the success-response schema via resolveSchema; converter.go:1349 calls responseBodyAssertions(successSchema); converter.go:1660-1663 returns nil when `resolved.Required` is empty. So allOf: [$ref Base(required:[id]), {properties:...}] yields merged.Required empty (only top-level s.Required survives) → zero body_contains assertions, silently, while properties/samples merge fine. Causal chain fully holds; low severity correct (weakened validation, no wrong request).

</details>


#### 116. Structured (non-string) authored example under an XML content type renders Go map syntax a
**Where:** `pkg/convert/openapi/converter.go:2128` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

renderGovernedExample (openapi/converter.go:2090-2136) handles XML content types only when the authored example is already a string (2120-2127). A structured example (YAML mapping / JSON object under application/xml) falls through to renderExample (2080-2088), which for non-JSON content types returns fmt.Sprint(value) — producing a Go-syntax body like 'map[user:map[name:bob]]' with no review code from this function. Downstream the shared XML projector receives non-XML bytes; depending on its malformed-XML disposition the action is either shipped with the garbage body or blocked — either way the authored example is destroyed rather than rendered or honestly gated at the source.

**Failure scenario:** YAML OpenAPI doc with requestBody content application/xml whose example is authored as a YAML mapping (a legal and common spelling). The converted action body is the Go fmt rendering 'map[...]' instead of an XML document; replay sends unparseable non-XML bytes (or the action is blocked by the shared projector with a confusing malformed-XML code), and the operator has to reconstruct the body by hand although the spec contained a complete example.

**Suggested fix:** For XML content types with structured examples, either render via the existing xmlForSchema-style writer (map keys to elements) or return a dedicated review code (e.g. unsupported_xml_example_shape) and an empty body instead of fmt.Sprint output.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V093 — CONFIRMED (low)

pkg/convert/openapi/converter.go:2120-2127: the XML branch of renderGovernedExample returns early only for `value.(string)`; a structured example falls through to renderExample (2128 → 2080-2088), whose non-JSON path is `fmt.Sprint(value)`. The doc is parsed with gopkg.in/yaml.v3 (converter.go:25, 691), so a YAML mapping example decodes into map[string]any → fmt.Sprint yields Go `map[...]` syntax as the body, with no review code from this function (2135 returns nil codes). Reachable via requestBodyExample at 1762/1775/1784 whenever preferredContentType is application/xml and the authored example is a mapping. Core defect (Go map rendering destroys the authored example) established; downstream XML-projector disposition was hedged by the finding itself and is not load-bearing. Low severity appropriate.

</details>


#### 117. Source-review masking via bare ReplaceAll of example values can shred the review copy
**Where:** `pkg/convert/openapi/converter.go:485` · **Area:** AREA-14 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Convert masks sensitive source values in the review copy with strings.ReplaceAll(reviewContent, sensitiveValue, scrub.Redacted) for every value collected by sensitiveOpenAPISourceValues (openapi/converter.go:484-491). The collected values are examples/defaults/enums of credential-shaped fields with no minimum length or word-boundary check, so a short or common example value (e.g. a password example of "test" or "admin") replaces every occurrence of that substring across the entire document — operation IDs, paths, titles — corrupting the operator-facing source review. If the mangled text then fails the disclosure re-parse, disclosureSafeOpenAPISourceReview (673-740) falls back to omitting the review entirely. Executable output is unaffected; this is review-copy functional correctness (over-scrubbing), which is in scope.

**Failure scenario:** Spec with securitySchemes-adjacent schema {password: {type: string, example: "test"}} and operationIds like create_test_user. The source review shown for import approval displays create_[REDACTED]_user, /[REDACTED]s paths, etc.; in the worst case the YAML no longer parses and the whole source review is replaced by the omission banner, so reviewers cannot see what they are approving even though nothing sensitive required that.

**Suggested fix:** Apply a minimum-length floor (like scrub's own heuristics) and/or token-boundary-aware replacement for collected example values, and prefer the quoted-value redaction path (scrub.RedactQuotedValues) over bare substring ReplaceAll for short values.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V095 — CONFIRMED (low)

pkg/convert/openapi/converter.go:484-490: for each collected value, `reviewContent = strings.ReplaceAll(reviewContent, sensitiveValue, scrub.Redacted)` — bare substring replacement over the whole review copy, plus a JSON-escaped variant. Collection in `sensitiveOpenAPISourceValues` (749-928) admits any non-empty string (`add`, 757-762) from example/default/enum of credential-shaped fields (837-843) with no length floor or boundary check. So a password example "test" rewrites operationIds/paths/titles containing "test" to `[REDACTED]` (pkg/credential/credential.go:27). Worse, `disclosureSafeOpenAPISourceReview` (673-695), called at 637-644, replaces the entire review with the omission banner if the mangled YAML no longer parses (e.g. a scalar now starting with `[`). Executable output untouched; review-copy-only corruption. Severity low stands.

</details>


#### 118. Structured Postman URL objects drop the port (and hash) fields
**Where:** `pkg/convert/postman/converter.go:1156` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The postmanURL struct (postman/converter.go:231-238) models raw, protocol, host, path, query, and variable but omits the Postman URL object's 'port' and 'hash' members. rawURL (1148-1173) therefore reconstructs structured URLs as protocol://host/path?query with no port. When 'raw' is present (the common case) the port survives via raw, but collections generated programmatically (schema allows raw to be absent) lose it.

**Failure scenario:** A collection item whose request.url is the object {protocol: "http", host: ["localhost"], port: "8080", path: ["api","v1"]} with no raw string (produced by tooling that writes structured URLs). The converted target is http://localhost/api/v1 — the request replays against port 80 instead of 8080, so deriveBaseURL and every action silently hit the wrong endpoint.

**Suggested fix:** Add Port (string) and Hash to postmanURL and include ":"+port after the joined host (and "#"+hash after query) in rawURL's reconstruction.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V094 — CONFIRMED (low)

pkg/convert/postman/converter.go:231-238: postmanURL declares Raw, Protocol, Host, Path, Query, Variable — no Port or Hash field, so those JSON members are silently discarded on unmarshal. rawURL (1148-1173): when Raw is non-empty it is used (1149-1155), so the common case survives; otherwise reconstruction is protocol:// + join(Host,".") + "/" + join(Path,"/") + "?" + query (1156-1172) with no port or fragment segment. A structured URL object {protocol, host, port:"8080", path} without raw therefore converts to a target on the default port. Failure scenario holds exactly as claimed; only affects raw-less structured URLs (schema-legal, tool-generated), so low severity is appropriate.

</details>


#### 119. Test-script pm.*.set literals apply retroactively to earlier requests
**Where:** `pkg/convert/postman/converter.go:821` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

applyPostmanEventVariables (postman/converter.go:808-827) scans every request's event scripts for pm.environment/collectionVariables.set("k","literal") and writes the value into the shared vars map before any request is converted. The value therefore participates in variable resolution for requests that execute before the script that sets it, which inverts Postman's runtime scoping (a set takes effect only after its owning request runs). Response-derived sets are correctly modeled as extractors with ordering awareness (request_pipeline.go:150-159 registers proven variables only after the producing action), but literal sets have no such ordering.

**Failure scenario:** Collection variable env="prod"; request 3's test script does pm.environment.set("env", "staging"). Postman would run requests 1-2 against prod and 4+ against staging; the converted script resolves {{env}} to "staging" everywhere, so requests 1-2 replay against the wrong host/path with only an Info diagnostic ("translated deterministic pm variable assignment") hinting at it.

**Suggested fix:** Apply literal sets positionally: seed vars with collection/environment values, then fold each request's sets into the lookup only for subsequent requests (mirror the provenVariables ordering used for extractors), or at minimum escalate the diagnostic to a warning naming the affected earlier requests.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V096 — CONFIRMED (low)

pkg/convert/postman/converter.go:319 calls `applyPostmanEventVariables(sourceVars, requests, ...)` before any request is converted; 808-827 scans every request's event scripts (prerequest and test alike) for `pm.environment/collectionVariables.set("k","literal")` and unconditionally overwrites `vars[key] = match[2]` (821), clobbering collection/environment seeds (314-318). The resulting single lookup (320-321) is passed index-independently to `buildAction` for every request (419) and seeds script parameters (371), so request 3's literal set resolves `{{env}}` for requests 1-2 too — inverting Postman's after-the-owning-request scoping. Contrast: response-derived captures are registered into provenVariables only after the producing action (request_pipeline.go:150-158). Only an Info diagnostic (822) flags it. Severity low stands.

</details>


#### 120. DevWeb transaction attribution only tracks the last-declared transaction variable
**Where:** `pkg/convert/devweb/diagnostics.go:257` · **Area:** AREA-15 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

activeDevWebTransaction takes the LAST `new load.Transaction(...)` match in the prefix as the only candidate (diagnostics.go:263-269), then checks start/stop only for that variable. Scripts that declare several transactions up front (`const t1 = new load.Transaction('Login'); const t2 = new load.Transaction('Search');`) and then run t1.start()...t1.stop() lose attribution entirely: activeVar is t2, whose .start() is never found, so requests inside the Login transaction get no source_transaction metadata. The substring search (`activeVar+".start("`) can also false-match longer variable names ending in the same identifier.

**Failure scenario:** A DevWeb script declares Login and Search transactions at the top of the action (common style), starts/stops each around its requests. Imported actions carry no transaction metadata for Login (and Search only if it happens to be last-declared), so per-transaction reporting groups collapse and results dashboards lose the transaction breakdown the source defined.

**Suggested fix:** Track all declared transaction vars in a map and pick the one with the latest .start() (word-boundary matched) not followed by its .stop() before the request offset.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # P12 — CONFIRMED (low)

pkg/convert/devweb/diagnostics.go:263-269: the loop over all
`new load.Transaction` matches overwrites activeVar/activeName each iteration,
keeping only the LAST declaration in the prefix; lines 273-278 then LastIndex-search
`.start(`/`.stop(` for that one variable only. With `const t1=...; const t2=...`
declared up front, a request inside t1's start/stop window sees activeVar=t2,
t2.start absent (start=-1), so "" is returned. converter.go:464-471 is the sole
place `source_transaction`/`devweb_transaction` metadata is set (the
transactionNewPattern loop at converter.go:527-533 only counts stats), so those
requests lose transaction attribution. Substring LastIndex also allows suffix
false-matches (e.g. var `t` matching `xt.start(`). Only bites the
declare-all-up-front style; per-request metadata loss only — low is right.

</details>


#### 121. Source mappings for samplers inside Loop/If/While/OnceOnly controllers discarded
**Where:** `pkg/convert/jmeter/converter.go:725` · **Area:** AREA-15 · **Category:** observability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

collectJMeterChildActions walks controller children into a temp script with a local tempMappings slice (converter.go:726-727) that is never merged back; only the controller itself gets one aggregate mapping. Nested samplers therefore have no per-sampler source-view mapping, and tallyJMeterCategories (converter.go:360) under-counts Mapped for plans that use controllers, skewing the four-way classification stats the UI reports.

**Failure scenario:** A plan whose 30 samplers all live in a LoopController imports with Stats.Mapped counting only the loop controller; the source-review pane shows the sampler XML as unmapped lines, so an operator auditing conversion fidelity cannot tell those samplers were converted.

**Suggested fix:** Rebase tempMappings target paths onto the parent control action path (actions[i].control.steps[j]) and append them to the shared sourceMappings.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # P11 — CONFIRMED (low)

pkg/convert/jmeter/converter.go:725-729: collectJMeterChildActions builds a local
`tempMappings []sourceview.Mapping`, passes `&tempMappings` to walkThreadGroup, then
returns only actions; mergeJMeterRuntimeParity (line 728) merges parity metadata,
never mappings. Call sites OnceOnly/Loop/If/While (lines 560, 580, 614, 640) each
`continue` after appending a single aggregate controller mapping (572, 594, 627, 660),
so the tail re-walk at 713-715 with the real slice is skipped — nested sampler
mappings are dropped. Line 402 builds result.SourceReview from top-level
sourceMappings only, and line 360 tallies Stats.Mapped from the same slice
(support.go:139-155), so nested samplers show unmapped and Mapped under-counts.
Conversion itself is unaffected — observability only; low severity stands.

</details>


#### 122. k6 import silently injects a fixed 1000ms think time absent from the source
**Where:** `pkg/convert/k6/converter.go:124` · **Area:** AREA-15 · **Category:** load-shape · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Convert hardcodes Config.ThinkTime {fixed, 1000ms} (converter.go:124-127). k6 has no implicit pacing — a script without sleep() iterates at maximum rate — and the engine applies the script-level think time globally per action (pkg/engine/replay.go:395-403 builds globalThinkCfg from Config.ThinkTime). The sleep-related warning (converter.go:252-254) fires only when the source contains sleep(), so a sleep-free k6 script (the exact case where the injected pause changes behavior most) imports executable with no disclosure that per-action 1s pauses were added.

**Failure scenario:** User imports `export default function () { http.get('https://api/x'); }` (a max-throughput probe; clean subset, no review gates). Against a 50ms endpoint, k6 drives ~20 req/s/VU; the imported script drives <1 req/s/VU because of the invented 1s pause. Capacity conclusions drawn from the imported test are ~20x off with no warning anywhere in the result.

**Suggested fix:** Default Config.ThinkTime to zero/none for k6 imports (map static sleep() literals instead — see companion opportunity), or at minimum emit an assumption/warning stating the injected default.

<details><summary>Verification evidence</summary>

**downgraded @ low:** # P10 — DOWNGRADED (medium -> low)

True facts: pkg/convert/k6/converter.go:124-127 unconditionally sets Config.ThinkTime {fixed, 1000ms}, and warnings (249-254) fire only when Checks/Sleeps > 0, so a sleep-free script gets no disclosure. BUT the impact chain breaks: both execution paths gate per-action think time on action.ThinkTimeAfter — pkg/engine/vuser.go:1414-1416 (`!action.ThinkTimeAfter` returns immediately, no sleep) and pkg/engine/replay.go:1305. The k6 pipeline never sets ThinkTimeAfter anywhere (no occurrence in pkg/convert/k6/ or its requestimport.go boundary; requestimport/project.go:1107 propagates it only if the source adapter sets it, unlike jmeter/converter_sampler.go:131). Repo-wide, no other non-test consumer of Config.ThinkTime applies pauses. So no 1s pause is injected and the claimed ~20x throughput distortion does not occur; residual is a dormant/misleading config value that could activate if ThinkTimeAfter is later enabled.

</details>


#### 123. TDigest.compress tracks cumWeight inconsistently, skewing the k1 size limit
**Where:** `pkg/engine/tdigest.go:247` · **Area:** AREA-16 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

In compress (tdigest.go:247-270) cumWeight starts as centroids[0].weight (INCLUDING the current last centroid), but after the first non-merge (`cumWeight += last.weight` then append) it EXCLUDES the new last centroid; the q used for maxWeight (`(cumWeight - last.weight/2 + proposedWeight/2)/totalWeight`, line 255) is therefore computed against two different conventions and is off by up to last.weight/2 relative to the true midpoint of the proposed merged centroid.

**Failure scenario:** During a run with heavy-tailed latencies, centroids near the affected quantile positions are allowed to grow slightly larger (or forced smaller) than the k1 scale function intends, degrading P95/P99 accuracy modestly versus a correct merging digest -- wrong by a small margin, not corrupt, but this digest feeds every published percentile including SLA verdicts.

**Suggested fix:** Maintain cumWeight strictly as the total weight of result centroids BEFORE last (start at 0), and compute q = (cumWeight + proposedWeight/2)/totalWeight; update cumWeight += last.weight only when starting a new centroid.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # E05 — CONFIRMED (low)

pkg/engine/tdigest.go:244-270. For q to be the midpoint of the proposed merged centroid, q=(B+proposedWeight/2)/total where B = weight strictly before `last`. The code's formula (line 255) `(cumWeight - last.weight/2 + proposedWeight/2)` is only correct if cumWeight = B + last.weight/2; but init sets cumWeight = centroids[0].weight (line 247, = B + full last.weight), the merge branch (260-264) grows last.weight without any cumWeight compensation, and the new-centroid branch adds full last.weight (267). The three sites imply different conventions; e.g. unit-weight trace at i=1 gives q=1.5/T vs true midpoint 1.0/T (off by last.weight/2), and the error drifts as merges enlarge last.weight. maxWeight(q) (277-279) is thus evaluated at a skewed quantile — modest percentile-accuracy skew, not corruption. Claim's exact "excludes new last" wording is imprecise for unequal weights, but the inconsistency and bound (≤ last.weight/2) hold. Low severity appropriate.

</details>


#### 124. b.pwContext read without lock races concurrent close paths
**Where:** `pkg/protocol/browser/runtime_context.go:396` · **Area:** AREA-17 · **Category:** concurrency · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

registerPage reads b.pwContext unguarded at runtime_context.go:396 (after releasing b.mu) and contextPages reads it unguarded (frame_actions.go:695-698), while close() nils it under b.mu via takeBrowserContext (lifecycle.go:82-88) and the HAR monitor goroutine nils it under b.mu in closeOversizedHARContext (har_capture.go:157-166). registerPage runs on Playwright's OnPage dispatch goroutine, so these are genuine data races visible to -race; installCDPSession's nil check limits the practical blast radius but a stale non-nil context can be used after Close.

**Failure scenario:** HAR size limit trips (monitor goroutine closes and nils pwContext) while a popup opens concurrently: OnPage -> registerPage reads the stale context and calls NewCDPSession on a closed context, surfacing a spurious error or (under -race in CI) a reported data race that fails the build.

**Suggested fix:** Add a locked accessor (currentBrowserContext()) mirroring currentPage() and use it in registerPage/contextPages.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # W06 — b.pwContext read without lock races concurrent close paths — CONFIRMED (low)

Every hop verified in snapshot (HEAD dc05008ab):

1. **Unguarded read**: `registerPage` releases `b.mu` at pkg/protocol/browser/runtime_context.go:390, then reads `b.pwContext` with no lock at :396 (`b.installCDPSession(b.pwContext, page)`). `contextPages` (pkg/protocol/browser/frame_actions.go:696-697) also reads it unguarded — it even takes `b.mu` for its fallback branch, but not the primary read.
2. **Concurrent locked writers**: `closeOversizedHARContext` nils `b.pwContext` under `b.mu` (har_capture.go:158-162) and runs on the independent HAR-monitor goroutine (har_capture.go:137,146, spawned whenever HAR capture is enabled); `takeBrowserContext` nils it under `b.mu` at close (lifecycle.go:82-88).
3. **Cross-goroutine reachability**: OnPage handler calls `registerPage` on Playwright's dispatch goroutine (runtime_context.go:161-165); action-goroutine `registerPage(...,true)` calls (frame_actions.go:648-687, runtime_context.go:91) race the monitor goroutine regardless of `autoActivatePopups`. Unsynchronized load vs mutex-guarded store = data race per Go memory model, -race detectable.
4. **Bounded blast radius**: stale context → `NewCDPSession` error is swallowed (runtime_context.go:621-623); nil check at :615 catches nil. So consequence is a -race failure / spurious error, matching claimed severity low.

Prior "popup auto-activation" fix (WEEK_REVIEW_2026-07-02) guarded the `pwCDP` **write** (comment runtime_context.go:625-627) and de-defaulted popup activation; the `pwContext` **read** was left unguarded — residual gap, not a regression.

</details>


#### 125. HTTP regex extractor without a capture group reports 'no match' though the pattern matched
**Where:** `pkg/protocol/http/extract.go:1064` · **Area:** AREA-17 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

extractRegex requires len(matches) >= 2 (extract.go:1064-1067), so a regex with no capture group that DOES match the body returns the misleading error 'no match for regex ...'; extractAllRegex silently skips such matches (extract.go:1213-1215). The sibling dialect used by the network protocol (pkg/protocol/jsonextract/jsonextract.go:97-110) falls back to the whole match instead, so the same expression behaves differently across protocols.

**Failure scenario:** An imported script carries extractor regex 'csrf-[0-9a-f]+' (no parentheses). The token is present in every response, yet the extractor reports 'no match for regex' and on_miss=fail fails every sample; the author debugs a 'missing' value that is plainly present, because the error message misstates the cause.

**Suggested fix:** Either fall back to the whole match (matching jsonextract), or return a distinct error 'regex matched but has no capture group' so the misconfiguration is diagnosable.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # W07 — Group-less regex extractor reports "no match" though pattern matched — CONFIRMED (low)

Code at HEAD: pkg/protocol/http/extract.go:1059-1071 — extractRegex calls re.FindSubmatch(body); when a pattern with zero capture groups matches, FindSubmatch returns a length-1 slice (whole match only), so the `len(matches) < 2` guard at extract.go:1065 returns the misleading error `no match for regex %q` even though the pattern matched. extractAllRegex (extract.go:1213-1215) silently `continue`s on `len(match) < 2`, dropping every such match.

Reachability: extractRegex is dispatched for extractor type "regex" on both header (extract.go:425) and body (extract.go:463) sources. No refuting guard exists: pkg/dsl/validate.go:786,790 only allowlists the type string; a repo-wide grep for NumSubexp shows the only capture-group check is in the Bruno importer (pkg/convert/bruno/native.go:3081) — hand-authored DSL and other import paths accept group-less regexes.

Divergence confirmed: pkg/protocol/jsonextract/jsonextract.go:102-109 (used by network protocol) falls back to the whole match when no group exists, so identical expressions behave differently across protocols.

Trigger/consequence realistic: a matching group-less regex yields a "no match" diagnostic; with on_miss=fail every sample fails while the value is plainly present. Not a regression of the prior fixed items (aggregate budget, extractJSONPath) — different symbol/behavior. Severity low (diagnosability/consistency, no data corruption) is apt.

</details>


#### 126. jsonextract JSONPath silently drops unparsable array indexes
**Where:** `pkg/protocol/jsonextract/jsonextract.go:73` · **Area:** AREA-17 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

splitPathToken (jsonextract.go:61-80) parses '[n]' with Sscanf and, on parse failure, silently skips the index (no error), so an expression like 'items[abc]' or a typo 'items[0' degrades to plain 'items' and returns the whole array as the extracted value instead of failing.

**Failure scenario:** A network/database extractor authored as 'records[O]' (letter O typo for 0) silently stores the JSON of the entire records array into the variable; a later request body embeds a JSON array where a scalar was expected, failing downstream in a way that never points at the typo.

**Suggested fix:** Return an error from splitPathToken/JSONPath when a bracket segment is present but unparsable.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # W08 — jsonextract silently drops unparsable array indexes — CONFIRMED (low)

pkg/protocol/jsonextract/jsonextract.go:61-80 splitPathToken: inside the bracket loop, line 73-75 does `if _, err := fmt.Sscanf(rest[1:end], "%d", &index); err == nil { indexes = append(...) }` with no else branch and no error return — an unparsable segment like "[abc]" is silently skipped. An unterminated "[0" hits `end <= 1 -> break` (69-70), also silently. Caller JSONPath (33-57) then applies only the key lookup and, with an empty index list, returns Stringify of the whole array (line 58 / 50-56 loop skipped), instead of erroring. So "items[abc]" or "records[O]" degrades to "items"/"records" and stores the entire array JSON, exactly as claimed; failure surfaces only downstream. Signature returns (string, []int) with no error, confirming no path reports the typo. Low severity appropriate.

</details>


#### 127. applySOAPDefaults force-overwrites an authored Content-Type header
**Where:** `pkg/protocol/soap/soap.go:755` · **Area:** AREA-17 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

applySOAPDefaults unconditionally stamps Content-Type via setHeaderCanonical (soap.go:755), unlike SOAPAction (whose existing value is preserved, soap.go:756-762) and Accept (only set when absent, soap.go:766-768). An authored or imported Content-Type — e.g. 'multipart/related' for MTOM-style payloads or a specific charset — is silently replaced with the version default ('text/xml; charset=utf-8').

**Failure scenario:** A migrated LR SOAP script sets Content-Type 'application/soap+xml; action="urn:op"' on a 1.1-configured service that requires it (or a multipart payload); the wire request goes out as text/xml, the server responds 415/fault, and the diff between authored headers and the snapshot is confusing because the action's own header was rewritten by the protocol.

**Suggested fix:** Only apply the default Content-Type when the action has none (mirroring Accept), or preserve authored parameters while normalizing the media type.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # W09 — applySOAPDefaults force-overwrites authored Content-Type — CONFIRMED (low)

soap.Execute always runs applySOAPDefaults on the prepared action (pkg/protocol/soap/soap.go:110, headers cloned from the authored action at :103). At soap.go:755 it unconditionally calls setHeaderCanonical(action.Headers, "Content-Type", defaultSOAPContentType(...)); setHeaderCanonical (soap.go:786-793) deletes any case-insensitive match and stamps the default, so an authored Content-Type (e.g. multipart/related or a custom charset) never reaches the wire. The asymmetry is real: SOAPAction preserves an existing value (soap.go:757-761 reads it when SOAP.Action is empty, then re-quotes it), and Accept is set only when absent (soap.go:766-768). No later code restores the authored header before delegation to http.Execute. Failure scenario holds; impact limited to SOAP scripts with deliberate non-default Content-Type — low severity is right.

</details>


#### 128. Persistent sidecar session JVM exit status is captured but never surfaced (waitErr is wri...
**Where:** `pkg/javasidecar/session.go:240` · **Area:** AREA-18 · **Category:** diagnostics · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

persistentSession.waitErr is assigned from cmd.Wait() (session.go:240) but read nowhere: failAndReap (session.go:424-462) and close (session.go:464-499) build the failure outcome from stdout/stderr/diagnostics only, so the ExecutionResponse for a died-JVM session carries error_code session_unusable with no exit status. The local runner has parity here — java_vuser/session.go:744-762 processExitError includes s.waitErr (exit code/signal) in the action error.

**Failure scenario:** A persistent sidecar JVM is OOM-killed by the kernel mid-iteration. The generator surfaces 'java_vuser sidecar execution failed (session_unusable)' plus whatever stderr noise was captured; nothing indicates the JVM died from SIGKILL vs a harness protocol fault, sending the operator down a contract-mismatch debugging path instead of a heap-sizing one. The same failure in local runner mode names the signal.

**Suggested fix:** In failAndReap, when waitDone is closed, append a bounded rendering of waitErr (exit status/signal) to the merged stderr diagnostics, mirroring the local runner's processExitError.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # J05 — CONFIRMED (low)

Grep over pkg/javasidecar shows exactly two `waitErr` occurrences: the field
declaration (pkg/javasidecar/session.go:52) and the single write
`session.waitErr = cmd.Wait()` (session.go:240). No read exists anywhere.
`failAndReap` (session.go:424-462) builds its `processOutcome` solely from
events/stdout/stderr/diagnostics snapshots — the exit status never enters
`mergedStderr` or the error code. `close` (session.go:464-499) returns only
`safeFailure` codes (`process_containment_unavailable` / context code).
Parity claim holds: local-mode `processExitError`
(pkg/protocol/java_vuser/session.go:744-762) wraps `s.waitErr` into the
surfaced error, so signal/exit-code (e.g. SIGKILL on OOM) is visible there but
lost in sidecar mode. Defect confirmed; diagnostics-only impact → low.

</details>


#### 129. Database assertions overwrite the root-cause SQL error message
**Where:** `pkg/protocol/database/database.go:194` · **Area:** AREA-18 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

evaluateDatabaseAssertions runs unconditionally (database.go:194-198) even when the action already failed (err set at 182-186), and a failing assertion replaces result.ErrorMessage; extractors are correctly gated on result.Success (database.go:187) but assertions are not.

**Failure scenario:** A db_query fails with a driver error ('relation "orders" does not exist'); the action's body_contains assertion then fails against the empty body and result.ErrorMessage becomes 'body_contains assertion failed: ... not found'. The run report and error taxonomy bucket the failure as an assertion failure and the actual SQL root cause is lost from the result row.

**Suggested fix:** Guard the assertion block with `if result.Success` (matching the extractor guard), or append rather than replace the message.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # J04 — CONFIRMED (low)

pkg/protocol/database/database.go:182-186 sets result.ErrorMessage from the SQL/driver error. Extractors are gated on `if result.Success` (line 187), but the assertion block at lines 194-198 runs unconditionally and assigns `result.ErrorMessage = assertionErr`, replacing the root cause. evaluateDatabaseAssertions (database.go:754-793) has no success/err guard and will fail e.g. body_contains against the empty/partial ResponseBody of a failed query (line 786-789 returns "body_contains assertion failed: %q not found"), so the driver error is overwritten and the failure is re-bucketed as an assertion failure. Causal chain holds; low severity (diagnostics only — Success/StatusCode already reflect failure).

</details>


#### 130. txPoisoned early return skips Duration, ProtocolEvidence and telemetry finalization
**Where:** `pkg/protocol/kafka/kafka.go:362` · **Area:** AREA-18 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Execute's poisoned-transaction guard (kafka.go:358-363) returns the result immediately, bypassing the shared finalization at kafka.go:411-430: result.Duration stays 0, ServerTelemetry stays nil, and ProtocolEvidence is never built, unlike every other failure path in the same function.

**Failure scenario:** After one failed transactional commit poisons the client, every subsequent action of that VUser (potentially thousands over the remaining run) is recorded with zero duration and no protocol evidence; run-report duration percentiles for the group are skewed toward zero and the evidence panel for those failures is empty, obscuring when the poisoning happened.

**Suggested fix:** Replace the early return with `err = fmt.Errorf(...)` falling through to the shared finalization block (mirrors how validateActionBounds failures at 347-354 at least set Duration/ProtocolEvidence).

<details><summary>Verification evidence</summary>

**confirmed @ low:** # J03 — CONFIRMED (low)

pkg/protocol/kafka/kafka.go:358-363: the txPoisoned guard sets Success=false/StatusCode=500/ErrorMessage then `return result, nil` immediately, skipping the shared finalization: result.Duration is set at line 411, ServerTelemetry at 412-414, and ProtocolEvidence at line 430 — none run, so Duration stays 0, telemetry nil, evidence nil. Contrast: the adjacent validateActionBounds failure path (kafka.go:347-354) does set Duration and ProtocolEvidence before returning. Since txPoisoned persists for the client's remaining lifetime, every subsequent action of that VUser records zero duration and no evidence, skewing duration stats and hiding the poisoning context. Low severity fits (diagnostics/telemetry only, already-failing actions).

</details>


#### 131. Consume served from the pending buffer always reports consumer lag 0
**Where:** `pkg/protocol/kafka/realclient_franz.go:440` · **Area:** AREA-18 · **Category:** telemetry · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

When Consume returns a record stashed in c.pending by an earlier poll (realclient_franz.go:426-443), it returns lag 0 unconditionally (`return record, 0, nil` at line 440), so the kafka_consumer_lag extraction (kafka.go:780) reads 0 for those samples even when the partition backlog is large; only records fetched fresh compute lag from the high watermark (realclient_franz.go:468-476).

**Failure scenario:** A multi-topic script alternates kafka_consume between topics A and B; polls for A frequently buffer B records into pending. Every B consume served from the buffer reports kafka_consumer_lag=0 while the real backlog grows, so a lag-based SLA/assertion or dashboard trend on that extraction shows a sawtooth of false zeros and understates backlog during exactly the interleaved-consumption pattern that creates it.

**Suggested fix:** Record the HighWatermark alongside each buffered pendingReply at stash time (available from the fetch) and compute lag = hw - offset - 1 when serving from the buffer, or return -1/omit the extraction to mark it unknown rather than claiming 0.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # J06 — CONFIRMED (low)

pkg/protocol/kafka/realclient_franz.go:426-443: when a matching record is
found in `c.pending`, the buffered path ends with `return record, 0, nil`
(line 440) — lag hardcoded to 0. The buffer is populated cross-topic: during a
poll for topic A, records for other topics are stashed via `addPendingLocked`
(lines 453-465), so a later Consume of topic B is served from pending and
skips the fresh-fetch path (467-483) that computes
`lag = highWatermark - offset - 1` from `fetches.EachPartition`. The stashed
`pendingReply` carries no high-watermark, so the buffered path cannot compute
lag. Downstream, kafka.go:764 receives the lag and kafka.go:780 emits it as
`Extractions["kafka_consumer_lag"]`, producing false zeros exactly in the
interleaved multi-topic pattern claimed. Telemetry-only impact → low.

</details>


#### 132. Slow-controller live streams silently drop whole metric windows with only a cumulative co...
**Where:** `pkg/distributed/generatorrpc/metrics_broker.go:91` · **Area:** AREA-19 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

A metrics subscriber queue is capped at 16 batches (metrics_broker.go:17); when the controller's StreamMetrics consumer stalls, non-terminal batches are dropped at enqueue (91-96) and only counted in broker.droppedBatches, which surfaces as a cumulative DroppedSubscriberBatches value inside LATER batches (service.go:2154). MetricsBatch carries no sequence number (generator.proto:194-217), so the controller cannot tell WHICH windows were lost or re-request them; each batch is the only carrier of its window's per-window aggregates, so the live view permanently loses those intervals (the generator-local archive does retain them for post-run fetch, which bounds the damage).

**Failure scenario:** During a 30s controller GC pause or network stall on a run publishing 1 batch/s, ~14 windows are dropped after the 16-deep queue fills; the live Command Center shows a gap-free line (subsequent timestamps just jump) with no gap marker, and the only evidence is a counter buried in later batch telemetry. Terminal state is protected (tombstone replay), so results are eventually correct from the archive, but live-view operators see silently thinned data during exactly the incidents they are watching for.

**Suggested fix:** Add a per-subscriber monotonically increasing sequence (or per-test batch index) to MetricsBatch so the controller can detect and annotate gaps; optionally coalesce dropped windows into the next batch's telemetry as a delta rather than a lifetime cumulative counter.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # S06 — CONFIRMED (low)

metricsSubscriberQueueCapacity=16 (metrics_broker.go:17); enqueue drops non-terminal batches at capacity, returning 1 (metrics_broker.go:91-93), counted only into broker.droppedBatches (345-347). The count surfaces solely as cumulative telemetry.DroppedSubscriberBatches on later batches (service.go:2154); controller merge just copies/sums it (pkg/api/distributed_metrics.go:126,207). MetricsBatch (api/proto/generator.proto:194-217) has timestamp_unix_ms and window_ms but no sequence/batch-index field, so the controller cannot identify which windows were lost; subscriber re-registration replays only the current live snapshot plus terminal tombstones (metrics_broker.go:250-261), not missed windows. Damage bounded as claimed: recordMetricsBatch archives every interval locally (service.go:1656-1681) independent of subscribers, so only the live view is thinned. Low severity fits.

</details>


#### 133. RetainImportSource silently drops parent-revision lineage on query error
**Where:** `pkg/storage/sqlite_import_source_retention.go:149` · **Area:** AREA-21 · **Category:** error-handling · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The previous-active-revision lookup 'if err := tx.QueryRow(...).Scan(&previousActiveID); err == nil { revision.ParentRevisionID = previousActiveID }' treats every error — including real driver/transaction failures, not just sql.ErrNoRows — as 'no previous revision', so a transient error yields a committed revision with NULL parent_revision_id instead of failing the operation.

**Failure scenario:** During a re-import that retains source for an existing script, the SELECT for the current active revision fails transiently (context cancellation propagated into the tx, or an I/O error). The retention proceeds, deactivates all prior revisions, and commits a new active revision whose parent_revision_id is NULL — the revision chain for that script is permanently broken (history/diff views can no longer link the new revision to its predecessor), with no error surfaced anywhere.

**Suggested fix:** Handle the error explicitly: on sql.ErrNoRows leave ParentRevisionID empty; on any other error return it.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # S13 — RetainImportSource drops parent lineage on query error — CONFIRMED (low)

pkg/storage/sqlite_import_source_retention.go:148-152: `if err := tx.QueryRow(...active_flag = 1...).Scan(&previousActiveID); err == nil { revision.ParentRevisionID = previousActiveID }` — any non-nil error (scan/conversion failure, transient driver error) is conflated with sql.ErrNoRows. Execution then proceeds: line 153-156 deactivates all prior revisions, lines 162-174 insert the new revision with nullableString(ParentRevisionID)=NULL, line 175 commits. On SQLite a per-statement error does not abort the tx, so the silent NULL-parent commit is reachable. Contrast: the same lookup in pkg/storage/sqlite_bruno_imports.go:111-115 correctly returns non-ErrNoRows errors. Broken lineage, no data loss — low severity holds.

</details>


#### 134. powerbi_publishes rows are never cleaned up on test or project deletion
**Where:** `pkg/storage/sqlite_powerbi.go:28` · **Area:** AREA-21 · **Category:** retention-orphans · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

powerbi_publishes (sqlite_schema.go:685-697) stores test_id and project_id with no foreign keys, and there is no 'DELETE FROM powerbi_publishes' anywhere in the package: deleteTestGraphTx (sqlite_tests.go:1323, which does clean grafana_dashboard_snapshots at line 1372) and the project-deletion cascade (sqlite_projects.go:1300-1361) both omit it. Publish-history rows referencing deleted tests/projects accumulate indefinitely.

**Failure scenario:** An installation publishes runs to Power BI routinely and prunes old tests on a retention schedule. Every deleted test leaves its powerbi_publishes row(s) behind forever: unbounded slow growth of a table that can never shrink, and ListPowerBIPublishesForTest/project listings can return records pointing at tests that no longer exist, confusing the publish-history UI.

**Suggested fix:** Add 'DELETE FROM powerbi_publishes WHERE test_id = ?' to deleteTestGraphTx and 'DELETE FROM powerbi_publishes WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)' to the project cascade.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # S12 — powerbi_publishes never cleaned up — CONFIRMED (low)

DDL at pkg/storage/sqlite_schema.go:685-700 stores test_id and project_id with no REFERENCES clause, so no FK error ever surfaces the gap. A grep across pkg/ finds zero `DELETE FROM powerbi_publishes` in non-test code; the only writers are INSERT/UPDATE in pkg/storage/sqlite_powerbi.go:40/69. deleteTestGraphTx (pkg/storage/sqlite_tests.go:1323) cleans grafana_dashboard_snapshots at line 1372 but never touches powerbi_publishes, and the project-deletion cascade in sqlite_projects.go contains no reference to the table either (grep confirms only sqlite_powerbi.go and sqlite_schema.go mention it). Rows referencing deleted tests/projects therefore accumulate indefinitely. Orphan-growth/stale-listing impact only; low severity is appropriate.

</details>


#### 135. RedactCredentialBody re-serializes form bodies: params re-sorted, escapes normalized, sen...
**Where:** `pkg/scrub/credentials.go:98` · **Area:** AREA-22 · **Category:** data-fidelity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The form-encoded branch of RedactCredentialBody (credentials.go:98-111) parses the whole body with url.ParseQuery and re-emits it with values.Encode() whenever any credential field is present. Encode() sorts keys alphabetically and re-escapes every value, so the recorded baseline's parameter order and byte spellings (lowercase escapes, '+' vs '%20') are rewritten for all non-credential fields too, and the '[REDACTED]' sentinel itself is emitted percent-encoded as '%5BREDACTED%5D' — a spelling the rest of the codebase's sentinel checks (e.g. scrub.go:212's HasPrefix(matches[2], Redacted) idempotence guards and any UI matching on [REDACTED]) do not recognize.

**Failure scenario:** Recorded baseline body 'z=%2fpath&b=2&password=x&a=1' becomes 'a=1&b=2&password=%5BREDACTED%5D&z=%2Fpath': side-by-side source review shows a reordered, re-escaped body that no longer matches the recording; a correlation/consistency check that looks for the original '%2fpath' spelling in the stored baseline misses; UI code looking for the literal '[REDACTED]' marker does not flag the field as redacted.

**Suggested fix:** Redact in place instead of round-tripping: reuse redactSensitiveQueryAssignments (already called as fallback at credentials.go:113) as the primary mechanism with isCredentialField, which surgically replaces only the credential value bytes and preserves order/spelling and the plain sentinel.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # T05 — CONFIRMED (low)

pkg/scrub/credentials.go:97-110: when any credential field is present, the whole form body is round-tripped through `url.ParseQuery` then `values.Encode()`. Go's documented `url.Values.Encode` sorts keys alphabetically and QueryEscapes every value, so non-credential params are re-ordered and re-escaped (`%2f`→`%2F`, space→`+`), and the sentinel `Redacted = "[REDACTED]"` (pkg/credential/credential.go:27) is emitted as `%5BREDACTED%5D`. scrub.go:212 and :220 idempotence guards use `strings.HasPrefix(..., Redacted)` against the literal spelling and would not recognize the percent-encoded form. Package comment (credentials.go:12-19) confirms this path exists to preserve the recorded baseline for correlation, which the re-serialization undermines. Low severity is right: values are redacted correctly; harm is baseline fidelity/sentinel-matching only.

</details>


#### 136. HA active-EndpointSlice port is hardcoded to 8080 regardless of APEXLOAD_ADDR
**Where:** `cmd/controller/main.go:429` · **Area:** AREA-23 · **Category:** operational-correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

activeEndpointPublisherFromEnv (cmd/controller/main.go:397-431) always publishes PortName "http"/Port 8080 into the leader EndpointSlice, while the actual listen port comes from APEXLOAD_ADDR via config.ResolveControllerBindAddr (main.go:71, pkg/config/bind_addr.go:16-28) and is operator-configurable.

**Failure scenario:** An operator overrides APEXLOAD_ADDR=:9443 (e.g. to co-exist with another listener) in an HA deployment; leadership is won and the slice publishes 8080, so the apexload-controller-active Service targetPort routes execution-mutating CI traffic to a closed port — followers 503 and the leader is unreachable, breaking all execution-routing until the port is reverted.

**Suggested fix:** Derive the published port from the resolved bind address (or a dedicated APEXLOAD_ACTIVE_ENDPOINT_PORT env) and fail startup on mismatch.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # C07 — CONFIRMED (low)

cmd/controller/main.go:422-430: `activeEndpointPublisherFromEnv` hardcodes `PortName: "http", Port: 8080`; no env var exists to change it. pkg/kubeops/active_endpoint.go:251-254 publishes exactly `cfg.Port` into the EndpointSlice (`ownPort`, used at :125). Meanwhile the real listener honors `APEXLOAD_ADDR` (cmd/controller/main.go:71 → pkg/config/bind_addr.go:20-21), so setting `APEXLOAD_ADDR=:9443` moves the listener but the leader slice still advertises 8080 → active-Service routes to a closed port. Mitigation check: shipped manifests pin `APEXLOAD_ADDR=":8080"` (deployments/k8s/onprem/controller-deployment.yaml:61-62; deployments/helm/apexload/templates/controller.yaml:167-168, literal, not values-templated), so defect is latent and only triggers on operator override — severity low stands.

</details>


#### 137. Helm default nodeSelector strands generator pods Pending on clusters without the generato...
**Where:** `deployments/helm/apexload/values.yaml:107` · **Area:** AREA-23 · **Category:** helm-kustomize-parity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

values.yaml:107-113 ships nodeSelector apexload.io/node-pool: generators (plus a matching toleration) as the default, so a stock helm install schedules zero generator pods unless nodes are pre-labeled; the equivalent kustomize onprem base leaves the same selector commented out as an optional recommendation (deployments/k8s/onprem/generator-statefulset.yaml:24-33), a defaults divergence between the two packaging paths for the same knob.

**Failure scenario:** First-time helm install on a standard AKS cluster: controller comes up, all generator pods sit Pending on an unsatisfiable nodeSelector, distributed runs fail to stage, and (with keda.enabled=true default) KEDA scale-ups just add more Pending pods; nothing in values.yaml warns that node labeling is a prerequisite.

**Suggested fix:** Default nodeSelector/tolerations to empty ({}/[]) and document the dedicated-pool recommendation in README/values comments, matching the kustomize base.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # C08 — CONFIRMED (low)

deployments/helm/apexload/values.yaml:107-113 ships default `generator.nodeSelector: {apexload.io/node-pool: generators}` plus matching toleration; templates/generator.yaml:61-62 (`{{- with .Values.generator.nodeSelector }}`) renders it into the StatefulSet, so a stock `helm install` on an unlabeled cluster leaves all generator pods Pending (unsatisfiable selector). Divergence confirmed: kustomize base deployments/k8s/onprem/generator-statefulset.yaml:24-33 has the identical selector/toleration commented out as an optional recommendation ("Label/taint your nodes ... and uncomment"). No warning anywhere in the chart: values.yaml lines 102-113 carry no comment, no NOTES.txt exists in templates/, and README.md has zero mentions of node-pool/nodeSelector/labeling. Severity low stands (symptom is standard, diagnosable Pending state).

</details>


#### 138. Production dashboard is built with Node 26 while CI validates it with Node 22
**Where:** `Dockerfile:3` · **Area:** AREA-23 · **Category:** operational-correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Dockerfile:3 pins the dashboard build stage to node:26.5.0-alpine3.23, but every dashboard job in .github/workflows/ci.yml pins node-version 22.22.0 (lines 177, 439, 549), and dashboard/package.json declares no engines constraint — so lint/typecheck/vitest/build validation runs on a different Node major than the one that produces the shipped production bundle.

**Failure scenario:** A vite/rollup/esbuild toolchain behavior difference or native-module incompatibility that only manifests on Node 26 breaks or subtly corrupts the embedded dashboard bundle in the release image while every CI job stays green on Node 22; the divergence is discovered only in a deployed controller.

**Suggested fix:** Align both to one pinned Node version (add an engines field, use the same version in CI setup-node and the Dockerfile builder stage; ideally derive both from one source such as .nvmrc).

<details><summary>Verification evidence</summary>

**downgraded @ low:** # C03 — Node 26 build vs Node 22 CI validation — CONFIRMED, downgraded to low

Skew is real: Dockerfile:3 pins `node:26.5.0-alpine3.23` for dashboard-builder; .github/workflows/ci.yml:177,439,549 pin setup-node 22.22.0 for all dashboard lint/typecheck/vitest/build jobs; dashboard/package.json has no engines/packageManager and no .nvmrc exists. However, the failure scenario is partly mitigated: the `containers` CI job (ci.yml:840-887) builds the exact main Dockerfile (4 controller/generator matrix entries, docker/build-push-action) and smoke-tests the controller image, so a hard Node-26 build break or native-module failure DOES fail CI. Only silent behavioral/bundle differences (validated on 22, shipped from 26) survive — a real but lower-impact skew. Downgrade medium → low.

</details>


## Opportunities (non-defect)

24 verified improvement opportunities (Effort S/M/L/XL, Impact/Risk low/med/high), quick wins first. These do not enter the fix phase.

| ID | Title | Where | Area | Eff | Imp | Risk |
|----|-------|-------|------|-----|-----|------|
| O8 | GitHub sync jobs record per-file failures only as bare counters — no reason, no log, misle | `pkg/api/github_sync_service.go:103` | AREA-09 | S | med | low |
| O9 | Every browser-replay GET takes the manager write lock and runs a full prune sweep | `pkg/api/browser_replay_manager.go:794` | AREA-10 | S | med | low |
| O14 | matchHAR re-parses URLs O(refs x entries) times | `pkg/convert/salesforce/analyzer.go:440` | AREA-14 | S | med | low |
| O15 | Legacy singlestat panels are skipped although the accepted schema range routinely contains | `pkg/convert/grafana/converter.go:695` | AREA-14 | S | med | low |
| O11 | extractDatabaseBinds builds heuristic bind order from Go map iteration | `pkg/convert/enterprisearchive/database_import.go:551` | AREA-11 | S | low | low |
| O13 | Nine duplicated maxLogicalSecretNameBytes=249 constants shadow credential.MaxLogicalNameBy | `pkg/convert/bruno/native.go:2798` | AREA-13 | S | low | low |
| O16 | projectJSONExampleCredentials can never report unsafe, leaving a dead review code and guar | `pkg/convert/openapi/converter.go:2287` | AREA-14 | S | low | low |
| O17 | Dead helper addTokensForElapsed and one-interval Resume hiccup in TokenBucket.SetPaused | `pkg/engine/pacing.go:341` | AREA-16 | S | low | low |
| O20 | GetDeploymentLease performs write statements on every read | `pkg/storage/deployment_lease.go:178` | AREA-21 | S | low | low |
| O23 | jsonextract.Regex recompiles the pattern on every call | `pkg/protocol/jsonextract/jsonextract.go:98` | AREA-17 | S | low | low |
| O24 | apexctl wait/status conflate transport failure and SLA breach into exit 1 | `cmd/apexctl/commands.go:206` | AREA-23 | S | low | low |
| O22 | Translate static k6 sleep()/status-check patterns instead of review-gating every request | `pkg/convert/k6/converter.go:888` | AREA-15 | M | high | med |
| O1 | Schedule-occurrence tables have no retention while the sibling timeslot table does | `pkg/api/timeslot_retention.go:3` | AREA-02 | M | med | low |
| O2 | Schedule CRUD, approval resolution, and timeslot bind hold the global runner write lock ac | `pkg/api/scheduler.go:2548` | AREA-02 | M | med | med |
| O4 | Parallelize per-target script distribution and shard assignment during distributed startup | `pkg/api/distributed_execution_backend.go:939` | AREA-05 | M | med | med |
| O5 | Scenario list enrichment is O(scenarios x tests) under the runner read lock | `pkg/api/scenario.go:1067` | AREA-07 | M | med | low |
| O10 | Retire or quarantine the dead native Java lanes and their pipeline-reconstruction tests | `pkg/convert/enterprisearchive/java_import.go:622` | AREA-11 | M | med | low |
| O12 | Native import is all-or-nothing: one malformed request file rejects every candidate | `pkg/convert/bruno/native.go:276` | AREA-13 | M | med | med |
| O18 | Deletion graphs are hand-maintained; add a schema-derived coverage guard test | `pkg/storage/sqlite_projects.go:1223` | AREA-20 | M | med | low |
| O19 | rewriteSQL substring translation is a latent Postgres-parity trap | `pkg/storage/sqlite.go:377` | AREA-21 | M | med | low |
| O6 | Route access rules are linear-scanned with per-rule string splits, 3x per request | `pkg/api/route_catalog.go:399` | AREA-08 | M | low | low |
| O7 | User listing for scoped admins is an O(users x bindings) N+1 over the store | `pkg/api/handlers_auth.go:1673` | AREA-08 | M | low | low |
| O3 | Import response builder re-serializes the same script tree many times per request | `pkg/api/import_response.go:83` | AREA-04 | L | med | med |
| O21 | Solace JMS sidecar forks one OS process per messaging action | `pkg/protocol/jms/sidecar_adapter.go:130` | AREA-18 | L | med | med |

_1 raw findings were duplicates of the above (multiple finders converging on the same site) and were merged._

# Appendix: per-area assessments

**AREA-01** — raw 7, confirmed 7, refuted 0

api-execution-core is in unusually strong shape for its complexity. The run state machine (start/stop/pause/resume/adjust/scale) is consistently hardened: every durable transition has a rollback-on-persist-failure path, terminal transitions are fenced by execution-lease tokens checked transactionally, stop is fail-closed with a versioned force-upgrade retry worker, leadership epochs cancel operation contexts synchronously, retry workers exit when leadership is lost, and restart reconciliation (reconcileStoredActiveTest) prevents runs wedging in non-terminal states. Bounded-growth discipline is real (error patterns/evidence caps, per-test history byte caps, route cache LRU, group-metrics history cache LRU). The defects found are concentrated in performance/scalability -- storage I/O and multi-megabyte script serialization performed while holding the global tr.mu (the repo's known 'locks held across DB I/O' class), per-VUser-step durable persistence during ramps, and unbounded aggregate memory from boot-time history hydration -- plus two data-accuracy issues (the local goal controller's stale VUser view and usage accounting billing configured rather than actual duration). No crash, data-loss, or wedged-run defect was found.

_Boundary leads:_ pkg/api/metrics.go:52-57 builds local LiveMetrics.TotalTx/TotalErrors from per-window engine snapshots (window semantics proven at pkg/engine/metrics.go:458-475, batch cleared each publish), while the distributed path sets the same fields from FleetMetrics at pkg/api/distributed_metrics.go:431 (fleet.Count - fleet.Errors). If FleetMetrics.Count is cumulative for the run (generator side, pkg/distributed/generatorrpc/service.go), the identical WebSocket/history field 'total_transactions' silently changes meaning between local and distributed backends -- distributed-metrics/frontend areas should confirm. · pkg/api/launch_preflight.go:258 evaluateGeneratorPlacementConflictsLocked overlays tr.cachedGeneratorInventorySnapshot(); immediately after controller boot (before the first inventory refresh) the cache can be empty/stale so active targets carry zero AvailableCapacity/protocol data -- whether chooseGeneratorShardsForGroup then rejects valid launches (preflight false negative) depends on distributed assignment code outside this area (pkg/api/distributed_assignment*.go) and should be verified there.

**AREA-02** — raw 6, confirmed 6, refuted 0

This area is in unusually strong shape for its complexity. Timeslot/reservation double-booking is genuinely closed: binds, releases, approvals, and edits all go through real store transactions with reciprocal test-row updates, updated_at/revision CAS tokens, and lease-term fencing threaded through every durable write (verified down into pkg/storage/sqlite_timeslots.go — the approve/edit TOCTOU I hunted for is correctly prevented by the updated_at CAS). Occurrence idempotency (deterministic test IDs, materializing claims, transition tokens, crash-recovery reconciliation) is thorough on both the scenario and execution-plan sides, admission fails closed on reservation lookup errors, and the cron evaluator handles timezones, POSIX dom/dow OR semantics, Quartz specifiers, and leap-day horizons with tests. The defects that remain are second-order: an idempotence gap that makes conflict-blocked schedules rewrite themselves (revision++) every 30s sweep and break operators' required If-Match edits; a DST fall-back double-fire for schedules inside the repeated hour; and read-path lock/SQL amplification where the fair-share pending sort issues a reservation-store query per sort comparison under the global write lock on every test list read. Nothing found rises to data loss or corruption.

_Boundary leads:_ pkg/api/execution_plan_fencing.go:47-64 — executionLeaseTermCurrent performs a durable GetSchedulerLease SELECT on every call and is invoked once per schedule per sweep iteration while tr.mu is held (pkg/api/scheduler.go:341); under HA+Postgres that is O(schedules) lease reads inside the global lock every 30s. The lease-check design is owned by the HA/leadership area. · pkg/api/tests.go:484-741 — list/read endpoints take the global WRITE lock (refreshReadMetadataForTestList/refreshReadMetadataForTest) to refresh pending-queue metadata; that read-path locking policy belongs to the runner/tests area but is the amplifier for my fair-share ranking finding.

**AREA-03** — raw 7, confirmed 7, refuted 0

This area is in genuinely strong shape at the durability layer: the finalization/cleanup/version outboxes are token-fenced with CAS transitions, orphan recovery is identity-checked and create-only, the materialization cache has correct reader/evictor coordination (keyed gates, identity re-checks, marker-based LRU), quantiles avoid the classic percentile-remerge trap by feeding raw durations into exact-then-t-digest accumulators, and the sanitize/export path preserves counts, sizes, and hashes with change detection. The report registry (HTML/PDF/CSV from one section table) is clean and consistent. The real weaknesses cluster in two places: (1) distributed-run archives merge per-generator interval summaries by plain concatenation, which silently breaks the archived report's fleet-level headline metrics, timeline ordering, and bucket attribution for every multi-generator run; and (2) several read endpoints do full-archive work per request with no cache or streaming (manifest sanitize pre-pass, release-trend sibling-report rebuilds against a 16-entry LRU, load-comparison full-slice reads that hard-fail at 500k transactions), which turns normal-sized perf-test archives into multi-second-to-failing requests.

_Boundary leads:_ pkg/distributed/generatorrpc/service.go:1656-1690 — generators archive per-generator, per-window interval summaries (TPS/ActiveVUsers local, TotalTx per-window); fixing AREA-03's distributed report wrongness likely needs merge-time aggregation or a generator-side schema change owned by the distributed area. · pkg/api/dynatrace_dry_run.go:201 — readAllArchiveTransactions error is silently discarded, so archives over 500k records / 128MB quietly produce a dry-run with no transaction evidence (same bounded-reader hard limit as the load-comparison finding, but in the integrations area). · pkg/api/library_bundle.go:433 — sanitizedRunArchiveMetadata performs a full transactions-file sanitize pass per bundled archive during library bundle builds (same uncached full-scan seam as the manifest-endpoint finding, owned by the library area).

**AREA-04** — raw 4, confirmed 4, refuted 0

The api-import-orchestration area is in unusually strong shape for its size and churn history. The historical defect classes this repo has been burned by are now systematically defended: the shared importCommitSpec boundary (import_commit_service.go) gives OpenAPI/Postman/AsyncAPI one create/replace path with deterministic create IDs (protectedImportCreateScriptID), idempotent-replay reporting, and honest 503 partial-state responses when source retention lags the durable script write; the source-retention/commit-fence pair (import_source_retention.go, import_source_commit_fence.go) fingerprints scripts and predecessor owners so a stale retry cannot attach a source to a newer revision; the Bruno one-shot session lifecycle (claim/restore/complete with a renewing lease, publication journals, and a reference-checked janitor) is genuinely careful about crash windows; and JMeter direct-import uses receipts plus a write-ahead createImportTransaction with deterministic asset IDs and exact-equivalence adoption on retry. Preserved-update and draft-attestation logic protect user edits and credentials through masked read-modify-write. The defects that remain are second-order: a silent multi-JMX drop at the JMeter ZIP boundary, a whole-server single-slot Bruno inspect semaphore that includes the request-body network read in its critical section, and one merge-re-import edge that replaces a user-customized data source while the diff claims preservation. The main structural cost is the response-projection layer, which re-serializes the same scripts many times per import request.

_Boundary leads:_ pkg/api/script.go:846 (createScriptDurable) re-applies preparePersistentImportedLibraryScript to scripts that handleSaveJMeterImport already projected at pkg/api/handlers_import_jmeter.go:403; the crash-retry adoption path (directImportPreparedAssetsMatch, pkg/api/direct_import_idempotency.go:446) only adopts on exact equivalence, so if any projection step is ever non-idempotent a retry after a crash 500s instead of adopting the committed assets. Owned by the scripts-persistence area; worth a fixpoint test there.

**AREA-05** — raw 7, confirmed 7, refuted 0

This area is engineered to an unusually high standard: execution-epoch fencing (claim TTL derated by transit budget, per-cycle re-verification), fenced per-target metrics consumers with topology-generation revalidation at every mutation point, staged AssignWork + coordinated CommitWork barriers, and compensation/rollback on every control fanout (pause, duration, vusers, evacuation). Placement math conserves VUser totals (baseline+floor+largest-remainder with per-target caps), and metrics aggregation applies correct semantics per field (rates summed per-window, gauges maxed, process CPU averaged over active pods). The genuine weak spots are in fault tolerance for a generator that is permanently unreachable: every fail-closed path assumes the fenced party can eventually ACK an exact-generation stop, so the one scenario tolerate_generator_loss advertises (a dead generator pod) instead force-stops the whole fleet and can leave the run stuck non-terminal in STOPPING behind an unbounded retry. Secondary issues are lock-scope (a config read-lock held across cross-controller HTTP; DB reads under the global runner mutex in launch gating) and fleet-alert webhooks that only fire when someone polls the GET endpoint.

_Boundary leads:_ testexecution.go:1173 and runStopTeardownRetryWorker (testexecution.go:1273+): stopping a RUNNING distributed test whose generator is permanently unreachable appears to loop in STOPPING forever — stopTestContext (pkg/api/distributed_execution_backend.go:1370-1376) tolerates Unavailable/NotFound only when status is already COMPLETE/FAILED, and the retry worker that owns recovery lives outside this area · pkg/api/capacity.go:367 (liveInventory writer): verify tr.mu -> liveInventoryMu lock ordering holds on all paths; cachedGeneratorInventorySnapshot (pkg/api/external_runner_gating.go:569-585) acquires liveInventoryMu.RLock while callers hold tr.mu · pkg/distributed/generatorrpc/service.go:1988: the generator currently always emits FleetMetrics alongside Groups; if a batch ever carried Groups[].Metrics plus raw Transactions without FleetMetrics, aggregateDistributedMetricsBatches (pkg/api/distributed_metrics.go:74-98) would double-count group TPS from both sources

**AREA-06** — raw 6, confirmed 6, refuted 0

This area is in genuinely strong shape at its core: pkg/dslwalk is a clean, budget-enforced traversal contract that really does cover every nested surface (control.steps/else plus all four slice forms of http_sequence params.requests — I verified no other catalog action nests children); the DSL action catalog is data-driven with schemagen freshness tests (TestGeneratedSchemaIsFresh) that make catalog drift a CI failure rather than a latent bug; script CRUD in pkg/api/script.go is CAS-fenced against concurrent editors and HA replicas on every mutation path; and the masked-write reconciliation in script_projection.go, while very dense, is carefully layered with explicit fail-closed decisions and transient-gate round-trip handling. The debug manager's lease/fence/abandon-backstop lifecycle is unusually robust against wedged protocol adapters — the historical session-leak defect class appears well closed. The real defects I found are at seams rather than in the cores: the debug manager's per-event broadcasts re-serialize the entire accumulated session (quadratic cost on large imported scripts) and its logs/events retain full wire bodies uncapped; the object-repository bind-from-script flow binds nested actions by name while its ambiguity/collection scan is top-level-only (the exact mis-binding its own comment tries to prevent); one replay endpoint missed the credential-preserving-echo special case its debug sibling received; and replay progress does not count as session activity for idle-cancel purposes.

_Boundary leads:_ pkg/api/handlers_replay.go:101-112 — handleReplayScript (POST /scripts/{id}/replay) has the same missing matchesStoredScriptDisclosureProjection special case as the in-scope replay-session handler (finding 3); a shared fix should cover all three sibling endpoints · pkg/api/websocket.go:292-299 — Hub.BroadcastJSON marshals every payload even with zero subscribers, amplifying the quadratic debug-session broadcast cost (finding 1); hub is owned by another area · pkg/engine/replay.go:987 — report.Events appends one event per executed leaf including every control_loop iteration with no cap below MaxControlLoopIterations=1,000,000, so the engine-side replay report itself can grow to GBs on high-iteration debug replays; engine area owns the cap decision

**AREA-07** — raw 8, confirmed 8, refuted 0

This area is in genuinely good shape overall: optimistic concurrency is pervasive and correctly plumbed (SHA-based group-settings revisions shared by both Command Center editors, data-pool row-version CAS whose storage layer returns the incremented token, revision-fenced cursor pagination and a three-pass revision-fenced CSV export that aborts the connection rather than serving a torn file), the masked-secret protocol is reorder-safe and consistently fail-closed, the data-pool import/export round-trip (formula-guard encode/decode, engine-cap preflight, APEX-export envelope) is carefully engineered, and the typed-nil storage gotcha from past reviews now has defense-in-depth in the decoders. The engine-side row handout is shard-aware, so the classic distributed 'unique' duplicate problem does not exist. The defects found are integration seams rather than rot: the new Command Center runtime-settings editor collides with create-time iteration-override provenance, the '@'-anywhere credential heuristic over-classifies plain emails and breaks legitimate parameter/Env workflows, the schedule-health hot path counts statuses by materializing an unbounded occurrence table (polled every 5s), and the run manifest's 'initial configuration' is sourced from the continuously rewritten durable record so it mirrors current state.

_Boundary leads:_ pkg/api/persistence.go:1373 buildManagedTestRecord boxes test.Assignment/test.GoalConfig/test.RuntimeSettings directly into storage `any` fields instead of using the generatorAssignmentForStorage/runtimeSettingsForStorage/goalConfigForStorage helpers used by the scenario path (pkg/api/scenario.go:320-322); currently safe only because decodeStoredGeneratorAssignment/decodeStoredRuntimeSettings carry typed-nil defenses (pkg/api/stored_decode.go:203, :97) — a new decoder without that defense reintroduces the known {Mode:"auto"} typed-nil bug · dashboard/src/components/MonitoringPage.tsx:687 polls ~13 endpoints including /scenarios/schedules/health every 5 seconds; the cadence amplifies the backend full-table scan reported in this area's schedule-health finding and belongs to the frontend reviewer · pkg/storage/sqlite_schedules.go:972 has no bounded/aggregating alternative (no COUNT-by-status query) and pkg/storage has no retention pruning for scenario_schedule_occurrences; fixing the health-endpoint finding needs a storage-side status-count query or retention job

**AREA-08** — raw 8, confirmed 8, refuted 0

api-platform-core is in genuinely strong shape — this is some of the most deliberately hardened code in the repo. The websocket Hub is bounded end-to-end (client slots, send-buffer drops, fanout WG + slots, periodic re-authorization), stored_decode has explicit typed-nil guards closing the historical {Mode:"auto"} class, the settings PUT path correctly layers omitted-section merge, write-only secret preservation, tri-state thresholds, and revision-fenced CAS, server lifecycle (Start/Shutdown/launchBackground with panic recovery and no-resurrection invariants) is exemplary, and the imported-dashboard fan-out uses CAS-reserved point/byte budgets with shared semaphores. The declarative route catalog plus test-enforced fail-closed default means no registered-but-unpoliced route drift was found. The defects that remain are edge/operational rather than headline: project/test deletion misses the newest child table (powerbi_publishes), the controller-wide project-mutation gate can stall every write during a slow deletion, the idempotency layer is at-most-once rather than idempotent (failed requests burn the key), the redis rate limiter has the classic INCR/EXPIRE atomicity gap, the default in-memory cache never sweeps expired entries, and the origin policy ignores X-Forwarded-Proto so same-origin detection fails behind TLS-terminating proxies.

_Boundary leads:_ pkg/storage/sqlite_projects.go:1289-1364 (deleteProjectGraphTx) and pkg/storage/sqlite_tests.go:1323-1394 (deleteTestGraphTx) are where the powerbi_publishes cascade entries are missing — the fix for the orphaned-rows finding lands in storage, not pkg/api · pkg/api/auth_resolution.go:479 and 632 (requiredServiceAccountScopes / requiredLibraryImportServiceAccountScope) call decodeRequestBody, which io.ReadAll-buffers the ENTIRE request body (up to the 128-272 MiB import limits) into memory purely to resolve a scope, then the handler buffers/parses it again — 2x memory amplification for service-account library imports and project-scoped JSON imports; the shared body-buffering design belongs to the import areas · pkg/api/audit.go:61-64 persistAuditEvent issues a GetProject DB read on every audit event to backfill domain_id — every audited mutation platform-wide pays an extra store round-trip; worth a small cache or a domain_id pass-through from handlers that already loaded the project

**AREA-09** — raw 5, confirmed 5, refuted 0

This area is in unusually strong shape. The CI run lifecycle (ci_runs/ci_checks/ci_evidence/ci_recovery/ci_script_snapshot) is defensively engineered end to end: idempotency-keyed materialization with metadata equality checks, durable Check intents/outbox with CAS creation claims and heartbeat leases, terminal evidence gated on re-verified archive integrity, and leader-fenced recovery that never re-triggers started runs. The library federation stack (claims journal, two-phase import with compensation, immutable result archives, CAS-persisted links) is similarly rigorous, and PowerBI publish has per-test locking, drift detection, and a commit-marker runs row. External HTTP clients consistently carry timeouts, host allowlists, and bounded reads. The real defects cluster in the newest, least-exercised corner: the GitHub bulk sync service (github_sync_service.go), which silently ignores the pull-request strategy (direct-committing even under a RequirePullRequest policy), skips the drift-conflict check the single-script push enforces, and reports per-file failures only as bare counters; plus a genuine at-least-once duplication hole in the PowerBI row-push retry that defeats the documented publish_id dedupe.

_Boundary leads:_ pkg/api/telemetry.go:838 routeThresholdAlertNotifications consumes a threshold_alert episode when Dispatch (pkg/api/notifications.go:190-212) admits AT LEAST ONE webhook; other configured webhooks whose per-destination/global slots were saturated at that instant permanently miss the episode (no per-webhook retry). The comment documents saturation retry only for the all-dropped case — owner of telemetry.go should confirm partial-admission loss is acceptable. · pkg/api/ci_checks.go:221 reconcileTerminalCIGitHubChecks skips links whose test is missing from the in-memory runner map without touching them; storage-side ListIncompleteGitHubCheckLinks (pkg/storage/sqlite_github.go:761) JOINs the tests table so DB-deleted tests are excluded, but a test present in the DB yet not hydrated in runner memory would sit at the head of the oldest-first batch forever — worth a check by the owner of runner hydration.

**AREA-10** — raw 7, confirmed 7, refuted 0

The recording-replay pipeline is in genuinely strong shape and has clearly absorbed many prior hardening passes: the browser-recording manager has a complete stop watchdog / force-stop / late-artifact-adoption lifecycle that protects operator curation; the browser-replay manager is lease-fenced with abandon backstops, terminal-fence suppression, and careful release ordering; the headed recorder bounds every buffer (steps, bindings, thumbnails, WS frames, HAR size policing) and drains goroutines against deadlines; the proxy recorder has tunnel registries closed per session, header budgets, decompression bounds, and a collision-checked correlation rewrite pass; CA persistence is atomic with legacy-mirror repair. I found no data-loss, crash, or deadlock. The real defects are second-order: an O(n^2) per-event broadcast that embeds the full accumulated event list in every browser-replay progress message, a summary/list projection that never carries artifact-derived counts (lists and broadcasts always report 0 requests / no HAR), silent capture-cap truncation in the proxy recorder, a synchronous classic-replay endpoint that outlives the server's fixed 60s write deadline, and two small papercuts (unchecked marker body decode; scrubbed WS payloads used as replay match hints).

_Boundary leads:_ pkg/api/server.go:562 fixed 60s http.Server WriteTimeout co-owns the synchronous-replay truncation defect (finding on pkg/api/handlers_replay.go:172); whichever area owns server bootstrap should weigh a per-route deadline extension mechanism · pkg/api/websocket.go:226-235 hub drops broadcasts for slow consumers via buffered select; the full-session-in-every-event payload in browser_replay_manager.go may be compensating for that — trimming it (finding at browser_replay_manager.go:1112) needs a client resync story in dashboard/src/components/BrowserReplayProgress.tsx · pkg/scrub value-rewrite patterns determine how often scrubbed WebSocket payloads break browser_ws_wait match hints (finding at pkg/recorder/browser/convert.go:749); scrub-area owners can quantify the rewrite frequency · pkg/engine ReplayScript invokes OnProgress synchronously between actions, which is what turns the broadcast marshal cost at pkg/api/browser_replay_manager.go:1045-1070 into replay-path latency

**AREA-11** — raw 7, confirmed 6, refuted 1

This area is in unusually good shape for its size and difficulty. The live import path (ConvertJava -> combined java_vuser draft) has a genuinely solid conservation model: every mappable leaf statement, unparsed region, and external lifecycle method receives exactly one source-review disposition, TotalFunctions reconciles against the same javaLRCallPartsBound parse used for mapping emission, and every analysis budget (statements, methods, brace tokens, overload comparisons, scope snapshots) fails closed into an explicit promotion blocker rather than silently truncating. The occurrence-key identity model (scope key + structural path) correctly avoids line-number aliasing, the lexical masker (javaCodeOnly) handles text blocks/escapes/comments correctly, and the C transaction alias/macro scanner plus AST fallback marks everything review-only whenever the AST parse fails. The published lr/JMS/JDBC/HTTP coverage matrices honestly describe the review-evidence-only reality. The real weaknesses found are at the expression-parsing shortcuts (the angle-bracket generics heuristic in splitJavaTopLevel merges arguments containing comparison or shift operators, which silently weakens runtime reachability, transaction stats, and the promotion gate), in wasted heavy passes on the live path (the discarded classifyJavaArchive result triggers a complete second runtime-reachability build plus ~9 evidence-index rebuilds per import), and in a large retired native-lane subsystem (JMS/HTTP/JDBC/SOAP statement lanes plus backfillJavaConservation) that is reachable only from tests but still reads as if it protected production.

_Boundary leads:_ Approval flow when ReviewArtifact.Metadata lacks java_source_manifest: java_import.go:931-933 silently omits the key whenever BuildJavaVuserSourceManifest returns nil; verify the approval/promotion consumer (outside pkg/convert/enterprisearchive, likely pkg/api script-approval handlers) fails closed rather than approving without source hash binding. · Engine/runner consumption of the emits_business_transactions action metadata stamped at java_import.go:990-993: the impact of missed transaction detection (finding on splitJavaTopLevel) depends on whether the engine uses this flag to suppress the automatic iteration transaction; engine code is outside this area. · Zip entry-name normalization feeding BuildJavaVuserSourceManifest: entries spelled './Actions.java' or with backslashes would make CanonicalJavaVuserSourcePath (java_source_manifest.go:152-168) reject and null the whole manifest; ConvertZip in pkg/convert/enterprisearchive/zip.go owns whether such spellings can reach the file map.

**AREA-12** — raw 9, confirmed 9, refuted 0

The enterprisearchive core is genuinely mature in its honesty machinery: protocol detection gates metadata/content conflicts to review-only, runtime-profile and run-logic selection fail closed on ambiguous pointers, every cfg/usp key receives an explicit disposition (runtime_disposition.go), case-fold conflict auditing is thorough, review placeholders are disclosure-safe, and C parse failures become durable execution gates. The defects cluster in two seams that the honesty machinery does not cover: (1) the post-loop config replacement in Convert (opts.HTTPConfig assignment plus mergeScriptConfig's wholesale sub-struct replacement) silently destroys script-derived config the conversion loop already placed — web_set_user basic auth and web_global_verification content checks vanish on any archive that carries a stock default.cfg, while the source-review still reports them 'converted' (both empirically confirmed); and (2) end-of-stream and map-iteration gaps — trailing web_reg_* registrations are silently dropped with overcounted stats and no review row, and ITEMDATA form bodies lose duplicated field names and have nondeterministic field order (both empirically confirmed). Everything else found is edge-case-grade. Tests are extensive but consistently exercise Convert() with empty options or minimal cfgs, which is exactly why the cfg-present wipe path escaped.

_Boundary leads:_ pkg/convert/enterprisearchive/zip.go:186 dereferences trueClientResult.Script.Config without a nil check; verify pkg/convert/trueclient.ConvertArchive can never return (result with nil Script, nil error) or this panics on TrueClient archives · pkg/convert/enterprisearchive/converter.go:906 pruneUnusedWholeBodyExtractors only walks top-level action Extractors, so whole-body extractors serialized into http_sequence children (grouping.go:201-205) are never pruned; whether that busts the engine's bounded extraction budget is owned by the engine area

**AREA-13** — raw 5, confirmed 5, refuted 0

This area (bruno native/canonical importer, shared requestimport pipeline, credentialpolicy projectors) is in unusually good health for its complexity. The code is defensive and deterministic throughout: byte-offset rewriting preserves non-sensitive wire bytes exactly (JSON/XML/multipart/form projectors never re-serialize untouched content), duplicate headers and authored casing are preserved via HeaderList with the map demoted to a compatibility summary, conservation of requests is enforced by fail-closed errors rather than silent drops, every lossy decision is surfaced as an acknowledgement-gated diagnostic, and requirement/candidate identities are pinned by golden tests. Credential over-classification is deliberately conservative but functionally non-destructive because source values are retained in protected requirements for migration, and the executableSchemeCredentialHeader / TrustedWholeValues / ProvenVars carve-outs prevent the classic 'governing a correlated token destroys the auth flow' failure. The genuinely weak spots are: (1) a query-index mismatch between candidate-time URL rewriting and authored-file scrub targets that can corrupt the sanitized source bundle, (2) heavy repeated YAML parsing that makes large native inspections needlessly expensive (the whole candidate pipeline runs twice and folder layers are re-parsed per request), and (3) a parameter-removal pass in the protected reconcile that does not check for remaining non-credential references.

_Boundary leads:_ pkg/dslwalk bounded-traversal limit vs maximum importable script size: pkg/convert/requestimport/script_credential_projection.go:290-304 replaces the ENTIRE imported script with a review-only draft (all actions discarded, base URL reset) whenever dslwalk.Walk returns an error; if pkg/dslwalk's node/depth budget is below the largest valid enterprise import, a legitimate large script is destroyed at finalize. Verify the walker's limits in pkg/dslwalk against importer size limits. · pkg/protocol/http HeaderListIssuesForVersionWithUnresolvedTemplates index contract: pkg/convert/requestimport/runtime_fidelity.go:197-214 assumes issue.Index addresses the occurrence slice passed in; a protocol-side change to report canonical-name indexes would silently omit the wrong header occurrences (the defensive name-fallback at runtime_fidelity.go:203-210 only partially mitigates).

**AREA-14** — raw 13, confirmed 13, refuted 0

This area is in genuinely strong shape overall. The OpenAPI, Postman, WSDL, Salesforce, and Grafana converters show consistent defensive engineering: bounded work budgets (Salesforce correlation budget, Postman variable-binding dependency limits, Grafana structural caps), cycle guards on every ref-resolution path, honest review-only dispositions instead of silent drops, and conservation guards (completeness stats plus explicit skipped lists). Standouts are the Salesforce analyzer (budgeted shape-aware correlation passes with collision-safety analysis and non-transitive-safe replacement ownership), the Grafana importer (strict variable policy, PromQL run-scope validator used at both import and query time, honest unit/panel dispositions), and the Postman pipeline (topologically resolved variable graph with fail-closed limits). The weak points are fidelity edges rather than structural rot: the WSDL doc-literal envelope ignores schema target namespaces and elementFormDefault, the RAML converter is far below its siblings' bar (no requestimport pipeline, raw URI templates, blanket 200 assertions), and a handful of correctness papercuts (OpenAPI wildcard '2XX' status keys, Event Hubs capture empty-body assertions, AsyncAPI replay-order comparator).

_Boundary leads:_ pkg/api/handlers_web_api_imports.go:675 — mixed-protocol AsyncAPI import: the response-level Preflight is built from result.Script (the first script, always Kafka) only, while per-script preflight is attached at lines 676-678; the dashboard (dashboard/src/components/ProtocolAssetImport.tsx ~365) prefers result.preflight over the per-script metadata, so the JMS tab of a mixed Kafka+JMS import shows the Kafka script's preflight. Fix belongs in pkg/api / dashboard, not pkg/convert/asyncapi. · pkg/convert/har/converter.go:89-113 — the actual HAR-to-script conversion and correlation detection live in pkg/convert/sample (outside AREA-14); the HAR governance wrapper's claim that scrubbed responses carry no raw secrets depends on sample.Correlation.Value remaining json-omitted and on scrub.Value coverage in that package.

**AREA-15** — raw 13, confirmed 13, refuted 0

The six importers are at very different maturity levels. k6 and recorderjson are genuinely solid: k6's residual-source net (residual_review.go) plus per-request review gates make its constrained-subset claim honest, and recorderjson tracks a disposition (mapped/degraded/skipped) for every Chrome Recorder step with counted skips and warnings. TruClient's step conservation (ReviewOnlySteps, Catch Error body preservation) reflects the earlier hardening pass and holds up, but its run-sequence reconstruction double-imports function bodies called from inside other actions. The JMeter converter is the weakest relative to its ambition: its completeness guard only counts samplers, so non-sampler elements in sampler scope (ResponseAssertion, timers) and CSV configs inside controllers are dropped with zero diagnostics, the enabled="false" attribute is never read anywhere, and CSV EOF/sharing semantics are not modeled. DevWeb converts a useful subset honestly but scrambles WebSocket/gRPC ordering (all steps pinned to the block start line and grouped by verb) and collapses all load.thinkTime calls onto one step.

_Boundary leads:_ pkg/convert/devweb/converter.go:270-276 comment admits the zip-metadata path (zipmeta.go) has 'pre-existing parameter/data-source defects' that FinalizeScript would surface as hard failures; zipmeta.go was not reviewed here and deserves its own pass. · pkg/protocol/http/capabilities.go:516-558 + http.go:1044-1050: authored action HeaderList duplicates are all sent via Header.Add with singleton checks only for Host/User-Agent; any importer that models a header override as an appended duplicate double-sends it. Transport-side half of the JMeter sampler-HeaderManager finding. · pkg/engine/replay.go:395-403 applies script.Config.ThinkTime globally to every action; any importer that invents a nonzero default (k6 and devweb both hardcode 1000ms) silently reshapes load. A cross-importer convention (default 0 unless source had pacing) would fix this class once. · pkg/convert/jmeter side of multi-plan imports: ConvertJMX takes a single []byte and makes no multi-file assumptions; the multi-.jmx ZIP silent-drop is entirely in the pkg/api handler (owned by another area).

**AREA-16** — raw 6, confirmed 6, refuted 0

Engine core is in genuinely good shape in its classic hot paths: the execution-goal reservation protocol (reserve/commit/release with pending-suppressed cancellation) is carefully reasoned and consistent across the iteration loop and lifecycle blocks; pause/active-clock plumbing (SleepActive/ActiveElapsed/pauseCompensation) correctly excludes paused time from pacing and the run deadline; open-workload arrival scheduling has explicit bounded catch-up and in-flight shedding; teardown is deadline-bounded and slot-capped so stuck adapters cannot leak unbounded goroutines; metrics dimensions and snapshot memory are bounded. The concentrated risk is the new (post-2026-07-23) runtime credential-redaction subsystem: its fail-closed architecture is sound, but it over-collects exact-match values (every cookie value in the jar, auth usernames) with no minimum-length or sensitivity floor -- so short common values corrupt action/group/dependency labels and all retained evidence -- and it recompiles the full ~64-variant-per-value matcher two-to-three times per action per VUser with no change detection, a material hot-path CPU cost that grows toward pathological with rotating session tokens.

_Boundary leads:_ pkg/protocol/http/http.go:212-227 attaches a full cookie-jar value snapshot (RuntimeCredentialValues + RuntimeCredentialValuesSnapshot=true) to EVERY Execute result, even when no cookie changed -- this is the trigger frequency for the engine-side per-action matcher recompile (AREA protocol-http should consider a jar dirty-flag). · pkg/scrub (Value/Structured) runs on every retained metadata field per result via scrubSensitiveTextWithConfig (redaction.go:919); its bare-token/entropy heuristics deserve their own over-redaction/perf review in the scrub area. · TDigest has no internal synchronization; engine usage is single-goroutine (aggregate loop), but controller-side distributed Merge callers (outside pkg/engine) should be audited for concurrent Merge/Quantile on shared digests. · replay.go:390,417 fail the whole replay closed on refreshImportedCredentialRuntimeValues errors -- replay-area reviewer should check how a sticky 'runtime credential redaction capacity exceeded' error surfaces to users.

**AREA-17** — raw 10, confirmed 10, refuted 0

This area is in genuinely strong shape on resource discipline: the HTTP path has process-wide response-capture and cookie budgets, bounded buffer pools, bounded compiled-expression caches, per-action extractor/assertion work budgets, a memory-accounted per-VUser response cache with correct clone-on-store of pooled buffers, and race-conscious httptrace handling; websocket/grpc/network all set deadlines, bound message sizes, and cancel streams via deferred contexts, and the browser package rings/bounds console, network, HAR, and page state carefully. The real weaknesses are behavioral-consistency seams rather than raw resource bugs: the SOAP wrapper inspects result.ResponseBody after the HTTP layer has already discarded successful bodies under the default log config (silently disabling fault detection), the extractor on_miss contract that the DSL validates for every protocol is implemented three different ways (honored in HTTP, silently ignored in websocket/grpc, always-fail in network), the browser protocol accumulates CDP sessions on every window switch/popup activation without detaching prior ones, and SSE stream timeouts up to 1h are validated while the default 60s client timeout silently kills the stream first.

_Boundary leads:_ DiscardResponseBodyOnSuccess is computed in pkg/engine/vuser_resolve.go:221-223 from log config and applied to ALL protocols' actions; wrapper protocols (soap, and any future composite) need a way to see the body before discard. The right fix may be an engine-side contract change (discard after wrapper post-processing), not a soap-only patch. · pkg/protocol/database (out of my scope) uses the same jsonextract package and likely the same always-fail-on-miss extractor pattern as pkg/protocol/network/network.go:625-635 — the on_miss inconsistency finding probably extends there. · The extractor on_miss/mode contract is validated protocol-agnostically in pkg/dsl/validate.go:757-780; either the validator should reject on_miss/mode for protocols that do not implement them, or the protocols should implement them — decision spans dsl + protocol packages. · Browser 'ps fork per action' (runtime metrics process snapshot) remains open per PERF_REVIEW_2026-07-06; not re-reported.

**AREA-18** — raw 7, confirmed 7, refuted 0

This stack is in far better shape than its defect history suggests. The new pkg/javarunner and pkg/javasidecar packages (post-7/23, previously unreviewed) are exceptionally carefully engineered: strong process containment with terminal quarantine leases (process.go, process_linux.go cgroup.kill + pidfd identities), HMAC frame-token request/response correlation for persistent sessions, owner-generation fencing with reap-before-ready takeover, bounded captures everywhere, and content-hash-verified classpath materialization with atomic rename publish. The notorious Java Vuser lifecycle classes (false success on nonzero exit, teardown hangs, wedged RPC correlation) are genuinely closed on both local and sidecar paths — envelope success is authoritative, timeouts kill-and-poison rather than wedge, and ambiguous transport outcomes fail closed. JMS/Kafka/EventHubs adapters are resource-bounded with deliberate rebalance fencing (franz ownership generations, discard-on-assign/revoke) and I verified the Commit-under-assignmentMu pattern against franz-go v1.21.5 internals: no deadlock cycle exists (callbacks run outside the join&sync commit-block window). What remains are moderate correctness gaps: the real Kafka adapter silently ignores explicit partition targeting that the mock honors, sidecar batch validation misreports budget exhaustion as invalid targets, and a handful of diagnostics/telemetry papercuts.

_Boundary leads:_ pkg/protocol/database/database.go:247 BeginTx binds the open transaction to the ctx passed into Execute; today the engine (pkg/engine/vuser_execute.go:765) passes a vuser-lifetime context so this is safe, but any future engine change to per-action contexts with cancel would silently roll back every db_begin transaction after the action returns. The contract is implicit and untested on the engine side. · pkg/javasidecar/server.go relies on the controller/generator explicitly closing poisoned session tombstones (unusableSessions, 15min TTL); if any engine-side vuser recycle path reuses a session ID without CloseSession within the TTL it gets session_unusable — worth confirming the engine's vuser-restart path calls Teardown (which does CloseSession) in all abort flows (engine scope, not verified here).

**AREA-19** — raw 6, confirmed 6, refuted 0

The generatorrpc data plane is genuinely well engineered: execution-epoch fencing is backed by durable state written with tmp-file + fsync + rename + directory-sync and an owner-bound restart revalidation path; assignment staging/commit is a real two-phase protocol with rollback, stage TTLs, and a single lifecycle transition table; stop handles staged-vs-running groups, detached drains, bounded fence worker pools, and archive-finalizer tracking; archive artifact streaming validates offset/size/eof on every chunk; archive writes are quota- and free-space-bounded with sticky failure escalation. The defects I found are concentrated in metrics semantics rather than lifecycle correctness: the no-new-evidence 'lastMetrics' replay fabricates sustained TPS during stalls/pauses and writes phantom interval-summary rows into the archive; archived interval summaries persist only scalar percentiles even though a merge-capable t-digest is in hand at record time (the generator side of the confirmed wrong-fleet-percentile merge); per-group publishers each record a full cross-group interval row so multi-group pods duplicate window counts; and mergeGeneratorStats sums process-wide gauges (CPU%, memory, heap, threads, FDs, GC) across co-located groups, over-reporting generator resources by the group count.

_Boundary leads:_ pkg/api archive merge (parallel review): generator archives DO carry merge-capable fleet t-digests in manifest TerminalMetrics and in every live MetricsBatch (MetricAggregate.duration_digest), but summaries.jsonl/parquet rows only carry scalar P50/P90/P95/P99 — the controller-side fleet-percentile merge should either consume digests (requires generator schema addition, see finding) or stop presenting concatenated scalar rows as fleet percentiles. · pkg/api live-view ingestion cannot distinguish a replayed no-new-evidence batch from fresh traffic: MetricsBatch has no 'replayed'/'no-evidence' marker, so buildLiveMetricsFromDistributedBatch (pkg/api/distributed_metrics.go) renders phantom sustained TPS during a full SUT stall. Fix needs a proto marker plus controller handling. · Interval-summary Timestamp is generator-local time.Now() (service.go:1976 via recordMetricsBatch); cross-generator archive alignment by these timestamps inherits node clock skew. Heartbeat carries both controller and generator timestamps (HeartbeatPing/Pong.timestamp_unix_ms) so an offset estimate is available but unused — controller-side merge concern, generator ships no offset metadata. · Coordinated start barrier semantics are shared with pkg/api distributed_execution_backend.go: the controller picks start_at_unix_nano lead time; generator-side rejection windows (elapsed / beyond stage TTL) are evaluated against the generator's local clock (execution_control.go:365-374), so the controller's lead-time choice must budget for fleet clock skew.

**AREA-20** — raw 4, confirmed 4, refuted 0

This slice of pkg/storage is unusually disciplined for a hand-rolled SQL layer: every mutating path begins a transaction, locks parent rows in a documented deterministic order (project -> application -> release -> child) to avoid Postgres deadlocks, uses CAS-on-CAST(updated_at AS TEXT) fences for optimistic concurrency, and the newer repair passes (blank-script-ID backfill, script-recording binding/projection/sidecar/status convergence loops) are byte-bounded, cursor-paged, and CAS-guarded against concurrent writers. Scan lists match column lists on the wide tests/recordings tables, typed-nil boxing is defused by explicit "null"/"{}" checks (marshalNullableJSON, decodeStoredScriptJSON optional fields), and schema migrations are idempotent. The real weakness is deletion-graph completeness versus the FK graph: newer tables (direct_import_receipts, powerbi_publishes) and the schedule-occurrence/last-run test pointers were never folded into the per-test and per-project delete graphs, so foreign keys (enforced on both SQLite via _pragma=foreign_keys(ON) and Postgres via the derived FK pass) now abort two headline delete workflows, and one table leaks orphans forever.

_Boundary leads:_ pkg/api/tests.go DeleteTestWithError surfaces the storage FK failure as a raw 500 'failed to delete test: FOREIGN KEY constraint failed' with the archive-commit fence held; once the storage graph is fixed, no API change is needed, but the error mapping there hides the actionable cause today. · Completed direct_import_receipts rows (pkg/storage/direct_import_receipts.go) are retained forever by design for idempotency but have no retention/pruning path at all; whichever area owns import lifecycle should decide a TTL, since they also block project deletion (reported here) and grow unboundedly. · pkg/storage/sqlite_schedules.go schedule/occurrence deletion paths were not verified to clear scenario_schedule_occurrences.test_id before deleting their parent schedule; the schedules-area reviewer should confirm DeleteScenarioSchedule does not hit the same tests(id) FK interaction in reverse. · replay_session_leases carries project_id with no FK and is not touched by deleteProjectGraphTx; rows are TTL-expired so this is likely benign, but the replay-session area should confirm the expiry sweep always runs on followers too.

**AREA-21** — raw 6, confirmed 4, refuted 1

This area is in genuinely strong shape. Dialect discipline is consistently applied: preciseDatabaseTime/Context guards strftime vs clock_timestamp, correlationRulesetJSONByteLengthSQL guards OCTET_LENGTH vs LENGTH(CAST AS BLOB), FOR UPDATE is appended only on Postgres with SQLite relying on write-reservation no-op updates, PRAGMA-based repair paths early-return on non-SQLite, and postgres_foreign_keys.go derives the FK graph programmatically from the actual schema text (verified: every REFERENCES clause in sqlite_schema.go is single-line single-column, so the line parser is sound). CAS is pervasive and correct — revision fences on occurrences/schedules, generation fences on finalization jobs and replay leases, predicate-fenced deletes on external-result evidence, and the datapools rows_revision/row_count fence is maintained self-correctingly by every mutation path I read. A sweep of all 43 scope files found zero unguarded SQLite-only constructs and zero nested-query-while-rows-open patterns (the single-connection deadlock class). The one real defect found is a deletion-cascade completeness gap: direct_import_receipts carries a projects FK but is missing from the hand-enumerated project-deletion cascade, permanently blocking project deletion once a direct import has run; powerbi_publishes has the inverse problem (no FK, no cleanup, orphans forever).

_Boundary leads:_ pkg/api/direct_import_idempotency.go:99 is the live caller of ClaimDirectImportReceipt; whoever fixes the project-deletion cascade gap should also confirm the API layer surfaces a sane error today when project deletion fails mid-transaction (currently the FK violation bubbles up as a raw 500-class storage error). · powerbi_publishes rows reference test_id/project_id with no FK and no cleanup; the Power BI publish feature (pkg/api powerbi routes) may also want a retention policy for publish history independent of test deletion. · sqlite_projects.go maintains the project-deletion cascade as a hand-enumerated list; a schema-driven completeness test (diff tables carrying project_id REFERENCES projects(id) against the cascade list) lives naturally next to postgres_foreign_keys_test.go and would have caught the direct_import_receipts gap automatically.

**AREA-22** — raw 5, confirmed 5, refuted 0

This area is in unusually strong shape: the July hardening waves are visible everywhere (deterministic work budgets with fail-closed exhaustion in paramize/correlate/scrub/quoted_values, escape-preserving nested-URL rewrites, idempotent redaction sentinels, claim-token-fenced session lifecycles in importsession/importartifact/secretstore, and a genuinely careful disclosure projector). keywrap/refkey round-trips, uniquify's case-folded dedup, srcparse's ambiguity rejection, and importreview's gate normalization are all correct as far as I could push them. The real defects live at the seams the hardening missed: paramize's secret form-field regex never received the quote-exclusion + JSON-body-branch hardening that the analogous pkg/scrub pattern got in July (so it can destroy JSON bodies), paramize's data-param SearchText has no left boundary while apply is a global substring replacer, scrub's entropy heuristic wipes base64-encoded HAR response content while leaving the encoding flag in place, and tmpl silently translates JMeter counters to variables nothing defines.

_Boundary leads:_ pkg/convert/uniquify returns Rename{OldName,NewName} for side-data reconciliation, but when two actions carried the *identical* name the first occurrence keeps OldName while the second is renamed; any caller (e.g. enterprise-archive source-review TargetName reconciliation) that rewrites side data by matching OldName will also retarget records belonging to the kept first action. The rename list cannot disambiguate; callers need positional identity. Primary cause is in the callers under pkg/convert/enterprisearchive, outside this area. · pkg/convert/har/converter.go:73 runs scrub.HAR over the uploaded document before conversion, so the base64-content destruction described in the scrub finding also degrades the HAR import/conversion input path itself (response bodies become [REDACTED] before candidate discovery); worth checking whether the har converter should convert from raw and only persist scrubbed. · pkg/convert/sample/correlate_redact.go (lines ~605, 641, 729, 764) is the automatic blast radius for the paramize SanitizeBodySecrets JSON-corruption finding; once the regex is fixed, previously-sanitized stored artifacts produced through that path may already contain truncated bodies.

**AREA-23** — raw 9, confirmed 9, refuted 0

This area is in much better shape than 'never deeply reviewed' suggested. The CI gate path (cmd/apexctl/ci.go) is exceptionally hardened: bounded retries with jittered backoff and a retry window, distinct exit codes per outcome, evidence persisted before remote aborts, and abort confirmation loops with timeouts. pkg/kubeops (lease-fenced EndpointSlice publishing with JSON-patch resourceVersion tests), pkg/internal (zipbounds central-directory preflight, subprocessguard env allowlisting, diskspace overflow clamping), pkg/perfevidence (self-recomputing statistics, explicit no-target-attainment claim policy), and the controller/generator mains (coordinated shutdown, archive-safe drain, fail-closed transport checks) are all genuinely solid. The MCP Python server has zero path drift against the live route registry (verified programmatically), and every helm template .Values reference exists in values.yaml (also verified programmatically). The real defects cluster in two places: the older hand-rolled flag parsing in the non-ci apexctl subcommands (silent flag drops that can disable an SLA gate, an unbounded poll loop, a brittle wait loop), and helm packaging seams (import-only Bruno artifacts placed on an emptyDir while the required PVC mounts at an unused path, a Node major-version gap between CI and the production Dockerfile, a namespace-hardcoded generator DNS default).

_Boundary leads:_ dashboard/package.json has no engines field and no packageManager pin, which is why the Node 22-vs-26 CI/Dockerfile divergence is silent; adding an engines pin is a dashboard-area change (evidence: Dockerfile:3 uses node:26.5.0-alpine, .github/workflows/ci.yml:177/439/549 use node-version 22.22.0). · pkg/api importAssetDir fallback: when APEXLOAD_BRUNO_ARTIFACT_ROOT is empty the encrypted Bruno artifact store defaults to filepath.Join(s.importAssetDir, ".bruno-artifacts") (pkg/api/handlers_bruno_import.go:2949-2951); in the kustomize onprem deployment the data dirs are emptyDir, so any storage-area review should confirm import-enabled deployments never silently place durable encrypted artifacts on pod-local scratch. · Managed-test status vocabulary is duplicated across at least pkg/api/execution_backend.go:556, pkg/api/persistence.go:653, pkg/api/capacity.go:75, and cmd/apexctl/api_client.go:409; today they agree (verified), but there is no shared constant or test enforcing it, so a new terminal status added in pkg/api would silently hang apexctl wait/run polling.

---
*Generated by a multi-agent adversarially-verified review (Claude, 2026-08-02). Non-security scope; security is covered by separate dedicated passes. Partial coverage — see the Coverage section.*