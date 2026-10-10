package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/storage"
)

// protectedDraftHeldByAnotherRequest returns a protected-store server, the
// browser-visible draft of a credential-bearing import, and the claim that a
// concurrent request holds on it. Release the claim to let the draft be used.
func protectedDraftHeldByAnotherRequest(t *testing.T) (*Server, []any, *importsession.Manager, *importsession.ClaimedSession) {
	t.Helper()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5b}, 32), KeyID: "busy-draft"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	script := &dsl.Script{
		Name: "Busy protected draft", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer busy-draft-secret"}},
		}},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
	preview := server.newScriptImportResponseWithSourceSecrets(
		request,
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	token := importDraftAttestationToken(script)
	if !strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		t.Fatalf("draft capability %q is not a protected session", token)
	}
	manager, err := server.protectedImportSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	_, actor, _ := server.resolveAuditActor(request)
	held, err := manager.Claim(request.Context(), importsession.Scope{
		ID:        strings.TrimPrefix(token, importDraftProtectedTokenPrefix),
		Profile:   "loadrunner",
		ProjectID: storage.DefaultProjectID,
		Actor:     actor,
	}, importCandidateSessionMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	clear(held.Payload)
	return server, masked, manager, held
}

func assertImportDraftBusyResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusConflict {
		t.Fatalf("busy status = %d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "protected_import_session_conflict" || body["retryable"] != true ||
		strings.Contains(fmt.Sprint(body["error"]), "invalid or expired") {
		t.Fatalf("busy response = %#v, want a retryable conflict rather than a dead draft", body)
	}
}

func TestWriteImportDraftBusyClassifiesOnlyHeldDrafts(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		busy bool
	}{
		{"attestation busy", errImportDraftAttestationBusy, true},
		{"protected session claimed", fmt.Errorf("claim: %w", importsession.ErrSessionClaimed), true},
		{"invalid draft", errImportDraftAttestationInvalid, false},
		{"invalid candidate with unrelated release failure", errors.Join(
			errors.New("protected import candidate is invalid"),
			fmt.Errorf("release protected import candidate claim: %v", importsession.ErrSessionClaimed),
		), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			if got := writeImportDraftBusy(response, tc.err); got != tc.busy {
				t.Fatalf("writeImportDraftBusy = %v, want %v", got, tc.busy)
			}
			if tc.busy {
				assertImportDraftBusyResponse(t, response)
			} else if response.Body.Len() != 0 {
				t.Fatalf("non-busy error wrote a response: %s", response.Body.String())
			}
		})
	}
}

func TestSecretBindingApplyReportsBusyProtectedDraftAsRetryable(t *testing.T) {
	server, masked, manager, held := protectedDraftHeldByAnotherRequest(t)
	post := func() *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]any{"script": masked[0], "bindings": []any{}})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/secret-bindings/apply", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}
	assertImportDraftBusyResponse(t, post())
	if err := manager.Release(held.Claim); err != nil {
		t.Fatal(err)
	}
	if retry := post(); strings.Contains(retry.Body.String(), "protected_import_session_conflict") {
		t.Fatalf("released draft still reported busy: %d %s", retry.Code, retry.Body.String())
	}
}

// failingReleaseStore reports every claim release as owned by someone else.
type failingReleaseStore struct {
	*importartifact.EncryptedStore
}

func (s *failingReleaseStore) ReleaseSessionClaim(id, projectID, actor, token string) error {
	_ = s.EncryptedStore.ReleaseSessionClaim(id, projectID, actor, token)
	return importsession.ErrSessionClaimed
}

func TestClaimProtectedCandidateReleaseFailureIsNotReportedAsBusy(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5c}, 32), KeyID: "release-failure"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := importsession.NewManager(&failingReleaseStore{EncryptedStore: objects})
	if err != nil {
		t.Fatal(err)
	}
	server.importSessions = manager
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	_, actor, _ := server.resolveAuditActor(request)
	id := "6f1f8f3e-3d55-4c43-9a6f-5ad0f4a3b1d2"
	if err := manager.Save(request.Context(), importsession.SessionDescriptor{
		ID: id, Profile: enterpriseArchiveSaveProfile, ProjectID: "p", Actor: actor, ExpiresAt: importCandidateNoExpiration,
	}, []byte(`{"version":"not-a-candidate"}`), importCandidateSessionMaxBytes); err != nil {
		t.Fatal(err)
	}
	_, _, err = server.claimProtectedImportCandidateSession(request, enterpriseArchiveSaveProfile, id, "p")
	if err == nil {
		t.Fatal("corrupt candidate was accepted")
	}
	if errors.Is(err, importsession.ErrSessionClaimed) {
		t.Fatalf("release failure leaked into candidate classification: %v", err)
	}
	if !strings.Contains(err.Error(), "release protected import candidate claim") {
		t.Fatalf("release failure was not reported: %v", err)
	}
}

func TestOpenProtectedCandidateDoesNotCopyRawSource(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5d}, 32), KeyID: "raw-source"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	draft := &dsl.Script{Name: "raw source", Version: "1.0", Protocol: "http", ProjectID: "p", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}}
	ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	source := []byte("synthetic uploaded archive bytes")
	id, _, err := server.registerImportCandidateSessionWithRequirements(request, enterpriseArchiveSaveProfile, draft, nil, importCandidateSource{Bytes: source, Format: "zip"})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := server.openProtectedImportCandidateSession(request, enterpriseArchiveSaveProfile, id, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer clearProtectedImportCandidate(opened)
	if opened.RawSource != nil || opened.Script == nil {
		t.Fatalf("read-only open copied %d raw source bytes", len(opened.RawSource))
	}
	claimed, claim, err := server.claimProtectedImportCandidateSession(request, enterpriseArchiveSaveProfile, id, "p")
	if err != nil {
		t.Fatal(err)
	}
	defer server.releaseImportCandidateClaim(claim)
	defer clearProtectedImportCandidate(claimed)
	if !bytes.Equal(claimed.RawSource, source) {
		t.Fatal("claiming consumer lost the retained raw source")
	}
}

func TestJMeterBatchRepairPreviewDoesNotBlockConcurrentBatchConsumer(t *testing.T) {
	server, original, _ := batchTestPreview(t)
	id := decodeBatchIdentity(t, original["batch"])
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	spy := &consumeDuringReadStore{EncryptedStore: objects, sessionID: id.Revision}
	if server.importSessions, err = importsession.NewManager(spy); err != nil {
		t.Fatal(err)
	}
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	response := httptest.NewRecorder()
	server.processImportRepair(response, httptest.NewRequest(http.MethodPost, "/", nil), false, req)
	if response.Code != http.StatusOK {
		t.Fatalf("preview %d: %s", response.Code, response.Body.String())
	}
	if !spy.attempted {
		t.Fatal("preview never read the batch")
	}
	if spy.consumed != nil {
		t.Fatalf("a read-only batch preview blocked the batch consumer: %v", spy.consumed)
	}
}

func TestInMemoryDraftClaimReportsBusyUntilRetainedAfterCommit(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		Name: "In-memory busy draft", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer in-memory-busy-secret"}},
		}},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
	preview := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}})
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	token := importDraftAttestationToken(script)
	if token == "" || strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		t.Fatalf("draft capability %q is not an in-memory attestation", token)
	}
	held, err := server.claimImportDraftAttestation(request, script)
	if err != nil {
		t.Fatal(err)
	}
	post := func(operationID string) *httptest.ResponseRecorder {
		return postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": operationID, "scripts": masked})
	}
	assertImportDraftBusyResponse(t, post("in-memory-busy"))

	// The holder committed its assets but could not finish cleanup: the
	// capability stays claimed, and is no longer reported as in flight.
	server.retainCommittedImportDraftAttestation(held)
	retained := post("in-memory-retained")
	if retained.Code != http.StatusConflict || !strings.Contains(retained.Body.String(), enterpriseArchiveDraftInvalidCode) {
		t.Fatalf("retained capability = %d %s, want the invalid-draft conflict", retained.Code, retained.Body.String())
	}
}

// ageProtectedDraftClaim makes a held protected claim look abandoned, as it is
// once its holder stops renewing the lease.
func ageProtectedDraftClaim(t *testing.T, server *Server, sessionID string) {
	t.Helper()
	stale := time.Now().UTC().Add(-2 * time.Minute)
	found := false
	err := filepath.WalkDir(server.brunoArtifactDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != sessionID+".claim.json" {
			return err
		}
		found = true
		return os.Chtimes(path, stale, stale)
	})
	if err != nil || !found {
		t.Fatalf("age claim %s: found=%v err=%v", sessionID, found, err)
	}
}

func TestRetainedCommittedProtectedDraftIsNotSavedAgainAfterItsLease(t *testing.T) {
	server, masked, _, held := protectedDraftHeldByAnotherRequest(t)
	// The holder committed its assets but could not finish cleanup, so it
	// retains the claim and stops renewing it.
	server.retainCommittedImportDraftAttestation(&importDraftAttestationClaim{ProtectedClaim: &held.Claim})
	ageProtectedDraftClaim(t, server, held.Claim.ID)

	response := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "after-retained-commit", "scripts": masked})
	if response.Code == http.StatusCreated {
		t.Fatalf("a committed draft was saved a second time: %s", response.Body.String())
	}
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), enterpriseArchiveDraftInvalidCode) {
		t.Fatalf("status = %d body=%s, want the invalid-draft conflict", response.Code, response.Body.String())
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("committed draft produced another script: %#v", scripts)
	}
}

// cleanupFailingStore retires a completed claim, then fails its cleanup, as a
// filesystem error after the consumed mark would.
type cleanupFailingStore struct {
	*importartifact.EncryptedStore
}

func (s *cleanupFailingStore) CompleteSessionClaim(_ context.Context, id, projectID, actor, claimToken string) error {
	if err := s.EncryptedStore.MarkSessionClaimConsumed(id, projectID, actor, claimToken); err != nil {
		return err
	}
	return errors.New("simulated session cleanup failure")
}

func TestImportDraftMutationKeepsReplacementWhenRetiredDraftCleanupFails(t *testing.T) {
	server, masked, manager, held := protectedDraftHeldByAnotherRequest(t)
	if err := manager.Release(held.Claim); err != nil {
		t.Fatal(err)
	}
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	if server.importSessions, err = importsession.NewManager(&cleanupFailingStore{EncryptedStore: objects}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(masked[0])
	if err != nil {
		t.Fatal(err)
	}
	var draft dsl.Script
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/secret-bindings/apply", nil)
	claim, err := server.claimImportDraftMutation(request, &draft)
	if err != nil || claim == nil {
		t.Fatalf("claim draft for mutation: %v", err)
	}
	replacement := cloneScriptDefinition(claim.Script)
	if _, err := server.completeImportDraftMutation(request, claim, replacement); err != nil {
		t.Fatalf("a secured replacement was discarded after the old draft was retired: %v", err)
	}
	replacementToken := importDraftAttestationToken(replacement)
	if replacementToken == "" || replacementToken == importDraftAttestationToken(&draft) {
		t.Fatalf("replacement capability = %q", replacementToken)
	}
	// The retired draft is gone for good; the replacement is the usable draft.
	if _, err := server.claimImportDraftAttestation(request, &draft); !errors.Is(err, errImportDraftAttestationInvalid) {
		t.Fatalf("retired draft claim = %v, want invalid", err)
	}
	next, err := server.claimImportDraftAttestation(request, replacement)
	if err != nil {
		t.Fatalf("replacement draft is not usable: %v", err)
	}
	server.releaseImportDraftAttestation(next)
}
