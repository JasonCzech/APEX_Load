//go:build linux || darwin || windows

package duckdb

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type workerFixtureExecutor struct {
	mode     string
	commands *os.File
}

func (e workerFixtureExecutor) execute(ctx context.Context, a *workerAttempt, r workerRequest) (workerResult, error) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "AZURE_CLIENT_SECRET", "AWS_SECRET_ACCESS_KEY", "APEXLOAD_DSN", "APEXLOAD_OTEL_ENABLED", "HOME"} {
		if os.Getenv(name) != "" {
			return workerResult{}, errWorkerProtocol
		}
	}
	if e.mode == "stdin-owned" {
		if e.commands == nil {
			return workerResult{}, errWorkerProtocol
		}
		stdin, stdinErr := os.Stdin.Stat()
		commands, commandErr := e.commands.Stat()
		if stdinErr != nil || commandErr != nil || stdin.Mode()&os.ModeNamedPipe == 0 || !os.SameFile(stdin, commands) {
			return workerResult{}, errWorkerProtocol
		}
	}
	if e.mode == "panic" {
		panic("fixture native crash")
	}
	if e.mode == "exit" {
		os.Exit(19)
	}
	if e.mode == "exit-clean" {
		// A successful exit after taking the request but before any result.
		os.Exit(0)
	}
	if e.mode == "cancel" || e.mode == "ack-hang" {
		file, err := a.root.OpenFile("helper-operation-started", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return workerResult{}, err
		}
		file.Close()
	}
	if e.mode == "cancel" {
		<-ctx.Done()
		return workerResult{}, ctx.Err()
	}
	if e.mode == "ack-hang" {
		<-ctx.Done()
		select {}
	}
	if e.mode == "invalid-frame" {
		os.Stdout.Write([]byte{255, 255, 255, 255})
		return workerResult{}, errWorkerProtocol
	}
	if e.mode == "late-success" {
		file, err := a.root.OpenFile("helper-operation-started", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return workerResult{}, err
		}
		file.Close()
		ns, err := strconv.ParseInt(os.Getenv("APEXLOAD_NATIVE_TEST_DEADLINE"), 10, 64)
		if err != nil {
			return workerResult{}, errWorkerProtocol
		}
		timer := time.NewTimer(max(0, time.Until(time.Unix(0, ns))) + 20*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return workerResult{}, ctx.Err()
		}
	}
	if e.mode == "probe-finish" {
		// The parent creates this private fixture marker only after its exact
		// recurring probe entered, making probe-before-success deterministic.
		for {
			if _, err := os.Stat(filepath.Join(a.path, "helper-probe-finish")); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return workerResult{}, err
			}
			select {
			case <-ctx.Done():
				return workerResult{}, ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	if r.Preview == nil {
		return workerResult{}, errWorkerProtocol
	}
	number := "9007199254740993"
	return workerResult{Status: "ok", Preview: &workerPreviewResult{Schema: analysis.ParquetSchemaPreview{Rows: 3, FileSize: 100, Columns: []analysis.ParquetColumn{{Path: "value", PathComponents: []string{"value"}, PhysicalType: "INT64"}}}, Samples: [][]workerCell{{{Kind: "number", Number: &number}}}}}, nil
}
func TestWorkerRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("APEXLOAD_NATIVE_TEST_HELPER") != "1" {
		return
	}
	boot, root, commands, err := workerInheritedChannels()
	if err != nil {
		os.Exit(78)
	}
	code := runWorkerChild(boot, root, commands, os.Stdout, workerFixtureExecutor{mode: os.Getenv("APEXLOAD_NATIVE_TEST_MODE"), commands: commands})
	boot.Close()
	root.Close()
	commands.Close()
	os.Exit(code)
}
func workerRuntimeTestLaunch(t *testing.T, a *workerAttempt, mode string) *workerLaunch {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workerExecutableDigest()
	if err != nil {
		t.Fatal(err)
	}
	a.bootstrap.Identity.BuildID = digest
	return &workerLaunch{executable: path, arguments: []string{"-test.run=^TestWorkerRuntimeHelperProcess$"}, environment: append(workerChildEnvironment(a.path), "APEXLOAD_NATIVE_TEST_HELPER=1", "APEXLOAD_NATIVE_TEST_MODE="+mode)}
}
func TestWorkerActualSubprocessHappyPathReapsJoinsAndRetainsPublicationLease(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "LD_PRELOAD", "AZURE_CLIENT_SECRET", "APEXLOAD_DSN", "APEXLOAD_OTEL_ENABLED"} {
		t.Setenv(key, "forbidden-parent-sentinel")
	}
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	launch := workerRuntimeTestLaunch(t, a, "preview")
	r := workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}
	result, err := runWorkerParent(a, r, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: launch})
	if err != nil {
		t.Fatal("actual helper failed", err)
	}
	if !a.reaped || !a.pipesJoined || admission.releases.Load() != 0 {
		t.Fatal("exit released publication lease or failed joins")
	}
	if *result.result.Preview.Samples[0][0].Number != "9007199254740993" {
		t.Fatal("exact sample number changed")
	}
	if err = result.release(); err != nil || admission.releases.Load() != 1 {
		t.Fatal("completion failed", err)
	}
}
func TestWorkerActualCrashExitAndMalformedProtocolPreserveParent(t *testing.T) {
	for _, mode := range []string{"panic", "exit", "exit-clean", "invalid-frame"} {
		t.Run(mode, func(t *testing.T) {
			a, admission := workerRuntimeTestAttempt(t, context.Background())
			launch := workerRuntimeTestLaunch(t, a, mode)
			result, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: launch})
			if result != nil || err == nil || strings.Contains(err.Error(), "fixture") {
				t.Fatal("child failure was published/disclosed", err)
			}
			if !a.reaped || !a.pipesJoined || admission.releases.Load() != 0 {
				t.Fatal("child failure did not retain proven publication lease")
			}
			if err = a.release(); err != nil || admission.releases.Load() != 1 {
				t.Fatal("failed child ownership leaked", err)
			}
		})
	}
}
func TestWorkerActualCancellationAcknowledgementIsNotExitProof(t *testing.T) {
	for _, mode := range []string{"cancel", "ack-hang"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			a, admission := workerRuntimeTestAttempt(t, ctx)
			launch := workerRuntimeTestLaunch(t, a, mode)
			type completion struct {
				result *workerRunResult
				err    error
			}
			finished := make(chan completion, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-finished:
					case <-time.After(8 * time.Second):
						t.Error("controlled helper did not join during failure cleanup")
					}
				}
			}()
			go func() {
				result, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: launch})
				finished <- completion{result, err}
			}()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
		waiting:
			for {
				select {
				case <-ticker.C:
					if _, err := a.root.Stat("helper-operation-started"); err == nil {
						break waiting
					}
				case done := <-finished:
					joined = true
					t.Fatal("helper did not enter controlled operation", done.err)
				case <-deadline.C:
					t.Fatal("helper operation entry not observed")
				}
			}
			if admission.releases.Load() != 0 {
				t.Fatal("active operation lost physical admission")
			}
			cancel()
			var done completion
			select {
			case done = <-finished:
				joined = true
			case <-time.After(8 * time.Second):
				t.Fatal("cancelled actual helper was not reaped")
			}
			if done.result != nil || done.err == nil {
				t.Fatal("cancelled worker result accepted")
			}
			if errors.Is(done.err, errWorkerOwnership) {
				t.Fatal("owned actual child could not be reaped", done.err)
			}
			if !a.reaped || !a.pipesJoined || !a.cancelAcknowledged || admission.releases.Load() != 0 {
				t.Fatal("acknowledgement replaced actual reap/join proof")
			}
			if err := a.release(); err != nil || admission.releases.Load() != 1 {
				t.Fatal("cancelled ownership leaked", err)
			}
		})
	}
}

func TestWorkerActualLateResultCannotBeatDelayedDeadlineErr(t *testing.T) {
	a, admission := workerRuntimeTestAttempt(t, context.Background())
	launch := workerRuntimeTestLaunch(t, a, "late-success")
	deadline := time.Now().Add(2 * time.Second)
	a.ctx = workerLaggingDeadlineContext{Context: context.Background(), deadline: deadline}
	launch.environment = append(launch.environment, "APEXLOAD_NATIVE_TEST_DEADLINE="+strconv.FormatInt(deadline.UnixNano(), 10))
	result, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: launch})
	if result != nil || err == nil {
		t.Fatal("late success admitted while Err callback lagged")
	}
	if !a.reaped || !a.pipesJoined || admission.releases.Load() != 0 {
		t.Fatal("late result lost physical/publication ownership")
	}
	if _, err := a.root.Stat("helper-operation-started"); err != nil {
		t.Fatal("late-result fixture did not enter actual operation", err)
	}
	if err := a.release(); err != nil || admission.releases.Load() != 1 {
		t.Fatal("late result cleanup", err)
	}
}

func TestWorkerFailedPipeCloseAndBlockedFrameReadReturnBoundedRetainExactOwners(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, admission := workerRuntimeTestAttempt(t, ctx)
	launch := workerRuntimeTestLaunch(t, a, "preview")
	entered, resume := make(chan struct{}), make(chan struct{})
	var readOnce, resumeOnce sync.Once
	var physical atomic.Pointer[os.File]
	unblock := func() { resumeOnce.Do(func() { close(resume) }) }
	defer unblock()
	a.mu.Lock()
	a.closeDescriptorHook = func(owner io.Closer) error {
		if owner == physical.Load() {
			return errWorkerOwnership
		}
		return owner.Close()
	}
	a.mu.Unlock()
	finished := make(chan error, 1)
	go func() {
		_, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{
			probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: launch, testCallerProofWindow: 30 * time.Millisecond,
			testReadFrame: func(input io.Reader) (workerFrame, error) {
				readOnce.Do(func() { physical.Store(input.(*os.File)); close(entered) })
				<-resume
				return readWorkerFrame(input)
			},
		})
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("owned result reader did not enter")
	}
	file := physical.Load()
	if !errors.Is(a.closeDescriptor(file), errWorkerOwnership) {
		t.Fatal("failed actual pipe close hidden")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, errWorkerOwnership) {
			t.Fatal("unjoined reader unexpectedly returned proof", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked frame reader trapped public caller")
	}
	a.mu.Lock()
	owner := a.descriptors[file]
	reaped, pipesJoined := a.reaped, a.pipesJoined
	a.mu.Unlock()
	if owner == nil || owner.descriptor != file || owner.joined || reaped || pipesJoined || admission.releases.Load() != 0 {
		t.Fatal("failed pipe or active reader lost exact ownership")
	}
	if _, err := os.Stat(a.path); err != nil {
		t.Fatal("unjoined reader scratch removed", err)
	}
	// The test supplies actual OS closure separately. No production cancellation
	// or repeated Close converts the failed receipt into physical proof.
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	owner.err = nil
	owner.joined = true
	a.notifyPhysicalLocked()
	a.mu.Unlock()
	unblock()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for admission.releases.Load() == 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("sole supervisor did not finish actual reader/wait/pipe joins")
		}
	}
	if admission.releases.Load() != 1 {
		t.Fatal("physical continuation released admission more than once")
	}
}

func TestWorkerPrivateExecutableSnapshotLaunchesApprovedBytesAfterInstallerReplacement(t *testing.T) {
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	approvedLaunch := workerRuntimeTestLaunch(t, a, "preview")
	// The installer path is independently replaceable; the actual child uses
	// only the private snapshot produced from a retained approved descriptor.
	original, err := os.Open(approvedLaunch.executable)
	if err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(t.TempDir(), "controller-install")
	copy, err := os.OpenFile(installer, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		original.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(copy, original)
	err = errors.Join(copyErr, copy.Close(), original.Close())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workerSnapshotExecutable(a, installer, a.bootstrap.Identity.BuildID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(installer, []byte("replacement must never be executed"), 0700); err != nil {
		t.Fatal(err)
	}
	launch := *approvedLaunch
	launch.executable = snapshot
	result, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{probe: func(*workerAttempt, *os.Process) error { return nil }, testLaunch: &launch})
	if err != nil || result == nil {
		t.Fatal("approved snapshot failed after installer replacement", err)
	}
	if err = result.release(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerBlockedRecurringProbeDoesNotPreventActualKillAndWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, admission := workerRuntimeTestAttempt(t, ctx)
	launch := workerRuntimeTestLaunch(t, a, "ack-hang")
	entered, resume, reaped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var enteredOnce, resumeOnce sync.Once
	unblock := func() { resumeOnce.Do(func() { close(resume) }) }
	defer unblock()
	finished := make(chan error, 1)
	go func() {
		_, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{
			testLaunch: launch, testCallerProofWindow: 30 * time.Millisecond, testAfterWait: func() { close(reaped) },
			probe: func(_ *workerAttempt, child *os.Process) error {
				if child != nil {
					enteredOnce.Do(func() { close(entered) })
					<-resume
				}
				return nil
			},
		})
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("recurring owned probe did not enter")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, errWorkerOwnership) {
			t.Fatal("blocked probe invented closure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked probe trapped public caller")
	}
	select {
	case <-reaped:
	case <-time.After(7 * time.Second):
		t.Fatal("blocked probe prevented sole supervisor kill/Wait")
	}
	if admission.releases.Load() != 0 {
		t.Fatal("probe operation lost charge before actual join")
	}
	unblock()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for admission.releases.Load() == 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("actual probe join did not drain retained supervisor")
		}
	}
}

func TestWorkerLateProbeFailureSurvivesSuccessfulWaitAndMissingSampleRequiresProof(t *testing.T) {
	for _, tc := range []struct {
		name             string
		missing, foreign bool
	}{
		{"ceiling-after-wait", false, false}, {"exact-missing-after-wait", true, false}, {"foreign-missing-after-wait", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			a, admission := workerRuntimeTestAttempt(t, ctx)
			launch := workerRuntimeTestLaunch(t, a, "probe-finish")
			entered, resume, reaped := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var enteredOnce, resumeOnce, reapedOnce sync.Once
			unblock := func() { resumeOnce.Do(func() { close(resume) }) }
			var signals atomic.Int32
			finished := make(chan struct {
				result *workerRunResult
				err    error
			}, 1)
			joined := make(chan struct{})
			go func() {
				defer close(joined)
				result, err := runWorkerParent(a, workerRequest{Preview: &workerPreviewOperation{InputHandle: a.bootstrap.Handles[0].Handle}}, workerRunOptions{
					testLaunch: launch, testBeforeSignal: func() { signals.Add(1) }, testAfterWaitConsumed: func() { reapedOnce.Do(func() { close(reaped) }) },
					probe: func(_ *workerAttempt, child *os.Process) error {
						if child == nil {
							return nil
						}
						enteredOnce.Do(func() { close(entered) })
						file, err := a.root.OpenFile("helper-probe-finish", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
						if err != nil {
							return errWorkerResourceLimit
						}
						a.trackDescriptor(file)
						if a.closeDescriptor(file) != nil {
							return errWorkerOwnership
						}
						<-resume
						if tc.missing {
							pid := child.Pid
							if tc.foreign {
								pid++
							}
							return &workerMissingProcessObservation{pid: pid}
						}
						return errWorkerResourceLimit
					},
				})
				finished <- struct {
					result *workerRunResult
					err    error
				}{result, err}
			}()
			t.Cleanup(func() {
				cancel()
				unblock()
				select {
				case <-joined:
				case <-time.After(8 * time.Second):
					t.Error("retaining exact late-probe supervisor")
					return
				}
				if err := a.release(); err != nil {
					t.Error("retaining unjoined late-probe attempt", err)
				}
			})
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("probe never entered")
			}
			select {
			case <-reaped:
			case <-ctx.Done():
				t.Fatal("successful sole Wait was not consumed")
			}
			if admission.releases.Load() != 0 {
				t.Fatal("blocked sample authorized physical release")
			}
			unblock()
			select {
			case done := <-finished:
				if tc.missing && !tc.foreign {
					if done.err != nil || done.result == nil {
						t.Fatal("exact missing sample did not reconcile with complete receipt", done.err)
					}
					if err := done.result.release(); err != nil {
						t.Fatal(err)
					}
				} else {
					if done.result != nil || !errors.Is(done.err, errWorkerResourceLimit) {
						t.Fatal("late actual breach/foreign observation was erased", done.err)
					}
					if err := a.release(); err != nil {
						t.Fatal(err)
					}
				}
			case <-ctx.Done():
				t.Fatal("late probe completion did not join")
			}
			if signals.Load() != 0 {
				t.Fatal("signal attempted after exact Wait was consumed")
			}
			if admission.releases.Load() != 1 {
				t.Fatal("complete supervisor proof did not release once")
			}
		})
	}
}
