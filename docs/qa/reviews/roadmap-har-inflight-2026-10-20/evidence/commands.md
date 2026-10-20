# Validation commands and scope

The isolated task checkout uses a session-local Go cache and scratch directory, GOMAXPROCS=2, GOGC=30 and GOFLAGS=-p=1. Main is fetched and checked integrated before explicit builds and full API suites. Dashboard assets finish generation before Go builds/tests that embed them.

- Draft `go test -overlay=<browser-overlay> -count=1 -timeout=5m -json ./pkg/protocol/browser` with `APEXLOAD_BROWSER_TESTS=require` exercises the real installed Chromium/WebKit runtimes. The reviewed draft includes synthetic-owner watchdog cleanup.
- Draft and applied `go test -race -count=1 -timeout=5m -json ./pkg/protocol/browser -run TestHARInFlight` exercise exact limits, sequential completions, stale generations, native close monitoring and cancellation overlap.
- Applied browser package, full Go suite and generated browser acceptance inventory are retained separately.
- `APEXLOAD_UPDATE_BROWSER_ACCEPTANCE=1 go test ./docs/qa -run TestBrowserAcceptanceInventory` regenerates the code-owned matrix.
- Dashboard typecheck, lint, relevant Help unit tests and build precede `scripts/run-e2e.sh workflow-help.spec.ts` in dev-admin and local-auth profiles. Each script owns and removes its disposable controller/database/browser session.
- `python3 scripts/check-docs.py` checks repository links and documentation invariants.
- The private HAR compatibility harness adds a temporary test through an external Go overlay. It POSTs the original ZIP to the real import handler; converted-script output is disabled. Compare safe summary fields with the retained baseline, excluding random script/attestation hashes. No capture or converted customer content is committed.

These commands do not establish reference-host, multi-host, Linux/Windows or actual-device qualification. Draft overlays are identified separately from installed-source results.

The final applied full Go suite includes only the external private-HAR compatibility test via a temporary Go overlay. All production and ordinary test files come from the installed checkout. Its actual multipart import and safe summary are therefore part of the same full-suite gate, not a separately claimed repeat. The overlay does not change production code, and the customer capture and converted content are never added to the repository.

The first installed browser package log is retained as failed (applied-browser-first). Five diagnostic native navigation repetitions passed, but a deterministic dependency-module regression then reproduced a lost load-state event at waiter registration. sdk-load-state-before records the failure. sdk-lifecycle-race validates the fix together with the owned-HAR extension; sdk-packaging validates the explicit vendor manifest and CI/Make test selection. Navigation-fixed repeats both actual engines after the fix. Help-before-sdk logs remain superseded controller cohorts; final Help runs rebuild the controller with the SDK correction.
