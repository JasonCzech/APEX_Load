package api

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type orderedScaleClient struct {
	*evacuationFakeGeneratorClient
	heartbeatClient generatorClient
	mu              sync.Mutex
	calls           []int32
	firstEntered    chan struct{}
	releaseFirst    <-chan struct{}
}

func (c *orderedScaleClient) OpenHeartbeat(ctx context.Context) (*generatorrpc.HeartbeatStream, error) {
	return c.heartbeatClient.OpenHeartbeat(ctx)
}

func (c *orderedScaleClient) AdjustVUsers(ctx context.Context, adjustment generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	c.mu.Lock()
	c.calls = append(c.calls, adjustment.TargetVUsers)
	callNumber := len(c.calls)
	c.mu.Unlock()
	if callNumber == 1 {
		close(c.firstEntered)
		select {
		case <-ctx.Done():
			return generatorrpc.AdjustmentResult{}, ctx.Err()
		case <-c.releaseFirst:
		}
	}
	return c.evacuationFakeGeneratorClient.AdjustVUsers(ctx, adjustment)
}

func (c *orderedScaleClient) callTargets() []int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int32(nil), c.calls...)
}

func TestDistributedScaleSerializesControllerTransactionPerTestGroup(t *testing.T) {
	releaseFirst := make(chan struct{})
	clientA := &orderedScaleClient{
		evacuationFakeGeneratorClient: newEvacuationFakeGeneratorClient("gen-a"),
		firstEntered:                  make(chan struct{}),
		releaseFirst:                  releaseFirst,
	}
	clientB := &orderedScaleClient{
		evacuationFakeGeneratorClient: newEvacuationFakeGeneratorClient("gen-b"),
		firstEntered:                  make(chan struct{}),
		releaseFirst:                  releaseFirst,
	}
	clientA.heartbeatClient, _ = newTestGeneratorRPCClient(t, "gen-a", 100)
	clientB.heartbeatClient, _ = newTestGeneratorRPCClient(t, "gen-b", 100)
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, nil)
	hub := NewHub()
	test := &ManagedTest{
		ID:               "ordered-scale",
		Status:           "RUNNING",
		Groups:           []TestGroup{{Name: "buyers", VUsers: 6}},
		TotalVUsers:      6,
		executionBackend: backend,
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {
				{Group: "buyers", Target: "gen-a", VUsers: 3},
				{Group: "buyers", Target: "gen-b", VUsers: 3},
			},
		},
		remoteClientsByTarget: map[string]generatorClient{"gen-a": clientA, "gen-b": clientB},
		remoteTargetStates:    map[string]string{"gen-a": "RUNNING", "gen-b": "RUNNING"},
	}
	runner := &TestRunner{
		tests:     map[string]*ManagedTest{test.ID: test},
		execution: backend,
		hub:       hub,
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- runner.ScaleVUsers(test.ID, "buyers", 10) }()
	for _, entered := range []chan struct{}{clientA.firstEntered, clientB.firstEntered} {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("first scale did not reach every generator")
		}
	}

	secondDone := make(chan error, 1)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		secondDone <- runner.ScaleVUsers(test.ID, "buyers", 20)
	}()
	<-secondStarted
	deadline := time.Now().Add(time.Second)
	for {
		runner.remoteControlMu.Lock()
		gate := runner.remoteControlGates[test.ID]
		waiters := 0
		if gate != nil {
			waiters = gate.refs
		}
		runner.remoteControlMu.Unlock()
		if waiters >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second scale did not reach the per-test/group controller gate")
		}
		time.Sleep(time.Millisecond)
	}
	if got := len(clientA.callTargets()); got != 1 {
		t.Fatalf("second scale reached gen-a before first controller transaction committed: calls=%v", clientA.callTargets())
	}
	if got := len(clientB.callTargets()); got != 1 {
		t.Fatalf("second scale reached gen-b before first controller transaction committed: calls=%v", clientB.callTargets())
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first ScaleVUsers: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second ScaleVUsers: %v", err)
	}

	if got := clientA.callTargets(); len(got) != 2 || got[0] != 5 || got[1] != 10 {
		t.Fatalf("gen-a adjustment order = %v, want [5 10]", got)
	}
	if got := clientB.callTargets(); len(got) != 2 || got[0] != 5 || got[1] != 10 {
		t.Fatalf("gen-b adjustment order = %v, want [5 10]", got)
	}
	if test.Groups[0].VUsers != 20 || shardTotalVUsers(test.remoteGroupShards["buyers"]) != 20 {
		t.Fatalf("final controller allocation = group:%d shards:%+v, want 20", test.Groups[0].VUsers, test.remoteGroupShards["buyers"])
	}
}

func TestDistributedScaleDifferentGroupsCommitConcurrently(t *testing.T) {
	releaseScales := make(chan struct{})
	buyersClient := &orderedScaleClient{
		evacuationFakeGeneratorClient: newEvacuationFakeGeneratorClient("gen-buyers"),
		firstEntered:                  make(chan struct{}),
		releaseFirst:                  releaseScales,
	}
	sellersClient := &orderedScaleClient{
		evacuationFakeGeneratorClient: newEvacuationFakeGeneratorClient("gen-sellers"),
		firstEntered:                  make(chan struct{}),
		releaseFirst:                  releaseScales,
	}
	buyersClient.heartbeatClient, _ = newTestGeneratorRPCClient(t, "gen-buyers", 100)
	sellersClient.heartbeatClient, _ = newTestGeneratorRPCClient(t, "gen-sellers", 100)
	backend := newDistributedExecutionBackend([]string{"gen-buyers", "gen-sellers"}, time.Second, nil)
	test := &ManagedTest{
		ID:               "parallel-group-scale",
		Status:           "RUNNING",
		Groups:           []TestGroup{{Name: "buyers", VUsers: 3}, {Name: "sellers", VUsers: 4}},
		TotalVUsers:      7,
		executionBackend: backend,
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers":  {{Group: "buyers", Target: "gen-buyers", VUsers: 3}},
			"sellers": {{Group: "sellers", Target: "gen-sellers", VUsers: 4}},
		},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-buyers":  buyersClient,
			"gen-sellers": sellersClient,
		},
		remoteTargetStates: map[string]string{"gen-buyers": "RUNNING", "gen-sellers": "RUNNING"},
	}
	runner := &TestRunner{
		tests:     map[string]*ManagedTest{test.ID: test},
		execution: backend,
		hub:       NewHub(),
	}

	buyersDone := make(chan error, 1)
	sellersDone := make(chan error, 1)
	go func() { buyersDone <- runner.ScaleVUsers(test.ID, "buyers", 8) }()
	go func() { sellersDone <- runner.ScaleVUsers(test.ID, "sellers", 9) }()
	for group, entered := range map[string]chan struct{}{
		"buyers":  buyersClient.firstEntered,
		"sellers": sellersClient.firstEntered,
	} {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatalf("%s scale did not reach its generator while the other group was in flight", group)
		}
	}
	close(releaseScales)

	if err := <-buyersDone; err != nil {
		t.Fatalf("buyers ScaleVUsers: %v", err)
	}
	if err := <-sellersDone; err != nil {
		t.Fatalf("sellers ScaleVUsers: %v", err)
	}
	if got := test.remoteControlGeneration; got != 0 {
		t.Fatalf("topology generation = %d, want unchanged by group scales", got)
	}
	if got := test.Groups[0].VUsers; got != 8 {
		t.Fatalf("buyers controller VUsers = %d, want 8", got)
	}
	if got := test.Groups[1].VUsers; got != 9 {
		t.Fatalf("sellers controller VUsers = %d, want 9", got)
	}
	if got := shardTotalVUsers(test.remoteGroupShards["buyers"]); got != 8 {
		t.Fatalf("buyers remote shard VUsers = %d, want 8", got)
	}
	if got := shardTotalVUsers(test.remoteGroupShards["sellers"]); got != 9 {
		t.Fatalf("sellers remote shard VUsers = %d, want 9", got)
	}
	if got := test.TotalVUsers; got != 17 {
		t.Fatalf("total controller VUsers = %d, want 17", got)
	}
}

func shardTotalVUsers(shards []GeneratorShardSummary) int {
	total := 0
	for _, shard := range shards {
		total += shard.VUsers
	}
	return total
}

type blockingAssignClient struct {
	generatorClient
	assigned chan struct{}
	release  <-chan struct{}
	once     sync.Once
}

func (c *blockingAssignClient) AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	result, err := c.generatorClient.AssignWork(ctx, assignment)
	c.once.Do(func() { close(c.assigned) })
	select {
	case <-ctx.Done():
		return generatorrpc.AssignmentResult{}, ctx.Err()
	case <-c.release:
		return result, err
	}
}

func (c *blockingAssignClient) CommitWorkAt(ctx context.Context, testID string, executionGeneration uint64, startAt time.Time) (generatorrpc.WorkCommitResult, error) {
	committer, ok := c.generatorClient.(coordinatedWorkCommitClient)
	if !ok {
		return generatorrpc.WorkCommitResult{}, errors.New("wrapped generator does not support coordinated commit")
	}
	return committer.CommitWorkAt(ctx, testID, executionGeneration, startAt)
}

func TestDistributedEvacuationRollsBackNewCandidateWhenStopWinsCommit(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	service := generatorrpc.NewService("generator-b", 10)
	pb.RegisterGeneratorControlServer(grpcServer, service)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("generator service shutdown: %v", err)
		}
		grpcServer.Stop()
		_ = listener.Close()
	})

	assigned := make(chan struct{})
	releaseAssignment := make(chan struct{})
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 2*time.Second, func(ctx context.Context, target string) (generatorClient, error) {
		if target != "gen-b" {
			return nil, errors.New("failed generator is unavailable")
		}
		client, err := generatorrpc.Dial(
			ctx,
			target,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		if err != nil {
			return nil, err
		}
		return &blockingAssignClient{generatorClient: client, assigned: assigned, release: releaseAssignment}, nil
	})
	script := &dsl.Script{
		ID:       "buyers-script",
		Name:     "Buyers",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "http://127.0.0.1"},
		Actions:  []dsl.Action{{Name: "home", Type: "GET", Target: "/"}},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := time.Now()
	failedClient := newEvacuationFakeGeneratorClient("gen-a")
	test := &ManagedTest{
		ID:               "evacuation-stop-race",
		Name:             "evacuation stop race",
		Status:           "RUNNING",
		Duration:         time.Minute,
		StartedAt:        &started,
		executionCtx:     runCtx,
		cancel:           cancel,
		executionBackend: backend,
		Assignment:       &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:           []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 4, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 4}},
		},
		remoteClientsByTarget:        map[string]generatorClient{"gen-a": failedClient},
		remoteMetricsStreamsByTarget: make(map[string]*generatorrpc.MetricsStream),
		remoteMetricsBatchesByTarget: make(map[string]generatorrpc.MetricsBatch),
		remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
	}
	hub := NewHub()
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}, execution: backend, hub: hub}

	evacuated := make(chan error, 1)
	go func() {
		_, err := backend.EvacuateGenerator(runner, test, "gen-a", false, "generator_loss")
		evacuated <- err
	}()
	select {
	case <-assigned:
	case err := <-evacuated:
		t.Fatalf("evacuation stopped before assigning the replacement generator: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("evacuation did not assign the replacement generator")
	}
	if got := service.ActiveVUsers(); got != 4 {
		t.Fatalf("replacement generator active VUsers before commit = %d, want 4", got)
	}
	if err := runner.StopTest(test.ID); err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	close(releaseAssignment)
	if err := <-evacuated; err == nil {
		t.Fatal("expected evacuation commit to be rejected after StopTest reserved STOPPING")
	}
	if got := service.ActiveVUsers(); got != 0 {
		t.Fatalf("stale evacuation left %d VUsers running on replacement generator", got)
	}
	if _, exists := test.remoteClientsByTarget["gen-b"]; exists {
		t.Fatal("stale evacuation committed replacement generator into controller topology")
	}
}

type parallelStopState struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

type deadlineStopClient struct {
	*evacuationFakeGeneratorClient
	state *parallelStopState
}

func (c *deadlineStopClient) StopTest(ctx context.Context, _ string, _ bool) (generatorrpc.StopResult, error) {
	c.state.mu.Lock()
	c.state.active++
	if c.state.active > c.state.maxActive {
		c.state.maxActive = c.state.active
	}
	c.state.mu.Unlock()
	<-ctx.Done()
	c.state.mu.Lock()
	c.state.active--
	c.state.mu.Unlock()
	return generatorrpc.StopResult{}, ctx.Err()
}

func TestDistributedStopTestContextFansOutWithinParentDeadline(t *testing.T) {
	state := &parallelStopState{}
	clients := make(map[string]generatorClient)
	targets := []string{"gen-a", "gen-b", "gen-c"}
	for _, target := range targets {
		clients[target] = &deadlineStopClient{
			evacuationFakeGeneratorClient: newEvacuationFakeGeneratorClient(target),
			state:                         state,
		}
	}
	backend := newDistributedExecutionBackend(targets, 5*time.Second, nil)
	// A live run context must not hide the caller's shorter shutdown/API
	// deadline.
	test := &ManagedTest{ID: "parallel-stop", Status: "STOPPING", executionCtx: context.Background(), remoteClientsByTarget: clients}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := backend.StopTestContext(ctx, runner, test)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("expected parent deadline error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("parallel stop exceeded parent deadline: %s", elapsed)
	}
	state.mu.Lock()
	maxActive := state.maxActive
	state.mu.Unlock()
	if maxActive != len(targets) {
		t.Fatalf("maximum concurrent remote stops = %d, want %d", maxActive, len(targets))
	}
}

func TestWholeRunControlsHoldStableDistributedTopology(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    string
		configure func(*lifecycleTransitionBackend, chan struct{}, <-chan struct{})
		invoke    func(*TestRunner, string) error
	}{
		{
			name:   "pause",
			status: "RUNNING",
			configure: func(backend *lifecycleTransitionBackend, entered chan struct{}, release <-chan struct{}) {
				backend.pauseHook = func(*TestRunner, *ManagedTest) { close(entered); <-release }
			},
			invoke: func(runner *TestRunner, id string) error { return runner.PauseTest(id) },
		},
		{
			name:   "resume",
			status: "PAUSED",
			configure: func(backend *lifecycleTransitionBackend, entered chan struct{}, release <-chan struct{}) {
				backend.resumeHook = func(*TestRunner, *ManagedTest) { close(entered); <-release }
			},
			invoke: func(runner *TestRunner, id string) error { return runner.ResumeTest(id) },
		},
		{
			name:   "duration",
			status: "RUNNING",
			configure: func(backend *lifecycleTransitionBackend, entered chan struct{}, release <-chan struct{}) {
				backend.durationHook = func(*TestRunner, *ManagedTest) { close(entered); <-release }
			},
			invoke: func(runner *TestRunner, id string) error { return runner.AdjustDuration(id, 2*time.Minute) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			backend := &lifecycleTransitionBackend{}
			entered := make(chan struct{})
			release := make(chan struct{})
			tc.configure(backend, entered, release)
			runner.setExecutionBackend(backend)
			test := &ManagedTest{
				ID: "topology-control-" + tc.name, Status: tc.status,
				Duration: time.Minute, DurationStr: time.Minute.String(),
				executionBackend: backend,
			}
			if tc.status == "PAUSED" {
				pausedAt := time.Now().UTC()
				test.PausedAt = &pausedAt
			}
			runner.tests[test.ID] = test

			controlDone := make(chan error, 1)
			go func() { controlDone <- tc.invoke(runner, test.ID) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("control fan-out did not start")
			}

			topologyAcquired := make(chan struct{})
			topologyRelease := make(chan struct{})
			go func() {
				unlock := runner.lockDistributedTopologyControl(test.ID)
				close(topologyAcquired)
				<-topologyRelease
				unlock()
			}()
			select {
			case <-topologyAcquired:
				t.Fatal("topology replacement overlapped whole-run control fan-out")
			case <-time.After(50 * time.Millisecond):
			}

			close(release)
			if err := <-controlDone; err != nil {
				t.Fatalf("control returned error: %v", err)
			}
			select {
			case <-topologyAcquired:
			case <-time.After(time.Second):
				t.Fatal("topology replacement did not proceed after control commit")
			}
			close(topologyRelease)
		})
	}
}
