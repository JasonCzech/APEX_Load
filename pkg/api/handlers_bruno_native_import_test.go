package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
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

func TestBrunoNativeInspectServesAuthorizedSanitizedSourceFile(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	requestSource := func(sourcePath string) *httptest.ResponseRecorder {
		t.Helper()
		query := url.Values{
			"project_id": {brunoImportTestProject},
			"path":       {sourcePath},
		}.Encode()
		request := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/scripts/import/bruno/sessions/"+url.PathEscape(inspection.ImportSessionID)+"/source?"+query,
			nil,
		)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}

	response := requestSource("z/zeta.yml")
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "url: https://example.test/zeta") {
		t.Fatalf("source response status = %d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("source Cache-Control = %q, want no-store", got)
	}

	response = requestSource("opencollection.yaml")
	if response.Code != http.StatusNotFound {
		t.Fatalf("non-candidate source status = %d body=%s", response.Code, response.Body.String())
	}
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
		revisions, err := store.ListImportSourceRevisions(script.ID, brunoImportTestProject)
		if err != nil {
			t.Fatalf("ListImportSourceRevisions(%q): %v", script.ID, err)
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
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "bruno_runner_retired") {
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

func TestBrunoNativeBatchAuthorizedMigrationOptInWithoutSecretsStillCommits(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
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
	enableSourceSecretMigration(t, server)
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
	if requirement.Value != "" || requirement.SourceValueAvailable ||
		requirement.DestinationValuePreserved || !requirement.ExistingReference ||
		requirement.PreservationUnavailableReason != brunoconvert.PreservationUnavailableReference {
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

func TestBrunoNativeInspectExplainsUnresolvedCredentialValue(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Unresolved credential}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Unresolved credential, uid: unresolved-credential, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: basic, username: '{{username}}', password: '{{password}}'}
`,
		},
	})
	if len(inspection.Result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v", inspection.Result.SecretRequirements)
	}
	for _, requirement := range inspection.Result.SecretRequirements {
		if requirement.DestinationValuePreserved ||
			requirement.PreservationUnavailableReason != brunoconvert.PreservationUnavailableSourceValue {
			t.Fatalf("unresolved requirement = %#v", requirement)
		}
	}
}

func TestBrunoNativeBatchCommitsUnresolvedCredentialsWithoutBindings(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Unbound credential, uid: unbound-credential}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Unbound credential, uid: unbound-credential-request, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: basic, username: '{{username}}', password: '{{password}}'}
`,
		},
	})
	if len(inspection.Result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v", inspection.Result.SecretRequirements)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-unbound")
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("unbound credential commit status = %d body=%s", response.Code, response.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, response)
	encoded, err := json.Marshal(created.Scripts[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range inspection.Result.SecretRequirements {
		if !strings.Contains(string(encoded), requirement.Replacement) {
			t.Fatalf("saved script lost unresolved reference %q: %s", requirement.Replacement, encoded)
		}
	}
}

// A session header is recorded state, not a credential. When the export
// redacted its value, the header becomes an input supplied at review instead
// of a project secret, and the sentinel is never sent or disclosed.
func TestBrunoNativeInspectTurnsRedactedRecordedHeaderIntoInput(t *testing.T) {
	tests := []struct {
		name    string
		headers string
	}{
		{
			name: "header list",
			headers: `
    - {name: idpctx-ixp-dark-mode-session, value: "[REDACTED]"}`,
		},
		{
			name: "header map",
			headers: `
    idpctx-ixp-dark-mode-session: "[redacted]"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _, _ := newBrunoImportAPITestServer(t)
			inspection, responseBody := inspectBrunoNativeFilesWithBody(
				t,
				server,
				brunoImportTestProject,
				[]brunoInspectJSONFile{
					{
						Path:    "demo/opencollection.yml",
						Content: "opencollection: 1.0.0\ninfo: {name: Redacted native header}\n",
					},
					{
						Path: "demo/request.yml",
						Content: `info: {name: Redacted native header, uid: redacted-native-header, type: http}
http:
  method: GET
  url: https://example.test
  headers:` + test.headers + `
`,
					},
				},
			)
			if strings.Contains(strings.ToLower(string(responseBody)), "[redacted]") {
				t.Fatalf("native inspect response disclosed the redaction sentinel: %s", responseBody)
			}
			if len(inspection.Result.SecretRequirements) != 0 || len(inspection.Result.Scripts) != 1 {
				t.Fatalf("redacted recorded header = aggregate %#v candidates %#v", inspection.Result.SecretRequirements, inspection.Result.Scripts)
			}
			candidate := inspection.Result.Scripts[0]
			if len(candidate.SecretRequirements) != 0 ||
				!slices.Equal(candidate.MissingVariables, []string{"header_idpctx_ixp_dark_mode_session"}) {
				t.Fatalf("redacted recorded header candidate = %#v", candidate)
			}
			// The review offers a value field for the input.
			offered := false
			if inspection.SharedValues != nil {
				for _, value := range inspection.SharedValues.Candidates {
					offered = offered || (value.Kind == sharedvalue.KindSourceVariable && value.Name == "header_idpctx_ixp_dark_mode_session")
				}
			}
			if !offered {
				t.Fatalf("shared values = %#v, want an input for the redacted header", inspection.SharedValues)
			}

			session, err := server.loadBrunoImportSession(
				httptest.NewRequest(http.MethodGet, "/", nil),
				inspection.ImportSessionID,
				brunoImportTestProject,
			)
			if err != nil {
				t.Fatalf("load redacted native session: %v", err)
			}
			encoded, err := json.Marshal(session.Result.Scripts)
			if err != nil {
				t.Fatal(err)
			}
			if len(session.Result.SecretRequirements) != 0 || strings.Contains(strings.ToLower(string(encoded)), "[redacted]") {
				t.Fatalf("protected session kept the redaction sentinel: %s", encoded)
			}
		})
	}
}

func TestBrunoNativeInspectTreatsCredentialRedactionSentinelsAsUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		request string
	}{
		{
			name: "JSON credential",
			request: `info: {name: JSON sentinel, uid: json-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body: {type: json, data: '{"password":" [rEdAcTeD] "}'}
`,
		},
		{
			name: "percent encoded URL credential",
			request: `info: {name: URL sentinel, uid: url-sentinel, type: http}
http:
  method: GET
  url: https://example.test?token=%5bReDaCtEd%5d
`,
		},
		{
			name: "XML credential",
			request: `info: {name: XML sentinel, uid: xml-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body: {type: xml, data: '<root><password>&#91;ReDaCtEd&#93;</password></root>'}
`,
		},
		{
			name: "Authorization wrapper",
			request: `info: {name: Authorization sentinel, uid: authorization-sentinel, type: http}
http:
  method: GET
  url: https://example.test
  headers: [{name: Authorization, value: "Bearer [REDACTED]"}]
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _, _ := newBrunoImportAPITestServer(t)
			inspection, responseBody := inspectBrunoNativeFilesWithBody(
				t,
				server,
				brunoImportTestProject,
				[]brunoInspectJSONFile{
					{
						Path:    "demo/opencollection.yml",
						Content: "opencollection: 1.0.0\ninfo: {name: Sentinel collection}\n",
					},
					{Path: "demo/request.yml", Content: test.request},
				},
			)
			for _, sentinel := range []string{
				"[redacted]", "%5bredacted%5d", "&#91;redacted&#93;", "&#x5b;redacted&#x5d;",
			} {
				if strings.Contains(strings.ToLower(string(responseBody)), sentinel) {
					t.Fatalf("native inspect response disclosed sentinel %q: %s", sentinel, responseBody)
				}
			}
			if len(inspection.Result.SecretRequirements) != 1 ||
				len(inspection.Result.Scripts) != 1 ||
				len(inspection.Result.Scripts[0].SecretRequirements) != 1 {
				t.Fatalf("sentinel requirements = aggregate %#v candidates %#v", inspection.Result.SecretRequirements, inspection.Result.Scripts)
			}
			for _, requirement := range []brunoconvert.SecretRequirement{
				inspection.Result.SecretRequirements[0],
				inspection.Result.Scripts[0].SecretRequirements[0],
			} {
				if requirement.Value != "" || requirement.SourceValueAvailable ||
					requirement.DestinationValuePreserved ||
					requirement.PreservationUnavailableReason != brunoconvert.PreservationUnavailableSourceValue {
					t.Errorf("sentinel requirement = %#v", requirement)
				}
			}

			session, err := server.loadBrunoImportSession(
				httptest.NewRequest(http.MethodGet, "/", nil),
				inspection.ImportSessionID,
				brunoImportTestProject,
			)
			if err != nil {
				t.Fatalf("load sentinel session: %v", err)
			}
			if len(session.Result.SecretRequirements) != 1 ||
				session.Result.SecretRequirements[0].Value != "" {
				t.Fatalf("protected session retained sentinel: %#v", session.Result.SecretRequirements)
			}
			candidate, err := json.Marshal(session.Result.Scripts[0].Script)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.ToLower(string(candidate)), "redacted") ||
				!bytes.Contains(candidate, []byte("{{secret.")) {
				t.Fatalf("candidate did not retain only a governed reference: %s", candidate)
			}
		})
	}
}

func TestBrunoNativePersistedSentinelReloadCommitsOnlyGovernedReference(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Reloaded sentinel}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Reloaded sentinel, uid: reloaded-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body: {type: xml, data: '<root><password>&#91;ReDaCtEd&#93;</password></root>'}
`,
		},
	})
	if len(inspection.Result.SecretRequirements) != 1 {
		t.Fatalf("sentinel requirements = %#v", inspection.Result.SecretRequirements)
	}

	rewriteBrunoImportSessionForTest(t, server, inspection.ImportSessionID, func(session *brunoImportSession) {
		if len(session.Result.SecretRequirements) != 1 {
			t.Fatalf("protected sentinel requirements = %#v", session.Result.SecretRequirements)
		}
		// Simulate a pre-fix or internally tampered retained-value envelope. The
		// persistence boundary must omit this unusable value.
		session.Result.SecretRequirements[0].Value = "  [rEdAcTeD]  "
	})
	server.brunoImportMu.Lock()
	for _, session := range server.brunoImportSessions {
		if session != nil && session.Timer != nil {
			session.Timer.Stop()
			session.Timer = nil
		}
	}
	server.brunoImportSessions = make(map[string]*brunoImportSession)
	server.brunoImportMu.Unlock()

	reloaded, err := server.loadBrunoImportSession(
		httptest.NewRequest(http.MethodGet, "/", nil),
		inspection.ImportSessionID,
		brunoImportTestProject,
	)
	if err != nil {
		t.Fatalf("reload sentinel session: %v", err)
	}
	if len(reloaded.Result.SecretRequirements) != 1 ||
		reloaded.Result.SecretRequirements[0].Value != "" {
		t.Fatalf("reloaded session retained sentinel: %#v", reloaded.Result.SecretRequirements)
	}

	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-reloaded-sentinel")
	acknowledged := make([]string, 0)
	for _, diagnostic := range inspection.Result.Scripts[0].Diagnostics {
		if diagnostic.AcknowledgementRequired {
			acknowledged = append(acknowledged, diagnostic.ID)
		}
	}
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		SecretBindings: map[string]string{
			inspection.Result.SecretRequirements[0].ID: inspection.Result.SecretRequirements[0].SuggestedName,
		},
		AcknowledgedFindingIDs: acknowledged,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("reloaded sentinel commit status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "redacted") {
		t.Fatalf("reloaded sentinel commit response leaked sentinel: %s", response.Body.String())
	}
	scripts, err := store.ListScripts(brunoImportTestProject, &folderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || len(scripts[0].Actions) != 1 ||
		strings.Contains(strings.ToLower(scripts[0].Actions[0].Body), "redacted") ||
		!strings.Contains(scripts[0].Actions[0].Body, "{{secret.") {
		t.Fatalf("committed sentinel destination = %#v", scripts)
	}
}

func TestDecodePersistedBrunoImportSessionDropsSentinelCredentials(t *testing.T) {
	for _, value := range []string{
		" [rEdAcTeD] ",
		"Bearer [REDACTED]",
		"sid=[redacted]",
		"%5BReDaCtEd%5D",
		"%25255BReDaCtEd%25255D",
		"%5BReDaCtEd%5D%ZZ",
		"&#91;ReDaCtEd&#93;",
		"&amp;amp;#91;ReDaCtEd&amp;amp;#93;",
		strings.Repeat("%25", 40) + "5BReDaCtEd%5D",
		"&#x5B;ReDaCtEd&#x5D;",
	} {
		t.Run(value, func(t *testing.T) {
			const sessionID = "persisted-sentinel-session"
			payload, err := json.Marshal(persistedBrunoImportSession{
				ID:        sessionID,
				ProjectID: brunoImportTestProject,
				Result: &brunoconvert.Result{SecretRequirements: []brunoconvert.SecretRequirement{{
					ID:                        "sentinel-requirement",
					SuggestedName:             "SENTINEL_REQUIREMENT",
					Replacement:               "{{secret.SENTINEL_REQUIREMENT}}",
					SourceValueAvailable:      true,
					DestinationValuePreserved: true,
				}}},
				SecretValues: map[string]string{"sentinel-requirement": value},
			})
			if err != nil {
				t.Fatal(err)
			}
			session, err := decodePersistedBrunoImportSession(sessionID, payload)
			if err != nil {
				t.Fatal(err)
			}
			requirement := session.Result.SecretRequirements[0]
			if requirement.Value != "" || requirement.SourceValueAvailable ||
				requirement.DestinationValuePreserved ||
				requirement.PreservationUnavailableReason != brunoconvert.PreservationUnavailableSourceValue {
				t.Fatalf("decoded sentinel requirement = %#v", requirement)
			}
		})
	}
}

func TestBrunoNativeInspectExposesSafeUnresolvedXMLCredentialContext(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Unresolved XML credential}\n",
		},
		{
			Path: "demo/first.yml",
			Content: `info: {name: First, uid: unresolved-xml-first, type: http, seq: 1}
http:
  method: POST
  url: https://example.test/first
  body: {type: xml, data: '<Envelope><mh:authentication><mh:userName>{{partnerUserName}}</mh:userName></mh:authentication></Envelope>'}
`,
		},
		{
			Path: "demo/second.yml",
			Content: `info: {name: Second, uid: unresolved-xml-second, type: http, seq: 2}
http:
  method: POST
  url: https://example.test/second
  body: {type: xml, data: '<Envelope><mh:authentication><mh:userName>{{partnerUserName}}</mh:userName></mh:authentication></Envelope>'}
`,
		},
	})
	if len(inspection.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspection.Result.SecretRequirements)
	}
	requirement := inspection.Result.SecretRequirements[0]
	if requirement.SuggestedName != "PARTNER_USERNAME" ||
		requirement.Replacement != "{{secret."+requirement.SuggestedName+"}}" ||
		requirement.DisplayLabel != "Partner username" ||
		requirement.SourceVariable != "partnerUserName" ||
		!requirement.UnresolvedSourceVariable ||
		requirement.Context != "XML username element <mh:userName>" ||
		requirement.SourceValueAvailable || requirement.DestinationValuePreserved ||
		requirement.PreservationUnavailableReason != brunoconvert.PreservationUnavailableSourceValue ||
		requirement.Value != "" {
		t.Fatalf("unresolved XML requirement = %#v", requirement)
	}
	wantPaths := []string{"demo/first.yml", "demo/second.yml"}
	if !reflect.DeepEqual(requirement.AffectedPaths, wantPaths) {
		t.Fatalf("affected paths = %#v, want %#v", requirement.AffectedPaths, wantPaths)
	}
	wantCandidatePaths := []string{"first.yml", "second.yml"}
	if len(inspection.Result.MissingVariables) != 0 {
		t.Fatalf("result missing variables = %#v, want none", inspection.Result.MissingVariables)
	}
	if len(inspection.Result.Scripts) != len(wantCandidatePaths) {
		t.Fatalf("native candidates = %#v, want %d", inspection.Result.Scripts, len(wantCandidatePaths))
	}
	for index, candidate := range inspection.Result.Scripts {
		if candidate.SourcePath != wantCandidatePaths[index] {
			t.Fatalf("candidate %d source path = %q, want %q", index, candidate.SourcePath, wantCandidatePaths[index])
		}
		if len(candidate.MissingVariables) != 0 {
			t.Fatalf("candidate %q missing variables = %#v, want none", candidate.SourcePath, candidate.MissingVariables)
		}
		if len(candidate.SecretRequirements) != 1 {
			t.Fatalf("candidate %q secret requirements = %#v, want one", candidate.SourcePath, candidate.SecretRequirements)
		}
		candidateRequirement := candidate.SecretRequirements[0]
		if candidateRequirement.ID != requirement.ID ||
			candidateRequirement.SuggestedName != requirement.SuggestedName ||
			candidateRequirement.Replacement != requirement.Replacement ||
			candidateRequirement.DisplayLabel != "Partner username" ||
			candidateRequirement.SourceVariable != "partnerUserName" ||
			!candidateRequirement.UnresolvedSourceVariable ||
			candidateRequirement.Context != "XML username element <mh:userName>" ||
			candidateRequirement.SourceValueAvailable || candidateRequirement.DestinationValuePreserved ||
			candidateRequirement.PreservationUnavailableReason != brunoconvert.PreservationUnavailableSourceValue ||
			candidateRequirement.Value != "" {
			t.Fatalf("candidate %q unresolved XML requirement = %#v", candidate.SourcePath, candidateRequirement)
		}
		if candidateRequirement.Path != wantPaths[index] ||
			!reflect.DeepEqual(candidateRequirement.AffectedPaths, []string{wantPaths[index]}) {
			t.Fatalf(
				"candidate %q requirement locations = path %q affected %#v, want candidate-only path",
				candidate.SourcePath,
				candidateRequirement.Path,
				candidateRequirement.AffectedPaths,
			)
		}
		if candidate.Script == nil {
			t.Fatalf("candidate %q script is nil", candidate.SourcePath)
		}
		for actionIndex, action := range candidate.Script.Actions {
			if action.Metadata["execution_blocker"] == "bruno_missing_variables" {
				t.Fatalf(
					"candidate %q action %d retained missing-variable blocker: %#v",
					candidate.SourcePath,
					actionIndex,
					action.Metadata,
				)
			}
		}
	}
}

func TestBrunoNativeInspectExposesSuppliedHeaderCredentialMetadata(t *testing.T) {
	const (
		activateSessionValue = "test-session-id-for-activate-subscriber"
		darkModeSessionValue = "test-dark-mode-session-value"
		cpniSessionValue     = "test-session-id-for-cpni-contact"
	)
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection, responseBody := inspectBrunoNativeFilesWithBody(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "CTA_Collection 6a/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: CTA credential presentation}\n",
		},
		{
			Path: "CTA_Collection 6a/CTA/InquireAccountProfile_312.yml",
			Content: `info: {name: InquireAccountProfile_312, uid: inquire-account-profile-312, type: http, seq: 1}
http:
  method: POST
  url: https://example.test/account
  headers:
    - {name: Content-Type, value: text/xml}
    - {name: X-CDF-AUTH-TOKEN, value: "{{authToken}}"}
`,
		},
		{
			Path: "CTA_Collection 6a/CTA/ActivateSubscriber_v302.yml",
			Content: `info: {name: ActivateSubscriber_v302, uid: activate-subscriber-v302, type: http, seq: 2}
http:
  method: POST
  url: https://example.test/activate
  headers:
    - {name: Content-Type, value: text/xml}
    - {name: idpctx-feature-svc-fraud-check, value: "false"}
    - {name: idpctx-feature-svc-relax-credit-check-requirement, value: "true"}
    - {name: idpctx-session-id, value: ` + activateSessionValue + `}
    - {name: randomheadertest, value: harmless-test-value}
    - {name: idpctx-user-type, value: test-user}
    - {name: idpctx-partner-initiated-order, value: "true"}
    - {name: idpctx-ixp-dark-mode-session, value: ` + darkModeSessionValue + `}
`,
		},
		{
			Path: "CTA_Collection 6a/CTA/InquireCPNIContactInformation_224.yml",
			Content: `info: {name: InquireCPNIContactInformation_224, uid: inquire-cpni-contact-224, type: http, seq: 3}
http:
  method: POST
  url: https://example.test/cpni
  headers:
    - {name: Content-Type, value: text/xml}
    - {name: idp-trace-id, value: test-trace-id-for-cpni-contact}
    - {name: idpctx-session-id, value: ` + cpniSessionValue + `}
`,
		},
	})
	for _, protectedValue := range []string{
		activateSessionValue,
		darkModeSessionValue,
		cpniSessionValue,
	} {
		if bytes.Contains(responseBody, []byte(protectedValue)) {
			t.Fatalf("native inspect response disclosed a supplied header credential")
		}
	}

	tests := []struct {
		name                 string
		label                string
		path                 string
		candidateSourcePath  string
		sourceVariable       string
		contextParts         []string
		unresolved           bool
		destinationPreserved bool
		preservationReason   string
	}{
		{
			name:                "X_CDF_AUTH_TOKEN",
			label:               "X-CDF auth token",
			path:                "CTA_Collection 6a/CTA/InquireAccountProfile_312.yml",
			candidateSourcePath: "CTA/InquireAccountProfile_312.yml",
			sourceVariable:      "authToken",
			contextParts:        []string{`header "X-CDF-AUTH-TOKEN"`, "references {{authToken}}", "does not include a value", "configured destination value", "when this request runs"},
			unresolved:          true,
			preservationReason:  brunoconvert.PreservationUnavailableSourceValue,
		},
	}
	// Session and trace headers are recorded state, not credentials: they
	// stay in the saved scripts as imported.
	if len(inspection.Result.SecretRequirements) != len(tests) {
		t.Fatalf("requirements = %#v, want only the auth token", inspection.Result.SecretRequirements)
	}
	session, err := server.loadBrunoImportSession(
		httptest.NewRequest(http.MethodGet, "/", nil),
		inspection.ImportSessionID,
		brunoImportTestProject,
	)
	if err != nil {
		t.Fatalf("load native session: %v", err)
	}
	keptValues := map[string]string{}
	for _, candidate := range session.Result.Scripts {
		for _, header := range candidate.Script.Actions[0].HeaderList {
			keptValues[candidate.SourcePath+" "+header.Name] = header.Value
		}
	}
	for key, want := range map[string]string{
		"CTA/ActivateSubscriber_v302.yml idpctx-session-id":            activateSessionValue,
		"CTA/ActivateSubscriber_v302.yml idpctx-ixp-dark-mode-session": darkModeSessionValue,
		"CTA/InquireCPNIContactInformation_224.yml idpctx-session-id":  cpniSessionValue,
		"CTA/InquireCPNIContactInformation_224.yml idp-trace-id":       "test-trace-id-for-cpni-contact",
	} {
		if keptValues[key] != want {
			t.Errorf("%s = %q, want the imported value", key, keptValues[key])
		}
	}

	aggregateByName := make(map[string]brunoconvert.SecretRequirement, len(inspection.Result.SecretRequirements))
	for _, requirement := range inspection.Result.SecretRequirements {
		aggregateByName[requirement.SuggestedName] = requirement
	}
	candidatesByPath := make(map[string]brunoconvert.NativeScriptCandidate, len(inspection.Result.Scripts))
	for _, candidate := range inspection.Result.Scripts {
		candidatesByPath[candidate.SourcePath] = candidate
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			aggregate, exists := aggregateByName[test.name]
			if !exists {
				t.Fatalf("aggregate requirement %q not found in %#v", test.name, inspection.Result.SecretRequirements)
			}
			assertBrunoNativeHeaderCredentialMetadata(
				t,
				aggregate,
				test.name,
				test.label,
				test.path,
				test.sourceVariable,
				test.contextParts,
				test.unresolved,
				test.destinationPreserved,
				test.preservationReason,
			)

			candidate, exists := candidatesByPath[test.candidateSourcePath]
			if !exists {
				t.Fatalf("candidate %q not found in %#v", test.candidateSourcePath, inspection.Result.Scripts)
			}
			if len(candidate.MissingVariables) != 0 {
				t.Fatalf("candidate %q missing variables = %#v, want none", candidate.SourcePath, candidate.MissingVariables)
			}
			var candidateRequirement *brunoconvert.SecretRequirement
			for index := range candidate.SecretRequirements {
				if candidate.SecretRequirements[index].SuggestedName == test.name {
					candidateRequirement = &candidate.SecretRequirements[index]
					break
				}
			}
			if candidateRequirement == nil {
				t.Fatalf("candidate %q requirement %q not found in %#v", candidate.SourcePath, test.name, candidate.SecretRequirements)
			}
			if candidateRequirement.ID != aggregate.ID {
				t.Fatalf("candidate requirement id = %q, aggregate id = %q", candidateRequirement.ID, aggregate.ID)
			}
			assertBrunoNativeHeaderCredentialMetadata(
				t,
				*candidateRequirement,
				test.name,
				test.label,
				test.path,
				test.sourceVariable,
				test.contextParts,
				test.unresolved,
				test.destinationPreserved,
				test.preservationReason,
			)
			if candidate.Script == nil {
				t.Fatalf("candidate %q script is nil", candidate.SourcePath)
			}
			for actionIndex, action := range candidate.Script.Actions {
				if brunoNativeMetadataHasBlocker(action.Metadata, "bruno_missing_variables") {
					t.Fatalf("candidate %q action %d retained missing-variable blocker: %#v", candidate.SourcePath, actionIndex, action.Metadata)
				}
			}
		})
	}
}

func assertBrunoNativeHeaderCredentialMetadata(
	t *testing.T,
	requirement brunoconvert.SecretRequirement,
	name, label, sourcePath, sourceVariable string,
	contextParts []string,
	unresolved, destinationPreserved bool,
	preservationReason string,
) {
	t.Helper()
	if requirement.SuggestedName != name ||
		requirement.Replacement != "{{secret."+name+"}}" ||
		requirement.DisplayLabel != label ||
		requirement.Path != sourcePath ||
		requirement.SourceVariable != sourceVariable ||
		requirement.UnresolvedSourceVariable != unresolved ||
		requirement.SourceValueAvailable ||
		requirement.DestinationValuePreserved != destinationPreserved ||
		requirement.PreservationUnavailableReason != preservationReason ||
		requirement.Value != "" ||
		!reflect.DeepEqual(requirement.AffectedPaths, []string{sourcePath}) {
		t.Fatalf("header credential metadata = %#v", requirement)
	}
	for _, part := range contextParts {
		if !strings.Contains(requirement.Context, part) {
			t.Errorf("context %q does not contain %q", requirement.Context, part)
		}
	}
}

func brunoNativeMetadataHasBlocker(metadata map[string]any, want string) bool {
	if metadata == nil {
		return false
	}
	if blocker, _ := metadata["execution_blocker"].(string); blocker == want {
		return true
	}
	switch blockers := metadata["execution_blockers"].(type) {
	case []string:
		for _, blocker := range blockers {
			if blocker == want {
				return true
			}
		}
	case []any:
		for _, raw := range blockers {
			if blocker, _ := raw.(string); blocker == want {
				return true
			}
		}
	}
	return false
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
	for _, artifact := range []storage.ImportSourceArtifact{rawArtifact, sanitizedArtifact} {
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
	artifacts := []storage.ImportSourceArtifact{session.RawArtifact, session.SanitizedArtifact}

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
  url: 'https://example.test/{{secret.PASSWORD}}'
  body:
    type: xml
    data: "<password>{{password}}</password>"
`,
		},
	})
	candidate := inspection.Result.Scripts[0]
	preservedRequirements := 0
	for _, requirement := range candidate.SecretRequirements {
		if requirement.DestinationValuePreserved {
			preservedRequirements++
		}
	}
	if preservedRequirements != 1 {
		t.Fatalf("protected candidate requirement = %#v, want destination preservation evidence", candidate.SecretRequirements)
	}
	if strings.Contains(candidate.Script.Actions[0].Body, "source-password-value") {
		t.Fatalf("public native candidate disclosed its source credential: %q", candidate.Script.Actions[0].Body)
	}
	if candidate.Script.Actions[0].Target != "https://example.test/{{secret.PASSWORD}}" {
		t.Fatalf(
			"authored lookalike reference changed during inspection: target=%q",
			candidate.Script.Actions[0].Target,
		)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-protected-candidate")
	migrate := true
	unauthorized := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		MigrateSecrets:  &migrate,
	})
	if unauthorized.Code != http.StatusForbidden ||
		!strings.Contains(unauthorized.Body.String(), "source secret migration is not allowed") ||
		!strings.Contains(unauthorized.Body.String(), `"reason":"policy_disabled"`) {
		t.Fatalf("unauthorized native migration status = %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	if strings.Contains(unauthorized.Body.String(), "source-password-value") {
		t.Fatalf("unauthorized native migration disclosed the imported credential: %s", unauthorized.Body.String())
	}

	// The authorization failure is non-consuming. With migration omitted, the
	// protected source credential is restored into the runnable destination.
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:              brunoImportTestProject,
		ImportSessionID:        inspection.ImportSessionID,
		Mode:                   "create_native_batch",
		FolderID:               &folderID,
		AcknowledgedFindingIDs: requiredBrunoNativeFindingIDs(candidate),
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("native commit status = %d body=%s", response.Code, response.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, response)
	responseBody := created.Scripts[0].Actions[0].Body
	if strings.Contains(responseBody, "source-password-value") {
		t.Fatalf("commit response disclosed the imported credential: %q", responseBody)
	}
	stored, err := store.GetScript(candidate.PlannedScriptID)
	if err != nil {
		t.Fatalf("GetScript(%q): %v", candidate.PlannedScriptID, err)
	}
	body := stored.Actions[0].Body
	if body != "<password>source-password-value</password>" ||
		strings.Contains(body, "{{secret.") || strings.Contains(body, "[REDACTED]") {
		t.Fatalf("durable destination body = %q, want exact runnable imported body", body)
	}
	if stored.Actions[0].Target != "https://example.test/{{secret.PASSWORD}}" {
		t.Fatalf(
			"protected credential escaped its exact target: target=%q",
			stored.Actions[0].Target,
		)
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

func TestBrunoNativeBatchRollsBackPartialSecretMigrationAndRetries(t *testing.T) {
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
		partial.MigratedCount != 0 || len(partial.CommittedNames) != 0 ||
		strings.Join(partial.RemainingNames, ",") != "BRUNO_NATIVE_PARTIAL_A,BRUNO_NATIVE_PARTIAL_B" ||
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
	if value, _, err := underlyingSecrets.Get(context.Background(), brunoImportTestProject, "BRUNO_NATIVE_PARTIAL_A"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("rolled-back native secret = %q, err=%v", value, err)
	}

	retry := commitBrunoImportWithStatus(t, server, request)
	if retry.Code != http.StatusCreated || failingSecrets.createCalls < 3 {
		t.Fatalf("partial native retry status = %d calls=%d body=%s", retry.Code, failingSecrets.createCalls, retry.Body.String())
	}
}

func TestBrunoNativeBatchRollsBackSecretsWhenDatabaseCommitDefinitelyFails(t *testing.T) {
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
		partial.ScriptCommitted || len(partial.CommittedNames) != 0 ||
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
	for _, name := range expectedNames {
		if value, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, name); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatalf("definite native commit failure left secret %s = %q, %v", name, value, err)
		}
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
	inspected, _ := inspectBrunoNativeFilesWithBody(t, server, projectID, files)
	return inspected
}

func inspectBrunoNativeFilesWithBody(
	t *testing.T,
	server *Server,
	projectID string,
	files []brunoInspectJSONFile,
) (brunoInspectResponse, []byte) {
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
	return inspected, append([]byte(nil), response.Body.Bytes()...)
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
