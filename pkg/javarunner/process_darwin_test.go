//go:build darwin

package javarunner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const darwinProcessTreeHelperMode = "APEXLOAD_DARWIN_PROCESS_TREE_TEST_HELPER"

func TestDarwinProcessTreeHelper(t *testing.T) {
	switch os.Getenv(darwinProcessTreeHelperMode) {
	case "detached":
		for {
			time.Sleep(time.Second)
		}
	case "root", "root-exit":
		detached := exec.Command(os.Args[0], "-test.run=^TestDarwinProcessTreeHelper$")
		detached.Env = append(os.Environ(), darwinProcessTreeHelperMode+"=detached")
		detached.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		detached.Stdout = os.Stdout
		detached.Stderr = os.Stderr
		if err := detached.Start(); err != nil {
			os.Exit(91)
		}
		pidPath := os.Getenv("APEXLOAD_DARWIN_PROCESS_TREE_TEST_PID_FILE")
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(detached.Process.Pid)), 0o600); err != nil {
			_ = detached.Process.Kill()
			os.Exit(92)
		}
		time.Sleep(4 * darwinDescendantPollInterval)
		if os.Getenv(darwinProcessTreeHelperMode) == "root-exit" {
			return
		}
		for {
			time.Sleep(time.Second)
		}
	default:
		return
	}
}

func TestDarwinKillProcessTreeAfterRootExitUsesOwnedDescendantIdentity(t *testing.T) {
	pidPath := t.TempDir() + "/detached.pid"
	cmd := exec.Command(os.Args[0], "-test.run=^TestDarwinProcessTreeHelper$")
	cmd.Env = append(os.Environ(),
		darwinProcessTreeHelperMode+"=root-exit",
		"APEXLOAD_DARWIN_PROCESS_TREE_TEST_PID_FILE="+pidPath,
	)
	stdout, stdoutChild, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	cmd.Stdout = stdoutChild
	cmd.Stderr = stdoutChild
	ConfigureProcessTree(cmd)
	if err := StartProcessTree(cmd); err != nil {
		_ = stdoutChild.Close()
		t.Fatalf("StartProcessTree() = %v", err)
	}
	_ = stdoutChild.Close()
	detachedPID := waitForDarwinDetachedPID(t, pidPath)
	t.Cleanup(func() {
		_ = syscall.Kill(detachedPID, syscall.SIGKILL)
	})
	drainDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		close(drainDone)
	}()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("root Wait() = %v", err)
	}
	if err := KillProcessTree(cmd); err != nil {
		t.Fatalf("KillProcessTree() after root exit = %v", err)
	}
	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatal("owned setsid descendant retained output after root exit")
	}
	waitForDarwinProcessGone(t, detachedPID)
}

func TestDarwinKillProcessTreeTerminatesSetsidDescendantAndUnblocksOutput(t *testing.T) {
	pidPath := t.TempDir() + "/detached.pid"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDarwinProcessTreeHelper$")
	cmd.Env = append(os.Environ(),
		darwinProcessTreeHelperMode+"=root",
		"APEXLOAD_DARWIN_PROCESS_TREE_TEST_PID_FILE="+pidPath,
	)
	stdout, stdoutChild, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	cmd.Stdout = stdoutChild
	cmd.Stderr = stdoutChild
	ConfigureProcessTree(cmd)
	if err := StartProcessTree(cmd); err != nil {
		_ = stdoutChild.Close()
		t.Fatalf("StartProcessTree() = %v", err)
	}
	_ = stdoutChild.Close()

	detachedPID := waitForDarwinDetachedPID(t, pidPath)
	t.Cleanup(func() {
		_ = syscall.Kill(detachedPID, syscall.SIGKILL)
	})
	drainDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		close(drainDone)
	}()

	cancel()
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("direct process was not reaped after cancellation")
	}
	if err := KillProcessTree(cmd); err != nil {
		t.Fatalf("KillProcessTree() after Wait = %v", err)
	}
	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatal("setsid descendant retained inherited output and blocked drain")
	}
	waitForDarwinProcessGone(t, detachedPID)
}

func TestDarwinSnapshotDoesNotAdoptReusedEmptyProcessGroup(t *testing.T) {
	root := darwinProcessIdentity{pid: 100, parent: 1, group: 100, startSec: 10}
	tree := &managedDarwinProcessTree{
		root: root, known: map[int]darwinProcessIdentity{root.pid: root},
	}
	unrelated := darwinProcessIdentity{pid: 200, parent: 1, group: 100, startSec: 20}
	tree.discoverFromSnapshotLocked(map[int]darwinProcessIdentity{
		unrelated.pid: unrelated,
	})
	if _, adopted := tree.known[unrelated.pid]; adopted {
		t.Fatal("reused numeric process group adopted an unrelated identity")
	}

	child := darwinProcessIdentity{pid: 101, parent: root.pid, group: root.group, startSec: 11}
	tree.discoverFromSnapshotLocked(map[int]darwinProcessIdentity{
		root.pid: root, child.pid: child,
	})
	if _, adopted := tree.known[child.pid]; !adopted {
		t.Fatal("live owned group member did not extend ownership to its child")
	}
}

func TestDarwinSharedTrackerQuiescesAndRestarts(t *testing.T) {
	waitForDarwinTrackerState(t, false)
	darwinTrackerState.mu.Lock()
	startsBefore := darwinTrackerState.starts
	darwinTrackerState.mu.Unlock()

	firstCommand := &exec.Cmd{}
	if err := registerDarwinProcessTree(firstCommand, &managedDarwinProcessTree{known: make(map[int]darwinProcessIdentity)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := takeDarwinProcessTree(firstCommand); !ok {
		t.Fatal("first synthetic process tree was not registered")
	}
	waitForDarwinTrackerState(t, false)

	secondCommand := &exec.Cmd{}
	if err := registerDarwinProcessTree(secondCommand, &managedDarwinProcessTree{known: make(map[int]darwinProcessIdentity)}); err != nil {
		t.Fatal(err)
	}
	darwinTrackerState.mu.Lock()
	startsAfter := darwinTrackerState.starts
	darwinTrackerState.mu.Unlock()
	if startsAfter < startsBefore+2 {
		t.Fatalf("tracker starts = %d, want at least %d after restart", startsAfter, startsBefore+2)
	}
	if _, ok := takeDarwinProcessTree(secondCommand); !ok {
		t.Fatal("second synthetic process tree was not registered")
	}
	waitForDarwinTrackerState(t, false)
}

func TestDarwinStrongProcessTreeFailsBeforeTargetStarts(t *testing.T) {
	marker := t.TempDir() + "/started"
	cmd := exec.Command("/bin/sh", "-c", "printf started > \"$1\"", "sh", marker)
	ConfigureProcessTree(cmd)
	err := StartStrongProcessTree(cmd)
	if !errors.Is(err, ErrStrongProcessContainmentUnavailable) {
		t.Fatalf("StartStrongProcessTree() = %v, want containment unavailable", err)
	}
	if cmd.Process != nil {
		t.Fatal("Darwin strong start launched a target despite unavailable containment")
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target marker stat = %v, want not-exist", statErr)
	}
	if SupportsStrongProcessContainment() {
		t.Fatal("Darwin advertised polling-based cleanup as strong containment")
	}
}

func waitForDarwinTrackerState(t *testing.T, wantRunning bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		darwinTrackerState.mu.Lock()
		running := darwinTrackerState.running
		darwinTrackerState.mu.Unlock()
		if running == wantRunning {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Darwin tracker running state did not become %v", wantRunning)
}

func waitForDarwinDetachedPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(string(data))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached helper PID was not published")
	return 0
}

func waitForDarwinProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("detached helper PID %d survived tree termination", pid)
}
