package duckdb

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type workerProcessProfile struct {
	requireHard bool
	memoryBytes int64
	linuxCgroup string
}
type workerLaunch struct {
	executable  string
	arguments   []string
	environment []string
}
type workerRunOptions struct {
	profile workerProcessProfile
	probe   func(*workerAttempt, *os.Process) error
	// testLaunch is package-private and never populated by public DTO/config.
	testLaunch            *workerLaunch
	testReadFrame         func(io.Reader) (workerFrame, error)
	testCallerProofWindow time.Duration
	testAfterWait         func()
	testAfterWaitConsumed func()
	testBeforeSignal      func()
}
type workerRunResult struct {
	result  workerResult
	attempt *workerAttempt
}

func (r *workerRunResult) release() error { return workerFinishAttempt(r.attempt) }

func workerProductionLaunch(a *workerAttempt) (workerLaunch, error) {
	path, err := workerRunningExecutableSourcePath()
	if err != nil {
		return workerLaunch{}, errWorkerProtocol
	}
	expected, err := workerExpectedExecutableDigest()
	if err != nil || expected != a.bootstrap.Identity.BuildID {
		return workerLaunch{}, errWorkerProtocol
	}
	path, err = workerSnapshotExecutable(a, path, expected)
	if err != nil {
		return workerLaunch{}, err
	}
	environment := workerChildEnvironment(a.path)
	if len(environment) == 0 {
		return workerLaunch{}, errWorkerProtocol
	}
	return workerLaunch{executable: path, arguments: []string{workerChildArgument}, environment: environment}, nil
}
func workerChildEnvironment(root string) []string {
	// No parent environment iteration: credentials, proxy/config/dotenv,
	// telemetry and loader injection values cannot be inherited accidentally.
	return workerPlatformEnvironment(root)
}

type workerPipeEvent struct {
	frame workerFrame
	err   error
}
type workerWriteEvent struct{ err error }

// workerLogUnresolved records why an attempt's physical ownership stayed
// unproven. Such an attempt stays charged, so its fixed public error alone
// cannot tell an operator what is still held.
func workerLogUnresolved(a *workerAttempt, cause string, attrs ...any) {
	slog.Warn("native worker ownership unresolved", append([]any{"cause", cause, "attempt", a.bootstrap.Identity.AttemptID}, attrs...)...)
}

// The caller retains the returned attempt through durable copy and publication
// reconciliation, then closes output readers and releases it. Error callers
// may release only if physical ownership was proven; unresolved attempts stay
// charged even when the API has returned a fixed error.
func runWorkerParent(a *workerAttempt, request workerRequest, options workerRunOptions) (*workerRunResult, error) {
	if a == nil {
		return nil, errWorkerProtocol
	}
	frozen, err := (workerFrame{Identity: a.bootstrap.Identity, Kind: "request", Request: &request}).clone()
	if err != nil {
		return nil, errWorkerProtocol
	}
	request = *frozen.Request
	finished := make(chan struct {
		result *workerRunResult
		err    error
	}, 1)
	unresolved := make(chan struct{}, 1)
	go func() {
		result, err := runWorkerParentOwned(a, request, options, unresolved)
		finished <- struct {
			result *workerRunResult
			err    error
		}{result, err}
	}()
	proofWindow := 6 * time.Second
	if options.testCallerProofWindow > 0 {
		proofWindow = options.testCallerProofWindow
	}
	absolute := time.Now().Add(time.Duration(a.bootstrap.Limits.RuntimeMS) * time.Millisecond)
	if deadline, ok := a.ctx.Deadline(); ok && deadline.Before(absolute) {
		absolute = deadline
	}
	callerTimer := time.NewTimer(max(time.Duration(0), time.Until(absolute)) + proofWindow)
	defer callerTimer.Stop()
	callerCtx := a.ctx.Done()
	abandon := func() {
		go func() {
			<-finished
			_ = workerFinishAttempt(a)
		}()
	}
	for {
		select {
		case <-callerCtx:
			callerCtx = nil
			if !callerTimer.Stop() {
				select {
				case <-callerTimer.C:
				default:
				}
			}
			callerTimer.Reset(proofWindow)
		case <-callerTimer.C:
			workerLogUnresolved(a, "caller_deadline", "runtime_ms", a.bootstrap.Limits.RuntimeMS, "proof_window", proofWindow)
			a.cancel()
			abandon()
			return nil, errWorkerOwnership
		case done := <-finished:
			return done.result, done.err
		case <-unresolved:
			abandon()
			return nil, errWorkerOwnership
		}
	}
}

func runWorkerParentOwned(a *workerAttempt, request workerRequest, options workerRunOptions, unresolved chan<- struct{}) (*workerRunResult, error) {
	if a == nil || options.probe == nil {
		return nil, errWorkerProtocol
	}
	if err := a.beginPhysicalOperation(); err != nil {
		return nil, err
	}
	defer a.endPhysicalOperation()
	a.mu.Lock()
	alreadyStarted := a.started || a.supervisorStarted
	if !alreadyStarted {
		a.supervisorStarted = true
	}
	a.mu.Unlock()
	if alreadyStarted {
		return nil, errWorkerOwnership
	}
	if err := workerContextError(a.ctx); err != nil {
		return nil, err
	}
	state, err := newWorkerProtocolState(a.bootstrap)
	if err != nil || request.validate(a.bootstrap) != nil {
		return nil, errWorkerProtocol
	}
	frozen, err := (workerFrame{Identity: a.bootstrap.Identity, Kind: "request", Request: &request}).clone()
	if err != nil {
		return nil, errWorkerProtocol
	}
	request = *frozen.Request
	if a.verifyInputs(a.ctx) != nil {
		return nil, errWorkerProtocol
	}
	if options.probe(a, nil) != nil {
		return nil, errWorkerResourceLimit
	}
	var launch workerLaunch
	if options.testLaunch != nil {
		launch = *options.testLaunch
	} else {
		launch, err = workerProductionLaunch(a)
	}
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(launch.executable, launch.arguments...)
	cmd.Env = append([]string{}, launch.environment...)
	cmd.Dir = a.path
	// Native worker launch is never CommandContext: ownership owns cancellation
	// and exactly one Wait, independently from the request's context lifetime.
	all := []*os.File{}
	pipe := func() (*os.File, *os.File, error) {
		read, write, err := os.Pipe()
		if err == nil {
			a.trackDescriptor(read)
			a.trackDescriptor(write)
			all = append(all, read, write)
		}
		return read, write, err
	}
	closeAll := func() {
		for _, file := range all {
			a.requestDescriptorClose(file)
		}
	}
	defer closeAll()
	bootRead, bootWrite, err := pipe()
	if err != nil {
		return nil, errWorkerProtocol
	}
	commandRead, commandWrite, err := pipe()
	if err != nil {
		return nil, errWorkerProtocol
	}
	resultRead, resultWrite, err := pipe()
	if err != nil {
		return nil, errWorkerProtocol
	}
	errorRead, errorWrite, err := pipe()
	if err != nil {
		return nil, errWorkerProtocol
	}
	// Supply the existing exact owned command endpoint for stdin as well.
	// A nil stdin makes exec.Cmd open an untracked DevNull descriptor whose
	// internal Close result is ignored. The child never reads standard input;
	// its command protocol uses the explicit inherited channel only.
	cmd.Stdin = commandRead
	cmd.Stdout = resultWrite
	cmd.Stderr = errorWrite
	if err := workerContextError(a.ctx); err != nil {
		closeAll()
		return nil, err
	}
	options.profile.memoryBytes = a.bootstrap.Limits.MemoryBytes
	process, startErr := workerStartOwned(a, cmd, bootRead, a.directory, commandRead, options.profile)
	if startErr != nil && process == nil {
		closeAll()
		return nil, startErr
	}
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	// Only these inherited child ends close in the parent; root stays owned.
	for _, file := range []*os.File{bootRead, commandRead, resultWrite, errorWrite} {
		a.requestDescriptorClose(file)
	}
	waitDone := make(chan error, 1)
	go func() {
		err := process.wait()
		if options.testAfterWait != nil {
			options.testAfterWait()
		}
		waitDone <- err
	}() // the sole physical Wait owner
	frames := make(chan workerPipeEvent, 1)
	readerStop := make(chan struct{})
	readerDone := make(chan struct{})
	readerStopOnce := sync.Once{}
	stopReader := func() { readerStopOnce.Do(func() { close(readerStop) }) }
	go func() {
		defer close(readerDone)
		for {
			readFrame := readWorkerFrame
			if options.testReadFrame != nil {
				readFrame = options.testReadFrame
			}
			frame, err := readFrame(resultRead)
			select {
			case frames <- workerPipeEvent{frame, err}:
			case <-readerStop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	stderrDone := make(chan bool, 1)
	go func() {
		// Drain and discard provider/native details. Excess is a fixed failure.
		n, err := io.Copy(io.Discard, io.LimitReader(errorRead, (64<<10)+1))
		stderrDone <- err == nil && n <= 64<<10
	}()
	writes := make(chan workerFrame, 2)
	writeDone := make(chan workerWriteEvent, 1)
	go func() {
		err := writeWorkerBootstrap(bootWrite, a.bootstrap)
		err = errors.Join(err, a.closeDescriptor(bootWrite))
		if err == nil {
			for f := range writes {
				if err = writeWorkerFrame(commandWrite, f); err != nil {
					break
				}
				if f.Kind == "cancel" {
					break
				}
			}
		}
		err = errors.Join(err, a.closeDescriptor(commandWrite))
		writeDone <- workerWriteEvent{err}
	}()
	startupTimer := time.NewTimer(10 * time.Second)
	defer startupTimer.Stop()
	startupC := startupTimer.C
	probeTicker := time.NewTicker(250 * time.Millisecond)
	defer probeTicker.Stop()
	var result *workerResult
	var cause error
	var waitErr error
	waited, stdoutEOF, writerJoined, stderrJoined, readerJoined := false, false, false, false, false
	var probeDone chan error
	childAbsenceObserved := false
	var cancelStarted time.Time
	var terminateTimer, killTimer, proofTimer *time.Timer
	var terminateC, killC, proofC <-chan time.Time
	cancelRequested := false
	closeWritesOnce := sync.Once{}
	writesClosed := false
	closeWrites := func() { closeWritesOnce.Do(func() { writesClosed = true; close(writes) }) }
	beginCancellation := func(why error, reason string) {
		if cause == nil {
			cause = why
		}
		a.cancel() // revoke any output publication immediately
		if cancelRequested {
			return
		}
		cancelRequested = true
		cancelStarted = time.Now()
		if state.stage == "running" {
			// The command channel closes once the child is reaped, possibly
			// before its result: a late cancellation then has no one to tell.
			if state.cancel(1) == nil && !writesClosed {
				writes <- workerFrame{Identity: a.bootstrap.Identity, Kind: "cancel", Cancel: &workerCancel{Sequence: 1, Reason: reason}}
			}
		}
		closeWrites()
		terminateTimer = time.NewTimer(2 * time.Second)
		terminateC = terminateTimer.C
		killTimer = time.NewTimer(5 * time.Second)
		killC = killTimer.C
	}
	defer func() {
		if terminateTimer != nil {
			terminateTimer.Stop()
		}
		if killTimer != nil {
			killTimer.Stop()
		}
		if proofTimer != nil {
			proofTimer.Stop()
		}
	}()
	if startErr != nil {
		beginCancellation(startErr, "cancelled")
	}
	ctxDone := a.ctx.Done()
	pipesClosed := func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, file := range all {
			if owner := a.descriptors[file]; owner == nil || !owner.joined {
				return false
			}
		}
		return true
	}
	var processOwner *workerDescriptorOwner
	var processCloseDone <-chan struct{}
	processJoined := false
	startProcessClose := func() {
		if processOwner == nil {
			processOwner = a.requestDescriptorClose(&workerProcessCloseOwner{process: process})
			processCloseDone = processOwner.done
		}
	}
	for !(waited && stdoutEOF && writerJoined && stderrJoined && readerJoined && pipesClosed() && processJoined && probeDone == nil) {
		select {
		case <-startupC:
			startupC = nil
			beginCancellation(errWorkerProtocol, "deadline")
		case <-ctxDone:
			reason := "cancelled"
			if errors.Is(a.ctx.Err(), context.DeadlineExceeded) {
				reason = "deadline"
			}
			why := workerContextError(a.ctx)
			if why == nil {
				why = errWorkerProtocol
			}
			beginCancellation(why, reason)
			ctxDone = nil
		case event := <-frames:
			if event.err != nil {
				if event.err == io.EOF {
					a.requestDescriptorClose(resultRead)
					stdoutEOF = true
					if state.stage != "terminal" && !cancelRequested {
						beginCancellation(errWorkerProtocol, "cancelled")
					}
				} else {
					beginCancellation(errWorkerProtocol, "cancelled")
					stdoutEOF = true
				}
			} else if state.accept(event.frame) != nil {
				beginCancellation(errWorkerProtocol, "cancelled")
			} else {
				switch event.frame.Kind {
				case "hello":
					if err := workerContextError(a.ctx); err != nil {
						beginCancellation(err, "deadline")
					}
					startupTimer.Stop()
					startupC = nil
					if !cancelRequested {
						if state.begin(request) != nil || writesClosed {
							// A hello read after the child was reaped cannot take a request.
							beginCancellation(errWorkerProtocol, "cancelled")
						} else {
							writes <- workerFrame{Identity: a.bootstrap.Identity, Kind: "request", Request: state.request}
						}
					}
				case "cancel_ack":
					a.mu.Lock()
					a.cancelAcknowledged = true
					a.mu.Unlock()
				case "result":
					result = event.frame.Result
					closeWrites()
				}
			}
		case event := <-writeDone:
			writerJoined = true
			writeDone = nil
			if event.err != nil && !cancelRequested {
				beginCancellation(errWorkerProtocol, "cancelled")
			}
		case <-readerDone:
			readerJoined = true
			readerDone = nil
			if cancelRequested {
				stdoutEOF = true
			}
		case <-processCloseDone:
			processCloseDone = nil
			if processOwner.err == nil {
				processJoined = true
			} else {
				beginCancellation(errWorkerOwnership, "cancelled")
			}
		case valid := <-stderrDone:
			a.requestDescriptorClose(errorRead)
			stderrJoined = true
			stderrDone = nil
			if !valid {
				beginCancellation(errWorkerProtocol, "cancelled")
			}
		case err := <-waitDone:
			waited = true
			waitErr = err
			waitDone = nil
			if options.testAfterWaitConsumed != nil {
				options.testAfterWaitConsumed()
			}
			// Do not signal numeric process/group identities after reaping.
			terminateC = nil
			if err != nil && !cancelRequested {
				beginCancellation(errWorkerProtocol, "cancelled")
			}
			closeWrites()
			a.requestDescriptorClose(bootWrite)
			a.requestDescriptorClose(commandWrite)
			startProcessClose()
		case <-probeTicker.C:
			a.mu.Lock()
			closeFailed := false
			for _, file := range all {
				if owner := a.descriptors[file]; owner != nil && owner.err != nil {
					closeFailed = true
				}
			}
			a.mu.Unlock()
			if closeFailed && !cancelRequested {
				beginCancellation(errWorkerOwnership, "cancelled")
			}
			if err := workerContextError(a.ctx); err != nil && !cancelRequested {
				beginCancellation(err, "deadline")
			}
			if !waited && !cancelRequested && probeDone == nil {
				if err := a.beginPhysicalOperation(); err != nil {
					beginCancellation(err, "resource_limit")
				} else {
					probeDone = make(chan error, 1)
					ownedProbe := probeDone
					child := cmd.Process
					go func() { err := options.probe(a, child); a.endPhysicalOperation(); ownedProbe <- err }()
				}
			}
		case err := <-probeDone:
			probeDone = nil
			if err != nil {
				if workerObservationMissingChild(err, cmd.Process) {
					// Observation is provisional, never reap/pipe/closure proof.
					// A queued sole Wait and valid terminal result must still join.
					childAbsenceObserved = true
				} else {
					// A real ceiling or inspection failure remains a failure even
					// when the select consumed successful Wait before this probe.
					beginCancellation(errWorkerResourceLimit, "resource_limit")
				}
			}
		case <-terminateC:
			terminateC = nil
			if !waited && !state.cancelAcknowledged {
				if options.testBeforeSignal != nil {
					options.testBeforeSignal()
				}
				if process.terminate() != nil {
					cause = errWorkerOwnership
				}
			}
		case <-killC:
			killC = nil
			// Proof timeout starts before every potentially blocked close/read join.
			proofTimer = time.NewTimer(time.Second)
			proofC = proofTimer.C
			if !waited {
				if options.testBeforeSignal != nil {
					options.testBeforeSignal()
				}
				if process.kill() != nil {
					cause = errWorkerOwnership
				}
			}
			for _, file := range []*os.File{resultRead, errorRead, commandWrite, bootWrite} {
				a.requestDescriptorClose(file)
			}
			stopReader()
		case <-proofC:
			proofC = nil
			if !waited || !writerJoined || !stderrJoined || !readerJoined || !pipesClosed() || !processJoined || probeDone != nil {
				var processCloseErr error
				if processOwner != nil {
					a.mu.Lock()
					processCloseErr = processOwner.err
					a.mu.Unlock()
				}
				workerLogUnresolved(a, "join_timeout", "waited", waited, "stdout_eof", stdoutEOF, "writer_joined", writerJoined, "stderr_joined", stderrJoined, "reader_joined", readerJoined, "pipes_closed", pipesClosed(), "process_joined", processJoined, "process_close_error", processCloseErr, "probe_pending", probeDone != nil, "stage", state.stage)
				select {
				case unresolved <- struct{}{}:
				default:
				}
			}
		}
	}
	stopReader()
	closeAll()
	inputErr := a.verifyInputs(a.ctx)
	a.mu.Lock()
	a.reaped = true
	a.pipesJoined = true
	physicalClosed := a.descriptorsJoinedLocked(true)
	a.mu.Unlock()
	if !physicalClosed {
		workerLogUnresolved(a, "descriptors_unjoined")
		return nil, errWorkerOwnership
	}
	_ = cancelStarted // retained for later resource evidence receipt
	if cause != nil {
		if errors.Is(cause, errWorkerOwnership) {
			workerLogUnresolved(a, "supervisor", "process_joined", processJoined, "stage", state.stage)
		}
		return nil, cause
	}
	if waitErr != nil || state.stage != "terminal" || result == nil || workerContextError(a.ctx) != nil {
		return nil, errWorkerProtocol
	}
	if result.Status != "ok" {
		switch result.Code {
		case "resource_limit":
			return nil, errWorkerResourceLimit
		case "unavailable":
			return nil, ErrUnavailable
		case "incompatible_histogram_bounds":
			return nil, ErrIncompatibleHistogramBounds
		case "schema_mismatch":
			return nil, &analysis.SchemaMismatchError{File: result.Mismatch.File, Column: result.Mismatch.Column, Reason: result.Mismatch.Reason}
		case "rejected_rows":
			rejected := result.RejectedRows
			return nil, &analysis.RejectedRowsError{File: rejected.File, Measure: rejected.Measure, Count: rejected.Count, Rows: append([]int64(nil), rejected.Rows...)}
		default:
			return nil, errWorkerProtocol
		}
	}
	if inputErr != nil {
		return nil, errWorkerProtocol
	}
	// Only this complete successful receipt discharges a missing-child sample;
	// the returns above already require a clean wait, the terminal stage and an
	// ok result. No missing PID reaches reaped/pipesJoined/cleanup or signaling
	// authority.
	if childAbsenceObserved && !(waited && processJoined && physicalClosed) {
		workerLogUnresolved(a, "child_absence_unproven")
		return nil, errWorkerOwnership
	}
	return &workerRunResult{result: *result, attempt: a}, nil
}

type workerProcessCloseOwner struct{ process *workerOwnedProcess }

func (owner *workerProcessCloseOwner) Close() error { return owner.process.closeAfterWait() }
