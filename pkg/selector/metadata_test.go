package selector

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFromMetadataValue(t *testing.T) {
	native := []Candidate{
		{Kind: "css", Value: "#a", Confidence: 90, Reason: "id"},
		{Kind: "text", Value: `text="Go"`, Confidence: 62, Reason: "text"},
	}

	// Simulate a JSON storage round-trip: []Candidate → []any of map[string]any.
	raw, err := json.Marshal(native)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTripped any
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	tests := []struct {
		name  string
		input any
		want  []Candidate
	}{
		{name: "native slice", input: native, want: native},
		{name: "json round trip", input: roundTripped, want: native},
		{name: "nil", input: nil, want: nil},
		{name: "wrong type", input: "candidates", want: nil},
		{name: "list of non-maps", input: []any{"x", 7}, want: nil},
		{
			name:  "entries without value dropped",
			input: []any{map[string]any{"kind": "css", "confidence": 90.0}},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromMetadataValue(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FromMetadataValue() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFromMetadataValueCopiesNativeSlice(t *testing.T) {
	native := []Candidate{{Kind: "css", Value: "#a", Confidence: 90}}
	got := FromMetadataValue(native)
	got[0].Value = "#mutated"
	if native[0].Value != "#a" {
		t.Error("FromMetadataValue must copy the native slice, not alias it")
	}
}

func TestDiagnose(t *testing.T) {
	candidates := []Candidate{
		{Kind: "css", Value: `[data-testid="go"]`, Confidence: 95},
		{Kind: "css", Value: "#go", Confidence: 90},
		{Kind: "text", Value: `text="Go"`, Confidence: 62},
	}
	steps := []ReplayStep{
		{ActionName: "Click Go", Selector: `[data-testid="go"]`, Candidates: candidates, Status: StatusFailed, Error: "timeout"},
		{ActionName: "Click Pay", Selector: "#pay", Candidates: []Candidate{{Kind: "css", Value: "#pay", Confidence: 90}, {Kind: "css", Value: "form #pay-btn", Confidence: 68}}, Status: StatusHealed, HealedSelector: "form #pay-btn"},
		{ActionName: "Click Weak", Selector: `text="Maybe"`, Candidates: []Candidate{{Kind: "text", Value: `text="Maybe"`, Confidence: 62}}, Status: StatusOK},
		{ActionName: "Click Solid", Selector: "#solid", Candidates: []Candidate{{Kind: "css", Value: "#solid", Confidence: 90}}, Status: StatusOK},
		{ActionName: "No candidates", Selector: ".x > .y", Status: StatusFailed},
	}

	suggestions := Diagnose(steps)
	if len(suggestions) != 4 {
		t.Fatalf("suggestions = %d: %+v", len(suggestions), suggestions)
	}

	// Sorted errors first, then warnings, then info.
	if suggestions[0].Severity != SeverityError || suggestions[1].Severity != SeverityError {
		t.Errorf("first two should be errors: %+v", suggestions[:2])
	}
	if suggestions[2].Severity != SeverityWarning || suggestions[3].Severity != SeverityInfo {
		t.Errorf("ordering = %v %v", suggestions[2].Severity, suggestions[3].Severity)
	}

	failed := suggestions[0]
	if failed.ActionName != "Click Go" {
		t.Errorf("failed action = %q", failed.ActionName)
	}
	if failed.SuggestedSelector != "#go" || failed.Confidence != 90 {
		t.Errorf("failed alternate = %q (%d), want #go (90)", failed.SuggestedSelector, failed.Confidence)
	}
	if failed.ReviewAction != "alternate_available" {
		t.Errorf("failed review action = %q", failed.ReviewAction)
	}

	noCandidates := suggestions[1]
	if noCandidates.SuggestedSelector != "" || noCandidates.ReviewAction != "manual_review_required" {
		t.Errorf("no-candidate suggestion = %+v", noCandidates)
	}

	healed := suggestions[2]
	if healed.SuggestedSelector != "form #pay-btn" || healed.ReviewAction != "auto_apply_available" {
		t.Errorf("healed = %+v", healed)
	}
	if healed.Confidence != 68 {
		t.Errorf("healed confidence = %d, want 68 from matching candidate", healed.Confidence)
	}

	weak := suggestions[3]
	if weak.ActionName != "Click Weak" || weak.ReviewAction != "stability_advisory" {
		t.Errorf("weak = %+v", weak)
	}

	if !HasBlockingRepairs(suggestions) {
		t.Error("failed selectors must be blocking")
	}
	if HasBlockingRepairs(suggestions[2:]) {
		t.Error("warnings and info are not blocking")
	}
}

func TestDiagnoseEmpty(t *testing.T) {
	if got := Diagnose(nil); len(got) != 0 {
		t.Errorf("Diagnose(nil) = %+v", got)
	}
}
