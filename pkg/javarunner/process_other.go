//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package javarunner

import (
	"errors"
	"os"
	"os/exec"
)

func supportsStrongProcessContainment() bool {
	return false
}

func configureProcessTree(*exec.Cmd) {}

func startProcessTree(cmd *exec.Cmd) error {
	return cmd.Start()
}

func startStrongProcessTree(*exec.Cmd) error {
	return ErrStrongProcessContainmentUnavailable
}

func killProcessTree(cmd *exec.Cmd) error {
	err := cmd.Process.Kill()
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
