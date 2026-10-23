package api

// Scenario persistence and scheduling: CRUD for stored scenarios plus the
// schedule/occurrence bookkeeping (save, audit, list, health) that drives the
// run calendar. Extracted from runner.go; all methods hang off *TestRunner and
// follow its locking contract ("Locked" suffix = caller holds tr.mu).

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/contentmutation"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scenarioScheduleCASPersistence interface {
	CreateScenarioScheduleIfAbsent(schedule *storage.ScenarioSchedule) (bool, error)
	UpdateScenarioScheduleIfRevision(schedule *storage.ScenarioSchedule, expectedRevision int) (bool, error)
}

type conditionalScenarioPersistence interface {
	CreateScenarioWithDataPoolBindingsIfAbsent(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) (bool, error)
	UpdateScenarioWithDataPoolBindingsIfUnchanged(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding, time.Time) (bool, error)
}

type conditionalScenarioDeletePersistence interface {
	DeleteScenarioIfUnchanged(string, time.Time) (bool, error)
}

func updateScenarioAggregateIfUnchanged(store conditionalScenarioPersistence, scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding, expected time.Time) error {
	updated, err := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario, scripts, bindings, expected)
	if err != nil {
		return err
	}
	if !updated {
		return errScenarioConcurrentChange
	}
	return nil
}

func createScenarioAggregateIfAbsent(store conditionalScenarioPersistence, scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
	created, err := store.CreateScenarioWithDataPoolBindingsIfAbsent(scenario, scripts, bindings)
	if err != nil {
		return err
	}
	if !created {
		return errScenarioConcurrentChange
	}
	return nil
}

var errScenarioConcurrentChange = fmt.Errorf("scenario changed concurrently after conflict preflight")
var errScheduleConcurrentChange = errors.New("schedule changed concurrently")
var errScheduleHasActiveOccurrence = errors.New("schedule has an active materializing occurrence")

type executionFencedScenarioSchedulePersistence interface {
	CreateScenarioScheduleIfAbsentAndLeaseHeld(schedule *storage.ScenarioSchedule, leaseName, leaseOwnerID string) (bool, error)
	UpdateScenarioScheduleIfRevisionAndLeaseHeld(schedule *storage.ScenarioSchedule, expectedRevision int, leaseName, leaseOwnerID string) (bool, error)
	SaveScenarioScheduleOccurrenceIfLeaseHeld(occurrence *storage.ScenarioScheduleOccurrence, leaseName, leaseOwnerID string) (bool, error)
}

type leaseTermFencedScenarioSchedulePersistence interface {
	CreateScenarioScheduleIfAbsentAndLeaseTermHeld(schedule *storage.ScenarioSchedule, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	UpdateScenarioScheduleIfRevisionAndLeaseTermHeld(schedule *storage.ScenarioSchedule, expectedRevision int, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	CreateScenarioScheduleOccurrenceIfAbsentAndLeaseTermHeld(occurrence *storage.ScenarioScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	CompareAndSwapScenarioScheduleOccurrenceIfLeaseTermHeld(occurrence *storage.ScenarioScheduleOccurrence, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

type guardedScenarioScheduleDeletePersistence interface {
	DeleteScenarioScheduleGuarded(id string) (storage.ScheduleDeleteResult, error)
	DeleteScenarioScheduleOccurrenceGuarded(id string) (storage.ScheduleOccurrenceDeleteResult, error)
}

// revisionGuardedScenarioScheduleDeletePersistence is used only for
// compensation after a non-atomic schedule+approval write. An ordinary
// guarded delete protects occurrence anchors, but it is not sufficient here:
// another request may have advanced the schedule while the approval write was
// in flight. The revision predicate prevents compensation from deleting that
// newer durable state.
type revisionGuardedScenarioScheduleDeletePersistence interface {
	DeleteScenarioScheduleGuardedIfRevision(id string, expectedRevision int) (storage.ScheduleDeleteResult, error)
}

type leaseTermFencedScenarioScheduleDeletePersistence interface {
	DeleteScenarioScheduleGuardedIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (storage.ScheduleDeleteResult, error)
}

type scenarioScheduleOccurrenceCASPersistence interface {
	CreateScenarioScheduleOccurrenceIfAbsent(occurrence *storage.ScenarioScheduleOccurrence) (bool, error)
	CompareAndSwapScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence, expectedRevision int64) (bool, error)
}

type scenarioScheduleOccurrenceClaimPersistence interface {
	ClaimScenarioScheduleOccurrenceIfCurrent(occurrence *storage.ScenarioScheduleOccurrence) (bool, error)
}

type leaseTermFencedScenarioScheduleOccurrenceClaimPersistence interface {
	ClaimScenarioScheduleOccurrenceIfCurrentAndLeaseTermHeld(occurrence *storage.ScenarioScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

func (tr *TestRunner) StoreScenario(req CreateScenarioRequest) (*StoredScenario, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	stored, err := tr.storeScenarioLocked(uuid.New().String(), req)
	return cloneStoredScenarioForReadLocked(stored), err
}

func (tr *TestRunner) StoreScenarioWithID(id string, req CreateScenarioRequest) (*StoredScenario, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("scenario id is required")
	}
	stored, err := tr.storeScenarioLocked(id, req)
	return cloneStoredScenarioForReadLocked(stored), err
}

func (tr *TestRunner) UpdateScenario(id string, req CreateScenarioRequest) (*StoredScenario, bool, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	current, ok := tr.scenarios[id]
	if !ok {
		return nil, false, nil
	}
	var scenario *StoredScenario
	var err error
	if store, supported := tr.scenarioStore.(conditionalScenarioPersistence); supported {
		expectedUpdatedAt := current.UpdatedAt
		scenario, err = tr.storeScenarioLockedWithWriter(id, req, func(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
			return updateScenarioAggregateIfUnchanged(store, scenario, scripts, bindings, expectedUpdatedAt)
		})
	} else {
		// Lightweight adapters retain the legacy writer contract. Production
		// SQLite/PostgreSQL stores implement the aggregate CAS above.
		scenario, err = tr.storeScenarioLocked(id, req)
	}
	return cloneStoredScenarioForReadLocked(scenario), true, err
}

func (tr *TestRunner) storeScenarioLocked(id string, req CreateScenarioRequest) (*StoredScenario, error) {
	return tr.storeScenarioLockedWithWriter(id, req, nil)
}

// StoreScenarioWithIDCreateOnly materializes a deterministic imported scenario
// without permitting a concurrent replica or client to be overwritten.
func (tr *TestRunner) StoreScenarioWithIDCreateOnly(id string, req CreateScenarioRequest) (*StoredScenario, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("scenario id is required")
	}
	return tr.storeScenarioLockedWithWriter(id, req, tr.createOnlyScenarioWriterLocked(id))
}

// createOnlyScenarioWriterLocked persists a new scenario and refuses an
// existing ID instead of overwriting it.
func (tr *TestRunner) createOnlyScenarioWriterLocked(id string) func(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) error {
	return func(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
		if store, ok := tr.scenarioStore.(conditionalScenarioPersistence); ok {
			return createScenarioAggregateIfAbsent(store, scenario, scripts, bindings)
		}
		if tr.scenarioStore != nil {
			return fmt.Errorf("durable scenario store does not support atomic create-only persistence")
		}
		if tr.scenarios[id] != nil {
			return errScenarioConcurrentChange
		}
		return nil
	}
}

// UpdateScenarioIfUnchanged applies a library synchronization update only when
// the durable scenario version observed during conflict detection is current.
func (tr *TestRunner) UpdateScenarioIfUnchanged(id string, req CreateScenarioRequest, expectedUpdatedAt time.Time) (*StoredScenario, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" || expectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf("scenario id and expected updated_at are required")
	}
	return tr.storeScenarioLockedWithWriter(id, req, func(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
		if store, ok := tr.scenarioStore.(conditionalScenarioPersistence); ok {
			return updateScenarioAggregateIfUnchanged(store, scenario, scripts, bindings, expectedUpdatedAt)
		}
		if tr.scenarioStore != nil {
			return fmt.Errorf("durable scenario store does not support conditional updates")
		}
		current := tr.scenarios[id]
		if current == nil || !current.UpdatedAt.Equal(expectedUpdatedAt) {
			return errScenarioConcurrentChange
		}
		return nil
	})
}

func (tr *TestRunner) DeleteScenarioIfUnchanged(id string, expectedUpdatedAt time.Time) (bool, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(tr.scenarioProjectIDForContentMutation(id))
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" || expectedUpdatedAt.IsZero() {
		return false, fmt.Errorf("scenario id and expected updated_at are required")
	}
	if store, ok := tr.scenarioStore.(conditionalScenarioDeletePersistence); ok {
		deleted, err := contentmutation.OutsideLock(&tr.mu, func() (bool, error) { return store.DeleteScenarioIfUnchanged(id, expectedUpdatedAt) })
		if err != nil || !deleted {
			return deleted, err
		}
	} else if tr.scenarioStore != nil {
		return false, fmt.Errorf("durable scenario store does not support conditional delete")
	} else {
		current := tr.scenarios[id]
		if current == nil || !current.UpdatedAt.Equal(expectedUpdatedAt) || len(current.Schedules) != 0 || current.ResultCount != 0 {
			return false, nil
		}
	}
	if current := tr.scenarios[id]; current != nil {
		tr.removeRefKeyLocked(current.RefKey)
		tr.removeScenarioReadinessLocked(current)
	}
	delete(tr.scenarios, id)
	tr.contentRevision++
	return true, nil
}

func (tr *TestRunner) storeScenarioLockedWithWriter(id string, req CreateScenarioRequest, writer func(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) error) (*StoredScenario, error) {
	return tr.storeScenarioLockedWithSources(id, req, nil, writer)
}

// scenarioSourceBinder replaces the private build snapshot's cached group
// sources with durable ones before compilation, so validation runs against
// exactly the source revisions the final write claims. It runs with tr.mu
// released and must touch only the snapshot it is given.
type scenarioSourceBinder func(snapshot *TestRunner, groups []CreateGroupEntry) error

func (tr *TestRunner) storeScenarioLockedWithSources(id string, req CreateScenarioRequest, bind scenarioSourceBinder, writer func(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) error) (*StoredScenario, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("scenario name is required")
	}
	if req.TerminationMode != dsl.TerminationIterations && strings.TrimSpace(req.Duration) == "" {
		return nil, fmt.Errorf("scenario duration is required")
	}
	// Reconcile masked runtime secrets against the prior stored scenario before any
	// downstream use (compile, review, persist), so an echoed [REDACTED] never
	// clobbers a stored Env secret and never reaches the engine. Keyed by stable
	// group identity, not array index (reorder-safe).
	existing := tr.scenarios[id]
	if existing != nil {
		if err := tr.projectDeletionErrorLocked(existing.ProjectID); err != nil {
			return nil, err
		}
	}
	req.RuntimeSettings = reconcileGroupRuntimeSecrets(req.RuntimeSettings, existingRootRuntime(existing))
	for i := range req.Groups {
		req.Groups[i].RuntimeSettings = reconcileGroupRuntimeSecrets(req.Groups[i].RuntimeSettings, existingGroupRuntime(existing, req.Groups[i]))
	}
	protectedRoot, rootValues, _, err := protectLegacyRuntimeOverrideSecrets(id, -1, nil, req.RuntimeSettings)
	if err != nil {
		return nil, fmt.Errorf("protect scenario runtime credentials: %w", err)
	}
	runtimeSecretValues := cloneRuntimeSecretValues(rootValues)
	clearRuntimeValues := func(values map[string]string) {
		for name := range values {
			values[name] = ""
			delete(values, name)
		}
	}
	clearRuntimeValues(rootValues)
	defer clearRuntimeValues(runtimeSecretValues)
	req.RuntimeSettings = protectedRoot
	for i := range req.Groups {
		protected, values, _, protectErr := protectLegacyRuntimeOverrideSecrets(id, i, nil, req.Groups[i].RuntimeSettings)
		if protectErr != nil {
			clearRuntimeValues(values)
			return nil, fmt.Errorf("protect scenario group[%d] runtime credentials: %w", i, protectErr)
		}
		if runtimeSecretValues == nil && len(values) > 0 {
			runtimeSecretValues = make(map[string]string, len(values))
		}
		for name, value := range values {
			if prior, exists := runtimeSecretValues[name]; exists && prior != value {
				clearRuntimeValues(values)
				return nil, fmt.Errorf("runtime credential projection conflict for %s", name)
			}
			runtimeSecretValues[name] = value
		}
		clearRuntimeValues(values)
		req.Groups[i].RuntimeSettings = protected
	}
	if _, err := validateScenarioRequest(CreateTestRequest{
		TerminationMode: req.TerminationMode,
		ScenarioMode:    req.ScenarioMode,
		Duration:        req.Duration,
		StartDelay:      req.StartDelay,
		Assignment:      req.Assignment,
		GoalConfig:      req.GoalConfig,
		Groups:          req.Groups,
	}); err != nil {
		return nil, err
	}
	groups, err := normalizeCreateTestGroups(CreateTestRequest{Groups: req.Groups})
	if err != nil {
		return nil, err
	}
	jointEvidence, err := normalizeJointEvidence(req.JointEvidence)
	if err != nil {
		return nil, err
	}
	if err := validateTrafficProfile(req.TrafficProfile, groups, req.Duration, req.ScenarioMode, req.GoalConfig); err != nil {
		return nil, err
	}
	effectiveShapeID := ""
	if req.WorkloadShapeID != nil {
		effectiveShapeID = strings.TrimSpace(*req.WorkloadShapeID)
	} else if existing != nil {
		effectiveShapeID = existing.WorkloadShapeID
	}
	if err := validateTrafficProfileForWorkloadShape(effectiveShapeID, req.TrafficProfile, groups); err != nil {
		return nil, err
	}
	for index := range groups {
		groups[index].runtimeSecretScope = id
		if stored := tr.scripts[tr.resolveRefKeyLocked(groups[index].ScriptID)]; stored != nil && stored.Script != nil && stored.Script.Config.JMeter != nil && stored.Script.Config.JMeter.Profile == dsl.JMeterMessagingProfile {
			groups[index].preserveJMeterDraft = true
		}
		groups[index].iterationCompletion = req.TerminationMode == dsl.TerminationIterations
	}
	buildRequest := CreateTestRequest{
		Groups:          groups,
		RuntimeSettings: req.RuntimeSettings,
		TerminationMode: req.TerminationMode,
		ScenarioMode:    normalizeScenarioMode(req.ScenarioMode),
	}
	buildSnapshot, preparedBuildRequest, buildFence := tr.managedTestBuildSnapshotLocked(buildRequest)
	tr.mu.Unlock()
	var testGroups []TestGroup
	var engineGroups []engine.GroupConfig
	if bind != nil {
		err = bind(buildSnapshot, preparedBuildRequest.Groups)
	}
	if err == nil {
		testGroups, engineGroups, _, err = buildSnapshot.compileManagedTestGroupsForModeWithDataSourcesLocked(
			preparedBuildRequest.Groups,
			preparedBuildRequest.RuntimeSettings,
			nil,
			preparedBuildRequest.ScenarioMode,
		)
	}
	tr.mu.Lock()
	if err != nil {
		return nil, err
	}
	if err := validateTrafficProfileCompiledGroups(req.TrafficProfile, testGroups); err != nil {
		return nil, err
	}
	if err := validateJointEvidenceCategories(jointEvidence, testGroups); err != nil {
		return nil, err
	}
	scenarioDuration, err := parseTerminationDuration(req.Duration, req.TerminationMode)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}
	if err := validateIterationEngineGroups(req.TerminationMode, engineGroups); err != nil {
		return nil, err
	}
	for index, group := range engineGroups {
		if req.TerminationMode != dsl.TerminationIterations && group.StartOffset > scenarioDuration {
			return nil, fmt.Errorf("group[%d]: start_offset %s begins after the %s scenario deadline", index, group.StartOffset, scenarioDuration)
		}
	}
	if tr.contentRevision != buildFence.contentRevision {
		return nil, errScenarioConcurrentChange
	}
	if runtimeSettingsReview := buildRuntimeSettingsScenarioReview(testGroups, engineGroups); runtimeSettingsReview != nil && runtimeSettingsReview.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", runtimeSettingsReview.Summary)
	}
	// Scenarios are stored at microseconds, and the cached copy's CreatedAt
	// must equal the stored one.
	now := time.Now().UTC().Truncate(time.Microsecond)
	createdAt := now
	refKey := ""
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	if existing != nil {
		refKey = existing.RefKey
	}
	workloadShapeID := ""
	if req.WorkloadShapeID != nil {
		workloadShapeID = strings.TrimSpace(*req.WorkloadShapeID)
	} else if existing != nil {
		workloadShapeID = existing.WorkloadShapeID
	}
	if refKey == "" {
		tr.mu.Unlock()
		sequence, err := tr.nextScenarioSeqOutsideRunnerLock()
		tr.mu.Lock()
		if err != nil {
			return nil, fmt.Errorf("allocate scenario reference key: %w", err)
		}
		refKey = refkey.Scenario(tr.envCode, sequence)
	}
	scenario := &storage.Scenario{
		ID:               id,
		RefKey:           refKey,
		Name:             req.Name,
		WorkloadShapeID:  workloadShapeID,
		Duration:         req.Duration,
		StartDelay:       req.StartDelay,
		TerminationMode:  req.TerminationMode,
		ScenarioMode:     normalizeScenarioMode(req.ScenarioMode),
		ScriptShakeout:   req.ScriptShakeout,
		ResourceBaseline: req.ResourceBaseline,
		Assignment:       generatorAssignmentForStorage(req.Assignment),
		GoalConfig:       goalConfigForStorage(req.GoalConfig),
		TrafficProfile:   trafficProfileForStorage(req.TrafficProfile),
		RuntimeSettings:  runtimeSettingsForStorage(req.RuntimeSettings),
		JointEvidence:    jointEvidenceForStorage(jointEvidence),
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	scripts := make([]storage.ScenarioScript, 0, len(groups))
	for i, group := range groups {
		// compileManagedTestGroupsLocked validated and normalized the role
		// (e.g. the "canary" alias), so persist the normalized value.
		scripts = append(scripts, storage.ScenarioScript{
			ScenarioID:                   id,
			Order:                        i,
			Name:                         group.Name,
			ScriptID:                     group.ScriptID,
			Role:                         testGroups[i].Role,
			VUsers:                       group.VUsers,
			RampUp:                       group.RampUp,
			RampDown:                     group.RampDown,
			StartOffset:                  group.StartOffset,
			Stages:                       cloneLoadStages(group.Stages),
			Iterations:                   group.Iterations,
			Initialization:               group.InitStrategy,
			RuntimeSettings:              runtimeSettingsForStorage(group.RuntimeSettings),
			Assignment:                   generatorAssignmentForStorage(group.Assignment),
			TransactionResponseTimeSLAMs: cloneFloat64Ptr(group.TransactionResponseTimeSLAMs),
		})
	}
	projectID, singleProject := tr.scenarioProjectIDFromScriptsLocked(scripts)
	if !singleProject {
		return nil, fmt.Errorf("scenario scripts span multiple projects")
	}
	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return nil, err
	}
	dataPools, err := normalizeScenarioDataPoolBindings(id, req.DataPools)
	if err != nil {
		return nil, err
	}
	poolStore := tr.dataPoolStore
	tr.mu.Unlock()
	poolProjectErr := validateDataPoolProjects(poolStore, projectID, dataPools)
	poolArchiveErr := validateDataPoolsNotArchived(poolStore, dataPools)
	var livePolicyErr error
	if hasLiveDataPools(dataPools) {
		livePolicyErr = tr.validateLiveTablePolicy(projectID)
	}
	// The scenario's chosen telemetry acceptance policy must be one of its
	// own project's live, valid policies when it is saved (TEL-B); each run
	// created from it is checked again at creation and pinned at Start.
	// A save that keeps the scenario's current choice (an unrelated edit, a
	// library import update, a rollback) is not judged again: a choice
	// deleted since is refused by every run that inherits it, and restoring a
	// scenario must not depend on a policy read.
	telemetryPolicyID, telemetryPolicyErr := normalizeTelemetryPolicyChoice(req.TelemetryAcceptancePolicyID), error(nil)
	if existing == nil || telemetryPolicyID != existing.TelemetryAcceptancePolicyID || normalizeProjectID(existing.ProjectID) != normalizeProjectID(projectID) {
		telemetryPolicyID, telemetryPolicyErr = tr.validateRunTelemetryAcceptancePolicy(projectID, req.TelemetryAcceptancePolicyID)
	}
	// The chosen telemetry budget policy is judged the same way, and only
	// when the save changes it or the scenario's project.
	telemetryBudgetPolicyID, telemetryBudgetPolicyErr := normalizeTelemetryPolicyChoice(req.TelemetryBudgetPolicyID), error(nil)
	if existing == nil || telemetryBudgetPolicyID != existing.TelemetryBudgetPolicyID || normalizeProjectID(existing.ProjectID) != normalizeProjectID(projectID) {
		telemetryBudgetPolicyID, telemetryBudgetPolicyErr = tr.validateRunTelemetryBudgetPolicy(projectID, req.TelemetryBudgetPolicyID)
	}
	tr.mu.Lock()
	if poolProjectErr != nil {
		return nil, poolProjectErr
	}
	if telemetryPolicyErr != nil {
		return nil, telemetryPolicyErr
	}
	if telemetryBudgetPolicyErr != nil {
		return nil, telemetryBudgetPolicyErr
	}
	scenario.TelemetryAcceptancePolicyID = telemetryPolicyID
	scenario.TelemetryBudgetPolicyID = telemetryBudgetPolicyID
	if poolArchiveErr != nil {
		return nil, poolArchiveErr
	}
	if livePolicyErr != nil {
		return nil, livePolicyErr
	}
	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return nil, err
	}
	tr.mu.Unlock()
	releaseProjectMutation := tr.acquireProjectTestMutation(projectID)
	tr.mu.Lock()
	defer releaseProjectMutation()
	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return nil, err
	}
	actor := req.creationActor
	if strings.TrimSpace(actor.Type) == "" && strings.TrimSpace(actor.Subject) == "" {
		actor = systemTestCreationActor("controller:scenario-store")
	}
	credentialGroups := []TestGroup(nil)
	if len(runtimeSecretValues) > 0 {
		credentialGroups = []TestGroup{{runtimeSecretValues: runtimeSecretValues}}
	}
	commitFingerprint := scenarioRuntimeCommitFingerprint(scenario, scripts)
	scenarioStore := tr.scenarioStore
	tr.mu.Unlock()
	runtimeSecretAttempt, persistErr := tr.persistRuntimeOverrideSecretValuesForResource(
		projectID, id, commitFingerprint, refKey, credentialGroups, actor,
		runtimeSecretProvisioningOptions{ResourceType: "scenario", CommitFingerprint: commitFingerprint},
	)
	if persistErr == nil {
		rollbackProvisioning := func(commitErr error) error {
			if errors.Is(commitErr, storage.ErrScenarioAuthoringOutcomeUnknown) {
				// The scenario may have committed. Its commit fingerprint is the
				// evidence recovery uses to keep or roll back the credentials, so
				// the attempt is left to recovery instead of being undone here.
				if runtimeSecretAttempt.ID != "" {
					tr.scheduleRuntimeOverrideSecretAttempt(runtimeSecretAttempt.ID, runtimeSecretAttempt.LeaseExpiresAt)
				}
				return commitErr
			}
			rollbackErr := tr.rollbackRuntimeOverrideSecretAttempt(projectID, id, runtimeSecretAttempt, "scenario_persistence_failed")
			return errors.Join(commitErr, rollbackErr)
		}
		if writer != nil {
			if commitErr := writer(scenario, scripts, dataPools); commitErr != nil {
				persistErr = rollbackProvisioning(commitErr)
			}
		} else if scenarioStore != nil {
			if atomicStore, ok := scenarioStore.(atomicScenarioPersistence); ok {
				if commitErr := atomicStore.SaveScenarioWithDataPoolBindings(scenario, scripts, dataPools); commitErr != nil {
					persistErr = rollbackProvisioning(commitErr)
				}
			} else if commitErr := scenarioStore.SaveScenario(scenario, scripts); commitErr != nil {
				persistErr = rollbackProvisioning(commitErr)
			} else if commitErr := scenarioStore.SaveScenarioDataPoolBindings(id, dataPools); commitErr != nil {
				persistErr = rollbackProvisioning(commitErr)
			}
		}
	}
	cleanupErr := error(nil)
	if persistErr == nil {
		cleanupErr = tr.completeRuntimeOverrideSecretAttempt(runtimeSecretAttempt)
	}
	tr.mu.Lock()
	if persistErr != nil {
		return nil, persistErr
	}
	if cleanupErr != nil {
		// The scenario fingerprint is durable commit evidence. Recovery retains
		// the governed values and removes only the value-free journal.
		slog.Warn("scenario runtime credential provisioning journal cleanup deferred", "scenario_id", id, "error", cleanupErr)
		tr.scheduleRuntimeOverrideSecretAttempt(runtimeSecretAttempt.ID, runtimeSecretAttempt.LeaseExpiresAt)
	}
	schedules := []*storage.ScenarioSchedule(nil)
	if current := tr.scenarios[id]; current != nil {
		for _, schedule := range current.Schedules {
			schedules = append(schedules, cloneScenarioSchedule(schedule))
		}
	}
	stored := tr.buildStoredScenarioFromPartsLocked(scenario, scripts, dataPools, schedules)
	tr.scenarios[id] = stored
	tr.contentRevision++
	tr.indexRefKeyLocked(stored.RefKey, stored.ID)
	return stored, nil
}

// scenarioRuntimeCommitFingerprint identifies the scenario content a runtime
// credential attempt committed with. Recovery recomputes it from storage,
// where settings decode into maps, so the value is canonicalized through
// generic JSON first: typed settings and their decoded form hash alike.
func scenarioRuntimeCommitFingerprint(scenario *storage.Scenario, scripts []storage.ScenarioScript) string {
	if scenario == nil {
		return ""
	}
	// PostgreSQL keeps a group's SLA in a 4-byte REAL, so recovery reads back
	// the nearest float32; both sides hash that value.
	canonicalScripts := slices.Clone(scripts)
	for i := range canonicalScripts {
		if sla := canonicalScripts[i].TransactionResponseTimeSLAMs; sla != nil {
			stored := float64(float32(*sla))
			canonicalScripts[i].TransactionResponseTimeSLAMs = &stored
		}
	}
	return hashCanonical(canonicalJSONValue(struct {
		ID               string
		RefKey           string
		Name             string
		WorkloadShapeID  string
		Duration         string
		StartDelay       string
		ScenarioMode     string
		TerminationMode  string
		ScriptShakeout   bool
		ResourceBaseline bool
		Assignment       any
		GoalConfig       any
		TrafficProfile   any
		RuntimeSettings  any
		JointEvidence    any    `json:",omitempty"`
		TelemetryPolicy  string `json:",omitempty"`
		TelemetryBudget  string `json:",omitempty"`
		Scripts          []storage.ScenarioScript
	}{
		ID:               scenario.ID,
		RefKey:           scenario.RefKey,
		Name:             scenario.Name,
		WorkloadShapeID:  scenario.WorkloadShapeID,
		Duration:         scenario.Duration,
		StartDelay:       scenario.StartDelay,
		TerminationMode:  scenario.TerminationMode,
		ScenarioMode:     scenario.ScenarioMode,
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		Assignment:       scenario.Assignment,
		GoalConfig:       scenario.GoalConfig,
		TrafficProfile:   scenario.TrafficProfile,
		RuntimeSettings:  scenario.RuntimeSettings,
		JointEvidence:    scenario.JointEvidence,
		TelemetryPolicy:  scenario.TelemetryAcceptancePolicyID,
		TelemetryBudget:  scenario.TelemetryBudgetPolicyID,
		Scripts:          canonicalScripts,
	}))
}

func normalizeScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) ([]storage.ScenarioDataPoolBinding, error) {
	out, err := normalizeDataPoolBindingIntent(bindings)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for index := range out {
		binding := &out[index]
		binding.ScenarioID = scenarioID
		binding.ID = strings.TrimSpace(binding.ID)
		if binding.ID == "" {
			binding.ID = uuid.New().String()
		}
		if binding.CreatedAt.IsZero() {
			binding.CreatedAt = now
		}
		binding.UpdatedAt = now
	}
	return out, nil
}

// normalizeDataPoolBindingIntent canonicalizes the executable portion of data
// pool bindings without rewriting persistence-only identity/audit metadata. It
// is shared by scenario authoring and test materialization so direct, scenario,
// revision, and execution-plan requests all observe one contract.
func normalizeDataPoolBindingIntent(bindings []storage.ScenarioDataPoolBinding) ([]storage.ScenarioDataPoolBinding, error) {
	return normalizeDataPoolBindingIntentWithLegacy(bindings, false)
}

// normalizeLegacyDataPoolBindingIntent preserves compatibility for trusted,
// already-persisted scenario/run/plan snapshots authored before access-mode
// enforcement. Those modes historically executed as read-only in-memory CSV
// snapshots, so canonicalizing them to read_only preserves actual behavior
// without pretending durable consume/write semantics exist.
func normalizeLegacyDataPoolBindingIntent(bindings []storage.ScenarioDataPoolBinding) ([]storage.ScenarioDataPoolBinding, error) {
	return normalizeDataPoolBindingIntentWithLegacy(bindings, true)
}

func normalizeDataPoolBindingIntentWithLegacy(bindings []storage.ScenarioDataPoolBinding, allowLegacyModes bool) ([]storage.ScenarioDataPoolBinding, error) {
	if bindings == nil {
		return nil, nil
	}
	out := make([]storage.ScenarioDataPoolBinding, len(bindings))
	seenAliases := make(map[string]struct{}, len(bindings))
	liveCount := 0
	for index, original := range bindings {
		binding := original
		binding.PoolID = strings.TrimSpace(binding.PoolID)
		binding.Scope = strings.TrimSpace(binding.Scope)
		binding.ProjectTableID = strings.TrimSpace(binding.ProjectTableID)
		if binding.Scope != "" && binding.Scope != "run" && binding.Scope != "project" {
			return nil, fmt.Errorf("unsupported data binding scope")
		}
		if binding.Scope != "project" && binding.ProjectTableID != "" {
			return nil, fmt.Errorf("project_table_id requires project scope")
		}
		if binding.Scope == "project" && binding.AccessMode != "live" {
			return nil, fmt.Errorf("project scope requires live access")
		}
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.AccessMode = strings.TrimSpace(binding.AccessMode)
		binding.SelectionStrategy = strings.TrimSpace(binding.SelectionStrategy)
		if binding.Alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		aliasKey := strings.ToLower(binding.Alias)
		if _, duplicate := seenAliases[aliasKey]; duplicate {
			return nil, fmt.Errorf("duplicate data pool binding alias %q", binding.Alias)
		}
		seenAliases[aliasKey] = struct{}{}

		if binding.AccessMode == "" {
			binding.AccessMode = "read_only"
		}
		switch binding.AccessMode {
		case "read_only":
		case "live":
			liveCount++
			if liveCount > maxLiveTablesPerRun {
				return nil, fmt.Errorf("at most %d live tables are supported per run", maxLiveTablesPerRun)
			}
			var err error
			binding, err = normalizeLiveTableBinding(binding)
			if err != nil {
				return nil, err
			}
			out[index] = binding
			continue
		case "read_write", "consume":
			if allowLegacyModes {
				binding.AccessMode = "read_only"
			} else {
				return nil, fmt.Errorf("unsupported data pool access mode %q for binding %q; use \"read_only\" or \"live\"", binding.AccessMode, binding.Alias)
			}
		default:
			return nil, fmt.Errorf("unsupported data pool access mode %q for binding %q; use \"read_only\" or \"live\"", binding.AccessMode, binding.Alias)
		}
		if binding.PoolID == "" {
			return nil, fmt.Errorf("data pool id is required")
		}
		if binding.LiveOptions != nil {
			return nil, fmt.Errorf("live_options require access_mode live")
		}
		if binding.SelectionStrategy == "" {
			binding.SelectionStrategy = "sequential"
		}
		switch binding.SelectionStrategy {
		case "sequential", "random", "unique", "pop":
		default:
			return nil, fmt.Errorf("unsupported data pool selection strategy %q", binding.SelectionStrategy)
		}
		out[index] = binding
	}
	return out, nil
}

func (tr *TestRunner) GetScenario(id string) (*StoredScenario, bool) {
	tr.refreshScenariosForHARead()
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	scenario, ok := tr.scenarios[id]
	if !ok {
		tr.mu.RUnlock()
		return nil, false
	}
	snapshot := tr.enrichScenarioSummaryLocked(scenario)
	tr.mu.RUnlock()
	return tr.publishedScenarioSnapshot(snapshot)
}

// getScenarioForMutation deliberately preserves the cached updated_at value.
// The aggregate CAS in UpdateScenario must observe and reject an HA write that
// raced this replica's snapshot; public read revalidation would otherwise turn
// that stale-but-real scenario into a misleading 404 before the CAS can return
// its 409. Pending direct-import assets remain hidden.
func (tr *TestRunner) getScenarioForMutation(id string) (*StoredScenario, bool) {
	if tr == nil {
		return nil, false
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	snapshot := cloneStoredScenarioForReadLocked(tr.scenarios[id])
	tr.mu.RUnlock()
	if snapshot == nil {
		return nil, false
	}
	if tr.importPublicationConfigured() && !tr.importAssetPublished("scenario", snapshot.ID) {
		return nil, false
	}
	return snapshot, true
}

func (tr *TestRunner) refreshScenariosForHARead(force ...bool) {
	if tr == nil || tr.leadership == nil {
		return
	}
	tr.haScenarioRefreshMu.Lock()
	defer tr.haScenarioRefreshMu.Unlock()
	forceRefresh := len(force) > 0 && force[0]
	if !forceRefresh && !tr.haScenarioRefreshAt.IsZero() && time.Since(tr.haScenarioRefreshAt) < 250*time.Millisecond {
		return
	}
	if err := tr.reloadStoredScripts(); err != nil {
		slog.Warn("failed to refresh scripts for HA scenario read", "error", err)
		return
	}
	if err := tr.reloadStoredScenarios(); err != nil {
		slog.Warn("failed to refresh scenarios for HA read", "error", err)
		return
	}
	tr.haScenarioRefreshAt = time.Now()
}

// refreshScenariosAfterDurableMismatch reloads the scenario cache when it is
// known to disagree with the durable rows: after a write whose commit
// acknowledgement was lost, or when a cached clock differs from the durable
// one. Unlike the HA read refresh it also runs on a single controller.
func (tr *TestRunner) refreshScenariosAfterDurableMismatch() {
	if tr == nil {
		return
	}
	if tr.leadership != nil {
		tr.refreshScenariosForHARead(true)
		return
	}
	if err := tr.reloadStoredScenarios(); err != nil {
		slog.Warn("failed to reload scenarios after a durable mismatch", "error", err)
	}
}

// refreshScenarioScheduleContentForHARead keeps a polled schedule read coherent
// across replicas without the unthrottled full-library reload a forced refresh
// performs on every poll: the scheduler content version probe short-circuits
// when no replica has touched the script/scenario/schedule tables. Stores
// without the probe keep the conservative forced refresh.
func (tr *TestRunner) refreshScenarioScheduleContentForHARead() {
	if tr == nil || tr.leadership == nil {
		return
	}
	if _, versioned := tr.scriptStore.(schedulerContentVersionPersistence); !versioned {
		tr.refreshScenariosForHARead(true)
		return
	}
	if err := tr.refreshSchedulerContentForHASweep(); err != nil {
		slog.Warn("failed to refresh scheduler content for HA schedule read", "error", err)
	}
}

func (tr *TestRunner) replaceScenarioDataPools(id string, bindings []storage.ScenarioDataPoolBinding, expectedUpdatedAt, updatedAt time.Time) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	scenario := tr.scenarios[id]
	if scenario == nil || !scenario.UpdatedAt.Equal(expectedUpdatedAt) {
		return false
	}
	scenario.DataPools = append([]storage.ScenarioDataPoolBinding(nil), bindings...)
	scenario.UpdatedAt = updatedAt
	tr.contentRevision++
	return true
}

func (tr *TestRunner) ListScenarios() []*StoredScenario {
	tr.refreshScenariosForHARead()
	tr.mu.RLock()
	selected := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		selected = append(selected, scenario)
	}
	selected = tr.enrichScenarioSummariesLocked(selected)
	tr.mu.RUnlock()
	return tr.filterPublishedScenarios(selected)
}

func (tr *TestRunner) ListScenariosFiltered(projectID string) []*StoredScenario {
	tr.refreshScenariosForHARead()
	projectID = normalizeProjectID(projectID)
	tr.mu.RLock()
	// Filter before enrichment: enrichment never changes ProjectID, so building
	// a full summary for scenarios that are about to be discarded is pure waste.
	selected := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		if scenario == nil || scenario.ProjectID != projectID {
			continue
		}
		selected = append(selected, scenario)
	}
	selected = tr.enrichScenarioSummariesLocked(selected)
	tr.mu.RUnlock()
	return tr.filterPublishedScenariosScoped(selected, projectID)
}

func (tr *TestRunner) DeleteScenario(id string) bool {
	deleted, _ := tr.DeleteScenarioDurable(id)
	return deleted
}

func (tr *TestRunner) DeleteScenarioDurable(id string, guard ...*storage.ScenarioWriterGuard) (bool, error) {
	tr.scenarioMutationMu.Lock()
	defer tr.scenarioMutationMu.Unlock()
	defer tr.flushContentReferenceFloors()
	releaseProjectMutation := tr.acquireProjectTestMutation(tr.scenarioProjectIDForContentMutation(id))
	defer releaseProjectMutation()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	scenario, ok := tr.scenarios[id]
	if !ok {
		return false, nil
	}
	for _, test := range tr.tests {
		if test.ScenarioID == id {
			return false, nil
		}
	}
	if tr.scenarioStore != nil {
		if guardedStore, canGuard := tr.scenarioStore.(storage.ScenarioGuardedWriterStore); canGuard && len(guard) > 0 && guard[0] != nil {
			if err := contentmutation.WriteOutsideLock(&tr.mu, func() error { return guardedStore.DeleteScenarioGuarded(id, *guard[0]) }); err != nil {
				return false, err
			}
		} else if err := contentmutation.WriteOutsideLock(&tr.mu, func() error { return tr.scenarioStore.DeleteScenario(id) }); err != nil {
			return false, err
		}
	}
	tr.removeRefKeyLocked(scenario.RefKey)
	tr.removeScenarioReadinessLocked(scenario)
	delete(tr.scenarios, id)
	tr.contentRevision++
	return true, nil
}

func (tr *TestRunner) saveScenarioScheduleLocked(schedule *storage.ScenarioSchedule) error {
	if schedule == nil {
		return fmt.Errorf("schedule is required")
	}
	fence, err := tr.captureScenarioScheduleMemoryFenceLocked(schedule.ScenarioID, schedule.ID)
	if err != nil {
		return err
	}
	store := tr.scenarioStore
	if store != nil {
		tr.mu.Unlock()
		changed := true
		if casStore, ok := store.(scenarioScheduleCASPersistence); ok {
			if fence.existed {
				changed, err = casStore.UpdateScenarioScheduleIfRevision(schedule, fence.expectedRevision)
			} else {
				changed, err = casStore.CreateScenarioScheduleIfAbsent(schedule)
			}
		} else {
			err = store.SaveScenarioSchedule(schedule)
		}
		tr.mu.Lock()
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("%w: scenario schedule %q changed or was deleted; reload and retry", errScheduleConcurrentChange, schedule.ID)
		}
	}
	return tr.installScenarioScheduleAfterPersistenceLocked(schedule, fence)
}

type scenarioScheduleMemoryFence struct {
	scenarioID       string
	scheduleID       string
	existed          bool
	expectedRevision int
}

// captureScenarioScheduleMemoryFenceLocked records only the mutable state that
// determines whether a schedule CAS result can still be installed. Persistence
// is allowed to run after tr.mu is released; the exact schedule identity and
// revision are revalidated before the in-memory projection is changed.
func (tr *TestRunner) captureScenarioScheduleMemoryFenceLocked(scenarioID, scheduleID string) (scenarioScheduleMemoryFence, error) {
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return scenarioScheduleMemoryFence{}, fmt.Errorf("scenario not found: %s", scenarioID)
	}
	fence := scenarioScheduleMemoryFence{scenarioID: scenarioID, scheduleID: scheduleID}
	for _, existing := range scenario.Schedules {
		if existing != nil && existing.ID == scheduleID {
			fence.existed = true
			fence.expectedRevision = existing.Revision
			break
		}
	}
	return fence, nil
}

func (tr *TestRunner) installScenarioScheduleAfterPersistenceLocked(schedule *storage.ScenarioSchedule, fence scenarioScheduleMemoryFence) error {
	if schedule == nil || schedule.ScenarioID != fence.scenarioID || schedule.ID != fence.scheduleID {
		return fmt.Errorf("%w: scenario schedule identity changed while persistence was in flight", errScheduleConcurrentChange)
	}
	scenario, ok := tr.scenarios[fence.scenarioID]
	if !ok {
		return fmt.Errorf("%w: scenario %q changed or was deleted while schedule persistence was in flight", errScheduleConcurrentChange, fence.scenarioID)
	}
	currentIndex := -1
	for index, current := range scenario.Schedules {
		if current == nil || current.ID != fence.scheduleID {
			continue
		}
		currentIndex = index
		if !fence.existed || current.Revision != fence.expectedRevision {
			return fmt.Errorf("%w: scenario schedule %q changed while persistence was in flight", errScheduleConcurrentChange, fence.scheduleID)
		}
		break
	}
	if fence.existed && currentIndex < 0 {
		return fmt.Errorf("%w: scenario schedule %q was deleted while persistence was in flight", errScheduleConcurrentChange, fence.scheduleID)
	}
	var previous *storage.ScenarioSchedule
	if currentIndex >= 0 {
		previous = scenario.Schedules[currentIndex]
		scenario.Schedules[currentIndex] = cloneScenarioSchedule(schedule)
	} else {
		scenario.Schedules = append(scenario.Schedules, cloneScenarioSchedule(schedule))
	}
	tr.contentRevision++
	tr.adjustSchedulerReadinessLocked(previous, schedule)
	return nil
}

func (tr *TestRunner) saveScenarioScheduleOccurrenceLocked(occurrence *storage.ScenarioScheduleOccurrence) error {
	if occurrence == nil {
		return fmt.Errorf("schedule occurrence is required")
	}
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	if tr.scenarioStore != nil {
		if existing, err := tr.scenarioStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(occurrence.ScheduleID, occurrence.PlannedAt); err != nil {
			return err
		} else if existing != nil && existing.ID != "" {
			occurrence.ID = existing.ID
			occurrence.CreatedAt = existing.CreatedAt
		}
		if err := tr.scenarioStore.SaveScenarioScheduleOccurrence(occurrence); err != nil {
			return err
		}
		tr.recordScenarioScheduleOccurrenceAuditLocked(occurrence)
		return nil
	}
	return nil
}

func (tr *TestRunner) recordScenarioScheduleOccurrenceAuditLocked(occurrence *storage.ScenarioScheduleOccurrence) {
	if tr == nil || tr.scenarioStore == nil || occurrence == nil {
		return
	}
	action, status, ok := scenarioScheduleOccurrenceAuditAction(occurrence.Status)
	if !ok {
		return
	}
	// saveScenarioScheduleOccurrenceLocked reaches this with tr.mu held; the
	// scheduler sweep reaches it with tr.mu released. Counting both is correct -
	// the gauge answers "is a durable write inside the driver", and the
	// lock-held caller is the one the backstop must not misread.
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	details := map[string]any{
		"schedule_id":       occurrence.ScheduleID,
		"scenario_id":       occurrence.ScenarioID,
		"schedule_revision": occurrence.ScheduleRevision,
		"planned_at":        occurrence.PlannedAt.UTC().Format(time.RFC3339),
		"occurrence_status": occurrence.Status,
		"reason_code":       occurrence.ReasonCode,
	}
	if occurrence.MaterializedAt != nil {
		details["materialized_at"] = occurrence.MaterializedAt.UTC().Format(time.RFC3339)
	}
	if occurrence.TestID != "" {
		details["test_id"] = occurrence.TestID
	}
	if occurrence.ApprovalID != "" {
		details["approval_id"] = occurrence.ApprovalID
	}
	if occurrence.ConflictSummary != "" {
		details["conflict_summary"] = occurrence.ConflictSummary
	}
	if err := tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           action,
		ActorType:        "system",
		ActorSubject:     "scenario-scheduler",
		ActorDisplayName: "Scenario Scheduler",
		ProjectID:        normalizeProjectID(occurrence.ProjectID),
		ResourceType:     "scenario_schedule_occurrence",
		ResourceID:       occurrence.ID,
		Status:           status,
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	}); err != nil {
		slog.Warn("failed to persist scenario-scheduler audit event", "occurrence_id", occurrence.ID, "action", action, "error", err)
	}
}

func scenarioScheduleOccurrenceAuditAction(occurrenceStatus string) (string, string, bool) {
	switch strings.TrimSpace(occurrenceStatus) {
	case "materialized", "queued", "completed", "approved":
		return "scenario.schedule.occurrence.materialized", "success", true
	case "blocked":
		return "scenario.schedule.occurrence.blocked", "blocked", true
	case "skipped":
		return "scenario.schedule.occurrence.skipped", "success", true
	case "failed":
		return "scenario.schedule.occurrence.failed", "failure", true
	default:
		return "", "", false
	}
}

func (tr *TestRunner) ListScenarioScheduleOccurrences(scenarioID string, scheduleID string) ([]*storage.ScenarioScheduleOccurrence, bool) {
	tr.refreshScenariosForHARead(true)
	tr.mu.RLock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		tr.mu.RUnlock()
		return nil, false
	}
	found := false
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			found = true
			break
		}
	}
	if !found {
		tr.mu.RUnlock()
		return nil, false
	}
	store := tr.scenarioStore
	tr.mu.RUnlock()
	if store == nil {
		return []*storage.ScenarioScheduleOccurrence{}, true
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(scheduleID)
	if err != nil {
		return []*storage.ScenarioScheduleOccurrence{}, true
	}
	out := make([]*storage.ScenarioScheduleOccurrence, 0, len(occurrences))
	for _, occurrence := range occurrences {
		out = append(out, cloneScenarioScheduleOccurrence(occurrence))
	}
	return out, true
}

// ListScenarioScheduleOccurrencesPage is the controller-facing bounded history
// read. Production storage applies LIMIT/OFFSET in SQL; compatibility stores
// are sliced after their legacy read.
func (tr *TestRunner) ListScenarioScheduleOccurrencesPage(scenarioID, scheduleID string, limit, offset int) ([]*storage.ScenarioScheduleOccurrence, bool, error) {
	tr.refreshScenariosForHARead(true)
	tr.mu.RLock()
	scenario, ok := tr.scenarios[scenarioID]
	found := false
	if ok {
		for _, schedule := range scenario.Schedules {
			if schedule != nil && schedule.ID == scheduleID {
				found = true
				break
			}
		}
	}
	store := tr.scenarioStore
	tr.mu.RUnlock()
	if !ok || !found {
		return nil, false, nil
	}
	if store == nil {
		return []*storage.ScenarioScheduleOccurrence{}, true, nil
	}
	var occurrences []*storage.ScenarioScheduleOccurrence
	var err error
	if paged, ok := store.(interface {
		ListScenarioScheduleOccurrencesPage(string, int, int) ([]*storage.ScenarioScheduleOccurrence, error)
	}); ok {
		occurrences, err = paged.ListScenarioScheduleOccurrencesPage(scheduleID, limit, offset)
	} else {
		occurrences, err = store.ListScenarioScheduleOccurrences(scheduleID)
		if err == nil {
			occurrences = pageScenarioScheduleOccurrences(occurrences, limit, offset)
		}
	}
	if err != nil {
		return nil, true, fmt.Errorf("list scenario schedule occurrences: %w", err)
	}
	out := make([]*storage.ScenarioScheduleOccurrence, 0, len(occurrences))
	for _, occurrence := range occurrences {
		out = append(out, cloneScenarioScheduleOccurrence(occurrence))
	}
	return out, true, nil
}

func pageScenarioScheduleOccurrences(items []*storage.ScenarioScheduleOccurrence, limit, offset int) []*storage.ScenarioScheduleOccurrence {
	if limit <= 0 || offset >= len(items) {
		return []*storage.ScenarioScheduleOccurrence{}
	}
	if offset < 0 {
		offset = 0
	}
	end := len(items)
	if limit < len(items)-offset {
		end = offset + limit
	}
	return items[offset:end]
}

// ListScenarioScheduleApprovals returns the approval history (pending + resolved)
// for a schedule, scoped to its scenario. ok=false when the scenario or schedule
// is unknown. Mirrors ListScenarioScheduleOccurrences. The approvals come straight
// from a store query (not shared in-memory state), so they are returned as-is.
func (tr *TestRunner) ListScenarioScheduleApprovals(scenarioID string, scheduleID string) ([]*storage.ScenarioScheduleApproval, bool) {
	tr.refreshScenariosForHARead(true)
	tr.mu.RLock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		tr.mu.RUnlock()
		return nil, false
	}
	found := false
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			found = true
			break
		}
	}
	if !found {
		tr.mu.RUnlock()
		return nil, false
	}
	store := tr.scenarioStore
	tr.mu.RUnlock()
	if store == nil {
		return []*storage.ScenarioScheduleApproval{}, true
	}
	approvals, err := store.ListScenarioScheduleApprovals(scheduleID)
	if err != nil {
		return []*storage.ScenarioScheduleApproval{}, true
	}
	if approvals == nil {
		approvals = []*storage.ScenarioScheduleApproval{}
	}
	return approvals, true
}

// ListScenarioScheduleApprovalsPage is the bounded API counterpart of the
// complete approval-history reader used by scheduler internals.
func (tr *TestRunner) ListScenarioScheduleApprovalsPage(scenarioID, scheduleID string, limit, offset int) ([]*storage.ScenarioScheduleApproval, bool, error) {
	tr.refreshScenariosForHARead(true)
	tr.mu.RLock()
	scenario, ok := tr.scenarios[scenarioID]
	found := false
	if ok {
		for _, schedule := range scenario.Schedules {
			if schedule != nil && schedule.ID == scheduleID {
				found = true
				break
			}
		}
	}
	store := tr.scenarioStore
	tr.mu.RUnlock()
	if !ok || !found {
		return nil, false, nil
	}
	if store == nil {
		return []*storage.ScenarioScheduleApproval{}, true, nil
	}
	var approvals []*storage.ScenarioScheduleApproval
	var err error
	if paged, ok := store.(interface {
		ListScenarioScheduleApprovalsPage(string, int, int) ([]*storage.ScenarioScheduleApproval, error)
	}); ok {
		approvals, err = paged.ListScenarioScheduleApprovalsPage(scheduleID, limit, offset)
	} else {
		approvals, err = store.ListScenarioScheduleApprovals(scheduleID)
		if err == nil {
			if limit <= 0 || offset >= len(approvals) {
				approvals = []*storage.ScenarioScheduleApproval{}
			} else {
				if offset < 0 {
					offset = 0
				}
				end := len(approvals)
				if limit < len(approvals)-offset {
					end = offset + limit
				}
				approvals = approvals[offset:end]
			}
		}
	}
	if err != nil {
		return nil, true, fmt.Errorf("list scenario schedule approvals: %w", err)
	}
	if approvals == nil {
		approvals = []*storage.ScenarioScheduleApproval{}
	}
	return approvals, true, nil
}

// DeleteScenarioScheduleOccurrence removes a single occurrence record for a
// schedule, scoped to its scenario. Returns false (→ 404) when the scenario,
// schedule, or occurrence is unknown or the occurrence belongs to a different
// schedule. Only terminal history whose parent schedule has durably advanced
// past the occurrence may be pruned; active or cross-row-commit-window records
// remain the scheduler's duplicate-prevention anchor. The schedule's cached
// LastOccurrence is cleared if it pointed at the deleted record.
func (tr *TestRunner) DeleteScenarioScheduleOccurrence(scenarioID string, scheduleID string, occurrenceID string) (bool, error) {
	tr.refreshScenariosForHARead(true)
	tr.mu.Lock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		tr.mu.Unlock()
		return false, nil
	}
	found := false
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			found = true
			break
		}
	}
	store := tr.scenarioStore
	tr.mu.Unlock()
	if !found || store == nil {
		return false, nil
	}
	occurrence, err := store.GetScenarioScheduleOccurrence(occurrenceID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || occurrence == nil || occurrence.ScheduleID != scheduleID {
		return false, err
	}
	if guardedStore, ok := store.(guardedScenarioScheduleDeletePersistence); ok {
		result, deleteErr := guardedStore.DeleteScenarioScheduleOccurrenceGuarded(occurrenceID)
		if deleteErr != nil {
			return true, deleteErr
		}
		switch result {
		case storage.ScheduleOccurrenceDeleteBlockedByStatus:
			return true, errScheduleHasActiveOccurrence
		case storage.ScheduleOccurrenceDeleteMissing:
			return false, nil
		}
	} else {
		switch strings.ToLower(strings.TrimSpace(occurrence.Status)) {
		case "materialized", "failed", "blocked", "skipped":
		default:
			return true, errScheduleHasActiveOccurrence
		}
		if err := store.DeleteScenarioScheduleOccurrence(occurrenceID); err != nil {
			return true, err
		}
	}
	tr.mu.Lock()
	if currentScenario := tr.scenarios[scenarioID]; currentScenario != nil {
		for _, current := range currentScenario.Schedules {
			if current != nil && current.ID == scheduleID && current.LastOccurrence != nil && current.LastOccurrence.ID == occurrenceID {
				current.LastOccurrence = nil
				break
			}
		}
	}
	tr.mu.Unlock()
	return true, nil
}

func (tr *TestRunner) ScenarioScheduleHealth(now time.Time) ScenarioScheduleHealth {
	// Health is a schedule entry point, so it must not reuse a recently cached
	// empty snapshot after another controller replica commits schedule changes.
	// The dashboard polls it every 5s, so the version probe supplies that
	// cross-replica freshness without re-materializing the whole library.
	tr.refreshScenarioScheduleContentForHARead()
	tr.mu.RLock()
	health := ScenarioScheduleHealth{StatusCounts: map[string]int{}}
	for _, schedule := range tr.listAllScenarioSchedulesLocked() {
		if schedule == nil {
			continue
		}
		health.Total++
		if schedule.Enabled {
			health.Enabled++
		}
		if schedule.NextRunAt != nil && !schedule.NextRunAt.After(now) {
			health.Due++
		}
		if strings.TrimSpace(schedule.ConflictSummary) != "" {
			health.Blocked++
		}
		if strings.TrimSpace(schedule.LastError) != "" {
			health.Failed++
		}
		if schedule.NextRequiredAction == "approval_required" {
			health.PendingApproval++
		}
	}
	store := tr.scenarioStore
	tr.mu.RUnlock()
	if store != nil {
		var err error
		if counter, ok := store.(scenarioScheduleOccurrenceStatusCounter); ok {
			var counts map[string]int
			counts, err = counter.CountScenarioScheduleOccurrencesByStatus()
			if counts != nil {
				health.StatusCounts = counts
			}
		} else {
			var occurrences []*storage.ScenarioScheduleOccurrence
			occurrences, err = store.ListAllScenarioScheduleOccurrences()
			if err == nil {
				for _, occurrence := range occurrences {
					if occurrence != nil {
						health.StatusCounts[occurrence.Status]++
					}
				}
			}
		}
		if err != nil {
			health.StorageError = "scenario occurrence history unavailable: " + err.Error()
		}
	}
	health.Queued = health.StatusCounts["queued"]
	health.Materialized = health.StatusCounts["materialized"]
	health.Skipped = health.StatusCounts["skipped"]
	health.Completed = health.StatusCounts["completed"]
	return health
}

type scenarioScheduleOccurrenceStatusCounter interface {
	CountScenarioScheduleOccurrencesByStatus() (map[string]int, error)
}

// scenarioRunSummary is the per-scenario rollup of the runner's test map that
// decorates a scenario listing. It is accumulated in a single pass over
// tr.tests so a listing costs O(tests) instead of O(scenarios x tests).
type scenarioRunSummary struct {
	resultCount      int
	releaseIDs       map[string]struct{}
	lastRunTestID    string
	lastRunStartedAt *time.Time
	lastRunEndedAt   *time.Time
}

// absorb folds one test into the rollup. Ties on startedAt keep the
// first-observed test, matching the previous per-scenario loop (tr.tests is a
// map, so that ordering was already unspecified).
func (summary *scenarioRunSummary) absorb(test *ManagedTest) {
	if test == nil {
		return
	}
	if test.Status == "COMPLETE" || test.Status == "FAILED" || test.Status == "ABORTED" {
		summary.resultCount++
	}
	if test.ReleaseID != "" {
		if summary.releaseIDs == nil {
			summary.releaseIDs = map[string]struct{}{}
		}
		summary.releaseIDs[test.ReleaseID] = struct{}{}
	}
	startedAt := test.CreatedAt
	if test.StartedAt != nil && !test.StartedAt.IsZero() {
		startedAt = *test.StartedAt
	}
	if summary.lastRunStartedAt == nil || startedAt.After(*summary.lastRunStartedAt) {
		t := startedAt
		summary.lastRunStartedAt = &t
		summary.lastRunTestID = test.ID
		summary.lastRunEndedAt = cloneTimePtr(test.EndedAt)
	}
}

func (tr *TestRunner) enrichScenarioSummaryLocked(scenario *StoredScenario) *StoredScenario {
	if scenario == nil {
		return nil
	}
	summary := scenarioRunSummary{}
	for _, test := range tr.tests {
		if test == nil || test.ScenarioID != scenario.ID {
			continue
		}
		summary.absorb(test)
	}
	return tr.applyScenarioRunSummaryLocked(scenario, &summary, nil)
}

// enrichScenarioSummariesLocked is the listing form of
// enrichScenarioSummaryLocked. It walks tr.tests once for the whole batch and
// resolves each distinct release once, instead of re-scanning every test (and
// re-reading the same release rows) for every scenario.
func (tr *TestRunner) enrichScenarioSummariesLocked(scenarios []*StoredScenario) []*StoredScenario {
	summaries := make(map[string]*scenarioRunSummary, len(scenarios))
	for _, scenario := range scenarios {
		if scenario == nil {
			continue
		}
		if _, exists := summaries[scenario.ID]; !exists {
			summaries[scenario.ID] = &scenarioRunSummary{}
		}
	}
	if len(summaries) > 0 {
		for _, test := range tr.tests {
			if test == nil {
				continue
			}
			if summary, ok := summaries[test.ScenarioID]; ok {
				summary.absorb(test)
			}
		}
	}
	releaseCache := make(map[string]ReleaseRef)
	out := make([]*StoredScenario, 0, len(scenarios))
	for _, scenario := range scenarios {
		if scenario == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, tr.applyScenarioRunSummaryLocked(scenario, summaries[scenario.ID], releaseCache))
	}
	return out
}

// applyScenarioRunSummaryLocked projects a rollup onto a read-safe clone. The
// optional releaseCache lets a batch resolve each release ref exactly once;
// pass nil for a single-scenario read.
func (tr *TestRunner) applyScenarioRunSummaryLocked(scenario *StoredScenario, summary *scenarioRunSummary, releaseCache map[string]ReleaseRef) *StoredScenario {
	if scenario == nil {
		return nil
	}
	copy := cloneStoredScenarioForReadLocked(scenario)
	if summary == nil {
		summary = &scenarioRunSummary{}
	}
	releaseSet := make(map[string]ReleaseRef, len(summary.releaseIDs))
	for releaseID := range summary.releaseIDs {
		ref, cached := releaseCache[releaseID]
		if !cached {
			ref = tr.releaseRefLocked(releaseID)
			if releaseCache != nil {
				releaseCache[releaseID] = ref
			}
		}
		releaseSet[releaseID] = ref
	}
	copy.ResultCount = summary.resultCount
	copy.AssociatedReleases = releaseRefsFromSet(releaseSet)
	copy.LastRunTestID = summary.lastRunTestID
	// Clone the timestamps so two clones built from one rollup never share a
	// mutable pointer, preserving cloneStoredScenarioForReadLocked's contract.
	copy.LastRunAt = cloneTimePtr(summary.lastRunStartedAt)
	copy.LastRunEndedAt = cloneTimePtr(summary.lastRunEndedAt)
	return copy
}

// cloneStoredScenarioForReadLocked returns a snapshot that shares no mutable
// pointers, slices, or maps with the runner's canonical scenario. Callers hold
// tr.mu while taking the snapshot, then may safely encode or mutate it after
// the lock is released.
func cloneStoredScenarioForReadLocked(scenario *StoredScenario) *StoredScenario {
	if scenario == nil {
		return nil
	}
	cloned := *scenario
	cloned.Assignment = cloneGeneratorAssignmentSettings(scenario.Assignment)
	cloned.GoalConfig = cloneGoalConfig(scenario.GoalConfig)
	cloned.TrafficProfile = cloneTrafficProfile(scenario.TrafficProfile)
	cloned.RuntimeSettings = cloneRuntimeSettings(scenario.RuntimeSettings)
	cloned.JointEvidence = scenario.JointEvidence.Clone()
	cloned.Scripts = cloneScenarioScriptRefs(scenario.Scripts)
	cloned.Schedules = make([]*storage.ScenarioSchedule, len(scenario.Schedules))
	for index, schedule := range scenario.Schedules {
		cloned.Schedules[index] = cloneScenarioSchedule(schedule)
	}
	cloned.DataPools = append([]storage.ScenarioDataPoolBinding(nil), scenario.DataPools...)
	cloned.AssociatedReleases = append([]ReleaseRef(nil), scenario.AssociatedReleases...)
	cloned.LastRunAt = cloneTimePtr(scenario.LastRunAt)
	cloned.LastRunEndedAt = cloneTimePtr(scenario.LastRunEndedAt)
	return &cloned
}

// scenarioProjectIDForContentMutation resolves the project before acquiring its
// deletion gate; the mutation owner prevents a local same-ID replacement.
func (tr *TestRunner) scenarioProjectIDForContentMutation(id string) string {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if scenario := tr.scenarios[tr.resolveRefKeyLocked(strings.TrimSpace(id))]; scenario != nil {
		return scenario.ProjectID
	}
	return ""
}
