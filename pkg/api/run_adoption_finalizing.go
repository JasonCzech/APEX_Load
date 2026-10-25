package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// A generator removes a finished run's assignment, finalizes its archive and
// only then publishes the terminal outcome that adoption reports. A claim in
// between used to read ALREADY_STOPPED or UNKNOWN_TEST, so every recovery held
// a run that had in fact finished. Generators that know better now flag such
// an answer Finalizing: every execution has ended and the outcome is on its
// way. The successor waits for it, boundedly, with claims of the generation
// it already holds, which can only confirm what the first claim did.
const (
	// defaultRunAdoptionFinalizingWait bounds the wait for one generator's
	// finalizing runs. It stays well inside the Phase A claim budget and the
	// supervision TTL the first claim gave every adopted run.
	defaultRunAdoptionFinalizingWait = 5 * time.Second
	runAdoptionFinalizingPoll        = 100 * time.Millisecond
)

// finalizingWait is the wait for one generator's finalizing runs. It takes at
// most a third of what is left of ctx: Phase A shares that context with every
// candidate's later steps (client retention, the roll-forward's commits, the
// restart's preparation and the capacity transfer), which must not be starved.
func (b *distributedExecutionBackend) finalizingWait(ctx context.Context) time.Duration {
	wait := defaultRunAdoptionFinalizingWait
	if b.runAdoptionFinalizingWait > 0 {
		wait = b.runAdoptionFinalizingWait
	}
	if deadline, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(deadline)/3)
	}
	return wait
}

// awaitFinalizingRunAdoptions re-queries client for every run claim reported
// finalizing, until each reports its published outcome or the wait ends. Only
// finalizing answers are replaced: a same-generation claim of a run the first
// claim refused or drained would read differently, and that first answer is
// what the recoveries judge. An answer still finalizing when the wait ends is
// kept, and every recovery refuses it as it refuses any unproven stop.
func (b *distributedExecutionBackend) awaitFinalizingRunAdoptions(ctx context.Context, client generatorClient, claim generatorrpc.ExecutionEpochResult, adoptions []generatorrpc.RunAdoption, generation uint64, ownerID string, leaseDeadline time.Time) generatorrpc.ExecutionEpochResult {
	deadline := time.Now().Add(b.finalizingWait(ctx))
	for {
		pending := make([]generatorrpc.RunAdoption, 0)
		finalizing := make(map[string]int)
		for index, outcome := range claim.Adoptions {
			if outcome.Finalizing {
				finalizing[outcome.TestID] = index
			}
		}
		for _, adoption := range adoptions {
			if _, ok := finalizing[adoption.TestID]; ok {
				pending = append(pending, adoption)
			}
		}
		if len(pending) == 0 {
			return claim
		}
		if !time.Now().Before(deadline) || !waitForGeneratorEpochKeeper(ctx, runAdoptionFinalizingPoll) {
			for testID := range finalizing {
				slog.Warn("generator still finalizes the run's terminal outcome after the bounded wait", "test_id", testID, "execution_generation", generation)
			}
			return claim
		}
		// A requery that hangs ends with the wait, not with the claim context.
		requeryCtx, cancel := context.WithDeadline(ctx, deadline)
		again, err := claimGeneratorExecutionEpochWithAdoptions(requeryCtx, client, generation, ownerID, leaseDeadline, b.generatorEpochKeeperTransit(), pending)
		cancel()
		if err != nil {
			// The first answers stand; try again until the wait ends.
			continue
		}
		for _, outcome := range again.Adoptions {
			if index, ok := finalizing[outcome.TestID]; ok {
				claim.Adoptions[index] = outcome
				if !outcome.Finalizing {
					slog.Info("generator published the finalized run's outcome", "test_id", outcome.TestID, "outcome", outcome.Outcome.String(),
						"terminal_state", outcome.TerminalState, "terminal_cause", outcome.TerminalCause.String())
				}
			}
		}
	}
}
