package storage

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSQLiteDynatraceConnectionsLifecycleAndProjectIsolation(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/dynatrace.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	connection := &DynatraceConnection{ID: "dt-a", ProjectID: "project-a", Name: "Dynatrace", Enabled: true,
		EnvironmentURL: "https://abc.live.dynatrace.com", PlatformURL: "https://abc.apps.dynatrace.com",
		MetricsEnabled: true, ProblemsEnabled: true, LogsEnabled: true, EnvironmentTokenSecret: "environment-token",
		PlatformAuthType: DynatracePlatformAuthToken, PlatformTokenSecret: "platform-token",
		MetricQueries: []DynatraceMetricQuery{{Name: "service_latency", MetricSelector: "builtin:service.response.time", Aggregation: "avg"}}}
	if err := store.CreateDynatraceConnection(connection); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetDynatraceConnection(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigurationStatus() != "ready" || loaded.EnvironmentTokenSecret != "environment-token" || loaded.PlatformTokenSecret != "platform-token" {
		t.Fatalf("loaded = %#v", loaded)
	}
	capture := map[string]any{"connection_id": connection.ID, "environment_url": connection.EnvironmentURL, "environment_token_secret": connection.EnvironmentTokenSecret}
	if err := store.SaveTest(&TestRecord{ID: "run-a", ProjectID: "project-a", Name: "Run", Status: "QUEUED", StartTime: now, DynatraceCapture: capture}); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetTest("run-a")
	if err != nil {
		t.Fatal(err)
	}
	encodedCapture, _ := json.Marshal(run.DynatraceCapture)
	if !strings.Contains(string(encodedCapture), `"connection_id":"dt-a"`) || !strings.Contains(string(encodedCapture), `"environment_token_secret":"environment-token"`) {
		t.Fatalf("durable capture = %s", encodedCapture)
	}
	other, err := store.ListDynatraceConnections("project-b")
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-project list = %#v, %v", other, err)
	}
	expected := loaded.UpdatedAt
	loaded.Name = "Dynatrace updated"
	updated, err := store.UpdateDynatraceConnectionIfUnchanged(loaded, expected)
	if err != nil || !updated {
		t.Fatalf("update = %t, %v", updated, err)
	}
	if updated, err := store.UpdateDynatraceConnectionIfUnchanged(loaded, expected); err != nil || updated {
		t.Fatalf("stale update = %t, %v", updated, err)
	}
	if err := store.UpdateDynatraceConnectionHealth(loaded.ID, loaded.ProjectID, "partial", "logs unavailable", map[string]DynatraceCapabilityHealth{"logs": {Status: "error"}}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	loaded, _ = store.GetDynatraceConnection(loaded.ID)
	if loaded.LastCheckStatus != "partial" || loaded.LastCapabilityHealth["logs"].Status != "error" {
		t.Fatalf("health = %#v", loaded)
	}
	if err := store.DeleteProject("project-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetDynatraceConnection(connection.ID); err != sql.ErrNoRows {
		t.Fatalf("connection survived project deletion: %v", err)
	}
}

func TestDynatraceConnectionValidationAndSecretSerialization(t *testing.T) {
	incomplete := &DynatraceConnection{ID: "dt", ProjectID: "project", Name: "Later", Enabled: true}
	if err := incomplete.Validate(); err != nil {
		t.Fatal(err)
	}
	if incomplete.ConfigurationStatus() != "unconfigured" {
		t.Fatalf("status = %q", incomplete.ConfigurationStatus())
	}
	for _, endpoint := range []string{"http://abc.live.dynatrace.com", "https://user:pass@abc.live.dynatrace.com", "https://example.com"} {
		candidate := *incomplete
		candidate.EnvironmentURL = endpoint
		if err := candidate.Validate(); err == nil {
			t.Errorf("unsafe endpoint %q accepted", endpoint)
		}
	}
	unsafeScope := *incomplete
	unsafeScope.OAuthScopes = []string{"storage:logs:read", "iam:users:write"}
	if err := unsafeScope.Validate(); err == nil {
		t.Fatal("unsupported OAuth scope was accepted")
	}
	configured := &DynatraceConnection{ID: "dt", ProjectID: "project", Name: "Dynatrace", EnvironmentTokenSecret: "private-reference", PlatformTokenSecret: "another-reference"}
	encoded, err := json.Marshal(configured)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-reference") || strings.Contains(string(encoded), "another-reference") {
		t.Fatalf("secret references disclosed: %s", encoded)
	}
}
