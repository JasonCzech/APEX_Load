package selector

import (
	"reflect"
	"testing"
)

func TestUnique(t *testing.T) {
	tests := []struct {
		name  string
		input []Candidate
		want  []Candidate
	}{
		{
			name:  "empty input",
			input: nil,
			want:  []Candidate{},
		},
		{
			name: "drops empty values and trims whitespace",
			input: []Candidate{
				{Kind: "css", Value: "  #login  ", Confidence: 90},
				{Kind: "css", Value: "   ", Confidence: 95},
			},
			want: []Candidate{
				{Kind: "css", Value: "#login", Confidence: 90},
			},
		},
		{
			name: "dedupes by kind and value keeping first",
			input: []Candidate{
				{Kind: "css", Value: "#login", Confidence: 90, Reason: "first"},
				{Kind: "css", Value: "#login", Confidence: 50, Reason: "second"},
				{Kind: "xpath", Value: "#login", Confidence: 58, Reason: "different kind kept"},
			},
			want: []Candidate{
				{Kind: "css", Value: "#login", Confidence: 90, Reason: "first"},
				{Kind: "xpath", Value: "#login", Confidence: 58, Reason: "different kind kept"},
			},
		},
		{
			name: "sorts by confidence desc then value",
			input: []Candidate{
				{Kind: "text", Value: "Submit", Confidence: 62},
				{Kind: "css", Value: "#b", Confidence: 90},
				{Kind: "css", Value: "#a", Confidence: 90},
			},
			want: []Candidate{
				{Kind: "css", Value: "#a", Confidence: 90},
				{Kind: "css", Value: "#b", Confidence: 90},
				{Kind: "text", Value: "Submit", Confidence: 62},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Unique(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Unique() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCSSAttributeSelector(t *testing.T) {
	tests := []struct {
		name  string
		attr  string
		value string
		want  string
	}{
		{name: "plain value", attr: "data-testid", value: "submit-btn", want: `[data-testid="submit-btn"]`},
		{name: "escapes double quotes", attr: "aria-label", value: `Say "hi"`, want: `[aria-label="Say \"hi\""]`},
		{name: "escapes backslashes", attr: "name", value: `a\b`, want: `[name="a\\b"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CSSAttributeSelector(tt.attr, tt.value); got != tt.want {
				t.Errorf("CSSAttributeSelector() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlaywrightSelector(t *testing.T) {
	tests := []struct {
		name       string
		candidates []Candidate
		want       string
	}{
		{name: "empty", candidates: nil, want: ""},
		{
			name: "promotes highest css",
			candidates: []Candidate{
				{Kind: "css", Value: "#login", Confidence: 90},
				{Kind: "xpath", Value: "//input", Confidence: 58},
			},
			want: "#login",
		},
		{
			name: "skips role and text promoting xpath with prefix",
			candidates: []Candidate{
				{Kind: "role", Value: `role=button[name="Go"]`, Confidence: 72},
				{Kind: "text", Value: `text="Go"`, Confidence: 62},
				{Kind: "xpath", Value: "//button[1]", Confidence: 58},
			},
			want: "xpath=//button[1]",
		},
		{
			name: "preserves existing xpath prefix",
			candidates: []Candidate{
				{Kind: "xpath", Value: "xpath=//button", Confidence: 58},
			},
			want: "xpath=//button",
		},
		{
			name: "only unpromotable kinds",
			candidates: []Candidate{
				{Kind: "role", Value: "button", Confidence: 72},
				{Kind: "text", Value: "Go", Confidence: 62},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlaywrightSelector(tt.candidates); got != tt.want {
				t.Errorf("PlaywrightSelector() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUsable(t *testing.T) {
	tests := []struct {
		name      string
		candidate Candidate
		want      string
		wantOK    bool
	}{
		{name: "css passes through", candidate: Candidate{Kind: "css", Value: "#login"}, want: "#login", wantOK: true},
		{name: "xpath gains prefix", candidate: Candidate{Kind: "xpath", Value: "//a[1]"}, want: "xpath=//a[1]", wantOK: true},
		{name: "engine-form role usable", candidate: Candidate{Kind: "role", Value: `role=button[name="Go"]`}, want: `role=button[name="Go"]`, wantOK: true},
		{name: "bare role not usable", candidate: Candidate{Kind: "role", Value: "button"}, want: "", wantOK: false},
		{name: "engine-form text usable", candidate: Candidate{Kind: "text", Value: `text="Go"`}, want: `text="Go"`, wantOK: true},
		{name: "bare text converted", candidate: Candidate{Kind: "text", Value: "Sign in"}, want: `text="Sign in"`, wantOK: true},
		{name: "empty value not usable", candidate: Candidate{Kind: "css", Value: "  "}, want: "", wantOK: false},
		{name: "unknown kind not usable", candidate: Candidate{Kind: "image", Value: "x.png"}, want: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Usable(tt.candidate)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Usable() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestFallbacksForPrimaryKeepsRoleAndTextSkippedByPrimarySelection(t *testing.T) {
	candidates := []Candidate{
		{Kind: "role", Value: `role=button[name="Checkout"]`, Confidence: 72},
		{Kind: "text", Value: "Checkout", Confidence: 62},
		{Kind: "xpath", Value: "//button[1]", Confidence: 58},
		{Kind: "css", Value: "   ", Confidence: 95},
	}
	primary := PlaywrightSelector(candidates)
	if primary != "xpath=//button[1]" {
		t.Fatalf("primary = %q, want xpath fallback primary", primary)
	}

	fallbacks := FallbacksForPrimary(candidates, primary)
	if len(fallbacks) != 2 {
		t.Fatalf("fallbacks = %+v, want role and text only", fallbacks)
	}
	if fallbacks[0].Kind != "role" || fallbacks[1].Kind != "text" {
		t.Fatalf("fallback kinds = %s/%s, want role/text", fallbacks[0].Kind, fallbacks[1].Kind)
	}
}

func TestRoleAndTextSelector(t *testing.T) {
	if got := RoleSelector("button", "Submit order"); got != `role=button[name="Submit order"]` {
		t.Errorf("RoleSelector() = %q", got)
	}
	if got := RoleSelector("button", ""); got != "" {
		t.Errorf("RoleSelector() without name = %q, want empty", got)
	}
	if got := RoleSelector("", "Submit"); got != "" {
		t.Errorf("RoleSelector() without role = %q, want empty", got)
	}
	if got := TextSelector(` Sign "in" `); got != `text="Sign \"in\""` {
		t.Errorf("TextSelector() = %q", got)
	}
	if got := TextSelector("   "); got != "" {
		t.Errorf("TextSelector() blank = %q, want empty", got)
	}
}

func TestNormalizeXPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "  ", want: ""},
		{name: "bare expression gains prefix", input: "//div[@id='x']", want: "xpath=//div[@id='x']"},
		{name: "existing prefix preserved", input: "xpath=//div", want: "xpath=//div"},
		{name: "case-insensitive prefix detection", input: "XPATH=//div", want: "XPATH=//div"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeXPath(tt.input); got != tt.want {
				t.Errorf("NormalizeXPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
