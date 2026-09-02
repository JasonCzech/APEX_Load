package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type LoadModelSupportEntry struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Category      string   `json:"category"`
	Status        string   `json:"status"`
	APEXLoadShape string   `json:"apexload_shape"`
	Fields        []string `json:"fields"`
	Notes         string   `json:"notes"`
}

type LoadModelTemplate struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	ShapeID     string                 `json:"shape_id"`
	Description string                 `json:"description"`
	Scenario    LoadModelScenarioDraft `json:"scenario"`
	Notes       []string               `json:"notes,omitempty"`
}

type LoadModelScenarioDraft struct {
	ScenarioMode     string                   `json:"scenario_mode,omitempty"`
	Duration         string                   `json:"duration"`
	ScriptShakeout   bool                     `json:"script_shakeout,omitempty"`
	ResourceBaseline bool                     `json:"resource_baseline,omitempty"`
	GoalConfig       *GoalConfig              `json:"goal_config,omitempty"`
	RuntimeSettings  *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	Groups           []LoadModelTemplateGroup `json:"groups"`
}

type LoadModelTemplateGroup struct {
	Name       string              `json:"name"`
	VUsers     int                 `json:"vusers"`
	RampUp     string              `json:"ramp_up,omitempty"`
	RampDown   string              `json:"ramp_down,omitempty"`
	Stages     []storage.LoadStage `json:"stages,omitempty"`
	Iterations int                 `json:"iterations,omitempty"`
	StartDelay string              `json:"start_delay,omitempty"`
}

// productionReplayShapeID is the workload shape backed by the traffic-profile
// contract. Its catalog feature gates the analyze/derive endpoints as well as
// scenario persistence, so the whole workflow is governed, not just the save.
const productionReplayShapeID = "production-replay"
const csvDrivenShapeID = "csv-driven"

func workloadShapeFeatureID(shapeID string) string {
	return "workload_shape." + strings.TrimSpace(shapeID)
}

func (s *Server) handleLoadModelSupportMatrix(w http.ResponseWriter, r *http.Request) {
	matrix := loadModelSupportMatrix()
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		// Resolve the policy once. Asking per entry re-read the project row, the
		// policy row, and rebuilt the whole feature catalog for every shape.
		policy, err := s.domainFeaturePolicyForProject(projectID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
			return
		}
		filtered := make([]LoadModelSupportEntry, 0, len(matrix))
		for _, entry := range matrix {
			if policy.featureEnabled(workloadShapeFeatureID(entry.ID)) {
				filtered = append(filtered, entry)
			}
		}
		matrix = filtered
	}
	counts := map[string]int{}
	for _, entry := range matrix {
		counts[entry.Status]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": matrix,
		"status_counts":  counts,
		"notes": []string{
			"Executed means the runtime has first-class behavior today.",
			"Partial means a future shape can be approximated through existing fields but needs dedicated authoring or validation work; no current matrix entries are partial.",
			"Planned means the shape is intentionally tracked but not yet represented as a first-class model.",
		},
	})
}

func (s *Server) handleLoadModelTemplates(w http.ResponseWriter, r *http.Request) {
	shapeID := r.URL.Query().Get("shape_id")
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	templates := loadModelTemplates()
	var policy *domainFeaturePolicyResponse
	if projectID != "" {
		resolved, err := s.domainFeaturePolicyForProject(projectID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
			return
		}
		policy = resolved
	}
	filtered := make([]LoadModelTemplate, 0, len(templates))
	for _, template := range templates {
		if shapeID != "" && template.ShapeID != shapeID {
			continue
		}
		if projectID != "" && !policy.featureEnabled(workloadShapeFeatureID(template.ShapeID)) {
			continue
		}
		filtered = append(filtered, template)
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": filtered, "count": len(filtered)})
}

func knownWorkloadShape(shapeID string) bool {
	shapeID = strings.TrimSpace(shapeID)
	for _, entry := range loadModelSupportMatrix() {
		if entry.ID == shapeID {
			return true
		}
	}
	return false
}

var errUnknownWorkloadShape = errors.New("unknown workload shape")

func (s *Server) validateScenarioWorkloadShape(req CreateScenarioRequest, projectID, grandfatheredShapeID string) (string, string, error) {
	if req.WorkloadShapeID == nil {
		return "", "", nil
	}
	shapeID := strings.TrimSpace(*req.WorkloadShapeID)
	if shapeID == "" {
		return "", "", nil
	}
	if !knownWorkloadShape(shapeID) {
		return "", "", errUnknownWorkloadShape
	}
	if shapeID == strings.TrimSpace(grandfatheredShapeID) {
		return "", "", nil
	}
	featureID := workloadShapeFeatureID(shapeID)
	policy, err := s.domainFeaturePolicyForProject(projectID)
	if err != nil {
		return "", "", err
	}
	if !policy.featureEnabled(featureID) {
		return policy.DomainID, featureID, nil
	}
	return "", "", nil
}

func loadModelSupportMatrix() []LoadModelSupportEntry {
	return []LoadModelSupportEntry{
		{
			ID:            "closed-vuser",
			Name:          "Closed VUser",
			Category:      "vuser",
			Status:        "executed",
			APEXLoadShape: "Continuous ramp-up to a sustained closed-model load with an optional continuous ramp-down.",
			Fields:        []string{"groups[].vusers", "groups[].stages", "duration", "goal_config.workload_shape_config.closed_vuser"},
			Notes:         "Each VUser loops during its configured timeline; scenario scope repeats the profile independently for every group.",
		},
		{
			ID:            "open-arrival-rate",
			Name:          "Goal-Driven Scenario",
			Category:      "throughput",
			Status:        "executed",
			APEXLoadShape: "Adaptive goal-oriented workload sized from per-group throughput and response-time inputs.",
			Fields:        []string{"scenario_mode=goal_oriented", "goal_config.workload_shape_config.goal_driven", "goal_config.target_average_tps", "goal_config.min_vusers", "goal_config.max_vusers"},
			Notes:         "Target TPS is configured per group; VUsers are derived planning seeds and runtime demand adapts within guardrails.",
		},
		{
			ID:            "stepped-ramp",
			Name:          "Stepped Ramp",
			Category:      "ramp",
			Status:        "executed",
			APEXLoadShape: "First-class staged segments with ramp-up, hold, ramp-down, target VUsers, and target TPS.",
			Fields:        []string{"groups[].stages[].type", "groups[].stages[].duration", "groups[].stages[].target_vusers", "groups[].stages[].target_tps"},
			Notes:         "Stage lists round-trip through API/storage, derive compatible runtime ramps, and appear in archive load-model goal evidence.",
		},
		{
			ID:            "production-replay",
			Name:          "Production Traffic Profile",
			Category:      "traffic-profile",
			Status:        "executed",
			APEXLoadShape: "Full-resolution step profiles derived from CSV, XLSX, Prometheus, or OpenSearch time series.",
			Fields:        []string{"traffic_profile", "traffic_profile.mappings[].points", "scenario_mode", "goal_config.min_vusers", "goal_config.max_vusers"},
			Notes:         "VUser targets are applied directly; TPS targets use closed-loop adaptive control and may lag short or abrupt production changes.",
		},
		{
			ID:            csvDrivenShapeID,
			Name:          "CSV Driven",
			Category:      "traffic-profile",
			Status:        "executed",
			APEXLoadShape: "Exact step-after VUser or TPS targets authored in a CSV file.",
			Fields:        []string{"traffic_profile", "traffic_profile.mappings[].points", "scenario_mode", "goal_config.min_vusers", "goal_config.max_vusers"},
			Notes:         "Accepts wide or long CSV layouts. VUser targets are applied directly; TPS targets use adaptive control and ART-backed capacity guardrails.",
		},
		{
			ID:            "spike-rendezvous",
			Name:          "Rendezvous Spike",
			Category:      "spike",
			Status:        "executed",
			APEXLoadShape: "Use a rendezvous action to synchronize VUsers before the target transaction.",
			Fields:        []string{"actions[].type=rendezvous", "actions[].params.count", "actions[].params.timeout_ms"},
			Notes:         "Supports named synchronization points inside the script action stream.",
		},
		{
			ID:            "soak",
			Name:          "Soak",
			Category:      "duration",
			Status:        "executed",
			APEXLoadShape: "Closed or open workload with long duration and steady ramp.",
			Fields:        []string{"duration", "groups[].vusers", "config.pacing"},
			Notes:         "Archive/report completeness scoring is tracked separately in P6.",
		},
		{
			ID:            "stress",
			Name:          "Stress",
			Category:      "capacity",
			Status:        "executed",
			APEXLoadShape: "High VUser or TPS targets with capacity advisory, staged escalation, goal control, and archived completion evidence.",
			Fields:        []string{"groups[].vusers", "groups[].stages", "goal_config.max_vusers", "capacity_advisory", "overview.goal_completion"},
			Notes:         "Stress workloads can be authored through templates or staged groups and are validated before save/run against current capacity envelopes.",
		},
		{
			ID:            "goal-based",
			Name:          "Goal-Driven Scenario (legacy alias)",
			Category:      "adaptive",
			Status:        "executed",
			APEXLoadShape: "Goal controller adjusts VUsers within configured guardrails.",
			Fields:        []string{"scenario_mode=goal_oriented", "goal_config.target_average_tps", "goal_config.min_vusers", "goal_config.max_vusers"},
			Notes:         "Supports scale-up/down and stop-on-SLA/error guardrails.",
		},
		{
			ID:            "chaos-resilience",
			Name:          "Chaos Resilience",
			Category:      "resilience",
			Status:        "executed",
			APEXLoadShape: "Runs a guarded load envelope with pre-run script shakeout, staged steady/recovery phases, and evidence-first failure review before full load.",
			Fields:        []string{"script_shakeout", "groups[].stages", "runtime_settings.error_handling.snapshot_on_error", "goal_config.max_error_pct"},
			Notes:         "Use with scripts or environments that inject dependency latency, failures, throttling, or failover events; APEX Load preserves shakeout evidence and lets the operator continue or cancel.",
		},
		{
			ID:            "mixed-protocol",
			Name:          "Mixed Protocol Groups",
			Category:      "composition",
			Status:        "executed",
			APEXLoadShape: "Scenario groups can reference different script protocols.",
			Fields:        []string{"groups[].script_id", "groups[].vusers", "capacity_advisory.protocol_densities"},
			Notes:         "Capacity advice already accounts for protocol-specific density envelopes.",
		},
	}
}

func loadModelTemplates() []LoadModelTemplate {
	targetTPS := 25.0
	minVUsers := 10
	maxVUsers := 20
	p95 := 800.0
	maxErrorPct := 1.0
	chaosTargetTPS := 15.0
	chaosMinVUsers := 5
	chaosMaxVUsers := 75
	chaosP95 := 1200.0
	chaosMaxErrorPct := 5.0
	return []LoadModelTemplate{
		{
			ID:          "ci-api-smoke-closed",
			Name:        "CI API Smoke",
			ShapeID:     "closed-vuser",
			Description: "Small closed-model smoke run suitable for pull-request or release-candidate gates.",
			Scenario: LoadModelScenarioDraft{
				ScenarioMode: "vuser_driven",
				Duration:     "67m",
				GoalConfig: &GoalConfig{WorkloadShapeConfig: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{
					Scope: "scenario",
					Scenario: &ClosedVUserDefinition{
						PeakVUsers: 5, RampUp: "2m", Sustain: "65m", RampDownEnabled: false, RampDown: "2m",
					},
				}}},
				Groups: []LoadModelTemplateGroup{{
					Name: "smoke", VUsers: 5, RampUp: "2m",
					Stages: []storage.LoadStage{
						{Type: "ramp_up", Duration: "2m", TargetVUsers: 5, Name: "Ramp up"},
						{Type: "hold", Duration: "1h5m", TargetVUsers: 5, Name: "Sustained load"},
					},
				}},
			},
		},
		{
			ID:          "open-api-arrival-rate",
			Name:        "Goal-Driven API",
			ShapeID:     "open-arrival-rate",
			Description: "Adaptive API load sized from a throughput goal and expected response time.",
			Scenario: LoadModelScenarioDraft{
				ScenarioMode: "goal_oriented",
				Duration:     "60m",
				GoalConfig: &GoalConfig{TargetAverageTPS: &targetTPS, MinVUsers: &minVUsers, MaxVUsers: &maxVUsers, P95LatencyMs: &p95, MaxErrorPct: &maxErrorPct,
					WorkloadShapeConfig: &WorkloadShapeConfig{GoalDriven: &GoalDrivenConfig{Groups: map[string]GoalDrivenGroupDefinition{
						"group_0": {TargetTPS: targetTPS, ART: &GoalDrivenARTConfig{Basis: "manual", ARTMs: 400}},
					}}}},
				Groups: []LoadModelTemplateGroup{{Name: "arrival-rate", VUsers: 10, RampUp: "2m"}},
			},
			Notes: []string{"The saved 10-VUser seed is ceil(25 TPS × 0.4 seconds ART); runtime demand remains adaptive."},
		},
		{
			ID:          "rendezvous-spike",
			Name:        "Rendezvous Spike",
			ShapeID:     "spike-rendezvous",
			Description: "Spike template for scripts that include a named rendezvous action before a critical transaction.",
			Scenario: LoadModelScenarioDraft{
				Duration: "8m",
				Groups:   []LoadModelTemplateGroup{{Name: "spike", VUsers: 100, RampUp: "1m", RampDown: "1m"}},
			},
			Notes: []string{"Script should contain an actions[].type=rendezvous step before the spike transaction."},
		},
		{
			ID:          "chaos-resilience-shakeout",
			Name:        "Chaos Resilience With Shakeout Gate",
			ShapeID:     "chaos-resilience",
			Description: "Staged resilience starter that validates scripts first, captures failure evidence, then runs baseline, turbulence, and recovery phases.",
			Scenario: LoadModelScenarioDraft{
				ScenarioMode:     "goal_oriented",
				Duration:         "18m",
				ScriptShakeout:   true,
				ResourceBaseline: true,
				GoalConfig:       &GoalConfig{TargetAverageTPS: &chaosTargetTPS, MinVUsers: &chaosMinVUsers, MaxVUsers: &chaosMaxVUsers, P95LatencyMs: &chaosP95, MaxErrorPct: &chaosMaxErrorPct},
				RuntimeSettings:  &RuntimeSettingsOverride{ErrorHandling: &dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true), SnapshotOnError: true}},
				Groups: []LoadModelTemplateGroup{{
					Name:     "chaos-steady-state",
					VUsers:   25,
					RampUp:   "3m",
					RampDown: "2m",
					Stages: []storage.LoadStage{
						{Name: "Baseline", Type: "ramp_up", Duration: "3m", TargetVUsers: 25},
						{Name: "Turbulence", Type: "hold", Duration: "10m", TargetVUsers: 25, TargetTPS: &chaosTargetTPS},
						{Name: "Recovery", Type: "ramp_down", Duration: "5m", TargetVUsers: 5},
					},
				}},
			},
			Notes: []string{"Pair this with controlled environment fault injection or scripts that exercise degraded dependencies.", "Shakeout must pass before the full load starts; failures pause for operator review with evidence."},
		},
		{
			ID:          "mixed-browser-canary",
			Name:        "Mixed HTTP Load With Browser Canary",
			ShapeID:     "mixed-protocol",
			Description: "Enterprise mixed-shape starter with bulk HTTP VUsers and low-density browser canaries.",
			Scenario: LoadModelScenarioDraft{
				Duration: "15m",
				Groups: []LoadModelTemplateGroup{
					{Name: "api-bulk", VUsers: 500, RampUp: "5m", RampDown: "2m"},
					{Name: "browser-canary", VUsers: 5, RampUp: "2m", RampDown: "1m"},
				},
			},
			Notes: []string{"Assign scripts after selection; capacity advisory will account for protocol density."},
		},
	}
}
