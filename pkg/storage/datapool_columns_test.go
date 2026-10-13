package storage

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDataPoolColumnOrderPersists(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	exerciseDataPoolColumnOrder(t, store)
}

// exerciseDataPoolColumnOrder covers saving, updating, listing and live-table
// seeding of a pool's column order; the Postgres test runs it as written.
func exerciseDataPoolColumnOrder(t *testing.T, store *SQLiteStore) {
	t.Helper()
	pool := &SharedDataPool{ID: "pool-columns", Name: "Customers", ProjectID: DefaultProjectID, Columns: []string{" zip ", "customer_id", "", "zip", "Email"}}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatal(err)
	}
	want := []string{"zip", "customer_id", "Email"}
	if !reflect.DeepEqual(pool.Columns, want) {
		t.Fatalf("saved columns = %#v, want %#v", pool.Columns, want)
	}
	loaded, err := store.GetDataPool(pool.ID)
	if err != nil || !reflect.DeepEqual(loaded.Columns, want) {
		t.Fatalf("loaded columns = %#v (%v), want %#v", loaded, err, want)
	}
	listed, err := store.ListDataPoolsForProject(false, DefaultProjectID)
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0].Columns, want) {
		t.Fatalf("listed pools = %#v (%v)", listed, err)
	}

	// Rename without touching the order keeps it; an explicit order replaces it.
	loaded.Name = "Customer accounts"
	if ok, err := store.UpdateDataPoolIfUnchanged(loaded, loaded.UpdatedAt); err != nil || !ok {
		t.Fatalf("rename = %v %v", ok, err)
	}
	if again, _ := store.GetDataPool(pool.ID); !reflect.DeepEqual(again.Columns, want) {
		t.Fatalf("rename changed columns to %#v", again.Columns)
	}
	// A metadata-only update keeps the stored order even if its copy is stale.
	stale, _ := store.GetDataPool(pool.ID)
	stale.Columns = []string{"not", "applied"}
	if ok, err := store.UpdateDataPoolIfUnchanged(stale, stale.UpdatedAt); err != nil || !ok || !reflect.DeepEqual(stale.Columns, want) {
		t.Fatalf("metadata update = %v %v, columns %#v", ok, err, stale.Columns)
	}
	again, _ := store.GetDataPool(pool.ID)
	again.Columns = []string{"Email", "zip"}
	if ok, err := store.UpdateDataPoolAndColumnsIfUnchanged(again, again.UpdatedAt); err != nil || !ok {
		t.Fatalf("reorder = %v %v", ok, err)
	}
	if reordered, _ := store.GetDataPool(pool.ID); !reflect.DeepEqual(reordered.Columns, []string{"Email", "zip"}) {
		t.Fatalf("reordered columns = %#v", reordered.Columns)
	}

	// A live table seeded from the pool sees the same order.
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{ID: "row-1", PoolID: pool.ID, Data: map[string]string{"zip": "10001", "Email": "a@example.test"}}); err != nil {
		t.Fatal(err)
	}
	seed, err := store.ReadLiveDataPoolSeed(context.Background(), pool.ID, DefaultProjectID, 100, 1<<20)
	if err != nil || !reflect.DeepEqual(seed.Columns, []string{"Email", "zip"}) {
		t.Fatalf("seed columns = %#v (%v)", seed, err)
	}

	// Imports replace the order only over the order they read, and leave the
	// edit token alone.
	current, _ := store.GetDataPool(pool.ID)
	if ok, err := store.UpdateDataPoolColumnsIfUnchanged(pool.ID, []string{"zip"}, []string{"zip", "Email"}); err != nil || ok {
		t.Fatalf("stale column update = %v %v, want refused", ok, err)
	}
	if ok, err := store.UpdateDataPoolColumnsIfUnchanged(pool.ID, current.Columns, []string{"zip", "Email", "region"}); err != nil || !ok {
		t.Fatalf("column update = %v %v", ok, err)
	}
	if after, _ := store.GetDataPool(pool.ID); !reflect.DeepEqual(after.Columns, []string{"zip", "Email", "region"}) || !after.UpdatedAt.Equal(current.UpdatedAt) {
		t.Fatalf("after column update = %#v (token %v, was %v)", after.Columns, after.UpdatedAt, current.UpdatedAt)
	}
	if ok, err := store.UpdateDataPoolColumnsIfUnchanged("no-such-pool", nil, []string{"a"}); err != nil || ok {
		t.Fatalf("missing pool column update = %v %v", ok, err)
	}

	// Clearing the order stores nothing.
	cleared, _ := store.GetDataPool(pool.ID)
	cleared.Columns = nil
	if ok, err := store.UpdateDataPoolAndColumnsIfUnchanged(cleared, cleared.UpdatedAt); err != nil || !ok {
		t.Fatalf("clear = %v %v", ok, err)
	}
	if final, _ := store.GetDataPool(pool.ID); final.Columns != nil {
		t.Fatalf("cleared columns = %#v", final.Columns)
	}

	// The order is capped like the engine's columns.
	tooMany := make([]string, MaxDataPoolColumns+1)
	for i := range tooMany {
		tooMany[i] = "c" + strconv.Itoa(i)
	}
	over := &SharedDataPool{ID: "pool-too-wide", Name: "Wide", ProjectID: DefaultProjectID, Columns: tooMany, CreatedAt: time.Now()}
	if err := store.SaveDataPool(over); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("over-wide pool err = %v", err)
	}
}

func TestLiveTableSaveAsNewKeepsTableColumnOrder(t *testing.T) {
	s := newHAFencingStore(t)
	seed := seedLiveDataPool(t, s)
	r, term := closedLiveTableForSeed(t, s, seed, "ordered-output")
	in := LiveTableFinalizationRequest{TableID: r.ID, ProjectID: r.ProjectID, Revision: 7, Mode: "save_as_new", OutputPoolName: "Ordered jobs", Seed: seed.Identity, Rows: liveFinalRowsFromSeed(seed), Schema: seed.Identity.Schema, Columns: []string{"id", "empty"}}
	result, err := s.FinalizeLiveTable(context.Background(), in, term)
	if err != nil || result.State != "complete" {
		t.Fatalf("finalization = %+v %v", result, err)
	}
	output, err := s.GetDataPool(result.OutputPoolID)
	if err != nil || !reflect.DeepEqual(output.Columns, []string{"id", "empty"}) {
		t.Fatalf("output pool = %+v %v", output, err)
	}
}
