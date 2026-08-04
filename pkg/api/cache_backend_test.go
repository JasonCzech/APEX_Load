package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestMemoryCacheBackendStoresLiveStateWithTTL(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "test", FanoutChannel: "events"})
	ctx := context.Background()
	snapshot := RunLiveStateSnapshot{
		TestID:    "run-1",
		TestName:  "Checkout",
		ProjectID: "project-1",
		Status:    "RUNNING",
		UpdatedAt: time.Now().UTC(),
	}

	if err := cache.SetRunSnapshot(ctx, snapshot, time.Second); err != nil {
		t.Fatalf("set run snapshot: %v", err)
	}
	got, err := cache.GetRunSnapshot(ctx, snapshot.TestID)
	if err != nil {
		t.Fatalf("get run snapshot: %v", err)
	}
	if got.TestID != snapshot.TestID || got.Status != snapshot.Status || got.ProjectID != snapshot.ProjectID {
		t.Fatalf("unexpected snapshot: %#v", got)
	}

	if err := cache.SetRunSnapshot(ctx, snapshot, time.Nanosecond); err != nil {
		t.Fatalf("set expiring run snapshot: %v", err)
	}
	time.Sleep(time.Millisecond)
	if _, err := cache.GetRunSnapshot(ctx, snapshot.TestID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected expired snapshot to be missing, got %v", err)
	}
}

func TestMemoryCacheBackendIdempotencyAndRateLimit(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "test", FanoutChannel: "events"})
	ctx := context.Background()

	reserved, err := cache.ReserveIdempotencyKey(ctx, "create-run", "owner-1", time.Minute)
	if err != nil {
		t.Fatalf("reserve idempotency key: %v", err)
	}
	if !reserved {
		t.Fatal("expected first idempotency reservation to succeed")
	}
	reserved, err = cache.ReserveIdempotencyKey(ctx, "create-run", "owner-2", time.Minute)
	if err != nil {
		t.Fatalf("reserve duplicate idempotency key: %v", err)
	}
	if reserved {
		t.Fatal("expected duplicate idempotency reservation to be rejected")
	}
	if released, err := cache.ReleaseIdempotencyKey(ctx, "create-run", "owner-2"); err != nil || released {
		t.Fatalf("non-owner release = %v, %v", released, err)
	}
	if released, err := cache.ReleaseIdempotencyKey(ctx, "create-run", "owner-1"); err != nil || !released {
		t.Fatalf("owner release = %v, %v", released, err)
	}
	if reserved, err = cache.ReserveIdempotencyKey(ctx, "create-run", "owner-3", time.Minute); err != nil || !reserved {
		t.Fatalf("reserve after release = %v, %v", reserved, err)
	}

	allowed, remaining, err := cache.AllowRateLimit(ctx, "api-key", 2, time.Minute)
	if err != nil || !allowed || remaining != 1 {
		t.Fatalf("first rate-limit call = allowed %v remaining %d err %v", allowed, remaining, err)
	}
	allowed, remaining, err = cache.AllowRateLimit(ctx, "api-key", 2, time.Minute)
	if err != nil || !allowed || remaining != 0 {
		t.Fatalf("second rate-limit call = allowed %v remaining %d err %v", allowed, remaining, err)
	}
	allowed, remaining, err = cache.AllowRateLimit(ctx, "api-key", 2, time.Minute)
	if err != nil || allowed || remaining != 0 {
		t.Fatalf("third rate-limit call = allowed %v remaining %d err %v", allowed, remaining, err)
	}
}

func TestMemoryCacheBackendAtomicLoadOrStore(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "test", FanoutChannel: "events"})
	ctx := context.Background()
	value := []byte("first")
	actual, loaded, err := cache.LoadOrStoreValue(ctx, "exchange", value, time.Minute)
	if err != nil || loaded || string(actual) != "first" {
		t.Fatalf("first load-or-store = %q loaded=%v err=%v", actual, loaded, err)
	}
	value[0], actual[0] = 'x', 'y'
	actual, loaded, err = cache.LoadOrStoreValue(ctx, "exchange", []byte("second"), time.Minute)
	if err != nil || !loaded || string(actual) != "first" {
		t.Fatalf("second load-or-store = %q loaded=%v err=%v", actual, loaded, err)
	}

	exerciseConcurrentLoadOrStore(t, []CacheBackend{cache}, "concurrent")

	if _, _, err := cache.LoadOrStoreValue(ctx, "expires", []byte("old"), time.Nanosecond); err != nil {
		t.Fatalf("store expiring value: %v", err)
	}
	time.Sleep(time.Millisecond)
	actual, loaded, err = cache.LoadOrStoreValue(ctx, "expires", []byte("new"), time.Minute)
	if err != nil || loaded || string(actual) != "new" {
		t.Fatalf("post-expiry load-or-store = %q loaded=%v err=%v", actual, loaded, err)
	}
}

func TestRedisCacheBackendAtomicLoadOrStoreAcrossClients(t *testing.T) {
	server := miniredis.RunT(t)
	newBackend := func() CacheBackend {
		cache, err := newCacheBackend(cacheRuntimeConfig{
			Backend: cacheBackendRedis, RedisURL: "redis://" + server.Addr() + "/0", KeyPrefix: "atomic-test",
			Timeout: time.Second, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		})
		if err != nil {
			t.Fatalf("new redis cache: %v", err)
		}
		t.Cleanup(func() { _ = cache.Close() })
		return cache
	}
	caches := []CacheBackend{newBackend(), newBackend()}
	exerciseConcurrentLoadOrStore(t, caches, "cross-controller")

	actual, loaded, err := caches[0].LoadOrStoreValue(context.Background(), "ttl", []byte("old"), time.Second)
	if err != nil || loaded || string(actual) != "old" {
		t.Fatalf("first Redis TTL value = %q loaded=%v err=%v", actual, loaded, err)
	}
	server.FastForward(2 * time.Second)
	actual, loaded, err = caches[1].LoadOrStoreValue(context.Background(), "ttl", []byte("new"), time.Minute)
	if err != nil || loaded || string(actual) != "new" {
		t.Fatalf("post-expiry Redis value = %q loaded=%v err=%v", actual, loaded, err)
	}
}

func TestRedisRateLimitAtomicallyInstallsAndRepairsTTL(t *testing.T) {
	server := miniredis.RunT(t)
	cache, err := newRedisCacheBackend(cacheRuntimeConfig{
		Backend: cacheBackendRedis, RedisURL: "redis://" + server.Addr() + "/0", KeyPrefix: "rate-test",
		Timeout: time.Second, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	if allowed, _, err := cache.AllowRateLimit(context.Background(), "actor", 2, time.Minute); err != nil || !allowed {
		t.Fatalf("first rate limit = allowed %v err %v", allowed, err)
	}
	key := cache.key("rate-limit", "actor")
	if ttl := server.TTL(key); ttl <= 0 {
		t.Fatalf("initial TTL = %v", ttl)
	}
	server.SetTTL(key, 0)
	if _, _, err := cache.AllowRateLimit(context.Background(), "actor", 2, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ttl := server.TTL(key); ttl <= 0 {
		t.Fatalf("repaired TTL = %v", ttl)
	}
}

func TestAtomicLoadOrStoreValidationAndUnavailableBackend(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "test"})
	if _, _, err := cache.LoadOrStoreValue(context.Background(), "", []byte("value"), time.Minute); err == nil {
		t.Fatal("empty atomic key was accepted")
	}
	if _, _, err := cache.LoadOrStoreValue(context.Background(), "key", []byte("value"), 0); err == nil {
		t.Fatal("non-positive atomic TTL was accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cache.LoadOrStoreValue(cancelled, "key", []byte("value"), time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled atomic operation error = %v, want context.Canceled", err)
	}
	unavailable := &unavailableCacheBackend{err: errors.New("cache unavailable")}
	if _, _, err := unavailable.LoadOrStoreValue(context.Background(), "key", []byte("value"), time.Minute); err == nil {
		t.Fatal("unavailable backend accepted atomic load-or-store")
	}
}

func exerciseConcurrentLoadOrStore(t *testing.T, caches []CacheBackend, key string) {
	t.Helper()
	const goroutines = 64
	type result struct {
		actual []byte
		loaded bool
		err    error
	}
	results := make(chan result, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			actual, loaded, err := caches[index%len(caches)].LoadOrStoreValue(context.Background(), key, []byte(fmt.Sprintf("candidate-%d", index)), time.Minute)
			results <- result{actual: actual, loaded: loaded, err: err}
		}(index)
	}
	close(start)
	wg.Wait()
	close(results)

	var authoritative string
	stores := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent atomic load-or-store: %v", result.err)
		}
		if authoritative == "" {
			authoritative = string(result.actual)
		}
		if string(result.actual) != authoritative {
			t.Fatalf("atomic callers observed different values: %q and %q", authoritative, result.actual)
		}
		if !result.loaded {
			stores++
		}
	}
	if stores != 1 {
		t.Fatalf("successful stores = %d, want exactly 1", stores)
	}
}

func TestMemoryCacheBackendFanout(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{KeyPrefix: "test", FanoutChannel: "events"})
	ctx := context.Background()
	sub, err := cache.Subscribe(ctx, "events")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	if err := cache.Publish(ctx, "events", []byte(`{"type":"test_started"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case got := <-sub.Messages():
		if string(got) != `{"type":"test_started"}` {
			t.Fatalf("unexpected message: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fanout message")
	}
}

func TestRedisCacheBackendStoresLiveStateAndFanout(t *testing.T) {
	server := miniredis.RunT(t)
	cache, err := newCacheBackend(cacheRuntimeConfig{
		Backend:       cacheBackendRedis,
		RedisURL:      "redis://" + server.Addr() + "/0",
		KeyPrefix:     "apexload-test",
		Timeout:       time.Second,
		DialTimeout:   time.Second,
		ReadTimeout:   time.Second,
		WriteTimeout:  time.Second,
		FanoutChannel: "events",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()
	if err := cache.Ping(ctx); err != nil {
		t.Fatalf("ping redis cache: %v", err)
	}
	snapshot := RunLiveStateSnapshot{TestID: "run-2", TestName: "Login", Status: "RUNNING", UpdatedAt: time.Now().UTC()}
	if err := cache.SetRunSnapshot(ctx, snapshot, time.Minute); err != nil {
		t.Fatalf("set run snapshot: %v", err)
	}
	got, err := cache.GetRunSnapshot(ctx, snapshot.TestID)
	if err != nil {
		t.Fatalf("get run snapshot: %v", err)
	}
	if got.TestID != snapshot.TestID || got.TestName != snapshot.TestName {
		t.Fatalf("unexpected redis snapshot: %#v", got)
	}

	sub, err := cache.Subscribe(ctx, "events")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()
	if err := cache.Publish(ctx, "events", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case got := <-sub.Messages():
		if string(got) != "hello" {
			t.Fatalf("unexpected fanout payload: %q", string(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for redis fanout message")
	}
}

func TestConfiguredCacheBackendReturnsUnavailableRedisWhenURLMissing(t *testing.T) {
	t.Setenv("APEXLOAD_CACHE_BACKEND", "redis")
	t.Setenv("APEXLOAD_REDIS_URL", "")

	cache := configuredCacheBackend()
	info := cache.Info()
	if info.Backend != cacheBackendRedis || info.ConfigError == "" {
		t.Fatalf("expected unavailable redis cache info, got %#v", info)
	}
	if err := cache.Ping(context.Background()); err == nil {
		t.Fatal("expected unavailable redis ping to fail")
	}
}

func TestMemoryCacheSweepsExpiredUniqueKeys(t *testing.T) {
	cache := newMemoryCacheBackend(cacheRuntimeConfig{})
	expired := time.Now().Add(-time.Minute)
	cache.mu.Lock()
	for index := 0; index < 300; index++ {
		key := fmt.Sprintf("expired-%d", index)
		cache.entries[key] = memoryCacheEntry{payload: []byte("x"), expiresAt: expired}
		cache.rateLimits[key] = memoryRateBucket{count: 1, resetAt: expired}
	}
	cache.writes = 255
	cache.mu.Unlock()
	if _, err := cache.ReserveIdempotencyKey(context.Background(), "fresh", "owner", time.Minute); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != 1 || len(cache.rateLimits) != 0 {
		t.Fatalf("cache sizes after sweep = entries:%d rate:%d", len(cache.entries), len(cache.rateLimits))
	}
}
