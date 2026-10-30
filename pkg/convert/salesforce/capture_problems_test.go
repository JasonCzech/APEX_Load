package salesforce

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

const capturedAuraToken = "eyJub25jZSI6InJlY29yZGVkIn0"

// capturedAuraHAR records two Aura POSTs. The second recorded a Content-Length
// but, like a large body in a Chrome export, has no postData. requestHeaders
// and responseHeaders are added to the first entry.
func capturedAuraHAR(t *testing.T, requestHeaders, responseHeaders []any) string {
	t.Helper()
	body := "message=" + url.QueryEscape(syntheticAuraRequest) + "&aura.token=" + capturedAuraToken
	response := `{"actions":[{"id":"first;a","state":"SUCCESS"},{"id":"second;a","state":"SUCCESS"}]}`
	form := map[string]any{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}
	if responseHeaders == nil {
		responseHeaders = []any{}
	}
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{
			"request":  map[string]any{"method": "POST", "url": "https://example.lightning.force.com/aura?r=1", "headers": append([]any{form}, requestHeaders...), "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": body}},
			"response": map[string]any{"status": 200, "headers": responseHeaders, "content": map[string]any{"mimeType": "application/json", "text": response}},
		},
		map[string]any{
			"request":  map[string]any{"method": "POST", "url": "https://example.lightning.force.com/aura?r=2", "headers": []any{form, map[string]any{"name": "Content-Length", "value": "93994"}}},
			"response": map[string]any{"status": 200, "content": map[string]any{"mimeType": "application/json", "text": response}},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func analyzeCapturedAura(t *testing.T, content string) *Review {
	t.Helper()
	converted, err := sample.Convert(sample.ImportRequest{Format: "har", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	review, err := Analyze(converted.Script, AnalyzeOptions{BuildNative: true, HARData: []byte(content)})
	if err != nil {
		t.Fatal(err)
	}
	return review
}

// An Aura request whose body the export left out is still an invalid request
// that gates native replay, but its issue and blocker name the missing body
// and the HAR entry instead of a malformed envelope.
func TestAuraRequestBodyNotCapturedNamesTheEntry(t *testing.T) {
	review := analyzeCapturedAura(t, capturedAuraHAR(t, nil, nil))
	outcomes := review.AuraOutcomes
	if outcomes == nil || outcomes.InvalidRequests != 1 || outcomes.IssueCounts[AuraRequestBodyNotCaptured] != 1 || outcomes.IssueCounts[aura.MissingMessage] != 0 {
		t.Fatalf("outcomes = %#v", outcomes)
	}
	var batch *AuraOutcomeBatch
	for index := range outcomes.Batches {
		if outcomes.Batches[index].HAREntryID == 2 {
			batch = &outcomes.Batches[index]
		}
	}
	if batch == nil || batch.RequestValid || len(batch.RequestIssues) != 1 || batch.RequestIssues[0].Code != AuraRequestBodyNotCaptured {
		t.Fatalf("batch for HAR entry 2 = %#v", batch)
	}
	if count, entries := outcomes.RequestBodyNotCaptured(); count != 1 || len(entries) != 1 || entries[0] != 2 {
		t.Fatalf("not captured = %d %v", count, entries)
	}
	var found bool
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "could not be validated") {
			t.Fatalf("generic envelope blocker for a missing body: %q", blocker)
		}
		if strings.HasPrefix(blocker, "1 Aura request(s) (HAR entry 2) have no captured request body") && strings.Contains(blocker, "Re-capture or remove those steps; native replay is gated") {
			found = true
		}
	}
	if !found {
		t.Fatalf("blockers = %q", review.Blockers)
	}
	// The native gate on the step is unchanged.
	gated := 0
	for _, action := range review.NativePreview.Script.Actions {
		if codes, _ := action.Metadata["execution_blockers"].([]string); len(codes) > 0 {
			gated++
		}
	}
	if gated != 1 {
		t.Fatalf("%d native action(s) gated, want 1", gated)
	}
}

// Invalid envelopes of another kind keep the generic blocker; the missing
// bodies are named after it, in the same single blocker.
func TestAuraInvalidRequestsBlockerMixesCauses(t *testing.T) {
	outcomes := &AuraOutcomeReview{InvalidRequests: 3, bodyNotCaptured: 2, bodyNotCapturedEntries: []int{116, 215}}
	got := auraInvalidRequestsBlocker(outcomes)
	if !strings.HasPrefix(got, "Imported Aura requests could not be validated;") || !strings.Contains(got, "2 Aura request(s) (HAR entries 116, 215) have no captured request body") {
		t.Fatalf("blocker = %q", got)
	}
	if got := auraInvalidRequestsBlocker(&AuraOutcomeReview{InvalidRequests: 1}); got != "Imported Aura requests could not be validated; native replay is gated until those request envelopes are repaired." {
		t.Fatalf("generic blocker = %q", got)
	}
}

// The stale aura.token warning names a sanitized export as the likely cause
// only when the capture has no cookie or Authorization evidence at all, the
// same evidence the capture-level sanitized-export warning requires.
func TestAuraTokenWarningNamesSanitizedExport(t *testing.T) {
	tests := []struct {
		name            string
		requestHeaders  []any
		responseHeaders []any
		sanitized       bool
	}{
		{name: "no cookie or credential evidence", sanitized: true},
		{name: "Set-Cookie header", responseHeaders: []any{map[string]any{"name": "Set-Cookie", "value": "BrowserId=x; Path=/"}}},
		{name: "Authorization header", requestHeaders: []any{map[string]any{"name": "Authorization", "value": "Bearer x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			review := analyzeCapturedAura(t, capturedAuraHAR(t, tt.requestHeaders, tt.responseHeaders))
			var warning string
			for _, candidate := range review.Warnings {
				if strings.Contains(candidate, "send the recorded aura.token") {
					warning = candidate
				}
			}
			if warning == "" {
				t.Fatalf("no aura.token warning in %q", review.Warnings)
			}
			sanitized := strings.Contains(warning, "looks like a sanitized export") && strings.Contains(warning, `"Export HAR (with sensitive data)"`)
			if sanitized != tt.sanitized {
				t.Fatalf("sanitized-export cause=%v, want %v, in %q", sanitized, tt.sanitized, warning)
			}
			if strings.Contains(warning, capturedAuraToken) {
				t.Fatal("warning disclosed the recorded token")
			}
		})
	}
}

// A cookie-free capture that does contain a producer for aura.token needs its
// correlation reviewed, not a re-export, so the producer variant of the warning
// never names a sanitized export.
func TestAuraTokenProducerWarningOmitsSanitizedExport(t *testing.T) {
	token := "eyJub25jZSI6InJlY29yZGVkIn0.eyJzdWIiOiJ4In0.c2lnbmF0dXJlMTIzNDU2"
	body := "message=" + url.QueryEscape(syntheticAuraRequest) + "&aura.context=" + url.QueryEscape(`{"mode":"PROD","fwuid":"abcdef123456","app":"one:one"}`) + "&aura.token=" + token
	response := `{"actions":[{"id":"first;a","state":"SUCCESS"},{"id":"second;a","state":"SUCCESS"}]}`
	form := map[string]any{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}
	auraEntry := func(r string) any {
		return map[string]any{
			"request":  map[string]any{"method": "POST", "url": "https://example.lightning.force.com/aura?r=" + r, "headers": []any{form}, "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": body}},
			"response": map[string]any{"status": 200, "headers": []any{}, "content": map[string]any{"mimeType": "application/json", "text": response}},
		}
	}
	producer := map[string]any{
		"request":  map[string]any{"method": "GET", "url": "https://example.lightning.force.com/one/one.app", "headers": []any{}},
		"response": map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Type", "value": "application/json"}}, "content": map[string]any{"mimeType": "application/json", "text": `{"auraToken":"` + token + `","token":"` + token + `"}`}},
	}
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{auraEntry("1"), producer, auraEntry("2")}}})
	if err != nil {
		t.Fatal(err)
	}
	review := analyzeCapturedAura(t, string(data))
	var warning string
	for _, candidate := range review.Warnings {
		if strings.Contains(candidate, "send the recorded aura.token") {
			warning = candidate
		}
	}
	if !strings.Contains(warning, "contains a producer") {
		t.Fatalf("no producer variant of the aura.token warning in %d warning(s)", len(review.Warnings))
	}
	if strings.Contains(warning, "sanitized export") || strings.Contains(warning, "Export HAR (with sensitive data)") {
		t.Fatal("producer variant of the aura.token warning names a sanitized export")
	}
	if strings.Contains(warning, token) {
		t.Fatal("warning disclosed the recorded token")
	}
}

// The blocker names at most maxAuraBodyNotCapturedEntryIDs HAR entries and
// counts the rest.
func TestAuraInvalidRequestsBlockerCapsEntryIDs(t *testing.T) {
	entries := make([]int, maxAuraBodyNotCapturedEntryIDs+2)
	for index := range entries {
		entries[index] = 100 + index
	}
	got := auraInvalidRequestsBlocker(&AuraOutcomeReview{InvalidRequests: len(entries), bodyNotCaptured: len(entries), bodyNotCapturedEntries: entries})
	if !strings.HasPrefix(got, "12 Aura request(s) (HAR entries 100, 101, 102, 103, 104, 105, 106, 107, 108, 109 and 2 more) have no captured request body") {
		t.Fatalf("blocker = %q", got)
	}
	got = auraInvalidRequestsBlocker(&AuraOutcomeReview{InvalidRequests: 10, bodyNotCaptured: 10, bodyNotCapturedEntries: entries[:10]})
	if strings.Contains(got, "more") || !strings.Contains(got, "(HAR entries 100, 101, 102, 103, 104, 105, 106, 107, 108, 109)") {
		t.Fatalf("blocker at the cap = %q", got)
	}
	// Requests not matched to a HAR entry are counted beside the named ones.
	got = auraInvalidRequestsBlocker(&AuraOutcomeReview{InvalidRequests: 3, bodyNotCaptured: 3, bodyNotCapturedEntries: []int{116}})
	if !strings.HasPrefix(got, "3 Aura request(s) (HAR entry 116 and 2 more) have no captured request body") {
		t.Fatalf("blocker with unmatched requests = %q", got)
	}
}

// A script reloaded from JSON carries the recorded Content-Length as a
// float64, and a cleared gate drops the execution_blockers code; the recorded
// length alone still names the uncaptured body, matched or not.
func TestAuraRequestBodyNotCapturedSurvivesReloadWithoutGate(t *testing.T) {
	content := capturedAuraHAR(t, nil, nil)
	converted, err := sample.Convert(sample.ImportRequest{Format: "har", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(converted.Script)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded dsl.Script
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	reloadedLengths := 0
	for index := range reloaded.Actions {
		metadata := reloaded.Actions[index].Metadata
		delete(metadata, "execution_blockers")
		if length, ok := metadata["har.request.recorded_content_length"].(float64); ok && length > 0 {
			reloadedLengths++
		}
	}
	if reloadedLengths != 1 {
		t.Fatalf("%d reloaded action(s) with a float64 recorded length, want 1", reloadedLengths)
	}
	document, err := sample.ParseHARDocument(content)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := harDocumentEntries(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		entries []harEntry
		ids     []int
	}{
		{name: "unmatched"},
		{name: "matched", entries: entries, ids: []int{2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs := flattenRequestActions(&reloaded)
			for index, harID := range matchHAR(refs, tc.entries) {
				refs[index].HARID = harID
			}
			outcomes := inspectAuraOutcomes(refs, tc.entries)
			if outcomes == nil || outcomes.InvalidRequests != 1 || outcomes.IssueCounts[AuraRequestBodyNotCaptured] != 1 || outcomes.IssueCounts[aura.MissingMessage] != 0 {
				t.Fatalf("outcomes = %#v", outcomes)
			}
			if count, ids := outcomes.RequestBodyNotCaptured(); count != 1 || !slices.Equal(ids, tc.ids) {
				t.Fatalf("not captured = %d %v, want 1 %v", count, ids, tc.ids)
			}
		})
	}
}
