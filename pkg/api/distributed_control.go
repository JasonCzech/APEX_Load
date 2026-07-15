package api

import (
	"context"
	"sync"
)

// distributedControlGate coordinates controller-side mutations that fan out to
// more than one generator. Scales for different groups may proceed concurrently,
// but a single group's adjustment is an ordered transaction and topology changes
// (evacuation/teardown) exclude every scale for that test.
type distributedControlGate struct {
	topology sync.RWMutex
	// lifecycle serializes whole-run controls (stop, pause, resume, duration)
	// without excluding evacuation. Stop must be able to reserve STOPPING while
	// an evacuation RPC is in flight so the evacuation can detect a stale commit
	// and roll back newly assigned generators.
	lifecycle chan struct{}

	groupsMu sync.Mutex
	groups   map[string]*sync.Mutex
	refs     int
}

func (tr *TestRunner) retainDistributedControlGate(testID string) *distributedControlGate {
	tr.remoteControlMu.Lock()
	defer tr.remoteControlMu.Unlock()
	if tr.remoteControlGates == nil {
		tr.remoteControlGates = make(map[string]*distributedControlGate)
	}
	gate := tr.remoteControlGates[testID]
	if gate == nil {
		gate = &distributedControlGate{
			lifecycle: make(chan struct{}, 1),
			groups:    make(map[string]*sync.Mutex),
		}
		gate.lifecycle <- struct{}{}
		tr.remoteControlGates[testID] = gate
	}
	gate.refs++
	return gate
}

// lockTestLifecycleControl serializes a whole-run lifecycle transaction. The
// context-aware acquisition lets bounded shutdown abandon a control that is
// stuck behind another request without leaking a waiter or retaining a gate.
func (tr *TestRunner) lockTestLifecycleControl(ctx context.Context, testID string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	gate := tr.retainDistributedControlGate(testID)
	select {
	case <-gate.lifecycle:
		if err := ctx.Err(); err != nil {
			gate.lifecycle <- struct{}{}
			tr.releaseDistributedControlGate(testID, gate)
			return nil, err
		}
		return func() {
			gate.lifecycle <- struct{}{}
			tr.releaseDistributedControlGate(testID, gate)
		}, nil
	case <-ctx.Done():
		tr.releaseDistributedControlGate(testID, gate)
		return nil, ctx.Err()
	}
}

func (tr *TestRunner) releaseDistributedControlGate(testID string, gate *distributedControlGate) {
	tr.remoteControlMu.Lock()
	defer tr.remoteControlMu.Unlock()
	gate.refs--
	if gate.refs == 0 && tr.remoteControlGates[testID] == gate {
		delete(tr.remoteControlGates, testID)
	}
}

// lockDistributedGroupControl serializes the complete controller transaction for
// one test/group, including the runner's post-RPC group-total update.
func (tr *TestRunner) lockDistributedGroupControl(testID string, groupName string) func() {
	gate := tr.retainDistributedControlGate(testID)
	gate.topology.RLock()
	gate.groupsMu.Lock()
	group := gate.groups[groupName]
	if group == nil {
		group = &sync.Mutex{}
		gate.groups[groupName] = group
	}
	gate.groupsMu.Unlock()
	group.Lock()
	return func() {
		group.Unlock()
		gate.topology.RUnlock()
		tr.releaseDistributedControlGate(testID, gate)
	}
}

// lockDistributedTopologyReadControl keeps a whole-run fan-out (pause, resume,
// or duration) on one stable generator set. Stop intentionally does not use
// this gate: it must be able to mark STOPPING while evacuation is in flight so
// the evacuation's stale-commit path can fail closed.
func (tr *TestRunner) lockDistributedTopologyReadControl(testID string) func() {
	gate := tr.retainDistributedControlGate(testID)
	gate.topology.RLock()
	return func() {
		gate.topology.RUnlock()
		tr.releaseDistributedControlGate(testID, gate)
	}
}

// lockDistributedTopologyControl excludes every in-flight group scale while a
// run's generator topology is replaced or detached.
func (tr *TestRunner) lockDistributedTopologyControl(testID string) func() {
	gate := tr.retainDistributedControlGate(testID)
	gate.topology.Lock()
	return func() {
		gate.topology.Unlock()
		tr.releaseDistributedControlGate(testID, gate)
	}
}

// distributedControlCommitCurrentLocked verifies that the topology observed
// before an RPC fan-out is still the live run state. Per-group serialization
// orders scales within a group; scales for different groups intentionally do
// not advance this topology generation. Caller holds tr.mu.
func distributedControlCommitCurrentLocked(tr *TestRunner, test *ManagedTest, expectedStatus string, generation uint64) bool {
	if tr.tests[test.ID] != test || test.Status != expectedStatus || test.remoteFinalizing || test.remoteControlGeneration != generation {
		return false
	}
	return tr.isExecutionLeader() && (test.executionCtx == nil || test.executionCtx.Err() == nil)
}
