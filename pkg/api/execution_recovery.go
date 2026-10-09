package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

const (
	// executionRecoveryGiveUpAfter bounds how long a replica holds the execution
	// lease while acquire-time recovery keeps failing. Past it, the replica hands
	// armed continue-policy runs off and steps down so another replica can try;
	// otherwise a replica-local fault (credentials, network path) would keep
	// execution closed while its healthy lease renewals block every takeover.
	executionRecoveryGiveUpAfter = 2 * time.Minute
	// executionRecoveryCooldown keeps a replica that gave up from contending
	// again before another replica has had several renewal ticks to take over.
	executionRecoveryCooldown = time.Minute
	// executionRecoveryMaxRetryDelay caps the backoff between recovery attempts.
	executionRecoveryMaxRetryDelay = 5 * time.Second
	// capacityTakeoverMaxRetryDelay caps the backoff between provider sweeps of
	// pools whose post-takeover reconciliation failed.
	capacityTakeoverMaxRetryDelay = 30 * time.Second
	capacitySweepPollInterval     = 250 * time.Millisecond
)

// recoverExecutionLeadership is the execution-lease acquire callback. It
// reconciles durable state while admission is closed and retries with backoff.
// Provider recovery runs after admission (runCapacityTakeoverRecovery). If
// recovery still fails after the give-up budget, the replica hands adoptable
// runs to a successor and steps down, because holding the lease without
// admitting work blocks every other replica from taking over.
func (s *Server) recoverExecutionLeadership() {
	leadership, runner := s.leadership, s.runner
	if leadership == nil || runner == nil {
		return
	}
	_, generation := leadership.executionLeaseTerm()
	// Gate provider launches until the post-admission sweep covers each pool.
	s.capacitySweep.begin(generation)
	started := time.Now()
	s.executionRecovery.begin(started)
	step := s.executionRecoveryStep
	if step == nil {
		step = s.runExecutionRecoverySteps
	}
	giveUpAfter := s.executionRecoveryGiveUp
	if giveUpAfter <= 0 {
		giveUpAfter = executionRecoveryGiveUpAfter
	}
	delay := 250 * time.Millisecond
	for leadership.IsLeader() {
		if runner.runAdoptionEnabled() {
			s.publishExecutionEndpoint(true)
		}
		err := step()
		if err == nil {
			s.executionRecovery.finish()
			return
		}
		s.executionRecovery.recordFailure()
		if !leadership.IsLeader() {
			return
		}
		slog.Warn("execution leadership recovery failed; admission remains closed", "error", err)
		if time.Since(started) >= giveUpAfter {
			handedOff := runner.markContinuityHandoff(generation)
			s.executionRecovery.finish()
			slog.Error("execution leadership recovery did not complete; stepping down so another replica can take over",
				"error", err, "recovering_for", time.Since(started).Round(time.Second), "handed_off_runs", handedOff, "cooldown", executionRecoveryCooldown)
			leadership.relinquishAfterRecoveryFailure(executionRecoveryCooldown)
			return
		}
		ctx, cancel := leadership.operationContext(context.Background())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			cancel()
			return
		case <-timer.C:
			cancel()
		}
		delay = min(2*delay, executionRecoveryMaxRetryDelay)
	}
}

// runExecutionRecoverySteps is the durable recovery a new leader completes
// before admission.
func (s *Server) runExecutionRecoverySteps() error {
	if err := s.reloadGeneratorDrainStatesOnLeadershipAcquire(); err != nil {
		return fmt.Errorf("generator drain-state refresh: %w", err)
	}
	if err := s.repairScriptRecordingsOnLeadershipAcquire(); err != nil {
		return fmt.Errorf("script recording repair: %w", err)
	}
	return s.runner.reloadAndReconcile()
}

// executionRecoveryState records acquire-time recovery for the execution
// readiness endpoint, so "leader but not accepting work" is observable rather
// than indistinguishable from an ordinary follower.
type executionRecoveryState struct {
	mu        sync.Mutex
	active    bool
	startedAt time.Time
	attempts  int
}

func (r *executionRecoveryState) begin(now time.Time) {
	r.mu.Lock()
	r.active, r.startedAt, r.attempts = true, now.UTC(), 0
	r.mu.Unlock()
}

func (r *executionRecoveryState) recordFailure() {
	r.mu.Lock()
	r.attempts++
	r.mu.Unlock()
}

func (r *executionRecoveryState) finish() {
	r.mu.Lock()
	r.active = false
	r.mu.Unlock()
}

func (r *executionRecoveryState) snapshot() (active bool, startedAt time.Time, attempts int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active, r.startedAt, r.attempts
}

// capacityTakeoverSweep gates provider launches per fleet pool until the
// successor's orphaned-capacity sweep has covered that pool.
//
// The sweep deprovisions provider instances it cannot match to a durable
// intent, but it lists intents only once per pool. A launch that persists a
// new intent after that listing could therefore have its instance terminated
// as "untracked". Before admission no launches happen; after admission a pool
// launches only once its sweep has succeeded, and a swept pool is never swept
// again in the same term. One unreachable provider therefore blocks launches
// only on its own pools instead of keeping the whole controller closed.
type capacityTakeoverSweep struct {
	mu         sync.Mutex
	generation uint64
	active     bool
	// planned is set once the first full pass has classified every pool. Pools
	// created later have no predecessor capacity and never need a sweep.
	planned bool
	swept   map[string]bool
	failed  map[string]string
}

// begin starts the sweep for one leadership term. Until then, and in
// single-controller mode where no takeover sweep exists, launches are not gated.
func (c *capacityTakeoverSweep) begin(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation = generation
	c.active = true
	c.planned = false
	c.swept = make(map[string]bool)
	c.failed = make(map[string]string)
}

func (c *capacityTakeoverSweep) currentLocked(generation uint64) bool {
	return c.active && c.generation == generation
}

func (c *capacityTakeoverSweep) markSwept(generation uint64, poolID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentLocked(generation) {
		return
	}
	c.swept[poolID] = true
	delete(c.failed, poolID)
	c.completeIfDoneLocked()
}

func (c *capacityTakeoverSweep) markFailed(generation uint64, poolID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentLocked(generation) || c.swept[poolID] {
		return
	}
	c.failed[poolID] = err.Error()
}

func (c *capacityTakeoverSweep) markPlanned(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentLocked(generation) {
		return
	}
	c.planned = true
	c.completeIfDoneLocked()
}

func (c *capacityTakeoverSweep) completeIfDoneLocked() {
	if c.planned && len(c.failed) == 0 {
		c.active = false
	}
}

// pending reports whether a pass is still required and, after the first full
// pass, the pools that must be retried.
func (c *capacityTakeoverSweep) pending(generation uint64) (required bool, planned bool, poolIDs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentLocked(generation) {
		return false, false, nil
	}
	for poolID := range c.failed {
		poolIDs = append(poolIDs, poolID)
	}
	sort.Strings(poolIDs)
	return true, c.planned, poolIDs
}

func (c *capacityTakeoverSweep) alreadySwept(generation uint64, poolID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentLocked(generation) && c.swept[poolID]
}

// poolReady reports whether a provider launch may proceed for poolID.
func (c *capacityTakeoverSweep) poolReady(poolID string) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active || c.swept[poolID] {
		return true, ""
	}
	if !c.planned {
		return false, "waiting for provider reconciliation after controller takeover"
	}
	if _, failed := c.failed[poolID]; failed {
		return false, "provider reconciliation after controller takeover has not succeeded for this pool; retrying"
	}
	return true, ""
}

func (c *capacityTakeoverSweep) pendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return 0
	}
	return len(c.failed)
}

// awaitCapacityPoolSweep blocks a provider launch for poolID until the
// takeover sweep has covered it, ctx ends, or deadline passes.
func (s *Server) awaitCapacityPoolSweep(ctx context.Context, poolID string, deadline time.Time) error {
	for {
		ready, reason := s.capacitySweep.poolReady(poolID)
		if ready {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("capacity pool %s is not ready: %s", poolID, reason)
		}
		timer := time.NewTimer(capacitySweepPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// runCapacityTakeoverRecovery resumes provider work the previous leader left
// in flight. It runs after admission: provisioning recovery and the orphaned-
// capacity sweep call external Kubernetes and cloud APIs, and a slow or failing
// provider must not keep the execution leader closed. Pools awaiting their
// sweep refuse new launches (see capacityTakeoverSweep) and are retried with
// backoff for as long as this term leads.
func (s *Server) runCapacityTakeoverRecovery(generation uint64) {
	if s == nil || s.leadership == nil {
		return
	}
	delay := time.Second
	provisioningRecovered := false
	lastLogged := ""
	for {
		owner, current := s.leadership.executionLeaseTerm()
		if owner == "" || current != generation || !s.isExecutionLeader() {
			return
		}
		ctx, cancel := s.leadership.operationContext(context.Background())
		var errs []error
		if !provisioningRecovered {
			if err := s.reconcileProvisioningRequests(ctx); err != nil {
				errs = append(errs, fmt.Errorf("provisioning recovery: %w", err))
			} else {
				provisioningRecovered = true
			}
		}
		if err := s.reconcileOrphanedCapacityForTerm(ctx, generation); err != nil {
			errs = append(errs, err)
		}
		required, _, _ := s.capacitySweep.pending(generation)
		if provisioningRecovered && !required {
			cancel()
			return
		}
		if err := errors.Join(errs...); err != nil && err.Error() != lastLogged {
			lastLogged = err.Error()
			slog.Warn("provider recovery after controller takeover is incomplete; affected pools refuse new launches until it succeeds", "pending_pools", s.capacitySweep.pendingCount(), "error", err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			cancel()
			return
		case <-timer.C:
		}
		cancel()
		delay = min(2*delay, capacityTakeoverMaxRetryDelay)
	}
}
