package api

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// panickingExecutionBackend raises a panic from the run driver's dispatch, the
// same way a nil map/slice bug deep inside engine construction, archive setup,
// or metrics broadcasting would.
type panickingExecutionBackend struct {
	mu       sync.Mutex
	runCalls int
	// before runs immediately before the panic. Tests use it to reproduce the
	// state the driver would really be in when it dies: holding a lock, having
	// already lost the lifecycle race, or having been displaced by a reload.
	before     func(tr *TestRunner, test *ManagedTest)
	panicValue any
}

func (b *panickingExecutionBackend) Run(tr *TestRunner, test *ManagedTest) {
	b.mu.Lock()
	b.runCalls++
	b.mu.Unlock()
	if b.before != nil {
		b.before(tr, test)
	}
	panic(b.panicValue)
}

func (b *panickingExecutionBackend) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runCalls
}

func newPanicBackstopRunner(t *testing.T, backend testExecutionBackend) (*TestRunner, *ManagedTest, context.Context, context.CancelFunc) {
	t.Helper()
	runner := NewTestRunner(1, NewHub(), nil)
	runner.setExecutionBackend(backend)
	executionCtx, cancel := context.WithCancel(context.Background())
	test := &ManagedTest{
		ID:           "panicking-run",
		Name:         "panicking-run",
		Status:       "INITIALIZING",
		Duration:     time.Minute,
		executionCtx: executionCtx,
		cancel:       cancel,
	}
	runner.tests[test.ID] = test
	return runner, test, executionCtx, cancel
}

// driveRunTestToCompletion runs the driver on its own goroutine and fails the
// test if it neither returns nor re-panics.
func driveRunTestToCompletion(t *testing.T, runner *TestRunner, test *ManagedTest) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.runTest(test)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("runTest did not return after the backend panicked")
	}
}

// TestRunTestPanicBackstopFailsOnlyTheRun proves the run-driver goroutine no
// longer propagates a backend panic out of runTest. Without the backstop the
// panic escapes the goroutine and terminates the whole controller process,
// killing every other run on the replica; with it, exactly one run goes FAILED.
func TestRunTestPanicBackstopFailsOnlyTheRun(t *testing.T) {
	backend := &panickingExecutionBackend{panicValue: "runtime error: index out of range [3] with length 0"}
	runner, test, executionCtx, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()

	driveRunTestToCompletion(t, runner, test)

	if got := backend.calls(); got != 1 {
		t.Fatalf("backend Run calls = %d, want 1", got)
	}
	runner.mu.RLock()
	status := test.Status
	failure := test.Error
	ended := test.EndedAt
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status after a driver panic = %s, want FAILED", status)
	}
	if !strings.Contains(failure, "test execution panicked") ||
		!strings.Contains(failure, "index out of range") {
		t.Fatalf("failure message = %q, want it to name the panic", failure)
	}
	if ended == nil {
		t.Fatal("panicked run was left without an end time")
	}
	// The half-finished driver must not leave engine/VUser goroutines running.
	select {
	case <-executionCtx.Done():
	default:
		t.Fatal("panicked run left its execution context live")
	}
}

// TestRunTestPanicBackstopDoesNotResurrectATerminalRun proves the backstop
// respects a terminal decision that already won: a stop that aborted the run
// before the driver panicked must stay ABORTED, not be rewritten to FAILED.
//
// The abort is committed from inside the backend, so the whole run goes through
// runTest rather than calling the backstop directly, and the assertions pair the
// preserved terminal state with the one effect the backstop still owes an
// already-terminal run: cancelling the execution context the dead driver left
// behind.
func TestRunTestPanicBackstopDoesNotResurrectATerminalRun(t *testing.T) {
	var abortedAt time.Time
	backend := &panickingExecutionBackend{
		panicValue: "late teardown panic",
		before: func(tr *TestRunner, test *ManagedTest) {
			// A stop wins the lifecycle race while the driver is still in flight.
			tr.mu.Lock()
			abortedAt = time.Now()
			test.Status = "ABORTED"
			test.EndedAt = &abortedAt
			tr.mu.Unlock()
		},
	}
	runner, test, executionCtx, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()

	driveRunTestToCompletion(t, runner, test)

	if got := backend.calls(); got != 1 {
		t.Fatalf("backend Run calls = %d, want 1", got)
	}
	runner.mu.RLock()
	status := test.Status
	failure := test.Error
	ended := test.EndedAt
	runner.mu.RUnlock()
	if status != "ABORTED" {
		t.Fatalf("status = %s, want the earlier terminal decision ABORTED to stand", status)
	}
	if failure != "" {
		t.Fatalf("aborted run picked up a panic failure message %q", failure)
	}
	if ended == nil || !ended.Equal(abortedAt) {
		t.Fatalf("end time = %v, want the abort's own end time %v", ended, abortedAt)
	}
	select {
	case <-executionCtx.Done():
	default:
		t.Fatal("backstop left an already-aborted run's execution context live")
	}
}

// TestRunTestPanicBackstopIgnoresADisplacedHandle proves the backstop's
// stale-handle re-check holds: once a reload, delete, or replacement swapped a
// different ManagedTest into tr.tests, the panicking handle owns no durable row
// and must publish nothing at all - not a status, and not a cancellation that
// would tear down the successor's execution context.
func TestRunTestPanicBackstopIgnoresADisplacedHandle(t *testing.T) {
	successor := &ManagedTest{ID: "panicking-run", Name: "successor", Status: "INITIALIZING"}
	backend := &panickingExecutionBackend{
		panicValue: "panic after a reload replaced the handle",
		before: func(tr *TestRunner, test *ManagedTest) {
			tr.mu.Lock()
			tr.tests[test.ID] = successor
			tr.mu.Unlock()
		},
	}
	runner, test, executionCtx, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()

	driveRunTestToCompletion(t, runner, test)

	runner.mu.RLock()
	staleStatus := test.Status
	staleError := test.Error
	successorStatus := successor.Status
	runner.mu.RUnlock()
	if staleStatus != "INITIALIZING" || staleError != "" {
		t.Fatalf("displaced handle published status=%s error=%q, want it left untouched", staleStatus, staleError)
	}
	if successorStatus != "INITIALIZING" {
		t.Fatalf("successor status = %s, want INITIALIZING", successorStatus)
	}
	select {
	case <-executionCtx.Done():
		t.Fatal("backstop cancelled the execution context of a run it no longer owns")
	default:
	}
}

// TestRunTestPanicBackstopRePanicsWhenRunnerLockIsOrphaned proves the backstop
// fails fast instead of converting a crash into a silent wedge. A panic raised
// while tr.mu is held leaves the mutex permanently locked, so every terminal
// transition the backstop wants to make would block forever.
func TestRunTestPanicBackstopRePanicsWhenRunnerLockIsOrphaned(t *testing.T) {
	backend := &panickingExecutionBackend{
		before:     func(tr *TestRunner, _ *ManagedTest) { tr.mu.RLock() },
		panicValue: "panic under the runner lock",
	}
	assertRunTestRePanics(t, backend, "panic under the runner lock")
}

// TestRunTestPanicBackstopRePanicsWhenRunnerWriteLockIsOrphaned covers the
// orphan flavour a read lock cannot model. A recursive RLock is still
// acquirable, so only a write-lock orphan proves nothing on the unwind path -
// span annotation above all - blocks before the backstop is ever entered.
func TestRunTestPanicBackstopRePanicsWhenRunnerWriteLockIsOrphaned(t *testing.T) {
	backend := &panickingExecutionBackend{
		before:     func(tr *TestRunner, _ *ManagedTest) { tr.mu.Lock() },
		panicValue: "panic under the runner write lock",
	}
	assertRunTestRePanics(t, backend, "panic under the runner write lock")
}

// TestRunTestPanicBackstopRePanicsWhenArchiveFenceIsOrphaned covers the other
// lock the terminal path needs. tr.mu being acquirable says nothing about the
// per-test archive-commit fence that completeTestRun takes, and a panic out of
// a fence holder that unlocks manually orphans that gate. Waiting on
// terminalization forever would strand the run, its capacity reservation, and
// every later taker of the same fence.
func TestRunTestPanicBackstopRePanicsWhenArchiveFenceIsOrphaned(t *testing.T) {
	backend := &panickingExecutionBackend{
		before: func(tr *TestRunner, test *ManagedTest) {
			tr.lockArchiveCommitFence(test.ID)
		},
		panicValue: "panic under the archive commit fence",
	}
	assertRunTestRePanics(t, backend, "panic under the archive commit fence")
}

func assertRunTestRePanics(t *testing.T, backend *panickingExecutionBackend, want string) {
	t.Helper()
	previous := runTestPanicRecoveryLockTimeout
	runTestPanicRecoveryLockTimeout = 50 * time.Millisecond
	defer func() { runTestPanicRecoveryLockTimeout = previous }()

	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()

	repanicked := make(chan any, 1)
	go func() {
		defer func() { repanicked <- recover() }()
		runner.runTest(test)
	}()
	select {
	case recovered := <-repanicked:
		if recovered == nil {
			t.Fatal("backstop swallowed a panic that orphaned a lock the terminal path needs")
		}
		if got, ok := recovered.(string); !ok || got != want {
			t.Fatalf("re-panicked value = %v, want the original panic value %q", recovered, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runTest neither returned nor re-panicked with an orphaned lock")
	}
}

// TestRunTestPanicBackstopMarksTheLifecycleSpanFailed proves a crashed run is
// not exported as a healthy one. The span defer unwinds before the backstop has
// terminalized anything, so reading the live status there would publish
// outcome=initializing/running with codes.Ok - a green span for a run the API
// reports as FAILED.
func TestRunTestPanicBackstopMarksTheLifecycleSpanFailed(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)),
	)
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	backend := &panickingExecutionBackend{panicValue: "runtime error: invalid memory address or nil pointer dereference"}
	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()

	driveRunTestToCompletion(t, runner, test)

	spans := exporter.GetSpans()
	var execute *tracetest.SpanStub
	for index := range spans {
		if spans[index].Name == "apexload.test.execute" {
			execute = &spans[index]
			break
		}
	}
	if execute == nil {
		t.Fatalf("span names = %#v, want an apexload.test.execute span", spanNames(spans))
	}
	if execute.Status.Code != codes.Error {
		t.Fatalf("execute span status = %#v, want codes.Error for a panicked run", execute.Status)
	}
	attrs := spanStringAttributes(*execute)
	if got := attrs["apexload.test.outcome"]; got != "panicked" {
		t.Fatalf("apexload.test.outcome = %q, want \"panicked\"", got)
	}
}

// panicOnStatusPersistence models a storage driver that panics on the terminal
// write, which is the shape of secondary panic the backstop is most exposed to:
// terminalization walks the same shared state the primary panic may have
// corrupted.
type panicOnStatusPersistence struct {
	calls atomic.Int32
}

func (s *panicOnStatusPersistence) SaveTest(*storage.TestRecord) error { return nil }

func (s *panicOnStatusPersistence) GetTest(string) (*storage.TestRecord, error) { return nil, nil }

func (s *panicOnStatusPersistence) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *panicOnStatusPersistence) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *panicOnStatusPersistence) UpdateTestStatus(string, string, *time.Time, string) error {
	s.calls.Add(1)
	panic("storage driver panicked while terminalizing the panicked run")
}

func (s *panicOnStatusPersistence) DeleteTest(string) error { return nil }

func (s *panicOnStatusPersistence) MoveTest(string, *string) error { return nil }

// TestRunTestPanicBackstopRePanicsOnAContainedSecondaryPanic proves the backstop
// neither crashes raw from its helper goroutine nor returns as if terminalization
// had succeeded.
//
// Containment matters because a goroutine has no caller to recover it: without
// the helper's own recover the secondary panic takes the whole test binary down.
// But containment alone is not a fix. This secondary panic unwinds out of
// failTestBeforeRun's tr.mu critical section (persistStoredTestStatus is called
// while the lock is held), and no recover can unlock that mutex. Returning
// normally would leave a live process whose runner lock is permanently
// write-locked - every status transition and the /api/v1/system/health probe
// would block forever. So the driver must re-panic, with the original panic
// value, and let the supervisor restart the process.
func TestRunTestPanicBackstopRePanicsOnAContainedSecondaryPanic(t *testing.T) {
	store := &panicOnStatusPersistence{}
	backend := &panickingExecutionBackend{panicValue: "primary driver panic"}
	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()
	runner.testStore = store

	repanicked := make(chan any, 1)
	go func() {
		defer func() { repanicked <- recover() }()
		runner.runTest(test)
	}()
	select {
	case recovered := <-repanicked:
		if recovered == nil {
			t.Fatal("backstop returned normally after terminalization panicked, leaving tr.mu orphaned in a live process")
		}
		if got, ok := recovered.(string); !ok || got != "primary driver panic" {
			t.Fatalf("re-panicked value = %v, want the original panic value %q", recovered, "primary driver panic")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runTest neither returned nor re-panicked after terminalization panicked")
	}
	if got := store.calls.Load(); got < 1 {
		t.Fatalf("terminal status writes = %d, want the backstop to have reached the storage driver", got)
	}
	// Deliberately no tr.mu assertions: the secondary panic orphaned it, which is
	// exactly why the driver re-panics instead of returning.
}

// slowStatusPersistence models a healthy but stalled store - a managed-database
// failover, say - whose status write eventually completes.
type slowStatusPersistence struct {
	delay time.Duration
	calls atomic.Int32
	// entered, when non-nil, is signalled as each write reaches the driver and
	// before it stalls, so a test can panic a run driver at the one instant an
	// unrelated write is genuinely in flight.
	entered chan struct{}
}

func (s *slowStatusPersistence) SaveTest(*storage.TestRecord) error { return nil }

func (s *slowStatusPersistence) GetTest(string) (*storage.TestRecord, error) { return nil, nil }

func (s *slowStatusPersistence) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *slowStatusPersistence) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *slowStatusPersistence) UpdateTestStatus(string, string, *time.Time, string) error {
	s.calls.Add(1)
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
	time.Sleep(s.delay)
	return nil
}

func (s *slowStatusPersistence) DeleteTest(string) error { return nil }

func (s *slowStatusPersistence) MoveTest(string, *string) error { return nil }

// TestRunTestPanicBackstopWaitsOutASlowDurableWrite proves the bounded terminal
// wait does not mistake slow durable I/O for an orphaned lock. The terminal
// transition takes no context, so a stalled store can hold one status write open
// for far longer than the bound; re-panicking there would restart the replica
// into the same unreachable store and crash-loop, and would kill every other run
// on it. The wait must extend while a durable write is genuinely in flight and
// then terminalize the run normally.
func TestRunTestPanicBackstopWaitsOutASlowDurableWrite(t *testing.T) {
	previousTimeout := runTestPanicRecoveryLockTimeout
	runTestPanicRecoveryLockTimeout = 50 * time.Millisecond
	// Generous headroom on the extension cap: this test is about tolerating a
	// write that is still progressing, not about where the cap sits (that has its
	// own test), and a loaded CI box must not turn into a spurious re-panic.
	previousExtensions := runTestPanicRecoveryDurableWriteExtensions
	runTestPanicRecoveryDurableWriteExtensions = 64
	defer func() {
		runTestPanicRecoveryLockTimeout = previousTimeout
		runTestPanicRecoveryDurableWriteExtensions = previousExtensions
	}()

	// Do not align completion exactly with a 50ms recovery deadline. At that
	// boundary the write gauge can reach zero one scheduler tick before the
	// terminalized channel closes, making this fixture test select ordering
	// instead of the durable-write behavior it is intended to cover.
	store := &slowStatusPersistence{delay: 225 * time.Millisecond}
	backend := &panickingExecutionBackend{panicValue: "driver panic during a database failover"}
	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()
	runner.testStore = store

	repanicked := make(chan any, 1)
	go func() {
		defer func() { repanicked <- recover() }()
		runner.runTest(test)
	}()
	select {
	case recovered := <-repanicked:
		if recovered != nil {
			t.Fatalf("backstop re-panicked with %v over a slow-but-healthy durable write; the store was progressing, not wedged", recovered)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runTest neither returned nor re-panicked while the store was slow")
	}
	if got := store.calls.Load(); got < 1 {
		t.Fatalf("terminal status writes = %d, want the backstop to have reached the storage driver", got)
	}
	runner.mu.RLock()
	status := test.Status
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status after waiting out the slow store = %s, want FAILED", status)
	}
}

// TestRunTestPanicBackstopWaitsOutASlowDurableWriteHoldingTheRunnerLock covers
// the stage the terminalization test cannot reach: the wait for tr.mu to become
// acquirable at all.
//
// Every terminal transition in this package holds tr.mu across its durable
// status write (failTestBeforeRun and completeTestRunUnderArchiveFence... both
// call persistStoredTestStatus under the write lock), so during a managed-store
// failover an unrelated run finishing normally pins tr.mu for the whole stall.
// If a second run's driver panics in that window, the backstop's helper blocks
// on the same mutex. A flat deadline there cannot tell that from a mutex a
// panic orphaned, so it re-panics and kills the replica - every other run with
// it - and the restart lands on the same unreachable store and crash-loops.
// That is precisely the outcome the durable-write extension exists to prevent,
// so the acquirability wait must honour the in-flight gauge too.
func TestRunTestPanicBackstopWaitsOutASlowDurableWriteHoldingTheRunnerLock(t *testing.T) {
	previousTimeout := runTestPanicRecoveryLockTimeout
	runTestPanicRecoveryLockTimeout = 50 * time.Millisecond
	// Generous headroom on the cap, as in the terminalization test: this is about
	// tolerating a write that is still progressing, not about where the cap sits.
	previousExtensions := runTestPanicRecoveryDurableWriteExtensions
	runTestPanicRecoveryDurableWriteExtensions = 64
	defer func() {
		runTestPanicRecoveryLockTimeout = previousTimeout
		runTestPanicRecoveryDurableWriteExtensions = previousExtensions
	}()

	store := &slowStatusPersistence{delay: 300 * time.Millisecond, entered: make(chan struct{}, 1)}
	unrelatedDone := make(chan struct{})
	backend := &panickingExecutionBackend{
		panicValue: "driver panic during a database failover",
		before: func(tr *TestRunner, _ *ManagedTest) {
			// An unrelated, perfectly healthy run reaches its terminal transition
			// first and stalls inside the storage driver holding tr.mu.
			unrelated := &ManagedTest{ID: "unrelated-run", Name: "unrelated-run", Status: "INITIALIZING"}
			tr.mu.Lock()
			tr.tests[unrelated.ID] = unrelated
			tr.mu.Unlock()
			go func() {
				defer close(unrelatedDone)
				_ = tr.failTestBeforeRun(unrelated, "unrelated pre-run failure")
			}()
			select {
			case <-store.entered:
			case <-time.After(10 * time.Second):
				t.Error("the unrelated status write never reached the storage driver")
			}
		},
	}
	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()
	runner.testStore = store

	repanicked := make(chan any, 1)
	go func() {
		defer func() { repanicked <- recover() }()
		runner.runTest(test)
	}()
	select {
	case recovered := <-repanicked:
		if recovered != nil {
			t.Fatalf("backstop re-panicked with %v while tr.mu was merely held by an unrelated run's slow-but-healthy durable write; the store was progressing, not wedged", recovered)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runTest neither returned nor re-panicked while an unrelated slow write held the runner lock")
	}
	<-unrelatedDone
	runner.mu.RLock()
	status := test.Status
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status after waiting out the lock-holding slow store = %s, want FAILED", status)
	}
}

// TestRunTestPanicBackstopStopsExtendingForAStoreThatNeverReturns proves the
// tolerance for slow durable I/O is a longer bound, not an unbounded wait. The
// in-flight gauge is process-wide, so without a cap a store that never returns -
// or merely a busy controller with some write in flight at every expiry - could
// postpone the re-panic forever and strand the run behind an orphaned gate,
// which is the wedge the bound exists to prevent.
func TestRunTestPanicBackstopStopsExtendingForAStoreThatNeverReturns(t *testing.T) {
	previousTimeout := runTestPanicRecoveryLockTimeout
	runTestPanicRecoveryLockTimeout = 10 * time.Millisecond
	previousExtensions := runTestPanicRecoveryDurableWriteExtensions
	runTestPanicRecoveryDurableWriteExtensions = 3
	defer func() {
		runTestPanicRecoveryLockTimeout = previousTimeout
		runTestPanicRecoveryDurableWriteExtensions = previousExtensions
	}()

	// Longer than every extension put together, so the write is still in flight
	// at each expiry and only the cap can end the wait.
	store := &slowStatusPersistence{delay: 5 * time.Second}
	backend := &panickingExecutionBackend{panicValue: "driver panic during a total store outage"}
	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()
	runner.testStore = store

	repanicked := make(chan any, 1)
	go func() {
		defer func() { repanicked <- recover() }()
		runner.runTest(test)
	}()
	select {
	case recovered := <-repanicked:
		if recovered == nil {
			t.Fatal("backstop returned normally while the store was still hung; the run was never terminalized")
		}
		if got, ok := recovered.(string); !ok || got != "driver panic during a total store outage" {
			t.Fatalf("re-panicked value = %v, want the original panic value", recovered)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backstop extended the wait past its cap for a store that never returns")
	}
}
