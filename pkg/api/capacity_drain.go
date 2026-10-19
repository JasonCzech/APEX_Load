package api

import "context"

// Capacity drains exclude placement temporarily. They must never become
// operator intent, or finishing a scale-down could clear a later manual cordon.
type capacityDrain struct {
	ctx          context.Context
	attemptToken string
	generatorIDs map[string]struct{}
}

type capacityDrainBackend interface {
	identityDrainableExecutionBackend
	setCapacityDrain(context.Context, string, string, []string)
	adoptCapacityDrain(context.Context, string, string)
	clearCapacityDrain(string, string)
}

func (b *distributedExecutionBackend) setCapacityDrain(ctx context.Context, requestID, attemptToken string, generatorIDs []string) {
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	if executionContextExpired(ctx) {
		return
	}
	if b.capacityDrains == nil {
		b.capacityDrains = make(map[string]capacityDrain)
	}
	// Expired epochs cannot exclude placement, even if their provider never
	// returns. Reap their bookkeeping when another capacity operation starts.
	for id, drain := range b.capacityDrains {
		if executionContextExpired(drain.ctx) {
			delete(b.capacityDrains, id)
		}
	}
	drain := capacityDrain{ctx: ctx, attemptToken: attemptToken, generatorIDs: make(map[string]struct{}, len(generatorIDs))}
	for _, id := range generatorIDs {
		drain.generatorIDs[id] = struct{}{}
	}
	b.capacityDrains[requestID] = drain
}

func (b *distributedExecutionBackend) adoptCapacityDrain(ctx context.Context, requestID, attemptToken string) {
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	if drain, ok := b.capacityDrains[requestID]; ok && !executionContextExpired(ctx) {
		drain.ctx, drain.attemptToken = ctx, attemptToken
		b.capacityDrains[requestID] = drain
	}
}

func (b *distributedExecutionBackend) clearCapacityDrain(requestID, attemptToken string) {
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	if drain, ok := b.capacityDrains[requestID]; ok && drain.attemptToken == attemptToken {
		delete(b.capacityDrains, requestID)
	}
}

// Caller holds drainMu. Epoch cancellation is checked here, synchronously, so a blocked old
// worker cannot strand local exclusions after same-process lease reacquisition.
func (b *distributedExecutionBackend) capacityCordonedLocked(generatorID string) bool {
	for _, drain := range b.capacityDrains {
		if _, present := drain.generatorIDs[generatorID]; present && !executionContextExpired(drain.ctx) {
			return true
		}
	}
	return false
}

func (s *Server) restoreFleetPoolAfterScaleDown(owner *capacityOperationOwner) {
	if s == nil || s.runner == nil {
		return
	}
	s.fleetProvisioningMu.Lock()
	defer s.fleetProvisioningMu.Unlock()
	if backend, ok := s.runner.getExecutionBackend().(capacityDrainBackend); ok {
		backend.clearCapacityDrain(owner.requestID, owner.attemptToken)
	}
}

// Caller holds fleetProvisioningMu and has committed the new durable attempt.
func (s *Server) adoptCapacityDrainLocked(owner *capacityOperationOwner) {
	if s.runner == nil {
		return
	}
	if backend, ok := s.runner.getExecutionBackend().(capacityDrainBackend); ok {
		backend.adoptCapacityDrain(owner.ctx, owner.requestID, owner.attemptToken)
	}
}
