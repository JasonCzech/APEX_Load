package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func nativeSeriesFixture(t *testing.T) (*storage.RunArchiveRecord, time.Time) {
	t.Helper()
	root := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{TestID: "run", CompletedAt: start.Add(20 * time.Second), TransactionsFile: "transactions.jsonl", Provenance: RunArchiveProvenance{StartedAt: &start}}
	body, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i, ms := range []float64{10, 20, 30, 1000} {
		event := TransactionEvent{Timestamp: start.Add(time.Duration(i) * time.Second), Population: "action", ActionName: "Checkout", DurationMs: ms, Success: i != 1, PropertyDimensions: map[string]string{"kind": "paid"}}
		if i == 3 {
			event.Population = "transaction"
			event.BusinessTransaction = true
		}
		if err := json.NewEncoder(f).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	return &storage.RunArchiveRecord{TestID: "run", ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}, start
}
func TestNativeArchiveSeriesRawPopulationAndGaps(t *testing.T) {
	record, _ := nativeSeriesFixture(t)
	q := storage.AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 20, PropertyName: "kind", PropertyValue: "paid", Filters: map[string]string{"action": "Checkout"}}
	series, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	if series.Buckets[0].Count != 3 || *series.Buckets[0].Value != 30 || series.Buckets[1].Value != nil {
		t.Fatalf("bad raw buckets: %+v", series)
	}
	q.Metric = "error_rate"
	q.Aggregation = "avg"
	series, err = buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil || *series.Buckets[0].Value != 100.0/3 {
		t.Fatalf("error rate: %+v %v", series, err)
	}
	q.Filters["action"] = "checkout"
	series, err = buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil || series.Buckets[0].Value != nil || len(series.Warnings) == 0 {
		t.Fatal("exact identity or missing evidence lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildNativeArchiveSeries(ctx, record, q); err == nil {
		t.Fatal("cancelled query succeeded")
	}
}
func TestNativeArchiveDrilldownHalfOpenExactIdentity(t *testing.T) {
	_, start := nativeSeriesFixture(t)
	end := start.Add(time.Second)
	f := transactionEventFilter{Population: "action", ActionExact: "Checkout", TimeFrom: &start, TimeTo: &end, TimeToExclusive: true, PropertyName: "kind", PropertyValue: "paid"}
	tx := TransactionEvent{Timestamp: start, Population: "action", ActionName: "Checkout", PropertyDimensions: map[string]string{"kind": "paid"}}
	if !transactionMatchesArchiveFilter(&tx, f) {
		t.Fatal("start excluded")
	}
	tx.Timestamp = end
	if transactionMatchesArchiveFilter(&tx, f) {
		t.Fatal("end included")
	}
	tx.Timestamp = start
	tx.ActionName = "checkout"
	if transactionMatchesArchiveFilter(&tx, f) {
		t.Fatal("case collision")
	}
}

func TestNativeArchiveSeriesHTTPComparisonScope(t *testing.T) {
	server, project := newGrafanaDashboardAPIServer(t)
	record, start := nativeSeriesFixture(t)
	server.runner.tests["run"] = &ManagedTest{ID: "run", Name: "Run", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives["run"] = record
	server.runner.tests["foreign"] = &ManagedTest{ID: "foreign", Name: "Foreign", ProjectID: "other", Status: "COMPLETE"}
	q := storage.AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 20, ComparisonRunID: "foreign"}
	response := dashboardAPIRequest(t, server, "POST", "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 404 {
		t.Fatalf("foreign comparison: %d %s", response.Code, response.Body.String())
	}
	q.ComparisonRunID = "run"
	response = dashboardAPIRequest(t, server, "POST", "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 200 {
		t.Fatalf("query: %d %s", response.Code, response.Body.String())
	}
	var result NativeArchiveResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Baseline == nil || result.Comparable || len(result.Warnings) == 0 || result.Current.Buckets[0].Count != 3 {
		t.Fatalf("compatibility/evidence: %+v", result)
	}

	baseline, _ := nativeSeriesFixture(t)
	baseline.TestID = "baseline"
	raw, err := os.ReadFile(baseline.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.TestID = "baseline"
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(baseline.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	pathTransactions := filepath.Join(baseline.ArchivePath, "transactions.jsonl")
	raw, err = os.ReadFile(pathTransactions)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathTransactions, bytes.ReplaceAll(raw, []byte("Checkout"), []byte("Search")), 0600); err != nil {
		t.Fatal(err)
	}
	server.runner.tests["baseline"] = &ManagedTest{ID: "baseline", Name: "Baseline", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives["baseline"] = baseline
	q.ComparisonRunID = "baseline"
	response = dashboardAPIRequest(t, server, "POST", "/api/v1/tests/run/archive/native-series", q)
	if response.Code != 200 {
		t.Fatalf("distinct comparison: %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Baseline == nil || result.Baseline.RunID != "baseline" || len(result.Added) != 1 || result.Added[0] != "Checkout" || len(result.Removed) != 1 || result.Removed[0] != "Search" {
		t.Fatalf("lost identity differences: %+v", result)
	}
	// Inspector uses the same exact population and half-open bucket bounds.
	path := "/api/v1/tests/run/archive/transactions?population=action&action_exact=Checkout&time_to_exclusive=true&time_from=" + start.Format(time.RFC3339) + "&time_to=" + start.Add(2*time.Second).Format(time.RFC3339)
	response = dashboardAPIRequest(t, server, "GET", path, nil)
	var details struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || details.Total != 2 {
		t.Fatalf("inspector: %d %s", response.Code, response.Body.String())
	}
}

func TestNativeArchiveQueryUsesReadAuthorization(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/tests/run/archive/native-series", nil)
	access, ok := accessRequirementForRequest(req)
	if !ok || access.ServiceAccountScope != "results:read" || access.MaxBodyBytes != 16<<10 || access.ProjectLookup == nil {
		t.Fatalf("unexpected query authorization: %+v", access)
	}
	found := false
	for _, role := range access.ProjectRoles {
		if role == RoleViewer {
			found = true
		}
	}
	if !found {
		t.Fatal("viewer query denied")
	}
}

func TestNativeArchiveSeriesMissingPropertyAndLegacyEvidence(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	f, err := os.OpenFile(filepath.Join(record.ArchivePath, "transactions.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for _, population := range []string{"action", ""} {
		if err := json.NewEncoder(f).Encode(TransactionEvent{Timestamp: start, Population: population, ActionName: "Checkout", DurationMs: 5, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	q := storage.AnalyticsQuery{Population: "action", Metric: "observation_count", Aggregation: "count", BucketSeconds: 10, EndSeconds: 20, PropertyName: "kind", PropertyValue: "paid"}
	series, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	if *series.Buckets[0].Value != 3 || len(series.Warnings) != 2 {
		t.Fatalf("missing/legacy evidence: %+v", series)
	}
}

func TestNativeArchiveSeriesRawLimitDoesNotTruncate(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	f, err := os.Create(filepath.Join(record.ArchivePath, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(f)
	for i := 0; i < 100001; i++ {
		if err := encoder.Encode(TransactionEvent{Timestamp: start, Population: "action", ActionName: "Checkout", DurationMs: 1, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	q := storage.AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 20}
	if _, err := buildNativeArchiveSeries(context.Background(), record, q); err == nil || !strings.Contains(err.Error(), "raw observation limit") {
		t.Fatalf("raw limit: %v", err)
	}
}

func TestNativeArchiveViewerCanReopenSharedPanelAndQueryOnlyOwnProject(t *testing.T) {
	server, project := newGrafanaDashboardAPIServer(t)
	store := server.store.(*storage.SQLiteStore)
	record, start := nativeSeriesFixture(t)
	if err := store.SaveTest(&storage.TestRecord{ID: "run", Name: "Run", ProjectID: project, Status: "COMPLETE", StartTime: start}); err != nil {
		t.Fatal(err)
	}
	server.runner.tests["run"] = &ManagedTest{ID: "run", Name: "Run", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives["run"] = record
	q := storage.AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 20}
	definition := &storage.DashboardDefinition{ID: "shared", ProjectID: project, Name: "Shared", Owner: "editor", Tabs: []storage.DashboardTab{{ID: "one", Title: "One", Panels: []storage.DashboardPanel{{ID: "p", Title: "P", Kind: "table", W: 24, H: 8, Query: &q}}}}}
	if err := store.SaveDashboardDefinition(definition); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, subject := range []string{"second-viewer", "outsider"} {
		if err := store.SaveUser(&storage.User{ID: subject, Subject: subject, Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: "viewer-binding", UserID: "second-viewer", ScopeType: "project", ScopeID: project, Role: RoleViewer, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	enableTrustedHeaderAuth(t, server)
	for _, tc := range []struct {
		subject, method, path string
		want                  int
	}{
		{"second-viewer", "GET", "/api/v1/monitoring/dashboard-definitions/shared", 200},
		{"second-viewer", "POST", "/api/v1/tests/run/archive/native-series", 200},
		{"second-viewer", "POST", "/api/v1/tests/run/native-series", 200},
		{"second-viewer", "POST", "/api/v1/monitoring/dashboard-definitions", 403},
		{"outsider", "POST", "/api/v1/tests/run/archive/native-series", 403},
		{"outsider", "POST", "/api/v1/tests/run/native-series", 403},
	} {
		var body any = q
		if tc.path == "/api/v1/tests/run/native-series" {
			body = nativeEvidenceQuery("archive")
		}
		if strings.Contains(tc.path, "dashboard-definitions") {
			body = definition
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Request-User", tc.subject)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		if response.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.subject, tc.path, response.Code, response.Body.String())
		}
	}
}
