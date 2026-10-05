//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDynatracePostgresRunTracePersistenceAndOperationTermFence(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("dynatrace_final_fences_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	storeURL := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	store, err := NewPostgresStore(storeURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	_, source, start := seedDynatraceMonitorFixture(t, store.SQLiteStore)
	const runID = "pg-trace-persist"
	if err := store.SaveTest(&TestRecord{ID: runID, ProjectID: source.ProjectID, Name: "PG trace persistence", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}}); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors(source.ProjectID, runID, "", start, start.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	ids := []string{strings.Repeat("A", 32), fmt.Sprintf("%032x", 1)}
	if ok, err := store.RecordDynatraceRunTraceIDsFenced(source.ProjectID, runID, start, ids, "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(storeURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	want := []string{strings.ToLower(ids[0]), ids[1]}
	if err != nil || !slices.Equal(job.RunTraceIDs, want) || job.NextSequence != 0 || len(job.Coverage) != 0 || len(job.QueryCoverage) != 0 || job.ProviderCursor != "" {
		t.Fatal("PostgreSQL restart changed trace attribution or query state", job, err)
	}
	checkDynatracePostgresTraceRowLockDeadlines(t, store.SQLiteStore, source.ProjectID, runID, start, jobs[0].ID)
	more := make([]string, MaxDynatraceRunTraceIDs)
	for i := range more {
		more[i] = fmt.Sprintf("%032x", i+2)
	}
	if ok, err := store.RecordDynatraceRunTraceIDsFenced(source.ProjectID, runID, start, more, "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	job, err = store.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || len(job.RunTraceIDs) != MaxDynatraceRunTraceIDs || !slices.Equal(job.RunTraceIDs[:2], want) || slices.Contains(job.RunTraceIDs, more[62]) {
		t.Fatal("PostgreSQL trace sampling exceeded its first-observed bound", job, err)
	}
	checkDynatraceRunTraceIDsRequireRunScopeAndExecutionTerm(t, store.SQLiteStore, source)
	checkDynatraceOperationAndHealthRejectSupersededExecutionTerm(t, store.SQLiteStore, source)
}

func checkDynatracePostgresTraceRowLockDeadlines(t *testing.T, s *SQLiteStore, project, run string, start time.Time, jobID string) {
	t.Helper()
	const lease = "trace-deadline-lease"
	now := time.Now().UTC()
	ok, term, err := s.TryAcquireSchedulerLeaseTerm(lease, "trace-deadline-owner", now, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	before, err := s.GetRunMonitorJob(project, jobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []struct {
		name, query, identity string
		fenced                bool
	}{
		{"execution lease", `SELECT name FROM scheduler_leases WHERE name=? FOR UPDATE`, lease, true},
		{"project", `SELECT id FROM projects WHERE id=? FOR UPDATE`, project, false},
		{"test incarnation", `SELECT id FROM tests WHERE id=? FOR UPDATE`, run, false},
		{"job write", `SELECT id FROM run_monitor_jobs WHERE id=? FOR UPDATE`, jobID, false},
	} {
		t.Run(stage.name, func(t *testing.T) {
			blocker, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			var locked string
			if err := blocker.QueryRow(stage.query, stage.identity).Scan(&locked); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			leaseName, owner, generation := "", "", uint64(0)
			if stage.fenced {
				leaseName, owner, generation = lease, "trace-deadline-owner", term.Generation
			}
			began := time.Now()
			ok, err := s.RecordDynatraceRunTraceIDsFencedContext(ctx, project, run, start, []string{fmt.Sprintf("%032x", 1000)}, leaseName, owner, generation)
			if ok || err == nil || ctx.Err() != context.DeadlineExceeded || time.Since(began) > 2*time.Second {
				t.Fatal("trace storage ignored a lock deadline", ok, err, ctx.Err(), time.Since(began))
			}
			if err := blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetRunMonitorJob(project, jobID)
			if err != nil || !slices.Equal(current.RunTraceIDs, before.RunTraceIDs) || current.NextSequence != before.NextSequence {
				t.Fatal("cancelled trace transaction persisted attribution", current, err)
			}
		})
	}
}
