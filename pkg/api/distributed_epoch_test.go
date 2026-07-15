package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
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
	leadership.ttl = 150 * time.Millisecond
	leadership.renew = 25 * time.Millisecond
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

func TestRenewGeneratorExecutionClientsDoesNotSerializeSlowTargets(t *testing.T) {
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
		done <- backend.renewGeneratorExecutionClients(context.Background(), map[string]generatorClient{
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
