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
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) GetTest(id string) (*ManagedTest, bool) {
	tr.refreshTestsForHARead()
	tr.refreshReadMetadataForTest(id)
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	t, ok := tr.tests[resolvedID]
	if ok {
		cloned := cloneManagedTestForReadLocked(t)
		cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(t)
		tr.mu.RUnlock()
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
	managed := managedTestFromRecord(record)
	tr.loadStoredTestHistory(managed)
	tr.loadStoredTestArchive(managed)
	tr.mu.Lock()
	if current := tr.tests[managed.ID]; current == nil || current.cancel == nil {
		tr.tests[managed.ID] = managed
		tr.observeAndIndexRefKeyLocked(managed.RefKey, managed.ID)
	}
	cloned := cloneManagedTestForReadLocked(tr.tests[managed.ID])
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(tr.tests[managed.ID])
	tr.mu.Unlock()
	return cloned, true
}

func (tr *TestRunner) refreshTestsForHARead() {
	if tr == nil || tr.leadership == nil || tr.holdsExecutionLeadership() {
		return
	}
	if err := tr.reloadStoredTests(); err != nil {
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
			if pinned == nil || markedAt.After(pinnedMarkedAt) || (markedAt.Equal(pinnedMarkedAt) && completedAt.After(pinnedCompletedAt)) {
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
	metrics := latestMetricsWithComparableBreakdown(selected)
	if metrics == nil {
		return nil
	}
	return &RunComparisonBaseline{
		SourceTestID:     selected.ID,
		SourceTestName:   selected.Name,
		Policy:           policy,
		CompletedAt:      selectedCompletedAt,
		Transactions:     cloneTxMetricsMap(metrics.Transactions),
		DependencyHealth: cloneTxMetricsMap(metrics.DependencyHealth),
	}
}

func isComparableBaselineCandidate(current *ManagedTest, candidate *ManagedTest) bool {
	if current == nil || candidate == nil || current.ID == candidate.ID {
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

func (tr *TestRunner) persistBaselineChanges(changes map[string]*time.Time) error {
	if store, ok := tr.testStore.(baselinePersistence); ok {
		return store.UpdateTestBaselines(changes)
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
	type baselinePinSnapshot struct {
		test     *ManagedTest
		pinned   bool
		pinnedAt *time.Time
	}
	snapshots := make([]baselinePinSnapshot, 0)
	pinnedAt := time.Now().UTC()
	for _, candidate := range tr.tests {
		if !sharesComparisonScope(test, candidate) {
			continue
		}
		snapshots = append(snapshots, baselinePinSnapshot{
			test:     candidate,
			pinned:   candidate.BaselinePinned,
			pinnedAt: cloneTimePtr(candidate.BaselinePinnedAt),
		})
		if candidate.ID == test.ID {
			candidate.BaselinePinned = true
			candidate.BaselinePinnedAt = &pinnedAt
		} else if candidate.BaselinePinned || candidate.BaselinePinnedAt != nil {
			candidate.BaselinePinned = false
			candidate.BaselinePinnedAt = nil
		}
	}
	changes := make(map[string]*time.Time, len(snapshots))
	for _, snapshot := range snapshots {
		changes[snapshot.test.ID] = cloneTimePtr(snapshot.test.BaselinePinnedAt)
	}
	if err := tr.persistBaselineChanges(changes); err != nil {
		for _, previous := range snapshots {
			previous.test.BaselinePinned = previous.pinned
			previous.test.BaselinePinnedAt = cloneTimePtr(previous.pinnedAt)
		}
		return nil, fmt.Errorf("persist comparison baseline: %w", err)
	}
	cloned := cloneManagedTestForReadLocked(test)
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return cloned, nil
}

func (tr *TestRunner) UnpinComparisonBaseline(id string) (*ManagedTest, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
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
	tr.refreshReadMetadataForTestList()
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	contextFiltered := query.applicationID != "" || query.releaseID != "" || query.scenarioID != ""
	resolvedScenarioID := query.scenarioID
	if resolvedScenarioID != "" {
		resolvedScenarioID = tr.resolveRefKeyLocked(resolvedScenarioID)
	}
	projectFiltered := !contextFiltered && (query.projectID != "" || query.folderID != nil)
	projectID := query.projectID
	if projectFiltered {
		projectID = normalizeProjectID(projectID)
	}

	matches := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		if test == nil {
			continue
		}
		if contextFiltered {
			if query.applicationID != "" && test.ApplicationID != query.applicationID {
				continue
			}
			if query.releaseID != "" && test.ReleaseID != query.releaseID {
				continue
			}
			if resolvedScenarioID != "" && test.ScenarioID != resolvedScenarioID {
				continue
			}
		} else if projectFiltered {
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

	sortTestList(matches, query.limit > 0)
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

	tr.mu.Lock()
	defer tr.mu.Unlock()
	resolvedID = tr.resolveRefKeyLocked(id)
	tr.refreshPendingQueueMetadataLocked()
	if test, ok := tr.tests[resolvedID]; ok && (test.Status == "QUEUED" || test.Status == "PENDING") {
		tr.refreshCapacityAdvisoryLocked(test)
	}
}

func (tr *TestRunner) refreshReadMetadataForTestList() {
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

	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
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
	cloned.Sparkline = buildManagedTestSparkline(test.History, test.LiveMetrics, defaultManagedTestSparklinePoints)
	return cloned
}

// cloneManagedTestFieldsLocked returns a serialization-safe snapshot of every
// exported ManagedTest field. Callers hold tr.mu, but the returned value is used
// after that lock is released (including by JSON encoders), so no mutable child
// may still alias the live controller state.
func cloneManagedTestFieldsLocked(test *ManagedTest) *ManagedTest {
	cloned := *test
	cloned.FolderID = cloneStringPtr(test.FolderID)
	cloned.Groups = cloneArchiveTestGroups(test.Groups)
	cloned.CapacityAdvisory = cloneCapacityAdvisory(test.CapacityAdvisory)
	cloned.RuntimeSettingsReview = cloneRuntimeSettingsReview(test.RuntimeSettingsReview)
	cloned.Assignment = cloneGeneratorAssignmentSettings(test.Assignment)
	cloned.GoalConfig = cloneGoalConfig(test.GoalConfig)
	cloned.GoalTargets = cloneGoalTargetStatusMap(test.GoalTargets)
	cloned.RuntimeSettings = cloneRuntimeSettings(test.RuntimeSettings)
	cloned.GeneratorShards = cloneGeneratorShardSummaries(test.GeneratorShards)
	cloned.ScheduledStartAt = cloneTimePtr(test.ScheduledStartAt)
	cloned.TimeslotID = cloneStringPtr(test.TimeslotID)
	cloned.PendingSince = cloneTimePtr(test.PendingSince)
	cloned.BaselinePinnedAt = cloneTimePtr(test.BaselinePinnedAt)
	cloned.StartedAt = cloneTimePtr(test.StartedAt)
	cloned.EndedAt = cloneTimePtr(test.EndedAt)
	cloned.ExecutionPlanApproval = cloneRunArchiveExecutionPlanApprovalSnapshot(test.ExecutionPlanApproval)
	cloned.VUserRamps = cloneVUserRampStatusMap(test.VUserRamps)
	cloned.LiveMetrics = cloneLiveMetrics(test.LiveMetrics)
	cloned.Sparkline = append([]TestSparklinePoint(nil), test.Sparkline...)
	cloned.ComparisonBaseline = cloneRunComparisonBaseline(test.ComparisonBaseline)
	cloned.Archive = cloneRunArchiveRecord(test.Archive)
	cloned.TelemetryHealth = cloneTelemetryRunHealth(test.TelemetryHealth)
	return &cloned
}

func cloneLiveMetrics(metrics *LiveMetrics) *LiveMetrics {
	if metrics == nil {
		return nil
	}
	cloned := *metrics
	cloned.ErrorBreakdown = cloneStringInt64Map(metrics.ErrorBreakdown)
	cloned.Transactions = cloneTxMetricsMap(metrics.Transactions)
	cloned.DependencyHealth = cloneTxMetricsMap(metrics.DependencyHealth)
	cloned.DependencyMap = cloneLiveDependencyMap(metrics.DependencyMap)
	cloned.ErrorPatterns = cloneLiveErrorPatterns(metrics.ErrorPatterns)
	cloned.SOAPOperations = cloneTxMetricsMap(metrics.SOAPOperations)
	cloned.NetworkProfiles = cloneTxMetricsMap(metrics.NetworkProfiles)
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
	cloned.ErrorEvidence = cloneLiveErrorEvidence(metrics.ErrorEvidence)
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
		if err := tr.testStore.MoveTest(id, folderID); err != nil {
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
	record, ok = tr.loadRunArchiveFromStore(id)
	if !ok {
		return nil, false
	}
	if !runArchiveRecordAvailableContext(ctx, record) {
		tr.mu.Lock()
		if test, exists := tr.tests[id]; exists {
			test.Archive = nil
		}
		tr.mu.Unlock()
		return nil, false
	}
	tr.mu.Lock()
	tr.archives[id] = record
	if test, exists := tr.tests[id]; exists {
		test.Archive = record
	}
	tr.mu.Unlock()
	return cloneRunArchiveRecord(record), true
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
		switch test.Status {
		case "RUNNING", "INITIALIZING", "PAUSED", "STOPPING", "SHAKEOUT":
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
	if test.Status == "RUNNING" || test.Status == "INITIALIZING" || test.Status == "PAUSED" || test.Status == "STOPPING" || test.Status == "SHAKEOUT" {
		return false, nil // Can't delete active tests
	}
	if err := tr.deleteStoredTest(id); err != nil {
		return false, err
	}
	deleteGroupMetricsHistoryCache(id)
	tr.removeRefKeyLocked(test.RefKey)
	delete(tr.tests, id)
	delete(tr.archives, id)
	forgetRunArchiveCache(id)
	return true, nil
}
