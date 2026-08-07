package generatorrpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// executionEpochManager is the single owner of the generator execution-term
// high-water, volatile lease, durable state, expiry timer, and readiness
// signal. It is embedded in Service during the migration so package-level
// compatibility seams continue to resolve the historical field names while
// production code delegates through manager operations.
//
// Fields are protected by Service.mu unless a method explicitly documents a
// construction- or shutdown-only access.
type executionEpochManager struct {
	executionGeneration      uint64
	executionExpiresAt       time.Time
	executionEpochTimer      *time.Timer
	executionEpochTimerToken uint64
	nextEpochTimerToken      uint64
	executionEpochFencing    bool
	executionFencePending    int
	executionEpochState      *executionEpochStateStore
	executionEpochStateErr   error
	executionEpochStatePath  string
	executionLeaseOwner      string

	// executionRestartRevalidation is set only while reconstructing a service
	// from a positive durable high-water. It permits one exact-owner claim to
	// restore the volatile expiry lost with the process. Ordinary timer expiry
	// never restores it, so an expired live session remains non-revivable.
	executionRestartRevalidation bool
	readinessChanged             chan struct{}

	pendingPersistence   *executionEpochPersistence
	nextPersistenceToken uint64
	shuttingDown         bool
	persistState         func(*executionEpochStateStore, uint64, string) error
}

func newExecutionEpochManager() *executionEpochManager {
	return &executionEpochManager{
		readinessChanged: make(chan struct{}, 1),
		persistState: func(state *executionEpochStateStore, generation uint64, leaseOwner string) error {
			return state.persist(generation, leaseOwner)
		},
	}
}

type executionEpochPersistence struct {
	token      uint64
	generation uint64
	leaseOwner string
	expiresAt  time.Time
	higher     bool
	bindOwner  bool
	state      *executionEpochStateStore
	done       chan struct{}
}

type executionEpochClaimKind uint8

const (
	executionEpochClaimApply executionEpochClaimKind = iota + 1
	executionEpochClaimReject
	executionEpochClaimPersist
	executionEpochClaimWait
)

type executionEpochClaimDecision struct {
	kind              executionEpochClaimKind
	persistence       *executionEpochPersistence
	wait              <-chan struct{}
	currentGeneration uint64
	expiresAt         time.Time
	errorMessage      string
	lowerTestIDs      []string
	err               error
}

func (manager *executionEpochManager) planClaimLocked(service *Service, generation uint64, leaseOwner string, expiresAt time.Time, now time.Time) executionEpochClaimDecision {
	if manager.pendingPersistence != nil {
		return executionEpochClaimDecision{kind: executionEpochClaimWait, wait: manager.pendingPersistence.done}
	}
	if service.draining || manager.shuttingDown {
		return executionEpochClaimDecision{err: status.Error(codes.Unavailable, "generator is draining")}
	}
	if manager.executionEpochStateErr != nil || manager.executionEpochState == nil {
		err := manager.executionEpochStateErr
		if err == nil {
			err = errors.New("durable execution epoch state is unavailable")
		}
		return executionEpochClaimDecision{err: status.Errorf(codes.FailedPrecondition, "durable execution epoch state: %v", err)}
	}
	current := manager.executionGeneration
	if generation < current {
		return executionEpochClaimDecision{
			kind:              executionEpochClaimReject,
			currentGeneration: current,
			expiresAt:         manager.executionExpiresAt,
			errorMessage:      fmt.Sprintf("execution generation %d has been fenced by generation %d", generation, current),
		}
	}
	if generation == current && current > 0 && manager.executionLeaseOwner != "" && leaseOwner != manager.executionLeaseOwner {
		return executionEpochClaimDecision{
			kind:              executionEpochClaimReject,
			currentGeneration: current,
			expiresAt:         manager.executionExpiresAt,
			errorMessage:      "execution generation belongs to a different controller lease owner",
		}
	}
	revalidatingRestart := generation == current && current > 0 && manager.executionRestartRevalidation && (manager.executionExpiresAt.IsZero() || !now.Before(manager.executionExpiresAt))
	if generation == current && current > 0 && !revalidatingRestart && (manager.executionExpiresAt.IsZero() || !now.Before(manager.executionExpiresAt)) {
		return executionEpochClaimDecision{
			kind:              executionEpochClaimReject,
			currentGeneration: current,
			expiresAt:         manager.executionExpiresAt,
			errorMessage:      "execution generation has expired; a higher generation is required",
		}
	}

	higher := generation > current
	bindOwner := revalidatingRestart && manager.executionLeaseOwner == ""
	if higher || bindOwner {
		manager.nextPersistenceToken++
		if manager.nextPersistenceToken == 0 {
			manager.nextPersistenceToken++
		}
		persistence := &executionEpochPersistence{
			token:      manager.nextPersistenceToken,
			generation: generation,
			leaseOwner: leaseOwner,
			expiresAt:  expiresAt,
			higher:     higher,
			bindOwner:  bindOwner,
			state:      manager.executionEpochState,
			done:       make(chan struct{}),
		}
		manager.pendingPersistence = persistence
		return executionEpochClaimDecision{kind: executionEpochClaimPersist, persistence: persistence}
	}
	return manager.installLeaseLocked(service, generation, expiresAt)
}

func (manager *executionEpochManager) persistenceCurrentLocked(persistence *executionEpochPersistence) bool {
	return persistence != nil && manager.pendingPersistence != nil && manager.pendingPersistence.token == persistence.token
}

func (manager *executionEpochManager) completePersistenceLocked(persistence *executionEpochPersistence) {
	if !manager.persistenceCurrentLocked(persistence) {
		return
	}
	manager.pendingPersistence = nil
	close(persistence.done)
}

func (manager *executionEpochManager) abortPersistenceLocked(persistence *executionEpochPersistence) {
	manager.completePersistenceLocked(persistence)
}

func (manager *executionEpochManager) failPersistenceLocked(persistence *executionEpochPersistence, err error) error {
	if !manager.persistenceCurrentLocked(persistence) {
		return status.Error(codes.Aborted, "execution epoch persistence is no longer current")
	}
	manager.executionEpochStateErr = err
	manager.notifyReadinessChanged()
	manager.completePersistenceLocked(persistence)
	label := fmt.Sprintf("persist execution generation %d", persistence.generation)
	if persistence.bindOwner && !persistence.higher {
		label += " owner"
	}
	return status.Errorf(codes.FailedPrecondition, "%s: %v", label, err)
}

func (manager *executionEpochManager) commitPersistenceLocked(service *Service, persistence *executionEpochPersistence, canceled error) (executionEpochClaimDecision, error) {
	if !manager.persistenceCurrentLocked(persistence) {
		return executionEpochClaimDecision{}, status.Error(codes.Aborted, "execution epoch persistence is no longer current")
	}
	if persistence.higher {
		manager.executionGeneration = persistence.generation
		manager.executionLeaseOwner = persistence.leaseOwner
		manager.executionRestartRevalidation = false
		service.resetTestExecutionLeasesForGenerationLocked(persistence.generation)
		service.metricsBroker.fenceBeforeGeneration(persistence.generation)
	} else if persistence.bindOwner {
		manager.executionLeaseOwner = persistence.leaseOwner
	}
	if canceled != nil {
		if persistence.higher {
			manager.executionExpiresAt = time.Time{}
		}
		lowerTestIDs := service.testIDsBeforeGenerationLocked(persistence.generation)
		if len(lowerTestIDs) > 0 {
			manager.executionEpochFencing = true
		}
		manager.completePersistenceLocked(persistence)
		return executionEpochClaimDecision{
			kind:              executionEpochClaimApply,
			currentGeneration: manager.executionGeneration,
			expiresAt:         manager.executionExpiresAt,
			lowerTestIDs:      lowerTestIDs,
		}, canceled
	}
	decision := manager.installLeaseLocked(service, persistence.generation, persistence.expiresAt)
	manager.completePersistenceLocked(persistence)
	return decision, nil
}

func (manager *executionEpochManager) installLeaseLocked(service *Service, generation uint64, expiresAt time.Time) executionEpochClaimDecision {
	if generation == manager.executionGeneration && manager.executionExpiresAt.After(expiresAt) {
		expiresAt = manager.executionExpiresAt
	}
	manager.executionExpiresAt = expiresAt
	manager.executionRestartRevalidation = false
	manager.resetTimerLocked(service, generation, expiresAt)
	lowerTestIDs := service.testIDsBeforeGenerationLocked(generation)
	if len(lowerTestIDs) > 0 {
		manager.executionEpochFencing = true
	}
	return executionEpochClaimDecision{
		kind:              executionEpochClaimApply,
		currentGeneration: manager.executionGeneration,
		expiresAt:         expiresAt,
		lowerTestIDs:      lowerTestIDs,
	}
}

func (manager *executionEpochManager) finalizeClaimLocked(service *Service, generation uint64, now time.Time) executionEpochClaimDecision {
	if manager.executionGeneration == generation && manager.executionFencePending == 0 && len(service.testIDsBeforeGenerationLocked(generation)) == 0 {
		manager.executionEpochFencing = false
	}
	accepted := manager.executionGeneration == generation && !manager.executionEpochFencing && now.Before(manager.executionExpiresAt)
	decision := executionEpochClaimDecision{
		kind:              executionEpochClaimApply,
		currentGeneration: manager.executionGeneration,
		expiresAt:         manager.executionExpiresAt,
	}
	if !accepted {
		decision.errorMessage = "execution generation changed or fencing did not complete"
	}
	return decision
}

func (manager *executionEpochManager) validateMutationLocked(generation uint64, now time.Time) error {
	if manager.executionEpochStateErr != nil || manager.executionEpochState == nil {
		return status.Error(codes.FailedPrecondition, "durable execution epoch state is unavailable")
	}
	if manager.pendingPersistence != nil {
		return status.Error(codes.Unavailable, "execution generation transition is being persisted")
	}
	if generation == 0 {
		if manager.executionGeneration != 0 {
			return status.Errorf(codes.FailedPrecondition, "legacy generation 0 is fenced by execution generation %d", manager.executionGeneration)
		}
		return nil
	}
	if generation != manager.executionGeneration {
		return status.Errorf(codes.FailedPrecondition, "execution generation %d is not active; current generation is %d", generation, manager.executionGeneration)
	}
	if manager.executionExpiresAt.IsZero() || !now.Before(manager.executionExpiresAt) {
		return status.Error(codes.FailedPrecondition, ExecutionGenerationSessionExpiredMessage)
	}
	if manager.executionEpochFencing {
		return status.Error(codes.Unavailable, "previous execution generation is still being fenced")
	}
	return nil
}

func (manager *executionEpochManager) resetTimerLocked(service *Service, generation uint64, expiresAt time.Time) {
	if manager.executionEpochTimer != nil {
		manager.executionEpochTimer.Stop()
	}
	manager.nextEpochTimerToken++
	if manager.nextEpochTimerToken == 0 {
		manager.nextEpochTimerToken++
	}
	token := manager.nextEpochTimerToken
	manager.executionEpochTimerToken = token
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	manager.executionEpochTimer = time.AfterFunc(delay, func() {
		service.expireExecutionEpochToken(generation, expiresAt, token, true)
	})
}

func (manager *executionEpochManager) planExpiryLocked(service *Service, generation uint64, expiresAt time.Time, token uint64, requireToken bool, now time.Time) (<-chan struct{}, []string, bool) {
	if manager.pendingPersistence != nil {
		return manager.pendingPersistence.done, nil, false
	}
	if requireToken && manager.executionEpochTimerToken != token {
		return nil, nil, false
	}
	if manager.executionGeneration != generation || !manager.executionExpiresAt.Equal(expiresAt) || now.Before(manager.executionExpiresAt) {
		return nil, nil, false
	}
	testIDs := make([]string, 0)
	for testID, assignment := range service.assignments {
		if assignment.executionGeneration <= generation {
			testIDs = append(testIDs, testID)
		}
	}
	if len(testIDs) > 0 {
		manager.executionEpochFencing = true
	}
	return nil, testIDs, true
}

func (manager *executionEpochManager) beginDetachedFencesLocked(count int) {
	if count <= 0 {
		return
	}
	manager.executionFencePending += count
	manager.executionEpochFencing = true
}

func (manager *executionEpochManager) completeDetachedFenceLocked(service *Service) {
	if manager.executionFencePending > 0 {
		manager.executionFencePending--
	}
	if manager.executionFencePending == 0 && len(service.testIDsBeforeGenerationLocked(manager.executionGeneration)) == 0 {
		manager.executionEpochFencing = false
	}
}

func (manager *executionEpochManager) readinessErrorLocked() error {
	if manager == nil {
		return errors.New("durable execution epoch state is unavailable")
	}
	if manager.executionEpochStateErr != nil {
		return fmt.Errorf("durable execution epoch state: %w", manager.executionEpochStateErr)
	}
	if manager.executionEpochState == nil {
		return errors.New("durable execution epoch state is unavailable")
	}
	return nil
}

func (manager *executionEpochManager) notifyReadinessChanged() {
	if manager == nil || manager.readinessChanged == nil {
		return
	}
	select {
	case manager.readinessChanged <- struct{}{}:
	default:
	}
}

func (manager *executionEpochManager) changes() <-chan struct{} {
	if manager == nil {
		return nil
	}
	return manager.readinessChanged
}

func (manager *executionEpochManager) stopTimerLocked() {
	manager.shuttingDown = true
	manager.executionEpochTimerToken = 0
	if manager.executionEpochTimer != nil {
		manager.executionEpochTimer.Stop()
		manager.executionEpochTimer = nil
	}
}

func (manager *executionEpochManager) stateForCloseLocked() *executionEpochStateStore {
	return manager.executionEpochState
}

func (manager *executionEpochManager) waitForPersistence(ctx context.Context, service *Service) error {
	for {
		service.mu.RLock()
		var done <-chan struct{}
		if manager.pendingPersistence != nil {
			done = manager.pendingPersistence.done
		}
		service.mu.RUnlock()
		if done == nil {
			return nil
		}
		if ctx == nil {
			<-done
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		}
	}
}

func (service *Service) ClaimExecutionEpoch(ctx context.Context, claim *pb.ExecutionEpochClaim) (*pb.ExecutionEpochAck, error) {
	if claim == nil || claim.GetExecutionGeneration() == 0 {
		return nil, status.Error(codes.InvalidArgument, "execution_generation must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	ttlMs := claim.GetLeaseTtlMilliseconds()
	if ttlMs <= 0 {
		return nil, status.Error(codes.InvalidArgument, "lease_ttl_milliseconds must be positive")
	}
	if ttlMs > maxExecutionEpochTTL.Milliseconds() {
		return nil, status.Errorf(codes.InvalidArgument, "lease_ttl_milliseconds exceeds maximum %d", maxExecutionEpochTTL.Milliseconds())
	}
	generation := claim.GetExecutionGeneration()
	leaseOwner := strings.TrimSpace(claim.GetExecutionLeaseOwnerId())
	if err := validateExecutionLeaseOwner(generation, leaseOwner, true); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "execution_lease_owner_id: %v", err)
	}
	expiresAt := time.Now().Add(time.Duration(ttlMs) * time.Millisecond)
	if testID := strings.TrimSpace(claim.GetTestId()); testID != "" {
		return service.claimTestExecutionLease(ctx, testID, generation, leaseOwner, expiresAt)
	}
	return service.claimValidatedExecutionEpoch(ctx, generation, leaseOwner, expiresAt)
}

func (service *Service) claimValidatedExecutionEpoch(ctx context.Context, generation uint64, leaseOwner string, expiresAt time.Time) (*pb.ExecutionEpochAck, error) {
	for {
		service.mu.Lock()
		if err := ctx.Err(); err != nil {
			service.mu.Unlock()
			return nil, status.FromContextError(err).Err()
		}
		decision := service.executionEpochManager.planClaimLocked(service, generation, leaseOwner, expiresAt, time.Now())
		service.mu.Unlock()
		if decision.err != nil {
			return nil, decision.err
		}
		switch decision.kind {
		case executionEpochClaimReject:
			return &pb.ExecutionEpochAck{
				Accepted:          false,
				CurrentGeneration: decision.currentGeneration,
				ExpiresAtUnixMs:   decision.expiresAt.UnixMilli(),
				ErrorMessage:      decision.errorMessage,
			}, nil
		case executionEpochClaimWait:
			select {
			case <-decision.wait:
				continue
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			}
		case executionEpochClaimPersist:
			persistence := decision.persistence
			if err := ctx.Err(); err != nil {
				service.mu.Lock()
				service.executionEpochManager.abortPersistenceLocked(persistence)
				service.mu.Unlock()
				return nil, status.FromContextError(err).Err()
			}
			persistErr := service.executionEpochManager.persistState(persistence.state, persistence.generation, persistence.leaseOwner)
			service.mu.Lock()
			if persistErr != nil {
				err := service.executionEpochManager.failPersistenceLocked(persistence, persistErr)
				service.mu.Unlock()
				return nil, err
			}
			decision, canceled := service.executionEpochManager.commitPersistenceLocked(service, persistence, ctx.Err())
			service.mu.Unlock()
			return service.finishExecutionEpochClaim(ctx, generation, decision, canceled)
		case executionEpochClaimApply:
			return service.finishExecutionEpochClaim(ctx, generation, decision, nil)
		default:
			return nil, status.Error(codes.Internal, "execution epoch manager returned an invalid claim decision")
		}
	}
}

func (service *Service) finishExecutionEpochClaim(ctx context.Context, generation uint64, decision executionEpochClaimDecision, committedContextError error) (*pb.ExecutionEpochAck, error) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	cleanupErr := service.forceStopFencedTests(cleanupCtx, decision.lowerTestIDs, generation-1)
	cleanupCancel()
	if committedContextError != nil {
		return nil, status.FromContextError(committedContextError).Err()
	}
	if cleanupErr != nil {
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: generation,
			ExpiresAtUnixMs:   decision.expiresAt.UnixMilli(),
			ErrorMessage:      cleanupErr.Error(),
		}, nil
	}
	service.mu.Lock()
	final := service.executionEpochManager.finalizeClaimLocked(service, generation, time.Now())
	service.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	ack := &pb.ExecutionEpochAck{
		Accepted:          final.errorMessage == "",
		CurrentGeneration: final.currentGeneration,
		ExpiresAtUnixMs:   final.expiresAt.UnixMilli(),
		ErrorMessage:      final.errorMessage,
	}
	return ack, nil
}
