package generatorrpc

import (
	"sort"
	"strings"

	pb "github.com/jasonczech/apexload/api/proto"
)

// The durable controller intent journal and the wire request share this bound.
// A run with a longer unconfirmed takeover chain fails closed rather than growing
// credential history without limit.
const MaxRunAdoptionPriorProofs = 4

func adoptionPriorProofs(adoption *pb.RunAdoption) []*pb.RunAdoptionProof {
	proofs := make([]*pb.RunAdoptionProof, 0, 1+len(adoption.GetPriorProofs()))
	proofs = append(proofs, &pb.RunAdoptionProof{Generation: adoption.GetPriorGeneration(), RunSupervisionToken: adoption.GetPriorRunSupervisionToken()})
	proofs = append(proofs, adoption.GetPriorProofs()...)
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].GetGeneration() > proofs[j].GetGeneration() })
	return proofs
}

// Select only an exact generation/token pair. The ordinary adoption path still
// checks supervision, stop state, grace, capacity lineage and the first-claim
// boundary. No alternative proof permits restarting work or extending its grace.
// Caller holds s.mu and has validated the bounded request.
func (s *Service) selectAdoptionPriorLocked(adoption *pb.RunAdoption) *pb.RunAdoption {
	proofs := adoptionPriorProofs(adoption)
	testID := strings.TrimSpace(adoption.GetTestId())
	var chosen *pb.RunAdoptionProof
	if assignment, ok := s.assignments[testID]; ok {
		for _, proof := range proofs {
			if proof.GetGeneration() == assignment.executionGeneration {
				chosen = proof
				break
			}
		}
	} else {
		for _, proof := range proofs {
			_, terminal := s.metricsBroker.terminalState(testID, proof.GetGeneration())
			tombstone := s.hasRunSupervisionTombstoneLocked(runSupervisionTombstoneKey{testID: testID, generation: proof.GetGeneration(), tokenHash: runSupervisionTokenHash(proof.GetRunSupervisionToken())})
			if terminal || tombstone {
				chosen = proof
				break
			}
		}
	}
	if chosen == nil {
		return adoption
	}
	return &pb.RunAdoption{TestId: testID, PriorGeneration: chosen.GetGeneration(), PriorRunSupervisionToken: chosen.GetRunSupervisionToken(), NewRunSupervisionToken: adoption.GetNewRunSupervisionToken()}
}
