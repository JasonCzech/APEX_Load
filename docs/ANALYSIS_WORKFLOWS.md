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
ownership. Dataset deletion removes originals, upload chunks and normalized
objects; a durable cleanup queue also handles project/test deletion and late
cancelled writers.

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

One heavy analytical pipeline runs per controller process, including staging
and publication. DuckDB defaults to two threads, a 2 GiB memory setting and an
8 GiB spill quota. Queries/imports have deadlines; disk headroom and total process
RSS and container memory usage are monitored because native allocations are not bounded by Go's heap limit
or DuckDB's configured memory setting. Concurrent requests receive explicit busy,
resource, cancellation or size-limit errors.

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

Native action and business-transaction panels support `observation_rate`, `error_observation_rate` and `success_observation_rate`, each with `aggregation: rate` and unit `observations/s`. Counts come from the selected retained evidence. Archive buckets use their actual elapsed span, including shortened last buckets; source live intervals use their publication window. Merged live rates require complete contributing source-window coverage. Empty or incomplete buckets remain unavailable; an observed population with no failures has a measured zero failure rate. These rates describe retained observations, not guaranteed total execution throughput. Existing capture/coverage warnings and filters apply.
