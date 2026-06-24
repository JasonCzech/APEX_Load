package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestTestNoteMutationSyncsArchiveArtifact(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	testID := "sync-notes-run"
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	if err := store.SaveTest(&storage.TestRecord{
		ID:        testID,
		Name:      "Sync Notes Run",
		Status:    "COMPLETE",
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              "Sync Notes Run",
		CreatedAt:             time.Now().UTC(),
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resource_samples.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{
		TestID:       testID,
		ArchivePath:  archiveDir,
		ManifestPath: manifestPath,
		CreatedAt:    time.Now().UTC(),
		CompletedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveRunArchive returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	body, _ := json.Marshal(testNoteRequest{Body: "# Synced\n\npost-run note"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body=%s)", resp.Code, resp.Body.String())
	}

	var manifest *RunArchiveManifest
	waitForTestCondition(t, func() bool {
		loaded, err := loadRunArchiveManifest(manifestPath)
		if err != nil {
			return false
		}
		manifest = loaded
		return loaded.TestNotesFile == testNotesArchiveFileName
	})
	if manifest.TestNotesFile != testNotesArchiveFileName {
		t.Fatalf("manifest test_notes_file = %q, want %q", manifest.TestNotesFile, testNotesArchiveFileName)
	}
	var artifact testNotesArchiveArtifact
	data, err := os.ReadFile(filepath.Join(archiveDir, testNotesArchiveFileName))
	if err != nil {
		t.Fatalf("read synced artifact: %v", err)
	}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("decode synced artifact: %v", err)
	}
	if artifact.TestID != testID || len(artifact.Notes) != 1 || artifact.Notes[0].Body != "# Synced\n\npost-run note" {
		t.Fatalf("synced artifact = %#v", artifact)
	}
}

func TestRunArchiveDownloadsTestNotesArtifact(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	testID := "download-notes-run"
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	if err := store.SaveTest(&storage.TestRecord{
		ID:        testID,
		Name:      "Download Notes Run",
		Status:    "COMPLETE",
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	if err := writeIndentedJSONFile(filepath.Join(archiveDir, testNotesArchiveFileName), testNotesArchiveArtifact{
		TestID: testID,
		Notes:  []*storage.TestNote{{ID: "n1", TestID: testID, Body: "download me"}},
	}); err != nil {
		t.Fatalf("write notes artifact: %v", err)
	}
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              "Download Notes Run",
		CreatedAt:             time.Now().UTC(),
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resource_samples.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TestNotesFile:         testNotesArchiveFileName,
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{
		TestID:       testID,
		ArchivePath:  archiveDir,
		ManifestPath: manifestPath,
		CreatedAt:    time.Now().UTC(),
		CompletedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveRunArchive returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/files/"+testNotesArchiveFileName, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200 (body=%s)", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "download me") {
		t.Fatalf("download body = %q, want notes artifact", resp.Body.String())
	}
}

func TestRunArchiveDownloadMissingTestNotesArtifactUsesJSONError(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	testID := "missing-notes-artifact-run"
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	if err := store.SaveTest(&storage.TestRecord{
		ID:        testID,
		Name:      "Missing Notes Artifact Run",
		Status:    "COMPLETE",
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              "Missing Notes Artifact Run",
		CreatedAt:             time.Now().UTC(),
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resource_samples.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TestNotesFile:         testNotesArchiveFileName,
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{
		TestID:       testID,
		ArchivePath:  archiveDir,
		ManifestPath: manifestPath,
		CreatedAt:    time.Now().UTC(),
		CompletedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveRunArchive returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/files/"+testNotesArchiveFileName, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("download missing status = %d, want 404 (body=%s)", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %q, want application/json", resp.Header().Get("Content-Type"))
	}
	if !strings.Contains(resp.Body.String(), "archive artifact not found") {
		t.Fatalf("body = %q, want JSON archive artifact error", resp.Body.String())
	}
}

func waitForTestCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
