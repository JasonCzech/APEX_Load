package javarunner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigureProcessTreePreservesCommandCancellationContract(t *testing.T) {
	plain := exec.Command("java", "-version")
	ConfigureProcessTree(plain)
	if plain.Cancel != nil {
		t.Fatal("plain exec.Command received a Cancel function; Go rejects this command at Start")
	}

	contextual := exec.CommandContext(context.Background(), "java", "-version")
	originalCancel := contextual.Cancel
	if originalCancel == nil {
		t.Fatal("exec.CommandContext unexpectedly has no Cancel function")
	}
	ConfigureProcessTree(contextual)
	if contextual.Cancel == nil {
		t.Fatal("CommandContext lost whole-tree cancellation")
	}
}

func TestSupportedPlatformsAdvertiseStrongProcessContainment(t *testing.T) {
	got := SupportsStrongProcessContainment()
	switch runtime.GOOS {
	case "windows":
		if !got {
			t.Fatal("Windows Job Object containment was not advertised")
		}
	case "darwin", "aix", "dragonfly", "freebsd", "netbsd", "openbsd", "solaris":
		if got {
			t.Fatalf("unsupported platform %s advertised strong containment", runtime.GOOS)
		}
	}
}

type observedCloser struct {
	closed atomic.Bool
	done   chan struct{}
}

func (c *observedCloser) Close() error {
	c.closed.Store(true)
	if c.done != nil {
		close(c.done)
	}
	return nil
}

var _ io.Closer = (*observedCloser)(nil)

func TestWaitForOutputDrainForcesBoundedReaderClosure(t *testing.T) {
	closer := &observedCloser{}
	neverDone := make(chan struct{})
	start := time.Now()
	if WaitForOutputDrain(10*time.Millisecond, []io.Closer{closer}, neverDone) {
		t.Fatal("WaitForOutputDrain unexpectedly reported a completed reader")
	}
	if !closer.closed.Load() {
		t.Fatal("WaitForOutputDrain did not close the blocked reader")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("WaitForOutputDrain exceeded its bound: %s", elapsed)
	}
}

func TestWaitForOutputDrainReportsTruncationWhenForcedCloseUnblocksReader(t *testing.T) {
	done := make(chan struct{})
	closer := &observedCloser{done: done}
	if WaitForOutputDrain(10*time.Millisecond, []io.Closer{closer}, done) {
		t.Fatal("forced reader closure was reported as a clean output drain")
	}
	if !closer.closed.Load() {
		t.Fatal("blocked reader was not closed")
	}
	select {
	case <-done:
	default:
		t.Fatal("test reader did not unwind after forced closure")
	}
}

func TestReapFailedStartOwnsDirectRootWait(t *testing.T) {
	if os.Getenv("APEXLOAD_TEST_FAILED_START_REAP") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReapFailedStartOwnsDirectRootWait$")
	cmd.Env = append(os.Environ(), "APEXLOAD_TEST_FAILED_START_REAP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ReapFailedStart(cmd):
		if cmd.ProcessState == nil {
			t.Fatal("failed-start reaper completed without publishing process state")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed-start direct root was not reaped")
	}
}

func TestReapFailedStartSerializesWithCommandContextCancellation(t *testing.T) {
	if os.Getenv("APEXLOAD_TEST_FAILED_START_REAP") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	for iteration := 0; iteration < 10; iteration++ {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReapFailedStartOwnsDirectRootWait$")
		cmd.Env = append(os.Environ(), "APEXLOAD_TEST_FAILED_START_REAP=1")
		ConfigureProcessTree(cmd)
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		done := ReapFailedStart(cmd)
		cancel()
		select {
		case <-done:
			if cmd.ProcessState == nil {
				t.Fatal("failed-start reaper completed without publishing process state")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("failed-start reaper did not complete after context cancellation")
		}
	}
}

func TestTerminalContainmentStateRejectsStartsAndTracksPendingReap(t *testing.T) {
	resetProcessContainmentStateForTest()
	t.Cleanup(resetProcessContainmentStateForTest)

	reap := make(chan error)
	markProcessContainmentFailed(reap)
	if !ProcessContainmentFailed() || SupportsStrongProcessContainment() {
		t.Fatal("terminal containment state was not reflected in process capabilities")
	}
	command := exec.Command("must-not-start")
	if err := StartProcessTree(command); !errors.Is(err, ErrProcessContainmentFailed) {
		t.Fatalf("StartProcessTree() = %v, want terminal containment rejection", err)
	}
	if command.Process != nil {
		t.Fatal("terminal containment rejection started a new process")
	}

	shortCtx, cancelShort := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelShort()
	if err := WaitForProcessContainmentReaps(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForProcessContainmentReaps() = %v, want pending reap deadline", err)
	}
	close(reap)
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := WaitForProcessContainmentReaps(waitCtx); err != nil {
		t.Fatalf("WaitForProcessContainmentReaps() after reap = %v", err)
	}
}

func resetProcessContainmentStateForTest() {
	processContainmentState.failed.Store(false)
	processContainmentState.mu.Lock()
	processContainmentState.reaps = nil
	processContainmentState.mu.Unlock()
}
