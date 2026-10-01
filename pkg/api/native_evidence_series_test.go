package api

import (
	"context"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func nativeEvidenceQuery(mode string) storage.AnalyticsQuery {
	return storage.AnalyticsQuery{EvidenceMode: mode, Population: "action", Metric: "response_time", Aggregation: "p95", Dimensions: []string{"action"}, MaxSeries: 10, EndSeconds: 20, BucketSeconds: 10}
}
func TestNativeEvidenceLocalDistributedAndLiveWindows(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	live := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{Timestamp: start.Add(time.Second), Window: time.Second, ByAction: map[string]*engine.ActionMetrics{"Checkout": {Count: 2, P95: 25 * time.Millisecond}}, ByTransaction: map[string]*engine.ActionMetrics{"Business": {Count: 1, P95: 100 * time.Millisecond}}}, nil)
	result, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{live, {Timestamp: start.Add(2 * time.Second)}}, nativeEvidenceQuery("live"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 1 || *result.Series[0].Points[0].Value != 25 || result.Series[0].Points[1].Value != nil {
		t.Fatalf("population/gaps %+v", result)
	}
	copy := cloneLiveMetrics(live)
	delete(copy.NativeEvidence.Dimensions["action"], "Checkout")
	if len(live.NativeEvidence.Dimensions["action"]) != 1 {
		t.Fatal("unsafe evidence clone")
	}
	batch := generatorrpc.MetricsBatch{TimestampUnixMs: start.Add(time.Second).UnixMilli(), WindowMs: 1000, ActionMetricsVersion: 1, ActionMetrics: []generatorrpc.MetricAggregate{{Name: "Checkout", Count: 2, P95Ms: 25}}, FleetMetrics: &generatorrpc.MetricAggregate{Name: "fleet", Count: 1}, TransactionMetrics: []generatorrpc.MetricAggregate{{Name: "Business", Count: 1, P95Ms: 100}}}
	distributed := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
	if distributed.NativeEvidence == nil || distributed.NativeEvidence.Dimensions["action"]["Checkout"].P95 != 25 {
		t.Fatalf("lost distributed evidence %+v", distributed.NativeEvidence)
	}
	combined, _, _ := aggregateDistributedMetricsBatches("run", map[string]generatorrpc.MetricsBatch{"new": batch, "old": {}})
	if combined.ActionMetricsVersion != 0 || len(combined.ActionMetrics) != 0 {
		t.Fatal("mixed fleet claimed complete action evidence")
	}
}
func TestNativeEvidenceGroupedArchiveLimitsAndProperty(t *testing.T) {
	record, _ := nativeSeriesFixture(t)
	q := nativeEvidenceQuery("archive")
	q.Dimensions = []string{"property"}
	q.PropertyName = "kind"
	result, err := buildNativeGroupedArchive(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 1 || result.Series[0].Name != "kind=paid" || *result.Series[0].Points[0].Value != 30 || result.Series[0].Points[1].Value != nil {
		t.Fatalf("grouping %+v", result)
	}
	q.MaxSeries = 0
	if _, err := buildNativeGroupedArchive(context.Background(), record, q); err == nil {
		t.Fatal("expected series limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildNativeGroupedArchive(ctx, record, nativeEvidenceQuery("archive")); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestNativeEvidenceReadAuthorizationAndHTTP(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/tests/run/native-series", nil)
	access, ok := accessRequirementForRequest(req)
	if !ok || access.ServiceAccountScope != "results:read" || access.MaxBodyBytes != 16<<10 || access.ProjectLookup == nil {
		t.Fatalf("access %+v", access)
	}
	server, project := newGrafanaDashboardAPIServer(t)
	record, _ := nativeSeriesFixture(t)
	server.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives["run"] = record
	response := dashboardAPIRequest(t, server, "POST", "/api/v1/tests/run/native-series", nativeEvidenceQuery("archive"))
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var result NativeEvidenceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Series) != 1 {
		t.Fatalf("result %+v %v", result, err)
	}
}

func TestNativeEvidenceDatasetAssociationCheckedBeforeRead(t *testing.T) {
	server, project := newGrafanaDashboardAPIServer(t)
	imports := server.store.(storage.AnalysisImportStore)
	dataset, err := imports.SaveAnalysisImport(storage.AnalysisImport{ID: "dataset", ProjectID: project, Name: "external", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "f", RelativePath: "source.parquet", Size: 1}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"mapped", "normalizing", "ready"} {
		dataset.State = state
		dataset, err = imports.SaveAnalysisImport(*dataset, dataset.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	q := nativeEvidenceQuery("archive")
	q.OverlaySelection = &storage.NativeOverlaySelection{DatasetID: "dataset", Metric: "latency", Aggregation: "avg"}
	out := nativeEvidenceEnvelope(time.Now().UTC(), q)
	if err := server.attachNativeOverlay(context.Background(), project, "run", q, &out); err == nil || !strings.Contains(err.Error(), "associate") {
		t.Fatalf("association guard: %v", err)
	}
	if err := server.attachNativeOverlay(context.Background(), "foreign", "run", q, &out); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("project guard: %v", err)
	}
}
