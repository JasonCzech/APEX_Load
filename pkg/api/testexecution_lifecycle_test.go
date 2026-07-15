package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type failingLifecycleStore struct {
	*metricsStoreSpy
	err error
}

func (s *failingLifecycleStore) SaveTest(*storage.TestRecord) error { return s.err }

type statusFailingLifecycleStore struct {
	*metricsStoreSpy
	status string
	err    error
}

func (s *statusFailingLifecycleStore) SaveTest(record *storage.TestRecord) error {
	if record != nil && record.Status == s.status {
		return s.err
	}
	return s.metricsStoreSpy.SaveTest(record)
}

type contextBlockingStopBackend struct {
	mu    sync.Mutex
	calls int
}

func (*contextBlockingStopBackend) Run(*TestRunner, *ManagedTest) {}

func (b *contextBlockingStopBackend) StopTestContext(ctx context.Context, _ *TestRunner, _ *ManagedTest) error {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

type lifecycleTransitionBackend struct {
	mu           sync.Mutex
	runCalls     int
	stopCalls    int
	pauseHook    func(*TestRunner, *ManagedTest)
	resumeHook   func(*TestRunner, *ManagedTest)
	durationHook func(*TestRunner, *ManagedTest)
}

func (b *lifecycleTransitionBackend) Run(_ *TestRunner, _ *ManagedTest) {
	b.mu.Lock()
	b.runCalls++
	b.mu.Unlock()
}

func (b *lifecycleTransitionBackend) StopTest(_ *TestRunner, _ *ManagedTest) error {
	b.mu.Lock()
	b.stopCalls++
	b.mu.Unlock()
	return nil
}

func (b *lifecycleTransitionBackend) StopTestContext(_ context.Context, tr *TestRunner, test *ManagedTest) error {
	return b.StopTest(tr, test)
}

func (b *lifecycleTransitionBackend) PauseTest(tr *TestRunner, test *ManagedTest) error {
	if b.pauseHook != nil {
		b.pauseHook(tr, test)
	}
	return nil
}

func (b *lifecycleTransitionBackend) ResumeTest(tr *TestRunner, test *ManagedTest) error {
	if b.resumeHook != nil {
		b.resumeHook(tr, test)
	}
	return nil
}

func (b *lifecycleTransitionBackend) AdjustDuration(tr *TestRunner, test *ManagedTest, _ time.Duration) error {
	if b.durationHook != nil {
		b.durationHook(tr, test)
	}
	return nil
}

func (b *lifecycleTransitionBackend) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runCalls, b.stopCalls
}

func TestStartTestDoesNotLaunchWhenInitializingPersistenceFails(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &lifecycleTransitionBackend{}
	runner.setExecutionBackend(backend)
	wantErr := errors.New("database unavailable")
	runner.testStore = &failingLifecycleStore{metricsStoreSpy: &metricsStoreSpy{}, err: wantErr}
	test := &ManagedTest{ID: "start-persistence-failure", Name: "start-persistence-failure", Status: "QUEUED", Duration: time.Minute}
	runner.tests[test.ID] = test

	if err := runner.StartTest(test.ID); !errors.Is(err, wantErr) {
		t.Fatalf("StartTest error = %v, want %v", err, wantErr)
	}
	if test.Status != "QUEUED" || test.executionCtx != nil || test.cancel != nil {
		t.Fatalf("test state after failed persistence = status %s, context %v, cancel %v", test.Status, test.executionCtx, test.cancel)
	}
	if runs, _ := backend.counts(); runs != 0 {
		t.Fatalf("backend run calls = %d, want 0", runs)
	}
}

func TestCompleteTestRunClearsLocalLivenessMarkers(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	test := &ManagedTest{ID: "completed", Status: "RUNNING", ProjectID: storage.DefaultProjectID, executionCtx: ctx, cancel: cancel}
	runner.tests[test.ID] = test
	if err := runner.completeTestRun(test, "COMPLETE", time.Now(), "", nil); err != nil {
		t.Fatal(err)
	}
	if test.cancel != nil || test.executionCtx != nil {
		t.Fatalf("terminal test retained local liveness markers: context=%v cancel=%v", test.executionCtx, test.cancel)
	}
}

func TestStopTestDoesNotCancelInitializingRunWhenAbortPersistenceFails(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	wantErr := errors.New("database unavailable")
	runner.testStore = &failingLifecycleStore{metricsStoreSpy: &metricsStoreSpy{}, err: wantErr}
	executionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID:           "stop-persistence-failure",
		Name:         "stop-persistence-failure",
		Status:       "INITIALIZING",
		executionCtx: executionCtx,
		cancel:       cancel,
	}
	runner.tests[test.ID] = test

	if err := runner.StopTest(test.ID); !errors.Is(err, wantErr) {
		t.Fatalf("StopTest error = %v, want %v", err, wantErr)
	}
	if test.Status != "INITIALIZING" {
		t.Fatalf("status = %s, want INITIALIZING", test.Status)
	}
	select {
	case <-executionCtx.Done():
		t.Fatal("execution context was cancelled despite failed abort persistence")
	default:
	}
}

func TestPauseAndDurationCompensateWhenPersistenceFails(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	wantErr := errors.New("database unavailable")
	runner.testStore = &failingLifecycleStore{metricsStoreSpy: &metricsStoreSpy{}, err: wantErr}
	resumeCalls := 0
	durationCalls := 0
	backend := &lifecycleTransitionBackend{
		resumeHook: func(*TestRunner, *ManagedTest) { resumeCalls++ },
		durationHook: func(*TestRunner, *ManagedTest) {
			durationCalls++
		},
	}
	runner.setExecutionBackend(backend)
	test := &ManagedTest{ID: "control-persistence-failure", Name: "control-persistence-failure", Status: "RUNNING", Duration: time.Minute, DurationStr: "1m0s", executionBackend: backend}
	runner.tests[test.ID] = test

	if err := runner.PauseTest(test.ID); !errors.Is(err, wantErr) {
		t.Fatalf("PauseTest error = %v, want %v", err, wantErr)
	}
	if test.Status != "RUNNING" || resumeCalls != 1 {
		t.Fatalf("pause compensation left status %s with %d resume calls", test.Status, resumeCalls)
	}
	if err := runner.AdjustDuration(test.ID, 2*time.Minute); !errors.Is(err, wantErr) {
		t.Fatalf("AdjustDuration error = %v, want %v", err, wantErr)
	}
	if test.Duration != time.Minute || test.DurationStr != "1m0s" || durationCalls != 2 {
		t.Fatalf("duration compensation = %s/%q with %d backend calls", test.Duration, test.DurationStr, durationCalls)
	}
}

func TestStopActiveTestsHonorsDeadlineAcrossBlockingBackends(t *testing.T) {
	runner := NewTestRunner(4, NewHub(), nil)
	backend := &contextBlockingStopBackend{}
	for index := 0; index < 3; index++ {
		executionCtx, cancel := context.WithCancel(context.Background())
		test := &ManagedTest{
			ID:               fmt.Sprintf("blocking-stop-%d", index),
			Status:           "RUNNING",
			executionCtx:     executionCtx,
			cancel:           cancel,
			executionBackend: backend,
		}
		runner.tests[test.ID] = test
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runner.stopActiveTests(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stopActiveTests error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("stopActiveTests exceeded bounded deadline: %s", elapsed)
	}
	backend.mu.Lock()
	calls := backend.calls
	backend.mu.Unlock()
	if calls != 3 {
		t.Fatalf("context stop calls = %d, want 3", calls)
	}
}

type legacyBlockingStopBackend struct {
	called  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*legacyBlockingStopBackend) Run(*TestRunner, *ManagedTest) {}

func (b *legacyBlockingStopBackend) StopTest(*TestRunner, *ManagedTest) error {
	b.once.Do(func() { close(b.called) })
	<-b.release
	return nil
}

func TestStopActiveTestsDoesNotLaunchUnboundedLegacyStop(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &legacyBlockingStopBackend{called: make(chan struct{}), release: make(chan struct{})}
	defer close(backend.release)
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	defer cancelExecution()
	test := &ManagedTest{
		ID:               "legacy-bounded-shutdown",
		Status:           "RUNNING",
		executionCtx:     executionCtx,
		cancel:           cancelExecution,
		executionBackend: backend,
	}
	runner.tests[test.ID] = test

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := runner.stopActiveTests(ctx); err != nil {
		t.Fatalf("stopActiveTests: %v", err)
	}
	select {
	case <-backend.called:
		t.Fatal("bounded shutdown invoked a legacy stop with no context contract")
	default:
	}
	select {
	case <-executionCtx.Done():
	default:
		t.Fatal("bounded shutdown did not cancel the durably stopping run")
	}
	if test.Status != "STOPPING" {
		t.Fatalf("status = %s, want STOPPING", test.Status)
	}
}

func TestStopActiveTestsDoesNotCancelWhenStopPersistenceFails(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	wantErr := errors.New("database unavailable")
	runner.testStore = &failingLifecycleStore{metricsStoreSpy: &metricsStoreSpy{}, err: wantErr}
	executionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID:           "shutdown-persistence-failure",
		Status:       "RUNNING",
		executionCtx: executionCtx,
		cancel:       cancel,
	}
	runner.tests[test.ID] = test

	if err := runner.stopActiveTests(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("stopActiveTests error = %v, want %v", err, wantErr)
	}
	if test.Status != "RUNNING" {
		t.Fatalf("status = %s, want RUNNING after persistence rollback", test.Status)
	}
	select {
	case <-executionCtx.Done():
		t.Fatal("shutdown cancelled execution before the stop transition persisted")
	default:
	}
}

func TestStopWaitsForControlPersistenceCompensation(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	wantErr := errors.New("database unavailable")
	runner.testStore = &statusFailingLifecycleStore{metricsStoreSpy: &metricsStoreSpy{}, status: "PAUSED", err: wantErr}
	compensationStarted := make(chan struct{})
	releaseCompensation := make(chan struct{})
	backend := &lifecycleTransitionBackend{
		resumeHook: func(*TestRunner, *ManagedTest) {
			close(compensationStarted)
			<-releaseCompensation
		},
	}
	executionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{
		ID:               "control-compensation-stop",
		Status:           "RUNNING",
		Duration:         time.Minute,
		DurationStr:      time.Minute.String(),
		executionCtx:     executionCtx,
		cancel:           cancel,
		executionBackend: backend,
	}
	runner.tests[test.ID] = test

	pauseDone := make(chan error, 1)
	go func() { pauseDone <- runner.PauseTest(test.ID) }()
	select {
	case <-compensationStarted:
	case <-time.After(time.Second):
		t.Fatal("pause persistence compensation did not start")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- runner.StopTest(test.ID) }()
	select {
	case err := <-stopDone:
		t.Fatalf("stop completed before compensation was safe: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCompensation)
	if err := <-pauseDone; !errors.Is(err, wantErr) {
		t.Fatalf("PauseTest error = %v, want %v", err, wantErr)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("StopTest returned error: %v", err)
	}
	if _, stops := backend.counts(); stops != 1 {
		t.Fatalf("backend stop calls = %d, want 1", stops)
	}
	if test.Status != "STOPPING" {
		t.Fatalf("status = %s, want STOPPING", test.Status)
	}
}

func TestShutdownAdmissionGateRejectsStartsAndPromotion(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &lifecycleTransitionBackend{}
	runner.setExecutionBackend(backend)
	queued := &ManagedTest{ID: "queued-at-shutdown", Status: "QUEUED", Duration: time.Minute}
	pending := &ManagedTest{ID: "pending-at-shutdown", Status: "PENDING", Duration: time.Minute}
	runner.tests[queued.ID] = queued
	runner.tests[pending.ID] = pending

	runner.beginShutdown()
	if err := runner.StartTest(queued.ID); !errors.Is(err, errRunnerShuttingDown) {
		t.Fatalf("StartTest error = %v, want shutdown admission error", err)
	}
	runner.promotePendingTests()
	if queued.Status != "QUEUED" || pending.Status != "PENDING" {
		t.Fatalf("shutdown changed queued statuses to %s/%s", queued.Status, pending.Status)
	}
	if runs, _ := backend.counts(); runs != 0 {
		t.Fatalf("backend run calls = %d, want 0", runs)
	}
}

func TestStopTestAbortsInitializingRunBeforeBackendDispatch(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &lifecycleTransitionBackend{}
	runner.setExecutionBackend(backend)

	executionCtx, cancel := context.WithCancel(context.Background())
	test := &ManagedTest{
		ID:           "initializing-stop",
		Name:         "initializing-stop",
		Status:       "INITIALIZING",
		executionCtx: executionCtx,
		cancel:       cancel,
	}
	runner.tests[test.ID] = test

	if err := runner.StopTest(test.ID); err != nil {
		t.Fatalf("StopTest returned error: %v", err)
	}
	if test.Status != "ABORTED" {
		t.Fatalf("status = %s, want ABORTED", test.Status)
	}
	select {
	case <-executionCtx.Done():
	default:
		t.Fatal("initializing execution context was not cancelled")
	}

	// A goroutine already queued by StartTest must re-check the atomic state and
	// decline to dispatch after the stop wins the transition.
	runner.runTest(test)
	if runs, _ := backend.counts(); runs != 0 {
		t.Fatalf("backend Run calls = %d, want 0", runs)
	}

	// A late startup error (for example an AssignWork response racing the stop)
	// must not replace the user's terminal decision.
	runner.failTestBeforeRun(test, "late generator startup failure")
	if test.Status != "ABORTED" {
		t.Fatalf("late startup failure changed status to %s, want ABORTED", test.Status)
	}
}

func TestControlTransitionsDoNotOverwriteConcurrentStop(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		invoke      func(*TestRunner, string) error
		installHook func(*lifecycleTransitionBackend, func(*TestRunner, *ManagedTest))
	}{
		{
			name:   "pause",
			status: "RUNNING",
			invoke: func(tr *TestRunner, id string) error { return tr.PauseTest(id) },
			installHook: func(b *lifecycleTransitionBackend, hook func(*TestRunner, *ManagedTest)) {
				b.pauseHook = hook
			},
		},
		{
			name:   "resume",
			status: "PAUSED",
			invoke: func(tr *TestRunner, id string) error { return tr.ResumeTest(id) },
			installHook: func(b *lifecycleTransitionBackend, hook func(*TestRunner, *ManagedTest)) {
				b.resumeHook = hook
			},
		},
		{
			name:   "duration",
			status: "RUNNING",
			invoke: func(tr *TestRunner, id string) error { return tr.AdjustDuration(id, 2*time.Minute) },
			installHook: func(b *lifecycleTransitionBackend, hook func(*TestRunner, *ManagedTest)) {
				b.durationHook = hook
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			backend := &lifecycleTransitionBackend{}
			test := &ManagedTest{
				ID:               "transition-" + tc.name,
				Name:             tc.name,
				Status:           tc.status,
				Duration:         time.Minute,
				DurationStr:      time.Minute.String(),
				executionBackend: backend,
			}
			runner.tests[test.ID] = test
			tc.installHook(backend, func(tr *TestRunner, current *ManagedTest) {
				tr.mu.Lock()
				current.Status = "STOPPING"
				tr.mu.Unlock()
			})

			err := tc.invoke(runner, test.ID)
			if err == nil || !strings.Contains(err.Error(), "STOPPING") {
				t.Fatalf("transition error = %v, want concurrent STOPPING error", err)
			}
			if test.Status != "STOPPING" {
				t.Fatalf("status = %s, want STOPPING", test.Status)
			}
		})
	}
}

func TestLifecycleControlsCompensateWhenRemoteFinalizationBegins(t *testing.T) {
	t.Run("pause", func(t *testing.T) {
		runner := NewTestRunner(1, NewHub(), nil)
		pauseCalls := 0
		resumeCalls := 0
		backend := &lifecycleTransitionBackend{}
		test := &ManagedTest{
			ID:               "finalizing-pause",
			Status:           "RUNNING",
			Duration:         time.Minute,
			DurationStr:      time.Minute.String(),
			executionBackend: backend,
		}
		backend.pauseHook = func(tr *TestRunner, current *ManagedTest) {
			pauseCalls++
			tr.mu.Lock()
			current.remoteFinalizing = true
			tr.mu.Unlock()
		}
		backend.resumeHook = func(*TestRunner, *ManagedTest) { resumeCalls++ }
		runner.tests[test.ID] = test

		err := runner.PauseTest(test.ID)
		if err == nil || !strings.Contains(err.Error(), "finalizing") {
			t.Fatalf("PauseTest error = %v, want finalizing error", err)
		}
		if pauseCalls != 1 || resumeCalls != 1 {
			t.Fatalf("pause/resume calls = %d/%d, want forward pause and compensating resume", pauseCalls, resumeCalls)
		}
		if test.Status != "RUNNING" {
			t.Fatalf("status = %s, want RUNNING", test.Status)
		}
	})

	t.Run("resume", func(t *testing.T) {
		runner := NewTestRunner(1, NewHub(), nil)
		pauseCalls := 0
		resumeCalls := 0
		backend := &lifecycleTransitionBackend{}
		test := &ManagedTest{
			ID:               "finalizing-resume",
			Status:           "PAUSED",
			Duration:         time.Minute,
			DurationStr:      time.Minute.String(),
			executionBackend: backend,
		}
		backend.resumeHook = func(tr *TestRunner, current *ManagedTest) {
			resumeCalls++
			tr.mu.Lock()
			current.remoteFinalizing = true
			tr.mu.Unlock()
		}
		backend.pauseHook = func(*TestRunner, *ManagedTest) { pauseCalls++ }
		runner.tests[test.ID] = test

		err := runner.ResumeTest(test.ID)
		if err == nil || !strings.Contains(err.Error(), "finalizing") {
			t.Fatalf("ResumeTest error = %v, want finalizing error", err)
		}
		if resumeCalls != 1 || pauseCalls != 1 {
			t.Fatalf("resume/pause calls = %d/%d, want forward resume and compensating pause", resumeCalls, pauseCalls)
		}
		if test.Status != "PAUSED" {
			t.Fatalf("status = %s, want PAUSED", test.Status)
		}
	})

	t.Run("duration", func(t *testing.T) {
		runner := NewTestRunner(1, NewHub(), nil)
		durationCalls := 0
		backend := &lifecycleTransitionBackend{}
		test := &ManagedTest{
			ID:               "finalizing-duration",
			Status:           "RUNNING",
			Duration:         time.Minute,
			DurationStr:      time.Minute.String(),
			executionBackend: backend,
		}
		backend.durationHook = func(tr *TestRunner, current *ManagedTest) {
			durationCalls++
			if durationCalls == 1 {
				tr.mu.Lock()
				current.remoteFinalizing = true
				tr.mu.Unlock()
			}
		}
		runner.tests[test.ID] = test

		err := runner.AdjustDuration(test.ID, 2*time.Minute)
		if err == nil || !strings.Contains(err.Error(), "finalizing") {
			t.Fatalf("AdjustDuration error = %v, want finalizing error", err)
		}
		if durationCalls != 2 {
			t.Fatalf("duration backend calls = %d, want forward adjustment and compensation", durationCalls)
		}
		if test.Duration != time.Minute || test.DurationStr != time.Minute.String() {
			t.Fatalf("controller duration = %s/%q, want original duration", test.Duration, test.DurationStr)
		}
	})
}

func TestStopActiveTestsStopsBackendAndCancelsRun(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &lifecycleTransitionBackend{}
	executionCtx, cancel := context.WithCancel(context.Background())
	test := &ManagedTest{
		ID:               "active-shutdown",
		Name:             "active-shutdown",
		Status:           "RUNNING",
		executionCtx:     executionCtx,
		cancel:           cancel,
		executionBackend: backend,
	}
	runner.tests[test.ID] = test

	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := runner.stopActiveTests(ctx); err != nil {
		t.Fatalf("stopActiveTests returned error: %v", err)
	}
	if _, stops := backend.counts(); stops != 1 {
		t.Fatalf("backend StopTest calls = %d, want 1", stops)
	}
	select {
	case <-executionCtx.Done():
	default:
		t.Fatal("active execution context was not cancelled")
	}
	if test.Status != "STOPPING" {
		t.Fatalf("status = %s, want STOPPING", test.Status)
	}
}
