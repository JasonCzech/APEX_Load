package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

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

func TestHandleSetGeneratorDrainStatePersistsAcrossServerRestart(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	targets := []string{"gen-a:9090|region=us-east", "gen-b:9090|region=us-east"}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080", GeneratorTargets: targets, GeneratorTimeout: time.Millisecond})
	body := bytes.NewBufferString(`{"target":"gen-a:9090","drain_state":"cordoned"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/system/generators/drain", body)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	raw, err := store.GetSetting(generatorDrainStatesStoreKey)
	if err != nil {
		t.Fatalf("GetSetting returned error: %v", err)
	}
	if !strings.Contains(raw, "gen-a:9090") || !strings.Contains(raw, "cordoned") {
		t.Fatalf("stored drain states = %q, want gen-a cordoned", raw)
	}

	restarted := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080", GeneratorTargets: targets, GeneratorTimeout: time.Millisecond})
	inventory := restarted.systemInventory(req.Context())
	drainStates := map[string]string{}
	for _, generator := range inventory.Generators {
		drainStates[generator.Target] = generator.DrainState
	}
	if drainStates["gen-a:9090"] != "cordoned" {
		t.Fatalf("restarted gen-a drain_state = %q, want cordoned", drainStates["gen-a:9090"])
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

func TestPoolScopedAssignmentSkipsOfflineMembersLeniently(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9090"}, time.Second, nil)

	// Pool-scoped (PoolID set): gen-c is not configured on this controller → it is
	// skipped, gen-a is kept (lenient).
	poolScoped := &ManagedTest{Assignment: &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeSpecificHosts,
		SelectedTargets: []string{"gen-a:9090", "gen-c:9090"},
		PoolID:          "pool-eu",
	}}
	allowed, err := backend.allowedTargetsForTest(poolScoped)
	if err != nil {
		t.Fatalf("pool-scoped allowedTargetsForTest error = %v", err)
	}
	if len(allowed) != 1 || allowed[0] != "gen-a:9090" {
		t.Fatalf("pool-scoped allowed = %#v, want only gen-a (offline member skipped)", allowed)
	}

	// Explicit specific_hosts (no PoolID): an unconfigured target stays strict (error).
	strict := &ManagedTest{Assignment: &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeSpecificHosts,
		SelectedTargets: []string{"gen-a:9090", "gen-c:9090"},
	}}
	if _, err := backend.allowedTargetsForTest(strict); err == nil {
		t.Fatal("explicit specific_hosts with an unconfigured target should error")
	}

	// A pool whose members are all offline → clear error.
	empty := &ManagedTest{Assignment: &GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeSpecificHosts,
		SelectedTargets: []string{"gen-c:9090"},
		PoolID:          "pool-eu",
	}}
	if _, err := backend.allowedTargetsForTest(empty); err == nil {
		t.Fatal("pool with no active configured members should error")
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

func TestCompatibleMajorVersionTreatsDevLabelsAsUnknown(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		required  string
		want      bool
	}{
		{name: "matching numeric majors", candidate: "v1.2.3", required: "1.4.0", want: true},
		{name: "different numeric majors", candidate: "2.0.0", required: "1.4.0", want: false},
		{name: "candidate dev label is unknown", candidate: "dev+abc1234", required: "0.1.0", want: true},
		{name: "required dev label is unknown", candidate: "1.0.0", required: "dev+abc1234", want: true},
		{name: "raw build label is unknown", candidate: "abcdef0123456789", required: "0.1.0", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := compatibleMajorVersion(tc.candidate, tc.required); got != tc.want {
				t.Fatalf("compatibleMajorVersion(%q, %q) = %v, want %v", tc.candidate, tc.required, got, tc.want)
			}
		})
	}
}

func TestLeadingMajorVersionIgnoresNonSemverLabels(t *testing.T) {
	tests := map[string]string{
		"v1.2.3":           "1",
		"1.2.3":            "1",
		"dev+abc1234":      "",
		"abcdef0123456789": "",
		"unknown":          "",
	}

	for version, want := range tests {
		if got := leadingMajorVersion(version); got != want {
			t.Fatalf("leadingMajorVersion(%q) = %q, want %q", version, got, want)
		}
	}
}

func TestHandleGetFleetAlertsSummarizesInventoryRisks(t *testing.T) {
	origVersion, origCommit, origBuildTime := buildinfo.Version, buildinfo.Commit, buildinfo.BuildTime
	buildinfo.Version = "0.1.0"
	buildinfo.Commit = ""
	buildinfo.BuildTime = ""
	t.Cleanup(func() {
		buildinfo.Version = origVersion
		buildinfo.Commit = origCommit
		buildinfo.BuildTime = origBuildTime
	})

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

func TestHandleGetFleetCapacityFiltersRegionProtocolAndReservations(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Generator.MaxVUsers = 1000
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 3
	settings.CapacityPlanning.Reservations = []CapacityReservation{{
		ProjectID:          "checkout",
		ReservedGenerators: 2,
	}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{
		{
			Target:             "gen-a:9090",
			GeneratorID:        "gen-a",
			Region:             "us-east",
			Status:             "connected",
			Version:            "0.1.0",
			SupportedProtocols: []string{"http", "grpc"},
			CompatibleFeatures: []string{"http", "grpc", "browser-canary"},
			DrainState:         "active",
			CapacityState:      "available",
			AvailableCapacity:  800,
			ActiveVUsers:       200,
			MaxVUsers:          1000,
			CompatibilityState: "compatible",
		},
		{
			Target:             "gen-b:9090",
			GeneratorID:        "gen-b",
			Region:             "us-east",
			Status:             "connected",
			Version:            "0.1.0",
			SupportedProtocols: []string{"http"},
			DrainState:         "active",
			CapacityState:      "available",
			AvailableCapacity:  700,
			ActiveVUsers:       300,
			MaxVUsers:          1000,
			CompatibilityState: "compatible",
		},
		{
			Target:             "gen-c:9090",
			GeneratorID:        "gen-c",
			Region:             "eu-west",
			Status:             "connected",
			Version:            "0.1.0",
			SupportedProtocols: []string{"grpc"},
			DrainState:         "active",
			CapacityState:      "available",
			AvailableCapacity:  900,
			ActiveVUsers:       100,
			MaxVUsers:          1000,
			CompatibilityState: "compatible",
		},
	}})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-fleet-capacity-checkout",
		Name:      "Checkout Fleet Capacity",
		Protocol:  "grpc",
		Version:   "1.0.0",
		ProjectID: "checkout",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-fleet-capacity-shared",
		Name:      "Shared Fleet Capacity",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "shared",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	active, err := server.runner.CreateTest(CreateTestRequest{Name: "Checkout Active", Duration: "30s", ScriptID: "script-fleet-capacity-checkout", VUsers: 250})
	if err != nil {
		t.Fatalf("CreateTest(active) returned error: %v", err)
	}
	pending, err := server.runner.CreateTest(CreateTestRequest{Name: "Checkout Pending", Duration: "30s", ScriptID: "script-fleet-capacity-checkout", VUsers: 400})
	if err != nil {
		t.Fatalf("CreateTest(pending) returned error: %v", err)
	}
	shared, err := server.runner.CreateTest(CreateTestRequest{Name: "Shared Pending", Duration: "30s", ScriptID: "script-fleet-capacity-shared", VUsers: 700})
	if err != nil {
		t.Fatalf("CreateTest(shared) returned error: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.tests[active.ID].Status = "RUNNING"
	server.runner.tests[pending.ID].Status = "PENDING"
	server.runner.tests[shared.ID].Status = "PENDING"
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/capacity?project_id=checkout&region=us-east&protocol=grpc", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload FleetCapacityResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.ProjectID != "checkout" || payload.Region != "us-east" || payload.Protocol != "grpc" {
		t.Fatalf("filters = %#v, want checkout/us-east/grpc", payload)
	}
	if payload.TotalGenerators != 2 || payload.EligibleGenerators != 1 || payload.AvailableSlots != 800 || payload.ActiveSlots != 500 || payload.MaxSlots != 2000 {
		t.Fatalf("capacity payload = %#v, want filtered slot accounting", payload)
	}
	if payload.ReservedGenerators != 2 || payload.ReservedSlots != 2000 {
		t.Fatalf("reservation payload = %#v, want two reserved generators", payload)
	}
	if payload.ProjectActiveSlots != 250 || payload.ProjectPendingSlots != 400 {
		t.Fatalf("project slots = active %d pending %d, want 250/400", payload.ProjectActiveSlots, payload.ProjectPendingSlots)
	}
	checkoutSlots := findFleetProjectSlots(payload.ProjectSlots, "checkout")
	if checkoutSlots == nil || checkoutSlots.ActiveSlots != 250 || checkoutSlots.PendingSlots != 400 || checkoutSlots.FairShareTier != "reserved" {
		t.Fatalf("checkout slots = %#v, want reserved active/pending accounting", checkoutSlots)
	}
	sharedSlots := findFleetProjectSlots(payload.ProjectSlots, "shared")
	if sharedSlots == nil || sharedSlots.PendingSlots != 700 || sharedSlots.FairShareTier != "shared" {
		t.Fatalf("shared slots = %#v, want shared pending accounting", sharedSlots)
	}
	if !containsString(payload.CompatibleFeatures, "grpc") || !containsString(payload.CompatibleFeatures, "http") || !containsString(payload.CompatibleFeatures, "browser-canary") {
		t.Fatalf("compatible features = %#v, want negotiated and fallback features", payload.CompatibleFeatures)
	}
	if len(payload.RejectionReasons) != 1 || !strings.Contains(payload.RejectionReasons[0], "gen-b does not support protocol grpc") {
		t.Fatalf("rejection reasons = %#v, want gen-b protocol rejection", payload.RejectionReasons)
	}
}

func TestHandleGetFleetLoadZonesAggregatesCapacityAndLiveSignals(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{
		{
			Target:             "gen-a:9090",
			GeneratorID:        "gen-a",
			Region:             "us-east",
			Status:             "connected",
			SupportedProtocols: []string{"http", "grpc"},
			DrainState:         "active",
			AvailableCapacity:  800,
			ActiveVUsers:       200,
			MaxVUsers:          1000,
			CompatibilityState: "compatible",
		},
		{
			Target:             "gen-b:9090",
			GeneratorID:        "gen-b",
			Region:             "eu-west",
			Status:             "connected",
			SupportedProtocols: []string{"grpc"},
			DrainState:         "active",
			AvailableCapacity:  400,
			ActiveVUsers:       100,
			MaxVUsers:          1000,
			CompatibilityState: "compatible",
		},
	}})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-zone-http",
		Name:      "Zone HTTP",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "checkout",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "step", Type: "request", Method: "GET", Target: "/"}},
	})
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Zone Weighted",
		Duration: "30s",
		ScriptID: "script-zone-http",
		VUsers:   120,
		Assignment: &GeneratorAssignmentSettings{
			Mode:            generatorAssignmentModeLoadZoneWeighted,
			LoadZoneWeights: map[string]int{"us-east": 100},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.tests[testRun.ID].Status = "RUNNING"
	server.runner.tests[testRun.ID].LiveMetrics = &LiveMetrics{P95Ms: 320, P99Ms: 480, ErrorPct: 1.5}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/load-zones?protocol=http", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload FleetLoadZonesResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	east := findFleetLoadZone(payload.LoadZones, "us-east")
	if east == nil {
		t.Fatalf("load zones = %#v, want us-east", payload.LoadZones)
	}
	if east.TotalGenerators != 1 || east.EligibleGenerators != 1 || east.ActiveVUsers != 120 || east.P95Ms != 320 || east.ErrorPct != 1.5 {
		t.Fatalf("east zone = %#v, want capacity and live metrics", east)
	}
	west := findFleetLoadZone(payload.LoadZones, "eu-west")
	if west == nil || west.EligibleGenerators != 0 || len(west.RejectionReasons) == 0 || !strings.Contains(west.RejectionReasons[0], "does not support protocol http") {
		t.Fatalf("west zone = %#v, want protocol rejection", west)
	}
}

func TestNormalizeGeneratorAssignmentSettingsAcceptsLoadZoneWeights(t *testing.T) {
	assignment, err := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{
		Mode:            generatorAssignmentModeLoadZoneWeighted,
		LoadZoneWeights: map[string]int{"us-east": 70, "eu-west": 30},
	})
	if err != nil {
		t.Fatalf("normalizeGeneratorAssignmentSettings returned error: %v", err)
	}
	if assignment.Mode != generatorAssignmentModeLoadZoneWeighted || assignment.LoadZoneWeights["us-east"] != 70 {
		t.Fatalf("assignment = %#v", assignment)
	}
	if weights := effectiveRegionWeights(assignment); weights["eu-west"] != 30 {
		t.Fatalf("effective weights = %#v", weights)
	}
}

func TestNormalizeGroupAssignmentRejectsPoolID(t *testing.T) {
	// A group-scoped pool_id is never resolved to targets, so it must be rejected
	// rather than silently fall back to fleet-wide (auto) placement.
	if _, err := normalizeGroupGeneratorAssignmentSettings(&GeneratorAssignmentSettings{PoolID: "pool-eu"}); err == nil {
		t.Fatal("expected group-scope pool_id to be rejected")
	}
	// The test-level normalize still accepts a pool_id (it is resolved there).
	out, err := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{PoolID: "pool-eu"})
	if err != nil {
		t.Fatalf("test-level pool_id should be accepted: %v", err)
	}
	if out.PoolID != "pool-eu" {
		t.Fatalf("test-level pool_id not preserved: %#v", out)
	}
	// A group assignment without pool_id is still fine.
	if _, err := normalizeGroupGeneratorAssignmentSettings(&GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto}); err != nil {
		t.Fatalf("group auto assignment should be accepted: %v", err)
	}
}

func findFleetLoadZone(zones []FleetLoadZone, id string) *FleetLoadZone {
	for index := range zones {
		if zones[index].ID == id {
			return &zones[index]
		}
	}
	return nil
}

func findFleetProjectSlots(slots []FleetProjectSlotAccounting, projectID string) *FleetProjectSlotAccounting {
	for index := range slots {
		if slots[index].ProjectID == projectID {
			return &slots[index]
		}
	}
	return nil
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
