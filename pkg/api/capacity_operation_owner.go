package api

import (
	"context"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

// capacityOperationOwner belongs to one launch, never to whichever attempt or
// leadership epoch happens to be current when its provider call returns.
type capacityOperationOwner struct {
	server       *Server
	requestID    string
	attemptToken string
	term         executionLeaseTerm
	ctx          context.Context
	cancel       context.CancelFunc
}

func (s *Server) capacityOperationOwner(request *storage.ProvisioningRequest, parents ...context.Context) (*capacityOperationOwner, error) {
	ctx, cancel := s.runner.executionOperationContext(context.Background())
	term, _ := executionLeaseTermFromContext(ctx)
	if len(parents) > 0 && parents[0] != nil {
		expected, hasTerm := executionLeaseTermFromContext(parents[0])
		if parents[0].Err() != nil || (hasTerm && expected != term) {
			cancel()
			return nil, errNotExecutionLeader
		}
	}
	owner := &capacityOperationOwner{s, request.ID, request.AttemptToken, term, ctx, cancel}
	if !owner.current() {
		cancel()
		return nil, errNotExecutionLeader
	}
	return owner, nil
}

func (o *capacityOperationOwner) locallyCurrent() bool {
	return o.ctx.Err() == nil && o.server.runner.executionLeaseTermLocallyCurrent(o.term)
}

func (o *capacityOperationOwner) current() bool {
	return o.locallyCurrent() && o.server.runner.executionLeaseTermCurrent(o.term)
}

func (o *capacityOperationOwner) request() (*storage.ProvisioningRequest, error) {
	request, err := o.server.store.GetProvisioningRequest(o.requestID)
	if err != nil {
		return nil, err
	}
	if request.AttemptToken != o.attemptToken {
		return nil, errors.New("capacity operation changed concurrently")
	}
	if !o.current() {
		return nil, errNotExecutionLeader
	}
	return request, nil
}

func (o *capacityOperationOwner) finish() {
	o.server.restoreFleetPoolAfterScaleDown(o)
	o.cancel()
	o.server.endFleetProvisioningAttempt(o.requestID, o.attemptToken)
}

func (s *Server) beginCapacityOperation(owner *capacityOperationOwner) bool {
	s.fleetProvisioningMu.Lock()
	defer s.fleetProvisioningMu.Unlock()
	s.expireCapacityOperationLocked(owner.requestID)
	if s.fleetProvisioningActive[owner.requestID] != "" || !owner.locallyCurrent() {
		return false
	}
	if s.fleetProvisioningActive == nil {
		s.fleetProvisioningActive = make(map[string]string)
	}
	if s.fleetCapacityOwners == nil {
		s.fleetCapacityOwners = make(map[string]*capacityOperationOwner)
	}
	s.fleetProvisioningActive[owner.requestID] = owner.attemptToken
	s.fleetCapacityOwners[owner.requestID] = owner
	return true
}

// A provider may ignore cancellation. Recovery must not wait for that old call
// to return before a newly acquired epoch can claim its own attempt.
func (s *Server) expireCapacityOperationLocked(requestID string) {
	if owner := s.fleetCapacityOwners[requestID]; owner != nil && !owner.locallyCurrent() {
		delete(s.fleetProvisioningActive, requestID)
		delete(s.fleetCapacityOwners, requestID)
	}
}

func (o *capacityOperationOwner) transition(request *storage.ProvisioningRequest, revision int, token string) error {
	// Transfer transient placement ownership with the durable attempt claim,
	// before cancellation or late cleanup can release the former owner's drain.
	claiming := token != o.attemptToken
	if claiming {
		o.server.fleetProvisioningMu.Lock()
		defer o.server.fleetProvisioningMu.Unlock()
	}
	if !o.current() {
		return errNotExecutionLeader
	}
	store, ok := o.server.store.(interface {
		CommitCapacityOperation(*storage.ProvisioningRequest, int, string, string, string, uint64, bool) (bool, error)
	})
	if !ok {
		return errors.New("storage does not support fenced capacity operations")
	}
	leaseName := ""
	if o.server.runner != nil && o.server.runner.durableExecutionFence {
		leaseName = executionLeaseName
	}
	commitWarmSize := request.Status == "success" && !isAutomaticRunCapacityRequest(request)
	changed, err := store.CommitCapacityOperation(request, revision, token, leaseName, o.term.ownerID, o.term.generation, commitWarmSize)
	if err != nil {
		return err
	}
	if !changed {
		return errors.New("capacity operation changed concurrently")
	}
	if claiming {
		o.server.adoptCapacityDrainLocked(o)
	}
	return nil
}

func (o *capacityOperationOwner) updateIntent(intent *storage.CapacityInstanceIntent, expected time.Time) (bool, error) {
	if !o.current() {
		return false, errNotExecutionLeader
	}
	store, ok := o.server.store.(interface {
		UpdateCapacityInstanceIntentIfAttemptAndLeaseTermHeld(*storage.CapacityInstanceIntent, time.Time, string, string, string, string, uint64) (bool, error)
	})
	if !ok {
		return false, errors.New("storage does not support fenced capacity intents")
	}
	leaseName := ""
	if o.server.runner != nil && o.server.runner.durableExecutionFence {
		leaseName = executionLeaseName
	}
	return store.UpdateCapacityInstanceIntentIfAttemptAndLeaseTermHeld(intent, expected, o.requestID, o.attemptToken, leaseName, o.term.ownerID, o.term.generation)
}

func updateOwnedCapacityIntent(store storage.CapacityInstanceIntentStore, intent *storage.CapacityInstanceIntent, expected time.Time, owners []*capacityOperationOwner) (bool, error) {
	if len(owners) > 0 {
		return owners[0].updateIntent(intent, expected)
	}
	return store.UpdateCapacityInstanceIntentIfUnchanged(intent, expected)
}

func capacityOwnersCurrent(owners []*capacityOperationOwner) bool {
	if len(owners) == 0 {
		return true
	}
	_, err := owners[0].request()
	return err == nil
}

// Cancellation is durable work too: a successor resumes cancelling with a new
// owner instead of accidentally restarting the original scale-up.
func (s *Server) cancelOwnedCapacityOperation(owner *capacityOperationOwner, backend compute.Backend, base compute.CapacityRequest, intents []*storage.CapacityInstanceIntent) error {
	request, err := owner.request()
	if err != nil {
		return err
	}
	if request.Status != "cancelling" {
		return errors.New("capacity operation is no longer cancelling")
	}
	attachCapacityLaunchIntents(&base, intents)
	ctx, cancel := context.WithTimeout(owner.ctx, 30*time.Second)
	providerErr := backend.Cancel(ctx, base)
	cancel()
	if _, err := owner.request(); err != nil {
		return err
	}
	now := time.Now().UTC()
	revision := request.Revision
	request.Revision++
	request.Status, request.CompletedAt = "cancelled", &now
	if providerErr != nil {
		request.Status, request.LastError = "failed", "provider cancellation failed: "+providerErr.Error()
	} else {
		s.markCapacityIntentsTerminated(intents, storage.CapacityArchiveTerminal, owner)
	}
	if err := owner.transition(request, revision, owner.attemptToken); err != nil {
		return err
	}
	return providerErr
}

func (o *capacityOperationOwner) patchGeneratorMetadata(generatorID string, patch storage.GeneratorMetadataPatch) error {
	if !o.current() {
		return errNotExecutionLeader
	}
	store, ok := o.server.store.(interface {
		PatchGeneratorMetadataIfCapacityAttemptHeld(string, storage.GeneratorMetadataPatch, string, string, string, string, uint64) (*storage.GeneratorMetadata, error)
	})
	if !ok {
		return errors.New("storage does not support fenced capacity membership")
	}
	leaseName := ""
	if o.server.runner != nil && o.server.runner.durableExecutionFence {
		leaseName = executionLeaseName
	}
	_, err := store.PatchGeneratorMetadataIfCapacityAttemptHeld(generatorID, patch, o.requestID, o.attemptToken, leaseName, o.term.ownerID, o.term.generation)
	return err
}
