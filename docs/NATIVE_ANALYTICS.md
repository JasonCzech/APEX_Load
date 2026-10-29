# Native analytics: imports, identities and panels

Imported Parquet, measurement identities, bounded queries, saved panels and resource limits. Start with [project and native-build enablement](#enable-the-workflows). [RCA](ROOT_CAUSE_ANALYSIS.md), [baseline comparison](ROOT_CAUSE_ANALYSIS.md#baseline-comparison) and [monitoring and preparation](MONITORING_AND_PREPARATION.md) retain their distinct contracts. Qualification is tracked in the analytics acceptance matrix (`docs/qa/ANALYTICS_ACCEPTANCE.md`).

## Contents

- [Enable the workflows](#enable-the-workflows)
- [Import and compare Parquet](#import-and-compare-parquet)
- [Decimal precision review](#decimal-precision-review)
- [Imported measurement identities](#imported-measurement-identities)
- [Resource and acceptance limits](#resource-and-acceptance-limits)
- [Business keys](#business-keys)
- [Response properties and native panels](#response-properties-and-native-panels)
- [Filtered archive panels and comparisons](#filtered-archive-panels-and-comparisons)
- [Live and grouped evidence panels](#live-and-grouped-evidence-panels)
- [Export displayed charts](#export-displayed-charts)
- [Shared metric and operation catalog](#shared-metric-and-operation-catalog)

## Enable the workflows

These workflows are explicitly enabled per project; existing runs, clients and archives remain readable without them. Set comma-separated project IDs on every controller replica:

```text
APEXLOAD_RUN_MONITOR_PROJECTS=project-id
APEXLOAD_PREPARATION_PROJECTS=project-id
APEXLOAD_ANALYSIS_PROJECTS=project-id
```

Native Parquet operations additionally require `CGO_ENABLED=1` and the `analytics_duckdb` build tag (`make controller-analytics`). The pinned driver is `github.com/duckdb/duckdb-go/v2`; published controller images use `Dockerfile.controller` (`standard` and `enterprise` profiles), and desktop packaging includes the tag. The ordinary Go build and `Dockerfile` preserve the CGO-free profile, whose `GET /api/v1/analysis/capabilities` response explicitly reports that native Parquet is unavailable. Generator and collector builds do not import DuckDB. Native build and test lanes cover Linux amd64/arm64, macOS arm64 and Windows amd64; a CI lane definition alone is not evidence that a target release has passed qualification.

DuckDB databases are process-local, disposable caches. SQLite or PostgreSQL owns metadata, revisions and publication state, and the configured archive backend owns original and normalized artifacts. HA replicas never share a writable DuckDB database. Queries use approved operations with extension installation and loading disabled and file access limited to their protected staging directory. When multiple controller replicas share publication or archive source staging, configure an absolute pod-private reader cache directory via `APEXLOAD_ARCHIVE_READER_CACHE_DIR` (canonicalized through symlinks and verified non-overlapping with the filesystem archive root and `APEXLOAD_ARCHIVE_STAGING_DIR`) so process-local reader leases and eviction remain isolated per replica. The generated [API specification](swagger.yaml) is the authoritative route and request contract.

## Import and compare Parquet

Open **Monitoring → Analysis**. Editors upload files, inspect a bounded schema
and redacted preview, map timestamps and measurements, then validate and import.
Directory selection preserves relative partition paths as source provenance. Hive-style `name=value` directories do not supply or override measurement columns, timestamps, metric names or identities: native preview and normalization disable implicit Hive partition inference. A measure selects either an explicit constant metric name or a physical name column; selecting both is rejected. Mapping versions 1 and 2 retain this contract. The partition-precedence review (`docs/qa/reviews/roadmap-partition-precedence-2026-10-20/README.md`) preserves native fixture and unchanged-source evidence. Long metric-name/value and wide measurement mappings are supported, including explicit nested scalar paths. Uploads use resumable 4 MiB chunks with content verification; resuming rechecks
acknowledged chunks against the selected original. Multi-file imports preserve
validated relative paths and byte-for-byte original files. Metadata publication
is atomic after all normalized artifacts are durable. Cancel invalidates the
worker's revision; an expired worker can be retried idempotently with fresh output
ownership. Dataset cancellation immediately hides the dataset and queues
originals, upload chunks and normalized objects for cleanup. Physical deletion
requires durable proof that every writer has stopped. New uploads record an
immutable writer intent before opening storage; accepted chunk metadata and the
physical completion seal commit together. Cancellation, timeout, panic, Goexit
or an uncertain Close retain ownership and cannot advance the cursor. Exact
retry state can be reconciled across a same-process Server replacement without
redirecting its SQL or artifact namespace. After all writers are proven closed,
delete joins the complete owned dataset namespace, including accepted opaque
chunk keys and closed abandoned object/temporary siblings, before returning
success. Uploads from the legacy writer path lack this proof: DELETE returns
pending status 503, retains their bytes and keeps the cleanup job. See the
upload and deletion review (`docs/qa/reviews/feature-completion-native-upload-2026-10-04/README.md`).

**Controller restart.** Each normalization attempt and upload
chunk writer records the controller process that writes its artifacts before
any I/O. When that process dies, a controller that can judge it proves the
exit: one in the same PID namespace on the same machine and boot, or one on
the same identified machine after a reboot. Each replica's sweep covers about
400 outstanding generations a minute, so the work is retired within about a
minute per 400 outstanding generations, or at once when the upload is retried
at the blocked offset. An interrupted normalization becomes `failed` ("the
controller running it exited"), and committing again starts a fresh attempt;
an interrupted upload chunk is retried by the client. With a filesystem backend
whose artifact directory (`<archive root>/_analysis`, judged through its links
and mount points) is on a local filesystem, the exited writer's own objects are
then cleaned up and DELETE completes. The local filesystems are an allowlist:

- Linux: ext2/3/4, XFS, Btrfs, F2FS, bcachefs, ZFS, tmpfs, ramfs and overlayfs;
- macOS: APFS or HFS on a mount flagged local;
- Windows: a fixed drive formatted NTFS or ReFS (not a UNC path, and not
  reached through a link or junction to a share).

Everything else gets the remote treatment: S3, Azure Blob and GCS, a network
or user-space filesystem (NFS, SMB/CIFS, EFS, Azure Files, Ceph, GlusterFS,
9p, virtiofs, FUSE such as rclone or blobfuse, WinFsp on Windows), and any
unlisted local filesystem (vfat, exFAT, ntfs3, MS-DOS, removable or RAM
drives). A write the dead process had already issued may still land there, so
its objects are retained and the dataset stays deletion-pending. Only the
`_analysis` directory itself is judged: do not mount another filesystem below
it. A probe that fails judges nothing that sweep and is retried.

**Physical volume identity (`_analysis/source.v1`).** On a fresh filesystem
backend, minting a writer intent atomically materializes `<archive root>/_analysis`
(mode `0700`) and `<archive root>/_analysis/source.v1` (mode `0600`, holding a
canonical UUID) and binds that volume UUID in `app_settings` before I/O begins.
Because `source.v1` is written at writer-mint time, a same-host successor can
immediately judge an upload or normalization writer that crashed before its first
chunk or output write. The exit-scope sweep, `DELETE`, cleanup and backend checks
verify `_analysis/source.v1` without creating `_analysis`: once a volume UUID is
bound in `app_settings`, an unmounted share or replaced disk whose `source.v1`
is missing or carries a different UUID fails closed (`503`) instead of granting
exit scope or reporting bytes on the unmounted share as deleted.

**Start the controller only after the archive volume is mounted.** Before any
volume UUID has been bound on a brand-new installation, starting the controller
against an unmounted NFS, SMB or EFS mount point would mint `source.v1` on the
local mount-point directory rather than on the share. Mount the archive volume
before starting the controller so the initial `source.v1` binding lands on the
intended filesystem.

Automatic process-exit recovery reaches a successor in the dead controller's own
PID namespace (a process restarted in place under systemd, launchd, a service
manager or the desktop app) or on the same identified machine after a reboot.
The Helm chart (no `shareProcessNamespace`) and docker compose restart the
controller in a new container (a new PID namespace), and HA takeover on another
host or macOS after a reboot (macOS records no machine identity) cannot prove
exit automatically: that work stays owned until attested as stopped. A replaced
Kubernetes pod on another node could judge its predecessor only if its host name
and machine ID both matched, which the machine identity requirement below forbids
for distinct hosts.

**Machine identity requirement.** Every host that shares the SQL store must
have its own machine identity: on Linux a distinct `/etc/machine-id` (only
recorded together with the host name, so a host without one records no
machine identity at all), on Windows a distinct `MachineGuid` and host name.
Otherwise one host can take another's live writer for its own exited process:
- containers built with a baked `/etc/machine-id` and given a fixed host name
  (compose `hostname:`, a pod `spec.hostname`, a StatefulSet) on two nodes read
  as an earlier boot of the same machine, so each judges the other's writers
  exited;
- VMs cloned from one memory snapshot share the boot and PID namespace: with
  the same host name, or without a machine-id, they share a machine identity;
- macOS clones (from one memory snapshot) share their boot session and record
  no machine identity.

A pending DELETE answers 503 with code `analysis_dataset_deletion_pending`
and counts: `unproven_writers` (recorded where this host can judge them, so
it may settle them: it does unless their PID has since been reused by a
process this host cannot inspect, such as another user's under `hidepid`),
`unjudgeable_writers` (recorded where this host cannot judge them: another
machine, or another boot or PID namespace of this one), `unrecorded_writers` (journalled before writer incarnations,
by a controller with no process identity, or rewritten by an older
controller), `remote_commits_unproven` and `legacy_writers`. `retryable` is
true while some writer may still resolve; `Retry-After` is set only while this
host may settle one. Repeating DELETE re-reads this status without
re-queuing cleanup.

**Audited writer-stop attestation.** For legacy writers, unproven remote commits,
container restarts across PID namespaces or writers whose host has been retired,
a project or system administrator who has independently verified that the writer
process has stopped can release ownership on a cancelled, failed or deleting
import via `POST /api/v1/analysis/imports/{id}/attest-writer-stopped` with
`{"reason": "..."}` (3–1,024 characters). The controller refuses with `409` if
a writer on the handling controller is still active or if the import is not yet
cancelled, failed or deleting, and records an audited
`analysis.import.attest-writer-stopped` event before allowing physical cleanup
and `DELETE` to complete. See the writer ownership review
(`docs/qa/reviews/roadmap-native-writer-ownership-2026-10-25/README.md`).

Rejected measurements fail publication by default; the import's error then names the file, the measure, how many rows were rejected and the first zero-based source row ordinals. Explicitly accepting them retains a complete rejected-row Parquet artifact with file/measure identity and zero-based source row ordinals, plus bounded metadata summaries. Rejected source values are never copied into error reports. The dashboard caps artifact downloads at 64 MiB; larger artifacts remain accessible through the authenticated API.

Every source file must hold each mapped column as a scalar column. A file that lacks one, holds it as a list or repeated field, or holds a group of fields there fails the whole import before any row is read; nothing is accepted, and the import's error names the file and the column, for example `File "b.parquet" does not match the mapping: it has no column "latency".` A mapped column whose values in some file cannot be read as numbers is a rejected measurement with row provenance, as above. Other normalization failures keep the generic message. Two files in the same import with identical content are refused at commit (`422`, naming both files): commit resolves whole-file SHA-256 digests across single-chunk and assembled multi-chunk uploads (or compares file size and ordered `(offset, size, sha256)` chunk provenance), and normalization repeats verified source-hash duplicate detection so identical content uploaded with different chunk boundaries still fails closed.

Mappings distinguish gauges, counters, raw transaction observations and summary
values. Timestamp mappings require an explicit timezone and encoding. Offset
text requires `timestamp_offset`; local wall timestamps use `timestamp`.
The absolute-time mapping accepts a timezone-aware Parquet timestamp or text
with an explicit `Z`/numeric offset (`±HH`, `±HHMM` or `±HH:MM`) or a spaced
`UTC` suffix after its clock component. Clock fields may have any number of
digits; offset hours use one or two digits. Seconds may end in a bare fraction
point, and any whitespace may separate date and clock or follow the offset,
as the parser accepts. Offset-free text, DATE and naive
TIMESTAMP columns are rejected rather than assigned the worker's timezone. Use
a local-wall mapping with the source timezone for those values. Local-wall
mappings refuse offset text, including short offsets, one-digit clock fields
and a `UTC` suffix, rather than discard its offset. Conversion uses
each row's source instant; one file may cross a daylight-saving transition.
This tightening applies to new normalization; retained canonical artifacts
and historical query results are not rewritten. Local-wall inputs must resolve
to exactly one instant: repeated or skipped clock times (including non-hour
changes and skipped dates) are rejected with source-row provenance. Supply an
explicit offset with `timestamp_offset` for a repeated clock time. A bounded
timezone resolution that cannot complete, or disagrees with the native
engine's timezone conversion, also rejects the row. Accepted-rejection mode
retains the existing rejected-row artifacts; default refusal publishes no
canonical output. This is normalization behavior, not platform or scale
qualification.
Units are normalized before comparison. Saved mappings retain immutable
revisions, and imports record the selected revision when one is used.
Mapping version 2 preserves source, host, service and exact bounded dimensions.
The workspace exposes separate identity filters for ordinary series and both
comparison sides; saved definitions retain the executed selectors. See
[imported measurement identities](#imported-measurement-identities) for nested scalar paths,
legacy mapping behavior, quality admission and saved-bound precision.

Percentiles come from raw observations, from a summary that declares the
percentile it reports, or interpolated within a histogram's bins. Summary
percentiles are never averaged or treated as transaction samples. Counters require explicit rate conversion;
resets, duplicate instants and missing samples interrupt the rate chain. A null
scalar measurement is a missing sample: its whole bucket, count included, reads as
missing rather than as fewer samples or zero. A histogram whose bin counts are all
null is missing; one with only some null is rejected. Queries
are bounded to 10,000 buckets and 366 days. Unsupported semantics produce an
explicit error instead of a misleading chart.

A summary measure declares the one statistic its column reports (`avg`, `min`,
`max`, `sum`, `count`, `p50`, `p75`, `p90`, `p95`, `p99` or `p999`), its
`window_seconds` (1–86,400) and optionally the columns holding each window's
sample count and sum; new mappings must declare them, and a stored summary
without a declared statistic is refused at query time with a request to
re-import it. A series answers only its declared statistic: counts and sums
add across windows, minimum and maximum combine, an average combines only with
sample counts, and a precomputed percentile never spans several windows
(`precomputed_percentile_not_mergeable`). A histogram measure declares its
ordered upper `bounds` (at most 128; the last may be `+Inf`), whether its
`bucket_counts` are `per_bin` or `le_cumulative`, its layout (wide rows with a
count column per bound but the last, whose count is the measure column, or
long rows whose `bound_column` labels each row with its bound; either layout
takes either kind of count), and its `temporality`: `delta` per window
(with `window_seconds`) or `cumulative` counters, differenced between scrapes
with resets reported as `histogram_counter_reset`. Histogram buckets answer
`count` and percentiles interpolated within the bin holding the rank
(`histogram_interpolated.v1`, never an exact quantile); the point's
`estimate_bin` is that bin, and in the first bin, whose lower edge the
histogram does not declare, `lower_assumed` marks the lower edge as assumed.
A rank in an unbounded last bin is `histogram_percentile_in_overflow_bin`, and
a bucket without observations `histogram_no_observations`. Measures whose
metric names come from a name column are queried by name like any other; the
workspace offers their declared statistics and widens buckets to their window
when every such measure declaring the statistic shares one.

A declared summary measure names the statistic and window the source computed;
the editor never guesses either. A bucket spanning several source windows is
missing for a percentile, and for an average without sample counts, whatever
number of rows it happens to hold. The workspace and comparison panel widen
buckets to whole source windows (in a comparison, to a common multiple of both
sides' windows) and start them on the metric's own window grid, read from the
first window of each imported partition (one per file and measure, narrowed to
the selected series when identity filters select one); windows anchored at a
test start keep their phase. When partitions disagree, or a partition mixing
several series cannot tell the selected one's grid, the range is left as
entered. Only each partition's first window is read, so a phase change inside
one file, such as daily windows at a local midnight across a daylight-saving
change, is not detected: buckets follow the file's first phase, and windows
after the change straddle bucket boundaries, so summary rows can move to the
neighbouring bucket and delta-histogram buckets come back as
`partial_histogram_window`; split such files at the change. The workspace also takes an explicit bucket size. A cumulative histogram is differenced per series across
every file of an import; an instant several files report is one sample when they
agree and `duplicate_histogram_window` when they disagree. Its windows run between
consecutive scrapes, so a bucket is missing as `partial_histogram_window` when a
window ends past it or a window of an earlier bucket (or from before `from`) ends
inside it: choose a `from` at a scrape time and a bucket that is a whole multiple
of the scrape interval. A query staged from imported partitions also reads an
earlier histogram partition whose last window reaches past `from`, so a window
crossing into the range is seen.
Histogram bounds that differ within a selection are refused, and a FLOAT bound
column is matched at its own precision. Summary rows rejected only for their
sample count or sum are recorded as `invalid_summary_sample`.

Summary and histogram declarations are stored as fields of mapping versions 1,
2 and 3. Changing an associated run must retain those declarations and imported
identity fields. The dashboard requires the exact controller capability
`mapping_association_version: 1` for these mappings and sends contract version 1
with the expected dataset revision to the negotiated PATCH association route.
It verifies the returned contract, dataset/project/run/revision and unchanged
mapping before adopting the response. Missing capabilities, unsupported versions
and failed confirmation leave the local selection available; reload the dataset
after an ambiguous response before retrying. It never falls back to legacy PUT
when PATCH is rejected. Unavailable mapping metadata also blocks reassociation
until the dataset can be reloaded. Plain legacy mappings retain their existing PUT contract.

The minimum controller for this workflow is one that advertises association
contract version 1 and implements that PATCH route; a generic native-Parquet or
imported-identity capability is insufficient. Complete the upgrade across every
controller serving the project before enabling reassociation of identity,
summary or histogram datasets, and require compatible clients. An older route
table rejects PATCH before rewriting metadata, so a request routed to an old peer
during rollout fails safely. The capability read alone does not prove that every
peer has upgraded. An old client can still call legacy PUT against an older
controller and silently discard these fields; the new protocol cannot repair
that path or previously damaged mappings. Keep those writers disabled until the
rolling-upgrade barrier is complete, and re-import any affected dataset. Unknown
contract or mapping versions are refused before mutation on capable controllers.
See the reassociation review (`docs/qa/reviews/roadmap-mapping-association-2026-10-20/README.md`)
for exact local evidence and remaining qualification limits.

### Analysis policies and run pins

`/api/v1/analysis/policies?project_id=…` stores revisioned policies of five
kinds: `telemetry_policy`, `telemetry_acceptance_policy`, `browser_policy`,
`journey_policy` and `derived_operation`. Viewers list (by `kind`) and read
them; editors create, update with the expected `revision`, and delete. At most
one policy of each kind may set `apply_by_default`. A browser policy's
`pinned_run` baseline must be a run of the same project. When a run starts, or a queued run is promoted, each kind's project default—overridden by any explicit `analysis_policy_selections` (`browser_policy` and `journey_policy` revision pins configured on the scenario, scenario schedule under schema release 85 `scenario-schedule-analysis-policy-selections`, or run request) and any chosen `telemetry_acceptance_policy_id` or `telemetry_budget_policy_id` (`source: "run_selection"` vs. `"project_default"`)—is pinned into the run's admission record and appears as `policy_pins` in its provenance and archive manifest; a later edit never changes a pinned run. Pins are recorded with `applied: false`, a field kept for compatibility that says nothing about enforcement (the pin is written at admission, before any consumer judges the run). The consumers that judge a run against its pins say so in their own results: telemetry acceptance verdicts (`enforced` under `fail_ci`), browser findings, journey accounting and RCA's browser baseline (`browser_baseline.policy`) each name the pinned revision they used. A project whose defaults cannot be read unambiguously, or a chosen policy revision that no longer exists, refuses Start (and a queued run's promotion) rather than falling back silently or running without its pins. System-wide telemetry defaults remain separate from project `policy_pins`.

A `telemetry_policy` revision can also be named explicitly in a
`telemetry_acceptance` evaluation (`POST /api/v1/analysis/evaluations`), which
re-classifies up to ten runs' retained interval lag evidence under that one
revision as a derived, hashed result without changing the runs; see the
[telemetry guide](guides/TELEMETRY_POLICY_RECEIPTS.md#comparing-runs-and-re-evaluating-under-a-common-policy).

An external dataset keeps its own identity. Associate it explicitly with a run
in the same project before comparing it with managed evidence. Comparisons
select datasets, bound monitor evidence, or archived transaction durations and
align UTC buckets. Lagged correlation reports paired bucket count, coverage and
an optional coefficient; constant or insufficient evidence stays unscored and
says which (`constant_series`, `insufficient_pairs`). A positive lag pairs each
left bucket with the right series that much later. `correlation_semantics`
keeps each side's own unit, kind and source kind, population, statistic and
scalar conversion (a counter's per-second rate, a summary's declared
statistic, a percentile of raw observations, a percentile estimated from
histogram buckets) and any derivation, with its valid buckets, the buckets
kept out, and the raw samples bucketing could not use, by reason (stale,
clock skew, dropped, and a raw counter's resets and duplicate instants; a side
read from an imported dataset is aggregated by its dataset query and reports
buckets only). Two sides share a unit only when unit, kind and source kind
match and, where both declare one, the population; a side with
no evidence or no declared unit leaves that unknown (`units_undeclared`).
Series in different units are associated, never compared value for value:
the comparison view gives each its own labelled axis, and series in one unit
share one.
Correlation is association, never proof of causation. Saved comparison
definitions are revisioned and recheck ownership when used.

New normalization attempts retain a versioned producer manifest in the existing
import metadata and sealed publication receipt. Each normalized object has its
verified hash, size, row/quality counts, UTC time range and any proven constant
metric/full identity. Queries select objects from this immutable proof before
artifact access. Mixed or unknown categories, and nonempty all-missing files,
remain eligible for scanning; historical datasets without manifests use full
scans. Present invalid manifests fail explicitly. File names and independently
varying marginal dimensions do not establish a joint identity.

Successful native query responses may include `artifact_selections`: exact
selected/pruned object counts and staged/avoided artifact bytes, tied to the
dataset revision and manifest digest. These are verified artifact geometry,
not DuckDB physical scan bytes, memory consumption or elapsed performance.
Selection never grants later read authority: scope, association, revision and
deadline are checked again before response publication.

Canonical Parquet timestamps use microseconds. Dataset queries, comparisons
and overlays apply the same UTC microsecond window to both analytical sides
and report requested and effective bounds in optional `time_window` metadata.
Retained source timestamps and run start times remain unchanged. A window that
collapses at this precision is refused at the API. Whole-publication rate
coverage rules still apply: rounding a boundary cannot prorate a partial
publication into a complete observed interval.

Low-level CLI workflows support descriptors and JSON mappings without loading a
whole Parquet file into memory:

```sh
apexctl analysis create --project PROJECT_ID --file import-descriptor.json
apexctl analysis upload --id IMPORT_ID --file-id FILE_ID --file data.parquet
apexctl analysis preview --id IMPORT_ID --file-id FILE_ID
apexctl analysis map --id IMPORT_ID --file mapping-request.json
apexctl analysis commit --id IMPORT_ID
apexctl analysis show --id IMPORT_ID
apexctl analysis query --id DATASET_ID --file query.json
apexctl analysis associate --id DATASET_ID --file association.json
apexctl analysis compare --project PROJECT_ID --file comparison.json
apexctl analysis save-mapping --project PROJECT_ID --file saved-mapping.json
apexctl analysis comparisons --project PROJECT_ID
```

## Decimal precision review

On controllers advertising `precision_consent_version: 1`, use **Review precision**
after selecting the mapping. The review inspects every immutable uploaded file and
names mapped DECIMAL columns that may round when converted to float64. Fractional
DECIMAL columns and integer DECIMAL columns with more than 15 declared digits are
conservatively flagged. This is a schema-level potential-loss disclosure, not a
count of changed rows. It covers measurement values, summary counts/sums and
histogram count/bound columns. Original source bytes remain unchanged.

The default is **Reject potential rounding**. To proceed with flagged columns,
choose **Allow rounding after review**, review again, and explicitly acknowledge
the listed source files and mapping. A pending review becomes stale when its
mapping, import revision or source changes. Accepted consent remains bound to
that reviewed snapshot through normalization and later reassociation. Saved
mappings retain the policy; they do not carry source-specific acknowledgement
into another import.

Mapping format 3 requires an explicit `precision_policy` of
`reject_potential_loss` or `allow_float64_rounding`; it still uses imported identity
schema 2. Formats 1/2 remain readable and can normalize risk-free inputs. New
normalization of a flagged DECIMAL requires format 3, the allow policy, and an
exact acknowledgement. Already normalized historical datasets are not rewritten.
The worker independently re-derives the review from verified source hashes before
creating normalized output. Duplicate source content, unsupported/conflicting
metadata, absent columns and exceeded review limits fail the whole review.
The API and worker inspect one source schema at a time, retaining only bounded
risk records and source hashes. An incomplete or failed source cohort cannot
publish a partial review.

The API review is read-only, project-scoped and revision-bound. Its bounded receipt
contains the canonical mapping digest, complete source-set digest, per-column
risks and a review SHA-256. The acknowledgement additionally binds project,
import and revision; it is an integrity binding, not an authorization token.
The mapping write accepts `precision_review_sha256` and
`precision_acknowledgement_sha256` together. Durable CAS, attempt input binding,
publication and reassociation preserve them. A review covers at most 1,024 source
files, 1,024 file/measure combinations, 64 affected file/column pairs and 64 KiB of review
JSON. No partial prefix is accepted when a limit is exceeded.

For CLI workflows, send a JSON object containing `revision` and `mapping` with
`apexctl analysis precision-review --id <import> --file <review.json>`. Inspect its
receipt, then provide the same mapping/revision and both receipt digests with
`apexctl analysis map`; commit with `apexctl analysis commit`. The CLI and dashboard
require explicit controller support before sending format-3 mutations. Keep the
[rolling-upgrade barrier](#import-and-compare-parquet) in force; older
writers cannot preserve fields they do not understand.

## Imported measurement identities

Native analytics remains capability gated. These workflows require an enabled
project and a qualified `analytics_duckdb` controller or desktop release. A
CGO-free release reports native Parquet analysis as unavailable. The source and
client changes alone do not qualify a native platform or satisfy the required
reference-load acceptance.

In the Analysis workspace, inspect the Parquet schema, select a timestamp and
measurement columns, then use **Source and labels** to map optional source,
host, service and dimension columns. Identity controls admit scalar columns;
repeated fields and protected credential column paths are excluded. The same
credential classification used by protected previews applies to each selected
path component and its joined path. Existing datasets with such identity
mappings cannot be queried or compared; originals remain unchanged and protected.
Ordinary identity values and dimension label names are not rewritten or merged.
Each path consists of at most 16 schema components.
Source, host and service values are bounded to 256 UTF-8 bytes, and dimension
names and values to 128 bytes. At most 16 dimensions are supported. Names are
exact and unique; slash, tilde, quotes and names such as `__proto__` are labels.

The dashboard enables version-2 mapping controls only when the connected
controller advertises `imported_identity_version >= 2`. Existing version-2
mappings and selectors remain unchanged on an older or unadvertised controller;
commit, query and comparison are explicitly unavailable. Version-1 imports and
queries remain usable. The client never retries an identity request with fields
removed or a lower mapping version.

Changing an identity control selects mapping version 2, or preserves version 3 and its precision policy when already selected. Project and dataset
ownership come from the authorized import session. Row values cannot supply
project, dataset or managed-run ownership. Without a mapped source column, the
source defaults to the dataset identity. An omitted host or service has an empty
canonical value. Required source and metric names cannot be blank Unicode text;
NUL and malformed Unicode are rejected.

After import, use **Dataset identity filters** to select a source, host, service
and label combination. Filter text is matched exactly, including surrounding
spaces. A dimension row with an empty value selects an explicit empty label; an
omitted row makes no selection for that name. A metric with several distinct
series requires a narrower selection rather than averaging across identities.
Counters and rates are separated by the same identities. Version 1 historical
datasets remain queryable, but requested labels they cannot represent are
rejected explicitly.
A v2 query requires v2 canonical artifacts; the historical adapter cannot silently
downgrade the pinned mapping version. Empty source, host and service filter text
currently omits that filter. An empty host or service cannot yet be selected as
an explicit filter; presence-aware selectors remain part of the next analytics
slice. Empty dimension values are supported as described above.

Comparison provides separate filters for both imported sides and retains them
when saving and reopening the executed definition. Later edits to controls do
not alter the definition of the displayed result or the saved result. Run
comparison still requires an authorized explicit dataset association. Correlation
reports association and coverage; missing measurements never become zero.
Saved comparison bounds use UTC and retain millisecond precision in the dashboard.
A saved bound with a nonzero fraction finer than a millisecond is refused before
changing either selector or time control; use the authenticated API or CLI for
that bound. This client limit does not change the immutable source artifact.
Canonical stale, skewed, dropped, unknown or inconsistent quality/value rows
cause a qualified query error rather than being silently discarded into a
healthy aggregate.

The existing `apexctl analysis map`, `query`, `compare`, `save-mapping` and
`save-comparison` commands accept a JSON request through `--file`. Identity paths
live in `mapping.identity` (or `definition.identity` for a saved mapping).
The CLI checks authorized dataset metadata and the controller identity capability
before mapping, commit, query or comparison requests that require version 2.
It refuses an older or unadvertised controller without rewriting the request.
Version-1 operations and existing managed-run monitor selectors remain available.

Query filters use `source_id`, `host`, `service` and a `dimensions` object;
comparison carries those fields independently in `left` and `right`. Controller
authorization, mapping-version, cardinality and resource limits still apply.

New saved-definition writes reject unsupported fields, aliases, duplicate keys
and trailing JSON. Historical definitions remain readable; remove unsupported
options explicitly before writing a new revision. Consult the generated API
contract for the complete request shapes.

## Resource and acceptance limits

One heavy analytical pipeline runs per controller process, including staging,
query execution, publication and physical cleanup. Native operations execute in
a supervised child of the same executable through bounded typed messages;
worker startup precedes controller or desktop initialization. Only approved
operations and private staged files cross that boundary. DuckDB defaults to two
threads, a 2 GiB engine setting and an 8 GiB spill quota. Smaller profiles use
`APEXLOAD_ANALYSIS_MEMORY_LIMIT` and `APEXLOAD_ANALYSIS_SPILL_LIMIT`, with integer
`B`, `KiB`, `MiB` or `GiB` units and the same maximum settings. Invalid profiles
are reported as unavailable configuration before analytical filesystem work.

Admission includes API staging, worker input/output, executable copy and spill
reserves. Controller and worker RSS, container usage and disk headroom remain
separate safeguards; the engine setting is not a total native-memory bound.
Cancellation deadlines do not release an unresolved process, descriptor or
publication owner. The watchdog retains uncertainty until actual closure and
SQL acknowledgement; platform hard containment and the native worker's
restart qualification (its heavy-work reservation and private staging after a
controller crash) remain open as separate gates. Concurrent requests receive explicit busy, resource,
cancellation or size-limit errors. SQLite/PostgreSQL schema 33 records exact
attempt ownership, sealed publication and cleanup. Process-local retry records
bind the immutable SQL and artifact namespaces and retain ambiguous outcomes;
they are not proof of cross-process or cross-host writer termination. A
controller's recorded process identity and a same-host exit judgement are
(see Controller restart above); a lease expiry never is.

Analytical storage is bound durably to its first configured archive namespace. All replicas must use that namespace. A changed filesystem root, bucket, prefix, or cloud identity stops analytical artifact access and cleanup with an explicit error. Restore the configuration, or migrate all analytical artifacts and the `analysis_storage_identity_v1` setting together offline with writers stopped; changing only the setting is unsafe. Dataset metadata and cancellation remain available when the native build capability or project flag is disabled. Physical deletion still requires the matching storage namespace.

## Business keys

A business key is a value a script extracts that also appears in the system
under test's logs: an order ID, a billing account number (BAN). A business
key lookup finds a run's requests and the matching log entries for values an
operator types in. Projects opt in under Settings › Project › Analysis, or
the deployment names them in `APEXLOAD_BUSINESS_KEY_PROJECTS`.

- **Approved mappings.** A project admin approves a mapping from an
  extraction name to a log field of one of the project's Elasticsearch or
  OpenSearch query sources, as an analysis definition of kind
  `business_key_mapping`: `{"extraction": "order_id", "source_id": "...",
  "field": "order.id", "label": "Order ID"}`. The field should be a keyword
  field: lookups use exact term queries. Routes:
  `/api/v1/analysis/business-key-mappings` (viewers read; project admins
  create, revise and delete), with the same revision rules as other
  definitions. A project holds at most 1,000.
- **Archives.** When a run's archive recording begins, the mapped extraction
  names (at most 64) are frozen into its provenance (`business_keys`). Each archived
  request keeps a digest of every mapped value in `extraction_digests`: an
  HMAC-SHA-256 under a key of the project's own, created on first use and
  stored wrapped by the controller's key wrapper (`business_key_project_keys`,
  schema release 41). Groups whose effective script sets
  `config.log.redact_run_evidence` keep only the digest; other groups also
  keep the value, as before. A group whose script cannot be read is treated as
  redacting. Remote generators never hold the key: the controller digests
  their rows when it merges their archives and discards any digest a bundle
  carries. Without a key wrapper the values of redacting groups are dropped
  without a digest, and the archive records
  `business_key_digests_unavailable`.
- **Lookups.** `POST /api/v1/tests/{id}/archive/business-keys/lookup`
  (editors) takes `{"mapping_id": "...", "values": [...]}`, 1 to 20 values of
  at most 256 bytes. It matches archived requests by digest, or by the value
  where it was kept, and searches the mapped source's logs over the run (with
  five minutes either side), one query per value, four at a time. Results are keyed
  by each value's position in the request: values are not echoed, stored,
  logged or audited (the audit event records the mapping and the count). Each
  result has up to 50 requests and 50 log entries with their totals, and the
  response states what could not be matched, such as runs archived before
  the mapping was approved.
- **Dashboard.** Run report Diagnostics shows a Business key lookup panel in
  projects that opt in. Values typed there stay in the page; they never go
  into the URL or browser storage. Project admins approve and remove
  mappings from the same panel.

## Response properties and native panels

In runtime settings, explicitly promote up to four non-sensitive extraction keys with 1–16 approved category values each. The equivalent script field is `config.results.property_analytics`:

```json
[{"name":"operation","extraction":"operation_type","values":["checkout","search"]}]
```

Promotion occurs after result redaction. Unlisted values become `other`; absent extractions become `missing`. Raw arbitrary values never become property labels. Names, values and extraction keys are bounded; credential and personal-identifier names are rejected. Generators aggregate action observations, retain mergeable distributions, and require the `property_analytics_v1` capability for distributed execution. Each observation can contribute to several properties; counts across properties must not be added into a run total.

Live summaries expose `properties`; archive reports recompute `by_property` from action observations. JSONL and Parquet retain `property_dimensions`. Existing archive transaction queries accept paired `property_name` and `property_value` exact filters; use the action population when reconciling with live property counts. Old archives without promotion evidence remain unavailable. Property comparisons across script revisions must use the same promotion definitions.

Run pages offer native project panel creation with line, latest-value and table views, using the retained run time window. Supported run measurements include TPS, response-time means/interval percentiles, errors, VUsers, process resources, protocol payload/body bytes, messaging counters, Java HTTP representation diagnostics and browser document cohorts. Project viewers can reopen saved panels. Missing intervals remain gaps, interval percentiles are never averaged, and unsupported stored query dimensions/filters/aggregations are explicitly unavailable. Archived action/transaction and property filtering, grouped dimension selections and run-monitor or imported-dataset overlays are described below.


### Saved panel revisions and pinned inputs

Every save of a project dashboard (panel) definition is kept as a revision.
That covers create, and each edit that wins the definition's revision check.
The newest 100 revisions are kept, and they are deleted with the definition
or its project.

- **Reopening a revision.** `GET
  /api/v1/monitoring/dashboard-definitions/{id}?revision=N` returns exactly what
  was saved as revision N, with `current_revision` (the definition's revision
  now; equal to N when N is current) beside it. Reading one definition lists
  its `retained_revisions`, newest first (the list of definitions does not),
  never one newer than the definition returned. `apexctl analysis panel
  --revision N` refuses an answer that is not revision N (a controller that
  predates saved revisions answers the current one). A revision that was never saved, or that
  was pruned, is `404`. Access is resolved from the stored definition's project,
  never from the request. On the run page, **Saved revision** opens a past
  revision read-only; a definition saved before revisions were kept has only its
  current revision.
- **Edits and deletes.** Both still need the revision they replace: a stale or
  racing edit or delete is `409`, and an edit of a deleted definition is `404`.
- **Query version.** Saved panels declare the `query_version` semantics they
  were saved under: `2` for standard run, grouped, byte and overlay panels, `3`
  for `browser_document` multi-tuple cohort panels, `4` for pinned browser
  comparisons (`BrowserPanelInputs` v1), and `5` for browser comparisons that
  also pin `browser_policy`/`journey_policy` revisions and the RCA
  `selection_receipt_sha256` (`BrowserPanelInputs` v2). A missing version is
  read under `query_version: 2`; a version newer than the controller's is
  refused when a panel is saved and when a query runs.
- **Pinned dataset artifacts.** A committed dataset reports `artifact_sha256`,
  a digest of its normalized artifacts, wherever it is read (one dataset or
  import, the import list, a commit of a ready import, an association). A mapping change or a new commit
  replaces it; associating the dataset with a run does not. A comparison
  selection or a dataset overlay may pin it as `dataset_artifact_sha256`. A
  pinned dataset whose artifacts changed since is refused with `409`, naming
  both digests, rather than answered from different evidence; each side's pin
  is checked, also when both sides read one dataset. `apexctl analysis
  compare` and `save-comparison` carry the pin. An unpinned selection reads
  the current artifacts.

`apexctl analysis` covers the complete native analytics, panel, policy, telemetry
and browser-intelligence CLI surface. Commands that print the controller's
response verbatim stream it directly to standard output (or `--out FILE`)
without loading it whole; commands that inspect or verify the response first
read within an explicit bound (`journeys`, `browser-findings`, `interactions`,
`har-entry`, `policies`, `browser-har list`, `browser-documents`,
`telemetry-policy`, `telemetry-policy-change`, `telemetry-reevaluate`,
`panel --revision`, `replay-panel` and `baseline --fail-on-regression` within
8 MiB; `browser-resources` one 2 MiB page at a time):

| Command | Does |
| --- | --- |
| `catalog --project P` / `capabilities --project P` | Reads project analysis capabilities and `metric_catalog` (`GET /api/v1/analysis/capabilities`). |
| `list --project P` / `show --id IMPORT` | Lists project imports/datasets or shows one import/dataset (`GET /api/v1/analysis/imports`). |
| `create --project P --file desc.json` | Creates a Parquet import session (`POST /api/v1/analysis/imports`). |
| `upload --id IMPORT --file-id FILE --file data.parquet [--chunk-bytes N]` | Uploads resumable verified chunks (`1024` to `4194304` bytes). |
| `preview --id IMPORT --file-id FILE` | Reads bounded schema and redacted preview (`GET /api/v1/analysis/imports/{id}/files/{fileId}/preview`). |
| `precision-review --id IMPORT --file review.json` | Reviews mapped DECIMAL rounding risk (`POST /api/v1/analysis/imports/{id}/precision-review`). |
| `map --id IMPORT --file mapping.json` | Saves mapping (and optional precision digests) (`PUT /api/v1/analysis/imports/{id}/mapping`). |
| `commit --id IMPORT` / `cancel --id IMPORT` | Starts normalization (`POST .../commit`) or cancels/deletes an import (`DELETE /api/v1/analysis/imports/{id}`). |
| `associate --id DATASET --file assoc.json` | Associates a dataset with a project run (`PUT /api/v1/analysis/datasets/{id}/association`). |
| `query --id DATASET --file query.json` | Queries bucketed dataset series (`POST /api/v1/analysis/datasets/{id}/series`). |
| `compare --project P --file cmp.json` | Runs a two-side lagged correlation/comparison (`POST /api/v1/analysis/compare`). |
| `mappings --project P` / `save-mapping --project P --file map.json` | Lists or saves reusable Parquet mapping definitions. |
| `comparisons --project P` / `save-comparison --project P --file cmp.json` | Lists or saves reusable dataset/monitor comparison definitions. |
| `panels --project P` / `panel --id ID [--revision N]` | Lists saved panel definitions or opens a definition (or exact saved revision `N`). |
| `save-panel --file def.json` / `update-panel --id ID --file patch.json` / `delete-panel --id ID --revision N` | Creates (`POST`), updates (`PATCH` with expected `revision`) or deletes (`DELETE`) a saved panel definition. |
| `replay-panel --id ID [--project P] [--revision N]` | Re-executes every panel in a saved dashboard definition (verifying `BrowserPanelInputs` pins when present). |
| `run-query --id RUN (--file query.json \| --metric M ...)` | Queries archived run series (`POST /api/v1/tests/{id}/archive/native-series`). |
| `evidence-query --id RUN (--file query.json \| --metric M ...)` | Queries live run interval or merged series (`POST /api/v1/tests/{id}/native-series`). |
| `byte-query --id RUN --mode live\|archive (--file query.json \| --metric M ...)` | Queries live (`POST /api/v1/tests/{id}/native-series`) or archived (`POST /api/v1/tests/{id}/archive/native-series`) protocol payload/body byte series. |
| `progress --id RUN --mode live\|archive [--group G] [--source-id S]` | Reads standalone Event Hubs processor progress (`GET /api/v1/tests/{id}/messaging-progress`). |
| `baseline --id RUN --file req.json [--fail-on-regression]` | Runs a workload-qualified baseline comparison (`POST /api/v1/tests/{id}/baseline-comparisons`). |
| `policies list\|get\|save\|delete --project P ...` | Lists, reads (including `--revision`), creates/updates or deletes revisioned analysis policies. |
| `telemetry-policy` / `telemetry-policy-change` / `telemetry-reevaluate` | Reads live or archived telemetry policy receipts, appends runtime budget segments (`POST /api/v1/tests/{id}/telemetry-policy-segments`), or re-evaluates runs under a policy revision (`POST /api/v1/analysis/evaluations`). |
| `journeys` / `browser-findings` / `browser-documents` / `browser-resources` / `browser-har` / `interactions` / `har-entry` | Reads finalized journey accounting, browser findings, document evidence, paginated retained resources, HAR inventories/downloads, interaction patterns and single-request HAR entries. |

## Filtered archive panels and comparisons

In a run's **Create project analytics panel** form, choose **Archived actions** or **Archived business transactions**. Select a case-sensitive exact action/transaction name, an optional retained approved property category (actions only), elapsed start/end seconds and bucket width. An optional comparison run ID saves a two-run query with the same selection and elapsed wall-time window for both runs. Editors save project definitions; viewers can reopen and query them. Existing run-total definitions keep their previous semantics.

The first slice supports raw observation counts, error percentages and response-time mean/min/max/P50/P90/P95/P99. Percentiles use nearest rank over raw observations within each bucket, never averages of stored percentiles. Empty buckets are missing, including count buckets; they do not assert that zero traffic occurred. Missing retained properties and legacy observations whose population cannot be established produce explicit warnings. Property selection matches the retained finite category, including `other` or `missing` when those were actually recorded.

Queries require a completed archive and recorded execution start. They allow a window within the first 24 elapsed hours, at most 1,000 buckets and 100,000 selected raw observations. Existing archive scan record/byte limits also apply, with a 30-second query deadline and shared analytics admission. Oversized or busy queries fail explicitly; narrow the selection or retry. This slice reads the canonical JSONL transaction evidence and does not require DuckDB. Summary-only archives cannot supply raw percentiles.

Click a chart bucket or open **Inspect time buckets** to inspect current/baseline observations. The paginated inspector reuses the authorized archive transaction endpoint and redacted snapshot preview components. Exact identity/property/population filters and inclusive-start/exclusive-end UTC bounds match the plotted bucket. Request and response bodies remain subject to existing capture and disclosure policies.

Comparisons require both runs to belong to the same project. Results show added/removed identities and compatibility warnings when execution fingerprints are missing/different or evidence limitations exist. Values remain separate; no automatic regression verdict is inferred from an unqualified pair. This original comparison workflow remains archive-only. The grouped/live workflow below adds bounded dimension selection and monitor/imported overlays. Live SLA actions remain separate.

## Live and grouped evidence panels

Choose **Filtered live intervals** or **Grouped archive and overlays** in the native panel form. Saved selections remain project-scoped and revisioned. Group by action/transaction identity, a retained property name, region, or dependency; choose 1–20 series. Archive grouping scans raw observations once with the same 100,000-observation and 1,000-bucket bounds. Exceeding the selected series cap fails rather than silently discarding groups.

Live panels refresh every ten seconds after the preceding request completes. They read the newest 1,000 retained publication intervals within the saved elapsed window. Action, business-transaction, property, region and dependency summaries are retained in the existing bounded history, persisted in an optional versioned `native_evidence_json` column, and restored after restart. Detailed evidence is queried separately from the metrics WebSocket feed. Each dimension retains at most 128 identities; incomplete intervals remain unavailable. A generator's additive native-evidence version marks support; mixed peers and source-plan runtimes without the required summaries cannot claim complete evidence.

Completed archives also carry these distributions in `interval_distributions.jsonl` (`apexload.interval_distribution.v1`), so they outlive history retention and travel with exported archives. Finalization copies every retained interval with version 2 or later native evidence: per dimension and key, the count, errors, the duration sketch and t-digest in nanoseconds (intervals recorded before the sketch carry only the t-digest), the percentiles live showed and the incomplete flag. Each key's `percentile_quality` names the distribution kept: `native_sketch`, `native_tdigest` or `native_scalar`. Runs without such evidence are rebuilt from one scan of the archived transaction rows, per transaction and action, within 100,000 observations, 1,000 intervals and 128 keys per dimension, and marked `reconstructed_from_rows`; their percentiles come from the rebuilt sketch. The manifest records `interval_distributions_file`, its count and source, and `interval_distributions_truncated` when a bound, skipped interval or the sidecar's byte allowance cut it short, or when the run reached the store's 3,600 retained interval rows (its oldest may have been trimmed). The sidecar is written once; later metadata refreshes keep it. Merge the sketches across intervals, or the t-digests where an interval has no sketch; never average the percentiles.

Live percentiles describe their original publication summaries and are never averaged or re-bucketed. The configured bucket width applies to archive and overlay queries. Live regions describe business transactions; dependencies and properties describe actions. Independent live dimension summaries cannot answer intersections: action/property filtering requires version 2 merged evidence; action/network/hierarchy/lifecycle filtering requires version 3 merged evidence. Unsupported combinations and unavailable peers return explicit errors. History eviction, old rows and missing observations cannot be interpreted as zeros. Selected live elapsed windows are fixed, not automatically sliding. In a distributed run the controller publishes at most about once a second for the whole fleet, and each published point carries one generator's window, so a merged version 2 or 3 query over several generators sees only the published windows: a bucket that lacks any expected generator's window is a gap; a bucket where every generator published but whose window is not wholly covered shows counts and distributions from the published windows only (an undercount), no rates, and coverage below 1, and the query carries the partial-coverage warning. Joint evidence v4 (below) carries every accepted batch instead.

For an overlay, select a run monitor source or an imported dataset, its metric and aggregation, and optionally a monitor host. Imported datasets must be ready, project-authorized and explicitly associated with this run; association is checked before analytical reads. Monitor and dataset project flags still apply, and imported queries require the native DuckDB profile. A metric must have a known unit; the existing analytical query layer rejects mixed or incompatible series. Different measurements use separately labelled axes, with no implied numerical equivalence. Missing/stale/invalid-quality values remain gaps. The table and CSV preserve original values and units.

Archive panels use the shared lag-correlation implementation on aligned buckets. Results include paired observations, coverage and an optional coefficient; insufficient or constant evidence stays unscored. Live source mode displays publication intervals without resampling. Optional `live_bucket_mode: merged` enables selectable completion-cohort buckets and lag correlation; it requires version 2 native evidence with complete duration distributions from every contributing runtime. A positive lag means overlay evidence follows the selected series. Association does not establish causation. Cross-run baselines still use the existing archive comparison workflow; the new grouped workflow does not invent regression verdicts.

### Merged live publication buckets

Select **Merge live intervals into buckets** for counts, weighted averages, errors and percentiles computed from mergeable distributions. A bucket contains whole source publications ending in `(from, to]`; distributions cannot be split, so boundary attribution is approximate at source-window resolution. Source mode remains the default and historical summary-only evidence is never converted into a distribution.

Action/property intersections use bounded joint evidence (128 identities per interval), rather than intersecting unrelated marginals. Queries retain the 20-series and 1,000-bucket limits and the latest 1,000 publications. Distributed source publications are deduplicated by source, execution generation and sequence. Missing expected sources, drops and malformed distributions remain missing. Collection coverage describes source windows separately from correlation pair coverage. Correlation is association, not proof of causation.

### Dimensions, timeline annotations and transaction investigation

Archive panels can filter and group by network profile, hierarchy and lifecycle phase, retaining existing action/property/region/dependency options. Network profile is also available as an independent live action marginal. Merged live action evidence version 3 supports hierarchy and lifecycle grouping, and exact action/network-profile/hierarchy/phase intersections. These tuples retain at most 128 identities per publication, each with a mergeable duration distribution. Unretained combinations (including property with hierarchy) fail explicitly, as do missing selected labels; older peers remain unavailable for these queries. Dimension filters exclude missing legacy values; missing is never zero.

Joint evidence v4 measures configured intersections. During a rolling upgrade a projection can be lost silently: a scenario's projection is applied only by controllers that know it (an older execution leader creates its scheduled or plan runs without it), and a scenario update replaces the whole scenario: an update without `joint_evidence` clears the projection, so an older dashboard or an API client unaware of the field that saves the scenario clears it. Finish the upgrade before relying on scenario projections, and check `joint_evidence` after saving from an older client. A projection is one population (`action` or `transaction`) and up to three dimensions from `name`, `protocol`, `group`, `region`, `dependency`, `page`, `hierarchy`, `phase`, `network_profile`, `browser_profile`, `browser_engine`, `browser_platform` and one `property:<category>`. Choose it on a scenario (`joint_evidence` on create and update, or **Joint evidence** under the scenario builder's run settings, Advanced settings): every run created from the scenario captures it, whether started directly, by a schedule or by an execution-plan entry, unless the run's own request names another `joint_evidence`. A run created without a scenario names it on its request (`POST /api/v1/tests`, or `POST /api/v1/ci/runs`, where it is part of the request an `Idempotency-Key` retry must repeat). A `property:<category>` must be a category one of the run's scripts promotes (`results.property_analytics`, after the scenario's and the group's runtime overrides), or `browser_iteration`, the cold/warm category the engine gives each browser iteration; any other is refused when the scenario is saved (including by a runtime-settings change applied to its groups) and when a run is created. A revised copy of a queued run is checked like any run, except that it may keep a projection its source run was admitted with before this rule (a category the source's own groups do not promote either). A run that inherits a projection whose category its scripts no longer promote (a script edited since) is refused with that category named; update the scenario's projection or the script. The projection is frozen at Start and sent only to generators advertising `native_joint_evidence_v4`; each counts every sample of the population under the exact tuple of its values (a dimension a sample lacks is the empty value). A publication keeps at most 512 tuples; the rest go to an explicit other bucket, the distinct omitted tuples are counted and the window is marked incomplete. Generators' tuples merge by exact key; an older generator's windows are `unavailable` (`source_lacks_joint_evidence_v4`) and leave the fleet window incomplete. Merged live queries with `joint_evidence: true` group by up to three projected dimensions (series are named by the JSON array of the selected values) and filter on projected dimensions, summing exact tuples over the rest; a window with unavailable, incomplete or differently projected evidence is a gap, and an unprojected dimension or another population is refused. Joint evidence is incomplete only past its own tuple bound or once samples were lost (dropped results or subscriber batches, missing windows; these counters are cumulative for the run, so every window after the first loss is incomplete); the marginal dimensions' 128-key caps do not affect it. The catalog advertises these as the `live/merged/<population>/projected/joint_v4` query profiles, with `property_mode: projected_category` for a projected `property:<category>`, which groups and filters like a dimension. Distributed publications are throttled fleet-wide, so a generator batch accepted between two published points does not get its own point: the next published point carries the joint evidence of every such batch (at most 1,024 batches or about 64 MiB held; past that the oldest are dropped and counted; a single publication larger than the whole byte allowance is refused and counted before evicting earlier windows), and merged live queries and the archive read them as separate source publications identified by source, source epoch, execution generation and sequence. The carried batches count toward the in-memory history limit, so such a run keeps a shorter live window in memory. When a generator in the fleet publishes no native evidence, published points cannot carry the batches: the archive gets only those still held at finalize and, if any were dropped past the bound, is marked truncated. After finalize the archive holds `joint_intervals_v4.jsonl` and `.parquet`, read from the run's persisted interval evidence: one row per measured tuple of each source publication (including the batches carried by a published point, each with its `source_epoch`), one for its other bucket and one per unavailable source window. Batches still held at finalize are added after the history. The file is marked truncated when any were dropped past the bound, when a distributed run was adopted after a failover or reloaded after a restart (the previous controller's held batches are unknown), when the run reached the store's 3,600 retained interval rows (its oldest may have been trimmed), or when it is first written after the held generator batches were released: by a later refresh after its write at finalize failed (they are released when the archive is committed), or by a later recovery archive after this controller collected none (they are released when no archive is collected, and on leadership loss). A publication the controller still holds in memory but whose persisted write is missing is added, and the file is then marked truncated (earlier writes may have failed too). Each file has its own byte allowance (`APEXLOAD_ARCHIVE_JOINT_INTERVALS_MAX_BYTES`, 64 MiB; the Parquet copy has 1 MiB more for its format); a JSONL that reaches it is marked truncated, and a failed write leaves neither file.

Invalid distribution copies remain bounded before retention: duration sketches over 4,096 bins and t-digests over 1,024 centroids are refused rather than trimmed into a valid distribution. Counts and compatible fallback evidence retain their existing interpretation; absence is not a measured zero or a qualified percentile. The distribution and queue review (`docs/qa/reviews/roadmap-joint-bounds-2026-10-20/README.md`) covers these bounds. Received joint protobuf publications are checked before retained copies: at most 512 canonical unique tuple keys, an admitted projection, bounded distribution arrays, nonnegative counts and finite scalar durations, and a 64 MiB encoded joint-field envelope including protobuf framing. Malformed joint evidence is explicitly unavailable while unrelated action evidence survives. Omitted tuples remain incomplete. This preflight does not replace transport admission or recovery qualification; the entry-point slice (`docs/qa/reviews/roadmap-joint-entry-points-2026-10-21/README.md`) covers how a projection reaches each run and which property categories it may name.

Limits: labels are capped at 256 bytes and keys at 1 KiB. Each script that promotes a property category approves at most 16 values; any other observed value counts as `other` and an absent one as `missing`, and samples from groups whose scripts do not promote it carry the empty value, so the category's cardinality is bounded before any sample is counted. A generator keeps for each tuple only what joint evidence publishes (its count, errors and duration distribution; diagnostics, transport timings, bytes, duration bounds and correlation samples stay with the marginal action metrics) and publishes only those fields, so the largest joint part it can build (512 tuples with 1 KiB keys and the other bucket, each with a 1,024-centroid digest and a 4,096-bin sketch) encodes to about 48.7 MiB, within the 64 MiB joint envelope; the transport's per-message limit applies to the whole batch separately. Received joint publications are bounded and validated at ingest as described above; digests are validated again when queried.

Journey accounting counts how a finished run's declared journeys turned out under its pinned `journey_policy` (or a named revision): completed, failed, aborted, skipped and censored attempts per journey, step failures separately, and the weighted failure index, which is test-journey risk and never revenue. Every archived row carries its VUser and iteration; a failure recorded once its VUser was stopping (the run's stop or end, a ramp-down or scale-down, a goal boundary), or caused by the generator's own resource limit, is marked `interrupted` and censors the attempts it cut instead of failing them. Read it from the run report's Journey risk panel, `GET /api/v1/tests/{id}/archive/journeys` or `apexctl analysis journeys`; see the [journey accounting guide](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#journey-accounting).

Browser findings judge a run's archived document metrics against its pinned `browser_policy` as a `browser_intelligence` evaluation job (automatically at finalize for runs that pinned one and captured document evidence, or on demand): threshold findings per engine and device profile at the qualified p75 (active, suppressed outside applicability, or unscored with a reason), collection-health findings kept apart, and a weighted `browser_score.v1` that is withheld when a required metric is missing. Read them in the report's Browser findings panel, `GET /api/v1/tests/{id}/archive/browser-findings` or `apexctl analysis browser-findings`; see the [browser findings guide](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#browser-findings-and-qualified-scores).

The run report's Browser area, the policy editors in Settings, the browser intelligence report sections, saved browser-document panels, the HAR-entry drill-down and the matching CLI and MCP tools are described in [Browser intelligence workflows](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md).

Select a grouped archive bucket to inspect up to 25 raw observations per page. The selection retains population, property, time and exact group identity, including capitalization. The timing waterfall shows retained DNS/connect/TLS durations, TTFB as an elapsed milestone, and total observation duration. Unrecorded timing phases remain unavailable.

Editors can add timestamped **Run timeline annotations** after execution starts, using elapsed wall time. Viewers read notes. Revisions prevent concurrent edits from overwriting each other; reload after a conflict. There are at most 200 audit entries and 512 KiB of encoded immutable note content per run. Bounded deletion metadata has separate headroom, with total retained history capped at 1 MiB, so accepted notes remain deletable even when content is at its quota. Deletion hides a note but retains its authorship and deletion audit until the parent run is deleted. Notes stay separate from measurements and report commentary; grouped timeline responses include active notes in the requested window.

## Export displayed charts

CSV/SVG controls now cover live/native and archived charts, saved baseline details, dataset series/overlays, run-bound and deployment Prometheus timelines, imported line/heatmap/stat panels, load comparisons, release trends, browser breakdowns, retained execution load and Dashboard live summaries. Authoring/capacity/traffic-profile previews and the Command Center planned-only load previews are planning surfaces, not evidence exports.

Exports retain the displayed rows and series, selected source identities, units, filters and available quality/coverage metadata. Changing a dataset query draft does not relabel its last returned chart; export provenance describes the query that produced those rows. Baseline Prometheus exports include original source timestamps and aligned display timestamps. Chart CSV keeps missing/nonfinite evidence empty and protects spreadsheet formula-bearing strings. Limits are 100,000 rows, 256 columns and 16 MiB; exceeding a limit returns a visible error, never a partial export. Live chart context menus retain their basic CSV layout while adding displayed metric labels, units, source/selection context and equivalent resource/formula guards. Displayed smoothing and sampling are exported explicitly; retained sample counts remain separate.

SVG files resolve theme paints while the chart is attached, retain local gradients, include accessible descriptions and remove active content/external resources. Exports are bounded to 100,000 elements and 16 MiB. Load Impact's CSV exports the selected metric/load cells, interval means, min/max and retained interval counts. Its SVG is a labelled 2D summary of that selection even when the interactive view is 3D; it does not claim to capture the 3D camera. Cell means of recorded percentile intervals are not pooled transaction percentiles. Sparkline exports explicitly use sample indices where source timestamps are unavailable. Dashboard aggregate coverage includes only retained contributors; it is not verified complete fleet coverage.

### Retained observation rates

Native action and business-transaction panels support `observation_rate`, `error_observation_rate` and `success_observation_rate`, each with `aggregation: rate` and unit `observations/s`. Counts come from the selected retained evidence. Archive buckets use their actual elapsed span, including shortened last buckets; source live intervals use their publication window. Merged live rates require complete coverage from each contributing source, with nonoverlapping publication windows wholly contained in the bucket. Coverage uses the union of each source's clipped time windows; whole-window counts are never prorated across bucket boundaries. Empty or incomplete buckets remain unavailable; an observed population with no failures has a measured zero failure rate. These rates describe retained observations, not guaranteed total execution throughput. Existing capture/coverage warnings and filters apply.

## Shared metric and operation catalog

The analysis capabilities response includes `metric_catalog` even when native imports are disabled for the project. `apexctl analysis capabilities` exposes the same contract. Catalog presence describes supported semantics; project enablement and the selected run's captured evidence still determine availability.

The retained action/transaction catalog describes response time in milliseconds, error rate in percent, observation count, and observation/error/success rates in counts per elapsed second. Each metric also identifies its existing native wire unit: `ms`, `%`, `observations` or `observations/s`. Those response units remain unchanged; shared normalization maps them to the canonical catalog units without treating message or byte populations as transaction observations. Query profiles enumerate currently executable population, archive/live mode, grouping, filters, property selection, bounds and required evidence. They do not claim protocol/group intersections that have not been captured. Existing saved definitions retain their behavior.

Shared typed operations implement eligible count ratios, epoch-aware counter deltas/rates, comparable absolute/percentage baseline changes, and bounded moving mean/standard deviation. Existing raw counter bucket rates retain their reset-only display behavior and explicitly unknown epoch lineage. They cannot qualify the new baseline operations simply because a metric version/population is supplied; qualified counter delta and rate operations require the explicit epoch-aware input (moving statistics derive the bucketed display series as it is). A complete observed window with zero events has a measured count/rate of zero. Missing windows, incompatible semantics, unknown/reset epochs and empty denominators remain unscored. Derived points retain bounded operation, algorithm and elapsed-bucket lineage, so a P95 series cannot be compared as an average series. Legacy derived points with unknown parent statistics remain readable but unqualified for these new operations. Imported precomputed summaries and histograms are re-bucketed by their own typed rules (see the summary and histogram section above); counter, ratio and baseline operations do not accept them as inputs, while moving statistics derive their bucketed percentile series.

### Executable derivations in queries

A native query (live or archive evidence, the archive panel and the imported dataset series) accepts `derivation`: one catalog operation and its parameters. The catalog is the validator; the controller evaluates the operation with the same `pkg/analysis` functions on the query's buckets, so live, archive and imported series share one semantics.

| Operation | Applies to | Parameters |
| --- | --- | --- |
| `error_ratio`, `success_ratio` | action and transaction populations | — |
| `duplicate_ratio`, `redelivery_ratio`, `dead_letter_ratio` | the archive `message` population (messaging v2 counters) | — |
| `acknowledgement_ratio`, `settlement_ratio` | the `message` population | — (every bucket is `evidence_unavailable`: the engine records no acknowledgement or settlement counters yet) |
| `baseline_delta`, `baseline_percent_delta` | archive panels with `comparison_run_id` | — |
| `moving_mean`, `moving_stddev` | scalar series on an aligned grid (archive, merged live, imported) | `window_buckets` 2–1,000, `minimum_coverage` in (0, 1] |
| `delta`, `rate` | imported counter series queried with `aggregation: max` (the controller then reads each bucket's closing counter reading and its instant, `reading_at`, so a rate divides by the time between readings; a display query still converts counters to rates; the dataset-series response returns the derived values in `derived`, beside the raw `points`) | — |

Only a baseline derivation takes a `comparison_run_id`; any other derivation
with one is refused, so a comparison panel never mixes a derived current
series with a raw baseline. Moving statistics need contiguous buckets on one
grid. Merged live buckets are complete when some source published into them and
every expected source did so with valid evidence; a count or count rate panel
also needs the bucket's whole window covered by publications inside it,
except under a ratio derivation, which reads the counts as a sample. Ratios
therefore score in merged live mode; moving statistics over a count panel
score only where publications cover whole buckets. An archive whose source coverage lists an uncollected
generator, or whose CSV export is incomplete, leaves every bucket incomplete:
a ratio or counter derivation reports `incomplete_evidence`, a moving one
`insufficient_coverage` or `insufficient_valid_buckets`. Live evidence queries (`POST /api/v1/tests/{id}/native-series`) serve the `action`, `transaction` and `byte` (`sum` and `rate` from `byte_evidence.live_interval_totals.v1`) populations; `message`, `browser_document`, `java_http_action` and per-observation `byte` distributions are archive queries (`POST /api/v1/tests/{id}/archive/native-series`). Responses name the catalog operation even when an alias was asked for. A controller that predates derivations ignores the field: during a rolling upgrade a saved derivation can be dropped by an older peer that re-saves the dashboard, and a derived query answered by one returns raw values without a `derivation` echo.

Each derived bucket carries its value or a `reason`: `zero_denominator`, `incomplete_evidence`, `numerator_not_subset`, `missing_bucket`, `missing_baseline_bucket`, `baseline_not_comparable`, `insufficient_window`, `insufficient_valid_buckets`, `insufficient_coverage`, `no_prior_sample`, `missing_counter_sample`, `counter_reset`, `duplicate_counter_instant`, `invalid_counter_sample`, `counter_epoch_changed_or_unknown`, `evidence_unavailable` and the rest of the typed functions' reasons. A complete window with no events is a measured zero for counts and rates but an unscored zero denominator for a ratio (a merged live bucket is complete only when some source published into it, and a count bucket only when publications cover its whole window); a bucket past run completion, with legacy population or missing property evidence, is incomplete. A baseline derivation compares only a comparable baseline run (the same comparison fingerprint and complete evidence); otherwise every bucket is `baseline_not_comparable`. Responses name the applied `derivation` and the derived unit (`ratio`, `percent` or the series' canonical unit); a query without a derivation renders exactly as before.

The archive `message` population reads received messages from messaging v2 evidence (`message_count`, optionally filtered by `source`). A message ratio divides the messages its counter counted by the messages that counter observed, and a bucket is complete only when every selected dimension carried complete v2 evidence and the counter observed every eligible message; a bucket described only by v1 summaries stays missing. Duplicate ratios are lower bounds within each consumer's local sequence window. Imported counters use one epoch per dataset revision and identity; a bucket in which a reading falls below the previous one (inside it or across from an earlier bucket) is a `counter_reset`, and one instant with two different readings is a `duplicate_counter_instant`; the bucket after a missing one is a `missing_counter_sample`. Moving statistics over an imported series derive the bucketed series itself (a counter's rate, a histogram or summary percentile), and imported metric names are the dataset's own, never matched against the native catalog. Moving derivations on live per-source intervals, whose windows are not aligned, are refused.

Saved dashboards validate through the catalog: catalog metrics and aggregations, the retained grouping and filter dimensions, and an alias table that keeps saved names meaning what they did (`tps` is `observation_rate`, `transaction_duration` is `response_time`, and live display cards such as `active_vusers`, `throughput`, `regional_activity`, `resource_cpu` and `resource_memory` keep rendering but are not catalog measurements and take no derivation; `stddev` remains a display aggregation). The catalog also declares `message_count` (population `message`; it takes moving statistics, and saved message panels may filter by `source`), the protocol payload and body byte metrics of population `byte` ([HTTP body bytes](#http-body-bytes)), and the Java HTTP codec/representation metrics of population `java_http_action` ([Java HTTP representation diagnostics](#java-http-representation-diagnostics)). Browser document metrics `document_fcp`, `document_lcp`, `document_cls` (unit `score`) and `document_inp` (population `browser_document`, aggregations `p50`, `p75`, `p90`, `p95` and `p99`; each bucket's `count` is its final documents) read one measurement tuple's final document distribution under `query_version: 2`, or up to 20 separate cohort series under `query_version: 3`–`5` (`browser_tuple_mode: "separate"`); their filters (`engine`, `device_profile`, `frame_scope`, `lifecycle_phase`, `group`, `region`, `cohort`) apply to these metrics only. See [Browser intelligence workflows](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#document-and-navigation-evidence).

### HTTP body bytes

Panels and queries of population `byte` (`pkg/analysis/byte_catalog.go`) measure protocol payload and body bytes across HTTP, gRPC and WebSocket in both archive and live modes (`apexctl analysis byte-query --mode archive|live`):
- `bytes_sent` and `bytes_received` (`bytes`): per-bucket `sum`, or—from archived raw observations—per-observation `avg`, `min`, `max`, `p50`, `p90`, `p95` and `p99` (nearest rank);
- `bytes_sent_rate` and `bytes_received_rate` (`bytes/s`): `rate` over complete elapsed windows.

Each supported protocol stamps an explicit `byte_evidence` contract on every measured observation so readers distinguish a measured zero from an unmeasured protocol or step:
- **HTTP (`http_body.v1`, `filters.protocol: "http"`):** HTTP message body bytes as they crossed the wire, after transfer decoding and before content decoding (compressed bodies count compressed). **Sent** counts the dispatched request body once (and again for a completed digest-auth replay, excluding failed replays, cache hits, dials refused before headers were written, redirect re-sends and earlier engine-retry attempts; a request that failed after headers were written counts its whole body). **Received** counts the response body plus embedded resources and meta-refresh follow-ups fetched by the action (nothing for fresh or `304` cache hits). HTTP headers, framing, TLS and connection setup are excluded; `ServerTelemetry` payload sizes retain their separate diagnostic basis.
- **gRPC (`grpc_uncompressed_payload.v1`, `filters.protocol: "grpc"`):** uncompressed gRPC protobuf payload bytes observed by the gRPC stats handler across unary, client-stream, server-stream and bidirectional RPCs (`ServerTelemetry.GRPC` request/response payload bytes projected into archived byte columns). HTTP/2 framing, HPACK headers and TLS record overhead are excluded.
- **WebSocket (`ws_application_payload.v1`, `filters.protocol: "websocket"`):** WebSocket application message payload bytes sent or received on data frames (`ws_send`, `ws_receive` and callback data messages). WebSocket frame headers, masking keys, control frames (`ws_ping`, `ws_close`) and synthetic connection/dispatch steps are excluded.

**Provenance, exclusions and protocol separation.**
- **Steps that dispatch no payload transfer (`byte_evidence: "none"`):** checks, handshakes, pings, closes and actions refused before sending are excluded from the population and counted under `no_request`.
- **Earlier versions and mixed evidence:** observations archived without a matching `byte_evidence` stamp (or intervals stamped `unmeasured` because an execution lacked byte evidence or mixed incompatible contracts) leave their bucket unscored (`byte_evidence_unavailable`), never a measured zero. SOAP and `java_http` count as their own protocols outside population `byte` (`java_http` representation metrics use `java_http_action` below).
- **Filtering and grouping:** byte queries may filter by `protocol` (`http`, `grpc`, `websocket`) and exact `action`, group by `action` (up to `max_series`, max 20), and—in archive mode—compare against `comparison_run_id`. When an archive holds multiple measured protocols without a `filters.protocol` selector, mixed-protocol buckets are refused or kept separate by action/protocol provenance rather than summing incompatible wire layers silently.

**Archive vs. live byte buckets.**
- **Archived raw observations (`POST /api/v1/tests/{id}/archive/native-series`):** each observation is bucketed by its start timestamp (up to 100,000 selected observations and 1,000 action identities per protocol). `sum` and `rate` require a complete bucket—every selected observation evidenced, `required` source coverage with all sources collected on distributed runs, and the bucket ended by run completion—otherwise the bucket is unscored (`incomplete_bucket`). Negative counts (`invalid_byte_count`) and sums exceeding $2^{53} - 1$ (`exact_sum_limit`) are unscored. A bucket with no observations has no value; a request or frame that transferred zero payload bytes is a measured zero.
- **Live interval byte totals (`POST /api/v1/tests/{id}/native-series`, `byte_evidence.live_interval_totals.v1`):** live queries support `sum` and `rate` over complete retained publication intervals (`live_bucket_mode: "source"`, requiring native evidence `v1`–`v3`) or merged completion-cohort buckets (`live_bucket_mode: "merged"`). Live interval totals never synthesize per-request percentiles (`avg`, `min`, `max`, `p50`–`p99`) or transport-overhead estimates; incomplete or mixed-contract intervals report `incomplete_bucket` or `byte_evidence_unavailable`.

### Java HTTP representation diagnostics

Archived `java_http` actions expose a dedicated `java_http_action` catalog population (`pkg/analysis/java_http_catalog.go`, version `java_http.diagnostics.v1`) kept separate from generic latency and `byte` throughput adapters so serialized representation sizes are never misread as wire payload throughput:
- **Byte and timing distributions (`avg`, `min`, `max`, `p50`, `p90`, `p95`, `p99`):** `java_http_input_bytes`, `java_http_decoded_request_bytes`, `java_http_prepared_body_bytes`, `java_http_retained_response_bytes`, `java_http_response_base64_bytes`, `java_shallow_object_bytes`, `java_reachable_graph_bytes` (`bytes`), plus `java_http_base64_decode_ms`, `java_http_base64_encode_ms`, `java_http_enclosing_ms`, `java_serialization_ms` and `java_deserialization_ms` (`ms`).
- **Paired representation ratios (`ratio_of_sums` over same-sample pairs):** `java_http_decoded_per_encoded`, `java_http_prepared_per_decoded`, `serialized_representation_vs_http_body`, `java_http_base64_per_retained` and `java_http_base64_work_share` (`ratio`).

All `java_http_action` metrics require `java_http_diagnostics.v1` over complete selected observations of the same action population (`MissingRule: unscored`) and support two-run baseline comparison.
