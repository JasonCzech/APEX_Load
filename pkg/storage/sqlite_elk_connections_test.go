package storage

import (
	"database/sql"
	"testing"
	"time"
)

func TestSQLiteELKConnectionsLifecycleAndProjectIsolation(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/elk.db")
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
	connection := &ELKConnection{ID: "elk-a", ProjectID: "project-a", Name: "Elastic", Enabled: true,
		Endpoint: "https://elastic.example.test:9200", IndexPattern: "logs-*", TimestampField: "@timestamp", MessageField: "message",
		AuthType: ELKAuthAPIKey, APIKeySecretName: "elastic-api-key",
		MetricAggregations: []ELKMetricAggregation{{Name: "latency", Field: "duration_ms", Operation: "avg"}}}
	if err := store.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetELKConnection(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConfigurationStatus() != "ready" || loaded.APIKeySecretName != "elastic-api-key" {
		t.Fatalf("loaded = %#v", loaded)
	}
	other, err := store.ListELKConnections("project-b")
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-project list = %#v, %v", other, err)
	}

	expected := loaded.UpdatedAt
	loaded.Name = "Elastic updated"
	updated, err := store.UpdateELKConnectionIfUnchanged(loaded, expected)
	if err != nil || !updated {
		t.Fatalf("update = %t, %v", updated, err)
	}
	if updated, err := store.UpdateELKConnectionIfUnchanged(loaded, expected); err != nil || updated {
		t.Fatalf("stale update = %t, %v", updated, err)
	}
	checkedAt := now.Add(time.Minute)
	if err := store.UpdateELKConnectionHealth(loaded.ID, loaded.ProjectID, "connected", "ok", checkedAt); err != nil {
		t.Fatal(err)
	}
	loaded, _ = store.GetELKConnection(loaded.ID)
	if loaded.LastCheckStatus != "connected" || loaded.LastCheckedAt == nil {
		t.Fatalf("health = %#v", loaded)
	}

	if err := store.DeleteProject("project-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetELKConnection(connection.ID); err != sql.ErrNoRows {
		t.Fatalf("connection survived project deletion: %v", err)
	}
}

func TestELKConnectionValidationAllowsIncompleteButRejectsUnsafeEndpoints(t *testing.T) {
	incomplete := &ELKConnection{ID: "elk", ProjectID: "project", Name: "Later", Enabled: true}
	if err := incomplete.Validate(); err != nil {
		t.Fatalf("incomplete validation: %v", err)
	}
	if got := incomplete.ConfigurationStatus(); got != "unconfigured" {
		t.Fatalf("status = %q", got)
	}
	for _, endpoint := range []string{"http://elastic.example.test", "https://user:pass@elastic.example.test", "https://elastic.example.test?q=x"} {
		candidate := *incomplete
		candidate.Endpoint = endpoint
		if err := candidate.Validate(); err == nil {
			t.Errorf("unsafe endpoint %q accepted", endpoint)
		}
	}
}
