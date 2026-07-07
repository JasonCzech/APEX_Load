package runtimetuning

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

const defaultCgroupRoot = "/sys/fs/cgroup"

// Settings describes Go runtime limits inferred from cgroups. Zero values mean
// the corresponding runtime/env setting was already configured or no usable
// cgroup limit was present.
type Settings struct {
	GOMAXPROCS       int
	GOMAXPROCSSource string
	GOMEMLIMIT       int64
	GOMEMLIMITSource string
}

// ApplyFromCgroup configures GOMAXPROCS and GOMEMLIMIT from cgroup CPU/memory
// limits when the matching environment variables are not already set.
func ApplyFromCgroup(getenv func(string) string) Settings {
	settings := ResolveFromCgroup(getenv, defaultCgroupRoot)
	if settings.GOMAXPROCS > 0 {
		runtime.GOMAXPROCS(settings.GOMAXPROCS)
	}
	if settings.GOMEMLIMIT > 0 {
		debug.SetMemoryLimit(settings.GOMEMLIMIT)
	}
	return settings
}

func ResolveFromCgroup(getenv func(string) string, root string) Settings {
	if getenv == nil {
		getenv = os.Getenv
	}
	var settings Settings
	if strings.TrimSpace(getenv("GOMAXPROCS")) == "" {
		settings.GOMAXPROCS, settings.GOMAXPROCSSource = readCgroupCPUQuota(root)
	}
	if strings.TrimSpace(getenv("GOMEMLIMIT")) == "" {
		settings.GOMEMLIMIT, settings.GOMEMLIMITSource = readCgroupMemoryLimit(root)
	}
	return settings
}

func readCgroupCPUQuota(root string) (int, string) {
	if raw, source, ok := readTrimmed(filepath.Join(root, "cpu.max")); ok {
		fields := strings.Fields(raw)
		if len(fields) >= 2 && fields[0] != "max" {
			quota, quotaErr := strconv.ParseInt(fields[0], 10, 64)
			period, periodErr := strconv.ParseInt(fields[1], 10, 64)
			if quotaErr == nil && periodErr == nil {
				if procs := gomaxprocsFromQuota(quota, period); procs > 0 {
					return procs, source
				}
			}
		}
	}
	quota, _, quotaOK := readInt(filepath.Join(root, "cpu", "cpu.cfs_quota_us"))
	period, source, periodOK := readInt(filepath.Join(root, "cpu", "cpu.cfs_period_us"))
	if quotaOK && periodOK {
		if procs := gomaxprocsFromQuota(quota, period); procs > 0 {
			return procs, source
		}
	}
	return 0, ""
}

func gomaxprocsFromQuota(quota int64, period int64) int {
	if quota <= 0 || period <= 0 {
		return 0
	}
	procs := int((quota + period - 1) / period)
	if procs < 1 {
		return 1
	}
	return procs
}

func readCgroupMemoryLimit(root string) (int64, string) {
	if raw, source, ok := readTrimmed(filepath.Join(root, "memory.max")); ok && raw != "max" {
		if limit, err := strconv.ParseInt(raw, 10, 64); err == nil {
			if runtimeLimit := gomemlimitFromCgroup(limit); runtimeLimit > 0 {
				return runtimeLimit, source
			}
		}
	}
	if limit, source, ok := readInt(filepath.Join(root, "memory", "memory.limit_in_bytes")); ok {
		if runtimeLimit := gomemlimitFromCgroup(limit); runtimeLimit > 0 {
			return runtimeLimit, source
		}
	}
	return 0, ""
}

func gomemlimitFromCgroup(limit int64) int64 {
	if limit <= 0 || limit >= 1<<60 {
		return 0
	}
	return limit * 90 / 100
}

func readInt(path string) (int64, string, bool) {
	raw, source, ok := readTrimmed(path)
	if !ok {
		return 0, "", false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return value, source, true
}

func readTrimmed(path string) (string, string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	return strings.TrimSpace(string(data)), path, true
}
