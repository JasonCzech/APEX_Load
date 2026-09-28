# Confirmed object-archive source retention

`APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL` opts controllers into retiring local copies
of successfully published object archives. It accepts durations from `1m` through
`720h`; unset, zero, invalid and out-of-range values disable retirement. Enable it
only after every controller that can write the same staging volume runs a version
with the publication-source file fence. An older writer does not participate in
that fence. Source retirement is disabled by default and has not yet been qualified
in a deployed fault trial.

Collection and publication hold a per-run cross-process file lock for both filesystem
and object backends, including when source retirement is disabled. Lock inodes
remain after source cleanup so concurrent writers cannot acquire different locks
for the same run. Collection is tied to the current controller leadership epoch;
lease loss cancels pending downloads and prevents a new source from being committed.
Before collecting or replacing a source, the controller checks the durable run
identity, cleanup state and existing publication. An existing outbox or catalog
record is reused; a resolved archive removed by retention is not collected again.
An existing merged directory without a publication owner is preserved for recovery;
a later collection cannot overwrite it, even if that directory is incomplete.

The publisher
creates a local cleanup receipt only after object upload and the durable archive
catalog commit succeed. The receipt identifies the immutable object version and
manifest SHA-256. A failed upload or catalog commit leaves its source available for
retry. Producer retries reuse the current durable intent instead of replacing its
newer manifest hash with an older in-memory copy; an already committed catalog is
returned without publishing again.

The periodic controller archive worker, including on followers, checks confirmed
sources under `remote/<run-id>` and `_publication/<intent-token>/<run-id>` in the
current configured staging namespace. Once the TTL expires, it acquires the source
file lock, checks that no finalization job remains, revalidates directory identity
and manifest hash, and removes only that local source. Reader materialization
caches, canonical filesystem archives, local-engine archives and unconfirmed
producer directories are excluded. Object-store contents and catalog rows are
unchanged. Readers continue to download and verify the published archive.

If a controller crashes after catalog commit but before writing the receipt, the
worker can recover it only when the catalog identifies that exact local source and
its immutable manifest matches both the local file and the remote commit marker.
Recovery starts a new full TTL. Missing objects, mismatched hashes, changed
endpoints, replaced directories or pending publication retain the source. Sources
left in an older configured namespace need separate investigation; changing the
backend configuration does not authorize sweeping unrelated namespaces.

Each sweep has a twenty-second context budget, one-second lock/confirmation
attempts, and a 10,000-entry traversal limit. The small lock files are retained to
preserve lock identity for other processes. The underlying filesystem must support
the advisory file-lock semantics used by the controller; independently qualify
shared-volume behavior on the deployment platform.

This is TTL retention of confirmed copies, not a total disk quota. Unpublished
archives are deliberately preserved during storage outages. Size the staging
volume for collection, concurrent publication, retry backlog and the TTL window;
monitor free space and pending finalization jobs. The separate materialization
cache byte cap applies only to downloaded reader caches. Neither setting makes a
single-host Docker Desktop trial a production storage-availability qualification.

The disposable local renderer/deployer can set this explicitly with
`--source-retention-seconds 300`; zero omits the setting. For reader-cache growth,
newly downloaded archives enforce the idle byte policy synchronously when their
reader lease is released. Cache hits and idle TTL maintenance still use coalesced
periodic sweeps. Active readers remain protected from eviction. A separate
`APEXLOAD_ARCHIVE_ACTIVE_READER_MAX_BYTES` budget bounds active reader/download
payload admission per controller process (default 256 GiB). Each distinct cache
holds its configured `APEXLOAD_ARCHIVE_MATERIALIZATION_MAX_BYTES` allowance until
its last reader or waiter releases it; simultaneous readers of that same cache share
one allowance. The conservative full allowance also covers old manifests without
reliable artifact sizes. Set the active budget at least as high as the per-archive
limit; a smaller budget rejects that read instead of waiting forever.

When the aggregate budget is full, another cache waits within the existing request
and materialization deadline, before opening the object store. Cancellation and
failed transfers release the reservation. Cached archives are checked against their
actual manifest and artifact bytes, so a cache created under an older, larger limit
cannot bypass admission. This is a payload working-set bound, not a total filesystem
quota: idle caches, replacement copies, producer sources, transfer spools and filesystem
metadata need additional space. The budget is process-local; separate controllers on
a shared volume need independent sizing and platform qualification. The disposable
Docker Desktop profile uses 256 MiB per archive, a 512 MiB active-reader budget and
a separate 256 MiB idle-cache cap.

SQLite and PostgreSQL finalization also keep a private prepared-manifest journal.
After adjunct files are durably written, the next manifest bytes and SHA-256 are
committed together in the pending job before the local manifest is replaced. A
retry can recover an interrupted replacement only from that database-anchored
payload and only after all declared artifact sizes and hashes pass validation.
The payload is capped at 8 MiB, excluded from job-list snapshots and API JSON, and
removed with the completed job. A record-only update cannot replace its digest.
Custom outbox implementations without the preparation extension retain their
previous behavior. Jobs damaged by older writers without a prepared payload are
not automatically re-trusted; their source must still be investigated separately.

Distributed runs with durable source identities also retain successfully downloaded
sources under `_collection/<run-id>/<intent-token>`. A required-source failure does
not discard completed peers. SQLite/PostgreSQL anchor each private receipt's hash
and byte count; replays verify the receipt and payload hashes while copying into a
fresh transfer spool. Original assignment identity survives controller/token changes.
Legacy unbound runs retain their existing collection behavior.

`APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES` freezes a positive per-run cache ceiling
(default and maximum 32 GiB). `APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES` limits
payload and receipt bytes across the configured cache root (default 128 GiB).
Both accept integer byte counts; invalid values fail collection. The shared capacity
file lock serializes admission, counts orphan/unanchored files, rejects symlinks,
and bounds traversal to 100,000 entries and six directory levels. Each new copy
requires its entire size plus 64 MiB of currently available space. Existing verified
sources remain readable when the aggregate ceiling is lowered. Changing configuration
cannot enlarge an already frozen run ceiling.

These limits cover the collection cache; transfer spools, merged sources, producer
archives, filesystem metadata and publication backlog still require separate sizing.
No unconfirmed cache evidence is automatically pruned. Terminal bound runs now have
a durable, bounded collection retry worker as described below.
This cache has source/unit/database validation; deployed fault qualification is pending.

Startup orphan recovery now requires a storage extension that atomically checks
the exact run start/end timestamps, terminal status, project and current execution
lease before creating publication work. Resolved collection intents cannot be
resurrected after catalog retention, including by producer retries. Recovery holds
the source file fence and skips already-published, pending, deleted and resolved
runs before reading their payloads.

A genuine orphan must have complete artifact size/hash descriptors; existing files
are verified through the inspected directory handle before publication can rewrite
adjunct metadata. Verification uses the configured per-archive materialization byte
ceiling. Missing, changed, unanchored or excessive payloads remain untouched for
investigation. Legacy orphan sources without integrity descriptors are preserved
rather than automatically re-trusted. Discovery counts all directory entries toward
its 10,000-entry budget, including entries that are not archive candidates.

SQL receipts are shared metadata; cached files belong to the configured local
staging root. A controller with a fresh staging directory can download an absent
source again only if the full receipt and byte count match the existing SQL anchor.
That verified copy does not spend the logical per-run ceiling twice, while every
local copy still counts against its own root's physical byte quota. An existing
incomplete or corrupt cache directory is preserved and rejected, not silently replaced.
Receipts alone cannot recreate bytes when neither a local copy nor the original
generator source is available. Per-pod `emptyDir` staging is not shared durable storage.

Confirmed collection-cache copies are now retired independently on each controller's
local staging root. A serial worker checks immediately and every minute, with a
30-minute sweep deadline and a one-second source-lock acquisition budget. It requires
the exact terminal run, resolved collection intent, no pending publication/cleanup,
and a catalog-bound primary manifest that records the source as collected. Filesystem
primaries also have their payload hashes checked; immutable object primaries require
only their manifest commit marker. Corrupt/missing markers preserve cached sources.

Before removal, cleanup verifies the SQL-anchored receipt, every cached payload,
exact directory contents, and directory identity. Source/capacity locks protect
removal; unexpected files and unanchored siblings are preserved. Partial publication
retires only sources explicitly marked collected. SQL receipts remain unchanged so
other staging roots retain independent cleanup/refetch authority. This worker runs
separately from publication retries and is canceled and joined on server shutdown.

Run identity comes from `provenance.requested_at`, independently of the earliest
generator creation time in `created_at`. Legacy manifests without request provenance
must match their creation timestamp instead. The SQL orphan-insertion fence receives
the inspected request time separately and never rewrites the immutable creation time.

A separate leader-only collection worker checks unresolved terminal intents at startup
and once per minute. It scans at most 100 due intents per sweep, with a 30-minute
operation context and one-second source-lock acquisition budget. Each network attempt
is claimed in SQL before I/O; retries wait at least one minute and cannot reset their
three-attempt budget after controller restart. Only the recorded generator targets
are dialed (at most eight concurrently), using the configured distributed transport.
Every owned transport closes after the attempt. No VUsers are dispatched or controlled.

After three network attempts, or six hours after durable terminality, a separate
cache-only pass performs no generator RPCs. Verified surviving sources may become
an explicitly partial archive; no usable sources or invalid retained evidence mark
collection exhausted while preserving files. Shutdown or leadership cancellation
leaves the final decision for a later owner. Existing merged sources are recovered
through the same terminal-incarnation and integrity fences instead of being replaced.

Recovered manifests retain the exact durable terminal outcome and completion time.
They use best-effort coverage and an explicit incompleteness notice even when all
recorded generator sources are available, because volatile controller-only timeline
and telemetry evidence cannot be reconstructed from those receipts. Publication is
queued through the transactional request-time/status/end-time/lease fence. Automatic
collection does not recreate a lost source for an already-resolved publication outbox;
per-pod emptyDir loss during pending publication remains a separate recovery item.

Single-run terminal API reads expose a bounded `archive_recovery` projection of
durable bookkeeping. Results distinguish queued collection, cache-only recovery,
pending publication, exhausted recovery and resolved-but-unavailable archives.
Verified-source counts are historical receipts, not proof that files survive.
Attempt counts are durable budget claims and do not prove a network call occurred.
Uncertain identity or storage reads omit the projection; they never probe generators
or storage files to guess progress. Earlier collection failures remain historical
context. **Check again** refreshes the read; it does not restart recovery.

## Shared controller staging

Controller archive staging is ephemeral by default. Helm now supports a dedicated
claim at `/var/lib/apexload/archives`, separate from imported assets and exports:

```yaml
archiveStaging:
  persistence:
    enabled: true
    existingClaim: apexload-controller-archive-staging
    accessModes: [ReadWriteMany]
```

HA requires an explicit `ReadWriteMany` declaration, including for existing claims.
Helm cannot verify the actual access mode or filesystem semantics of an existing
claim. Provision and qualify it in the release namespace first. For chart-created
claims, leave `existingClaim` empty and set `storageClassName` to a qualified class;
`size` defaults to `256Gi` and accepts positive integer Kubernetes binary quantities.
An empty class uses the cluster default. The chart selects no cloud-specific class.
Chart-created staging PVCs carry `helm.sh/resource-policy: keep`: disabling the
option or uninstalling the chart preserves the PVC, but detaches it from controllers.
Retained claims require explicit operator management before reuse or deletion.

Kustomize users can add `deployments/k8s/components/archive-staging` to an overlay
of `aks`, `eks`, `onprem`, or their HA variants. For example, a sibling directory
under `deployments/k8s` can contain:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - ../onprem-ha
components:
  - ../components/archive-staging
```

The component mounts an **existing** `apexload-controller-archive-staging` claim in
the deployment namespace; it does not create a PVC or select a storage class. Patch
the `archive-staging` volume's claim name if needed. HA requires a shared writable
claim accessible to all controller replicas. Both deployment methods pin object
staging beneath the mounted archive directory so a ConfigMap override cannot move
it back to ephemeral scratch. Downloaded reader caches stay in each pod's private
`/var/lib/apexload/archive-read-cache` scratch directory through
`APEXLOAD_ARCHIVE_READER_CACHE_DIR`. Their leases and eviction locks are process-local,
so this directory must never be a shared volume. Custom deployments must set a
private absolute reader-cache directory when sharing source staging. Source/cache
path overlap (including existing symlink aliases) is rejected on materialization;
without the override, legacy deployments retain their original cache layout.

The filesystem must support cross-process and cross-client advisory `flock`, atomic
rename and durable `fsync`. Access mode alone does not establish those properties.
Use the same absolute mount path, backend namespace and controller UID/GID on every
replica. Preserve the complete directory tree, including `_source_locks` and capacity
lock inodes; replacing lock files while writers exist can allow concurrent owners.
Only one deployment may own a staging namespace. Do not share this claim with import
assets, Bruno artifacts or unrelated applications. Validate mount permissions and
storage-network access for the selected CSI driver before rollout.

Enabling a volume does not migrate existing per-pod sources. Before changing mounts,
quiesce archive writers, inventory every controller's pending publication and cached
sources, and copy verified evidence while preserving namespace layout and identity.
Conflicting or unverified copies need investigation; never overwrite them blindly.
Keep the old evidence until the migrated publication is verified. Apply the same
procedure before disabling persistence, changing claims, or rolling back the mount.
All writers must run the source-fencing implementation before sharing this tree.

Size collection quotas below the claim's usable capacity, reserving space for
transfer/merge copies, prepared publication and filesystem overhead. Size each pod's
private scratch separately for reader caches and their active-reader budget.
The default 128 GiB collection-cache quota is not a total staging-disk bound;
producer/publication backlog admission remains separate work. Exhaustion must not
be resolved by deleting unconfirmed evidence. This option does not enable source TTL.
It does not replicate generator raw files, recover lost execution ownership, or
qualify physical CSI detach/reattach, permanent-node or zone failures. The local
cross-process test exercises shared-directory locking and publication after process
loss; production multi-host storage qualification remains required.

## Controller transfer capacity

Controller downloads now use a dedicated transfer directory, with aggregate
admission before any generator or cached-source read. These settings apply to every
controller that uses the same directory:

| Setting | Default | Meaning |
| --- | --- | --- |
| `APEXLOAD_ARCHIVE_TRANSFER_DIR` | `apexload-archive-transfers` under the process temporary directory | Absolute, dedicated transfer scratch directory. Keep it separate from source staging and reader caches. |
| `APEXLOAD_ARCHIVE_TRANSFER_MAX_BYTES` | 128 GiB | Aggregate admitted payload bytes, supplied as a positive decimal integer (maximum 1 PiB). |
| `APEXLOAD_ARCHIVE_TRANSFER_MIN_FREE_BYTES` | 64 MiB | Minimum filesystem free space preserved before each payload write, supplied as a positive decimal integer (maximum 1 PiB). |

Admission reserves the run's full frozen collection-byte ceiling, or 32 GiB for
legacy collections without a durable cache intent. A reservation lasts until the
merge finishes and releases the downloaded bundles. For example, a 512 MiB transfer
quota admits two collections whose frozen ceilings are 256 MiB each, even if their
actual files are small. A lower new configuration does not shrink existing durable
run ceilings; such runs may remain unable to collect until capacity is restored.
The transfer quota is separate from collection-cache, reader-cache and active-reader
budgets. Size their combined possible usage, merge/publication copies, filesystem
metadata and other scratch users below the actual volume limit. Filesystem free
space checks do not discover Kubernetes `emptyDir.sizeLimit`.

Cross-process file locks serialize admission and free-space checks. Operations
retain their full reservations after a crash. Unrecognized leftovers count at their
actual size; oversized, malformed, linked or excessively deep inventories fail
admission. Capacity pressure and elapsed time never authorize deleting abandoned
files. Investigate stale reservations only after quiescing all users of the root
and preserving or verifying any evidence needed for collection/publication recovery.
Keep capacity lock inodes intact. Normal cleanup only removes the operation's own
unchanged directory and reservation; uncertain cleanup leaves evidence charged.

The quota counts payload reservations; small marker files, directory entries and
filesystem allocation overhead require additional volume headroom. It is not a
bound on RAM used by manifests or on unrelated writers to the same filesystem.
Old transfer directories directly under the process temporary directory are left
untouched after upgrade and are outside the new logical inventory; physical free
space checks still include their disk usage. Transfer admission is separate from controller-local raw capture, derived
merge/report expansion and publication backlog.


## Controller raw capture limits

Each new controller-local run freezes `APEXLOAD_ARCHIVE_CAPTURE_MAX_BYTES`
(default 32 GiB, maximum 32 GiB) and `APEXLOAD_ARCHIVE_CAPTURE_MIN_FREE_BYTES`
(default 64 MiB, maximum 1 PiB). Both accept positive decimal byte counts. Invalid
configuration prevents creation of the writer. The byte ceiling is shared by the
transaction, resource, interval-summary, Elasticsearch and Dynatrace JSONL files.
Whole serialized records are admitted before buffering; a record over the remaining
allowance is rejected without appending a partial JSON line. The physical free-space
check runs at buffered file writes under a cross-process lock in the archive root.
Keep `_capture_locks` and its lock inode intact while writers can use this root.

Any capture failure is sticky across all five record types. Finalization attempts
to flush accepted records and close every file, preserves raw evidence, and refuses
to publish a complete manifest. The execution result remains unchanged; the existing
terminal archive-availability marker reports an unconfirmed archive where applicable.
A filesystem/write error can leave a partial final raw record; that source must not
be treated as a complete archive. No capacity failure deletes earlier evidence.

This is a per-run raw-byte bound. It does not reserve the entire allowance on disk,
limit aggregate concurrent capture or retained unpublished sources, or cover derived
Parquet/CSV files, merge copies and publication backlog. Those budgets remain separate
work. Include filesystem overhead and other writers when sizing the volume; free-space
checks cannot enforce an `emptyDir.sizeLimit` smaller than its backing filesystem.
