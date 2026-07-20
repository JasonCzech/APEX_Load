package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScenarioSchedulerOwnsPeriodicWorkerLifecycle(t *testing.T) {
	scheduler := newScenarioScheduler(nil)
	scheduler.sweepInterval = time.Millisecond
	var sweeps atomic.Int32
	firstSweep := make(chan struct{}, 1)
	scheduler.runSweep = func() error {
		sweeps.Add(1)
		select {
		case firstSweep <- struct{}{}:
		default:
		}
		return nil
	}

	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("idempotent Start: %v", err)
	}
	select {
	case <-firstSweep:
	case <-time.After(time.Second):
		t.Fatal("periodic scheduler did not perform a sweep")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := scheduler.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	stoppedAt := sweeps.Load()
	time.Sleep(5 * time.Millisecond)
	if got := sweeps.Load(); got != stoppedAt {
		t.Fatalf("scheduler swept after Shutdown: before=%d after=%d", stoppedAt, got)
	}
	if err := scheduler.Start(context.Background()); !errors.Is(err, errScenarioSchedulerStopped) {
		t.Fatalf("Start after Shutdown = %v, want errScenarioSchedulerStopped", err)
	}
}

func TestScenarioSchedulerShutdownWaitIsBoundedByContext(t *testing.T) {
	scheduler := newScenarioScheduler(nil)
	scheduler.sweepInterval = time.Millisecond
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	scheduler.runSweep = func() error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not enter the blocking sweep")
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	err := scheduler.Shutdown(shortCtx)
	shortCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded Shutdown = %v, want context deadline exceeded", err)
	}

	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := scheduler.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown after sweep release: %v", err)
	}
}

func TestScenarioSchedulerConcurrentLifecycleCannotResurrectWorker(t *testing.T) {
	scheduler := newScenarioScheduler(nil)
	scheduler.sweepInterval = time.Millisecond
	scheduler.runSweep = func() error { return nil }

	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := 0; i < 16; i++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			if err := scheduler.Start(context.Background()); err != nil && !errors.Is(err, errScenarioSchedulerStopped) {
				t.Errorf("concurrent Start: %v", err)
			}
		}()
		go func() {
			defer wait.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := scheduler.Shutdown(ctx); err != nil {
				t.Errorf("concurrent Shutdown: %v", err)
			}
		}()
	}
	close(start)
	wait.Wait()
	if err := scheduler.Start(context.Background()); !errors.Is(err, errScenarioSchedulerStopped) {
		t.Fatalf("final Start = %v, want stopped scheduler", err)
	}
}

func TestServerShutdownStopsOwnedScenarioScheduler(t *testing.T) {
	scheduler := newScenarioScheduler(nil)
	scheduler.sweepInterval = time.Millisecond
	firstSweep := make(chan struct{}, 1)
	scheduler.runSweep = func() error {
		select {
		case firstSweep <- struct{}{}:
		default:
		}
		return nil
	}
	server := &Server{scheduler: scheduler}
	if err := server.StartScheduler(context.Background()); err != nil {
		t.Fatalf("StartScheduler: %v", err)
	}
	select {
	case <-firstSweep:
	case <-time.After(time.Second):
		t.Fatal("server-owned scheduler did not sweep")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Server.Shutdown: %v", err)
	}
	if err := server.StartScheduler(context.Background()); !errors.Is(err, errScenarioSchedulerStopped) {
		t.Fatalf("StartScheduler after Server.Shutdown = %v, want stopped scheduler", err)
	}
}
