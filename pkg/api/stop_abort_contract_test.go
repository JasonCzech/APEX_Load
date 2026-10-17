package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type cancelAwareGracefulOnlyBackend struct {
	calls     atomic.Int64
	returned  chan struct{}
	returnOne sync.Once
	err       error
}

type coordinatedRetryModeBackend struct {
	mu              sync.Mutex
	forceModes      []bool
	gracefulStarted chan struct{}
	releaseGraceful chan struct{}
	forceCalled     chan struct{}
	gracefulOnce    sync.Once
	forceOnce       sync.Once
}

type expiredGenerationStopBackend struct {
	calls       atomic.Int64
	abortCalls  atomic.Int64
	abortCalled chan struct{}
	abortOnce   sync.Once
}

type mixedGenerationStopBackend struct {
	calls atomic.Int64
}

type missingTargetStopBackend struct {
	calls atomic.Int64
}

type blockingExternalFenceStore struct {
	SQLiteStore *storage.SQLiteStore
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
}

func (s *blockingExternalFenceStore) SaveTest(record *storage.TestRecord) error {
	if strings.Contains(record.Error, stopTeardownExternalFenceMessage) {
		s.once.Do(func() {
			close(s.entered)
			<-s.release
		})
	}
	return s.SQLiteStore.SaveTest(record)
}

func (*expiredGenerationStopBackend) Run(*TestRunner, *ManagedTest) {}

func (b *expiredGenerationStopBackend) StopTestContext(context.Context, *TestRunner, *ManagedTest) error {
	b.calls.Add(1)
	failure := grpcStatus.Error(codes.FailedPrecondition, generatorrpc.ExecutionGenerationSessionExpiredMessage)
	return &distributedStopFanoutError{
		attemptedTargets: []string{"gen-a"}, failedErrors: []error{failure}, err: failure,
	}
}

func (b *expiredGenerationStopBackend) AbortTestContext(context.Context, *TestRunner, *ManagedTest) error {
	b.abortCalls.Add(1)
	if b.abortCalled != nil {
		b.abortOnce.Do(func() { close(b.abortCalled) })
	}
	return nil
}

func (*mixedGenerationStopBackend) Run(*TestRunner, *ManagedTest) {}

func (*missingTargetStopBackend) Run(*TestRunner, *ManagedTest) {}

func (b *missingTargetStopBackend) StopTestContext(context.Context, *TestRunner, *ManagedTest) error {
	b.calls.Add(1)
	return errDistributedGeneratorTargetUnknown
}

func (b *mixedGenerationStopBackend) StopTestContext(context.Context, *TestRunner, *ManagedTest) error {
	call := b.calls.Add(1)
	expired := grpcStatus.Error(codes.FailedPrecondition, generatorrpc.ExecutionGenerationSessionExpiredMessage)
	if call == 1 {
		unavailable := grpcStatus.Error(codes.Unavailable, "temporarily unavailable")
		return &distributedStopFanoutError{
			attemptedTargets: []string{"gen-a", "gen-b"},
			failedErrors:     []error{expired, unavailable},
			err:              errors.Join(expired, unavailable),
		}
	}
	return &distributedStopFanoutError{
		attemptedTargets:    []string{"gen-a", "gen-b"},
		acknowledgedTargets: []string{"gen-b"},
		failedErrors:        []error{expired},
		err:                 expired,
	}
}

func TestMixedStopFailuresRetryTransientTargetBeforeRetiring(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backend := &mixedGenerationStopBackend{}
	runner := NewTestRunner(1, NewHub(), store)
	test := &ManagedTest{ID: "mixed-stop", Name: "mixed stop", Status: "STOPPING", executionBackend: backend}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, Status: test.Status, StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	runner.tests[test.ID] = test
	runner.enqueueStopTeardownRetry(test.ID, false)
	deadline := time.Now().Add(3 * time.Second)
	for {
		runner.terminalRetryMu.Lock()
		_, pending := runner.terminalRetrying["stop-teardown:"+test.ID]
		runner.terminalRetryMu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mixed stop retry did not retire after the transient target acknowledged")
		}
		time.Sleep(time.Millisecond)
	}
	if calls := backend.calls.Load(); calls != 2 {
		t.Fatalf("stop attempts = %d, want two attempts including retry of transient target", calls)
	}
	row, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "STOPPING" || !strings.HasPrefix(row.Error, stopTeardownExternalFenceMessage) {
		t.Fatalf("durable unconfirmed stop = %+v", row)
	}
}

func TestMissingGeneratorTargetStopRetryRetainsExternalFenceState(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backend := &missingTargetStopBackend{}
	runner := NewTestRunner(1, NewHub(), store)
	test := &ManagedTest{ID: "target-unknown", Name: "target unknown", Status: "STOPPING", executionBackend: backend}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, Status: test.Status, StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	runner.tests[test.ID] = test
	runner.enqueueStopTeardownRetry(test.ID, false)
	deadline := time.Now().Add(2 * time.Second)
	for {
		runner.terminalRetryMu.Lock()
		_, pending := runner.terminalRetrying["stop-teardown:"+test.ID]
		runner.terminalRetryMu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("missing-target stop retry was not retired")
		}
		time.Sleep(time.Millisecond)
	}
	row, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if backend.calls.Load() != 1 || row.Status != "STOPPING" || !strings.HasPrefix(row.Error, stopTeardownExternalFenceMessage) {
		t.Fatalf("missing-target stop = calls %d, row %+v", backend.calls.Load(), row)
	}
}

func TestExpiredGenerationStopRetryLeavesDurableActionableStoppingState(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backend := &expiredGenerationStopBackend{}
	runner := NewTestRunner(1, NewHub(), store)
	test := &ManagedTest{ID: "unconfirmed-stop", Name: "unconfirmed stop", Status: "STOPPING", Error: "prior stop request failed; password=supersecret" + strings.Repeat("x", 4096), executionBackend: backend}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, Status: test.Status, Error: test.Error, StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	runner.tests[test.ID] = test
	runner.enqueueStopTeardownRetry(test.ID, false)
	deadline := time.Now().Add(2 * time.Second)
	for {
		runner.terminalRetryMu.Lock()
		_, pending := runner.terminalRetrying["stop-teardown:"+test.ID]
		runner.terminalRetryMu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired-generation stop retry did not retire")
		}
		time.Sleep(time.Millisecond)
	}
	if calls := backend.calls.Load(); calls != 1 {
		t.Fatalf("stop calls = %d, want one failed permanent retry", calls)
	}
	row, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "STOPPING" || !strings.HasPrefix(row.Error, stopTeardownExternalFenceMessage) || !strings.Contains(row.Error, "prior stop request failed") || strings.Contains(row.Error, "supersecret") || len(row.Error) > 2048 {
		t.Fatalf("durable unconfirmed stop = %+v", row)
	}
}

func TestStopTeardownRetryHonorsForceUpgradeDuringFenceNotePersistence(t *testing.T) {
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &blockingExternalFenceStore{
		SQLiteStore: sqlite,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-store.release:
		default:
			close(store.release)
		}
	})
	backend := &expiredGenerationStopBackend{abortCalled: make(chan struct{})}
	runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
	test := &ManagedTest{ID: "late-force-upgrade", Name: "late force upgrade", Status: "STOPPING", executionBackend: backend}
	if err := sqlite.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, Status: test.Status, StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	runner.tests[test.ID] = test
	runner.enqueueStopTeardownRetry(test.ID, false)
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("failed stop did not reach durable fence-note write")
	}
	// The first version check has already completed; the worker is paused
	// before its final retirement decision.
	runner.enqueueStopTeardownRetry(test.ID, true)
	close(store.release)
	select {
	case <-backend.abortCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("force abort was lost during fence-note persistence")
	}
	deadline := time.Now().Add(time.Second)
	for {
		runner.terminalRetryMu.Lock()
		_, pending := runner.terminalRetrying["stop-teardown:"+test.ID]
		runner.terminalRetryMu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successful force retry did not retire")
		}
		time.Sleep(time.Millisecond)
	}
	if calls, aborts := backend.calls.Load(), backend.abortCalls.Load(); calls != 1 || aborts != 1 {
		t.Fatalf("stop/abort calls = %d/%d, want 1/1", calls, aborts)
	}
}

func TestDistributedStopWithoutRecordedTargetNeedsExternalFence(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, nil)
	test := &ManagedTest{ID: "target-unknown", Status: "STOPPING"}
	if err := backend.stopTestContext(context.Background(), runner, test, true); !errors.Is(err, errDistributedGeneratorTargetUnknown) {
		t.Fatalf("missing-target stop = %v, want unprovable target identity", err)
	}
}

func TestStopTeardownExternalFenceClassification(t *testing.T) {
	expired := grpcStatus.Error(codes.FailedPrecondition, generatorrpc.ExecutionGenerationSessionExpiredMessage)
	superseded := grpcStatus.Error(codes.FailedPrecondition, "execution generation 7 is not active; current generation is 8")
	unavailable := grpcStatus.Error(codes.Unavailable, "offline")
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "all expired", err: &distributedStopFanoutError{failedErrors: []error{expired, expired}, err: expired}, want: true},
		{name: "superseded", err: &distributedStopFanoutError{failedErrors: []error{superseded}, err: superseded}, want: true},
		{name: "mixed transient", err: &distributedStopFanoutError{failedErrors: []error{expired, unavailable}, err: errors.Join(expired, unavailable)}},
		{name: "token mismatch stopping", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.FailedPrecondition, "run supervision token does not match the stopping assignment")}}, want: true},
		{name: "token mismatch terminal", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.FailedPrecondition, "run supervision token does not match the terminal assignment")}}, want: true},
		{name: "token required stopping", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.FailedPrecondition, "run supervision token is required for the stopping assignment")}}, want: true},
		{name: "token required terminal", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.FailedPrecondition, "run supervision token is required for the terminal assignment")}}, want: true},
		{name: "supervision required", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.FailedPrecondition, "run supervision is required")}}, want: true},
		{name: "token mismatch assignment", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.FailedPrecondition, "run supervision token does not match the assignment")}}, want: true},
		{name: "unrelated precondition", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.FailedPrecondition, "a live unexpired run supervision lease is required")}}},
		{name: "wrong code", err: &distributedStopFanoutError{failedErrors: []error{grpcStatus.Error(codes.Unavailable, "run supervision token does not match the assignment")}}},
		{name: "transient", err: &distributedStopFanoutError{failedErrors: []error{unavailable}, err: unavailable}},
		{name: "unrelated", err: expired},
		{name: "missing target", err: errDistributedGeneratorTargetUnknown, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stopTeardownRequiresExternalFence(tc.err); got != tc.want {
				t.Fatalf("external fence required = %v, want %v", got, tc.want)
			}
		})
	}
}

func (*coordinatedRetryModeBackend) Run(*TestRunner, *ManagedTest) {}

func (b *coordinatedRetryModeBackend) StopTestContext(context.Context, *TestRunner, *ManagedTest) error {
	b.mu.Lock()
	b.forceModes = append(b.forceModes, false)
	b.mu.Unlock()
	b.gracefulOnce.Do(func() { close(b.gracefulStarted) })
	<-b.releaseGraceful
	// Exercise the version check before non-retryable classification.
	return sql.ErrConnDone
}

func (b *coordinatedRetryModeBackend) AbortTestContext(context.Context, *TestRunner, *ManagedTest) error {
	b.mu.Lock()
	b.forceModes = append(b.forceModes, true)
	b.mu.Unlock()
	b.forceOnce.Do(func() { close(b.forceCalled) })
	return nil
}

func (b *coordinatedRetryModeBackend) modes() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.forceModes...)
}

func (*cancelAwareGracefulOnlyBackend) Run(*TestRunner, *ManagedTest) {}

func (b *cancelAwareGracefulOnlyBackend) StopTestContext(ctx context.Context, _ *TestRunner, test *ManagedTest) error {
	b.calls.Add(1)
	select {
	case <-test.executionCtx.Done():
		b.returnOne.Do(func() { close(b.returned) })
		return b.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestAbortCancelsBeforeGracefulOnlyBackendAndStaysFailClosed(t *testing.T) {
	wantErr := errors.New("legacy remote cleanup unavailable")
	backend := &cancelAwareGracefulOnlyBackend{returned: make(chan struct{}), err: wantErr}
	runner := NewTestRunner(1, NewHub(), nil)
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	test := &ManagedTest{
		ID: "force-before-legacy-fallback", Status: "RUNNING",
		executionCtx: executionCtx, cancel: cancelExecution, executionBackend: backend,
	}
	runner.tests[test.ID] = test

	started := time.Now()
	err := runner.AbortTest(test.ID)
	if !errors.Is(err, wantErr) {
		t.Fatalf("AbortTest error = %v, want %v", err, wantErr)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("AbortTest waited on graceful-only backend for %s", elapsed)
	}
	select {
	case <-backend.returned:
	case <-time.After(time.Second):
		t.Fatal("graceful-only backend did not observe the pre-dispatch execution cancellation")
	}
	if executionCtx.Err() == nil {
		t.Fatal("force abort did not cancel execution context")
	}
	if test.Status != "STOPPING" {
		t.Fatalf("status = %q, want fail-closed STOPPING after irreversible abort", test.Status)
	}
	runner.terminalRetryMu.Lock()
	intent, retryPending := runner.stopTeardownRetries[test.ID]
	runner.terminalRetryMu.Unlock()
	if !retryPending || !intent.force {
		t.Fatalf("retry intent = %+v pending=%v, want force teardown retained", intent, retryPending)
	}

	// Shut the retry worker down and wait for its registry entry to retire so the
	// test proves the fallback itself did not leave a blocked goroutine behind.
	runner.mu.Lock()
	runner.shuttingDown = true
	runner.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		runner.terminalRetryMu.Lock()
		_, retrying := runner.terminalRetrying["stop-teardown:"+test.ID]
		runner.terminalRetryMu.Unlock()
		if !retrying {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stop teardown retry worker did not retire during shutdown")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStopTeardownRetryObservesForceUpgradeBeforeRetiring(t *testing.T) {
	backend := &coordinatedRetryModeBackend{
		gracefulStarted: make(chan struct{}),
		releaseGraceful: make(chan struct{}),
		forceCalled:     make(chan struct{}),
	}
	runner := NewTestRunner(1, NewHub(), nil)
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	t.Cleanup(cancelExecution)
	test := &ManagedTest{
		ID: "retry-force-upgrade", Status: "STOPPING",
		executionCtx: executionCtx, cancel: cancelExecution, executionBackend: backend,
	}
	runner.tests[test.ID] = test

	runner.enqueueStopTeardownRetry(test.ID, false)
	select {
	case <-backend.gracefulStarted:
	case <-time.After(time.Second):
		t.Fatal("graceful retry did not start")
	}
	// Model the Abort request recording force intent while the deduplicated
	// graceful retry is still in flight. The stale call's non-retryable result
	// must not retire the worker before it applies graceful=false.
	runner.enqueueStopTeardownRetry(test.ID, true)
	close(backend.releaseGraceful)
	select {
	case <-backend.forceCalled:
	case <-time.After(2 * time.Second):
		t.Fatalf("force retry did not run after upgrade; modes=%v", backend.modes())
	}
	if got := backend.modes(); len(got) < 2 || got[0] || !got[1] {
		t.Fatalf("backend force modes = %v, want [false true]", got)
	}
	deadline := time.Now().Add(time.Second)
	for {
		runner.terminalRetryMu.Lock()
		_, intentPending := runner.stopTeardownRetries[test.ID]
		_, workerPending := runner.terminalRetrying["stop-teardown:"+test.ID]
		runner.terminalRetryMu.Unlock()
		if !intentPending && !workerPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successful force retry did not clear intent/worker")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStopAndAbortHandlersReportDurablePostRequestStatus(t *testing.T) {
	for _, tc := range []struct {
		name          string
		initialStatus string
		abort         bool
		wantStatus    string
		requestField  string
	}{
		{name: "active graceful stop remains stopping", initialStatus: "RUNNING", wantStatus: "stopping", requestField: "stop_requested"},
		{name: "active abort remains stopping", initialStatus: "RUNNING", abort: true, wantStatus: "stopping", requestField: "abort_requested"},
		{name: "queued stop is immediately aborted", initialStatus: "QUEUED", wantStatus: "aborted", requestField: "stop_requested"},
		{name: "terminal stop reports complete", initialStatus: "COMPLETE", wantStatus: "complete", requestField: "stop_requested"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			test := &ManagedTest{ID: "handler-status", Name: "handler status", Status: tc.initialStatus}
			if tc.initialStatus == "RUNNING" {
				executionCtx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				test.executionCtx = executionCtx
				test.cancel = cancel
				test.multiEngine = &engine.MultiGroupEngine{}
			}
			runner.tests[test.ID] = test
			server := &Server{runner: runner}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID, nil)
			request.SetPathValue("id", test.ID)
			if tc.abort {
				server.handleAbortTest(recorder, request)
			} else {
				server.handleStopTest(recorder, request)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status code = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response["status"] != tc.wantStatus || response[tc.requestField] != true {
				t.Fatalf("response = %#v, want status %q and %s=true", response, tc.wantStatus, tc.requestField)
			}
		})
	}
}
