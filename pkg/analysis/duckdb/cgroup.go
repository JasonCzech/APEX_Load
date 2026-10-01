package duckdb

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Native allocations do not honor GOMEMLIMIT. Read container usage as well as
// process RSS so a controller cannot exhaust a smaller container on a large host.
func cgroupHeadroom(root string) error {
	for _, pair := range [][2]string{{"memory.max", "memory.current"}, {"memory/memory.limit_in_bytes", "memory/memory.usage_in_bytes"}} {
		data, err := os.ReadFile(filepath.Join(root, pair[0]))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.New("cannot read container memory limit")
		}
		raw := strings.TrimSpace(string(data))
		if raw == "max" {
			continue
		}
		limit, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || limit == 0 {
			return errors.New("invalid container memory limit")
		}
		if limit >= 1<<60 {
			continue
		}
		data, err = os.ReadFile(filepath.Join(root, pair[1]))
		if err != nil {
			return errors.New("cannot read container memory usage")
		}
		used, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			return errors.New("invalid container memory usage")
		}
		if used >= limit || limit-used <= max(uint64(512<<20), limit/10) {
			return errors.New("analytics cancelled to preserve container memory headroom")
		}
	}
	return nil
}
