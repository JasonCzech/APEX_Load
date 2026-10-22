# Native import failure provenance — 2026-10-21

This [NA-A slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics) makes the two import failures a person can act on name their file, instead of the generic "Normalization failed; inspect mapping, schema, rejection and resource evidence." The contract is documented in the [native analytics guide](../../../NATIVE_ANALYTICS.md#import-and-compare-parquet).

## What changed

- **Schema drift.** Before any row is read, every source file must hold each column the mapping reads (`ParquetMapping.MappedColumns`: the timestamp, identity columns, then each measure's value, name, sample and histogram columns) as a scalar column. A file that lacks one, holds it as a list or repeated field, or holds a group of fields there fails the whole import. The stored error names the file and the column, for example `File "b.parquet" does not match the mapping: it has no column "latency".` Nothing is accepted.
- **Rejected rows.** When rows had a measurement rejected and the import did not accept rejected measurements, the stored error names the file, the measure, the count and the first five zero-based source row ordinals. Source values are never included.
- **Across the contained worker.** The worker still returns only fixed codes. Two new codes, `schema_mismatch` and `rejected_rows`, carry indices only: the file index into the request's inputs, the mapped-column or measure index into the request's mapping, a fixed reason, a count and ascending row ordinals. The parent refuses a payload that is out of range, has the wrong code, or does not belong to a normalization, and rejected rows unless the normalization declined rejections, with a count and row ordinals within the file's known rows and exactly its first ordinals. Column and measure names are shown as the schema preview shows them. The controller turns the indices into the file's import path and the mapping's own column or measure name.
- **Storage.** `FailAnalysisImportAttempt` takes a reason; an empty one keeps the generic message, as every other failure (engine, resource, reconciliation) still does.

## Validation

| Gate | Result |
| --- | --- |
| Mapping unit tests (`TestMappedColumns…`, `TestSchemaMismatch…`, `TestRejectedRows…`), including a group holding a list and an escaped `.` in a component | Passed |
| Worker contract tests for both payloads (in range, code pairing, operation scope, acceptance, input row bounds, ordering, exact first rows) | Passed |
| Native golden `TestNativeAcceptanceSchemaDriftAcrossFiles`: absent column, DuckDB list and struct columns, rejected text rows, numeric text | Passed |
| API acceptance through the real worker `TestAnalysisAcceptanceDuplicateAndDriftedFiles`: the stored error for each case, including a long table's missing name column | Passed |
| Full untagged `go test ./pkg/api` | Passed (758 s) |
| Untagged `pkg/analysis/...` and `pkg/storage`; tagged analytics suite (`pkg/analysis/...` and `pkg/api -run 'Test(Analysis\|Native\|Parquet\|Container\|Lagged\|Bucket\|Worker)'`) | Passed |

## Review

One reviewer read scratch snapshots only:

- Round 1: five findings, all fixed. The medium one: the worker accepted rejected rows the engine cannot report (for a normalization that accepts rejections, beyond the file's rows, or with too few ordinals). The low ones: a group with a list among its fields read as a list, column paths were not escaped as in the schema preview, the duplicate-file sentence overclaimed, and no test covered a group column or a mapped column other than the value.
- Rounds 2–5: one or two low findings each, all fixed: the escaped path was escaped twice by `%q`; the mapping's own quotes, line breaks, separators and bidi controls could split or reorder the message; this record lagged the code.
- Round 6: no findings.

## Remaining

Engine, resource and reconciliation failures keep the generic message. The other NA-A gates (counter resets, raw/summary/histogram reconciliation goldens, external qualification) remain open.
