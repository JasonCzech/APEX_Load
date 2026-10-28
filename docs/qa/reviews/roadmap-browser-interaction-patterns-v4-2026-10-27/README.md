# Browser Interaction Patterns v4 and UI/Report v4 Qualification Review (`BI-B2`)

## Scope

This review covers `BI-B2` (`browser_interaction_patterns.v4` + UI/Report `.v4`):

- **`browser_interaction_patterns.v4` Evaluation (`pkg/api/browser_interaction_patterns.go`):**
  - Emits `Version: "browser_interaction_patterns.v4"` when raw v4 interaction evidence (`protocol.BrowserInteractionVersion4`) is present, while preserving `"browser_interaction_patterns.v3"` for raw v2/v3 archives and `"browser_interaction_patterns.v2"` for historical v1 archives.
  - Evaluates all declared outcomes in `g.DeclaredOutcomes()` without stopping after the first missed outcome:
    - Navigated interactions (`UnavailableReason == "document_changed"` or a `navigation` outcome with `Observed == "met"` or `Reason == "unexpected_navigation"`) emit `unexpected_navigation` for each missed in-page outcome (`element_visible`, `visible`), `assertion` outcome, or unexpected `navigation` outcome, never `dead_interaction`.
    - Non-navigated interactions emit `dead_interaction` for each missed page-response outcome (`sync` with `element_visible`/`visible`/`load`/`domcontentloaded`/`dialog`/`download`, or `dialog`, `download`, `assertion`, `navigation`), while ignoring missed non-page-response syncs (`dom_stable`, `network_idle`, `duration`).
    - Populates `outcome_kind`, `outcome_index`, `outcome_reason`, `declared`, and `deadline_ms` on `BrowserInteractionPattern` and derives `Pattern.ID` from `(kind, interaction_id, outcome.kind, outcome_index, outcome.declared)` so multiple missed outcomes on the same interaction receive distinct deterministic IDs.
  - Groups surviving attempts of the same logical invocation (`sameInvocation` with strictly increasing `Execution.Attempt`) into `retry` across non-consecutive attempt numbers (when intermediate attempts were dropped) and across interleaved unrelated gestures on the same document, while keeping all retry attempts (including lone surviving `Attempt > 1` records) out of `repeatedGestures`.
  - Anchors `repeatedGestures` windows (`2 s`, `3` gestures, `32 px`) strictly on renderer `Dispatch.At` when `BrowserInteractionHasDispatch(g.Version)` is true and ensures `"scripted"` or `"unknown"` provenance never receives the human-frustration label `"repeated_gesture"`.
- **Report & Dashboard v4 Support (`pkg/api/report_browser_intelligence.go`, `dashboard/src/components/runReport/BrowserInteractionsPanel.tsx`, `dashboard/e2e/workflow-browser-intelligence.spec.ts`):**
  - Extends `InteractionMapView` in `pkg/api/report_browser_intelligence.go` and `BrowserInteractionsPanel.tsx` to recognize `browser_interaction_patterns.v4`, surface `mapped_via_transform` child-frame transform counts, and include `outcome <kind>[<index>]` details in HTML, PDF, and CSV report exports.

## Verification

- **Dashboard Typecheck, Vitest & Build:**
  - `cd dashboard && npm run typecheck`
  - `cd dashboard && npx vitest run src/components/runReport/BrowserInteractionsPanel.test.tsx src/labelCasing.test.ts`
  - `cd dashboard && npm run build`
- **Go Race & Shuffle Tests:**
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserInteraction|TestRunArchiveBrowserInteractions|TestCapturedTapsRepeat|TestEngineRetries|TestDeadInteractions|TestRepetition|TestHeatmaps|TestBrowserGenerationCohorts|TestInteractionHeatmap|TestInteractionAnalysis|TestBrowserIntelligenceReportSections|TestArchiveAuthoredPage'`
- **Mutation Verification (`go test -overlay`):**
  - `m1_multi_outcome_first_only`: Evaluating only `g.DeclaredOutcomes()[:1]` in `findInteractionPatterns` for v4 interactions is killed by `TestBrowserInteractionPatternsV4CombinedQualificationAndReportFormats` (`exit= 1`).
  - `m2_retry_consecutive`: Requiring consecutive `Attempt == prev + 1` instead of `Attempt > prev` on v4 retry grouping is killed by `TestBrowserInteractionPatternsV4CombinedQualificationAndReportFormats` (`exit= 1`).
