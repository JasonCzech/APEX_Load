package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	playwright "github.com/mxschmitt/playwright-go"
)

type wsTestPage struct {
	playwright.Page
	handler  func(playwright.WebSocket)
	gotoHook func()
}

func (p *wsTestPage) OnWebSocket(handler func(playwright.WebSocket)) { p.handler = handler }
func (p *wsTestPage) RemoveListener(_ string, _ any)                 { p.handler = nil }
func (p *wsTestPage) MainFrame() playwright.Frame                    { return nil }
func (p *wsTestPage) URL() string                                    { return "https://example.test/" }
func (p *wsTestPage) GoBack(...playwright.PageGoBackOptions) (playwright.Response, error) {
	return nil, nil
}
func (p *wsTestPage) GoForward(...playwright.PageGoForwardOptions) (playwright.Response, error) {
	return nil, nil
}
func (p *wsTestPage) Goto(_ string, _ ...playwright.PageGotoOptions) (playwright.Response, error) {
	if p.gotoHook != nil {
		p.gotoHook()
	}
	return nil, fmt.Errorf("test navigation stopped")
}

type wsTestSocket struct {
	playwright.WebSocket
	url      string
	received func([]byte)
}

func (s *wsTestSocket) URL() string                          { return s.url }
func (s *wsTestSocket) OnFrameSent(func([]byte))             {}
func (s *wsTestSocket) OnFrameReceived(handler func([]byte)) { s.received = handler }

func wsTestRedactor(value string) (string, bool) { return scrub.Value(value, scrub.Policy{}), true }

func setWSTestRedactor(b *Browser, redact protocol.BoundedTextRedactor) {
	b.SetRuntimeBoundedTextRedactorSnapshot(func() (protocol.BoundedTextRedactor, protocol.BoundedTextRedactorCommitGuard) {
		return redact, func(commit func() bool) bool { return commit() }
	})
}

func TestBrowserWSMatchesOriginalPayloadAndSeparatesEvidence(t *testing.T) {
	for _, debug := range []bool{false, true} {
		b := &Browser{unredactedDebug: debug}
		setWSTestRedactor(b, wsTestRedactor)
		b.recordWSEvent("wss://example.test/live?token=url-secret", "received", []byte(`{"token":"debug-secret"}`))
		for _, matcher := range []browserWSMatcher{
			{Match: "debug-secret", MatchMode: "contains"},
			{Match: `{"token":"debug-secret"}`, MatchMode: "exact"},
			{Match: `debug-.*`, MatchMode: "regex", MatchRegex: regexp.MustCompile(`debug-.*`)},
		} {
			if matches := b.matchWSEvents(matcher); len(matches) != 1 {
				t.Fatalf("debug=%v matcher=%+v did not match original payload", debug, matcher)
			}
		}
		if matches := b.matchWSEvents(browserWSMatcher{Match: "[REDACTED]", MatchMode: "contains"}); len(matches) != 0 {
			t.Fatalf("debug=%v matched a redaction marker not present in the frame", debug)
		}
		matches := b.matchWSEvents(browserWSMatcher{URLContains: "url-secret", Match: "debug-secret"})
		if len(matches) != 1 {
			t.Fatal("raw URL matcher did not match")
		}
		got := b.wsEvidence(matches)
		encoded, err := json.Marshal(got)
		if err != nil || strings.Contains(string(encoded), "debug-secret") != debug || strings.Contains(string(encoded), "url-secret") != debug {
			t.Fatalf("debug=%v evidence=%s error=%v", debug, encoded, err)
		}
		if !debug && (strings.Contains(fmt.Sprintf("%#v", got), "debug-secret") || strings.Contains(fmt.Sprintf("%#v", got), "url-secret")) {
			t.Fatal("public evidence retained private matcher data")
		}
		if len(protocol.BoundProtocolEvidence(map[string]any{"browser_ws_matches": got})) == 0 {
			t.Fatal("evidence was dropped by the protocol evidence boundary")
		}
	}
}

func TestBrowserWSMatcherFromAction(t *testing.T) {
	action := &protocol.Action{
		Type: "browser_ws_wait",
		Params: map[string]any{
			"url_contains": "live",
			"direction":    "received",
			"match":        `"ready"`,
			"match_mode":   "contains",
		},
	}
	matcher, err := browserWSMatcherFromAction(action)
	if err != nil {
		t.Fatalf("browserWSMatcherFromAction() error: %v", err)
	}
	if matcher.URLContains != "live" || matcher.Direction != "received" || matcher.Match != `"ready"` || matcher.MatchMode != "contains" {
		t.Fatalf("matcher = %+v", matcher)
	}

	action.Params["match_mode"] = "[broken"
	if _, err := browserWSMatcherFromAction(action); err == nil {
		t.Fatal("invalid match_mode accepted")
	}
	action.Params["match_mode"] = "regex"
	if _, err := browserWSMatcherFromAction(action); err != nil {
		t.Fatalf("valid regex mode rejected: %v", err)
	}
	action.Params["match"] = "[broken"
	if _, err := browserWSMatcherFromAction(action); err == nil {
		t.Fatal("invalid regex accepted")
	}
}

func TestBrowserMatchWSEvents(t *testing.T) {
	b := &Browser{
		wsEvents: []browserWSEvent{
			{Seq: 1, URL: "wss://shop.example/live", Direction: "open", Timestamp: time.Now()},
			{Seq: 2, URL: "wss://shop.example/live", Direction: "received", Payload: `{"type":"ready"}`, matchPayload: `{"type":"ready"}`, Timestamp: time.Now()},
			{Seq: 3, URL: "wss://shop.example/chat", Direction: "sent", Payload: `{"type":"ping"}`, matchPayload: `{"type":"ping"}`, Timestamp: time.Now()},
		},
	}

	matches := b.matchWSEvents(browserWSMatcher{
		URLContains: "live",
		Direction:   "received",
		Match:       `"ready"`,
		MatchMode:   "contains",
	})
	if len(matches) != 1 || matches[0].Seq != 2 {
		t.Fatalf("matches = %+v, want received ready frame", matches)
	}
	if matches := b.matchWSEvents(browserWSMatcher{URLContains: "live", Direction: "received", Match: `"ready"`, MatchMode: "contains", MinSeq: 2}); len(matches) != 0 {
		t.Fatalf("matches with MinSeq=2 = %+v, want no stale frame", matches)
	}
	b.advanceWSWatermark([]browserWSEvent{{Seq: 2}})
	if got := b.currentWSWatermark(); got != 2 {
		t.Fatalf("ws watermark = %d, want 2", got)
	}
	b.resetWSEvents()
	if got := b.currentWSWatermark(); got != 0 || len(b.wsEvents) != 0 || b.wsSeq != 0 {
		t.Fatalf("reset ws state = watermark %d seq %d events %d", got, b.wsSeq, len(b.wsEvents))
	}
	if !browserWSPayloadMatches(`{"type":"ready"}`, `"type":"ready"`, "contains", nil) {
		t.Fatal("contains payload match failed")
	}
	matcher, err := browserWSMatcherFromAction(&protocol.Action{Type: "browser_ws_wait", Params: map[string]any{
		"match":      `^\{"type":"ready"\}$`,
		"match_mode": "regex",
	}})
	if err != nil {
		t.Fatalf("browserWSMatcherFromAction regex: %v", err)
	}
	if !browserWSPayloadMatches(`{"type":"ready"}`, `^\{"type":"ready"\}$`, "regex", matcher.MatchRegex) {
		t.Fatal("regex payload match failed")
	}
	if browserWSPayloadMatches(`{"type":"ready"}`, `ready`, "exact", nil) {
		t.Fatal("exact payload match accepted partial payload")
	}
}

func TestBrowserWSWaitThenAssertCanReadSameFrame(t *testing.T) {
	b := &Browser{}
	b.recordWSEvent("wss://shop.example/live", "received", []byte(`{"type":"ready"}`))

	waitAction := &protocol.Action{
		Type: "browser_ws_wait",
		Params: map[string]any{
			"url_contains": "live",
			"direction":    "received",
			"match":        `"ready"`,
		},
	}
	waitResult := &protocol.Result{}
	if err, fail := b.dispatchWSWait(context.Background(), waitAction, waitResult); err != nil || fail {
		t.Fatalf("dispatchWSWait err=%v fail=%v", err, fail)
	}
	if got := b.currentWSWatermark(); got != 1 {
		t.Fatalf("watermark after wait = %d, want 1", got)
	}

	assertResult := &protocol.Result{}
	if err, fail := b.dispatchWSAssert(context.Background(), &protocol.Action{
		Type:   "browser_ws_assert",
		Params: waitAction.Params,
	}, assertResult); err != nil || fail {
		t.Fatalf("dispatchWSAssert err=%v fail=%v", err, fail)
	}
	if got := b.currentWSWatermark(); got != 1 {
		t.Fatalf("watermark after assert = %d, want unchanged 1", got)
	}
	matches, ok := assertResult.ProtocolEvidence["browser_ws_matches"].([]any)
	if !ok || len(matches) != 1 || matches[0].(map[string]any)["seq"] != int64(1) {
		t.Fatalf("assert matches = %#v, want original wait frame", assertResult.ProtocolEvidence["browser_ws_matches"])
	}
}

func TestBrowserWSWaitAndAssertMatchSensitiveOriginal(t *testing.T) {
	for _, debug := range []bool{false, true} {
		b := &Browser{unredactedDebug: debug}
		setWSTestRedactor(b, wsTestRedactor)
		b.recordWSEvent("wss://example.test/live?token=url-secret", "received", []byte(`{"token":"debug-secret"}`))
		params := map[string]any{"match": "debug-secret", "url_contains": "url-secret"}
		for _, action := range []string{"browser_ws_wait", "browser_ws_assert"} {
			result := &protocol.Result{}
			var err error
			var failed bool
			if action == "browser_ws_wait" {
				err, failed = b.dispatchWSWait(context.Background(), &protocol.Action{Type: action, Params: params}, result)
			} else {
				err, failed = b.dispatchWSAssert(context.Background(), &protocol.Action{Type: action, Params: params}, result)
			}
			if err != nil || failed {
				t.Fatalf("debug=%v %s: error=%v failed=%v", debug, action, err, failed)
			}
			encoded, err := json.Marshal(result.ProtocolEvidence)
			if err != nil || strings.Contains(string(encoded), "debug-secret") != debug || strings.Contains(string(encoded), "url-secret") != debug {
				t.Fatalf("debug=%v %s evidence=%s error=%v", debug, action, encoded, err)
			}
		}
	}
}

func TestBrowserWSBoundedBinaryMatchingAndDynamicRedaction(t *testing.T) {
	b := &Browser{}
	payload := append([]byte{0xff}, []byte("dynamic-secret")...)
	b.recordWSEvent("wss://example.test", "received", payload)
	setWSTestRedactor(b, func(s string) (string, bool) { return strings.ReplaceAll(s, "dynamic-secret", "[REDACTED]"), true })
	encoded := base64.StdEncoding.EncodeToString(payload)
	matches := b.matchWSEvents(browserWSMatcher{Match: encoded, MatchMode: "exact"})
	if len(matches) != 1 {
		t.Fatal("binary matcher did not use original base64 bytes")
	}
	evidence := b.wsEvidence(matches).([]any)[0].(map[string]any)
	if evidence["payload_omitted"] != true || evidence["payload"] != nil {
		t.Fatalf("binary evidence was not safely omitted: %#v", evidence)
	}
	long := strings.Repeat("x", browserWSPayloadLimitBytes) + "beyond-limit"
	b.recordWSEvent("wss://example.test", "received", []byte(long))
	if got := b.matchWSEvents(browserWSMatcher{Match: "beyond-limit"}); len(got) != 0 {
		t.Fatal("matcher read beyond capture limit")
	}
	if got := b.matchWSEvents(browserWSMatcher{Match: strings.Repeat("x", browserWSPayloadLimitBytes), MatchMode: "exact"}); len(got) != 0 {
		t.Fatal("exact matcher accepted only a truncated prefix")
	}
	if got := b.matchWSEvents(browserWSMatcher{Match: "^x+$", MatchMode: "regex", MatchRegex: regexp.MustCompile("^x+$")}); len(got) != 0 {
		t.Fatal("regex matcher accepted only a truncated prefix")
	}
	for _, tc := range []struct {
		pattern string
		want    bool
	}{
		{"xxx", true}, {"^xxx", true}, {"xxx$", false}, {`xxx\b`, false}, {`xxx\B`, true}, {`xxx\z`, false},
	} {
		matcher, err := browserWSMatcherFromAction(&protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": tc.pattern, "match_mode": "regex"}})
		if err != nil {
			t.Fatalf("compile %q: %v", tc.pattern, err)
		}
		if got := len(b.matchWSEvents(matcher)) > 0; got != tc.want {
			t.Errorf("truncated regex %q matched=%v, want %v", tc.pattern, got, tc.want)
		}
	}
	if got := b.matchWSEvents(browserWSMatcher{Match: strings.Repeat("x", browserWSPayloadLimitBytes), MatchMode: "contains"}); len(got) != 1 || !got[0].Truncated {
		t.Fatal("bounded contains matching lost truncation receipt")
	}
}

func TestBrowserWSTruncatedBinaryRegexAvoidsUnstableBase64Tail(t *testing.T) {
	b := &Browser{}
	payload := append([]byte{0xff}, []byte(strings.Repeat("a", browserWSPayloadLimitBytes+3))...)
	b.recordWSEvent("wss://example.test", "received", payload)
	for _, tc := range []struct {
		pattern string
		want    bool
	}{{"/2Fh", true}, {"/2Fh.*", true}, {"YQ==", false}, {"YQ==$", false}} {
		matcher, err := browserWSMatcherFromAction(&protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": tc.pattern, "match_mode": "regex"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(b.matchWSEvents(matcher)) != 0; got != tc.want {
			t.Errorf("binary regex %q matched=%v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestBrowserWSTruncatedRegexAssertionsUseObservedContext(t *testing.T) {
	b := &Browser{}
	prefix := " token=abc \n" + strings.Repeat("x", browserWSPayloadLimitBytes-12)
	b.recordWSEvent("wss://example.test", "received", []byte(prefix+"tail"))
	for _, tc := range []struct {
		pattern string
		want    bool
	}{
		{`\btoken=abc\b`, true},
		{`(?m)token=abc $`, true},
		{`x\b`, false},
		{`x\z`, false},
	} {
		matcher, err := browserWSMatcherFromAction(&protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": tc.pattern, "match_mode": "regex"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(b.matchWSEvents(matcher)) != 0; got != tc.want {
			t.Errorf("regex %q matched=%v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestBrowserWSTruncatedRegexGreedyInteriorAlternative(t *testing.T) {
	for _, tc := range []struct {
		name, payload, pattern string
		want                   bool
	}{
		{"interior word boundary", "token " + strings.Repeat("a", browserWSPayloadLimitBytes-6) + "b", `.+\b`, true},
		{"artificial word boundary", strings.Repeat("a", browserWSPayloadLimitBytes) + "b", `^a+\b`, false},
		{"artificial end", strings.Repeat("a", browserWSPayloadLimitBytes) + "b", `^a+$`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Browser{}
			b.recordWSEvent("wss://example.test", "received", []byte(tc.payload))
			matcher, err := browserWSMatcherFromAction(&protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": tc.pattern, "match_mode": "regex"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(b.matchWSEvents(matcher)) != 0; got != tc.want {
				t.Fatalf("regex %q matched=%v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestBrowserWSTruncatedBinaryContainsAvoidsUnstableBase64Tail(t *testing.T) {
	b := &Browser{}
	payload := append([]byte{0xff}, []byte(strings.Repeat("a", browserWSPayloadLimitBytes-1)+"b")...)
	b.recordWSEvent("wss://example.test", "received", payload)
	for _, tc := range []struct {
		match string
		want  bool
	}{{"/2Fh", true}, {"YQ==", false}} {
		got := len(b.matchWSEvents(browserWSMatcher{Match: tc.match, MatchMode: "contains"})) != 0
		if got != tc.want {
			t.Errorf("contains %q matched=%v, want %v", tc.match, got, tc.want)
		}
	}
}

func TestBrowserWSProjectionPanicFailsClosed(t *testing.T) {
	b := &Browser{}
	b.recordWSEvent("wss://example.test", "received", []byte("secret"))
	setWSTestRedactor(b, func(string) (string, bool) { panic("test projector panic") })
	evidence := b.wsEvidence(b.matchWSEvents(browserWSMatcher{})).([]any)[0].(map[string]any)
	if evidence["payload_omitted"] != true || evidence["url_omitted"] != true || strings.Contains(fmt.Sprint(evidence), "secret") {
		t.Fatalf("projector panic leaked evidence: %#v", evidence)
	}
}

func TestBrowserWSTruncatedUTF8BoundaryUsesBoundedWholeFrameValidation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		total        int
		invalid      bool
		wantEncoding string
	}{
		{"valid-under-limit", browserWSTextValidationBytes, false, "text"},
		{"valid-over-limit", browserWSTextValidationBytes + 1, false, "base64"},
		{"binary-under-limit", browserWSTextValidationBytes, true, "base64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := append([]byte(strings.Repeat("a", browserWSPayloadLimitBytes-1)+"é"), []byte(strings.Repeat("z", tc.total-browserWSPayloadLimitBytes-1))...)
			if tc.invalid {
				payload[len(payload)-1] = 0xff
			}
			b := &Browser{}
			b.recordWSEvent("wss://example.test", "received", payload)
			if len(b.wsEvents) != 1 || b.wsEvents[0].matchEncoding != tc.wantEncoding {
				t.Fatalf("encoding = %#v, want %s", b.wsEvents, tc.wantEncoding)
			}
			if tc.wantEncoding == "text" && len(b.matchWSEvents(browserWSMatcher{Match: "aaaa"})) != 1 {
				t.Fatal("text prefix did not match")
			}
		})
	}
}

func TestBrowserWSWaitDistinguishesParentDeadlineAndActionTimeout(t *testing.T) {
	t.Run("parent deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		result := &protocol.Result{}
		err, failed := (&Browser{}).dispatchWSWait(ctx, &protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"timeout_ms": 1000}}, result)
		if failed || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timed out after 1s") {
			t.Fatalf("parent deadline error=%v failed=%v", err, failed)
		}
	})
	t.Run("parent canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err, failed := (&Browser{}).dispatchWSWait(ctx, &protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"timeout_ms": 1000}}, &protocol.Result{})
		if failed || !errors.Is(err, context.Canceled) {
			t.Fatalf("parent cancellation error=%v failed=%v", err, failed)
		}
	})
	t.Run("action timeout", func(t *testing.T) {
		err, failed := (&Browser{}).dispatchWSWait(context.Background(), &protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"timeout_ms": 10}}, &protocol.Result{})
		if failed || err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out after 10ms") {
			t.Fatalf("action timeout error=%v failed=%v", err, failed)
		}
	})
}

func TestBrowserWSBinaryAfterValidRetainedPrefix(t *testing.T) {
	for _, tc := range []struct {
		name         string
		total        int
		invalid      bool
		wantEncoding string
	}{
		{"invalid just beyond prefix", browserWSPayloadLimitBytes + 1, true, "base64"},
		{"invalid at validation limit", browserWSTextValidationBytes, true, "base64"},
		{"valid at validation limit", browserWSTextValidationBytes, false, "text"},
		{"above validation limit uses bounded prefix", browserWSTextValidationBytes + 1, true, "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(strings.Repeat("a", tc.total))
			if tc.invalid {
				payload[len(payload)-1] = 0xff
			}
			b := &Browser{unredactedDebug: true}
			b.recordWSEvent("wss://example.test", "received", payload)
			if len(b.wsEvents) != 1 || b.wsEvents[0].matchEncoding != tc.wantEncoding || !b.wsEvents[0].Truncated {
				t.Fatalf("unexpected frame encoding or truncation: %#v", b.wsEvents)
			}
			match := "aaaa"
			if tc.wantEncoding == "base64" {
				match = base64.StdEncoding.EncodeToString([]byte("aaa"))
				if b.wsEvents[0].matchPayload != base64.StdEncoding.EncodeToString(payload[:browserWSPayloadLimitBytes]) {
					t.Fatal("binary payload was not encoded from the retained bytes")
				}
			}
			matches := b.matchWSEvents(browserWSMatcher{Match: match, MatchMode: "contains"})
			if len(matches) != 1 {
				t.Fatalf("%s prefix matcher missed the frame", tc.wantEncoding)
			}
			evidence := b.wsEvidence(matches).([]any)[0].(map[string]any)
			if evidence["encoding"] != tc.wantEncoding || evidence["truncated"] != true {
				t.Fatalf("evidence = %#v", evidence)
			}
		})
	}
}

func TestBrowserWSEvidenceUsesCurrentBoundedProjectionOfOriginal(t *testing.T) {
	b := &Browser{}
	const secret = "prefix supersecret"
	setWSTestRedactor(b, func(value string) (string, bool) { return scrub.Value(value, scrub.Policy{}), true })
	b.recordWSEvent("wss://example.test", "received", []byte("token="+secret))
	// A value may become known only after capture. Re-projecting the already
	// scrubbed prefix would otherwise leave the suffix of this secret visible.
	setWSTestRedactor(b, func(value string) (string, bool) {
		return scrub.Value(strings.ReplaceAll(value, secret, "[REDACTED]"), scrub.Policy{}), true
	})
	matches := b.matchWSEvents(browserWSMatcher{Match: secret})
	if len(matches) != 1 {
		t.Fatal("matcher lost original secret")
	}
	encoded, err := json.Marshal(b.wsEvidence(matches))
	if err != nil || strings.Contains(string(encoded), "supersecret") {
		t.Fatalf("evidence leaked part of a runtime secret: %s, %v", encoded, err)
	}
}

func TestBrowserWSEvidenceWithoutBoundedRuntimeRedactor(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		b := &Browser{}
		if legacy {
			b.SetRuntimeTextRedactor(func(value string) string { panic("unbounded callback must not run") })
		}
		b.recordWSEvent("wss://example.test?token=url-secret", "received", []byte(`{"token":"private"}`))
		evidence := b.wsEvidence(b.matchWSEvents(browserWSMatcher{Match: "private"})).([]any)[0].(map[string]any)
		if legacy {
			if evidence["payload_omitted"] != true || evidence["url"] != "" {
				t.Fatalf("legacy callback did not fail closed: %#v", evidence)
			}
		} else {
			encoded, _ := json.Marshal(evidence)
			if evidence["payload"] == nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "url-secret") {
				t.Fatalf("built-in scrub evidence = %s", encoded)
			}
		}
	}
}

func TestBrowserWSEvidenceFailsClosedForDirectBoundedCallback(t *testing.T) {
	b := &Browser{}
	b.SetRuntimeBoundedTextRedactor(func(string) (string, bool) { panic("unfenced projector must not be used") })
	b.recordWSEvent("wss://example.test", "received", []byte("private"))
	evidence := b.wsEvidence(b.matchWSEvents(browserWSMatcher{Match: "private"})).([]any)[0].(map[string]any)
	if evidence["payload_omitted"] != true || evidence["url"] != "" {
		t.Fatalf("unfenced callback was not withheld: %#v", evidence)
	}
}

func TestBrowserWSEvidenceWithoutBoundedRedactorFailsClosedWhenSecretActivityIsConfigured(t *testing.T) {
	for _, active := range []bool{false, true} {
		b := &Browser{}
		b.SetRuntimeSecretActivity(func() bool { return active })
		b.recordWSEvent("wss://example.test", "received", []byte("runtime-secret"))
		evidence := b.wsEvidence(b.matchWSEvents(browserWSMatcher{Match: "runtime-secret"})).([]any)[0].(map[string]any)
		if evidence["payload_omitted"] != true || evidence["url"] != "" {
			t.Fatalf("active=%v: unprojectable runtime evidence = %#v", active, evidence)
		}
	}
}

func TestBrowserWSGenerationFencesDelayedCallbacks(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(fmt.Sprintf("teardown=%v", retire), func(t *testing.T) {
			b := &Browser{}
			entered := make(chan struct{})
			resume := make(chan struct{})
			epoch, ok := b.wsEpochForPage(0)
			if !ok {
				t.Fatal("page observer unavailable")
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				close(entered)
				<-resume
				b.recordWSEventAt(epoch, 0, "wss://example.test", "received", []byte("old-frame"))
			}()
			<-entered
			if retire {
				if err := b.closeWithContext(context.Background()); err != nil {
					t.Fatalf("close browser: %v", err)
				}
			} else {
				b.resetWSEvents()
			}
			close(resume)
			<-done
			if got := b.matchWSEvents(browserWSMatcher{Match: "old-frame"}); len(got) != 0 {
				t.Fatal("stale callback repopulated the new generation")
			}
			if retire {
				if _, ok := b.wsEpochForPage(0); ok {
					t.Fatal("retired page observer is still valid")
				}
			} else {
				if _, ok := b.wsEpochForPage(0); !ok {
					t.Fatal("navigation invalidated the page observer")
				}
			}
			b.SetRuntimeBoundedTextRedactorSnapshot(nil)
			b.recordWSEvent("wss://example.test", "received", []byte("new-frame"))
			if got := b.matchWSEvents(browserWSMatcher{Match: "new-frame"}); len(got) != 1 {
				t.Fatal("new generation cannot observe frames")
			}
		})
	}
}

func TestBrowserWSOpenWindowResetKeepsExistingSockets(t *testing.T) {
	b := &Browser{}
	oldEpoch, ok := b.wsEpochForPage(0)
	if !ok {
		t.Fatal("page observer unavailable")
	}
	// An invalid open still resets the observation window, but leaves the
	// original page and its live sockets in place.
	if err, _ := b.dispatchAction(context.Background(), &protocol.Action{Type: "browser_open_window"}, &protocol.Result{}); err == nil {
		t.Fatal("empty target unexpectedly opened a window")
	}
	b.recordWSEventAt(oldEpoch, 0, "wss://example.test", "received", []byte("in-flight-old"))
	if got := b.matchWSEvents(browserWSMatcher{Match: "in-flight-old"}); len(got) != 0 {
		t.Fatal("old in-flight frame survived reset")
	}
	newEpoch, ok := b.wsEpochForPage(0)
	if !ok || newEpoch == oldEpoch {
		t.Fatal("existing socket was not retained with a new observation epoch")
	}
	b.recordWSEventAt(newEpoch, 0, "wss://example.test", "received", []byte("new-frame"))
	if got := b.matchWSEvents(browserWSMatcher{Match: "new-frame"}); len(got) != 1 {
		t.Fatal("live socket could not publish a frame after window reset")
	}
	b.resetWSEvents()
	if _, ok := b.wsEpochForPage(0); !ok {
		t.Fatal("navigation retired a socket in the live context")
	}
}

func TestBrowserWSObserverSurvivesResetButNotContextRetirement(t *testing.T) {
	b, page := &Browser{}, &wsTestPage{}
	b.observePageWebSockets(page, 0)
	socket := &wsTestSocket{url: "wss://example.test"}
	page.handler(socket)
	if socket.received == nil {
		t.Fatal("socket frame listener missing")
	}
	b.resetWSEvents()
	socket.received([]byte("after-reset"))
	if len(b.matchWSEvents(browserWSMatcher{Match: "after-reset"})) != 1 {
		t.Fatal("reset retired a live socket")
	}
	b.retireWSEvents()
	socket.received([]byte("after-retirement"))
	if len(b.matchWSEvents(browserWSMatcher{Match: "after-retirement"})) != 0 {
		t.Fatal("retired context accepted a frame")
	}
	late := &wsTestSocket{url: "wss://example.test"}
	page.handler(late)
	if late.received != nil {
		t.Fatal("retired context registered a socket")
	}
}

func TestBrowserWSResetDuringProjectionDoesNotPublishOrAdvance(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(fmt.Sprintf("teardown=%v", retire), func(t *testing.T) {
			b := &Browser{}
			b.recordWSEvent("wss://example.test", "received", []byte("secret"))
			entered, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			setWSTestRedactor(b, func(value string) (string, bool) {
				once.Do(func() { close(entered); <-resume })
				return strings.ReplaceAll(value, "secret", "[REDACTED]"), true
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := &protocol.Result{}
			done := make(chan error, 1)
			go func() {
				err, _ := b.dispatchWSWait(ctx, &protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": "secret"}}, result)
				done <- err
			}()
			<-entered
			if retire {
				if err := b.closeWithContext(context.Background()); err != nil {
					t.Fatalf("close browser: %v", err)
				}
			} else {
				b.resetWSEvents()
			}
			cancel()
			close(resume)
			if err := <-done; err == nil {
				t.Fatal("stale wait returned success")
			}
			if result.ProtocolEvidence != nil || b.currentWSWatermark() != 0 {
				t.Fatalf("stale result or watermark was published: %#v", result.ProtocolEvidence)
			}
			b.recordWSEvent("wss://example.test", "received", []byte("fresh"))
			if got := b.matchWSEvents(browserWSMatcher{Match: "fresh", MinSeq: b.currentWSWatermark()}); len(got) != 1 {
				t.Fatal("stale watermark skipped fresh frame")
			}
		})
	}
}

func TestBrowserWSAssertResetDuringProjectionDoesNotPublish(t *testing.T) {
	b := &Browser{}
	b.recordWSEvent("wss://example.test", "received", []byte("secret"))
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	setWSTestRedactor(b, func(value string) (string, bool) {
		once.Do(func() { close(entered); <-resume })
		return strings.ReplaceAll(value, "secret", "[REDACTED]"), true
	})
	result := &protocol.Result{}
	done := make(chan error, 1)
	go func() {
		err, _ := b.dispatchWSAssert(context.Background(), &protocol.Action{Type: "browser_ws_assert", Params: map[string]any{"match": "secret"}}, result)
		done <- err
	}()
	<-entered
	b.resetWSEvents()
	close(resume)
	if err := <-done; err == nil || result.ProtocolEvidence != nil {
		t.Fatal("assert published an invalidated observation")
	}
}

func TestBrowserWSCancellationDuringEvidencePublication(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
	}{
		{"wait timeout during projection", "wait"},
		{"wait timeout at commit", "wait-commit"},
		{"assert during projection", "assert"},
		{"assert at commit", "assert-commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Browser{}
			b.recordWSEvent("wss://example.test", "received", []byte("secret"))
			entered, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			block := func() { once.Do(func() { close(entered); <-resume }) }
			b.SetRuntimeBoundedTextRedactorSnapshot(func() (protocol.BoundedTextRedactor, protocol.BoundedTextRedactorCommitGuard) {
				return func(value string) (string, bool) {
						if !strings.HasSuffix(tc.mode, "-commit") {
							block()
						}
						return value, true
					}, func(commit func() bool) bool {
						if strings.HasSuffix(tc.mode, "-commit") {
							block()
						}
						return commit()
					}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := &protocol.Result{}
			done := make(chan error, 1)
			go func() {
				var err error
				if strings.HasPrefix(tc.mode, "wait") {
					err, _ = b.dispatchWSWait(ctx, &protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": "secret", "count": 2, "timeout_ms": 10}}, result)
				} else {
					err, _ = b.dispatchWSAssert(ctx, &protocol.Action{Type: "browser_ws_assert", Params: map[string]any{"match": "secret"}}, result)
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("projection or commit did not begin")
			}
			cancel()
			close(resume)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || result.ProtocolEvidence != nil || b.currentWSWatermark() != 0 {
					t.Fatalf("cancellation error=%v evidence=%#v watermark=%d", err, result.ProtocolEvidence, b.currentWSWatermark())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled action did not finish")
			}
		})
	}
}

func TestBrowserWSPolicyChangesDuringProjectionFailClosed(t *testing.T) {
	for _, replaceSetter := range []bool{false, true} {
		t.Run(fmt.Sprintf("setter=%v", replaceSetter), func(t *testing.T) {
			b := &Browser{}
			b.recordWSEvent("wss://example.test", "received", []byte("secret"))
			entered, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			var version atomic.Int64
			version.Store(1)
			b.SetRuntimeBoundedTextRedactorSnapshot(func() (protocol.BoundedTextRedactor, protocol.BoundedTextRedactorCommitGuard) {
				captured := version.Load()
				return func(value string) (string, bool) {
					once.Do(func() { close(entered); <-resume })
					return value, true
				}, func(commit func() bool) bool { return version.Load() == captured && commit() }
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := &protocol.Result{}
			done := make(chan error, 1)
			go func() {
				err, _ := b.dispatchWSWait(ctx, &protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": "secret"}}, result)
				done <- err
			}()
			<-entered
			if replaceSetter {
				b.SetRuntimeBoundedTextRedactorSnapshot(nil)
			} else {
				version.Add(1)
			}
			cancel()
			close(resume)
			if err := <-done; err == nil || result.ProtocolEvidence != nil || b.currentWSWatermark() != 0 {
				t.Fatal("stale policy evidence or watermark was published")
			}
		})
	}
}

func TestBrowserWSPolicyGuardFencesEvidenceCommit(t *testing.T) {
	b := &Browser{}
	b.recordWSEvent("wss://example.test", "received", []byte("secret"))
	var fence sync.RWMutex
	version := 1
	entered, release := make(chan struct{}), make(chan struct{})
	b.SetRuntimeBoundedTextRedactorSnapshot(func() (protocol.BoundedTextRedactor, protocol.BoundedTextRedactorCommitGuard) {
		fence.RLock()
		captured := version
		fence.RUnlock()
		return func(value string) (string, bool) { return strings.ReplaceAll(value, "secret", "[REDACTED]"), true }, func(commit func() bool) bool {
			fence.RLock()
			defer fence.RUnlock()
			if version != captured {
				return false
			}
			close(entered)
			<-release
			return commit()
		}
	})
	result := &protocol.Result{}
	done := make(chan error, 1)
	go func() {
		err, _ := b.dispatchWSAssert(context.Background(), &protocol.Action{Type: "browser_ws_assert", Params: map[string]any{"match": "secret"}}, result)
		done <- err
	}()
	<-entered
	if fence.TryLock() {
		fence.Unlock()
		t.Fatal("policy fence was released before evidence commit")
	}
	storeStarted, storeDone := make(chan struct{}), make(chan struct{})
	go func() { close(storeStarted); fence.Lock(); version++; fence.Unlock(); close(storeDone) }()
	<-storeStarted
	select {
	case <-storeDone:
		t.Fatal("policy changed during the guarded commit")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("guarded assert: %v", err)
	}
	<-storeDone
	encoded, err := json.Marshal(result.ProtocolEvidence)
	if err != nil || strings.Contains(string(encoded), "secret") {
		t.Fatalf("guarded evidence = %s, %v", encoded, err)
	}
}

func TestBrowserWSTruncatedEvidenceIsOmittedUnlessDebug(t *testing.T) {
	for _, debug := range []bool{false, true} {
		b := &Browser{unredactedDebug: debug}
		secret := "prefix-secret"
		setWSTestRedactor(b, func(value string) (string, bool) { return strings.ReplaceAll(value, secret, "[REDACTED]"), true })
		payload := strings.Repeat("a", browserWSPayloadLimitBytes-6) + secret
		url := "wss://example.test/" + strings.Repeat("a", browserWSURLLimitBytes-25) + secret
		b.recordWSEvent(url, "received", []byte(payload))
		matches := b.matchWSEvents(browserWSMatcher{Match: "prefix"})
		if len(matches) != 1 || !matches[0].Truncated || !matches[0].urlTruncated {
			t.Fatalf("debug=%v truncation not recorded", debug)
		}
		evidence := b.wsEvidence(matches).([]any)[0].(map[string]any)
		if debug {
			if evidence["payload"] == nil || evidence["url"] == "" {
				t.Fatal("debug evidence lost observed prefixes")
			}
		} else if evidence["payload_omitted"] != true || evidence["url_omitted"] != true || evidence["payload"] != nil || evidence["url"] != "" {
			t.Fatalf("partial-secret evidence was not omitted: %#v", evidence)
		}
	}
}

func TestBrowserWSInvalidBytesAreOmittedUnlessDebug(t *testing.T) {
	for _, debug := range []bool{false, true} {
		b := &Browser{unredactedDebug: debug}
		b.recordWSEvent("wss://example.test", "received", []byte{0xff, 0, 'x'})
		matches := b.matchWSEvents(browserWSMatcher{Match: base64.StdEncoding.EncodeToString([]byte{0xff, 0, 'x'}), MatchMode: "exact"})
		if len(matches) != 1 {
			t.Fatal("binary frame did not match")
		}
		evidence := b.wsEvidence(matches).([]any)[0].(map[string]any)
		if debug && evidence["encoding"] != "base64" {
			t.Fatalf("debug lost binary evidence: %#v", evidence)
		}
		if !debug && (evidence["payload_omitted"] != true || evidence["payload"] != nil) {
			t.Fatalf("binary evidence not omitted: %#v", evidence)
		}
	}
}

func TestBrowserWSRingEvictionAndResetDropMatcherReferences(t *testing.T) {
	b := &Browser{}
	for i := 0; i <= browserWSRingLimit; i++ {
		b.recordWSEvent("wss://example.test", "received", []byte(fmt.Sprintf("frame-%d", i)))
	}
	if got := b.matchWSEvents(browserWSMatcher{Match: "frame-0", MatchMode: "exact"}); len(got) != 0 {
		t.Fatal("evicted payload remained matchable")
	}
	if len(b.wsEvents) != browserWSRingLimit {
		t.Fatal("ring limit not enforced")
	}
	if cap(b.wsEvents) > len(b.wsEvents) && b.wsEvents[:cap(b.wsEvents)][len(b.wsEvents)].matchPayload != "" {
		t.Fatal("ring tail retained matcher payload")
	}
	b.resetWSEvents()
	if b.wsEvents != nil || len(b.matchWSEvents(browserWSMatcher{Match: "frame-256"})) != 0 {
		t.Fatal("reset retained matcher state")
	}
}

func TestBrowserWSConcurrentRecordMatchAndReset(t *testing.T) {
	b := &Browser{}
	var workers sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := 0; i < 150; i++ {
				switch worker {
				case 0:
					b.recordWSEvent("wss://example.test", "received", []byte(`{"token":"secret"}`))
				case 1:
					_ = b.wsEvidence(b.matchWSEvents(browserWSMatcher{Match: "secret"}))
				case 2:
					b.resetWSEvents()
				}
			}
		}(worker)
	}
	workers.Wait()
}
