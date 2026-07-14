package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	archiveDir := filepath.Join(archiveRoot, testID)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("create archive directory: %v", err)
	}
	for _, name := range []string{"transactions.jsonl", "resource_samples.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("write archive dataset %s: %v", name, err)
		}
	}
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
	var published *storage.RunArchiveRecord
	waitForTestCondition(t, func() bool {
		record, err := store.GetRunArchive(testID)
		if err != nil || record == nil || record.ManifestSHA256 == "" {
			return false
		}
		loaded, err := loadRunArchiveManifest(record.ManifestPath)
		if err != nil {
			return false
		}
		published = record
		manifest = loaded
		return loaded.TestNotesFile == testNotesArchiveFileName
	})
	if manifest.TestNotesFile != testNotesArchiveFileName {
		t.Fatalf("manifest test_notes_file = %q, want %q", manifest.TestNotesFile, testNotesArchiveFileName)
	}
	var artifact testNotesArchiveArtifact
	data, err := os.ReadFile(filepath.Join(published.ArchivePath, testNotesArchiveFileName))
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

func TestTestNoteMutationDoesNotReblessTamperedArchiveArtifacts(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	testID := "tamper-notes-run"
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	archiveDir := filepath.Join(archiveRoot, testID)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("create archive directory: %v", err)
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	originalTransactions := []byte(`{"name":"original"}` + "\n")
	if err := os.WriteFile(filepath.Join(archiveDir, "transactions.jsonl"), originalTransactions, 0o644); err != nil {
		t.Fatalf("write transactions: %v", err)
	}
	for _, name := range []string{"resource_samples.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("write archive dataset %s: %v", name, err)
		}
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID:        testID,
		Name:      "Tamper Notes Run",
		Status:    "COMPLETE",
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	record := &storage.RunArchiveRecord{
		TestID:       testID,
		ArchivePath:  archiveDir,
		ManifestPath: manifestPath,
		CreatedAt:    time.Now().UTC(),
		CompletedAt:  time.Now().UTC(),
	}
	originalSum := sha256.Sum256(originalTransactions)
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              "Tamper Notes Run",
		CreatedAt:             time.Now().UTC(),
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resource_samples.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		Artifacts: []RunArchiveArtifact{{
			Name:       "transactions.jsonl",
			Kind:       "dataset",
			Format:     "jsonl",
			RecordType: "transactions",
			SizeBytes:  int64(len(originalTransactions)),
			SHA256:     fmt.Sprintf("%x", originalSum[:]),
		}},
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatalf("SaveRunArchive returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "transactions.jsonl"), []byte(`{"name":"tampered"}`+"\n"), 0o644); err != nil {
		t.Fatalf("tamper transactions: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	body, _ := json.Marshal(testNoteRequest{Body: "tamper should remain visible"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body=%s)", resp.Code, resp.Body.String())
	}

	refresh, err := store.GetArchiveRefreshJob(testID)
	if err != nil {
		t.Fatalf("GetArchiveRefreshJob returned error: %v", err)
	}
	finalization, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatalf("GetArchiveFinalizationJob returned error: %v", err)
	}
	if finalization.RefreshToken != refresh.IntentToken {
		t.Fatalf("durable retry refresh token = %q, want %q", finalization.RefreshToken, refresh.IntentToken)
	}

	manifest, err := loadRunArchiveManifest(manifestPath)
	if err != nil {
		t.Fatalf("loadRunArchiveManifest returned error: %v", err)
	}
	if manifest.TestNotesFile != "" {
		t.Fatalf("tampered archive was mutated with test_notes_file %q", manifest.TestNotesFile)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, testNotesArchiveFileName)); !os.IsNotExist(err) {
		t.Fatalf("tampered archive notes artifact stat = %v, want not created", err)
	}

	var txArtifact *RunArchiveArtifact
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Name == "transactions.jsonl" {
			txArtifact = &manifest.Artifacts[i]
			break
		}
	}
	if txArtifact == nil {
		t.Fatalf("transactions artifact disappeared from manifest: %#v", manifest.Artifacts)
	}
	if txArtifact.SHA256 != fmt.Sprintf("%x", originalSum[:]) {
		t.Fatalf("transactions hash was re-blessed to %s, want original %x", txArtifact.SHA256, originalSum)
	}
	persistedRecord, err := store.GetRunArchive(testID)
	if err != nil {
		t.Fatalf("GetRunArchive returned error: %v", err)
	}
	if persistedRecord.ManifestSHA256 != "" {
		t.Fatalf("tampered archive manifest was re-blessed with trust anchor %q", persistedRecord.ManifestSHA256)
	}
	report := verifyRunArchiveIntegrity(persistedRecord, manifest)
	if report.Status != integrityStatusTampered || report.Mismatched != 1 {
		t.Fatalf("integrity report after note sync = %#v, want tampered mismatch", report)
	}
}

func TestRunArchiveIntegrityDetectsManifestReblessing(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	testID := "manifest-rebless-run"
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, runArchiveManifestFileName)
	transactionsPath := filepath.Join(archiveDir, "transactions.jsonl")
	if err := store.SaveTest(&storage.TestRecord{
		ID:        testID,
		Name:      "Manifest Rebless Run",
		Status:    "COMPLETE",
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	originalTransactions := []byte(`{"name":"original"}` + "\n")
	if err := os.WriteFile(transactionsPath, originalTransactions, 0o644); err != nil {
		t.Fatalf("write transactions: %v", err)
	}
	originalSum := sha256.Sum256(originalTransactions)
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TestName:              "Manifest Rebless Run",
		CreatedAt:             time.Now().UTC(),
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resource_samples.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		Artifacts: []RunArchiveArtifact{{
			Name:       "transactions.jsonl",
			Kind:       "dataset",
			Format:     "jsonl",
			RecordType: "transactions",
			SizeBytes:  int64(len(originalTransactions)),
			SHA256:     fmt.Sprintf("%x", originalSum[:]),
		}},
	}
	if err := writeIndentedJSONFile(manifestPath, manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifestHash, err := sha256File(manifestPath)
	if err != nil {
		t.Fatalf("hash manifest: %v", err)
	}
	record := &storage.RunArchiveRecord{
		TestID:         testID,
		ArchivePath:    archiveDir,
		ManifestPath:   manifestPath,
		ManifestSHA256: manifestHash,
		CreatedAt:      time.Now().UTC(),
		CompletedAt:    time.Now().UTC(),
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatalf("SaveRunArchive returned error: %v", err)
	}

	tamperedTransactions := []byte(`{"name":"tampered"}` + "\n")
	if err := os.WriteFile(transactionsPath, tamperedTransactions, 0o644); err != nil {
		t.Fatalf("tamper transactions: %v", err)
	}
	tamperedSum := sha256.Sum256(tamperedTransactions)
	manifest.Artifacts[0].SHA256 = fmt.Sprintf("%x", tamperedSum[:])
	manifest.Artifacts[0].SizeBytes = int64(len(tamperedTransactions))
	if err := writeIndentedJSONFile(manifestPath, manifest); err != nil {
		t.Fatalf("write tampered manifest: %v", err)
	}

	loaded, err := loadRunArchiveManifest(manifestPath)
	if err != nil {
		t.Fatalf("load tampered manifest: %v", err)
	}
	report := verifyRunArchiveIntegrity(record, loaded)
	if report.Status != integrityStatusTampered || report.Mismatched != 1 {
		t.Fatalf("integrity report = %#v, want manifest tampering", report)
	}
	if len(report.Artifacts) == 0 || report.Artifacts[0].Name != runArchiveManifestFileName || report.Artifacts[0].Status != integrityArtifactMismatch {
		t.Fatalf("first artifact report = %#v, want manifest mismatch", report.Artifacts)
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
	// Generous deadline: these conditions wait on an async archive-sync goroutine
	// doing file + SQLite I/O, which can be starved under the full parallel test
	// load (`go test ./...`). A large deadline only lengthens the genuine-failure
	// path — a met condition returns immediately.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
