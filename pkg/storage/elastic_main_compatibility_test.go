package storage

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

func TestElasticPreparationConfigurationExcludesMutableHealth(t *testing.T) {
	a := &PreparationMonitorSnapshot{RulesFrozen: true, ExpandedElasticConnections: map[string]*ExpandedElasticConnectionSnapshot{"s": {ConnectionID: "connection", Revision: 7, Config: elastic.Config{Endpoint: "https://search.example.com", Product: "elasticsearch", AuthType: "basic"}, APIKeySecretName: "KEY"}}}
	raw, _ := json.Marshal(a)
	var b PreparationMonitorSnapshot
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	b.Warnings = []string{"source unavailable"}
	b.ExpandedElasticConnections["s"].Capabilities = elastic.Capabilities{DSL: true, Status: map[string]string{"search": "available"}}
	if !samePreparationMonitorConfiguration(a, &b) {
		t.Fatal("verification health and warning update changed execution configuration")
	}
	freezeVerifiedDynatracePreparationCapabilities(a, &b)
	if !a.ExpandedElasticConnections["s"].Capabilities.DSL {
		t.Fatal("verified capabilities not frozen")
	}
	b.ExpandedElasticConnections["s"].Capabilities.Status["search"] = "forbidden"
	if a.ExpandedElasticConnections["s"].Capabilities.Status["search"] != "available" {
		t.Fatal("capability status aliases mutable current snapshot")
	}
	for _, mutate := range []func(*ExpandedElasticConnectionSnapshot){func(s *ExpandedElasticConnectionSnapshot) { s.Revision++ }, func(s *ExpandedElasticConnectionSnapshot) { s.ConnectionID = "other" }, func(s *ExpandedElasticConnectionSnapshot) { s.Config.Endpoint = "https://other.example.com" }, func(s *ExpandedElasticConnectionSnapshot) { s.APIKeySecretName = "OTHER_KEY" }} {
		var changed PreparationMonitorSnapshot
		if err := json.Unmarshal(raw, &changed); err != nil {
			t.Fatal(err)
		}
		mutate(changed.ExpandedElasticConnections["s"])
		if samePreparationMonitorConfiguration(a, &changed) {
			t.Fatal("identity/configuration mutation ignored")
		}
	}
}

func TestElasticRecordIdentityPreservesPublishedElasticQueryReceipts(t *testing.T) {
	at := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	point := analysis.MetricPoint{Source: "legacy-source", MetricName: "logs.count", Unit: "count", Dimensions: map[string]string{"service": "orders"}, Provider: &analysis.ProviderMeasurement{Name: "elasticsearch", MetricID: "logs.count", Mode: "interval_bucket", BucketStart: at, BucketEnd: at.Add(time.Minute), ResolutionSeconds: 60, Statistic: "count"}}
	// These keys are from the published elastic-query receipt contract. A
	// change makes previously retained heads fail artifact integrity checks.
	const oldPointKey = "5fcb525b3191267dceb27062bf87fff0ed3bb2709fbcb0899fdb9ad37efd2aee"
	if got := ObservationKey(point); got != oldPointKey {
		t.Fatalf("published metric key changed: %s", got)
	}
	revisedValue := float64(12)
	point.Value = &revisedValue
	if got := ObservationKey(point); got != oldPointKey {
		t.Fatal("value revision changed canonical metric identity")
	}
	point.Provider.QueryID, point.Provider.WindowKind = "query-one", "native_bucket"
	expanded := ObservationKey(point)
	if expanded == oldPointKey {
		t.Fatal("expanded query identity reused legacy head")
	}
	point.Provider.QueryID = "query-two"
	if ObservationKey(point) == expanded {
		t.Fatal("distinct expanded queries collapsed")
	}
	diagnostic := monitorprovider.Diagnostic{Provider: "elasticsearch", Kind: "log", ApplicationID: "orders", ID: "document-one"}
	const oldDiagnosticKey = "77f3edcd9da54accfb9c1a69ededc8730a89e848733da4ff6855b3d687fffb8c"
	if got := DiagnosticKeyForJob("legacy-job", diagnostic); got != oldDiagnosticKey {
		t.Fatalf("published diagnostic key changed: %s", got)
	}
	diagnostic.QueryID, diagnostic.Index, diagnostic.DocumentID = "logs", "logs-a", diagnostic.ID
	expandedDiagnostic := DiagnosticKeyForJob("legacy-job", diagnostic)
	diagnostic.Index = "logs-b"
	if expandedDiagnostic == oldDiagnosticKey || DiagnosticKeyForJob("legacy-job", diagnostic) == expandedDiagnostic {
		t.Fatal("expanded evidence lost query/index identity")
	}
}

func TestElasticPrivateSnapshotPreservesExistingProviderState(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	original := RunMonitorJob{Source: MonitorSource{Pack: "elastic"}, FrozenMonitorRules: []analysis.MonitorRule{{SourceID: "rule"}}, RulesFrozen: true, QueryTime: &now, ProviderCursor: "opaque", RunTraceIDs: []string{"trace"}, ScanConsumedBytes: 123, ScanReservedBytes: 456, ConnectionSnapshot: &AppDynamicsConnectionSnapshot{ConnectionID: "appd"}, PrometheusConnectionSnapshot: &PrometheusConnectionSnapshot{ConnectionID: "prom"}, DynatraceSnapshot: &DynatraceConnectionSnapshot{ConnectionID: "dyna", Capabilities: dynatrace.Capabilities{}}, QueryCoverage: map[string]monitorprovider.Coverage{"q": {Status: "complete", ExpectedPoints: 4, MissingPoints: 1}}, ElasticConnectionSnapshot: &ExpandedElasticConnectionSnapshot{ConnectionID: "elastic"}, ElasticContinuation: &analysis.ProviderContinuation{PITID: "private", QueryID: "q", WindowFrom: now, WindowUntil: now.Add(time.Minute)}, ElasticResolvedIndices: []string{"logs-0001"}, ElasticPendingIngestionWatermark: now, TruncatedQueries: map[string]bool{"q": true}, RetainedEvidenceBytes: 99, AcceptedArtifactBytes: 199, StableSinceAt: &now}
	original.ElasticSnapshot = &ElasticConnectionSnapshot{ConnectionID: "legacy-elastic", Endpoint: "https://legacy.example", IndexPattern: "logs-*", TimestampField: "@timestamp", TraceIDField: "trace.id", BaseFilter: `{"term":{"service.name":"orders"}}`, AuthType: ELKAuthBasic, UsernameSecretName: "USER", PasswordSecretName: "PASSWORD", UpdatedAt: now}
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored RunMonitorJob
	if err = json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, restored) {
		t.Fatalf("private job state lost\noriginal: %#v\nrestored: %#v", original, restored)
	}
	prepared := PreparationMonitorSnapshot{RulesFrozen: true, MonitorRules: original.FrozenMonitorRules, Warnings: []string{"warning"}, Connections: map[string]*AppDynamicsConnectionSnapshot{"appd": original.ConnectionSnapshot}, PrometheusConnections: map[string]*PrometheusConnectionSnapshot{"prom": original.PrometheusConnectionSnapshot}, DynatraceConnections: map[string]*DynatraceConnectionSnapshot{"dyna": original.DynatraceSnapshot}, ExpandedElasticConnections: map[string]*ExpandedElasticConnectionSnapshot{"elastic": original.ElasticConnectionSnapshot}}
	prepared.ElasticConnections = map[string]*ElasticConnectionSnapshot{"legacy-elastic": original.ElasticSnapshot}
	body, err = json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var restoredPrep PreparationMonitorSnapshot
	if err = json.Unmarshal(body, &restoredPrep); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared, restoredPrep) {
		t.Fatal("private preparation state lost")
	}
}
