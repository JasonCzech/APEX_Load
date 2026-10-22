# Native accepted rejections and histogram preview — 2026-10-21

This [NA-A slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics) closes two API-level reconciliation gaps that the [population golden](../roadmap-native-population-golden-2026-10-21/README.md) left open.

## What changed

- **Accepted rejections.** `TestAnalysisAcceptanceAcceptedRejectionsReconcile` uploads a source's exact bytes, in which some latency values are not numbers, and checks its preview (row count and first timestamp). It commits with rejected measurements accepted, then checks these against DuckDB's own SQL over the source, under the engine's contract (a finite number is a measurement, a null a missing sample, anything else rejected):
  - the accepted and rejected counts;
  - the upload digest;
  - the rejection download: exactly the rejected source rows, each once, attributed to the one file and measure, with the engine's fixed reason;
  - each minute's queried `count` and `sum` over the readable rows, with every series response reporting the rejected count.
- **Histogram preview.** The histogram golden, renamed `TestAnalysisHistogramImportPreviewCommitQueryReconcile`, now checks the file preview's row count and that the first scrape's sample rows hold its source `requests` values (the text `le` column reads as redacted) before it maps, commits and queries.
- **Inventory.** The [analytics acceptance inventory](../../ANALYTICS_ACCEPTANCE.md) lists the new goldens under nulls, resets, rejection provenance, preview/commit/query reconciliation, source non-mutation and raw statistics, and follows the rename.
- **Spec.** The NA-A status now names version 1 and version 2 mappings committed and queried through the API (`TestNativePartitionQueriesPreserveFullScanQualityLineageAndIndependentComparisonPlans` for version 2), and saved mappings that stay readable without being silently rewritten (`TestHistoricalSavedMappingReadsRemainCompatibleButCannotWriteUnsupportedOptions`), and leaves only external qualification open.

## Validation

| Gate | Result |
| --- | --- |
| `CGO_ENABLED=1 go test -tags analytics_duckdb ./pkg/api -run 'TestAnalysisAcceptance\|TestAnalysisHistogramImportPreviewCommitQueryReconcile'` | Passed |
| `go test ./docs/qa` (inventory and dashboard freshness) | Passed |
| Documentation checks | Passed |

No product code changed. A separate commit regenerated the QA dashboard, whose API contract count had been stale since the validation-context route was added.

## Review

One reviewer read scratch snapshots only:

- Round 1: six findings, all fixed. The two medium ones: the histogram preview's first-bin check could never run, because the text `le` column is redacted in previews; and the spec cited a test whose version 2 commit fails as proof that a version 2 dataset answers a historical query. The low ones: the rejection download could list a row twice and still pass, the inventory listed a preview step the test lacked, the oracle counted nulls as rejected and accepted non-finite values, and the staged diff carried the worktree's unrelated bundle state.
- Round 2: all six confirmed fixed; one optional nit applied (the accepted count now counts nulls, which the engine accepts as missing samples).

## Remaining

External native and reference-host qualification remains open for NA-A.
