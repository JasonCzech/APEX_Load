package api

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/rca"
)

// Delayed envelopes must not relocate an already observed window onto the
// receipt clock, whether the transport repeats its sequence or a later
// lifecycle envelope carries the same collection under a new sequence.
func TestTelemetryCoverageDoesNotRetimeReplayedWindows(t *testing.T) {
	for _, kind := range []string{"same_envelope", "same_collection", "older_collection", "legacy_collection_time"} {
		t.Run(kind, func(t *testing.T) {
			start := time.Unix(1_800_000_000, 0).UTC()
			ledger := newTelemetrySourceLedger()
			ledger.assign("gen-a", start)
			batch := sourceBatch("e1", 1000, 1)
			batch.WindowMs = 1000
			batch.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 2, PublishedAtUnixMs: 2000}
			if kind == "legacy_collection_time" {
				batch.Telemetry.CollectionSequence = 0
			}
			ledger.observe("gen-a", batch, start.Add(time.Second))
			if kind != "same_envelope" {
				batch.BatchSequence = 2
			}
			if kind == "older_collection" {
				batch.Telemetry.CollectionSequence = 1
			}
			ledger.observe("gen-a", batch, start.Add(10*time.Second))

			end := start.Add(10 * time.Second)
			run := &ManagedTest{ID: "coverage-replay", Status: "RUNNING", StartedAt: &start, telemetrySources: ledger}
			live := telemetryLiveCoverageLocked(run, end)
			if live == nil || live.ObservedMs != 1000 || live.UnknownMs != 9000 {
				t.Fatalf("replay became live coverage: %+v", live)
			}
			archive := telemetryRunCoverageRecord(ledger, []string{"gen-a"}, start, end, healthyRows(start, 10), nil)
			if archive.HealthyMs != 1000 || archive.UnknownMs != 9000 {
				t.Fatalf("replay became archived healthy time: %+v", archive)
			}
			if epoch := onlyEpoch(t, ledger); epoch.Received != 2 || kind == "same_envelope" && epoch.Duplicates != 1 {
				t.Fatalf("publication evidence was discarded: %+v", epoch)
			}
		})
	}
}

func TestTelemetryCoverageLateSequenceDoesNotMoveCursor(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", start)
	for _, event := range []struct {
		sequence uint64
		second   int
	}{{1, 1}, {3, 3}, {2, 5}, {4, 6}} {
		batch := sourceBatch("e1", 1000, event.sequence)
		batch.WindowMs = 1000
		ledger.observe("gen-a", batch, start.Add(time.Duration(event.second)*time.Second))
	}
	coverage, _ := coverageOf(ledger, []string{"gen-a"}, start, start.Add(6*time.Second))
	if coverage.ObservedMs != 3000 || coverage.UnknownMs != 3000 {
		t.Fatalf("late sequence filled unknown receipt time: %+v", coverage)
	}
	if epoch := onlyEpoch(t, ledger); epoch.Received != 4 || epoch.OutOfOrder != 1 || epoch.LostPublications != 0 {
		t.Fatalf("late publication no longer repairs the sequence loss ledger: %+v", epoch)
	}
}

func TestTelemetryCoverageTerminalReplayRetiresWithoutNewWindow(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", start)
	batch := sourceBatch("e1", 1000, 1)
	batch.WindowMs = 1000
	batch.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 1}
	ledger.observe("gen-a", batch, start.Add(time.Second))
	batch.BatchSequence, batch.LifecycleState = 2, "FINALIZING"
	ledger.observe("gen-a", batch, start.Add(7*time.Second))
	batch.BatchSequence, batch.LifecycleState = 3, "COMPLETE"
	ledger.observe("gen-a", batch, start.Add(9*time.Second))
	// A later terminal envelope neither extends the requirement nor adds
	// coverage, even when its publication sequence is new.
	batch.BatchSequence = 4
	ledger.observe("gen-a", batch, start.Add(10*time.Second))
	coverage, _ := coverageOf(ledger, []string{"gen-a"}, start, start.Add(12*time.Second))
	if len(coverage.Sources) != 1 {
		t.Fatal(coverage)
	}
	source := coverage.Sources[0]
	if source.ObservedMs != 1000 || source.RequiredMs != 9000 || source.UnknownMs != 8000 {
		t.Fatalf("terminal replay changed coverage or retirement: %+v", source)
	}
	if epoch := onlyEpoch(t, ledger); epoch.Received != 4 || epoch.TerminalSequence != 4 || !epoch.LossExact {
		t.Fatalf("terminal publication accounting changed: %+v", epoch)
	}

	// A terminal collection first seen here still has a genuine window.
	first := newTelemetrySourceLedger()
	first.assign("gen-a", start)
	batch.BatchSequence = 1
	first.observe("gen-a", batch, start.Add(time.Second))
	got, _ := coverageOf(first, []string{"gen-a"}, start, start.Add(time.Second))
	if got.ObservedMs != 1000 || got.UnknownMs != 0 {
		t.Fatalf("unseen terminal collection was discarded: %+v", got)
	}
}

func TestTelemetryCoverageOlderEpochCannotRetireCurrentSource(t *testing.T) {
	for _, kind := range []string{"older_epoch", "unknown_epoch_start", "older_generation", "unsequenced_downgrade", "late_terminal"} {
		t.Run(kind, func(t *testing.T) {
			start := time.Unix(1_800_000_000, 0).UTC()
			ledger := newTelemetrySourceLedger()
			ledger.assign("gen-a", start)
			current := sourceBatch("e2", 2000, 2)
			current.ExecutionGeneration = 8
			current.WindowMs = 1000
			ledger.observe("gen-a", current, start.Add(time.Second))
			stale := current
			stale.LifecycleState = "COMPLETE"
			switch kind {
			case "older_epoch":
				stale.SourceEpoch, stale.SourceEpochStartedUnixMs, stale.BatchSequence = "e1", 1000, 99
			case "unknown_epoch_start":
				stale.SourceEpoch, stale.SourceEpochStartedUnixMs = "other", 0
			case "older_generation":
				stale.ExecutionGeneration, stale.SourceEpoch, stale.SourceEpochStartedUnixMs = 7, "old", 9000
			case "unsequenced_downgrade":
				stale.BatchSequence = 0
			case "late_terminal":
				stale.BatchSequence = 1
			}
			ledger.observe("gen-a", stale, start.Add(5*time.Second))
			current.BatchSequence = 3
			ledger.observe("gen-a", current, start.Add(6*time.Second))
			coverage, _ := coverageOf(ledger, []string{"gen-a"}, start, start.Add(6*time.Second))
			if coverage.ObservedMs != 2000 || len(coverage.Sources) != 1 || coverage.Sources[0].RequiredMs != 6000 {
				t.Fatalf("stale evidence changed the active source: %+v", coverage)
			}
			_, requirements, _ := ledger.coverageSnapshot()
			if requirements["gen-a"].retiredAtMs != 0 {
				t.Fatal("stale terminal retired the active epoch")
			}
		})
	}
}

func TestTelemetryCoverageNewEpochRestartsCollectionIdentity(t *testing.T) {
	for _, higherGeneration := range []bool{false, true} {
		t.Run(fmt.Sprint(higherGeneration), func(t *testing.T) {
			start := time.Unix(1_800_000_000, 0).UTC()
			ledger := newTelemetrySourceLedger()
			batch := sourceBatch("e1", 1000, 90)
			batch.WindowMs = 1000
			batch.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 50}
			ledger.observe("gen-a", batch, start.Add(time.Second))
			batch.SourceEpoch, batch.SourceEpochStartedUnixMs, batch.BatchSequence = "e2", 2000, 1
			batch.Telemetry.CollectionSequence = 1
			if higherGeneration {
				batch.ExecutionGeneration++
				// Generation fencing wins over timestamps from another clock.
				batch.SourceEpochStartedUnixMs = 500
			}
			ledger.observe("gen-a", batch, start.Add(3*time.Second))
			batch.BatchSequence, batch.Telemetry.CollectionSequence = 2, 2
			ledger.observe("gen-a", batch, start.Add(4100*time.Millisecond))
			coverage, _ := coverageOf(ledger, []string{"gen-a"}, start, start.Add(5*time.Second))
			if coverage.ObservedMs != 3100 || coverage.UnknownMs != 1900 {
				t.Fatalf("new epoch lost its windows or bridged the restart gap: %+v", coverage)
			}
		})
	}
}

func TestTelemetryCoverageEpochLimitDoesNotCreateUntrackedWindows(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	for i := 0; i < maxTelemetrySourceEpochs; i++ {
		batch := sourceBatch("epoch", 1000, 1)
		batch.WindowMs = 1000
		ledger.observe(fmt.Sprintf("gen-%d", i), batch, start.Add(time.Second))
	}
	ledger.assign("overflow", start)
	batch := sourceBatch("epoch", 1000, 1)
	batch.WindowMs, batch.LifecycleState = 1000, "COMPLETE"
	ledger.observe("overflow", batch, start.Add(time.Second))
	observed, requirements, _ := ledger.coverageSnapshot()
	if !ledger.truncated || observed["overflow"] != nil || requirements["overflow"].retiredAtMs != 0 || len(ledger.cursors) > maxTelemetrySourceEpochs {
		t.Fatal("untracked epoch allocated coverage or retired an unobserved source")
	}
	end := start.Add(2 * time.Second)
	run := &ManagedTest{ID: "coverage-bound", Status: "RUNNING", StartedAt: &start, telemetrySources: ledger}
	live := telemetryLiveCoverageLocked(run, end)
	archive := telemetryRunCoverageRecord(ledger, []string{"overflow"}, start, end, healthyRows(start, 2), nil)
	for _, coverage := range []*TelemetryCoverage{live, &archive.Coverage} {
		criterion := telemetryCoverageCriterion(0.9, telemetryAcceptanceEvidence{coverage: coverage})
		if !coverage.Truncated || criterion.Outcome != TelemetryAcceptanceInconclusive || criterion.Bound != telemetryAcceptanceLowerBound {
			t.Fatalf("epoch retention limit became an exact coverage failure: %+v", criterion)
		}
	}
}

func TestTelemetryCoverageAlgorithmKeepsLegacyComparisonSeparate(t *testing.T) {
	makeRun := func(id, algorithm string) *baselineComparisonRun {
		return telemetryComparisonRun(id, telemetryReceipt(t, 1, 250), 4, func(m *RunArchiveManifest) {
			m.TelemetryCoverage = &TelemetryCoverageRecord{Algorithm: algorithm}
		})
	}
	current := makeRun("current", telemetryCoverageAlgorithm)
	legacy := makeRun("legacy", "telemetry_coverage.v2")
	comparison := compareBaselineTelemetry(current, legacy, rca.WorkloadQualification{Status: rca.BaselineQualified})
	if comparison.Comparable || !slices.Contains(comparison.Reasons, telemetryReasonCapabilityDiffers) {
		t.Fatalf("replay-safe and legacy coverage were treated as equivalent: %+v", comparison)
	}
	if legacy.manifest.TelemetryCoverage.Algorithm != "telemetry_coverage.v2" {
		t.Fatal("legacy evidence was rewritten")
	}
	if same := compareBaselineTelemetry(current, makeRun("same", telemetryCoverageAlgorithm), rca.WorkloadQualification{Status: rca.BaselineQualified}); !same.Comparable {
		t.Fatalf("same-algorithm coverage no longer compares: %+v", same)
	}
}

func TestTelemetryCoverageLegacyAndMissingCollectionIdentity(t *testing.T) {
	for _, kind := range []string{"legacy", "missing_after_sequence", "missing_after_time", "incomparable_upgrade"} {
		t.Run(kind, func(t *testing.T) {
			start := time.Unix(1_800_000_000, 0).UTC()
			ledger := newTelemetrySourceLedger()
			ledger.assign("gen-a", start)
			batch := sourceBatch("e1", 1000, 1)
			batch.WindowMs = 1000
			switch kind {
			case "missing_after_sequence":
				batch.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 1}
			case "missing_after_time", "incomparable_upgrade":
				batch.Telemetry = &generatorrpc.MetricsTelemetry{PublishedAtUnixMs: 1000}
			}
			ledger.observe("gen-a", batch, start.Add(time.Second))
			batch.BatchSequence, batch.Telemetry = 2, nil
			if kind == "incomparable_upgrade" {
				batch.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 1, PublishedAtUnixMs: 2000}
			}
			ledger.observe("gen-a", batch, start.Add(3*time.Second))
			got, _ := coverageOf(ledger, []string{"gen-a"}, start, start.Add(3*time.Second))
			want := int64(1000)
			if kind == "legacy" {
				want = 3000 // legacy windows still bridge bounded receipt jitter
			}
			if got.ObservedMs != want {
				t.Fatalf("identity compatibility: observed %d, want %d", got.ObservedMs, want)
			}
		})
	}
}

func TestTelemetryCoverageReplayCannotBridgeTheNextFreshWindow(t *testing.T) {
	start := time.Unix(1_800_000_000, 0).UTC()
	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a", start)
	batch := sourceBatch("e1", 1000, 1)
	batch.WindowMs = 1000
	batch.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 1}
	ledger.observe("gen-a", batch, start.Add(time.Second))
	batch.BatchSequence = 2
	ledger.observe("gen-a", batch, start.Add(2*time.Second))
	batch.BatchSequence, batch.Telemetry.CollectionSequence = 3, 2
	ledger.observe("gen-a", batch, start.Add(3*time.Second))
	got, _ := coverageOf(ledger, []string{"gen-a"}, start, start.Add(3*time.Second))
	if got.ObservedMs != 2000 || got.UnknownMs != 1000 {
		t.Fatalf("replayed collection enabled a false jitter bridge: %+v", got)
	}
}
