package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func sandboxReplayFixture(t *testing.T) (*dsl.Script, []engine.LiveTableSandboxSeed) {
	t.Helper()
	name := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true}, nil
	})
	return &dsl.Script{Name: "Isolated table debugger", Version: "1.0", Protocol: name,
			Config:  dsl.ScriptConfig{BaseURL: "http://sandbox.invalid"},
			Actions: []dsl.Action{{Name: "Take sandbox work", Type: "live_table", Params: map[string]any{"alias": "work", "operation": "pop", "result_prefix": "taken"}}}},
		[]engine.LiveTableSandboxSeed{{Alias: "work", Columns: []string{"id", "note"}, Rows: []livetable.Row{{Values: map[string]string{"id": "sandbox-only", "note": ""}}}}}
}

func TestLiveTableSandboxReplayEndpointsUseFreshExplicitSeeds(t *testing.T) {
	for _, route := range []string{"/api/v1/scripts/replay", "/api/v1/scripts/debug/sessions", "/api/v1/scripts/replay/sessions"} {
		t.Run(route, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			t.Cleanup(server.scriptDebug.Close)
			script, seeds := sandboxReplayFixture(t)
			for attempt := 0; attempt < 2; attempt++ {
				body, _ := json.Marshal(map[string]any{"script": script, "live_table_seeds": seeds})
				req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, req)
				if response.Code != http.StatusOK {
					t.Fatalf("attempt %d: %d %s", attempt, response.Code, response.Body.String())
				}
				var reply struct {
					ReplaySessionID string               `json:"replay_session_id"`
					Report          *engine.ReplayReport `json:"report"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil {
					t.Fatal(err)
				}
				if route != "/api/v1/scripts/replay" {
					finished := waitForScriptDebugStatus(t, server.scriptDebug, reply.ReplaySessionID, ScriptDebugCompleted)
					reply.Report = finished.FinalReport
					if strings.Contains(response.Body.String(), "live_table_seeds") {
						t.Fatal("session admission echoed raw seed configuration")
					}
				}
				if reply.Report == nil || !reply.Report.Success || reply.Report.FinalVariables["taken.values.id"] != "sandbox-only" {
					t.Fatalf("fresh replay did not consume the seed: %+v", reply.Report)
				}
				if seeds[0].Rows[0].Values["id"] != "sandbox-only" {
					t.Fatal("replay changed the supplied seed")
				}
			}
		})
	}
}

func TestLiveTableSandboxPreflightRejectsInvalidSeedsWithoutStarting(t *testing.T) {
	server := NewServer(ServerConfig{})
	t.Cleanup(server.scriptDebug.Close)
	for _, tc := range []struct {
		name string
		edit func(*dsl.Script, *[]engine.LiveTableSandboxSeed)
	}{
		{"missing", func(_ *dsl.Script, seeds *[]engine.LiveTableSandboxSeed) { *seeds = nil }},
		{"wrong alias", func(_ *dsl.Script, seeds *[]engine.LiveTableSandboxSeed) { (*seeds)[0].Alias = "other" }},
		{"static collision", func(script *dsl.Script, _ *[]engine.LiveTableSandboxSeed) {
			script.DataSources = []dsl.DataSource{{Name: "work"}}
		}},
		{"record identity", func(_ *dsl.Script, seeds *[]engine.LiveTableSandboxSeed) { (*seeds)[0].Rows[0].ID = "running-record" }},
		{"managed reference", func(_ *dsl.Script, seeds *[]engine.LiveTableSandboxSeed) {
			(*seeds)[0].Rows[0].Values["id"] = "{{ secret.API_KEY}}"
		}},
		{"oversized field", func(_ *dsl.Script, seeds *[]engine.LiveTableSandboxSeed) {
			(*seeds)[0].Rows[0].Values["id"] = strings.Repeat("x", 8193)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, seeds := sandboxReplayFixture(t)
			tc.edit(script, &seeds)
			response := httptest.NewRecorder()
			if server.prepareReplayLiveTables(context.Background(), response, script, seeds) || response.Code != http.StatusBadRequest {
				t.Fatalf("unexpected admission: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestLiveTableSandboxRequiresProjectDataReadAccess(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.settings.Auth.Mode = authModeTrustedHeaders
	script, seeds := sandboxReplayFixture(t)
	script.ProjectID = "sandbox-project"
	for _, tc := range []struct {
		name       string
		resolution authResolution
		want       bool
	}{
		{"anonymous", authResolution{}, false},
		{"other project", authResolution{User: &AuthenticatedUser{Subject: "user", ProjectRoles: map[string]map[string]struct{}{"other": {RoleEditor: {}}}}}, false},
		{"project reader", authResolution{User: &AuthenticatedUser{Subject: "user", ProjectRoles: map[string]map[string]struct{}{"sandbox-project": {RoleViewer: {}}}}}, true},
		{"service missing read", authResolution{User: &AuthenticatedUser{Subject: "service", GlobalRoles: map[string]struct{}{RoleEditor: {}}}, ServiceAccount: &AuthenticatedServiceAccount{ID: "service", Scopes: []string{"tests:run"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := withAuthenticatedContext(httptest.NewRequest(http.MethodPost, "/", nil), tc.resolution)
			response := httptest.NewRecorder()
			if got := server.prepareReplayLiveTables(r.Context(), response, script, seeds); got != tc.want {
				t.Fatalf("allowed=%v want=%v: %d %s", got, tc.want, response.Code, response.Body.String())
			}
		})
	}
}
