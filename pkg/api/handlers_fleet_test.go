package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleSetGeneratorDrainStateUpdatesInventory(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	backend := newDistributedExecutionBackend([]string{"gen-a:9090|region=us-east", "gen-b:9090|region=us-east"}, time.Second, nil)
	server.runner.setExecutionBackend(backend)

	body := bytes.NewBufferString(`{"target":"gen-a:9090","drain_state":"cordoned"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/system/generators/drain", body)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	inventory := server.systemInventory(req.Context())
	if len(inventory.Generators) != 2 {
		t.Fatalf("generator count = %d, want 2", len(inventory.Generators))
	}
	drainStates := map[string]string{}
	for _, generator := range inventory.Generators {
		drainStates[generator.Target] = generator.DrainState
	}
	if drainStates["gen-a:9090"] != "cordoned" {
		t.Fatalf("gen-a drain_state = %q, want cordoned", drainStates["gen-a:9090"])
	}
	if drainStates["gen-b:9090"] != "active" {
		t.Fatalf("gen-b drain_state = %q, want active", drainStates["gen-b:9090"])
	}
}

func TestDistributedBackendSkipsCordonedTargetsForPlacement(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	if err := backend.SetGeneratorDrainState("gen-a:9090", "cordoned"); err != nil {
		t.Fatalf("SetGeneratorDrainState returned error: %v", err)
	}

	allowed, err := backend.allowedTargetsForTest(&ManagedTest{})
	if err != nil {
		t.Fatalf("allowedTargetsForTest returned error: %v", err)
	}
	if len(allowed) != 1 || allowed[0] != "gen-b:9090" {
		t.Fatalf("allowed targets = %#v, want only gen-b", allowed)
	}

	_, err = backend.allowedTargetsForTest(&ManagedTest{Assignment: &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeSpecificHosts,
		SelectedTargets: []string{"gen-a:9090"},
	}})
	if err == nil || !strings.Contains(err.Error(), "cordoned") {
		t.Fatalf("specific cordoned target error = %v, want cordoned rejection", err)
	}
}

func TestDistributedPlannerSkipsIncompatibleProtocolTargets(t *testing.T) {
	groups := []TestGroup{{Name: "grpc-users", Protocol: "grpc", VUsers: 5}}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-http:9090": {Target: "gen-http:9090", AvailableCapacity: 100},
		"gen-grpc:9090": {Target: "gen-grpc:9090", AvailableCapacity: 50},
	}
	specs := []generatorTargetSpec{
		parseGeneratorTargetSpec("gen-http:9090|protocols=http|version=0.1.0"),
		parseGeneratorTargetSpec("gen-grpc:9090|protocols=grpc,http|version=0.1.0"),
	}

	assignments := planDistributedGroupTargets(groups, loadByTarget, []string{"gen-http:9090", "gen-grpc:9090"}, nil, specs)
	if assignments["grpc-users"] != "gen-grpc:9090" {
		t.Fatalf("assignment = %q, want gen-grpc:9090", assignments["grpc-users"])
	}
}

func TestDistributedPlannerRejectsIncompatibleGeneratorVersion(t *testing.T) {
	groups := []TestGroup{{Name: "http-users", Protocol: "http", VUsers: 5}}
	loadByTarget := map[string]generatorLoadSnapshot{
		"gen-old:9090": {Target: "gen-old:9090", AvailableCapacity: 100},
	}
	specs := []generatorTargetSpec{
		parseGeneratorTargetSpec("gen-old:9090|protocols=http|version=2.0.0"),
	}

	assignments := planDistributedGroupTargets(groups, loadByTarget, []string{"gen-old:9090"}, nil, specs)
	if assignments["http-users"] != "" {
		t.Fatalf("assignment = %q, want empty target for incompatible generator version", assignments["http-users"])
	}
	message := generatorPlacementRejectionMessage(groups[0], loadByTarget, specs)
	if !strings.Contains(message, "incompatible") || !strings.Contains(message, "2.0.0") {
		t.Fatalf("rejection message = %q, want incompatible version detail", message)
	}
}

func TestHandleGetFleetAlertsSummarizesInventoryRisks(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{
		{
			Target:             "gen-a:9090",
			GeneratorID:        "gen-a",
			Region:             "us-east",
			Status:             "connected",
			Version:            "0.1.0",
			DrainState:         "cordoned",
			CapacityState:      "available",
			AvailableCapacity:  100,
			ActiveVUsers:       50,
			MaxVUsers:          1000,
			CompatibilityState: "compatible",
		},
		{
			Target:              "gen-b:9090",
			GeneratorID:         "gen-b",
			Region:              "us-west",
			Status:              "unreachable",
			Version:             "1.0.0",
			DrainState:          "active",
			CapacityState:       "saturated",
			AvailableCapacity:   0,
			ActiveVUsers:        2000,
			MaxVUsers:           2000,
			CompatibilityState:  "protocol_mismatch",
			CompatibilityIssues: []string{"missing grpc runtime"},
		},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/alerts", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload FleetAlertsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.Count != 5 || len(payload.Alerts) != 5 {
		t.Fatalf("payload = %#v, want five alerts", payload)
	}
	byType := map[string]FleetAlert{}
	for _, alert := range payload.Alerts {
		byType[alert.Type] = alert
	}
	if byType["drain_state"].Target != "gen-a" || byType["unreachable"].Severity != "danger" || byType["saturation"].Target != "gen-b" {
		t.Fatalf("alerts by type = %#v, want drain/unreachable/saturation", byType)
	}
	if !strings.Contains(byType["compatibility"].Summary, "missing grpc runtime") {
		t.Fatalf("compatibility alert = %#v, want runtime reason", byType["compatibility"])
	}
	if !strings.Contains(byType["version_mismatch"].Summary, "1.0.0") {
		t.Fatalf("version alert = %#v, want generator version", byType["version_mismatch"])
	}
}

func TestHandleSetGeneratorDrainStateRejectsInvalidState(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.setExecutionBackend(newDistributedExecutionBackend([]string{"gen-a:9090"}, time.Second, nil))

	body, _ := json.Marshal(GeneratorDrainRequest{Target: "gen-a:9090", DrainState: "paused"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/system/generators/drain", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}
