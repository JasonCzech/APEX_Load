package generatorrpc

import (
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
)

// Document loads cross the wire; a generator that predates them sent page-load
// averages over every sample.
func TestBrowserMetricsCarryDocumentLoads(t *testing.T) {
	sent := &BrowserMetrics{Samples: 8, DocumentLoads: 2, AvgLCPMs: 1100}
	if got := browserMetricsFromProto(browserMetricsToProto(sent)); got == nil || got.DocumentLoads != 2 || got.AvgLCPMs != 1100 {
		t.Fatalf("round trip = %+v", got)
	}
	if got := browserMetricsFromProto(&pb.BrowserMetrics{Samples: 8, AvgLcpMs: 1100}); got == nil || got.DocumentLoads != 8 {
		t.Fatalf("older generator = %+v", got)
	}
}
