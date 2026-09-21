package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDisabledMonitoringConnectionsCanBeVerifiedWithoutEnabling(t *testing.T) {
	t.Setenv(devFakeSecretsEnv, "false")
	store, err := storage.NewSQLiteStore(t.TempDir() + "/connections.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project", Name: "Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	elk := &storage.ELKConnection{ID: "elk", ProjectID: "project", Name: "Elastic", Endpoint: "https://127.0.0.1:1", IndexPattern: "logs-*", TimestampField: "@timestamp", MessageField: "message", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "missing-ux-test-secret"}
	dt := &storage.DynatraceConnection{ID: "dt", ProjectID: "project", Name: "Dynatrace", EnvironmentURL: "https://ux-test.live.dynatrace.com", EnvironmentTokenSecret: "missing-ux-test-secret", ProblemsEnabled: true, PollIntervalSeconds: 15}
	if err := store.CreateELKConnection(elk); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDynatraceConnection(dt); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, runner: NewTestRunner(1, NewHub(), store)}
	for _, id := range []string{"elk", "dt"} {
		r := httptest.NewRequest("POST", "/test", nil)
		r.SetPathValue("id", "project")
		r.SetPathValue("connection_id", id)
		w := httptest.NewRecorder()
		if id == "elk" {
			s.handleTestELKConnection(w, r)
		} else {
			s.handleTestDynatraceConnection(w, r)
		}
		// Missing credentials fail diagnostics, not the disabled-state gate.
		if w.Code != 502 {
			t.Fatalf("%s: status=%d body=%s", id, w.Code, w.Body.String())
		}
	}
	elk, err = store.GetELKConnection("elk")
	if err != nil || elk.Enabled || elk.LastCheckStatus != "error" {
		t.Fatalf("ELK changed enablement or lost diagnostic: %#v %v", elk, err)
	}
	dt, err = store.GetDynatraceConnection("dt")
	if err != nil || dt.Enabled || dt.LastCheckStatus != "error" {
		t.Fatalf("Dynatrace changed enablement or lost diagnostic: %#v %v", dt, err)
	}
}
