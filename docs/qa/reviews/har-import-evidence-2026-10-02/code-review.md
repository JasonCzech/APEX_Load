# HAR fidelity code review — 2026-10-02

Scope: the HAR response decoding, framing/version projection,
correlation degradation, BOM/proxy handling, shared Salesforce observations,
route-specific upload limits, dashboard file validation, synthetic regressions
and disclosure-safe capture reports.

## Findings fixed

**P2 — Base64 C1 text was mistaken for compressed bytes.** The new decoded-text
check rejected every Unicode control character, including UTF-8 C1 characters
that legitimately occur in JavaScript literals. Raw text was preserved, but the
same resource under HAR base64 encoding was incorrectly decompressed using stale
wire headers, falling back to its base64 spelling and losing response analysis.
The check now distinguishes binary-looking ASCII control bytes while accepting
UTF-8 C1 text. A regression reproduces the failure on the reviewed code under
retained gzip, deflate, Brotli and zstd headers, then passes with the correction.
Compressed-byte compatibility, corrupt-data warnings and expansion bounds remain
covered by the existing tests.

**P2 — Generated QA dashboard was stale.** The new browser regression adds a
Playwright spec file. The integrated documentation freshness test detected that
the generated inventory still showed 55 files. Regeneration updates it to 56;
the freshness regression and the complete integrated Go suite pass.

## Final review

Checked parser/source preservation, request accounting, correlation provenance
and incomplete-analysis behavior, binary-body rewrite guards, repeated headers,
Content-Length rejection paths, transport version gates, upload authorization
and size envelopes, primary/comparison picker validation, and test/evidence scope.
No further actionable findings were identified in this review. Recorded-host
replay remains outside the evidence; known source/runtime blockers are retained.

The changes are integrated with main at `2314ab698c805f5b75ac8b984143a6389deef03b`.
The integrated result passes the complete Go suite, dashboard checks and both
browser auth profiles. See
validation results (`additional-validation.json`).
