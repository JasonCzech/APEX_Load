package generatorrpc

import (
	"testing"
	"time"
)

// TestWindowSecondsFromMs covers infra-misc-1: the <=0 -> 1.0s fallback (which
// preserves legacy count-as-rate behavior) and the normal ms->s conversion that
// the archive TPS divisor depends on.
func TestWindowSecondsFromMs(t *testing.T) {
	cases := []struct {
		ms   int64
		want float64
	}{
		{0, 1.0},    // no window reported -> treat the batch count as a per-second rate
		{-5, 1.0},   // negative is invalid -> same fallback
		{1000, 1.0}, // exactly one second
		{500, 0.5},  // sub-second window
		{2000, 2.0}, // multi-second window
		{1500, 1.5}, // fractional
	}
	for _, c := range cases {
		if got := windowSecondsFromMs(c.ms); got != c.want {
			t.Errorf("windowSecondsFromMs(%d) = %v, want %v", c.ms, got, c.want)
		}
	}
}

// TestTransactionBufferDrainWindow covers infra-misc-1: DrainWindow measures the
// window from the prior drain, still advances lastDrainAt when the buffer is
// empty, and drains+clears buffered transactions.
func TestTransactionBufferDrainWindow(t *testing.T) {
	b := &transactionBuffer{}

	// First drain on a zero lastDrainAt reports a zero window but must advance
	// lastDrainAt so the NEXT window is measured.
	out, window := b.DrainWindow()
	if out != nil {
		t.Fatalf("empty first drain returned %d transactions, want nil", len(out))
	}
	if window != 0 {
		t.Fatalf("first drain (zero lastDrainAt) window = %v, want 0", window)
	}
	if b.lastDrainAt.IsZero() {
		t.Fatal("DrainWindow must advance lastDrainAt even when the buffer is empty")
	}

	// A subsequent drain measures the elapsed window from the prior drain.
	b.lastDrainAt = time.Now().Add(-120 * time.Millisecond)
	if _, window = b.DrainWindow(); window < 100*time.Millisecond {
		t.Fatalf("subsequent drain window = %v, want >= ~120ms (measured from lastDrainAt)", window)
	}

	// Buffered transactions are returned and the buffer is cleared.
	b.transactions = []TransactionMetric{{Name: "a"}, {Name: "b"}}
	out, _ = b.DrainWindow()
	if len(out) != 2 {
		t.Fatalf("drain returned %d transactions, want 2", len(out))
	}
	if len(b.transactions) != 0 {
		t.Fatalf("drain must clear the buffer, got %d remaining", len(b.transactions))
	}
}

// TestEffectiveBatchWindowMsAdditive verifies the multi-group window correction:
// summing cross-group success and dividing by the max window mis-weights TPS when
// group windows differ; the effective window makes totalSuccess/window equal the
// additive sum-of-(groupSuccess/groupWindow) rate.
func TestEffectiveBatchWindowMsAdditive(t *testing.T) {
	successN := func(n int) []TransactionMetric {
		out := make([]TransactionMetric, n)
		for i := range out {
			out[i] = TransactionMetric{Success: true}
		}
		return out
	}
	// group a: 100 tx / 1000ms = 100 TPS; group b: 200 tx / 2000ms = 100 TPS.
	// Additive = 200 TPS. Naive (300 success / 2000ms max) = 150 TPS (wrong).
	a := assignedTest{
		groupTransactions: map[string][]TransactionMetric{"a": successN(100), "b": successN(200)},
		groupTelemetry:    map[string]MetricsTelemetry{"a": {WindowMs: 1000}, "b": {WindowMs: 2000}},
	}
	windowMs := effectiveBatchWindowMs(a, 2000)
	tps := float64(300) / windowSecondsFromMs(windowMs)
	if tps < 199.0 || tps > 201.0 {
		t.Fatalf("effective TPS = %.2f (window=%dms), want ~200 (additive), not 150 (naive max-window)", tps, windowMs)
	}

	// Steady state (equal windows) must be unchanged: effective == that window.
	steady := assignedTest{
		groupTransactions: map[string][]TransactionMetric{"a": successN(6), "b": successN(4)},
		groupTelemetry:    map[string]MetricsTelemetry{"a": {WindowMs: 1000}, "b": {WindowMs: 1000}},
	}
	if got := effectiveBatchWindowMs(steady, 1000); got != 1000 {
		t.Fatalf("steady-state effective window = %d, want 1000 (unchanged)", got)
	}

	// No successes -> fall back to the provided window.
	if got := effectiveBatchWindowMs(assignedTest{groupTransactions: map[string][]TransactionMetric{}}, 1234); got != 1234 {
		t.Fatalf("no-success effective window = %d, want fallback 1234", got)
	}
}

// TestMergeMetricsTelemetryWindowMsMax covers infra-misc-1: the per-group window
// merge takes the max across groups (groups on a pod publish on one cadence, so
// the max is the representative batch window).
func TestMergeMetricsTelemetryWindowMsMax(t *testing.T) {
	combined := mergeMetricsTelemetry(map[string]MetricsTelemetry{
		"group-a": {WindowMs: 1000, SampleAgeMs: 30},
		"group-b": {WindowMs: 2500, SampleAgeMs: 80},
		"group-c": {WindowMs: 500, SampleAgeMs: 10},
	})
	if combined == nil {
		t.Fatal("expected non-nil combined telemetry")
	}
	if combined.WindowMs != 2500 {
		t.Fatalf("WindowMs = %d, want 2500 (max across groups)", combined.WindowMs)
	}
	if combined.SampleAgeMs != 80 {
		t.Fatalf("SampleAgeMs = %v, want 80 (max across groups)", combined.SampleAgeMs)
	}
}
