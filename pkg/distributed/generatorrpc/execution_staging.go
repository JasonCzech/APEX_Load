package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	"google.golang.org/protobuf/proto"
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

func assignmentPreservesImportedCredentials(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		strings.TrimSpace(script.Metadata.Import.Source) == "" {
		return false
	}
	policy, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey].(string)
	return strings.EqualFold(
		strings.TrimSpace(policy),
		dsl.ImportPreservedCredentialPolicyPreserve,
	)
}

// assignmentRuntimePreflightError distinguishes a generator-specific runtime
// incompatibility from ordinary admission rejections. AssignWork exposes this
// condition as gRPC FailedPrecondition after rolling the staged reservation
// back, so controllers cannot mistake an incompatible generator for a normal
// negative-capacity acknowledgement.
type assignmentRuntimePreflightError struct {
	message string
}

func (e *assignmentRuntimePreflightError) Error() string {
	if e == nil {
		return "runtime preflight failed"
	}
	return e.message
}

// pendingAssignmentStage is coordinator-owned reservation state. At most one
// group per test is prepared concurrently, which keeps late-group duration,
// archive-writer creation, and duplicate retry semantics deterministic.
type pendingAssignmentStage struct {
	runtimeResources        assignmentRuntimeResources
	token                   uint64
	testID                  string
	groupName               string
	executionGeneration     uint64
	scriptID                string
	protocol                string
	vusers                  int32
	persistentJavaLifecycle bool
	legacyJavaLifecycle     bool
	persistentJavaDemand    int64
	done                    chan struct{}
}

// assignmentStagePlan is an immutable execution-preparation snapshot. The
// request is synchronously RPC-owned and read-only; scriptContent is copied out
// of the mutable cache before the compatibility lock is released.
type assignmentStagePlan struct {
	runtimeResources        assignmentRuntimeResources
	token                   uint64
	validated               validatedWorkAssignment
	hadCurrent              bool
	currentGuard            [sha256.Size]byte
	executionEnabled        bool
	scriptContent           []byte
	protocol                string
	persistentJavaLifecycle bool
	legacyJavaLifecycle     bool
	persistentJavaDemand    int64
	executionDuration       time.Duration
	durationOffset          time.Duration
	nextDuration            time.Duration
	nextDurationSeconds     int64
	nextLifecycle           assignmentLifecycleState
	nextCommitted           bool
	startPaused             bool
	needsArchive            bool
}

type preparedAssignmentStage struct {
	execution                       *groupExecution
	writer                          *archiveRecorder
	createdWriter                   bool
	browserDocumentCaptureRequested bool
	javaCapacity                    javaVuserCapacityObservation
}

// AssignWork is the transport facade for the two-phase staging transition:
// bounded request validation, one coordinator reservation, unlocked execution
// preparation, and an atomic coordinator commit or rollback.
func (service *Service) AssignWork(ctx context.Context, assignment *pb.WorkAssignment) (ack *pb.AssignmentAck, callErr error) {
	defer func() {
		if assignment != nil && (callErr != nil || !ack.GetAccepted()) {
			service.abortSourcePlanStage(assignment.TestId, assignment.ExecutionGeneration, assignment.RunSupervisionToken)
		}
	}()
	defer func() {
		if ack != nil && !ack.Accepted && assignment.GetRuntimeResourceClaim() != nil {
			ack.RuntimeResourceAdmission = runtimeResourceAdmission(assignment.RuntimeResourceClaim, nil)
		}
		if callErr == nil && ack.GetAccepted() && assignment.GetRuntimeResourceClaim() != nil {
			service.mu.RLock()
			current := service.assignments[assignment.GetTestId()]
			ack.RuntimeResourceAdmission = runtimeResourceAdmission(current.groupRuntimeResourceClaims[assignment.GetGroupName()], current.groupRuntimeResources[assignment.GetGroupName()])
			service.mu.RUnlock()
			if !ValidRuntimeResourceAdmission(assignment.RuntimeResourceClaim, ack.RuntimeResourceAdmission) {
				ack = nil
				callErr = status.Error(codes.Aborted, "runtime_resource_reservation_unavailable")
				return
			}
		}
		if callErr == nil && ack.GetAccepted() && assignment.GetJavaHost() != nil {
			service.mu.RLock()
			execution := service.executions[assignment.GetTestId()][assignment.GetGroupName()]
			service.mu.RUnlock()
			if execution == nil || execution.engine == nil {
				ack = nil
				callErr = status.Error(codes.Aborted, "java_host_reservation_unavailable")
				return
			}
			placements := execution.engine.JavaBrokerPlacements()
			caps := service.javaHostCapabilities()
			if len(placements) != 1 || caps == nil {
				ack = nil
				callErr = status.Error(codes.Aborted, "java_host_reservation_unavailable")
				return
			}
			ack.JavaHost = &pb.JavaHostPlacement{Selection: proto.Clone(assignment.GetJavaHost()).(*pb.JavaHostSelection), ReservationId: placements[0].ReservationID, Mode: caps.Mode, Containment: caps.Containment, MaxHosts: caps.MaxHosts, MaxVusers: caps.MaxVusers}
		}
	}()

	validated, err := validateWorkAssignment(assignment, service.scriptCache.maxBytes(), TransportSecurityFromEnv().AllowsSecretDelivery())
	if err != nil {
		return nil, err
	}
	if service.runSupervisionRequired && !validRunSupervisionToken(assignment.GetRunSupervisionToken()) {
		return nil, status.Errorf(codes.FailedPrecondition, "run_supervision_token must contain exactly %d non-zero bytes", RunSupervisionTokenSize)
	}
	if len(assignment.GetRunSupervisionToken()) > 0 && !validRunSupervisionToken(assignment.GetRunSupervisionToken()) {
		return nil, status.Errorf(codes.InvalidArgument, "run_supervision_token must contain exactly %d non-zero bytes", RunSupervisionTokenSize)
	}
	if target := assignment.GetArchiveSourceTarget(); target != "" && (!validArchiveSourceTarget(target) || !validRunSupervisionToken(assignment.GetRunSupervisionToken())) {
		return nil, status.Error(codes.InvalidArgument, "invalid archive source target or missing supervision token")
	}
	if err := validateAdoptionGrace(assignment); err != nil {
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
		javaCapacity := service.assignmentJavaVuserCapacityObservation(validated)
		service.mu.Lock()
		decision := service.lifecycleCoordinator.reserveStageLocked(service, validated, javaCapacity)
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
			if preflightErr, ok := prepareErr.(*assignmentRuntimePreflightError); ok {
				return nil, status.Error(codes.FailedPrecondition, preflightErr.Error())
			}
			switch status.Code(prepareErr) {
			case codes.Canceled, codes.DeadlineExceeded:
				return nil, prepareErr
			}
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

func (coordinator *assignmentLifecycleCoordinator) reserveStageLocked(service *Service, validated validatedWorkAssignment, capacityObservations ...javaVuserCapacityObservation) assignmentStageDecision {
	request := validated.request
	var javaCapacity javaVuserCapacityObservation
	if len(capacityObservations) > 0 {
		javaCapacity = capacityObservations[0]
	}
	available := service.availableCapacityLocked()
	if err := service.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		return assignmentStageDecision{err: err, availableCapacity: available}
	}
	if err := service.validatePodCapacityAssignmentLocked(request.GetTestId(), request.GetGroupName(), javaHostPreparedVUsers(request)); err != nil {
		return rejectedAssignmentStage(err.Error(), available)
	}
	if request.GetExecutionGeneration() == 0 && service.executionEpochManager.runSupervisionRestartFenceInherited {
		return assignmentStageDecision{err: status.Error(codes.FailedPrecondition, runSupervisionRestartFenceMessage), availableCapacity: available}
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
	if currentExists && SourcePlanDigest(current.sourcePlan) != SourcePlanDigest(request.SourcePlan) {
		return rejectedAssignmentStage("source-plan manifest changed", available)
	}
	if current.committed && current.sourcePlan != nil {
		if _, exists := current.groups[request.GroupName]; !exists {
			return rejectedAssignmentStage("source-plan membership is sealed", available)
		}
	}
	if !currentExists && service.archiveManager.retainedTest(request.GetTestId()) {
		return rejectedAssignmentStage("test id already has a retained completed archive", available)
	}
	if currentExists {
		if current.archiveSourceTarget != request.GetArchiveSourceTarget() {
			return rejectedAssignmentStage("all groups in a test must use the same archive source target", available)
		}
		if dsl.CanonicalTerminationMode(current.terminationMode) != dsl.CanonicalTerminationMode(request.GetTerminationMode()) || (current.terminationMode == "iterations" && current.committed) {
			return rejectedAssignmentStage("iteration runs cannot admit late groups or mixed termination modes", available)
		}
		if current.executionGeneration != request.GetExecutionGeneration() {
			return assignmentStageDecision{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", current.executionGeneration), availableCapacity: available}
		}
		if current.requireTestExecutionLease != request.GetRequireTestExecutionLease() {
			return assignmentStageDecision{err: status.Error(codes.FailedPrecondition, "all groups in a test must use the same assignment lease policy"), availableCapacity: available}
		}
		if !sameRunSupervisionTokenHash(current.runSupervisionTokenHash, runSupervisionTokenHash(request.GetRunSupervisionToken())) {
			return assignmentStageDecision{err: status.Error(codes.FailedPrecondition, "all groups in a test must use the same run supervision token"), availableCapacity: available}
		}
		if current.adoptionGrace != service.effectiveAdoptionGrace(request) {
			return assignmentStageDecision{err: status.Error(codes.FailedPrecondition, "all groups in a test must use the same adoption grace"), availableCapacity: available}
		}
		if existing, groupExists := current.groups[request.GetGroupName()]; groupExists {
			existingFingerprint, fingerprintExists := current.groupAssignments[request.GetGroupName()]
			existingSecretBundleID := current.groupSecretBundleIDs[request.GetGroupName()]
			if existing == request.GetVuserCount() && fingerprintExists && sameSHA256Digest(existingFingerprint, validated.fingerprint) && existingSecretBundleID == request.GetSecretBundleId() {
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
	persistentJavaDemand := int64(0)
	if request.GetJavaHost() == nil && cached.requiresPhaseLifecycle {
		if strings.EqualFold(strings.TrimSpace(validated.initStrategy), "at_once") {
			return rejectedAssignmentStage("init_strategy at_once is incompatible with persistent java_vuser lifecycle; use at_runtime", available)
		}
		persistentJavaDemand = cached.javaVuserCapacity.Demand(int(request.GetVuserCount()))
		if service.legacyJavaRuntimeOccupiedLocked() {
			return rejectedAssignmentStage("persistent java_vuser lifecycle cannot share generator process slots with an active legacy full_lifecycle assignment", available)
		}
		if maxVUsers, ok := javaCapacity.persistentMaxVUsers(); ok {
			projected := service.activePersistentJavaVusersLocked() +
				coordinator.pendingPersistentJavaVusersLocked() +
				persistentJavaDemand
			if projected > maxVUsers {
				return rejectedAssignmentStage(fmt.Sprintf("insufficient java_vuser runtime capacity: maximum %d", maxVUsers), available)
			}
		}
	} else if request.GetJavaHost() == nil && strings.EqualFold(cached.protocol, "java_vuser") &&
		service.persistentJavaRuntimeOccupiedLocked() {
		return rejectedAssignmentStage("legacy java_vuser full_lifecycle cannot share generator process slots with an active persistent lifecycle assignment", available)
	}
	// Persistent Java capacity is accounted above from the unlocked
	// observation. Legacy full_lifecycle actions still time-share process slots
	// and have no pinned placement demand. Other protocol capability providers
	// retain their existing admission path.
	if !strings.EqualFold(cached.protocol, "java_vuser") {
		if instance, getErr := protocol.Get(cached.protocol); getErr == nil {
			if capabilities, supported, capabilityErr := protocol.ReadRuntimeCapabilities(instance); capabilityErr != nil {
				return assignmentStageDecision{err: status.Error(codes.FailedPrecondition, capabilityErr.Error()), availableCapacity: available}
			} else if supported && capabilities.MaxVUsers > 0 {
				projected := int64(request.GetVuserCount()) + service.activeProtocolVUsersLocked(cached.protocol) + coordinator.pendingProtocolVUsersLocked(cached.protocol)
				if projected > capabilities.MaxVUsers {
					return rejectedAssignmentStage(fmt.Sprintf("insufficient %s runtime capacity: maximum %d", cached.protocol, capabilities.MaxVUsers), available)
				}
			}
		}
	}
	additional := int64(javaHostPreparedVUsers(request))
	if request.SourcePlan != nil {
		additional = sourcePlanUsers(request.SourcePlan)
		if currentExists {
			additional = 0
		}
	}
	if service.capacityVUsers64Locked()+coordinator.pendingVUsersLocked()+additional > int64(service.maxVUsers) {
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

	var runtimeResources assignmentRuntimeResources
	if cached.protocol == "eventhubs" || request.RuntimeResourceClaim != nil {
		var resourceErr error
		runtimeResources, resourceErr = reserveAssignmentRuntimeResourcesLocked(cached.content, validated.initStrategy, request)
		if resourceErr != nil {
			return rejectedAssignmentStage(resourceErr.Error(), available)
		}
	}
	coordinator.nextStagePlanToken++
	if coordinator.nextStagePlanToken == 0 {
		coordinator.nextStagePlanToken++
	}
	token := coordinator.nextStagePlanToken
	pending := &pendingAssignmentStage{
		runtimeResources: runtimeResources, token: token,
		testID:                  request.GetTestId(),
		groupName:               request.GetGroupName(),
		executionGeneration:     request.GetExecutionGeneration(),
		scriptID:                request.GetScriptId(),
		protocol:                cached.protocol,
		vusers:                  int32(additional),
		persistentJavaLifecycle: request.GetJavaHost() == nil && cached.requiresPhaseLifecycle,
		legacyJavaLifecycle:     request.GetJavaHost() == nil && strings.EqualFold(cached.protocol, "java_vuser") && !cached.requiresPhaseLifecycle,
		persistentJavaDemand:    persistentJavaDemand,
		done:                    make(chan struct{}),
	}
	coordinator.pendingStages[request.GetTestId()] = pending
	return assignmentStageDecision{
		kind: assignmentStagePrepare,
		plan: &assignmentStagePlan{
			runtimeResources: runtimeResources, token: token,
			validated:               validated,
			hadCurrent:              currentExists,
			currentGuard:            assignmentControlFingerprint(current, currentExists),
			executionEnabled:        service.executionEnabled,
			scriptContent:           append([]byte(nil), cached.content...),
			protocol:                cached.protocol,
			persistentJavaLifecycle: request.GetJavaHost() == nil && cached.requiresPhaseLifecycle,
			legacyJavaLifecycle:     request.GetJavaHost() == nil && strings.EqualFold(cached.protocol, "java_vuser") && !cached.requiresPhaseLifecycle,
			persistentJavaDemand:    persistentJavaDemand,
			executionDuration:       executionDuration,
			durationOffset:          executionDurationOffset,
			nextDuration:            nextDuration,
			nextDurationSeconds:     nextDurationSeconds,
			nextLifecycle:           staged.lifecycle,
			nextCommitted:           staged.committed,
			startPaused:             current.paused || request.GetStartPaused(),
			needsArchive:            service.executionEnabled || nextDuration > 0,
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
	if err := service.validatePodCapacityAssignmentLocked(request.GetTestId(), request.GetGroupName(), javaHostPreparedVUsers(request)); err != nil {
		return nil, service.availableCapacityLocked(), status.Error(codes.ResourceExhausted, err.Error())
	}
	if request.RuntimeResourceClaim != nil && !ValidRuntimeResourceAdmission(request.RuntimeResourceClaim, runtimeResourceAdmission(request.RuntimeResourceClaim, plan.runtimeResources.reservation)) {
		return nil, service.availableCapacityLocked(), status.Error(codes.FailedPrecondition, "runtime resource reservation changed")
	}
	current, currentExists := service.assignments[request.GetTestId()]
	if currentExists && SourcePlanDigest(current.sourcePlan) != SourcePlanDigest(request.SourcePlan) {
		return nil, service.availableCapacityLocked(), status.Error(codes.Aborted, "source-plan manifest changed")
	}
	if current.committed && current.sourcePlan != nil {
		if _, exists := current.groups[request.GroupName]; !exists {
			return nil, service.availableCapacityLocked(), status.Error(codes.Aborted, "source-plan membership is sealed")
		}
	}
	if currentExists != plan.hadCurrent || !sameSHA256Digest(assignmentControlFingerprint(current, currentExists), plan.currentGuard) {
		return nil, service.availableCapacityLocked(), status.Error(codes.Aborted, "test assignment changed while execution was prepared")
	}
	if service.capacityVUsers64Locked()+coordinator.pendingVUsersLocked() > int64(service.maxVUsers) {
		return nil, service.availableCapacityLocked(), status.Error(codes.ResourceExhausted, "insufficient generator capacity")
	}
	if strings.EqualFold(plan.protocol, "java_vuser") {
		if plan.persistentJavaLifecycle && service.legacyJavaRuntimeOccupiedLocked() {
			return nil, service.availableCapacityLocked(), status.Error(codes.ResourceExhausted, "persistent java_vuser lifecycle cannot share generator process slots with an active legacy full_lifecycle assignment")
		}
		if plan.legacyJavaLifecycle && service.persistentJavaRuntimeOccupiedLocked() {
			return nil, service.availableCapacityLocked(), status.Error(codes.ResourceExhausted, "legacy java_vuser full_lifecycle cannot share generator process slots with an active persistent lifecycle assignment")
		}
	}
	if plan.persistentJavaLifecycle {
		if prepared == nil {
			return nil, service.availableCapacityLocked(), status.Error(codes.Internal, "prepared Java runtime capacity is unavailable")
		}
		if maxVUsers, ok := prepared.javaCapacity.persistentMaxVUsers(); ok {
			occupied := service.activePersistentJavaVusersLocked() + coordinator.pendingPersistentJavaVusersLocked()
			if occupied > maxVUsers {
				return nil, service.availableCapacityLocked(), status.Errorf(codes.ResourceExhausted, "insufficient java_vuser runtime capacity: maximum %d", maxVUsers)
			}
		} else if plan.executionEnabled {
			return nil, service.availableCapacityLocked(), status.Error(codes.FailedPrecondition, prepared.javaCapacity.persistentRuntimeError().Error())
		}
	}
	if !strings.EqualFold(plan.protocol, "java_vuser") {
		if instance, getErr := protocol.Get(plan.protocol); getErr == nil {
			if capabilities, supported, capabilityErr := protocol.ReadRuntimeCapabilities(instance); capabilityErr != nil {
				return nil, service.availableCapacityLocked(), status.Error(codes.FailedPrecondition, capabilityErr.Error())
			} else if supported && capabilities.MaxVUsers > 0 {
				occupied := service.activeProtocolVUsersLocked(plan.protocol) + coordinator.pendingProtocolVUsersLocked(plan.protocol)
				if occupied > capabilities.MaxVUsers {
					return nil, service.availableCapacityLocked(), status.Errorf(codes.ResourceExhausted, "insufficient %s runtime capacity: maximum %d", plan.protocol, capabilities.MaxVUsers)
				}
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
	if request.SourcePlan != nil {
		next.sourcePlan = cloneSourcePlan(request.SourcePlan)
	}
	next.testID = request.GetTestId()
	next.archiveSourceTarget = request.GetArchiveSourceTarget()
	next.terminationMode = request.GetTerminationMode()
	next.lifecycle = plan.nextLifecycle
	next.executionGeneration = request.GetExecutionGeneration()
	next.requireTestExecutionLease = request.GetRequireTestExecutionLease()
	if len(request.GetRunSupervisionToken()) > 0 {
		next.runSupervisionTokenHash = runSupervisionTokenHash(request.GetRunSupervisionToken())
	}
	next.adoptionGrace = service.effectiveAdoptionGrace(request)
	next.committed = plan.nextCommitted
	next.paused = plan.startPaused
	next.duration = plan.nextDuration
	next.durationSeconds = plan.nextDurationSeconds
	next.groups[request.GetGroupName()] += request.GetVuserCount()
	if plan.executionEnabled {
		// Configured assignment capacity is not live load. The execution engine
		// publishes authoritative zero/ramping counts after commit; seeding this
		// gauge with the target makes unstarted peer groups appear fully active.
		next.activeGroups[request.GetGroupName()] = 0
	} else {
		// Preserve the legacy capacity-only service contract when no execution
		// engine exists to publish live gauges.
		next.activeGroups[request.GetGroupName()] += request.GetVuserCount()
	}
	if len(next.groupAssignments) == 0 {
		next.prepareOnly = request.GetPrepareOnly()
		if claim := service.podCapacityClaims[request.GetTestId()]; claim != nil && next.prepareOnly {
			next.preparationCapacityClaim = proto.Clone(claim).(*pb.PodCapacityClaim)
		}
	} else {
		next.prepareOnly = next.prepareOnly && request.GetPrepareOnly()
	}
	if plan.runtimeResources.managed {
		next.groupRuntimeResourcePeaks[request.GroupName] = plan.runtimeResources.peak
		if request.RuntimeResourceClaim != nil {
			next.groupRuntimeResourceClaims[request.GroupName] = CloneRuntimeResourceClaim(request.RuntimeResourceClaim)
		}
		if plan.runtimeResources.reservation != nil {
			next.groupRuntimeResources[request.GroupName] = plan.runtimeResources.reservation
		}
	}
	next.groupAssignments[request.GetGroupName()] = plan.validated.fingerprint
	next.groupSecretBundleIDs[request.GetGroupName()] = request.GetSecretBundleId()
	next.groupScripts[request.GetGroupName()] = request.GetScriptId()
	if request.GetJavaHost() != nil {
		next.groupJavaHosts[request.GetGroupName()] = request.GetJavaHost().PolicySha256
		next.groupJavaHostPrepared[request.GetGroupName()] = request.GetJavaHost().PreparedVusers
	}
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
	pending.runtimeResources.reservation = nil
	coordinator.finishPendingStageLocked(request.GetTestId(), plan.token)
	if prepared != nil && prepared.writer != nil {
		prepared.writer.bindExecution(request.GetExecutionGeneration(), request.GetGroupName(), ArchiveSourceIdentity(request.GetTestId(), request.GetRunSupervisionToken(), request.GetArchiveSourceTarget()))
		prepared.writer.bindBrowserDocumentCapture(prepared.browserDocumentCaptureRequested)
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
	if pending.runtimeResources.reservation != nil {
		pending.runtimeResources.reservation.Release()
		pending.runtimeResources.reservation = nil
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
	if coordinator.stageExpiresAt == nil {
		coordinator.stageExpiresAt = make(map[string]time.Time)
	}
	coordinator.stageExpiresAt[testID] = time.Now().Add(coordinator.stageTTL)
	coordinator.stageTimers[testID] = time.AfterFunc(coordinator.stageTTL, func() {
		service.expireStagedAssignment(testID, token)
	})
}

func (coordinator *assignmentLifecycleCoordinator) stopStageTimerLocked(testID string) {
	if coordinator == nil {
		return
	}
	delete(coordinator.stageTokens, testID)
	delete(coordinator.stageExpiresAt, testID)
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
	next.groupRuntimeResourceClaims = CloneRuntimeResourceClaims(current.groupRuntimeResourceClaims)
	if next.groupRuntimeResourceClaims == nil {
		next.groupRuntimeResourceClaims = map[string]*pb.RuntimeResourceClaim{}
	}
	next.groupRuntimeResourcePeaks = cloneStringInt32Map(current.groupRuntimeResourcePeaks)
	if next.groupRuntimeResourcePeaks == nil {
		next.groupRuntimeResourcePeaks = map[string]int32{}
	}
	next.groupRuntimeResources = make(map[string]protocol.RuntimeResourceReservation, len(current.groupRuntimeResources))
	for name, reservation := range current.groupRuntimeResources {
		next.groupRuntimeResources[name] = reservation
	}
	next.groups = cloneStringInt32Map(current.groups)
	next.activeGroups = cloneStringInt32Map(current.activeGroups)
	next.groupAssignments = cloneStringFingerprintMap(current.groupAssignments)
	next.groupSecretBundleIDs = cloneStringMap(current.groupSecretBundleIDs)
	next.groupScripts = cloneStringMap(current.groupScripts)
	next.groupJavaHostPrepared = cloneStringInt32Map(current.groupJavaHostPrepared)
	if next.groupJavaHostPrepared == nil {
		next.groupJavaHostPrepared = map[string]int32{}
	}
	next.groupJavaHosts = cloneStringMap(current.groupJavaHosts)
	if next.groupJavaHosts == nil {
		next.groupJavaHosts = make(map[string]string)
	}
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
	fingerprintWriteString(h, assignment.archiveSourceTarget)
	fingerprintWriteUint64(h, uint64(assignment.lifecycle))
	fingerprintWriteUint64(h, assignment.executionGeneration)
	fingerprintWriteBool(h, assignment.requireTestExecutionLease)
	fingerprintWriteBytes(h, assignment.runSupervisionTokenHash[:])
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
		peak, managed := assignment.groupRuntimeResourcePeaks[name]
		fingerprintWriteBool(h, managed)
		fingerprintWriteUint64(h, uint64(peak))
		fingerprintWriteString(h, assignment.groupJavaHosts[name])
		fingerprintWriteUint64(h, uint64(assignment.groupJavaHostPrepared[name]))
	}
	return fingerprintDigest(h)
}

func (service *Service) prepareAssignmentStage(ctx context.Context, plan *assignmentStagePlan) (_ *preparedAssignmentStage, err error) {
	if plan == nil || plan.validated.request == nil {
		return nil, status.Error(codes.Internal, "assignment stage plan is unavailable")
	}
	request := plan.validated.request
	// Reject before any runtime capacity provider or helper can be invoked.
	if request.GetPrepareOnly() && (request.GetJavaHost() != nil || plan.protocol == "java_vuser" || plan.protocol == "bruno") {
		return nil, status.Error(codes.FailedPrecondition, "runtime cannot guarantee traffic-free preparation")
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, status.FromContextError(ctxErr).Err()
		}
	}
	prepared := &preparedAssignmentStage{}
	if request.GetJavaHost() != nil && (!plan.executionEnabled || service.javaHostRuntime == nil) {
		return nil, status.Error(codes.FailedPrecondition, "java_host_backend_unavailable")
	}
	if plan.persistentJavaLifecycle {
		// Capability providers may probe a JVM or sidecar. Preparation runs
		// outside the service coordinator lock so the probe cannot stall
		// assignments, heartbeats, or control operations.
		prepared.javaCapacity = observeJavaVuserCapacity()
	}
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
	prepared.browserDocumentCaptureRequested = len(dsl.RequiredBrowserRuntimeFeatures(&script)) > 0
	if len(request.GetRunCorrelationOrigins()) > 0 {
		if err := engine.ValidateRunCorrelationHeaders(&script, request.GetTestId()); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	if err := validateSourcePlanScript(request, &script); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	javaHost, hostErr := service.javaHostGroupOptions(request, &script, &script)
	if hostErr != nil {
		return nil, status.Error(codes.FailedPrecondition, hostErr.Error())
	}
	applyAssignmentMessageDetailBudget(&script, request)
	if javaHost != nil {
		javaHost.ScriptSHA256, err = engine.JavaPooledScriptSHA256(&script)
		if err != nil {
			return nil, err
		}
	}
	var instance protocol.Protocol
	var getErr error
	if javaHost == nil {
		instance, getErr = protocol.Get(strings.ToLower(strings.TrimSpace(script.Protocol)))
	}
	if javaHost == nil && getErr == nil {
		if strings.EqualFold(script.Protocol, "java_vuser") {
			observation := prepared.javaCapacity
			if !observation.probed {
				capabilities, supported, capabilityErr := protocol.ReadRuntimeCapabilities(instance)
				observation = javaVuserCapacityObservation{
					probed:       true,
					capabilities: capabilities,
					supported:    supported,
					err:          capabilityErr,
				}
			}
			if observation.err != nil {
				return nil, status.Error(codes.FailedPrecondition, observation.err.Error())
			}
			if admissionErr := javaVuserDistributedContainmentError(observation.capabilities, observation.supported); admissionErr != nil {
				return nil, status.Error(codes.FailedPrecondition, admissionErr.Error())
			}
			prepared.javaCapacity = observation
			if requiredProtocolRuntimeFeature(&script) == "" {
				legacyAvailable, supported := legacyRuntimeAvailable(instance)
				if !supported || !legacyAvailable {
					return nil, status.Errorf(codes.FailedPrecondition, "%s legacy one-shot runtime is unavailable on this generator", script.Protocol)
				}
			} else if runtimeErr := prepared.javaCapacity.persistentRuntimeError(); runtimeErr != nil {
				return nil, status.Error(codes.FailedPrecondition, runtimeErr.Error())
			}
		} else {
			capabilities, hasCapabilities, capabilityErr := protocol.ReadRuntimeCapabilities(instance)
			if capabilityErr != nil {
				return nil, status.Error(codes.FailedPrecondition, capabilityErr.Error())
			}
			if strings.EqualFold(script.Protocol, "bruno") &&
				(!hasCapabilities || !brunoRuntimeContained(capabilities.Features)) {
				return nil, status.Error(codes.FailedPrecondition, "bruno runtime does not advertise required runner owner and session lease capabilities")
			}
			if hasCapabilities {
				if !capabilities.Available {
					return nil, status.Errorf(codes.FailedPrecondition, "%s runtime is unavailable on this generator", script.Protocol)
				}
				if required := requiredProtocolRuntimeFeature(&script); required != "" && !protocol.RuntimeFeaturesSatisfy(capabilities.Features, required) {
					return nil, status.Errorf(codes.FailedPrecondition, "%s runtime does not advertise required capability %s", script.Protocol, required)
				}
			} else if required := requiredProtocolRuntimeFeature(&script); required != "" {
				return nil, status.Errorf(codes.FailedPrecondition, "%s runtime does not advertise required capability %s", script.Protocol, required)
			}
		}
	}
	validationIssues := dsl.ValidateScriptIssues(&script)
	if assignmentPreservesImportedCredentials(&script) {
		// Assignments originate from the controller's durable script snapshot.
		// A server-attested import may retain exact credential-bearing JVM argv;
		// the trusted validator still rejects every structural/process-control
		// option violation.
		validationIssues = dsl.ValidateScriptIssuesWithPreservedImportCredentials(&script)
	}
	if len(validationIssues) > 0 {
		const maxValidationMessageBytes = 16 << 10
		message := strings.Join(validationIssues, "; ")
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
	if duration <= 0 && request.GetTerminationMode() != "iterations" {
		duration = time.Second
	}
	if err := dsl.ValidateTermination(request.GetTerminationMode(), duration, &script); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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
	// Retained import credentials are always inspected so malformed locator
	// metadata is rejected at the staging boundary. Whether their values also join
	// the run-output matcher is the operator's call, carried by the script's own
	// Config.Log.RedactRunEvidence — so it travels with scriptContent and changes
	// the assignment fingerprint for free, with no proto field of its own.
	//
	// The platform-managed secret bundle above is registered first and is never
	// gated. A script-sourced overflow drops the script-sourced contribution
	// rather than rejecting the assignment.
	importedCredentials, err := engine.ImportedCredentialValues(&script)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "inspect retained import credentials")
	}
	if engine.ScriptRedactsRunEvidence(&script) && len(importedCredentials) > 0 {
		scriptSourcedRedaction, scriptSourcedErr := engine.WithScriptSourcedSecretValueRedaction(
			redactionConfig,
			importedCredentials,
		)
		if scriptSourcedErr != nil {
			slog.Warn("script-sourced run-evidence redaction was dropped",
				"test_id", request.GetTestId(),
				"script", script.Name,
			)
		} else {
			redactionConfig = scriptSourcedRedaction
		}
	}
	if request.GetPrepareOnly() && (javaHost != nil || script.Protocol == "java_vuser" || script.Protocol == "bruno") {
		return nil, status.Error(codes.FailedPrecondition, "runtime does not support traffic-free preparation")
	}
	runtimeSecrets := secretBundleResolver(request.GetSecretBundle())
	var preflight protocol.RuntimePreflightResult
	var preflightErr error
	if javaHost == nil && !request.GetPrepareOnly() {
		preflight, preflightErr = engine.PreflightScriptRuntime(ctx, &script, runtimeSecrets)
	}
	if preflightErr != nil {
		if ctx != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, status.FromContextError(ctxErr).Err()
			}
		}
		if errors.Is(preflightErr, context.Canceled) || errors.Is(preflightErr, context.DeadlineExceeded) {
			return nil, status.FromContextError(preflightErr).Err()
		}
		message := redactionConfig.ScrubText(preflightErr.Error())
		const maxRuntimePreflightMessageBytes = 16 << 10
		if len(message) > maxRuntimePreflightMessageBytes {
			message = boundedtext.TruncateUTF8(message, maxRuntimePreflightMessageBytes-len("...[truncated]")) + "...[truncated]"
		}
		return nil, &assignmentRuntimePreflightError{message: fmt.Sprintf("%s runtime preflight failed: %s", script.Protocol, message)}
	}
	for _, warning := range preflight.Warnings {
		slog.Warn("assignment runtime preflight warning", "protocol", script.Protocol, "code", normalizedRuntimeWarningCode(warning))
	}
	results := &transactionBuffer{lastDrainAt: time.Now()}
	writer, created, archiveErr := service.archiveManager.ensureWriter(request.GetTestId(), script.Name)
	if archiveErr != nil {
		return nil, status.Errorf(codes.Internal, "initialize archive recorder: %v", archiveErr)
	}
	prepared.writer = writer
	prepared.createdWriter = created
	if engine.HasAllocatedParameters(&script) && (request.GetParameterPopulation() == 0 || uint64(request.GetParameterOffset())+uint64(request.GetVuserCount()) > uint64(request.GetParameterPopulation())) {
		return nil, fmt.Errorf("invalid distributed parameter allocation")
	}
	groupConfig := engine.MultiGroupConfig{
		TerminationMode: request.GetTerminationMode(),
		Groups: []engine.GroupConfig{{
			RuntimeResourcePeakFrozen:    plan.runtimeResources.managed,
			RuntimeResourcePeakVUsers:    int(plan.runtimeResources.peak),
			RuntimeResourceReservation:   plan.runtimeResources.reservation,
			JavaBroker:                   javaHost,
			Name:                         request.GetGroupName(),
			StartOffset:                  plan.validated.startOffset,
			Region:                       strings.TrimSpace(request.GetRegion()),
			RunID:                        request.GetTestId(),
			ScenarioID:                   request.GetScenarioId(),
			RunCorrelationOrigins:        append([]string(nil), request.GetRunCorrelationOrigins()...),
			CorrelationCarriers:          append([]string(nil), request.GetCorrelationCarriers()...),
			CorrelationTestName:          request.GetCorrelationTestName(),
			Script:                       &script,
			VUsers:                       int(request.GetVuserCount()),
			RampUp:                       plan.validated.rampUp,
			RampDown:                     plan.validated.rampDown,
			AllowZeroVUsers:              request.GetVuserCount() == 0,
			InitStrategy:                 plan.validated.initStrategy,
			TransactionResponseTimeSLAMs: request.GetTransactionResponseTimeSlaMs(),
			DataShard: engine.DataShardConfig{
				Ordinal: request.GetShardOrdinal(),
				Count:   plan.validated.shardCount,
			},
			StagedDataFiles:     stagedDataFiles,
			VUserIDOffset:       int(request.GetVuserIdOffset()),
			ParameterAllocation: engine.ParameterAllocation{Population: int(request.GetParameterPopulation()), Offset: int(request.GetParameterOffset()), Iterations: script.Config.RunLogic.Iterations},
			VUserIDSpan:         int(maxGeneratorVUsers),
			ResultSink:          combineSinks(writer, results),
			RedactionConfig:     redactionConfig,
			SecretResolver:      runtimeSecrets,
		}},
		Duration:     duration,
		PreBaseline:  plan.validated.preBaseline,
		PostBaseline: plan.validated.postBaseline,
		StartPaused:  plan.startPaused,
	}
	mge, err := engine.NewMultiGroup(groupConfig)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "build execution engine: %v", err)
	}
	prepared.execution = &groupExecution{
		javaHost:            javaHost != nil,
		stagedConfig:        &groupConfig,
		groupName:           request.GetGroupName(),
		executionGeneration: request.GetExecutionGeneration(),
		engine:              mge,
		durationOffset:      plan.durationOffset,
		redactionConfig:     redactionConfig,
		results:             results,
		publisherDone:       make(chan struct{}),
		done:                make(chan struct{}),
	}
	if javaHost != nil {
		if err := mge.PreflightRuntime(ctx); err != nil {
			return nil, status.Error(codes.FailedPrecondition, "java_host_preparation_failed")
		}
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, status.FromContextError(ctxErr).Err()
		}
	}
	return prepared, nil
}

func applyAssignmentMessageDetailBudget(script *dsl.Script, request *pb.WorkAssignment) {
	if script == nil || request == nil || script.Config.Results.MessageDetail == nil {
		return
	}
	detail := script.Config.Results.MessageDetail
	if value := int(request.GetMessageDetailMaxItems()); value > 0 {
		detail.MaxItems = min(detail.MaxItems, value)
		if detail.MaxItems <= 0 {
			detail.MaxItems = value
		}
	}
	if value := request.GetMessageDetailMaxItemBytes(); value > 0 {
		detail.MaxItemBytes = minPositiveInt64(detail.MaxItemBytes, value)
	}
	if value := request.GetMessageDetailMaxTotalBytes(); value > 0 {
		detail.MaxTotalBytes = minPositiveInt64(detail.MaxTotalBytes, value)
	}
}

func minPositiveInt64(current, ceiling int64) int64 {
	if current <= 0 || current > ceiling {
		return ceiling
	}
	return current
}

func javaVuserDistributedContainmentError(capabilities protocol.RuntimeCapabilities, supported bool) error {
	if !supported {
		return fmt.Errorf("java_vuser runtime does not advertise governed capabilities")
	}
	if !javaVuserRuntimeContained(capabilities.Features) {
		return fmt.Errorf("java_vuser runtime does not advertise required process containment capabilities")
	}
	if !capabilities.Available {
		return fmt.Errorf("java_vuser runtime is unavailable on this generator")
	}
	return nil
}

func requiredRuntimeFeature(script *dsl.Script) string {
	base := requiredBaseRuntimeFeature(script)
	for _, feature := range append(append(dsl.RequiredMessagingRuntimeFeatures(script), dsl.RequiredCompositionRuntimeFeatures(script)...), dsl.RequiredBrowserRuntimeFeatures(script)...) {
		if base != "" {
			base += "+"
		}
		base += feature
	}
	if script != nil && len(script.Config.Results.PropertyAnalytics) > 0 {
		if base != "" {
			base += "+"
		}
		base += dsl.FeaturePropertyAnalyticsV1
	}
	if dsl.RequiresJMeterMessaging(script) {
		if base != "" {
			base += "+"
		}
		base += dsl.FeatureJMeterMessagingV1
	}
	if dsl.RequiresLRFidelity(script) {
		if base != "" {
			return base + "+" + dsl.FeatureLRFidelityV1
		}
		return dsl.FeatureLRFidelityV1
	}
	return base
}
func requiredBaseRuntimeFeature(script *dsl.Script) string {
	if script == nil {
		return ""
	}
	detailRequested := false
	if detail := script.Config.Results.MessageDetail; detail != nil {
		mode := strings.ToLower(strings.TrimSpace(detail.Mode))
		if mode == "sampled" || mode == "adaptive" {
			detailRequested = true
		}
	}
	parallelRequested := scriptRequestsControlParallel(script)
	if detailRequested && parallelRequested {
		return FeatureMessageDetailControlParallelV1
	}
	if detailRequested {
		return FeatureMessageDetailCaptureV1
	}
	if parallelRequested {
		return FeatureControlParallelV1
	}
	if !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
		return ""
	}
	if javaVuserActionsRequirePhaseLifecycle(script.Init) ||
		javaVuserActionsRequirePhaseLifecycle(script.Actions) ||
		javaVuserActionsRequirePhaseLifecycle(script.Teardown) {
		return protocol.FeatureJavaVuserPersistentLifecycleV1
	}
	return ""
}

func scriptRequestsControlParallel(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	stack := append(append(append([]dsl.Action(nil), script.Init...), script.Actions...), script.Teardown...)
	for len(stack) > 0 {
		action := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) == "control_parallel" {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				stack = append(stack, branch.Actions...)
			}
		}
	}
	return false
}

func javaVuserActionsRequirePhaseLifecycle(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			method := ""
			if value, ok := action.Params["method"]; ok && value != nil {
				method = strings.TrimSpace(fmt.Sprint(value))
			}
			if method != "" && !strings.EqualFold(method, "full_lifecycle") {
				return true
			}
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if javaVuserActionsRequirePhaseLifecycle(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}

func legacyRuntimeAvailable(instance protocol.Protocol) (available bool, supported bool) {
	legacy, supported := instance.(interface{ LegacyRuntimeAvailable() bool })
	if !supported {
		return false, false
	}
	defer func() {
		if recover() != nil {
			available = false
		}
	}()
	return legacy.LegacyRuntimeAvailable(), true
}

func normalizedRuntimeWarningCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "runtime_preflight_warning"
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			continue
		}
		return "runtime_preflight_warning"
	}
	return value
}

func (service *Service) cleanupPreparedAssignmentStage(plan *assignmentStagePlan, prepared *preparedAssignmentStage) {
	if prepared != nil && prepared.execution != nil {
		_ = prepared.execution.engine.ClosePreparedRuntime()
	}
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

// Engine capabilities are advertised by this generator binary, not by each
// protocol plugin. Only external runtime capabilities need plugin validation.
func requiredProtocolRuntimeFeature(script *dsl.Script) string {
	required := requiredRuntimeFeature(script)
	external := []string{}
	for _, feature := range strings.Split(required, "+") {
		switch feature {
		case "", dsl.FeatureParallelLoopControlV1, dsl.FeaturePropertyAnalyticsV1, dsl.FeatureLRFidelityV1, FeatureControlParallelV1, FeatureMessageDetailCaptureV1, FeatureMessageDetailControlParallelV1:
		default:
			external = append(external, feature)
		}
	}
	return strings.Join(external, "+")
}

// validateAdoptionGrace rejects adoption grace the generator could not honour
// safely: adoption proves lineage with the run-supervision token, so a grace
// without one is refused rather than silently ignored.
func validateAdoptionGrace(assignment *pb.WorkAssignment) error {
	if assignment.GetAdoptionGrace() == nil {
		return nil
	}
	grace := assignment.GetAdoptionGrace().AsDuration()
	switch {
	case grace < 0:
		return status.Error(codes.InvalidArgument, "adoption_grace must not be negative")
	case grace == 0:
		return nil
	case !validRunSupervisionToken(assignment.GetRunSupervisionToken()):
		return status.Error(codes.InvalidArgument, "adoption_grace requires a run_supervision_token")
	}
	return nil
}

// effectiveAdoptionGrace caps the requested grace at this generator's limit.
func (service *Service) effectiveAdoptionGrace(assignment *pb.WorkAssignment) time.Duration {
	if assignment.GetAdoptionGrace() == nil {
		return 0
	}
	return min(max(assignment.GetAdoptionGrace().AsDuration(), 0), service.maxAdoptionGrace)
}
