package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestDataPoolRowPagingStateMigratesPopulatedLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-data-pool-paging.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE shared_data_pools (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			schema_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			archived_at DATETIME
		)`,
		`CREATE TABLE shared_data_pool_rows (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL,
			data_json TEXT NOT NULL,
			status TEXT,
			version INTEGER DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			_ = legacy.Close()
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"legacy-populated", "legacy-empty"} {
		if _, err := legacy.Exec(`INSERT INTO shared_data_pools
			(id, name, description, schema_json, created_at, updated_at)
			VALUES (?, ?, '', '{}', ?, ?)`, id, id, now, now); err != nil {
			_ = legacy.Close()
			t.Fatalf("insert legacy pool %q: %v", id, err)
		}
	}
	for index := 0; index < 3; index++ {
		if _, err := legacy.Exec(`INSERT INTO shared_data_pool_rows
			(id, pool_id, data_json, status, version, created_at, updated_at)
			VALUES (?, 'legacy-populated', ?, 'active', 1, ?, ?)`,
			"legacy-row-"+string(rune('a'+index)),
			`{"value":"legacy"}`,
			now.Add(time.Duration(index)*time.Second),
			now.Add(time.Duration(index)*time.Second),
		); err != nil {
			_ = legacy.Close()
			t.Fatalf("insert legacy row %d: %v", index, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	assertDataPoolPagingState(t, store, "legacy-populated", "1", 3)
	assertDataPoolPagingState(t, store, "legacy-empty", "0", 0)
	var indexCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_shared_data_pool_rows_pool_created_id'`).Scan(&indexCount); err != nil {
		t.Fatalf("inspect paging index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("paging index count = %d, want 1", indexCount)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	// Opening the database again must not reinterpret already-migrated state as
	// legacy state or advance the revision without a row mutation.
	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer reopened.Close()
	assertDataPoolPagingState(t, reopened, "legacy-populated", "1", 3)
	assertDataPoolPagingState(t, reopened, "legacy-empty", "0", 0)
}

func TestDataPoolRowPagingStateTracksEveryMutationPath(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "data-pool-paging-mutations.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "paging-mutation-pool", Name: "Paging mutations"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "0", 0)

	row := &SharedDataPoolRow{
		ID:     "row-upsert",
		PoolID: pool.ID,
		Data:   map[string]string{"value": "created"},
	}
	if err := store.SaveDataPoolRow(row); err != nil {
		t.Fatalf("SaveDataPoolRow create: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "1", 1)

	row.Data["value"] = "upserted"
	if err := store.SaveDataPoolRow(row); err != nil {
		t.Fatalf("SaveDataPoolRow upsert: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "2", 1)

	current, err := store.GetDataPoolRowFromPool(pool.ID, row.ID)
	if err != nil {
		t.Fatalf("GetDataPoolRowFromPool: %v", err)
	}
	current.Data["value"] = "optimistic update"
	staleVersion := current.Version
	if updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, current, current.Version); err != nil || !updated {
		t.Fatalf("UpdateDataPoolRowIfVersion updated=%v err=%v", updated, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "3", 1)
	if updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, current, staleVersion); err != nil || updated {
		t.Fatalf("stale UpdateDataPoolRowIfVersion updated=%v err=%v", updated, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "3", 1)

	if err := store.BulkReplaceDataPoolRows(pool.ID, []*SharedDataPoolRow{
		{ID: "row-bulk-a", Data: map[string]string{"value": "bulk-a"}},
		{ID: "row-bulk-b", Data: map[string]string{"value": "bulk-b"}},
	}, false); err != nil {
		t.Fatalf("BulkReplaceDataPoolRows append: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "4", 3)

	existing, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows before guarded append: %v", err)
	}
	if applied, err := store.BulkAppendDataPoolRowsIfUnchanged(
		pool.ID,
		[]*SharedDataPoolRow{{ID: "row-guarded-append", Data: map[string]string{"value": "guarded"}}},
		existing,
	); err != nil || !applied {
		t.Fatalf("BulkAppendDataPoolRowsIfUnchanged applied=%v err=%v", applied, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "5", 4)

	if applied, err := store.BulkReplaceDataPoolRowsIfCount(
		pool.ID,
		[]*SharedDataPoolRow{{ID: "row-count-replacement", Data: map[string]string{"value": "count"}}},
		true,
		3,
	); err != nil || applied {
		t.Fatalf("stale BulkReplaceDataPoolRowsIfCount applied=%v err=%v", applied, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "5", 4)
	if applied, err := store.BulkReplaceDataPoolRowsIfCount(
		pool.ID,
		[]*SharedDataPoolRow{{ID: "row-count-replacement", Data: map[string]string{"value": "count"}}},
		true,
		4,
	); err != nil || !applied {
		t.Fatalf("BulkReplaceDataPoolRowsIfCount applied=%v err=%v", applied, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "6", 1)

	contentRevision, count, err := store.DataPoolRowsRevision(pool.ID)
	if err != nil {
		t.Fatalf("DataPoolRowsRevision: %v", err)
	}
	if count != 1 {
		t.Fatalf("content revision row count = %d, want 1", count)
	}
	if applied, err := store.BulkReplaceDataPoolRowsIfRevision(
		pool.ID,
		[]*SharedDataPoolRow{{ID: "row-revision-replacement", Data: map[string]string{"value": "revision"}}},
		contentRevision,
	); err != nil || !applied {
		t.Fatalf("BulkReplaceDataPoolRowsIfRevision applied=%v err=%v", applied, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "7", 1)

	if err := store.BulkReplaceDataPoolRows(pool.ID, nil, true); err != nil {
		t.Fatalf("BulkReplaceDataPoolRows deplete: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "8", 0)

	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID: "row-legacy-delete", PoolID: pool.ID, Data: map[string]string{"value": "legacy delete"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow before DeleteDataPoolRow: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "9", 1)
	if err := store.DeleteDataPoolRow("row-legacy-delete"); err != nil {
		t.Fatalf("DeleteDataPoolRow: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "10", 0)

	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID: "row-scoped-delete", PoolID: pool.ID, Data: map[string]string{"value": "scoped delete"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow before DeleteDataPoolRowFromPool: %v", err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "11", 1)
	if deleted, err := store.DeleteDataPoolRowFromPool(pool.ID, "row-scoped-delete"); err != nil || !deleted {
		t.Fatalf("DeleteDataPoolRowFromPool deleted=%v err=%v", deleted, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "12", 0)
	if deleted, err := store.DeleteDataPoolRowFromPool(pool.ID, "missing"); err != nil || deleted {
		t.Fatalf("missing DeleteDataPoolRowFromPool deleted=%v err=%v", deleted, err)
	}
	assertDataPoolPagingState(t, store, pool.ID, "12", 0)
}

func assertDataPoolPagingState(t *testing.T, store *SQLiteStore, poolID, wantRevision string, wantCount int) {
	t.Helper()
	revision, count, err := store.DataPoolRowsPageState(poolID)
	if err != nil {
		t.Fatalf("DataPoolRowsPageState(%q): %v", poolID, err)
	}
	if revision != wantRevision || count != wantCount {
		t.Fatalf(
			"DataPoolRowsPageState(%q) = revision %q count %d, want revision %q count %d",
			poolID,
			revision,
			count,
			wantRevision,
			wantCount,
		)
	}
}
