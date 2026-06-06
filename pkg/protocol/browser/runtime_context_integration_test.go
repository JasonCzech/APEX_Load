package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserInitSupportsWebKitEngineOrReturnsInstallGuidance(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{
		"browser_engine": "webkit",
	})
	if err == nil {
		defer b.Teardown(context.Background())
		return
	}
	if !isWebKitRuntimeUnavailable(err) {
		t.Fatalf("Init error = %v, want WebKit install guidance or successful startup", err)
	}
}

func TestBrowserProtocolExecuteWebKitActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>WebKit Browser Test</title>
    <script>
      function submitForm() {
        const value = document.querySelector('#name').value;
        document.querySelector('#status').textContent = 'Saved ' + value;
      }
    </script>
  </head>
  <body>
    <input id="name" />
    <button id="submit" onclick="submitForm()">Save</button>
    <div id="status">Idle</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_engine":         "webkit",
		"browser_headless":       true,
		"browser_client_metrics": true,
		"browser_device_profile": "iphone_15_pro",
	}); err != nil {
		if isWebKitRuntimeUnavailable(err) {
			t.Skipf("webkit runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open homepage",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}
	if navigate.PageTitle != "WebKit Browser Test" {
		t.Fatalf("PageTitle = %q, want WebKit Browser Test", navigate.PageTitle)
	}

	fill, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Fill name",
		Type:     "browser_fill",
		Selector: "#name",
		Value:    "apexload",
	})
	if err != nil {
		t.Fatalf("fill returned error: %v", err)
	}
	if !fill.Success {
		t.Fatalf("fill failed: %s", fill.ErrorMessage)
	}

	tap, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Tap save",
		Type:     "browser_tap",
		Selector: "#submit",
	})
	if err != nil {
		t.Fatalf("tap returned error: %v", err)
	}
	if !tap.Success {
		t.Fatalf("tap failed: %s", tap.ErrorMessage)
	}

	wait, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Wait for saved state",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "Saved apexload",
			TimeoutMs: 3000,
		},
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if !wait.Success {
		t.Fatalf("wait failed: %s", wait.ErrorMessage)
	}
	if wait.ClientMetrics == nil {
		t.Fatal("ClientMetrics = nil, want WebKit page metrics")
	}
	if wait.ClientMetrics.TotalResourceCount == 0 {
		t.Fatal("TotalResourceCount = 0, want recorded resource metrics")
	}
}

func TestBrowserProtocolValidationMatrixAcrossRepresentativeDeviceProfiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Validation Matrix</title>
    <script>
      function submitForm() {
        const value = document.querySelector('#name').value;
        document.querySelector('#status').textContent = 'Saved ' + value;
      }
    </script>
  </head>
  <body>
    <label for="name">Name</label>
    <input id="name" />
    <button id="submit" onclick="submitForm()">Save</button>
    <div id="status">Idle</div>
  </body>
</html>`))
	}))
	defer server.Close()

	tests := []struct {
		name          string
		engine        string
		deviceProfile string
		actionType    string
	}{
		{name: "ChromiumDesktop", engine: "chromium", deviceProfile: "desktop_1080p", actionType: "browser_click"},
		{name: "ChromiumMobile", engine: "chromium", deviceProfile: "pixel_8", actionType: "browser_tap"},
		{name: "WebKitMobile", engine: "webkit", deviceProfile: "iphone_15_pro", actionType: "browser_tap"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			profile, ok := LookupDevice(tc.deviceProfile)
			if !ok {
				t.Fatalf("LookupDevice(%q) = false, want known profile", tc.deviceProfile)
			}

			b := &Browser{}
			err := b.Init(context.Background(), map[string]any{
				"base_url":               server.URL,
				"browser_engine":         tc.engine,
				"browser_headless":       true,
				"browser_client_metrics": true,
				"browser_device_profile": tc.deviceProfile,
			})
			if err != nil {
				lower := strings.ToLower(err.Error())
				if isWebKitRuntimeUnavailable(err) || strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
					t.Skipf("browser runtime unavailable for %s/%s: %v", tc.engine, tc.deviceProfile, err)
				}
				t.Fatalf("Init returned error: %v", err)
			}
			defer b.Teardown(context.Background())

			navigate, err := b.Execute(context.Background(), &protocol.Action{
				Name:   "Open validation matrix page",
				Type:   "browser_navigate",
				Target: "/",
			})
			if err != nil {
				t.Fatalf("navigate returned error: %v", err)
			}
			if !navigate.Success {
				t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
			}
			if navigate.PageTitle != "Browser Validation Matrix" {
				t.Fatalf("PageTitle = %q, want Browser Validation Matrix", navigate.PageTitle)
			}
			if navigate.ClientMetrics == nil {
				t.Fatal("ClientMetrics = nil, want populated metrics")
			}
			if navigate.ClientMetrics.DeviceProfile != tc.deviceProfile {
				t.Fatalf("DeviceProfile = %q, want %q", navigate.ClientMetrics.DeviceProfile, tc.deviceProfile)
			}
			if navigate.ClientMetrics.DevicePlatform != profile.Platform {
				t.Fatalf("DevicePlatform = %q, want %q", navigate.ClientMetrics.DevicePlatform, profile.Platform)
			}
			if navigate.ClientMetrics.DeviceTier != profile.DeviceTier {
				t.Fatalf("DeviceTier = %q, want %q", navigate.ClientMetrics.DeviceTier, profile.DeviceTier)
			}
			if navigate.ClientMetrics.BrowserEngine != tc.engine {
				t.Fatalf("BrowserEngine = %q, want %q", navigate.ClientMetrics.BrowserEngine, tc.engine)
			}

			fill, err := b.Execute(context.Background(), &protocol.Action{
				Name:     "Fill device name",
				Type:     "browser_fill",
				Selector: "#name",
				Value:    tc.deviceProfile,
			})
			if err != nil {
				t.Fatalf("fill returned error: %v", err)
			}
			if !fill.Success {
				t.Fatalf("fill failed: %s", fill.ErrorMessage)
			}

			action, err := b.Execute(context.Background(), &protocol.Action{
				Name:     "Submit sample app",
				Type:     tc.actionType,
				Selector: "#submit",
			})
			if err != nil {
				t.Fatalf("%s returned error: %v", tc.actionType, err)
			}
			if !action.Success {
				t.Fatalf("%s failed: %s", tc.actionType, action.ErrorMessage)
			}

			wait, err := b.Execute(context.Background(), &protocol.Action{
				Name: "Wait for saved state",
				Type: "browser_wait",
				WaitFor: &protocol.WaitCondition{
					Type:      "text",
					Value:     "Saved " + tc.deviceProfile,
					TimeoutMs: 3000,
				},
			})
			if err != nil {
				t.Fatalf("wait returned error: %v", err)
			}
			if !wait.Success {
				t.Fatalf("wait failed: %s", wait.ErrorMessage)
			}
			if wait.ClientMetrics == nil {
				t.Fatal("wait ClientMetrics = nil, want populated metrics")
			}
			if wait.ClientMetrics.DeviceProfile != tc.deviceProfile {
				t.Fatalf("wait DeviceProfile = %q, want %q", wait.ClientMetrics.DeviceProfile, tc.deviceProfile)
			}
			if wait.ClientMetrics.BrowserEngine != tc.engine {
				t.Fatalf("wait BrowserEngine = %q, want %q", wait.ClientMetrics.BrowserEngine, tc.engine)
			}
			if wait.ClientMetrics.TotalResourceCount == 0 {
				t.Fatal("TotalResourceCount = 0, want recorded resource metrics")
			}
		})
	}
}

func isWebKitRuntimeUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "webkit")
}
