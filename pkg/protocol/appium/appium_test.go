package appium

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// fakeAppium is a W3C WebDriver server with one session and one element,
// "Search", that shows up on the third look.
type fakeAppium struct {
	mu           sync.Mutex
	capabilities map[string]any
	auth         string
	looks        int
	commands     []string
	typed        []string
	executed     []map[string]any
	deleted      bool
	// staleClicks is how many clicks or text reads answer stale element
	// reference.
	staleClicks int
}

func (f *fakeAppium) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/wd/hub")
		f.commands = append(f.commands, r.Method+" "+path)
		if user, password, ok := r.BasicAuth(); ok {
			f.auth = user + ":" + password
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply := func(status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
		}
		switch {
		case r.Method == http.MethodGet && path == "/status":
			reply(200, map[string]any{"ready": true})
		case r.Method == http.MethodPost && path == "/session":
			caps, _ := body["capabilities"].(map[string]any)
			f.capabilities, _ = caps["alwaysMatch"].(map[string]any)
			if f.capabilities["platformName"] != "iOS" {
				reply(500, map[string]any{"error": "session not created", "message": "platformName is required\nstack trace"})
				return
			}
			reply(200, map[string]any{"sessionId": "s1", "capabilities": f.capabilities})
		case r.Method == http.MethodPost && path == "/session/s1/element":
			f.looks++
			if body["using"] == "accessibility id" && body["value"] == "Search" && f.looks >= 3 {
				reply(200, map[string]any{webdriverElementKey: "e1"})
				return
			}
			reply(404, map[string]any{"error": "no such element", "message": "An element could not be located"})
		case r.Method == http.MethodPost && path == "/session/s1/element/e1/click":
			if f.staleClicks > 0 {
				f.staleClicks--
				reply(404, map[string]any{"error": "stale element reference", "message": "The element is no longer attached"})
				return
			}
			reply(200, nil)
		case r.Method == http.MethodPost && path == "/session/s1/element/e1/clear":
			reply(200, nil)
		case r.Method == http.MethodPost && path == "/session/s1/element/e1/value":
			f.typed = append(f.typed, body["text"].(string))
			reply(200, nil)
		case r.Method == http.MethodGet && path == "/session/s1/element/e1/text":
			if f.staleClicks > 0 {
				f.staleClicks--
				reply(404, map[string]any{"error": "stale element reference", "message": "The element is no longer attached"})
				return
			}
			reply(200, "3 results")
		case r.Method == http.MethodPost && path == "/session/s1/execute/sync":
			f.executed = append(f.executed, body)
			reply(200, "done")
		case r.Method == http.MethodGet && path == "/session/s1/screenshot":
			reply(200, "iVBORw0KGgo=")
		case r.Method == http.MethodDelete && path == "/session/s1":
			f.deleted = true
			reply(200, nil)
		default:
			t.Errorf("unexpected command %s %s", r.Method, path)
			reply(404, map[string]any{"error": "unknown command"})
		}
	})
}

func startAppium(t *testing.T, fake *fakeAppium, capabilities map[string]any) (*Appium, error) {
	t.Helper()
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	serverURL := strings.Replace(server.URL, "http://", "http://cloud-user:cloud-key@", 1) + "/wd/hub"
	device := &Appium{}
	err := device.Init(context.Background(), map[string]any{
		"appium_server_url":         serverURL,
		"appium_capabilities":       capabilities,
		"appium_element_timeout_ms": float64(2000),
	})
	return device, err
}

func run(t *testing.T, device *Appium, action protocol.Action) *protocol.Result {
	t.Helper()
	result, err := device.Execute(context.Background(), &action)
	if err != nil {
		t.Fatalf("%s returned an error: %v", action.Type, err)
	}
	return result
}

func TestAppiumDrivesTheDevice(t *testing.T) {
	fake := &fakeAppium{}
	device, err := startAppium(t, fake, map[string]any{"platformName": "iOS", "appium:bundleId": "com.example.searchauth"})
	if err != nil {
		t.Fatal(err)
	}
	if fake.auth != "cloud-user:cloud-key" || fake.capabilities["appium:bundleId"] != "com.example.searchauth" {
		t.Fatalf("session: auth %q, capabilities %v", fake.auth, fake.capabilities)
	}

	// The tap waits for the element to show.
	if result := run(t, device, protocol.Action{Name: "Open search", Type: "appium_tap", Target: "Search"}); !result.Success || result.DependencyType != "appium" {
		t.Fatalf("tap = %+v", result)
	}
	if fake.looks != 3 {
		t.Fatalf("looked %d times, want 3", fake.looks)
	}
	if result := run(t, device, protocol.Action{Name: "Query", Type: "appium_type", Target: "Search", Value: "acme", Params: map[string]any{"clear": true}}); !result.Success {
		t.Fatalf("type = %+v", result)
	}
	if strings.Join(fake.typed, ",") != "acme" || !containsCommand(fake.commands, "POST /session/s1/element/e1/clear") {
		t.Fatalf("typed %v, commands %v", fake.typed, fake.commands)
	}
	if result := run(t, device, protocol.Action{Name: "Results", Type: "appium_wait", Target: "Search", Params: map[string]any{"text": "results"}}); !result.Success || result.ResponseBody != "3 results" {
		t.Fatalf("wait = %+v", result)
	}
	if result := run(t, device, protocol.Action{Name: "Results", Type: "appium_wait", Target: "Search", Params: map[string]any{"text": "no match"}}); result.Success || !strings.Contains(result.ErrorMessage, `shows "3 results", not "no match"`) {
		t.Fatalf("a wait for missing text = %+v", result)
	}
	if result := run(t, device, protocol.Action{Name: "Launch", Type: "appium_activate_app", Params: map[string]any{"bundle_id": "com.example.searchauth"}}); !result.Success {
		t.Fatalf("activate = %+v", result)
	}
	if result := run(t, device, protocol.Action{Name: "Swipe", Type: "appium_execute", Params: map[string]any{"script": "mobile: swipe", "args": map[string]any{"direction": "up"}}}); !result.Success || result.ResponseBody != "done" {
		t.Fatalf("execute = %+v", result)
	}
	if len(fake.executed) != 2 || fake.executed[0]["script"] != "mobile: activateApp" ||
		fake.executed[1]["script"] != "mobile: swipe" || fake.executed[1]["args"].([]any)[0].(map[string]any)["direction"] != "up" {
		t.Fatalf("executed %v", fake.executed)
	}
	if result := run(t, device, protocol.Action{Name: "Evidence", Type: "appium_screenshot"}); !result.Success || result.ScreenshotBase64 != "iVBORw0KGgo=" {
		t.Fatalf("screenshot = %+v", result)
	}
	if result := run(t, device, protocol.Action{Name: "Missing", Type: "appium_tap", Target: "Checkout", Params: map[string]any{"timeout_ms": float64(300)}}); result.Success ||
		!strings.Contains(result.ErrorMessage, `no element matched accessibility id "Checkout" within 300ms`) {
		t.Fatalf("a missing element = %+v", result)
	}
	if err := device.HealthCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := device.Teardown(context.Background()); err != nil || !fake.deleted {
		t.Fatalf("teardown: %v, deleted %v", err, fake.deleted)
	}
	if err := device.Teardown(context.Background()); err != nil {
		t.Fatalf("a second teardown: %v", err)
	}
	if result := run(t, device, protocol.Action{Name: "After", Type: "appium_screenshot"}); result.Success {
		t.Fatal("a step after teardown succeeded")
	}
}

func containsCommand(commands []string, want string) bool {
	for _, command := range commands {
		if command == want {
			return true
		}
	}
	return false
}

// A screen that re-renders between finding an element and tapping it is
// looked at again, once.
func TestAppiumFindsAStaleElementAgain(t *testing.T) {
	fake := &fakeAppium{looks: 2, staleClicks: 1}
	device, err := startAppium(t, fake, map[string]any{"platformName": "iOS"})
	if err != nil {
		t.Fatal(err)
	}
	if result := run(t, device, protocol.Action{Name: "Go", Type: "appium_tap", Target: "Search"}); !result.Success {
		t.Fatalf("tap after a stale element = %+v", result)
	}
	fake.staleClicks = 2
	if result := run(t, device, protocol.Action{Name: "Go", Type: "appium_tap", Target: "Search"}); result.Success || !strings.Contains(result.ErrorMessage, "stale element reference") {
		t.Fatalf("a twice-stale element = %+v", result)
	}
	// A wait reads the text of the element it finds again.
	fake.staleClicks = 1
	if result := run(t, device, protocol.Action{Name: "Results", Type: "appium_wait", Target: "Search"}); !result.Success || result.ResponseBody != "3 results" {
		t.Fatalf("wait after a stale element = %+v", result)
	}
}

// A governed run reaches only the Appium server its runtime policy allows.
func TestAppiumHonoursTheRuntimeTargetPolicy(t *testing.T) {
	device := &Appium{}
	err := device.Init(context.Background(), map[string]any{
		"appium_server_url":            "http://10.0.0.5:4723",
		"appium_capabilities":          map[string]any{"platformName": "iOS"},
		protocol.TargetPolicyConfigKey: []string{"*.example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), `"10.0.0.5" is not allowed`) {
		t.Fatalf("err = %v", err)
	}
}

// The server's reason for refusing a session reaches the run, on one line.
func TestAppiumReportsSessionRefusal(t *testing.T) {
	_, err := startAppium(t, &fakeAppium{}, map[string]any{"platformName": "Windows"})
	if err == nil || err.Error() != "start the appium session: appium session not created: platformName is required" {
		t.Fatalf("err = %v", err)
	}
	if _, err := startAppium(t, &fakeAppium{}, nil); err == nil || !strings.Contains(err.Error(), "capabilities are required") {
		t.Fatalf("no capabilities: %v", err)
	}
}

// A device cloud's credentials never reach an error message.
func TestAppiumKeepsCredentialsOutOfErrors(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(server.URL, "http://")
	server.Close()
	device := &Appium{}
	err := device.Init(context.Background(), map[string]any{
		"appium_server_url":   "http://user:super-secret-key@" + address + "/wd/hub",
		"appium_capabilities": map[string]any{"platformName": "iOS"},
	})
	if err == nil || strings.Contains(err.Error(), "super-secret-key") {
		t.Fatalf("err = %v", err)
	}
}
