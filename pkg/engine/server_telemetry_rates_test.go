package engine

import (
	"math"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestMessagingRatePct(t *testing.T) {
	cases := []struct {
		count, received int64
		want            float64
	}{
		{0, 0, 0},
		{5, 0, 0},   // divide-by-zero guard
		{0, 100, 0}, // no events
		{5, 100, 5},
		{1, 4, 25},
	}
	for _, c := range cases {
		if got := MessagingRatePct(c.count, c.received); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("MessagingRatePct(%d,%d) = %v, want %v", c.count, c.received, got, c.want)
		}
	}
}

func TestFinalizeDerivesReliabilityRates(t *testing.T) {
	summary := &ServerTelemetrySummary{}
	// 100 consumed messages; 5 redelivered, 2 dead-lettered, 1 duplicate.
	for i := 0; i < 100; i++ {
		mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{MessagesReceived: 1, ResponsePayloadKB: 1})
	}
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{RedeliveryCount: 5, DeadLetteredCount: 2, DuplicateCount: 1})
	finalizeServerTelemetrySummary(summary)

	if math.Abs(summary.RedeliveryRatePct-5) > 1e-9 {
		t.Fatalf("RedeliveryRatePct = %v, want 5", summary.RedeliveryRatePct)
	}
	if math.Abs(summary.DeadLetterRatePct-2) > 1e-9 {
		t.Fatalf("DeadLetterRatePct = %v, want 2", summary.DeadLetterRatePct)
	}
	if math.Abs(summary.DuplicateRatePct-1) > 1e-9 {
		t.Fatalf("DuplicateRatePct = %v, want 1", summary.DuplicateRatePct)
	}
}

func TestFinalizeRatesZeroWithoutReceives(t *testing.T) {
	summary := &ServerTelemetrySummary{}
	// Counters present but nothing received -> rates stay 0 (no divide-by-zero).
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{RedeliveryCount: 3, TransferMs: 1})
	finalizeServerTelemetrySummary(summary)
	if summary.RedeliveryRatePct != 0 {
		t.Fatalf("RedeliveryRatePct = %v, want 0 with no receives", summary.RedeliveryRatePct)
	}
}
