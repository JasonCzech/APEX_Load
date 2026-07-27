package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
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

type brunoNativeBatchTestResponse struct {
	BatchID          string        `json:"batch_id"`
	Scripts          []*dsl.Script `json:"scripts"`
	Count            int           `json:"count"`
	Created          bool          `json:"created"`
	IdempotentReplay bool          `json:"idempotent_replay"`
}

func TestBrunoNativeBatchInspectCommitAndIdempotentRetry(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())

	if inspection.Result == nil || inspection.Result.OutputMode != brunoconvert.OutputModeNativeRequests {
		t.Fatalf("native inspection result = %#v", inspection.Result)
	}
	if inspection.Result.Script != nil {
		t.Fatalf("native inspection unexpectedly returned a canonical script: %#v", inspection.Result.Script)
	}
	if len(inspection.Result.Scripts) != 2 {
		t.Fatalf("native candidates = %#v, want two", inspection.Result.Scripts)
	}
	if got := []string{
		inspection.Result.Scripts[0].SourcePath,
		inspection.Result.Scripts[1].SourcePath,
	}; got[0] != "a/alpha.yml" || got[1] != "z/zeta.yml" {
		t.Fatalf("candidate order = %#v, want seq-ordered folder/request paths", got)
	}
	for _, candidate := range inspection.Result.Scripts {
		if candidate.CandidateID == "" || candidate.PlannedScriptID == "" ||
			candidate.Script == nil || candidate.Script.Protocol != "http" {
			t.Fatalf("candidate is incomplete: %#v", candidate)
		}
	}

	commitRequest := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
	}
	missingFolder := commitBrunoImportWithStatus(t, server, commitRequest)
	if missingFolder.Code != http.StatusBadRequest ||
		!strings.Contains(missingFolder.Body.String(), "folder_id is required") {
		t.Fatalf("missing folder status = %d body=%s", missingFolder.Code, missingFolder.Body.String())
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native")
	commitRequest.FolderID = &folderID
	createdResponse := commitBrunoImportWithStatus(t, server, commitRequest)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("native commit status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, createdResponse)
	if created.BatchID == "" || !created.Created || created.IdempotentReplay ||
		created.Count != 2 || len(created.Scripts) != 2 {
		t.Fatalf("created native batch = %#v", created)
	}
	for index, script := range created.Scripts {
		candidate := inspection.Result.Scripts[index]
		if script == nil || script.ID != candidate.PlannedScriptID ||
			script.Name != candidate.Script.Name || script.Protocol != "http" {
			t.Fatalf("created script[%d] = %#v, candidate = %#v", index, script, candidate)
		}
		revisions, err := store.ListBrunoSourceRevisions(script.ID, brunoImportTestProject)
		if err != nil {
			t.Fatalf("ListBrunoSourceRevisions(%q): %v", script.ID, err)
		}
		if len(revisions) != 0 {
			t.Fatalf("native script %q has Bruno source revisions: %#v", script.ID, revisions)
		}
	}
	persisted, err := store.ListScripts(brunoImportTestProject, nil)
	if err != nil {
		t.Fatalf("ListScripts: %v", err)
	}
	if len(persisted) != 2 {
		t.Fatalf("persisted scripts = %#v, want two", persisted)
	}

	replayResponse := commitBrunoImportWithStatus(t, server, commitRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("idempotent retry status = %d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	replayed := decodeBrunoNativeBatchTestResponse(t, replayResponse)
	if replayed.BatchID != created.BatchID || replayed.Created || !replayed.IdempotentReplay ||
		replayed.Count != created.Count || len(replayed.Scripts) != len(created.Scripts) {
		t.Fatalf("idempotent retry = %#v, original = %#v", replayed, created)
	}
	for index := range replayed.Scripts {
		if replayed.Scripts[index].ID != created.Scripts[index].ID {
			t.Fatalf("idempotent script ids = %q/%q", replayed.Scripts[index].ID, created.Scripts[index].ID)
		}
	}

	mismatch := commitRequest
	mismatch.NameOverrides = map[string]string{
		inspection.Result.Scripts[0].CandidateID: "Changed after commit",
	}
	conflict := commitBrunoImportWithStatus(t, server, mismatch)
	if conflict.Code != http.StatusConflict ||
		!strings.Contains(conflict.Body.String(), storage.ErrBrunoNativeBatchConflict.Error()) {
		t.Fatalf("mismatched retry status = %d body=%s", conflict.Code, conflict.Body.String())
	}
	foreignActor := commitBrunoNativeAsActor(t, server, mismatch, "other-native-import-actor")
	if foreignActor.Code != http.StatusForbidden ||
		!strings.Contains(foreignActor.Body.String(), "belongs to another actor") {
		t.Fatalf("foreign-actor retry status = %d body=%s", foreignActor.Code, foreignActor.Body.String())
	}
}

func TestBrunoNativeSessionRequiresNativeCommitMode(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
	})
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "bruno_native_commit_mode_required") {
		t.Fatalf("missing native mode status = %d body=%s", response.Code, response.Body.String())
	}

	// A client can correct the request because validation does not consume the
	// inspection session.
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-mode-required")
	response = commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("corrected native commit status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBrunoNativeBatchRetryOmittingCandidateIDsConflictsWithCommittedSubset(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-subset")
	selectedID := inspection.Result.Scripts[0].CandidateID
	created := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		CandidateIDs:    []string{selectedID},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("subset commit status = %d body=%s", created.Code, created.Body.String())
	}
	conflict := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if conflict.Code != http.StatusConflict ||
		!strings.Contains(conflict.Body.String(), storage.ErrBrunoNativeBatchConflict.Error()) {
		t.Fatalf("omitted retry status = %d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestBrunoNativeBatchIgnoresUnusedMigrationOptIn(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-no-secrets")
	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		MigrateSecrets:  &migrate,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("unused migration opt-in status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBrunoNativeBatchMigrationOptInIgnoresRequirementWithoutSourceValue(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yaml",
			Content: "opencollection: 1.0.0\ninfo: {name: Existing Secret, uid: existing-secret}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Existing Secret Request, uid: existing-secret-request, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - name: Authorization
      value: 'Bearer {{secret.api-token}}'
settings: {followRedirects: true, maxRedirects: 5, timeout: 1000}
`,
		},
	})
	if len(inspection.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspection.Result.SecretRequirements)
	}
	requirement := inspection.Result.SecretRequirements[0]
	if requirement.Value != "" || requirement.SourceValueAvailable || !requirement.ExistingReference {
		t.Fatalf("non-migratable secret requirement = %#v", requirement)
	}

	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-existing-secret")
	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		MigrateSecrets:  &migrate,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("non-migratable requirement status = %d body=%s", response.Code, response.Body.String())
	}
	if _, _, err := server.projectSecretStore.Get(
		context.Background(), brunoImportTestProject, requirement.SuggestedName,
	); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("non-migratable requirement unexpectedly created a secret: %v", err)
	}
}

func TestBrunoNativeBatchKeepsSourceArtifactsWithoutReferenceChecker(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	session, err := server.loadBrunoImportSession(
		httptest.NewRequest(http.MethodGet, "/", nil),
		inspection.ImportSessionID,
		brunoImportTestProject,
	)
	if err != nil {
		t.Fatalf("load inspection session: %v", err)
	}
	rawArtifact := session.RawArtifact
	sanitizedArtifact := session.SanitizedArtifact
	server.store = &brunoNativeStoreWithoutArtifactChecker{
		Store:    store,
		delegate: store,
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-artifacts")
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("native commit status = %d body=%s", response.Code, response.Body.String())
	}
	for _, artifact := range []storage.BrunoSourceArtifact{rawArtifact, sanitizedArtifact} {
		reader, _, err := server.brunoArtifacts.Open(context.Background(), importartifact.OpenRequest{
			BackendKey: artifact.BackendKey,
			ProjectID:  artifact.ProjectID,
			Kind:       artifact.ArtifactKind,
			SHA256:     artifact.SHA256,
			SizeBytes:  artifact.SizeBytes,
		})
		if err != nil {
			t.Fatalf("open committed %s artifact: %v", artifact.ArtifactKind, err)
		}
		_ = reader.Close()
	}
}

func TestBrunoNativeCleanupRetainsArtifactsReferencedByActiveInspection(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	session, err := server.loadBrunoImportSession(
		httptest.NewRequest(http.MethodGet, "/", nil),
		inspection.ImportSessionID,
		brunoImportTestProject,
	)
	if err != nil {
		t.Fatalf("load inspection session: %v", err)
	}
	artifacts := []storage.BrunoSourceArtifact{session.RawArtifact, session.SanitizedArtifact}

	server.cleanupUnreferencedBrunoArtifacts(artifacts)

	for _, artifact := range artifacts {
		reader, _, err := server.brunoArtifacts.Open(context.Background(), importartifact.OpenRequest{
			BackendKey: artifact.BackendKey,
			ProjectID:  artifact.ProjectID,
			Kind:       artifact.ArtifactKind,
			SHA256:     artifact.SHA256,
			SizeBytes:  artifact.SizeBytes,
		})
		if err != nil {
			t.Fatalf("active inspection %s artifact was deleted: %v", artifact.ArtifactKind, err)
		}
		_ = reader.Close()
	}
}

func TestBrunoNativeReceiptRetryCleansClaimAfterPostCommitInterruption(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	session, err := server.loadBrunoImportSession(
		httptest.NewRequest(http.MethodGet, "/", nil),
		inspection.ImportSessionID,
		brunoImportTestProject,
	)
	if err != nil {
		t.Fatalf("load inspection session: %v", err)
	}
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	reader, sessionDescriptor, err := objects.OpenSession(
		context.Background(), inspection.ImportSessionID, brunoImportTestProject, session.Actor,
	)
	if err != nil {
		t.Fatalf("open persisted inspection session: %v", err)
	}
	_ = reader.Close()

	blocking := &blockingAfterBrunoNativeCommitStore{
		Store:     store,
		delegate:  store,
		committed: make(chan struct{}),
		release:   make(chan struct{}),
	}
	server.store = blocking
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-crash")
	commitRequest := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	}
	payload, err := json.Marshal(commitRequest)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/scripts/import/bruno/commit",
			bytes.NewReader(payload),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		firstDone <- response
	}()

	select {
	case <-blocking.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("native database commit did not reach interruption point")
	}
	retry := commitBrunoImportWithStatus(t, server, commitRequest)
	if retry.Code != http.StatusOK {
		t.Fatalf("receipt retry status = %d body=%s", retry.Code, retry.Body.String())
	}
	replayed := decodeBrunoNativeBatchTestResponse(t, retry)
	if !replayed.IdempotentReplay || replayed.Created || replayed.Count != 2 {
		t.Fatalf("receipt retry = %#v", replayed)
	}
	if _, _, err := objects.OpenSession(
		context.Background(), inspection.ImportSessionID, brunoImportTestProject, session.Actor,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed session descriptor survived receipt cleanup: %v", err)
	}
	if _, _, err := objects.Open(context.Background(), importartifact.OpenRequest{
		BackendKey: sessionDescriptor.BackendKey,
		ProjectID:  sessionDescriptor.ProjectID,
		Kind:       "session",
		SHA256:     sessionDescriptor.SHA256,
		SizeBytes:  sessionDescriptor.SizeBytes,
	}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential-bearing session payload survived receipt cleanup: %v", err)
	}

	close(blocking.release)
	select {
	case first := <-firstDone:
		if first.Code != http.StatusCreated {
			t.Fatalf("interrupted commit response = %d body=%s", first.Code, first.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted commit did not finish after release")
	}
}

func TestBrunoNativeBatchRejectsCrossProjectFolder(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	now := time.Now().UTC()
	const foreignProjectID = "project-bruno-foreign"
	const foreignFolderID = "folder-bruno-foreign"
	if err := store.SaveProject(&storage.Project{
		ID: foreignProjectID, Name: "Foreign", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveFolder(&storage.Folder{
		ID: foreignFolderID, ProjectID: foreignProjectID, Name: "Foreign APIs",
		Category: "general", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())

	folderID := foreignFolderID
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "belongs to another project") {
		t.Fatalf("cross-project folder status = %d body=%s", response.Code, response.Body.String())
	}
	const generalFolderID = "folder-bruno-general"
	if err := store.SaveFolder(&storage.Folder{
		ID: generalFolderID, ProjectID: brunoImportTestProject, Name: "Not scripts",
		Category: "general", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}
	general := generalFolderID
	response = commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &general,
	})
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "must be a script folder") {
		t.Fatalf("general-folder status = %d body=%s", response.Code, response.Body.String())
	}
	persisted, err := store.ListScripts(brunoImportTestProject, nil)
	if err != nil {
		t.Fatalf("ListScripts: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("cross-project rejection persisted scripts: %#v", persisted)
	}
}

func TestBrunoNativeBatchValidatesSessionBeforeDestinationFolder(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	now := time.Now().UTC()
	const (
		foreignProjectID  = "project-bruno-session-oracle-foreign"
		foreignFolderID   = "folder-bruno-session-oracle-foreign"
		missingFolderID   = "folder-bruno-session-oracle-missing"
		fabricatedSession = "bruno-native-session-does-not-exist"
	)
	if err := store.SaveProject(&storage.Project{
		ID: foreignProjectID, Name: "Foreign", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveFolder(&storage.Folder{
		ID: foreignFolderID, ProjectID: foreignProjectID, Name: "Foreign APIs",
		Category: "scripts", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}

	responses := make(map[string]*httptest.ResponseRecorder, 2)
	for name, destinationID := range map[string]string{
		"foreign":     foreignFolderID,
		"nonexistent": missingFolderID,
	} {
		folderID := destinationID
		responses[name] = commitBrunoImportWithStatus(t, server, brunoCommitRequest{
			ProjectID:       brunoImportTestProject,
			ImportSessionID: fabricatedSession,
			Mode:            "create_native_batch",
			FolderID:        &folderID,
		})
	}
	for name, response := range responses {
		if response.Code != http.StatusNotFound ||
			!strings.Contains(response.Body.String(), `"code":"bruno_import_session_consumed"`) {
			t.Fatalf("%s destination status = %d body=%s", name, response.Code, response.Body.String())
		}
	}
	if foreign, missing := responses["foreign"].Body.String(), responses["nonexistent"].Body.String(); foreign != missing {
		t.Fatalf("destination lookup disclosed folder existence: foreign=%s nonexistent=%s", foreign, missing)
	}
}

func TestBrunoNativeInspectionMasksCandidateTargetProjection(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Masked target}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Masked target, uid: masked-target, type: http}
http:
  method: GET
  url: https://example.test/resource?password=source-target-password
`,
		},
	})
	if len(inspection.Result.Scripts) != 1 {
		t.Fatalf("native candidates = %d, want 1", len(inspection.Result.Scripts))
	}
	candidate := inspection.Result.Scripts[0]
	if strings.Contains(candidate.Target, "source-target-password") ||
		strings.Contains(candidate.Script.Actions[0].Target, "source-target-password") {
		t.Fatalf("candidate target was not masked: top-level=%q action=%q", candidate.Target, candidate.Script.Actions[0].Target)
	}
}

func TestBrunoNativeCommitUsesProtectedCandidateInsteadOfMaskedReviewCopy(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Protected candidate}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Protected candidate, uid: protected-candidate, type: http}
variables:
  - {name: password, value: source-password-value}
http:
  method: POST
  url: https://example.test/resource
  body:
    type: xml
    data: "<password>{{password}}</password>"
`,
		},
	})
	candidate := inspection.Result.Scripts[0]
	if strings.Contains(candidate.Script.Actions[0].Body, "source-password-value") {
		t.Fatalf("public native candidate disclosed its source credential: %q", candidate.Script.Actions[0].Body)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-protected-candidate")
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("native commit status = %d body=%s", response.Code, response.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, response)
	body := created.Scripts[0].Actions[0].Body
	if !strings.Contains(body, "{{secret.") || strings.Contains(body, "[REDACTED]") ||
		strings.Contains(body, "source-password-value") {
		t.Fatalf("committed body = %q, want executable governed secret reference", body)
	}
}

func TestBrunoNativeBatchCanonicalizesMapsAndRejectsIncompatiblePayloads(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-validation")
	candidateID := inspection.Result.Scripts[0].CandidateID
	base := map[string]any{
		"project_id":        brunoImportTestProject,
		"import_session_id": inspection.ImportSessionID,
		"mode":              "create_native_batch",
		"folder_id":         folderID,
	}

	cases := []struct {
		name    string
		mutate  func(map[string]any)
		message string
	}{
		{
			name: "explicit empty candidate selection",
			mutate: func(payload map[string]any) {
				payload["candidate_ids"] = []string{}
			},
			message: "candidate_ids must be omitted",
		},
		{
			name: "canonical-only field",
			mutate: func(payload map[string]any) {
				payload["script_id"] = "client-owned-id"
			},
			message: "does not accept fields: script_id",
		},
		{
			name: "normalized name key collision",
			mutate: func(payload map[string]any) {
				payload["name_overrides"] = map[string]string{
					candidateID:       "First",
					" " + candidateID: "Second",
				}
			},
			message: "normalize to the same value",
		},
		{
			name: "unknown secret binding",
			mutate: func(payload map[string]any) {
				payload["secret_bindings"] = map[string]string{"unknown-requirement": "PROJECT_SECRET"}
			},
			message: "stale, unknown, or not selected",
		},
		{
			name: "invalid secret binding name",
			mutate: func(payload map[string]any) {
				payload["secret_bindings"] = map[string]string{"unknown-requirement": "not a valid secret name"}
			},
			message: "must contain 1-249 letters",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := make(map[string]any, len(base)+1)
			for key, value := range base {
				payload[key] = value
			}
			testCase.mutate(payload)
			response := commitBrunoNativeRawWithStatus(t, server, payload)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), testCase.message) {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}
	stale := make(map[string]any, len(base)+1)
	for key, value := range base {
		stale[key] = value
	}
	stale["candidate_ids"] = []string{"stale-candidate-id"}
	staleResponse := commitBrunoNativeRawWithStatus(t, server, stale)
	if staleResponse.Code != http.StatusConflict ||
		!strings.Contains(staleResponse.Body.String(), "stale or unavailable") {
		t.Fatalf("stale candidate status = %d body=%s", staleResponse.Code, staleResponse.Body.String())
	}

	padded := make(map[string]any, len(base)+1)
	for key, value := range base {
		padded[key] = value
	}
	padded["name_overrides"] = map[string]string{" " + candidateID + " ": " Canonicalized Alpha "}
	createdResponse := commitBrunoNativeRawWithStatus(t, server, padded)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("canonicalized commit status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, createdResponse)
	if created.Scripts[0].Name != "Canonicalized Alpha" {
		t.Fatalf("canonicalized name = %q", created.Scripts[0].Name)
	}

	retry := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		NameOverrides:   map[string]string{candidateID: "Canonicalized Alpha"},
	}
	replay := commitBrunoImportWithStatus(t, server, retry)
	if replay.Code != http.StatusOK {
		t.Fatalf("canonical retry status = %d body=%s", replay.Code, replay.Body.String())
	}
}

func TestBrunoNativeBatchRejectsExpiredSession(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-expired")
	server.deleteBrunoImportSession(inspection.ImportSessionID)

	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusNotFound ||
		!strings.Contains(response.Body.String(), "claimed, consumed, or expired") {
		t.Fatalf("expired session status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBrunoNativeBatchRejectsExistingPlannedScriptIDBeforeClaim(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-collision")
	plannedID := inspection.Result.Scripts[0].PlannedScriptID
	if err := store.SaveScript(&dsl.Script{
		ID: plannedID, RefKey: "LOC-SCR-9001-v1", Name: "Existing", Protocol: "http",
		Version: "1", ProjectID: brunoImportTestProject, FolderID: &folderID,
		Actions: []dsl.Action{{Name: "Existing", Type: "http_request", Method: "GET", Target: "https://example.test"}},
	}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}

	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "already exists") {
		t.Fatalf("planned-id collision status = %d body=%s", response.Code, response.Body.String())
	}
	if _, err := server.loadBrunoImportSession(
		httptest.NewRequest(http.MethodGet, "/", nil),
		inspection.ImportSessionID,
		brunoImportTestProject,
	); err != nil {
		t.Fatalf("planned-id collision consumed session: %v", err)
	}
}

func TestBrunoNativeMissingVariableSavesDraftButBlocksLaunch(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Missing Variable API, uid: missing-variable-api}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Missing Variable, uid: missing-variable-request, type: http, seq: 1}
http: {method: GET, url: "https://example.test/{{tenant}}"}
`,
		},
	})
	if got := inspection.Result.Scripts[0].MissingVariables; len(got) != 1 || got[0] != "tenant" {
		t.Fatalf("missing variables = %#v", got)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-missing")
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("missing-variable draft status = %d body=%s", response.Code, response.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, response)
	action := created.Scripts[0].Actions[0]
	if action.Metadata["execution_blocker"] != "bruno_missing_variables" {
		t.Fatalf("saved execution blocker = %#v", action.Metadata)
	}
	if _, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Blocked native draft", Duration: "30s", ScriptID: created.Scripts[0].ID, VUsers: 1,
	}); err == nil || !strings.Contains(err.Error(), "bruno_missing_variables") {
		t.Fatalf("CreateTest error = %v, want missing-variable launch blocker", err)
	}
}

func TestBrunoNativeBatchReportsPartialSecretMigrationAndRetries(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	const username = "native-partial-user-never-return"
	const password = "native-partial-password-never-return"
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Partial Native, uid: partial-native}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Partial Native, uid: partial-native-request, type: http, seq: 1}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: ` + username + `
    password: ` + password + `
`,
		},
	})
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-partial")
	bindings := make(map[string]string, len(inspection.Result.SecretRequirements))
	for _, requirement := range inspection.Result.SecretRequirements {
		switch requirement.Key {
		case "http.auth.username":
			bindings[requirement.ID] = "BRUNO_NATIVE_PARTIAL_A"
		case "http.auth.password":
			bindings[requirement.ID] = "BRUNO_NATIVE_PARTIAL_B"
		default:
			t.Fatalf("unexpected native secret requirement: %#v", requirement)
		}
	}
	underlyingSecrets := server.projectSecretStore
	failingSecrets := &failNthCreateSecretStore{delegate: underlyingSecrets, failOnCall: 2}
	server.projectSecretStore = failingSecrets
	migrate := true
	request := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		SecretBindings:  bindings,
		MigrateSecrets:  &migrate,
	}
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("partial native migration status = %d body=%s", response.Code, response.Body.String())
	}
	var partial struct {
		Code                  string   `json:"code"`
		ScriptCommitted       bool     `json:"script_committed"`
		MigratedCount         int      `json:"migrated_count"`
		CommittedNames        []string `json:"committed_names"`
		RemainingNames        []string `json:"remaining_names"`
		ImportSessionConsumed bool     `json:"import_session_consumed"`
		SessionRetained       bool     `json:"session_retained"`
		ManualRetrySafe       bool     `json:"manual_retry_safe"`
		IdempotencySessionID  string   `json:"idempotency_session_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &partial); err != nil {
		t.Fatal(err)
	}
	if partial.Code != "bruno_secret_migration_partial" || partial.ScriptCommitted ||
		partial.MigratedCount != 1 ||
		strings.Join(partial.CommittedNames, ",") != "BRUNO_NATIVE_PARTIAL_A" ||
		strings.Join(partial.RemainingNames, ",") != "BRUNO_NATIVE_PARTIAL_B" ||
		partial.ImportSessionConsumed || !partial.SessionRetained || !partial.ManualRetrySafe ||
		partial.IdempotencySessionID != inspection.ImportSessionID {
		t.Fatalf("partial native migration = %#v body=%s", partial, response.Body.String())
	}
	if strings.Contains(response.Body.String(), username) || strings.Contains(response.Body.String(), password) {
		t.Fatalf("partial native migration disclosed source values: %s", response.Body.String())
	}
	if scripts, err := store.ListScripts(brunoImportTestProject, nil); err != nil || len(scripts) != 0 {
		t.Fatalf("scripts after partial migration = %#v, err=%v", scripts, err)
	}
	if value, _, err := underlyingSecrets.Get(context.Background(), brunoImportTestProject, "BRUNO_NATIVE_PARTIAL_A"); err != nil || value != username {
		t.Fatalf("retained native secret = %q, err=%v", value, err)
	}

	retry := commitBrunoImportWithStatus(t, server, request)
	if retry.Code != http.StatusCreated || failingSecrets.createCalls < 3 {
		t.Fatalf("partial native retry status = %d calls=%d body=%s", retry.Code, failingSecrets.createCalls, retry.Body.String())
	}
}

func TestBrunoNativeBatchReportsRetainedSecretsWhenDatabaseCommitFails(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	const password = "native-db-failure-secret-never-return"
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: DB Failure Native, uid: db-failure-native}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: DB Failure Native, uid: db-failure-native-request, type: http, seq: 1}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: user
    password: ` + password + `
`,
		},
	})
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-db-failure")
	if len(inspection.Result.SecretRequirements) != 2 {
		t.Fatalf("native DB-failure requirements = %#v", inspection.Result.SecretRequirements)
	}
	expectedNames := make([]string, 0, len(inspection.Result.SecretRequirements))
	for _, requirement := range inspection.Result.SecretRequirements {
		if requirement.Key != "http.auth.username" && requirement.Key != "http.auth.password" {
			t.Fatalf("unexpected native DB-failure requirement: %#v", requirement)
		}
		expectedNames = append(expectedNames, requirement.SuggestedName)
	}
	sort.Strings(expectedNames)
	nativeStore, ok := any(store).(storage.BrunoNativeImportStore)
	if !ok {
		t.Fatal("test store does not implement BrunoNativeImportStore")
	}
	server.store = &failingBrunoNativeCommitStore{
		Store: store, delegate: nativeStore, err: errors.New("simulated native database failure"),
	}
	migrate := true
	request := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		MigrateSecrets:  &migrate,
	}
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("native DB failure status = %d body=%s", response.Code, response.Body.String())
	}
	var partial struct {
		Code                  string   `json:"code"`
		ScriptCommitted       bool     `json:"script_committed"`
		CommittedNames        []string `json:"committed_names"`
		ImportSessionConsumed bool     `json:"import_session_consumed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &partial); err != nil {
		t.Fatal(err)
	}
	if partial.Code != "bruno_native_commit_failed_after_secret_migration" ||
		partial.ScriptCommitted ||
		strings.Join(partial.CommittedNames, ",") != strings.Join(expectedNames, ",") ||
		partial.ImportSessionConsumed {
		t.Fatalf("native DB failure payload = %#v body=%s", partial, response.Body.String())
	}
	if strings.Contains(response.Body.String(), password) || strings.Contains(response.Body.String(), "simulated native database failure") {
		t.Fatalf("native DB failure disclosed protected detail: %s", response.Body.String())
	}
	if scripts, err := store.ListScripts(brunoImportTestProject, nil); err != nil || len(scripts) != 0 {
		t.Fatalf("scripts after native DB failure = %#v, err=%v", scripts, err)
	}
	if sequence, err := store.GetSetting("refseq:LOC:SCR"); err != nil || sequence != "" {
		t.Fatalf("reference sequence after native DB failure = %q, err=%v", sequence, err)
	}
	plannedID := inspection.Result.Scripts[0].PlannedScriptID
	if _, cached := server.runner.GetScript(plannedID); cached {
		t.Fatal("failed native batch refreshed the runner cache")
	}

	server.store = store
	retry := commitBrunoImportWithStatus(t, server, request)
	if retry.Code != http.StatusCreated {
		t.Fatalf("native DB failure retry status = %d body=%s", retry.Code, retry.Body.String())
	}
	if _, cached := server.runner.GetScript(plannedID); !cached {
		t.Fatal("successful native batch did not refresh the runner cache")
	}
}

func TestBrunoNativeBatchDoesNotAdvertiseExpiredSessionAsRetryable(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-expired-restore")
	expiry := time.Now().UTC().Add(2 * time.Second)
	rewriteBrunoImportSessionForTest(t, server, inspection.ImportSessionID, func(session *brunoImportSession) {
		session.ExpiresAt = expiry
	})
	nativeStore, ok := any(store).(storage.BrunoNativeImportStore)
	if !ok {
		t.Fatal("test store does not implement BrunoNativeImportStore")
	}
	blocking := &blockingUnreconciledBrunoNativeCommitStore{
		Store: store, delegate: nativeStore,
		entered: make(chan struct{}), release: make(chan struct{}),
		commitErr: errors.New("simulated commit failure"),
		lookupErr: errors.New("simulated receipt lookup failure"),
	}
	server.store = blocking
	request := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(
		http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(payload),
	)
	httpRequest.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.mux.ServeHTTP(response, httpRequest)
		close(done)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native commit did not reach the blocking store before expiry")
	}
	if wait := time.Until(expiry.Add(25 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	close(blocking.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("native commit did not finish after release")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expired restoration status = %d body=%s", response.Code, response.Body.String())
	}
	var failure struct {
		SessionRetained      bool `json:"session_retained"`
		ManualRetrySafe      bool `json:"manual_retry_safe"`
		SourceValuesRetained bool `json:"source_values_retained"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.SessionRetained || failure.ManualRetrySafe || failure.SourceValuesRetained {
		t.Fatalf("expired restoration was advertised as retryable: %#v body=%s", failure, response.Body.String())
	}
	server.store = store
	retry := commitBrunoImportWithStatus(t, server, request)
	if retry.Code != http.StatusNotFound {
		t.Fatalf("expired restoration remained reusable: status=%d body=%s", retry.Code, retry.Body.String())
	}
}

func TestBrunoNativeBatchReconcilesAmbiguousCommitFromDurableReceipt(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-ambiguous")
	nativeStore, ok := any(store).(storage.BrunoNativeImportStore)
	if !ok {
		t.Fatal("test store does not implement BrunoNativeImportStore")
	}
	server.store = &failingBrunoNativeCommitStore{
		Store:             store,
		delegate:          nativeStore,
		err:               errors.New("simulated lost commit acknowledgement"),
		commitBeforeError: true,
	}

	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("ambiguous native commit status = %d body=%s", response.Code, response.Body.String())
	}
	reconciled := decodeBrunoNativeBatchTestResponse(t, response)
	if reconciled.Created || !reconciled.IdempotentReplay || reconciled.Count != 2 {
		t.Fatalf("reconciled native receipt = %#v", reconciled)
	}
	persisted, err := store.ListScripts(brunoImportTestProject, nil)
	if err != nil || len(persisted) != 2 {
		t.Fatalf("reconciled persisted scripts = %#v, err=%v", persisted, err)
	}
	for _, script := range reconciled.Scripts {
		if _, cached := server.runner.GetScript(script.ID); !cached {
			t.Fatalf("reconciled script %q was not refreshed into cache", script.ID)
		}
	}
}

func TestBrunoNativeBatchRequiresReviewAcknowledgementAndSavedScriptBlocksReplay(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Dynamic API, uid: dynamic-api}\n",
		},
		{
			Path: "demo/dynamic.yml",
			Content: `info: {name: Dynamic Request, uid: dynamic-request, type: http, seq: 1}
http: {method: GET, url: https://example.test/dynamic}
runtime:
  scripts:
    - type: after-response
      code: |
        test("dynamic", function () {
          bru.setVar("token", res.getBody());
        });
`,
		},
	})
	if inspection.Result == nil || len(inspection.Result.Scripts) != 1 {
		t.Fatalf("review inspection = %#v", inspection.Result)
	}
	candidate := inspection.Result.Scripts[0]
	requiredFindingIDs := requiredBrunoNativeFindingIDs(candidate)
	if len(requiredFindingIDs) == 0 {
		t.Fatalf("candidate diagnostics = %#v, want acknowledgement finding", candidate.Diagnostics)
	}

	commitRequest := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-review")
	commitRequest.FolderID = &folderID
	blocked := commitBrunoImportWithStatus(t, server, commitRequest)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("unacknowledged commit status = %d body=%s", blocked.Code, blocked.Body.String())
	}
	var blockedPayload struct {
		Code     string   `json:"code"`
		Findings []string `json:"findings"`
	}
	if err := json.Unmarshal(blocked.Body.Bytes(), &blockedPayload); err != nil {
		t.Fatalf("decode acknowledgement response: %v", err)
	}
	if blockedPayload.Code != "bruno_native_review_acknowledgement_required" ||
		strings.Join(blockedPayload.Findings, ",") != strings.Join(requiredFindingIDs, ",") {
		t.Fatalf("acknowledgement response = %#v, want %#v", blockedPayload, requiredFindingIDs)
	}

	commitRequest.AcknowledgedFindingIDs = requiredFindingIDs
	createdResponse := commitBrunoImportWithStatus(t, server, commitRequest)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("acknowledged commit status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, createdResponse)
	if len(created.Scripts) != 1 || created.Scripts[0] == nil {
		t.Fatalf("acknowledged batch = %#v", created)
	}
	action := created.Scripts[0].Actions[0]
	if action.Metadata["review_gate"] != "bruno_javascript" ||
		action.Metadata["review_required"] != true ||
		action.Metadata["promotion_blocker"] != true {
		t.Fatalf("saved review metadata = %#v", action.Metadata)
	}

	validateResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(
		validateResponse,
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+created.Scripts[0].ID+"/validate", nil),
	)
	if validateResponse.Code != http.StatusOK {
		t.Fatalf("saved validation status = %d body=%s", validateResponse.Code, validateResponse.Body.String())
	}
	var validation scriptValidationResponse
	if err := json.Unmarshal(validateResponse.Body.Bytes(), &validation); err != nil {
		t.Fatalf("decode saved validation: %v", err)
	}
	if validation.Preflight == nil || validation.Preflight.Valid ||
		replayPreflightCheckStatus(validation.Preflight, "review_gates") != "fail" ||
		!brunoNativeIssuesContain(validation.Preflight.Issues, "review-required action blocks replay") {
		t.Fatalf("saved replay preflight = %#v, want review-gated blocker", validation.Preflight)
	}
}

func inspectBrunoNativeFiles(
	t *testing.T,
	server *Server,
	projectID string,
	files []brunoInspectJSONFile,
) brunoInspectResponse {
	t.Helper()
	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: projectID,
		Files:     files,
		Options: brunoconvert.InspectOptions{
			OutputMode: brunoconvert.OutputModeNativeRequests,
		},
	})
	if err != nil {
		t.Fatalf("marshal native inspection: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect",
		bytes.NewReader(payload),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("native inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatalf("decode native inspection: %v", err)
	}
	return inspected
}

func orderedBrunoNativeFiles() []brunoInspectJSONFile {
	return []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yaml",
			Content: "opencollection: 1.0.0\ninfo: {name: Ordered APIs, uid: ordered-apis}\n",
		},
		{
			Path:    "demo/z/folder.yml",
			Content: "info: {name: Z APIs, uid: z-folder, seq: 2}\n",
		},
		{
			Path: "demo/z/zeta.yml",
			Content: `info: {name: Zeta, uid: zeta-request, type: http, seq: 1}
http: {method: POST, url: https://example.test/zeta, body: {type: text, data: zeta}}
settings: {followRedirects: true, maxRedirects: 5, timeout: 1000}
`,
		},
		{
			Path:    "demo/a/folder.yaml",
			Content: "info: {name: A APIs, uid: a-folder, seq: 1}\n",
		},
		{
			Path: "demo/a/alpha.yml",
			Content: `info: {name: Alpha, uid: alpha-request, type: http, seq: 9}
http: {method: GET, url: https://example.test/alpha}
settings: {followRedirects: true, maxRedirects: 5, timeout: 1000}
`,
		},
	}
}

func decodeBrunoNativeBatchTestResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
) brunoNativeBatchTestResponse {
	t.Helper()
	var decoded brunoNativeBatchTestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode native batch: %v", err)
	}
	return decoded
}

func commitBrunoNativeRawWithStatus(t *testing.T, server *Server, payload any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal native commit: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func commitBrunoNativeAsActor(
	t *testing.T,
	server *Server,
	payload brunoCommitRequest,
	subject string,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal native actor commit: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request = withAuthenticatedContext(request, authResolution{User: &AuthenticatedUser{Subject: subject}})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func requiredBrunoNativeFindingIDs(candidate brunoconvert.NativeScriptCandidate) []string {
	var ids []string
	for _, diagnostic := range candidate.Diagnostics {
		if diagnostic.AcknowledgementRequired {
			ids = append(ids, diagnostic.ID)
		}
	}
	return ids
}

func brunoNativeIssuesContain(issues []string, expected string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, expected) {
			return true
		}
	}
	return false
}

func saveBrunoNativeScriptFolder(t *testing.T, store *storage.SQLiteStore, projectID, folderID string) string {
	t.Helper()
	now := time.Now().UTC()
	if err := store.SaveFolder(&storage.Folder{
		ID:        folderID,
		ProjectID: projectID,
		Name:      "Native Bruno APIs",
		Category:  "scripts",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}
	return folderID
}

type failingBrunoNativeCommitStore struct {
	storage.Store
	delegate          storage.BrunoNativeImportStore
	err               error
	commitBeforeError bool
}

type brunoNativeStoreWithoutArtifactChecker struct {
	storage.Store
	delegate storage.BrunoNativeImportStore
}

type blockingAfterBrunoNativeCommitStore struct {
	storage.Store
	delegate  storage.BrunoNativeImportStore
	committed chan struct{}
	release   chan struct{}
	once      sync.Once
}

type blockingUnreconciledBrunoNativeCommitStore struct {
	storage.Store
	delegate  storage.BrunoNativeImportStore
	entered   chan struct{}
	release   chan struct{}
	commitErr error
	lookupErr error
	once      sync.Once
	mu        sync.Mutex
	lookups   int
}

func (s *blockingUnreconciledBrunoNativeCommitStore) CommitBrunoNativeImport(
	*storage.BrunoNativeImportCommit,
) (*storage.BrunoNativeImportCommitResult, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return nil, s.commitErr
}

func (s *blockingUnreconciledBrunoNativeCommitStore) GetBrunoNativeImportByIdempotencyKey(
	idempotencyKey, projectID string,
) (*storage.BrunoNativeImportCommitResult, error) {
	s.mu.Lock()
	s.lookups++
	lookup := s.lookups
	s.mu.Unlock()
	if lookup == 1 {
		return s.delegate.GetBrunoNativeImportByIdempotencyKey(idempotencyKey, projectID)
	}
	return nil, s.lookupErr
}

func (s *blockingAfterBrunoNativeCommitStore) CommitBrunoNativeImport(
	commit *storage.BrunoNativeImportCommit,
) (*storage.BrunoNativeImportCommitResult, error) {
	result, err := s.delegate.CommitBrunoNativeImport(commit)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() { close(s.committed) })
	<-s.release
	return result, nil
}

func (s *blockingAfterBrunoNativeCommitStore) GetBrunoNativeImportByIdempotencyKey(
	idempotencyKey, projectID string,
) (*storage.BrunoNativeImportCommitResult, error) {
	return s.delegate.GetBrunoNativeImportByIdempotencyKey(idempotencyKey, projectID)
}

func (s *brunoNativeStoreWithoutArtifactChecker) CommitBrunoNativeImport(commit *storage.BrunoNativeImportCommit) (*storage.BrunoNativeImportCommitResult, error) {
	return s.delegate.CommitBrunoNativeImport(commit)
}

func (s *brunoNativeStoreWithoutArtifactChecker) GetBrunoNativeImportByIdempotencyKey(idempotencyKey, projectID string) (*storage.BrunoNativeImportCommitResult, error) {
	return s.delegate.GetBrunoNativeImportByIdempotencyKey(idempotencyKey, projectID)
}

func (s *failingBrunoNativeCommitStore) CommitBrunoNativeImport(commit *storage.BrunoNativeImportCommit) (*storage.BrunoNativeImportCommitResult, error) {
	if s.commitBeforeError {
		if _, err := s.delegate.CommitBrunoNativeImport(commit); err != nil {
			return nil, err
		}
	}
	return nil, s.err
}

func (s *failingBrunoNativeCommitStore) GetBrunoNativeImportByIdempotencyKey(idempotencyKey, projectID string) (*storage.BrunoNativeImportCommitResult, error) {
	return s.delegate.GetBrunoNativeImportByIdempotencyKey(idempotencyKey, projectID)
}
