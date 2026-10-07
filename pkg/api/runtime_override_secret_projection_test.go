package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRuntimeOverrideCredentialsBecomeMemoryOnlySecretReferences(t *testing.T) {
	const secret = "runtime-override-a&b"
	script := &dsl.Script{
		Name:     "runtime projection",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Target: "https://example.test/resource?q={{env.API_TOKEN}}",
		}},
	}
	overrides := &RuntimeSettingsOverride{
		Env:     map[string]string{"API_TOKEN": secret},
		BaseURL: "https://user:" + secret + "@example.test",
		HTTP: &dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Username: "runtime-user", Password: secret},
			Proxy: dsl.ProxyConfig{
				Username: "proxy-user",
				Password: secret,
			},
			DefaultHeaders: map[string]string{"Authorization": "Bearer " + secret},
		},
		BrowserConfig: &dsl.BrowserConfig{
			HTTPCredentials: dsl.BrowserHTTPCredentials{Username: "browser-user", Password: secret},
			Proxy:           dsl.BrowserProxyConfig{Username: "browser-proxy-user", Password: secret},
			ClientCertificates: []dsl.BrowserClientCertificate{{
				Origin: "https://example.test", Passphrase: secret,
			}},
		},
	}

	protected, values, err := protectRuntimeOverrideSecrets("test-scope", 0, script, overrides, true)
	if err != nil {
		t.Fatalf("protectRuntimeOverrideSecrets: %v", err)
	}
	if err := applyRuntimeOverrides(script, protected); err != nil {
		t.Fatalf("applyRuntimeOverrides: %v", err)
	}
	values, err = filterReferencedRuntimeSecretValues(script, values)
	if err != nil {
		t.Fatalf("filterReferencedRuntimeSecretValues: %v", err)
	}
	if len(values) == 0 {
		t.Fatal("runtime secret overlay is empty")
	}
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), secret) || strings.Contains(string(payload), "runtime-user") || strings.Contains(string(payload), "proxy-user") {
		t.Fatalf("protected script retained runtime credentials: %s", payload)
	}
	if !strings.Contains(string(payload), "{{secret."+runtimeOverrideSecretPrefix) {
		t.Fatalf("protected script has no runtime references: %s", payload)
	}

	resolver := overlayRuntimeSecretResolver(values, engine.SecretResolverFunc(func(string) (string, bool) { return "", false }))
	bundle, err := resolveDistributedSecretBundle(script, resolver)
	if err != nil {
		t.Fatalf("resolveDistributedSecretBundle: %v", err)
	}
	if len(bundle) != len(values) {
		t.Fatalf("bundle entries = %d, want %d", len(bundle), len(values))
	}
	content, _, _, err := generatorrpc.PrepareDistributedScript(script)
	if err != nil {
		t.Fatalf("PrepareDistributedScript: %v", err)
	}
	if strings.Contains(string(content), secret) {
		t.Fatalf("distributed script retained credential: %s", content)
	}
}

func TestRuntimeOverrideMixedLiteralAndSecretReferenceIsRejected(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}}
	for _, value := range []string{"literal-{{secret.EXISTING}}", "literal-{{ secret.EXISTING }}"} {
		_, _, err := protectRuntimeOverrideSecrets("test-scope", 0, script, &RuntimeSettingsOverride{
			HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: value}},
		}, true)
		if err == nil || !strings.Contains(err.Error(), "mixes literal material") {
			t.Fatalf("mixed runtime credential %q error = %v", value, err)
		}
	}
	protected, values, err := protectRuntimeOverrideSecrets("test-scope", 0, script, &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Token: "{{secret.EXISTING}}"}},
	}, true)
	if err != nil || protected.HTTP.Auth.Token != "{{secret.EXISTING}}" || len(values) != 0 {
		t.Fatalf("exact existing reference = %#v values %#v err %v", protected, values, err)
	}
}

func TestRuntimeOverrideReferenceNamesDoNotFingerprintValues(t *testing.T) {
	project := func(value string) (string, string) {
		t.Helper()
		script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "{{env.API_TOKEN}}"}}}
		protected, values, err := protectRuntimeOverrideSecrets("test-scope", 2, script, &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": value}}, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyRuntimeOverrides(script, protected); err != nil {
			t.Fatal(err)
		}
		for name := range values {
			return name, script.Actions[0].Target
		}
		t.Fatal("no projected runtime secret")
		return "", ""
	}
	firstName, firstTarget := project("first-runtime-secret")
	secondName, secondTarget := project("different-runtime-secret")
	if firstName != secondName || firstTarget != secondTarget {
		t.Fatalf("reference identity changed with value: %q/%q vs %q/%q", firstName, firstTarget, secondName, secondTarget)
	}
}

type runtimeProjectionSecretStore struct {
	mu            sync.Mutex
	values        map[string]string
	creators      map[string]string
	createCalls   int
	rollbackCalls int
	sawDeadline   bool
	createErr     error
	failAfter     int
	commitThenErr bool
	rollbackErr   error
	attemptStore  storage.RuntimeSecretProvisioningAttemptStore
	sawJournal    bool
}

func (s *runtimeProjectionSecretStore) Get(_ context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[projectID+"/"+name]
	if !ok {
		return "", secretstore.Metadata{}, secretstore.ErrNotFound
	}
	return value, secretstore.Metadata{ProjectID: projectID, Name: name, Provider: s.Provider(), Writable: true, CreatedBy: s.creators[projectID+"/"+name]}, nil
}

func (s *runtimeProjectionSecretStore) Create(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	_, s.sawDeadline = ctx.Deadline()
	if s.attemptStore != nil {
		attempts, err := s.attemptStore.ListRuntimeSecretProvisioningAttempts()
		s.sawJournal = err == nil && len(attempts) > 0
	}
	if s.createErr != nil && (s.failAfter <= 0 || s.createCalls > s.failAfter) {
		if s.commitThenErr {
			key := projectID + "/" + name
			s.values[key] = value
			if s.creators == nil {
				s.creators = make(map[string]string)
			}
			s.creators[key] = actor
		}
		return secretstore.Metadata{}, s.createErr
	}
	key := projectID + "/" + name
	if _, exists := s.values[key]; exists {
		return secretstore.Metadata{}, secretstore.ErrAlreadyExists
	}
	s.values[key] = value
	if s.creators == nil {
		s.creators = make(map[string]string)
	}
	s.creators[key] = actor
	return secretstore.Metadata{ProjectID: projectID, Name: name, Provider: s.Provider(), Writable: true, CreatedAt: time.Now(), CreatedBy: actor}, nil
}

func (s *runtimeProjectionSecretStore) RollbackCreated(_ context.Context, projectID, name, actor string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollbackCalls++
	if s.rollbackErr != nil {
		return false, s.rollbackErr
	}
	key := projectID + "/" + name
	if s.creators[key] != actor {
		return false, nil
	}
	delete(s.values, key)
	delete(s.creators, key)
	return true, nil
}

func (s *runtimeProjectionSecretStore) Replace(_ context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := projectID + "/" + name
	if _, exists := s.values[key]; !exists {
		return secretstore.Metadata{}, secretstore.ErrNotFound
	}
	s.values[key] = value
	return secretstore.Metadata{ProjectID: projectID, Name: name, Provider: s.Provider(), Writable: true, CreatedBy: actor}, nil
}
func (*runtimeProjectionSecretStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return nil, nil
}
func (*runtimeProjectionSecretStore) Writable() bool   { return true }
func (*runtimeProjectionSecretStore) Provider() string { return "runtime-test" }

type failCreatedRuntimeAuditStore struct {
	scenarioPersistence
	err error
}

func (s *failCreatedRuntimeAuditStore) SaveAuditEvent(event *storage.AuditEvent) error {
	if event != nil && event.Action == "project_secret.runtime_override.create" && event.Details["outcome"] == "created" {
		return s.err
	}
	return s.scenarioPersistence.SaveAuditEvent(event)
}

type panicRuntimeAuditStore struct {
	scenarioPersistence
}

func (*panicRuntimeAuditStore) SaveAuditEvent(*storage.AuditEvent) error {
	panic("injected runtime credential audit panic")
}

func TestManagedTestPersistenceContainsReferencesNotRuntimeCredentialValues(t *testing.T) {
	const secret = "managed-test-runtime-secret"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-projection-script", "Runtime projection")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{
			Env: map[string]string{"API_TOKEN": secret},
		},
		Groups: []CreateGroupEntry{{
			ScriptID: script.ID,
			VUsers:   1,
			RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "bearer", Token: secret},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	apiPayload, err := json.Marshal(testRun)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(apiPayload), secret) || !strings.Contains(string(apiPayload), "{{secret."+runtimeOverrideSecretPrefix) {
		t.Fatalf("managed test API projection is unsafe: %s", apiPayload)
	}
	record, err := records.GetTest(testRun.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	storedPayload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedPayload), secret) || !strings.Contains(string(storedPayload), "{{secret."+runtimeOverrideSecretPrefix) {
		t.Fatalf("managed test persistence is unsafe: %s", storedPayload)
	}
	secrets.mu.Lock()
	found := false
	for _, value := range secrets.values {
		found = found || value == secret
	}
	sawDeadline := secrets.sawDeadline
	sawJournal := secrets.sawJournal
	secrets.mu.Unlock()
	if !found {
		t.Fatal("runtime credential was not stored in the governed secret store")
	}
	if !sawDeadline {
		t.Fatal("governed secret provider call did not receive a bounded context")
	}
	if !sawJournal {
		t.Fatal("governed secret provider was called before the durable provisioning journal was written")
	}
	server.runner.mu.RLock()
	managed := server.runner.tests[testRun.ID]
	if managed == nil || len(managed.Groups) != 1 || len(managed.groupSpecs) != 1 {
		server.runner.mu.RUnlock()
		t.Fatalf("managed runtime state missing after creation: %#v", managed)
	}
	retainedOverlay := len(managed.Groups[0].runtimeSecretValues)
	retainedMatcher := managed.groupSpecs[0].RedactionConfig != nil && managed.groupSpecs[0].RedactionConfig.HasRuntimeSecretMaterial()
	retainedResolver := managed.groupSpecs[0].SecretResolver != nil
	server.runner.mu.RUnlock()
	if retainedOverlay != 0 || retainedMatcher || retainedResolver {
		t.Fatalf("provisioned plaintext remained in queued test memory: overlay=%d matcher=%t resolver=%t", retainedOverlay, retainedMatcher, retainedResolver)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("completed provisioning journals = %#v, %v", attempts, err)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{
		Action: "project_secret.runtime_override.create",
		Limit:  100,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	outcomes := make(map[string]bool)
	for _, event := range events {
		if encoded, marshalErr := json.Marshal(event); marshalErr != nil {
			t.Fatal(marshalErr)
		} else if strings.Contains(string(encoded), secret) {
			t.Fatalf("runtime secret audit disclosed the credential: %s", encoded)
		}
		outcomes[event.Details["outcome"].(string)] = true
	}
	if !outcomes["pending"] || !outcomes["created"] {
		t.Fatalf("runtime secret audit outcomes = %#v, want pending and created", outcomes)
	}
}

func TestScheduledTestUsesRotatedGovernedRuntimeCredentialAtLaunch(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)

	observed := make(chan string, 4)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.URL.Query().Get("token")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	script := httpScript("scheduled-runtime-rotation", "Scheduled runtime rotation")
	script.Config.BaseURL = target.URL
	script.Config.RunLogic.Iterations = 1
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)
	start := time.Now().Add(time.Hour)
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Duration:         "250ms",
		ScheduledStartAt: &start,
		RuntimeSettings: &RuntimeSettingsOverride{
			Env: map[string]string{"API_TOKEN": "old"},
		},
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	name := runtimeOverrideSecretName(testRun.ID, -1, "env.API_TOKEN")
	if _, err := secrets.Replace(context.Background(), storage.DefaultProjectID, name, "new", "admin"); err != nil {
		t.Fatalf("rotate governed runtime secret: %v", err)
	}

	server.runner.mu.RLock()
	managed := server.runner.tests[testRun.ID]
	server.runner.mu.RUnlock()
	runEngine, err := server.runner.buildRunEngine(managed, nil)
	if err != nil {
		t.Fatalf("buildRunEngine after rotation: %v", err)
	}
	if _, err := runEngine.Run(context.Background()); err != nil {
		t.Fatalf("run rotated scheduled test: %v", err)
	}
	select {
	case value := <-observed:
		if value != "new" {
			t.Fatalf("scheduled launch used runtime credential %q, want rotated value", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled test did not reach target")
	}
}

func TestScenarioRuntimeCredentialsAreGovernedAndPersistOnlyReferences(t *testing.T) {
	const secret = "scenario-runtime-secret-value"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)
	script := httpScript("scenario-governed-runtime-script", "Scenario governed runtime")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}

	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:            "Governed scenario",
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": secret}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
		creationActor:   testCreationActor{Type: "user", Subject: "scenario-editor", DisplayName: "Scenario Editor"},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	full, scripts, err := records.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	payload, err := json.Marshal(struct {
		Scenario *storage.Scenario
		Scripts  []storage.ScenarioScript
	}{full, scripts})
	if err != nil {
		t.Fatal(err)
	}
	name := runtimeOverrideSecretName(scenario.ID, -1, "env.API_TOKEN")
	if strings.Contains(string(payload), secret) || !strings.Contains(string(payload), "{{secret."+name+"}}") {
		t.Fatalf("durable scenario runtime boundary is unsafe: %s", payload)
	}
	value, _, err := secrets.Get(context.Background(), storage.DefaultProjectID, name)
	if err != nil || value != secret {
		t.Fatalf("governed scenario value = %q, %v", value, err)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("completed scenario journals = %#v, %v", attempts, err)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.runtime_override.create", Limit: 20})
	if err != nil || len(events) < 2 {
		t.Fatalf("scenario runtime audit events = %#v, %v", events, err)
	}
	for _, event := range events {
		if event.ActorType != "user" || event.ActorSubject != "scenario-editor" || event.Details["scenario_id"] != scenario.ID {
			t.Fatalf("scenario runtime audit attribution = %#v", event)
		}
	}

	secrets.mu.Lock()
	beforeCalls := secrets.createCalls
	secrets.mu.Unlock()
	_, err = server.runner.StoreScenario(CreateScenarioRequest{
		Name:            "Denied service scenario",
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-write"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
		creationActor:   testCreationActor{Type: "service_account", Subject: "service-account:scenario-writer"},
	})
	if !errors.Is(err, errServiceAccountRuntimeSecretWrite) {
		t.Fatalf("service-account scenario save error = %v", err)
	}
	secrets.mu.Lock()
	afterDeniedCalls := secrets.createCalls
	secrets.mu.Unlock()
	if afterDeniedCalls != beforeCalls {
		t.Fatalf("service-account scenario denial occurred after provider call")
	}

	// A service account may materialize the already-configured reference; it is
	// denied only secret-write authority, not ordinary secret resolution.
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		ScenarioID: scenario.ID,
		creationActor: testCreationActor{
			Type: "service_account", Subject: "service-account:runner",
		},
	})
	if err != nil || testRun == nil {
		t.Fatalf("service-account use of configured scenario reference = %#v, %v", testRun, err)
	}
	secrets.mu.Lock()
	afterMaterializeCalls := secrets.createCalls
	secrets.mu.Unlock()
	if afterMaterializeCalls != beforeCalls {
		t.Fatalf("configured scenario materialization made an extra provider create")
	}
}

func TestLegacyScenarioRuntimeCredentialsAreDeferredWithoutProviderWrites(t *testing.T) {
	const secret = "legacy-scenario-plaintext"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	bootstrap := NewServer(ServerConfig{Store: records, MaxConcurrent: 2})
	script := httpScript("legacy-scenario-runtime-script", "Legacy scenario runtime")
	if _, err := bootstrap.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	bootstrap.runner.beginShutdown()
	const scenarioID = "legacy-scenario-runtime"
	now := time.Now().UTC()
	if err := records.SaveScenario(&storage.Scenario{
		ID: scenarioID, Name: "Legacy scenario runtime", Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": secret}},
		CreatedAt:       now, UpdatedAt: now,
	}, []storage.ScenarioScript{{
		ScenarioID: scenarioID, ScriptID: script.ID, VUsers: 1,
		RuntimeSettings: &RuntimeSettingsOverride{
			HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: "[REDACTED]"}},
		},
	}}); err != nil {
		t.Fatalf("SaveScenario(legacy): %v", err)
	}

	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("legacy scenario repair made %d provider writes", createCalls)
	}
	full, scripts, err := records.GetScenario(scenarioID)
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	payload, _ := json.Marshal(struct {
		Scenario *storage.Scenario
		Scripts  []storage.ScenarioScript
	}{full, scripts})
	if strings.Contains(string(payload), secret) || strings.Contains(string(payload), "[REDACTED]") {
		t.Fatalf("legacy scenario repair retained credential material: %s", payload)
	}
	if !strings.Contains(string(payload), "{{secret."+runtimeOverrideSecretName(scenarioID, -1, "env.API_TOKEN")+"}}") ||
		!strings.Contains(string(payload), "{{secret."+runtimeOverrideSecretName(scenarioID, 0, "http.auth.password")+"}}") {
		t.Fatalf("legacy scenario repair did not persist deferred references: %s", payload)
	}
}

func TestServiceAccountTestCreateRejectsRuntimeSecretBeforeProviderCall(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("service-account-runtime-secret-script", "Service account runtime secret")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	body, err := json.Marshal(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-be-written"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	request = withAuthenticatedContext(request, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: "runtime-writer", Name: "Runtime writer", Scopes: []string{"tests:run"}}})
	response := httptest.NewRecorder()
	server.handleCreateTest(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "service accounts cannot create project secrets") {
		t.Fatalf("service-account create = %d %s, want explicit forbidden", response.Code, response.Body.String())
	}
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("service-account denial occurred after %d provider Create calls", createCalls)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("service-account denial journals = %#v, %v", attempts, err)
	}
}

func TestHumanTestCreateAttributesRuntimeSecretAuditToCaller(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("human-runtime-secret-script", "Human runtime secret")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	body, err := json.Marshal(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "human-entered-secret"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	request = withAuthenticatedContext(request, authResolution{User: &AuthenticatedUser{Subject: "human-editor", DisplayName: "Human Editor"}})
	response := httptest.NewRecorder()
	server.handleCreateTest(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("human create = %d %s", response.Code, response.Body.String())
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.runtime_override.create", Limit: 20})
	if err != nil || len(events) < 2 {
		t.Fatalf("runtime secret audit events = %#v, %v", events, err)
	}
	for _, event := range events {
		if event.ActorType != "user" || event.ActorSubject != "human-editor" || event.ActorDisplayName != "Human Editor" {
			t.Fatalf("runtime secret audit actor = %s/%s/%s, want human caller", event.ActorType, event.ActorSubject, event.ActorDisplayName)
		}
	}
}

func TestDraftCapacityEstimateDoesNotPersistRuntimeCredentials(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string)}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-estimate-script", "Runtime estimate")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	if _, err := server.runner.EstimateDraftCapacity(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "estimate-only-secret"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	}); err != nil {
		t.Fatalf("EstimateDraftCapacity: %v", err)
	}
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	valueCount := len(secrets.values)
	secrets.mu.Unlock()
	if createCalls != 0 || valueCount != 0 {
		t.Fatalf("capacity estimate wrote governed secrets: calls=%d values=%d", createCalls, valueCount)
	}
}

func TestFailedTestPersistenceRollsBackCreatedRuntimeCredentials(t *testing.T) {
	const secret = "orphan-audit-runtime-secret"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string)}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-orphan-audit-script", "Runtime orphan audit")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)
	wantErr := errors.New("forced test persistence failure")
	server.runner.testStore = failingTestPersistence{err: wantErr}

	_, err = server.runner.CreateTest(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": secret}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CreateTest error = %v, want %v", err, wantErr)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{
		Action: "project_secret.runtime_override.rollback",
		Status: "success",
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) == 0 || events[0].Details["outcome"] != "rolled_back" || events[0].Details["trigger"] != "test_persistence_failed" {
		t.Fatalf("rollback audit events = %#v", events)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("rollback audit disclosed the credential: %s", encoded)
	}
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	if len(secrets.values) != 0 || secrets.rollbackCalls != 1 {
		t.Fatalf("failed persistence retained runtime credentials: values=%d rollback_calls=%d", len(secrets.values), secrets.rollbackCalls)
	}
}

func TestRuntimeCredentialProviderErrorsAreExactlyRedactedAndAudited(t *testing.T) {
	const secret = "provider-echoed-runtime-secret"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{
		values:    make(map[string]string),
		createErr: errors.New("provider echoed " + secret),
	}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-provider-error-script", "Runtime provider error")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	_, err = server.runner.CreateTest(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": secret}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("CreateTest provider error = %q, want exact-redacted failure", err)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{
		Action: "project_secret.runtime_override.create",
		Status: "failure",
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) == 0 || events[0].Details["outcome"] != "provider_error" {
		t.Fatalf("provider failure audit events = %#v", events)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("provider failure audit disclosed credential: %s", encoded)
	}
}

func TestPartialRuntimeCredentialBatchIsRolledBack(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{
		values:    make(map[string]string),
		createErr: errors.New("second provider create failed"),
		failAfter: 1,
	}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-partial-batch-script", "Runtime partial batch")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}&password={{env.DB_PASSWORD}}"
	server.runner.StoreScript(script)

	_, err = server.runner.CreateTest(CreateTestRequest{
		Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{
			"API_TOKEN":   "first-partial-secret",
			"DB_PASSWORD": "second-partial-secret",
		}},
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err == nil {
		t.Fatal("CreateTest unexpectedly succeeded")
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{
		Action: "project_secret.runtime_override.rollback",
		Status: "success",
		Limit:  20,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	rolledBack, ownershipNoop := 0, 0
	for _, event := range events {
		switch event.Details["outcome"] {
		case "rolled_back":
			rolledBack++
		case "ownership_not_held":
			ownershipNoop++
		}
	}
	if rolledBack != 1 || ownershipNoop != 1 {
		t.Fatalf("partial-batch rollback events = %#v", events)
	}
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	if len(secrets.values) != 0 || secrets.rollbackCalls != 2 {
		t.Fatalf("partial batch retained runtime credentials: values=%d rollback_calls=%d", len(secrets.values), secrets.rollbackCalls)
	}
}

func TestRuntimeCredentialCommitAmbiguousCreateIsOwnershipSafelyRolledBack(t *testing.T) {
	const secret = "commit-ambiguous-runtime-secret"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{
		values:        make(map[string]string),
		createErr:     errors.New("provider lost response after commit " + secret),
		commitThenErr: true,
	}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-ambiguous-create-script", "Runtime ambiguous create")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	_, err = server.runner.CreateTest(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": secret}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("CreateTest error = %q, want redacted ambiguous provider failure", err)
	}
	secrets.mu.Lock()
	if len(secrets.values) != 0 || secrets.rollbackCalls != 1 {
		secrets.mu.Unlock()
		t.Fatalf("ambiguous create retained value: values=%#v rollback_calls=%d", secrets.values, secrets.rollbackCalls)
	}
	secrets.mu.Unlock()
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("ambiguous create journals = %#v, %v", attempts, err)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.runtime_override.create", Status: "failure", Limit: 10})
	if err != nil || len(events) == 0 || events[0].Details["outcome"] != "provider_error" || events[0].Details["commit_state"] != "ambiguous" {
		t.Fatalf("ambiguous create audit = %#v, %v", events, err)
	}
}

func TestRuntimeCredentialRollbackProviderErrorNeverDisclosesValue(t *testing.T) {
	const secret = "rollback-provider-echoed-secret"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{
		values:      make(map[string]string),
		rollbackErr: errors.New("provider echoed rollback value " + secret),
	}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-rollback-redaction-script", "Runtime rollback redaction")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)
	wantErr := errors.New("forced persistence failure")
	server.runner.testStore = failingTestPersistence{err: wantErr}

	_, err = server.runner.CreateTest(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": secret}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if !errors.Is(err, wantErr) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "provider echoed") {
		t.Fatalf("CreateTest rollback error = %q, want persistence error plus generic compensation failure", err)
	}
	events, listErr := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.runtime_override.rollback", Limit: 10})
	if listErr != nil {
		t.Fatal(listErr)
	}
	encoded, marshalErr := json.Marshal(events)
	if marshalErr != nil || strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "provider echoed") {
		t.Fatalf("rollback audit disclosed provider material: %s, %v", encoded, marshalErr)
	}
}

func TestRuntimeCredentialPostCreateAuditFailureRollsBackValue(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string)}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	server.runner.scenarioStore = &failCreatedRuntimeAuditStore{scenarioPersistence: records, err: errors.New("forced post-create audit failure")}
	script := httpScript("runtime-audit-rollback-script", "Runtime audit rollback")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	_, err = server.runner.CreateTest(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "audit-failure-secret"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "forced post-create audit failure") {
		t.Fatalf("CreateTest error = %v", err)
	}
	secrets.mu.Lock()
	if len(secrets.values) != 0 || secrets.rollbackCalls != 1 {
		secrets.mu.Unlock()
		t.Fatalf("audit failure retained runtime credential: values=%d rollback_calls=%d", len(secrets.values), secrets.rollbackCalls)
	}
	secrets.mu.Unlock()
	events, err := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.runtime_override.rollback", Status: "success", Limit: 10})
	if err != nil || len(events) != 1 || events[0].Details["trigger"] != "runtime_secret_batch_failed" {
		t.Fatalf("rollback audit = %#v, %v", events, err)
	}
}

func TestRuntimeCredentialCreationFailsWhenGovernedCreateIsUnavailable(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secretstore.EnvironmentStore{}})
	script := httpScript("runtime-read-only-script", "Runtime read only")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)

	_, err = server.runner.CreateTest(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-be-lost"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot create governed values") {
		t.Fatalf("plaintext runtime credential error = %v", err)
	}

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "{{secret.EXISTING_TOKEN}}"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil || testRun == nil {
		t.Fatalf("existing reference should remain saveable: test=%#v err=%v", testRun, err)
	}
}

func TestRuntimeCredentialRollbackNeverDeletesPreexistingValue(t *testing.T) {
	const testID = "11111111-1111-4111-8111-111111111111"
	const value = "preexisting-runtime-value"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	name := runtimeOverrideSecretName(testID, -1, "env.API_TOKEN")
	key := storage.DefaultProjectID + "/" + name
	secrets := &runtimeProjectionSecretStore{
		values:   map[string]string{key: value},
		creators: map[string]string{key: "preexisting-admin"},
	}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-preexisting-script", "Runtime preexisting")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)
	wantErr := errors.New("forced persistence failure after reuse")
	server.runner.testStore = failingTestPersistence{err: wantErr}

	_, err = server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: testID,
		Duration:        "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": value}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CreateTest error = %v, want %v", err, wantErr)
	}
	secrets.mu.Lock()
	defer secrets.mu.Unlock()
	if secrets.values[key] != value || secrets.creators[key] != "preexisting-admin" || secrets.rollbackCalls != 0 {
		t.Fatalf("preexisting credential changed: values=%#v creators=%#v rollback_calls=%d", secrets.values, secrets.creators, secrets.rollbackCalls)
	}
}

func TestStartupReconcilesExpiredOrphanRuntimeCredentialAttempt(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const (
		testID    = "22222222-2222-4222-8222-222222222222"
		attemptID = "33333333-3333-4333-8333-333333333333"
		name      = "APEX_RUNTIME_OVERRIDE_RECOVERY"
		secret    = "orphaned-runtime-secret"
	)
	actor := "runtime-test:" + testID + ":attempt:" + attemptID
	created, err := records.CreateRuntimeSecretProvisioningAttempt(&storage.RuntimeSecretProvisioningAttempt{
		ID:             attemptID,
		ProjectID:      storage.DefaultProjectID,
		TestID:         testID,
		Actor:          actor,
		SecretNames:    []string{name},
		LeaseExpiresAt: time.Now().Add(-time.Minute),
		CreatedAt:      time.Now().Add(-2 * time.Minute),
	})
	if err != nil || !created {
		t.Fatalf("CreateRuntimeSecretProvisioningAttempt = %v, %v", created, err)
	}
	key := storage.DefaultProjectID + "/" + name
	secrets := &runtimeProjectionSecretStore{
		values:   map[string]string{key: secret},
		creators: map[string]string{key: actor},
	}
	_ = NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})

	secrets.mu.Lock()
	if len(secrets.values) != 0 || secrets.rollbackCalls != 1 {
		secrets.mu.Unlock()
		t.Fatalf("startup recovery retained orphan: values=%#v rollback_calls=%d", secrets.values, secrets.rollbackCalls)
	}
	secrets.mu.Unlock()
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("startup recovery journals = %#v, %v", attempts, err)
	}
}

func TestStartupSchedulesRuntimeCredentialRecoveryForLeaseExpiry(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const (
		testID    = "55555555-5555-4555-8555-555555555555"
		attemptID = "66666666-6666-4666-8666-666666666666"
		name      = "APEX_RUNTIME_OVERRIDE_FUTURE_RECOVERY"
	)
	actor := "runtime-test:" + testID + ":attempt:" + attemptID
	created, err := records.CreateRuntimeSecretProvisioningAttempt(&storage.RuntimeSecretProvisioningAttempt{
		ID:             attemptID,
		ProjectID:      storage.DefaultProjectID,
		TestID:         testID,
		Actor:          actor,
		SecretNames:    []string{name},
		LeaseExpiresAt: time.Now().Add(50 * time.Millisecond),
		CreatedAt:      time.Now(),
	})
	if err != nil || !created {
		t.Fatalf("CreateRuntimeSecretProvisioningAttempt = %v, %v", created, err)
	}
	key := storage.DefaultProjectID + "/" + name
	secrets := &runtimeProjectionSecretStore{
		values:   map[string]string{key: "future-orphaned-runtime-secret"},
		creators: map[string]string{key: actor},
	}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)
	deadline := time.Now().Add(2 * time.Second)
	for {
		secrets.mu.Lock()
		remaining := len(secrets.values)
		secrets.mu.Unlock()
		attempts, listErr := records.ListRuntimeSecretProvisioningAttempts()
		if listErr != nil {
			t.Fatalf("list future recovery journals: %v", listErr)
		}
		// Recovery removes the provider value before it deletes the durable
		// journal. Observe the complete operation instead of racing that final
		// persistence step under a shuffled/race-enabled suite.
		if remaining == 0 && len(attempts) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("future provisioning lease expired without complete recovery: values=%d journals=%#v", remaining, attempts)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRuntimeCredentialRecoveryTimerStopsWithRunnerShutdown(t *testing.T) {
	runner := &TestRunner{}
	runner.scheduleRuntimeOverrideSecretAttempt("attempt", time.Now().Add(time.Hour))
	runner.secretRecovery.mu.Lock()
	armed := runner.secretRecovery.timer != nil
	runner.secretRecovery.mu.Unlock()
	if !armed {
		t.Fatal("runtime credential recovery timer was not armed")
	}
	runner.beginShutdown()
	runner.secretRecovery.mu.Lock()
	defer runner.secretRecovery.mu.Unlock()
	if runner.secretRecovery.timer != nil || !runner.secretRecovery.stopped {
		t.Fatal("runtime credential recovery timer remained armed after shutdown")
	}
}

func TestRuntimeCredentialProvisioningArmsRecoveryBeforeAuditOrProviderCalls(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string)}
	runner := &TestRunner{
		projectSecretStore: secrets,
		secretAttemptStore: records,
		scenarioStore:      &panicRuntimeAuditStore{scenarioPersistence: records},
	}
	t.Cleanup(runner.stopRuntimeOverrideSecretAttemptReconciliation)

	didPanic := false
	func() {
		defer func() {
			didPanic = recover() != nil
		}()
		_, _ = runner.persistRuntimeOverrideSecretValues(
			storage.DefaultProjectID,
			"77777777-7777-4777-8777-777777777777",
			"runtime-secret-panic-owner",
			"test:runtime-secret-panic-owner",
			[]TestGroup{{runtimeSecretValues: map[string]string{"PANIC_SECRET": "never-disclose"}}},
		)
	}()
	if !didPanic {
		t.Fatal("injected audit panic did not propagate")
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 1 {
		t.Fatalf("provisioning journals = %#v, %v", attempts, err)
	}
	runner.secretRecovery.mu.Lock()
	armed := runner.secretRecovery.timer != nil
	scheduledAt := runner.secretRecovery.at
	runner.secretRecovery.mu.Unlock()
	if !armed {
		t.Fatal("runtime credential recovery was not armed immediately after journal creation")
	}
	// Durable timestamps are normalized to microsecond precision. The in-memory
	// timer retains the clock's native precision, which can be nanoseconds on
	// Linux, so exact equality is not a portable assertion.
	leaseDelta := scheduledAt.Sub(attempts[0].LeaseExpiresAt)
	if leaseDelta < 0 {
		leaseDelta = -leaseDelta
	}
	if leaseDelta >= time.Microsecond {
		t.Fatalf("runtime credential recovery scheduled for %s, journal lease is %s (delta %s)", scheduledAt, attempts[0].LeaseExpiresAt, leaseDelta)
	}
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("provider was called %d times before the injected audit panic", createCalls)
	}
}

func TestRuntimeCredentialRecoveryDoesNotRaceActiveProvisioning(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const (
		testID    = "99999999-9999-4999-8999-999999999999"
		attemptID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		name      = "APEX_RUNTIME_OVERRIDE_ACTIVE"
	)
	actor := "runtime-test:" + testID + ":attempt:" + attemptID
	key := storage.DefaultProjectID + "/" + name
	secrets := &runtimeProjectionSecretStore{
		values:   map[string]string{key: "active-provisioning-secret"},
		creators: map[string]string{key: actor},
	}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)
	record := &storage.RuntimeSecretProvisioningAttempt{
		ID:                 attemptID,
		ProjectID:          storage.DefaultProjectID,
		TestID:             testID,
		MaterializationKey: "active-provisioning-owner",
		RefKey:             "test:active-provisioning-owner",
		Actor:              actor,
		SecretNames:        []string{name},
		LeaseExpiresAt:     time.Now().Add(-time.Minute),
		CreatedAt:          time.Now().Add(-2 * time.Minute),
	}
	created, err := records.CreateRuntimeSecretProvisioningAttempt(record)
	if err != nil || !created {
		t.Fatalf("CreateRuntimeSecretProvisioningAttempt = %v, %v", created, err)
	}
	releaseActive := server.runner.markRuntimeOverrideSecretAttemptActive(attemptID)
	if err := server.runner.reconcileRuntimeOverrideSecretAttempt(record); !errors.Is(err, errRuntimeSecretProvisioningActive) {
		t.Fatalf("active reconciliation error = %v", err)
	}
	secrets.mu.Lock()
	_, retainedWhileActive := secrets.values[key]
	rollbackCallsWhileActive := secrets.rollbackCalls
	secrets.mu.Unlock()
	if !retainedWhileActive || rollbackCallsWhileActive != 0 {
		t.Fatalf("active attempt was compensated: retained=%v rollback_calls=%d", retainedWhileActive, rollbackCallsWhileActive)
	}
	releaseActive()
	if err := server.runner.reconcileRuntimeOverrideSecretAttempt(record); err != nil {
		t.Fatalf("reconcile after active attempt ended: %v", err)
	}
	secrets.mu.Lock()
	_, retainedAfterRelease := secrets.values[key]
	rollbackCallsAfterRelease := secrets.rollbackCalls
	secrets.mu.Unlock()
	if retainedAfterRelease || rollbackCallsAfterRelease != 1 {
		t.Fatalf("ended attempt was not compensated: retained=%v rollback_calls=%d", retainedAfterRelease, rollbackCallsAfterRelease)
	}
}

func TestStartupKeepsRuntimeCredentialWhenTestCommitIsDurable(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string)}
	first := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("runtime-committed-recovery-script", "Runtime committed recovery")
	first.runner.StoreScript(script)
	testRun, err := first.runner.CreateTest(CreateTestRequest{
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	const (
		attemptID = "44444444-4444-4444-8444-444444444444"
		name      = "APEX_RUNTIME_OVERRIDE_COMMITTED"
		secret    = "committed-runtime-secret"
	)
	actor := "runtime-test:" + testRun.ID + ":attempt:" + attemptID
	created, err := records.CreateRuntimeSecretProvisioningAttempt(&storage.RuntimeSecretProvisioningAttempt{
		ID:                 attemptID,
		ProjectID:          storage.DefaultProjectID,
		TestID:             testRun.ID,
		MaterializationKey: testRun.MaterializationKey,
		RefKey:             testRun.RefKey,
		Actor:              actor,
		SecretNames:        []string{name},
		LeaseExpiresAt:     time.Now().Add(-time.Minute),
		CreatedAt:          time.Now().Add(-2 * time.Minute),
	})
	if err != nil || !created {
		t.Fatalf("CreateRuntimeSecretProvisioningAttempt = %v, %v", created, err)
	}
	key := storage.DefaultProjectID + "/" + name
	secrets.mu.Lock()
	secrets.values[key] = secret
	if secrets.creators == nil {
		secrets.creators = make(map[string]string)
	}
	secrets.creators[key] = actor
	secrets.mu.Unlock()
	_ = NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})

	secrets.mu.Lock()
	value, rollbackCalls := secrets.values[key], secrets.rollbackCalls
	secrets.mu.Unlock()
	if value != secret || rollbackCalls != 0 {
		t.Fatalf("committed recovery value=%q rollback_calls=%d", value, rollbackCalls)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("committed recovery journals = %#v, %v", attempts, err)
	}
}

func TestLegacyRuntimeCredentialSnapshotIsScrubbedWithoutSecretWrites(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })

	bootstrap := NewServer(ServerConfig{Store: records, MaxConcurrent: 2})
	script := httpScript("legacy-runtime-credential-script", "Legacy runtime credential")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	if _, err := bootstrap.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	bootstrap.runner.beginShutdown()

	const (
		testID         = "12121212-1212-4212-8212-121212121212"
		plaintextValue = "legacy-runtime-plaintext"
	)
	started := time.Now().UTC().Truncate(time.Microsecond)
	if err := records.SaveTest(&storage.TestRecord{
		ID:                 testID,
		MaterializationKey: "legacy-runtime-materialization",
		Name:               "Legacy runtime credentials",
		Status:             "QUEUED",
		ProjectID:          storage.DefaultProjectID,
		TotalVUsers:        1,
		Duration:           int64(time.Minute),
		StartTime:          started,
		CreatedBySubject:   "unattributed-service-caller",
		Groups: []TestGroup{{
			Name:     "Legacy",
			ScriptID: script.ID,
			VUsers:   1,
			RuntimeSettings: &RuntimeSettingsOverride{
				HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: "[REDACTED]"}},
			},
		}},
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": plaintextValue}},
	}); err != nil {
		t.Fatalf("SaveTest(legacy): %v", err)
	}

	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	first := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(first.runner.beginShutdown)
	first.runner.mu.RLock()
	restored := first.runner.tests[testID]
	hydrationErr := ""
	if restored != nil {
		hydrationErr = restored.runtimeHydrationError
	}
	first.runner.mu.RUnlock()
	if restored == nil || hydrationErr != "" {
		t.Fatalf("legacy runtime hydration = %#v error=%q", restored, hydrationErr)
	}

	secrets.mu.Lock()
	createCalls := secrets.createCalls
	valueCount := len(secrets.values)
	secrets.mu.Unlock()
	if createCalls != 0 || valueCount != 0 {
		t.Fatalf("legacy hydration wrote governed secrets: calls=%d values=%d", createCalls, valueCount)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("legacy hydration journals = %#v, %v", attempts, err)
	}
	durable, err := records.GetTest(testID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	encoded, err := json.Marshal(durable)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), plaintextValue) || strings.Contains(string(encoded), "[REDACTED]") {
		t.Fatalf("durable legacy snapshot retained executable credential material: %s", encoded)
	}
	rootName := runtimeOverrideSecretName(testID, -1, "env.API_TOKEN")
	groupName := runtimeOverrideSecretName(testID, 0, "http.auth.password")
	if !strings.Contains(string(encoded), "{{secret."+rootName+"}}") || !strings.Contains(string(encoded), "{{secret."+groupName+"}}") {
		t.Fatalf("durable legacy snapshot did not retain deferred references: %s", encoded)
	}

	first.runner.beginShutdown()
	second := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(second.runner.beginShutdown)
	secrets.mu.Lock()
	createCalls = secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("second hydration repeated secret provisioning: calls=%d", createCalls)
	}
}

func TestExpiredLegacyRuntimeRestoreAttemptRollsBackDespitePreexistingTest(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), creators: make(map[string]string)}
	first := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	script := httpScript("legacy-restore-attempt-script", "Legacy restore attempt")
	first.runner.StoreScript(script)
	testRun, err := first.runner.CreateTest(CreateTestRequest{
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	first.runner.beginShutdown()

	const (
		attemptID = "13131313-1313-4313-8313-131313131313"
		name      = "APEX_RUNTIME_OVERRIDE_LEGACY_RESTORE"
	)
	actor := "runtime-test:" + testRun.ID + ":attempt:" + attemptID
	created, err := records.CreateRuntimeSecretProvisioningAttempt(&storage.RuntimeSecretProvisioningAttempt{
		ID:                 attemptID,
		ProjectID:          storage.DefaultProjectID,
		TestID:             testRun.ID,
		MaterializationKey: testRun.MaterializationKey,
		RefKey:             testRun.RefKey,
		Actor:              actor,
		AuditActorType:     "system",
		AuditActorSubject:  "controller:runtime-restore",
		SecretNames:        []string{name},
		LeaseExpiresAt:     time.Now().Add(-time.Minute),
		CreatedAt:          time.Now().Add(-2 * time.Minute),
	})
	if err != nil || !created {
		t.Fatalf("CreateRuntimeSecretProvisioningAttempt = (%v, %v)", created, err)
	}
	key := storage.DefaultProjectID + "/" + name
	secrets.mu.Lock()
	secrets.values[key] = "partial-legacy-restore-value"
	secrets.creators[key] = actor
	secrets.mu.Unlock()

	second := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(second.runner.beginShutdown)
	secrets.mu.Lock()
	_, retained := secrets.values[key]
	rollbackCalls := secrets.rollbackCalls
	secrets.mu.Unlock()
	if retained || rollbackCalls != 1 {
		t.Fatalf("legacy restore recovery retained partial value: retained=%t rollback_calls=%d", retained, rollbackCalls)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("legacy restore recovery journals = %#v, %v", attempts, err)
	}
}

func TestRuntimeCredentialRecoveryRejectsSameIDWithoutMatchingOwnership(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string)}
	first := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(first.runner.beginShutdown)
	script := httpScript("runtime-collision-recovery-script", "Runtime collision recovery")
	first.runner.StoreScript(script)
	testRun, err := first.runner.CreateTest(CreateTestRequest{
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	const (
		attemptID = "88888888-8888-4888-8888-888888888888"
		name      = "APEX_RUNTIME_OVERRIDE_COLLISION"
		secret    = "same-id-uncommitted-secret"
	)
	actor := "runtime-test:" + testRun.ID + ":attempt:" + attemptID
	created, err := records.CreateRuntimeSecretProvisioningAttempt(&storage.RuntimeSecretProvisioningAttempt{
		ID:                 attemptID,
		ProjectID:          storage.DefaultProjectID,
		TestID:             testRun.ID,
		MaterializationKey: "different-materialization-owner",
		RefKey:             "test:different-materialization-owner",
		Actor:              actor,
		SecretNames:        []string{name},
		LeaseExpiresAt:     time.Now().Add(-time.Minute),
		CreatedAt:          time.Now().Add(-2 * time.Minute),
	})
	if err != nil || !created {
		t.Fatalf("CreateRuntimeSecretProvisioningAttempt = %v, %v", created, err)
	}
	key := storage.DefaultProjectID + "/" + name
	secrets.mu.Lock()
	secrets.values[key] = secret
	if secrets.creators == nil {
		secrets.creators = make(map[string]string)
	}
	secrets.creators[key] = actor
	secrets.mu.Unlock()

	second := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(second.runner.beginShutdown)
	secrets.mu.Lock()
	_, retained := secrets.values[key]
	rollbackCalls := secrets.rollbackCalls
	secrets.mu.Unlock()
	if retained || rollbackCalls != 1 {
		t.Fatalf("colliding durable test suppressed rollback: retained=%v rollback_calls=%d", retained, rollbackCalls)
	}
	if durable, getErr := records.GetTest(testRun.ID); getErr != nil || durable == nil || durable.RefKey != testRun.RefKey {
		t.Fatalf("unrelated durable test changed during recovery: %#v, %v", durable, getErr)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("collision recovery journals = %#v, %v", attempts, err)
	}
}

// A scenario saved again after its credential create committed no longer
// matches the journal's fingerprint, but recovery keeps the credentials it
// still references; an attempt whose names it does not reference is undone.
func TestScenarioCredentialRecoveryKeepsCredentialsAnEditedScenarioStillUses(t *testing.T) {
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)
	script := httpScript("edited-scenario-runtime-script", "Edited scenario runtime")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Edited scenario", Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "edited-scenario-secret"}},
		Groups:          []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
		creationActor:   testCreationActor{Type: "user", Subject: "scenario-editor", DisplayName: "Scenario Editor"},
	})
	if err != nil {
		t.Fatal(err)
	}
	name := runtimeOverrideSecretName(scenario.ID, -1, "env.API_TOKEN")
	key := storage.DefaultProjectID + "/" + name
	secrets.mu.Lock()
	creator := secrets.creators[key]
	secrets.mu.Unlock()
	if creator == "" {
		t.Fatal("fixture: the credential was not created")
	}
	journal := func(id string, names ...string) *storage.RuntimeSecretProvisioningAttempt {
		record := &storage.RuntimeSecretProvisioningAttempt{
			ID: id, ProjectID: storage.DefaultProjectID, TestID: scenario.ID, ResourceType: "scenario",
			CommitFingerprint: "sha256:the-scenario-was-saved-again", Actor: creator, SecretNames: names,
			LeaseExpiresAt: time.Now().Add(-time.Minute), CreatedAt: time.Now().Add(-2 * time.Minute),
		}
		if created, err := records.CreateRuntimeSecretProvisioningAttempt(record); err != nil || !created {
			t.Fatalf("CreateRuntimeSecretProvisioningAttempt = %v, %v", created, err)
		}
		return record
	}
	if err := server.runner.reconcileRuntimeOverrideSecretAttempt(journal("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", name)); err != nil {
		t.Fatal(err)
	}
	secrets.mu.Lock()
	_, retained := secrets.values[key]
	rollbacks := secrets.rollbackCalls
	secrets.mu.Unlock()
	if !retained || rollbacks != 0 {
		t.Fatalf("a credential the scenario still references was rolled back: retained=%v rollback_calls=%d", retained, rollbacks)
	}
	if attempts, err := records.ListRuntimeSecretProvisioningAttempts(); err != nil || len(attempts) != 0 {
		t.Fatalf("the kept attempt's journal was not completed: %#v, %v", attempts, err)
	}
	unused := runtimeOverrideSecretName(scenario.ID, -1, "env.UNUSED")
	secrets.mu.Lock()
	secrets.values[storage.DefaultProjectID+"/"+unused], secrets.creators[storage.DefaultProjectID+"/"+unused] = "orphan", creator
	secrets.mu.Unlock()
	if err := server.runner.reconcileRuntimeOverrideSecretAttempt(journal("cccccccc-cccc-4ccc-8ccc-cccccccccccc", unused)); err != nil {
		t.Fatal(err)
	}
	secrets.mu.Lock()
	_, orphanRetained := secrets.values[storage.DefaultProjectID+"/"+unused]
	secrets.mu.Unlock()
	if orphanRetained {
		t.Fatal("a credential no stored scenario references was kept")
	}
}
