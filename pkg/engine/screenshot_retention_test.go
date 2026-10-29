package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func encodedScreenshot(t *testing.T, size int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	var state uint32 = 1
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			state = 1664525*state + 1013904223
			img.SetRGBA(x, y, color.RGBA{R: byte(state >> 24), G: byte(state >> 16), B: byte(state >> 8), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func assertDecodableScreenshot(t *testing.T, screenshot string) {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(screenshot)
	if err != nil {
		t.Fatalf("invalid base64: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("invalid PNG: %v", err)
	}
}

func TestBoundedScreenshotIsAtomicAtRetentionLimits(t *testing.T) {
	screenshot := encodedScreenshot(t, 32)
	limit := len(screenshot)
	for _, tc := range []struct {
		name     string
		budget   int
		response string
		omitted  bool
	}{
		{name: "exact", budget: 2 * limit},
		{name: "field limit", budget: 2 * (limit - 1), omitted: true},
		{name: "remaining budget", budget: 2 * limit, response: strings.Repeat("r", limit), omitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, used := boundedErrorSnapshot(&protocol.ErrorSnapshot{ScreenshotBase64: screenshot, ResponseBody: tc.response}, tc.budget)
			if got == nil || used > tc.budget || got.ScreenshotOmitted != tc.omitted {
				t.Fatalf("snapshot=%#v used=%d", got, used)
			}
			if tc.omitted {
				if got.ScreenshotBase64 != "" {
					t.Fatal("partial image was retained")
				}
			} else {
				if got.ScreenshotBase64 != screenshot {
					t.Fatal("image changed")
				}
				assertDecodableScreenshot(t, got.ScreenshotBase64)
			}
		})
	}
}

func TestReplayScreenshotOmissionReachesProgressReportAndSnapshot(t *testing.T) {
	screenshot := encodedScreenshot(t, 800)
	if len(screenshot) <= defaultErrorSnapshotMaxBytes/2 {
		t.Fatalf("fixture too small: %d", len(screenshot))
	}
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: false, ErrorMessage: "failed", ScreenshotBase64: screenshot, Snapshot: &protocol.ErrorSnapshot{ScreenshotBase64: screenshot}}, nil
	})
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "screenshot", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}, ReplayOptions{UnredactedDebug: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}})
	if err != nil || len(report.Events) != 1 || progress == nil {
		t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		data, err := json.Marshal(event)
		var decoded ReplayActionEvent
		if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.Snapshot == nil || !decoded.Snapshot.ScreenshotOmitted || decoded.Snapshot.ScreenshotBase64 != "" {
			t.Fatalf("snapshot receipt lost: omitted=%t err=%v", event.ScreenshotOmitted, err)
		}
		if decoded.Screenshot != "" {
			assertDecodableScreenshot(t, decoded.Screenshot)
		} else if !decoded.ScreenshotOmitted {
			t.Fatal("missing event screenshot omission receipt")
		}
	}
}

func TestScreenshotRetentionAndOrdinaryRedaction(t *testing.T) {
	screenshot := encodedScreenshot(t, 800)
	result := &protocol.Result{ScreenshotBase64: screenshot, Snapshot: &protocol.ErrorSnapshot{ScreenshotBase64: screenshot}}
	boundResultEvidence(result)
	if result.Snapshot == nil || !result.Snapshot.ScreenshotOmitted || result.Snapshot.ScreenshotBase64 != "" {
		t.Fatalf("snapshot omission missing: %#v", result.Snapshot)
	}
	if result.ScreenshotBase64 != "" {
		assertDecodableScreenshot(t, result.ScreenshotBase64)
	} else if !result.ScreenshotOmitted {
		t.Fatal("missing result omission receipt")
	}
	redacted := &protocol.Result{ScreenshotBase64: screenshot, ScreenshotOmitted: true, Snapshot: &protocol.ErrorSnapshot{ScreenshotBase64: screenshot, ScreenshotOmitted: true}}
	scrubSensitiveResultWithOptions(redacted, WithSecretValueRedaction(nil, []string{"secret"}), false)
	if redacted.ScreenshotBase64 != "" || redacted.ScreenshotOmitted || redacted.Snapshot.ScreenshotBase64 != "" || redacted.Snapshot.ScreenshotOmitted {
		t.Fatalf("redacted screenshot disclosed or mislabeled: %#v", redacted)
	}
	compact := BoundReplayActionEventForRetention(ReplayActionEvent{Screenshot: screenshot}, false)
	if compact.Screenshot != "" || !compact.ScreenshotOmitted {
		t.Fatal("compact replay lost screenshot omission receipt")
	}
}

func TestReplayRetainsCompleteScreenshotInProgressAndReport(t *testing.T) {
	screenshot := encodedScreenshot(t, 32)
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, ScreenshotBase64: screenshot}, nil
	})
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "screenshot", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}, ReplayOptions{UnredactedDebug: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}})
	if err != nil || len(report.Events) != 1 || progress == nil {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		if event.ScreenshotOmitted || event.Screenshot != screenshot {
			t.Fatalf("complete image changed or omitted")
		}
		assertDecodableScreenshot(t, event.Screenshot)
	}
}

func TestReplayRetainsScreenshotWhenLaterHTMLCaptureFails(t *testing.T) {
	screenshot := encodedScreenshot(t, 32)
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: false, ErrorMessage: "primary action failure", ScreenshotBase64: screenshot, Snapshot: &protocol.ErrorSnapshot{ScreenshotBase64: screenshot}}, nil
	})
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%v", debug), func(t *testing.T) {
			var progress *ReplayActionEvent
			report, err := ReplayScript(context.Background(), &dsl.Script{Name: "capture failure", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}, ReplayOptions{
				UnredactedDebug: debug,
				DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }),
				Variables:       map[string]string{"password": "sensitive-seed"},
				OnProgress: func(p ReplayProgressEvent) {
					if p.Type == ReplayProgressEventRun {
						progress = p.Event
					}
				},
			})
			if err != nil || report == nil || len(report.Events) != 1 || progress == nil {
				t.Fatalf("missing replay evidence: report=%#v error=%v", report, err)
			}
			for _, event := range []*ReplayActionEvent{progress, &report.Events[0]} {
				data, err := json.Marshal(event)
				var decoded ReplayActionEvent
				if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.ErrorMessage != "primary action failure" {
					t.Fatalf("incorrect event: %s (%v)", data, err)
				}
				if debug {
					if decoded.Screenshot != screenshot {
						t.Fatalf("debugger screenshot length=%d want=%d omitted=%v", len(decoded.Screenshot), len(screenshot), decoded.ScreenshotOmitted)
					}
				} else if decoded.Screenshot != "" || decoded.Snapshot != nil && decoded.Snapshot.ScreenshotBase64 != "" {
					t.Fatal("ordinary replay retained a screenshot with active secrets")
				}
			}
		})
	}
}

func TestReplayRetainsAssertionAndDialogFailureScreenshots(t *testing.T) {
	screenshot := encodedScreenshot(t, 32)
	for _, failure := range []string{"assertion failure", "dialog failure"} {
		t.Run(failure, func(t *testing.T) {
			name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
				return &protocol.Result{Success: false, ErrorMessage: failure, ScreenshotBase64: screenshot, Snapshot: &protocol.ErrorSnapshot{ScreenshotBase64: screenshot}}, nil
			})
			for _, debug := range []bool{false, true} {
				var progress *ReplayActionEvent
				report, err := ReplayScript(context.Background(), &dsl.Script{Name: "browser failure", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}, ReplayOptions{
					UnredactedDebug: debug,
					DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }),
					Variables:       map[string]string{"password": "sensitive-seed"},
					OnProgress: func(p ReplayProgressEvent) {
						if p.Type == ReplayProgressEventRun {
							progress = p.Event
						}
					},
				})
				if err != nil || report == nil || len(report.Events) != 1 || progress == nil {
					t.Fatalf("debug=%v report=%#v error=%v", debug, report, err)
				}
				for _, event := range []*ReplayActionEvent{progress, &report.Events[0]} {
					data, err := json.Marshal(event)
					var decoded ReplayActionEvent
					if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.ErrorMessage != failure || (decoded.Screenshot == screenshot) != debug {
						t.Fatalf("debug=%v failure=%s screenshot bytes=%d error=%v", debug, decoded.ErrorMessage, len(decoded.Screenshot), err)
					}
				}
			}
		})
	}
}

func TestReplayRetainsEvaluatedAssertionHTMLInProgressAndReport(t *testing.T) {
	const secret = "assertion-secret-value"
	const html = "<html>evaluated " + secret + "</html>"
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ErrorMessage: "assertion failed", HTML: html, Snapshot: &protocol.ErrorSnapshot{HTML: html}}, nil
	})
	for _, debug := range []bool{false, true} {
		var progress *ReplayActionEvent
		report, err := ReplayScript(context.Background(), &dsl.Script{Name: "assertion HTML", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}, ReplayOptions{
			UnredactedDebug: debug,
			DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }),
			Variables:       map[string]string{"password": secret},
			OnProgress: func(p ReplayProgressEvent) {
				if p.Type == ReplayProgressEventRun {
					progress = p.Event
				}
			},
		})
		if err != nil || report == nil || len(report.Events) != 1 || progress == nil {
			t.Fatalf("debug=%v report=%#v err=%v", debug, report, err)
		}
		for _, event := range []*ReplayActionEvent{progress, &report.Events[0]} {
			if !strings.Contains(event.HTML, "evaluated") || strings.Contains(event.HTML, secret) != debug || strings.Contains(event.HTML, "later") {
				t.Fatalf("debug=%v HTML=%q", debug, event.HTML)
			}
		}
	}
}

func TestConcurrentResultReportsScreenshotOmissionAndRedaction(t *testing.T) {
	screenshot := encodedScreenshot(t, 64)
	for _, secret := range []bool{false, true} {
		config := (*RedactionConfig)(nil)
		if secret {
			config = WithSecretValueRedaction(nil, []string{"managed-secret"})
		}
		result := &protocol.Result{Success: true, ScreenshotBase64: screenshot}
		if _, err := boundConcurrentCompletedResult(result, 256, config); err != nil {
			t.Fatal(err)
		}
		if result.ScreenshotBase64 != "" || result.ScreenshotOmitted == secret {
			t.Fatalf("secret=%t screenshot length=%d omitted=%t", secret, len(result.ScreenshotBase64), result.ScreenshotOmitted)
		}
	}
}

func TestReplayScreenshotCaptureErrorIsRetainedAndOrdinarySinkScrubbed(t *testing.T) {
	const secret = "private-capture-detail"
	reason := "screenshot renderer failed: " + secret
	htmlReason := "HTML renderer failed: " + secret
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: false, ErrorMessage: "primary action failure", ProtocolEvidence: map[string]any{"screenshot_capture_error": reason, "html_capture_error": htmlReason}}, nil
	})
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "capture failure", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}, ReplayOptions{
		UnredactedDebug: true,
		DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }),
		RedactionRules:  []RedactionRule{{Pattern: secret}},
		OnProgress: func(p ReplayProgressEvent) {
			if p.Type == ReplayProgressEventRun {
				progress = p.Event
			}
		},
	})
	if err != nil || report == nil || len(report.Events) != 1 || progress == nil {
		t.Fatalf("missing replay evidence: report=%#v error=%v", report, err)
	}
	for _, event := range []*ReplayActionEvent{progress, &report.Events[0]} {
		encoded, err := json.Marshal(event)
		if err != nil || event.ErrorMessage != "primary action failure" || !strings.Contains(string(encoded), reason) || !strings.Contains(string(encoded), htmlReason) {
			t.Fatalf("capture reason not retained separately: event=%s error=%v", encoded, err)
		}
	}

	sink := &captureResultSink{}
	vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
	result := &protocol.Result{Success: false, ErrorMessage: "primary action failure", ProtocolEvidence: map[string]any{"screenshot_capture_error": reason, "html_capture_error": htmlReason}}
	now := time.Now()
	event, _ := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "request", Type: "GET"}, &protocol.Action{Type: "GET"}, result, nil, now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, nil, nil)
	if event.ProtocolEvidence["screenshot_capture_error"] != reason || event.ProtocolEvidence["html_capture_error"] != htmlReason {
		t.Fatalf("debugger lost capture reason: %#v", event.ProtocolEvidence)
	}
	encoded, err := json.Marshal(sink.result)
	if err != nil || strings.Contains(string(encoded), secret) {
		t.Fatalf("ordinary sink leaked capture error: %s (%v)", encoded, err)
	}
}

func TestReplayAssertionHTMLFailureProgressAndReport(t *testing.T) {
	const secret = "private-assertion-detail"
	reason := "HTML failed: " + secret
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%v", debug), func(t *testing.T) {
			name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
				result := &protocol.Result{ErrorMessage: reason}
				if debug {
					result.ProtocolEvidence = map[string]any{"html_capture_error": reason, "screenshot_capture_error": "screenshot failed separately"}
				}
				return result, nil
			})
			var progress *ReplayActionEvent
			report, err := ReplayScript(context.Background(), &dsl.Script{Name: "assertion HTML error", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}, ReplayOptions{
				UnredactedDebug: debug,
				DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }),
				RedactionRules:  []RedactionRule{{Pattern: secret}},
				OnProgress: func(p ReplayProgressEvent) {
					if p.Type == ReplayProgressEventRun {
						progress = p.Event
					}
				},
			})
			if err != nil || report == nil || len(report.Events) != 1 || progress == nil {
				t.Fatalf("debug=%v report=%#v err=%v", debug, report, err)
			}
			for _, event := range []*ReplayActionEvent{progress, &report.Events[0]} {
				encoded, err := json.Marshal(event)
				if err != nil || strings.Contains(string(encoded), secret) != debug || event.HTML != "" {
					t.Fatalf("debug=%v event=%s err=%v", debug, encoded, err)
				}
				if debug && (event.ProtocolEvidence["html_capture_error"] != reason || event.ProtocolEvidence["screenshot_capture_error"] != "screenshot failed separately") {
					t.Fatalf("capture errors missing: %#v", event.ProtocolEvidence)
				}
			}
		})
	}
}

func TestSnapshotRequestHeaderOmissionReceiptSurvivesRetention(t *testing.T) {
	snapshot := &protocol.ErrorSnapshot{RequestHeaders: map[string]string{"X-Long": strings.Repeat("x", 20<<10), "X-Other": "ok"}}
	bounded, _ := boundedErrorSnapshot(snapshot, 64<<10)
	if bounded == nil || !bounded.RequestHeadersOmitted || !strings.Contains(bounded.RequestHeaders["X-Long"], "...[truncated]") {
		t.Fatalf("missing header omission receipt: %#v", bounded)
	}
	cloned, _ := boundedErrorSnapshot(bounded, 64<<10)
	if cloned == nil || !cloned.RequestHeadersOmitted {
		t.Fatalf("receipt lost on clone: %#v", cloned)
	}
	report := &ReplayReport{}
	event := appendReplayReportEvent(report, ReplayActionEvent{Snapshot: snapshot})
	data, err := json.Marshal(event)
	var decoded ReplayActionEvent
	if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.Snapshot == nil || !decoded.Snapshot.RequestHeadersOmitted {
		t.Fatalf("receipt lost through report: %s (%v)", data, err)
	}
}
