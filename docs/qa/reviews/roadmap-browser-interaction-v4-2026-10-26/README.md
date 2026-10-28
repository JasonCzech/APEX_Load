# QA Review Record — `BE-A1 + BE-A2 + BI-B1` (`FeatureBrowserInteractionEvidenceV4` & Authored Page Receipt v3)

- **Date:** `2026-10-27`
- **Branch:** `be-c1-vuser-attribution`
- **Packages:** `third_party/playwright-go`, `pkg/protocol`, `pkg/protocol/browser`, `pkg/distributed/generatorrpc`, `pkg/engine`, `pkg/api`
- **Roadmap Items:**
  - `BE-A1`: Child-Frame Interaction Mapping & Post-Action Geometry Provenance (`BrowserInteractionVersion4`)
  - `BE-A2`: Multi-Outcome Interaction Evidence (`Outcomes []BrowserInteractionOutcome` for `sync`, `dialog`, `download`, `assertion`, and `navigation`)
  - `BI-B1`: Authored-Page Binding v3 (`BrowserAuthoredPageVersion3`: `history_traversal`, `same_document_route`, `opened_by_action`, and `redirect_chain` with `RouteSeq`)

## Summary of Changes

1. **Playwright Navigation Commit & `CDPSession` Event Hardening (`third_party/playwright-go`):**
   - Extended `APEXNavigationCommit` and `frameImpl.onFrameNavigated` in `third_party/playwright-go/apex_navigation_commit.go` and `third_party/playwright-go/frame.go` to record `NewDocument`, `SameDocument`, and `Failed` flags from native `frameNavigated` protocol events.
   - Hardened `connection.Dispatch` and `replaceGuidsWithChannels` in `third_party/playwright-go/connection.go` so raw `CDPSession` events (such as `Page.downloadWillBegin` and `Page.downloadProgress`, which carry CDP download `guid` fields in multi-key maps) pass through without corrupting the Playwright channel table, and only single-key `{"guid": "..."}` maps are interpreted as Playwright channel references.

2. **Protocol Contracts (`pkg/protocol`):**
   - Added `BrowserAuthoredPageVersion3 = 3`, `RouteSeq uint64`, v3 association constants (`history_traversal`, `same_document_route`, `opened_by_action`, `redirect_chain`), and v3 unavailable reason constants in `pkg/protocol/browser_authored_page.go`.
   - Added `BrowserInteractionVersion4 = 4`, `"download"` pointer kind, `GeometryProvenance` (`"dispatch"` / `"post_action"`), `AuthoredPageID`, `RouteSeq`, `FrameChain` (`[]BrowserInteractionFrameHop` with `Invertible2D()` and `ApplyToPoint()`), and `Outcomes []BrowserInteractionOutcome` (`sync`, `dialog`, `download`, `assertion`, `navigation`) in `pkg/protocol/browser_interaction.go` and `pkg/protocol/browser_interaction_v2.go`.
   - Added `Begin`, `Finish`, and `ClampToDeadline` helpers to `BrowserOutcomeClock` in `pkg/protocol/browser_outcome_clock.go`.

3. **Distributed Capability Negotiation (`pkg/distributed/generatorrpc`, `pkg/engine`):**
   - Added `FeatureBrowserInteractionEvidenceV4 = "browser_interaction_evidence_v4"` in `pkg/distributed/generatorrpc/browser_documents.go`, negotiated v4 across homogeneous generator fleets in `pkg/distributed/generatorrpc/browser_interaction_version.go` (falling back to v3/v2/v1 on mixed fleets), and enabled v4 + v3 page bindings by default for local runs in `pkg/engine/config_protocols.go`.

4. **Browser Runtime Capture (`pkg/protocol/browser`):**
   - Implemented v3 authored-page bindings in `pkg/protocol/browser/authored_page.go` and `pkg/protocol/browser/authored_page_native_commit.go` for `browser_back` / `browser_forward` (`history_traversal`), same-document `browser_navigate` and post-action `history.pushState`/`replaceState`/hash transitions (`same_document_route` with monotonic `RouteSeq`), `browser_open_window` / `browser_switch_window` / action-opened popups (`opened_by_action`), and HTTP/client redirect chains (`redirect_chain`).
   - Extended the injected interaction observer in `pkg/protocol/browser/interaction_evidence.go` and `pkg/protocol/browser/interaction_dispatch.go` to walk `window.frameElement` up to `MaxBrowserInteractionFrameDepth = 8` same-origin hops, capturing 2D affine transforms, scroll/viewport offsets, border/padding, and explicit exclusion reasons (`cross_origin_ancestry`, `non_axis_aligned_or_3d_transform`, `frame_depth_exceeded`).
   - Recorded multi-outcome slices (`sync`, `dialog`, `download`, `assertion`, `navigation`) on v4 interactions in `pkg/protocol/browser/interaction_outcome_v2.go`, `pkg/protocol/browser/download_actions.go`, `pkg/protocol/browser/assertions.go`, and `pkg/protocol/browser/browser.go`.

5. **Controller Archive & Pattern Evaluators (`pkg/api`):**
   - Updated `archiveAuthoredPageAccumulator` in `pkg/api/archive_browser_authored_pages.go` to key bindings by `(SourceGeneration, DocumentID, LifecycleID, RouteSeq)` so distinct SPA route transitions within a single document lifecycle are retained without false conflict drops.
   - Updated `pkg/api/browser_interaction_patterns.go` to project v4 child-frame points into top-level viewport coordinates via `BrowserInteractionFrameHop.ApplyToPoint` (`MappedViaTransform = true`), exclude `post_action_geometry` and unmapped frames from heatmaps, and evaluate all declared outcomes in `DeclaredOutcomes()`.

## Verification & Mutation Testing

- **Targeted & Package Tests:**
  - `APEXLOAD_BROWSER_TESTS=require go test ./pkg/protocol ./pkg/protocol/browser ./pkg/distributed/generatorrpc ./pkg/engine -count=1`
  - `APEXLOAD_BROWSER_TESTS=require go test ./pkg/api -run "TestBrowser|TestArchive.*Browser|TestArchiveAuthoredPage" -count=1`
- **Overlay Mutation Checks (`go test -overlay`):**
  1. `pkg/protocol/browser_interaction_v2.go`: Disabling the non-empty `FrameChain` requirement on dispatch-qualified `child_frame` v4 interactions was killed by `TestBrowserInteractionV4OutcomesAndFrameChainContract`.
  2. `pkg/protocol/browser_authored_page.go`: Disabling the `RouteSeq > 0` requirement on `same_document_route` v3 bindings was killed by `TestBrowserAuthoredPageBindingV3Contract`.
  3. `pkg/api/browser_interaction_patterns.go`: Disabling `post_action_geometry` heatmap exclusion was killed by `TestBrowserInteractionV4ChildFrameMappingAndExclusions`.
  4. `pkg/api/archive_browser_authored_pages.go`: Omitting `RouteSeq` from the `archiveAuthoredPageAccumulator` dedup key was killed by `TestArchiveAuthoredPageAccumulatorRetainsDistinctRouteSeqs`.
