//go:build linux

package procidentity

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// The kernel interfaces are variables so tests can present a hidden /proc
// entry, a foreign /proc and the kill probe's answers.
var (
	readFile = os.ReadFile
	readLink = os.Readlink
	killZero = func(pid int) error { return unix.Kill(pid, 0) }
	hostName = os.Hostname
)

func readHost() (Host, error) {
	boot, err := readFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return Host{}, err
	}
	namespace, err := readLink("/proc/self/ns/pid")
	if err != nil {
		return Host{}, err
	}
	// A process's start time counts from boot as its time namespace sees it,
	// so a judge in another time namespace must not compare start times.
	if timeNamespace, err := readLink("/proc/self/ns/time"); err == nil {
		namespace += " " + timeNamespace
	} else if !errors.Is(err, os.ErrNotExist) {
		return Host{}, err
	}
	host := Host{Boot: strings.TrimSpace(string(boot)), Namespace: namespace}
	// A machine-id alone is not host-unique: container images and cloned VMs
	// share one. Paired with the host name (distinct per pod and per default
	// container), an earlier boot proves a process gone only on the same
	// host; a renamed host simply proves nothing. Hosts sharing a HAR
	// directory or analytical storage must not share both (a VM cloned
	// without regenerating its machine-id or renaming it would).
	hostname, _ := hostName()
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if machine, err := readFile(path); err == nil && strings.TrimSpace(string(machine)) != "" && strings.TrimSpace(hostname) != "" {
			host.Machine = strings.TrimSpace(string(machine)) + "@" + strings.TrimSpace(hostname)
			break
		}
	}
	// A /proc mounted from another PID namespace would describe other
	// processes under the same numbers.
	if self, err := readLink("/proc/self"); err != nil || self != strconv.Itoa(os.Getpid()) {
		return Host{}, errors.New("/proc does not describe this PID namespace")
	}
	if host.Boot == "" || host.Namespace == "" {
		return Host{}, errors.New("boot or PID namespace identity is empty")
	}
	return host, nil
}

// processStart reads a process's start time (clock ticks after boot) from
// /proc; found is false when no such process exists.
func processStart(pid int) (string, bool, error) {
	data, err := readFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		// /proc hides other users' processes under hidepid or ProtectProc, so
		// a missing entry alone proves nothing: only the kernel's ESRCH does.
		if killErr := killZero(pid); errors.Is(killErr, unix.ESRCH) {
			return "", false, nil
		}
		return "", false, errors.New("process presence is undecidable from /proc")
	}
	if err != nil {
		return "", false, err
	}
	// The command name is parenthesized and may hold spaces; fields
	// resume after its last ')', starting at field 3 (state).
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", false, errors.New("malformed process stat")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return "", false, errors.New("malformed process stat")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", false, err
	}
	return fields[19], true, nil
}
