package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	generatorMaxInstanceLifetimeEnv     = "APEXLOAD_GENERATOR_MAX_INSTANCE_LIFETIME"
	defaultGeneratorMaxInstanceLifetime = 4 * time.Hour
	automaticRunCapacityReasonPrefix    = "automatic capacity for run "
)

func (s *Server) capacityBackendForMaintenance(ctx context.Context, pool *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	if s == nil || s.capacityBackends == nil {
		return nil, nil, errCapacityBackendUnavailable
	}
	if resolver, ok := s.capacityBackends.(capacityBackendMaintenanceResolver); ok {
		return resolver.BackendForPoolMaintenance(ctx, pool)
	}
	return s.capacityBackends.BackendForPool(ctx, pool)
}

// startAsyncCapacityProvisioning handles normalized PaaS targets. Kubernetes
// returns handled=false and continues through the byte-compatible scaler path.
func (s *Server) startAsyncCapacityProvisioning(w http.ResponseWriter, r *http.Request, pool *storage.FleetPool, request *storage.ProvisioningRequest, now time.Time) (bool, error) {
	if pool == nil || request == nil || strings.TrimSpace(pool.ExecutionTargetID) == "" || s.executionTargetStore == nil {
		return false, nil
	}
	owner, err := s.capacityOperationOwner(request, r.Context())
	if err != nil {
		return true, err
	}
	launched := false
	defer func() {
		if !launched {
			owner.cancel()
		}
	}()
	target, err := s.executionTargetStore.GetExecutionTarget(pool.ExecutionTargetID)
	if err != nil {
		return true, fmt.Errorf("load execution target: %w", err)
	}
	if target.Kind == storage.ExecutionTargetKubernetes {
		return false, nil
	}
	if s.capacityBackends == nil {
		return true, errCapacityBackendUnavailable
	}
	backend, target, err := s.capacityBackends.BackendForPool(r.Context(), pool)
	if err != nil {
		return true, err
	}

	timeout := time.Duration(pool.ReadinessTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := now.Add(timeout)
	// Operator provisioning requests manage durable warm capacity. Ephemeral
	// burst instances are owned by a run and are launched only by
	// ensureRunCapacity, which gives them an archive-safe teardown owner.
	warmFloor := request.DesiredCount
	capacityRequest := compute.CapacityRequest{
		OperationID: request.ID, IdempotencyKey: request.ID, ExecutionTargetID: target.ID,
		FleetPoolID: pool.ID, RuntimeProfileID: pool.RuntimeProfileID, ConnectivityMode: target.ConnectivityMode, Desired: request.DesiredCount,
		WarmFloor: warmFloor, Deadline: deadline, Admission: firstNonEmpty(pool.CapacityAdmission, storage.CapacityAdmissionStrict),
	}
	applyStandingRollback(&capacityRequest, pool, backend.Describe())
	plan, err := backend.Plan(r.Context(), capacityRequest)
	if err != nil {
		return true, fmt.Errorf("capacity preflight failed: %w", err)
	}

	if plan.Launch > 0 {
		return true, fmt.Errorf("%w: this target only supports run-owned burst instances; operator provisioning requires durable warm capacity", compute.ErrUnsupportedOperation)
	}
	if !owner.current() {
		return true, errNotExecutionLeader
	}
	request.Status = "provisioning"
	request.ExecutionTargetID = target.ID
	request.IdempotencyKey = request.ID
	request.DeadlineAt = &deadline
	request.PendingCount = request.DesiredCount
	if err := s.createProvisioningRequest(request); err != nil {
		return true, err
	}
	s.saveProvisioningEvent(request.ID, "requested", "asynchronous capacity operation accepted", now)
	s.saveProvisioningEvent(request.ID, "preflight", fmt.Sprintf("planned %d standing generator(s)", plan.Standing), now)
	if !s.beginCapacityOperation(owner) {
		owner.cancel()
		return true, errors.New("provisioning request already has an active reconcile attempt")
	}
	if !s.launchBackground(func() { s.reconcileAsyncCapacityOperation(owner, pool, backend, capacityRequest, nil) }) {
		owner.finish()
		return true, errors.New("controller is stopping and cannot start a capacity operation")
	}
	launched = true
	s.recordAuditEvent(r, "provisioning.request.create", "success", "provisioning_request", request.ID, request.ProjectID, map[string]any{
		"fleet_pool_id": request.FleetPoolID, "execution_target_id": target.ID, "desired_count": request.DesiredCount, "status": request.Status,
	})
	s.broadcastFleetEvent("provisioning_request_updated", map[string]any{"request_id": request.ID, "fleet_pool_id": request.FleetPoolID, "status": request.Status})
	s.writeProvisioningRequestStatus(w, request.ID, http.StatusAccepted)
	return true, nil
}

func (s *Server) reconcileAsyncCapacityOperation(owner *capacityOperationOwner, pool *storage.FleetPool, backend compute.Backend, base compute.CapacityRequest, intents []*storage.CapacityInstanceIntent) {
	defer owner.finish()
	attachCapacityLaunchIntents(&base, intents)
	ctx, cancel := context.WithDeadline(owner.ctx, base.Deadline)
	defer cancel()
	if _, err := owner.request(); err != nil {
		return
	}
	var drainingRunIDs []string
	if pool != nil && base.Desired < pool.DesiredSize {
		var err error
		err = s.cordonFleetPoolForScaleDown(pool.ID, owner)
		if err != nil {
			if request, loadErr := owner.request(); loadErr == nil {
				now := time.Now().UTC()
				revision := request.Revision
				request.Revision++
				request.Status, request.LastError, request.CompletedAt = "failed", "prepare scale-down drain: "+err.Error(), &now
				_ = owner.transition(request, revision, owner.attemptToken)
			}
			return
		}
		drainingRunIDs = s.startedFleetPoolRunIDs(pool.ID)
	}
	for {
		request, err := owner.request()
		if err != nil || !canCancelProvisioningStatus(request.Status) {
			return
		}
		base.ProviderOperationIDs = decodeProviderOperationIDs(request.ProviderOperationID)
		observation := compute.Observation{}
		var reconcileErr error
		if ctx.Err() != nil {
			reconcileErr = errors.New("capacity readiness deadline exceeded")
		} else if (len(drainingRunIDs) > 0 && !s.fleetPoolRunsArchiveTerminal(drainingRunIDs)) || (pool != nil && base.Desired < pool.DesiredSize && s.poolHasPodReservations(pool.ID)) {
			observation = compute.Observation{
				Requested: base.Desired, Draining: pool.DesiredSize - base.Desired,
				RetryAfter: time.Second, Message: "waiting for active runs to drain before reducing provider capacity",
			}
		} else if ready, reason := s.capacitySweep.poolReady(base.FleetPoolID); !ready {
			observation = compute.Observation{Requested: base.Desired, RetryAfter: 5 * time.Second, Message: reason}
		} else {
			observation, reconcileErr = backend.Reconcile(ctx, base)
		}
		// Reconcile may ignore cancellation and return after takeover or retry.
		// Do not publish its observations or undo a successor's provider work.
		// Durable client tokens/nonces remain available for successor discovery.
		latest, err := owner.request()
		if err != nil || latest.Revision != request.Revision {
			return
		}
		if reconcileErr == nil && ctx.Err() != nil {
			reconcileErr = ctx.Err()
		}
		if reconcileErr == nil {
			observation = s.applyCapacityConnectivityReadiness(base, observation, owner)
		}
		if _, err := owner.request(); err != nil {
			return
		}
		now := time.Now().UTC()
		expectedRevision := request.Revision
		request.Revision++
		request.ObservedCount, request.ReadyCount = observation.Observed, observation.Ready
		request.PendingCount, request.DrainingCount, request.FailedCount = observation.Pending, observation.Draining, observation.Failed
		if observation.ProviderOperationID != "" {
			request.ProviderOperationID = encodeProviderOperationIDs(decodeProviderOperationIDs(observation.ProviderOperationID))
			base.ProviderOperationIDs = decodeProviderOperationIDs(observation.ProviderOperationID)
		}
		s.updateCapacityIntentObservations(intents, observation.Instances, owner)
		if reconcileErr != nil {
			if _, err := owner.request(); err != nil {
				return
			}
			request.LastError = reconcileErr.Error()
			// The readiness deadline can expire while we still own the operation.
			// Cleanup has its own budget, but remains tied to the original epoch.
			cleanupCtx, cleanupCancel := context.WithTimeout(owner.ctx, 30*time.Second)
			cleanupErr := backend.Cancel(cleanupCtx, base)
			cleanupCancel()
			if _, err := owner.request(); err != nil {
				return
			}
			if cleanupErr == nil {
				s.markCapacityIntentsTerminated(intents, storage.CapacityArchiveTerminal, owner)
			} else {
				request.LastError += "; provider cleanup failed: " + cleanupErr.Error()
			}
			request.Status, request.CompletedAt = "failed", &now
			if ctx.Err() != nil || !now.Before(base.Deadline) {
				request.Status = "timed_out"
			}
		} else if observation.Terminal {
			request.Status, request.CompletedAt, request.LastError = "success", &now, ""
		} else {
			request.Status = "waiting_ready"
		}
		if err := owner.transition(request, expectedRevision, owner.attemptToken); err != nil {
			return
		}
		if _, err := owner.request(); err != nil {
			return
		}
		s.saveProvisioningEvent(request.ID, request.Status, firstNonEmpty(observation.Message, request.LastError, fmt.Sprintf("%d/%d generators ready", request.ReadyCount, request.DesiredCount)), now)
		s.broadcastFleetEvent("provisioning_request_updated", map[string]any{"request_id": request.ID, "fleet_pool_id": request.FleetPoolID, "status": request.Status, "ready_count": request.ReadyCount, "desired_count": request.DesiredCount})
		if request.CompletedAt != nil {
			return
		}
		wait := observation.RetryAfter
		if wait <= 0 || wait > 10*time.Second {
			wait = 3 * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (s *Server) poolHasPodReservations(poolID string) bool {
	store, ok := s.store.(podCapacityStore)
	if !ok {
		return false
	}
	claims, err := store.ListPodCapacityReservations()
	if err != nil {
		return true
	}
	members, err := s.store.ListFleetPoolMembers(poolID)
	if err != nil {
		return true
	}
	b, ok := s.runner.getExecutionBackend().(identityDrainableExecutionBackend)
	if !ok {
		return len(claims) > 0
	}
	targets := map[string]bool{}
	for _, member := range members {
		if member != nil {
			targets[b.GeneratorTargetForID(member.GeneratorID)] = true
		}
	}
	for _, claim := range claims {
		if targets[claim.Target] {
			return true
		}
	}
	return false
}

func (s *Server) cordonFleetPoolForScaleDown(poolID string, owner *capacityOperationOwner) error {
	if s == nil || s.store == nil || s.runner == nil {
		return errors.New("live generator drain coordination is unavailable")
	}
	backend, ok := s.runner.getExecutionBackend().(capacityDrainBackend)
	if !ok {
		return errors.New("execution backend does not support owned capacity drains")
	}
	members, err := s.store.ListFleetPoolMembers(poolID)
	if err != nil {
		return err
	}
	generatorIDs := make([]string, 0, len(members))
	for _, member := range members {
		if member != nil && strings.TrimSpace(member.GeneratorID) != "" && backend.GeneratorTargetForID(member.GeneratorID) != "" {
			generatorIDs = append(generatorIDs, member.GeneratorID)
		}
	}
	// Serialize with attempt transfer and cleanup. A slow membership lookup
	// must not install an old attempt's overlay after cancellation or recovery.
	s.fleetProvisioningMu.Lock()
	defer s.fleetProvisioningMu.Unlock()
	if _, err := owner.request(); err != nil {
		return err
	}
	backend.setCapacityDrain(owner.ctx, owner.requestID, owner.attemptToken, generatorIDs)
	return nil
}

func (s *Server) startedFleetPoolRunIDs(poolID string) []string {
	if s == nil || s.runner == nil {
		return nil
	}
	ids := []string{}
	for _, test := range s.runner.ListTestSummaries() {
		if test != nil && test.Assignment != nil && test.Assignment.PoolID == poolID && test.StartedAt != nil && !isTerminalTestStatus(test.Status) {
			ids = append(ids, test.ID)
		}
	}
	return ids
}

func (s *Server) fleetPoolRunsArchiveTerminal(testIDs []string) bool {
	for _, testID := range testIDs {
		test, ok := s.runner.GetTestSummary(testID)
		if !ok || test == nil || !isTerminalTestStatus(test.Status) {
			return false
		}
		if _, err := s.store.GetRunArchive(testID); err != nil {
			return false
		}
	}
	return true
}

func (s *Server) asyncCapacityRequest(pool *storage.FleetPool, request *storage.ProvisioningRequest) compute.CapacityRequest {
	if pool == nil || request == nil {
		return compute.CapacityRequest{}
	}
	deadline := time.Now().UTC().Add(5 * time.Minute)
	if request != nil && request.DeadlineAt != nil {
		deadline = request.DeadlineAt.UTC()
	} else if pool != nil && pool.ReadinessTimeoutSeconds > 0 {
		deadline = time.Now().UTC().Add(time.Duration(pool.ReadinessTimeoutSeconds) * time.Second)
	}
	warmFloor := request.DesiredCount
	if isAutomaticRunCapacityRequest(request) {
		warmFloor = max(pool.WarmFloor, pool.DesiredSize)
	} else if pool.LifecycleMode == "" || pool.LifecycleMode == storage.FleetLifecycleStanding {
		warmFloor = request.DesiredCount
	}
	return compute.CapacityRequest{
		OperationID: request.ID, IdempotencyKey: firstNonEmpty(request.IdempotencyKey, request.ID),
		ExecutionTargetID: request.ExecutionTargetID, FleetPoolID: request.FleetPoolID,
		Desired: request.DesiredCount, WarmFloor: warmFloor, Deadline: deadline,
		Admission:            firstNonEmpty(pool.CapacityAdmission, storage.CapacityAdmissionStrict),
		ProviderOperationIDs: decodeProviderOperationIDs(request.ProviderOperationID),
	}
}

func isAutomaticRunCapacityRequest(request *storage.ProvisioningRequest) bool {
	return request != nil && strings.HasPrefix(strings.TrimSpace(request.Reason), automaticRunCapacityReasonPrefix)
}

func applyStandingRollback(request *compute.CapacityRequest, pool *storage.FleetPool, info compute.BackendInfo) {
	if request == nil || pool == nil {
		return
	}
	// pool.DesiredSize is the last successfully committed operator-managed warm
	// capacity. The request's warm floor is the new provider plan. Using the
	// configured pool.WarmFloor here would be wrong for an operator request that
	// deliberately raises warm capacity above that floor.
	before := max(pool.DesiredSize, 0)
	after := max(request.Desired, 0)
	if info.CanLaunch {
		after = min(after, max(request.WarmFloor, 0))
	}
	request.RestoreStanding = before != after
	request.RollbackStanding = before
}

func loadCapacityLaunchIntents(store storage.Store, requestID string) ([]*storage.CapacityInstanceIntent, error) {
	intentStore, ok := store.(storage.CapacityInstanceIntentStore)
	if !ok {
		return nil, errors.New("storage backend does not support durable capacity instance intents")
	}
	return intentStore.ListCapacityInstanceIntents("", requestID)
}

func attachCapacityLaunchIntents(request *compute.CapacityRequest, intents []*storage.CapacityInstanceIntent) {
	request.LaunchIntents = make([]compute.LaunchIntent, 0, len(intents))
	for _, intent := range intents {
		request.LaunchIntents = append(request.LaunchIntents, compute.LaunchIntent{
			ClientToken: intent.ClientToken,
			GeneratorID: intent.GeneratorID,
			LaunchNonce: intent.LaunchNonce,
		})
	}
}

func (s *Server) restartAsyncCapacityProvisioning(request *storage.ProvisioningRequest, pool *storage.FleetPool) (bool, error) {
	if isAutomaticRunCapacityRequest(request) {
		return true, errors.New("run-owned capacity must be retried through a new preparation or run; standalone retry would lose the immutable execution policy")
	}
	if request == nil || pool == nil || request.ExecutionTargetID == "" || s.capacityBackends == nil {
		return false, nil
	}
	expectedRevision, expectedToken := request.Revision, request.AttemptToken
	next := *request
	request = &next
	request.Revision++
	request.AttemptToken = uuid.NewString()
	owner, err := s.capacityOperationOwner(request)
	if err != nil {
		return true, err
	}
	launched := false
	defer func() {
		if !launched {
			owner.cancel()
		}
	}()
	backend, target, err := s.capacityBackends.BackendForPool(context.Background(), pool)
	if err != nil {
		return true, err
	}
	if target.Kind == storage.ExecutionTargetKubernetes {
		return false, nil
	}
	intents, err := loadCapacityLaunchIntents(s.store, request.ID)
	if err != nil {
		return true, err
	}
	now := time.Now().UTC()
	timeout := time.Duration(pool.ReadinessTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := now.Add(timeout)
	request.Status = "provisioning"
	request.CompletedAt = nil
	request.LastError = ""
	request.DeadlineAt = &deadline
	base := s.asyncCapacityRequest(pool, request)
	if !isAutomaticRunCapacityRequest(request) {
		applyStandingRollback(&base, pool, backend.Describe())
	}
	base.ConnectivityMode = target.ConnectivityMode
	for _, intent := range intents {
		if strings.TrimSpace(intent.TestID) != "" {
			base.TestID = strings.TrimSpace(intent.TestID)
			break
		}
	}
	attachCapacityLaunchIntents(&base, intents)
	plan, err := backend.Plan(context.Background(), base)
	if err != nil {
		return true, err
	}
	if plan.Launch > 0 {
		return true, fmt.Errorf("%w: operator provisioning requires durable warm capacity; use a run for burst instances", compute.ErrUnsupportedOperation)
	}
	if !s.beginCapacityOperation(owner) {
		owner.cancel()
		return true, errors.New("provisioning request already has an active reconcile attempt")
	}
	if err := owner.transition(request, expectedRevision, expectedToken); err != nil {
		owner.finish()
		return true, err
	}
	if !s.launchBackground(func() { s.reconcileAsyncCapacityOperation(owner, pool, backend, base, intents) }) {
		owner.finish()
		return true, errors.New("controller is stopping and cannot resume the capacity operation")
	}
	launched = true
	return true, nil
}

func (s *Server) updateCapacityIntentObservations(intents []*storage.CapacityInstanceIntent, instances []compute.Instance, owners ...*capacityOperationOwner) {
	store, ok := s.store.(storage.CapacityInstanceIntentStore)
	if !ok {
		return
	}
	instancesByGenerator := make(map[string]compute.Instance, len(instances))
	for _, instance := range instances {
		if generatorID := strings.TrimSpace(instance.GeneratorID); generatorID != "" {
			instancesByGenerator[generatorID] = instance
		}
	}
	// Azure Job observations currently carry only burst executions and ARM does
	// not echo the injected generator ID. Positional association is safe only
	// when the provider returned exactly the same number of instances as durable
	// burst intents. AWS observations also contain warm service tasks, so they
	// must always join through the per-launch generator identity instead.
	allowPositionalFallback := len(instances) == len(intents)
	for index, intent := range intents {
		instance, matched := instancesByGenerator[strings.TrimSpace(intent.GeneratorID)]
		if !matched {
			if !allowPositionalFallback || index >= len(instances) || strings.TrimSpace(instances[index].GeneratorID) != "" {
				continue
			}
			instance = instances[index]
		}
		expected := intent.UpdatedAt
		intent.ProviderID = instance.ProviderID
		if instance.Healthy {
			intent.State = storage.CapacityIntentReady
		} else if strings.EqualFold(instance.State, "failed") || strings.EqualFold(instance.State, "stopped") {
			intent.State = storage.CapacityIntentFailed
		} else {
			intent.State = storage.CapacityIntentLaunching
		}
		updated, err := updateOwnedCapacityIntent(store, intent, expected, owners)
		if err != nil || !updated {
			slog.Warn("failed to persist capacity instance observation", "intent_id", intent.ID, "error", err)
		}
	}
}

func encodeProviderOperationIDs(ids []string) string {
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}
func decodeProviderOperationIDs(value string) []string {
	var ids []string
	if json.Unmarshal([]byte(value), &ids) == nil {
		return ids
	}
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
func capacityTimePtr(value time.Time) *time.Time { return &value }

func generatorMaxInstanceLifetime() time.Duration {
	value := strings.TrimSpace(os.Getenv(generatorMaxInstanceLifetimeEnv))
	if value == "" {
		return defaultGeneratorMaxInstanceLifetime
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 5*time.Minute || duration > 7*24*time.Hour {
		return defaultGeneratorMaxInstanceLifetime
	}
	return duration
}

func (s *Server) ensureRunCapacity(ctx context.Context, test *ManagedTest) (runErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	request := &storage.ProvisioningRequest{ID: uuid.NewString(), Revision: 1, AttemptToken: uuid.NewString(), Status: "provisioning"}
	owner, err := s.capacityOperationOwner(request, ctx)
	if err != nil {
		return err
	}
	defer owner.finish()
	operationCtx, operationCancel := context.WithCancel(ctx)
	stopEpochCancellation := context.AfterFunc(owner.ctx, operationCancel)
	defer func() { stopEpochCancellation(); operationCancel() }()
	ctx = operationCtx
	policy := compute.CapacityPolicy{}
	if test != nil && test.preparationID != "" {
		prepared, ok := s.store.(storage.PreparationStore)
		if !ok {
			return errors.New("preparation store unavailable")
		}
		p, err := prepared.GetPreparation(test.preparationID)
		if err != nil {
			return err
		}
		policy = p.Capacity
		if err := policy.Validate(); err != nil {
			return err
		}
		if policy.Selection == "existing" {
			return nil
		}
		if policy.Selection == "ephemeral" && (test.Assignment == nil || test.Assignment.PoolID == "" || s.capacityBackends == nil) {
			return errors.New("ephemeral preparation requires an approved asynchronous capacity pool")
		}
	}
	if s == nil || test == nil || test.Assignment == nil || strings.TrimSpace(test.Assignment.PoolID) == "" || s.capacityBackends == nil {
		return nil
	}
	pool, err := s.store.GetFleetPool(strings.TrimSpace(test.Assignment.PoolID))
	if err != nil || strings.TrimSpace(pool.ExecutionTargetID) == "" {
		return err
	}
	backend, target, err := s.capacityBackends.BackendForPool(ctx, pool)
	if err != nil {
		if policy.Selection == "ephemeral" {
			return errors.New("target cannot provision preparation-owned ephemeral capacity")
		}
		if errors.Is(err, errCapacityBackendUnavailable) {
			// Kubernetes/static execution targets continue through their existing
			// discovery and scaler paths. A PaaS target, however, must fail visibly
			// when provider mutations are disabled or misconfigured; silently
			// continuing produces a misleading "no generators" error later.
			if s.executionTargetStore != nil {
				configured, loadErr := s.executionTargetStore.GetExecutionTarget(pool.ExecutionTargetID)
				if loadErr == nil && configured != nil && (configured.Kind == storage.ExecutionTargetKubernetes || configured.Kind == storage.ExecutionTargetStatic) {
					return nil
				}
			}
		}
		return err
	}
	desired := max(1, pool.DesiredSize)
	if test.CapacityAdvisory != nil && test.CapacityAdvisory.RecommendedGenerators > desired {
		desired = test.CapacityAdvisory.RecommendedGenerators
	}
	desired = max(desired, pool.MinSize)
	desired, err = effectiveProvisioningDesiredCount(pool, desired)
	if err != nil {
		return err
	}
	timeout := time.Duration(pool.ReadinessTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	now := time.Now().UTC()
	deadline := now.Add(timeout)
	cleanupDeadline := now.Add(generatorMaxInstanceLifetime())
	request.FleetPoolID, request.ProjectID = pool.ID, test.ProjectID
	request.DesiredCount, request.ExecutionTargetID = desired, target.ID
	request.IdempotencyKey, request.DeadlineAt = "run-"+test.ID, &deadline
	request.Reason = automaticRunCapacityReasonPrefix + test.ID
	base := s.asyncCapacityRequest(pool, request)
	// Automatic runs maintain the configured warm floor. A failed cold start
	// cleans up burst instances but must not tear the warm service back down.
	base.RestoreStanding = false
	base.Policy = policy
	base.TestID = test.ID
	base.ConnectivityMode = target.ConnectivityMode
	// A successor must finish its post-takeover sweep of this pool before any
	// provider launch into it (capacityTakeoverSweep). Waiting spends part of
	// the run's readiness budget rather than failing the run outright.
	if err := s.awaitCapacityPoolSweep(ctx, pool.ID, deadline); err != nil {
		return err
	}
	plan, err := backend.Plan(ctx, base)
	if err != nil {
		return err
	}
	if !owner.current() {
		return errNotExecutionLeader
	}
	if err := s.createProvisioningRequest(request); err != nil {
		return err
	}
	if !s.beginCapacityOperation(owner) {
		return errNotExecutionLeader
	}
	provisioned := false
	readyCommitted := false
	var intents []*storage.CapacityInstanceIntent
	defer func() {
		if provisioned {
			return
		}
		latest, loadErr := owner.request()
		if loadErr != nil {
			return // the successor owns uncertain resources and durable intents
		}
		ownUnpublishedSuccess := readyCommitted && latest.Status == "success" && latest.Revision == request.Revision
		if !canCancelProvisioningStatus(latest.Status) && !ownUnpublishedSuccess {
			return
		}
		cancelCtx, cancel := context.WithTimeout(owner.ctx, 30*time.Second)
		cancelErr := backend.Cancel(cancelCtx, base)
		cancel()
		if _, err := owner.request(); err != nil {
			return
		}
		if cancelErr != nil {
			slog.Error("failed to cancel partial run capacity", "test_id", test.ID, "request_id", request.ID, "error", cancelErr)
		} else {
			s.markCapacityIntentsTerminated(intents, storage.CapacityArchiveTerminal, owner)
		}

		if request.ProviderOperationID != "" {
			latest.ProviderOperationID = request.ProviderOperationID
		}
		now := time.Now().UTC()
		expectedRevision, expectedToken := latest.Revision, latest.AttemptToken
		latest.Status = "failed"
		if errors.Is(runErr, context.Canceled) {
			latest.Status = "cancelled"
		}
		if errors.Is(runErr, context.DeadlineExceeded) || now.After(deadline) {
			latest.Status = "timed_out"
		}
		latest.CompletedAt = &now
		if runErr != nil {
			latest.LastError = runErr.Error()
		}
		if cancelErr != nil {
			latest.LastError = firstNonEmpty(latest.LastError, "run capacity failed") + "; provider cleanup failed: " + cancelErr.Error()
		}
		latest.Revision++
		if err := owner.transition(latest, expectedRevision, expectedToken); err != nil {
			slog.Warn("persist failed run capacity request", "test_id", test.ID, "request_id", request.ID, "error", err)
		}
	}()
	intents = make([]*storage.CapacityInstanceIntent, 0, plan.Launch)
	intentStore, ok := s.store.(storage.CapacityInstanceIntentStore)
	if plan.Launch > 0 && !ok {
		return errors.New("storage backend does not support durable run capacity intents")
	}
	for index := 0; index < plan.Launch; index++ {
		intent := &storage.CapacityInstanceIntent{
			ID: uuid.NewString(), ExecutionTargetID: target.ID, FleetPoolID: pool.ID, TestID: test.ID,
			ProvisioningRequestID: request.ID, ClientToken: fmt.Sprintf("%s-%d", request.ID, index),
			LaunchNonce: uuid.NewString(), GeneratorID: uuid.NewString(), CleanupDeadline: &cleanupDeadline,
		}
		if err := intentStore.CreateCapacityInstanceIntent(intent); err != nil {
			return err
		}
		intents = append(intents, intent)
	}
	attachCapacityLaunchIntents(&base, intents)
	s.saveProvisioningEvent(request.ID, "requested", "automatic run capacity requested before supervision", time.Now().UTC())
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		latest, err := s.store.GetProvisioningRequest(request.ID)
		if err != nil {
			return err
		}
		if latest.Revision != request.Revision || latest.AttemptToken != request.AttemptToken {
			return fmt.Errorf("run capacity request changed concurrently to %s", latest.Status)
		}
		observation, err := backend.Reconcile(ctx, base)
		if _, ownershipErr := owner.request(); ownershipErr != nil {
			return ownershipErr
		}
		if observation.ProviderOperationID != "" {
			base.ProviderOperationIDs = strings.Split(observation.ProviderOperationID, ",")
			request.ProviderOperationID = encodeProviderOperationIDs(base.ProviderOperationIDs)
		}
		s.updateCapacityIntentObservations(intents, observation.Instances, owner)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		observation = s.applyCapacityConnectivityReadiness(base, observation, owner)
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := owner.request(); err != nil {
			return err
		}
		request.ObservedCount, request.ReadyCount, request.PendingCount = observation.Observed, observation.Ready, observation.Pending
		request.FailedCount = observation.Failed
		ready := observation.Terminal
		addresses := make([]string, 0, len(observation.Instances))
		for _, instance := range observation.Instances {
			if instance.Healthy && strings.TrimSpace(instance.Address) != "" {
				addresses = append(addresses, instance.Address)
			}
		}
		if target.ConnectivityMode == storage.ConnectivityReverse {
			ids := s.reverseCapacitySessionIDs(base)
			addresses = addresses[:0]
			for _, id := range ids {
				addresses = append(addresses, generatorrpc.ReverseTargetPrefix+id)
			}
		}
		if ready {
			now := time.Now().UTC()
			expectedRevision, expectedToken := request.Revision, request.AttemptToken
			request.Status, request.CompletedAt = "success", &now
			request.Revision++
			if err := owner.transition(request, expectedRevision, expectedToken); err != nil {
				return err
			}
			readyCommitted = true
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.runner != nil {
				s.runner.mu.Lock()
				// Stop can win while the durable readiness transition is in flight.
				// Do not attach newly provisioned placement to its terminal run;
				// the deferred provider cancellation still owns these instances.
				if err := ctx.Err(); err != nil {
					s.runner.mu.Unlock()
					return err
				}
				if !owner.locallyCurrent() {
					s.runner.mu.Unlock()
					return errNotExecutionLeader
				}
				current := s.runner.tests[test.ID]
				if (current != nil && current != test) || isTerminalTestStatus(test.Status) || test.Status == "STOPPING" || (test.executionCtx != nil && test.executionCtx.Err() != nil) {
					s.runner.mu.Unlock()
					return context.Canceled
				}
				if test.Assignment != nil {
					authored := preparationAssignmentLocked(test)
					test.Assignment.Mode = generatorAssignmentModeSpecificHosts
					test.Assignment.SelectedTargets = orderedUniqueTargets(append(test.Assignment.SelectedTargets, addresses...))
					if test.preparationID != "" {
						test.preparationCapacityAssignment = &preparationCapacityAssignment{authored: authored, resolved: cloneGeneratorAssignmentSettings(test.Assignment)}
					}
				}
				s.runner.mu.Unlock()
			}
			provisioned = true
			return nil
		}
		expectedRevision, expectedToken := request.Revision, request.AttemptToken
		request.Status = "waiting_ready"
		request.Revision++
		if err := owner.transition(request, expectedRevision, expectedToken); err != nil {
			return err
		}
		wait := observation.RetryAfter
		if wait <= 0 || wait > 5*time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// applyCapacityConnectivityReadiness prevents provider acceptance from being
// mistaken for usable generator capacity. Reverse targets require an
// authenticated outbound session; direct targets require a successful gRPC
// heartbeat to the provider-observed address.
func (s *Server) applyCapacityConnectivityReadiness(request compute.CapacityRequest, observation compute.Observation, owners ...*capacityOperationOwner) compute.Observation {
	if s == nil || strings.TrimSpace(request.ConnectivityMode) == "" {
		return observation
	}
	providerReady := observation.Ready
	sessionReady := 0
	message := "waiting for authenticated reverse generator sessions"
	if request.ConnectivityMode == storage.ConnectivityReverse {
		for _, generatorID := range s.reverseCapacitySessionIDs(request) {
			if !capacityOwnersCurrent(owners) {
				return compute.Observation{}
			}
			if strings.TrimSpace(request.FleetPoolID) != "" {
				if err := s.associateDynamicGenerator(generatorID, generatorrpc.ReverseTargetPrefix+generatorID, request.FleetPoolID, "", "ready", owners...); err != nil {
					slog.Warn("associate reverse generator with capacity pool", "generator_id", generatorID, "pool_id", request.FleetPoolID, "error", err)
					continue
				}
			}
			sessionReady++
		}
	} else if request.ConnectivityMode == storage.ConnectivityDirect {
		sessionReady = s.directGeneratorHeartbeatCount(request, observation.Instances, owners)
		message = "waiting for direct generator heartbeats"
	} else {
		return observation
	}
	observation.Ready = min(providerReady, sessionReady)
	observation.Pending = max(request.Desired-observation.Ready-observation.Failed, 0)
	observation.Terminal = observation.Ready >= request.Desired
	if !observation.Terminal {
		observation.Message = fmt.Sprintf("%s (%d/%d ready)", message, observation.Ready, request.Desired)
		if observation.RetryAfter <= 0 {
			observation.RetryAfter = time.Second
		}
	}
	return observation
}

// reverseCapacitySessionIDs prevents one run from borrowing another run's
// ephemeral generator. Sessions with no burst intent are durable warm
// capacity; sessions with an intent are eligible only for that intent's run.
func (s *Server) reverseCapacitySessionIDs(request compute.CapacityRequest) []string {
	ids := s.reverseTargetSessionIDs(request.ExecutionTargetID)
	store, ok := s.store.(storage.CapacityInstanceIntentStore)
	if !ok || len(ids) == 0 {
		return ids
	}
	intents, err := store.ListCapacityInstanceIntents(request.ExecutionTargetID, "")
	if err != nil {
		return nil // fail closed: target-wide sharing would violate run isolation
	}
	type burstSession struct {
		testID string
		active bool
	}
	burstSessions := make(map[string]burstSession)
	for _, intent := range intents {
		if intent == nil || strings.TrimSpace(intent.GeneratorID) == "" {
			continue
		}
		burstSessions[strings.TrimSpace(intent.GeneratorID)] = burstSession{
			testID: strings.TrimSpace(intent.TestID),
			active: intent.State == storage.CapacityIntentPlanned || intent.State == storage.CapacityIntentLaunching || intent.State == storage.CapacityIntentReady,
		}
	}
	eligible := make([]string, 0, len(ids))
	for _, generatorID := range ids {
		session, burst := burstSessions[generatorID]
		if !burst || (session.active && strings.TrimSpace(request.TestID) != "" && session.testID == strings.TrimSpace(request.TestID)) {
			eligible = append(eligible, generatorID)
		}
	}
	return eligible
}

func (s *Server) directGeneratorHeartbeatCount(request compute.CapacityRequest, instances []compute.Instance, owners []*capacityOperationOwner) int {
	if s == nil || s.runner == nil {
		return 0
	}
	if s.capacityHeartbeatProbe != nil {
		ready := 0
		for _, instance := range instances {
			if instance.Healthy && strings.TrimSpace(instance.Address) != "" && s.capacityHeartbeatProbe(instance.Address) {
				ready++
			}
		}
		return ready
	}
	backend, ok := s.runner.getExecutionBackend().(*distributedExecutionBackend)
	if !ok || backend == nil {
		return 0
	}
	ready := 0
	heartbeatTimeout := backend.timeout
	if heartbeatTimeout <= 0 || heartbeatTimeout > 5*time.Second {
		heartbeatTimeout = 5 * time.Second
	}
	candidates := make([]compute.Instance, 0, len(instances))
	for _, instance := range instances {
		if !instance.Healthy || strings.TrimSpace(instance.Address) == "" {
			continue
		}
		candidates = append(candidates, instance)
	}
	if len(candidates) == 0 {
		return 0
	}
	probeParent := context.Background()
	if len(owners) > 0 {
		probeParent = owners[0].ctx
	}
	probeCtx, cancel := context.WithTimeout(probeParent, heartbeatTimeout)
	defer cancel()
	jobs := make(chan compute.Instance, len(candidates))
	results := make(chan bool, len(candidates))
	workers := min(len(candidates), 8)
	for worker := 0; worker < workers; worker++ {
		go func() {
			for instance := range jobs {
				client, err := backend.dial(probeCtx, instance.Address)
				if err != nil {
					results <- false
					continue
				}
				load := backend.probeGeneratorLoad(probeCtx, instance.Address, client)
				_ = client.Close()
				if !load.HeartbeatOK || !capacityOwnersCurrent(owners) {
					results <- false
					continue
				}
				if strings.TrimSpace(request.FleetPoolID) != "" {
					if err := s.associateDynamicGenerator(load.GeneratorID, instance.Address, request.FleetPoolID, instance.ProviderID, instance.State, owners...); err != nil {
						slog.Warn("associate direct generator with capacity pool", "generator_id", load.GeneratorID, "pool_id", request.FleetPoolID, "error", err)
						results <- false
						continue
					}
				}
				results <- true
			}
		}()
	}
	for _, instance := range candidates {
		jobs <- instance
	}
	close(jobs)
	for range candidates {
		if <-results {
			ready++
		}
	}
	return ready
}

func (s *Server) associateDynamicGenerator(generatorID, target, poolID, providerID, providerStatus string, owners ...*capacityOperationOwner) error {
	if s == nil || s.store == nil || strings.TrimSpace(generatorID) == "" || strings.TrimSpace(poolID) == "" {
		return errors.New("generator identity and fleet pool are required for dynamic capacity membership")
	}
	patcher, ok := s.store.(interface {
		PatchGeneratorMetadata(string, storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error)
	})
	if !ok {
		return errors.New("storage backend does not support atomic generator membership updates")
	}
	poolID = strings.TrimSpace(poolID)
	target = strings.TrimSpace(target)
	providerID = strings.TrimSpace(providerID)
	providerStatus = strings.TrimSpace(providerStatus)
	patch := storage.GeneratorMetadataPatch{FleetPoolIDSet: true, FleetPoolID: &poolID}
	if target != "" {
		patch.Target = &target
	}
	if providerID != "" {
		patch.ProviderID = &providerID
	}
	if providerStatus != "" {
		patch.ProviderStatus = &providerStatus
	}
	if len(owners) > 0 {
		return owners[0].patchGeneratorMetadata(strings.TrimSpace(generatorID), patch)
	}
	_, err := patcher.PatchGeneratorMetadata(strings.TrimSpace(generatorID), patch)
	return err
}

func (s *Server) markCapacityIntentsTerminated(intents []*storage.CapacityInstanceIntent, archiveState string, owners ...*capacityOperationOwner) {
	store, ok := s.store.(storage.CapacityInstanceIntentStore)
	if !ok {
		return
	}
	patcher, _ := s.store.(interface {
		PatchGeneratorMetadata(string, storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error)
	})
	for _, intent := range intents {
		if intent == nil || (len(owners) > 0 && strings.TrimSpace(intent.ProviderID) == "") {
			continue
		}
		expected := intent.UpdatedAt
		intent.State = storage.CapacityIntentTerminated
		intent.ArchiveState = archiveState
		if updated, err := updateOwnedCapacityIntent(store, intent, expected, owners); err != nil || !updated {
			slog.Warn("persist terminated capacity intent", "intent_id", intent.ID, "error", err)
			continue
		}
		if len(owners) > 0 && !owners[0].current() {
			return
		}
		if patcher != nil && strings.TrimSpace(intent.GeneratorID) != "" {
			status := "terminated"
			patch := storage.GeneratorMetadataPatch{FleetPoolIDSet: true, FleetPoolID: nil, ProviderStatus: &status}
			var err error
			if len(owners) > 0 {
				err = owners[0].patchGeneratorMetadata(intent.GeneratorID, patch)
			} else {
				_, err = patcher.PatchGeneratorMetadata(intent.GeneratorID, patch)
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				slog.Warn("detach terminated dynamic generator", "generator_id", intent.GeneratorID, "error", err)
			}
		}
	}
}

func (s *Server) releaseRunCapacity(test *ManagedTest, archiveTerminal bool) {
	if s == nil || test == nil || s.capacityBackends == nil || !archiveTerminal {
		if test != nil && !archiveTerminal {
			slog.Warn("retaining ephemeral generators because the run archive is not terminal", "test_id", test.ID)
		}
		return
	}
	intentStore, ok := s.store.(storage.CapacityInstanceIntentStore)
	if !ok {
		return
	}
	intents, err := intentStore.ListCapacityInstanceIntents("", "")
	if err != nil {
		slog.Error("list run capacity for teardown", "test_id", test.ID, "error", err)
		return
	}
	byPool := make(map[string][]*storage.CapacityInstanceIntent)
	for _, intent := range intents {
		if intent != nil && intent.TestID == test.ID && intent.State != storage.CapacityIntentTerminated {
			byPool[intent.FleetPoolID] = append(byPool[intent.FleetPoolID], intent)
		}
	}
	for poolID, poolIntents := range byPool {
		pool, err := s.store.GetFleetPool(poolID)
		if err != nil {
			continue
		}
		backend, target, err := s.capacityBackendForMaintenance(context.Background(), pool)
		if err != nil {
			slog.Error("resolve run capacity backend for teardown", "test_id", test.ID, "pool_id", poolID, "error", err)
			continue
		}
		instances := make([]compute.Instance, 0, len(poolIntents))
		providerIDs := make([]string, 0, len(poolIntents))
		teardownIntents := make([]*storage.CapacityInstanceIntent, 0, len(poolIntents))
		for _, intent := range poolIntents {
			if strings.TrimSpace(intent.ProviderID) == "" {
				slog.Warn("retaining capacity intent without provider identity", "test_id", test.ID, "intent_id", intent.ID)
				continue
			}
			instances = append(instances, compute.Instance{ProviderID: intent.ProviderID, GeneratorID: intent.GeneratorID, State: intent.State, ArchiveTerminal: true})
			providerIDs = append(providerIDs, intent.ProviderID)
			teardownIntents = append(teardownIntents, intent)
		}
		if len(instances) == 0 {
			continue
		}
		request := compute.CapacityRequest{OperationID: "teardown-" + test.ID, TestID: test.ID, ExecutionTargetID: target.ID, FleetPoolID: poolID, ProviderOperationIDs: providerIDs}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err = backend.Drain(ctx, request, instances)
		if err == nil {
			err = backend.Deprovision(ctx, request, instances)
		}
		cancel()
		if err != nil {
			slog.Error("archive-safe run capacity teardown failed", "test_id", test.ID, "pool_id", poolID, "error", err)
			continue
		}
		s.markCapacityIntentsTerminated(teardownIntents, storage.CapacityArchiveTerminal)
	}
}

// reconcileOrphanedCapacityForTerm enforces the billable-resource safety net
// after a leader takeover. Normal cleanup remains archive-fenced; a
// maximum-lifetime expiry is recorded distinctly so an operator can see that
// the cost guard terminated an instance whose archive path never became
// durable.
//
// A provider failure affects only its own pool: the pool is recorded as failed
// (keeping its launches gated) and the remaining pools are still swept. The
// first call classifies every pool; later calls retry only the failed ones.
func (s *Server) reconcileOrphanedCapacityForTerm(ctx context.Context, generation uint64) error {
	if s == nil || s.runner == nil || !s.runner.holdsExecutionLeadership() {
		return nil
	}
	required, planned, retry := s.capacitySweep.pending(generation)
	if !required {
		return nil
	}
	intentStore, ok := s.store.(storage.CapacityInstanceIntentStore)
	if s.store == nil || s.capacityBackends == nil || !ok {
		s.capacitySweep.markPlanned(generation)
		return nil
	}
	intents, err := intentStore.ListCapacityInstanceIntents("", "")
	if err != nil {
		return err
	}
	byPool := make(map[string][]*storage.CapacityInstanceIntent)
	for _, intent := range intents {
		if intent != nil && intent.State != storage.CapacityIntentTerminated {
			byPool[intent.FleetPoolID] = append(byPool[intent.FleetPoolID], intent)
		}
	}
	var pools []*storage.FleetPool
	var errs []error
	if planned {
		for _, poolID := range retry {
			pool, getErr := s.store.GetFleetPool(poolID)
			if errors.Is(getErr, sql.ErrNoRows) {
				// A deleted pool cannot launch again; nothing remains to gate.
				s.capacitySweep.markSwept(generation, poolID)
				continue
			}
			if getErr != nil {
				errs = append(errs, fmt.Errorf("pool %s: load pool: %w", poolID, getErr))
				continue
			}
			pools = append(pools, pool)
		}
	} else if pools, err = s.store.ListFleetPools(0, 0); err != nil {
		return err
	}
	for _, pool := range pools {
		if ctx.Err() != nil || !s.runner.holdsExecutionLeadership() {
			return firstNonNilError(ctx.Err(), errNotExecutionLeader)
		}
		if pool == nil || s.capacitySweep.alreadySwept(generation, pool.ID) {
			continue
		}
		if pool.ExecutionTargetID == "" {
			s.capacitySweep.markSwept(generation, pool.ID)
			continue
		}
		backend, target, resolveErr := s.capacityBackendForMaintenance(ctx, pool)
		if errors.Is(resolveErr, errCapacityOwnerUnavailable) {
			// Without the owner identity this pool's instances cannot be
			// recognized; keep it gated and retry rather than skip its sweep.
			resolveErr = fmt.Errorf("pool %s: %w", pool.ID, resolveErr)
			s.capacitySweep.markFailed(generation, pool.ID, resolveErr)
			errs = append(errs, resolveErr)
			continue
		}
		if resolveErr != nil || target.Kind == storage.ExecutionTargetKubernetes {
			s.capacitySweep.markSwept(generation, pool.ID)
			continue
		}
		if sweepErr := s.sweepOrphanedPoolCapacity(ctx, intentStore, backend, target, pool, byPool[pool.ID]); sweepErr != nil {
			if ctx.Err() != nil || !s.runner.holdsExecutionLeadership() {
				return firstNonNilError(ctx.Err(), errNotExecutionLeader)
			}
			sweepErr = fmt.Errorf("pool %s: %w", pool.ID, sweepErr)
			s.capacitySweep.markFailed(generation, pool.ID, sweepErr)
			errs = append(errs, sweepErr)
			continue
		}
		s.capacitySweep.markSwept(generation, pool.ID)
	}
	if !planned {
		s.capacitySweep.markPlanned(generation)
	}
	return errors.Join(errs...)
}

// sweepOrphanedPoolCapacity reconciles one pool's provider instances against
// its durable launch intents: it adopts lost launch responses, terminates
// untracked instances, and drains instances whose run archive is terminal or
// whose cleanup deadline passed.
func (s *Server) sweepOrphanedPoolCapacity(ctx context.Context, intentStore storage.CapacityInstanceIntentStore, backend compute.Backend, target *storage.ExecutionTarget, pool *storage.FleetPool, poolIntents []*storage.CapacityInstanceIntent) error {
	providerIDs := make([]string, 0, len(poolIntents))
	known := make(map[string]*storage.CapacityInstanceIntent, len(poolIntents))
	knownLaunches := make(map[string]*storage.CapacityInstanceIntent, len(poolIntents))
	for _, intent := range poolIntents {
		if intent.ProviderID != "" {
			providerIDs = append(providerIDs, intent.ProviderID)
			known[intent.ProviderID] = intent
		}
		if intent.ProvisioningRequestID != "" && intent.GeneratorID != "" && intent.LaunchNonce != "" {
			knownLaunches[strings.TrimSpace(intent.GeneratorID)+"\x00"+strings.TrimSpace(intent.LaunchNonce)] = intent
		}
	}
	startupOperationID := "startup-reconcile"
	if len(providerIDs) == 0 {
		// With no persisted provider identity, discovery must enumerate every
		// controller-owned instance in the pool so a lost launch response can
		// be adopted through its durable launch nonce.
		startupOperationID = ""
	}
	request := compute.CapacityRequest{OperationID: startupOperationID, ExecutionTargetID: target.ID, FleetPoolID: pool.ID, ProviderOperationIDs: providerIDs}
	discovered, discoverErr := backend.Discover(ctx, request)
	if discoverErr != nil {
		return fmt.Errorf("discover capacity for pool %s: %w", pool.ID, discoverErr)
	}
	if len(providerIDs) > 0 {
		allRequest := request
		allRequest.ProviderOperationIDs = nil
		// An empty operation ID deliberately asks provider discovery for every
		// controller-owned instance in this pool. The ordinary request keeps
		// its synthetic startup operation only for audit/error context.
		allRequest.OperationID = ""
		allDiscovered, allErr := backend.Discover(ctx, allRequest)
		if allErr != nil {
			return fmt.Errorf("discover untracked capacity for pool %s: %w", pool.ID, allErr)
		}
		seen := make(map[string]struct{}, len(discovered))
		for _, instance := range discovered {
			seen[instance.ProviderID] = struct{}{}
		}
		for _, instance := range allDiscovered {
			if _, exists := seen[instance.ProviderID]; !exists {
				discovered = append(discovered, instance)
			}
		}
	}
	for _, instance := range discovered {
		intent := known[instance.ProviderID]
		if intent != nil {
			delete(knownLaunches, strings.TrimSpace(intent.GeneratorID)+"\x00"+strings.TrimSpace(intent.LaunchNonce))
		}
		if intent == nil {
			nonce := strings.TrimSpace(instance.Tags["apexload.io/launch-nonce"])
			launchKey := strings.TrimSpace(instance.GeneratorID) + "\x00" + nonce
			candidate := knownLaunches[launchKey]
			if candidate != nil {
				adopted, adoptErr := backend.Adopt(ctx, compute.AdoptionRequest{
					ExecutionTargetID: target.ID,
					FleetPoolID:       pool.ID,
					ProviderID:        instance.ProviderID,
					OperationID:       candidate.ProvisioningRequestID,
					GeneratorID:       candidate.GeneratorID,
					LaunchNonce:       candidate.LaunchNonce,
				})
				if adoptErr != nil {
					return fmt.Errorf("adopt provider instance %s: %w", instance.ProviderID, adoptErr)
				}
				expected := candidate.UpdatedAt
				candidate.ProviderID = adopted.ProviderID
				if adopted.Healthy {
					candidate.State = storage.CapacityIntentReady
				} else if strings.EqualFold(adopted.State, "failed") || strings.EqualFold(adopted.State, "stopped") {
					candidate.State = storage.CapacityIntentFailed
				} else {
					candidate.State = storage.CapacityIntentLaunching
				}
				updated, updateErr := intentStore.UpdateCapacityInstanceIntentIfUnchanged(candidate, expected)
				if updateErr != nil {
					return fmt.Errorf("persist adopted provider instance %s: %w", instance.ProviderID, updateErr)
				}
				if !updated {
					return fmt.Errorf("persist adopted provider instance %s: capacity intent changed concurrently", instance.ProviderID)
				}
				intent = candidate
				known[adopted.ProviderID] = candidate
				delete(knownLaunches, launchKey)
				instance = adopted
			}
		}
		if intent == nil {
			instance.ArchiveTerminal = true
			if err := backend.Deprovision(ctx, request, []compute.Instance{instance}); err != nil {
				return fmt.Errorf("terminate untracked provider instance %s: %w", instance.ProviderID, err)
			}
			continue
		}
		expired := intent.CleanupDeadline != nil && !time.Now().UTC().Before(*intent.CleanupDeadline)
		archiveTerminal := intent.ArchiveState == storage.CapacityArchiveTerminal || capacityIntentRunArchiveTerminal(s.store, intent)
		if !expired && !archiveTerminal {
			continue
		}
		instance.ArchiveTerminal = archiveTerminal || expired
		if err := backend.Drain(ctx, request, []compute.Instance{instance}); err != nil {
			return err
		}
		if err := backend.Deprovision(ctx, request, []compute.Instance{instance}); err != nil {
			return err
		}
		if expired && !archiveTerminal {
			s.markCapacityIntentsTerminated([]*storage.CapacityInstanceIntent{intent}, storage.CapacityArchiveExpired)
		} else {
			s.markCapacityIntentsTerminated([]*storage.CapacityInstanceIntent{intent}, storage.CapacityArchiveTerminal)
		}
	}
	return nil
}

func capacityIntentRunArchiveTerminal(store storage.Store, intent *storage.CapacityInstanceIntent) bool {
	if store == nil || intent == nil {
		return false
	}
	if strings.TrimSpace(intent.TestID) == "" {
		return true
	}
	record, err := store.GetTest(intent.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil || record == nil || !isTerminalTestStatus(record.Status) {
		return false
	}
	_, err = store.GetRunArchive(intent.TestID)
	return err == nil
}
