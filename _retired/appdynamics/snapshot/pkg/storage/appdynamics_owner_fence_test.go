package storage

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"testing"
	"time"
)

func TestAppDynamicsDurableOwnerFence(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/owner-fence.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkAppDynamicsDurableOwnerFence(t, s)
}
func checkAppDynamicsDurableOwnerFence(t *testing.T, s *SQLiteStore) {
	t.Helper()
	_, _, now := seedMonitorFixture(t, s)
	start := now.Add(-10 * time.Minute).Truncate(time.Minute)
	c := &AppDynamicsConnection{ID: "appd-fenced", ProjectID: "monitor-p", Name: "APM", Enabled: true, CollectionMode: "direct", Config: appdynamics.Config{Endpoint: "https://controller.example", AccountName: "account", AuthType: "basic"}, UsernameSecretName: "username", PasswordSecretName: "password"}
	if err := s.CreateAppDynamicsConnection(c); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateAppDynamicsConnectionHealth(c.ID, c.ProjectID, 1, "connected", "", appdynamics.Capabilities{Metrics: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	source := MonitorSource{ID: "apm-fenced-source", ProjectID: c.ProjectID, Name: "APM", Pack: "appdynamics", ExecutorMode: "direct", ConnectionID: c.ID, IntervalSeconds: 60, Enabled: true, AppDynamics: &appdynamics.Selection{ProjectID: c.ProjectID, Applications: []string{"1"}, Metrics: []appdynamics.MetricSelection{{ApplicationID: "1", Path: "calls", Name: "calls", Unit: "count", Statistic: "sum"}, {ApplicationID: "1", Path: "errors", Name: "errors", Unit: "count", Statistic: "sum"}}}}
	saved, err := s.SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	source = *saved
	if err := s.SaveTest(&TestRecord{ID: "monitor-run", Name: "run", ProjectID: source.ProjectID, ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	job := jobs[0]
	const leaseName = "appdynamics-test-owner"
	acquired, oldTerm, err := s.TryAcquireSchedulerLeaseTerm(leaseName, "owner-old", now, time.Minute)
	if err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	until := start.Add(2 * time.Minute)
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{providerTestPoint(source, start, 0)}, Progress: &appdynamics.QueryProgress{NextQuery: 1, TotalQueries: 2}, QueryFromUTC: start, QueryUntilUTC: until, Coverage: map[string]appdynamics.Coverage{"metrics:0": {Status: "complete", From: start, To: until}}}
	receipt := providerReceipt(t, job.ID, 0, batch, until)
	for range 2 {
		if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, batch, leaseName, "owner-old", oldTerm.Generation); err != nil {
			t.Fatal(err)
		}
	}
	cursor, err := s.GetRunMonitorJob(source.ProjectID, job.ID)
	if err != nil || cursor.QueryOffset != 1 || !cursor.QueryWatermark.IsZero() || cursor.NextSequence != 1 {
		t.Fatal(cursor, err)
	}
	batch.Points[0] = providerTestPoint(source, start, 9)
	batch.Progress = &appdynamics.QueryProgress{NextQuery: 2, TotalQueries: 2, Complete: true}
	batch.QueryWindowEnd = &until
	batch.Coverage = map[string]appdynamics.Coverage{"metrics:1": {Status: "complete", From: start, To: until}}
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 1, batch, until), batch, leaseName, "owner-old", oldTerm.Generation); err != nil {
		t.Fatal(err)
	}
	cursor, err = s.GetRunMonitorJob(source.ProjectID, job.ID)
	if err != nil || cursor.QueryOffset != 0 || !cursor.QueryWatermark.Equal(until) || cursor.NextSequence != 2 {
		t.Fatal(cursor, err)
	}
	heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "point", "", 10)
	if err != nil || len(heads) != 1 || heads[0].Sequence != 1 {
		t.Fatal(heads, err)
	}
	if err := s.ReleaseSchedulerLease(leaseName, "owner-old"); err != nil {
		t.Fatal(err)
	}
	acquired, newTerm, err := s.TryAcquireSchedulerLeaseTerm(leaseName, "owner-new", time.Now().UTC(), time.Minute)
	if err != nil || !acquired || newTerm.Generation <= oldTerm.Generation {
		t.Fatal(newTerm, err)
	}
	final := ProviderMonitorBatch{Points: []analysis.MetricPoint{providerTestPoint(source, start, 10)}}
	finalReceipt := providerReceipt(t, job.ID, 2, final, until)
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", finalReceipt, final, leaseName, "owner-old", oldTerm.Generation); err == nil {
		t.Fatal("stale execution owner committed telemetry")
	}
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", finalReceipt, final, leaseName, "owner-new", newTerm.Generation); err != nil {
		t.Fatal(err)
	}
	receipts, err := s.ListMonitorBatchReceipts(source.ProjectID, "monitor-run", job.ID, -1, 10)
	if err != nil || len(receipts) != 3 {
		t.Fatal(receipts, err)
	}
}
