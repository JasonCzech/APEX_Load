package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Legacy per-test Dynatrace capture predates native monitoring. It must keep
// working for projects outside the run-monitoring gate, including connections
// that were enabled before native verification existed.
func TestLegacyDynatraceCaptureSurvivesNativeMonitoring(t *testing.T) {
	store, server := newCreateTestProjectIsolationServer(t)
	connection := &storage.DynatraceConnection{ID: "legacy-dt", ProjectID: "project-a", Name: "Legacy SaaS", Enabled: true, EnvironmentURL: "https://legacy.live.dynatrace.com", EnvironmentTokenSecret: "legacy-token", ProblemsEnabled: true, PollIntervalSeconds: 60}
	if err := store.CreateDynatraceConnection(connection); err != nil {
		t.Fatal(err)
	}
	if connection.ConfigurationStatus() != "ready" || connection.Readiness() != "unverified" {
		t.Fatalf("legacy=%s native=%s", connection.ConfigurationStatus(), connection.Readiness())
	}
	create := func(name string) (*ManagedTest, error) {
		return server.runner.CreateTest(CreateTestRequest{Name: name, ProjectID: "project-a", Duration: "1m", Groups: []CreateGroupEntry{{Name: "Primary", ScriptID: "script-project-a", VUsers: 1}}, DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: connection.ID}})
	}

	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	legacy, err := create("Legacy capture")
	if err != nil {
		t.Fatalf("legacy capture rejected outside the run-monitoring gate: %v", err)
	}
	if legacy.DynatraceCapture == nil || legacy.DynatraceCapture.ConnectionID != connection.ID || legacy.MonitorBindings != nil && len(legacy.MonitorBindings.Sources) > 0 {
		t.Fatalf("legacy capture was not preserved: capture=%+v bindings=%+v", legacy.DynatraceCapture, legacy.MonitorBindings)
	}

	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "project-a")
	if _, err := create("Unverified native"); err == nil || !strings.Contains(err.Error(), "Test saved connection") {
		t.Fatalf("gated project accepted an unverified native translation: %v", err)
	}
	if updated, err := store.UpdateDynatraceConnectionHealthIfRevision(connection.ID, connection.ProjectID, connection.Revision, "connected", "", dynatrace.Capabilities{Problems: true}, time.Now().UTC()); err != nil || !updated {
		t.Fatalf("verify %v %v", updated, err)
	}
	native, err := create("Verified native")
	if err != nil {
		t.Fatalf("verified native translation: %v", err)
	}
	if native.DynatraceCapture != nil || native.MonitorBindings == nil || len(native.MonitorBindings.Sources) != 1 || !strings.HasPrefix(native.MonitorBindings.Sources[0].SourceID, "dynatrace-legacy:") {
		t.Fatalf("gated project did not translate to a native binding: capture=%+v bindings=%+v", native.DynatraceCapture, native.MonitorBindings)
	}
}

func TestDynatraceConnectionEnablesWithoutNativeVerification(t *testing.T) {
	server, _ := newTimeslotServer(t)
	base := "/api/v1/projects/proj-a/monitoring/dynatrace-connections"
	created := timeslotRequest(t, server, http.MethodPost, base, map[string]any{"name": "Legacy SaaS", "enabled": true, "environment_url": "https://legacy.live.dynatrace.com", "environment_token_secret": "legacy-token", "problems_enabled": true, "poll_interval_seconds": 60})
	if created.Code != 201 {
		t.Fatalf("enabled connection rejected: %d %s", created.Code, created.Body.String())
	}
	var body struct {
		Status              string `json:"status"`
		LegacyCaptureStatus string `json:"legacy_capture_status"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || body.LegacyCaptureStatus != "ready" || body.Status != "unverified" {
		t.Fatalf("statuses %+v %v %s", body, err, created.Body.String())
	}
}
