package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAppDynamicsArchiveCanonicalLateBucketsDiagnosticsAndRules(t *testing.T) {
	checkAppDynamicsArchiveCanonical(t, false)
}

func TestAppDynamicsArchiveEvidenceBudgetPreservesPartialArchive(t *testing.T) {
	checkAppDynamicsArchiveCanonical(t, true)
}

func checkAppDynamicsArchiveCanonical(t *testing.T, limited bool) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, durable := newTimeslotServer(t)
	store := durable.(providerMonitorStore)
	start := time.Now().UTC().Add(-15 * time.Minute).Truncate(time.Minute)
	end := start.Add(2 * time.Minute)
	connection := &storage.AppDynamicsConnection{ID: "archive-connection", ProjectID: "proj-a", Name: "connection", Enabled: true, CollectionMode: "direct", Config: appdynamics.Config{Endpoint: "https://apm.example.com", AccountName: "account"}, ClientIDSecretName: "client", ClientSecretSecretName: "secret"}
	connections := durable.(storage.AppDynamicsConnectionStore)
	if err := connections.CreateAppDynamicsConnection(connection); err != nil {
		t.Fatal(err)
	}
	if _, err := connections.UpdateAppDynamicsConnectionHealth(connection.ID, "proj-a", 1, "connected", "", appdynamics.Capabilities{Metrics: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	source := storage.MonitorSource{ID: "archive-source", ProjectID: "proj-a", Name: "source", Enabled: true, Pack: "appdynamics", ExecutorMode: "direct", ConnectionID: connection.ID, IntervalSeconds: 60, AppDynamics: &appdynamics.Selection{ProjectID: "proj-a", Applications: []string{"1"}, Events: true, Metrics: []appdynamics.MetricSelection{{ApplicationID: "1", Path: "Overall Application Performance|Calls per Minute", Name: "calls", Unit: "count", Statistic: "sum"}}}}
	if _, err := store.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}
	record := &storage.TestRecord{GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: source.ID, Metric: "calls", Unit: "count", Statistic: "sum", Comparator: "lte", Threshold: 5}}}, ID: "archive-run", Name: "run", ProjectID: "proj-a", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: bindings}
	if err := durable.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors("proj-a", record.ID, "", start, time.Now().UTC().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	job := jobs[0]
	makePoint := func(at time.Time, value float64) analysis.MetricPoint {
		return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "proj-a", RunID: record.ID, Source: source.ID, MetricName: "calls", Unit: "count", Kind: analysis.KindGauge, Quality: analysis.QualityValid, TimestampUTC: at, Value: &value, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "1", CatalogPath: "Overall Application Performance|Calls per Minute", EntityIDs: map[string]string{"application": "1"}, BucketStart: at, BucketEnd: at.Add(time.Minute), ResolutionSeconds: 60, Statistic: "sum", Settled: true, AggregationEligible: true}}
	}
	first := storage.ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: end, Points: []analysis.MetricPoint{makePoint(start, 1), makePoint(start.Add(time.Minute), 2)}, Evidence: []appdynamics.Diagnostic{{ID: "1", Kind: "event", ApplicationID: "1", TimestampUTC: start, Summary: "first"}}}
	if err := server.ingestAppDynamicsBatch(context.Background(), job, 0, first, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	last := storage.ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: end, Points: []analysis.MetricPoint{makePoint(start, 4)}, Evidence: []appdynamics.Diagnostic{{ID: "1", Kind: "event", ApplicationID: "1", TimestampUTC: start, Summary: "updated"}}}
	if err := server.ingestAppDynamicsBatch(context.Background(), job, 1, last, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	record.Status = "COMPLETE"
	record.EndTime = &end
	terminalStore := durable.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	})
	if saved, err := terminalStore.SaveTestWithRunMonitors(record, "", "", true); err != nil || !saved {
		t.Fatalf("persist terminal load and begin reconciliation: saved=%v err=%v", saved, err)
	}
	terminalJob, err := store.GetRunMonitorJob("proj-a", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminalJob.State != "reconciling" || terminalJob.LoadEndedAt == nil || !terminalJob.LoadEndedAt.Equal(end) || terminalJob.DrainDeadline == nil || !terminalJob.DrainDeadline.Equal(end.Add(5*time.Minute)) {
		t.Fatalf("terminal load did not preserve its reconciliation window: %+v", terminalJob)
	}
	root := filepath.Join(resolveArchiveRoot(), "fixture")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	archive := &storage.RunArchiveRecord{TestID: record.ID, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
	manifest := &RunArchiveManifest{TestID: record.ID, CreatedAt: start, CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start, GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: source.ID, Metric: "calls", Unit: "count", Statistic: "sum", Comparator: "lte", Threshold: 5}}}}}
	budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if limited {
		budget.remaining = 1 << 20
	}
	if err := server.runner.persistRunMonitorArchive("proj-a", record.ID, archive, manifest, budget); err != nil {
		t.Fatal(err)
	}
	if limited {
		if manifest.MonitorEvidenceCount != 0 || manifest.MonitorDiagnosticsCount != 0 || manifest.MonitoringStatus != "incomplete" || len(manifest.MonitorCoverage) != 1 || manifest.MonitorCoverage[0].Reason != "archive_evidence_budget" || !manifest.MonitorCoverage[0].ProviderCoverage["archive"].Truncated || budget.failure() != nil {
			t.Fatalf("archive truncation was not safely recorded: %+v, failure=%v", manifest, budget.failure())
		}
		if len(manifest.MonitorRuleResults) != 1 || manifest.MonitorRuleResults[0].Status != "not_evaluated" || !monitoringAcceptanceIncomplete(manifest) {
			t.Fatal("truncated required evidence produced a conclusive verdict", manifest.MonitorRuleResults)
		}
		for _, name := range []string{manifest.MonitorEvidenceFile, manifest.MonitorDiagnosticsFile} {
			body, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || len(body) != 0 {
				t.Fatalf("truncated artifact %s: %s %v", name, body, err)
			}
		}
		return
	}
	if manifest.MonitorEvidenceCount != 2 || manifest.MonitorDiagnosticsCount != 1 {
		t.Fatalf("canonical counts %+v", manifest)
	}
	if len(manifest.MonitorRuleResults) != 1 || manifest.MonitorRuleResults[0].Observed == nil || *manifest.MonitorRuleResults[0].Observed != 6 || manifest.MonitorRuleResults[0].Status != "fail" || !manifest.SLABreached {
		t.Fatalf("terminal rule %+v", manifest.MonitorRuleResults)
	}
	body, err := os.ReadFile(filepath.Join(root, manifest.MonitorDiagnosticsFile))
	if err != nil {
		t.Fatal(err)
	}
	var row RunMonitorDiagnostic
	if err := json.Unmarshal(body, &row); err != nil || row.SourceID != source.ID || row.Diagnostic.Summary != "updated" {
		t.Fatalf("canonical diagnostics %s %v", body, err)
	}
	if !monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("expired required drain was accepted as complete")
	}
	verdict := deriveReportVerdict(&RunArchiveReport{Manifest: manifest})
	if verdict.Status != "Fail" {
		t.Fatalf("conclusive failure lost to missing coverage %+v", verdict)
	}
}
func TestAppDynamicsTerminalAcceptancePendingAndIncomplete(t *testing.T) {
	response := ciRunResponse{Status: "COMPLETE", Outcome: "running"}
	applyCITerminalEvidence(&response, nil, errMonitoringPending)
	if response.Outcome != "monitoring_pending" || response.Error != "" || response.MonitoringStatus != "pending" {
		t.Fatalf("premature terminal failure %+v", response)
	}
	manifest := &RunArchiveManifest{MonitorCoverage: []RunMonitorCoverage{{Required: true, State: "incomplete"}}}
	if verdict := deriveReportVerdict(&RunArchiveReport{Manifest: manifest}); verdict.Status != "Inconclusive" {
		t.Fatalf("missing monitoring passed %+v", verdict)
	}
	manifest.MonitorCoverage[0].Required = false
	if monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("optional monitor absence blocks acceptance")
	}
	manifest.Provenance.GoalConfig = &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: "s", Metric: "m", Severity: "fail"}}}
	if !monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("missing explicit metric criterion passed")
	}
}

func TestAppDynamicsNumericEvidenceAndFrozenRuleSelection(t *testing.T) {
	selection := appdynamics.Selection{Applications: []string{"1"}, Events: true, Snapshots: true}
	if !selectedMonitorMetricsObserved(selection, nil) {
		t.Fatal("successful empty sampled diagnostics became missing metric evidence")
	}
	selection.Metrics = []appdynamics.MetricSelection{{ApplicationID: "1", Name: "calls"}}
	if selectedMonitorMetricsObserved(selection, nil) {
		t.Fatal("required empty numeric evidence passed")
	}
	if selectedMonitorMetricsObserved(selection, map[string]bool{"2:calls": true}) {
		t.Fatal("another application's bucket satisfies required scope")
	}
	if !selectedMonitorMetricsObserved(selection, map[string]bool{"1:calls": true}) {
		t.Fatal("eligible selected metric missing")
	}
	selection.Analytics = []appdynamics.AnalyticsQuery{{ApplicationID: "1", Name: "transactions", Kind: "metric"}}
	if selectedMonitorMetricsObserved(selection, map[string]bool{"1:calls": true}) {
		t.Fatal("missing selected Analytics numeric evidence passed")
	}
	initial := &RunArchiveProvenance{GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: "frozen"}}}}
	manifest := &RunArchiveManifest{InitialProvenance: initial, Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: "edited"}}}}}
	if rules := monitorRulesFromManifest(manifest); len(rules) != 1 || rules[0].SourceID != "frozen" {
		t.Fatal("archival uses edited acceptance rules")
	}
	initial.GoalConfig.MonitorRules = nil
	if len(monitorRulesFromManifest(manifest)) != 0 {
		t.Fatal("empty frozen acceptance rules inherited later edits")
	}
}

func TestAppDynamicsCaptureValidityIsIndependentOfRuleAggregation(t *testing.T) {
	start := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	value := 0.0
	for _, tc := range []struct {
		name, statistic string
		settled         bool
		offset          time.Duration
		want            bool
	}{
		{"percentile", "percentile_95", true, 0, true},
		{"unweighted_average", "average", true, 0, true},
		{"provisional", "average", false, 0, false},
		{"partial_boundary", "average", true, -time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := start.Add(tc.offset)
			point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "project", Source: "source", MetricName: "latency", Unit: "ms", Kind: analysis.KindGauge, Quality: analysis.QualityValid, TimestampUTC: at, Value: &value, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "1", BucketStart: at, BucketEnd: at.Add(time.Minute), ResolutionSeconds: 60, Statistic: tc.statistic, Settled: tc.settled, AggregationEligible: false}}
			if got := monitorPointSatisfiesCapture(point, start, start.Add(time.Minute)); got != tc.want {
				t.Fatalf("capture validity = %v, want %v", got, tc.want)
			}
			rule := analysis.MonitorRule{SourceID: "source", Metric: "latency", Unit: "ms", Statistic: "avg", Comparator: "lte", Threshold: 100}
			if result := analysis.EvaluateMonitorRule(rule, []analysis.MetricPoint{point}, start, start.Add(time.Minute)); result.Status != "not_evaluated" {
				t.Fatalf("nonaggregatable capture entered acceptance: %+v", result)
			}
		})
	}
}

func TestAppDynamicsWarningRulesDoNotBlockAcceptanceWhenEvidenceMissing(t *testing.T) {
	warning := analysis.MonitorRule{SourceID: "optional", Metric: "calls", Unit: "count", Statistic: "sum", Comparator: "lte", Threshold: 10, Severity: "warning"}
	manifest := &RunArchiveManifest{InitialProvenance: &RunArchiveProvenance{GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{warning}}}}
	if monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("missing optional warning results blocked acceptance")
	}
	manifest.MonitorRuleResults = []analysis.MonitorRuleResult{{Rule: warning, Status: "not_evaluated"}}
	if monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("unevaluated warning criterion blocked acceptance")
	}
	failure := warning
	failure.Severity = "fail"
	manifest.InitialProvenance.GoalConfig.MonitorRules = append(manifest.InitialProvenance.GoalConfig.MonitorRules, failure)
	if !monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("missing failure criterion accepted a warning result for another rule")
	}
	manifest.MonitorCoverage = []RunMonitorCoverage{{Required: false, State: "incomplete"}}
	manifest.InitialProvenance.GoalConfig.MonitorRules = nil
	manifest.MonitorRuleResults = nil
	if monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("optional capture problem without acceptance rules blocked acceptance")
	}
}
