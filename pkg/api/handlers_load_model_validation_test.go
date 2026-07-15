package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleValidateScenarioLoadModelReturnsCapacityAndDataPoolFindings(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now()
	if err := store.SaveDataPool(&storage.SharedDataPool{ID: "pool-customers", Name: "Customers", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "row-1", PoolID: "pool-customers", Data: map[string]string{"email": "one@example.test"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveDataPoolRow returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-browser-validate",
		Name:     "Browser Checkout",
		Protocol: "browser",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{DeviceProfile: "iphone_15_pro"},
		},
		Parameters: []dsl.Parameter{{
			Name:         "email",
			DataSource:   "customers",
			Column:       "email",
			Selection:    "unique",
			OnExhaustion: "error",
		}},
		Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "https://example.test"}},
	})
	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	targetTPS := 12.5
	body, _ := json.Marshal(CreateScenarioRequest{
		Name:         "Browser Validation",
		Duration:     "10m",
		ScenarioMode: "goal_oriented",
		GoalConfig:   &GoalConfig{TargetAverageTPS: &targetTPS},
		Groups: []CreateGroupEntry{{
			Name:     "browser canary",
			ScriptID: "script-browser-validate",
			Stages: []storage.LoadStage{
				{Type: "ramp_up", Duration: "1m", TargetVUsers: 30, TargetTPS: &targetTPS},
				{Type: "hold", Duration: "5m", TargetVUsers: 30, TargetTPS: &targetTPS},
			},
		}},
		DataPools: []storage.ScenarioDataPoolBinding{{
			PoolID:            "pool-customers",
			Alias:             "customers",
			AccessMode:        "read_only",
			SelectionStrategy: "unique",
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/validate-load-model", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var report LoadModelValidationReport
	if err := json.Unmarshal(resp.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.Status != "blocked" {
		t.Fatalf("status = %q, want blocked report=%#v", report.Status, report)
	}
	if report.TotalVUsers != 30 {
		t.Fatalf("total vusers = %d, want 30", report.TotalVUsers)
	}
	if report.PeakTargetTPS != 12.5 {
		t.Fatalf("peak target tps = %v, want 12.5", report.PeakTargetTPS)
	}
	if report.CapacityAdvisory == nil || !report.CapacityAdvisory.RequiresOverride {
		t.Fatalf("expected capacity advisory requiring override, got %#v", report.CapacityAdvisory)
	}
	assertLoadModelValidationItem(t, report.Items, "browser_canary", "blocked")
	assertLoadModelValidationItem(t, report.Items, "capacity", "blocked")
	assertLoadModelValidationItem(t, report.Items, "data_pool_rows", "ready")
	assertLoadModelValidationItem(t, report.Items, "data_source_fit", "blocked")
	assertLoadModelValidationItem(t, report.Items, "modeled_demand", "ready")
}

func TestVUserDrivenStagesRejectUnsupportedTargetTPS(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "script-stage-target-tps",
		Name:     "Stage target TPS",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	targetTPS := 10.0
	for _, targetVUsers := range []int{0, 5} {
		_, err := runner.CreateTest(CreateTestRequest{
			Name:     "Unsupported staged TPS",
			Duration: "1m",
			Groups: []CreateGroupEntry{{
				ScriptID: "script-stage-target-tps",
				Stages: []storage.LoadStage{{
					Type:         "hold",
					Duration:     "30s",
					TargetVUsers: targetVUsers,
					TargetTPS:    &targetTPS,
				}},
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "target_tps is not supported for vuser-driven stages") {
			t.Fatalf("target_vusers=%d err=%v, want unsupported target_tps error", targetVUsers, err)
		}
	}
}

func TestCreateTestRequestFromScenarioPreservesStages(t *testing.T) {
	stage := storage.LoadStage{Type: "ramp_up", Duration: "30s", TargetVUsers: 20}
	req := createTestRequestFromScenario(CreateTestRequest{}, &StoredScenario{
		Name:     "Staged",
		Duration: "5m",
		Scripts: []ScenarioScriptRef{{
			Name:     "buyers",
			ScriptID: "script-staged",
			VUsers:   20,
			Stages:   []storage.LoadStage{stage},
		}},
	})
	if len(req.Groups) != 1 || len(req.Groups[0].Stages) != 1 {
		t.Fatalf("groups = %#v, want scenario stages preserved", req.Groups)
	}
	if req.Groups[0].Stages[0].Duration != "30s" {
		t.Fatalf("stage duration = %q, want 30s", req.Groups[0].Stages[0].Duration)
	}
}

func TestValidateScenarioLoadModelReportsRendezvousReadiness(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-rendezvous-validate",
		Name:     "Rendezvous Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "Sync buyers", Type: "rendezvous", Params: map[string]any{"count": 5, "timeout_ms": 1000}},
			{Name: "Checkout", Type: "request", Target: "/checkout"},
		},
	})

	report, err := server.runner.ValidateScenarioLoadModel(CreateScenarioRequest{
		Name:     "Rendezvous Spike",
		Duration: "2m",
		Groups: []CreateGroupEntry{{
			Name:     "buyers",
			ScriptID: "script-rendezvous-validate",
			VUsers:   5,
		}},
	})
	if err != nil {
		t.Fatalf("ValidateScenarioLoadModel returned error: %v", err)
	}
	assertLoadModelValidationItem(t, report.Items, "rendezvous", "ready")
}

func assertLoadModelValidationItem(t *testing.T, items []LoadModelValidationItem, dimension string, status string) {
	t.Helper()
	for _, item := range items {
		if item.Dimension == dimension && item.Status == status {
			return
		}
	}
	t.Fatalf("items = %#v, want dimension %q with status %q", items, dimension, status)
}
