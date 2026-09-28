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

The complete deployed fault harness, active-load/node-loss liveness confirmation,
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

## Independent fault-trial traffic target

Added `apexfault-target`, a dependency-free HTTP counter service for a single
disposable fault run. Load requests require a run-bound header and load token;
snapshots require a separate observer token and expose only the run/process identity,
start/observation timestamps and accepted-request count. Health, observation and
rejected traffic do not count. Input preparation now generates separate target
credentials/config and a target certificate. The service defaults to loopback,
supports TLS, bounds HTTP timeouts/headers and exposes no reset operation.

Review fixed graceful shutdown so the process waits for active requests rather
than exiting as soon as the listener stops. Tests cover concurrent HTTPS traffic,
monotonic snapshots, distinct process incarnations, credential/run/method isolation,
duplicate headers, counter overflow, bounded config parsing, TLS failure and
cancellation. The target is an independent accepted-request counter, not an
authoritative assignment observer or a production throughput benchmark. Stack
provisioning and real controller/KEDA/shard correlation remain open.

The target race suite passed (1.512 seconds), including real HTTPS requests and
shutdown tests. Fresh-input/certificate tests passed (0.768 seconds), as did
68-file documentation and whitespace checks. No further actionable finding remained
in the target service slice. No Docker/Kubernetes deployment was performed.

## Target process continuity in the fault driver

The observer contract now requires the target's 32-character lowercase hexadecimal
`target_instance_id`. Both direct sample validation and the driver's retained
observation state reject a changed identity. This closes the false-pass case where
a restarted target's counter overtakes the old value between observations, including
across a transport outage. Missing/invalid identity is a latched safety failure;
old adapters must forward the new field and cannot silently fall back to counts.
The allowlisted event record retains only the validated identity.

Regression cases cover missing/malformed identities, unchanged identity acceptance,
a baseline restart preventing injection, and a post-fault restart after an outage
with a higher counter. Post-fault failure restores the worker and cannot become a
traffic-gate pass. Existing retryable transport and counter-reset tests remain.
Target identity detects process replacement, not generator execution ownership;
authoritative shard/generation correlation remains an open observation-adapter task.

Review found that process identity must be checked before freshness: otherwise a
stale replacement sample could be retried away and hidden by later traffic from
the old process. The driver now checks identity first, and a stale replacement
followed by good observations is a latched-failure regression case.

The compiled CLI smoke test exposed a strict-TLS interoperability failure in the
generated certificates: the leaf authority-key identifier was missing. Input
preparation now emits subject/authority key identifiers on CA and leaf certificates.
Tests use both strict OpenSSL verification and strict Python TLS handshakes for all
three service certificates. The repeated real CLI smoke passed with CA verification
enabled: authenticated counting, zero/new identity after process restart and clean
SIGTERM exit. Existing private inputs were not changed.

The full `go test ./...` suite passed (API: 338.461 seconds; target: 0.319
seconds). After the final Python/certificate fixes, the scripts suite was explicitly
rerun uncached and passed (23.196 seconds), including all 70 fault-harness tests.
Target race checks passed (1.512 seconds); strict certificate and real CLI TLS smoke
checks passed. Documentation checks for 68 active Markdown files and whitespace
checks passed. Final review found no further actionable issue in these slices.
Unrelated Java/performance work was preserved. No live Kubernetes fault or
independent-node/zone qualification was performed.

## Verified target snapshot reader

Added `apexfault-target observe` to read the independent target with the observer
token from private inputs. It verifies TLS using explicit CA or system roots,
permits plaintext only on numeric loopback addresses, disables redirects/proxies,
and bounds network duration, headers, body and CA input. The output preserves the
target timestamp and includes only the six v1 snapshot fields. Run identity,
process identity, counter types and freshness are checked; duplicate/missing/null
fields, unknown schema fields and non-JSON/compressed responses are rejected.

Tests cover verified and untrusted HTTPS, authenticated command output, URL policy,
redirect credential isolation, stale/future/mismatched snapshots, malformed payloads,
response bounds, stalled bodies, cancellation, invalid configuration and output
failures. Review tightened URL validation to reject an empty hostname. The reader
is a component for the future full observation adapter; it does not infer VUsers,
KEDA demand, ownership or successful responses from a target request count.

The full `go test ./...` suite passed, with cached results for unchanged packages.
The final target race suite, including the exact 4 KiB response-size boundary,
passed (1.393 seconds). A compiled CLI smoke against the real target with fresh
inputs passed: verified TLS, pinned run identity, exact output fields, and empty
stdout/nonzero exit when the generated CA was not trusted. Documentation checks
for 68 active Markdown files and whitespace checks passed. Final review found no
further actionable issue in this slice. Unrelated Java/performance work was
preserved; no live Kubernetes fault qualification was performed.

## Container and Kubernetes resources for the independent target

Added a dedicated standard-library-only target Dockerfile using the repository's
pinned Go builder and a non-root scratch runtime. A private renderer consumes the
fresh input directory and a repository manifest digest, validates ownership,
credentials and TLS, and emits Secrets, a single-replica Deployment, ClusterIP
Service and scoped NetworkPolicy. It pins the target to the owned kind control-plane
node, outside worker fault injection, and keeps observer credentials/private keys
out of the separate load Secret. No namespace is created/relabelled and no cluster
mutation is performed. Output is private and exclusive.

Review strengthened TLS validation to perform a strict handshake for the Service
DNS name using the exact captured certificate/key/CA bytes, avoiding validation of
a second file read. Tests cover wrong CA/service identity, key mismatch, bounded
malformed inputs, ownership mismatch, mutable/invalid image references, credential
separation, workload placement/security, output permissions, overwrite/symlink
refusal, safe CLI output and offline Kustomize rendering.

Docker Desktop responds to read-only queries, but the image build failed before
compilation: containerd's metadata database remains a read-only filesystem. The
existing PostgreSQL container also reports unhealthy. Existing containers/data
were left untouched; image runtime and live cluster validation remain unverified.

Validation passed: all 76 fault-harness tests (3.737 seconds), the six focused
renderer tests (0.890 seconds), the uncached Go harness bridge (12.393 seconds),
and `go test ./...` (API package: 402.687 seconds; cached results for unchanged
packages). Offline Kustomize rendering and a static Linux cross-compile with the
Dockerfile's build flags passed; the latter used the host compiler and does not
validate the container build. Documentation checks for 68 active Markdown files
and whitespace checks passed. Final review found no further actionable issue in
this slice. Unrelated Java/performance work was preserved.

## Bounded healthy-control comparison

Added a separate healthy-control command for comparing fault trials with a sampled
load window that has no deliberate node failure. It reuses the driver's strict
observation validation and private event recording, with a configurable command
timeout that preserves the existing fault driver's default. It obtains a sustained
baseline within a bounded acquisition budget, then requires each observed interval
to meet traffic, VUser and KEDA thresholds with zero controller restarts. A final
sample covers the requested window with a bounded five-second observation allowance.
Late responses cannot pass. No Docker command or replica mutation is performed.

Mandatory before/after Kubernetes inventories bind the run to the owned namespace
and node identities. Optional background sampling is finalized before a pass and
stopped on interruption/failure. Review checked deadline boundaries, failed final
inventories, preservation of existing evidence, credential omission, and the
distinction between sampled traffic and continuous availability. Tests cover low
traffic/demand, stale/terminal observations, target resets/replacements, duplicate
assignments, prior/new restarts, transport loss, interruption, changed cluster
identities, sampler artifact conflicts and fixed CLI errors.

All 86 harness tests passed, and the complete uncached `go test ./scripts -count=1`
suite passed (19.972 seconds), including the Python safety bridge.
An end-to-end CLI smoke with synthetic command sources and real timing passed a
10.055-second measured control window; it did not contact Kubernetes or Docker.
Documentation checks for 68 active Markdown files and whitespace checks passed. No application
or dashboard runtime code changed; the full application Go suite was not repeated
for this tooling-only slice. Final review found no further actionable issue.
The real observation adapter and deployed stack remain required for live control
qualification; this change does not qualify the production fault matrix. Unrelated
Java/performance work was preserved.

## Recovery completion deadlines and observation budgets

Review found that the fault driver recorded the start of a sustained healthy
interval as recovery time. An interval beginning before the recovery budget but
finishing after it could therefore pass. Recovery now requires the whole interval
to finish strictly before the recovery deadline and scheduled worker restoration.
The reported recovery time is the completed observation time; the interval start
is retained separately as `healthy_since_seconds`. Older evidence uses a different
timing definition and is documented accordingly.

Baseline and fault observations now inherit the remaining phase budget, capped at
15 seconds. Poll sleeps cannot cross the next recovery/restoration/trial boundary.
A late baseline cannot trigger injection, and a slow observation cannot extend the
scheduled stopped-node window. Failed recovery immediately enters the existing
restoration path. Docker commands and post-restoration checks retain their separate
budgets; this does not establish a hard Docker restoration completion guarantee.

The follow-up review found that late responses must still latch known controller
restarts. Restart validation now precedes the late-response rejection and records
the existing safety-failure event. Tests cover sustained completion before/at/after
the budget, post-response timing, slow baseline/recovery reads, restoration at both
shorter and longer fault windows, insufficient stopped-node time, and a controller
restart reported at the deadline. The seven initial timing regressions fail against
the prior committed driver, confirming that they detect the original behavior.

All 94 harness tests passed (3.843 seconds), including existing sampling,
restoration-readiness and healthy-control regressions. The complete uncached
`go test ./scripts -count=1` passed (15.296 seconds). Documentation checks for 68
active Markdown files and whitespace checks passed. Final review found no further
actionable issue. This slice changes fault tooling only; the full application Go
suite and live Docker/Kubernetes qualification were not repeated. Unrelated
Java/performance changes were preserved.

## Docker recovery and TLS dependency resources

With explicit user approval, restarted Docker Desktop without resetting or deleting
data. The containerd read-only error cleared; existing Compose controller,
PostgreSQL and Redis returned healthy. A new owned three-node kind v0.33.0 cluster
uses Kubernetes v1.34.3; it does not reuse prior trial credentials or snapshots.

Added private PostgreSQL 16 Alpine and Redis 7 Alpine resource rendering, fresh
service certificates, persistent claims, authenticated TLS readiness, restricted
non-root containers, scoped ingress and separate controller client credentials.
Both data services reside on the control plane to isolate worker-loss trials.
Shared input/image/TLS/output checks retain the target renderer's contracts.
Review tightened storage-class syntax and rejected encrypted TLS keys without
interactive stdin prompts. Neither the renderer nor input generation applies
resources or creates namespaces.

Live component checks in run `e7514cf6e06205d8` passed: PostgreSQL TLS verified with
`pg_stat_ssl`, bad-password/plaintext rejection, Redis TLS/authentication and
plaintext rejection, and retained probe data after both dependency pods were
cleanly replaced. Both PVCs remained bound. The target image built and ran under
restricted pod security. Three independent rounds of 64 requests with eight
concurrent clients each produced exactly 64 accepted requests over verified TLS;
bad tokens/run IDs and an untrusted CA were rejected. This is component evidence,
not a benchmark or a production/node-failure qualification.

Image manifests: PostgreSQL
`sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777`, Redis
`sha256:e7723ff73d963f5cc6d9c4643ea3d989527a402a319239054e9472a7fb9219a2`, target
`sha256:aeb1eeb0349d0beab828cd7a8aa933a7cfe857b386c2977fd7c9f0e72d117ea9`.
Docker Desktop's partial multi-platform cache caused ordinary kind image loading
to fail on absent platform content. Explicit ARM64 export/import worked; the
locally built target's repository digest was registered against its verified
manifest in the disposable node image cache.

All 98 harness tests passed (4.337 seconds), and the uncached scripts suite passed
(30.286 seconds). Offline Kustomize, documentation and whitespace checks passed.
Final review found no further actionable issue in this slice. Private JSON
component evidence is retained under the ignored `artifacts/scalability/2026-09-27/`
trial directory. Single-instance dependency failover, CNI enforcement, the full
application matrix, and multi-host/zone storage qualification remain open. The
user confirmed that only Docker Desktop is available. Application images are
being built from committed source; unrelated Java/performance work is preserved.


## Docker storage recovery and bounded liveness sampling

Application-image import exhausted host storage (116 MiB free) and made Docker's
container storage read-only again. The authorized restart stalled during shutdown;
Docker Desktop's force-stop/start recovered it. Removed only this trial's transfer
archives and unused Docker build cache (10.68 GB reclaimed). Existing containers,
images and data volumes were preserved. The original Compose controller,
PostgreSQL and Redis returned healthy. The owned worker's damaged application
snapshots contained zero-byte binaries; removing only those application image-cache
entries and reimporting the existing images restored their verified binary hashes.
No PVC was deleted. These startup failures precede the liveness baseline and are
not counted as passing runtime evidence.

Added a read-only loopback liveness sampler with bounded curl transfers, concurrent
per-controller samples, per-controller nearest-rank p99, complete-window and sampling-gap
checks, and private evidence. It does not mutate Kubernetes or establish dependency
outage, pod identity, restart counts or readiness. Those require separate evidence.
Review disabled curl startup configuration to prevent local options changing the
request and moved verdict publication after sample persistence. Regressions cover
invalid ports/durations, failed/slow endpoints, malformed results, interruptions,
coverage gaps, late completion, transport bounds, output permissions, overwrite
refusal and evidence-write failure. A second review found no additional actionable
issue in this scope.

A ten-second live sampler smoke during the dependency outage passed against both
controller pod port-forwards (11 samples each, zero failures; p99 29.885 ms and
24.526 ms). This smoke validates the new sampler's transport path; the separate
600-second trial and restoration results follow.

The 600-second idle dependency-unavailability trial passed on two production-mode
HA controllers, one per kind worker, using application commit
`6be3855497f9d3aa1f3ad0a816d498353323dd88`. Before injection, four mTLS/token-authenticated
generators were ready, two per worker, with TLS PostgreSQL and Redis on the control
plane. Both controllers used the on-prem HA overlay, fresh credentials, PostgreSQL
leases and trusted-header authentication. Local emptyDir archives/import assets
were used only for this liveness profile; this does not qualify shared archives.

After PostgreSQL, Redis and all generator pods were scaled to zero and confirmed
absent, each controller received 601 direct pod-port-forward health requests over
600.000538 seconds. Both had zero failed requests. Per-controller nearest-rank p99
was 11.431 ms and 11.937 ms; maximums were 133.443 ms and 110.455 ms. The largest
sampling gap was 1.167 seconds. Every minute, the runner confirmed dependency pods
remained absent and both controller UIDs/restart counts were unchanged. Both
controllers retained their original UIDs with zero restarts after the outage.

The finally path restored PostgreSQL/Redis to one replica each and generators to
four. All nine application/dependency/target pods became Ready without further
mutations; the second controller had transient DNS/RPC timeouts while discovery
recovered. PostgreSQL and Redis retained their pre-outage probe data. All trial
port-forwards stopped. The original Compose services remained healthy. The target's
one earlier Docker-restart count is outside the measured controller baseline.

Private evidence includes the profile and image digests, trial scripts and hashes,
before/after controller identities, 1,202 request outcomes, latency series,
restoration inventory and retained-data checks. It remains under the ignored trial
directory. This is sampled idle liveness during dependency unavailability, not
successful test execution during an outage, dependency HA promotion, active-load
recovery, KEDA qualification, archive failover, or physical-host/zone resilience.

Final validation: all 109 harness tests passed through the complete uncached
`go test ./scripts -count=1` suite (19.618 seconds); the dedicated sampler's live
transport smoke passed. Documentation checks for 68 active Markdown files and
whitespace checks passed. Application runtime code did not change, so the full
application Go suite was not repeated. The prior watchdog regressions remain the
separate internal-wedge evidence. Unrelated Java/performance edits were preserved.


## Private TLS S3 component and signed retention checker

Added fresh S3 certificates and a private digest-pinned SeaweedFS renderer with
bucket-scoped credentials, separate client/server Secrets, TLS-only S3, authenticated
S3 gRPC, loopback internal services, restricted pod settings and PVC-backed metadata
and object storage. Added a bounded repository Go checker for signed probe creation,
exact retained reads, invalid-credential rejection and foreign-bucket rejection.
It confines transport to an explicit loopback port-forward, uses fresh installation
credentials and CA, refuses redirects/proxies, and saves private exclusive evidence
with fixed failure stages. The retention mode does not rewrite the probe.

Review/live trials caught and fixed three image-specific problems: port zero opens
an ephemeral listener rather than disabling HTTP; the gRPC TLS configuration needs
an explicit maximum compatible with its minimum; and internal metadata collections
consume volume slots before the archive bucket. The final profile serves primary
port 8333 with TLS, requires client certificates on its extra gRPC endpoint, and
allows four small volumes with one-at-a-time growth. These settings were checked
against pinned image source commit `4a5243886`, not only its nominal 4.17 tag.

The standalone Docker component used image digest
`sha256:186de7ef977a20343ee9a5544073f081976a29e2d29ecf8379891e7bf177fbe9`.
Signed virtual-hosted upload/read and bucket/auth rejection passed. Gracefully
replacing the owned container while preserving its named volume retained the exact
probe bytes. TLS rejected an untrusted CA; plaintext did not serve S3; the S3 gRPC
endpoint rejected clients without certificates and accepted the private client
certificate. The first cold upload exceeded the exploratory five-second timeout;
a subsequent upload received a successful response. Reads immediately after
replacement failed during startup and passed once the service initialized. These
failures are retained as startup observations, not counted as passing availability.
The reusable checker uses a bounded twenty-second request timeout and one-minute
whole-run deadline without SDK retries.

The component is a single-instance local archive service. These checks do not
establish Kubernetes DNS/PVC behavior, application publication/replay, leadership
transfer, replication, physical-node resilience or complete fault qualification.
All prior volumes, Compose services and the existing kind cluster were preserved.

Final slice review found no further actionable issue after fixes. All 112 Python
harness tests passed; `go test ./scripts/... -count=1` passed (scripts: 24.911 s),
and the S3 checker race tests passed. Whitespace checks passed. The clean-worktree
documentation check exposed existing links to ignored local evidence in historical
reviews; those artifacts are absent here. The two such links in the edited plan
were converted to explicit local artifact paths. No new broken links were added.


## Phased HA application profile and deployed shared archive replay

Added a private stack renderer that composes the actual `onprem-ha` base with
fresh target/data/S3 resources. It retains production authentication, PostgreSQL
leases, Redis, mTLS/token transport and S3 archives; pins two controllers to distinct
kind workers and defaults to four generators with 100-VUser local capacity. It
preserves per-generator local-path PVCs and explicitly limits import assets to
an ephemeral HTTP-only test profile. PostgreSQL authentication bootstrap is a
separate transactional SQL step; a subject-pinned audited first-admin bootstrap
allows authenticated test setup without enabling a bypass mode. No renderer
applies resources or injects faults.

Review/render testing caught an import-volume patch that retained a deleted PVC
reference and a RoleBinding retaining the base namespace; both were corrected and
regression-tested against actual Kustomize output. The application phase no longer
owns the Namespace, preventing it from removing dependency-phase Pod Security
labels. Fresh bootstrap, exact retry, incompatible-auth rejection and rollback
passed against PostgreSQL 16 in a separate disposable database. All 114 harness
tests and the uncached scripts suites passed (scripts: 20.463 s); the final optional
CNI-patch output was covered by a subsequent focused renderer test. No application
runtime source changed. Final review found no additional actionable renderer issue.

Both phases passed Kubernetes server dry-run and were applied to the existing owned
three-node trial with its original credentials and volumes. S3 signed put/get and
retention checks passed before/after normal pod replacement with the same PVC UID
and volume name. The first application rollout stalled with new cross-node PostgreSQL
connections in SYN-SENT. Kindnet's active policy engine retained old pod addresses
under its default 100m CPU/50 Mi limits and logged watch/TLS timeouts. Raising only
this disposable cluster's kindnet limits to one CPU/128 Mi and rolling that DaemonSet
restored connectivity. Original CNI configuration and diagnostics were saved. Two
controller startup restarts are recorded setup failures, not passing availability
evidence. No policy was removed or broadened to bypass the failure. An optional
resource patch and its limited scope are documented; the two interventions do not
isolate CPU throttling from stale-cache recovery as the sole cause.

After the new rollout, both controllers and all four generators were Ready, with
one controller and two generators per worker. The authenticated inventory confirmed
PostgreSQL, Redis and S3. A 40-VUser, 30-second HTTP smoke with a two-second ramp
completed on one generator; sampled steady live observations reached 40 VUsers with
zero observed errors. Its v4 archive has 23,120 transaction records, 31 resource
samples, 31 intervals and required source coverage with its one generator collected.
These transaction records are not a count of independent target HTTP requests; this
smoke is not the sustained independent-counter load gate or a performance benchmark.

`apexctl archive evidence` passed with the launch project/test/creation identity,
PostgreSQL microsecond semantics, nine verified artifacts and no missing declared
sources. Normal replacement of the idle execution leader moved the authoritative
active endpoint from generation 6 to 7. The successor, which had not previously
materialized this archive, passed the same evidence command with the identical
catalog manifest SHA-256. This demonstrates fresh shared S3 archive replay across
an actual controller leadership transfer. It does not establish abrupt active-run
adoption, combined generator loss, older investigation-archive migration, data-service
HA, KEDA scaling or multi-host/zone resilience. An expiring project-scoped
`results:read` service account was used for the evidence command; its key and all
raw profile inputs remain private. The original Compose services and all prior
PVCs were preserved.

## Local monitoring and first deployed KEDA trial

Added private Prometheus/KEDA inputs preserving all six shipped production queries,
including fleet identity, expiry and reserved-ordinal protection. Render tests cover
production/accelerated timing, sizing bounds, file permissions, authentication-free
internal scrape scope and safe merge-patch configuration. The Prometheus configuration
passed promtool validation. KEDA 2.21.0 was installed with pinned operator, metrics
server and webhook images, restricted to the disposable application namespace.
Prometheus 3.12.0 scraped both HA controllers successfully before the trial.

Queued demand of 320 VUsers changed the StatefulSet from two to four Ready and
API-discovered generators. Dispatch then failed: legacy auto placement assigned the
single 320-VUser group to one pod capped at 100 VUsers. This is a failed end-to-end
trial, not a scaling qualification pass. No passing target-rate or scale-down result
is claimed for it. The run was stopped/terminal and demand returned to zero; no node
fault was injected. Capacity placement needs correction and the full trial must be
repeated. The render feature does not qualify production HPA timing or KEDA failover.

An image-import startup failure was resolved before baseline: the ARM64 digest-only
containerd import lacked its normalized repository alias. Restoring that alias for
the verified manifest allowed Prometheus to start. This is preserved as setup evidence.
At minimum two replicas, existing generator PVCs pin both low ordinals to one worker;
at four replicas there are two per worker. No volumes were deleted to alter placement.

## Capacity admission correction from live KEDA trials

The live oversized-group failure exposed an auto-placement fallback that ignored
available capacity. Removed that fallback while retaining the documented one-host
legacy auto contract. Explicit host sharding now bounds weighted allocations by each
host's available VUsers as well as persistent Java runtime limits, including zero
capacity and seed-rounding cases. Failed heartbeat probes advertise zero capacity.
Known HTTP capacity shortages now use the existing non-overridable structured start
conflict and leave the queued run unchanged; cold discovery checks the complete
placement before sending any AssignWork RPC. Reservation-mode queuing remains intact.

Review found that two concurrency fixtures depended on failed heartbeat probes being
unlimited. Their heartbeat observations now use real in-process gRPC services while
their scale barriers continue testing serialization and independent-group concurrency.
A new cold-discovery integration test verifies zero assignments when the first group
fits but the complete run cannot. No source-affinity, fencing, run-goal or persistent
Java safety restriction was relaxed. Concurrent legacy launches still require opt-in
peak reservations for atomic admission; this change does not add reserved node-loss
headroom or guarantee replacement of participating shards.

Live investigation then found one already-draining generator, despite all four pods
being Ready/connected. Restoring that owned idle generator allowed one complete
four-group cycle: queued demand 320, scale 2 → 4, explicit dispatch of four 80-VUser
HTTP groups on four generators, sustained 320 active VUsers with zero observed errors,
independent loaded target rate 3,173.3 requests/s, COMPLETE, and idle scale-down to two.
Nine archive artifacts passed checksum/incarnation verification, with four declared
sources and zero missing sources. A separate queued 640-VUser trial reached four
replicas, HPA `ScalingLimited=True/TooManyReplicas`, and unchanged target traffic.
These results used the preceding application image and accelerated HPA timing.

The next attempt caught a pending pod without an IP in the private observation
helper; that helper now waits for a full set of matching, connected, active targets.
Its following trial correctly refused dispatch: ordinal 3 returned as `draining`
after KEDA recreated it. Code inspection found that the pod watcher persists a
termination drain under the stable generator name. This is a new actionable runtime
finding, so repeated-cycle qualification remains incomplete until that separate fix
is reviewed and deployed. No node fault was injected during these trials.

Final capacity-slice validation: `go test ./...` passed (API package 410.475 s),
focused capacity/start/discovery/concurrent-scale tests passed, and the corresponding
race run passed (5.080 s). `git diff --check` passed. Final review found no further
capacity-slice issue; the independently identified watcher defect is the next slice.

## Temporary pod termination state

Separated Kubernetes termination exclusions from persisted operator drain/cordon
intent. Successful complete pod inventories replace the temporary exclusion set;
failed inventories retain it. A recreated StatefulSet ordinal can therefore become
eligible without inheriting shutdown state. Operator drains remain durable and are
not automatically migrated away, because older stored drains have no reliable
provenance distinguishing operator intent from the previous watcher behavior.

Review caught two interactions: reconnect reconciliation must still apply offline
metadata cordons beneath a temporary exclusion, and failed operator updates must
restore operator intent rather than accidentally persisting the effective shutdown
state. Both now use the operator-state reader. Tests cover replacement addresses,
empty and failed inventories, duplicate observations, concurrent readers, operator
updates during termination, metadata reconciliation and lifecycle rollback.

While validation ran, a separate queued-demand trial on the preceding application
image replaced the idle leader normally. The authoritative generation changed from
8 to 9. Demand recovered to 320 without exceeding it; five post-baseline samples
were missing/zero during handoff. The observed StatefulSet stayed at four replicas,
and the independent target counter did not move. The successor retained the QUEUED
run, which was stopped afterward. This is bounded queued-demand handoff evidence,
not continuous metric availability, active-run adoption or a node-fault pass.

Final termination-state validation: focused lifecycle/metadata tests passed,
`go test ./...` passed (API 387.817 s), and targeted race checks passed (11.151 s).
`git diff --check` passed. Final review found no additional issue in this slice.
Deployed repeated-cycle verification will use a new image built from this commit.

## Reusable scaling readiness gate

Promoted the corrected pre-dispatch eligibility check into the fault harness.
It fails closed for pending pods without IPs, drains, stale/duplicate target rows,
identity mismatches, unavailable capacity and malformed observations. It requires a
one-to-one match between Ready pod identities and compatible idle HTTP generators;
namespace ownership and observation freshness remain caller responsibilities.
The live trial helper now uses this same function. All 123 Python harness tests
passed, including both previously observed readiness failures and IPv6 matching.
Final review added malformed protocol/address and contradictory Ready-condition
coverage; the rerun passed and `git diff --check` is clean.

## Refresh operator drains on HA takeover

The queued-demand leader-transfer trial exposed another stale-state path: a standby
loaded drain settings only at startup. It could restore an already-cleared exclusion
when it became leader. Leadership recovery now strictly reloads the persisted map and
offline cordon metadata before reconciling runs or admitting work. Storage errors,
malformed states and canceled reads retain the previous projection and keep admission
closed for retry. Operator drains and the separate temporary termination overlay are
preserved. Reacquisition by the same process also refreshes the snapshot.

Review also found that discovery on a standby could persist its entire stale map when
reconciling one generator's metadata cordon. Reconnect reconciliation now updates only
the local projection; metadata already stores the intent durably. Regression tests
cover newer settings, offline metadata, failed reads, invalid states, retry, canceled
reads, reacquisition and standby write-back. The fixture uses SQLite's real durable
lease generation; omitting that interface correctly failed the admission gate.

Focused tests passed (5.235 s); targeted race checks, including execution leadership,
passed (15.124 s). `go test ./...` passed (API 343.241 s), and
`git diff --check` passed. Final review found no further issue in this slice.
Deployed verification will use the reviewed image built from this commit.

## Verified kind application image loader

The reviewed controller image was built from commit `5d7bb3f3` with the normal
Dockerfile and standard profile. Its ARM64 manifest is
`sha256:e21631bd60ad1d1e09b2104cfd6a892f0b2174d46d157acfc37cfcd7792f134a`.
The named-tag import again lacked the normalized repository/digest alias in kind's
containerd. Restoring that alias after checking its manifest allowed both controllers
to become Ready with zero restarts. The health API's full `revision` field matched
the reviewed source; `commit` is intentionally abbreviated. Both setup checks were
completed before taking any load baseline.

Added `load_image.py` to make this import/verification procedure reusable. It checks
both workers' ownership, immutable container IDs and architecture before any import;
streams the verified local image; checks the imported manifest; creates the missing
repository/digest reference; and rechecks it. It never deploys or removes resources.
Review added exporter cleanup when the importer cannot start and full orchestration
coverage for validating all workers before mutation and verifying both new aliases.
The 130-test harness suite passed before the final orchestration case was added;
all eight focused loader tests then passed. `git diff --check` passed. No further
loader issue was found in the final review.

On the new deployed controller image, the queued-demand/cleared-drain handoff check
passed from leadership generation 10 to 11. Demand never exceeded 320 and recovered
with one missing/zero sample. The successor retained all four generators as active,
without reintroducing the startup drain snapshot. The queued run was stopped afterward.
This is normal controller replacement evidence, not an independent-node fault or
in-flight adoption result.

## Archive workspace sizing found by repeated scaling

Two consecutive reviewed-image cycles passed without manual drain repair: 320 active
HTTP VUsers, four 80-VUser groups, COMPLETE, independent loaded target rates 3,112.2
and 3,108.0 requests/s, then scale-down to two. The third attempt is a failed clean
control: Kubernetes evicted the leader with `Usage of EmptyDir volume "tmp" exceeds
the limit "128Mi"`. The controller container exited normally after eviction; a zero
restart count did not make the replacement a clean baseline. The private observer
correctly lost its original pod forward and did not publish a pass.

The successor reached generation 12 and marked the run COMPLETE, recording four
already-completed generators, zero running generators adopted, and a 16.188-second
observation gap. This does not establish uninterrupted traffic or an injected-node
recovery pass. The independent target's one historical restart preceded these trials
and was not a new target restart during this failure.

The local renderer had reduced controller scratch space below the observed roughly
190-MiB archive size. It now provides 1 GiB for transfer scratch, 2 GiB for local data,
a 4-GiB aggregate ephemeral-storage limit and 512-MiB request. Materialization is capped
at 256 MiB per archive and 256 MiB of idle cache, with a 15-minute TTL, rather than the
large production defaults. These are explicit small-trial limits; pinned active readers
and publication staging still need headroom. No shared archives or PVCs were deleted.
Render/validation tests, including a real Kustomize build, and `git diff --check` passed.
Final review found no further profile-sizing issue; repeated deployed replay remains
required before claiming three clean cycles.

## Deployed-run observation adapter

Added a repository read-only adapter for the owned PostgreSQL/Redis/S3 local profile.
It binds the namespace UID and run incarnation, resolves the current execution leader,
checks fresh live metrics against its armed generation, maps shard targets to owned
worker pods, reads expiring fleet demand, and reads the independent TLS target counter.
It compares surviving controller UIDs and restart counts with the supplied baseline.
All forwards are loopback-only and cleaned up within the CLI's bounded read window.

Live review corrected two assumptions: the publisher omits `targetRef.uid`, so the
adapter binds name/address to the observed pod UID and rereads that UID before returning;
and the target's pod port is 8090, whereas 443 belongs to its Service. Tests cover these
contracts, stale metrics, changed namespaces/runs/processes, duplicate assignments,
replaced leaders/survivors, invalid ordinals and terminal observations. The fault driver
now latches a fixed adapter safety marker, without exposing supplied diagnostic text.

The full 144-test Python harness suite passed (6.617 s). Final marker-ordering review
then passed all 45 fault-driver tests and the focused one-shot safety regression.
`git diff --check` passed; final review found no further issue. A terminal live read
completed in 0.635 s. Ten consecutive observations during a 320-VUser run all reported
four assignments, current demand 320 and zero survivor restarts; the slowest took
0.629 s. No node fault was injected for this adapter verification. The adapter reports
controller-declared assignments; it does not independently prove generator fencing,
reserve spares, or complete combined-loss recovery.


## Idle archive-cache maintenance

Three consecutive cycles after the scratch/profile adjustment completed with 320
active VUsers and independent loaded target rates 3,174.3, 3,179.6 and 3,177.7
requests/s. Each scaled back to two without manual drain repair. The first cycle's
archive passed the PostgreSQL incarnation and four-source checksum checks. This
qualifies these accelerated clean cycles, not worker-loss recovery.

Storage inspection then found idle materialized caches at roughly 765 MiB on the
follower and 1.30 GiB on the leader despite the configured 256-MiB cache budget.
The initial asynchronous sweep could encounter pinned readers, while final releases
inside its one-minute coalescing window scheduled no later work. Without another
read, eligible idle files remained over budget indefinitely.

The coordinator now has one periodic maintenance worker for known cache roots.
The bounded notification queue still coalesces bursts; saturated notifications retain
the root for periodic retry. Eviction retains existing reader/reservation gates and
filesystem-identity checks. Regression tests exercise byte and TTL eviction after the
last release without another read, pending-reservation protection, and a saturated
wake queue. Test coordinators stop their workers before cleanup; the clock used by
the coalescing regression is atomic.

The deployed maximum test also held 640 queued VUsers at four Ready generator pods
with no target traffic. A subsequent missing-metric trial did not establish its
four-pod baseline and injected no fault: it started during scale-down at three pods,
then held three against demand 320. That ratio is inside the standard HPA 10% tolerance.
The strict admission boundary must continue rejecting insufficient actual capacity;
this edge remains a separate qualification item. A repeat starts from a verified
idle two-pod baseline. Failed evidence is preserved.


The clean-baseline missing-metric repeat passed: an empty required Prometheus query
held four pods for 75 seconds with zero target traffic; restoring the exact original
query resumed downscale to two. The initial failed attempt did not patch the query.

Cache-focused tests passed (4.412 s), the focused race run passed (5.825 s), and
five repeats of the retention race cases including pending-reservation protection
passed (7.147 s). The first full suite failed its ten-minute API timeout and two
short adoption timing checks. Its stack dump contained 14,527 VUsers blocked in
archive recording: the capacity-override HTTP handler test had dispatched 15,000
real VUsers without waiting for their teardown. That admission test now uses the
existing dispatch test boundary, checks the truthful INITIALIZING/202 response,
retained override authorization, and the exact dispatched run ID. Three focused
repeats including both real-generator adoption scenarios passed (21.073 s).
The test-isolation fix is committed separately as `cc926579`.


A second runner-level override test had the same uncontrolled 15,000-VUser launch;
it now verifies dispatch at the existing execution boundary (`cefb8225`). The
preceding full-suite rerun finished in 379.066 s with no execution assertion failures;
its sole failure was generated OpenAPI freshness while the following placement slice
was being authored. Contracts were regenerated before the final combined checks.

## Kubernetes-observed generator placement

Generator inventory now carries an optional pod-placement observation with immutable
pod UID, namespace/name, worker node, readiness and termination state. The Kubernetes
client and local/remote controller adapters preserve those fields. The inventory join
requires a literal target IP, the generator name and configured namespace; unknown
UIDs/nodes, ambiguous IPs and reused names at a new IP cannot produce placement.
DNS hints remain insufficient for this stronger join. Unready or terminating pods
remain observable, with their state preserved explicitly.

Placement is cleared on unavailable or disabled Kubernetes inventory and copied when
inventory is cached or returned, preventing stale/self-reported placement and caller
mutation. This uses the existing pod-list permission and bounded pod cache, not a new
cluster-wide node permission. It supplies observed topology for subsequent recovery
capacity work; it does not reserve spares, attest execution ownership or itself prove
that an old generator is fenced. Generated OpenAPI and TypeScript contracts include
the additive field. The focused client/controller/API checks passed (API 4.913 s),
and the placement/contract/start-test checks passed (2.488 s).


The combined cache/placement race checks passed (API 5.826 s, Kubernetes client
1.269 s). Dashboard typecheck and lint passed, as did eight relevant unit tests
and the production build. Existing browser workflows passed: three dev-admin
checks (Fleet capacity at 390/1440 px and Monitoring navigation, 2.5 s) and the
local-auth Monitoring check (7.8 s), each against an isolated real controller.
The generated field does not add a new dashboard control. The final full Go run
uses the completed contracts and embedded dashboard build.

The final full `go test ./...` suite passed, including API in 343.559 s.
Final source review found no further actionable cache or placement defect.

The production-timing cycle 13 is **not a pass**: the workload completed and
scale-down reached three pods, but a bounded TLS handshake to the independent
target timed out before the final two-pod observation. Its terminal projection
also contained 79 errors, despite clean fully-loaded samples. The original
autoscaling specification was restored by cleanup; the failed evidence remains
available for diagnosis.


## Atomic terminal archive availability

The five lifecycle status mutation entry points now commit an incarnation-bound
`archive_collection_unconfirmed` marker in the same SQLite/PostgreSQL transaction
as terminality when neither a catalog record nor publication intent exists. This
covers the crash interval before best-effort remote collection begins, including
pre-start failures and adoption failure using these lifecycle writes. A marker
write failure rolls back the status transition. Existing lease/materialization
predicates remain authoritative. Repeated writes preserve the original marker;
a subsequently observed collection failure replaces it, and publication evidence
supersedes it. This marker is not a collection retry job, a data-loss assertion,
or an implementation of durable partial-archive recovery.

Results distinguishes unconfirmed collection from observed failure, explains that
collection could still be running or interrupted, and retains the explicit warning
that checking availability does not restart collection. SQLite tests cover all five
entry points and three terminal statuses, stale owners/keys, idempotence, observed
failure precedence, publication precedence, rollback and reopen. The same 15-case
matrix passed three times against isolated PostgreSQL 16. The first Kubernetes
port-forward run passed all assertions but failed schema cleanup after its forward
closed; it is not counted as a pass.

The cache/placement image from `ebc3d918` was loaded by digest on both workers and
rolled out at idle with no restart or drain repair. The API's pod UIDs and node
names matched Kubernetes. Cycle 14 passed 2→4→2 at 3,180.2 requests/s and passed
incarnation-bound S3 archive checks. Cycle 15 failed closed before traffic because
coordinated-start persistence exhausted the commit safety window while local
builds/tests were also using this single Docker Desktop host. No fencing timeout
was weakened. Subsequent load qualification is sequenced after compilation.

The production cycle 13 archive passed integrity checks. Sampled failed archive
transactions report `context canceled` at the duration boundary; those records
remain errors, and no claim of zero terminal errors is made. The private long-trial
helper also now drains port-forward output continuously to avoid blocking the
forward on a full stdout pipe.

The full suite exposed an interaction with evacuation retry: its existing
"any failure record means collection finished" guard also matched the new
unconfirmed marker. The guard now recognizes only the two observed collection
outcomes, allowing unconfirmed work to collect. A dedicated regression starts
from a durable unconfirmed terminal row and verifies archive publication. The
new restart-readback fixture was also corrected to avoid startup reconciliation
of a deliberately incomplete active run before the transition under test.

Three deployed tolerance-edge trials passed with a 100-VUser hard pod capacity
and a 90-VUser scaling threshold. Observed Ready-three to Ready-four intervals
were 22.23, 25.75 and 27.76 seconds. Each retained zero target traffic and unchanged
controller identities/restarts, cancelled its queued run and returned to two.
These are queued capacity tests, not worker-failure or automatic-dispatch tests.

Marked idle materializations measured 196,685,914 bytes on one controller and
212,328,874 bytes on the other, each below 256 MiB without further reads. Total
staging disk use is larger because confirmed-upload sources without cache markers
are retained separately; the cache policy deliberately does not delete unmarked
publication sources. Cycle 14's terminal metrics recorded 242 errors; its pass
applies to clean fully-loaded samples and scale transitions, not zero errors over
the complete duration-boundary shutdown.

After that fix, the complete focused API race set, including fail-closed evacuation,
passed (17.608 s); storage race checks passed (12.741 s). Both Results browser
profiles passed (dev-admin 1.5 s, local-auth 8.2 s). Typecheck, lint, 25 Results
unit cases and the rebuilt dashboard passed. Final review also verified that both
catalog-publication transactions permanently remove superseded collection markers,
so archive retention cannot resurrect the old marker. The final full suite is run
against these completed sources without concurrent dashboard regeneration.

The final full `go test ./...` suite passed (API 326.006 s). The source review and
focused regressions found no remaining actionable issue in this terminal-marker
slice. Global documentation checking still reports pre-existing missing ignored
evidence links; neither modified readiness document appears among those errors.


## Reusable phased stack deployment

`deploy_stack.py` renders and deploys the private dependency/application/monitoring
profile into a fresh namespace in an already-created owned kind cluster with KEDA.
It verifies Docker container and Kubernetes node identities, refuses all existing
installation namespaces, binds the namespace UID returned by create, and rechecks
ownership between phases. The local kindnet adjustment uses strategic merge and a
bounded rollout wait. Authentication SQL executes with ON_ERROR_STOP before any
controller starts, and KEDA ownership is present in the initial ConfigMap. Errors
and interruption preserve resources and private phase evidence, without reset or
delete. Command failures never echo possibly credential-bearing manifests.

Review added returned-namespace identity binding, a CNI rollout readiness gate and
identity-bearing phase receipts. The 155-case Python suite passed (7.444 s),
including real OpenSSL/Kustomize rendering, interrupted/failed stages, stale owners,
container/namespace replacement and forbidden output reuse. A live read-only guard
check on the existing installation refused deployment with zero cluster mutations.
This command has not yet completed a fresh-cluster live deployment; readiness is
explicitly labelled `qualified: false`. Cluster/KEDA creation, image availability,
scenario seeding and the load/fault matrix remain separate steps. Final review
found no further actionable issue in this deployment-command slice.
