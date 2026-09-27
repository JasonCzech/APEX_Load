# Node-failure readiness: first implementation slice

Date: 2026-09-27. Branch: `codex/node-failure-readiness`.
Scope: initial fixes from [the implementation plan](../../NODE_FAILURE_READINESS_PLAN.md).
These results do not qualify combined worker/controller recovery for production.

## Changes

- Liveness reads locally held settings and cached generator inventory. It no longer
  refreshes settings from the database or performs generator RPCs. Stale inventory
  is omitted. The existing bounded durable-write bypass and runner-lock watchdog
  remain in place; an indefinitely wedged controller does not gain an unlimited
  healthy response.
- PostgreSQL archive provenance uses the exact microsecond representation produced
  by pgx. Archive identity and CI evidence checks accept older nanosecond manifests
  using that representation. Test ID, project ID, zero-timestamp compatibility,
  and SQLite's exact timestamp comparison retain their existing semantics.
- Local, EKS, on-prem, on-prem autoscaling, and Azure Monitor KEDA trigger lists now
  retain the reservation floor already present in AKS and Helm. Azure Monitor uses
  the same authentication reference as its other triggers. This protects retained
  StatefulSet ordinals; it does not reserve unused recovery pods.

## Validation

| Check | Result |
|---|---|
| `go test ./...` | Passed; API package completed in 344 seconds |
| Focused health, timestamp, HA archive, and CI archive tests | Passed |
| Focused race tests for health, timestamps, and provenance | Passed |
| Real PostgreSQL 16 archive reload | Passed, including microsecond boundaries and same-ID reuse rejection |
| Real PostgreSQL plus SeaweedFS 4.17 S3-compatible storage | Passed, including corrupt S3 manifest checksum and same-ID reuse rejection |
| KEDA reservation-floor parity tests | Passed |
| Kustomize render: local, local-kind, on-prem, on-prem HA, on-prem autoscaling, AKS, AKS HA, AKS Azure Monitor, EKS, EKS HA | Passed |
| Helm HA render with ordinary Prometheus and Azure Monitor | Passed; exactly one reservation-floor trigger in each |
| Documentation consistency and diff whitespace | Passed |

The integration tests create an isolated PostgreSQL schema and use fresh database
connections and HA server instances for the archive HTTP requests. S3 cases upload
an artifact and its SHA-256-bearing manifest, remove the writer's local directory,
and fetch through the application. They cover nanosecond fractions 135, 999, and
001 around a microsecond boundary, timezone equivalence, canonical new provenance,
older manifest compatibility, same-ID reuse, and rejection of an incorrect S3
manifest checksum. Controller construction/reload is exercised; an actual elected
leadership transfer is a separate fault-matrix gate.

Disposable PostgreSQL and SeaweedFS containers use fresh credentials and loopback
ports and are removed after each run. The S3 endpoint used a wildcard DNS name
resolving to loopback because the existing adapter uses virtual-hosted bucket URLs.
No existing Docker Compose or Kubernetes workloads were changed by these tests.

To rerun the integration cases against disposable services, set
`APEXLOAD_TEST_POSTGRES_URL`. For S3, also set `APEXLOAD_TEST_ARCHIVE_S3=1`,
`APEXLOAD_ARCHIVE_BUCKET`, `APEXLOAD_ARCHIVE_REGION`, `APEXLOAD_ARCHIVE_ENDPOINT`,
and the standard AWS test credentials. The bucket must exist and its virtual-hosted
name must resolve. Run:

```sh
go test -tags postgres_integration ./pkg/api \
  -run 'TestPostgres(S3)?ArchiveIdentityAfterReload' -count=1 -v
```

## Remaining release gates

The complete deployed fault harness, ten-minute dependency outage deployment check,
reserved spare-capacity admission, combined-loss checkpoint/fencing recovery, and
durable terminal partial-result reporting remain open. The guarded fault-driver
foundation, scoped KEDA signals, storage runbook and terminal historical-sample labels
are implemented in the review cycles below. The original two recorded S3 archives have not yet been replayed through
an updated deployed controller. There has been no repeat of the multi-node fault
matrix with these binaries, and no multi-host, CSI, PostgreSQL-primary, or Redis-primary
failover qualification.

## Review/fix cycle

The first review found that the filesystem manifest loader ignored a persisted
checksum and that a future-dated inventory snapshot could be reported as current.
The loader now verifies a nonempty catalog checksum before decoding the manifest;
legacy records without a checksum remain readable. Health capacity observations
reject future timestamps as well as expired timestamps. The integration regression
now rejects corrupt catalog checksums for both filesystem and S3 archives.

The S3 integration test also registers cleanup before upload and bounds the upload
attempt, including failure paths. A second source review found no additional issues
in this slice. Post-review validation passed: API full suite (370 seconds), focused race tests,
PostgreSQL/filesystem/S3 integration checks, deployment tests, and documentation checks.
The full-suite inventory guard found an undocumented integration switch; its entry
was added to `docs/qa/TEST_PROGRAM.md` and the guard passed on rerun. Fleet-scoped KEDA
demand remains a separately tracked implementation batch.


## Fleet autoscaling review/fix cycle

Fleet demand now comes only from a reconciled execution leader, carries an installation
ID and expires at the earlier of 30 seconds or its lease deadline. Every shipped KEDA
query filters expiry and selects that fleet before deduplicating with `max`. Missing
observations fail the metric instead of representing zero demand. Legacy diagnostic
metrics remain available. Total demand includes queued stage/adaptive admission peaks.

Review fixed stopped/shakeout demand disappearing too early, under-reported queued
peaks, misplaced ConfigMap identity wiring, and cloud renderers that broke inherited
HA overlay paths. Renderers now expand overlays before replacing their placeholders.
A second review found no further issues in this slice. Reserved recovery capacity and
combined-loss adoption remain separate unfinished work.

Validation: focused API tests passed (1.433 seconds); focused race tests passed
(3.055 seconds); deployment tests and the complete scripts suite passed (24.693
seconds for scripts). Real Prometheus v3.12.0 `promtool test rules` passed all six
query families, duplicate HA, stale observations, independent fleets and expiry.
The later attempted repeat could not start because Docker Desktop's containerd
metadata filesystem became read-only; this is a local runtime blocker, not a passing
repeat. No Docker restart or cleanup of shared workloads was attempted.

The broad API run found one existing asynchronous-test race: the tracing test read
the exporter immediately after the start response, before execution ended its span.
It now waits for that ended span with a five-second bound, preserving all trace,
parentage and disclosure assertions. Thirty repeated runs passed (2.434 seconds).
The broad API rerun is recorded separately once complete.


## Fault-driver and storage review/fix cycle

The storage runbook was checked against the local, AKS, EKS, on-prem and Helm storage
contracts and generator epoch/supervision persistence. It distinguishes same-identity
reattachment from new-identity replacement and leaves CSI/zone RTO/RPO unqualified.

The repository fault driver now refuses unowned namespaces, mismatched kind clusters,
control-plane victims, stale/future observations, duplicate shard ownership, absent
KEDA demand and unsustained target traffic. It restores the immutable container ID
on timeout, interruption and failed recovery. Review fixed a false-pass risk by
requiring sustained traffic while the failed node remains stopped, reset the target
comparison at injection, and removed arbitrary nested assignment fields from evidence.
Fresh input generation refuses overwrite, produces a new CA and credentials, and
keeps directory/files at 0700/0600. Nine Python regressions pass, including actual
OpenSSL verification; the Go scripts test invokes these regressions as well.
A second review found no additional issues in this foundation's documented scope.

This is not a completed end-to-end harness: the application stack provisioner, real
observation adapter, complete HPA/final-archive evidence and deployed three-trial
matrix remain open. Docker's read-only metadata filesystem prevents runtime fault
validation here. The driver never force-deletes a StatefulSet identity or PVC.

## Watchdog completion-boundary review/fix cycle

The broader API rerun exposed a watchdog race after a slow durable write returned:
the gauge could become zero before the caller published completion, causing a healthy
controller to re-panic. The backstop now prefers an already-completed stage and gives
an observed write one settling interval, within the existing extension cap. It still
fails a genuinely wedged stage and never extends the maximum watchdog budget.

The watchdog regressions passed ten repetitions (18.474 seconds), and watchdog plus
terminal-observation race regressions passed (4.316 seconds). Source review confirmed
that the completion preference and settling interval cannot bypass the cap. A race
build initially overlapped dashboard asset replacement and failed to read removed
embed inputs; it was rerun after the completed dashboard build and passed.


## Terminal observation review/fix cycle

Terminal REST projections and cached WebSocket snapshots expose
`live_metrics.observation_state: historical`; sample timestamps, VUsers, TPS and totals
retain their measured values. History reloads preserve that label without changing
canonical/history measurements. The dashboard displays capture time and explicitly
states that historical VUsers are not currently running VUsers. Generated API and
TypeScript contracts were refreshed.

Review found that database history reload replaced the annotated projection, that a
missing history tail needed a nil guard, and that terminal routes redirect to Results
rather than advanced details. All three are addressed; both views now label historical
samples. The local-auth browser fixture refreshes its workspace catalog after seeding
through the API. No production routing or authorization change was needed.

UI validation passed 26 relevant unit tests, typecheck, lint and build. The complete
analysis workflow file (existing comparison workflow plus terminal-observation case)
passed under dev-admin and local-auth. The local-auth screenshot was inspected.
Focused API/history-reload tests and race tests passed; final whole-repository results
are recorded below. Active-run stale/recovery semantics and durable partial archives
remain open; the new label does not claim measurement completeness.

The final fault-driver review additionally made evidence directories exclusive so a
retry cannot overwrite the only record of a prior failure; all ten Python cases pass.
QA inventory now ignores private artifact packages. The opt-in Java fixture environment
gate introduced by concurrent work was documented after the full-suite inventory guard
flagged it; the other Java changes remain outside these commits.

## Final validation and environment boundary

The final `go test ./...` run passed (API package: 318.002 seconds), including the
corrected QA/dashboard and opt-in-suite inventory checks. The subsequent nil-tail
projection regression also passed with the focused history-reload tests (1.156
seconds). Terminal projection/history race tests passed (2.835 seconds). Dashboard
validation passed typecheck, lint, build, 26 relevant unit tests, both complete affected
browser workflow profiles, and an additional local-auth screenshot inspection.
Documentation checks passed for 68 active Markdown files and the staged diff passed
whitespace validation.

Docker Desktop still rejects container creation with a read-only
`/var/lib/desktop-containerd/daemon/io.containerd.metadata.v1.bolt/meta.db` filesystem.
Restarting it would interrupt other stacks, so no shared-runtime restart was performed
without the requested user decision. The next deployed harness, outage, KEDA, archive
replay and fault-matrix work needs a repaired disposable test runtime. No multi-host
context was supplied for permanent-worker/zone/storage qualification.

The readiness plan is not complete. Its ledger remains authoritative for the
unfinished stack adapter/provisioner, deployed outage and archive-replay evidence,
reserved unused recovery capacity, durable combined-loss recovery, explicit partial
archive finalization, and repeated local/multi-host release gates. These commits do
not enable or claim production combined-node-loss continuity.


## Active observation freshness API

Active current-sample projections now emit `observation_valid_until` and mark expired
samples `observation_state: stale`. The five-second bound is a presentation rule, not
a recovery budget or proof of target traffic. Local samples expire from their actual
sample timestamp. Distributed samples expire from the oldest contributing batch;
a required target with no batch, a missing timestamp or a future timestamp makes the
aggregate stale. Finished contributors retained in an aggregate retain their original
age too. Canonical counts, timestamps, SLA evidence and historical values are preserved.

Review checked all public projections, WebSocket publication, persisted-history and
cache serialization, and copy ownership. Absolute deadlines survive relays and cannot
be renewed by reading a sample. Terminal state takes precedence and removes the active
freshness deadline. Focused regressions cover boundaries, missing/future timestamps,
mixed-age generators, restored freshness, detached projections and actual WebSocket
serialization. These changes do not enable recovery or establish a no-overlap guarantee.

Validation: the full API package passed (332.930 seconds), focused API regressions
passed (1.318 seconds), and focused observation/concurrent-metrics race checks passed
(3.320 seconds). Generated OpenAPI/TypeScript contracts were refreshed. Documentation
and whitespace checks passed. Browser presentation and its checks are the next slice.


## Active observation presentation

Command Center and advanced run details expire observations in the browser even when
WebSocket updates and polls stop. Old API responses without a deadline use the same
five-second sample-age bound. Explicit server stale state, invalid timestamps and
invalid deadlines cannot display as current. Fresh evidence clears the warning;
terminal state still displays historical evidence. A stale sample does not assert
that a run failed, that its load stopped, or that recovery succeeded. Configuring a
sampling interval longer than five seconds can legitimately produce these warnings
between samples; this bound is independent of telemetry health and recovery policy.

Review moved the Command Center expiry timer into the small notice to avoid rendering
all its graphs every second. Browser screenshot review then found conflicting “live
run data” feedback under the warning; the feedback and load summary label now describe
observations. Run controls, captured values, target settings and history are preserved.

Validation passed typecheck, lint, build and 94 relevant unit tests. The full affected
Command Center and analysis workflow files passed in both dev-admin (four cases,
9.8 seconds) and local-auth (four cases, 50.6 seconds). After the screenshot-driven
wording fix, both Command Center cases passed again in dev-admin (4.3 seconds) and
local-auth (17.9 seconds); the final local-auth screenshot was inspected. Browser
fixtures use real scoped run IDs with controlled observations; they do not inject a
real node failure. Final review found no remaining actionable issue in this slice.


## Fault observation integrity

Review reproduced three false passes in the driver: a duplicate ownership observation
was retried away as an outage; a run could fail after its initial sustained recovery
and still pass; and clearing the previous rate sample during an outage also forgot
counter continuity. Regression tests failed against the prior implementation before
the fixes were applied.

Safety violations now abort the trial permanently, emit a fixed validation reason and
restore the worker by immutable container ID. The last validated target counter and
clock survive transport outages; only the consecutive-load window resets. A terminal
run before the trial ends fails even after an earlier recovery. Finite, nonnegative,
correctly typed load/restart counts and shard identity fields are required. Invalid
JSON and wrong-run evidence cannot be treated as a retryable transport failure.
Actual transport outages and unavailable fresh observations remain retryable.

All 14 Python tests passed, including OpenSSL generation and negative/cleanup cases;
the final Go harness wrapper passed uncached (1.692 seconds). Final review checked
exception ordering, continuity across gaps, terminal-after-recovery behavior and
sanitized evidence. No remaining actionable finding was identified in this slice.
End-to-end Docker node-fault validation and the real observation adapter are still open.

## Follow-up validation result

The repository-wide `go test ./...` passed (API package: 505.813 seconds). The fault
harness was separately rerun through its Go wrapper with `-count=1` after the Python
changes, so the cached scripts result in the broad suite is not the evidence for the
new regressions. Focused observation race tests, 94 dashboard unit tests, typecheck,
lint, build, both affected browser auth profiles, documentation checks and diff
whitespace checks passed as described above. The three follow-up slices were reviewed,
fixed and committed independently. Concurrent Java/performance work remains separate.
No new Docker, KEDA or real node-failure qualification was performed in this follow-up.

## Durable archive collection failure records

Distributed completion now records a fixed reason and observation time when archive
collection errors or yields no generator archive. These records are separate from
publication retry jobs and do not alter the already committed execution outcome.
The existing COMPLETE-path failure on required archive collection remains unchanged.
A catalog entry or publication intent takes precedence over the failure on reads.

Schema release 17 adds an idempotent record bound to project, run creation/completion
timestamps and terminal status. HA writes check the execution lease in the same
transaction. SQLite retains nanosecond identity; PostgreSQL uses pgx microsecond
precision. Run and project deletion cascade the record. Reused IDs cannot accept
late writes or expose old failures. Single-run full, summary and metrics reads hydrate
`archive_failure`; lists do not perform an additional query per run. The scoped
`test_archive_failed` WebSocket event carries only the fixed reason and recording time,
not transport errors, credentials or internal identity fields.

Review fixed rejected-lease readback/publication and checked identity again between
storage readback and event emission. Regression tests cover restart reads, duplicate
observation, publication precedence, deletion, same-ID replacement, stale producers,
schema upgrade, failed/aborted collection, and the existing fail-closed COMPLETE path.
Focused storage/deletion/schema tests and API race tests passed. Generated API contracts
and documentation checks passed. The full Go gate is running and will be recorded
below. A tagged PostgreSQL precision/deletion regression compiles but was skipped
because `APEXLOAD_TEST_POSTGRES_URL` is unset; it is not PostgreSQL runtime evidence.

This slice records failures observed by a surviving collector. It does not close the
crash window after terminal commit but before collection/recording, guarantee recording
through a database outage beyond bounded retries, cover every adoption-failure path,
retry collection, or describe missing shards in partial archives. Those acceptance
items and real Docker/KEDA/multi-host qualification remain open.

## Archive failure supersession and retention

Follow-up lifecycle review reproduced stale failures resurfacing after later archive
metadata was removed. Catalog writes, normal publication intents, orphan-recovery
intents, and successful finalization now clear the superseded failure transactionally.
The regression failed for catalog, outbox, and orphan paths before the fix. Tests also
inject a failure-record deletion error to prove that publication rolls back, preserves
the failure, and succeeds coherently on retry. Completion handles records left beside
an existing job by an older writer. Focused storage/archive race tests passed; final
review found no remaining actionable issue in this fix. The final broad gate below
includes the publication change.

## Results presentation for recorded collection failures

Results now distinguishes an observed collection failure from unknown publication
progress, shows the recording time, retains the execution outcome and available
historical samples, and explains that Check again refreshes availability without
restarting collection. A later archive pointer takes precedence. Unknown reason codes
receive a fixed explanation rather than exposing raw strings; invalid timestamps show
Not recorded. Existing bounded polling and manual refresh behavior remain unchanged.

The 49 relevant dashboard unit tests, typecheck, lint and build passed. The full
analysis workflow file passed both dev-admin and local-auth (three cases each).
The narrow-screen failure notice was visually inspected; it preserved the terminal
outcome and retained sample without horizontal overflow. Browser fixtures use real
scoped run IDs with controlled evidence. The new fixture also aborts the actual queued
run before showing synthetic evidence, keeping the catalog/topbar terminal. This is
presentation evidence, not a real generator outage. Final UI review found no remaining
actionable issue in this slice.

## Final archive-failure validation gate

The final `go test ./...` passed with the supersession fix included (API package:
372.712 seconds; storage: 23.338 seconds). Focused API race checks passed, as did
storage/archive race checks including completion of a previously coexisting failure
record. Dashboard typecheck, lint, build, 49 relevant unit tests, documentation checks,
and diff whitespace checks passed. Final analysis browser runs passed all three cases
in each profile: dev-admin 3.0 seconds and local-auth 22.6 seconds. The final local-auth
screenshot was inspected after correcting the fixture's catalog/topbar status.

An earlier overlapping broad run timed out the existing eight-second
`TestDistributedExecutionBackendStagesRemoteArchiveOnCompletion` assertion while the
run was still RUNNING. No deadline was relaxed. Subsequent full runs passed, and that
specific case passed three additional uncached repetitions (20.854 seconds total).
Its transient timeout was not isolated to a specific cause; the final passing gates
do not establish a production archive-completion latency bound.

No new live Docker, KEDA, Redis/PostgreSQL failover, or multi-host qualification was
performed. The new tagged PostgreSQL regression was skipped without a configured
integration database. Concurrent Java/performance and admission-test changes were
preserved outside these commits. The three implementation/fix slices are complete;
the remaining acceptance gaps listed above and in the readiness plan remain open.


## Partial archive source coverage

The collector now persists an authoritative source inventory from the same snapshot
used for collection, including retired transports and known assignments whose
transports disappeared. Generator-supplied coverage cannot replace that inventory.
Best-effort archives preserve surviving measurements and terminal outcomes while
recording missing sources. Required collection still fails closed on missing sources.

Completeness scoring independently evaluates source loss, malformed coverage and the
legacy incomplete-export flag. Intact surviving files therefore cannot yield complete
evidence when known sources are missing. HTML, PDF/text and CSV exports include the
source inventory. Controller-failover prose no longer unconditionally claims that
measurements are complete. Review found and fixed omission of known assignments whose
clients disappeared; the final focused API race run passed (7.086 seconds), including
collection modes, retired/missing sources, authoritative metadata, persistence,
acceptance scoring, exports and existing report profiles. No further actionable
finding remained in this slice. Generated API contracts were refreshed. The full Go
gate is running and will be recorded after completion.

This inventory describes sources known at collection time. It does not reconstruct
all historical shard ownership, quantify lost samples, close the pre-collection crash
window, or implement combined leader/worker recovery. Live production qualification
remains open.


### Recorded single-target coverage review fix

A further collector review found that a recorded single target could be omitted when
its dial failed but retired archives were available. The source union now includes
that recorded target independently of client availability. A regression injects that
dial failure and verifies the missing original source beside the collected retired
source. Focused API race tests passed (5.915 seconds); final review found no further
actionable collector finding. The final broad gate below includes this follow-up.


## Partial archive coverage in Results

Results now shows server-provided source-coverage findings alongside the saved archive
and offers an expandable, bounded source inventory. The warning remains visible
across analysis tabs. Execution outcome and retained sample counts remain unchanged;
legacy incomplete archives and invalid coverage can show a warning without inventing
a missing-source list. Complete archives without a source finding gain no warning.

Dashboard typecheck, lint, build and 46 relevant unit tests passed. The analysis
workflow passed all four cases in both dev-admin (5.7 seconds) and local-auth (31.3
seconds). Both narrow-screen screenshots were inspected, showing the terminal outcome,
partial-evidence warning, collected/missing sources and retained 500-sample count
without horizontal overflow. Review corrected the presentation fixture to avoid
requesting action drilldowns absent from its synthetic archive and centered the
notice for screenshot capture. These are scoped presentation fixtures, not live
node-loss evidence. Final UI review found no remaining actionable issue.

The first broad Go run passed (API: 404.978 seconds; storage: 32.524 seconds). A final
run includes the subsequent recorded-single-target fix and is recorded below when
complete. No new Docker, KEDA, Redis/PostgreSQL failover or multi-host qualification
was performed. Durable intent on every terminal path, exact historical shard loss,
combined recovery and deployment qualification remain open.

## Final source-coverage validation gate

The final `go test ./...` passed with the single-target follow-up included (API:
326.321 seconds). The focused API race suite passed after that fix. Dashboard
checks passed: typecheck, lint, build, 46 relevant unit tests, and four analysis
browser cases in each authentication profile. Final documentation checks and
`git diff --check` passed. No actionable finding remains in these source-coverage
and Results slices. Unrelated concurrent Java/performance and admission-test work
was preserved outside these commits. Production recovery claims remain bounded by
the open readiness-plan acceptance gates.


## Fail-closed evacuation archive outcomes and retries

Fail-closed evacuation now records the same durable, incarnation-bound archive
collection failure as ordinary distributed completion. Missing bundles and collection
errors remain distinct fixed reasons; a new controller can read the recorded outcome
without relying on the producer's memory. Successful publication takes precedence.

Review also corrected archive finalization around terminal-write outages and callback
replays. Generator transports stay available until the terminal row commits and
collection is attempted. An in-process retry retains the original controller interval
target history across terminal compaction, and duplicate cleanup callbacks leave that
snapshot to its existing worker. Archives use the committed completion timestamp and
failure reason. Deleted/replaced runs, different terminal outcomes and already
published archives are not recollected. Existing publication jobs and catalog records
protect their evidence from replacement by a replayed evacuation callback; failed
storage checks defer collection instead of treating the publication state as empty.

Regression tests cover missing/error/success outcomes after runner reconstruction,
original archive identity, duplicate and superseded callbacks, temporary terminal
storage failure, preservation of early interval targets, and outbox/catalog precedence.
Focused tests passed. The final evacuation/archive race suite passed (29.065 seconds),
including the duplicate-callback history guard. Final review found no further
actionable issue in this slice. The full Go gate is running and will be recorded
below. No dashboard or public API contract changed.

These retries are process-local and only cover the fail-closed evacuation collector.
They do not persist collection intent before terminalization, survive controller loss
before a collection outcome is recorded, implement combined leader/worker recovery,
or establish PostgreSQL/Redis/KEDA or multi-host failure qualification.


### Replacement-run retry guard fix

A further identity review found that the new retry guard's test-ID-only key could
suppress a replacement run while an older worker was still pending. The guard now
uses the exact in-process run instance, matching the collector's ownership check.
A regression holds the old worker claim while finalizing a new instance with the
same persisted identity and verifies that only the replacement receives the archive.
Final validation below includes this fix.

The outer stop-cleanup retry was also reviewed: once collection has a durable failure
record, callbacks now accept that result instead of repeatedly trying closed sources.
A regression makes archive storage unavailable after the first attempt, replays the
callback, and verifies the original reason and recording time remain unchanged. The
first full Go run passed (API: 369.205 seconds); final validation includes both retry
follow-ups.

The final evacuation/archive race suite passed with both follow-ups (31.551 seconds).
Review found no further actionable issue in the run-instance guard or recorded-outcome
handling. Documentation and whitespace checks passed. The final broad gate is still
running and is recorded below when complete.

## Final evacuation archive validation gate

The final `go test ./...` passed with both retry follow-ups included (API: 339.774
seconds). The final evacuation/archive race suite passed (31.551 seconds), and
documentation and whitespace checks passed. The preceding broad runs also passed
(API: 369.205 and 351.647 seconds); an earlier development run failed to compile a
test fixture that incorrectly supplied ProjectID on RunArchiveRecord, which was
corrected before the passing runs. No actionable review finding remains in this
scope. No dashboard files or public API contracts changed, so no new browser workflow
was required. Unrelated Java/performance and admission-test changes were preserved.

No new Docker, KEDA, PostgreSQL/Redis failover or independent-node qualification was
performed. Persisted pre-collection intent on every terminal path and combined
controller/worker recovery remain open readiness work.


## Disposable-cluster evidence inventory

Added a read-only cluster inventory command for the owned disposable kind namespace.
It records node/pod placement, readiness/restarts, runtime image digests, workload
replicas, HPA/KEDA state, PVC bindings and selected Kubernetes event fields. Raw
messages, pod environment/template values, annotations, trigger metadata and command
errors are excluded. Namespace UID/ownership is rechecked after collection; foreign
nodes, partial pagination, oversized lists and malformed counts cannot silently pass
as complete inventory. Missing APIs/CRDs remain explicit gaps in the retained artifact.
Files are private, refuse overwrite, and have an 8 MiB artifact limit.

The 22 Python harness tests passed, including an executable fake-kubectl CLI test
that verifies explicit context/namespace arguments, partial-inventory exit status,
private output, no overwrite and error redaction. These are command-boundary tests,
not a deployed Kubernetes qualification. Review found and corrected a fixture newline
escape before the final passing test run. The repository scripts test bridge and
documentation checks are recorded with the final gate below. No actionable finding
remained in the collector review.


## Fault-driver cluster evidence checkpoints

The optional `capture_cluster_evidence` boolean adds private preflight and restored
inventory artifacts, linked from the event stream with SHA-256 and explicit resource
gap names. Preflight capture runs before sustained baseline observations. Cleanup
restores the immutable container before any diagnostic read, so collection cannot
consume the stopped-node traffic-recovery budget or delay restoration. Ownership or
artifact-write failures before baseline prevent fault injection. Individual missing
resource APIs remain diagnostic gaps, separate from the traffic gate.

Review covered diagnostic errors during cleanup. Read failures are recorded with a
fixed event, and even a failure to write that event preserves an existing safety or
traffic exception. A recording failure after an otherwise successful trial fails
the driver. Tests verify checkpoint timing, hashes/private files, ownership changes,
missing KEDA, artifact conflicts, and primary-failure preservation after restoration.
This is immediate post-Docker-restoration evidence, not a pod/PVC readiness gate.
No actionable finding remained after review. Final test results are recorded below.

## Final cluster-evidence validation gate

All 28 Python fault-harness tests passed (1.566 seconds). The complete repository
scripts package passed uncached (`go test ./scripts -count=1`, 14.609 seconds), which
also runs the harness through its Go CI bridge. Documentation checks and
`git diff --check` passed. The standalone CLI test executes a fake kubectl process;
the driver timing/restoration tests use a fake command boundary. No actionable
review finding remains in these collector and checkpoint slices.

No live Docker/Kubernetes fault was injected. The stack provisioner, authoritative
traffic/assignment observer, during-fault scaling evidence, final archive evidence,
and repeated multi-host/dependency-failover qualification remain open. Application,
dashboard and database schemas are unchanged. Unrelated concurrent work was preserved.


## Kubernetes restoration readiness evaluator

Added a conservative evaluator over sanitized inventories. It checks retained node,
workload and PVC identities; current observed generations and converged replica
counts; pod readiness and Deployment → ReplicaSet → Pod / StatefulSet → Pod ownership;
and unchanged bound volume identities. Bare running pods must retain their identity.
Normal controller-managed pod replacements are accepted. Current desired replicas
may differ from baseline, but a previously active workload cannot pass by scaling to
zero. Completed job pods are ignored; active unsupported owners fail closed. This is
readiness evidence, not data integrity, availability or execution-fencing proof.

The collector now includes allowlisted owner identities, deletion state and ReplicaSet
inventory. Review found that duplicate node names could satisfy the old inventory's
three-node count; duplicate names now abort collection. Twenty focused collector and
evaluator tests passed, covering missing evidence, wrong identities, volume rebinding,
stale generation, unavailable replicas, pod ownership/counts, pressure, termination,
normal replacement and autoscaling. No further actionable finding remained in this
slice. Driver integration and its bounded cleanup tests follow separately.


## Bounded Kubernetes restoration gate integration

The optional `restoration_readiness_seconds` setting requires cluster evidence and a
ready supported baseline before injection. After Docker restoration and the traffic
trial, the driver retries readiness with a shared monotonic read deadline, capped at
30 minutes. Each subprocess receives only the remaining budget, and even a ready
response arriving at/after the deadline cannot pass. The gate records per-attempt
reason counts and a final private assessment plus the final available inventory,
with hashes in the event stream. It does not accumulate a full snapshot per poll.
Traffic success followed by Kubernetes readiness failure fails the driver separately.

Command-boundary tests cover delayed readiness, permanently unready resources, slow
reads, API outages, namespace replacement, optional event gaps, unsupported/unready
baselines, artifact conflicts, explicit bounded configuration and preservation of the
original safety exception. Interruption restores Docker and skips the optional wait.
Review found and fixed two additional cases: terminating nodes must block readiness,
and checkpoint-index write errors must remain failures rather than being reclassified
as harmless diagnostic outages. Regression tests cover both. No further actionable
finding remained in the implementation review; final validation is recorded below.

This gate observes Kubernetes readiness and unchanged PVC binding identities, not
application/dependency availability, volume contents, archive completion or execution
fencing. It supports active Deployment/StatefulSet stacks plus bare pods; active
Job/DaemonSet/custom owners fail baseline qualification. There was no live Docker or
Kubernetes fault injection, and production qualification remains open.

## Final restoration-readiness validation gate

All 48 Python fault-harness tests passed (1.584 seconds). The complete Go scripts
package passed uncached (`go test ./scripts -count=1`, 18.758 seconds), including its
Python harness CI bridge. Documentation checks passed for 68 active Markdown files,
and `git diff --check` passed. Final review found no further actionable issue in this
scope. Tests use sanitized Kubernetes fixtures and command boundaries; the collector
CLI regression executes a fake kubectl process. These results do not constitute a
live cluster or production failure-domain test.

Only harness tooling, tests and readiness documentation changed. Application, public
API and dashboard behavior are unchanged; no new full application or browser suite
was run for this slice. Unrelated Java/performance and admission-test work was left
untouched. The stack provisioner, authoritative observer, during-fault scaling and
final archive evidence, repeated fault matrix, and multi-host/dependency-failover
qualification remain open.

## Background cluster sampling foundation

Added a single read-only background sampler over the sanitized inventory collector.
It pins namespace identity to the preflight inventory, retries transport gaps, stops
on invalid scope/inventory, and records cancellation explicitly. Each snapshot shares
a ten-second read deadline. The caller requests stop without waiting, then joins only
after attempting Docker restoration; a stuck worker has an explicit bounded-join
failure. The sampler never writes driver events or runs mutation commands.

Artifacts use private new directories and exclusive files, SHA-256 indexes, a 512
attempt cap, 8 MiB per-file cap and 64 MiB aggregate snapshot cap. Raw exceptions and
credentials do not enter the index. Review checked failure during writing/worker exit
and avoided publishing a final byte count for a worker that did not stop. Eleven
real-thread tests cover slow reads, stop/join timeout, cancellation, scope replacement,
transport retry, partial API gaps, cumulative byte/attempt bounds, hashes/permissions,
no overwrite and unexpected worker failure. All 59 harness tests passed. No further
actionable finding remained in this foundation; driver integration follows separately.

## During-trial sampler integration and final validation

The optional `cluster_evidence_interval_seconds` setting (5–300 seconds, requiring
cluster evidence) now starts the sampler after a successful fault injection. The
foreground traffic loop never waits for snapshots. Cleanup requests cancellation,
attempts Docker restoration, then performs the bounded worker join and writes the
private hashed series index. Failed restoration still stops/finalizes the sampler;
the restoration error remains primary. Sampler errors do not skip the optional
Kubernetes readiness gate, and they preserve any original trial failure.

Review checked startup failure, ambiguous kill timeout, interruption, stuck workers,
namespace replacement, storage/attempt limits, index conflicts and diagnostic-error
precedence. The series now records an explicit UTC clock origin for attempt offsets;
an unstarted sampler is explicit. A real-thread integration regression holds a
Kubernetes read blocked until Docker restoration is attempted and verifies that the
fault duration does not advance to wait for it. Gaps and zero observed samples remain
visible and cannot be interpreted as successful scaling evidence. No further
actionable finding remained after review.

All 68 Python fault-harness tests passed (1.956 seconds). The complete Go scripts
package passed uncached (`go test ./scripts -count=1`, 15.276 seconds), including the
Python CI bridge. Documentation checks passed for 68 active Markdown files, and
`git diff --check` passed. The sampler tests use real threads with controlled command
boundaries; the existing collector CLI test executes fake kubectl. No live Docker or
Kubernetes fault was injected. No application/API/dashboard code changed, so the full
application and browser suites were not rerun for this tooling slice. Unrelated
Java/performance and admission-test work was preserved.

Remaining qualification includes the stack provisioner, authoritative traffic/shard
observer, deployed KEDA/dependency time series, final archive evidence, repeated
fault cases, and independent multi-host/zone testing. Sampled Kubernetes state alone
does not establish production availability, exclusive ownership or archive integrity.

## Final archive evidence collector

Added an incarnation-bound collector to apexctl. It pins test/project/creation time,
expected terminal status and the catalog's manifest SHA-256, reads the archive
manifest and controller integrity report, then rechecks the terminal run/catalog.
PostgreSQL identities use exact microsecond truncation; SQLite identities remain
nanosecond-exact. The collector validates artifact verdict/count consistency and the
manifest trust-anchor entry rather than accepting an HTTP 200 or a bare verified
boolean. Source coverage remains distinct: unknown, partial and best-effort snapshots
do not pass this strict distributed-evidence gate.

Review added explicit rejection of unsupported manifest schemas; this initial gate
supports v4 archives. It also covers ID reuse, missing terminal times, wrong projects,
legacy provenance, missing archives, durable collection-failure reasons, inconsistent
hash/count reports, duplicate entries/sources, transport/oversize/malformed responses,
read deadlines and run replacement during collection. Output excludes source/artifact
names, scripts, configuration, server error text, URLs and credentials. A passing
archive for an explicitly expected FAILED run does not imply execution success.
Focused collector and command tests passed; no further actionable finding remained
in the collector slice. CLI exposure and final validation follow separately.

## Archive evidence CLI and review follow-up

Exposed `apexctl archive evidence` with explicit test/project/creation-time identity,
expected terminal status, metadata backend, new private output directory and a
command-wide read timeout. The command writes a safe JSON artifact even when the
check fails and refuses to reuse output directories. Help, CLI discovery and the
fault-harness workflow document the command and its limits. Tests exercise actual
subcommand dispatch in a subprocess, environment authentication, argument validation,
private file permissions, failure artifacts and overwrite refusal.

Final review found that an old integrity response could otherwise look like current
post-fault evidence. Verification timestamps must now fall in the verification request
window with a documented five-second controller/client clock-skew allowance. Tests
reject both stale and future-dated reports. The final CLI package suite passed (13.508
seconds), and focused race checks passed (3.829 seconds). Documentation/whitespace
checks passed. No further actionable issue remained in this scope; the broad Go gate
is recorded below once complete.

The pass describes the controller's integrity verdict and a complete declared source
snapshot. It does not claim independent client hashing, SLA success, zero measurement
loss, an atomic read snapshot or successful live failover. Recorded archive replay,
leadership transfer and multi-host/dependency qualification remain open. No dashboard
or public API route/schema changed; unrelated concurrent work was preserved.

## Final archive-evidence validation gate

The full `go test ./...` suite passed (API package: 353.099 seconds). After the
freshness follow-up, the final full-suite confirmation also passed, reusing cached
results for unchanged packages. The final CLI package suite passed (13.508 seconds)
and the focused archive-evidence race suite passed (3.829 seconds). Documentation
checks passed for 68 active Markdown files, and `git diff --check` passed. Final
review found no further actionable issue in this collector/command scope.

The tests use local HTTP servers and a real CLI dispatch subprocess. They validate
response contracts, bounded reads, identity/coverage gates and retained safe evidence;
they do not replace recorded PostgreSQL/S3 restart or leadership-transfer trials.
No new live Kubernetes fault or independent-node qualification was performed.

## Structured start-boundary placement rejection

Start-boundary placement revalidation now returns a typed error containing all
blocking conflicts. The start handler responds with HTTP 409, code
`generator_placement_conflict`, the existing preflight-shaped `blocking_conflicts`,
and `override_available: false`, and records a blocked placement audit event. The
first-conflict error text remains compatible with scheduler and direct-runner callers.

A real SQLite/API regression exercises the existing aggregate Java runtime-capacity
gate with three individually eligible groups that cannot share the advertised slots.
It verifies both conflicts survive, ordinary and capacity-override requests are
rejected, audit identity is correct, and neither memory nor durable lifecycle state
is started or failed. Error tests cover nil/empty payloads and wrapped typed errors.
Focused tests and generated API-contract freshness checks passed.

Review confirmed this changes reporting for the existing gate only: placement
revalidation currently runs for Java Vuser groups, and the earlier Java-specific
runtime gate remains separate. This does not add HTTP placement checks, live
reachability admission, spare-capacity reservations, retryable discovery responses,
or new scheduler behavior. Asynchronous startup failures retain their existing
terminal response. No dashboard or public route/schema change is required.

The full `go test ./...` suite passed (API: 333.328 seconds). Focused race
checks passed (7.423 seconds), including existing successful start, capacity
override and asynchronous fast-failure behavior. Generated API-contract freshness,
documentation checks for 68 active Markdown files and `git diff --check` passed.
Final review found no further actionable issue in this reporting scope. Unrelated
Java/performance work was preserved. No new live cluster qualification was performed.

## Structured launch-window admission conflicts

Launch-window revalidation now preserves its conflict records in a typed error.
The start API returns HTTP 409 with `launch_window_conflict`, the preflight-shaped
`blocking_conflicts` and `override_available: false`, plus a blocked
`capacity.launch_window.reject` audit event. The original first-conflict message
remains unchanged for scheduler and direct-runner callers. This covers the existing
window checks, including HTTP runs that overlap future capacity reservations.

The SQLite/API regression creates an HTTP run and a future reservation, verifies
the conflict time and required/available generator counts, rejects both ordinary
and capacity-override requests without dispatch or lifecycle changes, and verifies
that releasing the reservation permits the same run to start. A separate injected
occurrence-read outage remains HTTP 503 with `Retry-After: 1`. Unit cases preserve
retryable classification for stale, reservation, occurrence and quota evidence;
available nonconflicting evidence and nil inputs remain accepted. Existing tests
cover current-capacity queueing and reserved manual starts.

Review corrected a clock-boundary risk in the test fixture: the reservation is
placed 45–60 minutes ahead so it cannot become an immediate-capacity case during
the request. Focused API tests and generated-contract freshness checks passed.
This changes response classification and audit evidence, not capacity planning,
scheduler policy, live generator discovery or recovery headroom.

The full `go test ./...` suite passed (API: 315.276 seconds); focused race
checks passed (6.205 seconds), including queueing, placement rejection, reserved
manual starts, capacity overrides and asynchronous fast failures. Generated API
contracts remained fresh. Documentation checks for 68 active Markdown files and
`git diff --check` passed. Final review found no further actionable issue in this
scope. Unrelated Java/performance work was preserved; no new live Kubernetes
fault or production qualification was performed.
