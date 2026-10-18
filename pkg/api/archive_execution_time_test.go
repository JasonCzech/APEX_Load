package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveExecutionCompletedAtPreservesLegacyFallback(t *testing.T) {
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	durable := start.Add(2 * time.Minute)
	zero := time.Time{}
	for _, tc := range []struct {
		name     string
		manifest *RunArchiveManifest
		want     time.Time
	}{
		{"nil", nil, zero},
		{"missing", &RunArchiveManifest{}, zero},
		{"legacy", &RunArchiveManifest{CompletedAt: durable}, durable},
		{"zero executor", &RunArchiveManifest{CompletedAt: durable, ExecutorCompletedAt: &zero}, durable},
		{"retained executor", &RunArchiveManifest{CompletedAt: durable, ExecutorCompletedAt: &start}, start},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := archiveExecutionCompletedAt(tc.manifest); !got.Equal(tc.want) {
				t.Fatalf("execution end = %v, want %v", got, tc.want)
			}
		})
	}
}

// Use recovery's actual serialization, including the retained executor time,
// so consumers are exercised with the same manifest a recovered archive reads.
func restampExecutionFixture(t *testing.T, manifest *RunArchiveManifest, durable time.Time) *RunArchiveManifest {
	t.Helper()
	original := manifest.CompletedAt
	data, completed, err := restampedOrphanArchiveManifest(manifest, durable)
	if err != nil {
		t.Fatal(err)
	}
	var restored RunArchiveManifest
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !completed.Equal(durable) || !restored.CompletedAt.Equal(durable) || !manifest.CompletedAt.Equal(original) {
		t.Fatal("restamping lost durable identity or mutated the source")
	}
	return &restored
}

func TestRestampedArchivePreservesExecutionRatesAndOutcome(t *testing.T) {
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	durable := start.Add(2 * time.Minute)
	manifest := restampExecutionFixture(t, &RunArchiveManifest{
		CreatedAt: start, CompletedAt: start.Add(time.Minute),
		Provenance: RunArchiveProvenance{StartedAt: &start, Duration: "1m"},
	}, durable)
	window := archivePopulationWindow(manifest)
	if window != time.Minute || transactionSLARunSeconds(manifest) != 60 || reportElapsedDuration(manifest) != "60 seconds" {
		t.Fatalf("recovery extended measured duration: %v / %v / %s", window, transactionSLARunSeconds(manifest), reportElapsedDuration(manifest))
	}
	population := &archivePopulationAccumulator{}
	for range 300 {
		population.add(TransactionEvent{Success: true})
	}
	if result := population.finish(window); result.AverageRate == nil || *result.AverageRate != 5 {
		t.Fatalf("recovery lowered throughput: %+v", result)
	}
	outcome := buildIntegrationOutcome(&RunArchiveReport{Manifest: manifest})
	if outcome.Execution.ObservedDurationSeconds == nil || *outcome.Execution.ObservedDurationSeconds != 60 || outcome.Execution.PlannedDurationSeconds == nil || *outcome.Execution.PlannedDurationSeconds != 60 {
		t.Fatalf("execution outcome changed: %+v", outcome.Execution)
	}
	report := &RunArchiveReport{Manifest: manifest}
	report.Overview.Transactions = 300
	row := buildPowerBIRunRow(report, "publish", durable.Add(time.Minute), "")
	if row["duration_seconds"] != float64(60) || row["avg_tps"] != float64(5) || row["completed_at"] != durable.Format(time.RFC3339) {
		t.Fatalf("Power BI conflated execution duration with completion identity: %+v", row)
	}
	if !manifest.CompletedAt.Equal(durable) {
		t.Fatal("analytics replaced canonical completion identity")
	}
	// An invalid executor window cannot borrow the later recovery time to
	// fabricate a valid rate or observed execution duration.
	invalid := start.Add(-time.Second)
	manifest.ExecutorCompletedAt = &invalid
	if archivePopulationWindow(manifest) != 0 || transactionSLARunSeconds(manifest) != 0 || reportElapsedDuration(manifest) != "Unknown" || buildIntegrationOutcome(&RunArchiveReport{Manifest: manifest}).Execution.ObservedDurationSeconds != nil {
		t.Fatal("invalid execution window was hidden by durable recovery time")
	}
}

func TestRestampedArchiveTransactionSLARetainsExecutionTPS(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	file, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := json.NewEncoder(file).Encode(TransactionEvent{Timestamp: start.Add(time.Duration(i) * time.Second), ActionName: "Checkout", BusinessTransaction: true, Success: true}); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := restampExecutionFixture(t, &RunArchiveManifest{
		CreatedAt: start, CompletedAt: start.Add(time.Minute), TransactionsFile: "transactions.jsonl",
		Provenance: RunArchiveProvenance{StartedAt: &start, GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{
			{Transaction: "Checkout", Metric: "tps", Comparator: "eq", Threshold: 2.0 / 60.0, Severity: "fail"},
		}}},
	}, start.Add(2*time.Minute))
	if err := applyTransactionSLARules(root, manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.TransactionRuleResults) != 1 || manifest.TransactionRuleResults[0].Status != "pass" || manifest.TransactionRuleResults[0].MatchedCount != 2 {
		t.Fatalf("recovery changed TPS acceptance: %+v", manifest.TransactionRuleResults)
	}
}

func TestRestampedArchiveNativeBucketsEndAtExecutorCompletion(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	manifest := restampExecutionFixture(t, &RunArchiveManifest{
		TestID: record.TestID, CompletedAt: start.Add(10 * time.Second), TransactionsFile: "transactions.jsonl",
		Provenance: RunArchiveProvenance{StartedAt: &start},
	}, start.Add(20*time.Second))
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	q := nativeEvidenceQuery("archive")
	rawQuery := storage.AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 20}
	raw, err := buildNativeArchiveSeries(context.Background(), record, rawQuery)
	if err != nil {
		t.Fatal(err)
	}
	const warning = "The requested window extends past run completion."
	if len(raw.Buckets) != 2 || !raw.Buckets[0].complete || raw.Buckets[1].complete || raw.Buckets[0].Count != 3 || raw.Buckets[0].Value == nil || *raw.Buckets[0].Value != 30 || !slices.Contains(raw.Warnings, warning) {
		t.Fatalf("raw query extended workload evidence through recovery: %+v", raw)
	}
	grouped, err := buildNativeGroupedArchive(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Series) != 1 || len(grouped.Series[0].Points) != 2 || !grouped.Series[0].Points[0].complete || grouped.Series[0].Points[1].complete || grouped.Series[0].Points[0].Value == nil || *grouped.Series[0].Points[0].Value != 30 || !slices.Contains(grouped.Warnings, "The selection extends past run completion.") {
		t.Fatalf("grouped query extended workload evidence through recovery: %+v", grouped)
	}
	findings, err := evaluateArchiveBrowserFindings(context.Background(), &browserIntelligenceInputs{runID: record.TestID, record: record, policy: e1Policy(e1LCP)})
	if err != nil {
		t.Fatal(err)
	}
	if !findings.Window.Start.Equal(start) || !findings.Window.End.Equal(start.Add(10*time.Second)) {
		t.Fatalf("browser analysis extended execution window: %+v", findings.Window)
	}
}

func TestRestampedArchiveBusinessKeyLogQueryUsesExecutorEnd(t *testing.T) {
	server, store := businessKeyServer(t)
	approveBusinessKeyMapping(t, server, store)
	record := exemplarArchiveFixture(t, server, store, "restamped-logs", nil)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	executor := manifest.CompletedAt
	manifest = restampExecutionFixture(t, manifest, executor.Add(time.Minute))
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", storage.DefaultProjectID)
	run, err := store.GetTest(record.TestID)
	if err != nil {
		t.Fatal(err)
	}
	run.MonitorBindings = &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: "elastic-bk"}}}
	if err := store.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.StartRunMonitors(storage.DefaultProjectID, record.TestID, "", exemplarFixtureStart, exemplarFixtureStart.Add(time.Hour)); err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	logs := &businessKeyLogs{}
	server.elasticHTTPClient = &http.Client{Transport: logs}
	server.runner.projectSecretStore = &devSecretStore{value: "fixture-token"}
	results := make([]BusinessKeyLookupResult, 1)
	server.lookupBusinessKeyLogs(context.Background(), storage.DefaultProjectID, record.TestID, manifest, businessKeyMappingDefinition{businessKeyMapping: businessKeyMapping{SourceID: "elastic-bk", Field: "order.id"}}, []string{"ORD-1"}, results)
	if len(logs.requests) != 1 || results[0].Logs.Status != businessKeyLogsNotFound {
		t.Fatalf("log query did not execute: %+v / %+v", logs.requests, results)
	}
	filters := logs.requests[0]["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
	rangeFilter := filters[0].(map[string]any)["range"].(map[string]any)["@timestamp"].(map[string]any)
	if rangeFilter["gte"] != exemplarFixtureStart.Add(-businessKeyLogPadding).Format(time.RFC3339Nano) || rangeFilter["lte"] != executor.Add(businessKeyLogPadding).Format(time.RFC3339Nano) {
		t.Fatalf("log query includes recovery delay: %+v", rangeFilter)
	}
}

func TestRestampedArchivePhaseStopUsesExecutorEnd(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	executor, durable := start.Add(time.Minute), start.Add(2*time.Minute)
	root := t.TempDir()
	record := &storage.RunArchiveRecord{TestID: "restamped", ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
	admitArchiveForTest(t, record)
	manifest := restampExecutionFixture(t, &RunArchiveManifest{CompletedAt: executor}, durable)
	if err := runner.persistPhaseEventArchive(context.Background(), &ManagedTest{ID: record.TestID, EndedAt: &durable}, record, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, manifest.PhaseEventsFile))
	if err != nil {
		t.Fatal(err)
	}
	var event PhaseEvent
	if err := json.Unmarshal(data, &event); err != nil || event.Kind != PhaseStop || !event.At.Equal(executor) || manifest.PhaseEventCount != 1 {
		t.Fatalf("phase stop includes recovery delay: %s / %v", data, err)
	}
	if _, kept := runner.phaseLogs.Load(record.TestID); kept {
		t.Fatal("archiving retained the completed phase log")
	}
}
