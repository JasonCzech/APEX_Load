package api

import (
	"database/sql"
	"errors"
	"log/slog"

	"github.com/jasonczech/apexload/pkg/storage"
)

type runArchiveFailureStore interface {
	SaveRunArchiveFailure(*storage.RunArchiveFailure, string, string) (bool, error)
	GetRunArchiveFailure(string) (*storage.RunArchiveFailure, error)
}

func (tr *TestRunner) archiveFailureStore() runArchiveFailureStore {
	store, _ := tr.testStore.(runArchiveFailureStore)
	return store
}

// The caller holds the run's archive commit fence. Recording a collection failure
// never rewrites the already durable execution outcome or creates a publication job.
func (tr *TestRunner) recordRemoteArchiveFailure(test *ManagedTest, reason string) {
	store := tr.archiveFailureStore()
	if store == nil {
		return
	}
	tr.mu.RLock()
	if test == nil || tr.tests[test.ID] != test || !isTerminalTestStatus(test.Status) || test.EndedAt == nil || test.Archive != nil {
		tr.mu.RUnlock()
		return
	}
	failure := &storage.RunArchiveFailure{TestID: test.ID, ProjectID: test.ProjectID, StartTime: test.CreatedAt,
		EndTime: *test.EndedAt, Status: test.Status, Reason: reason}
	leaseOwner := test.executionFence
	tr.mu.RUnlock()
	leaseName := ""
	if tr.durableExecutionFence {
		if leaseOwner == "" {
			return
		}
		leaseName = executionLeaseName
	} else {
		leaseOwner = ""
	}
	var saved bool
	err := retryDurableTransition(func() error {
		var err error
		saved, err = store.SaveRunArchiveFailure(failure, leaseName, leaseOwner)
		return err
	})
	if err != nil {
		slog.Error("failed to persist remote archive collection failure", "test_id", failure.TestID, "error", err)
		return
	}
	if !saved {
		return
	}
	stored, err := store.GetRunArchiveFailure(failure.TestID)
	if err != nil || stored == nil || normalizeProjectID(stored.ProjectID) != normalizeProjectID(failure.ProjectID) || stored.Status != failure.Status ||
		!archiveRunTimestampMatches(stored.StartTime, failure.StartTime, tr.testStore) || !archiveRunTimestampMatches(stored.EndTime, failure.EndTime, tr.testStore) {
		return
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.Archive != nil || test.EndedAt == nil || test.Status != failure.Status ||
		normalizeProjectID(test.ProjectID) != normalizeProjectID(failure.ProjectID) || !test.CreatedAt.Equal(failure.StartTime) || !test.EndedAt.Equal(failure.EndTime) {
		tr.mu.Unlock()
		return
	}
	event := cloneManagedTestLifecycleEvent(test)
	tr.mu.Unlock()
	if tr.hub != nil {
		tr.hub.BroadcastScopedJSON(event.ID, normalizeProjectID(event.ProjectID), map[string]any{
			"type": "test_archive_failed", "test_id": event.ID, "project_id": normalizeProjectID(event.ProjectID),
			"status": event.Status, "archive_failure": stored,
		})
	}
}

// Hydrate only single-run terminal reads. Lists do not add an N+1 database scan.
func (tr *TestRunner) attachRunArchiveFailure(test *ManagedTest) {
	if test == nil {
		return
	}
	test.ArchiveFailure = nil
	store := tr.archiveFailureStore()
	if store == nil || !isTerminalTestStatus(test.Status) || test.Archive != nil || test.EndedAt == nil {
		return
	}
	failure, err := store.GetRunArchiveFailure(test.ID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("failed to read archive failure state", "test_id", test.ID, "error", err)
		}
		return
	}
	// The test may have been deleted/recreated between the two reads. Use the
	// same backend timestamp precision as archive provenance validation.
	if failure != nil && normalizeProjectID(failure.ProjectID) == normalizeProjectID(test.ProjectID) && failure.Status == test.Status &&
		archiveRunTimestampMatches(failure.StartTime, test.CreatedAt, tr.testStore) && archiveRunTimestampMatches(failure.EndTime, *test.EndedAt, tr.testStore) {
		test.ArchiveFailure = cloneRunArchiveFailure(failure)
	}
}

func cloneRunArchiveFailure(failure *storage.RunArchiveFailure) *storage.RunArchiveFailure {
	if failure == nil {
		return nil
	}
	copy := *failure
	return &copy
}
