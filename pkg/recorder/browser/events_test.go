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
			name: "drag and drop captures target candidates",
			payload: `{"kind":"drag_and_drop","ts":1700000000000,
				"element":{"tag":"div","id":"source","css_path":"#source"},
				"target_element":{"tag":"div","data_test_id":"drop-zone","attributes":{"data-testid":"drop-zone"},"css_path":"#target"}}`,
			wantKind:    StepDragAndDrop,
			wantBest:    "#source",
			wantNumCand: 1,
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
			if tt.wantKind == StepDragAndDrop {
				if step.TargetSelector != `[data-testid="drop-zone"]` {
					t.Errorf("TargetSelector = %q", step.TargetSelector)
				}
				if len(step.TargetCandidates) == 0 {
					t.Fatal("TargetCandidates empty")
				}
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

func TestAppendStepDoesNotCollapseIntoExcludedOrDeletedFill(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*testing.T, *Session)
	}{
		{
			name: "excluded",
			mutate: func(t *testing.T, session *Session) {
				if _, ok := session.setStepExcluded(1, true); !ok {
					t.Fatal("setStepExcluded() ok = false")
				}
			},
		},
		{
			name: "deleted",
			mutate: func(t *testing.T, session *Session) {
				if _, ok := session.deleteLastStep(); !ok {
					t.Fatal("deleteLastStep() ok = false")
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session := &Session{}
			el := &selector.ElementMetadata{Tag: "input", Name: "q", CSSPath: "#q"}
			session.appendStep(Step{Kind: StepFill, Value: "first", Timestamp: time.UnixMilli(1000), Element: el})
			tt.mutate(t, session)

			recorded := session.appendStep(Step{Kind: StepFill, Value: "second", Timestamp: time.UnixMilli(2000), Element: el})

			if len(session.Steps) != 2 {
				t.Fatalf("steps = %d, want 2 (new fill must not merge into %s fill)", len(session.Steps), tt.name)
			}
			if recorded.Seq != 2 {
				t.Fatalf("recorded seq = %d, want 2", recorded.Seq)
			}
			if session.Steps[0].Value != "first" {
				t.Errorf("first fill value = %q, want unchanged first", session.Steps[0].Value)
			}
			if session.Steps[1].Value != "second" {
				t.Errorf("second fill value = %q, want second", session.Steps[1].Value)
			}
		})
	}
}

func TestUpdateSwitchWindowStepBackfillsPopupURL(t *testing.T) {
	session := &Session{}
	step := session.appendStep(Step{Kind: StepSwitchWindow, Value: "about:blank", URL: "about:blank", Timestamp: time.UnixMilli(1000)})

	updated, ok := session.updateSwitchWindowStep(step.Seq, " https://shop.example/popup ")
	if !ok {
		t.Fatal("updateSwitchWindowStep() ok = false, want true")
	}
	if updated.Value != "https://shop.example/popup" || updated.URL != "https://shop.example/popup" {
		t.Fatalf("updated step = %#v, want popup URL in value and URL", updated)
	}
	if session.Steps[0].Value != "https://shop.example/popup" || session.Steps[0].URL != "https://shop.example/popup" {
		t.Fatalf("stored step = %#v, want popup URL in value and URL", session.Steps[0])
	}
}

func TestUpdateSwitchWindowStepIgnoresBlankURL(t *testing.T) {
	session := &Session{}
	step := session.appendStep(Step{Kind: StepSwitchWindow, Value: "about:blank", URL: "about:blank", Timestamp: time.UnixMilli(1000)})

	updated, ok := session.updateSwitchWindowStep(step.Seq, "about:blank")
	if ok || updated != nil {
		t.Fatalf("updateSwitchWindowStep() = (%#v, %v), want no update", updated, ok)
	}
	if session.Steps[0].Value != "about:blank" || session.Steps[0].URL != "about:blank" {
		t.Fatalf("stored step = %#v, want unchanged about:blank", session.Steps[0])
	}
}

func TestPromoteStepSelectorPrependsHighConfidenceCandidate(t *testing.T) {
	session := &Session{}
	el := &selector.ElementMetadata{Tag: "button", ID: "old", CSSPath: "#old"}
	step := session.appendStep(Step{Kind: StepClick, Element: el, Candidates: selector.Candidates(*el)})

	updated, ok := session.PromoteStepSelector(step.Seq, `[data-testid="new"]`)
	if !ok {
		t.Fatal("PromoteStepSelector() ok = false")
	}
	if updated.Candidates[0].Value != `[data-testid="new"]` || updated.Candidates[0].Confidence != 100 {
		t.Fatalf("promoted candidates = %+v", updated.Candidates)
	}
	if session.Steps[0].Candidates[0].Value != `[data-testid="new"]` {
		t.Fatalf("stored candidates = %+v", session.Steps[0].Candidates)
	}
	if got := session.Steps[0].bestSelector(); got != `[data-testid="new"]` {
		t.Fatalf("best selector = %q", got)
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
