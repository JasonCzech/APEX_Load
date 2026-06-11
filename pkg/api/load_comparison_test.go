package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBuildLoadComparisonGroupScopeUsesGroupMetricsUnderLoad(t *testing.T) {
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	runs := []loadComparisonRun{
		loadComparisonRunFixture("run-a", "rel-a", "Sprint 1", base, nil, []IntervalSummaryRecord{
			loadComparisonSummary(base, 10, map[string]*GroupLiveMetrics{
				"Checkout": {Name: "Checkout", VUsers: 5, Transactions: 10, Errors: 1, TPS: 2, AvgResponseMs: 100},
			}),
			loadComparisonSummary(base.Add(time.Second), 20, map[string]*GroupLiveMetrics{
				"Checkout": {Name: "Checkout", VUsers: 10, Transactions: 12, Errors: 0, TPS: 3, AvgResponseMs: 140},
			}),
		}),
		loadComparisonRunFixture("run-b", "rel-b", "Sprint 2", base.Add(time.Hour), nil, []IntervalSummaryRecord{
			loadComparisonSummary(base.Add(time.Hour), 10, map[string]*GroupLiveMetrics{
				"Checkout": {Name: "Checkout", VUsers: 5, Transactions: 20, Errors: 0, TPS: 4, AvgResponseMs: 90},
			}),
		}),
	}

	comparison := buildLoadComparison("app-1", LoadComparisonScopeGroup, "Checkout", runs)

	if comparison.Scope != LoadComparisonScopeGroup || comparison.SelectedKey != "Checkout" {
		t.Fatalf("scope/key = %s/%s, want group/Checkout", comparison.Scope, comparison.SelectedKey)
	}
	if len(comparison.Options) != 1 || comparison.Options[0].RunCount != 2 || comparison.Options[0].ReleaseCount != 2 {
		t.Fatalf("options = %#v, want Checkout across two runs/releases", comparison.Options)
	}
	if got := loadLevelLabels(comparison.LoadLevels); !reflect.DeepEqual(got, []string{"5 VUsers", "10 VUsers"}) {
		t.Fatalf("load levels = %#v, want 5 and 10 VUsers", got)
	}
	sprint1 := comparison.Releases[0]
	if sprint1.ReleaseID != "rel-a" || len(sprint1.Points) != 2 {
		t.Fatalf("first release = %#v, want rel-a with two load points", sprint1)
	}
	if sprint1.Points[0].TPS != 2 || sprint1.Points[0].AvgResponseMs != 100 || sprint1.Points[0].ErrorPct != 10 {
		t.Fatalf("rel-a 5 VUser point = %#v, want TPS 2 ART 100 error 10%%", sprint1.Points[0])
	}
	if sprint1.Points[1].TPS != 3 || sprint1.Points[1].AvgResponseMs != 140 {
		t.Fatalf("rel-a 10 VUser point = %#v, want TPS 3 ART 140", sprint1.Points[1])
	}
}

func TestBuildLoadComparisonTransactionScopeAggregatesByIntervalAndPrefersSourceTransaction(t *testing.T) {
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	runs := []loadComparisonRun{
		loadComparisonRunFixture("run-a", "rel-a", "Sprint 1", base,
			[]TransactionEvent{
				{Timestamp: base.Add(100 * time.Millisecond), ActionName: "Click Checkout", SourceTransaction: "Checkout Flow", Group: "Buyers", DurationMs: 100, Success: true},
				{Timestamp: base.Add(200 * time.Millisecond), ActionName: "Click Checkout", SourceTransaction: "Checkout Flow", Group: "Buyers", DurationMs: 140, Success: false},
				{Timestamp: base.Add(300 * time.Millisecond), ActionName: "Click Search", Group: "Buyers", DurationMs: 80, Success: true},
			},
			[]IntervalSummaryRecord{
				loadComparisonSummary(base, 25, map[string]*GroupLiveMetrics{
					"Buyers": {Name: "Buyers", VUsers: 25},
				}),
			},
		),
	}

	comparison := buildLoadComparison("app-1", LoadComparisonScopeTransaction, "", runs)

	if comparison.SelectedKey != "Checkout Flow" {
		t.Fatalf("selected key = %q, want source transaction Checkout Flow", comparison.SelectedKey)
	}
	if len(comparison.Options) != 2 {
		t.Fatalf("options = %#v, want source transaction plus fallback action", comparison.Options)
	}
	if comparison.Options[0].SourceKind != "source_transaction" {
		t.Fatalf("top option source kind = %q, want source_transaction", comparison.Options[0].SourceKind)
	}
	if got := loadLevelLabels(comparison.LoadLevels); !reflect.DeepEqual(got, []string{"25 VUsers"}) {
		t.Fatalf("load levels = %#v, want selected group load", got)
	}
	point := comparison.Releases[0].Points[0]
	if point.Transactions != 2 || point.Errors != 1 || point.TPS != 2 || point.AvgResponseMs != 120 || point.ErrorPct != 50 {
		t.Fatalf("transaction point = %#v, want 2 tx, 1 error, TPS 2, ART 120, error 50%%", point)
	}
}

func TestBuildLoadComparisonTransactionFallsBackToTotalActiveVUsers(t *testing.T) {
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	runs := []loadComparisonRun{
		loadComparisonRunFixture("run-a", "rel-a", "Sprint 1", base,
			[]TransactionEvent{
				{Timestamp: base, ActionName: "Search", Group: "Missing", DurationMs: 80, Success: true},
			},
			[]IntervalSummaryRecord{
				loadComparisonSummary(base, 50, nil),
			},
		),
	}

	comparison := buildLoadComparison("app-1", LoadComparisonScopeTransaction, "Search", runs)

	if got := loadLevelLabels(comparison.LoadLevels); !reflect.DeepEqual(got, []string{"50 VUsers"}) {
		t.Fatalf("load levels = %#v, want total active VUser fallback", got)
	}
}

func TestBuildLoadComparisonReleaseRunCountOnlyIncludesSelectedGraphRuns(t *testing.T) {
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	runs := []loadComparisonRun{
		loadComparisonRunFixture("run-with-checkout", "rel-a", "Sprint 1", base,
			[]TransactionEvent{
				{Timestamp: base, ActionName: "Checkout", SourceTransaction: "Checkout Flow", DurationMs: 100, Success: true},
			},
			[]IntervalSummaryRecord{loadComparisonSummary(base, 10, nil)},
		),
		loadComparisonRunFixture("run-with-search", "rel-a", "Sprint 1", base.Add(time.Minute),
			[]TransactionEvent{
				{Timestamp: base.Add(time.Minute), ActionName: "Search", DurationMs: 80, Success: true},
			},
			[]IntervalSummaryRecord{loadComparisonSummary(base.Add(time.Minute), 10, nil)},
		),
	}

	comparison := buildLoadComparison("app-1", LoadComparisonScopeTransaction, "Checkout Flow", runs)

	if len(comparison.Releases) != 1 {
		t.Fatalf("releases = %#v, want one release", comparison.Releases)
	}
	if comparison.Releases[0].RunCount != 1 {
		t.Fatalf("release run_count = %d, want only selected graph run", comparison.Releases[0].RunCount)
	}
	if got := comparison.Releases[0].Points[0].RunCount; got != 1 {
		t.Fatalf("point run_count = %d, want selected graph run count", got)
	}
}

func TestApplicationLoadComparisonEndpointReturnsJSON(t *testing.T) {
	server := NewServer(ServerConfig{})
	applicationID := "app-load-comparison"
	releaseID := "rel-load-comparison"
	started := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	archive := writeLoadComparisonArchiveFixture(t, releaseID, "run-load", "Load Run", started)

	server.runner.mu.Lock()
	server.runner.tests["run-load"] = &ManagedTest{
		ID:            "run-load",
		Name:          "Load Run",
		Status:        "COMPLETE",
		ApplicationID: applicationID,
		ReleaseID:     releaseID,
		CreatedAt:     started,
		StartedAt:     ptrTime(started),
		EndedAt:       ptrTime(started.Add(time.Minute)),
		Archive:       archive,
	}
	server.runner.archives["run-load"] = archive
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+applicationID+"/load-comparison?scope=group&key=Checkout", nil)
	rr := httptest.NewRecorder()
	server.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var comparison LoadComparisonResponse
	if err := json.NewDecoder(rr.Body).Decode(&comparison); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if comparison.Scope != LoadComparisonScopeGroup || comparison.SelectedKey != "Checkout" {
		t.Fatalf("comparison = %#v, want group Checkout", comparison)
	}
	if len(comparison.Releases) != 1 || len(comparison.Releases[0].Points) != 1 {
		t.Fatalf("releases = %#v, want one release point", comparison.Releases)
	}
}

func TestApplicationLoadComparisonEndpointFiltersSelectedTestIDs(t *testing.T) {
	server := NewServer(ServerConfig{})
	applicationID := "app-load-comparison-filter"
	releaseID := "rel-load-comparison-filter"
	started := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	archiveA := writeLoadComparisonArchiveFixture(t, releaseID, "run-load-a", "Load Run A", started)
	archiveB := writeLoadComparisonArchiveFixture(t, releaseID, "run-load-b", "Load Run B", started.Add(time.Minute))

	server.runner.mu.Lock()
	server.runner.tests["run-load-a"] = &ManagedTest{
		ID:            "run-load-a",
		Name:          "Load Run A",
		Status:        "COMPLETE",
		ApplicationID: applicationID,
		ReleaseID:     releaseID,
		CreatedAt:     started,
		StartedAt:     ptrTime(started),
		EndedAt:       ptrTime(started.Add(time.Minute)),
		Archive:       archiveA,
	}
	server.runner.tests["run-load-b"] = &ManagedTest{
		ID:            "run-load-b",
		Name:          "Load Run B",
		Status:        "COMPLETE",
		ApplicationID: applicationID,
		ReleaseID:     releaseID,
		CreatedAt:     started.Add(time.Minute),
		StartedAt:     ptrTime(started.Add(time.Minute)),
		EndedAt:       ptrTime(started.Add(2 * time.Minute)),
		Archive:       archiveB,
	}
	server.runner.archives["run-load-a"] = archiveA
	server.runner.archives["run-load-b"] = archiveB
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+applicationID+"/load-comparison?scope=transaction&key=Checkout%20Flow&test_ids=run-load-a,missing-run", nil)
	rr := httptest.NewRecorder()
	server.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var comparison LoadComparisonResponse
	if err := json.NewDecoder(rr.Body).Decode(&comparison); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(comparison.Options) != 1 {
		t.Fatalf("options = %#v, want one selected-run option", comparison.Options)
	}
	if comparison.Options[0].RunCount != 1 || comparison.Options[0].TransactionCount != 1 {
		t.Fatalf("option = %#v, want only run-load-a counted", comparison.Options[0])
	}
	if len(comparison.Releases) != 1 || comparison.Releases[0].RunCount != 1 {
		t.Fatalf("releases = %#v, want one selected run", comparison.Releases)
	}
	if got := comparison.Releases[0].Points[0].RunCount; got != 1 {
		t.Fatalf("point run_count = %d, want only run-load-a counted", got)
	}
}

func loadComparisonRunFixture(id string, releaseID string, releaseName string, createdAt time.Time, transactions []TransactionEvent, summaries []IntervalSummaryRecord) loadComparisonRun {
	return loadComparisonRun{
		Test:             &ManagedTest{ID: id, Name: id, Status: "COMPLETE", ReleaseID: releaseID, CreatedAt: createdAt},
		ReleaseID:        releaseID,
		ReleaseName:      releaseName,
		ReleaseCreatedAt: createdAt,
		Transactions:     transactions,
		Summaries:        summaries,
	}
}

func loadComparisonSummary(timestamp time.Time, activeVUsers int64, groups map[string]*GroupLiveMetrics) IntervalSummaryRecord {
	return IntervalSummaryRecord{
		Timestamp:    timestamp,
		ActiveVUsers: activeVUsers,
		Groups:       groups,
	}
}

func loadLevelLabels(levels []LoadComparisonLoadLevel) []string {
	labels := make([]string, 0, len(levels))
	for _, level := range levels {
		labels = append(labels, level.Label)
	}
	return labels
}

func writeLoadComparisonArchiveFixture(t *testing.T, releaseID string, testID string, testName string, startedAt time.Time) *storage.RunArchiveRecord {
	t.Helper()
	archiveDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              testName,
		CreatedAt:             startedAt,
		CompletedAt:           startedAt.Add(time.Minute),
		Status:                "COMPLETE",
		Provenance:            RunArchiveProvenance{ReleaseID: releaseID, StartedAt: ptrTime(startedAt)},
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      1,
		IntervalSummaryCount:  1,
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeJSONL(t, filepath.Join(archiveDir, manifest.TransactionsFile),
		TransactionEvent{Timestamp: startedAt, TestID: testID, TestName: testName, Group: "Checkout", ActionName: "Submit Order", SourceTransaction: "Checkout Flow", DurationMs: 125, Success: true},
	)
	writeJSONL(t, filepath.Join(archiveDir, manifest.ResourceSamplesFile))
	writeJSONL(t, filepath.Join(archiveDir, manifest.IntervalSummariesFile),
		IntervalSummaryRecord{
			Timestamp:    startedAt,
			TestID:       testID,
			TestName:     testName,
			ActiveVUsers: 5,
			Groups: map[string]*GroupLiveMetrics{
				"Checkout": {Name: "Checkout", VUsers: 5, Transactions: 1, TPS: 1, AvgResponseMs: 125},
			},
		},
	)
	return &storage.RunArchiveRecord{
		TestID:               testID,
		SchemaVersion:        archiveSchemaVersion,
		ArchivePath:          archiveDir,
		ManifestPath:         manifestPath,
		TransactionCount:     manifest.TransactionCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount,
		CreatedAt:            startedAt,
		CompletedAt:          startedAt.Add(time.Minute),
	}
}
