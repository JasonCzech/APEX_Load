package analysis_test

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/analysis"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

func TestPublicationSharedProgressPreservesBothContinuationContracts(t *testing.T) {
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	progress := analysis.ProviderQueryProgress{NextQuery: 1, TotalQueries: 3, NextTime: &at,
		Cursor: "async-request", CursorExpiresAt: &at, ScannedBytes: 10,
		ScanReservationBytes: 20, ScanReservationIDs: []string{"attempt"},
		ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: "attempt", ScannedBytes: 10}}, QueryCostKnown: true,
		Continuation: &analysis.ProviderContinuation{QueryID: "elastic", WindowFrom: at, WindowUntil: at.Add(time.Minute), PITID: "private-pit"},
	}
	body, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	var restored monitorprovider.QueryProgress
	if err = json.Unmarshal(body, &restored); err != nil || !reflect.DeepEqual(progress, restored) {
		t.Fatalf("provider continuation lost: %+v %v", restored, err)
	}
}

func TestPublicationRuleProviderDoesNotCrossEvaluate(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	value := 1.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "s", MetricName: "cpu", Kind: analysis.KindGauge, Value: &value, Unit: "percent", Quality: analysis.QualityValid, TimestampUTC: start,
		Provider: &analysis.ProviderMeasurement{Name: "prometheus", MetricID: "cpu", QueryID: "cpu", DefinitionHash: strings.Repeat("a", 64), BackendProfile: "prometheus", Mode: "evaluation_point", EvaluationStepSeconds: 15, Settled: true, AggregationEligible: true, Statistic: "value"}}
	if err := point.Validate(); err != nil {
		t.Fatal(err)
	}
	rule := analysis.MonitorRule{Provider: "dynatrace", SourceID: point.Source, Metric: point.MetricName,
		Unit: point.Unit, Statistic: "avg", Comparator: "gte", Threshold: 0, MinCoveragePct: 1}
	if got := analysis.EvaluateMonitorRule(rule, []analysis.MetricPoint{point}, start, start); got.Status != "not_evaluated" {
		t.Fatal(got)
	}
	if got := analysis.EvaluateMonitorRule(rule, []analysis.MetricPoint{point}, start, start.Add(15*time.Second)); got.Status != "not_evaluated" {
		t.Fatal("Dynatrace selector evaluated Prometheus evidence", got)
	}
}

func TestPublicationSharedCoverageAndEvidenceRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	coverage := analysis.ProviderCoverage{Status: "partial", From: at, To: at.Add(time.Minute), Points: 2, Evidence: 1, ExpectedPoints: 4, MissingPoints: 2, Warnings: []string{"upstream-warning"}, Infos: []string{"upstream-info"}, Cached: true, CacheAgeSeconds: 15, ObservedAt: &at, Freshness: "stale", QueryID: "elastic", AsOf: &at, Reason: "page_budget"}
	body, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	var restored monitorprovider.Coverage
	if err = json.Unmarshal(body, &restored); err != nil || !reflect.DeepEqual(coverage, restored) {
		t.Fatalf("coverage lost: %+v %v", restored, err)
	}
	diagnostic := analysis.ProviderEvidence{Provider: "dynatrace", Backend: "grail", ID: "event", Kind: "event", ApplicationID: "entity", TimestampUTC: at, Summary: "safe", Properties: map[string]string{"status": "ok"}}
	if err = diagnostic.Validate(); err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	var evidence monitorprovider.Diagnostic
	if err = json.Unmarshal(body, &evidence); err != nil || !reflect.DeepEqual(diagnostic, evidence) {
		t.Fatalf("diagnostic lost: %+v %v", evidence, err)
	}
	// Alias assignment is a compile-time guarantee of one shared wire identity.
	var _ analysis.ProviderCoverage = restored
	var _ analysis.ProviderDiagnostic = evidence
	var _ monitorprovider.EvaluationWindow = analysis.ProviderEvaluationWindow{QueryID: "q", From: at, To: at.Add(time.Minute)}
	var _ monitorprovider.Entity = analysis.ProviderEntity{ID: "n", Name: "node", Type: "host"}
}

func TestPublicationLegacyElasticQueryPreservesIntervalBuckets(t *testing.T) {
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	value := 1.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "s", MetricName: "events", TimestampUTC: at, Kind: analysis.KindGauge, Value: &value, Unit: "count", Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderElasticsearch, MetricID: "events", Mode: "interval_bucket", BucketStart: at, BucketEnd: at.Add(time.Minute), ResolutionSeconds: 60, Statistic: "count"}}
	if err := p.Validate(); err != nil {
		t.Fatal("legacy lightweight query rejected", err)
	}
	p.Provider.QueryID = "expanded"
	if err := p.Validate(); err == nil {
		t.Fatal("expanded source accepted legacy mode")
	}
}
