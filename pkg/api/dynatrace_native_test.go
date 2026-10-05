package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func apiDynatraceNativeFixture(t *testing.T, configure ...func(storage.Store, *storage.TestRecord, storage.MonitorSource)) (*Server, providerMonitorStore, storage.RunMonitorJob, analysis.MetricPoint) {
	t.Helper()
	server, db := newTimeslotServer(t)
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", "")
	c := &storage.DynatraceConnection{ID: "dt-native", ProjectID: "proj-a", Name: "SaaS", Enabled: true, CollectionMode: "direct", EnvironmentURL: "https://fixture.live.dynatrace.com", EnvironmentTokenSecret: "protected-environment-ref", MetricsEnabled: true, ProblemsEnabled: true}
	connections := db.(storage.DynatraceConnectionStore)
	if err := connections.CreateDynatraceConnection(c); err != nil {
		t.Fatal(err)
	}
	health := db.(interface {
		UpdateDynatraceConnectionHealthIfRevision(string, string, int64, string, string, dynatrace.Capabilities, time.Time) (bool, error)
	})
	if updated, err := health.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, c.Revision, "connected", "", dynatrace.Capabilities{Metrics: true, Problems: true}, time.Now().UTC()); err != nil || !updated {
		t.Fatalf("health %v %v", updated, err)
	}
	metric := dynatrace.MetricSelection{Name: "calls", MetricSelector: "builtin:service.requestCount.total", Aggregation: "sum", Unit: "count"}
	source := storage.MonitorSource{ID: "dt-native-source", ProjectID: c.ProjectID, Name: "SaaS", Enabled: true, Pack: "dynatrace", ExecutorMode: "direct", ConnectionID: c.ID, IntervalSeconds: 60, Dynatrace: &dynatrace.Selection{ProjectID: c.ProjectID, Problems: true, Metrics: []dynatrace.MetricSelection{metric}}}
	monitors := db.(providerMonitorStore)
	if _, err := monitors.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	record := &storage.TestRecord{ID: "dt-native-run", ProjectID: c.ProjectID, Name: "SaaS run", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}}
	for _, apply := range configure {
		apply(db, record, source)
	}
	if err := db.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	// This fixture has no execution backend. Persist its terminal lifecycle
	// before the server's owned cleanup tries to stop active runs.
	t.Cleanup(func() {
		saved, err := db.GetTest(record.ID)
		if err != nil {
			t.Errorf("load synthetic Dynatrace run for cleanup: %v", err)
			return
		}
		if saved.Status != "COMPLETE" && saved.Status != "FAILED" && saved.Status != "ABORTED" {
			ended := time.Now().UTC()
			saved.Status, saved.EndTime = "COMPLETE", &ended
			terminal := db.(interface {
				SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
			})
			if updated, err := terminal.SaveTestWithRunMonitors(saved, "", "", true); err != nil || !updated {
				t.Errorf("complete synthetic Dynatrace run: %v %v", updated, err)
				return
			}
		}
		server.runner.mu.Lock()
		if current := server.runner.tests[record.ID]; current != nil {
			current.Status, current.EndedAt = saved.Status, saved.EndTime
		}
		server.runner.mu.Unlock()
	})
	jobs, err := monitors.StartRunMonitors(c.ProjectID, record.ID, record.ScenarioID, start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	value := 0.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: c.ProjectID, RunID: record.ID, ScenarioID: record.ScenarioID, Source: source.ID, TimestampUTC: start, MetricName: "calls", Kind: analysis.KindGauge, Value: &value, Unit: "count", Quality: analysis.QualityValid, Dimensions: map[string]string{"dt.entity.service": "SERVICE-1234567890ABCDEF"}, Provider: &analysis.ProviderMeasurement{Name: "dynatrace", Backend: "classic", MetricID: dynatrace.MetricID(metric), CatalogPath: metric.MetricSelector, EntityIDs: map[string]string{"dt.entity.service": "SERVICE-1234567890ABCDEF"}, BucketStart: start, BucketEnd: start.Add(time.Minute), ResolutionSeconds: 60, Statistic: "sum", Settled: true, AggregationEligible: true}}
	return server, monitors, jobs[0], point
}

type misdirectedDynatraceCanonicalStore struct{ providerMonitorStore }

func (s misdirectedDynatraceCanonicalStore) ListProviderObservationHeads(project, run, source, kind, after string, limit int) ([]storage.ObservationHead, error) {
	heads, err := s.providerMonitorStore.ListProviderObservationHeads(project, run, source, kind, after, limit)
	for i := range heads {
		if heads[i].Sequence > 0 {
			heads[i].Sequence-- // Same key and valid artifact, but the wrong revision.
		}
	}
	return heads, err
}

func TestDynatraceNativeCanonicalRevisionsAndDisclosure(t *testing.T) {
	server, store, job, point := apiDynatraceNativeFixture(t)
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Points: []analysis.MetricPoint{point}, Evidence: []analysis.ProviderDiagnostic{{Provider: "dynatrace", Backend: "classic", ID: "problem-1", Kind: "problem", TimestampUTC: point.TimestampUTC, Summary: "password=confidential", Properties: map[string]string{"sql": "SELECT secret", "request_body": "sensitive", "correlation": "entity_time"}}}}
	for range 2 {
		if err := server.ingestDynatraceBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err != nil {
			t.Fatal(err)
		}
	}
	value := 7.0
	payload.Points[0].Value = &value
	payload.Evidence[0].Status = "CLOSED"
	if err := server.ingestDynatraceBatch(context.Background(), job, 1, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "point", "", 10)
	if err != nil || len(page.Points) != 1 || *page.Points[0].Value != 7 || page.Points[0].Quality != analysis.QualityValid {
		t.Fatalf("canonical %+v %v", page, err)
	}
	evidence, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, job.Source.ID, "diagnostic", "", 10)
	if err != nil || len(evidence.Evidence) != 1 || evidence.Evidence[0].Status != "CLOSED" || evidence.Evidence[0].Properties["sql"] != "" || evidence.Evidence[0].Properties["request_body"] != "" || strings.Contains(evidence.Evidence[0].Summary, "confidential") {
		t.Fatalf("evidence %+v %v", evidence, err)
	}
	receipts, _ := store.ListMonitorBatchReceipts(job.ProjectID, job.RunID, "", -1, 10)
	if len(receipts) != 2 {
		t.Fatal("identical retry duplicated receipt")
	}
	for _, kind := range []string{"point", "diagnostic"} {
		_, err := loadCanonicalMonitorData(t.Context(), misdirectedDynatraceCanonicalStore{store}, job.ProjectID, job.RunID, job.Source.ID, kind, "", 10)
		if err == nil || !strings.Contains(err.Error(), "content") {
			t.Fatalf("same-key older %s revision escaped integrity validation: %v", kind, err)
		}
	}
	public := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitors", nil)
	if public.Code != 200 || strings.Contains(public.Body.String(), "protected-environment-ref") || strings.Contains(public.Body.String(), "dynatrace_snapshot") {
		t.Fatalf("public %d %s", public.Code, public.Body.String())
	}
}
func TestDynatraceNativeRejectsMisattributionAndCursorForgery(t *testing.T) {
	server, _, job, point := apiDynatraceNativeFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*storage.ProviderMonitorBatch)
	}{
		{"project", func(b *storage.ProviderMonitorBatch) { b.Points[0].ProjectID = "other" }},
		{"host", func(b *storage.ProviderMonitorBatch) { b.Points[0].Host = "other" }},
		{"provider metric", func(b *storage.ProviderMonitorBatch) { b.Points[0].Provider.MetricID = "foreign" }},
		{"watermark", func(b *storage.ProviderMonitorBatch) { end := b.QueryUntilUTC; b.QueryWindowEnd = &end }},
		{"coverage", func(b *storage.ProviderMonitorBatch) {
			b.Coverage = map[string]analysis.ProviderCoverage{"foreign": {Status: "complete", From: b.QueryFromUTC, To: b.QueryUntilUTC}}
		}},
		{"progress", func(b *storage.ProviderMonitorBatch) {
			b.Progress = &analysis.ProviderQueryProgress{TotalQueries: 100, Complete: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(point)
			var copy analysis.MetricPoint
			_ = json.Unmarshal(raw, &copy)
			batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Points: []analysis.MetricPoint{copy}}
			tc.change(&batch)
			if err := server.ingestDynatraceBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err == nil {
				t.Fatal("accepted invalid batch")
			}
		})
	}
}
func TestDynatraceCredentialIdentityAndDraftReadiness(t *testing.T) {
	c := &storage.DynatraceConnection{EnvironmentURL: "https://old.live.dynatrace.com", EnvironmentTokenSecret: "old-env", PlatformURL: "https://old.apps.dynatrace.com", PlatformAuthType: storage.DynatracePlatformAuthOAuth, OAuthResource: "old", OAuthClientIDSecret: "old-id", OAuthClientSecretSecret: "old-secret"}
	blank := ""
	applyDynatraceConnectionRequest(c, DynatraceConnectionRequest{Name: "draft", EnvironmentURL: c.EnvironmentURL, PlatformURL: c.PlatformURL, PlatformAuthType: c.PlatformAuthType, OAuthResource: c.OAuthResource, EnvironmentTokenSecret: &blank, OAuthClientIDSecret: &blank, OAuthClientSecretSecret: &blank})
	if c.EnvironmentTokenSecret != "old-env" || c.OAuthClientSecretSecret != "old-secret" {
		t.Fatal("same-identity blank replacements must preserve references")
	}
	applyDynatraceConnectionRequest(c, DynatraceConnectionRequest{Name: "draft", EnvironmentURL: "https://new.live.dynatrace.com", PlatformURL: "https://new.apps.dynatrace.com", PlatformAuthType: storage.DynatracePlatformAuthOAuth, OAuthResource: "new"})
	if c.EnvironmentTokenSecret != "" || c.OAuthClientIDSecret != "" || c.OAuthClientSecretSecret != "" {
		t.Fatal("credentials followed changed identity")
	}
	server, _ := newTimeslotServer(t)
	created := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitoring/dynatrace-connections", map[string]any{"name": "disabled draft"})
	if created.Code != 201 {
		t.Fatalf("draft %d %s", created.Code, created.Body.String())
	}
	rejected := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitoring/dynatrace-connections", map[string]any{"name": "unverified", "enabled": true})
	if rejected.Code != 409 {
		t.Fatalf("enable %d %s", rejected.Code, rejected.Body.String())
	}
}
func TestDynatraceResolvedVersionSeparatesProjectsAndRotation(t *testing.T) {
	snapshot := &storage.DynatraceConnectionSnapshot{ProjectID: "one", Config: dynatrace.Config{EnvironmentURL: "https://fixture.live.dynatrace.com"}, AllowedCapabilities: dynatrace.Capabilities{Metrics: true}, EnvironmentTokenSecret: "token"}
	value := "first"
	resolver := engine.SecretResolverFunc(func(string) (string, bool) { return value, true })
	first, err := resolveNativeDynatraceCredentials(context.Background(), resolver, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	value = "second"
	rotated, _ := resolveNativeDynatraceCredentials(context.Background(), resolver, snapshot)
	snapshot.ProjectID = "two"
	other, _ := resolveNativeDynatraceCredentials(context.Background(), resolver, snapshot)
	if first.VersionIdentity == rotated.VersionIdentity || other.VersionIdentity == rotated.VersionIdentity {
		t.Fatal("credential cache identity crossed rotation or projects")
	}
}

func TestDynatraceCredentialsOnlyRequireConfiguredCapabilities(t *testing.T) {
	snapshot := &storage.DynatraceConnectionSnapshot{ProjectID: "one", Config: dynatrace.Config{EnvironmentURL: "https://fixture.live.dynatrace.com", PlatformURL: "https://fixture.apps.dynatrace.com", PlatformAuthType: dynatrace.AuthToken}, AllowedCapabilities: dynatrace.Capabilities{Logs: true}, EnvironmentTokenSecret: "unused-environment", PlatformTokenSecret: "platform"}
	requested := []string{}
	resolver := engine.SecretResolverFunc(func(name string) (string, bool) {
		requested = append(requested, name)
		return "fixture-token", name == "platform"
	})
	credentials, err := resolveNativeDynatraceCredentials(t.Context(), resolver, snapshot)
	if err != nil || credentials.PlatformToken == "" || credentials.EnvironmentToken != "" || strings.Join(requested, ",") != "platform" {
		t.Fatalf("independent platform profile: %+v %v; requested %v", credentials, err, requested)
	}
	snapshot.AllowedCapabilities.Metrics = true
	if _, err = resolveNativeDynatraceCredentials(t.Context(), resolver, snapshot); err == nil || strings.Contains(err.Error(), "unused-environment") {
		t.Fatalf("selected missing credential must fail safely: %v", err)
	}
	if dynatraceOperationSelectionAllowed(&dynatrace.Selection{Metrics: []dynatrace.MetricSelection{{Name: "calls"}}}, dynatrace.Capabilities{Logs: true}) {
		t.Fatal("preview exceeded the configured credential/capability profile")
	}
}
func TestDynatraceLegacyNativeBindingConflict(t *testing.T) {
	server, _, job, _ := apiDynatraceNativeFixture(t)
	selected := []storage.MonitorBinding{{SourceID: job.Source.ID}}
	if _, err := server.runner.translateLegacyDynatraceBindings(job.ProjectID, "", &selected, job.Source.ConnectionID); err == nil {
		t.Fatal("ambiguous legacy/native selection accepted")
	}
	empty := []storage.MonitorBinding{}
	if _, err := server.runner.translateLegacyDynatraceBindings(job.ProjectID, "", &empty, job.Source.ConnectionID); err == nil {
		t.Fatal("legacy capture ignored explicit disable")
	}
}

func TestDynatraceNativeFailureCoverageAndCapabilityClassifications(t *testing.T) {
	server, _, job, _ := apiDynatraceNativeFixture(t)
	statuses := []string{"authentication_denied", "permission_denied", "throttled", "query_rejected", "query_budget", "upstream_error", "query_expired", "budget_exhausted", "unreachable"}
	for i, status := range statuses {
		payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(time.Minute), Coverage: map[string]analysis.ProviderCoverage{"metrics:calls": {Status: status, From: job.StartedAt, To: job.StartedAt.Add(time.Minute)}}}
		if err := server.ingestDynatraceBatch(context.Background(), job, int64(i), payload, executionLeaseTerm{}); err != nil {
			t.Fatalf("failure status %s was discarded: %v", status, err)
		}
		caps := sanitizeDynatraceCapabilities(dynatrace.Capabilities{Status: map[string]string{"metrics": status, "metrics:calls": status, "unknown": "unsafe"}})
		if caps.Status["metrics"] != status || caps.Status["metrics:calls"] != status || len(caps.Status) != 2 {
			t.Fatalf("capability classification lost: %+v", caps)
		}
	}
}

func TestDynatraceNativeGrantBindsFrozenIdentity(t *testing.T) {
	_, _, job, _ := apiDynatraceNativeFixture(t)
	job.Source.CollectorID = "assigned-collector"
	job.Source.ExecutorMode = "collector"
	cfg := job.DynatraceSnapshot.Config.Defaults()
	cfg.RequestsPerMinute = max(1, cfg.RequestsPerMinute/2)
	cfg.PlatformRequestsPerMinute = max(1, cfg.PlatformRequestsPerMinute/2)
	selection := *job.Source.Dynatrace
	grant := collectoragent.RunJob{ID: job.ID, ProjectID: job.ProjectID, RunID: job.RunID, ScenarioID: job.ScenarioID, CollectorID: job.Source.CollectorID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, Pack: "dynatrace", Destination: job.Source.Destination, Dynatrace: &dynatrace.JobConfig{Config: cfg, Selection: selection}}
	if !dynatraceGrantMatchesJob(grant, job, job.Source.CollectorID) {
		t.Fatal("frozen assigned grant rejected")
	}
	for _, change := range []func(*collectoragent.RunJob){
		func(g *collectoragent.RunJob) { g.ProjectID = "other-project" },
		func(g *collectoragent.RunJob) { g.CollectorID = "other-collector" },
		func(g *collectoragent.RunJob) { g.SourceRevision++ },
		func(g *collectoragent.RunJob) { g.Dynatrace.Config.EnvironmentURL = "https://other.live.dynatrace.com" },
		func(g *collectoragent.RunJob) { g.Dynatrace.Selection.Problems = false },
	} {
		body, _ := json.Marshal(grant)
		var altered collectoragent.RunJob
		_ = json.Unmarshal(body, &altered)
		change(&altered)
		if dynatraceGrantMatchesJob(altered, job, job.Source.CollectorID) {
			t.Fatal("grant crossed its frozen scope")
		}
	}
}

func TestDynatraceNativePreviewRejectsForeignSeriesAndPrivateProgress(t *testing.T) {
	_, _, job, point := apiDynatraceNativeFixture(t)
	point.RunID = ""
	from, to := point.Provider.BucketStart, point.Provider.BucketEnd
	work := dynatraceOperationWork{Connection: job.DynatraceSnapshot, Selection: job.Source.Dynatrace, PreviewFromUTC: from, PreviewUntilUTC: to}
	preview := dynatrace.Result{Points: []analysis.MetricPoint{point}, Progress: analysis.ProviderQueryProgress{TotalQueries: 2, NextQuery: 2, Complete: true, ScanReservationIDs: []string{"0123456789abcdef0123456789abcdef"}}}
	if err := sanitizeDynatracePreview(work, &preview); err != nil || len(preview.Progress.ScanReservationIDs) != 0 {
		t.Fatalf("safe preview: %+v %v", preview, err)
	}
	preview.Points[0].Provider.MetricID = "foreign"
	if err := sanitizeDynatracePreview(work, &preview); err == nil {
		t.Fatal("preview exposed an unselected series")
	}
}

func TestDynatraceNativeArchiveUsesCanonicalBucketsAndTerminalRules(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	var rule analysis.MonitorRule
	server, _, job, point := apiDynatraceNativeFixture(t, func(_ storage.Store, record *storage.TestRecord, source storage.MonitorSource) {
		metric := source.Dynatrace.Metrics[0]
		rule = analysis.MonitorRule{SourceID: source.ID, Provider: "dynatrace", Backend: "classic", Metric: metric.Name, MetricID: dynatrace.MetricID(metric), Unit: "count", SourceStatistic: "sum", ResolutionSeconds: 60, EvaluationOperation: "sum", Comparator: "lte", Threshold: 5}
		record.GoalConfig = &GoalConfig{MonitorRules: []analysis.MonitorRule{rule}}
	})
	if !job.RulesFrozen || len(job.FrozenMonitorRules) != 1 || job.FrozenMonitorRules[0].Threshold != rule.Threshold {
		t.Fatalf("run-start rule snapshot: %+v", job)
	}
	end := job.StartedAt.Add(2 * time.Minute)
	body, _ := json.Marshal(point)
	var second analysis.MetricPoint
	_ = json.Unmarshal(body, &second)
	second.TimestampUTC = point.TimestampUTC.Add(time.Minute)
	second.Provider.BucketStart = second.TimestampUTC
	second.Provider.BucketEnd = end
	two := 2.0
	second.Value = &two
	batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: end, Points: []analysis.MetricPoint{point, second}}
	if err := server.ingestDynatraceBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	four := 4.0
	point.Value = &four
	batch.Points = []analysis.MetricPoint{point}
	if err := server.ingestDynatraceBatch(context.Background(), job, 1, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	record, _ := server.store.GetTest(job.RunID)
	record.Status, record.EndTime = "COMPLETE", &end
	terminal := server.store.(interface {
		SaveTestWithRunMonitors(*storage.TestRecord, string, string, bool) (bool, error)
	})
	if saved, err := terminal.SaveTestWithRunMonitors(record, "", "", true); err != nil || !saved {
		t.Fatalf("terminal persistence: %v %v", saved, err)
	}
	root := filepath.Join(resolveArchiveRoot(), "fixture")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	archive := &storage.RunArchiveRecord{TestID: job.RunID, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
	// Later manifest edits cannot replace the criteria frozen at run start.
	editedRule := rule
	editedRule.Threshold = 100
	manifest := &RunArchiveManifest{TestID: job.RunID, CreatedAt: job.StartedAt, CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &job.StartedAt, GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{editedRule}}}}
	budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if err := server.runner.persistRunMonitorArchive(job.ProjectID, job.RunID, archive, manifest, budget); err != nil {
		t.Fatal(err)
	}
	if manifest.MonitorEvidenceCount != 2 || len(manifest.MonitorRuleResults) != 1 || manifest.MonitorRuleResults[0].Status != "fail" || manifest.MonitorRuleResults[0].Observed == nil || *manifest.MonitorRuleResults[0].Observed != 6 {
		t.Fatalf("canonical archive/rule: %+v", manifest)
	}
	if verdict := deriveReportVerdict(&RunArchiveReport{Manifest: manifest}); verdict.Status != "Fail" {
		t.Fatalf("verdict: %+v", verdict)
	}
	if !monitoringAcceptanceIncomplete(manifest) {
		t.Fatal("expired required evidence was accepted as complete")
	}
}

func TestDynatraceNativeSyntheticValuesCannotSatisfyRequiredCapture(t *testing.T) {
	_, _, _, point := apiDynatraceNativeFixture(t)
	start, end := point.Provider.BucketStart, point.Provider.BucketEnd
	if !monitorPointSatisfiesCapture(point, start, end) {
		t.Fatal("real measured zero did not satisfy capture")
	}
	point.Provider.Synthetic = true
	point.Provider.AggregationEligible = false
	if monitorPointSatisfiesCapture(point, start, end) {
		t.Fatal("synthetic fill satisfied required numeric evidence")
	}
}

func TestDynatraceNativeRuleIdentityIsFrozenWithGoals(t *testing.T) {
	original := &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: "source", EntityIDs: map[string]string{"dt.entity.service": "SERVICE-1"}, Dimensions: map[string]string{"zone": "production"}}}}
	clone := cloneGoalConfig(original)
	original.MonitorRules[0].EntityIDs["dt.entity.service"] = "SERVICE-2"
	original.MonitorRules[0].Dimensions["zone"] = "other"
	if clone.MonitorRules[0].EntityIDs["dt.entity.service"] != "SERVICE-1" || clone.MonitorRules[0].Dimensions["zone"] != "production" {
		t.Fatal("frozen monitoring criteria shared mutable identity maps")
	}
}

func TestDynatraceNativeW3CCorrelationUsesDurableObservedRunIdentities(t *testing.T) {
	server, store, job, _ := apiDynatraceNativeFixture(t)
	known, remote, unknown := fmt.Sprintf("%032x", 1), fmt.Sprintf("%032x", 2), fmt.Sprintf("%032x", 999)
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Evidence: []analysis.ProviderDiagnostic{
		{Provider: "dynatrace", Backend: "classic", Kind: "problem", ID: "known", TimestampUTC: job.StartedAt, Properties: map[string]string{"trace_id": known, "correlation": "trace_id"}},
		{Provider: "dynatrace", Backend: "classic", Kind: "problem", ID: "unknown", TimestampUTC: job.StartedAt, Properties: map[string]string{"trace_id": unknown, "correlation": "trace_id"}},
	}}
	if err := server.ingestDynatraceBatch(t.Context(), job, 0, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	before, err := loadCanonicalMonitorData(t.Context(), store, job.ProjectID, job.RunID, job.Source.ID, "diagnostic", "", 10)
	if err != nil || len(before.Evidence) != 2 {
		t.Fatalf("initial evidence: %+v %v", before, err)
	}
	for _, evidence := range before.Evidence {
		if evidence.Properties["correlation"] != "entity_time" {
			t.Fatal("collector's unmatched trace claim was trusted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	test := &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, Status: "RUNNING", StartedAt: &job.StartedAt, executionCtx: ctx}
	artifacts := &runArtifacts{runner: server.runner, test: test}
	t.Cleanup(artifacts.stopMonitoring)
	artifacts.startMonitoring(test)
	if !artifacts.nativeDynatrace.Load() || test.DynatraceCapture != nil {
		t.Fatal("native trace capture did not activate independently of legacy capture")
	}
	// A local window's exemplar and an older peer's correlation ID both reach the proof set.
	artifacts.observeMonitoringTraces(&LiveMetrics{Transactions: map[string]*TxMetrics{
		"local":  {Exemplars: []engine.TraceExemplar{{TraceID: known, Reason: engine.ExemplarSample, Success: true}}},
		"remote": {CorrelationIDs: []string{"00-" + remote + "-00f067aa0ba902b7-01"}},
	}})
	artifacts.stopMonitoring() // Final worker flush persists both local and distributed IDs.
	jobs, err := store.ListRunMonitorJobs(job.ProjectID, job.RunID)
	if err != nil || len(jobs) != 1 || len(jobs[0].RunTraceIDs) != 2 {
		t.Fatalf("durable observed identities: %+v %v", jobs, err)
	}
	public, _ := json.Marshal(safeMonitorJob(jobs[0]))
	if strings.Contains(string(public), "run_trace_ids") || strings.Contains(string(public), known) {
		t.Fatal("private run identity sample disclosed in job projection")
	}
	after, err := loadCanonicalMonitorData(t.Context(), store, job.ProjectID, job.RunID, job.Source.ID, "diagnostic", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range after.Evidence {
		want := "entity_time"
		if evidence.ID == "known" {
			want = "trace_id"
		}
		if evidence.Properties["correlation"] != want || evidence.Properties["correlation_sampling"] != "up_to_64_run_observed_trace_ids" {
			t.Fatalf("observed trace matching: %+v", evidence)
		}
	}
	if err := server.ingestDynatraceBatch(t.Context(), job, 0, payload, executionLeaseTerm{}); err != nil {
		t.Fatalf("new trace proof changed an identical batch retry: %v", err)
	}
}

func TestDynatraceNativeSecretsRegisteredAsProjectConsumers(t *testing.T) {
	server, _, job, _ := apiDynatraceNativeFixture(t)
	usage, err := server.projectSecretUsage(job.ProjectID, "protected-environment-ref")
	if err != nil || usage.ConsumerCount != 2 || usage.ReferenceCount != 2 {
		t.Fatalf("consumer: %+v %v", usage, err)
	}
	consumers := map[string]bool{}
	for _, consumer := range usage.Consumers {
		consumers[consumer.ResourceType] = true
	}
	if !consumers["dynatrace_connection"] || !consumers["run_monitor_job"] {
		t.Fatalf("connection and frozen run must both protect their credential: %+v", usage)
	}
	other, err := server.projectSecretUsage("other-project", "protected-environment-ref")
	if err != nil || other.ConsumerCount != 0 {
		t.Fatalf("project isolation: %+v %v", other, err)
	}
}

func TestDynatraceNativeAcceptanceSelectionRequiresExactCatalog(t *testing.T) {
	_, _, job, point := apiDynatraceNativeFixture(t)
	rule := analysis.MonitorRule{SourceID: job.Source.ID, Provider: "dynatrace", Backend: "classic", Metric: point.MetricName, MetricID: point.Provider.MetricID, Unit: point.Unit, SourceStatistic: point.Provider.Statistic, ResolutionSeconds: 60, EvaluationOperation: "sum", Comparator: "lte", Threshold: 10}
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: job.Source.ID}}}
	if err := (&TestRunner{}).validateSelectedMonitorRules(nil, []storage.MonitorSource{job.Source}, &GoalConfig{MonitorRules: []analysis.MonitorRule{rule}}); err == nil {
		t.Fatal("monitoring rule without bindings accepted")
	}
	if err := (&TestRunner{}).validateSelectedMonitorRules(bindings, []storage.MonitorSource{job.Source}, &GoalConfig{MonitorRules: []analysis.MonitorRule{rule}}); err != nil {
		t.Fatal(err)
	}
	undiscovered := job.Source
	selection := *job.Source.Dynatrace
	selection.Metrics = append([]dynatrace.MetricSelection(nil), selection.Metrics...)
	selection.Metrics[0].Unit = ""
	undiscovered.Dynatrace = &selection
	discoveredRule := rule
	discoveredRule.MetricID = dynatrace.MetricID(selection.Metrics[0])
	if err := (&TestRunner{}).validateSelectedMonitorRules(bindings, []storage.MonitorSource{undiscovered}, &GoalConfig{MonitorRules: []analysis.MonitorRule{discoveredRule}}); err != nil {
		t.Fatalf("discovered unit with exact catalog identity: %v", err)
	}
	for _, change := range []func(*analysis.MonitorRule){
		func(r *analysis.MonitorRule) { r.Provider = "" },
		func(r *analysis.MonitorRule) { r.Backend = "grail" },
		func(r *analysis.MonitorRule) { r.MetricID = "foreign" },
		func(r *analysis.MonitorRule) { r.SourceStatistic = "average" },
		func(r *analysis.MonitorRule) { r.ResolutionSeconds = 0 },
	} {
		altered := rule
		change(&altered)
		if err := (&TestRunner{}).validateSelectedMonitorRules(bindings, []storage.MonitorSource{job.Source}, &GoalConfig{MonitorRules: []analysis.MonitorRule{altered}}); err == nil {
			t.Fatal("inexact or mismatched catalog rule accepted")
		}
	}
}

func TestDynatraceNativeComparisonChecksProviderSemantics(t *testing.T) {
	_, _, _, point := apiDynatraceNativeFixture(t)
	if !compatibleNativeMonitorComparison(point, point) {
		t.Fatal("identical series rejected")
	}
	for _, change := range []func(*analysis.MetricPoint){
		func(p *analysis.MetricPoint) { p.Provider.Name = "appdynamics" },
		func(p *analysis.MetricPoint) { p.Provider.Backend = "grail" },
		func(p *analysis.MetricPoint) { p.Provider.MetricID = "other" },
		func(p *analysis.MetricPoint) { p.Provider.Statistic = "time_average" },
		func(p *analysis.MetricPoint) { p.Provider.ResolutionSeconds = 120 },
		func(p *analysis.MetricPoint) { p.Provider.EntityIDs = map[string]string{"dt.entity.service": "other"} },
	} {
		altered := point
		meta := *point.Provider
		altered.Provider = &meta
		change(&altered)
		if compatibleNativeMonitorComparison(point, altered) {
			t.Fatal("different provider series compared as identical")
		}
	}
}

func TestDynatraceNativeVerifiedIdentityCannotEscapeApprovedOrigins(t *testing.T) {
	cfg := dynatrace.Config{EnvironmentURL: "https://fixture.live.dynatrace.com", PlatformURL: "https://fixture.apps.dynatrace.com"}
	trusted := dynatrace.Capabilities{Metrics: true, Logs: true, EnvironmentID: "fixture", PlatformEnvironmentID: "fixture", TenantID: "arbitrary", EnvironmentOrigin: cfg.EnvironmentURL, PlatformOrigin: cfg.PlatformURL, VerifiedOrigins: []string{"https://unapproved.example"}}
	verified := sanitizeDynatraceCapabilitiesForConfig(trusted, cfg)
	if !verified.Metrics || !verified.Logs || verified.TenantID != "fixture" || len(verified.VerifiedOrigins) != 2 {
		t.Fatalf("verified: %+v", verified)
	}
	trusted.EnvironmentID, trusted.PlatformOrigin = "other", "https://other.apps.dynatrace.com"
	rejected := sanitizeDynatraceCapabilitiesForConfig(trusted, cfg)
	if rejected.Metrics || rejected.Logs || rejected.TenantID != "" || len(rejected.VerifiedOrigins) != 0 {
		t.Fatalf("foreign identity accepted: %+v", rejected)
	}
}

func TestDynatraceNativePreparationSharesTenantRequestBudget(t *testing.T) {
	server, _, job, _ := apiDynatraceNativeFixture(t)
	first := job.Source
	selection := *first.Dynatrace
	selection.Metrics = nil
	for i := range 15 {
		selection.Metrics = append(selection.Metrics, dynatrace.MetricSelection{Name: fmt.Sprintf("calls%d", i), MetricSelector: "builtin:service.requestCount.total", Aggregation: "sum", Unit: "count"})
	}
	first.Dynatrace = &selection
	second := first
	second.ID = "another-source"
	connections := server.store.(storage.DynatraceConnectionStore)
	one := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: first.ID, Required: true}}}
	resolver := engine.SecretResolverFunc(func(name string) (string, bool) { return name, true })
	if err := validateDynatraceCollectionBudgets(t.Context(), connections, resolver, job.ProjectID, one, []storage.MonitorSource{first}); err != nil {
		t.Fatal(err)
	}
	optional := first
	optional.ID = "slow-optional"
	optional.IntervalSeconds = 180
	optionalSelection := *optional.Dynatrace
	optionalSelection.Metrics = nil
	optionalSelection.Problems = true
	optionalSelection.SettleSeconds = 300
	optional.Dynatrace = &optionalSelection
	mixed := &storage.MonitorBindings{Sources: append(append([]storage.MonitorBinding(nil), one.Sources...), storage.MonitorBinding{SourceID: optional.ID})}
	if err := validateDynatraceCollectionBudgets(t.Context(), connections, resolver, job.ProjectID, mixed, []storage.MonitorSource{first, optional}); err != nil {
		t.Fatalf("optional settling policy blocked an otherwise feasible required source: %v", err)
	}
	both := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: first.ID, Required: true}, {SourceID: second.ID, Required: true}}}
	if err := validateDynatraceCollectionBudgets(t.Context(), connections, resolver, job.ProjectID, both, []storage.MonitorSource{first, second}); err == nil {
		t.Fatal("required sources independently bypassed the shared tenant budget")
	}
	for i := range both.Sources {
		both.Sources[i].Required = false
	}
	if err := validateDynatraceCollectionBudgets(t.Context(), connections, resolver, job.ProjectID, both, []storage.MonitorSource{first, second}); err != nil {
		t.Fatalf("optional capture blocked load: %v", err)
	}
	if err := validateDynatraceCollectionBudgets(t.Context(), connections, resolver, job.ProjectID, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDynatraceNativeDiscoveryContinuationIsPrivateAndScoped(t *testing.T) {
	server, _, job, _ := apiDynatraceNativeFixture(t)
	store := server.store.(storage.DynatraceOperationStore)
	connection, err := server.store.(storage.DynatraceConnectionStore).GetDynatraceConnection(job.Source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	options := dynatrace.DiscoveryRequest{Kind: "metrics", Search: "calls", Limit: 100}
	privateCursor := "private-provider-query-token"
	create := func(id string, created time.Time) *storage.DynatraceOperation {
		t.Helper()
		work, _ := json.Marshal(dynatraceOperationWork{Connection: connection.Snapshot(), Options: options})
		op := &storage.DynatraceOperation{ID: id, ProjectID: connection.ProjectID, ConnectionID: connection.ID, ConnectionRevision: connection.Revision, Kind: "discover", PayloadJSON: string(work), CreatedAt: created, ExpiresAt: created.Add(time.Minute)}
		if err := store.CreateDynatraceOperation(op); err != nil {
			t.Fatal(err)
		}
		if claimed, err := store.ClaimDynatraceOperation(id, "fixture", created, created.Add(time.Minute)); err != nil || !claimed {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		body, _ := json.Marshal(dynatraceOperationResult{NextCursor: privateCursor, Truncated: true, Preview: &dynatrace.Result{Progress: analysis.ProviderQueryProgress{Cursor: privateCursor, ScanReservationIDs: []string{privateCursor}}}})
		if committed, err := store.CompleteDynatraceOperation(id, "fixture", string(body), "", created.Add(time.Second)); err != nil || !committed {
			t.Fatalf("complete: %v %v", committed, err)
		}
		op, err = store.GetDynatraceOperation(id)
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	op := create("fixture-discovery", time.Now().UTC().Add(-2*time.Second))
	response := dynatraceOperationResponse(op)
	if strings.Contains(string(response.Result), privateCursor) {
		t.Fatal("provider continuation disclosed")
	}
	var public dynatraceOperationResult
	if err := json.Unmarshal(response.Result, &public); err != nil || public.NextCursor != "dynatrace-page:"+op.ID {
		t.Fatalf("opaque page handle: %+v %v", public, err)
	}
	next := options
	next.Cursor = public.NextCursor
	if err := resolveDynatraceDiscoveryCursor(store, connection, &next); err != nil || next.Cursor != privateCursor {
		t.Fatalf("scoped continuation: %q %v", next.Cursor, err)
	}
	for _, change := range []func(*storage.DynatraceConnection, *dynatrace.DiscoveryRequest){
		func(c *storage.DynatraceConnection, _ *dynatrace.DiscoveryRequest) { c.ProjectID = "other" },
		func(c *storage.DynatraceConnection, _ *dynatrace.DiscoveryRequest) { c.Revision++ },
		func(c *storage.DynatraceConnection, _ *dynatrace.DiscoveryRequest) { c.CollectorAgentID = "other" },
		func(_ *storage.DynatraceConnection, o *dynatrace.DiscoveryRequest) { o.Search = "other" },
		func(_ *storage.DynatraceConnection, o *dynatrace.DiscoveryRequest) { o.Cursor = privateCursor },
	} {
		copyConnection, copyOptions := *connection, options
		copyOptions.Cursor = public.NextCursor
		change(&copyConnection, &copyOptions)
		if err := resolveDynatraceDiscoveryCursor(store, &copyConnection, &copyOptions); err == nil {
			t.Fatal("out-of-scope continuation accepted")
		}
	}
	expired := create("expired-discovery", time.Now().UTC().Add(-6*time.Minute))
	next.Cursor = "dynatrace-page:" + expired.ID
	if err := resolveDynatraceDiscoveryCursor(store, connection, &next); err == nil {
		t.Fatal("expired continuation accepted")
	}
	if strings.Contains(string(dynatraceOperationResponse(expired).Result), "next_cursor") {
		t.Fatal("expired continuation published")
	}
}
