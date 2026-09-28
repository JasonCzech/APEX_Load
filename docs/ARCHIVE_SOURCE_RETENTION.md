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
No unconfirmed cache evidence is automatically pruned. Cache cleanup after confirmed
publication and the bounded background collection retry worker remain follow-up work.
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
