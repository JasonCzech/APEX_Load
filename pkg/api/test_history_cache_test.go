package api

import (
	"sync"
	"testing"
	"time"
)

type blockingTerminalHistoryStore struct {
	mu      sync.Mutex
	history []any
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type delayedSaveTerminalHistoryStore struct {
	mu          sync.Mutex
	history     []any
	saveStarted chan struct{}
	saveRelease chan struct{}
	getCalls    int
}

type invalidatedInFlightTerminalHistoryStore struct {
	mu        sync.Mutex
	old       []any
	current   []any
	started   chan struct{}
	release   chan struct{}
	firstOnce sync.Once
	getCalls  int
}

func (s *invalidatedInFlightTerminalHistoryStore) SaveHistory(string, time.Time, any) error {
	return nil
}

func (s *invalidatedInFlightTerminalHistoryStore) GetHistory(testID string) ([]any, error) {
	return s.GetHistoryLimit(testID, 0)
}

func (s *invalidatedInFlightTerminalHistoryStore) GetHistoryLimit(string, int) ([]any, error) {
	s.mu.Lock()
	s.getCalls++
	first := s.getCalls == 1
	history := s.current
	if first {
		history = s.old
	}
	snapshot := append([]any(nil), history...)
	s.mu.Unlock()
	if first {
		s.firstOnce.Do(func() { close(s.started) })
		<-s.release
	}
	return snapshot, nil
}

func (s *delayedSaveTerminalHistoryStore) SaveHistory(_ string, _ time.Time, metrics any) error {
	close(s.saveStarted)
	<-s.saveRelease
	live := metrics.(*LiveMetrics)
	s.mu.Lock()
	s.history = append(s.history, map[string]any{"timestamp": live.Timestamp, "total_transactions": float64(live.TotalTx)})
	s.mu.Unlock()
	return nil
}

func (s *delayedSaveTerminalHistoryStore) GetHistory(string) ([]any, error) {
	return s.GetHistoryLimit("", 0)
}

func (s *delayedSaveTerminalHistoryStore) GetHistoryLimit(_ string, limit int) ([]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	history := s.history
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	return append([]any(nil), history...), nil
}

func (s *blockingTerminalHistoryStore) SaveHistory(string, time.Time, any) error { return nil }

func (s *blockingTerminalHistoryStore) GetHistory(string) ([]any, error) {
	return s.GetHistoryLimit("", 0)
}

func (s *blockingTerminalHistoryStore) GetHistoryLimit(string, int) ([]any, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]any(nil), s.history...), nil
}

func TestStoredTestHistoryCacheKeyIncludesProjectIdentity(t *testing.T) {
	createdAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	left := &ManagedTest{ID: "shared-id", ProjectID: "project-a", CreatedAt: createdAt, EndedAt: &endedAt}
	right := &ManagedTest{ID: "shared-id", ProjectID: "project-b", CreatedAt: createdAt, EndedAt: &endedAt}
	if storedTestHistoryCacheKey(left) == storedTestHistoryCacheKey(right) {
		t.Fatal("different project identities shared a terminal-history cache key")
	}
}

func TestStoredTestHistoryCacheRefusesAttachmentAfterLoadInvalidation(t *testing.T) {
	cache := newStoredTestHistoryCache()
	value, revision, err := cache.getOrLoadAtRevision("same-run|identity", func() (*storedTestHistorySnapshot, error) {
		return &storedTestHistorySnapshot{History: []*LiveMetrics{{TotalTx: 1}}}, nil
	})
	if err != nil || value == nil {
		t.Fatalf("cache load = %#v, %v", value, err)
	}
	cache.deleteTest("same-run")
	applied := false
	if cache.applyIfCurrent(revision, func() { applied = true }) {
		t.Fatal("cache accepted an attachment after its load revision was invalidated")
	}
	if applied {
		t.Fatal("invalidated attachment callback ran")
	}
}

func TestAttachStoredTerminalHistoryRevalidatesExpiredHAEntry(t *testing.T) {
	start := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	middle := start.Add(time.Second)
	endedAt := start.Add(2 * time.Second)
	test := &ManagedTest{
		ID: "cross-replica-final-write", ProjectID: "project-a", CreatedAt: start, EndedAt: &endedAt, Status: "COMPLETE",
		LiveMetrics: &LiveMetrics{Timestamp: endedAt, TotalTx: 3},
		History:     []*LiveMetrics{{Timestamp: endedAt, TotalTx: 3}},
	}
	store := &metricsStoreSpy{historyByTestID: map[string][]any{
		test.ID: {
			map[string]any{"timestamp": start, "total_transactions": float64(1)},
			map[string]any{"timestamp": middle, "total_transactions": float64(2)},
		},
	}}
	cache := newStoredTestHistoryCache()
	key := storedTestHistoryCacheKey(test)
	if _, _, err := cache.getOrLoadAtRevision(key, func() (*storedTestHistorySnapshot, error) {
		return &storedTestHistorySnapshot{
			History:  []*LiveMetrics{{Timestamp: start, TotalTx: 1}},
			LoadedAt: time.Now().Add(-storedTestHistoryHARevalidateInterval - time.Second),
		}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &TestRunner{
		tests:              map[string]*ManagedTest{test.ID: test},
		historyStore:       store,
		storedHistoryCache: cache,
		leadership:         &executionLeadership{},
	}

	snapshot := cloneManagedTestForReadLocked(test)
	runner.attachStoredTerminalHistory(snapshot)

	if store.limitedHistoryCalls != 1 {
		t.Fatalf("history reloads = %d, want one HA revalidation", store.limitedHistoryCalls)
	}
	if len(snapshot.History) != 3 || snapshot.History[1].TotalTx != 2 || snapshot.History[2].TotalTx != 3 {
		t.Fatalf("revalidated history = %#v, want final durable point and canonical tail", snapshot.History)
	}
	freshSnapshot := cloneManagedTestForReadLocked(test)
	runner.attachStoredTerminalHistory(freshSnapshot)
	for _, sample := range []*LiveMetrics{snapshot.LiveMetrics, freshSnapshot.LiveMetrics} {
		if sample.ObservationState != "historical" {
			t.Fatal("reload lost the terminal observation label")
		}
	}
	if test.LiveMetrics.ObservationState != "" {
		t.Fatal("reload mutated the canonical sample")
	}
	if store.limitedHistoryCalls != 1 {
		t.Fatalf("fresh HA cache reloaded history; calls = %d, want 1", store.limitedHistoryCalls)
	}
}

func TestAttachStoredTerminalHistoryRejectsInFlightReplacedIdentity(t *testing.T) {
	createdAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	store := &blockingTerminalHistoryStore{
		history: []any{map[string]any{"timestamp": endedAt, "total_transactions": float64(999)}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	oldCanonical := &ManagedTest{
		ID: "shared-id", ProjectID: "project-a", CreatedAt: createdAt, EndedAt: &endedAt, Status: "COMPLETE",
		LiveMetrics: &LiveMetrics{Timestamp: endedAt, TotalTx: 1},
		History:     []*LiveMetrics{{Timestamp: endedAt, TotalTx: 1}},
	}
	runner := &TestRunner{
		tests:              map[string]*ManagedTest{oldCanonical.ID: oldCanonical},
		historyStore:       store,
		storedHistoryCache: newStoredTestHistoryCache(),
	}
	snapshot := cloneManagedTestForReadLocked(oldCanonical)
	done := make(chan struct{})
	go func() {
		runner.attachStoredTerminalHistory(snapshot)
		close(done)
	}()
	<-store.started

	runner.mu.Lock()
	runner.tests[oldCanonical.ID] = &ManagedTest{
		ID: oldCanonical.ID, ProjectID: "project-b", CreatedAt: createdAt, EndedAt: &endedAt, Status: "COMPLETE",
	}
	runner.storedHistoryCache.deleteTest(oldCanonical.ID)
	runner.mu.Unlock()
	close(store.release)
	<-done

	if len(snapshot.History) != 1 || snapshot.History[0].TotalTx != 1 {
		t.Fatalf("replaced identity received stale in-flight history: %#v", snapshot.History)
	}
	if runner.storedHistoryCache.len() != 0 {
		t.Fatal("invalidated in-flight history was installed in the cache")
	}
}

func TestAttachStoredTerminalHistoryRetriesInvalidatedInFlightLoad(t *testing.T) {
	start := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	middle := start.Add(time.Second)
	endedAt := start.Add(2 * time.Second)
	store := &invalidatedInFlightTerminalHistoryStore{
		old:     []any{map[string]any{"timestamp": start, "total_transactions": float64(1)}},
		current: []any{map[string]any{"timestamp": start, "total_transactions": float64(1)}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	canonical := &ManagedTest{
		ID: "same-run", ProjectID: "project-a", CreatedAt: start, EndedAt: &endedAt, Status: "COMPLETE",
		LiveMetrics: &LiveMetrics{Timestamp: endedAt, TotalTx: 3},
		History:     []*LiveMetrics{{Timestamp: endedAt, TotalTx: 3}},
	}
	runner := &TestRunner{
		tests:              map[string]*ManagedTest{canonical.ID: canonical},
		historyStore:       store,
		storedHistoryCache: newStoredTestHistoryCache(),
	}
	snapshot := cloneManagedTestForReadLocked(canonical)
	done := make(chan struct{})
	go func() {
		runner.attachStoredTerminalHistory(snapshot)
		close(done)
	}()
	<-store.started

	store.mu.Lock()
	store.current = []any{
		map[string]any{"timestamp": start, "total_transactions": float64(1)},
		map[string]any{"timestamp": middle, "total_transactions": float64(2)},
	}
	store.mu.Unlock()
	runner.storedHistoryCache.deleteTest(canonical.ID)
	close(store.release)
	<-done

	if store.getCalls != 2 {
		t.Fatalf("history loads = %d, want invalidated load plus one retry", store.getCalls)
	}
	if len(snapshot.History) != 3 || snapshot.History[1].TotalTx != 2 || snapshot.History[2].TotalTx != 3 {
		t.Fatalf("history after same-identity invalidation = %#v, want refreshed middle point and canonical tail", snapshot.History)
	}
}

func TestMergeStoredHistoryPreservesNewerCanonicalTail(t *testing.T) {
	start := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	stored := []*LiveMetrics{{Timestamp: start, TotalTx: 1}, {Timestamp: start.Add(time.Second), TotalTx: 2}}
	canonical := &LiveMetrics{Timestamp: start.Add(2 * time.Second), TotalTx: 3}
	merged := mergeStoredHistoryWithCanonicalTail(stored, canonical)
	if len(merged) != 3 || merged[2].TotalTx != 3 {
		t.Fatalf("merged history = %#v, want newer canonical tail", merged)
	}
	canonical.TotalTx = 99
	if merged[2].TotalTx != 3 {
		t.Fatal("merged tail aliases canonical mutable state")
	}

	replacement := &LiveMetrics{Timestamp: start.Add(time.Second), TotalTx: 20}
	merged = mergeStoredHistoryWithCanonicalTail(stored, replacement)
	if len(merged) != 2 || merged[1].TotalTx != 20 {
		t.Fatalf("equal-timestamp canonical tail did not replace stored value: %#v", merged)
	}
}

func TestSaveLiveMetricsHistoryInvalidatesTerminalCache(t *testing.T) {
	createdAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	store := &metricsStoreSpy{historyByTestID: map[string][]any{
		"terminal-cache": {map[string]any{"timestamp": endedAt, "total_transactions": float64(1)}},
	}}
	test := &ManagedTest{
		ID: "terminal-cache", ProjectID: "project-a", CreatedAt: createdAt, EndedAt: &endedAt, Status: "COMPLETE",
		LiveMetrics: &LiveMetrics{Timestamp: endedAt, TotalTx: 1},
		History:     []*LiveMetrics{{Timestamp: endedAt, TotalTx: 1}},
	}
	runner := &TestRunner{
		tests:              map[string]*ManagedTest{test.ID: test},
		historyStore:       store,
		storedHistoryCache: newStoredTestHistoryCache(),
	}
	first := cloneManagedTestForReadLocked(test)
	runner.attachStoredTerminalHistory(first)
	if store.limitedHistoryCalls != 1 || first.LiveMetrics.TotalTx != 1 {
		t.Fatalf("initial cache load = calls:%d metrics:%#v", store.limitedHistoryCalls, first.LiveMetrics)
	}
	store.historyByTestID[test.ID] = []any{map[string]any{"timestamp": endedAt.Add(time.Second), "total_transactions": float64(2)}}
	if err := runner.saveLiveMetricsHistory(test.ID, endedAt.Add(time.Second), &LiveMetrics{Timestamp: endedAt.Add(time.Second), TotalTx: 2}); err != nil {
		t.Fatal(err)
	}
	second := cloneManagedTestForReadLocked(test)
	runner.attachStoredTerminalHistory(second)
	if store.limitedHistoryCalls != 2 || second.LiveMetrics.TotalTx != 2 {
		t.Fatalf("post-write cache load = calls:%d metrics:%#v", store.limitedHistoryCalls, second.LiveMetrics)
	}
}

func TestDelayedFinalHistorySaveInvalidatesCacheAfterTerminalization(t *testing.T) {
	createdAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	store := &delayedSaveTerminalHistoryStore{
		history:     []any{map[string]any{"timestamp": createdAt, "total_transactions": float64(1)}},
		saveStarted: make(chan struct{}),
		saveRelease: make(chan struct{}),
	}
	test := &ManagedTest{
		ID: "delayed-final", ProjectID: "project-a", CreatedAt: createdAt, Status: "RUNNING",
		LiveMetrics: &LiveMetrics{Timestamp: createdAt, TotalTx: 1},
		History:     []*LiveMetrics{{Timestamp: createdAt, TotalTx: 1}},
	}
	runner := &TestRunner{
		tests:              map[string]*ManagedTest{test.ID: test},
		historyStore:       store,
		storedHistoryCache: newStoredTestHistoryCache(),
	}
	finalMetrics := &LiveMetrics{Timestamp: endedAt, TotalTx: 2}
	saved := make(chan error, 1)
	go func() { saved <- runner.saveLiveMetricsHistory(test.ID, endedAt, finalMetrics) }()
	<-store.saveStarted

	runner.mu.Lock()
	test.Status = "COMPLETE"
	test.EndedAt = &endedAt
	test.LiveMetrics = finalMetrics
	test.History = []*LiveMetrics{finalMetrics}
	runner.mu.Unlock()
	beforeSave := cloneManagedTestForReadLocked(test)
	runner.attachStoredTerminalHistory(beforeSave)
	if store.getCalls != 1 || runner.storedHistoryCache.len() != 1 {
		t.Fatalf("pre-save terminal cache = calls:%d entries:%d", store.getCalls, runner.storedHistoryCache.len())
	}

	close(store.saveRelease)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if runner.storedHistoryCache.len() != 0 {
		t.Fatal("delayed final history save did not invalidate terminal cache")
	}
	afterSave := cloneManagedTestForReadLocked(test)
	runner.attachStoredTerminalHistory(afterSave)
	if store.getCalls != 2 || len(afterSave.History) != 2 || afterSave.LiveMetrics.TotalTx != 2 {
		t.Fatalf("post-save history = calls:%d history:%#v", store.getCalls, afterSave.History)
	}
}
