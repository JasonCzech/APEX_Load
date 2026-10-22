# Native population reconciliation golden — 2026-10-21

This [NA-A slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics) adds a raw-observation and counter golden through the import API. Summaries already reconcile across preview, commit and query (`TestAnalysisSummaryImportPreviewCommitQueryReconcile`), and histograms across commit and query (`TestAnalysisHistogramImportCommitQueryReconcile`).

## What changed

- **Golden.** `TestAnalysisAcceptanceRawAndCounterPopulationsReconcile` (`pkg/api/analysis_acceptance_populations_native_test.go`) imports two Parquet files through the real worker:
  - a raw latency observation with nulls in one minute only;
  - a cumulative counter rising by irregular steps, which restarts in the second file and again inside it.
- **What it checks.**
  - Each file's preview row count and first reading.
  - The accepted measurement count, with no rejections.
  - The recorded digest of each upload, and after commit, that each stored original holds the source's exact bytes.
  - Per-minute `count`, `sum`, `min`, `max`, `avg`, `p50` and `p95` of the observation. These are compared with DuckDB's own SQL over the source files, which is independent of the normalization and series code. Percentiles use DuckDB's linear interpolation (`quantile_cont`), the definition the engine uses. The minute holding nulls must read as missing for every aggregation.
  - The counter's per-minute `delta`. The values must equal the sources' differences of closing (last) readings, with `no_prior_sample`, `counter_reset` and `missing_counter_sample` where the contract puts them. Irregular steps make a regression to opening or lowest readings fail (checked against a temporary control).
- **Guide.** [Native analytics](../../../NATIVE_ANALYTICS.md) now states that a null scalar measurement is a missing sample, so its whole bucket, count included, reads as missing. A histogram whose bin counts are all null is missing, and one with only some null is rejected.
- **Helper.** The acceptance import helper takes checks to run before the mapping is saved (here, the previews).

## Validation

| Gate | Result |
| --- | --- |
| `CGO_ENABLED=1 go test -tags analytics_duckdb ./pkg/api -run 'TestAnalysisAcceptance'` | Passed |
| Documentation checks | Passed |

No product code changed.

## Review

One reviewer read scratch snapshots only:

- Round 1: five findings, all fixed. The two medium ones: a counter rising by a constant step could not tell closing readings from opening ones, and the spec claimed the whole exit list was covered locally when most goldens run at the engine and some items are untested. The low ones: the digest check proved the upload, not the stored original; the oracle shares DuckDB's percentile definition; the null-sample sentence was too general for histograms.
- Round 2: no findings beyond three wording nits, applied.

## Remaining

Mapping layouts, decimals beyond double precision, conflicting files, partition precedence and daylight-saving times are qualified at the engine rather than through the API. Accepted rejections reconciled through query, older mappings read through the API, histogram preview, and external native and reference-host qualification remain open for NA-A.
