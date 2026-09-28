# Confirmed object-archive source retention

`APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL` opts controllers into retiring local copies
of successfully published object archives. It accepts durations from `1m` through
`720h`; unset, zero, invalid and out-of-range values disable retirement. Enable it
only after every controller that can write the same staging volume runs a version
with the publication-source file fence. An older writer does not participate in
that fence. It is disabled by default and has not yet been qualified in a deployed
fault trial.

Collection and publication hold a per-run cross-process file lock. The publisher
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
periodic sweeps. Concurrent active readers remain protected and can exceed the
idle byte budget, so deployment sizing must allow their combined working set.
