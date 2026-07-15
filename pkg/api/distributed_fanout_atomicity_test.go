package api

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type fanoutAtomicityClient struct {
	mu             sync.Mutex
	stopResult     generatorrpc.StopResult
	stopErr        error
	stopCalls      int
	durationCalls  []int64
	durationResult func(call int, seconds int64) (generatorrpc.DurationResult, error)
}

func (c *fanoutAtomicityClient) DistributeScript(context.Context, generatorrpc.Script) (generatorrpc.ScriptResult, error) {
	return generatorrpc.ScriptResult{}, nil
}

func (c *fanoutAtomicityClient) AssignWork(context.Context, generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	return generatorrpc.AssignmentResult{Accepted: true}, nil
}

func (c *fanoutAtomicityClient) AdjustVUsers(context.Context, generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error) {
	return generatorrpc.AdjustmentResult{Accepted: true}, nil
}

func (c *fanoutAtomicityClient) AdjustDuration(_ context.Context, adjustment generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error) {
	c.mu.Lock()
	c.durationCalls = append(c.durationCalls, adjustment.TargetDurationSeconds)
	call := len(c.durationCalls)
	fn := c.durationResult
	c.mu.Unlock()
	if fn != nil {
		return fn(call, adjustment.TargetDurationSeconds)
	}
	return generatorrpc.DurationResult{Accepted: true}, nil
}

func (c *fanoutAtomicityClient) OpenMetricsStream(context.Context) (*generatorrpc.MetricsStream, error) {
	return nil, errors.New("not implemented")
}

func (c *fanoutAtomicityClient) OpenHeartbeat(context.Context) (*generatorrpc.HeartbeatStream, error) {
	return nil, errors.New("not implemented")
}

func (c *fanoutAtomicityClient) StopTest(context.Context, string, bool) (generatorrpc.StopResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopCalls++
	return c.stopResult, c.stopErr
}

func (c *fanoutAtomicityClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (c *fanoutAtomicityClient) Close() error { return nil }

func (c *fanoutAtomicityClient) durations() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int64(nil), c.durationCalls...)
}

func TestDistributedStopFanoutErrorIsFailClosedAfterDispatch(t *testing.T) {
	stopErr := errors.New("stop response lost")
	clientA := &fanoutAtomicityClient{stopResult: generatorrpc.StopResult{Acknowledged: true}}
	clientB := &fanoutAtomicityClient{stopErr: stopErr}
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, nil)
	test := &ManagedTest{
		ID:                    "partial-stop",
		Status:                "STOPPING",
		remoteClientsByTarget: map[string]generatorClient{"gen-a": clientA, "gen-b": clientB},
	}
	runner := &TestRunner{}

	err := backend.StopTestContext(context.Background(), runner, test)
	if !errors.Is(err, stopErr) {
		t.Fatalf("StopTestContext error = %v, want wrapped %v", err, stopErr)
	}
	if !distributedStopMayHaveApplied(err) {
		t.Fatalf("StopTestContext error %T was not marked as potentially applied", err)
	}
	var fanoutErr *distributedStopFanoutError
	if !errors.As(err, &fanoutErr) {
		t.Fatalf("StopTestContext error type = %T, want *distributedStopFanoutError", err)
	}
	if got := fanoutErr.AttemptedTargets(); !reflect.DeepEqual(got, []string{"gen-a", "gen-b"}) {
		t.Fatalf("attempted targets = %v, want [gen-a gen-b]", got)
	}
	if got := fanoutErr.AcknowledgedTargets(); !reflect.DeepEqual(got, []string{"gen-a"}) {
		t.Fatalf("acknowledged targets = %v, want [gen-a]", got)
	}
}

func TestRunnerKeepsStoppingAfterPartialDistributedStop(t *testing.T) {
	clientA := &fanoutAtomicityClient{stopResult: generatorrpc.StopResult{Acknowledged: true}}
	clientB := &fanoutAtomicityClient{stopErr: errors.New("stop response lost")}
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, nil)
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	test := &ManagedTest{
		ID:                    "runner-partial-stop",
		Status:                "RUNNING",
		executionBackend:      backend,
		executionCtx:          executionCtx,
		cancel:                cancelExecution,
		remoteClientsByTarget: map[string]generatorClient{"gen-a": clientA, "gen-b": clientB},
	}
	runner := &TestRunner{
		tests:     map[string]*ManagedTest{test.ID: test},
		execution: backend,
		hub:       NewHub(),
	}

	err := runner.StopTest(test.ID)
	if err == nil || !distributedStopMayHaveApplied(err) {
		t.Fatalf("StopTest error = %v, want potentially-applied fanout error", err)
	}
	if test.Status != "STOPPING" {
		t.Fatalf("test status = %q, want fail-closed STOPPING", test.Status)
	}
	if executionCtx.Err() == nil {
		t.Fatal("ambiguous stop did not cancel the run execution context")
	}
	clientB.mu.Lock()
	clientB.stopErr = nil
	clientB.stopResult = generatorrpc.StopResult{Acknowledged: true}
	clientB.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		clientA.mu.Lock()
		callsA := clientA.stopCalls
		clientA.mu.Unlock()
		clientB.mu.Lock()
		callsB := clientB.stopCalls
		clientB.mu.Unlock()
		if callsA >= 2 && callsB >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("automatic teardown retry calls = (%d, %d), want both >= 2", callsA, callsB)
		}
		time.Sleep(10 * time.Millisecond)
	}
	runner.mu.Lock()
	runner.shuttingDown = true
	runner.mu.Unlock()
}

func TestAcknowledgedDistributedStopEscalatesAndLeavesStopping(t *testing.T) {
	client := &fanoutAtomicityClient{stopResult: generatorrpc.StopResult{Acknowledged: true, PendingVUsers: 1}}
	backend := newDistributedExecutionBackend([]string{"gen-a"}, 20*time.Millisecond, nil)
	backend.stopGrace = 10 * time.Millisecond
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	defer cancelLifecycle()
	test := &ManagedTest{
		ID: "acked-stop-finalizes", Status: "RUNNING", executionBackend: backend,
		executionCtx: executionCtx, cancel: cancelExecution,
		remoteLifecycleCtx: lifecycleCtx, remoteLifecycleCancel: cancelLifecycle,
		remoteClientsByTarget: map[string]generatorClient{"gen-a": client},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}, execution: backend, hub: NewHub()}

	if err := runner.StopTest(test.ID); err != nil {
		t.Fatalf("StopTest: %v", err)
	}
	if executionCtx.Err() == nil {
		t.Fatal("acknowledged graceful stop did not cancel active execution work")
	}
	deadline := time.Now().Add(time.Second)
	for {
		runner.mu.RLock()
		status := test.Status
		runner.mu.RUnlock()
		if isTerminalTestStatus(status) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %s, want bounded terminal transition after forced stop", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	client.mu.Lock()
	stopCalls := client.stopCalls
	client.mu.Unlock()
	if stopCalls < 2 {
		t.Fatalf("stop RPC calls = %d, want graceful request plus forced escalation", stopCalls)
	}
}

func TestDistributedAdjustDurationRetriesCompensationForEveryAttemptedTarget(t *testing.T) {
	forwardErr := errors.New("duration response lost")
	rollbackErr := errors.New("duration rollback failed")
	clientA := &fanoutAtomicityClient{
		durationResult: func(call int, _ int64) (generatorrpc.DurationResult, error) {
			if call == 2 {
				return generatorrpc.DurationResult{}, rollbackErr
			}
			return generatorrpc.DurationResult{Accepted: true}, nil
		},
	}
	clientB := &fanoutAtomicityClient{
		durationResult: func(call int, _ int64) (generatorrpc.DurationResult, error) {
			if call == 1 {
				return generatorrpc.DurationResult{}, forwardErr
			}
			return generatorrpc.DurationResult{Accepted: true}, nil
		},
	}
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, time.Second, nil)
	test := &ManagedTest{
		ID:                    "partial-duration",
		Status:                "RUNNING",
		Duration:              time.Minute,
		remoteClientsByTarget: map[string]generatorClient{"gen-a": clientA, "gen-b": clientB},
	}
	runner := &TestRunner{}

	err := backend.AdjustDuration(runner, test, 2*time.Minute)
	if !errors.Is(err, forwardErr) || errors.Is(err, rollbackErr) {
		t.Fatalf("AdjustDuration error = %v, want forward error after rollback retry recovered", err)
	}
	if got := clientA.durations(); !reflect.DeepEqual(got, []int64{120, 60, 60}) {
		t.Fatalf("gen-a duration calls = %v, want [120 60 60]", got)
	}
	if got := clientB.durations(); !reflect.DeepEqual(got, []int64{120, 60}) {
		t.Fatalf("gen-b duration calls = %v, want [120 60]", got)
	}
}

func TestPauseControlFanoutCompensatesPartialMutation(t *testing.T) {
	type call struct {
		target string
		pause  bool
	}
	for _, tc := range []struct {
		name    string
		pause   bool
		want    []call
		forward error
	}{
		{
			name:    "pause resumes attempted targets",
			pause:   true,
			forward: errors.New("pause send failed"),
			want:    []call{{"gen-a", true}, {"gen-b", true}, {"gen-b", false}, {"gen-a", false}},
		},
		{
			name:    "resume pauses attempted targets",
			pause:   false,
			forward: errors.New("resume send failed"),
			want:    []call{{"gen-a", false}, {"gen-b", false}, {"gen-b", true}, {"gen-a", true}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			failed := false
			err := applyPauseControlFanout("partial-pause", tc.pause, []string{"gen-a", "gen-b"}, func(target string, pause bool) error {
				calls = append(calls, call{target: target, pause: pause})
				if target == "gen-b" && pause == tc.pause && !failed {
					failed = true
					return tc.forward
				}
				return nil
			})
			if !errors.Is(err, tc.forward) {
				t.Fatalf("fanout error = %v, want wrapped %v", err, tc.forward)
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("control calls = %+v, want %+v", calls, tc.want)
			}
		})
	}
}

func TestPauseControlFanoutJoinsRollbackErrors(t *testing.T) {
	forwardErr := errors.New("pause failed")
	rollbackErr := errors.New("resume rollback failed")
	failedForward := false
	err := applyPauseControlFanout("partial-pause", true, []string{"gen-a", "gen-b"}, func(target string, pause bool) error {
		if target == "gen-b" && pause && !failedForward {
			failedForward = true
			return forwardErr
		}
		if target == "gen-a" && !pause {
			return rollbackErr
		}
		return nil
	})
	if !errors.Is(err, forwardErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("fanout error = %v, want joined forward and rollback errors", err)
	}
}
