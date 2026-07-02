package api

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

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
	if err := writeIndentedJSONFile(filepath.Join(archivePath, testNotesArchiveFileName), testNotesArchiveArtifact{
		TestID: strings.TrimSpace(testID),
		Notes:  notes,
	}); err != nil {
		return "", err
	}
	return testNotesArchiveFileName, nil
}

func (s *Server) testNotesArchiveLock(testID string) *sync.Mutex {
	testID = strings.TrimSpace(testID)
	s.testNotesArchiveMu.Lock()
	defer s.testNotesArchiveMu.Unlock()
	if s.testNotesArchiveLocks == nil {
		s.testNotesArchiveLocks = make(map[string]*sync.Mutex)
	}
	lock := s.testNotesArchiveLocks[testID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.testNotesArchiveLocks[testID] = lock
	}
	return lock
}

func (s *Server) syncTestNotesToArchive(testID string) {
	if s == nil || s.runner == nil || s.store == nil {
		return
	}
	lock := s.testNotesArchiveLock(testID)
	lock.Lock()
	defer lock.Unlock()

	record, ok := s.runner.GetRunArchive(testID)
	if !ok || record == nil {
		// Running tests are not archived yet; persistTestManifestArtifact captures
		// all current notes when the run archive is finalized.
		return
	}
	notes, err := s.store.ListTestNotes(testID)
	if err != nil {
		slog.Warn("failed to load test notes for archive sync", "test_id", testID, "err", err)
		return
	}
	testNotesFile, err := writeTestNotesArtifact(record.ArchivePath, testID, notes)
	if err != nil {
		slog.Warn("failed to write test notes archive artifact", "test_id", testID, "err", err)
		return
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		slog.Warn("failed to load archive manifest for test notes sync", "test_id", testID, "err", err)
		return
	}
	manifest.TestNotesFile = testNotesFile
	if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{
		Name:       testNotesFile,
		Kind:       "notes",
		Format:     "json",
		RecordType: "test_notes",
		Count:      int64(len(notes)),
	}); err != nil {
		slog.Warn("failed to update archive artifact integrity for test notes sync", "test_id", testID, "err", err)
		return
	}
	if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
		slog.Warn("failed to update archive manifest for test notes sync", "test_id", testID, "err", err)
		return
	}
	// Publish the refreshed trust anchor via copy-and-swap rather than mutating the
	// shared archive record in place: concurrent verify/report readers hold the
	// record pointer lock-free (GetRunArchive returns the cached pointer), so an
	// in-place ManifestSHA256 write would be a data race (RN-3).
	if err := s.runner.replaceRunArchiveManifestTrust(testID); err != nil {
		slog.Warn("failed to update archive manifest trust anchor for test notes sync", "test_id", testID, "err", err)
		return
	}
	if err := uploadArchiveObjects(context.Background(), record.ArchivePath, record.TestID, testNotesFile, runArchiveManifestFileName); err != nil {
		slog.Warn("failed to upload archive after test notes sync", "test_id", testID, "err", err)
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

func (s *Server) syncTestNotesToArchiveAsync(testID string) {
	go s.syncTestNotesToArchive(testID)
}
