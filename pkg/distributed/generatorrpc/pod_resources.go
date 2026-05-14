package generatorrpc

import (
	"math"
	"os"
	"strconv"
	"strings"
)

type podResourceCapacity struct {
	CPULimitCores    float64
	MemoryLimitBytes int64
	MaxVUsers        int32
}

func detectPodResourceCapacity(maxVUsers int32) podResourceCapacity {
	return podResourceCapacity{
		CPULimitCores:    detectCPULimitCores(),
		MemoryLimitBytes: detectMemoryLimitBytes(),
		MaxVUsers:        maxVUsers,
	}
}

func detectCPULimitCores() float64 {
	if content, ok := readTrimmedFile("/sys/fs/cgroup/cpu.max"); ok {
		if limit, ok := parseCPUMax(content); ok {
			return limit
		}
	}
	quota, quotaOK := readTrimmedFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	period, periodOK := readTrimmedFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if quotaOK && periodOK {
		if limit, ok := parseCPUQuota(quota, period); ok {
			return limit
		}
	}
	return 0
}

func detectMemoryLimitBytes() int64 {
	if content, ok := readTrimmedFile("/sys/fs/cgroup/memory.max"); ok {
		if limit, ok := parseMemoryLimit(content); ok {
			return limit
		}
	}
	if content, ok := readTrimmedFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); ok {
		if limit, ok := parseMemoryLimit(content); ok {
			return limit
		}
	}
	return 0
}

func readTrimmedFile(path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func parseCPUMax(content string) (float64, bool) {
	fields := strings.Fields(content)
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	return parseCPUQuota(fields[0], fields[1])
}

func parseCPUQuota(quotaValue string, periodValue string) (float64, bool) {
	quota, err := strconv.ParseFloat(strings.TrimSpace(quotaValue), 64)
	if err != nil || quota <= 0 {
		return 0, false
	}
	period, err := strconv.ParseFloat(strings.TrimSpace(periodValue), 64)
	if err != nil || period <= 0 {
		return 0, false
	}
	limit := quota / period
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit <= 0 {
		return 0, false
	}
	return limit, true
}

func parseMemoryLimit(content string) (int64, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || trimmed == "max" {
		return 0, false
	}
	limit, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || limit <= 0 {
		return 0, false
	}
	if limit > 1<<50 {
		return 0, false
	}
	return limit, true
}
