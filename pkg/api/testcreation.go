package api

// The build-a-test pipeline: CreateTest and draft-capacity estimation,
// scenario-request derivation/validation, group normalization + compilation,
// shared data-source/CSV assembly, load-stage/duration parsing, name
// derivation, and the project-ID/runtime-settings/redaction resolvers.

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (req *CreateTestRequest) UnmarshalJSON(data []byte) error {
	type createTestRequestAlias CreateTestRequest
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var decoded createTestRequestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*req = CreateTestRequest(decoded)
	_, req.scriptShakeoutProvided = raw["script_shakeout"]
	_, req.resourceBaselineProvided = raw["resource_baseline"]
	return nil
}

func (tr *TestRunner) EstimateDraftCapacity(req CreateTestRequest) (*DraftCapacityEstimate, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	preview, err := tr.buildManagedTestLocked(req, "draft-estimate", "DRAFT")
	if err != nil {
		return nil, err
	}

	return &DraftCapacityEstimate{
		ScenarioMode:          preview.ScenarioMode,
		TotalVUsers:           preview.TotalVUsers,
		Duration:              preview.DurationStr,
		ResourceBaseline:      preview.ResourceBaseline,
		Groups:                preview.Groups,
		Assignment:            cloneGeneratorAssignmentSettings(preview.Assignment),
		Footprint:             buildScenarioFootprint(preview),
		Recommendation:        tr.buildAssignmentRecommendation(preview),
		CapacityAdvisory:      cloneCapacityAdvisory(preview.CapacityAdvisory),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(preview.RuntimeSettingsReview),
	}, nil
}

func (tr *TestRunner) CreateTest(req CreateTestRequest) (*ManagedTest, error) {
	return tr.createTest(req, true)
}

// createTest can defer the externally visible creation event until a containing
// execution-plan transition is durable. Ordinary API creation broadcasts
// immediately; plan materialization passes false and publishes only after its
// run entry (including the generated test ID) commits.
func (tr *TestRunner) createTest(req CreateTestRequest, broadcast bool) (*ManagedTest, error) {
	// Tests must be created on the execution leader so the in-memory queue stays
	// coherent with the replica that will actually run them.
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()

	now := time.Now()
	status := "QUEUED"
	if req.ReserveLaunchWindow {
		status = "RESERVED"
		start := now
		if req.ScheduledStartAt != nil && !req.ScheduledStartAt.IsZero() {
			start = *req.ScheduledStartAt
		}
		roundedStart := ceilToNextQuarterHour(start)
		req.ScheduledStartAt = &roundedStart
	} else if req.ScheduledStartAt != nil && req.ScheduledStartAt.After(now) {
		status = "SCHEDULED"
	}
	test, err := tr.buildManagedTestLocked(req, uuid.New().String(), status)
	if err != nil {
		return nil, err
	}
	if status == "RESERVED" && test.ScheduledStartAt != nil {
		window := launchWindowForTest(test, test.ScheduledStartAt.UTC())
		if conflicts := tr.launchWindowConflictsLocked(test, window, now.UTC(), "", ""); len(conflicts) > 0 {
			return nil, fmt.Errorf("launch window conflict: %s", conflicts[0].Message)
		}
	}

	// Strict: when a timeslot is supplied, bind it as part of creation. A bind
	// failure aborts the create so a run is never created unprotected.
	if req.TimeslotID != nil && strings.TrimSpace(*req.TimeslotID) != "" {
		if err := tr.validateAndBindTimeslotLocked(strings.TrimSpace(*req.TimeslotID), test); err != nil {
			return nil, err
		}
	}

	tr.tests[test.ID] = test
	tr.indexRefKeyLocked(test.RefKey, test.ID)

	if err := tr.saveManagedTestRecord(test); err != nil {
		delete(tr.tests, test.ID)
		tr.removeRefKeyLocked(test.RefKey)
		if tr.reservationStore != nil && test.TimeslotID != nil {
			if _, clearErr := tr.reservationStore.ClearCapacityReservationBinding(*test.TimeslotID, test.ID, time.Now().UTC()); clearErr != nil {
				slog.Warn("failed to release test timeslot after persistence failure", "test_id", test.ID, "timeslot_id", *test.TimeslotID, "error", clearErr)
			}
		}
		return nil, fmt.Errorf("persist test: %w", err)
	}

	if broadcast {
		tr.broadcastEvent("test_created", test)
	}
	return cloneManagedTestForReadLocked(test), nil
}

func ceilToNextQuarterHour(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	if value.Minute()%15 == 0 && value.Second() == 0 && value.Nanosecond() == 0 {
		return value
	}
	floorMinute := value.Minute() / 15 * 15
	floor := time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), floorMinute, 0, 0, value.Location())
	return floor.Add(15 * time.Minute)
}

func (tr *TestRunner) buildManagedTestLocked(req CreateTestRequest, id string, status string) (*ManagedTest, error) {
	if req.ScenarioID != "" {
		req.ScenarioID = tr.resolveRefKeyLocked(req.ScenarioID)
		scenario, ok := tr.scenarios[req.ScenarioID]
		if !ok {
			return nil, fmt.Errorf("scenario not found: %s", req.ScenarioID)
		}
		req = createTestRequestFromScenario(req, scenario)
	}

	// Pool-aware assignment: expand a fleet-pool selection into its member targets.
	resolvedAssignment, err := tr.resolvePoolAssignmentLocked(req.Assignment)
	if err != nil {
		return nil, err
	}
	req.Assignment = resolvedAssignment

	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}

	scenarioMode, err := validateScenarioRequest(req)
	if err != nil {
		return nil, err
	}

	groups, err := normalizeCreateTestGroups(req)
	if err != nil {
		return nil, err
	}
	projectID, err := tr.resolveCreateTestProjectID(req.ProjectID, groups)
	if err != nil {
		return nil, err
	}
	if err := tr.validateDataPoolProjectsLocked(projectID, req.DataPools); err != nil {
		return nil, err
	}

	sharedDataSources, err := tr.buildSharedDataSourcesLocked(req.DataPools)
	if err != nil {
		return nil, err
	}

	testGroups, engineGroups, totalVUsers, err := tr.compileManagedTestGroupsForModeWithDataSourcesLocked(groups, req.RuntimeSettings, sharedDataSources, scenarioMode)
	if err != nil {
		return nil, err
	}
	runtimeSettingsReview := buildRuntimeSettingsScenarioReview(testGroups, engineGroups)
	if status != "DRAFT" && runtimeSettingsReview != nil && runtimeSettingsReview.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", runtimeSettingsReview.Summary)
	}

	testName := deriveManagedTestName(req.Name, testGroups)

	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return nil, err
	}
	if err := tr.enforceTestQuotaLocked(projectID, totalVUsers, dur, status); err != nil {
		return nil, err
	}
	effectiveRuntimeSettings, err := tr.resolveEffectiveRuntimeSettings(projectID, req.RuntimeSettings)
	if err != nil {
		return nil, err
	}

	if req.ReleaseID == "" && status != "DRAFT" {
		req.ReleaseID = storage.LegacyReleaseID
	}
	applicationID := req.ApplicationID
	if req.ReleaseID != "" && tr.releaseStore != nil {
		if release, err := tr.releaseStore.GetRelease(req.ReleaseID); err == nil && release != nil {
			applicationID = release.ApplicationID
		}
	}
	if applicationID == "" && req.ReleaseID != "" {
		applicationID = storage.LegacyApplicationID
	}
	now := time.Now()
	refKey := ""
	if status != "DRAFT" {
		sequence, err := tr.nextRunSeqLocked(now)
		if err != nil {
			return nil, fmt.Errorf("allocate run reference key: %w", err)
		}
		refKey = refkey.Run(tr.envCode, now, sequence)
	}
	test := &ManagedTest{
		ID:                    id,
		RefKey:                refKey,
		Name:                  testName,
		Status:                status,
		ApplicationID:         applicationID,
		ReleaseID:             req.ReleaseID,
		ScenarioID:            req.ScenarioID,
		ProjectID:             projectID,
		FolderID:              req.FolderID,
		Groups:                testGroups,
		Assignment:            cloneGeneratorAssignmentSettings(req.Assignment),
		CapacityAdvisory:      tr.estimateCapacityAdvisoryForAssignment(projectID, testGroups, totalVUsers, dur, req.ResourceBaseline, effectiveRuntimeSettings, req.Assignment),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(runtimeSettingsReview),
		ScenarioMode:          scenarioMode,
		ScriptShakeout:        req.ScriptShakeout,
		ResourceBaseline:      req.ResourceBaseline,
		StartDelay:            req.StartDelay,
		GoalConfig:            cloneGoalConfig(req.GoalConfig),
		RuntimeSettings:       cloneRuntimeSettings(effectiveRuntimeSettings),
		TotalVUsers:           totalVUsers,
		Duration:              dur,
		DurationStr:           req.Duration,
		CreatedAt:             now,
		ScheduledStartAt:      cloneTimePtr(req.ScheduledStartAt),
		groupSpecs:            engineGroups,
	}

	if len(testGroups) == 1 {
		test.ScriptID = testGroups[0].ScriptID
		test.ScriptName = testGroups[0].ScriptName
	}
	if status != "DRAFT" {
		if conflict := externalRunnerLaunchConflict(test.Groups); conflict != nil {
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
		if conflicts := tr.javaVuserRunnerLaunchConflictsLocked(test.Groups); len(conflicts) > 0 {
			conflict := conflicts[0]
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
		if conflict := importPromotionLaunchConflict(test.Groups); conflict != nil {
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
	}

	return test, nil
}

func createTestRequestFromScenario(req CreateTestRequest, scenario *StoredScenario) CreateTestRequest {
	req.Name = firstNonEmpty(req.Name, scenario.Name)
	req.Duration = firstNonEmpty(req.Duration, scenario.Duration)
	req.ScenarioMode = firstNonEmpty(req.ScenarioMode, scenario.ScenarioMode)
	if !req.scriptShakeoutProvided && !req.ScriptShakeout {
		req.ScriptShakeout = scenario.ScriptShakeout
	}
	if !req.resourceBaselineProvided && !req.ResourceBaseline {
		req.ResourceBaseline = scenario.ResourceBaseline
	}
	if req.StartDelay == "" {
		req.StartDelay = scenario.StartDelay
	}
	if req.GoalConfig == nil {
		req.GoalConfig = cloneGoalConfig(scenario.GoalConfig)
	}
	if req.RuntimeSettings == nil {
		req.RuntimeSettings = cloneRuntimeSettings(scenario.RuntimeSettings)
	}
	if req.Assignment == nil {
		req.Assignment = cloneGeneratorAssignmentSettings(scenario.Assignment)
	}
	if len(req.DataPools) == 0 {
		req.DataPools = append([]storage.ScenarioDataPoolBinding(nil), scenario.DataPools...)
	}
	if len(req.Groups) == 0 && req.ScriptID == "" {
		req.Groups = make([]CreateGroupEntry, 0, len(scenario.Scripts))
		for _, script := range scenario.Scripts {
			req.Groups = append(req.Groups, CreateGroupEntry{
				Name:            script.Name,
				ScriptID:        script.ScriptID,
				Role:            script.Role,
				VUsers:          script.VUsers,
				RampUp:          script.RampUp,
				RampDown:        script.RampDown,
				Stages:          cloneLoadStages(script.Stages),
				Iterations:      script.Iterations,
				InitStrategy:    script.Initialization,
				RuntimeSettings: cloneRuntimeSettings(script.RuntimeSettings),
				Assignment:      cloneGeneratorAssignmentSettings(script.Assignment),
			})
		}
	}
	return req
}

func validateScenarioRequest(req CreateTestRequest) (string, error) {
	scenarioMode := normalizeScenarioMode(req.ScenarioMode)
	if _, err := normalizeGeneratorAssignmentSettings(req.Assignment); err != nil {
		return "", err
	}
	if req.StartDelay != "" {
		if _, err := time.ParseDuration(req.StartDelay); err != nil {
			return "", fmt.Errorf("invalid start_delay: %w", err)
		}
	}
	if scenarioMode != "goal_oriented" {
		return scenarioMode, nil
	}

	if req.GoalConfig == nil || !hasGoalConfig(req.GoalConfig) {
		return "", fmt.Errorf("goal-oriented scenarios require at least one goal or guardrail setting")
	}
	if req.GoalConfig.P95LatencyMs != nil && *req.GoalConfig.P95LatencyMs <= 0 {
		return "", fmt.Errorf("goal-oriented scenarios require p95_latency_ms to be greater than 0")
	}
	if req.GoalConfig.MaxErrorPct != nil && (*req.GoalConfig.MaxErrorPct < 0 || *req.GoalConfig.MaxErrorPct > 100) {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_pct to be between 0 and 100")
	}
	if req.GoalConfig.MaxAvgServerProcessingMs != nil && *req.GoalConfig.MaxAvgServerProcessingMs <= 0 {
		return "", fmt.Errorf("goal-oriented scenarios require max_avg_server_processing_ms to be greater than 0")
	}
	if req.GoalConfig.MaxAvgTransferMs != nil && *req.GoalConfig.MaxAvgTransferMs <= 0 {
		return "", fmt.Errorf("goal-oriented scenarios require max_avg_transfer_ms to be greater than 0")
	}
	if req.GoalConfig.MinVUsers != nil && *req.GoalConfig.MinVUsers < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be greater than 0")
	}
	if req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MaxVUsers < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require max_vusers to be greater than 0")
	}
	if req.GoalConfig.MinVUsers != nil && req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MinVUsers > *req.GoalConfig.MaxVUsers {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be less than or equal to max_vusers")
	}
	if req.GoalConfig.MaxErrorCount != nil && *req.GoalConfig.MaxErrorCount < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_count to be greater than 0")
	}
	if req.GoalConfig.SLABreachWindow != "" {
		if _, err := time.ParseDuration(req.GoalConfig.SLABreachWindow); err != nil {
			return "", fmt.Errorf("invalid sla_breach_window: %w", err)
		}
	}
	if req.GoalConfig.StopOnSLABreach && req.GoalConfig.P95LatencyMs == nil && req.GoalConfig.MaxErrorPct == nil && req.GoalConfig.MaxAvgServerProcessingMs == nil && req.GoalConfig.MaxAvgTransferMs == nil {
		return "", fmt.Errorf("goal-oriented scenarios require a latency, error, server processing, or transfer threshold when stop_on_sla_breach is enabled")
	}
	if req.GoalConfig.StopOnMaxErrors && req.GoalConfig.MaxErrorCount == nil {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_count when stop_on_max_errors is enabled")
	}
	return scenarioMode, nil
}

func normalizeCreateTestGroups(req CreateTestRequest) ([]CreateGroupEntry, error) {
	if len(req.Groups) > 0 {
		return req.Groups, nil
	}
	if req.ScriptID != "" {
		return []CreateGroupEntry{{
			ScriptID: req.ScriptID,
			VUsers:   req.VUsers,
			RampUp:   req.RampUp,
		}}, nil
	}
	return nil, fmt.Errorf("at least one group (or script_id) is required")
}

func (tr *TestRunner) compileManagedTestGroupsLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride) ([]TestGroup, []engine.GroupConfig, int, error) {
	return tr.compileManagedTestGroupsForModeWithDataSourcesLocked(groups, runtimeSettings, nil, "vuser_driven")
}

func (tr *TestRunner) compileManagedTestGroupsWithDataSourcesLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) ([]TestGroup, []engine.GroupConfig, int, error) {
	return tr.compileManagedTestGroupsForModeWithDataSourcesLocked(groups, runtimeSettings, sharedDataSources, "vuser_driven")
}

func (tr *TestRunner) compileManagedTestGroupsForModeWithDataSourcesLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource, scenarioMode string) ([]TestGroup, []engine.GroupConfig, int, error) {
	testGroups := make([]TestGroup, 0, len(groups))
	engineGroups := make([]engine.GroupConfig, 0, len(groups))
	totalVUsers := 0

	for i, group := range groups {
		testGroup, engineGroup, err := tr.compileManagedTestGroupForModeLocked(i, len(groups), group, runtimeSettings, sharedDataSources, scenarioMode)
		if err != nil {
			return nil, nil, 0, err
		}
		testGroups = append(testGroups, testGroup)
		engineGroups = append(engineGroups, engineGroup)
		totalVUsers += testGroup.VUsers
	}

	return testGroups, engineGroups, totalVUsers, nil
}

func (tr *TestRunner) compileManagedTestGroupLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) (TestGroup, engine.GroupConfig, error) {
	return tr.compileManagedTestGroupForModeLocked(index, totalGroups, group, runtimeSettings, sharedDataSources, "vuser_driven")
}

func (tr *TestRunner) compileManagedTestGroupForModeLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource, scenarioMode string) (TestGroup, engine.GroupConfig, error) {
	script, ok := tr.scripts[group.ScriptID]
	if !ok {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script not found: %s", index, group.ScriptID)
	}

	role, err := normalizeGroupRole(group.Role, script.Protocol, index)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}

	vusers := group.VUsers
	derivedRampUp, derivedRampDown, derivedVUsers, err := loadStageCompatibility(group.Stages, index)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	if vusers <= 0 && derivedVUsers > 0 {
		vusers = derivedVUsers
	}
	if vusers <= 0 {
		vusers = 10
		if role == GroupRoleObserver {
			vusers = observerDefaultVUsers
		}
	}
	if role == GroupRoleObserver && vusers > observerMaxVUsers {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf(
			"group[%d]: observer groups are diagnostic experience canaries capped at %d VUsers (got %d); use a load-role group for browser load",
			index, observerMaxVUsers, vusers)
	}
	rampUpValue := firstNonEmpty(group.RampUp, derivedRampUp)
	rampDownValue := firstNonEmpty(group.RampDown, derivedRampDown)
	stageDriven := normalizeScenarioMode(scenarioMode) == "vuser_driven" && len(group.Stages) > 0
	if len(group.Stages) > 0 && derivedVUsers > 0 && vusers != derivedVUsers {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: vusers must match the maximum target_vusers from stages (%d)", index, derivedVUsers)
	}
	if group.Iterations != nil && *group.Iterations < 1 {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: iterations must be greater than 0", index)
	}
	if group.RuntimeSettings != nil && group.RuntimeSettings.Iterations != nil && *group.RuntimeSettings.Iterations < 1 {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: runtime_settings.iterations must be greater than 0", index)
	}
	assignment, err := normalizeGroupGeneratorAssignmentSettings(group.Assignment)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %w", index, err)
	}

	rampUp, err := parseOptionalGroupDuration(rampUpValue, index, "ramp_up")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	rampDown, err := parseOptionalGroupDuration(rampDownValue, index, "ramp_down")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	engineVUsers := vusers
	engineRampUp := rampUp
	engineRampDown := rampDown
	allowZeroVUsers := false
	initialVUsers := vusers
	if stageDriven {
		initialVUsers = stagedInitialVUsers(group.Stages, vusers)
		engineVUsers = initialVUsers
		engineRampUp = 0
		engineRampDown = 0
		allowZeroVUsers = initialVUsers == 0
	}

	name := group.Name
	if name == "" {
		name = script.Name
		if totalGroups > 1 {
			name = fmt.Sprintf("%s (#%d)", script.Name, index+1)
		}
	}

	initStrategy := strings.ToLower(strings.TrimSpace(group.InitStrategy))
	if initStrategy == "" {
		initStrategy = "at_runtime"
	}
	switch initStrategy {
	case "at_runtime", "at_once":
	default:
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: initialization must be at_runtime or at_once", index)
	}

	effectiveRuntimeSettings := mergeRuntimeSettings(runtimeSettings, group.RuntimeSettings)
	if group.Iterations != nil && (group.RuntimeSettings == nil || group.RuntimeSettings.Iterations == nil) {
		if effectiveRuntimeSettings == nil {
			effectiveRuntimeSettings = &RuntimeSettingsOverride{}
		}
		iterations := *group.Iterations
		effectiveRuntimeSettings.Iterations = &iterations
	}
	effectiveIterations := group.Iterations
	if effectiveRuntimeSettings != nil && effectiveRuntimeSettings.Iterations != nil {
		iterations := *effectiveRuntimeSettings.Iterations
		effectiveIterations = &iterations
	}

	testGroup := TestGroup{
		Name:            name,
		ScriptID:        group.ScriptID,
		ScriptName:      script.Name,
		Protocol:        script.Protocol,
		Role:            role,
		VUsers:          vusers,
		RampUp:          rampUpValue,
		RampDown:        rampDownValue,
		Stages:          cloneLoadStages(group.Stages),
		Iterations:      effectiveIterations,
		RuntimeSettings: cloneRuntimeSettings(group.RuntimeSettings),
		Assignment:      cloneGeneratorAssignmentSettings(assignment),
		RunLogicGoals:   runLogicGoalSnapshot(script.Script),
		Initialization:  initStrategy,
		rampUpDur:       engineRampUp,
		stageDriven:     stageDriven,
		initialVUsers:   initialVUsers,
		currentVUsers:   initialVUsers,
	}

	scriptCopy := *script.Script
	// Runtime HAR capture and retention must use the same centrally managed
	// directory. Override any script-supplied path before local or distributed
	// execution so scripts cannot select arbitrary filesystem locations.
	scriptCopy.Config.Browser.HARArtifactDir = strings.TrimSpace(tr.settings.ArchiveRetention.HARArtifactDir)
	if len(sharedDataSources) > 0 {
		scriptCopy.DataSources = append(cloneDataSources(scriptCopy.DataSources), sharedDataSources...)
	}
	if effectiveRuntimeSettings != nil {
		if err := applyRuntimeOverrides(&scriptCopy, effectiveRuntimeSettings); err != nil {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: apply runtime overrides: %w", index, err)
		}
		if issues := dsl.ValidateScriptIssues(&scriptCopy); len(issues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
	}
	// Resolve object-repository bindings into concrete selectors before the
	// script is distributed — generators have no store, so an unresolved
	// object_ref would be unrunnable on the generator.
	if resolved, refWarnings, refIssues := resolveObjectRefs(tr.objectRepoStore, &scriptCopy); resolved != nil {
		if len(refIssues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(refIssues, "; "))
		}
		for _, w := range refWarnings {
			slog.Warn("object_ref resolution degraded to inline locator", "group", index, "detail", w)
		}
		scriptCopy = *resolved
	}
	if len(scriptCopy.Actions) == 0 {
		if issues := dsl.ValidateScriptIssues(&scriptCopy); len(issues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script must have at least one action", index)
	}
	effectiveRuntimeSettings, err = tr.resolveEffectiveRuntimeSettings(script.ProjectID, effectiveRuntimeSettings)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: resolve redaction rules: %w", index, err)
	}
	redactionConfig, err := buildRuntimeRedactionConfig(effectiveRuntimeSettings)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: build redaction config: %w", index, err)
	}
	testGroup.script = &scriptCopy
	testGroup.redactionRules = cloneRedactionRules(nilIfNoRules(effectiveRuntimeSettings))

	engineGroup := engine.GroupConfig{
		Name:            name,
		Script:          &scriptCopy,
		VUsers:          engineVUsers,
		RampUp:          engineRampUp,
		RampDown:        engineRampDown,
		AllowZeroVUsers: allowZeroVUsers,
		InitStrategy:    initStrategy,
		RedactionConfig: redactionConfig,
	}

	return testGroup, engineGroup, nil
}

func (tr *TestRunner) buildSharedDataSourcesLocked(bindings []storage.ScenarioDataPoolBinding) ([]dsl.DataSource, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if tr.dataPoolStore == nil {
		return nil, fmt.Errorf("data pool storage is not configured")
	}
	sources := make([]dsl.DataSource, 0, len(bindings))
	for _, binding := range bindings {
		alias := strings.TrimSpace(binding.Alias)
		if alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		rows, err := tr.dataPoolStore.ListDataPoolRows(binding.PoolID)
		if err != nil {
			return nil, fmt.Errorf("load data pool %q: %w", alias, err)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("data pool %q has no rows", alias)
		}
		inlineData, columns, err := inlineCSVFromDataPoolRows(rows)
		if err != nil {
			return nil, fmt.Errorf("prepare data pool %q: %w", alias, err)
		}
		strategy := binding.SelectionStrategy
		if strategy == "" {
			strategy = "sequential"
		}
		sources = append(sources, dsl.DataSource{
			Name:           alias,
			Type:           "csv",
			InlineData:     inlineData,
			Strategy:       strategy,
			FirstRowHeader: true,
			Columns:        columns,
		})
	}
	return sources, nil
}

// validateDataPoolProjectsLocked prevents a binding from turning a project-
// scoped scenario or run into a read primitive for another project's rows.
// Callers hold tr.mu, so validation and compilation observe one controller
// snapshot; the database ownership check remains authoritative.
func (tr *TestRunner) validateDataPoolProjectsLocked(projectID string, bindings []storage.ScenarioDataPoolBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	if tr.dataPoolStore == nil {
		return fmt.Errorf("data pool storage is not configured")
	}
	expectedProjectID := normalizeProjectID(projectID)
	for _, binding := range bindings {
		poolID := strings.TrimSpace(binding.PoolID)
		if poolID == "" {
			return fmt.Errorf("data pool id is required")
		}
		pool, err := tr.dataPoolStore.GetDataPool(poolID)
		if err != nil || pool == nil {
			if err != nil {
				return fmt.Errorf("load data pool %q: %w", poolID, err)
			}
			return fmt.Errorf("data pool %q not found", poolID)
		}
		if normalizeProjectID(pool.ProjectID) != expectedProjectID {
			return fmt.Errorf("data pool %q does not belong to project %q", poolID, expectedProjectID)
		}
	}
	return nil
}

func inlineCSVFromDataPoolRows(rows []*storage.SharedDataPoolRow) (string, []string, error) {
	columnSet := map[string]struct{}{}
	for _, row := range rows {
		for key := range row.Data {
			key = strings.TrimSpace(key)
			if key != "" {
				columnSet[key] = struct{}{}
			}
		}
	}
	if len(columnSet) == 0 {
		return "", nil, fmt.Errorf("data pool rows must include at least one column")
	}
	columns := make([]string, 0, len(columnSet))
	for key := range columnSet {
		columns = append(columns, key)
	}
	sort.Strings(columns)

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(columns); err != nil {
		return "", nil, err
	}
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, column := range columns {
			values[i] = row.Data[column]
		}
		if err := writer.Write(values); err != nil {
			return "", nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", nil, err
	}
	return buf.String(), columns, nil
}

func cloneDataSources(sources []dsl.DataSource) []dsl.DataSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]dsl.DataSource, len(sources))
	copy(out, sources)
	return out
}

func parseOptionalGroupDuration(value string, index int, fieldName string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("group[%d]: invalid %s: %w", index, fieldName, err)
	}
	return duration, nil
}

func loadStageCompatibility(stages []storage.LoadStage, groupIndex int) (string, string, int, error) {
	if len(stages) == 0 {
		return "", "", 0, nil
	}
	var rampUp time.Duration
	var rampDown time.Duration
	maxVUsers := 0
	for index, stage := range stages {
		stageType := strings.ToLower(strings.TrimSpace(stage.Type))
		if stageType == "" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: type is required", groupIndex, index)
		}
		if stageType != "ramp_up" && stageType != "hold" && stageType != "ramp_down" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: type must be ramp_up, hold, or ramp_down", groupIndex, index)
		}
		duration, err := time.ParseDuration(stage.Duration)
		if err != nil {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: duration is invalid: %w", groupIndex, index, err)
		}
		if duration <= 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: duration must be greater than zero", groupIndex, index)
		}
		if stage.TargetVUsers < 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_vusers must be zero or greater", groupIndex, index)
		}
		if stage.TargetTPS != nil && *stage.TargetTPS <= 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_tps must be greater than zero", groupIndex, index)
		}
		if stage.TargetVUsers == 0 && stage.TargetTPS == nil && stageType != "ramp_down" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_vusers or target_tps is required", groupIndex, index)
		}
		if stage.TargetVUsers > maxVUsers {
			maxVUsers = stage.TargetVUsers
		}
		switch stageType {
		case "ramp_up":
			rampUp += duration
		case "ramp_down":
			rampDown += duration
		}
	}
	return durationStringOrEmpty(rampUp), durationStringOrEmpty(rampDown), maxVUsers, nil
}

func stagedInitialVUsers(stages []storage.LoadStage, peakVUsers int) int {
	if len(stages) == 0 {
		return peakVUsers
	}
	first := stages[0]
	switch strings.ToLower(strings.TrimSpace(first.Type)) {
	case "ramp_up":
		return 0
	case "hold":
		return first.TargetVUsers
	case "ramp_down":
		if peakVUsers > first.TargetVUsers {
			return peakVUsers
		}
		return first.TargetVUsers
	default:
		return peakVUsers
	}
}

func durationStringOrEmpty(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}

func cloneLoadStages(stages []storage.LoadStage) []storage.LoadStage {
	if len(stages) == 0 {
		return nil
	}
	out := make([]storage.LoadStage, len(stages))
	copy(out, stages)
	for index := range out {
		if stages[index].TargetTPS != nil {
			value := *stages[index].TargetTPS
			out[index].TargetTPS = &value
		}
	}
	return out
}

func deriveManagedTestName(requestName string, groups []TestGroup) string {
	if requestName != "" {
		return requestName
	}
	if len(groups) == 1 {
		return groups[0].ScriptName
	}
	return fmt.Sprintf("Multi-group (%d groups)", len(groups))
}

func (tr *TestRunner) resolveCreateTestProjectID(requestProjectID string, groups []CreateGroupEntry) (string, error) {
	if requestProjectID != "" {
		return normalizeProjectID(requestProjectID), nil
	}
	projectID := ""
	for _, group := range groups {
		script, ok := tr.scripts[group.ScriptID]
		if !ok {
			continue
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return storage.DefaultProjectID, nil
		}
	}
	return normalizeProjectID(projectID), nil
}

func (tr *TestRunner) resolveEffectiveRuntimeSettings(projectID string, runtimeSettings *RuntimeSettingsOverride) (*RuntimeSettingsOverride, error) {
	effective := cloneRuntimeSettings(runtimeSettings)
	rules, err := tr.resolveEffectiveRedactionRules(projectID, nilIfNoRules(runtimeSettings))
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return effective, nil
	}
	if effective == nil {
		effective = &RuntimeSettingsOverride{}
	}
	effective.RedactionRules = rules
	return effective, nil
}

func (tr *TestRunner) resolveEffectiveRedactionRules(projectID string, explicit []engine.RedactionRule) ([]engine.RedactionRule, error) {
	merged := cloneRedactionRules(explicit)
	if tr.projectStore == nil {
		return merged, nil
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil || len(project.Settings.RedactionRules) == 0 {
		return merged, nil
	}
	merged = append(merged, cloneRedactionRules(project.Settings.RedactionRules)...)
	return merged, nil
}
