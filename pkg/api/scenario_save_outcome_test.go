package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// scenarioLostCommitAckStore commits the conditioned write and then reports
// the commit outcome as unknown, as a connection reset during COMMIT does.
type scenarioLostCommitAckStore struct {
	*storage.SQLiteStore
}

func (s *scenarioLostCommitAckStore) UpdateScenarioAuthoringWithSourcesIfCurrent(scenario *storage.Scenario, groups []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding, expected storage.ScenarioAuthoringPrecondition, admission storage.ScenarioSourceAdmission) (*storage.ScenarioAuthoringMutationResult, error) {
	if _, err := s.SQLiteStore.UpdateScenarioAuthoringWithSourcesIfCurrent(scenario, groups, pools, expected, admission); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: connection reset during commit", storage.ErrScenarioAuthoringOutcomeUnknown)
}

// A save whose commit acknowledgement is lost may have committed, so the
// credentials it provisioned are left to recovery instead of rolled back.
func TestScenarioSaveWithALostCommitAckKeepsItsCredentialsForRecovery(t *testing.T) {
	const secret = "lost-ack-runtime-secret"
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)
	script := httpScript("lost-ack-script", "Lost ack")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	actor := testCreationActor{Type: "user", Subject: "scenario-editor", DisplayName: "Scenario Editor"}
	created, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Before", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}, creationActor: actor})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := records.GetScenarioAuthoringSnapshot(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	cached, _ := server.runner.GetScenario(created.ID)
	server.runner.scenarioStore = &scenarioLostCommitAckStore{SQLiteStore: records}
	_, _, _, err = server.runner.UpdateScenarioAuthoringDurable(created.ID, CreateScenarioRequest{
		Name: "After", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": secret}}, creationActor: actor,
	}, storage.ScenarioAuthoringPrecondition{ProjectID: snapshot.ProjectID, ContentRevision: snapshot.ContentRevision, CreatedAt: snapshot.Scenario.CreatedAt}, cached.UpdatedAt)
	server.runner.scenarioStore = records
	if !errors.Is(err, storage.ErrScenarioAuthoringOutcomeUnknown) {
		t.Fatalf("lost commit acknowledgement: %v", err)
	}
	name := runtimeOverrideSecretName(created.ID, -1, "env.API_TOKEN")
	if value, _, getErr := secrets.Get(context.Background(), storage.DefaultProjectID, name); getErr != nil || value != secret {
		t.Fatalf("a possibly committed save rolled back its credential: %q %v", value, getErr)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 1 {
		t.Fatalf("the provisioning attempt was not left to recovery: %#v %v", attempts, err)
	}
}

// Without HA a conditioned save against the durable revision still reloads
// a stale replica cache instead of reporting a false conflict.
func TestScenarioConditionedSaveRefreshesAStaleCacheWithoutHA(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	external, children, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	external.Name = "Saved by another replica"
	external.UpdatedAt = external.UpdatedAt.Add(time.Second)
	if err := store.SaveScenario(external, children); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := scenarioAuthoringETag(snapshot.ContentRevision)
	if err != nil {
		t.Fatal(err)
	}
	if response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Conditioned without HA", 2), tag); response.Code != http.StatusOK {
		t.Fatalf("conditioned save against the durable revision without HA: %d %s", response.Code, response.Body.String())
	}
}

// fingerprintAtCommitStore records the commit fingerprint of the typed
// scenario the create writes, as the credential attempt journals it.
type fingerprintAtCommitStore struct {
	*storage.SQLiteStore
	committed string
}

func (s *fingerprintAtCommitStore) CreateScenarioAuthoringIfAbsent(scenario *storage.Scenario, groups []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding) (*storage.ScenarioAuthoringMutationResult, error) {
	s.committed = scenarioRuntimeCommitFingerprint(scenario, groups)
	return s.SQLiteStore.CreateScenarioAuthoringIfAbsent(scenario, groups, pools)
}

// The commit fingerprint a credential attempt records equals the one
// recovery recomputes from storage, where typed settings come back as maps
// with sorted keys: recovery must keep a committed scenario's credentials.
func TestScenarioRuntimeCommitFingerprintSurvivesStorage(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store})
	script := httpScript("fingerprint-script", "Fingerprint")
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	exerciseScenarioRuntimeCommitFingerprint(t, server, store, script.ID)
}

// exerciseScenarioRuntimeCommitFingerprint creates a scenario with typed
// settings and a group SLA no 4-byte float holds exactly, then recomputes
// the fingerprint from what the store returns.
func exerciseScenarioRuntimeCommitFingerprint(t *testing.T, server *Server, store *storage.SQLiteStore, scriptID string) {
	t.Helper()
	recorder := &fingerprintAtCommitStore{SQLiteStore: store}
	server.runner.scenarioStore = recorder
	thinkTime := &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 250, MinPercent: 50, MaxPercent: 150}
	sla := 250.1
	created, _, err := server.runner.CreateScenarioAuthoringDurable("", CreateScenarioRequest{
		Name: "Fingerprint", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1, RuntimeSettings: &RuntimeSettingsOverride{ThinkTime: thinkTime}, TransactionResponseTimeSLAMs: &sla}},
		RuntimeSettings: &RuntimeSettingsOverride{ThinkTime: thinkTime},
	})
	server.runner.scenarioStore = store
	if err != nil {
		t.Fatal(err)
	}
	stored, scripts, err := store.GetScenario(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || scripts[0].TransactionResponseTimeSLAMs == nil {
		t.Fatalf("stored groups %+v", scripts)
	}
	if recorder.committed == "" || scenarioRuntimeCommitFingerprint(stored, scripts) != recorder.committed {
		t.Fatalf("recovered fingerprint %s, committed %s (stored SLA %v)", scenarioRuntimeCommitFingerprint(stored, scripts), recorder.committed, *scripts[0].TransactionResponseTimeSLAMs)
	}
}

// A plain read on a replica whose cached copy is stale (another controller
// saved it, without HA leadership) refreshes once and returns the durable
// body with its validator, so the editor's "Load current baseline" recovers.
func TestScenarioReadRefreshesAStaleCacheWithoutHA(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	external, children, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	external.Name = "Saved by another replica"
	external.UpdatedAt = external.UpdatedAt.Add(time.Second)
	if err := store.SaveScenario(external, children); err != nil {
		t.Fatal(err)
	}
	response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID, nil, "")
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" || !strings.Contains(response.Body.String(), "Saved by another replica") {
		t.Fatalf("stale replica read: %d etag=%q %s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
}
