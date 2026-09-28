package api

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Producer retries may hold a record older than the durable retry worker's
// adjunct writes. Never replace its token, manifest hash, or refreshed source
// with that stale copy. The create-only transaction also prevents recreation
// after a catalog commit. Refresh publication has its own explicit token path.
func (tr *TestRunner) prepareProducerArchiveIntent(outbox archiveFinalizationOutbox, job *storage.ArchiveFinalizationJob) (bool, error) {
	creator, ok := tr.archiveStore.(orphanArchiveFinalizationStore)
	if !ok {
		return false, retryDurableTransition(func() error { return outbox.SaveArchiveFinalizationJob(job) })
	}
	created := false
	err := retryDurableTransition(func() error {
		var err error
		created, err = creator.SaveArchiveFinalizationJobIfOrphan(job)
		return err
	})
	if err != nil {
		return false, err
	}
	if created {
		return false, nil
	}
	current, err := outbox.GetArchiveFinalizationJob(job.TestID)
	if err == nil && current != nil {
		if normalizeProjectID(current.ProjectID) != normalizeProjectID(job.ProjectID) || !tr.sameProducerArchiveRun(&job.ArchiveRecord, &current.ArchiveRecord) {
			return false, fmt.Errorf("current archive intent belongs to a different run identity")
		}
		*job = *current
		return false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	catalog, err := tr.archiveStore.GetRunArchive(job.TestID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && catalog == nil) {
		return false, fmt.Errorf("archive publication was superseded or cancelled")
	}
	if err != nil {
		return false, err
	}
	if !tr.sameProducerArchiveRun(&job.ArchiveRecord, catalog) {
		return false, fmt.Errorf("archive catalog belongs to a different run identity")
	}
	job.ArchiveRecord = *catalog
	return true, nil
}

func (tr *TestRunner) sameProducerArchiveRun(left, right *storage.RunArchiveRecord) bool {
	return left != nil && right != nil && left.TestID == right.TestID && !left.CreatedAt.IsZero() && !left.CompletedAt.IsZero() &&
		archiveRunTimestampMatches(left.CreatedAt, right.CreatedAt, tr.testStore) && archiveRunTimestampMatches(left.CompletedAt, right.CompletedAt, tr.testStore)
}
