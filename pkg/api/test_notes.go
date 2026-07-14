package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const testNotesArchiveFileName = "test_notes.json"

type testNotesArchiveArtifact struct {
	TestID string              `json:"test_id"`
	Notes  []*storage.TestNote `json:"notes"`
}

func newTestNoteID() string {
	return uuid.New().String()
}

func writeTestNotesArtifact(archivePath string, testID string, notes []*storage.TestNote) (string, error) {
	if strings.TrimSpace(archivePath) == "" {
		return "", fmt.Errorf("archive path is required")
	}
	if notes == nil {
		notes = []*storage.TestNote{}
	}
	if err := writeArchiveJSONArtifactDurable(archivePath, testNotesArchiveFileName, testNotesArchiveArtifact{
		TestID: strings.TrimSpace(testID),
		Notes:  notes,
	}); err != nil {
		return "", err
	}
	return testNotesArchiveFileName, nil
}

func (s *Server) scheduleTestNotesArchiveRefresh(testID string) {
	if s == nil || s.runner == nil {
		return
	}
	outbox := s.runner.archiveRefreshOutbox()
	if outbox == nil {
		slog.Warn("test notes archive refresh is not durable with the configured store", "test_id", testID)
		return
	}
	job, err := outbox.GetArchiveRefreshJob(testID)
	if errors.Is(err, sql.ErrNoRows) {
		// Non-production/custom stores may not implement atomic note+dirty writes.
		// Fill that compatibility gap synchronously; the SQLite/Postgres path has
		// already committed this row with the note mutation.
		job, err = outbox.BumpArchiveRefreshJob(testID)
	}
	if err != nil {
		if !errors.Is(err, storage.ErrArchiveCleanupPending) && !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("failed to enqueue test notes archive refresh", "test_id", testID, "err", err)
		}
		return
	}
	if s.archiveFinalizationCancel != nil {
		s.wakeArchiveFinalizationRetry()
		return
	}
	if !s.runner.isExecutionLeader() || job == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), archiveFinalizationAttemptTimeout)
	defer cancel()
	if err := s.runner.processArchiveRefreshJob(ctx, job); err != nil {
		slog.Warn("test notes archive refresh remains durably queued", "test_id", testID, "err", err)
	}
}

// replaceRunArchiveManifestTrust recomputes the archive manifest trust anchor and
// publishes it by swapping in a fresh record pointer under tr.mu, so a concurrent
// reader that already holds the archive record pointer (verify / report) never
// observes a torn in-place ManifestSHA256 mutation.
func (tr *TestRunner) replaceRunArchiveManifestTrust(testID string) error {
	tr.mu.RLock()
	id := tr.resolveRefKeyLocked(testID)
	record, ok := tr.archives[id]
	manifestPath := ""
	if ok && record != nil {
		manifestPath = strings.TrimSpace(record.ManifestPath)
	}
	tr.mu.RUnlock()
	if manifestPath == "" {
		return nil
	}
	sum, err := sha256File(manifestPath)
	if err != nil {
		return err
	}
	tr.mu.Lock()
	current, ok := tr.archives[id]
	if !ok || current == nil {
		tr.mu.Unlock()
		return nil
	}
	updated := *current // RunArchiveRecord is a flat value struct: this is a full copy
	updated.ManifestSHA256 = sum
	tr.archives[id] = &updated
	if test, exists := tr.tests[id]; exists {
		test.Archive = &updated
	}
	tr.mu.Unlock()
	return tr.saveRunArchiveRecord(&updated)
}
