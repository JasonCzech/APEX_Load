package api

import (
	"net/http"
	"strings"
	"time"
)

type SystemServicesResponse struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Services    []SystemServiceEntry `json:"services"`
}

type SystemServiceEntry struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Status   string         `json:"status"`
	Address  string         `json:"address,omitempty"`
	URL      string         `json:"url,omitempty"`
	Version  string         `json:"version,omitempty"`
	Source   string         `json:"source"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (s *Server) handleGetSystemServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.systemServices(r))
}

func (s *Server) systemServices(r *http.Request) SystemServicesResponse {
	ctx := r.Context()
	inventory := s.systemInventory(ctx)
	services := []SystemServiceEntry{
		{
			ID:      "controller-api",
			Name:    "Controller API",
			Kind:    "backend",
			Status:  "running",
			Address: s.BoundAddr(),
			URL:     s.BrowserURL(),
			Version: inventory.Controller.Version,
			Source:  "controller",
			Metadata: map[string]any{
				"controller_id":  inventory.Controller.ID,
				"execution_mode": inventory.ExecutionMode,
				"health_status":  inventory.Controller.Status,
			},
		},
	}

	if s.recorder != nil {
		recorderStatus := "idle"
		recorderMetadata := map[string]any{}
		if active := s.recorder.ActiveSession(); active != nil {
			recorderStatus = active.Status
			recorderMetadata["active_session_id"] = active.ID
			recorderMetadata["active_session_name"] = active.Name
		}
		services = append(services, SystemServiceEntry{
			ID:       "recorder-proxy",
			Name:     "Recorder Proxy",
			Kind:     "backend",
			Status:   recorderStatus,
			Address:  s.recorder.ProxyAddr(),
			URL:      serviceHTTPURLFromAddress(s.recorder.ProxyAddr()),
			Source:   "recorder",
			Metadata: recorderMetadata,
		})
	}

	for _, generator := range inventory.Generators {
		name := generator.GeneratorID
		if name == "" {
			name = generator.Target
		}
		services = append(services, SystemServiceEntry{
			ID:      "generator:" + name,
			Name:    name,
			Kind:    "backend",
			Status:  generator.Status,
			Address: generator.Target,
			Version: generator.Version,
			Source:  "generator_inventory",
			Metadata: map[string]any{
				"role":               "generator",
				"region":             generator.Region,
				"available_capacity": generator.AvailableCapacity,
				"active_vusers":      generator.ActiveVUsers,
				"capacity_state":     generator.CapacityState,
				"drain_state":        generator.DrainState,
				"health_score":       generator.HealthScore,
			},
		})
	}

	return SystemServicesResponse{
		GeneratedAt: time.Now().UTC(),
		Services:    services,
	}
}

func serviceHTTPURLFromAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return address
	}
	if strings.HasPrefix(address, ":") {
		address = "127.0.0.1" + address
	}
	if strings.HasPrefix(address, "[::]:") {
		address = "127.0.0.1:" + strings.TrimPrefix(address, "[::]:")
	}
	if strings.HasPrefix(address, "0.0.0.0:") {
		address = "127.0.0.1:" + strings.TrimPrefix(address, "0.0.0.0:")
	}
	return "http://" + address
}
