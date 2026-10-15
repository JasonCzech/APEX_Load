package browser

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func testRecordingEnvironment() *RecordingEnvironment {
	width, height, scale, mobile, touch := 820, 1180, 2.0, true, true
	return &RecordingEnvironment{Version: 1, ObservationSource: RecordingObservationLocal, Engine: "chromium", DeviceProfile: "ipad_air", ProfileBrowserFamily: "webkit", Emulated: true, AppliedContext: &RecordingAppliedContext{ViewportWidth: &width, ViewportHeight: &height, DeviceScaleFactor: &scale, Mobile: &mobile, HasTouch: &touch}, PhysicalDeviceQualification: RecordingPhysicalUnverified}
}

func TestRecordingEnvironmentNormalizationAndDetachedRoundtrip(t *testing.T) {
	original := testRecordingEnvironment()
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var claim map[string]any
	if err := json.Unmarshal(body, &claim); err != nil {
		t.Fatal(err)
	}
	claim["physical_device_qualification"] = "verified"
	claim["browser_version"] = "149.0.7827.55"
	claim["user_agent"] = strings.Repeat("private-claim", 1000)
	claim["unknown"] = claim // Unknown recursive data is never traversed.
	got := NormalizeRecordingEnvironment(claim, RecordingObservationAgent)
	if got == nil || got.ObservationSource != RecordingObservationAgent || got.PhysicalDeviceQualification != RecordingPhysicalUnverified || got.Engine != "chromium" || got.ProfileBrowserFamily != "webkit" {
		t.Fatal("bridge normalization lost actual engine versus preset intent distinction")
	}
	if got.BrowserVersion != "149.0.7827.55" || got.RecordedBrowserVersion() != "149.0.7827.55" {
		t.Fatalf("browser version = %q", got.BrowserVersion)
	}
	got.Engine = "firefox"
	*got.AppliedContext.ViewportWidth = 1
	if original.Engine != "chromium" || *original.AppliedContext.ViewportWidth != 820 {
		t.Fatal("normalization aliased caller receipt")
	}
	clone := original.Clone()
	*clone.AppliedContext.HasTouch = false
	if !*original.AppliedContext.HasTouch {
		t.Fatal("clone aliased optional context pointer")
	}
	unknown := NormalizeRecordingEnvironment(&RecordingEnvironment{Version: 1, ObservationSource: RecordingObservationLocal, Engine: "chromium"}, "")
	if unknown == nil || unknown.AppliedContext != nil || unknown.Emulated {
		t.Fatal("default receipt guessed emulation/context")
	}
	falseValue := false
	unknown.AppliedContext = &RecordingAppliedContext{Mobile: &falseValue, HasTouch: &falseValue}
	got = NormalizeRecordingEnvironment(unknown, "")
	if got == nil || got.AppliedContext == nil || got.AppliedContext.Mobile == nil || *got.AppliedContext.Mobile {
		t.Fatal("explicit false collapsed into unknown")
	}
}

func TestRecordingEnvironmentRejectsMalformedAndResourceClaims(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RecordingEnvironment)
	}{
		{"unknown version", func(v *RecordingEnvironment) { v.Version = 2 }},
		{"unknown engine", func(v *RecordingEnvironment) { v.Engine = "physical-ipad" }},
		{"unknown source", func(v *RecordingEnvironment) { v.ObservationSource = "controller_verified" }},
		{"oversized profile", func(v *RecordingEnvironment) { v.DeviceProfile = strings.Repeat("x", 1<<20) }},
		{"unknown profile", func(v *RecordingEnvironment) { v.DeviceProfile = "physical_ipad" }},
		{"contradictory emulation", func(v *RecordingEnvironment) { v.Emulated = false }},
		{"family mismatch", func(v *RecordingEnvironment) { v.ProfileBrowserFamily = "chromium" }},
		{"partial viewport", func(v *RecordingEnvironment) { v.AppliedContext.ViewportHeight = nil }},
		{"viewport bound", func(v *RecordingEnvironment) { *v.AppliedContext.ViewportWidth = 8193 }},
		{"scaled pixels", func(v *RecordingEnvironment) {
			*v.AppliedContext.ViewportWidth = 4096
			*v.AppliedContext.ViewportHeight = 4096
			*v.AppliedContext.DeviceScaleFactor = 4
		}},
		{"nonfinite scale", func(v *RecordingEnvironment) { *v.AppliedContext.DeviceScaleFactor = math.NaN() }},
		{"browser version text", func(v *RecordingEnvironment) { v.BrowserVersion = "HeadlessChrome/149.0" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := testRecordingEnvironment()
			tc.mutate(v)
			if NormalizeRecordingEnvironment(v, "") != nil {
				t.Fatal("malformed receipt admitted")
			}
		})
	}
	for _, value := range []any{json.Number(strings.Repeat("1", 1<<20)), json.Number("1.5"), "1", true} {
		if NormalizeRecordingEnvironment(map[string]any{"version": value, "engine": "chromium", "emulated": false, "observation_source": RecordingObservationLocal}, "") != nil {
			t.Fatal("invalid numeric version admitted")
		}
	}
	claim := map[string]any{"version": 1, "engine": "chromium", "emulated": false, "observation_source": RecordingObservationLocal, "applied_context": map[string]any{"device_scale_factor": json.Number(strings.Repeat("1", 1<<20))}}
	if NormalizeRecordingEnvironment(claim, "") != nil {
		t.Fatal("oversized scale admitted")
	}
	if NormalizeRecordingEnvironment(testRecordingEnvironment(), RecordingObservationLocal) != nil {
		t.Fatal("claim upgraded to local observation")
	}
}

func TestRecordingEnvironmentConversionPreservesIntentAcrossSplitOutputs(t *testing.T) {
	session := recordedSession()
	session.DeviceProfile = "ipad_air"
	session.RecordingEnvironment = testRecordingEnvironment()
	session.Markers = []Marker{{Kind: MarkerTransactionStart, Name: "Login", Timestamp: ts(500)}, {Kind: MarkerTransactionEnd, Name: "Login", Timestamp: ts(1500)}, {Kind: MarkerTransactionStart, Name: "Checkout", Timestamp: ts(2500)}, {Kind: MarkerTransactionEnd, Name: "Checkout", Timestamp: ts(6500)}}
	for _, split := range []bool{false, true} {
		result := Convert(session, ConvertOptions{SplitAtTransactions: split})
		if split && len(result.Scripts) != 2 {
			t.Fatal("expected two transaction outputs")
		}
		for _, script := range result.Scripts {
			receipt := NormalizeRecordingEnvironment(script.Metadata.Import.Preserved[RecordingEnvironmentKey], "")
			if receipt == nil || receipt.Engine != "chromium" || script.Config.Browser.Engine != "webkit" || !reflect.DeepEqual(receipt, session.RecordingEnvironment) {
				t.Fatal("output confused observed Chromium with configured WebKit intent")
			}
			*receipt.AppliedContext.Mobile = false
			stored := script.Metadata.Import.Preserved[RecordingEnvironmentKey].(*RecordingEnvironment)
			if !*stored.AppliedContext.Mobile {
				t.Fatal("output receipt aliases normalized reader")
			}
		}
	}
	clone := session.Clone()
	*clone.RecordingEnvironment.AppliedContext.ViewportWidth = 1
	if *session.RecordingEnvironment.AppliedContext.ViewportWidth != 820 {
		t.Fatal("session snapshot receipt not detached")
	}
	session.RecordingEnvironment = nil
	legacy := Convert(session, ConvertOptions{}).Script
	if legacy.Metadata != nil && legacy.Metadata.Import != nil {
		if _, exists := legacy.Metadata.Import.Preserved[RecordingEnvironmentKey]; exists {
			t.Fatal("legacy profile fabricated launch observation")
		}
	}
}

func TestAttachRecordingEnvironmentPreservesHTTPImportReview(t *testing.T) {
	review := map[string]any{"safe_count": 3}
	script := &dsl.Script{Protocol: "http", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "salesforce_har", SourceScriptName: "Synthetic HAR", Preserved: map[string]any{"salesforce_review": review, "other": true}}}}
	AttachRecordingEnvironment(script, testRecordingEnvironment())
	if script.Metadata.Import.Source != "salesforce_har" || !reflect.DeepEqual(script.Metadata.Import.Preserved["salesforce_review"], review) || script.Metadata.Import.SourceScriptName != "Synthetic HAR" {
		t.Fatal("receipt replaced HTTP source or review")
	}
	AttachRecordingEnvironment(script, nil)
	if len(script.Metadata.Import.Preserved) != 2 {
		t.Fatal("clearing unsupported receipt removed other provenance")
	}
}

func TestRecorderFailedAdmissionCreatesNoEnvironment(t *testing.T) {
	device := &DeviceEmulation{Name: "ipad_air", Width: 820, Height: 1180, DeviceScaleFactor: 2, Mobile: true, HasTouch: true}
	recorder := New(Options{StartURL: "file:///unsupported", Device: device})
	device.Name = "pixel_8"
	device.Width = 1
	if recorder.opts.Device.Name != "ipad_air" || recorder.opts.Device.Width != 820 {
		t.Fatal("caller changed launch options after construction")
	}
	if err := recorder.Start(); err == nil {
		t.Fatal("unsupported start URL admitted")
	}
	if recorder.Snapshot().RecordingEnvironment != nil {
		t.Fatal("requested settings fabricated an observation after failed admission")
	}
}
