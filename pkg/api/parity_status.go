package api

import (
	"net/http"
	"time"
)

type ParityStatusResponse struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Overall     string             `json:"overall"`
	Areas       []ParityStatusArea `json:"areas"`
}

type ParityStatusArea struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary"`
	Evidence     []string `json:"evidence,omitempty"`
	Remaining    []string `json:"remaining,omitempty"`
	ReleaseGate  bool     `json:"release_gate"`
	BlockingGate bool     `json:"blocking_gate,omitempty"`
}

func (s *Server) handleGetParityStatus(w http.ResponseWriter, r *http.Request) {
	response := buildParityStatus(time.Now().UTC())
	writeJSON(w, http.StatusOK, response)
}

func buildParityStatus(now time.Time) ParityStatusResponse {
	areas := []ParityStatusArea{
		{
			ID:          "architecture",
			Name:        "Platform Architecture",
			Status:      "partial",
			Summary:     "Project ownership, readiness checks, settings persistence, and archive/report contracts are in place.",
			Evidence:    []string{"/api/v1/system/readiness", "/api/v1/system/settings"},
			Remaining:   []string{"Complete external dependency evidence and production benchmark gates."},
			ReleaseGate: true,
		},
		{
			ID:          "fleet",
			Name:        "Fleet And Scale-Out",
			Status:      "partial",
			Summary:     "Fleet inventory, alerts, persisted drain state, capacity filtering, benchmark-calibrated density envelopes, and placement rejection visibility have first-pass APIs.",
			Evidence:    []string{"/api/v1/system/inventory", "/api/v1/fleet/alerts", "/api/v1/fleet/capacity"},
			Remaining:   []string{"Complete production deployment autoscaling evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "authoring",
			Name:        "Authoring UX",
			Status:      "partial",
			Summary:     "Imports, validation, replay evidence, templates, runtime parity scoring, migration guardrails, and list-level authoring health badges are implemented across backend and dashboard slices.",
			Evidence:    []string{"/api/v1/scripts/import/loadrunner", "/api/v1/scripts/{id}/validate"},
			Remaining:   []string{"Complete final authoring benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "load_modeling",
			Name:        "Load Modeling",
			Status:      "partial",
			Summary:     "Staged ramps, rendezvous actions, arrival modeling, validation fit checks, goal reporting, and coordinated-omission comparison export evidence have backend coverage.",
			Evidence:    []string{"/api/v1/scenarios/validate-load-model", "/api/v1/tests/{id}/archive/report", "/api/v1/tests/{id}/archive/comparison"},
			Remaining:   []string{"Complete final load-model benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "monitoring",
			Name:        "Real-Time Monitoring",
			Status:      "partial",
			Summary:     "Live metrics, telemetry lag, fleet alerts, schedule health, browser/JMS diagnostics, Prometheus export, and the operator parity checklist dashboard are available.",
			Evidence:    []string{"/metrics", "/api/v1/schedules/health", "/api/v1/fleet/alerts", "/api/v1/system/parity-status"},
			Remaining:   []string{"Complete final monitoring benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "reporting",
			Name:        "Historical Reporting",
			Status:      "partial",
			Summary:     "Archives, standalone reports, comparison baselines, usage CSV export, archive dataset CSV export, profile-shaped CSV reports, comparison CSV export, release trend CSV export, usage snapshots, cadence capture, and retention apply workflow are implemented.",
			Evidence:    []string{"/api/v1/system/usage/export", "/api/v1/system/usage/snapshots", "/api/v1/archives/retention/apply", "/api/v1/releases/{id}/trend/export"},
			Remaining:   []string{"Complete final historical reporting benchmark evidence."},
			ReleaseGate: true,
		},
		{
			ID:          "scheduling",
			Name:        "Scheduling",
			Status:      "partial",
			Summary:     "Persisted schedules, occurrences, approvals, missed-run handling, durable lease ownership, health, and restart reconciliation are implemented.",
			Evidence:    []string{"/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences", "/api/v1/schedules/health"},
			Remaining:   []string{"Expand governance conflict evidence for all external environment calendars."},
			ReleaseGate: true,
		},
	}
	return ParityStatusResponse{
		GeneratedAt: now,
		Overall:     aggregateParityStatus(areas),
		Areas:       areas,
	}
}

func aggregateParityStatus(areas []ParityStatusArea) string {
	overall := "complete"
	for _, area := range areas {
		if area.BlockingGate || area.Status == "blocked" {
			return "blocked"
		}
		if area.Status != "complete" {
			overall = "partial"
		}
	}
	return overall
}
