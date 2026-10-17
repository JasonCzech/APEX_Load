package recordbridge

import (
	"errors"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"reflect"
	"testing"
)

func recordingEnvironmentFixture() *recbrowser.RecordingEnvironment {
	width, height, scale, mobile, touch := 820, 1180, 2.0, true, true
	return &recbrowser.RecordingEnvironment{Version: 1, ObservationSource: recbrowser.RecordingObservationLocal, Engine: "chromium", DeviceProfile: "ipad_air", ProfileBrowserFamily: "webkit", Emulated: true, AppliedContext: &recbrowser.RecordingAppliedContext{ViewportWidth: &width, ViewportHeight: &height, DeviceScaleFactor: &scale, Mobile: &mobile, HasTouch: &touch}, PhysicalDeviceQualification: recbrowser.RecordingPhysicalUnverified}
}
func TestBrowserRecordingManagerReceiptSurvivesDetachedSnapshotsAndLateRecovery(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.RecordingEnvironment = recordingEnvironmentFixture()
	manager := NewBrowserRecordingManager(newTestEventSink())
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
	safe := SanitizeBrowserRecordingSessionForResponse(stopped)
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
			manager := NewBrowserRecordingManager(newTestEventSink())
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
