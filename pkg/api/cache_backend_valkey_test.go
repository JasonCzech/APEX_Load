package api

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestCacheBackendIsRemote(t *testing.T) {
	cases := map[string]bool{
		"redis":  true,
		"valkey": true,
		"Redis":  true,
		"VALKEY": true,
		"memory": false,
		"":       false,
		"sqlite": false,
	}
	for backend, want := range cases {
		if got := cacheBackendIsRemote(backend); got != want {
			t.Errorf("cacheBackendIsRemote(%q) = %v, want %v", backend, got, want)
		}
	}
}

func TestRedactRedisEndpoint(t *testing.T) {
	cases := []struct {
		addr string
		tls  bool
		want string
	}{
		{"redis.prod.svc:6379", false, "redis://redis.prod.svc:6379"},
		{"valkey.prod.svc:6379", true, "rediss://valkey.prod.svc:6379"},
		{"", false, ""},
	}
	for _, tc := range cases {
		if got := redactRedisEndpoint(tc.addr, tc.tls); got != tc.want {
			t.Errorf("redactRedisEndpoint(%q, %v) = %q, want %q", tc.addr, tc.tls, got, tc.want)
		}
	}
}

func TestValkeyAliasRequiresURL(t *testing.T) {
	if _, err := newCacheBackend(cacheRuntimeConfig{Backend: cacheBackendValkey}); err == nil {
		t.Fatal("expected valkey backend without APEXLOAD_REDIS_URL to error")
	}
}

func TestValkeyBackendReportsValkeyLabelAndEndpoint(t *testing.T) {
	server := miniredis.RunT(t)
	cache, err := newCacheBackend(cacheRuntimeConfig{
		Backend:   cacheBackendValkey,
		RedisURL:  "redis://" + server.Addr() + "/0",
		KeyPrefix: "apexload-test",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("new valkey cache: %v", err)
	}
	defer cache.Close()

	info := cache.Info()
	if info.Backend != cacheBackendValkey {
		t.Fatalf("backend label = %q, want valkey", info.Backend)
	}
	if info.Endpoint != "redis://"+server.Addr() {
		t.Fatalf("endpoint = %q, want %q", info.Endpoint, "redis://"+server.Addr())
	}
	if err := cache.Ping(context.Background()); err != nil {
		t.Fatalf("ping valkey cache: %v", err)
	}
}

func TestCacheReadinessDetailsForRemoteBackend(t *testing.T) {
	details := cacheReadinessDetails(CacheBackendInfo{
		Backend:     cacheBackendValkey,
		KeyPrefix:   "apexload",
		RedisURLSet: true,
		TLSEnabled:  true,
		Endpoint:    "rediss://valkey:6379",
		Timeout:     2 * time.Second,
	})
	if details["cache_backend"] != cacheBackendValkey {
		t.Fatalf("cache_backend = %#v, want valkey", details["cache_backend"])
	}
	if details["endpoint"] != "rediss://valkey:6379" {
		t.Fatalf("endpoint = %#v, want rediss://valkey:6379", details["endpoint"])
	}
	if details["tls_enabled"] != true {
		t.Fatalf("tls_enabled = %#v, want true", details["tls_enabled"])
	}
	if details["redis_url_configured"] != true {
		t.Fatalf("redis_url_configured = %#v, want true", details["redis_url_configured"])
	}
}

func TestCacheBackendReadinessReportsLatencyAndEndpoint(t *testing.T) {
	server := miniredis.RunT(t)
	cache, err := newCacheBackend(cacheRuntimeConfig{
		Backend:  cacheBackendValkey,
		RedisURL: "redis://" + server.Addr() + "/0",
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("new valkey cache: %v", err)
	}
	defer cache.Close()

	srv := &Server{cache: cache}
	check := srv.cacheBackendReadiness(context.Background())
	if check.Status != readinessStatusReady {
		t.Fatalf("status = %q, want ready (message=%s)", check.Status, check.Message)
	}
	if check.Message != "valkey cache backend is reachable" {
		t.Fatalf("message = %q, want valkey reachable", check.Message)
	}
	if _, ok := check.Details["probe_latency_ms"]; !ok {
		t.Fatalf("expected probe_latency_ms in details: %#v", check.Details)
	}
	if check.Details["endpoint"] != "redis://"+server.Addr() {
		t.Fatalf("endpoint = %#v, want %q", check.Details["endpoint"], "redis://"+server.Addr())
	}

	// A configured-but-unreachable remote backend must fail readiness hard.
	server.Close()
	down := srv.cacheBackendReadiness(context.Background())
	if down.Status != readinessStatusNotReady {
		t.Fatalf("status after outage = %q, want not_ready", down.Status)
	}
}
