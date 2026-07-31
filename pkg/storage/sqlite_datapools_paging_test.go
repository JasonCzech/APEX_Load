package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Legacy rows were written through time.String() and carry Go monotonic-clock
// suffixes in their stored text. The keyset pager must page such pools without
// duplicating boundary rows or skipping any, in exactly the order the plain
// listing uses.
func TestListDataPoolRowsPageAfterHandlesLegacyMonotonicTimestamps(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-legacy-paging", Name: "Legacy paging"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}

	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("legacy-%03d", i)
		created := fmt.Sprintf("2026-05-11 17:18:41.%06d -0400 EDT m=+549.%09d", i, 100000000+i)
		if _, err := store.db.Exec(`INSERT INTO shared_data_pool_rows (id, pool_id, data_json, status, version, created_at, updated_at)
			VALUES (?, ?, ?, 'active', 1, ?, ?)`, id, pool.ID, `{"n":"`+id+`"}`, created, created); err != nil {
			t.Fatalf("raw insert %s: %v", id, err)
		}
	}
	appended := make([]*SharedDataPoolRow, 0, 10)
	for i := 0; i < 10; i++ {
		appended = append(appended, &SharedDataPoolRow{ID: fmt.Sprintf("new-%03d", i), Data: map[string]string{"n": fmt.Sprintf("new-%03d", i)}})
	}
	if err := store.BulkReplaceDataPoolRows(pool.ID, appended, false); err != nil {
		t.Fatalf("BulkReplaceDataPoolRows: %v", err)
	}

	full, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows: %v", err)
	}
	if len(full) != 35 {
		t.Fatalf("full listing = %d rows, want 35", len(full))
	}

	var paged []*SharedDataPoolRow
	afterKey, afterID := "", ""
	for {
		page, nextKey, err := store.ListDataPoolRowsPageAfter(pool.ID, afterKey, afterID, 7)
		if err != nil {
			t.Fatalf("ListDataPoolRowsPageAfter: %v", err)
		}
		paged = append(paged, page...)
		if len(page) < 7 {
			break
		}
		afterKey = nextKey
		afterID = page[len(page)-1].ID
		if len(paged) > len(full) {
			t.Fatalf("pager emitted %d rows for a %d-row pool — boundary rows are being duplicated", len(paged), len(full))
		}
	}
	if len(paged) != len(full) {
		t.Fatalf("paged %d rows, full listing has %d — duplicates or skips at page boundaries", len(paged), len(full))
	}
	for i := range full {
		if paged[i].ID != full[i].ID {
			t.Fatalf("row %d: paged %q, listing %q — pager order diverges from the stored sort order", i, paged[i].ID, full[i].ID)
		}
	}
}

func TestListDataPoolRowsPageAfterDoesNotSkipAfterBoundaryDeletes(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-delete-safe-paging", Name: "Delete-safe paging"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("row-%d", i)
		if err := store.SaveDataPoolRow(&SharedDataPoolRow{
			ID: id, PoolID: pool.ID, Data: map[string]string{"id": id}, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("SaveDataPoolRow(%s): %v", id, err)
		}
	}

	first, afterKey, err := store.ListDataPoolRowsPageAfter(pool.ID, "", "", 2)
	if err != nil {
		t.Fatalf("first ListDataPoolRowsPageAfter: %v", err)
	}
	if len(first) != 2 || first[0].ID != "row-1" || first[1].ID != "row-2" {
		t.Fatalf("first page = %#v, want row-1,row-2", first)
	}
	afterID := first[len(first)-1].ID
	// Delete both rows before/at the saved boundary. OFFSET 2 would now begin
	// at row-5 and skip row-3/row-4; the keyset values remain a valid boundary
	// even though the boundary row itself no longer exists.
	for _, id := range []string{"row-1", "row-2"} {
		deleted, err := store.DeleteDataPoolRowFromPool(pool.ID, id)
		if err != nil || !deleted {
			t.Fatalf("DeleteDataPoolRowFromPool(%s) deleted=%v err=%v", id, deleted, err)
		}
	}

	var remaining []string
	for {
		page, nextKey, err := store.ListDataPoolRowsPageAfter(pool.ID, afterKey, afterID, 2)
		if err != nil {
			t.Fatalf("next ListDataPoolRowsPageAfter: %v", err)
		}
		for _, row := range page {
			remaining = append(remaining, row.ID)
		}
		if len(page) < 2 {
			break
		}
		afterKey = nextKey
		afterID = page[len(page)-1].ID
	}
	if got, want := strings.Join(remaining, ","), "row-3,row-4,row-5,row-6"; got != want {
		t.Fatalf("rows after deleted boundary = %q, want %q", got, want)
	}
}

func TestDataPoolRowsPageStateTracksMutationsWithoutContentScans(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-page-state", Name: "Page state"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	assertState := func(wantRevision string, wantCount int) {
		t.Helper()
		revision, count, err := store.DataPoolRowsPageState(pool.ID)
		if err != nil {
			t.Fatalf("DataPoolRowsPageState: %v", err)
		}
		if revision != wantRevision || count != wantCount {
			t.Fatalf("page state = revision %q count %d, want revision %q count %d", revision, count, wantRevision, wantCount)
		}
		fastCount, err := store.CountDataPoolRows(pool.ID)
		if err != nil {
			t.Fatalf("CountDataPoolRows: %v", err)
		}
		if fastCount != wantCount {
			t.Fatalf("durable CountDataPoolRows = %d, want %d", fastCount, wantCount)
		}
	}
	assertState("0", 0)

	row := &SharedDataPoolRow{ID: "row-1", PoolID: pool.ID, Data: map[string]string{"id": "one"}}
	if err := store.SaveDataPoolRow(row); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	assertState("1", 1)

	current, err := store.GetDataPoolRowFromPool(pool.ID, row.ID)
	if err != nil {
		t.Fatalf("GetDataPoolRowFromPool: %v", err)
	}
	current.Data["id"] = "updated"
	if updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, current, current.Version); err != nil || !updated {
		t.Fatalf("UpdateDataPoolRowIfVersion updated=%v err=%v", updated, err)
	}
	assertState("2", 1)

	if err := store.BulkReplaceDataPoolRows(pool.ID, []*SharedDataPoolRow{
		{ID: "row-2", Data: map[string]string{"id": "two"}},
		{ID: "row-3", Data: map[string]string{"id": "three"}},
	}, false); err != nil {
		t.Fatalf("BulkReplaceDataPoolRows append: %v", err)
	}
	assertState("3", 3)

	if deleted, err := store.DeleteDataPoolRowFromPool(pool.ID, "row-2"); err != nil || !deleted {
		t.Fatalf("DeleteDataPoolRowFromPool deleted=%v err=%v", deleted, err)
	}
	assertState("4", 2)

	if count, err := store.CountDataPoolRows("missing-pool"); err != nil || count != 0 {
		t.Fatalf("missing pool count = %d err=%v, want historical zero/nil behavior", count, err)
	}
}

// SaveDataPoolRow must land single-row creates after a bulk import that
// future-dated created_at, so the visible order matches insertion order.
func TestSaveDataPoolRowSortsAfterFutureDatedBulkImport(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-future-bulk", Name: "Future bulk"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	bulk := make([]*SharedDataPoolRow, 0, 3)
	for i := 0; i < 3; i++ {
		bulk = append(bulk, &SharedDataPoolRow{ID: fmt.Sprintf("bulk-%d", i), Data: map[string]string{"n": fmt.Sprintf("bulk-%d", i)}})
	}
	if err := store.BulkReplaceDataPoolRows(pool.ID, bulk, false); err != nil {
		t.Fatalf("BulkReplaceDataPoolRows: %v", err)
	}
	// Push the last bulk row a full second into the future, mimicking the
	// created_at staggering of a very large import.
	future := time.Now().Add(time.Second).Truncate(time.Microsecond)
	if _, err := store.db.Exec(`UPDATE shared_data_pool_rows SET created_at = ? WHERE id = 'bulk-2'`, future); err != nil {
		t.Fatalf("future-date bulk row: %v", err)
	}

	single := &SharedDataPoolRow{ID: "single-after", PoolID: pool.ID, Data: map[string]string{"n": "single-after"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveDataPoolRow(single); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows: %v", err)
	}
	if rows[len(rows)-1].ID != "single-after" {
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		t.Fatalf("single-row create landed mid-pool: order = %v", ids)
	}
}

func TestBulkReplaceDataPoolRowsIfCountRejectsStaleSnapshot(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-stale-bulk", Name: "Stale bulk"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID: "existing", PoolID: pool.ID, Data: map[string]string{"id": "existing"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}

	applied, err := store.BulkReplaceDataPoolRowsIfCount(pool.ID, []*SharedDataPoolRow{{
		ID: "replacement", Data: map[string]string{"id": "replacement"},
	}}, true, 0)
	if err != nil {
		t.Fatalf("BulkReplaceDataPoolRowsIfCount: %v", err)
	}
	if applied {
		t.Fatal("stale replace applied")
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "existing" {
		t.Fatalf("rows after stale replace = %#v, want original row", rows)
	}

	archivedAt := time.Now().UTC()
	pool.ArchivedAt = &archivedAt
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool(archive): %v", err)
	}
	if _, err := store.BulkReplaceDataPoolRowsIfCount(pool.ID, []*SharedDataPoolRow{{
		ID: "archived-append", Data: map[string]string{"id": "archived"},
	}}, false, 1); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("archived bulk import err = %v, want archived rejection", err)
	}
}

func TestBulkAppendDataPoolRowsIfUnchangedRejectsSameCountMutation(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-revision-guard", Name: "Revision guard"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	existing := &SharedDataPoolRow{ID: "existing", PoolID: pool.ID, Data: map[string]string{"id": "before"}}
	if err := store.SaveDataPoolRow(existing); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	snapshot, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows(snapshot): %v", err)
	}
	current := *snapshot[0]
	current.Data = map[string]string{"id": "after", "new_column": strings.Repeat("x", 1024)}
	updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, &current, current.Version)
	if err != nil || !updated {
		t.Fatalf("UpdateDataPoolRowIfVersion updated=%v err=%v", updated, err)
	}

	applied, err := store.BulkAppendDataPoolRowsIfUnchanged(pool.ID, []*SharedDataPoolRow{{
		ID: "append", Data: map[string]string{"id": "appended"},
	}}, snapshot)
	if err != nil {
		t.Fatalf("BulkAppendDataPoolRowsIfUnchanged: %v", err)
	}
	if applied {
		t.Fatal("append applied against a same-count mutated snapshot")
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows(after): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "existing" || rows[0].Data["id"] != "after" {
		t.Fatalf("rows after rejected append = %#v, want only concurrently updated row", rows)
	}
}

func TestBulkReplaceDataPoolRowsIfRevisionRejectsSameCountMutation(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-replace-revision-guard", Name: "Replace revision guard"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	existing := &SharedDataPoolRow{ID: "existing", PoolID: pool.ID, Data: map[string]string{"id": "before"}}
	if err := store.SaveDataPoolRow(existing); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	revision, count, err := store.DataPoolRowsRevision(pool.ID)
	if err != nil {
		t.Fatalf("DataPoolRowsRevision: %v", err)
	}
	if count != 1 || revision == "" {
		t.Fatalf("revision snapshot = count:%d revision:%q", count, revision)
	}

	current, err := store.GetDataPoolRowFromPool(pool.ID, existing.ID)
	if err != nil {
		t.Fatalf("GetDataPoolRowFromPool: %v", err)
	}
	current.Data = map[string]string{"id": "concurrent"}
	updated, err := store.UpdateDataPoolRowIfVersion(pool.ID, current, current.Version)
	if err != nil || !updated {
		t.Fatalf("UpdateDataPoolRowIfVersion updated=%v err=%v", updated, err)
	}

	applied, err := store.BulkReplaceDataPoolRowsIfRevision(pool.ID, []*SharedDataPoolRow{{
		ID: "replacement", Data: map[string]string{"id": "replacement"},
	}}, revision)
	if err != nil {
		t.Fatalf("BulkReplaceDataPoolRowsIfRevision: %v", err)
	}
	if applied {
		t.Fatal("replace applied against a same-count mutated snapshot")
	}
	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows(after): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != existing.ID || rows[0].Data["id"] != "concurrent" {
		t.Fatalf("rows after rejected replace = %#v, want concurrent row preserved", rows)
	}
}

func TestDataPoolUpdatedAtAlwaysAdvances(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &SharedDataPool{ID: "pool-monotonic-token", Name: "Monotonic token"}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}

	future := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	if _, err := store.db.Exec(`UPDATE shared_data_pools SET updated_at = ? WHERE id = ?`, future, pool.ID); err != nil {
		t.Fatalf("future-date pool token: %v", err)
	}
	pool.Name = "Saved again"
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool(second): %v", err)
	}
	if !pool.UpdatedAt.After(future) {
		t.Fatalf("SaveDataPool token = %v, want after %v", pool.UpdatedAt, future)
	}

	current, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}
	nextFuture := current.UpdatedAt.Add(time.Hour)
	if _, err := store.db.Exec(`UPDATE shared_data_pools SET updated_at = ? WHERE id = ?`, nextFuture, pool.ID); err != nil {
		t.Fatalf("future-date CAS token: %v", err)
	}
	current.UpdatedAt = nextFuture
	current.Name = "CAS update"
	updated, err := store.UpdateDataPoolIfUnchanged(current, nextFuture)
	if err != nil {
		t.Fatalf("UpdateDataPoolIfUnchanged: %v", err)
	}
	if !updated || !current.UpdatedAt.After(nextFuture) {
		t.Fatalf("CAS updated=%v token=%v, want token after %v", updated, current.UpdatedAt, nextFuture)
	}
}
