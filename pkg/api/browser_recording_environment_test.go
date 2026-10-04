package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/storage"
)

func recordingEnvironmentFixture() *recbrowser.RecordingEnvironment {
	width, height, scale, mobile, touch := 820, 1180, 2.0, true, true
	return &recbrowser.RecordingEnvironment{Version: 1, ObservationSource: recbrowser.RecordingObservationLocal, Engine: "chromium", DeviceProfile: "ipad_air", ProfileBrowserFamily: "webkit", Emulated: true, AppliedContext: &recbrowser.RecordingAppliedContext{ViewportWidth: &width, ViewportHeight: &height, DeviceScaleFactor: &scale, Mobile: &mobile, HasTouch: &touch}, PhysicalDeviceQualification: recbrowser.RecordingPhysicalUnverified}
}

func assertRecordingEnvironment(t *testing.T, script *dsl.Script, source string) {
	t.Helper()
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatal("recording metadata missing")
	}
	got := recbrowser.NormalizeRecordingEnvironment(script.Metadata.Import.Preserved[recbrowser.RecordingEnvironmentKey], "")
	want := recordingEnvironmentFixture()
	want.ObservationSource = source
	if !reflect.DeepEqual(got, want) {
		t.Fatal("recording receipt lost engine, provenance, preset intent or applied context")
	}
}

func TestBrowserRecordingManagerReceiptSurvivesDetachedSnapshotsAndLateRecovery(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.RecordingEnvironment = recordingEnvironmentFixture()
	manager := NewBrowserRecordingManager(NewHub())
	fake := newFakeBrowserRecorder(artifact)
	fake.install(manager)
	start, err := manager.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/", DevicePreset: "ipad_air"})
	if err != nil {
		t.Fatal(err)
	}
	if start.RecordingEnvironment == nil || start.RecordingEnvironment.Engine != "chromium" || start.RecordingEnvironment.ProfileBrowserFamily != "webkit" {
		t.Fatal("start omitted successful backend observation")
	}
	*start.RecordingEnvironment.AppliedContext.ViewportWidth = 1
	*artifact.RecordingEnvironment.AppliedContext.ViewportWidth = 2
	listed := manager.List("")
	if len(listed) != 1 || *listed[0].RecordingEnvironment.AppliedContext.ViewportWidth != 820 {
		t.Fatal("summary receipt aliased caller/backend")
	}
	*listed[0].RecordingEnvironment.AppliedContext.Mobile = false
	full, ok := manager.Get(start.ID)
	if !ok || !*full.RecordingEnvironment.AppliedContext.Mobile {
		t.Fatal("list mutation changed manager observation")
	}
	detached, _, ok := manager.Artifact(start.ID)
	if !ok || *detached.RecordingEnvironment.AppliedContext.ViewportWidth != 820 {
		t.Fatal("artifact did not retain immutable observed context")
	}
	*detached.RecordingEnvironment.AppliedContext.ViewportWidth = 3
	stopped, ok := manager.Stop(start.ID)
	if !ok || *stopped.RecordingEnvironment.AppliedContext.ViewportWidth != 820 {
		t.Fatal("stop lost original context receipt")
	}
	safe := sanitizeBrowserRecordingSessionForResponse(stopped)
	if safe.RecordingEnvironment == nil {
		t.Fatal("viewer projection discarded safe receipt")
	}
	*safe.RecordingEnvironment.AppliedContext.HasTouch = false
	if !*stopped.RecordingEnvironment.AppliedContext.HasTouch {
		t.Fatal("viewer receipt shares pointers")
	}
	known := recordedBrowserArtifact()
	known.RecordingEnvironment = recordingEnvironmentFixture()
	late := recordedBrowserArtifact()
	late.RecordingEnvironment = &recbrowser.RecordingEnvironment{Version: 2, Engine: "firefox"}
	recovered := recoverLateBrowserRecordingArtifact(known, late)
	if !reflect.DeepEqual(recovered.RecordingEnvironment, known.RecordingEnvironment) || recovered == late {
		t.Fatal("late recovery lost detached initial observation")
	}
	*recovered.RecordingEnvironment.AppliedContext.ViewportWidth = 4
	if *known.RecordingEnvironment.AppliedContext.ViewportWidth != 820 {
		t.Fatal("recovery aliases earlier observation")
	}
}

func TestBrowserRecordingRequestedPresetDoesNotCreateObservation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy backend", true: "failed launch"}[failed], func(t *testing.T) {
			manager := NewBrowserRecordingManager(NewHub())
			fake := newFakeBrowserRecorder(nil)
			if failed {
				fake.startErr = errors.New("synthetic launch failure")
			}
			fake.install(manager)
			result, err := manager.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/", DevicePreset: "ipad_air"})
			if failed {
				if err == nil {
					t.Fatal("failed backend accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if result.RecordingEnvironment != nil {
					t.Fatal("requested preset fabricated observed engine")
				}
				defer manager.Stop(result.ID)
			}
			listed := manager.List("")
			if len(listed) != 1 || listed[0].DeviceProfile != "ipad_air" || listed[0].RecordingEnvironment != nil {
				t.Fatal("requested profile became launch evidence")
			}
		})
	}
}

func TestRecordedHTTPSiblingReceiptKeepsSalesforceReviewAndSource(t *testing.T) {
	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(salesforceHARImportJSON(t), &payload); err != nil {
		t.Fatal(err)
	}
	artifact := &recbrowser.Session{HAR: []byte(payload.Content), RecordingEnvironment: recordingEnvironmentFixture()}
	script, _, _, err := (&Server{}).createHTTPScriptFromHAR(artifact, &BrowserRecordingSession{Name: "Synthetic recording", ProjectID: "project-aura"}, "")
	if err != nil {
		t.Fatal(err)
	}
	assertRecordingEnvironment(t, script, recbrowser.RecordingObservationLocal)
	if script.GeneratedBy != "apexload-salesforce-builder" || script.Metadata.Import.Source == "browser_recorder" || script.Metadata.Import.Preserved["salesforce_review"] == nil {
		t.Fatal("environment attachment replaced native HTTP provenance/review")
	}
	if script.Metadata.Import.Preserved["source_review"] != nil {
		t.Fatal("receipt fabricated recorder HAR source links")
	}
}

func TestBridgedRecordingReceiptIsAgentReportedAndDurable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		claim any
		want  bool
	}{{"valid local claim", recordingEnvironmentFixture(), true}, {"unsupported version", map[string]any{"version": 2, "observation_source": "local_recorder", "engine": "chromium", "emulated": false}, false}, {"large profile", map[string]any{"version": 1, "observation_source": "local_recorder", "engine": "chromium", "emulated": true, "device_profile": strings.Repeat("x", 1<<20)}, false}, {"absent", nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			script := dsl.Script{Name: "Synthetic bridged receipt", Version: "1", Protocol: "http", ProjectID: storage.DefaultProjectID, Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}, Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{recbrowser.RecordingEnvironmentKey: tc.claim}}}}
			if v, ok := tc.claim.(*recbrowser.RecordingEnvironment); ok {
				v.PhysicalDeviceQualification = "verified"
			}
			body, err := json.Marshal(map[string]any{"project_id": storage.DefaultProjectID, "scripts": []dsl.Script{script}})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/browser-recorder/attest", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("handoff status=%d", response.Code)
			}
			var payload struct {
				Scripts []dsl.Script `json:"scripts"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Scripts) != 1 {
				t.Fatal("handoff output missing")
			}
			preserved := payload.Scripts[0].Metadata.Import.Preserved
			if tc.want {
				assertRecordingEnvironment(t, &payload.Scripts[0], recbrowser.RecordingObservationAgent)
			} else if _, ok := preserved[recbrowser.RecordingEnvironmentKey]; ok {
				t.Fatal("malformed or absent claim retained")
			}
			if payload.Scripts[0].Metadata.Import.Source != "browser_recording_har" {
				t.Fatal("receipt changed handoff import profile")
			}
			if strings.Contains(response.Body.String(), "verified\"") && !strings.Contains(response.Body.String(), "unverified\"") {
				t.Fatal("handoff claims physical qualification")
			}
			createBody, _ := json.Marshal(map[string]any{"project_id": storage.DefaultProjectID, "script": payload.Scripts[0]})
			createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(createBody))
			createRequest.Header.Set("Content-Type", "application/json")
			created := httptest.NewRecorder()
			server.mux.ServeHTTP(created, createRequest)
			if created.Code != http.StatusCreated {
				t.Fatalf("attested save status=%d", created.Code)
			}
			var saved StoredScript
			if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			reopened, ok := server.runner.GetScript(saved.ID)
			if !ok {
				t.Fatal("saved handoff missing")
			}
			if tc.want {
				assertRecordingEnvironment(t, reopened.Script, recbrowser.RecordingObservationAgent)
			} else if _, exists := reopened.Script.Metadata.Import.Preserved[recbrowser.RecordingEnvironmentKey]; exists {
				t.Fatal("invalid receipt persisted")
			}
		})
	}
}
