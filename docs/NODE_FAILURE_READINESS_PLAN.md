# Node-failure readiness implementation plan

Status: In progress on `codex/node-failure-readiness`; see implementation ledger below.
Date: 2026-09-27.
Baseline: application commit `72e17af3c24fdd46089792679f9b6cb1a105daf2`.
Evidence: local KEDA and node-failure report at
`artifacts/scalability/2026-09-27/REPORT.md` and machine-readable `summary.json`
in the same ignored directory.
These local evidence files are ignored artifacts, not distributed repository fixtures.
This plan extends [HA Run Continuity](HA_RUN_CONTINUITY_PLAN.md) to worker loss;
it does not equate successful controller-only adoption with combined controller/generator recovery.

## Implementation ledger

Reviewed implementation slices cover dependency-independent liveness and its bounded
watchdog, archive identity/integrity, fleet-scoped KEDA demand, fault-driver safety,
storage recovery guidance, and terminal observation labels. Combined leader/generator
recovery and production node-loss readiness remain unfinished.

| Package | Current implementation | Qualification still required |
|---|---|---|
| 1 | [Repository fault-driver foundation](../scripts/fault_harness/README.md): fresh credentials/TLS, explicit disposable targeting, sustained target-load gates with completion-based recovery deadlines and bounded observation calls, latched safety violations, counter continuity across outages, immutable-container cleanup, safety tests, and sanitized read-only Kubernetes inventory with optional preflight/post-restoration checkpoints and a separately bounded Kubernetes restoration-readiness gate, plus optional bounded background Kubernetes samples during the trial and an incarnation-bound apexctl final archive evidence command and an authenticated independent traffic-counter target with a bounded, verified snapshot reader, process-identity continuity enforcement, and private digest-pinned target and TLS PostgreSQL/Redis Kubernetes resources with local component validation, plus a bounded healthy-control comparison command and private TLS S3 resources with a signed retention/access checker | Automated stack provisioner and observation adapter, deployed scaling/dependency time series and repeated baseline/fault runs (private phased on-prem HA profile and one shared-archive replay trial now implemented) |
| 2 | Health uses local settings and cached inventory; bounded durable-write bypass remains; ten-minute idle PostgreSQL/Redis/generator outage passed on two HA controllers (601 requests each, p99 11.431/11.937 ms, zero failures/restarts); dependencies and readiness restored; bounded reusable HTTP sampler | Active-load/node-loss confirmation in the broader fault matrix |
| 3 | PostgreSQL provenance and comparisons use pgx microsecond precision; SQLite remains exact; real PostgreSQL reload regression | One fresh 40-VUser S3 archive passed incarnation/integrity checks before and after leader generation 6 → 7; repeated recorded-archive/fault replay remains |
| 4 | Fleet-scoped, expiring leader demand across all KEDA variants; peak queued demand; ordinal-floor protection; executable PromQL and cloud HA render tests; existing start-boundary placement and launch-window conflicts retain structured non-overridable HTTP 409 details | Spare-capacity admission, broader placement/discovery admission, deployed scale/failover qualification |
| 5 | Existing fail-closed behavior retained | Durable, fenced combined-loss recovery |
| 6 | [State inventory and fenced storage recovery runbook](NODE_STORAGE_RECOVERY.md) published | Multi-host CSI, permanent worker/zone trials and measured RTO/RPO |
| 7 | Terminal projections label samples historical; active API/WS observations carry a five-second deadline bounded by the oldest contributor; Command Center and advanced details expire samples locally; observed distributed archive collection failures have durable, incarnation-bound single-run API/WS records and an explicit Results notice; partial manifests preserve known collected/missing generator sources and exported report completeness, with an inspectable Results warning; fail-closed evacuation records collection failures and retains sources across in-process terminal-write retries | Atomic intent before collection on every terminal path, explicit recovery state and durable partial-result finalization |
| 8 | Go, race, deployment render, and PostgreSQL/S3 regression checks passed | Repeated fault matrix and external production qualification |

Validation details: [first-slice evidence](qa/reviews/node-failure-readiness-2026-09-27.md).

## Objective and scope

Make the documented, opt-in stateless HTTP recovery path survive loss of one worker,
including a worker hosting the active controller and running generators, without
uncontrolled duplicate load or incorrect success reports. Make autoscaling, archives,
and deployment configuration support that contract.

Keep strict failure as the default. Preserve current restrictions on stateful
protocols, allocated parameters, bounded iteration goals, and other scripts that
cannot safely restart a shard. Extending recovery to those workloads is a separate
project. Adding KEDA replicas does not by itself promise redistribution of an
existing run.

The release gate covers supported deployment profiles, not every possible topology.
Physical-node, storage, PostgreSQL, and Redis availability claims require a subsequent
multi-host environment; Docker Desktop results alone cannot establish them.

## Delivery sequence

| Order | Work package | Priority | Dependency | Deliverable |
|---|---|---|---|---|
| 1 | Reproducible fault harness and recovery contract | P0 | None | Repeatable baseline, safety assertions, documented budgets |
| 2 | Dependency-independent liveness | P0 | 1 | No cascading controller restarts on worker loss |
| 3 | Archive identity and timestamp precision | P0 | 1 | Existing valid archives readable after reload/failover |
| 4 | HA-safe autoscaling and reserved recovery capacity | P0 | 1 | Consistent demand, safe scale-down, eligible spare capacity |
| 5 | Combined leader/generator recovery | P0 | 2, 4 | Fenced, durable takeover and replacement of missing shards |
| 6 | Permanent worker/storage recovery | P0 for production node-loss claims | 1; validate with 5 | Supported storage profile and tested recovery runbook |
| 7 | Failure reporting and incomplete results | P1; required before release | 3, 5 | Accurate live/terminal state and explicit partial archives |
| 8 | Deployment parity and qualification | P0 release gate | 2–7 | Passing fault matrix and published limits |

Packages 2 and 3 can be developed independently. Package 6 starts with a storage
inventory while the controller work proceeds. Use separate reviewable PRs; do not
combine the entire change into one recovery rewrite. Assign owners when scheduling
implementation rather than treating this sequence as a dated delivery commitment.

## 1. Establish the recovery contract and reusable harness

**Work**

- Promote the useful parts of the local Python harness into repository-owned test
  tooling. Generate fresh credentials and certificates; do not commit the private
  snapshots, database dumps, or credentials from the investigation.
- Parameterize cluster/context, topology, replica counts, pod capacity, workload,
  fault timing, and recovery deadline. Require explicit targeting of the disposable
  cluster. Save configuration, image digests, timestamps, HPA decisions, events,
  logs, shard assignments, dependency state, and final results.
- Separate API outage, leadership acquisition, generator discovery, traffic recovery,
  pod eviction, and storage recovery measurements. Use an instrumented private target
  and assignment/generation telemetry to distinguish real traffic from stale samples.
- Make cleanup reliable on timeout/interruption: restore stopped nodes and any saved
  replica counts. Provide an explicit evidence-preserving failure mode.
- Define the budget from actual lease TTLs, orphan grace, RPC budgets, fencing waits,
  provisioning time, and HPA behavior. Record a proposed local traffic-recovery target
  of 120 seconds from fault injection, subject to validating the safe fencing bound.
  If that bound exceeds the target, revise the design or explicitly revise the target;
  never shorten fencing to make a timing test pass. Workloads must outlast that budget.

**Acceptance**

Reproduce both recorded failure modes, plus a healthy control run, on demand. Assert
that the test actually reached its requested load before injecting the fault. Detect
wrong-node injection, absent KEDA demand, unexpected pre-test restarts, and failed
cleanup. Preserve sanitized fixtures for regression tests independently of ignored
local artifacts.

## 2. Separate process liveness from dependency health

**Primary areas:** `pkg/api/handlers.go`, `pkg/api/readiness.go`, existing
`handlers_health_liveness*_test.go`, controller probes in Kustomize and Helm.

**Work**

- Trace the health handler's remote-generator checks and remove synchronous remote
  dependency work from the Kubernetes liveness path. Prefer a minimal process-health
  response; retain detailed dependency diagnostics separately or from bounded cached
  observations. Preserve API compatibility where existing clients need health details.
- Keep readiness specific to the operations a replica can safely serve. Distinguish
  follower read availability from leader execution admission; the loss of one generator
  must not automatically make every controller unavailable.
- Preserve existing detection of a genuinely wedged process. Do not replace the
  durable-write bypass with an indefinitely successful stale snapshot.
- If a new route is needed, update generated API contracts and all shipped probes.
  Use a startup probe if initialization legitimately exceeds normal liveness timing.

**Tests and acceptance**

Add unreachable/blackholed generators, slow DNS/RPC, unavailable PostgreSQL/Redis,
and held-lock cases. Check cancellation and bounded goroutines. Proposed local gate:
liveness p99 below 500 ms across a ten-minute dependency outage, with zero unintended
controller restarts; preserve a separate test showing that a genuine internal wedge
still trips the intended watchdog. Increasing the three-second probe timeout alone
is not the fix.

## 3. Fix archive lookup without weakening archive identity

**Primary areas:** `runArchiveManifestMatchesStoredTest` in `pkg/api/tests.go`,
archive provenance creation, PostgreSQL timestamp serialization, archive tests.

**Work**

- Verify the timestamp transformation at the pgx/PostgreSQL boundary, including
  rounding versus truncation at microsecond boundaries. Define one canonical persisted
  representation; do not introduce a broad arbitrary time tolerance.
- Apply that representation consistently when recording and comparing provenance.
  Preserve test/project identity checks and protection against same-ID reuse.
- Support existing nanosecond manifests and existing legacy manifests according to
  the established compatibility contract. Use a durable run-incarnation identifier
  for new archives if timestamp precision cannot preserve the required identity;
  specify a migration/versioning path before changing the schema.
- Keep integrity failures, missing objects, and identity mismatches distinguishable
  in internal diagnostics while preserving access-control behavior.

**Tests and acceptance**

Use real PostgreSQL round trips and S3-compatible storage. Cover nanosecond values on
both sides of microsecond boundaries, equivalent timezone offsets, zero/legacy
values, mismatched project/test IDs, same-ID reuse, corrupt checksums, controller
restart, and leadership transfer. Both recorded valid archives must return HTTP 200
through the API after reload, with verified checksums; mismatched archives must remain
inaccessible. Test SQLite compatibility too.

## 4. Make KEDA demand and recovery headroom explicit

**Primary areas:** `pkg/api/prometheus_exposition.go`, placement/capacity reservations,
`pkg/api/distributed_resilience.go`, Helm `templates/keda.yaml`, and all KEDA overlays.

**Work**

- Classify each metric as shared fleet demand, controller-local observation, or
  per-generator state. Deduplicate only replicated observations. Use stable fleet /
  installation identity so separate fleets cannot be collapsed by a global `max` or
  accidentally combined by an unscoped `sum`.
- Define handling for stale follower values, leader transitions, missing metrics, and
  Prometheus failures. Apply the semantics to queued, active, browser, saturation,
  and reservation-floor signals; do not blindly replace every `sum` with `max`.
- Reconcile scale-down protection: Helm and AKS already expose a reserved replica
  floor, while the inspected on-prem examples omit it. Preserve the StatefulSet
  ordinal floor required by retained reservations, not just the number of busy pods.
- Add an explicit recovery-capacity policy. Initially require eligible unused pods,
  because live evacuation currently excludes participating generators. Reserve enough
  compatible spare capacity outside the largest supported worker failure domain;
  one arbitrary spare is not sufficient for every placement. Account for heterogeneous
  capacities, protocol compatibility, project quotas, and concurrent runs.
- Prevent admission from consuming reserved recovery capacity and prevent HPA scale-down
  from removing it. Bound the policy by `maxReplicaCount` and available cluster resources.
  Report an unachievable recovery guarantee at preflight instead of silently promising it.
- Treat reuse of spare capacity on already participating generators as a later, separate
  enhancement requiring transactional shard resizing and duplicate-execution tests.
  Do not simply remove the current participation exclusion.
- Correct placement/discovery admission behavior exposed by preliminary HTTP 500s:
  validate real per-pod capacity, revalidate discovery at dispatch, and return structured
  capacity/conflict or retryable errors using existing API conventions.

**Tests and acceptance**

The same workload must produce the same desired replica count with one or multiple
HA controller scrape targets. Distinct fleets remain independent. Exercise real queued
work → scale-up → discovery → dispatch, active demand, maximum replica limits, missing
metrics, and scale-down. Demonstrate reserved headroom remains eligible on the surviving
node and that high-ordinal active/reserved pods are not removed. Render/test every shipped
Helm/Kustomize variant that enables KEDA, with both accelerated and production timing.

## 5. Recover combined controller and generator loss safely

**Primary areas:** `pkg/api/run_adoption.go`, `run_continuity.go`,
`distributed_resilience.go`, distributed epoch/supervision code, storage checkpoints.

**Work**

- Extend adoption reconciliation to classify each shard as reachable and adoptable,
  already terminal, temporarily unreachable, or missing beyond a bounded deadline.
  Adopt reachable shards without restarting their work.
- For opted-in, safe stateless groups, plan replacement of missing shards on eligible
  reserved capacity. Preserve strict failure for unsupported workloads or insufficient
  safe capacity, with a specific reason.
- Persist recovery intent, prior/new generation, shard identity, replacement mapping,
  progress, and completion state so a second controller failure can resume the operation
  idempotently. Keep credentials wrapped and generation ownership authoritative.
- Before replacement emits traffic, prove the old assignment is fenced or wait through
  the full safe lease/orphan-grace bound. A network timeout is not proof of death.
  Reuse and extend the existing evacuation/fencing primitives instead of constructing
  an independent unfenced restart path.
- Recompute remaining duration and pause/stage position from persisted state. Do not
  restart expired work, bypass stop requests, or claim missing measurements were recovered.
- Keep controller-only failover working independently of the new missing-shard path.

**Tests and acceptance**

Add failure injection at every transition: after adoption, after recording replacement
intent, after dispatch, before checkpoint commit, and during another leadership change.
Cover concurrent stop/pause, run expiry, no capacity, late generator return, network
partition/heal, repeated RPCs, and mixed generator versions/capabilities. Verify no
simultaneous old/replacement ownership, no repeated initialization caused by replayed
commands, and no traffic after terminal stop/expiry. The combined-loss scenario must
finish with accurate continuity/gap metadata and recover traffic within the agreed
budget when safe capacity exists. Fail boundedly and explicitly otherwise.

## 6. Define permanent-worker and storage recovery

**Primary areas:** generator StatefulSets/PVCs, controller placement, storage/archive
configuration, deployment documentation and platform-specific infrastructure.

**Work**

- Inventory what is stored locally: execution identity, supervision/recovery state,
  raw measurements, archive staging, and scratch files. Assign a durability requirement
  and recovery owner to each; these data classes need not use the same backend.
- Define supported profiles: local-path development with explicit loss limits, and
  production storage with verified node/zone attachment and recovery behavior. Do not
  assume a PVC, RWX access mode, or object storage alone solves execution fencing.
- Choose between safely reattaching the original identity and replacing a shard on a
  different identity. Document fencing, stale-volume attachment, and ownership checks
  before deleting stuck StatefulSet pods. Do not automate force deletion of an
  unreachable pod without proving the old writer cannot execute concurrently.
- Make archive staging/checkpoints recoverable where required. Specify partial-result
  behavior when the failed node's raw measurements are irretrievable.
- Ensure controllers and reserved generators span actual failure domains, and document
  how to restore that spread after a node returns without disrupting active runs.

**Tests and acceptance**

Permanently remove a worker through eviction and storage recovery; do not restart it
as the only recovery mechanism. Test an unavailable zone and storage detach/attach
failure in the selected multi-host platform. Verify identity fencing and new-run
availability, and measure traffic recovery separately from archive/data recovery.
Publish measured RTO/RPO per supported profile; do not claim zero measurement loss
from local generator files without a durable replication mechanism.

## 7. Report recovery, failure, and partial results accurately

**Work**

- Separate historical last-known metrics from current running VUsers. Mark missing
  samples stale and expose recovery state, last observation time, and loss/gap metadata.
  Clear or explicitly label terminal live fields so a failed run does not appear active.
- Audit finalization for failed/adoption-failed runs: both fault runs ended without a
  catalog entry or pending finalization job. Persist an idempotent finalization intent
  for every terminal path, or a durable explicit reason why an archive cannot be built.
- Where possible publish a partial archive containing surviving measurements and an
  exact missing-shard description. Never label incomplete results as complete evidence.
- Carry these semantics through REST, WebSocket, dashboard, and exported reports.

**Tests and acceptance**

Test terminalization during controller crashes, missing generators, S3 outages, retry,
restart, and duplicate finalization requests. Verify one durable terminal outcome and
an eventual archive or explicit durable failure state. Browser tests must show stale
and terminal metrics correctly under both supported authentication profiles; API-only
checks do not satisfy UI verification.

## 8. Qualification matrix and release gate

| Scenario | Required evidence |
|---|---|
| Healthy run; KEDA 2 → 4 → 2 and capped maximum | Correct demand, placement, completion, and protected scale-down |
| Queued work and insufficient maximum capacity | Automatic dispatch when capacity arrives; bounded truthful rejection otherwise |
| Controller-only abrupt loss | Adoption without restarting reachable shards |
| Standby-controller worker loss, with reserved spares | Safe missing-shard replacement; no liveness cascade |
| Active-controller worker loss, with reserved spares | Combined adoption/replacement; correct terminal and archive outcome |
| All pods participating; no eligible replacement | Accurate preflight limitation or explicit bounded failure, not promised continuity |
| Worker partition and late return | No overlapping ownership or duplicate replacement traffic |
| Second leader failure during recovery | Durable, idempotent recovery or explicit safe failure |
| Permanent worker loss | Tested rescheduling/storage procedure without relying on worker return |
| PostgreSQL primary failover; Redis primary failover | Valid execution fencing, consistent test state, measured API/fanout degradation |
| Archive access after restart/failover | API-readable valid archives; invalid identities/checksums rejected |
| Multi-host/zone outage and sustained load | Measured recovery limits and resource headroom in the production profile |

Run focused Go tests during development, race tests for the affected concurrent recovery
paths, and the full `go test ./...` before merging shared-infrastructure changes. Run
manifest render/Helm checks and the repository deployment tests. For dashboard changes,
run typecheck, lint, relevant unit tests, and affected Playwright workflows in both auth
profiles; broad/shared changes require `npm run test:cycle`. Regenerate API contracts
when routes change and run documentation freshness checks.

Repeat each automated local fault case at least three times from a clean baseline.
Require all safety assertions to pass every time; report the individual recovery
measurements, not only their average. Perform independent multi-host qualification
before widening the deployment's node/zone availability claims.

Roll out the new combined-loss behavior behind an explicit opt-in until qualified.
Retain the strict-failure path as the fallback. Test rolling-version compatibility,
checkpoint compatibility, and rollback while recovery records exist. A deployment
rollback must not leave generators running without an authoritative owner.

Completion means all P0 work and reporting gates pass for a named supported profile,
known limits are documented, and the original fault cases pass without hidden manual
intervention. Fixing only autoscaling or only controller election is insufficient.
