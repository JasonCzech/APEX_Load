package correlate

import "testing"

func TestDiagnoseReplayFlagsUnexplainedRejection(t *testing.T) {
	// The request is byte-identical to the recording, yet the server rejected the
	// replay (HTTP 403). No dynamic value rotated, so the value-diff loop emits
	// nothing — the status-aware pass must surface it.
	recorded := []Observation{{ID: 1, RequestBody: "x-anti-replay: abc", ResponseBody: "ok"}}
	replay := []Observation{{ID: 1, RequestBody: "x-anti-replay: abc", ResponseBody: "denied", ResponseStatus: 403}}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %d: %#v", len(suggestions), suggestions)
	}
	s := suggestions[0]
	if s.RuleID != "replay_rejected_unexplained" || s.FailingStepID != 1 || s.Severity != SeverityWarning {
		t.Fatalf("unexpected suggestion: %#v", s)
	}
}

func TestDiagnoseReplayIgnoresRejectionWithoutStatus(t *testing.T) {
	// Without ResponseStatus the pass is a no-op (backward compatible).
	recorded := []Observation{{ID: 1, RequestBody: "x-anti-replay: abc", ResponseBody: "ok"}}
	replay := []Observation{{ID: 1, RequestBody: "x-anti-replay: abc", ResponseBody: "denied"}}

	if suggestions := DiagnoseReplay(recorded, replay); len(suggestions) != 0 {
		t.Fatalf("expected no suggestions without status, got %#v", suggestions)
	}
}

func TestDiagnoseReplayDoesNotDoubleReportExplainedRejection(t *testing.T) {
	// A stale-value rejection is already explained by the error suggestion; the
	// status pass must not add a duplicate for the same failing step.
	recorded := sampleRecording()
	replay := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-BBB-2222-replayed"}`},
		{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded&submit=1", ResponseStatus: 403},
	}

	suggestions := DiagnoseReplay(recorded, replay)
	if len(suggestions) != 1 {
		t.Fatalf("expected exactly 1 suggestion (no duplicate), got %d: %#v", len(suggestions), suggestions)
	}
	if suggestions[0].Severity != SeverityError || suggestions[0].FailingStepID != 2 {
		t.Fatalf("expected the stale-value error to win, got %#v", suggestions[0])
	}
}
