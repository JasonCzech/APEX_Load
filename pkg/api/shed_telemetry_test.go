package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestBuildLiveMetricsFromSnapshotMapsSheddedArrivals(t *testing.T) {
	snap := engine.MetricsSnapshot{
		Telemetry: &engine.TelemetryMetrics{
			DroppedResults:  2,
			SheddedArrivals: 5,
		},
	}
	live := buildLiveMetricsFromSnapshot(snap, nil)
	if live == nil || live.Telemetry == nil {
		t.Fatal("expected telemetry in live metrics")
	}
	if live.Telemetry.SheddedArrivals != 5 {
		t.Fatalf("SheddedArrivals = %d, want 5", live.Telemetry.SheddedArrivals)
	}
	if live.Telemetry.DroppedResults != 2 {
		t.Fatalf("DroppedResults = %d, want 2", live.Telemetry.DroppedResults)
	}
}
