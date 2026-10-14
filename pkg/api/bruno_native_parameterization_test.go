package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	brunoGeneratedMessageID = "3f2b8c1e-9a4d-4e7b-8c21-5d6e7f8a9b0c"
	brunoGeneratedTraceID   = "4bf92f3577b34da6"
)

func brunoGeneratedValueFiles() []brunoInspectJSONFile {
	request := func(name, uid string, seq int) string {
		return `info: {name: ` + name + `, uid: ` + uid + `, type: http, seq: ` + string(rune('0'+seq)) + `}
http:
  method: POST
  url: https://example.test/soap
  headers:
    - {name: idp-trace-id, value: "` + brunoGeneratedTraceID + `:a3ce929d0e0e4736:0:1"}
  body: {type: xml, data: "<Envelope><Header><ns2:messageId>` + brunoGeneratedMessageID + `</ns2:messageId><dateTimeStamp>2018-05-04T10:22:33.123Z</dateTimeStamp></Header></Envelope>"}
`
	}
	return []brunoInspectJSONFile{
		{Path: "soap/opencollection.yml", Content: "opencollection: 1.0.0\ninfo: {name: Generated values}\n"},
		{Path: "soap/inquire.yml", Content: request("Inquire", "inquire-uid", 1)},
		{Path: "soap/update.yml", Content: request("Update", "update-uid", 2)},
	}
}

// Trace and message IDs and the request time stamp a collection recorded are
// suggested per request, arrive selected, and on commit become parameters
// generated each iteration.
func TestBrunoNativeGeneratedValuesAreSuggestedAndAppliedOnCommit(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, brunoGeneratedValueFiles())
	if len(inspection.Result.Scripts) != 2 {
		t.Fatalf("candidates = %d, want 2", len(inspection.Result.Scripts))
	}
	selections := map[string][]string{}
	for _, candidate := range inspection.Result.Scripts {
		if candidate.Parameterization == nil {
			t.Fatalf("candidate %s has no parameter suggestions", candidate.SourcePath)
		}
		rules := map[string]int{}
		for _, suggestion := range candidate.Parameterization.Suggestions {
			if suggestion.Kind != paramize.KindGenerated {
				continue
			}
			rules[suggestion.RuleID]++
			if !suggestion.AutoApply {
				t.Errorf("%s %s arrived unselected: %s", candidate.SourcePath, suggestion.VarName, suggestion.Rationale)
			}
			if suggestion.LiteralValue != "" || suggestion.SampleValue != "" || suggestion.Parameter != nil {
				t.Errorf("suggestion crossed the response with server-side fields: %+v", suggestion)
			}
			if !strings.Contains(suggestion.Rationale, "each iteration") {
				t.Errorf("rationale = %q, want what replaces the value", suggestion.Rationale)
			}
			selections[candidate.CandidateID] = append(selections[candidate.CandidateID], suggestion.ID)
		}
		if rules["generated.uuid"] != 1 || rules["generated.hex"] != 2 || rules["generated.timestamp"] != 1 {
			t.Fatalf("%s generated rules = %v, want the message ID, two trace parts and the time stamp", candidate.SourcePath, rules)
		}
	}

	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-generated")
	base := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	}

	stale := base
	stale.ParameterizationSelections = map[string][]string{inspection.Result.Scripts[0].CandidateID: {"pz_000000000000000000000000"}}
	if response := commitBrunoImportWithStatus(t, server, stale); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "stale or unavailable") {
		t.Fatalf("stale selection status = %d body=%s", response.Code, response.Body.String())
	}
	unselected := base
	unselected.CandidateIDs = []string{inspection.Result.Scripts[0].CandidateID}
	unselected.ParameterizationSelections = map[string][]string{inspection.Result.Scripts[1].CandidateID: selections[inspection.Result.Scripts[1].CandidateID]}
	if response := commitBrunoImportWithStatus(t, server, unselected); response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "not being saved") {
		t.Fatalf("unselected candidate status = %d body=%s", response.Code, response.Body.String())
	}

	commit := base
	commit.ParameterizationSelections = selections
	created := commitBrunoImportWithStatus(t, server, commit)
	if created.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", created.Code, created.Body.String())
	}
	batch := decodeBrunoNativeBatchTestResponse(t, created)
	for _, saved := range batch.Scripts {
		stored, err := store.GetScript(saved.ID)
		if err != nil {
			t.Fatalf("GetScript(%q): %v", saved.ID, err)
		}
		action := stored.Actions[0]
		if strings.Contains(action.Body, brunoGeneratedMessageID) || strings.Contains(action.Body, "2018-05-04") {
			t.Errorf("%s body still sends recorded values: %s", stored.Name, action.Body)
		}
		trace := ""
		for _, header := range action.HeaderList {
			if strings.EqualFold(header.Name, "idp-trace-id") {
				trace = header.Value
			}
		}
		if strings.Contains(trace, brunoGeneratedTraceID) || !strings.HasSuffix(trace, ":0:1") {
			t.Errorf("%s trace header = %q, want generated IDs and the kept flags", stored.Name, trace)
		}
		types := map[string]int{}
		for _, param := range stored.Parameters {
			types[param.Type]++
		}
		if types["uuid"] != 1 || types["random_string"] != 2 || types["date_time"] != 1 {
			t.Errorf("%s parameters = %+v", stored.Name, stored.Parameters)
		}
	}

	replay := commitBrunoImportWithStatus(t, server, commit)
	if replay.Code != http.StatusOK || !decodeBrunoNativeBatchTestResponse(t, replay).IdempotentReplay {
		t.Fatalf("replay status = %d body=%s", replay.Code, replay.Body.String())
	}
	fewer := commit
	fewer.ParameterizationSelections = map[string][]string{inspection.Result.Scripts[0].CandidateID: selections[inspection.Result.Scripts[0].CandidateID]}
	if response := commitBrunoImportWithStatus(t, server, fewer); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), storage.ErrBrunoNativeBatchConflict.Error()) {
		t.Fatalf("retry with other selections status = %d body=%s", response.Code, response.Body.String())
	}
}

// Leaving every suggestion unselected saves the recorded values, as before.
func TestBrunoNativeCommitWithoutSelectionsKeepsRecordedValues(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, brunoGeneratedValueFiles())
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-recorded")
	created := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", created.Code, created.Body.String())
	}
	for _, saved := range decodeBrunoNativeBatchTestResponse(t, created).Scripts {
		stored, err := store.GetScript(saved.ID)
		if err != nil {
			t.Fatalf("GetScript(%q): %v", saved.ID, err)
		}
		if !strings.Contains(stored.Actions[0].Body, brunoGeneratedMessageID) || len(stored.Parameters) != 0 {
			t.Errorf("%s changed without a selection: body=%s parameters=%+v", stored.Name, stored.Actions[0].Body, stored.Parameters)
		}
	}
}

// A message ID before a body credential in the same SOAP envelope: the
// generated value must not move the credential's reviewed location.
func TestBrunoNativeGeneratedValuesBesideBodyCredentials(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{Path: "soap/opencollection.yml", Content: "opencollection: 1.0.0\ninfo: {name: Generated beside credentials}\n"},
		{Path: "soap/inquire.yml", Content: `info: {name: Inquire, uid: inquire-credential-uid, type: http}
http:
  method: POST
  url: https://example.test/soap
  body: {type: xml, data: "<Envelope><Header><messageId>` + brunoGeneratedMessageID + `</messageId><SecurityMessageHeader><partnerUserName>{{partnerUserName}}</partnerUserName><partnerPassword>{{partnerPassword}}</partnerPassword></SecurityMessageHeader></Header></Envelope>"}
`},
	})
	if len(inspection.Result.SecretRequirements) == 0 {
		t.Fatalf("expected body credential requirements: %#v", inspection.Result.SecretRequirements)
	}
	candidate := inspection.Result.Scripts[0]
	var ids []string
	for _, suggestion := range candidate.Parameterization.Suggestions {
		if suggestion.Kind == paramize.KindGenerated {
			ids = append(ids, suggestion.ID)
		}
	}
	if len(ids) != 1 {
		t.Fatalf("generated suggestions = %#v", candidate.Parameterization)
	}
	folderID := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "folder-bruno-generated-credentials")
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:                  brunoImportTestProject,
		ImportSessionID:            inspection.ImportSessionID,
		Mode:                       "create_native_batch",
		FolderID:                   &folderID,
		AcknowledgedFindingIDs:     requiredBrunoNativeFindingIDs(candidate),
		ParameterizationSelections: map[string][]string{candidate.CandidateID: ids},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	saved := decodeBrunoNativeBatchTestResponse(t, response).Scripts[0]
	stored, err := store.GetScript(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := stored.Actions[0].Body
	if strings.Contains(body, brunoGeneratedMessageID) || !strings.Contains(body, "<messageId>{{var.") {
		t.Errorf("message ID was not generated: %s", body)
	}
	for _, requirement := range inspection.Result.SecretRequirements {
		if !strings.Contains(body, requirement.Replacement) {
			t.Errorf("credential %q lost its reference: %s", requirement.Replacement, body)
		}
	}
}
