package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCanceledRampDoesNotApplyQueuedScale(t *testing.T) {
	for _, operation := range []string{"hold current target", "replace with current target", "cancel parent"} {
		t.Run(operation, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			backend := &scalableExecutionBackendSpy{calls: make(chan scaleBackendCall, 8)}
			runner.setExecutionBackend(backend)
			const id = "cancelled-ramp"
			runner.tests[id] = &ManagedTest{
				ID: id, Status: "RUNNING", ScenarioMode: "vuser_driven",
				ProjectID: storage.DefaultProjectID, TotalVUsers: 3,
				Groups: []TestGroup{{Name: "buyers", VUsers: 3, stageDriven: true, initialVUsers: 1, currentVUsers: 1}},
			}
			// Another scale or topology operation already holds the group's gate.
			unlock := runner.lockDistributedGroupControl(id, "buyers")
			unlocked := false
			defer func() {
				if !unlocked {
					unlock()
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ramp, err := runner.startVUserScaleRamp(ctx, id, "buyers", 3, 2*time.Millisecond)
			if err != nil || ramp == nil {
				t.Fatalf("start ramp: %v", err)
			}
			defer ramp.cancel()
			// Wait until the ramp has completed its timer and queued a step.
			deadline := time.Now().Add(3 * time.Second)
			for {
				runner.remoteControlMu.Lock()
				gate := runner.remoteControlGates[id]
				waiting := gate != nil && gate.refs >= 2
				runner.remoteControlMu.Unlock()
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("ramp never reached group control gate")
				}
				time.Sleep(time.Millisecond)
			}
			switch operation {
			case "hold current target":
				if err := runner.applyStageHoldTarget(context.Background(), id, "buyers", 1); err != nil {
					t.Fatalf("hold target: %v", err)
				}
			case "replace with current target":
				if replacement, err := runner.startVUserScaleRamp(context.Background(), id, "buyers", 1, time.Second); err != nil || replacement != nil {
					t.Fatalf("replace ramp at current target: ramp=%v err=%v", replacement, err)
				}
			case "cancel parent":
				cancel()
			}
			unlock()
			unlocked = true
			select {
			case err := <-ramp.done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled ramp result = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled ramp did not finish")
			}
			select {
			case call := <-backend.calls:
				t.Fatalf("cancelled ramp changed VUsers: target=%d, want retained target=1", call.target)
			default:
			}
			runner.mu.RLock()
			current := runner.tests[id].Groups[0].currentVUsers
			runner.mu.RUnlock()
			if current != 1 {
				t.Fatalf("cancelled ramp persisted target=%d, want 1", current)
			}
		})
	}
}
