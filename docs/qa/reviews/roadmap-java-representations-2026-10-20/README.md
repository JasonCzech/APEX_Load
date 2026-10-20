# Java opaque representation review — 2026-10-20

This slice implements the versioned embedded Java-over-HTTP byte and base64-work
cohort in [PROTO-A/PROTO-B](../../../ROADMAP_IMPLEMENTATION_SPEC.md#proto-a--finish-truthful-measurements-and-propagation).
The [measurement matrix](../../PROTOCOL_TELEMETRY_MATRIX.md#versioned-java-opaque-representations)
links acquisition, propagation and consumer fixtures.

## Delivered contract

Optional versioned diagnostics distinguish runtime input, decoded request, first
prepared HTTP body and retained response representations. Base64 work and its
enclosing interval remain separate from JVM codec work. Action summaries preserve
own populations, missing values, measured zero, compression and bounded exact sums.
Typed RPC, live metrics, SQLite/PostgreSQL history, JSONL/Parquet, native queries,
baseline comparisons, reports and dashboard exports retain those meanings.

Native queries require one exact action and bounded raw evidence. Ratios divide
paired sums; zero denominators and incomplete windows remain unscored. Baseline
comparisons are advisory and require compatible complete action evidence. The new
`actual_action_samples` archive field excludes same-name transaction wrappers;
legacy records without that population remain unavailable. Existing transaction
totals and schema migration fingerprints retain their earlier meanings.

## Validation and review

- Final full Go gate: 168 packages, 17736 top-level tests and 17622 subtests passed; 33 top-level tests skipped. See [exact test counts and failures by cohort](evidence/validation-counts.json) and [final full output](evidence/final-clean-full-go.jsonl.gz). A pass here does not convert environment-gated skips to qualification.
- Final dashboard typecheck, lint, build and 720 unit files / 8,032 tests passed. The broad cycle passed dev-admin 329 tests (6 skipped) and local-auth 268 (1 skipped). After review fixes, the affected four Java workflows plus Help passed dev-admin 16 (1 skipped) and local-auth 17. Later screenshot-framing changes passed their two affected workflows separately in both profiles; the resulting Java evidence, independent counts, absence and help were visually reviewed.
- Affected protocol, HTTP, Java HTTP, engine, RPC, analysis, storage and API race checks passed. Storage/API were repeated separately after linker disk exhaustion. Targeted API, native metric/unit fixtures, generated contract checks, analytics inventory, documentation QA and actual PostgreSQL migration/history tests passed.
- The private attachment imported successfully through the real endpoint: HTTP 200, 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, 4 warnings, zero DSL issues and 264 protected requirements. [Aggregate comparison](evidence/har-compatibility-final.json) matches the preceding slice exactly except the randomized script hash. Original ZIP hash is unchanged. Neither target replay nor converted private payload retention occurred; only the aggregate and compatibility harness are preserved.

[Commands and cohort boundaries](evidence/commands.md), [runtime versions](evidence/runtime.txt),
[final source/asset hashes](evidence/final-source-hashes.json) and [review rounds](evidence/REVIEW.md)
make the repetitions distinguishable. Earlier failures remain in their own logs:
unit-resolution and compatibility-barrel defects were corrected; archive download
refusals were caused by the real 1 GiB free-space guard and repeated after reclaiming
obsolete build archives. The guard was preserved. Staging the new tests changed the tracked QA inventory; its generated dashboard was refreshed and the complete gate repeated. The CSV browser assertion was
corrected to match quoted export cells. A first local-auth selection covered Help
only; it is not counted as Java coverage.

Review fixed the action denominator, direct JSON byte bound, native unit lookup,
coverage consistency, text timing/availability projections, independent baseline
CSV counts, auth-profile selection and import grouping. The final source review
found no further issues; staged files and evidence are checked before commit.

## Limits

This is one local darwin/arm64 embedded opaque-byte cohort. UI workflows use
controlled API evidence with real navigation/authentication and panel persistence.
JVM object footprint, actual JVM codec execution, an approved helper, measured
production overhead, complete distributed source-window qualification, reference
hosts, Linux/Windows native runners, real TruClient exports and multi-host recovery
remain open. No acceptance promotion or full PROTO-A/PROTO-B closure is claimed.

[Evidence checksums](evidence/SHA256SUMS) cover the retained bundle. Source manifests
exclude this evidence directory to avoid recursive hashing.
