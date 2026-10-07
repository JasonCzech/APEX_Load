package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestElasticRetiredCleanupBytesAreBoundedAtomically(t *testing.T) {
	now := time.Now().UTC()
	var job RunMonitorJob
	for i := range 2 {
		cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: fmt.Sprintf("%d-", i) + strings.Repeat("\x01", 16380), WindowFrom: now.Add(-time.Minute), WindowUntil: now, KeepAliveUntil: now.Add(time.Hour), ResolvedIndices: []string{"logs-private"}, SearchAfter: []analysis.ScalarValue{{Kind: "string", StringValue: "private-sort"}}}
		if err := retainElasticCleanupContinuations(&job, []*analysis.ProviderContinuation{cursor}, cursor.WindowFrom, cursor.WindowUntil, now); err != nil {
			t.Fatal("valid escaped maximum handle rejected", err)
		}
	}
	before, _ := json.Marshal(job.ElasticRetiredContinuations)
	if len(before) > maxElasticCleanupBytes || len(job.ElasticRetiredContinuations) != 2 || len(job.ElasticRetiredContinuations[0].SearchAfter) != 0 || len(job.ElasticRetiredContinuations[0].ResolvedIndices) != 0 {
		t.Fatal("private cleanup exceeded its byte ceiling or retained pagination state")
	}
	third := *job.ElasticRetiredContinuations[0]
	third.PITID = "new-" + strings.Repeat("\x01", 16380)
	if err := retainElasticCleanupContinuations(&job, []*analysis.ProviderContinuation{&third}, third.WindowFrom, third.WindowUntil, now); err == nil {
		t.Fatal("oversized accumulated cleanup ownership accepted")
	}
	after, _ := json.Marshal(job.ElasticRetiredContinuations)
	if string(after) != string(before) {
		t.Fatal("rejected cleanup changed previously retained ownership")
	}
	if err := retainElasticCleanupContinuations(&job, []*analysis.ProviderContinuation{job.ElasticRetiredContinuations[0]}, third.WindowFrom, third.WindowUntil, now); err != nil {
		t.Fatal("identical ownership retry consumed additional bytes", err)
	}
}

func TestElasticCleanupBackpressureAndExactAcknowledgementResume(t *testing.T) {
	s, source, start := elasticMonitorFixture(t)
	assertElasticCleanupBackpressureAndExactAcknowledgementResume(t, s, source, start)
}

func assertElasticCleanupBackpressureAndExactAcknowledgementResume(t *testing.T, s *SQLiteStore, source MonitorSource, start time.Time) {
	t.Helper()
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(time.Minute)
	cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "private-failed-close", WindowFrom: start, WindowUntil: end, KeepAliveUntil: time.Now().UTC().Add(time.Hour)}
	batch := ProviderMonitorBatch{CleanupContinuations: []*analysis.ProviderContinuation{cursor}, QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Progress: &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Coverage: map[string]analysis.ProviderCoverage{"logs": {QueryID: "logs", Status: "empty", From: start, To: end}}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, batch, end), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	if direct, err := s.PollDirectRunMonitorJobs(time.Now().UTC()); err != nil || len(direct) != 0 {
		t.Fatalf("direct work continued while cleanup was pending: %+v %v", direct, err)
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise both collector polling contracts against the same accepted,
	// suspended job. Routing changes here do not issue upstream work.
	job.Source.CollectorID, job.Source.ExecutorMode = "monitor-collector", "collector"
	body, _ := json.Marshal(job)
	if _, err = s.db.Exec(`UPDATE run_monitor_jobs SET collector_id=?,payload=? WHERE id=?`, job.Source.CollectorID, string(body), job.ID); err != nil {
		t.Fatal(err)
	}
	if collected, err := s.PollRunMonitorJobs(source.ProjectID, job.Source.CollectorID, time.Now().UTC()); err != nil || len(collected) != 0 {
		t.Fatalf("legacy collector work continued while cleanup was pending: %+v %v", collected, err)
	}
	if collected, more, err := s.PollRunMonitorJobPage(source.ProjectID, job.Source.CollectorID, "", 1, time.Now().UTC()); err != nil || len(collected) != 0 || more {
		t.Fatalf("cursor page assigned suspended work or an unadvanceable empty page: %+v %v %v", collected, more, err)
	}
	wrong := *cursor
	wrong.PITID = "different-handle"
	if ok, err := s.ClearElasticContinuation(source.ProjectID, job.ID, &wrong, "", "", 0); err != nil || ok {
		t.Fatal("incorrect acknowledgement released backpressure", ok, err)
	}
	if ok, err := s.ClearElasticContinuation(source.ProjectID, job.ID, cursor, "", "", 0); err != nil || !ok {
		t.Fatal("exact acknowledgement did not release backpressure", ok, err)
	}
	if collected, more, err := s.PollRunMonitorJobPage(source.ProjectID, job.Source.CollectorID, "", 1, time.Now().UTC()); err != nil || len(collected) != 1 || more || collected[0].Coverage["logs"].Status != "empty" || collected[0].NextSequence != 1 {
		t.Fatalf("acknowledgement failed to resume unchanged query coverage: %+v %v %v", collected, more, err)
	}
	if collected, err := s.PollRunMonitorJobs(source.ProjectID, job.Source.CollectorID, time.Now().UTC()); err != nil || len(collected) != 1 {
		t.Fatalf("legacy collector failed to resume: %+v %v", collected, err)
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, job.ID)
	job.Source.CollectorID, job.Source.ExecutorMode = "", "direct"
	body, _ = json.Marshal(job)
	if _, err = s.db.Exec(`UPDATE run_monitor_jobs SET collector_id='',payload=? WHERE id=?`, string(body), job.ID); err != nil {
		t.Fatal(err)
	}
	if direct, err := s.PollDirectRunMonitorJobs(time.Now().UTC()); err != nil || len(direct) != 1 {
		t.Fatalf("direct work failed to resume: %+v %v", direct, err)
	}
}

func TestElasticFailedCloseOwnershipDoesNotDowngradeCompleteFreeze(t *testing.T) {
	s, source, start := elasticMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(time.Minute)
	run, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	run.Status, run.EndTime = "COMPLETE", &end
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "failed-close-owned-pit", WindowFrom: start, WindowUntil: end, KeepAliveUntil: time.Now().UTC().Add(time.Minute)}
	batch := ProviderMonitorBatch{CleanupContinuations: []*analysis.ProviderContinuation{cursor}, QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Final: true, Progress: &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Coverage: map[string]analysis.ProviderCoverage{"logs": {QueryID: "logs", Status: "empty", From: start, To: end}}}
	for i := range 2 {
		at := end.Add(time.Duration(65+i*15) * time.Second)
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, int64(i), batch, at), batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	final, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || final.State != "stopped" || len(final.ElasticRetiredContinuations) != 1 || final.ElasticContinuation != nil || final.Coverage["logs"].Status != "empty" {
		t.Fatalf("cleanup ownership changed truthful coverage or was lost: %+v err=%v", final, err)
	}
	cleanup, _, err := s.ListElasticContinuationCleanupJobs(source.ProjectID, run.ID, "", 100)
	if err != nil || len(cleanup) != 1 || cleanup[0].ElasticContinuation.PITID != cursor.PITID {
		t.Fatalf("complete job's owned handle is not recoverable: %+v err=%v", cleanup, err)
	}
	if ok, err := s.ClearElasticContinuation(source.ProjectID, final.ID, cursor, "", "", 0); err != nil || !ok {
		t.Fatalf("acknowledge completed cleanup: %v %v", ok, err)
	}
	final, err = s.GetRunMonitorJob(source.ProjectID, final.ID)
	if err != nil || final.State != "stopped" || len(final.ElasticRetiredContinuations) != 0 {
		t.Fatalf("cleanup acknowledgement changed final acceptance: %+v err=%v", final, err)
	}
}

func TestElasticContinuationCleanupCancellationDeadlineAndRetirement(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "retired"} {
		t.Run(mode, func(t *testing.T) {
			s, m, start := elasticMonitorFixture(t)
			jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			job := jobs[0]
			until := start.Add(time.Minute)
			cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "private-pit", WindowFrom: start, WindowUntil: until}
			batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderProgress{TotalQueries: 1, Continuation: cursor}}
			if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 0, batch, until), batch, "", "", 0); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.ClearElasticContinuation(m.ProjectID, job.ID, cursor, "", "", 0); err != nil || ok {
				t.Fatal("active current cursor was cleared", ok, err)
			}
			if mode == "cancel" {
				err = s.StopRunMonitors(m.ProjectID, job.RunID, until)
			} else {
				run, _ := s.GetTest(job.RunID)
				run.Status = "COMPLETE"
				run.EndTime = &until
				_, err = s.SaveTestWithRunMonitors(run, "", "", true)
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "deadline" {
				if err = s.CloseExpiredProviderMonitors(m.ProjectID, job.RunID, until.Add(301*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			cleanup, next, err := s.ListElasticContinuationCleanupJobs(m.ProjectID, job.RunID, "", 100)
			if err != nil || len(cleanup) != 1 || next != "" || cleanup[0].ElasticContinuation.PITID != cursor.PITID {
				t.Fatalf("cleanup %+v %s %v", cleanup, next, err)
			}
			if mode == "retired" {
				if cleanup[0].State != "reconciling" {
					t.Fatal("retired cursor unavailable before terminal freeze")
				}
			}
			wrong := *cursor
			wrong.PITID = "newer-pit"
			if ok, err := s.ClearElasticContinuation(m.ProjectID, job.ID, &wrong, "", "", 0); err != nil || ok {
				t.Fatal("different cursor cleared", ok, err)
			}
			now := time.Now().UTC()
			oldOK, old, err := s.TryAcquireSchedulerLeaseTerm("elastic-cleanup", "old", now, time.Minute)
			if err != nil || !oldOK {
				t.Fatal(err)
			}
			if err = s.ReleaseSchedulerLease("elastic-cleanup", "old"); err != nil {
				t.Fatal(err)
			}
			newOK, newTerm, err := s.TryAcquireSchedulerLeaseTerm("elastic-cleanup", "new", now, time.Minute)
			if err != nil || !newOK {
				t.Fatal(err)
			}
			if ok, err := s.ClearElasticContinuation(m.ProjectID, job.ID, cursor, "elastic-cleanup", "old", old.Generation); err != nil || ok {
				t.Fatal("former execution owner cleared cursor", ok, err)
			}
			if ok, err := s.ClearElasticContinuation(m.ProjectID, job.ID, cursor, "elastic-cleanup", "new", newTerm.Generation); err != nil || !ok {
				t.Fatal("current execution owner failed cleanup", ok, err)
			}
			if again, _, err := s.ListElasticContinuationCleanupJobs(m.ProjectID, job.RunID, "", 100); err != nil || len(again) != 0 {
				t.Fatalf("cleanup remained %+v %v", again, err)
			}
			frozen, _ := s.GetRunMonitorJob(m.ProjectID, job.ID)
			if frozen.ElasticConnectionSnapshot == nil || frozen.ElasticConnectionSnapshot.APIKeySecretName == "" {
				t.Fatal("immutable connection snapshot lost during cleanup")
			}
		})
	}
}

func TestElasticTerminalSecretConsumersUntilOwnedCleanup(t *testing.T) {
	for _, mode := range []string{"current", "retired", "expired", "none"} {
		t.Run(mode, func(t *testing.T) {
			s, m, start := elasticMonitorFixture(t)
			jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			until := start.Add(time.Minute)
			cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "owned-pit", WindowFrom: start, WindowUntil: until, KeepAliveUntil: time.Now().UTC().Add(time.Minute)}
			if mode == "expired" {
				cursor.KeepAliveUntil = time.Now().UTC().Add(-time.Second)
			}
			batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderProgress{TotalQueries: 1, Continuation: cursor}}
			if mode == "none" {
				batch.Progress = &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}
			}
			if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, batch, until), batch, "", "", 0); err != nil {
				t.Fatal(err)
			}
			if mode == "retired" {
				run, _ := s.GetTest(jobs[0].RunID)
				run.Status = "COMPLETE"
				run.EndTime = &until
				if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
					t.Fatal(err)
				}
				err = s.CloseExpiredProviderMonitors(m.ProjectID, jobs[0].RunID, until.Add(301*time.Second))
			} else {
				err = s.StopRunMonitors(m.ProjectID, jobs[0].RunID, until)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.DeleteMonitorSource(m.ProjectID, m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.DeleteELKConnection(m.ConnectionID, m.ProjectID); err != nil || !ok {
				t.Fatal("terminal connection removal", ok, err)
			}
			consumers, err := s.ListElasticSecretConsumers(m.ProjectID, "api-key")
			want := 0
			if mode == "current" || mode == "retired" {
				want = 1
			}
			if err != nil || len(consumers) != want || want == 1 && consumers[0].ResourceType != "run_monitor_job" {
				t.Fatalf("terminal consumers %+v %v", consumers, err)
			}
			if mode != "none" {
				if ok, err := s.ClearElasticContinuation(m.ProjectID, jobs[0].ID, cursor, "", "", 0); err != nil || !ok {
					t.Fatal("cleanup acknowledgement", ok, err)
				}
			}
			consumers, err = s.ListElasticSecretConsumers(m.ProjectID, "api-key")
			if err != nil || len(consumers) != 0 {
				t.Fatalf("historical snapshot blocked credential deletion %+v %v", consumers, err)
			}
		})
	}
}

func TestElasticCleanupCredentialOwnershipAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name   string
		cursor *analysis.ProviderContinuation
		want   bool
	}{
		{"none", nil, false},
		{"aggregation only", &analysis.ProviderContinuation{CompositeAfter: map[string]analysis.ScalarValue{"key": {Kind: "string", StringValue: "next"}}}, false},
		{"unknown expiry", &analysis.ProviderContinuation{PITID: "owned"}, true},
		{"async owned", &analysis.ProviderContinuation{AsyncID: "owned", KeepAliveUntil: now.Add(time.Second)}, true},
		{"expired", &analysis.ProviderContinuation{PITID: "owned", KeepAliveUntil: now}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, job := range []RunMonitorJob{{ElasticContinuation: test.cursor}, {ElasticRetiredContinuations: []*analysis.ProviderContinuation{test.cursor}}} {
				if got := elasticJobNeedsCleanupCredentials(job, now); got != test.want {
					t.Fatalf("cleanup credential need %v, want %v", got, test.want)
				}
			}
		})
	}
}
