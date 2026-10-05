package storage

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"testing"
	"time"
)

func TestDynatracePendingWindowAndFencedCancellationSurviveTerminality(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := start.Add(2 * time.Minute)
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{dynatracePoint(source, start, 1)}, QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderQueryProgress{NextQuery: 0, TotalQueries: 1, Cursor: "private-pending-cursor"}}
	acceptDynatraceFixture(t, s, source, 0, batch, until)
	run, _ := s.GetTest("monitor-run")
	end := until.Add(time.Minute)
	run.Status = "COMPLETE"
	run.EndTime = &end
	if _, err := s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || job.ProviderCursor != batch.Progress.Cursor || !job.QueryUntilUTC.Equal(until) || job.State != "reconciling" {
		t.Fatal(job, err)
	}
	if pending, err := s.PollDynatraceCancellationJobs(end, 2); err != nil || len(pending) != 0 {
		t.Fatal("active reconciliation was cancelled", pending, err)
	}
	if err := s.CloseExpiredProviderMonitors(source.ProjectID, "monitor-run", end.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PollDynatraceCancellationJobs(end.Add(6*time.Minute), 2)
	if err != nil || len(pending) != 1 || pending[0].ProviderCursor != batch.Progress.Cursor {
		t.Fatal(pending, err)
	}
	now := time.Now().UTC()
	const lease = "dynatrace-cancellation-owner"
	ok, old, err := s.TryAcquireSchedulerLeaseTerm(lease, "old", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.ReleaseSchedulerLease(lease, "old"); err != nil {
		t.Fatal(err)
	}
	ok, current, err := s.TryAcquireSchedulerLeaseTerm(lease, "new", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := s.ClearDynatraceCancellationCursorFenced(source.ProjectID, job.ID, batch.Progress.Cursor, lease, "old", old.Generation); err == nil {
		t.Fatal("stale owner cleared cleanup cursor")
	}
	if ok, err := s.ClearDynatraceCancellationCursorFenced(source.ProjectID, job.ID, "different-cursor", lease, "new", current.Generation); err != nil || ok {
		t.Fatal("stale cursor CAS cleared current token", ok, err)
	}
	if ok, err := s.ClearDynatraceCancellationCursorFenced(source.ProjectID, job.ID, batch.Progress.Cursor, lease, "new", current.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if pending, err := s.PollDynatraceCancellationJobs(end.Add(6*time.Minute), 2); err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "point", "", 10)
	if err != nil || len(heads) != 1 {
		t.Fatal("cleanup altered canonical evidence", heads, err)
	}
}

func TestDynatraceSingleControllerCancellationClearsOnlyTerminalCursor(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := start.Add(time.Minute)
	const cursor = "private-single-controller-cursor"
	batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderQueryProgress{NextQuery: 0, TotalQueries: 1, Cursor: cursor}}
	acceptDynatraceFixture(t, s, source, 0, batch, until)
	if ok, err := s.ClearDynatraceCancellationCursorFenced(source.ProjectID, jobs[0].ID, cursor, "", "", 0); err != nil || ok {
		t.Fatal("active collection cursor was cleared", ok, err)
	}
	if err := s.StopRunMonitors(source.ProjectID, "monitor-run", until); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClearDynatraceCancellationCursorFenced(source.ProjectID, jobs[0].ID, "different-cursor", "", "", 0); err != nil || ok {
		t.Fatal("single-controller cleanup bypassed cursor CAS", ok, err)
	}
	if ok, err := s.ClearDynatraceCancellationCursorFenced(source.ProjectID, jobs[0].ID, cursor, "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || job.ProviderCursor != "" || job.State != "stopped" {
		t.Fatal(job, err)
	}
}
