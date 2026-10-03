package engine

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"unsafe"
)

func TestMessagingV2SnapshotRetainedByteEstimateIncludesHistograms(t *testing.T) {
	var summary *protocol.MessagingSummary
	value := 2.0
	raw := &protocol.MessagingDiagnostics{Version: 2}
	for _, metric := range []string{"producer_ack", "settlement", "checkpoint", "local_admission", "action", "enqueue_age", "end_to_end"} {
		raw.Durations = append(raw.Durations, protocol.MessagingDurationObservation{Metric: metric, ValueMs: &value})
	}
	protocol.MergeMessagingTelemetry(&summary, &protocol.ServerTelemetry{MessagesSent: 1, Messaging: raw}, "topic", "kafka")
	snap := MetricsSnapshot{ByAction: map[string]*ActionMetrics{"send": {ServerTelemetry: &ServerTelemetrySummary{Messaging: summary}}}}
	baseline := summary.Clone()
	baseline.Dimensions[0].Evidence = nil
	plain := MetricsSnapshot{ByAction: map[string]*ActionMetrics{"send": {ServerTelemetry: &ServerTelemetrySummary{Messaging: baseline}}}}
	added := estimateMetricsSnapshotBytes(snap) - estimateMetricsSnapshotBytes(plain)
	minimum := len(summary.Dimensions[0].Evidence.Durations) * int(unsafe.Sizeof(protocol.MessagingDurationSummary{}))
	if added < minimum {
		t.Fatalf("histogram allocation omitted: added %d minimum %d", added, minimum)
	}
	// A collection with maximal dimensions must account for every independent allocation.
	for i := 1; i < protocol.MaxMessagingDimensions; i++ {
		d := summary.Dimensions[0]
		d.Evidence = d.Evidence.Clone()
		summary.Dimensions = append(summary.Dimensions, d)
	}
	if estimateMetricsSnapshotBytes(snap) < minimum*protocol.MaxMessagingDimensions {
		t.Fatal("dimension histogram population bypasses history byte budget")
	}
}

func TestMessagingV2ResultBoundDetachesAndRejectsMalformedEvidence(t *testing.T) {
	value := 0.0
	input := &protocol.MessagingDiagnostics{Version: 2, Durations: []protocol.MessagingDurationObservation{{Metric: "producer_ack", ValueMs: &value}}}
	r := &protocol.Result{ServerTelemetry: &protocol.ServerTelemetry{Messaging: input}}
	boundResultEvidence(r)
	*r.ServerTelemetry.Messaging.Durations[0].ValueMs = 7
	if *input.Durations[0].ValueMs != 0 {
		t.Fatal("async result retains producer pointer")
	}
	r.ServerTelemetry.Messaging = &protocol.MessagingDiagnostics{Version: 2, Durations: make([]protocol.MessagingDurationObservation, protocol.MaxMessagingDurationMetrics+1)}
	boundResultEvidence(r)
	if r.ServerTelemetry.Messaging == nil || r.ServerTelemetry.Messaging.Valid() || len(r.ServerTelemetry.Messaging.Durations) != 0 {
		t.Fatal("malformed raw evidence unbounded or missing qualifier")
	}
}
