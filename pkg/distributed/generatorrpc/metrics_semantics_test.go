package generatorrpc

import (
	"testing"
	"time"
)

func TestMergeGeneratorStatsSumsOnlyActiveVUsers(t *testing.T) {
	left := GeneratorStats{
		ActiveVUsers: 2,
		CPUPercent:   40,
		MemoryBytes:  100,
		GCCountDelta: 3,
	}
	right := GeneratorStats{
		ActiveVUsers: 5,
		CPUPercent:   60,
		MemoryBytes:  90,
		GCCountDelta: 2,
	}

	got := mergeGeneratorStats(left, right)
	if got.ActiveVUsers != 7 {
		t.Fatalf("active VUsers = %d, want 7", got.ActiveVUsers)
	}
	if got.CPUPercent != 60 || got.MemoryBytes != 100 || got.GCCountDelta != 3 {
		t.Fatalf("process gauges were summed: %#v", got)
	}
}

func TestCollectExecutionMetricsPublishesFreshIdleWindow(t *testing.T) {
	service := &Service{publishInterval: time.Second}
	execution := &groupExecution{lastMetrics: &executionMetricsSnapshot{
		transactions: []TransactionMetric{{Name: "old"}},
		aggregates: metricsAggregateSet{
			Transactions: []MetricAggregate{{}},
		},
		telemetry: MetricsTelemetry{WindowMs: 250},
	}}

	got, ok := service.collectExecutionMetrics(execution)
	if !ok {
		t.Fatal("idle window was omitted")
	}
	if len(got.transactions) != 0 || metricsAggregateSetHasEvidence(got.aggregates) {
		t.Fatalf("idle window replayed old evidence: %#v", got)
	}
	if got.telemetry.WindowMs != 1000 {
		t.Fatalf("idle window = %dms, want 1000ms", got.telemetry.WindowMs)
	}
}

func TestArchivePublisherIsDeterministicPerTest(t *testing.T) {
	service := &Service{executions: map[string]map[string]*groupExecution{
		"run": {"search": {}, "checkout": {}},
	}}
	if !service.isArchivePublisherLocked("run", "checkout") {
		t.Fatal("lexically first active group was not selected")
	}
	if service.isArchivePublisherLocked("run", "search") {
		t.Fatal("second group would duplicate the per-test archive interval")
	}
}
