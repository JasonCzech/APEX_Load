package generatorrpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultRunSupervisionTTL       = 60 * time.Second
	minimumRunSupervisionTTL       = 30 * time.Second
	defaultRunSupervisionDrain     = 5 * time.Second
	defaultRunSupervisionHardGrace = time.Second
	// maxAdoptionGrace bounds how long an orphaned run may keep generating load
	// with no controller. It is the hard ceiling on unsupervised load for a
	// continue-policy run; operators can only lower it.
	maxAdoptionGrace              = 120 * time.Second
	adoptionTimeoutReason         = "controller_adoption_timeout"
	envGeneratorMaxAdoptionGrace  = "APEXLOAD_GENERATOR_MAX_ADOPTION_GRACE"
	maxRunSupervisionTombstones   = 4096
	runSupervisionFailureReason   = "controller_supervision_lost"
	stopEscalationReason          = "controller_stop_unquiesced"
	fenceEscalationReason         = "execution_fence_unquiesced"
	shutdownEscalationReason      = "generator_shutdown_unquiesced"
	testFailureEscalationReason   = "test_failure_unquiesced"
	durationEscalationReason      = "duration_completion_unquiesced"
	defaultEscalationReason       = "generator_execution_unquiesced"
	runSupervisionCrashVersion    = 1
	runSupervisionJournalVersion  = 1
	maxRunSupervisionCrashEntries = 64
	maxRunSupervisionCrashBytes   = 64 << 10
)

const runSupervisionRestartFenceMessage = "generation-zero run supervision is durably fenced; a positive execution generation is required"

type runSupervisionLease struct {
	tokenHash  [sha256.Size]byte
	generation uint64
	ttl        time.Duration
	deadline   time.Time
	state      pb.RunSupervisionState
	timer      *time.Timer
	timerToken uint64
}

type runSupervisionTombstoneKey struct {
	testID     string
	generation uint64
	tokenHash  [sha256.Size]byte
}

type SelfTerminationRequest struct {
	TestID              string
	ExecutionGeneration uint64
	Reason              string
	Outstanding         int
	RequestedAt         time.Time
}

type RunSupervisionConfig struct {
	TTL   time.Duration
	Drain time.Duration
	// MaxAdoptionGrace caps the adoption grace a controller may request for a
	// continue-policy run. Zero disables orphaning on this generator.
	MaxAdoptionGrace time.Duration
}

// runSupervisionCrashIntent is one deliberately token-free journal entry. The
// process-level safety backstop is notified first, then the bounded journal is
// replaced while holding the state-store lifetime lock. A replacement generator
// can therefore explain multiple exits, while a wedged durable sync still cannot
// defeat the +2s exit bound.
type runSupervisionCrashIntent struct {
	Version             int       `json:"version"`
	Reason              string    `json:"reason"`
	TestID              string    `json:"test_id"`
	ExecutionGeneration uint64    `json:"execution_generation"`
	Outstanding         int       `json:"outstanding_executions"`
	RequestedAt         time.Time `json:"requested_at"`
	Reconciled          bool      `json:"reconciled,omitempty"`
}

type runSupervisionCrashJournal struct {
	Version int                         `json:"version"`
	Records []runSupervisionCrashIntent `json:"records"`
}

type runSupervisionCrashRecordKey struct {
	testID              string
	executionGeneration uint64
	requestedAt         string
}

func runSupervisionCrashKey(record runSupervisionCrashIntent) runSupervisionCrashRecordKey {
	return runSupervisionCrashRecordKey{
		testID:              record.TestID,
		executionGeneration: record.ExecutionGeneration,
		requestedAt:         record.RequestedAt.UTC().Format(time.RFC3339Nano),
	}
}

func RunSupervisionConfigFromEnv(getenv func(string) string) (RunSupervisionConfig, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	config := RunSupervisionConfig{TTL: defaultRunSupervisionTTL, Drain: defaultRunSupervisionDrain, MaxAdoptionGrace: maxAdoptionGrace}
	if raw := strings.TrimSpace(getenv(envGeneratorMaxAdoptionGrace)); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return RunSupervisionConfig{}, fmt.Errorf("%s: %w", envGeneratorMaxAdoptionGrace, err)
		}
		if parsed < 0 || parsed > maxAdoptionGrace {
			return RunSupervisionConfig{}, fmt.Errorf("%s must be between 0s and %s", envGeneratorMaxAdoptionGrace, maxAdoptionGrace)
		}
		config.MaxAdoptionGrace = parsed
	}
	if raw := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return RunSupervisionConfig{}, fmt.Errorf("APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL: %w", err)
		}
		config.TTL = parsed
	}
	if raw := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_ABANDON_DRAIN")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return RunSupervisionConfig{}, fmt.Errorf("APEXLOAD_GENERATOR_ABANDON_DRAIN: %w", err)
		}
		config.Drain = parsed
	}
	if config.TTL < minimumRunSupervisionTTL || config.TTL > defaultRunSupervisionTTL {
		return RunSupervisionConfig{}, fmt.Errorf("run supervision ttl must be between %s and %s", minimumRunSupervisionTTL, defaultRunSupervisionTTL)
	}
	if config.Drain < 0 || config.Drain > 10*time.Second || config.Drain >= config.TTL {
		return RunSupervisionConfig{}, fmt.Errorf("abandon drain must be between 0s and 10s and shorter than the supervision ttl")
	}
	return config, nil
}

func (s *Service) SelfTerminationRequested() <-chan SelfTerminationRequest {
	if s == nil {
		return nil
	}
	return s.selfTermination
}

func validRunSupervisionToken(token []byte) bool {
	if len(token) != RunSupervisionTokenSize {
		return false
	}
	var nonzero byte
	for _, value := range token {
		nonzero |= value
	}
	return nonzero != 0
}

func runSupervisionTokenHash(token []byte) [sha256.Size]byte {
	if len(token) == 0 {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(token)
}

func sameRunSupervisionToken(hash [sha256.Size]byte, token []byte) bool {
	if !validRunSupervisionToken(token) {
		return false
	}
	candidate := runSupervisionTokenHash(token)
	return subtle.ConstantTimeCompare(hash[:], candidate[:]) == 1
}

func sameRunSupervisionTokenHash(left, right [sha256.Size]byte) bool {
	return sameSHA256Digest(left, right)
}

func sameSHA256Digest(left, right [sha256.Size]byte) bool {
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

func emptyRunSupervisionTokenHash(hash [sha256.Size]byte) bool {
	var zero [sha256.Size]byte
	return sameRunSupervisionTokenHash(hash, zero)
}

func (s *Service) ClaimRunSupervision(ctx context.Context, claim *pb.RunSupervisionClaim) (*pb.RunSupervisionAck, error) {
	if claim == nil || strings.TrimSpace(claim.GetTestId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if !validRunSupervisionToken(claim.GetRunSupervisionToken()) {
		return nil, status.Errorf(codes.InvalidArgument, "run_supervision_token must contain exactly %d non-zero bytes", RunSupervisionTokenSize)
	}
	requestedTTLMilliseconds := claim.GetRequestedTtlMilliseconds()
	// Validate the wire integer before multiplying it into time.Duration. An
	// attacker-controlled int64 can otherwise wrap modulo Duration and land back
	// inside the 30–60s acceptance window.
	if requestedTTLMilliseconds < minimumRunSupervisionTTL.Milliseconds() ||
		requestedTTLMilliseconds > defaultRunSupervisionTTL.Milliseconds() {
		return nil, status.Errorf(codes.InvalidArgument, "requested run supervision ttl must be between %s and %s", minimumRunSupervisionTTL, defaultRunSupervisionTTL)
	}
	requestedTTL := time.Duration(requestedTTLMilliseconds) * time.Millisecond
	if ctx != nil && ctx.Err() != nil {
		return nil, status.FromContextError(ctx.Err()).Err()
	}

	testID := strings.TrimSpace(claim.GetTestId())
	tokenHash := runSupervisionTokenHash(claim.GetRunSupervisionToken())
	s.mu.Lock()
	if ctx != nil && ctx.Err() != nil {
		s.mu.Unlock()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if s.draining {
		s.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "generator is draining")
	}
	currentGeneration := s.executionEpochManager.executionGeneration
	epochNow := time.Now()
	if claim.GetExecutionGeneration() == 0 {
		if currentGeneration != 0 || strings.TrimSpace(claim.GetExecutionLeaseOwnerId()) != "" {
			s.mu.Unlock()
			return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "generation-zero supervision is fenced"), nil
		}
	} else if claim.GetExecutionGeneration() != currentGeneration ||
		strings.TrimSpace(claim.GetExecutionLeaseOwnerId()) == "" ||
		claim.GetExecutionLeaseOwnerId() != s.executionEpochManager.executionLeaseOwner ||
		s.executionEpochManager.executionExpiresAt.IsZero() || !epochNow.Before(s.executionEpochManager.executionExpiresAt) {
		s.mu.Unlock()
		return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "execution generation or lease owner is not current"), nil
	}
	tombstoneKey := runSupervisionTombstoneKey{testID: testID, generation: claim.GetExecutionGeneration(), tokenHash: tokenHash}
	if s.hasRunSupervisionTombstoneLocked(tombstoneKey) {
		s.mu.Unlock()
		return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "run supervision lease has expired and cannot be revived"), nil
	}
	if lease := s.runSupervisions[testID]; lease != nil &&
		lease.generation == claim.GetExecutionGeneration() && sameRunSupervisionTokenHash(lease.tokenHash, tokenHash) {
		if lease.state == pb.RunSupervisionState_RUN_SUPERVISION_STATE_STOPPING ||
			lease.state == pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED {
			s.mu.Unlock()
			return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "run supervision is stopping and cannot be revived"), nil
		}
		if !time.Now().Before(lease.deadline) {
			// The timestamp is the semantic deadline; AfterFunc delivery is only a
			// scheduling aid. Reject under the same lock that a renewal would use,
			// then synchronously drive the already-expired incarnation into fencing.
			timerToken := lease.timerToken
			s.mu.Unlock()
			s.expireRunSupervision(testID, timerToken)
			return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "run supervision lease has expired and cannot be revived"), nil
		}
	}
	assignment, ok := s.assignments[testID]
	if !ok || assignment.executionGeneration != claim.GetExecutionGeneration() {
		s.mu.Unlock()
		return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_UNSPECIFIED, "test assignment is unavailable"), nil
	}
	if assignment.requireTestExecutionLease &&
		!s.testExecutionLeaseCurrentLocked(testID, assignment.executionGeneration, time.Now()) {
		s.mu.Unlock()
		return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "test assignment lease is not current"), nil
	}
	if !sameRunSupervisionTokenHash(assignment.runSupervisionTokenHash, tokenHash) {
		s.mu.Unlock()
		return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "run supervision token does not match the assignment"), nil
	}
	if claim.GetExecutionGeneration() == 0 && s.executionEpochManager.runSupervisionRestartFenceInherited {
		lease := s.runSupervisions[testID]
		// An unrelated active generation-zero run was armed before commit and may
		// keep renewing. Staged or reconstructed work may not cross a start boundary
		// after this process inherited or observed a durable fence.
		if lease == nil || !assignment.committed || lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE ||
			lease.generation != 0 || !sameRunSupervisionTokenHash(lease.tokenHash, tokenHash) || !time.Now().Before(lease.deadline) {
			s.mu.Unlock()
			return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, runSupervisionRestartFenceMessage), nil
		}
	}
	if !s.executionEpochManager.runSupervisionRestartFenceArmed ||
		s.executionEpochManager.runSupervisionRestartFenceDisarming ||
		s.executionEpochManager.executionEpochStateErr != nil {
		s.mu.Unlock()
		if err := s.armRunSupervisionRestartFence(assignment.executionGeneration); err != nil {
			return nil, err
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		// Assignment, epoch, token, deadline, and the inherited generation-zero
		// fence may all have changed while durable I/O ran without Service.mu.
		return s.ClaimRunSupervision(ctx, claim)
	}
	grantedTTL := min(requestedTTL, s.runSupervisionTTL)
	if grantedTTL < minimumRunSupervisionTTL {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "generator run supervision ttl is below the safe minimum")
	}
	state := pb.RunSupervisionState_RUN_SUPERVISION_STATE_STAGED
	if assignment.committed {
		state = pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE
	}
	lease := s.runSupervisions[testID]
	if lease == nil {
		lease = &runSupervisionLease{tokenHash: tokenHash, generation: assignment.executionGeneration}
		s.runSupervisions[testID] = lease
	} else if lease.generation != assignment.executionGeneration || !sameRunSupervisionTokenHash(lease.tokenHash, tokenHash) {
		s.mu.Unlock()
		return rejectedRunSupervision(currentGeneration, pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED, "another run incarnation owns supervision"), nil
	}
	lease.ttl = grantedTTL
	lease.state = state
	s.resetRunSupervisionTimerLocked(testID, lease, time.Now().Add(grantedTTL))
	ack := &pb.RunSupervisionAck{
		Accepted: true, ContractVersion: RunSupervisionContractV1,
		CurrentGeneration: currentGeneration, GrantedTtlMilliseconds: grantedTTL.Milliseconds(),
		ExpiresAtUnixMs: lease.deadline.UnixMilli(), State: state,
	}
	s.mu.Unlock()
	return ack, nil
}

func rejectedRunSupervision(generation uint64, state pb.RunSupervisionState, message string) *pb.RunSupervisionAck {
	return &pb.RunSupervisionAck{
		Accepted: false, ContractVersion: RunSupervisionContractV1,
		CurrentGeneration: generation, State: state, ErrorMessage: message,
	}
}

func (s *Service) resetRunSupervisionTimerLocked(testID string, lease *runSupervisionLease, deadline time.Time) {
	if lease.timer != nil {
		lease.timer.Stop()
	}
	lease.timerToken++
	if lease.timerToken == 0 {
		lease.timerToken++
	}
	lease.deadline = deadline
	token := lease.timerToken
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	lease.timer = time.AfterFunc(delay, func() { s.expireRunSupervision(testID, token) })
}

func (s *Service) validateRunSupervisionCommitLocked(request *pb.WorkCommitRequest, now time.Time) (*runSupervisionLease, error) {
	assignment, ok := s.assignments[request.GetTestId()]
	if !ok {
		return nil, nil
	}
	if emptyRunSupervisionTokenHash(assignment.runSupervisionTokenHash) {
		if s.runSupervisionRequired {
			return nil, status.Error(codes.FailedPrecondition, "run supervision is required")
		}
		return nil, nil
	}
	if !sameRunSupervisionToken(assignment.runSupervisionTokenHash, request.GetRunSupervisionToken()) {
		return nil, status.Error(codes.FailedPrecondition, "run supervision token does not match the assignment")
	}
	if assignment.executionGeneration == 0 && s.executionEpochManager.runSupervisionRestartFenceInherited && !assignment.committed {
		return nil, status.Error(codes.FailedPrecondition, runSupervisionRestartFenceMessage)
	}
	lease := s.runSupervisions[request.GetTestId()]
	if lease == nil || lease.generation != assignment.executionGeneration ||
		!sameRunSupervisionTokenHash(lease.tokenHash, assignment.runSupervisionTokenHash) ||
		(lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_STAGED && lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE) ||
		!now.Before(lease.deadline) {
		return nil, status.Error(codes.FailedPrecondition, "a live run supervision claim is required before commit")
	}
	return lease, nil
}

func (s *Service) armRunSupervisionLocked(testID string, lease *runSupervisionLease, now time.Time) {
	if lease == nil {
		return
	}
	lease.state = pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE
	s.resetRunSupervisionTimerLocked(testID, lease, now.Add(lease.ttl))
}

func (s *Service) validateRunMutationToken(testID string, generation uint64, token []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.validateRunMutationTokenLocked(testID, generation, token)
}

func (s *Service) validateRunMutationTokenLocked(testID string, generation uint64, token []byte) error {
	assignment, ok := s.assignments[testID]
	if !ok {
		if lease := s.runSupervisions[testID]; lease != nil && lease.generation == generation &&
			!emptyRunSupervisionTokenHash(lease.tokenHash) {
			if sameRunSupervisionToken(lease.tokenHash, token) {
				return nil
			}
			return status.Error(codes.FailedPrecondition, "run supervision token does not match the stopping assignment")
		}
		candidate := runSupervisionTombstoneKey{testID: testID, generation: generation, tokenHash: runSupervisionTokenHash(token)}
		if validRunSupervisionToken(token) && s.hasRunSupervisionTombstoneLocked(candidate) {
			return nil
		}
		if s.hasAnyRunSupervisionTombstoneLocked(testID, generation) {
			return status.Error(codes.FailedPrecondition, "run supervision token does not match the terminal assignment")
		}
		if s.runSupervisionRequired {
			if stoppingGeneration, stopping := s.lifecycleCoordinator.stoppingGenerations[testID]; stopping && stoppingGeneration == generation {
				return status.Error(codes.FailedPrecondition, "run supervision token is required for the stopping assignment")
			}
			if terminalGeneration, terminal := s.terminalGenerationLocked(testID); terminal && terminalGeneration == generation {
				return status.Error(codes.FailedPrecondition, "run supervision token is required for the terminal assignment")
			}
		}
		return nil
	}
	if emptyRunSupervisionTokenHash(assignment.runSupervisionTokenHash) {
		if s.runSupervisionRequired {
			return status.Error(codes.FailedPrecondition, "run supervision is required")
		}
		return nil
	}
	if !sameRunSupervisionToken(assignment.runSupervisionTokenHash, token) {
		return status.Error(codes.FailedPrecondition, "run supervision token does not match the assignment")
	}
	return nil
}

// validateLiveRunMutationLocked performs identity and semantic-deadline checks
// in the same critical section as a control plan or commit. A timer callback is
// only a scheduling aid; it must never create a renewal/control window after
// the stored timestamp has elapsed.
func (s *Service) validateLiveRunMutationLocked(testID string, token []byte, now time.Time) error {
	assignment, ok := s.assignments[testID]
	if !ok {
		return status.Error(codes.NotFound, "test assignment not found")
	}
	if emptyRunSupervisionTokenHash(assignment.runSupervisionTokenHash) {
		if s.runSupervisionRequired {
			return status.Error(codes.FailedPrecondition, "run supervision is required")
		}
		return nil
	}
	if !sameRunSupervisionToken(assignment.runSupervisionTokenHash, token) {
		return status.Error(codes.FailedPrecondition, "run supervision token does not match the assignment")
	}
	lease := s.runSupervisions[testID]
	if lease == nil || lease.generation != assignment.executionGeneration ||
		!sameRunSupervisionTokenHash(lease.tokenHash, assignment.runSupervisionTokenHash) ||
		lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE ||
		!now.Before(lease.deadline) {
		return status.Error(codes.FailedPrecondition, "a live unexpired run supervision lease is required")
	}
	return nil
}

func (s *Service) retainRunSupervisionDrainLocked(testID string, assignment assignedTest) {
	if !emptyRunSupervisionTokenHash(assignment.runSupervisionTokenHash) {
		s.addRunSupervisionTombstoneLocked(runSupervisionTombstoneKey{
			testID: testID, generation: assignment.executionGeneration, tokenHash: assignment.runSupervisionTokenHash,
		})
	}
	if lease := s.runSupervisions[testID]; lease != nil &&
		lease.generation == assignment.executionGeneration &&
		sameRunSupervisionTokenHash(lease.tokenHash, assignment.runSupervisionTokenHash) {
		if lease.timer != nil {
			lease.timer.Stop()
		}
		lease.state = pb.RunSupervisionState_RUN_SUPERVISION_STATE_STOPPING
	}
	s.runSupervisionDrainingVUsers += int64(reservedAssignmentVUsers(assignment))
}

func (s *Service) releaseRunSupervisionDrain(stop detachedFencedTest) {
	s.mu.Lock()
	s.runSupervisionDrainingVUsers -= int64(stop.capacityDrainVUsers)
	if s.runSupervisionDrainingVUsers < 0 {
		s.runSupervisionDrainingVUsers = 0
	}
	if lease := s.runSupervisions[stop.testID]; lease != nil &&
		lease.generation == stop.assignment.executionGeneration &&
		sameRunSupervisionTokenHash(lease.tokenHash, stop.assignment.runSupervisionTokenHash) {
		s.clearRunSupervisionLocked(stop.testID)
	}
	s.mu.Unlock()
	s.maybeDisarmRunSupervisionRestartFence(stop.assignment.executionGeneration)
}

// armRunSupervisionRestartFence persists a fail-closed restart marker
// before acknowledging the first lease in a generation. A crash at any later
// point therefore cannot reconstruct pre-crash traffic, including during the
// cooperative drain between semantic expiry and archive finalization.
func (s *Service) armRunSupervisionRestartFence(generation uint64) error {
	s.runSupervisionFencePersistenceMu.Lock()
	defer s.runSupervisionFencePersistenceMu.Unlock()

	s.mu.Lock()
	manager := s.executionEpochManager
	if manager == nil || manager.executionEpochState == nil {
		s.mu.Unlock()
		return status.Error(codes.FailedPrecondition, "durable execution epoch state is unavailable")
	}
	if manager.executionEpochStateErr != nil {
		err := manager.executionEpochStateErr
		s.mu.Unlock()
		return status.Errorf(codes.FailedPrecondition, "durable execution epoch state: %v", err)
	}
	if generation != manager.executionGeneration {
		current := manager.executionGeneration
		s.mu.Unlock()
		return status.Errorf(codes.FailedPrecondition, "execution generation %d is not active; current generation is %d", generation, current)
	}
	if manager.runSupervisionRestartFenceArmed {
		s.mu.Unlock()
		return nil
	}
	state := manager.executionEpochState
	s.mu.Unlock()

	persistErr := state.persistRunSupervisionFence(generation, true)
	s.mu.Lock()
	defer s.mu.Unlock()
	manager = s.executionEpochManager
	if persistErr != nil {
		manager.executionEpochStateErr = persistErr
		manager.notifyReadinessChanged()
		return status.Errorf(codes.FailedPrecondition, "persist run supervision restart fence: %v", persistErr)
	}
	if manager.executionGeneration != generation {
		return status.Errorf(codes.FailedPrecondition, "execution generation %d changed while run supervision was armed", generation)
	}
	manager.runSupervisionRestartFenceArmed = true
	return nil
}

func (s *Service) markRunSupervisionGenerationExpiredLocked(generation uint64) {
	manager := s.executionEpochManager
	if manager == nil || generation != manager.executionGeneration {
		return
	}
	manager.runSupervisionGenerationExpired = true
	if generation == 0 {
		manager.runSupervisionRestartFenceInherited = true
	}
}

func (s *Service) maybeDisarmRunSupervisionRestartFence(generation uint64) {
	s.runSupervisionFencePersistenceMu.Lock()
	defer s.runSupervisionFencePersistenceMu.Unlock()

	s.mu.Lock()
	manager := s.executionEpochManager
	if manager == nil || generation != manager.executionGeneration || !manager.runSupervisionRestartFenceArmed ||
		manager.runSupervisionRestartFenceDisarming || manager.executionEpochStateErr != nil ||
		manager.runSupervisionRestartFenceInherited || manager.runSupervisionGenerationExpired ||
		s.hasSupervisedRunWorkLocked(generation) {
		s.mu.Unlock()
		return
	}
	if manager.executionEpochState == nil {
		s.mu.Unlock()
		return
	}
	// Claims must not rely on the old in-memory armed value while the durable bit
	// is temporarily clear. A staged assignment may appear while the write is in
	// flight; its claim waits on the persistence mutex and this method restores the
	// bit before that claim can be acknowledged.
	manager.runSupervisionRestartFenceDisarming = true
	state := manager.executionEpochState
	s.mu.Unlock()

	if err := state.persistRunSupervisionFence(generation, false); err != nil {
		// A rename may have made the cleared bit visible even if directory sync then
		// failed. Compensate by writing the safe value again before allowing another
		// supervised claim to reuse the in-memory armed state.
		rearmErr := state.persistRunSupervisionFence(generation, true)
		s.mu.Lock()
		manager = s.executionEpochManager
		if manager != nil && manager.executionGeneration == generation {
			manager.runSupervisionRestartFenceDisarming = false
			manager.runSupervisionRestartFenceArmed = true
			if rearmErr != nil {
				manager.executionEpochStateErr = errors.Join(err, rearmErr)
				manager.notifyReadinessChanged()
			}
		}
		s.mu.Unlock()
		slog.Error("disarm clean run supervision restart fence", "event", "generator_run_supervision_fence_disarm_failed", "execution_generation", generation, "error", err, "rearm_error", rearmErr)
		return
	}

	s.mu.Lock()
	manager = s.executionEpochManager
	if manager == nil || manager.executionGeneration != generation {
		s.mu.Unlock()
		return
	}
	if !manager.runSupervisionGenerationExpired && !manager.runSupervisionRestartFenceInherited &&
		!s.hasSupervisedRunWorkLocked(generation) {
		manager.runSupervisionRestartFenceArmed = false
		manager.runSupervisionRestartFenceDisarming = false
		s.mu.Unlock()
		return
	}
	// New supervised work or a semantic expiry became visible while the durable
	// bit was being cleared. Keep claims behind the disarming barrier until the
	// safe value has been restored.
	s.mu.Unlock()

	rearmErr := state.persistRunSupervisionFence(generation, true)
	s.mu.Lock()
	manager = s.executionEpochManager
	if manager != nil && manager.executionGeneration == generation {
		manager.runSupervisionRestartFenceDisarming = false
		manager.runSupervisionRestartFenceArmed = true
		if rearmErr != nil {
			manager.executionEpochStateErr = rearmErr
			manager.notifyReadinessChanged()
		}
	}
	s.mu.Unlock()
	if rearmErr != nil {
		slog.Error("rearm active run supervision restart fence", "event", "generator_run_supervision_fence_rearm_failed", "execution_generation", generation, "error", rearmErr)
	}
}

// hasSupervisedRunWorkLocked reports work whose safe restart semantics depend
// on the durable supervision marker. Service.mu must be held by the caller.
func (s *Service) hasSupervisedRunWorkLocked(generation uint64) bool {
	if len(s.runSupervisions) != 0 || s.runSupervisionDrainingVUsers != 0 {
		return true
	}
	for _, assignment := range s.assignments {
		if assignment.executionGeneration == generation && !emptyRunSupervisionTokenHash(assignment.runSupervisionTokenHash) {
			return true
		}
	}
	return false
}

func (s *Service) clearRunSupervisionLocked(testID string) {
	lease := s.runSupervisions[testID]
	if lease != nil && lease.timer != nil {
		lease.timer.Stop()
	}
	if lease != nil && !emptyRunSupervisionTokenHash(lease.tokenHash) {
		s.addRunSupervisionTombstoneLocked(runSupervisionTombstoneKey{
			testID: testID, generation: lease.generation, tokenHash: lease.tokenHash,
		})
	}
	delete(s.runSupervisions, testID)
}

func (s *Service) addRunSupervisionTombstoneLocked(key runSupervisionTombstoneKey) {
	if s.hasRunSupervisionTombstoneLocked(key) {
		return
	}
	s.runSupervisionTombstoneOrder = append(s.runSupervisionTombstoneOrder, key)
	if len(s.runSupervisionTombstoneOrder) <= maxRunSupervisionTombstones {
		return
	}
	s.runSupervisionTombstoneOrder = s.runSupervisionTombstoneOrder[1:]
}

func (s *Service) hasRunSupervisionTombstoneLocked(key runSupervisionTombstoneKey) bool {
	for _, existing := range s.runSupervisionTombstoneOrder {
		if existing.testID == key.testID && existing.generation == key.generation &&
			sameRunSupervisionTokenHash(existing.tokenHash, key.tokenHash) {
			return true
		}
	}
	return false
}

func (s *Service) hasAnyRunSupervisionTombstoneLocked(testID string, generation uint64) bool {
	for _, existing := range s.runSupervisionTombstoneOrder {
		if existing.testID == testID && existing.generation == generation {
			return true
		}
	}
	return false
}

func (s *Service) expireRunSupervision(testID string, timerToken uint64) {
	s.mu.Lock()
	now := time.Now()
	lease := s.runSupervisions[testID]
	if lease == nil || lease.timerToken != timerToken ||
		lease.state == pb.RunSupervisionState_RUN_SUPERVISION_STATE_STOPPING ||
		lease.state == pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED {
		s.mu.Unlock()
		return
	}
	if now.Before(lease.deadline) {
		s.resetRunSupervisionTimerLocked(testID, lease, lease.deadline)
		s.mu.Unlock()
		return
	}
	assignment, ok := s.assignments[testID]
	if !ok || assignment.executionGeneration != lease.generation ||
		!sameRunSupervisionTokenHash(assignment.runSupervisionTokenHash, lease.tokenHash) {
		s.clearRunSupervisionLocked(testID)
		s.mu.Unlock()
		return
	}
	s.addRunSupervisionTombstoneLocked(runSupervisionTombstoneKey{testID: testID, generation: lease.generation, tokenHash: lease.tokenHash})
	generation := assignment.executionGeneration
	s.markRunSupervisionGenerationExpiredLocked(generation)
	ttl := lease.ttl
	orphaned := !assignment.orphanedUntil.IsZero()
	stops := s.detachFencedTestsLocked([]string{testID}, generation, false)
	s.mu.Unlock()

	s.runSupervisionExpirations.Add(1)
	reason := runSupervisionFailureReason
	if orphaned {
		// The run outlived its controller under a continue policy and no
		// successor adopted it within the grace.
		reason = adoptionTimeoutReason
		slog.Error("orphaned run was not adopted within its grace; stopping", "event", "generator_run_adoption_timeout", "test_id", testID, "execution_generation", generation)
	} else {
		slog.Error("generator run supervision expired", "event", "generator_run_supervision_expired", "test_id", testID, "execution_generation", generation, "ttl", ttl)
	}
	if len(stops) == 0 {
		return
	}
	stop := stops[0]
	stop.finalState = "FAILED"
	stop.finalError = reason
	stop.escalationReason = reason
	s.startDetachedRunDrain(stop, true)
}

func signalDetachedRunSoft(stop detachedFencedTest) {
	for _, execution := range stop.executions {
		if execution == nil {
			continue
		}
		if execution.started.Load() && execution.engine != nil &&
			(execution.startAt.IsZero() || !time.Now().Before(execution.startAt)) {
			execution.engine.GracefulStop()
			continue
		}
		stopGroupExecution(execution)
	}
}

func signalDetachedRunHard(stop detachedFencedTest) {
	for _, execution := range stop.executions {
		stopGroupExecution(execution)
	}
}

// startDetachedRunDrain is the single retained safety state machine for every
// path that removes an assignment while one of its exact done barriers may
// still be open. Signalling is synchronous and fanout-friendly; the returned
// channel is only for callers that want to observe completion.
func (s *Service) startDetachedRunDrain(stop detachedFencedTest, graceful bool) <-chan error {
	result := make(chan error, 1)
	if graceful {
		signalDetachedRunSoft(stop)
	} else {
		signalDetachedRunHard(stop)
	}
	go func() {
		if graceful {
			drainCtx, drainCancel := context.WithTimeout(context.Background(), s.runSupervisionDrain)
			drainErr := waitForGeneratorExecutions(drainCtx, stop.executions)
			drainCancel()
			if drainErr == nil {
				result <- s.finishDetachedFencedTest(context.Background(), stop)
				return
			}
			signalDetachedRunHard(stop)
		}

		hardCtx, hardCancel := context.WithTimeout(context.Background(), s.runSupervisionHardGrace)
		hardErr := waitForGeneratorExecutions(hardCtx, stop.executions)
		hardCancel()
		if hardErr == nil {
			result <- s.finishDetachedFencedTest(context.Background(), stop)
			return
		}

		outstanding := outstandingGeneratorExecutions(stop.executions)
		s.SetDraining(true)
		reason := strings.TrimSpace(stop.escalationReason)
		if reason == "" {
			reason = defaultEscalationReason
		}
		if reason == runSupervisionFailureReason {
			s.runSupervisionEscalations.Add(1)
		}
		request := SelfTerminationRequest{
			TestID: stop.testID, ExecutionGeneration: stop.assignment.executionGeneration,
			Reason: reason, Outstanding: outstanding, RequestedAt: time.Now().UTC(),
		}
		// Notify the process owner first so its independent +2s os.Exit watchdog
		// bounds even a wedged filesystem sync. The buffered notification does not
		// wait for the graceful shutdown path. Holding the intent lock across both
		// operations prevents Shutdown from closing the durable root in between.
		s.runSupervisionIntentMu.Lock()
		s.requestSelfTermination(request)
		// The process notification is once-only, but every exact run escalation is
		// separate durable evidence. Multiple drains can cross the hard deadline
		// before the process watchdog exits; dropping all but the first would make
		// their terminal reasons unrecoverable after restart.
		intentErr := s.persistRunSupervisionCrashIntentLocked(request)
		s.runSupervisionIntentMu.Unlock()
		if intentErr != nil {
			slog.Error("persist generator safety-exit intent", "event", "generator_safety_exit_intent_persist_failed", "test_id", stop.testID, "execution_generation", stop.assignment.executionGeneration, "reason", reason, "error", intentErr)
		}
		event := "generator_execution_drain_escalated"
		if reason == runSupervisionFailureReason {
			event = "generator_run_abandon_escalated"
		}
		slog.Error("generator execution drain escalated", "event", event, "test_id", stop.testID, "execution_generation", stop.assignment.executionGeneration, "reason", reason, "outstanding_executions", outstanding)
		result <- fmt.Errorf("%s: %w", reason, hardErr)
	}()
	return result
}

func outstandingGeneratorExecutions(executions []*groupExecution) int {
	outstanding := 0
	for _, execution := range executions {
		if execution == nil || execution.done == nil {
			continue
		}
		select {
		case <-execution.done:
		default:
			outstanding++
		}
	}
	return outstanding
}

func (s *Service) requestSelfTermination(request SelfTerminationRequest) bool {
	if s == nil {
		return false
	}
	requested := false
	s.selfTerminationOnce.Do(func() {
		requested = true
		select {
		case s.selfTermination <- request:
		default:
		}
	})
	return requested
}

func runSupervisionCrashIntentName(state *executionEpochStateStore) string {
	if state == nil {
		return ""
	}
	return state.name + ".generator-safety-exit.json"
}

func (s *Service) persistRunSupervisionCrashIntent(request SelfTerminationRequest) (retErr error) {
	s.runSupervisionIntentMu.Lock()
	defer s.runSupervisionIntentMu.Unlock()
	return s.persistRunSupervisionCrashIntentLocked(request)
}

func (s *Service) persistRunSupervisionCrashIntentLocked(request SelfTerminationRequest) (retErr error) {
	if !validRunEscalationReason(request.Reason) {
		return fmt.Errorf("invalid generator safety-exit reason %q", request.Reason)
	}
	record := runSupervisionCrashIntent{
		Version:             runSupervisionCrashVersion,
		Reason:              request.Reason,
		TestID:              request.TestID,
		ExecutionGeneration: request.ExecutionGeneration,
		Outstanding:         request.Outstanding,
		RequestedAt:         request.RequestedAt.UTC(),
	}
	if err := validateRunSupervisionCrashRecord(record); err != nil {
		return err
	}
	records, err := s.readRunSupervisionCrashJournalLocked()
	if err != nil {
		return err
	}
	records = appendRunSupervisionCrashRecord(records, record)
	if err := s.persistRunSupervisionCrashJournalLocked(records); err != nil {
		return err
	}
	s.setSafetyExitEvidenceLocked(records)
	return nil
}

func appendRunSupervisionCrashRecord(records []runSupervisionCrashIntent, record runSupervisionCrashIntent) []runSupervisionCrashIntent {
	retained := make([]runSupervisionCrashIntent, 0, min(len(records)+1, maxRunSupervisionCrashEntries))
	for _, existing := range records {
		if runSupervisionCrashKey(existing) == runSupervisionCrashKey(record) {
			continue
		}
		retained = append(retained, existing)
	}
	if len(retained) >= maxRunSupervisionCrashEntries {
		retained = append([]runSupervisionCrashIntent(nil), retained[len(retained)-(maxRunSupervisionCrashEntries-1):]...)
	}
	return append(retained, record)
}

func (s *Service) setSafetyExitEvidenceLocked(records []runSupervisionCrashIntent) {
	s.safetyExitEvidence = append([]runSupervisionCrashIntent(nil), records...)
}

func runSupervisionCrashRecordProto(record runSupervisionCrashIntent) *pb.GeneratorSafetyExitEvidence {
	return &pb.GeneratorSafetyExitEvidence{
		TestId:                 record.TestID,
		ExecutionGeneration:    record.ExecutionGeneration,
		LifecycleState:         "FAILED",
		Reason:                 record.Reason,
		RequestedAtUnixMs:      record.RequestedAt.UnixMilli(),
		OutstandingExecutions:  int32(record.Outstanding),
		RequestedAtRfc3339Nano: record.RequestedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (s *Service) safetyExitEvidenceProto() (*pb.GeneratorSafetyExitEvidence, []*pb.GeneratorSafetyExitEvidence) {
	if s == nil {
		return nil, nil
	}
	s.runSupervisionIntentMu.Lock()
	defer s.runSupervisionIntentMu.Unlock()
	if len(s.safetyExitEvidence) == 0 {
		return nil, nil
	}
	values := make([]*pb.GeneratorSafetyExitEvidence, 0, len(s.safetyExitEvidence))
	for _, record := range s.safetyExitEvidence {
		values = append(values, runSupervisionCrashRecordProto(record))
	}
	return values[len(values)-1], values
}

func (s *Service) persistRunSupervisionCrashJournalLocked(records []runSupervisionCrashIntent) (retErr error) {
	state := s.executionEpochManager.executionEpochState
	if state == nil || state.root == nil {
		return errors.New("durable generator state store is unavailable")
	}
	if len(records) == 0 || len(records) > maxRunSupervisionCrashEntries {
		return fmt.Errorf("run supervision crash journal contains %d records", len(records))
	}
	seen := make(map[runSupervisionCrashRecordKey]struct{}, len(records))
	for _, record := range records {
		if err := validateRunSupervisionCrashRecord(record); err != nil {
			return err
		}
		key := runSupervisionCrashKey(record)
		if _, exists := seen[key]; exists {
			return errors.New("run supervision crash journal contains duplicate incidents")
		}
		seen[key] = struct{}{}
	}
	raw, err := json.Marshal(runSupervisionCrashJournal{Version: runSupervisionJournalVersion, Records: records})
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxRunSupervisionCrashBytes {
		return fmt.Errorf("run supervision crash intent exceeds %d bytes", maxRunSupervisionCrashBytes)
	}
	name := runSupervisionCrashIntentName(state)
	if name == "" {
		return errors.New("durable generator state path is unavailable")
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	tempName := name + ".tmp-" + hex.EncodeToString(random)
	file, err := state.root.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = state.root.Remove(tempName)
		}
	}()
	if err := writeExecutionEpochState(file, raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := state.root.Rename(tempName, name); err != nil {
		return err
	}
	directory, err := state.root.Open(".")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func (s *Service) readRunSupervisionCrashJournalLocked() ([]runSupervisionCrashIntent, error) {
	state := s.executionEpochManager.executionEpochState
	if state == nil || state.root == nil {
		return nil, errors.New("durable generator state store is unavailable")
	}
	name := runSupervisionCrashIntentName(state)
	info, err := state.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("run supervision crash intent is not a regular non-symlink file")
	}
	if info.Size() > maxRunSupervisionCrashBytes {
		return nil, fmt.Errorf("run supervision crash intent exceeds %d bytes", maxRunSupervisionCrashBytes)
	}
	file, err := state.root.Open(name)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maxRunSupervisionCrashBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(raw) > maxRunSupervisionCrashBytes {
		return nil, fmt.Errorf("run supervision crash intent exceeds %d bytes", maxRunSupervisionCrashBytes)
	}
	return decodeRunSupervisionCrashJournal(raw)
}

func decodeRunSupervisionCrashJournal(raw []byte) ([]runSupervisionCrashIntent, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("decode run supervision crash intent: %w", err)
	}
	var records []runSupervisionCrashIntent
	if _, journal := probe["records"]; journal {
		var value runSupervisionCrashJournal
		if err := decodeStrictRunSupervisionJSON(raw, &value); err != nil {
			return nil, err
		}
		if value.Version != runSupervisionJournalVersion || len(value.Records) == 0 || len(value.Records) > maxRunSupervisionCrashEntries {
			return nil, errors.New("run supervision crash journal is invalid")
		}
		records = value.Records
	} else {
		// Version 1 shipped as one bare record. Read it strictly and migrate it to
		// the bounded journal on the next reconciliation or safety exit.
		var legacy runSupervisionCrashIntent
		if err := decodeStrictRunSupervisionJSON(raw, &legacy); err != nil {
			return nil, err
		}
		records = []runSupervisionCrashIntent{legacy}
	}
	seen := make(map[runSupervisionCrashRecordKey]struct{}, len(records))
	for _, record := range records {
		if err := validateRunSupervisionCrashRecord(record); err != nil {
			return nil, err
		}
		key := runSupervisionCrashKey(record)
		if _, exists := seen[key]; exists {
			return nil, errors.New("run supervision crash journal contains duplicate incidents")
		}
		seen[key] = struct{}{}
	}
	return records, nil
}

func decodeStrictRunSupervisionJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode run supervision crash intent: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode run supervision crash intent: %w", err)
	}
	return nil
}

func validateRunSupervisionCrashRecord(record runSupervisionCrashIntent) error {
	if record.Version != runSupervisionCrashVersion || !validRunEscalationReason(record.Reason) ||
		strings.TrimSpace(record.TestID) == "" || len(record.TestID) > maxGeneratorOperationIDBytes ||
		record.Outstanding < 0 || int64(record.Outstanding) > maxGeneratorVUsers || record.RequestedAt.IsZero() {
		return errors.New("run supervision crash intent is invalid")
	}
	return nil
}

func (s *Service) reconcileRunSupervisionCrashIntent() error {
	s.runSupervisionIntentMu.Lock()
	defer s.runSupervisionIntentMu.Unlock()
	records, err := s.readRunSupervisionCrashJournalLocked()
	if err != nil || len(records) == 0 {
		return err
	}
	// A safety exit is a durable fence, not an ordinary generator restart. Older
	// binaries did not pre-arm the execution-state marker, so reconcile the
	// token-free crash journal into that marker before accepting any work. A later
	// positive generation clears it atomically when its own high-water is written.
	s.mu.Lock()
	currentGeneration := s.executionEpochManager.executionGeneration
	fenceCurrentGeneration := false
	for _, record := range records {
		if record.ExecutionGeneration >= currentGeneration {
			fenceCurrentGeneration = true
			break
		}
	}
	if fenceCurrentGeneration {
		if err := s.executionEpochManager.executionEpochState.persistRunSupervisionFence(currentGeneration, true); err != nil {
			s.executionEpochManager.executionEpochStateErr = err
			s.executionEpochManager.notifyReadinessChanged()
			s.mu.Unlock()
			return fmt.Errorf("persist reconciled run supervision restart fence: %w", err)
		}
		s.executionEpochManager.runSupervisionRestartFenceArmed = true
		s.executionEpochManager.runSupervisionRestartFenceInherited = true
		s.executionEpochManager.runSupervisionGenerationExpired = true
		if currentGeneration > 0 {
			s.executionEpochManager.executionRestartRevalidation = false
		}
	}
	s.mu.Unlock()
	firstReconciliations := make([]bool, len(records))
	journalChanged := false
	for index := range records {
		firstReconciliations[index] = !records[index].Reconciled
		if firstReconciliations[index] {
			records[index].Reconciled = true
			journalChanged = true
		}
	}
	if journalChanged {
		// Preserve the evidence for terminal replay on later restarts, but durably
		// mark the one-time telemetry handoff before incrementing the process-local
		// counter. A corrupt/unwritable marker keeps the generator unready rather
		// than emitting the same escalation on every restart.
		if err := s.persistRunSupervisionCrashJournalLocked(records); err != nil {
			return fmt.Errorf("mark generator safety-exit intent reconciled: %w", err)
		}
	}
	s.setSafetyExitEvidenceLocked(records)
	for index, record := range records {
		if firstReconciliations[index] && record.Reason == runSupervisionFailureReason {
			s.runSupervisionEscalations.Add(1)
		}
		s.metricsBroker.restoreTerminal(s.metricsBatchForState(
			record.TestID,
			record.ExecutionGeneration,
			0,
			false,
			nil,
			"FAILED",
			record.Reason,
		), record.RequestedAt)
		if firstReconciliations[index] {
			slog.Error("reconciled prior generator safety exit", "event", "generator_safety_exit_reconciled", "test_id", record.TestID, "execution_generation", record.ExecutionGeneration, "reason", record.Reason, "outstanding_executions", record.Outstanding, "requested_at", record.RequestedAt)
		} else {
			slog.Info("restored reconciled generator safety-exit evidence", "event", "generator_safety_exit_restored", "test_id", record.TestID, "execution_generation", record.ExecutionGeneration, "reason", record.Reason)
		}
	}
	return nil
}

func validRunEscalationReason(reason string) bool {
	switch reason {
	case runSupervisionFailureReason, stopEscalationReason, fenceEscalationReason,
		shutdownEscalationReason, testFailureEscalationReason, durationEscalationReason,
		defaultEscalationReason, runtimeContainmentFailureReason, adoptionTimeoutReason:
		return true
	default:
		return false
	}
}
