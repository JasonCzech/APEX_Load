package api

// Persistence seam for the TestRunner: bootstrap loaders for stored scripts,
// scenarios, tests, history and archives; reconcile; save/delete/update of
// records and run status; in-memory<->storage record conversion; ownership
// attribution; and usage-accounting snapshot capture/cadence.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	durableTransitionAttempts   = 3
	durableTransitionRetryDelay = 10 * time.Millisecond
)

var (
	errStoredTestMissing           = errors.New("durable test row no longer exists")
	errStoredTestTransitionFenced  = errors.New("durable test lifecycle transition was fenced")
	errLegacyRuntimeSecretSnapshot = errors.New("stored runtime snapshot contains plaintext runtime credentials")
	// errStaleRuntimeSnapshotValidation marks a compiled snapshot that no longer
	// satisfies the current DSL validator. Validation coverage widens over time —
	// pacing, run-logic, and think-time resource limits recently began applying to
	// every protocol rather than only the messaging families — so a snapshot that
	// was valid when it was written can become invalid without the test changing.
	// Treat it like the legacy-secret case: recompile from the durable source
	// scripts instead of refusing to restore the test.
	errStaleRuntimeSnapshotValidation = errors.New("stored runtime snapshot no longer satisfies script validation")
)

type testRuntimeSnapshotRepairPersistence interface {
	RepairTestRuntimeSnapshot(id, projectID, materializationKey string, startTime time.Time, groups, runtimeSettings any) (bool, error)
	RepairTestRuntimeSnapshotIfLeaseHeld(id, projectID, materializationKey string, startTime time.Time, groups, runtimeSettings any, leaseName, leaseOwnerID string) (bool, error)
}

func isStoredTestTransitionCancelled(err error) bool {
	return errors.Is(err, errStoredTestMissing) ||
		errors.Is(err, errStoredTestTransitionFenced) ||
		errors.Is(err, errNotExecutionLeader)
}

// retryDurableTransition absorbs short storage interruptions without allowing a
// terminal state to become visible only in memory. The bound is intentionally
// small: execution goroutines may wait briefly for durability, but cannot hang
// indefinitely behind an unavailable database.
func retryDurableTransition(operation func() error) error {
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		if err := operation(); err != nil {
			lastErr = err
			if attempt+1 < durableTransitionAttempts {
				time.Sleep(durableTransitionRetryDelay)
			}
			continue
		}
		return nil
	}
	return lastErr
}

func (tr *TestRunner) loadStoredScripts() {
	if tr.scriptStore == nil {
		return
	}
	scripts, err := tr.scriptStore.ListScripts("", nil)
	if err != nil {
		return
	}
	for _, script := range scripts {
		tr.scripts[script.ID] = &StoredScript{
			ID:              script.ID,
			RefKey:          script.RefKey,
			Name:            script.Name,
			Protocol:        script.Protocol,
			Version:         script.Version,
			ProjectID:       script.ProjectID,
			FolderID:        script.FolderID,
			CreatedAt:       script.CreatedAt,
			AuthoringHealth: buildStoredScriptAuthoringHealth(script),
			Script:          script,
		}
	}
}

func (tr *TestRunner) reloadStoredScripts() error {
	if tr == nil || tr.scriptStore == nil {
		return nil
	}
	tr.contentReloadMu.Lock()
	defer tr.contentReloadMu.Unlock()
	tr.mu.RLock()
	revision := tr.contentRevision
	tr.mu.RUnlock()
	scripts, err := tr.scriptStore.ListScripts("", nil)
	if err != nil {
		return err
	}
	loaded := make(map[string]*StoredScript, len(scripts))
	for _, script := range scripts {
		if script == nil {
			continue
		}
		loaded[script.ID] = &StoredScript{
			ID:              script.ID,
			RefKey:          script.RefKey,
			Name:            script.Name,
			Protocol:        script.Protocol,
			Version:         script.Version,
			ProjectID:       script.ProjectID,
			FolderID:        script.FolderID,
			CreatedAt:       script.CreatedAt,
			AuthoringHealth: buildStoredScriptAuthoringHealth(script),
			Script:          script,
		}
	}
	tr.mu.Lock()
	if tr.contentRevision != revision {
		tr.mu.Unlock()
		return nil
	}
	// Private CI snapshots exist only long enough for synchronous test
	// compilation. Preserve them across an HA read refresh; they are deliberately
	// absent from the durable script listing used to build loaded.
	for id, references := range tr.privateCISnapshots {
		if references > 0 && tr.scripts[id] != nil {
			loaded[id] = tr.scripts[id]
		}
	}
	for _, script := range tr.scripts {
		if script != nil {
			tr.removeRefKeyLocked(script.RefKey)
		}
	}
	tr.scripts = loaded
	for _, script := range loaded {
		tr.observeAndIndexRefKeyLocked(script.RefKey, script.ID)
	}
	tr.mu.Unlock()
	return nil
}

func (tr *TestRunner) loadStoredScenarios() {
	if tr.scenarioStore == nil {
		return
	}
	scenarios, err := tr.scenarioStore.ListScenarios()
	if err != nil {
		return
	}
	for _, scenario := range scenarios {
		full, scripts, err := tr.scenarioStore.GetScenario(scenario.ID)
		if err != nil {
			full = scenario
		}
		if full != nil {
			bindings, bindingsErr := tr.scenarioStore.ListScenarioDataPoolBindings(full.ID)
			if bindingsErr != nil {
				slog.Warn("failed to load legacy scenario bindings for runtime repair", "scenario_id", full.ID, "error", bindingsErr)
			} else if repairErr := tr.repairLegacyStoredScenarioRuntime(full, scripts, bindings); repairErr != nil {
				slog.Error("failed to protect stored scenario runtime", "scenario_id", full.ID, "error", repairErr)
			}
		}
		tr.scenarios[scenario.ID] = tr.buildStoredScenario(full, scripts)
	}
}

// reloadStoredScenarios refreshes the in-memory scenario/schedule view from
// shared storage so an HA leader observes CRUD performed by another replica.
func (tr *TestRunner) reloadStoredScenarios() error {
	if tr == nil || tr.scenarioStore == nil {
		return nil
	}
	tr.contentReloadMu.Lock()
	defer tr.contentReloadMu.Unlock()
	tr.mu.RLock()
	revision := tr.contentRevision
	tr.mu.RUnlock()
	scenarios, err := tr.scenarioStore.ListScenarios()
	if err != nil {
		return err
	}
	loaded := make(map[string]*StoredScenario, len(scenarios))
	type loadedScenarioParts struct {
		full      *storage.Scenario
		scripts   []storage.ScenarioScript
		bindings  []storage.ScenarioDataPoolBinding
		schedules []*storage.ScenarioSchedule
	}
	parts := make([]loadedScenarioParts, 0, len(scenarios))
	for _, scenario := range scenarios {
		if scenario == nil {
			continue
		}
		full, scripts, loadErr := tr.scenarioStore.GetScenario(scenario.ID)
		if loadErr != nil {
			return loadErr
		}
		bindings, loadErr := tr.scenarioStore.ListScenarioDataPoolBindings(scenario.ID)
		if loadErr != nil {
			return fmt.Errorf("load scenario %s data pools: %w", scenario.ID, loadErr)
		}
		if repairErr := tr.repairLegacyStoredScenarioRuntime(full, scripts, bindings); repairErr != nil {
			return fmt.Errorf("protect stored scenario %s runtime: %w", scenario.ID, repairErr)
		}
		schedules, loadErr := tr.scenarioStore.ListScenarioSchedules(scenario.ID)
		if loadErr != nil {
			return fmt.Errorf("load scenario %s schedules: %w", scenario.ID, loadErr)
		}
		parts = append(parts, loadedScenarioParts{full: full, scripts: scripts, bindings: bindings, schedules: schedules})
	}
	tr.mu.RLock()
	for _, part := range parts {
		stored := tr.buildStoredScenario(part.full, part.scripts)
		stored.DataPools = append([]storage.ScenarioDataPoolBinding(nil), part.bindings...)
		stored.Schedules = make([]*storage.ScenarioSchedule, 0, len(part.schedules))
		for _, schedule := range part.schedules {
			stored.Schedules = append(stored.Schedules, cloneScenarioSchedule(schedule))
		}
		loaded[part.full.ID] = stored
	}
	tr.mu.RUnlock()
	tr.mu.Lock()
	if tr.contentRevision != revision {
		tr.mu.Unlock()
		return nil
	}
	for _, scenario := range tr.scenarios {
		if scenario != nil {
			tr.removeRefKeyLocked(scenario.RefKey)
		}
	}
	tr.scenarios = loaded
	for _, scenario := range loaded {
		tr.observeAndIndexRefKeyLocked(scenario.RefKey, scenario.ID)
	}
	tr.mu.Unlock()
	return nil
}

func (tr *TestRunner) repairLegacyStoredScenarioRuntime(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
	if tr == nil || scenario == nil {
		return nil
	}
	changed := false
	protectedRoot, rootValues, rootChanged, err := protectLegacyRuntimeOverrideSecrets(
		scenario.ID, -1, nil, decodeStoredRuntimeSettings(scenario.RuntimeSettings),
	)
	for name := range rootValues {
		rootValues[name] = ""
		delete(rootValues, name)
	}
	if err != nil {
		return err
	}
	if rootChanged {
		scenario.RuntimeSettings = runtimeSettingsForStorage(protectedRoot)
		changed = true
	}
	for index := range scripts {
		protected, values, groupChanged, protectErr := protectLegacyRuntimeOverrideSecrets(
			scenario.ID, index, nil, decodeStoredRuntimeSettings(scripts[index].RuntimeSettings),
		)
		for name := range values {
			values[name] = ""
			delete(values, name)
		}
		if protectErr != nil {
			return fmt.Errorf("group[%d]: %w", index, protectErr)
		}
		if groupChanged {
			scripts[index].RuntimeSettings = runtimeSettingsForStorage(protected)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	store, ok := tr.scenarioStore.(conditionalScenarioPersistence)
	if !ok {
		return errors.New("scenario persistence does not support conditional legacy runtime repair")
	}
	expectedUpdatedAt := scenario.UpdatedAt
	repaired, err := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(
		scenario, scripts, append([]storage.ScenarioDataPoolBinding(nil), bindings...), expectedUpdatedAt,
	)
	if err != nil {
		return err
	}
	if !repaired {
		// Another writer won the CAS. The local copy is already protected and the
		// next bounded reload will adopt the durable winner.
		return nil
	}
	return nil
}

func (tr *TestRunner) buildStoredScenario(scenario *storage.Scenario, scripts []storage.ScenarioScript) *StoredScenario {
	if scenario == nil {
		return nil
	}
	refs := make([]ScenarioScriptRef, 0, len(scripts))
	projectID, singleProject := tr.scenarioProjectIDFromScriptsLocked(scripts)
	for _, script := range scripts {
		ref := ScenarioScriptRef{
			Order:           script.Order,
			Name:            script.Name,
			ScriptID:        script.ScriptID,
			Role:            script.Role,
			VUsers:          script.VUsers,
			RampUp:          script.RampUp,
			RampDown:        script.RampDown,
			Stages:          cloneLoadStages(script.Stages),
			Iterations:      script.Iterations,
			Initialization:  script.Initialization,
			RuntimeSettings: decodeStoredRuntimeSettings(script.RuntimeSettings),
			Assignment:      decodeStoredGeneratorAssignment(script.Assignment),
		}
		if stored, ok := tr.scripts[script.ScriptID]; ok {
			ref.ScriptName = stored.Name
			ref.Protocol = stored.Protocol
			if ref.Name == "" {
				ref.Name = stored.Name
			}
		}
		refs = append(refs, ref)
	}
	dataPools := []storage.ScenarioDataPoolBinding{}
	if tr.scenarioStore != nil {
		if bindings, err := tr.scenarioStore.ListScenarioDataPoolBindings(scenario.ID); err == nil {
			dataPools = bindings
		}
	}
	schedules := []*storage.ScenarioSchedule{}
	if tr.scenarioStore != nil {
		if storedSchedules, err := tr.scenarioStore.ListScenarioSchedules(scenario.ID); err == nil {
			for _, schedule := range storedSchedules {
				schedules = append(schedules, cloneScenarioSchedule(schedule))
			}
		}
	}
	derivedProjectID := ""
	if singleProject {
		derivedProjectID = projectID
	}
	return &StoredScenario{
		ID:               scenario.ID,
		RefKey:           scenario.RefKey,
		Name:             scenario.Name,
		ProjectID:        derivedProjectID,
		Duration:         scenario.Duration,
		StartDelay:       scenario.StartDelay,
		ScenarioMode:     normalizeScenarioMode(scenario.ScenarioMode),
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		Assignment:       decodeStoredGeneratorAssignment(scenario.Assignment),
		GoalConfig:       decodeStoredGoalConfig(scenario.GoalConfig),
		RuntimeSettings:  decodeStoredRuntimeSettings(scenario.RuntimeSettings),
		Scripts:          refs,
		Schedules:        schedules,
		DataPools:        dataPools,
		CreatedAt:        scenario.CreatedAt,
		UpdatedAt:        scenario.UpdatedAt,
		ScriptCount:      len(refs),
	}
}

func (tr *TestRunner) scenarioProjectIDFromScriptsLocked(scripts []storage.ScenarioScript) (string, bool) {
	projectID := ""
	for _, scenarioScript := range scripts {
		stored, ok := tr.scripts[scenarioScript.ScriptID]
		if !ok || stored == nil {
			continue
		}
		candidate := normalizeProjectID(stored.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", false
		}
	}
	if projectID == "" {
		return "", false
	}
	return projectID, true
}

func (tr *TestRunner) loadStoredTests() {
	if tr.testStore == nil {
		return
	}
	tests, err := tr.testStore.ListTests("", nil)
	if err != nil {
		return
	}
	for _, record := range tests {
		managedTest := managedTestFromRecord(record)
		if err := tr.restoreManagedTestRuntimeAndRepair(managedTest); err != nil {
			managedTest.runtimeHydrationError = err.Error()
			slog.Error("failed to restore stored test runtime", "test_id", managedTest.ID, "error", err)
		}
		// Marking an interrupted run FAILED is a leader-only mutation: a booting
		// non-leader (HA standby) must not clobber the active leader's live runs
		// in the shared database. The eventual leader reconciles on acquisition.
		if tr.isExecutionLeader() {
			if err := tr.reconcileStoredActiveTest(managedTest); err != nil {
				slog.Error("failed to reconcile stored active test", "test_id", managedTest.ID, "error", err)
			}
		}
		tr.loadStoredTestHistory(managedTest)
		tr.loadStoredTestArchive(managedTest)
		tr.tests[record.ID] = managedTest
	}
}

// reloadStoredTests refreshes the in-memory test map from the database when this
// replica acquires execution leadership. Unlike loadStoredTests (boot, single
// threaded), it runs concurrently with API readers, so map mutation is locked.
// Tests this replica is actively running are left untouched; everything else is
// re-hydrated so a failover leader sees runs and queued work created elsewhere.
const haTestRefreshInterval = 250 * time.Millisecond

func (tr *TestRunner) reloadStoredTests() error {
	if tr.testStore == nil {
		return nil
	}
	tr.testReloadMu.Lock()
	defer tr.testReloadMu.Unlock()
	return tr.reloadStoredTestsLocked()
}

// reloadStoredTestsLocked performs the durable reconciliation while the caller
// holds testReloadMu. Keeping the implementation separate lets follower reads
// make their freshness decision and perform the refresh atomically, preventing
// a burst of waiters from each repeating the same full scan.
func (tr *TestRunner) reloadStoredTestsLocked() error {
	if tr.testStore == nil {
		return nil
	}
	leaderAtStart := tr.holdsExecutionLeadership()
	executionFenceAtStart := tr.currentExecutionFenceToken()
	sameExecutionEpoch := func() bool {
		if tr.holdsExecutionLeadership() != leaderAtStart {
			return false
		}
		return !tr.durableExecutionFence || tr.currentExecutionFenceToken() == executionFenceAtStart
	}
	records, err := tr.testStore.ListTests("", nil)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record == nil || strings.TrimSpace(record.ID) == "" {
			continue
		}
		if !sameExecutionEpoch() {
			return fmt.Errorf("execution leadership changed while reloading tests")
		}
		// ListTests is the durable snapshot for this refresh. Re-reading every row
		// with GetTest turned each follower poll into an avoidable N+1 query burst;
		// a concurrent change is picked up by the next bounded refresh.
		seen[record.ID] = struct{}{}
		tr.mu.RLock()
		existing, ok := tr.tests[record.ID]
		liveLocally := ok && liveTestRuntimeForExecutionEpoch(existing, tr.durableExecutionFence, executionFenceAtStart)
		sameIdentity := ok && sameStoredTestIdentity(existing, record)
		staleRuntimeHandle := sameIdentity && !liveLocally && (existing.cancel != nil || existing.executionCtx != nil)
		reuseReplicaState := sameIdentity && !staleRuntimeHandle
		retryHydration := reuseReplicaState && (existing.runtimeHydrationError != "" || existing.runtimeSnapshotRepairRequired)
		tr.mu.RUnlock()
		if liveLocally {
			continue
		}
		managedTest := managedTestFromRecord(record)
		if !reuseReplicaState || retryHydration {
			if err := tr.restoreManagedTestRuntimeAndRepair(managedTest); err != nil {
				managedTest.runtimeHydrationError = err.Error()
				slog.Error("failed to restore reloaded test runtime", "test_id", managedTest.ID, "error", err)
			}
		}
		if leaderAtStart && sameExecutionEpoch() {
			if err := tr.reconcileStoredActiveTest(managedTest); err != nil {
				return fmt.Errorf("reconcile stored active test %s: %w", managedTest.ID, err)
			}
		}
		// Rich history and archive materialization can be large and may touch
		// object storage. Hydrate them only for a genuinely new/reused identity;
		// unchanged replicas retain their already-loaded immutable snapshots.
		if !reuseReplicaState {
			tr.loadStoredTestHistory(managedTest)
			if archive, found, loadErr := tr.loadRunArchiveFromStore(managedTest.ID); loadErr != nil {
				slog.Warn("failed to load stored test archive", "test_id", managedTest.ID, "error", loadErr)
			} else if found && runArchiveRecordAvailable(archive) {
				managedTest.Archive = archive
			}
		}
		tr.mu.Lock()
		// Re-check liveness under the write lock: startManagedTestRun may have begun
		// a run for this ID in the window since the RLock above. Overwriting it with
		// the DB-hydrated copy (cancel == nil) would detach the running goroutine's
		// cancel func, so a later Stop/Abort could never stop the run.
		if !sameExecutionEpoch() {
			tr.mu.Unlock()
			return fmt.Errorf("execution leadership changed while reloading tests")
		}
		current, currentOK := tr.tests[record.ID]
		if currentOK && current != nil && (liveTestRuntimeForExecutionEpoch(current, tr.durableExecutionFence, executionFenceAtStart) || (isTerminalTestStatus(current.Status) && !isTerminalTestStatus(managedTest.Status))) {
			tr.mu.Unlock()
			continue
		}
		if currentOK && sameStoredTestIdentity(current, record) && current.cancel == nil && current.executionCtx == nil {
			oldRefKey := current.RefKey
			applyStoredManagedTestSnapshot(current, managedTest)
			if current.runtimeHydrationError != "" && managedTest.runtimeHydrationError == "" && len(managedTest.groupSpecs) > 0 {
				current.Groups = managedTest.Groups
				current.groupSpecs = managedTest.groupSpecs
				current.runtimeHydrationError = ""
			}
			if oldRefKey != current.RefKey {
				tr.removeRefKeyLocked(oldRefKey)
			}
			tr.observeAndIndexRefKeyLocked(current.RefKey, current.ID)
			tr.mu.Unlock()
			continue
		}
		if currentOK && current != nil {
			tr.removeRefKeyLocked(current.RefKey)
			tr.forgetRunSnapshotWriteStateLocked(record.ID)
			forgetRunArchiveCache(record.ID)
			deleteGroupMetricsHistoryCache(record.ID)
		}
		tr.tests[record.ID] = managedTest
		if managedTest.Archive != nil {
			tr.archives[record.ID] = managedTest.Archive
		} else {
			delete(tr.archives, record.ID)
		}
		tr.observeAndIndexRefKeyLocked(managedTest.RefKey, managedTest.ID)
		tr.mu.Unlock()
	}
	tr.mu.RLock()
	deleteCandidates := make(map[string]*ManagedTest)
	for id, current := range tr.tests {
		if _, ok := seen[id]; ok || liveTestRuntimeForExecutionEpoch(current, tr.durableExecutionFence, executionFenceAtStart) {
			continue
		}
		deleteCandidates[id] = current
	}
	tr.mu.RUnlock()
	for id, candidate := range deleteCandidates {
		if !sameExecutionEpoch() {
			return fmt.Errorf("execution leadership changed while reloading tests")
		}
		if durable, getErr := tr.testStore.GetTest(id); getErr == nil && durable != nil {
			continue
		} else if getErr != nil && !errors.Is(getErr, sql.ErrNoRows) {
			return fmt.Errorf("confirm deleted test %s: %w", id, getErr)
		}
		tr.mu.Lock()
		current := tr.tests[id]
		if current != candidate || liveTestRuntimeForExecutionEpoch(current, tr.durableExecutionFence, executionFenceAtStart) {
			tr.mu.Unlock()
			continue
		}
		if current != nil {
			tr.removeRefKeyLocked(current.RefKey)
		}
		tr.forgetRunSnapshotWriteStateLocked(id)
		delete(tr.tests, id)
		tr.mu.Unlock()
	}
	return nil
}

func liveTestRuntimeForExecutionEpoch(test *ManagedTest, requireFence bool, executionFence string) bool {
	if test == nil || test.cancel == nil || test.executionCtx == nil || test.executionCtx.Err() != nil {
		return false
	}
	return !requireFence || (executionFence != "" && test.executionFence == executionFence)
}

func sameStoredTestIdentity(test *ManagedTest, record *storage.TestRecord) bool {
	if test == nil || record == nil || test.ID != record.ID {
		return false
	}
	return normalizeProjectID(test.ProjectID) == normalizeProjectID(record.ProjectID) && test.CreatedAt.Equal(record.StartTime)
}

// applyStoredManagedTestSnapshot updates only fields whose source of truth is
// the durable test row. Runtime engines, compiled scripts, live metrics,
// history, archive state, and derived scheduling/capacity projections remain
// attached to the existing identity and are not repeatedly rebuilt by follower
// polling.
func applyStoredManagedTestSnapshot(current, stored *ManagedTest) {
	if current == nil || stored == nil {
		return
	}
	current.RefKey = stored.RefKey
	current.MaterializationKey = stored.MaterializationKey
	current.Name = stored.Name
	current.Status = stored.Status
	current.ApplicationID = stored.ApplicationID
	current.ReleaseID = stored.ReleaseID
	current.ScenarioID = stored.ScenarioID
	current.ProjectID = stored.ProjectID
	current.FolderID = cloneStringPtr(stored.FolderID)
	current.DataPools = cloneScenarioDataPoolBindings(stored.DataPools)
	current.TotalVUsers = stored.TotalVUsers
	current.Duration = stored.Duration
	current.DurationStr = stored.DurationStr
	current.CreatedAt = stored.CreatedAt
	current.ScheduledStartAt = cloneTimePtr(stored.ScheduledStartAt)
	current.TimeslotID = cloneStringPtr(stored.TimeslotID)
	current.PendingSince = cloneTimePtr(stored.PendingSince)
	current.PausedAt = cloneTimePtr(stored.PausedAt)
	current.CumulativePause = stored.CumulativePause
	current.CapacityWindowStartAt = cloneTimePtr(stored.CapacityWindowStartAt)
	current.CapacityWindowEndAt = cloneTimePtr(stored.CapacityWindowEndAt)
	current.BaselinePinned = stored.BaselinePinned
	current.BaselinePinnedAt = cloneTimePtr(stored.BaselinePinnedAt)
	current.EndedAt = cloneTimePtr(stored.EndedAt)
	current.CreatedBySubject = stored.CreatedBySubject
	current.CreatedByDisplayName = stored.CreatedByDisplayName
	current.StartedBySubject = stored.StartedBySubject
	current.StartedByDisplayName = stored.StartedByDisplayName
	current.StoppedBySubject = stored.StoppedBySubject
	current.StoppedByDisplayName = stored.StoppedByDisplayName
	current.StartAuthorization = cloneQueuedStartAuthorization(stored.StartAuthorization)
	current.ExecutionPlanApproval = cloneRunArchiveExecutionPlanApprovalSnapshot(stored.ExecutionPlanApproval)
	current.Error = stored.Error
}

func (tr *TestRunner) reconcileStoredActiveTest(test *ManagedTest) error {
	if test == nil {
		return nil
	}
	switch test.Status {
	case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
	default:
		return nil
	}
	if err := tr.claimTestExecutionFence(test); err != nil {
		return err
	}
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousEndedAt := cloneTimePtr(test.EndedAt)
	previousError := test.Error
	now := time.Now().UTC()
	test.Status = "FAILED"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.EndedAt = &now
	if test.Error == "" {
		test.Error = "controller restarted before the run completed"
	}
	if err := tr.persistStoredTestStatus(test.ID, test.executionFence, test.Status, &now, test.Error); err != nil {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.EndedAt = previousEndedAt
		test.Error = previousError
		slog.Error("failed to persist recovered test state", "test_id", test.ID, "status", test.Status, "error", err)
		return err
	}
	// The hydrated row is deliberately not in tr.tests yet. Release from the
	// explicit durable binding and retain that slot ID for retry so startup
	// reconciliation cannot leak capacity merely because map insertion happens
	// later in the load loop.
	if test.TimeslotID != nil {
		slotID := strings.TrimSpace(*test.TimeslotID)
		if err := tr.releaseCapacityReservationBinding(test.ID, slotID); err != nil {
			tr.enqueueCapacityReservationReleaseRetry(test.ID, slotID)
		}
	}
	return nil
}

func managedTestFromRecord(record *storage.TestRecord) *ManagedTest {
	managedTest := &ManagedTest{
		ID:                    record.ID,
		RefKey:                record.RefKey,
		MaterializationKey:    record.MaterializationKey,
		Name:                  record.Name,
		Status:                record.Status,
		ApplicationID:         record.ApplicationID,
		ReleaseID:             record.ReleaseID,
		ScenarioID:            record.ScenarioID,
		ProjectID:             record.ProjectID,
		FolderID:              record.FolderID,
		DataPools:             cloneScenarioDataPoolBindings(record.DataPools),
		Groups:                decodeStoredGroups(record.Groups),
		Assignment:            decodeStoredGeneratorAssignment(record.Assignment),
		ScenarioMode:          normalizeScenarioMode(record.ScenarioMode),
		ScriptShakeout:        record.ScriptShakeout,
		ResourceBaseline:      record.ResourceBaseline,
		StartDelay:            record.StartDelay,
		GoalConfig:            decodeStoredGoalConfig(record.GoalConfig),
		RuntimeSettings:       decodeStoredRuntimeSettings(record.RuntimeSettings),
		TotalVUsers:           record.TotalVUsers,
		Duration:              time.Duration(record.Duration),
		DurationStr:           time.Duration(record.Duration).String(),
		CreatedAt:             record.StartTime,
		ScheduledStartAt:      cloneTimePtr(record.ScheduledStartAt),
		TimeslotID:            cloneStringPtr(record.TimeslotID),
		PendingSince:          record.PendingSince,
		PausedAt:              cloneTimePtr(record.PausedAt),
		CumulativePause:       time.Duration(record.CumulativePause),
		CapacityWindowStartAt: cloneTimePtr(record.CapacityWindowStartAt),
		CapacityWindowEndAt:   cloneTimePtr(record.CapacityWindowEndAt),
		BaselinePinned:        record.BaselinePinned,
		BaselinePinnedAt:      record.BaselinePinnedAt,
		CreatedBySubject:      record.CreatedBySubject,
		CreatedByDisplayName:  record.CreatedByDisplayName,
		StartedBySubject:      record.StartedBySubject,
		StartedByDisplayName:  record.StartedByDisplayName,
		StoppedBySubject:      record.StoppedBySubject,
		StoppedByDisplayName:  record.StoppedByDisplayName,
		StartAuthorization:    decodeStoredStartAuthorization(record.StartAuthorization),
		ExecutionPlanApproval: decodeStoredExecutionPlanApproval(record.ExecutionPlanApproval),
		Error:                 record.Error,
	}
	if record.EndTime != nil {
		managedTest.EndedAt = record.EndTime
	}
	return managedTest
}

// restoreManagedTestRuntime rebuilds the process-private engine group specs that
// are intentionally absent from API JSON. New rows carry the exact compiled
// runtime script in groups_json; legacy rows are recompiled from their durable
// scripts so queued work created before this snapshot format remains runnable.
func (tr *TestRunner) restoreManagedTestRuntime(test *ManagedTest) error {
	if tr == nil || test == nil {
		return fmt.Errorf("test is required")
	}
	if len(test.Groups) == 0 {
		return fmt.Errorf("stored test has no groups")
	}

	// Legacy rows may contain plaintext runtime overrides even when they already
	// have a compiled script snapshot. Convert those values (and unrecoverable
	// sentinels) to deterministic references before deciding whether the stored
	// script can be trusted. No value is written to the project secret store:
	// unattributed historical rows must never gain secret-write authority during
	// startup, failover, or an ordinary read.
	protectedRuntimeSettings, rootValues, rootChanged, err := protectLegacyRuntimeOverrideSecrets(test.ID, -1, nil, test.RuntimeSettings)
	if err != nil {
		return fmt.Errorf("protect restored test runtime credentials: %w", err)
	}
	clearValues := func(values map[string]string) {
		for name := range values {
			values[name] = ""
			delete(values, name)
		}
	}
	defer clearValues(rootValues)
	runtimeOverridesChanged := rootChanged
	protectedGroupSettings := make([]*RuntimeSettingsOverride, len(test.Groups))
	for index := range test.Groups {
		protected, values, changed, protectErr := protectLegacyRuntimeOverrideSecrets(test.ID, index, test.Groups[index].script, test.Groups[index].RuntimeSettings)
		if protectErr != nil {
			clearValues(values)
			return fmt.Errorf("protect restored group[%d] runtime credentials: %w", index, protectErr)
		}
		clearValues(values)
		protectedGroupSettings[index] = protected
		runtimeOverridesChanged = runtimeOverridesChanged || changed
	}
	if runtimeOverridesChanged {
		test.RuntimeSettings = protectedRuntimeSettings
		for index := range test.Groups {
			test.Groups[index].RuntimeSettings = protectedGroupSettings[index]
			test.Groups[index].runtimeSecretValues = nil
		}
	}

	compiledSnapshots := true
	for _, group := range test.Groups {
		if group.script == nil {
			compiledSnapshots = false
			break
		}
	}
	// A compiled script built from plaintext overrides may already contain those
	// literals. Recompile from the durable source scripts after any conversion so
	// the new snapshot contains only {{secret.*}} references.
	if runtimeOverridesChanged {
		compiledSnapshots = false
	}
	if compiledSnapshots {
		specs, err := restoreStoredEngineGroupSpecs(test)
		if err != nil &&
			!errors.Is(err, errLegacyRuntimeSecretSnapshot) &&
			!errors.Is(err, errStaleRuntimeSnapshotValidation) {
			return err
		}
		if err == nil {
			for index := range specs {
				resolver := tr.secretResolverForProject(specs[index].Script.ProjectID)
				if index < len(test.Groups) {
					resolver = overlayRuntimeSecretResolver(test.Groups[index].runtimeSecretValues, resolver)
				}
				specs[index].SecretResolver = resolver
			}
			test.groupSpecs = specs
			return nil
		}
		test.runtimeSnapshotRepairRequired = true
	}

	entries := make([]CreateGroupEntry, 0, len(test.Groups))
	for _, group := range test.Groups {
		iterationOverride := group.IterationOverride
		if !group.IterationOverrideKnown {
			// Legacy rows have no layer provenance. A repair must preserve their
			// effective execution, so pin that value as an explicit compact
			// override instead of incorrectly treating it as inherited.
			iterationOverride = group.Iterations
		}
		entries = append(entries, CreateGroupEntry{
			Name:               group.Name,
			ScriptID:           group.ScriptID,
			Role:               group.Role,
			VUsers:             group.VUsers,
			RampUp:             group.RampUp,
			RampDown:           group.RampDown,
			Stages:             cloneLoadStages(group.Stages),
			Iterations:         cloneIntPtr(iterationOverride),
			RuntimeSettings:    cloneRuntimeSettings(group.RuntimeSettings),
			Assignment:         cloneGeneratorAssignmentSettings(group.Assignment),
			InitStrategy:       group.Initialization,
			runtimeSecretScope: test.ID,
		})
	}
	tr.mu.Lock()
	groups, specs, totalVUsers, err := tr.compileManagedTestGroupsForModeWithDataSourcesLocked(entries, test.RuntimeSettings, nil, test.ScenarioMode)
	tr.mu.Unlock()
	if err != nil {
		return fmt.Errorf("recompile legacy stored groups: %w", err)
	}
	if totalVUsers != test.TotalVUsers {
		return fmt.Errorf("stored test workload changed while recompiling: persisted total %d, restored total %d", test.TotalVUsers, totalVUsers)
	}
	for index := range groups {
		// The recompile used only protected references. Keep no recovered literal
		// overlay in process memory; execution must resolve through the governed
		// project store and therefore fails closed until values are configured.
		groups[index].runtimeSecretValues = nil
		if index < len(specs) {
			specs[index].SecretResolver = tr.secretResolverForProject(specs[index].Script.ProjectID)
		}
	}
	test.RuntimeSettings = protectedRuntimeSettings
	test.Groups = groups
	test.groupSpecs = specs
	test.runtimeSnapshotRepairRequired = true
	return nil
}

func (tr *TestRunner) restoreManagedTestRuntimeAndRepair(test *ManagedTest) error {
	if err := tr.restoreManagedTestRuntime(test); err != nil {
		return err
	}
	if !test.runtimeSnapshotRepairRequired || !tr.holdsExecutionLeadership() {
		return nil
	}
	return tr.persistManagedTestRuntimeRepair(test)
}

func (tr *TestRunner) persistManagedTestRuntimeRepair(test *ManagedTest) error {
	if tr == nil || test == nil || !test.runtimeSnapshotRepairRequired {
		return nil
	}
	store, ok := tr.testStore.(testRuntimeSnapshotRepairPersistence)
	if !ok {
		return errors.New("test persistence does not support narrow runtime snapshot repair")
	}
	groups := encodeStoredGroups(test.Groups)
	runtimeSettings := runtimeSettingsForStorage(test.RuntimeSettings)
	var (
		repaired bool
		err      error
	)
	if tr.durableExecutionFence {
		token := tr.currentExecutionFenceToken()
		if token == "" {
			return errNotExecutionLeader
		}
		repaired, err = store.RepairTestRuntimeSnapshotIfLeaseHeld(
			test.ID, test.ProjectID, test.MaterializationKey, test.CreatedAt,
			groups, runtimeSettings, executionLeaseName, token,
		)
	} else {
		repaired, err = store.RepairTestRuntimeSnapshot(
			test.ID, test.ProjectID, test.MaterializationKey, test.CreatedAt,
			groups, runtimeSettings,
		)
	}
	if err != nil {
		return fmt.Errorf("persist protected legacy runtime snapshot: %w", err)
	}
	if !repaired {
		if tr.durableExecutionFence && !tr.holdsExecutionLeadership() {
			return errNotExecutionLeader
		}
		return fmt.Errorf("%w: test %s changed before runtime repair", errStoredTestTransitionFenced, test.ID)
	}
	test.runtimeSnapshotRepairRequired = false
	return nil
}

func restoreStoredEngineGroupSpecs(test *ManagedTest) ([]engine.GroupConfig, error) {
	specs := make([]engine.GroupConfig, 0, len(test.Groups))
	for index := range test.Groups {
		group := &test.Groups[index]
		if group.script == nil {
			return nil, fmt.Errorf("group[%d] has no compiled runtime script", index)
		}
		if group.ScriptID != "" && group.script.ID != "" && group.script.ID != group.ScriptID {
			return nil, fmt.Errorf("group[%d] runtime script id %q does not match %q", index, group.script.ID, group.ScriptID)
		}
		if issues := validateStoredExecutionScript(group.script); len(issues) > 0 {
			return nil, fmt.Errorf("group[%d] compiled runtime script is invalid: %s: %w",
				index, strings.Join(issues, "; "), errStaleRuntimeSnapshotValidation)
		}
		rampUp, err := parseOptionalGroupDuration(group.RampUp, index, "ramp_up")
		if err != nil {
			return nil, err
		}
		rampDown, err := parseOptionalGroupDuration(group.RampDown, index, "ramp_down")
		if err != nil {
			return nil, err
		}
		stageDriven := normalizeScenarioMode(test.ScenarioMode) == "vuser_driven" && len(group.Stages) > 0
		initialVUsers := group.VUsers
		engineRampUp := rampUp
		engineRampDown := rampDown
		allowZeroVUsers := false
		if stageDriven {
			initialVUsers = stagedInitialVUsers(group.Stages, group.VUsers)
			engineRampUp = 0
			engineRampDown = 0
			allowZeroVUsers = initialVUsers == 0
		}
		redactionConfig, err := engine.NewRedactionConfig(group.redactionRules)
		if err != nil {
			return nil, fmt.Errorf("group[%d] restore redaction config: %w", index, err)
		}
		_, projectedRuntimeSecrets, err := protectRuntimeOverrideSecrets(test.ID, index, group.script, mergeRuntimeSettings(test.RuntimeSettings, group.RuntimeSettings), false)
		if err != nil {
			return nil, fmt.Errorf("group[%d] restore runtime credentials: %w", index, err)
		}
		referencedRuntimeSecrets, err := filterReferencedRuntimeSecretValues(group.script, projectedRuntimeSecrets)
		if err != nil {
			return nil, fmt.Errorf("group[%d] inspect protected runtime credentials", index)
		}
		if len(projectedRuntimeSecrets) > len(referencedRuntimeSecrets) && runtimeSecretValueAppearsInScript(group.script, projectedRuntimeSecrets) {
			return nil, fmt.Errorf("group[%d]: %w", index, errLegacyRuntimeSecretSnapshot)
		}
		group.runtimeSecretValues = cloneRuntimeSecretValues(referencedRuntimeSecrets)
		redactionConfig, err = engine.WithSecretValueRedactionChecked(redactionConfig, runtimeSecretValueList(referencedRuntimeSecrets))
		if err != nil {
			return nil, fmt.Errorf("group[%d] restore runtime credential redaction: %w", index, err)
		}
		group.rampUpDur = rampUp
		group.stageDriven = stageDriven
		group.initialVUsers = initialVUsers
		group.currentVUsers = initialVUsers
		specs = append(specs, engine.GroupConfig{
			Name:            group.Name,
			Script:          group.script,
			VUsers:          initialVUsers,
			RampUp:          engineRampUp,
			RampDown:        engineRampDown,
			AllowZeroVUsers: allowZeroVUsers,
			InitStrategy:    group.Initialization,
			RedactionConfig: redactionConfig,
		})
	}
	return specs, nil
}

func (tr *TestRunner) loadStoredTestHistory(test *ManagedTest) {
	if tr.historyStore == nil {
		return
	}
	var history []any
	var err error
	if limited, ok := tr.historyStore.(limitedHistoryPersistence); ok {
		history, err = limited.GetHistoryLimit(test.ID, maxManagedTestHistory)
	} else {
		history, err = tr.historyStore.GetHistory(test.ID)
	}
	if err != nil {
		// Best-effort at startup, but never swallow silently: a dialect break
		// here (e.g. a Postgres-incompatible query) would otherwise blank every
		// run's history with no trace.
		slog.Warn("failed to load stored test history", "test_id", test.ID, "error", err)
		return
	}
	for _, entry := range history {
		switch metrics := entry.(type) {
		case storage.IntervalSummaryRecord:
			appendManagedTestHistoryWithLimits(test, intervalSummaryRecordToLiveMetrics(metrics), maxManagedTestHistory, maxManagedTestHistoryBytes)
		case *storage.IntervalSummaryRecord:
			if metrics != nil {
				appendManagedTestHistoryWithLimits(test, intervalSummaryRecordToLiveMetrics(*metrics), maxManagedTestHistory, maxManagedTestHistoryBytes)
			}
		case map[string]any:
			// This is a bit lossy due to any -> map conversion,
			// but works for basic history display.
			appendManagedTestHistoryWithLimits(test, mapToLiveMetrics(metrics), maxManagedTestHistory, maxManagedTestHistoryBytes)
		}
	}
	if len(test.History) > 0 {
		test.LiveMetrics = test.History[len(test.History)-1]
	}
}

func (tr *TestRunner) loadStoredTestArchive(test *ManagedTest) {
	if tr.archiveStore == nil {
		return
	}
	archive, err := tr.archiveStore.GetRunArchive(test.ID)
	if err != nil {
		return
	}
	if !runArchiveRecordAvailable(archive) {
		return
	}
	test.Archive = archive
	tr.mu.Lock()
	tr.archives[test.ID] = archive
	tr.mu.Unlock()
}

func storedScenarioToStorage(stored *StoredScenario) *storage.Scenario {
	if stored == nil {
		return nil
	}
	return &storage.Scenario{
		ID:               stored.ID,
		RefKey:           stored.RefKey,
		Name:             stored.Name,
		Duration:         stored.Duration,
		StartDelay:       stored.StartDelay,
		ScenarioMode:     stored.ScenarioMode,
		ScriptShakeout:   stored.ScriptShakeout,
		ResourceBaseline: stored.ResourceBaseline,
		Assignment:       generatorAssignmentForStorage(stored.Assignment),
		GoalConfig:       goalConfigForStorage(stored.GoalConfig),
		RuntimeSettings:  runtimeSettingsForStorage(stored.RuntimeSettings),
		CreatedAt:        stored.CreatedAt,
		UpdatedAt:        stored.UpdatedAt,
	}
}

func storedScenarioRefsToStorage(stored *StoredScenario) []storage.ScenarioScript {
	if stored == nil || len(stored.Scripts) == 0 {
		return nil
	}
	scripts := make([]storage.ScenarioScript, 0, len(stored.Scripts))
	for _, ref := range stored.Scripts {
		scripts = append(scripts, storage.ScenarioScript{
			ScenarioID:      stored.ID,
			Order:           ref.Order,
			Name:            ref.Name,
			ScriptID:        ref.ScriptID,
			Role:            ref.Role,
			VUsers:          ref.VUsers,
			RampUp:          ref.RampUp,
			RampDown:        ref.RampDown,
			Stages:          cloneLoadStages(ref.Stages),
			Iterations:      ref.Iterations,
			Initialization:  ref.Initialization,
			RuntimeSettings: runtimeSettingsForStorage(ref.RuntimeSettings),
			Assignment:      generatorAssignmentForStorage(ref.Assignment),
		})
	}
	return scripts
}

func cloneScenarioScriptRefs(in []ScenarioScriptRef) []ScenarioScriptRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]ScenarioScriptRef, len(in))
	copy(out, in)
	for i := range out {
		out[i].Stages = cloneLoadStages(in[i].Stages)
		if in[i].Iterations != nil {
			iterations := *in[i].Iterations
			out[i].Iterations = &iterations
		}
		out[i].RuntimeSettings = cloneRuntimeSettings(in[i].RuntimeSettings)
		out[i].Assignment = cloneGeneratorAssignmentSettings(in[i].Assignment)
	}
	return out
}

func mapToLiveMetrics(m map[string]any) *LiveMetrics {
	// Re-marshal to JSON and unmarshal to struct to handle all fields robustly
	data, err := json.Marshal(m)
	if err != nil {
		return &LiveMetrics{}
	}
	var lm LiveMetrics
	if err := json.Unmarshal(data, &lm); err != nil {
		return &LiveMetrics{}
	}
	return &lm
}

func intervalSummaryRecordToLiveMetrics(record storage.IntervalSummaryRecord) *LiveMetrics {
	live := &LiveMetrics{
		Timestamp:       record.Timestamp,
		ActiveVUsers:    record.ActiveVUsers,
		TPS:             record.TPS,
		AvgResponseMs:   record.AvgResponseMs,
		ErrorPct:        record.ErrorPct,
		TotalTx:         record.TotalTx,
		TotalErrors:     record.TotalErrors,
		P50Ms:           record.P50Ms,
		P90Ms:           record.P90Ms,
		P95Ms:           record.P95Ms,
		P99Ms:           record.P99Ms,
		Browser:         browserSummaryRecordToLiveMetrics(record.Browser),
		BrowserByDevice: browserDeviceSummaryRecordToLiveMetrics(record.BrowserByDevice),
		Latency:         networkLatencyRecordToLiveMetrics(record.Latency),
	}
	if record.DroppedResults > 0 || record.WebsocketClients > 0 || record.WebsocketDroppedMessages > 0 || record.WebsocketDroppedEvents > 0 || !record.CollectedAt.IsZero() || !record.GeneratorPublishedAt.IsZero() || !record.ControllerReceivedAt.IsZero() || !record.PublishedAt.IsZero() || record.SampleAgeMs > 0 || record.GeneratorProcessingLagMs > 0 || record.TransportLagMs > 0 || record.ControllerFanoutLagMs > 0 {
		live.Telemetry = &TelemetryHealth{
			DroppedResults:           record.DroppedResults,
			WebsocketClients:         record.WebsocketClients,
			WebsocketDroppedMessages: record.WebsocketDroppedMessages,
			WebsocketDroppedEvents:   record.WebsocketDroppedEvents,
			GeneratorPublishedAt:     record.GeneratorPublishedAt,
			ControllerReceivedAt:     record.ControllerReceivedAt,
			CollectedAt:              record.CollectedAt,
			PublishedAt:              record.PublishedAt,
			GeneratorProcessingLagMs: record.GeneratorProcessingLagMs,
			TransportLagMs:           record.TransportLagMs,
			ControllerFanoutLagMs:    record.ControllerFanoutLagMs,
			SampleAgeMs:              record.SampleAgeMs,
		}
	}
	return live
}

func browserSummaryRecordToLiveMetrics(record *storage.BrowserMetricsSummaryRecord) *BrowserLiveMetrics {
	if record == nil {
		return nil
	}
	return &BrowserLiveMetrics{
		Samples:              record.Samples,
		AvgFCPMs:             record.AvgFCPMs,
		AvgLCPMs:             record.AvgLCPMs,
		AvgCLSScore:          record.AvgCLSScore,
		AvgINPMs:             record.AvgINPMs,
		AvgTTFBMs:            record.AvgTTFBMs,
		AvgBlockingResources: record.AvgBlockingResources,
		AvgLongTaskCount:     record.AvgLongTaskCount,
		AvgJSHeapUsedMB:      record.AvgJSHeapUsedMB,
		AvgBrowserProcessCPU: record.AvgBrowserProcessCPU,
		AvgBrowserProcessMem: record.AvgBrowserProcessMem,
	}
}

func browserDeviceSummaryRecordToLiveMetrics(records map[string]*storage.BrowserDeviceMetricsRecord) map[string]*BrowserDeviceLiveMetrics {
	if len(records) == 0 {
		return nil
	}
	result := make(map[string]*BrowserDeviceLiveMetrics, len(records))
	for name, record := range records {
		if record == nil {
			continue
		}
		result[name] = &BrowserDeviceLiveMetrics{
			DeviceProfile:        record.DeviceProfile,
			DevicePlatform:       record.DevicePlatform,
			DeviceTier:           record.DeviceTier,
			BrowserEngine:        record.BrowserEngine,
			Samples:              record.Samples,
			AvgFCPMs:             record.AvgFCPMs,
			AvgLCPMs:             record.AvgLCPMs,
			AvgCLSScore:          record.AvgCLSScore,
			AvgINPMs:             record.AvgINPMs,
			AvgTTFBMs:            record.AvgTTFBMs,
			AvgBlockingResources: record.AvgBlockingResources,
			AvgLongTaskCount:     record.AvgLongTaskCount,
			AvgJSHeapUsedMB:      record.AvgJSHeapUsedMB,
			AvgBrowserProcessCPU: record.AvgBrowserProcessCPU,
			AvgBrowserProcessMem: record.AvgBrowserProcessMem,
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func networkLatencyRecordToLiveMetrics(record *storage.NetworkLatencySummaryRecord) *NetworkLatency {
	if record == nil {
		return nil
	}
	latency := &NetworkLatency{Targets: make([]LatencyTarget, 0, len(record.Targets))}
	for _, target := range record.Targets {
		latency.Targets = append(latency.Targets, LatencyTarget{
			Name:           target.Name,
			Endpoint:       target.Endpoint,
			LatencyMs:      target.LatencyMs,
			ProbeTotalMs:   target.ProbeTotalMs,
			DNSMs:          target.DNSMs,
			ConnectMs:      target.ConnectMs,
			TLSMs:          target.TLSMs,
			TTFBMs:         target.TTFBMs,
			HTTPStatus:     target.HTTPStatus,
			ProbeMethod:    target.ProbeMethod,
			IP:             target.IP,
			Hops:           target.Hops,
			LastSuccess:    target.LastSuccess,
			JitterMs:       target.JitterMs,
			SuccessRatePct: target.SuccessRatePct,
			SampleCount:    target.SampleCount,
			RouteStatus:    target.RouteStatus,
			Error:          target.Error,
			Timestamp:      target.Timestamp,
		})
	}
	return latency
}

func (tr *TestRunner) releaseRefLocked(id string) ReleaseRef {
	ref := ReleaseRef{ID: id, Name: id}
	if tr.releaseStore == nil {
		return ref
	}
	release, err := tr.releaseStore.GetRelease(id)
	if err != nil || release == nil {
		return ref
	}
	return ReleaseRef{ID: release.ID, Name: release.Name, ApplicationID: release.ApplicationID}
}

func releaseRefsFromSet(set map[string]ReleaseRef) []ReleaseRef {
	out := make([]ReleaseRef, 0, len(set))
	for _, release := range set {
		out = append(out, release)
	}
	return out
}

func scenarioRefsFromSet(set map[string]ScenarioRef) []ScenarioRef {
	out := make([]ScenarioRef, 0, len(set))
	for _, scenario := range set {
		out = append(out, scenario)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (tr *TestRunner) loadRunArchiveFromStore(id string) (*storage.RunArchiveRecord, bool, error) {
	if tr.archiveStore == nil {
		return nil, false, nil
	}
	record, err := tr.archiveStore.GetRunArchive(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return record, record != nil, nil
}

// forgetProject removes successfully deleted project data from the controller's
// in-memory indexes so deleted assets are not served until the next restart.
func (tr *TestRunner) forgetProject(projectID string) {
	projectID = normalizeProjectID(projectID)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	contentDeleted := false
	for id, script := range tr.scripts {
		if script != nil && normalizeProjectID(script.ProjectID) == projectID {
			tr.removeRefKeyLocked(script.RefKey)
			delete(tr.scripts, id)
			contentDeleted = true
		}
	}
	for id, scenario := range tr.scenarios {
		if scenario != nil && normalizeProjectID(scenario.ProjectID) == projectID {
			tr.removeRefKeyLocked(scenario.RefKey)
			delete(tr.scenarios, id)
			contentDeleted = true
		}
	}
	if contentDeleted {
		tr.contentRevision++
	}
	for id, test := range tr.tests {
		if test != nil && normalizeProjectID(test.ProjectID) == projectID {
			tr.removeRefKeyLocked(test.RefKey)
			tr.forgetRunSnapshotWriteStateLocked(id)
			delete(tr.tests, id)
			delete(tr.archives, id)
			forgetRunArchiveCache(id)
			deleteGroupMetricsHistoryCache(id)
		}
	}
}

func (tr *TestRunner) saveStoredScript(script *dsl.Script) error {
	return tr.saveStoredScriptWithImportTrust(script, nil, false, "")
}

func (tr *TestRunner) saveStoredScriptWithImportTrust(
	script, prior *dsl.Script,
	trustedImport bool,
	sourceProfile string,
) error {
	if tr.scriptStore == nil {
		return nil
	}
	var projected *dsl.Script
	var err error
	if trustedImport {
		projected, err = preparePersistentImportedLibraryScript(script, prior, sourceProfile)
	} else {
		projected, err = preparePersistentLibraryScript(script, prior)
	}
	if err != nil {
		return err
	}
	// Keep the in-memory object aligned with the disclosure-safe form passed to
	// storage. This seam is also used by the compatibility ref-key backfill for
	// lightweight stores that do not implement the atomic ref-key operation.
	*script = *projected
	return tr.scriptStore.SaveScript(script)
}

func (tr *TestRunner) deleteStoredScript(id string) error {
	if tr.scriptStore != nil {
		return tr.scriptStore.DeleteScript(id)
	}
	return nil
}

func buildManagedTestRecord(test *ManagedTest) *storage.TestRecord {
	if test == nil {
		return nil
	}
	return &storage.TestRecord{
		ID:                    test.ID,
		RefKey:                test.RefKey,
		MaterializationKey:    test.MaterializationKey,
		Name:                  test.Name,
		Status:                test.Status,
		ApplicationID:         test.ApplicationID,
		ReleaseID:             test.ReleaseID,
		ScenarioID:            test.ScenarioID,
		ProjectID:             normalizeProjectID(test.ProjectID),
		FolderID:              test.FolderID,
		DataPools:             cloneScenarioDataPoolBindings(test.DataPools),
		ScenarioMode:          test.ScenarioMode,
		ScriptShakeout:        test.ScriptShakeout,
		ResourceBaseline:      test.ResourceBaseline,
		StartDelay:            test.StartDelay,
		Assignment:            test.Assignment,
		TotalVUsers:           test.TotalVUsers,
		Duration:              int64(test.Duration),
		StartTime:             test.CreatedAt,
		ScheduledStartAt:      cloneTimePtr(test.ScheduledStartAt),
		TimeslotID:            cloneStringPtr(test.TimeslotID),
		PendingSince:          test.PendingSince,
		PausedAt:              cloneTimePtr(test.PausedAt),
		CumulativePause:       int64(test.CumulativePause),
		CapacityWindowStartAt: cloneTimePtr(test.CapacityWindowStartAt),
		CapacityWindowEndAt:   cloneTimePtr(test.CapacityWindowEndAt),
		BaselinePinned:        test.BaselinePinned,
		BaselinePinnedAt:      test.BaselinePinnedAt,
		EndTime:               cloneTimePtr(test.EndedAt),
		Error:                 test.Error,
		CreatedBySubject:      test.CreatedBySubject,
		CreatedByDisplayName:  test.CreatedByDisplayName,
		StartedBySubject:      test.StartedBySubject,
		StartedByDisplayName:  test.StartedByDisplayName,
		StoppedBySubject:      test.StoppedBySubject,
		StoppedByDisplayName:  test.StoppedByDisplayName,
		StartAuthorization:    startAuthorizationForStorage(test.StartAuthorization),
		ExecutionPlanApproval: test.ExecutionPlanApproval,
		Groups:                encodeStoredGroups(test.Groups),
		GoalConfig:            test.GoalConfig,
		RuntimeSettings:       test.RuntimeSettings,
	}
}

func (tr *TestRunner) saveManagedTestRecord(test *ManagedTest) error {
	if tr.testStore != nil {
		record := buildManagedTestRecord(test)
		if tr.durableExecutionFence {
			if test == nil {
				return fmt.Errorf("test is required")
			}
			if test.executionFence == "" {
				return fmt.Errorf("%w: test %s has no claimed execution epoch", errNotExecutionLeader, test.ID)
			}
			store, ok := tr.testStore.(interface {
				SaveTestIfLeaseHeld(*storage.TestRecord, string, string) (bool, error)
			})
			if !ok {
				return fmt.Errorf("test persistence does not support execution-lease fencing")
			}
			saved, err := store.SaveTestIfLeaseHeld(record, executionLeaseName, test.executionFence)
			if err != nil {
				return err
			}
			if !saved {
				return fmt.Errorf("%w: test %s belongs to an expired execution epoch", errNotExecutionLeader, test.ID)
			}
			return nil
		}
		return tr.testStore.SaveTest(record)
	}
	return nil
}

func (tr *TestRunner) AttributeTestCreated(id string, subject string, displayName string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousSubject := test.CreatedBySubject
	previousDisplayName := test.CreatedByDisplayName
	test.CreatedBySubject = strings.TrimSpace(subject)
	test.CreatedByDisplayName = strings.TrimSpace(displayName)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.CreatedBySubject = previousSubject
		test.CreatedByDisplayName = previousDisplayName
		slog.Warn("failed to persist test creator attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) AttributeTestStarted(id string, subject string, displayName string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousSubject := test.StartedBySubject
	previousDisplayName := test.StartedByDisplayName
	test.StartedBySubject = strings.TrimSpace(subject)
	test.StartedByDisplayName = strings.TrimSpace(displayName)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.StartedBySubject = previousSubject
		test.StartedByDisplayName = previousDisplayName
		slog.Warn("failed to persist test starter attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) AttributeTestStopped(id string, subject string, displayName string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousSubject := test.StoppedBySubject
	previousDisplayName := test.StoppedByDisplayName
	test.StoppedBySubject = strings.TrimSpace(subject)
	test.StoppedByDisplayName = strings.TrimSpace(displayName)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.StoppedBySubject = previousSubject
		test.StoppedByDisplayName = previousDisplayName
		slog.Warn("failed to persist test stopper attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) AttributeTestExecutionPlanApproval(id string, approval *storage.ExecutionPlanApproval) {
	snapshot := runArchiveExecutionPlanApprovalSnapshot(approval)
	if snapshot == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return
	}
	previousApproval := test.ExecutionPlanApproval
	if snapshot.ApprovedPeakVUsers <= 0 {
		snapshot.ApprovedPeakVUsers = approvedExecutionPlanPeakForTest(test)
	}
	test.ExecutionPlanApproval = snapshot
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.ExecutionPlanApproval = previousApproval
		slog.Warn("failed to persist execution-plan approval attribution", "test_id", id, "error", err)
	}
}

func (tr *TestRunner) deleteStoredTest(id string) (storage.LeaseFencedDeleteResult, error) {
	if tr.testStore == nil {
		return storage.LeaseFencedDeleteDeleted, nil
	}
	if tr.durableExecutionFence {
		leaseOwnerID := tr.currentExecutionFenceToken()
		if leaseOwnerID == "" {
			return storage.LeaseFencedDeleteFenced, errNotExecutionLeader
		}
		store, ok := tr.testStore.(leaseFencedTestDeletePersistence)
		if !ok {
			return storage.LeaseFencedDeleteFenced, fmt.Errorf("test persistence does not support execution-lease-fenced deletion")
		}
		result, err := store.DeleteTestIfLeaseHeld(id, executionLeaseName, leaseOwnerID)
		if err != nil {
			return result, err
		}
		if result == storage.LeaseFencedDeleteFenced {
			return result, errNotExecutionLeader
		}
		return result, nil
	}
	if err := tr.testStore.DeleteTest(id); err != nil {
		return storage.LeaseFencedDeleteFenced, err
	}
	return storage.LeaseFencedDeleteDeleted, nil
}

func (tr *TestRunner) saveLiveMetricsHistory(testID string, timestamp time.Time, live *LiveMetrics) error {
	if tr.historyStore != nil {
		return tr.historyStore.SaveHistory(testID, timestamp, live)
	}
	return nil
}

func (tr *TestRunner) updateStoredTestStatus(testID string, status string, endTime *time.Time, errStr string) error {
	executionFence := ""
	if tr.durableExecutionFence {
		tr.mu.RLock()
		if test := tr.tests[testID]; test != nil {
			executionFence = test.executionFence
		}
		tr.mu.RUnlock()
	}
	if err := tr.persistStoredTestStatus(testID, executionFence, status, endTime, errStr); err != nil {
		return err
	}
	// A terminal status is visible to admission only after the durable test row
	// commits. Releasing the reservation first would let new work consume capacity
	// while a failover controller still sees the run as active.
	if err := tr.releaseCapacityReservationForTerminalStatus(testID, status); err != nil {
		tr.mu.RLock()
		slotID := ""
		if test := tr.tests[testID]; test != nil && test.TimeslotID != nil {
			slotID = strings.TrimSpace(*test.TimeslotID)
		}
		tr.mu.RUnlock()
		tr.enqueueCapacityReservationReleaseRetry(testID, slotID)
	}
	return nil
}

// persistStoredTestStatus writes only the test transition. Callers that hold
// tr.mu use this helper and release secondary resources after unlocking, avoiding
// the recursive tr.mu read taken by releaseCapacityReservationForTerminalStatus.
func (tr *TestRunner) persistStoredTestStatus(testID string, executionFence string, status string, endTime *time.Time, errStr string) error {
	if tr.testStore == nil {
		return nil
	}
	if tr.durableExecutionFence {
		if strings.TrimSpace(executionFence) == "" {
			return errNotExecutionLeader
		}
		store, ok := tr.testStore.(interface {
			UpdateTestStatusIfActiveAndLeaseHeld(string, string, *time.Time, string, string, string) (storage.TestStatusTransitionResult, error)
		})
		if !ok {
			return fmt.Errorf("test persistence does not support execution-lease-fenced lifecycle transitions")
		}
		var result storage.TestStatusTransitionResult
		if err := retryDurableTransition(func() error {
			var err error
			result, err = store.UpdateTestStatusIfActiveAndLeaseHeld(testID, status, endTime, errStr, executionLeaseName, executionFence)
			return err
		}); err != nil {
			return err
		}
		switch result {
		case storage.TestStatusTransitionUpdated, storage.TestStatusTransitionUnchanged:
			return nil
		case storage.TestStatusTransitionMissing:
			return fmt.Errorf("%w: %s", errStoredTestMissing, testID)
		default:
			return fmt.Errorf("%w: test %s belongs to an expired execution epoch", errNotExecutionLeader, testID)
		}
	}
	if store, ok := tr.testStore.(interface {
		UpdateTestStatusIfActive(string, string, *time.Time, string) (storage.TestStatusTransitionResult, error)
	}); ok {
		var result storage.TestStatusTransitionResult
		if err := retryDurableTransition(func() error {
			var err error
			result, err = store.UpdateTestStatusIfActive(testID, status, endTime, errStr)
			return err
		}); err != nil {
			return err
		}
		switch result {
		case storage.TestStatusTransitionUpdated, storage.TestStatusTransitionUnchanged:
			return nil
		case storage.TestStatusTransitionMissing:
			return fmt.Errorf("%w: %s", errStoredTestMissing, testID)
		default:
			return fmt.Errorf("%w: test %s has a newer state", errStoredTestTransitionFenced, testID)
		}
	}
	return retryDurableTransition(func() error {
		return tr.testStore.UpdateTestStatus(testID, status, endTime, errStr)
	})
}

func (tr *TestRunner) saveRunArchiveRecord(record *storage.RunArchiveRecord) error {
	if tr.archiveStore == nil || record == nil {
		return nil
	}
	return tr.archiveStore.SaveRunArchive(record)
}

func (tr *TestRunner) captureUsageSnapshotForTerminalRun(testID string, projectID string) {
	if tr == nil || tr.usageStore == nil {
		return
	}
	projectID = normalizeProjectID(projectID)
	if _, err := tr.captureUsageSnapshotForCadence(projectID); err != nil {
		slog.Warn("failed to save usage accounting snapshot", "test_id", testID, "project_id", projectID, "error", err)
	}
}

func (tr *TestRunner) captureUsageSnapshotForCadence(projectID string) (*storage.UsageAccountingSnapshot, error) {
	if tr == nil || tr.usageStore == nil {
		return nil, nil
	}
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	tr.mu.RLock()
	tests := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		tests = append(tests, cloneManagedTestForReadLocked(test))
	}
	tr.mu.RUnlock()
	return tr.saveUsageAccountingSnapshotFromTests(projectID, tests)
}

func (tr *TestRunner) saveUsageAccountingSnapshotFromTests(projectID string, tests []*ManagedTest) (*storage.UsageAccountingSnapshot, error) {
	usage := usageAccountingFromTests(projectID, tests)
	payload, err := json.Marshal(usage)
	if err != nil {
		return nil, fmt.Errorf("encode usage accounting snapshot: %w", err)
	}
	now := time.Now().UTC()
	snapshot := &storage.UsageAccountingSnapshot{
		ID:          uuid.NewString(),
		ProjectID:   projectID,
		GeneratedAt: usage.GeneratedAt,
		PayloadJSON: string(payload),
		CreatedAt:   now,
	}
	if err := tr.usageStore.SaveUsageAccountingSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (tr *TestRunner) UsageAccountingSnapshotInterval() time.Duration {
	if tr == nil {
		return 0
	}
	seconds := tr.GetSystemSettings().General.UsageSnapshotIntervalSeconds
	if seconds <= 0 {
		return 0
	}
	interval, ok := checkedDuration(seconds, time.Second)
	if !ok {
		return 0
	}
	return interval
}

const (
	usageSnapshotLookupPageSize = 500
	maxUsageSnapshotLookupPages = 20
)

func (tr *TestRunner) latestGlobalUsageSnapshotTime() (time.Time, error) {
	if tr == nil || tr.usageStore == nil {
		return time.Time{}, nil
	}
	// The storage API treats an empty project filter as all projects. Walk newest
	// pages until the first global cadence row is found. Bound the fallback scan:
	// a compatibility store that ignores offset (or a database containing only a
	// very large project history) must not trap leadership acquisition in an
	// infinite/full-table walk. If the marker is older than the scan window the
	// cadence safely treats it as absent and writes a fresh global marker.
	for page := 0; page < maxUsageSnapshotLookupPages; page++ {
		offset := page * usageSnapshotLookupPageSize
		snapshots, err := tr.usageStore.ListUsageAccountingSnapshots("", usageSnapshotLookupPageSize, offset)
		if err != nil {
			return time.Time{}, err
		}
		for _, snapshot := range snapshots {
			if snapshot != nil && strings.TrimSpace(snapshot.ProjectID) == "" {
				return snapshot.GeneratedAt.UTC(), nil
			}
		}
		if len(snapshots) < usageSnapshotLookupPageSize {
			return time.Time{}, nil
		}
	}
	return time.Time{}, nil
}

func (tr *TestRunner) RunUsageAccountingSnapshotCadence(ctx context.Context, ticks <-chan time.Time) {
	if tr == nil || ticks == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if _, err := tr.captureUsageSnapshotForCadence(""); err != nil {
				slog.Warn("failed to save cadence usage accounting snapshot", "error", err)
			}
		}
	}
}
