package javarunner

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const forcedOutputDrainGrace = 250 * time.Millisecond

// ErrStrongProcessContainmentUnavailable means the platform/runtime cannot
// atomically own every descendant before user code starts.
var ErrStrongProcessContainmentUnavailable = errors.New("strong process containment unavailable")

// ErrProcessContainmentFailed means a target process was started, but cleanup
// could not prove that every owned descendant was terminated. When either
// process-tree start API returns this sentinel, the caller owns the direct-root
// Wait exactly once through ReapFailedStart. Callers that hold a process-
// capacity lease must quarantine that lease and fail the executor's future
// process admission closed.
var ErrProcessContainmentFailed = errors.New("process containment failed")

var processContainmentState = struct {
	failed atomic.Bool
	mu     sync.Mutex
	reaps  map[<-chan error]struct{}
}{}

type failedStartReapError struct {
	cause error
	done  <-chan error
}

func (e *failedStartReapError) Error() string { return e.cause.Error() }
func (e *failedStartReapError) Unwrap() error { return e.cause }

func (e *failedStartReapError) failedStartReapDone() <-chan error {
	return e.done
}

// FailedStartReapDone returns the direct-root reap owned by a failed process-
// tree start when an error has propagated through a higher-level operation such
// as JDK discovery. Owners can retain that channel through shutdown without
// issuing a second Wait. A nil result means the error carries no pending
// failed-start reap.
func FailedStartReapDone(err error) <-chan error {
	var carrier interface {
		failedStartReapDone() <-chan error
	}
	if errors.As(err, &carrier) {
		return carrier.failedStartReapDone()
	}
	return nil
}

// ConfigureProcessTree isolates cmd in an executor-local process tree and
// replaces CommandContext's direct-child cancellation with whole-tree
// termination. Java Actions may start native child processes; those children
// must not outlive a completed, canceled, or timed-out Vuser invocation.
//
// Call this after constructing cmd and before Start, Run, or Output.
func ConfigureProcessTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	configureProcessTree(cmd)
	// A non-nil Cancel is legal only for commands created by CommandContext.
	// Persistent JVMs intentionally use exec.Command and are canceled through
	// their explicit session abort path.
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			return KillProcessTree(cmd)
		}
	}
}

// StartProcessTree starts a command using the platform's managed process-tree
// primitive. On Windows this is stronger than cmd.Start: the suspended root is
// assigned to a kill-on-close Job Object before any user code can spawn an
// unowned descendant. Call ConfigureProcessTree before this function.
func StartProcessTree(cmd *exec.Cmd) error {
	if cmd == nil {
		return exec.ErrNotFound
	}
	if ProcessContainmentFailed() {
		return ErrProcessContainmentFailed
	}
	err := startProcessTree(cmd)
	if errors.Is(err, ErrProcessContainmentFailed) {
		markProcessContainmentFailed(nil)
	}
	return err
}

// StartStrongProcessTree starts cmd only when the platform can atomically
// establish descendant ownership before any target code executes. Persistent
// Java lifecycle sessions must use this entry point. Legacy one-shot execution
// may use StartProcessTree's documented best-effort behavior.
func StartStrongProcessTree(cmd *exec.Cmd) error {
	if cmd == nil {
		return exec.ErrNotFound
	}
	if ProcessContainmentFailed() {
		return ErrProcessContainmentFailed
	}
	err := startStrongProcessTree(cmd)
	if errors.Is(err, ErrProcessContainmentFailed) {
		markProcessContainmentFailed(nil)
	}
	return err
}

// WaitProcessTree waits for a command started through StartProcessTree, then
// terminates whatever its tree left behind, and returns the command's Wait
// error and KillProcessTree's containment error. On Linux the tree's members
// are recorded after the root exits but before Wait reaps it, while the exited
// root still pins its process group: a descendant it orphaned before the
// descendant poll saw it is still found and killed.
func WaitProcessTree(cmd *exec.Cmd) (waitErr, containmentErr error) {
	recordProcessTreeAtRootExit(cmd)
	waitErr = cmd.Wait()
	return waitErr, KillProcessTree(cmd)
}

// KillProcessTree terminates cmd and any native descendants that still belong
// to its isolated process tree. It is safe to call after Wait so successful
// JVM invocations cannot leave helper processes behind.
func KillProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := killProcessTree(cmd)
	if err != nil {
		markProcessContainmentFailed(nil)
	}
	return err
}

// ReapFailedStart performs the one direct-root Wait owned by a caller after
// StartProcessTree or StartStrongProcessTree returns
// ErrProcessContainmentFailed. It is deliberately asynchronous: the same
// containment failure may mean the platform could not terminate the root, so
// an action must not block forever while retaining its quarantine lease. The
// returned channel receives the Wait result and closes.
//
// Do not call this after any other result from either start API.
func ReapFailedStart(cmd *exec.Cmd) <-chan error {
	done := make(chan error, 1)
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		done <- nil
		close(done)
		trackProcessContainmentReap(done)
		return done
	}
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	trackProcessContainmentReap(done)
	return done
}

// ProcessContainmentFailed reports the process-wide terminal state. Once a
// process tree cannot be proven contained, every Java runner mode in this
// executor must fail admission closed; local and sidecar runtimes share the
// same OS process boundary.
func ProcessContainmentFailed() bool {
	return processContainmentState.failed.Load()
}

// WaitForProcessContainmentReaps waits until every currently tracked
// failed-start direct root has completed its sole Wait. New reaps cannot be
// registered after the owning runtime's operation-admission barrier closes.
func WaitForProcessContainmentReaps(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		processContainmentState.mu.Lock()
		reaps := make([]<-chan error, 0, len(processContainmentState.reaps))
		for reap := range processContainmentState.reaps {
			reaps = append(reaps, reap)
		}
		processContainmentState.mu.Unlock()
		if len(reaps) == 0 {
			return nil
		}
		for _, reap := range reaps {
			select {
			case <-reap:
				processContainmentState.mu.Lock()
				delete(processContainmentState.reaps, reap)
				processContainmentState.mu.Unlock()
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func markProcessContainmentFailed(reap <-chan error) {
	processContainmentState.failed.Store(true)
	trackProcessContainmentReap(reap)
}

func trackProcessContainmentReap(reap <-chan error) {
	if reap == nil {
		return
	}
	processContainmentState.mu.Lock()
	if processContainmentState.reaps == nil {
		processContainmentState.reaps = make(map[<-chan error]struct{})
	}
	processContainmentState.reaps[reap] = struct{}{}
	processContainmentState.mu.Unlock()
	go func() {
		<-reap
		processContainmentState.mu.Lock()
		delete(processContainmentState.reaps, reap)
		processContainmentState.mu.Unlock()
	}()
}

// SupportsStrongProcessContainment reports whether this runtime can atomically
// establish an ownership-bound process tree for Java Vusers.
// Persistent lifecycle sessions must fail closed when this is false: a plain
// POSIX process group cannot contain a native descendant that calls setsid.
func SupportsStrongProcessContainment() bool {
	if ProcessContainmentFailed() {
		return false
	}
	return supportsStrongProcessContainment()
}

// WaitForOutputDrain gives stdout/stderr readers a bounded opportunity to
// consume buffered output after process-tree termination. If an unowned
// descendant still retained an inherited writer, the readers are closed so a
// failed cleanup cannot permanently retain a Java Vuser capacity slot.
//
// The return value is false when at least one reader did not report completion,
// even after its read side was forcibly closed. Callers must treat output as
// truncated in that case, but may safely continue cleanup and release capacity.
func WaitForOutputDrain(timeout time.Duration, readers []io.Closer, done ...<-chan struct{}) bool {
	if len(done) == 0 {
		return true
	}
	if timeout <= 0 {
		timeout = forcedOutputDrainGrace
	}
	if waitForDrainSignals(time.Now().Add(timeout), done) {
		return true
	}
	for _, reader := range readers {
		if reader != nil {
			_ = reader.Close()
		}
	}
	// Once a reader had to be closed, its stream may have been truncated even
	// when the copy goroutine exits promptly afterward. Still give the
	// goroutines a bounded chance to unwind, but never report a clean drain.
	_ = waitForDrainSignals(time.Now().Add(forcedOutputDrainGrace), done)
	return false
}

func waitForDrainSignals(deadline time.Time, done []<-chan struct{}) bool {
	for _, signal := range done {
		if signal == nil {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		timer := time.NewTimer(remaining)
		select {
		case <-signal:
			timer.Stop()
		case <-timer.C:
			return false
		}
	}
	return true
}
