# Roadmap Review — `BE-B6`: UI-Driven Managed Chain Across Chromium and WebKit Cohorts (2026-10-27)

## Contract

`BE-B6` qualifies one unstubbed Playwright E2E workflow in [`dashboard/e2e/workflow-browser-har.spec.ts`](../../../../dashboard/e2e/workflow-browser-har.spec.ts) that drives:

1. **Authoring in the dashboard UI**: Opening a `protocol: "browser"` script in [`ScriptEditor.tsx`](../../../../dashboard/src/components/ScriptEditor.tsx), selecting the target `Browser Engine` (`chromium` or `webkit`) and `HAR capture` policy (`always`) alongside `Capture page timings and Web Vitals` and `Capture interaction evidence` in [`ScriptRuntimeSettingsView.tsx`](../../../../dashboard/src/components/ScriptRuntimeSettingsView.tsx), and saving the script.
2. **Native browser replay in the UI**: Running a native browser replay session (`POST /api/v1/scripts/{id}/browser-replay/sessions` with `record_har: true`) from the `Native browser replay` section of [`ScriptBrowserEvidence.tsx`](../../../../dashboard/src/components/ScriptBrowserEvidence.tsx), waiting for `Verified ✓`, and downloading the native replay HAR (`X-APEXLoad-HAR-Fidelity: native`) and replay evidence ZIP through [`BrowserReplayProgress.tsx`](../../../../dashboard/src/components/BrowserReplayProgress.tsx).
3. **Managed run execution**: Launching a 1-iteration managed run from the `New Test` page (`/#/new-test`), waiting for run completion and archive manifest publication.
4. **Archived resource inspection and native HAR downloads without `page.route` stubs**: Navigating to the run report Diagnostics → Browser evidence view, opening the retained archive resource inspector against the real archived resource page, verifying query-token redaction (`?token=[REDACTED]`), drilling from the retained resource row into its native HAR entry, and downloading both the standalone native HAR (`fidelity: "native"`, no synthetic `_apexload` envelope) and the HAR-inclusive archive ZIP (`?include=har`).
5. **Persona RBAC enforcement in `local-auth`**: Verifying that a `viewer` bound to the domain lists both synthetic and native HARs (`can_download: false`), sees disabled `Download synthetic HAR` and `Download native HAR` controls with the editor-access notice, and receives `403 Forbidden` on direct native/synthetic archive HAR downloads, `?include=har` archive exports, and `/api/v1/browser-replay/sessions/{id}/har` + `/evidence`, while an unbound user is denied (`403`) across all endpoints.

## Invariants

- `ScriptRuntimeSettingsView` exposes `HAR capture` (`har_capture`: `off`, `always`, `on_error`, `on_sla_breach`, `manual`) under `Browser Emulation`, clearing `config.browser.har_capture` to `undefined` when reset to `off`.
- `ScriptEditor.tsx` wires `onStartNativeReplay` (`record_har: true`, calling `startBrowserReplaySession(scriptId, payload)` when `scriptId` is present and `startDraftBrowserReplaySession(payload)` for unsaved draft scripts without a `scriptId`) into `ScriptBrowserEvidence.tsx`, which renders `Run native browser replay` (fenced while starting or running) and `<BrowserReplayProgress>` with native replay HAR and evidence downloads.
- Neither `UI-driven Chromium managed chain authors, replays, runs and downloads native HARs without stubs` nor `UI-driven WebKit managed chain authors, replays, runs and downloads native HARs without stubs` installs any `page.route` stub.
- Raw query credentials (`secret-query-token`) never appear in the downloaded replay HAR, archived native HAR, per-request HAR download, or retained resource table.

## Validation

- **Unit & Lint Gates (`dashboard/`)**:
  - `npm run typecheck` — pass (`0` errors).
  - `npm run lint` (`eslint . --max-warnings=0`) — pass (`0` errors, `0` warnings).
  - `npm run lint:a11y` — pass (`0` blocking errors).
  - `npx vitest run src/components/ScriptRuntimeSettingsView.browserEmulation.test.tsx src/components/ScriptBrowserEvidence.test.tsx src/components/runReport/BrowserInteractionsPanel.test.tsx src/components/runReport/JourneyRiskPanel.test.tsx src/components/CommandCenterBrowserGroups.test.tsx src/labelCasing.test.ts` — pass (`6` files, `38` tests).
  - `npm run build && npm run test:bundle-budget && npm run bundle:check` — pass (all route budgets within limits).
- **Mutation Verification**:
  - Removed `setNativeReplaySessionId(sessionId)` in `ScriptBrowserEvidence.tsx` and verified `src/components/ScriptBrowserEvidence.test.tsx` fails immediately (`expected undefined to be 'replay-session-1'`).
  - Mutated `e.target.value === 'off' ? undefined : ...` in `ScriptRuntimeSettingsView.tsx` and verified `src/components/ScriptRuntimeSettingsView.browserEmulation.test.tsx` fails immediately.
- **Playwright End-to-End (`scripts/run-e2e.sh workflow-browser-har.spec.ts`)**:
  - `dev-admin`: `3 passed, 1 skipped (16.3s)` (controller log retained at `dashboard/test-results/controller-logs/dev-admin/run-LxIBny/selected.log`).
  - `local-auth` (`APEXLOAD_E2E_PROFILE=local-auth`): `4 passed (1.1m)` (controller log retained at `dashboard/test-results/controller-logs/local-auth/run-Rr33Tg/selected.log`).
- **Inventory & Documentation Checks**:
  - `APEXLOAD_UPDATE_BROWSER_ACCEPTANCE=1 go test ./docs/qa -run TestBrowserAcceptanceInventory` and `go test ./docs/qa` — pass.
  - `python3 scripts/qa-generate-dashboard.py --check` and `go test ./pkg/api -run TestDashboardDocs` — pass.
  - `python3 scripts/check-docs.py` — pass.

## Review

- **Round 1**:
  - Code-safety reviewer (`06f0a2b8-77fa-4e27-9b93-447fd15a05ae`): Found `0 High, 0 Medium, 2 Low` — add a synchronous `nativeReplayInFlightRef` fence and `nativeReplayRunning` state in `ScriptBrowserEvidence.tsx` so `Run native browser replay` stays disabled until `<BrowserReplayProgress>` completes or is dismissed, and clarify `startBrowserReplaySession` vs `startDraftBrowserReplaySession` in the review record. Fixed.
  - Test/QA reviewer (`d5dc1e44-5ff9-410c-b4ba-c5c1e7b90ba1`): Found `0 High, 1 Medium, 1 Low` — extend `ScriptBrowserEvidence.test.tsx` to cover resolved `sessionId` rendering `<BrowserReplayProgress>`, `nativeReplayDisabled`/`nativeReplayTitle`, and `onComplete`/`onDismiss` transitions, and record the exact verified mutations in the review record. Fixed.
- **Round 2**: Re-reviewed all Round 1 fixes; zero High, Medium, or Low issues remained.

## Remaining

- Local macOS Chromium and WebKit UI-driven managed chain cohorts are qualified; Linux and Windows UI-driven managed chain cohorts remain tracked per host/engine in [`docs/qa/BROWSER_ACCEPTANCE.md`](../../BROWSER_ACCEPTANCE.md).
