package engine

import (
	"testing"
	"time"
)

func TestRecordSheddedArrivalSurfacedInSnapshot(t *testing.T) {
	mc := NewMetricsCollector(100)
	if mc == nil {
		t.Fatal("nil collector")
	}
	mc.RecordSheddedArrival()
	mc.RecordSheddedArrival()
	mc.RecordSheddedArrival()

	snap := mc.computeSnapshot(nil, time.Second)
	if snap.Telemetry == nil {
		t.Fatal("expected telemetry in snapshot")
	}
	if snap.Telemetry.SheddedArrivals != 3 {
		t.Fatalf("SheddedArrivals = %d, want 3", snap.Telemetry.SheddedArrivals)
	}
}

func TestRecordSheddedArrivalNilSafe(t *testing.T) {
	var mc *MetricsCollector
	mc.RecordSheddedArrival() // must not panic on a nil collector
}
