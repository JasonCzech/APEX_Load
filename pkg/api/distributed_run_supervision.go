package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func runSupervisionTokenForTest(tr *TestRunner, test *ManagedTest) []byte {
	tr.mu.RLock()
	token := test.remoteRunSupervisionToken
	tr.mu.RUnlock()
	return cloneRunSupervisionToken(token)
}

func cloneRunSupervisionToken(token [generatorrpc.RunSupervisionTokenSize]byte) []byte {
	var zero [generatorrpc.RunSupervisionTokenSize]byte
	if token == zero {
		return nil
	}
	return append([]byte(nil), token[:]...)
}

const maxRunSupervisionClaimWorkers = 16

// runSupervisionFanoutBudget keeps one renewal pass comfortably inside the
// configured cadence. With the maximum 256 targets, a fixed two-second timeout
// per queued target can otherwise take 32 seconds and starve later targets.
func (b *distributedExecutionBackend) runSupervisionFanoutBudget() time.Duration {
	budget := b.runSupervisionRenew * 3 / 4
	if budget <= 0 {
		budget = 2 * time.Second
	}
	if ttlBudget := b.runSupervisionTTL / 4; ttlBudget > 0 && ttlBudget < budget {
		budget = ttlBudget
	}
	if budget < 100*time.Millisecond {
		budget = 100 * time.Millisecond
	}
	return budget
}

func (b *distributedExecutionBackend) claimRunSupervisionClients(parent context.Context, testID string, clients map[string]generatorClient, generation uint64, ownerID string, token []byte) error {
	err, _ := b.claimRunSupervisionClientsWithJoin(parent, testID, clients, generation, ownerID, token)
	return err
}

// claimRunSupervisionClientsWithJoin returns both the bounded fanout result and
// a channel that closes only after every ClaimRunSupervision invocation has
// actually returned. A keeper must join that channel before advertising itself
// stopped; otherwise a cancellation-ignoring client could renew an evacuated
// source after the controller sampled its process-fence deadline.
func (b *distributedExecutionBackend) claimRunSupervisionClientsWithJoin(parent context.Context, testID string, clients map[string]generatorClient, generation uint64, ownerID string, token []byte) (error, <-chan struct{}) {
	workersDone := make(chan struct{})
	if !b.requireRunSupervision {
		close(workersDone)
		return nil, workersDone
	}
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	if len(targets) == 0 {
		close(workersDone)
		return fmt.Errorf("no assigned generator clients"), workersDone
	}
	if parent == nil {
		parent = context.Background()
	}
	fanoutBudget := b.runSupervisionFanoutBudget()
	fanoutCtx, fanoutCancel := context.WithTimeout(parent, fanoutBudget)
	defer fanoutCancel()
	type result struct {
		target string
		err    error
	}
	jobs := make(chan string, len(targets))
	results := make(chan result, len(targets))
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)
	workerCount := min(len(targets), maxRunSupervisionClaimWorkers)
	rounds := (len(targets) + workerCount - 1) / workerCount
	perTargetTimeout := fanoutBudget / time.Duration(rounds)
	if perTargetTimeout > 2*time.Second {
		perTargetTimeout = 2 * time.Second
	}
	if b.timeout > 0 && perTargetTimeout > b.timeout {
		perTargetTimeout = b.timeout
	}
	if perTargetTimeout < time.Millisecond {
		perTargetTimeout = time.Millisecond
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for target := range jobs {
				client, ok := clients[target].(runSupervisionGeneratorClient)
				if !ok {
					results <- result{target: target, err: fmt.Errorf("generator client does not support %s", generatorrpc.FeatureRunSupervisionV1)}
					continue
				}
				// The timeout starts when this target leaves the queue. The outer
				// fanout deadline bounds the complete pass, while this per-target
				// deadline prevents one unreachable generator from consuming a
				// worker for the rest of the cadence.
				ctx, cancel := context.WithTimeout(fanoutCtx, perTargetTimeout)
				claimToken := append([]byte(nil), token...)
				claim, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{
					TestID: testID, ExecutionGeneration: generation, Token: claimToken,
					RequestedTTL: b.runSupervisionTTL, ExecutionLeaseOwnerID: ownerID,
				})
				cancel()
				clear(claimToken)
				if err == nil {
					switch {
					case !claim.Accepted:
						err = fmt.Errorf("generator rejected run supervision: %s", claim.ErrorMessage)
					case claim.ContractVersion != generatorrpc.RunSupervisionContractV1:
						err = fmt.Errorf("generator returned run supervision contract version %d", claim.ContractVersion)
					case claim.GrantedTTL < 30*time.Second:
						err = fmt.Errorf("generator granted unsafe run supervision TTL %s", claim.GrantedTTL)
					case claim.GrantedTTL < 3*b.runSupervisionRenew:
						err = fmt.Errorf("generator granted run supervision TTL %s, below three renewal intervals (%s)", claim.GrantedTTL, 3*b.runSupervisionRenew)
					}
				}
				results <- result{target: target, err: err}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	var failures []error
	for remaining := len(targets); remaining > 0; remaining-- {
		select {
		case result := <-results:
			if result.err != nil {
				failures = append(failures, fmt.Errorf("claim run supervision on %s: %w", result.target, result.err))
			}
		case <-fanoutCtx.Done():
			failures = append(failures, fmt.Errorf("run supervision claim fanout: %w", fanoutCtx.Err()))
			return errors.Join(failures...), workersDone
		}
	}
	return errors.Join(failures...), workersDone
}

func waitForRunSupervisionClaimWorkers(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *distributedExecutionBackend) runSupervisionKeeperJoinTimeout() time.Duration {
	timeout := b.timeout
	if timeout <= 0 || timeout > 2*time.Second {
		timeout = 2 * time.Second
	}
	return timeout + 250*time.Millisecond
}

type runSupervisionKeeperHandle struct {
	generation uint64
	cancel     context.CancelFunc
	done       chan struct{}
}

// detachRunSupervisionKeeperLocked invalidates the keeper before returning its
// join handles. A stale cleanup closure may therefore never clear a replacement
// keeper installed by evacuation or a later execution incarnation.
func detachRunSupervisionKeeperLocked(test *ManagedTest) runSupervisionKeeperHandle {
	if test == nil {
		return runSupervisionKeeperHandle{}
	}
	handle := runSupervisionKeeperHandle{
		generation: test.remoteRunSupervisionKeeperGeneration,
		cancel:     test.remoteRunSupervisionCancel,
		done:       test.remoteRunSupervisionDone,
	}
	test.remoteRunSupervisionKeeperGeneration++
	test.remoteRunSupervisionCancel = nil
	test.remoteRunSupervisionDone = nil
	return handle
}

func stopAndWaitRunSupervisionKeeper(ctx context.Context, handle runSupervisionKeeperHandle) error {
	if handle.cancel != nil {
		handle.cancel()
	}
	if handle.done == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-handle.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startRunSupervisionKeeper installs one owner for staged and live renewal. It
// reads the published topology after activation, so evacuation replacements
// enter subsequent cycles without starting a competing keeper.
func (b *distributedExecutionBackend) startRunSupervisionKeeper(parent context.Context, tr *TestRunner, test *ManagedTest, staged map[string]generatorClient, generation uint64, ownerID string, token [generatorrpc.RunSupervisionTokenSize]byte) func() {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	tr.mu.Lock()
	installed := tr.tests[test.ID] == test && !isTerminalTestStatus(test.Status) && test.Status != "STOPPING" && test.remoteRunSupervisionToken == token && test.remoteRunSupervisionCancel == nil
	keeperGeneration := test.remoteRunSupervisionKeeperGeneration
	if installed {
		test.remoteRunSupervisionKeeperGeneration++
		keeperGeneration = test.remoteRunSupervisionKeeperGeneration
		test.remoteRunSupervisionCancel = cancel
		test.remoteRunSupervisionDone = done
	}
	tr.mu.Unlock()
	if !installed {
		cancel()
	}
	go func() {
		defer close(done)
		defer clear(token[:])
		ticker := time.NewTicker(b.runSupervisionRenew)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			tr.mu.RLock()
			current := tr.tests[test.ID]
			clients := cloneClientMap(staged)
			if current == test && len(test.remoteClientsByTarget) > 0 {
				clients = cloneClientMap(test.remoteClientsByTarget)
			}
			for target := range test.remoteRunSupervisionFencedTargets {
				delete(clients, target)
			}
			active := current == test && !isTerminalTestStatus(test.Status) && test.Status != "STOPPING" && test.remoteRunSupervisionToken == token && test.remoteRunSupervisionKeeperGeneration == keeperGeneration
			tr.mu.RUnlock()
			if !active {
				return
			}
			err, claimsDone := b.claimRunSupervisionClientsWithJoin(ctx, test.ID, clients, generation, ownerID, token[:])
			if err != nil && ctx.Err() == nil {
				slog.Warn("generator run supervision renewal failed", "test_id", test.ID, "execution_generation", generation, "error", err)
			}
			// Do not begin another round, or let keeper shutdown report joined,
			// until the RPC workers from this round have really returned.
			if err := waitForRunSupervisionClaimWorkers(context.Background(), claimsDone); err != nil {
				return
			}
		}
	}()
	return func() {
		joinCtx, joinCancel := context.WithTimeout(context.Background(), b.runSupervisionKeeperJoinTimeout())
		joinErr := stopAndWaitRunSupervisionKeeper(joinCtx, runSupervisionKeeperHandle{generation: keeperGeneration, cancel: cancel, done: done})
		joinCancel()
		if joinErr != nil {
			slog.Warn("timed out joining generator run supervision keeper", "test_id", test.ID, "execution_generation", generation, "error", joinErr)
		}
		tr.mu.Lock()
		if installed && test.remoteRunSupervisionKeeperGeneration == keeperGeneration {
			test.remoteRunSupervisionCancel = nil
			test.remoteRunSupervisionDone = nil
		}
		tr.mu.Unlock()
	}
}

func (b *distributedExecutionBackend) startRunSupervisionTargetKeeper(parent context.Context, testID string, clients map[string]generatorClient, generation uint64, ownerID string, token []byte) func() {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	keeperToken := append([]byte(nil), token...)
	go func() {
		defer close(done)
		defer clear(keeperToken)
		ticker := time.NewTicker(b.runSupervisionRenew)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err, claimsDone := b.claimRunSupervisionClientsWithJoin(ctx, testID, clients, generation, ownerID, keeperToken)
				if err != nil && ctx.Err() == nil {
					slog.Warn("staged generator run supervision renewal failed", "test_id", testID, "execution_generation", generation, "error", err)
				}
				if err := waitForRunSupervisionClaimWorkers(context.Background(), claimsDone); err != nil {
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		joinCtx, joinCancel := context.WithTimeout(context.Background(), b.runSupervisionKeeperJoinTimeout())
		defer joinCancel()
		select {
		case <-done:
		case <-joinCtx.Done():
			slog.Warn("timed out joining staged generator run supervision keeper", "test_id", testID, "execution_generation", generation)
		}
	}
}

// fenceRunSupervisionTarget cancels and joins any in-flight renewal before it
// excludes a source. The replacement keeper renews only the remaining topology.
func (b *distributedExecutionBackend) fenceRunSupervisionTarget(ctx context.Context, tr *TestRunner, test *ManagedTest, target string, generation uint64, ownerID string) error {
	tr.mu.Lock()
	if tr.tests[test.ID] != test {
		tr.mu.Unlock()
		return fmt.Errorf("run supervision source fence became stale")
	}
	keeper := detachRunSupervisionKeeperLocked(test)
	if test.remoteRunSupervisionFencedTargets == nil {
		test.remoteRunSupervisionFencedTargets = make(map[string]bool)
	}
	test.remoteRunSupervisionFencedTargets[target] = true
	token := test.remoteRunSupervisionToken
	executionCtx := test.executionCtx
	tr.mu.Unlock()
	if err := stopAndWaitRunSupervisionKeeper(ctx, keeper); err != nil {
		return fmt.Errorf("join source run supervision renewals: %w", err)
	}
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	b.startRunSupervisionKeeper(executionCtx, tr, test, nil, generation, ownerID, token)
	return nil
}

func stopGeneratorTestWithToken(ctx context.Context, client generatorClient, testID string, graceful bool, generation uint64, token []byte) (generatorrpc.StopResult, error) {
	if len(token) == generatorrpc.RunSupervisionTokenSize {
		supervised, ok := client.(supervisedStopClient)
		if !ok {
			return generatorrpc.StopResult{}, fmt.Errorf("generator client does not support token-fenced stop")
		}
		return supervised.StopTestAtGenerationWithToken(ctx, testID, graceful, generation, token)
	}
	return stopGeneratorTest(ctx, client, testID, graceful, generation)
}
