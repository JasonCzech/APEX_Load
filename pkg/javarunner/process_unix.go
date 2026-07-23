//go:build aix || dragonfly || freebsd || netbsd || openbsd || solaris

package javarunner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func supportsStrongProcessContainment() bool {
	return false
}

func startStrongProcessTree(_ *exec.Cmd) error {
	return ErrStrongProcessContainmentUnavailable
}

func configureProcessTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func startProcessTree(cmd *exec.Cmd) error {
	return cmd.Start()
}

func killProcessTree(cmd *exec.Cmd) error {
	// Setpgid makes the child's PID the process-group ID. Signal the group
	// first so descendants cannot survive after the Java launcher exits.
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	// If the platform refused the group signal, still terminate the direct
	// process rather than turning a best-effort tree cleanup into no cleanup.
	directErr := cmd.Process.Kill()
	if directErr == nil || errors.Is(directErr, os.ErrProcessDone) {
		return nil
	}
	return err
}
