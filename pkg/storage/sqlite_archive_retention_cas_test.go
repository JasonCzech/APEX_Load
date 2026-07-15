package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveRetentionCASRejectsSupersededPublication(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	projectID := "archive-retention-cas"
	testID := "retained-run"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	old := &RunArchiveRecord{
		TestID: testID, ArchiveBackend: "filesystem", ArchivePath: "/archives/old",
		ManifestPath: "/archives/old/manifest.json", ManifestSHA256: "old-sha",
		CreatedAt: now.Add(-time.Hour), CompletedAt: now.Add(-time.Hour),
	}
	if err := store.SaveRunArchive(old); err != nil {
		t.Fatal(err)
	}
	newer := *old
	newer.ArchivePath = "/archives/new"
	newer.ManifestPath = "/archives/new/manifest.json"
	newer.ManifestSHA256 = "new-sha"
	newer.CompletedAt = now
	if err := store.SaveRunArchive(&newer); err != nil {
		t.Fatal(err)
	}

	created, err := store.SaveArchiveCleanupJobIfArchiveUnchanged(&ArchiveCleanupJob{
		TestID: testID, ProjectID: projectID, ArchiveRecord: *old,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("stale publication installed a cleanup tombstone")
	}
	if _, err := store.GetArchiveCleanupJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleanup job lookup err = %v, want sql.ErrNoRows", err)
	}
	deleted, err := store.DeleteRunArchiveIfUnchanged(old)
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("stale publication deleted newer catalog record")
	}
	current, err := store.GetRunArchive(testID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ManifestSHA256 != newer.ManifestSHA256 || current.ArchivePath != newer.ArchivePath {
		t.Fatalf("current archive = %+v, want newer publication %+v", current, newer)
	}
}

func TestArchiveRetentionCASCreatesIntentAndDeletesMatchingPublication(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	projectID := "archive-retention-match"
	testID := "matching-run"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	record := &RunArchiveRecord{
		TestID: testID, ArchiveBackend: "filesystem", ArchivePath: "/archives/current",
		ManifestPath: "/archives/current/manifest.json", ManifestSHA256: "current-sha",
		CreatedAt: now, CompletedAt: now,
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	created, err := store.SaveArchiveCleanupJobIfArchiveUnchanged(&ArchiveCleanupJob{
		TestID: testID, ProjectID: projectID, ArchiveRecord: *record,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("matching publication did not install cleanup tombstone")
	}
	deleted, err := store.DeleteRunArchiveIfUnchanged(record)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("matching publication was not deleted")
	}
	if _, err := store.GetRunArchive(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archive lookup err = %v, want sql.ErrNoRows", err)
	}
}
