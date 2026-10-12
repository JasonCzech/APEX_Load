# Enterprise Fleet and Capacity

Status: Current

Consolidated generator fleet, autoscaling, capacity, and run-supervision reference. Merged from the former ENTERPRISE_GENERATOR_FLEET_MODEL, ENTERPRISE_AUTOSCALING_SIGNALS, GENERATOR_RUN_SUPERVISION, and POD_CAPACITY_RESERVATIONS documents. The platform architecture and data-platform runbooks remain separate: see the [platform architecture section](ENTERPRISE_PLATFORM_RUNBOOK.md#enterprise-platform-architecture-runbook) and [data platform operations section](ENTERPRISE_PLATFORM_RUNBOOK.md#enterprise-data-platform-operations-runbook).

Contents:

1. [Generator fleet model](#generator-fleet-model)
2. [Autoscaling signals and project capacity](#autoscaling-signals-and-project-capacity)
3. [Generator run supervision](#generator-run-supervision)
4. [Peak-based pod capacity reservations](#peak-based-pod-capacity-reservations)
5. [Plan mapping](#plan-mapping)

## Generator fleet model

This section defines the scale-out inventory contract for the APEX Load generator fleet.

### Fleet Inventory Contract

`GET /api/v1/system/inventory` is the fleet inventory surface. Each generator entry exposes:

| Field | Meaning |
| --- | --- |
| `target` | Controller-configured RPC target. |
| `generator_id` | Generator-reported identity when reachable; target address otherwise. |
| `region` | Placement region from controller target configuration. |
| `status` | Current reachability, such as `connected` or `unreachable`. |
| `version` | Generator binary/API version reported by heartbeat when reachable; target metadata or `unknown` otherwise. |
| `supported_protocols` | Protocol names the generator can execute, negotiated from generator heartbeat when reachable. |
| `compatible_features` | Negotiated feature/protocol surface used by fleet capacity responses and dashboard filtering. |
| `drain_state` | Scheduling state: `active`, `cordoned`, or `draining`. |
| `capacity_state` | `available`, `constrained`, `saturated`, `unknown`, or `unavailable`. |
| `health_score` | 0-100 rollup for operator scanning and dashboard sort order. |
| `available_capacity`, `active_vusers`, `max_vusers` | VUser capacity envelope. |
| `cpu_percent`, `memory_bytes`, `goroutine_count` | Runtime resource telemetry from heartbeat. |
| `cpu_limit_cores`, `memory_limit_bytes` | Generator pod/container resource envelope when reported. |

### Capacity State

Capacity state is intentionally simple at this stage:

| State | Rule |
| --- | --- |
| `unavailable` | Generator is not reachable. |
| `unknown` | Generator is reachable but does not report a max VUser envelope. |
| `saturated` | Generator is reachable but has no available VUser capacity. |
| `constrained` | Available capacity is at or below 20 percent of max VUsers. |
| `available` | Generator is reachable and above the constrained threshold. |

### Drain State

Operators can update generator placement state with `PATCH /api/v1/system/generators/drain`:

```json
{
  "target": "apexload-generator-0.apexload-generator:9090",
  "drain_state": "cordoned"
}
```

Supported states:

- `active`: eligible for new placements.
- `cordoned`: no new placements, existing runs continue.
- `draining`: no new placements and existing work should finish or migrate where supported.

Operator drain state persists through controller restarts in the metadata store.
The Kubernetes pod watcher adds a separate temporary `draining` exclusion while a
pod is terminating. A successful later inventory clears that temporary exclusion
when the pod is gone or its replacement is no longer terminating; inventory failures
retain the last exclusion. This allows KEDA to reuse StatefulSet ordinals without
inheriting shutdown state, while deliberate operator drains and cordons survive.
Older releases persisted both kinds of drain identically. After upgrading, review
any existing persisted drains before explicitly clearing obsolete ones; the upgrade
does not automatically discard potentially deliberate operator intent.

### Version And Protocol Compatibility

Reachable generators negotiate version and protocol capability through the heartbeat stream. The controller prefers those live values for inventory, placement, and compatibility decisions. Controller generator targets can still declare fallback version and protocol compatibility in `APEXLOAD_GENERATORS` for bootstrap and legacy generators:

```text
apexload-generator-0.apexload-generator:9090|region=us-east|version=0.1.0|protocols=http,grpc,websocket
```

Placement enforces the effective capability set before work is assigned:

- A group can only be placed on a generator whose `supported_protocols` contains the group's script protocol.
- A generator with an explicit incompatible major version is excluded from placement.
- `version=unknown` remains allowed for backwards compatibility with generators that do not report version metadata.

The inventory response exposes `compatibility_state` and `compatibility_issues` so operators can see why a target is excluded before a run starts.

### Placement capacity bounds

Legacy auto placement keeps each group on one eligible generator. Scaling the fleet
with KEDA does not implicitly split an oversized group. Use an explicit group-level
`specific_hosts` assignment for sharding, separate fitting groups, or enable peak pod
reservations below for reservation-aware partitioning.

Available generator VUsers are a hard placement limit even when peak reservations
are disabled. Explicit sharding respects each selected host's capacity, including
rounding and capacity already projected for preceding groups. Persistent Java must
also fit its advertised runtime process slots. Failed heartbeat probes provide no
available capacity. A fresh cached inventory shortage is a non-overridable structured
HTTP 409 at start; a cold inventory is checked by fresh execution discovery. The
complete discovered placement is checked before any group is staged. Reservation
mode retains its existing capacity queue and transactional claims. Legacy admission
is not an atomic reservation against concurrent launches; use peak reservations when
that guarantee is required.

### Saturation Metrics And Rejection Reasons

Prometheus exposes fleet rollups for dashboarding and alerts:

- `apexload_generator_capacity_state_count{region,state}`
- `apexload_generator_drain_state_count{region,state}`
- `apexload_generator_compatibility_state_count{region,state}`
- `apexload_generator_region_available_capacity{region}`
- `apexload_generator_region_active_vusers{region}`

Placement rejection messages now include compatibility causes when no target can run a group, including unsupported protocol and incompatible negotiated or declared generator version.

## Autoscaling signals and project capacity

This section defines the first HPA/KEDA signal contract for APEX Load scale-out workstream P2.2.

### Metrics

APEX Load exposes these Prometheus metrics for generator autoscaling:

| Metric | Labels | Purpose |
| --- | --- | --- |
| `apexload_scaling_vusers` | `state=queued|active` | Primary demand signal for pending generator capacity and currently allocated VUsers. |
| `apexload_scaling_tests` | `state=queued|active` | Run concurrency signal for controller/generator scheduling pressure. |
| `apexload_scaling_protocol_vusers` | `state`, `protocol` | Protocol mix signal for density-aware pools. |
| `apexload_generator_capacity_state_count` | `region`, `state` | Saturation signal for available, constrained, saturated, unknown, and unavailable generator capacity. |
| `apexload_generator_region_available_capacity` | `region` | Regional headroom signal. |
| `apexload_generator_region_active_vusers` | `region` | Regional allocation signal. |
| `apexload_capacity_available_generators` | `scope=configured|effective|live|reserved|shared` | Controller capacity-planning envelope, including reserved and shared generator pools. |

`queued` includes tests in `QUEUED` and `PENDING`. `active` includes `INITIALIZING`, `RUNNING`, and `PAUSED`, because paused tests may still reserve execution capacity.

### HPA/KEDA Policy

Recommended autoscaling policy:

1. Scale out when `apexload_scaling_vusers{state="queued"}` remains above zero for multiple scrape intervals.
2. Scale out faster when queued VUsers are concentrated in protocols with lower density, especially browser canaries.
3. Hold or scale out when active VUsers approach regional available capacity.
4. Avoid scaling on `unavailable` generators alone; use it as an alert and replacement signal.
5. Scale in only when queued VUsers are zero and active VUsers remain below the pool's lower utilization target for a cooldown window.

For KEDA, scrape the controller `/metrics` endpoint into Prometheus and use the
Prometheus scaler with queued and active VUsers as trigger queries. The AKS and
on-prem autoscaling overlays define the production ScaledObjects; the local
Kubernetes KEDA file remains a reference artifact and is not included in the
fresh-cluster local base.

Example trigger query:

```promql
max(apexload_fleet_autoscaling{fleet="your-installation",signal="total_vusers"} and on(fleet,job,instance) (apexload_fleet_autoscaling_valid_until > time()))
```

Regional diagnostic query (not an HA autoscaling trigger):

```promql
sum by (region) (apexload_generator_region_active_vusers)
```

### Cluster Artifacts

The KEDA `ScaledObject` for `apexload-generator` defines:

- `minReplicaCount: 2` and `maxReplicaCount: 50`, matching the enterprise scale-out envelope.
- Total queued plus active VUsers and separate active-demand triggers.
- Total queued plus active browser VUsers and separate active-browser triggers.
- Saturation and retained StatefulSet ordinal-floor triggers.
- All queries select the installation's fleet ID, discard expired observations, and deduplicate HA observations with `max`; see HA autoscaling observations below.
- Conservative scale-down stabilization so recently queued demand does not collapse the generator pool too quickly.

The default Prometheus endpoint is `http://prometheus-operated.monitoring.svc:9090`; production overlays should replace it when Prometheus uses a different service name or namespace.

### Project Generator Reservations

System settings now include project-level reserved generator pools under `capacity_planning.reservations`:

```json
{
  "capacity_planning": {
    "availability": {
      "source": "configured",
      "configured_available_generators": 5
    },
    "reservations": [
      {
        "project_id": "critical-checkout",
        "reserved_generators": 2
      }
    ]
  }
}
```

Capacity advice evaluates reservations against the test project:

- Tests from a reserved project can use the shared pool plus their reserved generators.
- Tests from other projects only see the shared pool after all reservations are removed.
- Advisory responses include `reservation_project_id`, `reserved_generators`, and `shared_available_generators` so schedulers and dashboards can explain capacity shortfalls.
- `/api/v1/system/health`, `/api/v1/fleet/capacity`, and `/metrics` expose reserved/shared capacity rollups for operator visibility.

The fleet capacity API also includes `project_active_slots`, `project_pending_slots`, and `project_slots[]` so operators can see how much active and queued demand each project is contributing to the shared or reserved pool.

### Fair-Share Queue Promotion

Pending test promotion now uses a project-aware fair-share order:

1. Prefer a project with reserved generator capacity that is not yet consumed by that project's active tests.
2. Then prefer the project with fewer active tests.
3. Then prefer the project with fewer active VUsers.
4. Use pending age, creation time, and test ID as deterministic tie breakers.

This keeps old FIFO behavior as the final tie breaker while preventing one busy project from indefinitely occupying every newly available start slot. Capacity shortfall checks still run before promotion, so non-reserved projects cannot consume generator capacity that has been removed from their shared-pool advisory. `/api/v1/fleet/capacity` publishes the same project-level slot accounting used to explain contention in the Monitoring capacity card.

### Protocol Density Envelopes

Capacity advice now publishes protocol-specific density envelopes in `capacity_advisory.protocol_densities` and uses them when computing VUser-driven generator recommendations. Each envelope also carries planning metadata: `benchmark_profile`, `benchmark_evidence`, `calibration_source`, and `calibrated_at`, so API consumers can distinguish design targets, calibrated models, and conservative fallback assumptions. These are planning envelopes: the HTTP figure is a **design target** (not yet benchmarked) and the others are calibrated models, not measured results. Treat any value as authoritative only once a deployment publishes local benchmark evidence.

| Protocol | Max VUsers / generator | Confidence | Notes |
| --- | ---: | --- | --- |
| HTTP / HTTPS / HTTP/2 | 10,000 | Design target | Baseline goroutine-per-VUser target; not yet benchmarked. |
| HTTP/3 (`3` or `auto+h3`) | 4,000 | Unmeasured | Groups whose requests resolve to an HTTP/3 policy, after group and scenario overrides, form the `http3` class. QUIC moves congestion control and encryption into the generator and adds one UDP socket per VUser, so the HTTP design target is never reused: the class takes the conservative envelope, with a warning, until matched HTTP/2 and HTTP/3 measurements are published. Generators also check their open-file limit against the planned sockets at startup. |
| gRPC | 12,000 | Calibrated model | High-density HTTP/2 transport with compact payloads. |
| WebSocket | 6,000 | Calibrated model | Persistent connection footprint lowers density relative to stateless HTTP. |
| SOAP | 5,000 | Calibrated model | XML parsing and larger payload assumptions. |
| Java-over-HTTP | 7,000 | Calibrated model | HTTP-family behavior with heavier client/runtime assumptions. |
| JMS / Solace / IBM MQ | 3,000 | Calibrated model | Broker/session resources drive lower default density. |
| Browser canaries | 1-5 (2 recommended) | Acceptance contract | Real browser runtime canaries, not HTTP-scale VUsers; raise the cap only with release-attached evidence for the exact image and hardware. |
| Unknown | 4,000 | Conservative | Applied with a warning until the protocol has an explicit envelope. |

The browser row is the one envelope that is a governance contract rather than a resource model: `capacity_advisory.max_browser_vusers_per_generator` is derived from the same diagnostic-canary cap that the `browser_density_release_gate` enforces, so the advisory never sizes a fleet at a density the gate would refuse. The internal device-tier model (low/mid/high) still sets the per-browser-VUser CPU, memory, and network footprint used for the CPU/memory/network recommendations, and its higher per-tier ceiling becomes claimable only on the bounded real-browser-load tier, i.e. after benchmark evidence is attached and `APEXLOAD_BROWSER_DENSITY_OVERRIDE=true` is set.

Inside a generator, browser sessions are also admitted at run time. A new browser context opens only when projected memory plus about 128 MiB for the new context stays under the browser memory limit: `APEXLOAD_BROWSER_MEMORY_LIMIT_MB` when set (0 disables; an invalid value is logged and ignored), otherwise 90% of the cgroup memory limit or of physical memory. Projected memory is the larger of the latest one-second sample of the resident memory of the Playwright driver, its browser processes and the generator (read from `/proc` on Linux, so images without procps `ps` are measured; elsewhere from `ps`; a driver missing from the process list is a failed measurement) plus 128 MiB for every context admitted since that measurement started, and 128 MiB for every context still open (a fresh context is nearly blank when sampled, so live contexts keep that floor until they close). If memory cannot be measured, or the last sample is more than ten seconds old, only the 128 MiB floor for every open context applies; such contexts are counted as unmeasured, and each new run of measurement failures is logged. A refused context does not fail the run or the browser's initialization: the VUser keeps running, each of its actions fails at once with a `browser resource limit (browser_memory_admission)` error, classified as `resource_limit` in the error taxonomy rather than as a target failure, and it tries admission again at its next iteration boundary once five seconds have passed since the refusal, so it rejoins the run with a fresh context when memory recovers. Each refused iteration waits out that backoff, so a refused VUser does not spin. Init-phase actions (for example a LoadRunner `vuser_init` section) run on the refused context and fail, which ends that VUser as any failed initialization does. A context admitted at an iteration boundary that then cannot open ends that VUser only: the generator logs `iteration setup failed` and records no result for the iteration, so the lost VUser shows in the running-VUser count rather than in the error breakdown (its teardown actions, if any, still run and fail as `browser protocol is not initialized`). Refusals log once per run of refusals per session; the refusal counter counts admission attempts. Each HAR-recording session reserves its full 100 MiB allowance before it opens, against a generator-wide 1 GiB budget, which allows ten concurrent HAR writers per generator (the 128-artifact cap does not bind first), and free scratch space minus 512 MiB of headroom; a refused writer runs without HAR and reports `browser_har_unavailable_<reason>` (`har_budget_exhausted`, `har_scratch_headroom`, or `har_scratch_unavailable` when the HAR directory cannot be inspected). A reservation is held until its writer has finished producing the raw and scrubbed copies, even when that outlasts the session's close. While writers are active a one-second watchdog samples the same memory, because Playwright buffers a recording until its context closes, and closes the fully open recording with the most observed bytes when the limit is exceeded; sessions already closed for a limit or still opening are never chosen. A recording closed for its own size or by the watchdog fails the affected VUser explicitly (`browser_resource_limit_<reason>`) and censors its unfinished document observations with the `resource_limit` boundary; the session does not reopen, its remaining actions in that iteration fail at once, and the VUser ends at its next iteration boundary (logged as `iteration setup failed`) rather than failing every action for the rest of the run; with a new user per iteration, the replaced session is admitted afresh instead. Context and HAR-writer refusals, size-limit and watchdog closures, and measurement failures are logged with the generator's cumulative evidence counters; the counters are not yet sent to the controller. The budget covers writers that are recording or finishing; HAR artifacts already retained on disk are bounded only by the retention sweep.

The advisor applies the configured headroom percentage to protocol envelopes before calculating required generators. For mixed workloads, the advisory includes one envelope per protocol and the generator recommendation is the maximum of CPU, memory, network, global VUser density, and protocol-specific density.

`GET /api/v1/scripts/web-api/support-matrix` republishes these envelopes for evaluator-facing Web/API parity evidence alongside protocol diagnostics and sample-project references. Capacity planning remains anchored here so execution support claims and scheduler/fleet advice do not drift.

## Generator run supervision

Distributed runs are fail-closed. Every current controller assignment carries a
random run token and every current generator requires a renewable lease for that
exact test, execution generation, and token. If renewals stop, the generator
fences the run locally; it does not wait for a controller command that may never
arrive.

The shipped contract is:

- lease TTL: `60s`
- controller renewal interval: `10s`
- graceful abandonment drain: `5s`
- hard cancellation grace: `1s`, followed by process self-termination when work
  still cannot be stopped

The controller accepts TTLs from 30 to 60 seconds and requires the generator's
granted TTL to cover at least three configured renewal intervals. The generator
accepts abandonment drains from 0 to 10 seconds. Kubernetes, Helm, and Compose
ship the 60/10/5 profile above. There is no legacy opt-out: a generator that does
not advertise `run_supervision_v1` is excluded from new distributed runs.

`assignment.tolerate_generator_loss` is a separate evacuation feature. It is
rejected unless the controller uses HA execution leadership and the leadership
lease is backed by the same durable test store. A single controller cannot
prove that replacement shards and a lost source belong to one execution epoch.

### Configuration

| Process | Setting | Shipped value |
| --- | --- | --- |
| Controller | `APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL` | `60s` |
| Controller | `APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL` | `10s` |
| Generator | `APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL` | `60s` |
| Generator | `APEXLOAD_GENERATOR_ABANDON_DRAIN` | `5s` |
| Generator | `APEXLOAD_GENERATOR_MAX_ADOPTION_GRACE` | unset (`120s`, the hard maximum) |

`APEXLOAD_GENERATOR_MAX_ADOPTION_GRACE` caps HA run continuity: how long a run
whose controller is lost may keep generating load while it waits for a
successor controller to adopt it. It only applies to runs a controller staged
with an adoption grace (`assignment.failover_policy=continue`). Unadopted runs
stop `FAILED` with `controller_adoption_timeout`. Combined with
`tolerate_generator_loss`, a generator lost while no controller is leading fails
the run (it is not evacuated); losses while a leader is present are evacuated
as usual. A generator cannot tell a lost controller from a network partition,
so a generator cut off from a live controller also keeps a continue-policy run's
load going for up to the grace before stopping; generator-loss evacuation waits
out that grace before starting a replacement, so the shard never runs twice. `0` disables orphaning on the
generator, and values above `120s` stop startup. See
[HA_RUN_CONTINUITY_PLAN.md](HA_RUN_CONTINUITY_PLAN.md).

Keep the controller TTL equal to the generator TTL. A generator may grant less
than requested, but the controller rejects that claim when it would leave fewer
than three renewal opportunities. Generator fencing state and terminal evidence
must use the retained per-ordinal state volume; Compose uses `generator-data`
and the production StatefulSets use `generator-state` PVCs.

Before acknowledging the first supervision claim in an execution generation,
the generator durably arms a restart fence in that execution-epoch state. A
clean exact terminal transition disarms it only after every supervised
assignment and retained drain in the generation has quiesced. Semantic lease
expiry keeps it armed even when cooperative cancellation succeeds. After a
restart, an armed positive generation cannot be revalidated by its old owner; a
higher HA generation is required. Generation zero has no owner epoch, so an
armed generation-zero fence rejects new staging and commits until a positive
generation is durably acquired. Already-active unrelated generation-zero runs
in the original process may continue renewing while they quiesce normally.

Helm includes the supervision values in the controller pod-template checksum,
so changing either controller value rolls the Deployment. The static Kustomize
overlays reference an ordinary ConfigMap; after a config-only apply, restart and
wait for the controller Deployment before admitting tests:

```bash
kubectl -n <namespace> rollout restart deployment/apexload-controller
kubectl -n <namespace> rollout status deployment/apexload-controller
```

Changing the generator TTL or drain edits its StatefulSet pod template and
therefore rolls the generator fleet. Apply the no-active-tests upgrade gate
below whenever either side of this fail-closed contract changes.

### Upgrade and rollback gate

This contract is intentionally incompatible with tokenless controller or
generator releases. Use a maintenance window for the first upgrade:

1. Close run admission and let every active distributed test reach a terminal
   state. Do not rely on a rolling mixed fleet for active traffic.
2. Upgrade the complete generator fleet first while the old controllers remain
   admission-closed. Wait for every desired generator pod to become ready, then
   inspect system inventory and confirm every schedulable generator advertises
   `run_supervision_v1`.
3. Upgrade every controller only after the generator verification succeeds.
   Keep admission closed until the controller rollout is complete and healthy.
4. Execute the connectivity-loss drill below, then reopen run admission.

Use the same no-active-tests gate when rolling back to a release that does not
implement the contract, in reverse order: roll back every controller first,
then roll back the complete generator fleet, verify both rollouts, and only then
reopen admission. Rolling back only one side leaves the fleet deliberately
unavailable for new runs.

### Probes and restart behavior

Kubernetes readiness and liveness probes use the generator's dedicated gRPC
health listener. They are useful for removing a draining process and restarting
a failed process, but they do not prove controller connectivity and are not the
runaway-load safety mechanism. Adding a controller-connectivity healthcheck
would be misleading and could restart healthy generators during a transient
control-plane interruption before their per-run lease decision is due.

Compose relies on `restart: unless-stopped` after the generator exits. It does
not add a second connectivity healthcheck. The renewable run lease remains the
authoritative signal in both environments.

### Acceptance drills

Run these against a non-production target before reopening admission:

1. Start a load test and confirm each assigned generator advertises
   `run_supervision_v1`.
2. Drop only controller-to-generator control traffic while leaving the target
   system reachable. Do not delete the generator pod.
3. Confirm the generator logs `generator_run_supervision_expired` after the TTL,
   stops producing target traffic, and completes the five-second graceful drain.
   If a VUser refuses to stop, confirm `generator_run_abandon_escalated` and a
   non-zero process exit. With the shipped profile, target traffic must cease no
   later than roughly 73 seconds after the last successful renewal, including
   the external runner's owner-lease and cleanup-exit backstop.
4. Restore connectivity and confirm the replacement pod is ready. The old run
   token must not revive the fenced incarnation.
5. Repeat with one generator isolated in a multi-generator run. With loss
   tolerance disabled, the controller fails closed. With HA and explicit loss
   tolerance enabled, replacement work must not start until the source's
   process-fence deadline has passed. That deadline uses the maximum accepted
   10-second drain plus the longer post-drain containment path (currently the
   external runner's five-second owner lease and two-second cleanup exit), plus
   a scheduling margin.
6. In HA, force leadership loss and confirm the old leader cancels and joins its
   renewal keeper and sends token-fenced stop requests before closing clients.
7. After a hard escalation, restart both the generator and controller. Query
   `GET /api/v1/system/inventory` and confirm that generator's
   `safety_exits` still contains the exact test and execution generation with
   `lifecycle_state: FAILED` and `reason: controller_supervision_lost`. Repeat
   the incident with another run and confirm both entries survive both restarts.
   The generator retains a bounded journal of the latest 64 token-free incidents;
   `last_safety_exit` is only the rolling-upgrade projection of its newest entry.
   This heartbeat evidence is authoritative when an interrupted generator archive
   could not reach its normal finalization path.

Alert on either safety counter:

```promql
increase(apexload_generator_run_supervision_expirations_total[5m]) > 0
increase(apexload_generator_run_supervision_escalations_total[5m]) > 0
```

The same counters are available on each connected generator entry in the system
inventory response. An expiration is an operational incident even when the
automatic fence succeeds; an escalation means the process safety exit was
needed.

## Peak-based pod capacity reservations

Peak reservations are logical admission budgets inside a generator process.
They do not create separate containers, CPU quotas, memory heaps, or garbage
collectors for individual tests. Live CPU and memory utilization remain separate
from reserved capacity.

### Enablement

1. Upgrade generators first, then controllers. Generators advertise
   `pod_capacity_v1`; old generators cannot accept partitioned launches.
2. Drain existing unreserved tests. A generator with legacy assignments is
   reported as unreconciled until those assignments finish.
3. Configure network capacity, maximum VUsers, and headroom in Capacity Planning.
   CPU and memory use the lower of the configured budget and each pod's reported
   cgroup limits. Missing resource limits
   make partitioned capacity unavailable. Network capacity remains a configured
   estimate, not a measured link guarantee.
4. Enable **Enforce peak pod reservations** in the generator capacity settings
   (`capacity_planning.pod_reservations_enabled`). It defaults to false for
   rolling-upgrade compatibility. Deploy the updated KEDA manifests as well.

Enforcement cannot be disabled while the ledger holds reservations. Drain tests
and wait for confirmed release before disabling it.

The default sharing mode is `shared`. Set test-level
`assignment.sharing_mode` to `dedicated` to claim the entire schedulable budget
of each selected pod. Dedicated execution isolates tests at pod level, not from
other pods on the same node. Existing domain, pool, entitlement, and project
restrictions still apply.

### Accounting

The scheduler checks CPU, memory, network, total VUsers, and per-protocol VUsers
independently. It applies the configured headroom once, then reserves each
assigned group's estimated peak. Group peaks are conservatively summed even
when their scheduled peaks occur at different times. Model estimates are not
published benchmark guarantees.

Adaptive tests require an explicit maximum VUser count. Because adaptive group
weights can change, v1 reserves that maximum for each load-bearing group and
sums those group peaks; observer groups retain their configured peaks. This can
be deliberately more conservative than a fixed weighted split of the maximum.

With 20% headroom, the schedulable budget is 80% of raw capacity. A test using
40% of raw capacity in its limiting dimension therefore shows **50% reserved /
50% remaining**, with **20% headroom excluded**. A different workload must fit
every remaining dimension; the headline percentage alone cannot establish fit.

Reservations survive ramp changes, pause, and teardown. Runtime increases
acquire a revisioned capacity delta before changing VUsers. Model coefficients
stay frozen for the run. Failed changes roll back the capacity delta only after
the generator confirms the earlier envelope is safe; uncertain responses retain
capacity until reconciliation.

The launch preview reports each proposed pod's before/after budget. It does not
reserve capacity. Launch revalidates and atomically records the full placement;
capacity contention queues the test. Compatible pods are packed before idle
pods are used, subject to existing placement preferences.

### Recovery And Operations

The durable `pod_capacity_reservations` ledger records pending, committed, and
releasing claims. Storage transactions use the existing execution-owner and
capacity-settings fences. The generator independently enforces the envelope and
group peaks under its admission lock. No test IDs from this ledger are exposed
in the aggregate capacity display.

Each launch attempt also has a unique reservation identity. Delayed cleanup from
an earlier queued attempt cannot release a later attempt's capacity.

Release requires confirmed execution drainage or an incarnation change for a
terminal/releasing claim. A predicted end time never releases capacity. A stale
or unreconciled snapshot blocks new partitioned admission. The inventory refresh
worker retries release after completion and controller recovery.

Evacuation reserves replacement peaks before staging replacement work, retaining
the source claim until its execution is fenced. A group whose future peak cannot
be mapped to a replacement is refused rather than losing its capacity guarantee.

The KEDA `apexload_pod_reserved_replica_floor` metric protects reserved StatefulSet
ordinals from scale-down; active VUser scaling demand also includes held peaks.
For KEDA-owned generator fleets, each supervised archive source also records a
durable floor for its StatefulSet ordinal before dispatch. The leader includes
the highest pending source floor in the same fleet-scoped KEDA signal after
execution becomes terminal, until collection resolves or exhausts. The hold is
bound to the collection intent and StatefulSet UID, so a replacement workload
cannot inherit an old floor. KEDA-owned starts require fresh, identified pod
inventory and a valid fleet ID; while a hold is pending, unavailable workload
inventory suppresses the demand signal rather than publishing a zero floor.
`apexload_pod_capacity_demand` reports reserved and queued resource vectors for
capacity monitoring (CPU cores, memory MiB, network Mbps, and VUser counts).
Non-ordinal targets use the configured fleet size as a conservative floor.
Managed pool scale-down waits for retained reservations. External/manual pod
deletion is outside this logical admission mechanism.

### Validation

Focused tests cover capacity arithmetic, half-capacity concurrency, duplicate
claims, dedicated exclusion, active-release rejection, immutable model versions,
and transaction rollback. Run the repeatable concurrent SOAP probe with:

```sh
APEXLOAD_POD_CAPACITY_SOAK=1 go test ./pkg/distributed/generatorrpc -run '^TestPodCapacityConcurrentSOAP$' -count=1 -v
```

This probe runs two SOAP tests with eight active VUsers each, 2 KiB responses,
and independently held peaks for 30 seconds. Its heap samples describe the local
test process; they do not validate a 10,000-VUser production density or prove the
absence of a long-duration memory leak.

## Plan mapping

Combined status of the fleet (P2) workstream items.

| Plan item | Status | Artifact |
| --- | --- | --- |
| P2.1 Formal fleet model | Complete for first-pass negotiated inventory | Inventory fields and the fleet model section |
| P2.2 Drain/cordon workflows | Complete for first-pass persisted placement control | `PATCH /api/v1/system/generators/drain`, persisted drain settings |
| P2.3 Version compatibility checks | Complete for heartbeat-negotiated and target-declared protocol/version compatibility | `version`, `supported_protocols`, `compatible_features`, `compatibility_state`, placement rejection reasons |
| P2.4 Saturation alerts and rejection reasons | Complete for API, metrics, and dashboard alert cards | Fleet Prometheus metrics, fleet alert cards, and placement rejection messages |
| P2.5 HPA/KEDA scaling signals | Complete for first-pass Prometheus signal contract and KEDA generator scale-out artifact | `/metrics`, the autoscaling section, `deployments/k8s/local/generator-keda-scaledobject.yaml` |
| P2.6 Capacity reservations | Complete for settings, advisory, health, metrics, fleet API, and Monitoring visibility | `capacity_planning.reservations`, capacity advisory fields, `/metrics`, `/api/v1/fleet/capacity` |
| P2.7 Fair-share placement | Complete for reservation-aware pending promotion and project slot accounting | Project-aware pending promotion, `/api/v1/fleet/capacity` |
| P2.8 Protocol density envelopes | Complete for code-owned advisory envelopes; values remain design/model inputs until local benchmark evidence is published | `capacity_advisory.protocol_densities` with benchmark metadata |

## HA autoscaling observations

KEDA uses `apexload_fleet_autoscaling{fleet,signal}` from the fully reconciled
execution leader. Existing `apexload_scaling_*` metrics remain diagnostic observations
and must not be summed across HA replicas to drive scaling. The fleet signal reports
active plus queued peak VUsers for total demand (including stage/adaptive admission
reservations), active VUsers separately, the corresponding
browser demand, the sum of saturated generators across regions, and the retained
StatefulSet ordinal floor. New replicas are not automatically assigned to existing runs.

Set `APEXLOAD_FLEET_ID` to a stable installation identifier shared by its controllers
and unique across every installation in the Prometheus query domain. The AKS/EKS render
scripts require this value and substitute it into both controller configuration and
KEDA queries. For manually rendered on-prem manifests, replace the placeholder in both
places. Helm derives `<namespace>/<release>` by default; set `keda.fleetID` to a cluster-
specific identifier when multiple clusters share Prometheus. Local examples use
`apexload-local`; change that ID and the selector together when cloning an installation.

Each observation expires after at most 30 seconds, and earlier when the current
execution lease expires. A follower, reconciling leader, changed term, or failed
reservation-store read emits no fleet observation. The PromQL filters expiry and takes
`max` within the selected installation, preserving replicated observations without
multiplying them. The queries return no value when evidence is missing. All triggers
set `ignoreNullValues: "false"`; this surfaces unavailable evidence instead of presenting
it as idle demand. Keep Prometheus and controller clocks synchronized and scrape every
15 seconds or faster. The reservation floor protects retained ordinals; reserved
recovery headroom is opt-in through the admission policy below.

The [KEDA Prometheus scaler documentation](https://keda.sh/docs/2.18/scalers/prometheus/)
describes missing-result behavior. The executable regression cases live in
`scripts/testdata/keda-promql.json`; run `promtool test rules` against that file. They
cover duplicate observations, stale followers, independent installations, expiry,
browser demand, saturation, and the ordinal floor. Deployment tests ensure every shipped
query matches a tested expression.


### Reserved worker recovery capacity

For fenced stateless HTTP runs, enable peak pod reservations and set
`assignment.tolerate_generator_loss: true` plus a positive
`assignment.recovery_spare_vusers` budget. The scenario builder exposes this as
**Recovery spare VUser budget** under Controller/generator. Zero preserves ordinary
placement. The budget is an integer quota envelope, not additional generated traffic;
it is charged alongside peak workload demand to test, project, pool and fleet VUser
limits. Group-level budgets, weighted/source-affinity placement and bounded iteration
workloads are rejected.

Admission requires fresh Kubernetes pod UID, node and workload identity, a current
KEDA/HPA maximum, and compatible idle generators supporting standby reservations.
The controller matches each source to a spare on another worker, checks every
single-worker contingency and reserves the union of full dedicated spare budgets.
For example, two source pods on one worker with two 100-VUser spares on another
require a spare budget of at least 200 and a maximum of at least four replicas.
Preview reports the footprint; dispatch rechecks it and reserves all source/spare
claims in one lease-fenced database transaction before staging work. Unconfirmed
acknowledgments retain capacity for ordinary cleanup rather than releasing uncertain
claims. Queued demand includes the envelope and a bounded whole-pod replica floor.

The frozen reservation records bind spares to permitted sources, pod/workload identity
and worker placement. Evacuation revalidates those identities before activation;
a changed pod or stale observation fails closed. Scaling within the original frozen
peak remains supported; exceeding it requires a new run and admission. Idle spares
remain outside execution metrics and archive participant sets and are retained by
the normal reservation scale-down floor.

This policy enables reserved capacity for the existing generator-evacuation path.
Combined controller/generator loss still needs durable recovery intent and takeover
reconciliation. Physical host, zone and storage recovery require separate multi-host
qualification; a Docker Desktop worker-container trial cannot establish those claims.
