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

Only the implemented packs are accepted. SSH, OTLP metrics ingestion, JMX,
JDBC, CloudWatch and Azure Monitor adapters are separate follow-on work;
selecting one cannot silently return host metrics.

## Prepare before Start

For an enabled project, queued runs expose **Prepare**. Preparation validates
the immutable script/asset revision, reserves or provisions capacity using the
existing target backends, stages assignments and checks collector availability.
It waits before runtime preflight, protocol initialization, reflection, init
actions and monitoring-source polling. Runtimes that cannot honor this boundary
are rejected explicitly (currently Java Vuser, Bruno, and shakeout preparation).

Readiness defaults to 30 minutes from the ready transition; API/CLI callers can choose 5 minutes to 4 hours. Renewal rechecks the execution revision, reservation, ownership and collector readiness. Provisioning itself also has the requested TTL deadline.
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
