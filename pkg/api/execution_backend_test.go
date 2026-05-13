package api

import (
	"context"
	"net"
	"reflect"
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
		if started.Status == "RUNNING" && started.StartedAt != nil && started.LiveMetrics != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark test running with live metrics, got status %q metrics=%#v", started.Status, started.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	started, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist", testRun.ID)
	}
	if started.LiveMetrics.ActiveVUsers != 5 {
		t.Fatalf("active vusers = %d, want 5", started.LiveMetrics.ActiveVUsers)
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

func TestDistributedExecutionBackendAdjustsDuration(t *testing.T) {
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

	if err := runner.AdjustDuration(testRun.ID, 45*time.Second); err != nil {
		t.Fatalf("AdjustDuration returned error: %v", err)
	}

	adjusted, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after duration change", testRun.ID)
	}
	if adjusted.Duration != 45*time.Second {
		t.Fatalf("duration = %v, want 45s", adjusted.Duration)
	}
	if adjusted.DurationStr != "45s" {
		t.Fatalf("duration string = %q, want 45s", adjusted.DurationStr)
	}
}

func TestDistributedExecutionBackendPausesAndResumesTest(t *testing.T) {
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
		Name:     "Distributed pause resume",
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
		if started.Status == "RUNNING" && started.LiveMetrics != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark test running with live metrics, got status %q metrics=%#v", started.Status, started.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	started, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist", testRun.ID)
	}
	if started.LiveMetrics.ActiveVUsers != 5 {
		t.Fatalf("active vusers before pause = %d, want 5", started.LiveMetrics.ActiveVUsers)
	}

	if err := runner.PauseTest(testRun.ID); err != nil {
		t.Fatalf("PauseTest returned error: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		paused, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after pause", testRun.ID)
		}
		if paused.Status == "PAUSED" && paused.LiveMetrics != nil && paused.LiveMetrics.ActiveVUsers == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected paused status and zero live vusers, got status=%q metrics=%#v", paused.Status, paused.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	paused, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after pause", testRun.ID)
	}
	if paused.Status != "PAUSED" {
		t.Fatalf("status after pause = %q, want PAUSED", paused.Status)
	}
	if paused.LiveMetrics == nil || paused.LiveMetrics.ActiveVUsers != 0 {
		t.Fatalf("paused live metrics = %#v, want active vusers 0", paused.LiveMetrics)
	}

	if err := runner.ResumeTest(testRun.ID); err != nil {
		t.Fatalf("ResumeTest returned error: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		resumed, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after resume", testRun.ID)
		}
		if resumed.Status == "RUNNING" && resumed.LiveMetrics != nil && resumed.LiveMetrics.ActiveVUsers == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected resumed status and restored live vusers, got status=%q metrics=%#v", resumed.Status, resumed.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	resumed, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after resume", testRun.ID)
	}
	if resumed.Status != "RUNNING" {
		t.Fatalf("status after resume = %q, want RUNNING", resumed.Status)
	}
	if resumed.LiveMetrics == nil || resumed.LiveMetrics.ActiveVUsers != 5 {
		t.Fatalf("resumed live metrics = %#v, want active vusers 5", resumed.LiveMetrics)
	}
}

func TestDistributedExecutionBackendScalesSingleGroupTest(t *testing.T) {
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
		Name:     "Distributed scale",
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

	if err := runner.ScaleVUsers(testRun.ID, "Group A", 7); err != nil {
		t.Fatalf("ScaleVUsers returned error: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		scaled, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
		}
		if scaled.LiveMetrics != nil && scaled.LiveMetrics.ActiveVUsers == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected live metrics to reflect scaled vusers, got %#v", scaled.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	scaled, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
	}
	if scaled.TotalVUsers != 7 {
		t.Fatalf("total vusers = %d, want 7", scaled.TotalVUsers)
	}
	if scaled.Groups[0].VUsers != 7 {
		t.Fatalf("group vusers = %d, want 7", scaled.Groups[0].VUsers)
	}
	if scaled.LiveMetrics == nil || scaled.LiveMetrics.ActiveVUsers != 7 {
		t.Fatalf("live metrics active vusers = %#v, want 7", scaled.LiveMetrics)
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

	stopAck, err := client.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	if stopAck.PendingVUsers != 7 {
		t.Fatalf("pending vusers = %d, want 7", stopAck.PendingVUsers)
	}
}

func TestDistributedExecutionBackendScalesMultiGroupTest(t *testing.T) {
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
		Name:     "Distributed scale multi-group",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "5s",
		}, {
			Name:     "Group B",
			ScriptID: "script-1",
			VUsers:   3,
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
		if started.Status == "RUNNING" && started.LiveMetrics != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark test running with live metrics, got status %q metrics=%#v", started.Status, started.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := runner.ScaleVUsers(testRun.ID, "Group A", 7); err != nil {
		t.Fatalf("ScaleVUsers returned error: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		scaled, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
		}
		if scaled.LiveMetrics != nil && scaled.LiveMetrics.ActiveVUsers == 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected live metrics to reflect multi-group scaled vusers, got %#v", scaled.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	scaled, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
	}
	if scaled.TotalVUsers != 10 {
		t.Fatalf("total vusers = %d, want 10", scaled.TotalVUsers)
	}
	if scaled.Groups[0].VUsers != 7 {
		t.Fatalf("group A vusers = %d, want 7", scaled.Groups[0].VUsers)
	}
	if scaled.Groups[1].VUsers != 3 {
		t.Fatalf("group B vusers = %d, want 3", scaled.Groups[1].VUsers)
	}
	if scaled.LiveMetrics == nil || scaled.LiveMetrics.ActiveVUsers != 10 {
		t.Fatalf("live metrics active vusers = %#v, want 10", scaled.LiveMetrics)
	}
	if scaled.LiveMetrics.GroupMetrics == nil {
		t.Fatalf("group live metrics = %#v, want populated group metrics", scaled.LiveMetrics)
	}
	if scaled.LiveMetrics.GroupMetrics["Group A"] == nil || scaled.LiveMetrics.GroupMetrics["Group A"].VUsers != 7 {
		t.Fatalf("group A live metrics = %#v, want vusers 7", scaled.LiveMetrics.GroupMetrics["Group A"])
	}
	if scaled.LiveMetrics.GroupMetrics["Group B"] == nil || scaled.LiveMetrics.GroupMetrics["Group B"].VUsers != 3 {
		t.Fatalf("group B live metrics = %#v, want vusers 3", scaled.LiveMetrics.GroupMetrics["Group B"])
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

	stopAck, err := client.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	if stopAck.PendingVUsers != 10 {
		t.Fatalf("pending vusers = %d, want 10", stopAck.PendingVUsers)
	}
}