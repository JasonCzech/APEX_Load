# Native HAR capture omission retention — 2026-10-20

A native capture can fail before publishing an artifact. Previously that left a successful managed run with no native HAR and no capture-loss count in its archive. The capture owner now hands off bounded, closed-reason omission counts through local and distributed archive sinks, including their combined wrappers. Generator archive collection preserves those counts. The existing authorized HAR listing exposes the incomplete state.

A capture owner reports at most once. Deadline exhaustion, unretired ownership, joined capture failure and publication-queue exhaustion remain distinct. Invalid provider status becomes a sanitized unavailable count. Deliberate capture-policy discard remains uncounted. Reporting never changes physical ownership, retirement eligibility, target run outcome or target traffic.

The real managed fixture uses an owned loopback target and a private capture directory filled with unrelated files to exceed its bounded retention scan. It verifies successful target execution, no advertised native artifact, retained omission in the finalized manifest/listing, and unchanged unrelated files. It is local Chromium evidence, not Linux/Windows crash, actual-device or replay-durability qualification.

## Validation and review

- Full Go suite: **35,676 passing tests/subtests**, 38 explicit skips; native browsers required. All stable private HAR import compatibility results match the earlier qualified import.
- Targeted race coverage passed **42 tests/subtests** across three repetitions, including the real managed failure path.
- Dashboard typecheck, lint, 31 Help unit tests and build passed. HAR/resource/Help Playwright workflows passed: dev-admin 21 passing with two auth-specific skips, local-auth 23 passing. The embedded ownership article exactly matches its repository source. Documentation links passed for 301 active Markdown files.
- Review fixed combined-sink forwarding and the ownership-label finding; the final round is clean. `evidence/review-rounds.json`, `evidence/validation-summary.json`, `evidence/tested-source-hashes.json`, `evidence/tested-asset-hashes.json` and `evidence/SHA256SUMS` retain the validation scope.

Earlier fixture/forwarding diagnostics and the full run before the final ownership-label correction are retained as such; they do not replace the final full-suite and race evidence. Raw evidence stays beside this Markdown record on disk in accordance with the parallel QA evidence cleanup; only the review record is committed. No private source HAR bytes are committed.
