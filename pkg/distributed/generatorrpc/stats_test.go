package generatorrpc

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestGeneratorStatsProtoRoundTripIncludesEnhancedResources(t *testing.T) {
	stats := GeneratorStats{
		CompletedIterations:               5,
		ActiveVUsers:                      12,
		CPUPercent:                        44.5,
		CPUFreePct:                        55.5,
		MemoryBytes:                       256 * 1024 * 1024,
		MemFreeMB:                         4096,
		NetSentKbps:                       128.5,
		NetRecvKbps:                       256.25,
		DiskReadKbps:                      64.75,
		DiskWriteKbps:                     32.5,
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
		ContainerCPUPeriodsDelta:          10,
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
		CPUFreePct:                        55.5,
		MemProcessMb:                      256,
		MemFreeMb:                         4096,
		NetSentKbps:                       128.5,
		NetRecvKbps:                       256.25,
		DiskReadKbps:                      64.75,
		DiskWriteKbps:                     32.5,
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
		ContainerCPUPeriodsDelta:          10,
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
	if !reflect.DeepEqual(mapped, resources) {
		t.Fatalf("mapped resources = %#v, want %#v", mapped, resources)
	}
}

func TestMergeGeneratorStatsPreservesPodResourcesAndIO(t *testing.T) {
	left := GeneratorStats{
		ActiveVUsers:             4,
		CompletedIterations:      10,
		CPUPercent:               25,
		CPUFreePct:               75,
		MemFreeMB:                2048,
		NetSentKbps:              100,
		NetRecvKbps:              200,
		DiskReadKbps:             50,
		DiskWriteKbps:            20,
		ContainerCPUPeriodsDelta: 5,
	}
	right := GeneratorStats{
		ActiveVUsers:             6,
		CompletedIterations:      15,
		CPUPercent:               30,
		CPUFreePct:               70,
		MemFreeMB:                2048,
		NetSentKbps:              150,
		NetRecvKbps:              180,
		DiskReadKbps:             40,
		DiskWriteKbps:            60,
		ContainerCPUPeriodsDelta: 8,
	}

	merged := mergeGeneratorStats(left, right)
	if merged.ActiveVUsers != 10 || merged.CompletedIterations != 25 {
		t.Fatalf("merged counts = %#v, want 10 vusers and 25 iterations", merged)
	}
	if merged.CPUFreePct != 75 || merged.MemFreeMB != 2048 ||
		merged.NetSentKbps != 150 || merged.NetRecvKbps != 200 ||
		merged.DiskReadKbps != 50 || merged.DiskWriteKbps != 60 ||
		merged.ContainerCPUPeriodsDelta != 8 {
		t.Fatalf("merged stats = %#v, want pod resource gauges/rates preserved", merged)
	}
}

func TestArchiveRecorderResourceSamplePreservesAllResourceFields(t *testing.T) {
	var buf bytes.Buffer
	recorder := &archiveRecorder{
		manifest:        archiveManifest{TestID: "test-1", TestName: "Smoke"},
		resourceEncoder: json.NewEncoder(&buf),
	}
	ts := time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC)
	resources := &engine.SystemResources{
		CPUProcessPct:               35.5,
		CPUFreePct:                  64.5,
		MemProcessMb:                128,
		MemFreeMb:                   3072,
		NetSentKbps:                 512.5,
		NetRecvKbps:                 1024.25,
		DiskReadKbps:                88.5,
		DiskWriteKbps:               44.25,
		Load1:                       1.5,
		Load5:                       1.25,
		Load15:                      0.75,
		EphemeralPortUtilizationPct: 12.5,
		DiskIOTimePct:               18.5,
		DiskWeightedIOTimePct:       29.5,
		DiskInProgress:              3,
		ContainerCPUPeriodsDelta:    14,
	}

	roundTripResources := resourcesFromGeneratorStats(generatorStatsFromProto(toProtoStats(generatorStatsFromResources(resources, 9))))
	if err := recorder.RecordResourceSample(ts, 9, roundTripResources); err != nil {
		t.Fatalf("RecordResourceSample returned error: %v", err)
	}

	var sample archiveResourceSample
	if err := json.Unmarshal(buf.Bytes(), &sample); err != nil {
		t.Fatalf("decode archiveResourceSample: %v", err)
	}
	if sample.CPUFreePct != 64.5 || sample.MemFreeMB != 3072 ||
		sample.NetSentKbps != 512.5 || sample.NetRecvKbps != 1024.25 ||
		sample.DiskReadKbps != 88.5 || sample.DiskWriteKbps != 44.25 ||
		sample.Load1 != 1.5 || sample.Load5 != 1.25 || sample.Load15 != 0.75 ||
		sample.EphemeralPortUtilizationPct != 12.5 ||
		sample.DiskWeightedIOTimePct != 29.5 || sample.DiskInProgress != 3 ||
		sample.ContainerCPUPeriodsDelta != 14 {
		t.Fatalf("archiveResourceSample = %#v, want all resource fields preserved", sample)
	}

	parquetRows := toParquetResources([]archiveResourceSample{sample})
	if len(parquetRows) != 1 {
		t.Fatalf("len(parquetRows) = %d, want 1", len(parquetRows))
	}
	row := parquetRows[0]
	if row.CPUFreePct != 64.5 || row.MemFreeMB != 3072 ||
		row.NetSentKbps != 512.5 || row.NetRecvKbps != 1024.25 ||
		row.DiskReadKbps != 88.5 || row.DiskWriteKbps != 44.25 ||
		row.Load1 != 1.5 || row.Load5 != 1.25 || row.Load15 != 0.75 ||
		row.EphemeralPortUtilizationPct != 12.5 ||
		row.DiskWeightedIOTimePct != 29.5 || row.DiskInProgress != 3 {
		t.Fatalf("parquetResourceSample = %#v, want all resource fields preserved", row)
	}
}
