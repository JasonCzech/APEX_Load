package api

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// RunArchiveRecovery describes durable recovery bookkeeping, not source-file
// availability. VerifiedSources counts historical receipts; attempts are budget
// claims and do not prove a network request or successful retrieval occurred.
type RunArchiveRecovery struct {
	State             string     `json:"state"`
	RecoveryAttempts  *int       `json:"recovery_attempts,omitempty"`
	ExpectedSources   *int       `json:"expected_sources,omitempty"`
	VerifiedSources   *int       `json:"verified_sources,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
	RetryWindowEndsAt *time.Time `json:"retry_window_ends_at,omitempty"`
}

// Single-run terminal reads only. Never probe local/object storage or generators:
// a follower's missing staging copy is not evidence that all sources are lost.
func (tr *TestRunner) attachRunArchiveRecovery(test *ManagedTest) {
	if test == nil {
		return
	}
	test.ArchiveRecovery = nil
	if tr.testStore == nil || test.EndedAt == nil || !isTerminalTestStatus(test.Status) {
		return
	}
	row, err := tr.testStore.GetTest(test.ID)
	if err != nil || !tr.archiveRecoveryRunMatches(test, row) {
		return
	}
	state := &RunArchiveRecovery{}
	if test.Archive != nil {
		state.State = "available"
	} else {
		sourceStore := tr.archiveCollectionStore()
		outbox := tr.archiveFinalizationOutbox()
		if sourceStore == nil || outbox == nil {
			return
		}
		intent, err := sourceStore.GetRunArchiveCollection(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return
		}
		if intent != nil && (intent.TestID != test.ID || normalizeProjectID(intent.ProjectID) != normalizeProjectID(row.ProjectID) || !archiveRunTimestampMatches(intent.StartTime, row.StartTime, tr.testStore)) {
			return
		}
		pending, err := outbox.GetArchiveFinalizationJob(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return
		}
		if pending != nil {
			// A generator-created timestamp is not the controller request identity.
			// Without prepared proof, leave progress unknown rather than guessing.
			if len(pending.PreparedManifestJSON) == 0 || len(pending.PreparedManifestJSON) > maxArchiveManifestDownloadBytes || len(pending.ArchiveRecord.ManifestSHA256) != 64 {
				return
			}
			if _, err := tr.validateArchiveFinalizationIdentity(pending, &pending.ArchiveRecord, row, []byte(pending.PreparedManifestJSON)); err != nil {
				return
			}
			state.State = "publication_pending"
		} else if intent != nil {
			switch {
			case intent.Resolved:
				// A temporarily unreadable catalog must not be described as
				// retention/removal. Its bytes may still exist on another backend.
				catalog, err := tr.archiveStore.GetRunArchive(test.ID)
				if (err != nil && !errors.Is(err, sql.ErrNoRows)) || catalog != nil {
					return
				}
				state.State = "unavailable"
			case intent.Exhausted:
				state.State = "exhausted"
			case intent.Attempts >= archiveCollectionNetworkAttempts || time.Since(*row.EndTime) >= archiveCollectionTerminalDeadline:
				state.State = "cache_recovery_pending"
			default:
				state.State = "collection_pending"
			}
		} else {
			return
		}
		if intent != nil {
			attempts, expected, verified := intent.Attempts, len(intent.Sources), 0
			for target := range intent.CachedSources {
				if _, ok := intent.Sources[target]; ok {
					verified++
				}
			}
			state.RecoveryAttempts = &attempts
			state.ExpectedSources = &expected
			state.VerifiedSources = &verified
			state.UpdatedAt = cloneTimePtr(&intent.UpdatedAt)
			deadline := row.EndTime.Add(archiveCollectionTerminalDeadline)
			state.RetryWindowEndsAt = &deadline
		}
	}
	// Detect deletion/reuse during the independent metadata reads. Project/request
	// identity and terminal outcome must all still match the returned snapshot.
	current, err := tr.testStore.GetTest(test.ID)
	if err != nil || !tr.archiveRecoveryRunMatches(test, current) {
		return
	}
	test.ArchiveRecovery = state
}

func (tr *TestRunner) archiveRecoveryRunMatches(test *ManagedTest, row *storage.TestRecord) bool {
	return test != nil && row != nil && row.ID == test.ID && test.EndedAt != nil && row.EndTime != nil &&
		isTerminalTestStatus(row.Status) && row.Status == test.Status && normalizeProjectID(row.ProjectID) == normalizeProjectID(test.ProjectID) &&
		archiveRunTimestampMatches(row.StartTime, test.CreatedAt, tr.testStore) && archiveRunTimestampMatches(*row.EndTime, *test.EndedAt, tr.testStore)
}

func cloneRunArchiveRecovery(status *RunArchiveRecovery) *RunArchiveRecovery {
	if status == nil {
		return nil
	}
	copy := *status
	copy.RecoveryAttempts = cloneIntPtr(status.RecoveryAttempts)
	copy.ExpectedSources = cloneIntPtr(status.ExpectedSources)
	copy.VerifiedSources = cloneIntPtr(status.VerifiedSources)
	copy.UpdatedAt = cloneTimePtr(status.UpdatedAt)
	copy.RetryWindowEndsAt = cloneTimePtr(status.RetryWindowEndsAt)
	return &copy
}
