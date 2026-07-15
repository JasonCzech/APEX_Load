package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

const (
	// A controller may discover many generator pods at once. Keep epoch
	// maintenance bounded so a bad discovery result or an unreachable fleet
	// cannot create one long-lived dial goroutine per target on every renewal.
	maxGeneratorEpochKeeperWorkers   = 16
	maxGeneratorEpochKeeperTransit   = 2 * time.Second
	maxGeneratorEpochKeeperInterval  = 5 * time.Second
	generatorEpochKeeperRetryDelay   = 250 * time.Millisecond
	generatorEpochKeeperFollowerPoll = 100 * time.Millisecond
)

type executionEpochGeneratorClient interface {
	ClaimExecutionEpoch(context.Context, uint64, string, time.Duration) (generatorrpc.ExecutionEpochResult, error)
}

type executionGenerationStopClient interface {
	StopTestAtGeneration(context.Context, string, bool, uint64) (generatorrpc.StopResult, error)
}

type executionGenerationMetricsClient interface {
	OpenMetricsStreamForGeneration(context.Context, uint64) (*generatorrpc.MetricsStream, error)
}

type executionGenerationSetupMetricsClient interface {
	OpenMetricsStreamWithSetupTimeoutForGeneration(context.Context, time.Duration, uint64) (*generatorrpc.MetricsStream, error)
}

type executionTestMetricsClient interface {
	OpenMetricsStreamForTestGeneration(context.Context, string, uint64) (*generatorrpc.MetricsStream, error)
}

type executionTestSetupMetricsClient interface {
	OpenMetricsStreamWithSetupTimeoutForTestGeneration(context.Context, time.Duration, string, uint64) (*generatorrpc.MetricsStream, error)
}

func (tr *TestRunner) generatorExecutionClaim() (uint64, string, time.Time, error) {
	if tr == nil || tr.leadership == nil {
		return 0, "", time.Time{}, nil
	}
	generation, leaseOwnerID, deadline, ok := tr.leadership.executionGeneratorClaim()
	if !ok || generation == 0 || leaseOwnerID == "" || deadline.IsZero() || !time.Now().Before(deadline) {
		return 0, "", time.Time{}, errNotExecutionLeader
	}
	return generation, leaseOwnerID, deadline, nil
}

func claimGeneratorExecutionEpoch(ctx context.Context, client generatorClient, generation uint64, leaseOwnerID string, leaseDeadline time.Time, maxTransit time.Duration) error {
	if generation == 0 {
		return nil
	}
	if leaseOwnerID == "" {
		return fmt.Errorf("execution lease owner is unavailable")
	}
	if ctx == nil {
		return fmt.Errorf("execution claim context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("execution claim context: %w", err)
	}
	claiming, ok := client.(executionEpochGeneratorClient)
	if !ok {
		return fmt.Errorf("generator client does not support execution fencing")
	}
	if leaseDeadline.IsZero() {
		return fmt.Errorf("execution lease deadline is unavailable")
	}
	if maxTransit <= 0 {
		return fmt.Errorf("execution claim transit bound must be positive")
	}
	now := time.Now()
	remaining := leaseDeadline.Sub(now)
	if remaining < 3*time.Millisecond {
		return fmt.Errorf("execution lease has insufficient time remaining for a safe claim")
	}
	transitBudget := min(maxTransit, remaining/3)
	if parentDeadline, ok := ctx.Deadline(); ok {
		parentRemaining := time.Until(parentDeadline)
		if parentRemaining <= 0 {
			return fmt.Errorf("execution claim context: %w", context.DeadlineExceeded)
		}
		transitBudget = min(transitBudget, parentRemaining)
	}
	if transitBudget < time.Millisecond {
		return fmt.Errorf("execution claim has less than one millisecond of transit budget")
	}
	// The generator starts its relative TTL only when the request arrives. Remove
	// the entire maximum transit window so even a request delivered at its RPC
	// deadline cannot keep load alive past the controller's local lease deadline.
	safeTTL := remaining - transitBudget
	if safeTTL < time.Millisecond {
		return fmt.Errorf("execution lease has less than one millisecond of safe generator ttl")
	}
	claimCtx, cancel := context.WithTimeout(ctx, transitBudget)
	defer cancel()
	result, err := claiming.ClaimExecutionEpoch(claimCtx, generation, leaseOwnerID, safeTTL)
	if err != nil {
		return err
	}
	if err := claimCtx.Err(); err != nil {
		return fmt.Errorf("execution claim exceeded its transit bound: %w", err)
	}
	if !time.Now().Before(leaseDeadline) {
		return fmt.Errorf("execution lease expired while claiming generator")
	}
	if !result.Accepted || result.CurrentGeneration != generation {
		return fmt.Errorf("generator rejected execution generation %d (current %d): %s", generation, result.CurrentGeneration, result.ErrorMessage)
	}
	return nil
}

func stopGeneratorTest(ctx context.Context, client generatorClient, testID string, graceful bool, generation uint64) (generatorrpc.StopResult, error) {
	if generation == 0 {
		return client.StopTest(ctx, testID, graceful)
	}
	stopping, ok := client.(executionGenerationStopClient)
	if !ok {
		return generatorrpc.StopResult{}, fmt.Errorf("generator client does not support execution-fenced stop")
	}
	return stopping.StopTestAtGeneration(ctx, testID, graceful, generation)
}

// maintainGeneratorExecutionEpoch keeps the controller's durable execution
// generation live on every configured generator for the entire leadership
// epoch, including periods with no running tests. Generators deliberately
// refuse to revive an expired generation, so tying renewal only to a test run
// would make the next run fail after an idle period longer than the generator
// claim TTL.
//
// Connections are scoped to one leadership epoch and reused across renewal
// cycles. They are all closed synchronously on lease loss. Target snapshots are
// refreshed every cycle so DNS discovery and autoscaling changes are observed.
func (b *distributedExecutionBackend) maintainGeneratorExecutionEpoch(ctx context.Context, tr *TestRunner) {
	if b == nil || tr == nil || tr.leadership == nil {
		return
	}
	for {
		generation, leaseOwnerID, _, err := tr.generatorExecutionClaim()
		if err != nil {
			if !waitForGeneratorEpochKeeper(ctx, generatorEpochKeeperFollowerPoll) {
				return
			}
			continue
		}

		// Capture the leadership-epoch cancellation token, then verify the term
		// again. A loss between the first snapshot and operationContext must not
		// let a stale keeper start claiming generators.
		epochCtx, epochCancel := tr.executionOperationContext(ctx)
		confirmedGeneration, confirmedOwnerID, _, confirmErr := tr.generatorExecutionClaim()
		if confirmErr != nil || confirmedGeneration != generation || confirmedOwnerID != leaseOwnerID || epochCtx.Err() != nil {
			epochCancel()
			if ctx.Err() != nil {
				return
			}
			continue
		}

		clients := make(map[string]generatorClient)
		b.maintainGeneratorExecutionTerm(epochCtx, tr, generation, leaseOwnerID, clients)
		closeGeneratorEpochKeeperClients(clients)
		epochCancel()
		if ctx.Err() != nil {
			return
		}
	}
}

func (b *distributedExecutionBackend) maintainGeneratorExecutionTerm(ctx context.Context, tr *TestRunner, generation uint64, leaseOwnerID string, clients map[string]generatorClient) {
	for {
		cycleStarted := time.Now()
		currentGeneration, currentOwnerID, leaseDeadline, err := tr.generatorExecutionClaim()
		if err != nil || currentGeneration != generation || currentOwnerID != leaseOwnerID || ctx.Err() != nil {
			return
		}

		targets := b.generatorEpochKeeperTargets()
		renewalErr := b.claimConfiguredGeneratorExecutionEpoch(ctx, clients, targets, generation, leaseOwnerID, leaseDeadline)
		remaining := time.Until(leaseDeadline)
		if remaining <= 0 {
			return
		}

		next := remaining / 3
		if renewalErr != nil {
			slog.Warn("generator execution epoch keeper renewal failed", "generation", generation, "error", renewalErr)
			next = remaining / 10
			if next > generatorEpochKeeperRetryDelay {
				next = generatorEpochKeeperRetryDelay
			}
		} else if next > maxGeneratorEpochKeeperInterval {
			next = maxGeneratorEpochKeeperInterval
		}
		// Slow/unreachable targets consumed part of this lease window. Measure
		// the next attempt from cycle start rather than response completion.
		next -= time.Since(cycleStarted)
		if next < time.Millisecond {
			next = time.Millisecond
		}
		if !waitForGeneratorEpochKeeper(ctx, next) {
			return
		}
	}
}

func (b *distributedExecutionBackend) generatorEpochKeeperTargets() []string {
	if b == nil {
		return nil
	}
	// Keep cordoned/draining targets fenced too. If one is later reactivated in
	// the same controller term, its generation must still be live before it can
	// become an assignment candidate.
	unique := make(map[string]struct{})
	for _, spec := range b.configuredTargetSpecs() {
		if spec.Address != "" {
			unique[spec.Address] = struct{}{}
		}
	}
	targets := make([]string, 0, len(unique))
	for target := range unique {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

type generatorEpochKeeperJob struct {
	target string
	client generatorClient
}

type generatorEpochKeeperResult struct {
	target string
	client generatorClient
	err    error
}

// claimConfiguredGeneratorExecutionEpoch performs one bounded renewal pass.
// The clients map is owned by the caller and is updated only after every worker
// exits, which keeps target refresh and leadership-loss teardown race-free.
func (b *distributedExecutionBackend) claimConfiguredGeneratorExecutionEpoch(ctx context.Context, clients map[string]generatorClient, targets []string, generation uint64, leaseOwnerID string, leaseDeadline time.Time) error {
	configured := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		configured[target] = struct{}{}
	}
	for target, client := range clients {
		if _, ok := configured[target]; ok {
			continue
		}
		if client != nil {
			_ = client.Close()
		}
		delete(clients, target)
	}
	if len(targets) == 0 {
		return nil
	}

	cycleCtx, cycleCancel := context.WithDeadline(ctx, leaseDeadline)
	defer cycleCancel()
	jobs := make(chan generatorEpochKeeperJob)
	results := make(chan generatorEpochKeeperResult, len(targets))
	workerCount := min(maxGeneratorEpochKeeperWorkers, len(targets))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobs {
				client := job.client
				var err error
				if client == nil {
					client, err = b.dialGeneratorEpochKeeperClient(cycleCtx, job.target, leaseDeadline)
				}
				if err == nil {
					err = claimGeneratorExecutionEpoch(cycleCtx, client, generation, leaseOwnerID, leaseDeadline, b.generatorEpochKeeperTransit())
				}
				results <- generatorEpochKeeperResult{target: job.target, client: client, err: err}
			}
		}()
	}

	queued := 0
enqueue:
	for _, target := range targets {
		if cycleCtx.Err() != nil {
			break
		}
		select {
		case jobs <- generatorEpochKeeperJob{target: target, client: clients[target]}:
			queued++
		case <-cycleCtx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()

	failures := make([]error, 0)
	for range queued {
		result := <-results
		if result.err != nil {
			if result.client != nil {
				_ = result.client.Close()
			}
			delete(clients, result.target)
			failures = append(failures, fmt.Errorf("renew generator %s execution generation: %w", result.target, result.err))
			continue
		}
		clients[result.target] = result.client
	}
	if queued != len(targets) {
		failures = append(failures, fmt.Errorf("execution epoch renewal pass ended before all targets were queued: %w", cycleCtx.Err()))
	}
	return errors.Join(failures...)
}

func (b *distributedExecutionBackend) dialGeneratorEpochKeeperClient(ctx context.Context, target string, leaseDeadline time.Time) (generatorClient, error) {
	if b == nil || b.dial == nil {
		return nil, fmt.Errorf("generator dialer is unavailable")
	}
	remaining := time.Until(leaseDeadline)
	if remaining < 3*time.Millisecond {
		return nil, fmt.Errorf("execution lease has insufficient time remaining to dial generator")
	}
	dialBudget := min(b.generatorEpochKeeperTransit(), remaining/3)
	if dialBudget < time.Millisecond {
		return nil, fmt.Errorf("execution epoch dial has less than one millisecond of transit budget")
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialBudget)
	defer cancel()
	client, err := b.dial(dialCtx, target)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("generator dial returned a nil client")
	}
	if err := dialCtx.Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("generator dial exceeded its transit bound: %w", err)
	}
	return client, nil
}

func (b *distributedExecutionBackend) generatorEpochKeeperTransit() time.Duration {
	if b == nil || b.timeout <= 0 {
		return maxGeneratorEpochKeeperTransit
	}
	return min(b.timeout, maxGeneratorEpochKeeperTransit)
}

func closeGeneratorEpochKeeperClients(clients map[string]generatorClient) {
	for target, client := range clients {
		if client != nil {
			_ = client.Close()
		}
		delete(clients, target)
	}
}

func waitForGeneratorEpochKeeper(ctx context.Context, delay time.Duration) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (b *distributedExecutionBackend) renewGeneratorExecutionEpoch(ctx context.Context, tr *TestRunner, test *ManagedTest, generation uint64) {
	if generation == 0 || tr == nil || test == nil {
		return
	}
	for {
		cycleStarted := time.Now()
		currentGeneration, leaseOwnerID, leaseDeadline, err := tr.generatorExecutionClaim()
		if err != nil || currentGeneration != generation {
			return
		}

		// Take a fresh snapshot every cycle. Generator-loss evacuation can add a
		// participant after the run starts; renewing only the startup map would
		// let that replacement's session expire underneath an otherwise-live run.
		tr.mu.RLock()
		if tr.tests[test.ID] != test || test.executionGeneration != generation || test.executionCtx == nil || test.executionCtx.Err() != nil {
			tr.mu.RUnlock()
			return
		}
		clients := cloneClientMap(test.remoteClientsByTarget)
		tr.mu.RUnlock()
		if len(clients) == 0 {
			return
		}

		remaining := time.Until(leaseDeadline)
		if remaining <= 0 {
			return
		}
		renewalErr := b.renewGeneratorExecutionClients(ctx, clients, generation, leaseOwnerID, leaseDeadline)
		var next time.Duration
		if renewalErr != nil {
			slog.Warn("generator execution generation renewal failed", "test_id", test.ID, "generation", generation, "error", renewalErr)
			next = remaining / 10
			if next > 250*time.Millisecond {
				next = 250 * time.Millisecond
			}
		} else {
			next = remaining / 3
			if next > 5*time.Second {
				next = 5 * time.Second
			}
		}
		// Account for time already spent on the fanout so a slow renewal cycle
		// cannot postpone the next attempt past the controller's local deadline.
		next -= time.Since(cycleStarted)
		if next < 10*time.Millisecond {
			next = 10 * time.Millisecond
		}
		timer := time.NewTimer(next)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (b *distributedExecutionBackend) renewGeneratorExecutionClients(ctx context.Context, clients map[string]generatorClient, generation uint64, leaseOwnerID string, leaseDeadline time.Time) error {
	if len(clients) == 0 {
		return nil
	}
	targets := make([]string, 0, len(clients))
	for target := range clients {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	jobs := make(chan string, len(targets))
	errCh := make(chan error, len(targets))
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)
	workerCount := min(maxGeneratorEpochKeeperWorkers, len(targets))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for target := range jobs {
				client := clients[target]
				if client == nil {
					errCh <- fmt.Errorf("renew generator %s execution generation: client is unavailable", target)
					continue
				}
				err := claimGeneratorExecutionEpoch(ctx, client, generation, leaseOwnerID, leaseDeadline, b.timeout)
				if err != nil {
					errCh <- fmt.Errorf("renew generator %s execution generation: %w", target, err)
					continue
				}
				errCh <- nil
			}
		}()
	}
	workers.Wait()
	failures := make([]error, 0)
	for range targets {
		if err := <-errCh; err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
