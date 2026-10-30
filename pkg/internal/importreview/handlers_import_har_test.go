package importreview

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
)

func unhandledPairHAR(t *testing.T, pageID, token string) []byte {
	t.Helper()
	entry := map[string]any{
		"startedDateTime": "2026-01-01T00:00:00Z",
		"time":            10,
		"request": map[string]any{
			"method":   "POST",
			"url":      "https://app.test/api/save",
			"headers":  []map[string]string{{"name": "X-Page-Id", "value": pageID}, {"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
			"postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "token=" + token},
		},
		"response": map[string]any{"status": 200, "headers": []map[string]string{}, "content": map[string]any{"mimeType": "text/plain", "text": "ok"}},
	}
	raw, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{entry}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// An automatic parameterization suggestion replaces its value when the draft
// is saved, so the value it covers is not unhandled; the others are, and the
// review says so in the pair diff section and in a warning.
func TestHARPairUnhandledInputsAreJudgedOnTheFinalScript(t *testing.T) {
	baseline := unhandledPairHAR(t, "aaaa1111bbbb2222", "Zq9vT0kenAAA")
	opts := har.ConvertOptions{ScriptName: "pair"}
	result, err := har.Convert(baseline, opts)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := har.DiffWithBaseline(result, unhandledPairHAR(t, "cccc3333dddd4444", "Pp8uU1lfoBBB"), opts)
	if err != nil {
		t.Fatal(err)
	}
	parameterization := &paramize.Result{Suggestions: []paramize.Suggestion{{
		AutoApply:    true,
		LiteralValue: "aaaa1111bbbb2222",
		Locations:    []paramize.Location{{SearchText: "aaaa1111bbbb2222"}},
	}}}
	ResolveHARPairUnhandledInputs(diff, result.Script, parameterization)
	if len(diff.UnhandledInputs) != 1 || diff.UnhandledInputs[0].Field != "form:token" {
		t.Fatalf("unhandled inputs = %#v, want only form:token (the page id is auto-parameterized)", diff.UnhandledInputs)
	}

	warning := HARPairUnhandledInputsWarning(diff)
	if !strings.Contains(warning, "1 request field(s)") || !strings.Contains(warning, "1 request(s)") {
		t.Fatalf("warning = %q", warning)
	}
	sections := AppendHARPairUnhandledInputs(HarImportReviewSections(&har.ConvertResult{Diff: diff}), diff)
	var found bool
	for _, section := range sections {
		if section.Key != "har_pair_diff" {
			continue
		}
		found = true
		if section.Severity != "warning" || !strings.Contains(section.Summary, "still sent as recorded") {
			t.Fatalf("section = %#v, want a warning that names the unhandled inputs", section)
		}
		if last := section.Items[len(section.Items)-1]; !strings.HasPrefix(last, "form:token: still sent as recorded in 1 request(s)") {
			t.Fatalf("items = %q, want form:token listed", section.Items)
		}
	}
	if !found {
		t.Fatal("no har_pair_diff section")
	}
}

func TestHARPairUnhandledInputsLeaveACleanReviewAlone(t *testing.T) {
	diff := &har.DiffReport{Compared: true}
	if warning := HARPairUnhandledInputsWarning(diff); warning != "" {
		t.Fatalf("warning = %q, want none", warning)
	}
	sections := HarImportReviewSections(&har.ConvertResult{Diff: diff})
	sections = AppendHARPairUnhandledInputs(sections, diff)
	for _, section := range sections {
		if section.Key == "har_pair_diff" && section.Severity != "info" {
			t.Fatalf("section severity = %q, want info", section.Severity)
		}
	}
}

// Client counters (Aura action ids, for example) replay harmlessly, so they are
// listed without a warning or a warning-level section.
func TestHARPairClientCountersAloneDoNotWarn(t *testing.T) {
	diff := &har.DiffReport{Compared: true, UnhandledInputs: []har.DiffRequestInput{{
		Field: "form:message.actions[].id", Kind: har.DiffInputCounter, Requests: 47, DistinctValues: 47, Reason: "a client counter",
	}}}
	if warning := HARPairUnhandledInputsWarning(diff); warning != "" {
		t.Fatalf("warning = %q, want none for counters", warning)
	}
	sections := AppendHARPairUnhandledInputs(HarImportReviewSections(&har.ConvertResult{Diff: diff}), diff)
	for _, section := range sections {
		if section.Key != "har_pair_diff" {
			continue
		}
		if section.Severity != "info" || !strings.HasPrefix(section.Items[len(section.Items)-1], "form:message.actions[].id") {
			t.Fatalf("section = %#v, want the counter listed at info severity", section)
		}
	}
}
