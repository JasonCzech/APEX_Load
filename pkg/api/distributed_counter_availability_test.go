package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDistributedReceiptAndGapDoNotInventDropCounters(t *testing.T) {
	for _, reported := range []bool{false, true} {
		batch := generatorrpc.MetricsBatch{TimestampUnixMs: 1000}
		if reported {
			batch.Telemetry = &generatorrpc.MetricsTelemetry{}
		}
		recordDistributedMetricsReceipt(&batch, 1100)
		live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
		if live.Telemetry.DroppedResultsAvailable != reported || live.Telemetry.ControllerReceivedAt.UnixMilli() != 1100 {
			t.Fatalf("source=%v telemetry=%+v", reported, live.Telemetry)
		}
		evidence := baselineTelemetryEvidence("run", []IntervalSummaryRecord{{Telemetry: live.Telemetry}})
		if (evidence.DroppedResults != nil) != reported {
			t.Fatalf("source=%v evidence=%+v", reported, evidence)
		}
	}
	runner, run, backend := newDistributedThresholdTestRunner(t)
	first := distributedP99Batch(run.ID, 1000, 100)
	first.BatchSequence = 1
	third := distributedP99Batch(run.ID, 3000, 100)
	third.BatchSequence = 3
	for _, batch := range []generatorrpc.MetricsBatch{first, third} {
		if !backend.storeRemoteMetricsBatchOnly(runner, run, "generator-a", batch) {
			t.Fatal("batch rejected")
		}
	}
	stored := run.remoteMetricsBatchesByTarget["generator-a"]
	live := buildLiveMetricsFromDistributedBatch(run, stored, 0, nil)
	if live.Telemetry.MissingMetricWindows != 1 || live.Telemetry.DroppedResultsAvailable {
		t.Fatalf("gap metadata invented measured zero: %+v", live.Telemetry)
	}
}

func TestDistributedDropCounterAvailabilityRequiresEveryPeer(t *testing.T) {
	for _, missing := range []bool{false, true} {
		batches := map[string]generatorrpc.MetricsBatch{"a": {Telemetry: &generatorrpc.MetricsTelemetry{}}, "b": {Telemetry: &generatorrpc.MetricsTelemetry{}}}
		if missing {
			batches["b"] = generatorrpc.MetricsBatch{}
		}
		combined, _, _ := aggregateDistributedMetricsBatches("run", batches)
		live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, 0, nil)
		if live.Telemetry.DroppedResultsAvailable == missing {
			t.Fatalf("missing=%v telemetry=%+v", missing, live.Telemetry)
		}
		if missing {
			b := batches["b"]
			recordDistributedMetricsReceipt(&b, 1100)
			batches["b"] = b
			combined, _, _ = aggregateDistributedMetricsBatches("run", batches)
			live = buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, 0, nil)
			if live.Telemetry.DroppedResultsAvailable {
				t.Fatal("controller-only metadata qualified a partial counter cohort")
			}
		}
	}
}

func TestRemoteArchiveDropCounterAvailabilityRequiresEveryPeer(t *testing.T) {
	for _, incoming := range []*TelemetryHealth{nil, {}, {DroppedResultsAvailable: true}} {
		current := &TelemetryHealth{DroppedResultsAvailable: true}
		got := mergeRemoteArchiveSummaryTelemetry(current, incoming)
		want := incoming != nil && incoming.DroppedResultsAvailable
		if got.DroppedResultsAvailable != want {
			t.Fatalf("incoming=%+v got=%+v", incoming, got)
		}
	}
}

func TestRemoteArchiveFleetRetainsUnknownDropCounterSources(t *testing.T) {
	for _, tc := range []struct {
		name      string
		a, b      *TelemetryHealth
		omitB     bool
		available bool
		drops     int64
	}{
		{name: "all reported zero", a: &TelemetryHealth{DroppedResultsAvailable: true}, b: &TelemetryHealth{DroppedResultsAvailable: true}, available: true},
		{name: "invalid second source cannot cancel positive", a: &TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 1}, b: &TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: -1}, drops: 1},
		{name: "invalid first source cannot cancel positive", a: &TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: -1}, b: &TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 1}, drops: 1},
		{name: "invalid source cannot qualify zero", a: &TelemetryHealth{DroppedResultsAvailable: true}, b: &TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: -1}},
		{name: "overflow cannot qualify exact counter", a: &TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: math.MaxInt64}, b: &TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 1}, drops: math.MaxInt64},
		{name: "first source missing", b: &TelemetryHealth{DroppedResultsAvailable: true}},
		{name: "second source missing", a: &TelemetryHealth{DroppedResultsAvailable: true}},
		{name: "legacy metadata only", a: &TelemetryHealth{CollectedAt: time.Now()}, b: &TelemetryHealth{DroppedResultsAvailable: true}},
		{name: "expected peer absent", a: &TelemetryHealth{DroppedResultsAvailable: true}, omitB: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			at := time.Date(2026, 10, 2, 0, 0, 1, 0, time.UTC)
			write := func(name string, telemetry *TelemetryHealth) remoteArchiveBundle {
				path := filepath.Join(root, name+".jsonl")
				data, err := json.Marshal(IntervalSummaryRecord{TestID: "run", Timestamp: at, Telemetry: telemetry})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				return remoteArchiveBundle{target: name, summariesPath: path, manifest: RunArchiveManifest{IntervalSummaryCount: 1}}
			}
			bundles := []remoteArchiveBundle{write("a", tc.a)}
			if !tc.omitB {
				bundles = append(bundles, write("b", tc.b))
			}
			destination := filepath.Join(root, "fleet.jsonl")
			coverage := &RunArchiveSourceCoverage{Sources: []RunArchiveSource{{Target: "a", Collected: true}, {Target: "b", Collected: !tc.omitB}}}
			count, err := mergeRemoteArchiveIntervalSummariesWithCoverage(destination, bundles, nil, coverage)
			if err != nil || count != 1 {
				t.Fatalf("merge count %d: %v", count, err)
			}
			data, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			var row IntervalSummaryRecord
			if err := json.Unmarshal(data, &row); err != nil {
				t.Fatal(err)
			}
			if row.Telemetry == nil || row.Telemetry.DroppedResultsAvailable != tc.available || row.Telemetry.DroppedResults != tc.drops {
				t.Fatalf("availability changed %+v", row.Telemetry)
			}
			evidence := baselineTelemetryEvidence("run", []IntervalSummaryRecord{row})
			if (evidence.DroppedResults != nil) != (tc.available || tc.drops > 0) {
				t.Fatalf("unknown source became zero: %+v", evidence)
			}
		})
	}
}

func TestDistributedDropCounterRequiresColdAssignedPeers(t *testing.T) {
	runner, run, backend := newDistributedThresholdTestRunner(t)
	run.remoteClientsByTarget["generator-b"] = nil
	batchA := distributedP99Batch(run.ID, 1000, 100)
	batchA.Telemetry = &generatorrpc.MetricsTelemetry{}
	if !backend.storeRemoteMetricsBatchOnly(runner, run, "generator-a", batchA) {
		t.Fatal("batch A rejected")
	}
	live, _, _, fresh, _ := backend.applyStoredRemoteMetricsBatch(runner, run, "generator-a")
	if !fresh || live == nil || live.Telemetry.DroppedResultsAvailable {
		t.Fatalf("cold peer inferred zero: %+v", live)
	}
	archived := IntervalSummaryRecord{Timestamp: live.Timestamp, Telemetry: &TelemetryHealth{DroppedResultsAvailable: false}}
	applyRemoteArchiveTargetSnapshot(&archived, []remoteArchiveTargetSnapshot{{timestamp: live.Timestamp, telemetry: live.Telemetry}})
	if archived.Telemetry.DroppedResultsAvailable {
		t.Fatal("snapshot qualified cold peer")
	}
	// An intentionally removed cold peer does not block the current fleet.
	run.remoteTargetStates["generator-b"] = "evacuated"
	batchA.TimestampUnixMs = 2000
	if !backend.storeRemoteMetricsBatchOnly(runner, run, "generator-a", batchA) {
		t.Fatal("second A rejected")
	}
	live, _, _, fresh, _ = backend.applyStoredRemoteMetricsBatch(runner, run, "generator-a")
	if !fresh || !live.Telemetry.DroppedResultsAvailable {
		t.Fatal("removed cold peer blocked fleet")
	}
	delete(run.remoteTargetStates, "generator-b")
	batchB := distributedP99Batch(run.ID, 2000, 100)
	batchB.Telemetry = &generatorrpc.MetricsTelemetry{}
	if !backend.storeRemoteMetricsBatchOnly(runner, run, "generator-b", batchB) {
		t.Fatal("batch B rejected")
	}
	live, _, _, fresh, _ = backend.applyStoredRemoteMetricsBatch(runner, run, "generator-b")
	if !fresh || !live.Telemetry.DroppedResultsAvailable {
		t.Fatal("complete reported zero unavailable")
	}
}

func TestArchiveSnapshotPreservesIndependentCounterEvidence(t *testing.T) {
	at := time.Now().UTC()
	for _, tc := range []struct {
		name          string
		raw, snapshot TelemetryHealth
		available     bool
		drops         int64
	}{
		{name: "known snapshot supplies legacy archive zero", snapshot: TelemetryHealth{DroppedResultsAvailable: true}, available: true},
		{name: "known archive survives metadata snapshot", raw: TelemetryHealth{DroppedResultsAvailable: true}, available: true},
		{name: "neither source knows zero"},
		{name: "negative archive cannot qualify metadata zero", raw: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: -1}},
		{name: "negative snapshot cannot qualify legacy zero", snapshot: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: -1}},
		{name: "valid snapshot survives invalid archive", raw: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: -1}, snapshot: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 2}, available: true, drops: 2},
		{name: "valid archive survives invalid snapshot", raw: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 2}, snapshot: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: -1}, available: true, drops: 2},
		{name: "snapshot cannot erase raw loss", raw: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 5}, snapshot: TelemetryHealth{DroppedResultsAvailable: true}, available: true, drops: 5},
		{name: "partial snapshot cannot erase raw loss", raw: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 5}, snapshot: TelemetryHealth{DroppedResults: 1}, available: true, drops: 5},
		{name: "positive legacy archive retained", raw: TelemetryHealth{DroppedResults: 3}, drops: 3},
		{name: "higher independent snapshot retained", raw: TelemetryHealth{DroppedResults: 3}, snapshot: TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: 5}, available: true, drops: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rawBefore, snapshotBefore := tc.raw, tc.snapshot
			row := IntervalSummaryRecord{Timestamp: at, Telemetry: &tc.raw}
			applyRemoteArchiveTargetSnapshot(&row, []remoteArchiveTargetSnapshot{{timestamp: at, telemetry: &tc.snapshot}})
			if row.Telemetry.DroppedResultsAvailable != tc.available || row.Telemetry.DroppedResults != tc.drops {
				t.Fatalf("evidence changed %+v", row.Telemetry)
			}
			if !reflect.DeepEqual(tc.raw, rawBefore) || !reflect.DeepEqual(tc.snapshot, snapshotBefore) {
				t.Fatal("snapshot reconciliation mutated source evidence")
			}
		})
	}
}
