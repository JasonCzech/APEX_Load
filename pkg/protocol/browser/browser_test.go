package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolRegistered(t *testing.T) {
	p, err := protocol.Get("browser")
	if err != nil {
		t.Fatalf("Get(browser) returned error: %v", err)
	}
	if p.Name() != "browser" {
		t.Fatalf("Name() = %q, want browser", p.Name())
	}
}

func TestBrowserConfigIntAcceptsEngineNumericTypes(t *testing.T) {
	config := map[string]any{
		"int":     12,
		"int64":   int64(34),
		"float64": float64(56),
		"float32": float32(78),
		"string":  "90",
	}

	tests := map[string]int{
		"int":     12,
		"int64":   34,
		"float64": 56,
		"float32": 78,
		"string":  0,
		"missing": 0,
	}
	for key, want := range tests {
		if got := configInt(config, key); got != want {
			t.Fatalf("configInt(%q) = %d, want %d", key, got, want)
		}
	}
}

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

func TestBrowserInitSupportsWebKitDeviceProfilesOrReturnsInstallGuidance(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{
		"browser_device_profile": "iphone_15_pro",
	})
	if err == nil {
		defer b.Teardown(context.Background())
		return
	}
	if !isWebKitRuntimeUnavailable(err) {
		t.Fatalf("Init error = %v, want WebKit install guidance or successful startup", err)
	}
}

func TestBrowserInitRejectsMismatchedEngineAndDeviceProfile(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{
		"browser_engine":         "chromium",
		"browser_device_profile": "iphone_15_pro",
	})
	if err == nil {
		t.Fatal("Init error = nil, want incompatible engine/profile error")
	}
	if !strings.Contains(err.Error(), "incompatible with device profile") {
		t.Fatalf("Init error = %v, want incompatible device profile error", err)
	}
}

func TestBrowserInitRejectsUnsupportedEngine(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{
		"browser_engine": "gecko",
	})
	if err == nil {
		t.Fatal("Init error = nil, want unsupported engine")
	}
	if !strings.Contains(err.Error(), "unsupported browser engine") {
		t.Fatalf("Init error = %v, want unsupported browser engine", err)
	}
}

func TestBrowserInitRejectsUnknownDeviceProfile(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{
		"browser_device_profile": "unknown-device",
	})
	if err == nil {
		t.Fatal("Init error = nil, want unknown device profile")
	}
	if !strings.Contains(err.Error(), "unknown browser device profile") {
		t.Fatalf("Init error = %v, want unknown browser device profile", err)
	}
}

func TestBrowserApplyDeviceProfile(t *testing.T) {
	b := &Browser{}
	b.userAgent = ""
	b.applyDeviceProfile(DeviceProfile{
		Name:               "iphone_se",
		Width:              375,
		Height:             667,
		DeviceScaleFactor:  2,
		Mobile:             true,
		HasTouch:           true,
		DefaultOrientation: "portrait",
		UserAgent:          "MobileUA",
		CPUThrottleRate:    4,
		DeviceTier:         "low",
	})
	if b.deviceProfile != "iphone_se" {
		t.Fatalf("deviceProfile = %q, want iphone_se", b.deviceProfile)
	}
	if b.viewportW != 375 || b.viewportH != 667 {
		t.Fatalf("viewport = %dx%d, want 375x667", b.viewportW, b.viewportH)
	}
	if !b.mobile || !b.touchMode {
		t.Fatalf("mobile/touchMode = %v/%v, want true/true", b.mobile, b.touchMode)
	}
	if b.orientation != "portrait" {
		t.Fatalf("orientation = %q, want portrait", b.orientation)
	}
	if b.userAgent != "MobileUA" {
		t.Fatalf("userAgent = %q, want MobileUA", b.userAgent)
	}
	if b.cpuThrottleRate != 4 {
		t.Fatalf("cpuThrottleRate = %v, want 4", b.cpuThrottleRate)
	}
	if b.deviceTier != "low" {
		t.Fatalf("deviceTier = %q, want low", b.deviceTier)
	}
}

func TestBrowserExecuteNilActionReturnsFailureResult(t *testing.T) {
	b := &Browser{}
	result, err := b.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("result = nil")
	}
	if result.Success {
		t.Fatal("Success = true, want false")
	}
	if result.ErrorMessage != "browser action is required" {
		t.Fatalf("ErrorMessage = %q, want browser action is required", result.ErrorMessage)
	}
}

func TestBrowserProtocolExecuteBrowserActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Test</title>
    <script>
      function submitForm() {
        const value = document.querySelector('#name').value;
        document.querySelector('#status').textContent = 'Saved ' + value;
        console.log('saved', value);
      }
      window.addEventListener('load', () => console.log('page loaded'));
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

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                    server.URL,
		"browser_headless":            true,
		"browser_capture_screenshots": "per_action",
		"browser_viewport_width":      1280,
		"browser_viewport_height":     720,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
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
	if navigate.PageTitle != "Browser Test" {
		t.Fatalf("PageTitle = %q, want Browser Test", navigate.PageTitle)
	}
	if navigate.ScreenshotBase64 == "" {
		t.Fatal("navigate did not capture per-action screenshot")
	}
	if len(navigate.NetworkEntries) == 0 {
		t.Fatal("navigate did not capture network activity")
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

	click, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Click save",
		Type:     "browser_click",
		Selector: "#submit",
	})
	if err != nil {
		t.Fatalf("click returned error: %v", err)
	}
	if !click.Success {
		t.Fatalf("click failed: %s", click.ErrorMessage)
	}
	if len(click.ConsoleEntries) == 0 {
		t.Fatal("click did not capture console activity")
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

	screenshot, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Capture page",
		Type: "browser_screenshot",
		Capture: &protocol.BrowserCaptureOptions{
			Screenshot: true,
			HTML:       true,
		},
	})
	if err != nil {
		t.Fatalf("screenshot returned error: %v", err)
	}
	if !screenshot.Success {
		t.Fatalf("screenshot failed: %s", screenshot.ErrorMessage)
	}
	if screenshot.ScreenshotBase64 == "" {
		t.Fatal("ScreenshotBase64 = empty, want captured screenshot")
	}
	if !strings.Contains(screenshot.HTML, "Saved apexload") {
		t.Fatalf("HTML did not contain saved marker: %q", screenshot.HTML)
	}
	if screenshot.DOMSummary == nil || screenshot.DOMSummary.Title != "Browser Test" {
		t.Fatalf("DOMSummary = %#v, want title Browser Test", screenshot.DOMSummary)
	}
}

func TestBrowserProtocolEvaluatesAssertions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Assertions</title>
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
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Open homepage",
		Type:       "browser_navigate",
		Target:     "/",
		Assertions: []protocol.Assertion{{Type: "page_title", Value: "Browser Assertions"}, {Type: "url_contains", Value: server.URL}},
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
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

	click, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Click save",
		Type:     "browser_click",
		Selector: "#submit",
		Assertions: []protocol.Assertion{
			{Type: "body_contains", Value: "Saved apexload"},
			{Type: "body_not_contains", Value: "Error"},
			{Type: "response_time_ms", Max: 5000},
		},
	})
	if err != nil {
		t.Fatalf("click returned error: %v", err)
	}
	if !click.Success {
		t.Fatalf("click failed: %s", click.ErrorMessage)
	}
	if !strings.Contains(click.HTML, "Saved apexload") {
		t.Fatalf("HTML = %q, want Saved apexload", click.HTML)
	}

	failed, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Fail assertion",
		Type:       "browser_click",
		Selector:   "#submit",
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Never appears"}},
	})
	if err != nil {
		t.Fatalf("failed assertion click returned error: %v", err)
	}
	if failed.Success {
		t.Fatal("Success = true, want false on browser assertion failure")
	}
	if !strings.Contains(failed.ErrorMessage, `browser page HTML does not contain "Never appears"`) {
		t.Fatalf("ErrorMessage = %q, want browser assertion failure", failed.ErrorMessage)
	}
	if failed.Snapshot == nil {
		t.Fatal("Snapshot = nil, want populated snapshot on browser assertion failure")
	}
	if !strings.Contains(failed.Snapshot.HTML, "Saved apexload") {
		t.Fatalf("Snapshot.HTML = %q, want saved page HTML", failed.Snapshot.HTML)
	}
}

func TestBrowserProtocolExecutesSelectAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Select</title>
    <script>
      function updateStatus() {
        const value = document.querySelector('#country').value;
        document.querySelector('#status').textContent = 'Selected ' + value;
      }
    </script>
  </head>
  <body>
    <select id="country" onchange="updateStatus()">
      <option value="">Choose</option>
      <option value="US">United States</option>
      <option value="CA">Canada</option>
    </select>
    <div id="status">Selected none</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	selectResult, err := b.Execute(context.Background(), &protocol.Action{
		Name:       "Choose country",
		Type:       "browser_select",
		Selector:   "#country",
		Value:      "United States",
		Assertions: []protocol.Assertion{{Type: "body_contains", Value: "Selected US"}},
	})
	if err != nil {
		t.Fatalf("select returned error: %v", err)
	}
	if !selectResult.Success {
		t.Fatalf("select failed: %s", selectResult.ErrorMessage)
	}
	if !strings.Contains(selectResult.HTML, "Selected US") {
		t.Fatalf("HTML did not reflect selected option: %q", selectResult.HTML)
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
	return strings.Contains(message, "webkit runtime unavailable") && strings.Contains(message, strings.ToLower(WebKitInstallCommand()))
}

func TestBrowserProtocolTouchActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Touch Test</title>
    <script>
      function renderCounts() {
        const touches = document.querySelector('#touch-count').textContent;
        const clicks = document.querySelector('#click-count').textContent;
        document.querySelector('#status').textContent = 'touches=' + touches + ' clicks=' + clicks;
      }
      window.addEventListener('load', () => {
        const button = document.querySelector('#touch-target');
        button.addEventListener('touchend', (event) => {
          event.preventDefault();
          const counter = document.querySelector('#touch-count');
          counter.textContent = String(Number(counter.textContent) + 1);
          renderCounts();
        });
        button.addEventListener('click', () => {
          const counter = document.querySelector('#click-count');
          counter.textContent = String(Number(counter.textContent) + 1);
          renderCounts();
        });
        renderCounts();
      });
    </script>
  </head>
  <body>
    <button id="touch-target">Touch me</button>
    <div id="touch-count">0</div>
    <div id="click-count">0</div>
    <div id="status">pending</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_headless":       true,
		"browser_device_profile": "pixel_8",
		"browser_touch_mode":     true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open touch page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	tap, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Tap target",
		Type:     "browser_tap",
		Selector: "#touch-target",
	})
	if err != nil {
		t.Fatalf("tap returned error: %v", err)
	}
	if !tap.Success {
		t.Fatalf("tap failed: %s", tap.ErrorMessage)
	}

	click, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Click target in touch mode",
		Type:     "browser_click",
		Selector: "#touch-target",
	})
	if err != nil {
		t.Fatalf("click returned error: %v", err)
	}
	if !click.Success {
		t.Fatalf("click failed: %s", click.ErrorMessage)
	}

	capture, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Capture touch state",
		Type: "browser_screenshot",
		Capture: &protocol.BrowserCaptureOptions{
			HTML: true,
		},
	})
	if err != nil {
		t.Fatalf("capture returned error: %v", err)
	}
	if !capture.Success {
		t.Fatalf("capture failed: %s", capture.ErrorMessage)
	}
	if !strings.Contains(capture.HTML, `<div id="touch-count">2</div>`) {
		t.Fatalf("capture HTML missing touch count 2: %s", capture.HTML)
	}
}

func TestBrowserProtocolAdvancedTouchActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Advanced Touch Test</title>
    <script>
      window.addEventListener('load', () => {
        let longPressTimer = null;
        const holdTarget = document.querySelector('#hold-target');
        holdTarget.addEventListener('touchstart', () => {
          longPressTimer = setTimeout(() => {
            const counter = document.querySelector('#long-press-count');
            counter.textContent = String(Number(counter.textContent) + 1);
          }, 200);
        });
        holdTarget.addEventListener('touchend', () => {
          if (longPressTimer) {
            clearTimeout(longPressTimer);
            longPressTimer = null;
          }
        });

        let swipeStartX = 0;
        const swipeTarget = document.querySelector('#swipe-target');
        swipeTarget.addEventListener('touchstart', (event) => {
          swipeStartX = event.changedTouches[0].clientX;
        });
        swipeTarget.addEventListener('touchend', (event) => {
          const delta = event.changedTouches[0].clientX - swipeStartX;
          if (delta < -80) {
            const counter = document.querySelector('#swipe-count');
            counter.textContent = String(Number(counter.textContent) + 1);
          }
        });
      });
    </script>
  </head>
  <body>
    <button id="hold-target">Hold me</button>
    <div id="long-press-count">0</div>
    <div id="swipe-target" style="width: 320px; height: 80px; border: 1px solid #000;">Swipe here</div>
    <div id="swipe-count">0</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_headless":       true,
		"browser_device_profile": "pixel_8",
		"browser_touch_mode":     true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open gesture page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	longPress, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Long press target",
		Type:     "browser_long_press",
		Selector: "#hold-target",
		Params: map[string]any{
			"duration_ms": 350,
		},
	})
	if err != nil {
		t.Fatalf("long press returned error: %v", err)
	}
	if !longPress.Success {
		t.Fatalf("long press failed: %s", longPress.ErrorMessage)
	}

	swipe, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Swipe left",
		Type:     "browser_swipe",
		Selector: "#swipe-target",
		Params: map[string]any{
			"direction": "left",
			"distance":  180,
		},
	})
	if err != nil {
		t.Fatalf("swipe returned error: %v", err)
	}
	if !swipe.Success {
		t.Fatalf("swipe failed: %s", swipe.ErrorMessage)
	}

	capture, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Capture gesture state",
		Type: "browser_screenshot",
		Capture: &protocol.BrowserCaptureOptions{
			HTML: true,
		},
	})
	if err != nil {
		t.Fatalf("capture returned error: %v", err)
	}
	if !capture.Success {
		t.Fatalf("capture failed: %s", capture.ErrorMessage)
	}
	if !strings.Contains(capture.HTML, `<div id="long-press-count">1</div>`) {
		t.Fatalf("capture HTML missing long press count 1: %s", capture.HTML)
	}
	if !strings.Contains(capture.HTML, `<div id="swipe-count">1</div>`) {
		t.Fatalf("capture HTML missing swipe count 1: %s", capture.HTML)
	}
}

func TestBrowserProtocolClientMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte(`body { background: #fff; }`))
		default:
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Metrics Test</title>
    <link rel="stylesheet" href="/style.css" />
  </head>
  <body>
    <div id="status">metrics ready</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_headless":       true,
		"browser_client_metrics": true,
		"browser_device_profile": "pixel_8",
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open metrics page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("navigate failed: %s", result.ErrorMessage)
	}
	if result.ClientMetrics == nil {
		t.Fatal("ClientMetrics = nil, want populated metrics")
	}
	if result.ClientMetrics.DeviceProfile != "pixel_8" {
		t.Fatalf("DeviceProfile = %q, want pixel_8", result.ClientMetrics.DeviceProfile)
	}
	if result.ClientMetrics.DeviceTier != "high" {
		t.Fatalf("DeviceTier = %q, want high", result.ClientMetrics.DeviceTier)
	}
	if result.ClientMetrics.ActionDurationMs <= 0 {
		t.Fatalf("ActionDurationMs = %v, want > 0", result.ClientMetrics.ActionDurationMs)
	}
	if result.ClientMetrics.JSHeapTotalMB <= 0 {
		t.Fatalf("JSHeapTotalMB = %v, want > 0", result.ClientMetrics.JSHeapTotalMB)
	}
	if result.ClientMetrics.TotalResourceCount <= 0 {
		t.Fatalf("TotalResourceCount = %d, want > 0", result.ClientMetrics.TotalResourceCount)
	}
	if result.ClientMetrics.LongTaskCount < 0 {
		t.Fatalf("LongTaskCount = %d, want >= 0", result.ClientMetrics.LongTaskCount)
	}
}

func TestBrowserProcessResourceMetrics(t *testing.T) {
	started := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	cpuPct, memoryMB := browserProcessResourceMetrics(
		browserProcessSnapshot{CPUTimeSeconds: 1.25, MemoryMB: 128, CapturedAt: started},
		browserProcessSnapshot{CPUTimeSeconds: 1.75, MemoryMB: 256, CapturedAt: started.Add(2 * time.Second)},
	)
	if cpuPct != 25 {
		t.Fatalf("cpuPct = %v, want 25", cpuPct)
	}
	if memoryMB != 256 {
		t.Fatalf("memoryMB = %v, want 256", memoryMB)
	}
}

func TestBrowserProtocolCacheDisabledReloadsCacheableAssets(t *testing.T) {
	var assetHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset.js":
			atomic.AddInt32(&assetHits, 1)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(`window.__apexloadAssetLoaded = (window.__apexloadAssetLoaded || 0) + 1;`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Cache Test</title>
    <script src="/asset.js"></script>
  </head>
  <body>
    <div id="status">cache test</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":              server.URL,
		"browser_headless":      true,
		"browser_cache_enabled": false,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	for _, target := range []string{"/?view=one", "/?view=two"} {
		result, err := b.Execute(context.Background(), &protocol.Action{
			Name:   "Navigate " + target,
			Type:   "browser_navigate",
			Target: target,
		})
		if err != nil {
			t.Fatalf("navigate %s returned error: %v", target, err)
		}
		if !result.Success {
			t.Fatalf("navigate %s failed: %s", target, result.ErrorMessage)
		}
	}

	if got := atomic.LoadInt32(&assetHits); got < 2 {
		t.Fatalf("asset hits = %d, want at least 2 when cache is disabled", got)
	}
}

func TestBrowserProtocolDownloadResourcesNoneBlocksStaticAssets(t *testing.T) {
	var imageHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pixel.png":
			atomic.AddInt32(&imageHits, 1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not-a-real-png"))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Browser Download Filter Test</title></head>
  <body>
    <img src="/pixel.png" alt="pixel" />
    <div id="status">resource gate</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                   server.URL,
		"browser_headless":           true,
		"browser_download_resources": "none",
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Navigate with blocked assets",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("navigate failed: %s", result.ErrorMessage)
	}
	if got := atomic.LoadInt32(&imageHits); got != 0 {
		t.Fatalf("image hits = %d, want 0 when download_resources is none", got)
	}
}

func TestBrowserProtocolDownloadResourcesRecordedRequiresMetadata(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{
		"base_url":                   "https://example.test",
		"browser_headless":           true,
		"browser_download_resources": "recorded",
	})
	if err == nil {
		t.Fatal("Init returned nil error, want recorded-resource metadata guard")
	}
	if !strings.Contains(err.Error(), "recorded resource metadata") {
		t.Fatalf("Init error = %q, want recorded resource metadata", err.Error())
	}
}

func TestNormalizeDownloadFilterHosts(t *testing.T) {
	hosts := normalizeDownloadFilterHosts([]string{
		" https://cdn.example.com/ ",
		"http://cdn.example.com",
		"assets.example.com:8443/",
		"",
		"assets.example.com:8443",
	})

	want := []string{"cdn.example.com", "assets.example.com:8443"}
	if len(hosts) != len(want) {
		t.Fatalf("normalizeDownloadFilterHosts length = %d, want %d (%v)", len(hosts), len(want), hosts)
	}
	for idx, expected := range want {
		if hosts[idx] != expected {
			t.Fatalf("normalizeDownloadFilterHosts[%d] = %q, want %q", idx, hosts[idx], expected)
		}
	}
}

func TestBrowserFetchRequestPatterns(t *testing.T) {
	tests := []struct {
		name string
		b    Browser
		want []network.ResourceType
	}{
		{
			name: "download filters only",
			b:    Browser{downloadFilterMode: "block_list", downloadFilterHosts: []string{"ads.example.com"}, cacheContentURLs: true},
			want: []network.ResourceType{
				network.ResourceTypeImage,
				network.ResourceTypeStylesheet,
				network.ResourceTypeScript,
				network.ResourceTypeFont,
				network.ResourceTypeMedia,
				network.ResourceTypeOther,
			},
		},
		{
			name: "content cache override only",
			b:    Browser{cacheContentURLs: false},
			want: []network.ResourceType{
				network.ResourceTypeDocument,
				network.ResourceTypeFetch,
				network.ResourceTypeXHR,
			},
		},
		{
			name: "combined patterns are unique",
			b:    Browser{downloadFilterMode: "allow_list", downloadFilterHosts: []string{"cdn.example.com"}, cacheContentURLs: false},
			want: []network.ResourceType{
				network.ResourceTypeImage,
				network.ResourceTypeStylesheet,
				network.ResourceTypeScript,
				network.ResourceTypeFont,
				network.ResourceTypeMedia,
				network.ResourceTypeOther,
				network.ResourceTypeDocument,
				network.ResourceTypeFetch,
				network.ResourceTypeXHR,
			},
		},
	}

	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			patterns := tests[i].b.fetchRequestPatterns()
			if len(patterns) != len(tests[i].want) {
				t.Fatalf("len(patterns) = %d, want %d", len(patterns), len(tests[i].want))
			}
			for idx, expected := range tests[i].want {
				if patterns[idx].ResourceType != expected {
					t.Fatalf("patterns[%d] = %v, want %v", idx, patterns[idx].ResourceType, expected)
				}
			}
		})
	}
}

func TestContinuedRequestHeadersAddsNoCache(t *testing.T) {
	headers := continuedRequestHeaders(network.Headers{
		"Accept":        "application/json",
		"Cache-Control": "public, max-age=3600",
	})
	values := make(map[string]string, len(headers))
	for _, header := range headers {
		values[strings.ToLower(header.Name)] = header.Value
	}
	if values["accept"] != "application/json" {
		t.Fatalf("accept header = %q, want application/json", values["accept"])
	}
	if values["cache-control"] != "no-cache" {
		t.Fatalf("cache-control header = %q, want no-cache", values["cache-control"])
	}
	if values["pragma"] != "no-cache" {
		t.Fatalf("pragma header = %q, want no-cache", values["pragma"])
	}
}

func TestShouldRevalidateContentRequest(t *testing.T) {
	if !shouldRevalidateContentRequest(network.ResourceTypeDocument) {
		t.Fatal("document requests should be revalidated")
	}
	if !shouldRevalidateContentRequest(network.ResourceTypeXHR) {
		t.Fatal("xhr requests should be revalidated")
	}
	if shouldRevalidateContentRequest(network.ResourceTypeImage) {
		t.Fatal("image requests should not be revalidated by cache_content_urls")
	}
}

func TestNormalizeRecordedResources(t *testing.T) {
	resources := normalizeRecordedResources([]string{
		"https://example.com/static/logo.png",
		"/static/logo.png",
		"https://cdn.example.com/app/site.css?v=2",
	})
	if _, ok := resources["example.com/static/logo.png"]; !ok {
		t.Fatal("expected host-qualified recorded resource key")
	}
	if _, ok := resources["/static/logo.png"]; !ok {
		t.Fatal("expected path-only recorded resource key")
	}
	if _, ok := resources["cdn.example.com/app/site.css?v=2"]; !ok {
		t.Fatal("expected query-preserving recorded resource key")
	}
}

func TestBrowserShouldBlockUnrecordedRequest(t *testing.T) {
	browser := &Browser{
		downloadResources:          "recorded",
		recordedResourcesAvailable: true,
		recordedResources: normalizeRecordedResources([]string{
			"https://example.com/static/logo.png",
			"https://cdn.example.com/app/site.css?v=2",
		}),
	}
	if browser.shouldBlockUnrecordedRequest("https://example.com/static/logo.png") {
		t.Fatal("recorded asset should not be blocked")
	}
	if browser.shouldBlockUnrecordedRequest("https://cdn.example.com/app/site.css?v=2") {
		t.Fatal("recorded asset with query should not be blocked")
	}
	if !browser.shouldBlockUnrecordedRequest("https://example.com/static/app.js") {
		t.Fatal("unrecorded asset should be blocked")
	}
	empty := &Browser{downloadResources: "recorded", recordedResourcesAvailable: true}
	if !empty.shouldBlockUnrecordedRequest("https://example.com/static/app.js") {
		t.Fatal("recorded mode with empty metadata should block assets")
	}
	unavailable := &Browser{downloadResources: "recorded", recordedResourcesAvailable: false}
	if unavailable.shouldBlockUnrecordedRequest("https://example.com/static/app.js") {
		t.Fatal("missing recorded metadata should not silently classify in helper")
	}
}

func TestBrowserShouldBlockFilteredRequest(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		hosts  []string
		rawURL string
		want   bool
	}{
		{
			name:   "allow list keeps listed host",
			mode:   "allow_list",
			hosts:  []string{"cdn.example.com"},
			rawURL: "https://cdn.example.com/app.js",
			want:   false,
		},
		{
			name:   "allow list blocks unlisted host",
			mode:   "allow_list",
			hosts:  []string{"cdn.example.com"},
			rawURL: "https://ads.example.com/banner.js",
			want:   true,
		},
		{
			name:   "block list blocks listed host",
			mode:   "block_list",
			hosts:  []string{"ads.example.com"},
			rawURL: "https://ads.example.com/banner.js",
			want:   true,
		},
		{
			name:   "block list allows unlisted host",
			mode:   "block_list",
			hosts:  []string{"ads.example.com"},
			rawURL: "https://cdn.example.com/app.js",
			want:   false,
		},
		{
			name:   "host match accepts explicit port",
			mode:   "block_list",
			hosts:  []string{"assets.example.com:8443"},
			rawURL: "https://assets.example.com:8443/file.css",
			want:   true,
		},
		{
			name:   "invalid url does not block",
			mode:   "allow_list",
			hosts:  []string{"cdn.example.com"},
			rawURL: "://bad-url",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			browser := &Browser{
				downloadFilterMode:  tt.mode,
				downloadFilterHosts: tt.hosts,
			}
			if got := browser.shouldBlockFilteredRequest(tt.rawURL); got != tt.want {
				t.Fatalf("shouldBlockFilteredRequest(%q) = %v, want %v", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestBrowserProtocolCheckNewerVersionsRevalidatesCachedAssets(t *testing.T) {
	var assetHits int32
	var revalidationHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset.js":
			atomic.AddInt32(&assetHits, 1)
			w.Header().Set("ETag", `"asset-v1"`)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Type", "application/javascript")
			if r.Header.Get("If-None-Match") == `"asset-v1"` {
				atomic.AddInt32(&revalidationHits, 1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write([]byte(`window.__apexloadRevalidated = (window.__apexloadRevalidated || 0) + 1;`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Revalidation Test</title>
    <script src="/asset.js"></script>
  </head>
  <body>
    <div id="status">revalidation test</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                     server.URL,
		"browser_headless":             true,
		"browser_cache_enabled":        true,
		"browser_check_newer_versions": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	for _, target := range []string{"/?view=one", "/?view=two"} {
		result, err := b.Execute(context.Background(), &protocol.Action{
			Name:   "Navigate " + target,
			Type:   "browser_navigate",
			Target: target,
		})
		if err != nil {
			t.Fatalf("navigate %s returned error: %v", target, err)
		}
		if !result.Success {
			t.Fatalf("navigate %s failed: %s", target, result.ErrorMessage)
		}
	}

	if got := atomic.LoadInt32(&assetHits); got < 2 {
		t.Fatalf("asset hits = %d, want at least 2 with revalidation enabled", got)
	}
	if got := atomic.LoadInt32(&revalidationHits); got < 1 {
		t.Fatalf("revalidation hits = %d, want at least 1 conditional asset request", got)
	}
}

func TestBrowserProtocolNetworkSimulationAddsLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Browser Network Simulation Test</title></head>
  <body>
    <div id="status">network simulation</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                          server.URL,
		"browser_headless":                  true,
		"web_network_simulation_mode":       "custom",
		"web_network_simulation_latency_ms": 250,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	startedAt := time.Now()
	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Navigate with simulated latency",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("navigate failed: %s", result.ErrorMessage)
	}
	if elapsed := time.Since(startedAt); elapsed < 150*time.Millisecond {
		t.Fatalf("navigation elapsed = %v, want at least 150ms with simulated latency", elapsed)
	}
	if len(result.NetworkEntries) == 0 {
		t.Fatal("navigate did not capture network activity")
	}
}
