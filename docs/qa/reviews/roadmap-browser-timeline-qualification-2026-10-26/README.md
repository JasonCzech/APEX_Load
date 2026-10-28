# Browser timeline and onset qualification (BI-A2)

This slice enables in-run `ScopeTime` onset detection and timeline alignment for qualified browser document series (`TimelineQualification = "qualified"`, `apexload.rca.engine.v8`) in [BI-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#bi-a--qualified-baseline-selection-and-findings).

## Contract and invariants

1. **Timeline qualification (`pkg/api/rca_browser_documents.go`, `pkg/rca/browser_qualification.go`, `pkg/rca/detect.go`):**
   - `rcaBrowserDocumentSeries` sets `s.Document.TimelineQualification = rca.BrowserEvidenceQualified` and `s.ClockUncertainty = t.clockUncertainty` only when:
     - `t.mapped && clockProofComplete` (`s.Document.ClockQualification == rca.BrowserEvidenceQualified`),
     - `s.Document.SourceCoverageQualification == rca.BrowserEvidenceQualified` and `!s.Document.Incomplete`,
     - `t.clockUncertainty >= 0 && t.clockUncertainty < bucket` (sub-bucket clock uncertainty), and
     - at least one populated bucket window in `s.Document.Windows` lies inside a verified steady-load span (`w.MonotonicVerified && !w.Invalid`).
   - Otherwise, `s.Document.TimelineQualification` remains `rca.BrowserEvidenceUnavailable`, and `rca.Detect` excludes the series with `rca.BrowserTimelineUnavailable` (`"browser_timeline_unavailable"`). `rca.Detect` also refuses `SeriesBrowserDocument` with `BrowserTimelineUnavailable` when `s.Document.Incomplete`, `s.Observation != ObservationInterval`, `s.Bucket <= 0`, `docUncertainty < 0 || docUncertainty >= s.Bucket`, or `!browserBucketGridValid(sortedPoints(s.Points), s.Bucket)`, and only counts non-browser series toward `LimitationBaselineUnmatched`.
   - Distributed observations whose `clockUncertainty >= bucket` mark their bucket window `Invalid: true` with `InvalidReason: "observation_window_exceeds_bucket"`.

2. **Invalid bucket filtering, span boundary enforcement, and uncertainty propagation (`pkg/rca/detect.go`):**
   - `usableForSeries(s Series, p SeriesPoint)` requires `s.Document != nil` and a present, non-invalid `DocumentWindow` at `p.At.UTC()` (`!ok || w.Invalid` returns `false`) for `SeriesBrowserDocument` before any point enters baseline construction, CUSUM scoring, or spike detection in `inRun`.
   - In `inRun`, `SeriesBrowserDocument` treats each `LoadLevel` independently (never pooling equal-load spans across pauses) and verifies `browserSourceWindowsWithin(s, usableSpan, levels[index])` before judging any level span (refusing spans that bridge distinct `MonotonicSpanID`s across a pause, mix raw and monotonic proof modes, or cross steady-load boundaries).
   - `ScopeLoad` cross-run comparison for `SeriesBrowserDocument` remains exclusively in `browser_baseline.v1` (`DetectBrowserBaseline`).
   - `detector.open` propagates `max(s.ClockUncertainty, s.Document.ClockUncertainty)` into `dir.uncertainty` (`Anomaly.OnsetUncertainty = spread + s.Bucket + clockUncertainty`).

3. **Engine version identity (`pkg/rca/assessment.go`):**
   - `EngineVersionBase` is bumped from `"apexload.rca.engine.v7"` to `"apexload.rca.engine.v8"`, preserving historical v7 assessments while preventing reuse under earlier qualification rules.

## Validation and mutation testing

- `go test -race -count=2 -shuffle=on ./pkg/rca/...`: passed (`TestBrowserTimelineOnsetQualificationAndInvalidBucketFiltering`, `TestBrowserQualificationAlsoGuardsInRunDetection`).
- `go test -race -count=2 -shuffle=on -run "TestRCADistributedTwoGeneratorBrowserClockQualificationAndRTTRefusal|TestBrowserSourceSameBucketCannotBridgePause|TestRCABrowserQualificationVersionPreservesHistoricalAssessment" ./pkg/api`: passed.
- Mutation verification via `go test -overlay`:
  - Mutant 1 (`if !ok || w.Invalid` -> `if !ok && w.Invalid` in `usableForSeries`, `pkg/rca/detect.go`): killed by `TestBrowserTimelineOnsetQualificationAndInvalidBucketFiltering` (`invalid bucket 10 leaked into spikes`).
  - Mutant 2 (`dir.uncertainty = spread + s.Bucket + clockUncertainty` -> `dir.uncertainty = spread + s.Bucket` in `detector.open`, `pkg/rca/detect.go`): killed by `TestBrowserTimelineOnsetQualificationAndInvalidBucketFiltering` (`OnsetUncertainty = 1s, want >= 1.015s`).

## Review and remaining

- Reviewed by code-safety and test/QA subagents to zero findings.
- Remaining in Track `BI-A`: evaluated baseline status in findings (`BI-A3`, `browser_findings.v2`) and the 95 s two-run seeded-regression production exit (`BI-A4`).
