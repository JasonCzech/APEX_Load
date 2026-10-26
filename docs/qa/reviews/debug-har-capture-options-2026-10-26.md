# Debug HAR capture options

Date: 2026-10-26
Base: `819563141`

## Result

Debug options now offers request-only, response-only, both, or no body retention.
Bodies are off by default. Per-body choices are 16, 64, 256, and 1,024 KiB;
run-wide choices are 1, 4, and 16 MiB, with defaults of 64 KiB and 4 MiB.
The dashboard enables intermediate redirects and measured timing phases by
default. Controls apply to the next debug run, survive named-profile saves,
and are recorded in the frozen completion summary and HAR metadata.

Capture observes dispatched HTTP exchanges, including resource requests, digest
retries, redirect hops, meta-refresh requests, and wire cache revalidation.
It preserves existing execution, redirect, resource-discovery, and cache policy.
HTTP version clones share the same body allowance. The final HTTP response uses
execution's existing bounded decoder for compressed content. Browser bodies
require complete, known-size, unencoded textual responses. Missing, binary,
invalid-UTF-8, oversized, or budget-exhausted bodies are omitted whole; exported
entries explain unavailable bodies. Empty retained bodies remain empty.

Credential, runtime-secret, and custom redaction precede publication and the
HAR writer applies the existing HAR scrubber again. Body-specific redaction also
handles quoted credential assignments in inline JavaScript, including textual
base64 HAR bodies. Stable-label redaction behavior is unchanged.

Measured body sizes remain distinct from redacted text length. HTTP timing uses
per-exchange traces; HAR connection time includes TLS. Browser timing comes from
its measured evidence, never from the worker's body-collection time. Optional
missing timing phases remain unavailable. Native HAR file disposal, session
retention, authorization, disclosure restrictions, and existing JSON/log exports
retain their existing behavior.

## Review

Review covered capability propagation, frozen options, observer transparency,
resource counters and deduplication, body budgets, cloning, publication redaction,
HTTP and browser timing provenance, and optional-field compatibility. Findings
around redirected browser POST headers, resource coverage, unknown body sizes,
inline credential assignments, TLS phase semantics, and measured byte counts
were fixed and regression-tested. Final source review found no remaining issues.

## Validation

- Dashboard build, final typecheck, and lint pass.
- Full dashboard unit suite: all 8,700 tests across 749 files pass. The run uses
  a 20-second test timeout to accommodate source-scanning checks under load;
  repository timeout configuration is unchanged.
- All eight affected Playwright workflows pass in dev-admin and again in
  local-auth (16 passes). They inspect downloaded HAR bodies, redaction,
  redirect hops and timings; rerun with all options off; verify frozen summary
  options, focus and keyboard checkbox interaction; and retain existing summary,
  failure, stopped-run and responsive coverage.
- Native select key changes were unavailable in a plain HTML reproduction in
  this headless macOS Chromium runtime. Browser verification uses native form
  selection for dropdown values and exercises tab focus and Space toggles.
- All affected capture/replay/redaction package tests pass. Final targeted race
  checks also pass, including body sizes, TLS timing, resources, and inline
  credential redaction.
- Final HAR API, authorization, OpenAPI freshness, and generated contract checks
  pass (`pkg/api`, 157.331 seconds under concurrent validation).
- The final repository-wide Go run (`go test -p 4 -timeout=20m ./...`) completed
  with all non-API packages passing. The API package repeated the six previously
  documented replay-artifact failures and exhausted its 20-minute aggregate
  budget (1,203.165 seconds). It had just entered
  `TestScenarioRuntimeSettingsReportShowsScheduleOverride` at the cutoff. The
  full Go gate is not green and the timeout means later API tests did not all
  complete in that invocation. Feature-specific API checks pass separately.
  The cutoff test passes in isolation in 1.662 seconds, confirming that its
  appearance in the timeout does not establish an individual test failure.

The repeated baseline failures are `TestReplayArtifactHTTPCleanupLifecycle`,
`TestReplayArtifactHTTPProjectDeletionAndNamespaceCleanup`,
`TestReplayArtifactRuntimeRecoversNativePair`,
`TestReplayArtifactRuntimeCleanupRequiresSourceAndJoin`,
`TestReplayArtifactRuntimeCleanupTraversesForeignPage`, and
`TestReplayArtifactRuntimeCleanupThroughStoreOverlay`. Their unchanged-main
reproduction is recorded in the preceding debug-completion QA record.

Raw evidence is local and deliberately untracked:

- Focused capture checks: `/private/tmp/apex-har-options-focused.log`
- Affected package checks: `/private/tmp/apex-har-options-packages-final.log`
- Redaction and resource checks: `/private/tmp/apex-har-options-redaction.log`, `/private/tmp/apex-har-options-resources.log`
- Final review regressions: `/private/tmp/apex-har-options-final-review.log`
- Race checks: `/private/tmp/apex-har-options-race-complete.log`
- HAR API checks: `/private/tmp/apex-har-options-api-complete.log`
- API generation: `/private/tmp/apex-har-options-generate-final.log`
- Dashboard build: `/private/tmp/apex-har-options-build.log`
- Typecheck/lint: `/private/tmp/apex-har-options-typecheck-final.log`, `/private/tmp/apex-har-options-lint-complete.log`
- Dashboard unit suite: `/private/tmp/apex-har-options-dashboard-final.log`
- Browser profiles: `/private/tmp/apex-har-options-e2e-dev-final.log`, `/private/tmp/apex-har-options-e2e-auth-final.log`
- Browser artifacts and controller logs: `/private/tmp/apex-har-options-e2e/`
- Cutoff-test rerun: `/private/tmp/apex-har-options-api-cutoff.log`
- Full Go regression run: `/private/tmp/apex-har-options-go-full-final.log`

The earlier broad Go attempt was superseded after source changes and terminated;
only the final run is evidence for the complete implementation.

## Cleanup

- Cleared the isolated Go build/test cache (approximately 11 GiB, reduced to
  8 KiB) and the superseded build scratch directory (approximately 1.1 GiB).
- Removed frontend Vite caches after all frontend users finished; removed the
  temporary dependency symlink while preserving the shared dependency directory.
- Ran `docker builder prune --all --force`; Docker reported 0 B reclaimed.
- Both browser runners closed their browsers and disposable controllers. The
  cutoff run left no tracked native subprocesses; neither browser test port
  (18121 or 18122) has a remaining listener. The standalone native-select
  reproduction also closed its browser in a finally block.
- Preserved source changes, generated dashboard outputs, dependencies, and raw
  validation evidence in the attached managed worktree and local evidence paths.
  The original shared checkout was not modified by this feature work.

The subsequent review/fix cycle and publication are recorded in
[the final publication review](debug-summary-har-final-review-2026-10-26.md).
