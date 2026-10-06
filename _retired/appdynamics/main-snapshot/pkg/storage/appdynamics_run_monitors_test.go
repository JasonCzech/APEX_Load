package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"testing"
	"time"
)

func appDynamicsMonitorFixture(t *testing.T) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	s, _, now := monitorFixture(t)
	start := now.Add(-10 * time.Minute).Truncate(time.Minute)
	c := &AppDynamicsConnection{ID: "appd", ProjectID: "monitor-p", Name: "APM", Enabled: true, CollectionMode: "direct", Config: appdynamics.Config{Endpoint: "https://controller.example", AccountName: "account", AuthType: "basic"}, UsernameSecretName: "username", PasswordSecretName: "password"}
	if err := s.CreateAppDynamicsConnection(c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAppDynamicsConnectionHealth(c.ID, c.ProjectID, c.Revision, "connected", "", appdynamics.Capabilities{Metrics: true}, now); err != nil {
		t.Fatal(err)
	}
	source := MonitorSource{ID: "apm-source", ProjectID: c.ProjectID, Name: "APM", Pack: "appdynamics", ExecutorMode: "direct", ConnectionID: c.ID, IntervalSeconds: 60, Enabled: true, AppDynamics: &appdynamics.Selection{ProjectID: c.ProjectID, Applications: []string{"1"}, Metrics: []appdynamics.MetricSelection{{ApplicationID: "1", Path: "Overall Application Performance|Calls per Minute", Name: "calls", Unit: "count", Statistic: "sum"}}}}
	saved, err := s.SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	source = *saved
	if err := s.SaveTest(&TestRecord{ID: "monitor-run", Name: "run", ProjectID: "monitor-p", ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}}); err != nil {
		t.Fatal(err)
	}
	return s, source, start
}
func providerTestPoint(source MonitorSource, start time.Time, value float64) analysis.MetricPoint {
	return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: source.ProjectID, RunID: "monitor-run", ScenarioID: "monitor-scenario", Source: source.ID, TimestampUTC: start, MetricName: "calls", Unit: "count", Kind: analysis.KindGauge, Value: &value, Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "1", EntityIDs: map[string]string{"application": "1"}, BucketStart: start, BucketEnd: start.Add(time.Minute), ResolutionSeconds: 60, Statistic: "sum", Settled: true, AggregationEligible: true}}
}
func providerReceipt(t *testing.T, job string, sequence int64, payload ProviderMonitorBatch, at time.Time) MonitorBatchReceipt {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	return MonitorBatchReceipt{JobID: job, Sequence: sequence, Digest: hex.EncodeToString(hash[:]), ArtifactKey: "fixture", SampleCount: len(payload.Points), ReceivedAt: at}
}

func TestAppDynamicsSourceRequiresCollectionCapability(t *testing.T) {
	_, source, _ := appDynamicsMonitorFixture(t)
	for _, kind := range []string{"empty", "metrics", "events", "violations", "snapshots"} {
		t.Run(kind, func(t *testing.T) {
			candidate := source
			selection := *source.AppDynamics
			selection.Metrics = nil
			candidate.AppDynamics = &selection
			switch kind {
			case "metrics":
				selection.Metrics = source.AppDynamics.Metrics
			case "events":
				selection.Events = true
			case "violations":
				selection.Violations = true
			case "snapshots":
				selection.Snapshots = true
			}
			if err := candidate.Validate(); (err != nil) != (kind == "empty") {
				t.Fatalf("selection %s: %v", kind, err)
			}
		})
	}
}

func TestAppDynamicsStableReconciliationUsesPersistedAnchor(t *testing.T) {
	s, source, start := appDynamicsMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(5 * time.Minute)
	record, _ := s.GetTest("monitor-run")
	record.Status, record.EndTime = "COMPLETE", &end
	if _, err := s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	progress := appdynamics.QueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true}
	payload := ProviderMonitorBatch{Points: []analysis.MetricPoint{providerTestPoint(source, start, 0)}, Progress: &progress, QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Final: true, Coverage: map[string]appdynamics.Coverage{"metrics:0": {Status: "complete", From: start, To: end}}}
	for i := 0; i < 5; i++ {
		at := end.Add(time.Duration(181+15*i) * time.Second)
		if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, int64(i), payload, at), payload, "", "", 0); err != nil {
			t.Fatal(err)
		}
		job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if i < 4 && job.State != "reconciling" {
			t.Fatalf("closed before stable interval: %+v", job)
		}
		if i == 4 && (job.State != "stopped" || job.StablePasses < 2) {
			t.Fatalf("never closed after frequent successful passes: %+v", job)
		}
	}
}
func TestAppDynamicsCanonicalRevisionsAndDrain(t *testing.T) {
	s, source, start := appDynamicsMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("start %+v %v", jobs, err)
	}
	job := jobs[0]
	if job.Source.CollectorID != "" || job.ConnectionSnapshot == nil {
		t.Fatal("direct run needs immutable connection without artificial collector")
	}
	payload := ProviderMonitorBatch{Points: []analysis.MetricPoint{providerTestPoint(source, start, 0)}}
	receipt := providerReceipt(t, job.ID, 0, payload, start.Add(time.Minute))
	for range 2 {
		if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, payload, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	payload.Points[0] = providerTestPoint(source, start, 9)
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 0, payload, start.Add(time.Minute)), payload, "", "", 0); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 1, payload, start.Add(2*time.Minute)), payload, "", "", 0); err != nil {
		t.Fatal(err)
	}
	heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "point", "", 10)
	if err != nil || len(heads) != 1 || heads[0].Sequence != 1 {
		t.Fatalf("heads %+v %v", heads, err)
	}
	receipts, err := s.ListMonitorBatchReceipts(source.ProjectID, "monitor-run", "", -1, 10)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("immutable receipts %+v %v", receipts, err)
	}
	end := start.Add(5 * time.Minute)
	record, _ := s.GetTest("monitor-run")
	record.Status = "COMPLETED"
	record.EndTime = &end
	if _, err = s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	drained, _ := s.GetRunMonitorJob(source.ProjectID, job.ID)
	if drained.State != "reconciling" || drained.DrainDeadline == nil || !drained.DrainDeadline.Equal(end.Add(5*time.Minute)) {
		t.Fatalf("drain %+v", drained)
	}
	if err = s.CloseExpiredProviderMonitors(source.ProjectID, "monitor-run", end.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	drained, _ = s.GetRunMonitorJob(source.ProjectID, job.ID)
	if drained.State != "incomplete" {
		t.Fatal(drained.State)
	}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 2, payload, end.Add(6*time.Minute)), payload, "", "", 0); err == nil {
		t.Fatal("closed job accepted late revision")
	}
}
func TestAppDynamicsBindingOverridePersistence(t *testing.T) {
	s, source, start := appDynamicsMonitorFixture(t)
	if _, err := s.SaveMonitorBindings(MonitorBindings{ProjectID: source.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: source.ID}}}, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		bindings *MonitorBindings
		want     int
	}{{"inherit", nil, 1}, {"disabled", &MonitorBindings{Sources: []MonitorBinding{}}, 0}, {"replace", &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			run := "run-" + tc.name
			if err := s.SaveTest(&TestRecord{ID: run, Name: run, ProjectID: source.ProjectID, ScenarioID: "monitor-scenario", StartTime: start, Status: "QUEUED", MonitorBindings: tc.bindings}); err != nil {
				t.Fatal(err)
			}
			got, err := s.ResolveRunMonitorBindings(source.ProjectID, run, "monitor-scenario")
			if err != nil || len(got.Sources) != tc.want {
				t.Fatalf("resolved %+v %v", got, err)
			}
			saved, err := s.GetTest(run)
			if err != nil || (saved.MonitorBindings == nil) != (tc.bindings == nil) {
				t.Fatalf("round trip %+v %v", saved, err)
			}
		})
	}
}
func TestAppDynamicsQueryCursorOnlyCommitsWithReceipt(t *testing.T) {
	s, source, start := appDynamicsMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job := jobs[0]
	until := start.Add(3 * time.Minute)
	batch := ProviderMonitorBatch{Progress: &appdynamics.QueryProgress{NextQuery: 1, TotalQueries: 2}, QueryFromUTC: start, QueryUntilUTC: until, Coverage: map[string]appdynamics.Coverage{"metrics:0": {Status: "complete", From: start, To: until}}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 0, batch, until), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetRunMonitorJob(source.ProjectID, job.ID)
	if got.QueryOffset != 1 || !got.QueryWatermark.IsZero() {
		t.Fatalf("partial cursor %+v", got)
	}
	batch.Progress = &appdynamics.QueryProgress{NextQuery: 2, TotalQueries: 2, Complete: true}
	batch.QueryWindowEnd = &until
	batch.Coverage = map[string]appdynamics.Coverage{"metrics:1": {Status: "complete", From: start, To: until}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 1, batch, until), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetRunMonitorJob(source.ProjectID, job.ID)
	if got.QueryOffset != 0 || !got.QueryWatermark.Equal(until) || len(got.Coverage) != 2 {
		t.Fatalf("complete cursor %+v", got)
	}
}
