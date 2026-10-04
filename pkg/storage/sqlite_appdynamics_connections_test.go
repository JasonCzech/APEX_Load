package storage

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
)

func TestSQLiteAppDynamicsConnectionsCASAndIsolation(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/appd.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"p1", "p2"} {
		if err := store.SaveProject(&Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	c := &AppDynamicsConnection{ID: "c1", ProjectID: "p1", Name: "AppDynamics", Enabled: true, Config: appdynamics.Config{Endpoint: "https://controller.example", AccountName: "tenant"}, ClientIDSecretName: "hidden-client-id", ClientSecretSecretName: "hidden-client-secret"}
	if err := store.CreateAppDynamicsConnection(c); err != nil {
		t.Fatal(err)
	}
	if c.Revision != 1 || c.Readiness() != "unverified" {
		t.Fatalf("connection=%+v", c)
	}
	checked, err := store.UpdateAppDynamicsConnectionHealth(c.ID, c.ProjectID, c.Revision, "connected", "Connected", appdynamics.Capabilities{Metrics: true}, now)
	if err != nil || !checked {
		t.Fatal(checked, err)
	}
	loaded, err := store.GetAppDynamicsConnection(c.ID)
	if err != nil || loaded.Readiness() != "ready" || loaded.ClientIDSecretName != "hidden-client-id" {
		t.Fatal(loaded, err)
	}
	public, _ := json.Marshal(loaded)
	if strings.Contains(string(public), "hidden-client") {
		t.Fatalf("public credentials leaked %s", public)
	}
	other, err := store.ListAppDynamicsConnections("p2")
	if err != nil || len(other) != 0 {
		t.Fatal(other, err)
	}
	snapshot := loaded.Snapshot()
	loaded.Config.ApprovedCIDRs = []string{"10.0.0.0/8"}
	if len(snapshot.Config.ApprovedCIDRs) != 0 {
		t.Fatal("snapshot mutated")
	}
	expected := loaded.Revision
	loaded.Name = "Updated"
	ok, err := store.UpdateAppDynamicsConnectionIfRevision(loaded, expected)
	if err != nil || !ok || loaded.Revision != 2 || loaded.Readiness() != "unverified" {
		t.Fatal(ok, loaded, err)
	}
	if ok, err := store.UpdateAppDynamicsConnectionIfRevision(loaded, expected); err != nil || ok {
		t.Fatal("stale config update", ok, err)
	}
	if ok, err := store.UpdateAppDynamicsConnectionHealth(loaded.ID, loaded.ProjectID, expected, "connected", "late", appdynamics.Capabilities{}, now); err != nil || ok {
		t.Fatal("stale verification", ok, err)
	}
	if ok, err := store.DeleteAppDynamicsConnection(c.ID, "p2"); err != nil || ok {
		t.Fatal("cross-project delete", ok, err)
	}
	if err := store.DeleteProject("p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAppDynamicsConnection(c.ID); err != sql.ErrNoRows {
		t.Fatalf("connection survived deletion %v", err)
	}
}
func TestAppDynamicsConnectionDraftAndValidation(t *testing.T) {
	c := &AppDynamicsConnection{ID: "draft", ProjectID: "p", Name: "Later", Enabled: true}
	if err := c.Validate(); err != nil || c.Readiness() != "unconfigured" {
		t.Fatal(c, err)
	}
	c.CollectionMode = "collector"
	if c.Validate() == nil {
		t.Fatal("accepted missing collector")
	}
	c.CollectorAgentID = "agent"
	c.Config = appdynamics.Config{Endpoint: "http://controller.example", AccountName: "account"}
	if c.Validate() == nil {
		t.Fatal("accepted unsafe endpoint")
	}
}

func TestSQLiteAppDynamicsPresentationChangePreservesVerification(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/appd.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "p", Name: "p", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	c := &AppDynamicsConnection{ID: "c", ProjectID: "p", Name: "name", Config: appdynamics.Config{Endpoint: "https://controller.example", AccountName: "tenant"}, ClientIDSecretName: "id", ClientSecretSecretName: "secret"}
	if err := store.CreateAppDynamicsConnection(c); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.UpdateAppDynamicsConnectionHealth("c", "p", 1, "connected", "Connected", appdynamics.Capabilities{Metrics: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	c, err = store.GetAppDynamicsConnection("c")
	if err != nil {
		t.Fatal(err)
	}
	c.Enabled = true
	c.Name = "display update"
	c.Description = "notes"
	if ok, err := store.UpdateAppDynamicsConnectionIfRevision(c, 1); err != nil || !ok || c.VerifiedRevision != 2 || c.Readiness() != "ready" {
		t.Fatal(c, ok, err)
	}
	if loaded, err := store.GetAppDynamicsConnection("c"); err != nil || loaded.VerifiedRevision != 2 || loaded.Readiness() != "ready" {
		t.Fatal(loaded, err)
	}
	c.ClientSecretSecretName = "different-secret"
	if ok, err := store.UpdateAppDynamicsConnectionIfRevision(c, 2); err != nil || !ok || c.VerifiedRevision != 0 || c.Readiness() != "unverified" {
		t.Fatal(c, ok, err)
	}
	if ok, err := store.DeleteAppDynamicsConnectionIfRevision("c", "p", 2); err != nil || ok {
		t.Fatal("stale delete", ok, err)
	}
	if ok, err := store.DeleteAppDynamicsConnectionIfRevision("c", "p", 3); err != nil || !ok {
		t.Fatal("current delete", ok, err)
	}
}
