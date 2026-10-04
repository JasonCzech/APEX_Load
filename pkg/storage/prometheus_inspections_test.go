package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPrometheusInspectionDurableClaimsAndProjectIsolation(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/inspect.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	if err = s.SaveProject(&Project{ID: "p", Name: "Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	op := &PrometheusInspection{ID: "op", ProjectID: "p", ConnectionID: "conn", ConnectionRevision: 2, Kind: "test", PayloadJSON: `{"secret_reference":"hidden"}`, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = s.CreatePrometheusInspection(op); err != nil {
		t.Fatal(err)
	}
	if err = s.CreatePrometheusInspection(op); err == nil {
		t.Fatal("duplicate operation accepted")
	}
	serialized, _ := json.Marshal(op)
	if strings.Contains(string(serialized), "hidden") {
		t.Fatal("private payload serialized")
	}
	if list, err := s.ListPrometheusInspections("other", "", 10); err != nil || len(list) != 0 {
		t.Fatalf("project leak: %v %v", list, err)
	}
	claimed, err := s.ClaimPrometheusInspection("op", "owner", now, now.Add(30*time.Second))
	if err != nil || !claimed {
		t.Fatalf("claim %t %v", claimed, err)
	}
	if ok, err := s.ClaimPrometheusInspection("op", "other", now, now.Add(30*time.Second)); err != nil || ok {
		t.Fatal("concurrent claim accepted")
	}
	if ok, err := s.CompletePrometheusInspection("op", "other", `{}`, "", now); err != nil || ok {
		t.Fatal("wrong owner completed")
	}
	at := now.Add(31 * time.Second)
	if ok, err := s.ClaimPrometheusInspection("op", "new", at, now.Add(59*time.Second)); err != nil || !ok {
		t.Fatal("expired claim did not recover")
	}
	if ok, err := s.CompletePrometheusInspection("op", "owner", `{}`, "", at); err != nil || ok {
		t.Fatal("previous owner completed")
	}
	if ok, err := s.CompletePrometheusInspection("op", "new", `{"capabilities":{"metrics":true}}`, "", at); err != nil || !ok {
		t.Fatalf("complete %t %v", ok, err)
	}
	loaded, err := s.GetPrometheusInspection("op")
	if err != nil || loaded.State != "complete" {
		t.Fatalf("durable result: %+v %v", loaded, err)
	}
	if list, err := s.ListPrometheusInspections("p", "", 10); err != nil || len(list) != 0 {
		t.Fatal("completed work redelivered")
	}
}
func TestPrometheusInspectionBounds(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/inspect.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, op := range []*PrometheusInspection{nil, {ID: "x", ProjectID: "p", ConnectionID: "c", ConnectionRevision: 1, Kind: "execute", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}} {
		if err := s.CreatePrometheusInspection(op); err == nil {
			t.Fatal("invalid operation accepted")
		}
	}
}

func TestPrometheusInspectionDeadlineBecomesTerminalError(t *testing.T) {
	s, _, now := monitorFixture(t)
	op := &PrometheusInspection{ID: "expired-prom", ProjectID: "monitor-p", ConnectionID: "prom", ConnectionRevision: 1, Kind: "test", PayloadJSON: `{}`, CreatedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-time.Minute)}
	if err := s.CreatePrometheusInspection(op); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetPrometheusInspection(op.ID)
	if err != nil || loaded.State != "error" || loaded.Error != "Inspection deadline expired" {
		t.Fatalf("expired operation remained pending %+v %v", loaded, err)
	}
	if claimed, err := s.ClaimPrometheusInspection(op.ID, "owner", now, now.Add(time.Minute)); err != nil || claimed {
		t.Fatal("expired operation reclaimed", claimed, err)
	}
}

func TestProviderInspectionsShareJournalWithoutCrossAssignment(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/providers.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	requireProjectFixtures(t, s, "p")
	for _, provider := range []string{"appdynamics", "prometheus"} {
		op := &AppDynamicsInspection{ID: provider, ProjectID: "p", ConnectionID: "c", ConnectionRevision: 1, Kind: "test", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
		if provider == "appdynamics" {
			err = s.CreateAppDynamicsInspection(op)
		} else {
			err = s.CreatePrometheusInspection(op)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.ListAppDynamicsInspections("p", "", 4)
	if err != nil || len(a) != 1 || a[0].ID != "appdynamics" {
		t.Fatal(a, err)
	}
	p, err := s.ListPrometheusInspections("p", "", 4)
	if err != nil || len(p) != 1 || p[0].ID != "prometheus" {
		t.Fatal(p, err)
	}
	if ok, err := s.ClaimAppDynamicsInspection("prometheus", "wrong", now, now.Add(time.Second)); err != nil || ok {
		t.Fatal("cross-provider claim", ok, err)
	}
	if _, err := s.GetPrometheusInspection("appdynamics"); err == nil {
		t.Fatal("cross-provider inspection read")
	}
}
