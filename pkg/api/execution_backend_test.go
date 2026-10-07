package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type executionBackendSpy struct {
	called chan string
}

func TestLocalExecutionFinalStatePreservesExplicitStop(t *testing.T) {
	tests := []struct {
		name, status, testError, wantStatus, wantError string
		runErr                                         error
		gracefulStop                                   bool
	}{
		{name: "normal completion", status: "RUNNING", wantStatus: "COMPLETE"},
		{name: "graceful stop with partial result", status: "STOPPING", gracefulStop: true, wantStatus: "COMPLETE"},
		{name: "force abort with partial result", status: "STOPPING", wantStatus: "ABORTED"},
		{name: "typed timeout", status: "STOPPING", testError: "ci abort reason: timed_out", wantStatus: "ABORTED", wantError: "ci abort reason: timed_out"},
		{name: "leadership cancellation", status: "RUNNING", runErr: context.Canceled, wantStatus: "FAILED", wantError: context.Canceled.Error()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, message := localExecutionFinalState(test.status, test.testError, test.runErr, test.gracefulStop)
			if status != test.wantStatus || message != test.wantError {
				t.Fatalf("localExecutionFinalState = (%q, %q), want (%q, %q)", status, message, test.wantStatus, test.wantError)
			}
		})
	}
}

func TestLocalExecutionRunErrorRejectsPartialResultAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := localExecutionRunError(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("localExecutionRunError = %v, want context.Canceled", err)
	}
	sentinel := errors.New("engine failure")
	if err := localExecutionRunError(ctx, sentinel); !errors.Is(err, sentinel) {
		t.Fatalf("localExecutionRunError replaced engine error: %v", err)
	}
}

func (s *executionBackendSpy) Run(_ *TestRunner, test *ManagedTest) {
	s.called <- test.ID
}

func waitForDistributedLoadMetrics(t *testing.T, runner *TestRunner, testID string, wantActive int64) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		now := time.Now().UTC()
		runner.mu.RLock()
		test := runner.tests[testID]
		ready := test != nil && runner.testLoadReadyLocked(test, now)
		active := int64(-1)
		status := "missing"
		detail := ""
		if test != nil {
			status = test.Status
			detail = test.Error
			if test.LiveMetrics != nil {
				active = test.LiveMetrics.ActiveVUsers
			}
		}
		runner.mu.RUnlock()
		if ready && active == wantActive {
			return
		}
		if test != nil && isTerminalTestStatus(status) {
			t.Fatalf("distributed run became terminal before load readiness: status=%s error=%s", status, detail)
		}
		if time.Now().After(deadline) {
			t.Fatalf("distributed load did not become ready: status=%s active=%d want=%d", status, active, wantActive)
		}
		time.Sleep(10 * time.Millisecond)
	}
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

func TestStartTestRequiresConfiguredStaticSecretsWithoutInvalidatingSavedScript(t *testing.T) {
	const (
		projectID  = "project-start-secret-preflight"
		secretName = "API_TOKEN"
	)
	runner := NewTestRunner(1, NewHub(), nil)
	spy := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(spy)
	runner.StoreScript(&dsl.Script{
		ID:        "script-start-secret-preflight",
		ProjectID: projectID,
		Name:      "Deferred credential script",
		Protocol:  "http",
		Version:   "1.0.0",
		Actions: []dsl.Action{{
			Name:    "request",
			Type:    "GET",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	})
	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:      "Deferred credential run",
		ProjectID: projectID,
		Duration:  "30s",
		ScriptID:  "script-start-secret-preflight",
		VUsers:    1,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = runner.StartTest(testRun.ID)
	if err == nil || !strings.Contains(err.Error(), secretName) || !strings.Contains(err.Error(), "configure") {
		t.Fatalf("StartTest missing-secret error = %v", err)
	}
	if current, ok := runner.GetTest(testRun.ID); !ok || current.Status != "QUEUED" {
		t.Fatalf("failed start changed saved run: %#v", current)
	}
	if stored, ok := runner.GetScript("script-start-secret-preflight"); !ok || stored.Script.Actions[0].Headers["Authorization"] != "Bearer {{secret.API_TOKEN}}" {
		t.Fatalf("failed start changed saved script: %#v", stored)
	}
	select {
	case id := <-spy.called:
		t.Fatalf("backend launched missing-secret test %q", id)
	default:
	}

	t.Setenv(engine.ProjectSecretEnvironmentName(projectID, secretName), "configured-at-launch")
	if err := runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest with configured secret: %v", err)
	}
	select {
	case id := <-spy.called:
		if id != testRun.ID {
			t.Fatalf("backend received test %q, want %q", id, testRun.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("configured secret did not permit backend launch")
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
		400*time.Millisecond,
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
	waitForDistributedLoadMetrics(t, runner, testRun.ID, 5)

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

func TestPlanDistributedGroupTargetsUsesRegionWeights(t *testing.T) {
	groups := []TestGroup{
		{Name: "buyers-east-1", VUsers: 10},
		{Name: "buyers-east-2", VUsers: 10},
		{Name: "buyers-west", VUsers: 10},
	}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-a": {Target: "gen-a", AvailableCapacity: 50},
		"gen-b": {Target: "gen-b", AvailableCapacity: 50},
		"gen-c": {Target: "gen-c", AvailableCapacity: 50},
	}
	assignment := &GeneratorAssignmentSettings{
		Mode: generatorAssignmentModeRegionWeighted,
		RegionWeights: map[string]int{
			"us-east": 2,
			"eu-west": 1,
		},
	}
	targetSpecs := []generatorTargetSpec{
		{Address: "gen-a", Region: "us-east"},
		{Address: "gen-b", Region: "eu-west"},
		{Address: "gen-c", Region: "us-east"},
	}

	assignments := planDistributedGroupTargets(groups, loadByTarget, []string{"gen-a", "gen-b", "gen-c"}, assignment, targetSpecs)

	assignedByRegion := map[string]int{}
	regionByTarget := map[string]string{
		"gen-a": "us-east",
		"gen-b": "eu-west",
		"gen-c": "us-east",
	}
	for _, target := range assignments {
		assignedByRegion[regionByTarget[target]]++
	}
	if assignedByRegion["us-east"] != 2 {
		t.Fatalf("us-east assignments = %d, want 2 (%v)", assignedByRegion["us-east"], assignments)
	}
	if assignedByRegion["eu-west"] != 1 {
		t.Fatalf("eu-west assignments = %d, want 1 (%v)", assignedByRegion["eu-west"], assignments)
	}
}

func TestPlanDistributedGroupTargetsUsesLoadZoneWeights(t *testing.T) {
	groups := []TestGroup{
		{Name: "buyers", VUsers: 10, Protocol: "http"},
		{Name: "browsers", VUsers: 10, Protocol: "http"},
	}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-a": {Target: "gen-a", AvailableCapacity: 50, SupportedProtocols: []string{"http"}},
		"gen-b": {Target: "gen-b", AvailableCapacity: 50, SupportedProtocols: []string{"http"}},
	}
	assignment := &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeLoadZoneWeighted,
		LoadZoneWeights: map[string]int{"us-east": 100},
	}
	targetSpecs := []generatorTargetSpec{
		{Address: "gen-a", Region: "us-east", Protocols: []string{"http"}},
		{Address: "gen-b", Region: "eu-west", Protocols: []string{"http"}},
	}

	assignments := planDistributedGroupTargets(groups, loadByTarget, []string{"gen-a", "gen-b"}, assignment, targetSpecs)
	if assignments["buyers"] != "gen-a" || assignments["browsers"] != "gen-a" {
		t.Fatalf("assignments = %#v, want load-zone weighted placement in us-east", assignments)
	}
}

func TestPlanDistributedGroupTargetsUsesNegotiatedGeneratorProtocols(t *testing.T) {
	groups := []TestGroup{{Name: "grpc-callers", Protocol: "grpc", VUsers: 5}}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-a": {Target: "gen-a", AvailableCapacity: 50, SupportedProtocols: []string{"http"}},
		"gen-b": {Target: "gen-b", AvailableCapacity: 20, SupportedProtocols: []string{"grpc"}},
	}
	targetSpecs := []generatorTargetSpec{
		{Address: "gen-a", Protocols: []string{"http", "grpc"}},
		{Address: "gen-b", Protocols: []string{"http"}},
	}

	assignments := planDistributedGroupTargets(groups, loadByTarget, []string{"gen-a", "gen-b"}, nil, targetSpecs)

	if assignments["grpc-callers"] != "gen-b" {
		t.Fatalf("grpc group assigned to %q, want negotiated grpc-capable gen-b", assignments["grpc-callers"])
	}
}

func TestFreshEmptyGeneratorCapabilitiesOverrideConfiguredDefaults(t *testing.T) {
	spec := generatorTargetSpec{
		Address:   "gen-jvm",
		Protocols: []string{"java_vuser"},
		Features:  []string{protocol.FeatureJavaVuserPhaseLifecycleV1},
		Labels:    map[string]string{"runtime": "java_vuser"},
	}
	if !targetSupportsProtocol(spec, "java_vuser") {
		t.Fatal("configured protocol should remain a provisional hint before a heartbeat")
	}

	effective := generatorEffectiveSpec(spec, generatorLoadSnapshot{HeartbeatOK: true})
	if got := generatorSpecProtocols(effective); len(got) != 0 {
		t.Fatalf("effective protocols = %#v, want authoritative empty heartbeat advertisement", got)
	}
	if targetSupportsProtocol(effective, "java_vuser") {
		t.Fatal("fresh empty protocol advertisement was replaced by configured/controller defaults")
	}
	if protocol.RuntimeFeaturesSatisfy(effective.Features, protocol.FeatureJavaVuserPhaseLifecycleV1) {
		t.Fatal("fresh empty feature advertisement was replaced by configured features")
	}

	index := newGeneratorCompatibilityIndex(
		[]generatorTargetSpec{spec},
		map[string]generatorLoadSnapshot{"gen-jvm": {HeartbeatOK: true}},
	)
	if index.targetCanRun("gen-jvm", "java_vuser", protocol.FeatureJavaVuserPhaseLifecycleV1, nil) {
		t.Fatal("compatibility index accepted labels/defaults after an authoritative empty heartbeat")
	}
}

func TestPlanDistributedGroupShardsSplitsSpecificHostsByCapacity(t *testing.T) {
	groups := []TestGroup{{
		Name:     "buyers",
		Protocol: "http",
		VUsers:   10,
		Assignment: &GeneratorAssignmentSettings{
			Mode:            generatorAssignmentModeSpecificHosts,
			SelectedTargets: []string{"gen-a", "gen-b"},
			ShardStrategy:   generatorShardStrategyCapacityWeighted,
		},
	}}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-a": {Target: "gen-a", AvailableCapacity: 90, SupportedProtocols: []string{"http"}},
		"gen-b": {Target: "gen-b", AvailableCapacity: 10, SupportedProtocols: []string{"http"}},
	}

	shards := planDistributedGroupShards(groups, loadByTarget, []string{"gen-a", "gen-b"}, nil, nil)

	if len(shards["buyers"]) != 2 {
		t.Fatalf("buyer shards = %#v, want two host shards", shards["buyers"])
	}
	got := map[string]int{}
	for _, shard := range shards["buyers"] {
		got[shard.Target] = shard.VUsers
		if shard.Group != "buyers" || shard.ShardStrategy != generatorShardStrategyCapacityWeighted {
			t.Fatalf("unexpected shard metadata: %#v", shard)
		}
	}
	if got["gen-a"] != 8 || got["gen-b"] != 2 {
		t.Fatalf("shard vusers = %#v, want gen-a=8 gen-b=2", got)
	}
}

func TestPlanDistributedGroupShardsRejectsIncompatibleSpecificHost(t *testing.T) {
	groups := []TestGroup{{
		Name:     "grpc-callers",
		Protocol: "grpc",
		VUsers:   5,
		Assignment: &GeneratorAssignmentSettings{
			Mode:            generatorAssignmentModeSpecificHosts,
			SelectedTargets: []string{"gen-a"},
		},
	}}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-a": {Target: "gen-a", AvailableCapacity: 50, SupportedProtocols: []string{"http"}},
	}

	shards := planDistributedGroupShards(groups, loadByTarget, []string{"gen-a"}, nil, []generatorTargetSpec{{Address: "gen-a", Protocols: []string{"http"}}})

	if len(shards["grpc-callers"]) != 0 {
		t.Fatalf("grpc-callers shards = %#v, want no compatible placement", shards["grpc-callers"])
	}
}

func TestAggregateDistributedMetricsBatchesSumsDuplicateGroupMetrics(t *testing.T) {
	batch, _, _ := aggregateDistributedMetricsBatches("test-1", map[string]generatorrpc.MetricsBatch{
		"gen-a": {
			TestID: "test-1",
			Stats:  generatorrpc.GeneratorStats{ActiveVUsers: 8},
			Groups: []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 8}},
		},
		"gen-b": {
			TestID: "test-1",
			Stats:  generatorrpc.GeneratorStats{ActiveVUsers: 2},
			Groups: []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 2}},
		},
	})

	if batch.Stats.ActiveVUsers != 10 {
		t.Fatalf("active vusers = %d, want 10", batch.Stats.ActiveVUsers)
	}
	if len(batch.Groups) != 1 || batch.Groups[0].Name != "buyers" || batch.Groups[0].ActiveVUsers != 10 {
		t.Fatalf("group metrics = %#v, want one aggregated buyers group with 10 vusers", batch.Groups)
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
		2*time.Second,
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
	waitForDistributedLoadMetrics(t, runner, testRun.ID, 5)
	runner.mu.RLock()
	runningTest := runner.tests[testRun.ID]
	if runningTest == nil || runningTest.executionCtx == nil {
		runner.mu.RUnlock()
		t.Fatalf("expected distributed execution context for %q", testRun.ID)
	}
	executionDone := runningTest.executionCtx.Done()
	runner.mu.RUnlock()

	if err := runner.StopTest(testRun.ID); err != nil {
		t.Fatalf("StopTest returned error: %v", err)
	}
	select {
	case <-executionDone:
		// The terminal metrics stream must remain independently live after this
		// cancellation so the controller can distinguish ABORTED from generator
		// loss below.
	default:
		t.Fatal("distributed execution context remained live after graceful stop")
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		stopped, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after stop", testRun.ID)
		}
		if stopped.Status == "ABORTED" && stopped.EndedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed stop to complete test, got status=%q ended=%v error=%q", stopped.Status, stopped.EndedAt, stopped.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stopped, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after stop", testRun.ID)
	}
	if stopped.Status != "ABORTED" {
		t.Fatalf("status = %q, want ABORTED", stopped.Status)
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

	// Match the distributed backend's five-second dial/stream budget. The API
	// package has many integration tests, and a two-second eventual assertion can
	// expire under full-suite scheduler load even though the stream is healthy.
	deadline := time.Now().Add(5 * time.Second)
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
		Name:             "Home",
		DurationMs:       100,
		StatusCode:       200,
		Success:          true,
		BytesSent:        100,
		BytesReceived:    200,
		CorrelationID:    "corr-home-1",
		DependencyType:   "http",
		DependencyTarget: "api.example.test",
		DNSMs:            9,
		ConnectMs:        14,
		TLSMs:            6,
		TTFBMs:           74,
		ServerTelemetry: &generatorrpc.ServerTelemetry{
			ServerProcessingMs: 40,
			TransferMs:         20,
			RequestPayloadKB:   1.5,
			RequestSizeClass:   "small",
			ResponsePayloadKB:  3,
			ResponseSizeClass:  "medium",
			MessageLatencyMs:   35,
			MessagesSent:       1,
			MessagesReceived:   0,
			MessagesSentPerSec: 10,
			ConsumerLagMs:      30,
			ConnectionReused:   false,
		},
	}, {
		Name:             "Home",
		DurationMs:       300,
		StatusCode:       500,
		Success:          false,
		ErrorMessage:     "remote failure",
		BytesSent:        120,
		BytesReceived:    80,
		CorrelationID:    "corr-home-2",
		DependencyType:   "http",
		DependencyTarget: "api.example.test",
		DNSMs:            3,
		ConnectMs:        6,
		TLSMs:            0,
		TTFBMs:           60,
		ServerTelemetry: &generatorrpc.ServerTelemetry{
			ServerProcessingMs:     60,
			TransferMs:             40,
			RequestPayloadKB:       2.5,
			RequestSizeClass:       "medium",
			ResponsePayloadKB:      5,
			ResponseSizeClass:      "large",
			MessageLatencyMs:       55,
			MessagesSent:           1,
			MessagesReceived:       1,
			MessagesSentPerSec:     5,
			MessagesReceivedPerSec: 5,
			ConsumerLagMs:          60,
			ConnectionReused:       true,
		},
	}, {
		Name:             "Search",
		DurationMs:       200,
		StatusCode:       200,
		Success:          true,
		BytesSent:        90,
		BytesReceived:    180,
		CorrelationID:    "corr-search-1",
		DependencyType:   "http",
		DependencyTarget: "search.example.test",
	}}, generatorrpc.MetricsTelemetry{DroppedResults: 4, CollectedAtUnixMs: 900, PublishedAtUnixMs: 1500, SampleAgeMs: 600})
	if err != nil {
		t.Fatalf("PublishMetrics returned error: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for {
		updated, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after metrics update", testRun.ID)
		}
		if updated.LiveMetrics != nil && updated.LiveMetrics.TotalTx == 2 {
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
	if updated.LiveMetrics.P50Ms != 200 || updated.LiveMetrics.P95Ms != 290 {
		t.Fatalf("unexpected distributed percentiles: p50=%v p95=%v", updated.LiveMetrics.P50Ms, updated.LiveMetrics.P95Ms)
	}
	if updated.LiveMetrics.Telemetry == nil {
		t.Fatal("expected distributed telemetry to be populated")
	}
	if updated.LiveMetrics.Telemetry.DroppedResults != 4 {
		t.Fatalf("dropped results = %d, want 4", updated.LiveMetrics.Telemetry.DroppedResults)
	}
	if !updated.LiveMetrics.Telemetry.CollectedAt.Equal(time.UnixMilli(900)) {
		t.Fatalf("collected_at = %s, want %s", updated.LiveMetrics.Telemetry.CollectedAt, time.UnixMilli(900))
	}
	if !updated.LiveMetrics.Telemetry.GeneratorPublishedAt.Equal(time.UnixMilli(1500)) {
		t.Fatalf("generator_published_at = %s, want %s", updated.LiveMetrics.Telemetry.GeneratorPublishedAt, time.UnixMilli(1500))
	}
	if updated.LiveMetrics.Telemetry.ControllerReceivedAt.IsZero() {
		t.Fatal("expected controller_received_at to be populated")
	}
	if updated.LiveMetrics.Telemetry.PublishedAt.Before(updated.LiveMetrics.Telemetry.CollectedAt) {
		t.Fatalf("published_at = %s, want >= collected_at %s", updated.LiveMetrics.Telemetry.PublishedAt, updated.LiveMetrics.Telemetry.CollectedAt)
	}
	if updated.LiveMetrics.Telemetry.GeneratorProcessingLagMs != 600 {
		t.Fatalf("generator_processing_lag_ms = %v, want 600", updated.LiveMetrics.Telemetry.GeneratorProcessingLagMs)
	}
	if updated.LiveMetrics.Telemetry.TransportLagMs < 0 {
		t.Fatalf("transport_lag_ms = %v, want non-negative", updated.LiveMetrics.Telemetry.TransportLagMs)
	}
	if updated.LiveMetrics.Telemetry.ControllerFanoutLagMs < 0 {
		t.Fatalf("controller_fanout_lag_ms = %v, want non-negative", updated.LiveMetrics.Telemetry.ControllerFanoutLagMs)
	}
	if updated.LiveMetrics.Telemetry.SampleAgeMs < 0 {
		t.Fatalf("sample_age_ms = %v, want non-negative", updated.LiveMetrics.Telemetry.SampleAgeMs)
	}
	if updated.LiveMetrics.Resources == nil || updated.LiveMetrics.Resources.CPUProcessPct != 33.3 {
		t.Fatalf("expected cpu process pct 33.3, got %#v", updated.LiveMetrics.Resources)
	}
	if updated.LiveMetrics.Transactions["Home"] == nil || updated.LiveMetrics.Transactions["Home"].Count != 2 || updated.LiveMetrics.Transactions["Home"].Errors != 1 {
		t.Fatalf("unexpected Home transaction metrics: %#v", updated.LiveMetrics.Transactions["Home"])
	}
	if updated.LiveMetrics.Transactions["Home"].DependencyType != "http" || updated.LiveMetrics.Transactions["Home"].DependencyTarget != "api.example.test" {
		t.Fatalf("unexpected Home dependency metadata: %#v", updated.LiveMetrics.Transactions["Home"])
	}
	if updated.LiveMetrics.Transactions["Home"].AvgDNSMs != 6 || updated.LiveMetrics.Transactions["Home"].AvgConnectMs != 10 || updated.LiveMetrics.Transactions["Home"].AvgTLSMs != 3 || updated.LiveMetrics.Transactions["Home"].AvgTTFBMs != 67 {
		t.Fatalf("unexpected Home timing phase averages: %#v", updated.LiveMetrics.Transactions["Home"])
	}
	if updated.LiveMetrics.Transactions["Home"].CorrelationCount != 2 {
		t.Fatalf("unexpected Home correlation count: %#v", updated.LiveMetrics.Transactions["Home"])
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry == nil {
		t.Fatalf("expected Home server telemetry summary, got %#v", updated.LiveMetrics.Transactions["Home"])
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgServerProcessingMs != 50 {
		t.Fatalf("Home avg_server_processing_ms = %v, want 50", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgServerProcessingMs)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgTransferMs != 30 {
		t.Fatalf("Home avg_transfer_ms = %v, want 30", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgTransferMs)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ConnectionReusedCount != 1 {
		t.Fatalf("Home connection_reused_count = %d, want 1", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ConnectionReusedCount)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ConnectionReusedPct != 50 {
		t.Fatalf("Home connection_reused_pct = %v, want 50", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ConnectionReusedPct)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgMessageLatencyMs != 45 {
		t.Fatalf("Home avg_message_latency_ms = %v, want 45", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgMessageLatencyMs)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.MaxMessageLatencyMs != 55 {
		t.Fatalf("Home max_message_latency_ms = %v, want 55", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.MaxMessageLatencyMs)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.MessagesSent != 2 || updated.LiveMetrics.Transactions["Home"].ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("Home message totals = %#v", updated.LiveMetrics.Transactions["Home"].ServerTelemetry)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgMessagesSentPerSec != 7.5 || updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgMessagesReceivedPerSec != 2.5 {
		t.Fatalf("Home message rates = %#v", updated.LiveMetrics.Transactions["Home"].ServerTelemetry)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgConsumerLagMs != 45 {
		t.Fatalf("Home avg_consumer_lag_ms = %v, want 45", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.AvgConsumerLagMs)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.MaxConsumerLagMs != 60 {
		t.Fatalf("Home max_consumer_lag_ms = %v, want 60", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.MaxConsumerLagMs)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.RequestSizeClasses["small"] != 1 || updated.LiveMetrics.Transactions["Home"].ServerTelemetry.RequestSizeClasses["medium"] != 1 {
		t.Fatalf("Home request_size_classes = %#v", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.RequestSizeClasses)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ResponseSizeClasses["medium"] != 1 || updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ResponseSizeClasses["large"] != 1 {
		t.Fatalf("Home response_size_classes = %#v", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ResponseSizeClasses)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.RequestPayloadHistogram["1-4 KB"] != 2 {
		t.Fatalf("Home request_payload_histogram = %#v", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.RequestPayloadHistogram)
	}
	if updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ResponsePayloadHistogram["1-4 KB"] != 1 || updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ResponsePayloadHistogram["4-16 KB"] != 1 {
		t.Fatalf("Home response_payload_histogram = %#v", updated.LiveMetrics.Transactions["Home"].ServerTelemetry.ResponsePayloadHistogram)
	}
}

func TestBuildLiveMetricsFromDistributedBatchUsesBatchTelemetry(t *testing.T) {
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{
		TimestampUnixMs: 2_000,
		Stats: generatorrpc.GeneratorStats{
			ActiveVUsers: 3,
		},
		Telemetry: &generatorrpc.MetricsTelemetry{
			DroppedResults:    7,
			CollectedAtUnixMs: 900,
			PublishedAtUnixMs: 1_500,
			SampleAgeMs:       600,
		},
	}, 0, nil)

	if live.Telemetry == nil {
		t.Fatal("expected telemetry on distributed live metrics")
	}
	if live.Telemetry.DroppedResults != 7 {
		t.Fatalf("dropped results = %d, want 7", live.Telemetry.DroppedResults)
	}
	if !live.Telemetry.CollectedAt.Equal(time.UnixMilli(900)) {
		t.Fatalf("collected_at = %s, want %s", live.Telemetry.CollectedAt, time.UnixMilli(900))
	}
	if !live.Telemetry.GeneratorPublishedAt.Equal(time.UnixMilli(1500)) {
		t.Fatalf("generator_published_at = %s, want %s", live.Telemetry.GeneratorPublishedAt, time.UnixMilli(1500))
	}
	if live.Telemetry.GeneratorProcessingLagMs != 600 {
		t.Fatalf("generator_processing_lag_ms = %v, want 600", live.Telemetry.GeneratorProcessingLagMs)
	}
	if live.Telemetry.SampleAgeMs != 600 {
		t.Fatalf("sample_age_ms = %v, want 600", live.Telemetry.SampleAgeMs)
	}
}

func TestBuildLiveMetricsFromDistributedBatchMapsErrorEvidenceAndGroupMetrics(t *testing.T) {
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{
		Groups: []TestGroup{{Name: "buyers", VUsers: 7}},
	}, generatorrpc.MetricsBatch{
		TimestampUnixMs: 2_000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 7},
		Groups:          []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 7}},
		Transactions: []generatorrpc.TransactionMetric{
			{Name: "POST /orders", Group: "buyers", DurationMs: 100, StatusCode: 201, Success: true},
			{
				Name:            "POST /orders",
				Group:           "buyers",
				StartTimeUnixMs: 2_000,
				DurationMs:      220,
				StatusCode:      500,
				Success:         false,
				ErrorMessage:    "HTTP 500 checkout order 123",
				Snapshot:        &protocol.ErrorSnapshot{RequestMethod: "POST", RequestURL: "https://checkout.example.test/orders", ResponseStatus: 500, ResponseBody: `{"error":"failed"}`},
			},
			{Name: "POST /orders", Group: "buyers", DurationMs: 280, StatusCode: 500, Success: false, ErrorMessage: "HTTP 500 checkout order 456"},
		},
	}, 0, nil)

	if len(live.ErrorEvidence) != 1 {
		t.Fatalf("live error evidence = %#v, want one snapshot-backed evidence item", live.ErrorEvidence)
	}
	if live.ErrorEvidence[0].Action != "POST /orders" || live.ErrorEvidence[0].Group != "buyers" {
		t.Fatalf("live evidence routing = %#v", live.ErrorEvidence[0])
	}
	if live.ErrorEvidence[0].Snapshot == nil || live.ErrorEvidence[0].Snapshot.ResponseBody != `{"error":"failed"}` {
		t.Fatalf("live evidence snapshot = %#v", live.ErrorEvidence[0].Snapshot)
	}

	group := live.GroupMetrics["buyers"]
	if group == nil {
		t.Fatalf("group metrics = %#v, want buyers", live.GroupMetrics)
	}
	if group.VUsers != 7 || group.Transactions != 1 || group.Errors != 2 {
		t.Fatalf("group metrics = %#v, want vusers 7, tx 1, errors 2", group)
	}
	if group.AvgResponseMs != 200 || group.P95Ms <= 0 {
		t.Fatalf("group latency metrics = %#v, want avg 200 and p95", group)
	}
	tx := live.Transactions["POST /orders"]
	if tx == nil || tx.LastErrorMessage != "HTTP 500 checkout order 456" || tx.LastStatusCode != 500 {
		t.Fatalf("transaction error metadata = %#v", tx)
	}
}

func TestBuildLiveMetricsFromDistributedBatchAggregatesSOAPOperations(t *testing.T) {
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{
		Transactions: []generatorrpc.TransactionMetric{
			{
				Name:             "SOAPRequest",
				DurationMs:       120,
				Success:          true,
				DependencyType:   "soap",
				DependencyTarget: "checkout.example.test#CreateOrder",
				SOAPOperation:    "CreateOrder",
				ServerTelemetry:  &generatorrpc.ServerTelemetry{ServerProcessingMs: 40, TransferMs: 20},
			},
			{
				Name:             "SOAPRequest",
				DurationMs:       180,
				Success:          false,
				DependencyType:   "soap",
				DependencyTarget: "checkout.example.test#CreateOrder",
				SOAPOperation:    "CreateOrder",
				ServerTelemetry:  &generatorrpc.ServerTelemetry{ServerProcessingMs: 60, TransferMs: 10},
			},
		},
	}, 0, nil)

	if live.SOAPOperations == nil || live.SOAPOperations["CreateOrder"] == nil {
		t.Fatalf("expected live SOAP operations, got %#v", live.SOAPOperations)
	}
	operation := live.SOAPOperations["CreateOrder"]
	if operation.Count != 2 || operation.Errors != 1 {
		t.Fatalf("unexpected operation metrics %#v", operation)
	}
	if operation.ServerTelemetry == nil || operation.ServerTelemetry.AvgServerProcessingMs != 50 {
		t.Fatalf("unexpected SOAP operation server telemetry %#v", operation.ServerTelemetry)
	}
}

func TestBuildLiveMetricsFromDistributedBatchAggregatesDependencyHealth(t *testing.T) {
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{
		Transactions: []generatorrpc.TransactionMetric{
			{
				Name:             "GET /",
				DurationMs:       90,
				Success:          true,
				DNSMs:            6,
				ConnectMs:        12,
				DependencyType:   "http",
				DependencyTarget: "api.example.test",
				ServerTelemetry:  &generatorrpc.ServerTelemetry{ServerProcessingMs: 35, TransferMs: 10},
			},
			{
				Name:             "POST /checkout",
				DurationMs:       180,
				Success:          false,
				DNSMs:            10,
				ConnectMs:        18,
				DependencyType:   "http",
				DependencyTarget: "api.example.test",
				ServerTelemetry:  &generatorrpc.ServerTelemetry{ServerProcessingMs: 55, TransferMs: 14},
			},
		},
	}, 0, nil)

	if live.DependencyHealth == nil || live.DependencyHealth["api.example.test"] == nil {
		t.Fatalf("expected dependency health aggregate, got %#v", live.DependencyHealth)
	}
	dep := live.DependencyHealth["api.example.test"]
	if dep.Count != 2 || dep.Errors != 1 {
		t.Fatalf("unexpected dependency health counts %#v", dep)
	}
	if dep.AvgMs != 135 || dep.P95Ms <= 0 || dep.P99Ms <= 0 {
		t.Fatalf("expected dependency health latency stats, got %#v", dep)
	}
	if dep.ServerTelemetry == nil || dep.ServerTelemetry.AvgServerProcessingMs != 45 || dep.ServerTelemetry.AvgTransferMs != 12 {
		t.Fatalf("unexpected dependency health telemetry %#v", dep.ServerTelemetry)
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
		if updated.LiveMetrics != nil && updated.LiveMetrics.TotalTx > 0 && updated.LiveMetrics.Transactions["Checkout"] != nil {
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

	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())

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
			// Exercise remote groups without turning this integration test into an
			// unbounded archive-throughput benchmark on fast loopback connections.
			Pacing: dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
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
			// Exercise remote groups without turning this integration test into an
			// unbounded archive-throughput benchmark on fast loopback connections.
			Pacing: dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
			RunLogic: dsl.RunLogicConfig{
				Iterations: 0,
			},
		},
		Actions: []dsl.Action{{Name: "Admins Home", Type: "GET", Target: "/admins"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed actual multi-group execution",
		Duration: "5s",
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

	deadline := time.Now().Add(15 * time.Second)
	for {
		updated, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if updated.LiveMetrics != nil && updated.LiveMetrics.TotalTx > 0 && updated.LiveMetrics.Transactions["Buyers"] != nil && updated.LiveMetrics.Transactions["Admins"] != nil {
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

	deadline = time.Now().Add(15 * time.Second)
	for {
		completed, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after execution", testRun.ID)
		}
		// Terminal execution is visible before publication finishes. Keep this
		// fixture's archive directory alive until publication is attached.
		if completed.Status == "COMPLETE" && completed.EndedAt != nil && completed.Archive != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected remote multi-group execution to complete, got status=%q ended=%v error=%q", completed.Status, completed.EndedAt, completed.Error)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDistributedExecutionBackendDistributesGroupsAcrossMultipleGenerators(t *testing.T) {
	const bufSize = 1024 * 1024

	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
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

	listenerA := bufconn.Listen(bufSize)
	serverA := grpc.NewServer()
	serviceA := generatorrpc.NewService("generator-a", 50, generatorrpc.WithExecution())
	pb.RegisterGeneratorControlServer(serverA, serviceA)
	go func() {
		_ = serverA.Serve(listenerA)
	}()
	t.Cleanup(func() {
		serverA.Stop()
		_ = listenerA.Close()
	})

	listenerB := bufconn.Listen(bufSize)
	serverB := grpc.NewServer()
	serviceB := generatorrpc.NewService("generator-b", 50, generatorrpc.WithExecution())
	pb.RegisterGeneratorControlServer(serverB, serviceB)
	go func() {
		_ = serverB.Serve(listenerB)
	}()
	t.Cleanup(func() {
		serverB.Stop()
		_ = listenerB.Close()
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet-a", "bufnet-b"},
		5*time.Second,
		func(ctx context.Context, target string) (generatorClient, error) {
			switch target {
			case "bufnet-a":
				return generatorrpc.Dial(
					ctx,
					target,
					grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
						return listenerA.Dial()
					}),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithBlock(),
				)
			case "bufnet-b":
				return generatorrpc.Dial(
					ctx,
					target,
					grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
						return listenerB.Dial()
					}),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithBlock(),
				)
			default:
				return nil, context.DeadlineExceeded
			}
		},
	))

	// Bound archive volume: this tests distribution and merge correctness, not
	// unpaced localhost throughput or Parquet compression speed.
	runner.StoreScript(&dsl.Script{
		ID:       "buyers-script",
		Name:     "Buyers",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 0}, Pacing: dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100}},
		Actions:  []dsl.Action{{Name: "Buyers Home", Type: "GET", Target: "/buyers"}},
	})
	runner.StoreScript(&dsl.Script{
		ID:       "admins-script",
		Name:     "Admins",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 0}, Pacing: dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100}},
		Actions:  []dsl.Action{{Name: "Admins Home", Type: "GET", Target: "/admins"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed multi-generator execution",
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

	deadline := time.Now().Add(8 * time.Second)
	for {
		updated, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if updated.Status == "COMPLETE" && updated.Archive != nil && updated.LiveMetrics != nil && updated.LiveMetrics.GroupMetrics["buyers"] != nil && updated.LiveMetrics.GroupMetrics["admins"] != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed multi-generator run to complete with merged archive, got status=%q archive=%#v metrics=%#v", updated.Status, updated.Archive, updated.LiveMetrics)
		}
		time.Sleep(25 * time.Millisecond)
	}

	completed, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q after completion", testRun.ID)
	}
	if completed.LiveMetrics.GroupMetrics["buyers"] == nil || completed.LiveMetrics.GroupMetrics["admins"] == nil {
		t.Fatalf("expected merged group metrics, got %#v", completed.LiveMetrics.GroupMetrics)
	}
	manifest, err := runner.GetRunArchiveManifest(testRun.ID)
	if err != nil {
		t.Fatalf("GetRunArchiveManifest returned error: %v", err)
	}
	if manifest.TransactionCount == 0 || manifest.ResourceSampleCount == 0 || manifest.IntervalSummaryCount == 0 {
		t.Fatalf("expected merged archive counts to be populated, got %#v", manifest)
	}
	for _, name := range []string{"manifest.json", "transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl", "transactions.parquet", "resources.parquet", "interval_summaries.parquet"} {
		if _, err := os.Stat(filepath.Join(completed.Archive.ArchivePath, name)); err != nil {
			t.Fatalf("expected merged remote archive file %s to exist: %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientA, err := generatorrpc.Dial(
		ctx,
		"bufnet-a",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listenerA.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("Dial generator A: %v", err)
	}
	defer clientA.Close()
	clientB, err := generatorrpc.Dial(
		ctx,
		"bufnet-b",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listenerB.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("Dial generator B: %v", err)
	}
	defer clientB.Close()

	manifestAContent, err := clientA.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: testRun.ID, Name: "manifest.json"})
	if err != nil {
		t.Fatalf("GetArchiveArtifact A manifest: %v", err)
	}
	manifestBContent, err := clientB.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: testRun.ID, Name: "manifest.json"})
	if err != nil {
		t.Fatalf("GetArchiveArtifact B manifest: %v", err)
	}
	var manifestA RunArchiveManifest
	if err := json.Unmarshal(manifestAContent, &manifestA); err != nil {
		t.Fatalf("Unmarshal generator A manifest: %v", err)
	}
	var manifestB RunArchiveManifest
	if err := json.Unmarshal(manifestBContent, &manifestB); err != nil {
		t.Fatalf("Unmarshal generator B manifest: %v", err)
	}
	if manifest.TransactionCount != manifestA.TransactionCount+manifestB.TransactionCount {
		t.Fatalf("merged transaction count = %d, want %d", manifest.TransactionCount, manifestA.TransactionCount+manifestB.TransactionCount)
	}

	transactionsA, err := clientA.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: testRun.ID, Name: "transactions.jsonl"})
	if err != nil {
		t.Fatalf("GetArchiveArtifact A transactions: %v", err)
	}
	transactionsB, err := clientB.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: testRun.ID, Name: "transactions.jsonl"})
	if err != nil {
		t.Fatalf("GetArchiveArtifact B transactions: %v", err)
	}
	if !strings.Contains(string(transactionsA), "Buyers Home") || strings.Contains(string(transactionsA), "Admins Home") {
		t.Fatalf("expected generator A archive to only contain buyers traffic, got %q", string(transactionsA))
	}
	if !strings.Contains(string(transactionsB), "Admins Home") || strings.Contains(string(transactionsB), "Buyers Home") {
		t.Fatalf("expected generator B archive to only contain admins traffic, got %q", string(transactionsB))
	}
}

func TestDistributedExecutionBackendPrefersHigherCapacityGenerator(t *testing.T) {
	const bufSize = 1024 * 1024

	listenerA := bufconn.Listen(bufSize)
	serverA := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverA, generatorrpc.NewService("generator-a", 10))
	go func() {
		_ = serverA.Serve(listenerA)
	}()
	t.Cleanup(func() {
		serverA.Stop()
		_ = listenerA.Close()
	})

	listenerB := bufconn.Listen(bufSize)
	serverB := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverB, generatorrpc.NewService("generator-b", 10))
	go func() {
		_ = serverB.Serve(listenerB)
	}()
	t.Cleanup(func() {
		serverB.Stop()
		_ = listenerB.Close()
	})

	dialTarget := func(ctx context.Context, target string) (*generatorrpc.Client, error) {
		switch target {
		case "bufnet-a":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerA.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		case "bufnet-b":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerB.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		default:
			return nil, context.DeadlineExceeded
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	preloadClientA, err := dialTarget(ctx, "bufnet-a")
	if err != nil {
		t.Fatalf("Dial preload generator A: %v", err)
	}
	defer preloadClientA.Close()
	if _, err := preloadClientA.DistributeScript(ctx, generatorrpc.Script{ID: "preload-script", Content: []byte(`{"name":"preload"}`), ContentHash: "hash-preload"}); err != nil {
		t.Fatalf("Distribute preload script: %v", err)
	}
	if _, err := preloadClientA.AssignWork(ctx, generatorrpc.Assignment{TestID: "existing-load", GroupName: "Busy", ScriptID: "preload-script", VUserCount: 8}); err != nil {
		t.Fatalf("Assign preload work: %v", err)
	}

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	backend := newDistributedExecutionBackend(
		[]string{"bufnet-a", "bufnet-b"},
		5*time.Second,
		func(ctx context.Context, target string) (generatorClient, error) {
			return dialTarget(ctx, target)
		},
	)
	runner.setExecutionBackend(backend)

	probeA := backend.probeGeneratorLoad(ctx, "bufnet-a", preloadClientA)
	if probeA.AvailableCapacity != 2 {
		t.Fatalf("probe generator A available capacity = %d, want 2", probeA.AvailableCapacity)
	}
	probeClientB, err := dialTarget(ctx, "bufnet-b")
	if err != nil {
		t.Fatalf("Dial probe generator B: %v", err)
	}
	probeB := backend.probeGeneratorLoad(ctx, "bufnet-b", probeClientB)
	_ = probeClientB.Close()
	if probeB.AvailableCapacity != 10 {
		t.Fatalf("probe generator B available capacity = %d, want 10", probeB.AvailableCapacity)
	}

	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Capacity aware placement",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-1",
			VUsers:   4,
			RampUp:   "0s",
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
			if started.remoteGroupTargets["Group A"] != "bufnet-b" {
				t.Fatalf("remote group target = %q, want bufnet-b", started.remoteGroupTargets["Group A"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark capacity-aware test running, got status %q", started.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	clientA, err := dialTarget(ctx, "bufnet-a")
	if err != nil {
		t.Fatalf("Dial generator A: %v", err)
	}
	defer clientA.Close()
	clientB, err := dialTarget(ctx, "bufnet-b")
	if err != nil {
		t.Fatalf("Dial generator B: %v", err)
	}
	defer clientB.Close()

	stopA, err := clientA.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator A: %v", err)
	}
	if stopA.Acknowledged {
		t.Fatalf("expected generator A to not own new test assignment")
	}
	stopB, err := clientB.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator B: %v", err)
	}
	if !stopB.Acknowledged {
		t.Fatalf("expected generator B to own new test assignment")
	}
	if stopB.PendingVUsers != 4 {
		t.Fatalf("generator B pending vusers = %d, want 4", stopB.PendingVUsers)
	}
	stopExisting, err := preloadClientA.StopTest(ctx, "existing-load", true)
	if err != nil {
		t.Fatalf("StopTest existing load on generator A: %v", err)
	}
	if stopExisting.PendingVUsers != 8 {
		t.Fatalf("existing load pending vusers = %d, want 8", stopExisting.PendingVUsers)
	}
}

func TestDistributedExecutionBackendHonorsExplicitHostSelection(t *testing.T) {
	const bufSize = 1024 * 1024

	listenerA := bufconn.Listen(bufSize)
	serverA := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverA, generatorrpc.NewService("generator-a", 10))
	go func() {
		_ = serverA.Serve(listenerA)
	}()
	t.Cleanup(func() {
		serverA.Stop()
		_ = listenerA.Close()
	})

	listenerB := bufconn.Listen(bufSize)
	serverB := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverB, generatorrpc.NewService("generator-b", 10))
	go func() {
		_ = serverB.Serve(listenerB)
	}()
	t.Cleanup(func() {
		serverB.Stop()
		_ = listenerB.Close()
	})

	dialTarget := func(ctx context.Context, target string) (*generatorrpc.Client, error) {
		switch target {
		case "bufnet-a":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerA.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		case "bufnet-b":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerB.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		default:
			return nil, context.DeadlineExceeded
		}
	}

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet-a", "bufnet-b"},
		5*time.Second,
		func(ctx context.Context, target string) (generatorClient, error) {
			return dialTarget(ctx, target)
		},
	))

	runner.StoreScript(&dsl.Script{
		ID:       "script-explicit-host",
		Name:     "Explicit Host",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Explicit host assignment",
		Duration: "30s",
		Assignment: &GeneratorAssignmentSettings{
			Mode:            generatorAssignmentModeSpecificHosts,
			SelectedTargets: []string{"bufnet-a"},
		},
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-explicit-host",
			VUsers:   4,
			RampUp:   "0s",
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
			if started.remoteGroupTargets["Group A"] != "bufnet-a" {
				t.Fatalf("remote group target = %q, want bufnet-a", started.remoteGroupTargets["Group A"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to start explicit host test, got status %q", started.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientA, err := dialTarget(ctx, "bufnet-a")
	if err != nil {
		t.Fatalf("Dial generator A: %v", err)
	}
	defer clientA.Close()
	clientB, err := dialTarget(ctx, "bufnet-b")
	if err != nil {
		t.Fatalf("Dial generator B: %v", err)
	}
	defer clientB.Close()

	stopA, err := clientA.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator A: %v", err)
	}
	if !stopA.Acknowledged {
		t.Fatalf("expected generator A to own explicit-host test")
	}
	stopB, err := clientB.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator B: %v", err)
	}
	if stopB.Acknowledged {
		t.Fatalf("expected generator B to not own explicit-host test")
	}
}

func TestDistributedExecutionBackendShardsGroupAcrossSpecificHostsAndRescales(t *testing.T) {
	const bufSize = 1024 * 1024

	listenerA := bufconn.Listen(bufSize)
	serverA := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverA, generatorrpc.NewService("generator-a", 100))
	go func() {
		_ = serverA.Serve(listenerA)
	}()
	t.Cleanup(func() {
		serverA.Stop()
		_ = listenerA.Close()
	})

	listenerB := bufconn.Listen(bufSize)
	serverB := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverB, generatorrpc.NewService("generator-b", 100))
	go func() {
		_ = serverB.Serve(listenerB)
	}()
	t.Cleanup(func() {
		serverB.Stop()
		_ = listenerB.Close()
	})

	dialTarget := func(ctx context.Context, target string) (*generatorrpc.Client, error) {
		switch target {
		case "bufnet-a":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerA.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		case "bufnet-b":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerB.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		default:
			return nil, context.DeadlineExceeded
		}
	}

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet-a", "bufnet-b"},
		400*time.Millisecond,
		func(ctx context.Context, target string) (generatorClient, error) {
			return dialTarget(ctx, target)
		},
	))

	runner.StoreScript(&dsl.Script{
		ID:       "script-sharded-host",
		Name:     "Sharded Host",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Group host sharding",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "Group A",
			ScriptID: "script-sharded-host",
			VUsers:   30,
			RampUp:   "0s",
			Assignment: &GeneratorAssignmentSettings{
				Mode:            generatorAssignmentModeSpecificHosts,
				SelectedTargets: []string{"bufnet-a", "bufnet-b"},
				ShardStrategy:   generatorShardStrategyCapacityWeighted,
			},
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
			shards := started.remoteGroupShards["Group A"]
			if len(shards) != 2 {
				t.Fatalf("remote group shards = %#v, want two shards", shards)
			}
			byTarget := map[string]int{}
			for _, shard := range shards {
				byTarget[shard.Target] = shard.VUsers
			}
			if byTarget["bufnet-a"] != 15 || byTarget["bufnet-b"] != 15 {
				t.Fatalf("initial shard vusers = %#v, want 15/15", byTarget)
			}
			if len(started.GeneratorShards) != 2 {
				t.Fatalf("generator shard summary = %#v, want two entries", started.GeneratorShards)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to start sharded test, got status %q", started.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitForDistributedLoadMetrics(t, runner, testRun.ID, 30)

	if err := runner.ScaleVUsers(testRun.ID, "Group A", 60); err != nil {
		t.Fatalf("ScaleVUsers returned error: %v", err)
	}

	scaled, ok := runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
	}
	scaledByTarget := map[string]int{}
	for _, shard := range scaled.remoteGroupShards["Group A"] {
		scaledByTarget[shard.Target] = shard.VUsers
	}
	if scaledByTarget["bufnet-a"] != 30 || scaledByTarget["bufnet-b"] != 30 {
		t.Fatalf("scaled shard vusers = %#v, want 30/30", scaledByTarget)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientA, err := dialTarget(ctx, "bufnet-a")
	if err != nil {
		t.Fatalf("Dial generator A: %v", err)
	}
	defer clientA.Close()
	clientB, err := dialTarget(ctx, "bufnet-b")
	if err != nil {
		t.Fatalf("Dial generator B: %v", err)
	}
	defer clientB.Close()

	stopA, err := clientA.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator A: %v", err)
	}
	stopB, err := clientB.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator B: %v", err)
	}
	if !stopA.Acknowledged || !stopB.Acknowledged {
		t.Fatalf("expected both generators to own sharded work, got stopA=%#v stopB=%#v", stopA, stopB)
	}
	if stopA.PendingVUsers != 30 || stopB.PendingVUsers != 30 {
		t.Fatalf("pending vusers after scale = %d/%d, want 30/30", stopA.PendingVUsers, stopB.PendingVUsers)
	}
}

func TestDistributedExecutionBackendFailsWhenAssignedGeneratorStreamDrops(t *testing.T) {
	const bufSize = 1024 * 1024

	listenerA := bufconn.Listen(bufSize)
	serverA := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverA, generatorrpc.NewService("generator-a", 10))
	go func() {
		_ = serverA.Serve(listenerA)
	}()
	t.Cleanup(func() {
		serverA.Stop()
		_ = listenerA.Close()
	})

	listenerB := bufconn.Listen(bufSize)
	serverB := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverB, generatorrpc.NewService("generator-b", 10))
	go func() {
		_ = serverB.Serve(listenerB)
	}()
	t.Cleanup(func() {
		serverB.Stop()
		_ = listenerB.Close()
	})

	dialTarget := func(ctx context.Context, target string) (*generatorrpc.Client, error) {
		switch target {
		case "bufnet-a":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerA.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		case "bufnet-b":
			return generatorrpc.Dial(
				ctx,
				target,
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
					return listenerB.Dial()
				}),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
		default:
			return nil, context.DeadlineExceeded
		}
	}

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet-a", "bufnet-b"},
		5*time.Second,
		func(ctx context.Context, target string) (generatorClient, error) {
			return dialTarget(ctx, target)
		},
	))

	runner.StoreScript(&dsl.Script{
		ID:       "buyers-script",
		Name:     "Buyers",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "Buyers Home", Type: "GET", Target: "/buyers"}},
	})
	runner.StoreScript(&dsl.Script{
		ID:       "admins-script",
		Name:     "Admins",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "Admins Home", Type: "GET", Target: "/admins"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed generator loss",
		Duration: "30s",
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

	startedDeadline := time.Now().Add(2 * time.Second)
	for {
		started, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if started.Status == "RUNNING" && started.LiveMetrics != nil {
			if started.remoteGroupTargets["buyers"] != "bufnet-a" || started.remoteGroupTargets["admins"] != "bufnet-b" {
				t.Fatalf("remote group targets = %#v, want buyers on bufnet-a and admins on bufnet-b", started.remoteGroupTargets)
			}
			break
		}
		if time.Now().After(startedDeadline) {
			t.Fatalf("expected distributed test to start before generator loss, got status %q", started.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	serverB.Stop()

	failedDeadline := time.Now().Add(3 * time.Second)
	for {
		updated, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after generator loss", testRun.ID)
		}
		if updated.Status == "FAILED" {
			if !strings.Contains(updated.Error, "bufnet-b") {
				t.Fatalf("expected failure to mention lost generator target, got %q", updated.Error)
			}
			break
		}
		if time.Now().After(failedDeadline) {
			t.Fatalf("expected distributed test to fail after generator loss, got status=%q error=%q", updated.Status, updated.Error)
		}
		time.Sleep(20 * time.Millisecond)
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

	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
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

	// The deadline catches a run that never publishes its archive; it is not
	// a latency budget. Staging and finalization (with its evidence sidecars
	// and their fsyncs) take most of eight seconds on a busy workstation.
	deadline := time.Now().Add(20 * time.Second)
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
	// The generator's publications were sequenced within one source epoch, and
	// the controller archived their coverage.
	if manifest.TelemetrySourcesFile != telemetrySourcesFileName || manifest.TelemetrySourceEpochCount < 1 {
		t.Fatalf("expected archived telemetry source epochs, got file=%q count=%d", manifest.TelemetrySourcesFile, manifest.TelemetrySourceEpochCount)
	}
	sources, err := os.ReadFile(filepath.Join(completed.Archive.ArchivePath, telemetrySourcesFileName))
	if err != nil {
		t.Fatal(err)
	}
	var epoch TelemetrySourceEpoch
	if err := json.Unmarshal(bytes.SplitN(sources, []byte("\n"), 2)[0], &epoch); err != nil || epoch.Source == "" || epoch.Epoch == "" || epoch.FirstSequence == 0 || epoch.Received == 0 {
		t.Fatalf("archived source epoch %s: %v", sources, err)
	}
	// The consumer observed the terminal publication, so a run watched from
	// its first publication knows its loss exactly.
	if epoch.TerminalSequence == 0 || epoch.TerminalSequence != epoch.LastSequence || !epoch.LossExact {
		t.Fatalf("archived source epoch is not exact after a complete run: %s", sources)
	}
	// Its loss counters, this test's subscriber drops included, are exact
	// when every publication arrived.
	if epoch.LostPublications == 0 {
		for _, name := range telemetryEpochCounters {
			if epoch.Counters[name].State != TelemetryCounterExact {
				t.Fatalf("archived %s is not exact after a complete run: %s", name, sources)
			}
		}
	}
	// The live roll-up reached the interval summaries the run archived.
	intervals, err := os.ReadFile(filepath.Join(completed.Archive.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	rolledUp := false
	for _, line := range bytes.Split(bytes.TrimSpace(intervals), []byte("\n")) {
		var interval IntervalSummaryRecord
		if json.Unmarshal(line, &interval) == nil && interval.Telemetry != nil && interval.Telemetry.Sources != nil && interval.Telemetry.Sources.Epochs >= 1 && interval.Telemetry.Sources.Sources >= 1 &&
			interval.Telemetry.Loss != nil && interval.Telemetry.Loss.DroppedResults != nil && interval.Telemetry.Loss.LostPublications != nil {
			rolledUp = true
			break
		}
	}
	if !rolledUp {
		t.Fatal("no archived interval carried the live telemetry source roll-up")
	}
	// Transport lag crossed the generator's clock and carries its quality.
	qualified := false
	for _, line := range bytes.Split(bytes.TrimSpace(intervals), []byte("\n")) {
		var interval IntervalSummaryRecord
		if json.Unmarshal(line, &interval) == nil && interval.Telemetry != nil && interval.Telemetry.TransportLagQuality != nil {
			qualified = true
			break
		}
	}
	if !qualified {
		t.Fatal("no archived interval carried the transport lag's clock quality")
	}
	// The manifest carries the run's wall-time telemetry account.
	coverage := manifest.TelemetryCoverage
	if coverage == nil || coverage.Algorithm != telemetryCoverageAlgorithm || coverage.Coverage.WindowMs <= 0 || len(coverage.Coverage.Sources) != 1 || coverage.Coverage.Sources[0].ObservedMs <= 0 || len(coverage.Segments) != 1 || coverage.RetainedIntervals == 0 ||
		coverage.HealthyMs+coverage.DegradedMs+coverage.BreachedMs+coverage.UnknownMs != coverage.Coverage.WindowMs {
		t.Fatalf("telemetry coverage record %+v", coverage)
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

	deadline := time.Now().Add(5 * time.Second)
	for {
		runner.mu.RLock()
		started := runner.tests[testRun.ID]
		if started == nil {
			runner.mu.RUnlock()
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		status := started.Status
		activated := started.remoteActivated
		runner.mu.RUnlock()
		if status == "RUNNING" && activated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to activate test, got status %q activated=%v", status, activated)
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
	if !reflect.DeepEqual(backend.configuredTargets(), []string{"127.0.0.1:9090"}) {
		t.Fatalf("backend configured targets = %v, want [127.0.0.1:9090]", backend.configuredTargets())
	}
	if len(backend.targets) != 1 || backend.targets[0].Address != "127.0.0.1:9090" || backend.targets[0].Region != "" {
		t.Fatalf("backend target specs = %#v, want address-only spec", backend.targets)
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
		if started.Status == "RUNNING" && started.remoteActivated && !started.remoteActivationPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend activation, got status=%q activated=%t pending=%t", started.Status, started.remoteActivated, started.remoteActivationPending)
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
		400*time.Millisecond,
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
	waitForDistributedLoadMetrics(t, runner, testRun.ID, 5)

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

	deadline = time.Now().Add(5 * time.Second)
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
		400*time.Millisecond,
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
	waitForDistributedLoadMetrics(t, runner, testRun.ID, 5)

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
		400*time.Millisecond,
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
	waitForDistributedLoadMetrics(t, runner, testRun.ID, 8)

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

func TestDistributedExecutionBackendScalesGroupOnOwningGenerator(t *testing.T) {
	const bufSize = 1024 * 1024

	listenerA := bufconn.Listen(bufSize)
	serverA := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverA, generatorrpc.NewService("generator-a", 50))
	go func() {
		_ = serverA.Serve(listenerA)
	}()
	t.Cleanup(func() {
		serverA.Stop()
		_ = listenerA.Close()
	})

	listenerB := bufconn.Listen(bufSize)
	serverB := grpc.NewServer()
	pb.RegisterGeneratorControlServer(serverB, generatorrpc.NewService("generator-b", 50))
	go func() {
		_ = serverB.Serve(listenerB)
	}()
	t.Cleanup(func() {
		serverB.Stop()
		_ = listenerB.Close()
	})

	hub := NewHub()
	runner := NewTestRunner(1, hub, nil)
	runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"bufnet-a", "bufnet-b"},
		400*time.Millisecond,
		func(ctx context.Context, target string) (generatorClient, error) {
			switch target {
			case "bufnet-a":
				return generatorrpc.Dial(
					ctx,
					target,
					grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
						return listenerA.Dial()
					}),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithBlock(),
				)
			case "bufnet-b":
				return generatorrpc.Dial(
					ctx,
					target,
					grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
						return listenerB.Dial()
					}),
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithBlock(),
				)
			default:
				return nil, context.DeadlineExceeded
			}
		},
	))

	runner.StoreScript(&dsl.Script{
		ID:       "script-1",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})

	testRun, err := runner.CreateTest(CreateTestRequest{
		Name:     "Distributed scale fan-out",
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
		if started.Status == "RUNNING" && started.remoteGroupTargets["Group A"] != "" && started.remoteGroupTargets["Group B"] != "" && started.remoteGroupTargets["Group A"] != started.remoteGroupTargets["Group B"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected distributed backend to mark fan-out test running, got status=%q metrics=%#v", started.Status, started.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitForDistributedLoadMetrics(t, runner, testRun.ID, 8)

	if err := runner.ScaleVUsers(testRun.ID, "Group B", 7); err != nil {
		t.Fatalf("ScaleVUsers returned error: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		scaled, ok := runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist after scale", testRun.ID)
		}
		if scaled.LiveMetrics != nil && scaled.LiveMetrics.ActiveVUsers == 12 && scaled.LiveMetrics.GroupMetrics["Group A"] != nil && scaled.LiveMetrics.GroupMetrics["Group B"] != nil && scaled.LiveMetrics.GroupMetrics["Group B"].VUsers == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected fan-out live metrics to reflect scaled vusers, got %#v", scaled.LiveMetrics)
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clientA, err := generatorrpc.Dial(
		ctx,
		"bufnet-a",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listenerA.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("Dial generator A: %v", err)
	}
	defer clientA.Close()
	clientB, err := generatorrpc.Dial(
		ctx,
		"bufnet-b",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listenerB.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		t.Fatalf("Dial generator B: %v", err)
	}
	defer clientB.Close()

	stopA, err := clientA.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator A: %v", err)
	}
	stopB, err := clientB.StopTest(ctx, testRun.ID, true)
	if err != nil {
		t.Fatalf("StopTest generator B: %v", err)
	}
	if stopA.PendingVUsers != 5 {
		t.Fatalf("generator A pending vusers = %d, want 5", stopA.PendingVUsers)
	}
	if stopB.PendingVUsers != 7 {
		t.Fatalf("generator B pending vusers = %d, want 7", stopB.PendingVUsers)
	}
}

// TestApplyRemoteMetricsBatchTerminalBatchPreservesTransactions guards the fix
// for a flaky distributed-execution race: the generator emits the terminal
// COMPLETE/FAILED lifecycle batch with no transaction window (metricsBatchForState),
// and under load the controller can drain the last metrics batch and the terminal
// batch from the stream in a single burst. Without preserving the prior snapshot,
// the terminal batch resets live TotalTx to zero and a poller never observes the
// run's transactions.
func TestApplyRemoteMetricsBatchTerminalBatchPreservesTransactions(t *testing.T) {
	hub := NewHub()
	tr := NewTestRunner(1, hub, nil)
	b := newDistributedExecutionBackend([]string{"gen-1"}, 5*time.Second, nil)
	const target = "gen-1"
	test := &ManagedTest{
		ID:                    "t-terminal-preserve",
		Groups:                []TestGroup{{Name: "buyers", VUsers: 1}, {Name: "admins", VUsers: 1}},
		remoteClientsByTarget: map[string]generatorClient{target: nil},
	}

	running := generatorrpc.MetricsBatch{
		TestID:         test.ID,
		LifecycleState: "RUNNING",
		WindowMs:       1000,
		Stats:          generatorrpc.GeneratorStats{ActiveVUsers: 2},
		Groups:         []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 1}, {Name: "admins", ActiveVUsers: 1}},
		Transactions: []generatorrpc.TransactionMetric{
			{Name: "Buyers", Group: "buyers", DurationMs: 10, StatusCode: 200, Success: true},
			{Name: "Admins", Group: "admins", DurationMs: 12, StatusCode: 200, Success: true},
		},
	}
	live, finalStatus, _ := b.applyRemoteMetricsBatch(tr, test, target, running)
	if finalStatus != "" {
		t.Fatalf("running batch finalStatus = %q, want empty", finalStatus)
	}
	if live.TotalTx == 0 {
		t.Fatal("expected TotalTx>0 from the running batch")
	}

	// Terminal COMPLETE batch with no transaction window (as the generator emits).
	complete := generatorrpc.MetricsBatch{
		TestID:         test.ID,
		LifecycleState: "COMPLETE",
		Stats:          generatorrpc.GeneratorStats{ActiveVUsers: 0},
	}
	liveDone, finalStatusDone, _ := b.applyRemoteMetricsBatch(tr, test, target, complete)
	if finalStatusDone != "COMPLETE" {
		t.Fatalf("complete batch finalStatus = %q, want COMPLETE", finalStatusDone)
	}
	if liveDone.TotalTx == 0 {
		t.Fatal("terminal COMPLETE batch must not reset live TotalTx to zero — the run's transactions must persist")
	}
	if liveDone.Transactions["Buyers"] == nil || liveDone.Transactions["Admins"] == nil {
		t.Fatalf("terminal batch must preserve per-action transactions, got %#v", liveDone.Transactions)
	}
	if liveDone.GroupMetrics["buyers"] == nil || liveDone.GroupMetrics["admins"] == nil {
		t.Fatalf("terminal batch must preserve group metrics, got %#v", liveDone.GroupMetrics)
	}
}

func TestApplyRemoteMetricsBatchIgnoresStaleWindow(t *testing.T) {
	tr := NewTestRunner(1, NewHub(), nil)
	b := &distributedExecutionBackend{}
	target := "gen-a"
	test := &ManagedTest{
		ID:                    "t-stale-window",
		Groups:                []TestGroup{{Name: "buyers", VUsers: 1}},
		remoteClientsByTarget: map[string]generatorClient{target: nil},
	}

	newer := generatorrpc.MetricsBatch{
		TestID:          test.ID,
		LifecycleState:  "RUNNING",
		TimestampUnixMs: 2000,
		WindowMs:        1000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 1},
		Groups:          []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 1}},
		Transactions:    []generatorrpc.TransactionMetric{{Name: "Checkout", Group: "buyers", DurationMs: 10, Success: true}},
	}
	live, _, _ := b.applyRemoteMetricsBatch(tr, test, target, newer)
	if live.TotalTx != 1 {
		t.Fatalf("newer live TotalTx = %d, want 1", live.TotalTx)
	}

	stale := generatorrpc.MetricsBatch{
		TestID:          test.ID,
		LifecycleState:  "RUNNING",
		TimestampUnixMs: 1000,
		WindowMs:        1000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 1},
		Groups:          []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 1}},
		Transactions: []generatorrpc.TransactionMetric{
			{Name: "Stale", Group: "buyers", DurationMs: 10, Success: true},
			{Name: "Stale", Group: "buyers", DurationMs: 10, Success: true},
		},
	}
	live, _, _ = b.applyRemoteMetricsBatch(tr, test, target, stale)
	if live.TotalTx != 1 || live.Transactions["Checkout"] == nil || live.Transactions["Stale"] != nil {
		t.Fatalf("stale batch overwrote live metrics: total=%d tx=%#v", live.TotalTx, live.Transactions)
	}
}

func TestApplyRemoteMetricsBatchAcceptsEqualTimestampCommitTransition(t *testing.T) {
	tr := NewTestRunner(1, NewHub(), nil)
	b := &distributedExecutionBackend{}
	target := "gen-a"
	test := &ManagedTest{
		ID:                    "t-equal-timestamp-commit",
		Groups:                []TestGroup{{Name: "buyers", VUsers: 3}},
		remoteClientsByTarget: map[string]generatorClient{target: nil},
	}

	staged := generatorrpc.MetricsBatch{
		TestID:          test.ID,
		LifecycleState:  "STAGED",
		TimestampUnixMs: 2_000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 0},
		Groups:          []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 0}},
	}
	live, _, _ := b.applyRemoteMetricsBatch(tr, test, target, staged)
	if live.ActiveVUsers != 0 {
		t.Fatalf("staged active VUsers = %d, want 0", live.ActiveVUsers)
	}

	committed := generatorrpc.MetricsBatch{
		TestID:          test.ID,
		LifecycleState:  "RUNNING",
		TimestampUnixMs: staged.TimestampUnixMs,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 3},
		Groups:          []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 3}},
	}
	live, _, _ = b.applyRemoteMetricsBatch(tr, test, target, committed)
	if live.ActiveVUsers != 3 {
		t.Fatalf("committed active VUsers = %d, want 3", live.ActiveVUsers)
	}

	// A delayed staged broadcast must not regress a committed snapshot.
	live, _, _ = b.applyRemoteMetricsBatch(tr, test, target, staged)
	if live.ActiveVUsers != 3 {
		t.Fatalf("delayed staged snapshot regressed active VUsers to %d", live.ActiveVUsers)
	}
}
