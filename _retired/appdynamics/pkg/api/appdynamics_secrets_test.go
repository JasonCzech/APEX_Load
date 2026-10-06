package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAppDynamicsProjectSecretUsageIncludesAllCredentialFields(t *testing.T) {
	server, durable := newTimeslotServer(t)
	connection := &storage.AppDynamicsConnection{ID: "secret-use", ProjectID: "proj-a", Name: "Monitoring", CollectionMode: "direct", ClientIDSecretName: "shared", ClientSecretSecretName: "shared", UsernameSecretName: "shared", PasswordSecretName: "shared", EventsAPIKeySecretName: "shared"}
	if err := durable.(storage.AppDynamicsConnectionStore).CreateAppDynamicsConnection(connection); err != nil {
		t.Fatal(err)
	}
	usage, err := server.projectSecretUsage("proj-a", "shared")
	if err != nil || usage.ConsumerCount != 1 || usage.ReferenceCount != 5 {
		t.Fatalf("monitoring secret usage %+v %v", usage, err)
	}
	consumer := usage.Consumers[0]
	if consumer.ResourceType != "appdynamics_connection" || consumer.ResourceID != connection.ID {
		t.Fatalf("consumer identity %+v", consumer)
	}
	if err := validateExpectedProjectSecretUsage(usage, &projectSecretExpectedUsage{UsageRevision: usage.UsageRevision, ExclusiveScriptID: consumer.ScriptID}); err == nil {
		t.Fatal("monitoring consumer treated as an exclusive script")
	}
	other, err := server.projectSecretUsage("other", "shared")
	if err != nil || other.ConsumerCount != 0 {
		t.Fatal("cross project monitoring credential usage", err)
	}
	for _, reference := range []string{"bad ref", "{{secret.value}}", "raw\ncredential", strings.Repeat("x", 257)} {
		connection.ClientIDSecretName = reference
		if validateAppDynamicsSecretReferences(connection) == nil {
			t.Fatalf("invalid logical reference accepted %q", reference)
		}
	}
}

func TestAppDynamicsDiagnosticOnlyConnectionCanBeEnabled(t *testing.T) {
	server, durable := newTimeslotServer(t)
	req := appDynamicsRequestFixture()
	req.CollectionMode = "direct"
	req.CollectorAgentID = ""
	connection := &storage.AppDynamicsConnection{ID: "events-connection", ProjectID: "proj-a"}
	applyAppDynamicsConnection(connection, req)
	store := durable.(storage.AppDynamicsConnectionStore)
	if err := store.CreateAppDynamicsConnection(connection); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAppDynamicsConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", appdynamics.Capabilities{Events: true, Status: map[string]string{"metrics": "forbidden", "events": "available"}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	req.Enabled = true
	req.Revision = connection.Revision
	req.ClientIDSecret = nil
	req.ClientSecretSecret = nil
	result := timeslotRequest(t, server, "PUT", "/api/v1/projects/proj-a/monitoring/appdynamics-connections/"+connection.ID, req)
	if result.Code != 200 {
		t.Fatalf("independent capability connection could not be enabled: %d %s", result.Code, result.Body.String())
	}
}
