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
	"strings"
	"time"

	"github.com/google/uuid"
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	stored, err := tr.storeScenarioLocked(uuid.New().String(), req)
	return cloneStoredScenarioForReadLocked(stored), err
}

func (tr *TestRunner) StoreScenarioWithID(id string, req CreateScenarioRequest) (*StoredScenario, error) {
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
			updated, updateErr := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario, scripts, bindings, expectedUpdatedAt)
			if updateErr != nil {
				return updateErr
			}
			if !updated {
				return errScenarioConcurrentChange
			}
			return nil
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("scenario id is required")
	}
	return tr.storeScenarioLockedWithWriter(id, req, func(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
		if store, ok := tr.scenarioStore.(conditionalScenarioPersistence); ok {
			created, err := store.CreateScenarioWithDataPoolBindingsIfAbsent(scenario, scripts, bindings)
			if err != nil {
				return err
			}
			if !created {
				return errScenarioConcurrentChange
			}
			return nil
		}
		if tr.scenarioStore != nil {
			return fmt.Errorf("durable scenario store does not support atomic create-only persistence")
		}
		if tr.scenarios[id] != nil {
			return errScenarioConcurrentChange
		}
		return nil
	})
}

// UpdateScenarioIfUnchanged applies a library synchronization update only when
// the durable scenario version observed during conflict detection is current.
func (tr *TestRunner) UpdateScenarioIfUnchanged(id string, req CreateScenarioRequest, expectedUpdatedAt time.Time) (*StoredScenario, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" || expectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf("scenario id and expected updated_at are required")
	}
	return tr.storeScenarioLockedWithWriter(id, req, func(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
		if store, ok := tr.scenarioStore.(conditionalScenarioPersistence); ok {
			updated, err := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario, scripts, bindings, expectedUpdatedAt)
			if err != nil {
				return err
			}
			if !updated {
				return errScenarioConcurrentChange
			}
			return nil
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" || expectedUpdatedAt.IsZero() {
		return false, fmt.Errorf("scenario id and expected updated_at are required")
	}
	deleted := false
	if store, ok := tr.scenarioStore.(conditionalScenarioDeletePersistence); ok {
		var err error
		deleted, err = store.DeleteScenarioIfUnchanged(id, expectedUpdatedAt)
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
		deleted = true
	}
	if current := tr.scenarios[id]; current != nil {
		tr.removeRefKeyLocked(current.RefKey)
	}
	delete(tr.scenarios, id)
	tr.contentRevision++
	return true, nil
}

func (tr *TestRunner) storeScenarioLockedWithWriter(id string, req CreateScenarioRequest, writer func(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) error) (*StoredScenario, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("scenario name is required")
	}
	if strings.TrimSpace(req.Duration) == "" {
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
		ScenarioMode: req.ScenarioMode,
		Duration:     req.Duration,
		StartDelay:   req.StartDelay,
		Assignment:   req.Assignment,
		GoalConfig:   req.GoalConfig,
	}); err != nil {
		return nil, err
	}
	groups, err := normalizeCreateTestGroups(CreateTestRequest{Groups: req.Groups})
	if err != nil {
		return nil, err
	}
	for index := range groups {
		groups[index].runtimeSecretScope = id
	}
	testGroups, engineGroups, _, err := tr.compileManagedTestGroupsForModeWithDataSourcesLocked(groups, req.RuntimeSettings, nil, normalizeScenarioMode(req.ScenarioMode))
	if err != nil {
		return nil, err
	}
	if runtimeSettingsReview := buildRuntimeSettingsScenarioReview(testGroups, engineGroups); runtimeSettingsReview != nil && runtimeSettingsReview.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", runtimeSettingsReview.Summary)
	}
	now := time.Now().UTC()
	createdAt := now
	refKey := ""
	if existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}
	if existing != nil {
		refKey = existing.RefKey
	}
	if refKey == "" {
		sequence, err := tr.nextScenarioSeqLocked()
		if err != nil {
			return nil, fmt.Errorf("allocate scenario reference key: %w", err)
		}
		refKey = refkey.Scenario(tr.envCode, sequence)
	}
	scenario := &storage.Scenario{
		ID:               id,
		RefKey:           refKey,
		Name:             req.Name,
		Duration:         req.Duration,
		StartDelay:       req.StartDelay,
		ScenarioMode:     normalizeScenarioMode(req.ScenarioMode),
		ScriptShakeout:   req.ScriptShakeout,
		ResourceBaseline: req.ResourceBaseline,
		Assignment:       generatorAssignmentForStorage(req.Assignment),
		GoalConfig:       goalConfigForStorage(req.GoalConfig),
		RuntimeSettings:  runtimeSettingsForStorage(req.RuntimeSettings),
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	scripts := make([]storage.ScenarioScript, 0, len(groups))
	for i, group := range groups {
		// compileManagedTestGroupsLocked validated and normalized the role
		// (e.g. the "canary" alias), so persist the normalized value.
		scripts = append(scripts, storage.ScenarioScript{
			ScenarioID:      id,
			Order:           i,
			Name:            group.Name,
			ScriptID:        group.ScriptID,
			Role:            testGroups[i].Role,
			VUsers:          group.VUsers,
			RampUp:          group.RampUp,
			RampDown:        group.RampDown,
			Stages:          cloneLoadStages(group.Stages),
			Iterations:      group.Iterations,
			Initialization:  group.InitStrategy,
			RuntimeSettings: runtimeSettingsForStorage(group.RuntimeSettings),
			Assignment:      generatorAssignmentForStorage(group.Assignment),
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
	if err := tr.validateDataPoolProjectsLocked(projectID, dataPools); err != nil {
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
	runtimeSecretAttempt, err := tr.persistRuntimeOverrideSecretValuesForResource(
		projectID, id, commitFingerprint, refKey, credentialGroups, actor,
		runtimeSecretProvisioningOptions{
			ResourceType:      "scenario",
			CommitFingerprint: commitFingerprint,
		},
	)
	if err != nil {
		return nil, err
	}
	rollbackProvisioning := func(persistErr error) error {
		rollbackErr := tr.rollbackRuntimeOverrideSecretAttempt(projectID, id, runtimeSecretAttempt, "scenario_persistence_failed")
		return errors.Join(persistErr, rollbackErr)
	}
	if writer != nil {
		if err := writer(scenario, scripts, dataPools); err != nil {
			return nil, rollbackProvisioning(err)
		}
	} else if tr.scenarioStore != nil {
		if atomicStore, ok := tr.scenarioStore.(atomicScenarioPersistence); ok {
			if err := atomicStore.SaveScenarioWithDataPoolBindings(scenario, scripts, dataPools); err != nil {
				return nil, rollbackProvisioning(err)
			}
		} else {
			// Non-SQL test/adaptor stores retain the legacy two-call contract.
			// Production SQLite/PostgreSQL stores implement the atomic interface.
			if err := tr.scenarioStore.SaveScenario(scenario, scripts); err != nil {
				return nil, rollbackProvisioning(err)
			}
			if err := tr.scenarioStore.SaveScenarioDataPoolBindings(id, dataPools); err != nil {
				return nil, rollbackProvisioning(err)
			}
		}
	}
	if err := tr.completeRuntimeOverrideSecretAttempt(runtimeSecretAttempt); err != nil {
		// The scenario fingerprint is durable commit evidence. Recovery retains
		// the governed values and removes only the value-free journal.
		slog.Warn("scenario runtime credential provisioning journal cleanup deferred", "scenario_id", id, "error", err)
		tr.scheduleRuntimeOverrideSecretAttempt(runtimeSecretAttempt.ID, runtimeSecretAttempt.LeaseExpiresAt)
	}
	stored := tr.buildStoredScenario(scenario, scripts)
	stored.DataPools = dataPools
	tr.scenarios[id] = stored
	tr.contentRevision++
	tr.indexRefKeyLocked(stored.RefKey, stored.ID)
	return stored, nil
}

func scenarioRuntimeCommitFingerprint(scenario *storage.Scenario, scripts []storage.ScenarioScript) string {
	if scenario == nil {
		return ""
	}
	return hashCanonical(struct {
		ID               string
		RefKey           string
		Name             string
		Duration         string
		StartDelay       string
		ScenarioMode     string
		ScriptShakeout   bool
		ResourceBaseline bool
		Assignment       any
		GoalConfig       any
		RuntimeSettings  any
		Scripts          []storage.ScenarioScript
	}{
		ID:               scenario.ID,
		RefKey:           scenario.RefKey,
		Name:             scenario.Name,
		Duration:         scenario.Duration,
		StartDelay:       scenario.StartDelay,
		ScenarioMode:     scenario.ScenarioMode,
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		Assignment:       scenario.Assignment,
		GoalConfig:       scenario.GoalConfig,
		RuntimeSettings:  scenario.RuntimeSettings,
		Scripts:          scripts,
	})
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
	for index, original := range bindings {
		binding := original
		binding.PoolID = strings.TrimSpace(binding.PoolID)
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.AccessMode = strings.TrimSpace(binding.AccessMode)
		binding.SelectionStrategy = strings.TrimSpace(binding.SelectionStrategy)
		if binding.PoolID == "" {
			return nil, fmt.Errorf("data pool id is required")
		}
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
		case "read_write", "consume":
			if allowLegacyModes {
				binding.AccessMode = "read_only"
			} else {
				return nil, fmt.Errorf("unsupported data pool access mode %q for binding %q; only \"read_only\" is currently supported", binding.AccessMode, binding.Alias)
			}
		default:
			return nil, fmt.Errorf("unsupported data pool access mode %q for binding %q; only \"read_only\" is currently supported", binding.AccessMode, binding.Alias)
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
	defer tr.mu.RUnlock()
	id = tr.resolveRefKeyLocked(id)
	scenario, ok := tr.scenarios[id]
	if !ok {
		return nil, false
	}
	return tr.enrichScenarioSummaryLocked(scenario), true
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
	defer tr.mu.RUnlock()
	out := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		out = append(out, tr.enrichScenarioSummaryLocked(scenario))
	}
	return out
}

func (tr *TestRunner) ListScenariosFiltered(projectID string) []*StoredScenario {
	tr.refreshScenariosForHARead()
	projectID = normalizeProjectID(projectID)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		enriched := tr.enrichScenarioSummaryLocked(scenario)
		if enriched.ProjectID != projectID {
			continue
		}
		out = append(out, enriched)
	}
	return out
}

func (tr *TestRunner) DeleteScenario(id string) bool {
	deleted, _ := tr.DeleteScenarioDurable(id)
	return deleted
}

func (tr *TestRunner) DeleteScenarioDurable(id string) (bool, error) {
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
		if err := tr.scenarioStore.DeleteScenario(id); err != nil {
			return false, err
		}
	}
	tr.removeRefKeyLocked(scenario.RefKey)
	delete(tr.scenarios, id)
	tr.contentRevision++
	return true, nil
}

func (tr *TestRunner) saveScenarioScheduleLocked(schedule *storage.ScenarioSchedule) error {
	if schedule == nil {
		return fmt.Errorf("schedule is required")
	}
	scenario, ok := tr.scenarios[schedule.ScenarioID]
	if !ok {
		return fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
	}
	existingIndex := -1
	expectedRevision := 0
	for index, existing := range scenario.Schedules {
		if existing != nil && existing.ID == schedule.ID {
			existingIndex = index
			expectedRevision = existing.Revision
			break
		}
	}
	if tr.scenarioStore != nil {
		if casStore, ok := tr.scenarioStore.(scenarioScheduleCASPersistence); ok {
			var changed bool
			var err error
			if existingIndex < 0 {
				changed, err = casStore.CreateScenarioScheduleIfAbsent(schedule)
			} else {
				changed, err = casStore.UpdateScenarioScheduleIfRevision(schedule, expectedRevision)
			}
			if err != nil {
				return err
			}
			if !changed {
				return fmt.Errorf("%w: scenario schedule %q changed or was deleted; reload and retry", errScheduleConcurrentChange, schedule.ID)
			}
		} else if err := tr.scenarioStore.SaveScenarioSchedule(schedule); err != nil {
			return err
		}
	}
	if existingIndex >= 0 {
		scenario.Schedules[existingIndex] = cloneScenarioSchedule(schedule)
		tr.contentRevision++
		return nil
	}
	scenario.Schedules = append(scenario.Schedules, cloneScenarioSchedule(schedule))
	tr.contentRevision++
	return nil
}

func (tr *TestRunner) saveScenarioScheduleOccurrenceLocked(occurrence *storage.ScenarioScheduleOccurrence) error {
	if occurrence == nil {
		return fmt.Errorf("schedule occurrence is required")
	}
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
	defer tr.mu.RUnlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
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
		return nil, false
	}
	if tr.scenarioStore == nil {
		return []*storage.ScenarioScheduleOccurrence{}, true
	}
	occurrences, err := tr.scenarioStore.ListScenarioScheduleOccurrences(scheduleID)
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
	defer tr.mu.RUnlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
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
		return nil, false
	}
	if tr.scenarioStore == nil {
		return []*storage.ScenarioScheduleApproval{}, true
	}
	approvals, err := tr.scenarioStore.ListScenarioScheduleApprovals(scheduleID)
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
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return false, nil
	}
	var target *storage.ScenarioSchedule
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			target = schedule
			break
		}
	}
	if target == nil || tr.scenarioStore == nil {
		return false, nil
	}
	occurrence, err := tr.scenarioStore.GetScenarioScheduleOccurrence(occurrenceID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || occurrence == nil || occurrence.ScheduleID != scheduleID {
		return false, err
	}
	if store, ok := tr.scenarioStore.(guardedScenarioScheduleDeletePersistence); ok {
		result, deleteErr := store.DeleteScenarioScheduleOccurrenceGuarded(occurrenceID)
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
		if err := tr.scenarioStore.DeleteScenarioScheduleOccurrence(occurrenceID); err != nil {
			return true, err
		}
	}
	if target.LastOccurrence != nil && target.LastOccurrence.ID == occurrenceID {
		target.LastOccurrence = nil
	}
	return true, nil
}

func (tr *TestRunner) ScenarioScheduleHealth(now time.Time) ScenarioScheduleHealth {
	tr.refreshScenariosForHARead(true)
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
		if occurrences, err := store.ListAllScenarioScheduleOccurrences(); err == nil {
			for _, occurrence := range occurrences {
				if occurrence != nil {
					health.StatusCounts[occurrence.Status]++
				}
			}
		} else {
			health.StorageError = "scenario occurrence history unavailable: " + err.Error()
		}
	}
	health.Queued = health.StatusCounts["queued"]
	health.Materialized = health.StatusCounts["materialized"]
	health.Skipped = health.StatusCounts["skipped"]
	health.Completed = health.StatusCounts["completed"]
	return health
}

func (tr *TestRunner) enrichScenarioSummaryLocked(scenario *StoredScenario) *StoredScenario {
	if scenario == nil {
		return nil
	}
	copy := cloneStoredScenarioForReadLocked(scenario)
	releaseSet := map[string]ReleaseRef{}
	var lastRunTestID string
	var lastRunStartedAt *time.Time
	var lastRunEndedAt *time.Time
	resultCount := 0
	for _, test := range tr.tests {
		if test.ScenarioID != scenario.ID {
			continue
		}
		if test.Status == "COMPLETE" || test.Status == "FAILED" || test.Status == "ABORTED" {
			resultCount++
		}
		if test.ReleaseID != "" {
			releaseSet[test.ReleaseID] = tr.releaseRefLocked(test.ReleaseID)
		}
		startedAt := test.CreatedAt
		if test.StartedAt != nil && !test.StartedAt.IsZero() {
			startedAt = *test.StartedAt
		}
		if lastRunStartedAt == nil || startedAt.After(*lastRunStartedAt) {
			t := startedAt
			lastRunStartedAt = &t
			lastRunTestID = test.ID
			lastRunEndedAt = cloneTimePtr(test.EndedAt)
		}
	}
	copy.ResultCount = resultCount
	copy.AssociatedReleases = releaseRefsFromSet(releaseSet)
	copy.LastRunTestID = lastRunTestID
	copy.LastRunAt = lastRunStartedAt
	copy.LastRunEndedAt = lastRunEndedAt
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
	cloned.RuntimeSettings = cloneRuntimeSettings(scenario.RuntimeSettings)
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
