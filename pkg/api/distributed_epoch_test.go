package api

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type epochRenewalFakeClient struct {
	startupLifecycleFakeClient
	claimHook func(context.Context, uint64, time.Duration) error
	calls     atomic.Int32
	notified  chan struct{}
}

func (c *epochRenewalFakeClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, _ string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	c.calls.Add(1)
	if c.notified != nil {
		select {
		case c.notified <- struct{}{}:
		default:
		}
	}
	if c.claimHook != nil {
		if err := c.claimHook(ctx, generation, ttl); err != nil {
			return generatorrpc.ExecutionEpochResult{}, err
		}
	}
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation}, nil
}

func (c *epochRenewalFakeClient) ClaimTestExecutionEpoch(ctx context.Context, _ string, generation uint64, owner string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	return c.ClaimExecutionEpoch(ctx, generation, owner, ttl)
}

type generatorEpochKeeperLeaseStore struct {
	mu         sync.Mutex
	acquired   bool
	generation uint64
}

func (s *generatorEpochKeeperLeaseStore) TryAcquireSchedulerLease(string, string, time.Time, time.Duration) (bool, error) {
	panic("generation-aware lease method should be used")
}

func (s *generatorEpochKeeperLeaseStore) TryAcquireSchedulerLeaseTerm(name, owner string, now time.Time, ttl time.Duration) (bool, *storage.SchedulerLease, error) {
	s.mu.Lock()
	acquired := s.acquired
	generation := s.generation
	s.mu.Unlock()
	if !acquired {
		return false, nil, nil
	}
	return true, &storage.SchedulerLease{
		Name:       name,
		OwnerID:    owner,
		Generation: generation,
		AcquiredAt: now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(ttl),
	}, nil
}

func (s *generatorEpochKeeperLeaseStore) ReleaseSchedulerLease(string, string) error {
	return nil
}

func (s *generatorEpochKeeperLeaseStore) set(acquired bool, generation uint64) {
	s.mu.Lock()
	s.acquired = acquired
	s.generation = generation
	s.mu.Unlock()
}

type expiringGeneratorEpochClient struct {
	startupLifecycleFakeClient
	epochMu    sync.Mutex
	generation uint64
	expiresAt  time.Time
	accepted   chan uint64
}

func (c *expiringGeneratorEpochClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, _ string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	if err := ctx.Err(); err != nil {
		return generatorrpc.ExecutionEpochResult{}, err
	}
	now := time.Now()
	c.epochMu.Lock()
	if generation < c.generation {
		current := c.generation
		c.epochMu.Unlock()
		return generatorrpc.ExecutionEpochResult{CurrentGeneration: current, ErrorMessage: "stale generation"}, nil
	}
	if generation == c.generation && !c.expiresAt.IsZero() && !now.Before(c.expiresAt) {
		current := c.generation
		c.epochMu.Unlock()
		return generatorrpc.ExecutionEpochResult{CurrentGeneration: current, ErrorMessage: "generation expired"}, nil
	}
	c.generation = generation
	c.expiresAt = now.Add(ttl)
	c.epochMu.Unlock()
	if c.accepted != nil {
		select {
		case c.accepted <- generation:
		default:
		}
	}
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation}, nil
}

type leadershipTransitionEpochClient struct {
	startupLifecycleFakeClient
	oldStarted  chan struct{}
	oldCanceled chan struct{}
	newClaimed  chan struct{}
	startOnce   sync.Once
	cancelOnce  sync.Once
	newOnce     sync.Once
}

func (c *leadershipTransitionEpochClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	switch generation {
	case 71:
		c.startOnce.Do(func() { close(c.oldStarted) })
		<-ctx.Done()
		c.cancelOnce.Do(func() { close(c.oldCanceled) })
		return generatorrpc.ExecutionEpochResult{}, ctx.Err()
	case 72:
		c.newOnce.Do(func() { close(c.newClaimed) })
		return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation}, nil
	default:
		return generatorrpc.ExecutionEpochResult{CurrentGeneration: generation, ErrorMessage: "unexpected generation"}, nil
	}
}

func waitForEpochTestSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func stopEpochKeeperServer(t *testing.T, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("execution leadership and generator epoch keeper did not stop")
	}
}

func TestGeneratorExecutionEpochKeeperAllowsNextRunAfterIdleBeyondTTL(t *testing.T) {
	store := &generatorEpochKeeperLeaseStore{acquired: true, generation: 70}
	leadership := newExecutionLeadership(store, "idle-keeper")
	leadership.ttl = 500 * time.Millisecond
	leadership.renew = 50 * time.Millisecond
	client := &expiringGeneratorEpochClient{accepted: make(chan uint64, 64)}
	backend := newDistributedExecutionBackend([]string{"generator-idle"}, 15*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return client, nil
	})
	runner := &TestRunner{leadership: leadership, execution: backend}
	server := &Server{leadership: leadership, runner: runner}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		server.RunExecutionLeadership(ctx)
		close(done)
	}()
	defer func() {
		select {
		case <-done:
		default:
			stopEpochKeeperServer(t, cancel, done)
		}
	}()

	select {
	case generation := <-client.accepted:
		if generation != 70 {
			t.Fatalf("initial keeper generation = %d, want 70", generation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("generator was not initially claimed by leadership keeper")
	}
	// This is longer than any single claim's TTL. Without leadership-scoped
	// renewal, the generator's same-generation no-revival rule rejects the next
	// run at this point.
	time.Sleep(3 * leadership.ttl)
	generation, leaseOwnerID, deadline, err := runner.generatorExecutionClaim()
	if err != nil {
		t.Fatalf("capture next-run execution claim: %v", err)
	}
	if err := claimGeneratorExecutionEpoch(context.Background(), client, generation, leaseOwnerID, deadline, 15*time.Millisecond); err != nil {
		t.Fatalf("next run after idle period could not use live generation: %v", err)
	}
	stopEpochKeeperServer(t, cancel, done)
}

func TestGeneratorExecutionEpochKeeperCancelsOldTermAndClaimsReacquiredTerm(t *testing.T) {
	store := &generatorEpochKeeperLeaseStore{acquired: true, generation: 71}
	leadership := newExecutionLeadership(store, "transition-keeper")
	leadership.ttl = time.Second
	leadership.renew = 20 * time.Millisecond
	client := &leadershipTransitionEpochClient{
		oldStarted:  make(chan struct{}),
		oldCanceled: make(chan struct{}),
		newClaimed:  make(chan struct{}),
	}
	backend := newDistributedExecutionBackend([]string{"generator-transition"}, 500*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return client, nil
	})
	runner := &TestRunner{leadership: leadership, execution: backend}
	server := &Server{leadership: leadership, runner: runner}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		server.RunExecutionLeadership(ctx)
		close(done)
	}()
	defer func() {
		select {
		case <-done:
		default:
			stopEpochKeeperServer(t, cancel, done)
		}
	}()

	waitForEpochTestSignal(t, client.oldStarted, "old-generation claim to start")
	store.set(false, 71)
	waitForEpochTestSignal(t, client.oldCanceled, "leadership loss to cancel old-generation claim")
	if leadership.IsLeader() {
		t.Fatal("controller remained leader after lease store rejected renewal")
	}
	store.set(true, 72)
	waitForEpochTestSignal(t, client.newClaimed, "reacquired generation to be claimed")
	if generation, _, _, err := runner.generatorExecutionClaim(); err != nil || generation != 72 {
		t.Fatalf("reacquired controller claim = generation %d, err %v; want generation 72", generation, err)
	}
	stopEpochKeeperServer(t, cancel, done)
}

func TestGeneratorExecutionEpochKeeperRefreshesDiscoveredTargetAfterDialFailures(t *testing.T) {
	store := &generatorEpochKeeperLeaseStore{acquired: true, generation: 73}
	leadership := newExecutionLeadership(store, "discovery-keeper")
	leadership.ttl = 180 * time.Millisecond
	leadership.renew = 25 * time.Millisecond
	initial := &expiringGeneratorEpochClient{accepted: make(chan uint64, 32)}
	discovered := &expiringGeneratorEpochClient{accepted: make(chan uint64, 32)}
	var discoveredDials atomic.Int32
	backend := newDistributedExecutionBackend([]string{"generator-initial"}, 15*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		switch target {
		case "generator-initial":
			return initial, nil
		case "generator-discovered":
			if discoveredDials.Add(1) <= 2 {
				return nil, errors.New("temporarily unreachable")
			}
			return discovered, nil
		default:
			return nil, fmt.Errorf("unexpected target %q", target)
		}
	})
	runner := &TestRunner{leadership: leadership, execution: backend}
	server := &Server{leadership: leadership, runner: runner}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		server.RunExecutionLeadership(ctx)
		close(done)
	}()
	defer func() {
		select {
		case <-done:
		default:
			stopEpochKeeperServer(t, cancel, done)
		}
	}()

	select {
	case <-initial.accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial generator was not claimed")
	}
	backend.SetTargets([]string{"generator-initial", "generator-discovered"})
	select {
	case generation := <-discovered.accepted:
		if generation != 73 {
			t.Fatalf("discovered generator generation = %d, want 73", generation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("temporarily unreachable discovered generator was not retried and claimed")
	}
	if attempts := discoveredDials.Load(); attempts < 3 {
		t.Fatalf("discovered generator dial attempts = %d, want at least 3", attempts)
	}
	stopEpochKeeperServer(t, cancel, done)
}

func TestGeneratorExecutionEpochKeeperBoundsConcurrentDials(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	started := make(chan struct{}, 64)
	targets := make([]string, 64)
	for i := range targets {
		targets[i] = fmt.Sprintf("generator-%02d", i)
	}
	backend := newDistributedExecutionBackend(targets, time.Second, func(ctx context.Context, _ string) (generatorClient, error) {
		current := active.Add(1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		started <- struct{}{}
		<-ctx.Done()
		active.Add(-1)
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- backend.claimConfiguredGeneratorExecutionEpoch(ctx, make(map[string]generatorClient), targets, 74, "controller/74", time.Now().Add(2*time.Second))
	}()
	for range maxGeneratorEpochKeeperWorkers {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("bounded epoch workers did not start")
		}
	}
	time.Sleep(25 * time.Millisecond)
	if got := maximum.Load(); got > maxGeneratorEpochKeeperWorkers {
		t.Fatalf("concurrent epoch dials = %d, limit %d", got, maxGeneratorEpochKeeperWorkers)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bounded epoch dial pass did not stop after cancellation")
	}
}

func TestRenewGeneratorTestExecutionClientsDoesNotSerializeSlowTargets(t *testing.T) {
	slowStarted := make(chan struct{}, 1)
	releaseSlow := make(chan struct{})
	slow := &epochRenewalFakeClient{claimHook: func(ctx context.Context, _ uint64, _ time.Duration) error {
		slowStarted <- struct{}{}
		select {
		case <-releaseSlow:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	fast := &epochRenewalFakeClient{notified: make(chan struct{}, 1)}
	backend := &distributedExecutionBackend{timeout: time.Second}
	done := make(chan error, 1)
	go func() {
		done <- backend.renewGeneratorTestExecutionClients(context.Background(), "run", map[string]generatorClient{
			"slow": slow,
			"fast": fast,
		}, 12, "controller/12", time.Now().Add(2*time.Second))
	}()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow generator renewal did not start")
	}
	select {
	case <-fast.notified:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("fast generator renewal was serialized behind a slow target")
	}
	close(releaseSlow)
	if err := <-done; err != nil {
		t.Fatalf("renew fanout: %v", err)
	}
}

func TestRenewGeneratorTestExecutionClientsDoesNotStarveHealthyTail(t *testing.T) {
	backend := &distributedExecutionBackend{timeout: 10 * time.Second}
	clients := make(map[string]generatorClient, maxGeneratorEpochKeeperWorkers+1)
	for index := range maxGeneratorEpochKeeperWorkers {
		clients[fmt.Sprintf("a-blocked-%02d", index)] = &epochRenewalFakeClient{claimHook: func(ctx context.Context, _ uint64, _ time.Duration) error {
			<-ctx.Done()
			return ctx.Err()
		}}
	}
	healthy := &epochRenewalFakeClient{notified: make(chan struct{}, 1)}
	clients["z-healthy"] = healthy
	deadline := time.Now().Add(900 * time.Millisecond)
	done := make(chan error, 1)
	go func() {
		done <- backend.renewGeneratorTestExecutionClients(context.Background(), "healthy-tail", clients, 1, "controller/1", deadline)
	}()
	select {
	case <-healthy.notified:
	case <-time.After(450 * time.Millisecond):
		t.Fatal("healthy tail target was starved behind one saturated worker round")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bounded assignment renewal pass did not finish")
	}
}

func TestGeneratorExecutionClaimSubtractsDelayedDeliveryBudget(t *testing.T) {
	receivedExpiry := make(chan time.Time, 1)
	client := &epochRenewalFakeClient{claimHook: func(ctx context.Context, _ uint64, ttl time.Duration) error {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			receivedExpiry <- time.Now().Add(ttl)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	leaseDeadline := time.Now().Add(600 * time.Millisecond)
	if err := claimGeneratorExecutionEpoch(context.Background(), client, 13, "controller/13", leaseDeadline, 200*time.Millisecond); err != nil {
		t.Fatalf("claim delayed generator: %v", err)
	}
	generatorExpiry := <-receivedExpiry
	if generatorExpiry.After(leaseDeadline) {
		t.Fatalf("delayed generator session expires at %s, after controller deadline %s", generatorExpiry, leaseDeadline)
	}
}

func TestAssignmentLeaseCapabilityFailsClosedForOlderGenerator(t *testing.T) {
	client := &startupLifecycleFakeClient{}
	err := claimGeneratorTestExecutionLease(context.Background(), client, "loss-tolerant-run", 13, "controller/13", time.Now().Add(time.Second), 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "assignment-scoped execution fencing") {
		t.Fatalf("older generator capability error = %v", err)
	}
}

func TestRunScopedFenceDoesNotSuppressHealthyRunRenewal(t *testing.T) {
	now := time.Now().UTC()
	leadership := newExecutionLeadership(nil, "multi-run-fence")
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = "multi-run-fence/1"
	leadership.durableGeneration = 31
	leadership.localLeaseDeadline = now.Add(500 * time.Millisecond)
	leadership.mu.Unlock()

	lostClient := &epochRenewalFakeClient{notified: make(chan struct{}, 1)}
	healthyClient := &epochRenewalFakeClient{notified: make(chan struct{}, 1)}
	lostCtx, cancelLost := context.WithCancel(context.Background())
	defer cancelLost()
	healthyCtx, cancelHealthy := context.WithCancel(context.Background())
	defer cancelHealthy()
	lost := &ManagedTest{ID: "lost-run", executionCtx: lostCtx, executionGeneration: 31, remoteClientsByTarget: map[string]generatorClient{"shared": lostClient}}
	healthy := &ManagedTest{ID: "healthy-run", executionCtx: healthyCtx, executionGeneration: 31, remoteClientsByTarget: map[string]generatorClient{"shared": healthyClient}}
	runner := &TestRunner{tests: map[string]*ManagedTest{lost.ID: lost, healthy.ID: healthy}, leadership: leadership}
	backend := newDistributedExecutionBackend([]string{"shared"}, 20*time.Millisecond, nil)
	backend.fenceTestExecutionLease(lost, "shared")

	lostDone := make(chan struct{})
	go func() {
		backend.renewGeneratorExecutionEpoch(lostCtx, runner, lost, 31)
		close(lostDone)
	}()
	go backend.renewGeneratorExecutionEpoch(healthyCtx, runner, healthy, 31)
	time.Sleep(50 * time.Millisecond)
	select {
	case <-healthyClient.notified:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("healthy run on shared target was not renewed")
	}
	if calls := lostClient.calls.Load(); calls != 0 {
		t.Fatalf("fenced run received %d renewal calls", calls)
	}
	cancelLost()
	select {
	case <-lostDone:
	case <-time.After(time.Second):
		t.Fatal("fenced run renewal did not stop after cancellation")
	}
}

func TestStagedAssignmentLeaseKeeperRenewsBeforeLiveTopologyCommit(t *testing.T) {
	now := time.Now().UTC()
	leadership := newExecutionLeadership(nil, "staged-replacement")
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = "staged-replacement/1"
	leadership.durableGeneration = 41
	leadership.localLeaseDeadline = now.Add(180 * time.Millisecond)
	leadership.mu.Unlock()
	client := &epochRenewalFakeClient{notified: make(chan struct{}, 8)}
	runner := &TestRunner{leadership: leadership}
	backend := &distributedExecutionBackend{timeout: 20 * time.Millisecond}
	testRun := &ManagedTest{ID: "replacement"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		backend.maintainStagedGeneratorTestExecutionLeases(ctx, runner, testRun, 41, map[string]generatorClient{"new": client})
		close(done)
	}()
	select {
	case <-client.notified:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("staged replacement did not receive its first renewal")
	}
	leadership.mu.Lock()
	leadership.localLeaseDeadline = time.Now().Add(400 * time.Millisecond)
	leadership.mu.Unlock()
	deadline := time.Now().Add(250 * time.Millisecond)
	for client.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls := client.calls.Load(); calls < 2 {
		t.Fatalf("staged replacement renewal calls = %d, want at least 2", calls)
	}
	backend.fenceTestExecutionLease(testRun, "new")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("staged replacement lease keeper did not stop after its assignment was fenced")
	}
	cancel()
}

// scopedEpochRenewalFakeClient separates the two execution-fencing scopes that
// epochRenewalFakeClient deliberately conflates, so a test can prove which one a
// keeper actually renews.
type scopedEpochRenewalFakeClient struct {
	startupLifecycleFakeClient
	processCalls atomic.Int32
	testCalls    atomic.Int32
	notified     chan struct{}
}

func (c *scopedEpochRenewalFakeClient) ClaimExecutionEpoch(_ context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	c.processCalls.Add(1)
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation}, nil
}

func (c *scopedEpochRenewalFakeClient) ClaimTestExecutionEpoch(_ context.Context, _ string, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	c.testCalls.Add(1)
	if c.notified != nil {
		select {
		case c.notified <- struct{}{}:
		default:
		}
	}
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation}, nil
}

// A staged target is in no other renewal set until topology commit publishes it,
// and its assignment lease is both rejected by and clamped to the generator's
// process epoch. Renewing only the assignment scope here would silently delegate
// the staged process epoch to the controller-lifetime fleet keeper.
func TestStagedAssignmentLeaseKeeperRenewsProcessEpochAndAssignmentLease(t *testing.T) {
	now := time.Now().UTC()
	leadership := newExecutionLeadership(nil, "staged-scopes")
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = "staged-scopes/1"
	leadership.durableGeneration = 44
	leadership.localLeaseDeadline = now.Add(300 * time.Millisecond)
	leadership.mu.Unlock()
	client := &scopedEpochRenewalFakeClient{notified: make(chan struct{}, 8)}
	runner := &TestRunner{leadership: leadership}
	backend := &distributedExecutionBackend{timeout: 20 * time.Millisecond}
	testRun := &ManagedTest{ID: "staged-scopes-run"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		backend.maintainStagedGeneratorTestExecutionLeases(ctx, runner, testRun, 44, map[string]generatorClient{"new": client})
		close(done)
	}()
	waitForEpochTestSignal(t, client.notified, "staged assignment lease renewal")
	if got := client.processCalls.Load(); got < 1 {
		t.Fatalf("staged process execution epoch claims = %d, want at least 1 alongside the assignment lease", got)
	}

	backend.fenceTestExecutionLease(testRun, "new")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("staged lease keeper did not stop after its assignment was fenced")
	}
	// A fenced assignment must not have its process epoch refreshed by this
	// keeper either; the fleet keeper owns that target from here on.
	processAfterFence := client.processCalls.Load()
	time.Sleep(150 * time.Millisecond)
	if got := client.processCalls.Load(); got != processAfterFence {
		t.Fatalf("staged keeper renewed a fenced target's process epoch: %d -> %d", processAfterFence, got)
	}
}

func TestGeneratorExecutionEpochSessionExpiredMatchesOnlyLapsedProcessEpochs(t *testing.T) {
	lapsed := grpcStatus.Error(codes.FailedPrecondition, generatorrpc.ExecutionGenerationSessionExpiredMessage)
	wrapped := errors.Join(
		fmt.Errorf("renew generator gen-b execution generation: %w", lapsed),
		errors.New("unrelated"),
	)
	if !generatorExecutionEpochSessionExpired(wrapped) {
		t.Fatal("lapsed process epoch was not recognized through the fan-out wrappers")
	}
	mismatched := fmt.Errorf("renew generator gen-b execution generation: %w",
		grpcStatus.Errorf(codes.FailedPrecondition, "execution generation %d is not active; current generation is %d", 7, 8))
	if generatorExecutionEpochSessionExpired(mismatched) {
		t.Fatal("a generation mismatch was misreported as a lapsed process epoch")
	}
	if generatorExecutionEpochSessionExpired(nil) || generatorExecutionEpochSessionExpired(errors.New("plain")) {
		t.Fatal("non-gRPC errors were misreported as a lapsed process epoch")
	}
}

func TestStagedAssignmentLeaseKeeperHandsOffToSingleTopologyOwner(t *testing.T) {
	now := time.Now().UTC()
	leadership := newExecutionLeadership(nil, "staged-handoff")
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = "staged-handoff/1"
	leadership.durableGeneration = 42
	leadership.localLeaseDeadline = now.Add(600 * time.Millisecond)
	leadership.mu.Unlock()

	stagedClient := &epochRenewalFakeClient{notified: make(chan struct{}, 8)}
	liveClient := &epochRenewalFakeClient{notified: make(chan struct{}, 8)}
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	testRun := &ManagedTest{
		ID:                    "staged-handoff-run",
		executionCtx:          runCtx,
		executionGeneration:   42,
		remoteClientsByTarget: map[string]generatorClient{"new": liveClient},
	}
	runner := &TestRunner{
		tests:      map[string]*ManagedTest{testRun.ID: testRun},
		leadership: leadership,
	}
	backend := &distributedExecutionBackend{timeout: 20 * time.Millisecond}
	stagedCtx, cancelStaged := context.WithCancel(runCtx)
	stagedDone := make(chan struct{})
	go func() {
		backend.maintainStagedGeneratorTestExecutionLeases(stagedCtx, runner, testRun, 42, map[string]generatorClient{"new": stagedClient})
		close(stagedDone)
	}()
	waitForEpochTestSignal(t, stagedClient.notified, "initial staged lease renewal")

	backend.handoffStagedGeneratorTestExecutionLeaseKeeper(runCtx, runner, testRun, 42, cancelStaged)
	waitForEpochTestSignal(t, liveClient.notified, "topology lease renewal after handoff")
	select {
	case <-stagedDone:
	case <-time.After(time.Second):
		t.Fatal("staged lease keeper remained active after topology handoff")
	}
	stagedCalls := stagedClient.calls.Load()
	time.Sleep(250 * time.Millisecond)
	if calls := stagedClient.calls.Load(); calls != stagedCalls {
		t.Fatalf("staged lease keeper renewed after handoff: calls %d -> %d", stagedCalls, calls)
	}
}

func TestRenewGeneratorExecutionEpochIncludesEvacuationClients(t *testing.T) {
	now := time.Now().UTC()
	leadership := newExecutionLeadership(nil, "renew-test")
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = "renew-test/1"
	leadership.durableGeneration = 19
	leadership.localLeaseDeadline = now.Add(750 * time.Millisecond)
	leadership.mu.Unlock()

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &epochRenewalFakeClient{notified: make(chan struct{}, 2)}
	replacement := &epochRenewalFakeClient{notified: make(chan struct{}, 2)}
	testRun := &ManagedTest{
		ID:                    "renew-dynamic",
		executionCtx:          runCtx,
		executionGeneration:   19,
		remoteClientsByTarget: map[string]generatorClient{"initial": initial},
	}
	runner := &TestRunner{
		tests:      map[string]*ManagedTest{testRun.ID: testRun},
		leadership: leadership,
	}
	backend := &distributedExecutionBackend{timeout: 100 * time.Millisecond}
	done := make(chan struct{})
	go func() {
		backend.renewGeneratorExecutionEpoch(runCtx, runner, testRun, 19)
		close(done)
	}()
	select {
	case <-initial.notified:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("initial generator was not renewed")
	}

	runner.mu.Lock()
	testRun.remoteClientsByTarget["replacement"] = replacement
	runner.mu.Unlock()
	select {
	case <-replacement.notified:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("evacuation replacement generator was not included in renewal snapshots")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("renewal loop did not stop with the run context")
	}
}

func TestGeneratorEpochKeeperContinuesUnrelatedRunsAcrossTestFenceAndReactivation(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"generator-a:9090", "generator-b:9090"}, time.Second, nil)
	lostRun := &ManagedTest{ID: "lost-run"}
	healthyRun := &ManagedTest{ID: "healthy-run"}
	firstToken := backend.fenceTestExecutionLease(lostRun, "generator-a:9090")
	if got := backend.generatorEpochKeeperTargets(); !reflect.DeepEqual(got, []string{"generator-a:9090", "generator-b:9090"}) {
		t.Fatalf("process keeper targets after run fence = %#v", got)
	}
	if err := backend.SetGeneratorDrainState("generator-a:9090", "active"); err != nil {
		t.Fatalf("reactivate generator: %v", err)
	}
	if !backend.testExecutionLeaseFenced(lostRun, "generator-a:9090") {
		t.Fatal("fleet reactivation cleared the active run-scoped fence")
	}
	if backend.testExecutionLeaseFenced(healthyRun, "generator-a:9090") {
		t.Fatal("run-scoped fence leaked into an unrelated run")
	}
	if nextToken := backend.fenceTestExecutionLease(lostRun, "generator-a:9090"); nextToken <= firstToken {
		t.Fatalf("replacement fence token = %d, want newer than %d", nextToken, firstToken)
	}
}

func TestExistingRunPlacementExcludesItsPreviouslyFencedTargetOnly(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"old-source", "current-source", "candidate"}, time.Second, nil)
	runA := &ManagedTest{ID: "run-a"}
	runB := &ManagedTest{ID: "run-b"}
	backend.fenceTestExecutionLease(runA, "old-source")
	targets := []string{"old-source", "current-source", "candidate"}
	if got := backend.filterTestPlacementTargets(runA, targets); !reflect.DeepEqual(got, []string{"current-source", "candidate"}) {
		t.Fatalf("run-a placement targets = %#v", got)
	}
	if got := backend.filterTestPlacementTargets(runB, targets); !reflect.DeepEqual(got, targets) {
		t.Fatalf("unrelated run placement targets = %#v, want %#v", got, targets)
	}
	if err := backend.SetGeneratorDrainState("old-source", "active"); err != nil {
		t.Fatalf("reactivate old source: %v", err)
	}
	if got := backend.filterTestPlacementTargets(runA, targets); !reflect.DeepEqual(got, []string{"current-source", "candidate"}) {
		t.Fatalf("reactivation reintroduced fenced target: %#v", got)
	}
}

func TestStaleRunCleanupCannotClearReplacementIncarnationFence(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"generator"}, time.Second, nil)
	oldRun := &ManagedTest{ID: "reused-id"}
	newRun := &ManagedTest{ID: "reused-id"}
	backend.fenceTestExecutionLease(oldRun, "generator")
	backend.fenceTestExecutionLease(newRun, "generator")
	backend.clearTestExecutionLeaseFences(oldRun)
	if backend.testExecutionLeaseFenced(oldRun, "generator") {
		t.Fatal("old run fence survived its own cleanup")
	}
	if !backend.testExecutionLeaseFenced(newRun, "generator") {
		t.Fatal("stale old-run cleanup cleared replacement incarnation fence")
	}
}

func TestStaleEvacuationDetachClearsOnlyDetachedIncarnationFence(t *testing.T) {
	backend := newDistributedExecutionBackend(nil, 20*time.Millisecond, nil)
	oldRun := &ManagedTest{ID: "reused-evacuation-id"}
	newRun := &ManagedTest{ID: oldRun.ID}
	runner := &TestRunner{tests: map[string]*ManagedTest{newRun.ID: newRun}}
	backend.fenceTestExecutionLease(oldRun, "generator")
	backend.fenceTestExecutionLease(newRun, "generator")

	backend.detachEvacuationResources(runner, oldRun, map[string]generatorClient{}, map[string]*generatorrpc.MetricsStream{})
	if backend.testExecutionLeaseFenced(oldRun, "generator") {
		t.Fatal("stale evacuation detach retained its pointer-scoped fence")
	}
	if !backend.testExecutionLeaseFenced(newRun, "generator") {
		t.Fatal("stale evacuation detach cleared the replacement incarnation fence")
	}
}

func TestEvacuationDetachAcceptsNilCapturedResourceMaps(t *testing.T) {
	backend := newDistributedExecutionBackend(nil, 20*time.Millisecond, nil)
	testRun := &ManagedTest{
		ID:                           "terminal-evacuation",
		remoteClientsByTarget:        map[string]generatorClient{"generator": nil},
		remoteMetricsStreamsByTarget: map[string]*generatorrpc.MetricsStream{"generator": nil},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{testRun.ID: testRun}}

	backend.detachEvacuationResources(runner, testRun, nil, nil)

	if testRun.remoteClientsByTarget != nil || testRun.remoteMetricsStreamsByTarget != nil {
		t.Fatal("evacuation resources remained attached after nil-map cleanup")
	}
}

func TestRunEpochRenewalWaitsForReplacementAfterLossFencedParticipant(t *testing.T) {
	now := time.Now().UTC()
	leadership := newExecutionLeadership(nil, "loss-fence-test")
	leadership.mu.Lock()
	leadership.leader.Store(true)
	leadership.leaseOwnerID = "loss-fence-test/1"
	leadership.durableGeneration = 23
	leadership.localLeaseDeadline = now.Add(250 * time.Millisecond)
	leadership.mu.Unlock()

	client := &epochRenewalFakeClient{notified: make(chan struct{}, 1)}
	runCtx, cancel := context.WithCancel(context.Background())
	testRun := &ManagedTest{
		ID: "loss-fenced-run", executionCtx: runCtx, executionGeneration: 23,
		remoteClientsByTarget: map[string]generatorClient{"lost:9090": client},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{testRun.ID: testRun}, leadership: leadership}
	backend := newDistributedExecutionBackend([]string{"lost:9090"}, 20*time.Millisecond, nil)
	backend.fenceTestExecutionLease(testRun, "lost:9090")
	done := make(chan struct{})
	go func() {
		backend.renewGeneratorExecutionEpoch(runCtx, runner, testRun, 23)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if calls := client.calls.Load(); calls != 0 {
		t.Fatalf("loss-fenced participant received %d renewal calls", calls)
	}
	replacement := &epochRenewalFakeClient{notified: make(chan struct{}, 1)}
	runner.mu.Lock()
	testRun.remoteClientsByTarget["replacement:9090"] = replacement
	runner.mu.Unlock()
	select {
	case <-replacement.notified:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("run renewal did not take over the committed replacement")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run renewal did not stop after cancellation")
	}
}
