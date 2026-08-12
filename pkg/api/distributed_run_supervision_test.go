package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type runSupervisionControllerTestClient struct {
	generatorClient
	claim func(context.Context, generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error)
	stop  func(context.Context, string, bool, uint64, []byte) (generatorrpc.StopResult, error)
	close func() error
}

func (c *runSupervisionControllerTestClient) ClaimRunSupervision(ctx context.Context, claim generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
	return c.claim(ctx, claim)
}

func (c *runSupervisionControllerTestClient) StopTestAtGenerationWithToken(ctx context.Context, testID string, graceful bool, generation uint64, token []byte) (generatorrpc.StopResult, error) {
	return c.stop(ctx, testID, graceful, generation, token)
}

func (c *runSupervisionControllerTestClient) Close() error {
	if c.close != nil {
		return c.close()
	}
	return nil
}

func acceptedRunSupervisionClaim(ttl time.Duration) generatorrpc.RunSupervisionResult {
	return generatorrpc.RunSupervisionResult{
		Accepted:        true,
		ContractVersion: generatorrpc.RunSupervisionContractV1,
		GrantedTTL:      ttl,
	}
}

func TestClaimRunSupervisionClientsRejectsGrantedTTLBelowRenewalSafetyMargin(t *testing.T) {
	backend := &distributedExecutionBackend{
		requireRunSupervision: true,
		timeout:               time.Second,
		runSupervisionTTL:     60 * time.Second,
		runSupervisionRenew:   20 * time.Second,
	}
	client := &runSupervisionControllerTestClient{claim: func(context.Context, generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
		return acceptedRunSupervisionClaim(30 * time.Second), nil
	}}
	err := backend.claimRunSupervisionClients(context.Background(), "run", map[string]generatorClient{"generator": client}, 9, "owner", bytes.Repeat([]byte{1}, generatorrpc.RunSupervisionTokenSize))
	if err == nil || !strings.Contains(err.Error(), "below three renewal intervals") {
		t.Fatalf("claim error = %v, want granted-TTL safety rejection", err)
	}
}

func TestClaimRunSupervisionClientsAttemptsTargetsQueuedBehindBlockedWorkers(t *testing.T) {
	backend := &distributedExecutionBackend{
		requireRunSupervision: true,
		timeout:               5 * time.Second,
		runSupervisionTTL:     60 * time.Second,
		runSupervisionRenew:   800 * time.Millisecond,
	}
	clients := make(map[string]generatorClient, maxRunSupervisionClaimWorkers+1)
	for index := 0; index < maxRunSupervisionClaimWorkers; index++ {
		clients[fmt.Sprintf("a-%02d", index)] = &runSupervisionControllerTestClient{claim: func(ctx context.Context, _ generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
			<-ctx.Done()
			return generatorrpc.RunSupervisionResult{}, ctx.Err()
		}}
	}
	lastAttempted := make(chan struct{}, 1)
	clients["z-last"] = &runSupervisionControllerTestClient{claim: func(context.Context, generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
		lastAttempted <- struct{}{}
		return acceptedRunSupervisionClaim(60 * time.Second), nil
	}}

	done := make(chan error, 1)
	go func() {
		done <- backend.claimRunSupervisionClients(context.Background(), "run", clients, 3, "owner", bytes.Repeat([]byte{2}, generatorrpc.RunSupervisionTokenSize))
	}()
	select {
	case <-lastAttempted:
	case <-time.After(time.Second):
		t.Fatal("target queued behind blocked workers was never attempted within one renewal cadence")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked claims unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("bounded supervision fanout did not return")
	}
}

func TestClaimRunSupervisionClientsReturnsWhenClientIgnoresContext(t *testing.T) {
	backend := &distributedExecutionBackend{
		requireRunSupervision: true,
		timeout:               5 * time.Second,
		runSupervisionTTL:     60 * time.Second,
		runSupervisionRenew:   100 * time.Millisecond,
	}
	release := make(chan struct{})
	client := &runSupervisionControllerTestClient{claim: func(context.Context, generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
		<-release
		return acceptedRunSupervisionClaim(60 * time.Second), nil
	}}
	started := time.Now()
	err := backend.claimRunSupervisionClients(context.Background(), "run", map[string]generatorClient{"generator": client}, 1, "owner", bytes.Repeat([]byte{5}, generatorrpc.RunSupervisionTokenSize))
	elapsed := time.Since(started)
	close(release)
	if err == nil || !strings.Contains(err.Error(), "claim fanout") {
		t.Fatalf("claim error = %v, want bounded fanout deadline", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("context-ignoring claim blocked fanout for %s", elapsed)
	}
}

func TestRunSupervisionKeeperCleanupCannotClearReplacementKeeper(t *testing.T) {
	backend := &distributedExecutionBackend{requireRunSupervision: true, timeout: time.Second, runSupervisionTTL: 60 * time.Second, runSupervisionRenew: time.Hour}
	var token [generatorrpc.RunSupervisionTokenSize]byte
	copy(token[:], bytes.Repeat([]byte{3}, len(token)))
	test := &ManagedTest{ID: "run", Status: "RUNNING", remoteRunSupervisionToken: token}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}

	stopOld := backend.startRunSupervisionKeeper(context.Background(), runner, test, nil, 1, "owner", token)
	runner.mu.Lock()
	old := detachRunSupervisionKeeperLocked(test)
	runner.mu.Unlock()
	if err := stopAndWaitRunSupervisionKeeper(context.Background(), old); err != nil {
		t.Fatalf("stop old keeper: %v", err)
	}
	stopReplacement := backend.startRunSupervisionKeeper(context.Background(), runner, test, nil, 1, "owner", token)
	runner.mu.RLock()
	replacementGeneration := test.remoteRunSupervisionKeeperGeneration
	replacementInstalled := test.remoteRunSupervisionCancel != nil
	runner.mu.RUnlock()
	if !replacementInstalled {
		t.Fatal("replacement keeper was not installed")
	}

	stopOld()
	runner.mu.RLock()
	stillInstalled := test.remoteRunSupervisionCancel != nil && test.remoteRunSupervisionKeeperGeneration == replacementGeneration
	runner.mu.RUnlock()
	if !stillInstalled {
		t.Fatal("stale keeper cleanup cleared the replacement keeper")
	}
	stopReplacement()
}

func TestFenceRunSupervisionTargetFailsClosedWhenClaimWorkerIgnoresCancellation(t *testing.T) {
	backend := &distributedExecutionBackend{requireRunSupervision: true, timeout: time.Second, runSupervisionTTL: 60 * time.Second, runSupervisionRenew: 10 * time.Millisecond}
	var token [generatorrpc.RunSupervisionTokenSize]byte
	copy(token[:], bytes.Repeat([]byte{6}, len(token)))
	claimStarted := make(chan struct{}, 1)
	releaseClaim := make(chan struct{})
	client := &runSupervisionControllerTestClient{claim: func(context.Context, generatorrpc.RunSupervisionClaim) (generatorrpc.RunSupervisionResult, error) {
		claimStarted <- struct{}{}
		<-releaseClaim
		return acceptedRunSupervisionClaim(60 * time.Second), nil
	}}
	test := &ManagedTest{
		ID: "run", Status: "RUNNING", remoteRunSupervisionToken: token,
		remoteClientsByTarget: map[string]generatorClient{"source": client},
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}
	stopKeeper := backend.startRunSupervisionKeeper(context.Background(), runner, test, nil, 1, "owner", token)
	select {
	case <-claimStarted:
	case <-time.After(time.Second):
		t.Fatal("keeper did not begin the test renewal")
	}

	joinCtx, joinCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err := backend.fenceRunSupervisionTarget(joinCtx, runner, test, "source", 1, "owner")
	joinCancel()
	if err == nil || !strings.Contains(err.Error(), "join source run supervision renewals") {
		t.Fatalf("source fence error = %v, want bounded renewal-join failure", err)
	}
	runner.mu.RLock()
	replacementInstalled := test.remoteRunSupervisionCancel != nil
	runner.mu.RUnlock()
	if replacementInstalled {
		t.Fatal("source fence installed a replacement keeper after an unjoined renewal")
	}

	close(releaseClaim)
	stopKeeper()
}

func TestLeadershipLossUsesTokenFencedStopAndJoinsKeeper(t *testing.T) {
	var token [generatorrpc.RunSupervisionTokenSize]byte
	copy(token[:], bytes.Repeat([]byte{4}, len(token)))
	keeperCtx, keeperCancel := context.WithCancel(context.Background())
	keeperDone := make(chan struct{})
	go func() {
		<-keeperCtx.Done()
		close(keeperDone)
	}()

	var mu sync.Mutex
	var gotToken []byte
	var gotGeneration uint64
	client := &runSupervisionControllerTestClient{
		stop: func(_ context.Context, testID string, graceful bool, generation uint64, supplied []byte) (generatorrpc.StopResult, error) {
			if testID != "run" || graceful {
				return generatorrpc.StopResult{}, errors.New("unexpected stop request")
			}
			mu.Lock()
			gotToken = append([]byte(nil), supplied...)
			gotGeneration = generation
			mu.Unlock()
			return generatorrpc.StopResult{Acknowledged: true}, nil
		},
	}
	test := &ManagedTest{
		ID:                                   "run",
		Status:                               "RUNNING",
		executionGeneration:                  17,
		remoteClientsByTarget:                map[string]generatorClient{"generator": client},
		remoteRunSupervisionToken:            token,
		remoteRunSupervisionCancel:           keeperCancel,
		remoteRunSupervisionDone:             keeperDone,
		remoteRunSupervisionKeeperGeneration: 5,
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{test.ID: test}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.teardownActiveTestsAfterLeadershipLoss(ctx); err != nil {
		t.Fatalf("teardown leadership-loss runtime: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotGeneration != 17 || !bytes.Equal(gotToken, token[:]) {
		t.Fatalf("token-fenced stop = generation %d token %x, want generation 17 token %x", gotGeneration, gotToken, token)
	}
	select {
	case <-keeperDone:
	default:
		t.Fatal("leadership-loss teardown did not join the supervision keeper")
	}
	if test.remoteRunSupervisionCancel != nil || test.remoteRunSupervisionDone != nil || cloneRunSupervisionToken(test.remoteRunSupervisionToken) != nil {
		t.Fatal("leadership-loss teardown retained stale supervision ownership")
	}
}

func TestLeadershipLossStuckKeeperDoesNotSuppressTokenFencedStops(t *testing.T) {
	type observedStop struct {
		testID     string
		generation uint64
		token      []byte
	}
	stops := make(chan observedStop, 2)
	newClient := func() *runSupervisionControllerTestClient {
		return &runSupervisionControllerTestClient{stop: func(ctx context.Context, testID string, _ bool, generation uint64, token []byte) (generatorrpc.StopResult, error) {
			if ctx.Err() != nil {
				return generatorrpc.StopResult{}, fmt.Errorf("stop inherited expired leadership context: %w", ctx.Err())
			}
			if _, ok := ctx.Deadline(); !ok {
				return generatorrpc.StopResult{}, errors.New("leadership-loss stop is not deadline bounded")
			}
			stops <- observedStop{testID: testID, generation: generation, token: append([]byte(nil), token...)}
			return generatorrpc.StopResult{Acknowledged: true}, nil
		}}
	}
	var stuckToken, healthyToken [generatorrpc.RunSupervisionTokenSize]byte
	copy(stuckToken[:], bytes.Repeat([]byte{7}, len(stuckToken)))
	copy(healthyToken[:], bytes.Repeat([]byte{8}, len(healthyToken)))
	stuck := &ManagedTest{
		ID: "stuck", Status: "RUNNING", executionGeneration: 21,
		remoteClientsByTarget:      map[string]generatorClient{"stuck-generator": newClient()},
		remoteRunSupervisionToken:  stuckToken,
		remoteRunSupervisionCancel: func() {},
		remoteRunSupervisionDone:   make(chan struct{}),
	}
	healthyDone := make(chan struct{})
	close(healthyDone)
	healthy := &ManagedTest{
		ID: "healthy", Status: "RUNNING", executionGeneration: 22,
		remoteClientsByTarget:      map[string]generatorClient{"healthy-generator": newClient()},
		remoteRunSupervisionToken:  healthyToken,
		remoteRunSupervisionCancel: func() {},
		remoteRunSupervisionDone:   healthyDone,
	}
	runner := &TestRunner{tests: map[string]*ManagedTest{stuck.ID: stuck, healthy.ID: healthy}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := runner.teardownActiveTestsAfterLeadershipLoss(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("teardown error = %v, want stuck keeper deadline", err)
	}

	observed := make(map[string]observedStop, 2)
	for range 2 {
		select {
		case stop := <-stops:
			observed[stop.testID] = stop
		case <-time.After(time.Second):
			t.Fatal("stuck keeper suppressed a token-fenced leadership-loss stop")
		}
	}
	for testID, want := range map[string]struct {
		generation uint64
		token      []byte
	}{
		"stuck":   {generation: 21, token: stuckToken[:]},
		"healthy": {generation: 22, token: healthyToken[:]},
	} {
		got, ok := observed[testID]
		if !ok || got.generation != want.generation || !bytes.Equal(got.token, want.token) {
			t.Fatalf("stop for %s = %+v, want generation %d token %x", testID, got, want.generation, want.token)
		}
	}
}

func TestGeneratorLossToleranceRejectedBeforeNonHAStart(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{
		ID: "run", Status: "QUEUED",
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	err := runner.StartTest(test.ID)
	if err == nil || !strings.Contains(err.Error(), "requires HA execution leadership") {
		t.Fatalf("StartTest error = %v, want early non-HA generator-loss rejection", err)
	}
}

func TestGeneratorLossToleranceRejectedDuringNonHACreateBuild(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.mu.Lock()
	_, err := runner.buildManagedTestLocked(CreateTestRequest{
		Name: "unsafe", Duration: "1m",
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
	}, "run", "QUEUED")
	runner.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "requires HA execution leadership") {
		t.Fatalf("build error = %v, want early non-HA generator-loss rejection", err)
	}
}

func TestRunSupervisionProcessFenceDeadlineUsesMaximumAcceptedDrainContract(t *testing.T) {
	joined := time.Unix(100, 0)
	ttl := 30 * time.Second
	want := ttl + 10*time.Second + 5*time.Second + 2*time.Second + 250*time.Millisecond
	if got := runSupervisionProcessFenceDeadline(joined, ttl).Sub(joined); got != want {
		t.Fatalf("process-fence delay = %s, want %s", got, want)
	}
}
