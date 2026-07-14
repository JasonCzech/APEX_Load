package api

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestBoundedArchiveCacheEvictsLeastRecentlyUsed(t *testing.T) {
	cache := newBoundedArchiveCache[int](2)
	loads := map[string]int{}
	load := func(key string, value int) func() (*int, error) {
		return func() (*int, error) {
			loads[key]++
			copy := value
			return &copy, nil
		}
	}
	if _, err := cache.getOrLoad("a", load("a", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.getOrLoad("b", load("b", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.getOrLoad("a", load("a", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.getOrLoad("c", load("c", 3)); err != nil {
		t.Fatal(err)
	}
	if cache.len() != 2 {
		t.Fatalf("cache len = %d, want 2", cache.len())
	}
	if _, err := cache.getOrLoad("b", load("b", 2)); err != nil {
		t.Fatal(err)
	}
	if loads["a"] != 1 || loads["b"] != 2 || loads["c"] != 1 {
		t.Fatalf("loads = %#v, want b evicted once", loads)
	}
}

func TestBoundedArchiveCacheSingleflightsConcurrentLoad(t *testing.T) {
	cache := newBoundedArchiveCache[int](2)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	loader := func() (*int, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		value := 42
		return &value, nil
	}
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cache.getOrLoad("same", loader)
			if err != nil {
				t.Errorf("getOrLoad: %v", err)
				return
			}
			results <- *value
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", calls.Load())
	}
	for value := range results {
		if value != 42 {
			t.Fatalf("value = %d, want 42", value)
		}
	}
}
