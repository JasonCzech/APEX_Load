package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failingSecretUsageScriptPersistence struct {
	scriptPersistence
	err error
}

func (s failingSecretUsageScriptPersistence) ListScripts(string, *string) ([]*dsl.Script, error) {
	return nil, s.err
}

func storedSecretReferenceScript(t *testing.T, server *Server, projectID, id, name, secretName string) *StoredScript {
	t.Helper()
	script := &dsl.Script{
		ID: id, Name: name, Version: "1", Protocol: "http", ProjectID: projectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Call", Type: "request", Method: "GET", Target: "/?token={{secret." + secretName + "}}"}},
	}
	stored, err := server.runner.StoreScriptDurable(script)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestScriptSecretStatusDetailsAndProjectUsageAreValueFree(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	if _, err := managed.Create(context.Background(), projectID, "SHARED_TOKEN", "never-disclose-this", "admin"); err != nil {
		t.Fatal(err)
	}
	first := storedSecretReferenceScript(t, server, projectID, "usage-first", "First", "SHARED_TOKEN")
	storedSecretReferenceScript(t, server, projectID, "usage-second", "Second", "SHARED_TOKEN")

	details := secretAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+first.ID+"/secret-status?details=bindings", nil)
	if details.Code != http.StatusOK {
		t.Fatalf("details status = %d body=%s", details.Code, details.Body.String())
	}
	var detailsPayload struct {
		Bindings []scriptSecretBindingDetail `json:"bindings"`
	}
	if err := json.Unmarshal(details.Body.Bytes(), &detailsPayload); err != nil {
		t.Fatal(err)
	}
	if len(detailsPayload.Bindings) != 1 || detailsPayload.Bindings[0].Name != "SHARED_TOKEN" ||
		detailsPayload.Bindings[0].OccurrenceCount != 1 || len(detailsPayload.Bindings[0].Locations) != 1 {
		t.Fatalf("binding details = %#v", detailsPayload.Bindings)
	}
	if strings.Contains(details.Body.String(), "never-disclose-this") {
		t.Fatalf("details disclosed value: %s", details.Body.String())
	}

	usage := secretAPIRequest(t, server, http.MethodGet, "/api/v1/projects/"+projectID+"/secrets/SHARED_TOKEN/usage?limit=1", nil)
	if usage.Code != http.StatusOK {
		t.Fatalf("usage status = %d body=%s", usage.Code, usage.Body.String())
	}
	var usagePayload struct {
		UsageRevision  string                  `json:"usage_revision"`
		ConsumerCount  int                     `json:"consumer_count"`
		ReferenceCount int                     `json:"reference_count"`
		Consumers      []projectSecretConsumer `json:"consumers"`
		HasMore        bool                    `json:"has_more"`
	}
	if err := json.Unmarshal(usage.Body.Bytes(), &usagePayload); err != nil {
		t.Fatal(err)
	}
	if usagePayload.UsageRevision == "" || usagePayload.ConsumerCount != 2 || usagePayload.ReferenceCount != 2 || len(usagePayload.Consumers) != 1 || !usagePayload.HasMore {
		t.Fatalf("usage = %#v", usagePayload)
	}
	if strings.Contains(usage.Body.String(), "never-disclose-this") {
		t.Fatalf("usage disclosed value: %s", usage.Body.String())
	}
}

func TestRebindScriptSecretReferenceTransformsExactExecutableOccurrences(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"token":"{{ secret.OLD-TOKEN }}","keep":"{{secret.OLD_TOKEN_EXTRA}}"}`))
	script := &dsl.Script{
		ID: "rebind-script", Name: "Rebind", Version: "1", Protocol: "http", ProjectID: projectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "bearer", Token: "{{secret.OLD_TOKEN}}"}}},
		Actions: []dsl.Action{{Name: "Call", Type: "request", Method: "GET", Target: "/{{secret.OLD_TOKEN}}", Body: encoded, BodyEncoding: "base64"}},
	}
	stored, err := server.runner.StoreScriptDurable(script)
	if err != nil {
		t.Fatal(err)

	}
	rebind := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/secret-references/rebind", map[string]any{
		"script": stored.Script, "old_name": "OLD_TOKEN", "new_name": "NEW_TOKEN",
	})
	if rebind.Code != http.StatusOK {
		t.Fatalf("rebind status = %d body=%s", rebind.Code, rebind.Body.String())
	}
	var payload struct {
		Script           dsl.Script `json:"script"`
		ChangedLocations []string   `json:"changed_locations"`
		ChangedCount     int        `json:"changed_count"`
	}
	if err := json.Unmarshal(rebind.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ChangedCount != 3 || len(payload.ChangedLocations) != 3 {
		t.Fatalf("changed = %d locations=%#v", payload.ChangedCount, payload.ChangedLocations)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.Script.Actions[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), "{{secret.NEW_TOKEN}}") || !strings.Contains(string(decoded), "{{secret.OLD_TOKEN_EXTRA}}") {
		t.Fatalf("decoded transformed body = %s", decoded)
	}
	if current, _ := server.runner.GetScript(stored.ID); current.Script.Actions[0].Target != "/{{secret.OLD_TOKEN}}" {
		t.Fatal("draft transformation mutated the saved script")
	}

	stale := cloneScriptDefinition(stored.Script)
	stale.UpdatedAt = stale.UpdatedAt.Add(-time.Second)
	conflict := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/secret-references/rebind", map[string]any{
		"script": stale, "old_name": "OLD_TOKEN", "new_name": "NEW_TOKEN",
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("stale status = %d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestRebindScriptSecretReferenceRejectsInvalidReservedAndAbsentNames(t *testing.T) {
	ordinary := &dsl.Script{Actions: []dsl.Action{{Name: "Call", Type: "request", Method: "GET", Target: "/{{secret.OLD_TOKEN}}"}}}
	reserved := &dsl.Script{Actions: []dsl.Action{{Name: "Call", Type: "request", Method: "GET", Target: "/{{secret.APEX_RUNTIME_OVERRIDE_G1_ABC}}"}}}
	tests := []struct {
		name    string
		script  *dsl.Script
		oldName string
		newName string
	}{
		{name: "invalid old name", script: ordinary, oldName: "invalid name", newName: "NEW_TOKEN"},
		{name: "invalid new name", script: ordinary, oldName: "OLD_TOKEN", newName: "invalid name"},
		{name: "reserved old name", script: reserved, oldName: "APEX_RUNTIME_OVERRIDE_G1_ABC", newName: "NEW_TOKEN"},
		{name: "reserved new name", script: ordinary, oldName: "OLD_TOKEN", newName: "APEX_RUNTIME_OVERRIDE_G1_ABC"},
		{name: "absent old name", script: ordinary, oldName: "ABSENT_TOKEN", newName: "NEW_TOKEN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := transformScriptSecretReferences(test.script, test.oldName, test.newName); err == nil {
				t.Fatal("transform succeeded; want rejection")
			}
		})
	}
}

func TestDurableScriptMutationsShareSecretReferenceFence(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	release, err := server.acquireSecretReferenceMutation(projectID)
	if err != nil {
		t.Fatalf("acquire secret reference fence: %v", err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := server.runner.StoreScriptDurable(&dsl.Script{
			ID: "fenced-script", Name: "Fenced", Version: "1", Protocol: "http", ProjectID: projectID,
			Actions: []dsl.Action{{Name: "Call", Type: "request", Method: "GET", Target: "/{{secret.FENCED_TOKEN}}"}},
		})
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		release()
		t.Fatalf("script mutation bypassed the secret reference fence: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("script mutation after fence release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("script mutation did not resume after fence release")
	}
}

func TestSecretReferenceMutationFenceCoordinatesControllerReplicas(t *testing.T) {
	_, store, _, projectID := newProjectSecretAPIServer(t)
	first := &projectMutationCoordinator{}
	second := &projectMutationCoordinator{}
	first.configureDurableLease(store)
	second.configureDurableLease(store)

	releaseFirst, err := first.acquireDurable(projectID)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	acquiredSecond := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		release, acquireErr := second.acquireDurable(projectID)
		if acquireErr != nil {
			errs <- acquireErr
			return
		}
		acquiredSecond <- release
	}()
	select {
	case release := <-acquiredSecond:
		release()
		releaseFirst()
		t.Fatal("second controller bypassed the durable project fence")
	case err := <-errs:
		releaseFirst()
		t.Fatalf("second acquire failed: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	releaseFirst()
	select {
	case release := <-acquiredSecond:
		release()
	case err := <-errs:
		t.Fatalf("second acquire after release failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second controller did not acquire the released durable fence")
	}
}

func TestDedicatedSecretCreateRejectsChangedConsumers(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	first := storedSecretReferenceScript(t, server, projectID, "dedicated-first", "First", "DEDICATED_TOKEN")
	usage, err := server.projectSecretUsage(projectID, "DEDICATED_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	storedSecretReferenceScript(t, server, projectID, "dedicated-second", "Second", "DEDICATED_TOKEN")

	create := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
		"secrets": []map[string]any{{
			"name": "DEDICATED_TOKEN", "value": "must-not-commit",
			"expected_usage": map[string]any{"usage_revision": usage.UsageRevision, "exclusive_script_id": first.ID},
		}},
	})
	if create.Code != http.StatusConflict {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	if _, _, err := managed.Get(context.Background(), projectID, "DEDICATED_TOKEN"); err != secretstore.ErrNotFound {
		t.Fatalf("stale dedicated create persisted a value: %v", err)
	}
}

func TestSharedRotationRequiresCurrentUsageAndAcknowledgement(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	if _, err := managed.Create(context.Background(), projectID, "ROTATE_SHARED", "original", "admin"); err != nil {
		t.Fatal(err)
	}
	storedSecretReferenceScript(t, server, projectID, "rotate-first", "First", "ROTATE_SHARED")
	storedSecretReferenceScript(t, server, projectID, "rotate-second", "Second", "ROTATE_SHARED")
	usage, err := server.projectSecretUsage(projectID, "ROTATE_SHARED")
	if err != nil {
		t.Fatal(err)
	}

	withoutAck := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/ROTATE_SHARED", map[string]any{
		"value": "replacement", "expected_usage_revision": usage.UsageRevision,
	})
	if withoutAck.Code != http.StatusConflict {
		t.Fatalf("without acknowledgement = %d body=%s", withoutAck.Code, withoutAck.Body.String())
	}
	stale := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/ROTATE_SHARED", map[string]any{
		"value": "replacement", "expected_usage_revision": "stale", "acknowledge_shared_usage": true,
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale revision = %d body=%s", stale.Code, stale.Body.String())
	}
	rotated := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/ROTATE_SHARED", map[string]any{
		"value": "replacement", "expected_usage_revision": usage.UsageRevision, "acknowledge_shared_usage": true,
	})
	if rotated.Code != http.StatusOK || strings.Contains(rotated.Body.String(), "replacement") {
		t.Fatalf("rotation = %d body=%s", rotated.Code, rotated.Body.String())
	}
}

func TestUsageEvidenceAndRotationFailClosedWhenScriptReferencesAreUnavailable(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	if _, err := managed.Create(context.Background(), projectID, "OUTAGE_SHARED", "original", "admin"); err != nil {
		t.Fatal(err)
	}
	storedSecretReferenceScript(t, server, projectID, "outage-first", "First", "OUTAGE_SHARED")
	storedSecretReferenceScript(t, server, projectID, "outage-second", "Second", "OUTAGE_SHARED")
	server.runner.scriptStore = failingSecretUsageScriptPersistence{
		scriptPersistence: server.runner.scriptStore,
		err:               errors.New("script reference store unavailable"),
	}

	usage := secretAPIRequest(t, server, http.MethodGet, "/api/v1/projects/"+projectID+"/secrets/OUTAGE_SHARED/usage", nil)
	if usage.Code != http.StatusServiceUnavailable {
		t.Fatalf("usage outage status = %d body=%s", usage.Code, usage.Body.String())
	}
	rotation := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/OUTAGE_SHARED", map[string]any{
		"value": "must-not-commit", "expected_usage_revision": "unknown", "acknowledge_shared_usage": true,
	})
	if rotation.Code != http.StatusServiceUnavailable {
		t.Fatalf("rotation outage status = %d body=%s", rotation.Code, rotation.Body.String())
	}
	if value, _, err := managed.Get(context.Background(), projectID, "OUTAGE_SHARED"); err != nil || value != "original" {
		t.Fatalf("rotation outage changed value = %q, %v", value, err)
	}
	create := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
		"secrets": []map[string]any{{
			"name": "OUTAGE_DEDICATED", "value": "must-not-commit",
			"expected_usage": map[string]any{"usage_revision": "unknown"},
		}},
	})
	if create.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected-usage create outage status = %d body=%s", create.Code, create.Body.String())
	}
	if _, _, err := managed.Get(context.Background(), projectID, "OUTAGE_DEDICATED"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("usage outage create persisted a value: %v", err)
	}
}

func TestRebindRouteRequiresManageScriptsAccess(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/secret-references/rebind", strings.NewReader(`{"script":{"project_id":"project-a"},"old_name":"OLD","new_name":"NEW"}`))
	requirement, ok := accessRequirementForRequest(req)
	if !ok || requirement.ProjectLookup == nil || !containsRoleString(requirement.GlobalRoles, RoleEditor) || containsRoleString(requirement.GlobalRoles, RoleViewer) {
		t.Fatalf("rebind access = %+v ok=%t", requirement, ok)
	}
}

func TestScriptSecretReferenceLifecycleKeepsNamesAuthoritative(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	if _, err := managed.Create(context.Background(), projectID, "LIFECYCLE_TOKEN", "source-value", "admin"); err != nil {
		t.Fatal(err)
	}
	source := storedSecretReferenceScript(t, server, projectID, "lifecycle-source", "Lifecycle source", "LIFECYCLE_TOKEN")

	clone := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+source.ID+"/clone-draft", map[string]any{})
	if clone.Code != http.StatusOK {
		t.Fatalf("clone status = %d body=%s", clone.Code, clone.Body.String())
	}
	var cloned struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(clone.Body.Bytes(), &cloned); err != nil {
		t.Fatal(err)
	}
	if got := scriptSecretReferences(&cloned.Script); len(got) != 1 || got[0] != "LIFECYCLE_TOKEN" {
		t.Fatalf("same-project clone references = %#v", got)
	}

	withoutReference := cloneScriptDefinition(source.Script)
	withoutReference.Actions[0].Target = "/without-secret"
	removed := secretAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+source.ID, map[string]any{"script": withoutReference})
	if removed.Code != http.StatusOK {
		t.Fatalf("remove reference status = %d body=%s", removed.Code, removed.Body.String())
	}
	if usage, err := server.projectSecretUsage(projectID, "LIFECYCLE_TOKEN"); err != nil || usage.ConsumerCount != 0 {
		t.Fatalf("usage after final reference removal = %#v", usage)
	}
	if value, _, err := managed.Get(context.Background(), projectID, "LIFECYCLE_TOKEN"); err != nil || value != "source-value" {
		t.Fatalf("reference removal changed vault value = %q, %v", value, err)
	}

	var removedStored StoredScript
	if err := json.Unmarshal(removed.Body.Bytes(), &removedStored); err != nil {
		t.Fatal(err)
	}
	withReference := cloneScriptDefinition(removedStored.Script)
	withReference.Actions[0].Target = "/again/{{secret.LIFECYCLE_TOKEN}}"
	added := secretAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+source.ID, map[string]any{"script": withReference})
	addedUsage, usageErr := server.projectSecretUsage(projectID, "LIFECYCLE_TOKEN")
	if added.Code != http.StatusOK || usageErr != nil || addedUsage.ConsumerCount != 1 {
		t.Fatalf("add reference status = %d body=%s", added.Code, added.Body.String())
	}

	destinationProjectID := "project-secret-destination"
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: destinationProjectID, Name: "Destination", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	crossProjectCopy := cloneScriptDefinition(withReference)
	crossProjectCopy.ID = ""
	crossProjectCopy.ProjectID = destinationProjectID
	crossProjectCopy.CreatedAt = time.Time{}
	crossProjectCopy.UpdatedAt = time.Time{}
	crossProject := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{
		"project_id": destinationProjectID, "script": crossProjectCopy,
	})
	if crossProject.Code != http.StatusCreated {
		t.Fatalf("cross-project copy status = %d body=%s", crossProject.Code, crossProject.Body.String())
	}
	var destination StoredScript
	if err := json.Unmarshal(crossProject.Body.Bytes(), &destination); err != nil {
		t.Fatal(err)
	}
	status := secretAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+destination.ID+"/secret-status", nil)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"missing":["LIFECYCLE_TOKEN"]`) {
		t.Fatalf("destination status = %d body=%s", status.Code, status.Body.String())
	}
}

func TestKafkaOAuthLogicalSecretReferenceRebind(t *testing.T) {
	for _, authored := range []string{"kafka_oauth", "{{secret.kafka_oauth}}"} {
		script := &dsl.Script{Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{OAuth: &dsl.KafkaOAuthConfig{ClientSecretRef: authored}}}}
		updated, paths, count, err := transformScriptSecretReferences(script, "kafka_oauth", "replacement_oauth")
		if err != nil || count != 1 || len(paths) != 1 {
			t.Fatalf("rebind count=%d paths=%v err=%v", count, paths, err)
		}
		if !strings.Contains(updated.Config.Kafka.OAuth.ClientSecretRef, "REPLACEMENT_OAUTH") || script.Config.Kafka.OAuth.ClientSecretRef != authored {
			t.Fatal("secret reference was not immutably rebound")
		}
	}
}
