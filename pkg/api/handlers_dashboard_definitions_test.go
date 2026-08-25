package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDashboardDefinitionsCreateListAndRevisionConflict(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	body := map[string]any{
		"project_id": projectID,
		"name":       "Shared operations",
		"visibility": "project",
		"tabs": []map[string]any{{
			"id": "overview", "title": "Overview",
			"panels": []map[string]any{{
				"id": "latency", "title": "Latency", "kind": "line",
				"x": 0, "y": 0, "w": 12, "h": 8,
				"query": map[string]any{"metric": "response_time", "aggregation": "p95", "dimensions": []string{"region"}, "max_series": 20},
			}},
		}},
	}
	created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	var definition storage.DashboardDefinition
	if err := json.Unmarshal(created.Body.Bytes(), &definition); err != nil {
		t.Fatal(err)
	}
	if definition.ID == "" || definition.Revision != 1 || definition.ProjectID != projectID {
		t.Fatalf("created definition = %#v", definition)
	}

	listed := dashboardAPIRequest(t, server, http.MethodGet, "/api/v1/monitoring/dashboard-definitions?project_id="+projectID, nil)
	if listed.Code != http.StatusOK || !json.Valid(listed.Body.Bytes()) {
		t.Fatalf("list status = %d body=%s", listed.Code, listed.Body.String())
	}

	patch := dashboardAPIRequest(t, server, http.MethodPatch, "/api/v1/monitoring/dashboard-definitions/"+definition.ID, map[string]any{"revision": 1, "name": "Shared operations v2"})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", patch.Code, patch.Body.String())
	}
	stale := dashboardAPIRequest(t, server, http.MethodPatch, "/api/v1/monitoring/dashboard-definitions/"+definition.ID, map[string]any{"revision": 1, "name": "stale"})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale patch status = %d body=%s", stale.Code, stale.Body.String())
	}
}

func TestDashboardDefinitionRejectsArbitraryPanelKind(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{
		"project_id": projectID, "name": "Unsafe", "visibility": "project",
		"tabs": []map[string]any{{"id": "one", "title": "One", "panels": []map[string]any{{
			"id": "raw", "title": "Raw", "kind": "iframe", "x": 0, "y": 0, "w": 24, "h": 8, "card_id": "raw",
		}}}},
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}
