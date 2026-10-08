# Run monitoring, preparation, and Parquet analysis

These workflows are explicitly enabled per project. Existing runs, clients, and
archives remain readable without them. The generated [API specification](swagger.yaml)
is the authoritative route and request contract.

## Enable the workflows

Set comma-separated project IDs on every controller replica:

```text
APEXLOAD_RUN_MONITOR_PROJECTS=project-id
APEXLOAD_PREPARATION_PROJECTS=project-id
APEXLOAD_ANALYSIS_PROJECTS=project-id
```

Native Parquet operations additionally require `CGO_ENABLED=1` and the
`analytics_duckdb` build tag (`make controller-analytics`). The pinned driver is
`github.com/duckdb/duckdb-go/v2`; published controller images use
`Dockerfile.controller`, and desktop packaging includes the tag. The ordinary
Go build and `Dockerfile` preserve the CGO-free profile, whose capabilities
response explicitly reports that native Parquet is unavailable. Generator and
collector builds do not import DuckDB. Native build/test lanes cover Linux and
macOS amd64/arm64 and Windows amd64; a CI lane is not evidence that its current
run has passed.

DuckDB databases are process-local, disposable caches. SQLite/PostgreSQL owns
metadata, revisions and publication state. The configured archive backend owns
original and normalized artifacts. HA replicas never share a writable DuckDB
database. Queries use approved operations with extension installation/loading
disabled and file access limited to their protected staging directory.

## Bind monitoring to a run

Register a collector through the existing project administration workflow.
Configure a persistent, base64-encoded Ed25519 seed in
`APEXLOAD_MONITOR_SIGNING_SEED` on controllers. Pin its public key with the
collector's `-monitor-public-key` flag or `APEXLOAD_MONITOR_PUBLIC_KEY`.

The collector must approve the same pack and exact Prometheus URL locally:

```sh
apexload-collector-agent -collector-id COLLECTOR_ID \
  -packs prometheus -prometheus-url https://metrics.example/metrics \
  -spool-directory /var/lib/apexload/collector
```

Supply the collector token using `APEXLOAD_COLLECTOR_TOKEN`, rather than a
command-line argument. Keep the spool directory persistent and private. It
retains bounded unacknowledged batches and locks out a second process using
the same controller/collector identity. Signed mode sends availability
heartbeats without polling the monitored system until an execution commits.

In **Monitoring → Telemetry → Run monitor sources**, project administrators
create Linux host, Go runtime, or Prometheus sources. In an existing scenario's
data step, editors select sources and save revisioned bindings. Required
sources must have a recent healthy heartbeat and advertise support for the
signed job and approved destination. Optional failures remain visible as
incomplete coverage. Source edits affect subsequent runs.

Each run captures immutable source and binding revisions. Samples have source
timestamps, durable receipt times and an elapsed wall-time clock that includes
pauses. Batches are acknowledged after artifact persistence and metadata
publication; retries deduplicate by job, sequence and evidence digest. A run
archive includes hashed `monitor_evidence.v1.jsonl` evidence and coverage
metadata. The execution page overlays selected monitor measurements with run
latency. Missing/stale evidence is not plotted as zero.

The collector supports fixed SSH/Linux, bounded OTLP/HTTP JSON, and restricted
JMX/JDBC packs in addition to Linux/runtime/Prometheus. CloudWatch and Azure
Monitor are opt-in preview adapters with bounded identity-scoped queries; see
the cloud collector configuration below. Live cloud acceptance remains required; selecting a pack cannot silently return host metrics.

## Prepare before Start

For an enabled project, queued runs expose **Prepare**. Preparation validates
the immutable script/asset revision, reserves or provisions capacity using the
existing target backends, stages assignments and checks collector availability.
It waits before runtime preflight, protocol initialization, reflection, init
actions and monitoring-source polling. Runtimes that cannot honor this boundary
are rejected explicitly (currently Java Vuser, Bruno, and shakeout preparation).

Readiness defaults to 30 minutes from the ready transition; API/CLI callers can choose 5 minutes to 4 hours. Renewal rechecks the execution revision, reservation, ownership and collector readiness. Provisioning itself also has the requested TTL deadline.

Distributed preparation requires both `prepare_v1` and `prepare_readiness_v1` from every selected generator. Its durable allocation records the staged group/peer/VUser reservations before readiness is published. A separate control-plane RPC checks the immutable allocation, unstarted execution groups, execution generation, supervision lease and peak-capacity claim. Periodic checks refresh the existing generator stage deadline without runtime preflight, user code, protocol initialization or target probes. Missing, expired, drained or changed reservations fail readiness; an expired assignment cannot be recreated by a check. Start and renewal repeat the check. Revision or controller-ownership loss invalidates preparation and follows ordinary fenced cleanup. Older peers reject this requested preparation capability while ordinary Start remains compatible.

Local HTTP/TCP counters and real in-process gRPC transport test the preparation boundary, including HTTP init, gRPC reflection, WebSocket, SQL, network, browser navigation and Kafka staging (the native Kafka profile has its own build-tag check). Deterministic Kubernetes/Azure/ECS provider tests cover cancellation and cleanup. These tests do not establish live cloud production acceptance.

**Start prepared run** supplies the preparation ID and expected execution hash.
Start rechecks revision, secrets, ownership, collector availability and the
execution reservation, then atomically consumes readiness. Concurrent consumers
cannot start the same preparation twice. Ordinary Start remains available for
runs without preparations. Cancellation and expiry interrupt provisioning and
release capacity through the existing execution cleanup path; audit history is
retained. Existing readiness and cancellation remain accessible through the API and CLI after the project preparation flag is disabled. Controller recovery fails closed when a reservation cannot be recovered.

```sh
apexctl prepare create --id RUN_ID --ttl 30m
apexctl prepare show --id RUN_ID
apexctl prepare renew --id RUN_ID --preparation-id PREPARATION_ID --revision REVISION_NUMBER --ttl 30m
apexctl prepare start --id RUN_ID --preparation-id PREPARATION_ID --revision HASH
apexctl prepare cancel --id RUN_ID
```

Zero-traffic acceptance must be verified for each target/runtime combination.
Provider implementations reusing the common gate do not by themselves establish
that every cloud or runtime combination has passed live acceptance.

## Import and compare Parquet

Open **Monitoring → Analysis**. Editors upload files, inspect a bounded schema
and redacted preview, map timestamps and measurements, then validate and import.
Directory selection preserves relative partition paths. Long metric-name/value and wide measurement mappings are supported, including explicit nested scalar paths. Uploads use resumable 4 MiB chunks with content verification; resuming rechecks
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
pending status 503, retains their bytes and keeps the cleanup job. Qualified
process-restart, HA and remote-backend recovery remain open. See the
[upload and deletion review](qa/reviews/feature-completion-native-upload-2026-10-04/README.md).

Rejected measurements fail publication by default. Explicitly accepting them retains a complete rejected-row Parquet artifact with file/measure identity and zero-based source row ordinals, plus bounded metadata summaries. Rejected source values are never copied into error reports. The dashboard caps artifact downloads at 64 MiB; larger artifacts remain accessible through the authenticated API.

Mappings distinguish gauges, counters, raw transaction observations and summary
values. Timestamp mappings require an explicit timezone and encoding. Offset
text requires `timestamp_offset`; local wall timestamps use `timestamp`.
Units are normalized before comparison. Saved mappings retain immutable
revisions, and imports record the selected revision when one is used.
Mapping version 2 preserves source, host, service and exact bounded dimensions.
The workspace exposes separate identity filters for ordinary series and both
comparison sides; saved definitions retain the executed selectors. See
[imported measurement identities](ANALYSIS_IDENTITY.md) for nested scalar paths,
legacy mapping behavior, quality admission and saved-bound precision.

Percentiles come from raw observations, from a summary that declares the
percentile it reports, or interpolated within a histogram's bins. Summary
percentiles are never averaged or treated as transaction samples. Counters require explicit rate conversion;
resets, duplicate instants and missing samples interrupt the rate chain. Queries
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

Summary and histogram declarations are stored as fields of mapping versions 1
and 2. An older controller that rewrites a dataset's association drops them, and
queries of that dataset then fail; do not run controllers older than this
release against datasets that declare summary statistics or histograms, and
re-import a dataset that an older controller rewrote.

### Analysis policies and run pins

`/api/v1/analysis/policies?project_id=…` stores revisioned policies of five
kinds: `telemetry_policy`, `telemetry_acceptance_policy`, `browser_policy`,
`journey_policy` and `derived_operation`. Viewers list (by `kind`) and read
them; editors create, update with the expected `revision`, and delete. At most
one policy of each kind may set `apply_by_default`. A browser policy's
`pinned_run` baseline must be a run of the same project. When a run starts, or
a queued run is promoted, the project's default policy of each kind is pinned
into the run's admission record and appears as `policy_pins` in its provenance
and archive manifest; a later edit never changes a pinned run. Pins are
recorded with `applied: false`: no evaluation consumes them yet. A project
whose defaults cannot be read unambiguously refuses Start (and a queued run's
promotion) instead of running without its pins. Only project defaults are
pinned; system-wide telemetry defaults and scenario-level policy selection
are not yet resolved into the snapshot.

A `telemetry_policy` revision can also be named explicitly in a
`telemetry_acceptance` evaluation (`POST /api/v1/analysis/evaluations`), which
re-classifies up to ten runs' retained interval lag evidence under that one
revision as a derived, hashed result without changing the runs; see the
[telemetry guide](guides/TELEMETRY_POLICY_RECEIPTS.md#comparing-runs-and-re-evaluating-under-a-common-policy).

An external dataset keeps its own identity. Associate it explicitly with a run
in the same project before comparing it with managed evidence. Comparisons
select datasets, bound monitor evidence, or archived transaction durations and
align UTC buckets. Lagged correlation reports paired bucket count, coverage and
an optional coefficient; constant or insufficient evidence stays unscored.
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
SQL acknowledgement; platform hard containment and restart qualification remain
separate gates. Concurrent requests receive explicit busy, resource,
cancellation or size-limit errors. SQLite/PostgreSQL schema 33 records exact
attempt ownership, sealed publication and cleanup. Process-local retry records
bind the immutable SQL and artifact namespaces and retain ambiguous outcomes;
they are not proof of cross-process or cross-host writer termination.

The requested **10 GB compressed-input benchmark was skipped at the user's
direction**. No 10 GB duration, peak RSS, spill-volume or telemetry-lag result is
claimed. Cloud live acceptance and cross-platform release outcomes must be
recorded from actual executions before broader support is advertised.

Analytical storage is bound durably to its first configured archive namespace. All replicas must use that namespace. A changed filesystem root, bucket, prefix, or cloud identity stops analytical artifact access and cleanup with an explicit error. Restore the configuration, or migrate all analytical artifacts and the `analysis_storage_identity_v1` setting together offline with writers stopped; changing only the setting is unsafe. Dataset metadata and cancellation remain available when the native build capability or project flag is disabled. Physical deletion still requires the matching storage namespace.


## Collector adapter configuration

Use signed jobs for remote packs. `--adapter-targets` names a local JSON file of
approved pack/destination pairs. A scenario cannot change this allowlist.
For SSH, include the exact OpenSSH `host_key`; the only command reads loadavg,
meminfo, and uptime from `/proc`. No supplied command is executed.

Provide a protected `--credential-key-file` containing the base64 X25519 private
key. Enroll its public key in the project-admin source configuration using
`collector_public_key`. Set `credential_ref` to a project secret whose JSON value
contains `username` and `password`, or `private_key` for SSH. Credential bundles
are sealed to the collector and bound to project, job, source revision,
destination, and a one-minute signed lease. Readiness advertises the same public
key. Changing credentials never writes plaintext into spool or archive files.

OTLP uses `--otlp-listen 127.0.0.1:4318` and a protected `--otlp-token-file` (32 or
more bytes). Approve `otel-metrics` with an `otlp://stream-name` destination.
Emitters POST uncompressed OTLP JSON to `/v1/metrics` with bearer authentication
and `X-APEX-Monitor-Stream: otlp://stream-name`. Remote access requires a TLS proxy.
The receiver accepts gauges and cumulative monotonic sums, retaining timestamps
and bounded string attributes. Unsupported histogram, summary, delta, or
attribute semantics return explicit partial-success rejection counts. Payloads
are capped at 1 MiB and 512 buffered points per stream; full buffers return 429.
No automatic exporter/probe traffic is introduced by preparation.

Build and configure the [restricted Java helper](../runners/monitor-java/README.md)
for the `jmx` and PostgreSQL `jdbc` packs. Broader database drivers and attribute
packs require explicit implementations and acceptance; they are not advertised.

## Qualified diagnostics and dependency evidence

Enable `APEXLOAD_DIAGNOSTICS_PROJECTS` with a comma-separated project allowlist.
Live TPS, average response time, and error percentage are compared with 30
consecutive buckets having the same VUser count, load levels, and interval.
Staleness, missing buckets, telemetry drops, and pauses break qualification.
Zero-variance baselines remain unscored. Three-standard-deviation findings and
repeated error patterns are advisory; explicit SLA stop rules are unchanged.
Findings flow through existing incident history/webhook delivery. Reports derive
the same statistical evidence from retained interval summaries.

Set a source's `service` to the exact dependency label displayed by the run.
Dependency drilldown returns one actual interval with monitor coverage and
selectable metric links. It never averages percentile values or treats an
association as proof of cause. Missing and bounded-scan coverage are visible.

## Run validity

Every archived run report carries `validity` (rules `validity.v1`): whether the
load generators and telemetry could carry the load the run reports. It is one
of `valid`, `suspect`, `invalid` or `unknown`, with the rules behind it, the
intervals that were not valid and any limitations.

| Verdict | Rule (first values, recalibrated by the evaluation corpus) |
| --- | --- |
| `suspect` | A generator held CPU at or above 85%, lost 10% or more to steal, or was throttled in 10% or more of its CFS periods, for three consecutive intervals |
| `invalid` | A generator container was OOM-killed, more than 0.1% of an interval's results were dropped, an open model shed arrivals, or test data ran out |
| `unknown` | Generator statistics are missing for more than 10% of judged intervals, a generator's archive was not collected, or no intervals were archived |

- **Dropped results** are counted per protocol result, so their share is
  taken against the interval's archived result rows (or its completed
  transactions, when larger). The counters are cumulative; only a rise above
  their previous high counts, because a distributed run's fleet total dips
  when one generator's batch is late.
- **Missing statistics** count inside the span each generator reported over,
  so a generator added or removed mid-run is not missing outside it. An
  expected generator that never reported is missing throughout.

- **Judged intervals.** The verdict is the worst among intervals that overlap
  a measured steady level (the same plateaus baseline comparison uses, after
  their 30-second settling): steady VUsers for a closed model, a steady target
  arrival rate for an open model, and both for a mixed run. With no steady
  level, every interval is judged and `no_steady_level` is listed. Saturation
  must be sustained within judged intervals, so a ramp does not count.
- **Generators.** CPU is the higher of process CPU against allocated cores and
  container CPU against its limit. Distributed archives name each resource
  sample's generator (its target) when generator archives merge, so the rules
  hold per generator and an expected generator that never reported counts as
  missing. Archives merged before this are judged as one pool by the worst
  sample, with `generator_identity_unrecorded`.
- **Throughput.** An open model that misses its arrival target while its
  generators are healthy is an `open_model_shortfall` anomaly, not invalidity.
  A closed model whose throughput falls by more than 10% while latency rises by
  more than 10% at one steady level is a `closed_model_slowdown` anomaly: a
  system-under-test outcome, and the run stays valid.
- **Limitations** name evidence a rule could not use, such as throttle periods,
  dropped results or shed arrivals that the run did not record. Data-pool
  exhaustion is not recorded yet, so every verdict lists
  `data_exhaustion_unrecorded`.
- **Root-cause summary.** The verdict replaces the peak generator-CPU rule:
  `suspect` is a warning resource finding, `invalid` a critical one, and every
  verdict appears as the `Run validity` signal.
- **CI.** A CI run reports the verdict as `validity`. It stays advisory unless
  the project's Actions policy sets `validity_gate` to `invalid` or `suspect`;
  a run the gate fails is `run_invalid` (see
  [Run validity gate](CICD_INTEGRATION.md#run-validity-gate)).

Archived intervals are about one second long, so three consecutive intervals
is a short window; the evaluation corpus (WP3.9) recalibrates the rules under a
new version.

## Root-cause analysis budgets

The v2 root-cause engine (`pkg/rca`) runs every analysis inside a budget
(`rca.DefaultBudget`):

| Limit | Default |
| --- | --- |
| Series | 2,000 |
| Points per series (`MaxSeriesPoints`) | 10,000 |
| Exemplars | 20,000 |
| Graph nodes / edges | 5,000 / 20,000 |
| Wall time | 120 s |
| Heap growth | 512 MiB |

Exceeding a limit never fails the analysis and never cuts evidence silently:
the engine keeps what the limit allows and adds a `budget_truncated`
limitation naming the limit, for example `series: 2000 of 2500`. Wall time and
heap are checked before each stage; a stage that would start over budget is
skipped and reported the same way, while a stage that finishes over time keeps
its result and is not reported as truncated. A stage is not interrupted part
way: validity is linear (about 0.2 µs per resource sample), so it reaches the
wall limit only on inputs of hundreds of millions of rows. The heap limit
applies to the live heap's growth since the analysis started, measured
process-wide, so two concurrent analyses see each other's growth. Analysis
jobs run in the controller process, at most two at a time (see the
root-cause analysis service below); they do not take the native analytical
pipeline's single slot, whose limits govern DuckDB imports and queries.

An assessment judges acceptance (the run's SLA verdict) and validity, lists
the anomalies onset detection finds, builds the run's graph, ranks the
hypotheses that explain the anomalies and reports capacity (all below). Each
stage starts only within the wall-time and heap budget; a skipped stage is
listed as `stage_skipped`.

Baselines (Apple M4, Go 1.26.5, darwin/arm64, 2026-10-05, load average 4–6).
`BenchmarkRCAAnalyze` measures acceptance and validity, where a row is one
generator resource sample from ten generators at one steady level;
`BenchmarkRCADetect` measures onset detection over six hours of 10-second
buckets at one level, a quarter of the series stepping halfway:

| Benchmark | Time per analysis | Allocated | Allocations |
| --- | --- | --- | --- |
| Analyze, 10,000 rows | 0.9–2.1 ms | 0.67 MB | 73 |
| Analyze, 1,000,000 rows | 95–220 ms | 67 MB | 888 |
| Detect, 48 series × 2,160 buckets | 17 ms | 6 MB | 1,654 |
| Detect, 384 series × 2,160 buckets | 136–151 ms | 48 MB | 13,484 |

The upper analysis times are from a heavily loaded machine (load average
about 15). Allocation does not vary with load.

Rerun with `go test ./pkg/rca -run '^$' -bench BenchmarkRCA -benchtime 5x`.

## Root-cause onset detection

Onset detection (`pkg/rca/detect.go`, parameters `detect.v2` in
`detect_params.go`, recorded in each assessment's `detection` block) finds
where a series changed and whether it recovered. It reads only frozen
evidence.

- **Series.** Per transaction: the interval p95, error rate and throughput
  from `interval_distributions.jsonl`. Archives without that sidecar are
  rebuilt from their rows within bounds (`interval_distributions_reconstructed`).
  A run whose scripts authored no business transactions is judged on its
  actions too (`actions_judged`), beside the automatic per-iteration
  transaction the engine records under each script's name, so a slow request
  is named rather than only a slow iteration; an authored transaction takes
  precedence. A transaction absent from a complete interval completed
  nothing in it, so its throughput there is zero. Provider series come from the
  archived monitor points, one per measurement series identity
  (`analysis.MeasurementSeriesIdentity`, `apexload.series_identity.v1`) on the
  observation mode and grid the provider declares. Entity IDs are not part of
  the identity, except a provider's own series discriminator (Prometheus's
  label-set hash); a series collects them as graph nodes. A point recorded
  without a project belongs to the run's, and a point whose identity fields
  are incomplete keeps the earlier provider/source/metric/query/statistic/
  dimension key with no identity. Interval and snapshot providers are compared
  bucket to bucket. Counters and cumulative providers are excluded
  (`cumulative_series_excluded`), and points from providers that declare no
  observation semantics are skipped (`monitor_semantics_unknown`). Every
  transaction and action series also carries the identity of its MetricPoint
  projection; its readable ID is unchanged. Equal identities name the same
  measurement but never by themselves authorize a baseline comparison, which
  still needs a qualified baseline run.
- **Joint and imported series.** Joint interval evidence (version 3) becomes
  series on its exact `[action, network profile, hierarchy, phase]` tuple
  (`action_dimensions/<tuple>/p95_ms`, `error_rate`, `throughput`), measuring
  the tuple's action; intervals recorded without joint evidence are not zeros,
  and no slice is synthesized from marginals. A ready analysis dataset of the
  run's project whose run association names the run contributes `imported`
  series, one per metric and identity its partition manifest records over the
  run's span, read through one bounded native query each (at most 10,000
  points, the mean per bucket, a counter as its rate; histograms and
  summaries are not read). A dataset sampled more coarsely than the run's
  interval grid is regridded to its own interval, so detection still needs
  ten of those buckets before an onset. Imported series count as external
  evidence for hypotheses, like providers, and name their host as a node.
  Partitions that mix metrics or identities, and histogram or summary
  measures, are not enumerated (`imported_series_unenumerated`); empty
  partitions are skipped; native analysis that is off or unavailable for the
  project is `imported_dataset_unavailable`. Imported reads use at most a
  quarter of the analysis's wall-time budget and 128 native queries, so the
  run's own evidence is always judged (what they leave out is
  `detection_evidence_cut`). A read the single native-analysis slot refuses
  is retried with backoff, and concurrent analyses queue their reads in
  arrival order. A slot still busy at the read budget before any imported
  series was read fails the analysis, which an identical request retries,
  rather than completing it without that evidence; once some were read, what
  the budget leaves is `detection_evidence_cut`, and so is a read still
  running at the budget even when nothing was read (retrying cannot make it
  faster). Other admission failures (a build without native analysis,
  resource limits, an invalid resource configuration) stop the imported
  reads with one `imported_dataset_unavailable` limitation. A dataset is checked again after its reads and dropped
  (`imported_dataset_changed`) if it changed or lost its association
  meanwhile. Joint slices rank last within the series budget: imported and
  document series take the room they hold, and under the anomaly cap their
  anomalies never displace those of a series that can be a symptom or cause.
- **Provider series IDs** pair a run's series with a baseline run's. They
  leave out the scenario the run was launched from (the series' identity keeps
  it), so a copied scenario or an ad-hoc run still pairs. They keep the host
  that collected the series, so a provider source moved between collector and
  direct collection, or to another collector, starts new series that do not
  pair with the baseline's; a provider's own
  series discriminator (Prometheus's label-set hash) is part of both, so
  series that the allowed labels alone would merge stay apart.
- **Browser document series.** Each measurement tuple of the run's final
  FCP, LCP, CLS and INP document measurements (one algorithm, unit,
  lifecycle phase, frame scope, engine, device profile and actual runtime
  cohort, within one group and region) becomes a `browser_document` series
  (`browser_document/<metric>/<tuple digest>/p75`): the p75 of the final
  measurements whose window closed in each bucket of the run's interval
  grid. Each measurement counts once, at its latest retained state;
  provisional and censored measurements never enter, and a bucket without a
  final document is a gap. The tuple is part of the measurement identity, so
  a baseline-run judgment pairs only the same tuple. Layout shift (`score`)
  is judged on a robust scale never below 0.01 (`score_floor`), so changes
  well below it build no onset. Incomplete document evidence is
  `detection_evidence_cut`.
- **Levels.** Only buckets inside a steady load level are judged: the same
  settled spans validity and baseline comparison use, which skip each level's
  first 30 s. Ramps and pauses are never judged; a run with no steady level
  gets `load_levels_unavailable` and no anomalies. Levels whose load is within
  5% are the same level.
- **Two baselines.** A `time` anomaly compares each bucket with earlier buckets
  at the same load in this run. The baseline is the level's first ten buckets,
  growing to 120 while nothing is alarming, so a slow drift is measured against
  where the level began; buckets from an onset on never join it. A `load`
  anomaly compares the level with the baseline run's buckets at the same level,
  when `baseline_test_id` names a run whose workload qualification is not
  `unqualified`. A level with fewer than ten baseline buckets is not judged
  (`insufficient_baseline`).
- **Statistic.** Robust z = (x − median) / max(1.4826 × MAD, 5% of the median,
  unit floor): 5 ms of latency, 0.5 points of error rate, 0.1 requests per
  second of throughput, 0.01 of layout shift score (`score_floor`), and for
  providers the unit's latency or percentage floor. Two-sided CUSUM with k = 0.5 and h = 5. An onset needs three
  consecutive buckets beyond k while the alarm is on, so noise just before a
  change cannot complete it. The onset is the maximum-likelihood change point
  since the CUSUM last left zero. Its uncertainty spans every change point
  within two log-likelihood units, plus one bucket and the series' clock
  uncertainty (WP2.2). Three in-band buckets end an anomaly as `recovered`. A
  single bucket that alone reaches the alarm, with an in-band bucket after it,
  is a spike: listed under `detection.spikes`, never an onset. A gap of more
  than three buckets resets the CUSUM.
- **Anomalies** carry a stable ID, series, onset, end, uncertainty, scope,
  direction, level, effect (median minus baseline median, in the series' unit),
  peak z, baseline and an evidence reference to the series window. At most 500
  anomalies and 200 spikes are kept, the largest first (`anomalies_truncated`,
  `spikes_truncated`).
- **Evidence limits.** A sidecar that cannot be read costs its series, not
  the analysis (`detection_evidence_unreadable`). Transaction series take the
  series budget first, provider series then, imported and browser document
  series and joint action-dimension slices what remains (joint slices last); what the budget or the analysis budget
  leaves out is `detection_evidence_cut`. A baseline run
  none of whose series matches one of the run's is `baseline_series_unmatched`,
  and the limits of the baseline's own evidence are listed in its `baseline`
  coverage.
- **Live findings.** The run's live 3σ findings stay as they are, labelled
  "advisory (live)" in the run summary.

Measured on synthetic 10-second series with noise of one standard deviation,
500 trials each: a step of 3σ is placed exactly in 82% of trials and within
one bucket in 94%; 5σ, 97% and 99%; 10σ, 99.6%. Healthy one-hour series
produce about 0.24 false onsets per series-hour. That is inherent to k = 0.5 and
h = 5, which alarm about once per 465 buckets even with a perfect baseline.
Raising h to 8 would cut false onsets to about 0.03 while missing no more 3σ
steps (about 2% at either setting).
The fault corpus (WP3.9) calibrates these values; until then they are the
plan's starting values.

## Root-cause graph

Each assessment carries the run's typed graph (`graph`, schema
`apexload.rca_graph.v1`, built by `pkg/rca/graph.go`) from frozen evidence:
the archived rows, the monitor jobs' archived topology, the archived monitor
points and the mapping decisions frozen into the evidence revision.

| Node | From |
| --- | --- |
| `transaction`, `action` | Archived rows; IDs match the detection series (`transaction:<name>`, `action:<name>`) |
| `dependency` | The dependency type and target an action reached, with the target's host |
| `monitor_source` | Each monitor job bound to the run |
| `provider_entity` | Dynatrace entities (classic and Smartscape), AppDynamics tiers, nodes and business transactions |
| `infrastructure` | Hosts, processes, containers, pods, nodes and queues, from provider entities or monitor point dimensions |

| Edge | Meaning | Confidence |
| --- | --- | --- |
| `calls` | An action reached a dependency; a provider entity calls another (Smartscape, classic `calls`) | explicit (rows) or provider |
| `contains` | A transaction encloses an action; a tier its nodes; an entity its instances | explicit or provider |
| `shares_infrastructure` | An entity runs on an infrastructure node; entities on the same node share it | provider |
| `monitors` | A monitor source measured an entity or infrastructure node | provider |
| `maps_to` | A dependency is a provider entity | explicit (approved) or inferred |

- **Approval.** A dependency whose host is a provider entity's name (including
  provider-reported hosts and processes) gets an inferred `maps_to` edge with
  `used: false`: listed, never walked, and counted in
  `graph_unapproved_mappings`. Hosts and pods seen only in point dimensions
  are not candidates, since no decision can name them. Several candidates are
  all kept and marked `ambiguous` (`graph_ambiguous_mappings`). An operator
  records a decision as an `rca_mapping` definition at
  `/api/v1/analysis/rca-mappings?project_id=…` (GET, POST; GET, PUT and
  DELETE by ID; editor to write, viewer to read; gated by
  `APEXLOAD_RCA_V2_PROJECTS`) with `{dependency, provider, entity_id,
  entity_name, decision}`, where `dependency` is the dependency label as the
  graph shows it and `decision` is `approved` or `rejected`. Approved
  decisions become explicit edges carrying the definition's ID and
  revision; rejected ones remove the candidate. Decisions are revisioned,
  apply to every run of the project, and have their own allowance of 1,000
  per project apart from saved mappings and comparisons. Dynatrace entities
  collected through Smartscape also answer to their classic IDs, which points
  and classic relationships use.
- **Freezing.** An evidence revision freezes the project's decisions and
  their digest, which is the analysis's `mapping` input. A decision changed
  after the latest revision makes the next POST capture a new one, so the
  same request is a new analysis.
- **Cycles** among used `calls` edges are found (Tarjan) and listed under
  `graph.cycles`; they are allowed.
- **Bounds.** The graph keeps 5,000 nodes and 20,000 edges (the run's own
  nodes first, explicit edges before provider and inferred ones) and at most
  1 MiB; a run contributes at most 20,000 distinct calls and 1,000 observed
  entities and infrastructure nodes per source (`graph_truncated`,
  `graph_evidence_cut`). The bundle drops unused edges first.

## Root-cause hypotheses and capacity

Hypotheses (`pkg/rca/hypotheses.go`, signatures `signatures.v1` in
`pkg/rca/signatures/`, one file each) explain symptoms: rising latency or
errors, or falling throughput, on a transaction or action series.

- **Evidence kinds.** Provider series are classified by keywords in their
  metric names (`signatures.Classify`): CPU, memory, GC, pool, consumer lag,
  rebalances, dead letters, retries, scaling (replicas, restarts, OOM kills,
  cold starts), database wait, and the queue depth, arrival rate and
  residence time Little's law reads. The run's own DNS, connect and TLS time
  per dependency, averaged per interval over the requests that spent any,
  become `dependency/<label>/dns_ms`, `connect_ms` and `tls_ms` series and are
  detected like any other; the baseline run's are built the same way, so
  they are compared at the same level too.
- **Window and path.** For each symptom, the anomalies of the same scope that
  began within five minutes before its onset and before its end are
  candidates. The graph is walked from the symptom's transaction or action
  through used `calls`, `contains`, `maps_to` and `shares_infrastructure`
  edges (eight hops at most); evidence on a reached node, or on one of its
  aliases, is connected. Only series detection judged in the symptom's scope
  count as measured: a series it cut, excluded or could not baseline says
  nothing by staying quiet.
- **Ordering.** A cause precedes a symptom only when the symptom began later
  by more than both onsets' uncertainty; otherwise they are unordered, and a
  cause that began later by more than that contradicts. An onset within its
  uncertainty of a level's settle period is `load_coincident`.
- **Signatures.** `resource_pressure`, `garbage_collection`,
  `pool_exhaustion`, `consumer_lag`, `rebalance`, `dead_letters`, `retries`,
  `cold_start`, `database_wait` and `network_phase`. Each names its required
  evidence kinds, the indicators that support it, its competitors and its
  falsification check. With none of its required kinds measured it lacks
  evidence, and one that cannot explain the symptom's kind is not
  applicable. Each indicator that moved the right way no later than the
  symptom adds weight: more for a strong anomaly, half when unconnected, 0.6
  when unordered. One that followed the symptom subtracts 0.5. When nothing
  supports the signature, each indicator measured on the path that stayed in
  band subtracts 1 (0.25 off the path). A signature is supported with any
  support and a positive score. A cold start scores higher when the symptom
  began at a load step.
- **Hypotheses** carry the symptoms they explain (one cause behind several
  symptoms, or seen both over time and against the baseline, is one
  hypothesis: the same signature on the same evidence series), score, rank,
  chain of onsets, the supporting
  and contradicting evidence, the other supported signatures for the same
  symptoms, the falsification check, limitations naming competitors that
  could not be judged, and the graph nodes the supporting evidence measured.
  Confidence is `high` when connected, preceding and uncontradicted;
  `medium` when connected; `low` otherwise, with the reasons. Validity caps
  it: an invalid run's hypotheses are all `low`, and a suspect or unknown
  run's at most `medium`, because its symptoms may come from the load
  generators (`validity_caps_confidence`). The 100 strongest are kept, best
  first.
- **Outcome.** `supported` with any hypothesis; `insufficient_evidence` when
  detection could not run (no steady level, or no budget left) or no
  signature's evidence was measured (`signatures_unmeasured`);
  `no_supported_hypothesis` otherwise, including a run whose detection found
  no symptom (`no_symptoms`).

Capacity is reported beside the hypotheses:

- **Knee.** For each transaction or action latency series measured at three
  or more steady loads, the median p95 per load is fitted with two lines
  joined at an interior level, choosing the join with the least squared
  error. The join is the knee when the second slope is positive, at least
  three times the first, and raises p95 by more than 5 ms; otherwise the
  result says why there is none.
- **Little's law** applies only at a broker queue (a queue named in the
  series' dimensions, or else the provider entity they name) whose depth, arrival rate
  (per second) and residence time (a time unit) were all measured, in each
  steady level with at least three points of each and a depth trend that
  moves it by no more than 10% of its mean (or one message). It reports the
  time-average depth against arrival rate × residence time and their ratio.
  Any other queue or level is `eligible: false` with the reason.

## Root-cause evaluation

The engine is calibrated and judged on a labelled corpus in
[`docs/qa/rca-eval`](qa/rca-eval/README.md) (WP3.9). Each trial names its run,
fault class, fault window and the signature that should rank first; healthy
controls name none. A trial's seed fixes its split: 30% calibrate, 70% are
held out, decided by the seed's hash before any tuning (`rca.EvalSplit`).
`apexctl rca eval --corpus DIR` scores a split's assessments. It reports
top-1 and top-3 recovery, unsupported high-confidence conclusions, false
attribution on controls, abstention, per-class recall and onset error, each
rate with a Wilson 95% interval. It also checks the release bars (top-1
≥ 80%, top-3 ≥ 90%, no unsupported high-confidence conclusion, false
attribution ≤ 5%) and the corpus minimums (8 fault classes × 20 trials, 100
controls). `--replay` first re-analyzes each trial's frozen archive with the
controller's current engine; `--require-bars`, held-out split only, fails
unless the corpus is ready and every bar is met. The default split is calibration, so held-out
labels are scored only when asked for. No corpus has been collected yet, so
the detection, signature and capacity parameters above remain the plan's
starting values.

## Root-cause analysis service

v2 root-cause analyses run as revisioned jobs over a run's archive.
`APEXLOAD_RCA_V2_PROJECTS` lets a project compute them; saved analyses stay
readable when the project leaves that list. `APEXLOAD_RCA_V2_PRESENT_PROJECTS`
marks the projects whose Summary, Diagnostics and reports present v2 (each
response carries `presented`); v1 routes, explain, AI root-cause and the v1
MCP tools keep returning v1.

| Route | Access | Returns |
| --- | --- | --- |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses` | Editor | 202 with the queued analysis; body `{enrich, baseline_test_id}`. Identical frozen inputs return the existing analysis with 200 |
| `GET /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}` | Viewer | State (`queued`, `running`, `completed`, `failed`, `cancelled`), inputs and, once completed, the `apexload.root_cause.v2` assessment |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/cancel` | Editor | The cancelled analysis; 409 when it already ended |
| `GET /api/v1/tests/{id}/archive/root-cause/v2` | Viewer | The latest completed analysis, or `{"state":"none"}` |
| `GET /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/bundle` | Viewer | `apexload.rca_bundle.v2`: the assessment (with its coverage and limitations), the evidence excerpts its reference IDs name and the evidence revision's inventory, at most 256 KiB (`truncated` when something was cut) |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/narration-check` | Viewer | Whether a narrative `{narrative}` stays within the analysis's bundle (see Agents and critiques) |
| `GET /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/critiques` | Viewer | The analysis's critiques, oldest first |
| `POST /api/v1/tests/{id}/archive/root-cause/analyses/{analysis_id}/critiques` | Editor | 201 with the stored critique; 422 with the check when the narrative fails it |

- **Stored size.** An assessment larger than the 2 MiB stored limit is cut,
  not failed. The least load-bearing parts go first: validity windows,
  spikes, capacity results, the graph's unused and weakest edges and uncited
  nodes, anomalies no hypothesis cites, and then the lowest-ranked
  hypotheses. A kept hypothesis's anomalies and nodes stay. The cut is
  listed as `assessment_trimmed`. The 256 KiB bundle is cut the same way
  before its evidence and SLA details.
- **Frozen inputs.** An analysis's `input_digest` hashes the archive's
  manifest digest, the evidence revision, the baseline archive's digest, the
  digest of the mapping decisions frozen into the evidence revision, the
  policy revision (none yet), the signature set and the engine version. The
  engine version (`rca.EngineVersion`) carries the rules', parameters' and
  signatures' versions and a fingerprint of every value the stages use: the
  budget, detection parameters, validity rules, hypothesis, graph and
  capacity bounds, signature weights and the classifier, followed by the
  archive adapter's version (how the archive is read into series, levels and
  evidence; `archive.3` keys provider series by measurement identity, gives
  every series an identity, adds joint, imported and browser document series,
  and reads unscored telemetry as unknown rather than healthy). Changing a value makes a new analysis; a change to
  a stage's logic bumps the engine's base version, and a change to how the
  archive is read bumps the adapter's. A POST
  whose digest matches a queued, running or completed analysis returns it;
  a failed or cancelled one can be retried. A run keeps at most 50
  analyses and 50 evidence revisions.
- **Evidence revisions** freeze the archive's evidence sidecars, the run's
  monitor sources with their outcomes, the project's mapping decisions and the
  associated analysis datasets (at most eight, by ID; `imported_datasets_truncated`
  beyond that, or when the project's dataset listing exceeds the 2,000 rows
  scanned; the listing pages by offset, so a dataset can be missed by a
  capture during which another of the project's datasets is deleted, and is
  picked up by the next), each with its revision, mapping SHA-256 and partition-manifest
  digest. An analysis reads a dataset only while all three still match; one
  that changed or lost its association afterwards is
  `imported_dataset_changed` and is not read. A POST captures a revision when
  the run has none, when the archive, the mapping decisions or the associated
  datasets changed since the latest, or when `enrich` is true.
  `APEXLOAD_RCA_AUTO_ENRICH_PROJECTS` captures an enriched revision at archive
  finalize. Provider evidence pulls (topology and diagnostics) arrive with the
  per-run graph and signatures (WP3.7–WP3.8); until then enrichment records
  the same inventory.
- **Jobs** run on the execution leader, at most two at a time per controller,
  inside the engine's budgets. A job holds the archive's materialization
  lease while it reads; an analysis whose archive changed after the request
  fails rather than judging different evidence. A claimed job holds a
  five-minute lease, renewed while it runs; if its controller stalls, shuts
  down or loses leadership, another takes it over once the lease expires,
  and the stalled owner's late result is rejected, so each analysis records
  one outcome. A job that panics fails instead of being retried.
  Cancellation stops the running job, on another controller once its next
  renewal finds the analysis cancelled, and discards its result.
- **Retention.** Analyses and evidence revisions are deleted with their run,
  their project, or the run's archive.
- **Baselines.** `baseline_test_id` names an archived run in the same
  project. Its workload qualification against the run appears as `baseline`
  coverage. Unless it is `unqualified`, onset detection compares each load
  level with the baseline's same level.
- **Investigation.** Where a project presents v2, the run report's
  Diagnostics area leads with a Root cause panel, linked from Summary:
  outcome, acceptance, validity, the ranked hypotheses with their confidence
  reasons, evidence chain, contradicting evidence, competing hypotheses and
  the check that would disprove each, then anomalies, coverage and
  limitations. Editors can re-analyze; a comparison baseline open in the
  report is sent and named. Investigating a hypothesis opens one timeline
  (anomaly windows, onset uncertainty bands, the archived load-phase
  events, suspect and invalid spans, throughput and p95 drawn at interval
  starts) with the symptom's transaction population, exemplars with trace
  links, the cause evidence and the matching error patterns. A run whose
  archived phase events mark no load level (one that held a single level
  archives only its stop) shows load steps inferred from the detection
  levels, marked as inferred, beside any archived pause, resume or stop. When
  the run's archive changed after the analysis read it, as late monitoring
  evidence does, the panel and the report say so. The report URL carries `analysis`, `rev`,
  `hypothesis` and `window`, so a shared link reopens the same view.
- **Reports.** The report section `rca` renders the generated root cause in
  the dashboard's report preview, HTML, PDF, Word, CSV and the analysis ZIP
  from one model (`root_cause_view` on the report JSON): the latest
  completed v2 assessment where the project presents v2, otherwise the v1
  summary, with a note when v2 is presented but no analysis completed. It
  is separate from the Findings section, which holds analyst-curated or
  derived findings, and never carries run annotations.
- **Evidence excerpts.** Each analysis stores, beside its assessment, one
  excerpt per anomaly evidence reference: the judged series' points from
  three intervals before the onset's earliest time to one interval after the
  anomaly's end, at most 24 (spread evenly, marked `sampled`), keyed by the
  reference ID hypotheses cite; analyses made before excerpts existed have
  none until re-analyzed. Excerpts no hypothesis cites are cut first
  when an assessment is trimmed, and an excerpt goes with its anomaly. The
  bundle is cut in this order: an evidence inventory over 16 KiB, uncited
  excerpts, the assessment as stored assessments are cut, the inventory,
  the remaining excerpts, and last the SLA details.
- **Agents and critiques.** An agent narrates an analysis from its bundle.
  The narration check rejects a narrative that cites a reference ID
  (`anomaly-…`, `hypothesis-…`, `evidence-…` or the analysis ID) the bundle
  does not hold, a time or date no bundle time matches at the precision
  written, or a number no bundle number rounds to at the precision written.
  Bundle numbers count in their own unit and as nanoseconds in ms, s or min,
  milliseconds in s, and fractions in percent; integers up to ten count
  things and pass; a number joined to a word (`p95`) names something and is
  not checked. A critique is a narrative that passed the check, stored apart
  from the analysis with its author kind (`agent` or `analyst`), model and
  author, at most 100 per analysis; it never changes the assessment and
  goes with its run. External LLM wiring stays disabled: the explain
  provider remains `deterministic-local`.
- **Clients.** `apexctl rca analyze --id RUN [--enrich] [--baseline-id RUN]`,
  `apexctl rca status --id RUN --analysis-id ID`, `apexctl rca show --id RUN`
  and `apexctl rca bundle --id RUN --analysis-id ID`, and `apexctl rca eval`
  for the evaluation corpus; MCP tools
  `run_rca_analyze`, `run_rca_status`, `run_rca_assessment`,
  `run_rca_bundle`, `run_rca_check_narration`, `run_rca_critiques` and
  `run_rca_critique_add`.

Schema release 40 (`rca-analysis-revisions`) adds `rca_evidence_revisions`
and `rca_analyses`; release 42 (`rca-critiques`) adds `rca_critiques`.

## Business keys

A business key is a value a script extracts that also appears in the system
under test's logs: an order ID, a billing account number (BAN). A business
key lookup finds a run's requests and the matching log entries for values an
operator types in. Projects opt in with `APEXLOAD_BUSINESS_KEY_PROJECTS`.

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

## Preparation capacity policies

Preparation accepts an immutable `capacity` object. `selection: existing`
reserves available generators without a provider mutation. `selection: ephemeral`
requires an approved asynchronous launch-capable pool; unsupported local/static
or Kubernetes targets fail before preparation starts. Azure Apps/Jobs/Instances
and ECS reuse their durable launch intent and cancellation paths.

`spot` is `off`, `preferred`, or `required`. ECS spot must be approved by a target
runtime profile. `on_demand_floor` and `spot_ceiling_percent` bound the exact
integer allocation. Required spot fails when unsupported or rounded to zero.
Preferred uses on-demand where spot is unsupported; it does not fall back across
clouds or regions, or retry failed provider spot launches as on-demand. Standing
services are not scaled by ephemeral preparation. Once consumed, existing
archive-terminal teardown fences govern cleanup. Standalone provisioning retries
of run-owned requests are refused because they would lose execution ownership.

Equivalent CLI flags are `--capacity`, `--spot`, `--on-demand-floor`, and
`--spot-ceiling` on `apexctl prepare create`.

OTLP receiver buffering is bounded to 512 samples per approved stream and five
minutes. The receiver returns explicit partial rejection for unsupported units,
semantics, timestamps or dimensions, and HTTP 429 when full. Late arrivals within
this retention period remain collectable. Per-run accepted sample identities and
pending batches survive collector restart through the spool. The passive receiver acknowledges HTTP ingestion only after its protected local
journal has been replaced and synced. It refuses ingestion until the collector
holds its spool ownership lock and has recovered that journal. Five-minute
retention still applies; this is not an OTLP archival gateway.

Dependency monitor links now carry the selected UTC interval into the overlay.
A bounded receipt scan reports `scan_limited` rather than claiming absent historical
evidence is zero; mixed valid and invalid observations report incomplete coverage.

Use `apexctl monitor dependencies --id RUN --at 2026-01-01T00:00:00Z` to inspect
the same qualified dependency interval through the CLI (omit `--at` for latest).

## Approved cloud metric collectors

`cloudwatch` and `azure-monitor` are opt-in signed-job packs. Their source destinations are local aliases (`cloudwatch://orders`, `azure-monitor://orders`), with polling intervals of at least 60 seconds. No controller-provided URL, SQL, expression or secret can change the approved query. They use the collector's cloud identity, not sealed password bundles. Configure a persistent spool directory as for other run jobs.

Example entry in the operator-owned adapter-targets file:

```json
{
  "pack": "cloudwatch",
  "destination": "cloudwatch://orders",
  "cloud": {
    "region": "us-east-1",
    "account_id": "123456789012",
    "namespace": "AWS/EC2",
    "metrics": [{"name":"CPUUtilization","unit":"Percent","statistic":"Average","dimensions":{"InstanceId":"i-approved"}}]
  }
}
```

The file is an array of entries. AWS identity is checked against the approved account using STS before CloudWatch use; service endpoints are fixed to commercial AWS regional endpoints. Ambient AWS credentials must belong to the collector's approved role, with permission to read only the necessary metrics.

Azure entries use `pack: azure-monitor`, an `azure-monitor://name` alias, and `cloud` fields `tenant_id`, `client_id`, `identity` (`workload` or `managed`), `resource_id`, `namespace`, and `metrics`. Resource IDs must be full subscription/resource-group/provider paths. The adapter uses the approved user-assigned managed identity or workload identity and the fixed Azure Resource Manager endpoint. The first slice accepts resource totals; dimensional Azure series are rejected. Configure resource-scoped read access externally.

Each target permits eight explicitly named metrics, fixed one-minute periods and a five-minute lookback after execution starts. Supported statistics are Average, Minimum, Maximum and Sum (Azure Total); percentiles and arbitrary expressions are rejected. Values retain summary semantics and statistic/period dimensions. Seconds normalize to milliseconds; supported units also include percent, count, bytes and their per-second rates. Missing points are never filled with zero. Provider-null buckets remain absent and can acquire late numeric observations during the five-minute lookback. Acknowledged identities are retained in the durable spool so overlapping polls do not duplicate observations; accepted historical values are immutable.

Requests use bounded deadlines, at most three attempts, a 1 MiB response limit, and no redirects. Incomplete, paginated, duplicate-timestamp or incompatible-unit results fail visibly. Deterministic adapter tests are implemented; live AWS/Azure acceptance is still required before advertising production readiness. API contracts: [CloudWatch GetMetricData](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricData.html), [Azure Monitor Metrics](https://learn.microsoft.com/en-us/rest/api/monitor/metrics/list?view=rest-monitor-2023-10-01).

## Response properties and native panels

In runtime settings, explicitly promote up to four non-sensitive extraction keys with 1–16 approved category values each. The equivalent script field is `config.results.property_analytics`:

```json
[{"name":"operation","extraction":"operation_type","values":["checkout","search"]}]
```

Promotion occurs after result redaction. Unlisted values become `other`; absent extractions become `missing`. Raw arbitrary values never become property labels. Names, values and extraction keys are bounded; credential and personal-identifier names are rejected. Generators aggregate action observations, retain mergeable distributions, and require the `property_analytics_v1` capability for distributed execution. Each observation can contribute to several properties; counts across properties must not be added into a run total.

Live summaries expose `properties`; archive reports recompute `by_property` from action observations. JSONL and Parquet retain `property_dimensions`. Existing archive transaction queries accept paired `property_name` and `property_value` exact filters; use the action population when reconciling with live property counts. Old archives without promotion evidence remain unavailable. Property comparisons across script revisions must use the same promotion definitions.

Run pages now offer native project panel creation with line, latest-value and table views, using the retained run time window. Supported run measurements include TPS, response-time means/interval percentiles, errors, VUsers and process resources. Project viewers can reopen saved panels. Missing intervals remain gaps, interval percentiles are never averaged, and unsupported stored query dimensions/filters/aggregations are explicitly unavailable. Archived action/transaction and property filtering is described below. General dimensions and monitor/imported-dataset overlay authoring remain follow-on work.


## Filtered archive panels and comparisons

In a run's **Create project analytics panel** form, choose **Archived actions** or **Archived business transactions**. Select a case-sensitive exact action/transaction name, an optional retained approved property category (actions only), elapsed start/end seconds and bucket width. An optional comparison run ID saves a two-run query with the same selection and elapsed wall-time window for both runs. Editors save project definitions; viewers can reopen and query them. Existing run-total definitions keep their previous semantics.

The first slice supports raw observation counts, error percentages and response-time mean/min/max/P50/P90/P95/P99. Percentiles use nearest rank over raw observations within each bucket, never averages of stored percentiles. Empty buckets are missing, including count buckets; they do not assert that zero traffic occurred. Missing retained properties and legacy observations whose population cannot be established produce explicit warnings. Property selection matches the retained finite category, including `other` or `missing` when those were actually recorded.

Queries require a completed archive and recorded execution start. They allow a window within the first 24 elapsed hours, at most 1,000 buckets and 100,000 selected raw observations. Existing archive scan record/byte limits also apply, with a 30-second query deadline and shared analytics admission. Oversized or busy queries fail explicitly; narrow the selection or retry. This slice reads the canonical JSONL transaction evidence and does not require DuckDB. Summary-only archives cannot supply raw percentiles.

Click a chart bucket or open **Inspect time buckets** to inspect current/baseline observations. The paginated inspector reuses the authorized archive transaction endpoint and redacted snapshot preview components. Exact identity/property/population filters and inclusive-start/exclusive-end UTC bounds match the plotted bucket. Request and response bodies remain subject to existing capture and disclosure policies.

Comparisons require both runs to belong to the same project. Results show added/removed identities and compatibility warnings when execution fingerprints are missing/different or evidence limitations exist. Values remain separate; no automatic regression verdict is inferred from an unqualified pair. This original comparison workflow remains archive-only. The grouped/live workflow below adds bounded dimension selection and monitor/imported overlays. Live SLA actions remain separate.


## Live and grouped evidence panels

Choose **Filtered live intervals** or **Grouped archive and overlays** in the native panel form. Saved selections remain project-scoped and revisioned. Group by action/transaction identity, a retained property name, region, or dependency; choose 1–20 series. Archive grouping scans raw observations once with the same 100,000-observation and 1,000-bucket bounds. Exceeding the selected series cap fails rather than silently discarding groups.

Live panels refresh every ten seconds after the preceding request completes. They read the newest 1,000 retained publication intervals within the saved elapsed window. Action, business-transaction, property, region and dependency summaries are retained in the existing bounded history, persisted in an optional versioned `native_evidence_json` column, and restored after restart. Detailed evidence is queried separately from the metrics WebSocket feed. Each dimension retains at most 128 identities; incomplete intervals remain unavailable. A generator's additive native-evidence version marks support; mixed peers and source-plan runtimes without the required summaries cannot claim complete evidence.

Completed archives also carry these distributions in `interval_distributions.jsonl` (`apexload.interval_distribution.v1`), so they outlive history retention and travel with exported archives. Finalization copies every retained interval with version 2 or later native evidence: per dimension and key, the count, errors, the t-digest in nanoseconds, the percentiles live showed and the incomplete flag. Runs without such evidence are rebuilt from one scan of the archived transaction rows, per transaction and action, within 100,000 observations, 1,000 intervals and 128 keys per dimension, and marked `reconstructed_from_rows`. The manifest records `interval_distributions_file`, its count and source, and `interval_distributions_truncated` when a bound, skipped interval or the sidecar's byte allowance cut it short. The sidecar is written once; later metadata refreshes keep it. Merge digests across intervals; never average the percentiles.

Live percentiles describe their original publication summaries and are never averaged or re-bucketed. The configured bucket width applies to archive and overlay queries. Live regions describe business transactions; dependencies and properties describe actions. Independent live dimension summaries cannot answer intersections: action/property filtering requires version 2 merged evidence; action/network/hierarchy/lifecycle filtering requires version 3 merged evidence. Unsupported combinations and unavailable peers return explicit errors. History eviction, old rows and missing observations cannot be interpreted as zeros. Selected live elapsed windows are fixed, not automatically sliding.

For an overlay, select a run monitor source or an imported dataset, its metric and aggregation, and optionally a monitor host. Imported datasets must be ready, project-authorized and explicitly associated with this run; association is checked before analytical reads. Monitor and dataset project flags still apply, and imported queries require the native DuckDB profile. A metric must have a known unit; the existing analytical query layer rejects mixed or incompatible series. Different measurements use separately labelled axes, with no implied numerical equivalence. Missing/stale/invalid-quality values remain gaps. The table and CSV preserve original values and units.

Archive panels use the shared lag-correlation implementation on aligned buckets. Results include paired observations, coverage and an optional coefficient; insufficient or constant evidence stays unscored. Live source mode displays publication intervals without resampling. Optional `live_bucket_mode: merged` enables selectable completion-cohort buckets and lag correlation; it requires version 2 native evidence with complete duration distributions from every contributing runtime. A positive lag means overlay evidence follows the selected series. Association does not establish causation. Cross-run baselines still use the existing archive comparison workflow; the new grouped workflow does not invent regression verdicts.

### Merged live publication buckets

Select **Merge live intervals into buckets** for counts, weighted averages, errors and percentiles computed from mergeable distributions. A bucket contains whole source publications ending in `(from, to]`; distributions cannot be split, so boundary attribution is approximate at source-window resolution. Source mode remains the default and historical summary-only evidence is never converted into a distribution.

Action/property intersections use bounded joint evidence (128 identities per interval), rather than intersecting unrelated marginals. Queries retain the 20-series and 1,000-bucket limits and the latest 1,000 publications. Distributed source publications are deduplicated by source, execution generation and sequence. Missing expected sources, drops and malformed distributions remain missing. Collection coverage describes source windows separately from correlation pair coverage. Correlation is association, not proof of causation.

### Dimensions, timeline annotations and transaction investigation

Archive panels can filter and group by network profile, hierarchy and lifecycle phase, retaining existing action/property/region/dependency options. Network profile is also available as an independent live action marginal. Merged live action evidence version 3 supports hierarchy and lifecycle grouping, and exact action/network-profile/hierarchy/phase intersections. These tuples retain at most 128 identities per publication, each with a mergeable duration distribution. Unretained combinations (including property with hierarchy) fail explicitly, as do missing selected labels; older peers remain unavailable for these queries. Dimension filters exclude missing legacy values; missing is never zero.

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
`insufficient_coverage` or `insufficient_valid_buckets`. Evidence queries
(`POST /api/v1/tests/{id}/native-series`) select the action or transaction
population only; message and browser document populations are
archive panels. Responses name the catalog operation even when an alias was
asked for. A controller that predates derivations ignores the field: during a
rolling upgrade a saved derivation can be dropped by an older peer that
re-saves the dashboard, and a derived query answered by one returns raw
values without a `derivation` echo.

Each derived bucket carries its value or a `reason`: `zero_denominator`, `incomplete_evidence`, `numerator_not_subset`, `missing_bucket`, `missing_baseline_bucket`, `baseline_not_comparable`, `insufficient_window`, `insufficient_valid_buckets`, `insufficient_coverage`, `no_prior_sample`, `missing_counter_sample`, `counter_reset`, `duplicate_counter_instant`, `invalid_counter_sample`, `counter_epoch_changed_or_unknown`, `evidence_unavailable` and the rest of the typed functions' reasons. A complete window with no events is a measured zero for counts and rates but an unscored zero denominator for a ratio (a merged live bucket is complete only when some source published into it, and a count bucket only when publications cover its whole window); a bucket past run completion, with legacy population or missing property evidence, is incomplete. A baseline derivation compares only a comparable baseline run (the same comparison fingerprint and complete evidence); otherwise every bucket is `baseline_not_comparable`. Responses name the applied `derivation` and the derived unit (`ratio`, `percent` or the series' canonical unit); a query without a derivation renders exactly as before.

The archive `message` population reads received messages from messaging v2 evidence (`message_count`, optionally filtered by `source`). A message ratio divides the messages its counter counted by the messages that counter observed, and a bucket is complete only when every selected dimension carried complete v2 evidence and the counter observed every eligible message; a bucket described only by v1 summaries stays missing. Duplicate ratios are lower bounds within each consumer's local sequence window. Imported counters use one epoch per dataset revision and identity; a bucket in which a reading falls below the previous one (inside it or across from an earlier bucket) is a `counter_reset`, and one instant with two different readings is a `duplicate_counter_instant`; the bucket after a missing one is a `missing_counter_sample`. Moving statistics over an imported series derive the bucketed series itself (a counter's rate, a histogram or summary percentile), and imported metric names are the dataset's own, never matched against the native catalog. Moving derivations on live per-source intervals, whose windows are not aligned, are refused.

Saved dashboards validate through the catalog: catalog metrics and aggregations, the retained grouping and filter dimensions, and an alias table that keeps saved names meaning what they did (`tps` is `observation_rate`, `transaction_duration` is `response_time`, and live display cards such as `active_vusers`, `throughput`, `regional_activity`, `resource_cpu` and `resource_memory` keep rendering but are not catalog measurements and take no derivation; `stddev` remains a display aggregation). The catalog also declares `message_count` (population `message`; it takes moving statistics, and saved message panels may filter by `source`) and `bytes_sent`/`bytes_received` (population `byte`); bytes have no native query and declare no derivation yet. Browser document metrics `document_fcp`, `document_lcp`, `document_cls` (unit `score`) and `document_inp` (population `browser_document`, aggregations `p50`, `p75`, `p90`, `p95` and `p99`; each bucket's `count` is its final documents) read one measurement tuple's final document distribution; their filters (`engine`, `device_profile`, `frame_scope`, `lifecycle_phase`, `group`, `region`, `cohort`) apply to these metrics only, and a selection spanning more than one tuple is refused. See [Browser document evidence](guides/BROWSER_DOCUMENT_EVIDENCE.md#comparison-and-native-analysis).

## Event Hubs processor progress analysis

The live and completed-run diagnostics retain a standalone processor progress snapshot even when an interval has no actions or business transactions. SQLite/PostgreSQL history reload now preserves both messaging action summaries and processor snapshots; old nullable rows remain missing. JSONL/Parquet archives and reports retain the same bounded evidence. The live read can recover the last processor snapshot after unrelated intervals while it remains in bounded resident history; archives retain final evidence.

Use `apexctl analysis progress --id RUN_ID --mode live`, or `--mode archive` for a completed archive. Optional `--group` and `--source-id` select exact retained identities. The equivalent authorized read is `GET /api/v1/tests/{id}/messaging-progress`; see the generated [API contract](swagger.yaml). This operation admits fixed live/archive modes and bounded identity filters, with shared analysis admission and a 30-second deadline. It does not accept SQL or formulas. Snapshots are limited to 64 rows and 128 KiB; overflow marks coverage incomplete, and aggregate omitted-row counts are lower bounds.

Positions and lag retain exact int64 decimal strings. Opaque provider offsets are displayed separately and never subtracted. Delivered-position and durable-checkpoint backlog are different measures; neither is enqueue age. Tail observation is opt-in and adds bounded management traffic only after Start: `config.eventhubs.observe_partition_tail: true`, with `tail_poll_interval_ms` from 10000 to 300000 (zero/omitted uses 10000). See [processor configuration](EVENT_HUBS_HOWTO.md#processor-progress-and-checkpoint-evidence) for identity approval and capability requirements.

The API returns nullable delivered/checkpoint lag. Complete inventory, owned generation and source-monotonic ages for ownership and both cursors must qualify within 30 seconds of each row's capture. A stale/missing/disabled/fenced/unconfirmed cursor, in-flight checkpoint or incomplete scope stays unscored; an observed zero remains distinct. Per-row capture timestamps preserve provenance, while transit time and cross-host clock alignment remain unqualified. Results describe backlog as of source capture and association, not current-live freshness or causation.

Counters and checkpoint timing distributions are cumulative by authenticated source, execution group, session, partition and ownership generation. Latest evidence/capture revisions replace prior rows instead of summing repeated windows. Checkpoint outcome, explicit retry, redelivery and ownership verification evidence remain visible. Current owned/unverified rows take precedence when retained history reaches its bound. Progress queries do not turn these cumulative values into transaction counts or percentile averages, and they do not add CI or live-stop verdicts. Strict processor pause confirms actual receive/tail/checkpoint quiescence; recovery and live Azure qualification remain open; local/tagged checks do not certify provider readiness.

## Exact messaging management evidence

The management foundation is separate from data-plane action diagnostics and from native DuckDB queries. Enable new poll admission explicitly with both `APEXLOAD_RUN_MONITOR_PROJECTS` and `APEXLOAD_MESSAGING_MANAGEMENT_PROJECTS`. The capabilities response distinguishes the v1 evidence contract, project enablement and genuinely registered executable packs. The foundation and provider adapters are distinct releases. The collector registers the genuine `kafka-offsets`, pinned shared-key `servicebus-runtime` and pinned Basic `solace-semp` readers. Provider qualification is recorded separately from reader availability; the Service Bus workflow below describes its finite supported scope.

An administrator-approved source carries a versioned entity inventory, provider identity, principal/policy metadata and an exact `management://alias` destination. The alias selects an operator-owned collector target containing finite approved endpoints; a job cannot replace those endpoints. Kafka scopes state `read_committed` or `read_uncommitted` explicitly. Source revisions are immutable for already-started runs. Collector readiness publishes startup instance and scope/identity hashes; required sources remain unavailable without a matching genuine observer.

Credential names for management are separate from send/receive credentials. SASL/basic usernames and shared-key key names must equal the approved principal. The controller seals a purpose-specific short-lived credential bundle to the enrolled collector key. An opaque keyed credential version pins each source revision; rotation blocks observed publication until an administrator saves a new revision. Secret values and their raw hashes are never returned.

One admitted poll captures a bounded cohort of eligible overlapping runs. The collector performs one source observation and the controller durably writes private run-owned envelopes before atomically publishing their receipts. Exact retries retain source capture times, generation, poll identity and core digest. Controller receive timestamps use UTC microsecond precision consistently in immutable artifacts and database receipts; source timestamps retain their original precision. Even a denied poll with no numeric points and no rows is a durable snapshot with explicit missing inventory; it does not become an observed zero.

Offsets and populations are decimal strings, including observed `"0"`, through API, archive, CLI and dashboard. Kafka committed positions and read boundaries are next offsets. High watermark and last stable positions stay distinct; the physical replica log end is unavailable in the foundation. [Apache Kafka protocol semantics](https://kafka.apache.org/22/design/protocol/) describe these boundaries. Service Bus active counts can include deferred messages, so the foundation never invents a separate deferred count; see [Service Bus message counters](https://learn.microsoft.com/en-us/azure/service-bus-messaging/message-counters). Solace depth refers to the instantaneous queue collection count, as described in [SEMP collection counts](https://docs.solace.com/Admin/SEMP/SEMP-Features.htm).

Use `apexctl monitor management --id RUN_ID --source-id SOURCE_ID --limit 20` to read bounded receipt pages. Add `--from`/`--to` for an inclusive/exclusive capture window and `--cursor` for the returned opaque continuation. An entity filter selects complete snapshots containing that entity; it preserves their original approved inventory coverage. The dashboard's **Messaging management evidence** table displays exact values, unavailable reasons, source capture/receipt clocks and cadence gaps. Reads of accepted evidence remain authorized after feature disable.

Run archives keep the historical point artifact and optionally add `monitor_management.v1.jsonl` with snapshot/row counts, schema version, coverage and integrity metadata. Older archives omit the sidecar and gain no guessed management evidence. Scalar queries ignore typed-only observations rather than rounding large integers.

Poll expiry is not proof that provider calls stopped. A source with unresolved physical ownership remains unavailable to a new collector instance. The **Management collection ownership** controls show its exact owner/generation. Administrators may attest a verified process stop, with a durable audit trail, to release that exact fence. `apexctl monitor collection --project PROJECT_ID --id SOURCE_ID` reads the state; `monitor release-collection` accepts an explicit JSON attestation using `--file`. A stale owner/generation cannot release a newer collector. Collector-confirmed completion remains a distinct workflow.

[Review and verification status](qa/reviews/feature-completion-management-2026-10-03/README.md) records foundation acceptance separately from subsequent real provider qualification.

Terminal management poll journals remain available for seven days for retry and audit. The leader maintenance sweep scans at most 512 terminal journals and locks at most 50 projects per sweep. A durable cursor advances across protected records so repeated maintenance cannot scan the full retained history. Receipt-referenced polls, unresolved physical ownership, and the active configuration fairness cursor remain retained; run deletion makes obsolete journals eligible after the retry window.

A collector supports at most 32 assigned collecting run jobs, separately from its 32-job pending evidence budget. Required bindings reserve assignment slots before optional bindings; excess required bindings block Prepare/Start, while excess optional bindings record `collector_capacity` incomplete coverage. Stopping a run releases its assignments. Existing databases above the assignment limit report an explicit error until runs are stopped or expired; they cannot claim full monitoring coverage.


## Kafka management source configuration

The `kafka-offsets` pack reads only approved topic/partition/group tuples. Select it in **Monitoring sources** when project management collection is enabled and the controller advertises that executable pack. Administrators enter the exact cluster ID, read isolation, finite inventory, collector alias, SASL principal/policy revision, management-secret reference and enrolled collector public key. The saved approval displays its source revision and opaque credential version separately from unsaved edits. Source updates use the expected revision; a conflict retains the draft for review and requires reloading the current approval.

A local collector target pins every permitted TLS broker endpoint to an explicit numeric address at the same port. No hostname resolution or broker-discovered destination expands this map. The cluster ID must equal the scope provider. Supply a protected CA file containing only approved certificates; the collector loads and validates it once during configuration. Changing the file does not alter a running collector's trust. Reconstruct the collector with the revised configuration to approve new trust.

```json
{
  "pack": "kafka-offsets",
  "destination": "management://orders-kafka",
  "management": {
    "endpoints": ["tls://broker.example:9093"],
    "tls_ca_file": "/protected/kafka-ca.pem",
    "kafka": {
      "cluster_id": "APPROVED_CLUSTER_ID",
      "dial_addresses": {"tls://broker.example:9093": "192.0.2.10:9093"}
    }
  }
}
```

This fragment illustrates routing, not a complete target: copy the saved `management_scope` and `management_identity` into the corresponding local `management.scope` and `management.identity`, including the controller-generated credential version. Keep the sealed management credential separate from script send/receive credentials. Supported authentication is SASL PLAIN over verified TLS and SCRAM SHA-256/SHA-512. Anonymous, OAuth and ambient credentials are unavailable for this reader. Readiness validates only local immutable approval and does not contact the broker.

One source poll admits every TCP dial, TLS handshake and fixed Kafka request under the shared limits. The reader makes no background retries, consumption, production, offset commits or group joins. It verifies cluster identity on authenticated connections before reading positions. Observations preserve committed-next, high-watermark-next and last-stable-next separately; physical replica log-end remains unsupported. The capture time is the oldest successful component of a row, including cleanup in its source-age qualification. Partial permission or budget failures retain explicit field availability rather than converting missing positions to zero. The minimum source cadence is ten seconds.

```sh
apexctl monitor create-source --project PROJECT_ID --file source.json
apexctl monitor update-source --project PROJECT_ID --id SOURCE_ID --file revised-source.json
apexctl monitor delete-source --project PROJECT_ID --id SOURCE_ID --revision SOURCE_REVISION
```

An update file contains the currently saved `revision`. Deletion also requires that exact revision. Rotate a secret by saving a new administrator-approved source revision before collecting with the new credential. Existing run bindings retain their immutable older approval and fail visibly if its pinned credential no longer matches.

A timeout or failed socket close cannot release physical ownership. The collector keeps that resource charged until closure is proven; an unresolved close can also hold collector shutdown. Verify the process has actually stopped before using the existing audited stopped-process release controls. A lease expiry alone is insufficient.

Provider qualification is recorded separately from these implementation contracts. Controlled protocol fixtures cover framing, missing data, authentication, budgets and cleanup. Real broker results must identify the exact broker version/image, authentication profile and topology. The pinned Apache Kafka 3.9.2 single-broker fixture verifies all three SASL profiles and the distinction between high-watermark and last-stable next positions during an open transaction and after abort. This does not certify full consume-transform-produce atomicity, broker failover, newer releases, Event Hubs Kafka endpoints or protected hosted clusters.

## Service Bus management source configuration

The `servicebus-runtime` pack reads runtime descriptions for an administrator-approved finite inventory of queues or topic/subscription pairs. Select it in **Monitoring sources** when project management collection is enabled and the executable pack is advertised. Enter the canonical lowercase namespace hostname, public collector alias, exact Manage policy principal and policy revision, management-secret reference and enrolled collector public key. The source never accepts a connection string, raw key, external SAS or send/receive credential. A saved approval shows its revision and opaque credential version separately from the unsaved draft; revision conflicts retain the draft until the current approval is reloaded.

The initial management profile uses a pinned shared-key identity. A Manage claim can authorize provider changes; the collector itself exposes only fixed entity-description GET operations. Entra/ambient identity and live Azure interoperability are separate qualification gates. Data-plane Service Bus execution has its own runtime capabilities and is not enabled by selecting this management source.

A collector target approves one HTTPS namespace, one literal numeric address at port 443, and a protected explicit CA bundle. Construction loads trust once and makes no target requests. No DNS lookup, proxy, redirect, automatic retry, AMQP connection or discovered destination expands the approval. Changing a CA file requires reconstructing the collector with a revised operator-approved configuration.

```json
{
  "pack": "servicebus-runtime",
  "destination": "management://orders-servicebus",
  "management": {
    "endpoints": ["https://example.servicebus.windows.net"],
    "tls_ca_file": "/protected/servicebus-ca.pem",
    "service_bus": {"dial_address": "192.0.2.20:443"}
  }
}
```

This routing fragment is not a complete target. Copy the saved `management_scope` and `management_identity` into local `management.scope` and `management.identity`, including the opaque credential version. The scope provider must equal the approved namespace hostname. The first profile accepts queue/topic hierarchical ASCII paths of at most 256 characters and one-segment subscription names of at most 50; reserved management path segments and case-aliased duplicates are rejected. Authored entity casing remains in evidence.

Collection cadence is 30–3600 seconds, with a 30-second default for local approval. Older stored ten-second definitions remain readable and deletable, but a new revision must meet the current policy. The shared namespace origin gate prevents overlapping sources from bypassing the cadence. Each successful entity consumes separate admitted TCP, TLS and GET operations under the signed authorization deadline, whole-poll deadline, response-byte and shared activity budgets. Approval of 256 entities does not promise that all fit one poll; remaining evidence explicitly reports budget exhaustion.

Observed populations remain exact decimal strings, including zero. Missing or nil fields remain missing. Deferred counts are unsupported because active counts can include deferred messages; this reader does not estimate a separate count. Subscription scheduled/size evidence stays unsupported in this initial profile. Queue total, active, scheduled, dead-letter, transfer, transfer-dead-letter and size observations retain individual availability. A row's capture/age includes its actual read and physical cleanup; partial coverage remains visible in the existing management table, archive sidecar and CLI queries.

A timeout or unexplained socket close retains physical ownership. Verify the actual collector process has stopped before using the audited stopped-process release controls; expiry alone does not prove release. Controlled TLS/HTTP/XML tests qualify local parsing, authentication, budgets and cancellation behavior. They do not certify live Azure counters, service tiers or identity profiles. Provider results must identify the exact approved namespace/profile and independently reconcile populations.

## Solace management source configuration

The `solace-semp` collector pack polls only exact administrator-approved VPN/queue or VPN/topic-endpoint tuples through individual SEMP v2 monitor requests. The dashboard advertises it only with project management collection enabled and an executable collector pack. Enter the canonical lowercase broker hostname, exact Basic principal without a colon, policy revision, separate management-secret reference and enrolled collector public key. Entity case, Unicode and literal punctuation remain part of the identity. Saved approval and its opaque credential version remain separate from the unsaved draft; revision conflicts retain the draft.

The initial decoder profile follows the pinned software-broker 10.26.7.12655 monitor/action schemas. A controlled fixture is not live broker qualification. Collection discovery, pagination, legacy XML and alternate provider versions remain separate acceptance gates. Depth comes only from `collections.msgs.count`, preserving exact decimal strings and observed zero; absent/null counts remain missing. A DMQ relationship is retained only when its exact queue is approved in the same VPN. The legacy empty DMQ field means the relationship is unavailable, and cannot establish that no DMQ exists.

Approve one HTTPS origin, one fixed numeric route with a matching port and a protected explicit CA bundle in the collector. Startup loads trust once without target traffic. The collector requires a protected durable spool directory for session quarantine and explicit permission for the fixed own-session logout action.

```json
{
  "pack": "solace-semp",
  "destination": "management://orders-solace",
  "management": {
    "endpoints": ["https://broker.example.test:943"],
    "tls_ca_file": "/protected/solace-ca.pem",
    "solace": {
      "profile": "semp_v2",
      "dial_address": "192.0.2.30:943",
      "allow_own_session_logout": true
    }
  }
}
```

This routing fragment is not a complete target. Copy the saved `management_scope` and `management_identity` into local `management.scope` and `management.identity`, including the controller-generated credential version. Use the existing source CLI create/update/delete commands above; edits and deletion require the saved revision. The management identity is separate from message send/receive credentials.

Each source has a minimum thirty-second interval; aliases at the same normalized origin share the cooldown and a bounded five-HTTP-operation-per-second rate with burst two. Every TCP, TLS, fixed GET and own-session PUT operation consumes signed activity/deadline authority. There is no DNS, proxy, redirect, retry, discovery or arbitrary action. The response capture time precedes physical GET cleanup, and row age includes later transport and logout work. Remaining inventory is visibly incomplete when poll bounds are exhausted.

Before Basic bytes can escape, the collector durably records a bounded credential-free session intent. It reuses only the exact in-memory owned Session cookie and logs out using `PUT /SEMP/v2/action/about/user/logout` with fixed `{}` inside the original signed authority. A successful provider response, validated action metadata, proven raw socket close and durable intent removal are all required to release the session. Cookies and credentials never enter evidence. Cancellation may finish this bounded cleanup only while the original authority remains valid.

A missing/ambiguous cookie, rejected logout, expired authority, failed persistence or unproven close retains quarantine and prevents further authentication at that origin. Restart reloads unresolved intents even if the collector identity, controller URL or enabled pack changes. Process/socket destruction is separate from provider-session destruction; the existing stopped-process attestation cannot clear this remote-session quarantine. No automatic session reset or public provider-audited recovery workflow is advertised by this first slice. Preserve the protected spool and independently audit the provider session before any later recovery procedure.

Controlled TLS fixtures validate request surfaces, session ownership, exact depth, cancellation, physical cleanup and retained restart quarantine. Live software-broker/provider-version acceptance and audited restarted-source recovery remain open. Selecting this monitoring pack does not enable Solace SMF or JMS load execution.
