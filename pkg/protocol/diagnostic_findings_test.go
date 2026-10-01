package protocol

import "testing"

func TestProtocolFindingsUseObservedDenominators(t *testing.T) {
	var g *GRPCSummary
	budget := 100.0
	status := int32(4)
	for range 3 {
		MergeGRPCDiagnostic(&g, &GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 90, DeadlineBudgetMs: &budget, StatusCode: &status})
	}
	MergeGRPCDiagnostic(&g, &GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 90})
	findings := ProtocolDiagnosticFindings(g, nil)
	if len(findings) != 2 || findings[0].Samples != 3 || findings[0].Observed != 3 || !findings[0].Advisory {
		t.Fatalf("findings: %+v", findings)
	}
	MergeGRPCDiagnostic(&g, &GRPCDiagnostics{Method: "/example.Service/Other", Mode: "unary", CallMs: 1})
	if !g.MixedMethods || g.Method != "" {
		t.Fatal("mixed methods have a misleading single identity")
	}
	if len(ProtocolDiagnosticFindings(nil, &WebSocketSummary{})) != 0 {
		t.Fatal("missing samples treated as anomalies")
	}
	ws := &WebSocketSummary{Samples: 5, Timeouts: 2}
	for range 5 {
		ws.WriteWait.Add(60)
	}
	findings = ProtocolDiagnosticFindings(nil, ws)
	if len(findings) != 2 {
		t.Fatalf("websocket findings: %+v", findings)
	}
}
