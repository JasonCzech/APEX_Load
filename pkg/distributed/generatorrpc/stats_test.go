package generatorrpc

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestGeneratorStatsProtoRoundTripIncludesEnhancedResources(t *testing.T) {
	stats := GeneratorStats{
		ActiveVUsers:                      12,
		CPUPercent:                        44.5,
		MemoryBytes:                       256 * 1024 * 1024,
		GoroutineCount:                    33,
		CPUUserPct:                        20,
		CPUSystemPct:                      10,
		CPUIOWaitPct:                      3,
		CPUStealPct:                       1,
		Load1:                             1.2,
		Load5:                             1.1,
		Load15:                            0.9,
		ThreadCount:                       8,
		OpenFDCount:                       42,
		HeapAllocMB:                       64,
		HeapInUseMB:                       80,
		GCCountDelta:                      2,
		GCPauseMs:                         14,
		ContainerCPULimitCores:            2,
		ContainerCPUUsagePct:              90,
		ContainerCPUThrottledPeriodsDelta: 3,
		ContainerCPUThrottledSecondsRate:  0.4,
		ContainerMemoryWorkingSetMB:       512,
		ContainerMemoryLimitMB:            1024,
		ContainerMemoryUsagePct:           50,
		ContainerMemoryFailCountDelta:     1,
		ContainerOOMKillCountDelta:        1,
		NetDropInPerSec:                   0.1,
		NetDropOutPerSec:                  0.2,
		NetErrorInPerSec:                  0.3,
		NetErrorOutPerSec:                 0.4,
		TCPRetransmitsPerSec:              0.5,
		TCPEstablished:                    11,
		TCPTimeWait:                       9,
		EphemeralPortUtilizationPct:       7,
		DiskIOTimePct:                     31,
		DiskWeightedIOTimePct:             45,
		DiskInProgress:                    2,
	}

	roundTrip := generatorStatsFromProto(toProtoStats(stats))

	if !reflect.DeepEqual(roundTrip, stats) {
		t.Fatalf("round trip stats = %#v, want %#v", roundTrip, stats)
	}
}

func TestGeneratorStatsResourcesRoundTripIncludesEnhancedResources(t *testing.T) {
	resources := &engine.SystemResources{
		CPUProcessPct:                     44.5,
		MemProcessMb:                      256,
		GoroutineCount:                    33,
		CPUUserPct:                        20,
		CPUSystemPct:                      10,
		CPUIOWaitPct:                      3,
		CPUStealPct:                       1,
		Load1:                             1.2,
		Load5:                             1.1,
		Load15:                            0.9,
		ThreadCount:                       8,
		OpenFDCount:                       42,
		HeapAllocMB:                       64,
		HeapInUseMB:                       80,
		GCCountDelta:                      2,
		GCPauseMs:                         14,
		ContainerCPULimitCores:            2,
		ContainerCPUUsagePct:              90,
		ContainerCPUThrottledPeriodsDelta: 3,
		ContainerCPUThrottledSecondsRate:  0.4,
		ContainerMemoryWorkingSetMB:       512,
		ContainerMemoryLimitMB:            1024,
		ContainerMemoryUsagePct:           50,
		ContainerMemoryFailCountDelta:     1,
		ContainerOOMKillCountDelta:        1,
		NetDropInPerSec:                   0.1,
		NetDropOutPerSec:                  0.2,
		NetErrorInPerSec:                  0.3,
		NetErrorOutPerSec:                 0.4,
		TCPRetransmitsPerSec:              0.5,
		TCPEstablished:                    11,
		TCPTimeWait:                       9,
		EphemeralPortUtilizationPct:       7,
		DiskIOTimePct:                     31,
		DiskWeightedIOTimePct:             45,
		DiskInProgress:                    2,
	}

	stats := generatorStatsFromResources(resources, 12)
	mapped := resourcesFromGeneratorStats(stats)

	if stats.ActiveVUsers != 12 || stats.MemoryBytes != 256*1024*1024 {
		t.Fatalf("basic stats = %#v, want active vusers and memory mapped", stats)
	}
	if mapped.ContainerCPUThrottledSecondsRate != resources.ContainerCPUThrottledSecondsRate ||
		mapped.ContainerMemoryUsagePct != resources.ContainerMemoryUsagePct ||
		mapped.TCPRetransmitsPerSec != resources.TCPRetransmitsPerSec ||
		mapped.DiskWeightedIOTimePct != resources.DiskWeightedIOTimePct {
		t.Fatalf("mapped resources = %#v, want enhanced fields from %#v", mapped, resources)
	}
}
