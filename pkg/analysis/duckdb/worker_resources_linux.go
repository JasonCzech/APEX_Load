//go:build linux

package duckdb

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func workerContainerHeadroom(required int64) error {
	// Fixed kernel files, never an environment/client-selected path. Resolve
	// the current v2 membership and all visible ancestor limits conservatively.
	body, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return errWorkerResourceLimit
	}
	var member string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "0::") {
			member = strings.TrimPrefix(line, "0::")
		}
	}
	if member == "" {
		return errWorkerResourceLimit
	} // unsupported v1 needs explicit profile
	root := "/sys/fs/cgroup"
	directory := filepath.Join(root, strings.TrimPrefix(filepath.Clean(member), "/"))
	for {
		limit, limitErr := os.ReadFile(filepath.Join(directory, "memory.max"))
		used, usedErr := os.ReadFile(filepath.Join(directory, "memory.current"))
		if limitErr != nil || usedErr != nil {
			return errWorkerResourceLimit
		}
		if strings.TrimSpace(string(limit)) != "max" {
			ceiling, e1 := strconv.ParseInt(strings.TrimSpace(string(limit)), 10, 64)
			current, e2 := strconv.ParseInt(strings.TrimSpace(string(used)), 10, 64)
			if e1 != nil || e2 != nil || current < 0 || ceiling < current || ceiling-current < required {
				return errWorkerResourceLimit
			}
		}
		if directory == root {
			break
		}
		next := filepath.Dir(directory)
		if next == directory || !strings.HasPrefix(next, root) {
			return errWorkerResourceLimit
		}
		directory = next
	}
	return nil
}
