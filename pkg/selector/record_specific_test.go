package selector

import "testing"

func TestRecordSpecificText(t *testing.T) {
	for _, text := range []string{
		"Subtabs for I-0463595045 | Interaction",
		"Order 12345678",
		"Case CASE-000123",
		"Open a0B5e000004XyZqEAK",
		"0015e00000AbCdE",
		"Record 3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		"#tab-I-0463595045",
		`[data-testid="row-CASE-000123"]`,
	} {
		if !RecordSpecificText(text) {
			t.Errorf("RecordSpecificText(%q) = false, want true", text)
		}
	}
	for _, text := range []string{
		"Search Customer", "Interaction Type", "input-237", "Customer_Search",
		"brandBand_3", "Account Name", "555-1234", "Comfy", "Workspace tabs for Customer Central",
		"00N5e00000ABcDe", "0125e000000AbCd",
	} {
		if RecordSpecificText(text) {
			t.Errorf("RecordSpecificText(%q) = true, want false", text)
		}
	}
}

// A tab labelled with one record's number cannot match the next run's tab, so
// the positional CSS path outranks it.
func TestCandidatesDemoteRecordSpecificLabels(t *testing.T) {
	meta := ElementMetadata{
		Tag:       "div",
		AriaLabel: "Subtabs for I-0463595045 | Interaction",
		CSSPath:   "#brandBand_3 > div > div",
	}
	candidates := Candidates(meta)
	if len(candidates) < 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].Value != "#brandBand_3 > div > div" {
		t.Fatalf("best candidate = %+v, want the CSS path ahead of the record's label", candidates[0])
	}
	for _, candidate := range candidates {
		if candidate.Value == `div[aria-label="Subtabs for I-0463595045 | Interaction"]` && candidate.Confidence != ConfidenceRecordSpecific {
			t.Fatalf("record label confidence = %d, want %d", candidate.Confidence, ConfidenceRecordSpecific)
		}
	}
	if got := PlaywrightSelector(candidates); got != "#brandBand_3 > div > div" {
		t.Fatalf("PlaywrightSelector = %q, want the CSS path", got)
	}
}

func TestCandidatesKeepStableLabels(t *testing.T) {
	candidates := Candidates(ElementMetadata{Tag: "button", AriaLabel: "Search Customer", CSSPath: "#app > button"})
	if candidates[0].Confidence != ConfidenceAriaLabel {
		t.Fatalf("best candidate = %+v, want the ARIA label kept first", candidates[0])
	}
}
