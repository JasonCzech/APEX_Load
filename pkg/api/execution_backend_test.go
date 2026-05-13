package api

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/stormrunner/api/proto"
	"github.com/jasonczech/stormrunner/pkg/distributed/generatorrpc"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type executionBackendSpy struct {
	called chan string
}

func (s *executionBackendSpy) Run(_ *TestRunner, test *ManagedTest) {
	s.called <- test.ID
}

func TestStartTestDispatchesThroughExecutionBackend(t *testing.T) {
	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	spy := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(spy)

	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Remote-ready run",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "5s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	select {
	case startedID := <-spy.called:
		if startedID != testRun.ID {
			t.Fatalf("execution backend received test id %q, want %q", startedID, testRun.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected execution backend to be invoked")
	}
}

func TestDistributedExecutionBackendAssignsWorkToGenerator(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50))

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet"},
		5*time.Second,
		func(ctx context.Context, target string) (generatorClient, error) {
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listener.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		},
	))

	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed run",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "5s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		started, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if started.Status == "RUNNING" && started.StartedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark test running, got status %q", started.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("Dial generator client: %v", err)
	}
	defer client.Close()

	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatalf("OpenHeartbeat: %v", err)
	}
	if err := heartbeat.Send(generatorrpc.Heartbeat{GeneratorID: "generator-1", TimestampUnixMs: 42}); err != nil {
		t.Fatalf("Heartbeat Send: %v", err)
	}
	pong, err := heartbeat.Receive()
	if err != nil {
		t.Fatalf("Heartbeat Receive: %v", err)
	}
	if !reflect.DeepEqual(pong.ActiveTestIDs, []string{testRun.ID}) {
		t.Fatalf("active test ids = %v, want [%s]", pong.ActiveTestIDs, testRun.ID)
	}
	if err := heartbeat.CloseSend(); err != nil {
		t.Fatalf("Heartbeat CloseSend: %v", err)
	}
}

func TestDistributedExecutionBackendStopTestClearsRemoteAssignment(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50))

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet"},
		5*time.Second,
		func(ctx context.Context, target string) (generatorClient, error) {
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listener.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		},
	))

	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed run",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "5s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		started, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if started.Status == "RUNNING" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark test running, got status %q", started.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := runner.StopTest(testRun.ID); err != nil {
		t.Fatalf("StopTest returned error: %v", err)
	}
	stopped, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after stop", testRun.ID)
	}
	if stopped.Status != "STOPPING" {
		t.Fatalf("status = %q, want STOPPING", stopped.Status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(
		ctx,
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("Dial generator client: %v", err)
	}
	defer client.Close()

	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatalf("OpenHeartbeat: %v", err)
	}
	if err := heartbeat.Send(generatorrpc.Heartbeat{GeneratorID: "generator-1", TimestampUnixMs: 43}); err != nil {
		t.Fatalf("Heartbeat Send: %v", err)
	}
	pong, err := heartbeat.Receive()
	if err != nil {
		t.Fatalf("Heartbeat Receive: %v", err)
	}
	if len(pong.ActiveTestIDs) != 0 {
		t.Fatalf("active test ids after runner stop = %v, want empty", pong.ActiveTestIDs)
	}
	if err := heartbeat.CloseSend(); err != nil {
		t.Fatalf("Heartbeat CloseSend: %v", err)
	}
}

func TestNewServerConfiguresDistributedExecutionBackend(t *testing.T) {
	server := NewServer(ServerConfig{
		Addr:             ":0",
		MaxConcurrent:    1,
		GeneratorTargets: []string{"127.0.0.1:9090"},
		GeneratorTimeout: 3 * time.Second,
	})

	backend, ok := server.runner.execution.(*distributedExecutionBackend)
	if !ok {
		t.Fatalf("runner execution backend = %T, want *distributedExecutionBackend", server.runner.execution)
	}
	if !reflect.DeepEqual(backend.targets, []string{"127.0.0.1:9090"}) {
		t.Fatalf("backend targets = %v, want [127.0.0.1:9090]", backend.targets)
	}
	if backend.timeout != 3*time.Second {
		t.Fatalf("backend timeout = %v, want 3s", backend.timeout)
	}
}

func TestDistributedExecutionBackendReportsUnsupportedLifecycleControls(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50))

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet"},
		5*time.Second,
		func(ctx context.Context, target string) (generatorClient, error) {
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listener.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		},
	))

	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			RunLogic: dsl.RunLogicConfig{
				Iterations: 1,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed control gaps",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "5s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		started, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if started.Status == "RUNNING" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark test running, got status %q", started.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := runner.ScaleVUsers(testRun.ID, "Group A", 7); err == nil || !strings.Contains(err.Error(), "does not support scaling vusers yet") {
		t.Fatalf("ScaleVUsers error = %v, want distributed unsupported scaling error", err)
	}
	if err := runner.AdjustDuration(testRun.ID, 45*time.Second); err == nil || !strings.Contains(err.Error(), "does not support duration changes yet") {
		t.Fatalf("AdjustDuration error = %v, want distributed unsupported duration error", err)
	}
	if err := runner.PauseTest(testRun.ID); err == nil || !strings.Contains(err.Error(), "does not support pause yet") {
		t.Fatalf("PauseTest error = %v, want distributed unsupported pause error", err)
	}

	runner.mu.Lock()
	runner.tests[testRun.ID].Status = "PAUSED"
	runner.mu.Unlock()

	if err := runner.ResumeTest(testRun.ID); err == nil || !strings.Contains(err.Error(), "does not support resume yet") {
		t.Fatalf("ResumeTest error = %v, want distributed unsupported resume error", err)
	}
}