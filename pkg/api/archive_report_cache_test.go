package api

import (
	"context"
	"errors"
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

func TestBoundedArchiveCacheCanceledWaiterDoesNotPoisonSharedLoad(t *testing.T) {
	cache := newBoundedArchiveCache[int](2)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	loader := func() (*int, error) {
		calls.Add(1)
		close(started)
		<-release
		value := 42
		return &value, nil
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoadContext(firstCtx, "shared", loader)
		firstDone <- err
	}()
	<-started
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", err)
	}

	secondDone := make(chan struct {
		value *int
		err   error
	}, 1)
	go func() {
		value, err := cache.getOrLoadContext(context.Background(), "shared", loader)
		secondDone <- struct {
			value *int
			err   error
		}{value: value, err: err}
	}()
	close(release)
	second := <-secondDone
	if second.err != nil || second.value == nil || *second.value != 42 {
		t.Fatalf("healthy waiter = value:%v err:%v", second.value, second.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("shared loader calls = %d, want 1", calls.Load())
	}
}

func TestBoundedArchiveCacheRecoversOwnedLoaderPanicAndAllowsRetry(t *testing.T) {
	cache := newBoundedArchiveCache[int](2)
	const key = "run-1|report"

	if value, err := cache.getOrLoad(key, func() (*int, error) {
		panic("sensitive loader detail")
	}); value != nil || err == nil || err.Error() != "archive cache load failed" {
		t.Fatalf("panic result = value:%v err:%v", value, err)
	}
	cache.mu.Lock()
	_, stillInFlight := cache.inFlight[key]
	cache.mu.Unlock()
	if stillInFlight {
		t.Fatal("panicked load remained in-flight")
	}

	value, err := cache.getOrLoad(key, func() (*int, error) {
		result := 42
		return &result, nil
	})
	if err != nil || value == nil || *value != 42 {
		t.Fatalf("retry result = value:%v err:%v", value, err)
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

func TestBoundedArchiveCacheInvalidationDetachesStaleInFlightLoad(t *testing.T) {
	cache := newBoundedArchiveCache[int](2)
	const key = "run-1|report"
	staleStarted := make(chan struct{})
	releaseStale := make(chan struct{})
	staleDone := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad(key, func() (*int, error) {
			close(staleStarted)
			<-releaseStale
			value := 1
			return &value, nil
		})
		staleDone <- err
	}()
	<-staleStarted

	cache.deleteTest("run-1")
	freshLoads := 0
	fresh, err := cache.getOrLoad(key, func() (*int, error) {
		freshLoads++
		value := 2
		return &value, nil
	})
	if err != nil || fresh == nil || *fresh != 2 || freshLoads != 1 {
		t.Fatalf("fresh load = value:%v loads:%d err:%v", fresh, freshLoads, err)
	}
	close(releaseStale)
	if err := <-staleDone; err != nil {
		t.Fatalf("stale waiter error = %v", err)
	}

	cached, err := cache.getOrLoad(key, func() (*int, error) {
		freshLoads++
		value := 3
		return &value, nil
	})
	if err != nil || cached == nil || *cached != 2 || freshLoads != 1 {
		t.Fatalf("post-stale cache = value:%v loads:%d err:%v", cached, freshLoads, err)
	}
}
