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

func TestBoundedArchiveCacheEvictsByWeightAndSkipsOversizedValues(t *testing.T) {
	cache := newWeightedBoundedArchiveCache[int](10, 5, func(value *int) int64 {
		return int64(*value)
	})
	load := func(value int) func() (*int, error) {
		return func() (*int, error) {
			copy := value
			return &copy, nil
		}
	}
	if _, err := cache.getOrLoad("three", load(3)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.getOrLoad("four", load(4)); err != nil {
		t.Fatal(err)
	}
	if cache.len() != 1 {
		t.Fatalf("cache len after weighted eviction = %d, want 1", cache.len())
	}
	if _, err := cache.getOrLoad("oversized", load(6)); err != nil {
		t.Fatal(err)
	}
	if cache.len() != 1 {
		t.Fatalf("oversized value changed cache len to %d, want 1", cache.len())
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.bytes != 4 {
		t.Fatalf("cached bytes = %d, want 4", cache.bytes)
	}
	if _, ok := cache.items["four"]; !ok {
		t.Fatal("most recent weighted entry was evicted")
	}
}

func TestBoundedArchiveCacheDeleteDuringWeightedLoadPreventsCaching(t *testing.T) {
	cache := newWeightedBoundedArchiveCache[int](10, 100, func(value *int) int64 {
		return int64(*value)
	})
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad("run-1|report", func() (*int, error) {
			close(started)
			<-release
			value := 5
			return &value, nil
		})
		done <- err
	}()
	<-started
	cache.deleteTest("run-1")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if cache.len() != 0 {
		t.Fatalf("cache len = %d, want in-flight deleted value to remain uncached", cache.len())
	}
}
