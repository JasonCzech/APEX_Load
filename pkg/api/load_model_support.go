package api

import "net/http"

type LoadModelSupportEntry struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Category         string   `json:"category"`
	Status           string   `json:"status"`
	StormrunnerShape string   `json:"stormrunner_shape"`
	Fields           []string `json:"fields"`
	Notes            string   `json:"notes"`
}

func (s *Server) handleLoadModelSupportMatrix(w http.ResponseWriter, r *http.Request) {
	matrix := loadModelSupportMatrix()
	counts := map[string]int{}
	for _, entry := range matrix {
		counts[entry.Status]++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": matrix,
		"status_counts":  counts,
		"notes": []string{
			"Executed means the runtime has first-class behavior today.",
			"Partial means the shape can be approximated through existing fields but needs dedicated authoring or validation work.",
			"Planned means the shape is intentionally tracked but not yet represented as a first-class model.",
		},
	})
}

func loadModelSupportMatrix() []LoadModelSupportEntry {
	return []LoadModelSupportEntry{
		{
			ID:               "closed-vuser",
			Name:             "Closed VUser",
			Category:         "vuser",
			Status:           "executed",
			StormrunnerShape: "Scenario groups with fixed VUsers and duration.",
			Fields:           []string{"groups[].vusers", "duration", "groups[].ramp_up", "groups[].ramp_down"},
			Notes:            "Primary execution model; each VUser loops until duration or iteration limits stop the group.",
		},
		{
			ID:               "open-arrival-rate",
			Name:             "Open Arrival Rate",
			Category:         "throughput",
			Status:           "executed",
			StormrunnerShape: "Open workload runtime using target throughput controls.",
			Fields:           []string{"config.run_logic.workload_model=open", "config.pacing.tps", "goal_config.target_average_tps"},
			Notes:            "Runtime emits target TPS, throughput gap, and miss flags for observability.",
		},
		{
			ID:               "stepped-ramp",
			Name:             "Stepped Ramp",
			Category:         "ramp",
			Status:           "partial",
			StormrunnerShape: "Approximate with multiple groups and per-group ramp timing.",
			Fields:           []string{"groups[].ramp_up", "groups[].start_delay", "groups[].vusers"},
			Notes:            "Dedicated staged segments with hold/ramp-up/ramp-down are tracked by P4.3.",
		},
		{
			ID:               "spike-rendezvous",
			Name:             "Rendezvous Spike",
			Category:         "spike",
			Status:           "executed",
			StormrunnerShape: "Use a rendezvous action to synchronize VUsers before the target transaction.",
			Fields:           []string{"actions[].type=rendezvous", "actions[].params.count", "actions[].params.timeout_ms"},
			Notes:            "Supports named synchronization points inside the script action stream.",
		},
		{
			ID:               "soak",
			Name:             "Soak",
			Category:         "duration",
			Status:           "executed",
			StormrunnerShape: "Closed or open workload with long duration and steady ramp.",
			Fields:           []string{"duration", "groups[].vusers", "config.pacing"},
			Notes:            "Archive/report completeness scoring is tracked separately in P6.",
		},
		{
			ID:               "stress",
			Name:             "Stress",
			Category:         "capacity",
			Status:           "partial",
			StormrunnerShape: "Use high VUser or TPS targets with capacity advisory and optional goal control.",
			Fields:           []string{"groups[].vusers", "goal_config.max_vusers", "capacity_advisory"},
			Notes:            "Reusable stress templates and staged escalation are tracked by P4.2/P4.3.",
		},
		{
			ID:               "goal-based",
			Name:             "Goal-Based",
			Category:         "adaptive",
			Status:           "executed",
			StormrunnerShape: "Goal controller adjusts VUsers within configured guardrails.",
			Fields:           []string{"scenario_mode=goal_oriented", "goal_config.target_average_tps", "goal_config.min_vusers", "goal_config.max_vusers"},
			Notes:            "Supports scale-up/down and stop-on-SLA/error guardrails.",
		},
		{
			ID:               "mixed-protocol",
			Name:             "Mixed Protocol Groups",
			Category:         "composition",
			Status:           "executed",
			StormrunnerShape: "Scenario groups can reference different script protocols.",
			Fields:           []string{"groups[].script_id", "groups[].vusers", "capacity_advisory.protocol_densities"},
			Notes:            "Capacity advice already accounts for protocol-specific density envelopes.",
		},
	}
}
