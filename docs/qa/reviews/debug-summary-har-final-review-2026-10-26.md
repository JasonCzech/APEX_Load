# Debug summary and HAR publication review — 2026-10-26

Scope: the retained HAR capture controls and expandable transaction summary
changes following `819563141`. The user requested a review/fix cycle and a direct
commit/push to main. Work remains isolated from the shared dirty checkout.

## Review cycle

The first publication review covered all source changes and generated contracts,
including capture propagation, body limits, redaction, timing provenance,
observation identity, session authorization/expiry, frozen transaction structure,
repeated executions, evidence links, and keyboard navigation.

Findings fixed with regression tests:

1. HTTP exports with measured send timing incorrectly claimed that wait included
   send. `wait_includes_send` now reflects whether send was separately measured.
2. Closing an earlier overlapping transaction discarded the later transaction's
   matching end marker. Crossing spans are now represented as siblings, so each
   retains its own recorded outcome and complete boundaries. Incomplete started
   transactions also link to their start evidence.
3. The HTTP observer treated pre-dispatch policy failures as retained traffic and
   could hide the final observed redirect when a proposed successor was refused.
   Capture now requires a connection attempt or an observed response. Failed
   connection attempts remain retained, even with detailed timings disabled;
   rejected authored targets remain absent. A redirect is intermediate only when
   its successor was actually observed.

The second review inspected these fixes and the complete staged source set.
It found no further actionable findings. Generated dashboard build artifacts
are excluded from the source commit, consistent with their existing ignore rules.

## Validation

The final dashboard build (including both TypeScript projects), lint, all 8,715
unit tests across 750 files, and focused Go regression/race checks passed.
All nine affected debugger Playwright workflows passed in each authentication
profile (18 passes), including actual HTTP/browser execution and downloaded HARs.
The affected Go linters reported zero issues. HAR API authorization, expiry,
option validation, OpenAPI freshness, and generated TypeScript contract checks
passed separately. Documentation checks passed.

The repository-wide `go test -p 4 -timeout=20m -json ./...` completed without a
timeout: all 169 non-API tested packages passed, and 37,703 test cases/subtests
passed. The API package finished in 971.749 seconds with only the six existing
native replay-artifact cleanup failures listed below. Their failure messages
match the previous unchanged-main reproduction; the full Go gate is therefore
not green. This feature does not change native HAR ownership or disposal.

- `TestReplayArtifactHTTPCleanupLifecycle`
- `TestReplayArtifactHTTPProjectDeletionAndNamespaceCleanup`
- `TestReplayArtifactRuntimeRecoversNativePair`
- `TestReplayArtifactRuntimeCleanupRequiresSourceAndJoin`
- `TestReplayArtifactRuntimeCleanupTraversesForeignPage`
- `TestReplayArtifactRuntimeCleanupThroughStoreOverlay`

Baseline evidence and reproduction are described in
[the earlier completion-summary review](debug-completion-summary-2026-10-25.md).
An API test inventory audit confirmed that every top-level test completed or was
explicitly skipped by existing platform/integration gates. The follow-up batch
covered the 17 gated skips and completed successfully; no tail was left untested
by an aggregate timeout in this run.

Raw evidence: `/private/tmp/apex-debug-review-build.log`,
`/private/tmp/apex-debug-review-lint.log`,
`/private/tmp/apex-debug-review-unit.log`,
`/private/tmp/apex-debug-review-dashboard-full.log`,
`/private/tmp/apex-debug-review-regressions.log`,
`/private/tmp/apex-debug-review-race.log`,
`/private/tmp/apex-debug-review-api.log`,
`/private/tmp/apex-debug-review-contracts.log`,
`/private/tmp/apex-debug-review-docs.log`,
`/private/tmp/apex-debug-review-go-full.log`,
`/private/tmp/apex-debug-review-go-lint.log`,
`/private/tmp/apex-debug-review-api-test-list.log`,
`/private/tmp/apex-debug-review-api-remaining-list.txt`,
`/private/tmp/apex-debug-review-api-remaining.log`,
`/private/tmp/apex-debug-review-e2e-dev-admin.log`,
`/private/tmp/apex-debug-review-e2e-local-auth.log`, and
`/private/tmp/apex-debug-review-e2e/`.

## Publication and cleanup

The reviewed source is committed for the user-requested direct push to main.
`origin/main` was fetched again after validation and had no incoming changes.
The shared checkout and its unrelated source changes were preserved.

No session browser/controller processes or listeners on ports 18121/18122
remained. The isolated Go build/test cache was cleaned from approximately
5.4 GiB to 8 KiB. The 54 MiB linter cache, disposable frontend caches, and temporary
dependency symlink were removed; installed dependencies and generated deliverable
assets were preserved. Docker build-cache prune completed with 0 B reclaimed.
