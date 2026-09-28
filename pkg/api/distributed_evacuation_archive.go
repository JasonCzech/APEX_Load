package api

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// The pointer is an in-process incarnation token, matching the runner's ownership
// checks. A pending worker for a replaced run must not suppress its successor.
func evacuationArchiveRetryKey(test *ManagedTest) string {
	return fmt.Sprintf("evacuation-archive:%s:%p", test.ID, test)
}

func (b *distributedExecutionBackend) finalizeFailClosedEvacuation(tr *TestRunner, test *ManagedTest) error {
	unlock := tr.lockArchiveCommitFence(test.ID)
	defer unlock()
	// An existing worker owns the pre-compaction history snapshot. A duplicate
	// stop-cleanup callback must not start a competing collection with a trimmed
	// history while that worker is waiting for storage recovery.
	retryKey := evacuationArchiveRetryKey(test)
	tr.terminalRetryMu.Lock()
	_, pending := tr.terminalRetrying[retryKey]
	tr.terminalRetryMu.Unlock()
	if pending {
		return nil
	}
	tr.mu.RLock()
	owned := tr.tests[test.ID] == test
	finalError := test.Error
	tr.mu.RUnlock()
	if !owned {
		return nil
	}
	completedAt := time.Now()
	// Keep pre-compaction evidence alive across a transient terminal-write failure.
	history := snapshotRemoteArchiveTargetHistory(tr, test)
	retry, err := b.finalizeEvacuationArchiveUnderFence(tr, test, completedAt, finalError, history)
	if retry {
		tr.enqueueTerminalRetry(retryKey, "evacuation archive after terminal commit", func() error {
			unlock := tr.lockArchiveCommitFence(test.ID)
			defer unlock()
			retry, err := b.finalizeEvacuationArchiveUnderFence(tr, test, completedAt, finalError, history)
			if !retry {
				if err != nil {
					slog.Warn("evacuation archive attempt finished", "test_id", test.ID, "error", err)
				}
				return nil
			}
			return err
		})
	}
	return err
}

// The caller holds the archive commit fence. Terminal-write failures and
// pre-collection storage checks are retried here; collection is best effort,
// with a durable failure record when unavailable. Publication already has its own durable outbox and retry worker.
func (b *distributedExecutionBackend) finalizeEvacuationArchiveUnderFence(tr *TestRunner, test *ManagedTest, completedAt time.Time, finalError string, history []remoteArchiveTargetSnapshot) (bool, error) {
	tr.mu.RLock()
	eligible := tr.tests[test.ID] == test && test.Archive == nil && (test.Status == "STOPPING" || test.Status == "FAILED")
	tr.mu.RUnlock()
	if !eligible {
		return false, nil
	}
	completionErr := tr.completeTestRunUnderArchiveFence(test, "FAILED", completedAt, finalError, nil)
	tr.mu.RLock()
	owned := tr.tests[test.ID] == test
	terminal := owned && test.Status == "FAILED" && test.EndedAt != nil
	if terminal {
		// A completion worker or earlier callback may have committed first. Preserve
		// that exact incarnation and outcome rather than inventing a new end time.
		completedAt, finalError = *test.EndedAt, test.Error
	}
	tr.mu.RUnlock()
	if !owned {
		return false, nil
	}
	if !terminal {
		if completionErr != nil {
			return true, completionErr
		}
		// A permanently fenced/deleted transition is cancellation, not permission to
		// publish an archive. Its old transports can now be closed without collecting.
		b.closeRemoteResources(tr, test)
		return false, nil
	}
	// An earlier callback may have committed publication without attaching the
	// catalog yet. Recollection must not replace files used by that durable job.
	if outbox := tr.archiveFinalizationOutbox(); outbox != nil {
		job, err := outbox.GetArchiveFinalizationJob(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return true, fmt.Errorf("read evacuation archive publication intent: %w", err)
		}
		if job != nil {
			b.closeRemoteResources(tr, test)
			return false, completionErr
		}
	}
	if tr.archiveStore != nil {
		record, err := tr.archiveStore.GetRunArchive(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return true, fmt.Errorf("read evacuation archive catalog: %w", err)
		}
		if record != nil {
			b.closeRemoteResources(tr, test)
			return false, completionErr
		}
	}
	// Only an observed best-effort failure finishes collection. The atomic
	// unconfirmed marker predates collection and must allow this attempt to run.
	// A stop-cleanup retry must not redial sources after an observed outcome.
	if store := tr.archiveFailureStore(); store != nil {
		failure, err := store.GetRunArchiveFailure(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return true, fmt.Errorf("read evacuation archive failure: %w", err)
		}
		if failure != nil && (failure.Reason == storage.RunArchiveFailureRemoteCollection || failure.Reason == storage.RunArchiveFailureRemoteUnavailable) {
			b.closeRemoteResources(tr, test)
			return false, completionErr
		}
	}
	archiveRecord, archiveErr := b.stageRemoteArchiveWithTargetHistory(tr, test, "FAILED", finalError, completedAt, false, history)
	// Retain the transports until a terminal commit and collection attempt finish.
	b.closeRemoteResources(tr, test)
	if archiveRecord != nil {
		tr.persistAndAttachRunArchiveUnderFence(test, archiveRecord)
	}
	if archiveErr != nil {
		tr.recordRemoteArchiveFailure(test, storage.RunArchiveFailureRemoteCollection)
		archiveErr = fmt.Errorf("collect fail-closed evacuation archive: %w", archiveErr)
	} else if archiveRecord == nil {
		tr.recordRemoteArchiveFailure(test, storage.RunArchiveFailureRemoteUnavailable)
	}
	return false, errors.Join(completionErr, archiveErr)
}
