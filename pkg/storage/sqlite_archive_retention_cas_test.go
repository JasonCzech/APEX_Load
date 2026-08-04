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
	// Trend points are a derived projection, not part of the physical archive
	// publication identity. Simulate a lazy backfill racing ahead of retention;
	// both the cleanup-intent CAS and final catalog delete must still match.
	expected := *record
	updated, err := store.UpdateRunArchiveTrendPointIfUnchanged(record, `{"run_id":"matching-run"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("matching trend projection was not updated")
	}
	created, err := store.SaveArchiveCleanupJobIfArchiveUnchanged(&ArchiveCleanupJob{
		TestID: testID, ProjectID: projectID, ArchiveRecord: expected,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("matching publication did not install cleanup tombstone")
	}
	deleted, err := store.DeleteRunArchiveIfUnchanged(&expected)
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

func TestUpdateRunArchiveTrendPointIfUnchangedIsTargetedAndVersionFenced(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	projectID := "archive-trend-point-cas"
	testID := "trend-run"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	record := &RunArchiveRecord{
		TestID: testID, RefKey: "RUN-TREND", ArchiveBackend: "filesystem",
		ArchivePath: "/archives/trend-v1", ManifestPath: "/archives/trend-v1/manifest.json",
		ManifestSHA256: "trend-v1", TransactionCount: 17, ResourceSampleCount: 3,
		IntervalSummaryCount: 2, TrendPointJSON: `{"run_id":"wrong"}`,
		CreatedAt: now.Add(-time.Minute), CompletedAt: now,
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	expected := *record
	const repaired = `{"run_id":"trend-run","transactions":17}`
	updated, err := store.UpdateRunArchiveTrendPointIfUnchanged(&expected, repaired)
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("matching publication was not updated")
	}
	current, err := store.GetRunArchive(testID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TrendPointJSON != repaired || current.ArchivePath != record.ArchivePath || current.TransactionCount != record.TransactionCount {
		t.Fatalf("targeted update changed unexpected fields: %+v", current)
	}
	updated, err = store.UpdateRunArchiveTrendPointIfUnchanged(&expected, `{"run_id":"stale-retry"}`)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale trend projection CAS unexpectedly succeeded twice")
	}

	newer := *current
	newer.ArchivePath = "/archives/trend-v2"
	newer.ManifestPath = "/archives/trend-v2/manifest.json"
	newer.ManifestSHA256 = "trend-v2"
	newer.TrendPointJSON = `{"run_id":"trend-run","transactions":23}`
	newer.TransactionCount = 23
	newer.CompletedAt = now.Add(time.Minute)
	if err := store.SaveRunArchive(&newer); err != nil {
		t.Fatal(err)
	}
	updated, err = store.UpdateRunArchiveTrendPointIfUnchanged(current, `{"run_id":"stale-publication"}`)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("superseded publication accepted a trend projection")
	}
	current, err = store.GetRunArchive(testID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ManifestSHA256 != newer.ManifestSHA256 || current.TrendPointJSON != newer.TrendPointJSON || current.TransactionCount != newer.TransactionCount {
		t.Fatalf("stale projection overwrote newer publication: %+v", current)
	}
}
