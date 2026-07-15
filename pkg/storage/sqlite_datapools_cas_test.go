package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDataPoolOptimisticUpdatesRejectStaleSnapshots(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	pool := &SharedDataPool{ID: "pool-cas", ProjectID: DefaultProjectID, Name: "original"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	first, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool first: %v", err)
	}
	stale, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool stale: %v", err)
	}
	first.Name = "first writer"
	if updated, err := store.UpdateDataPoolIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Name = "stale writer"
	if updated, err := store.UpdateDataPoolIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	fresh, err := store.GetDataPool(pool.ID)
	if err != nil || fresh.Name != "first writer" {
		t.Fatalf("fresh pool = %#v, err=%v", fresh, err)
	}

	if err := store.SaveDataPoolRow(&SharedDataPoolRow{ID: "row-cas", PoolID: pool.ID, Data: map[string]string{"value": "original"}}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	rowFirst, err := store.GetDataPoolRowFromPool(pool.ID, "row-cas")
	if err != nil {
		t.Fatalf("GetDataPoolRowFromPool first: %v", err)
	}
	rowStale, err := store.GetDataPoolRowFromPool(pool.ID, "row-cas")
	if err != nil {
		t.Fatalf("GetDataPoolRowFromPool stale: %v", err)
	}
	rowFirst.Data["value"] = "first writer"
	if updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, rowFirst, rowFirst.Version); err != nil || !updated {
		t.Fatalf("first row update = (%v, %v), want success", updated, err)
	}
	if rowFirst.Version != 2 {
		t.Fatalf("updated row version = %d, want 2", rowFirst.Version)
	}
	rowStale.Data["value"] = "stale writer"
	if updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, rowStale, rowStale.Version); err != nil || updated {
		t.Fatalf("stale row update = (%v, %v), want conflict", updated, err)
	}
	rowFresh, err := store.GetDataPoolRowFromPool(pool.ID, "row-cas")
	if err != nil || rowFresh.Data["value"] != "first writer" || rowFresh.Version != 2 {
		t.Fatalf("fresh row = %#v, err=%v", rowFresh, err)
	}
}

func TestDataPoolSaveCannotRelocateExistingPool(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := poolTestTime()
	if err := store.SaveProject(&Project{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject A: %v", err)
	}
	if err := store.SaveProject(&Project{ID: "project-b", Name: "B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject B: %v", err)
	}
	pool := &SharedDataPool{ID: "pool-owner", ProjectID: "project-a", Name: "owned"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool initial: %v", err)
	}
	if err := store.SaveDataPool(&SharedDataPool{ID: pool.ID, ProjectID: "project-b", Name: "updated"}); err != nil {
		t.Fatalf("SaveDataPool update: %v", err)
	}
	loaded, err := store.GetDataPool(pool.ID)
	if err != nil || loaded.ProjectID != "project-a" {
		t.Fatalf("pool ownership after upsert = %#v, err=%v", loaded, err)
	}
}

func poolTestTime() (now time.Time) {
	return time.Now().UTC()
}
