//go:build linux

package browser

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func readHARHost() (harHost, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return harHost{}, err
	}
	namespace, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return harHost{}, err
	}
	host := harHost{boot: strings.TrimSpace(string(boot)), namespace: namespace}
	// A machine-id alone is not host-unique: container images and cloned VMs
	// share one. Paired with the host name (distinct per pod and per cloned
	// VM), an earlier boot proves a process gone only on the same host; a
	// renamed host simply proves nothing.
	hostname, _ := os.Hostname()
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if machine, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(machine)) != "" && strings.TrimSpace(hostname) != "" {
			host.machine = strings.TrimSpace(string(machine)) + "@" + strings.TrimSpace(hostname)
			break
		}
	}
	if host.boot == "" || host.namespace == "" {
		return harHost{}, errors.New("boot or PID namespace identity is empty")
	}
	return host, nil
}

// harProcessStart reads a process's start time (clock ticks after boot) from
// /proc; found is false when no such process exists.
func harProcessStart(pid int) (string, bool, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
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
