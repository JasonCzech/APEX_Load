package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type evacuationFakeGeneratorClient struct {
	target   string
	mu       sync.Mutex
	adjust   map[string]int32
	stops    int
	closes   int
	stopHook func()
}

type faultEvacuationGeneratorClient struct {
	*evacuationFakeGeneratorClient
	muFault                   sync.Mutex
	activeVUsers              int32
	adjustCalls               int
	stopCalls                 int
	loseForwardAdjustResponse bool
	failRollbackAdjustments   bool
	loseInitialStopResponses  int
	lostStopResponseApplies   bool
}

type commitFailingEvacuationClient struct {
	generatorClient
	commitCalls atomic.Int32
}

type stagedEvacuationFaultClient struct {
	generatorClient
	mu                  sync.Mutex
	assignCalls         int
	scenarioIDs         []string
	stopCalls           int
	loseAssignResponses int
	loseStopResponses   int
}

type setupRecordingEvacuationClient struct {
	*generatorrpc.Client
	assignment          generatorrpc.Assignment
	beforeAssign        func()
	beforeCommit        func() error
	commitCalls         atomic.Int32
	setupTimeout        atomic.Int64
	durationMu          sync.Mutex
	durationAdjustments []generatorrpc.DurationAdjustment
}

func (c *setupRecordingEvacuationClient) CommitWorkSupervisedAt(ctx context.Context, testID string, generation uint64, startAt time.Time, token []byte) (generatorrpc.WorkCommitResult, error) {
	c.commitCalls.Add(1)
	if c.beforeCommit != nil {
		if err := c.beforeCommit(); err != nil {
			return generatorrpc.WorkCommitResult{}, err
		}
	}
	return c.Client.CommitWorkSupervisedAt(ctx, testID, generation, startAt, token)
}

func (c *setupRecordingEvacuationClient) AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	if c.beforeAssign != nil {
		c.beforeAssign()
	}
	c.assignment = assignment
	return c.Client.AssignWork(ctx, assignment)
}

type supervisedEvacuationSource struct{ *evacuationFakeGeneratorClient }

func (c *supervisedEvacuationSource) StopTestAtGenerationWithToken(ctx context.Context, id string, graceful bool, generation uint64, _ []byte) (generatorrpc.StopResult, error) {
	return c.StopTestAtGeneration(ctx, id, graceful, generation)
}

type terminalObservingEvacuationClient struct {
	*generatorrpc.Client
	runner   *TestRunner
	testID   string
	target   string
	ackDelay time.Duration
}

func (c *terminalObservingEvacuationClient) StopTest(ctx context.Context, testID string, graceful bool) (generatorrpc.StopResult, error) {
	result, err := c.Client.StopTest(ctx, testID, graceful)
	if err != nil {
		return result, err
	}
	return result, c.waitForTerminalBatch(ctx)
}

func (c *terminalObservingEvacuationClient) StopTestAtGeneration(ctx context.Context, testID string, graceful bool, generation uint64) (generatorrpc.StopResult, error) {
	result, err := c.Client.StopTestAtGeneration(ctx, testID, graceful, generation)
	if err != nil {
		return result, err
	}
	if err := c.waitForTerminalBatch(ctx); err != nil {
		return result, err
	}
	if c.ackDelay > 0 {
		timer := time.NewTimer(c.ackDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	return result, nil
}

func (c *terminalObservingEvacuationClient) waitForTerminalBatch(ctx context.Context) error {
	for {
		c.runner.mu.RLock()
		managed := c.runner.tests[c.testID]
		observed := managed != nil && isTerminalRemoteLifecycle(managed.remoteMetricsBatchesByTarget[c.target].LifecycleState)
		c.runner.mu.RUnlock()
		if observed {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("controller did not observe the source terminal batch before stop acknowledgement: %w", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

func (c *setupRecordingEvacuationClient) OpenMetricsStreamWithSetupTimeoutForTestGeneration(parent context.Context, timeout time.Duration, testID string, generation uint64) (*generatorrpc.MetricsStream, error) {
	c.setupTimeout.Store(int64(timeout))
	return c.Client.OpenMetricsStreamWithSetupTimeoutForTestGeneration(parent, timeout, testID, generation)
}

func (c *setupRecordingEvacuationClient) AdjustDuration(ctx context.Context, adjustment generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error) {
	c.durationMu.Lock()
	c.durationAdjustments = append(c.durationAdjustments, adjustment)
	c.durationMu.Unlock()
	return c.Client.AdjustDuration(ctx, adjustment)
}

func (c *setupRecordingEvacuationClient) recordedDurationAdjustments() []generatorrpc.DurationAdjustment {
	c.durationMu.Lock()
	defer c.durationMu.Unlock()
	return append([]generatorrpc.DurationAdjustment(nil), c.durationAdjustments...)
}

func (c *stagedEvacuationFaultClient) AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	result, err := c.generatorClient.AssignWork(ctx, assignment)
	c.mu.Lock()
	c.assignCalls++
	c.scenarioIDs = append(c.scenarioIDs, assignment.ScenarioID)
	call := c.assignCalls
	c.mu.Unlock()
	if err == nil && call <= c.loseAssignResponses {
		return result, context.DeadlineExceeded
	}
	return result, err
}

func (c *stagedEvacuationFaultClient) StopTest(ctx context.Context, testID string, graceful bool) (generatorrpc.StopResult, error) {
	c.mu.Lock()
	c.stopCalls++
	call := c.stopCalls
	c.mu.Unlock()
	if call <= c.loseStopResponses {
		return generatorrpc.StopResult{}, context.DeadlineExceeded
	}
	return c.generatorClient.StopTest(ctx, testID, graceful)
}

func (c *stagedEvacuationFaultClient) CommitWorkAt(ctx context.Context, testID string, executionGeneration uint64, startAt time.Time) (generatorrpc.WorkCommitResult, error) {
	committer, ok := c.generatorClient.(coordinatedWorkCommitClient)
	if !ok {
		return generatorrpc.WorkCommitResult{}, errors.New("wrapped generator does not support coordinated commit")
	}
	return committer.CommitWorkAt(ctx, testID, executionGeneration, startAt)
}

func (c *stagedEvacuationFaultClient) stops() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopCalls
}

func (c *stagedEvacuationFaultClient) assignedScenarioIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.scenarioIDs...)
}

func (c *commitFailingEvacuationClient) CommitWork(context.Context, string, uint64) (generatorrpc.WorkCommitResult, error) {
	c.commitCalls.Add(1)
	return generatorrpc.WorkCommitResult{}, context.DeadlineExceeded
}

func (c *commitFailingEvacuationClient) CommitWorkAt(context.Context, string, uint64, time.Time) (generatorrpc.WorkCommitResult, error) {
	c.commitCalls.Add(1)
	return generatorrpc.WorkCommitResult{}, context.DeadlineExceeded
}

func (c *commitFailingEvacuationClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, owner string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	claiming, ok := c.generatorClient.(executionEpochGeneratorClient)
	if !ok {
		return generatorrpc.ExecutionEpochResult{}, errors.New("wrapped generator does not support execution epochs")
	}
	return claiming.ClaimExecutionEpoch(ctx, generation, owner, ttl)
}

func (c *commitFailingEvacuationClient) ClaimTestExecutionEpoch(ctx context.Context, testID string, generation uint64, owner string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	claiming, ok := c.generatorClient.(testExecutionEpochGeneratorClient)
	if !ok {
		return generatorrpc.ExecutionEpochResult{}, errors.New("wrapped generator does not support test execution leases")
	}
	return claiming.ClaimTestExecutionEpoch(ctx, testID, generation, owner, ttl)
}

func (c *commitFailingEvacuationClient) StopTestAtGeneration(ctx context.Context, testID string, graceful bool, generation uint64) (generatorrpc.StopResult, error) {
	stopping, ok := c.generatorClient.(executionGenerationStopClient)
	if !ok {
		return generatorrpc.StopResult{}, errors.New("wrapped generator does not support execution-fenced stop")
	}
	return stopping.StopTestAtGeneration(ctx, testID, graceful, generation)
}

func (c *commitFailingEvacuationClient) OpenMetricsStreamWithSetupTimeoutForTestGeneration(ctx context.Context, timeout time.Duration, testID string, generation uint64) (*generatorrpc.MetricsStream, error) {
	streaming, ok := c.generatorClient.(executionTestSetupMetricsClient)
	if !ok {
		return nil, errors.New("wrapped generator does not support test-scoped metrics streams")
	}
	return streaming.OpenMetricsStreamWithSetupTimeoutForTestGeneration(ctx, timeout, testID, generation)
}

func newFaultEvacuationGeneratorClient(target string, active int32) *faultEvacuationGeneratorClient {
	return &faultEvacuationGeneratorClient{
		evacuationFakeGeneratorClient: newEvacuationFakeGeneratorClient(target),
		activeVUsers:                  active,
	}
}

func (c *faultEvacuationGeneratorClient) AdjustVUsers(_ context.Context, adjustment generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	c.muFault.Lock()
	defer c.muFault.Unlock()
	c.adjustCalls++
	if c.loseForwardAdjustResponse && c.adjustCalls == 1 {
		c.activeVUsers = adjustment.TargetVUsers
		return generatorrpc.AdjustmentResult{}, context.DeadlineExceeded
	}
	if c.failRollbackAdjustments && c.adjustCalls > 1 {
		return generatorrpc.AdjustmentResult{}, context.DeadlineExceeded
	}
	c.activeVUsers = adjustment.TargetVUsers
	return generatorrpc.AdjustmentResult{Accepted: true, CurrentVUsers: adjustment.TargetVUsers}, nil
}

func (c *faultEvacuationGeneratorClient) StopTest(context.Context, string, bool) (generatorrpc.StopResult, error) {
	c.muFault.Lock()
	defer c.muFault.Unlock()
	c.stopCalls++
	if c.stopCalls <= c.loseInitialStopResponses {
		if c.lostStopResponseApplies {
			c.activeVUsers = 0
		}
		return generatorrpc.StopResult{}, context.DeadlineExceeded
	}
	c.activeVUsers = 0
	return generatorrpc.StopResult{Acknowledged: true}, nil
}

func (c *faultEvacuationGeneratorClient) StopTestAtGeneration(ctx context.Context, testID string, graceful bool, _ uint64) (generatorrpc.StopResult, error) {
	return c.StopTest(ctx, testID, graceful)
}

func (c *faultEvacuationGeneratorClient) active() int32 {
	c.muFault.Lock()
	defer c.muFault.Unlock()
	return c.activeVUsers
}

func (c *faultEvacuationGeneratorClient) faultStops() int {
	c.muFault.Lock()
	defer c.muFault.Unlock()
	return c.stopCalls
}

func newEvacuationFakeGeneratorClient(target string) *evacuationFakeGeneratorClient {
	return &evacuationFakeGeneratorClient{target: target, adjust: make(map[string]int32)}
}

func TestDistributedEvacuationRemainingDurationExcludesStartupAndPauseTime(t *testing.T) {
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	started := now.Add(-12 * time.Minute)
	test := &ManagedTest{
		Duration:         10 * time.Minute,
		StartedAt:        &started,
		StartDelay:       "2m",
		ResourceBaseline: true,
		CumulativePause:  time.Minute,
	}
	// 12m wall - 2m delay - 5m resource baseline - 1m pause = 4m
	// steady-state elapsed, leaving 6m of the requested target duration.
	remaining, elapsed, preRemaining := distributedEvacuationTiming(test, now)
	if want := 6 * time.Minute; remaining != want {
		t.Fatalf("remaining duration = %s, want %s", remaining, want)
	}
	if elapsed != 4*time.Minute || preRemaining != 0 {
		t.Fatalf("timing = elapsed:%s pre_remaining:%s, want 4m/0", elapsed, preRemaining)
	}

	started = now.Add(-30 * time.Minute)
	remaining, _, _ = distributedEvacuationTiming(test, now)
	if remaining != 0 {
		t.Fatalf("exhausted duration = %s, want zero", remaining)
	}

	started = now.Add(-8 * time.Minute)
	pausedAt := now.Add(-3 * time.Minute)
	test.StartedAt = &started
	test.CumulativePause = time.Minute
	test.PausedAt = &pausedAt
	// 8m wall - 2m delay - 5m resource baseline - 1m completed pause - 3m
	// current pause leaves no active steady-state time and the full target.
	remaining, elapsed, preRemaining = distributedEvacuationTiming(test, now)
	if remaining != test.Duration || elapsed != 0 || preRemaining != 0 {
		t.Fatalf("ongoing pause timing = remaining:%s elapsed:%s pre:%s, want %s/0/0", remaining, elapsed, preRemaining, test.Duration)
	}
}

func TestGeneratorSupportsEvacuationAssignmentStateRequiresExplicitCapability(t *testing.T) {
	tests := []struct {
		name     string
		snapshot generatorLoadSnapshot
		want     bool
	}{
		{name: "healthy current generator", snapshot: generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{"http", generatorrpc.FeatureEvacuationAssignmentStateV1}}, want: true},
		{name: "rolling upgrade legacy generator", snapshot: generatorLoadSnapshot{HeartbeatOK: true, CompatibleFeatures: []string{"http"}}},
		{name: "unhealthy current generator", snapshot: generatorLoadSnapshot{HeartbeatOK: false, CompatibleFeatures: []string{generatorrpc.FeatureEvacuationAssignmentStateV1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := generatorSupportsEvacuationAssignmentState(tc.snapshot); got != tc.want {
				t.Fatalf("generatorSupportsEvacuationAssignmentState() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDistributedShardRunGoalsRequireSingleGenerator(t *testing.T) {
	twoShards := []GeneratorShardSummary{{Target: "gen-a"}, {Target: "gen-b"}}
	tests := []struct {
		name  string
		group TestGroup
		want  bool
	}{
		{name: "global iterations from effective snapshot", group: TestGroup{RunLogicGoals: &RunLogicGoalSnapshot{GlobalIterations: 10}}, want: true},
		{name: "transaction cap from script", group: TestGroup{script: &dsl.Script{Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{MaxTransactions: 10}}}}, want: true},
		{name: "duration driven", group: TestGroup{}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDistributedShardRunGoals(tc.group, twoShards)
			if (err != nil) != tc.want {
				t.Fatalf("validateDistributedShardRunGoals() error = %v, want error %v", err, tc.want)
			}
			if err := validateDistributedShardRunGoals(tc.group, twoShards[:1]); err != nil {
				t.Fatalf("single shard rejected: %v", err)
			}
		})
	}
}

func TestDistributedEvacuationTimingReportsRemainingPreBaseline(t *testing.T) {
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	started := now.Add(-90 * time.Second)
	test := &ManagedTest{Duration: 10 * time.Minute, StartedAt: &started, StartDelay: "2m"}
	remaining, elapsed, preRemaining := distributedEvacuationTiming(test, now)
	if remaining != test.Duration || elapsed != 0 || preRemaining != 30*time.Second {
		t.Fatalf("timing = remaining:%s elapsed:%s pre:%s", remaining, elapsed, preRemaining)
	}
}

func TestDistributedEvacuationRemainingRamp(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		want    time.Duration
	}{
		{name: "start", elapsed: 0, want: 5 * time.Minute},
		{name: "mid-ramp", elapsed: 2 * time.Minute, want: 3 * time.Minute},
		{name: "steady", elapsed: 7 * time.Minute, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := distributedEvacuationRemainingRamp(5*time.Minute, tc.elapsed); got != tc.want {
				t.Fatalf("remaining ramp = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDistributedEvacuationRefusesCursorStatefulDataSources(t *testing.T) {
	for _, strategy := range []string{"", "sequential", "pop"} {
		t.Run(firstNonEmpty(strategy, "default-sequential"), func(t *testing.T) {
			script := &dsl.Script{DataSources: []dsl.DataSource{{
				Name:       "accounts",
				Type:       "csv",
				InlineData: "id\n1\n2\n",
				Strategy:   strategy,
			}}}
			allowed, reason := groupAllowsStatelessDataReshard(TestGroup{Name: "buyers", script: script})
			if allowed || reason == "" {
				t.Fatalf("strategy %q allowed=%v reason=%q", strategy, allowed, reason)
			}
		})
	}

	for _, strategy := range []string{"unique", "random"} {
		script := &dsl.Script{DataSources: []dsl.DataSource{{Name: "accounts", Type: "csv", Strategy: strategy}}}
		if allowed, reason := groupAllowsStatelessDataReshard(TestGroup{script: script}); !allowed {
			t.Fatalf("strategy %q was refused: %s", strategy, reason)
		}
	}
}

func TestDistributedEvacuationRefusesUncheckpointedBoundedAndParameterState(t *testing.T) {
	tests := []struct {
		name   string
		script *dsl.Script
	}{
		{name: "per-vuser iterations", script: &dsl.Script{Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 2}}}},
		{name: "global iterations", script: &dsl.Script{Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{GlobalIterations: 20}}}},
		{name: "transaction cap", script: &dsl.Script{Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{MaxTransactions: 20}}}},
		{name: "iteration counter parameter", script: &dsl.Script{Parameters: []dsl.Parameter{{Name: "iteration", Type: "current_iteration"}}}},
		{name: "advancing list parameter", script: &dsl.Script{Parameters: []dsl.Parameter{{Name: "account", Type: "list", Values: []string{"a", "b"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, reason := groupAllowsGeneratorLossReshard(TestGroup{Protocol: "http", script: tt.script})
			if allowed || !strings.Contains(reason, "checkpoint") {
				t.Fatalf("allowed=%v reason=%q, want checkpoint refusal", allowed, reason)
			}
		})
	}

	safe := &dsl.Script{Parameters: []dsl.Parameter{{
		Name: "account", Type: "list", Values: []string{"a", "b"}, ValueForEachVUser: true,
	}}}
	if allowed, reason := groupAllowsGeneratorLossReshard(TestGroup{Protocol: "http", script: safe}); !allowed {
		t.Fatalf("fixed per-VUser parameter was refused: %s", reason)
	}
}

func TestDistributedEvacuationRefusesUnsafeLifecycleWindows(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name       string
		started    time.Time
		duration   time.Duration
		startDelay string
	}{
		{name: "pre-baseline", started: now, duration: time.Minute, startDelay: "30s"},
		{name: "steady-exhausted", started: now.Add(-2 * time.Minute), duration: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newDistributedExecutionBackend([]string{"gen-a"}, 50*time.Millisecond, nil)
			source := newEvacuationFakeGeneratorClient("gen-a")
			test := &ManagedTest{
				ID:         "unsafe-" + tc.name,
				Status:     "RUNNING",
				Duration:   tc.duration,
				StartDelay: tc.startDelay,
				StartedAt:  &tc.started,
				Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
				Groups:     []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 1, script: &dsl.Script{Protocol: "http"}}},
				remoteGroupShards: map[string][]GeneratorShardSummary{
					"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 1}},
				},
				remoteClientsByTarget: map[string]generatorClient{"gen-a": source},
				remoteTargetStates:    map[string]string{"gen-a": "RUNNING"},
			}
			runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}
			impacts, err := backend.EvacuateGenerator(runner, test, "gen-a", true, "review")
			if err != nil {
				t.Fatalf("dry-run evacuation: %v", err)
			}
			if len(impacts) != 1 || !impacts[0].Refused || impacts[0].RefusalReason == "" {
				t.Fatalf("impacts = %#v, want lifecycle refusal", impacts)
			}
		})
	}
}

func (c *evacuationFakeGeneratorClient) DistributeScript(context.Context, generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	return generatorrpc.ScriptResult{}, nil
}
func (c *evacuationFakeGeneratorClient) AssignWork(context.Context, generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	return generatorrpc.AssignmentResult{Accepted: true}, nil
}
func (c *evacuationFakeGeneratorClient) AdjustVUsers(_ context.Context, adjustment generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.adjust[adjustment.GroupName] = adjustment.TargetVUsers
	return generatorrpc.AdjustmentResult{Accepted: true, CurrentVUsers: adjustment.TargetVUsers}, nil
}
func (c *evacuationFakeGeneratorClient) AdjustDuration(context.Context, generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error) {
	return generatorrpc.DurationResult{Accepted: true}, nil
}
func (c *evacuationFakeGeneratorClient) OpenMetricsStream(context.Context) (*generatorrpc.MetricsStream, error) {
	return nil, errors.New("metrics stream unavailable in test")
}
func (c *evacuationFakeGeneratorClient) OpenHeartbeat(context.Context) (*generatorrpc.HeartbeatStream, error) {
	return nil, errors.New("heartbeat unavailable in test")
}
func (c *evacuationFakeGeneratorClient) StopTest(context.Context, string, bool) (generatorrpc.StopResult, error) {
	if c.stopHook != nil {
		c.stopHook()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	return generatorrpc.StopResult{Acknowledged: true}, nil
}

func (c *evacuationFakeGeneratorClient) StopTestAtGeneration(ctx context.Context, testID string, graceful bool, _ uint64) (generatorrpc.StopResult, error) {
	return c.StopTest(ctx, testID, graceful)
}
func (c *evacuationFakeGeneratorClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, nil
}
func (c *evacuationFakeGeneratorClient) Close() error {
	c.mu.Lock()
	c.closes++
	c.mu.Unlock()
	return nil
}

func (c *evacuationFakeGeneratorClient) adjusted(group string) int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.adjust[group]
}

func (c *evacuationFakeGeneratorClient) stopCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stops
}

func (c *evacuationFakeGeneratorClient) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

func configureEvacuationExecutionLease(runner *TestRunner, test *ManagedTest, generation uint64, owner string, ttl time.Duration) {
	leadership := newExecutionLeadership(nil, owner)
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = owner
	leadership.durableGeneration = generation
	leadership.localLeaseDeadline = time.Now().Add(ttl)
	leadership.mu.Unlock()
	runner.leadership = leadership
	test.executionGeneration = generation
}

func TestDistributedEvacuateGeneratorRefusesMergeOntoExistingGroupShard(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090", "gen-c:9090"}, time.Second, nil)
	clients := map[string]*evacuationFakeGeneratorClient{
		"gen-a:9090": newEvacuationFakeGeneratorClient("gen-a:9090"),
		"gen-b:9090": newEvacuationFakeGeneratorClient("gen-b:9090"),
		"gen-c:9090": newEvacuationFakeGeneratorClient("gen-c:9090"),
	}
	started := time.Now()
	test := &ManagedTest{
		ID:         "run-1",
		Name:       "Checkout",
		Status:     "RUNNING",
		Duration:   time.Minute,
		StartedAt:  &started,
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 9}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {
				{Group: "buyers", Target: "gen-a:9090", VUsers: 3},
				{Group: "buyers", Target: "gen-b:9090", VUsers: 3},
				{Group: "buyers", Target: "gen-c:9090", VUsers: 3},
			},
		},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-a:9090": clients["gen-a:9090"],
			"gen-b:9090": clients["gen-b:9090"],
			"gen-c:9090": clients["gen-c:9090"],
		},
		remoteTargetStates: map[string]string{
			"gen-a:9090": "RUNNING",
			"gen-b:9090": "RUNNING",
			"gen-c:9090": "RUNNING",
		},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	dryRun, err := backend.EvacuateGenerator(runner, test, "gen-a:9090", true, "dry_run")
	if err != nil {
		t.Fatalf("dry-run evacuate returned error: %v", err)
	}
	if len(dryRun) != 1 || dryRun[0].VUsers != 3 || !dryRun[0].Refused || !strings.Contains(dryRun[0].RefusalReason, "does not already participate in this run") {
		t.Fatalf("dry-run impacts = %+v, want one refusal for an existing run participant", dryRun)
	}
	if got := clients["gen-b:9090"].adjusted("buyers") + clients["gen-c:9090"].adjusted("buyers"); got != 0 {
		t.Fatalf("dry-run adjusted survivors, got %d total", got)
	}
}

func TestDistributedEvacuateGeneratorRefusesReplacementAlreadyHostingDifferentGroup(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	healthyParticipant, _ := newTestGeneratorRPCClient(t, "gen-b", 10)
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, nil)
	started := time.Now()
	test := &ManagedTest{
		ID:         "run-cross-group-participant",
		Name:       "Cross-group participant",
		Status:     "RUNNING",
		Duration:   time.Minute,
		StartedAt:  &started,
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups: []TestGroup{
			{Name: "buyers", Protocol: "http", VUsers: 3},
			{Name: "admins", Protocol: "http", VUsers: 2},
		},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 3}},
			"admins": {{Group: "admins", Target: "gen-b", VUsers: 2}},
		},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-a": newEvacuationFakeGeneratorClient("gen-a"),
			"gen-b": healthyParticipant,
		},
		remoteTargetStates: map[string]string{"gen-a": "RUNNING", "gen-b": "RUNNING"},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	impacts, err := backend.EvacuateGenerator(runner, test, "gen-a", true, "dry_run")
	if err != nil {
		t.Fatalf("dry-run evacuation: %v", err)
	}
	if len(impacts) != 1 || !impacts[0].Refused || !strings.Contains(impacts[0].RefusalReason, "already participate in this run") {
		t.Fatalf("impacts = %+v, want refusal because gen-b already hosts a different group", impacts)
	}
}

func TestDistributedEvacuationResponseLossAndRollbackFailureStopsWholeRun(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	remote, remoteService := newTestGeneratorRPCClient(t, "gen-b", 10)
	replacement := &stagedEvacuationFaultClient{
		generatorClient:     remote,
		loseAssignResponses: 1,
		loseStopResponses:   durableTransitionAttempts,
	}
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 250*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return replacement, nil
	})
	source := newFaultEvacuationGeneratorClient("gen-a", 3)
	started := time.Now()
	script := &dsl.Script{ID: "buyers-script", Name: "Buyers", Protocol: "http"}
	test := &ManagedTest{
		ID:         "run-evacuation-rollback-uncertain",
		ScenarioID: "managed-evacuation-scenario",
		Status:     "RUNNING",
		Duration:   time.Minute,
		StartedAt:  &started,
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 3, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 3}},
		},
		remoteClientsByTarget: map[string]generatorClient{"gen-a": source},
		remoteTargetStates:    map[string]string{"gen-a": "RUNNING"},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	_, err := backend.EvacuateGenerator(runner, test, "gen-a", false, "operator_evacuate")
	var uncertain *distributedEvacuationUncertainError
	if !errors.As(err, &uncertain) {
		t.Fatalf("EvacuateGenerator error = %v, want distributedEvacuationUncertainError", err)
	}
	if test.Status == "RUNNING" {
		t.Fatalf("status = %q after uncertain rollback, want fail-closed state", test.Status)
	}
	if source.active() != 0 || remoteService.ActiveVUsers() != 0 {
		t.Fatalf("active VUsers after fail-closed cleanup = source:%d replacement:%d, want zero", source.active(), remoteService.ActiveVUsers())
	}
	if source.faultStops() == 0 || replacement.stops() <= durableTransitionAttempts {
		t.Fatalf("whole-run cleanup did not stop every participant: source=%d replacement=%d", source.faultStops(), replacement.stops())
	}
	if got := replacement.assignedScenarioIDs(); len(got) == 0 || got[0] != test.ScenarioID {
		t.Fatalf("evacuation assignment scenario identities = %v, want %q", got, test.ScenarioID)
	}
}

func TestFailClosedUncertainEvacuationPreservesStoppedParticipantArchive(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	const testID = "fail-closed-archive"
	sourceDir := t.TempDir()
	write := func(name string, content []byte) string {
		t.Helper()
		path := filepath.Join(sourceDir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	manifestPath := filepath.Join(sourceDir, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testID,
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "summaries.jsonl",
	}); err != nil {
		t.Fatal(err)
	}
	client := &archiveStreamingFakeClient{
		startupLifecycleFakeClient: startupLifecycleFakeClient{accept: true},
		artifacts: map[string]string{
			runArchiveManifestFileName: manifestPath,
			"transactions.jsonl":       write("transactions.jsonl", nil),
			"resources.jsonl":          write("resources.jsonl", nil),
			"summaries.jsonl":          write("summaries.jsonl", nil),
		},
	}
	test := &ManagedTest{
		ID:                    testID,
		Name:                  "Fail-closed evidence",
		Status:                "RUNNING",
		CreatedAt:             time.Now().Add(-time.Minute),
		remoteClientsByTarget: map[string]generatorClient{"gen-a": client},
		remoteTargetStates:    map[string]string{"gen-a": "RUNNING"},
	}
	runner := NewTestRunner(1, nil, nil)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	backend := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil)
	uncertain := &distributedEvacuationUncertainError{
		testID:             test.ID,
		participantClients: map[string]generatorClient{"gen-a": client},
		touchedTargets:     []string{"gen-a"},
		err:                errors.New("replacement commit response was lost"),
	}

	if err := backend.failClosedUncertainEvacuation(runner, test, uncertain, nil, nil); err == nil {
		t.Fatal("expected the evacuation uncertainty to be returned")
	}
	runner.mu.RLock()
	status, archive := test.Status, test.Archive
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status = %q, want FAILED", status)
	}
	if archive == nil {
		t.Fatal("stopped participant archive was not attached")
	}
	if _, err := os.Stat(archive.ManifestPath); err != nil {
		t.Fatalf("attached archive manifest: %v", err)
	}
	client.mu.Lock()
	streamCalls := client.streamCalls
	client.mu.Unlock()
	client.startupLifecycleFakeClient.mu.Lock()
	stopped, closed := client.stopped, client.closed
	client.startupLifecycleFakeClient.mu.Unlock()
	if streamCalls < 4 || stopped == 0 || closed == 0 {
		t.Fatalf("archive lifecycle = streams:%d stopped:%d closed:%d, want fetch after stop before close", streamCalls, stopped, closed)
	}
}

func TestPublicGeneratorLossReasonCannotBypassSourceStopFence(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	replacement, replacementService := newTestGeneratorRPCClient(t, "gen-b", 10)
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 250*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return replacement, nil
	})
	source := newFaultEvacuationGeneratorClient("gen-a", 3)
	// Every source-fencing attempt loses its response. The first fail-closed
	// retry then acknowledges the already-applied stop.
	source.loseInitialStopResponses = durableTransitionAttempts
	source.lostStopResponseApplies = true
	started := time.Now()
	script := &dsl.Script{ID: "buyers-script", Name: "Buyers", Protocol: "http"}
	test := &ManagedTest{
		ID:         "run-evacuation-source-timeout",
		Status:     "RUNNING",
		Duration:   time.Minute,
		StartedAt:  &started,
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: false},
		Groups:     []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 3, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 3}},
		},
		remoteClientsByTarget: map[string]generatorClient{"gen-a": source},
		remoteTargetStates:    map[string]string{"gen-a": "RUNNING"},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}
	configureEvacuationExecutionLease(runner, test, 84, "hostile-reason/84", 2*time.Second)

	startedEvacuation := time.Now()
	_, err := backend.EvacuateGenerator(runner, test, "gen-a", false, "generator_loss")
	if elapsed := time.Since(startedEvacuation); elapsed >= time.Second {
		t.Fatalf("public free-text reason waited for an unauthorized lease-expiry fence: %s", elapsed)
	}
	var uncertain *distributedEvacuationUncertainError
	if !errors.As(err, &uncertain) {
		t.Fatalf("EvacuateGenerator error = %v, want distributedEvacuationUncertainError", err)
	}
	if test.Status == "RUNNING" {
		t.Fatalf("status = %q after ambiguous source stop, want fail-closed state", test.Status)
	}
	if source.active() != 0 || replacementService.ActiveVUsers() != 0 {
		t.Fatalf("active VUsers after source-stop timeout cleanup = source:%d replacement:%d, want zero", source.active(), replacementService.ActiveVUsers())
	}
	if source.faultStops() <= durableTransitionAttempts {
		t.Fatalf("whole-run cleanup attempts = source:%d, want more than %d", source.faultStops(), durableTransitionAttempts)
	}
}

func TestTerminalEvacuationUncertaintyStillRetriesUncommittedParticipantStop(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-c"}, 5*time.Millisecond, nil)
	replacement := newFaultEvacuationGeneratorClient("gen-c", 4)
	replacement.loseInitialStopResponses = durableTransitionAttempts
	test := &ManagedTest{ID: "terminal-evacuation-race", Status: "FAILED", Duration: time.Minute}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.tests[test.ID] = test
	uncertain := &distributedEvacuationUncertainError{
		testID:             test.ID,
		participantClients: map[string]generatorClient{"gen-c": replacement},
		touchedTargets:     []string{"gen-c"},
		err:                errors.New("stale evacuation cleanup was not acknowledged"),
	}

	err := backend.failClosedUncertainEvacuation(runner, test, uncertain, nil, nil)
	if err == nil {
		t.Fatal("expected initial participant stop failure")
	}
	if got := replacement.active(); got != 4 {
		t.Fatalf("replacement active VUsers after failed inline cleanup = %d, want 4 before retry", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for replacement.active() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := replacement.active(); got != 0 {
		t.Fatalf("terminal evacuation cleanup left %d untracked VUsers", got)
	}
	if replacement.faultStops() <= durableTransitionAttempts {
		t.Fatalf("participant stop attempts = %d, want background retry after %d inline attempts", replacement.faultStops(), durableTransitionAttempts)
	}
}

func TestFailClosedGeneratorLossEvacuationTerminalizesWithoutDeadSourceAcknowledgement(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	remote, _ := newTestGeneratorRPCClient(t, "gen-b", 10)
	// The replacement loses its assignment response and then every rollback
	// stop, so the evacuation exits through the uncertain fail-closed path.
	replacement := &stagedEvacuationFaultClient{
		generatorClient:     remote,
		loseAssignResponses: 1,
		loseStopResponses:   durableTransitionAttempts,
	}
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 250*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return replacement, nil
	})
	// The source pod is gone: no exact-generation stop can ever be acknowledged.
	source := newFaultEvacuationGeneratorClient("gen-a", 3)
	source.loseInitialStopResponses = 1 << 30
	started := time.Now()
	script := &dsl.Script{ID: "buyers-script", Name: "Buyers", Protocol: "http"}
	test := &ManagedTest{
		ID:         "run-evacuation-dead-source",
		ScenarioID: "managed-evacuation-scenario",
		Status:     "RUNNING",
		Duration:   time.Minute,
		StartedAt:  &started,
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 3, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 3}},
		},
		remoteClientsByTarget: map[string]generatorClient{"gen-a": source},
		remoteTargetStates:    map[string]string{"gen-a": "RUNNING"},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.tests[test.ID] = test

	_, err := backend.evacuateGenerator(runner, test, "gen-a", false, true)
	var uncertain *distributedEvacuationUncertainError
	if !errors.As(err, &uncertain) {
		t.Fatalf("evacuateGenerator error = %v, want distributedEvacuationUncertainError", err)
	}
	runner.mu.RLock()
	status, failure := test.Status, test.Error
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status = %q after fail-closed cleanup with a dead source, want FAILED", status)
	}
	if !strings.Contains(failure, "unreachable generators left to execution-lease expiry") || !strings.Contains(failure, "gen-a") {
		t.Fatalf("run error = %q, want the unreachable gen-a reconciliation note", failure)
	}
	if source.faultStops() < durableTransitionAttempts {
		t.Fatalf("dead source stop attempts = %d, want at least %d before it was tolerated", source.faultStops(), durableTransitionAttempts)
	}
	if replacement.stops() <= durableTransitionAttempts {
		t.Fatalf("replacement stop attempts = %d, want a fail-closed stop after %d rollback attempts", replacement.stops(), durableTransitionAttempts)
	}
}

// renewEvacuationExecutionLease mimics the controller's lease keeper, which
// keeps the execution lease alive while an evacuation runs. Tests that exercise
// the lost-generator epoch fence need it: the fence deliberately waits past the
// deadline it sampled, so a static deadline would make leadership lapse at
// exactly the moment the fence completes and mask the behaviour under test with
// a leadership-change failure.
func renewEvacuationExecutionLease(t *testing.T, runner *TestRunner, ttl time.Duration) {
	t.Helper()
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(ttl / 8)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				runner.leadership.mu.Lock()
				runner.leadership.localLeaseDeadline = time.Now().Add(ttl)
				runner.leadership.mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-stopped
	})
}

// A generator that is actually gone — OOM-killed pod, drained or decommissioned
// node — can never acknowledge an exact-generation StopTest, and that is the
// primary loss mode tolerate_generator_loss advertises. The automatic
// stream-loss path must fence such a source by execution-lease epoch expiry and
// carry the run on to its replacement. Requiring the dead source's ACK instead
// would escalate a single generator loss into a whole-fleet force-stop, leaving
// the run wedged non-terminal in STOPPING while a terminal retry re-sent stops
// the source can never answer — strictly worse than leaving the flag off.
func TestGeneratorLossEvacuationSurvivesDeadSourceAndKeepsTheRunOnItsReplacement(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	service := generatorrpc.NewService("generator-b", 10, generatorrpc.WithExecution())
	pb.RegisterGeneratorControlServer(grpcServer, service)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(ctx)
		grpcServer.Stop()
		_ = listener.Close()
	})

	var replacementRequests atomic.Int64
	sut := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		replacementRequests.Add(1)
	}))
	t.Cleanup(sut.Close)

	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 250*time.Millisecond, func(ctx context.Context, target string) (generatorClient, error) {
		if target != "gen-b" {
			return nil, errors.New("lost generator is unreachable")
		}
		return generatorrpc.Dial(
			ctx,
			target,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
	})
	// The source pod is gone. Every exact-generation stop fails forever, exactly
	// as a gRPC Unavailable against a deleted pod would.
	source := newFaultEvacuationGeneratorClient("gen-a", 2)
	source.loseInitialStopResponses = 1 << 30
	script := &dsl.Script{
		ID:       "buyers-script",
		Name:     "Buyers",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: sut.URL},
		Actions:  []dsl.Action{{Name: "home", Type: "GET", Target: "/"}},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := time.Now()
	test := &ManagedTest{
		ID:           "run-evacuation-dead-source-survives",
		Name:         "dead source survives",
		ScenarioID:   "managed-evacuation-scenario",
		Status:       "RUNNING",
		Duration:     time.Minute,
		StartedAt:    &started,
		executionCtx: runCtx,
		cancel:       cancel,
		Assignment:   &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:       []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 2, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 2}},
		},
		remoteClientsByTarget:        map[string]generatorClient{"gen-a": source},
		remoteMetricsStreamsByTarget: make(map[string]*generatorrpc.MetricsStream),
		remoteMetricsBatchesByTarget: make(map[string]generatorrpc.MetricsBatch),
		remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	runner := NewTestRunner(1, hub, nil)
	runner.tests[test.ID] = test
	// The fence deliberately waits out a whole lease window. Keep it wide enough
	// that scheduler jitter cannot expire a generator session mid-fence and turn
	// this into a lease-plumbing failure rather than the behaviour under test.
	const leaseTTL = 3 * time.Second
	configureEvacuationExecutionLease(runner, test, 91, "dead-source-evacuation/91", leaseTTL)
	renewEvacuationExecutionLease(t, runner, leaseTTL)
	// A staged replacement is deliberately absent from the live topology, so its
	// process-level execution epoch is renewed only by the controller-lifetime
	// fleet keeper (server.go starts this for every distributed backend). The
	// fence waits past the deadline that keeper's first claim was derived from,
	// and an assignment lease can never outlive its process epoch, so without
	// the keeper the post-fence lease renewal fails FailedPrecondition.
	keeperCtx, stopKeeper := context.WithCancel(context.Background())
	keeperDone := make(chan struct{})
	go func() {
		defer close(keeperDone)
		backend.maintainGeneratorExecutionEpoch(keeperCtx, runner)
	}()
	t.Cleanup(func() {
		stopKeeper()
		<-keeperDone
	})

	// The automatic path: gen-a's metrics stream dies because the pod died.
	if !backend.tolerateMetricsStreamLoss(runner, test, "gen-a", errors.New("rpc error: code = Unavailable desc = transport is closing")) {
		t.Fatal("lost generator was not tolerated; the run was failed instead of evacuated")
	}

	runner.mu.RLock()
	status, failure, finalizing := test.Status, test.Error, test.remoteFinalizing
	shards := cloneGeneratorShardSummaries(test.remoteGroupShards["buyers"])
	_, sourceRetained := test.remoteClientsByTarget["gen-a"]
	replacementClient := test.remoteClientsByTarget["gen-b"]
	replacementStream := test.remoteMetricsStreamsByTarget["gen-b"]
	sourceState := test.remoteTargetStates["gen-a"]
	runner.mu.RUnlock()

	if status != "RUNNING" {
		t.Fatalf("status = %q after tolerated loss of a dead generator, want RUNNING", status)
	}
	if finalizing {
		t.Fatal("a tolerated generator loss must not put the run into remote finalization")
	}
	if strings.TrimSpace(failure) != "" {
		t.Fatalf("run error = %q, want no failure after a tolerated generator loss", failure)
	}
	if len(shards) != 1 || shards[0].Target != "gen-b" || shards[0].VUsers != 2 {
		t.Fatalf("buyers shards = %+v, want the 2 VUsers rehomed onto gen-b", shards)
	}
	if sourceRetained {
		t.Fatal("dead source remains in the run client set and would keep receiving lifecycle controls")
	}
	if replacementClient == nil || replacementStream == nil {
		t.Fatalf("replacement was not published: client=%v stream=%v", replacementClient, replacementStream)
	}
	if !remoteTargetStateDone(sourceState) {
		t.Fatalf("dead source target state = %q, want an evacuated sentinel", sourceState)
	}
	if source.faultStops() < durableTransitionAttempts {
		t.Fatalf("dead source stop attempts = %d, want at least %d before the epoch fence took over", source.faultStops(), durableTransitionAttempts)
	}

	deadline := time.Now().Add(3 * time.Second)
	for service.ActiveVUsers() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := service.ActiveVUsers(); got != 2 {
		t.Fatalf("replacement active VUsers = %d, want the evacuated 2 still under load", got)
	}
	for replacementRequests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if replacementRequests.Load() == 0 {
		t.Fatal("replacement generator never issued load after the dead source was fenced")
	}

	cancel()
	backend.closeRemoteResources(runner, test)
	if _, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: test.ID, ExecutionGeneration: 91}); err != nil {
		t.Fatalf("cleanup StopTest: %v", err)
	}
}

// The lost-generator fence runs while the evacuation mutex and the topology
// write gate are held, so a run that can no longer commit must not keep them for
// the rest of the execution-lease TTL: an operator stop that reserves STOPPING
// has to be observed while the fence is still waiting.
func TestLostGeneratorEpochFenceAbortsWhenTheRunStopsWaitingForTheLeaseTTL(t *testing.T) {
	now := time.Now().UTC()
	leadership := newExecutionLeadership(nil, "lost-generator-fence")
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = "lost-generator-fence/1"
	leadership.durableGeneration = 17
	leadership.localLeaseDeadline = now.Add(30 * time.Second)
	leadership.mu.Unlock()

	executionCtx, cancelExecution := context.WithCancel(context.Background())
	defer cancelExecution()
	test := &ManagedTest{
		ID:                      "run-lost-generator-fence",
		Status:                  "RUNNING",
		executionCtx:            executionCtx,
		executionGeneration:     17,
		remoteControlGeneration: 4,
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}, leadership: leadership}

	fenced := make(chan error, 1)
	go func() {
		fenced <- waitForLostGeneratorEpochFence(executionCtx, runner, test, "RUNNING", 4, 17)
	}()
	select {
	case err := <-fenced:
		t.Fatalf("fence returned %v before the lease deadline while the run was still committable", err)
	case <-time.After(100 * time.Millisecond):
	}

	// An operator stop reserves STOPPING without taking the topology gate, which
	// is exactly the transition the fence must notice while it waits.
	runner.mu.Lock()
	test.Status = "STOPPING"
	runner.mu.Unlock()
	select {
	case err := <-fenced:
		if err == nil {
			t.Fatal("fence reported the lost generator as fenced after the run was stopped")
		}
		if !strings.Contains(err.Error(), "abandoned while fencing lost generator") {
			t.Fatalf("fence error = %v, want an abandoned-evacuation error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fence held the evacuation and topology locks after the run was stopped")
	}
}

func TestDistributedEvacuateGeneratorRefusesBrowserGroups(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	clientA := newEvacuationFakeGeneratorClient("gen-a:9090")
	clientB := newEvacuationFakeGeneratorClient("gen-b:9090")
	test := &ManagedTest{
		ID:         "run-browser",
		Status:     "RUNNING",
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "browser", Protocol: "browser", VUsers: 2}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"browser": {
				{Group: "browser", Target: "gen-a:9090", VUsers: 1},
				{Group: "browser", Target: "gen-b:9090", VUsers: 1},
			},
		},
		remoteClientsByTarget: map[string]generatorClient{"gen-a:9090": clientA, "gen-b:9090": clientB},
		remoteTargetStates:    map[string]string{"gen-a:9090": "RUNNING", "gen-b:9090": "RUNNING"},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	impacts, err := backend.EvacuateGenerator(runner, test, "gen-a:9090", false, "operator_evacuate")
	if err == nil {
		t.Fatal("expected browser evacuation to be refused")
	}
	if len(impacts) != 1 || !impacts[0].Refused || impacts[0].RefusalReason == "" {
		t.Fatalf("impacts = %+v, want refused browser impact", impacts)
	}
	if got := clientB.adjusted("browser"); got != 0 {
		t.Fatalf("browser survivor adjusted = %d, want 0", got)
	}
}

func TestFailDistributedTestOnStreamErrorIgnoresEvacuatedTarget(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	test := &ManagedTest{
		ID:                 "run-evacuated-stream",
		Status:             "RUNNING",
		remoteTargetStates: map[string]string{"gen-a:9090": "operator_evacuate", "gen-b:9090": "RUNNING"},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-b:9090": newEvacuationFakeGeneratorClient("gen-b:9090"),
		},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	backend.failDistributedTestOnStreamError(runner, test, "gen-a:9090", errors.New("stream closed after evacuation"))

	if test.Status != "RUNNING" {
		t.Fatalf("status = %q, want RUNNING after evacuated target stream closes", test.Status)
	}
	if test.remoteFinalizing {
		t.Fatal("evacuated target stream close must not mark the test finalizing")
	}
	if test.remoteTargetStates["gen-a:9090"] != "operator_evacuate" {
		t.Fatalf("evacuated state overwritten: %#v", test.remoteTargetStates)
	}
}

func TestFailDistributedTestOnStreamErrorDoesNotReenterEvacuationForTransitionalTarget(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, nil)
	test := &ManagedTest{
		ID:                 "run-evacuating-stream",
		Status:             "RUNNING",
		Assignment:         &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		remoteTargetStates: map[string]string{"gen-a": remoteTargetStateEvacuating, "gen-b": "RUNNING"},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	// If stream loss recursively starts another evacuation, it blocks trying to
	// reacquire this operation gate. Intentional transitional loss must return.
	backend.evacuationMu.Lock()
	done := make(chan struct{})
	go func() {
		backend.failDistributedTestOnStreamError(runner, test, "gen-a", errors.New("source stream closed during fence"))
		close(done)
	}()
	select {
	case <-done:
		backend.evacuationMu.Unlock()
	case <-time.After(250 * time.Millisecond):
		backend.evacuationMu.Unlock()
		t.Fatal("stream loss re-entered evacuation while the source was intentionally being removed")
	}

	if test.Status != "RUNNING" || test.remoteFinalizing {
		t.Fatalf("transitional stream loss changed lifecycle: status=%q finalizing=%v", test.Status, test.remoteFinalizing)
	}
}

func TestDistributedEvacuateGeneratorProvisionsPreviouslyUnassignedSurvivor(t *testing.T) {
	runEvacuationProvisionScenario(t, false)
}

func TestDistributedEvacuationPreservesRunContinuity(t *testing.T) {
	runEvacuationProvisionScenario(t, true)
}

func runEvacuationProvisionScenario(t *testing.T, continuity bool) {
	runEvacuationProvisionScenarioWithFault(t, continuity, "")
}

func TestDistributedEvacuationCheckpointWriteFailures(t *testing.T) {
	for _, phase := range []string{"prepare", "staged", "source-fenced", "replacement-committed", "complete"} {
		t.Run(phase, func(t *testing.T) { runEvacuationProvisionScenarioWithFault(t, true, phase) })
	}
}

func runEvacuationProvisionScenarioWithFault(t *testing.T, continuity bool, checkpointFailure string) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	const bufSize = 1024 * 1024
	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	service := generatorrpc.NewService("generator-b", 10, generatorrpc.WithExecution(), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	var replacementRequests atomic.Int64
	sut := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		replacementRequests.Add(1)
	}))
	t.Cleanup(sut.Close)
	failedClient := newEvacuationFakeGeneratorClient("gen-a")
	sourceStopped := make(chan int64, 1)
	sourceStopCheckpointPhase := make(chan string, 1)
	var store *storage.SQLiteStore
	failedClient.stopHook = func() {
		// Record the first fence; cleanup may idempotently stop the source again.
		select {
		case sourceStopped <- replacementRequests.Load():
		default:
		}
		if store != nil {
			phase := "unavailable"
			if record, err := store.GetTest("run-provision-survivor"); err == nil {
				var guard runEvacuationCheckpoint
				if json.Unmarshal([]byte(record.ContinuityCheckpoint), &guard) == nil {
					phase = guard.Phase
				}
			}
			select {
			case sourceStopCheckpointPhase <- phase:
			default:
			}
		}
	}
	var replacementClient *setupRecordingEvacuationClient
	var beforeAssign func()
	var beforeCommit func() error
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 400*time.Millisecond, func(ctx context.Context, target string) (generatorClient, error) {
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
		replacementClient = &setupRecordingEvacuationClient{Client: client, beforeAssign: func() {
			if beforeAssign != nil {
				beforeAssign()
			}
		}, beforeCommit: func() error {
			if beforeCommit != nil {
				return beforeCommit()
			}
			return nil
		}}
		return replacementClient, nil
	})
	script := &dsl.Script{
		ID:       "buyers-script",
		Name:     "Buyers",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: sut.URL},
		Actions:  []dsl.Action{{Name: "home", Type: "GET", Target: "/"}},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := time.Now()
	pausedAt := time.Now()
	test := &ManagedTest{
		ID:           "run-provision-survivor",
		CreatedAt:    started,
		Name:         "provision survivor",
		Status:       "PAUSED",
		Duration:     time.Minute,
		StartedAt:    &started,
		PausedAt:     &pausedAt,
		executionCtx: runCtx,
		cancel:       cancel,
		Assignment:   &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:       []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 4, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 4}},
		},
		remoteClientsByTarget:        map[string]generatorClient{"gen-a": failedClient},
		remoteMetricsStreamsByTarget: make(map[string]*generatorrpc.MetricsStream),
		remoteMetricsBatchesByTarget: make(map[string]generatorrpc.MetricsBatch),
		remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	runner := NewTestRunner(1, hub, nil)
	runner.tests[test.ID] = test
	configureEvacuationExecutionLease(runner, test, 81, "operator-evacuation/81", 10*time.Second)
	generation := uint64(81)
	var token []byte
	sourceClient := generatorClient(failedClient)
	if continuity {
		var leadership *executionLeadership
		runner, store, leadership = newAdoptionTestRunner(t)
		runner.hub = hub
		runner.execution = backend
		runner.tests[test.ID] = test
		_, generation = leadership.executionLeaseTerm()
		test.executionGeneration = generation
		if err := runner.claimTestExecutionFence(test); err != nil {
			t.Fatal(err)
		}
		test.Assignment.FailoverPolicy = "continue"
		for i := range test.remoteRunSupervisionToken {
			test.remoteRunSupervisionToken[i] = 7
		}
		token = cloneRunSupervisionToken(test.remoteRunSupervisionToken)
		sourceClient = &supervisedEvacuationSource{failedClient}
		test.remoteClientsByTarget["gen-a"] = sourceClient
		backend.enableRunSupervision(time.Minute, 10*time.Second)
		if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, Status: test.Status, StartTime: started, Groups: []any{}}); err != nil {
			t.Fatal(err)
		}
	}

	if continuity {
		beforeAssign = func() {
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			if record.ContinuityCheckpoint == "" {
				t.Fatal("replacement staged without durable evacuation guard")
			}
			if _, _, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint); err == nil {
				t.Fatal("in-flight evacuation remained adoptable")
			}
		}
		beforeCommit = func() error {
			record, err := store.GetTest(test.ID)
			if err != nil {
				return err
			}
			var guard runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(record.ContinuityCheckpoint), &guard); err != nil {
				return err
			}
			if guard.Phase != runEvacuationPhaseSourceFenced || guard.StagedAt.IsZero() || len(guard.StagedTargets) != 1 || guard.StagedTargets[0] != "gen-b" || guard.SourceFencedAt.IsZero() {
				return errors.New("replacement commit preceded durable positive source fence")
			}
			return nil
		}
	}

	if checkpointFailure != "" {
		runner.testStore = &evacuationCheckpointFaultStore{SQLiteStore: store, rejectWrite: func(next string) bool {
			var guard runEvacuationCheckpoint
			if json.Unmarshal([]byte(next), &guard) != nil || guard.Version != runEvacuationCheckpointVersion {
				return checkpointFailure == "complete"
			}
			return (checkpointFailure == "prepare" && guard.Phase == runEvacuationPhasePrepared) ||
				(checkpointFailure == "staged" && guard.Phase == runEvacuationPhaseStaged) ||
				(checkpointFailure == "source-fenced" && guard.Phase == runEvacuationPhaseSourceFenced) ||
				(checkpointFailure == "replacement-committed" && guard.Phase == runEvacuationPhaseReplacementCommitted)
		}}
	}

	impacts, err := backend.EvacuateGenerator(runner, test, "gen-a", false, "node maintenance window")
	if checkpointFailure != "" {
		if err == nil || !strings.Contains(err.Error(), "injected checkpoint write failure") {
			t.Fatalf("evacuation did not reject checkpoint failure: %v", err)
		}
		if got := service.ActiveVUsers(); got != 0 {
			t.Fatalf("failed checkpoint left %d replacement VUsers", got)
		}
		if got := replacementRequests.Load(); got != 0 {
			t.Fatalf("paused replacement emitted %d requests", got)
		}
		if (checkpointFailure == "staged" || checkpointFailure == "source-fenced") && replacementClient == nil {
			t.Fatal("checkpoint fault never reached a staged replacement")
		}
		if checkpointFailure != "complete" && checkpointFailure != "replacement-committed" && replacementClient != nil && replacementClient.commitCalls.Load() != 0 {
			t.Fatal("replacement commit was called before the source-fence write succeeded")
		}
		if checkpointFailure == "replacement-committed" && (replacementClient == nil || replacementClient.commitCalls.Load() == 0) {
			t.Fatal("replacement-commit fault did not occur after CommitWork")
		}
		runner.mu.RLock()
		finalStatus := test.Status
		armed := runContinuityStatusLocked(test).Armed
		runner.mu.RUnlock()
		if armed {
			t.Fatal("failed evacuation checkpoint left adoption armed")
		}
		if checkpointFailure == "prepare" {
			if replacementClient.assignment.TestID != "" || failedClient.stopCount() != 0 || finalStatus != "PAUSED" {
				t.Fatal("failed intent write changed execution")
			}
		} else if finalStatus != "FAILED" {
			t.Fatalf("failed completion status=%s, want FAILED; error=%v", finalStatus, err)
		}
		cancel()
		backend.closeRemoteResources(runner, test)
		return
	}
	if err != nil {
		t.Fatalf("EvacuateGenerator: %v", err)
	}
	if len(impacts) != 1 || len(impacts[0].ToShards) != 1 || impacts[0].ToShards[0].Target != "gen-b" {
		t.Fatalf("evacuation impacts = %+v, want replacement shard on gen-b", impacts)
	}
	select {
	case requestsAtFence := <-sourceStopped:
		if requestsAtFence != 0 {
			t.Fatalf("replacement emitted %d requests before source fence ACK", requestsAtFence)
		}
	default:
		t.Fatal("source was not fenced before evacuation completed")
	}
	if continuity {
		select {
		case phase := <-sourceStopCheckpointPhase:
			if phase != runEvacuationPhaseStaged {
				t.Fatalf("source stop observed checkpoint phase %q, want staged", phase)
			}
		default:
			t.Fatal("source stop did not observe durable staging progress")
		}
	}
	// The controller is evacuating a logically paused run onto a generator that
	// has never seen this test. Wait beyond the coordinated release barrier to
	// prove the assignment carried that state across the wire and no replacement
	// protocol traffic escaped merely because CommitWork released the shard.
	time.Sleep(distributedCommitStartLead(400*time.Millisecond) + 100*time.Millisecond)
	if got := replacementRequests.Load(); got != 0 {
		t.Fatalf("paused replacement emitted %d requests after its commit barrier", got)
	}
	resumeCtx, resumeCancel := context.WithTimeout(context.Background(), time.Second)
	resumeResult, resumeErr := replacementClient.SetPaused(resumeCtx, generatorrpc.PauseControl{
		TestID:              test.ID,
		ExecutionGeneration: generation,
		RunSupervisionToken: token,
		Pause:               false,
		OperationID:         "resume-paused-evacuation",
		OperationSequence:   1,
	})
	resumeCancel()
	if resumeErr != nil || !resumeResult.Accepted || resumeResult.CurrentPaused {
		t.Fatalf("resume replacement = (%+v, %v)", resumeResult, resumeErr)
	}

	deadline := time.Now().Add(3 * time.Second)
	for replacementRequests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if replacementRequests.Load() == 0 {
		t.Fatal("replacement did not start after its paused gate was resumed")
	}
	if got := service.ActiveVUsers(); got != 4 {
		t.Fatalf("new survivor active VUsers = %d, want 4", got)
	}
	if test.remoteClientsByTarget["gen-b"] == nil || test.remoteMetricsStreamsByTarget["gen-b"] == nil {
		t.Fatalf("new survivor was not retained with a metrics stream: clients=%v streams=%v", test.remoteClientsByTarget, test.remoteMetricsStreamsByTarget)
	}
	if replacementClient == nil {
		t.Fatal("replacement client was not created")
	}
	if got := time.Duration(replacementClient.setupTimeout.Load()); got != 200*time.Millisecond {
		t.Fatalf("replacement stream setup timeout = %v, want 200ms inside the commit barrier", got)
	}
	if _, exists := test.remoteClientsByTarget["gen-a"]; exists {
		t.Fatal("failed target remains in run client set")
	}
	runner.mu.RLock()
	archiveSource := test.remoteArchiveClientsByTarget["gen-a"]
	runner.mu.RUnlock()
	if archiveSource != sourceClient {
		t.Fatalf("stopped source was not retained for final archive merge: %T", archiveSource)
	}
	if continuity {
		if replacementClient.assignment.AdoptionGrace != defaultRunAdoptionGrace {
			t.Fatalf("replacement lost adoption grace: %s", replacementClient.assignment.AdoptionGrace)
		}
		record, err := store.GetTest(test.ID)
		if err != nil {
			t.Fatal(err)
		}
		checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
		if err != nil || !present || len(checkpoint.Shards["buyers"]) != 1 || checkpoint.Shards["buyers"][0].Target != "gen-b" || len(checkpoint.ArchiveTargets) != 1 || checkpoint.ArchiveTargets[0] != "gen-a" {
			t.Fatalf("evacuation checkpoint = %+v, present=%v, error=%v", checkpoint, present, err)
		}
	}
	if state := test.remoteTargetStates["gen-a"]; !remoteTargetStateDone(state) {
		t.Fatalf("custom evacuation reason left unrecognized target state %q", state)
	}

	cancel()
	backend.closeRemoteResources(runner, test)
	if got := failedClient.closeCount(); got != 1 {
		t.Fatalf("archive-only source close count = %d, want 1", got)
	}
	if _, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: test.ID, ExecutionGeneration: generation, RunSupervisionToken: token}); err != nil {
		t.Fatalf("cleanup StopTest: %v", err)
	}
}

func TestDistributedEvacuationIgnoresSoleSourceTerminalUntilReplacementCommit(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	sourceClient, sourceService := newTestGeneratorRPCClient(t, "gen-a", 10)
	replacementClient, replacementService := newTestGeneratorRPCClient(t, "gen-b", 10)
	recordingReplacement := &setupRecordingEvacuationClient{Client: replacementClient}

	const (
		testID              = "run-source-terminal-race"
		executionGeneration = uint64(82)
		executionLeaseOwner = "operator-terminal-race/82"
	)
	script := &dsl.Script{ID: "buyers-script", Name: "Buyers", Protocol: "http"}
	scriptContent, contentHash, dataFiles, err := generatorrpc.PrepareDistributedScript(script)
	if err != nil {
		t.Fatalf("prepare source script: %v", err)
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer setupCancel()
	for target, client := range map[string]*generatorrpc.Client{"source": sourceClient, "replacement": replacementClient} {
		claim, claimErr := client.ClaimExecutionEpoch(setupCtx, executionGeneration, executionLeaseOwner, 10*time.Second)
		if claimErr != nil || !claim.Accepted {
			t.Fatalf("claim %s execution epoch = (%+v, %v)", target, claim, claimErr)
		}
	}
	if _, err := sourceClient.DistributeScript(setupCtx, generatorrpc.Script{ID: script.ID, ExecutionGeneration: executionGeneration, Content: scriptContent, ContentHash: contentHash}); err != nil {
		t.Fatalf("distribute source script: %v", err)
	}
	assignment, err := sourceClient.AssignWork(setupCtx, generatorrpc.Assignment{
		TestID:                    testID,
		ExecutionGeneration:       executionGeneration,
		RequireTestExecutionLease: true,
		GroupName:                 "buyers",
		TargetDuration:            time.Minute,
		ScriptID:                  script.ID,
		ExpectedScriptHash:        contentHash,
		VUserCount:                2,
		ShardOrdinal:              0,
		ShardCount:                1,
		DataFiles:                 dataFiles,
	})
	if err != nil || !assignment.Accepted {
		t.Fatalf("assign source work = (%+v, %v)", assignment, err)
	}
	if claim, claimErr := sourceClient.ClaimTestExecutionEpoch(setupCtx, testID, executionGeneration, executionLeaseOwner, 8*time.Second); claimErr != nil || !claim.Accepted {
		t.Fatalf("claim source test execution lease = (%+v, %v)", claim, claimErr)
	}
	if commit, err := sourceClient.CommitWork(setupCtx, testID, executionGeneration); err != nil || !commit.Accepted {
		t.Fatalf("commit source work = (%+v, %v)", commit, err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sourceStream, err := sourceClient.OpenMetricsStreamWithSetupTimeoutForTestGeneration(runCtx, time.Second, testID, executionGeneration)
	if err != nil {
		t.Fatalf("open source metrics stream: %v", err)
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	runner := NewTestRunner(1, hub, nil)
	source := &terminalObservingEvacuationClient{
		Client: sourceClient, runner: runner, testID: testID, target: "gen-a",
		ackDelay: 120 * time.Millisecond,
	}
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 750*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if target != "gen-b" {
			return nil, fmt.Errorf("unexpected evacuation dial target %q", target)
		}
		return recordingReplacement, nil
	})
	started := time.Now()
	test := &ManagedTest{
		ID:           testID,
		Name:         "Source terminal race",
		Status:       "RUNNING",
		Duration:     time.Minute,
		StartedAt:    &started,
		executionCtx: runCtx,
		cancel:       cancel,
		Assignment:   &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:       []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 2, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 2}},
		},
		remoteClientsByTarget:        map[string]generatorClient{"gen-a": source},
		remoteMetricsStreamsByTarget: map[string]*generatorrpc.MetricsStream{"gen-a": sourceStream},
		remoteMetricsBatchesByTarget: make(map[string]generatorrpc.MetricsBatch),
		remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
		remoteLifecycleCtx:           runCtx,
		remoteActivated:              true,
		executionBackend:             backend,
	}
	configureEvacuationExecutionLease(runner, test, executionGeneration, executionLeaseOwner, 10*time.Second)
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	go backend.consumeMetrics(runCtx, runner, test, "gen-a", sourceStream)

	deadline := time.Now().Add(2 * time.Second)
	for {
		runner.mu.RLock()
		lifecycle := test.remoteMetricsBatchesByTarget["gen-a"].LifecycleState
		runner.mu.RUnlock()
		if lifecycle == "RUNNING" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("source lifecycle = %q, want RUNNING before evacuation", lifecycle)
		}
		time.Sleep(time.Millisecond)
	}

	impacts, err := backend.EvacuateGenerator(runner, test, "gen-a", false, "node maintenance")
	if err != nil {
		t.Fatalf("EvacuateGenerator: %v", err)
	}
	if len(impacts) != 1 || len(impacts[0].ToShards) != 1 || impacts[0].ToShards[0].Target != "gen-b" {
		t.Fatalf("evacuation impacts = %+v, want source moved to gen-b", impacts)
	}
	runner.mu.RLock()
	status := test.Status
	finalizing := test.remoteFinalizing
	sourceState := test.remoteTargetStates["gen-a"]
	replacement := test.remoteClientsByTarget["gen-b"]
	replacementStream := test.remoteMetricsStreamsByTarget["gen-b"]
	_, sourceRetained := test.remoteClientsByTarget["gen-a"]
	runner.mu.RUnlock()
	if status != "RUNNING" || finalizing {
		t.Fatalf("source terminal batch finalized the sole-source run: status=%q finalizing=%v", status, finalizing)
	}
	if !remoteTargetStateDone(sourceState) || sourceRetained {
		t.Fatalf("source topology was not retired: state=%q retained=%v", sourceState, sourceRetained)
	}
	if replacement == nil || replacementStream == nil {
		t.Fatalf("replacement topology incomplete: client=%v stream=%v", replacement, replacementStream)
	}
	if got := sourceService.ActiveVUsers(); got != 0 {
		t.Fatalf("source active VUsers = %d, want 0 after positive fence", got)
	}
	if got := replacementService.ActiveVUsers(); got != 2 {
		t.Fatalf("replacement active VUsers = %d, want 2", got)
	}
	durationAdjustments := recordingReplacement.recordedDurationAdjustments()
	if len(durationAdjustments) == 0 {
		t.Fatal("replacement duration was not refreshed after the delayed source fence")
	}
	if got := durationAdjustments[len(durationAdjustments)-1].TargetDuration; got <= 0 || got >= time.Minute-50*time.Millisecond {
		t.Fatalf("refreshed replacement logical duration = %s, want positive and shortened after the 120ms source fence", got)
	}

	cancel()
	backend.closeRemoteResources(runner, test)
}

// deadSourceEvacuationClient never acknowledges an execution-fenced stop, which
// is what drives the tolerated-loss path onto waitForLostGeneratorEpochFence
// instead of a positive source fence.
type deadSourceEvacuationClient struct {
	*evacuationFakeGeneratorClient
	stopAttempts atomic.Int32
}

func (c *deadSourceEvacuationClient) StopTestAtGeneration(context.Context, string, bool, uint64) (generatorrpc.StopResult, error) {
	c.stopAttempts.Add(1)
	return generatorrpc.StopResult{}, context.DeadlineExceeded
}

// The lost-source fence deliberately waits past the controller lease deadline
// the staged replacement was dialled under, so that replacement's process-scoped
// execution epoch lapses mid-fence unless something renews it. This test runs
// the whole evacuation with the controller-lifetime fleet keeper
// (maintainGeneratorExecutionEpoch) deliberately NOT running, which is the state
// an unreachable, newly autoscaled, or DNS-churned target is effectively in.
// A lapsed epoch is not recoverable after the fact: the generator force-stops
// and deletes the staged assignment, so a tolerated loss would silently degrade
// into the whole-fleet fail-closed stop the feature exists to avoid.
func TestGeneratorLossEvacuationKeepsStagedReplacementEpochWithoutTheFleetKeeper(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	replacementClient, replacementService := newTestGeneratorRPCClient(t, "gen-b", 10, generatorrpc.WithExecution())

	const (
		testID              = "run-lost-source-staged-epoch"
		executionGeneration = uint64(84)
		executionLeaseOwner = "loss-evacuation/84"
		leaseTTL            = time.Second
	)
	source := &deadSourceEvacuationClient{evacuationFakeGeneratorClient: newEvacuationFakeGeneratorClient("gen-a")}
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 750*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if target != "gen-b" {
			return nil, fmt.Errorf("lost generator %q is unreachable", target)
		}
		return replacementClient, nil
	})
	sut := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(sut.Close)
	script := &dsl.Script{
		ID:       "buyers-script",
		Name:     "Buyers",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: sut.URL},
		Actions:  []dsl.Action{{Name: "home", Type: "GET", Target: "/"}},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := time.Now()
	test := &ManagedTest{
		ID:           testID,
		Name:         "Lost source staged epoch",
		Status:       "RUNNING",
		Duration:     time.Minute,
		StartedAt:    &started,
		executionCtx: runCtx,
		cancel:       cancel,
		Assignment:   &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:       []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 2, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 2}},
		},
		remoteClientsByTarget:        map[string]generatorClient{"gen-a": source},
		remoteMetricsStreamsByTarget: make(map[string]*generatorrpc.MetricsStream),
		remoteMetricsBatchesByTarget: make(map[string]generatorrpc.MetricsBatch),
		remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
		remoteLifecycleCtx:           runCtx,
		remoteActivated:              true,
		executionBackend:             backend,
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	runner := NewTestRunner(1, hub, nil)
	runner.tests[test.ID] = test
	configureEvacuationExecutionLease(runner, test, executionGeneration, executionLeaseOwner, leaseTTL)

	// A live controller keeps extending its own lease; without that the fence's
	// post-wait leadership recheck would fail for an unrelated reason and this
	// would stop proving anything about generator epochs. This renewal is
	// controller-local only — it touches no generator.
	leaseCtx, stopLeaseRenewal := context.WithCancel(context.Background())
	defer stopLeaseRenewal()
	go func() {
		ticker := time.NewTicker(leaseTTL / 10)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				runner.leadership.mu.Lock()
				runner.leadership.localLeaseDeadline = time.Now().Add(leaseTTL)
				runner.leadership.mu.Unlock()
			}
		}
	}()

	impacts, err := backend.evacuateGenerator(runner, test, "gen-a", false, true)
	if err != nil {
		t.Fatalf("tolerated generator loss degraded into a fail-closed stop: %v", err)
	}
	if len(impacts) != 1 || len(impacts[0].ToShards) != 1 || impacts[0].ToShards[0].Target != "gen-b" {
		t.Fatalf("evacuation impacts = %+v, want the lost shard moved to gen-b", impacts)
	}
	if source.stopAttempts.Load() == 0 {
		t.Fatal("the lost source was never asked to stop before the lease fence")
	}
	// Zero here means the generator expired its execution generation during the
	// fence and force-stopped the staged assignment.
	if got := replacementService.ActiveVUsers(); got != 2 {
		t.Fatalf("replacement active VUsers = %d, want 2 held across the lost-source fence", got)
	}
	runner.mu.RLock()
	status := test.Status
	finalizing := test.remoteFinalizing
	sourceState := test.remoteTargetStates["gen-a"]
	_, sourceRetained := test.remoteClientsByTarget["gen-a"]
	replacement := test.remoteClientsByTarget["gen-b"]
	runner.mu.RUnlock()
	if status != "RUNNING" || finalizing {
		t.Fatalf("run did not survive the tolerated loss: status=%q finalizing=%v", status, finalizing)
	}
	if !remoteTargetStateDone(sourceState) || sourceRetained {
		t.Fatalf("lost source was not retired: state=%q retained=%v", sourceState, sourceRetained)
	}
	if replacement == nil {
		t.Fatal("replacement was not published into the run topology")
	}

	cancel()
	backend.closeRemoteResources(runner, test)
}

func TestDistributedEvacuationCommitFailureAfterSourceFenceFailsClosed(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	service := generatorrpc.NewService("generator-b", 10, generatorrpc.WithExecution())
	pb.RegisterGeneratorControlServer(grpcServer, service)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(ctx)
		grpcServer.Stop()
		_ = listener.Close()
	})

	var replacement *commitFailingEvacuationClient
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 250*time.Millisecond, func(ctx context.Context, target string) (generatorClient, error) {
		if target != "gen-b" {
			return nil, errors.New("source is unavailable")
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
		replacement = &commitFailingEvacuationClient{generatorClient: client}
		return replacement, nil
	})
	source := newEvacuationFakeGeneratorClient("gen-a")
	script := &dsl.Script{
		ID:       "commit-failure-script",
		Name:     "Commit failure",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "http://127.0.0.1"},
		Actions:  []dsl.Action{{Name: "home", Type: "GET", Target: "/"}},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := time.Now()
	test := &ManagedTest{
		ID:           "evacuation-commit-failure",
		Status:       "RUNNING",
		Duration:     time.Minute,
		StartedAt:    &started,
		executionCtx: runCtx,
		cancel:       cancel,
		Assignment:   &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:       []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 2, script: script}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 2}},
		},
		remoteClientsByTarget:        map[string]generatorClient{"gen-a": source},
		remoteMetricsStreamsByTarget: make(map[string]*generatorrpc.MetricsStream),
		remoteMetricsBatchesByTarget: make(map[string]generatorrpc.MetricsBatch),
		remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	runner := NewTestRunner(1, hub, nil)
	runner.tests[test.ID] = test
	configureEvacuationExecutionLease(runner, test, 83, "loss-evacuation/83", 10*time.Second)

	_, err := backend.EvacuateGenerator(runner, test, "gen-a", false, "generator_loss")
	var uncertain *distributedEvacuationUncertainError
	if !errors.As(err, &uncertain) {
		t.Fatalf("evacuation error = %v, want fail-closed uncertainty", err)
	}
	if source.stopCount() == 0 {
		t.Fatal("source was not fenced before replacement commit")
	}
	if replacement == nil || replacement.commitCalls.Load() != 1 {
		t.Fatalf("replacement commit calls = %#v", replacement)
	}
	if got := service.ActiveVUsers(); got != 0 {
		t.Fatalf("fail-closed cleanup left %d reserved replacement VUsers", got)
	}
	if test.Status != "FAILED" {
		t.Fatalf("test status = %q, want FAILED", test.Status)
	}
}
