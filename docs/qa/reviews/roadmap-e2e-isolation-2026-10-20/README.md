# Browser workflow isolation and roadmap integration — 2026-10-20

This follow-up integrates the committed [decimal precision-consent slice](../roadmap-native-precision-2026-10-20/README.md) with main at `c90fa4cd148bab1c8446838a2fa713bc9a43ca54`, including the peer fixes already merged through [PR #254](https://github.com/JasonCzech/APEX_Load/pull/254). Main's graph changes are retained. The broader [implementation plan](../../../ROADMAP_IMPLEMENTATION_SPEC.md) and external qualification gates remain open.

## Runner contract

Whole-suite invocations use installed Playwright discovery and a separate disposable controller, database, archive directory, import-asset directory and protected-session store for each selected file. Tests within one file share that store, and each profile builds the controller once. Both import roots override inherited environment values. Production admission limits remain unchanged. Explicit CLI filters/options retain Playwright's argument semantics and use one disposable controller.

Failed files do not suppress later files, and any failure makes the profile fail. Discovery, startup and auth-profile failures stop the profile. Complete controller logs live outside Playwright's cleared output directories. Signal cleanup stops owned processes before removing their stores. The existing CI artifact upload includes the new directories. See the [operating instructions](../../E2E_SUITES.md#normal-code-change-cycle).

The first broad local-auth run reproduced three LoadRunner attestation failures whose logs explicitly report protected import-session capacity exhaustion, plus a JMeter publication 409 with no recorded inner cause. Discovering shared filesystem import roots prompted an intentional stop; shared user data was left intact. Database/archive isolation alone was insufficient. Regression tests now verify private import roots and unchanged inherited-root sentinels. All four cases pass in the final broad run and the focused import repetition. That establishes current regression coverage without establishing the unrecorded historical cause of the JMeter error.

## Bundle inventory

An independent build of main measured 2.40 MiB of aggregate JavaScript gzip, versus 2.43 MiB for the candidate. The new lazy native analytics Help article accounts for 24,664 bytes of the 26,078-byte increase; the remaining growth is predominantly the deferred precision-review UI. The aggregate gzip inventory cap moves from 2.42 to 2.45 MiB. Per-asset, raw JavaScript, CSS and all route/startup limits are unchanged. Initial shell, Scripts and Test execution remain approximately 200/228/411 KiB gzip. The boundary regression charges lazy Help to aggregate size while keeping it outside the startup route. The original failed budget check, exact-main source inventory and measurements remain retained.

## Validation

The validation summary (`evidence/validation-summary.json`) and parameterized commands (`evidence/validation-commands.json`) distinguish complete checks from failed or interrupted invocations.

| Check | Result |
| --- | --- |
| Default Go suite | 168 packages passed; 17,789 top-level tests and 17,722 subtests passed. The 33 top-level and 5 nested environment/feature skips remain skips. |
| Native analytics | Real DuckDB worker: 165 top-level tests / 310 subtests passed. Native API: 3 / 16 passed. |
| PostgreSQL consent/attempt fences | Actual disposable PostgreSQL: 1 top-level test / 9 subtests passed. Earlier availability failures were superseded by this actual run. |
| Dashboard stages | Typecheck, lint, 8,044 unit tests across 722 files, and production build passed. |
| Dev-admin browser profile | 89 selected files, 337 selected tests: 331 passed / 6 skipped / zero failures; separate frozen-source invocation exited 0. |
| Local-auth browser profile | 67 selected files, 271 selected tests: 270 passed / 1 skipped / zero failures. |
| Focused JMeter/LoadRunner repetition | 20 passed / 1 skipped / zero failures. |
| Runner and bundle regressions | 14 runner/preflight tests and 7 bundle tests passed; Bash syntax and targeted lint passed. |
| Final generated/static contracts | 2 packages, 12 top-level tests / 8 subtests passed. Native analytics Help is byte-equal to its Markdown source; both profiles exercise Help navigation. |

Exact browser accounting (`evidence/browser-final-accounting.json`) reconciles each selected file and test with its JUnit report. Both final precision-panel captures are byte-identical; visual inspection confirms that the long column name wraps and controls remain contained. Browser fixtures exercise the consent UI, while the native API/worker tests exercise real DECIMAL normalization.

The original final `npm run test:cycle` invocation **exited 127 and remains a failed invocation**. It overlapped edits to both shell files: dev-admin stopped with a parsing error before discovery, and the outer wrapper later encountered a truncated loop terminator after local-auth completed. All non-browser cycle stages passed. The frozen dev-admin repetition and complete local-auth JUnit reports establish browser coverage separately. Direct frozen-wrapper regressions verify both-profile success, continued execution after a first-profile failure, correct aggregate exit status and port forwarding. See the timing and process record (`evidence/cycle-startup-overlap.json`). This record does not relabel that original cycle as a pass.

The attached private HAR was imported through the real endpoint without replaying target traffic or retaining its executable payload. All ten aggregate fields match the earlier Java baseline, excluding the previously documented randomized script hash: 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, 4 warnings, zero DSL issues and 264 protected requirements. The original archive hash was rechecked unchanged. The comparison (`evidence/private-har-comparison.json`) and disclosure scan (`evidence/private-har-log-disclosure-review.json`) retain the result without the source payload; the latter checks capture hostnames in validation output, not general payload redaction.

## Review and preservation

Review rounds (`evidence/review-rounds.json`) record repairs for path canonicalization, fixture teardown ordering, platform-specific tests, filesystem import isolation, measured bundle growth and timestamp wording. Review also corrected the validation inventory: 38 obsolete staged bundle names were absent from the actual build. The original inventory remains retained; the current manifest (`evidence/current-validation-source-hashes.json`) covers the actual validated source and assets. The only later source change adds wrapper regression tests; production source and embedded assets remain unchanged.

The parallel-work audit (`evidence/worktree-runner-overlap.json`) found no duplicate runner isolation implementation in the other checked worktrees. Published peer fixes were integrated from main. Both originally dirty primary-checkout files retain their original hashes; additional parallel work is excluded from this commit, as recorded by the read-only primary audit (`evidence/primary-checkout-after.json`).

Task-private Go/test and frontend build caches were cleared after dependent checks completed. Owned browser/controller processes exited, and the disposable PostgreSQL fixture was stopped. Shared Docker build-cache cleanup is deferred while parallel work remains active. Source, dependencies, deliverable outputs, images, containers, volumes, application data and evidence are preserved; see the cleanup record (`evidence/cache-cleanup.json`).

## Evidence and limits

Bounded `*-artifacts-*.tar.gz` archives retain the complete browser output of failed, interrupted and successful attempts. Each has a per-file SHA-256 inventory; JUnit reports are also retained individually under `evidence/junit/`, gzip-compressed without changing their bytes. The archive verification (`evidence/artifact-archive-verification.json`) checks every retained file. Full Go output and controller/browser logs are compressed without changing their contents. Earlier failures remain separate from final results. `SHA256SUMS` covers this record and its evidence.

This is local darwin/arm64 validation. Separate reference-host performance, Linux/Windows native packaging, real TruClient exports and multi-host recovery remain unqualified. None is promoted by this record. The original precision review remains an unchanged historical record; this follow-up records the later integration and browser results.
