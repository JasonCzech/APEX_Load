package storage

import (
	"path/filepath"
	"sort"
	"strings"
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

func TestEnsureSettingSequenceAtLeastNeverLowersDurableCounter(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sequence-floor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveSetting("refseq:test-floor", "100"); err != nil {
		t.Fatal(err)
	}
	if value, err := store.EnsureSettingSequenceAtLeast("refseq:test-floor", 80); err != nil || value != 100 {
		t.Fatalf("lower floor = (%d, %v), want (100, nil)", value, err)
	}
	if value, err := store.EnsureSettingSequenceAtLeast("refseq:test-floor", 120); err != nil || value != 120 {
		t.Fatalf("raised floor = (%d, %v), want (120, nil)", value, err)
	}
	if value, err := store.NextSettingSequence("refseq:test-floor"); err != nil || value != 121 {
		t.Fatalf("next after floor = (%d, %v), want (121, nil)", value, err)
	}
}

func TestEnsureSettingSequenceAtLeastSQLRewritesForPostgres(t *testing.T) {
	rewritten := rewriteSQL(ensureSettingSequenceAtLeastQuery, storeDialectPostgres)
	for _, placeholder := range []string{"$1", "$2", "$3"} {
		if !strings.Contains(rewritten, placeholder) {
			t.Fatalf("Postgres sequence-floor query missing %s: %s", placeholder, rewritten)
		}
	}
	if strings.Contains(rewritten, "?") || !strings.Contains(rewritten, "RETURNING value") {
		t.Fatalf("Postgres sequence-floor query rewrite is unsafe: %s", rewritten)
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
