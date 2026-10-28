# Roadmap Review — Local TruClient Replay Primitives (`BE-B7a`, 2026-10-26)

## Scope

This slice implements the local browser DSL, runtime, and TruClient importer primitives required to convert and replay supported TruClient 2.0 Web and Mobile Web step shapes without false semantic review gates:

1. **Append typing (`browser_fill` `params.clear: false`):**
   - Added `params.clear` (`FieldKindBool`) to `browser_fill` in `pkg/dsl/catalog_browser.go` (validated in `browserFillChecks`, regenerated in `docs/dsl.schema.json` and `docs/DSL_ACTION_REFERENCE.md`).
   - Updated `fillAction` in `pkg/protocol/browser/frame_actions.go` so `input_mode: "type"` with `clear: false` skips `locator.Fill("")`, focuses the element and places the selection caret at the end of existing text, and dispatches per-character keyboard events via `locator.PressSequentially`.
   - Updated TruClient `Type` step conversion in `pkg/convert/trueclient/steps.go` to map `Clear=false`/`0` to `action.Params["clear"] = false` without raising `truclient_append_typing_requires_review`.
2. **Tab close lifecycle (`browser_close_window`):**
   - Added `browser_close_window` (`target`, `params.index`, `sync`, `capture`) to `pkg/dsl/catalog_browser.go`, `pkg/dsl/browser_target.go`, and `pkg/dsl/catalog_test.go`.
   - Implemented `dispatchCloseWindow`, `tryCloseWindow`, `selectWindowToClose`, and `finalizePageDocuments` in `pkg/protocol/browser/frame_actions.go` (wired in `pkg/protocol/browser/action_dispatch.go` and `browserPageNavigation` in `pkg/protocol/browser/browser.go`), finalizing document evidence on the closing page's frames before `targetPage.Close()` so `page.OnClose` never marks the session's document clock incomplete, refusing to close the last remaining open page, and switching scope to an open remaining page.
   - Updated `switchWindowForAction` and `Close Tab` conversion in `pkg/convert/trueclient/steps.go` to emit `browser_close_window` without raising `truclient_tab_lifecycle_requires_review` or duplicating window-switch actions.
3. **Pre-armed download coalescing (`Click` + `Wait for File Download` → `browser_download`):**
   - Added `canCoalesceTruClientDownload` and `coalesceTruClientDownloadStep` in `pkg/convert/trueclient/converter.go` (plus `convertWaitForDownloadStep` and `applyTruClientDownloadArguments` in `pkg/convert/trueclient/steps.go`) so a selector-backed `Click` followed by `Wait for File Download` coalesces into a single pre-armed `browser_download` action while preserving `source_download_step_id` in `applyXMLAnchoredTransactions` (`pkg/convert/trueclient/source_contract.go`). Standalone selectorless `Wait for File Download` steps remain `source_review`.
4. **Network-idle completion (`Step network completed` + `NetworkEndThreshold`):**
   - Added `"Step network completed"` to `applyTruClientStepContract` and `applyTruClientCompletionContract` in `pkg/convert/trueclient/source_contract.go` (`action.Sync = &dsl.BrowserSyncPolicy{Type: "network_idle"}`).
   - Mapped `[TruClient] NetworkEndThreshold` in `pkg/convert/enterprisearchive/truclient_runtime.go` to `actions[].sync.stable_ms`.
   - Added `waitForNetworkQuiet` in `pkg/protocol/browser/wait_actions.go` backed by `lastNetworkActivity` / `inFlight` tracking in `pkg/protocol/browser/resource_evidence.go` so `network_idle` waits for in-flight post-interaction requests and the configured quiet window.

Sanitized real customer/VuGen `truclient2_web` and `truclient_mobile_web` export archives and external physical mobile device qualification remain tracked under `BE-B7b`.

## Verification

- **Unit & Importer Contract Tests:**
  - `go test -v -run "TestTruClientReplayPrimitivesAppendTypingCloseTabDownloadAndNetworkIdle" ./pkg/convert/trueclient` — PASS
  - `go test -v -run "TestPlannedTruClientBrowserDraftFinalizesSettingsParametersAndProvenance" ./pkg/convert/enterprisearchive` — PASS
  - `go test ./pkg/dsl/... ./pkg/convert/trueclient/... ./pkg/convert/enterprisearchive/...` — PASS
- **Live Chromium & WebKit Integration Replay:**
  - `go test -v -run "TestBrowserFillClearValidationAndCloseWindowUnit|TestTruClientReplayPrimitivesLiveChromiumAndWebKit" ./pkg/protocol/browser` — PASS on both `chromium` (`1.32s`) and `webkit` (`1.38s`), verifying append typing (`settled:prefix-suffix`), `Step network completed` quiet-window synchronization, popup tab activation and `Close Tab` with complete document evidence (`!docEvidence.Incomplete`), and pre-armed `browser_download` file save + v4 `download` outcome satisfaction.
- **Mutation Verification (`go test -overlay`):**
  - Mutation 1 (`be-b7a-overlay-clear.json` ignoring `params.clear: false` in `pkg/protocol/browser/frame_actions.go`): `TestTruClientReplayPrimitivesLiveChromiumAndWebKit` fails as expected (`settled:-suffix` instead of `settled:prefix-suffix` on both Chromium and WebKit).
  - Mutation 2 (`be-b7a-overlay-coalesce.json` disabling `canCoalesceTruClientDownload` in `pkg/convert/trueclient/converter.go`): `TestTruClientReplayPrimitivesAppendTypingCloseTabDownloadAndNetworkIdle` fails as expected (`ReviewOnlySteps: 1`).
