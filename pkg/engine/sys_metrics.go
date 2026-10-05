package engine

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type ResourceCollector struct {
	proc        *process.Process
	cgroupRoot  string
	procRoot    string
	lastCPU     cpu.TimesStat
	lastProcCPU cpu.TimesStat
	lastProcAt  time.Time
	hasProcCPU  bool
	lastRuntime runtime.MemStats
	lastCgroup  cgroupResourceSnapshot
	lastTCP     tcpDiagnostics
	lastTCPTime time.Time
	lastTCPRate float64
	lastNet     net.IOCountersStat
	lastDisk    disk.IOCountersStat
	lastTime    time.Time
	initialized bool
}

const tcpDiagnosticsInterval = 5 * time.Second

var systemResourceCollectionPanicWarning sync.Once

func NewResourceCollector() *ResourceCollector {
	p, _ := process.NewProcess(int32(os.Getpid()))
	return &ResourceCollector{
		proc:       p,
		cgroupRoot: "/sys/fs/cgroup",
		procRoot:   "/proc",
	}
}

func (rc *ResourceCollector) Collect() *SystemResources {
	return collectSystemResourcesSafely(rc.collect)
}

// collectSystemResourcesSafely is the process boundary around gopsutil. Some
// platform implementations invoke and parse host utilities; malformed or
// unexpectedly restricted output must make optional telemetry unavailable,
// not panic the metrics goroutine and terminate the entire load generator.
func collectSystemResourcesSafely(collect func() *SystemResources) (resources *SystemResources) {
	resources = &SystemResources{}
	defer func() {
		if recover() != nil {
			systemResourceCollectionPanicWarning.Do(func() {
				slog.Warn("system resource collection panicked; publishing empty snapshots")
			})
			resources = &SystemResources{}
		}
	}()
	if collect == nil {
		return resources
	}
	if collected := collect(); collected != nil {
		resources = collected
	}
	return resources
}

func (rc *ResourceCollector) collect() *SystemResources {
	now := time.Now()
	res := &SystemResources{}
	dt := 0.0
	if rc.initialized {
		dt = now.Sub(rc.lastTime).Seconds()
	}

	// CPU
	if percentages, err := cpu.Percent(0, false); err == nil && len(percentages) > 0 {
		res.CPUFreePct = 100.0 - percentages[0]
	}
	if times, err := cpu.Times(false); err == nil && len(times) > 0 {
		current := times[0]
		if rc.initialized {
			user, system, iowait, steal := cpuShapePercent(rc.lastCPU, current)
			res.CPUUserPct = user
			res.CPUSystemPct = system
			res.CPUIOWaitPct = iowait
			res.CPUStealPct = steal
		}
		rc.lastCPU = current
	}
	if avg, err := load.Avg(); err == nil && avg != nil {
		res.Load1 = avg.Load1
		res.Load5 = avg.Load5
		res.Load15 = avg.Load15
	}
	processCPUSeconds := 0.0
	processCPUWallSeconds := 0.0
	processCPUAvailable := false
	if rc.proc != nil {
		if current, err := rc.proc.Times(); err == nil && current != nil {
			processSampleAt := time.Now()
			if rc.hasProcCPU {
				processCPUSeconds, processCPUWallSeconds, processCPUAvailable = processCPUSampleDelta(
					rc.lastProcCPU,
					*current,
					rc.lastProcAt,
					processSampleAt,
				)
			}
			rc.lastProcCPU = *current
			rc.lastProcAt = processSampleAt
			rc.hasProcCPU = true
		}
		if threads, err := rc.proc.NumThreads(); err == nil {
			res.ThreadCount = threads
		}
		if fds, err := rc.proc.NumFDs(); err == nil {
			res.OpenFDCount = fds
		}
	}
	res.GoroutineCount = int32(runtime.NumGoroutine())
	var runtimeStats runtime.MemStats
	runtime.ReadMemStats(&runtimeStats)
	res.HeapAllocMB = bytesToMB(runtimeStats.HeapAlloc)
	res.HeapInUseMB = bytesToMB(runtimeStats.HeapInuse)
	if rc.initialized {
		if runtimeStats.NumGC >= rc.lastRuntime.NumGC {
			res.GCCountDelta = runtimeStats.NumGC - rc.lastRuntime.NumGC
		}
		if runtimeStats.PauseTotalNs >= rc.lastRuntime.PauseTotalNs {
			res.GCPauseMs = float64(runtimeStats.PauseTotalNs-rc.lastRuntime.PauseTotalNs) / float64(time.Millisecond)
		}
	}
	rc.lastRuntime = runtimeStats

	// Memory
	if v, err := mem.VirtualMemory(); err == nil {
		res.MemFreeMb = float64(v.Available) / 1024 / 1024
	}
	if rc.proc != nil {
		if m, err := rc.proc.MemoryInfo(); err == nil {
			res.MemProcessMb = float64(m.RSS) / 1024 / 1024
		}
	}

	// Network
	if io, err := net.IOCounters(false); err == nil && len(io) > 0 {
		current := io[0]
		if rc.initialized && dt > 0 {
			res.NetSentKbps = safeRateUint64(current.BytesSent, rc.lastNet.BytesSent, dt) / 1024
			res.NetRecvKbps = safeRateUint64(current.BytesRecv, rc.lastNet.BytesRecv, dt) / 1024
			res.NetDropInPerSec = safeRateUint64(current.Dropin, rc.lastNet.Dropin, dt)
			res.NetDropOutPerSec = safeRateUint64(current.Dropout, rc.lastNet.Dropout, dt)
			res.NetErrorInPerSec = safeRateUint64(current.Errin, rc.lastNet.Errin, dt)
			res.NetErrorOutPerSec = safeRateUint64(current.Errout, rc.lastNet.Errout, dt)
		}
		rc.lastNet = current
	}
	if !rc.initialized || rc.lastTCPTime.IsZero() || now.Sub(rc.lastTCPTime) >= tcpDiagnosticsInterval {
		tcp := collectTCPDiagnostics(rc.procRoot)
		if !rc.lastTCPTime.IsZero() {
			tcpDT := now.Sub(rc.lastTCPTime).Seconds()
			if tcpDT > 0 {
				rc.lastTCPRate = safeRateUint64(tcp.Retransmits, rc.lastTCP.Retransmits, tcpDT)
			}
		}
		rc.lastTCP = tcp
		rc.lastTCPTime = now
	}
	res.TCPEstablished = rc.lastTCP.Established
	res.TCPTimeWait = rc.lastTCP.TimeWait
	res.EphemeralPortUtilizationPct = rc.lastTCP.EphemeralPortUtilizationPct
	res.TCPRetransmitsPerSec = rc.lastTCPRate

	// Disk
	if io, err := disk.IOCounters(); err == nil {
		// Aggregate all disks
		var current disk.IOCountersStat
		for _, d := range io {
			current.ReadBytes += d.ReadBytes
			current.WriteBytes += d.WriteBytes
			current.IopsInProgress += d.IopsInProgress
			current.IoTime += d.IoTime
			current.WeightedIO += d.WeightedIO
		}
		res.DiskInProgress = current.IopsInProgress
		if rc.initialized && dt > 0 {
			res.DiskReadKbps = safeRateUint64(current.ReadBytes, rc.lastDisk.ReadBytes, dt) / 1024
			res.DiskWriteKbps = safeRateUint64(current.WriteBytes, rc.lastDisk.WriteBytes, dt) / 1024
			res.DiskIOTimePct = counterMillisecondsToPct(current.IoTime, rc.lastDisk.IoTime, dt)
			res.DiskWeightedIOTimePct = counterMillisecondsToPct(current.WeightedIO, rc.lastDisk.WeightedIO, dt)
		}
		rc.lastDisk = current
	}
	cgroup := collectCgroupResources(rc.cgroupRoot)
	res.ContainerCPULimitCores = cgroup.CPULimitCores
	if processCPUAvailable {
		// GOMAXPROCS is the process's actual Go execution capacity and, on
		// container-aware runtimes, already reflects the default cgroup quota.
		// NumCPU reports host-visible CPUs and can understate utilization when an
		// operator intentionally constrains GOMAXPROCS below that value.
		capacityCores := effectiveCPUCapacity(cgroup.CPULimitCores, runtime.GOMAXPROCS(0))
		res.CPUProcessPct = processCPUPercent(processCPUSeconds, processCPUWallSeconds, capacityCores)
	}
	res.ContainerMemoryWorkingSetMB = bytesToMBUint64(cgroup.MemoryWorkingSetBytes)
	res.ContainerMemoryLimitMB = bytesToMBUint64(cgroup.MemoryLimitBytes)
	if cgroup.MemoryLimitBytes > 0 && cgroup.MemoryWorkingSetBytes > 0 {
		res.ContainerMemoryUsagePct = float64(cgroup.MemoryWorkingSetBytes) / float64(cgroup.MemoryLimitBytes) * 100
	}
	if rc.initialized && dt > 0 {
		if cgroup.CPULimitCores > 0 {
			res.ContainerCPUUsagePct = safeRateUint64(cgroup.CPUUsageNanos, rc.lastCgroup.CPUUsageNanos, dt) / 1_000_000_000 / cgroup.CPULimitCores * 100
		}
		res.ContainerCPUThrottledPeriodsDelta = safeDeltaUint64(cgroup.CPUThrottledPeriods, rc.lastCgroup.CPUThrottledPeriods)
		res.ContainerCPUPeriodsDelta = safeDeltaUint64(cgroup.CPUPeriods, rc.lastCgroup.CPUPeriods)
		res.ContainerCPUThrottledSecondsRate = safeRateUint64(cgroup.CPUThrottledNanos, rc.lastCgroup.CPUThrottledNanos, dt) / 1_000_000_000
		res.ContainerMemoryFailCountDelta = safeDeltaUint64(cgroup.MemoryFailCount, rc.lastCgroup.MemoryFailCount)
		res.ContainerOOMKillCountDelta = safeDeltaUint64(cgroup.OOMKillCount, rc.lastCgroup.OOMKillCount)
	}
	rc.lastCgroup = cgroup

	rc.lastTime = now
	rc.initialized = true
	return res
}

// processCPUPercent converts the process's cumulative CPU-time delta into a
// current, capacity-normalized utilization percentage. Process.Times is a
// cumulative counter; Process.CPUPercent instead averages over the entire OS
// process lifetime and can leave a later test reporting an earlier test's load.
// Normalize against the lesser of a container quota and the process's Go
// execution capacity, so 100 means all CPU actually available to the generator.
func processCPUPercent(cpuSeconds, wallSeconds, capacityCores float64) float64 {
	if cpuSeconds <= 0 || wallSeconds <= 0 || capacityCores <= 0 {
		return 0
	}
	return cpuSeconds / wallSeconds / capacityCores * 100
}

// processCPUSampleDelta keeps the cumulative CPU counter and wall interval on
// the same pair of successful samples. In particular, a transient Times error
// must not make the next multi-interval CPU delta use only one collection
// interval as its denominator.
func processCPUSampleDelta(previous, current cpu.TimesStat, previousAt, currentAt time.Time) (cpuSeconds, wallSeconds float64, ok bool) {
	// Process.Times populates User and System with CPU execution time. On Linux
	// it also populates Iowait from delayacct_blkio_ticks, which is time blocked
	// on I/O rather than time executing on a CPU and must not inflate this gauge.
	cpuSeconds = processCPUTime(current) - processCPUTime(previous)
	wallSeconds = currentAt.Sub(previousAt).Seconds()
	return cpuSeconds, wallSeconds, cpuSeconds >= 0 && wallSeconds > 0
}

func processCPUTime(sample cpu.TimesStat) float64 {
	return sample.User + sample.System
}

func effectiveCPUCapacity(cgroupLimitCores float64, runtimeCPUs int) float64 {
	availableCores := float64(runtimeCPUs)
	if availableCores <= 0 {
		return max(cgroupLimitCores, 0)
	}
	if cgroupLimitCores > 0 && cgroupLimitCores < availableCores {
		return cgroupLimitCores
	}
	return availableCores
}

type cgroupResourceSnapshot struct {
	CPULimitCores         float64
	CPUUsageNanos         uint64
	CPUThrottledPeriods   uint64
	CPUPeriods            uint64
	CPUThrottledNanos     uint64
	MemoryWorkingSetBytes uint64
	MemoryLimitBytes      uint64
	MemoryFailCount       uint64
	OOMKillCount          uint64
}

type tcpDiagnostics struct {
	Retransmits                 uint64
	Established                 int
	TimeWait                    int
	EphemeralPortUtilizationPct float64
}

func cpuShapePercent(previous cpu.TimesStat, current cpu.TimesStat) (float64, float64, float64, float64) {
	totalDelta := current.Total() - previous.Total()
	if totalDelta <= 0 {
		return 0, 0, 0, 0
	}
	return pctDelta(current.User, previous.User, totalDelta),
		pctDelta(current.System, previous.System, totalDelta),
		pctDelta(current.Iowait, previous.Iowait, totalDelta),
		pctDelta(current.Steal, previous.Steal, totalDelta)
}

func pctDelta(current float64, previous float64, totalDelta float64) float64 {
	if current < previous || totalDelta <= 0 {
		return 0
	}
	return (current - previous) / totalDelta * 100
}

func collectCgroupResources(root string) cgroupResourceSnapshot {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "/sys/fs/cgroup"
	}
	snapshot := cgroupResourceSnapshot{}
	if limit, ok := readCgroupV2CPULimit(root); ok {
		snapshot.CPULimitCores = limit
	} else if limit, ok := readCgroupV1CPULimit(root); ok {
		snapshot.CPULimitCores = limit
	}
	if stats, ok := readKeyedUintFile(filepath.Join(root, "cpu.stat")); ok {
		snapshot.CPUUsageNanos = stats["usage_usec"] * 1000
		snapshot.CPUThrottledPeriods = stats["nr_throttled"]
		snapshot.CPUPeriods = stats["nr_periods"]
		snapshot.CPUThrottledNanos = stats["throttled_usec"] * 1000
	}
	if usage, ok := readUintFile(filepath.Join(root, "cpuacct", "cpuacct.usage")); ok && snapshot.CPUUsageNanos == 0 {
		snapshot.CPUUsageNanos = usage
	}
	if stats, ok := readKeyedUintFile(filepath.Join(root, "cpu", "cpu.stat")); ok {
		if snapshot.CPUThrottledPeriods == 0 {
			snapshot.CPUThrottledPeriods = stats["nr_throttled"]
		}
		if snapshot.CPUPeriods == 0 {
			snapshot.CPUPeriods = stats["nr_periods"]
		}
		if snapshot.CPUThrottledNanos == 0 {
			snapshot.CPUThrottledNanos = stats["throttled_time"]
		}
	}
	if current, ok := readUintFile(filepath.Join(root, "memory.current")); ok {
		snapshot.MemoryWorkingSetBytes = current
	} else if current, ok := readUintFile(filepath.Join(root, "memory", "memory.usage_in_bytes")); ok {
		snapshot.MemoryWorkingSetBytes = current
	}
	if limit, ok := readCgroupMemoryLimit(filepath.Join(root, "memory.max")); ok {
		snapshot.MemoryLimitBytes = limit
	} else if limit, ok := readCgroupMemoryLimit(filepath.Join(root, "memory", "memory.limit_in_bytes")); ok {
		snapshot.MemoryLimitBytes = limit
	}
	if events, ok := readKeyedUintFile(filepath.Join(root, "memory.events")); ok {
		snapshot.MemoryFailCount = events["max"] + events["high"]
		snapshot.OOMKillCount = events["oom_kill"]
	}
	if fail, ok := readUintFile(filepath.Join(root, "memory", "memory.failcnt")); ok && snapshot.MemoryFailCount == 0 {
		snapshot.MemoryFailCount = fail
	}
	return snapshot
}

func readCgroupV2CPULimit(root string) (float64, bool) {
	content, ok := readTrimmedFile(filepath.Join(root, "cpu.max"))
	if !ok {
		return 0, false
	}
	fields := strings.Fields(content)
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	return parseCPUQuota(fields[0], fields[1])
}

func readCgroupV1CPULimit(root string) (float64, bool) {
	quota, quotaOK := readTrimmedFile(filepath.Join(root, "cpu", "cpu.cfs_quota_us"))
	period, periodOK := readTrimmedFile(filepath.Join(root, "cpu", "cpu.cfs_period_us"))
	if !quotaOK || !periodOK {
		return 0, false
	}
	return parseCPUQuota(quota, period)
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
	if limit <= 0 {
		return 0, false
	}
	return limit, true
}

func readCgroupMemoryLimit(path string) (uint64, bool) {
	content, ok := readTrimmedFile(path)
	if !ok || content == "max" {
		return 0, false
	}
	value, err := strconv.ParseUint(content, 10, 64)
	if err != nil || value == 0 || value > 1<<60 {
		return 0, false
	}
	return value, true
}

func readKeyedUintFile(path string) (map[string]uint64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		values[fields[0]] = value
	}
	return values, scanner.Err() == nil
}

func collectTCPDiagnostics(procRoot string) tcpDiagnostics {
	procRoot = strings.TrimSpace(procRoot)
	if procRoot == "" {
		procRoot = "/proc"
	}
	diagnostics := tcpDiagnostics{Retransmits: readTCPRetransmits(filepath.Join(procRoot, "net", "snmp"))}
	start, end := readEphemeralPortRange(filepath.Join(procRoot, "sys", "net", "ipv4", "ip_local_port_range"))
	inRange := 0
	totalRange := 0
	if start > 0 && end >= start {
		totalRange = end - start + 1
	}
	for _, path := range []string{filepath.Join(procRoot, "net", "tcp"), filepath.Join(procRoot, "net", "tcp6")} {
		established, timeWait, ports := readTCPConnectionStates(path, start, end)
		diagnostics.Established += established
		diagnostics.TimeWait += timeWait
		inRange += ports
	}
	if totalRange > 0 && inRange > 0 {
		diagnostics.EphemeralPortUtilizationPct = float64(inRange) / float64(totalRange) * 100
	}
	return diagnostics
}

func readTCPRetransmits(path string) uint64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	var header []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "Tcp:" {
			continue
		}
		if header == nil {
			header = fields[1:]
			continue
		}
		values := fields[1:]
		for i, name := range header {
			if name == "RetransSegs" && i < len(values) {
				value, _ := strconv.ParseUint(values[i], 10, 64)
				return value
			}
		}
		header = nil
	}
	return 0
}

func readEphemeralPortRange(path string) (int, int) {
	content, ok := readTrimmedFile(path)
	if !ok {
		return 0, 0
	}
	fields := strings.Fields(content)
	if len(fields) != 2 {
		return 0, 0
	}
	start, startErr := strconv.Atoi(fields[0])
	end, endErr := strconv.Atoi(fields[1])
	if startErr != nil || endErr != nil || start <= 0 || end < start {
		return 0, 0
	}
	return start, end
}

func readTCPConnectionStates(path string, rangeStart int, rangeEnd int) (int, int, int) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, 0
	}
	defer file.Close()
	established := 0
	timeWait := 0
	portsInRange := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] == "sl" {
			continue
		}
		switch fields[3] {
		case "01":
			established++
		case "06":
			timeWait++
		}
		if rangeStart > 0 && rangeEnd >= rangeStart {
			port := parseTCPStateLocalPort(fields[1])
			if port >= rangeStart && port <= rangeEnd {
				portsInRange++
			}
		}
	}
	return established, timeWait, portsInRange
}

func parseTCPStateLocalPort(value string) int {
	colon := strings.LastIndex(value, ":")
	if colon < 0 || colon == len(value)-1 {
		return 0
	}
	port, err := strconv.ParseInt(value[colon+1:], 16, 32)
	if err != nil {
		return 0
	}
	return int(port)
}

func readUintFile(path string) (uint64, bool) {
	content, ok := readTrimmedFile(path)
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseUint(content, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func readTrimmedFile(path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(content))
	return value, value != ""
}

func safeDeltaUint64(current uint64, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

func safeRateUint64(current uint64, previous uint64, seconds float64) float64 {
	if seconds <= 0 || current < previous {
		return 0
	}
	return float64(current-previous) / seconds
}

func counterMillisecondsToPct(current uint64, previous uint64, seconds float64) float64 {
	if seconds <= 0 || current < previous {
		return 0
	}
	return float64(current-previous) / (seconds * 1000) * 100
}

func bytesToMB(value uint64) float64 {
	return float64(value) / 1024 / 1024
}

func bytesToMBUint64(value uint64) float64 {
	return float64(value) / 1024 / 1024
}
