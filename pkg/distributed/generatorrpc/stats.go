package generatorrpc

import (
	"runtime"

	"github.com/jasonczech/apexload/pkg/engine"
)

func generatorStatsFromResources(resources *engine.SystemResources, activeVUsers int32) GeneratorStats {
	stats := GeneratorStats{
		ActiveVUsers:   activeVUsers,
		GoroutineCount: int32(runtime.NumGoroutine()),
	}
	if resources == nil {
		return stats
	}
	stats.CPUPercent = resources.CPUProcessPct
	stats.CPUFreePct = resources.CPUFreePct
	stats.MemoryBytes = int64(resources.MemProcessMb * 1024 * 1024)
	stats.MemFreeMB = resources.MemFreeMb
	stats.NetSentKbps = resources.NetSentKbps
	stats.NetRecvKbps = resources.NetRecvKbps
	stats.DiskReadKbps = resources.DiskReadKbps
	stats.DiskWriteKbps = resources.DiskWriteKbps
	stats.GoroutineCount = firstNonZeroInt32(resources.GoroutineCount, stats.GoroutineCount)
	stats.CPUUserPct = resources.CPUUserPct
	stats.CPUSystemPct = resources.CPUSystemPct
	stats.CPUIOWaitPct = resources.CPUIOWaitPct
	stats.CPUStealPct = resources.CPUStealPct
	stats.Load1 = resources.Load1
	stats.Load5 = resources.Load5
	stats.Load15 = resources.Load15
	stats.ThreadCount = resources.ThreadCount
	stats.OpenFDCount = resources.OpenFDCount
	stats.HeapAllocMB = resources.HeapAllocMB
	stats.HeapInUseMB = resources.HeapInUseMB
	stats.GCCountDelta = resources.GCCountDelta
	stats.GCPauseMs = resources.GCPauseMs
	stats.ContainerCPULimitCores = resources.ContainerCPULimitCores
	stats.ContainerCPUUsagePct = resources.ContainerCPUUsagePct
	stats.ContainerCPUThrottledPeriodsDelta = resources.ContainerCPUThrottledPeriodsDelta
	stats.ContainerCPUPeriodsDelta = resources.ContainerCPUPeriodsDelta
	stats.ContainerCPUThrottledSecondsRate = resources.ContainerCPUThrottledSecondsRate
	stats.ContainerMemoryWorkingSetMB = resources.ContainerMemoryWorkingSetMB
	stats.ContainerMemoryLimitMB = resources.ContainerMemoryLimitMB
	stats.ContainerMemoryUsagePct = resources.ContainerMemoryUsagePct
	stats.ContainerMemoryFailCountDelta = resources.ContainerMemoryFailCountDelta
	stats.ContainerOOMKillCountDelta = resources.ContainerOOMKillCountDelta
	stats.NetDropInPerSec = resources.NetDropInPerSec
	stats.NetDropOutPerSec = resources.NetDropOutPerSec
	stats.NetErrorInPerSec = resources.NetErrorInPerSec
	stats.NetErrorOutPerSec = resources.NetErrorOutPerSec
	stats.TCPRetransmitsPerSec = resources.TCPRetransmitsPerSec
	stats.TCPEstablished = int32(resources.TCPEstablished)
	stats.TCPTimeWait = int32(resources.TCPTimeWait)
	stats.EphemeralPortUtilizationPct = resources.EphemeralPortUtilizationPct
	stats.DiskIOTimePct = resources.DiskIOTimePct
	stats.DiskWeightedIOTimePct = resources.DiskWeightedIOTimePct
	stats.DiskInProgress = resources.DiskInProgress
	return stats
}

func resourcesFromGeneratorStats(stats GeneratorStats) *engine.SystemResources {
	return &engine.SystemResources{
		CPUProcessPct:                     stats.CPUPercent,
		CPUFreePct:                        stats.CPUFreePct,
		MemProcessMb:                      float64(stats.MemoryBytes) / (1024 * 1024),
		MemFreeMb:                         stats.MemFreeMB,
		NetSentKbps:                       stats.NetSentKbps,
		NetRecvKbps:                       stats.NetRecvKbps,
		DiskReadKbps:                      stats.DiskReadKbps,
		DiskWriteKbps:                     stats.DiskWriteKbps,
		GoroutineCount:                    stats.GoroutineCount,
		CPUUserPct:                        stats.CPUUserPct,
		CPUSystemPct:                      stats.CPUSystemPct,
		CPUIOWaitPct:                      stats.CPUIOWaitPct,
		CPUStealPct:                       stats.CPUStealPct,
		Load1:                             stats.Load1,
		Load5:                             stats.Load5,
		Load15:                            stats.Load15,
		ThreadCount:                       stats.ThreadCount,
		OpenFDCount:                       stats.OpenFDCount,
		HeapAllocMB:                       stats.HeapAllocMB,
		HeapInUseMB:                       stats.HeapInUseMB,
		GCCountDelta:                      stats.GCCountDelta,
		GCPauseMs:                         stats.GCPauseMs,
		ContainerCPULimitCores:            stats.ContainerCPULimitCores,
		ContainerCPUUsagePct:              stats.ContainerCPUUsagePct,
		ContainerCPUThrottledPeriodsDelta: stats.ContainerCPUThrottledPeriodsDelta,
		ContainerCPUPeriodsDelta:          stats.ContainerCPUPeriodsDelta,
		ContainerCPUThrottledSecondsRate:  stats.ContainerCPUThrottledSecondsRate,
		ContainerMemoryWorkingSetMB:       stats.ContainerMemoryWorkingSetMB,
		ContainerMemoryLimitMB:            stats.ContainerMemoryLimitMB,
		ContainerMemoryUsagePct:           stats.ContainerMemoryUsagePct,
		ContainerMemoryFailCountDelta:     stats.ContainerMemoryFailCountDelta,
		ContainerOOMKillCountDelta:        stats.ContainerOOMKillCountDelta,
		NetDropInPerSec:                   stats.NetDropInPerSec,
		NetDropOutPerSec:                  stats.NetDropOutPerSec,
		NetErrorInPerSec:                  stats.NetErrorInPerSec,
		NetErrorOutPerSec:                 stats.NetErrorOutPerSec,
		TCPRetransmitsPerSec:              stats.TCPRetransmitsPerSec,
		TCPEstablished:                    int(stats.TCPEstablished),
		TCPTimeWait:                       int(stats.TCPTimeWait),
		EphemeralPortUtilizationPct:       stats.EphemeralPortUtilizationPct,
		DiskIOTimePct:                     stats.DiskIOTimePct,
		DiskWeightedIOTimePct:             stats.DiskWeightedIOTimePct,
		DiskInProgress:                    stats.DiskInProgress,
	}
}

func firstNonZeroInt32(values ...int32) int32 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func mergeGeneratorStats(left GeneratorStats, right GeneratorStats) GeneratorStats {
	left.ActiveVUsers += right.ActiveVUsers
	left.CompletedIterations += right.CompletedIterations
	left.CPUPercent = maxFloat64(left.CPUPercent, right.CPUPercent)
	left.CPUFreePct = maxFloat64(left.CPUFreePct, right.CPUFreePct)
	left.MemoryBytes = maxInt64(left.MemoryBytes, right.MemoryBytes)
	left.MemFreeMB = maxFloat64(left.MemFreeMB, right.MemFreeMB)
	left.NetSentKbps = maxFloat64(left.NetSentKbps, right.NetSentKbps)
	left.NetRecvKbps = maxFloat64(left.NetRecvKbps, right.NetRecvKbps)
	left.DiskReadKbps = maxFloat64(left.DiskReadKbps, right.DiskReadKbps)
	left.DiskWriteKbps = maxFloat64(left.DiskWriteKbps, right.DiskWriteKbps)
	left.GoroutineCount = maxInt32(left.GoroutineCount, right.GoroutineCount)
	left.ThreadCount = maxInt32(left.ThreadCount, right.ThreadCount)
	left.OpenFDCount = maxInt32(left.OpenFDCount, right.OpenFDCount)
	left.GCCountDelta = maxUint32(left.GCCountDelta, right.GCCountDelta)
	left.ContainerCPUThrottledPeriodsDelta = maxUint64(left.ContainerCPUThrottledPeriodsDelta, right.ContainerCPUThrottledPeriodsDelta)
	left.ContainerCPUPeriodsDelta = maxUint64(left.ContainerCPUPeriodsDelta, right.ContainerCPUPeriodsDelta)
	left.ContainerMemoryFailCountDelta = maxUint64(left.ContainerMemoryFailCountDelta, right.ContainerMemoryFailCountDelta)
	left.ContainerOOMKillCountDelta = maxUint64(left.ContainerOOMKillCountDelta, right.ContainerOOMKillCountDelta)
	left.DiskInProgress = maxUint64(left.DiskInProgress, right.DiskInProgress)
	left.CPUUserPct = maxFloat64(left.CPUUserPct, right.CPUUserPct)
	left.CPUSystemPct = maxFloat64(left.CPUSystemPct, right.CPUSystemPct)
	left.CPUIOWaitPct = maxFloat64(left.CPUIOWaitPct, right.CPUIOWaitPct)
	left.CPUStealPct = maxFloat64(left.CPUStealPct, right.CPUStealPct)
	left.Load1 = maxFloat64(left.Load1, right.Load1)
	left.Load5 = maxFloat64(left.Load5, right.Load5)
	left.Load15 = maxFloat64(left.Load15, right.Load15)
	left.HeapAllocMB = maxFloat64(left.HeapAllocMB, right.HeapAllocMB)
	left.HeapInUseMB = maxFloat64(left.HeapInUseMB, right.HeapInUseMB)
	left.GCPauseMs = maxFloat64(left.GCPauseMs, right.GCPauseMs)
	left.ContainerCPULimitCores = maxFloat64(left.ContainerCPULimitCores, right.ContainerCPULimitCores)
	left.ContainerCPUUsagePct = maxFloat64(left.ContainerCPUUsagePct, right.ContainerCPUUsagePct)
	left.ContainerCPUThrottledSecondsRate = maxFloat64(left.ContainerCPUThrottledSecondsRate, right.ContainerCPUThrottledSecondsRate)
	left.ContainerMemoryWorkingSetMB = maxFloat64(left.ContainerMemoryWorkingSetMB, right.ContainerMemoryWorkingSetMB)
	left.ContainerMemoryLimitMB = maxFloat64(left.ContainerMemoryLimitMB, right.ContainerMemoryLimitMB)
	left.ContainerMemoryUsagePct = maxFloat64(left.ContainerMemoryUsagePct, right.ContainerMemoryUsagePct)
	left.NetDropInPerSec = maxFloat64(left.NetDropInPerSec, right.NetDropInPerSec)
	left.NetDropOutPerSec = maxFloat64(left.NetDropOutPerSec, right.NetDropOutPerSec)
	left.NetErrorInPerSec = maxFloat64(left.NetErrorInPerSec, right.NetErrorInPerSec)
	left.NetErrorOutPerSec = maxFloat64(left.NetErrorOutPerSec, right.NetErrorOutPerSec)
	left.TCPRetransmitsPerSec = maxFloat64(left.TCPRetransmitsPerSec, right.TCPRetransmitsPerSec)
	left.TCPEstablished = maxInt32(left.TCPEstablished, right.TCPEstablished)
	left.TCPTimeWait = maxInt32(left.TCPTimeWait, right.TCPTimeWait)
	left.EphemeralPortUtilizationPct = maxFloat64(left.EphemeralPortUtilizationPct, right.EphemeralPortUtilizationPct)
	left.DiskIOTimePct = maxFloat64(left.DiskIOTimePct, right.DiskIOTimePct)
	left.DiskWeightedIOTimePct = maxFloat64(left.DiskWeightedIOTimePct, right.DiskWeightedIOTimePct)
	return left
}

func maxInt32(left, right int32) int32 {
	if right > left {
		return right
	}
	return left
}

func maxUint64(left, right uint64) uint64 {
	if right > left {
		return right
	}
	return left
}

func maxUint32(left, right uint32) uint32 {
	if right > left {
		return right
	}
	return left
}

func maxFloat64(left float64, right float64) float64 {
	if right > left {
		return right
	}
	return left
}
