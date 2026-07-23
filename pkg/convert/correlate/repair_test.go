package correlate

import (
	"encoding/json"
	"errors"
	"fmt"
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
	if s.Extractor.OnMiss != "fail" {
		t.Fatalf("suggested extractor on_miss = %q, want fail so a later iteration cannot reuse a stale value", s.Extractor.OnMiss)
	}
	encodedExtractor, err := json.Marshal(s.Extractor)
	if err != nil {
		t.Fatalf("marshal suggested extractor: %v", err)
	}
	if !strings.Contains(string(encodedExtractor), `"on_miss":"fail"`) {
		t.Fatalf("suggested extractor API contract = %s, want on_miss=fail", encodedExtractor)
	}
	if s.SourceFamily != FamilyJSON || s.ConfidenceBand != "high" || s.ReviewAction != "manual_value_replacement_required" {
		t.Fatalf("unexpected evidence fields: %#v", s)
	}
	if len(s.AffectedFields) != 1 || s.AffectedFields[0] != "request_body" || s.UsageCount != 1 {
		t.Fatalf("affected fields/usage = %#v/%d, want request_body and one usage", s.AffectedFields, s.UsageCount)
	}
	if !HasBlockingRepairs(suggestions) {
		t.Fatalf("a stale value should be a blocking repair")
	}
}

func TestDiagnoseReplayDetectsEncodedStaleValueInExactFields(t *testing.T) {
	recordedValue := "TOKEN-AAA+/==recorded"
	replayValue := "TOKEN-BBB+/==replayed"
	encodedRecorded := "TOKEN-AAA%2B%2F%3D%3Drecorded"
	formEncodedRecorded := "TOKEN-AAA+%2F%3D%3Drecorded"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{
			ID:             2,
			RequestURL:     "https://example.test/submit?csrf=" + encodedRecorded,
			RequestBody:    "csrf=" + formEncodedRecorded,
			RequestHeaders: map[string]string{"X-CSRF": encodedRecorded},
		},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + replayValue + `"}`},
		{
			ID:             2,
			RequestURL:     "https://example.test/submit?csrf=" + encodedRecorded,
			RequestBody:    "csrf=" + formEncodedRecorded,
			RequestHeaders: map[string]string{"X-CSRF": encodedRecorded},
		},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 2 {
		t.Fatalf("suggestions = %#v, want exact-query and unsupported-form repairs", suggestions)
	}
	exact := suggestions[0]
	if got, want := exact.AffectedFields, []string{"request_url", "request_headers.X-CSRF"}; !equalStrings(got, want) {
		t.Fatalf("exact encoded fields = %#v, want %#v", got, want)
	}
	if exact.Extractor == nil || exact.Extractor.Convert != "url_encode" || len(exact.RepairLocations) != 2 {
		t.Fatalf("exact query spelling should be safely auto-repairable: %#v", exact)
	}
	manual := suggestions[1]
	if got, want := manual.AffectedFields, []string{"request_body"}; !equalStrings(got, want) {
		t.Fatalf("unsupported encoded fields = %#v, want %#v", got, want)
	}
	if manual.Extractor != nil || manual.ReviewAction != "manual_value_replacement_required" {
		t.Fatalf("non-canonical form spelling must require manual review: %#v", manual)
	}
	if !HasBlockingRepairs(suggestions) {
		t.Fatal("encoded stale values must block")
	}
}

func TestDiagnoseReplaySplitsRawAndQueryEncodedRepairs(t *testing.T) {
	recordedValue := "token /+ value"
	replayValue := "rotated /+ value"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestURL: "/submit?encoded=" + QueryEncodedFormOf(recordedValue), RequestBody: "raw=" + recordedValue},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + replayValue + `"}`},
		{ID: 2, RequestURL: "/submit?encoded=" + QueryEncodedFormOf(recordedValue), RequestBody: "raw=" + recordedValue},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 2 {
		t.Fatalf("suggestions = %#v, want raw and encoded repairs", suggestions)
	}
	if suggestions[0].Extractor == nil || suggestions[0].Extractor.Name != "corr_csrf_token" || suggestions[0].Extractor.Convert != "" {
		t.Fatalf("raw extractor = %#v", suggestions[0].Extractor)
	}
	if suggestions[1].Extractor == nil || suggestions[1].Extractor.Name != "corr_csrf_token_encoded" || suggestions[1].Extractor.Convert != "url_encode" {
		t.Fatalf("encoded extractor = %#v", suggestions[1].Extractor)
	}
}

func TestDiagnoseReplayGivesEncodedSpellingOwnershipOverRawPrefix(t *testing.T) {
	const (
		recordedValue = "recorded-token%"
		replayValue   = "rotated-token%"
	)
	encoded := QueryEncodedFormOf(recordedValue)
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestURL: "/submit?csrf=" + encoded},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + replayValue + `"}`},
		{ID: 2, RequestURL: "/submit?csrf=" + encoded, EditableRequestURL: true},
	}

	suggestions, err := DiagnoseReplayChecked(recorded, replay)
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want one encoded repair without a raw-prefix repair", suggestions)
	}
	suggestion := suggestions[0]
	if suggestion.Extractor == nil || suggestion.Extractor.Convert != "url_encode" {
		t.Fatalf("extractor = %#v, want canonical encoded repair", suggestion.Extractor)
	}
	if len(suggestion.RepairLocations) != 1 {
		t.Fatalf("repair locations = %#v, want one full encoded range", suggestion.RepairLocations)
	}
	location := suggestion.RepairLocations[0]
	if got := replay[1].RequestURL[location.Start:location.End]; got != encoded {
		t.Fatalf("owned repair range = %q, want full encoded spelling %q", got, encoded)
	}
}

func TestStaleRepairLocationsDropsSelfOverlappingLiteralMatches(t *testing.T) {
	raw, encoded, unsupported, overflow := staleRepairLocations("request_body", "aaaaaaaaa", "aaaaaaaa", "", true)
	if overflow {
		t.Fatal("self-overlapping matches unexpectedly exhausted the location budget")
	}
	if len(raw) != 1 || raw[0].Start != 0 || raw[0].End != 8 {
		t.Fatalf("raw locations = %#v, want one leftmost non-overlapping range", raw)
	}
	if len(encoded) != 0 || len(unsupported) != 0 {
		t.Fatalf("unexpected encoded locations: query=%#v unsupported=%#v", encoded, unsupported)
	}
}

func TestDiagnoseReplayUsesEncodedVariableAcrossSeparateConsumers(t *testing.T) {
	recordedValue := "token /+ value"
	replayValue := "rotated /+ value"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestBody: "raw=" + recordedValue},
		{ID: 3, RequestURL: "/submit?encoded=" + QueryEncodedFormOf(recordedValue)},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + replayValue + `"}`},
		{ID: 2, RequestBody: "raw=" + recordedValue},
		{ID: 3, RequestURL: "/submit?encoded=" + QueryEncodedFormOf(recordedValue)},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 2 {
		t.Fatalf("suggestions = %#v, want one per consumer", suggestions)
	}
	if suggestions[0].Extractor == nil || suggestions[0].Extractor.Name != "corr_csrf_token" {
		t.Fatalf("raw suggestion = %#v", suggestions[0])
	}
	if suggestions[1].Extractor == nil || suggestions[1].Extractor.Name != "corr_csrf_token_encoded" || suggestions[1].Extractor.Convert != "url_encode" {
		t.Fatalf("encoded suggestion = %#v", suggestions[1])
	}
}

func TestRepairLocationsUseJavaScriptUTF16Offsets(t *testing.T) {
	const recordedValue = "TOKEN-OLD-123456"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestBody: "emoji=😀&csrf=" + recordedValue},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-NEW-654321"}`},
		{ID: 2, RequestBody: "emoji=😀&csrf=" + recordedValue},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 || len(suggestions[0].RepairLocations) != 1 {
		t.Fatalf("suggestions = %#v", suggestions)
	}
	location := suggestions[0].RepairLocations[0]
	if location.Start != 14 || location.End != 30 {
		t.Fatalf("UTF-16 location = %#v, want [14,30)", location)
	}
}

func TestDiagnoseReplayFreshValueElsewhereDoesNotHideStaleField(t *testing.T) {
	recorded := sampleRecording()
	fresh := "TOKEN-BBB-2222-replayed"
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + fresh + `"}`},
		{
			ID:             2,
			RequestBody:    "stale=TOKEN-AAA-1111-recorded&fresh=" + fresh,
			RequestHeaders: map[string]string{"X-CSRF": fresh},
		},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want stale request_body repair", suggestions)
	}
	if got := suggestions[0].AffectedFields; !equalStrings(got, []string{"request_body"}) {
		t.Fatalf("affected fields = %#v, want only request_body", got)
	}
}

func TestDiagnoseReplayFreshValueContainingRecordedPrefixIsNotStale(t *testing.T) {
	recordedValue := "TOKEN-AAA-1111"
	replayValue := recordedValue + "-rotated"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestBody: "csrf=" + recordedValue},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + replayValue + `"}`},
		{ID: 2, RequestBody: "csrf=" + replayValue},
	}
	if suggestions := DiagnoseReplay(recorded, replay); len(suggestions) != 0 {
		t.Fatalf("fresh value containing recorded prefix must not look stale: %#v", suggestions)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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

func TestDiagnoseReplayWarnsWhenSourceObservationMissing(t *testing.T) {
	recorded := sampleRecording()
	replay := []Observation{{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded&submit=1"}}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 || suggestions[0].Severity != SeverityWarning {
		t.Fatalf("missing source observation suggestions = %#v, want one warning", suggestions)
	}
	if suggestions[0].SourceStepID != 1 || suggestions[0].Title != "Correlation source missing on replay" {
		t.Fatalf("unexpected missing-source warning: %#v", suggestions[0])
	}
}

func TestDiagnoseReplayDoesNotFallbackToSameNameAtDifferentPath(t *testing.T) {
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"payment":{"token":"PAYMENT-OLD-111111"}}`},
		{ID: 2, RequestBody: "token=PAYMENT-OLD-111111"},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"token":"UNRELATED-NEW-222222"}`},
		{ID: 2, RequestBody: "token=PAYMENT-OLD-111111"},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 || suggestions[0].Severity != SeverityWarning {
		t.Fatalf("different-path candidate suggestions = %#v, want boundary warning", suggestions)
	}
	if suggestions[0].ReplayValue != "" || suggestions[0].Extractor == nil || suggestions[0].Extractor.Expression != "$.payment.token" {
		t.Fatalf("unrelated candidate must not become replay value: %#v", suggestions[0])
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

func TestDiagnoseReplayMarksOnlyProvenEditableLocationsForAutomaticRepair(t *testing.T) {
	const recordedValue = "TOKEN-OLD-123456"
	const replayValue = "TOKEN-NEW-654321"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{
			ID:             2,
			RequestURL:     "/submit?csrf=" + recordedValue,
			RequestBody:    "csrf=" + recordedValue,
			RequestHeaders: map[string]string{"X-CSRF": recordedValue},
		},
	}

	for _, test := range []struct {
		name       string
		replayStep Observation
		wantAll    bool
		wantAction string
	}{
		{
			name: "trusted authored surfaces",
			replayStep: Observation{
				ID:                     2,
				RequestURL:             "/submit?csrf=" + recordedValue,
				RequestBody:            "csrf=" + recordedValue,
				RequestHeaders:         map[string]string{"X-CSRF": recordedValue},
				EditableRequestURL:     true,
				EditableRequestBody:    true,
				EditableRequestHeaders: map[string]bool{"x-csrf": true},
			},
			wantAll:    true,
			wantAction: "auto_apply_available",
		},
		{
			name: "generic wire surfaces",
			replayStep: Observation{
				ID:             2,
				RequestURL:     "/submit?csrf=" + recordedValue,
				RequestBody:    "csrf=" + recordedValue,
				RequestHeaders: map[string]string{"X-CSRF": recordedValue},
			},
			wantAction: "manual_value_replacement_required",
		},
		{
			name: "mixed proof fails closed",
			replayStep: Observation{
				ID:                     2,
				RequestURL:             "/submit?csrf=" + recordedValue,
				RequestBody:            "csrf=" + recordedValue,
				RequestHeaders:         map[string]string{"X-CSRF": recordedValue},
				EditableRequestURL:     true,
				EditableRequestHeaders: map[string]bool{"X-CSRF": true},
			},
			wantAction: "manual_value_replacement_required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			replay := []Observation{
				{ID: 1, ResponseBody: `{"csrf_token":"` + replayValue + `"}`},
				test.replayStep,
			}
			suggestions := DiagnoseReplay(recorded, replay)
			if len(suggestions) != 1 || len(suggestions[0].RepairLocations) != 3 {
				t.Fatalf("suggestions = %#v, want one repair with URL/body/header locations", suggestions)
			}
			allExact := true
			for _, location := range suggestions[0].RepairLocations {
				allExact = allExact && location.EditableSurfaceExact
			}
			if allExact != test.wantAll {
				t.Errorf("all repair locations exact = %t, want %t: %#v", allExact, test.wantAll, suggestions[0].RepairLocations)
			}
			if suggestions[0].ReviewAction != test.wantAction {
				t.Fatalf("review action = %q, want %q", suggestions[0].ReviewAction, test.wantAction)
			}
		})
	}
}

func TestDiagnoseReplayRequiresManualRepairForTransformedBody(t *testing.T) {
	const recordedValue = "TOKEN-OLD-123456"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestBody: "csrf=" + recordedValue},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-NEW-654321"}`},
		{ID: 2, RequestBody: "csrf=" + recordedValue, RequestBodyTransformed: true},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want one manual transformed-body repair", suggestions)
	}
	suggestion := suggestions[0]
	if suggestion.Extractor != nil || len(suggestion.RepairLocations) != 0 || suggestion.ReviewAction != "manual_value_replacement_required" {
		t.Fatalf("transformed body exposed unsafe automatic repair: %#v", suggestion)
	}
	if got := suggestion.AffectedFields; !equalStrings(got, []string{"request_body"}) {
		t.Fatalf("affected fields = %#v, want transformed request_body", got)
	}
}

func TestDiagnoseReplayFailsClosedWhenRepairLocationLimitExceeded(t *testing.T) {
	const recordedValue = "TOKEN-OLD-123456"
	recorded := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedValue + `"}`},
		{ID: 2, RequestBody: "csrf=" + recordedValue},
	}
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-NEW-654321"}`},
		{ID: 2, RequestBody: strings.Repeat("csrf="+recordedValue+"&", maxRepairLocationsPerField+1), EditableRequestBody: true},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %#v, want one atomic manual repair", suggestions)
	}
	suggestion := suggestions[0]
	if suggestion.Extractor != nil || len(suggestion.RepairLocations) != 0 || suggestion.ReviewAction != "manual_value_replacement_required" {
		t.Fatalf("overflow returned a partial automatic repair: %#v", suggestion)
	}
}

func TestDiagnoseReplayCheckedRejectsReplayCandidateOverflow(t *testing.T) {
	var body strings.Builder
	body.WriteByte('[')
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"token":"token-%06d"}`, index)
	}
	body.WriteByte(']')

	suggestions, err := DiagnoseReplayChecked([]Observation{{ID: 1}}, []Observation{{ID: 1, ResponseBody: body.String()}})
	if !errors.Is(err, ErrCorrelationWorkLimit) || suggestions != nil {
		t.Fatalf("DiagnoseReplayChecked overflow = %#v, error %v; want nil suggestions with ErrCorrelationWorkLimit", suggestions, err)
	}
}

func TestDiagnoseReplayCheckedRejectsRecordedCandidateOverflow(t *testing.T) {
	var body strings.Builder
	body.WriteByte('[')
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"token":"token-%06d"}`, index)
	}
	body.WriteByte(']')

	recorded := []Observation{
		{ID: 1, ResponseBody: body.String()},
		// The reused value is deliberately the candidate beyond the retained
		// prefix. Treating the prefix as complete would return a clean diagnosis.
		{ID: 2, RequestBody: fmt.Sprintf("token-%06d", maxCorrelationCandidatesPerObservation)},
	}
	suggestions, err := DiagnoseReplayChecked(recorded, nil)
	if !errors.Is(err, ErrCorrelationWorkLimit) || suggestions != nil {
		t.Fatalf("DiagnoseReplayChecked recorded overflow = %#v, error %v; want nil suggestions with ErrCorrelationWorkLimit", suggestions, err)
	}
}

func TestDiagnoseReplayCheckedRejectsReplayObservationOverflow(t *testing.T) {
	replay := make([]Observation, maxCorrelationObservationsPerAnalysis+1)
	for index := range replay {
		replay[index].ID = index + 1
	}
	if _, err := DiagnoseReplayChecked(nil, replay); !errors.Is(err, ErrCorrelationWorkLimit) {
		t.Fatalf("DiagnoseReplayChecked replay observation error = %v, want ErrCorrelationWorkLimit", err)
	}
}

func TestDiagnoseReplayCheckedEnforcesAggregateRepairSearchBudget(t *testing.T) {
	recorded := sampleRecording()
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-BBB-2222-replayed"}`},
		{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded&submit=1"},
	}
	limits := defaultRepairAnalysisLimits
	limits.searchBytes = 1
	if _, err := diagnoseReplayCheckedWithLimits(recorded, replay, limits); !errors.Is(err, ErrCorrelationWorkLimit) {
		t.Fatalf("diagnoseReplayCheckedWithLimits search error = %v, want ErrCorrelationWorkLimit", err)
	}
}

func TestDiagnoseReplayCheckedEnforcesAggregateSuggestionBudget(t *testing.T) {
	recorded := sampleRecording()
	replay := []Observation{{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded&submit=1"}}
	limits := defaultRepairAnalysisLimits
	limits.suggestions = 0
	if _, err := diagnoseReplayCheckedWithLimits(recorded, replay, limits); !errors.Is(err, ErrCorrelationWorkLimit) {
		t.Fatalf("diagnoseReplayCheckedWithLimits suggestion error = %v, want ErrCorrelationWorkLimit", err)
	}
}
