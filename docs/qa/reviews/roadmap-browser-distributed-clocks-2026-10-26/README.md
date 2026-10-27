# Browser distributed RPC causal brackets and multi-generator steady-load reconciliation (BI-A1)

This slice implements periodic controller↔generator RPC causal bracket acquisition, bounded crystal-drift projection into the controller monotonic clock domain, and multi-generator steady-load span intersection for [BI-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#bi-a--qualified-baseline-selection-and-findings).

## Scope and design

1. **Controller monotonic clock domain and envelope (`pkg/protocol/browser_clock_mapping.go`):**
   - `NewBrowserClockDomain(runID string)` creates the controller's run-scoped monotonic clock authority domain.
   - `BrowserClockBracket` records `[ControllerSendNS, ControllerRecvNS]` in the controller's monotonic domain alongside `OwnerMonoNS` in the generator's `BrowserSourceJournal` monotonic domain (`time.Since(j.start)`), `Authority`, and `OwnerID`.
   - `BrowserDistributedClockEnvelope` retains bounded per-owner brackets (`MaxBrowserClockBracketsPerOwner = 1024`), `MaxBracketRTTNS` (`DefaultBrowserClockMaxBracketRTTNS = int64(time.Second)`, 1 second), and `MaxBracketGapNS` (`DefaultBrowserClockMaxBracketGapNS = int64(45 * time.Second)`, 45 seconds).
   - `browserClockSkewBoundsNS` applies the conservative 200 ppm relative crystal-skew bound (`q*maxSkewPPM + (rem*maxSkewPPM+999_999)/1_000_000` with `addInt64Checked` overflow guards) when projecting any generator monotonic reading `ownerReadingNS` through every bracket `[ControllerSendNS, ControllerRecvNS]`:
     - Lower bound at bracket $b$: `b.ControllerSendNS + (ownerReadingNS - b.OwnerMonoNS) - skew(|ownerReadingNS - b.OwnerMonoNS|)`
     - Upper bound at bracket $b$: `b.ControllerRecvNS + (ownerReadingNS - b.OwnerMonoNS) + skew(|ownerReadingNS - b.OwnerMonoNS|)`
   - Taking the tightest intersection (`max` of lowers, `min` of uppers) across all brackets for that owner produces a conservative `[lowNS, highNS]` interval in the controller's monotonic domain without ever trusting generator wall-clock timestamps (`time.Now()` UTC) or dividing `RTT / 2`.

2. **Generator journal bracket sampling and RPC metadata transport (`pkg/protocol/browser_source_ledger.go`, `pkg/distributed/generatorrpc/browser_clock_brackets.go`):**
   - `BrowserSourceJournal.SampleClockBracket()` returns `BrowserClockBracketSample{Authority, OwnerID, MonoNS}` (`time.Since(j.start)` clamped non-negative and monotonic via `lastAtNS`).
   - Generator RPC handlers attach `x-apexload-browser-clock-bracket` response/header metadata on:
     - `CommitWork` (sampled before `exec.commit` starts VUsers so the initial bracket precedes `slot_expected`, `context_expected`, and `"steady"` when `RampUp == 0`)
     - `Heartbeat` (sampled for all active tests on the generator during periodic heartbeats)
     - `StopTest` (sampled after `exec.gracefulStop()` / `exec.stop()` completes so the closing bracket follows all generator activity)
     - `GetArchiveArtifact` and `StreamArchiveArtifact` (sampled during archive collection after test execution completes)
   - `generatorrpc.Client` brackets each RPC with `sendAt := time.Now()` and `recvAt := time.Now()`, pairs the observed generator sample with `[sendAt, recvAt]`, and exposes `Client.BrowserClockBrackets(testID)`.

3. **Zero-ramp steady phase ordering (`pkg/engine/group.go`):**
   - When `rampDelay == 0`, `MultiGroupEngine` now activates the prepared closed-group VUser batch via `activatePreparedVUserBatchWithAdmission`, marking all group VUsers running and calling `tryFinishClosedInitialRampLocked()` under `mge.mu.Lock()` before spawning the VUser goroutines (`spawnPreparedVUser`). This guarantees the `"steady"` phase event is written to `BrowserSourceJournal` before any zero-ramp VUser executes its first action or opens a browser context.

4. **Multi-generator steady-load reconciliation and RCA qualification (`pkg/protocol/browser_source_reconcile.go`, `pkg/api/rca_browser_source_proof.go`, `pkg/api/rca_browser_documents.go`, `pkg/rca/browser_qualification.go`):**
   - `BrowserSourceReconciler.ValidateDistributedClocks()` verifies that every expected remote owner has at least two brackets covering `[firstActivityNS, lastActivityNS]` (`browser_clock_brackets_unbounded`), no bracket exceeds `MaxBracketRTTNS` (`browser_clock_bracket_rtt_exceeded`), no adjacent bracket gap exceeds `MaxBracketGapNS` (`browser_clock_bracket_gap_exceeded`), brackets are pairwise drift-cone consistent (`browser_clock_brackets_inconsistent`), and all owners project into a single controller `BrowserClockDomain`.
   - `distributedSteadySpans()` projects each owner's `[startNS, endNS]` steady span conservatively into the controller domain (`[startHighNS, endLowNS]`, inner certainty bound where the generator is guaranteed to be in steady state) and intersects overlapping steady spans across all expected owners, summing `VUsers` across the participating generators.
   - `BrowserSourceReconciler.MapDistributed(o BrowserDocumentObservation)` projects each document's `[BeforeNS, AfterNS]` envelope into the controller's `BrowserClockDomain` so `protocol.ContainedInBrowserSteadySpan` and `rca.DetectBrowserBaseline` verify that every contributing bucket's `[WindowStart, WindowEnd]` lies within a single fleet-wide intersected steady-load span and `clockUncertainty <= bucket` (`observation_window_exceeds_bucket`).
   - Archive manifests persist `BrowserProofAuthorityMode = "distributed_envelope"` and `BrowserProofClockEnvelope`; `rcaArchiveAdapterVersion` is bumped to `"archive.11"`.

## Verification and mutation testing

- `go test ./pkg/protocol -run 'TestBrowserSource' -count=1`: passed (`TestBrowserSourceDistributedClockBracketsAndMultiGeneratorReconciliation` covering 2-generator bracket reconciliation, 10-minute generator UTC wall-clock skew immunity, >1s RTT rejection `browser_clock_bracket_rtt_exceeded`, gap rejection `browser_clock_bracket_gap_exceeded`, unbounded activity rejection `browser_clock_brackets_unbounded`, inconsistent drift-cone rejection `browser_clock_brackets_inconsistent`, and conservative steady-span intersection).
- `go test ./pkg/engine -run 'TestBrowserSource' -count=1`: passed (`TestBrowserSourceSteadyPhasePrecedesZeroRampVUserExecution` verifying `"steady"` precedes VUser execution when `RampUp == 0`).
- `go test ./pkg/distributed/generatorrpc -run 'TestBrowserDocument|TestArchive' -count=1`: passed.
- `go test ./pkg/rca/... -count=1`: passed.
- `go test ./pkg/api -run 'TestRCABrowser|TestRCADistributedTwoGenerator|TestDistributedBrowserClockBrackets|TestBrowserSource' -count=1`: passed (`TestRCADistributedTwoGeneratorBrowserClockQualificationAndRTTRefusal` and `TestDistributedBrowserClockBracketsRPCTransport`).
- Mutation verification via `go test -overlay`:
  - Mutant 1 (bypass `b.ControllerRecvNS-b.ControllerSendNS > maxRTT` check in `pkg/protocol/browser_source_reconcile.go`): killed by `TestBrowserSourceDistributedClockBracketsAndMultiGeneratorReconciliation` and `TestRCADistributedTwoGeneratorBrowserClockQualificationAndRTTRefusal`.
  - Mutant 2 (use union `min(a.StartNS, b.StartNS)/max(a.EndNS, b.EndNS)` instead of conservative intersection `max(a.StartNS, b.StartNS)/min(a.EndNS, b.EndNS)` in `distributedSteadySpans`): killed by `TestBrowserSourceDistributedClockBracketsAndMultiGeneratorReconciliation`.
