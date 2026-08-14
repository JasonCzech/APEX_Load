package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	httpServer := httptest.NewServer(server.mux)
	t.Cleanup(httpServer.Close)

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set(generatorGatewayIDHeader, "generator-reverse-1")
	headers.Set(generatorGatewayTargetHeader, target.ID)
	headers.Set(generatorGatewaySessionHeader, strings.Repeat("n", 32))
	headers.Set(generatorGatewayTimestampHeader, fmt.Sprintf("%d", time.Now().UTC().Unix()))
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v1/generator-gateway/connect"
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
