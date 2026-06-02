package api

import (
	"fmt"
	"math"
	"strings"
)

// GoalSizingGroupTarget assigns a throughput goal to one script group in a
// baseline run. The group is identified by ScriptID (preferred) or by the
// baseline Group name. Name optionally renames the group in the produced
// scenario draft.
type GoalSizingGroupTarget struct {
	ScriptID  string  `json:"script_id,omitempty"`
	Group     string  `json:"group,omitempty"`
	Name      string  `json:"name,omitempty"`
	TargetTPS float64 `json:"target_tps"`
}

// GoalSizingRequest asks the platform to size a goal-oriented scenario from a
// completed baseline run by applying Little's Law (VUsers = TPS * ART_seconds)
// to each requested group.
type GoalSizingRequest struct {
	BaselineTestID string                  `json:"baseline_test_id"`
	Name           string                  `json:"name,omitempty"`
	Duration       string                  `json:"duration,omitempty"`
	StartDelay     string                  `json:"start_delay,omitempty"`
	Groups         []GoalSizingGroupTarget `json:"groups"`
	MinVUsers      *int                    `json:"min_vusers,omitempty"`
	MaxVUsers      *int                    `json:"max_vusers,omitempty"`
}

// GoalSizingGroupResult records the per-group sizing math for review.
type GoalSizingGroupResult struct {
	Group          string  `json:"group"`
	ScriptID       string  `json:"script_id,omitempty"`
	BaselineARTMs  float64 `json:"baseline_art_ms"`
	TargetTPS      float64 `json:"target_tps"`
	ComputedVUsers int     `json:"computed_vusers"`
	Basis          string  `json:"basis"`
}

// GoalSizingReport is the response: the per-group breakdown, totals, a
// ready-to-save goal-oriented scenario draft, and (best-effort) the load-model
// validation of that draft.
type GoalSizingReport struct {
	BaselineTestID      string                     `json:"baseline_test_id"`
	Groups              []GoalSizingGroupResult    `json:"groups"`
	TotalTargetTPS      float64                    `json:"total_target_tps"`
	TotalVUsers         int                        `json:"total_vusers"`
	ScenarioDraft       CreateScenarioRequest      `json:"scenario_draft"`
	LoadModelValidation *LoadModelValidationReport `json:"load_model_validation,omitempty"`
}

const defaultGoalScenarioDuration = "10m"

// buildGoalSizingReport applies Little's Law to a baseline run report. It is a
// pure function (no store or network access) so the sizing math is unit
// testable in isolation. The caller resolves the archive report and, after
// sizing, attaches load-model validation.
func buildGoalSizingReport(report *RunArchiveReport, req GoalSizingRequest) (*GoalSizingReport, error) {
	if report == nil {
		return nil, fmt.Errorf("baseline run report is unavailable")
	}
	if len(req.Groups) == 0 {
		return nil, fmt.Errorf("at least one group target is required")
	}

	var baselineGroups []TestGroup
	if report.Manifest != nil {
		baselineGroups = report.Manifest.Provenance.Groups
	}
	singleGroupBaseline := len(report.ByGroup) <= 1 && len(baselineGroups) <= 1

	result := &GoalSizingReport{
		BaselineTestID: strings.TrimSpace(req.BaselineTestID),
		Groups:         make([]GoalSizingGroupResult, 0, len(req.Groups)),
	}
	draftGroups := make([]CreateGroupEntry, 0, len(req.Groups))

	for index, target := range req.Groups {
		if target.TargetTPS <= 0 {
			return nil, fmt.Errorf("group %d: target_tps must be greater than 0", index+1)
		}

		groupName, scriptID := resolveBaselineGroup(baselineGroups, target)
		artMs, basis, err := baselineGroupART(report, groupName, singleGroupBaseline)
		if err != nil {
			label := firstNonBlank(groupName, scriptID, target.Group, target.ScriptID)
			if label == "" {
				label = fmt.Sprintf("group %d", index+1)
			}
			return nil, fmt.Errorf("%s: %w", label, err)
		}

		vusers := littlesLawVUsers(target.TargetTPS, artMs)
		newName := firstNonEmpty(strings.TrimSpace(target.Name), groupName)
		if newName == "" {
			newName = fmt.Sprintf("Group %d", index+1)
		}

		result.Groups = append(result.Groups, GoalSizingGroupResult{
			Group:          newName,
			ScriptID:       scriptID,
			BaselineARTMs:  artMs,
			TargetTPS:      target.TargetTPS,
			ComputedVUsers: vusers,
			Basis:          basis,
		})
		draftGroups = append(draftGroups, CreateGroupEntry{
			Name:     newName,
			ScriptID: scriptID,
			VUsers:   vusers,
			RampUp:   suggestedRampUp(vusers),
		})

		result.TotalTargetTPS += target.TargetTPS
		result.TotalVUsers += vusers
	}

	minV, maxV := goalVUserBounds(result.TotalVUsers, req.MinVUsers, req.MaxVUsers)
	totalTPS := result.TotalTargetTPS

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fmt.Sprintf("Goal %s TPS", strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", totalTPS), "0"), "."))
	}
	duration := strings.TrimSpace(req.Duration)
	if duration == "" {
		duration = defaultGoalScenarioDuration
	}

	result.ScenarioDraft = CreateScenarioRequest{
		Name:         name,
		Duration:     duration,
		StartDelay:   strings.TrimSpace(req.StartDelay),
		ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{
			TargetAverageTPS: &totalTPS,
			MinVUsers:        &minV,
			MaxVUsers:        &maxV,
		},
		Groups: draftGroups,
	}
	return result, nil
}

// resolveBaselineGroup maps a sizing target onto a baseline group, returning
// the baseline group name and its script id where available.
func resolveBaselineGroup(baselineGroups []TestGroup, target GoalSizingGroupTarget) (groupName, scriptID string) {
	scriptID = strings.TrimSpace(target.ScriptID)
	groupName = strings.TrimSpace(target.Group)

	if groupName != "" {
		for _, g := range baselineGroups {
			if g.Name == groupName {
				if scriptID == "" {
					scriptID = g.ScriptID
				}
				return groupName, scriptID
			}
		}
		return groupName, scriptID
	}
	if scriptID != "" {
		for _, g := range baselineGroups {
			if g.ScriptID == scriptID {
				return g.Name, scriptID
			}
		}
	}
	return groupName, scriptID
}

// baselineGroupART returns the average response time (ms) to use for sizing a
// group, plus the basis ("group" or "overview"). It falls back to the overall
// run ART when the baseline has a single group or no per-group breakdown.
func baselineGroupART(report *RunArchiveReport, groupName string, singleGroupBaseline bool) (artMs float64, basis string, err error) {
	if groupName != "" {
		if stats, ok := report.ByGroup[groupName]; ok {
			if stats.AvgResponseMs <= 0 {
				return 0, "", fmt.Errorf("baseline average response time is zero; cannot size")
			}
			return stats.AvgResponseMs, "group", nil
		}
	}
	if singleGroupBaseline {
		if report.Overview.AvgResponseMs <= 0 {
			return 0, "", fmt.Errorf("baseline average response time is zero; cannot size")
		}
		return report.Overview.AvgResponseMs, "overview", nil
	}
	return 0, "", fmt.Errorf("no baseline response-time data for this group")
}

// littlesLawVUsers computes VUsers = ceil(targetTPS * ART_seconds), clamped to
// a minimum of 1. A small epsilon absorbs floating-point error so clean inputs
// (e.g. 100 TPS * 3.000 s) land on the exact integer.
func littlesLawVUsers(targetTPS, artMs float64) int {
	vusers := int(math.Ceil(targetTPS*(artMs/1000.0) - 1e-9))
	if vusers < 1 {
		vusers = 1
	}
	return vusers
}

// goalVUserBounds derives min/max VUser guardrails around the static estimate
// so the runtime goal controller has room to converge (response time rises
// under load, so it usually scales up). Explicit overrides win when valid.
func goalVUserBounds(totalVUsers int, minOverride, maxOverride *int) (minV, maxV int) {
	minV = int(math.Round(0.5 * float64(totalVUsers)))
	if minV < 1 {
		minV = 1
	}
	maxV = int(math.Ceil(2.0 * float64(totalVUsers)))
	if maxV < totalVUsers {
		maxV = totalVUsers
	}
	if minOverride != nil && *minOverride >= 1 {
		minV = *minOverride
	}
	if maxOverride != nil && *maxOverride >= 1 {
		maxV = *maxOverride
	}
	if minV > maxV {
		minV = maxV
	}
	return minV, maxV
}

// suggestedRampUp proposes a ramp-up duration from the documented
// 500 VUsers/sec/pod target, with a 10s floor.
func suggestedRampUp(vusers int) string {
	secs := (vusers + 499) / 500
	if secs < 10 {
		secs = 10
	}
	return fmt.Sprintf("%ds", secs)
}

// firstNonBlank returns the first non-empty, trimmed value.
func firstNonBlank(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
