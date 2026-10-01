package api

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
)

func TestBaselineProtocolFindingsRetainQualificationAndIdentity(t *testing.T) {
	measure := func(ms float64, messages int64) *BaselineMeasurement {
		m := &BaselineMeasurement{Samples: 100, Successes: 100}
		for range 100 {
			protocol.MergeGRPCDiagnostic(&m.GRPC, &protocol.GRPCDiagnostics{Method: "/test.Service/Call", Mode: "unary", CallMs: ms, EncodeMs: &ms})
			protocol.MergeWebSocketDiagnostic(&m.WebSocket, &protocol.WebSocketDiagnostics{Operation: "ws_receive", ReceiveMs: &ms, MessageType: "text", MessagesReceived: messages})
		}
		return m
	}
	a, b := measure(80, 2), measure(20, 1)
	window := &BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 0, EndSeconds: 100}, Load: 10}
	row := BaselineComparisonRow{ID: "rpc", Name: "Call", Unit: "ms", Current: a, Window: window, Baselines: map[string]BaselineComparisonCell{"b": {Measurement: b, Window: window}}}
	build := func(row BaselineComparisonRow) *BaselineComparisonSnapshot {
		out := &BaselineComparisonSnapshot{Policy: defaultBaselineComparisonPolicy(), Baselines: []BaselineRunIdentity{{ID: "b"}}, Transactions: []BaselineComparisonRow{row}}
		buildBaselineFindings(out)
		return out
	}
	out := build(row)
	want := map[string]bool{"gRPC call P95": false, "gRPC protobuf encode P95": false, "WebSocket text receive-wait P95": false, "WebSocket message throughput": false}
	for _, f := range out.Findings {
		if _, ok := want[f.Metric]; ok {
			want[f.Metric] = true
			if f.Status != "changed" {
				t.Errorf("qualified finding: %+v", f)
			}
			if f.Metric == "WebSocket message throughput" && !strings.Contains(f.Message, "/s") {
				t.Errorf("wrong throughput units: %+v", f)
			}
		}
	}
	for metric, found := range want {
		if !found {
			t.Errorf("missing %s", metric)
		}
	}
	a.Samples++ // One retained action lacks diagnostics: do not score throughput.
	for _, f := range build(row).Findings {
		if f.Metric == "WebSocket message throughput" && f.Status == "changed" {
			t.Fatal("partial diagnostic coverage scored throughput")
		}
	}
	a.Samples--
	cell := row.Baselines["b"]
	cell.Qualification = []string{"Different workload"}
	row.Baselines["b"] = cell
	for _, f := range build(row).Findings {
		if f.Status == "changed" {
			t.Fatalf("unqualified evidence scored: %+v", f)
		}
	}
	b.GRPC.Method = "/test.Service/Other"
	for _, f := range build(row).Findings {
		if strings.HasPrefix(f.Metric, "gRPC") {
			t.Fatalf("mixed method comparison: %+v", f)
		}
	}
}
