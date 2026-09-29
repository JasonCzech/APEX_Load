//go:build apexload_fault_probe

package api

import (
	"context"
	"testing"
	"time"
)

func TestFaultProbeHoldsOnlyForBoundedDurations(t *testing.T) {
	for _, tc := range []struct {
		name string
		hold string
		wait bool
	}{
		{"missing", "", false},
		{"invalid", "oops", false},
		{"zero", "0s", false},
		{"over bound", "121s", false},
		{"bounded", "40ms", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(evacuationGuardProbeEnv, tc.hold)
			started := time.Now()
			waitAfterEvacuationGuard(context.Background())
			elapsed := time.Since(started)
			if tc.wait && elapsed < 30*time.Millisecond {
				t.Fatalf("fault hold ended early: %s", elapsed)
			}
			if !tc.wait && elapsed > 500*time.Millisecond {
				t.Fatalf("invalid fault hold blocked: %s", elapsed)
			}
		})
	}
}

func TestFaultProbeReleasesOnCancellation(t *testing.T) {
	t.Setenv(evacuationGuardProbeEnv, "2m")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	waitAfterEvacuationGuard(ctx)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("canceled fault hold blocked for %s", elapsed)
	}
}
