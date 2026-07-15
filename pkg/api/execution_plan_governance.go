package api

// Execution-plan run governance. A plan run whose aggregate peak VUsers would
// exceed the project's VUser quota is gated: it requires a project-admin approval
// before it can materialize. Parallel plans count concurrent demand (sum of
// enabled entries); sequential plans count the single largest entry. The gate is
// shared by the manual run handler and the recurring plan scheduler so both
// honor the same quota.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// executionPlanAggregatePeak estimates the plan's peak concurrent VUsers from its
// enabled entries' scenarios (parallel = sum, sequential = max single entry).
func (tr *TestRunner) executionPlanAggregatePeak(plan *storage.ExecutionPlan) int {
	peak := 0
	for _, entry := range plan.Entries {
		if !entry.IsEnabled() {
			continue
		}
		scenario, ok := tr.GetScenario(entry.ScenarioID)
		if !ok {
			continue
		}
		entryVUsers := scenarioPeakVUsers(scenario)
		if plan.RunMode == "sequential" {
			if entryVUsers > peak {
				peak = entryVUsers
			}
		} else {
			peak = saturatingAddNonNegative(peak, entryVUsers)
		}
	}
	return peak
}

func scenarioPeakVUsers(scenario *StoredScenario) int {
	if scenario == nil {
		return 0
	}
	peak := 0
	for _, script := range scenario.Scripts {
		groupPeak := script.VUsers
		for _, stage := range script.Stages {
			if stage.TargetVUsers > groupPeak {
				groupPeak = stage.TargetVUsers
			}
		}
		peak = saturatingAddNonNegative(peak, groupPeak)
	}
	if strings.EqualFold(scenario.ScenarioMode, "goal_oriented") && scenario.GoalConfig != nil && scenario.GoalConfig.MaxVUsers != nil && *scenario.GoalConfig.MaxVUsers > peak {
		peak = *scenario.GoalConfig.MaxVUsers
	}
	return peak
}

// executionPlanRunGate decides whether a plan run may proceed. It returns:
//   - (approval, false, nil): proceed; if approval != nil it is either an
//     unconsumed approval the caller must claim or an approval already claimed
//     by the same recoveryRunID.
//   - (approval, true, nil): blocked; approval is the pending approval to surface.
//   - (nil, false, nil): no governance required (under quota / no quota set).
func (tr *TestRunner) executionPlanRunGate(plan *storage.ExecutionPlan, actorSubject string, recoveryRunID ...string) (*storage.ExecutionPlanApproval, bool, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, false, err
	}
	return tr.executionPlanRunGateWithTerm(plan, actorSubject, leaseTerm, recoveryRunID...)
}

func (tr *TestRunner) executionPlanRunGateWithTerm(plan *storage.ExecutionPlan, actorSubject string, leaseTerm executionLeaseTerm, recoveryRunID ...string) (*storage.ExecutionPlanApproval, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, nil
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, false, errNotExecutionLeader
	}
	projectID := normalizeProjectID(plan.ProjectID)
	maxVUsers, err := tr.projectMaxVUsersStrict(projectID)
	if err != nil {
		// Fail closed: a transient project-quota lookup error must not silently
		// disable the governance gate.
		return nil, false, fmt.Errorf("could not resolve project quota for governance: %w", err)
	}
	if maxVUsers <= 0 {
		return nil, false, nil
	}
	peak := tr.executionPlanAggregatePeak(plan)
	if peak <= maxVUsers {
		return nil, false, nil
	}
	contentHash := tr.executionPlanApprovalContentHash(plan)
	desiredRunID := ""
	if len(recoveryRunID) > 0 {
		desiredRunID = strings.TrimSpace(recoveryRunID[0])
	}
	approvals, err := tr.execPlanScheduleStore.ListExecutionPlanApprovals(plan.ID)
	if err != nil {
		return nil, false, err
	}
	var pending *storage.ExecutionPlanApproval
	for _, approval := range approvals {
		if approval.PlanContentHash != contentHash {
			continue
		}
		if approval.Status == "approved" && (approval.RunID == "" || (desiredRunID != "" && approval.RunID == desiredRunID)) {
			// An approval already claimed by this exact preassigned run remains
			// valid when schedule recovery resumes after a crash. It cannot
			// authorize any other run ID.
			return approval, false, nil
		}
		if approval.Status == "pending" && pending == nil {
			pending = approval
		}
	}
	if pending != nil {
		return pending, true, nil
	}
	now := time.Now().UTC()
	approval := &storage.ExecutionPlanApproval{
		ID:              uuid.New().String(),
		PlanID:          plan.ID,
		ProjectID:       projectID,
		Policy:          "quota_burst",
		Status:          "pending",
		RequestedBy:     actorSubject,
		RequestedAt:     now,
		RequiredRole:    RoleProjectAdmin,
		PlanContentHash: contentHash,
		DecisionReason:  fmt.Sprintf("Aggregate peak %d VUsers exceeds project limit %d; project-admin approval required.", peak, maxVUsers),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := tr.saveExecutionPlanApprovalWithTerm(approval, leaseTerm); err != nil {
		return nil, false, err
	}
	return approval, true, nil
}

// ResolveExecutionPlanApproval approves or rejects a pending plan approval. It
// only flips a still-pending row (a lightweight guard against double-resolve).
func (tr *TestRunner) ResolveExecutionPlanApproval(planID, approvalID string, approve bool, resolvedBy, reason string) (*storage.ExecutionPlanApproval, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, nil
	}
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, false, err
	}
	approval, err := tr.execPlanScheduleStore.GetExecutionPlanApproval(approvalID)
	if err != nil {
		return nil, false, err
	}
	if approval == nil || approval.PlanID != planID {
		return nil, false, nil
	}
	if approval.Status != "pending" {
		return approval, true, fmt.Errorf("approval is already %s", approval.Status)
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(planID)
	if err != nil {
		return nil, true, err
	}
	if plan == nil {
		return nil, false, nil
	}
	currentHash := tr.executionPlanApprovalContentHash(plan)
	if approval.PlanContentHash != "" && approval.PlanContentHash != currentHash {
		return approval, true, fmt.Errorf("approval no longer matches current execution plan content")
	}
	now := time.Now().UTC()
	toStatus := "rejected"
	if approve {
		toStatus = "approved"
	}
	// Atomic compare-and-set so two concurrent resolutions cannot both win.
	won, err := tr.resolveExecutionPlanApprovalWithTerm(approval.ID, toStatus, resolvedBy, reason, currentHash, now, leaseTerm)
	if err != nil {
		return nil, true, err
	}
	if !won {
		return approval, true, fmt.Errorf("approval was already resolved concurrently")
	}
	fresh, err := tr.execPlanScheduleStore.GetExecutionPlanApproval(approval.ID)
	if err != nil || fresh == nil {
		approval.Status = toStatus
		approval.ResolvedBy = resolvedBy
		approval.ResolvedAt = &now
		approval.DecisionReason = reason
		approval.UpdatedAt = now
		return approval, true, nil
	}
	return fresh, true, nil
}

func (tr *TestRunner) ListExecutionPlanApprovals(planID string) ([]*storage.ExecutionPlanApproval, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, nil
	}
	return tr.execPlanScheduleStore.ListExecutionPlanApprovals(planID)
}

func (tr *TestRunner) ListExecutionPlanApprovalsPage(planID string, limit, offset int) ([]*storage.ExecutionPlanApproval, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, nil
	}
	if paged, ok := tr.execPlanScheduleStore.(interface {
		ListExecutionPlanApprovalsPage(string, int, int) ([]*storage.ExecutionPlanApproval, error)
	}); ok {
		return paged.ListExecutionPlanApprovalsPage(planID, limit, offset)
	}
	approvals, err := tr.execPlanScheduleStore.ListExecutionPlanApprovals(planID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || offset >= len(approvals) {
		return []*storage.ExecutionPlanApproval{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	end := len(approvals)
	if limit < len(approvals)-offset {
		end = offset + limit
	}
	return approvals[offset:end], nil
}

type executionPlanApprovalFingerprint struct {
	PlanID        string                                  `json:"plan_id"`
	ProjectID     string                                  `json:"project_id"`
	Name          string                                  `json:"name"`
	Description   string                                  `json:"description,omitempty"`
	RunMode       string                                  `json:"run_mode"`
	TimeslotID    string                                  `json:"timeslot_id,omitempty"`
	AggregatePeak int                                     `json:"aggregate_peak_vusers"`
	Entries       []executionPlanApprovalEntryFingerprint `json:"entries"`
}

type executionPlanApprovalEntryFingerprint struct {
	Order            int                               `json:"order"`
	ScenarioID       string                            `json:"scenario_id"`
	ScenarioName     string                            `json:"scenario_name,omitempty"`
	Enabled          bool                              `json:"enabled"`
	FailurePolicy    string                            `json:"failure_policy,omitempty"`
	TimeslotID       string                            `json:"timeslot_id,omitempty"`
	RuntimeOverrides any                               `json:"runtime_overrides,omitempty"`
	DataPools        []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	Scenario         *executionPlanScenarioFingerprint `json:"scenario,omitempty"`
}

type executionPlanScenarioFingerprint struct {
	ID               string                                   `json:"id"`
	ProjectID        string                                   `json:"project_id,omitempty"`
	Name             string                                   `json:"name"`
	Duration         string                                   `json:"duration"`
	StartDelay       string                                   `json:"start_delay,omitempty"`
	ScenarioMode     string                                   `json:"scenario_mode,omitempty"`
	ScriptShakeout   bool                                     `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                                     `json:"resource_baseline,omitempty"`
	GoalConfig       any                                      `json:"goal_config,omitempty"`
	RuntimeSettings  any                                      `json:"runtime_settings,omitempty"`
	Scripts          []executionPlanScenarioScriptFingerprint `json:"scripts"`
	PeakVUsers       int                                      `json:"peak_vusers"`
}

type executionPlanScenarioScriptFingerprint struct {
	Order           int                 `json:"order"`
	ScriptID        string              `json:"script_id"`
	ScriptName      string              `json:"script_name,omitempty"`
	Protocol        string              `json:"protocol,omitempty"`
	Role            string              `json:"role,omitempty"`
	VUsers          int                 `json:"vusers"`
	RampUp          string              `json:"ramp_up,omitempty"`
	RampDown        string              `json:"ramp_down,omitempty"`
	Stages          []storage.LoadStage `json:"stages,omitempty"`
	Iterations      *int                `json:"iterations,omitempty"`
	Initialization  string              `json:"initialization,omitempty"`
	RuntimeSettings any                 `json:"runtime_settings,omitempty"`
	Assignment      any                 `json:"assignment,omitempty"`
}

func (tr *TestRunner) executionPlanApprovalContentHash(plan *storage.ExecutionPlan) string {
	if plan == nil {
		return ""
	}
	entries := make([]storage.ExecutionPlanEntry, len(plan.Entries))
	copy(entries, plan.Entries)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })
	fp := executionPlanApprovalFingerprint{
		PlanID:        strings.TrimSpace(plan.ID),
		ProjectID:     normalizeProjectID(plan.ProjectID),
		Name:          strings.TrimSpace(plan.Name),
		Description:   strings.TrimSpace(plan.Description),
		RunMode:       strings.TrimSpace(plan.RunMode),
		TimeslotID:    trimPtr(plan.TimeslotID),
		AggregatePeak: tr.executionPlanAggregatePeak(plan),
		Entries:       make([]executionPlanApprovalEntryFingerprint, 0, len(entries)),
	}
	for _, entry := range entries {
		entryFP := executionPlanApprovalEntryFingerprint{
			Order:            entry.Order,
			ScenarioID:       strings.TrimSpace(entry.ScenarioID),
			ScenarioName:     strings.TrimSpace(entry.ScenarioName),
			Enabled:          entry.IsEnabled(),
			FailurePolicy:    strings.TrimSpace(entry.FailurePolicy),
			TimeslotID:       trimPtr(entry.TimeslotID),
			RuntimeOverrides: canonicalJSONValue(entry.RuntimeOverrides),
			DataPools:        append([]storage.ScenarioDataPoolBinding(nil), entry.DataPools...),
		}
		if scenario, ok := tr.GetScenario(entry.ScenarioID); ok {
			entryFP.Scenario = executionPlanScenarioApprovalFingerprint(scenario)
		}
		fp.Entries = append(fp.Entries, entryFP)
	}
	data, _ := json.Marshal(fp)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// validateExecutionPlanApprovalMaterialization rechecks the mutable plan and
// scenario graph immediately around test creation. Approved quota bursts are
// tied to that exact content hash; a later scenario edit must never inherit the
// old approval merely because the run snapshotted only its scenario ID.
func (tr *TestRunner) validateExecutionPlanApprovalMaterialization(planID, runID string, approval *storage.ExecutionPlanApproval) error {
	if approval == nil {
		return nil
	}
	if tr.execPlanScheduleStore == nil {
		return fmt.Errorf("execution-plan approval storage is unavailable")
	}
	approvalID := strings.TrimSpace(approval.ID)
	if approvalID == "" {
		return fmt.Errorf("execution-plan approval identity is missing")
	}
	fresh, err := tr.execPlanScheduleStore.GetExecutionPlanApproval(approvalID)
	if err != nil {
		return fmt.Errorf("reload execution-plan approval: %w", err)
	}
	if fresh == nil || fresh.Status != "approved" || strings.TrimSpace(fresh.PlanID) != strings.TrimSpace(planID) || strings.TrimSpace(fresh.PlanContentHash) == "" {
		return fmt.Errorf("execution-plan approval is no longer valid")
	}
	if claimedRunID := strings.TrimSpace(fresh.RunID); claimedRunID != "" && claimedRunID != strings.TrimSpace(runID) {
		return fmt.Errorf("execution-plan approval is claimed by another run")
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(planID)
	if err != nil {
		return fmt.Errorf("reload execution plan for approval validation: %w", err)
	}
	if plan == nil || tr.executionPlanApprovalContentHash(plan) != fresh.PlanContentHash {
		return fmt.Errorf("execution-plan content changed after quota approval")
	}
	return nil
}

func executionPlanScenarioApprovalFingerprint(scenario *StoredScenario) *executionPlanScenarioFingerprint {
	if scenario == nil {
		return nil
	}
	fp := &executionPlanScenarioFingerprint{
		ID:               strings.TrimSpace(scenario.ID),
		ProjectID:        normalizeProjectID(scenario.ProjectID),
		Name:             strings.TrimSpace(scenario.Name),
		Duration:         strings.TrimSpace(scenario.Duration),
		StartDelay:       strings.TrimSpace(scenario.StartDelay),
		ScenarioMode:     strings.TrimSpace(scenario.ScenarioMode),
		ScriptShakeout:   scenario.ScriptShakeout,
		ResourceBaseline: scenario.ResourceBaseline,
		GoalConfig:       canonicalJSONValue(scenario.GoalConfig),
		RuntimeSettings:  canonicalJSONValue(scenario.RuntimeSettings),
		Scripts:          make([]executionPlanScenarioScriptFingerprint, 0, len(scenario.Scripts)),
		PeakVUsers:       scenarioPeakVUsers(scenario),
	}
	scripts := append([]ScenarioScriptRef(nil), scenario.Scripts...)
	sort.SliceStable(scripts, func(i, j int) bool { return scripts[i].Order < scripts[j].Order })
	for _, script := range scripts {
		fp.Scripts = append(fp.Scripts, executionPlanScenarioScriptFingerprint{
			Order:           script.Order,
			ScriptID:        strings.TrimSpace(script.ScriptID),
			ScriptName:      strings.TrimSpace(script.ScriptName),
			Protocol:        strings.TrimSpace(script.Protocol),
			Role:            strings.TrimSpace(script.Role),
			VUsers:          script.VUsers,
			RampUp:          strings.TrimSpace(script.RampUp),
			RampDown:        strings.TrimSpace(script.RampDown),
			Stages:          cloneLoadStages(script.Stages),
			Iterations:      cloneIntPtr(script.Iterations),
			Initialization:  strings.TrimSpace(script.Initialization),
			RuntimeSettings: canonicalJSONValue(script.RuntimeSettings),
			Assignment:      canonicalJSONValue(script.Assignment),
		})
	}
	return fp
}

func canonicalJSONValue(value any) any {
	if value == nil {
		return nil
	}
	var data []byte
	var err error
	switch v := value.(type) {
	case json.RawMessage:
		data = append([]byte(nil), v...)
	case []byte:
		data = append([]byte(nil), v...)
	default:
		data, err = json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}
	}
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return string(data)
	}
	return decoded
}
