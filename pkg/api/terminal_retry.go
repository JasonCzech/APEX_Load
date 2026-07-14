package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// enqueueTerminalRetry keeps a single reconciliation worker per test. The
// operation must be idempotent and re-check current in-memory state before
// applying side effects; completeTestRun, failTestBeforeRun, and the scheduled
// pre-launch failure transition all satisfy that contract. Workers run only on
// the execution leader and stop after shutdown admission closes.
func (tr *TestRunner) enqueueTerminalRetry(testID, transition string, operation func() error) {
	if tr == nil || testID == "" || operation == nil || !tr.beginTerminalRetry(testID) {
		return
	}
	go func() {
		defer tr.endTerminalRetry(testID)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed() {
			if !tr.waitExecutionPlanRetry(delay) {
				return
			}
			if err := operation(); err == nil {
				return
			} else {
				slog.Warn("terminal transition retry failed", "test_id", testID, "transition", transition, "error", err)
				if !durableBackgroundRetryable(err) {
					return
				}
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}

func durableBackgroundRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrConnDone) {
		return false
	}
	if errors.Is(err, errExecutionPlanRunConflict) {
		return false
	}
	message := strings.ToLower(err.Error())
	return !strings.Contains(message, "database is closed") && !strings.Contains(message, "sql: database is closed")
}

func (tr *TestRunner) beginTerminalRetry(testID string) bool {
	tr.terminalRetryMu.Lock()
	defer tr.terminalRetryMu.Unlock()
	if tr.terminalRetrying == nil {
		tr.terminalRetrying = make(map[string]struct{})
	}
	if _, exists := tr.terminalRetrying[testID]; exists {
		return false
	}
	tr.terminalRetrying[testID] = struct{}{}
	return true
}

func (tr *TestRunner) endTerminalRetry(testID string) {
	tr.terminalRetryMu.Lock()
	delete(tr.terminalRetrying, testID)
	tr.terminalRetryMu.Unlock()
}

func (tr *TestRunner) enqueueTestCompletionRetry(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) {
	if test == nil {
		return
	}
	tr.enqueueTerminalRetry(test.ID, finalStatus, func() error {
		return tr.completeTestRun(test, finalStatus, endTime, finalError, archiveRecord)
	})
}

func (tr *TestRunner) enqueueRunArchiveRecordRetry(test *ManagedTest, archiveRecord *storage.RunArchiveRecord) {
	tr.enqueueRunArchiveFinalizationRetry(test, archiveRecord, false)
}

func (tr *TestRunner) enqueueRunArchiveFinalizationRetry(test *ManagedTest, archiveRecord *storage.RunArchiveRecord, persistArtifacts bool) {
	if tr == nil || test == nil || archiveRecord == nil {
		return
	}
	record := *archiveRecord
	tr.enqueueTerminalRetry("archive:"+test.ID, "archive_finalize", func() error {
		unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
		defer unlockArchiveCommit()

		tr.mu.RLock()
		current := tr.tests[test.ID]
		if current == nil || current != test {
			tr.mu.RUnlock()
			return nil
		}
		terminal := isTerminalTestStatus(current.Status)
		tr.mu.RUnlock()
		if !terminal {
			return fmt.Errorf("test %s is not durably terminal yet", test.ID)
		}
		publishCtx, cancel := context.WithTimeout(context.Background(), archiveFinalizationAttemptTimeout)
		defer cancel()
		return tr.finalizeRunArchiveDurablyUnderFence(publishCtx, test, &record, persistArtifacts)
	})
}
