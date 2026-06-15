package api

// Test-record management: lookups (get/list/filter/by-context), run-comparison
// baseline selection and pinning, archive-record access, and delete. This is
// the read/manage side of tests; the execution lifecycle (StartTest, etc.)
// stays in runner.go. Extracted from the runner.go god-file.

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) GetTest(id string) (*ManagedTest, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	tr.refreshPendingQueueMetadataLocked()
	t, ok := tr.tests[id]
	if ok && t.Status == "QUEUED" {
		tr.refreshCapacityAdvisoryLocked(t)
	}
	if ok && t.Status == "PENDING" {
		tr.refreshCapacityAdvisoryLocked(t)
	}
	if !ok {
		return nil, false
	}
	cloned := *t
	cloned.Groups = cloneArchiveTestGroups(t.Groups)
	cloned.VUserRamps = cloneVUserRampStatusMap(t.VUserRamps)
	cloned.GoalTargets = cloneGoalTargetStatusMap(t.GoalTargets)
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(t)
	return &cloned, true
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
	pinnedAt := time.Now().UTC()
	for _, candidate := range tr.tests {
		if !sharesComparisonScope(test, candidate) {
			continue
		}
		if candidate.ID == test.ID {
			candidate.BaselinePinned = true
			candidate.BaselinePinnedAt = &pinnedAt
		} else if candidate.BaselinePinned || candidate.BaselinePinnedAt != nil {
			candidate.BaselinePinned = false
			candidate.BaselinePinnedAt = nil
		}
		tr.saveManagedTestRecord(candidate)
	}
	cloned := *test
	cloned.Groups = cloneArchiveTestGroups(test.Groups)
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return &cloned, nil
}

func (tr *TestRunner) UnpinComparisonBaseline(id string) (*ManagedTest, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	test.BaselinePinned = false
	test.BaselinePinnedAt = nil
	tr.saveManagedTestRecord(test)
	cloned := *test
	cloned.Groups = cloneArchiveTestGroups(test.Groups)
	cloned.ComparisonBaseline = tr.buildRunComparisonBaselineLocked(test)
	return &cloned, nil
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
	out := make([]*ManagedTest, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		if t.Status == "PENDING" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) ListTestsFiltered(projectID string, folderID *string) []*ManagedTest {
	projectID = normalizeProjectID(projectID)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
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
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		if t.Status == "PENDING" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) ListTestsByContext(applicationID string, releaseID string, scenarioID string) []*ManagedTest {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenarioID = tr.resolveRefKeyLocked(scenarioID)
	tr.refreshPendingQueueMetadataLocked()
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
		if t.Status == "QUEUED" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		if t.Status == "PENDING" {
			tr.refreshCapacityAdvisoryLocked(t)
		}
		out = append(out, t)
	}
	return out
}

func (tr *TestRunner) MoveTest(id string, folderID *string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return false
	}
	test.FolderID = folderID
	if tr.testStore != nil {
		_ = tr.testStore.MoveTest(id, folderID)
	}
	return true
}

func (tr *TestRunner) GetRunArchive(id string) (*storage.RunArchiveRecord, bool) {
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	record, ok := tr.archives[id]
	tr.mu.RUnlock()
	if ok {
		return record, true
	}
	record, ok = tr.loadRunArchiveFromStore(id)
	if !ok {
		return nil, false
	}
	if !runArchiveRecordAvailable(record) {
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
	return record, true
}

func runArchiveRecordAvailable(record *storage.RunArchiveRecord) bool {
	if record == nil || record.ManifestPath == "" {
		return false
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return false
	}
	_, err := os.Stat(record.ManifestPath)
	return err == nil
}

func (tr *TestRunner) GetRunArchiveManifest(id string) (*RunArchiveManifest, error) {
	record, ok := tr.GetRunArchive(id)
	if !ok {
		return nil, fmt.Errorf("archive not found: %s", id)
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, err
	}
	return loadRunArchiveManifest(record.ManifestPath)
}

func (tr *TestRunner) resolveTestID(id string) string {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.resolveRefKeyLocked(id)
}

func (tr *TestRunner) DeleteTest(id string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return false
	}
	if test.Status == "RUNNING" || test.Status == "INITIALIZING" || test.Status == "PAUSED" || test.Status == "STOPPING" {
		return false // Can't delete active tests
	}
	archivePath := ""
	if archive, exists := tr.archives[id]; exists {
		archivePath = archive.ArchivePath
	} else if test.Archive != nil {
		archivePath = test.Archive.ArchivePath
	}
	tr.removeRefKeyLocked(test.RefKey)
	delete(tr.tests, id)
	delete(tr.archives, id)
	tr.deleteStoredTest(id)
	if archivePath != "" {
		if err := os.RemoveAll(archivePath); err != nil {
			slog.Warn("failed to remove archive directory", "test_id", id, "archive_path", archivePath, "error", err)
		}
	}
	return true
}
