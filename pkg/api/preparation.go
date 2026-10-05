package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func preparationProjectEnabled(project string) bool {
	for _, p := range strings.Split(os.Getenv("APEXLOAD_PREPARATION_PROJECTS"), ",") {
		if strings.TrimSpace(p) == project && project != "" {
			return true
		}
	}
	return false
}

func (tr *TestRunner) preparationRevision(id string) (string, error) {
	fingerprint, err := tr.scriptShakeoutExecutableFingerprint(id)
	if err != nil {
		return "", err
	}
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return "", errors.New("test not found")
	}
	project, scenario := test.ProjectID, test.ScenarioID
	override := cloneMonitorBindings(test.MonitorBindings)
	snapshot := struct {
		Goals       *GoalConfig
		Script      string
		Duration    string
		VUsers      int
		Assignment  *GeneratorAssignmentSettings
		Bindings    *storage.MonitorBindings
		Sources     []storage.MonitorSource
		Connections []int64
		Override    *storage.MonitorBindings
	}{Goals: cloneGoalConfig(test.GoalConfig), Script: fingerprint.EvidenceSHA256, Duration: test.DurationStr, VUsers: test.TotalVUsers, Assignment: cloneGeneratorAssignmentSettings(test.Assignment), Override: override}
	tr.mu.RUnlock()
	if store, ok := tr.testStore.(storage.RunMonitorStore); ok {
		if override != nil {
			snapshot.Bindings = override
		} else if scenario != "" {
			snapshot.Bindings, err = store.GetMonitorBindings(project, scenario)
		} else {
			snapshot.Bindings = &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}
		}
		if err != nil {
			return "", err
		}
		sources, err := store.ListMonitorSources(project)
		if err != nil {
			return "", err
		}
		for _, source := range sources {
			for _, binding := range snapshot.Bindings.Sources {
				if binding.SourceID == source.ID {
					snapshot.Sources = append(snapshot.Sources, source)
					if source.Pack == monitorprovider.PackAppDynamics {
						if connections, ok := tr.testStore.(storage.AppDynamicsConnectionStore); ok {
							c, e := connections.GetAppDynamicsConnection(source.ConnectionID)
							if e != nil {
								return "", e
							}
							snapshot.Connections = append(snapshot.Connections, c.Revision)
						}
					}
					if source.Pack == monitorprovider.PackPrometheusQuery {
						if connections, ok := tr.testStore.(storage.PrometheusConnectionStore); ok {
							c, e := connections.GetPrometheusConnection(source.ConnectionID)
							if e != nil {
								return "", e
							}
							snapshot.Connections = append(snapshot.Connections, c.Revision)
						}
					}
					if source.Pack == monitorprovider.PackDynatrace {
						if connections, ok := tr.testStore.(storage.DynatraceConnectionStore); ok {
							c, e := connections.GetDynatraceConnection(source.ConnectionID)
							if e != nil {
								return "", e
							}
							snapshot.Connections = append(snapshot.Connections, c.Revision)
						}
					}
					break
				}
			}
		}
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Server) handlePreparation(w http.ResponseWriter, r *http.Request) {
	project, status, message := resolveTestProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	// Disabling new preparations must not strand an existing reservation or
	// hide its audit history. Read and cancellation keep their ordinary RBAC.
	if r.Method == http.MethodPost && !preparationProjectEnabled(project) {
		writeError(w, 409, "preparation is not enabled for this project")
		return
	}
	store, ok := s.store.(storage.PreparationStore)
	if !ok {
		writeError(w, 501, "durable preparation unavailable")
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodGet {
		p, err := store.LatestPreparation(id)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 200, map[string]any{"state": "none"})
			return
		}
		if err != nil {
			writeError(w, 503, "cannot read preparation")
			return
		}
		writeJSON(w, 200, safePreparation(p))
		return
	}
	if !s.requireExecutionLeader(w) {
		return
	}
	if r.Method == http.MethodDelete {
		p, err := store.LatestPreparation(id)
		if err != nil {
			writeError(w, 404, "preparation not found")
			return
		}
		if p.State == "cancelled" || p.State == "expired" || p.State == "failed" {
			writeJSON(w, 200, safePreparation(p))
			return
		}
		next, err := store.TransitionPreparation(p.ID, p.State, "cancelled", p.Revision, p.OwnerFence, nil)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.runner.cancelPreparationExecution(id, p.ID)
		s.recordAuditEvent(r, "test.preparation.cancel", "success", "test", id, project, nil)
		writeJSON(w, 200, safePreparation(next))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req struct {
		Capacity         compute.CapacityPolicy `json:"capacity"`
		TTLSeconds       int                    `json:"ttl_seconds"`
		ExpectedRevision string                 `json:"expected_revision"`
		PreparationID    string                 `json:"preparation_id"`
		Revision         int64                  `json:"revision"`
	}
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	if err := req.Capacity.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.TTLSeconds == 0 {
		req.TTLSeconds = 1800
	}
	if req.TTLSeconds < 300 || req.TTLSeconds > 14400 {
		writeError(w, 400, "preparation TTL must be 300-14400 seconds")
		return
	}
	s.runner.mu.RLock()
	test := s.runner.tests[id]
	if test == nil {
		s.runner.mu.RUnlock()
		writeError(w, 404, "test not found")
		return
	}
	assignment := cloneGeneratorAssignmentSettings(test.Assignment)
	unsupported := test.ScriptShakeout
	for _, group := range test.groupSpecs {
		if group.Script != nil && (group.Script.Protocol == "java_vuser" || group.Script.Protocol == "bruno") {
			unsupported = true
		}
	}
	s.runner.mu.RUnlock()
	if req.PreparationID == "" && req.Capacity.Selection == "ephemeral" {
		if assignment == nil || assignment.PoolID == "" || s.capacityBackends == nil {
			writeError(w, 422, "ephemeral preparation requires a supported capacity pool")
			return
		}
		pool, loadErr := s.store.GetFleetPool(assignment.PoolID)
		if loadErr != nil {
			writeError(w, 422, "capacity pool unavailable")
			return
		}
		backend, _, backendErr := s.capacityBackends.BackendForPool(r.Context(), pool)
		if backendErr != nil {
			writeError(w, 422, "target cannot provision preparation-owned ephemeral capacity")
			return
		}
		if _, policyErr := req.Capacity.SpotCount(max(1, pool.DesiredSize, pool.MinSize), backend.Describe()); policyErr != nil {
			writeError(w, 422, policyErr.Error())
			return
		}
	}
	if unsupported {
		writeError(w, 422, "this runtime or shakeout policy cannot currently guarantee traffic-free preparation")
		return
	}
	if err := s.runner.validateStartSecretRequirements(id); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	revision, err := s.runner.preparationRevision(id)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if req.ExpectedRevision != "" && req.ExpectedRevision != revision {
		writeError(w, 409, "execution revision changed")
		return
	}
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	if req.PreparationID != "" {
		p, err := store.GetPreparation(req.PreparationID)
		if err != nil || p.TestID != id || p.ExecutionRevision != revision || p.OwnerFence != term.ownerID || p.State != "ready" || req.Revision != p.Revision || !s.runner.preparationReservationValid(id, p) {
			writeError(w, 409, "preparation changed or lost its reservation")
			return
		}
		if err = s.runner.validatePreparedMonitorReadiness(id); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		next, err := store.RenewPreparation(p.ID, p.Revision, p.OwnerFence, time.Duration(req.TTLSeconds)*time.Second)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.recordAuditEvent(r, "test.preparation.renew", "success", "test", id, project, nil)
		writeJSON(w, 200, safePreparation(next))
		return
	}
	now := time.Now().UTC()
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	}
	p, err := store.CreatePreparation(storage.Preparation{Capacity: req.Capacity, ID: uuid.NewString(), TestID: id, ProjectID: project, ExecutionRevision: revision, IdempotencyKey: key, OwnerFence: term.ownerID, CreatedAt: now, ExpiresAt: now.Add(time.Duration(req.TTLSeconds) * time.Second)})
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	s.runner.mu.Lock()
	test = s.runner.tests[id]
	already := test != nil && test.preparationID == p.ID
	if test != nil && !already && p.State == "preparing" {
		test.preparationID = p.ID
	}
	s.runner.mu.Unlock()
	if !already && p.State == "preparing" {
		actorType, actorSubject, actorName := s.resolveAuditActor(r)
		opts := StartTestOptions{prepareOnly: true, actor: testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorName}}
		if err = s.runner.startTestWithOptionsAndExpectedTerm(id, opts, &term); err != nil {
			_, _ = store.TransitionPreparation(p.ID, "preparing", "failed", p.Revision, p.OwnerFence, nil)
			s.runner.mu.Lock()
			if test != nil && test.preparationID == p.ID && (test.Status == "QUEUED" || test.Status == "PENDING") {
				test.preparationID = ""
			}
			s.runner.mu.Unlock()
			writeError(w, 409, err.Error())
			return
		}
		go s.runner.watchPreparation(p.ID, id)
	}
	s.recordAuditEvent(r, "test.preparation.create", "started", "test", id, project, map[string]any{"preparation_id": p.ID, "execution_revision": revision})
	writeJSON(w, 202, safePreparation(p))
}

// awaitPreparedStart is placed after immutable staging/reservation but before
// any runtime preflight, protocol initialization, probe, or execution commit.
func (tr *TestRunner) awaitPreparedStart(test *ManagedTest) error {
	if test.preparationID == "" {
		return tr.validatePreparedMonitorReadiness(test.ID)
	}
	defer func() {
		tr.mu.Lock()
		test.preparationPeerCheck = nil
		tr.mu.Unlock()
	}()
	store, ok := tr.testStore.(storage.PreparationStore)
	if !ok {
		return errors.New("preparation storage unavailable")
	}
	p, err := store.GetPreparation(test.preparationID)
	if err != nil {
		return err
	}
	if p.State != "preparing" {
		return storage.ErrPreparationConflict
	}
	tr.mu.RLock()
	ctx := test.executionCtx
	allocation, err := json.Marshal(test.GeneratorShards)
	tr.mu.RUnlock()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if current, err := tr.preparationRevision(test.ID); err != nil || current != p.ExecutionRevision {
		_, _ = tr.transitionPreparation(p.ID, "preparing", "failed", p.Revision, p.OwnerFence, nil)
		return errors.New("execution revision changed while preparing")
	}
	if err := tr.validatePreparedMonitorReadiness(test.ID); err != nil {
		return err
	}
	if !tr.preparationReservationValid(test.ID, p) {
		return errors.New("prepared reservation unavailable before readiness")
	}
	p, err = tr.transitionPreparation(p.ID, "preparing", "ready", p.Revision, p.OwnerFence, allocation)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := store.GetPreparation(p.ID)
			if err != nil {
				return err
			}
			if !tr.isExecutionLeader() {
				return errNotExecutionLeader
			}
			if current.State == "consumed" {
				return nil
			}
			if current.State != "ready" {
				return fmt.Errorf("preparation %s", current.State)
			}
			if !current.ExpiresAt.After(time.Now()) {
				_, _ = tr.transitionPreparation(current.ID, "ready", "expired", current.Revision, current.OwnerFence, nil)
				return errors.New("preparation expired")
			}
		}
	}
}

func (tr *TestRunner) consumePreparation(id string, opts StartTestOptions) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	store, ok := tr.testStore.(storage.PreparationStore)
	if !ok {
		return errors.New("preparation storage unavailable")
	}
	p, err := store.GetPreparation(opts.PreparationID)
	if err != nil {
		return err
	}
	if p.TestID != id || !preparationProjectEnabled(p.ProjectID) || p.State != "ready" || opts.ExpectedPreparationRevision == "" || opts.ExpectedPreparationRevision != p.ExecutionRevision {
		return storage.ErrPreparationConflict
	}
	if !tr.preparationReservationValid(id, p) {
		return errors.New("preparation lost its execution reservation or controller ownership")
	}
	if err = tr.validateStartSecretRequirements(id); err != nil {
		return err
	}
	if err = tr.validatePreparedMonitorReadiness(id); err != nil {
		return err
	}
	revision, err := tr.preparationRevision(id)
	if err != nil {
		return err
	}
	if revision != p.ExecutionRevision {
		_, _ = tr.transitionPreparation(p.ID, "ready", "failed", p.Revision, p.OwnerFence, nil)
		return errors.New("prepared execution revision changed")
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test := tr.tests[id]
	if test == nil || test.Status != "INITIALIZING" || test.preparationID != p.ID || test.executionFence != p.OwnerFence || test.executionCtx == nil || test.executionCtx.Err() != nil || !preparationAllocationMatches(test, p) {
		return storage.ErrPreparationConflict
	}
	snapshot, _ := applyTestStartedByActor(test, opts.actor)
	err = tr.trackDurableWrite(func() error {
		_, err := store.ConsumePreparation(p.ID, p.Revision, p.OwnerFence, test.StartedBySubject, test.StartedByDisplayName)
		return err
	})
	if err != nil {
		restoreTestStartedByActor(test, snapshot)
	}
	return err
}

func (tr *TestRunner) preparationReservationValid(id string, p *storage.Preparation) bool {
	if p == nil || !tr.isExecutionLeader() {
		return false
	}
	if tr.durableExecutionFence && tr.currentExecutionFenceToken() != p.OwnerFence {
		return false
	}
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil || test.Status != "INITIALIZING" || test.preparationID != p.ID || test.executionFence != p.OwnerFence || test.executionCtx == nil || test.executionCtx.Err() != nil || !preparationAllocationMatches(test, p) {
		tr.mu.RUnlock()
		return false
	}
	check, ctx := test.preparationPeerCheck, test.executionCtx
	_, distributed := tr.resolveBackend(test).(*distributedExecutionBackend)
	tr.mu.RUnlock()
	if distributed && check == nil {
		return false
	}
	if check != nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := check(ctx); err != nil || ctx.Err() != nil {
			return false
		}
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return ctx.Err() == nil && tr.tests[id] == test && test.Status == "INITIALIZING" && test.preparationID == p.ID && test.executionFence == p.OwnerFence && preparationAllocationMatches(test, p)
}

// Preparing has not yet published allocation; ready reservations must exactly
// match the immutable shard snapshot captured after distributed staging.
func preparationAllocationMatches(test *ManagedTest, p *storage.Preparation) bool {
	if p.State == "preparing" {
		return true
	}
	allocation, err := json.Marshal(test.GeneratorShards)
	return err == nil && bytes.Equal(allocation, p.Allocation)
}

func (tr *TestRunner) validatePreparedMonitorReadiness(id string) error {
	if err := tr.validateRunMonitorCorrelation(id); err != nil {
		return err
	}
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return errors.New("test not found")
	}
	record := &storage.TestRecord{ID: id, ProjectID: test.ProjectID, ScenarioID: test.ScenarioID, MonitorBindings: cloneMonitorBindings(test.MonitorBindings)}
	goals := cloneGoalConfig(test.GoalConfig)
	readinessContext := test.executionCtx
	tr.mu.RUnlock()
	if readinessContext == nil {
		readinessContext = context.Background()
	}
	bindings, err := tr.resolvedTestMonitorBindings(record)
	if err != nil {
		return err
	}
	if !runMonitoringEnabled(record.ProjectID) {
		for _, binding := range bindings.Sources {
			if binding.Required {
				return errors.New("required run monitoring is not enabled for this project")
			}
		}
		if goals != nil && len(goals.MonitorRules) > 0 {
			return errors.New("monitoring rules require run monitoring to be enabled for this project")
		}
		return nil
	}
	if err := tr.validateMonitorStorageAndSigning(record); err != nil {
		return err
	}
	monitorStore, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return errors.New("monitor readiness storage unavailable")
	}
	sources, err := monitorStore.ListMonitorSources(record.ProjectID)
	if err != nil {
		return err
	}
	if err := tr.validateSelectedMonitorRules(bindings, sources, goals); err != nil {
		return err
	}
	if err := tr.validatePrometheusCollectionBudgets(bindings, sources, goals); err != nil {
		return err
	}
	if err := tr.validateAppDynamicsCollectionBudgets(bindings, sources); err != nil {
		return err
	}
	if err := tr.validateDynatraceReadiness(readinessContext, record.ProjectID, bindings, sources); err != nil {
		return err
	}
	for _, binding := range bindings.Sources {
		if !binding.Required {
			continue
		}
		for _, source := range sources {
			if source.ID != binding.SourceID {
				continue
			}
			if source.Pack == monitorprovider.PackAppDynamics {
				connections, ok := tr.testStore.(storage.AppDynamicsConnectionStore)
				if !ok {
					return errors.New("AppDynamics connection storage unavailable")
				}
				c, err := connections.GetAppDynamicsConnection(source.ConnectionID)
				if err != nil || c.ProjectID != record.ProjectID || c.ConfigurationStatus() != "ready" {
					return errors.New("required AppDynamics connection is not verified")
				}
				if err := storage.ValidateAppDynamicsSourceConnection(source, c.Snapshot()); err != nil {
					return err
				}
				if source.ExecutorMode != "direct" {
					inspections, ok := tr.testStore.(storage.AppDynamicsInspectionStore)
					if !ok {
						return errors.New("AppDynamics collector enrollment storage unavailable")
					}
					key, err := inspections.AppDynamicsCollectorPublicKey(record.ProjectID, source.CollectorID, source.Destination, time.Now().UTC())
					if err != nil || key != source.CollectorPublicKey {
						return errors.New("AppDynamics source collector enrollment changed; update the source before preparing")
					}
				}
				if source.AppDynamics == nil || len(source.AppDynamics.Metrics) > 0 && !c.Capabilities.Metrics || source.AppDynamics.Events && !c.Capabilities.Events || source.AppDynamics.Violations && !c.Capabilities.Violations || source.AppDynamics.Snapshots && !c.Capabilities.Snapshots || len(source.AppDynamics.Analytics) > 0 && !c.Capabilities.Analytics {
					return errors.New("required AppDynamics capability has not been verified")
				}
				if _, err := resolveAppDynamicsCredentials(readinessContext, tr.secretResolverForProject(record.ProjectID), c.Snapshot()); err != nil {
					return err
				}
				if tr.appDynamicsPrepare != nil {
					ctx, cancel := context.WithTimeout(readinessContext, time.Minute)
					err := tr.appDynamicsPrepare(ctx, source, c.Snapshot())
					cancel()
					if err != nil {
						return err
					}
				}
			} else if source.Pack == monitorprovider.PackPrometheusQuery {
				if err := tr.validatePreparedPrometheus(readinessContext, source, record.ProjectID, goals); err != nil {
					return err
				}
			} else if source.CredentialRef != "" {
				single := &fixedMonitorBindingsStore{RunMonitorStore: monitorStore, bindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{binding}}}
				if err := validateRequiredMonitorCredentials(context.Background(), single, tr.projectSecretStore, record.ProjectID, "selected"); err != nil {
					return err
				}
			}
		}
	}
	store, ok := tr.testStore.(interface {
		ValidateRunMonitorReadiness(string, string, string, time.Time) error
	})
	if !ok {
		return errors.New("monitor readiness storage unavailable")
	}
	return tr.trackDurableWrite(func() error {
		return store.ValidateRunMonitorReadiness(record.ProjectID, id, record.ScenarioID, time.Now().UTC())
	})
}

type fixedMonitorBindingsStore struct {
	storage.RunMonitorStore
	bindings *storage.MonitorBindings
}

func (s *fixedMonitorBindingsStore) GetMonitorBindings(string, string) (*storage.MonitorBindings, error) {
	return s.bindings, nil
}

func (tr *TestRunner) cancelPreparationExecution(id, preparation string) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if test := tr.tests[id]; test != nil && test.preparationID == preparation && test.Status == "INITIALIZING" && test.cancel != nil {
		test.cancel()
	}
}

// Cancellation/expiry also interrupts provider provisioning, before the ready
// gate has been reached. A successfully consumed preparation is never cancelled.
func (tr *TestRunner) watchPreparation(preparation, id string) {
	store, ok := tr.testStore.(storage.PreparationStore)
	if !ok {
		return
	}
	tr.mu.RLock()
	test := tr.tests[id]
	var ctx context.Context
	if test != nil {
		ctx = test.executionCtx
	}
	tr.mu.RUnlock()
	if ctx == nil {
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var nextReadinessCheck time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p, err := store.GetPreparation(preparation)
			if err != nil {
				continue
			}
			if p.State == "consumed" {
				return
			}
			if p.State == "cancelled" || p.State == "failed" || p.State == "expired" {
				tr.cancelPreparationExecution(id, preparation)
				return
			}
			if !tr.isExecutionLeader() || (tr.durableExecutionFence && tr.currentExecutionFenceToken() != p.OwnerFence) {
				tr.cancelPreparationExecution(id, preparation)
				return
			}
			if !p.ExpiresAt.After(time.Now()) {
				if _, err = tr.transitionPreparation(p.ID, p.State, "expired", p.Revision, p.OwnerFence, nil); err == nil {
					tr.cancelPreparationExecution(id, preparation)
					return
				}
			}
			if p.State == "ready" && time.Now().After(nextReadinessCheck) {
				nextReadinessCheck = time.Now().Add(time.Second)
				current, revisionErr := tr.preparationRevision(id)
				if revisionErr != nil || current != p.ExecutionRevision || !tr.preparationReservationValid(id, p) {
					if _, err = tr.transitionPreparation(p.ID, "ready", "failed", p.Revision, p.OwnerFence, nil); err == nil {
						tr.cancelPreparationExecution(id, preparation)
						return
					}
				}
			}
		}
	}
}

func (tr *TestRunner) transitionPreparation(id, from, to string, revision int64, owner string, allocation json.RawMessage) (*storage.Preparation, error) {
	store, ok := tr.testStore.(storage.PreparationStore)
	if !ok {
		return nil, errors.New("preparation storage unavailable")
	}
	var next *storage.Preparation
	err := tr.trackDurableWrite(func() error {
		var err error
		next, err = store.TransitionPreparation(id, from, to, revision, owner, allocation)
		return err
	})
	return next, err
}
