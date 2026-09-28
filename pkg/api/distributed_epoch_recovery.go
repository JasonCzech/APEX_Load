package api

import (
	"context"
	"log/slog"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type idleExecutionLeaseReleaseStore interface {
	ReleaseSchedulerLeaseTermIfTestsIdle(name, owner string, generation uint64) (bool, error)
}

// recoverExpiredGeneratorEpoch advances through ordinary leadership acquisition;
// it never revives a generator's expired generation. Both local start staging
// and the durable start transaction must agree that the fleet is idle before
// releasing the old term. Active tests continue unaffected on usable targets.
func (tr *TestRunner) recoverExpiredGeneratorEpoch(ctx context.Context, generation uint64, owner string, renewalErr error) bool {
	if tr == nil || tr.leadership == nil || !tr.durableExecutionFence || ctx.Err() != nil {
		return false
	}
	if !anyErrorInTree(renewalErr, func(err error) bool {
		rejection, ok := err.(*generatorExecutionEpochRejection)
		return ok && rejection.generation == generation && rejection.currentGeneration == generation &&
			rejection.message == generatorrpc.ExecutionGenerationExpiredMessage
	}) {
		return false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.shuttingDown || ctx.Err() != nil {
		return false
	}
	for _, test := range tr.tests {
		if test == nil {
			continue
		}
		// Failed/aborted outcomes become durable before remote cleanup and
		// archive transfer finish. These handles still own terminal evidence.
		if test.remoteClient != nil || len(test.remoteClientsByTarget) != 0 ||
			len(test.remoteArchiveClientsByTarget) != 0 || test.remoteLifecycleCancel != nil {
			return false
		}
		switch test.Status {
		case "DRAFT", "PENDING", "QUEUED", "SHAKEOUT_REVIEW", "COMPLETE", "FAILED", "ABORTED":
		default:
			// Includes the process-local STARTING reservation while its durable
			// admission transaction is running outside tr.mu.
			return false
		}
	}
	// Keep the reference registry closed across the release decision, so a new
	// local collection/publication cannot start in the snapshot-to-release gap.
	// Remote terminal cleanup above bridges the earlier pre-collection phase.
	tr.archiveCommitMu.Lock()
	defer tr.archiveCommitMu.Unlock()
	if len(tr.archiveCommitGates) != 0 {
		return false
	}
	// The exact-term transaction holds tr.mu through database I/O. Publish that
	// wait to the bounded liveness and panic-recovery bypasses.
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	return tr.leadership.releaseIdleExecutionTerm(ctx, generation, owner)
}

func (l *executionLeadership) releaseIdleExecutionTerm(ctx context.Context, generation uint64, owner string) bool {
	store, ok := l.store.(idleExecutionLeaseReleaseStore)
	if !ok {
		return false
	}
	// Serialize against renewal: an in-flight tick must not renew a term after
	// the idle transaction has expired it and before local admission closes.
	if err := lockMutexWithContext(ctx, &l.tickMu); err != nil {
		return false
	}
	defer l.tickMu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	l.mu.Lock()
	current := l.leader.Load() && l.pendingLoss == 0 && !l.reconciling.Load() &&
		l.durableGeneration == generation && l.leaseOwnerID == owner && !l.leaseExpiredLocked(l.now().UTC())
	l.mu.Unlock()
	if !current {
		return false
	}
	released, err := store.ReleaseSchedulerLeaseTermIfTestsIdle(executionLeaseName, owner, generation)
	if !released && err == nil {
		return false
	}
	// A commit error may be an ambiguous success. In that case stop using the
	// term too; reacquisition still goes through the durable lease authority.
	l.mu.Lock()
	if l.durableGeneration == generation && l.leaseOwnerID == owner {
		l.transitionToFollowerLocked()
	}
	l.mu.Unlock()
	if err != nil {
		slog.Warn("idle execution generation release failed; stepping down", "generation", generation, "error", err)
	} else {
		slog.Info("idle execution generation released to recover expired generators", "generation", generation)
	}
	return true
}
