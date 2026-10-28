# QA Review — Interaction Analysis Bounds, Keyboard Target Counts & Raw Interaction CSV Export (`BI-B4`)

- **Date:** 2026-10-27
- **Requirement / Track:** `BI-3` (`BI-B` — Interaction maps and authored journey outcomes)
- **Work Package:** `BI-B4`
- **Branch / Worktree:** `be-c1-vuser-attribution` (`/Users/jasonczech/Documents/GitHub/Project_Stormrunner/review-worktrees/be-b3`)

## Scope & Contract Changes

1. **Seven Interaction-Analysis Bounds & Population Separation ([browser_interaction_patterns.go](../../../../pkg/api/browser_interaction_patterns.go)):**
   - Exposed the streaming vs pattern-retained population split on `BrowserInteractionCoverage`:
     - `streamed_interactions`: count of raw interaction records observed across the archive stream (contributing to coverage, cohort attribution, and heatmaps even beyond the 100,000 pattern retention cap).
     - `pattern_retained`: count of interaction records retained in the pattern analyzer (`maxRetainedInteractions = 100,000`).
     - `pattern_truncated`: count of interaction records beyond `maxRetainedInteractions` excluded from pattern detection while still contributing to streaming coverage and heatmaps.
   - Preserved `maxInteractionGenerationCohorts = 65,536` (`document_evidence_truncated`), `maxInteractionDocumentJoins = 100,000` (`document_joins_truncated`), `maxInteractionHeatmaps = 64` (`heatmaps_truncated`), `maxHeatmapTargets = 64` per map, `maxRetainedHeatmapTargets = 1,024` per map (`keyboard_targets_truncated`), `maxInteractionPatterns = 200` (`patterns_truncated`), and `maxInteractionPatternMembers = 16` (`MembersTruncated` on `BrowserInteractionPattern` while preserving true `Count`).
   - Added `patternTruncationPriority` so actionable patterns (`dead_interaction`, `unexpected_navigation`, `retry`, `repeated_gesture`) sort ahead of benign `scripted_repetition` before `[:maxInteractionPatterns]` truncation when more than 200 patterns are present.
   - Qualified historical v1 evidence in `newInteractionAnalyzer` with explicit notes explaining post-action geometry/scroll and capture-time repeat windows.

2. **Keyboard Target Counts & Report Format Parity ([report_browser_intelligence.go](../../../../pkg/api/report_browser_intelligence.go), [report_section_templates.go](../../../../pkg/api/report_section_templates.go), [BrowserInteractionsPanel.tsx](../../../../dashboard/src/components/runReport/BrowserInteractionsPanel.tsx)):**
   - Added `Targets` (`formatReportHeatmapTargets`) and combined pointer+keyboard counts (`heatmap.Points + sumCounts(heatmap.Targets)`) to `InteractionMapView` so keyboard-only interactions are never rendered as `0 gestures` across HTML, PDF, and CSV report formats.
   - Surfaced `pattern_retained` / `pattern_truncated` coverage notices, `members_truncated` badges (`N of M IDs listed`), and combined pointer/keyboard gesture counts (`X pointer gestures · Y keyboard gestures (Z total)`) in `BrowserInteractionsPanel.tsx`.

3. **Raw Interaction Analysis CSV Export ([browser_interaction_patterns.go](../../../../pkg/api/browser_interaction_patterns.go)):**
   - Extended `GET /api/v1/tests/{id}/archive/browser-interactions` with `?format=csv` (`writeBrowserInteractionAnalysisCSV`), returning `text/csv; charset=utf-8` with `coverage`, `pattern`, `heatmap`, `heatmap_note`, `limitation`, and `note` sections, `reportCSVSafeCell` formula-injection sanitization on untrusted strings, and `400 Bad Request` on unsupported `format` values.

## Verification & Mutation Testing

- **Dashboard Typecheck, Unit Tests & Production Build:**
  - `cd dashboard && npm run typecheck && npx vitest run src/components/runReport/BrowserInteractionsPanel.test.tsx src/labelCasing.test.ts && npm run build` (`11 passed (11)`).
- **Go Race & Shuffle Suite:**
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserInteraction|TestRunArchiveBrowserInteractions|TestCapturedTapsRepeat|TestEngineRetries|TestDeadInteractions|TestRepetition|TestHeatmaps|TestBrowserGenerationCohorts|TestInteractionHeatmap|TestInteractionAnalysis|TestBrowserIntelligenceReportSections|TestArchiveAuthoredPage|TestOpenAPISpecIsFresh|TestRouteCatalogCharacterization'`
- **Mutation Verification (`go test -overlay`):**
  - `m1_pattern_priority`: Neutralized `patternTruncationPriority` in `findInteractionPatterns` so `scripted_repetition` sorts ahead of `dead_interaction` by count -> killed by `TestBrowserInteractionAnalysisSevenBoundsAndCSVReportParity`.
  - `m2_kbd_gesture_sum`: Omitted `sumCounts(heatmap.Targets)` from `InteractionMapView.Gestures` in `report_browser_intelligence.go` -> killed by `TestBrowserInteractionAnalysisSevenBoundsAndCSVReportParity`.
