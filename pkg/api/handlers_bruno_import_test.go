package api

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

const brunoImportTestProject = "project-bruno-api"

func TestBrunoNativePreflightTreatsProtectedSourceCredentialAsRunnable(t *testing.T) {
	const requirementID = "source-password"
	result := &brunoconvert.Result{
		Scripts: []brunoconvert.NativeScriptCandidate{{
			CandidateID: "request-1",
			Script: &dsl.Script{
				Name: "Request", Protocol: "http",
				Actions: []dsl.Action{{Name: "GET", Type: "request", Method: http.MethodGet, Target: "https://example.test"}},
			},
			SecretRequirements: []brunoconvert.SecretRequirement{{
				ID: requirementID,
			}},
		}},
	}

	decode := func(value map[string]any) struct {
		Ready      bool `json:"ready"`
		Blockers   int  `json:"blockers"`
		Candidates []struct {
			Ready    bool     `json:"ready"`
			Blockers []string `json:"blockers"`
		} `json:"candidates"`
	} {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Ready      bool `json:"ready"`
			Blockers   int  `json:"blockers"`
			Candidates []struct {
				Ready    bool     `json:"ready"`
				Blockers []string `json:"blockers"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	missing := decode(brunoNativeExecutionPreflight(result, nil))
	if missing.Ready || missing.Blockers != 1 ||
		len(missing.Candidates) != 1 ||
		!stringSliceContainsEqualFold(missing.Candidates[0].Blockers, "secret_bindings_required") {
		t.Fatalf("missing protected credential preflight = %#v", missing)
	}

	retained := decode(brunoNativeExecutionPreflight(result, map[string]bool{requirementID: true}))
	if !retained.Ready || retained.Blockers != 0 ||
		len(retained.Candidates) != 1 || !retained.Candidates[0].Ready ||
		len(retained.Candidates[0].Blockers) != 0 {
		t.Fatalf("retained protected credential preflight = %#v", retained)
	}
}

func TestBrunoSecretMatchesRetainedMigration(t *testing.T) {
	started := time.Now().UTC()
	window := brunoSecretMigrationWindow{StartedAt: started, ExpiresAt: started.Add(30 * time.Minute)}
	for _, test := range []struct {
		name     string
		value    string
		metadata secretstore.Metadata
		actor    string
		want     bool
	}{
		{
			name:  "same session",
			value: "source-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-a",
				CreatedAt: started.Add(time.Second),
			},
			actor: "actor-a",
			want:  true,
		},
		{
			name:  "different value",
			value: "other-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-a",
				CreatedAt: started.Add(time.Second),
			},
			actor: "actor-a",
		},
		{
			name:  "different actor",
			value: "source-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-b",
				CreatedAt: started.Add(time.Second),
			},
			actor: "actor-a",
		},
		{
			name:  "outside session window",
			value: "source-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-a",
				CreatedAt: started.Add(-2 * time.Minute),
			},
			actor: "actor-a",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := brunoSecretMatchesRetainedMigration(
				test.value, "source-value", test.metadata, test.actor, window,
			)
			if got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}

func rewriteBrunoImportSessionForTest(t *testing.T, server *Server, id string, mutate func(*brunoImportSession)) *brunoImportSession {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	session, err := server.consumeBrunoImportSession(request, id, brunoImportTestProject)
	if err != nil {
		t.Fatalf("consume test Bruno import session: %v", err)
	}
	mutate(session)
	if err := server.persistBrunoImportSession(context.Background(), session); err != nil {
		t.Fatalf("persist rewritten Bruno import session: %v", err)
	}
	server.storeBrunoImportSession(session)
	return session
}

func TestGroupedBrunoRequestedBindingsRejectsConflictingDuplicateReferenceTargets(t *testing.T) {
	requirements := []brunoconvert.SecretRequirement{
		{ID: "first", SuggestedName: "API_TOKEN", ExistingReference: true},
		{ID: "second", SuggestedName: "API_TOKEN", ExistingReference: true},
	}
	grouped, err := groupedBrunoRequestedBindings(requirements, map[string]string{
		"first":  "project-api-token",
		"second": "PROJECT.API.TOKEN",
	})
	if err != nil || grouped["API_TOKEN"] != "PROJECT_API_TOKEN" {
		t.Fatalf("compatible aliases were not grouped: %#v, %v", grouped, err)
	}
	if _, err := groupedBrunoRequestedBindings(requirements, map[string]string{
		"first":  "FIRST_TOKEN",
		"second": "SECOND_TOKEN",
	}); err == nil || !strings.Contains(err.Error(), "cannot target multiple") {
		t.Fatalf("conflicting grouped binding error = %v", err)
	}
}

func TestSetBrunoDestinationPreservationEvidenceExplainsUnavailableCases(t *testing.T) {
	tests := []struct {
		name              string
		requirement       brunoconvert.SecretRequirement
		sourceRetained    bool
		destinationMapped bool
		wantPreserved     bool
		wantReason        string
	}{
		{
			name:              "exact value mapped",
			sourceRetained:    true,
			destinationMapped: true,
			wantPreserved:     true,
		},
		{
			name:              "source value unavailable",
			destinationMapped: true,
			wantReason:        brunoconvert.PreservationUnavailableSourceValue,
		},
		{
			name: "existing governed reference",
			requirement: brunoconvert.SecretRequirement{
				ExistingReference: true,
			},
			sourceRetained:    true,
			destinationMapped: true,
			wantReason:        brunoconvert.PreservationUnavailableReference,
		},
		{
			name:              "destination mapping incomplete",
			sourceRetained:    true,
			destinationMapped: false,
			wantReason:        brunoconvert.PreservationUnavailableDestination,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requirement := test.requirement
			requirement.DestinationValuePreserved = !test.wantPreserved
			requirement.PreservationUnavailableReason = "stale-reason"
			setBrunoDestinationPreservationEvidence(
				&requirement,
				test.sourceRetained,
				test.destinationMapped,
			)
			if requirement.DestinationValuePreserved != test.wantPreserved ||
				requirement.PreservationUnavailableReason != test.wantReason {
				t.Fatalf("preservation evidence = %#v", requirement)
			}
		})
	}
}

func TestBrunoRevisionPrivateKeySourceRestriction(t *testing.T) {
	if brunoRevisionHasPrivateKeyFiles(nil) {
		t.Fatal("nil revision reported private-key files")
	}
	revision := &storage.ImportSourceRevision{SecurityProfile: map[string]any{
		"capabilities": map[string]any{"has_private_key_files": true},
	}}
	if !brunoRevisionHasPrivateKeyFiles(revision) {
		t.Fatal("private-key capability was not detected")
	}
}

func TestBrunoImportSessionScopeMismatchDoesNotConsumeOwnerSession(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := []byte("info: {name: Scoped, type: http}\nhttp: {method: GET, url: https://example.test}\n")
	ownerRequest := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	}
	consumeAsOwner := func(sessionID string) {
		session, err := server.consumeBrunoImportSession(ownerRequest(), sessionID, brunoImportTestProject)
		if err != nil {
			t.Fatalf("owner consume after rejected scope attempt: %v", err)
		}
		server.releaseBrunoImportSession(session, false, true)
	}

	t.Run("actor", func(t *testing.T) {
		inspect := inspectBrunoJSON(t, server, source, "")
		wrongActor := withAuthenticatedContext(ownerRequest(), authResolution{User: &AuthenticatedUser{Subject: "other-actor"}})
		if _, err := server.consumeBrunoImportSession(wrongActor, inspect.ImportSessionID, brunoImportTestProject); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("wrong-actor consume error = %v, want not found", err)
		}
		consumeAsOwner(inspect.ImportSessionID)
	})

	t.Run("project", func(t *testing.T) {
		inspect := inspectBrunoJSON(t, server, source, "")
		if _, err := server.consumeBrunoImportSession(ownerRequest(), inspect.ImportSessionID, "another-project"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("wrong-project consume error = %v, want not found", err)
		}
		consumeAsOwner(inspect.ImportSessionID)
	})
}

func TestBoundedBrunoImportSecretValues(t *testing.T) {
	requirements := func(count, valueBytes int) []brunoconvert.SecretRequirement {
		result := make([]brunoconvert.SecretRequirement, count)
		for index := range result {
			result[index] = brunoconvert.SecretRequirement{
				ID:    fmt.Sprintf("requirement-%d", index),
				Value: strings.Repeat("s", valueBytes),
			}
		}
		return result
	}
	tests := []struct {
		name         string
		requirements []brunoconvert.SecretRequirement
		wantError    string
	}{
		{name: "project secret value limit does not constrain preservation", requirements: requirements(1, projectSecretMaxValueBytes+1)},
		{name: "duplicate id", requirements: []brunoconvert.SecretRequirement{{ID: "duplicate", Value: "one"}, {ID: "duplicate", Value: "two"}}, wantError: "duplicated"},
		{name: "invalid id", requirements: []brunoconvert.SecretRequirement{{ID: " invalid", Value: "one"}}, wantError: "invalid id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, err := boundedBrunoImportSecretValues(test.requirements)
			if test.wantError == "" {
				if err != nil || len(values) != len(test.requirements) {
					t.Fatalf("bounded values count=%d error=%v", len(values), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestBoundedBrunoImportSecretValuesOmitsRedactionSentinels(t *testing.T) {
	values, err := boundedBrunoImportSecretValues([]brunoconvert.SecretRequirement{
		{ID: "exact", Value: " [rEdAcTeD] "},
		{ID: "authorization", Value: "Bearer [REDACTED]"},
		{ID: "cookie", Value: "sid=[redacted]"},
		{ID: "percent", Value: "%5BReDaCtEd%5D"},
		{ID: "nested-percent", Value: "%25255BReDaCtEd%25255D"},
		{ID: "malformed-percent", Value: "%5BReDaCtEd%5D%ZZ"},
		{ID: "xml", Value: "&#91;ReDaCtEd&#93;"},
		{ID: "nested-xml", Value: "&amp;amp;#91;ReDaCtEd&amp;amp;#93;"},
		{ID: "depth-cap", Value: strings.Repeat("%25", 40) + "5BReDaCtEd%5D"},
		{ID: "usable", Value: "real-source-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values["usable"] != "real-source-value" {
		t.Fatalf("bounded Bruno source values = %#v", values)
	}
}

func TestProjectDeletionSweepsPersistedBrunoSessionAfterControllerRestart(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Restart, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: restart-source-secret}
`), "")
	if inspect.ImportSessionID == "" {
		t.Fatal("inspect returned no import session")
	}
	server.brunoImportMu.Lock()
	for _, session := range server.brunoImportSessions {
		if session != nil && session.Timer != nil {
			session.Timer.Stop()
			session.Timer = nil
		}
	}
	server.brunoImportSessions = make(map[string]*brunoImportSession)
	server.brunoImportMu.Unlock()

	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5a}, 32),
		KeyID: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewServer(ServerConfig{
		Store:                store,
		MaxConcurrent:        2,
		ImportAssetDir:       filepath.Dir(artifactDir),
		BrunoArtifactWrapper: wrapper,
	})
	response := httptest.NewRecorder()
	restarted.mux.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+brunoImportTestProject, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("delete project status = %d body=%s", response.Code, response.Body.String())
	}
	assertDirectoryHasNoRegularFiles(t, artifactDir)
}

func TestExpiredBrunoSessionJanitorDoesNotRaceCrashClaimAfterControllerRestart(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Expired, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: expired-source-secret}
`), "")
	server.brunoImportMu.Lock()
	for _, session := range server.brunoImportSessions {
		if session != nil && session.Timer != nil {
			session.Timer.Stop()
			session.Timer = nil
		}
	}
	server.brunoImportSessions = make(map[string]*brunoImportSession)
	server.brunoImportMu.Unlock()

	sessionDir := filepath.Join(artifactDir, ".sessions")
	descriptorPath := filepath.Join(sessionDir, inspect.ImportSessionID+".json")
	descriptorData, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor importartifact.SessionDescriptor
	if err := json.Unmarshal(descriptorData, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	descriptorData, err = json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath, descriptorData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(descriptorPath, filepath.Join(sessionDir, inspect.ImportSessionID+".claim.json")); err != nil {
		t.Fatal(err)
	}

	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5a}, 32),
		KeyID: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewServer(ServerConfig{
		Store:                store,
		MaxConcurrent:        2,
		ImportAssetDir:       filepath.Dir(artifactDir),
		BrunoArtifactWrapper: wrapper,
	})
	if err := restarted.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatalf("prune must ignore an in-flight claim: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, inspect.ImportSessionID+".claim.json")); err != nil {
		t.Fatalf("in-flight claim descriptor stat error = %v", err)
	}
	objects, err := restarted.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.DeleteProject(context.Background(), brunoImportTestProject); err != nil {
		t.Fatalf("project cleanup must sweep abandoned claims: %v", err)
	}
	assertDirectoryHasNoRegularFiles(t, artifactDir)
}

func TestExpiredBrunoSessionJanitorDefersSourceCollectionDuringPublicationGrace(t *testing.T) {
	server, _, artifactDir := newBrunoImportAPITestServer(t)
	inspected := inspectBrunoJSON(t, server, []byte(`info: {name: Grace, type: http}
http: {method: GET, url: https://example.test/grace}
`), "")

	server.brunoImportMu.Lock()
	session := server.brunoImportSessions[inspected.ImportSessionID]
	if session == nil {
		server.brunoImportMu.Unlock()
		t.Fatal("inspect session was not retained")
	}
	if session.Timer != nil {
		session.Timer.Stop()
		session.Timer = nil
	}
	rawArtifact := session.RawArtifact
	server.brunoImportMu.Unlock()

	expiredAt := time.Now().UTC().Add(-time.Minute)
	expireBrunoSessionDescriptor(t, artifactDir, inspected.ImportSessionID, expiredAt)
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	request := importartifact.OpenRequest{
		BackendKey: rawArtifact.BackendKey,
		ProjectID:  rawArtifact.ProjectID,
		Kind:       rawArtifact.ArtifactKind,
		SHA256:     rawArtifact.SHA256,
		SizeBytes:  rawArtifact.SizeBytes,
	}
	if _, err := objects.ReadAll(context.Background(), request, rawArtifact.SizeBytes); err != nil {
		t.Fatalf("source was collected during publication grace: %v", err)
	}
	descriptorPath := filepath.Join(artifactDir, ".sessions", inspected.ImportSessionID+".json")
	if _, err := os.Stat(descriptorPath); err != nil {
		t.Fatalf("reservation descriptor was removed during publication grace: %v", err)
	}

	afterGrace := expiredAt.Add(brunoArtifactCollectionGrace + time.Second)
	if err := server.pruneExpiredBrunoImportSessions(afterGrace); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), request, rawArtifact.SizeBytes); err == nil {
		t.Fatal("unreferenced source remained after collection grace")
	}
}

func TestExpiredBrunoSessionJanitorFailsClosedWithoutDatabaseReferenceChecker(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	inspected := inspectBrunoJSON(t, server, []byte(`info: {name: Unknown references, type: http}
http: {method: GET, url: https://example.test/unknown}
`), "")

	server.brunoImportMu.Lock()
	session := server.brunoImportSessions[inspected.ImportSessionID]
	if session == nil {
		server.brunoImportMu.Unlock()
		t.Fatal("inspect session was not retained")
	}
	if session.Timer != nil {
		session.Timer.Stop()
		session.Timer = nil
	}
	rawArtifact := session.RawArtifact
	server.brunoImportMu.Unlock()

	expiredAt := time.Now().UTC().Add(-brunoArtifactCollectionGrace - time.Minute)
	expireBrunoSessionDescriptor(t, artifactDir, inspected.ImportSessionID, expiredAt)
	server.store = storeOverlay(&brunoStoreWithoutArtifactReferenceChecker{Store: store})
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), importartifact.OpenRequest{
		BackendKey: rawArtifact.BackendKey,
		ProjectID:  rawArtifact.ProjectID,
		Kind:       rawArtifact.ArtifactKind,
		SHA256:     rawArtifact.SHA256,
		SizeBytes:  rawArtifact.SizeBytes,
	}, rawArtifact.SizeBytes); err != nil {
		t.Fatalf("unknown database reference state deleted source evidence: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, ".sessions", inspected.ImportSessionID+".json")); err != nil {
		t.Fatalf("cleanup locator descriptor was removed: %v", err)
	}
}

func expireBrunoSessionDescriptor(t *testing.T, artifactDir, sessionID string, expiresAt time.Time) {
	t.Helper()
	descriptorPath := filepath.Join(artifactDir, ".sessions", sessionID+".json")
	descriptorData, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor importartifact.SessionDescriptor
	if err := json.Unmarshal(descriptorData, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.ExpiresAt = expiresAt.UTC()
	descriptorData, err = json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath, descriptorData, 0o600); err != nil {
		t.Fatal(err)
	}
}

func brunoNativeCredentialTargetForTest(
	t *testing.T,
	script *dsl.Script,
	requirementID string,
	pointer string,
	occurrence int,
) brunoconvert.NativeCredentialTarget {
	t.Helper()
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	value, ok := jsonPointerStringValue(document, pointer)
	if !ok {
		t.Fatalf("target pointer %q is not a string", pointer)
	}
	sum := sha256.Sum256([]byte(value))
	return brunoconvert.NativeCredentialTarget{
		RequirementID:   requirementID,
		JSONPointer:     pointer,
		Occurrence:      occurrence,
		SanitizedSHA256: fmt.Sprintf("%x", sum[:]),
	}
}

func TestApplyBrunoNativeCredentialTargetsDoesNotCascade(t *testing.T) {
	script := &dsl.Script{
		Name:     "{{secret.PASSWORD}}",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name: "request",
			Type: "request",
			HeaderList: dsl.HeaderList{
				{Name: "X-Password", Value: "{{secret.PASSWORD}}"},
				{Name: "X-Token", Value: "{{secret.TOKEN}}"},
			},
		}},
	}
	requirements := []brunoconvert.SecretRequirement{
		{ID: "password", SuggestedName: "PASSWORD", Replacement: "{{secret.PASSWORD}}"},
		{ID: "token", SuggestedName: "TOKEN", Replacement: "{{secret.TOKEN}}"},
	}
	targets := []brunoconvert.NativeCredentialTarget{
		brunoNativeCredentialTargetForTest(t, script, "password", "/actions/0/header_list/0/value", 0),
		brunoNativeCredentialTargetForTest(t, script, "token", "/actions/0/header_list/1/value", 0),
	}
	bound, err := applyBrunoNativeCredentialTargets(
		script,
		targets,
		requirements,
		map[string]string{
			"password": "{{secret.TOKEN}}",
			"token":    "{{secret.FINAL_TOKEN}}",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := bound.Actions[0].HeaderList[0].Value; got != "{{secret.TOKEN}}" {
		t.Fatalf("PASSWORD binding cascaded to %q", got)
	}
	if got := bound.Actions[0].HeaderList[1].Value; got != "{{secret.FINAL_TOKEN}}" {
		t.Fatalf("TOKEN binding = %q", got)
	}
	if bound.Name != "{{secret.PASSWORD}}" {
		t.Fatalf("credential target binding escaped into unrelated script name %q", bound.Name)
	}
}

func TestAllowedBrunoNativeCredentialTargetPointers(t *testing.T) {
	for _, test := range []struct {
		pointer string
		want    bool
	}{
		{pointer: "/config/base_url", want: true},
		{pointer: "/config/http/auth/password", want: true},
		{pointer: "/actions/0/target", want: true},
		{pointer: "/actions/0/body", want: true},
		{pointer: "/actions/0/headers/X~1Password", want: true},
		{pointer: "/actions/0/header_list/2/value", want: true},
		{pointer: "/actions/0/auth/token", want: true},
		{pointer: "/name", want: false},
		{pointer: "/metadata/import/preserved/source", want: false},
		{pointer: "/actions/0/metadata/source_path", want: false},
		{pointer: "/actions/not-an-index/body", want: false},
		{pointer: "/actions/0/header_list/not-an-index/value", want: false},
	} {
		t.Run(test.pointer, func(t *testing.T) {
			tokens, err := splitJSONPointer(test.pointer)
			if err != nil {
				t.Fatal(err)
			}
			if got := isAllowedBrunoNativeCredentialTargetPointer(tokens); got != test.want {
				t.Fatalf("allowed = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPlanBrunoNativeSecretBindingsPreservesSourceValueByDefault(t *testing.T) {
	const value = "quote\"-slash\\-line\nend"
	requirements := []brunoconvert.SecretRequirement{{
		ID: "requirement-password", SuggestedName: "PASSWORD", Replacement: "{{secret.PASSWORD}}",
		Value: value, SourceValueAvailable: true,
	}}

	bindings, replacements, err := planBrunoNativeSecretBindings(requirements, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 || replacements["requirement-password"] != value {
		t.Fatalf("default plan bindings=%#v replacements=%#v", bindings, replacements)
	}
	script := &dsl.Script{
		Name: "preserved", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "request", Type: "request",
			HeaderList: dsl.HeaderList{{Name: "X-Password", Value: "{{secret.PASSWORD}}"}},
		}},
	}
	targets := []brunoconvert.NativeCredentialTarget{
		brunoNativeCredentialTargetForTest(
			t,
			script,
			"requirement-password",
			"/actions/0/header_list/0/value",
			0,
		),
	}
	preserved, err := applyBrunoNativeCredentialTargets(
		script,
		targets,
		requirements,
		replacements,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := preserved.Actions[0].HeaderList[0].Value; got != value {
		t.Fatalf("preserved value = %q, want %q", got, value)
	}
}

func TestPlanBrunoNativeSecretBindingsExternalizesOnlyWhenRequested(t *testing.T) {
	requirements := []brunoconvert.SecretRequirement{{
		ID: "requirement-password", SuggestedName: "PASSWORD", Replacement: "{{secret.PASSWORD}}",
		Value: "source-password", SourceValueAvailable: true,
	}}
	for _, test := range []struct {
		name      string
		requested map[string]string
		migrate   bool
		want      string
	}{
		{name: "explicit", requested: map[string]string{"requirement-password": "PROJECT_PASSWORD"}, want: "{{secret.PROJECT_PASSWORD}}"},
		{name: "migration", migrate: true, want: "{{secret.PASSWORD}}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bindings, replacements, err := planBrunoNativeSecretBindings(requirements, test.requested, test.migrate)
			if err != nil {
				t.Fatal(err)
			}
			if bindings["PASSWORD"] != test.want ||
				replacements["requirement-password"] != test.want {
				t.Fatalf("bindings=%#v replacements=%#v, want %q", bindings, replacements, test.want)
			}
		})
	}
}

func TestBrunoRawUploadCanSelectNativeOutput(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := []byte(`info: {name: Raw native, uid: raw-native, type: http}
http:
  method: GET
  url: https://example.test
`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect?project_id="+url.QueryEscape(brunoImportTestProject)+
			"&filename=request.yml&output_mode=native_requests",
		bytes.NewReader(source),
	)
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("native raw inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Result.OutputMode != brunoconvert.OutputModeNativeRequests ||
		len(inspected.Result.Scripts) != 1 {
		t.Fatalf("native raw result = %#v", inspected.Result)
	}
}

func TestBrunoJSONQueryCanSelectNativeOutputAndRejectsConflict(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := "info: {name: JSON native, uid: json-native, type: http}\nhttp: {method: GET, url: https://example.test}\n"
	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: brunoImportTestProject,
		Filename:  "request.yml",
		Content:   source,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect?output_mode=native_requests",
		bytes.NewReader(payload),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("JSON query native inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Result == nil || inspected.Result.OutputMode != brunoconvert.OutputModeNativeRequests {
		t.Fatalf("JSON query native result = %#v", inspected.Result)
	}

	conflictingPayload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: brunoImportTestProject,
		Filename:  "request.yml",
		Content:   source,
		Options: brunoconvert.InspectOptions{
			OutputMode: brunoconvert.OutputModeCanonical,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect?output_mode=native_requests",
		bytes.NewReader(conflictingPayload),
	)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "conflicts") {
		t.Fatalf("JSON output mode conflict status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestCleanupUnreferencedBrunoArtifactsDeletesFailedPublication(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("failed-publication-source")
	object, err := objects.PutBytes(context.Background(), brunoImportTestProject, "raw", payload, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	artifact := storage.ImportSourceArtifact{
		ProjectID: brunoImportTestProject, ArtifactKind: "raw",
		BackendKey: object.BackendKey, SHA256: object.SHA256, SizeBytes: object.SizeBytes,
	}
	server.cleanupUnreferencedBrunoArtifacts([]storage.ImportSourceArtifact{artifact})
	if _, err := objects.ReadAll(
		context.Background(),
		brunoArtifactOpenRequest(artifact),
		int64(len(payload)),
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed-publication artifact survived cleanup: %v", err)
	}
}

func TestExpiredBrunoPublicationJournalRecoversInterruptedSourceWrite(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("interrupted-source-write")
	planned, err := objects.PlanBytes(brunoImportTestProject, "raw", payload, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.SavePublication(importartifact.PublicationDescriptor{
		ID:        "interrupted-source",
		ProjectID: brunoImportTestProject,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Objects:   []importartifact.OpenRequest{planned},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.PutBytes(
		context.Background(), brunoImportTestProject, "raw", payload, int64(len(payload)),
	); err != nil {
		t.Fatal(err)
	}
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), planned, int64(len(payload))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted source object survived publication recovery: %v", err)
	}
	if publications, err := objects.ListExpiredPublications(time.Now().UTC()); err != nil || len(publications) != 0 {
		t.Fatalf("recovered publication journal = %#v, %v", publications, err)
	}
}

func TestExpiredBrunoPublicationJournalFailsClosedWithoutReferenceChecker(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("unknown-reference-source")
	planned, err := objects.PlanBytes(brunoImportTestProject, "raw", payload, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.SavePublication(importartifact.PublicationDescriptor{
		ID:        "unknown-reference-publication",
		ProjectID: brunoImportTestProject,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Objects:   []importartifact.OpenRequest{planned},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.PutBytes(
		context.Background(), brunoImportTestProject, "raw", payload, int64(len(payload)),
	); err != nil {
		t.Fatal(err)
	}
	server.store = storeOverlay(&brunoStoreWithoutArtifactReferenceChecker{Store: store})
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), planned, int64(len(payload))); err != nil {
		t.Fatalf("unknown reference state deleted source: %v", err)
	}
	publications, err := objects.ListExpiredPublications(time.Now().UTC())
	if err != nil || len(publications) != 1 || publications[0].ID != "unknown-reference-publication" {
		t.Fatalf("fail-closed publication journal = %#v, %v", publications, err)
	}
}

func TestBrunoSupportMatrixAdvertisesNativeRuntime(t *testing.T) {
	server := NewServer(ServerConfig{})
	response, support := requestBrunoSupportMatrix(t, server)
	if support.SecretStore.SourceSecretMigrationPolicyEnabled {
		t.Fatal("support matrix enabled source migration by default")
	}
	if !strings.Contains(response.Body.String(), "native_http") ||
		strings.Contains(response.Body.String(), "WebSocket") {
		t.Fatalf("support matrix = %s", response.Body.String())
	}

	enableSourceSecretMigration(t, server)
	_, support = requestBrunoSupportMatrix(t, server)
	if !support.SecretStore.SourceSecretMigrationPolicyEnabled {
		t.Fatal("support matrix did not advertise the enabled source migration policy")
	}
}

type brunoMigrationRaceStore struct {
	mu              sync.Mutex
	firstName       string
	conflictingName string
	gets            map[string]int
	puts            int
}

type blockingCreateSecretStore struct {
	delegate secretstore.Store
	entered  chan string
	release  chan struct{}

	mu    sync.Mutex
	calls []string
}

func (s *blockingCreateSecretStore) Get(ctx context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	return s.delegate.Get(ctx, projectID, name)
}

func (s *blockingCreateSecretStore) Create(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	s.mu.Lock()
	s.calls = append(s.calls, name)
	s.mu.Unlock()
	select {
	case s.entered <- name:
	case <-ctx.Done():
		return secretstore.Metadata{}, ctx.Err()
	}
	select {
	case <-s.release:
		return s.delegate.Create(ctx, projectID, name, value, actor)
	case <-ctx.Done():
		return secretstore.Metadata{}, ctx.Err()
	}
}

func (s *blockingCreateSecretStore) Replace(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	return s.delegate.Replace(ctx, projectID, name, value, actor)
}

func (s *blockingCreateSecretStore) List(ctx context.Context, projectID string) ([]secretstore.Metadata, error) {
	return s.delegate.List(ctx, projectID)
}

func (s *blockingCreateSecretStore) Writable() bool   { return s.delegate.Writable() }
func (s *blockingCreateSecretStore) Provider() string { return "blocking-" + s.delegate.Provider() }

func (s *blockingCreateSecretStore) RollbackCreated(ctx context.Context, projectID, name, actor string) (bool, error) {
	rollback, ok := s.delegate.(secretstore.CreatedValueRollbackStore)
	if !ok {
		return false, secretstore.ErrRollbackUnsupported
	}
	return rollback.RollbackCreated(ctx, projectID, name, actor)
}

func (s *blockingCreateSecretStore) CreatedValueRollbackSupported() bool {
	return secretstore.SupportsCreatedValueRollback(s.delegate)
}

func (s *blockingCreateSecretStore) createNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

type failingBrunoCommitStore struct {
	storage.Store
	delegate  storage.ImportSourceStore
	err       error
	lookupErr error
}

func (s *failingBrunoCommitStore) CommitBrunoImport(*storage.BrunoImportCommit) (*storage.BrunoImportCommitResult, error) {
	return nil, s.err
}

func (s *failingBrunoCommitStore) GetImportSourceArtifact(id string, projectID string) (*storage.ImportSourceArtifact, error) {
	return s.delegate.GetImportSourceArtifact(id, projectID)
}

func (s *failingBrunoCommitStore) GetImportSourceRevision(id string, projectID string) (*storage.ImportSourceRevision, error) {
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return s.delegate.GetImportSourceRevision(id, projectID)
}

func (s *failingBrunoCommitStore) GetActiveImportSourceRevision(scriptID string, projectID string) (*storage.ImportSourceRevision, error) {
	return s.delegate.GetActiveImportSourceRevision(scriptID, projectID)
}

func (s *failingBrunoCommitStore) ListImportSourceRevisions(scriptID string, projectID string) ([]*storage.ImportSourceRevision, error) {
	return s.delegate.ListImportSourceRevisions(scriptID, projectID)
}

func (s *failingBrunoCommitStore) ActivateImportSourceRevision(scriptID string, projectID string, revisionID string, expectedActiveRevisionID string) (*storage.ImportSourceRevision, error) {
	return s.delegate.ActivateImportSourceRevision(scriptID, projectID, revisionID, expectedActiveRevisionID)
}

type flakyBrunoArtifactReferenceStore struct {
	storage.Store
	fail bool
}

type brunoStoreWithoutArtifactReferenceChecker struct {
	Store storage.Store
}

func (s *flakyBrunoArtifactReferenceStore) IsBrunoArtifactBackendKeyReferenced(backendKey string) (bool, error) {
	if s.fail {
		return false, errors.New("temporary reference check failure")
	}
	checker, ok := s.Store.(storage.BrunoArtifactReferenceChecker)
	if !ok {
		return false, nil
	}
	return checker.IsBrunoArtifactBackendKeyReferenced(backendKey)
}

func (s *brunoMigrationRaceStore) Get(_ context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gets == nil {
		s.gets = map[string]int{}
	}
	s.gets[name]++
	if name == s.conflictingName && s.gets[name] >= 2 {
		return "existing-different-value", secretstore.Metadata{
			ProjectID: projectID,
			Name:      name,
			Provider:  "race-test",
			Writable:  true,
		}, nil
	}
	return "", secretstore.Metadata{}, secretstore.ErrNotFound
}

func (s *brunoMigrationRaceStore) Create(_ context.Context, projectID, name, _ string, _ string) (secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	return secretstore.Metadata{ProjectID: projectID, Name: name, Provider: "race-test", Writable: true}, nil
}

func (*brunoMigrationRaceStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrNotFound
}

func (*brunoMigrationRaceStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return nil, nil
}

func (*brunoMigrationRaceStore) RollbackCreated(context.Context, string, string, string) (bool, error) {
	return true, nil
}

func (*brunoMigrationRaceStore) CreatedValueRollbackSupported() bool { return true }

func (*brunoMigrationRaceStore) Writable() bool   { return true }
func (*brunoMigrationRaceStore) Provider() string { return "race-test" }

func newBrunoImportAPITestServer(t *testing.T) (*Server, *storage.SQLiteStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(root, "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: brunoImportTestProject, Name: "Bruno API", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5a}, 32),
		KeyID: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	importAssetDir := filepath.Join(root, "import-assets")
	artifactDir := filepath.Join(importAssetDir, ".bruno-artifacts")
	server := NewServer(ServerConfig{
		Store:                store,
		MaxConcurrent:        2,
		ImportAssetDir:       importAssetDir,
		BrunoArtifactWrapper: wrapper,
	})
	return server, store, artifactDir
}

func enableSourceSecretMigration(t *testing.T, server *Server) {
	t.Helper()
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source secret migration: %v", err)
	}
}

func requestBrunoSupportMatrix(t *testing.T, server *Server) (*httptest.ResponseRecorder, struct {
	SecretStore struct {
		SourceSecretMigrationPolicyEnabled bool `json:"source_secret_migration_policy_enabled"`
	} `json:"secret_store"`
}) {
	t.Helper()
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/bruno/support-matrix", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var support struct {
		SecretStore struct {
			SourceSecretMigrationPolicyEnabled bool `json:"source_secret_migration_policy_enabled"`
		} `json:"secret_store"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &support); err != nil {
		t.Fatal(err)
	}
	return response, support
}

func inspectBrunoJSON(t *testing.T, server *Server, source []byte, syncScriptID string) brunoInspectResponse {
	t.Helper()
	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID:    brunoImportTestProject,
		Filename:     "login.yml",
		Content:      string(source),
		SyncScriptID: syncScriptID,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	return inspected
}

func TestBrunoInspectReturnsRetryable429WhenInspectionSlotIsBusy(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	brunoInspectConcurrency <- struct{}{}
	defer func() { <-brunoInspectConcurrency }()

	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: brunoImportTestProject,
		Filename:  "request.yml",
		Content:   "info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("429 response is missing Retry-After")
	}
}

func TestBrunoInspectRejectsQueryBodyProjectMismatch(t *testing.T) {
	server, _, artifactDir := newBrunoImportAPITestServer(t)
	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: "project-b",
		Filename:  "request.yml",
		Content:   "info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect?project_id="+url.QueryEscape(brunoImportTestProject), bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "does not match") {
		t.Fatalf("mismatched inspect status=%d body=%s", response.Code, response.Body.String())
	}
	assertDirectoryHasNoRegularFiles(t, artifactDir)
}

func commitBrunoImport(t *testing.T, server *Server, request brunoCommitRequest) *httptest.ResponseRecorder {
	t.Helper()
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	return response
}

func commitBrunoImportWithStatus(t *testing.T, server *Server, request brunoCommitRequest) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(payload))
	httpRequest.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httpRequest)
	return response
}

func commitBrunoImportRawWithStatus(t *testing.T, server *Server, payload any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(encoded))
	httpRequest.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httpRequest)
	return response
}

func decodeCommittedBrunoScript(t *testing.T, payload []byte) *storageScriptAlias {
	t.Helper()
	var response struct {
		Script *storageScriptAlias `json:"script"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Script == nil {
		t.Fatalf("commit response has no script: %s", payload)
	}
	return response.Script
}

func readCommittedBrunoRuntimeSource(
	t *testing.T,
	server *Server,
	script *dsl.Script,
) []byte {
	t.Helper()
	if server == nil || script == nil {
		t.Fatal("server and committed Bruno script are required")
	}
	if script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved[dsl.ImportPreservedRuntimeArtifactKey] != "raw" {
		t.Fatalf("committed Bruno runtime marker = %#v, want raw", script.Metadata)
	}
	artifactID := strings.TrimPrefix(script.Config.Bruno.BundleRef, "artifact://")
	brunoStore, ok := server.store.(storage.ImportSourceStore)
	if !ok {
		t.Fatal("test store does not implement Bruno import storage")
	}
	artifact, err := brunoStore.GetImportSourceArtifact(
		artifactID,
		normalizeProjectID(script.ProjectID),
	)
	if err != nil {
		t.Fatalf("load committed Bruno runtime artifact: %v", err)
	}
	reader, _, err := server.brunoArtifacts.Open(
		context.Background(),
		importartifact.OpenRequest{
			BackendKey: artifact.BackendKey,
			ProjectID:  artifact.ProjectID,
			Kind:       artifact.ArtifactKind,
			SHA256:     artifact.SHA256,
			SizeBytes:  artifact.SizeBytes,
		},
	)
	if err != nil {
		t.Fatalf("open committed Bruno runtime artifact: %v", err)
	}
	payload, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read committed Bruno runtime artifact: read=%v close=%v", readErr, closeErr)
	}
	return payload
}

type storageScriptAlias = struct {
	ID        string `json:"id"`
	UpdatedAt time.Time
	Config    struct {
		Bruno struct {
			SourceRevision string            `json:"source_revision"`
			SecretBindings map[string]string `json:"secret_bindings"`
		} `json:"bruno"`
	} `json:"config"`
}

func requestBrunoSource(t *testing.T, server *Server, scriptID string, raw bool) []byte {
	t.Helper()
	target := "/api/v1/scripts/" + url.PathEscape(scriptID) + "/bruno-imports/source"
	if raw {
		target += "?raw=true"
	}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("source status = %d body=%s", response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}

func writeBrunoMultipartFile(t *testing.T, writer *multipart.Writer, relative string, content []byte) {
	t.Helper()
	part, err := writer.CreateFormFile("file:"+url.QueryEscape(relative), filepath.Base(relative))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
}

func tarContains(t *testing.T, payload []byte, name string) bool {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(payload))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == name {
			return true
		}
	}
}

func assertDirectoryDoesNotContain(t *testing.T, root, value string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(payload, []byte(value)) {
			t.Fatalf("encrypted artifact file %q contains plaintext %q", path, value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryHasNoRegularFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(filepath.ToSlash(path), "/.project-tombstones/") {
			return nil
		}
		if info.Mode().IsRegular() {
			t.Errorf("unexpected retained file %s", path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
