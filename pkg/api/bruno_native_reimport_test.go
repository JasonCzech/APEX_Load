package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

type brunoNativeReimportTestResponse struct {
	brunoNativeBatchTestResponse
	UpdatedScriptIDs        []string `json:"updated_script_ids"`
	UnconfiguredSecretNames []string `json:"unconfigured_secret_names"`
}

func decodeBrunoNativeReimportResponse(t *testing.T, body []byte) brunoNativeReimportTestResponse {
	t.Helper()
	var decoded brunoNativeReimportTestResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func brunoNativeUpdateTargets(t *testing.T, inspection brunoInspectResponse) map[string]brunoNativeUpdateTarget {
	t.Helper()
	targets := make(map[string]brunoNativeUpdateTarget, len(inspection.Result.Scripts))
	for _, candidate := range inspection.Result.Scripts {
		if candidate.ExistingScript == nil {
			t.Fatalf("candidate %s has no existing script on re-import", candidate.SourcePath)
		}
		targets[candidate.CandidateID] = brunoNativeUpdateTarget{
			ScriptID:          candidate.ExistingScript.ScriptID,
			ExpectedUpdatedAt: candidate.ExistingScript.UpdatedAt,
		}
	}
	return targets
}

func TestBrunoNativeReimportUpdatesExistingScriptsAsNewVersion(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-reimport")

	first := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	for _, candidate := range first.Result.Scripts {
		if candidate.ExistingScript != nil {
			t.Fatalf("first inspection matched an existing script: %#v", candidate.ExistingScript)
		}
	}
	createdResponse := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: first.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("first commit status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, createdResponse)
	createdReplay := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: first.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	})
	if replayed := decodeBrunoNativeReimportResponse(t, createdReplay.Body.Bytes()); createdReplay.Code != http.StatusOK ||
		replayed.UpdatedScriptIDs == nil || len(replayed.UpdatedScriptIDs) != 0 {
		t.Fatalf("replay of a created batch = %d %s", createdReplay.Code, createdReplay.Body.String())
	}
	createdByID := make(map[string]string, len(created.Scripts))
	for _, script := range created.Scripts {
		createdByID[script.ID] = script.RefKey
	}

	second := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	for index, candidate := range second.Result.Scripts {
		existing := candidate.ExistingScript
		if existing == nil || existing.ScriptID != created.Scripts[index].ID || existing.Matches != 1 ||
			existing.Name != created.Scripts[index].Name || existing.FolderID != folderID || existing.UpdatedAt.IsZero() ||
			!existing.SameCollection || existing.EditedSinceImport {
			t.Fatalf("re-inspected candidate %s existing = %#v, created = %#v", candidate.SourcePath, existing, created.Scripts[index])
		}
	}
	updateResponse := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: second.ImportSessionID,
		Mode: "create_native_batch", UpdateTargets: brunoNativeUpdateTargets(t, second),
	})
	if updateResponse.Code != http.StatusCreated {
		t.Fatalf("update commit status = %d body=%s", updateResponse.Code, updateResponse.Body.String())
	}
	updated := decodeBrunoNativeReimportResponse(t, updateResponse.Body.Bytes())
	if len(updated.UpdatedScriptIDs) != 2 || len(updated.Scripts) != 2 {
		t.Fatalf("update response = %#v", updated)
	}
	replay := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: second.ImportSessionID,
		Mode: "create_native_batch", UpdateTargets: brunoNativeUpdateTargets(t, second),
	})
	if replay.Code != http.StatusOK {
		t.Fatalf("update replay status = %d body=%s", replay.Code, replay.Body.String())
	}
	if replayed := decodeBrunoNativeReimportResponse(t, replay.Body.Bytes()); len(replayed.UpdatedScriptIDs) != 2 {
		t.Fatalf("replay does not report the updated scripts: %#v", replayed)
	}
	for _, script := range updated.Scripts {
		previousKey, existed := createdByID[script.ID]
		if !existed {
			t.Fatalf("update created a new script %q", script.ID)
		}
		before, okBefore := refkey.Parse(previousKey)
		after, okAfter := refkey.Parse(script.RefKey)
		if !okBefore || !okAfter || after.Seq != before.Seq || after.Version != before.Version+1 {
			t.Fatalf("ref key %q -> %q, want the next version", previousKey, script.RefKey)
		}
		if script.FolderID == nil || *script.FolderID != folderID {
			t.Fatalf("updated script moved folders: %#v", script.FolderID)
		}
	}
	persisted, err := store.ListScripts(brunoImportTestProject, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 {
		t.Fatalf("project has %d scripts after re-import, want 2", len(persisted))
	}

	// A review based on an older updated_at must not overwrite a newer save.
	third := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	for _, candidate := range third.Result.Scripts {
		if candidate.ExistingScript == nil || candidate.ExistingScript.EditedSinceImport {
			t.Fatalf("script saved by the update import reads as edited: %#v", candidate.ExistingScript)
		}
	}
	stale := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: third.ImportSessionID,
		Mode: "create_native_batch", UpdateTargets: brunoNativeUpdateTargets(t, second),
	})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "bruno_native_update_target_stale") {
		t.Fatalf("stale update status = %d body=%s", stale.Code, stale.Body.String())
	}

	// Mixing rows: update one script, create a copy of the other.
	targets := brunoNativeUpdateTargets(t, third)
	copied := third.Result.Scripts[1].CandidateID
	delete(targets, copied)
	withoutFolder := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: third.ImportSessionID,
		Mode: "create_native_batch", UpdateTargets: targets,
	})
	if withoutFolder.Code != http.StatusBadRequest || !strings.Contains(withoutFolder.Body.String(), "folder_id is required") {
		t.Fatalf("mixed commit without folder status = %d body=%s", withoutFolder.Code, withoutFolder.Body.String())
	}
	mixed := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: third.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID, UpdateTargets: targets,
		NameOverrides: map[string]string{copied: "Zeta copy"},
	})
	if mixed.Code != http.StatusCreated {
		t.Fatalf("mixed commit status = %d body=%s", mixed.Code, mixed.Body.String())
	}
	mixedReplay := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: third.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID, UpdateTargets: targets,
		NameOverrides: map[string]string{copied: "Zeta copy"},
	})
	wantUpdated := []string{targets[third.Result.Scripts[0].CandidateID].ScriptID}
	if replayed := decodeBrunoNativeReimportResponse(t, mixedReplay.Body.Bytes()); mixedReplay.Code != http.StatusOK ||
		!slices.Equal(replayed.UpdatedScriptIDs, wantUpdated) {
		t.Fatalf("replay of a mixed batch = %d %s, want updated %v", mixedReplay.Code, mixedReplay.Body.String(), wantUpdated)
	}
	persisted, err = store.ListScripts(brunoImportTestProject, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 3 {
		t.Fatalf("project has %d scripts after one copy, want 3", len(persisted))
	}

	// The newest of several matching scripts is offered, and the count says so.
	fourth := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	if existing := fourth.Result.Scripts[1].ExistingScript; existing == nil || existing.Matches != 2 || existing.Name != "Zeta copy" {
		t.Fatalf("candidate with two earlier scripts = %#v", existing)
	}
}

func TestBrunoNativeUpdateTargetMustComeFromTheSameRequest(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-target")
	first := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	createdResponse := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: first.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("first commit status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	second := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	alpha, zeta := second.Result.Scripts[0], second.Result.Scripts[1]
	swapped := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: second.ImportSessionID,
		Mode: "create_native_batch", CandidateIDs: []string{alpha.CandidateID},
		UpdateTargets: map[string]brunoNativeUpdateTarget{alpha.CandidateID: {
			ScriptID: zeta.ExistingScript.ScriptID, ExpectedUpdatedAt: zeta.ExistingScript.UpdatedAt,
		}},
	})
	if swapped.Code != http.StatusConflict || !strings.Contains(swapped.Body.String(), "not imported from this request") {
		t.Fatalf("cross-request update status = %d body=%s", swapped.Code, swapped.Body.String())
	}
	unselected := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: second.ImportSessionID,
		Mode: "create_native_batch", CandidateIDs: []string{alpha.CandidateID}, FolderID: &folderID,
		UpdateTargets: map[string]brunoNativeUpdateTarget{zeta.CandidateID: {
			ScriptID: zeta.ExistingScript.ScriptID, ExpectedUpdatedAt: zeta.ExistingScript.UpdatedAt,
		}},
	})
	if unselected.Code != http.StatusConflict || !strings.Contains(unselected.Body.String(), "is not selected") {
		t.Fatalf("unselected update target status = %d body=%s", unselected.Code, unselected.Body.String())
	}
}

func invalidCookieBrunoNativeFiles() []brunoInspectJSONFile {
	return []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Cookie repair}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Cookie repair, type: http}
http:
  method: GET
  url: https://example.test/orders
  headers:
    - name: Accept
      value: text/xml
    - name: Cookie
      value: 'session=abc" -H accept https://example.test/orders'
`,
		},
	}
}

// invalidAPIKeyBrunoNativeFiles has a credential header whose imported value
// HTTP cannot carry. Unlike a cookie, it stays in the script, routed through
// credential review.
func invalidAPIKeyBrunoNativeFiles() []brunoInspectJSONFile {
	return []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: Key repair}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Key repair, type: http}
http:
  method: GET
  url: https://example.test/orders
  headers:
    - name: Accept
      value: text/xml
    - name: X-API-Key
      value: "key-123\x01 pasted text"
`,
		},
	}
}

func brunoNativeHeaderRepairFinding(t *testing.T, candidate brunoconvert.NativeScriptCandidate) brunoconvert.NativeDiagnostic {
	t.Helper()
	for _, diagnostic := range candidate.Diagnostics {
		if diagnostic.Code == brunoconvert.NativeHeaderRepairDiagnosticCode {
			return diagnostic
		}
	}
	t.Fatalf("candidate diagnostics = %#v, want an invalid-header finding", candidate.Diagnostics)
	return brunoconvert.NativeDiagnostic{}
}

func TestBrunoNativeInvalidCookieIsFlaggedAtInspectAndTrimmedAtCommit(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection, body := inspectBrunoNativeFilesWithBody(t, server, brunoImportTestProject, invalidCookieBrunoNativeFiles())
	candidate := inspection.Result.Scripts[0]
	finding := brunoNativeHeaderRepairFinding(t, candidate)
	if !finding.AcknowledgementRequired || finding.HeaderRepair == nil ||
		finding.HeaderRepair.HeaderName != "Cookie" || len(finding.HeaderRepair.Options) != 2 {
		t.Fatalf("invalid cookie finding = %#v", finding)
	}
	if strings.Contains(string(body), "session=abc") {
		t.Fatalf("inspection disclosed the cookie value: %s", body)
	}
	if !strings.Contains(string(body), `"invalid_header"`) {
		t.Fatalf("execution preflight does not report the invalid header: %s", body)
	}

	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-cookie")
	unreviewed := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	})
	if unreviewed.Code != http.StatusConflict || !strings.Contains(unreviewed.Body.String(), "bruno_native_review_acknowledgement_required") {
		t.Fatalf("unreviewed invalid header status = %d body=%s", unreviewed.Code, unreviewed.Body.String())
	}
	unknown := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
		HeaderRepairs: map[string]string{"bruno-native-unknown": "trim"},
	})
	if unknown.Code != http.StatusConflict || !strings.Contains(unknown.Body.String(), "stale or not selected") {
		t.Fatalf("unknown repair status = %d body=%s", unknown.Code, unknown.Body.String())
	}

	repaired := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
		HeaderRepairs: map[string]string{finding.ID: "trim"},
	})
	if repaired.Code != http.StatusCreated {
		t.Fatalf("repaired commit status = %d body=%s", repaired.Code, repaired.Body.String())
	}
	saved, err := store.GetScript(candidate.PlannedScriptID)
	if err != nil {
		t.Fatal(err)
	}
	var cookie string
	for _, header := range saved.Actions[0].HeaderList {
		if header.Name == "Cookie" {
			cookie = header.Value
		}
	}
	if cookie != "session=abc" {
		t.Fatalf("saved cookie = %q, want the text before the quote", cookie)
	}
}

// A cookie is not a credential, so an invalid one is left out of the
// candidate instead of reaching the shared pipeline, which would drop it and
// block replay. Removing it or acknowledging the finding keeps it out.
func TestBrunoNativeInvalidCookieIsLeftOutWhenRemovedOrAcknowledged(t *testing.T) {
	for _, test := range []struct {
		name   string
		repair string
	}{
		{name: "remove", repair: "remove"},
		{name: "acknowledge"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, store, _ := newBrunoImportAPITestServer(t)
			inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidCookieBrunoNativeFiles())
			candidate := inspection.Result.Scripts[0]
			finding := brunoNativeHeaderRepairFinding(t, candidate)
			if !finding.HeaderRepair.Omitted || finding.HeaderRepair.TrimmedValue != "" {
				t.Fatalf("cookie finding = %#v, want a left-out header with no value in the response", finding.HeaderRepair)
			}
			if scriptHasPromotionBlocker(candidate.Script) {
				t.Fatalf("left-out cookie blocked replay: %#v", candidate.Script.Metadata)
			}
			folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-cookie-"+test.name)
			request := brunoCommitRequest{
				ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
				Mode: "create_native_batch", FolderID: &folderID,
			}
			if test.repair != "" {
				request.HeaderRepairs = map[string]string{finding.ID: test.repair}
			} else {
				request.AcknowledgedFindingIDs = []string{finding.ID}
			}
			response := commitBrunoImportWithStatus(t, server, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
			}
			saved, err := store.GetScript(candidate.PlannedScriptID)
			if err != nil {
				t.Fatal(err)
			}
			for _, header := range saved.Actions[0].HeaderList {
				if header.Name == "Cookie" {
					t.Fatalf("saved headers = %#v, want no cookie", saved.Actions[0].HeaderList)
				}
			}
		})
	}
}

func TestBrunoNativeReimportFlagsEditedScriptsAndPathOnlyMatches(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-edited")
	first := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidCookieFreeBrunoNativeFiles("Orders API"))
	createdResponse := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: first.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	})
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("first commit status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, createdResponse)
	saved, err := store.GetScript(created.Scripts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Name = "Orders, tuned in the editor"
	saved.UpdatedAt = time.Now().UTC().Add(time.Second)
	if err := store.SaveScript(saved); err != nil {
		t.Fatal(err)
	}

	edited := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidCookieFreeBrunoNativeFiles("Orders API"))
	if existing := edited.Result.Scripts[0].ExistingScript; existing == nil || !existing.EditedSinceImport || !existing.SameCollection {
		t.Fatalf("edited script match = %#v", existing)
	}
	// Another collection with the same request path and no UIDs is offered,
	// but not as the same collection. Neither is one whose name is only a
	// folder name, which unrelated uploads can share.
	other := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidCookieFreeBrunoNativeFiles("Billing API"))
	if existing := other.Result.Scripts[0].ExistingScript; existing == nil || existing.SameCollection {
		t.Fatalf("path-only match = %#v", existing)
	}
	unnamed := invalidCookieFreeBrunoNativeFiles("Orders API")
	unnamed[0].Content = "opencollection: 1.0.0\ninfo: {uid: ''}\n"
	if result := inspectBrunoNativeFiles(t, server, brunoImportTestProject, unnamed); result.Result.Scripts[0].ExistingScript == nil ||
		result.Result.Scripts[0].ExistingScript.SameCollection {
		t.Fatalf("undeclared collection name match = %#v", result.Result.Scripts[0].ExistingScript)
	}
}

func invalidCookieFreeBrunoNativeFiles(collectionName string) []brunoInspectJSONFile {
	return []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: " + collectionName + "}\n",
		},
		{
			Path:    "demo/request.yml",
			Content: "info: {name: Orders, type: http}\nhttp: {method: GET, url: https://example.test/orders}\n",
		},
	}
}

func TestBrunoNativeCommitListsUnconfiguredSecretNames(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{Path: "demo/opencollection.yml", Content: "opencollection: 1.0.0\ninfo: {name: Unset secrets}\n"},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Unset secrets, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: basic, username: '{{username}}', password: '{{password}}'}
`,
		},
	})
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-unset")
	request := brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	}
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	decoded := decodeBrunoNativeReimportResponse(t, response.Body.Bytes())
	if len(decoded.UnconfiguredSecretNames) != len(inspection.Result.SecretRequirements) {
		t.Fatalf("unconfigured secret names = %#v, requirements = %#v", decoded.UnconfiguredSecretNames, inspection.Result.SecretRequirements)
	}
	for _, requirement := range inspection.Result.SecretRequirements {
		if !slices.Contains(decoded.UnconfiguredSecretNames, requirement.SuggestedName) {
			t.Fatalf("unconfigured secret names %#v lack %q", decoded.UnconfiguredSecretNames, requirement.SuggestedName)
		}
	}
	// A retried save answered from the receipt reports the same secrets.
	replay := commitBrunoImportWithStatus(t, server, request)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d body=%s", replay.Code, replay.Body.String())
	}
	if replayed := decodeBrunoNativeReimportResponse(t, replay.Body.Bytes()); !slices.Equal(replayed.UnconfiguredSecretNames, decoded.UnconfiguredSecretNames) {
		t.Fatalf("replayed unconfigured secret names = %#v, want %#v", replayed.UnconfiguredSecretNames, decoded.UnconfiguredSecretNames)
	}
}

func TestBrunoNativeHeaderRepairRejectsMigratedCredentialHeader(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidAPIKeyBrunoNativeFiles())
	finding := brunoNativeHeaderRepairFinding(t, inspection.Result.Scripts[0])
	if finding.HeaderRepair.Omitted || finding.HeaderRepair.HeaderName != "X-API-Key" {
		t.Fatalf("credential header finding = %#v, want an in-place repair", finding.HeaderRepair)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-key-migrate")
	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID, MigrateSecrets: &migrate,
		HeaderRepairs: map[string]string{finding.ID: "trim"},
	})
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "bruno_native_header_repair_failed") ||
		!strings.Contains(response.Body.String(), "moved into project secrets") {
		t.Fatalf("migrated repair status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBrunoNativeHeaderRepairAllowsMigrationWhenHeaderUsesExistingSecret(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	if _, err := server.projectSecretStore.Create(context.Background(), brunoImportTestProject, "API_KEY_PROD", "key-prod", "test"); err != nil {
		t.Fatal(err)
	}
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidAPIKeyBrunoNativeFiles())
	finding := brunoNativeHeaderRepairFinding(t, inspection.Result.Scripts[0])
	var keyRequirement string
	for _, requirement := range inspection.Result.SecretRequirements {
		if strings.Contains(strings.ToLower(requirement.Context+requirement.Key), "x-api-key") {
			keyRequirement = requirement.ID
		}
	}
	if keyRequirement == "" {
		t.Fatalf("no API key requirement in %#v", inspection.Result.SecretRequirements)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-key-existing")
	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID, MigrateSecrets: &migrate,
		SecretBindings: map[string]string{keyRequirement: "API_KEY_PROD"},
		HeaderRepairs:  map[string]string{finding.ID: "trim"},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("repair with an existing secret binding status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBrunoNativeHeaderRepairsApplyLastHeaderFirst(t *testing.T) {
	script := &dsl.Script{
		Name: "two bad headers", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "GET", Method: "GET", Target: "/orders",
			HeaderList: dsl.HeaderList{
				{Name: "Cookie", Value: `a=1" junk`},
				{Name: "Accept", Value: "text/xml"},
				{Name: "X-Note", Value: "first\nsecond"},
			},
		}},
	}
	diagnostics := brunoconvert.NativeHeaderRepairDiagnostics("candidate", "request.yml", script)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	candidate := brunoconvert.NativeScriptCandidate{CandidateID: "candidate", Diagnostics: diagnostics}
	planned, err := selectedBrunoNativeHeaderRepairs(
		[]brunoconvert.NativeScriptCandidate{candidate},
		map[string]string{diagnostics[0].ID: "remove", diagnostics[1].ID: "remove"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, repair := range planned["candidate"] {
		if _, err := brunoconvert.ApplyNativeHeaderRepair(script, repair.repair, repair.choice); err != nil {
			t.Fatalf("apply %#v: %v", repair.repair, err)
		}
	}
	if headers := script.Actions[0].HeaderList; len(headers) != 1 || headers[0].Name != "Accept" {
		t.Fatalf("headers after removing both = %#v", headers)
	}
}

// Left-out headers and a credential header fixed in place can share a
// position. The fix in place runs first and the left-out headers go back in
// source order.
func TestBrunoNativeHeaderRepairsRestoreSourceOrder(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{Path: "demo/opencollection.yml", Content: "opencollection: 1.0.0\ninfo: {name: Header order}\n"},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Header order, type: http}
http:
  method: GET
  url: https://example.test/orders
  headers:
    - {name: Accept, value: text/xml}
    - {name: Cookie, value: 'a=1" -H accept'}
    - {name: X-Note, value: "note\x01 pasted"}
    - {name: X-API-Key, value: "key-123\x01 pasted"}
    - {name: Accept-Language, value: en}
`,
		},
	})
	candidate := inspection.Result.Scripts[0]
	repairs := map[string]string{}
	for _, diagnostic := range candidate.Diagnostics {
		if diagnostic.HeaderRepair != nil {
			repairs[diagnostic.ID] = "trim"
		}
	}
	if len(repairs) != 3 {
		t.Fatalf("diagnostics = %#v, want three header repairs", candidate.Diagnostics)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-header-order")
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID, HeaderRepairs: repairs,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	saved, err := store.GetScript(candidate.PlannedScriptID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, header := range saved.Actions[0].HeaderList {
		got = append(got, header.Name+": "+header.Value)
	}
	want := []string{"Accept: text/xml", "Cookie: a=1", "X-Note: note", "X-API-Key: key-123", "Accept-Language: en"}
	if !slices.Equal(got, want) {
		t.Fatalf("saved headers = %q, want %q", got, want)
	}
}

// A left-out Content-Type is replaced by the body's default. Putting it back
// overwrites that default even when a header left out after it goes back at
// the same position.
func TestBrunoNativeHeaderRepairsReplaceTheBodyDefaultContentType(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{Path: "demo/opencollection.yml", Content: "opencollection: 1.0.0\ninfo: {name: Body default}\n"},
		{
			Path: "demo/request.yml",
			Content: `info: {name: Body default, type: http}
http:
  method: POST
  url: https://example.test/orders
  headers:
    - {name: Accept, value: text/xml}
    - {name: X-Before, value: "before\x01 pasted"}
    - {name: Content-Type, value: "application/vnd.orders+json\x01 pasted"}
    - {name: X-Note, value: "note\x01 pasted"}
  body: {type: json, data: '{"id": 1}'}
`,
		},
	})
	candidate := inspection.Result.Scripts[0]
	repairs := map[string]string{}
	replacesDefault := false
	for _, diagnostic := range candidate.Diagnostics {
		if diagnostic.HeaderRepair != nil {
			repairs[diagnostic.ID] = "trim"
			replacesDefault = replacesDefault ||
				(diagnostic.HeaderRepair.HeaderName == "Content-Type" && diagnostic.HeaderRepair.ReplacesBodyDefault)
		}
	}
	if len(repairs) != 3 || !replacesDefault {
		t.Fatalf("diagnostics = %#v, want three header repairs, the Content-Type one replacing the body default", candidate.Diagnostics)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-body-default")
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID, HeaderRepairs: repairs,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	saved, err := store.GetScript(candidate.PlannedScriptID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, header := range saved.Actions[0].HeaderList {
		got = append(got, header.Name+": "+header.Value)
	}
	want := []string{"Accept: text/xml", "X-Before: before", "Content-Type: application/vnd.orders+json", "X-Note: note"}
	if !slices.Equal(got, want) {
		t.Fatalf("saved headers = %q, want %q", got, want)
	}
}

func TestBrunoNativeReimportIgnoresUndeclaredNameOfExistingScript(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-undeclared")
	// The first collection declares no name, so it is named after its folder.
	undeclared := invalidCookieFreeBrunoNativeFiles("unused")
	undeclared[0].Content = "opencollection: 1.0.0\ninfo: {uid: ''}\n"
	first := inspectBrunoNativeFiles(t, server, brunoImportTestProject, undeclared)
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: first.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("first commit status = %d body=%s", response.Code, response.Body.String())
	}
	// An unrelated collection that declares the same name as that folder.
	declared := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidCookieFreeBrunoNativeFiles(first.Result.Collection.Name))
	if existing := declared.Result.Scripts[0].ExistingScript; existing == nil || existing.SameCollection {
		t.Fatalf("declared name matched a folder-derived name: %#v (collection %q)", existing, first.Result.Collection.Name)
	}
}

// scriptReadFailureStore fails script reads while every other storage
// capability, including the native batch store, still works.
type scriptReadFailureStore struct {
	*storage.SQLiteStore
}

func (scriptReadFailureStore) GetScript(string) (*dsl.Script, error) {
	return nil, errors.New("script authoring snapshot unavailable")
}

func TestBrunoNativeUpdateTargetReadFailureIsNotReportedAsStale(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-unreadable")
	first := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	if response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: first.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID,
	}); response.Code != http.StatusCreated {
		t.Fatalf("first commit status = %d body=%s", response.Code, response.Body.String())
	}
	second := inspectBrunoNativeFiles(t, server, brunoImportTestProject, orderedBrunoNativeFiles())
	server.store = scriptReadFailureStore{SQLiteStore: store}
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: second.ImportSessionID,
		Mode: "create_native_batch", UpdateTargets: brunoNativeUpdateTargets(t, second),
	})
	body := response.Body.String()
	if response.Code != http.StatusInternalServerError ||
		!strings.Contains(body, "bruno_native_update_target_unavailable") || !strings.Contains(body, `"ref"`) ||
		strings.Contains(body, "snapshot unavailable") {
		t.Fatalf("unreadable update target status = %d body=%s", response.Code, body)
	}
}

// statusFailingSecretStore fails every secret lookup.
type statusFailingSecretStore struct {
	secretstore.Store
}

func (statusFailingSecretStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	return "", secretstore.Metadata{}, errors.New("provider unavailable")
}

func TestBrunoNativeHeaderRepairFailsClosedWhenSecretStatusIsUnknown(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, invalidAPIKeyBrunoNativeFiles())
	finding := brunoNativeHeaderRepairFinding(t, inspection.Result.Scripts[0])
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-key-unknown")
	server.projectSecretStore = statusFailingSecretStore{Store: server.projectSecretStore}
	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID,
		Mode: "create_native_batch", FolderID: &folderID, MigrateSecrets: &migrate,
		HeaderRepairs: map[string]string{finding.ID: "trim"},
	})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "could not be checked") {
		t.Fatalf("unknown secret status repair = %d body=%s", response.Code, response.Body.String())
	}
}
