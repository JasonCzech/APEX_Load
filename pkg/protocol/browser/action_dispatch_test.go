package browser

import (
	"context"
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
		{name: "tap selector", action: protocol.Action{Type: "browser_tap"}, wantError: "browser_tap requires selector or target"},
		{name: "long press selector", action: protocol.Action{Type: "browser_long_press"}, wantError: "browser_long_press requires selector or target"},
		{name: "swipe selector", action: protocol.Action{Type: "browser_swipe"}, wantError: "browser_swipe requires selector or target"},
		{name: "fill selector", action: protocol.Action{Type: "browser_fill"}, wantError: "browser_fill requires selector or target"},
		{name: "fill value", action: protocol.Action{Type: "browser_fill", Selector: "#name"}, wantError: "browser_fill requires value"},
		{name: "select selector", action: protocol.Action{Type: "browser_select"}, wantError: "browser_select requires selector or target"},
		{name: "select value", action: protocol.Action{Type: "browser_select", Selector: "#country"}, wantError: "browser_select requires value"},
		{name: "dblclick selector", action: protocol.Action{Type: "browser_dblclick"}, wantError: "browser_dblclick requires selector or target"},
		{name: "hover selector", action: protocol.Action{Type: "browser_hover"}, wantError: "browser_hover requires selector or target"},
		{name: "drag target", action: protocol.Action{Type: "browser_drag_and_drop", Selector: "#source"}, wantError: "browser_drag_and_drop requires selector/target and target param"},
		{name: "evaluate script", action: protocol.Action{Type: "browser_evaluate_js"}, wantError: "browser_evaluate_js requires script in target"},
		{name: "unsupported", action: protocol.Action{Type: "browser_unknown"}, wantError: "unsupported browser action type: browser_unknown"},
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
