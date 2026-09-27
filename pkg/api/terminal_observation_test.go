package api

import (
	"testing"
	"time"
)

func TestTerminalObservationIsHistoricalWithoutMutatingMeasurements(t *testing.T) {
	for _, state := range []string{"COMPLETE", "FAILED", "ABORTED", "RUNNING", "PAUSED"} {
		t.Run(state, func(t *testing.T) {
			at := time.Now().Add(-time.Minute)
			point := &LiveMetrics{Timestamp: at, ActiveVUsers: 120, TPS: 100, TotalTx: 5000}
			run := &ManagedTest{ID: "run", Status: state, LiveMetrics: point, History: []*LiveMetrics{point}}
			clone := cloneManagedTestFieldsLocked(run)
			snapshot := buildRunLiveStateSnapshotLocked(run, time.Now())
			operational := cloneOperationalTestProjectionLocked(run)
			for _, metrics := range []*LiveMetrics{clone.LiveMetrics, snapshot.LiveMetrics, operational.LiveMetrics} {
				want := "stale"
				if isTerminalTestStatus(state) {
					want = "historical"
				}
				if metrics.ObservationState != want {
					t.Fatalf("state = %q, want %q", metrics.ObservationState, want)
				}
				if metrics.ActiveVUsers != 120 || metrics.TPS != 100 || !metrics.Timestamp.Equal(at) {
					t.Fatal("historical evidence was rewritten")
				}
			}
			if clone.LiveMetrics.TotalTx != 5000 || snapshot.LiveMetrics.TotalTx != 5000 {
				t.Fatal("retained totals were rewritten")
			}
			if point.ObservationState != "" || run.History[0] != point {
				t.Fatal("projection mutated canonical sample")
			}
		})
	}
}

func TestTerminalObservationWithoutSampleRemainsAbsent(t *testing.T) {
	if cloneRunLiveMetrics(nil) != nil {
		t.Fatal("nil run produced a sample")
	}
	run := &ManagedTest{ID: "run", Status: "FAILED"}
	if cloneRunLiveMetrics(run) != nil || buildRunLiveStateSnapshotLocked(run, time.Now()).LiveMetrics != nil {
		t.Fatal("fabricated terminal observation")
	}
}

func TestTerminalObservationHistoryReloadAllowsMissingTail(t *testing.T) {
	now := time.Now()
	run := &ManagedTest{ID: "missing-tail", ProjectID: "p", Status: "FAILED", CreatedAt: now, EndedAt: &now}
	cache := newStoredTestHistoryCache()
	_, _, err := cache.getOrLoadAtRevision(storedTestHistoryCacheKey(run), func() (*storedTestHistorySnapshot, error) {
		return &storedTestHistorySnapshot{History: []*LiveMetrics{nil}, LoadedAt: now}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{run.ID: run}, historyStore: &metricsStoreSpy{}, storedHistoryCache: cache}
	copy := cloneManagedTestForReadLocked(run)
	runner.attachStoredTerminalHistory(copy)
	if copy.LiveMetrics != nil {
		t.Fatal("missing tail fabricated a sample")
	}
}
