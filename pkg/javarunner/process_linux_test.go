//go:build linux

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

const linuxProcessTreeHelperMode = "APEXLOAD_PROCESS_TREE_TEST_HELPER"

func TestLinuxProcessTreeHelper(t *testing.T) {
	switch os.Getenv(linuxProcessTreeHelperMode) {
	case "detached":
		for {
			time.Sleep(time.Second)
		}
	case "root", "root-exit", "root-immediate-exit":
		detached := exec.Command(os.Args[0], "-test.run=^TestLinuxProcessTreeHelper$")
		detached.Env = append(os.Environ(), linuxProcessTreeHelperMode+"=detached")
		detached.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		detached.Stdout = os.Stdout
		detached.Stderr = os.Stderr
		if err := detached.Start(); err != nil {
			os.Exit(91)
		}
		pidPath := os.Getenv("APEXLOAD_PROCESS_TREE_TEST_PID_FILE")
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(detached.Process.Pid)), 0o600); err != nil {
			_ = detached.Process.Kill()
			os.Exit(92)
		}
		if os.Getenv(linuxProcessTreeHelperMode) != "root-immediate-exit" {
			// Give the best-effort /proc fallback time to observe the detached
			// child on hosts where cgroup v2 delegation is unavailable.
			time.Sleep(4 * linuxDescendantPollInterval)
		}
		if os.Getenv(linuxProcessTreeHelperMode) == "root-exit" {
			return
		}
		if os.Getenv(linuxProcessTreeHelperMode) == "root-immediate-exit" {
			return
		}
		for {
			time.Sleep(time.Second)
		}
	default:
		return
	}
}

func TestStrongProcessTreeFailsBeforeStartWithoutDelegatedCgroup(t *testing.T) {
	originalFactory := createPrivateCgroupForProcess
	originalProbe := strongLinuxContainmentProbe
	createPrivateCgroupForProcess = func() string { return "" }
	strongLinuxContainmentProbe = &linuxContainmentProbe{}
	t.Cleanup(func() {
		createPrivateCgroupForProcess = originalFactory
		strongLinuxContainmentProbe = originalProbe
	})

	marker := t.TempDir() + "/started"
	cmd := exec.Command("/bin/sh", "-c", "printf started > \"$1\"", "sh", marker)
	ConfigureProcessTree(cmd)
	err := StartStrongProcessTree(cmd)
	if !errors.Is(err, ErrStrongProcessContainmentUnavailable) {
		t.Fatalf("StartStrongProcessTree() = %v, want containment unavailable", err)
	}
	if cmd.Process != nil {
		t.Fatal("strong start launched target without delegated cgroup")
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target marker stat = %v, want not-exist", statErr)
	}
	if SupportsStrongProcessContainment() {
		t.Fatal("capability advertised while private cgroup creation was disabled")
	}
}

func TestStrongContainmentProbeWaitIsBounded(t *testing.T) {
	neverReaped := make(chan error)
	started := time.Now()
	if _, reaped := waitForLinuxContainmentProbe(neverReaped, 5*time.Millisecond); reaped {
		t.Fatal("unreaped containment probe was reported complete")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("containment probe wait exceeded its bound: %s", elapsed)
	}
}

func TestStrongProcessTreeContainsImmediateSetsidDetachWhenAvailable(t *testing.T) {
	if !SupportsStrongProcessContainment() {
		t.Skip("private cgroup delegation is unavailable")
	}
	pidPath := t.TempDir() + "/detached.pid"
	cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxProcessTreeHelper$")
	cmd.Env = append(os.Environ(),
		linuxProcessTreeHelperMode+"=root-immediate-exit",
		"APEXLOAD_PROCESS_TREE_TEST_PID_FILE="+pidPath,
	)
	stdout, stdoutChild, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	cmd.Stdout = stdoutChild
	cmd.Stderr = stdoutChild
	ConfigureProcessTree(cmd)
	if err := StartStrongProcessTree(cmd); err != nil {
		_ = stdoutChild.Close()
		t.Fatalf("StartStrongProcessTree() = %v", err)
	}
	_ = stdoutChild.Close()
	detachedPID := waitForDetachedPID(t, pidPath)
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
		t.Fatalf("KillProcessTree() = %v", err)
	}
	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatal("immediate setsid descendant retained output")
	}
	waitForProcessGone(t, detachedPID)
}

func TestCgroupPopulatedRequiresReadableValidState(t *testing.T) {
	tests := []struct {
		name      string
		events    string
		want      bool
		wantError bool
	}{
		{name: "empty", events: "populated 0\nfrozen 0\n"},
		{name: "populated", events: "populated 1\nfrozen 0\n", want: true},
		{name: "missing state", events: "frozen 0\n", wantError: true},
		{name: "invalid state", events: "populated unknown\n", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir()
			if err := os.WriteFile(path+"/cgroup.events", []byte(test.events), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := cgroupPopulated(path)
			if (err != nil) != test.wantError {
				t.Fatalf("cgroupPopulated() error = %v, wantError %v", err, test.wantError)
			}
			if got != test.want {
				t.Fatalf("cgroupPopulated() = %v, want %v", got, test.want)
			}
		})
	}

	t.Run("unreadable or absent events", func(t *testing.T) {
		got, err := cgroupPopulated(t.TempDir())
		if err == nil {
			t.Fatal("cgroupPopulated() accepted an absent cgroup.events file")
		}
		if got {
			t.Fatal("cgroupPopulated() reported an unreadable cgroup as populated")
		}
	})
}

func TestKillProcessTreeAfterRootExitUsesOwnedDescendantIdentity(t *testing.T) {
	pidPath := t.TempDir() + "/detached.pid"
	cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxProcessTreeHelper$")
	cmd.Env = append(os.Environ(),
		linuxProcessTreeHelperMode+"=root-exit",
		"APEXLOAD_PROCESS_TREE_TEST_PID_FILE="+pidPath,
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
	detachedPID := waitForDetachedPID(t, pidPath)
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
	waitForProcessGone(t, detachedPID)
}

func TestKillProcessTreeTerminatesSetsidDescendantAndUnblocksOutput(t *testing.T) {
	pidPath := t.TempDir() + "/detached.pid"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLinuxProcessTreeHelper$")
	cmd.Env = append(os.Environ(),
		linuxProcessTreeHelperMode+"=root",
		"APEXLOAD_PROCESS_TREE_TEST_PID_FILE="+pidPath,
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

	detachedPID := waitForDetachedPID(t, pidPath)
	t.Cleanup(func() {
		_ = syscall.Kill(detachedPID, syscall.SIGKILL)
	})
	drainDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdout)
		close(drainDone)
	}()

	// CommandContext cancellation exercises the same KillProcessTree hook used
	// by Java action timeout/cancellation.
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
	waitForProcessGone(t, detachedPID)
}

func waitForDetachedPID(t *testing.T, path string) int {
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

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) || linuxProcessIsZombie(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("detached helper PID %d survived tree termination", pid)
}

func linuxProcessIsZombie(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	closeParen := -1
	for index := len(data) - 1; index >= 0; index-- {
		if data[index] == ')' {
			closeParen = index
			break
		}
	}
	return closeParen >= 0 && closeParen+2 < len(data) && data[closeParen+2] == 'Z'
}
