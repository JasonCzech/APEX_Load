# Feature completion Wave 3 — 2026-10-08

Status: implemented on `claude/feature-completion-wave1` and reviewed: each work package through independent review rounds until no finding stayed open, then once across the whole wave. Every finding is fixed, documented below, or accepted as a listed residual. The requirements these packages advance remain open; nothing here promotes support.

Wave 3 delivers nine work packages of the [feature completion specification](../../../FEATURE_COMPLETION_SPEC.md), building on [Wave 2](../feature-completion-wave2-2026-10-07/README.md). Schema releases 52 (`tests.joint_evidence_json`) and 53 (`feature_evaluation_jobs.subject_id` and its index) are additive; the next free release is 54. The generator protocol gains `WorkAssignment.joint_evidence_projection` (field 47), `MetricsBatch.joint_metrics_v4` and `joint_metrics_info` (fields 34 and 35) and the `native_joint_evidence_v4` capability; archived transaction rows (JSONL and both Parquet schemas, controller and generator) gain `vuser_id`, `iteration`, `interrupted`, `vuser_lifetime`, `elapsed_ms` and `ends_iteration`. Older peers keep working with the documented limits.

## Work packages

| WP | Requirement | What landed | Commit |
| --- | --- | --- | --- |
| D6 | BE-3 | Synthetic HAR 1.2 from retained network evidence (archived transactions and replay events), never inventing phases, sizes or content, with labelled completeness and native-HAR coverage | 40d3425dd |
| D7 | BE-3 | HAR listing and editor-only downloads, the HAR-inclusive archive export, native recording of browser replays with a private per-session copy, replay HAR and evidence downloads, dashboard and CLI access | a49419b88 |
| C5 | TEL-2 | Mergeable per-stage telemetry distributions (generator processing, transport, controller fan-out, sample age, browser render) and opt-in, advisory client render evidence | 2f053f03e |
| C6 | TEL-3 | Policy-compatible whole-run telemetry comparison in baseline comparisons and an explicit `telemetry_acceptance` re-evaluation under a named policy revision | c5e2f7807 |
| B6 | NA-3 | Correlation semantics per side (units, kind, population, statistic, conversion), sample exclusions by reason and lag unavailability reasons | 4bffebe20 |
| B5 | NA-3 | Joint evidence v4: a run's projection of up to three dimensions, exact tuples on the wire and in merges, merged live queries and a joint archive sidecar | 35403dd36 |
| E5 | BI-3 | Journey accounting from archived VUser iterations: completed, failed, aborted, skipped and censored attempts and a weighted failure index under a pinned journey policy | 5a86d09ec |
| E1 | BI-1 | Browser findings and qualified scores under a pinned browser policy, evaluated when a run's archive is attached, with webhook transitions | 95f1cef7d |
| A6 | AUTH-1 | `ScenarioDraft` as the one scenario assembly owner for TestHub, CreateTestModal, the queued-run revision editor and NewTestPage previews | e1c993b69 |

0ac39a2c9 (merged as 9163d1b22) rebuilt the embedded dashboard bundle on main, and bcfd949f1 merged main again (its UX review fixes, request and client error logging and the Event Hubs Capture Avro importer) before the final verification rounds, which read the merged code. Review rounds fixed findings in 4f258cce5, 85ed734cf, e48bac8b1 and c1f20741b, and the cross-cutting review in 724e3dba3.

## Review

Each package was reviewed by an independent agent that read a frozen snapshot of the implementation commit against its specification. Fix commits were re-reviewed round by round, each round against the previous round's findings and anything the fix introduced, until no finding stayed open without a fix or a documented reason. Fixes were checked by removing them and confirming their tests fail.

- The first round raised 62 findings, 6 of them high:
  - browser findings dispatched one webhook per transition, so the per-destination cap dropped all but two;
  - a run's findings were looked up only among the project's 50 newest evaluation jobs;
  - reused VUser IDs merged distinct VUser lifetimes in journey accounting (open workloads, evacuation, scaling);
  - merged joint queries were refused whenever the unrelated 128-key marginal caps had been reached;
  - generator telemetry stages were sampled only for the batches a throttled publication happened to re-read;
  - correlation's per-reason sample exclusions were never produced on real routes.
- The second round found D6, A6, C6, B6 and C5 clean. It found one missed medium in D7: replay HAR and evidence downloads were cut by the server's 60-second write timeout. E5 had three new medium defects, among them that think time and open source transactions shifted attempt intervals, and that a timing rule settled attempts that a ramp-down or scale-down had ended. B5 and E1 had lows.
- The third round found E1 clean and two medium defects:
  - B5 archived local windows twice on PostgreSQL, because history and store timestamps differed in precision;
  - E5 took a goal limiter that refused a slot without cancelling anything for a VUser ending on its own, so the open transactions were finalized as genuine failures.
- The fourth round found every package clean. Its one new low was fixed: a goal refusal inside a parallel branch failed the block like a genuine failure.

A final review read the whole wave through four lenses: storage and HA, API contracts and security, runtime and protocol compatibility, and analytics and documentation. It found 7 medium and 14 low findings. Two of the medium ones were raised by two lenses each, so there were five distinct medium defects:
- Deleting a run left its evaluations behind, so a CI run recreated under the same deterministic ID inherited the deleted run's browser findings and transition webhooks.
- A distributed run's joint evidence kept only the throttled published points, about one batch in N with N generators, and its publication identity omitted the source epoch.
- A queued run's revision dropped its joint-evidence projection.
- Generator resource-limit failures, such as a browser context closed at its HAR or memory limit, counted as journey failures.
- Browser replays recorded a native HAR by default. A replay therefore ran under the HAR size and memory limits that close the browser context, and could fail where it had passed before.

The fixes were then verified by two further agents, round after round, on a snapshot of the uncommitted working tree; they were committed only after a round found nothing.
- **Round 1** raised one medium: the Script Editor's new "Record native HAR" option did nothing, because the debugger runs script debug sessions (which never record) and the replay HAR download panel was never rendered anywhere. The option was removed, and the downloads moved to the recorder's replay verification and the recorder-export import's replay validation.
- **Rounds 1–9** raised that medium and 55 low issues in the fixes. Among them:
  - joint batches held for the sidecar were never released when no archive would be committed (leadership loss, failed or unstaged collection);
  - a HAR recorded by a script's own capture policy was left in the HAR directory by replays, debug sessions and classic replays;
  - the store's 3,600-row history cap silently trimmed the joint and interval-distribution sidecars;
  - database failures in policy lookups answered 404;
  - the CLI did not wait for a replay that was still closing;
  - many documentation corrections.
- **After round 3**, main was merged (bcfd949f1); rounds 5–10 read the merged code.
- **Round 10** found nothing in either lens.

Fixes were mutation-checked through round 3 (each fix's test failed without it); from round 4 the reviewers' test-adequacy checks replaced per-round mutation runs.

Notable corrections from review:

- Journey accounting settles a lifetime's last iteration only from a row the engine recorded because the iteration ended on its own; anything a stop, ramp-down, scale-down, goal limit or generator limit cut is censored, never failed.
- A run's browser findings are only its pinned-policy evaluations (a named revision is a separate subject), stored per run and deleted with it.
- Joint evidence v4 carries each accepted generator batch as its own source publication, identified by source, epoch, generation and sequence, within a bound of 1,024 batches or about 64 MiB between published points, so merged live joint queries and the archive no longer depend on the publication throttle; drops are counted and mark the archive truncated.
- Replay HAR copies belong to their session record, live in a private managed directory and are streamed, bounded and refused while the replay is still closing.

## Documented deviations and open work

- **HAR (D6, D7):** entries of a covered context whose evidence was dropped by the row's evidence budget are kept as unknown; the archive ZIP export carries native HARs only (the synthetic HAR is derivable from its transactions); replay HARs are process-local (lost on restart, not reachable from another replica); replay recording is opt-in through the replay API (`record_har`), a replay without it records only under the script's own capture policy (and serves that HAR as native), the Script Editor's debugger (script debug sessions) and classic replays offer no HAR (one the script's policy records is retired), and the recorder's replay verification and a recorder-export import's replay validation are where a replay's HAR and evidence are downloaded (the enterprise archive import's replay validation does not offer them yet); the end-to-end walk uses an HTTP run, so no native HAR is downloaded there (native paths are covered by Go tests and the opt-in real-Chromium qualification); native-HAR runtime secret suppression and the run-evidence redaction setting are not separately tested for downloads.
- **Telemetry (C5, C6):** generator-side stages are sampled per received batch (processing lag as one stage) rather than split into capture, aggregation and publish; controller receive and queue time are inside the fan-out stage; the dashboard WebSocket clock exchange is not built, so browser evidence is local receive-to-render only; digests are not merged across controllers. Telemetry comparison does not compare policy hashes directly, the `source_epochs` capability is lost when a run is adopted before finalize (fail-closed), intervals archived before `sample_age_unavailable` cannot tell sample-less windows apart, re-evaluation reports per-run counts and re-scores only the lag budgets; DOCX/JUnit exports and acceptance verdicts are C7.
- **Correlation (B6):** the native evidence panel does not render the semantics (API only); a dataset side reports bucket exclusions only.
- **Joint evidence (B5):** the projection is set only on `POST /api/v1/tests` (no scenario, schedule, CI or execution-plan path and no dashboard control); any valid `property:<category>` is accepted; there is no per-publication byte cap and digests are validated at query; archive panels read raw transactions, not joint rows; root-cause analysis does not read joint evidence v4 yet (B1 deferred it to B5, which did not adopt it). Batches carried on published points count toward the in-memory history limit, so a distributed run with a projection keeps a shorter live window in memory; at most 1,024 batches or about 64 MiB wait between published points (the oldest are dropped and counted, and the joint sidecar is then marked truncated); a fleet whose points carry no native evidence (a generator without it) has no live joint view and keeps only the latest batches for the archive; a distributed run this controller did not hold from its start (adopted after a failover, or reloaded after a restart) has its joint sidecar marked truncated; the store keeps the newest 3,600 interval rows per run, so a run that reached that many has its joint and interval-distribution sidecars marked truncated; a joint sidecar first written after the held generator batches were released (by a refresh after its write at finalize failed, or by a recovery archive after this controller collected none; they are released at commit, when no archive is collected here, and on leadership loss) is marked truncated; and merged live version 2 and 3 queries over several generators still see only the throttled published windows, so most of their buckets are gaps.
- **Journeys (E5):** with explicit transactions, a last iteration that ended on its own with no transaction open, and a fail-fast failure outside any open transaction, are censored rather than settled; journeys are accounted on request, not persisted; example attempts carry no browser evidence links; revenue and conversion models are not built.
- **Browser findings (E1):** automation/selector failures are not classified; findings link no HAR entries, raise no incidents and resolve only within one run's evaluations; the automatic evaluation is queued once, best effort; findings are not cited by RCA nor projected live; `telemetry_acceptance` results (one or more runs each) are not indexed by run, so they outlive a deleted run until the project is deleted.
- **Scenario assembly (A6):** the round trips are owner-level, not component-level, and do not exercise server normalization; NewTestPage's launch is not round-tripped; TestHub's load-model chart and CreateTestModal's chart still compute their own curves; TestHub's flat form and NewTestPage's override model remain editing views over the owner; no surface offers a reorder control.

## Accepted residuals

Reviewers raised these; each is low severity, fails safe, and stays open:

- **Journeys (E5):** tests accepted without a dedicated case: carried-transaction refusal rows at the loop top, a genuine failure after a soft stop, a carry through `lr_exit` via Run, stop and scale-down through the real multi-group engine, and the 2-million-row archive scan budget (a constant).
- **Joint evidence (B5):** the evacuation and Start-probe call sites of the shared capability helper have no end-to-end test (the helper is unit-tested); loss counters are run-cumulative, so every window after the first loss is incomplete; a joint sidecar rewritten by a refresh after its write at finalize failed is covered by its invariant (released batches count as dropped, so the rewrite is truncated), not by a failed write through the whole finalization pipeline.
- **Telemetry (C5):** the client-render report's no-history-clone path has no regression test (verified by review).
- **Scenario assembly (A6):** the TestHub timeline track has no component test (the owner's projection is tested).
- **Replay HAR (D7):** the e2e walk does not download a native HAR.

## Observations

- A local run's merged live native query marks the bucket holding the run's terminal state-only history point as a gap (that point has no native evidence). This predates Wave 3 and applies to every merged live query over a completed local run's last bucket; it was left unchanged.
- C5 had placed one route helper's doc comment above another's function; fixed in round 1 with gofmt-unclean files from Waves 1 and 2 (`pkg/engine/replay.go`, `pkg/analysis/points.go`, `pkg/storage/termination_postgres_integration_test.go`).
- The durable-write guard took the joint archive's `ForEachNativeIntervalEvidence` (a read) for a write; "ForEach" was added to its read prefixes.
- `TestBrowserActualInteractionOutcomesTargetsAndNavigation` (real Chromium) timed out once while five packages ran concurrently; it passes alone and in a full package run.
- The Go build cache reached 71 GB mid-cycle (3.4 GB of disk left); it was cleared and every later gate checked free space first.
- `TestPowerBIPublishHandlerEndToEnd` failed once with "archive not found" in a full API run that overlapped the dashboard suite: it publishes as soon as the run is COMPLETE without waiting for the archive to be attached. It passed 5 of 5 in separate processes and in the rerun gate; this is a pre-existing test race.
- `TestUppercaseOpenTPSValidationExecutesAsOpenWorkload` (engine) exceeded its 900 ms wall-clock bound once (943 ms) while reviewer agents ran; it passes 5 of 5 alone. The engine changes in this wave do not touch open-workload pacing.
- The verification rounds read snapshots of the uncommitted working tree, so the fixes were committed once (724e3dba3) after a clean round; a tagged stash carried them across the merge of main.

## Gates

Final gates (macOS arm64, Go 1.26.5, Node 24, PostgreSQL 16 in a local container). Each row says which code it ran on: the core gate ran on the committed fixes; the other suites ran on the merged tree after the last change to the code they cover (later changes were confined to `pkg/api`, tests and comments).

| Gate | Command | Result |
| --- | --- | --- |
| API | `go test ./pkg/api/ -count=1` | pass (748 s), on 724e3dba3 |
| Distributed, engine, CLI, scripts, QA docs | `go test ./pkg/distributed/... ./pkg/engine/... ./cmd/apexctl ./scripts ./docs/qa -count=1` | pass, on 724e3dba3 |
| Dashboard typecheck | `npx tsc --noEmit` | pass, on 724e3dba3 |
| Docs | `python3 scripts/check-docs.py` | pass, on 724e3dba3 |
| Dashboard tests | `npx vitest run` | pass (6,442 tests), merged tree after the last dashboard change |
| Protocols (real Chromium and WebKit), storage, DSL, internal, analysis, RCA, generator | `go test ./pkg/protocol/... ./pkg/storage/... ./pkg/dsl/... ./pkg/internal/... ./pkg/analysis/... ./pkg/rca/... ./cmd/generator -count=1` | pass, merged tree |
| PostgreSQL storage | `go test -tags postgres_integration ./pkg/storage -count=1` | pass, merged tree (storage changed only in comments afterwards) |
| API with DuckDB | `go test -tags analytics_duckdb ./pkg/analysis/... ./pkg/api -run 'Test(Analysis\|Native\|Parquet\|Container\|Lagged\|Bucket\|Joint\|IntervalDistributions\|RCA)'` | pass, before the merge of main (analytics untouched since) |
| Browser end-to-end | `scripts/run-e2e.sh` with the browser HAR, journeys, browser findings, telemetry re-evaluation, telemetry stages, debugger experience and settings monitoring specs, on a freshly built bundle, default and local-auth profiles | pass (15 and 1 skipped default, 16 local-auth), merged tree after the last dashboard change |

The PostgreSQL API suite (`go test -tags postgres_integration ./pkg/api -run Postgres`), the HA, RCA real-run and live-provider opt-in suites, and the real-Chromium replay HAR qualification were not run for this wave.
