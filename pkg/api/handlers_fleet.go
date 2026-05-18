package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type FleetAlertsResponse struct {
	Alerts []FleetAlert `json:"alerts"`
	Count  int          `json:"count"`
}

type FleetAlert struct {
	Type     string `json:"type"`
	Target   string `json:"target"`
	Region   string `json:"region,omitempty"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

type GeneratorDrainRequest struct {
	Target     string `json:"target"`
	DrainState string `json:"drain_state"`
}

func (s *Server) handleGetFleetAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := buildFleetAlerts(s.systemInventory(r.Context()))
	writeJSON(w, http.StatusOK, FleetAlertsResponse{Alerts: alerts, Count: len(alerts)})
}

func (s *Server) handleSetGeneratorDrainState(w http.ResponseWriter, r *http.Request) {
	var req GeneratorDrainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	req.DrainState = strings.TrimSpace(req.DrainState)
	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	if req.DrainState == "" {
		req.DrainState = "active"
	}

	backend, ok := s.runner.getExecutionBackend().(drainableExecutionBackend)
	if !ok {
		writeError(w, http.StatusBadRequest, "generator drain control requires distributed execution")
		return
	}
	if err := backend.SetGeneratorDrainState(req.Target, req.DrainState); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.recordAuditEvent(r, "generator.drain_state.update", "success", "generator", req.Target, "", map[string]any{"drain_state": req.DrainState})
	writeJSON(w, http.StatusOK, map[string]string{
		"target":      req.Target,
		"drain_state": req.DrainState,
	})
}

func buildFleetAlerts(inventory SystemInventoryResponse) []FleetAlert {
	alerts := make([]FleetAlert, 0)
	versionCounts := map[string]int{}
	for _, generator := range inventory.Generators {
		target := firstNonEmpty(generator.GeneratorID, generator.Target)
		status := strings.ToLower(strings.TrimSpace(generator.Status))
		drainState := strings.ToLower(strings.TrimSpace(generator.DrainState))
		capacityState := strings.ToLower(strings.TrimSpace(generator.CapacityState))
		compatibilityState := strings.ToLower(strings.TrimSpace(generator.CompatibilityState))
		version := strings.TrimSpace(generator.Version)
		if version != "" && version != "unknown" {
			versionCounts[version]++
		}
		if status != "" && status != "connected" && status != "healthy" && status != "ready" {
			alerts = append(alerts, FleetAlert{
				Type:     "unreachable",
				Target:   target,
				Region:   generator.Region,
				Severity: "danger",
				Summary:  fmt.Sprintf("%s is %s; placement cannot use this generator.", target, status),
			})
		}
		if drainState == "" {
			drainState = "active"
		}
		if drainState != "active" {
			alerts = append(alerts, FleetAlert{
				Type:     "drain_state",
				Target:   target,
				Region:   generator.Region,
				Severity: "warning",
				Summary:  fmt.Sprintf("%s is %s; new placements are excluded.", target, drainState),
			})
		}
		if capacityState == "saturated" || generator.AvailableCapacity <= 0 {
			alerts = append(alerts, FleetAlert{
				Type:     "saturation",
				Target:   target,
				Region:   generator.Region,
				Severity: "danger",
				Summary:  fmt.Sprintf("%s is saturated with %d active VUsers.", target, generator.ActiveVUsers),
			})
		} else if capacityState == "constrained" {
			alerts = append(alerts, FleetAlert{
				Type:     "saturation",
				Target:   target,
				Region:   generator.Region,
				Severity: "warning",
				Summary:  fmt.Sprintf("%s capacity is constrained: %d slots remain.", target, generator.AvailableCapacity),
			})
		}
		if compatibilityState != "" && compatibilityState != "compatible" {
			reason := compatibilityState
			if len(generator.CompatibilityIssues) > 0 && strings.TrimSpace(generator.CompatibilityIssues[0]) != "" {
				reason = strings.TrimSpace(generator.CompatibilityIssues[0])
			}
			alerts = append(alerts, FleetAlert{
				Type:     "compatibility",
				Target:   target,
				Region:   generator.Region,
				Severity: "danger",
				Summary:  fmt.Sprintf("%s is not placement-compatible: %s.", target, reason),
			})
		}
	}
	controllerMajor := leadingMajorVersion(inventory.Controller.Version)
	for version, count := range versionCounts {
		if controllerMajor == "" || leadingMajorVersion(version) == controllerMajor {
			continue
		}
		alerts = append(alerts, FleetAlert{
			Type:     "version_mismatch",
			Target:   version,
			Severity: "warning",
			Summary:  fmt.Sprintf("%d generator%s report version %s, which differs from controller %s.", count, pluralSuffix(count), version, inventory.Controller.Version),
		})
	}
	return alerts
}

func leadingMajorVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "unknown" {
		return ""
	}
	if dot := strings.Index(version, "."); dot >= 0 {
		version = version[:dot]
	}
	return strings.TrimLeft(version, "v")
}
