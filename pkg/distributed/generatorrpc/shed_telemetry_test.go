package generatorrpc

import "testing"

func TestMergeMetricsTelemetrySumsSheddedArrivals(t *testing.T) {
	values := map[string]MetricsTelemetry{
		"gen-a": {DroppedResults: 1, SheddedArrivals: 4},
		"gen-b": {DroppedResults: 2, SheddedArrivals: 6},
	}
	combined := mergeMetricsTelemetry(values)
	if combined == nil {
		t.Fatal("expected non-nil combined telemetry")
	}
	if combined.SheddedArrivals != 10 {
		t.Fatalf("SheddedArrivals = %d, want 10 (summed across the fleet)", combined.SheddedArrivals)
	}
	if combined.DroppedResults != 3 {
		t.Fatalf("DroppedResults = %d, want 3", combined.DroppedResults)
	}
}
