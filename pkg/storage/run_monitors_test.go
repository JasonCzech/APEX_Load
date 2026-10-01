package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func monitorFixture(t *testing.T) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "monitors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return seedMonitorFixture(t, s)
}

func seedMonitorFixture(t *testing.T, s *SQLiteStore) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	requireProjectFixtures(t, s, "monitor-p", "monitor-other")
	now := time.Now().UTC()
	for _, err := range []error{
		s.SaveScript(&dsl.Script{ID: "monitor-script", Name: "script", Protocol: "http", ProjectID: "monitor-p", CreatedAt: now}),
		s.SaveScenario(&Scenario{ID: "monitor-scenario", Name: "scenario", Duration: "1m", CreatedAt: now}, []ScenarioScript{{ScriptID: "monitor-script"}}),
		s.SaveTest(&TestRecord{ID: "monitor-run", Name: "run", ProjectID: "monitor-p", ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: now}),
		s.SaveMonitoringCollector(&MonitoringCollector{ID: "monitor-collector", Name: "collector", ProjectID: "monitor-p", TokenHash: "test", Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"linux-hosts", "prometheus"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	m := MonitorSource{ID: "monitor-source", Name: "host", ProjectID: "monitor-p", CollectorID: "monitor-collector", Pack: "linux-hosts", IntervalSeconds: 10, Enabled: true}
	if err := s.SaveCollectorRunReadiness(m.ProjectID, m.CollectorID, []string{"linux-hosts", "prometheus"}, nil, now); err != nil {
		t.Fatal(err)
	}
	return s, m, now
}

func TestRunMonitorRequiresCollectorJobReadiness(t *testing.T) {
	s, m, now := monitorFixture(t)
	if _, err := s.SaveMonitorSource(m, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMonitorBindings(MonitorBindings{ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: m.ID, Required: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM run_monitor_collector_readiness`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour)); err == nil {
		t.Fatal("legacy heartbeat counted as signed job readiness")
	}
	if err := s.SaveCollectorRunReadiness(m.ProjectID, m.CollectorID, []string{"go-runtime"}, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour)); err == nil {
		t.Fatal("unavailable pack counted as ready")
	}
}

func TestMonitorSourcesRevisionAndProjectIsolation(t *testing.T) {
	s, m, _ := monitorFixture(t)
	created, err := s.SaveMonitorSource(m, 0)
	if err != nil || created.Revision != 1 {
		t.Fatalf("create: %+v %v", created, err)
	}
	if _, err := s.SaveMonitorSource(m, 0); !errors.Is(err, ErrMonitorRevision) {
		t.Fatalf("duplicate: %v", err)
	}
	m.ProjectID = "monitor-other"
	if _, err := s.SaveMonitorSource(m, 1); err == nil {
		t.Fatal("moved source across projects")
	}
	m.ProjectID = "monitor-p"
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.SaveMonitorSource(m, 1); results <- err }()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrMonitorRevision) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("CAS winners %d conflicts %d", success, conflicts)
	}
	if list, err := s.ListMonitorSources("monitor-other"); err != nil || len(list) != 0 {
		t.Fatalf("project leak: %+v %v", list, err)
	}
	if err := s.DeleteMonitorSource("monitor-p", m.ID, 1); !errors.Is(err, ErrMonitorRevision) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := s.DeleteMonitorSource("monitor-p", m.ID, 2); err != nil {
		t.Fatal(err)
	}
}

func TestRunMonitorSnapshotReadinessAndDurableBatchJournal(t *testing.T) {
	s, m, now := monitorFixture(t)
	if _, err := s.SaveMonitorSource(m, 0); err != nil {
		t.Fatal(err)
	}
	b := MonitorBindings{ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: m.ID, Required: true}}}
	if _, err := s.SaveMonitorBindings(b, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRunMonitors(m.ProjectID, "monitor-run", b.ScenarioID, now.Add(2*time.Minute), now.Add(time.Hour)); err == nil {
		t.Fatal("stale required collector admitted")
	}
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", b.ScenarioID, now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("start: %+v %v", jobs, err)
	}
	m.Name = "edited"
	m.Enabled = false
	if _, err := s.SaveMonitorSource(m, 1); err != nil {
		t.Fatal(err)
	}
	again, err := s.StartRunMonitors(m.ProjectID, "monitor-run", b.ScenarioID, now, now.Add(time.Hour))
	if err != nil || len(again) != 1 || again[0].Source.Name != "host" || again[0].Source.Revision != 1 {
		t.Fatalf("snapshot changed: %+v %v", again, err)
	}
	b.Sources = append(b.Sources, b.Sources[0])
	if _, err := s.SaveMonitorBindings(b, 1); err == nil {
		t.Fatal("duplicate binding accepted")
	}
	receipt := MonitorBatchReceipt{JobID: jobs[0].ID, Sequence: 0, Digest: strings.Repeat("a", 64), ArtifactKey: "immutable/batch", SampleCount: 2, ReceivedAt: now.Add(time.Second)}
	if err := s.AcceptMonitorBatch("monitor-other", m.CollectorID, receipt); err == nil {
		t.Fatal("cross-project batch")
	}
	if err := s.AcceptMonitorBatch(m.ProjectID, "other", receipt); err == nil {
		t.Fatal("wrong collector batch")
	}
	gap := receipt
	gap.Sequence = 1
	if err := s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, gap); err == nil {
		t.Fatal("gap accepted")
	}
	if err := s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, receipt); err != nil {
		t.Fatalf("retry: %v", err)
	}
	conflict := receipt
	conflict.Digest = strings.Repeat("b", 64)
	if err := s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, conflict); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	if err := s.StopRunMonitors(m.ProjectID, "monitor-run", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, receipt); err != nil {
		t.Fatalf("accepted retry after stop: %v", err)
	}
	if err := s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, gap); err == nil {
		t.Fatal("new batch after stop accepted")
	}
	got, err := s.GetRunMonitorJob(m.ProjectID, jobs[0].ID)
	if err != nil || got.NextSequence != 1 || got.State != "stopped" {
		t.Fatalf("job: %+v %v", got, err)
	}
	page, err := s.ListMonitorBatchReceipts(m.ProjectID, "monitor-run", "", -1, 10)
	if err != nil || len(page) != 1 || page[0].Digest != receipt.Digest {
		t.Fatalf("receipts: %+v %v", page, err)
	}
	if got, err := s.PollRunMonitorJobs(m.ProjectID, m.CollectorID, now); err != nil || len(got) != 0 {
		t.Fatalf("stopped poll: %+v %v", got, err)
	}
}

func TestMonitorSourceValidation(t *testing.T) {
	_, m, _ := monitorFixture(t)
	for _, u := range []string{"http://user:secret@host/metrics", "http://host/metrics?token=secret", "file:///tmp/data", "http://host/#secret"} {
		bad := m
		bad.Pack = "prometheus"
		bad.Destination = u
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	for _, pack := range []string{"ssh", "jmx", "unknown"} {
		bad := m
		bad.Pack = pack
		if bad.Validate() == nil {
			t.Fatalf("advertised unsupported %s", pack)
		}
	}
}

func TestMonitorPackRejectsUnusedCredentials(t *testing.T) {
	_, source, _ := monitorFixture(t)
	for _, pack := range []string{"linux-hosts", "go-runtime", "otel-metrics", "prometheus"} {
		source.Pack = pack
		source.CredentialRef = "unused"
		if err := source.Validate(); err == nil {
			t.Fatalf("%s accepted ignored credentials", pack)
		}
	}
}

func TestCloudMonitorSourcesRequireBoundedPollingAndAlias(t *testing.T) {
	for _, pack := range []string{"cloudwatch", "azure-monitor"} {
		source := MonitorSource{ID: "source", ProjectID: "project", Name: "cloud", CollectorID: "collector", Pack: pack, Destination: pack + "://approved", IntervalSeconds: 60}
		if err := source.Validate(); err != nil {
			t.Fatal(err)
		}
		source.IntervalSeconds = 1
		if source.Validate() == nil {
			t.Fatal("cloud polling below minimum accepted")
		}
		source.IntervalSeconds = 60
		source.Destination += ":443"
		if source.Validate() == nil {
			t.Fatal("cloud alias accepted a port")
		}
	}
}
