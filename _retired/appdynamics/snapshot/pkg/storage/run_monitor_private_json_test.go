package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
)

func TestAppDynamicsPrivateSnapshotsSurviveDurableRoundTrip(t *testing.T) {
	snapshot := &AppDynamicsConnectionSnapshot{ConnectionID: "c", Revision: 3, PasswordSecretName: "project-secret-ref"}
	now := time.Now().UTC().Truncate(time.Second)
	job := RunMonitorJob{ID: "job", ConnectionSnapshot: snapshot, QueryCoverage: map[string]appdynamics.Coverage{"metrics": {From: now, To: now.Add(time.Minute), Status: "complete"}}, QueryOffset: 2}
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var recovered RunMonitorJob
	if err := json.Unmarshal(body, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.ConnectionSnapshot == nil || recovered.ConnectionSnapshot.PasswordSecretName != snapshot.PasswordSecretName || recovered.QueryOffset != 2 || recovered.QueryCoverage["metrics"].Status != "complete" {
		t.Fatalf("lost immutable job or query cursor: %+v", recovered)
	}
	preparation := PreparationMonitorSnapshot{Connections: map[string]*AppDynamicsConnectionSnapshot{"s": snapshot}}
	body, err = json.Marshal(preparation)
	if err != nil {
		t.Fatal(err)
	}
	var prepared PreparationMonitorSnapshot
	if err := json.Unmarshal(body, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Connections["s"] == nil || prepared.Connections["s"].Revision != 3 || prepared.Connections["s"].PasswordSecretName != snapshot.PasswordSecretName {
		t.Fatal("lost prepared connection revision")
	}
}
