package generatorrpc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	apptelemetry "github.com/jasonczech/apexload/pkg/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type assignmentControlKind uint8

const (
	assignmentControlPause assignmentControlKind = iota + 1
	assignmentControlScale
	assignmentControlDuration
)

// pendingAssignmentControl is the coordinator-owned barrier for a control
// transition whose concrete engine operation must run outside Service.mu.
// AssignWork and other controls wait for done before taking a new snapshot.
type pendingAssignmentControl struct {
	token      uint64
	kind       assignmentControlKind
	testID     string
	generation uint64
	done       chan struct{}
}

func (coordinator *assignmentLifecycleCoordinator) reserveControlLocked(testID string, generation uint64, kind assignmentControlKind) (*pendingAssignmentControl, <-chan struct{}) {
	if stage := coordinator.pendingStages[testID]; stage != nil {
		return nil, stage.done
	}
	if control := coordinator.pendingControls[testID]; control != nil {
		return nil, control.done
	}
	coordinator.nextControlToken++
	if coordinator.nextControlToken == 0 {
		coordinator.nextControlToken++
	}
	control := &pendingAssignmentControl{
		token:      coordinator.nextControlToken,
		kind:       kind,
		testID:     testID,
		generation: generation,
		done:       make(chan struct{}),
	}
	coordinator.pendingControls[testID] = control
	return control, nil
}

func (coordinator *assignmentLifecycleCoordinator) controlCurrentLocked(control *pendingAssignmentControl) bool {
	if control == nil {
		return false
	}
	current := coordinator.pendingControls[control.testID]
	return current != nil && current.token == control.token && current.kind == control.kind && current.generation == control.generation
}

func (coordinator *assignmentLifecycleCoordinator) completeControlLocked(control *pendingAssignmentControl) bool {
	if !coordinator.controlCurrentLocked(control) {
		return false
	}
	delete(coordinator.pendingControls, control.testID)
	close(control.done)
	return true
}

func (service *Service) waitForAllPendingControls(ctx context.Context) error {
	for {
		service.mu.RLock()
		waiters := make([]<-chan struct{}, 0, len(service.lifecycleCoordinator.pendingControls))
		for _, control := range service.lifecycleCoordinator.pendingControls {
			if control != nil {
				waiters = append(waiters, control.done)
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

type commitTransitionKind uint8

const (
	commitTransitionApply commitTransitionKind = iota + 1
	commitTransitionAccepted
	commitTransitionRejected
	commitTransitionWait
)

type executionStartEvent struct {
	testID    string
	groupName string
	execution *groupExecution
	ctx       context.Context
}

// commitTransition is an immutable result of the coordinator's locked state
// decision. Engine goroutines consume its copied event list only after the
// compatibility lock and lifecycle-publication lock are released.
type commitTransition struct {
	kind         commitTransitionKind
	wait         <-chan struct{}
	errorMessage string
	err          error
	starts       []executionStartEvent
	lifecycle    *pb.MetricsBatch
}

type commitRuntimeValidation struct {
	generation        uint64
	executions        map[string]*groupExecution
	javaCapacity      javaVuserCapacityObservation
	javaCapacityValid bool
}

func (service *Service) revalidateCommitRuntime(ctx context.Context, request *pb.WorkCommitRequest) (*commitRuntimeValidation, error) {
	service.mu.RLock()
	assignment, exists := service.assignments[request.GetTestId()]
	if !exists || assignment.executionGeneration != request.GetExecutionGeneration() {
		service.mu.RUnlock()
		return nil, nil
	}
	executions := service.executions[request.GetTestId()]
	type runtimeCandidate struct {
		groupName string
		execution *groupExecution
	}
	candidates := make([]runtimeCandidate, 0, len(executions))
	for groupName, execution := range executions {
		if execution == nil || execution.engine == nil || execution.started.Load() {
			continue
		}
		scriptID := assignment.groupScripts[groupName]
		cached, ok := service.scriptCache.get(scriptID)
		if !ok || !cached.requiresPhaseLifecycle {
			continue
		}
		candidates = append(candidates, runtimeCandidate{groupName: groupName, execution: execution})
	}
	generation := assignment.executionGeneration
	service.mu.RUnlock()

	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].groupName < candidates[right].groupName
	})
	validation := &commitRuntimeValidation{
		generation: generation,
		executions: make(map[string]*groupExecution, len(candidates)),
	}
	for _, candidate := range candidates {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, status.FromContextError(err).Err()
			}
		}
		if err := candidate.execution.engine.RevalidateRuntime(ctx); err != nil {
			if ctx != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, status.FromContextError(ctxErr).Err()
				}
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, status.FromContextError(err).Err()
			}
			message := err.Error()
			if candidate.execution.redactionConfig != nil {
				message = candidate.execution.redactionConfig.ScrubText(message)
			}
			const maxCommitRuntimePreflightMessageBytes = 16 << 10
			if len(message) > maxCommitRuntimePreflightMessageBytes {
				message = boundedtext.TruncateUTF8(message, maxCommitRuntimePreflightMessageBytes-len("...[truncated]")) + "...[truncated]"
			}
			return nil, status.Errorf(codes.FailedPrecondition, "group %q runtime revalidation failed: %s", candidate.groupName, message)
		}
		validation.executions[candidate.groupName] = candidate.execution
	}
	if len(candidates) > 0 {
		// Probe capacity after target validation so the locked commit decision
		// uses the freshest available aggregate limit. Per-group validation alone
		// cannot detect a sidecar restart that lowers MaxVUsers below the sum of
		// independently valid groups.
		validation.javaCapacity = observeJavaVuserCapacity()
		if runtimeErr := validation.javaCapacity.persistentRuntimeError(); runtimeErr != nil {
			return nil, status.Error(codes.FailedPrecondition, runtimeErr.Error())
		}
		validation.javaCapacityValid = true
	}
	return validation, nil
}

// revalidateScaleRuntime refreshes a persistent Java group's runner target and
// capability observation before a scale-up reservation is decided. The Java
// sidecar publishes this fresh preflight into its short-lived capability cache,
// so the immediately following aggregate capacity check cannot use a previous
// sidecar process's larger MaxVUsers value.
func (service *Service) revalidateScaleRuntime(ctx context.Context, request *pb.VUserAdjustment) error {
	if request == nil {
		return nil
	}
	service.mu.RLock()
	assignment, exists := service.assignments[request.GetTestId()]
	if !exists || assignment.executionGeneration != request.GetExecutionGeneration() {
		service.mu.RUnlock()
		return nil
	}
	currentVUsers, exists := assignment.groups[request.GetGroupName()]
	if !exists || request.GetTargetVusers() <= currentVUsers {
		service.mu.RUnlock()
		return nil
	}
	scriptID := assignment.groupScripts[request.GetGroupName()]
	cached, cachedOK := service.scriptCache.get(scriptID)
	var execution *groupExecution
	if executions := service.executions[request.GetTestId()]; executions != nil {
		execution = executions[request.GetGroupName()]
	}
	service.mu.RUnlock()
	if !cachedOK || !cached.requiresPhaseLifecycle || execution == nil || execution.engine == nil {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return status.FromContextError(err).Err()
		}
	}
	if err := execution.engine.RevalidateRuntime(ctx); err != nil {
		if ctx != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return status.FromContextError(ctxErr).Err()
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return status.FromContextError(err).Err()
		}
		message := err.Error()
		if execution.redactionConfig != nil {
			message = execution.redactionConfig.ScrubText(message)
		}
		const maxScaleRuntimePreflightMessageBytes = 16 << 10
		if len(message) > maxScaleRuntimePreflightMessageBytes {
			message = boundedtext.TruncateUTF8(message, maxScaleRuntimePreflightMessageBytes-len("...[truncated]")) + "...[truncated]"
		}
		return status.Errorf(codes.FailedPrecondition, "group %q runtime revalidation failed before scale-up: %s", request.GetGroupName(), message)
	}
	return nil
}

func (coordinator *assignmentLifecycleCoordinator) commitWorkLocked(service *Service, ctx context.Context, request *pb.WorkCommitRequest, now time.Time) commitTransition {
	return coordinator.commitWorkValidatedLocked(service, ctx, request, now, nil)
}

func (coordinator *assignmentLifecycleCoordinator) commitWorkValidatedLocked(service *Service, ctx context.Context, request *pb.WorkCommitRequest, now time.Time, runtimeValidation *commitRuntimeValidation) commitTransition {
	testID := request.GetTestId()
	if stage := coordinator.pendingStages[testID]; stage != nil {
		return commitTransition{kind: commitTransitionWait, wait: stage.done}
	}
	if control := coordinator.pendingControls[testID]; control != nil {
		return commitTransition{kind: commitTransitionWait, wait: control.done}
	}
	if err := service.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		return commitTransition{err: err}
	}
	if service.draining {
		return commitTransition{kind: commitTransitionRejected, errorMessage: "generator is draining"}
	}
	if _, stopping := coordinator.stoppingTests[testID]; stopping {
		return commitTransition{err: status.Error(codes.FailedPrecondition, "test assignment is stopping")}
	}
	assignment, ok := service.assignments[testID]
	if !ok {
		return commitTransition{err: status.Error(codes.NotFound, "test assignment not found")}
	}
	if assignment.executionGeneration != request.GetExecutionGeneration() {
		return commitTransition{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)}
	}
	if assignment.requireTestExecutionLease && !service.testExecutionLeaseCurrentLocked(testID, assignment.executionGeneration, now) {
		return commitTransition{err: status.Error(codes.FailedPrecondition, "test assignment execution lease is missing or expired")}
	}
	committed, lifecycleErr := coordinator.commit(assignment)
	if lifecycleErr != nil {
		return commitTransition{err: status.Error(codes.FailedPrecondition, lifecycleErr.Error())}
	}

	starts := make([]executionStartEvent, 0)
	if service.executionEnabled {
		executions := service.executions[testID]
		if len(executions) != len(assignment.groups) || len(executions) == 0 {
			return commitTransition{err: status.Error(codes.FailedPrecondition, "not all assigned groups are staged")}
		}
		groupNames := make([]string, 0, len(executions))
		for groupName := range executions {
			groupNames = append(groupNames, groupName)
		}
		sort.Strings(groupNames)
		persistentJavaStarting := false
		for _, groupName := range groupNames {
			execution := executions[groupName]
			if execution == nil || execution.engine == nil {
				return commitTransition{err: status.Errorf(codes.FailedPrecondition, "group %q is not in a committable staged state", groupName)}
			}
			if execution.started.Load() {
				continue
			}
			scriptID := assignment.groupScripts[groupName]
			cached, cachedOK := service.scriptCache.get(scriptID)
			if cachedOK && cached.requiresPhaseLifecycle &&
				(runtimeValidation == nil ||
					runtimeValidation.generation != assignment.executionGeneration ||
					runtimeValidation.executions[groupName] != execution) {
				return commitTransition{err: status.Errorf(codes.Aborted, "group %q Java runtime validation is stale; retry commit", groupName)}
			}
			persistentJavaStarting = persistentJavaStarting || (cachedOK && cached.requiresPhaseLifecycle)
		}
		if persistentJavaStarting {
			if runtimeValidation == nil || !runtimeValidation.javaCapacityValid {
				return commitTransition{err: status.Error(codes.Aborted, "Java runtime capacity validation is stale; retry commit")}
			}
			maxVUsers, available := runtimeValidation.javaCapacity.persistentMaxVUsers()
			if !available {
				return commitTransition{err: status.Error(codes.FailedPrecondition, runtimeValidation.javaCapacity.persistentRuntimeError().Error())}
			}
			projected := service.activePersistentJavaVusersLocked() + coordinator.pendingPersistentJavaVusersLocked()
			if projected > maxVUsers {
				return commitTransition{err: status.Errorf(
					codes.ResourceExhausted,
					"insufficient java_vuser runtime capacity at commit: requires %d VUsers, maximum %d",
					projected,
					maxVUsers,
				)}
			}
		}
		if assignment.committed {
			allStarted := true
			for _, groupName := range groupNames {
				if !executions[groupName].started.Load() {
					allStarted = false
					break
				}
			}
			if allStarted {
				return commitTransition{kind: commitTransitionAccepted}
			}
		}

		startAt := now
		if request.GetStartAtUnixNano() != 0 {
			startAt = time.Unix(0, request.GetStartAtUnixNano())
			if !startAt.After(now) {
				return commitTransition{
					kind: commitTransitionRejected,
					errorMessage: fmt.Sprintf(
						"coordinated start barrier elapsed by %s (local now %s, local start deadline %s)",
						now.Sub(startAt),
						now.UTC().Format(time.RFC3339Nano),
						startAt.UTC().Format(time.RFC3339Nano),
					),
				}
			}
			if startAt.Sub(now) > coordinator.stageTTLValue() {
				return commitTransition{
					kind: commitTransitionRejected,
					errorMessage: fmt.Sprintf(
						"coordinated start barrier is %s ahead of local now, exceeding staging TTL %s",
						startAt.Sub(now),
						coordinator.stageTTLValue(),
					),
				}
			}
		}
		for _, groupName := range groupNames {
			execution := executions[groupName]
			if execution.started.Load() {
				continue
			}
			executionContext, cancel := context.WithCancel(apptelemetry.Detach(ctx))
			execution.cancel = cancel
			execution.startAt = startAt
			execution.started.Store(true)
			starts = append(starts, executionStartEvent{testID: testID, groupName: groupName, execution: execution, ctx: executionContext})
		}
		if assignment.committed && len(starts) == 0 {
			return commitTransition{kind: commitTransitionAccepted}
		}
	} else if assignment.committed {
		return commitTransition{kind: commitTransitionAccepted}
	}

	service.assignments[testID] = committed
	if !service.hasStagedExecutionLocked(testID) {
		coordinator.stopStageTimerLocked(testID)
	}
	if !service.executionEnabled && assignedTestDuration(committed) > 0 {
		service.scheduleCompletionLocked(committed.testID, assignedTestDuration(committed))
	}
	return commitTransition{kind: commitTransitionApply, starts: starts, lifecycle: service.metricsBatchLocked(testID)}
}

// CommitWork delegates its complete state decision to the lifecycle
// coordinator, publishes the resulting event, and only then starts engines.
func (service *Service) CommitWork(ctx context.Context, request *pb.WorkCommitRequest) (*pb.WorkCommitAck, error) {
	if request == nil || strings.TrimSpace(request.GetTestId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	return service.commitValidatedWork(ctx, request)
}

func (service *Service) commitValidatedWork(ctx context.Context, request *pb.WorkCommitRequest) (*pb.WorkCommitAck, error) {
	translatedRequest, err := translateCoordinatedStartRequest(ctx, request, time.Now())
	if err != nil {
		return nil, err
	}
	request = translatedRequest
	for {
		runtimeValidation, validationErr := service.revalidateCommitRuntime(ctx, request)
		if validationErr != nil {
			return nil, validationErr
		}
		service.metricsBroker.lockPublication()
		service.mu.Lock()
		if ctx != nil && ctx.Err() != nil {
			service.mu.Unlock()
			service.metricsBroker.unlockPublication()
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		transition := service.lifecycleCoordinator.commitWorkValidatedLocked(service, ctx, request, time.Now(), runtimeValidation)
		service.mu.Unlock()
		if transition.lifecycle != nil {
			service.publishLifecycleTransitionLocked(transition.lifecycle)
		}
		service.metricsBroker.unlockPublication()

		if transition.err != nil {
			return nil, transition.err
		}
		switch transition.kind {
		case commitTransitionAccepted:
			return &pb.WorkCommitAck{Accepted: true}, nil
		case commitTransitionRejected:
			return &pb.WorkCommitAck{Accepted: false, ErrorMessage: transition.errorMessage}, nil
		case commitTransitionWait:
			if ctx == nil {
				<-transition.wait
				continue
			}
			select {
			case <-transition.wait:
				continue
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			}
		case commitTransitionApply:
			for _, event := range transition.starts {
				service.startExecutionEvent(event)
			}
			return &pb.WorkCommitAck{Accepted: true}, nil
		default:
			return nil, status.Error(codes.Internal, "assignment coordinator returned an invalid commit decision")
		}
	}
}

// translateCoordinatedStartRequest converts a controller-clock barrier into a
// generator-clock deadline. Current clients provide a heartbeat RTT-midpoint
// clock estimate, so one-way CommitWork transit cannot move the common barrier
// later on a slower target. The sent-at relative translation remains only for
// rolling compatibility with clients that do not provide an estimate. This
// intentionally runs once on receipt: retries behind a pending lifecycle
// transition must keep the original deadline rather than extending it.
func translateCoordinatedStartRequest(ctx context.Context, request *pb.WorkCommitRequest, receivedAt time.Time) (*pb.WorkCommitRequest, error) {
	if request == nil || request.GetStartAtUnixNano() == 0 {
		return request, nil
	}
	controllerSentAt, present, err := incomingCoordinatedStartControllerTime(ctx)
	if err != nil {
		return nil, err
	}
	clockOffset, _, clockEstimatePresent, err := incomingCoordinatedStartClockEstimate(ctx)
	if err != nil {
		return nil, err
	}
	if clockEstimatePresent && !present {
		return nil, status.Error(codes.InvalidArgument, "coordinated start clock estimate requires controller sent-time metadata")
	}
	if !present {
		// Preserve compatibility with older clients and direct in-process calls.
		return request, nil
	}
	controllerBarrier := time.Unix(0, request.GetStartAtUnixNano())
	localBarrier := receivedAt.Add(controllerBarrier.Sub(controllerSentAt))
	if clockEstimatePresent {
		localBarrier = controllerBarrier.Add(clockOffset)
	}
	translated := proto.Clone(request).(*pb.WorkCommitRequest)
	translated.StartAtUnixNano = localBarrier.UnixNano()
	return translated, nil
}

func (service *Service) startExecutionEvent(event executionStartEvent) {
	go func() {
		if !waitForCoordinatedExecutionStart(event.ctx, event.execution.startAt) {
			close(event.execution.publisherDone)
			return
		}
		service.runExecutionPublisher(event.ctx, event.testID, event.groupName, event.execution)
	}()
	go func() {
		if !waitForCoordinatedExecutionStart(event.ctx, event.execution.startAt) {
			service.completeExecutionCallbacks(event.testID, event.groupName, event.execution, nil)
			return
		}
		_, runErr := event.execution.engine.Run(event.ctx)
		service.completeExecutionCallbacks(event.testID, event.groupName, event.execution, runErr)
	}()
}

func waitForCoordinatedExecutionStart(ctx context.Context, startAt time.Time) bool {
	delay := time.Until(startAt)
	if delay <= 0 {
		return ctx == nil || ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type stopTransitionKind uint8

const (
	stopTransitionAck stopTransitionKind = iota + 1
	stopTransitionWait
	stopTransitionGraceful
	stopTransitionDetached
)

type stopTransitionPlan struct {
	kind                     stopTransitionKind
	testID                   string
	assignment               assignedTest
	executions               []*groupExecution
	staged                   []*groupExecution
	running                  []*groupExecution
	wait                     <-chan struct{}
	retryAfterWait           bool
	acknowledged             bool
	pendingVUsers            int32
	persistentJavaDrainToken uint64
	err                      error
}

func (coordinator *assignmentLifecycleCoordinator) planStopLocked(service *Service, request *pb.StopRequest, bypassExecutionFence bool, enforceMaximumGeneration bool, maximumGeneration uint64) stopTransitionPlan {
	testID := request.GetTestId()
	if stage := coordinator.pendingStages[testID]; stage != nil {
		return stopTransitionPlan{kind: stopTransitionWait, wait: stage.done, retryAfterWait: true}
	}
	if control := coordinator.pendingControls[testID]; control != nil {
		return stopTransitionPlan{kind: stopTransitionWait, wait: control.done, retryAfterWait: true}
	}
	if !bypassExecutionFence {
		if err := service.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
			return stopTransitionPlan{err: err}
		}
	}

	assignment, ok := service.assignments[testID]
	if ok && bypassExecutionFence && enforceMaximumGeneration && assignment.executionGeneration > maximumGeneration {
		return stopTransitionPlan{kind: stopTransitionAck, acknowledged: true}
	}
	if ok && !bypassExecutionFence && assignment.executionGeneration != request.GetExecutionGeneration() {
		return stopTransitionPlan{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)}
	}
	if !ok {
		_, stopping := coordinator.stoppingTests[testID]
		stoppingGeneration, trackedGeneration := coordinator.stoppingGenerations[testID]
		completion := coordinator.stopCompletions[testID]
		if trackedGeneration && !bypassExecutionFence && stoppingGeneration != request.GetExecutionGeneration() {
			return stopTransitionPlan{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", stoppingGeneration)}
		}
		if trackedGeneration && bypassExecutionFence && enforceMaximumGeneration && stoppingGeneration > maximumGeneration {
			return stopTransitionPlan{kind: stopTransitionAck, acknowledged: true}
		}
		if terminalGeneration, terminal := service.terminalGenerationLocked(testID); terminal {
			if !bypassExecutionFence && terminalGeneration != request.GetExecutionGeneration() {
				return stopTransitionPlan{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", terminalGeneration)}
			}
			return stopTransitionPlan{kind: stopTransitionAck, acknowledged: true}
		}
		if !stopping || completion == nil {
			return stopTransitionPlan{kind: stopTransitionAck, acknowledged: false}
		}
		return stopTransitionPlan{kind: stopTransitionWait, wait: completion.done}
	}

	assignment, _ = coordinator.beginStopping(testID, assignment.executionGeneration, assignment)
	service.assignments[testID] = assignment
	// A positively acknowledged StopTest is itself the assignment fence. Stop
	// renewing/expiring its loss-tolerance lease while graceful shutdown drains.
	service.clearTestExecutionLeaseLocked(testID)
	if request.GetGraceful() && assignment.committed {
		executionMap := service.executions[testID]
		if len(executionMap) > 0 {
			running := make([]*groupExecution, 0, len(executionMap))
			staged := make([]*groupExecution, 0)
			stagedNames := make([]string, 0)
			for groupName, execution := range executionMap {
				if execution != nil && execution.started.Load() {
					running = append(running, execution)
					continue
				}
				staged = append(staged, execution)
				stagedNames = append(stagedNames, groupName)
			}
			if len(running) > 0 {
				for _, groupName := range stagedNames {
					delete(executionMap, groupName)
					if assignment.activeGroups != nil {
						assignment.activeGroups[groupName] = 0
					}
				}
				service.assignments[testID] = assignment
				coordinator.stopStageTimerLocked(testID)
				return stopTransitionPlan{
					kind:          stopTransitionGraceful,
					testID:        testID,
					assignment:    assignment,
					staged:        append([]*groupExecution(nil), staged...),
					running:       append([]*groupExecution(nil), running...),
					acknowledged:  true,
					pendingVUsers: assignment.vuserCount,
				}
			}
		}
	}

	persistentJavaDrainToken := service.reserveAssignmentJavaDrainLocked(assignment)
	service.clearTestExecutionLeaseLocked(testID)
	delete(service.assignments, testID)
	service.stopCompletionTimerLocked(testID)
	coordinator.stopStageTimerLocked(testID)
	executionMap := service.executions[testID]
	delete(service.executions, testID)
	executions := make([]*groupExecution, 0, len(executionMap))
	for _, execution := range executionMap {
		executions = append(executions, execution)
	}
	return stopTransitionPlan{
		kind:                     stopTransitionDetached,
		testID:                   testID,
		assignment:               assignment,
		executions:               executions,
		acknowledged:             true,
		pendingVUsers:            assignment.vuserCount,
		persistentJavaDrainToken: persistentJavaDrainToken,
	}
}

func (service *Service) StopTest(ctx context.Context, request *pb.StopRequest) (*pb.StopAck, error) {
	return service.stopTest(ctx, request, false, false, 0)
}

func (service *Service) stopTest(ctx context.Context, request *pb.StopRequest, bypassExecutionFence bool, enforceMaximumGeneration bool, maximumGeneration uint64) (*pb.StopAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	for {
		service.mu.Lock()
		if ctx != nil && ctx.Err() != nil {
			service.mu.Unlock()
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		plan := service.lifecycleCoordinator.planStopLocked(service, request, bypassExecutionFence, enforceMaximumGeneration, maximumGeneration)
		service.mu.Unlock()
		if plan.err != nil {
			return nil, plan.err
		}
		switch plan.kind {
		case stopTransitionAck:
			return &pb.StopAck{Acknowledged: plan.acknowledged, PendingVusers: plan.pendingVUsers}, nil
		case stopTransitionWait:
			if err := service.waitForControlDecision(ctx, plan.wait); err != nil {
				return nil, err
			}
			if plan.retryAfterWait {
				continue
			}
			return &pb.StopAck{Acknowledged: true}, nil
		case stopTransitionGraceful:
			for _, execution := range plan.staged {
				stopGroupExecution(execution)
			}
			for _, execution := range plan.running {
				if !execution.startAt.IsZero() && time.Now().Before(execution.startAt) && execution.cancel != nil {
					execution.cancel()
				} else {
					execution.engine.GracefulStop()
				}
			}
			return &pb.StopAck{Acknowledged: true, PendingVusers: plan.pendingVUsers}, nil
		case stopTransitionDetached:
			return service.finishDetachedStop(ctx, plan)
		default:
			return nil, status.Error(codes.Internal, "assignment coordinator returned an invalid stop decision")
		}
	}
}

func (service *Service) finishDetachedStop(ctx context.Context, plan stopTransitionPlan) (*pb.StopAck, error) {
	writer := service.archiveManager.takeWriter(plan.testID)
	trackedFinalizer := writer != nil && service.archiveManager.addFinalizer()
	for _, execution := range plan.executions {
		stopGroupExecution(execution)
	}
	if err := waitForGeneratorExecutions(ctx, plan.executions); err != nil {
		if !trackedFinalizer {
			go func() {
				_ = waitForGeneratorExecutions(context.Background(), plan.executions)
				service.releaseStopDrain(plan.persistentJavaDrainToken)
				service.captureStoppedExecutionMetrics(&plan.assignment, plan.executions)
				service.finishStoppedTest(plan.testID, plan.assignment, writer)
			}()
			return nil, status.Errorf(codes.DeadlineExceeded, "wait for stopped execution: %v", err)
		}
		go func() {
			defer service.archiveManager.finalizerDone()
			_ = waitForGeneratorExecutions(context.Background(), plan.executions)
			service.releaseStopDrain(plan.persistentJavaDrainToken)
			service.captureStoppedExecutionMetrics(&plan.assignment, plan.executions)
			service.finishStoppedTest(plan.testID, plan.assignment, writer)
		}()
		return nil, status.Errorf(codes.DeadlineExceeded, "wait for stopped execution: %v", err)
	}
	if trackedFinalizer {
		defer service.archiveManager.finalizerDone()
	}
	service.releaseStopDrain(plan.persistentJavaDrainToken)
	service.captureStoppedExecutionMetrics(&plan.assignment, plan.executions)
	_, finalError := service.finishStoppedTest(plan.testID, plan.assignment, writer)
	if finalError != "" {
		return nil, status.Error(codes.Internal, finalError)
	}
	return &pb.StopAck{Acknowledged: true, PendingVusers: plan.pendingVUsers}, nil
}

func (service *Service) releaseStopDrain(token uint64) {
	if token == 0 {
		return
	}
	service.mu.Lock()
	service.releasePersistentJavaDrainLocked(token)
	service.mu.Unlock()
}

type enginePauseParticipant struct {
	engine *engine.MultiGroupEngine
}

type pauseTransitionPlan struct {
	control           *pendingAssignmentControl
	testID            string
	generation        uint64
	targetPaused      bool
	previousPaused    bool
	operationID       string
	operationSequence uint64
	participants      []enginePauseParticipant
}

type pauseTransitionDecision struct {
	plan               *pauseTransitionPlan
	wait               <-chan struct{}
	appliedOperationID string
	appliedSequence    uint64
	idempotent         bool
	err                error
}

func (coordinator *assignmentLifecycleCoordinator) planPauseLocked(service *Service, testID string, paused bool, generation uint64, operationID string, operationSequence uint64) pauseTransitionDecision {
	if stage := coordinator.pendingStages[testID]; stage != nil {
		return pauseTransitionDecision{wait: stage.done}
	}
	if control := coordinator.pendingControls[testID]; control != nil {
		return pauseTransitionDecision{wait: control.done}
	}
	if err := service.validateMutationGenerationLocked(generation); err != nil {
		return pauseTransitionDecision{err: err}
	}
	assignment, ok := service.assignments[testID]
	if !ok {
		return pauseTransitionDecision{err: status.Error(codes.NotFound, "test assignment not found")}
	}
	if _, stopping := coordinator.stoppingTests[testID]; stopping {
		return pauseTransitionDecision{err: status.Error(codes.FailedPrecondition, "test assignment is stopping")}
	}
	if assignment.executionGeneration != generation {
		return pauseTransitionDecision{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)}
	}
	if !assignment.committed {
		return pauseTransitionDecision{err: status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")}
	}
	if operationID != "" {
		if operationSequence < assignment.pauseOperationSequence {
			return pauseTransitionDecision{err: status.Errorf(codes.FailedPrecondition, "pause operation sequence %d is stale; current sequence is %d", operationSequence, assignment.pauseOperationSequence)}
		}
		if operationSequence == assignment.pauseOperationSequence {
			if assignment.pauseOperationID != operationID || assignment.paused != paused {
				return pauseTransitionDecision{err: status.Error(codes.FailedPrecondition, "pause operation sequence was already used for a different operation")}
			}
			return pauseTransitionDecision{appliedOperationID: operationID, appliedSequence: operationSequence, idempotent: true}
		}
	}

	participants := make([]enginePauseParticipant, 0)
	if service.executionEnabled {
		executions := service.executions[testID]
		groupNames := make([]string, 0, len(executions))
		for groupName := range executions {
			groupNames = append(groupNames, groupName)
		}
		sort.Strings(groupNames)
		for _, groupName := range groupNames {
			execution := executions[groupName]
			if execution != nil && !execution.started.Load() {
				return pauseTransitionDecision{err: status.Error(codes.FailedPrecondition, "test has staged groups that are not committed")}
			}
			if execution != nil && execution.engine != nil {
				participants = append(participants, enginePauseParticipant{engine: execution.engine})
			}
		}
	}
	control, wait := coordinator.reserveControlLocked(testID, generation, assignmentControlPause)
	if wait != nil {
		return pauseTransitionDecision{wait: wait}
	}
	return pauseTransitionDecision{plan: &pauseTransitionPlan{
		control:           control,
		testID:            testID,
		generation:        generation,
		targetPaused:      paused,
		previousPaused:    assignment.paused,
		operationID:       operationID,
		operationSequence: operationSequence,
		participants:      participants,
	}}
}

func applyPauseParticipants(participants []enginePauseParticipant, paused bool) (int, error) {
	for index, participant := range participants {
		var err error
		if paused {
			err = participant.engine.Pause()
		} else {
			err = participant.engine.Resume()
		}
		if err != nil {
			return index, err
		}
	}
	return len(participants), nil
}

func rollbackPauseParticipants(participants []enginePauseParticipant, applied int, paused bool) error {
	rollbackErrors := make([]error, 0)
	for index := applied - 1; index >= 0; index-- {
		var err error
		if paused {
			err = participants[index].engine.Pause()
		} else {
			err = participants[index].engine.Resume()
		}
		if err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

func (coordinator *assignmentLifecycleCoordinator) commitPauseLocked(service *Service, plan *pauseTransitionPlan) (*pb.MetricsBatch, error) {
	if !coordinator.controlCurrentLocked(plan.control) {
		return nil, status.Error(codes.Aborted, "pause control changed while applying")
	}
	if err := service.validateMutationGenerationLocked(plan.generation); err != nil {
		return nil, err
	}
	assignment, ok := service.assignments[plan.testID]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	if _, stopping := coordinator.stoppingTests[plan.testID]; stopping {
		return nil, status.Error(codes.FailedPrecondition, "test assignment is stopping")
	}
	if assignment.executionGeneration != plan.generation {
		return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if !assignment.committed {
		return nil, status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")
	}
	assignment.paused = plan.targetPaused
	if plan.operationID != "" {
		assignment.pauseOperationID = plan.operationID
		assignment.pauseOperationSequence = plan.operationSequence
	}
	service.assignments[plan.testID] = assignment
	batch := service.metricsBatchLocked(plan.testID)
	coordinator.completeControlLocked(plan.control)
	return batch, nil
}

func (service *Service) abortControl(plan *pendingAssignmentControl) {
	service.mu.Lock()
	service.lifecycleCoordinator.completeControlLocked(plan)
	service.mu.Unlock()
}

func (service *Service) SetPaused(ctx context.Context, request *pb.PauseRequest) (*pb.PauseAck, error) {
	operationID := strings.TrimSpace(request.GetOperationId())
	if operationID == "" {
		return nil, status.Error(codes.InvalidArgument, "operation_id is required")
	}
	if err := validateGeneratorFieldBytes(operationID, "operation_id", maxGeneratorOperationIDBytes); err != nil {
		return nil, err
	}
	if request.GetOperationSequence() == 0 {
		return nil, status.Error(codes.InvalidArgument, "operation_sequence is required")
	}
	appliedOperationID, appliedSequence, err := service.setPausedWithOperation(ctx, request.GetTestId(), request.GetPause(), request.GetExecutionGeneration(), operationID, request.GetOperationSequence())
	if err != nil {
		return nil, err
	}
	return &pb.PauseAck{
		Accepted:                 true,
		CurrentPaused:            request.GetPause(),
		AppliedOperationId:       appliedOperationID,
		AppliedOperationSequence: appliedSequence,
	}, nil
}

func (service *Service) setPausedWithOperation(ctx context.Context, testID string, paused bool, executionGeneration uint64, operationID string, operationSequence uint64) (string, uint64, error) {
	if testID == "" {
		return "", 0, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", 0, status.FromContextError(err).Err()
		}
	}
	for {
		service.mu.Lock()
		if ctx != nil && ctx.Err() != nil {
			service.mu.Unlock()
			return "", 0, status.FromContextError(ctx.Err()).Err()
		}
		decision := service.lifecycleCoordinator.planPauseLocked(service, testID, paused, executionGeneration, operationID, operationSequence)
		service.mu.Unlock()
		if decision.err != nil {
			return "", 0, decision.err
		}
		if decision.wait != nil {
			if ctx == nil {
				<-decision.wait
				continue
			}
			select {
			case <-decision.wait:
				continue
			case <-ctx.Done():
				return "", 0, status.FromContextError(ctx.Err()).Err()
			}
		}
		if decision.idempotent {
			return decision.appliedOperationID, decision.appliedSequence, nil
		}

		plan := decision.plan
		applied, applyErr := applyPauseParticipants(plan.participants, plan.targetPaused)
		if applyErr != nil {
			rollbackErr := rollbackPauseParticipants(plan.participants, applied, plan.previousPaused)
			service.abortControl(plan.control)
			if rollbackErr != nil {
				return "", 0, status.Errorf(codes.Internal, "update running pause state: %v; rollback failed: %v", applyErr, rollbackErr)
			}
			return "", 0, status.Errorf(codes.FailedPrecondition, "update running pause state: %v", applyErr)
		}

		service.metricsBroker.lockPublication()
		service.mu.Lock()
		var commitErr error
		if ctx != nil && ctx.Err() != nil {
			commitErr = status.FromContextError(ctx.Err()).Err()
		} else {
			var batch *pb.MetricsBatch
			batch, commitErr = service.lifecycleCoordinator.commitPauseLocked(service, plan)
			if commitErr == nil {
				service.mu.Unlock()
				service.publishLifecycleTransitionLocked(batch)
				service.metricsBroker.unlockPublication()
				return plan.operationID, plan.operationSequence, nil
			}
		}
		service.mu.Unlock()
		service.metricsBroker.unlockPublication()

		rollbackErr := rollbackPauseParticipants(plan.participants, len(plan.participants), plan.previousPaused)
		service.abortControl(plan.control)
		if rollbackErr != nil {
			return "", 0, status.Errorf(codes.Internal, "%v; rollback running pause state: %v", commitErr, rollbackErr)
		}
		return "", 0, commitErr
	}
}

func (service *Service) setPaused(ctx context.Context, testID string, paused bool, executionGeneration uint64) error {
	_, _, err := service.setPausedWithOperation(ctx, testID, paused, executionGeneration, "", 0)
	return err
}

type scaleTransitionPlan struct {
	control                      *pendingAssignmentControl
	testID                       string
	groupName                    string
	generation                   uint64
	previousGroupVUsers          int32
	targetGroupVUsers            int32
	reservedTotalVUsers          int32
	persistentJavaPreviousDemand int64
	persistentJavaTargetDemand   int64
	persistentJavaDrainToken     uint64
	legacyJavaLifecycle          bool
	engine                       *engine.MultiGroupEngine
}

type scaleTransitionDecision struct {
	plan *scaleTransitionPlan
	wait <-chan struct{}
	err  error
}

func (coordinator *assignmentLifecycleCoordinator) planScaleLocked(service *Service, request *pb.VUserAdjustment, javaCapacity javaVuserCapacityObservation) scaleTransitionDecision {
	testID := request.GetTestId()
	if stage := coordinator.pendingStages[testID]; stage != nil {
		return scaleTransitionDecision{wait: stage.done}
	}
	if control := coordinator.pendingControls[testID]; control != nil {
		return scaleTransitionDecision{wait: control.done}
	}
	if err := service.validateMutationGenerationLocked(request.GetExecutionGeneration()); err != nil {
		return scaleTransitionDecision{err: err}
	}
	assignment, ok := service.assignments[testID]
	if !ok {
		return scaleTransitionDecision{err: status.Error(codes.NotFound, "test assignment not found")}
	}
	if _, stopping := coordinator.stoppingTests[testID]; stopping {
		return scaleTransitionDecision{err: status.Error(codes.FailedPrecondition, "test assignment is stopping")}
	}
	if assignment.executionGeneration != request.GetExecutionGeneration() {
		return scaleTransitionDecision{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)}
	}
	if !assignment.committed {
		return scaleTransitionDecision{err: status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")}
	}
	currentGroupVUsers, ok := assignment.groups[request.GetGroupName()]
	if !ok {
		return scaleTransitionDecision{err: status.Error(codes.NotFound, "group assignment not found")}
	}
	if service.draining && request.GetTargetVusers() > currentGroupVUsers {
		return scaleTransitionDecision{err: status.Error(codes.Unavailable, "generator is draining and cannot increase vusers")}
	}
	var persistentJavaCurrentDemand int64
	var persistentJavaTargetDemand int64
	legacyJavaLifecycle := false
	if scriptID := assignment.groupScripts[request.GetGroupName()]; scriptID != "" {
		cached, cachedOK := service.scriptCache.get(scriptID)
		if !cachedOK {
			return scaleTransitionDecision{err: status.Error(codes.FailedPrecondition, "group script is no longer available in the generator cache")}
		}
		if cached.requiresPhaseLifecycle {
			persistentJavaCurrentDemand = cached.javaVuserCapacity.Demand(int(currentGroupVUsers))
			persistentJavaTargetDemand = cached.javaVuserCapacity.Demand(int(request.GetTargetVusers()))
			if persistentJavaTargetDemand > persistentJavaCurrentDemand {
				if service.legacyJavaRuntimeOccupiedLocked() {
					return scaleTransitionDecision{err: status.Error(codes.ResourceExhausted, "persistent java_vuser lifecycle cannot share generator process slots with an active legacy full_lifecycle assignment")}
				}
				if runtimeErr := javaCapacity.persistentRuntimeError(); runtimeErr != nil {
					return scaleTransitionDecision{err: status.Error(codes.FailedPrecondition, runtimeErr.Error())}
				}
				maxVUsers, _ := javaCapacity.persistentMaxVUsers()
				projectedPersistent := service.activePersistentJavaVusersLocked() - persistentJavaCurrentDemand + persistentJavaTargetDemand
				if projectedPersistent > maxVUsers {
					return scaleTransitionDecision{err: status.Errorf(codes.ResourceExhausted, "insufficient java_vuser runtime capacity: maximum %d", maxVUsers)}
				}
			}
		} else if strings.EqualFold(cached.protocol, "java_vuser") {
			legacyJavaLifecycle = true
			if request.GetTargetVusers() > currentGroupVUsers &&
				service.persistentJavaRuntimeOccupiedLocked() {
				return scaleTransitionDecision{err: status.Error(codes.ResourceExhausted, "legacy java_vuser full_lifecycle cannot share generator process slots with an active persistent lifecycle assignment")}
			}
		}
	}

	updatedTotal64 := int64(assignment.vuserCount) - int64(currentGroupVUsers) + int64(request.GetTargetVusers())
	projected := service.activeVUsers64Locked() - int64(assignment.vuserCount) + updatedTotal64
	if updatedTotal64 < 0 || updatedTotal64 > maxGeneratorVUsers || projected > int64(service.maxVUsers) {
		return scaleTransitionDecision{err: status.Error(codes.ResourceExhausted, "insufficient generator capacity")}
	}
	var executionEngine *engine.MultiGroupEngine
	if service.executionEnabled {
		if executions := service.executions[testID]; executions != nil {
			execution := executions[request.GetGroupName()]
			if execution != nil {
				if !execution.started.Load() {
					return scaleTransitionDecision{err: status.Error(codes.FailedPrecondition, "group assignment is staged but not committed")}
				}
				executionEngine = execution.engine
			}
		}
	}
	control, wait := coordinator.reserveControlLocked(testID, request.GetExecutionGeneration(), assignmentControlScale)
	if wait != nil {
		return scaleTransitionDecision{wait: wait}
	}
	persistentJavaDrainToken := uint64(0)
	if persistentJavaCurrentDemand > persistentJavaTargetDemand {
		persistentJavaDrainToken = service.reservePersistentJavaDrainLocked(persistentJavaCurrentDemand - persistentJavaTargetDemand)
	} else if legacyJavaLifecycle && currentGroupVUsers > 0 && request.GetTargetVusers() == 0 {
		persistentJavaDrainToken = service.reserveLegacyJavaDrainLocked()
	}
	updatedTotal := int32(updatedTotal64)
	assignment.groups[request.GetGroupName()] = request.GetTargetVusers()
	if assignment.activeGroups == nil {
		assignment.activeGroups = make(map[string]int32)
	}
	assignment.activeGroups[request.GetGroupName()] = request.GetTargetVusers()
	assignment.vuserCount = updatedTotal
	service.assignments[testID] = assignment
	return scaleTransitionDecision{plan: &scaleTransitionPlan{
		control:                      control,
		testID:                       testID,
		groupName:                    request.GetGroupName(),
		generation:                   request.GetExecutionGeneration(),
		previousGroupVUsers:          currentGroupVUsers,
		targetGroupVUsers:            request.GetTargetVusers(),
		reservedTotalVUsers:          updatedTotal,
		persistentJavaPreviousDemand: persistentJavaCurrentDemand,
		persistentJavaTargetDemand:   persistentJavaTargetDemand,
		persistentJavaDrainToken:     persistentJavaDrainToken,
		legacyJavaLifecycle:          legacyJavaLifecycle,
		engine:                       executionEngine,
	}}
}

func (coordinator *assignmentLifecycleCoordinator) commitScaleLocked(service *Service, plan *scaleTransitionPlan) (*pb.MetricsBatch, int32, error) {
	if !coordinator.controlCurrentLocked(plan.control) {
		return nil, 0, status.Error(codes.Aborted, "group assignment changed while scaling")
	}
	if err := service.validateMutationGenerationLocked(plan.generation); err != nil {
		return nil, 0, err
	}
	assignment, ok := service.assignments[plan.testID]
	if !ok {
		return nil, 0, status.Error(codes.NotFound, "test assignment not found")
	}
	if assignment.executionGeneration != plan.generation {
		return nil, 0, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if _, stopping := coordinator.stoppingTests[plan.testID]; stopping {
		return nil, 0, status.Error(codes.FailedPrecondition, "test assignment is stopping")
	}
	reservedVUsers, groupExists := assignment.groups[plan.groupName]
	if !groupExists {
		return nil, 0, status.Error(codes.NotFound, "group assignment not found")
	}
	if reservedVUsers != plan.targetGroupVUsers || assignment.vuserCount != plan.reservedTotalVUsers {
		return nil, 0, status.Error(codes.Aborted, "group assignment changed while scaling")
	}
	batch := service.metricsBatchLocked(plan.testID)
	coordinator.completeControlLocked(plan.control)
	return batch, assignment.vuserCount, nil
}

func (coordinator *assignmentLifecycleCoordinator) rollbackScaleLocked(service *Service, plan *scaleTransitionPlan) bool {
	if !coordinator.controlCurrentLocked(plan.control) {
		return false
	}
	restored := false
	assignment, exists := service.assignments[plan.testID]
	if exists && assignment.executionGeneration == plan.generation {
		if reserved, groupExists := assignment.groups[plan.groupName]; groupExists && reserved == plan.targetGroupVUsers && assignment.vuserCount == plan.reservedTotalVUsers {
			assignment.groups[plan.groupName] = plan.previousGroupVUsers
			if assignment.activeGroups == nil {
				assignment.activeGroups = make(map[string]int32)
			}
			assignment.activeGroups[plan.groupName] = plan.previousGroupVUsers
			assignment.vuserCount = assignment.vuserCount - plan.targetGroupVUsers + plan.previousGroupVUsers
			service.assignments[plan.testID] = assignment
			restored = true
		}
	}
	coordinator.completeControlLocked(plan.control)
	return restored
}

func (coordinator *assignmentLifecycleCoordinator) releaseScaleWithoutRollbackLocked(service *Service, plan *scaleTransitionPlan) bool {
	if !coordinator.controlCurrentLocked(plan.control) {
		return false
	}
	assignment, exists := service.assignments[plan.testID]
	reservationCurrent := exists && assignment.executionGeneration == plan.generation && assignment.vuserCount == plan.reservedTotalVUsers && assignment.groups[plan.groupName] == plan.targetGroupVUsers
	coordinator.completeControlLocked(plan.control)
	return reservationCurrent
}

func (service *Service) ensureScaleRollbackDrainLocked(plan *scaleTransitionPlan) {
	if plan == nil || plan.persistentJavaDrainToken != 0 {
		return
	}
	if plan.legacyJavaLifecycle && plan.targetGroupVUsers > 0 && plan.previousGroupVUsers == 0 {
		plan.persistentJavaDrainToken = service.reserveLegacyJavaDrainLocked()
		return
	}
	if plan.persistentJavaTargetDemand <= plan.persistentJavaPreviousDemand {
		return
	}
	plan.persistentJavaDrainToken = service.reservePersistentJavaDrainLocked(
		plan.persistentJavaTargetDemand - plan.persistentJavaPreviousDemand,
	)
}

func (service *Service) prepareScaleRollbackOverlapLocked(plan *scaleTransitionPlan) {
	if plan == nil || plan.persistentJavaDrainToken == 0 ||
		plan.persistentJavaPreviousDemand <= plan.persistentJavaTargetDemand {
		return
	}
	delta := plan.persistentJavaPreviousDemand - plan.persistentJavaTargetDemand
	drain := service.persistentJavaDrains[plan.persistentJavaDrainToken]
	drain.persistentDemand = 2 * delta
	service.persistentJavaDrains[plan.persistentJavaDrainToken] = drain
}

func (service *Service) normalizeScaleRollbackDrainLocked(plan *scaleTransitionPlan) {
	if plan == nil || plan.persistentJavaDrainToken == 0 ||
		plan.persistentJavaPreviousDemand <= plan.persistentJavaTargetDemand {
		return
	}
	drain := service.persistentJavaDrains[plan.persistentJavaDrainToken]
	drain.persistentDemand = plan.persistentJavaPreviousDemand - plan.persistentJavaTargetDemand
	service.persistentJavaDrains[plan.persistentJavaDrainToken] = drain
}

func (service *Service) releaseScaleDrainLocked(plan *scaleTransitionPlan) {
	if plan == nil || plan.persistentJavaDrainToken == 0 {
		return
	}
	service.releasePersistentJavaDrainLocked(plan.persistentJavaDrainToken)
	plan.persistentJavaDrainToken = 0
}

func (service *Service) releaseScaleDrainAfterReap(plan *scaleTransitionPlan, maxLive int32) {
	if plan == nil || plan.persistentJavaDrainToken == 0 {
		return
	}
	if plan.engine == nil {
		service.mu.Lock()
		service.releaseScaleDrainLocked(plan)
		service.mu.Unlock()
		return
	}
	token := plan.persistentJavaDrainToken
	plan.persistentJavaDrainToken = 0
	go func() {
		_ = plan.engine.WaitForGroupVUsers(context.Background(), plan.groupName, int(maxLive))
		service.mu.Lock()
		service.releasePersistentJavaDrainLocked(token)
		service.mu.Unlock()
	}()
}

func (service *Service) waitForControlDecision(ctx context.Context, wait <-chan struct{}) error {
	if ctx == nil {
		<-wait
		return nil
	}
	select {
	case <-wait:
		return nil
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
}

func (service *Service) AdjustVUsers(ctx context.Context, request *pb.VUserAdjustment) (*pb.AdjustmentAck, error) {
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if request.GetGroupName() == "" {
		return nil, status.Error(codes.InvalidArgument, "group_name is required")
	}
	if request.GetTargetVusers() < 0 {
		return nil, status.Error(codes.InvalidArgument, "target_vusers must be non-negative")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	return service.applyVUserAdjustment(ctx, request)
}

func (service *Service) applyVUserAdjustment(ctx context.Context, request *pb.VUserAdjustment) (*pb.AdjustmentAck, error) {
	service.adjustMu.Lock()
	defer service.adjustMu.Unlock()
	for {
		if ctx != nil && ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		if err := service.revalidateScaleRuntime(ctx, request); err != nil {
			return nil, err
		}
		javaCapacity := service.adjustmentJavaVuserCapacityObservation(request)
		service.mu.Lock()
		decision := service.lifecycleCoordinator.planScaleLocked(service, request, javaCapacity)
		service.mu.Unlock()
		if decision.err != nil {
			return nil, decision.err
		}
		if decision.wait != nil {
			if err := service.waitForControlDecision(ctx, decision.wait); err != nil {
				return nil, err
			}
			continue
		}
		plan := decision.plan

		if plan.engine != nil {
			if ctx == nil {
				<-plan.engine.Ready()
			} else {
				select {
				case <-ctx.Done():
					service.mu.Lock()
					service.lifecycleCoordinator.rollbackScaleLocked(service, plan)
					service.releaseScaleDrainLocked(plan)
					service.mu.Unlock()
					return nil, status.FromContextError(ctx.Err()).Err()
				case <-plan.engine.Ready():
				}
				if err := ctx.Err(); err != nil {
					service.mu.Lock()
					service.lifecycleCoordinator.rollbackScaleLocked(service, plan)
					service.releaseScaleDrainLocked(plan)
					service.mu.Unlock()
					return nil, status.FromContextError(err).Err()
				}
			}
			if err := plan.engine.ScaleVUsers(plan.groupName, int(plan.targetGroupVUsers)); err != nil {
				service.mu.Lock()
				service.ensureScaleRollbackDrainLocked(plan)
				service.lifecycleCoordinator.rollbackScaleLocked(service, plan)
				service.mu.Unlock()
				service.releaseScaleDrainAfterReap(plan, plan.previousGroupVUsers)
				return nil, status.Errorf(codes.FailedPrecondition, "scale running group: %v", err)
			}
		}

		service.mu.Lock()
		var batch *pb.MetricsBatch
		var currentVUsers int32
		var commitErr error
		if ctx != nil && ctx.Err() != nil {
			commitErr = status.FromContextError(ctx.Err()).Err()
		} else {
			batch, currentVUsers, commitErr = service.lifecycleCoordinator.commitScaleLocked(service, plan)
		}
		service.mu.Unlock()
		if commitErr == nil {
			service.releaseScaleDrainAfterReap(plan, plan.targetGroupVUsers)
			service.broadcastMetrics(batch)
			return &pb.AdjustmentAck{Accepted: true, CurrentVusers: currentVUsers}, nil
		}

		if plan.engine != nil {
			service.mu.Lock()
			service.prepareScaleRollbackOverlapLocked(plan)
			service.mu.Unlock()
			if rollbackErr := plan.engine.ScaleVUsers(plan.groupName, int(plan.previousGroupVUsers)); rollbackErr != nil {
				service.mu.Lock()
				reservationCurrent := service.lifecycleCoordinator.releaseScaleWithoutRollbackLocked(service, plan)
				service.mu.Unlock()
				service.releaseScaleDrainAfterReap(plan, plan.targetGroupVUsers)
				if !reservationCurrent {
					return nil, commitErr
				}
				return nil, status.Errorf(codes.Internal, "%v; rollback running group: %v", commitErr, rollbackErr)
			}
		}
		service.mu.Lock()
		if plan.engine != nil {
			service.normalizeScaleRollbackDrainLocked(plan)
			service.ensureScaleRollbackDrainLocked(plan)
		}
		service.lifecycleCoordinator.rollbackScaleLocked(service, plan)
		service.mu.Unlock()
		if plan.engine == nil {
			service.mu.Lock()
			service.releaseScaleDrainLocked(plan)
			service.mu.Unlock()
		} else {
			service.releaseScaleDrainAfterReap(plan, plan.previousGroupVUsers)
		}
		return nil, commitErr
	}
}

type durationEngineTarget struct {
	engine *engine.MultiGroupEngine
	target time.Duration
}

type durationEngineApplied struct {
	engine   *engine.MultiGroupEngine
	previous time.Duration
}

type durationTransitionPlan struct {
	control      *pendingAssignmentControl
	testID       string
	generation   uint64
	duration     time.Duration
	participants []durationEngineTarget
}

type durationTransitionDecision struct {
	plan *durationTransitionPlan
	wait <-chan struct{}
	err  error
}

func (coordinator *assignmentLifecycleCoordinator) planDurationLocked(service *Service, testID string, generation uint64, duration time.Duration) durationTransitionDecision {
	if stage := coordinator.pendingStages[testID]; stage != nil {
		return durationTransitionDecision{wait: stage.done}
	}
	if control := coordinator.pendingControls[testID]; control != nil {
		return durationTransitionDecision{wait: control.done}
	}
	if err := service.validateMutationGenerationLocked(generation); err != nil {
		return durationTransitionDecision{err: err}
	}
	assignment, ok := service.assignments[testID]
	if !ok {
		return durationTransitionDecision{err: status.Error(codes.NotFound, "test assignment not found")}
	}
	if _, stopping := coordinator.stoppingTests[testID]; stopping {
		return durationTransitionDecision{err: status.Error(codes.FailedPrecondition, "test assignment is stopping")}
	}
	if assignment.executionGeneration != generation {
		return durationTransitionDecision{err: status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)}
	}
	if !assignment.committed {
		return durationTransitionDecision{err: status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")}
	}
	if service.draining && duration > assignedTestDuration(assignment) {
		return durationTransitionDecision{err: status.Error(codes.Unavailable, "generator is draining and cannot extend test duration")}
	}

	participants := make([]durationEngineTarget, 0)
	if service.executionEnabled {
		executions := service.executions[testID]
		groupNames := make([]string, 0, len(executions))
		for groupName := range executions {
			groupNames = append(groupNames, groupName)
		}
		sort.Strings(groupNames)
		for _, groupName := range groupNames {
			execution := executions[groupName]
			if execution == nil || execution.engine == nil {
				continue
			}
			if !execution.started.Load() {
				return durationTransitionDecision{err: status.Error(codes.FailedPrecondition, "test has staged groups that are not committed")}
			}
			target := duration - execution.durationOffset
			if target <= 0 {
				target = time.Nanosecond
			}
			participants = append(participants, durationEngineTarget{engine: execution.engine, target: target})
		}
	}
	control, wait := coordinator.reserveControlLocked(testID, generation, assignmentControlDuration)
	if wait != nil {
		return durationTransitionDecision{wait: wait}
	}
	return durationTransitionDecision{plan: &durationTransitionPlan{
		control:      control,
		testID:       testID,
		generation:   generation,
		duration:     duration,
		participants: participants,
	}}
}

func applyDurationParticipants(participants []durationEngineTarget) ([]durationEngineApplied, error) {
	applied := make([]durationEngineApplied, 0, len(participants))
	for _, participant := range participants {
		previous := participant.engine.GetBaseTargetDuration()
		if err := participant.engine.AdjustDuration(participant.target); err != nil {
			return applied, err
		}
		applied = append(applied, durationEngineApplied{engine: participant.engine, previous: previous})
	}
	return applied, nil
}

func rollbackDurationParticipants(applied []durationEngineApplied) error {
	rollbackErrors := make([]error, 0)
	for index := len(applied) - 1; index >= 0; index-- {
		if err := applied[index].engine.AdjustDuration(applied[index].previous); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
}

func (coordinator *assignmentLifecycleCoordinator) commitDurationLocked(service *Service, plan *durationTransitionPlan) (*pb.DurationAck, error) {
	if !coordinator.controlCurrentLocked(plan.control) {
		return nil, status.Error(codes.Aborted, "duration control changed while applying")
	}
	if err := service.validateMutationGenerationLocked(plan.generation); err != nil {
		return nil, err
	}
	assignment, ok := service.assignments[plan.testID]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	if _, stopping := coordinator.stoppingTests[plan.testID]; stopping {
		return nil, status.Error(codes.FailedPrecondition, "test assignment is stopping")
	}
	if assignment.executionGeneration != plan.generation {
		return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	if !assignment.committed {
		return nil, status.Error(codes.FailedPrecondition, "test assignment is staged but not committed")
	}
	assignment.duration = plan.duration
	assignment.durationSeconds = compatibilityDurationSeconds(plan.duration)
	service.assignments[plan.testID] = assignment
	if !service.executionEnabled {
		service.scheduleCompletionLocked(plan.testID, plan.duration)
	}
	coordinator.completeControlLocked(plan.control)
	return &pb.DurationAck{
		Accepted:               true,
		CurrentDurationSeconds: assignment.durationSeconds,
		CurrentDuration:        durationpb.New(plan.duration),
	}, nil
}

func (service *Service) AdjustDuration(ctx context.Context, request *pb.DurationAdjustment) (*pb.DurationAck, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "duration adjustment is required")
	}
	if request.GetTestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "test_id is required")
	}
	duration, err := validatedAssignmentDuration(request.GetTargetDuration(), request.GetTargetDurationSeconds(), "target_duration", false)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
	}
	return service.applyDurationAdjustment(ctx, request, duration)
}

func (service *Service) applyDurationAdjustment(ctx context.Context, request *pb.DurationAdjustment, duration time.Duration) (*pb.DurationAck, error) {
	for {
		service.mu.Lock()
		if ctx != nil && ctx.Err() != nil {
			service.mu.Unlock()
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		decision := service.lifecycleCoordinator.planDurationLocked(service, request.GetTestId(), request.GetExecutionGeneration(), duration)
		service.mu.Unlock()
		if decision.err != nil {
			return nil, decision.err
		}
		if decision.wait != nil {
			if err := service.waitForControlDecision(ctx, decision.wait); err != nil {
				return nil, err
			}
			continue
		}
		plan := decision.plan
		applied, applyErr := applyDurationParticipants(plan.participants)
		if applyErr != nil {
			rollbackErr := rollbackDurationParticipants(applied)
			service.abortControl(plan.control)
			if rollbackErr != nil {
				return nil, status.Errorf(codes.Internal, "adjust running duration: %v; rollback failed: %v", applyErr, rollbackErr)
			}
			return nil, status.Errorf(codes.FailedPrecondition, "adjust running duration: %v", applyErr)
		}

		service.mu.Lock()
		var ack *pb.DurationAck
		var commitErr error
		if ctx != nil && ctx.Err() != nil {
			commitErr = status.FromContextError(ctx.Err()).Err()
		} else {
			ack, commitErr = service.lifecycleCoordinator.commitDurationLocked(service, plan)
		}
		service.mu.Unlock()
		if commitErr == nil {
			return ack, nil
		}
		rollbackErr := rollbackDurationParticipants(applied)
		service.abortControl(plan.control)
		if rollbackErr != nil {
			return nil, status.Errorf(codes.Internal, "%v; rollback running duration: %v", commitErr, rollbackErr)
		}
		return nil, commitErr
	}
}
