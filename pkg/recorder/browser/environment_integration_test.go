package browser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// This checks workstation Chromium context emulation against its receipt. It
// does not qualify physical hardware or a target application's replay behavior.
func TestRecorderEnvironmentDescribesActualLoopbackContext(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration disabled in short mode")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Synthetic context</title><button>Ready</button>`))
	}))
	defer server.Close()
	for _, name := range []string{"", "ipad_air", "pixel_8"} {
		t.Run(name, func(t *testing.T) {
			var device *DeviceEmulation
			if name != "" {
				p, ok := browserprotocol.LookupDevice(name)
				if !ok {
					t.Fatal("missing synthetic test preset")
				}
				device = &DeviceEmulation{Name: p.Name, Width: p.Width, Height: p.Height, DeviceScaleFactor: p.DeviceScaleFactor, Mobile: p.Mobile, HasTouch: p.HasTouch, UserAgent: p.UserAgent}
			}
			recorder := New(Options{StartURL: server.URL, Headless: true, Device: device})
			if err := recorder.Start(); err != nil {
				if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
					t.Fatalf("required loopback recorder failed: %v", err)
				}
				t.Skipf("browser runtime unavailable: %v", err)
			}
			defer func() { _, _ = recorder.Stop() }()
			receipt := recorder.Snapshot().RecordingEnvironment
			if receipt == nil || receipt.Engine != recorder.browser.BrowserType().Name() || receipt.ObservationSource != RecordingObservationLocal || receipt.PhysicalDeviceQualification != RecordingPhysicalUnverified {
				t.Fatal("receipt does not describe successful actual browser launch")
			}
			if name == "" {
				if receipt.Emulated || receipt.AppliedContext != nil {
					t.Fatal("default context fabricated explicitly applied settings")
				}
			} else {
				page := recorder.context.Pages()[0]
				viewport := page.ViewportSize()
				if viewport == nil || receipt.AppliedContext == nil || *receipt.AppliedContext.ViewportWidth != viewport.Width || *receipt.AppliedContext.ViewportHeight != viewport.Height {
					t.Fatal("receipt differs from actual context viewport")
				}
				actual, err := page.Evaluate(`({scale:window.devicePixelRatio,touch:navigator.maxTouchPoints>0})`)
				if err != nil {
					t.Fatal(err)
				}
				fields, ok := actual.(map[string]interface{})
				if !ok {
					t.Fatal("unexpected synthetic context observation")
				}
				actualScale, scaleOK := recordingNumber(fields["scale"])
				if !scaleOK || actualScale != *receipt.AppliedContext.DeviceScaleFactor || fields["touch"] != *receipt.AppliedContext.HasTouch {
					t.Fatal("receipt differs from actual scale/touch context")
				}
			}
			stopped, err := recorder.Stop()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stopped.RecordingEnvironment, receipt) {
				t.Fatal("stop lost context receipt")
			}
			encoded, err := json.Marshal(stopped)
			if err != nil {
				t.Fatal(err)
			}
			var reopened Session
			if err := json.Unmarshal(encoded, &reopened); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reopened.RecordingEnvironment, receipt) {
				t.Fatal("artifact export/reopen lost receipt")
			}
			for _, script := range Convert(&reopened, ConvertOptions{}).Scripts {
				if NormalizeRecordingEnvironment(script.Metadata.Import.Preserved[RecordingEnvironmentKey], "") == nil {
					t.Fatal("exported artifact conversion lost receipt")
				}
			}
		})
	}
}
