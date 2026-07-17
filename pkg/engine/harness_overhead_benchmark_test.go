package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// BenchmarkHarnessOverheadPerTransaction measures the per-transaction cost of
// the metrics hot path (Record): the atomic counter updates plus the non-blocking
// buffered-channel handoff. This is the "harness overhead per transaction"
// component of the <50µs design target. A draining goroutine keeps the channel
// from filling so we measure the realistic successful-send path, not the
// drop-on-full branch.
//
// Run: go test -run=^$ -bench=BenchmarkHarnessOverhead -benchmem ./pkg/engine/
func BenchmarkHarnessOverheadPerTransaction(b *testing.B) {
	mc := NewMetricsCollector(1 << 16)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-mc.ch:
			case <-done:
				return
			}
		}
	}()

	result := &protocol.Result{
		ActionName: "checkout",
		Group:      "shoppers",
		StartTime:  time.Now(),
		Duration:   5 * time.Millisecond,
		Success:    true,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.Record(result)
	}
	b.StopTimer()
	close(done)
}

// BenchmarkHarnessOverheadParallel measures the same hot path under contention
// from many VUser goroutines recording concurrently — the realistic shape of a
// loaded generator pod.
func BenchmarkHarnessOverheadParallel(b *testing.B) {
	mc := NewMetricsCollector(1 << 16)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-mc.ch:
			case <-done:
				return
			}
		}
	}()

	result := &protocol.Result{
		ActionName: "checkout",
		Group:      "shoppers",
		StartTime:  time.Now(),
		Duration:   5 * time.Millisecond,
		Success:    true,
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mc.Record(result)
		}
	})
	b.StopTimer()
	close(done)
}

// TestHarnessOverheadUnderTarget is a guardrail that fails if the metrics hot
// path regresses past a generous fraction of the <50µs per-transaction target.
// It runs the benchmark inline and asserts ns/op stays well under budget, so a
// future change that adds a lock or allocation to Record is caught in CI.
func TestHarnessOverheadUnderTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf guardrail in -short mode")
	}
	if raceDetectorEnabled {
		t.Skip("skipping wall-clock performance guardrail under the race detector")
	}
	res := testing.Benchmark(BenchmarkHarnessOverheadPerTransaction)
	nsPerOp := float64(res.NsPerOp())
	// Budget: the metrics hot path is only one component of the per-transaction
	// overhead, so hold it to a small fraction of the 50,000ns (50µs) target.
	const budgetNs = 5000.0
	if nsPerOp > budgetNs {
		t.Fatalf("harness metrics overhead %.0f ns/op exceeds budget %.0f ns/op", nsPerOp, budgetNs)
	}
}
