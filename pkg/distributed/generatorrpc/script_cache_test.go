package generatorrpc

import (
	"fmt"
	"sync"
	"testing"
)

func TestScriptCacheManagerLRUEvictionHonorsPins(t *testing.T) {
	cache := newScriptCacheManager(2, 1024)
	if err := cache.put("first", []byte(`{"protocol":"http"}`), "first-hash", nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.put("second", []byte(`{"protocol":"http"}`), "second-hash", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.touch("first"); !ok {
		t.Fatal("touch did not find first entry")
	}
	if err := cache.put("third", []byte(`{"protocol":"http"}`), "third-hash", map[string]struct{}{"first": {}}); err != nil {
		t.Fatal(err)
	}

	entries, retainedBytes := cache.snapshot()
	if _, ok := entries["first"]; !ok {
		t.Fatal("pinned/recent entry was evicted")
	}
	if _, ok := entries["second"]; ok {
		t.Fatal("oldest unpinned entry was retained")
	}
	if _, ok := entries["third"]; !ok {
		t.Fatal("new entry was not retained")
	}
	if retainedBytes != int64(len(entries["first"].content)+len(entries["third"].content)) {
		t.Fatalf("retained bytes = %d, want content sum", retainedBytes)
	}
}

func TestScriptCacheManagerRejectsReplacementWithoutMutatingPinnedState(t *testing.T) {
	cache := newScriptCacheManager(2, 6)
	if err := cache.put("shared", []byte("old"), "old-hash", nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.put("blocker", []byte("xx"), "blocker-hash", nil); err != nil {
		t.Fatal(err)
	}
	err := cache.put("shared", []byte("newnew"), "new-hash", map[string]struct{}{
		"shared":  {},
		"blocker": {},
	})
	if err == nil {
		t.Fatal("pinned replacement unexpectedly succeeded")
	}
	entries, retainedBytes := cache.snapshot()
	if string(entries["shared"].content) != "old" || entries["shared"].hash != "old-hash" {
		t.Fatalf("failed replacement mutated entry: %+v", entries["shared"])
	}
	if retainedBytes != 5 {
		t.Fatalf("retained bytes = %d, want 5", retainedBytes)
	}
}

func TestScriptCacheManagerOwnsConcurrentAccess(t *testing.T) {
	cache := newScriptCacheManager(16, 1<<20)
	for index := 0; index < 8; index++ {
		id := fmt.Sprintf("script-%d", index)
		if err := cache.put(id, []byte(`{"protocol":"http"}`), id, nil); err != nil {
			t.Fatal(err)
		}
	}

	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := 0; iteration < 200; iteration++ {
				id := fmt.Sprintf("script-%d", (worker+iteration)%8)
				if _, ok := cache.touch(id); !ok {
					t.Errorf("touch missed %s", id)
					return
				}
				if _, ok := cache.get(id); !ok {
					t.Errorf("get missed %s", id)
					return
				}
				if iteration%20 == 0 {
					entries, _ := cache.snapshot()
					if len(entries) != 8 {
						t.Errorf("snapshot contains %d entries, want 8", len(entries))
						return
					}
				}
			}
		}()
	}
	workers.Wait()
}
