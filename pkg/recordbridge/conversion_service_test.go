package recordbridge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

func conversionTestService() *Service {
	return &Service{Conversion: ConversionPorts{
		Drafts: NewMemoryDraftPorts(nil, nil), WriteImportResponse: WriteMemoryDraftResponse,
		ProjectRecordingProfile: func(string, string) (*recordingprofile.Profile, error) { return nil, nil },
	}}
}
func recordingConversionFixture() (*recbrowser.Session, *BrowserRecordingSession) {
	artifact := &recbrowser.Session{ID: "recording-a", StartURL: "https://example.test", Steps: []recbrowser.Step{{Seq: 1, Kind: recbrowser.StepNavigate, URL: "https://example.test/orders"}}}
	info := &BrowserRecordingSession{ID: artifact.ID, Name: "Orders", ProjectID: "project-a", Status: BrowserRecordingStopped, final: artifact}
	return artifact, info
}

func TestSharedRecordingPreparationFencesReviewedOutputsBeforePersistence(t *testing.T) {
	service := conversionTestService()
	artifact, info := recordingConversionFixture()
	prepared, err := service.PrepareBrowserRecordingConversion(artifact, info, BrowserRecordingConvertRequest{ExpectedScriptCount: 1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Scripts) != 1 || len(prepared.TransactionScripts) != 1 {
		t.Fatalf("outputs=%#v", prepared)
	}
	script := prepared.Scripts[0]
	if script.ProjectID != "project-a" || script.Metadata.Import.Source != "browser_recorder" || script.Metadata.Import.Preserved["recorder_session_id"] != "recording-a" {
		t.Fatalf("conversion provenance changed: %#v", script)
	}
	if script.Version == "" || len(script.Actions) == 0 {
		t.Fatal("prepared output is not executable")
	}
	for _, tc := range []struct {
		name    string
		req     BrowserRecordingConvertRequest
		status  int
		message string
	}{
		{"review count", BrowserRecordingConvertRequest{ExpectedScriptCount: 2}, 409, "reviewed script(s)"},
		{"stale suggestion", BrowserRecordingConvertRequest{ParameterizationSelections: []BrowserRecordingParameterizationSelection{{ScriptIndex: 0, SelectedIDs: []string{"missing"}}}}, 409, "stale or invalid"},
		{"missing binding target", BrowserRecordingConvertRequest{SecretBindings: []BrowserRecordingSecretBinding{{ScriptIndex: 1, RequirementID: "missing", SecretName: "NAME"}}}, 409, "unavailable converted script"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := service.PrepareBrowserRecordingConversion(artifact, info, tc.req, false)
			if got != nil || err == nil || err.Status != tc.status || !strings.Contains(err.Message, tc.message) {
				t.Fatalf("result=%#v error=%v", got, err)
			}
		})
	}
}

func TestSharedRecordingPreviewRejectsSaveOnlyFieldsAndKeepsHTTPWarnings(t *testing.T) {
	service := conversionTestService()
	_, info := recordingConversionFixture()
	service.Recording = &BrowserRecordingManager{sessions: map[string]*BrowserRecordingSession{info.ID: info}}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"secret_bindings":[]}`, 400},
		{`{"also_create_http_script":true}`, 200},
	} {
		request := httptest.NewRequest(http.MethodPost, "/preview", strings.NewReader(tc.body))
		request.SetPathValue("id", info.ID)
		response := httptest.NewRecorder()
		service.HandlePreviewBrowserRecordingConversion(response, request)
		if response.Code != tc.want {
			t.Fatalf("body=%s status=%d response=%s", tc.body, response.Code, response.Body.String())
		}
		if tc.want == 200 && !strings.Contains(response.Body.String(), "no side-car HAR is available") {
			t.Fatal("HTTP generation warning disappeared")
		}
	}
}

func TestSharedDraftApplyReleasesRejectedMutationAndRotatesSuccessfulBinding(t *testing.T) {
	service := conversionTestService()
	draft := &dsl.Script{Name: "Login", Version: "1", Protocol: "http", ProjectID: "project-a", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Private", Type: "request", Method: "POST", Target: "/login", Headers: map[string]string{"Authorization": "Bearer original-recorded-token"}, Body: `{"password":"keep-original-password"}`}}}
	importreview.EnsureCredentialPreservingImportMetadata(draft, "browser_recorder")
	request := httptest.NewRequest(http.MethodPost, "/preview", nil)
	response := service.importResponse(request, importreview.ScriptImportResponseOptions{Script: draft})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var preview struct {
		Script       *dsl.Script                             `json:"script"`
		Requirements []importreview.SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err = json.Unmarshal(encoded, &preview); err != nil {
		t.Fatal(err)
	}
	oldToken := importreview.ImportDraftAttestationToken(preview.Script)
	if oldToken == "" {
		t.Fatalf("preview has no capability: %s", encoded)
	}
	var requirement importreview.SecretBindingRequirement
	for _, r := range preview.Requirements {
		if strings.Contains(r.Path, "Authorization") {
			requirement = r
		}
	}
	if requirement.ID == "" {
		t.Fatalf("missing requirement: %s", encoded)
	}
	call := func(handler func(http.ResponseWriter, *http.Request), payload any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		res := httptest.NewRecorder()
		handler(res, httptest.NewRequest(http.MethodPost, "/apply", bytes.NewReader(data)))
		return res
	}
	rejected := call(service.HandleApplyImportParameterization, ImportParameterizeApplyRequest{Script: preview.Script, SelectedIDs: []string{"stale"}})
	if rejected.Code != 409 {
		t.Fatalf("stale apply=%d %s", rejected.Code, rejected.Body.String())
	}
	applied := call(service.HandleApplyScriptSecretBindings, ApplySecretBindingsRequest{Script: preview.Script, Bindings: []importreview.SecretBindingSelection{{RequirementID: requirement.ID, SecretName: "AUTH_TOKEN"}}})
	if applied.Code != 200 {
		t.Fatalf("binding after rejection=%d %s", applied.Code, applied.Body.String())
	}
	var result struct {
		Script *dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(applied.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Script.Actions[0].Headers["Authorization"], "{{secret.AUTH_TOKEN}}") || result.Script.Actions[0].Body != draft.Actions[0].Body {
		t.Fatal("binding did not preserve the exact unselected body")
	}
	if token := importreview.ImportDraftAttestationToken(result.Script); token == "" || token == oldToken {
		t.Fatal("successful mutation did not rotate capability")
	}
	replay := call(service.HandleApplyScriptSecretBindings, ApplySecretBindingsRequest{Script: preview.Script})
	if replay.Code != 409 || !strings.Contains(replay.Body.String(), "import_draft_attestation_invalid") {
		t.Fatalf("old draft replay=%d %s", replay.Code, replay.Body.String())
	}
}

func TestSharedVerifyAdmissionAndReplayErrorWireContract(t *testing.T) {
	service := conversionTestService()
	service.Conversion.RequireExecutionLeader = func(w http.ResponseWriter) bool { WriteError(w, 503, "not leader"); return false }
	denied := httptest.NewRecorder()
	service.HandleVerifyBrowserRecording(denied, httptest.NewRequest(http.MethodPost, "/verify", nil))
	if denied.Code != 503 {
		t.Fatal("verify passed controller admission")
	}
	blocked := httptest.NewRecorder()
	service.writeReplayStartError(blocked, importreview.NewScriptReplayBlockedError([]string{"pending review"}))
	if blocked.Code != 409 || !strings.Contains(blocked.Body.String(), `"code":"script_replay_blocked"`) || !strings.Contains(blocked.Body.String(), `"issues":["pending review"]`) {
		t.Fatalf("review refusal changed: %d %s", blocked.Code, blocked.Body.String())
	}
}
