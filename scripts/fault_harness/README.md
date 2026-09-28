# Disposable node-fault driver

This is the repository-owned safety/evidence foundation for the
[node-failure plan](../../docs/NODE_FAILURE_READINESS_PLAN.md). It renders a phased local HA stack and includes a bounded deployed-run observation
adapter. A bounded deployment command now applies that stack to a fresh namespace of an owned kind cluster. Scenario seeding now uses the owned, authenticated API. A fresh-cluster bootstrap command creates the kind/KEDA prerequisites; fresh end-to-end deployment and the complete fault matrix remain to be qualified. Docker Desktop recovered after an authorized restart; target and
PostgreSQL/Redis component checks now pass in a fresh three-node kind cluster.
A ten-minute idle dependency outage also passed on two HA controllers; see the
[review ledger](../../docs/qa/reviews/node-failure-readiness-2026-09-27.md) for scope and measurements.
Full application fault qualification remains open. Unit tests use a fake command
boundary; fresh certificate tests execute OpenSSL.

```sh
python3 -m unittest discover -s scripts/fault_harness -p 'test_*.py'
python3 scripts/fault_harness/prepare_inputs.py /absolute/new/private-directory
```

The second command refuses an existing directory and generates fresh credentials,
a private CA, controller/generator/target/PostgreSQL/Redis/S3 certificates, a three-node kind configuration and
a random ownership record, and a separate target/observer credential pair in `target.json`. Nothing is copied from earlier deployment snapshots.
The directory must remain private; do not commit it or include its credentials in
public evidence. Certificates last two days. Install the generated values through
Secret resources, with the CA trust and server names required by your deployment.
Generated certificates include subject/authority key identifiers for strict TLS
verification. Generate a new private directory to replace older inputs; preparation
never rewrites existing credentials or certificates. The credentials file is input
data, not an environment file.

Create the disposable kind cluster with the exact name in `ownership.json`, write
its kubeconfig into that private directory, and deploy the test stack in namespace
`apexload-fault`, labelled with `namespace_label` from the ownership record. Keep the
control plane and instrumented target independent of the selected failed worker.
Use at least two worker nodes, multiple generator pods on each, PostgreSQL, Redis,
object storage, Prometheus and KEDA. Pin and record images. Deploy the rendered stack and configure the observation adapter below before an
end-to-end run.

Check host free space and Docker storage before building or importing images. A
local trial exhausted host storage and left zero-byte application binaries in an
already registered node image cache. A pinned image digest alone does not prove
that extraction survived an I/O failure. After recovery, verify workload startup
on every worker and establish a new baseline before collecting fault evidence.
Keep private evidence and PVCs when repairing an owned node's image cache; a
Docker data reset is not part of this harness.

The driver reads a JSON config with these fields:

```json
{
  "run_id": "0123456789abcdef",
  "cluster": "apexload-fault-0123456789abcdef",
  "context": "kind-apexload-fault-0123456789abcdef",
  "namespace": "apexload-fault",
  "kubeconfig": "/absolute/private/kubeconfig",
  "baseline_seconds": 10,
  "fault_seconds": 180,
  "recovery_seconds": 120,
  "expected_vusers": 120,
  "minimum_target_rps": 100,
  "capture_cluster_evidence": true,
  "cluster_evidence_interval_seconds": 10,
  "restoration_readiness_seconds": 120,
  "observation_command": ["/absolute/path/to/observation-adapter", "--private-config", "/absolute/private/adapter.json"]
}
```

Use the generated run ID, not this example. Invoke:

```sh
python3 scripts/fault_harness/node_fault.py \
  --config /absolute/private/config.json \
  --node apexload-fault-0123456789abcdef-worker \
  --output /absolute/new/private-evidence
```

The adapter prints one JSON object. It must obtain `target_requests` from an independent
private target's monotonic counter for this run; `observed_at_unix` is the target
observation time, not a timestamp added to old dashboard data. Forward the target's
32-character lowercase hexadecimal `target_instance_id` unchanged; it is required
and must remain constant across the entire trial, including observation outages.
Existing adapters must add this field; missing identities fail closed. Include `run_id`,
`observed_vusers`, `keda_total_demand`, `controller_restarts`, `status`, and `assignments`.
Each assignment has `group`, stable `shard`, `pod`, `node`, and authoritative execution
`generation`. Do not infer generation from pod names. A duplicated group/shard pair
fails the safety gate. The adapter must correlate actual target requests with the
assignments to detect old-owner traffic; this driver cannot invent that evidence.
`controller_restarts` counts unintended restarts on surviving controllers; expected
termination of a controller on the deliberately stopped node is a separate event.

The driver checks namespace ownership, explicit kubeconfig/context, node inventory,
and Docker kind ownership labels before killing a worker by immutable container ID.
It requires sustained baseline traffic, VUsers and KEDA demand, then requires sustained
traffic recovery while the node remains stopped. A stale sample, requested load alone,
or traffic returning only after node restoration does not pass. Use a workload long
enough to outlast baseline, failure and recovery windows. Fault and recovery windows
are bounded at 30 minutes. The full sustained recovery interval must finish strictly
before both the recovery deadline and scheduled node restoration. If it cannot,
the driver fails and immediately enters cleanup. `traffic_recovered.elapsed` and
`traffic_gate_passed.recovery_seconds` measure completion of that sustained interval;
`traffic_recovered.healthy_since_seconds` separately records its start. Older driver
evidence reported the interval's start as recovery time and must not be compared as
if it used the completion definition.

Baseline observation calls inherit the remaining acquisition budget. During the
fault trial, each observation is bounded by the next applicable recovery,
restoration or trial-end boundary, with a maximum 15-second command timeout. Poll
sleeps use the same boundary, and responses arriving at/after it cannot establish
recovery. Docker kill/start and post-restoration readiness have separate command
budgets; these observation limits do not guarantee a Docker restoration completion
time. Poll/command errors cannot silently pass the traffic gate. A transport outage resets
only the consecutive-load window; it does not erase the last target counter. Duplicate
ownership, a changed target identity (even with a higher counter), a counter reset
(including across an observation outage), invalid ownership/count evidence,
or a terminal run during the trial fails the trial permanently and triggers restoration.
A brief recovery followed by a failed run cannot pass. Load/restart counts must be finite,
nonnegative and correctly typed; booleans do not count as numbers or generations.

The `finally` path restores the stopped container, including when the kill command
itself times out. SIGTERM and Ctrl-C trigger that path. SIGKILL, host failure or Docker
failure can prevent cleanup; retain the immutable container ID in `events.json` for
manual restoration. `node_restored` proves the container is running, not that every pod
and volume is ready. The optional gate below checks Kubernetes readiness separately.
The driver does not change replicas, force-delete pods/PVCs, or destroy its cluster.

The output directory must be new; retries refuse to overwrite existing evidence.
Evidence is an allowlisted JSON event stream with private permissions. It captures
traffic observations, assignments, generation and timing; raw API/Secret responses,
command output and arbitrary adapter fields are excluded. The collector below adds
before/after Kubernetes inventories and optional background samples during the trial.
Measured dependency failover still requires the stack adapter. The archive command
below captures a separately requested final archive check. A passed traffic gate alone is not a passing completion, archive,
no-overlap, liveness, cleanup-readiness or production-availability qualification.


## Healthy control comparison

Use the same driver configuration and observation adapter to measure a control
window before running fault trials. Add `"control_seconds": 60` (allowed range:
10–1800 seconds), remove `restoration_readiness_seconds` if present, and use a new
evidence directory:

```sh
python3 scripts/fault_harness/healthy_control.py \
  --config /absolute/private/control-config.json \
  --output /absolute/new/private-control-evidence
```

The control command issues Kubernetes reads and runs the configured observation
adapter. It never invokes Docker, selects a victim, stops/restores nodes, or changes
replicas. The operator-provided adapter must also be read-only. All shared driver
configuration fields remain required; `fault_seconds` is unused in this mode.
`recovery_seconds` bounds acquisition of the sustained `baseline_seconds` load
baseline. Once acquired, every sample in the control window must meet the target
request-rate, observed-VUser and KEDA-demand thresholds, with zero controller
restarts. The adapter must count restarts across all controllers for a control run.
Transport failures, stale observations, duplicate assignments, changed target
identity, counter regression and terminal runs fail immediately. Load dips during
baseline acquisition reset its consecutive-load timer; dips during the measured
control window fail the trial.

Observations are requested about once per second, each with a maximum five-second
command timeout constrained by the remaining budget. A final sample is taken at or
after the requested window, with a five-second final-observation allowance. A
response at/after that deadline cannot pass. This is a sampled traffic gate; it does
not establish uninterrupted traffic between samples or successful-response TPS.

Mandatory `cluster-control-preflight.json` and `cluster-control-completed.json`
checkpoints verify disposable namespace ownership and retain node/namespace
identities. Replaced identities or failed checkpoint collection prevent a pass.
Each checkpoint has a separate 20-second command budget. Individual resource API
gaps remain explicit inventory diagnostics, not a cluster-readiness verdict.
Optional `cluster_evidence_interval_seconds` background sampling works as in fault
trials and requires `capture_cluster_evidence: true`; its worker must finalize
before `control_gate_passed` is recorded. Interruption stops that worker and records
a failed control, with no restoration needed. Output permissions and overwrite
protections match the fault driver.

This control path requires the configured observation adapter and deployed stack.
Its local safety tests do not qualify a live healthy run, archive completeness,
KEDA scale transitions, dependency failover or the production fault matrix.

## Controller liveness sampling

Forward each controller pod separately to a distinct loopback port, then sample
the existing `/api/v1/system/health` endpoint with the read-only helper:

```sh
python3 scripts/fault_harness/liveness_probe.py \
  --ports 18101 18102 --seconds 600 \
  --output /absolute/new/private-liveness-evidence
```

This command requires `curl`, disables its startup configuration and proxies,
does not follow redirects, and bounds each full HTTP transfer to two seconds
(with a 2.5-second subprocess backstop). One to eight unique loopback ports are
allowed. Controllers are sampled concurrently about once per second; delayed
requests do not trigger bursts of catch-up probes. The final sample must start
at or after the requested duration and finish before the five-second completion
allowance. The gate rejects sampling gaps over three seconds, any failed request,
incomplete coverage, and per-controller nearest-rank p99 latency at or above
500 ms. Supported durations are 10–1800 seconds; use 600 for the plan's local gate.

The new output directory is private (0700), files are 0600, and existing evidence
is never overwritten. `report.json` and `samples.json` retain only ports, timing,
counts, and outcomes. Interruptions and transport failures cannot pass. Raw
response bodies and command errors are not persisted.

A sampler pass establishes only sampled HTTP liveness. Separately record pod UIDs,
node placement, restart counts, actual dependency unavailability, and restoration.
Do not forward a load-balanced service: it could hide a failed replica. This helper
does not create port-forwards, stop dependencies, restore replicas, or establish
continuous availability between samples. Failed port-forwards count as failures.


## Independent traffic target

Build the target and start it with the fresh private inputs:

```sh
go build -o /absolute/private/apexfault-target ./cmd/apexfault-target
/absolute/private/apexfault-target --config /absolute/private/target.json \
  --tls-cert /absolute/private/fault-target.crt \
  --tls-key /absolute/private/fault-target.key
```

The default listener is `127.0.0.1:8090`. Set `--listen :8090` explicitly for a
private disposable-cluster deployment. Place this single target outside the failed
worker, trust the generated CA, and use its certificate name `apexload-fault-target`
(or localhost for a local test). Without TLS flags it serves HTTP for isolated local
use. Both TLS flags must be supplied together. This command does not deploy a pod.

Authenticated `GET /load` requests with `Authorization: Bearer <load_token>` and
`X-Apexload-Fault-Run: <run_id>` increment the process's counter and return 204.
Configure these headers through private workload inputs. `GET /snapshot` requires
the distinct observer token and returns only schema version, run ID, random
`target_instance_id`, process start time, fresh `observed_at_unix`, and
`target_requests`. Keep tokens out of URLs and published scripts/artifacts.
`GET /healthz` returns 204. Health, snapshot and rejected requests never increase
the counter; snapshot responses disable caching. There is no counter-reset endpoint.

The counter measures accepted load requests at the target, including requests whose
response is subsequently lost. It is not successful-response TPS, a VUser count,
a shard ownership record or a target performance benchmark. Counters are in memory;
a restarted process gets a new identity and begins at zero. Use one target process,
not load-balanced replicas. The observation adapter must preserve the target's
observation time and identity, then combine it with independently verified controller,
KEDA and assignment evidence. This service alone cannot detect old-owner traffic or
qualify failover. Graceful termination waits up to five seconds for active requests.


### Read a validated target snapshot

```sh
/absolute/private/apexfault-target observe \
  --config /absolute/private/target.json \
  --url https://127.0.0.1:8090 \
  --ca /absolute/private/ca.crt --timeout 3s
```

This prints one validated snapshot JSON object and exits nonzero with a fixed error
message on failure. It sends only the observer token and preserves the target's
original observation timestamp. URLs must use HTTPS, except numeric loopback HTTP
for local tests. The path may be empty, `/`, or `/snapshot`; URL credentials, query
parameters and fragments are rejected. Redirects and environment proxies are not
used. Omit `--ca` to use system trust roots; there is no insecure TLS mode.

The request deadline covers connection, TLS and body reading (default three seconds,
maximum ten). Headers are bounded to 8 KiB and the JSON body to 4 KiB. The reader
requires the exact v1 fields, rejects duplicate/missing/null fields and unknown
schemas, pins the configured run ID, checks process identity syntax and counter
types, and requires the original observation time to be at most five seconds old and never
in the future. Target and observer clocks must agree. CA input is bounded to
1 MiB. Errors omit remote bodies, URLs, file contents and credentials.

This is a single target read, not a complete `observation_command`: the adapter
still needs authoritative VUser, KEDA, controller-restart and shard/generation
observations. Preserve `target_instance_id` and `observed_at_unix` when combining
those sources. The reader does not compare separate invocations; the fault driver
performs continuity checks. An accepted-request snapshot is not a success or
no-duplicate-execution verdict.


### Container and private Kubernetes target resources

The target has a separate [minimal Dockerfile](../../cmd/apexfault-target/Dockerfile)
using the repository's pinned Go builder and a non-root scratch runtime. It includes
no application/dashboard dependencies or bundled CA roots; use `--ca` when running
the observer inside this image. Build from the repository root:

```sh
docker build -f cmd/apexfault-target/Dockerfile \
  -t localhost:5001/apexload-fault-target:trial .
```

Push to the registry reachable from the disposable cluster, then set
`TARGET_IMAGE_DIGEST` to its repository reference including `@sha256:` and the
registry manifest digest. A mutable tag or local image ID is not a substitute.
Generate a new private resource directory from fresh inputs:

```sh
python3 scripts/fault_harness/render_target.py \
  --inputs /absolute/private/inputs \
  --image "$TARGET_IMAGE_DIGEST" \
  --output /absolute/new/private-target
```

The renderer validates run ownership, distinct credentials, and a strict TLS
handshake using the exact captured PEM bytes and the Service's DNS name. It writes
`target-resources.json` and `kustomization.yaml` with private permissions and refuses
existing output directories. The resource list contains:

- An immutable server Secret containing target config and its TLS certificate/key.
- A separate immutable load Secret containing only the load token and CA, with no
  observer token or private key. Configure workload headers from these private inputs.
- One target replica with `Recreate` rollout strategy, pinned to the run's kind
  control-plane node and tolerating that node's scheduling taint. Worker fault
  injection must leave this node running. Restart/redeployment changes the target
  identity and invalidates the running trial.
- A non-root, read-only container with no service-account token, bounded resources,
  TLS health probes and a ten-second termination grace period. Health probes check
  process availability; the verified observer checks certificate trust separately.
- A ClusterIP HTTPS Service and ingress restricted to same-namespace generator or
  `apexload-fault-observer` pods, plus denied target egress. NetworkPolicy enforcement
  depends on the cluster's CNI and remains a live-validation requirement.

The renderer does not create/relabel a namespace, contact Kubernetes, apply resources,
or provision the rest of the application. Before applying, verify the live namespace's
`apexload.io/fault-run` label matches `ownership.json`, using the run's explicit
kubeconfig and kind context. Keep rendered resources private: Kustomize output includes
Secrets. Do not print or attach that output as public evidence. The fixed one-CPU/
128-MiB limit is a bounded initial target profile, not measured throughput capacity.

The target image build and pod checks passed after an authorized Docker Desktop
restart. Three rounds of 64 requests using eight concurrent clients produced exact
accepted-counter deltas over verified TLS; invalid tokens/run IDs were rejected.
These component checks do not qualify application recovery or target throughput.
Existing Compose volumes were preserved, and its PostgreSQL returned to healthy.


## Private PostgreSQL and Redis dependencies

Fresh input directories now include separate PostgreSQL and Redis certificates.
Render single-instance dependencies for isolating application worker faults:

```sh
python3 scripts/fault_harness/render_dependencies.py \
  --inputs /absolute/private/inputs \
  --postgres-image "$POSTGRES_IMAGE_DIGEST" \
  --redis-image "$REDIS_IMAGE_DIGEST" \
  --storage-class standard \
  --output /absolute/new/private-dependencies
```

Supply repository manifest digests for the official PostgreSQL 16 Alpine and Redis
7 Alpine images, or compatible mirrors. The renderer validates digest syntax, not
image contents. Numeric identities follow those images: PostgreSQL UID/GID 70 and
Redis UID 999/GID 1000. The images used for live checks are recorded in the review
ledger. Resources remain namespace-scoped and private, with no automatic apply or
namespace creation. Verify the live namespace ownership before applying as above.

Each service has one StatefulSet replica pinned to the owned control-plane node,
a 1-GiB RWO claim using the selected storage class, TLS credentials in an immutable
Secret, a headless Service, and a scoped NetworkPolicy permitting only controller
and observer pods. Containers are non-root with read-only root filesystems and no
service-account token. PostgreSQL requires SCRAM over TLS for TCP access; Redis has
no plaintext listener, requires authentication, and persists with AOF. Readiness
checks authenticate over TLS; TCP liveness/startup probes only establish a listener.
Redis uses `noeviction` to avoid silently evicting coordination state at its limit.

The separate `apexload-fault-data-client` Secret contains only database/cache URLs
and the CA, with no server private keys or unrelated application credentials.
Controllers must mount its CA at `/run/apexfault-data/ca.crt` and trust that CA for
Redis using `SSL_CERT_FILE=/run/apexfault-data/ca.crt`. The database URL uses
`sslmode=verify-full` with that CA path. Mount this private trust file explicitly;
rendering the Secret does not configure an existing controller. New certificates or
passwords require a fresh disposable installation, not an overwrite of immutable
Secrets or reuse of initialized database volumes.

The local deployment passed authenticated TLS checks, rejection of plaintext and
invalid PostgreSQL credentials/unauthenticated Redis requests, and data retention
across clean pod replacement. This is single-instance local-path storage. It does
not establish dependency HA, abrupt-loss RPO, cross-node volume recovery, or a full
stack provisioner. NetworkPolicy enforcement requires a compatible CNI and a
separate positive/negative connectivity test.

The image identity and TLS key-permission assumptions follow the
[PostgreSQL image source](https://github.com/docker-library/postgres/blob/master/16/alpine3.23/Dockerfile),
[Redis image source](https://github.com/redis/docker-library-redis/blob/master/7.4/alpine/Dockerfile),
and [PostgreSQL TLS documentation](https://www.postgresql.org/docs/16/ssl-tcp.html).


## Read-only cluster inventory

Collect an inventory independently, using the same driver config and a new private
output directory:

```sh
python3 scripts/fault_harness/cluster_evidence.py \
  --config /absolute/private/config.json \
  --output /absolute/new/private-cluster-evidence
```

This command performs only explicit-context, namespace-scoped reads (plus namespace
ownership and node inventory reads). It verifies the fault-run namespace label and
UID before and after collection, and rejects nodes outside the disposable cluster.
It does not run the observation command or inject a fault. `cluster.json` records:

- Node identities, zones and readiness/pressure conditions.
- Pod placement, phase/readiness, container restart counts and runtime SHA-256 image
  digests. A missing digest remains null; an image tag is not treated as a digest.
- Deployment/StatefulSet/ReplicaSet replica state and HPA/KEDA targets, bounds and conditions.
- Pod/workload controller-owner identities and deletion state, for checking replacement
  pods against the workload that owns them; arbitrary owner metadata is omitted.
- PVC binding phase, storage class, volume identity and access modes.
- Kubernetes event object references, type, reason, count and timestamps.

The output omits environment values, pod templates, annotations, event messages,
KEDA trigger metadata, authentication references and raw command errors. It never
requests Secrets or ConfigMaps. Object identifiers still describe private topology;
keep the directory private. Files are created with mode 0600 in a new 0700 directory
and refuse overwrite, including symlink destinations. Inventories are bounded to
2,000 objects per resource and artifacts to 8 MiB; oversized/paginated resource lists
are explicit gaps, not silently truncated successes.

`inventory_complete` means every listed resource inventory was observed. It does not
mean the cluster is healthy, every container has a digest, or recovery is qualified.
Unavailable APIs (including a missing KEDA CRD) and invalid responses produce fixed
per-resource gap statuses; the standalone command saves that partial inventory and
exits nonzero. Ownership failures abort collection. Reads span the recorded start/end
times and are not an atomic cluster snapshot. This diagnostic inventory is separate
from the independent target counter and authoritative assignment/generation evidence
required by the traffic observer.


## Optional driver checkpoints

Set `capture_cluster_evidence` to `true` in the driver config (default: `false`) to
save `cluster-preflight.json` before the sustained load baseline and
`cluster-restored.json` after the driver's final restoration attempt succeeds.
`events.json` links each file by name and SHA-256, with `inventory_complete` and
resource gap names. These checkpoint reads never run inside the stopped-node recovery
window or before the restoration command in cleanup. The separate background sampling
option below can collect inventories during the trial.

A preflight ownership/snapshot failure or artifact-write conflict prevents injection.
Individual unavailable resource APIs remain recorded gaps and do not independently
fail the traffic gate. A diagnostic read outage after restoration records
`cluster_evidence_unavailable`; it does not hide the original safety/traffic failure.
If the trial otherwise succeeds but the diagnostic result cannot be recorded, the
driver fails. Without the readiness option below, the restored checkpoint observes the cluster
immediately after Docker container restoration and does not wait for pod/PVC
readiness. These checkpoints do not provide a during-fault scaling trace or qualify
application recovery.


## Optional Kubernetes restoration gate

Set `restoration_readiness_seconds` to a finite value from 1 through 1800, together
with `capture_cluster_evidence: true`, to require a ready preflight inventory and
poll Kubernetes after Docker restoration and the traffic trial have finished. Omit
this option to retain the immediate diagnostic checkpoint behavior. An unready,
empty or unsupported baseline prevents fault injection.

The gate checks all cluster nodes and the disposable namespace's Deployment,
StatefulSet, ReplicaSet, pod and PVC inventories. These inventories must be observed;
missing RBAC/API access fails the gate. Nodes must retain their baseline identities,
report Ready, have no reported pressure/network condition other than False, and not
be terminating. Baseline workloads and PVCs must retain their identities; PVCs must
stay bound to the same volume/storage class/access modes. Every current workload
must observe its generation and converge to its current desired replica count,
including pod inventory through verified controller-owner references. Autoscaling
may change the desired count, but scaling a previously active workload to zero
cannot satisfy recovery. All active pods must report Ready/ContainersReady with
running ready application containers on inventoried nodes. Terminating pods block.

Managed pods may be replaced normally. Bare active pods, such as an independent
target, must retain their identity and remain running. Completed job pods are ignored.
Active Job, DaemonSet or custom-controller pods are currently unsupported and prevent
baseline qualification; this gate targets Deployment/StatefulSet stacks. HPA/KEDA and
event read gaps remain diagnostics, not Kubernetes readiness predicates, and are
still marked incomplete in the saved inventory. Autoscaler behavior and actual traffic
are separate gates. Binding identity does not verify volume contents or attachment.

Polling pauses up to five seconds between attempts. Every kubectl subprocess is
limited by the same remaining monotonic deadline, and a response at/after that
deadline cannot pass. This budget covers the post-trial readiness observation loop;
Docker restoration, preflight and artifact writes are separate operations. Readiness polling does not run while the victim is stopped. Ctrl-C/SIGTERM during the trial restores the
container and skips this optional wait; interruption during the wait exits after
restoration has already occurred. Other trial failures still attempt the gate, while
preserving the original failure if readiness or evidence recording also fails.

`events.json` records fixed reason counts for each readiness attempt. Only the last
snapshot is saved as `cluster-restored.json` (absent when collection fails entirely),
so polling does not accumulate full inventory files. `cluster-readiness.json` records
the gate result, budget, elapsed observation time, attempt count, deadline status and
last readiness assessment. Both artifacts are private, refuse overwrite and are
linked by SHA-256 from the event stream. A traffic pass followed by a readiness timeout
fails the driver, with distinct `cluster_readiness_failed` evidence. An artifact/index
write failure also fails an otherwise successful trial.

A readiness pass describes Kubernetes observations over the recorded snapshot span;
it is not an atomic snapshot, a sustained availability result, an application health
probe, or a proof of data integrity, archive completion, correct placement across
failure domains or exclusive execution ownership. It does not qualify production
node-loss recovery or replace the remaining real deployment/fault-matrix tests.


## Optional background inventories during the fault trial

Set `cluster_evidence_interval_seconds` from 5 through 300, together with
`capture_cluster_evidence: true`, to start one background sampler after the Docker
kill command succeeds. Omit the interval to disable it. The namespace UID is pinned
to the preflight inventory. Each sample uses the same allowlisted node, pod, workload,
HPA/KEDA, PVC and event projection as the checkpoint collector. The sampler issues
only reads; the traffic driver continues its observation/restoration loop without
waiting for a sample. API reads still consume cluster resources, so sampling is
optional and its effects should be considered when interpreting a performance run.

A sample gets a shared ten-second read budget. The interval is the pause after an
attempt completes; slow reads never queue concurrent samples or trigger catch-up
bursts. Missing resource APIs produce partial inventory artifacts, and whole-snapshot
transport/deadline failures produce explicit index gaps. Namespace replacement or
invalid cluster identity/inventory stops sampling and cannot be retried into success.
A snapshot spans its recorded UTC start/end times and is not an atomic cluster view.
Compare those times with `fault_injected` and `node_restored`; an overlapping sample
must not be described as wholly observed while the node was stopped. If restoration
occurs before the trial ends, sampling can include the remaining post-restoration
part of the trial.

Cleanup requests cancellation without waiting, attempts Docker restoration, and only
then joins the worker for at most eleven seconds. Cancellation is checked between
reads, and each command receives only the remaining snapshot budget. Even if Docker
restoration fails, the sampler is stopped/finalized and that restoration error stays
the primary failure. Interruption, ambiguous kill timeouts and trial safety failures
also enter cleanup. A worker that cannot stop is explicitly reported; later files
from that daemon worker, if any, are unindexed and must not count as evidence.

The new private `cluster-series/` directory contains exclusive `sample-NNNN.json`
files. `cluster-series.json` indexes attempt start/end offsets from the sampler's
UTC start time, fixed statuses, filenames, SHA-256 values, byte counts and resource
gaps. The driver event stream links the index by SHA-256. Raw errors, command output,
credentials and observation-adapter configuration are excluded. The series is capped
at 512 attempts and 64 MiB of snapshot files, with the existing 8 MiB per-file limit;
the final index is separate and also subject to the per-file limit. A partial write
can leave an unindexed file; only successfully indexed bytes are reported.

Unavailable API observations and cancellation remain explicit diagnostic gaps and do
not invalidate a traffic pass by themselves. Scope failures, storage/attempt limits,
worker failures, failure to stop, or failure to save/index the result fail an otherwise
successful driver run after restoration. These failures do not suppress the optional
Kubernetes readiness gate or replace an original trial failure. Zero observed samples
is visible in the event/index; it provides no scaling evidence even if traffic passed.

This feature provides sampled Kubernetes state, not a passing KEDA scaling test,
continuous coverage, authoritative shard ownership or dependency failover evidence.
The real stack/traffic adapter, final archive checks and repeated deployed fault
matrix remain required before qualification.


## Final archive evidence through apexctl

After a trial, use `apexctl archive evidence` against the deployed controller to save
an incarnation-bound archive check. It does not wait for run completion or inject a
fault. Supply the test ID, project and creation timestamp captured at launch, rather
than discovering a potentially reused ID after the failure. For example:

```sh
./bin/apexctl archive evidence \
  --api https://localhost:8443 \
  --test RUN_ID --project PROJECT_ID \
  --created-at '2026-09-27T12:00:00.123456789Z' \
  --metadata-backend postgres --status COMPLETE --timeout 2m \
  --output /absolute/new/private-archive-evidence
```

Use the controller's actual launch identity, not these example values. Configure
`APEXLOAD_API_KEY` or `APEXLOAD_BEARER_TOKEN` for authentication. TLS verification
stays enabled; for a disposable private CA, provide the CA through the Go runtime's
trusted-root configuration (for example `SSL_CERT_FILE` on supported Unix systems).
The command uses the existing CLI controller client and confines redirects to the
configured origin. All reads/retries share `--timeout` (positive, at most 30 minutes),
and each decoded API response is capped at 4 MiB.

The command reads the terminal run, v4 archive manifest and controller integrity
report, then rereads the run/catalog. It requires the expected project, creation time,
terminal status/end time and unchanged catalog manifest hash. PostgreSQL comparisons
truncate exactly to microseconds; the default `sqlite` mode compares exact timestamps.
The integrity report must name the expected test and include a matching `manifest.json`
trust-anchor entry, consistent counts and a matching SHA-256 for every artifact.
HTTP 200 or a bare `verified: true` field cannot pass by themselves. The verifier
timestamp must fall within this request window, with a five-second allowance for
controller/client clock skew; older or future-dated reports fail as stale evidence. This is the
controller's verification of its stored archive, not independent client-side hashing
of downloaded objects. The sanitized manifest can legitimately differ in bytes from
the stored manifest, so its response hash is not used as the catalog trust anchor.

A pass requires a nonempty `required` source snapshot with every source collected.
Missing/unknown coverage, `best_effort` collection, unsupported manifest schemas,
unverifiable hashes and absent archives exit nonzero. Counts and coverage labels
remain visible even when a check fails. Known durable remote-collection failure
reasons are retained, but raw server errors, scripts, configuration, artifact/source
names and credentials are excluded. An explicitly expected FAILED or ABORTED run
can have valid archive evidence; that pass does not claim the execution succeeded.
Even complete declared source coverage does not establish zero measurement loss or
complete historical shard membership.

The new output directory is mode 0700 and `archive-evidence.json` is mode 0600;
existing directories and files are never overwritten. The same safe report is emitted
as JSON stdout. A failed check still saves its stage, fixed reason and HTTP status
when available. Repeated before/after-failover checks must use separate directories.
These reads are not an atomic database snapshot, and a stable catalog does not prove
that no later mutation occurred. Running the command alone does not establish a
leadership transfer, S3 restart, data-service failover or production availability.


## Private S3 archive component

Render a single-instance SeaweedFS service for the same owned installation:

```sh
python3 scripts/fault_harness/render_s3.py \
  --inputs /absolute/private/inputs \
  --image docker.io/chrislusf/seaweedfs@sha256:186de7ef977a20343ee9a5544073f081976a29e2d29ecf8379891e7bf177fbe9 \
  --output /absolute/new/private-s3-resources
```

The tested image reports SeaweedFS 4.17, source commit `4a5243886`. The renderer
requires a repository digest, validates the captured certificate/key/CA against
both the service and bucket DNS names, and writes private, exclusive Kustomize
resources. It does not apply resources, create the namespace, create the bucket,
or configure application controllers. Apply only to the explicitly owned
`apexload-fault` namespace in the matching disposable cluster.

The non-root, read-only-root container runs on the control-plane node and retains
object volumes, master state and filer metadata on one RWO PVC. The default 2 Gi
claim has six 256 MiB volume slots. New fixtures can use `--storage-gib` (2–16)
and `--volume-count` (at most three slots per GiB); for example, `--storage-gib 4
--volume-count 12` allocates a 4 Gi claim with 3 Gi of object slots. One-at-a-time
volume growth and the slot ceiling reserve at least one quarter of the claim for
internal metadata. Existing local-path claims require an offline, checksum-verified
migration to a new claim; applying a larger request is not an in-place migration.
Keep the original claim until the copied service's object reads and writes have
been verified. This remains a single-pod local fixture without replication or a
production durability/primary-failover claim. Monitor both free disk space and
writable volume slots during repeated trials. `--memory-mib` sets an explicit
512–2048 MiB container limit (default 512 MiB); the request is one quarter of that
limit. A retained dataset can cause sustained memory reclaim before an OOM kill,
so inspect cgroup memory events and signed request latency when selecting the
allowance. The 4 GiB migration trial uses `--memory-mib 1024`.

The headless service and stable pod hostname provide the application's virtual-hosted
bucket address, `apexload-archives.apexload-s3.apexload-fault.svc`. The fresh certificate
covers that address. Port 8333 serves TLS only; internal filer/master/volume endpoints
bind loopback. The extra S3 gRPC listener requires a client certificate from the
private CA. Both TLS minimum and maximum are explicit because the pinned server's
TLS option validation rejects a minimum above its default maximum. The policy
exposes only 8333 to controller/observer pods; policy enforcement requires a capable
CNI. TCP pod probes indicate listener health, not signed object availability.

The server and client Secrets are separate. The client Secret contains only archive
configuration, CA and bucket-scoped AWS credentials. Mount its CA and configure
controller root trust; never disable verification. All controllers must share the
same bucket/prefix. The server's IAM identity permits only `apexload-archives`.
The source behaviors used here are in the pinned
[S3 server](https://github.com/seaweedfs/seaweedfs/blob/4a5243886/weed/command/s3.go)
and [TLS configuration](https://github.com/seaweedfs/seaweedfs/blob/4a5243886/weed/security/tls.go).

After establishing an explicit loopback port-forward to service port 8333, seed and
verify a run-bound probe with the repository command:

```sh
go run ./scripts/fault_s3_probe --inputs /absolute/private/inputs \
  --address 127.0.0.1:18333 --mode seed --output /absolute/new/private-s3-seed
# After replacing the owned server pod while retaining its PVC:
go run ./scripts/fault_s3_probe --inputs /absolute/private/inputs \
  --address 127.0.0.1:18333 --mode check --output /absolute/new/private-s3-check
```

`seed` creates the fixed bucket if needed, writes the 64 KiB object at
`fault/<run-id>/retention-probe-v2`, and reads the exact expected bytes. `check` never rewrites that object. Both modes
also attempt a foreign-bucket creation and an invalid-credential read, requiring
explicit access/signature rejection rather than accepting transport failure. An
incorrectly permissive server can therefore create the fixed negative-test bucket;
use only the disposable service. The command never deletes buckets, objects or PVCs.
It uses only the supplied credentials/CA, verifies service/bucket DNS identities,
dials only the supplied IPv4 loopback port and refuses redirects and proxies.
It does not test Kubernetes DNS routing through the port-forward.

One minute bounds all requests, each capped at 20 seconds, with no SDK retries.
Wait for startup before invoking it; a listener can start before cold volume
allocation finishes. Failures save a fixed stage without raw server errors. Evidence
is reserved before writes, stored in a new 0700 directory/0600 JSON file and never
overwritten. Restart/replacement actions and pod/PVC identities are separate
evidence; a passing read alone does not prove that a replacement happened. The
component trial passed signed put/get and retained reads after container replacement,
plus separate TLS/plaintext/untrusted-CA and gRPC client-certificate checks. It does
not qualify application archive publication, Kubernetes PVC retention or HA failover.


## Phased local HA stack

`render_stack.py` combines the target, PostgreSQL/Redis and S3 resources above with
an application overlay of the repository's actual `onprem-ha` manifests. Supply
`--inputs` and a new absolute `--output` plus six explicit repository digests:
`--controller-image`, `--generator-image`, `--target-image`, `--postgres-image`,
`--redis-image`, and `--s3-image`. It validates everything before creating output.
Use inputs prepared with this version (including `auth_session` and S3 TLS).
Application images must be built from recorded committed source, not a dirty
checkout. Keep the repository checkout available: the generated application
Kustomization references its production base using a relative directory path.

The renderer makes no cluster changes. Deploy only after confirming the explicit
kubeconfig/context, three-node kind topology and namespace ownership match
`inputs/ownership.json`. There are two Kustomize directories and a SQL bootstrap:

1. Apply `dependencies/` to the owned cluster and wait for PostgreSQL, Redis, S3
   and the target. This phase owns the namespace and its restricted Pod Security
   labels. Preload the recorded images on the nodes that will run them.
2. Run `bootstrap-auth.sql` against the disposable PostgreSQL `apexload` database
   with `psql -v ON_ERROR_STOP=1`. It transactionally initializes trusted-header
   settings if absent, accepts an exact retry, and rejects incompatible existing
   authentication without overwriting it. Do not use it against another database.
3. Seed the S3 bucket/probe using the command above, through an explicit port-forward.
4. Apply `application/`, then wait for both controllers and all generators to be
   Ready and verify actual cross-node connectivity and discovery before a baseline.

Controllers use PostgreSQL leases, Redis, shared S3, mTLS/token generator transport,
and production trusted-header authentication. The proxy secret comes from the private
`http` credential. Requests require `X-APEXLoad-Proxy-Secret` and the subject
`fault-<run-id>` in `X-Auth-Request-User`. The production first-admin bootstrap is
explicitly enabled and pinned to that subject; its first authenticated request
persists and audits the administrator grant. The upstream role header does not
bypass stored authorization. There is no external ingress; use loopback forwards.
Health/readiness probes remain separate from authenticated application checks.

Two controllers have required anti-affinity across the exact two worker nodes.
Generators default to four replicas and 100 HTTP VUsers each; `--replicas` accepts
2–12 and `--pod-capacity` 1–1000. Required worker placement and topology spread
prevent a control-plane placement from accidentally satisfying a node-fault test.
The profile preserves generator fencing/archive PVCs (1 Gi each, `standard` local
storage), limits local scratch, and trusts the fresh CA for target HTTP. These small
limits are test settings, not capacity claims or reserved recovery headroom.

Controller import assets are ephemeral in this HTTP-only profile. It does not qualify
import-dependent workloads, Java/browser runners, shared import storage, physical
node/zone loss or data-service HA. Prometheus and KEDA installation/configuration
are separate prerequisites; the initial replica count is static until those are
installed. A rendered profile is not a full-stack or fault-matrix pass.

The tested kindnet image was
`docker.io/kindest/kindnetd:v20251212-v0.29.0-alpha-105-g20ccfc88`, whose embedded policy
engine was active. Its policy cache retained old pod addresses during the first
rollout with its default 100m CPU/50 Mi limits. Raising limits to one CPU/128 Mi and
rolling that DaemonSet restored connectivity. `kindnet-resources-patch.json` records
that optional strategic patch for the `kindnet-cni` container. Inspect the installed
DaemonSet/image first, save its original configuration, and apply the patch only to
`kube-system/daemonset/kindnet` in the explicitly owned cluster. It changes no policy
rules. Do not assume every kind installation enforces policies or needs this tuning;
verify allowed and denied flows for the actual CNI. Any CNI tuning/restart is setup
work and requires a fresh application baseline afterward.

## Local Prometheus and KEDA inputs

`render_monitoring.py --inputs ABS_INPUTS --prometheus-image REPOSITORY@sha256:DIGEST
--output NEW_ABS_DIRECTORY` renders a bounded, ephemeral Prometheus on the control
plane and a KEDA overlay of the shipped on-prem ScaledObject. Apply `prometheus/`
and `autoscaling/` only to the explicitly owned context and namespace after installing
KEDA. The renderer does not install KEDA. Record its chart, image digests, version
and Kubernetes compatibility separately. Apply `controller-autoscaling-merge-patch.json`
as a **merge patch** to `apexload-config`, then roll controllers during setup; do not
apply that file as a partial ConfigMap. It selects KEDA as the sole fleet autoscaler.

Prometheus discovers both controller metrics endpoints through a headless service.
The six production queries retain installation scoping, leader-evidence expiry,
missing-metric errors and the reserved ordinal floor. Defaults are minimum two,
maximum four and a hard capacity of 100 VUsers per pod; override with `--minimum`, `--maximum` and
`--pod-capacity` within the documented local bounds. `--accelerated` uses a zero
scale-up window and 30-second scale-down window with bounded pod increments. With
a positive minimum, HPA behavior governs replica timing; KEDA's cooldown is not
an idle scale-down timer for this profile. Default output retains production timing. The local HTTP scaling
threshold is 90% of hard pod capacity (90 for a 100-VUser pod), leaving room for
Kubernetes' default 10% HPA tolerance. Otherwise demand 320 can remain at three
100-VUser pods. This margin may provision an extra idle pod; it does not change
reported demand or relax dispatch capacity checks. A cluster configured with a
larger tolerance needs separate qualification. `profile.json` records the actual
threshold and assumed tolerance. Browser triggers retain their production defaults;
this disposable application profile is qualified only for HTTP.

Require two healthy scrape targets and an idle, restart-free baseline before each
trial. Record queued demand, desired/Ready replicas, discovered generators, explicit
dispatch, independent target traffic and final scale-down separately. Legacy auto
placement keeps each group on one generator: a larger fleet alone does not make one
oversized group shardable. Use explicit group host selection, separate fitting groups,
or configure and validate opt-in peak pod reservations for automatic partitioning.
Existing retained local-path PVCs may pin low ordinals to the same worker; inspect
actual placement after scale-down. Never infer worker redundancy from replica count.

`scaling_readiness.generator_fleet_ready(pods, generators, replicas, pod_capacity,
namespace)` is the reusable pre-dispatch gate for the local HTTP profile. Pass the
fresh PodList `items` and inventory `generators` arrays after verifying namespace
ownership. It requires unique pod identities, Ready containers, matching current
IPv4/IPv6 RPC targets, HTTP compatibility, active scheduling state, zero active
VUsers and sufficient advertised capacity. Pending pods without IPs, stale or
extra inventory rows, drains, malformed evidence and duplicate identities all
return false. Poll within a fixed deadline; do not weaken the gate to make a trial
start. Callers must additionally check HPA/StatefulSet state, scrape freshness,
controller restart baselines and the independent sustained-traffic gate. This helper
does not reserve capacity or prove load delivery.

### Load local application images by verified digest

`load_image.py --inputs ABS_INPUTS --tag FULLY_QUALIFIED_TAG
--reference REPOSITORY@sha256:DIGEST --output NEW_ABSOLUTE_DIRECTORY` loads an
already-built, committed-source Linux image on the two workers owned by those inputs.
It verifies the local manifest digest, both immutable worker container identities,
kind ownership and matching architectures before streaming any image. The tag and
pinned reference must use the same fully qualified repository.

The loader verifies containerd's imported manifest and explicitly creates and checks
the `repository@digest` alias used by digest-pinned Deployments. A successful named-tag
import alone is insufficient: the observed kind/containerd runtime omitted that alias
and attempted an unauthorized remote pull until it was restored. Existing conflicting
aliases or a changed source manifest fail the loader. Both streaming processes are
reaped on failure/interruption. Logs and the final verification record are private;
a failed partial import does not create a successful result or deploy anything.

This helper does not build images, change Deployments, initialize storage, remove
images/volumes or run fault trials. Inspect host disk capacity before loading images:
Docker's sparse virtual-disk free-space figure does not establish host free space.

The local controller profile allows 1 GiB of `/tmp` transfer workspace and 3 GiB
of data workspace, within a 5-GiB ephemeral-storage limit (512-MiB request). It limits
an archive materialization and idle materialization cache to 256 MiB each, with a
15-minute TTL. These values cover the observed roughly 190-MiB 320-VUser/45-second
HTTP archive; larger trials must be sized explicitly. Active cache readers and
publication staging consume additional space. The earlier 128-MiB `/tmp` profile
caused a real controller eviction, invalidating the third repeated scaling trial.
The later 2-GiB data-volume limit also caused eviction during a cold archive-cache
startup burst. Use controllers with immediate byte retention after a downloaded
archive reader releases its lease; the periodic TTL sweep alone can allow a burst
to exceed the idle cache budget. This is not admission control for concurrent
active readers, and confirmed-source retention remains a separate opt-in policy.
For a fresh deployment whose controller images all implement publication-source
fencing, `render_stack.py --source-retention-seconds 300` enables a five-minute
TTL for confirmed local sources. `deploy_stack.py` accepts the same option.
The default is disabled (`0`); enabled values
must be from 60 through 2,592,000 seconds. The profile receipt records the value.
Never enable this on shared staging while an older unfenced writer is still
running. See [source retention](../../docs/ARCHIVE_SOURCE_RETENTION.md) for the
confirmation checks and exclusions; unpublished sources are retained.

### Read the deployed local profile for healthy and node-fault trials

`observe_run.py --config ABS_PRIVATE_CONFIG` is a read-only observation command for
`healthy_control.py` and `node_fault.py`. Its JSON config contains:

```json
{
  "inputs": "/absolute/private/inputs",
  "namespace_uid": "actual-namespace-uid",
  "test_id": "actual-test-id",
  "project_id": "actual-project-id",
  "created_at": "2026-09-28T00:00:00.123456Z",
  "target_instance_id": "0123456789abcdef0123456789abcdef",
  "surviving_controllers": [
    {"uid": "actual-pod-uid", "node": "apexload-fault-RUNID-worker2", "restarts": 0}
  ]
}
```

Capture these values from the fresh baseline, not from an earlier installation.
Use both controller baselines for healthy controls; for a worker fault, include the
controllers expected to survive on other workers. `restarts` is the observed sum of
container restart counts for that pod. Replacement of a designated survivor is also
reported as a restart failure, so a new UID cannot hide a reset counter.

The adapter verifies the owned namespace UID, reads the active EndpointSlice and pod
inventory, opens bounded loopback pod forwards, and uses the private profile's existing
trusted-header and observer credentials. It verifies the target's TLS certificate and
never follows redirects or inherits proxies. The target pod listens on 8090; the Service
port 443 is not a pod-forward port. Forward processes are killed/reaped on exit.
The POSIX CLI has a four-second overall observation budget plus bounded cleanup, suitable
for the healthy driver's five-second read allowance. Transport failures emit no snapshot.

Fresh live metrics must match the currently armed execution generation. Shard targets
must map unambiguously to owned worker pod addresses. The adapter binds the leader by
name/address and observed pod UID, then rereads that pod and the endpoint before returning;
the shipped EndpointSlice publisher does not populate `targetRef.uid`. Run creation time
uses PostgreSQL's microsecond precision in this local profile. The independent counter,
current fleet demand, controller restart deltas and declared assignments are the only
output fields. Assignments are controller declarations, not independent proof of generator
fencing or exclusive traffic ownership. This adapter does not reserve recovery capacity.

Namespace/run/target incarnation changes and invalid safety evidence emit the fixed
`{"safety_violation":true}` marker. The drivers latch that marker as failure rather than
retrying it away as an ordinary outage; diagnostic text or credentials are never emitted.
Terminal runs are returned with their terminal status so the driver rejects them immediately.


## Deploy the prepared disposable stack

`deploy_stack.py` runs the phased deployment after the owned three-node kind cluster
and KEDA are installed. Keep the cluster kubeconfig in the private input directory
as `kubeconfig`. All images must be digest-pinned and available to their assigned
nodes; use `load_image.py` for local controller/generator images. The command does
not build images, create clusters, install KEDA, create load-test records or inject
faults. It refuses any existing `apexload-fault` namespace, including an owned one,
so it cannot upgrade an existing installation or interrupt its active tests.

```sh
python3 scripts/fault_harness/deploy_stack.py \
  --inputs /absolute/private/inputs --output /absolute/new/private-deployment \
  --controller-image "$CONTROLLER_DIGEST" --generator-image "$GENERATOR_DIGEST" \
  --target-image "$TARGET_DIGEST" --postgres-image "$POSTGRES_DIGEST" \
  --redis-image "$REDIS_DIGEST" --s3-image "$S3_DIGEST" \
  --prometheus-image "$PROMETHEUS_DIGEST" --minimum 2 --maximum 4 \
  --pod-capacity 100 --accelerated
```

Omit `--accelerated` to preserve production HPA timings. Rendering derives every
resource from the reviewed profiles and fresh private inputs. Before mutation, the
command checks all three Docker kind owners, Kubernetes node identities, namespace
absence, and KEDA/metrics API readiness. It binds the namespace UID returned by
`create`, then rechecks cluster and namespace identities between phases. It applies
the local kindnet resource adjustment, dependencies, non-overwriting authentication
bootstrap, application resources, Prometheus and autoscaling. KEDA ownership is in
the initial controller configuration, before controllers start.

Each rollout has a bounded readiness wait. Authentication bootstrap, readiness or
identity failures stop subsequent phases. Interruptions preserve resources, PVCs
and private phase evidence; there is no reset, automatic rollback or cluster delete.
A partial installation requires explicit inspection before a new deployment; do not
remove its data just to make the command accept the namespace. `result.json` records
`deployed: true` and **`qualified: false`**: Kubernetes readiness does not establish
application admission, sustained traffic, failover, archive integrity or production
availability. Run those evidence gates separately.


## Seed a private scenario

After deploying the owned stack, create its project, project-scoped load credential,
HTTP script and saved scenario:

```sh
python3 scripts/fault_harness/seed_scenario.py \
  --inputs /absolute/private/inputs \
  --output /absolute/new/private-seed-evidence
```

The command requires PostgreSQL, Redis and S3 in authenticated inventory. It pins
the kubeconfig, namespace UID, three node UIDs, active controller UID and execution
generation across its bounded setup window. Requests go through a loopback-only
pod forward with redirects/proxies disabled; the forwarding log pipe is drained
without retaining a buffer. The fresh target credential is stored as a project
secret, while the script uses `{{secret.fault_load}}` and the private TLS target.
The saved scenario has 40 VUsers, fixed 100 ms pacing, a five-minute duration and
controller failover policy `continue`. It does not opt into generator-loss recovery.

This command never creates or starts a test. `seeded: true` means saved resources,
and `qualified: false` remains explicit. Receipts contain resource IDs, not keys
or raw responses. Failure, interruption or leader replacement stops setup and
preserves already-created resources. The last mutation may have committed even
when its response was lost; inspect the receipts and owned project before retrying.
Existing output directories are refused, and no automatic deletion or rollback runs.


## Bootstrap a fresh Docker Desktop cluster

Prepare new inputs first, preload the approved `kindest/node@sha256:...` image into
Docker Desktop, and obtain a trusted local KEDA 2.21.0 chart archive with its
independently recorded SHA-256. Supply a private JSON file mapping `keda`,
`metricsApiServer` and `webhooks` to the corresponding official
`ghcr.io/kedacore/<component>@sha256:...` image references. Then run:

```sh
python3 scripts/fault_harness/bootstrap_cluster.py \
  --inputs /absolute/new/private-inputs \
  --output /absolute/new/private-bootstrap-evidence \
  --kind-binary /absolute/path/to/kind \
  --node-image kindest/node@sha256:<approved-digest> \
  --keda-chart /absolute/path/to/keda-2.21.0.tgz \
  --keda-chart-sha256 <approved-chart-checksum> \
  --keda-images /absolute/private/keda-images.json
```

The command is specific to the `desktop-linux` Docker Desktop context and a
three-node topology: one control plane and two workers. It requires at least
8 GiB of host free space, refuses existing cluster containers or kubeconfig files,
and never changes the user's default kubeconfig. Chart/values/config bytes and
engine/container/node/namespace identities are checked between phases. KEDA runs
on the control plane and all three deployed image references must match the pins.
The supported server versions are Kubernetes 1.34–1.36, matching the
[KEDA 2.21 compatibility range](https://keda.sh/docs/2.21/deploy/).

The supplied chart checksum authenticates only the artifact chosen by the operator;
obtain both chart and expected checksum from a trusted source. The command does not
fetch a chart or resolve mutable image tags. It uses `kind --retain` and ordinary
Helm install, preserves failed/interrupted resources and private receipts, and never
uninstalls or deletes clusters. Inspect retained resources before preparing another
run. A successful receipt reports Kubernetes/KEDA readiness with `qualified: false`;
application image loading, `deploy_stack.py`, `seed_scenario.py`, healthy controls
and fault trials are subsequent steps. This bootstrap command has passed simulated
lifecycle tests and a real pinned-chart render; fresh-cluster live qualification is
still pending.


The signed S3 probe now uses a deterministic, incompressible 64 KiB object at
`fault/<run-id>/retention-probe-v2`. Tiny probes can succeed in filer metadata even
when no object volume can accept an archive upload. Seed the new probe once, then
use check mode after restarts; existing v1 probe objects are preserved. This detects
an unavailable data-volume write path, not sufficient capacity for an entire trial.


The local controller profile limits each materialized archive to 256 MiB and reserves
a 512 MiB aggregate active-reader allowance per controller. Distinct cold archive reads
wait before object-store access when that allowance is full; readers of the same cache
share it. The separate idle-cache cap is 256 MiB. These payload limits do not include
unconfirmed producer sources or transfer spools; preserve the existing disk-space guards
and distinct data/tmp volume limits when running faults.
