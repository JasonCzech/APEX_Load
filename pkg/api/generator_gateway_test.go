package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func seedReverseExecutionTarget(t *testing.T, store *storage.SQLiteStore, token string) *storage.ExecutionTarget {
	t.Helper()
	connection := &storage.CloudConnection{ID: "cloud-reverse", Name: "Azure", Provider: storage.CloudProviderAzure, AuthMode: storage.CloudAuthAzureIdentity, AzureSubscriptionID: "subscription"}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatalf("CreateCloudConnection: %v", err)
	}
	target := &storage.ExecutionTarget{
		ID: "target-reverse", Name: "Reverse", Kind: storage.ExecutionTargetAzureContainerApps,
		CloudConnectionID: connection.ID, ConnectivityMode: storage.ConnectivityReverse,
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerDisabled,
		ArchiveMode:        storage.ArchiveModeControllerPull,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", EnvironmentName: "env", WarmContainerApp: "warm"},
	}
	if err := storage.SetExecutionTargetEnrollmentToken(target, token); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatalf("CreateExecutionTarget: %v", err)
	}
	return target
}

func TestGeneratorGatewayAuthenticatesTargetAndRegistersByteStream(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	token := strings.Repeat("g", 32)
	target := seedReverseExecutionTarget(t, store, token)
	server.reverseGenerators = generatorrpc.NewReverseRegistry()
	// The transport is needed during acquisition, before ordinary mutations
	// are admitted, so exercise the real WebSocket handshake in that window.
	leadership := newExecutionLeadership(nil, "recovering")
	leadership.leader.Store(true)
	leadership.reconciling.Store(true)
	leadership.localLeaseDeadline = time.Now().Add(time.Minute)
	server.runner.leadership = leadership
	httpServer := httptest.NewServer(server.mux)
	t.Cleanup(httpServer.Close)

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set(generatorGatewayIDHeader, "generator-reverse-1")
	headers.Set(generatorGatewayTargetHeader, target.ID)
	headers.Set(generatorGatewaySessionHeader, strings.Repeat("n", 32))
	headers.Set(generatorGatewayTimestampHeader, fmt.Sprintf("%d", time.Now().UTC().Unix()))
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v1/generator-gateway/connect"
	// gorilla/websocket documents that the handshake response "does not need to
	// be closed by the application", so the response is read for its status and
	// otherwise left alone. A defensive close here would have to guard Body as
	// well as the response pointer, for no benefit.
	ws, response, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("dial gateway: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("dial gateway: %v", err)
	}
	clientConn := generatorrpc.NewWebSocketConn(ws)
	t.Cleanup(func() { _ = clientConn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	controllerConn, err := server.reverseGenerators.Dial(ctx, "generator-reverse-1")
	if err != nil {
		t.Fatalf("registry Dial: %v", err)
	}
	payload := []byte("grpc-http2-bytes")
	go func() { _, _ = clientConn.Write(payload) }()
	buffer := make([]byte, len(payload))
	if _, err := controllerConn.Read(buffer); err != nil {
		t.Fatalf("controller read: %v", err)
	}
	if string(buffer) != string(payload) {
		t.Fatalf("payload = %q", buffer)
	}
}

func TestGeneratorGatewayRejectsWrongEnrollmentToken(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	target := seedReverseExecutionTarget(t, store, strings.Repeat("v", 32))
	server.reverseGenerators = generatorrpc.NewReverseRegistry()
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
	headers.Set(generatorGatewayIDHeader, "generator-reverse-1")
	headers.Set(generatorGatewayTargetHeader, target.ID)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	req.Header = headers
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestReverseEnrollmentNonceRejectsReplayAndStaleTimestamp(t *testing.T) {
	server := &Server{}
	now := time.Now().UTC()
	nonce := strings.Repeat("r", 32)
	timestamp := fmt.Sprintf("%d", now.Unix())
	if !server.consumeReverseEnrollmentNonce("target", nonce, timestamp, now) {
		t.Fatal("fresh reverse enrollment nonce was rejected")
	}
	if server.consumeReverseEnrollmentNonce("target", nonce, timestamp, now) {
		t.Fatal("replayed reverse enrollment nonce was accepted")
	}
	if server.consumeReverseEnrollmentNonce("target", strings.Repeat("s", 32), fmt.Sprintf("%d", now.Add(-3*time.Minute).Unix()), now) {
		t.Fatal("stale reverse enrollment timestamp was accepted")
	}
}

func TestReverseGatewayForwardedHTTPSRequiresExplicitManagedIngressTrust(t *testing.T) {
	t.Setenv("APEXLOAD_REVERSE_GATEWAY_TRUST_FORWARDED_HTTPS", "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if reverseGatewayRequestSecure(req, AuthSettings{}) {
		t.Fatal("untrusted forwarded HTTPS was accepted")
	}
	t.Setenv("APEXLOAD_REVERSE_GATEWAY_TRUST_FORWARDED_HTTPS", "true")
	if !reverseGatewayRequestSecure(req, AuthSettings{}) {
		t.Fatal("managed-ingress forwarded HTTPS was rejected")
	}
}

func TestGeneratorGatewayRejectsDisabledExecutionTarget(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	token := strings.Repeat("d", 32)
	target := seedReverseExecutionTarget(t, store, token)
	loaded, err := store.GetExecutionTarget(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := loaded.UpdatedAt
	loaded.Status = "disabled"
	if updated, err := store.UpdateExecutionTargetIfUnchanged(loaded, expected); err != nil || !updated {
		t.Fatalf("disable target = (%v, %v)", updated, err)
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set(generatorGatewayIDHeader, "generator-disabled")
	headers.Set(generatorGatewayTargetHeader, target.ID)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	req.Header = headers
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBurstEnrollmentRejectsFailedAndExpiredIntents(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	expired := time.Now().UTC().Add(-time.Minute)
	for _, intent := range []*storage.CapacityInstanceIntent{
		{ID: "failed", ExecutionTargetID: "target", FleetPoolID: "pool", ClientToken: "failed", LaunchNonce: "nonce-failed", GeneratorID: "generator-failed", State: storage.CapacityIntentFailed},
		{ID: "expired", ExecutionTargetID: "target", FleetPoolID: "pool", ClientToken: "expired", LaunchNonce: "nonce-expired", GeneratorID: "generator-expired", State: storage.CapacityIntentLaunching, CleanupDeadline: &expired},
	} {
		if err := store.CreateCapacityInstanceIntent(intent); err != nil {
			t.Fatal(err)
		}
	}
	if server.validateBurstGeneratorEnrollment("target", "generator-failed", "nonce-failed") {
		t.Fatal("failed capacity intent authenticated a generator")
	}
	if server.validateBurstGeneratorEnrollment("target", "generator-expired", "nonce-expired") {
		t.Fatal("expired capacity intent authenticated a generator")
	}
	if known, valid := server.validateGeneratorCapacityEnrollment("target", "generator-failed", ""); !known || valid {
		t.Fatalf("failed intent without lifecycle metadata = (%v, %v), want known and invalid", known, valid)
	}
}

func TestGeneratorGatewayRejectsKnownFailedBurstIdentityWithoutLifecycleHeader(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	token := strings.Repeat("f", 32)
	target := seedReverseExecutionTarget(t, store, token)
	if err := store.CreateCapacityInstanceIntent(&storage.CapacityInstanceIntent{
		ID: "failed-no-header", ExecutionTargetID: target.ID, FleetPoolID: "pool",
		ClientToken: "failed-no-header", LaunchNonce: "launch-failed-no-header",
		GeneratorID: "generator-failed-no-header", State: storage.CapacityIntentFailed,
	}); err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set(generatorGatewayIDHeader, "generator-failed-no-header")
	headers.Set(generatorGatewayTargetHeader, target.ID)
	headers.Set(generatorGatewaySessionHeader, strings.Repeat("q", 32))
	headers.Set(generatorGatewayTimestampHeader, fmt.Sprintf("%d", time.Now().UTC().Unix()))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	req.Header = headers
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDynamicGeneratorModeInstallsDistributedRunBackendWithoutStaticTargets(t *testing.T) {
	store, err := storage.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, EnableDynamicGenerators: true})
	backend, ok := server.runner.getExecutionBackend().(*distributedExecutionBackend)
	if !ok {
		t.Fatalf("backend = %T, want distributed execution backend", server.runner.getExecutionBackend())
	}
	if got := len(backend.configuredTargetSpecs()); got != 0 {
		t.Fatalf("initial target count = %d, want 0", got)
	}
}

// The gateway is the only pre-auth credential-accepting route reachable without
// a service account, so it carries its own budget: local login, OIDC login, and
// the CI exchange are all rate limited and this must be too.
func TestGeneratorGatewayRateLimitsEnrollmentAttempts(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	target := seedReverseExecutionTarget(t, store, strings.Repeat("v", 32))
	server.reverseGenerators = generatorrpc.NewReverseRegistry()
	server.cache = newMemoryCacheBackend(cacheRuntimeConfig{Backend: cacheBackendMemory})

	attempt := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 32))
		req.Header.Set(generatorGatewayIDHeader, "generator-reverse-1")
		req.Header.Set(generatorGatewayTargetHeader, target.ID)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		return rec.Code
	}

	// A small budget keeps the test fast; the production default is sized for a
	// whole pool reconnecting at once.
	t.Setenv(reverseGatewayRateLimitEnvVar, "5")
	// A wrong token still consumes budget; otherwise the limit would not bound
	// unauthenticated churn, which is the entire point.
	for i := 0; i < 5; i++ {
		if code := attempt(); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i, code)
		}
	}
	if code := attempt(); code != http.StatusTooManyRequests {
		t.Fatalf("status after exhausting the budget = %d, want 429", code)
	}
}

func TestGeneratorGatewayRateLimitIsScopedPerExecutionTarget(t *testing.T) {
	t.Setenv(reverseGatewayRateLimitEnvVar, "5")
	server := &Server{cache: newMemoryCacheBackend(cacheRuntimeConfig{Backend: cacheBackendMemory})}
	exhaust := func(targetID string) {
		for i := 0; i < 5; i++ {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
			if !server.allowReverseGatewayAttempt(httptest.NewRecorder(), req, targetID) {
				t.Fatalf("target %s was throttled at attempt %d", targetID, i)
			}
		}
	}
	exhaust("target-a")
	// target-a is now exhausted; target-b must still have its full budget, so a
	// noisy target cannot starve another target's generators.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	if !server.allowReverseGatewayAttempt(httptest.NewRecorder(), req, "target-b") {
		t.Fatal("a second execution target was throttled by the first target's budget")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	if server.allowReverseGatewayAttempt(httptest.NewRecorder(), req, "target-a") {
		t.Fatal("the exhausted execution target was not throttled")
	}
}

// The default budget must clear the largest fleet an operator can reasonably
// run, because a throttled reconnect turns a controller restart into an outage.
func TestReverseGatewayRateLimitDefaultsAboveRealisticFleetSize(t *testing.T) {
	t.Setenv(reverseGatewayRateLimitEnvVar, "")
	if got := configuredReverseGatewayRatePerMinute(); got < 500 {
		t.Fatalf("default budget = %d, too low for a whole pool reconnecting at once", got)
	}
	t.Setenv(reverseGatewayRateLimitEnvVar, "0")
	if got := configuredReverseGatewayRatePerMinute(); got != 0 {
		t.Fatalf("explicit 0 = %d, want the limit disabled", got)
	}
	t.Setenv(reverseGatewayRateLimitEnvVar, "not-a-number")
	if got := configuredReverseGatewayRatePerMinute(); got != defaultReverseGatewayRatePerMinute {
		t.Fatalf("malformed value = %d, want the default rather than an unlimited gateway", got)
	}
}

// A follower must not accept a reverse tunnel: the leader could never drive a
// generator stranded on it. The refusal must be retryable (5xx), because the
// reverse connector exits permanently on a 4xx.
func TestGeneratorGatewayRefusesEnrollmentOnFollower(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	token := strings.Repeat("v", 32)
	target := seedReverseExecutionTarget(t, store, token)
	// Leadership is enabled but never acquired, so this server is a follower.
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	server.reverseGenerators = generatorrpc.NewReverseRegistry()
	if server.isExecutionLeader() {
		t.Fatal("server should be a follower until it acquires the lease")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(generatorGatewayIDHeader, "generator-reverse-1")
	req.Header.Set(generatorGatewayTargetHeader, target.ID)
	rec := httptest.NewRecorder()
	server.handleGeneratorGatewayConnect(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("follower gateway status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
}
