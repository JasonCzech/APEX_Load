package api

// Scenario persistence and scheduling: CRUD for stored scenarios plus the
// schedule/occurrence bookkeeping (save, audit, list, health) that drives the
// run calendar. Extracted from runner.go; all methods hang off *TestRunner and
// follow its locking contract ("Locked" suffix = caller holds tr.mu).

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) StoreScenario(req CreateScenarioRequest) (*StoredScenario, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.storeScenarioLocked(uuid.New().String(), req)
}

func (tr *TestRunner) UpdateScenario(id string, req CreateScenarioRequest) (*StoredScenario, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	if _, ok := tr.scenarios[id]; !ok {
		return nil, false, nil
	}
	scenario, err := tr.storeScenarioLocked(id, req)
	return scenario, true, err
}

func (tr *TestRunner) storeScenarioLocked(id string, req CreateScenarioRequest) (*StoredScenario, error) {
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
	req.RuntimeSettings = reconcileGroupRuntimeSecrets(req.RuntimeSettings, existingRootRuntime(existing))
	for i := range req.Groups {
		req.Groups[i].RuntimeSettings = reconcileGroupRuntimeSecrets(req.Groups[i].RuntimeSettings, existingGroupRuntime(existing, req.Groups[i]))
	}
	if _, err := validateScenarioRequest(CreateTestRequest{
		ScenarioMode: req.ScenarioMode,
		Assignment:   req.Assignment,
		GoalConfig:   req.GoalConfig,
	}); err != nil {
		return nil, err
	}
	groups, err := normalizeCreateTestGroups(CreateTestRequest{Groups: req.Groups})
	if err != nil {
		return nil, err
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
		refKey = refkey.Scenario(tr.envCode, tr.nextScenarioSeqLocked())
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
	dataPools, err := normalizeScenarioDataPoolBindings(id, req.DataPools)
	if err != nil {
		return nil, err
	}
	if tr.scenarioStore != nil {
		if err := tr.scenarioStore.SaveScenario(scenario, scripts); err != nil {
			return nil, err
		}
		if err := tr.scenarioStore.SaveScenarioDataPoolBindings(id, dataPools); err != nil {
			return nil, err
		}
	}
	stored := tr.buildStoredScenario(scenario, scripts)
	stored.DataPools = dataPools
	tr.scenarios[id] = stored
	tr.indexRefKeyLocked(stored.RefKey, stored.ID)
	return stored, nil
}

func normalizeScenarioDataPoolBindings(scenarioID string, bindings []storage.ScenarioDataPoolBinding) ([]storage.ScenarioDataPoolBinding, error) {
	out := make([]storage.ScenarioDataPoolBinding, 0, len(bindings))
	seenAliases := map[string]struct{}{}
	now := time.Now()
	for _, binding := range bindings {
		binding.ScenarioID = scenarioID
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
		if _, ok := seenAliases[aliasKey]; ok {
			return nil, fmt.Errorf("duplicate data pool binding alias %q", binding.Alias)
		}
		seenAliases[aliasKey] = struct{}{}
		if binding.ID == "" {
			binding.ID = uuid.New().String()
		}
		if binding.AccessMode == "" {
			binding.AccessMode = "read_only"
		}
		switch binding.AccessMode {
		case "read_only", "read_write", "consume":
		default:
			return nil, fmt.Errorf("unsupported data pool access mode %q", binding.AccessMode)
		}
		if binding.SelectionStrategy == "" {
			binding.SelectionStrategy = "sequential"
		}
		switch binding.SelectionStrategy {
		case "sequential", "random", "unique", "pop":
		default:
			return nil, fmt.Errorf("unsupported data pool selection strategy %q", binding.SelectionStrategy)
		}
		if binding.CreatedAt.IsZero() {
			binding.CreatedAt = now
		}
		binding.UpdatedAt = now
		out = append(out, binding)
	}
	return out, nil
}

func (tr *TestRunner) GetScenario(id string) (*StoredScenario, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	id = tr.resolveRefKeyLocked(id)
	scenario, ok := tr.scenarios[id]
	if !ok {
		return nil, false
	}
	return tr.enrichScenarioSummaryLocked(scenario), true
}

func (tr *TestRunner) ListScenarios() []*StoredScenario {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		out = append(out, tr.enrichScenarioSummaryLocked(scenario))
	}
	return out
}

func (tr *TestRunner) ListScenariosFiltered(projectID string) []*StoredScenario {
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id = tr.resolveRefKeyLocked(id)
	scenario, ok := tr.scenarios[id]
	if !ok {
		return false
	}
	for _, test := range tr.tests {
		if test.ScenarioID == id {
			return false
		}
	}
	tr.removeRefKeyLocked(scenario.RefKey)
	delete(tr.scenarios, id)
	if tr.scenarioStore != nil {
		_ = tr.scenarioStore.DeleteScenario(id)
	}
	return true
}

func (tr *TestRunner) saveScenarioScheduleLocked(schedule *storage.ScenarioSchedule) error {
	if schedule == nil {
		return fmt.Errorf("schedule is required")
	}
	scenario, ok := tr.scenarios[schedule.ScenarioID]
	if !ok {
		return fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
	}
	if tr.scenarioStore != nil {
		if err := tr.scenarioStore.SaveScenarioSchedule(schedule); err != nil {
			return err
		}
	}
	for index, existing := range scenario.Schedules {
		if existing != nil && existing.ID == schedule.ID {
			scenario.Schedules[index] = cloneScenarioSchedule(schedule)
			return nil
		}
	}
	scenario.Schedules = append(scenario.Schedules, cloneScenarioSchedule(schedule))
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
	_ = tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
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
	})
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

// ListScenarioScheduleApprovals returns the approval history (pending + resolved)
// for a schedule, scoped to its scenario. ok=false when the scenario or schedule
// is unknown. Mirrors ListScenarioScheduleOccurrences. The approvals come straight
// from a store query (not shared in-memory state), so they are returned as-is.
func (tr *TestRunner) ListScenarioScheduleApprovals(scenarioID string, scheduleID string) ([]*storage.ScenarioScheduleApproval, bool) {
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

// DeleteScenarioScheduleOccurrence removes a single occurrence record for a
// schedule, scoped to its scenario. Returns false (→ 404) when the scenario,
// schedule, or occurrence is unknown or the occurrence belongs to a different
// schedule. Removing a non-terminal (blocked/failed) occurrence lets the
// scheduler re-attempt that slot on the next sweep; removing a terminal one
// prunes history. The schedule's cached LastOccurrence is cleared if it pointed
// at the deleted record.
func (tr *TestRunner) DeleteScenarioScheduleOccurrence(scenarioID string, scheduleID string, occurrenceID string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return false
	}
	var target *storage.ScenarioSchedule
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			target = schedule
			break
		}
	}
	if target == nil || tr.scenarioStore == nil {
		return false
	}
	occurrence, err := tr.scenarioStore.GetScenarioScheduleOccurrence(occurrenceID)
	if err != nil || occurrence == nil || occurrence.ScheduleID != scheduleID {
		return false
	}
	if err := tr.scenarioStore.DeleteScenarioScheduleOccurrence(occurrenceID); err != nil {
		return false
	}
	if target.LastOccurrence != nil && target.LastOccurrence.ID == occurrenceID {
		target.LastOccurrence = nil
	}
	return true
}

func (tr *TestRunner) ScenarioScheduleHealth(now time.Time) ScenarioScheduleHealth {
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
	copy := *scenario
	copy.Scripts = cloneScenarioScriptRefs(scenario.Scripts)
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
	return &copy
}
