package generatorrpc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAdoptionPriorProofsRecoverInterruptedRotation(t *testing.T) {
	for _, rotated := range []bool{false, true} {
		t.Run(map[bool]string{false: "original generation", true: "partially adopted generation"}[rotated], func(t *testing.T) {
			s, original := startAdoptableRun(t, 45*time.Second, 5*time.Second)
			middle, final := supervisionTestToken(22), supervisionTestToken(23)
			s.mu.RLock()
			execution := s.executions["run"]["group"]
			s.mu.RUnlock()
			if rotated {
				ack := claimWithAdoptions(t, s, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: original, NewRunSupervisionToken: middle})
				if len(ack.GetAdoptions()) != 1 || ack.GetAdoptions()[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
					t.Fatal("initial rotation failed")
				}
			}
			client := supervisionHeartbeatClient(t, s)
			request := []RunAdoption{{TestID: "run", PriorGeneration: 1, PriorToken: original, NewToken: final, PriorProofs: []RunAdoptionProof{{Generation: 2, Token: middle}}}}
			for i := 0; i < 2; i++ {
				ack, err := client.ClaimExecutionEpochWithAdoptions(context.Background(), 3, "controller/3", 5*time.Second, request)
				if err != nil || !ack.Accepted || len(ack.Adoptions) != 1 || ack.Adoptions[0].Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
					t.Fatalf("adoption/retry failed: %+v %v", ack, err)
				}
			}
			s.mu.RLock()
			current := s.assignments["run"]
			sameExecution := s.executions["run"]["group"] == execution
			s.mu.RUnlock()
			if !sameExecution || current.executionGeneration != 3 || !sameRunSupervisionToken(current.runSupervisionTokenHash, final) {
				t.Fatal("adoption restarted work or lost ownership")
			}
			stale, err := s.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{TestId: "run", ExecutionGeneration: 3, ExecutionLeaseOwnerId: "controller/3", RunSupervisionToken: middle, RequestedTtlMilliseconds: 60000})
			if err != nil || stale.GetAccepted() {
				t.Fatal("interrupted token retained authority")
			}
		})
	}
}

func TestAdoptionPriorProofsRejectWrongTokenAndFirstClaimViolation(t *testing.T) {
	for _, mode := range []string{"wrong token", "omitted first claim", "future proof"} {
		t.Run(mode, func(t *testing.T) {
			s, original := startAdoptableRun(t, 45*time.Second, 5*time.Second)
			middle := supervisionTestToken(22)
			claimWithAdoptions(t, s, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: original, NewRunSupervisionToken: middle})
			proof := &pb.RunAdoptionProof{Generation: 2, RunSupervisionToken: middle}
			switch mode {
			case "wrong token":
				proof.RunSupervisionToken = supervisionTestToken(24)
			case "omitted first claim":
				claimWithAdoptions(t, s, 3)
			case "future proof":
				proof.Generation = 4
			}
			ack := claimWithAdoptions(t, s, 3, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: original, NewRunSupervisionToken: supervisionTestToken(23), PriorProofs: []*pb.RunAdoptionProof{proof}})
			if len(ack.GetAdoptions()) != 1 || ack.GetAdoptions()[0].GetOutcome() == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
				t.Fatal("unproven or fenced assignment adopted")
			}
		})
	}
}

func TestAdoptionPriorProofsValidateBoundAndDistinctGenerations(t *testing.T) {
	for _, mode := range []string{"nil", "zero generation", "duplicate primary", "duplicate proof", "short token", "new token", "too many"} {
		t.Run(mode, func(t *testing.T) {
			a := &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: supervisionTestToken(1), NewRunSupervisionToken: supervisionTestToken(9), PriorProofs: []*pb.RunAdoptionProof{{Generation: 2, RunSupervisionToken: supervisionTestToken(2)}}}
			p := a.PriorProofs[0]
			switch mode {
			case "nil":
				a.PriorProofs[0] = nil
			case "zero generation":
				p.Generation = 0
			case "duplicate primary":
				p.Generation = 1
			case "duplicate proof":
				a.PriorProofs = append(a.PriorProofs, p)
			case "short token":
				p.RunSupervisionToken = []byte{1}
			case "new token":
				p.RunSupervisionToken = a.NewRunSupervisionToken
			case "too many":
				for i := uint64(3); i <= 5; i++ {
					a.PriorProofs = append(a.PriorProofs, &pb.RunAdoptionProof{Generation: i, RunSupervisionToken: supervisionTestToken(byte(i))})
				}
			}
			if err := validateRunAdoptions([]*pb.RunAdoption{a}); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}

func TestAdoptionPriorProofsUseLatestRetainedTerminal(t *testing.T) {
	for _, state := range []string{"COMPLETE", "FAILED", "ABORTED"} {
		t.Run(state, func(t *testing.T) {
			s := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
			s.metricsBroker.publishTerminal(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 1, LifecycleState: "COMPLETE"})
			s.metricsBroker.publishTerminal(&pb.MetricsBatch{TestId: "run", ExecutionGeneration: 2, LifecycleState: state})
			a := &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: supervisionTestToken(1), NewRunSupervisionToken: supervisionTestToken(3), PriorProofs: []*pb.RunAdoptionProof{{Generation: 2, RunSupervisionToken: supervisionTestToken(2)}}}
			ack := claimWithAdoptions(t, s, 3, a)
			want := pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED
			if state == "COMPLETE" {
				want = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
			}
			if len(ack.GetAdoptions()) != 1 || ack.GetAdoptions()[0].GetOutcome() != want || ack.GetAdoptions()[0].GetTerminalState() != state {
				t.Fatalf("terminal classification=%+v", ack.GetAdoptions())
			}
		})
	}
}

func TestAdoptionPriorProofsAcceptMaximumWithoutMutatingOrder(t *testing.T) {
	a := &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: supervisionTestToken(1), NewRunSupervisionToken: supervisionTestToken(9)}
	for i := uint64(2); i <= MaxRunAdoptionPriorProofs; i++ {
		a.PriorProofs = append(a.PriorProofs, &pb.RunAdoptionProof{Generation: i, RunSupervisionToken: supervisionTestToken(byte(i))})
	}
	if err := validateRunAdoptions([]*pb.RunAdoption{a}); err != nil {
		t.Fatal(err)
	}
	proofs := adoptionPriorProofs(a)
	if len(proofs) != MaxRunAdoptionPriorProofs || proofs[0].Generation != MaxRunAdoptionPriorProofs || a.PriorProofs[0].Generation != 2 {
		t.Fatal("proof ordering mutated the request or lost the maximum boundary")
	}
}
