package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDynatraceRunTraceIDsRespectCancelledContext(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := s.RecordDynatraceRunTraceIDsFencedContext(ctx, source.ProjectID, "monitor-run", start, []string{fmt.Sprintf("%032x", 1)}, "", "", 0); ok || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled trace worker mutated storage", ok, err)
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || len(job.RunTraceIDs) != 0 {
		t.Fatal(job, err)
	}
}

func TestDynatraceRunTraceIDsPersistBoundedUnionAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-traces.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, source, start := seedDynatraceMonitorFixture(t, s)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Repeat("A", 32)
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, "monitor-run", start, []string{first, strings.ToLower(first)}, "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	ids := make([]string, MaxDynatraceRunTraceIDs)
	for i := range ids {
		ids[i] = fmt.Sprintf("%032x", i+1)
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, "monitor-run", start, ids, "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	before, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || len(before.RunTraceIDs) != MaxDynatraceRunTraceIDs || before.RunTraceIDs[0] != strings.ToLower(first) || before.RunTraceIDs[63] != ids[62] || slices.Contains(before.RunTraceIDs, ids[63]) {
		t.Fatal(before, err)
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, "monitor-run", start, []string{fmt.Sprintf("%032x", 1000)}, "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, "monitor-run", start, append(ids, fmt.Sprintf("%032x", 1001)), "", "", 0); err == nil {
		t.Fatal("unbounded incoming trace identifiers accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || !slices.Equal(before.RunTraceIDs, after.RunTraceIDs) || after.NextSequence != 0 || len(after.Coverage) != 0 || len(after.QueryCoverage) != 0 || after.ProviderCursor != "" {
		t.Fatal("trace correlation changed provider query state", after, err)
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, "monitor-run", start, ids[:1], "", "", 0); err != nil || !ok {
		t.Fatal("idempotent sampled trace replay rejected", ok, err)
	}
	// The storage serializer preserves private attribution; its base/public
	// projection omits the field without changing any provider batch contract.
	private, err := json.Marshal(after)
	if err != nil || !strings.Contains(string(private), "run_trace_ids") {
		t.Fatal(string(private), err)
	}
	public, err := json.Marshal(monitorJobJSON(*after))
	if err != nil || strings.Contains(string(public), "run_trace_ids") || strings.Contains(string(public), strings.ToLower(first)) {
		t.Fatal("public contract includes private run trace sample", string(public), err)
	}
	end := start.Add(2 * time.Minute)
	run, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	run.Status, run.EndTime = "COMPLETE", &end
	if _, err := s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, "monitor-run", start, ids[:1], "", "", 0); err != nil || !ok {
		t.Fatal("reconciling run cannot receive its late trace attribution", ok, err)
	}
	if err := s.CloseExpiredProviderMonitors(source.ProjectID, "monitor-run", end.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, "monitor-run", start, ids[:1], "", "", 0); err != nil || ok {
		t.Fatal("frozen terminal job accepted trace attribution", ok, err)
	}
}

func TestDynatraceRunTraceIDsRequireRunScopeAndExecutionTerm(t *testing.T) {
	s, source, _ := dynatraceMonitorFixture(t)
	checkDynatraceRunTraceIDsRequireRunScopeAndExecutionTerm(t, s, source)
}

func checkDynatraceRunTraceIDsRequireRunScopeAndExecutionTerm(t *testing.T, s *SQLiteStore, source MonitorSource) {
	t.Helper()
	now := time.Now().UTC()
	generic := MonitorSource{ID: "generic-trace-control", ProjectID: source.ProjectID, Name: "Host", Pack: "linux-hosts", CollectorID: "monitor-collector", IntervalSeconds: 10, Enabled: true}
	if _, err := s.SaveMonitorSource(generic, 0); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	run.StartTime, run.StartedAt = now, &now
	run.MonitorBindings.Sources = append(run.MonitorBindings.Sources, MonitorBinding{SourceID: generic.ID})
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, run.ID, "monitor-scenario", now, now.Add(time.Hour))
	if err != nil || len(jobs) != 2 {
		t.Fatal(jobs, err)
	}
	other := &TestRecord{ID: "trace-other-run", ProjectID: source.ProjectID, Name: "Other", Status: "RUNNING", StartTime: now, StartedAt: &now, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}}
	if err := s.SaveTest(other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRunMonitors(source.ProjectID, other.ID, "", now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	const lease = "dynatrace-run-trace-owner"
	ok, old, err := s.TryAcquireSchedulerLeaseTerm(lease, "old-owner", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.ReleaseSchedulerLease(lease, "old-owner"); err != nil {
		t.Fatal(err)
	}
	ok, fresh, err := s.TryAcquireSchedulerLeaseTerm(lease, "fresh-owner", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ids := []string{fmt.Sprintf("%032x", 1)}
	if _, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, run.ID, now, ids, lease, "old-owner", old.Generation); err == nil {
		t.Fatal("superseded executor recorded run trace attribution")
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced("monitor-other", run.ID, now, ids, lease, "fresh-owner", fresh.Generation); err != nil || ok {
		t.Fatal("trace identifiers crossed project ownership", ok, err)
	}
	if _, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, run.ID, now.Add(time.Second), ids, lease, "fresh-owner", fresh.Generation); err == nil {
		t.Fatal("wrong run incarnation recorded trace attribution")
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, run.ID, now, ids, lease, "fresh-owner", fresh.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, job := range jobs {
		current, err := s.GetRunMonitorJob(source.ProjectID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Source.Pack == "dynatrace" && !slices.Equal(current.RunTraceIDs, ids) || current.Source.Pack != "dynatrace" && len(current.RunTraceIDs) != 0 {
			t.Fatal("trace sample was not restricted to native Dynatrace jobs", current)
		}
	}
	separate, err := s.GetRunMonitorJob(source.ProjectID, other.ID+":"+source.ID)
	if err != nil || len(separate.RunTraceIDs) != 0 {
		t.Fatal("trace sample leaked to another run", separate, err)
	}
	for _, id := range []string{"", "0", strings.Repeat("0", 32), strings.Repeat("x", 32), strings.Repeat("1", 31), strings.Repeat("1", 33), "\n" + strings.Repeat("1", 31)} {
		if _, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, run.ID, now, []string{id}, lease, "fresh-owner", fresh.Generation); err == nil {
			t.Fatal("invalid W3C identifier accepted", id)
		}
	}
	if ok, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, run.ID, now, nil, lease, "fresh-owner", fresh.Generation); err != nil || ok {
		t.Fatal("empty sample should not mutate a job", ok, err)
	}
	// A rerun can keep the same test ID and execution owner, but an old
	// runtime worker must never attribute its identifiers to that incarnation.
	restarted := now.Add(time.Minute)
	if _, err := s.db.Exec(`UPDATE tests SET started_at=? WHERE id=? AND project_id=?`, restarted, run.ID, source.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, run.ID, now, ids, lease, "fresh-owner", fresh.Generation); err == nil {
		t.Fatal("old runtime worker crossed a rerun incarnation")
	}
	if _, err := s.RecordDynatraceRunTraceIDsFenced(source.ProjectID, run.ID, restarted, ids, lease, "fresh-owner", fresh.Generation); err == nil {
		t.Fatal("old monitor job accepted a different test incarnation")
	}
}
