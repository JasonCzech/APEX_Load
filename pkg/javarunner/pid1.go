package javarunner

import (
	"errors"
	"strconv"
	"strings"
)

var ErrPID1Isolation = errors.New("java_supervisor_pid1_isolation_required")

// Check the local kernel posture before a supervisor may start ANY JVM. This
// does not qualify a cluster runtime or establish broker-side termination proof.
func validatePID1Posture(pid, euid int, status []byte) error {
	if pid != 1 || euid <= 0 || len(status) > 64<<10 {
		return ErrPID1Isolation
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(status), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			if _, duplicate := fields[key]; duplicate {
				return ErrPID1Isolation
			}
			fields[key] = strings.TrimSpace(value)
		}
	}
	if fields["NoNewPrivs"] != "1" {
		return ErrPID1Isolation
	}
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		value, err := strconv.ParseUint(fields[key], 16, 64)
		if err != nil || value != 0 {
			return ErrPID1Isolation
		}
	}
	ids := strings.Fields(fields["Uid"])
	if len(ids) != 4 {
		return ErrPID1Isolation
	}
	for _, id := range ids {
		n, err := strconv.Atoi(id)
		if err != nil || n != euid {
			return ErrPID1Isolation
		}
	}
	return nil
}
