# Native partition precedence — 2026-10-20

This [NA-A qualification slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics) locks down the existing v1/v2 contract without changing normalization: physical columns provide timestamps and measurements; an explicit mapping chooses a constant metric name or a physical name column, and choosing both is rejected. Hive-style directory labels remain source provenance and cannot override those selections or create schema columns.

A native fixture places immutable Parquet bytes beneath conflicting timestamp, value, metric, source, region and additional directory labels. Direct native and contained preview retain physical numeric values. Contained normalization and query preserve both constant/column metric names, v2 source/dimension identities and independently known sums. Original and nested source hashes remain equal before and after the workflow.

## Validation and review

The installed native analytics suite passed 2 packages, 315 top-level tests and 782 subtests, including the contained v1/v2 precedence fixture. The generated analytics inventory, complete documentation QA package and documentation link checks passed. This test-and-guide slice changes no production or embedded Help content; the preceding committed assets remain unchanged. Exact commands, source/asset hashes and raw logs are retained in the evidence bundle (`SHA256SUMS`).

The same test fails in both mapping versions against a temporary control that enables Hive inference for native preview: it observes directory timestamp 1 and value 999 in place of the physical timestamp 1700000040 and value 7. This intentionally changed control is separate from a prior-product regression failure. Its hashes and failing log are retained. Final applied-source and staged review found no remaining actionable findings.

No source format, mapping version or generated contract changes. DECIMAL precision disclosure, broader schema drift and external native/reference qualification remain separate pending requirements. The [owning guide](../../../NATIVE_ANALYTICS.md) preserves the supported interpretation.
