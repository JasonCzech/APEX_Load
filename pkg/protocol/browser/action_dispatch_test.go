package browser

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDispatchActionValidationFailures(t *testing.T) {
	tests := []struct {
		name      string
		action    protocol.Action
		wantError string
	}{
		{name: "navigate target", action: protocol.Action{Type: "browser_navigate"}, wantError: "browser_navigate requires target"},
		{name: "click selector", action: protocol.Action{Type: "browser_click"}, wantError: "browser_click requires selector or target"},
		{name: "download selector", action: protocol.Action{Type: "browser_download"}, wantError: "browser_download requires selector or target"},
		{name: "check selector", action: protocol.Action{Type: "browser_check"}, wantError: "browser_check requires selector or target"},
		{name: "tap selector", action: protocol.Action{Type: "browser_tap"}, wantError: "browser_tap requires selector or target"},
		{name: "long press selector", action: protocol.Action{Type: "browser_long_press"}, wantError: "browser_long_press requires selector or target"},
		{name: "swipe selector", action: protocol.Action{Type: "browser_swipe"}, wantError: "browser_swipe requires selector or target"},
		{name: "fill selector", action: protocol.Action{Type: "browser_fill"}, wantError: "browser_fill requires selector or target"},
		{name: "select selector", action: protocol.Action{Type: "browser_select"}, wantError: "browser_select requires selector or target"},
		{name: "select value", action: protocol.Action{Type: "browser_select", Selector: "#country"}, wantError: "browser_select requires value"},
		{name: "scroll delta", action: protocol.Action{Type: "browser_scroll"}, wantError: "browser_scroll requires delta_x or delta_y"},
		{name: "dblclick selector", action: protocol.Action{Type: "browser_dblclick"}, wantError: "browser_dblclick requires selector or target"},
		{name: "hover selector", action: protocol.Action{Type: "browser_hover"}, wantError: "browser_hover requires selector or target"},
		{name: "drag target", action: protocol.Action{Type: "browser_drag_and_drop", Selector: "#source"}, wantError: "browser_drag_and_drop requires selector/target and target param"},
		{name: "set files selector", action: protocol.Action{Type: "browser_set_files"}, wantError: "browser_set_files requires selector or target"},
		{name: "set files value", action: protocol.Action{Type: "browser_set_files", Selector: "#upload"}, wantError: "browser_set_files requires value or params.files"},
		{name: "press key value", action: protocol.Action{Type: "browser_press_key"}, wantError: "browser_press_key requires value or params.key"},
		{name: "evaluate script", action: protocol.Action{Type: "browser_evaluate_js"}, wantError: "browser_evaluate_js requires script in target"},
		{name: "unsupported", action: protocol.Action{Type: "browser_unknown"}, wantError: "unsupported browser action type: browser_unknown"},
		{name: "wait response timeout", action: protocol.Action{Type: "browser_wait_response", Params: map[string]any{"timeout_ms": -1}}, wantError: "browser_wait_response params.timeout_ms must be >= 0"},
		{name: "wait response method", action: protocol.Action{Type: "browser_wait_response", Params: map[string]any{"method": "BREW"}}, wantError: "browser_wait_response params.method must be a valid HTTP method"},
		{name: "wait response status", action: protocol.Action{Type: "browser_wait_response", Params: map[string]any{"status_code": -1}}, wantError: "browser_wait_response params.status_code must be >= 0"},
		{name: "set geolocation latitude", action: protocol.Action{Type: "browser_set_geolocation", Params: map[string]any{"longitude": 0}}, wantError: "browser_set_geolocation requires params.latitude"},
		{name: "set geolocation longitude", action: protocol.Action{Type: "browser_set_geolocation", Params: map[string]any{"latitude": 0}}, wantError: "browser_set_geolocation requires params.longitude"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Browser{}
			result := &protocol.Result{StartTime: time.Now().Add(-time.Millisecond), Extractions: map[string]string{}}

			actionErr, terminal := b.dispatchAction(context.Background(), &tt.action, result)
			if actionErr != nil {
				t.Fatalf("dispatchAction returned actionErr %v, want nil", actionErr)
			}
			if !terminal {
				t.Fatal("terminal = false, want true")
			}
			if result.ErrorMessage != tt.wantError {
				t.Fatalf("ErrorMessage = %q, want %q", result.ErrorMessage, tt.wantError)
			}
			if result.Duration <= 0 {
				t.Fatalf("Duration = %v, want > 0", result.Duration)
			}
		})
	}
}

func TestNavigationWaitUntil(t *testing.T) {
	tests := []struct {
		name      string
		params    map[string]any
		wantNil   bool
		wantError string
	}{
		{name: "default", params: nil, wantNil: true},
		{name: "load", params: map[string]any{"wait_until": "load"}},
		{name: "dom content loaded", params: map[string]any{"wait_until": "domcontentloaded"}},
		{name: "network idle alias", params: map[string]any{"wait_until": "network_idle"}},
		{name: "commit", params: map[string]any{"wait_until": "commit"}},
		{name: "invalid", params: map[string]any{"wait_until": "ready"}, wantError: "params.wait_until must be load"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := navigationWaitUntil(&protocol.Action{Params: tt.params})
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("navigationWaitUntil error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("navigationWaitUntil returned error: %v", err)
			}
			if tt.wantNil != (got == nil) {
				t.Fatalf("navigationWaitUntil nil = %v, want %v", got == nil, tt.wantNil)
			}
		})
	}
}

func TestDispatchActionScreenshotIsDeferred(t *testing.T) {
	b := &Browser{}
	result := &protocol.Result{StartTime: time.Now().Add(-time.Millisecond), Extractions: map[string]string{}}

	actionErr, terminal := b.dispatchAction(context.Background(), &protocol.Action{Type: "browser_screenshot"}, result)
	if actionErr != nil {
		t.Fatalf("dispatchAction returned actionErr %v, want nil", actionErr)
	}
	if terminal {
		t.Fatal("terminal = true, want false")
	}
	if result.ErrorMessage != "" {
		t.Fatalf("ErrorMessage = %q, want empty", result.ErrorMessage)
	}
}

func TestActionCheckedState(t *testing.T) {
	tests := []struct {
		name   string
		action protocol.Action
		want   bool
	}{
		{name: "default", action: protocol.Action{Type: "browser_check"}, want: true},
		{name: "true value", action: protocol.Action{Type: "browser_check", Value: "true"}, want: true},
		{name: "false value", action: protocol.Action{Type: "browser_check", Value: "false"}, want: false},
		{name: "unchecked value", action: protocol.Action{Type: "browser_check", Value: "unchecked"}, want: false},
		{name: "checked param", action: protocol.Action{Type: "browser_check", Value: "false", Params: map[string]any{"checked": true}}, want: true},
		{name: "unchecked param", action: protocol.Action{Type: "browser_check", Value: "true", Params: map[string]any{"checked": false}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := actionCheckedState(&tt.action); got != tt.want {
				t.Fatalf("actionCheckedState() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPositiveActionParamInt(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   int
	}{
		{name: "missing", params: nil, want: 300},
		{name: "positive", params: map[string]any{"distance": 125}, want: 125},
		{name: "zero", params: map[string]any{"distance": 0}, want: 300},
		{name: "negative", params: map[string]any{"distance": -5}, want: 300},
		{name: "string", params: map[string]any{"distance": "250"}, want: 250},
		{name: "bad string", params: map[string]any{"distance": "left"}, want: 300},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := positiveActionParamInt(tt.params, "distance", 300); got != tt.want {
				t.Fatalf("positiveActionParamInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDownloadMatcherFromAction(t *testing.T) {
	matcher, err := downloadMatcherFromAction(&protocol.Action{
		Target: "https://example.test/reports",
		Params: map[string]any{
			"url_contains":      "/download",
			"filename_contains": "report",
			"filename_regex":    `\.csv$`,
		},
	})
	if err != nil {
		t.Fatalf("downloadMatcherFromAction returned error: %v", err)
	}
	if matcher.URLContains != "/download" {
		t.Fatalf("URLContains = %q, want /download", matcher.URLContains)
	}
	if matcher.FilenameContains != "report" {
		t.Fatalf("FilenameContains = %q, want report", matcher.FilenameContains)
	}
	if matcher.FilenameRegex == nil || !matcher.FilenameRegex.MatchString("monthly-report.csv") {
		t.Fatal("FilenameRegex did not compile or match expected filename")
	}

	_, err = downloadMatcherFromAction(&protocol.Action{
		Params: map[string]any{"filename_regex": "["},
	})
	if err == nil || !strings.Contains(err.Error(), "browser_download params.filename_regex is invalid") {
		t.Fatalf("invalid regex error = %v", err)
	}
}

func TestDownloadSavePath(t *testing.T) {
	saveDir := t.TempDir()
	tests := []struct {
		name      string
		action    *protocol.Action
		suggested string
		want      string
		wantError string
	}{
		{name: "nil action", suggested: "report.csv", want: ""},
		{name: "save as", action: &protocol.Action{Params: map[string]any{"save_as": "/tmp/report.csv"}}, suggested: "ignored.csv", want: "/tmp/report.csv"},
		{name: "save dir", action: &protocol.Action{Params: map[string]any{"save_dir": saveDir}}, suggested: "../report.csv", want: filepath.Join(saveDir, "report.csv")},
		{name: "empty filename fallback", action: &protocol.Action{Params: map[string]any{"save_dir": saveDir}}, suggested: "", want: filepath.Join(saveDir, "download")},
		{name: "parent filename fallback", action: &protocol.Action{Params: map[string]any{"save_dir": saveDir}}, suggested: "..", want: filepath.Join(saveDir, "download")},
		{name: "mutually exclusive", action: &protocol.Action{Params: map[string]any{"save_as": "/tmp/report.csv", "save_dir": saveDir}}, suggested: "report.csv", wantError: "mutually exclusive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := downloadSavePath(tt.action, tt.suggested)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("downloadSavePath error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("downloadSavePath returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("downloadSavePath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestActionParamBool(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   bool
	}{
		{name: "missing default false", params: nil, want: false},
		{name: "bool true", params: map[string]any{"include_failed": true}, want: true},
		{name: "string true", params: map[string]any{"include_failed": "yes"}, want: true},
		{name: "string false", params: map[string]any{"include_failed": "off"}, want: false},
		{name: "bad string default", params: map[string]any{"include_failed": "maybe"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defaultValue := tt.name == "bad string default"
			if got := actionParamBool(tt.params, "include_failed", defaultValue); got != tt.want {
				t.Fatalf("actionParamBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResponseEntryMatches(t *testing.T) {
	entry := protocol.NetworkEntry{
		URL:          "https://example.test/api/orders/123",
		Method:       "POST",
		StatusCode:   201,
		ResourceType: "fetch",
	}
	matcher, err := responseMatcherFromAction(&protocol.Action{
		Target: "/api/orders",
		Params: map[string]any{
			"method":        "post",
			"status_code":   201,
			"resource_type": "fetch",
			"url_regex":     `/orders/\d+$`,
		},
	})
	if err != nil {
		t.Fatalf("responseMatcherFromAction returned error: %v", err)
	}
	if !responseEntryMatches(entry, matcher) {
		t.Fatal("responseEntryMatches returned false, want true")
	}
	if responseEntryMatches(protocol.NetworkEntry{URL: entry.URL, Method: "GET", StatusCode: 201}, matcher) {
		t.Fatal("responseEntryMatches returned true for wrong method")
	}
	if responseEntryMatches(protocol.NetworkEntry{URL: entry.URL, Method: "POST", StatusCode: 0}, matcher) {
		t.Fatal("responseEntryMatches returned true for failed request without include_failed")
	}
}

func TestFailBrowserActionSetsDurationAndMessage(t *testing.T) {
	result := &protocol.Result{StartTime: time.Now().Add(-time.Millisecond)}
	if !failBrowserAction(result, "failed") {
		t.Fatal("failBrowserAction returned false")
	}
	if result.ErrorMessage != "failed" {
		t.Fatalf("ErrorMessage = %q, want failed", result.ErrorMessage)
	}
	if result.Duration <= 0 {
		t.Fatalf("Duration = %v, want > 0", result.Duration)
	}
	if strings.TrimSpace(result.ErrorMessage) == "" {
		t.Fatal("ErrorMessage was blank")
	}
}
