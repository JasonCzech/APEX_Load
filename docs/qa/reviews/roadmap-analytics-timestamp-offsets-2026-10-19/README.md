# Roadmap implementation: native timestamp source offsets

Date: 2026-10-19. Scope: a bounded [NA-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics) timestamp-correctness slice. Base: `dd1e8308f`.

## Contract

`timestamp_offset` normalization requires a timezone-aware Parquet timestamp or a parsable clock-bearing text value with an explicit Z/numeric offset. Offset-free text, DATE and naive TIMESTAMP values are rejected instead of inheriting the worker's UTC session setting. Local-wall `timestamp` mappings refuse offset text, including short numeric offsets, instead of dropping the supplied offset. Existing mapping versions and canonical readers remain intact; historical artifacts are not rewritten.

The native fixture contains both occurrences of a repeated local clock time with different explicit offsets in one file. Independent UTC expectations preserve both instants. Other fixtures cross spring/fall DST changes with unambiguous local timestamps and verify conversion per row. This does not qualify ambiguous or nonexistent local-wall inputs: that refusal contract remains a subsequent NA-A item, along with partition precedence, DECIMAL disclosure and schema-drift work.

Rejections use the existing bounded file/measure/source-row summaries and durable rejected-row artifacts on explicit acceptance. Default refusal publishes no output. Source bytes are SHA-256 checked before and after normalization. Worker isolation, memory/spill bounds, cancellation and output ownership are unchanged.

## Validation and review

Validation passed:

- Full default Go suite: 168 packages passed, 10 packages skipped/no test files, no failures. The compressed raw JSON trace and package summary are retained; this command permits normal Go test caching.
- Fresh native analytics suite (`analytics_duckdb`), timestamp race checks, and four native API import/rejection/summary/histogram workflows passed.
- Generated analytics acceptance inventory passed. Documentation checks passed for 255 active Markdown files, help guide ownership/fragment checks passed, and `git diff --check` passed. This slice changes no dashboard or embedded-help source.

Review added typed DATE/TIMESTAMP refusals and whitespace-before-offset coverage. The pinned parser refuses the spaced-offset fixture; it remains a rejected row rather than a guessed instant. The initial failure and failed parser-fixture expectation are retained alongside final runs. Review also registered the new tests in the generated acceptance inventory. After these fixes, the final source, test, documentation and scoped-change review found no further actionable findings.

[Evidence commands and environment](evidence/commands.txt), [full Go package results](evidence/full-go-package-results.json), and [SHA-256 manifest](evidence/SHA256SUMS) identify the validation records.

The unavailable reference hosts and native Linux/Windows runners leave release-scale and platform gates pending. These tests exercise the installed macOS arm64 native library only. The existing decimal and other legacy goldens are compatibility tests, not qualification of the remaining revised requirements.
