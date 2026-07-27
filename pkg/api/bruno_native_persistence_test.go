package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
)

func TestPreparePersistentBrunoNativeScriptPreservesXMLVerbatim(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?><soap:Envelope xmlns:soap="urn:soap"><soap:Header>` +
		`<messageId>AbCdEf0123456789GhIjKlMnOpQrStUv</messageId>` +
		`<password>{{secret.soap_password}}</password></soap:Header><soap:Body><run keep="exact"/></soap:Body></soap:Envelope>`
	script := &dsl.Script{
		ID: "native-verbatim", Name: "Native verbatim", Version: "1", Protocol: "http",
		GeneratedBy: brunoNativeImporterIdentity,
		Actions: []dsl.Action{{
			Name: "SOAP request", Type: "POST", Method: http.MethodPost, Target: "https://example.test/soap",
			Body: body,
			Metadata: map[string]any{
				"source_profile": "bruno_native",
				"source_path":    "service/request.yml",
			},
		}},
	}

	projected, err := preparePersistentBrunoNativeScript(script)
	if err != nil {
		t.Fatalf("prepare native script: %v", err)
	}
	if got := projected.Actions[0].Body; got != body {
		t.Fatalf("native XML body changed:\n got: %q\nwant: %q", got, body)
	}
	if script.Actions[0].Body != body {
		t.Fatal("native persistence projection mutated its input")
	}
}

func TestPreparePersistentBrunoNativeScriptRejectsUngovernedXMLCredential(t *testing.T) {
	script := &dsl.Script{
		ID: "native-inline-secret", Name: "Native inline secret", Version: "1", Protocol: "http",
		GeneratedBy: brunoNativeImporterIdentity,
		Actions: []dsl.Action{{
			Name: "SOAP request", Type: "POST", Method: http.MethodPost, Target: "https://example.test/soap",
			Body: `<Envelope><password>source-password-must-not-persist</password></Envelope>`,
			Metadata: map[string]any{
				"source_profile": "bruno_native",
			},
		}},
	}

	if _, err := preparePersistentBrunoNativeScript(script); err == nil ||
		!strings.Contains(err.Error(), "must use a governed") {
		t.Fatalf("prepare native script error = %v, want governed-secret rejection", err)
	}
}

func TestPreparePersistentBrunoNativeScriptRejectsUngovernedCredentialSuffix(t *testing.T) {
	script := &dsl.Script{
		ID: "native-suffixed-secret", Name: "Native suffixed secret", Version: "1", Protocol: "http",
		GeneratedBy: brunoNativeImporterIdentity,
		Actions: []dsl.Action{{
			Name: "SOAP request", Type: "POST", Method: http.MethodPost, Target: "https://example.test/soap",
			Body: `<Envelope><userPassword>source-password-must-not-persist</userPassword></Envelope>`,
			Metadata: map[string]any{
				"source_profile": "bruno_native",
			},
		}},
	}
	if _, err := preparePersistentBrunoNativeScript(script); err == nil ||
		!strings.Contains(err.Error(), "must use a governed") {
		t.Fatalf("prepare native script error = %v, want governed suffix rejection", err)
	}
}

func TestPreparePersistentBrunoNativeScriptAllowsGovernedCredentialContainer(t *testing.T) {
	script := &dsl.Script{
		ID: "native-credential-container", Name: "Native credential container", Version: "1", Protocol: "http",
		GeneratedBy: brunoNativeImporterIdentity,
		Actions: []dsl.Action{{
			Name: "SOAP request", Type: "POST", Method: http.MethodPost, Target: "https://example.test/soap",
			Body: `<Envelope><auth><password>{{secret.PASSWORD}}</password></auth></Envelope>`,
			Metadata: map[string]any{
				"source_profile": "bruno_native",
			},
		}},
	}
	if _, err := preparePersistentBrunoNativeScript(script); err != nil {
		t.Fatalf("prepare governed credential container: %v", err)
	}
}

func TestPreparePersistentBrunoNativeScriptPreservesSafeCompactProvenance(t *testing.T) {
	const findingID = "bruno-native-0123456789abcdef01234567"
	script := &dsl.Script{
		ID: "native-provenance", Name: "Native provenance", Version: "1", Protocol: "http",
		GeneratedBy: brunoNativeImporterIdentity,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "bruno",
			Preserved: map[string]any{
				"effective_auth": "basic",
				"diagnostic_ids": []string{findingID},
				"finding_ids":    []string{findingID},
				"effective_fields": map[string]any{
					"auth_mode": "basic",
				},
			},
		}},
		Actions: []dsl.Action{{
			Name: "SOAP request", Type: "POST", Method: http.MethodPost, Target: "https://example.test/soap",
			Body: `<Envelope/>`,
			Metadata: map[string]any{
				"source_profile": "bruno_native",
			},
		}},
	}

	projected, err := preparePersistentBrunoNativeScript(script)
	if err != nil {
		t.Fatalf("prepare native script: %v", err)
	}
	preserved := projected.Metadata.Import.Preserved
	if preserved["effective_auth"] != "basic" {
		t.Fatalf("effective auth = %#v", preserved["effective_auth"])
	}
	if got, ok := preserved["diagnostic_ids"].([]string); !ok || len(got) != 1 || got[0] != findingID {
		t.Fatalf("diagnostic ids = %#v", preserved["diagnostic_ids"])
	}
	if got, ok := preserved["finding_ids"].([]string); !ok || len(got) != 1 || got[0] != findingID {
		t.Fatalf("finding ids = %#v", preserved["finding_ids"])
	}
	fields, ok := preserved["effective_fields"].(map[string]any)
	if !ok || fields["auth_mode"] != "basic" {
		t.Fatalf("effective fields = %#v", preserved["effective_fields"])
	}
}

func TestPreparePersistentBrunoNativeScriptDoesNotRestoreUntrustedProvenanceValues(t *testing.T) {
	script := &dsl.Script{
		ID: "native-provenance-untrusted", Name: "Native provenance untrusted", Version: "1", Protocol: "http",
		GeneratedBy: brunoNativeImporterIdentity,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "bruno",
			Preserved: map[string]any{
				"effective_auth": "source-password-must-not-persist",
				"diagnostic_ids": []string{"source-password-must-not-persist"},
				"finding_ids":    []string{"bruno-native-not-hex"},
				"effective_fields": map[string]any{
					"auth_mode": "source-password-must-not-persist",
				},
			},
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "GET", Method: http.MethodGet, Target: "https://example.test",
			Metadata: map[string]any{"source_profile": "bruno_native"},
		}},
	}

	projected, err := preparePersistentBrunoNativeScript(script)
	if err != nil {
		t.Fatalf("prepare native script: %v", err)
	}
	payload, err := json.Marshal(projected.Metadata.Import.Preserved)
	if err != nil {
		t.Fatalf("marshal projected provenance: %v", err)
	}
	if strings.Contains(string(payload), "source-password-must-not-persist") ||
		strings.Contains(string(payload), "bruno-native-not-hex") {
		t.Fatalf("untrusted provenance survived projection: %s", payload)
	}
}

func TestBrunoNativeFindingIDsPreservesEmptyList(t *testing.T) {
	ids, ok := brunoNativeFindingIDs([]any{})
	if !ok || ids == nil || len(ids) != 0 {
		t.Fatalf("empty finding IDs = %#v, %t; want a non-nil empty list", ids, ok)
	}
}

func TestBrunoNativeBatchRetryCanonicalizesImplicitSelection(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-semantic-retry")
	request := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	}
	createdResponse := commitBrunoImportWithStatus(t, server, request)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("native commit status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}

	retry := request
	retry.CandidateIDs = []string{
		inspection.Result.Scripts[1].CandidateID,
		inspection.Result.Scripts[0].CandidateID,
	}
	retry.NameOverrides = map[string]string{}
	retry.SecretBindings = map[string]string{}
	replayedResponse := commitBrunoImportWithStatus(t, server, retry)
	if replayedResponse.Code != http.StatusOK {
		t.Fatalf("semantic retry status = %d body=%s", replayedResponse.Code, replayedResponse.Body.String())
	}
	replayed := decodeBrunoNativeBatchTestResponse(t, replayedResponse)
	if !replayed.IdempotentReplay || replayed.Created || replayed.Count != 2 {
		t.Fatalf("semantic retry response = %#v", replayed)
	}
}

func TestBrunoNativePersistenceValidationOccursBeforeClaimAndSecretWrites(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Preclaim validation}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Preclaim validation, uid: preclaim-validation, type: http}
http:
  method: POST
  url: https://example.test/soap
  auth: {type: basic, username: source-user, password: source-password}
  body: {type: xml, data: "<Envelope><messageId>AbCdEf0123456789GhIjKlMnOpQrStUv</messageId></Envelope>"}
`,
		},
	})

	request := newTestBrunoRequest()
	protected, err := server.consumeBrunoImportSession(request, inspection.ImportSessionID, brunoImportTestProject)
	if err != nil {
		t.Fatalf("claim protected inspection fixture: %v", err)
	}
	protected.Result.Scripts[0].Script.Actions[0].Body =
		`<Envelope><password>unbound-source-value</password></Envelope>`
	if err := server.restoreClaimedBrunoImportSession(protected); err != nil {
		t.Fatalf("restore protected inspection fixture: %v", err)
	}

	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-preclaim")
	bindings := make(map[string]string, len(inspection.Result.SecretRequirements))
	for index, requirement := range inspection.Result.SecretRequirements {
		bindings[requirement.ID] = "BRUNO_PRECLAIM_" + string(rune('A'+index))
	}
	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
		SecretBindings:  bindings,
		MigrateSecrets:  &migrate,
	})
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "bruno_native_persistence_validation_failed") {
		t.Fatalf("preclaim validation status = %d body=%s", response.Code, response.Body.String())
	}
	for _, name := range bindings {
		if _, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, name); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatalf("secret provider was written before validation for %q: %v", name, err)
		}
	}
	retained, err := server.loadBrunoImportSession(newTestBrunoRequest(), inspection.ImportSessionID, brunoImportTestProject)
	if err != nil {
		t.Fatalf("preclaim validation consumed session: %v", err)
	}
	clearBrunoImportSession(retained)
}

func TestBrunoNativeBatchRejectsTrulyExpiredPersistedSession(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-native-real-expiry")
	descriptorPath := filepath.Join(artifactDir, ".sessions", inspection.ImportSessionID+".json")
	data, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read protected session descriptor: %v", err)
	}
	var descriptor importartifact.SessionDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		t.Fatalf("decode protected session descriptor: %v", err)
	}
	descriptor.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	data, err = json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode protected session descriptor: %v", err)
	}
	if err := os.WriteFile(descriptorPath, data, 0o600); err != nil {
		t.Fatalf("expire protected session descriptor: %v", err)
	}

	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "expired") {
		t.Fatalf("expired persisted session status = %d body=%s", response.Code, response.Body.String())
	}
	scripts, err := store.ListScripts(brunoImportTestProject, nil)
	if err != nil {
		t.Fatalf("list scripts after expired session: %v", err)
	}
	if len(scripts) != 0 {
		t.Fatalf("expired session persisted scripts: %#v", scripts)
	}
}

func newTestBrunoRequest() *http.Request {
	return (&http.Request{Header: make(http.Header)}).WithContext(context.Background())
}
