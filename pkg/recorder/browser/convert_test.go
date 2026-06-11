package browser

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

func recordedSession() *Session {
	loginInput := &selector.ElementMetadata{Tag: "input", Name: "username", CSSPath: "form > input"}
	submitBtn := &selector.ElementMetadata{
		Tag: "button", DataTestID: "login-btn",
		Attributes: map[string]string{"data-testid": "login-btn"},
		Text:       "Sign in", CSSPath: "form > button",
	}
	status := &selector.ElementMetadata{Tag: "div", ID: "status", Text: "Welcome back", CSSPath: "#status"}
	session := &Session{
		ID:        "rec_test",
		StartURL:  "https://shop.example/login?next=%2Fcart",
		StartedAt: ts(0),
	}
	session.appendStep(Step{Kind: StepFill, Value: "jason", Timestamp: ts(1000), URL: "https://shop.example/login", Element: loginInput, Candidates: selector.Candidates(*loginInput)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(3000), URL: "https://shop.example/login", Element: submitBtn, Candidates: selector.Candidates(*submitBtn)})
	session.appendStep(Step{Kind: StepAssert, Value: "Welcome back", Timestamp: ts(6000), URL: "https://shop.example/account", Element: status, Candidates: selector.Candidates(*status)})
	session.NavEvents = []NavEvent{
		{Timestamp: ts(500), URL: "https://shop.example/login?next=%2Fcart"},
		{Timestamp: ts(3400), URL: "https://shop.example/account"},
	}
	session.NetworkEvents = []time.Time{ts(3100), ts(3200), ts(3300)}
	return session
}

func TestConvertProducesBrowserScript(t *testing.T) {
	session := recordedSession()
	result := Convert(session, ConvertOptions{ScriptName: "Login Flow"})
	script := result.Script
	if script == nil {
		t.Fatal("Convert() returned nil script")
	}
	if script.Protocol != "browser" {
		t.Errorf("Protocol = %q, want browser", script.Protocol)
	}
	if script.Name != "Login Flow" {
		t.Errorf("Name = %q", script.Name)
	}
	if script.Config.BaseURL != "https://shop.example" {
		t.Errorf("BaseURL = %q", script.Config.BaseURL)
	}
	if script.Config.Browser.Engine != "chromium" {
		t.Errorf("Browser.Engine = %q", script.Config.Browser.Engine)
	}
	if script.GeneratedBy != "browser-recorder" {
		t.Errorf("GeneratedBy = %q", script.GeneratedBy)
	}

	// navigate, fill, click, verify
	if len(script.Actions) != 4 {
		t.Fatalf("actions = %d, want 4: %+v", len(script.Actions), actionTypes(script.Actions))
	}
	nav := script.Actions[0]
	if nav.Type != "browser_navigate" || nav.Target != "/login?next=%2Fcart" {
		t.Errorf("initial navigate = %+v", nav)
	}
	fill := script.Actions[1]
	if fill.Type != "browser_fill" || fill.Value != "jason" || fill.Selector != `input[name="username"]` {
		t.Errorf("fill = %+v", fill)
	}
	click := script.Actions[2]
	if click.Type != "browser_click" || click.Selector != `[data-testid="login-btn"]` {
		t.Errorf("click = %+v", click)
	}
	verify := script.Actions[3]
	if verify.Type != "browser_wait" || len(verify.Assertions) != 2 {
		t.Fatalf("verify = %+v", verify)
	}
	if verify.Assertions[0].Type != "element_visible" || verify.Assertions[1].Type != "element_text_contains" {
		t.Errorf("assertions = %+v", verify.Assertions)
	}
	if verify.Assertions[1].Expected != "Welcome back" {
		t.Errorf("assert expected = %v", verify.Assertions[1].Expected)
	}
}

func TestConvertSyncThinkTimeAndMetadata(t *testing.T) {
	session := recordedSession()
	result := Convert(session, ConvertOptions{})
	script := result.Script

	fill := script.Actions[1]
	// fill at 1000 → click at 3000: 2000ms think time after fill
	if !fill.ThinkTimeAfter || fill.ThinkTime.Type != "fixed" || fill.ThinkTime.MeanMs != 2000 {
		t.Errorf("fill think time = after=%v %+v, want fixed 2000ms", fill.ThinkTimeAfter, fill.ThinkTime)
	}
	click := script.Actions[2]
	// click at 3000 triggered nav at 3400 → load sync
	if click.Sync == nil || click.Sync.Type != "load" {
		t.Errorf("click sync = %+v, want load", click.Sync)
	}
	if click.Metadata == nil {
		t.Fatal("click metadata missing")
	}
	candidates, ok := click.Metadata["source_selector_candidates"].([]selector.Candidate)
	if !ok || len(candidates) < 2 {
		t.Errorf("source_selector_candidates = %v", click.Metadata["source_selector_candidates"])
	}
	fallbacks, ok := click.Metadata["source_selector_fallbacks"].([]selector.Candidate)
	if !ok || len(fallbacks) != len(candidates)-1 {
		t.Errorf("source_selector_fallbacks = %v", click.Metadata["source_selector_fallbacks"])
	}
	if click.Metadata["source_url"] != "https://shop.example/login" {
		t.Errorf("source_url = %v", click.Metadata["source_url"])
	}
	if result.Stats.SyncPolicies == 0 || result.Stats.Assertions != 2 || result.Stats.Navigations != 1 {
		t.Errorf("stats = %+v", result.Stats)
	}
}

func TestConvertMaxThinkTimeCap(t *testing.T) {
	session := recordedSession()
	result := Convert(session, ConvertOptions{MaxThinkTimeMs: 1500})
	fill := result.Script.Actions[1]
	if fill.ThinkTime.MeanMs != 1500 {
		t.Errorf("capped think time = %v, want 1500", fill.ThinkTime.MeanMs)
	}
}

func TestConvertExplicitNavigationInterleaved(t *testing.T) {
	session := recordedSession()
	// A typed navigation long after the last interaction becomes an action.
	session.NavEvents = append(session.NavEvents, NavEvent{Timestamp: ts(60000), URL: "https://shop.example/orders"})
	result := Convert(session, ConvertOptions{})
	types := actionTypes(result.Script.Actions)
	if len(result.Script.Actions) != 5 {
		t.Fatalf("actions = %v", types)
	}
	last := result.Script.Actions[4]
	if last.Type != "browser_navigate" || last.Target != "/orders" {
		t.Errorf("explicit navigation = %+v", last)
	}
}

func TestConvertFrameAndShadowMetadata(t *testing.T) {
	el := &selector.ElementMetadata{Tag: "input", Name: "card", CSSPath: "pay-widget input", InShadowDOM: true}
	session := &Session{StartURL: "https://shop.example/pay"}
	session.appendStep(Step{
		Kind: StepFill, Value: "4111", Timestamp: ts(1000),
		FrameSelector: `iframe[name="payframe"]`,
		Element:       el, Candidates: selector.Candidates(*el),
	})
	result := Convert(session, ConvertOptions{})
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %v", actionTypes(result.Script.Actions))
	}
	fill := result.Script.Actions[1]
	if fill.Metadata["frame_selector"] != `iframe[name="payframe"]` {
		t.Errorf("frame_selector = %v", fill.Metadata["frame_selector"])
	}
	if fill.Metadata["source_shadow_dom"] != true {
		t.Errorf("source_shadow_dom = %v", fill.Metadata["source_shadow_dom"])
	}
}

func TestConvertWarningsAndSkips(t *testing.T) {
	noSelector := &selector.ElementMetadata{} // nothing usable
	session := &Session{StartURL: "https://x.example/"}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: noSelector})
	uploadInput := &selector.ElementMetadata{Tag: "input", ID: "upload"}
	session.appendStep(Step{Kind: StepSetFiles, Value: "resume.pdf", Timestamp: ts(2000), Element: uploadInput, Candidates: selector.Candidates(*uploadInput)})
	session.WebSockets = []WebSocketSummary{{URL: "wss://x.example/live"}}
	result := Convert(session, ConvertOptions{})

	if result.Stats.SkippedSteps != 1 {
		t.Errorf("skipped = %d, want 1", result.Stats.SkippedSteps)
	}
	wantFragments := []string{"no replayable selector", "uploads file(s)", "WebSocket connection(s) observed"}
	for _, fragment := range wantFragments {
		found := false
		for _, warning := range result.Warnings {
			if strings.Contains(warning, fragment) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("warnings missing %q: %v", fragment, result.Warnings)
		}
	}
}

func TestConvertEmptySession(t *testing.T) {
	result := Convert(&Session{StartURL: "https://x.example/home"}, ConvertOptions{})
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "browser_navigate" {
		t.Errorf("empty session actions = %v", actionTypes(result.Script.Actions))
	}
	if result.Script.Name != "Recorded Browser Script" {
		t.Errorf("default name = %q", result.Script.Name)
	}
}

func TestSplitStartURL(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantBase   string
		wantTarget string
	}{
		{name: "path and query", input: "https://a.example/p?q=1", wantBase: "https://a.example", wantTarget: "/p?q=1"},
		{name: "bare host", input: "https://a.example", wantBase: "https://a.example", wantTarget: "/"},
		{name: "not absolute", input: "/relative", wantBase: "", wantTarget: "/relative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, target := splitStartURL(tt.input)
			if base != tt.wantBase || target != tt.wantTarget {
				t.Errorf("splitStartURL(%q) = (%q, %q), want (%q, %q)", tt.input, base, target, tt.wantBase, tt.wantTarget)
			}
		})
	}
}

func actionTypes(actions []dsl.Action) []string {
	types := make([]string, 0, len(actions))
	for _, action := range actions {
		types = append(types, action.Type)
	}
	return types
}
