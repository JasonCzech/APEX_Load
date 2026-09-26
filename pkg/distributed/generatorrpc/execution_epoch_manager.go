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
	// lastRenewedAt/lastGrantedTTL record the most recent accepted epoch claim.
	// A live controller renews well within a third of the TTL; a lost one has
	// been silent for most of it. expireTestExecutionLease uses this to tell
	// controller loss from a deliberate per-run fence.
	lastRenewedAt  time.Time
	lastGrantedTTL time.Duration
	// runSupervisionRestartFenceArmed is persisted before the first supervised
	// lease is acknowledged in a generation. Inherited means this process did not
	// arm it and therefore may not reconstruct pre-restart work. Expired is sticky
	// for the live generation and prevents clean terminalization of other runs
	// from disarming a fence established by a semantic lease expiry.
	runSupervisionRestartFenceArmed     bool
	runSupervisionRestartFenceInherited bool
	runSupervisionRestartFenceDisarming bool
	runSupervisionGenerationExpired     bool

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
	// adoptions are applied when this persistence commits, before the new
	// generation fences lower-generation work.
	adoptions []*pb.RunAdoption
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
	adoptionResults   []*pb.RunAdoptionResult
	err               error
}

func (manager *executionEpochManager) planClaimLocked(service *Service, generation uint64, leaseOwner string, expiresAt time.Time, now time.Time, adoptions []*pb.RunAdoption) executionEpochClaimDecision {
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
			errorMessage:      ExecutionGenerationExpiredMessage,
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
			adoptions:  adoptions,
		}
		manager.pendingPersistence = persistence
		return executionEpochClaimDecision{kind: executionEpochClaimPersist, persistence: persistence}
	}
	// Same generation: an adoption can only be confirmed, never performed.
	adoptionResults := service.adoptRunsLocked(generation, adoptions, false, now)
	decision := manager.installLeaseLocked(service, generation, expiresAt)
	decision.adoptionResults = adoptionResults
	return decision
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
		manager.runSupervisionRestartFenceArmed = false
		manager.runSupervisionRestartFenceInherited = false
		manager.runSupervisionRestartFenceDisarming = false
		manager.runSupervisionGenerationExpired = false
		service.resetTestExecutionLeasesForGenerationLocked(persistence.generation)
		service.metricsBroker.fenceBeforeGeneration(persistence.generation)
	} else if persistence.bindOwner {
		manager.executionLeaseOwner = persistence.leaseOwner
	}
	// Adopt before either path below computes the lower-generation fence list,
	// and even if the caller's context was canceled: the generation change is
	// already durable, and a retrying successor will find the run adopted.
	adoptionResults := service.adoptRunsLocked(persistence.generation, persistence.adoptions, persistence.higher, service.nowTime())
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
			adoptionResults:   adoptionResults,
		}, canceled
	}
	decision := manager.installLeaseLocked(service, persistence.generation, persistence.expiresAt)
	decision.adoptionResults = adoptionResults
	manager.completePersistenceLocked(persistence)
	return decision, nil
}

func (manager *executionEpochManager) installLeaseLocked(service *Service, generation uint64, expiresAt time.Time) executionEpochClaimDecision {
	if generation == manager.executionGeneration && manager.executionExpiresAt.After(expiresAt) {
		expiresAt = manager.executionExpiresAt
	}
	manager.executionExpiresAt = expiresAt
	manager.executionRestartRevalidation = false
	now := service.nowTime()
	manager.lastRenewedAt = now
	manager.lastGrantedTTL = expiresAt.Sub(now)
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

// planExpiryLocked returns the assignments to fence when generation's epoch
// lapses, and the continue-policy runs orphaned instead (see orphanLocked).
// controllerSilentLocked reports whether the controller holding the epoch
// appears lost: it has not renewed for at least half of the last granted TTL,
// or the epoch is about to lapse. A live controller renews far more often.
func (manager *executionEpochManager) controllerSilentLocked(now time.Time) bool {
	if manager.executionExpiresAt.Sub(now) <= testLeaseOrphanWindow {
		return true
	}
	return !manager.lastRenewedAt.IsZero() && manager.lastGrantedTTL > 0 && now.Sub(manager.lastRenewedAt) >= manager.lastGrantedTTL/2
}

func (manager *executionEpochManager) planExpiryLocked(service *Service, generation uint64, expiresAt time.Time, token uint64, requireToken bool, now time.Time) (<-chan struct{}, []string, []string, bool) {
	if manager.pendingPersistence != nil {
		return manager.pendingPersistence.done, nil, nil, false
	}
	if requireToken && manager.executionEpochTimerToken != token {
		return nil, nil, nil, false
	}
	if manager.executionGeneration != generation || !manager.executionExpiresAt.Equal(expiresAt) || now.Before(manager.executionExpiresAt) {
		return nil, nil, nil, false
	}
	testIDs := make([]string, 0)
	var orphaned []string
	for testID, assignment := range service.assignments {
		if assignment.executionGeneration > generation {
			continue
		}
		if service.orphanLocked(testID, assignment, generation, now) {
			orphaned = append(orphaned, testID)
			continue
		}
		testIDs = append(testIDs, testID)
	}
	if len(testIDs) > 0 {
		manager.executionEpochFencing = true
	}
	return nil, testIDs, orphaned, true
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
	expiresAt := service.nowTime().Add(time.Duration(ttlMs) * time.Millisecond)
	if testID := strings.TrimSpace(claim.GetTestId()); testID != "" {
		if len(claim.GetAdoptions()) > 0 {
			return nil, status.Error(codes.InvalidArgument, "adoptions belong to a process-epoch claim, not a run-scoped lease")
		}
		return service.claimTestExecutionLease(ctx, testID, generation, leaseOwner, expiresAt)
	}
	if err := validateRunAdoptions(claim.GetAdoptions()); err != nil {
		return nil, err
	}
	return service.claimValidatedExecutionEpoch(ctx, generation, leaseOwner, expiresAt, claim.GetAdoptions()...)
}

func (service *Service) claimValidatedExecutionEpoch(ctx context.Context, generation uint64, leaseOwner string, expiresAt time.Time, adoptions ...*pb.RunAdoption) (*pb.ExecutionEpochAck, error) {
	for {
		service.mu.Lock()
		if err := ctx.Err(); err != nil {
			service.mu.Unlock()
			return nil, status.FromContextError(err).Err()
		}
		decision := service.executionEpochManager.planClaimLocked(service, generation, leaseOwner, expiresAt, service.nowTime(), adoptions)
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
				AdoptionSupported: true,
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
			AdoptionSupported: true,
			Adoptions:         decision.adoptionResults,
		}, nil
	}
	service.mu.Lock()
	final := service.executionEpochManager.finalizeClaimLocked(service, generation, service.nowTime())
	service.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	ack := &pb.ExecutionEpochAck{
		Accepted:          final.errorMessage == "",
		CurrentGeneration: final.currentGeneration,
		ExpiresAtUnixMs:   final.expiresAt.UnixMilli(),
		ErrorMessage:      final.errorMessage,
		AdoptionSupported: true,
		Adoptions:         decision.adoptionResults,
	}
	return ack, nil
}
