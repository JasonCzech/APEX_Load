# Native decimal precision consent review — 2026-10-20

This slice implements source-bound DECIMAL conversion consent within
[NA-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#na-a--deterministic-import-semantics).
The [operating contract](../../../NATIVE_ANALYTICS.md#decimal-precision-review)
and [generated acceptance inventory](../../ANALYTICS_ACCEPTANCE.md) describe its
supported scope. Other analytics and external qualification gates remain open.

## Delivered contract

Mapping version 3 adds an explicit reject/allow-rounding policy while retaining
identity schema 2. The bounded, read-only review inspects every immutable uploaded
file and identifies potentially lossy mapped DECIMAL columns from their precision
and scale. It does not invent a count of changed rows. The complete source set,
canonical mapping, project/import identity and current revision bind consent.
The worker re-derives the review from verified source handles before producing
outputs. Unconsented new lossy normalization fails; retained legacy artifacts
remain readable. Source files are unchanged.

SQLite and PostgreSQL persist the accepted review digests and consent revision.
Attempt inputs and publication fences preserve that receipt across recovery;
mapping edits invalidate it, while successful normalization and run reassociation
retain it. CLI and dashboard negotiate the precision capability, retain explicit
choices and refuse unsupported versions. The dashboard requires a current review
and explicit acknowledgement of a permitted risk. Saved mapping templates never
carry source-specific acknowledgement.

Bounds are 1,024 files, 64 measures, 1,024 file/measure pairs, 64 affected file/column
pairs and 64 KiB of review JSON. Source schemas are validated one at a time;
only bounded risks and source hashes remain in memory. Refusal returns no partial
review. API publication rechecks project authority, import revision, cancellation and state.
The minimum-controller upgrade barrier remains required; an old binary cannot be
made safe by a new client's capability check alone.

## Validation and review

- Final backend full Go: 168 packages, 17,767 top-level tests and 17,717 subtests passed; 33 top-level and 5 nested environment/feature skips remain skips. The API package completed in 963 seconds with `GOMAXPROCS=4`, `GOGC=100` and the unchanged 20-minute timeout.
- Native worker: 164 top-level tests and 293 subtests passed, including real DECIMAL conversion, exact-consent refusal, immutable input hashes and identity-v2 queries. Native API behavior passed; its generated response-schema finding was fixed and verified separately and by the final full Go gate.
- Race coverage passed for analysis, CLI and affected storage paths. An unnecessarily broad storage race run was deliberately interrupted; its log is retained and is not a passing package result. The replacement affected-storage run passed 49 top-level tests and 147 subtests.
- Actual PostgreSQL attempt/consent coverage passed (1 top-level test, 9 subtests). The earlier untagged invocation ran no PostgreSQL tests and is not counted as coverage.
- Dashboard typecheck, lint and 8,041 unit tests passed. Final analytics and Help browser workflows passed dev-admin 29 tests (1 skip) and local-auth 30 tests. Both profiles' narrow-layout screenshots were visually inspected, including a 207-character column name.
- The attached private HAR imported through the real endpoint with the same aggregate results as the preceding Java slice: 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, 4 warnings, zero DSL issues and 264 protected requirements. The original ZIP hash is unchanged. The randomized script hash is excluded from comparison. Neither target replay nor private payload retention occurred.

The broad dashboard cycle originally found a new select-locator mismatch and
three local-auth import-persistence failures. The locator was corrected. All
JMeter/LoadRunner workflow tests passed the diagnostic rerun (20 tests, 1 skip),
but that alone does not establish the original persistence failures' cause.
A diagnostic full local-auth rerun retained controller logs and reproduced the
import failures. The attestation errors explicitly report exhaustion of the
protected import store’s 128-session capacity across the shared test controller.
The JMeter publication failure has no recorded inner cause and remains unresolved.
The diagnostic run was interrupted when the shared disk filled; this record does
not claim a clean full dashboard cycle. Production import limits are unchanged.

Other review findings corrected before final validation: missing histogram-bound
precision disclosure, stale consent restoration, nested generated column schemas,
a stale generated authorization matrix, an omitted Help fragment and long-path
layout overflow, and simultaneous retention of every source schema. Streaming
review tests preserve the exact prior review JSON/digest, freeze the mapping,
discard schema aliases, and refuse incomplete, duplicate or failed cohorts. The
first native rerun hit the disk resource guard; that failure and the scoped cleanup
record remain retained. Original failure logs and screenshots remain separate from the
successful repetitions. A later full Go run under `GOMAXPROCS=2`/`GOGC=30` reached the aggregate API
package timeout without an earlier assertion failure; its failed result remains
separate from the passing final backend run. The staged review also found that
the embedded Help text predated the final streaming paragraph. Dashboard assets
were regenerated and checked for exact equality with the Markdown source. The
final assets passed static-serving/generated-contract tests (10 top-level, 8
subtests) and repeated analytics/Help browser checks: dev-admin 29 passed/1 skip,
local-auth 30 passed. Both profiles’ final screenshots were visually inspected.
The next precision-scope review found no actionable issues. The broader browser
gate and external qualifications above remain open.

## Parallel work and limits

A read-only audit of all 30 other registered worktrees found no implementation of
the precision-consent contracts. Older outstanding authoring/browser/telemetry
commits were patch-equivalent to changes already on main. The active `pr254`
worktree separately owns telemetry recovery/notification, Java diagnostics, HAR
limits, authoring and timestamp fixes. This slice does not copy or overwrite its
uncommitted work; those changes need integration when published. The unrelated
primary checkout remains untouched.

This is local darwin/arm64 qualification. The four native packaging lanes,
independent reference host, 1 GiB benchmark, remote writer/HA lifecycle, real
TruClient exports and multi-host recovery remain pending where unavailable.
No parent acceptance gate is promoted by these local results. Build caches remain
in use by follow-up work; shared caches are not pruned during parallel activity.

## Evidence

The [validation summary](evidence/validation-summary.json) distinguishes passing
checks, failed attempts and interrupted diagnostic runs. The
[review rounds](evidence/source-review-rounds.json) record findings and repairs.
The [worktree audit](evidence/worktree-audit-precision-overlap-recheck.json) records
the exact checked heads and contract search; the
[patch-equivalence check](evidence/worktree-audit-patch-equivalence.json) identifies
older work already incorporated into main. The
[HAR comparison](evidence/private-har-final-profile-comparison.json) preserves the
private-capture regression result without its payload.

The [final backend command](evidence/run-final-profile-validation.sh) requires the
private capture and test-overlay paths from the operator. Original unsuccessful
logs remain compressed beside successful runs. They are not acceptance evidence
for the gates they failed. [SHA256SUMS](SHA256SUMS) covers the retained record.
