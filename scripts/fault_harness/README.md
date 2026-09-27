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
a random ownership record. Nothing is copied from earlier deployment snapshots.
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
and volume is ready. Verify Kubernetes readiness and resource recovery separately.
The driver does not change replicas, force-delete pods/PVCs, or destroy its cluster.

The output directory must be new; retries refuse to overwrite existing evidence.
Evidence is an allowlisted JSON event stream with private permissions. It captures
traffic observations, assignments, generation and timing; raw API/Secret responses,
command output and arbitrary adapter fields are excluded. The collector below adds
before/after Kubernetes inventories. During-fault time series, measured dependency
failover and final archive evidence still require the stack adapter. A passed traffic gate alone is not a passing completion, archive,
no-overlap, liveness, cleanup-readiness or production-availability qualification.


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
- Deployment/StatefulSet replica state and HPA/KEDA targets, bounds and conditions.
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
resource gap names. Snapshot reads never run inside the stopped-node recovery
window or before the restoration command in cleanup.

A preflight ownership/snapshot failure or artifact-write conflict prevents injection.
Individual unavailable resource APIs remain recorded gaps and do not independently
fail the traffic gate. A diagnostic read outage after restoration records
`cluster_evidence_unavailable`; it does not hide the original safety/traffic failure.
If the trial otherwise succeeds but the diagnostic result cannot be recorded, the
driver fails. The restored checkpoint observes the cluster immediately after Docker
container restoration; it does not wait for or certify pod/PVC readiness. These two
checkpoints do not provide a during-fault scaling trace or qualify recovery.
