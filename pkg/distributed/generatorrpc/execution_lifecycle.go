package generatorrpc

import (
	"fmt"
	"time"
)

// assignmentLifecycleState is the generator-local lifecycle of one test
// assignment. It is deliberately separate from the protobuf lifecycle text:
// COMMITTED is an internal barrier state and PAUSED is an execution attribute,
// not a separate lifecycle transition.
type assignmentLifecycleState uint8

const (
	assignmentLifecycleUnknown assignmentLifecycleState = iota
	assignmentLifecycleStaged
	assignmentLifecycleCommitted
	assignmentLifecycleRunning
	assignmentLifecycleStopping
	assignmentLifecycleTerminal
)

func (state assignmentLifecycleState) String() string {
	switch state {
	case assignmentLifecycleStaged:
		return "STAGED"
	case assignmentLifecycleCommitted:
		return "COMMITTED"
	case assignmentLifecycleRunning:
		return "RUNNING"
	case assignmentLifecycleStopping:
		return "STOPPING"
	case assignmentLifecycleTerminal:
		return "TERMINAL"
	default:
		return "UNKNOWN"
	}
}

type assignmentLifecycleEvent uint8

const (
	assignmentLifecycleStage assignmentLifecycleEvent = iota + 1
	assignmentLifecycleCommit
	assignmentLifecycleStart
	assignmentLifecycleStop
	assignmentLifecycleFinish
)

func (event assignmentLifecycleEvent) String() string {
	switch event {
	case assignmentLifecycleStage:
		return "stage"
	case assignmentLifecycleCommit:
		return "commit"
	case assignmentLifecycleStart:
		return "start"
	case assignmentLifecycleStop:
		return "stop"
	case assignmentLifecycleFinish:
		return "finish"
	default:
		return "unknown"
	}
}

// transitionAssignmentLifecycle is the single transition table for assignment
// control decisions. Idempotent controller retries are represented explicitly;
// in particular, staging or committing a late evacuation group must not move an
// already-running test back behind the commit barrier.
func transitionAssignmentLifecycle(current assignmentLifecycleState, event assignmentLifecycleEvent) (assignmentLifecycleState, error) {
	switch current {
	case assignmentLifecycleStaged:
		switch event {
		case assignmentLifecycleStage:
			return assignmentLifecycleStaged, nil
		case assignmentLifecycleCommit:
			return assignmentLifecycleCommitted, nil
		case assignmentLifecycleStop:
			return assignmentLifecycleStopping, nil
		}
	case assignmentLifecycleCommitted:
		switch event {
		case assignmentLifecycleCommit:
			return assignmentLifecycleCommitted, nil
		case assignmentLifecycleStart:
			return assignmentLifecycleRunning, nil
		case assignmentLifecycleStop:
			return assignmentLifecycleStopping, nil
		}
	case assignmentLifecycleRunning:
		switch event {
		case assignmentLifecycleStage, assignmentLifecycleCommit, assignmentLifecycleStart:
			return assignmentLifecycleRunning, nil
		case assignmentLifecycleStop:
			return assignmentLifecycleStopping, nil
		}
	case assignmentLifecycleStopping:
		switch event {
		case assignmentLifecycleStop:
			return assignmentLifecycleStopping, nil
		case assignmentLifecycleFinish:
			return assignmentLifecycleTerminal, nil
		}
	case assignmentLifecycleTerminal:
		switch event {
		case assignmentLifecycleStop, assignmentLifecycleFinish:
			return assignmentLifecycleTerminal, nil
		}
	}
	return current, fmt.Errorf("cannot %s assignment in %s state", event, current)
}

// assignmentLifecycleCoordinator owns staging reservations/timers, the
// stopping/reuse barrier, and the pure lifecycle model. Service.mu is the
// documented compatibility lock for assignment maps, generation state, and
// coordinator methods ending in Locked during this migration. This preserves
// one atomic lock boundary for capacity and fencing decisions while engine,
// filesystem, and network work occurs only after that lock is released.
//
// The Service map fields alias these maps during the incremental migration so
// existing internal callers and characterization tests keep their current seam.
type assignmentLifecycleCoordinator struct {
	stoppingTests       map[string]struct{}
	stoppingGenerations map[string]uint64
	stopCompletions     map[string]*stopCompletion
	pendingStages       map[string]*pendingAssignmentStage
	pendingControls     map[string]*pendingAssignmentControl
	nextStagePlanToken  uint64
	nextControlToken    uint64
	stageTimers         map[string]*time.Timer
	stageTokens         map[string]uint64
	nextStageTimerToken uint64
	stageTTL            time.Duration
}

func newAssignmentLifecycleCoordinator() *assignmentLifecycleCoordinator {
	return &assignmentLifecycleCoordinator{
		stoppingTests:       make(map[string]struct{}),
		stoppingGenerations: make(map[string]uint64),
		stopCompletions:     make(map[string]*stopCompletion),
		pendingStages:       make(map[string]*pendingAssignmentStage),
		pendingControls:     make(map[string]*pendingAssignmentControl),
		stageTimers:         make(map[string]*time.Timer),
		stageTokens:         make(map[string]uint64),
		stageTTL:            defaultAssignmentStageTTL,
	}
}

// stateOf accepts legacy test fixtures that only initialize committed. New
// production assignments always persist an explicit state.
func (*assignmentLifecycleCoordinator) stateOf(assignment assignedTest) assignmentLifecycleState {
	if assignment.lifecycle != assignmentLifecycleUnknown {
		return assignment.lifecycle
	}
	if assignment.committed {
		return assignmentLifecycleRunning
	}
	return assignmentLifecycleStaged
}

func (coordinator *assignmentLifecycleCoordinator) stage(assignment assignedTest, keepRunning bool) (assignedTest, error) {
	// Execution-enabled services can stage an evacuation group without moving
	// the already-running test behind its commit boundary. The non-executing
	// compatibility path has only aggregate test state, so adding a group
	// deliberately creates a new staged boundary for the whole assignment.
	if !keepRunning {
		assignment.lifecycle = assignmentLifecycleStaged
		assignment.committed = false
		return assignment, nil
	}
	next, err := transitionAssignmentLifecycle(coordinator.stateOf(assignment), assignmentLifecycleStage)
	if err != nil {
		return assignment, err
	}
	assignment.lifecycle = next
	return assignment, nil
}

// commit crosses both internal barrier transitions while the Service lock is
// held. Engine goroutines are prepared before this call, so no protocol action
// can observe the intermediate COMMITTED state.
func (coordinator *assignmentLifecycleCoordinator) commit(assignment assignedTest) (assignedTest, error) {
	next, err := transitionAssignmentLifecycle(coordinator.stateOf(assignment), assignmentLifecycleCommit)
	if err != nil {
		return assignment, err
	}
	next, err = transitionAssignmentLifecycle(next, assignmentLifecycleStart)
	if err != nil {
		return assignment, err
	}
	assignment.lifecycle = next
	assignment.committed = true
	return assignment, nil
}

func (coordinator *assignmentLifecycleCoordinator) beginStopping(testID string, generation uint64, assignment assignedTest) (assignedTest, *stopCompletion) {
	next, err := transitionAssignmentLifecycle(coordinator.stateOf(assignment), assignmentLifecycleStop)
	if err == nil {
		assignment.lifecycle = next
	}
	coordinator.stoppingTests[testID] = struct{}{}
	coordinator.stoppingGenerations[testID] = generation
	if completion := coordinator.stopCompletions[testID]; completion != nil {
		return assignment, completion
	}
	completion := &stopCompletion{generation: generation, done: make(chan struct{})}
	coordinator.stopCompletions[testID] = completion
	return assignment, completion
}

func (coordinator *assignmentLifecycleCoordinator) finish(assignment assignedTest) assignedTest {
	state := coordinator.stateOf(assignment)
	if state != assignmentLifecycleStopping && state != assignmentLifecycleTerminal {
		state, _ = transitionAssignmentLifecycle(state, assignmentLifecycleStop)
	}
	next, err := transitionAssignmentLifecycle(state, assignmentLifecycleFinish)
	if err == nil {
		assignment.lifecycle = next
	}
	return assignment
}

func (coordinator *assignmentLifecycleCoordinator) completeStopping(testID string, generation uint64) {
	if trackedGeneration, ok := coordinator.stoppingGenerations[testID]; !ok || trackedGeneration != generation {
		return
	}
	delete(coordinator.stoppingTests, testID)
	delete(coordinator.stoppingGenerations, testID)
	completion := coordinator.stopCompletions[testID]
	if completion != nil && completion.generation == generation {
		delete(coordinator.stopCompletions, testID)
		close(completion.done)
	}
}
