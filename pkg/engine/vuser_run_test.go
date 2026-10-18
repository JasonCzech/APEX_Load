package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Observe the real think-time boundary without wall-clock sleeps. The large
// execution frame must be gone when thousands of VUsers park at this boundary.
type phaseBoundaryClock struct {
	t       *testing.T
	elapsed time.Duration
	delays  []time.Duration
	cancel  context.CancelFunc
}

func (clock *phaseBoundaryClock) WaitIfPaused(ctx context.Context) error { return ctx.Err() }
func (clock *phaseBoundaryClock) ActiveElapsed() time.Duration           { return clock.elapsed }
func (clock *phaseBoundaryClock) SleepActive(ctx context.Context, delay time.Duration) bool {
	clock.delays = append(clock.delays, delay)
	var pcs [64]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(1, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".(*vuserRun).runLeaf") {
			clock.t.Error("think time retains the large action execution frame")
		}
		if !more {
			break
		}
	}
	if clock.cancel != nil {
		clock.cancel()
		return false
	}
	clock.elapsed += delay
	return ctx.Err() == nil
}

func TestVUserPhaseDriverPreservesNestedWaitsAndTeardown(t *testing.T) {
	for _, cancelDuringThink := range []bool{false, true} {
		name := "complete"
		if cancelDuringThink {
			name = "cancel during think"
		}
		t.Run(name, func(t *testing.T) {
			proto := &captureProtocol{}
			action := click("child")
			action.ThinkTimeAfter = true
			action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1}
			vu := newControlFlowVUser(proto, nil, []dsl.Action{{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{action}}, ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 2}}})
			vu.Script.Config.RunLogic.Iterations = 2
			vu.Script.Init = []dsl.Action{click("init")}
			vu.Script.Teardown = []dsl.Action{click("cleanup")}
			before, err := json.Marshal(vu.Script)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clock := &phaseBoundaryClock{t: t}
			if cancelDuringThink {
				clock.cancel = cancel
			}
			vu.PauseControl = clock
			vu.Run(ctx)
			wantNames := []string{"init", "child", "child", "child", "child", "cleanup"}
			wantDelays := []time.Duration{time.Millisecond, time.Millisecond, 2 * time.Millisecond, time.Millisecond, time.Millisecond, 2 * time.Millisecond}
			if cancelDuringThink {
				wantNames = []string{"init", "child", "cleanup"}
				wantDelays = []time.Duration{time.Millisecond}
			}
			if !reflect.DeepEqual(proto.names, wantNames) {
				t.Fatalf("execution order %v, want %v", proto.names, wantNames)
			}
			if !reflect.DeepEqual(clock.delays, wantDelays) {
				t.Fatalf("think times %v, want %v", clock.delays, wantDelays)
			}
			if vu.currentIteration.Load() != 0 {
				t.Fatal("iteration ownership survived exit")
			}
			after, err := json.Marshal(vu.Script)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("execution mutated the shared admitted script")
			}
		})
	}
}
