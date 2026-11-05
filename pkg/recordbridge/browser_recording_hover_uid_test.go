package recordbridge

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/selector"
)

// Hover steps are opt-in per recording: off unless the start request asks.
func TestBrowserRecordingManagerHoverCaptureIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  bool
	}{{name: "default off", req: false}, {name: "requested", req: true}} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewBrowserRecordingManager(newTestEventSink())
			fake := newFakeBrowserRecorder(nil)
			fake.install(m)
			session, err := m.Start(BrowserRecordingStartRequest{Name: "Hovers", StartURL: "https://shop.example/", CaptureHovers: tc.req})
			if err != nil {
				t.Fatalf("Start() error: %v", err)
			}
			fake.mu.Lock()
			got := fake.opts.CaptureHovers
			fake.mu.Unlock()
			if got != tc.req {
				t.Fatalf("recorder CaptureHovers = %v, want %v", got, tc.req)
			}
			if session.CaptureHovers != tc.req || session.summary().CaptureHovers != tc.req {
				t.Fatalf("session view capture_hovers = %v/%v, want %v", session.CaptureHovers, session.summary().CaptureHovers, tc.req)
			}
		})
	}
}

// A saved recording carries a stable identity on every step, so an edit that
// deletes or moves steps can match each kept step to its stored self.
func TestConvertBrowserRecordingStampsStepIdentity(t *testing.T) {
	field := &selector.ElementMetadata{Tag: "input", ID: "code", CSSPath: "#code"}
	start := time.UnixMilli(0)
	artifact := &recbrowser.Session{ID: "rec_uids", StartURL: "https://shop.example/", StartedAt: start}
	for index, value := range []string{"a", "b"} {
		artifact.Steps = append(artifact.Steps, recbrowser.Step{
			Seq: index + 1, Kind: recbrowser.StepClick, Value: value, Timestamp: start.Add(time.Duration(index+1) * 3 * time.Second),
			Element: field, Candidates: selector.Candidates(*field), URL: "https://shop.example/",
		})
	}
	info := &BrowserRecordingSession{ID: "rec_uids", Name: "UIDs", StartURL: "https://shop.example/"}
	result := (&Service{}).ConvertBrowserRecording(artifact, info, BrowserRecordingConvertRequest{})
	if result == nil || result.Script == nil {
		t.Fatal("conversion produced no script")
	}
	seen := map[string]bool{}
	for _, actions := range [][]dsl.Action{result.Script.Init, result.Script.Actions, result.Script.Teardown} {
		for _, action := range actions {
			uid, _ := action.Metadata[dsl.ImportActionUIDMetadataKey].(string)
			if !dsl.IsValidImportActionUID(uid) {
				t.Fatalf("action %q has import_action_uid %#v, want a stamped identity", action.Name, action.Metadata[dsl.ImportActionUIDMetadataKey])
			}
			if seen[uid] {
				t.Fatalf("import_action_uid %q repeats", uid)
			}
			seen[uid] = true
		}
	}
}
