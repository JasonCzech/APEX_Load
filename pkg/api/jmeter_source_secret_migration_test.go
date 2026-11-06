package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/protocol/eventhubs"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

// newJMeterMigrationServer is a project-secret server with the admin
// migration policy set as given and a protected source-session store, which
// holds the converter's value-bearing requirements between review and
// migration.
func newJMeterMigrationServer(t *testing.T, allow bool) (*Server, secretstore.Store, string) {
	t.Helper()
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = allow
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x3b}, 32),
		KeyID: "jmeter-source-secret-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{Records: records, Wrapper: wrapper}
	return server, managed, projectID
}

// Synthetic credentials substituted for the fixture's "***" placeholders, so
// the converter has recoverable source values to offer for migration.
const (
	jmeterMigrationEventHubsKey    = "synthetic-eventhubs-key-canary-4f1a"
	jmeterMigrationKafkaClientID   = "synthetic-kafka-client-canary-9b2c"
	jmeterMigrationKafkaClientSecr = "synthetic-kafka-secret-canary-6d3e"
)

func jmeterMigrationFixture(t *testing.T) []byte {
	t.Helper()
	source, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for placeholder, value := range map[string]string{
		`<stringProp name="sharedAccessKey">***</stringProp>`: `<stringProp name="sharedAccessKey">` + jmeterMigrationEventHubsKey + `</stringProp>`,
		// The standard OAuth option names; the fixture's cid/cs spelling is
		// gated as nonstandard and carries no value to migrate.
		`cid="***" cs="***"`: `clientId="` + jmeterMigrationKafkaClientID + `" clientSecret="` + jmeterMigrationKafkaClientSecr + `"`,
	} {
		if !strings.Contains(text, placeholder) {
			t.Fatalf("fixture no longer contains %q", placeholder)
		}
		text = strings.ReplaceAll(text, placeholder, value)
	}
	return []byte(text)
}

func postJMeterMigrationImport(t *testing.T, server *Server, endpoint, projectID string, source []byte) map[string]any {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("project_id", projectID); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "messaging.jmx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, endpoint+"?project_id="+projectID, &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s status = %d: %s", endpoint, recorder.Code, recorder.Body.String())
	}
	for _, canary := range []string{jmeterMigrationEventHubsKey, jmeterMigrationKafkaClientID, jmeterMigrationKafkaClientSecr} {
		if strings.Contains(recorder.Body.String(), canary) {
			t.Fatalf("%s response disclosed a source credential value", endpoint)
		}
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

// A JMeter messaging import hands the converter's value-bearing requirements
// to the source-secret migration session, as HAR imports do: with the admin
// policy on, the review offers source_secret_session_id and marks the Event
// Hubs and Kafka requirements source_value_available, while no value reaches
// the JSON. The migrated Event Hubs string is the hub-scoped one the converter
// composed, and the runtime accepts it for the script's hub.
func TestJMeterImportOffersSourceSecretMigration(t *testing.T) {
	server, managed, projectID := newJMeterMigrationServer(t, true)
	source := jmeterMigrationFixture(t)

	response := postJMeterMigrationImport(t, server, "/api/v1/scripts/import/jmeter", projectID, source)
	sessionID, _ := response["source_secret_session_id"].(string)
	if strings.TrimSpace(sessionID) == "" || response["source_secret_migration_allowed"] != true {
		t.Fatalf("JMeter import did not offer source-secret migration: session=%q allowed=%v", sessionID, response["source_secret_migration_allowed"])
	}
	raw, _ := json.Marshal(response["secret_binding_requirements"])
	var requirements []SecretBindingRequirement
	if err := json.Unmarshal(raw, &requirements); err != nil {
		t.Fatal(err)
	}
	available := map[string]SecretBindingRequirement{}
	for _, requirement := range requirements {
		if requirement.SourceValueAvailable {
			available[requirement.Path] = requirement
		}
	}
	eventHubs, ok := available["/config/eventhubs/connection_string"]
	if !ok {
		t.Fatalf("Event Hubs requirement is not migratable: %+v", requirements)
	}
	if _, ok := available["/config/kafka/oauth/client_secret_ref"]; !ok {
		t.Fatalf("Kafka OAuth client secret is not migratable: %+v", requirements)
	}

	const secretName = "JMETER_EVENTHUBS_MIGRATED"
	migration := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
		"secrets":                  []map[string]string{{"requirement_id": eventHubs.ID, "secret_name": secretName}},
	})
	if migration.Code != http.StatusCreated || strings.Contains(migration.Body.String(), jmeterMigrationEventHubsKey) {
		t.Fatalf("migration = %d %s", migration.Code, migration.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, secretName)
	if err != nil || !strings.Contains(value, "SharedAccessKey="+jmeterMigrationEventHubsKey) {
		t.Fatalf("migrated Event Hubs value is not the composed connection string (err=%v)", err)
	}

	raw, _ = json.Marshal(response["scripts"])
	var scripts []dsl.Script
	if err := json.Unmarshal(raw, &scripts); err != nil {
		t.Fatal(err)
	}
	if eventHubs.ScriptIndex < 0 || eventHubs.ScriptIndex >= len(scripts) {
		t.Fatalf("Event Hubs requirement names script %d of %d ", eventHubs.ScriptIndex, len(scripts))
	}
	hub := scripts[eventHubs.ScriptIndex].Config.EventHubs.EventHub
	resolved, err := eventhubs.ResolveConnectionStringEventHub(value, hub)
	if err != nil || !strings.EqualFold(resolved, hub) || hub == "" {
		t.Fatalf("runtime rejected the migrated hub-scoped string for hub %q: resolved=%q err=%v", hub, resolved, err)
	}
}

// The validate route returns the same review, so it offers the same session;
// without the admin policy no session is offered and values stay private.
func TestJMeterImportSourceSecretMigrationFollowsPolicy(t *testing.T) {
	server, _, projectID := newJMeterMigrationServer(t, false)
	source := jmeterMigrationFixture(t)
	for _, endpoint := range []string{"/api/v1/scripts/import/jmeter", "/api/v1/scripts/import/jmeter/validate"} {
		response := postJMeterMigrationImport(t, server, endpoint, projectID, source)
		if sessionID, _ := response["source_secret_session_id"].(string); sessionID != "" {
			t.Fatalf("%s offered a session with migration disabled", endpoint)
		}
	}
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	response := postJMeterMigrationImport(t, server, "/api/v1/scripts/import/jmeter/validate", projectID, source)
	if sessionID, _ := response["source_secret_session_id"].(string); strings.TrimSpace(sessionID) == "" {
		t.Fatal("validate route did not offer source-secret migration")
	}
}

// In production every server that can migrate source secrets also has the
// protected batch store, so a messaging import always arrives as a batch, and
// its review goes through batch repairs (confirm_reference_context is required
// for every messaging plan). The batch must offer the session, and an applied
// repair must hand back a live replacement session with the requirements
// still marked source_value_available, never the values.
func TestJMeterBatchImportKeepsSourceSecretMigrationThroughRepairs(t *testing.T) {
	server, managed, projectID := newJMeterMigrationServer(t, true)
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5c}, 32), KeyID: "jmeter-batch-migration"})
	if err != nil {
		t.Fatal(err)
	}
	server.brunoArtifactDir, server.brunoArtifactWrapper = t.TempDir(), wrapper
	source := jmeterMigrationFixture(t)

	imported := postJMeterMigrationImport(t, server, "/api/v1/scripts/import/jmeter", projectID, source)
	if imported["batch"] == nil {
		t.Fatal("messaging import on a migration-capable server did not create a batch")
	}
	sessionID, _ := imported["source_secret_session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatal("batch import did not offer source-secret migration")
	}
	id := decodeBatchIdentity(t, imported["batch"])
	repair := func(apply bool, req ImportRepairRequest) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		body, _ := json.Marshal(req)
		endpoint := "/api/v1/scripts/import/repairs/preview"
		if apply {
			endpoint = "/api/v1/scripts/import/repairs/apply"
		}
		request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", endpoint, recorder.Code, recorder.Body.String())
		}
		for _, canary := range []string{jmeterMigrationEventHubsKey, jmeterMigrationKafkaClientID, jmeterMigrationKafkaClientSecr} {
			if strings.Contains(recorder.Body.String(), canary) {
				t.Fatalf("%s disclosed a source credential value", endpoint)
			}
		}
		var response map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return recorder, response
	}
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: projectID, Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	_, preview := repair(false, req)
	req.PreviewDigest, _ = preview["preview_digest"].(string)
	appliedRecorder, applied := repair(true, req)

	replacement, _ := applied["source_secret_session_id"].(string)
	if strings.TrimSpace(replacement) == "" || replacement == sessionID || applied["source_secret_migration_allowed"] != true {
		t.Fatalf("applied repair did not carry the migration offer: session=%q allowed=%v", replacement, applied["source_secret_migration_allowed"])
	}
	raw, _ := json.Marshal(applied["secret_binding_requirements"])
	var requirements []SecretBindingRequirement
	if err := json.Unmarshal(raw, &requirements); err != nil {
		t.Fatal(err)
	}
	var eventHubs *SecretBindingRequirement
	for index := range requirements {
		if requirements[index].SourceValueAvailable && requirements[index].Path == "/config/eventhubs/connection_string" {
			eventHubs = &requirements[index]
		}
	}
	if eventHubs == nil {
		t.Fatalf("repaired batch lost the migratable Event Hubs requirement: %+v", requirements)
	}

	// The consumed session is gone; the replacement migrates.
	migrate := func(session string) *httptest.ResponseRecorder {
		return secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
			"project_id":               projectID,
			"source_secret_session_id": session,
			"secrets":                  []map[string]string{{"requirement_id": eventHubs.ID, "secret_name": "JMETER_BATCH_EVENTHUBS"}},
		})
	}
	if stale := migrate(sessionID); stale.Code == http.StatusCreated {
		t.Fatal("the session the repair consumed still migrated")
	}
	migration := migrate(replacement)
	if migration.Code != http.StatusCreated || strings.Contains(migration.Body.String(), jmeterMigrationEventHubsKey) {
		t.Fatalf("migration with the repaired session = %d %s", migration.Code, migration.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "JMETER_BATCH_EVENTHUBS")
	if err != nil || !strings.Contains(value, "SharedAccessKey="+jmeterMigrationEventHubsKey) {
		t.Fatalf("migrated value is not the source connection string (err=%v)", err)
	}

	// A replayed apply returns the same receipt, and a repair after the
	// session was migrated tells the client to drop its ID.
	if again, _ := repair(true, req); batchJSONDigest(again.Body.Bytes()) != batchJSONDigest(appliedRecorder.Body.Bytes()) {
		t.Fatal("replayed apply did not return the published receipt")
	}
	next := decodeBatchIdentity(t, applied["batch"])
	second := ImportRepairRequest{BatchID: next.ID, Revision: next.Revision, ProjectID: projectID, Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	_, preview = repair(false, second)
	second.PreviewDigest, _ = preview["preview_digest"].(string)
	_, afterMigration := repair(true, second)
	if got, present := afterMigration["source_secret_session_id"]; !present || got != "" {
		t.Fatalf("repair after migration source_secret_session_id = %#v, want \"\"", got)
	}
}

// jmeterBatchFaultStore fails JMeter batch saves or receipt publication on
// demand, after which the test disarms it and retries.
type jmeterBatchFaultStore struct {
	*importartifact.EncryptedStore
	failBatchSave, failPublish bool
}

func (s *jmeterBatchFaultStore) SaveSessionBytes(ctx context.Context, descriptor importsession.SessionDescriptor, payload []byte, maxBytes int64) error {
	if s.failBatchSave && descriptor.Profile == jmeterBatchProfile {
		return errors.New("simulated batch save failure")
	}
	return s.EncryptedStore.SaveSessionBytes(ctx, descriptor, payload, maxBytes)
}

func (s *jmeterBatchFaultStore) PublishClaimReceipt(ctx context.Context, scope importsession.Scope, token string, receipt importsession.SessionDescriptor, payload []byte, maxBytes int64) error {
	if s.failPublish {
		return errors.New("simulated receipt publication failure")
	}
	return s.EncryptedStore.PublishClaimReceipt(ctx, scope, token, receipt, payload, maxBytes)
}

// createdSourceSessionSpy records every protected source session created.
type createdSourceSessionSpy struct {
	storage.ProtectedSourceSecretSessionStore
	created []string
}

func (s *createdSourceSessionSpy) CreateProtectedSourceSecretSession(session *storage.ProtectedSourceSecretSessionCipher) (bool, error) {
	created, err := s.ProtectedSourceSecretSessionStore.CreateProtectedSourceSecretSession(session)
	if created && err == nil {
		s.created = append(s.created, session.ID)
	}
	return created, err
}

func newJMeterBatchFaultServer(t *testing.T) (*Server, *jmeterBatchFaultStore, *createdSourceSessionSpy, string) {
	t.Helper()
	server, _, projectID := newJMeterMigrationServer(t, true)
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5c}, 32), KeyID: "jmeter-batch-fault"})
	if err != nil {
		t.Fatal(err)
	}
	server.brunoArtifactDir, server.brunoArtifactWrapper = t.TempDir(), wrapper
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	faults := &jmeterBatchFaultStore{EncryptedStore: objects}
	if server.importSessions, err = importsession.NewManager(faults); err != nil {
		t.Fatal(err)
	}
	spy := &createdSourceSessionSpy{ProtectedSourceSecretSessionStore: server.protectedSourceSessions.Records}
	server.protectedSourceSessions.Records = spy
	return server, faults, spy, projectID
}

// A batch import whose batch cannot be saved never hands out its session ID,
// so the session holding the source credential values is discarded at once
// instead of waiting out its TTL.
func TestJMeterBatchImportSaveFailureDiscardsSourceSecretSession(t *testing.T) {
	server, faults, spy, projectID := newJMeterBatchFaultServer(t)
	faults.failBatchSave = true
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("project_id", projectID)
	part, _ := form.CreateFormFile("file", "messaging.jmx")
	_, _ = part.Write(jmeterMigrationFixture(t))
	_ = form.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter?project_id="+projectID, &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("import with a failing batch save = %d, want 503", recorder.Code)
	}
	if len(spy.created) == 0 {
		t.Fatal("import registered no source-secret session; the test no longer covers the failure path")
	}
	_, actor, _ := server.resolveAuditActor(request)
	for index, id := range spy.created {
		if _, err := spy.ClaimProtectedSourceSecretSession(id, projectID, actor, "probe", time.Now().UTC(), time.Now().UTC().Add(time.Minute)); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("source-secret session %d of %d survived the failed batch import (claim err=%v)", index+1, len(spy.created), err)
		}
	}
}

// A batch repair whose replacement batch or receipt cannot be stored must
// leave the migration offer in place for the retry the error asks for: the
// stored revision still names the session the repair consumed.
func TestJMeterBatchRepairFailureKeepsSourceSecretMigrationForRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		arm    func(*jmeterBatchFaultStore, bool)
		status int
	}{
		{"batch save", func(f *jmeterBatchFaultStore, on bool) { f.failBatchSave = on }, http.StatusServiceUnavailable},
		{"receipt publication", func(f *jmeterBatchFaultStore, on bool) { f.failPublish = on }, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, faults, _, projectID := newJMeterBatchFaultServer(t)
			imported := postJMeterMigrationImport(t, server, "/api/v1/scripts/import/jmeter", projectID, jmeterMigrationFixture(t))
			if imported["batch"] == nil || imported["source_secret_session_id"] == "" {
				t.Fatal("batch import did not offer source-secret migration")
			}
			id := decodeBatchIdentity(t, imported["batch"])
			repair := func(endpoint string, req ImportRepairRequest) (int, map[string]any) {
				t.Helper()
				body, _ := json.Marshal(req)
				request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				server.mux.ServeHTTP(recorder, request)
				var response map[string]any
				_ = json.Unmarshal(recorder.Body.Bytes(), &response)
				return recorder.Code, response
			}
			req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: projectID, Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
			if code, preview := repair("/api/v1/scripts/import/repairs/preview", req); code == http.StatusOK {
				req.PreviewDigest, _ = preview["preview_digest"].(string)
			} else {
				t.Fatalf("preview = %d", code)
			}
			tc.arm(faults, true)
			if code, _ := repair("/api/v1/scripts/import/repairs/apply", req); code != tc.status {
				t.Fatalf("apply with a failing %s = %d, want %d", tc.name, code, tc.status)
			}
			tc.arm(faults, false)
			code, applied := repair("/api/v1/scripts/import/repairs/apply", req)
			if code != http.StatusOK {
				t.Fatalf("retried apply = %d", code)
			}
			replacement, _ := applied["source_secret_session_id"].(string)
			if strings.TrimSpace(replacement) == "" {
				t.Fatalf("retried apply lost the migration offer: source_secret_session_id = %#v", applied["source_secret_session_id"])
			}
			raw, _ := json.Marshal(applied["secret_binding_requirements"])
			var requirements []SecretBindingRequirement
			if err := json.Unmarshal(raw, &requirements); err != nil {
				t.Fatal(err)
			}
			var eventHubs string
			for _, requirement := range requirements {
				if requirement.SourceValueAvailable && requirement.Path == "/config/eventhubs/connection_string" {
					eventHubs = requirement.ID
				}
			}
			migration := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
				"project_id":               projectID,
				"source_secret_session_id": replacement,
				"secrets":                  []map[string]string{{"requirement_id": eventHubs, "secret_name": "JMETER_RETRY_EVENTHUBS"}},
			})
			if eventHubs == "" || migration.Code != http.StatusCreated {
				t.Fatalf("migration after the retried apply = %d (requirement %q)", migration.Code, eventHubs)
			}
		})
	}
}
