# PT Analysis Dashboard: Native Parquet Import Plan

## Recommendation

Support native Parquet import, but do not analyze arbitrary Parquet schemas directly.

The dashboard should read Parquet as a first-class source format, then normalize imported data into a canonical internal analysis model. This preserves Parquet's performance and schema advantages while giving the dashboard a consistent foundation for correlation across scenarios, transactions, and system metrics.

## Why Native Parquet Import Makes Sense

Parquet is well suited for performance test analysis because it is:

- Columnar, which is efficient for analytical queries over large result sets.
- Strongly typed, which helps preserve timestamps, numeric metrics, labels, and dimensions.
- Compressed, which is useful for storing large test-result archives.
- Widely supported by DuckDB, Arrow, Polars, Spark, Databricks, Python, and cloud analytics platforms.
- Good for multi-file and partitioned datasets, such as results grouped by test run, scenario, date, host, or metric type.

Avoid converting Parquet to CSV as the primary import path. CSV loses schema, types, compression, nested structures, and efficient column scans.

## Core Design

```text
Parquet files
   |
   v
Native reader: DuckDB / Arrow / Polars
   |
   v
Schema detection and field mapping
   |
   v
Canonical dashboard analysis tables
   |
   v
Correlation engine and dashboard queries
```

## Recommended Architecture

1. Accept Parquet files directly in the dashboard import workflow.
2. Preserve the original Parquet file as an imported artifact.
3. Infer the source schema and inspect column names, types, and sample values.
4. Map source columns into canonical dashboard fields.
5. Validate required fields for correlation, especially time and scenario/run identity.
6. Load normalized data into the dashboard's analysis store.
7. Use DuckDB or Arrow-backed query paths for fast local analytics.
8. Materialize common correlation views only when needed for performance.

## Canonical Fields

The internal model should normalize different source schemas into shared dimensions and facts.

Recommended fields:

- `run_id`
- `scenario_id`
- `scenario_name`
- `timestamp_utc`
- `relative_time_ms`
- `source`
- `host`
- `service`
- `metric_name`
- `metric_value`
- `unit`
- `transaction_name`
- `status`
- `error_type`
- `tags`
- `labels`

This allows different imported datasets to be compared even when source systems use different column names.

Examples of source fields that should map to a common dashboard concept:

```text
elapsed          -> response_time_ms
response_time    -> response_time_ms
duration_ms      -> response_time_ms
latency          -> response_time_ms
cpu_pct          -> cpu_percent
processor_time   -> cpu_percent
```

## Correlation Requirement

The most important requirement is a reliable shared time basis.

Every imported dataset should support both:

- Absolute time: `timestamp_utc`
- Test-relative time: `relative_time_ms`

This makes it possible to answer questions such as:

- Did CPU saturation start before checkout latency spiked?
- Did scenario 2 begin failing after scenario 1 increased load?
- Did network latency correlate with transaction response time?
- Did memory pressure increase across multiple concurrent scenarios?
- Did host-level system metrics explain one scenario's degradation but not another's?

## Multi-Scenario and System Data Correlation

To correlate across multiple scenarios and system metrics, normalize imports into event or metric tables that share common keys:

```text
run_id
scenario_id
timestamp_utc
relative_time_ms
metric_name
metric_value
source
host/service
tags/labels
```

Useful correlation dimensions:

- Time window
- Test run
- Scenario
- Transaction
- Host
- Service
- Region or environment
- Metric type
- Error category

Useful derived views:

- Scenario response time by time bucket
- Transaction percentile latency by time bucket
- Error rate by scenario and transaction
- Host CPU/memory/network by time bucket
- Correlated scenario latency versus system resource utilization
- Cross-scenario contention analysis

## Suggested Storage Strategy

Use a two-layer import model:

### 1. Raw Artifact Layer

Store the original imported Parquet files unchanged.

Purpose:

- Reprocessing
- Auditing
- Debugging mappings
- Supporting future schema improvements

### 2. Normalized Analysis Layer

Store mapped and validated records in dashboard-native analysis tables.

Purpose:

- Fast querying
- Reliable joins
- Cross-scenario comparison
- System metric correlation
- Consistent dashboard behavior

## Suggested Query Engine

DuckDB is a strong fit for the PT Analysis Dashboard because it can:

- Read Parquet directly.
- Query multiple Parquet files at once.
- Join imported scenario and system datasets.
- Run analytical SQL locally.
- Avoid requiring a heavyweight database for exploratory analysis.
- Interoperate with Arrow and Python if needed.

DuckDB can be used either as:

- A direct query layer over imported Parquet files.
- A staging engine during import.
- A local analytical cache for normalized dashboard tables.

## Import Workflow

Recommended user flow:

1. User uploads or selects one or more Parquet files.
2. Dashboard inspects schema and previews detected columns.
3. Dashboard auto-detects common fields where possible.
4. User confirms or edits mappings.
5. Dashboard validates required fields.
6. Dashboard imports and normalizes the data.
7. Dashboard exposes the imported data for comparison and correlation.

## Minimum Viable Implementation

Start with:

- Native Parquet file reading.
- Schema inspection.
- Mapping configuration.
- Required timestamp validation.
- Normalized metric table.
- Ability to import scenario metrics and system metrics.
- Time-bucketed correlation queries.

Minimum canonical table:

```text
metric_points
-------------
id
run_id
scenario_id
timestamp_utc
relative_time_ms
source
host
service
transaction_name
metric_name
metric_value
unit
status
tags_json
```

## Follow-On Enhancements

After the first import path works:

- Add saved import mappings by source/tool type.
- Add automatic unit normalization.
- Add partition-aware imports.
- Add batch import for directories of Parquet files.
- Add correlation scoring across metric series.
- Add lag analysis to detect whether system metrics lead or trail scenario degradation.
- Add derived metric generation, such as error rate, throughput, p95 latency, and saturation indicators.
- Add support for Parquet datasets partitioned by `run_id`, `scenario_id`, date, or metric type.

## Key Decision

The recommended decision is:

> Import Parquet natively, preserve the original file, and normalize the meaning into the dashboard's canonical analysis model.

Do not convert Parquet to CSV or JSON as the primary implementation. Conversion is only useful for exports, debugging, or building an optimized cache.
