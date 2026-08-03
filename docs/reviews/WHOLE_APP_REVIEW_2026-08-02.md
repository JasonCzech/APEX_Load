# Whole-Application Review (Partial: Backend Control Plane & Importers) — 2026-08-02

**Branch:** main @ dc05008ab (clean tree, == origin/main) · **Scope achieved:** 14 of 38 planned areas (~250k of ~620k first-party LOC) — the pkg/api control plane (fully partitioned), the recording/replay pipeline, and the enterprise-archive + API-spec importer families. The run was descoped mid-flight due to session token limits; findings below cover only the listed areas.
**Method:** 14 area finders over a git-archive snapshot of HEAD; 99 raw findings → 99 after dedup → **98 confirmed** (82 defects + 16 opportunities) / 1 refuted. Lean adversarial verification (token-constrained): 1 independent verifier per claimed-Critical/High finding and per suspected regression, batched single-vote verification for Medium/Low and opportunities. Prior-findings ledger (316 entries from the July reviews) tags rediscoveries instead of double-reporting them.

## Remediation status

<!-- REMEDIATION: rewritten after the fix phase completes -->
_Not yet remediated. Critical/High fix phase pending user go-ahead (token budget)._

## Coverage — what this review did and did not examine

**Covered (finders + verification):** AREA-01 api-execution-core, AREA-02 api-scheduling-plans-capacity, AREA-03 api-results-archive-reporting, AREA-04 api-import-orchestration, AREA-05 api-distributed-fleet, AREA-06 api-scripts-authoring-dsl, AREA-07 api-tests-scenarios-datapools, AREA-08 api-platform-core, AREA-09 api-integrations-and-library, AREA-10 recording-replay-pipeline, AREA-11 convert-enterprisearchive-java, AREA-12 convert-enterprisearchive-core, AREA-13 convert-bruno-requestimport-credentialpolicy, AREA-14 convert-api-spec-and-datasource-importers.

**NOT covered** (descoped when the session hit token limits): perftool importers (jmeter/devweb/k6/trueclient), pkg/engine, pkg/protocol (all transports), java/messaging stack, pkg/distributed, pkg/storage (both areas), import-transform + data-protection libs, CLI/controller/deployments, the entire dashboard frontend (9 areas), and all 6 cross-cutting sweeps (concurrency, storage-dialect parity, generated-freshness, test-quality, docs-accuracy, dependency-upgrade regressions). A follow-up pass is needed for whole-app coverage; the finder area map is preserved in the session plan.

## Baseline — all green

Captured before any change, against the exact HEAD bytes (which equal `origin/main`, so CI's full `gotestsum -- -race -shuffle=on -count=1 ./...` run at this commit also applies):

- `go build ./...` — clean · `go vet ./...` — clean
- dashboard `npm run typecheck` — clean · `npm run lint` — 0 errors (8 pre-existing warnings)
- dashboard `npm test` (vitest) — **274 files / 2782 tests passed** in 27s
- Toolchains: Go 1.26.5, Node 24 (CI uses 26), JDK 26, Playwright chromium

## Verdict

**82 confirmed defects: 0 critical, 7 high, 43 medium, 32 low** (+16 verified opportunities) across the covered areas. 15 previously-reported-still-open rediscoveries; 17 suspected regressions of previously-fixed findings.

Headline themes:

1. **Silent data loss in the enterprise-archive import chain** — the largest cluster, and 3 of the 7 Highs. LoadRunner archives lose `web_set_user` basic auth whenever a runtime config is present (#6), lose `web_global_verification` content checks to a wholesale WebRuntime replacement in `config_merge.go` (#5), and drop repeated ITEMDATA form fields with nondeterministic ordering (#7). Around them: trailing registrations dropped with overcounted stats, zip basename-collision misresolution, unchecked scanner truncation, and the JMeter multi-`.jmx` ZIP silently importing only the first plan. This is exactly the "conservation of elements" defect class the review targeted.

2. **Distributed results integrity** — the fleet path degrades correctness at exactly the scale it exists for: the distributed archive merge concatenates per-generator interval summaries so report percentiles are computed over per-generator aggregates rather than merged data (#1), and `tolerate_generator_loss` fails to tolerate the one thing it names — a dead generator escalates to a whole-fleet stop (#2).

3. **O(n²) accumulated-state broadcasts** — debug-session progress re-serializes the entire accumulated session on every event (#3, tagged regression), and browser-replay progress embeds the full event list per broadcast; long sessions degrade quadratically until effectively wedged.

4. **Locks held across I/O, platform-wide stalls** — the repo's recurring theme resurfaces in new code: launch-window admission runs many storage queries under `tr.mu`, capacity fair-share sorting issues a SQL query per comparison under the same lock, remote-run handlers hold an RLock across cross-controller HTTP, and project deletion stalls every mutating request platform-wide.

5. **Integration write-safety** — bulk GitHub sync ignores the configured `pr` strategy and commits directly to the default branch (#4), and skips the drift check so it silently overwrites remote edits.

6. **Over-eager masking/classification destroying legitimate data** — `runtime_mask.go` treats any value containing `@` as a credential (breaking email-valued parameters), continuing the over-scrub lineage from the July reviews.

7. **Time/accounting correctness** — DST fall-back double-fires schedules in the repeated hour, usage accounting bills full configured duration for stopped runs, and conflict-state schedules are durably rewritten with a revision bump every 30 seconds.

## Suspected regressions of previously-fixed findings

_Caveat: the "regression" tag comes from a file-level match against the ledger of previously-fixed findings — it means "a confirmed defect in a file where a July review previously fixed something," not necessarily the same defect recurring. Check the verification evidence per finding._

- **#11 Gradual ramps persist a full test row (incl. every compiled script) per single-VUser step ** — `pkg/api/testexecution.go:2130`
- **#12 Usage accounting bills configured duration for every retained test, including runs that st** — `pkg/api/usage_accounting.go:245`
- **#52 broadcastEvent reads live ManagedTest fields after tr.mu is released, racing with locked w** — `pkg/api/testexecution.go:2662`
- **#13 Fair-share pending sort issues a reservation SQL query per sort comparison under tr.mu on ** — `pkg/api/capacity.go:556`
- **#53 Run-calendar truncation keeps an arbitrary append-order subset instead of the events neare** — `pkg/api/run_calendar.go:269`
- **#56 Single-slot global Bruno inspect semaphore holds its exclusive section across the request-** — `pkg/api/handlers_bruno_import.go:232`
- **#3 Every debug progress broadcast serializes the entire accumulated session, making replay co** — `pkg/api/script_debug_manager.go:1379`
- **#62 Replay-session start endpoint lacks the credential-preserving-import echo special case its** — `pkg/api/handlers_script_debug.go:181`
- **#63 Replay progress never refreshes session activity, so an actively-running or breakpoint-pau** — `pkg/api/script_debug_manager.go:664`
- **#26 Any value containing '@' is classified as a credential, breaking email-valued parameters a** — `pkg/api/runtime_mask.go:87`
- **#65 Data-pool update mixes PATCH and PUT semantics: archived_at is absent-preserving but omitt** — `pkg/api/handlers_data_pools.go:262`
- **#36 Every browser-replay progress broadcast embeds the full accumulated event list (O(n^2) CPU** — `pkg/api/browser_replay_manager.go:1112`
- **#37 Synchronous classic replay endpoint exceeds the server's fixed 60s WriteTimeout and loses ** — `pkg/api/handlers_replay.go:172`
- **#35 Browser-recording list summaries and WS broadcasts always report 0 network requests, 0 web** — `pkg/api/browser_recording_manager.go:1176`
- **#70 Marker handler swallows JSON body decode errors, producing misleading validation errors** — `pkg/api/handlers_browser_recorder.go:537`
- **#39 Live import pays for a discarded classification pass, including a duplicate full runtime-r** — `pkg/convert/enterprisearchive/java_import.go:165`
- **#45 Native inspection re-parses YAML O(requests x ancestors x passes) and runs the full candid** — `pkg/convert/bruno/inspect.go:167`

## Previously reported, still open

Rediscovered by this review; counted in the totals but tagged rather than re-reported as new:

- **#14 Conflict/failed-blocked schedules are durably rewritten with a revision bump on every 30s ** — `pkg/api/scheduler.go:2203`
- **#15 DST fall-back: schedules firing inside the repeated local hour fire twice, ~60 minutes apa** — `pkg/api/scheduler.go:589`
- **#57 java_vuser launch gating performs a fleet-metadata DB read and placement simulation under ** — `pkg/api/distributed_execution_backend.go:641`
- **#58 Startup-failure cleanup stops assigned generators sequentially, so a wide-fleet failure ca** — `pkg/api/distributed_execution_backend.go:736`
- **#28 Run manifest 'initial configuration' is read from the continuously rewritten durable recor** — `pkg/api/test_manifest.go:284`
- **#66 Manifest configuration maps StartedAt from the stored record's ScheduledStartAt** — `pkg/api/test_manifest.go:375`
- **#31 Failed requests permanently burn their Idempotency-Key for the TTL** — `pkg/api/middleware.go:307`
- **#38 Proxy recorder silently drops requests once caps are hit — no warning, counter, or event** — `pkg/recorder/recorder.go:1251`
- **#77 orderReplayOps comparator is not transitive, so produce-before-consume ordering can silent** — `pkg/convert/asyncapi/converter.go:657`
- **#O2 Schedule CRUD, approval resolution, and timeslot bind hold the global runner write lock ac** — `pkg/api/scheduler.go:2548`
- **#O4 Parallelize per-target script distribution and shard assignment during distributed startup** — `pkg/api/distributed_execution_backend.go:939`
- **#O7 User listing for scoped admins is an O(users x bindings) N+1 over the store** — `pkg/api/handlers_auth.go:1673`
- **#O9 Every browser-replay GET takes the manager write lock and runs a full prune sweep** — `pkg/api/browser_replay_manager.go:794`
- **#O10 Retire or quarantine the dead native Java lanes and their pipeline-reconstruction tests** — `pkg/convert/enterprisearchive/java_import.go:622`
- **#O14 matchHAR re-parses URLs O(refs x entries) times** — `pkg/convert/salesforce/analyzer.go:440`

## Refuted (do not fix)

- ~~BuildJavaVuserSourceManifest nulls the entire manifest silently on any non-canonical or ca~~ (`pkg/convert/enterprisearchive/java_source_manifest.go:44`, AREA-11) — Fail-closed: import_promotion_gating.go:147-151 blocks approval with an explicit missing-manifest blocker; no silent weakening.

# Confirmed findings

## Critical (0)

## High (7)

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


## Medium (43)

#### 8. Launch-window admission performs many storage queries while holding tr.mu
**Where:** `pkg/api/launch_preflight.go:564` · **Area:** AREA-01 · **Category:** performance-lock-contention · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

launchWindowConflictsLocked runs with tr.mu held (write lock on the StartTest/promotion/resume/scale path via evaluateStartGatesLocked at pkg/api/testexecution.go:578-589, read lock in PreflightLaunch at pkg/api/launch_preflight.go:102-168) and performs storage I/O inside the lock: launchCapacityEventsLocked calls tr.scenarioStore.ListAllScenarioScheduleOccurrences() (full-table read, launch_preflight.go:608), boundTimeslotBoundariesInWindowLocked does a range query (pkg/api/timeslot_reservation.go:739-756), and the per-sample loop calls launchReservationConflictAtLocked -> boundTimeslotsActiveAtLocked -> ListCapacityReservationsByTimeRange once per sample instant (launch_preflight.go:572-585, timeslot_reservation.go:712-731); sample count grows with overlapping calendar events (launchSampleTimes, launch_preflight.go:743-781). launchTimeslotRevalidationErrorLocked (timeslot_reservation.go:552-583) and ValidateScenarioLoadModel's data-pool reads under RLock (pkg/api/load_model_validation.go:36-78, 338-352) are further instances. This is the repo's documented 'locks held across DB I/O' defect class.

**Failure scenario:** Enterprise deployment on PostgreSQL (2-5 ms per query RTT) with ~15 running/scheduled runs and a few capacity reservations: a StartTest evaluates ~2+2x15 sample instants, i.e. ~34 sequential DB queries plus the occurrence table scan, holding tr.mu (write) for 100-200 ms. Every gradual-ramp step and every ResumeTest/AdjustDuration repeats the same sweep (vuserScaleProposalLocked -> runtimeAdjustmentWindowErrorLocked -> launchWindowConflictsLocked, pkg/api/testexecution.go:2065-2078, 2174-2195). During each sweep, all API reads (GetTest/ListTests use RLock) and all metric-publish locking stall; with two concurrent ramps the dashboard visibly freezes in bursts for the whole ramp duration. A slow/failing PG node stretches each hold to seconds.

**Suggested fix:** Snapshot the needed durable inputs (occurrences, reservations in window) once per admission attempt BEFORE taking tr.mu, pass them into the locked evaluation as values; keep only the in-memory event assembly under the lock. Cache ListCapacityReservationsByTimeRange for the whole window and filter per-sample in memory instead of re-querying per sample.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V001 — CONFIRMED (medium)

launchWindowConflictsLocked (pkg/api/launch_preflight.go:545-587) runs under tr.mu: write lock via beginScriptShakeoutIfNeeded (pkg/api/testexecution.go:647-648) -> preflightStartBlocksLockedWithExpectedTerm:639 -> evaluateStartGatesLocked:579 -> launchStartWindowErrorLocked (launch_preflight.go:1103-1113); read lock in PreflightLaunch (launch_preflight.go:102-168). Inside the lock: launchCapacityEventsLocked calls scenarioStore.ListAllScenarioScheduleOccurrences() (launch_preflight.go:608); boundTimeslotBoundariesInWindowLocked issues a range query (timeslot_reservation.go:743); the per-sample loop (launch_preflight.go:572-574) calls launchCapacityConflictsAtLocked:837 -> launchReservationConflictAtLocked:844 -> boundTimeslotsActiveAtLocked -> ListCapacityReservationsByTimeRange (timeslot_reservation.go:718) once per sample; sample count grows with events/reservations (launchSampleTimes:743-781). Resume/adjust/ramp repeat via vuserScaleProposalLocked:2076 -> runtimeAdjustmentWindowErrorLocked:2190. Causal chain holds; severity medium appropriate.

</details>


#### 9. Local goal controller uses a stale groupVUsers map, misreporting current VUsers and silent
**Where:** `pkg/api/metrics.go:796` · **Area:** AREA-01 · **Category:** correctness-state-divergence · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

broadcastMetrics captures groupVUsers once when the run enters RUNNING (pkg/api/metrics.go:796-801) and thereafter the map is updated ONLY by the goal controller's own successful scales (pkg/api/testexecution.go:3062). An operator ScaleVUsers call updates test.Groups and the engine but never this map, so evaluateGoalControllerLocked computes currentTotalVUsers from stale values (metrics.go:891) and applyGoalControllerUpdateWithScaler both (a) plans absolute targets from the stale distribution and skips groups whose stale value equals the computed target (testexecution.go:3036-3037), and (b) writes the stale values back into test.Groups[i].VUsers for every group in the map after any scale round (testexecution.go:3072-3076), making the durable record diverge from the engine's actual VUser count. The distributed path already rebuilds a fresh map via liveGroupVUsers (pkg/api/distributed_stream.go:1050, helper at pkg/api/goal_controller.go:405-420); the local ticker does not.

**Failure scenario:** Local goal_oriented run with groups A(50), B(50), C(50). Operator scales C to 80 via POST /tests/{id}/scale (permitted while RUNNING; no scenario-mode gate in vuserScaleProposalLocked). Engine now runs 180 VUsers; test.Groups says 80. On the next TPS shortfall the controller computes currentTotal=150 from the stale map, distributes new targets; C's target comes out equal to its stale 50 so the loop skips scaling C, then the write-back loop sets test.Groups[C].VUsers=50 while the engine still runs 80. Durable record, TotalVUsers-based capacity accounting, and a restart-rehydrated copy now disagree with the actual fleet by 30 VUsers, and GoalStatus.CurrentTotalVUsers shows 150 while 180 run. If C's target differs from 50, the controller instead silently reverts the operator's 80 back toward the stale-based distribution.

**Suggested fix:** In broadcastMetrics, rebuild the map each tick with liveGroupVUsers(test, live) (as the distributed consumer does) before evaluateGoalControllerLocked, and drop the unconditional write-back loop in applyGoalControllerUpdateWithScaler for groups not scaled in this round (or read current values from test.Groups under the lock instead of the closure map).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V003 — CONFIRMED (medium)

broadcastMetrics captures groupVUsers once from test.Groups at run start (pkg/api/metrics.go:796-801) and the ticker loop (814-839) never rebuilds it. Only controller scales update it (pkg/api/testexecution.go:3062). Operator scale is permitted on goal runs — vuserScaleProposalLocked (testexecution.go:2011-2079) has no ScenarioMode gate — and writes test.Groups[groupIndex] durably (reserveVUserScaleLocked:2123-2130) without touching the closure map. Next goal round: evaluateGoalControllerLocked computes currentTotalVUsers from the stale map (metrics.go:891); applyGoalControllerUpdateWithScaler skips groups whose stale value equals target (testexecution.go:3037) and writes stale values back into test.Groups for every mapped group (3072-3076), reverting the operator's value; GoalStatus totals also stale (3088). liveGroupVUsers exists (goal_controller.go:405-420) and is used only by distributed_stream.go:1050. Confirmed medium.

</details>


#### 10. Boot hydrates full interval history for every stored test into memory with no aggregate bo
**Where:** `pkg/api/persistence.go:424` · **Area:** AREA-01 · **Category:** performance-memory · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

loadStoredTests iterates every durable test row (ListTests("", nil), pkg/api/persistence.go:402-428) and calls loadStoredTestHistory for each, which loads up to maxManagedTestHistory=3600 decoded LiveMetrics points capped at maxManagedTestHistoryBytes=64 MiB PER TEST (persistence.go:1003-1038, pkg/api/runner.go:1045-1046) and keeps them resident on the ManagedTest for the process lifetime. There is no global byte budget and no lazy loading, so controller RSS and boot time grow linearly with total retained run history across all runs (terminal runs included). The HA reload path repeats this for every newly-seen identity (persistence.go:504-511).

**Failure scenario:** An enterprise controller retains 300 completed runs averaging 1800 history points of ~5 KB each (rich Transactions/GroupMetrics maps): boot decodes and pins ~2.7 GB of history JSON-derived structures (plus Go overhead) before serving traffic, taking minutes of N+1 GetHistoryLimit queries; on a memory-limited pod (e.g. 2 GiB K8s limit) the controller OOM-kills in a crash loop after enough runs accumulate, with no configuration to bound it short of deleting runs.

**Suggested fix:** Hydrate history lazily on first read for terminal runs (keep only the latest point for LiveMetrics display at boot), or add a global history byte budget that evicts oldest terminal-run histories LRU-style; the archive already holds the durable copy.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V005 — CONFIRMED (medium)

loadStoredTests (pkg/api/persistence.go:402-428) iterates every row from ListTests("", nil) and calls tr.loadStoredTestHistory(managedTest) at line 424 for every stored test regardless of status; history stays resident on the ManagedTest in tr.tests (line 426) for process lifetime. loadStoredTestHistory (persistence.go:1003-1038) loads up to maxManagedTestHistory=3600 points per test (one GetHistoryLimit query per test — N+1), bounded only per-test by maxManagedTestHistoryBytes=64 MiB (pkg/api/runner.go:1045-1046; trimming in appendManagedTestHistoryWithLimits/trimManagedTestHistoryToLimits, pkg/api/metrics.go:546-594 — per-test accounting only). Grep found no global byte budget, no lazy load, and no retention pruning of stored tests. HA reload repeats hydration for new identities (persistence.go:504-505). Aggregate memory grows linearly with retained runs; medium stands.

</details>


#### 11. Gradual ramps persist a full test row (incl. every compiled script) per single-VUser step 
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


#### 12. Usage accounting bills configured duration for every retained test, including runs that st
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


#### 13. Fair-share pending sort issues a reservation SQL query per sort comparison under tr.mu on 
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


#### 14. Conflict/failed-blocked schedules are durably rewritten with a revision bump on every 30s 
**Where:** `pkg/api/scheduler.go:2203` · **Area:** AREA-02 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

reconcileScenarioScheduleOccurrenceLocked's no-change early-return (pkg/api/scheduler.go:2203-2219) covers only reason codes approval_required/approval_rejected/approval_state_inconsistent. An occurrence blocked with schedule_conflict, timeslot_unavailable, environment_unavailable, or missed_run_requires_decision — or a failed occurrence — falls through to lines 2220-2276, which set LastOccurrence/UpdatedAt/LastError/NextRequiredAction and call saveScenarioScheduleForSweepLocked unconditionally. Because those branches never advance NextRunAt past the blocked plannedAt, the schedule stays due and this repeats on EVERY sweep (defaultScenarioSchedulerSweepInterval=30s, scheduler.go:81). UpdateScenarioScheduleIfRevision always writes revision=expected+1 (pkg/storage/sqlite_schedules.go:249-264), so the schedule row churns and its revision inflates indefinitely. The execution-plan side already has the missing idempotence guard for its equivalent holds (pkg/api/execution_plan_scheduler.go:1035-1038 and 799-802), proving the scenario-side omission is an oversight, not a design choice.

**Failure scenario:** A schedule references an archived release → sweep records a blocked schedule_conflict occurrence. Left over a weekend, the schedule row is rewritten ~2,880 times/day with revision +1 per sweep (~8.6k revisions, plus constant UpdatedAt churn making 'last modified' meaningless). When the operator opens the schedule to fix it, the PUT requires If-Match/expected_revision (pkg/api/schedule_preconditions.go:11-43, enforced at pkg/api/handlers_scenarios.go:278); any read→edit→submit cycle that spans a 30s sweep boundary gets 409 'changed concurrently' — the exact operator trying to unblock the schedule is repeatedly rejected by the scheduler's own no-op rewrites.

**Suggested fix:** Extend the 2203 guard to every blocked/failed reason: when schedule.LastOccurrence matches the current occurrence (ID+Revision) AND LastError/NextRequiredAction already reflect the mapped values, return handled without saving — mirroring the exec-plan approval-hold guard.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V009 — CONFIRMED (medium)

Sweep runs every 30s (pkg/api/scheduler.go:81,150) and processes any schedule with NextRunAt<=now (scheduler.go:346-350) via runScenarioScheduleLocked (399) -> reconcileScenarioScheduleOccurrenceLocked (1620). The no-change guard (2203-2218) covers only approval_required/approval_rejected/approval_state_inconsistent; blocked schedule_conflict/environment_unavailable/missed_run_requires_decision and failed occurrences fall through (2220-2276): UpdatedAt=now, LastError/NextRequiredAction set, NextRunAt never advanced, then saveScenarioScheduleForSweepLocked (2276) -> UpdateScenarioScheduleIfRevision, which always writes revision=expected+1 (pkg/storage/sqlite_schedules.go:249-264). Schedule stays due, so this repeats indefinitely. PUT edits require If-Match/expected_revision (pkg/api/schedule_preconditions.go:11-43; handlers_scenarios.go:278) so mid-edit sweeps 409 the operator. Exec-plan sibling has the guard (execution_plan_scheduler.go:799-802,1034-1038). Prior-report 78-81 are unrelated perf items (minute-scan/LoadLocation), still open — no regression.

</details>


#### 15. DST fall-back: schedules firing inside the repeated local hour fire twice, ~60 minutes apa
**Where:** `pkg/api/scheduler.go:589` · **Area:** AREA-02 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

nextCronRun matches cron fields against local wall time and returns the earliest matching UTC instant (pkg/api/scheduler.go:589-624). On a DST fall-back day both UTC instants of the repeated local hour satisfy the same cron fields. After the first materialization the schedule advances via nextScenarioScheduleRun(schedule, now) (scheduler.go:1797-1815; reconcile path 2231-2242) or nextCronRun(.., advanceFrom=plannedAt) for execution-plan schedules (pkg/api/execution_plan_scheduler.go:1098-1110), and the search — being strictly-after — lands on the SECOND instant of the same local time one hour later. Occurrence dedup keys on (scheduleID, plannedAt) via GetScenarioScheduleOccurrenceByScheduleAndPlannedAt, and the two fires have different plannedAt values, so nothing dedups them. The comment at scheduler.go:592-595 ('during fall-back the earliest matching UTC instant wins') describes only a single evaluation; the test suite pins only that first instant (pkg/api/runner_test.go:6052-6059) and never exercises the advance across the transition.

**Failure scenario:** Schedule '30 1 * * *' timezone America/Chicago on 2026-11-01 (fall-back at 02:00 CDT = 07:00 UTC): sweep materializes the run at 06:30 UTC (01:30 CDT), advance-from-now finds 07:30 UTC (01:30 CST) matching the same fields → NextRunAt=07:30 UTC → a second, duplicate nightly run fires one hour after the first and overlaps it, double-consuming quota/reserved capacity and producing two occurrence/run records for one scheduled day. Applies equally to execution-plan schedules through the shared evaluator.

**Suggested fix:** In nextCronRun (or in the advance callers), reject a candidate whose local wall-clock time AND local calendar date equal the previous fire's — i.e., for non-wildcard hour fields, skip the second occurrence of a repeated local time. Alternatively advance from plannedAt converted to local, +1 local minute, re-converted after the transition.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V010 — CONFIRMED (medium)

nextCronRun (pkg/api/scheduler.go:589-624) iterates UTC minutes but matches cron fields against local wall time with no repeated-hour guard; on fall-back both UTC instants of the repeated local hour match. After the first fire the scenario path advances via nextScenarioScheduleRun(schedule, now) — a strictly-after search (scheduler.go:1797-1815, 554-561) — which lands on the second instant (e.g. cron "30 1" America/Chicago 2026-11-01: 06:30Z then 07:30Z, both local 01:30). Exec-plan path advances from plannedAt with the same result (execution_plan_scheduler.go:1101-1110). Occurrence dedup keys on (scheduleID, plannedAt) (scheduler.go:365-381,1965), and the two fires have different plannedAt, so a duplicate run materializes. Contradicts the code's own "earliest matching UTC instant wins" comment (592-594). Test pins only the first instant (runner_test.go:6052-6059); no advance-across-transition coverage. Prior-report 78-81 are perf items — different issue, no regression.

</details>


#### 16. GET /archive/manifest re-decodes and re-scrubs the entire transaction archive on every req
**Where:** `pkg/api/handlers_archive.go:45` · **Area:** AREA-03 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleGetRunArchiveManifest calls sanitizedRunArchiveManifest (pkg/api/handlers_archive.go:45), which builds a full sanitize plan: buildSanitizedRunArchivePlan runs writeSanitizedArchiveTransactions to io.Discard over the whole transactions.jsonl — JSON-decoding, scrubbing, and re-encoding every record — solely to compute the sanitized artifact's count/size/SHA (pkg/api/archive_export_sanitize.go:37-67, pkg/api/archive_transaction_sanitize.go:36-83). Only the raw manifest is cached (cachedRunArchiveManifest); the sanitize pass has no cache keyed on the manifest hash, so every manifest fetch repeats an O(archive-size) scan bounded only at 2M records/512MiB.

**Failure scenario:** A run archives 2M transactions (~500MB JSONL). Each time a viewer opens the run's archive view, GET /api/v1/tests/{id}/archive/manifest decodes+scrubs+re-encodes all 2M records (tens of seconds of CPU plus 500MB of file I/O) before returning a few KB of metadata; five concurrent viewers quintuple the load. The archive is immutable, so every pass after the first computes an identical result.

**Suggested fix:** Cache the sanitized stats/masked manifest keyed by runArchiveReportCacheKey (ManifestSHA256 + counts) in a small LRU alongside runArchiveManifestCache; invalidate through forgetRunArchiveCache.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V016 — CONFIRMED (medium)

Chain verified. `handleGetRunArchiveManifest` calls `sanitizedRunArchiveManifest` per request (pkg/api/handlers_archive.go:45), which always runs `buildSanitizedRunArchivePlan` → `writeSanitizedArchiveTransactions(ctx, record, manifest, io.Discard)` (pkg/api/archive_export_sanitize.go:37, 141-147) purely to compute sanitized count/size/SHA. That function JSON-decodes, scrubs, and re-encodes every transaction record (pkg/api/archive_transaction_sanitize.go:58-74), budgeted at 2M records / 512MiB (archive_loaders.go:29-30). No cache of the sanitize result exists: grep for "sanitiz" in pkg/api/archive_report_cache.go is empty; only the raw manifest is cached (`runArchiveManifestCache`, :169, 270-277). Archives are immutable, so every manifest GET repeats an identical O(archive-size) scan. Medium severity fits: heavy per-request CPU/IO on a metadata endpoint, no correctness impact.

</details>


#### 17. Every JSON report GET rebuilds full reports for all sibling runs in the release against a 
**Where:** `pkg/api/handlers_archive.go:528` · **Area:** AREA-03 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleGetRunArchiveReport's JSON path unconditionally calls attachReleaseTrend (pkg/api/handlers_archive.go:528), which runs collectReleaseTrendRuns → cachedRunArchiveReport for every archived run in the release (pkg/api/handlers_release_trend.go:39-56, 68-83). runArchiveReportCache holds only 16 entries / 128MiB (pkg/api/archive_report_cache.go:16-18), so a release with more than 16 archived runs thrashes the LRU: each report view re-streams multiple sibling archives end-to-end (buildRunArchiveReport scans up to 2M transactions per sibling), and all N cloned reports are held in memory for the trend computation. handleGetRunArchiveReportsBatch (:595-597) repeats this per batched ID.

**Failure scenario:** A release accumulates 30 archived runs of ~1M transactions each. Opening any one run's report (GET /tests/{id}/archive/report — the dashboard results data source) triggers ≥14 cache-miss full-archive report builds (~14M transaction decodes) before responding; every subsequent view of a different run in the release repeats the thrash. Report latency degrades from milliseconds to minutes and pins controller CPU/disk, even though the caller wanted one run plus a two-point trend.

**Suggested fix:** Persist or cache a compact per-run trend point (the ~10 scalar fields trendPointFromRun reads) keyed by manifest hash, or restrict attachReleaseTrend to the current run plus its immediate predecessor; alternatively size the report cache to release cardinality.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V017 — CONFIRMED (medium)

Chain verified. The JSON report path unconditionally calls `s.attachReleaseTrend(report)` (pkg/api/handlers_archive.go:528; comment at :525-526 says "unconditionally"), and the batch path repeats it per ID (:596). `attachReleaseTrend` → `collectReleaseTrendRuns` builds `cachedRunArchiveReport` for every archived run in the release (pkg/api/handlers_release_trend.go:39-56, 68-83); the code's own comment (:66-67) admits it "collects and rebuilds every archived report in the release". `runArchiveReportCache` holds 16 entries / 128MiB (pkg/api/archive_report_cache.go:15,17,168), so a release with >16 archived runs LRU-thrashes: each cache miss re-runs `buildRunArchiveReport`'s full-archive scan, and all N reports are held in `runs` during trend computation. Profile-gated variant `attachReleaseTrendForProfile` (:88-93) exists but is not used on the JSON path. Medium: severe latency/CPU on large releases, functionally correct.

</details>


#### 18. Application Load Comparison hard-fails when any run exceeds 500k transactions and holds ev
**Where:** `pkg/api/handlers_load_comparison.go:76` · **Area:** AREA-03 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

collectApplicationLoadComparisonRuns calls readAllArchiveTransactions for every archived release run of the application (pkg/api/handlers_load_comparison.go:76) and propagates its error (:77-79), and readAllArchiveRecordsContext returns a hard error at maxArchiveAnalysisRecords=500_000 records or 128MiB (pkg/api/archive_loaders.go:22-23, 749-751). All runs' decoded []TransactionEvent slices are retained simultaneously in the runs slice until buildLoadComparison finishes, even though the computation only needs per-interval aggregates.

**Failure scenario:** An application has one release-associated archived run of 600k transactions (≈170 TPS for 1 hour — routine for a load-testing platform). GET /api/v1/applications/{id}/load-comparison returns 500 'archive exceeds bounded analysis limit (500000 records or 134217728 bytes)' for the entire application, with no indication of which run is at fault; the Load Comparison view stays dead until the user guesses and excludes the offending run via test_id filters. Below the limit, 15 archived runs near the cap decode to multi-GB of resident TransactionEvent structs in a single request.

**Suggested fix:** Stream transactions per run with forEachArchiveTransactionContext (2M-record budget) feeding the interval accumulators directly, dropping full-slice retention; degrade per-run (skip + annotate) instead of failing the whole endpoint.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V015 — CONFIRMED (medium)

Chain verified. `collectApplicationLoadComparisonRuns` calls `readAllArchiveTransactions(record)` for every release-associated run (pkg/api/handlers_load_comparison.go:76) and returns the error (:77-80), which the handler converts to a 500 for the whole endpoint (:20-23). `readAllArchiveTransactions` → `readAllArchiveRecords` → `readAllArchiveRecordsContext` (pkg/api/archive_loaders.go:691-694, 720-724), which hard-errors at `maxArchiveAnalysisRecords=500_000` / `maxArchiveAnalysisBytes=128<<20` (:22-23, 749-751) with no per-run skip. Full decoded `[]TransactionEvent` slices for all runs are retained in `runs` (handlers_load_comparison.go:103-112) until `buildLoadComparison` returns (:25). One over-limit run kills the entire application's Load Comparison view; the error names no run. Severity medium is apt: feature-breaking at routine perf-test scale but confined to this endpoint.

</details>


#### 19. One active SLA waiver marks every release breach as waived — scope and creation time are i
**Where:** `pkg/api/release_sla_governance.go:233` · **Area:** AREA-03 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

buildReleaseSLAGovernance counts active waivers (pkg/api/release_sla_governance.go:163-168) and then, if any exist, sets Summary.WaivedBreaches = Summary.FailedRuns and leaves UnwaivedBreaches at 0 (:233-237), driving the release gate status to 'waived' instead of 'failed' (:238-251). The waiver's persisted Scope field (accepted from the API at :60-65, stored and returned) is never consulted, and breaches from runs completed after the waiver was created are equally treated as waived. Runs whose archive report is unreadable are conservatively counted as breached (:197-211) and are then also silently waived.

**Failure scenario:** An analyst creates a waiver for one historical breach (scope 'test-123', reason 'known infra blip'). A week later a new run in the same release breaches its SLA badly. GET /api/v1/releases/{id}/sla-governance reports status='waived', UnwaivedBreaches=0, and next action 'Review waiver expiration and audit trail before promotion' — the governance gate presents the new, never-reviewed breach as already risk-accepted, so the release can be promoted on stale paper.

**Suggested fix:** Evaluate waivers per breach: match Scope ('release' vs a specific test id) and require waiver.CreatedAt (or an explicit coverage window) to be at/after the breaching run's completion; count uncovered breaches as unwaived.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V018 — CONFIRMED (medium)

Chain verified. `buildReleaseSLAGovernance` counts waivers only by `Status == "active"` (pkg/api/release_sla_governance.go:163-168) — `Scope` and `CreatedAt` never inspected. Then the all-or-nothing assignment: `if activeWaivers > 0 { WaivedBreaches = FailedRuns }` else `UnwaivedBreaches = FailedRuns` (:233-237), so one active waiver zeroes UnwaivedBreaches and flips status to "waived" with next-action "Review waiver expiration..." (:245-247). `Scope` is accepted from the API (:87-90, default "release"), persisted (:122), and round-tripped by `listReleaseSLAWaivers` (sqlite path via `ListReleaseSLAWaivers`, in-memory :287-310) but evaluated nowhere. Breaches occurring after waiver creation are equally covered, and unreadable-archive runs conservatively marked breached (:197-211) are then silently waived too. Medium is fair: governance display/gate misrepresentation, not an execution blocker.

</details>


#### 20. JMeter ZIP with multiple .jmx files silently imports only the alphabetically first plan
**Where:** `pkg/api/handlers_import_jmeter.go:191` · **Area:** AREA-04 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

readJMeterZipImport (pkg/api/handlers_import_jmeter.go:172-192) collects every .jmx entry into jmxFiles, sorts the names, and returns only names[0]; the other test plans are discarded with no warning, diagnostic, or unsupported-element entry — neither handleImportJMeter (line 23) nor writeJMeterImportResponse (line 323) mentions the ignored files anywhere in the response.

**Failure scenario:** A user exports two JMeter test plans and uploads plans.zip containing checkout.jmx and login.jmx plus shared CSVs to POST /scripts/import/jmeter. Only checkout.jmx (alphabetically first) is converted; the response reports a successful import with stats/preflight for that one plan and zero mention of login.jmx. The user saves, believing the whole upload imported, and discovers the missing scenario only when the login flow is absent from load tests.

**Suggested fix:** Either convert every .jmx into the existing multi-script response shape (result.Scripts already supports it), or keep single-plan behavior but append a warning listing the ignored .jmx entries (and reject when >1 unless a `jmx_entry` form value selects one).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V021 — CONFIRMED (medium)

readJMeterZipImport (pkg/api/handlers_import_jmeter.go:172-192) collects every `.jmx` entry into `jmxFiles`, then `sort.Strings(names); selected := names[0]; return jmxFiles[selected], selected, sidecars, nil` — all other plans are discarded. The caller (lines 121-130) replaces `data`/`fileName` with the single selected entry; no warning is appended anywhere. `result.Warnings` (lines 334, 341) comes solely from the converter (ConvertJMX), and a grep of the handler shows no "multiple"/"ignored" surfacing of unselected .jmx entries. ZIP path is reachable via isJMeterZipUpload (line 146-150, .zip extension or zip content-type). Silent partial import with a success response matches the claimed failure scenario; medium severity holds.

</details>


#### 21. Merge re-import replaces a same-named CSV data source wholesale when its inline rows are e
**Where:** `pkg/api/openapi_import_commit.go:272` · **Area:** AREA-04 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

mergeCSVDataSource returns `imported, imported.Columns` whenever parseCSVInline(existing.InlineData) yields no header (pkg/api/openapi_import_commit.go:271-274), and mergeImportDataSources then overwrites the existing entry in place (line 253-255). A user-customized source whose data lives outside InlineData — Path/StorageRef file binding, or rows deliberately cleared — loses Strategy, Delimiter, Path, StorageRef and every other field to the fresh import's placeholder. Simultaneously diffImportByKey reports `PreservedDataSources = len(existing.DataSources)` and emits 'Preserved N existing data source(s)' (lines 55-64), so the dry-run and commit diff affirmatively misstate what the merge did. This is the documented merge contract's failure mode ('existing seed rows... preserved', line 237-240) and matches the area's re-import-wipes-user-edits defect class.

**Failure scenario:** User imports an OpenAPI spec producing csv source `users` with inline placeholder rows; via the editor they point it at a governed uploaded file (Path/StorageRef set, InlineData cleared, Strategy=unique). Later they re-import the updated spec with mode=replace, replace_mode=merge_by_operation_id. The committed script's `users` source is now the import's inline placeholder: file binding and unique strategy silently gone, next run iterates fabricated sample rows instead of real test data, while the commit response diff says 'Preserved 1 existing data source(s)'. Verified reachable: handleImportCommit line 946 calls spec.merge → mergeImportScriptByKey line 126 → mergeImportDataSources; dsl.DataSource (pkg/dsl/types.go:745-759) supports Path/StorageRef on csv-typed sources. Assumed (not verified end-to-end): the editor permits clearing InlineData on a csv-typed source while keeping the name; if it always retypes to 'file', the type guard at line 268 makes the wipe unreachable and this drops to low.

**Suggested fix:** When the existing source has no parseable inline header but is non-empty in other respects (Path/StorageRef set, or Strategy/Columns configured), return `existing` unchanged (optionally merging Columns metadata) instead of adopting the imported source; only adopt wholesale when the existing source is entirely default. Make the diff report replaced sources honestly.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V023 — CONFIRMED (medium)

mergeCSVDataSource (pkg/api/openapi_import_commit.go:267-274): both sources csv-typed, `parseCSVInline(existing.InlineData)` yields empty header → `return imported, imported.Columns`, replacing the struct wholesale via mergeImportDataSources (lines 252-254), losing Path/StorageRef/Strategy/Delimiter. The finding's open assumption is resolved: csv+empty-InlineData+Path is a legitimate runtime configuration — pkg/engine/data.go:292-298 falls back to `fileasset.OpenPath(src.Path)` when InlineData is blank (test at pkg/engine/data_test.go:562 exercises csv+Path). Reachable: import_commit_service.go:946 `spec.merge` → mergeOpenAPIImportScript:79 → mergeImportScriptByKey:126. Meanwhile diffImportByKey (lines 55-65) sets PreservedDataSources = len(existing.DataSources) unconditionally and emits "Preserved N existing data source(s)", misstating the replacement. Medium holds.

</details>


#### 22. Fleet-alert webhooks fire only when a client polls GET /api/v1/fleet/alerts, and concurren
**Where:** `pkg/api/handlers_fleet.go:112` · **Area:** AREA-05 · **Category:** reliability-notifications · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

routeFleetAlertNotifications is invoked solely from handleGetFleetAlerts (pkg/api/handlers_fleet.go:112-117); no background evaluator exists (only other buildFleetAlerts caller is the ack handler at 145). So configured fleet_alert webhooks never fire unless a user/dashboard happens to poll the endpoint — an unwatched controller sends no alert for an unreachable/saturated generator. Additionally routeFleetAlertNotifications (443-495) performs an unsynchronized read-modify-write of the 'fleet_alert_notification_state' setting: two concurrent GETs both load the state, both see an alert as un-routed, both Dispatch, and the second SaveSetting clobbers the first, so multi-viewer dashboards produce duplicate webhook notifications per new alert.

**Failure scenario:** Ops team configures a Slack webhook for fleet_alert and closes the dashboard overnight. A generator goes unreachable at 02:00; no notification is ever sent because nothing polls /fleet/alerts (defeats the purpose of webhook alerting). Conversely during the day, 3 open dashboards poll concurrently when a new saturation alert appears; each request observes Routed missing and dispatches, delivering the same alert 2-3 times to Slack.

**Suggested fix:** Move routing into the existing generator-inventory refresh cadence (leader-gated), and guard the routed-state read-modify-write with a mutex or the CAS setting primitive already used for remote-run links.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V027 — CONFIRMED (medium)

routeFleetAlertNotifications is called only from handleGetFleetAlerts (pkg/api/handlers_fleet.go:115); repo-wide grep shows no background caller, and buildFleetAlerts is only invoked from the GET (113) and ack (145) handlers — so fleet_alert webhooks (supported event, notifications.go:52) never fire without a client poll. The routed-state read-modify-write is unsynchronized: loadFleetAlertNotificationState (447), Dispatch (484), SaveSetting (490) with no mutex/CAS. Concurrent GETs both see an alert un-routed and both dispatch; sender-side there is no dedupe — notificationDedupeKey (notifications.go:434-453) includes event.Timestamp (time.Now per dispatch, handlers_fleet.go:533), so even receiver-side idempotency keys differ across the duplicate dispatches. Both claimed behaviors hold.

</details>


#### 23. Remote-run handlers hold remoteConfigMu.RLock across multi-call cross-controller HTTP, sta
**Where:** `pkg/api/remote_runs.go:88` · **Area:** AREA-05 · **Category:** concurrency-lock-scope · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleCreateRemoteRun (pkg/api/remote_runs.go:88-89), handleGetRemoteRun (227-228) and handleRemoteRunAction (260-261) take s.remoteConfigMu.RLock with defer and then perform a chain of outbound HTTP calls — one pushRemoteWorkspaceItem per referenced scenario/script (each a remoteLibraryImport POST), the test create, an optional start, and a status refresh — each bounded only by defaultLibraryRemoteJSONTimeout of 30s (pkg/api/library_federation.go:27). handleLibraryUpdateRemote/handleLibraryDeleteRemote take the write lock (pkg/api/library_handlers.go:279, 369). Go's RWMutex blocks new readers once a writer is waiting, so a single slow remote peer freezes every endpoint that touches remoteConfigMu for the duration of the in-flight chain.

**Failure scenario:** A federated peer accepts TCP but stalls responses (overloaded or half-dead LB). A user creates a remote run for a scenario with 3 scripts: the handler holds the read lock through 4-6 HTTP calls x 30s timeout (2-3 minutes). An admin then saves remote-workspace settings -> write Lock blocks; every subsequent remote-workspace/remote-run request (all RLock) queues behind the writer. Dashboard pages backed by these endpoints hang for minutes; more concurrent creates extend the freeze indefinitely. Expected: config reads snapshot the remote value (LibraryRemote is already copied by value) and release the lock before any network I/O.

**Suggested fix:** Resolve freshLibraryRemote under a short RLock, release, then run the remote workflow on the copied LibraryRemote value (authorizeRemoteRequest already revalidates transport per request). Re-take the lock only if config must be re-read.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V026 — CONFIRMED (medium)

RLock+defer spans entire handlers: pkg/api/remote_runs.go:88-89 (create), 227-228 (get), 260-261 (action). Inside the lock: per-item pushRemoteWorkspaceItem loop (remote_runs.go:358 via prepareRemoteRunCreateRequest at 103), remoteJSON create POST (125), optional start (192), status refresh (200) — each capped only by libraryRemoteJSONTimeout, default 30s (pkg/api/library_federation.go:27; applied in library_remote.go:250-252). Writers take remoteConfigMu.Lock: library_handlers.go:279 (update), 369 (delete). Go RWMutex blocks new readers once a writer waits, so one stalled peer plus one config save freezes all remoteConfigMu RLock endpoints (library_handlers.go:428/482/512/579/657 and remote_runs). server.go:89-93 documents holding the lock for the full outbound operation as intentional (cleanup-intent stability), but the multi-minute cross-endpoint starvation stands; LibraryRemote is copied by value so a snapshot-then-release fix is feasible.

</details>


#### 24. Object-repository bind-from-script rebinds nested control-flow actions by name while the a
**Where:** `pkg/api/handlers_object_repository.go:395` · **Area:** AREA-06 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

objectRepositoryObjectsFromScript (handlers_object_repository.go:386-453) and ambiguousObjectNames (348-381) scan ONLY top-level script.Init/Actions/Teardown (no recursion), but bindScriptToObjectRepository (301-330) recursively walks Control.Steps/Control.Else and sets object_ref on every name match. A nested action that shares a name with a top-level action but targets a different element is never seen by the ambiguity check, so both get bound to the one repository object built from the top-level action's locator. resolveActionObjectRef (object_ref_resolver.go:169) then overwrites the nested action's inline Selector with the repository primary at script-preparation time, so the nested action executes against the wrong element — exactly the mis-binding the code comment at 389-394 says the ambiguity exclusion exists to prevent.

**Failure scenario:** A TruClient/recorder-imported browser script has top-level action "Click Continue" (selector A) and, inside a control_if branch, another step named "Click Continue" with a different selector B (a different Continue button). Operator calls POST /api/v1/scripts/object-repositories/from-script with bind_script=true. The repo object "Click Continue" is built from selector A only; binding walks the nested branch and sets object_ref on the nested step too. On the next replay/load run, resolveObjectRefs rewrites the nested step's selector B to A, and the script silently clicks the wrong element inside the branch — corrupted behavior with no warning, since the nested action had an inline selector so degradation paths never fire.

**Suggested fix:** Make the three scans share one traversal scope: either recurse collect() and ambiguousObjectNames() through Control.Steps/Else (preferred, matches bind), or restrict bindScriptToObjectRepository to top-level actions. Sharing dslwalk.WalkActions for all three removes the drift permanently.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V033 — CONFIRMED (medium)

Scope drift verified. `ambiguousObjectNames` (pkg/api/handlers_object_repository.go:348-381) and `collect` in `objectRepositoryObjectsFromScript` (:396-451) iterate only top-level Init/Actions/Teardown slices — neither recurses into `action.Control`. But `bindScriptToObjectRepository` (:301-330) walks `action.Control.Steps` and `.Else` recursively (:320-323) and stamps `object_ref` on every name match. The bind path is live: handler calls it when `req.BindScript` (:249-251) and persists via UpdateScript. At preparation, `resolveActionObjectRefs` also recurses into Control (pkg/api/object_ref_resolver.go:135-138) and `resolveActionObjectRef` unconditionally overwrites `action.Selector = primary` (:169) on successful lookup — degrade paths fire only on lookup failure, so a nested same-named action with a different inline selector is silently re-targeted, exactly what the comment at :389-394 says the ambiguity exclusion prevents.

</details>


#### 25. Command Center iterations edit always 400s for groups with a create-time iteration overrid
**Where:** `pkg/api/handlers_test_group_settings.go:624` · **Area:** AREA-07 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

UpdateTestGroupRuntimeSettings builds the recompile entry with Iterations: cloneIntPtr(oldGroup.IterationOverride) (pkg/api/handlers_test_group_settings.go:624) while putting the user's new iterations value into entry.RuntimeSettings via replaceRuntimeFields (handlers_test_group_settings.go:598). compileManagedTestGroupForModeWithRevisionLocked then rejects any request where group.Iterations != nil and the runtime-settings iteration value differs (pkg/api/testcreation.go:1562-1565), so a group whose iterations came from the scenario per-script Iterations field (createGroupEntriesFromStoredScenario, testcreation.go:1037 -> IterationOverride at testcreation.go:1637) can never have its iterations changed from the Command Center dialog.

**Failure scenario:** Author a scenario with a script group whose Iterations = 10 (the standard scenario-editor iterations field). Create a queued run from it. In Command Center, right-click the group -> Runtime Settings, change Iterations to 20, Save. The dashboard sends fields:["iterations"], runtime_settings:{iterations:20} (GroupRuntimeSettingsDialog.tsx builds the payload from the group override layer); the backend merges it, then compile fails with HTTP 400 'group[0]: iterations conflicts with runtime_settings.run_logic.iterations' — a message referencing a conflict the user never authored. There is no path to change iterations for such a group from the run view; the pre-start in-place edit feature is broken for the most common iterations-authoring path. Workaround: revise the queued run through the scenario editor.

**Suggested fix:** When normalizedFields contains 'iterations', derive the new override from the merged runtime settings and pass it as entry.Iterations (and drop the compact alias from merged), or clear entry.Iterations so the runtime-settings layer is authoritative for this edit. Also surface IterationOverride in the view's layer attribution.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V038 — CONFIRMED (medium)

Chain verified. Scenario per-script Iterations becomes CreateGroupEntry.Iterations (pkg/api/testcreation.go:1036) and compiles to TestGroup.IterationOverride with IterationOverrideKnown=true (testcreation.go:1637-1638); at create time there is no conflict because scenario RuntimeSettings carries no iteration value. UpdateTestGroupRuntimeSettings always rebuilds the entry with Iterations=cloneIntPtr(oldGroup.IterationOverride) (pkg/api/handlers_test_group_settings.go:624) while replaceRuntimeFields("iterations") writes the user's new value into merged.Iterations (pkg/api/handlers_scenario_runtime.go:433-434, 477-498; stored group settings lack RunLogic since setRuntimeSettingsIteration only touches executionRuntimeSettings, testcreation.go:1689-1691, and stored settings come from protectedGroupSettings at 1795). Recompile (handlers_test_group_settings.go:630) then hits the conflict check: group.Iterations(old)=10 vs runtimeSettingsIterationValue(merged)=20 → error (testcreation.go:1562-1565) → request fails. No handler path updates entry.Iterations for the "iterations" field. Dialog exposes iterations as an editable field (GroupRuntimeSettingsDialog.tsx:34,86-87).

</details>


#### 26. Any value containing '@' is classified as a credential, breaking email-valued parameters a
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


#### 27. Schedule-health endpoint materializes the entire unbounded occurrence table (and force-rel
**Where:** `pkg/api/scenario.go:1040` · **Area:** AREA-07 · **Category:** performance · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

ScenarioScheduleHealth calls store.ListAllScenarioScheduleOccurrences() (pkg/api/scenario.go:1040), which is 'SELECT ... ORDER BY planned_at DESC' with no LIMIT (pkg/storage/sqlite_schedules.go:972-979), decoding every occurrence row ever created just to increment per-status counters. scenario_schedule_occurrences has no automatic retention (only the manual single-occurrence delete at scenario.go:960), so the table grows one row per planned tick per schedule forever. The same method also calls refreshScenariosForHARead(true) (scenario.go:1013), and the force flag bypasses the 250 ms throttle (scenario.go:560-561), so on HA deployments every health call additionally reloads all stored scripts and scenarios. The dashboard Monitoring page polls this endpoint every 5 seconds (dashboard/src/components/MonitoringPage.tsx:687).

**Failure scenario:** A deployment runs four recurring schedules on 15-minute crons for a year (~140k occurrence rows). An operator leaves the Monitoring page open: every 5 seconds the controller executes a full-table SELECT, allocates and JSON-decodes 140k ScenarioScheduleOccurrence structs, and (in HA) re-reads every script and scenario from the shared store. Health-endpoint latency and controller CPU/GC grow linearly with schedule history and never recover, degrading the whole controller while the run calendar card is visible; there is no retention mechanism to stop the growth.

**Suggested fix:** Add a CountScenarioScheduleOccurrencesByStatus storage method (GROUP BY status) and use it in ScenarioScheduleHealth; drop the force flag from the health path; add an occurrence retention policy or scheduled prune.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V040 — CONFIRMED (medium)

Chain verified. GET /api/v1/schedules/health (pkg/api/route_registry_scheduling.go:47) → handleScenarioScheduleHealth (pkg/api/handlers_scenarios.go:389) → schedulingDomainService().Health (pkg/api/scheduling_service.go:91) → TestRunner.ScenarioScheduleHealth (pkg/api/scenario.go:1012). It calls refreshScenariosForHARead(true) (scenario.go:1013); force=true bypasses the 250ms throttle (scenario.go:560-563), reloading all scripts+scenarios — HA-only (early return when tr.leadership==nil, scenario.go:555). It then materializes every occurrence row via ListAllScenarioScheduleOccurrences (scenario.go:1040) — unbounded `SELECT ... ORDER BY planned_at DESC` (pkg/storage/sqlite_schedules.go:972-979) — solely to increment StatusCounts (scenario.go:1041-1045). Only per-schedule/single-occurrence deletes exist (sqlite_schedules.go:536, 1014); no bulk retention, so rows accumulate per tick (SaveScenarioScheduleOccurrence, scenario.go:702). MonitoringPage fetches schedule health in refresh (MonitoringPage.tsx:617-644) on setInterval(refresh, 5000) (line 687).

</details>


#### 28. Run manifest 'initial configuration' is read from the continuously rewritten durable recor
**Where:** `pkg/api/test_manifest.go:284` · **Area:** AREA-07 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

initialManifestConfiguration sources the manifest's initial_configuration from tr.storedTestRecord(test.ID) (test_manifest.go:284-286), but the durable TestRecord is rewritten with the CURRENT test state on every lifecycle mutation — VUser scaling sets test.TotalVUsers/Groups and calls saveManagedTestRecord (pkg/api/testexecution.go:1963-1968), goal adjustments persist (testexecution.go:1488-1495), and there are ~34 saveManagedTestRecord call sites. For any test without an archived manifest (i.e. every live or pre-archive run), initial_configuration therefore equals current_configuration after any mid-run change, defeating the manifest's before/after comparison exactly when an operator inspects what changed mid-run.

**Failure scenario:** Start a run with group VUsers=100. Mid-run, scale the group to 300 (persisted via saveManagedTestRecord). GET /api/v1/tests/{id}/manifest while the run is live: both initial_configuration.total_vusers and current_configuration.total_vusers report 300; the manifest claims the run was created at 300 VUsers. The runtime_changes audit timeline still shows the scale event, contradicting the initial section in the same document — silent wrongness in an evidence artifact.

**Suggested fix:** Persist an immutable creation-time configuration snapshot (either a dedicated column written once at create, or reuse the runtime-secret journal pattern) and have initialManifestConfiguration prefer it; fall back to the current-record behavior with the existing warning only for legacy rows.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V041 — CONFIRMED (medium)

Chain verified. For any non-archived run (source="live", pkg/api/test_manifest.go:230), initialManifestConfiguration reads tr.storedTestRecord(test.ID) — the single current durable row — and projects it as initial_configuration (test_manifest.go:284-286). That row is rewritten with CURRENT state on every persist: buildManagedTestRecord serializes live Groups/TotalVUsers/GoalConfig/RuntimeSettings/Status (pkg/api/persistence.go:1373-1419), and mid-run VUser scaling both sets TotalVUsers=capacityVUsersForGroups and calls saveManagedTestRecord (pkg/api/testexecution.go:2158-2161 rollback path; 1963-1967 fail-closed path; 35 save sites total). So after a mid-run scale, initial_configuration equals current_configuration, contradicting the runtime_changes timeline in the same manifest. The fallback warning at test_manifest.go:291 shows the design assumed the record held initial state. PRIOR-REPORT CHECK: M6/TierC-5 (DATA_POOLS_EVALUATION_2026-07-30.md:81,70) concern missing pool provenance fields in the manifest — a different defect, and both are still "open", so this is not a regression of a fixed item.

</details>


#### 29. Redis AllowRateLimit INCR/EXPIRE gap can permanently lock out a key
**Where:** `pkg/api/cache_backend.go:642` · **Area:** AREA-08 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

redisCacheBackend.AllowRateLimit increments the counter and only sets the TTL when the returned count is exactly 1 (cache_backend.go:650-657); if the process crashes or the connection drops between INCR and EXPIRE (or EXPIRE errors), the key persists with no TTL, subsequent requests see count != 1 and never set an expiry, so the counter grows monotonically and once it passes the limit every request for that key is rejected forever until someone manually deletes the redis key.

**Failure scenario:** APEXLOAD_API_KEY_RATE_LIMIT_PER_MINUTE=600 is configured with a redis cache backend. A controller pod is OOM-killed between Incr and Expire for service-account:ci-bot. The key now has TTL=-1; every subsequent request increments it and after ten minutes of normal CI traffic count exceeds 600 permanently — from then on every ci-bot request returns 429 'service account rate limit exceeded' indefinitely across all replicas, until an operator manually runs DEL on the key in redis.

**Suggested fix:** Replace the two-step with a Lua script (INCR; if result == 1 or PTTL < 0 then PEXPIRE) mirroring redisLoadOrStoreValueScript, or self-heal by checking PTTL when count > 1 and re-arming a missing expiry.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V049 — CONFIRMED (medium)

redisCacheBackend.AllowRateLimit (pkg/api/cache_backend.go:642-664): Incr at 650, then `if count == 1 { Expire }` at 654-658 — the classic non-atomic fixed-window gap. A crash/connection drop between INCR and EXPIRE (or an EXPIRE error, which returns after the increment already landed) leaves the key with no TTL; every later call sees count != 1 so no expiry is ever re-armed, and once count exceeds the limit the key rejects forever. No PTTL self-heal exists, while sibling LoadOrStoreValue uses an atomic Lua script (607-614), proving the pattern is available in-tree. Consumers with no recovery path: enforceServiceAccountRateLimit → 429 (middleware.go:284-305) and per-IP OIDC login limiting (auth_oidc.go uses the same primitive). Low-probability trigger, high impact — medium is right.

</details>


#### 30. Project and test deletion orphan powerbi_publishes rows forever
**Where:** `pkg/api/handlers_projects.go:552` · **Area:** AREA-08 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleDeleteProject (pkg/api/handlers_projects.go:552) deletes the project graph via deleteProjectGraphTx (pkg/storage/sqlite_projects.go:1289-1364), and handleDeleteTest via deleteTestGraphTx (pkg/storage/sqlite_tests.go:1323-1394); neither cascade contains a DELETE for powerbi_publishes, whose rows carry NOT NULL test_id and project_id (pkg/storage/sqlite_schema.go:685-700), and a repo-wide grep shows no other DELETE for that table anywhere — so publish records outlive both their test and their project permanently.

**Failure scenario:** Publish a completed run to Power BI (creates a powerbi_publishes row keyed by test_id/project_id), then delete the test or the whole project. The publish row survives with dangling foreign references. Over months of project churn in a deployment using Power BI publishing, the table accumulates unbounded orphan rows; any listing/dedupe query keyed by test_id or dataset (idx_powerbi_publishes_dataset) returns records for tests and projects that no longer exist, and the force-republish dedupe can match a stale orphan from a previous, deleted incarnation of a re-imported test id.

**Suggested fix:** Add `DELETE FROM powerbi_publishes WHERE project_id = ? OR test_id IN (SELECT id FROM tests WHERE project_id = ?)` to deleteProjectGraphTx and `DELETE FROM powerbi_publishes WHERE test_id = ?` to deleteTestGraphTx; audit direct_import_receipts for the same gap (it has lease-scoped deletes but verify project deletion coverage).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V046 — CONFIRMED (medium)

powerbi_publishes DDL carries NOT NULL test_id and project_id (pkg/storage/sqlite_schema.go:685-703). deleteProjectGraphTx's ~40-entry delete list (pkg/storage/sqlite_projects.go:1293-1364) has no powerbi_publishes statement, and deleteTestGraphTx (pkg/storage/sqlite_tests.go:1323-1394) deletes test_history/test_notes/run_archives/etc. but not powerbi_publishes. Repo-wide grep shows only two files reference the table — sqlite_schema.go and sqlite_powerbi.go — and sqlite_powerbi.go contains no DELETE at all, so no retention/prune path exists anywhere. Rows therefore survive both test and project deletion permanently, accumulating orphans with dangling foreign references exactly as claimed. Severity medium is appropriate: data-integrity/unbounded-growth, no security impact.

</details>


#### 31. Failed requests permanently burn their Idempotency-Key for the TTL
**Where:** `pkg/api/middleware.go:307` · **Area:** AREA-08 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

enforceIdempotencyKey reserves the key in the coordination store BEFORE the handler runs and has no release/rollback path, so a request that fails validation (400), authorization at the handler level, or the operation itself (500) still consumes the key for configuredIdempotencyKeyTTL (default 15 min, middleware.go:353-355); a client retrying with the same key — the standard idempotency retry contract — receives 409 'duplicate idempotency key' even though nothing was ever created, and even on success a replay returns 409 instead of the original resource (except the three carved-out routes at middleware.go:314-317).

**Failure scenario:** A CI pipeline POSTs /api/v1/tests with Idempotency-Key K; the controller returns a transient 500 (store busy) without creating the test. The pipeline retries with K per standard semantics and gets 409 'duplicate idempotency key' on every attempt for 15 minutes; the run is never created and the error message actively misleads the operator into believing it was. The same happens after any 400 (typo fixed, key reused).

**Suggested fix:** Wrap the handler with a status-observing responseWriter and delete/expire the reservation when the response status is >= 400 (at-most-once is only needed for success); or reserve only after the handler reports success. Document that replay-of-response is not provided outside the carved-out durable routes, and change the 409 message to say the key may correspond to an in-flight or failed request.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V048 — CONFIRMED (medium)

enforceIdempotencyKey (pkg/api/middleware.go:307-342) calls store.ReserveIdempotencyKey (line 331) BEFORE next.ServeHTTP in both authMiddleware (line 204-215) and projectScopedAuthMiddleware (line 268-272); !reserved always yields 409 "duplicate idempotency key" (lines 337-339). The IdempotencyStore interface (cache_backend.go:78) exposes only Reserve — grep finds no Release/Delete API; memory backend is set-if-absent (cache_backend.go:345-358), redis is SetNX with TTL (600-605). So a 400/500 first attempt still burns the key for configuredIdempotencyKeyTTL (default 15 min, middleware.go:353-355) and retries get a misleading 409. Only three routes are carved out (314-317). The prior-report match (api-auth-rbac-2, middleware duplication, status open) is a different issue — not a regression. Medium confirmed.

</details>


#### 32. Project deletion stalls every mutating request platform-wide, unbounded
**Where:** `pkg/api/project_mutation_gate.go:15` · **Area:** AREA-08 · **Category:** concurrency · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

projectMutationAdmission takes projectMutationMu.RLock for the FULL duration of every mutating request (including reading a slow client's multi-hundred-MB upload body, since the gate wraps the mux at pkg/api/server.go:553 and handlers read bodies inside), while handleDeleteProject (pkg/api/handlers_projects.go:401) holds the write side across external secret-provider discovery (network I/O, handlers_projects.go:417), listing up to 10000 outbox jobs, acquiring per-test archive fences, the durable graph delete, and per-archive physical object-store cleanup (handlers_projects.go:582-585) — and Go's writer-preferring RWMutex blocks ALL new readers the moment the writer starts waiting.

**Failure scenario:** A user is 3 minutes into uploading a 200 MB Bruno import over a slow link (holding an RLock). An admin deletes a project with 150 archived runs on Azure blob storage. The DELETE blocks waiting for the upload; from that instant every mutating request across ALL projects (test starts, stops, scale, script saves) queues behind the pending writer; requests exceed the 60 s WriteTimeout and fail platform-wide. After the upload completes, the deletion itself holds the write lock through secret discovery and 150 blob deletions (potentially minutes with retries/timeouts), extending the global write outage.

**Suggested fix:** Shrink the write-side critical section to just the tombstone install + durable graph delete: move externalsecret discovery before the lock, and move physical archive cleanup after releasing projectMutationMu (the beginProjectDeletion tombstone plus the per-test archive fences already prevent new work on the deleted project). Alternatively acquire the RLock in handlers after the body is fully read, or replace the global gate with a per-project striped lock keyed by resolved project id.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V047 — CONFIRMED (medium)

projectMutationMu is sync.RWMutex (pkg/api/server.go:114). projectMutationAdmission holds RLock via defer across next.ServeHTTP for every POST/PUT/PATCH/DELETE (pkg/api/project_mutation_gate.go:21-23); it wraps the mux inside bodyLimitMiddleware (pkg/api/server.go:553), which only installs MaxBytesReader (middleware.go:625-634), so slow body reads occur under the RLock. handleDeleteProject takes the write side for its entire body (handlers_projects.go:401-402), covering discoverProjectSecretMetadata external-provider I/O (line 418), durable graph delete, and the per-archive cleanupQueuedArchiveUnderFence object-store loop (lines 583-586). Go's writer-preferring RWMutex blocks all new readers once the writer waits, so every mutating request platform-wide queues against WriteTimeout=60s (server.go:562). The gate comment declares global serialization intentional, but the unbounded write-side critical section is not. Medium stands.

</details>


#### 33. Same-origin check ignores X-Forwarded-Proto, 403s mutations behind TLS proxies
**Where:** `pkg/api/security.go:70` · **Area:** AREA-08 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

evaluateOriginPolicy derives requestScheme solely from r.TLS (security.go:71-74), so behind a TLS-terminating ingress a browser's Origin 'https://host' never matches the plain-HTTP request scheme, same-origin detection fails, and with no AllowedOrigins configured corsMiddleware rejects every non-safe-method request with 403 'request origin is not allowed' (middleware.go:492-495) and the websocket upgrade CheckOrigin (websocket.go:66-75) refuses the connection — even though securityHeadersMiddleware (middleware.go:671) already trusts X-Forwarded-Proto for HSTS, so the codebase is internally inconsistent about proxy awareness.

**Failure scenario:** Deploy the controller on Kubernetes behind an nginx ingress that terminates TLS (the canonical K8s-native topology) with APEXLOAD_ALLOWED_ORIGINS unset. The dashboard loads fine (GETs are safe methods) but every mutation — create test, start test, save script — returns 403 'request origin is not allowed', and live telemetry websockets fail the upgrade. The platform appears read-only; the operator must discover that the fix is adding their own external origin to the allow-list, which nothing in the error message suggests.

**Suggested fix:** In evaluateOriginPolicy, honor X-Forwarded-Proto (gated on the same trusted-proxy conditions used elsewhere, or at minimum when r.TLS == nil and the header says https) before comparing schemes; alternatively compare host-only for the same-origin shortcut and treat an https-Origin-over-http mismatch as same-origin when X-Forwarded-Proto=https.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V051 — CONFIRMED (medium)

evaluateOriginPolicy (pkg/api/security.go:71-78) sets requestScheme="https" only when r.TLS != nil or r.URL.Scheme is set (always empty on server requests; no rewrite anywhere in pkg/api). Behind a TLS-terminating proxy, Origin "https://host" fails strings.EqualFold(parsed.Scheme, "http") at line 78; with no allow-list, lines 82-101 return not-allowed. corsMiddleware then 403s every non-safe method (middleware.go:492-495 — browsers send Origin on all POSTs, even same-origin), and the WS upgrade uses the same policy (websocket.go:66-75). Inconsistency confirmed: HSTS (middleware.go:671) and powerbi_publish.go:69 both trust X-Forwarded-Proto. Workaround exists (APEXLOAD_ALLOWED_ORIGINS), so medium, not high.

</details>


#### 34. Bulk sync push skips the stored-link drift check and silently overwrites remote edits
**Where:** `pkg/api/github_sync_service.go:185` · **Area:** AREA-09 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

runPush fetches the file's CURRENT remote blob sha (pkg/api/github_sync_service.go:185-189) and passes it to PutFile (line 190), so the write always succeeds over whatever is on the branch. The single-script push path loads the stored GitHubScriptLink and returns 409 when remoteSHA != link.RemoteBlobSHA (pkg/api/github_handlers.go:433-442: "remote file has changed since the last sync; pull and review before pushing"). Bulk push never consults the link store before writing (it only writes links after, lines 195-210), so remote-side edits made since the last sync are clobbered without any conflict signal.

**Failure scenario:** Script X was previously pushed to repo file scripts/x.json (link.RemoteBlobSHA=S1). A teammate edits scripts/x.json directly on GitHub (blob now S2). An operator runs a bulk sync push job for the project. runPush reads the current sha S2 and PutFiles the local envelope over it; the teammate's edits disappear from the branch tip with no conflict reported — the job records Updated=1, status complete. Pushing the same script through the per-script endpoint would have returned 409. The commit is recoverable from git history but the overwrite is silent.

**Suggested fix:** Before PutFile in runPush, load the script's GitHubScriptLink; if a link exists with a RemoteBlobSHA/LastCommitSHA and the live remote sha differs, count a Conflict for that script (mirroring the single-push 409 semantics) instead of overwriting; optionally add a force flag on the job request.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V055 — CONFIRMED (medium)

Bulk push path: `pkg/api/github_sync_service.go` runPush (150-214) fetches the file's CURRENT remote sha (185-189) and passes it straight to PutFile (190), so the write always succeeds over whatever is on the branch; the link store is only written AFTER the overwrite (195-210) and is never consulted before it. By contrast the single-script push handler loads the stored link and returns 409 on drift (`pkg/api/github_handlers.go:433-442`, message "remote file has changed since the last sync; pull and review before pushing"). runPush is reachable via the sync-job handler (`pkg/api/github_enterprise.go:711-712`, kind="push"). Remote edits made since last sync are silently clobbered by a bulk job that reports Updated/complete. Recoverable from git history, so medium stands.

</details>


#### 35. Browser-recording list summaries and WS broadcasts always report 0 network requests, 0 web
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


#### 36. Every browser-replay progress broadcast embeds the full accumulated event list (O(n^2) CPU
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


#### 37. Synchronous classic replay endpoint exceeds the server's fixed 60s WriteTimeout and loses 
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


#### 38. Proxy recorder silently drops requests once caps are hit — no warning, counter, or event
**Where:** `pkg/recorder/recorder.go:1251` · **Area:** AREA-10 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

recordRequestWithOptions returns 0 without any signal when len(s.Requests) >= maxRecordedRequests (pkg/recorder/recorder.go:1251-1253) and when the per-session byte budget is exceeded (1358-1361); appendWebSocketMessage silently stops at its frame/byte caps (1403-1407). recorder.Session has no warnings field and no dropped counter, no SessionEvent is emitted, and the converter's warnings (pkg/recorder/converter.go) only cover correlation/static/websocket cases — so a capped recording converts to a script and exports a HAR that simply end mid-flow with zero indication anywhere in the API or UI.

**Failure scenario:** An operator records a chatty SPA (background polling every second) over a long workflow and crosses 10,000 captured requests; the final login/checkout steps of the workflow are silently absent from the session. ConvertSessionWithOptions produces a script missing those endpoints and ExportHAR omits them, with no warning in the conversion response, the session JSON, or the UI — the operator only discovers the gap when replay of the generated script never reaches the tail of the flow.

**Suggested fix:** Add DroppedRequests/DroppedWebSocketMessages counters (and a boolean truncated flag) to Session, increment them on every capped return, emit one warning-style SessionEvent on first drop, and surface the counters in sanitizeRecorderSessionForResponse plus a conversion warning in ConvertSessionWithOptions.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V062 — CONFIRMED (medium)

pkg/recorder/recorder.go:1251-1253 returns 0 when `len(s.Requests) >= maxRecordedRequests` (10,000; :35), and :1358-1361 returns 0 when the request exceeds `maxRecordedSessionBytes` (512MiB; :39) headroom — no counter increment, no event, no field set. appendWebSocketMessage (:1404-1407) likewise returns silently at frame/byte caps. Session struct (:113-135) has no warnings/dropped/truncated field; only per-body truncation flags exist. Converter warnings (pkg/recorder/converter.go:65-203) cover correlation/static/WS-metadata only — none reflect cap truncation, so scripts/HAR end mid-flow with zero indication. Prior-report check: PERF_REVIEW_2026-07-06 item 32 was unbounded RAM retention; these caps are its remedy, and the missing signaling is a new follow-on gap, not a regression.

</details>


#### 39. Live import pays for a discarded classification pass, including a duplicate full runtime-r
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


#### 40. splitJavaTopLevel treats comparison/shift operators as generics, merging call arguments
**Where:** `pkg/convert/enterprisearchive/java_model.go:534` · **Area:** AREA-11 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

splitJavaTopLevel (java_model.go:502-550) increments angleDepth for any '<' whenever findJavaGenericAngleEnd (java_model.go:552-592) finds a later '>', so an argument list like 'count < MAX, retries > 0' (or 'a << 2, b >> 3') is returned as ONE argument instead of two. javaCallArguments (java_import.go:1360-1377) feeds this into javaResolvedEvidenceHelperCalls (java_classify.go:4104-4119) and resolveConstructions (java_import.go:4480-4509), where javaMethodAcceptsArgumentCount (java_import.go:2063-2070) then rejects the real 2-parameter helper/constructor by arity.

**Failure scenario:** Actions.java: action() calls 'doWork(count < MAX_COUNT, retries > 0);' where private void doWork(boolean, boolean) contains lr.start_transaction/lr.end_transaction. The merged single argument makes javaHelperOverloadsForArguments return zero candidates, so the reachability builder never walks doWork: javaProgramTransactionStats (java_import.go:4546-4586) reports no transactions (the draft's emits_business_transactions metadata stays false, so the engine keeps the automatic iteration transaction while the real script also emits Checkout at runtime — double-counted transactions), recordJavaProgramTransactionNames misses the declared name, and javaVuserRunnerSemanticBlockers (java_import.go:5010-5053) fails to flag review-only lr.* calls inside doWork, weakening the promotion gate (fail-open). The mirror case 'new Helper(a < b, c > d)' hits java_import.go:4503-4509 and raises a FALSE 'did not resolve to exactly one archived constructor' promotion blocker (fail-closed nuisance).

**Suggested fix:** In splitJavaTopLevel, only enter angle-tracking when the '<' can open a generic type argument: previous non-space byte is an identifier byte and the candidate span between '<' and the matched '>' contains only type-argument-legal characters (identifiers, dots, commas, whitespace, ?, extends/super, nested <>, []). Otherwise treat '<' as an operator. Add the same guard for '<<'/'>>' by skipping doubled brackets.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V066 — CONFIRMED (medium)

java_model.go:533-536: on `<` at depth 0, angleDepth++ whenever findJavaGenericAngleEnd finds any balanced later `>`; findJavaGenericAngleEnd (552-592) only counts brackets, no type-position check. Traced `count < MAX, retries > 0`: the comma is consumed at angleDepth==1, so splitJavaArgs returns ONE merged argument; `a << 2, b >> 3` likewise. javaCallArguments (java_import.go:1360-1377) returns this list; javaHelperOverloadsForArguments filters via javaMethodAcceptsArgumentCount (java_import.go:2063-2070, exact ParameterCount match), so a real 2-param helper yields zero candidates and addCandidates (java_classify.go:4043-4073) appends nothing — the reachability walk misses the helper (fail-open for transaction stats/semantic blockers). Constructor mirror proven at java_import.go:4503-4509: len(constructors)!=1 raises the false "did not resolve to exactly one archived constructor" blocker.

</details>


#### 41. Registrations after the last request are silently dropped with overcounted stats and no re
**Where:** `pkg/convert/enterprisearchive/converter.go:802` · **Area:** AREA-12 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

pendingExtractors/pendingAssertions/pendingHeaders/pendingSourceMappings are attached only when a subsequent request converts (converter.go:655-738); the loop ends at converter.go:802 with no flush, so a web_reg_save_param/web_reg_find/web_add_header after the final request vanishes entirely. Stats.Extractors and NativeFunctions were already incremented at registration time (converter.go:272-273, recordSourceFunctionSupport Executable), so the fidelity report overstates conversion, and the queued source-review mapping is lost, violating the package's own no-silent-drop discipline.

**Failure scenario:** A common LR cross-iteration correlation pattern — web_reg_save_param at the end of Action.c capturing a token consumed at the top of the next iteration — imports with the extractor silently discarded. The earlier request still references {{var.next_token}}, which never gets populated; replay sends an empty/unresolved token and the correlation failure has no importer-side explanation because the registration is absent from the review table.

**Suggested fix:** After the loop, flush leftover pending items: either attach them to the first request of the same phase (LR next-iteration semantics) with an explanatory note, or emit review_only mappings plus a warning and decrement the optimistic stats.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V076 — CONFIRMED (medium)

pendingExtractors/pendingAssertions/pendingHeaders/pendingSourceMappings are declared at converter.go:204-209 and consumed ONLY inside the converted-request branch: extractors/assertions/headers attach at converter.go:655-676, queued mappings flush at converter.go:722-738. Grep over converter.go shows no other reference; the function loop ends at converter.go:802 and post-loop code (804-817, 844) never flushes leftovers. Meanwhile web_reg_save_param increments Stats.Extractors/ContributorFunctions and records Executable support at registration time (converter.go:271-273; headers likewise at 357-363). So a registration after the last converted request is dropped with no warning, no source-review row (its mapping dies in pendingSourceMappings), and overstated stats. pruneUnusedWholeBodyExtractors (converter.go:844) only adjusts attached whole-body extractors, not this. Chain holds.

</details>


#### 42. Parameter and data-source emission order is nondeterministic; shared data-file config is f
**Where:** `pkg/convert/enterprisearchive/parameters.go:33` · **Area:** AREA-12 · **Category:** determinism · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

ParseParameterFile ranges over the sections map (parameters.go:33), so script.Parameters and script.DataSources order differs between imports of the identical archive. When two parameter sections reference the same data file with different delimiter/column settings, dataSourceByFile dedupes by basename (parameters.go:126-133) and whichever section the map iteration visits first defines the DataSource's delimiter/columns — a nondeterministic tie-break that can select the wrong delimiter for the other parameter.

**Failure scenario:** Re-importing the same archive produces scripts whose parameters/data_sources arrays are ordered differently (spurious diffs in review and versioning). With two .prm sections pointing at the same data.dat but declaring delimiter="," and delimiter="\t", some imports parse the file as CSV and others as TSV, changing every resolved column value fed to runs.

**Suggested fix:** Sort section names before iterating and make the shared-data-file tie-break deterministic (or emit a warning when two sections declare conflicting delimiters for one file).

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V077 — CONFIRMED (medium)

ParseParameterFile iterates `for sectionName, values := range sections` (parameters.go:33) over the plain map returned by ParseConfigSections (parser.go:937-983, `map[string]map[string]string`, no key ordering). Parameters/DataSources are appended in that randomized order and the sole caller appends them verbatim with no sort (zip.go:545-555); `grep sort.` finds nothing in parameters.go. Shared-file tie-break: dataSourceByFile is keyed by lowercase basename and first-seen-wins (parameters.go:126-131) — buildParameterDataSource runs once, baking that section's delimiter into DataSource.Delimiter/columns (parameters.go:201-219). Two sections referencing one data file with different delimiters therefore get a map-iteration-order winner, and array order differs between identical imports. Chain holds.

</details>


#### 43. Legacy fallback scanner silently truncates: bufio ErrTooLong unchecked and in-string ");" 
**Where:** `pkg/convert/enterprisearchive/parser.go:310` · **Area:** AREA-12 · **Category:** error-handling · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

parseActionFileLegacy uses a default bufio.Scanner (64KiB token cap) and never checks scanner.Err() before returning nil error (parser.go:240-310), so a single source line over 64KiB stops the scan mid-file and every later function is dropped; ParseConfigSections/ParseUSRFile deliberately raise this limit for exactly this reason (parser.go:941-949) but the action-file scanner does not. Additionally the block terminator check strings.Contains(trimmed, ");") at parser.go:296 fires inside string literals, so a body line containing ");" truncates that call's parsed arguments.

**Failure scenario:** A VuGen script with a vendor macro (AST parse fails, normal per the fallback design) and a recorded one-line 100KB JSON body early in Action.c: the legacy scanner recovers only the functions before that line and returns success; the remaining ~30 requests are absent from the draft. The only signal is the generic 'syntax_error' parse gate from the AST failure, which misattributes the loss and gives the reviewer no hint that recovery itself was truncated. If the AST parse succeeds with zero functions while legacy truncates, there is no gate at all.

**Suggested fix:** Raise the scanner buffer as ParseConfigSections does, and record a distinct CSourceParseFailure kind (e.g. line_too_long / scan_truncated) when scanner.Err() != nil; make the block terminator string-aware.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V078 — CONFIRMED (medium)

parseActionFileLegacy builds a default bufio.Scanner (parser.go:240) with no Buffer() call — 64KiB token cap — and returns `parsed, nil` (parser.go:310) without ever checking scanner.Err(), so ErrTooLong on one long line silently ends the scan and drops all later functions. Contrast: ParseConfigSections deliberately raises the limit for this exact reason (parser.go:939-949). Legacy is the designated fallback whenever cc.Parse rejects the file (parser.go:170-201, comment cites vendor macros); partial recovery returns success (parser.go:199-201), and when astErr==nil with zero AST functions no syntax_error gate is added at all (gate only appended under astErr!=nil, parser.go:184-188). Terminator check `strings.Contains(trimmed, ");")` (parser.go:296) runs on comment-stripped raw text — stripCComments preserves string-literal contents — so a `);` inside a recorded body string truncates that call. Chain holds.

</details>


#### 44. findFile basename fallback picks an arbitrary file when basenames collide
**Where:** `pkg/convert/enterprisearchive/zip.go:1430` · **Area:** AREA-12 · **Category:** determinism · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

findFile's final fallback ranges over the files map matching by basename (zip.go:1430-1435) and returns the first hit, so with two archive entries sharing a basename (e.g. data/users.dat and backup/users.dat) the parameter-file and data-file resolution (zip.go:541, parameters.go:125, runtime_credentials.go:831) selects a nondeterministic winner per import, unlike findRuntimeArchiveFile which fails closed on ambiguity (zip.go:1494-1505).

**Failure scenario:** An archive contains users.dat at the root and an older backup/users.dat. Some imports of the same archive inline the stale backup rows into the data source, so runs consume different test data depending on which import happened to win the map iteration — with no warning that the reference was ambiguous.

**Suggested fix:** Sort candidates and either prefer the shallowest/exact-directory match deterministically or surface an ambiguity warning, mirroring findRuntimeArchiveFile.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V079 — CONFIRMED (medium)

findFile (zip.go:1418-1437): after a direct hit (1421) and a case-insensitive full-path scan (1424-1428), the basename fallback at zip.go:1431-1435 ranges over the unordered `files` map and returns the first entry whose basename matches — with `users.dat` and `backup/users.dat` both present, the winner is Go map-iteration random per import, and no ambiguity warning exists. Contrast standard confirmed: findRuntimeArchiveFile sorts candidates (zip.go:1465-1468) and fails closed on >1 basename matches (zip.go:1499-1505), proving colliding basenames are an anticipated archive shape. Affected callers verified: parameter-file content lookup (zip.go:541) and data-file resolution feeding DataSource.InlineData (parameters.go:125,134), so nondeterministically stale data rows can be inlined silently. Chain holds.

</details>


#### 45. Native inspection re-parses YAML O(requests x ancestors x passes) and runs the full candid
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


#### 46. Composed URL credential scrub targets use post-http.params query indexes against the autho
**Where:** `pkg/convert/bruno/native_url_sanitize.go:321` · **Area:** AREA-13 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

rewriteNativeURLTemplates (pkg/convert/bruno/native_url_sanitize.go:298-335) assigns pointer '/http/url/query/<index>/value' and scrub target {Kind:'url_query_value', Index:<index>} from the query string AFTER applyNativeHTTPParams (pkg/convert/bruno/native.go:818-876) has removed every raw-query pair whose name is managed by an http.params entry (including disabled ones) and re-appended enabled params at the end. applyNativeProjectedVariableSecrets (pkg/convert/bruno/native_sanitize.go:143-288) later applies that index via setNativeURLQueryValue (native_url_sanitize.go:442-480) to the AUTHORED /http/url string in the source file, whose '&'-split positions still include the removed pairs, so the index addresses the wrong pair whenever a managed/disabled pair precedes the credential pair in the authored query.

**Failure scenario:** Native import (output_mode=native_requests) of a request file with http.url 'https://api.test/login?debug=1&token=abc-{{tokenSuffix}}', http.params [{name: debug, type: query, value: '1', disabled: true}], and vars tokenSuffix: '12345'. Candidate-time query becomes 'token=abc-{{tokenSuffix}}' (index 0), so the composed-credential requirement records url_query_value Index 0. During Inspect's variable scrub, setNativeURLQueryValue rewrites index 0 of the AUTHORED query — 'debug=1' — so the stored sanitized source bundle becomes '?debug={{secret.LOGIN_QUERY_TOKEN_x}}&token=abc-{{tokenSuffix}}': the innocuous debug parameter now carries the token secret reference while the real credential pair keeps its authored composition. The manifest, collection view, and durable protected artifact are all rebuilt from these corrupted bytes and import succeeds silently. If the mis-indexed authored pair happens to have no '=' (e.g. '?flag&token=...'), the same flow instead hard-fails the whole import with 'URL query scrub index 0 has no value'. A secondary effect: the requirement ID/name embed the shifted index, so adding or removing an unrelated http.params entry churns the stable identity of an untouched query credential.

**Suggested fix:** Record scrub targets for raw-query credentials in the AUTHORED index space: compute the authored-parts index (or carry the authored offset through applyNativeHTTPParams by tagging surviving parts with their source index) and use that for both the '/http/url/query/N/value' identity and the url_query_value scrub Index. Alternatively, make setNativeURLQueryValue match by decoded pair name + occurrence rather than positional index.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V082 — CONFIRMED (medium)

Chain verified. pkg/convert/bruno/native.go:1537-1547: rawTarget passes through applyNativeHTTPParams BEFORE rewriteNativeURLTemplates. native.go:805-807/819-822: paramNames includes every query-type http.params entry (Disabled checked only at 840, not when building paramNames); lines 824-838 drop authored raw-query pairs with managed keys; 839-876 re-append enabled params at the end. native_url_sanitize.go:298-334: query index is computed on this composed string and recorded as scrub target {Pointer:"/http/url", Kind:"url_query_value", Index:index}. native_sanitize.go:226-248 applies the plan to the source file's document (authored /http/url, unmarshalled from file.Data), and setNativeURLQueryValue (native_url_sanitize.go:464-475) rewrites parts[index] of the AUTHORED query positionally; inspect.go:180-207 then rebuilds bundle/manifest/collection from the corrupted bytes. Requirement flows through nativeProjectedVariableRequirements (native_sanitize.go:31-33) because scrubTargets is non-empty. A managed/disabled pair preceding the credential pair shifts the index → wrong pair scrubbed, or hard-fail at native_url_sanitize.go:469-471 when the mis-indexed pair has no '='. No test mixes http.params with raw-query credentials.

</details>


#### 47. Empty-body captured events get a message_received assertion that deterministically fails
**Where:** `pkg/convert/eventhubscapture/eventhubscapture.go:128` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Convert counts empty-body capture records (eventhubscapture.go:94-96, warned at 141-143) but still attaches Assertions=[{Type: message_received}] to every produce action unconditionally (line 128). The eventhubs runtime evaluates produce assertions against the sent event (pkg/protocol/eventhubs/eventhubs.go:244 evaluateAssertions(action.Assertions, result, ev) where ev is the produced event), and message_received fails on len(ev.Body)==0 (pkg/protocol/eventhubs/helpers.go:253-255: "assertion message_received failed: empty event"). So the send succeeds but the action is scored failed.

**Failure scenario:** Import a Capture Avro file containing properties-only events (empty Body is legal in Event Hubs and appears in real captures; the importer even counts and warns about them). Each such event becomes an eventhubs_produce action whose message_received assertion fails on every iteration, in mock and real mode alike, so the imported script reports failed transactions for events that were sent successfully — inflating error rate and potentially tripping error-rate SLA gates.

**Suggested fix:** Skip the message_received assertion (or emit no assertion) for actions whose imported body is empty; alternatively use an assertion that verifies the send outcome rather than body non-emptiness for produce actions.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V089 — CONFIRMED (medium)

pkg/convert/eventhubscapture/eventhubscapture.go counts empty bodies (94-96) and warns (141-143) but attaches `Assertions=[{Type:"message_received"}]` to every produce action unconditionally (line 128); empty body yields action.Body="" (line 116). At runtime, EventHubs.produce (pkg/protocol/eventhubs/eventhubs.go:220-244) builds the outgoing event via buildEvent (392-423, Body=action.Body verbatim), sends it successfully, then returns evaluateAssertions(action.Assertions, result, ev) on the SENT event (line 244). evaluateAssertions (helpers.go:250-256) fails message_received when len(ev.Body)==0: "assertion message_received failed: empty event". So legally-empty properties-only capture events send fine but score failed every iteration. Chain fully verified; confirmed as claimed.

</details>


#### 48. Wildcard response keys like "2XX" parse as status 2 and emit an always-failing assertion
**Where:** `pkg/convert/openapi/converter.go:2426` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

firstSuccessStatus (pkg/convert/openapi/converter.go:2420-2437) accepts any 3-byte key starting with '2' and parses it with fmt.Sscanf(key, "%d", &status). Sscanf does not require consuming the whole input, so the OpenAPI 3.x wildcard range key "2XX" (and "2xx") yields status=2 with a nil error (verified empirically: key="2XX" -> n=1, err=nil, status=2). buildAction (line 1346-1350) then emits Assertion{Type: status_code, Expected: 2}, and successResponseSchema (replay_helpers.go:30-37) looks up responses["2"], which misses.

**Failure scenario:** Import an OpenAPI 3.x spec whose operations declare only wildcard response ranges (responses: {"2XX": ...}) — a shape emitted by several spec generators. Every converted action carries assertion status_code Expected=2; at replay the real status (200/201/...) never equals 2, so every request in the script is scored as a failed transaction on every run even though the server behaved correctly.

**Suggested fix:** In firstSuccessStatus, treat keys matching 2XX/2xx as status 200 for lookup purposes but either skip the exact status_code assertion (fall back to a 200-299 status_code_range assertion) or verify the Sscanf consumed all three digits (require key to be all digits before parsing). Also make successResponseSchema consult the wildcard key.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V088 — CONFIRMED (medium)

firstSuccessStatus (pkg/convert/openapi/converter.go:2420-2437) accepts any key with len==3 && key[0]=='2' (line 2426), then `fmt.Sscanf(key, "%d", &status)`. Sscanf does not require full-input consumption: on "2XX"/"2xx" it fills status=2 with err==nil (one verb, one operand matched), so the function returns (2, true). buildAction (converter.go:1333, 1346-1350) emits Assertion{Type:"status_code", Expected: 2}, which can never match a real 2xx response. successResponseSchema (replay_helpers.go:30-40) looks up responses["2"], missing the "2XX" entry (falls back to "default" or nil). Grep confirms no wildcard normalization anywhere in pkg/convert/openapi and firstSuccessStatus has a single caller. Every action from a wildcard-only spec fails replay. Confirmed.

</details>


#### 49. RAML converter leaves {param} URI templates unresolved and asserts status 200 on every met
**Where:** `pkg/convert/raml/converter.go:170` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

walkResource (raml/converter.go:148-193) emits actions with Target set to the raw RAML resource path — URI parameters like /users/{userId} are never substituted, parameterized, or even parsed (there is no uriParameters handling anywhere in the package), and baseUri templates like https://api.example.com/{version} likewise pass through (line 83). Every action also gets a hard-coded Assertion{Type: status_code, Expected: 200} (line 170) regardless of the method or the responses the RAML declares. Unlike every sibling importer, no requestimport finalization or import-review gate marks these actions as needing work.

**Failure scenario:** Import a typical RAML spec with /users/{userId}: get and a POST /users declaring a 201 response. The generated GET sends the literal path /users/{userId} (engine templates are {{...}}, so the braces go out on the wire) and receives a 404, and the POST that correctly returns 201 fails its hard-coded 200 assertion — every affected action fails replay with no review gate explaining why.

**Suggested fix:** Rewrite {param} segments to {{data.raml.param}} (or {{var.param}}) with a generated data source seeded from uriParameters examples/defaults, mirroring the OpenAPI templatePath approach; derive the expected status from the method's declared responses (first 2xx) instead of hard-coding 200; expand baseUri {version} from the RAML version field.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V090 — CONFIRMED (medium)

walkResource (pkg/convert/raml/converter.go:148-193) sets `Target: resourcePath` verbatim (line 166) and a hard-coded `status_code Expected: 200` assertion on every method (line 170). Grep over the package (converter.go, support.go only) finds zero hits for uriParameters, requestimport, importreview, or templatePath — no {param} substitution, no per-action review gate. baseUri passes through unexpanded (line 83). So GET /users/{userId} ships literal braces on the wire and a 201-returning POST fails its 200 assertion. Mitigation noted: support.go:17-19 statically documents "URI parameters are preserved as template literals" and the 200 assertion — but that is a support-matrix note, not an import diagnostic or gate, so replay still fails deterministically. Confirmed at medium.

</details>


#### 50. Doc-literal SOAP envelopes ignore schema targetNamespace and elementFormDefault
**Where:** `pkg/convert/wsdl/converter.go:1214` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

soapEnvelope (pkg/convert/wsdl/converter.go:1213-1222) binds the single tns prefix to FirstNonEmpty(op.BodyNamespace, doc.TargetNamespace) and renderXSDFields (pkg/convert/wsdl/converter.go:1496-1544) qualifies every body/header element, at every depth, as <tns:...>. The xsdSchema struct captures targetNamespace (line 116) and elementFormDefault (line 117) but neither field is read anywhere in the package (grep-verified). For doc-literal bindings soap:body has no namespace attribute, so tns is always the WSDL definitions targetNamespace even when the input element is declared in a schema with a different targetNamespace, and child elements are namespace-qualified even when elementFormDefault is unqualified (the XSD default). RPC and encoded operations are execution-gated (credential_projection.go:66-85) but resolved doc-literal bodies ship ungated as 'converted'.

**Failure scenario:** Import a common enterprise WSDL whose <types> schema declares targetNamespace="http://types.example.com" (different from the definitions targetNamespace) or omits elementFormDefault (unqualified default). Every generated doc-literal request wraps payload elements in the wrong namespace (or wrongly qualifies children), so on replay a validating SOAP server rejects each operation with an element-not-expected SOAP fault; with the default FaultHandling.FailOnFault=true every action in the imported script fails on every run until the operator hand-rewrites the envelopes.

**Suggested fix:** Track the declaring xsdSchema for each resolved element/type and emit its targetNamespace as the element's namespace (extra xmlns binding per schema namespace); honor elementFormDefault=unqualified by emitting local (unprefixed) child elements. Alternatively, when the element's declaring schema targetNamespace differs from the tns binding or elementFormDefault is unqualified, attach the existing wsdl_incomplete_xsd_type-style execution review gate instead of shipping the body as converted.

<details><summary>Verification evidence</summary>

**confirmed @ medium:** # V087 — CONFIRMED (medium)

soapEnvelope (pkg/convert/wsdl/converter.go:1213-1222) binds the single `tns` prefix to FirstNonEmpty(op.BodyNamespace, doc.TargetNamespace, "urn:service") — doc-literal soap:body carries no namespace attr, so tns is always the WSDL definitions targetNamespace. renderXSDFields (converter.go:1496-1544) emits `<tns:name>` for every child at every depth (lines 1516, 1519, 1526, 1533, 1541) unconditionally. xsdSchema.TargetNamespace/ElementFormDefault (converter.go:116-117) are decoded but grep shows no other reference in the package — only definitions.TargetNamespace (97/637/654/1214) is used. wsdlOperationReviewFindings (credential_projection.go:53-87) gates only unresolved-binding, rpc-style, and encoded-use; resolved doc-literal bodies ship ungated. Wrong-namespace/wrongly-qualified envelopes fault on validating servers. Confirmed as claimed.

</details>


## Low (32)

#### 51. collectorMetricStore never evicts snapshots for deleted collectors
**Where:** `pkg/api/collector_metrics.go:59` · **Area:** AREA-01 · **Category:** resource-leak · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

collectorMetricStore only has put/get (pkg/api/collector_metrics.go:59-70) and the only writers are the heartbeat handler (collector_metrics.go:157) and reader (line 180) -- verified there is no delete call anywhere (pkg/api/server.go:127,441 are the only other references). Deleting a monitoring collector removes its durable row but its last in-memory snapshot (up to maxCollectorHeartbeatBytes=256 KiB of decoded packs, collector_metrics.go:28-31) stays in the map until process restart, so memory grows monotonically with collector churn.

**Failure scenario:** A team provisions ephemeral collectors from CI (one per pipeline run, deleted afterwards) that each push a full 64-pack heartbeat before deletion. Over months, thousands of dead collector snapshots (~100-250 KB each) accumulate in s.collectorMetrics, adding hundreds of MB of unreclaimable heap to a long-lived controller until it is restarted.

**Suggested fix:** Add a delete(id) method and call it from the collector-deletion handler; optionally sweep entries whose ReceivedAt is older than a retention window as defense in depth.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V007 — CONFIRMED (low)

collectorMetricStore (pkg/api/collector_metrics.go:50-70) exposes only put and get; no delete method exists. Full-repo grep for collectorMetrics shows exactly four non-test references: server.go:127 (field), server.go:441 (init), collector_metrics.go:157 (put in heartbeat handler), collector_metrics.go:180 (get in read handler). handleDeleteMonitoringCollector (pkg/api/handlers_monitoring_collectors.go:153-179) deletes the durable row via DeleteMonitoringCollectorFromProject but never evicts the in-memory snapshot, so a deleted collector's last heartbeat payload (bounded per collector by maxCollectorHeartbeatBytes=256 KiB, collector_metrics.go:28) remains in s.collectorMetrics.snapshots until process restart. Memory grows monotonically with collector churn. Leak is real but slow and bounded per entry; low severity appropriate.

</details>


#### 52. broadcastEvent reads live ManagedTest fields after tr.mu is released, racing with locked w
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


#### 53. Run-calendar truncation keeps an arbitrary append-order subset instead of the events neare
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


#### 54. Browser diagnostics download fails outright past 5,000 events instead of returning a trunc
**Where:** `pkg/api/archive_browser.go:52` · **Area:** AREA-03 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

buildRunArchiveBrowserDiagnosticsContext returns an error once a run has more than maxBrowserDiagnosticEvents=5000 browser transactions (pkg/api/archive_browser.go:42, 52-53) or 128MiB of payload (:121-124), so handleDownloadRunArchiveBrowserDiagnostics responds 500 with no partial bundle — the limit converts a large-but-valid archive into a completely unretrievable diagnostics set via this endpoint.

**Failure scenario:** A 2-hour run with browser canaries emitting ~1 diagnostic transaction/second archives ~7,200 browser events. GET /api/v1/tests/{id}/archive/browser-diagnostics always returns 500 'browser diagnostics exceed bounded event limit of 5000'; the user gets zero screenshots/HTML/console evidence from the endpoint even though every event is individually small and the archive is intact.

**Suggested fix:** Stop collecting at the cap instead of erroring: mark bundle.Overview (e.g. TruncatedEvents count) and return the bounded bundle; prioritize failed transactions if truncating.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V020 — CONFIRMED (low)

pkg/api/archive_browser.go:42-53: once len(bundle.Events) >= maxBrowserDiagnosticEvents (5,000) the per-transaction callback returns fmt.Errorf — not a stop sentinel — and :121-124 does the same at the 128MiB payload cap. archive_loaders.go:505-507 propagates any callback error, so buildRunArchiveBrowserDiagnosticsContext returns nil, err (:127-129); no partial bundle survives. handlers_archive.go:243-253 wraps that into writeError 500 on GET .../archive/browser-diagnostics. A run with >5,000 browser-diagnostic transactions therefore always 500s on this endpoint with zero artifacts, though the archive itself is intact and other views remain. Bounded-limit-errors-instead-of-truncates; scope limited to one download endpoint — severity low.

</details>


#### 55. SOAP fault timeline bucket permanently drops by-code (or by-category) counts after a first
**Where:** `pkg/api/archive_report_aggregate.go:383` · **Area:** AREA-03 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

mergeArchiveSOAPFaultTimelineBucket nils a bucket's ByCode/ByCategory map whenever it is empty after a merge (pkg/api/archive_report_aggregate.go:383-388). Because incrementArchiveCount is a no-op on a nil map (:351-356), a minute bucket whose first fault carries only a category (empty code) sets ByCode=nil forever: every later fault code in that minute is silently uncounted while bucket.Total still increments, and vice versa for ByCategory.

**Failure scenario:** Within one minute a run records fault #1 with SOAPFaultCategory='client' and no code, then fault #2 with SOAPFaultCode='soap:Server'. The report's soap_fault_timeline bucket shows total=2, by_category={client:1}, and by_code omitted entirely — the soap:Server code disappears from the timeline breakdown (the run-level BySOAPFaultCode map is unaffected, so timeline and totals visibly disagree).

**Suggested fix:** Keep the maps non-nil during accumulation and nil empty maps once at finalize (where SOAPFaultTimeline is copied out), mirroring finalizeArchiveSOAPPayloadTimelineBucket.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V019 — CONFIRMED (low)

pkg/api/archive_report_aggregate.go:371-388: a new minute bucket is created with non-nil ByCode/ByCategory, but after every merge lines 383-388 set any empty map to nil. incrementArchiveCount (:351-356) early-returns when the map is nil. So a first fault with category-only (code=="", passes the :368 guard) leaves ByCode empty, which is nil'd; a later same-minute fault with a code increments nothing while bucket.Total++ (:380) still runs. No repair downstream: archive_report.go:327-338 merely copies buckets into report.SOAPFaultTimeline, and archive.go:490-491 tags ByCode `omitempty`, so by_code vanishes from JSON while total disagrees. Merge is invoked per transaction (archive_report.go:133). Chain holds; report-display inaccuracy only — severity low.

</details>


#### 56. Single-slot global Bruno inspect semaphore holds its exclusive section across the request-
**Where:** `pkg/api/handlers_bruno_import.go:232` · **Area:** AREA-04 · **Category:** performance-scalability · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

brunoInspectConcurrency is a fixed capacity-1 channel (pkg/api/handlers_bruno_import.go:149) acquired at the top of handleInspectBrunoImport (lines 232-238) BEFORE readBrunoInspectInput reads the request body (line 240) and before brunoconvert.Inspect (line 251) and the two encrypted artifact PutBytes writes (lines 405-414). The exclusive section therefore includes the client's upload over the network (limit 272 MiB per brunoImportMaxBodyBytes at pkg/api/route_catalog.go:603), so one slow-uplink client serializes every other Bruno inspection on the whole controller for the duration of their upload; queued requests wait indefinitely (no timeout, only client-context cancel).

**Failure scenario:** User A starts inspecting a 200 MB Bruno collection over a 10 Mbps uplink (~3 minutes of body transfer). User B's 2 KB single-.bru inspect request blocks in the semaphore select for those minutes with no server-side timeout, appearing hung in the dashboard; with three queued users the delays compound. The comparable enterprise-archive gate allows 2 concurrent conversions and returns 429 when busy (handlers_import_enterprise.go:22-35), while Bruno inspect both serializes to 1 and queues silently.

**Suggested fix:** Read and buffer the (already MaxBytesReader-bounded) body before acquiring the slot, so the exclusive section covers only conversion/artifact publication; optionally raise capacity to a small configurable N and fail fast with 429+Retry-After like acquireEnterpriseImportSlot instead of unbounded queuing.

<details><summary>Verification evidence</summary>

**downgraded @ low:** # V022 — Bruno inspect semaphore held across body read: DOWNGRADED (medium → low)

Code facts confirmed: `brunoInspectConcurrency = make(chan struct{}, 1)` (pkg/api/handlers_bruno_import.go:149); slot acquired at handlers_bruno_import.go:232-238 BEFORE `readBrunoInspectInput` fully reads the body (line 240 → :2976-3010, decodeRequestBody/io.ReadAll) and held via defer through `brunoconvert.Inspect` (:251) and both PutBytes writes; waiters queue in `select` with no 429 (contrast enterprise gate: cap 2 + immediate 429, handlers_import_enterprise.go:22-35). Route is production-wired (pkg/api/server.go:910); global-role editors skip the pre-handler ProjectLookup body read (middleware.go:379-381), and multipart resolution peeks only 256KiB (access_resolvers.go:1150-1158), so the upload genuinely occurs inside the critical section. Limit constant is at middleware.go:603 (finding miscited route_catalog.go:603).

Refuting guard the finding missed: the API server sets `ReadTimeout: 15 * time.Second` (pkg/api/server.go:561), which in Go bounds reading the ENTIRE request including body. A 200MB/10Mbps ~3-minute upload fails ~15s in; the handler errors and releases the slot. So one slow uplink can hold the slot ≤~15s, not minutes, and waits are not "indefinite". Residual real defect: serialization across conversion+artifact I/O with silent queuing, and waiters queued >15s hit their own expired read deadline and fail body reads with an opaque error instead of a 429. Not a regression of prior AUTHZ-1/SYNC-MERGE-1 (different symbols/issues).

</details>


#### 57. java_vuser launch gating performs a fleet-metadata DB read and placement simulation under 
**Where:** `pkg/api/distributed_execution_backend.go:641` · **Area:** AREA-05 · **Category:** performance-lock-scope · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

enrichGeneratorTargetSpecsWithLabels executes tr.fleetStore.ListGeneratorMetadata(0,0) — an unbounded full-table SQLite read (confirmed limit=0 means no LIMIT in pkg/storage/sqlite_fleet.go:458-468) — and is called from distributedJavaVuserGroupRunnerAvailableLocked (pkg/api/external_runner_gating.go:514) and distributedJavaVuserRunnerAvailableLocked (490), which run while the caller holds the global TestRunner mutex (call sites: testexecution.go:564, testcreation.go:575, launch_preflight.go:128, testexecution.go:3585). Blocking DB I/O plus a full placement simulation (chooseGeneratorShardsForGroup) inside tr.mu stalls every other API request that touches runner state for the duration of the query.

**Failure scenario:** Controller under load with SQLite on slow/contended disk (checkpoint in progress) and a few hundred generator-metadata rows. A user creates/starts a java_vuser test; the preflight holds tr.mu through the metadata query and placement simulation (tens to hundreds of ms). During that window every list-tests/live-metrics/status API call blocks on tr.mu, producing visible latency spikes correlated with java test launches. This is the repo's known locks-over-DB-I/O antipattern.

**Suggested fix:** Cache the labels-by-target index with a short TTL (like liveInventory) refreshed outside tr.mu, or snapshot the needed test fields and run the gating check before acquiring tr.mu.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V029 — CONFIRMED (low)

enrichGeneratorTargetSpecsWithLabels calls tr.fleetStore.ListGeneratorMetadata(0,0) (distributed_execution_backend.go:641); SQLite ListGeneratorMetadata adds LIMIT only when limit>0 (sqlite_fleet.go:458-468), so limit=0 is a full-table read. distributedJavaVuserGroupRunnerAvailableLocked calls it unconditionally (external_runner_gating.go:514) plus chooseGeneratorShardsForGroup placement simulation (551-561); reached via javaVuserRunnerLaunchConflictsForManagedTestLocked (240). All call sites hold tr.mu: evaluateStartGatesLocked contract "caller must hold tr.mu" (testexecution.go:544-549, call at 564), buildManagedTestLocked under tr.mu.RLock (launch_preflight.go:110-128; testcreation.go:575), and pending promotion holds the write lock explicitly (testexecution.go:3560-3590). Prior-report check: PERF_REVIEW_2026-07-06.md item 65 is a different defect (Run() serial dial/probe/script distribution under a shared 10s deadline, line 534), not this DB-read-under-tr.mu gating path — no regression, not a duplicate. Low confirmed (java_vuser launches only).

</details>


#### 58. Startup-failure cleanup stops assigned generators sequentially, so a wide-fleet failure ca
**Where:** `pkg/api/distributed_execution_backend.go:736` · **Area:** AREA-05 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

The cleanup closure in Run() iterates startedTargets sequentially, issuing stopGeneratorTest with a fresh b.timeout (default 10s) context per target (pkg/api/distributed_execution_backend.go:736-747). Every other stop fanout in this file is parallel (stopTestContext:1339-1360, forceStopDistributedTargets:568-584, commit:1209-1228). With up to maxDistributedTargetsPerRun=256 targets, a late startup failure (e.g. metrics-stream open failure at 1038-1042) during a fleet-wide network incident performs up to 256 sequential 10s-timeout stops (~43 minutes) before failTestBeforeRunAndLog marks the test FAILED.

**Failure scenario:** 200-generator run; all AssignWork calls succeed, then the network partitions before metrics streams open. failStartup -> cleanup(true) issues 200 sequential StopTest RPCs that each burn the full 10s deadline against unreachable generators. The test remains visibly INITIALIZING (not FAILED) for ~33 minutes, cannot be meaningfully interacted with, and holds its capacity accounting the whole time. Expected: the same bounded parallel fanout used by stopTestContext, completing within ~1-2 timeouts.

**Suggested fix:** Reuse the parallel stop-fanout pattern (goroutine per target, buffered results channel) inside cleanup, or cap total cleanup wall time with one shared deadline.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V030 — CONFIRMED (low)

Cleanup closure (pkg/api/distributed_execution_backend.go:736-747) iterates `startedTargets` in a plain sequential `for`, each iteration creating a fresh `context.WithTimeout(context.Background(), b.timeout)` (default 10s, lines 245-246) for `stopGeneratorTest`. `context.Background()` means the stop is not cancelable by executionCtx, so each unreachable generator burns up to the full timeout. `startedTargets` is populated per assigned shard target before AssignWork (line 981, deliberate ambiguous-commit tracking per comment 977-980), up to maxDistributedTargetsPerRun=256 (line 21). A late failure — e.g. metrics-stream open at 1038-1041 or prepare at 1046-1049 — triggers failStartup, which runs cleanup(true) BEFORE failTestBeforeRunAndLog (756-759), so the test stays INITIALIZING for O(N×10s). Contrast: stopTestContext fanout is goroutine-per-target parallel (1333-1360). Prior PERF_REVIEW item 65 (status open) covers the serial placement/distribute path — discovery is now worker-pool parallel (767-815) — but not this cleanup loop; distinct issue, not a regression.

</details>


#### 59. Evacuate endpoint pre-applies draining+cordon, then reports Applied=false on refusal while
**Where:** `pkg/api/handlers_fleet_evacuate.go:93` · **Area:** AREA-05 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleEvacuateGenerator marks the generator draining and persists the metadata cordon BEFORE computing evacuation impacts (pkg/api/handlers_fleet_evacuate.go:93-102). When EvacuateGenerator refuses (stateful protocol, no survivor capacity, pre-baseline window), the handler returns 409 with Applied=false and an error (117-125), but the drain state and cordon metadata remain applied — the response contradicts the actual state change, and the generator silently stops receiving new placements until an operator notices and uncordons.

**Failure scenario:** Operator POSTs /fleet/generators/gen-a/evacuate (not dry-run) while gen-a hosts a java_vuser group. Evacuation is refused ('java_vuser VUsers are stateful...'), response says applied=false. gen-a is nevertheless now draining+cordoned; subsequent runs skip it and fleet capacity views show it excluded, though the operator reasonably concluded from applied=false that nothing changed.

**Suggested fix:** Either run a dry-run evaluation first and only drain when at least one impact is applicable, or keep the pre-drain but report it honestly (e.g. applied_drain=true, evacuated=false) and document that a refused evacuation leaves the generator draining.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V028 — CONFIRMED (low)

Non-dry-run path pre-applies state: handlers_fleet_evacuate.go:93-101 sets drain "draining" (setGeneratorLifecycleState) and persists cordon metadata (syncGeneratorMetadataCordon) before any impact evaluation. EvacuateGenerator returns an error whenever any group is refused (distributed_resilience.go:334-336, "generator evacuation refused: ..."), including java_vuser/browser/sap_gui stateful groups (49-53 via 242-244), pre-baseline window (255-257), and no-survivor-capacity (267-272). On that error the handler sets response.Applied=false and returns 409 (handlers_fleet_evacuate.go:117-124) with no revert of the drain state or cordon metadata — nothing else in the file or handler path uncordons. So the response asserts nothing was applied while the generator persists draining+cordoned. API-contract mismatch confirmed; low severity appropriate (operator-recoverable via uncordon).

</details>


#### 60. isAIGeneratedDraft matches the bare substring "ai" in generated_by, misclassifying manual 
**Where:** `pkg/api/authoring_ai_guardrails.go:57` · **Area:** AREA-06 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

isAIGeneratedDraft (authoring_ai_guardrails.go:55-63) returns true when generated_by merely CONTAINS "ai" (or import source contains "llm"). generated_by is a free-form client-suppliable field (dsl.Script.GeneratedBy); values like "maintainer-tool", "main-suite-gen", or "aisha" trip the check. A misclassified draft gets an AIGuardrailReport whose diff_review and replay_before_save items are "blocked" (buildAIGuardrailReport:25-41), which drives AuthoringHealth to blocked and makes handleApproveAuthoringSession return 400 with the validation payload (handlers_authoring_sessions.go:542-561), so the session cannot be approved without attaching bogus AI evidence.

**Failure scenario:** A team's script generator stamps generated_by="maintainer-tool" (contains "ai") on a hand-maintained HTTP script. The authoring session shows ai_guardrails blockers ("AI-generated drafts require human diff-review evidence"), approval returns 400, and the operator must forge preserved diff_reviewed/replay_validated metadata to promote a script that was never AI-generated.

**Suggested fix:** Match structured markers only: generated_from == "ai_generated", import source == "ai_generated", or generated_by having the exact "apexload-ai-" prefix — drop the bare substring checks.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V036 — CONFIRMED (low)

pkg/api/authoring_ai_guardrails.go:57: `strings.Contains(generatedBy, "ai")` — bare substring; "maintainer-tool" (m-ai-ntainer) trips it. `GeneratedBy` is client-suppliable free text (pkg/dsl/types.go:79, json `generated_by`). Once misclassified, `buildAIGuardrailReport` marks diff_review/replay_before_save "blocked" absent preserved evidence (:28-29, aiGuardrailItem fail→blocked :48). `buildAuthoringHealth` maps aiGuardrails blocked>0 to Status "blocked" (pkg/api/authoring_capabilities.go:139-140). `handleApproveAuthoringSession` rejects with 400 when `AuthoringHealth.Status == "blocked"` (pkg/api/handlers_authoring_sessions.go:542-561). Chain complete: a manual script whose generator stamp merely contains "ai" cannot be approved without forging AI evidence. Low severity fits — workaround is renaming generated_by; no data loss.

</details>


#### 61. Capabilities endpoint reports the ai_generated authoring source as "planned" while the AI 
**Where:** `pkg/api/authoring_capabilities.go:533` · **Area:** AREA-06 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The authoring capability catalog marks Source "ai_generated" with Status "planned" and a note that "Generation guardrails are not complete" (authoring_capabilities.go, ai_generated entry near the file tail), while POST /api/v1/ai/drafts is fully implemented (handleAIDraftGeneration, ai_assistance.go:374-434, creates real authoring sessions) and GET /api/v1/ai/authoring/contract reports Status "active" with guardrails enforced (ai_assistance.go:265). Two server-owned discovery endpoints disagree about the same feature's availability, so a client that honors the capabilities contract hides or disables a working workflow.

**Failure scenario:** An external tool (or the dashboard's import-surface gating) reads /scripts/authoring/capabilities to decide which acquisition sources to offer, sees ai_generated=planned, and hides AI draft generation; users conclude the documented AI workflow is unavailable even though POST /api/v1/ai/drafts works and is audited as active.

**Suggested fix:** Update the ai_generated capability entry to active (or derive its status from the same policy check enforceAIPolicy uses) and drop the stale note.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V037 — CONFIRMED (low)

Contradiction verified between two served discovery endpoints. Capabilities catalog: pkg/api/authoring_capabilities.go:531-549 — Source "ai_generated", `Status: "planned"` (:534) with note "Generation guardrails are not complete" (:548); served at GET /api/v1/scripts/authoring/capabilities (pkg/api/server.go:854). AI contract: `buildAIAuthoringContract` returns `Status: "active"` (pkg/api/ai_assistance.go:265), served via handleAIAuthoringContract (:257-259). The workflow is genuinely live: POST /api/v1/ai/drafts routed (server.go:934, route_catalog.go:212) to handleAIDraftGeneration (ai_assistance.go:374+) which builds drafts and authoring sessions; guardrails are in fact complete (buildAIGuardrailReport + approval gate at handlers_authoring_sessions.go:542), so the "planned"/note text is stale. Low: discovery-metadata inconsistency only.

</details>


#### 62. Replay-session start endpoint lacks the credential-preserving-import echo special case its
**Where:** `pkg/api/handlers_script_debug.go:181` · **Area:** AREA-06 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** regression

handleStartScriptDebugScript (handlers_script_debug.go:110-129) detects that a request body script is the unchanged disclosure projection of a credential-preserving import (matchesStoredScriptDisclosureProjection) and substitutes the durable server-owned snapshot with trustedPreservedImport=true. handleStartReplaySessionScript (181-198) — the sibling endpoint for the same body shape — omits that check: when req.Script is present it always takes the untrusted path, where prepareTransientExecutableScript (handlers_replay.go:801-810) re-projects the already-masked script and discoverSecretBindingRequirements (secret_bindings.go:110-124) raises binding requirements for every [REDACTED] sentinel, failing the request with secret-bindings-required. Identical request bodies therefore succeed on the debug endpoint and 4xx on the replay endpoint. handleReplayScript (handlers_replay.go:101-112, out of scope) has the same gap.

**Failure scenario:** Operator opens a credential-preserving imported script (e.g. Bruno/LR import that retained credentials), makes no changes, and triggers a replay through a client that echoes the editor's GET copy in the request body (the same client behavior the debug endpoint explicitly accommodates). POST /scripts/{id}/replay-sessions returns 4xx demanding secret bindings for values that are actually present in the durable script, while the identical body on POST /scripts/{id}/debug/sessions replays fine. The user is misled into re-entering or binding credentials that already exist; replay of unchanged imports appears broken depending on which button they used.

**Suggested fix:** Extract the isCredentialPreservingImport + matchesStoredScriptDisclosureProjection substitution from handleStartScriptDebugScript into a shared helper and apply it in handleStartReplaySessionScript (and handleReplayScript).

<details><summary>Verification evidence</summary>

**downgraded @ low:** # V034 — verdict: downgraded (defect real, trigger narrower than claimed)

Chain confirmed statically. pkg/api/handlers_script_debug.go:118-126: debug endpoint substitutes the durable snapshot when `isCredentialPreservingImport(stored.Script) && matchesStoredScriptDisclosureProjection(script, stored.Script)`, setting trustedPreservedImport=true. Sibling handleStartReplaySessionScript (handlers_script_debug.go:181-192) omits this: req.Script present -> trusted stays false. Both funnel into handleStartScriptDebugRequest -> prepareTransientExecutableScript (handlers_script_debug.go:211-215; handlers_replay.go:773-810), whose untrusted branch runs maskScriptSecrets + discoverSecretBindingRequirements -> writeSecretBindingsRequired 4xx (handlers_replay.go:801-808). discoverRedactedValues raises one requirement per [REDACTED] occurrence (secret_bindings.go:208-235), and the GET projection of a preserving import contains sentinels (handlers_script_debug_test.go:966-968). Route wired: server.go:877 POST /api/v1/scripts/{id}/replay/sessions; documented (apidocs/api-endpoint-catalog.json:4248). matchesStoredScriptDisclosureProjection called nowhere else (grep). Test coverage pins only the debug endpoint (TestSavedImportedScriptDebugUsesDurableCredentialsForUnchangedProjection).

Refuting evidence on trigger: the dashboard never calls this endpoint — ScriptEditor.tsx:2767 routes both debug AND replay through /scripts/{id}/debug/sessions (the fixed endpoint) with the script echo (scriptEditorUtils.ts:730-756 always includes `script`); replayScript hook (useScriptApi.ts:509) is unused outside tests (grep). So "which button they used" scenario is wrong for the shipped UI; only external REST clients that echo the GET projection hit the 4xx, and the failure is fail-closed with an actionable requirements list. handleReplayScript (handlers_replay.go:101-112) shares the gap, also dashboard-unreached. Not a regression of prior fixed item 8 (java_vuser/session.go — unrelated file/subject).

</details>


#### 63. Replay progress never refreshes session activity, so an actively-running or breakpoint-pau
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


#### 64. Application/release 'optimistic' updates use a server-loaded token, so stale renames silen
**Where:** `pkg/api/handlers_applications.go:102` · **Area:** AREA-07 · **Category:** api-contract · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleUpdateApplication calls UpdateApplicationIfUnchanged(application, application.UpdatedAt) where application (and its UpdatedAt) was loaded inside the same handler (handlers_applications.go:81, 102); handleUpdateRelease does the same (handlers_applications.go:301, 322). The CAS therefore only guards the microseconds inside the handler, not the client's read-modify-write window — precisely the anti-pattern the data-pool handler documents and forbids ('Substituting the freshly loaded value here would only guard the in-handler window and let a stale editor silently overwrite changes', handlers_data_pools.go:289-291). The API contract is inconsistent: pools require the client token (400 without it), applications/releases ignore client staleness entirely.

**Failure scenario:** Operator A and operator B both load application 'Payments'. A renames it to 'Payments-EU' (persisted). B, still holding the old view, renames it to 'Payments-v2'; B's PUT reloads the row server-side, passes its own fresh UpdatedAt to the CAS, and silently overwrites A's rename — last-write-wins despite the storage layer supporting real optimistic concurrency.

**Suggested fix:** Accept updated_at in the request bodies (required, like data pools) and pass the client token to UpdateApplicationIfUnchanged/UpdateReleaseIfUnchanged, returning 409 on mismatch.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V044 — CONFIRMED (low)

handleUpdateApplication loads the row server-side (pkg/api/handlers_applications.go:81) and its request struct accepts only `Name` (lines 86-88) — no client concurrency token exists in the API. Line 102 then calls `UpdateApplicationIfUnchanged(application, application.UpdatedAt)`, passing the freshly loaded timestamp, so the CAS only guards the microseconds between line 81 and 102, not the client's read-modify-write window. handleUpdateRelease is identical (load at 301, self-token CAS at 322, Name-only body at 306-308). handlers_data_pools.go:288-294 documents this exact substitution as the forbidden anti-pattern and requires the editor's `updated_at` (400 if zero, line 291-293). A stale editor's rename therefore silently wins (last-write-wins) despite storage supporting real optimistic concurrency. Impact limited to name fields → severity low stands.

</details>


#### 65. Data-pool update mixes PATCH and PUT semantics: archived_at is absent-preserving but omitt
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


#### 66. Manifest configuration maps StartedAt from the stored record's ScheduledStartAt
**Where:** `pkg/api/test_manifest.go:375` · **Area:** AREA-07 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

manifestConfigurationFromTestRecord sets StartedAt: cloneTimePtr(record.ScheduledStartAt) (test_manifest.go:375). storage.TestRecord has no actual started-at field (StartTime holds CreatedAt per managedTestFromRecord at pkg/api/persistence.go:722 and buildManagedTestRecord at persistence.go:1396), so the scheduled launch time is presented as the start time.

**Failure scenario:** Create a run scheduled for 09:00 tomorrow. GET the test manifest today: initial_configuration.started_at reports tomorrow 09:00 although the run has never started; conversely an immediately-started run reports started_at null in the record-sourced configuration even while running. Manifest consumers comparing started_at across sections get inconsistent values.

**Suggested fix:** Emit ScheduledStartAt into its own field (or omit StartedAt from the record-sourced projection); if a durable started-at is wanted, add it to TestRecord.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V042 — CONFIRMED (low)

Chain verified. manifestConfigurationFromTestRecord sets StartedAt: cloneTimePtr(record.ScheduledStartAt) (pkg/api/test_manifest.go:375). storage.TestRecord has no started-at field (pkg/storage/storage.go:1174-1219: only StartTime, ScheduledStartAt, PendingSince, EndTime), and StartTime actually holds CreatedAt in both directions (buildManagedTestRecord, pkg/api/persistence.go:1396: StartTime: test.CreatedAt; managedTestFromRecord, persistence.go:~722: CreatedAt: record.StartTime). The parallel projection from the in-memory test uses the real test.StartedAt (test_manifest.go:352), so the two configuration sections of the same manifest disagree: a scheduled-not-yet-started run reports its future scheduled time as started_at in the record-sourced section, and an immediately started run (nil ScheduledStartAt) reports started_at null while running. Low severity is right — display/consistency wrongness in one manifest field, no state corruption. PRIOR-REPORT CHECK: M6/TierC-5 are pool-provenance gaps in the same file, unrelated and still open — not a regression.

</details>


#### 67. Default in-memory cache backend never evicts expired entries
**Where:** `pkg/api/cache_backend.go:345` · **Area:** AREA-08 · **Category:** unbounded-growth · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

memoryCacheBackend has no sweep goroutine: expired entries in m.entries are physically removed only when the SAME key is read via getJSON (cache_backend.go:483-487) or re-reserved, and m.rateLimits buckets are never deleted at all (cache_backend.go:390-395 only overwrites the same key) — so idempotency reservations (one per unique actor|method|path|Idempotency-Key, middleware.go:330) and atomic values accumulate forever on the default 'memory' backend.

**Failure scenario:** A long-running single-node controller serves automation whose HTTP client attaches a fresh UUID Idempotency-Key to every mutation (a common client-library default). Each mutation permanently adds one ~150-byte map entry; at 5 mutations/sec that is ~65 MB/day of unreclaimable map growth plus GC pressure, degrading a controller that runs for weeks. Per-IP OIDC login rate buckets from internet-facing scans accumulate the same way.

**Suggested fix:** Add a lazy sweep: on every Nth write (or a time-gated check inside the existing mutex), iterate and delete entries/buckets whose expiry/resetAt has passed; the maps are already fully guarded by m.mu so a periodic inline sweep is trivial.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V050 — CONFIRMED (low)

newMemoryCacheBackend (pkg/api/cache_backend.go:284-295) only allocates maps; no sweep/janitor goroutine exists anywhere in the file (grep for sweep/janitor/evict: none). ReserveIdempotencyKey (345-358) overwrites an expired entry only for the SAME key; a unique key never seen again stays in m.entries forever. getJSON (483-487) deletes expired entries only on a read of that same key. AllowRateLimit (390-395) resets a bucket in place but never deletes any m.rateLimits key, so per-IP buckets (auth_oidc.go:616, ci_auth.go:359) accumulate unboundedly. Memory is the default backend (cache_backend.go:152-156, 168-171), and the idempotency middleware creates one entry per unique actor|method|path|key scope (middleware.go:331). Unbounded growth claim holds; low severity appropriate.

</details>


#### 68. Remote probe persists remote-supplied Capabilities into the shared settings row without it
**Where:** `pkg/api/library_handlers.go:460` · **Area:** AREA-09 · **Category:** reliability · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

handleLibraryProbeRemote stores the remote's LibraryCapabilitiesResponse verbatim on the remote record (pkg/api/library_handlers.go:460) and persistLibraryRemotes serializes all remotes — including Capabilities — into the single library.remotes.v1 setting (pkg/api/library_federation.go:330-371). The only bound on the capabilities payload is decodeLibraryRemoteJSON's shared 128MB cap (pkg/api/library_federation.go:33, pkg/api/library_remote.go:168-180), which is sized for bundle exports, not a capabilities probe.

**Failure scenario:** A misbehaving or malicious federated remote returns a capabilities document with a 50MB ItemKinds/Operations array. Probe succeeds, the blob is written into library.remotes.v1, and from then on every loadLibraryState/loadLibraryLinksState caller (every remote list, import, link mutation) unmarshals tens of MB from the settings table, degrading all federation endpoints; with a CAS-less store the oversized row is rewritten on every remote mutation.

**Suggested fix:** Decode probe responses with a small dedicated limit (e.g. 64KB) and/or validate ItemKinds/Operations lengths before storing; store only the recognized fields.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V057 — CONFIRMED (low)

Chain verified: handleLibraryProbeRemote stores the remote-supplied capabilities verbatim (`pkg/api/library_handlers.go:460` `stored.Capabilities = caps`) then persists (464). remoteLibraryCapabilities (`pkg/api/library_remote.go:21-27`) decodes via remoteJSON → decodeLibraryRemoteJSON, whose only bound is the shared 128MB cap (`pkg/api/library_federation.go:33`; `library_remote.go:169-174`) sized for bundle exports. LibraryRemote.Capabilities is a serialized field (`library_federation.go:174` json:"capabilities,omitempty"), and persistLibraryRemotes marshals every remote incl. Capabilities into the single library.remotes.v1 setting (330-371; APIKey is cleared, Capabilities is not). loadLibraryState re-unmarshals the whole row on every call with no raw-unchanged short-circuit (230-268) and handlers call it per request (e.g. 431/442). Minor overstatement: a CAS path exists (356-363). Low severity appropriate.

</details>


#### 69. PowerBI row-push retry can duplicate rows under the SAME publish_id, defeating the documen
**Where:** `pkg/api/powerbi_client.go:116` · **Area:** AREA-09 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

doJSON (pkg/api/powerbi_client.go:116-171) retries up to 3 times on transport errors (lines 134-141) and on 408/429/5xx/401 (line 162). pushRows (lines 276-293) sends the rows POST through this retry loop with no idempotency mechanism (the Power BI push API has none), so a request that Power BI applied but whose response was lost (client timeout, connection reset after write) is re-sent and the same rows land twice with the SAME publish_id. Every documented duplicate-mitigation in this feature — powerBIPublishConflict's messages (pkg/api/powerbi_publish.go:139-160) and the force-republish flow — assumes duplicates are distinguishable by publish_id ("reports must keep only the latest publish_id per run"); intra-publish retry duplicates are invisible to that filter.

**Failure scenario:** A publish of a run with 300 transaction rows POSTs the single chunk; Power BI ingests it but the response exceeds the 30s client timeout (or a 500 is returned after the rows were committed). doJSON retries and the second POST succeeds. The dataset now holds 600 transaction rows carrying one publish_id; the local publish record says Complete with 300 rows. Executive reports that filter to the latest publish_id per run double-count every transaction/error/TPS aggregate for that run, silently, and neither force-republish detection nor the conflict messages can identify it.

**Suggested fix:** For the rows POST specifically, do not retry on transport errors/5xx after the request body may have been consumed (fail the publish as partial so the operator uses the documented force-republish path with a fresh publish_id); alternatively add a per-chunk sequence column (publish_id + chunk_index) so duplicated chunks are detectable in reports, and document the filter.

<details><summary>Verification evidence</summary>

**downgraded @ low:** # V056 — DOWNGRADED to low

Technical chain is real: `pkg/api/powerbi_client.go` doJSON retries up to 3x on transport errors (134-141) and on retryable statuses incl. 5xx/401 (162-168); pushRows (276-293) sends the rows POST through it with no idempotency token, so a chunk applied server-side but whose response was lost can land twice under the same publish_id. BUT the finding's framing — "defeating the documented dedupe", invisible/undocumented — is contradicted by `docs/POWERBI_INTEGRATION.md:82`, which explicitly documents this exact case: "Row delivery is at-least-once: a retried chunk whose first attempt actually landed ... can duplicate rows *within* one publish_id", calls it inherent/rare/bounded to one chunk, and names the recreate procedure as remediation. A documented, deliberate tradeoff with a recovery path, not a silent medium data-integrity defect → low.

</details>


#### 70. Marker handler swallows JSON body decode errors, producing misleading validation errors
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


#### 71. browser_ws_wait match hints are built from scrubbed WS payloads and can never match at rep
**Where:** `pkg/recorder/browser/convert.go:749` · **Area:** AREA-10 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

recordWSFrame scrubs captured WebSocket payloads before storage (pkg/recorder/browser/recorder.go:1794-1803: `text = r.opts.Scrubber(text)`, wired to scrub.Value by the manager at pkg/api/browser_recording_manager.go:299), and websocketWaitAction then uses that stored payload as the browser_ws_wait `match` value with match_mode contains (pkg/recorder/browser/convert.go:746-755). When the scrubber rewrote secret-looking content (tokens, session ids) the emitted match string contains the redaction marker, which the live server will never send, so the generated wait always exhausts its 10s timeout.

**Failure scenario:** Record with capture_ws_payloads=true against an app whose server frames carry `{"access_token":"eyJ..."}`; scrub rewrites the token in the stored frame; conversion emits browser_ws_wait with match containing the redacted form; every replay of the converted script stalls 10s on that action and the wait times out even though the application behaves identically to the recording.

**Suggested fix:** Detect the scrub redaction marker in the stored payload and either skip the match param (fall back to count/direction wait) or derive the hint from a stable structural fragment (e.g. the first JSON key names) instead of the value bytes.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V064 — CONFIRMED (low)

Chain verified. pkg/recorder/browser/recorder.go:1795-1798 scrubs the frame text before storage (`text = r.opts.Scrubber(text)`); the manager wires that to `scrub.Value` (pkg/api/browser_recording_manager.go:299), which replaces secrets with `[REDACTED]` (pkg/credential/credential.go:27) while keeping JSON valid. Conversion builds timeline entries from stored received-frame payloads (pkg/recorder/browser/convert.go:574-588), and websocketWaitAction (:746-751) emits `match` = truncateString(webSocketPayloadMatchHint(payload),160) with match_mode "contains" and timeout_ms 10000. A live server never sends the literal `[REDACTED]` marker, so a hint whose first 160 chars include a redacted value cannot match and the wait burns its 10s timeout. Minor scope caveat: only frames whose scrubbed region falls inside the 160-char hint are affected — consistent with severity low.

</details>


#### 72. Translated outer loop containing an untranslated nested loop splits into two loop blocks, 
**Where:** `pkg/convert/enterprisearchive/control_flow_import.go:375` · **Area:** AREA-11 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

structureFlowActions groups consecutive actions by exact loopID (control_flow_import.go:375), and tagSourceFlowMetadata only translates single-loop contexts (len(fn.Loops)==1, control_flow_import.go:250). Actions inside a nested loop carry loopID "outer>>inner" while outer-only siblings carry "outer", so one source loop body [A, inner-loop, B] imports as control_loop{A} xN, inner actions once (warned), control_loop{B} xN — two separate wrappers instead of one.

**Failure scenario:** C source: for (i=0;i<3;i++){ web_url("A"); for(j=0;j<atoi(lr_eval_string("{n}"));j++){ web_url("I"); } web_url("B"); }. Import executes A,A,A,I,B,B,B instead of A,I*,B repeated 3 times. Request counts for A/B are preserved and the inner loop's single-pass warning fires, but no warning explains that the outer loop was split into two blocks, so session-order-dependent servers (or correlation between A and B within one pass) behave differently from the source while the operator believes only the inner loop diverged.

**Suggested fix:** When a translated loop run is interrupted by actions whose loopID chain has the same first frame (prefix match on the outer ID), either extend the wrapper to include them as once-per-pass steps with a review tag, or emit an explicit warning that the outer loop was split into multiple blocks.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V069 — CONFIRMED (low)

tagSourceFlowMetadata stamps loopID as the full frame join (control_flow_import.go:249) and only sets loopExpr/loopCount when len(fn.Loops)==1 (line 250). So in [A, inner-loop I, B], A/B carry loopID "outer" with a count, I carries "outer>>inner" with neither. structureFlowActions groups by exact loopID equality (line 375): the run for A terminates at I, producing control_loop{A} xN; I is emitted unwrapped (runs once); B starts a NEW group at line 375 → second wrapper control_loop{B} xN. Execution becomes A,A,A,I,B,B,B instead of (A,I,B)x3. The only warning emitted is the line 271-273 "enclosed calls run once" notice for the nested display; nothing mentions the outer loop being split into two blocks. Chain holds; ordering divergence with partial warning → low.

</details>


#### 73. sourceControlArgs breaks positional integrity on empty arguments (C drops trailing args, J
**Where:** `pkg/convert/enterprisearchive/source_runtime.go:567` · **Area:** AREA-11 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

parseSourceRuntimeControl (parser.go:471-476) skips empty cleaned arguments but keeps original indices, leaving gaps in fn.Args (arg1, arg3 with no arg2); sourceControlArgs (source_runtime.go:569-577) iterates arg1..argN and BREAKS at the first missing key, silently dropping every argument after an empty one. The Java path (javaLRParsedFunction, java_import.go:5786-5790) stores empty strings instead, and sourceControlArgs skips them, SHIFTING later arguments into earlier positions.

**Failure scenario:** C action file contains web_set_user("bob", "", "auth.example.com:443"); — Args = {arg1:"bob", arg3:"auth.example.com:443"}. sourceControlArgs returns ["bob"], so applySourceWebSetUser (source_runtime.go:40-71) sets auth username only and the host mapping plus its preserved source_authentication evidence lose the host silently (no warning, no review flag). In a Java Vuser, lr-routed equivalents with an empty literal middle argument instead shift: position-based consumers read the third argument as the second (e.g. host consumed as password).

**Suggested fix:** Preserve positional slots: have parseSourceRuntimeControl store empty args (or have sourceControlArgs continue past present-but-empty argN keys and stop only on a truly absent key), and keep empty strings in the returned slice so consumers index positionally.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V068 — CONFIRMED (low)

C path: web_set_user routes to parseSourceRuntimeControl (parser.go:440,451). parser.go:471-476 skips empty cleaned args (`""` literal cleans to "" via cleanConfigValue, parser.go:616-622/1286-1298) but keeps 1-based indices, leaving Args={arg1,arg3}. sourceControlArgs (source_runtime.go:569-577) breaks at the first absent argN key, so `web_set_user("bob","","h:443")` returns ["bob"]; the CallArgs/RawArgs fallbacks (578-594) are skipped because args is non-empty. applySourceWebSetUser (source_runtime.go:40-71) then never sets host — silent loss, no warning. Java path: javaLRParsedFunction (java_import.go:5786-5790) stores empty strings for every position, and source_runtime.go:574-576 skips present-but-empty values, shifting later args left for positional consumers. Both halves hold; edge-case input, so low.

</details>


#### 74. Any parsed call name starting with "sql" classifies content as database ahead of web reque
**Where:** `pkg/convert/enterprisearchive/protocol_detection.go:321` · **Area:** AREA-12 · **Category:** protocol-detection · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

profileFromContent checks containsFunctionPrefix(counts, "sql") before counting web request functions (protocol_detection.go:321-329), so one custom C helper call named e.g. sqlEscape(...) or sqlite_log(...) in an otherwise pure web script makes the content profile 'database'. With a Web .usr this creates a metadata/content conflict that demotes an entirely convertible script to review-only (DetectProtocol:201-206); without a .usr the script is routed to ConvertDatabase.

**Failure scenario:** A Web HTTP/HTML archive whose Action.c calls a local helper sqlEscape(buf) once, plus 50 web_url calls, imports as review-only ('Metadata and content disagree') instead of executable — the headline import path is gated on a false conflict; the operator must manually confirm the protocol.

**Suggested fix:** Drop the bare 'sql' prefix (keep lrd_/lr_db), or check request evidence first and require multiple database-family signals before overriding it.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V081 — CONFIRMED (low)

protocol_detection.go:321-324: `containsFunctionPrefix(counts, "sql")` (prefix match over lowercased parsed call names, 608-615) returns databaseProfile before web request evidence is consulted at 329 (`requestCount`). Custom helper names do reach counts: flattenParsedFunctions (587-596) copies all ParsedFunctions unfiltered; the AST parser's expression-statement path (ast_parser.go:152-188) parses any statement-level call via reFuncCall (`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`, parser.go:127) with unknown names kept by the default case (parser.go:456-459) — so `sqlEscape(buf);` or `sqlite3_exec(...)` yields a "sql"-prefixed name. With Web .usr metadata, compatibleProfiles(web_http_html/http vs database/database) is false (541-561) → conflict (176) → review-only demotion (201-206). Confirmed at low.

</details>


#### 75. Explicit https:// proxy URLs on ports 3128/8080/8888 silently rewritten to http://
**Where:** `pkg/convert/enterprisearchive/runtime_config.go:4046` · **Area:** AREA-12 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

normalizeRuntimeProxyURL rewrites any https-scheme proxy URL whose port is 3128, 8080, or 8888 to http:// (runtime_config.go:4052-4059). That is a reasonable guess for endpoints synthesized from ProxyHTTPSHost/Port (where 'HTTPS' means proxied traffic, not TLS-to-proxy), but it also applies to an operator-authored ProxyServer=https://proxy.corp:8080 value, silently changing the transport to cleartext with no runtime issue or review note.

**Failure scenario:** A cfg declares ProxyServer=https://tls-proxy.corp:8080 where the proxy genuinely speaks TLS. The imported config silently becomes http://tls-proxy.corp:8080; replay attempts cleartext against a TLS listener and every request fails through the proxy, with the import claiming the proxy mapped cleanly.

**Suggested fix:** Restrict the rewrite to endpoints built from host/port fields (buildRuntimeProxyEndpoint) and leave explicit ProxyServer schemes untouched, or emit a proxy RuntimeIssue noting the scheme normalization.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V080 — CONFIRMED (low)

runtime_config.go:3674-3675 reads the operator-authored `ProxyServer`/`ProxyURL`/`Server` value directly into rc.ProxyURL; only when empty does it fall back to host/port synthesis (3677-3679). Line 3680 then unconditionally applies normalizeRuntimeProxyURL to whichever value won. normalizeRuntimeProxyURL (4046-4062) parses the URL and, when the scheme is https and Port() is "3128", "8080", or "8888" (4052-4057), rewrites the scheme to http and returns — the function returns only a string, emits no RuntimeMigrationIssue, and no caller adds one for this rewrite. So an explicit `ProxyServer=https://tls-proxy.corp:8080` is silently converted to cleartext, exactly as claimed. Severity low is fair: TLS-to-proxy is uncommon, but the failure (replay through proxy breaks) is silent at import.

</details>


#### 76. removeConsumedLiteralScriptParameters drops parameters still referenced by non-credential 
**Where:** `pkg/convert/requestimport/script_parameter_credentials.go:150` · **Area:** AREA-13 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

ReconcileProtectedScriptCredentialSources (script_parameter_credentials.go:106-155) calls removeConsumedLiteralScriptParameters (invoked at line 152, removal loop at lines 150-162), which deletes every script parameter whose name appears in a requirement's Value/SourceVariableNames without checking whether other, non-credential surfaces of the script still reference {{param.NAME}}; MaterializeSourceVariables only substitutes the parameter inside credential-bearing scalars, so any remaining reference in a non-sensitive field is left dangling after the parameter is removed.

**Failure scenario:** An imported script defines parameter password=['hunter2'] (single-value list) consumed into a protected requirement via auth.password governance; the same parameter is also referenced by a non-sensitive body field, e.g. "note": "prefix-{{param.password}}-suffix" (key 'note' is not credential vocabulary, and the value has no name=value shape so composedCredentialName does not govern it). After ReconcileProtectedScriptCredentialSources the parameter is deleted from script.Parameters, so replay interpolates the dangling {{param.password}} in the body as a literal/empty string instead of 'hunter2' — the request body silently changes on the wire with no finding or gate recording the alteration.

**Suggested fix:** Before deleting a consumed parameter, scan the projected script (marshal + collectParameterReferences) for remaining {{param.NAME}} references; if any remain outside governed surfaces, keep the parameter but redact its Values to credential.Redacted (matching redactCredentialSourceVariables) and emit a review finding instead of removing it.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V084 — CONFIRMED (low)

Code-level claim verified. pkg/convert/requestimport/script_credential_projection.go:118-134 builds `consumed` from requirement Value refs + SourceVariableNames; the kept-filter at 150-162 deletes every parameter whose name is consumed, with no scan of the projected script for residual {{param.NAME}} references on non-credential surfaces. credentialpolicy/projection.go:607-616 documents MaterializeSourceVariables applies only inside credential-bearing scalars, and body governance is name-vocabulary/composed name=value driven (projection.go:1308-1311, 1349-1376), so a non-sensitive field like note:"prefix-{{param.password}}-suffix" is neither governed nor materialized — its reference dangles after deletion. Reachability caveat: ReconcileProtectedScriptCredentialSources (script_parameter_credentials.go:106-155, sole call site of the removal at :152) has NO production callers in the snapshot — only script_credential_projection_test.go:1082 — so the wire-level failure is latent in an exported API, supporting low severity.

</details>


#### 77. orderReplayOps comparator is not transitive, so produce-before-consume ordering can silent
**Where:** `pkg/convert/asyncapi/converter.go:657` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** previously_reported_open

orderReplayOps (asyncapi/converter.go:637-663) sorts with a comparator that returns kindRank ordering only when a.topic == b.topic and false for any cross-topic pair. That relation is a partial order, and sort.SliceStable does not totalize it: when a same-topic produce and consume are separated by an op on another topic, the pair is never compared and the consume stays first. Verified empirically: input [consume(X), produce(Y), produce(X)] sorts to [consume(X), produce(Y), produce(X)] — the intended produce-before-consume invariant for topic X does not hold.

**Failure scenario:** AsyncAPI 2.x doc with channels sorted as: 'alpha' (subscribe, kafka binding topic X), 'beta' (publish topic Y), 'gamma' (publish, kafka binding topic X). The generated script runs kafka_consume on X before the kafka_produce that would create the message; on first iteration against the mock broker (or an empty real topic, even with the auto-set auto_offset_reset=earliest) the consume finds nothing and its message_received assertion fails, so the freshly imported script fails its first run for ordering reasons the importer claims to have fixed.

**Suggested fix:** Make the sort key total: group ops by topic first (e.g. sort by (targetProtocol, first-appearance index of topic, kindRank)) or do an explicit stable partition per topic instead of relying on a partial-order comparator.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V091 — CONFIRMED (low)

pkg/convert/asyncapi/converter.go:649-661: comparator orders by targetProtocol, then kindRank only when `a.topic == b.topic`, else returns false — a partial order, not a strict weak ordering. Static trace of Go's stable sort (insertion sort for small n): input [consume(X), produce(Y), produce(X)] → less(produceY,consumeX)=false, less(produceX,produceY)=false → no swaps, so consume(X) stays before produce(X), violating the produce-before-consume intent stated in the comment at 654-656 and enforced by kindRank (637-648). Reachable: called at converter.go:320 in the main conversion flow. Prior-report cross-ref (collectOperations, REFACTORING_OPPORTUNITIES_2026-06-27, status open) is an unrelated duplication note, not a fixed item — no regression. Severity low is appropriate (first-run assertion failure, easily diagnosed).

</details>


#### 78. allOf flattening drops member required lists (and scalar facets), silencing response body 
**Where:** `pkg/convert/openapi/converter.go:1163` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

resolveSchema's allOf merge (openapi/converter.go:1160-1177) copies only member Properties and (first non-empty) Type into the merged schema; member Required lists, Enum, Format, Example and Items are discarded. responseBodyAssertions (converter.go:1655-1680) reads resolved.Required to derive body_contains assertions, so any success-response schema composed via allOf — the standard base-plus-extension modeling pattern — produces zero body assertions even when members declare required properties. Sample bodies are unaffected (properties are merged), so this matches the 'allOf flattening dropping fields' defect class only for required/facets.

**Failure scenario:** Spec whose 200 response schema is allOf: [ $ref BaseEntity (required: [id]), {properties: {...}} ]. The importer advertises assertion generation, but the converted action gets only the status_code assertion — the documented body_contains assertions for required properties silently never materialize, weakening every imported script's validation without any diagnostic saying so.

**Suggested fix:** Union member Required (deduplicated) into merged.Required during the allOf merge; optionally also inherit Example/Enum/Format from the first member that defines them.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V092 — CONFIRMED (low)

pkg/convert/openapi/converter.go:1160-1177: allOf branch builds `merged := s`, clears AllOf, and copies from each resolved member only `Properties` (1169-1171) and first non-empty `Type` (1172-1174). Member `Required` (plus Enum/Format/Example/Items) is dropped. Consumer chain: replay_helpers.go:62 resolves the success-response schema via resolveSchema; converter.go:1349 calls responseBodyAssertions(successSchema); converter.go:1660-1663 returns nil when `resolved.Required` is empty. So allOf: [$ref Base(required:[id]), {properties:...}] yields merged.Required empty (only top-level s.Required survives) → zero body_contains assertions, silently, while properties/samples merge fine. Causal chain fully holds; low severity correct (weakened validation, no wrong request).

</details>


#### 79. Structured (non-string) authored example under an XML content type renders Go map syntax a
**Where:** `pkg/convert/openapi/converter.go:2128` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

renderGovernedExample (openapi/converter.go:2090-2136) handles XML content types only when the authored example is already a string (2120-2127). A structured example (YAML mapping / JSON object under application/xml) falls through to renderExample (2080-2088), which for non-JSON content types returns fmt.Sprint(value) — producing a Go-syntax body like 'map[user:map[name:bob]]' with no review code from this function. Downstream the shared XML projector receives non-XML bytes; depending on its malformed-XML disposition the action is either shipped with the garbage body or blocked — either way the authored example is destroyed rather than rendered or honestly gated at the source.

**Failure scenario:** YAML OpenAPI doc with requestBody content application/xml whose example is authored as a YAML mapping (a legal and common spelling). The converted action body is the Go fmt rendering 'map[...]' instead of an XML document; replay sends unparseable non-XML bytes (or the action is blocked by the shared projector with a confusing malformed-XML code), and the operator has to reconstruct the body by hand although the spec contained a complete example.

**Suggested fix:** For XML content types with structured examples, either render via the existing xmlForSchema-style writer (map keys to elements) or return a dedicated review code (e.g. unsupported_xml_example_shape) and an empty body instead of fmt.Sprint output.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V093 — CONFIRMED (low)

pkg/convert/openapi/converter.go:2120-2127: the XML branch of renderGovernedExample returns early only for `value.(string)`; a structured example falls through to renderExample (2128 → 2080-2088), whose non-JSON path is `fmt.Sprint(value)`. The doc is parsed with gopkg.in/yaml.v3 (converter.go:25, 691), so a YAML mapping example decodes into map[string]any → fmt.Sprint yields Go `map[...]` syntax as the body, with no review code from this function (2135 returns nil codes). Reachable via requestBodyExample at 1762/1775/1784 whenever preferredContentType is application/xml and the authored example is a mapping. Core defect (Go map rendering destroys the authored example) established; downstream XML-projector disposition was hedged by the finding itself and is not load-bearing. Low severity appropriate.

</details>


#### 80. Source-review masking via bare ReplaceAll of example values can shred the review copy
**Where:** `pkg/convert/openapi/converter.go:485` · **Area:** AREA-14 · **Category:** data-integrity · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

Convert masks sensitive source values in the review copy with strings.ReplaceAll(reviewContent, sensitiveValue, scrub.Redacted) for every value collected by sensitiveOpenAPISourceValues (openapi/converter.go:484-491). The collected values are examples/defaults/enums of credential-shaped fields with no minimum length or word-boundary check, so a short or common example value (e.g. a password example of "test" or "admin") replaces every occurrence of that substring across the entire document — operation IDs, paths, titles — corrupting the operator-facing source review. If the mangled text then fails the disclosure re-parse, disclosureSafeOpenAPISourceReview (673-740) falls back to omitting the review entirely. Executable output is unaffected; this is review-copy functional correctness (over-scrubbing), which is in scope.

**Failure scenario:** Spec with securitySchemes-adjacent schema {password: {type: string, example: "test"}} and operationIds like create_test_user. The source review shown for import approval displays create_[REDACTED]_user, /[REDACTED]s paths, etc.; in the worst case the YAML no longer parses and the whole source review is replaced by the omission banner, so reviewers cannot see what they are approving even though nothing sensitive required that.

**Suggested fix:** Apply a minimum-length floor (like scrub's own heuristics) and/or token-boundary-aware replacement for collected example values, and prefer the quoted-value redaction path (scrub.RedactQuotedValues) over bare substring ReplaceAll for short values.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V095 — CONFIRMED (low)

pkg/convert/openapi/converter.go:484-490: for each collected value, `reviewContent = strings.ReplaceAll(reviewContent, sensitiveValue, scrub.Redacted)` — bare substring replacement over the whole review copy, plus a JSON-escaped variant. Collection in `sensitiveOpenAPISourceValues` (749-928) admits any non-empty string (`add`, 757-762) from example/default/enum of credential-shaped fields (837-843) with no length floor or boundary check. So a password example "test" rewrites operationIds/paths/titles containing "test" to `[REDACTED]` (pkg/credential/credential.go:27). Worse, `disclosureSafeOpenAPISourceReview` (673-695), called at 637-644, replaces the entire review with the omission banner if the mangled YAML no longer parses (e.g. a scalar now starting with `[`). Executable output untouched; review-copy-only corruption. Severity low stands.

</details>


#### 81. Structured Postman URL objects drop the port (and hash) fields
**Where:** `pkg/convert/postman/converter.go:1156` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

The postmanURL struct (postman/converter.go:231-238) models raw, protocol, host, path, query, and variable but omits the Postman URL object's 'port' and 'hash' members. rawURL (1148-1173) therefore reconstructs structured URLs as protocol://host/path?query with no port. When 'raw' is present (the common case) the port survives via raw, but collections generated programmatically (schema allows raw to be absent) lose it.

**Failure scenario:** A collection item whose request.url is the object {protocol: "http", host: ["localhost"], port: "8080", path: ["api","v1"]} with no raw string (produced by tooling that writes structured URLs). The converted target is http://localhost/api/v1 — the request replays against port 80 instead of 8080, so deriveBaseURL and every action silently hit the wrong endpoint.

**Suggested fix:** Add Port (string) and Hash to postmanURL and include ":"+port after the joined host (and "#"+hash after query) in rawURL's reconstruction.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V094 — CONFIRMED (low)

pkg/convert/postman/converter.go:231-238: postmanURL declares Raw, Protocol, Host, Path, Query, Variable — no Port or Hash field, so those JSON members are silently discarded on unmarshal. rawURL (1148-1173): when Raw is non-empty it is used (1149-1155), so the common case survives; otherwise reconstruction is protocol:// + join(Host,".") + "/" + join(Path,"/") + "?" + query (1156-1172) with no port or fragment segment. A structured URL object {protocol, host, port:"8080", path} without raw therefore converts to a target on the default port. Failure scenario holds exactly as claimed; only affects raw-less structured URLs (schema-legal, tool-generated), so low severity is appropriate.

</details>


#### 82. Test-script pm.*.set literals apply retroactively to earlier requests
**Where:** `pkg/convert/postman/converter.go:821` · **Area:** AREA-14 · **Category:** correctness · **Verification:** 1 adversarial verifier confirmed · **Prior:** new

applyPostmanEventVariables (postman/converter.go:808-827) scans every request's event scripts for pm.environment/collectionVariables.set("k","literal") and writes the value into the shared vars map before any request is converted. The value therefore participates in variable resolution for requests that execute before the script that sets it, which inverts Postman's runtime scoping (a set takes effect only after its owning request runs). Response-derived sets are correctly modeled as extractors with ordering awareness (request_pipeline.go:150-159 registers proven variables only after the producing action), but literal sets have no such ordering.

**Failure scenario:** Collection variable env="prod"; request 3's test script does pm.environment.set("env", "staging"). Postman would run requests 1-2 against prod and 4+ against staging; the converted script resolves {{env}} to "staging" everywhere, so requests 1-2 replay against the wrong host/path with only an Info diagnostic ("translated deterministic pm variable assignment") hinting at it.

**Suggested fix:** Apply literal sets positionally: seed vars with collection/environment values, then fold each request's sets into the lookup only for subsequent requests (mirror the provenVariables ordering used for extractors), or at minimum escalate the diagnostic to a warning naming the affected earlier requests.

<details><summary>Verification evidence</summary>

**confirmed @ low:** # V096 — CONFIRMED (low)

pkg/convert/postman/converter.go:319 calls `applyPostmanEventVariables(sourceVars, requests, ...)` before any request is converted; 808-827 scans every request's event scripts (prerequest and test alike) for `pm.environment/collectionVariables.set("k","literal")` and unconditionally overwrites `vars[key] = match[2]` (821), clobbering collection/environment seeds (314-318). The resulting single lookup (320-321) is passed index-independently to `buildAction` for every request (419) and seeds script parameters (371), so request 3's literal set resolves `{{env}}` for requests 1-2 too — inverting Postman's after-the-owning-request scoping. Contrast: response-derived captures are registered into provenVariables only after the producing action (request_pipeline.go:150-158). Only an Info diagnostic (822) flags it. Severity low stands.

</details>


## Opportunities (non-defect)

16 verified improvement opportunities (Effort S/M/L/XL, Impact/Risk low/med/high), quick wins first. These do not enter the fix phase.

| ID | Title | Where | Area | Eff | Imp | Risk |
|----|-------|-------|------|-----|-----|------|
| O8 | GitHub sync jobs record per-file failures only as bare counters — no reason, no log, misle | `pkg/api/github_sync_service.go:103` | AREA-09 | S | med | low |
| O9 | Every browser-replay GET takes the manager write lock and runs a full prune sweep | `pkg/api/browser_replay_manager.go:794` | AREA-10 | S | med | low |
| O14 | matchHAR re-parses URLs O(refs x entries) times | `pkg/convert/salesforce/analyzer.go:440` | AREA-14 | S | med | low |
| O15 | Legacy singlestat panels are skipped although the accepted schema range routinely contains | `pkg/convert/grafana/converter.go:695` | AREA-14 | S | med | low |
| O11 | extractDatabaseBinds builds heuristic bind order from Go map iteration | `pkg/convert/enterprisearchive/database_import.go:551` | AREA-11 | S | low | low |
| O13 | Nine duplicated maxLogicalSecretNameBytes=249 constants shadow credential.MaxLogicalNameBy | `pkg/convert/bruno/native.go:2798` | AREA-13 | S | low | low |
| O16 | projectJSONExampleCredentials can never report unsafe, leaving a dead review code and guar | `pkg/convert/openapi/converter.go:2287` | AREA-14 | S | low | low |
| O1 | Schedule-occurrence tables have no retention while the sibling timeslot table does | `pkg/api/timeslot_retention.go:3` | AREA-02 | M | med | low |
| O2 | Schedule CRUD, approval resolution, and timeslot bind hold the global runner write lock ac | `pkg/api/scheduler.go:2548` | AREA-02 | M | med | med |
| O4 | Parallelize per-target script distribution and shard assignment during distributed startup | `pkg/api/distributed_execution_backend.go:939` | AREA-05 | M | med | med |
| O5 | Scenario list enrichment is O(scenarios x tests) under the runner read lock | `pkg/api/scenario.go:1067` | AREA-07 | M | med | low |
| O10 | Retire or quarantine the dead native Java lanes and their pipeline-reconstruction tests | `pkg/convert/enterprisearchive/java_import.go:622` | AREA-11 | M | med | low |
| O12 | Native import is all-or-nothing: one malformed request file rejects every candidate | `pkg/convert/bruno/native.go:276` | AREA-13 | M | med | med |
| O6 | Route access rules are linear-scanned with per-rule string splits, 3x per request | `pkg/api/route_catalog.go:399` | AREA-08 | M | low | low |
| O7 | User listing for scoped admins is an O(users x bindings) N+1 over the store | `pkg/api/handlers_auth.go:1673` | AREA-08 | M | low | low |
| O3 | Import response builder re-serializes the same script tree many times per request | `pkg/api/import_response.go:83` | AREA-04 | L | med | med |

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

---
*Generated by a multi-agent adversarially-verified review (Claude, 2026-08-02). Non-security scope; security is covered by separate dedicated passes. Partial coverage — see the Coverage section.*