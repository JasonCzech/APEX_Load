package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func successTransactions(group string, n int) []generatorrpc.TransactionMetric {
	txs := make([]generatorrpc.TransactionMetric, 0, n)
	for i := 0; i < n; i++ {
		txs = append(txs, generatorrpc.TransactionMetric{Name: "tx", Group: group, DurationMs: 100, Success: true})
	}
	return txs
}

// TPS is a per-second rate that is additive across generators: each generator's
// success count is divided by its OWN window before summing.
func TestAggregateDistributedMetricsBatchesTPSIsSumOfRates(t *testing.T) {
	tests := []struct {
		name      string
		batches   map[string]generatorrpc.MetricsBatch
		wantFleet float64
		wantGroup float64
	}{
		{
			name: "heterogeneous windows sum as rates",
			batches: map[string]generatorrpc.MetricsBatch{
				"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 10)},
				"gen-b": {TestID: "t", WindowMs: 2000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 20)},
				"gen-c": {TestID: "t", WindowMs: 500, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 30)},
			},
			// 10/1 + 20/2 + 30/0.5 = 10 + 10 + 60
			wantFleet: 80,
			wantGroup: 80,
		},
		{
			name: "window_ms=0 falls back to 1s (mixed-version fleet, no div-by-zero)",
			batches: map[string]generatorrpc.MetricsBatch{
				"old": {TestID: "t", WindowMs: 0, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 5)},
				"new": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 10)},
			},
			wantFleet: 15,
			wantGroup: 15,
		},
		{
			name: "coalesced 2x window halves the rate vs a naive count",
			batches: map[string]generatorrpc.MetricsBatch{
				"gen": {TestID: "t", WindowMs: 2000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 20)},
			},
			wantFleet: 10,
			wantGroup: 10,
		},
		{
			name: "per-group rate is additive across generators",
			batches: map[string]generatorrpc.MetricsBatch{
				"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 6)},
				"gen-b": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 4)},
			},
			wantFleet: 10,
			wantGroup: 10,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", tc.batches)
			if fleetTPS != tc.wantFleet {
				t.Fatalf("fleet TPS = %v, want %v", fleetTPS, tc.wantFleet)
			}
			if groupTPS["A"] != tc.wantGroup {
				t.Fatalf("group A TPS = %v, want %v", groupTPS["A"], tc.wantGroup)
			}
		})
	}
}

// buildLiveMetricsFromDistributedBatch must surface the windowed fleet rate on
// live.TPS and resolve per-group rates onto configured group names.
func TestBuildLiveMetricsFromDistributedBatchTPSIsRate(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "A", VUsers: 2}}}
	batches := map[string]generatorrpc.MetricsBatch{
		"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 6)},
		"gen-b": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: successTransactions("A", 4)},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	if live.TPS != 10 {
		t.Fatalf("live TPS = %v, want 10 (6+4 over 1s windows)", live.TPS)
	}
	if live.TotalTx != 10 {
		t.Fatalf("live TotalTx = %d, want 10", live.TotalTx)
	}
	group := live.GroupMetrics["A"]
	if group == nil || group.TPS != 10 {
		t.Fatalf("group A TPS = %#v, want 10", group)
	}
}

// Empty group names fall back to the sole configured group for both bucketing
// and rate resolution.
func TestBuildLiveMetricsFromDistributedBatchResolvesEmptyGroupTPS(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "only", VUsers: 3}}}
	batches := map[string]generatorrpc.MetricsBatch{
		"gen-a": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 3}, Transactions: successTransactions("", 8)},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	if live.TPS != 8 {
		t.Fatalf("live TPS = %v, want 8", live.TPS)
	}
	group := live.GroupMetrics["only"]
	if group == nil || group.TPS != 8 {
		t.Fatalf("group only TPS = %#v, want 8", group)
	}
}

func TestBuildLiveMetricsFromDistributedBatchNetworkPhasesAndErrorBreakdown(t *testing.T) {
	test := &ManagedTest{Groups: []TestGroup{{Name: "A"}}}
	phase := generatorrpc.TransactionMetric{Name: "tx", Group: "A", DurationMs: 150, DNSMs: 12, ConnectMs: 24, TLSMs: 36, TTFBMs: 90}
	success := phase
	success.Success = true
	httpErr := phase
	httpErr.Success = false
	httpErr.StatusCode = 500
	httpErr.ErrorMessage = "boom"
	timeoutErr := phase
	timeoutErr.Success = false
	timeoutErr.ErrorMessage = "context deadline exceeded"

	batches := map[string]generatorrpc.MetricsBatch{
		"gen": {TestID: "t", WindowMs: 1000, Stats: generatorrpc.GeneratorStats{ActiveVUsers: 1}, Transactions: []generatorrpc.TransactionMetric{success, httpErr, timeoutErr}},
	}
	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("t", batches)
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)

	// Phase averages are over all 3 attempts; identical per-tx values keep them exact.
	if live.AvgDNSMs != 12 || live.AvgConnectMs != 24 || live.AvgTLSMs != 36 || live.AvgTTFBMs != 90 {
		t.Fatalf("phase averages = dns %v connect %v tls %v ttfb %v", live.AvgDNSMs, live.AvgConnectMs, live.AvgTLSMs, live.AvgTTFBMs)
	}
	if live.AvgWaitMs != 60 { // 150 - 90
		t.Fatalf("avg wait = %v, want 60", live.AvgWaitMs)
	}
	if live.ErrorBreakdown["http_5xx"] != 1 || live.ErrorBreakdown["timeout"] != 1 {
		t.Fatalf("error breakdown = %#v, want one http_5xx and one timeout", live.ErrorBreakdown)
	}
	if live.TPS != 1 { // one success over a 1s window
		t.Fatalf("live TPS = %v, want 1", live.TPS)
	}
}

func TestAggregateDistributedGeneratorStats(t *testing.T) {
	t.Run("CPU averages over active pods, max preserved, GC pause maxed", func(t *testing.T) {
		stats := []generatorrpc.GeneratorStats{
			{ActiveVUsers: 5, CPUPercent: 90, MemoryBytes: 100, GCPauseMs: 120},
			{ActiveVUsers: 5, CPUPercent: 60, MemoryBytes: 200, GCPauseMs: 40},
			{ActiveVUsers: 5, CPUPercent: 30, MemoryBytes: 300, GCPauseMs: 0},
		}
		agg := aggregateDistributedGeneratorStats(stats)
		if agg.CPUPercent != 60 {
			t.Fatalf("CPUPercent = %v, want 60 (avg of 90,60,30)", agg.CPUPercent)
		}
		if agg.CPUProcessPctMax != 90 {
			t.Fatalf("CPUProcessPctMax = %v, want 90", agg.CPUProcessPctMax)
		}
		if agg.GCPauseMs != 120 {
			t.Fatalf("GCPauseMs = %v, want 120 (max)", agg.GCPauseMs)
		}
		if agg.MemoryBytes != 600 {
			t.Fatalf("MemoryBytes = %d, want 600 (sum)", agg.MemoryBytes)
		}
		if agg.ActiveVUsers != 15 {
			t.Fatalf("ActiveVUsers = %d, want 15 (sum)", agg.ActiveVUsers)
		}
	})

	t.Run("idle pods are excluded from the CPU average", func(t *testing.T) {
		stats := []generatorrpc.GeneratorStats{
			{ActiveVUsers: 10, CPUPercent: 90},
			{ActiveVUsers: 0, CPUPercent: 10},
		}
		agg := aggregateDistributedGeneratorStats(stats)
		if agg.CPUPercent != 90 {
			t.Fatalf("CPUPercent = %v, want 90 (idle pod excluded)", agg.CPUPercent)
		}
		if agg.CPUProcessPctMax != 90 {
			t.Fatalf("CPUProcessPctMax = %v, want 90", agg.CPUProcessPctMax)
		}
	})

	t.Run("all-idle falls back to averaging every pod", func(t *testing.T) {
		stats := []generatorrpc.GeneratorStats{
			{ActiveVUsers: 0, CPUPercent: 40},
			{ActiveVUsers: 0, CPUPercent: 20},
		}
		agg := aggregateDistributedGeneratorStats(stats)
		if agg.CPUPercent != 30 {
			t.Fatalf("CPUPercent = %v, want 30 (fallback avg of all)", agg.CPUPercent)
		}
	})
}
