package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
)

func TestDNSDiscoveryPreservesEnrolledReverseGenerators(t *testing.T) {
	backend := newDistributedExecutionBackend(nil, time.Second, nil)
	resolver := &fakeHostResolver{hosts: map[string][]string{"fleet.local": {"10.0.0.1"}}}
	discovery := &generatorDiscovery{specs: []generatorDiscoverySpec{{Host: "fleet.local", Port: "9090"}}, resolver: resolver, backend: backend}
	discovery.runOnce(context.Background())
	backend.AddTarget(generatorrpc.ReverseTargetPrefix + "enrolled-generator")
	backend.recordGeneratorIdentity(generatorrpc.ReverseTargetPrefix+"enrolled-generator", "enrolled-generator")
	resolver.hosts["fleet.local"] = []string{"10.0.0.1", "10.0.0.2"}
	discovery.runOnce(context.Background())
	for _, target := range backend.configuredTargets() {
		if target == generatorrpc.ReverseTargetPrefix+"enrolled-generator" {
			if got := backend.GeneratorTargetForID("enrolled-generator"); got != target {
				t.Fatalf("DNS refresh lost enrolled identity mapping: %q", got)
			}
			backend.RemoveTarget(target)
			if len(backend.configuredTargets()) != 2 || backend.GeneratorTargetForID("enrolled-generator") != "" {
				t.Fatal("disconnect failed to remove only the enrolled target")
			}
			return
		}
	}
	t.Fatalf("DNS autoscaling removed a still-connected reverse generator: %v", backend.configuredTargets())
}

func TestReverseDisconnectCleanupPreservesReconnectedGenerator(t *testing.T) {
	backend := newDistributedExecutionBackend(nil, time.Second, nil)
	server := &Server{
		reverseGenerators: generatorrpc.NewReverseRegistry(),
		runner:            &TestRunner{execution: backend},
	}
	const targetID, generatorID = "target-reconnect", "generator-reconnect"
	targetAddress := generatorrpc.ReverseTargetPrefix + generatorID
	enroll := func() uint64 {
		controllerConn, generatorConn := net.Pipe()
		t.Cleanup(func() { _ = controllerConn.Close(); _ = generatorConn.Close() })
		generation, err := server.registerReverseGenerator(targetID, generatorID, controllerConn)
		if err != nil {
			t.Fatal(err)
		}
		return generation
	}
	firstGeneration := enroll()
	server.reverseGenerators.Disconnect(generatorID, firstGeneration)
	// Reproduce the grace timer observing a disconnected generator, followed
	// by a successful reconnect before it removes the old fleet membership.
	if server.reverseGenerators.Connected(generatorID) {
		t.Fatal("initial disconnect did not close the transport")
	}
	secondGeneration := enroll()
	if server.removeDisconnectedReverseGenerator(targetID, generatorID) {
		t.Fatal("stale disconnect observation removed a newly enrolled generator")
	}
	if ids := server.reverseTargetSessionIDs(targetID); len(ids) != 1 || ids[0] != generatorID {
		t.Fatalf("reconnected session was lost: %v", ids)
	}
	if targets := backend.configuredTargets(); len(targets) != 1 || targets[0] != targetAddress {
		t.Fatalf("reconnected fleet target was lost: %v", targets)
	}
	server.reverseGenerators.Disconnect(generatorID, secondGeneration)
	if !server.removeDisconnectedReverseGenerator(targetID, generatorID) {
		t.Fatal("disconnected generator was retained after its grace period")
	}
	if len(server.reverseTargetSessionIDs(targetID)) != 0 || len(backend.configuredTargets()) != 0 {
		t.Fatal("disconnected generator membership was not removed")
	}
}

type reverseFleetHeartbeatService struct {
	pb.UnimplementedGeneratorControlServer
	reportedID string
}

func (s *reverseFleetHeartbeatService) Heartbeat(stream pb.GeneratorControl_HeartbeatServer) error {
	for {
		if _, err := stream.Recv(); err != nil {
			return err
		}
		if err := stream.Send(&pb.HeartbeatPong{GeneratorId: s.reportedID, AvailableCapacity: 100, MaxVusers: 100}); err != nil {
			return err
		}
	}
}

func newReverseFleetHeartbeatClient(t *testing.T, reportedID string) (*generatorrpc.Client, string) {
	t.Helper()
	for _, key := range []string{"APEXLOAD_GRPC_TLS_CERT", "APEXLOAD_GRPC_TLS_KEY", "APEXLOAD_GRPC_TLS_CA", "APEXLOAD_GRPC_TLS_SERVER_NAME", "APEXLOAD_GRPC_AUTH_TOKEN", "APEXLOAD_ENV"} {
		t.Setenv(key, "")
	}
	previous := generatorrpc.DefaultReverseRegistry
	generatorrpc.DefaultReverseRegistry = generatorrpc.NewReverseRegistry()
	t.Cleanup(func() { generatorrpc.DefaultReverseRegistry = previous })
	controllerConn, generatorConn := net.Pipe()
	listener := generatorrpc.NewConnListener(generatorConn.LocalAddr())
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, &reverseFleetHeartbeatService{reportedID: reportedID})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := listener.Offer(ctx, generatorConn); err != nil {
		t.Fatal(err)
	}
	if _, err := generatorrpc.DefaultReverseRegistry.Register("shared-reverse", controllerConn); err != nil {
		t.Fatal(err)
	}
	target := generatorrpc.ReverseTargetPrefix + "shared-reverse"
	activeClient, err := generatorrpc.Dial(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activeClient.Close() })
	return activeClient, target
}

func TestInventoryCanProbeReverseGeneratorAlreadyInUse(t *testing.T) {
	activeClient, target := newReverseFleetHeartbeatClient(t, "shared-reverse")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	backend := newDistributedExecutionBackend([]string{target}, time.Second, nil)
	if initial := backend.probeGeneratorLoad(ctx, target, activeClient); !initial.HeartbeatOK {
		t.Fatal("initial connection cannot probe healthy generator")
	}
	inventory := backend.SystemInventory(ctx)
	if len(inventory) != 1 || inventory[0].Status != "connected" {
		t.Fatalf("healthy reverse generator becomes unreachable solely because another controller client owns its transport: %+v", inventory)
	}
	if after := backend.probeGeneratorLoad(ctx, target, activeClient); !after.HeartbeatOK {
		t.Fatal("closing inventory's client disrupted the active run")
	}
}

func TestReverseHeartbeatCannotClaimAnotherGeneratorsIdentity(t *testing.T) {
	client, target := newReverseFleetHeartbeatClient(t, "victim")
	backend := newDistributedExecutionBackend([]string{target}, time.Second, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if snapshot := backend.probeGeneratorLoad(ctx, target, client); snapshot.HeartbeatOK || snapshot.AvailableCapacity != 0 {
		t.Fatalf("spoofed heartbeat admitted into placement: %+v", snapshot)
	}
	backend.recordGeneratorIdentity(target, "victim")
	if backend.GeneratorTargetForID("victim") != "" || backend.GeneratorIDForTarget(target) != "" {
		t.Fatal("spoofed heartbeat changed routing identity")
	}
}

func TestReverseMetricsCannotAttributeSamplesToAnotherGenerator(t *testing.T) {
	target := generatorrpc.ReverseTargetPrefix + "enrolled"
	backend := newDistributedExecutionBackend([]string{target}, time.Second, nil)
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "reverse-metric-identity", Status: "RUNNING"}
	batch := generatorrpc.MetricsBatch{TestID: test.ID, GeneratorID: "victim", TimestampUnixMs: time.Now().UnixMilli(), LifecycleState: "RUNNING", WindowMs: 1000}
	if backend.storeRemoteMetricsBatchOnly(runner, test, target, batch) {
		t.Fatal("spoofed metrics were retained")
	}
	if live, _, _, accepted, _ := backend.applyRemoteMetricsBatchWithFreshness(runner, test, target, batch, false); live != nil || accepted {
		t.Fatal("spoofed metrics were published")
	}
	if len(test.remoteMetricsBatchesByTarget) != 0 {
		t.Fatal("rejected metrics mutated run state")
	}
	snapshots := backend.buildGeneratorResourceSnapshots(test, map[string]generatorrpc.MetricsBatch{target: batch})
	if snapshots["victim"] != nil || snapshots["enrolled"] == nil {
		t.Fatal("resource snapshots trusted an unenrolled identity")
	}
	batch.GeneratorID = "enrolled"
	if !backend.storeRemoteMetricsBatchOnly(runner, test, target, batch) {
		t.Fatal("valid enrolled metrics were rejected")
	}
}

func TestGatewayRejectsCrossTargetGeneratorIdentity(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	tokenA, tokenB := strings.Repeat("a", 32), strings.Repeat("b", 32)
	targetA := seedReverseExecutionTarget(t, store, tokenA)
	targetB := *targetA
	targetB.ID, targetB.Name = "target-victim", "Victim Target"
	if err := storage.SetExecutionTargetEnrollmentToken(&targetB, tokenB); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateExecutionTarget(&targetB); err != nil {
		t.Fatal(err)
	}
	server.reverseGenerators = generatorrpc.NewReverseRegistry()
	// This test does not run the server's delayed inventory maintenance.
	server.beginBackgroundShutdown()
	httpServer := httptest.NewServer(server.mux)
	t.Cleanup(httpServer.Close)
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v1/generator-gateway/connect"
	headersFor := func(targetID, token, nonce string) http.Header {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+token)
		headers.Set(generatorGatewayIDHeader, "victim-generator")
		headers.Set(generatorGatewayTargetHeader, targetID)
		headers.Set(generatorGatewaySessionHeader, strings.Repeat(nonce, 32))
		headers.Set(generatorGatewayTimestampHeader, fmt.Sprintf("%d", time.Now().Unix()))
		return headers
	}
	victim, _, err := websocket.DefaultDialer.Dial(wsURL, headersFor(targetB.ID, tokenB, "v"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = victim.Close() })
	for _, identity := range []string{"tunnel://victim-generator", "victim-generator|region=other", "victim-generator?identity=other"} {
		headers := headersFor(targetA.ID, tokenA, "m")
		headers.Set(generatorGatewayIDHeader, identity)
		aliased, response, err := websocket.DefaultDialer.Dial(wsURL, headers)
		if err == nil {
			_ = aliased.Close()
			t.Fatalf("ambiguous generator identity %q was accepted", identity)
		}
		if response == nil || response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid identity %q response = %v, %v", identity, response, err)
		}
	}
	attacker, response, err := websocket.DefaultDialer.Dial(wsURL, headersFor(targetA.ID, tokenA, "a"))
	if err == nil {
		t.Cleanup(func() { _ = attacker.Close() })
		t.Fatalf("target A's token successfully replaced target B's generator connection (HTTP %d)", response.StatusCode)
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-target rejection = %v, %v", response, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	controllerConn, err := server.reverseGenerators.Dial(ctx, "victim-generator")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = controllerConn.Write([]byte("still-victim")) }()
	_ = victim.SetReadDeadline(time.Now().Add(time.Second))
	_, payload, err := victim.ReadMessage()
	if err != nil || string(payload) != "still-victim" {
		t.Fatalf("rejected enrollment disturbed victim transport: %q, %v", payload, err)
	}
	// Same-target reconnects remain supported and cannot lose their binding.
	reconnected, _, err := websocket.DefaultDialer.Dial(wsURL, headersFor(targetB.ID, tokenB, "r"))
	if err != nil {
		t.Fatalf("same-target reconnect failed: %v", err)
	}
	t.Cleanup(func() { _ = reconnected.Close() })
}

type unavailableEnrollmentIntentStore struct{ SQLiteStore *storage.SQLiteStore }

func (*unavailableEnrollmentIntentStore) ListCapacityInstanceIntents(string, string) ([]*storage.CapacityInstanceIntent, error) {
	return nil, errors.New("intent store unavailable")
}

func TestGatewayFailsClosedWhenCapacityIntentAuthorityIsUnavailable(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	token := strings.Repeat("s", 32)
	target := seedReverseExecutionTarget(t, store, token)
	server.store = sqliteOverlay(&unavailableEnrollmentIntentStore{store})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(generatorGatewayIDHeader, "generator")
	req.Header.Set(generatorGatewayTargetHeader, target.ID)
	req.Header.Set(generatorGatewaySessionHeader, strings.Repeat("x", 32))
	req.Header.Set(generatorGatewayTimestampHeader, fmt.Sprintf("%d", time.Now().Unix()))
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable authority must reject with retryable status, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
