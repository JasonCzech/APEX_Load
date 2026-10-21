package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type shutdownFinalizationBackend struct{ entered, release chan struct{} }

func (b *shutdownFinalizationBackend) Run(tr *TestRunner, run *ManagedTest) {
	// The terminal row can be visible while archive finalization still owns IO.
	tr.mu.Lock()
	run.Status = "COMPLETE"
	tr.mu.Unlock()
	close(b.entered)
	<-b.release
}

func TestShutdownJoinsTerminalExecutionDriverBeforeReturning(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "joins", true: "deadline retains ownership"}[deadline], func(t *testing.T) {
			server := NewServer(ServerConfig{MaxConcurrent: 1})
			backend := &shutdownFinalizationBackend{entered: make(chan struct{}), release: make(chan struct{})}
			server.runner.setExecutionBackend(backend)
			executionCtx, cancelExecution := context.WithCancel(context.Background())
			defer cancelExecution()
			run := &ManagedTest{ID: "shutdown-join", Status: "INITIALIZING", executionCtx: executionCtx, cancel: cancelExecution}
			server.runner.mu.Lock()
			server.runner.tests[run.ID] = run
			server.runner.mu.Unlock()
			done := make(chan struct{})
			go func() { defer close(done); server.runner.runTest(run) }()
			var release sync.Once
			t.Cleanup(func() {
				release.Do(func() { close(backend.release) })
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("driver did not join")
				}
			})
			select {
			case <-backend.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("driver did not enter")
			}
			budget := 3 * time.Second
			if deadline {
				budget = 50 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			shutdown := make(chan error, 1)
			go func() { shutdown <- server.Shutdown(ctx) }()
			if deadline {
				select {
				case err := <-shutdown:
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("shutdown hid unjoined owner", err)
					}
				case <-time.After(time.Second):
					t.Fatal("shutdown ignored deadline")
				}
				server.runner.executionWorkerMu.Lock()
				owned := server.runner.executionWorkers
				server.runner.executionWorkerMu.Unlock()
				if owned != 1 {
					t.Fatal("deadline erased physical owner", owned)
				}
			} else {
				select {
				case err := <-shutdown:
					t.Fatal("shutdown returned before finalization joined", err)
				case <-time.After(50 * time.Millisecond):
				}
				release.Do(func() { close(backend.release) })
				select {
				case err := <-shutdown:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("shutdown did not join")
				}
			}
		})
	}
}

func TestShutdownGateRefusesLateRunDriver(t *testing.T) {
	backend := &shutdownFinalizationBackend{entered: make(chan struct{}), release: make(chan struct{})}
	runner, run, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()
	runner.beginShutdown()
	runner.runTest(run)
	select {
	case <-backend.entered:
		t.Fatal("late driver crossed shutdown gate")
	default:
	}
	if err := runner.waitExecutionWorkers(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownJoinsEnteredTerminalRetry(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	if !runner.beginTerminalRetry("retry") {
		t.Fatal("retry not owned")
	}
	runner.beginShutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := runner.waitExecutionWorkers(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("in-flight retry hidden", err)
	}
	runner.endTerminalRetry("retry")
	if err := runner.waitExecutionWorkers(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownAbortsAdmittedRunBeforeDriverStarts(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 1})
	backend := &lifecycleTransitionBackend{}
	server.runner.setExecutionBackend(backend)
	executionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &ManagedTest{ID: "shutdown-before-dispatch", Status: "INITIALIZING", executionCtx: executionCtx, cancel: cancel}
	server.runner.tests[run.ID] = run
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != "ABORTED" {
		t.Fatalf("admitted run stranded in %s", run.Status)
	}
	server.runner.runTest(run)
	if runs, _ := backend.counts(); runs != 0 {
		t.Fatalf("late backend calls: %d", runs)
	}
	if err := server.runner.waitExecutionWorkers(ctx); err != nil {
		t.Fatal(err)
	}
}
