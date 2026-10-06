package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAppDynamicsInspectionDurableClaimsAndProjectIsolation(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/inspect.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	if err = s.SaveProject(&Project{ID: "p", Name: "Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	op := &AppDynamicsInspection{ID: "op", ProjectID: "p", ConnectionID: "conn", ConnectionRevision: 2, Kind: "test", PayloadJSON: `{"secret_reference":"hidden"}`, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = s.CreateAppDynamicsInspection(op); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateAppDynamicsInspection(op); err == nil {
		t.Fatal("duplicate operation accepted")
	}
	serialized, _ := json.Marshal(op)
	if strings.Contains(string(serialized), "hidden") {
		t.Fatal("private payload serialized")
	}
	if list, err := s.ListAppDynamicsInspections("other", "", 10); err != nil || len(list) != 0 {
		t.Fatalf("project leak: %v %v", list, err)
	}
	claimed, err := s.ClaimAppDynamicsInspection("op", "owner", now, now.Add(30*time.Second))
	if err != nil || !claimed {
		t.Fatalf("claim %t %v", claimed, err)
	}
	if ok, err := s.ClaimAppDynamicsInspection("op", "other", now, now.Add(30*time.Second)); err != nil || ok {
		t.Fatal("concurrent claim accepted")
	}
	if ok, err := s.CompleteAppDynamicsInspection("op", "other", `{}`, "", now); err != nil || ok {
		t.Fatal("wrong owner completed")
	}
	at := now.Add(31 * time.Second)
	if ok, err := s.ClaimAppDynamicsInspection("op", "new", at, now.Add(59*time.Second)); err != nil || !ok {
		t.Fatal("expired claim did not recover")
	}
	if ok, err := s.CompleteAppDynamicsInspection("op", "owner", `{}`, "", at); err != nil || ok {
		t.Fatal("previous owner completed")
	}
	if ok, err := s.CompleteAppDynamicsInspection("op", "new", `{"capabilities":{"metrics":true}}`, "", at); err != nil || !ok {
		t.Fatalf("complete %t %v", ok, err)
	}
	loaded, err := s.GetAppDynamicsInspection("op")
	if err != nil || loaded.State != "complete" {
		t.Fatalf("durable result: %+v %v", loaded, err)
	}
	if list, err := s.ListAppDynamicsInspections("p", "", 10); err != nil || len(list) != 0 {
		t.Fatal("completed work redelivered")
	}
}
func TestAppDynamicsInspectionBounds(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/inspect.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, op := range []*AppDynamicsInspection{nil, {ID: "x", ProjectID: "p", ConnectionID: "c", ConnectionRevision: 1, Kind: "execute", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}} {
		if err := s.CreateAppDynamicsInspection(op); err == nil {
			t.Fatal("invalid operation accepted")
		}
	}
}
