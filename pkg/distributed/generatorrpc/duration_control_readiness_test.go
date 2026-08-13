package generatorrpc

import (
	"context"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestAdjustDurationWaitsForCommittedEngineReadiness(t *testing.T) {
	script := &dsl.Script{
		Name:     "duration readiness",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"},
		Actions:  []dsl.Action{{Name: "request", Type: "GET", Target: "/"}},
	}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:   []engine.GroupConfig{{Name: "group", Script: script, VUsers: 1}},
		Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	service := newIsolatedService(t, 10, WithExecution())
	execution := &groupExecution{groupName: "group", engine: mge, done: make(chan struct{})}
	execution.started.Store(true)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, duration: time.Minute, durationSeconds: 60,
		groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
	}
	service.executions["run"] = map[string]*groupExecution{"group": execution}
	service.mu.Unlock()

	runCtx, cancelRun := context.WithCancel(context.Background())
	var engineDone chan struct{}
	t.Cleanup(func() {
		cancelRun()
		if engineDone != nil {
			select {
			case <-engineDone:
			case <-time.After(2 * time.Second):
				t.Error("engine did not stop during cleanup")
			}
		}
		service.mu.Lock()
		delete(service.executions, "run")
		delete(service.assignments, "run")
		service.mu.Unlock()
		close(execution.done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	type durationOutcome struct {
		ack *pb.DurationAck
		err error
	}
	outcomes := make(chan durationOutcome, 1)
	adjustCtx, cancelAdjust := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelAdjust()
	go func() {
		ack, adjustErr := service.AdjustDuration(adjustCtx, &pb.DurationAdjustment{
			TestId: "run", TargetDurationSeconds: 120,
		})
		outcomes <- durationOutcome{ack: ack, err: adjustErr}
	}()

	select {
	case outcome := <-outcomes:
		t.Fatalf("duration control returned before engine readiness: ack=%#v err=%v", outcome.ack, outcome.err)
	case <-time.After(25 * time.Millisecond):
	}

	engineDone = make(chan struct{})
	go func() {
		defer close(engineDone)
		_, _ = mge.Run(runCtx)
	}()

	select {
	case outcome := <-outcomes:
		if outcome.err != nil || !outcome.ack.GetAccepted() {
			t.Fatalf("duration control after readiness = (%#v, %v)", outcome.ack, outcome.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("duration control did not resume after engine readiness")
	}
	if got := mge.GetBaseTargetDuration(); got != 2*time.Minute {
		t.Fatalf("adjusted duration = %v, want 2m", got)
	}
}
