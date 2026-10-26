package procidentity

import (
	"errors"
	"strconv"
	"strings"
)

// ParseLinuxStatStart parses a Linux /proc/<pid>/stat record and returns its
// start time (field 22, clock ticks after boot). A process in the zombie ('Z')
// or dead ('X', 'x') state has already exited: it makes no further system
// calls and has closed every file descriptor, so found is false once a
// well-formed record is verified. Container PID 1 without an init reaps no
// orphans, which leaves an exited writer a zombie indefinitely.
func ParseLinuxStatStart(data []byte) (string, bool, error) {
	// The command name is parenthesized and may hold spaces or ')'; fields
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
	switch fields[0] {
	case "Z", "X", "x":
		return "", false, nil
	}
	return fields[19], true, nil
}
