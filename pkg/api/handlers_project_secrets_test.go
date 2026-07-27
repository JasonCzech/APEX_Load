package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newProjectSecretAPIServer(t *testing.T) (*Server, *storage.SQLiteStore, secretstore.Store, string) {
	t.Helper()
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	projectID := "project-secrets-api"
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: projectID, Name: "Secrets", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x44}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	managed := &secretstore.DatabaseStore{Records: records, Wrapper: wrapper}
	server := NewServer(ServerConfig{Store: records, ProjectSecretStore: managed})
	return server, records, managed, projectID
}

func TestProjectSecretAPICreateStatusRotateAndScriptSummary(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	create := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
		"secrets": []map[string]string{
			{"name": "login.password", "value": "first-secret-value"},
			{"name": "API_TOKEN", "value": "token-value"},
		},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	if strings.Contains(create.Body.String(), "first-secret-value") || strings.Contains(create.Body.String(), "token-value") {
		t.Fatalf("create response disclosed value: %s", create.Body.String())
	}
	duplicate := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
		"secrets": []map[string]string{{"name": "LOGIN_PASSWORD", "value": "must-not-overwrite"}},
	})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "LOGIN_PASSWORD")
	if err != nil || value != "first-secret-value" {
		t.Fatalf("duplicate create value = %q, %v", value, err)
	}
	status := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets/status", map[string]any{
		"names": []string{"LOGIN_PASSWORD", "MISSING"},
	})
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"configured":true`) || !strings.Contains(status.Body.String(), `"configured":false`) {
		t.Fatalf("status response = %d %s", status.Code, status.Body.String())
	}
	var statusPayload struct {
		Secrets []projectSecretStatus `json:"secrets"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &statusPayload); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if len(statusPayload.Secrets) != 2 {
		t.Fatalf("status entries = %d, want one per requested logical name: %#v", len(statusPayload.Secrets), statusPayload.Secrets)
	}
	rotate := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/LOGIN_PASSWORD", map[string]any{"value": "rotated-secret-value"})
	if rotate.Code != http.StatusOK || strings.Contains(rotate.Body.String(), "rotated-secret-value") {
		t.Fatalf("rotate response = %d %s", rotate.Code, rotate.Body.String())
	}
	value, _, err = managed.Get(context.Background(), projectID, "LOGIN_PASSWORD")
	if err != nil || value != "rotated-secret-value" {
		t.Fatalf("rotated value = %q, %v", value, err)
	}
	script := &dsl.Script{
		ID: "secret-status-script", Name: "Secret status", Version: "1", Protocol: "http", ProjectID: projectID,
		Config: dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
			Type: "basic", Password: "{{ secret.login-password }}",
		}}},
		Actions: []dsl.Action{{Name: "Call", Type: "request", Target: "/?token={{secret.missing.token}}"}},
	}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	summary := secretAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+script.ID+"/secret-status", nil)
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"configured":["LOGIN_PASSWORD"]`) ||
		!strings.Contains(summary.Body.String(), `"missing":["MISSING_TOKEN"]`) {
		t.Fatalf("script status = %d %s", summary.Code, summary.Body.String())
	}
	list := secretAPIRequest(t, server, http.MethodGet, "/api/v1/projects/"+projectID+"/secrets", nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "rotated-secret-value") {
		t.Fatalf("list response = %d %s", list.Code, list.Body.String())
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{ProjectID: projectID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "first-secret-value") || strings.Contains(string(encoded), "rotated-secret-value") {
		t.Fatalf("audit events disclosed values: %s", encoded)
	}
	foundBlockedCreate := false
	for _, event := range events {
		if event.Action != "project_secret.create" || event.Status != "blocked" || event.ResourceID != "LOGIN_PASSWORD" {
			continue
		}
		foundBlockedCreate = true
		if event.Details["provider"] != "encrypted_database" || event.Details["reason"] != "already_exists" {
			t.Fatalf("blocked create audit details = %#v", event.Details)
		}
	}
	if !foundBlockedCreate {
		t.Fatal("blocked create audit event was not recorded")
	}
}

func TestProjectSecretAPIRejectsRedactedSentinelForCreateAndRotation(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)

	for index, sentinel := range []string{"[REDACTED]", " [redacted] ", "\t[ReDaCtEd]\n"} {
		name := fmt.Sprintf("REDACTED_CREATE_%d", index)
		create := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
			"secrets": []map[string]string{{"name": name, "value": sentinel}},
		})
		if create.Code != http.StatusBadRequest {
			t.Fatalf("create sentinel %q status = %d body=%s", sentinel, create.Code, create.Body.String())
		}
		if _, _, err := managed.Get(context.Background(), projectID, name); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatalf("redacted create %q persisted a configured value: %v", sentinel, err)
		}
	}

	if _, err := managed.Create(context.Background(), projectID, "ROTATE_TARGET", "original-value", "admin"); err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{"[REDACTED]", " [redacted] ", "\t[ReDaCtEd]\n"} {
		rotate := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/ROTATE_TARGET", map[string]any{
			"value": sentinel,
		})
		if rotate.Code != http.StatusBadRequest {
			t.Fatalf("rotate sentinel %q status = %d body=%s", sentinel, rotate.Code, rotate.Body.String())
		}
		value, _, err := managed.Get(context.Background(), projectID, "ROTATE_TARGET")
		if err != nil || value != "original-value" {
			t.Fatalf("redacted rotation %q changed value = %q, %v", sentinel, value, err)
		}
	}

	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	sessionRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", nil)
	sessionID, err := server.registerSourceSecretSession(sessionRequest, projectID, map[string]string{
		"redacted-requirement": " [redacted] ",
	})
	if err != nil {
		t.Fatal(err)
	}
	migrate := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
		"secrets": []map[string]string{{
			"requirement_id": "redacted-requirement",
			"secret_name":    "REDACTED_MIGRATION",
		}},
	})
	if migrate.Code != http.StatusBadRequest {
		t.Fatalf("migration sentinel status = %d body=%s", migrate.Code, migrate.Body.String())
	}
	if _, _, err := managed.Get(context.Background(), projectID, "REDACTED_MIGRATION"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("redacted migration persisted a configured value: %v", err)
	}
}

func TestProjectSecretStatusTreatsExternalRedactionSentinelsAsMissing(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/secrets/status", nil)

	for _, value := range []string{"[REDACTED]", " [redacted] ", "\t[ReDaCtEd]\n"} {
		server.projectSecretStore = &rotationMetadataProbeStore{
			value: value,
			metadata: secretstore.Metadata{
				ProjectID: projectID,
				Name:      "EXTERNAL_SENTINEL_STATUS",
				Provider:  "azure_key_vault",
			},
		}
		statuses, err := server.projectSecretStatuses(request, projectID, []string{"EXTERNAL_SENTINEL_STATUS"})
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) != 1 || statuses[0].Configured {
			t.Fatalf("external sentinel %q status = %#v, want missing", value, statuses)
		}
	}

	server.projectSecretStore = nil
	t.Setenv(engine.ProjectSecretEnvironmentName(projectID, "ENV_SENTINEL_STATUS"), " [redacted] ")
	statuses, err := server.projectSecretStatuses(request, projectID, []string{"ENV_SENTINEL_STATUS"})
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Configured {
		t.Fatalf("environment sentinel status = %#v, want missing", statuses)
	}
}

type failNthCreateSecretStore struct {
	delegate    secretstore.Store
	failOnCall  int
	createCalls int
}

func (s *failNthCreateSecretStore) Get(ctx context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	return s.delegate.Get(ctx, projectID, name)
}

func (s *failNthCreateSecretStore) Create(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	s.createCalls++
	if s.createCalls == s.failOnCall {
		return secretstore.Metadata{}, errors.New("simulated provider write failure")
	}
	return s.delegate.Create(ctx, projectID, name, value, actor)
}

func (s *failNthCreateSecretStore) Replace(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	return s.delegate.Replace(ctx, projectID, name, value, actor)
}

func (s *failNthCreateSecretStore) List(ctx context.Context, projectID string) ([]secretstore.Metadata, error) {
	return s.delegate.List(ctx, projectID)
}

func (s *failNthCreateSecretStore) Writable() bool   { return s.delegate.Writable() }
func (s *failNthCreateSecretStore) Provider() string { return s.delegate.Provider() }

type createUnsupportedSecretStore struct{ secretstore.Store }

func (*createUnsupportedSecretStore) CreateSupported() bool { return false }

type selectiveAuditFailureStore struct {
	storage.Store
	action string
	status string
}

func (s *selectiveAuditFailureStore) SaveAuditEvent(event *storage.AuditEvent) error {
	if event != nil && event.Action == s.action && event.Status == s.status {
		return errors.New("simulated audit persistence failure")
	}
	return s.Store.SaveAuditEvent(event)
}

type commitAmbiguousSecretStore struct {
	delegate       secretstore.Store
	failCreate     bool
	failReplace    bool
	createApplied  bool
	replaceApplied bool
}

func (s *commitAmbiguousSecretStore) Get(ctx context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	return s.delegate.Get(ctx, projectID, name)
}

func (s *commitAmbiguousSecretStore) Create(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	metadata, err := s.delegate.Create(ctx, projectID, name, value, actor)
	if err != nil {
		return metadata, err
	}
	s.createApplied = true
	if s.failCreate {
		return secretstore.Metadata{}, errors.New("simulated lost create acknowledgement")
	}
	return metadata, nil
}

func (s *commitAmbiguousSecretStore) Replace(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	metadata, err := s.delegate.Replace(ctx, projectID, name, value, actor)
	if err != nil {
		return metadata, err
	}
	s.replaceApplied = true
	if s.failReplace {
		return secretstore.Metadata{}, errors.New("simulated lost replace acknowledgement")
	}
	return metadata, nil
}

func (s *commitAmbiguousSecretStore) List(ctx context.Context, projectID string) ([]secretstore.Metadata, error) {
	return s.delegate.List(ctx, projectID)
}

func (s *commitAmbiguousSecretStore) Writable() bool   { return s.delegate.Writable() }
func (s *commitAmbiguousSecretStore) Provider() string { return s.delegate.Provider() }

type rotationMetadataProbeStore struct {
	value    string
	metadata secretstore.Metadata
}

func (s *rotationMetadataProbeStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	return s.value, s.metadata, nil
}
func (*rotationMetadataProbeStore) Create(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, errors.New("unexpected create")
}
func (*rotationMetadataProbeStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrMetadataCommit
}
func (*rotationMetadataProbeStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return nil, nil
}
func (*rotationMetadataProbeStore) Writable() bool   { return true }
func (*rotationMetadataProbeStore) Provider() string { return "azure_key_vault" }

type blockingProjectSecretStore struct {
	getCalls    atomic.Int32
	createCalls atomic.Int32
}

func (s *blockingProjectSecretStore) Get(ctx context.Context, _, _ string) (string, secretstore.Metadata, error) {
	s.getCalls.Add(1)
	<-ctx.Done()
	return "", secretstore.Metadata{}, ctx.Err()
}
func (s *blockingProjectSecretStore) Create(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	s.createCalls.Add(1)
	return secretstore.Metadata{}, errors.New("unexpected create after failed probe")
}
func (*blockingProjectSecretStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, errors.New("unexpected replace")
}
func (*blockingProjectSecretStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return []secretstore.Metadata{}, nil
}
func (*blockingProjectSecretStore) Writable() bool   { return true }
func (*blockingProjectSecretStore) Provider() string { return "blocking_provider" }

type blockingProjectSecretMutationStore struct {
	createCalls  atomic.Int32
	replaceCalls atomic.Int32
}

func (*blockingProjectSecretMutationStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	return "", secretstore.Metadata{}, secretstore.ErrNotFound
}
func (s *blockingProjectSecretMutationStore) Create(ctx context.Context, _, _, _, _ string) (secretstore.Metadata, error) {
	s.createCalls.Add(1)
	<-ctx.Done()
	return secretstore.Metadata{}, ctx.Err()
}
func (s *blockingProjectSecretMutationStore) Replace(ctx context.Context, _, _, _, _ string) (secretstore.Metadata, error) {
	s.replaceCalls.Add(1)
	<-ctx.Done()
	return secretstore.Metadata{}, ctx.Err()
}
func (*blockingProjectSecretMutationStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return nil, nil
}
func (*blockingProjectSecretMutationStore) Writable() bool   { return true }
func (*blockingProjectSecretMutationStore) Provider() string { return "blocking_mutation_provider" }

type discoveryFailureSecretStore struct {
	metadata []secretstore.Metadata
}

func (*discoveryFailureSecretStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	return "", secretstore.Metadata{}, secretstore.ErrNotFound
}
func (*discoveryFailureSecretStore) Create(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrCreateUnsupported
}
func (*discoveryFailureSecretStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrReadOnly
}
func (s *discoveryFailureSecretStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return append([]secretstore.Metadata(nil), s.metadata...), nil
}
func (*discoveryFailureSecretStore) Writable() bool   { return false }
func (*discoveryFailureSecretStore) Provider() string { return "azure_key_vault" }
func (*discoveryFailureSecretStore) DiscoverManagedMetadata(context.Context, string, []string) error {
	return errors.New("simulated Azure Key Vault discovery outage")
}

type recordingDiscoverySecretStore struct {
	mu      sync.Mutex
	indexed map[string]secretstore.Metadata
	batches [][]string
}

func (*recordingDiscoverySecretStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	return "", secretstore.Metadata{}, secretstore.ErrNotFound
}
func (*recordingDiscoverySecretStore) Create(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrCreateUnsupported
}
func (*recordingDiscoverySecretStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrReadOnly
}
func (s *recordingDiscoverySecretStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata := make([]secretstore.Metadata, 0, len(s.indexed))
	for _, item := range s.indexed {
		metadata = append(metadata, item)
	}
	return metadata, nil
}
func (*recordingDiscoverySecretStore) Writable() bool   { return false }
func (*recordingDiscoverySecretStore) Provider() string { return "azure_key_vault" }
func (s *recordingDiscoverySecretStore) DiscoverManagedMetadata(_ context.Context, projectID string, names []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexed == nil {
		s.indexed = make(map[string]secretstore.Metadata)
	}
	s.batches = append(s.batches, append([]string(nil), names...))
	for _, name := range names {
		s.indexed[name] = secretstore.Metadata{ProjectID: projectID, Name: name, Provider: "azure_key_vault"}
	}
	return nil
}

func TestProjectSecretListSuppressesInternalRuntimeRollbackOwner(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	const internalOwner = "runtime-test:11111111-1111-4111-8111-111111111111:attempt:22222222-2222-4222-8222-222222222222"
	const scenarioOwner = "runtime-scenario:scenario-a:attempt:33333333-3333-4333-8333-333333333333"
	if _, err := managed.Create(context.Background(), projectID, "RUNTIME_TOKEN", "runtime-value", internalOwner); err != nil {
		t.Fatalf("create runtime-owned secret: %v", err)
	}
	if _, err := managed.Create(context.Background(), projectID, "SCENARIO_TOKEN", "scenario-value", scenarioOwner); err != nil {
		t.Fatalf("create scenario-owned secret: %v", err)
	}
	if _, err := managed.Create(context.Background(), projectID, "HUMAN_TOKEN", "human-value", "human@example.test"); err != nil {
		t.Fatalf("create human-owned secret: %v", err)
	}

	response := secretAPIRequest(t, server, http.MethodGet, "/api/v1/projects/"+projectID+"/secrets", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), internalOwner) || strings.Contains(response.Body.String(), scenarioOwner) {
		t.Fatalf("list exposed rollback ownership token: %s", response.Body.String())
	}
	var payload struct {
		Secrets []secretstore.Metadata `json:"secrets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	creators := make(map[string]string, len(payload.Secrets))
	for _, metadata := range payload.Secrets {
		creators[metadata.Name] = metadata.CreatedBy
	}
	if creators["RUNTIME_TOKEN"] != "" || creators["SCENARIO_TOKEN"] != "" || creators["HUMAN_TOKEN"] != "human@example.test" {
		t.Fatalf("public creators = %#v", creators)
	}
	_, storedMetadata, err := managed.Get(context.Background(), projectID, "RUNTIME_TOKEN")
	if err != nil || storedMetadata.CreatedBy != internalOwner {
		t.Fatalf("rollback owner was not retained inside provider: %#v, %v", storedMetadata, err)
	}
}

func TestProjectSecretCreateFailsClosedWhenIntentAuditIsUnavailable(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	server.store = &selectiveAuditFailureStore{Store: records, action: "project_secret.create", status: "pending"}
	const value = "intent-audit-value-must-not-be-written"
	response := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
		"secrets": []map[string]string{{"name": "AUDIT_INTENT", "value": value}},
	})
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), value) {
		t.Fatalf("intent audit response = %d %s", response.Code, response.Body.String())
	}
	if _, _, err := managed.Get(context.Background(), projectID, "AUDIT_INTENT"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("secret was mutated without a durable intent audit: %v", err)
	}
}

func TestProjectSecretMutationsDoNotAcknowledgeSuccessWhenOutcomeAuditFails(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		server, records, managed, projectID := newProjectSecretAPIServer(t)
		server.store = &selectiveAuditFailureStore{Store: records, action: "project_secret.create", status: "success"}
		const value = "create-outcome-audit-value"
		response := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
			"secrets": []map[string]string{{"name": "AUDIT_CREATE", "value": value}},
		})
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), value) {
			t.Fatalf("create outcome audit response = %d %s", response.Code, response.Body.String())
		}
		if observed, _, err := managed.Get(context.Background(), projectID, "AUDIT_CREATE"); err != nil || observed != value {
			t.Fatalf("provider outcome = %q, %v", observed, err)
		}
		events, err := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.create", ResourceID: "AUDIT_CREATE", Limit: 10})
		if err != nil || len(events) != 1 || events[0].Status != "pending" {
			t.Fatalf("durable audit intent = %#v, %v", events, err)
		}
	})

	t.Run("rotate", func(t *testing.T) {
		server, records, managed, projectID := newProjectSecretAPIServer(t)
		if _, err := managed.Create(context.Background(), projectID, "AUDIT_ROTATE", "before", "setup"); err != nil {
			t.Fatal(err)
		}
		server.store = &selectiveAuditFailureStore{Store: records, action: "project_secret.rotate", status: "success"}
		const value = "rotate-outcome-audit-value"
		response := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/AUDIT_ROTATE", map[string]any{"value": value})
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), value) {
			t.Fatalf("rotate outcome audit response = %d %s", response.Code, response.Body.String())
		}
		if observed, _, err := managed.Get(context.Background(), projectID, "AUDIT_ROTATE"); err != nil || observed != value {
			t.Fatalf("provider outcome = %q, %v", observed, err)
		}
	})

	t.Run("source migration", func(t *testing.T) {
		server, records, managed, projectID := newProjectSecretAPIServer(t)
		settings := server.runner.GetSystemSettings()
		settings.ScriptImports.AllowSourceSecretMigration = true
		if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
			t.Fatal(err)
		}
		const value = "migration-outcome-audit-value"
		sessionRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", nil)
		sessionID, err := server.registerSourceSecretSession(sessionRequest, projectID, map[string]string{"audit-requirement": value})
		if err != nil {
			t.Fatal(err)
		}
		server.store = &selectiveAuditFailureStore{Store: records, action: "script_import.secret.migrate", status: "success"}
		response := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
			"project_id":               projectID,
			"source_secret_session_id": sessionID,
			"secrets": []map[string]string{{
				"requirement_id": "audit-requirement",
				"secret_name":    "AUDIT_MIGRATION",
			}},
		})
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), value) {
			t.Fatalf("migration outcome audit response = %d %s", response.Code, response.Body.String())
		}
		if observed, _, err := managed.Get(context.Background(), projectID, "AUDIT_MIGRATION"); err != nil || observed != value {
			t.Fatalf("provider outcome = %q, %v", observed, err)
		}
	})
}

func TestProjectSecretMutationReconcilesLostProviderAcknowledgement(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	ambiguous := &commitAmbiguousSecretStore{delegate: managed, failCreate: true}
	server.projectSecretStore = ambiguous
	create := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", map[string]any{
		"secrets": []map[string]string{{"name": "RECONCILED", "value": "created-value"}},
	})
	if create.Code != http.StatusCreated || !ambiguous.createApplied {
		t.Fatalf("reconciled create = %d %s", create.Code, create.Body.String())
	}

	ambiguous.failCreate = false
	ambiguous.failReplace = true
	rotate := secretAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/RECONCILED", map[string]any{"value": "rotated-value"})
	if rotate.Code != http.StatusOK || !ambiguous.replaceApplied {
		t.Fatalf("reconciled rotate = %d %s", rotate.Code, rotate.Body.String())
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{ResourceID: "RECONCILED", Status: "success", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]bool{}
	for _, event := range events {
		outcome, _ := event.Details["outcome"].(string)
		outcomes[outcome] = true
	}
	if !outcomes["created_reconciled"] || !outcomes["rotated_reconciled"] {
		t.Fatalf("reconciled audit outcomes = %#v", outcomes)
	}
}

func TestProjectSecretRotationReconciliationRequiresFreshCommittedMetadata(t *testing.T) {
	const (
		projectID = "rotation-project"
		name      = "TOKEN"
		actor     = "admin"
		value     = "rotated-value"
	)
	for _, test := range []struct {
		name        string
		metadata    secretstore.Metadata
		wantSuccess bool
	}{
		{
			name: "fresh matching metadata",
			metadata: secretstore.Metadata{
				ProjectID: projectID, Name: name, Provider: "azure_key_vault", Writable: true,
				CreatedBy: actor, UpdatedAt: time.Now().UTC().Add(time.Minute),
			},
			wantSuccess: true,
		},
		{
			name: "stale metadata from same actor",
			metadata: secretstore.Metadata{
				ProjectID: projectID, Name: name, Provider: "azure_key_vault", Writable: true,
				CreatedBy: actor, UpdatedAt: time.Now().UTC().Add(-time.Minute),
			},
		},
		{
			name: "fresh metadata from prior actor",
			metadata: secretstore.Metadata{
				ProjectID: projectID, Name: name, Provider: "azure_key_vault", Writable: true,
				CreatedBy: "prior-admin", UpdatedAt: time.Now().UTC().Add(time.Minute),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &rotationMetadataProbeStore{value: value, metadata: test.metadata}
			metadata, outcome, err := replaceProjectSecretWithReconciliation(context.Background(), store, projectID, name, value, actor)
			if test.wantSuccess {
				if err != nil || outcome != "rotated_reconciled" || metadata.CreatedBy != actor {
					t.Fatalf("reconciliation = %#v, %q, %v", metadata, outcome, err)
				}
				return
			}
			if !errors.Is(err, secretstore.ErrMetadataCommit) || outcome != "provider_error_commit_ambiguous" {
				t.Fatalf("stale reconciliation = %#v, %q, %v", metadata, outcome, err)
			}
		})
	}
}

func TestProjectSecretMutationReconciliationHonorsCallerDeadline(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(context.Context, secretstore.Store) error
		calls  func(*blockingProjectSecretMutationStore) int32
	}{
		{
			name: "create",
			mutate: func(ctx context.Context, store secretstore.Store) error {
				_, _, err := createProjectSecretWithReconciliation(ctx, store, "project", "TOKEN", "value", "admin")
				return err
			},
			calls: func(store *blockingProjectSecretMutationStore) int32 { return store.createCalls.Load() },
		},
		{
			name: "replace",
			mutate: func(ctx context.Context, store secretstore.Store) error {
				_, _, err := replaceProjectSecretWithReconciliation(ctx, store, "project", "TOKEN", "value", "admin")
				return err
			},
			calls: func(store *blockingProjectSecretMutationStore) int32 { return store.replaceCalls.Load() },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &blockingProjectSecretMutationStore{}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			started := time.Now()
			err := test.mutate(ctx, store)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("mutation error = %v, want deadline exceeded", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("mutation elapsed = %s, want bounded provider call", elapsed)
			}
			if got := test.calls(store); got != 1 {
				t.Fatalf("mutation calls = %d, want 1", got)
			}
		})
	}
}

func TestProjectSecretBatchProbesHonorOneSharedDeadline(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		server, _, _, projectID := newProjectSecretAPIServer(t)
		blocking := &blockingProjectSecretStore{}
		server.projectSecretStore = blocking
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		started := time.Now()
		if _, err := server.projectSecretStatuses(request, projectID, []string{"ONE", "TWO"}); err == nil || time.Since(started) > time.Second {
			t.Fatalf("status probe error = %v elapsed=%s", err, time.Since(started))
		}
		if got := blocking.getCalls.Load(); got != 1 {
			t.Fatalf("status GET calls = %d, want cancellation before second probe", got)
		}
	})

	t.Run("create preflight", func(t *testing.T) {
		server, _, _, projectID := newProjectSecretAPIServer(t)
		blocking := &blockingProjectSecretStore{}
		server.projectSecretStore = blocking
		body := strings.NewReader(`{"secrets":[{"name":"ONE","value":"one"},{"name":"TWO","value":"two"}]}`)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", body).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		started := time.Now()
		server.mux.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || time.Since(started) > time.Second {
			t.Fatalf("create preflight = %d elapsed=%s body=%s", response.Code, time.Since(started), response.Body.String())
		}
		if got := blocking.getCalls.Load(); got != 1 || blocking.createCalls.Load() != 0 {
			t.Fatalf("create preflight calls = GET %d Create %d", got, blocking.createCalls.Load())
		}
	})

	t.Run("generic source migration preflight", func(t *testing.T) {
		server, _, _, projectID := newProjectSecretAPIServer(t)
		settings := server.runner.GetSystemSettings()
		settings.ScriptImports.AllowSourceSecretMigration = true
		if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
			t.Fatal(err)
		}
		sessionRequest := httptest.NewRequest(http.MethodPost, "/", nil)
		sessionID, err := server.registerSourceSecretSession(sessionRequest, projectID, map[string]string{"requirement": "source-value"})
		if err != nil {
			t.Fatal(err)
		}
		blocking := &blockingProjectSecretStore{}
		server.projectSecretStore = blocking
		body := strings.NewReader(`{"project_id":"` + projectID + `","source_secret_session_id":"` + sessionID + `","secrets":[{"requirement_id":"requirement","secret_name":"TOKEN"}]}`)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", body).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		started := time.Now()
		server.mux.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || time.Since(started) > time.Second ||
			!strings.Contains(response.Body.String(), `"source_secret_session_consumed":true`) {
			t.Fatalf("migration preflight = %d elapsed=%s body=%s", response.Code, time.Since(started), response.Body.String())
		}
		if got := blocking.getCalls.Load(); got != 1 || blocking.createCalls.Load() != 0 {
			t.Fatalf("migration preflight calls = GET %d Create %d", got, blocking.createCalls.Load())
		}
	})

	t.Run("Bruno preflight", func(t *testing.T) {
		server, _, _, projectID := newProjectSecretAPIServer(t)
		blocking := &blockingProjectSecretStore{}
		server.projectSecretStore = blocking
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		started := time.Now()
		_, _, _, _, err := server.bindOrMigrateBrunoImportSecrets(request, []brunoconvert.SecretRequirement{{
			ID: "requirement", SuggestedName: "TOKEN",
		}}, map[string]string{"requirement": "TOKEN"}, nil, projectID, "admin", false, brunoSecretMigrationWindow{}, nil)
		if err == nil || time.Since(started) > time.Second || blocking.getCalls.Load() != 1 {
			t.Fatalf("Bruno preflight error = %v elapsed=%s GET calls=%d", err, time.Since(started), blocking.getCalls.Load())
		}
	})
}

func TestConsumedSourceSecretSessionFailuresAlwaysDirectManualConfiguration(t *testing.T) {
	for _, test := range []struct {
		name       string
		selection  map[string]string
		configure  func(t *testing.T, server *Server, managed secretstore.Store, projectID string)
		wantStatus int
	}{
		{
			name:       "invalid selection",
			selection:  map[string]string{"requirement_id": "", "secret_name": "TOKEN"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:      "existing target",
			selection: map[string]string{"requirement_id": "requirement", "secret_name": "EXISTING"},
			configure: func(t *testing.T, _ *Server, managed secretstore.Store, projectID string) {
				if _, err := managed.Create(context.Background(), projectID, "EXISTING", "existing-value", "admin"); err != nil {
					t.Fatal(err)
				}
			},
			wantStatus: http.StatusConflict,
		},
		{
			name:      "unsupported provider",
			selection: map[string]string{"requirement_id": "requirement", "secret_name": "TOKEN"},
			configure: func(t *testing.T, server *Server, managed secretstore.Store, _ string) {
				server.projectSecretStore = &createUnsupportedSecretStore{Store: managed}
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _, managed, projectID := newProjectSecretAPIServer(t)
			settings := server.runner.GetSystemSettings()
			settings.ScriptImports.AllowSourceSecretMigration = true
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			sessionRequest := httptest.NewRequest(http.MethodPost, "/", nil)
			sessionID, err := server.registerSourceSecretSession(sessionRequest, projectID, map[string]string{"requirement": "source-value-never-returned"})
			if err != nil || sessionID == "" {
				t.Fatalf("register source session = %q, %v", sessionID, err)
			}
			if test.configure != nil {
				test.configure(t, server, managed, projectID)
			}
			response := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
				"project_id": projectID, "source_secret_session_id": sessionID, "secrets": []map[string]string{test.selection},
			})
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"source_secret_session_consumed":true`) ||
				!strings.Contains(response.Body.String(), `"manual_configuration_required":true`) || strings.Contains(response.Body.String(), "source-value-never-returned") {
				t.Fatalf("consumed failure = %d body=%s", response.Code, response.Body.String())
			}
			retry := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
				"project_id": projectID, "source_secret_session_id": sessionID, "secrets": []map[string]string{test.selection},
			})
			if retry.Code != http.StatusNotFound {
				t.Fatalf("consumed session retry = %d body=%s", retry.Code, retry.Body.String())
			}
		})
	}
}

func TestProjectSecretListAndDeletionContinueWhenOptionalDiscoveryFails(t *testing.T) {
	server, records, _, projectID := newProjectSecretAPIServer(t)
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	server.brunoArtifactWrapper = wrapper
	now := time.Now().UTC()
	created, err := records.CreateProjectSecretMetadata(&storage.ProjectSecretMetadata{
		ProjectID: projectID, SecretName: "INDEXED_TOKEN", Provider: "azure_key_vault",
		CreatedAt: now, UpdatedAt: now, CreatedBy: "admin",
	})
	if err != nil || !created {
		t.Fatalf("seed metadata = %t, %v", created, err)
	}
	script := &dsl.Script{
		ID: "discovery-outage-script", Name: "Discovery outage", Version: "1", Protocol: "http", ProjectID: projectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Call", Type: "request", Target: "/?token={{secret.UNINDEXED_TOKEN}}"}},
	}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	server.projectSecretStore = &discoveryFailureSecretStore{metadata: []secretstore.Metadata{{
		ProjectID: projectID, Name: "INDEXED_TOKEN", Provider: "azure_key_vault", CreatedAt: now, UpdatedAt: now,
	}}}
	list := secretAPIRequest(t, server, http.MethodGet, "/api/v1/projects/"+projectID+"/secrets", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"metadata_discovery_complete":false`) ||
		!strings.Contains(list.Body.String(), `"name":"INDEXED_TOKEN"`) {
		t.Fatalf("best-effort list = %d body=%s", list.Code, list.Body.String())
	}
	deleted := secretAPIRequest(t, server, http.MethodDelete, "/api/v1/projects/"+projectID, nil)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"external_secret_discovery_complete":false`) ||
		!strings.Contains(deleted.Body.String(), `"external_secret_cleanup_prefix"`) || !strings.Contains(deleted.Body.String(), "INDEXED_TOKEN") {
		t.Fatalf("best-effort deletion = %d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestProjectSecretDiscoveryRotatesBoundedBatchesUntilEveryReferencedNameIsConsidered(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	discovery := &recordingDiscoverySecretStore{}
	server.projectSecretStore = discovery
	var target strings.Builder
	target.WriteString("/")
	const referenceCount = 600
	for index := 0; index < referenceCount; index++ {
		fmt.Fprintf(&target, "?s%d={{secret.TOKEN_%03d}}", index, index)
	}
	script := &dsl.Script{
		ID: "bounded-discovery-script", Name: "Bounded discovery", Version: "1", Protocol: "http", ProjectID: projectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Call", Type: "request", Target: target.String()}},
	}
	projected := maskScriptSecrets(script)
	if projected == nil || len(projected.Actions) != 1 {
		t.Fatalf("projected script = %#v, want one action", projected)
	}
	if got := strings.Count(projected.Actions[0].Target, "{{secret."); got != referenceCount {
		t.Fatalf("projected portable secret references = %d, want %d (redactions=%d)", got, referenceCount, strings.Count(projected.Actions[0].Target, maskedScriptSecret))
	}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		complete, err := server.discoverProjectSecretMetadata(context.Background(), projectID)
		if err != nil {
			t.Fatal(err)
		}
		wantComplete := attempt == 2
		if complete != wantComplete {
			t.Fatalf("attempt %d completion = %t, want %t", attempt+1, complete, wantComplete)
		}
	}
	complete, err := server.discoverProjectSecretMetadata(context.Background(), projectID)
	if err != nil || !complete {
		t.Fatalf("completed rediscovery = %t, %v", complete, err)
	}
	discovery.mu.Lock()
	defer discovery.mu.Unlock()
	if len(discovery.batches) != 3 || len(discovery.indexed) != referenceCount {
		t.Fatalf("discovery batches = %d indexed=%d, want 3/%d", len(discovery.batches), len(discovery.indexed), referenceCount)
	}
	for index, batch := range discovery.batches {
		if len(batch) == 0 || len(batch) > projectSecretMaxBatchItems {
			t.Fatalf("batch %d size = %d, want 1-%d", index+1, len(batch), projectSecretMaxBatchItems)
		}
	}
}

func TestProjectSecretDiscoveryBatchMakesStatelessProgressAcrossAbsentNames(t *testing.T) {
	const referenceCount = 600
	pending := make([]string, 0, referenceCount)
	for index := 0; index < referenceCount; index++ {
		pending = append(pending, fmt.Sprintf("TOKEN_%03d", index))
	}
	base := time.Unix(0, 0).UTC()
	first, complete := projectSecretDiscoveryBatch("ha-project", pending, base)
	secondReplica, secondComplete := projectSecretDiscoveryBatch("ha-project", pending, base)
	if complete || secondComplete || strings.Join(first, ",") != strings.Join(secondReplica, ",") {
		t.Fatalf("same-window HA batches differ or falsely complete: first=%d second=%d complete=%t/%t", len(first), len(secondReplica), complete, secondComplete)
	}
	seen := make(map[string]struct{}, referenceCount)
	for window := 0; window < referenceCount && len(seen) < referenceCount; window++ {
		batch, batchComplete := projectSecretDiscoveryBatch(
			"ha-project",
			pending,
			base.Add(time.Duration(window)*projectSecretDiscoveryRotationInterval),
		)
		if batchComplete || len(batch) != projectSecretMaxBatchItems {
			t.Fatalf("window %d batch = %d complete=%t", window, len(batch), batchComplete)
		}
		for _, name := range batch {
			seen[name] = struct{}{}
		}
	}
	if len(seen) != referenceCount {
		t.Fatalf("stateless rotation considered %d/%d absent names", len(seen), referenceCount)
	}
}

func TestProjectSecretBatchPartialFailureReportsDurableSubsetWithoutValues(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	failing := &failNthCreateSecretStore{delegate: managed, failOnCall: 2}
	server.projectSecretStore = failing
	const firstValue = "partial-first-secret-never-return"
	const secondValue = "partial-second-secret-never-return"
	body := map[string]any{"secrets": []map[string]string{
		{"name": "PARTIAL_FIRST", "value": firstValue},
		{"name": "PARTIAL_SECOND", "value": secondValue},
	}}
	response := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", body)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("partial create status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error          string                 `json:"error"`
		Partial        bool                   `json:"partial"`
		Created        []secretstore.Metadata `json:"created"`
		CreatedCount   int                    `json:"created_count"`
		FailedName     string                 `json:"failed_name"`
		Reason         string                 `json:"reason"`
		RemainingNames []string               `json:"remaining_names"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Partial || payload.CreatedCount != 1 || len(payload.Created) != 1 || payload.Created[0].Name != "PARTIAL_FIRST" ||
		payload.FailedName != "PARTIAL_SECOND" || payload.Reason != "provider_error" || strings.Join(payload.RemainingNames, ",") != "PARTIAL_SECOND" {
		t.Fatalf("partial create payload = %+v body=%s", payload, response.Body.String())
	}
	if strings.Contains(response.Body.String(), firstValue) || strings.Contains(response.Body.String(), secondValue) {
		t.Fatalf("partial create response disclosed a value: %s", response.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "PARTIAL_FIRST")
	if err != nil || value != firstValue {
		t.Fatalf("first durable secret = %q, %v", value, err)
	}
	if _, _, err := managed.Get(context.Background(), projectID, "PARTIAL_SECOND"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("failed secret lookup error = %v, want not found", err)
	}

	// Retrying the original batch is rejected during the create-only preflight;
	// it cannot turn a partial response into an overwrite of the durable subset.
	retry := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", body)
	if retry.Code != http.StatusConflict {
		t.Fatalf("partial create retry status = %d body=%s", retry.Code, retry.Body.String())
	}
	value, _, err = managed.Get(context.Background(), projectID, "PARTIAL_FIRST")
	if err != nil || value != firstValue || failing.createCalls != 2 {
		t.Fatalf("retry changed durable subset: value=%q err=%v create_calls=%d", value, err, failing.createCalls)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{ProjectID: projectID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, _ := json.Marshal(events)
	if strings.Contains(string(encodedEvents), firstValue) || strings.Contains(string(encodedEvents), secondValue) {
		t.Fatalf("partial create audit disclosed values: %s", encodedEvents)
	}
}

func TestSourceSecretMigrationPartialFailureConsumesSessionAndReportsManualRemainder(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	failing := &failNthCreateSecretStore{delegate: managed, failOnCall: 2}
	server.projectSecretStore = failing
	const firstValue = "partial-migration-first-never-return"
	const secondValue = "partial-migration-second-never-return"
	sessionRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", nil)
	sessionID, err := server.registerSourceSecretSession(sessionRequest, projectID, map[string]string{
		"first-requirement":  firstValue,
		"second-requirement": secondValue,
	})
	if err != nil || sessionID == "" {
		t.Fatalf("register source session = %q, %v", sessionID, err)
	}
	body := map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
		"secrets": []map[string]string{
			{"requirement_id": "first-requirement", "secret_name": "MIGRATE_FIRST"},
			{"requirement_id": "second-requirement", "secret_name": "MIGRATE_SECOND"},
		},
	}
	response := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", body)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("partial migration status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Partial                     bool                   `json:"partial"`
		Migrated                    []secretstore.Metadata `json:"migrated"`
		CreatedCount                int                    `json:"created_count"`
		MigratedCount               int                    `json:"migrated_count"`
		FailedName                  string                 `json:"failed_name"`
		Reason                      string                 `json:"reason"`
		RemainingNames              []string               `json:"remaining_names"`
		SourceSecretSessionConsumed bool                   `json:"source_secret_session_consumed"`
		ManualConfigurationRequired bool                   `json:"manual_configuration_required"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Partial || payload.CreatedCount != 1 || payload.MigratedCount != 1 || len(payload.Migrated) != 1 ||
		payload.Migrated[0].Name != "MIGRATE_FIRST" || payload.FailedName != "MIGRATE_SECOND" || payload.Reason != "provider_error" ||
		strings.Join(payload.RemainingNames, ",") != "MIGRATE_SECOND" || !payload.SourceSecretSessionConsumed || !payload.ManualConfigurationRequired {
		t.Fatalf("partial migration payload = %+v body=%s", payload, response.Body.String())
	}
	if strings.Contains(response.Body.String(), firstValue) || strings.Contains(response.Body.String(), secondValue) {
		t.Fatalf("partial migration response disclosed a value: %s", response.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "MIGRATE_FIRST")
	if err != nil || value != firstValue {
		t.Fatalf("first migrated secret = %q, %v", value, err)
	}
	if _, _, err := managed.Get(context.Background(), projectID, "MIGRATE_SECOND"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("failed migrated secret lookup error = %v, want not found", err)
	}

	retry := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", body)
	if retry.Code != http.StatusNotFound {
		t.Fatalf("partial migration retry status = %d body=%s", retry.Code, retry.Body.String())
	}
	value, _, err = managed.Get(context.Background(), projectID, "MIGRATE_FIRST")
	if err != nil || value != firstValue || failing.createCalls != 2 {
		t.Fatalf("migration retry changed durable subset: value=%q err=%v create_calls=%d", value, err, failing.createCalls)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{ProjectID: projectID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, _ := json.Marshal(events)
	if strings.Contains(string(encodedEvents), firstValue) || strings.Contains(string(encodedEvents), secondValue) {
		t.Fatalf("partial migration audit disclosed values: %s", encodedEvents)
	}
}

func TestSourceSecretMigrationConsumesEncryptedSessionAcrossControllerReplicas(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const projectID = "ha-protected-session-project"
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: projectID, Name: "HA protected sessions", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x28}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	managed := &secretstore.DatabaseStore{Records: records, Wrapper: wrapper}
	first := NewServer(ServerConfig{Store: records, ProjectSecretStore: managed, BrunoArtifactWrapper: wrapper})
	settings := first.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := first.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	second := NewServer(ServerConfig{Store: records, ProjectSecretStore: managed, BrunoArtifactWrapper: wrapper})
	for _, server := range []*Server{first, second} {
		if !server.runner.GetSystemSettings().ScriptImports.AllowSourceSecretMigration {
			t.Fatal("controller replica did not load source-secret migration policy")
		}
		if server.protectedSourceSessions == nil || !server.protectedSourceSessions.Available() {
			t.Fatal("controller replica did not configure shared encrypted source sessions")
		}
	}
	const sourceValue = "cross-replica-source-value-never-on-wire"
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", nil)
	sessionID, err := first.registerSourceSecretSession(request, projectID, map[string]string{"shared-requirement": sourceValue})
	if err != nil || sessionID == "" {
		t.Fatalf("first replica register = %q, %v", sessionID, err)
	}
	if len(first.sourceSecretSessions) != 0 || len(second.sourceSecretSessions) != 0 {
		t.Fatal("durable HA session was duplicated into replica-local plaintext memory")
	}
	migrate := secretAPIRequest(t, second, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
		"secrets": []map[string]string{{
			"requirement_id": "shared-requirement",
			"secret_name":    "CROSS_REPLICA_TOKEN",
		}},
	})
	if migrate.Code != http.StatusCreated || strings.Contains(migrate.Body.String(), sourceValue) {
		t.Fatalf("second replica migration = %d %s", migrate.Code, migrate.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "CROSS_REPLICA_TOKEN")
	if err != nil || value != sourceValue {
		t.Fatalf("cross-replica migrated value = %q, %v", value, err)
	}
	replay := secretAPIRequest(t, first, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
		"secrets": []map[string]string{{
			"requirement_id": "shared-requirement",
			"secret_name":    "REPLAY_MUST_FAIL",
		}},
	})
	if replay.Code != http.StatusNotFound {
		t.Fatalf("cross-replica session replay = %d %s", replay.Code, replay.Body.String())
	}

	discardSessionID, err := first.registerSourceSecretSession(request, projectID, map[string]string{"discard-requirement": "discarded-cross-replica-value"})
	if err != nil || discardSessionID == "" {
		t.Fatalf("first replica discard-session register = %q, %v", discardSessionID, err)
	}
	discard := secretAPIRequest(t, second, http.MethodPost, "/api/v1/scripts/import/secrets/discard", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": discardSessionID,
	})
	if discard.Code != http.StatusNoContent || discard.Body.Len() != 0 {
		t.Fatalf("second replica discard = %d %s", discard.Code, discard.Body.String())
	}
	retryDiscard := secretAPIRequest(t, first, http.MethodPost, "/api/v1/scripts/import/secrets/discard", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": discardSessionID,
	})
	if retryDiscard.Code != http.StatusNoContent {
		t.Fatalf("idempotent cross-replica discard = %d %s", retryDiscard.Code, retryDiscard.Body.String())
	}
	discardedMigration := secretAPIRequest(t, first, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": discardSessionID,
		"secrets": []map[string]string{{
			"requirement_id": "discard-requirement",
			"secret_name":    "DISCARDED_MUST_NOT_MIGRATE",
		}},
	})
	if discardedMigration.Code != http.StatusNotFound {
		t.Fatalf("discarded cross-replica session migration = %d %s", discardedMigration.Code, discardedMigration.Body.String())
	}
}

func TestSourceSecretSessionDiscardClearsLocalMaterialAndIsIdempotent(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil)
	sessionID, err := server.registerSourceSecretSession(request, projectID, map[string]string{"requirement": "local-discard-value"})
	if err != nil || sessionID == "" {
		t.Fatalf("register local session = %q, %v", sessionID, err)
	}
	if got := len(server.sourceSecretSessions); got != 1 {
		t.Fatalf("local retained sessions = %d, want one before discard", got)
	}
	body := map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
	}
	for attempt := 0; attempt < 2; attempt++ {
		response := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/discard", body)
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
			t.Fatalf("discard attempt %d = %d %s", attempt+1, response.Code, response.Body.String())
		}
	}
	if got := len(server.sourceSecretSessions); got != 0 {
		t.Fatalf("local retained sessions = %d, want none after discard", got)
	}
	if _, err := server.consumeSourceSecretSession(request, projectID, sessionID); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("discarded local session consume error = %v, want not found", err)
	}
}

func TestSourceSecretSessionIsNotRetainedOrAdvertisedWithoutAtomicCreate(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	server.projectSecretStore = &createUnsupportedSecretStore{Store: managed}
	capabilities := server.projectSecretCapabilities(projectID)
	if capabilities["create_supported"] != false || capabilities["replace_supported"] != true {
		t.Fatalf("create-unsupported capabilities = %#v", capabilities)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", nil)
	sessionID, err := server.registerSourceSecretSession(request, projectID, map[string]string{
		"requirement": "must-not-be-retained",
	})
	if err != nil || sessionID != "" {
		t.Fatalf("create-unsupported source session = %q, %v", sessionID, err)
	}
	server.sourceSecretMu.Lock()
	retained := len(server.sourceSecretSessions)
	server.sourceSecretMu.Unlock()
	if retained != 0 {
		t.Fatalf("create-unsupported provider retained %d protected source session(s)", retained)
	}
}

func TestScriptSecretReferencesIncludesNamedCredentialFieldsButNotAssetReferences(t *testing.T) {
	script := &dsl.Script{
		Name:          "{{secret.DISPLAY_ONLY}}",
		GeneratedFrom: "{{secret.PROVENANCE_ONLY}}",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
			"dsn_secret": "PRESERVED_DSN",
			"token":      "{{secret.PRESERVED_TOKEN}}",
		}}},
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{DSNSecret: "database.dsn"},
			JMS: dsl.JMSConfig{TLS: &dsl.JMSTLSConfig{
				CACertSecret:  "JMS_CA",
				TrustStoreRef: "file://artifacts/truststore.jks",
			}},
		},
		DataSources: []dsl.DataSource{{
			Name:       "users",
			Type:       "file",
			StorageRef: "file://artifacts/users.csv",
		}},
		Actions: []dsl.Action{{
			Name:   "Source review",
			Type:   "source_review",
			Target: "{{secret.REVIEW_ONLY}}",
			Params: map[string]any{"dsn_secret": "REVIEW_DSN"},
		}},
	}
	if got := strings.Join(scriptSecretReferences(script), ","); got != "DATABASE_DSN,JMS_CA" {
		t.Fatalf("script secret references = %q, want only logical credential fields", got)
	}
}

func TestCloneDraftSourceMigrationIsPolicyGatedAndSingleUse(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	legacy := &dsl.Script{
		ID: "legacy-inline-script", Name: "Legacy login", Version: "1", Protocol: "http", ProjectID: projectID,
		Config: dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
			Type: "basic", Password: "legacy-plaintext-password",
		}}},
		Actions: []dsl.Action{{Name: "Call", Type: "request", Target: "/"}},
	}
	// Model a pre-upgrade row. Current durable library APIs deliberately reject
	// plaintext credentials, but Model Existing must still migrate legacy data
	// already present in an upgraded database.
	if err := records.SaveScript(legacy); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}
	disabled := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+legacy.ID+"/clone-draft", map[string]any{
		"import_detected_credential_values": true,
	})
	if disabled.Code != http.StatusOK || strings.Contains(disabled.Body.String(), "legacy-plaintext-password") {
		t.Fatalf("disabled clone = %d %s", disabled.Code, disabled.Body.String())
	}
	var disabledPayload struct {
		SessionID string `json:"source_secret_session_id"`
	}
	if err := json.Unmarshal(disabled.Body.Bytes(), &disabledPayload); err != nil {
		t.Fatal(err)
	}
	if disabledPayload.SessionID != "" {
		t.Fatalf("default-off policy created session %q", disabledPayload.SessionID)
	}
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	clone := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+legacy.ID+"/clone-draft", map[string]any{
		"import_detected_credential_values": true,
	})
	if clone.Code != http.StatusOK || strings.Contains(clone.Body.String(), "legacy-plaintext-password") {
		t.Fatalf("clone = %d %s", clone.Code, clone.Body.String())
	}
	var payload struct {
		SessionID    string                     `json:"source_secret_session_id"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(clone.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SessionID == "" || len(payload.Requirements) != 1 {
		t.Fatalf("clone payload = %+v body=%s", payload, clone.Body.String())
	}
	migrate := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": payload.SessionID,
		"secrets": []map[string]string{{
			"requirement_id": payload.Requirements[0].ID,
			"secret_name":    "MIGRATED_LOGIN_PASSWORD",
		}},
	})
	if migrate.Code != http.StatusCreated || strings.Contains(migrate.Body.String(), "legacy-plaintext-password") {
		t.Fatalf("migrate = %d %s", migrate.Code, migrate.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "MIGRATED_LOGIN_PASSWORD")
	if err != nil || value != "legacy-plaintext-password" {
		t.Fatalf("migrated value = %q, %v", value, err)
	}
	reuse := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": payload.SessionID,
		"secrets": []map[string]string{{
			"requirement_id": payload.Requirements[0].ID,
			"secret_name":    "SECOND_NAME",
		}},
	})
	if reuse.Code != http.StatusNotFound {
		t.Fatalf("session reuse status = %d body=%s", reuse.Code, reuse.Body.String())
	}
}

func TestProjectSecretAuthorizationEditorCreateAdminRotateAndServiceAccountDenial(t *testing.T) {
	server, records, managed, projectID := newProjectSecretAPIServer(t)
	enableTrustedHeaderAuth(t, server)
	now := time.Now().UTC()
	for _, user := range []*storage.User{
		{ID: "secret-editor-id", Subject: "secret-editor", Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: "secret-admin-id", Subject: "secret-admin", Active: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := records.SaveUser(user); err != nil {
			t.Fatalf("SaveUser(%s): %v", user.ID, err)
		}
	}
	for _, binding := range []*storage.UserRoleBinding{
		{ID: "secret-editor-binding", UserID: "secret-editor-id", ScopeType: "project", ScopeID: projectID, Role: RoleEditor, CreatedAt: now, UpdatedAt: now},
		{ID: "secret-admin-binding", UserID: "secret-admin-id", ScopeType: "project", ScopeID: projectID, Role: RoleProjectAdmin, CreatedAt: now, UpdatedAt: now},
	} {
		if err := records.SaveUserRoleBinding(binding); err != nil {
			t.Fatalf("SaveUserRoleBinding(%s): %v", binding.ID, err)
		}
	}

	create := secretAPIRequestAsSubject(t, server, http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", "secret-editor", map[string]any{
		"secrets": []map[string]string{{"name": "EDITOR_CREATED", "value": "created-by-editor"}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("editor create status = %d body=%s", create.Code, create.Body.String())
	}
	editorRotate := secretAPIRequestAsSubject(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/EDITOR_CREATED", "secret-editor", map[string]any{
		"value": "editor-must-not-rotate",
	})
	if editorRotate.Code != http.StatusForbidden {
		t.Fatalf("editor rotate status = %d, want 403 body=%s", editorRotate.Code, editorRotate.Body.String())
	}
	adminRotate := secretAPIRequestAsSubject(t, server, http.MethodPut, "/api/v1/projects/"+projectID+"/secrets/EDITOR_CREATED", "secret-admin", map[string]any{
		"value": "rotated-by-admin",
	})
	if adminRotate.Code != http.StatusOK {
		t.Fatalf("admin rotate status = %d body=%s", adminRotate.Code, adminRotate.Body.String())
	}
	editorMigration := secretAPIRequestAsSubject(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", "secret-editor", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": "not-authorized",
		"secrets":                  []map[string]string{{"requirement_id": "requirement", "secret_name": "MIGRATED"}},
	})
	if editorMigration.Code != http.StatusForbidden {
		t.Fatalf("editor migration status = %d, want 403 body=%s", editorMigration.Code, editorMigration.Body.String())
	}

	serviceRequest := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/secrets", strings.NewReader(`{"secrets":[{"name":"SERVICE_CREATED","value":"forbidden"}]}`))
	serviceRequest.Header.Set("Content-Type", "application/json")
	serviceRequest.SetPathValue("id", projectID)
	serviceRequest = withAuthenticatedContext(serviceRequest, authResolution{
		ServiceAccount: &AuthenticatedServiceAccount{ID: "secret-service-account"},
	})
	serviceResponse := httptest.NewRecorder()
	server.handleCreateProjectSecrets(serviceResponse, serviceRequest)
	if serviceResponse.Code != http.StatusForbidden {
		t.Fatalf("service-account create status = %d, want 403 body=%s", serviceResponse.Code, serviceResponse.Body.String())
	}

	value, _, err := managed.Get(context.Background(), projectID, "EDITOR_CREATED")
	if err != nil || value != "rotated-by-admin" {
		t.Fatalf("stored value = %q, %v; want admin rotation", value, err)
	}
	if strings.Contains(create.Body.String()+adminRotate.Body.String()+serviceResponse.Body.String(), "created-by-editor") ||
		strings.Contains(create.Body.String()+adminRotate.Body.String()+serviceResponse.Body.String(), "rotated-by-admin") {
		t.Fatal("secret write response disclosed a value")
	}
}

func TestSourceSecretSessionsEnforceBoundsScopeExpiryAndCreateOnlyMigration(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	actorRequest := func(subject string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/secrets/migrate", nil)
		return withAuthenticatedContext(req, authResolution{User: &AuthenticatedUser{
			Subject:      subject,
			GlobalRoles:  map[string]struct{}{RoleSystemAdmin: {}},
			ProjectRoles: map[string]map[string]struct{}{},
		}})
	}

	alice := actorRequest("alice")
	bob := actorRequest("bob")
	scopeSessionID, err := server.registerSourceSecretSession(alice, projectID, map[string]string{"requirement": "scope-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.consumeSourceSecretSession(bob, projectID, scopeSessionID); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("cross-actor consume error = %v, want not found", err)
	}
	ownerSession, err := server.consumeSourceSecretSession(alice, projectID, scopeSessionID)
	if err != nil {
		t.Fatalf("owner consume after cross-actor attempt: %v", err)
	}
	clearSourceSecretSession(ownerSession)

	projectSessionID, err := server.registerSourceSecretSession(alice, projectID, map[string]string{"requirement": "project-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.consumeSourceSecretSession(alice, "different-project", projectSessionID); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("cross-project consume error = %v, want not found", err)
	}
	ownerSession, err = server.consumeSourceSecretSession(alice, projectID, projectSessionID)
	if err != nil {
		t.Fatalf("owner consume after cross-project attempt: %v", err)
	}
	clearSourceSecretSession(ownerSession)

	expiredSessionID, err := server.registerSourceSecretSession(alice, projectID, map[string]string{"requirement": "expired-secret"})
	if err != nil {
		t.Fatal(err)
	}
	server.sourceSecretMu.Lock()
	server.sourceSecretSessions[expiredSessionID].ExpiresAt = time.Now().Add(-time.Second)
	server.sourceSecretMu.Unlock()
	if _, err := server.consumeSourceSecretSession(alice, projectID, expiredSessionID); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired session error = %v, want expired", err)
	}

	if _, err := server.registerSourceSecretSession(alice, projectID, map[string]string{
		"oversized": strings.Repeat("x", projectSecretMaxValueBytes+1),
	}); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized source value error = %v", err)
	}
	aggregate := make(map[string]string)
	for index := 0; index < 17; index++ {
		aggregate[string(rune('A'+index))] = strings.Repeat("x", projectSecretMaxValueBytes)
	}
	if _, err := server.registerSourceSecretSession(alice, projectID, aggregate); err == nil || !strings.Contains(err.Error(), "session limit") {
		t.Fatalf("aggregate source value error = %v", err)
	}
	tooMany := make(map[string]string)
	for index := 0; index <= projectSecretMaxBatchItems; index++ {
		tooMany[strconv.Itoa(index)] = "x"
	}
	if _, err := server.registerSourceSecretSession(alice, projectID, tooMany); err == nil || !strings.Contains(err.Error(), "entry session limit") {
		t.Fatalf("source entry count error = %v", err)
	}

	if _, err := managed.Create(context.Background(), projectID, "EXISTING", "original-value", "admin"); err != nil {
		t.Fatal(err)
	}
	conflictSessionID, err := server.registerSourceSecretSession(alice, projectID, map[string]string{"conflict-requirement": "must-not-overwrite"})
	if err != nil {
		t.Fatal(err)
	}
	migrationBody, err := json.Marshal(map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": conflictSessionID,
		"secrets": []map[string]string{{
			"requirement_id": "conflict-requirement",
			"secret_name":    "EXISTING",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	migrationRequest := actorRequest("alice")
	migrationRequest.Body = io.NopCloser(bytes.NewReader(migrationBody))
	migrationRequest.ContentLength = int64(len(migrationBody))
	migrationRequest.Header.Set("Content-Type", "application/json")
	migrationResponse := httptest.NewRecorder()
	server.handleMigrateSourceSecrets(migrationResponse, migrationRequest)
	if migrationResponse.Code != http.StatusConflict {
		t.Fatalf("conflicting migration status = %d body=%s", migrationResponse.Code, migrationResponse.Body.String())
	}
	if strings.Contains(migrationResponse.Body.String(), "must-not-overwrite") {
		t.Fatalf("conflict response disclosed source value: %s", migrationResponse.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "EXISTING")
	if err != nil || value != "original-value" {
		t.Fatalf("existing value after conflict = %q, %v", value, err)
	}
}

func TestProjectSecretRoutesDeclareWriteRoles(t *testing.T) {
	tests := []struct {
		method string
		path   string
		role   string
	}{
		{http.MethodPost, "/api/v1/projects/p/secrets", RoleEditor},
		{http.MethodPut, "/api/v1/projects/p/secrets/LOGIN", RoleProjectAdmin},
		{http.MethodPost, "/api/v1/scripts/import/secrets/migrate", RoleProjectAdmin},
		{http.MethodPost, "/api/v1/scripts/import/secrets/discard", RoleProjectAdmin},
	}
	for _, test := range tests {
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"project_id":"p"}`))
		requirement, ok := accessRequirementForRequest(req)
		if !ok || !containsRoleString(requirement.GlobalRoles, test.role) || requirement.ProjectLookup == nil {
			t.Errorf("%s %s requirement = %+v, ok=%t", test.method, test.path, requirement, ok)
		}
	}
}

func secretAPIRequest(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(encoded))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	return response
}

func secretAPIRequestAsSubject(t *testing.T, server *Server, method, path, subject string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(encoded))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", subject)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	return response
}
