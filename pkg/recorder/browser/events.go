// Package browser implements the embedded interaction recorder: a headed
// Playwright-driven Chromium session that captures DOM interactions (with ranked
// selector candidates), main-frame navigations, network activity, and WebSocket
// traffic, and converts the captured session into a browser-protocol DSL script
// plus a side-car HAR. Design-time only — replay runs in pkg/protocol/browser.
package browser

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/selector"
)

// Step kinds produced by the capture script and recorder wiring.
const (
	StepNavigate     = "navigate"
	StepClick        = "click"
	StepFill         = "fill"
	StepSelect       = "select"
	StepPressKey     = "press_key"
	StepSetFiles     = "set_files"
	StepAssert       = "assert"
	StepSwitchWindow = "switch_window"
)

// capturedEvent is the JSON payload reported by the injected capture script
// through the exposed binding. Field names match the script's payload keys.
type capturedEvent struct {
	Kind          string                    `json:"kind"`
	Value         string                    `json:"value,omitempty"`
	TimestampMs   int64                     `json:"ts"`
	URL           string                    `json:"url,omitempty"`
	Title         string                    `json:"title,omitempty"`
	FrameSelector string                    `json:"frame_selector,omitempty"`
	Element       *selector.ElementMetadata `json:"element,omitempty"`
}

// Step is one recorded user interaction with everything replay needs: the
// ranked selector candidates, frame context, page URL, and timing.
type Step struct {
	Seq           int                       `json:"seq"`
	Kind          string                    `json:"kind"`
	Value         string                    `json:"value,omitempty"`
	Timestamp     time.Time                 `json:"timestamp"`
	URL           string                    `json:"url,omitempty"`
	Title         string                    `json:"title,omitempty"`
	FrameSelector string                    `json:"frame_selector,omitempty"`
	Element       *selector.ElementMetadata `json:"element,omitempty"`
	Candidates    []selector.Candidate      `json:"candidates,omitempty"`
}

// NavEvent is a main-frame navigation observed during recording.
type NavEvent struct {
	Timestamp time.Time `json:"timestamp"`
	URL       string    `json:"url"`
}

// WebSocketSummary describes one WebSocket connection observed during recording.
type WebSocketSummary struct {
	URL            string    `json:"url"`
	OpenedAt       time.Time `json:"opened_at"`
	FramesSent     int       `json:"frames_sent"`
	FramesReceived int       `json:"frames_received"`
}

// Session is the complete artifact of one recording run.
type Session struct {
	ID            string             `json:"id"`
	StartURL      string             `json:"start_url"`
	StartedAt     time.Time          `json:"started_at"`
	StoppedAt     time.Time          `json:"stopped_at,omitempty"`
	Steps         []Step             `json:"steps"`
	NavEvents     []NavEvent         `json:"nav_events,omitempty"`
	NetworkEvents []time.Time        `json:"network_events,omitempty"`
	WebSockets    []WebSocketSummary `json:"websockets,omitempty"`
	HARPath       string             `json:"har_path,omitempty"`
	HAR           []byte             `json:"-"`
	Warnings      []string           `json:"warnings,omitempty"`
}

var validStepKinds = map[string]bool{
	StepNavigate:     true,
	StepClick:        true,
	StepFill:         true,
	StepSelect:       true,
	StepPressKey:     true,
	StepSetFiles:     true,
	StepAssert:       true,
	StepSwitchWindow: true,
}

// stepFromEvent decodes one capture-script payload into a Step, deriving ranked
// selector candidates from the captured element metadata.
func stepFromEvent(payload string) (Step, error) {
	var event capturedEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return Step{}, fmt.Errorf("decode capture event: %w", err)
	}
	kind := strings.TrimSpace(event.Kind)
	if !validStepKinds[kind] {
		return Step{}, fmt.Errorf("unsupported capture event kind %q", event.Kind)
	}
	step := Step{
		Kind:          kind,
		Value:         event.Value,
		Timestamp:     time.UnixMilli(event.TimestampMs),
		URL:           strings.TrimSpace(event.URL),
		Title:         strings.TrimSpace(event.Title),
		FrameSelector: strings.TrimSpace(event.FrameSelector),
		Element:       event.Element,
	}
	if event.TimestampMs <= 0 {
		step.Timestamp = time.Now()
	}
	if event.Element != nil {
		step.Candidates = selector.Candidates(*event.Element)
	}
	return step, nil
}

// appendStep adds a step to the session, assigning the sequence number and
// collapsing consecutive duplicate fills on the same element (the capture
// script can report a fill on both Enter and the subsequent blur).
func (s *Session) appendStep(step Step) {
	if len(s.Steps) > 0 && step.Kind == StepFill {
		last := &s.Steps[len(s.Steps)-1]
		if last.Kind == StepFill && sameElement(last.Element, step.Element) {
			last.Value = step.Value
			last.Timestamp = step.Timestamp
			return
		}
	}
	step.Seq = len(s.Steps) + 1
	s.Steps = append(s.Steps, step)
}

func sameElement(a, b *selector.ElementMetadata) bool {
	if a == nil || b == nil {
		return false
	}
	if a.CSSPath != "" || b.CSSPath != "" {
		return a.CSSPath == b.CSSPath
	}
	return a.Tag == b.Tag && a.ID == b.ID && a.Name == b.Name
}

// bestSelector returns the strongest replayable selector for the step: the
// css/xpath promotion first, then any engine-usable candidate (role/text).
func (s Step) bestSelector() string {
	if promoted := selector.PlaywrightSelector(s.Candidates); promoted != "" {
		return promoted
	}
	for _, candidate := range s.Candidates {
		if value, ok := selector.Usable(candidate); ok {
			return value
		}
	}
	return ""
}
