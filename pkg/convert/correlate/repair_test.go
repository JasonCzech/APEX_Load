package correlate

import (
	"strings"
	"testing"
)

// recording: step 1 issues a csrf token, step 2 echoes it back.
func sampleRecording() []Observation {
	return []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-AAA-1111-recorded"}`},
		{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded&submit=1"},
	}
}

func TestDiagnoseReplayDetectsStaleValue(t *testing.T) {
	recorded := sampleRecording()
	// On replay the server rotated the token, but the script still sends the old one.
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-BBB-2222-replayed"}`},
		{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded&submit=1"},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d: %#v", len(suggestions), suggestions)
	}
	s := suggestions[0]
	if s.Severity != SeverityError {
		t.Fatalf("expected error severity, got %s", s.Severity)
	}
	if s.SourceStepID != 1 || s.FailingStepID != 2 {
		t.Fatalf("step ids wrong: source=%d failing=%d", s.SourceStepID, s.FailingStepID)
	}
	if s.RecordedValue != "TOKEN-AAA-1111-recorded" || s.ReplayValue != "TOKEN-BBB-2222-replayed" {
		t.Fatalf("values wrong: recorded=%q replay=%q", s.RecordedValue, s.ReplayValue)
	}
	if s.Extractor == nil || s.Extractor.Name != "corr_csrf_token" {
		t.Fatalf("expected csrf extractor fix, got %#v", s.Extractor)
	}
	if s.SourceFamily != FamilyJSON || s.ConfidenceBand != "high" || s.ReviewAction != "auto_apply_available" {
		t.Fatalf("unexpected evidence fields: %#v", s)
	}
	if len(s.AffectedFields) != 1 || s.AffectedFields[0] != "request_body" || s.UsageCount != 1 {
		t.Fatalf("affected fields/usage = %#v/%d, want request_body and one usage", s.AffectedFields, s.UsageCount)
	}
	if !HasBlockingRepairs(suggestions) {
		t.Fatalf("a stale value should be a blocking repair")
	}
}

func TestDiagnoseReplayReturnsFullLongValues(t *testing.T) {
	recordedValue := "tok-" + strings.Repeat("A", 120)
	replayValue := "tok-" + strings.Repeat("B", 120)
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestBody: "csrf=" + recordedValue},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + replayValue + `"}`},
		{ID: 2, RequestBody: "csrf=" + recordedValue},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want one stale-value repair", suggestions)
	}
	if suggestions[0].RecordedValue != recordedValue || suggestions[0].ReplayValue != replayValue {
		t.Fatalf("values were truncated or altered: recorded len=%d replay len=%d suggestion=%#v", len(suggestions[0].RecordedValue), len(suggestions[0].ReplayValue), suggestions[0])
	}
	if strings.Contains(suggestions[0].RecordedValue, "…") || strings.Contains(suggestions[0].ReplayValue, "…") {
		t.Fatalf("suggestion values must be full literals, got %#v", suggestions[0])
	}
}

func TestCandidateValueForCorrelationMatchesLocationBeforeName(t *testing.T) {
	observation := Observation{ResponseBody: `{"token":"tok-wrong-111111","payment":{"token":"tok-right-222222"}}`}
	corr := Correlation{
		Name:        "corr_token",
		Source:      "body",
		ExtractType: "json_path",
		Expression:  "$.payment.token",
		Value:       "tok-old-000000",
	}
	if got := candidateValueForCorrelation(observation, corr); got != "tok-right-222222" {
		t.Fatalf("candidateValueForCorrelation = %q, want payment token by expression", got)
	}
}

func TestDiagnoseReplayPassesWhenCorrelated(t *testing.T) {
	recorded := sampleRecording()
	// The script correctly captured and re-sent the fresh token.
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-BBB-2222-replayed"}`},
		{ID: 2, RequestBody: "csrf=TOKEN-BBB-2222-replayed&submit=1"},
	}
	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 0 {
		t.Fatalf("expected no suggestions for a correctly correlated replay, got %#v", suggestions)
	}
	if HasBlockingRepairs(suggestions) {
		t.Fatalf("should not block")
	}
}

func TestDiagnoseReplayWarnsOnMissingSource(t *testing.T) {
	recorded := sampleRecording()
	// The replay response no longer carries the token (boundary drift).
	replay := []Observation{
		{ID: 1, ResponseBody: `{"status":"ok"}`},
		{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded&submit=1"},
	}
	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 warning, got %#v", suggestions)
	}
	if suggestions[0].Severity != SeverityWarning {
		t.Fatalf("expected warning, got %s", suggestions[0].Severity)
	}
	if HasBlockingRepairs(suggestions) {
		t.Fatalf("a warning alone should not block")
	}
}

func TestDiagnoseReplayErrorsRankBeforeWarnings(t *testing.T) {
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-AAA-1111-recorded"}`},
		{ID: 2, ResponseBody: `{"access_token":"ACCESS-XYZ-0001-recorded"}`},
		{ID: 3, RequestBody: "csrf=TOKEN-AAA-1111-recorded"},
		{ID: 4, RequestBody: "auth=ACCESS-XYZ-0001-recorded"},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-BBB-2222-replayed"}`}, // rotated -> step 3 stale (error)
		{ID: 2, ResponseBody: `{"status":"ok"}`},                          // source gone -> warning
		{ID: 3, RequestBody: "csrf=TOKEN-AAA-1111-recorded"},
		{ID: 4, RequestBody: "auth=ACCESS-XYZ-0001-recorded"},
	}
	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %d: %#v", len(suggestions), suggestions)
	}
	if suggestions[0].Severity != SeverityError || suggestions[1].Severity != SeverityWarning {
		t.Fatalf("expected error ranked first then warning, got %s, %s", suggestions[0].Severity, suggestions[1].Severity)
	}
}
