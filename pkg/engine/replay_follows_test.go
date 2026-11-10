package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// appendDiagnosedReplayEvent mirrors the replay path: diagnose the failed
// event, then hand it to the append seam.
func appendDiagnosedReplayEvent(report *ReplayReport, event ReplayActionEvent) ReplayActionEvent {
	attachReplayDiagnosis(&event)
	return appendReplayReportEvent(report, event)
}

func replayFollowsRootEvent() ReplayActionEvent {
	return ReplayActionEvent{
		EventID:      "session:init:000:000",
		Phase:        "init",
		ActionName:   "Login",
		ActionType:   "POST",
		Method:       "POST",
		Target:       "https://example.test/login",
		StatusCode:   200,
		ErrorMessage: `extractor "c_cshc" found no match`,
	}
}

func replayAuraInvalidSessionEvidence() map[string]any {
	return map[string]any{"aura": map[string]any{
		"success":      false,
		"complete":     true,
		"has_response": true,
		"issues":       []any{map[string]any{"code": "invalid_session"}},
	}}
}

func TestReplayFollowerLinksHTTP401ToFirstFailure(t *testing.T) {
	report := &ReplayReport{}
	root := appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
	if root.Diagnosis == nil || root.Diagnosis.FollowsFailureEventID != "" || report.DiagnosisSummary.FollowingFailureCount != 0 {
		t.Fatalf("the first failure linked to itself: %#v %#v", root.Diagnosis, report.DiagnosisSummary)
	}
	follower := appendDiagnosedReplayEvent(report, ReplayActionEvent{
		EventID: "session:action:001:001", Phase: "action", ActionName: "Aura getRecord", ActionType: "POST", Method: "POST",
		Target: "https://example.test/aura", StatusCode: 401, ErrorMessage: "Assertion: expected status 200, got 401",
	})
	diagnosis := follower.Diagnosis
	if diagnosis == nil || diagnosis.FollowsFailureEventID != root.EventID || diagnosis.FollowsFailureAction != "Login" || diagnosis.FollowsReason != ReplayFollowsReasonHTTP401 {
		t.Fatalf("follower diagnosis = %#v, want link to %q via http_401", diagnosis, root.EventID)
	}
	if diagnosis.Category != "assertion" || diagnosis.Cause != "Assertion: expected status 200, got 401" || diagnosis.Action == "" {
		t.Fatalf("rule-based diagnosis changed: %#v", diagnosis)
	}
	summary := report.DiagnosisSummary
	if summary.FailureCount != 2 || summary.FollowingFailureCount != 1 || summary.FirstFailureEventID != root.EventID {
		t.Fatalf("summary = %#v, want 2 failures with 1 follower of the root", summary)
	}
}

func TestReplayFollowerReasonsAndPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event ReplayActionEvent
		want  string
		vars  []string
	}{
		{
			name:  "aura invalid session at HTTP 200",
			event: ReplayActionEvent{StatusCode: 200, ActionType: "POST", Target: "/aura", ErrorMessage: "Aura validation failed: invalid_session", ProtocolEvidence: replayAuraInvalidSessionEvidence()},
			want:  ReplayFollowsReasonAuraInvalidSession,
		},
		{
			name:  "aura wins over 401 and unresolved variables",
			event: ReplayActionEvent{StatusCode: 401, ActionType: "POST", Target: "/aura", Body: "message={{var.c_interactionid}}", ErrorMessage: "Assertion: expected status 200, got 401", ProtocolEvidence: replayAuraInvalidSessionEvidence()},
			want:  ReplayFollowsReasonAuraInvalidSession,
			vars:  []string{"c_interactionid"},
		},
		{
			name:  "typed aura report",
			event: ReplayActionEvent{StatusCode: 200, ActionType: "POST", Target: "/aura", ErrorMessage: "Aura validation failed: invalid_session", ProtocolEvidence: map[string]any{"aura": aura.Report{Issues: []aura.Issue{{Code: aura.InvalidSession}}}}},
			want:  ReplayFollowsReasonAuraInvalidSession,
		},
		{
			name: "browser aura actions invalid session",
			event: ReplayActionEvent{ActionType: "browser_click", Selector: "#save", ErrorMessage: "locator #save not visible", ProtocolEvidence: map[string]any{
				"aura_actions": map[string]any{"requests": 1, "actions": []any{map[string]any{"name": "RecordUi.getRecord", "state": "invalid_session"}}},
			}},
			want: ReplayFollowsReasonAuraInvalidSession,
		},
		{
			name:  "403 wins over unresolved variables",
			event: ReplayActionEvent{StatusCode: 403, ActionType: "GET", Target: "/api/{{var.record_id}}", ErrorMessage: "forbidden"},
			want:  ReplayFollowsReasonHTTP403,
			vars:  []string{"record_id"},
		},
		{
			name: "unresolved variables only",
			event: ReplayActionEvent{StatusCode: 200, ActionType: "POST", Target: "/aura?r={{var.b}}", Body: "token={{ var.a }}&again={{var.b}}",
				Headers: map[string]string{"X-Token": "{{var.c}}"}, Value: "{{param.p}}", ErrorMessage: "Assertion: body does not contain Saved"},
			want: ReplayFollowsReasonUnresolvedVariable,
			vars: []string{"a", "b", "c"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := &ReplayReport{}
			appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
			tc.event.EventID = "session:action:000:001"
			tc.event.Phase = "action"
			tc.event.ActionName = "Follower"
			follower := appendDiagnosedReplayEvent(report, tc.event)
			if follower.Diagnosis == nil || follower.Diagnosis.FollowsReason != tc.want || follower.Diagnosis.FollowsFailureEventID != "session:init:000:000" {
				t.Fatalf("diagnosis = %#v, want reason %q linked to the root", follower.Diagnosis, tc.want)
			}
			if !slices.Equal(follower.Diagnosis.UnresolvedVariables, tc.vars) {
				t.Fatalf("unresolved variables = %#v, want %#v", follower.Diagnosis.UnresolvedVariables, tc.vars)
			}
			if report.DiagnosisSummary.FollowingFailureCount != 1 {
				t.Fatalf("following count = %d, want 1", report.DiagnosisSummary.FollowingFailureCount)
			}
		})
	}
}

func TestReplayFollowerNotLinkedWithoutSessionSignal(t *testing.T) {
	report := &ReplayReport{}
	appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
	for index, event := range []ReplayActionEvent{
		{StatusCode: 500, ErrorMessage: "server error"},
		{StatusCode: 200, ErrorMessage: "Assertion: body does not contain Saved", Body: "plain body"},
		{StatusCode: 200, ErrorMessage: "Aura validation failed: framework_error", ProtocolEvidence: map[string]any{"aura": map[string]any{"issues": []any{map[string]any{"code": "framework_error"}}}}},
		{StatusCode: 404, ErrorMessage: "not found", Body: "{{param.unbound}}"},
	} {
		event.EventID = fmt.Sprintf("session:action:000:%03d", index+1)
		event.Phase = "action"
		event.ActionName = "Other"
		event.ActionType = "POST"
		got := appendDiagnosedReplayEvent(report, event)
		if got.Diagnosis == nil || got.Diagnosis.FollowsFailureEventID != "" || got.Diagnosis.FollowsReason != "" || got.Diagnosis.FollowsFailureAction != "" {
			t.Fatalf("event %d linked without a session-loss signal: %#v", index, got.Diagnosis)
		}
		if len(got.RepairSuggestions) != 1 || got.RepairSuggestions[0].DraftPatchType == ReplayFollowsFailureDraftPatchType {
			t.Fatalf("event %d suggestions replaced without a link: %#v", index, got.RepairSuggestions)
		}
	}
	if report.DiagnosisSummary.FollowingFailureCount != 0 || report.DiagnosisSummary.FailureCount != 5 {
		t.Fatalf("summary = %#v, want 5 failures and no followers", report.DiagnosisSummary)
	}
}

func TestReplayFirstFailureWithSessionSignalDoesNotLinkToItself(t *testing.T) {
	report := &ReplayReport{}
	first := appendDiagnosedReplayEvent(report, ReplayActionEvent{
		EventID: "session:action:000:000", Phase: "action", ActionName: "Aura", ActionType: "POST", Target: "/aura", Body: "{{var.x}}",
		StatusCode: 401, ErrorMessage: "unauthorized",
	})
	if first.Diagnosis.FollowsFailureEventID != "" || first.Diagnosis.FollowsReason != "" || report.DiagnosisSummary.FollowingFailureCount != 0 {
		t.Fatalf("first failure linked to itself: %#v", first.Diagnosis)
	}
	if !slices.Equal(first.Diagnosis.UnresolvedVariables, []string{"x"}) {
		t.Fatalf("first failure unresolved variables = %#v, want [x]", first.Diagnosis.UnresolvedVariables)
	}
	// A re-appended event that reuses the first failure's ID (a debugger rerun)
	// is not its own follower either.
	again := appendDiagnosedReplayEvent(report, ReplayActionEvent{
		EventID: "session:action:000:000", Phase: "action", ActionName: "Aura", ActionType: "POST", StatusCode: 401, ErrorMessage: "unauthorized",
	})
	if again.Diagnosis.FollowsFailureEventID != "" || report.DiagnosisSummary.FollowingFailureCount != 0 {
		t.Fatalf("event with the first failure's ID linked to itself: %#v", again.Diagnosis)
	}
}

func TestReplayFollowerAsyncPhasesAreExcluded(t *testing.T) {
	t.Run("async follower is not linked", func(t *testing.T) {
		report := &ReplayReport{}
		appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
		async := ReplayActionEvent{EventID: "session:async:-01:001", Phase: "async", ActionName: "poll", ActionType: "http_async", StatusCode: 401, ErrorMessage: "async response failed"}
		// Async responses are not diagnosed on the replay path; give this one a
		// diagnosis anyway so only the phase rule keeps it unlinked.
		got := appendDiagnosedReplayEvent(report, async)
		if got.Diagnosis == nil || got.Diagnosis.FollowsFailureEventID != "" || report.DiagnosisSummary.FollowingFailureCount != 0 {
			t.Fatalf("async event linked: %#v", got.Diagnosis)
		}
	})
	for _, phase := range []string{"async", "finalize"} {
		t.Run(phase+" first failure is not a root", func(t *testing.T) {
			report := &ReplayReport{}
			appendDiagnosedReplayEvent(report, ReplayActionEvent{EventID: "session:" + phase + ":-01:000", Phase: phase, ActionName: "background", ActionType: "http_async", StatusCode: 500, ErrorMessage: "failed"})
			got := appendDiagnosedReplayEvent(report, ReplayActionEvent{EventID: "session:action:000:001", Phase: "action", ActionName: "Aura", ActionType: "POST", StatusCode: 401, ErrorMessage: "unauthorized"})
			if got.Diagnosis.FollowsFailureEventID != "" || report.DiagnosisSummary.FollowingFailureCount != 0 {
				t.Fatalf("linked to a %s first failure: %#v", phase, got.Diagnosis)
			}
		})
	}
	t.Run("teardown follower links", func(t *testing.T) {
		report := &ReplayReport{}
		appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
		got := appendDiagnosedReplayEvent(report, ReplayActionEvent{EventID: "session:teardown:000:002", Phase: "teardown", ActionName: "Logout", ActionType: "GET", StatusCode: 403, ErrorMessage: "forbidden"})
		if got.Diagnosis.FollowsReason != ReplayFollowsReasonHTTP403 || report.DiagnosisSummary.FollowingFailureCount != 1 {
			t.Fatalf("teardown follower = %#v", got.Diagnosis)
		}
	})
}

func TestReplayFollowerSummaryCountsEveryLinkedFailure(t *testing.T) {
	report := &ReplayReport{}
	appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
	appendReplayReportEvent(report, ReplayActionEvent{EventID: "session:action:000:001", Phase: "action", ActionName: "ok", Success: true, StatusCode: 200})
	for index := 0; index < 26; index++ {
		appendDiagnosedReplayEvent(report, ReplayActionEvent{
			EventID: fmt.Sprintf("session:action:%03d:%03d", index+1, index+2), Phase: "action", ActionName: "Aura", ActionType: "POST",
			Target: "/aura", Body: "message={{var.c_interactionid}}", StatusCode: 401, ErrorMessage: "Assertion: expected status 200, got 401",
		})
	}
	appendDiagnosedReplayEvent(report, ReplayActionEvent{EventID: "session:action:099:099", Phase: "action", ActionName: "Unrelated", ActionType: "GET", StatusCode: 500, ErrorMessage: "boom"})
	summary := report.DiagnosisSummary
	if summary.FailureCount != 28 || summary.FollowingFailureCount != 26 || summary.Categories["assertion"] != 26 {
		t.Fatalf("summary = %#v, want 28 failures, 26 followers, categories unchanged", summary)
	}
	payload, err := json.Marshal(summary)
	if err != nil || !strings.Contains(string(payload), `"following_failure_count":26`) {
		t.Fatalf("summary JSON = %s %v", payload, err)
	}
}

func TestReplayFollowerSuggestionsReplaced(t *testing.T) {
	report := &ReplayReport{}
	root := appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
	follower := appendDiagnosedReplayEvent(report, ReplayActionEvent{
		EventID: "session:action:001:001", Phase: "action", ActionName: "Aura getRecord", ActionType: "POST",
		Target: "/aura", StatusCode: 401, ErrorMessage: "Assertion: expected status 200, got 401",
	})
	if len(follower.RepairSuggestions) != 1 {
		t.Fatalf("follower suggestions = %#v, want one", follower.RepairSuggestions)
	}
	suggestion := follower.RepairSuggestions[0]
	if suggestion.ID != "session:action:001:001:fix-first-failure" || suggestion.Title != "Fix the first failure first" ||
		suggestion.DraftPatchType != "follows_failure" || len(suggestion.Fields) != 0 || !strings.Contains(suggestion.Description, `"Login"`) {
		t.Fatalf("follower suggestion = %#v", suggestion)
	}
	// The report-level list keeps the root's own suggestion and adds neither
	// the follower's pointer nor a "Review assertion" for it.
	reportSuggestions := collectReplayRepairSuggestions(report.Events)
	if len(reportSuggestions) != len(root.RepairSuggestions) || len(reportSuggestions) == 0 {
		t.Fatalf("report suggestions = %#v, want only the root's %#v", reportSuggestions, root.RepairSuggestions)
	}
	for _, s := range reportSuggestions {
		if s.DraftPatchType == "follows_failure" || s.DraftPatchType == "adjust_assertion" {
			t.Fatalf("report suggestions include follower suggestion %#v", s)
		}
	}
	// A follower without an event ID still gets a stable suggestion ID.
	unnamed := appendDiagnosedReplayEvent(report, ReplayActionEvent{Phase: "action", ActionName: "x", ActionType: "POST", StatusCode: 401, ErrorMessage: "unauthorized"})
	if len(unnamed.RepairSuggestions) != 1 || unnamed.RepairSuggestions[0].ID != "action-2:fix-first-failure" {
		t.Fatalf("unnamed follower suggestions = %#v", unnamed.RepairSuggestions)
	}
}

func TestReplayFollowerRetentionProjectionKeepsLink(t *testing.T) {
	report := &ReplayReport{}
	appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
	follower := ReplayActionEvent{
		EventID: "session:action:001:001", Phase: "action", ActionName: "Aura", ActionType: "POST", Target: "/aura",
		Body: "message={{var.c_interactionid}}", StatusCode: 401, ErrorMessage: "Assertion: expected status 200, got 401",
	}
	attachReplayDiagnosis(&follower)
	retained := appendReplayReportEvent(report, follower)
	if follower.Diagnosis.FollowsFailureEventID != "" || len(follower.Diagnosis.UnresolvedVariables) != 0 {
		t.Fatalf("append seam wrote through the caller's diagnosis pointer: %#v", follower.Diagnosis)
	}
	if retained.Diagnosis.FollowsFailureEventID == "" {
		t.Fatalf("retained follower lost its link: %#v", retained.Diagnosis)
	}

	full := report.Events[len(report.Events)-1]
	compact := BoundReplayActionEventForRetention(full, false)
	if compact.Body != "" || compact.Diagnosis == nil {
		t.Fatalf("compact projection = %#v, want stripped body and kept diagnosis", compact)
	}
	if compact.Diagnosis.FollowsFailureEventID != "session:init:000:000" || compact.Diagnosis.FollowsFailureAction != "Login" ||
		compact.Diagnosis.FollowsReason != ReplayFollowsReasonHTTP401 || !slices.Equal(compact.Diagnosis.UnresolvedVariables, []string{"c_interactionid"}) {
		t.Fatalf("compact diagnosis lost follow-on identity: %#v", compact.Diagnosis)
	}
	compact.Diagnosis.UnresolvedVariables[0] = "mutated"
	if full.Diagnosis.UnresolvedVariables[0] != "c_interactionid" {
		t.Fatal("compact projection shares the unresolved variable slice")
	}
	cloned := cloneReplayDiagnosis(full.Diagnosis)
	cloned.UnresolvedVariables[0] = "mutated"
	if full.Diagnosis.UnresolvedVariables[0] != "c_interactionid" {
		t.Fatal("cloneReplayDiagnosis shares the unresolved variable slice")
	}

	long := strings.Repeat("n", maxReplayIdentityBytes+64)
	oversized := ReplayActionEvent{Diagnosis: &ReplayDiagnosis{
		FollowsFailureEventID: long, FollowsFailureAction: long, FollowsReason: long,
		UnresolvedVariables: []string{long, "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"},
	}}
	stripReplayEventRichEvidence(&oversized)
	diagnosis := oversized.Diagnosis
	if len(diagnosis.FollowsFailureEventID) > maxReplayIdentityBytes || len(diagnosis.FollowsFailureAction) > maxReplayIdentityBytes ||
		len(diagnosis.FollowsReason) > maxReplayIdentityBytes || len(diagnosis.UnresolvedVariables) != maxReplayUnresolvedVariables ||
		len(diagnosis.UnresolvedVariables[0]) > maxReplayIdentityBytes {
		t.Fatalf("compact follow-on fields are not bounded: %d %d %d %d", len(diagnosis.FollowsFailureEventID), len(diagnosis.FollowsFailureAction), len(diagnosis.FollowsReason), len(diagnosis.UnresolvedVariables))
	}
}

func TestReplayUnresolvedVariablesOnAnyFailedEvent(t *testing.T) {
	report := &ReplayReport{}
	names := make([]string, 0, 12)
	var body strings.Builder
	for index := 11; index >= 0; index-- {
		name := fmt.Sprintf("v%02d", index)
		names = append(names, name)
		fmt.Fprintf(&body, "%s={{var.%s}}&dup={{var.%s}}&", name, name, name)
	}
	got := appendDiagnosedReplayEvent(report, ReplayActionEvent{
		EventID: "session:action:000:000", Phase: "action", ActionName: "Submit", ActionType: "POST", Target: "/submit",
		Body: body.String(), StatusCode: 200, ErrorMessage: "Assertion: body does not contain Saved",
	})
	slices.Sort(names)
	if !slices.Equal(got.Diagnosis.UnresolvedVariables, names[:maxReplayUnresolvedVariables]) {
		t.Fatalf("unresolved variables = %#v, want first %d sorted names", got.Diagnosis.UnresolvedVariables, maxReplayUnresolvedVariables)
	}
	passed := appendReplayReportEvent(report, ReplayActionEvent{EventID: "session:action:000:001", Phase: "action", Success: true, Body: "{{var.ignored}}"})
	if passed.Diagnosis != nil {
		t.Fatalf("passing event gained a diagnosis: %#v", passed.Diagnosis)
	}
	if got := replayUnresolvedVariables(ReplayActionEvent{Body: "{{var.}} {{ secret.x }} {{param.p}} {{var . y}}"}); got != nil {
		t.Fatalf("non-variable tokens reported: %#v", got)
	}
}

func TestReplayFollowerRespectsUnavailableEvidence(t *testing.T) {
	report := &ReplayReport{}
	appendDiagnosedReplayEvent(report, replayFollowsRootEvent())
	report.RuntimeEvidenceUnavailable = true
	got := appendDiagnosedReplayEvent(report, ReplayActionEvent{
		EventID: "session:action:001:001", Phase: "action", ActionName: "Aura", ActionType: "POST",
		Target: "/aura", Body: "{{var.c_interactionid}}", StatusCode: 401, ErrorMessage: "unauthorized",
	})
	if got.Diagnosis != nil || len(got.RepairSuggestions) != 0 {
		t.Fatalf("unavailable evidence kept a diagnosis: %#v %#v", got.Diagnosis, got.RepairSuggestions)
	}
	if report.DiagnosisSummary.FollowingFailureCount != 0 || report.DiagnosisSummary.FailureCount != 1 {
		t.Fatalf("summary counted a failure while evidence was unavailable: %#v", report.DiagnosisSummary)
	}

	// Direct calls are inert too, even with a diagnosis present.
	event := ReplayActionEvent{EventIndex: 9, EventID: "late", Phase: "action", StatusCode: 401, Body: "{{var.x}}", Diagnosis: &ReplayDiagnosis{Category: "auth_session"}}
	annotateReplayFailureDiagnosis(report, &event)
	if event.Diagnosis.FollowsFailureEventID != "" || event.Diagnosis.UnresolvedVariables != nil {
		t.Fatalf("annotation ran while evidence was unavailable: %#v", event.Diagnosis)
	}

	OmitUnavailableReplayReport(report)
	for _, retained := range report.Events {
		if retained.Diagnosis != nil {
			t.Fatalf("omitted report kept a diagnosis: %#v", retained.Diagnosis)
		}
	}
	if report.DiagnosisSummary != nil {
		t.Fatalf("omitted report kept a summary: %#v", report.DiagnosisSummary)
	}
}

func TestClassifyReplayFailureAuraInvalidSessionIsAuthSession(t *testing.T) {
	event := ReplayActionEvent{ActionType: "POST", Method: "POST", Target: "/aura", StatusCode: 200, ErrorMessage: "Aura validation failed: invalid_session", ProtocolEvidence: replayAuraInvalidSessionEvidence()}
	diagnosis := classifyReplayFailure(event)
	if diagnosis.Category != "auth_session" || !slices.Contains(diagnosis.Signals, "auth_session") {
		t.Fatalf("diagnosis = %#v, want auth_session", diagnosis)
	}
	suggestions := replayRepairSuggestionsForDiagnosis(event, diagnosis)
	if len(suggestions) != 1 || suggestions[0].DraftPatchType != "repair_auth_session" {
		t.Fatalf("suggestions = %#v, want repair_auth_session", suggestions)
	}
	// An unresolved token in the body no longer turns a rejected session into a
	// correlation failure.
	event.Body = "message={{var.c_interactionid}}"
	if got := classifyReplayFailure(event).Category; got != "auth_session" {
		t.Fatalf("aura invalid session with unresolved body = %q, want auth_session", got)
	}

	for name, other := range map[string]ReplayActionEvent{
		"other aura issue": {ActionType: "POST", Target: "/aura", StatusCode: 200, ErrorMessage: "Aura validation failed: framework_error", ProtocolEvidence: map[string]any{"aura": map[string]any{"issues": []any{map[string]any{"code": "framework_error"}}}}},
		"no aura evidence": {ActionType: "POST", Target: "/aura", StatusCode: 200, ErrorMessage: "Aura validation failed: invalid_session"},
		"server error":     {ActionType: "POST", Target: "/aura", StatusCode: 503, ErrorMessage: "Aura validation failed: invalid_session", ProtocolEvidence: replayAuraInvalidSessionEvidence()},
	} {
		want := map[string]string{"other aura issue": "runtime_environment", "no aura evidence": "runtime_environment", "server error": "service_unavailable"}[name]
		if got := classifyReplayFailure(other).Category; got != want {
			t.Errorf("%s: category %q, want %q", name, got, want)
		}
	}
}

var registerReplayFollowsHTTPProtocol sync.Once

func TestReplayScriptLinksSessionLossFailuresToFirstFailure(t *testing.T) {
	protocolName := "replay-follows-local-http"
	registerReplayFollowsHTTPProtocol.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><head><title>Login | Salesforce</title></head><body><input type="password" name="pw"></body></html>`)
		case "/aura":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"event":{"descriptor":"markup://aura:invalidSession","attributes":{"values":{}}},"exceptionEvent":true}`)
		case "/aura-200":
			// Salesforce also answers a rejected session at HTTP 200.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"event":{"descriptor":"markup://aura:invalidSession","attributes":{"values":{}}},"exceptionEvent":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	script := &dsl.Script{
		Name:     "Follows first failure",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: []dsl.Action{
			{
				Name: "Login", Type: "POST", Method: "POST", Target: "/login", Body: "un=user",
				Headers:    map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
				Extractors: []dsl.Extractor{{Name: "x", Source: "body", Type: "regex", Expression: `cshc=([a-z0-9]+)`, OnMiss: "fail"}},
			},
			{
				Name: "Aura getRecord", Type: "POST", Method: "POST", Target: "/aura",
				Headers:    map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
				Body:       "message=%7B%22actions%22%3A%5B%5D%7D&interaction={{var.x}}",
				Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}},
			},
			{
				Name: "Aura saveRecord", Type: "POST", Method: "POST", Target: "/aura-200",
				Headers:    map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
				Body:       "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22fixture%22%7D%5D%7D",
				Assertions: []dsl.Assertion{{Type: "aura_actions_success"}},
			},
		},
	}
	var progress []ReplayProgressEvent
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		ReplaySessionID: "follows-session",
		IgnoreThinkTime: true,
		OnProgress:      func(event ReplayProgressEvent) { progress = append(progress, event) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Success || len(report.Events) != 3 {
		t.Fatalf("report success/events = %v/%d (%s), want three failed events", report.Success, len(report.Events), report.Error)
	}
	root, follower, auraFollower := report.Events[0], report.Events[1], report.Events[2]
	if root.Success || !strings.Contains(root.ErrorMessage, `extractor "x" found no match`) || root.Diagnosis == nil || root.Diagnosis.FollowsFailureEventID != "" {
		t.Fatalf("root event = %#v (diagnosis %#v), want an unlinked extractor failure", root, root.Diagnosis)
	}
	if follower.Success || follower.StatusCode != 401 || follower.Diagnosis == nil {
		t.Fatalf("follower event = %#v, want failed 401", follower)
	}
	diagnosis := follower.Diagnosis
	if diagnosis.FollowsFailureEventID != root.EventID || diagnosis.FollowsFailureAction != "Login" {
		t.Fatalf("follower link = %#v, want %q / Login", diagnosis, root.EventID)
	}
	// The status assertion fails before the Aura envelope is validated, so the
	// 401 is the signal and the rule-based category stays "assertion".
	if diagnosis.FollowsReason != ReplayFollowsReasonHTTP401 || diagnosis.Category != "assertion" {
		t.Fatalf("follower reason/category = %q/%q, want http_401/assertion", diagnosis.FollowsReason, diagnosis.Category)
	}
	if auraFollower.Success || auraFollower.StatusCode != 200 || auraFollower.Diagnosis == nil ||
		auraFollower.Diagnosis.FollowsReason != ReplayFollowsReasonAuraInvalidSession || auraFollower.Diagnosis.FollowsFailureEventID != root.EventID ||
		auraFollower.Diagnosis.Category != "auth_session" || len(auraFollower.Diagnosis.UnresolvedVariables) != 0 {
		t.Fatalf("aura follower = %#v (diagnosis %#v), want an auth_session failure linked via aura_invalid_session", auraFollower, auraFollower.Diagnosis)
	}
	if !slices.Equal(diagnosis.UnresolvedVariables, []string{"x"}) {
		t.Fatalf("follower unresolved variables = %#v, want [x]", diagnosis.UnresolvedVariables)
	}
	if len(follower.RepairSuggestions) != 1 || follower.RepairSuggestions[0].DraftPatchType != ReplayFollowsFailureDraftPatchType {
		t.Fatalf("follower suggestions = %#v, want the single fix-first-failure suggestion", follower.RepairSuggestions)
	}
	summary := report.DiagnosisSummary
	if summary == nil || summary.FailureCount != 3 || summary.FollowingFailureCount != 2 || summary.FirstFailureEventID != root.EventID || summary.FirstFailureAction != "Login" {
		t.Fatalf("summary = %#v, want 3 failures with two followers of Login", summary)
	}
	for _, suggestion := range report.RepairSuggestions {
		if suggestion.DraftPatchType == ReplayFollowsFailureDraftPatchType {
			t.Fatalf("report-level suggestions include a follower pointer: %#v", report.RepairSuggestions)
		}
	}
	var streamed *ReplayActionEvent
	for i := range progress {
		if progress[i].Type == ReplayProgressActionFailed && progress[i].Event != nil && progress[i].Event.EventID == follower.EventID {
			streamed = progress[i].Event
		}
	}
	if streamed == nil || streamed.Diagnosis == nil || streamed.Diagnosis.FollowsFailureEventID != root.EventID {
		t.Fatalf("streamed follower event lacks the link: %#v", streamed)
	}
}

func TestReplayFollowerSkipsFailuresThatRanAlongsideTheFirstFailure(t *testing.T) {
	report := &ReplayReport{}
	t0 := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	root := replayFollowsRootEvent()
	root.Phase, root.EventID = "action", "session:action:000:000"
	root.StartedAt, root.CompletedAt = t0, t0.Add(10*time.Millisecond)
	root.concurrentGroupID = "session:action:000:group"
	appendDiagnosedReplayEvent(report, root)

	unauthorized := func(id string, started time.Time, group string) ReplayActionEvent {
		event := ReplayActionEvent{
			EventID: id, Phase: "action", ActionName: id, ActionType: "POST", Target: "/aura",
			StartedAt: started, CompletedAt: started.Add(time.Millisecond),
			StatusCode: 401, ErrorMessage: "Assertion: expected status 200, got 401",
		}
		event.concurrentGroupID = group
		return event
	}
	// A sibling queued behind the first failure (max_concurrency 1) started
	// after it completed, but group requests cannot depend on one another.
	queuedSibling := appendDiagnosedReplayEvent(report, unauthorized("queued-sibling", t0.Add(20*time.Millisecond), root.concurrentGroupID))
	// A request already in flight when the first failure completed.
	inFlight := appendDiagnosedReplayEvent(report, unauthorized("in-flight", t0.Add(5*time.Millisecond), ""))
	later := appendDiagnosedReplayEvent(report, unauthorized("later", t0.Add(30*time.Millisecond), ""))
	for _, event := range []ReplayActionEvent{queuedSibling, inFlight} {
		if event.Diagnosis == nil || event.Diagnosis.FollowsFailureEventID != "" || event.Diagnosis.FollowsReason != "" {
			t.Fatalf("%s linked to a first failure it ran alongside: %#v", event.EventID, event.Diagnosis)
		}
		for _, suggestion := range event.RepairSuggestions {
			if suggestion.DraftPatchType == ReplayFollowsFailureDraftPatchType {
				t.Fatalf("%s suggestions were replaced: %#v", event.EventID, event.RepairSuggestions)
			}
		}
	}
	if later.Diagnosis == nil || later.Diagnosis.FollowsFailureEventID != root.EventID || later.Diagnosis.FollowsReason != ReplayFollowsReasonHTTP401 {
		t.Fatalf("later failure = %#v, want a link to the first failure", later.Diagnosis)
	}
	if got := report.DiagnosisSummary.FollowingFailureCount; got != 1 {
		t.Fatalf("following count = %d, want 1", got)
	}
	for _, retained := range report.Events {
		if retained.concurrentGroupID != "" || retained.requestVariables.known {
			t.Fatalf("retained event kept append-seam carriers: %#v", retained)
		}
	}
}

func TestReplayScriptDoesNotLinkConcurrentGroupSiblings(t *testing.T) {
	protocolName := "replay-follows-local-http"
	registerReplayFollowsHTTPProtocol.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
		case "/aura":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	status200 := []dsl.Assertion{{Type: "status_code", Expected: 200}}
	for _, maxConcurrency := range []int{1, 2} {
		t.Run(fmt.Sprintf("max_concurrency_%d", maxConcurrency), func(t *testing.T) {
			group := dsl.Action{Name: "Aura group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": maxConcurrency, "requests": []dsl.Action{
				{Name: "Boom", Type: "GET", Target: "/boom", Assertions: status200},
				{Name: "Sibling", Type: "GET", Target: "/aura", Assertions: status200},
			}}}
			script := &dsl.Script{
				Name: "Concurrent siblings", Protocol: protocolName,
				Config:  dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
				Actions: []dsl.Action{group, {Name: "After", Type: "GET", Target: "/aura", Assertions: status200}},
			}
			report, err := ReplayScript(context.Background(), script, ReplayOptions{ReplaySessionID: "siblings-session", IgnoreThinkTime: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Events) != 3 {
				t.Fatalf("events = %d (%s), want two children and the later action", len(report.Events), report.Error)
			}
			root, sibling, after := report.Events[0], report.Events[1], report.Events[2]
			if root.Success || root.ActionName != "Boom" || report.DiagnosisSummary == nil || report.DiagnosisSummary.FirstFailureEventID != root.EventID {
				t.Fatalf("first failure = %#v (summary %#v), want the Boom child", root, report.DiagnosisSummary)
			}
			if sibling.Success || sibling.StatusCode != 401 || sibling.Diagnosis == nil || sibling.Diagnosis.FollowsFailureEventID != "" {
				t.Fatalf("concurrent sibling = %#v (diagnosis %#v), want an unlinked 401", sibling, sibling.Diagnosis)
			}
			if after.Success || after.StatusCode != 401 || after.Diagnosis == nil || after.Diagnosis.FollowsFailureEventID != root.EventID || after.Diagnosis.FollowsReason != ReplayFollowsReasonHTTP401 {
				t.Fatalf("later action = %#v (diagnosis %#v), want a 401 linked to Boom", after, after.Diagnosis)
			}
			if got := report.DiagnosisSummary.FollowingFailureCount; got != 1 {
				t.Fatalf("following count = %d, want 1", got)
			}
		})
	}
}

// replayFollowsRootAndProbe replays a root failure followed by one probe
// action against a server whose responses never satisfy the probe.
func replayFollowsRootAndProbe(t *testing.T, probe dsl.Action, variables map[string]string) *ReplayReport {
	t.Helper()
	protocolName := "replay-follows-local-http"
	registerReplayFollowsHTTPProtocol.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not saved")
	}))
	t.Cleanup(server.Close)
	script := &dsl.Script{
		Name: "Root then probe", Protocol: protocolName,
		Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: []dsl.Action{
			{Name: "Login", Type: "GET", Target: "/login", Extractors: []dsl.Extractor{{Name: "x", Source: "body", Type: "regex", Expression: `cshc=([a-z0-9]+)`, OnMiss: "fail"}}},
			probe,
		},
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{ReplaySessionID: "probe-session", IgnoreThinkTime: true, Variables: variables})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 2 || report.Events[0].Success || report.Events[1].Success || report.Events[1].Diagnosis == nil {
		t.Fatalf("events = %#v (%s), want a failed root and a failed, diagnosed probe", report.Events, report.Error)
	}
	return report
}

func TestReplayStructuredBodyResolutionFailureListsOnlyUnsetVariables(t *testing.T) {
	jsonHeaders := map[string]string{"Content-Type": "application/json"}
	t.Run("set variable rejected by the body", func(t *testing.T) {
		// count is set; its value cannot stand unquoted in JSON, so the body
		// fails to resolve and the request is refused with the template intact.
		report := replayFollowsRootAndProbe(t, dsl.Action{
			Name: "Submit", Type: "POST", Target: "/submit", Headers: jsonHeaders, BodyTemplateEncoding: "json",
			Body: `{"n": {{var.count}}, "label": "{{var.label}}"}`,
		}, map[string]string{"count": "abc", "label": "set"})
		probe := report.Events[1]
		if !strings.Contains(probe.ErrorMessage, "structured body") {
			t.Fatalf("probe error = %q, want a structured body refusal", probe.ErrorMessage)
		}
		if got := probe.Diagnosis.UnresolvedVariables; len(got) != 0 {
			t.Fatalf("unresolved variables = %#v, want none: every variable was set", got)
		}
		if probe.Diagnosis.FollowsFailureEventID != "" || probe.Diagnosis.FollowsReason != "" || report.DiagnosisSummary.FollowingFailureCount != 0 {
			t.Fatalf("body defect linked as a follower: %#v (summary %#v)", probe.Diagnosis, report.DiagnosisSummary)
		}
		for _, suggestion := range probe.RepairSuggestions {
			if suggestion.DraftPatchType == ReplayFollowsFailureDraftPatchType {
				t.Fatalf("body defect suggestions were replaced: %#v", probe.RepairSuggestions)
			}
		}
	})
	t.Run("unset variable breaks the body", func(t *testing.T) {
		report := replayFollowsRootAndProbe(t, dsl.Action{
			Name: "Submit", Type: "POST", Target: "/submit", Headers: jsonHeaders, BodyTemplateEncoding: "json",
			Body: `{"label": "{{var.label}}", "n": {{var.missing}}}`,
		}, map[string]string{"label": "set"})
		probe := report.Events[1]
		if !slices.Equal(probe.Diagnosis.UnresolvedVariables, []string{"missing"}) {
			t.Fatalf("unresolved variables = %#v, want only [missing]", probe.Diagnosis.UnresolvedVariables)
		}
		if probe.Diagnosis.FollowsReason != ReplayFollowsReasonUnresolvedVariable || probe.Diagnosis.FollowsFailureEventID != report.Events[0].EventID {
			t.Fatalf("probe diagnosis = %#v, want an unresolved_variable link to the root", probe.Diagnosis)
		}
	})
}

func TestReplayUnresolvedVariablesSurviveDefaultRedaction(t *testing.T) {
	report := replayFollowsRootAndProbe(t, dsl.Action{
		Name: "Save", Type: "POST", Target: "/save",
		Headers: map[string]string{
			"Content-Type":  "application/json",
			"X-CSRF-Token":  "{{var.csrf}}",
			"Authorization": "Bearer {{var.access_token}}",
			"X-Set-Token":   "{{var.set_token}}",
		},
		Body:       `{"token":"{{var.body_token}}"}`,
		Assertions: []dsl.Assertion{{Type: "body_contains", Expected: "Saved"}},
	}, map[string]string{"set_token": "synthetic-set-token-value"})
	probe := report.Events[1]
	for name, value := range probe.Headers {
		if strings.Contains(value, "{{var.") {
			t.Fatalf("precondition: default redaction left header %s as %q", name, value)
		}
	}
	want := []string{"access_token", "body_token", "csrf"}
	if !slices.Equal(probe.Diagnosis.UnresolvedVariables, want) {
		t.Fatalf("unresolved variables = %#v, want %#v despite redaction", probe.Diagnosis.UnresolvedVariables, want)
	}
	if probe.Diagnosis.FollowsReason != ReplayFollowsReasonUnresolvedVariable || probe.Diagnosis.FollowsFailureEventID != report.Events[0].EventID {
		t.Fatalf("probe diagnosis = %#v, want an unresolved_variable link to the root", probe.Diagnosis)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-set-token-value") {
		t.Fatal("report carries a set token value")
	}
}

// Concurrent group children take their resolution-time record when the group
// is prepared; the scrubbed child events alone could not name a variable whose
// header default redaction removed.
func TestReplayConcurrentChildrenCarryResolutionTimeVariables(t *testing.T) {
	protocolName := "replay-follows-local-http"
	registerReplayFollowsHTTPProtocol.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not saved")
	}))
	defer server.Close()
	bodySaved := []dsl.Assertion{{Type: "body_contains", Value: "Saved"}}
	group := dsl.Action{Name: "Aura group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{
		{Name: "Save", Type: "POST", Target: "/save", Assertions: bodySaved, Headers: map[string]string{
			"X-CSRF-Token":  "{{var.csrf}}",
			"Authorization": "Bearer {{var.access_token}}",
			"X-Set-Token":   "{{var.set_token}}",
		}},
		{Name: "Load", Type: "GET", Target: "/load", Assertions: bodySaved, Headers: map[string]string{"X-Set-Token": "{{var.set_token}}"}},
	}}}
	script := &dsl.Script{
		Name: "Root then concurrent group", Protocol: protocolName,
		Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: []dsl.Action{
			{Name: "Login", Type: "GET", Target: "/login", Extractors: []dsl.Extractor{{Name: "x", Source: "body", Type: "regex", Expression: `cshc=([a-z0-9]+)`, OnMiss: "fail"}}},
			group,
		},
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{ReplaySessionID: "concurrent-variables-session", IgnoreThinkTime: true, Variables: map[string]string{"set_token": "synthetic-set-token-value"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 3 || report.Events[0].Success || report.Events[0].ActionName != "Login" {
		t.Fatalf("events = %#v (%s), want the failed root and two children", report.Events, report.Error)
	}
	root := report.Events[0]
	children := map[string]ReplayActionEvent{}
	for _, event := range report.Events[1:] {
		children[event.ActionName] = event
	}
	save, load := children["Save"], children["Load"]
	if save.Success || save.Diagnosis == nil || load.Success || load.Diagnosis == nil {
		t.Fatalf("children = %#v, want two failed, diagnosed children", children)
	}
	for name, value := range save.Headers {
		if strings.Contains(value, "{{var.") {
			t.Fatalf("precondition: default redaction left header %s as %q", name, value)
		}
	}
	if want := []string{"access_token", "csrf"}; !slices.Equal(save.Diagnosis.UnresolvedVariables, want) {
		t.Fatalf("Save unresolved variables = %#v, want %#v from the resolution-time record", save.Diagnosis.UnresolvedVariables, want)
	}
	if save.Diagnosis.FollowsReason != ReplayFollowsReasonUnresolvedVariable || save.Diagnosis.FollowsFailureEventID != root.EventID {
		t.Fatalf("Save diagnosis = %#v, want an unresolved_variable link to the root", save.Diagnosis)
	}
	if got := load.Diagnosis.UnresolvedVariables; len(got) != 0 {
		t.Fatalf("Load unresolved variables = %#v, want none: set_token is set", got)
	}
	if load.Diagnosis.FollowsFailureEventID != "" || load.Diagnosis.FollowsReason != "" {
		t.Fatalf("Load diagnosis = %#v, want no link without a session signal or unset variable", load.Diagnosis)
	}
	if got := report.DiagnosisSummary.FollowingFailureCount; got != 1 {
		t.Fatalf("following count = %d, want 1", got)
	}
}

func TestReplayConcurrentChildWithWithheldEvidencePublishesNoVariableNames(t *testing.T) {
	protocolName := "replay-follows-local-http"
	registerReplayFollowsHTTPProtocol.Do(func() { protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} }) })
	value := strings.Repeat("x", protocol.MaxExtractionValueBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/value" {
			json.NewEncoder(w).Encode(map[string]string{"value": value})
			return
		}
		fmt.Fprint(w, "not saved")
	}))
	defer server.Close()
	// The siblings' combined outputs exceed the aggregate extraction bound, so
	// the join withholds every child's public evidence
	// (publicEvidenceUnavailable).
	requests := []dsl.Action{{Name: "Save", Type: "POST", Target: "/save", Assertions: []dsl.Assertion{{Type: "body_contains", Value: "Saved"}}, Headers: map[string]string{
		"X-CSRF-Token":  "{{var.csrf}}",
		"Authorization": "Bearer {{var.access_token}}",
	}}}
	for i := 1; i <= 5; i++ {
		requests = append(requests, dsl.Action{Name: fmt.Sprintf("Leaf %d", i), Type: "GET", Target: "/value", Extractors: []dsl.Extractor{{Name: fmt.Sprintf("output_%d", i), Source: "body", Type: "jsonpath", Expression: "$.value", OnMiss: "fail"}}})
	}
	group := dsl.Action{Name: "Aura group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": requests}}
	script := &dsl.Script{
		Name: "Root then withheld concurrent group", Protocol: protocolName,
		Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: []dsl.Action{
			{Name: "Login", Type: "GET", Target: "/login", Extractors: []dsl.Extractor{{Name: "x", Source: "body", Type: "regex", Expression: `cshc=([a-z0-9]+)`, OnMiss: "fail"}}},
			group,
		},
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{ReplaySessionID: "withheld-variables-session", IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 7 || report.Events[0].Success || report.Events[0].ActionName != "Login" {
		t.Fatalf("events = %#v (%s), want the failed root and six withheld children", report.Events, report.Error)
	}
	var failed []ReplayActionEvent
	for _, event := range report.Events[1:] {
		if event.ActionName != "Concurrent request" || event.Target != "" || len(event.Headers) != 0 {
			t.Fatalf("precondition: child %#v kept public request evidence", event)
		}
		if !event.Success {
			failed = append(failed, event)
		}
	}
	if len(failed) != 1 || failed[0].Diagnosis == nil || failed[0].Method != "POST" {
		t.Fatalf("children = %#v, want one failed, diagnosed Save child", report.Events[1:])
	}
	save := failed[0]
	if got := save.Diagnosis.UnresolvedVariables; len(got) != 0 {
		t.Fatalf("withheld child unresolved variables = %#v, want none published", got)
	}
	if save.Diagnosis.FollowsReason == ReplayFollowsReasonUnresolvedVariable {
		t.Fatalf("withheld child diagnosis = %#v, linked by an unpublished variable name", save.Diagnosis)
	}
	retained := BoundReplayActionEventForRetention(save, false)
	if retained.Diagnosis != nil && len(retained.Diagnosis.UnresolvedVariables) != 0 {
		t.Fatalf("retained withheld child unresolved variables = %#v, want none", retained.Diagnosis.UnresolvedVariables)
	}
}

func TestResolveActionRecordsOnlyRequestSurfaceVariables(t *testing.T) {
	vu := &VUser{Script: &dsl.Script{}, Variables: map[string]string{"set": "value"}}
	vu.resolveAction(dsl.Action{
		Type: "POST", Target: "/t/{{var.target}}/{{var.set}}", Selector: "{{var.selector}}", Value: "{{var.value}}",
		Headers: map[string]string{"X-A": "{{ var.header }}"}, Body: "a={{var.body}}&b={{var.body}}&c={{param.p}}",
		Params:     map[string]any{"p": "{{var.param}}"},
		Metadata:   map[string]any{"m": "{{var.metadata}}"},
		Extractors: []dsl.Extractor{{Name: "e", Source: "body", Type: "regex", Expression: "{{var.extractor}}"}},
		Assertions: []dsl.Assertion{{Type: "body_contains", Expected: "{{var.assertion}}"}},
	})
	got := vu.takeReplayRequestVariables()
	if want := []string{"body", "header", "param", "selector", "target", "value"}; !got.known || !slices.Equal(got.names, want) {
		t.Fatalf("recorded variables = %#v, want %#v", got, want)
	}
	if vu.recordUnresolvedVariables || vu.unresolvedVariableRefs != nil {
		t.Fatal("recording stayed on or the record was not cleared")
	}
	vu.interpolate("{{var.outside}}")
	if vu.unresolvedVariableRefs != nil {
		t.Fatalf("interpolation outside resolveAction was recorded: %#v", vu.unresolvedVariableRefs)
	}

	disabled := false
	defaultsScript := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
		DefaultHeaders: map[string]string{
			"X-Ctx":          "{{var.ctx}}",
			"X-Set":          "{{var.set}}",
			"X-Shadowed-Map": "{{var.map_shadowed}}",
			"X-Listed":       "{{var.list_shadows_map}}",
		},
		DefaultHeaderList: dsl.HeaderList{
			{Name: "X-Listed", Value: "{{var.list}}"},
			{Name: "X-List-Shadowed", Value: "{{var.list_shadowed}}"},
			{Name: "X-Off", Value: "{{var.disabled}}", Enabled: &disabled},
		},
	}}}
	t.Run("default headers an HTTP request sends unshadowed", func(t *testing.T) {
		vu := &VUser{Script: defaultsScript, Variables: map[string]string{"set": "value"}}
		resolved := vu.resolveAction(dsl.Action{
			Type: "GET", Target: "/t",
			Headers:    map[string]string{"x-shadowed-map": "static"},
			HeaderList: dsl.HeaderList{{Name: "x-list-shadowed", Value: "static"}},
		})
		got := vu.takeReplayRequestVariables()
		if want := []string{"ctx", "list"}; !got.known || !slices.Equal(got.names, want) {
			t.Fatalf("recorded variables = %#v, want %#v: shadowed and disabled defaults are not sent", got, want)
		}
		if !resolved.ResolvedDefaultHeaders || resolved.DefaultHeaders["X-Set"] != "value" || resolved.DefaultHeaders["X-Ctx"] != "{{var.ctx}}" || len(resolved.DefaultHeaders) != 4 || len(resolved.DefaultHeaderList) != 3 {
			t.Fatalf("resolved defaults = %#v / %#v, want every default entry interpolated", resolved.DefaultHeaders, resolved.DefaultHeaderList)
		}
	})
	t.Run("an action header overrides every default variable", func(t *testing.T) {
		vu := &VUser{Script: defaultsScript}
		vu.resolveAction(dsl.Action{Type: "POST", Target: "/t", Headers: map[string]string{
			"X-Ctx": "static", "X-Set": "static", "X-Shadowed-Map": "static", "X-Listed": "static", "X-List-Shadowed": "static",
		}})
		if got := vu.takeReplayRequestVariables(); !got.known || len(got.names) != 0 {
			t.Fatalf("recorded variables = %#v, want none: no default header is sent", got)
		}
	})
	for _, actionType := range []string{"db_query", "ws_send", "http_sequence", "rendezvous"} {
		t.Run("not sent with "+actionType, func(t *testing.T) {
			vu := &VUser{Script: defaultsScript}
			vu.resolveAction(dsl.Action{Type: actionType, Target: "/t/{{var.target}}"})
			if got := vu.takeReplayRequestVariables(); !got.known || !slices.Equal(got.names, []string{"target"}) {
				t.Fatalf("recorded variables = %#v, want only [target]: a %s does not send HTTP default headers", got, actionType)
			}
		})
	}
}
