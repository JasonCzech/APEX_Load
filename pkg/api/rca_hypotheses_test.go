package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRCAHypothesisFromConnectedProviderEvidence(t *testing.T) {
	server, store := baselineTestServer(t)
	// Cart requests reach api.shop.local and slow from 200 s.
	record := exemplarArchiveFixture(t, server, store, "rca-run", func(i int, tx *TransactionEvent) {
		tx.DependencyTarget = "api.shop.local"
		tx.DurationMs = 100 + float64(i%7)
		if i >= 200 {
			tx.DurationMs = 400 + float64(i%7)
		}
	})
	// The service's CPU rose from 150 s.
	var points []analysis.MetricPoint
	for i := 0; i < 36; i++ {
		value := 30 + float64(i%3)
		if i >= 15 {
			value = 90 + float64(i%3)
		}
		points = append(points, analysis.MetricPoint{Source: "dt-src", MetricName: "builtin:service.cpu.time", Kind: analysis.KindGauge, Unit: "%", Quality: analysis.QualityValid,
			TimestampUTC: exemplarFixtureStart.Add(time.Duration(i) * 10 * time.Second), Value: &value,
			Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderDynatrace, ResolutionSeconds: 10, Statistic: "avg", EntityIDs: map[string]string{"dt.entity.service": "SERVICE-1"}}})
	}
	writeArchiveSidecar(t, record, "monitor_points.jsonl", points, func(m *RunArchiveManifest) {
		m.MonitorEvidenceFile = "monitor_points.jsonl"
		m.MonitorCoverage = []RunMonitorCoverage{{SourceID: "dt-src", Dynatrace: &dynatrace.Selection{}, Topology: []appdynamics.Entity{{ID: "SERVICE-1", Name: "api.shop.local", Type: "SERVICE"}}}}
	})
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)

	first := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	pressure := rcaHypothesis(first, "resource_pressure")
	if first.Outcome != rca.OutcomeSupported || pressure == nil || pressure.Confidence != rca.ConfidenceLow || first.Inputs.SignatureSet != signatures.Version {
		t.Fatalf("unmapped: %s %+v", first.Outcome, first.Hypotheses)
	}

	// Approving the mapping connects the CPU evidence to the symptom.
	approval := timeslotRequest(t, server, http.MethodPost, "/api/v1/analysis/rca-mappings?project_id="+storage.DefaultProjectID, map[string]any{
		"name": "cart API", "definition": map[string]any{"dependency": "http:api.shop.local", "provider": monitorprovider.ProviderDynatrace, "entity_id": "SERVICE-1", "decision": rca.MappingApproved},
	})
	if approval.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", approval.Code, approval.Body.String())
	}
	second := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	pressure = rcaHypothesis(second, "resource_pressure")
	if second.Outcome != rca.OutcomeSupported || pressure == nil || pressure.Rank != 1 || pressure.Confidence != rca.ConfidenceHigh ||
		len(pressure.Chain) != 1 || pressure.Chain[0].Relation != rca.RelationPrecedes || len(pressure.Supporting) != 1 {
		t.Fatalf("mapped: %s %+v", second.Outcome, second.Hypotheses)
	}
}

func rcaHypothesis(a *rca.Assessment, signature string) *rca.Hypothesis {
	for i := range a.Hypotheses {
		if a.Hypotheses[i].Signature == signature {
			return &a.Hypotheses[i]
		}
	}
	return nil
}

func TestNetworkPhaseSeriesFromArchiveTiming(t *testing.T) {
	var summaries []IntervalSummaryRecord
	for i := 1; i <= 3; i++ {
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: exemplarFixtureStart.Add(time.Duration(i) * 10 * time.Second), SampleWindowSeconds: 10})
	}
	collector := newNetworkPhaseCollector(summaries, "transactions.jsonl")
	for _, tx := range []TransactionEvent{
		{Timestamp: exemplarFixtureStart.Add(5 * time.Second), DependencyType: "http", DependencyTarget: "api", DNSMs: 4, ConnectMs: 10},
		{Timestamp: exemplarFixtureStart.Add(6 * time.Second), DependencyType: "http", DependencyTarget: "api", DNSMs: 6},
		// A reused connection spends nothing on these phases.
		{Timestamp: exemplarFixtureStart.Add(15 * time.Second), DependencyType: "http", DependencyTarget: "api"},
		{Timestamp: exemplarFixtureStart.Add(25 * time.Second), DependencyType: "http", DependencyTarget: "api", DNSMs: 30},
	} {
		collector.add(&tx)
	}
	series := collector.series()
	if len(series) != 2 || series[0].ID != "dependency/http:api/dns_ms" || series[0].Measures != signatures.KindDNS || series[1].Measures != signatures.KindConnect {
		t.Fatalf("%+v", series)
	}
	dns := series[0].Points
	if len(dns) != 3 || !dns[0].Valid || dns[0].Value != 5 || dns[1].Valid || dns[2].Value != 30 || series[0].Bucket != 10*time.Second ||
		series[0].Nodes[0] != rca.DependencyNodeID("http:api") {
		t.Fatalf("%+v", series[0])
	}
}

func TestProviderSeriesMeasureAndNodes(t *testing.T) {
	point := analysis.MetricPoint{Source: "prom", MetricName: "jvm_gc_seconds_rate", Kind: analysis.KindGauge, Dimensions: map[string]string{"pod": "cart-1", "instance": "10.0.0.9:9100"},
		Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderPrometheus, Mode: "evaluation_point", EvaluationStepSeconds: 15}}
	s, ok := monitorSeriesTemplate(point, monitorSeriesIdentity(point), "monitor_points.jsonl")
	if !ok || s.Measures != signatures.KindGC || len(s.Nodes) != 2 || s.Nodes[0] != rca.InfrastructureNodeID("host", "10.0.0.9") || s.Nodes[1] != rca.InfrastructureNodeID("pod", "cart-1") {
		t.Fatalf("%+v", s)
	}
}
