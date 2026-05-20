package api

import (
	"context"
	"errors"
	"io/fs"
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

	reserved, err := cache.ReserveIdempotencyKey(ctx, "create-run", time.Minute)
	if err != nil {
		t.Fatalf("reserve idempotency key: %v", err)
	}
	if !reserved {
		t.Fatal("expected first idempotency reservation to succeed")
	}
	reserved, err = cache.ReserveIdempotencyKey(ctx, "create-run", time.Minute)
	if err != nil {
		t.Fatalf("reserve duplicate idempotency key: %v", err)
	}
	if reserved {
		t.Fatal("expected duplicate idempotency reservation to be rejected")
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
		KeyPrefix:     "stormrunner-test",
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
	t.Setenv("STORMRUNNER_CACHE_BACKEND", "redis")
	t.Setenv("STORMRUNNER_REDIS_URL", "")

	cache := configuredCacheBackend()
	info := cache.Info()
	if info.Backend != cacheBackendRedis || info.ConfigError == "" {
		t.Fatalf("expected unavailable redis cache info, got %#v", info)
	}
	if err := cache.Ping(context.Background()); err == nil {
		t.Fatal("expected unavailable redis ping to fail")
	}
}
