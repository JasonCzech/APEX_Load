# Disposable node-fault driver

This is the repository-owned safety/evidence foundation for the
[node-failure plan](../../docs/NODE_FAILURE_READINESS_PLAN.md). It does not yet
provision the application, provide its observation adapter, or qualify the complete
fault matrix. Docker Desktop containerd became read-only during development;
container-based validation of this driver remains open. Unit tests use a fake command
boundary; fresh certificate tests execute OpenSSL.

```sh
python3 -m unittest discover -s scripts/fault_harness -p 'test_*.py'
python3 scripts/fault_harness/prepare_inputs.py /absolute/new/private-directory
```

The second command refuses an existing directory and generates fresh credentials,
a private CA, controller/generator certificates, a three-node kind configuration and
a random ownership record, and a separate target/observer credential pair in `target.json`. Nothing is copied from earlier deployment snapshots.
The directory must remain private; do not commit it or include its credentials in
public evidence. Certificates last two days. Install the generated values through
Secret resources, with the CA trust and server names required by your deployment.
The credentials file is input data, not an environment file.

Create the disposable kind cluster with the exact name in `ownership.json`, write
its kubeconfig into that private directory, and deploy the test stack in namespace
`apexload-fault`, labelled with `namespace_label` from the ownership record. Keep the
control plane and instrumented target independent of the selected failed worker.
Use at least two worker nodes, multiple generator pods on each, PostgreSQL, Redis,
object storage, Prometheus and KEDA. Pin and record images. The production-like stack
preparer and real observation adapter are still required before an end-to-end run.

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
observation time, not a timestamp added to old dashboard data. Include `run_id`,
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
are bounded at 30 minutes. Poll/command errors cannot silently pass the traffic gate. A transport outage resets
only the consecutive-load window; it does not erase the last target counter. Duplicate
ownership, a counter reset (including across an observation outage), invalid ownership/count evidence,
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
