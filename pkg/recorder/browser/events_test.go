package browser

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/selector"
)

func TestStepFromEvent(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantErr     string
		wantKind    string
		wantValue   string
		wantBest    string
		wantFrame   string
		wantNumCand int
	}{
		{
			name:    "invalid json",
			payload: "{not json",
			wantErr: "decode capture event",
		},
		{
			name:    "unknown kind rejected",
			payload: `{"kind":"hover_party","ts":1}`,
			wantErr: "unsupported capture event kind",
		},
		{
			name: "click with element metadata yields candidates",
			payload: `{"kind":"click","ts":1700000000000,"url":"https://shop.example/cart",
				"title":"Cart","element":{"tag":"button","id":"checkout","data_test_id":"checkout-btn",
				"attributes":{"data-testid":"checkout-btn"},"text":"Checkout","css_path":"main > button"}}`,
			wantKind:    StepClick,
			wantBest:    `[data-testid="checkout-btn"]`,
			wantNumCand: 4, // data-testid, id, css path, text
		},
		{
			name: "fill captures value and frame selector",
			payload: `{"kind":"fill","value":"jason","ts":1700000000000,
				"frame_selector":"iframe[name=\"login\"]",
				"element":{"tag":"input","name":"username","css_path":"form > input"}}`,
			wantKind:    StepFill,
			wantValue:   "jason",
			wantFrame:   `iframe[name="login"]`,
			wantBest:    `input[name="username"]`,
			wantNumCand: 2,
		},
		{
			name:      "assert without element still valid",
			payload:   `{"kind":"assert","value":"Order complete","ts":1700000000000}`,
			wantKind:  StepAssert,
			wantValue: "Order complete",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, err := stepFromEvent(tt.payload)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("stepFromEvent() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("stepFromEvent() unexpected error: %v", err)
			}
			if step.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", step.Kind, tt.wantKind)
			}
			if step.Value != tt.wantValue {
				t.Errorf("Value = %q, want %q", step.Value, tt.wantValue)
			}
			if tt.wantFrame != "" && step.FrameSelector != tt.wantFrame {
				t.Errorf("FrameSelector = %q, want %q", step.FrameSelector, tt.wantFrame)
			}
			if tt.wantNumCand > 0 && len(step.Candidates) != tt.wantNumCand {
				t.Errorf("candidates = %d (%v), want %d", len(step.Candidates), step.Candidates, tt.wantNumCand)
			}
			if got := step.bestSelector(); got != tt.wantBest {
				t.Errorf("bestSelector() = %q, want %q", got, tt.wantBest)
			}
		})
	}
}

func TestStepFromEventZeroTimestampDefaultsToNow(t *testing.T) {
	step, err := stepFromEvent(`{"kind":"click","element":{"tag":"a","id":"x"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(step.Timestamp) > time.Minute {
		t.Errorf("timestamp not defaulted to now: %v", step.Timestamp)
	}
}

func TestAppendStepCollapsesDuplicateFills(t *testing.T) {
	session := &Session{}
	el := &selector.ElementMetadata{Tag: "input", Name: "q", CSSPath: "form > input"}
	session.appendStep(Step{Kind: StepFill, Value: "lap", Timestamp: time.UnixMilli(1000), Element: el})
	session.appendStep(Step{Kind: StepFill, Value: "laptop", Timestamp: time.UnixMilli(2000), Element: el})
	if len(session.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 (duplicate fill collapsed)", len(session.Steps))
	}
	if session.Steps[0].Value != "laptop" {
		t.Errorf("collapsed fill value = %q, want %q", session.Steps[0].Value, "laptop")
	}
	if session.Steps[0].Seq != 1 {
		t.Errorf("seq = %d, want 1", session.Steps[0].Seq)
	}

	other := &selector.ElementMetadata{Tag: "input", Name: "email", CSSPath: "form > input:nth-of-type(2)"}
	session.appendStep(Step{Kind: StepFill, Value: "a@b.c", Timestamp: time.UnixMilli(3000), Element: other})
	if len(session.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (different element not collapsed)", len(session.Steps))
	}
	session.appendStep(Step{Kind: StepClick, Timestamp: time.UnixMilli(4000), Element: other})
	session.appendStep(Step{Kind: StepClick, Timestamp: time.UnixMilli(5000), Element: other})
	if len(session.Steps) != 4 {
		t.Fatalf("steps = %d, want 4 (repeated clicks preserved)", len(session.Steps))
	}
}

func TestFrameSelectorFromURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "host and path", input: "https://pay.example/checkout/frame", want: `iframe[src*="pay.example/checkout/frame"]`},
		{name: "invalid url", input: "::bad::", want: ""},
		{name: "no host", input: "/relative", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := frameSelectorFromURL(tt.input); got != tt.want {
				t.Errorf("frameSelectorFromURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
