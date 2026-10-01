package engine

import (
	"fmt"
	"testing"
	"time"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestGRPCDiagnosticsSurviveMetricsProjectionAndIndependentAggregation(t *testing.T) {
	first := 0.0
	input := &protocol.Result{ServerTelemetry: &protocol.ServerTelemetry{GRPC: &protocol.GRPCDiagnostics{
		Method: "/example.Service/Call", Mode: "unary", CallMs: 5, FirstMessageMs: &first, MessagesReceived: 1,
	}}}
	projected := compactMetricsResult(input)
	*input.ServerTelemetry.GRPC.FirstMessageMs = 99
	if *projected.ServerTelemetry.GRPC.FirstMessageMs != 0 {
		t.Fatal("metric projection shares optional diagnostic pointers")
	}
	summary := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(summary, projected.ServerTelemetry)
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{GRPC: &protocol.GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 20}})
	finalizeServerTelemetrySummary(summary)
	if summary.GRPC == nil || summary.GRPC.Samples != 2 || summary.GRPC.Call.Sum != 25 || summary.GRPC.FirstMessage.Count != 1 || summary.GRPC.FirstMessage.Sum != 0 {
		t.Fatalf("summary=%+v", summary.GRPC)
	}
}

func TestGRPCHistogramsParticipateInSnapshotRetention(t *testing.T) {
	collector := NewMetricsCollector(1)
	collector.resCollector = nil
	results := make([]*protocol.Result, maxMetricDimensionEntries)
	for i := range results {
		results[i] = &protocol.Result{
			ActionName: fmt.Sprintf("rpc-%d", i), Success: true, Duration: time.Millisecond,
			ServerTelemetry: &protocol.ServerTelemetry{GRPC: &protocol.GRPCDiagnostics{
				Method: "/example.Service/Call", Mode: "unary", CallMs: 1,
			}},
		}
	}
	const published = 48
	for i := 0; i < published; i++ {
		results[0].ServerTelemetry.GRPC.CallMs = float64(i + 1)
		collector.publishSnapshot(results, time.Second)
	}
	// Count the actual inline diagnostic allocations retained across both
	// transaction and action projections, independently of the byte estimator.
	retainedBytes := 0
	for _, snapshot := range collector.snapshots {
		for _, values := range []map[string]*ActionMetrics{snapshot.ByAction, snapshot.ByTransaction} {
			for _, metrics := range values {
				if metrics.ServerTelemetry != nil && metrics.ServerTelemetry.GRPC != nil {
					retainedBytes += int(unsafe.Sizeof(*metrics.ServerTelemetry.GRPC))
				}
			}
		}
	}
	if len(collector.snapshots) == 0 || len(collector.snapshots) >= published || retainedBytes > maxMetricsSnapshotBytes {
		t.Fatalf("gRPC history retained %d/%d snapshots and %d diagnostic bytes (budget %d)", len(collector.snapshots), published, retainedBytes, maxMetricsSnapshotBytes)
	}
	latest := collector.snapshots[len(collector.snapshots)-1]
	if call := latest.ByAction["rpc-0"].ServerTelemetry.GRPC.Call; call.Count != 1 || call.Sum != published {
		t.Fatal("retention lost the newest diagnostic evidence")
	}
}

func TestGRPCMissingResponseDoesNotDiluteLegacyFirstEventAverage(t *testing.T) {
	first := 5.0
	d := &protocol.GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 10, FirstMessageMs: &first, MessagesSent: 1, MessagesReceived: 1}
	s := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(s, &protocol.ServerTelemetry{GRPC: d, FirstEventMs: first, MessagesSent: 1, MessagesReceived: 1})
	d = d.Clone()
	d.FirstMessageMs, d.MessagesReceived = nil, 0
	mergeServerTelemetrySummary(s, &protocol.ServerTelemetry{GRPC: d, MessagesSent: 1})
	finalizeServerTelemetrySummary(s)
	if s.Samples != 2 || s.FirstEventSamples != 1 || s.AvgFirstEventMs != first {
		t.Fatalf("missing response diluted average: %+v", s)
	}
}
