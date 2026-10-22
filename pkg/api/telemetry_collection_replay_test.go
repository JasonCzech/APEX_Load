package api

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func telemetryCollectionBatch(id string, batchSequence, collection uint64, collectedAt time.Time) generatorrpc.MetricsBatch {
	batch := distributedRunTotalsBatch(id, int64(batchSequence), 17, 2)
	batch.TimestampUnixMs = collectedAt.Add(20 * time.Millisecond).UnixMilli()
	batch.Telemetry = &generatorrpc.MetricsTelemetry{
		CollectionSequence: collection, CollectedAtUnixMs: collectedAt.UnixMilli(),
		PublishedAtUnixMs:          collectedAt.Add(5 * time.Millisecond).UnixMilli(),
		ControllerReceivedAtUnixMs: collectedAt.Add(20 * time.Millisecond).UnixMilli(),
		GeneratorProcessingLagMs:   5, TransportLagMs: 15,
		TransportLagQuality: &generatorrpc.ClockQuality{Qualified: true},
		CollectedAtQuality:  &generatorrpc.ClockQuality{Qualified: true},
		DroppedResults:      3, SheddedArrivals: 4, DroppedSubscriberBatches: 5,
	}
	return batch
}

func publishTelemetryCollection(t *testing.T, runner *TestRunner, run *ManagedTest, backend *distributedExecutionBackend, target string) {
	t.Helper()
	live, _, _, accepted, publication := backend.applyStoredRemoteMetricsBatch(runner, run, target)
	if !accepted || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
		t.Fatal("collection publication rejected")
	}
}

func telemetryReplayStage(t *testing.T, run *ManagedTest, name string) TelemetryStageDistribution {
	t.Helper()
	for _, stage := range stageRecorderLocked(run).snapshot() {
		if stage.Stage == name {
			return stage
		}
	}
	t.Fatalf("stage %s missing", name)
	return TelemetryStageDistribution{}
}

// The generator sends its final window before finalizing the archive, then
// sends the same collection again as COMPLETE. Archive time is lifecycle
// delivery, not another measurement of that collection's delivery latency.
func TestTelemetryCollectionReplayDoesNotReageTerminalMetrics(t *testing.T) {
	for _, collection := range []uint64{0, 1} { // Legacy publication-time identity and current sequence.
		for _, storeOnly := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "stored"}[storeOnly]+"/"+map[bool]string{false: "legacy", true: "sequence"}[collection > 0], func(t *testing.T) {
				runner, run, backend := distributedRunTotalsRun(t, "terminal-replay", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
				collectedAt := time.Now().Add(-10 * time.Second)
				first := telemetryCollectionBatch(run.ID, 1, collection, collectedAt)
				first.ActionMetricsVersion = 1
				if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", first) {
					t.Fatal("initial window rejected")
				}
				publishTelemetryCollection(t, runner, run, backend, "gen-a")
				// A new collection's publication is in the render population (TEL-A).
				publication := run.LiveMetrics.Telemetry.Publication.clone()
				if publication == nil || !publication.Eligible {
					t.Fatalf("a new collection's publication is not eligible: %+v", publication)
				}
				ageBefore := telemetryReplayStage(t, run, telemetryStageSampleAge)
				terminal := telemetryCollectionBatch(run.ID, 2, collection, collectedAt)
				terminal.ActionMetricsVersion = 1
				terminal.LifecycleState, terminal.Stats.ActiveVUsers, terminal.Groups[0].ActiveVUsers = "COMPLETE", 0, 0
				terminal.Telemetry.TransportLagMs = 9_000
				terminal.Telemetry.ControllerReceivedAtUnixMs = time.Now().UnixMilli()
				terminal.TimestampUnixMs = time.Now().UnixMilli()
				if storeOnly {
					if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", terminal) {
						t.Fatal("terminal snapshot rejected")
					}
				} else {
					publishTerminal(t, runner, run, backend, "gen-a", terminal)
					wantRunTotals(t, run, 15, 2)
					if evidence := run.LiveMetrics.NativeEvidence.Publication; evidence.Sequence != first.BatchSequence || evidence.Timestamp.UnixMilli() != first.TimestampUnixMs {
						t.Fatalf("direct terminal publication invented an interval identity: %+v", evidence)
					}
					if !run.LiveMetrics.Telemetry.SampleAgeUnavailable || run.LiveMetrics.ActiveVUsers != 0 {
						t.Fatalf("terminal live metrics: %+v", run.LiveMetrics)
					}
					// The replayed collection is published, but not counted in the
					// render population.
					if replayed := run.LiveMetrics.Telemetry.Publication; replayed == nil || replayed.Eligible || replayed.Epoch != publication.Epoch || replayed.Sequence != publication.Sequence+1 {
						t.Fatalf("terminal replay publication %+v after %+v", replayed, publication)
					}
					if got := telemetryReplayStage(t, run, telemetryStageSampleAge); got.Samples != ageBefore.Samples || got.MaxMs == nil || *got.MaxMs != *ageBefore.MaxMs {
						t.Fatalf("terminal replay acquired another sample age: %+v", got)
					}
					if got := telemetryReplayStage(t, run, telemetryStageControllerFanout); got.Samples != 2 {
						t.Fatalf("terminal delivery lost controller fan-out: %+v", got)
					}
				}
				stored := run.remoteMetricsBatchesByTarget["gen-a"]
				if !stored.Telemetry.CollectionRepeated || terminal.Telemetry.CollectionRepeated || first.Telemetry.CollectionRepeated {
					t.Fatal("replay ownership was lost or mutated the caller's telemetry")
				}
				if stored.LifecycleState != "COMPLETE" || stored.FleetMetrics.Count != 17 {
					t.Fatalf("terminal metrics/state changed: %+v", stored)
				}
				for _, name := range []string{telemetryStageGeneratorProcessing, telemetryStageTransport} {
					if got := telemetryReplayStage(t, run, name); got.Samples != 1 || got.MaxMs == nil || *got.MaxMs > 15 {
						t.Fatalf("%s re-counted terminal timing: %+v", name, got)
					}
				}
				// A stale direct apply still returns the current aggregate for
				// inspection; it cannot install another publication.
				current, _, _, _, _ := backend.applyRemoteMetricsBatchWithFreshness(runner, run, "gen-a", terminal, false)
				if !current.Telemetry.SampleAgeUnavailable || current.Telemetry.DroppedResults != 3 || current.Telemetry.SheddedArrivals != 4 || current.Telemetry.DroppedSubscriberBatches != 5 || current.Telemetry.ControllerReceivedAt.UnixMilli() != terminal.Telemetry.ControllerReceivedAtUnixMs {
					t.Fatalf("terminal replay lost counters/receipt or retained collection timing: %+v", current.Telemetry)
				}
			})
		}
	}
}

// A reconnect may deliver a final window the controller never saw. That
// terminal sample is real latency evidence, including a reset source epoch;
// absence of any collection identity is also treated conservatively.
func TestTelemetryCollectionReplayKeepsUnseenTerminalTiming(t *testing.T) {
	for _, mode := range []string{"new-collection", "reconnected-first-window", "new-epoch", "unknown-identity"} {
		t.Run(mode, func(t *testing.T) {
			runner, run, backend := distributedRunTotalsRun(t, "unseen-terminal", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
			at := time.Now().Add(-10 * time.Second)
			first := telemetryCollectionBatch(run.ID, 1, 1, at)
			first.SourceEpochStartedUnixMs = at.Add(-time.Second).UnixMilli()
			if mode != "reconnected-first-window" && !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", first) {
				t.Fatal("initial window rejected")
			}
			terminal := telemetryCollectionBatch(run.ID, 2, 2, at.Add(time.Second))
			terminal.SourceEpochStartedUnixMs = first.SourceEpochStartedUnixMs
			terminal.LifecycleState, terminal.Stats.ActiveVUsers, terminal.Groups[0].ActiveVUsers = "ABORTED", 0, 0
			terminal.TimestampUnixMs = time.Now().UnixMilli()
			terminal.Telemetry.TransportLagMs = 9_000
			terminal.Telemetry.ControllerReceivedAtUnixMs = terminal.TimestampUnixMs
			switch mode {
			case "new-epoch":
				terminal.SourceEpoch, terminal.SourceEpochStartedUnixMs = "restarted", at.UnixMilli()
				terminal.BatchSequence, terminal.Telemetry.CollectionSequence = 1, 1
			case "unknown-identity":
				terminal.Telemetry.CollectionSequence, terminal.Telemetry.PublishedAtUnixMs = 0, 0
			}
			publishTerminal(t, runner, run, backend, "gen-a", terminal)
			if run.remoteMetricsBatchesByTarget["gen-a"].Telemetry.CollectionRepeated {
				t.Fatal("an unseen terminal collection was marked as replayed")
			}
			if run.LiveMetrics.Telemetry.SampleAgeUnavailable || run.LiveMetrics.Telemetry.SampleAgeMs < 8_000 {
				t.Fatalf("unseen terminal latency was hidden: %+v", run.LiveMetrics.Telemetry)
			}
			want := int64(2)
			if mode == "reconnected-first-window" {
				want = 1
			}
			if got := telemetryReplayStage(t, run, telemetryStageTransport); got.Samples != want || got.MaxMs == nil || *got.MaxMs != 9_000 {
				t.Fatalf("unseen terminal transport: %+v", got)
			}
			if got := telemetryReplayStage(t, run, telemetryStageSampleAge); got.Samples != 1 || got.MaxMs == nil || *got.MaxMs < 8_000 {
				t.Fatalf("unseen terminal sample age: %+v", got)
			}
		})
	}
}

// Completing one target must not re-age the still-running fleet's samples.
// An active target's retained window, however, continues to expose its age.
func TestTelemetryCollectionReplayPreservesActiveFleetFreshness(t *testing.T) {
	runner, run, backend := distributedRunTotalsRun(t, "mixed-fleet", nil, "finished", "active")
	old := time.Now().Add(-10 * time.Second)
	for _, target := range []string{"finished", "active"} {
		if !backend.storeRemoteMetricsBatchOnly(runner, run, target, telemetryCollectionBatch(run.ID, 1, 1, old)) {
			t.Fatal("initial collection rejected")
		}
	}
	publishTelemetryCollection(t, runner, run, backend, "active")
	terminal := telemetryCollectionBatch(run.ID, 2, 1, old)
	terminal.LifecycleState, terminal.Stats.ActiveVUsers, terminal.Groups[0].ActiveVUsers = "COMPLETE", 0, 0
	publishTerminal(t, runner, run, backend, "finished", terminal)
	if run.LiveMetrics.Telemetry.SampleAgeMs < 8_000 {
		t.Fatal("the active generator's old collection was hidden")
	}
	fresh := telemetryCollectionBatch(run.ID, 2, 2, time.Now().Add(-100*time.Millisecond))
	if !backend.storeRemoteMetricsBatchOnly(runner, run, "active", fresh) {
		t.Fatal("fresh active collection rejected")
	}
	live, _, _, accepted, publication := backend.applyStoredRemoteMetricsBatch(runner, run, "active")
	if !accepted || !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
		t.Fatal("fresh active collection not published")
	}
	if run.LiveMetrics.Telemetry.SampleAgeUnavailable || run.LiveMetrics.Telemetry.CollectedAt.UnixMilli() != fresh.Telemetry.CollectedAtUnixMs {
		t.Fatalf("completed target still owns fleet age: %+v", run.LiveMetrics.Telemetry)
	}
	if got := telemetryReplayStage(t, run, telemetryStageTransport); got.Samples != 3 {
		t.Fatalf("fleet stage samples: %+v", got)
	}
}

// Receipt is not proof of end-to-end publication. A final collection can be
// throttled, lose a publication guard, or arrive while an older snapshot is
// being published; the terminal delivery must still expose its actual age.
func TestTelemetryCollectionReplayRequiresInstalledCollection(t *testing.T) {
	for _, mode := range []string{"throttled", "rejected", "older-snapshot-installed"} {
		t.Run(mode, func(t *testing.T) {
			runner, run, backend := distributedRunTotalsRun(t, "unpublished-final", nil, "gen-a")
			first := telemetryCollectionBatch(run.ID, 1, 1, time.Now().Add(-10*time.Second))
			if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", first) {
				t.Fatal("first collection rejected")
			}
			live, _, _, accepted, publication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-a")
			if !accepted {
				t.Fatal("first aggregate rejected")
			}
			final := telemetryCollectionBatch(run.ID, 2, 1, time.UnixMilli(first.Telemetry.CollectedAtUnixMs))
			wantAgeSamples := int64(1)
			switch mode {
			case "rejected":
				run.remoteControlGeneration++
				if backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
					t.Fatal("stale topology publication installed")
				}
			case "older-snapshot-installed":
				newer := telemetryCollectionBatch(run.ID, 2, 2, time.Now().Add(-9*time.Second))
				if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", newer) {
					t.Fatal("newer collection rejected")
				}
				if !backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication).published {
					t.Fatal("older still-current snapshot failed to install")
				}
				final = telemetryCollectionBatch(run.ID, 3, 2, time.UnixMilli(newer.Telemetry.CollectedAtUnixMs))
				wantAgeSamples++
			}
			final.LifecycleState, final.Stats.ActiveVUsers, final.Groups[0].ActiveVUsers = "COMPLETE", 0, 0
			publishTerminal(t, runner, run, backend, "gen-a", final)
			if !run.remoteMetricsBatchesByTarget["gen-a"].Telemetry.CollectionRepeated {
				t.Fatal("receipt replay was not recognized")
			}
			if run.LiveMetrics.Telemetry.SampleAgeUnavailable || run.LiveMetrics.Telemetry.SampleAgeMs < 8_000 {
				t.Fatalf("first publication of the final collection was hidden: %+v", run.LiveMetrics.Telemetry)
			}
			if got := telemetryReplayStage(t, run, telemetryStageSampleAge); got.Samples != wantAgeSamples {
				t.Fatalf("first terminal publication was not scored: %+v", got)
			}
		})
	}
}

// Two generators finish execution before either archive finishes. A COMPLETE
// from one must not re-age the other generator's explicitly finalizing drain.
func TestTelemetryCollectionReplayUsesExplicitFinalizingPeer(t *testing.T) {
	runner, run, backend := distributedRunTotalsRun(t, "two-final-drains", nil, "gen-a", "gen-b")
	at := time.Now().Add(-10 * time.Second)
	for _, target := range []string{"gen-a", "gen-b"} {
		if !backend.storeRemoteMetricsBatchOnly(runner, run, target, telemetryCollectionBatch(run.ID, 1, 1, at)) {
			t.Fatal("initial drain rejected")
		}
	}
	publishTelemetryCollection(t, runner, run, backend, "gen-a")
	for _, target := range []string{"gen-a", "gen-b"} {
		phase := telemetryCollectionBatch(run.ID, 2, 1, at)
		phase.LifecycleState, phase.Stats.ActiveVUsers, phase.Groups[0].ActiveVUsers = generatorrpc.MetricsLifecycleFinalizing, 0, 0
		if !backend.storeRemoteMetricsBatchOnly(runner, run, target, phase) {
			t.Fatal("finalization phase rejected")
		}
	}
	publishTelemetryCollection(t, runner, run, backend, "gen-b")
	if run.Status != "RUNNING" || run.remoteFinalizing || !run.LiveMetrics.Telemetry.SampleAgeUnavailable {
		t.Fatalf("pending archives completed the run or re-aged drains: status=%s finalizing=%v telemetry=%+v", run.Status, run.remoteFinalizing, run.LiveMetrics.Telemetry)
	}
	before := telemetryReplayStage(t, run, telemetryStageSampleAge).Samples
	terminal := telemetryCollectionBatch(run.ID, 3, 1, at)
	terminal.LifecycleState, terminal.Stats.ActiveVUsers, terminal.Groups[0].ActiveVUsers = "COMPLETE", 0, 0
	publishTerminal(t, runner, run, backend, "gen-a", terminal)
	if !run.LiveMetrics.Telemetry.SampleAgeUnavailable || telemetryReplayStage(t, run, telemetryStageSampleAge).Samples != before {
		t.Fatal("first terminal re-aged the other generator's final drain")
	}
	wantRunTotals(t, run, 30, 4)
	if got := telemetryReplayStage(t, run, telemetryStageTransport); got.Samples != 2 {
		t.Fatalf("completion phases re-counted transport: %+v", got)
	}
}

func TestTelemetryCollectionReplayKeepsFirstFinalizingPublication(t *testing.T) {
	for _, received := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconnect-first-drain", true: "received-never-published"}[received], func(t *testing.T) {
			runner, run, backend := distributedRunTotalsRun(t, "unseen-finalizing", nil, "gen-a")
			at := time.Now().Add(-10 * time.Second)
			if received && !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", telemetryCollectionBatch(run.ID, 1, 1, at)) {
				t.Fatal("initial drain rejected")
			}
			phase := telemetryCollectionBatch(run.ID, 2, 1, at)
			phase.LifecycleState, phase.Stats.ActiveVUsers, phase.Groups[0].ActiveVUsers = generatorrpc.MetricsLifecycleFinalizing, 0, 0
			if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", phase) {
				t.Fatal("finalization phase rejected")
			}
			publishTelemetryCollection(t, runner, run, backend, "gen-a")
			if run.remoteFinalizing || run.Status != "RUNNING" || run.LiveMetrics.Telemetry.SampleAgeUnavailable || run.LiveMetrics.Telemetry.SampleAgeMs < 8_000 {
				t.Fatalf("unseen finalizing drain lost latency or completed early: %+v", run.LiveMetrics.Telemetry)
			}
			if got := telemetryReplayStage(t, run, telemetryStageSampleAge); got.Samples != 1 {
				t.Fatalf("first finalizing publication was not scored: %+v", got)
			}
		})
	}
}

func TestTelemetryCollectionFinalizingCannotDowngradeWithinEpoch(t *testing.T) {
	prior := telemetryCollectionBatch("run", 2, 1, time.Now())
	prior.LifecycleState = generatorrpc.MetricsLifecycleFinalizing
	for _, state := range []string{"STAGED", "RUNNING", "PAUSED", generatorrpc.MetricsLifecycleFinalizing, "COMPLETE", "FAILED", "ABORTED"} {
		next := telemetryCollectionBatch("run", 3, 1, time.Now())
		next.LifecycleState = state
		want := generatorrpc.IsMetricsCollectionFinal(state)
		if got := remoteMetricsBatchSupersedes(prior, next); got != want {
			t.Fatalf("FINALIZING -> %s accepted=%v, want %v", state, got, want)
		}
	}
}

// A reconnect can first see a collection in its final phase. Its first
// installed publication is scored, but that retained envelope must not keep
// aging merely because another generator publishes its terminal outcome.
func TestTelemetryCollectionReplayDoesNotReageFirstSeenFinalPeer(t *testing.T) {
	for _, state := range []string{generatorrpc.MetricsLifecycleFinalizing, "COMPLETE"} {
		t.Run(state, func(t *testing.T) {
			runner, run, backend := distributedRunTotalsRun(t, "first-final-peer", nil, "gen-a", "gen-b")
			at := time.Now().Add(-10 * time.Second)
			for _, target := range []string{"gen-a", "gen-b"} {
				batch := telemetryCollectionBatch(run.ID, 1, 1, at)
				batch.LifecycleState = generatorrpc.MetricsLifecycleFinalizing
				if target == "gen-b" {
					batch.LifecycleState = state
				}
				batch.Stats.ActiveVUsers, batch.Groups[0].ActiveVUsers = 0, 0
				if target == "gen-b" && state == "COMPLETE" {
					publishTerminal(t, runner, run, backend, target, batch)
				} else if !backend.storeRemoteMetricsBatchOnly(runner, run, target, batch) {
					t.Fatalf("%s first final collection rejected", target)
				}
			}
			if state != "COMPLETE" {
				publishTelemetryCollection(t, runner, run, backend, "gen-b")
			}
			if run.remoteMetricsBatchesByTarget["gen-b"].Telemetry.CollectionRepeated || run.LiveMetrics.Telemetry.SampleAgeUnavailable || run.LiveMetrics.Telemetry.SampleAgeMs < 8_000 {
				t.Fatal("first final collection was not scored as new")
			}
			before := telemetryReplayStage(t, run, telemetryStageSampleAge).Samples
			terminal := telemetryCollectionBatch(run.ID, 2, 1, at)
			terminal.LifecycleState, terminal.Stats.ActiveVUsers, terminal.Groups[0].ActiveVUsers = "COMPLETE", 0, 0
			publishTerminal(t, runner, run, backend, "gen-a", terminal)
			if !run.LiveMetrics.Telemetry.SampleAgeUnavailable || telemetryReplayStage(t, run, telemetryStageSampleAge).Samples != before {
				t.Fatal("the first-seen final peer was re-aged after its first installed publication")
			}
		})
	}
}

func TestTelemetryCollectionReplayPreservesSingleIntervalEvidence(t *testing.T) {
	for _, firstState := range []string{"RUNNING", generatorrpc.MetricsLifecycleFinalizing} {
		for _, publishFirst := range []bool{false, true} {
			t.Run(firstState+map[bool]string{false: "/throttled", true: "/published"}[publishFirst], func(t *testing.T) {
				runner, run, backend := distributedRunTotalsRun(t, "final-interval-evidence", []TestGroup{{Name: "buyers", VUsers: 1}}, "gen-a")
				run.JointEvidence = jointTestProjection.Clone()
				run.GoalConfig = &GoalConfig{TransactionResponseTimeSLAMs: float64Pointer(100)}
				first := telemetryCollectionBatch(run.ID, 1, 1, time.Now().Add(-10*time.Second))
				first.LifecycleState = firstState
				first.ActionMetricsVersion = 3
				first.ActionMetrics = []generatorrpc.MetricAggregate{jointAggregate("checkout", 25)}
				first.JointMetrics = []generatorrpc.MetricAggregate{jointAggregate(jointKey("checkout", "east", "3g"), 25)}
				first.JointInfo = jointBatch(1, time.Now()).JointInfo
				first.Transactions = []generatorrpc.TransactionMetric{{Name: "checkout", Group: "buyers", DurationMs: 25, Success: true}}
				if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", first) || len(run.pendingJointPublications) != 1 {
					t.Fatal("first collection lost joint evidence")
				}
				if publishFirst {
					publishTelemetryCollection(t, runner, run, backend, "gen-a")
				}
				for index, state := range []string{generatorrpc.MetricsLifecycleFinalizing, "COMPLETE"} {
					phase := first
					phase.BatchSequence, phase.TimestampUnixMs = uint64(index+2), time.Now().UnixMilli()
					phase.LifecycleState, phase.Stats.ActiveVUsers = state, 0
					pending := len(run.pendingJointPublications)
					if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", phase) {
						t.Fatalf("%s replay rejected", state)
					}
					if len(run.pendingJointPublications) != pending {
						t.Fatalf("%s replay invented joint evidence", state)
					}
					if rows := remoteTransactionResponseTimeSLAResults(run); len(rows) != 1 || rows[0].TotalCount != 1 || rows[0].EligibleSuccessCount != 1 {
						t.Fatalf("%s replay recounted legacy SLA: %+v", state, rows)
					}
					var live *LiveMetrics
					if state == "COMPLETE" {
						// Terminal publication normally takes the direct consumer path.
						// Inspect the retained aggregate here after storage to verify
						// the acceptance-side SLA/joint replay guards as well.
						live, _, _, _, _ = backend.applyRemoteMetricsBatchWithFreshness(runner, run, "gen-a", phase, false)
					} else {
						publishTelemetryCollection(t, runner, run, backend, "gen-a")
						live = run.LiveMetrics
					}
					publication := live.NativeEvidence.Publication
					if publication.Sequence != first.BatchSequence || publication.Timestamp.UnixMilli() != first.TimestampUnixMs || len(live.NativeEvidence.JointPublications) != 0 {
						t.Fatalf("%s replay invented an interval identity: %+v", state, live.NativeEvidence)
					}
					if len(publication.Dimensions["action"]) != 1 || publication.Joint == nil {
						t.Fatalf("%s replay dropped retained evidence", state)
					}
					stored := run.remoteMetricsBatchesByTarget["gen-a"]
					if stored.BatchSequence != phase.BatchSequence || stored.TimestampUnixMs != phase.TimestampUnixMs || stored.LifecycleState != state {
						t.Fatalf("evidence dedupe changed lifecycle envelope: %+v", stored)
					}
				}
			})
		}
	}
}

func TestTelemetryCollectionReplayRechecksPreparedPublicationTiming(t *testing.T) {
	for _, firstState := range []string{generatorrpc.MetricsLifecycleFinalizing, "RUNNING"} {
		t.Run(firstState, func(t *testing.T) {
			runner, run, backend := distributedRunTotalsRun(t, "prepared-finalization", nil, "gen-a", "gen-b")
			old := telemetryCollectionBatch(run.ID, 1, 1, time.Now().Add(-10*time.Second))
			old.LifecycleState = firstState
			if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-a", old) {
				t.Fatal("old first collection rejected")
			}
			first, _, _, accepted, firstPublication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-a")
			if !accepted {
				t.Fatal("first preparation rejected")
			}
			fresh := telemetryCollectionBatch(run.ID, 1, 2, time.Now().Add(-100*time.Millisecond))
			fresh.LifecycleState = generatorrpc.MetricsLifecycleFinalizing
			if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-b", fresh) {
				t.Fatal("fresh peer rejected")
			}
			second, _, _, accepted, secondPublication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-b")
			if !accepted {
				t.Fatal("second preparation rejected")
			}
			fresh.BatchSequence++ // Another envelope prepared before either installation.
			if !backend.storeRemoteMetricsBatchOnly(runner, run, "gen-b", fresh) {
				t.Fatal("fresh peer lifecycle replay rejected")
			}
			third, _, _, accepted, thirdPublication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-b")
			if !accepted {
				t.Fatal("third preparation rejected")
			}
			if !backend.publishDistributedMetricsRevision(context.Background(), runner, run, first, firstPublication).published || run.LiveMetrics.Telemetry.SampleAgeMs < 8_000 {
				t.Fatal("first genuine delayed publication was not scored")
			}
			if !backend.publishDistributedMetricsRevision(context.Background(), runner, run, second, secondPublication).published {
				t.Fatal("second prepared publication rejected")
			}
			if firstState == generatorrpc.MetricsLifecycleFinalizing {
				if run.LiveMetrics.Telemetry.CollectedAt.UnixMilli() != fresh.Telemetry.CollectedAtUnixMs || run.LiveMetrics.Telemetry.SampleAgeMs > 2_000 {
					t.Fatalf("prepared publication re-aged the installed final peer: %+v", run.LiveMetrics.Telemetry)
				}
			} else if run.LiveMetrics.Telemetry.SampleAgeMs < 8_000 {
				t.Fatal("stale active peer was hidden")
			}
			if !backend.publishDistributedMetricsRevision(context.Background(), runner, run, third, thirdPublication).published {
				t.Fatal("third prepared publication rejected")
			}
			wantSamples := int64(3)
			if firstState == generatorrpc.MetricsLifecycleFinalizing {
				wantSamples = 2
				if !run.LiveMetrics.Telemetry.SampleAgeUnavailable {
					t.Fatal("all installed final collections were scored again")
				}
			}
			if got := telemetryReplayStage(t, run, telemetryStageSampleAge); got.Samples != wantSamples {
				t.Fatalf("prepared publication sample count=%d, want %d", got.Samples, wantSamples)
			}
			if run.LiveMetrics.Telemetry.DroppedResults != 6 || telemetryReplayStage(t, run, telemetryStageControllerFanout).Samples != 3 {
				t.Fatal("timing refresh discarded counters or lifecycle fan-out")
			}
		})
	}
}
