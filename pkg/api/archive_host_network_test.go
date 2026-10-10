package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/hostnet"
)

func hostNetworkSample(scope string, rcv, snd, in uint64) *hostnet.UDPDropCounters {
	return &hostnet.UDPDropCounters{Scope: scope, ScopeID: "net:[7]", ReceiveBufferErrors: rcv, SendBufferErrors: snd, InErrors: in, SampledAt: time.Unix(1, 0)}
}

func TestDistributedArchiveNamesEachGeneratorsHostNetworkEvidence(t *testing.T) {
	// A generator's manifest carries its evidence without a source.
	generatorManifest := []byte(`{"test_id":"run","host_network":[{"open_files":{"available":true,"soft":1048576},"udp_buffers":{"available":true,"receive_max_bytes":212992,"send_max_bytes":212992},"udp_drops_at_start":{"scope":"pod","scope_id":"net:[7]","receive_buffer_errors":1,"send_buffer_errors":0,"in_errors":2,"sampled_at":"2026-10-10T10:00:00Z"},"udp_drops_at_end":{"scope":"pod","scope_id":"net:[7]","receive_buffer_errors":41,"send_buffer_errors":0,"in_errors":2,"sampled_at":"2026-10-10T10:05:00Z"}}]}`)
	var decoded RunArchiveManifest
	if err := json.Unmarshal(generatorManifest, &decoded); err != nil {
		t.Fatal(err)
	}
	test := &ManagedTest{ID: "run", CreatedAt: time.Unix(1, 0)}
	bundles := []remoteArchiveBundle{{target: "gen-0:9090", manifest: decoded}, {target: "gen-1:9090", manifest: RunArchiveManifest{}}}
	manifest := mergeRemoteArchiveManifest(nil, test, bundles, "COMPLETE", "", test.CreatedAt)
	if len(manifest.HostNetwork) != 1 || manifest.HostNetwork[0].Source != "gen-0:9090" {
		t.Fatalf("merged host network = %+v", manifest.HostNetwork)
	}
	report := buildRunArchiveHostNetwork(manifest.HostNetwork)
	if report[0].Delta == nil || report[0].Delta.ReceiveBufferErrors != 40 || !strings.Contains(report[0].Warning, "gen-0:9090") {
		t.Fatalf("report evidence = %+v", report[0])
	}
}

func TestHostNetworkEvidenceRendersAndWarnsOnlyForOwnScopeDrops(t *testing.T) {
	evidence := buildRunArchiveHostNetwork([]hostnet.RunEvidence{
		{Source: "gen-0", OpenFiles: hostnet.OpenFileLimit{Available: true, Soft: 65536}, UDPBuffers: hostnet.UDPBufferLimits{Available: true, ReceiveMaxBytes: 212992, SendMaxBytes: 7340032},
			DropsAtStart: hostNetworkSample(hostnet.ScopePod, 0, 0, 0), DropsAtEnd: hostNetworkSample(hostnet.ScopePod, 12, 0, 3)},
		{Source: "controller", DropsAtStart: hostNetworkSample(hostnet.ScopeNetworkNamespace, 5, 0, 0), DropsAtEnd: hostNetworkSample(hostnet.ScopeNetworkNamespace, 50, 0, 0)},
		{Source: "gen-1"},
	})
	if evidence[0].Warning == "" || evidence[1].Warning != "" || evidence[1].Delta == nil || evidence[2].Delta != nil {
		t.Fatalf("evidence = %+v", evidence)
	}
	rows := reportHostNetworkRows(evidence)
	if rows[0].Buffers != "212992 / 7340032 bytes (below the 7 MB QUIC requests)" || rows[0].Note == "" ||
		!strings.Contains(rows[1].Note, "shared network namespace") || rows[2].OpenFiles != "Unavailable" || rows[2].Drops != "Unavailable" {
		t.Fatalf("rows = %+v", rows)
	}

	report := consistencyMatrixReport()
	report.HostNetwork = evidence
	data := buildRunArchiveReportRenderData(report, buildRunArchiveReportProfiles()[0], nil, consistencyMatrixManifest())
	renderer := reportSectionRegistry["network_phases"]
	html, err := renderer.HTML(&data)
	if err != nil || !strings.Contains(string(html), "Load generator UDP") || !strings.Contains(string(html), "receive buffer 12, send buffer 0, other input errors 3") {
		t.Fatalf("HTML = %v\n%s", err, html)
	}
	var lines []string
	renderer.PDF(&data, &lines)
	if !strings.Contains(strings.Join(lines, "\n"), "- gen-0: open files 65536") {
		t.Fatalf("PDF = %v", lines)
	}
	csv := map[string]string{}
	if err := renderer.CSV(&data, func(_, key, metric, value string) error { csv[key+"|"+metric] = value; return nil }); err != nil {
		t.Fatal(err)
	}
	if csv["host_network:gen-0|open_files"] != "65536" || csv["host_network:controller|note"] == "" {
		t.Fatalf("CSV = %v", csv)
	}
	var finding *ReportFinding
	for _, candidate := range deriveReportFindings(report) {
		if candidate.Title == "UDP drops on load generators" {
			finding = &candidate
		}
	}
	if finding == nil || !strings.Contains(finding.Observed, "gen-0") || strings.Contains(finding.Observed, "controller") {
		t.Fatalf("finding = %+v", finding)
	}
	// An archive from before the evidence renders without the table.
	report.HostNetwork = nil
	data = buildRunArchiveReportRenderData(report, buildRunArchiveReportProfiles()[0], nil, consistencyMatrixManifest())
	if legacy, err := renderer.HTML(&data); err != nil || strings.Contains(string(legacy), "Load generator UDP") {
		t.Fatalf("legacy HTML = %v", err)
	}
}
