//go:build apexload_fault_probe

package api

import (
	"context"
	"log/slog"
	"os"
	"time"
)

const evacuationGuardProbeEnv = "APEXLOAD_FAULT_HOLD_AFTER_EVACUATION_GUARD"

// This hook exists only in a deliberately tagged fault-qualification build. It
// pauses after the version-2 guard is committed, leaving time for an operator
// to terminate the leader before any replacement assignment is staged.
func waitAfterEvacuationGuard(ctx context.Context) {
	hold, err := time.ParseDuration(os.Getenv(evacuationGuardProbeEnv))
	if err != nil || hold <= 0 || hold > 2*time.Minute {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	slog.Warn("fault probe holding after durable evacuation guard", "hold", hold)
	timer := time.NewTimer(hold)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
