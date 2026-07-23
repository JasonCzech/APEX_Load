package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPowerBIPublishRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if latest, err := store.GetLatestPowerBIPublishForTest("test-1"); err != nil || latest != nil {
		t.Fatalf("expected empty history, got %v err=%v", latest, err)
	}

	first := &PowerBIPublish{
		ID:                   "pub-1",
		TestID:               "test-1",
		ProjectID:            "proj-1",
		WorkspaceID:          "11111111-2222-3333-4444-555555555555",
		DatasetID:            "ds-A",
		DatasetName:          "APEX Load Performance Runs",
		SchemaVersion:        1,
		Status:               PowerBIPublishStatusPending,
		TransactionRowsTotal: 12,
		RequestedBy:          "user@example.com",
	}
	if err := store.SavePowerBIPublish(first); err != nil {
		t.Fatalf("save publish: %v", err)
	}

	publishedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.UpdatePowerBIPublishStatus("pub-1", PowerBIPublishStatusComplete, 1, 12, "", &publishedAt); err != nil {
		t.Fatalf("update status: %v", err)
	}

	latest, err := store.GetLatestPowerBIPublishForTest("test-1")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest == nil || latest.ID != "pub-1" {
		t.Fatalf("unexpected latest publish: %+v", latest)
	}
	if latest.Status != PowerBIPublishStatusComplete || latest.RunRowsPushed != 1 || latest.TransactionRowsPushed != 12 {
		t.Fatalf("status update not persisted: %+v", latest)
	}
	if latest.PublishedAt == nil || !latest.PublishedAt.Equal(publishedAt) {
		t.Fatalf("published_at not persisted: %+v", latest.PublishedAt)
	}
	if latest.WorkspaceID != first.WorkspaceID || latest.DatasetName != first.DatasetName || latest.RequestedBy != first.RequestedBy {
		t.Fatalf("fields lost in round trip: %+v", latest)
	}

	// A later failed publish becomes the latest; the ordering is created_at desc.
	second := &PowerBIPublish{
		ID:            "pub-2",
		TestID:        "test-1",
		ProjectID:     "proj-1",
		WorkspaceID:   first.WorkspaceID,
		DatasetID:     "ds-B",
		DatasetName:   first.DatasetName,
		SchemaVersion: 1,
		Status:        PowerBIPublishStatusFailed,
		Error:         "boom",
		CreatedAt:     time.Now().UTC().Add(time.Minute),
	}
	if err := store.SavePowerBIPublish(second); err != nil {
		t.Fatalf("save second publish: %v", err)
	}
	latest, err = store.GetLatestPowerBIPublishForTest("test-1")
	if err != nil || latest == nil || latest.ID != "pub-2" {
		t.Fatalf("expected pub-2 latest, got %+v err=%v", latest, err)
	}

	list, err := store.ListPowerBIPublishesForTest("test-1", 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 publishes, got %d err=%v", len(list), err)
	}
	if list[0].ID != "pub-2" || list[1].ID != "pub-1" {
		t.Fatalf("unexpected order: %s, %s", list[0].ID, list[1].ID)
	}

	if err := store.UpdatePowerBIPublishStatus("missing", PowerBIPublishStatusFailed, 0, 0, "x", nil); err == nil {
		t.Fatalf("expected error updating missing publish")
	}

	// Dataset-scoped latest is independent of test-scoped latest: pub-2 (newer,
	// dataset ds-B) is the test's latest, but ds-A's latest is still pub-1.
	forDataset, err := store.GetLatestPowerBIPublishForDataset("ds-A")
	if err != nil || forDataset == nil || forDataset.ID != "pub-1" {
		t.Fatalf("expected pub-1 as latest for ds-A, got %+v err=%v", forDataset, err)
	}
	forDataset, err = store.GetLatestPowerBIPublishForDataset("ds-B")
	if err != nil || forDataset == nil || forDataset.ID != "pub-2" {
		t.Fatalf("expected pub-2 as latest for ds-B, got %+v err=%v", forDataset, err)
	}
	forDataset, err = store.GetLatestPowerBIPublishForDataset("ds-unknown")
	if err != nil || forDataset != nil {
		t.Fatalf("expected no publish for unknown dataset, got %+v err=%v", forDataset, err)
	}
}
