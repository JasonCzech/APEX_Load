//go:build darwin

package browser

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func readHARHost() (harHost, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return harHost{}, err
	}
	// macOS offers no host identity without IOKit: kern.uuid names the
	// kernel image (equal on every Mac of one build, new after each update),
	// so it is not recorded and an earlier boot never proves a process gone
	// here; same-boot proof (PID and start time) still applies.
	host := harHost{boot: strings.TrimSpace(boot), namespace: "darwin"}
	if host.boot == "" {
		return harHost{}, errors.New("boot identity is empty")
	}
	return host, nil
}

// harProcessStart reads a process's start time from the kernel's process
// table; found is false when no such process exists.
func harProcessStart(pid int) (string, bool, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.EIO) || err == nil && int(info.Proc.P_pid) != pid {
		// An empty answer: the process table has no such PID.
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	start := info.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", start.Sec, start.Usec), true, nil
}
