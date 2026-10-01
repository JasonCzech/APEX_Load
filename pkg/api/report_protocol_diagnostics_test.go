package api

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
)

func TestProtocolReportsRenderMeasuredEvidenceAndEscapeIdentities(t *testing.T) {
	status := int32(4)
	budget := 100.0
	encoded := .1
	var g *protocol.GRPCSummary
	protocol.MergeGRPCDiagnostic(&g, &protocol.GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 95, DeadlineBudgetMs: &budget, EncodeMs: &encoded, StatusCode: &status})
	a := RunArchiveActionStats{GRPC: g, DiagnosticFindings: protocol.ProtocolDiagnosticFindings(g, nil)}
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{}, ByAction: map[string]RunArchiveActionStats{"<script>alert(1)</script>": a}}
	d := &runArchiveHTMLReportData{Report: report}
	section := protocolDiagnosticReportSection()
	body, err := section.HTML(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "<script>") || !strings.Contains(string(body), "protobuf encoding") || !strings.Contains(string(body), "advisory") {
		t.Fatalf("unsafe/incomplete HTML: %s", body)
	}
	var lines []string
	section.PDF(d, &lines)
	if !strings.Contains(strings.Join(lines, "\n"), "gRPC client call") {
		t.Fatal("PDF omitted diagnostics")
	}
	var csv []string
	if err = section.CSV(d, func(_, _, _, value string) error { csv = append(csv, value); return nil }); err != nil || len(csv) < 3 {
		t.Fatal("CSV omitted diagnostics", err)
	}
	root := buildRunRootCauseSummary("r", report)
	found := false
	for _, finding := range root.TopFindings {
		if finding.Category == "protocol" && strings.HasPrefix(finding.Title, "Advisory:") {
			found = true
		}
	}
	if !found {
		t.Fatal("root-cause response omitted advisory protocol evidence")
	}
}
