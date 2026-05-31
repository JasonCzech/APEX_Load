package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRecordDataPointAggregates(t *testing.T) {
	mc := NewMetricsCollector(16)
	mc.RecordDataPoint("latency", 10, "checkout")
	mc.RecordDataPoint("latency", 20, "checkout")
	mc.RecordDataPoint("latency", 30, "checkout")

	snap := mc.computeSnapshot(nil, time.Second)
	dp, ok := snap.DataPoints["latency"]
	if !ok {
		t.Fatalf("data point not present in snapshot: %#v", snap.DataPoints)
	}
	if dp.Count != 3 {
		t.Errorf("count = %d, want 3", dp.Count)
	}
	if dp.Sum != 60 {
		t.Errorf("sum = %v, want 60", dp.Sum)
	}
	if dp.Avg != 20 {
		t.Errorf("avg = %v, want 20", dp.Avg)
	}
	if dp.Min != 10 || dp.Max != 30 {
		t.Errorf("min/max = %v/%v, want 10/30", dp.Min, dp.Max)
	}
	if dp.Last != 30 {
		t.Errorf("last = %v, want 30", dp.Last)
	}
	if dp.Group != "checkout" {
		t.Errorf("group = %q, want checkout", dp.Group)
	}

	// Snapshot must be a copy: mutating the snapshot does not corrupt the collector.
	dp.Count = 999
	if again := mc.computeSnapshot(nil, time.Second).DataPoints["latency"]; again.Count != 3 {
		t.Errorf("collector state mutated via snapshot: count = %d, want 3", again.Count)
	}
}

func TestRecordDataPointIgnoresEmptyName(t *testing.T) {
	mc := NewMetricsCollector(16)
	mc.RecordDataPoint("", 5, "g")
	if snap := mc.computeSnapshot(nil, time.Second); len(snap.DataPoints) != 0 {
		t.Fatalf("data points = %#v, want none for empty name", snap.DataPoints)
	}
}

func TestHandleLoadRunnerMetricActionRecordsDataPoint(t *testing.T) {
	mc := NewMetricsCollector(16)
	vu := &VUser{ID: 1, Group: "checkout", Metrics: mc}

	vu.handleLoadRunnerMetricAction(context.Background(), dsl.Action{
		Name: "latency",
		Type: "loadrunner_metric",
		Params: map[string]any{
			"function": "lr_user_data_point",
			"args":     []any{"latency", "42.5"},
		},
	})

	dp, ok := mc.computeSnapshot(nil, time.Second).DataPoints["latency"]
	if !ok {
		t.Fatalf("lr_user_data_point did not record a metric")
	}
	if dp.Count != 1 || dp.Last != 42.5 || dp.Group != "checkout" {
		t.Fatalf("data point = %#v, want one sample of 42.5 in group checkout", dp)
	}
}

func TestHandleLoadRunnerMetricActionSkipsNonNumeric(t *testing.T) {
	mc := NewMetricsCollector(16)
	vu := &VUser{ID: 1, Group: "g", Metrics: mc}

	// lr_checkpoint has no numeric value; it must not record (or panic).
	vu.handleLoadRunnerMetricAction(context.Background(), dsl.Action{
		Name: "Checkpoint1",
		Type: "loadrunner_metric",
		Params: map[string]any{
			"function": "lr_checkpoint",
			"args":     []any{"Checkpoint1"},
		},
	})

	if snap := mc.computeSnapshot(nil, time.Second); len(snap.DataPoints) != 0 {
		t.Fatalf("data points = %#v, want none for non-numeric metric call", snap.DataPoints)
	}
}
