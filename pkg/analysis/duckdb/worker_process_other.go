//go:build !linux && !darwin && !windows

package duckdb

import (
	"os"
	"os/exec"
)

// Unsupported operating systems cannot execute native SQL.
type workerOwnedProcess struct{}

func workerStartOwned(*workerAttempt, *exec.Cmd, *os.File, *os.File, *os.File, workerProcessProfile) (*workerOwnedProcess, error) {
	return nil, ErrUnavailable
}
func (*workerOwnedProcess) wait() error           { return ErrUnavailable }
func (*workerOwnedProcess) terminate() error      { return ErrUnavailable }
func (*workerOwnedProcess) kill() error           { return ErrUnavailable }
func (*workerOwnedProcess) closeAfterWait() error { return ErrUnavailable }
func workerInheritedChannels() (*os.File, *os.File, *os.File, error) {
	return nil, nil, nil, ErrUnavailable
}
func workerInheritedRootPath(*os.File) (string, error) { return "", ErrUnavailable }
func workerPlatformEnvironment(string) []string        { return nil }
