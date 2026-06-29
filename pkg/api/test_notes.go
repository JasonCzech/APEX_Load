package api

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
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
	if err := writeIndentedJSONFile(filepath.Join(archivePath, testNotesArchiveFileName), testNotesArchiveArtifact{
		TestID: strings.TrimSpace(testID),
		Notes:  notes,
	}); err != nil {
		return "", err
	}
	return testNotesArchiveFileName, nil
}

func (s *Server) syncTestNotesToArchive(testID string) {
	if s == nil || s.runner == nil || s.store == nil {
		return
	}
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
	if err := updateRunArchiveArtifactIntegrity(record.ArchivePath, manifest); err != nil {
		slog.Warn("failed to update archive artifact integrity for test notes sync", "test_id", testID, "err", err)
		return
	}
	if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
		slog.Warn("failed to update archive manifest for test notes sync", "test_id", testID, "err", err)
		return
	}
	if err := uploadArchiveObjects(context.Background(), record.ArchivePath, record.TestID, testNotesFile, runArchiveManifestFileName); err != nil {
		slog.Warn("failed to upload archive after test notes sync", "test_id", testID, "err", err)
	}
}

func (s *Server) syncTestNotesToArchiveAsync(testID string) {
	go s.syncTestNotesToArchive(testID)
}
