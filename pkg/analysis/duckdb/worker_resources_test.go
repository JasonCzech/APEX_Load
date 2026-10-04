package duckdb

import (
	"math"
	"testing"
)

func TestWorkerDiskHeadroomSignedBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		free, reserve int64
		want          bool
	}{
		{"exact", (512 << 20) + 12, 12, true},
		{"short", (512 << 20) + 11, 12, false},
		{"negative disk", -1, 12, false},
		{"negative quota", math.MaxInt64, -1, false},
		{"overflow", math.MaxInt64, math.MaxInt64, false},
		{"maximum", math.MaxInt64, math.MaxInt64 - (512 << 20), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workerDiskHeadroom(tc.free, tc.reserve); got != tc.want {
				t.Fatalf("headroom=%v want %v", got, tc.want)
			}
		})
	}
}

func TestWorkerPipelineDiskGeometryIncludesOnlyDeclaredReservationAndSnapshot(t *testing.T) {
	limits := workerLimits{InputBytes: 12, OutputBytes: 24, SpillBytes: 48}
	got, err := workerPipelineDiskBytes(limits, 96)
	if err != nil || got != workerExecutableMaxBytes+12+24+48+96 {
		t.Fatal("nested worker geometry omitted or expanded", got, err)
	}
	for _, tc := range []struct {
		limits  workerLimits
		staging int64
	}{
		{limits, -1},
		{workerLimits{InputBytes: -1}, 0},
		{workerLimits{SpillBytes: math.MaxInt64}, 0},
		{limits, math.MaxInt64},
	} {
		if _, err := workerPipelineDiskBytes(tc.limits, tc.staging); err == nil {
			t.Fatal("invalid or overflow geometry admitted", tc)
		}
	}
}
