package engine

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/v3/cpu"
)

func TestCollectCgroupResourcesV2(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "cpu.max"), "200000 100000\n")
	writeTestFile(t, filepath.Join(root, "cpu.stat"), "usage_usec 1500000\nnr_throttled 7\nthrottled_usec 250000\n")
	writeTestFile(t, filepath.Join(root, "memory.current"), "536870912\n")
	writeTestFile(t, filepath.Join(root, "memory.max"), "1073741824\n")
	writeTestFile(t, filepath.Join(root, "memory.events"), "low 0\nhigh 2\nmax 3\noom 1\noom_kill 1\n")

	snapshot := collectCgroupResources(root)

	if snapshot.CPULimitCores != 2 {
		t.Fatalf("cpu limit = %v, want 2", snapshot.CPULimitCores)
	}
	if snapshot.CPUUsageNanos != 1_500_000_000 {
		t.Fatalf("cpu usage ns = %d, want 1500000000", snapshot.CPUUsageNanos)
	}
	if snapshot.CPUThrottledPeriods != 7 {
		t.Fatalf("throttled periods = %d, want 7", snapshot.CPUThrottledPeriods)
	}
	if snapshot.CPUThrottledNanos != 250_000_000 {
		t.Fatalf("throttled ns = %d, want 250000000", snapshot.CPUThrottledNanos)
	}
	if snapshot.MemoryWorkingSetBytes != 536_870_912 || snapshot.MemoryLimitBytes != 1_073_741_824 {
		t.Fatalf("memory snapshot = %#v, want working set 512MiB limit 1GiB", snapshot)
	}
	if snapshot.MemoryFailCount != 5 || snapshot.OOMKillCount != 1 {
		t.Fatalf("memory pressure = fail %d oom %d, want fail 5 oom 1", snapshot.MemoryFailCount, snapshot.OOMKillCount)
	}
}

func TestCollectCgroupResourcesV1(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "cpu", "cpu.cfs_quota_us"), "50000\n")
	writeTestFile(t, filepath.Join(root, "cpu", "cpu.cfs_period_us"), "100000\n")
	writeTestFile(t, filepath.Join(root, "cpu", "cpu.stat"), "nr_periods 10\nnr_throttled 4\nthrottled_time 9000000\n")
	writeTestFile(t, filepath.Join(root, "cpuacct", "cpuacct.usage"), "123456789\n")
	writeTestFile(t, filepath.Join(root, "memory", "memory.usage_in_bytes"), "268435456\n")
	writeTestFile(t, filepath.Join(root, "memory", "memory.limit_in_bytes"), "536870912\n")
	writeTestFile(t, filepath.Join(root, "memory", "memory.failcnt"), "6\n")

	snapshot := collectCgroupResources(root)

	if snapshot.CPULimitCores != 0.5 {
		t.Fatalf("cpu limit = %v, want 0.5", snapshot.CPULimitCores)
	}
	if snapshot.CPUUsageNanos != 123_456_789 {
		t.Fatalf("cpu usage ns = %d, want 123456789", snapshot.CPUUsageNanos)
	}
	if snapshot.CPUThrottledPeriods != 4 || snapshot.CPUThrottledNanos != 9_000_000 {
		t.Fatalf("throttle snapshot = %#v, want periods 4 ns 9000000", snapshot)
	}
	if snapshot.MemoryWorkingSetBytes != 268_435_456 || snapshot.MemoryLimitBytes != 536_870_912 || snapshot.MemoryFailCount != 6 {
		t.Fatalf("memory snapshot = %#v, want v1 memory fields", snapshot)
	}
}

func TestTCPDiagnosticsParsing(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "net", "snmp"), "Ip: Forwarding DefaultTTL\nIp: 2 64\nTcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs\nTcp: 1 200 120000 -1 10 4 0 0 2 100 120 11\n")
	writeTestFile(t, filepath.Join(root, "sys", "net", "ipv4", "ip_local_port_range"), "32768 32770\n")
	writeTestFile(t, filepath.Join(root, "net", "tcp"), "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n   0: 0100007F:8000 00000000:0000 01 00000000:00000000 00:00000000 00000000   100        0 1\n   1: 0100007F:8001 00000000:0000 06 00000000:00000000 00:00000000 00000000   100        0 2\n")
	writeTestFile(t, filepath.Join(root, "net", "tcp6"), "  sl  local_address                         rem_address                           st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n   0: 00000000000000000000000000000000:8002 00000000000000000000000000000000:0000 01 00000000:00000000 00:00000000 00000000   100        0 3\n")

	diagnostics := collectTCPDiagnostics(root)

	if diagnostics.Retransmits != 11 {
		t.Fatalf("retransmits = %d, want 11", diagnostics.Retransmits)
	}
	if diagnostics.Established != 2 || diagnostics.TimeWait != 1 {
		t.Fatalf("states = established %d time_wait %d, want 2/1", diagnostics.Established, diagnostics.TimeWait)
	}
	if diagnostics.EphemeralPortUtilizationPct != 100 {
		t.Fatalf("ephemeral utilization = %v, want 100", diagnostics.EphemeralPortUtilizationPct)
	}
}

func TestCPUShapePercent(t *testing.T) {
	user, system, iowait, steal := cpuShapePercent(
		cpu.TimesStat{User: 10, System: 5, Idle: 80, Iowait: 2, Steal: 1},
		cpu.TimesStat{User: 20, System: 10, Idle: 95, Iowait: 5, Steal: 2},
	)

	if !floatClose(user, 10/34.0*100) || !floatClose(system, 5/34.0*100) || !floatClose(iowait, 3/34.0*100) || !floatClose(steal, 1/34.0*100) {
		t.Fatalf("cpu shape = %v/%v/%v/%v, want deltas over total", user, system, iowait, steal)
	}
}

func TestCollectSystemResourcesSafelyContainsDependencyPanic(t *testing.T) {
	resources := collectSystemResourcesSafely(func() *SystemResources {
		panic("malformed host telemetry")
	})
	if resources == nil {
		t.Fatal("panic recovery returned a nil resource snapshot")
	}
}

func TestCollectSystemResourcesSafelyHandlesNilCollectorAndResult(t *testing.T) {
	if resources := collectSystemResourcesSafely(nil); resources == nil {
		t.Fatal("nil collector returned a nil resource snapshot")
	}
	if resources := collectSystemResourcesSafely(func() *SystemResources { return nil }); resources == nil {
		t.Fatal("nil collector result was not replaced with an empty snapshot")
	}
}

func floatClose(left float64, right float64) bool {
	return math.Abs(left-right) < 0.000001
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
