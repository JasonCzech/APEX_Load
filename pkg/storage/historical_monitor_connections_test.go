package storage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/monitorcontract"
)

func TestRetainedMonitoringConnectionsPreservePrivateSecretReferences(t *testing.T) {
	s, _, now := monitorFixture(t)
	connection := &AppDynamicsConnection{ID: "retained", ProjectID: "monitor-p", Name: "Historical", Revision: 7, VerifiedRevision: 6, Config: monitorcontract.AppDynamicsConfig{Endpoint: "https://controller.example", AccountName: "customer", ApprovedCIDRs: []string{"192.0.2.0/24"}}, PasswordSecretName: "old.reference", CreatedAt: now, UpdatedAt: now}
	body, err := MarshalAppDynamicsConnectionPrivate(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO appdynamics_connections(id,project_id,name,revision,verified_revision,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, connection.ID, connection.ProjectID, connection.Name, connection.Revision, connection.VerifiedRevision, string(body), now, now); err != nil {
		t.Fatal(err)
	}
	retained, err := s.GetAppDynamicsConnection(connection.ID)
	if err != nil || retained.PasswordSecretName != connection.PasswordSecretName || retained.Config.ApprovedCIDRs[0] != "192.0.2.0/24" {
		t.Fatal(retained, err)
	}
	public, err := json.Marshal(retained)
	if err != nil || strings.Contains(string(public), connection.PasswordSecretName) {
		t.Fatal("public metadata disclosed a secret reference", err)
	}
	consumers, err := s.ListMonitorSecretConsumers(connection.ProjectID, "OLD-REFERENCE")
	if err != nil || len(consumers) != 1 || consumers[0].ReferenceCount != 1 {
		t.Fatal(consumers, err)
	}
	other, err := s.ListAppDynamicsConnections("monitor-other")
	if err != nil || len(other) != 0 {
		t.Fatal("cross-project historical connection read", other, err)
	}
}
