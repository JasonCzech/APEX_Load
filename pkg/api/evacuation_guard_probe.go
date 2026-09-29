//go:build apexload_fault_probe

package api

import (
	"context"
	"log/slog"
	"os"
	"time"
)

const evacuationGuardProbeEnv = "APEXLOAD_FAULT_HOLD_AFTER_EVACUATION_GUARD"
const evacuationReplacementCommitProbeEnv = "APEXLOAD_FAULT_HOLD_AFTER_REPLACEMENT_COMMIT"

// This hook exists only in a deliberately tagged fault-qualification build. It
// pauses after the version-2 guard is committed, leaving time for an operator
// to terminate the leader before any replacement assignment is staged.
func waitAfterEvacuationGuard(ctx context.Context) {
	waitAfterEvacuationFaultProbe(ctx, evacuationGuardProbeEnv, "fault probe holding after durable evacuation guard")
}

// The replacement is already committed and the source positively fenced here;
// holding before topology completion exercises successor promotion without
// replaying any generator assignment or commit command.
func waitAfterEvacuationReplacementCommit(ctx context.Context) {
	waitAfterEvacuationFaultProbe(ctx, evacuationReplacementCommitProbeEnv, "fault probe holding after acknowledged replacement commit")
}

func waitAfterEvacuationFaultProbe(ctx context.Context, env, message string) {
	hold, err := time.ParseDuration(os.Getenv(env))
	if err != nil || hold <= 0 || hold > 2*time.Minute {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	slog.Warn(message, "hold", hold)
	timer := time.NewTimer(hold)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
