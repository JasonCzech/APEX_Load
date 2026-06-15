package api

// distributed_inventory.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

func (b *distributedExecutionBackend) SystemInventory(ctx context.Context) []GeneratorInventoryEntry {
	specs := b.configuredTargetSpecs()
	entries := make([]GeneratorInventoryEntry, 0, len(specs))
	for _, target := range specs {
		entry := GeneratorInventoryEntry{
			Target:              target.Address,
			GeneratorID:         target.Address,
			Region:              target.Region,
			Status:              "unreachable",
			Version:             generatorSpecVersion(target),
			SupportedProtocols:  generatorSpecProtocols(target),
			DrainState:          b.generatorDrainState(target.Address),
			CapacityState:       generatorCapacityState(0, 0, false),
			HealthScore:         generatorHealthScore(false, "unavailable"),
			CompatibilityState:  generatorCompatibilityState(target),
			CompatibilityIssues: generatorCompatibilityIssues(target),
		}

		probeCtx, cancel := context.WithTimeout(ctx, b.timeout)
		client, err := b.dial(probeCtx, target.Address)
		if err != nil {
			cancel()
			entries = append(entries, entry)
			continue
		}

		snapshot := b.probeGeneratorLoad(probeCtx, target.Address, client)
		_ = client.Close()
		cancel()

		if snapshot.GeneratorID != "" {
			entry.GeneratorID = snapshot.GeneratorID
		}
		if snapshot.HeartbeatOK {
			effectiveSpec := generatorEffectiveSpec(target, snapshot)
			entry.Version = generatorSpecVersion(effectiveSpec)
			entry.SupportedProtocols = generatorSpecProtocols(effectiveSpec)
			entry.CompatibleFeatures = snapshot.CompatibleFeatures
			if len(entry.CompatibleFeatures) == 0 {
				entry.CompatibleFeatures = append([]string(nil), entry.SupportedProtocols...)
			}
			entry.CompatibilityState = generatorCompatibilityState(effectiveSpec)
			entry.CompatibilityIssues = generatorCompatibilityIssues(effectiveSpec)
			entry.Status = "connected"
			entry.AvailableCapacity = snapshot.AvailableCapacity
			applyGeneratorStatsToInventory(&entry, snapshot.Stats)
			entry.CPULimitCores = snapshot.CPULimitCores
			entry.MemoryLimitBytes = snapshot.MemoryLimitBytes
			entry.MaxVUsers = snapshot.MaxVUsers
			entry.CapacityState = generatorCapacityState(entry.AvailableCapacity, entry.MaxVUsers, true)
			entry.HealthScore = generatorHealthScore(true, entry.CapacityState)
		}

		entries = append(entries, entry)
	}
	return entries
}

func applyGeneratorStatsToInventory(entry *GeneratorInventoryEntry, stats generatorrpc.GeneratorStats) {
	if entry == nil {
		return
	}
	entry.ActiveVUsers = stats.ActiveVUsers
	entry.CPUPercent = stats.CPUPercent
	entry.MemoryBytes = stats.MemoryBytes
	entry.GoroutineCount = stats.GoroutineCount
	entry.CPUUserPct = stats.CPUUserPct
	entry.CPUSystemPct = stats.CPUSystemPct
	entry.CPUIOWaitPct = stats.CPUIOWaitPct
	entry.CPUStealPct = stats.CPUStealPct
	entry.ThreadCount = stats.ThreadCount
	entry.OpenFDCount = stats.OpenFDCount
	entry.HeapAllocMB = stats.HeapAllocMB
	entry.HeapInUseMB = stats.HeapInUseMB
	entry.GCCountDelta = stats.GCCountDelta
	entry.GCPauseMs = stats.GCPauseMs
	entry.ContainerCPUUsagePct = stats.ContainerCPUUsagePct
	entry.ContainerCPUThrottledPeriodsDelta = stats.ContainerCPUThrottledPeriodsDelta
	entry.ContainerCPUThrottledSecondsRate = stats.ContainerCPUThrottledSecondsRate
	entry.ContainerMemoryWorkingSetMB = stats.ContainerMemoryWorkingSetMB
	entry.ContainerMemoryUsagePct = stats.ContainerMemoryUsagePct
	entry.ContainerMemoryFailCountDelta = stats.ContainerMemoryFailCountDelta
	entry.ContainerOOMKillCountDelta = stats.ContainerOOMKillCountDelta
	entry.NetDropInPerSec = stats.NetDropInPerSec
	entry.NetDropOutPerSec = stats.NetDropOutPerSec
	entry.NetErrorInPerSec = stats.NetErrorInPerSec
	entry.NetErrorOutPerSec = stats.NetErrorOutPerSec
	entry.TCPRetransmitsPerSec = stats.TCPRetransmitsPerSec
	entry.TCPEstablished = int(stats.TCPEstablished)
	entry.TCPTimeWait = int(stats.TCPTimeWait)
	entry.DiskIOTimePct = stats.DiskIOTimePct
}

func resourcesFromGeneratorStats(stats generatorrpc.GeneratorStats) *engine.SystemResources {
	return &engine.SystemResources{
		CPUProcessPct:                     stats.CPUPercent,
		MemProcessMb:                      float64(stats.MemoryBytes) / (1024 * 1024),
		CPUUserPct:                        stats.CPUUserPct,
		CPUSystemPct:                      stats.CPUSystemPct,
		CPUIOWaitPct:                      stats.CPUIOWaitPct,
		CPUStealPct:                       stats.CPUStealPct,
		Load1:                             stats.Load1,
		Load5:                             stats.Load5,
		Load15:                            stats.Load15,
		ThreadCount:                       stats.ThreadCount,
		OpenFDCount:                       stats.OpenFDCount,
		GoroutineCount:                    stats.GoroutineCount,
		HeapAllocMB:                       stats.HeapAllocMB,
		HeapInUseMB:                       stats.HeapInUseMB,
		GCCountDelta:                      stats.GCCountDelta,
		GCPauseMs:                         stats.GCPauseMs,
		ContainerCPULimitCores:            stats.ContainerCPULimitCores,
		ContainerCPUUsagePct:              stats.ContainerCPUUsagePct,
		ContainerCPUThrottledPeriodsDelta: stats.ContainerCPUThrottledPeriodsDelta,
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

func mergeDistributedGeneratorStats(left generatorrpc.GeneratorStats, right generatorrpc.GeneratorStats) generatorrpc.GeneratorStats {
	left.ActiveVUsers += right.ActiveVUsers
	left.CPUPercent += right.CPUPercent
	left.MemoryBytes += right.MemoryBytes
	left.GoroutineCount += right.GoroutineCount
	left.ThreadCount += right.ThreadCount
	left.OpenFDCount += right.OpenFDCount
	left.GCCountDelta += right.GCCountDelta
	left.GCPauseMs += right.GCPauseMs
	left.ContainerCPULimitCores += right.ContainerCPULimitCores
	left.ContainerCPUThrottledPeriodsDelta += right.ContainerCPUThrottledPeriodsDelta
	left.ContainerCPUThrottledSecondsRate += right.ContainerCPUThrottledSecondsRate
	left.ContainerMemoryWorkingSetMB += right.ContainerMemoryWorkingSetMB
	left.ContainerMemoryLimitMB += right.ContainerMemoryLimitMB
	left.ContainerMemoryFailCountDelta += right.ContainerMemoryFailCountDelta
	left.ContainerOOMKillCountDelta += right.ContainerOOMKillCountDelta
	left.NetDropInPerSec += right.NetDropInPerSec
	left.NetDropOutPerSec += right.NetDropOutPerSec
	left.NetErrorInPerSec += right.NetErrorInPerSec
	left.NetErrorOutPerSec += right.NetErrorOutPerSec
	left.TCPRetransmitsPerSec += right.TCPRetransmitsPerSec
	left.TCPEstablished += right.TCPEstablished
	left.TCPTimeWait += right.TCPTimeWait
	left.DiskInProgress += right.DiskInProgress
	left.CPUUserPct = maxFloat(left.CPUUserPct, right.CPUUserPct)
	left.CPUSystemPct = maxFloat(left.CPUSystemPct, right.CPUSystemPct)
	left.CPUIOWaitPct = maxFloat(left.CPUIOWaitPct, right.CPUIOWaitPct)
	left.CPUStealPct = maxFloat(left.CPUStealPct, right.CPUStealPct)
	left.Load1 = maxFloat(left.Load1, right.Load1)
	left.Load5 = maxFloat(left.Load5, right.Load5)
	left.Load15 = maxFloat(left.Load15, right.Load15)
	left.HeapAllocMB += right.HeapAllocMB
	left.HeapInUseMB += right.HeapInUseMB
	left.ContainerCPUUsagePct = maxFloat(left.ContainerCPUUsagePct, right.ContainerCPUUsagePct)
	left.ContainerMemoryUsagePct = maxFloat(left.ContainerMemoryUsagePct, right.ContainerMemoryUsagePct)
	left.EphemeralPortUtilizationPct = maxFloat(left.EphemeralPortUtilizationPct, right.EphemeralPortUtilizationPct)
	left.DiskIOTimePct = maxFloat(left.DiskIOTimePct, right.DiskIOTimePct)
	left.DiskWeightedIOTimePct = maxFloat(left.DiskWeightedIOTimePct, right.DiskWeightedIOTimePct)
	return left
}

func (b *distributedExecutionBackend) buildGeneratorResourceSnapshots(test *ManagedTest, batches map[string]generatorrpc.MetricsBatch) map[string]*GeneratorResourceSnapshot {
	if len(batches) == 0 {
		return nil
	}
	out := make(map[string]*GeneratorResourceSnapshot, len(batches))
	for target, batch := range batches {
		resources := resourcesFromGeneratorStats(batch.Stats)
		p95, p99, errorPct, tps := summarizeGeneratorBatchTransactions(batch.Transactions)
		state, reasons := classifyGeneratorImpact(resources, p95, errorPct)
		id := firstNonEmpty(batch.GeneratorID, target)
		timestamp := time.UnixMilli(batch.TimestampUnixMs)
		if batch.TimestampUnixMs == 0 {
			timestamp = time.Now()
		}
		out[id] = &GeneratorResourceSnapshot{
			GeneratorID:     id,
			Target:          target,
			Region:          b.regionForTarget(target),
			ActiveVUsers:    batch.Stats.ActiveVUsers,
			TPS:             tps,
			ErrorPct:        errorPct,
			P95Ms:           p95,
			P99Ms:           p99,
			SaturationState: state,
			ImpactReasons:   reasons,
			Resources:       resources,
			Timestamp:       timestamp,
		}
	}
	if len(out) == 0 || test == nil {
		return out
	}
	return out
}

func (b *distributedExecutionBackend) regionForTarget(target string) string {
	for _, spec := range b.configuredTargetSpecs() {
		if spec.Address == target {
			return spec.Region
		}
	}
	return ""
}

func classifyGeneratorImpact(resources *engine.SystemResources, p95Ms float64, errorPct float64) (string, []string) {
	if resources == nil {
		return "unknown", nil
	}
	reasons := make([]string, 0, 6)
	appendReason := func(condition bool, reason string) {
		if condition {
			reasons = append(reasons, reason)
		}
	}
	appendReason(resources.ContainerCPUThrottledPeriodsDelta > 0 || resources.ContainerCPUThrottledSecondsRate > 0, "CPU throttling")
	appendReason(resources.ContainerCPUUsagePct >= 95 || resources.CPUProcessPct >= 95, "CPU saturation")
	appendReason(resources.ContainerMemoryUsagePct >= 90, "Memory near limit")
	appendReason(resources.ContainerOOMKillCountDelta > 0, "OOM kill observed")
	appendReason(resources.GCPauseMs >= 100, "GC pause pressure")
	appendReason(resources.NetDropInPerSec > 0 || resources.NetDropOutPerSec > 0 || resources.NetErrorInPerSec > 0 || resources.NetErrorOutPerSec > 0 || resources.TCPRetransmitsPerSec > 0, "Network loss/retransmits")
	appendReason(resources.DiskIOTimePct >= 90 || resources.DiskWeightedIOTimePct >= 90, "Disk I/O pressure")
	appendReason(resources.CPUIOWaitPct >= 20, "CPU I/O wait")
	appendReason(resources.CPUStealPct >= 10, "CPU steal")
	if len(reasons) == 0 {
		return "clear", nil
	}
	if errorPct > 0 || p95Ms >= slowTransactionDiagnosticThresholdMs {
		return "danger", reasons
	}
	return "warning", reasons
}

func maxFloat(left float64, right float64) float64 {
	if right > left {
		return right
	}
	return left
}
