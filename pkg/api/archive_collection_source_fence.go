package api

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Called under the source file fence. A duplicate completion or stale collector
// must not replace source files that already belong to publication or retention.
func (tr *TestRunner) existingCollectedArchive(test *ManagedTest) (*storage.RunArchiveRecord, error) {
	if tr == nil || test == nil {
		return nil, nil
	}
	if tr.testStore != nil {
		row, err := tr.testStore.GetTest(test.ID)
		if err != nil {
			return nil, err
		}
		if row == nil || row.ID != test.ID || normalizeProjectID(row.ProjectID) != normalizeProjectID(test.ProjectID) || !archiveRunTimestampMatches(row.StartTime, test.CreatedAt, tr.testStore) || !sameArchiveRunIncarnation(test, row) {
			return nil, fmt.Errorf("archive collection belongs to a different durable run")
		}
	}
	if cleanup, ok := tr.archiveStore.(archiveCleanupOutbox); ok {
		job, err := cleanup.GetArchiveCleanupJob(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if job != nil {
			return nil, storage.ErrArchiveCleanupPending
		}
	}
	intent, err := tr.runArchiveCollectionIntent(test)
	if err != nil {
		return nil, err
	}
	if outbox := tr.archiveFinalizationOutbox(); outbox != nil {
		job, err := outbox.GetArchiveFinalizationJob(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if job != nil {
			if job.TestID != test.ID || job.ArchiveRecord.TestID != test.ID || normalizeProjectID(job.ProjectID) != normalizeProjectID(test.ProjectID) {
				return nil, fmt.Errorf("existing archive publication belongs to another run")
			}
			record := job.ArchiveRecord
			return &record, nil
		}
	}
	if tr.archiveStore != nil {
		record, err := tr.archiveStore.GetRunArchive(test.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if record != nil {
			return record, nil
		}
	}
	if intent != nil && intent.Resolved {
		return nil, storage.ErrArchiveCleanupPending
	}
	return nil, nil
}
