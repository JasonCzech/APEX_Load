package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func TestLiveObservationFreshness(t *testing.T) {
	now := time.Unix(1800000000, 0).UTC()
	for _, tc := range []struct {
		name, status, want string
		at                 time.Time
	}{
		{"fresh", "RUNNING", "", now},
		{"boundary", "RUNNING", "stale", now.Add(-liveObservationFreshness)},
		{"old", "RUNNING", "stale", now.Add(-time.Minute)},
		{"paused", "PAUSED", "stale", now.Add(-time.Minute)},
		{"missing time", "RUNNING", "stale", time.Time{}},
		{"future", "RUNNING", "stale", now.Add(time.Millisecond)},
		{"terminal", "FAILED", "historical", now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			point := &LiveMetrics{Timestamp: tc.at, ActiveVUsers: 120, TPS: 100, TotalTx: 5000}
			copy := cloneLiveMetrics(point)
			annotateLiveObservation(copy, tc.status, now)
			if copy.ObservationState != tc.want {
				t.Fatalf("state = %q, want %q", copy.ObservationState, tc.want)
			}
			if copy.ActiveVUsers != 120 || copy.TPS != 100 || copy.TotalTx != 5000 || !copy.Timestamp.Equal(tc.at) {
				t.Fatal("measurements changed")
			}
			if point.ObservationState != "" || point.ObservationValidUntil != nil {
				t.Fatal("canonical sample mutated")
			}
			if tc.want == "historical" && copy.ObservationValidUntil != nil {
				t.Fatal("terminal sample still expires")
			}
		})
	}
}

func TestLiveObservationRelayDoesNotRenewFreshness(t *testing.T) {
	now := time.Now().UTC()
	deadline := now.Add(-time.Second)
	sample := &LiveMetrics{Timestamp: now, ObservationValidUntil: &deadline, ActiveVUsers: 120}
	// Exercise the same JSON round trip as shared cache and stored history.
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var restored LiveMetrics
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	run := &ManagedTest{Status: "RUNNING", LiveMetrics: &restored}
	for _, copy := range []*LiveMetrics{cloneRunLiveMetrics(run), cloneOperationalTestProjectionLocked(run).LiveMetrics, buildRunLiveStateSnapshotLocked(run, now).LiveMetrics} {
		if copy.ObservationState != "stale" || !copy.ObservationValidUntil.Equal(deadline) {
			t.Fatal("read renewed stale aggregate")
		}
		*copy.ObservationValidUntil = now.Add(time.Hour)
		if !restored.ObservationValidUntil.Equal(deadline) {
			t.Fatal("projection aliases expiry")
		}
	}
	// New evidence can become fresh; the previous stale label is not sticky.
	restored.Timestamp = now
	restored.ObservationValidUntil = nil
	restored.ObservationState = "stale"
	annotateLiveObservation(&restored, "RUNNING", now)
	if restored.ObservationState != "" {
		t.Fatal("new observation stayed stale")
	}
}

func TestDistributedObservationRequiresEveryContributor(t *testing.T) {
	now := time.Unix(1800000000, 0).UTC()
	for _, tc := range []struct {
		name             string
		second           int64
		missing, removed bool
		want             time.Time
	}{
		{name: "all fresh", second: now.Add(-time.Second).UnixMilli(), want: now.Add(4 * time.Second)},
		{name: "silent shard", second: now.Add(-time.Minute).UnixMilli(), want: now.Add(-55 * time.Second)},
		{name: "missing shard", missing: true, want: time.Unix(0, 0)},
		{name: "removed shard", missing: true, removed: true, want: now.Add(liveObservationFreshness)},
		{name: "legacy missing timestamp", want: time.Unix(0, 0)},
		{name: "clock in future", second: now.Add(time.Second).UnixMilli(), want: time.Unix(0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &ManagedTest{remoteClientsByTarget: map[string]generatorClient{"a": nil, "b": nil}, remoteTargetStates: map[string]string{}}
			if tc.removed {
				run.remoteTargetStates["b"] = remoteTargetStateEvacuated
			}
			batches := map[string]generatorrpc.MetricsBatch{"a": {TimestampUnixMs: now.UnixMilli()}}
			if !tc.missing {
				batches["b"] = generatorrpc.MetricsBatch{TimestampUnixMs: tc.second}
			}
			got := distributedObservationValidUntilLocked(run, batches, now)
			if !got.Equal(tc.want) {
				t.Fatalf("deadline = %v, want %v", got, tc.want)
			}
		})
	}
	if got := distributedObservationValidUntilLocked(&ManagedTest{}, nil, now); !got.Equal(time.Unix(0, 0)) {
		t.Fatal("empty fleet is fresh")
	}
}

func TestDistributedObservationPipelinePreservesSilentShardEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	run := &ManagedTest{ID: "freshness-run", Status: "RUNNING", remoteClientsByTarget: map[string]generatorClient{"a": nil, "b": nil}}
	runner := &TestRunner{tests: map[string]*ManagedTest{run.ID: run}}
	backend := &distributedExecutionBackend{}
	old := now.Add(-time.Minute)
	backend.applyRemoteMetricsBatch(runner, run, "b", generatorrpc.MetricsBatch{TestID: run.ID, TimestampUnixMs: old.UnixMilli(), Stats: generatorrpc.GeneratorStats{ActiveVUsers: 60}})
	live, _, _ := backend.applyRemoteMetricsBatch(runner, run, "a", generatorrpc.MetricsBatch{TestID: run.ID, TimestampUnixMs: now.UnixMilli(), Stats: generatorrpc.GeneratorStats{ActiveVUsers: 60}})
	run.LiveMetrics = live
	projection := cloneRunLiveMetrics(run)
	if projection.ObservationState != "stale" || projection.ActiveVUsers != 120 || !projection.Timestamp.Equal(now) {
		t.Fatalf("mixed-age aggregate lost provenance or values: %+v", projection)
	}
	if !projection.ObservationValidUntil.Equal(old.Add(liveObservationFreshness)) {
		t.Fatal("healthy shard renewed stale shard")
	}
	live, _, _ = backend.applyRemoteMetricsBatch(runner, run, "b", generatorrpc.MetricsBatch{TestID: run.ID, TimestampUnixMs: now.UnixMilli(), Stats: generatorrpc.GeneratorStats{ActiveVUsers: 60}})
	run.LiveMetrics = live
	if copy := cloneRunLiveMetrics(run); copy.ObservationState != "" || copy.ActiveVUsers != 120 {
		t.Fatal("fresh replacement did not restore freshness")
	}
}

func TestLiveObservationWebsocketProjectionLeavesHistoryUnchanged(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	run := &ManagedTest{ID: "stale-publication", Status: "RUNNING"}
	runner.tests[run.ID] = run
	point := &LiveMetrics{Timestamp: time.Now().Add(-time.Minute), ActiveVUsers: 120, TPS: 100}
	if !runner.publishLiveMetrics(run, point, nil) {
		t.Fatal("publication rejected")
	}
	for {
		select {
		case message := <-hub.broadcast:
			var event struct {
				Type string      `json:"type"`
				Data LiveMetrics `json:"data"`
			}
			if err := json.Unmarshal(message.payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "metrics_update" {
				continue
			}
			if event.Data.ObservationState != "stale" || event.Data.ObservationValidUntil == nil || event.Data.ActiveVUsers != 120 {
				t.Fatalf("invalid websocket observation: %+v", event.Data)
			}
			if run.LiveMetrics.ObservationState != "" || run.History[0].ObservationState != "" {
				t.Fatal("publication annotated canonical history")
			}
			return
		default:
			t.Fatal("metrics event missing")
		}
	}
}
