package api

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type countingListTestStore struct {
	storage.Store
	listCalls          atomic.Int32
	getCalls           atomic.Int32
	historyCalls       atomic.Int32
	historyLimitCalls  atomic.Int32
	runArchiveGetCalls atomic.Int32
}

func (s *countingListTestStore) ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error) {
	s.listCalls.Add(1)
	return s.Store.ListTests(projectID, folderID)
}

func (s *countingListTestStore) GetTest(id string) (*storage.TestRecord, error) {
	s.getCalls.Add(1)
	return s.Store.GetTest(id)
}

func (s *countingListTestStore) GetHistory(testID string) ([]any, error) {
	s.historyCalls.Add(1)
	return s.Store.GetHistory(testID)
}

func (s *countingListTestStore) GetHistoryLimit(testID string, limit int) ([]any, error) {
	s.historyLimitCalls.Add(1)
	if limited, ok := s.Store.(interface {
		GetHistoryLimit(string, int) ([]any, error)
	}); ok {
		return limited.GetHistoryLimit(testID, limit)
	}
	return s.Store.GetHistory(testID)
}

func (s *countingListTestStore) GetRunArchive(testID string) (*storage.RunArchiveRecord, error) {
	s.runArchiveGetCalls.Add(1)
	return s.Store.GetRunArchive(testID)
}

func (s *countingListTestStore) resetReadCounts() {
	s.listCalls.Store(0)
	s.getCalls.Store(0)
	s.historyCalls.Store(0)
	s.historyLimitCalls.Store(0)
	s.runArchiveGetCalls.Store(0)
}

func TestFollowerReadBurstCollapsesDurableTestRefresh(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	store := &countingListTestStore{Store: base}
	if err := store.SaveTest(&storage.TestRecord{
		ID:          "persisted-test",
		Name:        "Persisted test",
		Status:      "COMPLETE",
		ProjectID:   storage.DefaultProjectID,
		Duration:    int64(time.Minute),
		StartTime:   time.Now().UTC(),
		TotalVUsers: 1,
		Groups:      []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	follower := newExecutionLeadership(store, "test-follower")
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(follower))
	store.resetReadCounts() // Ignore constructor hydration.

	const readers = 32
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			_ = runner.ListTests()
		}()
	}
	wait.Wait()

	if got := store.listCalls.Load(); got != 1 {
		t.Fatalf("follower read burst performed %d durable test scans, want 1", got)
	}
	if got := store.getCalls.Load(); got != 0 {
		t.Fatalf("follower read burst performed %d per-row test reads, want 0", got)
	}
	if got := store.historyCalls.Load() + store.historyLimitCalls.Load(); got != 0 {
		t.Fatalf("follower read burst reloaded unchanged history %d times, want 0", got)
	}
	if got := store.runArchiveGetCalls.Load(); got != 0 {
		t.Fatalf("follower read burst reloaded unchanged archives %d times, want 0", got)
	}
	// Leadership recovery is a forced reconciliation and must not inherit the
	// follower-read throttle.
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reloadStoredTests: %v", err)
	}
	if got := store.listCalls.Load(); got != 2 {
		t.Fatalf("forced reload left durable test scans at %d, want 2", got)
	}
	if got := store.getCalls.Load(); got != 0 {
		t.Fatalf("forced reload performed %d per-row test reads, want 0", got)
	}
	if got := store.historyCalls.Load() + store.historyLimitCalls.Load(); got != 0 {
		t.Fatalf("forced reload reloaded unchanged history %d times, want 0", got)
	}
	if got := store.runArchiveGetCalls.Load(); got != 0 {
		t.Fatalf("forced reload reloaded unchanged archives %d times, want 0", got)
	}
}

func TestHASingleTestReadRejectsStaleCrossProjectRuntimeHandle(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	store := &countingListTestStore{Store: base}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.SaveProject(&storage.Project{
		ID: "project-b", Name: "Project B", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	const testID = "reused-ha-test-id"
	if err := store.SaveTest(&storage.TestRecord{
		ID:          testID,
		Name:        "Current project B",
		Status:      "COMPLETE",
		ProjectID:   "project-b",
		Duration:    int64(time.Minute),
		StartTime:   now,
		TotalVUsers: 1,
		Groups:      []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	follower := newExecutionLeadership(store, "test-follower")
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(follower))
	staleCtx, staleCancel := context.WithCancel(context.Background())
	defer staleCancel()
	stale := &ManagedTest{
		ID:          testID,
		Name:        "Prior project A",
		Status:      "COMPLETE",
		ProjectID:   "project-a",
		Duration:    time.Minute,
		DurationStr: "1m0s",
		CreatedAt:   now.Add(-time.Hour),
		History: []*LiveMetrics{{
			Timestamp: now.Add(-time.Hour),
		}},
		executionCtx: staleCtx,
		cancel:       staleCancel,
	}
	runner.mu.Lock()
	runner.tests[testID] = stale
	runner.mu.Unlock()
	// Keep the full-map follower refresh inside its intended bounded staleness
	// window. The single-row identity check must still prevent stale serialization.
	runner.haTestRefresh.markFresh(time.Now())
	store.resetReadCounts()

	full, ok := runner.GetTest(testID)
	if !ok {
		t.Fatal("GetTest did not return the durable identity")
	}
	if full.ProjectID != "project-b" || full.Name != "Current project B" {
		t.Fatalf("GetTest returned stale identity: project=%q name=%q", full.ProjectID, full.Name)
	}
	if len(full.History) != 0 {
		t.Fatalf("GetTest disclosed %d stale history points", len(full.History))
	}
	summary, ok := runner.GetTestSummary(testID)
	if !ok {
		t.Fatal("GetTestSummary did not return the durable identity")
	}
	if summary.ProjectID != "project-b" || summary.Name != "Current project B" {
		t.Fatalf("GetTestSummary returned stale identity: project=%q name=%q", summary.ProjectID, summary.Name)
	}
	if got := store.getCalls.Load(); got != 2 {
		t.Fatalf("durable single-row identity reads = %d, want 2", got)
	}

	runner.mu.RLock()
	retained := runner.tests[testID]
	runner.mu.RUnlock()
	if retained != stale || retained.cancel == nil {
		t.Fatal("stale local runtime handle was detached before leadership-loss teardown")
	}
}

func TestHARunArchiveReadRejectsStaleCrossProjectManifest(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	store := &countingListTestStore{Store: base}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.SaveProject(&storage.Project{
		ID: "project-b", Name: "Project B", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	const testID = "reused-ha-archive-id"
	if err := store.SaveTest(&storage.TestRecord{
		ID: testID, Name: "Current project B", Status: "COMPLETE", ProjectID: "project-b",
		Duration: int64(time.Minute), StartTime: now, TotalVUsers: 1, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	archiveDir := filepath.Join(t.TempDir(), "stale-project-a-archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifestPath := filepath.Join(archiveDir, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion,
		TestID:        testID,
		TestName:      "Prior project A",
		CreatedAt:     now.Add(-time.Hour),
		Provenance: RunArchiveProvenance{
			ProjectID:   "project-a",
			RequestedAt: now.Add(-time.Hour),
		},
	}); err != nil {
		t.Fatalf("write stale manifest: %v", err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveSchemaVersion,
		ArchiveBackend: archiveBackendFilesystem, ArchivePath: archiveDir, ManifestPath: manifestPath,
		CreatedAt: now.Add(-time.Hour), CompletedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveRunArchive: %v", err)
	}

	follower := newExecutionLeadership(store, "archive-follower")
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(follower))
	runner.haTestRefresh.markFresh(time.Now())
	if record, ok := runner.GetRunArchiveContext(context.Background(), testID); ok || record != nil {
		t.Fatalf("stale cross-project archive was returned: %+v", record)
	}
	runner.mu.RLock()
	_, cached := runner.archives[testID]
	current := runner.tests[testID]
	runner.mu.RUnlock()
	if cached || (current != nil && current.Archive != nil) {
		t.Fatal("rejected stale archive remained attached to the HA replica cache")
	}
}
