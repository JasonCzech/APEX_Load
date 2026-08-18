package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

// closeCountingEvacuationClient counts Close on a candidate that evacuation
// dials during placement. Placement dials every eligible candidate before it
// picks one, so the losers only ever leave through closeUntouchedDialedClients.
type closeCountingEvacuationClient struct {
	*generatorrpc.Client
	closes atomic.Int32
}

func (c *closeCountingEvacuationClient) Close() error {
	c.closes.Add(1)
	return c.Client.Close()
}

// postFenceFaultEvacuationClient reproduces, on demand, the two distinct shapes
// a generator uses to report one lapsed process-scoped execution epoch. The
// process-scoped claim answers with an accepted-false acknowledgement
// (planClaimLocked), while a generation-fenced mutation answers with a
// FailedPrecondition status (validateMutationLocked).
type postFenceFaultEvacuationClient struct {
	*generatorrpc.Client
	rejectProcessEpochClaim atomic.Bool
	expireAssignmentLease   atomic.Bool
}

func (c *postFenceFaultEvacuationClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, leaseOwnerID string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	if c.rejectProcessEpochClaim.Load() {
		return generatorrpc.ExecutionEpochResult{
			Accepted:          false,
			CurrentGeneration: generation,
			ErrorMessage:      generatorrpc.ExecutionGenerationExpiredMessage,
		}, nil
	}
	return c.Client.ClaimExecutionEpoch(ctx, generation, leaseOwnerID, ttl)
}

func (c *postFenceFaultEvacuationClient) ClaimTestExecutionEpoch(ctx context.Context, testID string, generation uint64, leaseOwnerID string, ttl time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	if c.expireAssignmentLease.Load() {
		return generatorrpc.ExecutionEpochResult{}, grpcStatus.Error(codes.FailedPrecondition, generatorrpc.ExecutionGenerationSessionExpiredMessage)
	}
	return c.Client.ClaimTestExecutionEpoch(ctx, testID, generation, leaseOwnerID, ttl)
}

// A lapsed process epoch is reported by the generator in two different shapes,
// and the process-scoped claim is the one an evacuation performs first. This
// pins the premise the post-fence diagnostics depend on: a claim rejection can
// never be mistaken for the assignment-lease status, and a same-generation claim
// is never revived in place, so a staged replacement whose epoch lapsed can only
// surface on the claim.
func TestLapsedProcessEpochRejectsTheClaimAndFencesTheAssignmentLeaseDifferently(t *testing.T) {
	// The epoch is installed with a comfortable TTL and lapsed by advancing a
	// controlled clock. A short TTL plus a sleep would reach the same state, but
	// only if the scheduler cooperates: under a loaded machine the epoch could
	// lapse before the claim that installed it was observed, failing at the
	// install rather than at the assertion below. That was FLAKE-1 — 2 failures
	// in 5 runs. See docs/qa/FLAKE_LOG.md.
	clock := newGeneratorTestClock(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC))
	client, _ := newTestGeneratorRPCClient(t, "gen-lapsed", 4,
		generatorrpc.WithExecution(),
		generatorrpc.WithClock(clock.Now),
	)
	const (
		generation = uint64(9)
		owner      = "lapsed-epoch-probe/9"
		epochTTL   = time.Minute
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	installed, err := client.ClaimExecutionEpoch(ctx, generation, owner, epochTTL)
	if err != nil || !installed.Accepted {
		t.Fatalf("install execution epoch = (%+v, %v)", installed, err)
	}
	// Let the process-scoped session lapse without changing the generation: this
	// is exactly the state a staged replacement reaches when nothing renews it
	// during waitForLostGeneratorEpochFence.
	clock.Advance(epochTTL + time.Second)

	deadline := time.Now().Add(2 * time.Second)
	claimErr := claimGeneratorExecutionEpoch(ctx, client, generation, owner, deadline, 500*time.Millisecond)
	if claimErr == nil {
		t.Fatal("the generator revived a lapsed session at its current generation")
	}
	if !strings.Contains(claimErr.Error(), generatorrpc.ExecutionGenerationExpiredMessage) {
		t.Fatalf("process-scoped re-claim error = %v, want the generation-expired rejection", claimErr)
	}
	if !generatorProcessExecutionEpochExpired(claimErr) {
		t.Fatalf("process-scoped re-claim rejection was not classified as a lapsed epoch: %v", claimErr)
	}
	if generatorExecutionEpochSessionExpired(claimErr) {
		t.Fatalf("a claim rejection was misclassified as the assignment-lease session-expired status: %v", claimErr)
	}

	leaseErr := claimGeneratorTestExecutionLease(ctx, client, "lapsed-epoch-run", generation, owner, deadline, 500*time.Millisecond)
	if leaseErr == nil {
		t.Fatal("an assignment lease was renewed against a lapsed process epoch")
	}
	if !generatorExecutionEpochSessionExpired(leaseErr) {
		t.Fatalf("assignment-lease error = %v, want the session-expired status", leaseErr)
	}
	if generatorProcessExecutionEpochExpired(leaseErr) {
		t.Fatalf("the assignment-lease status was misclassified as a claim rejection: %v", leaseErr)
	}
}

func TestGeneratorProcessExecutionEpochExpiredIgnoresOtherRejections(t *testing.T) {
	fenced := &generatorExecutionEpochRejection{
		generation:        7,
		currentGeneration: 8,
		message:           "execution generation 7 has been fenced by generation 8",
	}
	if generatorProcessExecutionEpochExpired(fenced) {
		t.Fatalf("a generation-fence rejection was reported as a lapsed epoch: %v", fenced)
	}
	if generatorProcessExecutionEpochExpired(nil) || generatorProcessExecutionEpochExpired(context.DeadlineExceeded) {
		t.Fatal("a non-rejection error was reported as a lapsed epoch")
	}
	lapsed := &generatorExecutionEpochRejection{
		generation:        7,
		currentGeneration: 7,
		message:           generatorrpc.ExecutionGenerationExpiredMessage,
	}
	wrapped := errors.Join(
		fmt.Errorf("renew generator gen-b process execution epoch: %w", lapsed),
		errors.New("unrelated"),
	)
	if !generatorProcessExecutionEpochExpired(wrapped) {
		t.Fatal("a lapsed epoch was not recognized through the fan-out wrappers")
	}
	if got, want := lapsed.Error(), "generator rejected execution generation 7 (current 7): "+generatorrpc.ExecutionGenerationExpiredMessage; got != want {
		t.Fatalf("rejection text = %q, want %q", got, want)
	}
}

// A fan-out joins one error per target, and the generator answers "lapsed",
// "fenced by a higher generation" and "a different controller owns the lease"
// with the same boxed rejection type, so the classifiers must look at every
// branch rather than the first one of that type. Both join orders describe the
// identical fleet state; only worker completion order differs.
func TestEpochClassifiersSeePastASiblingRejectionInEitherJoinOrder(t *testing.T) {
	lapsedClaim := fmt.Errorf("renew generator gen-z process execution epoch: %w", &generatorExecutionEpochRejection{
		generation:        9,
		currentGeneration: 9,
		message:           generatorrpc.ExecutionGenerationExpiredMessage,
	})
	fencedClaim := fmt.Errorf("renew generator gen-a process execution epoch: %w", &generatorExecutionEpochRejection{
		generation:        9,
		currentGeneration: 10,
		message:           "execution generation 9 has been fenced by generation 10",
	})
	ownerClaim := fmt.Errorf("renew generator gen-b process execution epoch: %w", &generatorExecutionEpochRejection{
		generation:        9,
		currentGeneration: 9,
		message:           "execution generation 9 is held by a different controller lease owner",
	})
	for name, joined := range map[string]error{
		"lapsed first":            errors.Join(lapsedClaim, fencedClaim, ownerClaim),
		"fenced first":            errors.Join(fencedClaim, lapsedClaim),
		"wrong lease owner first": errors.Join(ownerClaim, fencedClaim, lapsedClaim),
		"lapsed last":             errors.Join(fencedClaim, ownerClaim, lapsedClaim),
	} {
		if !generatorProcessExecutionEpochExpired(joined) {
			t.Errorf("%s: a lapsed process epoch was hidden behind a sibling rejection: %v", name, joined)
		}
	}
	if generatorProcessExecutionEpochExpired(errors.Join(fencedClaim, ownerClaim)) {
		t.Error("a tree without a lapsed rejection was reported as a lapsed epoch")
	}

	lapsedLease := fmt.Errorf("renew generator gen-z execution generation: %w",
		grpcStatus.Error(codes.FailedPrecondition, generatorrpc.ExecutionGenerationSessionExpiredMessage))
	unavailableLease := fmt.Errorf("renew generator gen-a execution generation: %w",
		grpcStatus.Error(codes.Unavailable, "connection refused"))
	mismatchedLease := fmt.Errorf("renew generator gen-b execution generation: %w",
		grpcStatus.Errorf(codes.FailedPrecondition, "execution generation %d is not active; current generation is %d", 9, 10))
	for name, joined := range map[string]error{
		"lapsed first":      errors.Join(lapsedLease, unavailableLease, mismatchedLease),
		"transport first":   errors.Join(unavailableLease, lapsedLease),
		"mismatched first":  errors.Join(mismatchedLease, unavailableLease, lapsedLease),
		"lapsed last":       errors.Join(unavailableLease, mismatchedLease, lapsedLease),
		"nested join first": errors.Join(errors.Join(unavailableLease, mismatchedLease), lapsedLease),
	} {
		if !generatorExecutionEpochSessionExpired(joined) {
			t.Errorf("%s: a lapsed epoch status was hidden behind a sibling status: %v", name, joined)
		}
	}
	if generatorExecutionEpochSessionExpired(errors.Join(unavailableLease, mismatchedLease)) {
		t.Error("a tree without a session-expired status was reported as a lapsed epoch")
	}
	if generatorProcessExecutionEpochExpired(nil) || generatorExecutionEpochSessionExpired(nil) {
		t.Error("a nil error was reported as a lapsed epoch")
	}
}

// staticEpochRejectionClient answers the process-scoped claim with one fixed
// accepted-false acknowledgement. Only the claim path is exercised, so the
// embedded interface stays nil. The optional delay pins the fan-out's
// completion order, which is otherwise whatever the workers happened to produce.
type staticEpochRejectionClient struct {
	generatorClient
	message string
	delay   time.Duration
}

func (c *staticEpochRejectionClient) ClaimExecutionEpoch(_ context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return generatorrpc.ExecutionEpochResult{
		Accepted:          false,
		CurrentGeneration: generation,
		ErrorMessage:      c.message,
	}, nil
}

// The post-fence re-claim (EvacuateGenerator) fans out over every staged
// replacement, so a two-replacement evacuation can join a fenced rejection ahead
// of the lapsed one. The keeper-fault attribution must not depend on which
// worker finished first. gen-a sorts first and answers immediately while gen-z
// sorts last and answers slowly, so the fenced rejection is joined ahead of the
// lapsed one under either scheduling of the two workers.
func TestPostFenceProcessEpochReclaimNamesALapsedTargetJoinedBehindAFencedOne(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-z"}, 2*time.Second, func(context.Context, string) (generatorClient, error) {
		return nil, context.DeadlineExceeded
	})
	clients := map[string]generatorClient{
		"gen-a": &staticEpochRejectionClient{message: "execution generation 9 has been fenced by generation 10"},
		"gen-z": &staticEpochRejectionClient{message: generatorrpc.ExecutionGenerationExpiredMessage, delay: 50 * time.Millisecond},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	epochErr := backend.claimGeneratorProcessExecutionEpochClients(ctx, clients, 9, "post-fence-evacuation/9", time.Now().Add(10*time.Second))
	if epochErr == nil {
		t.Fatal("two rejected process-epoch claims did not fail the re-claim")
	}
	if !strings.Contains(epochErr.Error(), generatorrpc.ExecutionGenerationExpiredMessage) {
		t.Fatalf("re-claim error = %v, want it to carry the generation-expired rejection", epochErr)
	}
	if !generatorProcessExecutionEpochExpired(epochErr) {
		t.Fatalf("the staged replacement's lapsed epoch was not attributed to the keeper because a fenced sibling was joined first: %v", epochErr)
	}
}

// Every post-fence fail-closed exit must release the candidates placement
// dialled but did not select, and must name the subsystem that actually failed.
// The unselected candidate is in neither touchedTargets nor the run's client
// map, so nothing downstream can ever close it.
func TestEvacuationPostFenceFailuresReleaseUnselectedCandidatesAndNameTheFault(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		arm             func(runner *TestRunner, replacement *postFenceFaultEvacuationClient)
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "leadership lost before replacement commit",
			arm: func(runner *TestRunner, _ *postFenceFaultEvacuationClient) {
				runner.leadership.leader.Store(false)
			},
			wantContains: []string{"execution leadership changed before replacement commit"},
		},
		{
			name: "staged process epoch lapsed during the fence",
			arm: func(_ *TestRunner, replacement *postFenceFaultEvacuationClient) {
				replacement.rejectProcessEpochClaim.Store(true)
			},
			wantContains: []string{
				"re-establish replacement process execution epoch after the source fence",
				"its process-scoped epoch was not renewed while staged",
			},
		},
		{
			name: "epoch lapsed between the re-claim and the assignment claim",
			arm: func(_ *TestRunner, replacement *postFenceFaultEvacuationClient) {
				replacement.expireAssignmentLease.Store(true)
			},
			wantContains: []string{
				"renew replacement assignment leases",
				"lapsed between the post-fence epoch re-claim and this assignment claim",
			},
			wantNotContains: []string{"was not renewed while staged"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")

			replacementBase, _ := newTestGeneratorRPCClient(t, "gen-b", 10, generatorrpc.WithExecution())
			// gen-c has capacity, so placement dials and epoch-claims it, but not
			// enough for the evacuated shard, so it always loses selection to gen-b.
			unselectedBase, _ := newTestGeneratorRPCClient(t, "gen-c", 1, generatorrpc.WithExecution())
			replacement := &postFenceFaultEvacuationClient{Client: replacementBase}
			unselected := &closeCountingEvacuationClient{Client: unselectedBase}

			source := newEvacuationFakeGeneratorClient("gen-a")
			backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b", "gen-c"}, 750*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
				switch target {
				case "gen-b":
					return replacement, nil
				case "gen-c":
					return unselected, nil
				default:
					return nil, context.DeadlineExceeded
				}
			})

			sut := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			t.Cleanup(sut.Close)
			script := &dsl.Script{
				ID:       "buyers-script",
				Name:     "Buyers",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: sut.URL},
				Actions:  []dsl.Action{{Name: "home", Type: "GET", Target: "/"}},
			}
			runCtx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			started := time.Now()
			test := &ManagedTest{
				ID:           "run-post-fence-" + strings.ReplaceAll(testCase.name, " ", "-"),
				Name:         "Post-fence fault",
				Status:       "RUNNING",
				Duration:     time.Minute,
				StartedAt:    &started,
				executionCtx: runCtx,
				cancel:       cancel,
				Assignment:   &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
				Groups:       []TestGroup{{Name: "buyers", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 2, script: script}},
				remoteGroupShards: map[string][]GeneratorShardSummary{
					"buyers": {{Group: "buyers", Target: "gen-a", VUsers: 2}},
				},
				remoteClientsByTarget:        map[string]generatorClient{"gen-a": source},
				remoteMetricsStreamsByTarget: make(map[string]*generatorrpc.MetricsStream),
				remoteMetricsBatchesByTarget: make(map[string]generatorrpc.MetricsBatch),
				remoteTargetStates:           map[string]string{"gen-a": "RUNNING"},
				remoteLifecycleCtx:           runCtx,
				remoteActivated:              true,
			}
			hub := NewHub()
			go hub.Run()
			t.Cleanup(hub.Stop)
			runner := NewTestRunner(1, hub, nil)
			runner.tests[test.ID] = test
			test.executionBackend = backend
			configureEvacuationExecutionLease(runner, test, 84, "post-fence-evacuation/84", 10*time.Second)

			// The source's own stop is the last controller step before the post-fence
			// block, so arming from its acknowledgement is deterministic.
			source.stopHook = func() { testCase.arm(runner, replacement) }

			_, err := backend.EvacuateGenerator(runner, test, "gen-a", false, "post-fence fault")
			if err == nil {
				t.Fatal("post-fence fault did not fail the evacuation closed")
			}
			// Report every violation: the leaked connection and the misattributed
			// diagnostic are independent defects on the same exit path.
			for _, want := range testCase.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("evacuation error = %v, want it to contain %q", err, want)
				}
			}
			for _, unwanted := range testCase.wantNotContains {
				if strings.Contains(err.Error(), unwanted) {
					t.Errorf("evacuation error = %v, must not misattribute with %q", err, unwanted)
				}
			}
			if got := unselected.closes.Load(); got != 1 {
				t.Errorf("dialled-but-unselected candidate close count = %d, want 1", got)
			}
			runner.mu.RLock()
			_, retained := test.remoteClientsByTarget["gen-c"]
			_, archived := test.remoteArchiveClientsByTarget["gen-c"]
			runner.mu.RUnlock()
			if retained || archived {
				t.Fatal("the unselected candidate was published into the run topology")
			}

			cancel()
			backend.closeRemoteResources(runner, test)
		})
	}
}
