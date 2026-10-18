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

A failed-over run is the one case where the end times legitimately differ: the
executing controller finalizes its archive at its own time, and the successor
reconciles the run row as `FAILED` at a later time. The run row records the HA
execution lease generation of the term that executes the run (`tests.execution_generation`,
schema release 63). The fenced Start commit stamps it, run adoption advances it,
and ordinary row saves and reconciliation never change it. Archive provenance
records the same generation as `execution_generation`. When the start time, project
and terminal status match but the end time does not, recovery accepts the archive
only if both generations are present and equal. It prepares a restamped manifest
in memory: `completed_at` becomes the durable run end, and the executor's time is
kept as `executor_completed_at`. The prepared bytes and their digest are committed
with the publication job, through the same exact end-time and lease fence. The
source is not modified before that fence. Finalization restores the prepared
manifest only after verifying every artifact digest, so a refused or interrupted
recovery leaves the source byte-for-byte unchanged. Rows and
archives without a generation (non-HA runs, and runs from before release 63)
keep the exact end-time rule. An archive from a term that lost the run to
adoption no longer matches the advanced generation, so it stays unpublished.

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

Voluntary idle epoch turnover, used to recover restored generators that reject an
expired generation, waits for terminal remote cleanup, active local archive fences,
current unresolved collection intents and pending primary publication jobs. Queued
runs may therefore wait for archive retries to settle before the fleet obtains a
new generation. Resolved or explicitly exhausted collection does not block this
turnover, and the guard never retires source data. This protects private staging
during voluntary turnover; it does not make private files survive involuntary
controller or volume loss.

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
loss. The opt-in `postgres_integration` test additionally replays publication
from a fresh process against PostgreSQL and S3-compatible storage, using
`APEXLOAD_TEST_POSTGRES_URL`, `APEXLOAD_TEST_ARCHIVE_S3=1`, and explicit
`APEXLOAD_ARCHIVE_ENDPOINT`/bucket/region and AWS test credentials. Both tests
share a directory on one host; production multi-host storage qualification
remains required.

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
Parquet/CSV files, merge copies or publication backlog. Conversion and merge have
separate per-attempt limits below, as does the private publication copy. Aggregate
admission remains separate work. Include filesystem overhead and other writers when sizing the volume; free-space
checks cannot enforce an `emptyDir.sizeLimit` smaller than its backing filesystem.

## Parquet and CSV conversion limits

The controller shares one output-byte allowance across all three Parquet files and
all declared CSV exports in a finalization attempt. `APEXLOAD_ARCHIVE_CONVERSION_MAX_BYTES`
defaults to 32 GiB and accepts positive integer bytes up to 128 GiB.
`APEXLOAD_ARCHIVE_CONVERSION_MIN_FREE_BYTES` defaults to 64 MiB and accepts positive
integer bytes up to 1 PiB. Local writers freeze both settings at creation;
distributed collection freezes them before conversion.

Admission checks occur at each physical write, including Parquet headers, row groups
and footers. Conversions coordinate the free-space check with raw capture under the
same archive root. The first write failure stops further output through that shared
budget. Removed incomplete output stays charged for the attempt. Conversion does not
modify source JSONL files. A failed conversion cannot publish a complete manifest.
Local conversion failures preserve the execution outcome. If local capture or
conversion prevents finalization, the controller records `local_archive_failed`
after the exact terminal run is durable. Results identifies this as a local
finalization failure; it does not imply that a generator failed or that automatic
reconstruction is running. Accepted raw files remain available when their writes
succeeded. Terminal-write retries retain the pending observation on the same run.
If the specific failure record cannot be saved after bounded retries, the existing
durable unconfirmed marker remains the safe fallback. A pending publication or
catalog record takes precedence. Distributed merge/conversion
byte-limit and free-space rejection also preserve that outcome and record a durable
archive-failure marker; missing or invalid required sources keep the existing strict
failure behavior. Local raw capture remains on disk; distributed collection retains its existing source-cache
and temporary-file lifecycle. CSV metadata records incomplete
exports when their writes fail.

This allowance covers format conversion only. Distributed merge uses its own
allowance below. The private publication copy also has its own allowance. Report
generation, aggregate concurrent producer retention, and retained unpublished
backlogs still require separate admission. A per-attempt conversion limit is not a
total archive-root quota.


## Distributed merge limits

One allowance covers merged transaction and resource JSONL, both the provisional
and final interval summaries, controller Elasticsearch/Dynatrace evidence and its
resource appends, and the final manifest. `APEXLOAD_ARCHIVE_MERGE_MAX_BYTES` defaults
to 64 GiB and accepts positive integer bytes up to 128 GiB.
`APEXLOAD_ARCHIVE_MERGE_MIN_FREE_BYTES` defaults to 64 MiB and accepts positive
integer bytes up to 1 PiB. Both settings are frozen before source collection for
that merge attempt. Parquet and declared CSV output use the separate conversion
allowance; both allowances must fit alongside retained sources and filesystem overhead.

Each physical output write checks remaining bytes and free space under the shared
`_capture_locks/capacity.lock` fence. JSONL uses bounded 64 KiB buffers that must flush
before a successful merge stage can finish. The first write failure or cancellation is
latched across output files. Removed provisional output stays charged for the attempt.
Telemetry evidence is serialized incrementally. A failed attempt cannot commit the
merged archive directory. Output-capacity rejection preserves the observed execution
outcome and records a durable archive-failure marker. Verified cached sources remain
available for a later retry; incomplete private merge output follows the existing
cleanup path. Artifact data and nested directory entries are synced before the merged
manifest and final directory rename; Parquet writers sync their writable descriptors.

This limits one merge attempt, not aggregate retained or concurrently produced archives.
Physical free-space checks cannot enforce a smaller Kubernetes `emptyDir.sizeLimit`.
Private publication copies have the separate allowance below. Aggregate producer/source
backlog and report output remain separate admission work; no pressure-based deletion
of unconfirmed evidence is introduced.


## Private publication copy limits

A refresh copies the immutable materialized archive into token-private staging
before adding report artifacts. That copy freezes
`APEXLOAD_ARCHIVE_PUBLICATION_MAX_BYTES` (default 64 GiB, maximum 128 GiB) and
`APEXLOAD_ARCHIVE_PUBLICATION_MIN_FREE_BYTES` (default 64 MiB, maximum 1 PiB),
both positive decimal integer byte counts. All declared artifacts and the manifest
share the allowance. Physical writes use the same capacity fence as raw capture,
merge and conversion, with bounded 64 KiB copy buffers and cancellation checks.

The source is never modified. A source directory that aliases the destination is
rejected before removing any files. On retry, the old destination manifest is removed
and that removal synced before artifact replacement. The new manifest is copied to
a temporary file and becomes visible only after artifact data and directory entries
are synced. The run, token and publication directory entries are synced before
successful return. Failed or canceled copies retain their original source and do
not advertise a complete new copy; the existing finalization job remains retryable.

This bounds the copy into publication staging. Finalization metadata uses the
separate allowance below. Report generation, outgoing object-store bytes, concurrent
copies and total retained backlog remain outside this copy allowance. No source
is deleted to make space.


## Finalization metadata limits

`APEXLOAD_ARCHIVE_ADJUNCT_MAX_BYTES` defaults to 64 MiB and accepts positive
integer bytes up to 1 GiB. `APEXLOAD_ARCHIVE_ADJUNCT_MIN_FREE_BYTES` defaults to
64 MiB and accepts positive integer bytes up to 1 PiB. Each finalization attempt
freezes these settings before prepared-manifest recovery. Restored manifest bytes,
`test_notes.json`, `test_manifest.json` and the replacement `manifest.json` share
one allowance. A direct metadata refresh uses the same admission rules.

Physical writes use the shared archive capacity fence and check descriptor-based
free space. Replacement data is written to a temporary file, synced, then renamed;
rejection or cancellation preserves that file's previous version. Earlier metadata
writes in the attempt may already have committed, but the archive manifest cannot
advance past its durable preparation and successful write. The existing prepared
manifest journal and publication outbox support retry without changing execution
outcomes. A retry receives a new allowance; removed or replaced bytes are not
refunded within an attempt, and recovery itself consumes that attempt's allowance.
No source data is deleted to make space.

`exemplars.jsonl` has its own allowance too,
`APEXLOAD_ARCHIVE_EXEMPLARS_MAX_BYTES` (default 64 MiB, up to 4 GiB), with
the same truncate-and-mark behaviour, as do `clock_observations.jsonl`
(`APEXLOAD_ARCHIVE_CLOCK_OBSERVATIONS_MAX_BYTES`, default 8 MiB) and
`telemetry_sources.jsonl` (`APEXLOAD_ARCHIVE_TELEMETRY_SOURCES_MAX_BYTES`,
default 4 MiB). `telemetry_sources.jsonl` records each generator process's
publication coverage per execution generation; loss is exact only for an
epoch observed from its first publication to its terminal one, and an
adopted run carries an `unobserved` record for each earlier generation. The
epoch travels on `MetricsBatch` as `source_epoch` and
`source_epoch_started_unix_ms` (fields 31 and 32). A generator that predates
source epochs sends neither; its publications are recorded under an empty
epoch, counted as `legacy_epochs` in the summary, and their loss is only a
lower bound. That count stands in for the specification's `legacy_unknown`
label. Gap ranges and observation windows are not persisted per interval yet.

Each record also carries `counters`: the process's cumulative
`dropped_results`, `shedded_arrivals` and `dropped_subscriber_batches` for the
test at its last publication, each with a `state` of `exact`, `lower_bound`
or `unavailable`, its `value`, and the `reason` it is not exact. A count is
exact only for an epoch observed from its first publication to its terminal
one with no publication lost, whose count never decreased and was never
negative. A generator marks the snapshot a new subscription starts from
(`MetricsBatch.sequence_prefix_state_only`, field 33) when every earlier
sequence was a state-only staging or activation publication, so an epoch
observed from that snapshot counts its origin as observed; the generator also
confirms each subscription with a header before the controller activates the
run, so no metric window precedes it.

Reasons, per level:

| Level | `lower_bound` (largest count seen) | `unavailable` |
| --- | --- | --- |
| Epoch counter | `legacy_source`, `counter_regressed`, `invalid_value`, `unsequenced_publication`, `origin_unobserved`, `terminal_unobserved`, `epoch_not_fully_observed`, `publications_lost` | `not_reported`, `invalid_value` (every reading negative), `process_wide_only`, `generation_unobserved` |
| Run total | the first non-exact epoch's reason, `source_` plus an unavailable process's reason (`source_not_reported`, `source_invalid_value`, `source_process_wide_only`, `source_generation_unobserved`), `ledger_truncated`, `generation_unobserved`, `overflow`, `legacy_source`, `counter_regressed` (an earlier generation reported more) | `not_reported` or the first process's unavailable reason; `controller_wide` (WebSocket drops) |
| Run `lost_publications` | `epoch_not_fully_observed`, `ledger_truncated`, `generation_unobserved`, `legacy_source`, `overflow` | — |

Subscriber drops are attributed to the test only by generators that send
`test_dropped_subscriber_batches` (`MetricsBatchTelemetry` field 9); older ones
report only their process-wide count, which reads as unavailable
(`process_wide_only`), never zero. The run's totals are `telemetry.loss` in
the live metrics and archived interval summaries: a generator process keeps
one cumulative count across the execution generations it serves, so it
contributes its latest count once, and separate processes add up. A total is
exact only when every process's count is, no generation went unobserved and
the ledger kept every epoch; a source that reported nothing, or a sum that
overflows, leaves a lower bound. `lost_publications` totals the coverage
loss above. The WebSocket drop counters are shared by every run on the
controller and read as `unavailable` (`controller_wide`) in a run's totals. A
run executed in the controller's own process reports its dropped results and
shed arrivals as exact.

The manifest's `telemetry_coverage` is the run's wall-time telemetry account
(`telemetry_coverage.v2`): the run window, each generator's observed time
(from its publications' windows on the controller's receipt clock, overlaps
counted once; a publication whose sequence directly follows the previous one
in the same epoch also covers the receipt jitter since it, so a continuous
stream stays one span), the observed time, and healthy, degraded, breached
and unknown time with `classified_ms` as the denominator of
`breached_known_fraction`, beside retained and classified interval counts and
the run's telemetry policy segment (`unavailable: immutable-policy-invalid`
when the captured policy fails validation). A moment is observed only when
some generator was required then and every generator required then was
observed: each is required from its assignment (or first publication) until
its retirement — an evacuation, its terminal publication, or completion
before an HA adoption — and a placed generator this controller has no record
of for the whole window. Time no observation covers, time no generator was
required for, an unclassified or `unscored` interval, and time before an HA
adoption are unknown, never healthy. A run is distributed when it has a
source ledger, clients or a durable generator placement (a local run's
runtime-resource placement, which names the controller's own process, is
not one), so one finalized without its source ledger (after a restart, or by
a controller that never executed it) is unknown throughout, never observed
throughout, and one adopted mid-run is unknown before the adoption. Either
way `observer_lost` (`ledger_lost` or `adopted`) says observed time is a lower
bound, live and at finalize. Each source records `required_ms`, the time it
had to be observed, and `unknown_ms`, the part of that it was not;
`source_requirements` marks coverage that records them (coverage written
before them lists only each source's observed time). Spans are timed by the controller's
wall clock at receipt, so a backward step of that clock during a run can
overlap later spans with earlier ones and hide a gap (coverage overstated by
at most the step). Past 1,024 disjoint spans per
generator the shortest is dropped to admit a longer one (`truncated`, a lower
bound). Coverage is read from the interval rows' timestamps, windows and
statuses only, and the run's cumulative coverage is not copied into archived
rows.

`interval_distributions.jsonl` has its own allowance,
`APEXLOAD_ARCHIVE_INTERVAL_DISTRIBUTIONS_MAX_BYTES` (default 256 MiB, positive
integer bytes up to 4 GiB), with the same free-space floor. It is separate so a
long run's distributions cannot exhaust the shared allowance before the archive
manifest is written. When the allowance runs out, the sidecar ends at the last
complete interval and the manifest marks it truncated; finalization continues.

This bounds metadata output on disk. It does not bound JSON serialization memory,
streamed PDF/CSV/DOCX exports, aggregate concurrent writers or retained backlog.
It also does not replace a filesystem or Kubernetes ephemeral-storage quota.


## Generator retained-backlog admission

Each generator archive namespace now admits incremental growth against
`APEXLOAD_GENERATOR_ARCHIVE_BACKLOG_MAX_BYTES` (default 512 GiB, positive integer
bytes up to 1 PiB). A recorder freezes its setting at creation; invalid configuration
rejects writer creation. This counts all regular-file payload in that generator's
namespace, including completed archives, crash leftovers and unknown retained files.
It is separate from the existing per-run byte, free-space, TTL and archive-count
limits. Multiple generator namespaces or unrelated scratch users on one volume
still need a combined storage allocation below that volume's limit.

Writers claim at most 4 MiB at a time under a cross-process file lock, reducing
the claim when a smaller quota has less room. The claim is durable before writing.
Its entire amount stays charged until the writer refills or closes, conservatively
double-counting up to one chunk of already-written data per active recorder.
Inventory is performed at refill, not on every record, and refuses symbolic links,
special files, more than 100,000 entries or more than 16 directory levels. Unknown
or malformed reservation metadata also fails admission. A lower configuration
applies to new recorders; existing writers retain their frozen limit and reservations.
Quiesce older writers when enforcing a lower namespace limit immediately. Every
writer sharing this namespace must support this admission protocol; an older binary
writing to the same root is not covered during a mixed-version upgrade.

`.apexload-backlog` is reserved bookkeeping, protected from run-ID reuse and ordinary
retention sweeps. Inventory recognizes its inode even on case-insensitive filesystems.
Normal completion or joined discard removes only the writer's unchanged reservation.
A crash or uncertain cleanup leaves the reservation charged; age does not establish
that its owner stopped. Preserve lock inodes, and quiesce every process using the
namespace before manually reconciling crash reservations and retained source evidence.

Quota pressure does not delete other completed or unknown archives. The existing
failed-recorder path still fails the active execution and discards its incomplete
archive; ordinary generator TTL/count retention remains unchanged. Failure to retire
a reservation after successful finalization preserves the completed source and logs
the retained charge. Payload accounting excludes small bookkeeping files, directory
entries and filesystem allocation overhead. Keep headroom for those costs and set
the quota below the actual volume or `emptyDir` allocation. Controller source and
publication backlog admission is described below.

## Controller retained-backlog admission

`APEXLOAD_ARCHIVE_BACKLOG_MAX_BYTES` (default 256 GiB, positive integer bytes up to
1 PiB) bounds aggregate workload payload in each controller archive namespace.
It counts retained raw files, derived Parquet/CSV, distributed merge output,
private publication copies and finalization metadata, including incomplete and
unknown retained payload. Capture, merge, conversion, publication and adjunct
writers freeze this setting at creation and retain their separate per-attempt
and free-space checks. Raw capture and its conversion share one incremental
reservation, as do distributed merge and conversion; small quotas do not require
reserving each stage's maximum in advance. Initial manifests and source-retention confirmation receipts are charged too.

The controller uses the same durable reservation protocol described above for
generators. Joined completion releases only that operation's reservation. Capacity
rejection preserves existing source files and leaves publication recovery retryable;
local and distributed terminal execution outcomes remain unchanged. Receipt replacement
is atomic: rejection preserves any prior confirmation and leaves unconfirmed sources
ineligible for retention cleanup. A partial raw
file is recoverable evidence, not a completed archive. Uncertain reservation cleanup
remains charged and is logged. No source is removed to make room.

The scope is one filesystem archive root or one object-backend staging namespace.
Different backend namespaces are independent, including older namespaces still
needed for finalization after a configuration change. `_collection` transfer caches,
`_object_staging` namespaces nested beneath a filesystem root, and `library-imports`
are excluded from that root's workload accounting. Output writers cannot write into
those exclusions using a workload reservation. Reader caches, transfer scratch,
streamed report downloads, serialization memory, filesystem allocation overhead and
unrelated volume users remain separate costs. Configure these independent limits
below the combined volume allocation, with headroom; the default is not a claim
that a smaller Docker `emptyDir` can hold 256 GiB.

All writers sharing a namespace must run the reservation-aware version. Quiesce
older binaries before enabling the bound or lowering it for an immediate limit.
Preserve `.apexload-backlog` and its lock inode. Reconcile crash reservations only
with all writers stopped and their retained source evidence accounted for.
