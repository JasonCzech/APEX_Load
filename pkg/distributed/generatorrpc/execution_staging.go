package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type assignmentStageDecisionKind uint8

const (
	assignmentStagePrepare assignmentStageDecisionKind = iota + 1
	assignmentStageAccepted
	assignmentStageRejected
	assignmentStageWait
)

type assignmentStageDecision struct {
	kind              assignmentStageDecisionKind
	plan              *assignmentStagePlan
	wait              <-chan struct{}
	errorMessage      string
	availableCapacity int32
	err               error
}

// pendingAssignmentStage is coordinator-owned reservation state. At most one
// group per test is prepared concurrently, which keeps late-group duration,
// archive-writer creation, and duplicate retry semantics deterministic.
type pendingAssignmentStage struct {
	token               uint64
	testID              string
	groupName           string
	executionGeneration uint64
	scriptID            string
	protocol            string
	vusers              int32
	done                chan struct{}
}

// assignmentStagePlan is an immutable execution-preparation snapshot. The
// request is synchronously RPC-owned and read-only; scriptContent is copied out
// of the mutable cache before the compatibility lock is released.
type assignmentStagePlan struct {
	token               uint64
	validated           validatedWorkAssignment
	hadCurrent          bool
	currentGuard        [sha256.Size]byte
	executionEnabled    bool
	scriptContent       []byte
	protocol            string
	executionDuration   time.Duration
	durationOffset      time.Duration
	nextDuration        time.Duration
	nextDurationSeconds int64
	nextLifecycle       assignmentLifecycleState
	nextCommitted       bool
	startPaused         bool
	needsArchive        bool
}

type preparedAssignmentStage struct {
	execution     *groupExecution
	writer        *archiveRecorder
	createdWriter bool
}

// AssignWork is the transport facade for the two-phase staging transition:
// bounded request validation, one coordinator reservation, unlocked execution
// preparation, and an atomic coordinator commit or rollback.
func (service *Service) AssignWork(ctx context.Context, assignment *pb.WorkAssignment) (*pb.AssignmentAck, error) {
	validated, err := validateWorkAssignment(assignment, service.scriptCache.maxBytes(), TransportSecurityFromEnv().AllowsSecretDelivery())
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	if validated.rejectionMessage != "" {
		service.mu.RLock()
		available := service.availableCapacityLocked()
		service.mu.RUnlock()
		return &pb.AssignmentAck{Accepted: false, ErrorMessage: validated.rejectionMessage, AvailableCapacity: available}, nil
	}

	// Retention cleanup is filesystem work and must happen before, never under,
	// the coordinator compatibility lock.
	service.sweepArchivedRecords(time.Now())
	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, status.FromContextError(err).Err()
			}
		}
		service.mu.Lock()
		decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
		service.mu.Unlock()
		if decision.err != nil {
			return nil, decision.err
		}
		switch decision.kind {
		case assignmentStageAccepted:
			return &pb.AssignmentAck{Accepted: true, AvailableCapacity: decision.availableCapacity}, nil
		case assignmentStageRejected:
			return &pb.AssignmentAck{Accepted: false, ErrorMessage: decision.errorMessage, AvailableCapacity: decision.availableCapacity}, nil
		case assignmentStageWait:
			if ctx == nil {
				<-decision.wait
				continue
			}
			select {
			case <-decision.wait:
				continue
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			}
		case assignmentStagePrepare:
		default:
			return nil, status.Error(codes.Internal, "assignment coordinator returned an invalid staging decision")
		}

		plan := decision.plan
		prepared, prepareErr := service.prepareAssignmentStage(ctx, plan)
		if prepareErr != nil {
			service.mu.Lock()
			service.lifecycleCoordinator.rollbackStageLocked(assignment.GetTestId(), plan.token)
			available := service.availableCapacityLocked()
			service.mu.Unlock()
			return &pb.AssignmentAck{Accepted: false, ErrorMessage: prepareErr.Error(), AvailableCapacity: available}, nil
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				service.cleanupPreparedAssignmentStage(plan, prepared)
				service.mu.Lock()
				service.lifecycleCoordinator.rollbackStageLocked(assignment.GetTestId(), plan.token)
				service.mu.Unlock()
				return nil, status.FromContextError(err).Err()
			}
		}

		service.metricsBroker.lockPublication()
		service.mu.Lock()
		batch, available, commitErr := service.lifecycleCoordinator.commitStageLocked(service, plan, prepared)
		service.mu.Unlock()
		if batch != nil {
			service.publishLifecycleTransitionLocked(batch)
		}
		service.metricsBroker.unlockPublication()
		if commitErr != nil {
			service.cleanupPreparedAssignmentStage(plan, prepared)
			service.mu.Lock()
			service.lifecycleCoordinator.rollbackStageLocked(assignment.GetTestId(), plan.token)
			service.mu.Unlock()
			return nil, commitErr
		}
		return &pb.AssignmentAck{Accepted: true, AvailableCapacity: available}, nil
	}
}

func (service *Service) waitForPendingStage(ctx context.Context, testID string) error {
	for {
		service.mu.RLock()
		pending := service.lifecycleCoordinator.pendingStages[testID]
		var done <-chan struct{}
		if pending != nil {
			done = pending.done
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

func (service *Service) waitForAllPendingStages(ctx context.Context) error {
	for {
		service.mu.RLock()
		waiters := make([]<-chan struct{}, 0, len(service.lifecycleCoordinator.pendingStages))
		for _, pending := range service.lifecycleCoordinator.pendingStages {
			if pending != nil {
				waiters = append(waiters, pending.done)
			}
		}
		service.mu.RUnlock()
		if len(waiters) == 0 {
			return nil
		}
		for _, done := range waiters {
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
}

func (coordinator *assignmentLifecycleCoordinator) reserveStageLocked(service *Service, validated validatedWorkAssignment) assignmentStageDecision {
	request := validated.request
	available := service.availableCapacityLocked()
	if err := service.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		return assignmentStageDecision{err: err, availableCapacity: available}
	}
	if request.GetExecutionGeneration() > 0 && strings.TrimSpace(request.GetExpectedScriptHash()) == "" {
		return assignmentStageDecision{err: status.Error(codes.InvalidArgument, "expected_script_hash is required for fenced assignments"), availableCapacity: available}
	}
	if service.draining {
		return rejectedAssignmentStage("generator is draining", available)
	}
	if service.archiveManager.deletingTest(request.GetTestId()) {
		return rejectedAssignmentStage("test id archive is being retired", available)
	}
	if _, stopping := coordinator.stoppingTests[request.GetTestId()]; stopping {
		return rejectedAssignmentStage("test id is stopping", available)
	}
	if control := coordinator.pendingControls[request.GetTestId()]; control != nil {
		return assignmentStageDecision{kind: assignmentStageWait, wait: control.done, availableCapacity: available}
	}
	if pending := coordinator.pendingStages[request.GetTestId()]; pending != nil {
		return assignmentStageDecision{kind: assignmentStageWait, wait: pending.done, availableCapacity: available}
	}

	current, currentExists := service.assignments[request.GetTestId()]
	if !currentExists && service.archiveManager.retainedTest(request.GetTestId()) {
		return rejectedAssignmentStage("test id already has a retained completed archive", available)
	}
	if currentExists {
		if current.executionGeneration != request.GetExecutionGeneration() {
			return assignmentStageDecision{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", current.executionGeneration), availableCapacity: available}
		}
		if existing, groupExists := current.groups[request.GetGroupName()]; groupExists {
			existingFingerprint, fingerprintExists := current.groupAssignments[request.GetGroupName()]
			existingSecretBundleID := current.groupSecretBundleIDs[request.GetGroupName()]
			if existing == request.GetVuserCount() && fingerprintExists && existingFingerprint == validated.fingerprint && existingSecretBundleID == request.GetSecretBundleId() {
				return assignmentStageDecision{kind: assignmentStageAccepted, availableCapacity: available}
			}
			return rejectedAssignmentStage("group is already assigned; use AdjustVUsers to resize it", available)
		}
	}

	if err := coordinator.assignmentObjectAdmissionErrorLocked(service, validated); err != nil {
		return assignmentStageDecision{err: status.Error(codes.ResourceExhausted, err.Error()), availableCapacity: available}
	}
	if len(request.GetScriptContent()) > 0 {
		if err := verifyExpectedScriptHash(request.GetExpectedScriptHash(), request.GetScriptContent()); err != nil {
			return rejectedAssignmentStage(err.Error(), available)
		}
		if err := service.cacheScriptLocked(request.GetScriptId(), request.GetScriptContent(), request.GetExpectedScriptHash()); err != nil {
			return rejectedAssignmentStage("cache inline script: "+err.Error(), available)
		}
	}
	cached, ok := service.touchScriptLocked(request.GetScriptId())
	if !ok {
		return rejectedAssignmentStage("script is not cached and no inline script content was provided", available)
	}
	if err := verifyExpectedScriptHash(request.GetExpectedScriptHash(), cached.content); err != nil {
		return rejectedAssignmentStage(err.Error(), available)
	}
	if instance, getErr := protocol.Get(cached.protocol); getErr == nil {
		if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok {
			capabilities := provider.RuntimeCapabilities()
			projected := int64(request.GetVuserCount()) + service.activeProtocolVUsersLocked(cached.protocol) + coordinator.pendingProtocolVUsersLocked(cached.protocol)
			if capabilities.MaxVUsers > 0 && projected > capabilities.MaxVUsers {
				return rejectedAssignmentStage(fmt.Sprintf("insufficient %s runtime capacity: maximum %d", cached.protocol, capabilities.MaxVUsers), available)
			}
		}
	}
	if service.activeVUsers64Locked()+coordinator.pendingVUsersLocked()+int64(request.GetVuserCount()) > int64(service.maxVUsers) {
		return rejectedAssignmentStage("insufficient generator capacity", available)
	}

	lateCommittedGroup := current.committed && service.executionEnabled
	staged, lifecycleErr := coordinator.stage(current, lateCommittedGroup)
	if lifecycleErr != nil {
		return assignmentStageDecision{err: status.Error(codes.FailedPrecondition, lifecycleErr.Error()), availableCapacity: available}
	}
	executionDuration := validated.targetDuration
	executionDurationOffset := validated.requestedDurationOffset
	existingDuration := assignedTestDuration(current)
	nextDuration := current.duration
	nextDurationSeconds := current.durationSeconds
	if lateCommittedGroup {
		if executionDuration <= 0 {
			executionDuration = existingDuration
		}
		if existingDuration > 0 && executionDuration > existingDuration {
			return rejectedAssignmentStage("late group remaining duration exceeds the test target duration", available)
		}
		if validated.requestedDurationOffset > 0 {
			if existingDuration > 0 && validated.logicalTargetDuration != existingDuration {
				return rejectedAssignmentStage("late group duration offset does not align with the test target duration", available)
			}
		} else if existingDuration > executionDuration {
			executionDurationOffset = existingDuration - executionDuration
		}
	} else if existingDuration == 0 && validated.logicalTargetDuration > 0 {
		nextDuration = validated.logicalTargetDuration
		nextDurationSeconds = compatibilityDurationSeconds(validated.logicalTargetDuration)
	} else if validated.logicalTargetDuration > 0 && existingDuration != validated.logicalTargetDuration {
		return rejectedAssignmentStage("all groups in a test must use the same target duration", available)
	}

	coordinator.nextStagePlanToken++
	if coordinator.nextStagePlanToken == 0 {
		coordinator.nextStagePlanToken++
	}
	token := coordinator.nextStagePlanToken
	pending := &pendingAssignmentStage{
		token:               token,
		testID:              request.GetTestId(),
		groupName:           request.GetGroupName(),
		executionGeneration: request.GetExecutionGeneration(),
		scriptID:            request.GetScriptId(),
		protocol:            cached.protocol,
		vusers:              request.GetVuserCount(),
		done:                make(chan struct{}),
	}
	coordinator.pendingStages[request.GetTestId()] = pending
	return assignmentStageDecision{
		kind: assignmentStagePrepare,
		plan: &assignmentStagePlan{
			token:               token,
			validated:           validated,
			hadCurrent:          currentExists,
			currentGuard:        assignmentControlFingerprint(current, currentExists),
			executionEnabled:    service.executionEnabled,
			scriptContent:       append([]byte(nil), cached.content...),
			protocol:            cached.protocol,
			executionDuration:   executionDuration,
			durationOffset:      executionDurationOffset,
			nextDuration:        nextDuration,
			nextDurationSeconds: nextDurationSeconds,
			nextLifecycle:       staged.lifecycle,
			nextCommitted:       staged.committed,
			startPaused:         current.paused || request.GetStartPaused(),
			needsArchive:        service.executionEnabled || nextDuration > 0,
		},
		availableCapacity: available,
	}
}

func rejectedAssignmentStage(message string, available int32) assignmentStageDecision {
	return assignmentStageDecision{kind: assignmentStageRejected, errorMessage: message, availableCapacity: available}
}

func (coordinator *assignmentLifecycleCoordinator) assignmentObjectAdmissionErrorLocked(service *Service, validated validatedWorkAssignment) error {
	request := validated.request
	_, testExists := service.assignments[request.GetTestId()]
	testCount := len(service.assignments)
	groupCount := 0
	executionCount := 0
	for _, assigned := range service.assignments {
		groupCount += len(assigned.groups)
	}
	for _, groups := range service.executions {
		executionCount += len(groups)
	}
	for testID := range coordinator.pendingStages {
		if _, active := service.assignments[testID]; !active {
			testCount++
		}
		groupCount++
		if service.executionEnabled {
			executionCount++
		}
	}
	if !testExists && testCount >= service.maxAssignedTests {
		return fmt.Errorf("generator active test limit exceeded (%d)", service.maxAssignedTests)
	}
	if groupCount >= service.maxAssignedGroups {
		return fmt.Errorf("generator active group limit exceeded (%d)", service.maxAssignedGroups)
	}
	if service.executionEnabled {
		if executionCount >= service.maxExecutions {
			return fmt.Errorf("generator active execution limit exceeded (%d)", service.maxExecutions)
		}
		return service.archiveManager.writerAdmissionError(request.GetTestId())
	}
	effectiveDuration := validated.targetDuration
	if existing, ok := service.assignments[request.GetTestId()]; ok && assignedTestDuration(existing) > effectiveDuration {
		effectiveDuration = assignedTestDuration(existing)
	}
	if effectiveDuration > 0 {
		return service.archiveManager.writerAdmissionError(request.GetTestId())
	}
	return nil
}

func (coordinator *assignmentLifecycleCoordinator) pendingVUsersLocked() int64 {
	var total int64
	for _, pending := range coordinator.pendingStages {
		if pending != nil {
			total += int64(pending.vusers)
		}
	}
	return total
}

func (coordinator *assignmentLifecycleCoordinator) pendingProtocolVUsersLocked(protocolName string) int64 {
	var total int64
	for _, pending := range coordinator.pendingStages {
		if pending != nil && strings.EqualFold(pending.protocol, protocolName) {
			total += int64(pending.vusers)
		}
	}
	return total
}

func (coordinator *assignmentLifecycleCoordinator) commitStageLocked(service *Service, plan *assignmentStagePlan, prepared *preparedAssignmentStage) (*pb.MetricsBatch, int32, error) {
	if plan == nil || plan.validated.request == nil {
		return nil, service.availableCapacityLocked(), status.Error(codes.Internal, "assignment stage plan is unavailable")
	}
	request := plan.validated.request
	pending := coordinator.pendingStages[request.GetTestId()]
	if pending == nil || pending.token != plan.token {
		return nil, service.availableCapacityLocked(), status.Error(codes.Aborted, "assignment stage reservation is no longer current")
	}
	if err := service.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		return nil, service.availableCapacityLocked(), err
	}
	if service.draining {
		return nil, service.availableCapacityLocked(), status.Error(codes.Unavailable, "generator is draining")
	}
	current, currentExists := service.assignments[request.GetTestId()]
	if currentExists != plan.hadCurrent || assignmentControlFingerprint(current, currentExists) != plan.currentGuard {
		return nil, service.availableCapacityLocked(), status.Error(codes.Aborted, "test assignment changed while execution was prepared")
	}
	if service.activeVUsers64Locked()+coordinator.pendingVUsersLocked() > int64(service.maxVUsers) {
		return nil, service.availableCapacityLocked(), status.Error(codes.ResourceExhausted, "insufficient generator capacity")
	}
	if instance, getErr := protocol.Get(plan.protocol); getErr == nil {
		if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok {
			capabilities := provider.RuntimeCapabilities()
			if capabilities.MaxVUsers > 0 && service.activeProtocolVUsersLocked(plan.protocol)+coordinator.pendingProtocolVUsersLocked(plan.protocol) > capabilities.MaxVUsers {
				return nil, service.availableCapacityLocked(), status.Errorf(codes.ResourceExhausted, "insufficient %s runtime capacity: maximum %d", plan.protocol, capabilities.MaxVUsers)
			}
		}
	}
	if plan.executionEnabled && (prepared == nil || prepared.execution == nil) {
		return nil, service.availableCapacityLocked(), status.Error(codes.Internal, "prepared execution is unavailable")
	}
	if current.groups != nil {
		if _, exists := current.groups[request.GetGroupName()]; exists {
			return nil, service.availableCapacityLocked(), status.Error(codes.Aborted, "assignment group changed while execution was prepared")
		}
	}

	next := cloneAssignmentForStage(current)
	next.testID = request.GetTestId()
	next.lifecycle = plan.nextLifecycle
	next.executionGeneration = request.GetExecutionGeneration()
	next.committed = plan.nextCommitted
	next.paused = plan.startPaused
	next.duration = plan.nextDuration
	next.durationSeconds = plan.nextDurationSeconds
	next.groups[request.GetGroupName()] += request.GetVuserCount()
	next.activeGroups[request.GetGroupName()] += request.GetVuserCount()
	next.groupAssignments[request.GetGroupName()] = plan.validated.fingerprint
	next.groupSecretBundleIDs[request.GetGroupName()] = request.GetSecretBundleId()
	next.groupScripts[request.GetGroupName()] = request.GetScriptId()
	next.vuserCount += request.GetVuserCount()

	if plan.executionEnabled {
		if service.executions[request.GetTestId()] == nil {
			service.executions[request.GetTestId()] = make(map[string]*groupExecution)
		}
		if service.executions[request.GetTestId()][request.GetGroupName()] != nil {
			return nil, service.availableCapacityLocked(), status.Error(codes.Aborted, "prepared execution group is no longer current")
		}
		service.executions[request.GetTestId()][request.GetGroupName()] = prepared.execution
	}
	service.assignments[request.GetTestId()] = next
	coordinator.finishPendingStageLocked(request.GetTestId(), plan.token)
	if prepared != nil && prepared.writer != nil {
		prepared.writer.bindExecution(request.GetExecutionGeneration(), request.GetGroupName())
	}
	coordinator.scheduleStageExpiryLocked(service, request.GetTestId())
	return service.metricsBatchLocked(request.GetTestId()), service.availableCapacityLocked(), nil
}

func (coordinator *assignmentLifecycleCoordinator) rollbackStageLocked(testID string, token uint64) bool {
	return coordinator.finishPendingStageLocked(testID, token)
}

func (coordinator *assignmentLifecycleCoordinator) finishPendingStageLocked(testID string, token uint64) bool {
	pending := coordinator.pendingStages[testID]
	if pending == nil || pending.token != token {
		return false
	}
	delete(coordinator.pendingStages, testID)
	close(pending.done)
	return true
}

func (coordinator *assignmentLifecycleCoordinator) configureStageTTL(ttl time.Duration) {
	if coordinator != nil && ttl > 0 {
		coordinator.stageTTL = ttl
	}
}

func (coordinator *assignmentLifecycleCoordinator) stageTTLValue() time.Duration {
	if coordinator == nil || coordinator.stageTTL <= 0 {
		return defaultAssignmentStageTTL
	}
	return coordinator.stageTTL
}

func (coordinator *assignmentLifecycleCoordinator) scheduleStageExpiryLocked(service *Service, testID string) {
	if coordinator == nil || strings.TrimSpace(testID) == "" || coordinator.stageTTL <= 0 || !service.hasStagedExecutionLocked(testID) {
		return
	}
	coordinator.stopStageTimerLocked(testID)
	coordinator.nextStageTimerToken++
	if coordinator.nextStageTimerToken == 0 {
		coordinator.nextStageTimerToken++
	}
	token := coordinator.nextStageTimerToken
	coordinator.stageTokens[testID] = token
	coordinator.stageTimers[testID] = time.AfterFunc(coordinator.stageTTL, func() {
		service.expireStagedAssignment(testID, token)
	})
}

func (coordinator *assignmentLifecycleCoordinator) stopStageTimerLocked(testID string) {
	if coordinator == nil {
		return
	}
	delete(coordinator.stageTokens, testID)
	timer := coordinator.stageTimers[testID]
	delete(coordinator.stageTimers, testID)
	if timer != nil {
		timer.Stop()
	}
}

func (coordinator *assignmentLifecycleCoordinator) claimStageExpiryLocked(service *Service, testID string, token uint64) bool {
	if coordinator == nil || coordinator.stageTokens[testID] != token || !service.hasStagedExecutionLocked(testID) {
		return false
	}
	coordinator.stopStageTimerLocked(testID)
	return true
}

func cloneAssignmentForStage(current assignedTest) assignedTest {
	next := current
	next.groups = cloneStringInt32Map(current.groups)
	next.activeGroups = cloneStringInt32Map(current.activeGroups)
	next.groupAssignments = cloneStringFingerprintMap(current.groupAssignments)
	next.groupSecretBundleIDs = cloneStringMap(current.groupSecretBundleIDs)
	next.groupScripts = cloneStringMap(current.groupScripts)
	if next.groups == nil {
		next.groups = make(map[string]int32)
	}
	if next.activeGroups == nil {
		next.activeGroups = make(map[string]int32)
	}
	if next.groupStats == nil {
		next.groupStats = make(map[string]GeneratorStats)
	}
	if next.groupTelemetry == nil {
		next.groupTelemetry = make(map[string]MetricsTelemetry)
	}
	if next.groupTransactions == nil {
		next.groupTransactions = make(map[string][]TransactionMetric)
	}
	if next.groupAssignments == nil {
		next.groupAssignments = make(map[string][sha256.Size]byte)
	}
	if next.groupSecretBundleIDs == nil {
		next.groupSecretBundleIDs = make(map[string]string)
	}
	if next.groupScripts == nil {
		next.groupScripts = make(map[string]string)
	}
	return next
}

func cloneStringInt32Map(values map[string]int32) map[string]int32 {
	if values == nil {
		return nil
	}
	clone := make(map[string]int32, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneStringFingerprintMap(values map[string][sha256.Size]byte) map[string][sha256.Size]byte {
	if values == nil {
		return nil
	}
	clone := make(map[string][sha256.Size]byte, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func assignmentControlFingerprint(assignment assignedTest, exists bool) [sha256.Size]byte {
	h := sha256.New()
	fingerprintWriteBool(h, exists)
	if !exists {
		return fingerprintDigest(h)
	}
	fingerprintWriteString(h, assignment.testID)
	fingerprintWriteUint64(h, uint64(assignment.lifecycle))
	fingerprintWriteUint64(h, assignment.executionGeneration)
	fingerprintWriteUint64(h, uint64(uint32(assignment.vuserCount)))
	fingerprintWriteBool(h, assignment.paused)
	fingerprintWriteBool(h, assignment.committed)
	fingerprintWriteUint64(h, uint64(assignment.duration))
	fingerprintWriteUint64(h, uint64(assignment.durationSeconds))
	names := make([]string, 0, len(assignment.groups))
	for name := range assignment.groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fingerprintWriteString(h, name)
		fingerprintWriteUint64(h, uint64(uint32(assignment.groups[name])))
		assignmentFingerprint := assignment.groupAssignments[name]
		fingerprintWriteBytes(h, assignmentFingerprint[:])
		fingerprintWriteString(h, assignment.groupSecretBundleIDs[name])
		fingerprintWriteString(h, assignment.groupScripts[name])
	}
	return fingerprintDigest(h)
}

func (service *Service) prepareAssignmentStage(ctx context.Context, plan *assignmentStagePlan) (_ *preparedAssignmentStage, err error) {
	if plan == nil || plan.validated.request == nil {
		return nil, status.Error(codes.Internal, "assignment stage plan is unavailable")
	}
	request := plan.validated.request
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, status.FromContextError(ctxErr).Err()
		}
	}
	prepared := &preparedAssignmentStage{}
	defer func() {
		if err != nil {
			service.cleanupPreparedAssignmentStage(plan, prepared)
		}
	}()

	if !plan.executionEnabled {
		if !plan.needsArchive {
			return prepared, nil
		}
		testName := request.GetScriptId()
		var header struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(plan.scriptContent, &header) == nil && strings.TrimSpace(header.Name) != "" {
			testName = header.Name
		}
		writer, created, archiveErr := service.archiveManager.ensureWriter(request.GetTestId(), testName)
		if archiveErr != nil {
			return nil, archiveErr
		}
		prepared.writer = writer
		prepared.createdWriter = created
		return prepared, nil
	}

	var script dsl.Script
	if err := json.Unmarshal(plan.scriptContent, &script); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode script: %v", err)
	}
	if instance, getErr := protocol.Get(strings.ToLower(strings.TrimSpace(script.Protocol))); getErr == nil {
		if provider, ok := instance.(protocol.RuntimeCapabilityProvider); ok && !provider.RuntimeCapabilities().Available {
			return nil, status.Errorf(codes.FailedPrecondition, "%s runtime is unavailable on this generator", script.Protocol)
		}
	}
	if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
		const maxValidationMessageBytes = 16 << 10
		message := strings.Join(issues, "; ")
		if len(message) > maxValidationMessageBytes {
			message = boundedtext.TruncateUTF8(message, maxValidationMessageBytes-len("...[truncated]")) + "...[truncated]"
		}
		return nil, status.Errorf(codes.InvalidArgument, "validate script: %s", message)
	}
	referencedSecrets, err := engine.ReferencedSecretNames(&script)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "inspect script secret references")
	}
	if err := validateAssignmentSecretReferences(referencedSecrets, request.GetSecretBundle()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	runLogic := script.Config.RunLogic
	if plan.validated.shardCount > 1 && (runLogic.GlobalIterations > 0 || runLogic.MaxTransactions > 0) {
		return nil, status.Error(codes.InvalidArgument, "global iteration/transaction goals require a single shard because no cross-generator goal coordinator is available")
	}
	if plan.durationOffset > 0 && (runLogic.Iterations > 0 || runLogic.GlobalIterations > 0 || runLogic.MaxTransactions > 0) {
		return nil, status.Error(codes.InvalidArgument, "bounded iteration/transaction goals cannot resume from a duration offset without a progress checkpoint")
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, status.FromContextError(ctxErr).Err()
		}
	}
	duration := plan.executionDuration
	if duration <= 0 {
		duration = time.Second
	}
	stagedDataFiles, err := assignmentDataFiles(request, &script, service.scriptCache.maxBytes())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "validate staged data files: %v", err)
	}
	redactionConfig, err := engine.NewRedactionConfig(redactionRulesFromProto(request.GetRedactionRules()))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "build redaction config: %v", err)
	}
	redactionConfig, err = engine.WithSecretValueRedactionChecked(redactionConfig, secretBundleValues(request.GetSecretBundle()))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "prepare runtime secret redaction: %v", err)
	}
	results := &transactionBuffer{lastDrainAt: time.Now()}
	writer, created, archiveErr := service.archiveManager.ensureWriter(request.GetTestId(), script.Name)
	if archiveErr != nil {
		return nil, status.Errorf(codes.Internal, "initialize archive recorder: %v", archiveErr)
	}
	prepared.writer = writer
	prepared.createdWriter = created
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name:            request.GetGroupName(),
			Script:          &script,
			VUsers:          int(request.GetVuserCount()),
			RampUp:          plan.validated.rampUp,
			RampDown:        plan.validated.rampDown,
			AllowZeroVUsers: request.GetVuserCount() == 0,
			InitStrategy:    plan.validated.initStrategy,
			DataShard: engine.DataShardConfig{
				Ordinal: request.GetShardOrdinal(),
				Count:   plan.validated.shardCount,
			},
			StagedDataFiles: stagedDataFiles,
			VUserIDOffset:   int(request.GetVuserIdOffset()),
			VUserIDSpan:     int(maxGeneratorVUsers),
			ResultSink:      combineSinks(results, writer),
			RedactionConfig: redactionConfig,
			SecretResolver:  secretBundleResolver(request.GetSecretBundle()),
		}},
		Duration:     duration,
		PreBaseline:  plan.validated.preBaseline,
		PostBaseline: plan.validated.postBaseline,
		StartPaused:  plan.startPaused,
	})
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "build execution engine: %v", err)
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, status.FromContextError(ctxErr).Err()
		}
	}
	prepared.execution = &groupExecution{
		groupName:           request.GetGroupName(),
		executionGeneration: request.GetExecutionGeneration(),
		engine:              mge,
		durationOffset:      plan.durationOffset,
		redactionConfig:     redactionConfig,
		results:             results,
		publisherDone:       make(chan struct{}),
		done:                make(chan struct{}),
	}
	return prepared, nil
}

func (service *Service) cleanupPreparedAssignmentStage(plan *assignmentStagePlan, prepared *preparedAssignmentStage) {
	if service == nil || plan == nil || prepared == nil || !prepared.createdWriter || prepared.writer == nil {
		return
	}
	testID := plan.validated.request.GetTestId()
	if writer := service.archiveManager.takeWriterIf(testID, prepared.writer); writer != nil {
		if err := writer.Discard(); err != nil {
			slog.Warn("discard failed execution archive", "test_id", testID, "error", err)
		}
	}
}
