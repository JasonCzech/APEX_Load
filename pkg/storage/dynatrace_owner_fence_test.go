package storage

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"testing"
	"time"
)

func TestDynatraceDurableOwnerFenceAndScan(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	checkDynatraceDurableOwnerFenceAndScan(t, s, source, start)
}
func checkDynatraceDurableOwnerFenceAndScan(t *testing.T, s *SQLiteStore, source MonitorSource, start time.Time) {
	t.Helper()
	now := time.Now().UTC()
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	const name = "dynatrace-owner-fence"
	ok, old, err := s.TryAcquireSchedulerLeaseTerm(name, "old", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.ReserveDynatraceScanFenced(source.ProjectID, jobs[0].ID, "old-execute", 1_000_000_000, now, name, "old", old.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.ReleaseSchedulerLease(name, "old"); err != nil {
		t.Fatal(err)
	}
	ok, fresh, err := s.TryAcquireSchedulerLeaseTerm(name, "new", now, time.Minute)
	if err != nil || !ok || fresh.Generation <= old.Generation {
		t.Fatal(ok, err)
	}
	if _, err := s.ReserveDynatraceScanFenced(source.ProjectID, jobs[0].ID, "stale-execute", 1_000_000_000, now, name, "old", old.Generation); err == nil {
		t.Fatal("stale owner admitted billed query")
	}
	if ok, err := s.ReserveDynatraceScanFenced(source.ProjectID, jobs[0].ID, "new-execute", 1_000_000_000, now, name, "new", fresh.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{dynatracePoint(source, start, 1)}}
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, batch, start.Add(time.Minute)), batch, name, "old", old.Generation); err == nil {
		t.Fatal("stale owner committed telemetry")
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || job.NextSequence != 0 || job.ScanReservedBytes != 2_000_000_000 {
		t.Fatal(job, err)
	}
}
