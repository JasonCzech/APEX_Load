package correlate

import (
	"strings"
	"testing"
)

// diffRecording builds one recording of a login flow where the session token
// and a static API version are both emitted and reused.
func diffRecording(sessionToken string) []Observation {
	return []Observation{
		{
			ID:           1,
			RequestURL:   "https://shop.example/login",
			ResponseBody: `{"session_token":"` + sessionToken + `","api_version_id":"55512345"}`,
		},
		{
			ID:             2,
			RequestURL:     "https://shop.example/cart",
			RequestHeaders: map[string]string{"X-Session": sessionToken},
			RequestBody:    `{"api_version_id":"55512345"}`,
			ResponseBody:   `{"ok":true}`,
		},
	}
}

func TestDiffRecordingsClassifiesDynamicAndStatic(t *testing.T) {
	a := diffRecording("tok-aaaa-1111-secret")
	b := diffRecording("tok-bbbb-2222-secret")

	results := DiffRecordings(a, b)
	if len(results) == 0 {
		t.Fatal("expected classified correlations")
	}

	var dynamic, static *DiffResult
	for i := range results {
		switch {
		case strings.Contains(results[i].Name, "session"):
			dynamic = &results[i]
		case strings.Contains(results[i].Name, "version"):
			static = &results[i]
		}
	}
	if dynamic == nil {
		t.Fatalf("session token not detected: %+v", results)
	}
	if dynamic.Class != DiffDynamic {
		t.Errorf("session token class = %s, want dynamic", dynamic.Class)
	}
	if dynamic.ValueB != "tok-bbbb-2222-secret" {
		t.Errorf("dynamic ValueB = %q", dynamic.ValueB)
	}
	if static == nil {
		t.Fatalf("static id not detected: %+v", results)
	}
	if static.Class != DiffStatic {
		t.Errorf("api version class = %s, want static", static.Class)
	}
	if static.Confidence >= dynamic.Confidence {
		t.Errorf("static confidence %v should be cut below dynamic %v", static.Confidence, dynamic.Confidence)
	}
	// Dynamic results sort first.
	if results[0].Class != DiffDynamic {
		t.Errorf("first result class = %s, want dynamic", results[0].Class)
	}
}

func TestDiffRecordingsBoostsAndCutsConfidence(t *testing.T) {
	a := diffRecording("tok-aaaa-1111-secret")
	baseline := Detect(a)
	baselineByName := map[string]float64{}
	for _, correlation := range baseline {
		baselineByName[correlation.Name] = correlation.Confidence
	}

	results := DiffRecordings(a, diffRecording("tok-bbbb-2222-secret"))
	for _, result := range results {
		base, ok := baselineByName[result.Name]
		if !ok {
			continue
		}
		switch result.Class {
		case DiffDynamic:
			if result.Confidence <= base {
				t.Errorf("%s: dynamic confidence %v not raised above baseline %v", result.Name, result.Confidence, base)
			}
		case DiffStatic:
			if result.Confidence >= base {
				t.Errorf("%s: static confidence %v not cut below baseline %v", result.Name, result.Confidence, base)
			}
		}
	}
}

func TestDiffRecordingsUnknownWhenStepMissing(t *testing.T) {
	a := diffRecording("tok-aaaa-1111-secret")
	// Second recording lacks the login step entirely.
	b := []Observation{{ID: 9, ResponseBody: `{"ok":true}`}}

	results := DiffRecordings(a, b)
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	for _, result := range results {
		if result.Class != DiffUnknown {
			t.Errorf("%s: class = %s, want unknown when counterpart step is missing", result.Name, result.Class)
		}
		if result.ValueB != "" {
			t.Errorf("%s: ValueB = %q, want empty for unknown", result.Name, result.ValueB)
		}
	}
}

func TestDiffRecordingsEmptyInputs(t *testing.T) {
	if got := DiffRecordings(nil, nil); got != nil {
		t.Errorf("DiffRecordings(nil, nil) = %v, want nil", got)
	}
	if got := DiffRecordings([]Observation{{ID: 1, ResponseBody: "plain"}}, nil); got != nil {
		t.Errorf("no correlations should yield nil, got %v", got)
	}
}
