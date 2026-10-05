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

Percentiles require raw observations. Summary-only percentiles are never averaged
or treated as transaction samples. Counters require explicit rate conversion;
resets, duplicate instants and missing samples interrupt the rate chain. Queries
are bounded to 10,000 buckets and 366 days. Unsupported semantics produce an
explicit error instead of a misleading chart.

An external dataset keeps its own identity. Associate it explicitly with a run
in the same project before comparing it with managed evidence. Comparisons
select datasets, bound monitor evidence, or archived transaction durations and
align UTC buckets. Lagged correlation reports paired bucket count, coverage and
an optional coefficient; constant or insufficient evidence stays unscored.
Correlation is association, never proof of causation. Saved comparison
definitions are revisioned and recheck ownership when used.

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
cancellation or size-limit errors. SQLite/PostgreSQL schema 29 records exact
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

Shared typed operations implement eligible count ratios, epoch-aware counter deltas/rates, comparable absolute/percentage baseline changes, and bounded moving mean/standard deviation. Existing raw counter bucket rates retain their reset-only display behavior and explicitly unknown epoch lineage. They cannot qualify the new baseline or moving operations simply because a metric version/population is supplied; qualified counter operations require the explicit epoch-aware input. A complete observed window with zero events has a measured count/rate of zero. Missing windows, incompatible semantics, unknown/reset epochs and empty denominators remain unscored. Derived points retain bounded operation, algorithm and elapsed-bucket lineage, so a P95 series cannot be compared as an average series. Legacy derived points with unknown parent statistics remain readable but unqualified for these new operations. Histogram and precomputed-summary reaggregation remain unavailable pending explicit typed import executors.

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
