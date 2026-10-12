package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Project persistence always uses a real database lease. Single-controller
// deployments use a separate coordinator without changing run HA admission or
// enabling controller continuation. Old run attachments retain their old term
// and fail closed when the local coordinator reacquires another epoch.
func (tr *TestRunner) captureProjectLiveTableTerm() (executionLeaseTerm, error) {
	if tr.durableExecutionFence {
		return tr.captureExecutionLeaseTerm()
	}
	tr.projectLiveLeaseMu.Lock()
	defer tr.projectLiveLeaseMu.Unlock()
	tr.mu.RLock()
	stopping := tr.shuttingDown
	tr.mu.RUnlock()
	if stopping {
		return executionLeaseTerm{}, errRunnerShuttingDown
	}
	if tr.projectLiveLeadership == nil {
		store, ok := tr.dataPoolStore.(executionLeaseStore)
		if !ok {
			return executionLeaseTerm{}, errors.New("project table execution leases unavailable")
		}
		leader := newExecutionLeadership(store, "project-local-"+uuid.NewString())
		leader.tick()
		ctx, cancel := context.WithCancel(context.Background())
		tr.projectLiveLeadership = leader
		tr.projectLiveLeaseCancel = cancel
		tr.projectLiveLeaseDone = make(chan struct{})
		done := tr.projectLiveLeaseDone
		go func() { defer close(done); leader.Run(ctx) }()
	}
	owner, generation := tr.projectLiveLeadership.executionLeaseTerm()
	if owner == "" || generation == 0 {
		return executionLeaseTerm{}, errNotExecutionLeader
	}
	return executionLeaseTerm{ownerID: owner, generation: generation}, nil
}
func (tr *TestRunner) projectLiveTableTermCurrent(term executionLeaseTerm) bool {
	if tr.durableExecutionFence {
		return tr.executionLeaseTermLocallyCurrent(term)
	}
	tr.projectLiveLeaseMu.Lock()
	leader := tr.projectLiveLeadership
	tr.projectLiveLeaseMu.Unlock()
	if leader == nil || !term.valid() {
		return false
	}
	owner, generation := leader.executionLeaseTerm()
	return owner == term.ownerID && generation == term.generation
}
func (tr *TestRunner) stopProjectLiveTableLeadership() {
	tr.projectLiveLeaseMu.Lock()
	cancel, done := tr.projectLiveLeaseCancel, tr.projectLiveLeaseDone
	tr.projectLiveLeaseCancel = nil
	tr.projectLiveLeaseMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}
