# Roadmap implementation: local-wall timestamp qualification

Date: 2026-10-19. Scope: a bounded [NA-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics) timestamp-correctness slice following `dd397bbd7`.

## Contract

New local-wall normalization requires one unique instant in the selected source timezone. A compiled-in native guard checks calendar components against the timezone's intervals and requires agreement with the native conversion. Repeated times, skipped times/dates, timezone-database disagreement and incomplete bounded resolution are rejected. The search is limited to 32 intervals within 48 hours either side and offsets within ±24 hours; it never guesses a one-hour transition. Explicit-offset and timezone-aware absolute inputs retain their instant. Historical canonical artifacts and query readers are not rewritten.

Raw, summary and histogram paths share this guard. Default refusal publishes no output. Explicit acceptance of rejected rows preserves the existing file/measure/source-row rejection artifacts and source hashes. Mapping/API versions, worker isolation, disabled external access, memory/spill limits and output ownership remain unchanged. The helper is a fixed compiled-in function, not caller-supplied SQL or an extension download.

## Validation and review

Completed checks:

- Full native analytics suite and focused race checks passed, including raw/summary/histogram refusal, typed absolute-time compatibility, historical non-hour transitions, source non-mutation and bounded traversal.
- Four existing native API import/rejection-download/summary/histogram workflows passed. Generated acceptance inventory, 257-file documentation checks, help ownership/fragment checks and whitespace checks passed.
- The required integration of upstream `609bf1b1a` was conflict-free. The integrated dashboard passed typecheck, lint, 99 import/help unit tests and 14 telemetry unit tests. The four affected browser workflow files passed in both profiles: dev-admin 19 passed/one profile skip; local-auth 19 passed. Dashboard assets were generated before Go API builds. The workflow script closed its controllers and browsers. Raw frontend logs are gzip-compressed without changing their bytes. All 1,145 generated static import/entry-point references resolve; the 308 built files have retained hashes and are committed separately from source changes.

Review added half-hour gaps, repeated dates, source hashes and timezone-aware inputs under a local mapping. The final source/test review found no further actionable issues. The initial integrated full Go suite exposed a separate [coordinated-start clock-domain defect](../roadmap-coordinated-published-clock-2026-10-19/README.md) in two real-generator adoption tests. Its failure trace is retained with that correction. The corrected full-suite run (`../roadmap-coordinated-published-clock-2026-10-19/evidence/corrected-full-go-results.json`) passed all 168 tested packages with 10 skipped/no-test packages and no failures. The final scoped review found no further actionable issues. Local tests are not qualification of Linux/Windows native release lanes, reference workloads or multi-host recovery; those environments are unavailable. DECIMAL disclosure, partition precedence and other NA-A requirements remain open.
