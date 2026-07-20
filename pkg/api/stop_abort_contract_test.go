package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
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
