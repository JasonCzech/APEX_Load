package generatorrpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func commitTestWork(t *testing.T, service *Service, testID string, generation uint64) {
	t.Helper()
	ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: testID, ExecutionGeneration: generation})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork %s = (%#v, %v)", testID, ack, err)
	}
}

func TestServiceAssignWorkRejectsMissingCachedScript(t *testing.T) {
	service := newIsolatedService(t, 50)

	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:     "test-1",
		GroupName:  "Group A",
		ScriptId:   "script-1",
		VuserCount: 5,
	})
	if err != nil {
		t.Fatalf("AssignWork returned error: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatal("expected assignment to be rejected when no script is cached")
	}
	if !strings.Contains(ack.GetErrorMessage(), "not cached") {
		t.Fatalf("expected missing script error, got %q", ack.GetErrorMessage())
	}
}

func TestAssignmentProtoCarriesRedactionRules(t *testing.T) {
	assignment := toProtoAssignment(Assignment{
		TestID:         "test-1",
		GroupName:      "Group A",
		RedactionRules: []engine.RedactionRule{{Name: "customer", Pattern: `customer-[0-9]+`, Replacement: "[CUSTOMER]"}},
	})
	if len(assignment.GetRedactionRules()) != 1 {
		t.Fatalf("redaction rules = %#v, want one", assignment.GetRedactionRules())
	}
	rule := assignment.GetRedactionRules()[0]
	if rule.GetName() != "customer" || rule.GetPattern() != `customer-[0-9]+` || rule.GetReplacement() != "[CUSTOMER]" {
		t.Fatalf("redaction rule = %#v", rule)
	}
}

func TestBroadcastMetricsCountsSubscriberDrops(t *testing.T) {
	service := newIsolatedService(t, 50)
	_, subscriber, _, err := service.registerSubscriber(0)
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	for i := 0; i < cap(subscriber.updates); i++ {
		subscriber.updates <- &pb.MetricsBatch{TestId: "queued"}
	}

	service.broadcastMetrics(&pb.MetricsBatch{TestId: "dropped", ExecutionGeneration: 0})

	if got := service.droppedBatches.Load(); got != 1 {
		t.Fatalf("dropped batches = %d, want 1", got)
	}
}

func TestServiceAssignWorkRejectsWhileDraining(t *testing.T) {
	service := newIsolatedService(t, 50)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script-1", Content: []byte(`{"name":"demo"}`)}); err != nil {
		t.Fatalf("DistributeScript returned error: %v", err)
	}
	service.SetDraining(true)

	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:        "test-1",
		GroupName:     "Group A",
		ScriptId:      "script-1",
		VuserCount:    5,
		ScriptContent: []byte(`{"name":"demo"}`),
	})
	if err != nil {
		t.Fatalf("AssignWork returned error: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatal("expected assignment to be rejected while draining")
	}
	if !strings.Contains(ack.GetErrorMessage(), "draining") {
		t.Fatalf("error = %q, want draining", ack.GetErrorMessage())
	}
}

func TestClientRoundTripDistributeAssignAndHeartbeat(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := newIsolatedService(t, 50)
	pb.RegisterGeneratorControlServer(server, service)

	go func() {
		service.podResources = podResourceCapacity{CPULimitCores: 2, MemoryLimitBytes: 2 * 1024 * 1024 * 1024, MaxVUsers: 50}
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	client := NewClient(conn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	scriptAck, err := client.DistributeScript(ctx, Script{
		ID:          "script-1",
		Content:     []byte(`{"name":"demo"}`),
		ContentHash: "hash-1",
	})
	if err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	if !scriptAck.Cached {
		t.Fatal("expected distributed script to be cached")
	}

	assignmentAck, err := client.AssignWork(ctx, Assignment{
		TestID:     "test-1",
		GroupName:  "Group A",
		ScriptID:   "script-1",
		VUserCount: 10,
		Environment: map[string]string{
			"TARGET_HOST": "https://example.test",
		},
	})
	if err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if !assignmentAck.Accepted {
		t.Fatalf("expected assignment acceptance, got error %q", assignmentAck.ErrorMessage)
	}
	if assignmentAck.AvailableCapacity != 40 {
		t.Fatalf("available capacity = %d, want 40", assignmentAck.AvailableCapacity)
	}
	if commit, err := client.CommitWork(ctx, "test-1", 0); err != nil || !commit.Accepted {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}

	metrics, err := client.OpenMetricsStream(ctx)
	if err != nil {
		t.Fatalf("OpenMetricsStream: %v", err)
	}
	batch, err := metrics.Receive()
	if err != nil {
		t.Fatalf("Receive initial metrics batch: %v", err)
	}
	if batch.TestID != "test-1" {
		t.Fatalf("metrics test id = %q, want test-1", batch.TestID)
	}
	if batch.LifecycleState != "RUNNING" {
		t.Fatalf("initial lifecycle state = %q, want RUNNING", batch.LifecycleState)
	}
	if batch.Stats.ActiveVUsers != 10 {
		t.Fatalf("initial active vusers = %d, want 10", batch.Stats.ActiveVUsers)
	}

	adjustmentAck, err := client.AdjustVUsers(ctx, VUserAdjustment{
		TestID:       "test-1",
		GroupName:    "Group A",
		TargetVUsers: 12,
	})
	if err != nil {
		t.Fatalf("AdjustVUsers: %v", err)
	}
	if !adjustmentAck.Accepted {
		t.Fatal("expected vuser adjustment to be accepted")
	}
	if adjustmentAck.CurrentVUsers != 12 {
		t.Fatalf("current vusers = %d, want 12", adjustmentAck.CurrentVUsers)
	}

	durationAck, err := client.AdjustDuration(ctx, DurationAdjustment{
		TestID:                "test-1",
		TargetDurationSeconds: 90,
	})
	if err != nil {
		t.Fatalf("AdjustDuration: %v", err)
	}
	if !durationAck.Accepted {
		t.Fatal("expected duration adjustment to be accepted")
	}
	if durationAck.CurrentDurationSeconds != 90 {
		t.Fatalf("current duration seconds = %d, want 90", durationAck.CurrentDurationSeconds)
	}
	batch, err = metrics.Receive()
	if err != nil {
		t.Fatalf("Receive adjusted metrics batch: %v", err)
	}
	if batch.Stats.ActiveVUsers != 12 {
		t.Fatalf("adjusted active vusers = %d, want 12", batch.Stats.ActiveVUsers)
	}

	if err := metrics.SendPause(PauseControl{TestID: "test-1", Pause: true}); err != nil {
		t.Fatalf("SendPause(true): %v", err)
	}
	batch, err = metrics.Receive()
	if err != nil {
		t.Fatalf("Receive paused metrics batch: %v", err)
	}
	if batch.LifecycleState != "PAUSED" {
		t.Fatalf("paused lifecycle state = %q, want PAUSED", batch.LifecycleState)
	}
	if batch.Stats.ActiveVUsers != 0 {
		t.Fatalf("paused active vusers = %d, want 0", batch.Stats.ActiveVUsers)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		paused := service.assignments["test-1"].paused
		service.mu.RUnlock()
		if paused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected pause control to mark assignment paused")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := metrics.SendPause(PauseControl{TestID: "test-1", Pause: false}); err != nil {
		t.Fatalf("SendPause(false): %v", err)
	}
	batch, err = metrics.Receive()
	if err != nil {
		t.Fatalf("Receive resumed metrics batch: %v", err)
	}
	if batch.LifecycleState != "RUNNING" {
		t.Fatalf("resumed lifecycle state = %q, want RUNNING", batch.LifecycleState)
	}
	if batch.Stats.ActiveVUsers != 12 {
		t.Fatalf("resumed active vusers = %d, want 12", batch.Stats.ActiveVUsers)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		paused := service.assignments["test-1"].paused
		service.mu.RUnlock()
		if !paused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected resume control to clear paused assignment state")
		}
		time.Sleep(10 * time.Millisecond)
	}

	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatalf("OpenHeartbeat: %v", err)
	}
	if err := heartbeat.Send(Heartbeat{GeneratorID: service.generatorID, TimestampUnixMs: 1234}); err != nil {
		t.Fatalf("Heartbeat Send: %v", err)
	}
	pong, err := heartbeat.Receive()
	if err != nil {
		t.Fatalf("Heartbeat Receive: %v", err)
	}
	if pong.GeneratorID != service.generatorID {
		t.Fatalf("heartbeat generator id = %q, want %q", pong.GeneratorID, service.generatorID)
	}
	if pong.TimestampUnixMs < 1234 {
		t.Fatalf("heartbeat pong timestamp = %d, want >= 1234", pong.TimestampUnixMs)
	}
	if !reflect.DeepEqual(pong.ActiveTestIDs, []string{"test-1"}) {
		t.Fatalf("active test ids = %v, want [test-1]", pong.ActiveTestIDs)
	}
	if want := buildinfo.Get().Version; pong.Version != want {
		t.Fatalf("heartbeat version = %q, want %q", pong.Version, want)
	}
	if !hasString(pong.SupportedProtocols, "http") {
		t.Fatalf("supported protocols = %v, want http", pong.SupportedProtocols)
	}
	if !hasString(pong.CompatibleFeatures, "http") {
		t.Fatalf("compatible features = %v, want http", pong.CompatibleFeatures)
	}
	if !hasString(pong.CompatibleFeatures, FeatureEvacuationAssignmentStateV1) {
		t.Fatalf("compatible features = %v, want %s", pong.CompatibleFeatures, FeatureEvacuationAssignmentStateV1)
	}
	if pong.Stats.ActiveVUsers != 12 {
		t.Fatalf("heartbeat active vusers = %d, want 12", pong.Stats.ActiveVUsers)
	}
	if pong.AvailableCapacity != 38 {
		t.Fatalf("heartbeat available capacity = %d, want 38", pong.AvailableCapacity)
	}
	if pong.CPULimitCores != 2 {
		t.Fatalf("heartbeat cpu limit cores = %v, want 2", pong.CPULimitCores)
	}
	if pong.MemoryLimitBytes != 2*1024*1024*1024 {
		t.Fatalf("heartbeat memory limit bytes = %d, want 2147483648", pong.MemoryLimitBytes)
	}
	if pong.MaxVUsers != 50 {
		t.Fatalf("heartbeat max vusers = %d, want 50", pong.MaxVUsers)
	}
	stopAck, err := client.StopTest(ctx, "test-1", true)
	if err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	if !stopAck.Acknowledged {
		t.Fatal("expected stop acknowledgement")
	}
	if stopAck.PendingVUsers != 12 {
		t.Fatalf("pending vusers = %d, want 12", stopAck.PendingVUsers)
	}
	batch, err = metrics.Receive()
	if err != nil {
		t.Fatalf("Receive stopped metrics batch: %v", err)
	}
	if batch.LifecycleState != "COMPLETE" {
		t.Fatalf("stopped lifecycle state = %q, want COMPLETE", batch.LifecycleState)
	}
	if batch.Stats.ActiveVUsers != 0 {
		t.Fatalf("stopped active vusers = %d, want 0", batch.Stats.ActiveVUsers)
	}
	if err := heartbeat.Send(Heartbeat{GeneratorID: service.generatorID, TimestampUnixMs: 1235}); err != nil {
		t.Fatalf("Heartbeat Send after stop: %v", err)
	}
	pong, err = heartbeat.Receive()
	if err != nil {
		t.Fatalf("Heartbeat Receive after stop: %v", err)
	}
	if len(pong.ActiveTestIDs) != 0 {
		t.Fatalf("active test ids after stop = %v, want empty", pong.ActiveTestIDs)
	}
	if pong.Stats.ActiveVUsers != 0 {
		t.Fatalf("heartbeat active vusers after stop = %d, want 0", pong.Stats.ActiveVUsers)
	}
	if pong.AvailableCapacity != 50 {
		t.Fatalf("heartbeat available capacity after stop = %d, want 50", pong.AvailableCapacity)
	}
	if err := metrics.CloseSend(); err != nil {
		t.Fatalf("Metrics CloseSend: %v", err)
	}
	if err := heartbeat.CloseSend(); err != nil {
		t.Fatalf("Heartbeat CloseSend: %v", err)
	}
}

func TestServiceFailTestBroadcastsFailedLifecycle(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := newIsolatedService(t, 50)
	pb.RegisterGeneratorControlServer(server, service)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	client := NewClient(conn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	if _, err := client.DistributeScript(ctx, Script{ID: "script-1", Content: []byte(`{"name":"demo"}`), ContentHash: "hash-1"}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	if _, err := client.AssignWork(ctx, Assignment{TestID: "test-1", GroupName: "Group A", ScriptID: "script-1", VUserCount: 10}); err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if commit, err := client.CommitWork(ctx, "test-1", 0); err != nil || !commit.Accepted {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}

	metrics, err := client.OpenMetricsStream(ctx)
	if err != nil {
		t.Fatalf("OpenMetricsStream: %v", err)
	}
	if _, err := metrics.Receive(); err != nil {
		t.Fatalf("Receive initial metrics batch: %v", err)
	}

	service.FailTest("test-1", "generator execution failed")

	batch, err := metrics.Receive()
	if err != nil {
		t.Fatalf("Receive failed metrics batch: %v", err)
	}
	if batch.LifecycleState != "FAILED" {
		t.Fatalf("failed lifecycle state = %q, want FAILED", batch.LifecycleState)
	}
	if batch.LifecycleError != "generator execution failed" {
		t.Fatalf("failed lifecycle error = %q, want generator execution failed", batch.LifecycleError)
	}
	if batch.Stats.ActiveVUsers != 0 {
		t.Fatalf("failed active vusers = %d, want 0", batch.Stats.ActiveVUsers)
	}

	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatalf("OpenHeartbeat: %v", err)
	}
	if err := heartbeat.Send(Heartbeat{GeneratorID: service.generatorID, TimestampUnixMs: 1236}); err != nil {
		t.Fatalf("Heartbeat Send after failure: %v", err)
	}
	pong, err := heartbeat.Receive()
	if err != nil {
		t.Fatalf("Heartbeat Receive after failure: %v", err)
	}
	if len(pong.ActiveTestIDs) != 0 {
		t.Fatalf("active test ids after failure = %v, want empty", pong.ActiveTestIDs)
	}
	if err := metrics.CloseSend(); err != nil {
		t.Fatalf("Metrics CloseSend: %v", err)
	}
	if err := heartbeat.CloseSend(); err != nil {
		t.Fatalf("Heartbeat CloseSend: %v", err)
	}
}

func TestServicePublishMetricsBroadcastsTransactions(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := newIsolatedService(t, 50)
	pb.RegisterGeneratorControlServer(server, service)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	client := NewClient(conn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	if _, err := client.DistributeScript(ctx, Script{ID: "script-1", Content: []byte(`{"name":"demo"}`), ContentHash: "hash-1"}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	if _, err := client.AssignWork(ctx, Assignment{TestID: "test-1", GroupName: "Group A", ScriptID: "script-1", VUserCount: 10}); err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if commit, err := client.CommitWork(ctx, "test-1", 0); err != nil || !commit.Accepted {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}

	metrics, err := client.OpenMetricsStream(ctx)
	if err != nil {
		t.Fatalf("OpenMetricsStream: %v", err)
	}
	if _, err := metrics.Receive(); err != nil {
		t.Fatalf("Receive initial metrics batch: %v", err)
	}

	err = service.PublishMetrics("test-1", "Group A", GeneratorStats{ActiveVUsers: 10, CPUPercent: 42.5, MemoryBytes: 64 * 1024 * 1024, GoroutineCount: 19}, []TransactionMetric{{
		Name:          "Home",
		DurationMs:    120,
		StatusCode:    200,
		Success:       true,
		BytesSent:     128,
		BytesReceived: 1024,
		SOAPOperation: "GetHome",
	}, {
		Name:            "Checkout",
		StartTimeUnixMs: 1700000000123,
		DurationMs:      340,
		StatusCode:      500,
		Success:         false,
		ErrorMessage:    "remote failure",
		BytesSent:       256,
		BytesReceived:   512,
		SOAPOperation:   "CreateOrder",
		Snapshot:        &protocol.ErrorSnapshot{RequestMethod: "POST", RequestURL: "https://example.test/checkout", RequestBody: `{"token":"[REDACTED]"}`, ResponseStatus: 500, ResponseBody: `{"error":"failed"}`},
	}}, MetricsTelemetry{DroppedResults: 3, CollectedAtUnixMs: 1000, PublishedAtUnixMs: 1300, SampleAgeMs: 300})
	if err != nil {
		t.Fatalf("PublishMetrics: %v", err)
	}

	batch, err := metrics.Receive()
	if err != nil {
		t.Fatalf("Receive metrics snapshot batch: %v", err)
	}
	if batch.Stats.ActiveVUsers != 10 {
		t.Fatalf("snapshot active vusers = %d, want 10", batch.Stats.ActiveVUsers)
	}
	if batch.Stats.CPUPercent != 42.5 {
		t.Fatalf("snapshot cpu percent = %v, want 42.5", batch.Stats.CPUPercent)
	}
	if batch.Stats.MemoryBytes != 64*1024*1024 {
		t.Fatalf("snapshot memory bytes = %d, want %d", batch.Stats.MemoryBytes, 64*1024*1024)
	}
	if len(batch.Transactions) != 2 {
		t.Fatalf("snapshot transaction count = %d, want 2", len(batch.Transactions))
	}
	if batch.Transactions[1].ErrorMessage != "remote failure" {
		t.Fatalf("snapshot transaction error = %q, want remote failure", batch.Transactions[1].ErrorMessage)
	}
	if batch.Transactions[1].SOAPOperation != "CreateOrder" {
		t.Fatalf("snapshot transaction soap operation = %q, want CreateOrder", batch.Transactions[1].SOAPOperation)
	}
	if batch.Transactions[1].Group != "Group A" {
		t.Fatalf("snapshot transaction group = %q, want Group A", batch.Transactions[1].Group)
	}
	if batch.Transactions[1].StartTimeUnixMs != 1700000000123 {
		t.Fatalf("snapshot transaction start time = %d, want 1700000000123", batch.Transactions[1].StartTimeUnixMs)
	}
	if batch.Transactions[1].Snapshot == nil || batch.Transactions[1].Snapshot.RequestURL != "https://example.test/checkout" {
		t.Fatalf("snapshot transaction evidence = %#v, want checkout snapshot", batch.Transactions[1].Snapshot)
	}
	if batch.Transactions[0].Snapshot != nil {
		t.Fatalf("success transaction snapshot = %#v, want nil", batch.Transactions[0].Snapshot)
	}
	if batch.Telemetry == nil {
		t.Fatal("expected telemetry on metrics batch")
	}
	if batch.Telemetry.DroppedResults != 3 || batch.Telemetry.CollectedAtUnixMs != 1000 || batch.Telemetry.PublishedAtUnixMs != 1300 || batch.Telemetry.SampleAgeMs != 300 {
		t.Fatalf("unexpected metrics telemetry: %#v", batch.Telemetry)
	}

	if err := metrics.CloseSend(); err != nil {
		t.Fatalf("Metrics CloseSend: %v", err)
	}
}

func TestServiceWithExecutionRunsAssignedScript(t *testing.T) {
	const bufSize = 1024 * 1024

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := newIsolatedService(t, 50, WithExecution())
	pb.RegisterGeneratorControlServer(server, service)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	client := NewClient(conn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	scriptBytes, err := json.Marshal(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: target.URL,
			RunLogic: dsl.RunLogicConfig{
				Iterations: 0,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("Marshal script: %v", err)
	}

	if _, err := client.DistributeScript(ctx, Script{ID: "script-1", Content: scriptBytes, ContentHash: "hash-1"}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	if _, err := client.AssignWork(ctx, Assignment{TestID: "test-1", GroupName: "Group A", TargetDurationSeconds: 2, ScriptID: "script-1", VUserCount: 1}); err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if commit, err := client.CommitWork(ctx, "test-1", 0); err != nil || !commit.Accepted {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}

	metrics, err := client.OpenMetricsStream(ctx)
	if err != nil {
		t.Fatalf("OpenMetricsStream: %v", err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		batch, err := metrics.Receive()
		if err != nil {
			t.Fatalf("Receive metrics batch: %v", err)
		}
		if len(batch.Transactions) > 0 {
			if batch.Transactions[0].Name != "Checkout" {
				t.Fatalf("transaction name = %q, want Checkout script iteration", batch.Transactions[0].Name)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected assigned execution to emit transaction metrics")
		}
	}
	if err := metrics.CloseSend(); err != nil {
		t.Fatalf("Metrics CloseSend: %v", err)
	}
}

func TestServiceWithExecutionExposesArchiveArtifacts(t *testing.T) {
	const bufSize = 1024 * 1024

	t.Setenv(envGeneratorArchiveMinFree, "1")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := newIsolatedService(t, 50, WithExecution())
	pb.RegisterGeneratorControlServer(server, service)

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}

	client := NewClient(conn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	scriptBytes, err := json.Marshal(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 0}},
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})
	if err != nil {
		t.Fatalf("Marshal script: %v", err)
	}

	if _, err := client.DistributeScript(ctx, Script{ID: "script-1", Content: scriptBytes, ContentHash: "hash-1"}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	if _, err := client.AssignWork(ctx, Assignment{TestID: "test-archive", GroupName: "Group A", TargetDurationSeconds: 2, ScriptID: "script-1", VUserCount: 1}); err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if commit, err := client.CommitWork(ctx, "test-archive", 0); err != nil || !commit.Accepted {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}

	metrics, err := client.OpenMetricsStream(ctx)
	if err != nil {
		t.Fatalf("OpenMetricsStream: %v", err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		batch, err := metrics.Receive()
		if err != nil {
			t.Fatalf("Receive metrics batch: %v", err)
		}
		if batch.LifecycleState == "COMPLETE" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected assigned execution to complete")
		}
	}

	manifestContent, err := client.GetArchiveArtifact(ctx, ArchiveArtifactRequest{TestID: "test-archive", Name: "manifest.json"})
	if err != nil {
		t.Fatalf("GetArchiveArtifact manifest: %v", err)
	}
	if len(manifestContent) == 0 {
		t.Fatal("expected manifest content to be populated")
	}
	txContent, err := client.GetArchiveArtifact(ctx, ArchiveArtifactRequest{TestID: "test-archive", Name: "transactions.jsonl"})
	if err != nil {
		t.Fatalf("GetArchiveArtifact transactions: %v", err)
	}
	if len(txContent) == 0 {
		t.Fatal("expected transaction archive content to be populated")
	}
	if err := metrics.CloseSend(); err != nil {
		t.Fatalf("Metrics CloseSend: %v", err)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
