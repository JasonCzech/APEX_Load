package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExternalResultArchiveRecordSurvivesStoreRestartWithoutAPIDisclosure(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "external-result-archive.db")
	store, err := NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	wantRecord := &RunArchiveRecord{
		TestID:         "source-run",
		SchemaVersion:  "1.0",
		ArchiveBackend: "s3",
		ArchiveURI:     "s3://private-bucket/evidence/library-imports/import-1/source-run",
		ManifestURI:    "s3://private-bucket/evidence/library-imports/import-1/source-run/manifest.json",
		ManifestSHA256: strings.Repeat("a", 64),
		CreatedAt:      now.Add(-time.Minute),
		CompletedAt:    now,
	}
	result := &ExternalResultImport{
		ID: "import-1", Provider: "apexload", Name: "durable archive", ProjectID: DefaultProjectID,
		RawResult: map[string]any{"source_item_id": "source-run"}, ArchiveRecord: wantRecord, CreatedAt: now,
	}
	created, err := store.CreateExternalResultImportIfAbsent(result)
	if err != nil || !created {
		_ = store.Close()
		t.Fatalf("CreateExternalResultImportIfAbsent = %t, %v", created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.GetExternalResultImport(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ArchiveRecord == nil || stored.ArchiveRecord.ArchiveURI != wantRecord.ArchiveURI || stored.ArchiveRecord.ManifestURI != wantRecord.ManifestURI || stored.ArchiveRecord.ManifestSHA256 != wantRecord.ManifestSHA256 {
		t.Fatalf("archive record after restart = %#v, want %#v", stored.ArchiveRecord, wantRecord)
	}
	payload, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	for _, secretLocation := range []string{"private-bucket", "archive_uri", "manifest_uri"} {
		if strings.Contains(string(payload), secretLocation) {
			t.Fatalf("external result API JSON disclosed %q: %s", secretLocation, payload)
		}
	}
}

func TestListExternalResultImportsUsesStableApplicationScopedPagination(t *testing.T) {
	store := newExternalResultPlacementStore(t)
	createdAt := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		id     string
		suffix string
	}{
		{id: "a-1", suffix: "a"},
		{id: "a-3", suffix: "a"},
		{id: "b-9", suffix: "b"},
		{id: "a-2", suffix: "a"},
	} {
		result := externalResultForProject(fixture.id, fixture.suffix)
		result.CreatedAt = createdAt
		if err := store.SaveExternalResultImport(result); err != nil {
			t.Fatalf("SaveExternalResultImport(%s): %v", fixture.id, err)
		}
	}

	first, err := store.ListExternalResultImports(ExternalResultImportFilter{ProjectID: "external-result-project-a", ApplicationID: "external-result-application-a", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != "a-3" || first[1].ID != "a-2" {
		t.Fatalf("first page = %#v, want [a-3 a-2]", first)
	}
	second, err := store.ListExternalResultImports(ExternalResultImportFilter{ProjectID: "external-result-project-a", ApplicationID: "external-result-application-a", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID != "a-1" {
		t.Fatalf("second page = %#v, want [a-1]", second)
	}
}
