//go:build linux

package duckdb

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func workerContainerHeadroom(required int64) error {
	// Fixed kernel files, never an environment/client-selected path.
	return workerCgroupHeadroom("/proc/self/cgroup", "/sys/fs/cgroup", required)
}

// workerCgroupHeadroom resolves the current v2 membership and all visible
// ancestor limits conservatively.
func workerCgroupHeadroom(membershipFile, root string, required int64) error {
	body, err := os.ReadFile(membershipFile)
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
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		return errWorkerResourceLimit
	}
	directory := filepath.Join(root, strings.TrimPrefix(filepath.Clean(member), "/"))
	for {
		if info, err := os.Stat(directory); err != nil || !info.IsDir() {
			return errWorkerResourceLimit
		}
		// A level without the memory controller (always the host's root
		// cgroup) has no memory.max and imposes no limit; host memory is
		// checked separately. Inside a cgroup namespace the visible root is
		// the container's own cgroup, which normally has one.
		limit, limitErr := os.ReadFile(filepath.Join(directory, "memory.max"))
		if errors.Is(limitErr, fs.ErrNotExist) {
			limit, limitErr = []byte("max"), nil
		}
		used, usedErr := os.ReadFile(filepath.Join(directory, "memory.current"))
		if limitErr != nil || (usedErr != nil && strings.TrimSpace(string(limit)) != "max") {
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
