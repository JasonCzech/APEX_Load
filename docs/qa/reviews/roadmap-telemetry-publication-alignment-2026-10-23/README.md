# Telemetry publication alignment — 2026-10-23

This [TEL-B slice](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-b--policy-evolution-and-complete-consumers) resolves the generator stage-budget mismatch the specification requires before TEL-C, from measured collection and publication scheduling. The decision is **scheduling alignment**, not a budget revision: the receipt, its cadence and the 500 ms default stage budgets are unchanged, and no archived run is reinterpreted.

## What was measured

The 20-second reference workload (`TestTelemetryReferenceWorkload`, `APEXLOAD_TELEMETRY_REFERENCE_SECONDS=20`: two generators over in-memory gRPC, 2,000 accepted observations a second, a headless browser on the live page, darwin-arm64 with 10 CPUs) was run on `main` (`33125479c`), after each change, and on the final code after review (the last column).

| Stage, ms (P50 / P95 / P99) | Before | Generators aligned | Gate aligned too (final code) |
| --- | --- | --- | --- |
| Generator processing | 502 / 503 / 503 | 14 / 20 / 20 | 12 / 19 / 21 |
| Transport | 6 / 8 / 9 | 4 / 6 / 8 | 4 / 7 / 11 |
| Controller fan-out | 4 / 974 / 1,001 | 5 / 1,003 / 1,004 | 2 / 588 / 1,001 |
| Sample age (collection to controller publication) | 513 / 1,512 / 1,512 | 1,019 / 1,023 / 1,024 | 19 / 237 / 494 |
| Browser render | 124 / 174 / 174 | 97 / 158 / 207 | 93 / 181 / 188 |

| Coverage under the default budgets | Before | Generators aligned | Gate aligned too (final code) |
| --- | --- | --- | --- |
| Healthy / degraded / breached / unknown | 0 / 13.0 s / 6.0 s / 2.4 s | 8.5 s / 8.0 s / 0 / 4.9 s | 19.5 s / 0 / 0 / 2.0 s |

The independent end-to-end criterion (capture to visible render at most 2,000 ms) is reported separately from stage health. The harness's bound on it, the largest sample age plus browser render, was 1,687 ms, 1,230 ms and 682 ms: within the target in all three runs. That sum leaves out delivery from the controller's publication to the browser's receipt, so it does not bound capture to visible render.

Every run accounted for all served requests but at most two in flight at the stop (the harness bound is one per VUser, 20 here).

### Cadence and phase

- **Generator collection.** The engine's metrics collector ticks every second, from when `MultiGroupEngine.RunAt` starts it (after its runtime preflight). That is before the coordinated start barrier: it runs during the commit lead.
- **Generator publication.** The publisher ran its own one-second ticker from the start barrier. It published the latest snapshot, so the published age (`GeneratorProcessingLagMs = now - snapshot.Timestamp`) was the two tickers' phase offset, the commit lead modulo one second: 502 ms here, P50 to P99 within 0.3 ms (steady-state samples within 1.5 ms). In the batch trace, publication minus collection had a median of 502 ms.
- **Actual work.** After alignment, collection to publication is 7–26 ms (median 12–14 ms; 8–21 ms in the final run): draining the run's results, computing the snapshot, resources and the publication's encoding.
- **Local controller.** The controller's broadcaster had the same two-ticker shape: its cadence ticker against the engine's collector.
- **Controller gate.** The distributed gate published at once only when a full second had passed since its last publication, and otherwise scheduled a flush a second after it. Of two windows arriving within a second of a publication, the first was published and the second held for about a second. Before alignment no steady-state publication was held; windows arrived together twice, merged into the next publication (the two 1,512 ms sample ages). Aligned, they arrive together, a few milliseconds apart, so the second of each pair was held: 9 steady-state publications waited about 1,000 ms, and since a publication's sample age follows its older window, the sample-age median rose from 513 ms to 1,019 ms while its tail fell.
- **After the gate change** steady-state fan-out is 1–3 ms. The tail is two publications present in every run: the pre-start lifecycle window (about 1,000 ms), and the stop publication (450 ms here). The two stop windows arrived about 550 ms after the previous publication; the first, a partial set, scheduled the one-second flush, and the second completed the set before the 750 ms spacing, so the set waited for that flush. An earlier run of the gate change, before the review fixed its partial-set spacing, measured sample age 18 / 131 / 248 ms.

## Contract

- **`MetricsCollector.CollectionTicked`** (`pkg/engine`) returns a channel closed after the next collection tick, once that tick's snapshot (if it stored one) is the latest; every waiter wakes. `MultiGroupEngine.CollectionTicked` exposes it.
- **`engine.PublishOnCollection`** publishes once per cadence, each time right after a collection tick. A tick sooner than the cadence less a quarter (at most 250 ms) after the previous publication started is skipped (measured from its start, so a slow publication does not push the next one to the watchdog), so a longer local cadence publishes on every n-th collection. Without ticks, a watchdog publishes a cadence and a half after the previous publication ended.
- **Generator** (`runExecutionPublisher`): follows its group's engine. Groups started by one commit enter `RunAt` together, so their collectors share a phase within milliseconds; a source plan shares one engine.
- **Local controller** (`broadcastMetrics`): follows the run's engine at the receipt's cadence, still publishing only new snapshots.
- **Distributed gate** (`shouldPublishDistributedMetricsBatch`): publishes when every connected generator has stored a window since the gate's last publication (tracked by accepted revision: `remoteMetricsStoredRevisionByTarget`, `remoteMetricsGateRevision`) and at least 750 ms have passed. Otherwise it schedules one flush: for a complete set, at the 750 ms spacing; for a partial set, 150 ms later to collect the missing windows, but never sooner than a second after the last publication. Generators out of phase (older generators, or a replacement started on a later barrier) therefore still publish once a second. A window that completes the set once it is due publishes at once, and the scheduled flush then finds itself superseded, as before; a set completed before the 750 ms spacing while a partial-set flush is pending waits for that flush (at most 250 ms longer).
  - A generator whose stored window is terminal (its workload ended) does not hold back a set.
  - A flush that ends without publishing (its consumer ended, or its fence went stale after a topology change) releases its reservation.
  - A partial set is published once 1.3 s have passed since the last publication, whatever flush is pending, so a lost flush can never stall publication.
  - The gate's revision state is reset wherever the accepted revision is (a new remote lifecycle, a failed start, an adoption).
- **Unchanged:** the receipt (`ExpectedControllerPublicationIntervalMs`, `CadenceScope`), budgets, classification, coverage and archive formats. Runs archived before keep their measured stages.

## Not covered

- The reference workload ran 20 seconds on one host, not the declared 60-second, 4 vCPU / 16 GiB release lane; that is TEL-C's.
- The decision rests on one 20-second run per change on one host. Groups of one assignment share a collector phase only as closely as their runtime preflights take equal time, and groups started by different commits (a later scale-out into a new group) keep their own phase; the elected group's collector sets the publication.
- Older generators keep their own ticker until upgraded, and their windows are not aligned; the gate keeps them at one publication a second but was not measured with them.
- **Comparisons across this change.** The receipt and its hash are unchanged, so a baseline comparison or a trend treats runs before and after this change as classified alike. Their generator-processing and sample-age stages differ by the removed phase offset (about 500 ms here): a real change of the pipeline that measured them, not of the application. The receipt's cadence scope still reads `local-controller-ticker` for local runs, a name for the cadence, which the controller keeps.

## Validation

- **`TestPublishOnCollectionFollowsTicksAtTheCadence`**: a tick publishes at once, a tick too soon after the previous publication is skipped, a due one is published.
- **`TestPublishOnCollectionWatchdogWithoutTicks`** and **`TestPublishOnCollectionStops`**.
- **`TestPublishOnCollectionPublishesFreshSnapshots`**: with a real collector and the publisher started half an interval (200 ms) after it, every published snapshot is at most a quarter interval old.
- **`TestCollectionTickedWakesEveryWaiterAfterTheSnapshot`**: one tick wakes every waiter after the snapshot is stored, and a closed channel is not handed out again.
- **`TestDistributedPublicationGateKeepsTheCadenceForOutOfPhaseGenerators`**: two generators half a second apart publish once a second over two cycles.
- **`TestDistributedPublicationGateNeverStalls`**: a flush whose consumer ended releases its reservation; a partial set behind a lost flush is held only until overdue; a generator whose workload ended does not hold back a set.
- **`TestDistributedMetricsTopologyRebasesOwnedSurvivingStream`** (updated): a stale pre-topology flush never consumes a newer reservation, and one that still holds its reservation releases it.
- **`TestDistributedPublicationGatePublishesOnceEveryGeneratorHasANewWindow`**: with two generators, the first window publishes at once; one generator's window waits the coalescing window and its sibling publishes at once, superseding the flush; a complete set too soon after a publication waits for the spacing, with one flush pending; once due it publishes.
- The three reference workload runs above, and the existing engine, generator and distributed-metrics suites.

## Review

Round 1 used two reviewers, one on the scheduling code and one on the record's measurements and claims.

- **Scheduling code.** `PublishOnCollection`, the collector's tick channel, the generator's final publication order and the local broadcaster were sound. The gate had four findings:
  - **High:** a flush that returned early (stale fence after an evacuation, or an ended consumer) kept its reservation; under the completeness rule the gate could then stop publishing for the rest of the run. Every early return now releases it, and an overdue partial set publishes regardless.
  - **Medium:** a generator whose workload ended never stored another window, so every later publication waited out the coalescing flush. Terminal targets no longer count.
  - **Medium:** generators out of phase made the gate publish every 750 ms, faster than the receipt's one second, which would also have shortened sample-counted stops and fed the goal controller partial-fleet samples more often. A partial set now waits the full second.
  - **Low:** the gate's revision state was not reset with the accepted revision.
- **Record.** Every number matched the harness output. Findings: the separate end-to-end criterion was not reported; the gate explanation contradicted the publication trace (only the second of two windows arriving together was held); the fan-out tail and unknown time were not explained or shown; comparisons across the change, the cadence scope's name and the out-of-phase cadence were unstated limits; and the collector starts after its runtime preflight, not on entry. All are corrected above.

Round 2 used two fresh reviewers with the same split.

- **Gate fixes.** The fixes held: a partial set publishes no sooner than a second, an overdue publication cannot repeat, every early return releases only its own reservation, terminal and never-stored targets cannot stall the gate, and the resets cover every site that clears the gate's state. One finding: an existing test asserted that a stale-fence flush keeps its reservation, the behaviour that could stall the gate. A stale flush whose token still matches is always the reservation's holder, so releasing is correct; the test now checks that a stale flush leaves a newer reservation alone and releases its own.
- **Record.** All numbers matched. Wording: the held publications before alignment were the pre-start and stop windows (present in every run), not steady-state ones; the stop publication's 450 ms and the pending-flush case it shows were unexplained; and the phase spread was the P50–P99 range. All corrected.

Round 3 verified the rewritten test and the record's changed sentences against the final run: the record held; the test's release check could also have passed had the flush published instead, so it now also checks that the flush released without publishing. Round 4 verified that assertion: no findings.

The whole-branch review of the in-run policy segments slice (see its [record](../roadmap-telemetry-policy-segments-2026-10-23/README.md)) found one medium defect here: the skip was measured from the previous publication's end, so a publication slower than a quarter of the cadence (250 ms at one second) made the next due tick look too soon; it was skipped and the watchdog published half a cadence plus the publication's own duration late, at an unaligned phase. The skip now measures from the publication's start, with a test whose publication outlasts a quarter of the cadence (`TestPublishOnCollectionKeepsTheCadenceWhenPublishingIsSlow`).

