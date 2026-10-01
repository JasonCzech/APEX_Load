package api

// Test-record management: lookups (get/list/filter/by-context), run-comparison
// baseline selection and pinning, archive-record access, and delete. This is
// the read/manage side of tests; the execution lifecycle (StartTest, etc.)
// stays in runner.go. Extracted from the runner.go god-file.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) GetTest(id string) (*ManagedTest, bool) {
	test, ok := tr.getTestSnapshot(id, true)
	tr.attachTerminalArchiveCatalog(test)
	tr.attachRunArchiveFailure(test)
	tr.attachRunArchiveRecovery(test)
	return test, ok
}

func (tr *TestRunner) GetTestSummary(id string) (*ManagedTest, bool) {
	test, ok := tr.getTestSnapshot(id, false)
	tr.attachTerminalArchiveCatalog(test)
	tr.attachRunArchiveFailure(test)
	tr.attachRunArchiveRecovery(test)
	return test, ok
}

// GetTestMetricsWindow returns the normal serialization-safe test summary plus
// only the requested metrics window. Live history is selected before cloning,
// so a small dashboard window does not copy the controller's complete retained
// history while holding tr.mu.
func (tr *TestRunner) GetTestMetricsWindow(id string, since *time.Time, limit int) (*ManagedTest, bool) {
	test, ok := tr.getTestSnapshot(id, false)
	if !ok {
		return nil, false
	}

	tr.mu.RLock()
	canonical := tr.tests[test.ID]
	if canonical != nil {
		selected := selectLiveMetricsHistory(canonical.History, since, limit)
		test.History = cloneLiveMetricsHistory(selected)
		test.LiveMetrics = cloneRunLiveMetrics(canonical)
	}
	tr.mu.RUnlock()

	// Terminal canonical records retain only their compact tail. When a bounded
	// history window is requested, ask persistence for at most that many points
	// instead of hydrating the runner-wide maximum and trimming it afterward.
	if isTerminalTestStatus(test.Status) && limit > 0 && tr.historyStore != nil {
		loaded := &ManagedTest{ID: test.ID}
		if err := tr.loadStoredTestHistoryLimit(loaded, limit); err == nil {
			merged := mergeStoredHistoryWithCanonicalTail(loaded.History, test.LiveMetrics)
			test.History = cloneLiveMetricsHistory(selectLiveMetricsHistory(merged, since, limit))
		}
	} else if isTerminalTestStatus(test.Status) && tr.historyStore != nil {
		// An unbounded request keeps the endpoint's historical contract. Loading the
		// full terminal series is intentional here; unlike bounded dashboard polls,
		// the caller explicitly omitted a limit.
		tr.attachStoredTerminalHistory(test)
		test.History = selectLiveMetricsHistory(test.History, since, 0)
	}
	tr.attachTerminalArchiveCatalog(test)
	tr.attachRunArchiveFailure(test)
	tr.attachRunArchiveRecovery(test)
	return test, true
}

func (tr *TestRunner) getTestSnapshot(id string, includeHistory bool) (*ManagedTest, bool) {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTest(id)
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	t, ok := tr.tests[resolvedID]
	// A follower's full-map refresh is intentionally throttled. During that
	// window another replica can delete and recreate the same ID in a different
	// project, so a cached hit is not a sufficient authorization identity. HA
	// single-object reads revalidate the one durable row below; list reads keep
	// using the bounded snapshot refresh and do not regain the former N+1 scan.
	if ok && (tr.leadership == nil || tr.testStore == nil) {
		cloned := tr.cloneTestSnapshotLocked(t, includeHistory)
		tr.mu.RUnlock()
		if includeHistory {
			tr.attachStoredTerminalHistory(cloned)
		}
		return cloned, true
	}
	tr.mu.RUnlock()
	// HA reads may land on a replica that has not yet observed a test created by
	// another replica. Fall back to the shared durable store instead of returning
	// a replica-local false 404.
	if tr.testStore == nil {
		return nil, false
	}
	record, err := tr.testStore.GetTest(resolvedID)
	if err != nil || record == nil {
		return nil, false
	}
	if tr.leadership != nil {
		tr.mu.Lock()
		current := tr.tests[record.ID]
		if sameStoredTestIdentity(current, record) {
			bindingChanged := applyStoredCapacityBindingLocked(current, record)
			cloned := tr.cloneTestSnapshotLocked(current, includeHistory)
			tr.mu.Unlock()
			if includeHistory {
				tr.attachStoredTerminalHistory(cloned)
			}
			if bindingChanged {
				tr.broadcastCapacityBindingUpdated(cloned)
			}
			return cloned, true
		}
		tr.mu.Unlock()
	}
	managed := managedTestFromRecord(record)
	if isTerminalTestStatus(managed.Status) {
		trimTerminalManagedTestRuntime(managed)
	} else if err := tr.restoreManagedTestRuntimeAndRepair(managed); err != nil {
		managed.runtimeHydrationError = err.Error()
		slog.Error("failed to restore test runtime during durable read", "test_id", managed.ID, "error", err)
	}
	if includeHistory {
		if isTerminalTestStatus(managed.Status) {
			tr.loadStoredTerminalTestSummary(managed)
		} else {
			tr.loadStoredTestHistory(managed)
		}
	}
	// Do not publish an archive pointer until the durable identity has won the
	// final cache race below. loadStoredTestArchive writes tr.archives eagerly and
	// could otherwise attach the recreated row's archive to a stale live handle.
	if archive, found, loadErr := tr.loadRunArchiveFromStore(managed.ID); loadErr != nil {
		slog.Warn("failed to load stored test archive during durable read", "test_id", managed.ID, "error", loadErr)
	} else if found && storedArchiveCatalogAvailable(archive) {
		managed.Archive = archive
	}
	tr.mu.Lock()
	current := tr.tests[managed.ID]
	canonical := managed
	bindingChanged := false
	if sameStoredTestIdentity(current, record) {
		canonical = current
		bindingChanged = applyStoredCapacityBindingLocked(current, record)
	} else if current == nil || (current.cancel == nil && current.executionCtx == nil) {
		if current != nil {
			tr.removeRefKeyLocked(current.RefKey)
			tr.forgetRunSnapshotWriteStateLocked(managed.ID)
			forgetRunArchiveCache(managed.ID)
			deleteGroupMetricsHistoryCache(managed.ID)
		}
		if tr.storedHistoryCache != nil {
			tr.storedHistoryCache.deleteTest(managed.ID)
		}
		tr.tests[managed.ID] = managed
		tr.observeAndIndexRefKeyLocked(managed.RefKey, managed.ID)
		if managed.Archive != nil {
			tr.archives[managed.ID] = managed.Archive
		} else {
			delete(tr.archives, managed.ID)
		}
	}
	// A stale local execution handle is retained so leadership-loss teardown can
	// still cancel it, but it is never the serialization source for this durable
	// identity. Returning managed here is deliberately fail-closed across tenants.
	cloned := tr.cloneTestSnapshotLocked(canonical, includeHistory)
	tr.mu.Unlock()
	if includeHistory {
		tr.attachStoredTerminalHistory(cloned)
	}
	if bindingChanged {
		tr.broadcastCapacityBindingUpdated(cloned)
	}
	return cloned, true
}

func (tr *TestRunner) cloneTestSnapshotLocked(test *ManagedTest, includeHistory bool) *ManagedTest {
	var cloned *ManagedTest
	if includeHistory {
		cloned = cloneManagedTestForReadLocked(test)
	} else {
		cloned = cloneManagedTestSummaryForReadLocked(test)
	}
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return cloned
}

func (tr *TestRunner) refreshTestsForHARead() {
	if tr == nil || tr.leadership == nil || tr.holdsExecutionLeadership() {
		return
	}
	// A follower list/read used to reload every test, history, and archive for
	// every request. Concurrent dashboard polls therefore queued one complete DB
	// scan each behind testReloadMu even though the first scan had already made
	// the others redundant. The shared coalescer collapses a request burst onto
	// one durable refresh and a short, bounded staleness window absorbs the
	// polling cadence. testReloadMu is still taken around the reload itself so a
	// follower read cannot interleave with the full reconciliation leadership
	// acquisition performs through reloadStoredTests.
	if err := tr.haTestRefresh.refresh(haTestRefreshInterval, func() error {
		tr.testReloadMu.Lock()
		defer tr.testReloadMu.Unlock()
		return tr.reloadStoredTestsLocked()
	}); err != nil {
		slog.Warn("failed to refresh tests for HA read", "error", err)
	}
}

func (tr *TestRunner) buildRunComparisonBaselineLocked(test *ManagedTest) *RunComparisonBaseline {
	if test == nil {
		return nil
	}
	referenceTime := test.CreatedAt
	if test.StartedAt != nil && !test.StartedAt.IsZero() {
		referenceTime = *test.StartedAt
	}
	var pinned *ManagedTest
	var pinnedMarkedAt time.Time
	var pinnedCompletedAt time.Time
	var latestSuccessful *ManagedTest
	var latestSuccessfulCompletedAt time.Time
	var latestFinished *ManagedTest
	var latestFinishedCompletedAt time.Time
	for _, candidate := range tr.tests {
		if !isComparableBaselineCandidate(test, candidate) {
			continue
		}
		completedAt, ok := baselineCandidateCompletedAt(candidate)
		if !ok || (!referenceTime.IsZero() && !completedAt.Before(referenceTime)) {
			continue
		}
		if candidate.BaselinePinned {
			markedAt := completedAt
			if candidate.BaselinePinnedAt != nil && !candidate.BaselinePinnedAt.IsZero() {
				markedAt = *candidate.BaselinePinnedAt
			}
			if pinned == nil || markedAt.After(pinnedMarkedAt) || (markedAt.Equal(pinnedMarkedAt) && (completedAt.After(pinnedCompletedAt) || completedAt.Equal(pinnedCompletedAt) && candidate.ID < pinned.ID)) {
				pinned = candidate
				pinnedMarkedAt = markedAt
				pinnedCompletedAt = completedAt
			}
			continue
		}
		if candidate.Status == "COMPLETE" {
			if latestSuccessful == nil || completedAt.After(latestSuccessfulCompletedAt) {
				latestSuccessful = candidate
				latestSuccessfulCompletedAt = completedAt
			}
			continue
		}
		if latestFinished == nil || completedAt.After(latestFinishedCompletedAt) {
			latestFinished = candidate
			latestFinishedCompletedAt = completedAt
		}
	}
	policy := "latest_successful"
	selected := latestSuccessful
	selectedCompletedAt := latestSuccessfulCompletedAt
	if pinned != nil {
		selected = pinned
		selectedCompletedAt = pinnedCompletedAt
		policy = "pinned"
	} else if selected == nil {
		selected = latestFinished
		selectedCompletedAt = latestFinishedCompletedAt
		policy = "latest_finished"
	}
	if selected == nil {
		return nil
	}
	transactions, dependencyHealth, ok := comparableBaselineBreakdown(selected)
	if !ok {
		return nil
	}
	return &RunComparisonBaseline{
		SourceTestID:     selected.ID,
		SourceTestName:   selected.Name,
		Policy:           policy,
		CompletedAt:      selectedCompletedAt,
		Transactions:     transactions,
		DependencyHealth: dependencyHealth,
	}
}

func isComparableBaselineCandidate(current *ManagedTest, candidate *ManagedTest) bool {
	if current == nil || candidate == nil || current.ID == candidate.ID {
		return false
	}
	if normalizeProjectID(current.ProjectID) != normalizeProjectID(candidate.ProjectID) {
		return false
	}
	switch candidate.Status {
	case "COMPLETE", "FAILED", "ABORTED":
	default:
		return false
	}
	if current.ScenarioID != "" || candidate.ScenarioID != "" {
		return current.ScenarioID != "" && current.ScenarioID == candidate.ScenarioID
	}
	if current.ReleaseID != "" || candidate.ReleaseID != "" {
		return current.ReleaseID != "" && current.ReleaseID == candidate.ReleaseID && current.Name == candidate.Name
	}
	if current.ApplicationID != "" || candidate.ApplicationID != "" {
		return current.ApplicationID != "" && current.ApplicationID == candidate.ApplicationID && current.Name == candidate.Name
	}
	return current.Name != "" && current.Name == candidate.Name
}

func sharesComparisonScope(left *ManagedTest, right *ManagedTest) bool {
	if left == nil || right == nil {
		return false
	}
	if normalizeProjectID(left.ProjectID) != normalizeProjectID(right.ProjectID) {
		return false
	}
	if left.ScenarioID != "" || right.ScenarioID != "" {
		return left.ScenarioID != "" && left.ScenarioID == right.ScenarioID
	}
	if left.ReleaseID != "" || right.ReleaseID != "" {
		return left.ReleaseID != "" && left.ReleaseID == right.ReleaseID && left.Name == right.Name
	}
	if left.ApplicationID != "" || right.ApplicationID != "" {
		return left.ApplicationID != "" && left.ApplicationID == right.ApplicationID && left.Name == right.Name
	}
	return left.Name != "" && left.Name == right.Name
}

func canPinComparisonBaseline(test *ManagedTest) bool {
	if test == nil {
		return false
	}
	switch test.Status {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

type baselinePersistence interface {
	UpdateTestBaselines(changes map[string]*time.Time) error
}

type scopedBaselinePersistence interface {
	SetTestComparisonBaseline(id string, pinnedAt *time.Time) ([]storage.TestBaselineState, error)
}

func (tr *TestRunner) applyDurableBaselineStatesLocked(states []storage.TestBaselineState) {
	for _, state := range states {
		if test := tr.tests[state.ID]; test != nil {
			test.BaselinePinned = state.Pinned
			test.BaselinePinnedAt = cloneTimePtr(state.PinnedAt)
		}
	}
}

// persistBaselineChanges is called with tr.mu write-locked (see
// PinComparisonBaseline/UnpinComparisonBaseline), so its store call must be
// published to the panic backstop's in-flight gauge like every other lock-held
// durable write; see trackDurableWrite.
func (tr *TestRunner) persistBaselineChanges(changes map[string]*time.Time) error {
	if store, ok := tr.testStore.(baselinePersistence); ok {
		return tr.trackDurableWrite(func() error {
			return store.UpdateTestBaselines(changes)
		})
	}
	// Compatibility for focused test doubles; production stores implement the
	// column-specific transactional method above.
	for id := range changes {
		if test := tr.tests[id]; test != nil {
			if err := tr.saveManagedTestRecord(test); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tr *TestRunner) PinComparisonBaseline(id string) (*ManagedTest, error) {
	tr.refreshTestsForHARead()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if !canPinComparisonBaseline(test) {
		return nil, fmt.Errorf("comparison baseline pinning requires a completed, failed, or aborted test")
	}
	pinnedAt := time.Now().UTC()
	if store, ok := tr.testStore.(scopedBaselinePersistence); ok {
		var states []storage.TestBaselineState
		err := tr.trackDurableWrite(func() error {
			var writeErr error
			states, writeErr = store.SetTestComparisonBaseline(test.ID, &pinnedAt)
			return writeErr
		})
		if err != nil {
			return nil, fmt.Errorf("persist comparison baseline: %w", err)
		}
		tr.applyDurableBaselineStatesLocked(states)
		cloned := cloneManagedTestForReadLocked(test)
		cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
		return cloned, nil
	}
	previousPinned, previousAt := test.BaselinePinned, cloneTimePtr(test.BaselinePinnedAt)
	test.BaselinePinned, test.BaselinePinnedAt = true, &pinnedAt
	if err := tr.persistBaselineChanges(map[string]*time.Time{test.ID: &pinnedAt}); err != nil {
		test.BaselinePinned, test.BaselinePinnedAt = previousPinned, previousAt
		return nil, fmt.Errorf("persist comparison baseline: %w", err)
	}

	cloned := cloneManagedTestForReadLocked(test)
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return cloned, nil
}

func (tr *TestRunner) UnpinComparisonBaseline(id string) (*ManagedTest, error) {
	tr.refreshTestsForHARead()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if store, ok := tr.testStore.(scopedBaselinePersistence); ok {
		var states []storage.TestBaselineState
		err := tr.trackDurableWrite(func() error {
			var writeErr error
			states, writeErr = store.SetTestComparisonBaseline(test.ID, nil)
			return writeErr
		})
		if err != nil {
			return nil, fmt.Errorf("persist comparison baseline removal: %w", err)
		}
		tr.applyDurableBaselineStatesLocked(states)
		cloned := cloneManagedTestForReadLocked(test)
		cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
		return cloned, nil
	}
	previousPinned := test.BaselinePinned
	previousPinnedAt := cloneTimePtr(test.BaselinePinnedAt)
	test.BaselinePinned = false
	test.BaselinePinnedAt = nil
	if err := tr.persistBaselineChanges(map[string]*time.Time{test.ID: nil}); err != nil {
		test.BaselinePinned = previousPinned
		test.BaselinePinnedAt = previousPinnedAt
		return nil, fmt.Errorf("persist comparison baseline removal: %w", err)
	}
	cloned := cloneManagedTestForReadLocked(test)
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return cloned, nil
}

func baselineCandidateCompletedAt(candidate *ManagedTest) (time.Time, bool) {
	if candidate == nil {
		return time.Time{}, false
	}
	if candidate.EndedAt != nil && !candidate.EndedAt.IsZero() {
		return *candidate.EndedAt, true
	}
	for index := len(candidate.History) - 1; index >= 0; index-- {
		if candidate.History[index] != nil && !candidate.History[index].Timestamp.IsZero() {
			return candidate.History[index].Timestamp, true
		}
	}
	return time.Time{}, false
}

// runComparisonBreakdown is the per-run projection a later run compares itself
// against. It carries only the two maps RunComparisonBaseline publishes, so
// retaining it never pins the rest of a terminal run's live metric collections.
type runComparisonBreakdown struct {
	Transactions     map[string]*TxMetrics
	DependencyHealth map[string]*TxMetrics
}

// maxComparisonBaselineBreakdownEntries bounds each retained breakdown map. A
// workload with dynamically named transactions has unbounded cardinality, and a
// terminal run stays resident for the lifetime of the process; the busiest
// entries are the ones a comparison panel shows, so an over-sized breakdown is
// truncated to the highest-volume entries rather than dropped.
const maxComparisonBaselineBreakdownEntries = 500

// comparableBaselineBreakdown resolves the breakdown a baseline is built from.
// A live or not-yet-compacted run still carries it on the resident metric
// point; a run terminalized in this process has had those maps released by
// compactTerminalManagedTestLocked and serves the bounded copy captured just
// before that (captureComparisonBaselineBreakdown). A run re-hydrated after a
// restart has neither - its stored history is typed interval summaries, which
// carry no per-transaction breakdown - and reports no baseline, exactly as
// before.
func comparableBaselineBreakdown(test *ManagedTest) (map[string]*TxMetrics, map[string]*TxMetrics, bool) {
	if metrics := latestMetricsWithComparableBreakdown(test); metrics != nil {
		return cloneTxMetricsMap(metrics.Transactions), cloneTxMetricsMap(metrics.DependencyHealth), true
	}
	if test == nil || test.comparisonBreakdown == nil {
		return nil, nil, false
	}
	captured := test.comparisonBreakdown
	if len(captured.Transactions) == 0 && len(captured.DependencyHealth) == 0 {
		return nil, nil, false
	}
	return cloneTxMetricsMap(captured.Transactions), cloneTxMetricsMap(captured.DependencyHealth), true
}

// captureComparisonBaselineBreakdown copies the comparison-relevant breakdown
// off the resident metric point before terminal compaction releases it. It is
// only ever a promotion: a later compaction (a re-hydrated terminal run whose
// stored history carries no breakdown, or a repeat compaction of the same run)
// must not erase what an earlier one captured.
//
// The caller must hold tr.mu, like every other reader/writer of the run handle.
func captureComparisonBaselineBreakdown(test *ManagedTest) {
	if test == nil {
		return
	}
	metrics := latestMetricsWithComparableBreakdown(test)
	if metrics == nil {
		return
	}
	test.comparisonBreakdown = &runComparisonBreakdown{
		Transactions:     boundedTxMetricsMap(metrics.Transactions, maxComparisonBaselineBreakdownEntries),
		DependencyHealth: boundedTxMetricsMap(metrics.DependencyHealth, maxComparisonBaselineBreakdownEntries),
	}
}

// boundedTxMetricsMap clones at most limit entries, keeping the highest-volume
// ones (ties broken by name so the retained set is deterministic).
func boundedTxMetricsMap(in map[string]*TxMetrics, limit int) map[string]*TxMetrics {
	if len(in) == 0 {
		return nil
	}
	if limit <= 0 {
		return nil
	}
	if len(in) <= limit {
		return cloneTxMetricsMap(in)
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		leftCount, rightCount := int64(0), int64(0)
		if tx := in[keys[left]]; tx != nil {
			leftCount = tx.Count
		}
		if tx := in[keys[right]]; tx != nil {
			rightCount = tx.Count
		}
		if leftCount != rightCount {
			return leftCount > rightCount
		}
		return keys[left] < keys[right]
	})
	out := make(map[string]*TxMetrics, limit)
	for _, key := range keys[:limit] {
		tx := in[key]
		if tx == nil {
			continue
		}
		clone := *tx
		out[key] = &clone
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func latestMetricsWithComparableBreakdown(test *ManagedTest) *LiveMetrics {
	if test == nil {
		return nil
	}
	if test.LiveMetrics != nil && (len(test.LiveMetrics.Transactions) > 0 || len(test.LiveMetrics.DependencyHealth) > 0) {
		return test.LiveMetrics
	}
	for index := len(test.History) - 1; index >= 0; index-- {
		if test.History[index] != nil && (len(test.History[index].Transactions) > 0 || len(test.History[index].DependencyHealth) > 0) {
			return test.History[index]
		}
	}
	return nil
}

func cloneTxMetricsMap(in map[string]*TxMetrics) map[string]*TxMetrics {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*TxMetrics, len(in))
	for key, tx := range in {
		if tx == nil {
			continue
		}
		clone := *tx
		if tx.ServerTelemetry != nil {
			telemetry := *tx.ServerTelemetry
			telemetry.GRPC = telemetry.GRPC.Clone()
			telemetry.WebSocket = telemetry.WebSocket.Clone()
			telemetry.DiagnosticFindings = protocol.ProtocolDiagnosticFindings(telemetry.GRPC, telemetry.WebSocket)
			telemetry.RequestSizeClasses = cloneTelemetryClassCounts(tx.ServerTelemetry.RequestSizeClasses)
			telemetry.ResponseSizeClasses = cloneTelemetryClassCounts(tx.ServerTelemetry.ResponseSizeClasses)
			telemetry.RequestPayloadHistogram = cloneTelemetryClassCounts(tx.ServerTelemetry.RequestPayloadHistogram)
			telemetry.ResponsePayloadHistogram = cloneTelemetryClassCounts(tx.ServerTelemetry.ResponsePayloadHistogram)
			telemetry.PartitionEvents = cloneTelemetryClassCounts(tx.ServerTelemetry.PartitionEvents)
			clone.ServerTelemetry = &telemetry
		}
		if len(tx.CorrelationIDs) > 0 {
			clone.CorrelationIDs = append([]string(nil), tx.CorrelationIDs...)
		}
		out[key] = &clone
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TestCount returns the number of tracked tests under the lock that guards the
// tests map, so callers (e.g. the health handler) never read the map bare.
func (tr *TestRunner) TestCount() int {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return len(tr.tests)
}

func (tr *TestRunner) ListTests() []*ManagedTest {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		out = append(out, cloneManagedTestForReadLocked(t))
	}
	return out
}

func (tr *TestRunner) ListTestSummaries() []*ManagedTest {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		out = append(out, cloneManagedTestSummaryForReadLocked(t))
	}
	return out
}

// listOperationalTestSnapshots returns a narrow projection used by
// controller-wide accounting, capacity, retention, calendar, and observability
// paths. In particular it never clones transaction/dependency metric maps or
// authoring/runtime state; those paths consume only scalar live metrics and the
// compact workload/topology fields copied below.
func (tr *TestRunner) listOperationalTestSnapshots() []*ManagedTest {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		if test == nil {
			continue
		}
		out = append(out, cloneOperationalTestProjectionLocked(test))
	}
	return out
}

func cloneOperationalTestProjectionLocked(test *ManagedTest) *ManagedTest {
	if test == nil {
		return nil
	}
	projection := &ManagedTest{
		ID:                   test.ID,
		RefKey:               test.RefKey,
		Name:                 test.Name,
		Status:               test.Status,
		ApplicationID:        test.ApplicationID,
		ReleaseID:            test.ReleaseID,
		ScenarioID:           test.ScenarioID,
		ProjectID:            test.ProjectID,
		FolderID:             cloneStringPtr(test.FolderID),
		Groups:               cloneArchiveTestGroups(test.Groups),
		Assignment:           cloneGeneratorAssignmentSettings(test.Assignment),
		ScenarioMode:         test.ScenarioMode,
		ResourceBaseline:     test.ResourceBaseline,
		StartDelay:           test.StartDelay,
		TotalVUsers:          test.TotalVUsers,
		GeneratorShards:      cloneGeneratorShardSummaries(test.GeneratorShards),
		Duration:             test.Duration,
		DurationStr:          test.DurationStr,
		CreatedAt:            test.CreatedAt,
		ScheduledStartAt:     cloneTimePtr(test.ScheduledStartAt),
		StartedAt:            cloneTimePtr(test.StartedAt),
		PausedAt:             cloneTimePtr(test.PausedAt),
		CumulativePause:      test.CumulativePause,
		EndedAt:              cloneTimePtr(test.EndedAt),
		CreatedBySubject:     test.CreatedBySubject,
		CreatedByDisplayName: test.CreatedByDisplayName,
		StartedBySubject:     test.StartedBySubject,
		StartedByDisplayName: test.StartedByDisplayName,
		StoppedBySubject:     test.StoppedBySubject,
		StoppedByDisplayName: test.StoppedByDisplayName,
		Archive:              cloneRunArchiveRecord(test.Archive),
		ScriptID:             test.ScriptID,
		ScriptName:           test.ScriptName,
	}
	for index := range projection.Groups {
		// cloneArchiveTestGroups preserves the complete public run-calendar
		// contract, but operational readers must not retain or alias executable
		// script graphs and runtime-only overlays through its unexported fields.
		projection.Groups[index].script = nil
		projection.Groups[index].baseScript = nil
		projection.Groups[index].sharedDataSources = nil
		projection.Groups[index].redactionRules = nil
		projection.Groups[index].runtimeSecretValues = nil
	}
	if test.CapacityAdvisory != nil {
		projection.CapacityAdvisory = &CapacityAdvisory{RequiresOverride: test.CapacityAdvisory.RequiresOverride}
	}
	if test.LiveMetrics != nil {
		projection.LiveMetrics = &LiveMetrics{
			Timestamp:    test.LiveMetrics.Timestamp,
			ActiveVUsers: test.LiveMetrics.ActiveVUsers,
			TPS:          test.LiveMetrics.TPS,
			ErrorPct:     test.LiveMetrics.ErrorPct,
			P95Ms:        test.LiveMetrics.P95Ms,
			P99Ms:        test.LiveMetrics.P99Ms,
		}
		projection.LiveMetrics.ObservationValidUntil = cloneTimePtr(test.LiveMetrics.ObservationValidUntil)
		annotateLiveObservation(projection.LiveMetrics, test.Status, time.Now())
	}
	return projection
}

type testSummaryListQuery struct {
	projectID     string
	folderID      *string
	applicationID string
	releaseID     string
	scenarioID    string
	activeOnly    bool
	limit         int
	offset        int
}

// listTestSummaryPage filters and orders the lightweight list while holding
// the runner read lock, then builds sparklines only for the requested window.
// This keeps a bounded dashboard poll from walking every retained history.
func (tr *TestRunner) listTestSummaryPage(query testSummaryListQuery) ([]*ManagedTest, int) {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	resolvedScenarioID := query.scenarioID
	if resolvedScenarioID != "" {
		resolvedScenarioID = tr.resolveRefKeyLocked(resolvedScenarioID)
	}
	projectFiltered := query.projectID != "" || query.folderID != nil
	projectID := query.projectID
	if projectFiltered {
		projectID = normalizeProjectID(projectID)
	}

	matches := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		if test == nil {
			continue
		}
		if query.applicationID != "" && test.ApplicationID != query.applicationID {
			continue
		}
		if query.releaseID != "" && test.ReleaseID != query.releaseID {
			continue
		}
		if resolvedScenarioID != "" && test.ScenarioID != resolvedScenarioID {
			continue
		}
		if projectFiltered {
			if test.ProjectID != projectID {
				continue
			}
			if query.folderID != nil {
				if *query.folderID == "" {
					if test.FolderID != nil && *test.FolderID != "" {
						continue
					}
				} else if test.FolderID == nil || *test.FolderID != *query.folderID {
					continue
				}
			}
		}
		if query.activeOnly && !isActiveTestListStatus(test.Status) {
			continue
		}
		matches = append(matches, test)
	}

	sortTestList(matches)
	total := len(matches)
	start := min(query.offset, total)
	end := total
	if query.limit > 0 {
		end = min(start+query.limit, total)
	}
	out := make([]*ManagedTest, 0, end-start)
	for _, test := range matches[start:end] {
		out = append(out, cloneManagedTestSummaryForReadLocked(test))
	}
	return out, total
}

func (tr *TestRunner) ListTestsFiltered(projectID string, folderID *string) []*ManagedTest {
	tr.refreshTestsForHARead()
	projectID = normalizeProjectID(projectID)
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t.ProjectID != projectID {
			continue
		}
		if folderID != nil {
			if *folderID == "" {
				if t.FolderID != nil && *t.FolderID != "" {
					continue
				}
			} else if t.FolderID == nil || *t.FolderID != *folderID {
				continue
			}
		}
		out = append(out, cloneManagedTestForReadLocked(t))
	}
	return out
}

func (tr *TestRunner) ListTestSummariesFiltered(projectID string, folderID *string) []*ManagedTest {
	tr.refreshTestsForHARead()
	projectID = normalizeProjectID(projectID)
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t.ProjectID != projectID {
			continue
		}
		if folderID != nil {
			if *folderID == "" {
				if t.FolderID != nil && *t.FolderID != "" {
					continue
				}
			} else if t.FolderID == nil || *t.FolderID != *folderID {
				continue
			}
		}
		out = append(out, cloneManagedTestSummaryForReadLocked(t))
	}
	return out
}

func (tr *TestRunner) ListTestsByContext(applicationID string, releaseID string, scenarioID string) []*ManagedTest {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	scenarioID = tr.resolveRefKeyLocked(scenarioID)
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if applicationID != "" && t.ApplicationID != applicationID {
			continue
		}
		if releaseID != "" && t.ReleaseID != releaseID {
			continue
		}
		if scenarioID != "" && t.ScenarioID != scenarioID {
			continue
		}
		out = append(out, cloneManagedTestForReadLocked(t))
	}
	return out
}

func (tr *TestRunner) ListTestSummariesByContext(applicationID string, releaseID string, scenarioID string) []*ManagedTest {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	scenarioID = tr.resolveRefKeyLocked(scenarioID)
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if applicationID != "" && t.ApplicationID != applicationID {
			continue
		}
		if releaseID != "" && t.ReleaseID != releaseID {
			continue
		}
		if scenarioID != "" && t.ScenarioID != scenarioID {
			continue
		}
		out = append(out, cloneManagedTestSummaryForReadLocked(t))
	}
	return out
}

func (tr *TestRunner) refreshReadMetadataForTest(id string) {
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[resolvedID]
	needsRefresh := ok && (test.Status == "QUEUED" || test.Status == "PENDING")
	tr.mu.RUnlock()
	if !needsRefresh {
		return
	}

	tr.refreshPendingQueueMetadata()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	resolvedID = tr.resolveRefKeyLocked(id)
	if test, ok := tr.tests[resolvedID]; ok && (test.Status == "QUEUED" || test.Status == "PENDING") {
		tr.refreshCapacityAdvisoryLocked(test)
	}
}

func (tr *TestRunner) refreshReadMetadataForTestList() {
	tr.refreshCapacityBindingsForHAListRead()
	tr.mu.RLock()
	needsRefresh := false
	for _, test := range tr.tests {
		if test != nil && (test.Status == "QUEUED" || test.Status == "PENDING") {
			needsRefresh = true
			break
		}
	}
	tr.mu.RUnlock()
	if !needsRefresh {
		return
	}

	tr.refreshPendingQueueMetadata()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, test := range tr.tests {
		if test != nil && (test.Status == "QUEUED" || test.Status == "PENDING") {
			tr.refreshCapacityAdvisoryLocked(test)
		}
	}
}

func cloneManagedTestForReadLocked(test *ManagedTest) *ManagedTest {
	if test == nil {
		return nil
	}
	cloned := cloneManagedTestFieldsLocked(test)
	if len(test.History) > 0 {
		cloned.History = make([]*LiveMetrics, len(test.History))
		for index, metrics := range test.History {
			cloned.History[index] = cloneLiveMetrics(metrics)
		}
	}
	return cloned
}

func cloneManagedTestSummaryForReadLocked(test *ManagedTest) *ManagedTest {
	if test == nil {
		return nil
	}
	cloned := cloneManagedTestFieldsLocked(test)
	cloned.History = nil
	if len(test.Sparkline) > 0 {
		cloned.Sparkline = append([]TestSparklinePoint(nil), test.Sparkline...)
	} else {
		cloned.Sparkline = buildManagedTestSparkline(test.History, test.LiveMetrics, defaultManagedTestSparklinePoints)
	}
	return cloned
}

// cloneManagedTestFieldsLocked returns a serialization-safe snapshot of every
// exported ManagedTest field. Callers hold tr.mu, but the returned value is used
// after that lock is released (including by JSON encoders), so no mutable child
// may still alias the live controller state.
func cloneManagedTestFieldsLocked(test *ManagedTest) *ManagedTest {
	cloned := *test
	cloned.FolderID = cloneStringPtr(test.FolderID)
	cloned.DataPools = cloneScenarioDataPoolBindings(test.DataPools)
	cloned.Groups = cloneArchiveTestGroups(test.Groups)
	cloned.CapacityAdvisory = cloneCapacityAdvisory(test.CapacityAdvisory)
	cloned.RuntimeSettingsReview = cloneRuntimeSettingsReview(test.RuntimeSettingsReview)
	cloned.Assignment = cloneGeneratorAssignmentSettings(test.Assignment)
	cloned.GoalConfig = cloneGoalConfig(test.GoalConfig)
	cloned.GoalTargets = cloneGoalTargetStatusMap(test.GoalTargets)
	cloned.Continuity = runContinuityStatusLocked(test)
	cloned.continuityFailovers = append([]RunFailover(nil), test.continuityFailovers...)
	cloned.TrafficProfile = cloneTrafficProfile(test.TrafficProfile)
	cloned.RuntimeSettings = cloneRuntimeSettings(test.RuntimeSettings)
	cloned.ELKCapture = cloneELKCaptureConfig(test.ELKCapture)
	cloned.DynatraceCapture = cloneDynatraceCaptureConfig(test.DynatraceCapture)
	cloned.GeneratorShards = cloneGeneratorShardSummaries(test.GeneratorShards)
	cloned.ScheduledStartAt = cloneTimePtr(test.ScheduledStartAt)
	cloned.TimeslotID = cloneStringPtr(test.TimeslotID)
	cloned.PendingSince = cloneTimePtr(test.PendingSince)
	cloned.BaselinePinnedAt = cloneTimePtr(test.BaselinePinnedAt)
	cloned.StartedAt = cloneTimePtr(test.StartedAt)
	cloned.EndedAt = cloneTimePtr(test.EndedAt)
	cloned.StartAuthorization = cloneQueuedStartAuthorization(test.StartAuthorization)
	cloned.ExecutionPlanApproval = cloneRunArchiveExecutionPlanApprovalSnapshot(test.ExecutionPlanApproval)
	cloned.VUserRamps = cloneVUserRampStatusMap(test.VUserRamps)
	cloned.LiveMetrics = cloneRunLiveMetrics(test)
	cloned.Sparkline = append([]TestSparklinePoint(nil), test.Sparkline...)
	cloned.ComparisonBaseline = cloneRunComparisonBaseline(test.ComparisonBaseline)
	cloned.Archive = cloneRunArchiveRecord(test.Archive)
	cloned.ArchiveFailure = cloneRunArchiveFailure(test.ArchiveFailure)
	cloned.ArchiveRecovery = cloneRunArchiveRecovery(test.ArchiveRecovery)
	if cloned.Archive != nil {
		cloned.ArchiveFailure = nil
	}
	cloned.TelemetryHealth = cloneTelemetryRunHealth(test.TelemetryHealth)
	if test.initialArchiveProvenance != nil {
		initial := maskRunArchiveProvenance(*test.initialArchiveProvenance)
		cloned.initialArchiveProvenance = &initial
	}
	cloned.ciSLADetails = append([]string(nil), test.ciSLADetails...)
	cloned.thresholdAlertActive = cloneStringBoolMap(test.thresholdAlertActive)
	cloned.thresholdAlertNotifiedAt = cloneStringTimeMap(test.thresholdAlertNotifiedAt)
	cloned.remoteSecretSnapshots = nil
	return &cloned
}

func cloneStringBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	cloned := make(map[string]bool, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneStringTimeMap(values map[string]time.Time) map[string]time.Time {
	if values == nil {
		return nil
	}
	cloned := make(map[string]time.Time, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// cloneRunLiveMetrics annotates the public current-run projection without changing
// the resident sample or history (which are also used by archive/SLA calculations).
func cloneRunLiveMetrics(test *ManagedTest) *LiveMetrics {
	if test == nil {
		return nil
	}
	metrics := cloneLiveMetrics(test.LiveMetrics)
	annotateLiveObservation(metrics, test.Status, time.Now())
	return metrics
}

func cloneLiveMetrics(metrics *LiveMetrics) *LiveMetrics {
	if metrics == nil {
		return nil
	}
	cloned := *metrics
	cloned.ObservationValidUntil = cloneTimePtr(metrics.ObservationValidUntil)
	cloned.ErrorBreakdown = cloneStringInt64Map(metrics.ErrorBreakdown)
	cloned.Transactions = cloneTxMetricsMap(metrics.Transactions)
	cloned.TransactionResponseTimeSLA = append([]TransactionResponseTimeSLAResult(nil), metrics.TransactionResponseTimeSLA...)
	cloned.DependencyHealth = cloneTxMetricsMap(metrics.DependencyHealth)
	cloned.DependencyMap = cloneLiveDependencyMap(metrics.DependencyMap)
	cloned.ErrorPatterns = cloneLiveErrorPatterns(metrics.ErrorPatterns)
	cloned.SOAPOperations = cloneTxMetricsMap(metrics.SOAPOperations)
	cloned.NetworkProfiles = cloneTxMetricsMap(metrics.NetworkProfiles)
	cloned.ByRegion = cloneTxMetricsMap(metrics.ByRegion)
	cloned.GroupMetrics = cloneGroupLiveMetricsMap(metrics.GroupMetrics)
	cloned.Browser = cloneBrowserLiveMetrics(metrics.Browser)
	cloned.BrowserByDevice = cloneBrowserByDeviceMetrics(metrics.BrowserByDevice)
	cloned.GoalStatus = cloneGoalStatus(metrics.GoalStatus)
	cloned.CapacityStatus = cloneLiveCapacityStatus(metrics.CapacityStatus)
	cloned.TelemetryStatus = cloneTelemetryRunHealth(metrics.TelemetryStatus)
	cloned.Telemetry = cloneTelemetryHealth(metrics.Telemetry)
	cloned.Resources = cloneSystemResources(metrics.Resources)
	cloned.GeneratorResources = cloneGeneratorResourceSnapshots(metrics.GeneratorResources)
	cloned.Latency = cloneNetworkLatency(metrics.Latency)
	cloned.ELK = cloneELKCaptureStatus(metrics.ELK)
	cloned.Dynatrace = cloneDynatraceCaptureStatus(metrics.Dynatrace)
	cloned.LoadLevels = append([]ActiveLoadLevel(nil), metrics.LoadLevels...)
	cloned.ErrorEvidence = cloneLiveErrorEvidence(metrics.ErrorEvidence)
	cloned.slaTelemetryPeaks = cloneSLATelemetryPeaks(metrics.slaTelemetryPeaks)
	return &cloned
}

func cloneStringInt64Map(values map[string]int64) map[string]int64 {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]int64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneLiveDependencyMap(dependencies *LiveDependencyMap) *LiveDependencyMap {
	if dependencies == nil {
		return nil
	}
	return &LiveDependencyMap{
		Nodes: append([]LiveDependencyNode(nil), dependencies.Nodes...),
		Edges: append([]LiveDependencyEdge(nil), dependencies.Edges...),
	}
}

func cloneLiveErrorPatterns(patterns []LiveErrorPattern) []LiveErrorPattern {
	if len(patterns) == 0 {
		return nil
	}
	cloned := make([]LiveErrorPattern, len(patterns))
	for index, pattern := range patterns {
		cloned[index] = pattern
		cloned[index].Actions = append([]string(nil), pattern.Actions...)
		cloned[index].AffectedGroups = append([]string(nil), pattern.AffectedGroups...)
	}
	return cloned
}

func cloneGoalStatus(status *GoalStatus) *GoalStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	cloned.TargetAverageTPS = cloneFloat64Ptr(status.TargetAverageTPS)
	cloned.MaxErrorCount = cloneIntPtr(status.MaxErrorCount)
	cloned.MinVUsers = cloneIntPtr(status.MinVUsers)
	cloned.MaxVUsers = cloneIntPtr(status.MaxVUsers)
	cloned.LastScaleAt = cloneTimePtr(status.LastScaleAt)
	return &cloned
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneLiveCapacityStatus(status *LiveCapacityStatus) *LiveCapacityStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	cloned.ByProtocol = cloneLiveCapacityBreakdowns(status.ByProtocol)
	cloned.ByRegion = cloneLiveCapacityBreakdowns(status.ByRegion)
	return &cloned
}

func cloneLiveCapacityBreakdowns(values map[string]*LiveCapacityBreakdown) map[string]*LiveCapacityBreakdown {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]*LiveCapacityBreakdown, len(values))
	for key, value := range values {
		if value == nil {
			cloned[key] = nil
			continue
		}
		item := *value
		cloned[key] = &item
	}
	return cloned
}

func cloneSystemResources(resources *engine.SystemResources) *engine.SystemResources {
	if resources == nil {
		return nil
	}
	cloned := *resources
	return &cloned
}

func cloneGeneratorResourceSnapshots(values map[string]*GeneratorResourceSnapshot) map[string]*GeneratorResourceSnapshot {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]*GeneratorResourceSnapshot, len(values))
	for key, value := range values {
		if value == nil {
			cloned[key] = nil
			continue
		}
		item := *value
		item.ImpactReasons = append([]string(nil), value.ImpactReasons...)
		item.PodCapacity = value.PodCapacity.Clone()
		item.Resources = cloneSystemResources(value.Resources)
		cloned[key] = &item
	}
	return cloned
}

func cloneLiveErrorEvidence(values []LiveErrorEvidence) []LiveErrorEvidence {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]LiveErrorEvidence, len(values))
	for index, value := range values {
		cloned[index] = value
		cloned[index].Snapshot = cloneProtocolErrorSnapshot(value.Snapshot)
	}
	return cloned
}

func cloneRunComparisonBaseline(baseline *RunComparisonBaseline) *RunComparisonBaseline {
	if baseline == nil {
		return nil
	}
	cloned := *baseline
	cloned.Transactions = cloneTxMetricsMap(baseline.Transactions)
	cloned.DependencyHealth = cloneTxMetricsMap(baseline.DependencyHealth)
	return &cloned
}

func (tr *TestRunner) MoveTest(id string, folderID *string) bool {
	moved, _ := tr.MoveTestDurable(id, folderID)
	return moved
}

func (tr *TestRunner) MoveTestDurable(id string, folderID *string) (bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return false, nil
	}
	if tr.testStore != nil {
		// tr.mu is held across this store call, so publish it to the panic
		// backstop's in-flight gauge (see trackDurableWrite).
		if err := tr.trackDurableWrite(func() error {
			return tr.testStore.MoveTest(id, folderID)
		}); err != nil {
			return false, err
		}
	}
	test.FolderID = folderID
	return true, nil
}

func (tr *TestRunner) GetRunArchive(id string) (*storage.RunArchiveRecord, bool) {
	return tr.GetRunArchiveContext(context.Background(), id)
}

func (tr *TestRunner) GetRunArchiveContext(ctx context.Context, id string) (*storage.RunArchiveRecord, bool) {
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	record, ok := tr.archives[id]
	cloned := cloneRunArchiveRecord(record)
	tr.mu.RUnlock()
	// In HA, another controller may have replaced the durable archive pointer
	// and scheduled this cached version for retirement. Re-read shared storage on
	// every request so this replica cannot serve an indefinitely stale URI.
	if ok && tr.leadership == nil {
		return cloned, true
	}
	record, ok, err := tr.loadRunArchiveFromStore(id)
	if err != nil {
		return nil, false
	}
	if !ok {
		tr.mu.Lock()
		delete(tr.archives, id)
		if test, exists := tr.tests[id]; exists {
			test.Archive = nil
		}
		tr.mu.Unlock()
		forgetRunArchiveCache(id)
		return nil, false
	}
	if !runArchiveRecordAvailableContext(ctx, record) {
		tr.forgetCachedRunArchive(id)
		return nil, false
	}
	// The archive catalog is keyed only by test ID. On an HA follower, an ID may
	// have been deleted/recreated by a peer while this replica still holds the old
	// tenant's catalog pointer. Bind the manifest back to the current durable test
	// identity before exposing any artifact through the general archive routes.
	if tr.leadership != nil && tr.testStore != nil {
		durable, loadErr := tr.testStore.GetTest(id)
		if loadErr != nil || durable == nil {
			tr.forgetCachedRunArchive(id)
			return nil, false
		}
		manifest, manifestErr := cachedRunArchiveManifest(record)
		if manifestErr != nil || !runArchiveManifestMatchesStoredTest(manifest, durable, tr.testStore) {
			tr.forgetCachedRunArchive(id)
			return nil, false
		}
	}
	tr.mu.Lock()
	tr.archives[id] = record
	if test, exists := tr.tests[id]; exists {
		test.Archive = record
	}
	tr.mu.Unlock()
	return cloneRunArchiveRecord(record), true
}

func (tr *TestRunner) forgetCachedRunArchive(id string) {
	tr.mu.Lock()
	delete(tr.archives, id)
	if test, exists := tr.tests[id]; exists {
		test.Archive = nil
	}
	tr.mu.Unlock()
	forgetRunArchiveCache(id)
}

func runArchiveManifestMatchesStoredTest(manifest *RunArchiveManifest, test *storage.TestRecord, metadataStore any) bool {
	if manifest == nil || test == nil || strings.TrimSpace(manifest.TestID) != strings.TrimSpace(test.ID) {
		return false
	}
	if normalizeProjectID(manifest.Provenance.ProjectID) != normalizeProjectID(test.ProjectID) {
		return false
	}
	// v2 archives persist the test's CreatedAt as RequestedAt. Keep legacy
	// archives (which omitted it) readable, but use it when present to reject a
	// same-project ID reuse as well as the cross-project case.
	return manifest.Provenance.RequestedAt.IsZero() || test.StartTime.IsZero() || archiveRunTimestampMatches(manifest.Provenance.RequestedAt, test.StartTime, metadataStore)
}

// archiveRunTimestampMatches compares run identity at the metadata store's
// precision. pgx encodes PostgreSQL timestamps by truncating to microseconds,
// whereas existing immutable manifests can retain the original nanoseconds.
// This is an exact comparison of that representation, not a time tolerance.
// Other stores retain their existing nanosecond identity checks.
func archiveRunTimestampMatches(requestedAt, createdAt time.Time, metadataStore any) bool {
	return archiveRunTimestamp(requestedAt, metadataStore).Equal(archiveRunTimestamp(createdAt, metadataStore))
}

func archiveRunTimestamp(at time.Time, metadataStore any) time.Time {
	if provider, ok := metadataStore.(storage.BackendInfoProvider); ok && provider.BackendInfo().Type == storage.StoreBackendPostgres {
		return at.Truncate(time.Microsecond)
	}
	return at
}

func cloneRunArchiveRecord(record *storage.RunArchiveRecord) *storage.RunArchiveRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	return &cloned
}

func runArchiveRecordAvailable(record *storage.RunArchiveRecord) bool {
	return runArchiveRecordAvailableContext(context.Background(), record)
}

func runArchiveRecordAvailableContext(ctx context.Context, record *storage.RunArchiveRecord) bool {
	if record == nil {
		return false
	}
	if archiveRecordBackend(record) == archiveBackendFilesystem && strings.TrimSpace(record.ManifestPath) == "" {
		return false
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return false
	}
	defer lease.Release()
	_, err = loadRunArchiveManifestFromRecord(record)
	return err == nil
}

func (tr *TestRunner) GetRunArchiveManifest(id string) (*RunArchiveManifest, error) {
	record, ok := tr.GetRunArchive(id)
	if !ok {
		return nil, fmt.Errorf("archive not found: %s", id)
	}
	return cachedRunArchiveManifest(record)
}

func (tr *TestRunner) resolveTestID(id string) string {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.resolveRefKeyLocked(id)
}

func (tr *TestRunner) DeleteTest(id string) bool {
	deleted, err := tr.DeleteTestWithError(id)
	if err != nil {
		slog.Warn("failed to delete test", "test_id", id, "metadata_deleted", deleted, "error", err)
		return deleted
	}
	return deleted
}

// DeleteTestWithError is the error-preserving form of the legacy bool API.
// Durable metadata (including an archive-cleanup outbox record) is committed
// before any irreversible object-store or filesystem deletion.
func (tr *TestRunner) DeleteTestWithError(id string) (bool, error) {
	if tr == nil || !tr.isExecutionLeader() {
		return false, errNotExecutionLeader
	}
	tr.mu.RLock()
	canonicalID := tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()

	unlockArchiveCommit := tr.lockArchiveCommitFence(canonicalID)
	defer unlockArchiveCommit()

	tr.mu.RLock()
	var archiveRecord *storage.RunArchiveRecord
	if test := tr.tests[canonicalID]; test != nil {
		if managedTestDeletionBlocked(test) {
			tr.mu.RUnlock()
			return false, nil
		}
	}
	if archive := tr.archives[canonicalID]; archive != nil {
		copy := *archive
		archiveRecord = &copy
	} else if test := tr.tests[canonicalID]; test != nil && test.Archive != nil {
		copy := *test.Archive
		archiveRecord = &copy
	}
	tr.mu.RUnlock()
	if archiveRecord == nil && tr.archiveStore != nil {
		record, err := tr.archiveStore.GetRunArchive(canonicalID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("load archive metadata before delete: %w", err)
		}
		if record != nil {
			copy := *record
			archiveRecord = &copy
		}
	}

	deleted, err := tr.DeleteTestDurable(canonicalID)
	if err != nil {
		return false, fmt.Errorf("delete durable test metadata: %w", err)
	}
	if !deleted {
		return false, nil
	}

	if archiveRecord != nil {
		ctx, cancel := context.WithTimeout(context.Background(), archiveCleanupAttemptTimeout)
		defer cancel()
		operationCtx, operationCancel := tr.archiveOperationContext(ctx)
		defer operationCancel()
		ctx = operationCtx
		if err := deleteArchiveBackendObjects(ctx, archiveRecord); err != nil {
			tr.markArchiveCleanupFailure(canonicalID, err)
			return true, fmt.Errorf("remove archive objects: %w", err)
		}
		if err := removeLocalArchiveDirectory(archiveRecord.ArchivePath); err != nil {
			tr.markArchiveCleanupFailure(canonicalID, err)
			return true, fmt.Errorf("remove archive directory: %w", err)
		}
		if outbox, ok := tr.archiveStore.(interface{ DeleteArchiveCleanupJob(string) error }); ok {
			if err := outbox.DeleteArchiveCleanupJob(canonicalID); err != nil {
				tr.markArchiveCleanupFailure(canonicalID, err)
				return true, fmt.Errorf("complete archive cleanup job: %w", err)
			}
		}
	}
	return true, nil
}

func (tr *TestRunner) markArchiveCleanupFailure(testID string, cleanupErr error) {
	outbox, ok := tr.archiveStore.(interface {
		MarkArchiveCleanupJobFailed(string, error, time.Time) error
	})
	if !ok {
		return
	}
	if err := outbox.MarkArchiveCleanupJobFailed(testID, cleanupErr, time.Now().UTC()); err != nil {
		slog.Warn("failed to annotate archive cleanup outbox", "test_id", testID, "cleanup_error", cleanupErr, "error", err)
	}
}

func (tr *TestRunner) DeleteTestDurable(id string) (bool, error) {
	if tr == nil || !tr.isExecutionLeader() {
		return false, errNotExecutionLeader
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return false, nil
	}
	if managedTestDeletionBlocked(test) {
		return false, nil // Can't delete active tests
	}
	result, err := tr.deleteStoredTest(id)
	if err != nil {
		return false, err
	}
	switch result {
	case storage.LeaseFencedDeleteMissing, storage.LeaseFencedDeleteActive:
		return false, nil
	case storage.LeaseFencedDeleteDeleted:
		// Continue with replica-local index cleanup below.
	default:
		return false, fmt.Errorf("unexpected durable test delete result %q", result)
	}
	deleteGroupMetricsHistoryCache(id)
	if tr.storedHistoryCache != nil {
		tr.storedHistoryCache.deleteTest(id)
	}
	tr.forgetRunSnapshotWriteStateLocked(id)
	tr.removeRefKeyLocked(test.RefKey)
	delete(tr.tests, id)
	delete(tr.archives, id)
	forgetRunArchiveCache(id)
	return true, nil
}

func managedTestDeletionBlocked(test *ManagedTest) bool {
	if test == nil {
		return false
	}
	// Passive transitions temporarily release tr.mu while their identity-fenced
	// storage write is in flight. Deletion must not remove that incarnation in
	// the gap, even when its public status is otherwise deletable.
	if strings.TrimSpace(test.passiveOperationToken) != "" {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(test.Status)) {
	case "INITIALIZING", "STARTING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
		return true
	default:
		return false
	}
}
