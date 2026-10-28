# QA Review Record — `BI-C1`: Provisional Live Browser Findings, Interactions, and Journeys

**Date:** 2026-10-27  
**Roadmap Item:** `BI-C1` (`Provisional live findings, interactions & journeys`)  
**Option Selected:** Option A — Distributed + HA Live Provisional Browser Intelligence  
**Status:** Verified (Zero Open Findings)

## Scope & Contract Changes

1. **Incremental Engine & Protocol Flush ([browser_documents.go](../../../../pkg/engine/browser_documents.go), [browser_har.go](../../../../pkg/engine/browser_har.go), [vuser.go](../../../../pkg/engine/vuser.go), [vuser_run.go](../../../../pkg/engine/vuser_run.go), [messaging_progress.go](../../../../pkg/engine/messaging_progress.go), [touch_actions.go](../../../../pkg/protocol/browser/touch_actions.go)):**
   - Added `LiveBrowserDocumentEvidenceSink` in [browser_documents.go](../../../../pkg/engine/browser_documents.go) and `LiveBrowserInteractionEvidenceSink` in [browser_har.go](../../../../pkg/engine/browser_har.go), wiring `flushBrowserIterationEvidence()` in [vuser.go](../../../../pkg/engine/vuser.go) (called from `prepareIteration` and `completeIteration` in [vuser_run.go](../../../../pkg/engine/vuser_run.go)) and `flushBrowserInteractions()` in [messaging_progress.go](../../../../pkg/engine/messaging_progress.go) (called from `state.sample(final)` in [browser_documents.go](../../../../pkg/engine/browser_documents.go)) to fan out raw `protocol.BrowserDocumentEvidence` and `protocol.BrowserInteractionEvidence` at iteration boundaries and every `<= 30 s` alongside the run archive writer while deduplicating when the primary sink implements both interfaces.
   - Bounded WebKit mobile native `locator.Tap()` hit-target checks in [touch_actions.go](../../../../pkg/protocol/browser/touch_actions.go) (`webkitElementUnoccludedAtCenter`) so pages without `<meta name="viewport">` fall back cleanly when unoccluded at center while preserving genuine occlusion failures and trusted touch dispatch on viewport-configured pages.

2. **Distributed Generator RPC Transport ([generator.proto](../../../../api/proto/generator.proto), [service.go](../../../../pkg/distributed/generatorrpc/service.go), [client.go](../../../../pkg/distributed/generatorrpc/client.go)):**
   - Extended `MetricsBatch` in [generator.proto](../../../../api/proto/generator.proto) with incremental `live_browser_documents_json`, `live_browser_interactions_json`, `live_journey_events` (`LiveJourneyEvent` carrying per-VUser iteration identity and `BrowserAuthoredStep` structural step paths), and `live_browser_truncation` (`LiveBrowserTruncation`).
   - Updated `combineSinks` (`combinedSink`) and `transactionBuffer` in [service.go](../../../../pkg/distributed/generatorrpc/service.go) with strict per-window bounds (`maxWindowLiveBrowserDocumentRecords = 256` / `maxWindowLiveBrowserDocumentBytes = 2 << 20`, `maxWindowLiveBrowserInteractionRecords = 256` / `maxWindowLiveBrowserInteractionBytes = 2 << 20`, `maxWindowLiveJourneyEvents = 4096`), malformed-evidence drop accounting (`Clone() == nil`), and truncation counters drained into each `MetricsBatch`.

3. **Controller Live Evaluator, HA Cache Fanout & Stop-and-Join Barrier ([live_browser_intelligence.go](../../../../pkg/api/live_browser_intelligence.go), [cache_backend.go](../../../../pkg/api/cache_backend.go), [execution_backend.go](../../../../pkg/api/execution_backend.go), [browser_findings.go](../../../../pkg/api/browser_findings.go), [browser_findings_delivery.go](../../../../pkg/api/browser_findings_delivery.go), [browser_interaction_patterns.go](../../../../pkg/api/browser_interaction_patterns.go), [journey_accounting.go](../../../../pkg/api/journey_accounting.go)):**
   - Added `liveBrowserIntelligenceState` and `ProvisionalBrowserIntelligenceSnapshot` on `ManagedTest` and `RunLiveStateSnapshot` (`provisional_browser_intelligence`), wired `combinedResultSink` in [execution_backend.go](../../../../pkg/api/execution_backend.go), and evaluated provisional findings, interaction patterns, and journeys using the exact archive evaluators (`evaluateBrowserFindings`, `newInteractionAnalyzer`, `newJourneyAccumulator`) on a `30 s` ticker (`defaultLiveBrowserIntelligenceInterval`), persisting provisional snapshots to `LiveStateStore` (Redis/Valkey) so HA followers serve identical provisional responses.
   - Enforced controller-side memory bounds (`maxLiveBrowserInteractionRecords = 2048`, `maxLiveBrowserInteractionTotalCount = 100_000`, `maxLiveBrowserJourneyEvents = 100_000`) with `live_truncated: true` and `truncated_reasons` when exceeded (including malformed non-nil batches rejected by `Clone()`), and deduplicated repeated final generator collections by `(target, SourceEpoch, CollectionSequence)`.
   - Marked provisional finding baselines with `status: "not_evaluated_live"` and `reason: "not_evaluated_live"`.
   - Enforced a strict stop-and-join barrier (`stopAndJoinLiveBrowserIntelligence` / `stopAndJoinLiveBrowserIntelligenceByID`) before archive finalization and at the start of `runArchiveAttached`, and released provisional state (`releaseLiveBrowserIntelligenceLocked` clearing both `liveBrowserIntelligence` and `provisionalBrowserIntelligence`) once `ManifestSHA256` is attached so finalized archive verdicts atomically replace provisional live views.

4. **Dashboard Provisional Views ([CommandCenterBrowserGroups.tsx](../../../../dashboard/src/components/CommandCenterBrowserGroups.tsx), [BrowserFindingsPanel.tsx](../../../../dashboard/src/components/runReport/BrowserFindingsPanel.tsx), [BrowserInteractionsPanel.tsx](../../../../dashboard/src/components/runReport/BrowserInteractionsPanel.tsx), [JourneyRiskPanel.tsx](../../../../dashboard/src/components/runReport/JourneyRiskPanel.tsx)):**
   - Updated `BrowserFindingsPanel`, `BrowserInteractionsPanel`, `JourneyRiskPanel`, and `CommandCenterBrowserGroups` to render `provisional: true` and `live_truncated: true` badges/notices and `not_evaluated_live` baseline labels, polling while `provisional: true` and atomically replacing provisional views once the archive is finalized.

## Verification & Mutation Testing

- **Go Unit, Integration & Race Tests:**
  - `go test -race -count=1 ./pkg/engine -run "TestBrowser|TestLive|TestCombinedSink|TestTransactionBuffer"`
  - `go test -count=1 ./pkg/protocol/browser -run "TestBrowserProtocolExecuteWebKitActions|TestBrowserProtocolValidationMatrixAcrossRepresentativeDeviceProfiles|TestBrowserTap"`
  - `go test -race -count=1 ./pkg/distributed/generatorrpc -run "TestBrowser|TestLive|TestCombinedSink|TestTransactionBuffer"`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run "TestLiveBrowserIntelligence"`
  - `go test -count=1 ./pkg/api -run "TestBrowserIntelligence|TestRunArchiveBrowserInteractions|TestJourney|TestSwagger"`
- **Frontend Build & Vitest:**
  - `cd dashboard && npm run build`
  - `cd dashboard && npx vitest run src/components/runReport/BrowserFindingsPanel.test.tsx src/components/runReport/BrowserInteractionsPanel.test.tsx src/components/runReport/JourneyRiskPanel.test.tsx src/components/CommandCenterBrowserGroups.test.tsx src/components/CommandCenterPrototype.browserGroups.test.tsx src/labelCasing.test.ts`
- **Mutation Verification (`go test -overlay`):**
  - Mutant 1 (`pkg/api/browser_findings.go` omitting `inputs.provisional` guard returning `not_evaluated_live` in `evaluateFindingBaseline`): killed by `TestLiveBrowserIntelligenceLocalHAFollowerAndArchiveParity`.
  - Mutant 2 (`pkg/api/live_browser_intelligence.go` omitting `seenCollections` deduplication in `observeGeneratorBatch`): killed by `TestLiveBrowserIntelligenceDistributedTwoGeneratorsAndTruncation`.
