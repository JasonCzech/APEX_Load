package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPrometheusRuleCoverageRequiresItsOwnCompleteQueryWindow(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	rule := analysis.MonitorRule{SourceID: "source", Metric: "cpu", Unit: "percent", Statistic: "avg", Comparator: "lte", Threshold: 5, Severity: "fail", StartOffsetSeconds: 15}
	value := 9.0
	base := analysis.MonitorRuleResult{Rule: rule, Status: "fail", Observed: &value, CoveragePct: 100, EligibleBuckets: 4}
	complete := monitorprovider.Coverage{Status: "complete", From: start, To: end}
	for _, tc := range []struct {
		name     string
		coverage *monitorprovider.Coverage
		severity string
		want     string
	}{
		{"wider completed window preserves failure", &complete, "fail", "fail"},
		{"partial", &monitorprovider.Coverage{Status: "partial", From: start, To: end}, "fail", "not_evaluated"},
		{"throttled", &monitorprovider.Coverage{Status: "throttled", From: start, To: end}, "fail", "not_evaluated"},
		{"unsupported", &monitorprovider.Coverage{Status: "unsupported", From: start, To: end}, "fail", "not_evaluated"},
		{"unavailable", &monitorprovider.Coverage{Status: "unavailable", From: start, To: end}, "fail", "not_evaluated"},
		{"empty", &monitorprovider.Coverage{Status: "no_data", From: start, To: end}, "fail", "not_evaluated"},
		{"missing", nil, "fail", "not_evaluated"},
		{"truncated", &monitorprovider.Coverage{Status: "complete", From: start, To: end, Truncated: true}, "fail", "not_evaluated"},
		{"warning", &monitorprovider.Coverage{Status: "complete", From: start, To: end, Warnings: []string{"missing upstream shard"}}, "fail", "not_evaluated"},
		{"short query window", &monitorprovider.Coverage{Status: "complete", From: start.Add(30 * time.Second), To: end}, "fail", "not_evaluated"},
		{"warning severity", nil, "warning", "warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := base
			result.Rule.Severity = tc.severity
			job := storage.RunMonitorJob{Source: storage.MonitorSource{ID: "source", Prometheus: &prometheus.Selection{Queries: []prometheus.Query{{ID: "cpu-query", Name: "cpu", Unit: "percent"}}}}, State: "incomplete", Coverage: map[string]monitorprovider.Coverage{"other-query": {Status: "partial"}, "targets": {Status: "unavailable"}}}
			if tc.coverage != nil {
				job.Coverage["cpu-query"] = *tc.coverage
			}
			got := guardPrometheusRuleCoverage(result, []storage.RunMonitorJob{job}, start, end)
			if got.Status != tc.want || got.Observed != base.Observed || got.EligibleBuckets != 4 || got.CoveragePct != 100 {
				t.Fatalf("coverage guard changed evidence or verdict: %+v", got)
			}
		})
	}
	legacy := storage.RunMonitorJob{Source: storage.MonitorSource{ID: "source"}}
	if got := guardPrometheusRuleCoverage(base, []storage.RunMonitorJob{legacy}, start, end); got.Status != "fail" {
		t.Fatal("legacy provider acceptance changed", got)
	}
}

func TestPrometheusExactWindowRuleRequiresMatchingTerminalCoverage(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	until := 45
	rule := analysis.MonitorRule{SourceID: "source", Metric: "cpu", QueryID: "cpu-query", EvaluationMode: "exact_window", StartOffsetSeconds: 15, EndOffsetSeconds: &until}
	from, to := start.Add(15*time.Second), start.Add(45*time.Second)
	for _, tc := range []struct {
		name     string
		coverage map[string]monitorprovider.Coverage
		want     string
	}{
		{"matching terminal", map[string]monitorprovider.Coverage{"terminal:1:cpu-query": {Status: "complete", From: from, To: to}}, "pass"},
		{"continuous only", map[string]monitorprovider.Coverage{"cpu-query": {Status: "complete", From: start, To: end}}, "not_evaluated"},
		{"different terminal window", map[string]monitorprovider.Coverage{"terminal:0:cpu-query": {Status: "complete", From: start, To: end}}, "not_evaluated"},
		{"incorrect interval", map[string]monitorprovider.Coverage{"terminal:1:cpu-query": {Status: "complete", From: start, To: end}}, "not_evaluated"},
		{"partial terminal", map[string]monitorprovider.Coverage{"terminal:1:cpu-query": {Status: "partial", From: from, To: to}}, "not_evaluated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := storage.RunMonitorJob{StartedAt: start, LoadEndedAt: &end, Source: storage.MonitorSource{ID: "source", Prometheus: &prometheus.Selection{Queries: []prometheus.Query{{ID: "cpu-query", Name: "cpu", TerminalExpression: "avg_over_time(cpu[{{window.duration}}])"}}}}, FrozenMonitorRules: []analysis.MonitorRule{rule}, Coverage: tc.coverage}
			got := guardPrometheusRuleCoverage(analysis.MonitorRuleResult{Rule: rule, Status: "pass"}, []storage.RunMonitorJob{job}, start, end)
			if got.Status != tc.want {
				t.Fatalf("wrong terminal window accepted: %+v", got)
			}
		})
	}
}

func TestPrometheusOptionalPartialTerminalQueryMakesAcceptanceInconclusive(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	s, store, originalJob, point := apiPrometheusFixture(t, func(selection *prometheus.Selection) {
		selection.Queries[0].TerminalExpression = "avg_over_time(up[{{window.duration}}])"
	})
	// Keep the evidence settled while its real five-minute drain is still open.
	// The shared fixture's ten-minute-old run would already reject this receipt.
	start := time.Now().UTC().Add(-4 * time.Minute).Truncate(time.Millisecond)
	end := start.Add(time.Minute)
	rule := analysis.MonitorRule{SourceID: originalJob.Source.ID, Metric: "cpu", QueryID: "cpu", Unit: "percent", Statistic: "avg", Comparator: "lte", Threshold: 5, Severity: "fail", EvaluationMode: "exact_window"}
	record := &storage.TestRecord{ID: "optional-prom-run", ProjectID: originalJob.ProjectID, Name: "optional monitoring", Status: "RUNNING", StartTime: start, StartedAt: &start, GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{rule}}, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: originalJob.Source.ID, Required: false}}}}
	if err := s.store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors(record.ProjectID, record.ID, "", start, time.Now().UTC().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	record.Status, record.EndTime = "COMPLETE", &end
	if saved, err := s.store.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	}).SaveTestWithRunMonitors(record, "", "", true); err != nil || !saved {
		t.Fatal(saved, err)
	}
	job, err := store.GetRunMonitorJob(record.ProjectID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	point.RunID, point.TimestampUTC = record.ID, end
	point.Provider.Mode, point.Provider.QueryWindow = "query_window", true
	point.Provider.BucketStart, point.Provider.BucketEnd = start, end
	payload := storage.ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: end, Points: []analysis.MetricPoint{point}, Coverage: map[string]monitorprovider.Coverage{"terminal:0:cpu": {Status: "partial", From: start, To: end, Points: 1, ExpectedPoints: 1, Warnings: []string{"upstream omitted a shard"}}}}
	if err := s.ingestPrometheusBatch(context.Background(), *job, 0, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	// Advance the storage lifecycle clock after receipt acceptance, so archive
	// closure exercises the expired partial job without waiting five minutes.
	if err := store.CloseExpiredProviderMonitors(record.ProjectID, record.ID, end.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(resolveArchiveRoot(), "optional-partial-prometheus")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	archive := &storage.RunArchiveRecord{TestID: record.ID, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
	manifest := &RunArchiveManifest{TestID: record.ID, CreatedAt: start, CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start, GoalConfig: record.GoalConfig.(*GoalConfig)}}
	budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if err := s.runner.persistRunMonitorArchive(record.ProjectID, record.ID, archive, manifest, budget); err != nil {
		t.Fatal(err)
	}
	if len(manifest.MonitorCoverage) != 1 || manifest.MonitorCoverage[0].Required || manifest.MonitorEvidenceCount != 1 || len(manifest.MonitorRuleResults) != 1 || manifest.MonitorRuleResults[0].Status != "not_evaluated" || manifest.SLABreached || !monitoringAcceptanceIncomplete(manifest) {
		t.Fatalf("partial optional evidence produced acceptance: %+v", manifest)
	}
	if verdict := deriveReportVerdict(&RunArchiveReport{Manifest: manifest}); verdict.Status != reportVerdictConditional {
		t.Fatal("missing criterion evidence must be Inconclusive", verdict)
	}
}

func TestPrometheusRuleCoverageMatchesQueryUnit(t *testing.T) {
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	rule := analysis.MonitorRule{SourceID: "source", Metric: "memory", Unit: "percent", Statistic: "avg", Comparator: "lte", Threshold: 80}
	job := storage.RunMonitorJob{Source: storage.MonitorSource{ID: "source", Prometheus: &prometheus.Selection{Queries: []prometheus.Query{
		{ID: "bytes", Name: "memory", Unit: "bytes"},
		{ID: "percent", Name: "memory", Unit: "percent"},
	}}}, Coverage: map[string]monitorprovider.Coverage{
		"bytes":   {Status: "complete", From: start, To: end},
		"percent": {Status: "partial", From: start, To: end},
	}}
	base := analysis.MonitorRuleResult{Rule: rule, Status: "pass"}
	if got := guardPrometheusRuleCoverage(base, []storage.RunMonitorJob{job}, start, end); got.Status != "not_evaluated" {
		t.Fatal("unrelated unit's complete query certified a partial criterion", got)
	}
	job.Coverage["bytes"] = monitorprovider.Coverage{Status: "partial", From: start, To: end}
	job.Coverage["percent"] = monitorprovider.Coverage{Status: "complete", From: start, To: end}
	if got := guardPrometheusRuleCoverage(base, []storage.RunMonitorJob{job}, start, end); got.Status != "pass" {
		t.Fatal("unrelated unit's partial query invalidated a complete criterion", got)
	}
}
