# Browser Ground-Truth Interaction Fixture (50+ Groups), Trusted Mobile Taps & SW Cache Provenance (`BE-A3`)

## Scope

- **Roadmap slice:** `BE-A` (`BE-A3` — Ground-truth interaction fixture 50+ groups, trusted mobile taps, and service-worker/HTTP cache provenance)
- **Date:** 2026-10-26
- **Worktree / branch:** `be-c1-vuser-attribution`

## Summary of Changes

1. **Trusted Mobile Single-Finger Taps ([touch_actions.go](../../../../pkg/protocol/browser/touch_actions.go), [interaction_evidence.go](../../../../pkg/protocol/browser/interaction_evidence.go)):**
   - Updated `shouldUseSyntheticTap()` to return `!b.touchMode` so `browser_tap` uses Playwright's native `locator.Tap()` when `hasTouch` is enabled (`b.touchMode == true`), delivering trusted (`event.isTrusted === true`) `pointerdown`/`touchstart`/`click` events while retaining `waitVisibleWithTimeout` on main-frame taps and falling back to `browserSyntheticTapScript` when `touchMode` is disabled.
   - Multi-finger and path gestures (`browser_double_tap`, `browser_long_press`, `browser_swipe`, `browser_drag_and_drop`, `browser_pinch_zoom`) continue to use synthetic touch dispatch and explicitly report `UnavailableReason: "synthetic_dispatch"`.

2. **Per-Response CDP Service-Worker & HTTP Cache Provenance ([resource_association.go](../../../../pkg/protocol/browser/resource_association.go), [BROWSER_DOCUMENT_EVIDENCE.md](../../../guides/BROWSER_DOCUMENT_EVIDENCE.md)):**
   - Extended `cdpRequestRecord` and `observeCDPResponse` / `observeCDPCacheHit` to capture CDP `response.fromServiceWorker` (`ServiceWorker: *bool`, `ServiceWorkerUnavailableReason: ""`) alongside HTTP disk/prefetch/memory cache provenance (`Cache: *bool`, `CacheUnavailableReason: ""`), setting `Protocol: ""` and `ProtocolUnavailableReason: "service_worker"` when `fromServiceWorker` is `true` and keeping `ServiceWorker == nil` (`"not_exposed"`) on WebKit.

3. **52-Interaction-Group Ground-Truth Fixture & Cross-Cohort Qualification ([interaction_groups_ground_truth_integration_test.go](../../../../pkg/protocol/browser/interaction_groups_ground_truth_integration_test.go)):**
   - Added `pkg/protocol/browser/testdata/interaction_groups/` (`index.html`, `frame.html`, `app.css`, `sw.js`) with 50 main-frame interaction targets (`#grp-btn-1..40`, `#grp-link-41..45`, `#grp-input-46..50`) and 2 child-frame targets (`#iframe-btn-51`, `#iframe-btn-52`), seeded INP outliers (`350 ms` on group 1, `250 ms` on group 2, `64 ms` on group 50), 3 stage-chained layout shifts across 2 session windows, a ServiceWorker answering `/sw-cached.json`, and a cached `/app.css` stylesheet reused by `#child-frame`.
   - Verified across `chromium_desktop`, `chromium_pixel_8`, `webkit_desktop`, and `webkit_iphone_15_pro` that:
     - All 52 distinct interaction groups receive `event.isTrusted === true` (including `browser_tap` on `pixel_8` and `iphone_15_pro`).
     - At `interactionCount >= 50`, `web-vitals` `onINP` selects rank `floor(N/50) = 1` (`250 ms`) rather than the rank-0 maximum (`350 ms`).
     - Chromium `onCLS` matches the maximum session-window score (`Session Window 1`) rather than the total shift sum, while WebKit reports `Available: false, Value: nil, UnavailableReason: "unsupported"` for `cls`.
     - Chromium reports `ServiceWorker == true` (`ProtocolUnavailableReason == "service_worker"`) on `/sw-cached.json` and `Cache == false` then `Cache == true` (`ProtocolUnavailableReason == "cached"`) on `/app.css`, while WebKit preserves `Cache == nil` (`CacheUnavailableReason == "not_exposed"`).
     - `window.__apexGroundTruth` is read once prior to teardown with zero `/sidecar` or probe HTTP requests, no leftover provisional observations or duplicate final `MeasurementID`s, and complete secret redaction across interaction routes and resource origins.

## Verification & Mutation Evidence

- `APEXLOAD_BROWSER_TESTS=require go test -v ./pkg/protocol/browser -run "TestBrowserDocumentActualGroundTruthInteractionGroups|TestBrowserProtocolTouchActions|TestBrowserTapFallsBackToSyntheticWhenTouchModeOff|TestBrowserTapUsesSyntheticPath|TestCDPJoinAssociatesDocumentsCacheAndInitiators|TestBrowserActualTouchGesturesSaySyntheticDispatch|TestBrowserInteractionDispatchV2UnavailablePopulations" -count=1` — PASS (`19.03s`)
- `APEXLOAD_BROWSER_TESTS=require go test -v ./pkg/api -run "TestBrowserIntelligenceGoldenAcceptance" -count=1` — PASS (`5.96s`)
- `go test -overlay` mutation 1 (`shouldUseSyntheticTap` returning `true` in `pkg/protocol/browser/touch_actions.go`) — fails `TestBrowserTapUsesSyntheticPath` as expected.
- `go test -overlay` mutation 2 (`cdpServiceWorker(response)` returning `false` in `pkg/protocol/browser/resource_association.go`) — fails `TestCDPJoinAssociatesDocumentsCacheAndInitiators` as expected.
