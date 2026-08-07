package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// startupAssignmentFakeClient records what a generator was asked to do during
// distributed startup and lets a test control when each RPC returns.
type startupAssignmentFakeClient struct {
	mu            sync.Mutex
	distributions []generatorrpc.Script
	assignments   []generatorrpc.Assignment

	// beforeAssign runs outside the lock before each AssignWork returns. It is
	// how tests build barriers or force completion orders.
	beforeAssign func(generatorrpc.Assignment) error
	assignResult generatorrpc.AssignmentResult
	distributeFn func(generatorrpc.Script) error
}

func (c *startupAssignmentFakeClient) DistributeScript(_ context.Context, script generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	c.mu.Lock()
	c.distributions = append(c.distributions, script)
	c.mu.Unlock()
	if c.distributeFn != nil {
		if err := c.distributeFn(script); err != nil {
			return generatorrpc.ScriptResult{}, err
		}
	}
	return generatorrpc.ScriptResult{Cached: true}, nil
}

func (c *startupAssignmentFakeClient) AssignWork(_ context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	c.mu.Lock()
	c.assignments = append(c.assignments, assignment)
	c.mu.Unlock()
	if c.beforeAssign != nil {
		if err := c.beforeAssign(assignment); err != nil {
			return generatorrpc.AssignmentResult{}, err
		}
	}
	result := c.assignResult
	if result == (generatorrpc.AssignmentResult{}) {
		result = generatorrpc.AssignmentResult{Accepted: true}
	}
	return result, nil
}

func (c *startupAssignmentFakeClient) AdjustVUsers(context.Context, generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	return generatorrpc.AdjustmentResult{Accepted: true}, nil
}

func (c *startupAssignmentFakeClient) AdjustDuration(context.Context, generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error) {
	return generatorrpc.DurationResult{Accepted: true}, nil
}

func (c *startupAssignmentFakeClient) OpenMetricsStream(context.Context) (*generatorrpc.MetricsStream, error) {
	return nil, errors.New("metrics stream unavailable in test")
}

func (c *startupAssignmentFakeClient) OpenHeartbeat(context.Context) (*generatorrpc.HeartbeatStream, error) {
	return nil, errors.New("heartbeat unavailable in test")
}

func (c *startupAssignmentFakeClient) StopTest(context.Context, string, bool) (generatorrpc.StopResult, error) {
	return generatorrpc.StopResult{Acknowledged: true}, nil
}

func (c *startupAssignmentFakeClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, errors.New("archive unavailable in test")
}

func (c *startupAssignmentFakeClient) Close() error { return nil }

func (c *startupAssignmentFakeClient) recordedAssignments() []generatorrpc.Assignment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]generatorrpc.Assignment(nil), c.assignments...)
}

func (c *startupAssignmentFakeClient) distributionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.distributions)
}

func TestPlanDistributedGroupTargetAssignmentsBucketsByGeneratorInPlacementOrder(t *testing.T) {
	clients := map[string]generatorClient{
		"gen-a": &startupAssignmentFakeClient{},
		"gen-b": &startupAssignmentFakeClient{},
	}
	shards := []GeneratorShardSummary{
		{Group: "checkout", Target: "gen-b", VUsers: 4, ShardOrdinal: 1, ShardCount: 3},
		{Group: "checkout", Target: "", VUsers: 0},
		{Group: "checkout", Target: "gen-a", VUsers: 3, ShardOrdinal: 0, ShardCount: 3},
		{Group: "checkout", Target: "gen-b", VUsers: 2, ShardOrdinal: 2, ShardCount: 3},
	}
	distributed := map[string]map[string]struct{}{"gen-a": {"script-1": struct{}{}}}

	plans, err := planDistributedGroupTargetAssignments(shards, clients, distributed, "script-1")
	if err != nil {
		t.Fatalf("planDistributedGroupTargetAssignments: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("plans = %d, want 2 (one per generator)", len(plans))
	}
	if plans[0].target != "gen-b" || plans[1].target != "gen-a" {
		t.Fatalf("plan order = %q,%q, want first-appearance order gen-b,gen-a", plans[0].target, plans[1].target)
	}
	if len(plans[0].shards) != 2 || plans[0].shards[0].ShardOrdinal != 1 || plans[0].shards[1].ShardOrdinal != 2 {
		t.Fatalf("gen-b shards = %#v, want both gen-b shards in placement order", plans[0].shards)
	}
	if !plans[0].distributeScript {
		t.Fatal("gen-b has not received the script yet and must be staged")
	}
	if plans[1].distributeScript {
		t.Fatal("gen-a already holds the script and must not be re-staged")
	}
}

func TestPlanDistributedGroupTargetAssignmentsRejectsUnconnectedGenerator(t *testing.T) {
	plans, err := planDistributedGroupTargetAssignments(
		[]GeneratorShardSummary{{Target: "gen-a", VUsers: 1}, {Target: "gen-missing", VUsers: 1}},
		map[string]generatorClient{"gen-a": &startupAssignmentFakeClient{}},
		nil,
		"script-1",
	)
	if err == nil || plans != nil {
		t.Fatalf("plans = %#v err = %v, want unconnected-generator rejection", plans, err)
	}
	if got := err.Error(); got != `selected generator "gen-missing" is not connected` {
		t.Fatalf("error = %q, want the unconnected generator message", got)
	}
}

// TestAssignDistributedGroupTargetsStagesGeneratorsConcurrently fails against a
// sequential startup: every generator's AssignWork blocks until the whole fleet
// has arrived, which can only happen if the controller stages them in parallel.
func TestAssignDistributedGroupTargetsStagesGeneratorsConcurrently(t *testing.T) {
	const fleetSize = 4
	var (
		barrierMu sync.Mutex
		arrived   int
		released  = make(chan struct{})
	)
	barrier := func(generatorrpc.Assignment) error {
		barrierMu.Lock()
		arrived++
		if arrived == fleetSize {
			close(released)
		}
		barrierMu.Unlock()
		select {
		case <-released:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("fleet never arrived at the startup barrier")
		}
	}

	plans := make([]distributedTargetAssignmentPlan, 0, fleetSize)
	clients := make([]*startupAssignmentFakeClient, 0, fleetSize)
	for index := range fleetSize {
		client := &startupAssignmentFakeClient{beforeAssign: barrier}
		clients = append(clients, client)
		plans = append(plans, distributedTargetAssignmentPlan{
			target:           fmt.Sprintf("gen-%d", index),
			client:           client,
			distributeScript: true,
			shards: []GeneratorShardSummary{{
				Group:         "checkout",
				Target:        fmt.Sprintf("gen-%d", index),
				VUsers:        index + 1,
				ShardOrdinal:  uint32(index),
				ShardCount:    fleetSize,
				VUserIDOffset: uint64(index) * 1000,
			}},
		})
	}

	backend := newDistributedExecutionBackend(nil, 30*time.Second, nil)
	var startedMu sync.Mutex
	started := make([]string, 0, fleetSize)
	outcomes := backend.assignDistributedGroupTargets(
		context.Background(),
		plans,
		generatorrpc.Script{ID: "script-1", ContentHash: "hash-1"},
		generatorrpc.Assignment{TestID: "run-1", GroupName: "checkout", ScriptID: "script-1"},
		"Checkout",
		func() bool { return true },
		func(target string) {
			startedMu.Lock()
			started = append(started, target)
			startedMu.Unlock()
		},
	)

	if len(outcomes) != fleetSize {
		t.Fatalf("outcomes = %d, want %d", len(outcomes), fleetSize)
	}
	for index, outcome := range outcomes {
		if outcome.err != nil || outcome.notStartable {
			t.Fatalf("outcome %d = err:%v notStartable:%v, want a clean parallel assignment", index, outcome.err, outcome.notStartable)
		}
		if outcome.target != plans[index].target {
			t.Fatalf("outcome %d target = %q, want %q (results must stay in plan order)", index, outcome.target, plans[index].target)
		}
		if !outcome.scriptDistributed || clients[index].distributionCount() != 1 {
			t.Fatalf("generator %d script staging = outcome:%v calls:%d, want exactly one staging call", index, outcome.scriptDistributed, clients[index].distributionCount())
		}
		recorded := clients[index].recordedAssignments()
		if len(recorded) != 1 {
			t.Fatalf("generator %d assignments = %d, want 1", index, len(recorded))
		}
		// Shard coordinates are decided before fan-out, so they must not depend on
		// which generator answered first.
		if recorded[0].ShardOrdinal != uint32(index) || recorded[0].ShardCount != fleetSize || recorded[0].VUserIDOffset != uint64(index)*1000 || recorded[0].VUserCount != int32(index+1) {
			t.Fatalf("generator %d assignment coordinates = %#v, want deterministic shard %d", index, recorded[0], index)
		}
	}
	startedMu.Lock()
	sort.Strings(started)
	startedMu.Unlock()
	if len(started) != fleetSize {
		t.Fatalf("attempted targets = %v, want every generator tracked before its AssignWork", started)
	}
}

// TestAssignDistributedGroupTargetsReportsFirstFailureInPlacementOrder pins the
// determinism requirement of the parallel path: the reported failure is the
// first in placement order, not whichever RPC failed first in wall-clock time.
func TestAssignDistributedGroupTargetsReportsFirstFailureInPlacementOrder(t *testing.T) {
	fastFailure := errors.New("fast generator refused")
	slowFailure := errors.New("slow generator refused")
	slowStarted := make(chan struct{})
	fastDone := make(chan struct{})

	// The rendezvous below only completes when the two refusals overlap, which
	// requires a parallel fan-out. Every wait is bounded so a reverted (or
	// single-worker) fan-out fails this test with a diagnosis instead of
	// deadlocking until the package-wide 10-minute panic.
	errNotParallel := errors.New("startup fan-out is not parallel: the two refusing generators never overlapped")
	slow := &startupAssignmentFakeClient{beforeAssign: func(generatorrpc.Assignment) error {
		close(slowStarted)
		select {
		case <-fastDone:
		case <-time.After(5 * time.Second):
			return errNotParallel
		}
		return slowFailure
	}}
	fast := &startupAssignmentFakeClient{beforeAssign: func(generatorrpc.Assignment) error {
		select {
		case <-slowStarted:
		case <-time.After(5 * time.Second):
			return errNotParallel
		}
		defer close(fastDone)
		return fastFailure
	}}
	healthy := &startupAssignmentFakeClient{}

	plans := []distributedTargetAssignmentPlan{
		{target: "gen-slow", client: slow, shards: []GeneratorShardSummary{{Target: "gen-slow", VUsers: 1}}},
		{target: "gen-fast", client: fast, shards: []GeneratorShardSummary{{Target: "gen-fast", VUsers: 1}}},
		{target: "gen-healthy", client: healthy, shards: []GeneratorShardSummary{{Target: "gen-healthy", VUsers: 1}}},
	}

	backend := newDistributedExecutionBackend(nil, 30*time.Second, nil)
	var startedMu sync.Mutex
	started := map[string]struct{}{}
	outcomes := backend.assignDistributedGroupTargets(
		context.Background(),
		plans,
		generatorrpc.Script{ID: "script-1"},
		generatorrpc.Assignment{TestID: "run-1", GroupName: "checkout"},
		"Checkout",
		func() bool { return true },
		func(target string) {
			startedMu.Lock()
			started[target] = struct{}{}
			startedMu.Unlock()
		},
	)

	var firstErr error
	for _, outcome := range outcomes {
		if outcome.err != nil {
			firstErr = outcome.err
			break
		}
	}
	if firstErr == nil {
		t.Fatal("no failure was reported for a fleet with two refusing generators")
	}
	want := `assign work for group "checkout" on "gen-slow": slow generator refused`
	if firstErr.Error() != want {
		t.Fatalf("reported failure = %q, want the first failure in placement order %q", firstErr.Error(), want)
	}
	if outcomes[1].err == nil || outcomes[2].err != nil {
		t.Fatalf("outcomes = %#v, want the fast refusal recorded and the healthy generator clean", outcomes)
	}
	startedMu.Lock()
	defer startedMu.Unlock()
	if len(started) != 3 {
		t.Fatalf("attempted targets = %v, want all three tracked so startup rollback can stop them", started)
	}
}

func TestAssignDistributedGroupTargetsBoundsFanoutConcurrency(t *testing.T) {
	const fleetSize = maxDistributedStartupAssignmentWorkers * 3
	var (
		mu       sync.Mutex
		inFlight int
		peak     int
	)
	track := func(generatorrpc.Assignment) error {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}

	plans := make([]distributedTargetAssignmentPlan, 0, fleetSize)
	for index := range fleetSize {
		target := fmt.Sprintf("gen-%03d", index)
		plans = append(plans, distributedTargetAssignmentPlan{
			target: target,
			client: &startupAssignmentFakeClient{beforeAssign: track},
			shards: []GeneratorShardSummary{{Target: target, VUsers: 1}},
		})
	}

	backend := newDistributedExecutionBackend(nil, 30*time.Second, nil)
	outcomes := backend.assignDistributedGroupTargets(
		context.Background(),
		plans,
		generatorrpc.Script{ID: "script-1"},
		generatorrpc.Assignment{TestID: "run-1", GroupName: "checkout"},
		"Checkout",
		nil,
		nil,
	)
	for index, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("outcome %d error = %v", index, outcome.err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > maxDistributedStartupAssignmentWorkers {
		t.Fatalf("peak concurrent generator assignments = %d, limit %d", peak, maxDistributedStartupAssignmentWorkers)
	}
	if peak < 2 {
		t.Fatalf("peak concurrent generator assignments = %d, want a genuinely parallel fan-out", peak)
	}
}

func TestAssignDistributedGroupTargetsStopsAtRunAbort(t *testing.T) {
	client := &startupAssignmentFakeClient{}
	plans := []distributedTargetAssignmentPlan{{
		target:           "gen-a",
		client:           client,
		distributeScript: true,
		shards:           []GeneratorShardSummary{{Target: "gen-a", VUsers: 1}},
	}}
	backend := newDistributedExecutionBackend(nil, 30*time.Second, nil)
	outcomes := backend.assignDistributedGroupTargets(
		context.Background(),
		plans,
		generatorrpc.Script{ID: "script-1"},
		generatorrpc.Assignment{TestID: "run-1", GroupName: "checkout"},
		"Checkout",
		func() bool { return false },
		nil,
	)
	if len(outcomes) != 1 || !outcomes[0].notStartable || outcomes[0].err != nil {
		t.Fatalf("outcomes = %#v, want a not-startable abort with no error", outcomes)
	}
	if client.distributionCount() != 0 || len(client.recordedAssignments()) != 0 {
		t.Fatalf("aborted startup still issued RPCs: distributions=%d assignments=%d", client.distributionCount(), len(client.recordedAssignments()))
	}
}
