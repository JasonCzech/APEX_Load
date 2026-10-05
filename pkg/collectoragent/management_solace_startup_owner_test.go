package collectoragent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func solaceStartupTestFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "owned-lock-")
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func solaceStartupTestOwner(t *testing.T, closeFile func(*os.File) error) *solaceStartupOwner {
	t.Helper()
	o, err := reserveSolaceStartupOwner(filepath.Join(t.TempDir(), "owner"), closeFile)
	if err != nil {
		t.Fatal(err)
	}
	// Fault fixtures explicitly close their actual file and discard only their
	// own test registry after assertions. This is not runtime release authority.
	t.Cleanup(func() {
		o.mu.Lock()
		f := o.file
		o.mu.Unlock()
		if f != nil {
			_ = f.Close()
		}
		solaceStartupOwners.Lock()
		if solaceStartupOwners.byPath[o.path] == o {
			delete(solaceStartupOwners.byPath, o.path)
		}
		solaceStartupOwners.Unlock()
	})
	return o
}
func solaceStartupAssertRetained(t *testing.T, o *solaceStartupOwner, f *os.File) {
	t.Helper()
	o.mu.Lock()
	same, joined := o.file == f, o.joined
	o.mu.Unlock()
	solaceStartupOwners.Lock()
	retained := solaceStartupOwners.byPath[o.path] == o
	solaceStartupOwners.Unlock()
	if !same || joined || !retained {
		t.Fatal("lost exact unresolved descriptor/admission")
	}
	if replacement, err := reserveSolaceStartupOwner(o.path, func(f *os.File) error { return f.Close() }); err == nil || replacement != nil {
		t.Fatal("created replacement owner")
	}
}
func solaceStartupWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("physical continuation did not finish")
	}
}

func TestSolaceStartupOwnerCloseOutcomesRemainExact(t *testing.T) {
	for _, tc := range []struct {
		name             string
		fail, panicClose bool
	}{{"healthy", false, false}, {"first-close-failure", true, false}, {"close-panic", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := solaceStartupTestFile(t)
			var closes, releases atomic.Int32
			fault := errors.New("controlled first Close failure")
			o := solaceStartupTestOwner(t, func(actual *os.File) error {
				if actual != f {
					t.Error("closed foreign file")
				}
				closes.Add(1)
				if tc.panicClose {
					panic("controlled Close panic")
				}
				if tc.fail {
					return fault
				}
				return actual.Close()
			})
			o.ownFile(f)
			o.ownRelease(func() error { releases.Add(1); return nil })
			o.completeOperation(nil, false)
			err := o.Close()
			if tc.fail {
				if !errors.Is(err, errSolaceStartupOwnerUnproven) {
					t.Fatalf("fault became proof: %v", err)
				}
				solaceStartupAssertRetained(t, o, f)
				if _, err := f.Stat(); err != nil {
					t.Fatal("failed Close lost actual open descriptor", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("healthy Close did not close actual descriptor", err)
				}
				solaceStartupOwners.Lock()
				held := solaceStartupOwners.byPath[o.path] != nil
				solaceStartupOwners.Unlock()
				if held {
					t.Fatal("healthy joined owner retained admission")
				}
			}
			if again := o.Close(); (again == nil) != (err == nil) || closes.Load() != 1 || releases.Load() != 1 {
				t.Fatal("Close retried or forgave first outcome", again, closes.Load(), releases.Load())
			}
		})
	}
}

func TestSolaceStartupOwnerBlockedCloseReturnsBoundedAndLateProofJoins(t *testing.T) {
	f := solaceStartupTestFile(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	var closes atomic.Int32
	o := solaceStartupTestOwner(t, func(actual *os.File) error { closes.Add(1); close(entered); <-unblock; return actual.Close() })
	t.Cleanup(func() { release(); solaceStartupWait(t, o.closeDone) })
	o.ownFile(f)
	o.completeOperation(nil, false)
	start := time.Now()
	if !errors.Is(o.Close(), errSolaceStartupOwnerUnproven) || time.Since(start) > 3*time.Second {
		t.Fatal("blocked Close was not bounded")
	}
	solaceStartupWait(t, entered)
	solaceStartupAssertRetained(t, o, f)
	release()
	solaceStartupWait(t, o.closeDone)
	if err := o.Close(); err != nil || closes.Load() != 1 {
		t.Fatal("late physical nil proof did not join once", err)
	}
}

func TestSolaceStartupOwnerReleaseErrorAndPanicRemainVisible(t *testing.T) {
	for _, panicRelease := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panicRelease], func(t *testing.T) {
			f := solaceStartupTestFile(t)
			var closes, releases atomic.Int32
			fault := errors.New("controlled unlock failure")
			o := solaceStartupTestOwner(t, func(f *os.File) error { closes.Add(1); return f.Close() })
			o.ownFile(f)
			o.ownRelease(func() error {
				releases.Add(1)
				if panicRelease {
					panic("unlock panic")
				}
				return fault
			})
			o.completeOperation(nil, false)
			err := o.Close()
			if !errors.Is(err, errSolaceStartupOwnerUnproven) || (!panicRelease && !errors.Is(err, fault)) {
				t.Fatal("lost unlock outcome", err)
			}
			solaceStartupAssertRetained(t, o, f)
			if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("exact Close not attempted after unlock error", err)
			}
			if o.Close() == nil || releases.Load() != 1 || closes.Load() != 1 {
				t.Fatal("release/Close retried or forgiven")
			}
		})
	}
}

func TestSolaceStartupConstructorUnknownCannotBecomeEmptyOwnerProof(t *testing.T) {
	for _, report := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-report", true: "after-report"}[report], func(t *testing.T) {
			f := solaceStartupTestFile(t)
			defer f.Close()
			var closes atomic.Int32
			o := solaceStartupTestOwner(t, func(f *os.File) error { closes.Add(1); return f.Close() })
			cleanup, err := runSolaceStartupOwned(context.Background(), o, func(context.Context, *solaceStartupOwner) error {
				if report {
					o.ownFile(f)
				}
				panic("allocation outcome unknown")
			})
			if cleanup != nil || !errors.Is(err, errSolaceStartupOwnerUnproven) {
				t.Fatal("constructor panic accepted", err)
			}
			o.mu.Lock()
			unknown, joined := o.operationUnknown, o.joined
			o.mu.Unlock()
			solaceStartupOwners.Lock()
			held := solaceStartupOwners.byPath[o.path] == o
			solaceStartupOwners.Unlock()
			if !unknown || joined || !held || closes.Load() != 0 {
				t.Fatal("empty known set/Close became unknown guard proof")
			}
			if _, err := f.Stat(); err != nil {
				t.Fatal("unknown guard was released", err)
			}
		})
	}
}

type solaceStartupDetachedDeadline struct {
	context.Context
	deadline time.Time
}

func (c solaceStartupDetachedDeadline) Deadline() (time.Time, bool) { return c.deadline, true }
func (c solaceStartupDetachedDeadline) Done() <-chan struct{}       { return nil }
func (c solaceStartupDetachedDeadline) Err() error                  { return nil }

func TestSolaceStartupExpiredActualDeadlineDoesNotOpen(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	target := solaceConfigTestTarget(t)
	a := &Agent{cfg: Config{SpoolDirectory: directory, Packs: []string{"solace-semp"}, AdapterTargets: []AdapterTarget{{Pack: "solace-semp", Management: &target}}}, spoolBudget: newCollectorSpoolBudget()}
	var opens atomic.Int32
	ctx := solaceStartupDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	cleanup, err := a.lockSolaceManagementQuarantineWithHooks(ctx, solaceStartupHooks{openFile: func(string, int, os.FileMode) (*os.File, error) { opens.Add(1); return nil, nil }, lockFile: func(context.Context, *os.File) (func() error, error) { t.Error("entered lock"); return nil, nil }, closeFile: func(f *os.File) error { return f.Close() }})
	if cleanup != nil || !errors.Is(err, context.DeadlineExceeded) || opens.Load() != 0 {
		t.Fatal("expired detached authority entered physical Open", err, opens.Load())
	}
}

func TestSolaceStartupEnteredOpenRetainsAdmissionUntilLateClose(t *testing.T) {
	f := solaceStartupTestFile(t)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	var closes atomic.Int32
	o := solaceStartupTestOwner(t, func(f *os.File) error { closes.Add(1); return f.Close() })
	t.Cleanup(func() { release(); solaceStartupWait(t, o.closeDone) })
	ctx := solaceStartupDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(40 * time.Millisecond)}
	start := time.Now()
	cleanup, err := runSolaceStartupOwned(ctx, o, func(ctx context.Context, o *solaceStartupOwner) error {
		close(entered)
		<-unblock
		o.ownFile(f)
		return nil
	})
	if cleanup != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("entered Open blocked caller or returned readiness", err)
	}
	solaceStartupWait(t, entered)
	solaceStartupOwners.Lock()
	held := solaceStartupOwners.byPath[o.path] == o
	solaceStartupOwners.Unlock()
	if !held || closes.Load() != 0 {
		t.Fatal("released beneath entered unreported Open")
	}
	release()
	solaceStartupWait(t, o.closeDone)
	if o.Close() != nil || closes.Load() != 1 {
		t.Fatal("late actual descriptor did not join once")
	}
}

func TestSolaceStartupHooksPreservePartialOpenAndLateAcquiredRelease(t *testing.T) {
	for _, phase := range []string{"partial-open", "late-acquisition"} {
		t.Run(phase, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				t.Fatal(err)
			}
			target := solaceConfigTestTarget(t)
			a := &Agent{cfg: Config{SpoolDirectory: directory, Packs: []string{"solace-semp"}, AdapterTargets: []AdapterTarget{{Pack: "solace-semp", Management: &target}}}, spoolBudget: newCollectorSpoolBudget()}
			var actual *os.File
			var releases, closes atomic.Int32
			fault := errors.New("controlled setup failure")
			_, err := a.lockSolaceManagementQuarantineWithHooks(context.Background(), solaceStartupHooks{
				openFile: func(path string, flags int, mode os.FileMode) (*os.File, error) {
					f, err := os.OpenFile(path, flags, mode)
					actual = f
					if err == nil && phase == "partial-open" {
						err = fault
					}
					return f, err
				},
				lockFile: func(context.Context, *os.File) (func() error, error) {
					return func() error { releases.Add(1); return nil }, context.Canceled
				},
				closeFile: func(f *os.File) error { closes.Add(1); return f.Close() },
			})
			if err == nil || actual == nil || closes.Load() != 1 {
				t.Fatal("constructor lost partial physical file", err)
			}
			if _, err := actual.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("partial descriptor not physically closed", err)
			}
			want := int32(0)
			if phase == "late-acquisition" {
				want = 1
			}
			if releases.Load() != want {
				t.Fatal("lost or fabricated acquired release", releases.Load())
			}
		})
	}
}

func TestSolaceStartupPartialOpenFailureRetainsExactCloseFault(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	target := solaceConfigTestTarget(t)
	a := &Agent{cfg: Config{SpoolDirectory: directory, Packs: []string{"solace-semp"}, AdapterTargets: []AdapterTarget{{Pack: "solace-semp", Management: &target}}}, spoolBudget: newCollectorSpoolBudget()}
	var actual *os.File
	fault := errors.New("controlled exact descriptor Close failure")
	var closes atomic.Int32
	cleanup, err := a.lockSolaceManagementQuarantineWithHooks(context.Background(), solaceStartupHooks{
		openFile: func(path string, flags int, mode os.FileMode) (*os.File, error) {
			f, err := os.OpenFile(path, flags, mode)
			actual = f
			if err != nil {
				return f, err
			}
			return f, errors.New("controlled partial Open error")
		},
		lockFile: func(context.Context, *os.File) (func() error, error) {
			t.Error("lock entered after partial Open error")
			return nil, nil
		},
		closeFile: func(f *os.File) error {
			if f != actual {
				t.Error("foreign descriptor Close")
			}
			closes.Add(1)
			return fault
		},
	})
	path := filepath.Join(directory, solaceManagementQuarantineFile+".lock")
	solaceStartupOwners.Lock()
	o := solaceStartupOwners.byPath[path]
	solaceStartupOwners.Unlock()
	if actual != nil {
		t.Cleanup(func() {
			_ = actual.Close()
			solaceStartupOwners.Lock()
			if solaceStartupOwners.byPath[path] == o {
				delete(solaceStartupOwners.byPath, path)
			}
			solaceStartupOwners.Unlock()
		})
	}
	if cleanup != nil || !errors.Is(err, fault) || !errors.Is(err, errSolaceStartupOwnerUnproven) || actual == nil || o == nil || closes.Load() != 1 {
		t.Fatal("partial constructor lost failed Close owner", err)
	}
	solaceStartupAssertRetained(t, o, actual)
	if o.Close() == nil || closes.Load() != 1 {
		t.Fatal("partial constructor retried or forgave actual Close failure")
	}
	if _, err := actual.Stat(); err != nil {
		t.Fatal("lost exact retained open descriptor", err)
	}
}

func TestSolaceStartupOwnerAdmissionHasExactCapacityAndNoEviction(t *testing.T) {
	var owners []*solaceStartupOwner
	for i := 0; i < solaceStartupOwnersMax; i++ {
		o := solaceStartupTestOwner(t, func(f *os.File) error { return f.Close() })
		owners = append(owners, o)
	}
	if o, err := reserveSolaceStartupOwner(filepath.Join(t.TempDir(), "overflow"), func(f *os.File) error { return f.Close() }); o != nil || !errors.Is(err, errSolaceStartupOwnerUnproven) {
		t.Fatal("exceeded startup owner cap")
	}
	for _, o := range owners {
		o.completeOperation(nil, false)
		if err := o.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentRunJoinsManagementBeforeSolaceCleanupAndReturnsCleanupError(t *testing.T) {
	heartbeat := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { heartbeat <- struct{}{}; w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	a, err := New(Config{ControllerURL: srv.URL, CollectorID: "c", Token: "t", Interval: time.Hour, Packs: []string{"go-runtime"}, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	a.management = newManagementScheduler(a, "fixture")
	taskDone, cancelled := make(chan struct{}), make(chan struct{})
	a.management.active["held"] = &managementTask{cancel: func() { close(cancelled) }, done: taskDone}
	var taskOnce sync.Once
	releaseTask := func() { taskOnce.Do(func() { close(taskDone) }) }
	defer releaseTask()
	f := solaceStartupTestFile(t)
	closeEntered := make(chan struct{})
	fault := errors.New("controlled actual lock Close failure")
	o := solaceStartupTestOwner(t, func(actual *os.File) error {
		if actual != f {
			t.Error("foreign cleanup")
		}
		close(closeEntered)
		return fault
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	runJoined := make(chan struct{})
	t.Cleanup(func() { cancel(); releaseTask(); solaceStartupWait(t, runJoined) })
	go func() {
		defer close(runJoined)
		runDone <- a.runWithSolaceStartup(ctx, func(ctx context.Context) (func() error, error) {
			return runSolaceStartupOwned(ctx, o, func(context.Context, *solaceStartupOwner) error { o.ownFile(f); return nil })
		})
	}()
	solaceStartupWait(t, heartbeat)
	cancel()
	solaceStartupWait(t, cancelled)
	select {
	case <-closeEntered:
		t.Fatal("released Solace lock beneath active management task")
	default:
	}
	select {
	case <-runDone:
		t.Fatal("Run returned before actual management join")
	default:
	}
	releaseTask()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, fault) || !errors.Is(err, errSolaceStartupOwnerUnproven) {
			t.Fatal("Run discarded exact cleanup failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after task joined")
	}
	solaceStartupAssertRetained(t, o, f)
}
