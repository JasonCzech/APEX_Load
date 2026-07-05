package browser

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestConvertPreservesDeviceProfileBrowserEngine(t *testing.T) {
	tests := []struct {
		name          string
		deviceProfile string
		wantEngine    string
	}{
		{name: "known webkit iphone", deviceProfile: "iphone_15_pro", wantEngine: "webkit"},
		{name: "known chromium android", deviceProfile: "pixel_8", wantEngine: "chromium"},
		{name: "unknown ios style", deviceProfile: "ipad_enterprise_lab", wantEngine: "webkit"},
		{name: "unknown desktop style", deviceProfile: "desktop_lab", wantEngine: "chromium"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := recordedSession()
			session.DeviceProfile = tt.deviceProfile
			result := Convert(session, ConvertOptions{ScriptName: "Device Flow"})
			if result.Script.Config.Browser.DeviceProfile != tt.deviceProfile {
				t.Fatalf("device_profile = %q, want %q", result.Script.Config.Browser.DeviceProfile, tt.deviceProfile)
			}
			if result.Script.Config.Browser.Engine != tt.wantEngine {
				t.Fatalf("browser engine = %q, want %q", result.Script.Config.Browser.Engine, tt.wantEngine)
			}
		})
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
		FramePath:     []string{`iframe#checkout`, `iframe[name="payframe"]`},
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
	if got, ok := fill.Metadata["frame_path"].([]string); !ok || strings.Join(got, "|") != `iframe#checkout|iframe[name="payframe"]` {
		t.Errorf("frame_path = %#v", fill.Metadata["frame_path"])
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

func TestConvertExpandedBrowserCaptureActions(t *testing.T) {
	source := &selector.ElementMetadata{Tag: "div", ID: "card", CSSPath: "#card"}
	target := &selector.ElementMetadata{Tag: "div", DataTestID: "lane-done", Attributes: map[string]string{"data-testid": "lane-done"}, CSSPath: "#done"}
	upload := &selector.ElementMetadata{Tag: "input", ID: "avatar", Type: "file", CSSPath: "#avatar"}
	button := &selector.ElementMetadata{Tag: "button", ID: "row", CSSPath: "#row"}
	menu := &selector.ElementMetadata{Tag: "button", ID: "row-menu", CSSPath: "#row-menu"}
	hoverMenu := &selector.ElementMetadata{Tag: "button", ID: "account-menu", CSSPath: "#account-menu"}
	selectEl := &selector.ElementMetadata{Tag: "select", ID: "shipping", CSSPath: "#shipping"}
	base := ts(0)
	session := &Session{ID: "rec_expanded", StartURL: "https://shop.example/board", StartedAt: base}
	session.appendStep(Step{Kind: StepDblClick, Timestamp: ts(1000), Element: button, Candidates: selector.Candidates(*button)})
	session.appendStep(Step{Kind: StepClick, Button: "right", Modifiers: []string{"Shift"}, Timestamp: ts(1500), Element: menu, Candidates: selector.Candidates(*menu)})
	session.appendStep(Step{Kind: StepHover, Timestamp: ts(1650), Element: hoverMenu, Candidates: selector.Candidates(*hoverMenu)})
	session.appendStep(Step{Kind: StepScroll, DeltaY: 420, ScrollY: 420, Timestamp: ts(1750)})
	session.appendStep(Step{
		Kind:             StepDragAndDrop,
		Timestamp:        ts(2000),
		Element:          source,
		Candidates:       selector.Candidates(*source),
		TargetSelector:   `[data-testid="lane-done"]`,
		TargetCandidates: selector.Candidates(*target),
	})
	session.appendStep(Step{Kind: StepSelect, Value: "standard", Values: []string{"standard", "express"}, Timestamp: ts(2500), Element: selectEl, Candidates: selector.Candidates(*selectEl)})
	session.appendStep(Step{Kind: StepSetFiles, Value: "avatar.png", Timestamp: ts(3000), Element: upload, Candidates: selector.Candidates(*upload)})
	session.WebSockets = []WebSocketSummary{{
		URL:      "wss://shop.example/live",
		OpenedAt: ts(3500),
		Frames: []WebSocketFrame{{
			Direction: "received",
			Payload:   `{"type":"ready","token":"abc"}`,
			Timestamp: ts(3600),
		}},
	}}

	result := Convert(session, ConvertOptions{})
	types := actionTypes(result.Script.Actions)
	want := []string{"browser_navigate", "browser_dblclick", "browser_click", "browser_hover", "browser_scroll", "browser_drag_and_drop", "browser_select", "browser_set_files", "browser_ws_wait"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", types, want)
	}
	click := result.Script.Actions[2]
	if click.Params["button"] != "right" || fmt.Sprint(click.Params["modifiers"]) != "[Shift]" {
		t.Errorf("right-click params = %+v", click.Params)
	}
	hover := result.Script.Actions[3]
	if hover.Type != "browser_hover" || hover.Selector != "#account-menu" {
		t.Errorf("hover action = %+v", hover)
	}
	scroll := result.Script.Actions[4]
	if scroll.Params["delta_y"] != 420 || scroll.Selector != "" {
		t.Errorf("scroll action = %+v", scroll)
	}
	drag := result.Script.Actions[5]
	if drag.Params["target"] != `[data-testid="lane-done"]` {
		t.Errorf("drag target = %+v", drag.Params)
	}
	if drag.Metadata["recorder_step_seq"] != 5 {
		t.Errorf("drag recorder_step_seq = %v", drag.Metadata["recorder_step_seq"])
	}
	if drag.Metadata["source_target_selector_candidates"] == nil {
		t.Errorf("drag missing target candidates metadata: %+v", drag.Metadata)
	}
	selectAction := result.Script.Actions[6]
	if selectAction.Value != "standard" || fmt.Sprint(selectAction.Params["values"]) != "[standard express]" {
		t.Errorf("select action = %+v", selectAction)
	}
	uploadAction := result.Script.Actions[7]
	if uploadAction.Metadata["source_upload_fixture"] != true {
		t.Errorf("upload metadata = %+v", uploadAction.Metadata)
	}
	ws := result.Script.Actions[8]
	if ws.Params["direction"] != "received" || ws.Params["match_mode"] != "contains" {
		t.Errorf("ws params = %+v", ws.Params)
	}
	if !strings.Contains(fmt.Sprint(ws.Params["match"]), "ready") {
		t.Errorf("ws match = %v", ws.Params["match"])
	}
}

func TestConvertMaskedFillEmitsEnvPlaceholder(t *testing.T) {
	passwordInput := &selector.ElementMetadata{Tag: "input", Name: "user_password", Type: "password", CSSPath: "form > input"}
	session := &Session{ID: "rec_pw", StartURL: "https://shop.example/login", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepFill, Value: "", Masked: true, Timestamp: ts(1000), Element: passwordInput, Candidates: selector.Candidates(*passwordInput)})

	result := Convert(session, ConvertOptions{})
	if len(result.Script.Actions) != 2 { // navigate + fill
		t.Fatalf("actions = %+v", actionTypes(result.Script.Actions))
	}
	fill := result.Script.Actions[1]
	if fill.Type != "browser_fill" || fill.Value != "{{env.USER_PASSWORD}}" {
		t.Errorf("masked fill = %+v, want {{env.USER_PASSWORD}} placeholder", fill)
	}
	if fill.Metadata["source_masked_secret"] != true {
		t.Errorf("metadata = %+v, want source_masked_secret=true", fill.Metadata)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "USER_PASSWORD") && strings.Contains(w, "masked") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want masked-secret guidance", result.Warnings)
	}
}

func TestSecretEnvKey(t *testing.T) {
	tests := []struct {
		name string
		step Step
		want string
	}{
		{"name attribute", Step{Element: &selector.ElementMetadata{Name: "user_password"}}, "USER_PASSWORD"},
		{"id fallback", Step{Element: &selector.ElementMetadata{ID: "loginPwd"}}, "LOGINPWD"},
		{"placeholder with punctuation", Step{Element: &selector.ElementMetadata{Placeholder: "Your password!"}}, "YOUR_PASSWORD"},
		{"digit prefix gets SECRET prefix", Step{Element: &selector.ElementMetadata{Name: "2fa-code"}}, "SECRET_2FA_CODE"},
		{"no element falls back", Step{}, "PASSWORD"},
		{"non-alphanumeric only falls back", Step{Element: &selector.ElementMetadata{Name: "----"}}, "PASSWORD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := secretEnvKey(tt.step); got != tt.want {
				t.Errorf("secretEnvKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConvertTransactionsAndNotes(t *testing.T) {
	button := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &Session{ID: "rec_txn", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.Markers = append(session.Markers,
		Marker{Kind: MarkerNote, Text: "login first", Timestamp: ts(500)},
		Marker{Kind: MarkerTransactionStart, Name: "Checkout", Timestamp: ts(900)},
		Marker{Kind: MarkerTransactionStart, Name: "Payment", Timestamp: ts(1500)},
		Marker{Kind: MarkerTransactionEnd, Name: "Payment", Timestamp: ts(2500)},
		Marker{Kind: MarkerTransactionEnd, Name: "Checkout", Timestamp: ts(3500)},
		Marker{Kind: MarkerNote, Text: "trailing observation", Timestamp: ts(4000)},
	)
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: button, Candidates: selector.Candidates(*button)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(2000), Element: button, Candidates: selector.Candidates(*button)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(3000), Element: button, Candidates: selector.Candidates(*button)})

	result := Convert(session, ConvertOptions{})
	types := actionTypes(result.Script.Actions)
	// navigate, start(Checkout), click, start(Payment), click, end(Payment), click, end(Checkout)
	want := []string{"browser_navigate", "source_transaction", "browser_click", "source_transaction", "browser_click", "source_transaction", "browser_click", "source_transaction"}
	if len(types) != len(want) {
		t.Fatalf("actions = %v, want %v", types, want)
	}
	for i, typ := range want {
		if types[i] != typ {
			t.Fatalf("action[%d] = %q, want %q (all: %v)", i, types[i], typ, types)
		}
	}

	start := result.Script.Actions[1]
	if start.Name != "Checkout" || start.Params["function"] != "lr_start_transaction" {
		t.Errorf("start action = %+v", start)
	}
	if start.Metadata["note"] != "login first" {
		t.Errorf("note metadata = %+v, want queued note on next action", start.Metadata)
	}
	end := result.Script.Actions[7]
	if end.Name != "Checkout" || end.Params["function"] != "lr_end_transaction" {
		t.Errorf("end action = %+v", end)
	}

	// Enclosed actions carry the innermost transaction tag.
	if got := result.Script.Actions[2].Metadata["source_transaction"]; got != "Checkout" {
		t.Errorf("click 1 transaction = %v, want Checkout", got)
	}
	if got := result.Script.Actions[4].Metadata["source_transaction"]; got != "Payment" {
		t.Errorf("click 2 transaction = %v, want Payment (innermost)", got)
	}
	if got := result.Script.Actions[6].Metadata["source_transaction"]; got != "Checkout" {
		t.Errorf("click 3 transaction = %v, want Checkout after Payment closed", got)
	}

	// The trailing note lands on the final action instead of disappearing.
	if got := end.Metadata["note"]; got != "trailing observation" {
		t.Errorf("trailing note = %v", got)
	}

	if result.Stats.Transactions != 2 || result.Stats.Notes != 2 {
		t.Errorf("stats = %+v, want 2 transactions and 2 notes", result.Stats)
	}
}

func TestConvertTransactionEndWithoutStartWarns(t *testing.T) {
	session := &Session{ID: "rec_bad", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.Markers = append(session.Markers, Marker{Kind: MarkerTransactionEnd, Name: "Ghost", Timestamp: ts(100)})

	result := Convert(session, ConvertOptions{})
	if len(result.Script.Actions) != 1 { // navigate only
		t.Fatalf("actions = %v", actionTypes(result.Script.Actions))
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "Ghost") && strings.Contains(w, "no matching start") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v", result.Warnings)
	}
}

func TestConvertSubtractsPauseFromThinkTime(t *testing.T) {
	button := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &Session{ID: "rec_pause", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: button, Candidates: selector.Candidates(*button)})
	// 61s gap, of which a 60s pause window: real think time is ~1s.
	session.PauseIntervals = []PauseInterval{{From: ts(1500), To: ts(61500)}}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(62000), Element: button, Candidates: selector.Candidates(*button)})

	result := Convert(session, ConvertOptions{})
	first := result.Script.Actions[1]
	if !first.ThinkTimeAfter {
		t.Fatalf("first click missing think time: %+v", first)
	}
	if first.ThinkTime.MeanMs != 1000 {
		t.Errorf("think time = %v ms, want 1000 (pause subtracted)", first.ThinkTime.MeanMs)
	}
}

func TestConvertPauseSwallowsSubPerceptualGap(t *testing.T) {
	button := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &Session{ID: "rec_pause2", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: button, Candidates: selector.Candidates(*button)})
	// The entire 10s gap was paused: no think time should be emitted.
	session.PauseIntervals = []PauseInterval{{From: ts(1100), To: ts(10900)}}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(11000), Element: button, Candidates: selector.Candidates(*button)})

	result := Convert(session, ConvertOptions{})
	if result.Script.Actions[1].ThinkTimeAfter {
		t.Errorf("think time emitted despite fully-paused gap: %+v", result.Script.Actions[1].ThinkTime)
	}
}

func TestPausedDurationBetween(t *testing.T) {
	tests := []struct {
		name      string
		intervals []PauseInterval
		from, to  time.Time
		want      time.Duration
	}{
		{"no intervals", nil, ts(0), ts(1000), 0},
		{"fully inside", []PauseInterval{{From: ts(200), To: ts(700)}}, ts(0), ts(1000), 500 * time.Millisecond},
		{"clamped both ends", []PauseInterval{{From: ts(0), To: ts(2000)}}, ts(500), ts(1500), time.Second},
		{"outside window", []PauseInterval{{From: ts(2000), To: ts(3000)}}, ts(0), ts(1000), 0},
		{"open interval clamps to window end", []PauseInterval{{From: ts(500)}}, ts(0), ts(1000), 500 * time.Millisecond},
		{"multiple intervals sum", []PauseInterval{{From: ts(100), To: ts(200)}, {From: ts(300), To: ts(400)}}, ts(0), ts(1000), 200 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pausedDurationBetween(tt.intervals, tt.from, tt.to); got != tt.want {
				t.Errorf("pausedDurationBetween() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvertPartitionsSections(t *testing.T) {
	btn := func(id string) *selector.ElementMetadata {
		return &selector.ElementMetadata{Tag: "button", ID: id, CSSPath: "#" + id}
	}
	session := &Session{ID: "rec_sec", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepClick, Section: SectionInit, Timestamp: ts(1000), Element: btn("login"), Candidates: selector.Candidates(*btn("login"))})
	session.appendStep(Step{Kind: StepClick, Section: SectionActions, Timestamp: ts(2000), Element: btn("buy"), Candidates: selector.Candidates(*btn("buy"))})
	session.appendStep(Step{Kind: StepClick, Section: SectionTeardown, Timestamp: ts(3000), Element: btn("logout"), Candidates: selector.Candidates(*btn("logout"))})

	result := Convert(session, ConvertOptions{})
	script := result.Script
	// initial navigate inherits the first step's section (init)
	if len(script.Init) != 2 || script.Init[0].Type != "browser_navigate" || script.Init[1].Selector != "#login" {
		t.Fatalf("init = %+v", actionTypes(script.Init))
	}
	if len(script.Actions) != 1 || script.Actions[0].Selector != "#buy" {
		t.Fatalf("actions = %+v", actionTypes(script.Actions))
	}
	if len(script.Teardown) != 1 || script.Teardown[0].Selector != "#logout" {
		t.Fatalf("teardown = %+v", actionTypes(script.Teardown))
	}
}

func TestConvertPreStepMarkersUseFirstStepSection(t *testing.T) {
	btn := &selector.ElementMetadata{Tag: "button", ID: "setup", CSSPath: "#setup"}
	session := &Session{ID: "rec_sec_marker", StartURL: "https://shop.example/setup", StartedAt: ts(0)}
	session.Markers = append(session.Markers,
		Marker{Kind: MarkerTransactionStart, Name: "Setup", Timestamp: ts(500)},
		Marker{Kind: MarkerTransactionEnd, Name: "Setup", Timestamp: ts(1500)},
	)
	session.appendStep(Step{Kind: StepClick, Section: SectionInit, Timestamp: ts(1000), Element: btn, Candidates: selector.Candidates(*btn)})

	result := Convert(session, ConvertOptions{})
	if got := actionTypes(result.Script.Init); strings.Join(got, ",") != "browser_navigate,source_transaction,browser_click,source_transaction" {
		t.Fatalf("init actions = %v, want navigate + transaction wrapper + click", got)
	}
	if len(result.Script.Actions) != 0 || len(result.Script.Teardown) != 0 {
		t.Fatalf("non-init actions leaked: actions=%v teardown=%v", actionTypes(result.Script.Actions), actionTypes(result.Script.Teardown))
	}
}

func TestConvertSkipsExcludedAndDeleted(t *testing.T) {
	btn := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &Session{ID: "rec_skip", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: btn, Candidates: selector.Candidates(*btn)})
	session.appendStep(Step{Kind: StepClick, Excluded: true, Timestamp: ts(2000), Element: btn, Candidates: selector.Candidates(*btn)})
	session.appendStep(Step{Kind: StepClick, Deleted: true, Timestamp: ts(3000), Element: btn, Candidates: selector.Candidates(*btn)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(4000), Element: btn, Candidates: selector.Candidates(*btn)})

	result := Convert(session, ConvertOptions{})
	// navigate + 2 surviving clicks
	if got := actionTypes(result.Script.Actions); len(got) != 3 {
		t.Fatalf("actions = %v, want navigate + 2 clicks", got)
	}
	// Think time folds the excluded/deleted dwell into the gap between the
	// surviving clicks (1s -> 4s = 3s).
	first := result.Script.Actions[1]
	if !first.ThinkTimeAfter || first.ThinkTime.MeanMs != 3000 {
		t.Errorf("think time = %+v, want 3000ms across skipped steps", first.ThinkTime)
	}
}

func TestConvertTrimByStepRange(t *testing.T) {
	btn := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &Session{ID: "rec_trim", StartURL: "https://shop.example/", StartedAt: ts(0)}
	for i := 1; i <= 5; i++ {
		session.appendStep(Step{Kind: StepClick, Timestamp: ts(int64(i) * 1000), Element: btn, Candidates: selector.Candidates(*btn)})
	}
	result := Convert(session, ConvertOptions{StepRange: StepRange{StartSeq: 2, EndSeq: 4}})
	// navigate + steps 2,3,4
	if got := actionTypes(result.Script.Actions); len(got) != 4 {
		t.Fatalf("actions = %v, want navigate + 3 clicks", got)
	}
}

func TestConvertTrimByStepRangeKeepsBoundaryTransactionMarkers(t *testing.T) {
	btn := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &Session{ID: "rec_trim_txn", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.Markers = append(session.Markers,
		Marker{Kind: MarkerTransactionStart, Name: "Checkout", Timestamp: ts(500)},
		Marker{Kind: MarkerTransactionEnd, Name: "Checkout", Timestamp: ts(2500)},
	)
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: btn, Candidates: selector.Candidates(*btn)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(2000), Element: btn, Candidates: selector.Candidates(*btn)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(4000), Element: btn, Candidates: selector.Candidates(*btn)})

	result := Convert(session, ConvertOptions{StepRange: StepRange{StartSeq: 1, EndSeq: 2}})
	want := []string{"browser_navigate", "source_transaction", "browser_click", "browser_click", "source_transaction"}
	if got := actionTypes(result.Script.Actions); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	if result.Script.Actions[1].Params["function"] != "lr_start_transaction" || result.Script.Actions[4].Params["function"] != "lr_end_transaction" {
		t.Fatalf("boundary markers = %+v / %+v", result.Script.Actions[1], result.Script.Actions[4])
	}
}

func TestConvertSplitAtTransactions(t *testing.T) {
	btn := func(id string) *selector.ElementMetadata {
		return &selector.ElementMetadata{Tag: "button", ID: id, CSSPath: "#" + id}
	}
	session := &Session{ID: "rec_split", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.Markers = append(session.Markers,
		Marker{Kind: MarkerTransactionStart, Name: "Login", Timestamp: ts(500)},
		Marker{Kind: MarkerTransactionEnd, Name: "Login", Timestamp: ts(1500)},
		Marker{Kind: MarkerTransactionStart, Name: "Checkout", Timestamp: ts(2500)},
		Marker{Kind: MarkerTransactionEnd, Name: "Checkout", Timestamp: ts(3500)},
	)
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: btn("login"), Candidates: selector.Candidates(*btn("login"))})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(3000), Element: btn("buy"), Candidates: selector.Candidates(*btn("buy"))})

	result := Convert(session, ConvertOptions{SplitAtTransactions: true})
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	// Each split script has the initial navigate + its one click; no
	// source_transaction wrappers.
	for _, sc := range result.Scripts {
		for _, a := range sc.Actions {
			if a.Type == "source_transaction" {
				t.Errorf("split script %q still contains a source_transaction wrapper", sc.Name)
			}
		}
	}
	if !strings.Contains(result.Scripts[0].Name, "Login") || !strings.Contains(result.Scripts[1].Name, "Checkout") {
		t.Errorf("split names = %q, %q", result.Scripts[0].Name, result.Scripts[1].Name)
	}
}

func TestConvertSplitAtTransactionsUsesPreTransactionNavigation(t *testing.T) {
	btn := func(id string) *selector.ElementMetadata {
		return &selector.ElementMetadata{Tag: "button", ID: id, CSSPath: "#" + id}
	}
	session := &Session{ID: "rec_split_nav", StartURL: "https://shop.example/home", StartedAt: ts(0)}
	session.Markers = append(session.Markers,
		Marker{Kind: MarkerTransactionStart, Name: "Login", Timestamp: ts(500)},
		Marker{Kind: MarkerTransactionEnd, Name: "Login", Timestamp: ts(1500)},
		Marker{Kind: MarkerTransactionStart, Name: "Checkout", Timestamp: ts(9000)},
		Marker{Kind: MarkerTransactionEnd, Name: "Checkout", Timestamp: ts(11000)},
	)
	session.NavEvents = []NavEvent{
		{Timestamp: ts(100), URL: "https://shop.example/home"},
		{Timestamp: ts(5000), URL: "https://shop.example/cart?coupon=YES#review"},
	}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: btn("login"), Candidates: selector.Candidates(*btn("login"))})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(10000), Element: btn("buy"), Candidates: selector.Candidates(*btn("buy"))})

	result := Convert(session, ConvertOptions{SplitAtTransactions: true})
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	if got := result.Scripts[0].Actions[0].Target; got != "/home" {
		t.Fatalf("login script first navigate = %q, want /home", got)
	}
	if got := result.Scripts[1].Actions[0].Target; got != "/cart?coupon=YES#review" {
		t.Fatalf("checkout script first navigate = %q, want pre-transaction cart URL", got)
	}
}

func TestConvertDialogActionPlacedBeforeTrigger(t *testing.T) {
	btn := &selector.ElementMetadata{Tag: "button", ID: "del", CSSPath: "#del"}
	session := &Session{ID: "rec_dialog", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: btn, Candidates: selector.Candidates(*btn)})
	session.appendStep(Step{Kind: StepHandleDialog, DialogType: "confirm", DialogMessage: "Delete item?", Timestamp: ts(1100)})

	result := Convert(session, ConvertOptions{})
	types := actionTypes(result.Script.Actions)
	// navigate, handle_dialog (moved before its trigger), click
	want := []string{"browser_navigate", "browser_handle_dialog", "browser_click"}
	if len(types) != len(want) {
		t.Fatalf("actions = %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("action[%d] = %q, want %q", i, types[i], want[i])
		}
	}
	dlg := result.Script.Actions[1]
	if dlg.Params["action"] != "accept" || dlg.Params["expected_message"] != "Delete item?" || dlg.Params["dialog_type"] != "confirm" {
		t.Errorf("dialog params = %+v", dlg.Params)
	}
	if result.Stats.Dialogs != 1 {
		t.Errorf("dialog stat = %d, want 1", result.Stats.Dialogs)
	}
}

func TestSuggestTransactionsFromSession(t *testing.T) {
	btn := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &Session{ID: "rec_suggest", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.NavEvents = []NavEvent{
		{Timestamp: ts(500), URL: "https://shop.example/cart"},
		{Timestamp: ts(2500), URL: "https://shop.example/checkout-page"},
	}
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), Element: btn, Candidates: selector.Candidates(*btn)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(3000), Element: btn, Candidates: selector.Candidates(*btn)})

	suggestions := SuggestTransactionsFromSession(session)
	if len(suggestions) != 2 {
		t.Fatalf("suggestions = %+v, want 2", suggestions)
	}
	if suggestions[0].Name != "Cart" || suggestions[0].AtStepSeq != 1 {
		t.Errorf("suggestion 0 = %+v", suggestions[0])
	}
	if suggestions[1].Name != "Checkout Page" || suggestions[1].AtStepSeq != 2 {
		t.Errorf("suggestion 1 = %+v", suggestions[1])
	}
}

func TestConvertMarkSecretFill(t *testing.T) {
	input := &selector.ElementMetadata{Tag: "input", Name: "api_token", CSSPath: "form > input"}
	session := &Session{ID: "rec_mark", StartURL: "https://shop.example/", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepFill, Value: "abc123", MarkSecret: true, Timestamp: ts(1000), Element: input, Candidates: selector.Candidates(*input)})

	result := Convert(session, ConvertOptions{})
	fill := result.Script.Actions[1]
	if fill.Value != "{{env.API_TOKEN}}" {
		t.Errorf("marked-secret fill = %q, want {{env.API_TOKEN}}", fill.Value)
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
		{name: "fragment", input: "https://a.example/app?q=1#/orders/42", wantBase: "https://a.example", wantTarget: "/app?q=1#/orders/42"},
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

func TestRelativeTargetRequiresSameOrigin(t *testing.T) {
	if got := relativeTarget("https://shop.example", "https://shop.example/cart?q=1"); got != "/cart?q=1" {
		t.Fatalf("same-origin relativeTarget = %q, want /cart?q=1", got)
	}
	raw := "https://shop.example.evil/cart"
	if got := relativeTarget("https://shop.example", raw); got != raw {
		t.Fatalf("same-prefix-host relativeTarget = %q, want absolute %q", got, raw)
	}
}

func TestWebSocketPayloadMatchHintStripsJSONTransportPrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "socket.io json array", input: `42["ready",{"status":"ok"}]`, want: `["ready",{"status":"ok"}]`},
		{name: "json object unchanged", input: `{"type":"ready"}`, want: `{"type":"ready"}`},
		{name: "plain text unchanged", input: `ready:42`, want: `ready:42`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webSocketPayloadMatchHint(tt.input); got != tt.want {
				t.Fatalf("webSocketPayloadMatchHint(%q) = %q, want %q", tt.input, got, tt.want)
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

func TestTruncateStringRuneSafe(t *testing.T) {
	// "é" (U+00E9) is 2 bytes; "😀" (U+1F600) is 4 bytes.
	if got := truncateString("café", 3); got != "caf" {
		t.Fatalf("truncateString(café,3) = %q, want caf", got)
	}
	// Limit lands inside the 2-byte é — must back off to "caf", not emit a half rune.
	got := truncateString("café", 4)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateString produced invalid UTF-8: %q", got)
	}
	if got != "caf" {
		t.Fatalf("truncateString(café,4) = %q, want caf (rune boundary)", got)
	}
	// Multi-byte emoji split mid-rune backs off to the prior boundary.
	emoji := truncateString("a😀b", 3)
	if !utf8.ValidString(emoji) {
		t.Fatalf("truncateString split the emoji rune: %q", emoji)
	}
	if emoji != "a" {
		t.Fatalf("truncateString(a😀b,3) = %q, want a", emoji)
	}
}

// TestConvertOrdersByCaptureTimeNotArrival guards B11: the capture script defers
// a click up to 250ms (to coalesce double-clicks) but now stamps it with its
// ORIGINAL event time. So a click can arrive (and be appended) AFTER a fill that
// the user performed later, yet carry an earlier timestamp. Conversion must order
// by capture timestamp, placing the click before the fill — not by arrival order.
func TestConvertOrdersByCaptureTimeNotArrival(t *testing.T) {
	clickedBtn := &selector.ElementMetadata{Tag: "button", Text: "Open", CSSPath: "#open"}
	filledInput := &selector.ElementMetadata{Tag: "input", Name: "q", CSSPath: "#q"}

	session := &Session{ID: "rec_order", StartURL: "https://shop.example/", StartedAt: ts(0)}
	// The fill (ts 2000) is appended FIRST (it was sent immediately); the deferred
	// click (ts 1000, the user's earlier action) is appended AFTER it.
	session.appendStep(Step{Kind: StepFill, Value: "widget", Timestamp: ts(2000), URL: "https://shop.example/", Element: filledInput, Candidates: selector.Candidates(*filledInput)})
	session.appendStep(Step{Kind: StepClick, Timestamp: ts(1000), URL: "https://shop.example/", Element: clickedBtn, Candidates: selector.Candidates(*clickedBtn)})

	result := Convert(session, ConvertOptions{ScriptName: "Order"})
	types := actionTypes(result.Script.Actions)

	clickIdx, fillIdx := -1, -1
	for i, typ := range types {
		switch typ {
		case "browser_click":
			if clickIdx == -1 {
				clickIdx = i
			}
		case "browser_fill":
			if fillIdx == -1 {
				fillIdx = i
			}
		}
	}
	if clickIdx == -1 || fillIdx == -1 {
		t.Fatalf("expected a browser_click and a browser_fill, got %v", types)
	}
	if clickIdx > fillIdx {
		t.Fatalf("click (capture ts 1000) must precede fill (capture ts 2000); got order %v", types)
	}
}
