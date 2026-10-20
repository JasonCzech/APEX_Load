# Native HAR outstanding-request limit — 2026-10-20

This [BE-A / BE-3 slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#be-a--stable-authored-identity-and-qualified-document-evidence) closes an active native HAR context when its synchronous request callback observes more than 256 outstanding issued requests. Browser-queued requests count. The existing exact-owner close monitor performs closure, marks affected document observations censored and exposes `har_in_flight` as a resource failure. It does not add routing or change cache behavior.

This is overflow-triggered closure, not a scheduler or a guarantee that no callback arrives after the limit is crossed. The 512-entry request-identity inventory remains a separate bound. Completed sequential requests release their count; non-recording contexts retain their prior limits. Cancellation and overflow join the same native writer. A caller deadline cannot remove its working file or release its reservation before physical join; the existing shared 30-second finalization deadline is unchanged.

## Validation and review

- Applied browser package: 387 top-level tests and 335 subtests passed, including actual Chromium/WebKit flood closure and document censoring. Environment-specific skips remain in the log.
- Applied race suite: 4 top-level tests passed for limits, counting, stale generations, close ownership and cancellation overlap.
- SDK lifecycle race suite: 9 top-level tests and 14 subtests passed. The identical controlled load-state regression fails with only the registration fix removed; five native navigation repetitions passed on each engine.
- Full Go suite: 168 packages, 17,654 top-level tests and 17,311 subtests passed. A test-only external overlay exercised the actual private HAR import in this same gate; all production code came from the installed checkout.
- The attached ZIP imported successfully: HTTP 200, 803 source requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings and zero DSL issues. Aggregate fields matched the preceding compatibility cohort.
- Type checking, lint, 34 Help units, embedded dashboard build, Help workflows in both auth profiles, generated browser inventory, documentation checks and evidence checksums passed. Exact workflow counts and source/controller hashes are retained in [the evidence bundle](SHA256SUMS).

The private customer ZIP was exercised only through the real multipart import endpoint, without replaying traffic or saving converted content. Retained evidence contains aggregate counts and source hashes only. The matching import result does not claim byte-identical generated IDs or protected attestations.

Review corrected the native fixture's invalid action field and an existing synthetic-owner cleanup that left watchdogs alive beyond their tests. The original failed compile log is retained. An installed native navigation failure also led to a deterministic SDK regression for a load event lost between its state snapshot and listener registration. A narrow registration lock fixes that gap without extending timeouts or emitting synthetic events; explicit CI/Make dependency tests and vendor provenance cover the patch. The original failure and passing diagnostic repeats remain distinct evidence. Final applied-source and staged review found no remaining actionable findings after the fixture corrections and the independently reproduced SDK load-state registration fix.

An earlier full-suite attempt hit the unchanged 1 GiB archive free-space reserve and was stopped. Its failed/incomplete log is retained. After all session builds stopped, cleanup removed 11,004,120,362 bytes of obsolete cached API objects and abandoned scratch directories; the complete retry used the same source and limits.

Local macOS Chromium/WebKit tests establish this overflow/ownership behavior only. Linux/Windows native crash qualification, reference-host performance, actual-device qualification and real sanitized TruClient migration fixtures remain pending. No external qualification gate is promoted.
