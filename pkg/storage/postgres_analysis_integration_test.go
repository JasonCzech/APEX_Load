//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresAnalysisWorkflows(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("apexload_analysis_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+quotePostgresIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+quotePostgresIdentifier(schema)+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	dsn := postgresIntegrationURLWithSearchPath(t, url, schema)
	pg, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if pg != nil {
			pg.Close()
		}
	}()
	s, m, now := seedMonitorFixture(t, pg.SQLiteStore)
	if _, err = s.SaveMonitorSource(m, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveMonitorBindings(MonitorBindings{ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: m.ID, Required: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	run := &TestRecord{ID: "prepared-pg", Name: "prepared", ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Status: "QUEUED", StartTime: now}
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveHistory(run.ID, now, map[string]any{"timestamp": now, "native_evidence": &NativeIntervalEvidence{Version: 1, WindowSeconds: 1, Dimensions: map[string]map[string]NativeIntervalMetric{"action": {"Checkout": {Count: 2, P95: 25}}}}}); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePreparation(Preparation{ID: "prep-pg", ProjectID: m.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "pg-request", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "INITIALIZING"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	p, err = s.TransitionPreparation(p.ID, "preparing", "ready", p.Revision, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := s.ConsumePreparation(p.ID, p.Revision, "", "starter", "Starter"); results <- err })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrPreparationConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent starts: %d", winners)
	}
	jobs, err := s.StartRunMonitors(m.ProjectID, run.ID, run.ScenarioID, now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	receipt := MonitorBatchReceipt{JobID: jobs[0].ID, Sequence: 0, Digest: strings.Repeat("b", 64), ArtifactKey: "immutable/batch", SampleCount: 1, ReceivedAt: now.Add(time.Second)}
	if err = s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, receipt); err == nil {
		t.Fatal("accepted evidence before execution committed")
	}
	run.Status = "RUNNING"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, receipt); err != nil {
			t.Fatal(err)
		}
	}
	dataset, err := s.SaveAnalysisImport(AnalysisImport{ID: "dataset-pg", ProjectID: m.ProjectID, Name: "data", State: "uploading", Files: []AnalysisImportFile{{ID: "file", RelativePath: "p/file.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"mapped", "normalizing", "ready"} {
		dataset.State = state
		dataset, err = s.SaveAnalysisImport(*dataset, dataset.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	definition, err := s.SaveAnalysisDefinition(AnalysisDefinition{ID: "mapping-pg", ProjectID: m.ProjectID, Kind: "mapping", Name: "first", Definition: json.RawMessage(`{"version":1}`)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	definition.Name = "second"
	if _, err = s.SaveAnalysisDefinition(*definition, definition.Revision); err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("c", 64)
	if err = s.EnsureAnalysisStorageIdentity(identity); err != nil {
		t.Fatal(err)
	}
	if err = pg.Close(); err != nil {
		t.Fatal(err)
	}
	pg, err = NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	s = pg.SQLiteStore
	history, err := s.GetHistoryLimit(run.ID, 1)
	if err != nil || len(history) != 1 {
		t.Fatalf("native history after reopen: %v %v", history, err)
	}
	evidence := history[0].(IntervalSummaryRecord).NativeEvidence
	if evidence == nil || evidence.Dimensions["action"]["Checkout"].P95 != 25 {
		t.Fatalf("native evidence lost: %+v", evidence)
	}
	if err = s.EnsureAnalysisStorageIdentity(identity); err != nil {
		t.Fatal(err)
	}
	receipts, err := s.ListMonitorBatchReceipts(m.ProjectID, run.ID, "", -1, 10)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt recovery: %+v %v", receipts, err)
	}
	if p, err = s.GetPreparation(p.ID); err != nil || p.State != "consumed" {
		t.Fatalf("preparation recovery: %+v %v", p, err)
	}
	if old, err := s.GetAnalysisDefinition(m.ProjectID, definition.ID, 1); err != nil || old.Name != "first" {
		t.Fatalf("mapping history: %+v %v", old, err)
	}
	if err = s.DeleteProject(m.ProjectID); err != nil {
		t.Fatal(err)
	}
	cleanup, err := s.ListAnalysisCleanupJobs(now.Add(time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, job := range cleanup {
		found[job.Kind] = true
	}
	if !found["dataset"] || !found["monitor"] {
		t.Fatalf("deletion lost durable cleanup: %+v", cleanup)
	}
}
