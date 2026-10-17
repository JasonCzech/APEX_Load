package recordbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

func TestStandaloneConversionRetainsSavedEnvelopeAndRetryOwnership(t *testing.T) {
	service := conversionTestService()
	configureStandaloneConversion(service)
	_, info := recordingConversionFixture()
	service.Recording = &BrowserRecordingManager{sessions: map[string]*BrowserRecordingSession{info.ID: info}}
	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader(body))
		r.SetPathValue("id", info.ID)
		w := httptest.NewRecorder()
		service.HandleConvertBrowserRecording(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder) *MemoryStoredScript {
		t.Helper()
		if w.Code != 201 {
			t.Fatalf("save status=%d body=%s", w.Code, w.Body.String())
		}
		var response struct {
			Script      *MemoryStoredScript   `json:"script"`
			Scripts     []*MemoryStoredScript `json:"scripts"`
			SecretStore map[string]any        `json:"secret_store"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Scripts) != 1 || response.Scripts[0].ID != response.Script.ID || response.SecretStore["provider"] != "unavailable" {
			t.Fatalf("saved envelope=%s", w.Body.String())
		}
		return response.Script
	}
	first := decode(call(`{"name":"Saved orders","expected_script_count":1}`))
	if first.ID == "" || first.RefKey == "" || first.AuthoringHealth == nil || first.Script.ID != first.ID || first.Script.Metadata.Import.Preserved[browserRecordingSaveProofKey] == "" {
		t.Fatalf("missing saved metadata: %#v", first)
	}
	if first.ID != importreview.DeterministicDirectImportAssetID(importreview.DirectImportOperationHash("project-a", "browser_recorder", "dev-mode\x00session:"+info.ID), "script", 0) {
		t.Fatal("conversion identity changed")
	}
	second := decode(call(`{"name":"Saved orders","expected_script_count":1}`))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("retry replaced saved content\nfirst=%+v\nsecond=%+v", first, second)
	}
	if got := call(`{"name":"Changed after save"}`); got.Code != http.StatusInternalServerError {
		t.Fatalf("changed retry=%d %s", got.Code, got.Body.String())
	}
	third := decode(call(`{"name":"Saved orders","expected_script_count":1}`))
	if !reflect.DeepEqual(first, third) {
		t.Fatal("conflicting retry mutated saved content")
	}
}

func TestStandaloneConversionRejectsFolderAndReviewMismatchWithoutSaving(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{{`{"folder_id":"folder"}`, 503}, {`{"expected_script_count":2}`, 409}, {`{"expected_script_count":-1}`, 400}, {`{"unknown":true}`, 400}} {
		t.Run(tc.body, func(t *testing.T) {
			service := conversionTestService()
			configureStandaloneConversion(service)
			_, info := recordingConversionFixture()
			service.Recording = &BrowserRecordingManager{sessions: map[string]*BrowserRecordingSession{info.ID: info}}
			r := httptest.NewRequest(http.MethodPost, "/convert", strings.NewReader(tc.body))
			r.SetPathValue("id", info.ID)
			w := httptest.NewRecorder()
			service.HandleConvertBrowserRecording(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(service.Library.scripts) != 0 || service.Library.sequence != 0 {
				t.Fatal("failed preparation published a script")
			}
		})
	}
}

func TestMemoryLibraryConflictingBatchIsAtomicAndResponsesDetached(t *testing.T) {
	script := func(id, name string) *dsl.Script {
		return &dsl.Script{ID: id, Name: name, Version: "1", Protocol: "browser", ProjectID: "p", Actions: []dsl.Action{{Type: "browser_navigate", Target: "https://example.test"}}, Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "browser_recorder"}}}
	}
	library := &MemoryScriptLibrary{}
	original := script("existing", "Original")
	saved, err := library.save([]*dsl.Script{original})
	if err != nil {
		t.Fatal(err)
	}
	saved[0].Script.Name = "Mutated response"
	if library.scripts["existing"].Script.Name != "Original" {
		t.Fatal("response shared retained definition")
	}
	if _, err = library.save([]*dsl.Script{script("new", "New"), script("existing", "Conflict")}); err == nil {
		t.Fatal("accepted conflicting batch")
	}
	if len(library.scripts) != 1 || library.sequence != 1 {
		t.Fatal("conflicting batch partially published")
	}
}
