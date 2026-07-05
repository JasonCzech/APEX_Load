package browser

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestActionParamInt(t *testing.T) {
	params := map[string]any{
		"int":        12,
		"int64":      int64(34),
		"float64":    float64(56.9),
		"float32":    float32(78.9),
		"string":     " 90 ",
		"bad_string": "nope",
	}

	tests := map[string]int{
		"int":        12,
		"int64":      34,
		"float64":    56,
		"float32":    78,
		"string":     90,
		"bad_string": -1,
		"missing":    -1,
	}
	for key, want := range tests {
		if got := actionParamInt(params, key, -1); got != want {
			t.Fatalf("actionParamInt(%q) = %d, want %d", key, got, want)
		}
	}
	if got := actionParamInt(nil, "anything", 7); got != 7 {
		t.Fatalf("actionParamInt(nil) = %d, want 7", got)
	}
}

func TestActionParamFloatValue(t *testing.T) {
	params := map[string]any{
		"int":     12,
		"int64":   int64(34),
		"float64": float64(56.75),
		"float32": float32(78.5),
		"string":  "90",
	}

	tests := map[string]struct {
		want float64
		ok   bool
	}{
		"int":     {want: 12, ok: true},
		"int64":   {want: 34, ok: true},
		"float64": {want: 56.75, ok: true},
		"float32": {want: 78.5, ok: true},
		"string":  {want: 0, ok: false},
		"missing": {want: 0, ok: false},
	}
	for key, tc := range tests {
		got, ok := actionParamFloatValue(params, key)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("actionParamFloatValue(%q) = %v/%v, want %v/%v", key, got, ok, tc.want, tc.ok)
		}
	}
}

func TestConfigIntAcceptsEngineNumericTypes(t *testing.T) {
	config := map[string]any{
		"int":     12,
		"int64":   int64(34),
		"float64": float64(56),
		"float32": float32(78),
		"string":  "90",
	}

	tests := map[string]int{
		"int":     12,
		"int64":   34,
		"float64": 56,
		"float32": 78,
		"string":  0,
		"missing": 0,
	}
	for key, want := range tests {
		if got := protocol.ConfigInt(config, key); got != want {
			t.Fatalf("protocol.ConfigInt(%q) = %d, want %d", key, got, want)
		}
	}
}

func TestActionParamString(t *testing.T) {
	params := map[string]any{
		"name":  " value ",
		"empty": "   ",
		"other": 12,
	}

	tests := map[string]string{
		"name":    "value",
		"empty":   "fallback",
		"other":   "fallback",
		"missing": "fallback",
	}
	for key, want := range tests {
		if got := actionParamString(params, key, "fallback"); got != want {
			t.Fatalf("actionParamString(%q) = %q, want %q", key, got, want)
		}
	}
	if got := actionParamString(nil, "anything", "fallback"); got != "fallback" {
		t.Fatalf("actionParamString(nil) = %q, want fallback", got)
	}
}

func TestActionFilesAndKey(t *testing.T) {
	files := actionFiles(&protocol.Action{
		Value: "/tmp/primary.txt",
		Params: map[string]any{
			"file":  " /tmp/secondary.txt ",
			"files": []any{"/tmp/third.txt", 12, " "},
		},
	})
	if len(files) != 3 || files[0] != "/tmp/primary.txt" || files[1] != "/tmp/secondary.txt" || files[2] != "/tmp/third.txt" {
		t.Fatalf("actionFiles = %#v, want primary/secondary/third paths", files)
	}
	if got := actionKey(&protocol.Action{Value: " Enter "}); got != "Enter" {
		t.Fatalf("actionKey(value) = %q, want Enter", got)
	}
	if got := actionKey(&protocol.Action{Params: map[string]any{"key_name": "Tab"}}); got != "Tab" {
		t.Fatalf("actionKey(params) = %q, want Tab", got)
	}
}

func TestKeyWithModifiers(t *testing.T) {
	tests := []struct {
		name   string
		action *protocol.Action
		key    string
		want   string
	}{
		{
			name:   "explicit modifiers",
			action: &protocol.Action{Params: map[string]any{"modifiers": []any{"ControlOrMeta", "Shift", "Shift"}}},
			key:    "A",
			want:   "ControlOrMeta+Shift+A",
		},
		{
			name:   "legacy chord value",
			action: &protocol.Action{},
			key:    "Control+Enter",
			want:   "Control+Enter",
		},
		{
			name:   "params override chord base",
			action: &protocol.Action{Params: map[string]any{"modifiers": []string{"Alt"}}},
			key:    "Control+A",
			want:   "Alt+A",
		},
		{
			name:   "unknown modifier ignored",
			action: &protocol.Action{Params: map[string]any{"modifiers": []string{"Hyper"}}},
			key:    "Enter",
			want:   "Enter",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyWithModifiers(tt.action, tt.key); got != tt.want {
				t.Fatalf("keyWithModifiers() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestActionSelectValues(t *testing.T) {
	values := actionSelectValues(&protocol.Action{
		Value: "standard",
		Params: map[string]any{
			"values": []any{"express", "overnight", " "},
		},
	})
	if strings.Join(values, ",") != "standard,express,overnight" {
		t.Fatalf("actionSelectValues = %#v, want standard/express/overnight", values)
	}
	if got := actionSelectValues(&protocol.Action{Params: map[string]any{"value": " economy "}}); len(got) != 1 || got[0] != "economy" {
		t.Fatalf("actionSelectValues(params.value) = %#v, want economy", got)
	}
}

func TestActionScrollDelta(t *testing.T) {
	dx, dy := actionScrollDelta(&protocol.Action{Params: map[string]any{"delta_x": 12, "delta_y": -240}})
	if dx != 12 || dy != -240 {
		t.Fatalf("actionScrollDelta(delta_*) = %d,%d, want 12,-240", dx, dy)
	}
	dx, dy = actionScrollDelta(&protocol.Action{Params: map[string]any{"x": 4, "y": 80}})
	if dx != 4 || dy != 80 {
		t.Fatalf("actionScrollDelta(x/y) = %d,%d, want 4,80", dx, dy)
	}
}

func TestParseWaitDelay(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "milliseconds shorthand", raw: "250", want: 250 * time.Millisecond},
		{name: "duration literal", raw: "2s", want: 2 * time.Second},
		{name: "empty", raw: "", wantErr: true},
		{name: "invalid", raw: "soon", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWaitDelay(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("duration = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrowserWaitTimeout(t *testing.T) {
	b := &Browser{}
	if got := b.waitTimeout(0); got != 10*time.Second {
		t.Fatalf("waitTimeout(0) = %v, want 10s", got)
	}
	if got := b.waitTimeout(1500); got != 1500*time.Millisecond {
		t.Fatalf("waitTimeout(1500) = %v, want 1500ms", got)
	}
}

func TestBrowserActionSelector(t *testing.T) {
	b := &Browser{}
	if got := b.actionSelector(&protocol.Action{Selector: "#primary", Target: "#fallback"}); got != "#primary" {
		t.Fatalf("actionSelector preferred %q, want #primary", got)
	}
	if got := b.actionSelector(&protocol.Action{Target: "#fallback"}); got != "#fallback" {
		t.Fatalf("actionSelector fallback = %q, want #fallback", got)
	}
}

func TestBrowserScreenshotSelector(t *testing.T) {
	b := &Browser{}
	if got := b.screenshotSelector(nil); got != "" {
		t.Fatalf("screenshotSelector(nil) = %q, want empty", got)
	}
	if got := b.screenshotSelector(&protocol.Action{Selector: "#panel", Type: "browser_screenshot", Target: "#target"}); got != "#panel" {
		t.Fatalf("screenshotSelector preferred %q, want #panel", got)
	}
	if got := b.screenshotSelector(&protocol.Action{Type: "browser_wait", Target: "#target"}); got != "#target" {
		t.Fatalf("screenshotSelector wait fallback = %q, want #target", got)
	}
	if got := b.screenshotSelector(&protocol.Action{Type: "browser_evaluate_js", Target: "#target"}); got != "" {
		t.Fatalf("screenshotSelector evaluate = %q, want empty", got)
	}
}

func TestNormalizeFrameSelector(t *testing.T) {
	if got := normalizeFrameSelector("#checkout-frame"); got != "#checkout-frame" {
		t.Fatalf("normalizeFrameSelector explicit = %q, want #checkout-frame", got)
	}
	if got := normalizeFrameSelector("checkout-frame"); got != `iframe[name="checkout-frame"], iframe[id="checkout-frame"], iframe[title="checkout-frame"]` {
		t.Fatalf("normalizeFrameSelector name = %q, want iframe attribute selector", got)
	}
	if got := normalizeFrameSelector(`pay"frame`); got != `iframe[name="pay\"frame"], iframe[id="pay\"frame"], iframe[title="pay\"frame"]` {
		t.Fatalf("normalizeFrameSelector escaped = %q", got)
	}
}

func TestCloneDOMSummary(t *testing.T) {
	original := &protocol.DOMSummary{
		Title:       "page",
		ReadyState:  "complete",
		VisibleText: []string{"first", "second"},
	}

	cloned := cloneDOMSummary(original)
	if cloned == nil {
		t.Fatal("cloneDOMSummary returned nil")
	}
	if cloned.Title != original.Title || cloned.ReadyState != original.ReadyState {
		t.Fatalf("clone = %+v, want title/state copied from %+v", cloned, original)
	}
	cloned.VisibleText[0] = "changed"
	if original.VisibleText[0] != "first" {
		t.Fatal("cloneDOMSummary did not deep-copy VisibleText")
	}
	if cloneDOMSummary(nil) != nil {
		t.Fatal("cloneDOMSummary(nil) returned non-nil")
	}
}

func TestBrowserEntrySnapshotsAreCloned(t *testing.T) {
	b := &Browser{
		consoleEntries: []protocol.ConsoleEntry{{Text: "first"}, {Text: "second"}},
		networkEntries: []protocol.NetworkEntry{
			{URL: "/first"},
			{URL: "/second"},
		},
	}

	consoleOffset, networkOffset := b.eventOffsets()
	if consoleOffset != 2 || networkOffset != 2 {
		t.Fatalf("eventOffsets = %d/%d, want 2/2", consoleOffset, networkOffset)
	}

	consoleEntries := b.consoleEntriesSince(1)
	if len(consoleEntries) != 1 || consoleEntries[0].Text != "second" {
		t.Fatalf("consoleEntriesSince(1) = %+v, want second entry", consoleEntries)
	}
	consoleEntries[0].Text = "changed"
	if b.consoleEntries[1].Text != "second" {
		t.Fatal("consoleEntriesSince returned aliased slice")
	}
	if got := b.consoleEntriesSince(2); got != nil {
		t.Fatalf("consoleEntriesSince(end) = %+v, want nil", got)
	}

	networkEntries := b.networkEntriesSince(0)
	if len(networkEntries) != 2 || networkEntries[0].URL != "/first" {
		t.Fatalf("networkEntriesSince(0) = %+v, want all entries", networkEntries)
	}
	networkEntries[0].URL = "/changed"
	if b.networkEntries[0].URL != "/first" {
		t.Fatal("networkEntriesSince returned aliased slice")
	}
	if got := b.networkEntriesSince(2); got != nil {
		t.Fatalf("networkEntriesSince(end) = %+v, want nil", got)
	}
}
