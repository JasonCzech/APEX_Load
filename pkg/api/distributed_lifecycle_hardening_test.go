package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type startupLifecycleFakeClient struct {
	mu              sync.Mutex
	accept          bool
	assignErr       error
	heartbeatClient generatorClient
	assigned        int
	scenarioIDs     []string
	active          int
	stopped         int
	closed          int
}

type blockingStopGeneratorClient struct {
	startupLifecycleFakeClient
	stopStarted chan struct{}
	releaseStop chan struct{}
	startOnce   sync.Once
}

type failingDistributedStartStore struct{ err error }

func (s failingDistributedStartStore) SaveTest(*storage.TestRecord) error        { return s.err }
func (failingDistributedStartStore) GetTest(string) (*storage.TestRecord, error) { return nil, nil }
func (failingDistributedStartStore) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}
func (failingDistributedStartStore) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}
func (failingDistributedStartStore) UpdateTestStatus(string, string, *time.Time, string) error {
	return nil
}
func (failingDistributedStartStore) DeleteTest(string) error        { return nil }
func (failingDistributedStartStore) MoveTest(string, *string) error { return nil }

func (c *startupLifecycleFakeClient) DistributeScript(context.Context, generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	return generatorrpc.ScriptResult{Cached: true}, nil
}
func (c *startupLifecycleFakeClient) AssignWork(_ context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.assigned++
	c.scenarioIDs = append(c.scenarioIDs, assignment.ScenarioID)
	if !c.accept {
		return generatorrpc.AssignmentResult{Accepted: false, ErrorMessage: "rejected for test"}, nil
	}
	// Model uncertain RPC commit semantics: the generator may apply the work
	// before the controller loses the response and observes an error.
	c.active += int(assignment.VUserCount)
	return generatorrpc.AssignmentResult{Accepted: true}, c.assignErr
}
func (c *startupLifecycleFakeClient) AdjustVUsers(context.Context, generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	return generatorrpc.AdjustmentResult{Accepted: true}, nil
}
func (c *startupLifecycleFakeClient) AdjustDuration(context.Context, generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error) {
	return generatorrpc.DurationResult{Accepted: true}, nil
}
func (c *startupLifecycleFakeClient) OpenMetricsStream(context.Context) (*generatorrpc.MetricsStream, error) {
	return nil, errors.New("not expected")
}

func (c *startupLifecycleFakeClient) OpenHeartbeat(ctx context.Context) (*generatorrpc.HeartbeatStream, error) {
	if c.heartbeatClient != nil {
		return c.heartbeatClient.OpenHeartbeat(ctx)
	}
	return nil, errors.New("probe unavailable")
}
func (c *startupLifecycleFakeClient) StopTest(context.Context, string, bool) (generatorrpc.StopResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped++
	c.active = 0
	return generatorrpc.StopResult{Acknowledged: true}, nil
}
func (c *startupLifecycleFakeClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, grpcStatus.Error(grpcCodes.NotFound, "archive missing")
}

func (c *blockingStopGeneratorClient) StopTest(ctx context.Context, _ string, _ bool) (generatorrpc.StopResult, error) {
	c.startOnce.Do(func() { close(c.stopStarted) })
	select {
	case <-c.releaseStop:
		return generatorrpc.StopResult{Acknowledged: true}, nil
	case <-ctx.Done():
		return generatorrpc.StopResult{}, ctx.Err()
	}
}

func TestFetchRemoteArchiveBundlesRequireAllRejectsZeroBundles(t *testing.T) {
	client := &startupLifecycleFakeClient{accept: true}
	backend := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil)
	test := &ManagedTest{
		ID:                    "run-missing-archive",
		remoteTarget:          "gen-a",
		remoteClient:          client,
		remoteClientsByTarget: map[string]generatorClient{"gen-a": client},
	}
	runner := &TestRunner{}

	if _, err := backend.fetchRemoteArchiveBundles(runner, test, true); err == nil {
		t.Fatal("requireAll accepted a run with zero remote archive bundles")
	}
	bundles, err := backend.fetchRemoteArchiveBundles(runner, test, false)
	if err != nil {
		t.Fatalf("best-effort archive fetch returned error: %v", err)
	}
	if len(bundles) != 0 {
		t.Fatalf("best-effort bundles = %d, want 0", len(bundles))
	}
}

func TestStartDistributedTestRunFailsClosedOnPersistenceError(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{ID: "run-persist-failure", Status: "INITIALIZING", executionCtx: runCtx, cancel: cancel}
	client := &startupLifecycleFakeClient{accept: true}
	runner := &TestRunner{
		tests:     map[string]*ManagedTest{test.ID: test},
		testStore: failingDistributedStartStore{err: errors.New("database unavailable")},
	}
	backend := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil)

	started, err := backend.startDistributedTestRun(
		runner,
		test,
		map[string][]GeneratorShardSummary{"group": {{Group: "group", Target: "gen-a", VUsers: 1}}},
		map[string]generatorClient{"gen-a": client},
		map[string]*generatorrpc.MetricsStream{},
	)
	if err == nil || started {
		t.Fatalf("start result = started:%v err:%v, want persistence error", started, err)
	}
	if test.Status != "INITIALIZING" || test.StartedAt != nil {
		t.Fatalf("test transition was not restored: status=%s started_at=%v", test.Status, test.StartedAt)
	}
	if test.remoteClient != nil || test.remoteClientsByTarget != nil || test.remoteGroupShards != nil {
		t.Fatalf("remote state remained attached after failed persistence: client=%v clients=%v shards=%v", test.remoteClient, test.remoteClientsByTarget, test.remoteGroupShards)
	}
}

func TestDistributedFailureIsVisibleBeforeRemoteStopCleanupCompletes(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocking := &blockingStopGeneratorClient{
		startupLifecycleFakeClient: startupLifecycleFakeClient{accept: true},
		stopStarted:                make(chan struct{}),
		releaseStop:                make(chan struct{}),
	}
	lost := &startupLifecycleFakeClient{accept: true}
	test := &ManagedTest{
		ID:           "run-generator-loss-cleanup-order",
		Name:         "generator loss cleanup order",
		Status:       "RUNNING",
		executionCtx: runCtx,
		cancel:       cancel,
		remoteClientsByTarget: map[string]generatorClient{
			"gen-lost":      lost,
			"gen-surviving": blocking,
		},
		remoteTargetStates: map[string]string{
			"gen-lost":      "FAILED",
			"gen-surviving": "RUNNING",
		},
	}
	runner := NewTestRunner(1, nil, nil)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	backend := newDistributedExecutionBackend([]string{"gen-lost", "gen-surviving"}, time.Second, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		backend.completeDistributedTestRun(runner, test, "FAILED", "remote target gen-lost metrics stream stopped")
	}()

	select {
	case <-blocking.stopStarted:
	case <-time.After(time.Second):
		t.Fatal("remote stop cleanup did not start")
	}
	updated, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("expected managed test %q to remain available", test.ID)
	}
	if updated.Status != "FAILED" || updated.EndedAt == nil || !strings.Contains(updated.Error, "gen-lost") {
		t.Fatalf("terminal state while cleanup is blocked = status:%q ended_at:%v error:%q", updated.Status, updated.EndedAt, updated.Error)
	}

	close(blocking.releaseStop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("distributed failure cleanup did not finish after remote stop was released")
	}
}

func (c *startupLifecycleFakeClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

func TestDistributedStartupRollsBackEveryAttemptedShard(t *testing.T) {
	clientA := &startupLifecycleFakeClient{accept: true}
	clientB := &startupLifecycleFakeClient{accept: false}
	clientA.heartbeatClient, _ = newTestGeneratorRPCClient(t, "gen-a", 10)
	clientB.heartbeatClient, _ = newTestGeneratorRPCClient(t, "gen-b", 10)
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, func(_ context.Context, target string) (generatorClient, error) {
		if target == "gen-a" {
			return clientA, nil
		}
		return clientB, nil
	})
	script := &dsl.Script{ID: "script", Name: "script", Protocol: "http"}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID:           "run-startup-rollback",
		ScenarioID:   "managed-startup-scenario",
		Name:         "rollback",
		Status:       "INITIALIZING",
		Duration:     time.Minute,
		executionCtx: runCtx,
		cancel:       cancel,
		Groups: []TestGroup{
			{Name: "group-a", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 1, script: script, Assignment: &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{"gen-a"}}},
			{Name: "group-b", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 1, script: script, Assignment: &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{"gen-b"}}},
		},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	backend.Run(runner, test)

	clientA.mu.Lock()
	assignedA, stoppedA := clientA.assigned, clientA.stopped
	clientA.mu.Unlock()
	clientB.mu.Lock()
	assignedB, stoppedB := clientB.assigned, clientB.stopped
	scenariosB := append([]string(nil), clientB.scenarioIDs...)
	clientB.mu.Unlock()
	if assignedA != 1 || assignedB != 1 {
		t.Fatalf("assignment calls = A:%d B:%d, want one each", assignedA, assignedB)
	}
	clientA.mu.Lock()
	scenariosA := append([]string(nil), clientA.scenarioIDs...)
	clientA.mu.Unlock()
	if len(scenariosA) != 1 || scenariosA[0] != test.ScenarioID || len(scenariosB) != 1 || scenariosB[0] != test.ScenarioID {
		t.Fatalf("assignment scenario identities = A:%v B:%v, want %q", scenariosA, scenariosB, test.ScenarioID)
	}
	if stoppedA != 1 {
		t.Fatalf("accepted shard stop count = %d, want 1", stoppedA)
	}
	if stoppedB != 1 {
		t.Fatalf("attempted shard stop count = %d, want 1", stoppedB)
	}
	if test.Status != "FAILED" {
		t.Fatalf("test status = %q, want FAILED", test.Status)
	}
}

func TestDistributedStartupCleansUpAmbiguousAssignWorkError(t *testing.T) {
	wantErr := errors.New("assignment response lost")
	client := &startupLifecycleFakeClient{accept: true, assignErr: wantErr}
	client.heartbeatClient, _ = newTestGeneratorRPCClient(t, "gen-a", 10)
	backend := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return client, nil
	})
	script := &dsl.Script{ID: "script", Name: "script", Protocol: "http"}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID:           "ambiguous-assignment-cleanup",
		Name:         "ambiguous assignment cleanup",
		Status:       "INITIALIZING",
		Duration:     time.Minute,
		executionCtx: runCtx,
		cancel:       cancel,
		Groups: []TestGroup{{
			Name:       "group-a",
			ScriptID:   script.ID,
			ScriptName: script.Name,
			Protocol:   "http",
			VUsers:     3,
			script:     script,
			Assignment: &GeneratorAssignmentSettings{
				Mode:            generatorAssignmentModeSpecificHosts,
				SelectedTargets: []string{"gen-a"},
			},
		}},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	backend.Run(runner, test)

	client.mu.Lock()
	assigned, active, stopped, closed := client.assigned, client.active, client.stopped, client.closed
	client.mu.Unlock()
	if assigned != 1 {
		t.Fatalf("AssignWork calls = %d, want 1", assigned)
	}
	if stopped != 1 {
		t.Fatalf("idempotent StopTest cleanup calls = %d, want 1", stopped)
	}
	if active != 0 {
		t.Fatalf("active remote VUsers after cleanup = %d, want 0", active)
	}
	if closed != 1 {
		t.Fatalf("client Close calls = %d, want 1", closed)
	}
	if test.Status != "FAILED" || !strings.Contains(test.Error, wantErr.Error()) {
		t.Fatalf("test terminal state = %s/%q, want FAILED containing %q", test.Status, test.Error, wantErr)
	}
}
