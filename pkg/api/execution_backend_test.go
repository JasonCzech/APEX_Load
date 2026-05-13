package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pb "github.com/jasonczech/stormrunner/api/proto"
	"github.com/jasonczech/stormrunner/pkg/distributed/generatorrpc"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/http"
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
	deadline = time.Now().Add(2 * time.Second)
	for {
		stopped, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after stop", testRun.ID)
		}
		if stopped.Status == "COMPLETE" && stopped.EndedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed stop to complete test, got status=%q ended=%v", stopped.Status, stopped.EndedAt)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stopped, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after stop", testRun.ID)
	}
	if stopped.Status != "COMPLETE" {
		t.Fatalf("status = %q, want COMPLETE", stopped.Status)
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

func TestDistributedExecutionBackendCompletesAfterDurationExpiry(t *testing.T) {
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
		Name:     "Distributed timed completion",
		Duration: "1s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "1s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		completed, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if completed.Status == "COMPLETE" && completed.EndedAt != nil && completed.LiveMetrics != nil && completed.LiveMetrics.ActiveVUsers == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed test to auto-complete, got status=%q ended=%v metrics=%#v", completed.Status, completed.EndedAt, completed.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDistributedExecutionBackendPublishesRemoteTransactionMetrics(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := generatorrpc.NewService("generator-1", 50)
	pb.RegisterGeneratorControlServer(server, service)

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
		Name:     "Distributed transaction metrics",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "1s",
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

	err = service.PublishMetrics(testRun.ID, "Group A", generatorrpc.GeneratorStats{ActiveVUsers: 5, CPUPercent: 33.3, MemoryBytes: 128 * 1024 * 1024, GoroutineCount: 21}, []generatorrpc.TransactionMetric{{
		Name:          "Home",
		DurationMs:    100,
		StatusCode:    200,
		Success:       true,
		BytesSent:     100,
		BytesReceived: 200,
	}, {
		Name:          "Home",
		DurationMs:    300,
		StatusCode:    500,
		Success:       false,
		ErrorMessage:  "remote failure",
		BytesSent:     120,
		BytesReceived: 80,
	}, {
		Name:          "Search",
		DurationMs:    200,
		StatusCode:    200,
		Success:       true,
		BytesSent:     90,
		BytesReceived: 180,
	}})
	if err != nil {
		t.Fatalf("PublishMetrics returned error: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		updated, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after metrics update", testRun.ID)
		}
		if updated.LiveMetrics != nil && updated.LiveMetrics.TotalTx == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed metrics snapshot to reach runner, got %#v", updated.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	updated, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after metrics update", testRun.ID)
	}
	if updated.LiveMetrics.TotalErrors != 1 {
		t.Fatalf("total errors = %d, want 1", updated.LiveMetrics.TotalErrors)
	}
	if updated.LiveMetrics.AvgResponseMs != 200 {
		t.Fatalf("avg response ms = %v, want 200", updated.LiveMetrics.AvgResponseMs)
	}
	if updated.LiveMetrics.P50Ms != 200 || updated.LiveMetrics.P95Ms != 200 {
		t.Fatalf("unexpected distributed percentiles: p50=%v p95=%v", updated.LiveMetrics.P50Ms, updated.LiveMetrics.P95Ms)
	}
	if updated.LiveMetrics.Resources == nil || updated.LiveMetrics.Resources.CPUProcessPct != 33.3 {
		t.Fatalf("expected cpu process pct 33.3, got %#v", updated.LiveMetrics.Resources)
	}
	if updated.LiveMetrics.Transactions["Home"] == nil || updated.LiveMetrics.Transactions["Home"].Count != 2 || updated.LiveMetrics.Transactions["Home"].Errors != 1 {
		t.Fatalf("unexpected Home transaction metrics: %#v", updated.LiveMetrics.Transactions["Home"])
	}
}

func TestDistributedExecutionBackendRunsRemoteEngine(t *testing.T) {
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
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50, generatorrpc.WithExecution()))

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
			BaseURL: target.URL,
			RunLogic: dsl.RunLogicConfig{
				Iterations: 0,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed actual execution",
		Duration: "2s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   1,
			RampUp:   "0s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		updated, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if updated.LiveMetrics != nil && updated.LiveMetrics.TotalTx > 0 && updated.LiveMetrics.Transactions["Home"] != nil {
			if updated.LiveMetrics.TotalErrors != 0 {
				t.Fatalf("total errors = %d, want 0", updated.LiveMetrics.TotalErrors)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected remote engine execution to publish live metrics, got %#v", updated.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDistributedExecutionBackendRunsRemoteMultiGroupEngine(t *testing.T) {
	const bufSize = 1024 * 1024

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/buyers", "/admins":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer target.Close()

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50, generatorrpc.WithExecution()))

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
		ID:       "buyers-script",
		Name:     "Buyers",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: target.URL,
			RunLogic: dsl.RunLogicConfig{
				Iterations: 0,
			},
		},
		Actions: []dsl.Action{{Name: "Buyers Home", Type: "GET", Target: "/buyers"}},
	})
	runner.StoreScript(&dsl.Script{
		ID:       "admins-script",
		Name:     "Admins",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: target.URL,
			RunLogic: dsl.RunLogicConfig{
				Iterations: 0,
			},
		},
		Actions: []dsl.Action{{Name: "Admins Home", Type: "GET", Target: "/admins"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed actual multi-group execution",
		Duration: "2s",
		Groups: []CreateGroupEntry{{
			Name:     "buyers",
			ScriptID: "buyers-script",
			VUsers:   1,
			RampUp:   "0s",
		}, {
			Name:     "admins",
			ScriptID: "admins-script",
			VUsers:   1,
			RampUp:   "0s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		updated, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if updated.LiveMetrics != nil && updated.LiveMetrics.TotalTx > 0 && updated.LiveMetrics.Transactions["Buyers Home"] != nil && updated.LiveMetrics.Transactions["Admins Home"] != nil {
			if updated.LiveMetrics.GroupMetrics["buyers"] == nil || updated.LiveMetrics.GroupMetrics["admins"] == nil {
				t.Fatalf("expected group live metrics for both remote groups, got %#v", updated.LiveMetrics.GroupMetrics)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected remote multi-group execution to publish live metrics, got %#v", updated.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}

	deadline = time.Now().Add(6 * time.Second)
	for {
		completed, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after execution", testRun.ID)
		}
		if completed.Status == "COMPLETE" && completed.EndedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected remote multi-group execution to complete, got status=%q ended=%v", completed.Status, completed.EndedAt)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDistributedExecutionBackendScalesRunningRemoteEngine(t *testing.T) {
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
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50, generatorrpc.WithExecution()))

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
			BaseURL: target.URL,
			RunLogic: dsl.RunLogicConfig{
				Iterations: 0,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed actual scaling",
		Duration: "3s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   1,
			RampUp:   "0s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		started, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if started.Status == "RUNNING" && started.LiveMetrics != nil && started.LiveMetrics.TotalTx > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected remote engine to start before scaling, got %#v", started.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}

	if err := runner.ScaleVUsers(testRun.ID, "Group A", 2); err != nil {
		t.Fatalf("ScaleVUsers returned error: %v", err)
	}

	deadline = time.Now().Add(6 * time.Second)
	for {
		scaled, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
		}
		if scaled.LiveMetrics != nil && scaled.LiveMetrics.ActiveVUsers == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected live metrics to reflect scaled remote vusers, got %#v", scaled.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}

	scaled, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
	}
	if scaled.Groups[0].VUsers != 2 || scaled.TotalVUsers != 2 {
		t.Fatalf("expected scaled test config to settle at 2 vusers, got groups=%#v total=%d", scaled.Groups, scaled.TotalVUsers)
	}
}

func TestDistributedExecutionBackendPausesRealRemoteEngine(t *testing.T) {
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
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50, generatorrpc.WithExecution()))

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
			BaseURL: target.URL,
			RunLogic: dsl.RunLogicConfig{
				Iterations: 0,
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed actual pause",
		Duration: "3s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   1,
			RampUp:   "0s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		started, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if started.Status == "RUNNING" && started.LiveMetrics != nil && started.LiveMetrics.TotalTx > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected remote engine to start before pause, got %#v", started.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}

	if err := runner.PauseTest(testRun.ID); err != nil {
		t.Fatalf("PauseTest returned error: %v", err)
	}

	deadline = time.Now().Add(6 * time.Second)
	for {
		paused, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after pause", testRun.ID)
		}
		if paused.Status == "PAUSED" && paused.LiveMetrics != nil && paused.LiveMetrics.ActiveVUsers == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected live metrics to reflect paused remote engine, got status=%q metrics=%#v", paused.Status, paused.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}

	if err := runner.ResumeTest(testRun.ID); err != nil {
		t.Fatalf("ResumeTest returned error: %v", err)
	}

	deadline = time.Now().Add(6 * time.Second)
	for {
		resumed, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after resume", testRun.ID)
		}
		if resumed.Status == "RUNNING" && resumed.LiveMetrics != nil && resumed.LiveMetrics.ActiveVUsers == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected live metrics to reflect resumed remote engine, got status=%q metrics=%#v", resumed.Status, resumed.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDistributedExecutionBackendStagesRemoteArchiveOnCompletion(t *testing.T) {
	const bufSize = 1024 * 1024

	t.Setenv("STORMRUNNER_ARCHIVE_DIR", t.TempDir())
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
	pb.RegisterGeneratorControlServer(server, generatorrpc.NewService("generator-1", 50, generatorrpc.WithExecution()))

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
		Config:   dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 0}},
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed archive completion",
		Duration: "2s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   1,
			RampUp:   "0s",
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for {
		completed, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if completed.Status == "COMPLETE" && completed.EndedAt != nil && completed.Archive != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed run to complete with archive metadata, got status=%q archive=%#v", completed.Status, completed.Archive)
		}
		time.Sleep(25 * time.Millisecond)
	}

	completed, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after completion", testRun.ID)
	}
	if completed.Archive.TransactionCount == 0 || completed.Archive.IntervalSummaryCount == 0 {
		t.Fatalf("expected archive counts to be populated, got %#v", completed.Archive)
	}
	manifest, err := runner.GetRunArchiveManifest(testRun.ID)
	if err != nil {
		t.Fatalf("GetRunArchiveManifest returned error: %v", err)
	}
	if manifest.TransactionCount != completed.Archive.TransactionCount {
		t.Fatalf("expected manifest transaction count %d, got %d", completed.Archive.TransactionCount, manifest.TransactionCount)
	}
	for _, name := range []string{"manifest.json", "transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl", "transactions.parquet", "resources.parquet", "interval_summaries.parquet"} {
		if _, err := os.Stat(filepath.Join(completed.Archive.ArchivePath, name)); err != nil {
			t.Fatalf("expected staged remote archive file %s to exist: %v", name, err)
		}
	}
}

func TestDistributedExecutionBackendPropagatesRemoteFailure(t *testing.T) {
	const bufSize = 1024 * 1024

	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := generatorrpc.NewService("generator-1", 50)
	pb.RegisterGeneratorControlServer(server, service)

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
		Name:     "Distributed remote failure",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   5,
			RampUp:   "1s",
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

	service.FailTest(testRun.ID, "generator execution failed")

	deadline = time.Now().Add(2 * time.Second)
	for {
		failed, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after failure", testRun.ID)
		}
		if failed.Status == "FAILED" && failed.EndedAt != nil && failed.Error == "generator execution failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed test to fail from remote lifecycle, got status=%q ended=%v error=%q", failed.Status, failed.EndedAt, failed.Error)
		}
		time.Sleep(20 * time.Millisecond)
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
