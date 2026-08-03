package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob/fileblob"
)

func TestImportedLibraryObjectArchiveSurvivesControllerRestart(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "private-archives")
	t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "prod/evidence")
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", "https://objects.example.test")

	const (
		importID = "import-durable"
		sourceID = "source-run"
	)
	sourceDir := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifactName := "transactions.jsonl"
	artifactData := []byte("{\"duration_ms\":42}\n")
	if err := os.WriteFile(filepath.Join(sourceDir, artifactName), artifactData, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		SchemaVersion:    archiveSchemaVersion,
		TestID:           sourceID,
		TestName:         "Imported durable result",
		Status:           "COMPLETE",
		CreatedAt:        now.Add(-time.Minute),
		CompletedAt:      now,
		TransactionsFile: artifactName,
		TransactionCount: 1,
	}
	if err := updateRunArchiveArtifactIntegrity(sourceDir, &manifest); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(sourceDir, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	record, err := buildImportedLibraryArchiveRecord(importID, sourceDir, "unused", hashArchiveManifestPath(manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(record.ArchiveURI, "/library-imports/"+importID+"/"+archiveSourceSegment+"/"+archiveSourceSchema+"/") {
		t.Fatalf("archive URI does not retain an import-scoped source identity: %q", record.ArchiveURI)
	}

	bucket, err := fileblob.OpenBucket(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	publication := *record
	publication.ArchivePath = sourceDir
	publication.ManifestPath = manifestPath
	names, err := archivePublicationArtifactNames(context.Background(), &publication)
	if err != nil {
		t.Fatal(err)
	}
	backend := libraryImportedArchiveBackend(importID)
	if err := uploadArchiveObjectsToBucket(context.Background(), bucket, backend, sourceDir, sourceID, names...); err != nil {
		t.Fatal(err)
	}

	databasePath := filepath.Join(t.TempDir(), "restart.db")
	store, err := storage.NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateExternalResultImportIfAbsent(&storage.ExternalResultImport{
		ID: importID, Provider: "apexload", Name: "durable", ProjectID: storage.DefaultProjectID,
		RawResult: map[string]any{"source_item_id": sourceID}, ArchiveRecord: record, CreatedAt: now,
	})
	if err != nil || !created {
		_ = store.Close()
		t.Fatalf("persist imported result = %t, %v", created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sourceDir); err != nil {
		t.Fatal(err)
	}

	reopened, err := storage.NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := &Server{store: reopened}
	_, recovered, recoveredSourceID, err := restarted.importedLibraryResultArchive(importID)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredSourceID != sourceID || recovered == nil || recovered.ArchiveURI != record.ArchiveURI {
		t.Fatalf("recovered archive = %#v, source = %q", recovered, recoveredSourceID)
	}
	materializationBackend, err := archiveMaterializationRuntime(currentArchiveBackend(), recovered)
	if err != nil {
		t.Fatalf("resolve persisted object archive after restart: %v", err)
	}
	if err := materializeArchiveFromBucket(context.Background(), bucket, materializationBackend, recovered); err != nil {
		t.Fatalf("materialize persisted object archive after restart: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(recovered.ArchivePath, artifactName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(artifactData) {
		t.Fatalf("materialized artifact = %q, want %q", got, artifactData)
	}
}
