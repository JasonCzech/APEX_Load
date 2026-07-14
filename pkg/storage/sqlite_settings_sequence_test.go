package storage

import (
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

func TestNextSettingSequenceIsAtomic(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sequence.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const workers = 20
	values := make([]int, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			value, err := store.NextSettingSequence("refseq:test")
			values[index] = value
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Ints(values)
	for index, value := range values {
		if value != index+1 {
			t.Fatalf("sequence values = %v", values)
		}
	}
}

func TestCompareAndSwapSettingRejectsStaleValue(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if updated, err := store.CompareAndSwapSetting("key", "", "one"); err != nil || !updated {
		t.Fatalf("create = %v, %v", updated, err)
	}
	if updated, err := store.CompareAndSwapSetting("key", "", "stale"); err != nil || updated {
		t.Fatalf("stale create = %v, %v", updated, err)
	}
	if updated, err := store.CompareAndSwapSetting("key", "one", "two"); err != nil || !updated {
		t.Fatalf("update = %v, %v", updated, err)
	}
}
