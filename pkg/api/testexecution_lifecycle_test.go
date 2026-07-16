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
	pauseErr     error
	resumeErr    error
	durationErr  error
	scaleErr     error
	stopErrs     []error
}

func TestUncertainRemoteMutationsStillStopWhenFailClosedPersistenceFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*TestRunner, *ManagedTest, testExecutionBackend) error
	}{
		{
			name: "pause",
			invoke: func(runner *TestRunner, test *ManagedTest, backend testExecutionBackend) error {
				return runner.failClosedUncertainPauseControl(test, "RUNNING", backend, errors.New("pause compensation unresolved"))
			},
		},
		{
			name: "duration",
			invoke: func(runner *TestRunner, test *ManagedTest, backend testExecutionBackend) error {
				return runner.failClosedUncertainDurationAdjustment(test, backend, 2*time.Minute, errors.New("duration compensation unresolved"))
			},
		},
		{
			name: "vusers",
			invoke: func(runner *TestRunner, test *ManagedTest, backend testExecutionBackend) error {
				return runner.failClosedUncertainVUserScale(&vuserScaleReservation{
					test: test, groupIndex: 0,
					oldGroup: TestGroup{Name: "group", VUsers: 5},
					newGroup: TestGroup{Name: "group", VUsers: 10},
				}, backend, errors.New("scale compensation unresolved"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			runner.testStore = &failingLifecycleStore{metricsStoreSpy: &metricsStoreSpy{}, err: errors.New("database unavailable")}
			backend := &lifecycleTransitionBackend{}
			ctx, cancel := context.WithCancel(context.Background())
			test := &ManagedTest{
				ID: "uncertain-" + tc.name, Status: "RUNNING", Duration: time.Minute,
				Groups: []TestGroup{{Name: "group", VUsers: 10}}, TotalVUsers: 10,
				cancel: cancel, executionCtx: ctx,
			}
			runner.tests[test.ID] = test

			if err := tc.invoke(runner, test, backend); err == nil || !strings.Contains(err.Error(), "database unavailable") {
				t.Fatalf("fail-closed error = %v, want persistence failure", err)
			}
			if test.Status != "STOPPING" {
				t.Fatalf("status = %s, want STOPPING", test.Status)
			}
			if ctx.Err() == nil {
				t.Fatal("execution context was not canceled")
			}
			_, stopCalls := backend.counts()
			if stopCalls != 1 {
				t.Fatalf("stop calls = %d, want 1", stopCalls)
			}
		})
	}
}

func TestUncertainRemoteMutationsRetryFailedTeardown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*TestRunner, *ManagedTest, testExecutionBackend) error
	}{
		{
			name: "pause",
			invoke: func(runner *TestRunner, test *ManagedTest, backend testExecutionBackend) error {
				return runner.failClosedUncertainPauseControl(test, "RUNNING", backend, errors.New("pause compensation unresolved"))
			},
		},
		{
			name: "duration",
			invoke: func(runner *TestRunner, test *ManagedTest, backend testExecutionBackend) error {
				return runner.failClosedUncertainDurationAdjustment(test, backend, 2*time.Minute, errors.New("duration compensation unresolved"))
			},
		},
		{
			name: "vusers",
			invoke: func(runner *TestRunner, test *ManagedTest, backend testExecutionBackend) error {
				return runner.failClosedUncertainVUserScale(&vuserScaleReservation{
					test: test, groupIndex: 0,
					oldGroup: TestGroup{Name: "group", VUsers: 5},
					newGroup: TestGroup{Name: "group", VUsers: 10},
				}, backend, errors.New("scale compensation unresolved"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantStopErr := errors.New("temporary stop failure")
			backend := &lifecycleTransitionBackend{stopErrs: []error{wantStopErr}}
			runner := NewTestRunner(1, NewHub(), nil)
			ctx, cancel := context.WithCancel(context.Background())
			test := &ManagedTest{
				ID: "uncertain-stop-retry-" + tc.name, Status: "RUNNING", Duration: time.Minute,
				Groups: []TestGroup{{Name: "group", VUsers: 10}}, TotalVUsers: 10,
				cancel: cancel, executionCtx: ctx, executionBackend: backend,
			}
			runner.tests[test.ID] = test

			if err := tc.invoke(runner, test, backend); !errors.Is(err, wantStopErr) {
				t.Fatalf("fail-closed error = %v, want stop failure", err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				_, stopCalls := backend.counts()
				if stopCalls >= 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("backend stop calls = %d, want automatic retry", stopCalls)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestCompensatedAdjustmentRollbackPersistenceFailureStopsFailClosed(t *testing.T) {
	wantBackendErr := errors.New("remote adjustment failed after compensation")
	wantStoreErr := errors.New("database unavailable")
	for _, tc := range []struct {
		name      string
		configure func(*lifecycleTransitionBackend)
		invoke    func(*TestRunner, string) error
		newTest   func(context.Context, context.CancelFunc, *lifecycleTransitionBackend) *ManagedTest
		assertMax func(*testing.T, *ManagedTest, *storage.TestRecord)
	}{
		{
			name:      "downscale",
			configure: func(backend *lifecycleTransitionBackend) { backend.scaleErr = wantBackendErr },
			invoke: func(runner *TestRunner, id string) error {
				return runner.ScaleVUsers(id, "group", 10)
			},
			newTest: func(ctx context.Context, cancel context.CancelFunc, backend *lifecycleTransitionBackend) *ManagedTest {
				return &ManagedTest{
					ID: "rollback-persist-downscale", Status: "RUNNING", Duration: time.Hour,
					Groups: []TestGroup{{Name: "group", VUsers: 100}}, TotalVUsers: 100,
					executionCtx: ctx, cancel: cancel, executionBackend: backend,
				}
			},
			assertMax: func(t *testing.T, test *ManagedTest, saved *storage.TestRecord) {
				t.Helper()
				if test.TotalVUsers != 100 || test.Groups[0].VUsers != 100 {
					t.Fatalf("fail-closed VUser reservation = total:%d group:%d, want 100", test.TotalVUsers, test.Groups[0].VUsers)
				}
				if saved.TotalVUsers != 100 {
					t.Fatalf("durable fail-closed VUser reservation = %d, want 100", saved.TotalVUsers)
				}
			},
		},
		{
			name:      "shorten duration",
			configure: func(backend *lifecycleTransitionBackend) { backend.durationErr = wantBackendErr },
			invoke: func(runner *TestRunner, id string) error {
				return runner.AdjustDuration(id, 5*time.Minute)
			},
			newTest: func(ctx context.Context, cancel context.CancelFunc, backend *lifecycleTransitionBackend) *ManagedTest {
				return &ManagedTest{
					ID: "rollback-persist-duration", Status: "RUNNING", Duration: time.Hour, DurationStr: time.Hour.String(),
					Groups: []TestGroup{{Name: "group", VUsers: 1}}, TotalVUsers: 1,
					executionCtx: ctx, cancel: cancel, executionBackend: backend,
				}
			},
			assertMax: func(t *testing.T, test *ManagedTest, saved *storage.TestRecord) {
				t.Helper()
				if test.Duration != time.Hour {
					t.Fatalf("fail-closed duration reservation = %s, want 1h", test.Duration)
				}
				if time.Duration(saved.Duration) != time.Hour {
					t.Fatalf("durable fail-closed duration reservation = %s, want 1h", time.Duration(saved.Duration))
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseStore := &metricsStoreSpy{}
			runner := NewTestRunner(1, NewHub(), nil)
			runner.testStore = &scriptedTestPersistence{
				testPersistence: baseStore,
				err:             wantStoreErr,
				// Call 1 reserves the proposed value. Calls 2-4 are the
				// failed durable rollback. The fail-closed max reservation on
				// call 5 must still be attempted and succeeds.
				failSaveCalls: failCalls(2, durableTransitionAttempts),
			}
			backend := &lifecycleTransitionBackend{}
			tc.configure(backend)
			runner.setExecutionBackend(backend)
			ctx, cancel := context.WithCancel(context.Background())
			test := tc.newTest(ctx, cancel, backend)
			runner.tests[test.ID] = test

			err := tc.invoke(runner, test.ID)
			if !errors.Is(err, wantBackendErr) || !errors.Is(err, wantStoreErr) {
				t.Fatalf("adjustment error = %v, want backend and rollback persistence failures", err)
			}
			if test.Status != "STOPPING" {
				t.Fatalf("status = %s, want STOPPING", test.Status)
			}
			if ctx.Err() == nil {
				t.Fatal("execution context was not canceled")
			}
			_, stopCalls := backend.counts()
			if stopCalls != 1 {
				t.Fatalf("backend stop calls = %d, want 1", stopCalls)
			}
			if len(baseStore.savedTests) < 2 {
				t.Fatalf("successful durable saves = %d, want reservation and fail-closed state", len(baseStore.savedTests))
			}
			last := baseStore.savedTests[len(baseStore.savedTests)-1]
			if last.Status != "STOPPING" {
				t.Fatalf("last durable status = %s, want STOPPING", last.Status)
			}
			tc.assertMax(t, test, last)
		})
	}
}

func (b *lifecycleTransitionBackend) Run(_ *TestRunner, _ *ManagedTest) {
	b.mu.Lock()
	b.runCalls++
	b.mu.Unlock()
}

func (b *lifecycleTransitionBackend) StopTest(_ *TestRunner, _ *ManagedTest) error {
	b.mu.Lock()
	var err error
	if len(b.stopErrs) > 0 {
		err = b.stopErrs[0]
		b.stopErrs = b.stopErrs[1:]
	}
	b.stopCalls++
	b.mu.Unlock()
	return err
}

func (b *lifecycleTransitionBackend) StopTestContext(_ context.Context, tr *TestRunner, test *ManagedTest) error {
	return b.StopTest(tr, test)
}

func (b *lifecycleTransitionBackend) PauseTest(tr *TestRunner, test *ManagedTest) error {
	if b.pauseHook != nil {
		b.pauseHook(tr, test)
	}
	return b.pauseErr
}

func (b *lifecycleTransitionBackend) ResumeTest(tr *TestRunner, test *ManagedTest) error {
	if b.resumeHook != nil {
		b.resumeHook(tr, test)
	}
	return b.resumeErr
}

func TestPauseResumePersistenceRollbackFailureStopsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		failStatus string
		configure  func(*lifecycleTransitionBackend)
		invoke     func(*TestRunner, string) error
	}{
		{
			name: "pause", status: "RUNNING", failStatus: "PAUSED",
			configure: func(backend *lifecycleTransitionBackend) { backend.resumeErr = errors.New("resume rollback failed") },
			invoke:    func(runner *TestRunner, id string) error { return runner.PauseTest(id) },
		},
		{
			name: "resume", status: "PAUSED", failStatus: "RUNNING",
			configure: func(backend *lifecycleTransitionBackend) { backend.pauseErr = errors.New("pause rollback failed") },
			invoke:    func(runner *TestRunner, id string) error { return runner.ResumeTest(id) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			runner.testStore = &statusFailingLifecycleStore{
				metricsStoreSpy: &metricsStoreSpy{}, status: tc.failStatus, err: errors.New("database unavailable"),
			}
			backend := &lifecycleTransitionBackend{}
			tc.configure(backend)
			runner.setExecutionBackend(backend)
			test := &ManagedTest{ID: "persist-rollback-" + tc.name, Status: tc.status, Duration: time.Minute}
			if tc.status == "PAUSED" {
				pausedAt := time.Now().Add(-time.Second)
				test.PausedAt = &pausedAt
			}
			runner.tests[test.ID] = test

			if err := tc.invoke(runner, test.ID); err == nil || !strings.Contains(err.Error(), "rollback failed") {
				t.Fatalf("control error = %v, want rollback failure", err)
			}
			if test.Status != "STOPPING" {
				t.Fatalf("status = %s, want STOPPING", test.Status)
			}
			_, stopCalls := backend.counts()
			if stopCalls != 1 {
				t.Fatalf("stop calls = %d, want 1", stopCalls)
			}
		})
	}
}

func (b *lifecycleTransitionBackend) AdjustDuration(tr *TestRunner, test *ManagedTest, _ time.Duration) error {
	if b.durationHook != nil {
		b.durationHook(tr, test)
	}
	return b.durationErr
}

func (b *lifecycleTransitionBackend) ScaleVUsers(_ *TestRunner, _ *ManagedTest, _ string, _ int) error {
	return b.scaleErr
}

func (b *lifecycleTransitionBackend) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runCalls, b.stopCalls
}

func TestStartTestDoesNotLaunchWhenInitializingPersistenceFails(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil, withUnfencedDeploymentLeaseStartForFaultInjection())
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

func TestPauseCompensatesAndDurationAvoidsRemoteMutationWhenPersistenceFails(t *testing.T) {
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
	if test.Duration != time.Minute || test.DurationStr != "1m0s" || durationCalls != 0 {
		t.Fatalf("duration reservation failure left %s/%q with %d backend calls, want original state and no remote mutation", test.Duration, test.DurationStr, durationCalls)
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

func TestSecondStopRetriesFailClosedStoppingTeardown(t *testing.T) {
	wantStopErr := errors.New("backend stop was not applied")
	wantStoreErr := errors.New("database unavailable")
	runner := NewTestRunner(1, NewHub(), nil)
	runner.testStore = &statusFailingLifecycleStore{
		metricsStoreSpy: &metricsStoreSpy{},
		status:          "RUNNING",
		err:             wantStoreErr,
	}
	backend := &lifecycleTransitionBackend{stopErrs: []error{wantStopErr}}
	runner.setExecutionBackend(backend)
	ctx, cancel := context.WithCancel(context.Background())
	test := &ManagedTest{
		ID: "retry-fail-closed-stop", Status: "RUNNING", Duration: time.Minute,
		executionCtx: ctx, cancel: cancel, executionBackend: backend,
	}
	runner.tests[test.ID] = test

	err := runner.StopTest(test.ID)
	if !errors.Is(err, wantStopErr) || !errors.Is(err, wantStoreErr) {
		t.Fatalf("first StopTest error = %v, want backend and rollback persistence failures", err)
	}
	if test.Status != "STOPPING" {
		t.Fatalf("status after failed rollback persistence = %s, want STOPPING", test.Status)
	}
	if ctx.Err() != nil {
		t.Fatal("known-not-applied first stop unexpectedly canceled execution before teardown retry")
	}

	if err := runner.StopTest(test.ID); err != nil {
		t.Fatalf("second StopTest did not retry teardown: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("successful second stop did not cancel execution")
	}
	_, stopCalls := backend.counts()
	if stopCalls != 2 {
		t.Fatalf("backend stop calls = %d, want 2", stopCalls)
	}
	// Prevent the already-enqueued safety worker from issuing an unnecessary
	// third call after this synchronous retry proved teardown succeeded.
	runner.mu.Lock()
	runner.shuttingDown = true
	runner.mu.Unlock()
}
