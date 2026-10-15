package selector

import (
	"strings"
	"testing"
)

func candidateValues(candidates []Candidate) []string {
	values := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		values = append(values, candidate.Kind+":"+candidate.Value)
	}
	return values
}

func TestCandidates(t *testing.T) {
	tests := []struct {
		name      string
		meta      ElementMetadata
		want      []string // kind:value in expected rank order
		wantEmpty bool
	}{
		{
			name:      "empty metadata yields no candidates",
			meta:      ElementMetadata{},
			wantEmpty: true,
		},
		{
			name: "full ladder ranks test hook first",
			meta: ElementMetadata{
				Tag:        "button",
				ID:         "submit",
				DataTestID: "submit-btn",
				Attributes: map[string]string{"data-testid": "submit-btn"},
				AriaLabel:  "Submit order",
				Role:       "button",
				Text:       "Submit",
				CSSPath:    "form > button:nth-of-type(1)",
				XPath:      "//form/button[1]",
			},
			want: []string{
				`css:[data-testid="submit-btn"]`,
				"css:#submit",
				`css:button[aria-label="Submit order"]`,
				`role:role=button[name="Submit order"]`,
				"css:form > button:nth-of-type(1)",
				`text:text="Submit"`,
				"xpath://form/button[1]",
			},
		},
		{
			name: "id with special characters uses attribute form",
			meta: ElementMetadata{Tag: "input", ID: "user:email"},
			want: []string{`css:[id="user:email"]`},
		},
		{
			name: "id with whitespace dropped",
			meta: ElementMetadata{Tag: "input", ID: "bad id", Name: "email"},
			want: []string{`css:input[name="email"]`},
		},
		{
			name: "shadow DOM suppresses xpath",
			meta: ElementMetadata{
				Tag:         "input",
				Name:        "q",
				CSSPath:     "search-box input",
				XPath:       "//search-box//input",
				InShadowDOM: true,
			},
			want: []string{
				`css:input[name="q"]`,
				"css:search-box input",
			},
		},
		{
			name:      "role without accessible name skipped",
			meta:      ElementMetadata{Tag: "div", Role: "dialog"},
			wantEmpty: true,
		},
		{
			name: "role uses text when no aria label",
			meta: ElementMetadata{Tag: "a", Role: "link", Text: "Read more"},
			want: []string{
				`role:role=link[name="Read more"]`,
				`text:text="Read more"`,
			},
		},
		{
			name:      "long text suppressed",
			meta:      ElementMetadata{Tag: "p", Text: strings.Repeat("very long content ", 10)},
			wantEmpty: true,
		},
		{
			name: "placeholder candidate",
			meta: ElementMetadata{Tag: "input", Placeholder: "Search products"},
			want: []string{`css:input[placeholder="Search products"]`},
		},
		{
			name: "alternate test hook attribute name preserved",
			meta: ElementMetadata{
				Tag:        "button",
				DataTestID: "checkout",
				Attributes: map[string]string{"data-cy": "checkout"},
			},
			want: []string{`css:[data-cy="checkout"]`},
		},
		{
			name: "text collapses internal whitespace",
			meta: ElementMetadata{Tag: "span", Text: "  Sign \n\t in  "},
			want: []string{`text:text="Sign in"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Candidates(tt.meta)
			if tt.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("Candidates() = %v, want empty", candidateValues(got))
				}
				return
			}
			gotValues := candidateValues(got)
			if len(gotValues) != len(tt.want) {
				t.Fatalf("Candidates() = %v, want %v", gotValues, tt.want)
			}
			for i := range tt.want {
				if gotValues[i] != tt.want[i] {
					t.Errorf("Candidates()[%d] = %q, want %q (full: %v)", i, gotValues[i], tt.want[i], gotValues)
				}
			}
		})
	}
}

func TestCandidatesConfidenceMonotonic(t *testing.T) {
	meta := ElementMetadata{
		Tag:         "button",
		ID:          "go",
		DataTestID:  "go-btn",
		AriaLabel:   "Go now",
		Name:        "go",
		Role:        "button",
		Placeholder: "type here",
		Text:        "Go",
		CSSPath:     "main > button",
		XPath:       "//main/button",
	}
	candidates := Candidates(meta)
	if len(candidates) < 5 {
		t.Fatalf("expected a full candidate set, got %d", len(candidates))
	}
	for i := 1; i < len(candidates); i++ {
		if candidates[i].Confidence > candidates[i-1].Confidence {
			t.Errorf("candidates out of order at %d: %d > %d", i, candidates[i].Confidence, candidates[i-1].Confidence)
		}
	}
	for _, candidate := range candidates {
		if candidate.Reason == "" {
			t.Errorf("candidate %s:%s missing reason", candidate.Kind, candidate.Value)
		}
	}
}

func TestCandidatesLightningDemotesAuraRenderID(t *testing.T) {
	meta := ElementMetadata{
		Tag:       "lightning-button",
		ID:        "123:456;a",
		Role:      "button",
		AriaLabel: "Save",
		Text:      "Save",
		Attributes: map[string]string{
			"data-aura-class": "force:button",
			"title":           "Save",
		},
		CSSPath: "one-app lightning-button:nth-of-type(4)",
	}

	got := Candidates(meta)
	gotValues := candidateValues(got)
	wantPrefix := []string{
		`css:lightning-button[aria-label="Save"]`,
		`role:role=button[name="Save"]`,
		`css:lightning-button[data-aura-class="force:button"]`,
		`css:lightning-button[title="Save"]`,
	}
	if len(gotValues) < len(wantPrefix) {
		t.Fatalf("Candidates() = %v, want at least %v", gotValues, wantPrefix)
	}
	for i, want := range wantPrefix {
		if gotValues[i] != want {
			t.Fatalf("Candidates()[%d] = %q, want %q (full: %v)", i, gotValues[i], want, gotValues)
		}
	}
	idIndex := -1
	textIndex := -1
	for i, value := range gotValues {
		switch value {
		case `css:[id="123:456;a"]`:
			idIndex = i
		case `text:text="Save"`:
			textIndex = i
		}
	}
	if idIndex == -1 {
		t.Fatalf("volatile Aura id candidate missing: %v", gotValues)
	}
	if textIndex == -1 {
		t.Fatalf("text fallback missing: %v", gotValues)
	}
	if idIndex < textIndex {
		t.Fatalf("volatile Aura id ranked above text fallback: %v", gotValues)
	}
}

func confidenceForValue(candidates []Candidate, kind, value string) (int, bool) {
	for _, c := range candidates {
		if c.Kind == kind && c.Value == value {
			return c.Confidence, true
		}
	}
	return 0, false
}

func TestCandidatesLightningConfidenceLevels(t *testing.T) {
	lightning := Candidates(ElementMetadata{
		Tag:       "lightning-button",
		Role:      "button",
		AriaLabel: "Save",
		Text:      "Save",
		Attributes: map[string]string{
			"data-aura-class": "force:button",
			"title":           "Save",
		},
	})
	checks := []struct {
		kind, value string
		want        int
	}{
		{"css", `lightning-button[aria-label="Save"]`, ConfidenceLightningAriaLabel},
		{"css", `lightning-button[data-aura-class="force:button"]`, ConfidenceLightningAuraClass},
		{"css", `lightning-button[title="Save"]`, ConfidenceLightningTitle},
		{"role", `role=button[name="Save"]`, ConfidenceLightningRoleName},
	}
	for _, c := range checks {
		got, ok := confidenceForValue(lightning, c.kind, c.value)
		if !ok {
			t.Fatalf("missing candidate %s:%s", c.kind, c.value)
		}
		if got != c.want {
			t.Fatalf("%s confidence = %d, want %d", c.value, got, c.want)
		}
	}

	// A plain (non-Lightning) element keeps the generic aria-label confidence,
	// not the Lightning-boosted one.
	plain := Candidates(ElementMetadata{Tag: "button", AriaLabel: "Save"})
	got, ok := confidenceForValue(plain, "css", `button[aria-label="Save"]`)
	if !ok {
		t.Fatal("missing plain aria-label candidate")
	}
	if got != ConfidenceAriaLabel {
		t.Fatalf("plain aria-label confidence = %d, want generic %d", got, ConfidenceAriaLabel)
	}
}

func TestVolatileAuraIDConfidence(t *testing.T) {
	got := Candidates(ElementMetadata{Tag: "span", ID: "j_id0:form:input"})
	conf, ok := confidenceForValue(got, "css", `[id="j_id0:form:input"]`)
	if !ok {
		t.Fatalf("missing volatile aura id candidate: %#v", got)
	}
	if conf != ConfidenceVolatileAuraID {
		t.Fatalf("volatile aura id confidence = %d, want %d", conf, ConfidenceVolatileAuraID)
	}
}

// On a Lightning page, an LWC id with its generated suffix ranks last, a
// Lightning button's name and any title are candidates, and CSS and XPath
// candidates match visible elements only.
func TestCandidatesOnALightningPage(t *testing.T) {
	input := Candidates(ElementMetadata{
		Tag: "input", ID: "input-237", Name: "Name", Role: "textbox", AriaLabel: "Account Name",
		CSSPath: "records-record-layout-item input", XPath: "//input[1]", Lightning: true,
	})
	if input[0].Value != `input[aria-label="Account Name"] >> visible=true` || input[0].Confidence != ConfidenceLightningAriaLabel {
		t.Fatalf("top candidate = %+v", input[0])
	}
	id, _ := confidenceForValue(input, "css", `#input-237 >> visible=true`)
	if id != ConfidenceVolatileAuraID {
		t.Fatalf("LWC id confidence = %d; candidates %v", id, candidateValues(input))
	}
	if _, ok := confidenceForValue(input, "xpath", `xpath=//input[1] >> visible=true`); !ok {
		t.Fatalf("xpath candidate not visible-only: %v", candidateValues(input))
	}
	if _, ok := confidenceForValue(input, "role", `role=textbox[name="Account Name"]`); !ok {
		t.Fatalf("role candidate changed: %v", candidateValues(input))
	}

	button := Candidates(ElementMetadata{Tag: "button", Name: "SaveEdit", Text: "Save", Attributes: map[string]string{"title": "Save"}, Lightning: true})
	if got, _ := confidenceForValue(button, "css", `button[name="SaveEdit"] >> visible=true`); got != ConfidenceLightningButtonName {
		t.Fatalf("button name confidence = %d; candidates %v", got, candidateValues(button))
	}
	if _, ok := confidenceForValue(button, "css", `button[title="Save"] >> visible=true`); !ok {
		t.Fatalf("title candidate missing: %v", candidateValues(button))
	}

	// Elsewhere an id with a numeric suffix is an ordinary id.
	plain := Candidates(ElementMetadata{Tag: "input", ID: "input-237"})
	if plain[0].Value != "#input-237" || plain[0].Confidence != ConfidenceHTMLID {
		t.Fatalf("plain page = %v", candidateValues(plain))
	}
}
