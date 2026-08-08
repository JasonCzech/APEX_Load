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
	client, _ := newTestGeneratorRPCClient(t, "gen-lapsed", 4, generatorrpc.WithExecution())
	const (
		generation = uint64(9)
		owner      = "lapsed-epoch-probe/9"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	installed, err := client.ClaimExecutionEpoch(ctx, generation, owner, 30*time.Millisecond)
	if err != nil || !installed.Accepted {
		t.Fatalf("install execution epoch = (%+v, %v)", installed, err)
	}
	// Let the process-scoped session lapse without changing the generation: this
	// is exactly the state a staged replacement reaches when nothing renews it
	// during waitForLostGeneratorEpochFence.
	time.Sleep(90 * time.Millisecond)

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
