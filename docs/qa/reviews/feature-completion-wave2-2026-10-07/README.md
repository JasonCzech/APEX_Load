# Feature completion Wave 2 — 2026-10-07

Status: implemented on `claude/feature-completion-wave1` and reviewed: each work package through independent review rounds until no finding stayed open, then once across the whole wave. Every finding is fixed, documented below, or accepted as a listed residual. The requirements these packages advance remain open; nothing here promotes support.

Wave 2 delivers eleven work packages of the [feature completion specification](../../../FEATURE_COMPLETION_SPEC.md), building on [Wave 1](../feature-completion-wave1-2026-10-06/README.md). Schema release 49 (`run_evidence_receipts`) is additive; the next free release is 50. The generator protocol gains `MetricsBatch.sequence_prefix_state_only` (field 33), `MetricsBatchTelemetry.test_dropped_subscriber_batches` (field 9), the `AcknowledgeEvidence` RPC, the `apexload-metrics-subscribed` stream header and the `browser_interaction_evidence_v1` capability; older peers keep working with the documented limits.

## Work packages

| WP | Requirement | What landed | Commit |
| --- | --- | --- | --- |
| C4 | TEL-2 | Exact or lower-bound accounting of dropped results, shed arrivals and subscriber drops per source epoch, with run totals that count each generator process once | c5bb4d612 |
| C3 | TEL-1 | Wall-time telemetry coverage: per-generator observed time from receipt-timed publication windows, required only from assignment to retirement, and a finalize-time account of healthy, degraded, breached and unknown time | 39b35e3a6 |
| C2 | TEL-1 | Clock calibration of every generator at least every 30 seconds; transport lag and sample age corrected and qualified, `unscored` run health when a decision cannot be scored | 4f27a710e |
| A3 | AUTH-1 | Dashboard scenario conflict recovery: 412/428 reconciliation with Load current baseline, field diff, Reapply and Reload, and browser checkpoints that record their baseline | 45d1e259a |
| A5 | AUTH-2 | Script authoring session: validation results bound to the draft's fingerprint or the stored revision, late results dropped | 41239d73c |
| B1 | NA-3 | RCA adoption of measurement series identity, joint action-dimension slices and associated imported datasets | c0686360e |
| B4 | NA-2 | Executable catalog derivations (ratios, counter delta and rate, baseline changes, moving statistics) on native archive, live, evidence and imported queries | f1d91b3e3 |
| D2 | BE-2 | Baseline comparison and RCA of qualified browser document metrics, with cohort labels and group mappings | 6a939bf69 |
| D4 | BE-3 | Crash reclamation of HAR working generations whose generator was killed, proven by recorded process identities | 67970483b |
| D5 | BE-4 | Managed transfer of native HARs into run archives with durable evidence receipts and generator acknowledgement | e328d94de |
| E3 | BI-1 | Opt-in interaction evidence: dispatched points, keyboard targets as role and hashed label, context, layout revision and declared post-action outcome | 81bdb4cff |

feaa2361a fixed a flaky engine test found while implementing B1. Review rounds fixed findings in 6daeaf809, d8905baf2, 0cfd9d741 and 4f635bd76, and the cross-cutting review in 308c53b8b, f0a91b686 and 30b90d4c6.

## Review

Each package was reviewed by an independent agent that read a frozen snapshot of the implementation commit against its specification. Fix commits were re-reviewed round by round, each round against the previous round's findings and anything the fix introduced, until no finding stayed open without a fix or a documented reason. Fixes were checked by removing them and confirming their tests fail.

- The first round raised 96 findings, 10 of them high. Among them: the generator's sequence-1 publication could be missed by a late metrics subscription (which also explained the flaky archive test noted while implementing C4); a distributed run finalized after a restart or adoption counted its whole window as observed; local runs dropped interaction evidence entirely; generator acknowledgements were never sent because the run's clients were closed before finalization; imported counter deltas never ran.
- The second found most fixes complete, A5, C4 and D2 clean, and 26 new defects (11 medium). Among them: a stored batch qualified on receipt could be scored across a later controller clock step; the first state-only publication of every distributed run announced a spurious `telemetry_recovered`; a re-checkpoint of a blocked resumed edit dropped its recorded baseline; every native admission error was retried as a busy slot; counter buckets read the pre-reset peak; a local run with a runtime-resource placement counted as distributed and 100% unknown; interaction capture waited two seconds for a target the gesture had removed.
- The third found C3, D4 and B4 clean and one medium defect: a HAR whose transfer failed was refunded to the transfer allowance but not to the run's HAR budget. The low ones included per-HAR rather than run-wide optional-evidence admission and a step check that ran at aggregation instead of publication.
- The fourth found every package clean; its notes (a test guard that could not fire, and an older fan-out-lag skew on clock steps) were fixed.

A final review read the whole wave through four lenses (storage and HA; API contracts and security; runtime and protocol compatibility; analytics and documentation). It found one high, four medium and about twenty low defects. The high one: on a generator the optional-evidence share was a quarter of the 256 GiB logical archive limit, so HARs could fill the disk until required archive records failed; optional evidence now shares one allowance fixed at its first admission from the free space actually available, on generators, on the controller's own capture and in archive merges. The medium ones: an imported counter's rate divided by the bucket width rather than the time between its readings; interaction routes bypassed the runtime-secret redactor; HAR reads held the HAR directory's lease while copying, so other sessions' closes timed out; and a generator wall-clock step went unseen until the next probe. Two further agents verified those fixes. The generator's published clock still re-anchored on any controller replica's inventory or placement heartbeat arriving between one run's clock probes (medium), and nine low items were only partly fixed: a deposed controller could still record a receipt between the new leader's reset and its first accept; acknowledgements were re-driven on every commit for swept or unreachable generators; the report verdict ignored unscored telemetry; the telemetry loss schema stayed opaque; one stalled generator could still spend the staging attempt one fetch deadline at a time; and several documents and an RCA label were inaccurate. f0a91b686 fixed them: the published clock re-anchors only while no test is assigned, and each receipt accept proves the execution lease term in its own transaction. A second verification found everything else resolved but two low items and three nits, fixed in 30b90d4c6: a near-full interaction record whose routes the run's redaction lengthened would have been refused whole at the sink (records are now split after scrubbing), and the documentation now says drift under 1 second never moves the published clock. Each fix has a test that fails without it.

Notable corrections from review:

- A generator marks a registration snapshot whose earlier sequences carried no metric window, and confirms the subscription before the controller activates the run, so an exact loss count no longer depends on winning a race with sequence 1.
- Clock qualification is re-checked at receipt, at aggregation and at publication against the controller's monotonic clock, so a wall-clock step can make a decision unscored but never a false breach.
- Counter derivations over imported data read each bucket's closing reading, and a bucket in which a reading falls is a `counter_reset` rather than a measured delta.
- Optional evidence (cross-filesystem HAR copies and the merged interactions sidecar) shares one allowance per archive merge, a quarter of the merge budget and of the free space above the floor, so required archive outputs always keep the rest.
- HAR-directory copies are retired only after the archive holding them is registered (generator) or finalized (controller).

## Documented deviations and open work

- **Authoring (A3, A5):** idempotency keys for creates from the dashboard (A6), session ownership and capability, policy and approval revisions in the script session (A8), and a TestHub integration test of a bindings save followed by a scenario save remain. A checkpoint without a revision resumes as an edit only while the listed scenario keeps the update time of the body it was built from.
- **RCA (B1):** imported series of the baseline run, v4 joint evidence (B5) and datasets whose partitions mix metrics or identities remain; the transaction identity projection applies in RCA only. Provider series IDs keep the collecting host, so a source moved between collectors starts new series.
- **Derivations (B4):** acknowledgement and settlement ratios have no counted evidence; there is no live message population; byte queries and controller-side imported derivations beyond counters and moving statistics remain; the dashboard does not author derivations yet.
- **Telemetry (C2–C4):** the dashboard WebSocket clock exchange (C5), clock quality in SQL history rows, report summaries and the dashboard (TEL-4), coverage durations in the report, dashboard and CLI, live coverage of in-process runs, pause handling and an end-to-end HA adoption test remain. The live API reports per-source summaries rather than per-source loss records, an assigned generator that never publishes counts as unknown coverage rather than loss, and run-level loss is not a separately persisted record.
- **Browser evidence (D2, D4, D5):** hosts sharing a HAR directory need distinct machine-id/host-name pairs; the real-driver crash test has not run on Linux; the transfer is controller-pulled with an explicit acknowledgement rather than generator-pushed; HARs are not cached for HA collection; safe exports omit HARs until D7; `har_synthetic` is accepted but not produced.
- **Interaction evidence (E3):** child-frame points and frame transforms (E4), a deduplicated keyboard-target table, drag and swipe ground-truth fixtures, and pattern and heatmap analysis remain; recorder-imported steps are `unknown` provenance until an importer emits consent.

## Accepted residuals

Reviewers raised these; each is low severity, fails safe, and stays open:

- **Coverage (C3):** spans are timed by the controller's wall clock at receipt, so a backward step of that clock during a run can overlap later spans with earlier ones and overstate coverage by at most the step; the clock-observation rate limit is wall-timed too.
- **Mixed versions (D5):** HAR transfer advertises no capability: a generator older than D5 declares no HARs, so its run archive does not record that HARs were captured, and a controller older than D5 leaves a newer generator's HARs in that generator's own archive (documented in the ownership guide).
- **Interactions (E3):** a source whose sidecar cannot be transferred marks the evidence incomplete without counting its interactions dropped; when HAR copies have used the merge's optional allowance, the interactions sidecar is the one omitted. A navigating key press without a selector keeps the focus it started with; it has no dedicated fixture.
- **Receipts (D5):** receipt rows outlive their run (at most 128 HARs and one sidecar per source per run) so tombstones keep refusing late uploads. Concurrent stagers on one volume each fix their optional allowance from the same observed free space, bounded by its quarter.
- **Telemetry (C2):** steps smaller than the 100 ms tolerance, one on each side of aggregation, can add up undetected; a de-escalation from breached to degraded is announced as a new degradation.
- **Published clock (C2):** drift under its 1-second tolerance is never corrected, so archived generator offsets can be up to 1 second from the generator's wall clock while their stated uncertainty is a few milliseconds.
- **Tests accepted without a dedicated case:** an end-to-end staging run showing optional evidence stopping at its allowance while later required writes succeed (each path's allowance is tested with injected free space); the interaction route fixture's custom redactor rather than the engine's bound one (E3); a TestHub bindings-save-then-save integration and a shape-only diff (A3); the default-build legacy RCA identifier formula and the gate's arrival order and 2 s wait cap (B1); a counter reading that falls across a missing instant or an empty bucket (B4, covered through the real worker otherwise); the evacuation and adoption assign/retire call sites (C3); the older crash-test ready handshake (D4); the controller-local post-consume removal (D5); the 25 ms start-read cap (E3).

## Gates

Final gates on 30b90d4c6 (macOS arm64, Go 1.26.5, Node 22, PostgreSQL 16 in a local container), each a full run:

| Gate | Command | Result |
| --- | --- | --- |
| API | `go test ./pkg/api/ -count=1` | pass (596 s) |
| Distributed, engine, CLI, scripts, QA docs | `go test ./pkg/distributed/... ./pkg/engine/... ./cmd/apexctl ./scripts ./docs/qa -count=1` | pass |
| Protocols (real Chromium and WebKit), storage, DSL, internal, analysis, RCA, generator | `go test ./pkg/protocol/... ./pkg/storage/... ./pkg/dsl/... ./pkg/internal/... ./pkg/analysis/... ./pkg/rca/... ./cmd/generator -count=1` | pass on a re-run (see below) |
| PostgreSQL storage | `go test -tags postgres_integration ./pkg/storage -count=1` | pass |
| PostgreSQL API | `go test -tags postgres_integration ./pkg/api -run Postgres -count=1` | pass |
| API with DuckDB | `go test -tags analytics_duckdb ./pkg/api -run 'RCA\|Analysis\|Native\|Dataset\|Derivation\|Imported'` | pass |
| DuckDB engine | `go test -tags analytics_duckdb ./pkg/analysis/duckdb -count=1` | pass |
| Dashboard | `npx tsc --noEmit` and `npx vitest run` | pass |
| Docs | `python3 scripts/check-docs.py` | pass |
| Browser end-to-end | `scripts/run-e2e.sh` with the script-session, authoring, scenario conflict, scenario builder, dashboard analysis, RCA investigation, archive references and baseline comparison specs, on a freshly built dashboard bundle, default and local-auth profiles | pass (27 tests default, 19 local-auth) |

The HA, RCA real-run and live-provider opt-in suites were not run for this wave.

## Retained failures and process notes

- `TestDistributedExecutionBackendStagesRemoteArchiveOnCompletion` failed once under heavy concurrent compile load during implementation (an archived epoch whose first sequence was 2). Round 1 traced it to the subscription race above, fixed in 6daeaf809.
- A full API run during implementation failed with "build failed": the disk had filled (the Go build cache had reached 94 GB). Clearing the cache recovered it; every later gate checked free space first.
- A3 left the QA dashboard's tracked spec count stale; A5 regenerated it.
- In the final gate run, `TestScriptContentRevisionConcurrentInitialization` (storage, older than this branch and untouched by it) failed once with `SQLITE_BUSY` while other packages ran alongside it; it passed 20 runs on its own and the whole gate passed on a re-run. It is a lock-contention flake under load.
- The D5 round-1 mutation check found fetch-time HAR verification to be an equivalent mutant at the time (the merge copy re-verified with the same outcome); the merge now moves rather than copies, so the fetch-time check is the one that counts.
- A background mutation run that was stopped mid-way left its mutant in a source file; it was found and restored before anything was committed, and later mutation runs ran one at a time in the foreground.
- The first round-2 e2e run used the committed static dashboard bundle after a failed build (a type error in an e2e spec); the build was fixed and every e2e run since rebuilds the bundle first.
