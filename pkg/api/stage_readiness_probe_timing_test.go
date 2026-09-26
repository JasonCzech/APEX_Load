package api

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type stageReadinessProbeCall struct {
	at     time.Time
	target int
}

type stageReadinessProbeBackend struct {
	mu         sync.Mutex
	rejections int
	calls      []stageReadinessProbeCall
	admitted   chan time.Time
	admitOnce  sync.Once
}

func (*stageReadinessProbeBackend) Run(*TestRunner, *ManagedTest) {}

func (*stageReadinessProbeBackend) requiresInitialRampReadinessProbe() {}

func (backend *stageReadinessProbeBackend) ScaleVUsers(_ *TestRunner, _ *ManagedTest, _ string, targetVUsers int) error {
	now := time.Now()
	backend.mu.Lock()
	backend.calls = append(backend.calls, stageReadinessProbeCall{at: now, target: targetVUsers})
	call := len(backend.calls)
	backend.mu.Unlock()
	if call <= backend.rejections {
		return fmt.Errorf("generator still preparing: %w", engine.ErrInitialRampInProgress)
	}
	backend.admitOnce.Do(func() { backend.admitted <- now })
	return nil
}

func (backend *stageReadinessProbeBackend) snapshotCalls() []stageReadinessProbeCall {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]stageReadinessProbeCall(nil), backend.calls...)
}

func TestDistributedStageReadinessProbePreservesFirstHoldAndRateLimitsRetries(t *testing.T) {
	const holdDuration = 120 * time.Millisecond
	backend := &stageReadinessProbeBackend{
		rejections: 2,
		admitted:   make(chan time.Time, 1),
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.setExecutionBackend(backend)
	runner.tests["slow-initial-ramp"] = &ManagedTest{
		ID:           "slow-initial-ramp",
		Name:         "Slow distributed initial ramp",
		Status:       "RUNNING",
		ScenarioMode: "vuser_driven",
		ProjectID:    storage.DefaultProjectID,
		TotalVUsers:  1,
		Groups: []TestGroup{{
			Name:          "buyers",
			VUsers:        1,
			stageDriven:   true,
			initialVUsers: 1,
			currentVUsers: 1,
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	completed := make(chan time.Time, 1)
	go func() {
		runner.runStageSchedule(ctx, "slow-initial-ramp", "buyers", []storage.LoadStage{{
			Type: "hold", Duration: holdDuration.String(), TargetVUsers: 1,
		}}, 0, nil)
		completed <- time.Now()
	}()

	var admittedAt time.Time
	select {
	case admittedAt = <-backend.admitted:
	case <-ctx.Done():
		t.Fatalf("readiness probe was never admitted: %v", ctx.Err())
	}
	var completedAt time.Time
	select {
	case completedAt = <-completed:
	case <-ctx.Done():
		t.Fatalf("stage schedule did not finish: %v", ctx.Err())
	}

	calls := backend.snapshotCalls()
	if len(calls) != 3 {
		t.Fatalf("readiness probe calls = %#v, want two rejections followed by one admission", calls)
	}
	for index, call := range calls {
		if call.target != 1 {
			t.Fatalf("readiness probe call %d target = %d, want configured target 1", index, call.target)
		}
		if index == 0 {
			continue
		}
		spacing := call.at.Sub(calls[index-1].at)
		if spacing < initialRampScaleRetryInterval-50*time.Millisecond {
			t.Fatalf("readiness retry %d spacing = %s, want approximately %s rather than a 100 Hz poll", index, spacing, initialRampScaleRetryInterval)
		}
		if spacing > 4*initialRampScaleRetryInterval {
			t.Fatalf("readiness retry %d spacing = %s, want bounded near %s", index, spacing, initialRampScaleRetryInterval)
		}
	}
	if elapsed := completedAt.Sub(admittedAt); elapsed < holdDuration {
		t.Fatalf("first hold elapsed only %s after readiness admission, want at least %s; initial-ramp retries consumed the hold", elapsed, holdDuration)
	}
}

func TestAdoptedStageScheduleJoinsCurrentOrFinalTarget(t *testing.T) {
	for _, elapsed := range []time.Duration{150 * time.Millisecond, time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			backend := &stageReadinessProbeBackend{admitted: make(chan time.Time, 1)}
			runner := NewTestRunner(1, NewHub(), nil)
			runner.setExecutionBackend(backend)
			started := time.Now().Add(-elapsed)
			runner.tests["adopted"] = &ManagedTest{
				ID: "adopted", Status: "RUNNING", ScenarioMode: "vuser_driven",
				StartedAt: &started, stageScheduleResume: true, TotalVUsers: 1,
				Groups: []TestGroup{{Name: "buyers", VUsers: 1, currentVUsers: 1, stageDriven: true}},
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			runner.runStageSchedule(ctx, "adopted", "buyers", []storage.LoadStage{
				{Type: "hold", Duration: "100ms", TargetVUsers: 2},
				{Type: "hold", Duration: "100ms", TargetVUsers: 4},
			}, 0, nil)
			calls := backend.snapshotCalls()
			if len(calls) != 1 || calls[0].target != 4 {
				t.Fatalf("resumed scale calls = %+v, want only the current/final target 4", calls)
			}
		})
	}
}
