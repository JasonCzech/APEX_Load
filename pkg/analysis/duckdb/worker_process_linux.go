//go:build linux

package duckdb

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func workerConfigureUnix(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
func workerDirectoryFDPath(root *os.File) (string, error) {
	return "/proc/self/fd/" + strconv.FormatUint(uint64(root.Fd()), 10), nil
}
