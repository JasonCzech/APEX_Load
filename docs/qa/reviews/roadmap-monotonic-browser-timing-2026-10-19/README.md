# Roadmap implementation: monotonic browser timing

Date: 2026-10-19. Scope: a bounded [TEL-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-a--durable-windows-and-measured-end-to-end-age) browser measurement prerequisite, following [durable controller cursors](../roadmap-durable-cursors-2026-10-19/README.md). Base: `07c25d409`.

## Contract

The dashboard records local receipt identity and monotonic time while handling each live metrics update. These records live in a weak map, outside wire JSON and history. The advisory `browser_render` report measures local receipt to a post-commit animation-frame/timer callback with `performance.now()`. Wall time labels the observation only. Invalid, regressed or overflowing measurements remain unavailable; they cannot become measured zero.

Receipt identity belongs to one run. Distinct receipts sharing a wall timestamp remain distinct; latency-only updates and repeated renders cannot resample a receipt. Disabled sessions discard pending samples, and a late refusal from an obsolete reporting session cannot stop a newer one. One bounded page-lifetime visibility listener records hidden transitions starting at the first receipt, including before React enables the reporter. Opaque visibility epochs preserve event ordering even at zero or reduced clock precision.

The existing authenticated report, per-viewer/tab quotas, opt-in behavior, 64-sample queue, API shape and advisory stage remain compatible. Superseded callbacks, hidden receipts, queue eviction and failed reports remain best-effort gaps. This change supplies neither a complete eligible-publication denominator nor cross-host calibration, publication correlation, a measured end-to-end distribution or proof of visible presentation. The callback is a paint opportunity. Existing archives are preserved; their timing method is not retroactively qualified.

## Validation

- Dashboard type checking and embedded build passed; lint passed. The existing bundle-size warning remains advisory.
- Five focused timing, stream and help unit files passed: **57 tests** (log (`evidence/render-unit-final.log`)).
- Real live-run telemetry and searchable/reloadable narrow help workflows passed in **both auth profiles, two tests each** (dev-admin (`evidence/render-dev-admin-reviewed.log`), local-auth (`evidence/render-local-auth-reviewed.log`)). The browser fixture steps the callback wall clock forward by two minutes while preserving bounded monotonic intervals and the advisory stage. Unit fixtures cover both forward and backward steps.
- Final telemetry table and narrow help screenshots were visually inspected in both profiles (dev-admin table (`evidence/dev-admin-stages.png`), local-auth table (`evidence/local-auth-stages.png`), dev-admin help (`evidence/dev-admin-help.png`), local-auth help (`evidence/local-auth-help.png`)). Missing generator measurements remain unavailable; browser timing remains explicitly advisory.
- The generated telemetry acceptance inventory passed and was refreshed. Documentation checks passed for **253 active Markdown files**; the in-app guide ownership/fragment scan reported no findings. `git diff --check` passed.
- Validation used Node 22.23.3 and Go 1.26.9 on macOS arm64, disposable local controllers/databases, and the maintained Playwright runner. Latest `origin/main` was integrated before builds. Browser sessions and controllers were closed by the runner. Dedicated build caches remain in use for subsequent implementation slices.
- This slice changes dashboard timing and documentation, not Go API production behavior. The preceding [durable-cursor validation](../roadmap-durable-cursors-2026-10-19/README.md) retains the full Go-suite evidence; it is not relabelled as a new run here. Local browser checks do not qualify cross-host end-to-end latency.

Raw final logs and screenshots are retained under `evidence/`, with SHA-256 hashes (`evidence/SHA256SUMS`).

## Review

Review found and fixed visibility transitions lost before reporter activation, non-finite rounded elapsed time that JSON could otherwise turn into null, repeated local receipts, stale-session refusals, the acceptance inventory's reference to a replaced visibility test, and an older guide paragraph that still claimed first paint. Regression assertions retain explicit unavailable and advisory behavior. A second review of final source, tests, generated inventory, help wording and retained evidence found no further actionable issues. Generated embedded bundles are validation outputs and are excluded from the scoped source commit.

## Remaining program dependencies

The user confirmed that separate reference hosts, Linux/Windows native runners, sanitized TruClient exports and a disposable multi-host recovery environment are unavailable. Their declared qualification gates remain pending. Implementation and local regression work continue; this local browser slice does not certify those environments or close TEL-A.
