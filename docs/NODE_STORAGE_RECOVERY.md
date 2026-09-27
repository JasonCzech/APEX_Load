# Worker and storage recovery runbook

Status: implementation guidance; no production node/zone RTO or RPO is qualified.
See [the readiness plan](NODE_FAILURE_READINESS_PLAN.md) for open release gates.

## State inventory

| State | Location and owner | Recovery requirement |
|---|---|---|
| Test records, execution lease, run-continuity checkpoint, pod reservations and archive catalog/outbox | Controller storage backend; PostgreSQL in HA installations | Recover from the authoritative database. Preserve generation/owner and wrapped-token keys. An older database restore cannot be combined with still-running generators without fencing the old installation. |
| Generator epoch high-water and supervision safety-exit intent | `APEXLOAD_GENERATOR_STATE_DIR`; `pkg/distributed/generatorrpc/execution_epoch_state.go` and `run_supervision.go` | Preserve with the same generator identity. Never reset this directory to clear an error or attach a stale copy while the original writer may run. |
| Running goroutines, protocol sessions, in-flight requests and local supervision deadline | Generator process memory | A volume does not recreate these. A replacement process is not an adopted running shard. Use the explicitly supported recovery path or fail the run. |
| Raw measurements and generator manifest | `APEXLOAD_ARCHIVE_DIR` beneath each generator's mounted state volume in production examples | Retain until controller archive collection completes. Permanent volume loss may permanently remove measurements; record missing shards rather than claiming complete evidence. |
| Controller archive staging and exports | `/var/lib/apexload` in shipped production manifests, currently `emptyDir` | Staging is transient. An uploaded object archive and durable catalog are the recoverable result. An outbox record does not replicate staging bytes. Verify retry after loss of the staging pod before qualifying the profile. |
| Imported files and governed runner artifacts | Controller import-assets PVC | All HA replicas must read the same contents. AKS HA selects `azurefile-csi`; EKS HA selects an operator-provisioned `efs-sc`; on-prem requires an appropriate shared storage class. |
| Live state/cache and cross-controller publication | Optional Redis/Valkey plus controller memory | Reconstruct from authoritative durable state and current observations. Cache availability alone does not establish execution ownership or archive durability. |
| Scratch files | `/tmp` `emptyDir` | Disposable; preserve relevant diagnostics separately before cleanup. |

## Deployment profiles

The local base uses ephemeral generator state (`emptyDir`) and a controller PVC.
Kind local-path volumes are tied to the Docker node filesystem. Returning that node
can recover its files; permanently deleting it is a different failure case. Docker
Desktop's nodes also share one VM and cannot qualify physical-node or zone isolation.

AKS, EKS and on-prem standing generators use a per-ordinal `ReadWriteOnce` claim.
The selected StorageClass, CSI driver, topology and reclaim policy determine whether
that claim can move to another worker or zone. Record those actual values, volume
handles, node affinity and attachment state before testing. `ReadWriteOnce` is not an
application fencing protocol. Shared controller import assets and object archives do
not replicate a generator's epoch file or uncollected raw measurements.

For every production profile, record: cluster/driver versions; node and zone spread;
controller and generator images; database/Redis HA configuration; PVC/storage classes;
lease and orphan-grace settings; archive backend and key provider; eligible unused
recovery capacity outside the failed domain. Keep credentials and wrapped tokens out
of the evidence bundle. No production profile is qualified by this document alone.

## Recovery procedure

1. Record the fault time and affected node, controller leader, generator identities,
   shard assignments/generations, PVCs and last target-observed traffic. Stop new
   admissions if capacity or ownership cannot be established. Preserve database,
   controller, generator and storage-event evidence before modifying resources.
2. Classify the node as partitioned, temporarily unavailable, or permanently fenced.
   A failed probe, expired Kubernetes lease or unreachable API is not proof that the
   generator stopped executing. Preserve the full application lease/orphan-grace
   bound; do not shorten it to meet a recovery target.
3. Prefer restoring connectivity for a temporary outage. For permanent loss, the
   infrastructure owner must prove the old node/writer cannot resume (for example,
   confirmed VM termination and storage fencing), recording the provider operation
   and time. Kubernetes warns that force deletion can create duplicate StatefulSet
   identities because it does not wait for the old process to stop; follow its
   [StatefulSet deletion guidance](https://kubernetes.io/docs/tasks/run-application/force-delete-stateful-set-pod/).
4. Choose one recovery path. To retain the original identity, preserve its PVC and
   epoch files, have the CSI driver complete safe detach/attach, and let the ordinary
   StatefulSet replacement proceed after the old identity is released safely. Do not
   delete claims, strip finalizers or reset epoch files to bypass attachment failures.
   If storage cannot be recovered, quarantine that identity and record its lost data;
   replacing a shard requires a different eligible identity through controller-owned
   fenced recovery. Combined leader/generator replacement remains an open gate.
5. Verify the authoritative controller generation, generator readiness and admission,
   current shard ownership, actual target traffic, and absence of old-owner traffic.
   A Ready pod or historical TPS value alone is insufficient. Explicitly stop/mark
   failed any run that cannot satisfy its recovery contract.
6. Reconcile archive catalog/outbox and object checksums. Separately record surviving
   measurements, irretrievable shard files, and any staging loss. Check a new test and
   archived-result reads before reopening normal admissions.
7. Restore controller/generator failure-domain spread and spare capacity. Drain or
   move workloads through supported operations after active runs finish; avoid an
   automatic rebalance that kills active generator processes. Restore policy settings
   and verify no resource remains unintentionally cordoned or paused.

This runbook intentionally contains no automatic force-delete command. Infrastructure
fencing and CSI recovery are platform operations whose preconditions must be proven.

## Qualification record

Run temporary outage, permanent worker termination, zone loss, detach/attach failure,
partition/heal and old-node late return separately. Include a second controller loss
while recovery is underway. Verify no duplicate old/replacement ownership, no traffic
after stop/expiry, and explicit handling of missing measurements. Exercise PostgreSQL
and Redis primary failover separately from generator loss.

Report each trial's API outage, leadership acquisition, generator discovery, target
traffic recovery, pod scheduling/attachment, and archive recovery times. Measure
RPO as missing observations against the target's independent counter and shard
coverage, not just the existence of a final manifest. Use at least three trials per
local scenario; publish individual outcomes. Multi-host CSI and zone trials must name
the tested infrastructure and retain fencing/attachment evidence. Until those gates
pass, leave RTO/RPO as **unqualified**, including zero-loss claims.
