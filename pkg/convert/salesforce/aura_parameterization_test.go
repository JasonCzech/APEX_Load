package salesforce

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

func encodeAuraParameterFixture(t *testing.T, params map[string]any) string {
	t.Helper()
	message, err := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-action", "descriptor": "synthetic://Action", "params": params}}})
	if err != nil {
		t.Fatal(err)
	}
	return url.Values{"message": {string(message)}}.Encode()
}

func nestedAuraParameterFixture(t *testing.T) string {
	t.Helper()
	inner, _ := json.Marshal(map[string]any{"accountId": "5003000000D8cuIAAR"})
	payload, _ := json.Marshal(map[string]any{
		"records": []any{map[string]any{"recordId": "001D000000IqhSLIAZ"}, map[string]any{"recordId": "001D000000IqhSLIAZ"}},
		"payload": string(inner), "note": "unchanged business note", "externalId": 123,
	})
	return encodeAuraParameterFixture(t, map[string]any{"input": string(payload), "recordId": "001D000000IqhSLIAZ", "password": map[string]any{"recordId": "003D000000Zx1AAAS"}})
}

func TestAuraParameterInspectionKeepsDistinctNestedOccurrencesAndStablePaths(t *testing.T) {
	body := nestedAuraParameterFixture(t)
	refs := []actionRef{{Kind: "aura", Sequence: 7, ActionPath: "actions[0].control.steps[1]", Action: &dsl.Action{Body: body}}}
	got, warnings := auraMessageParameterizationsChecked(refs, nil)
	if len(got) != 4 || len(warnings) != 1 || !strings.Contains(warnings[0], "non-string") {
		t.Fatalf("candidates=%d warnings=%v", len(got), warnings)
	}
	paths := map[string]bool{}
	names := map[string]bool{}
	columns := map[string]bool{}
	repeated, nested := 0, 0
	for _, candidate := range got {
		if candidate.ActionSeq != 7 || candidate.ActionPath != refs[0].ActionPath || candidate.DecodedFieldPath == "" || paths[candidate.DecodedFieldPath] || names[candidate.Name] || columns[candidate.Column] {
			t.Fatal("exact candidate paths or unique names lost")
		}
		paths[candidate.DecodedFieldPath] = true
		names[candidate.Name] = true
		columns[candidate.Column] = true
		if candidate.SampleValue == "001D000000IqhSLIAZ" {
			repeated++
		}
		if candidate.Expression == "" {
			nested++
			if len(candidate.EncodingLayers) < 2 {
				t.Fatal("nested decoding layers omitted")
			}
		}
		if candidate.SampleValue == "003D000000Zx1AAAS" {
			t.Fatal("credential parent yielded a business candidate")
		}
	}
	if repeated != 3 || nested != 3 {
		t.Fatalf("distinct repeated=%d nested=%d", repeated, nested)
	}
	for run := 0; run < 10; run++ {
		again, againWarnings := auraMessageParameterizationsChecked(refs, nil)
		if !reflect.DeepEqual(got, again) || !reflect.DeepEqual(warnings, againWarnings) {
			t.Fatal("candidate ordering changed across inspections")
		}
	}
	correlated := []correlate.Correlation{{Value: "001D000000IqhSLIAZ"}}
	remaining, _ := auraMessageParameterizationsChecked(refs, correlated)
	if len(remaining) != 1 || remaining[0].ParamKey != "accountId" {
		t.Fatal("correlated nested occurrences were offered as test data")
	}
}

func TestAuraParameterInspectionSupportsBase64AndReportsUnsupportedOrAmbiguousBodies(t *testing.T) {
	body := nestedAuraParameterFixture(t)
	refs := []actionRef{{Kind: "aura", Action: &dsl.Action{Body: base64.StdEncoding.EncodeToString([]byte(body)), BodyEncoding: "base64"}}}
	candidates, _ := auraMessageParameterizationsChecked(refs, nil)
	if len(candidates) != 4 {
		t.Fatal("base64 wire-body candidates omitted")
	}
	for _, candidate := range candidates {
		if !containsString(candidate.EncodingLayers, "base64") {
			t.Fatal("base64 decoding layer omitted")
		}
	}
	for _, tc := range []struct{ name, body, encoding string }{
		{"duplicate message", body + "&message=%7B%7D", ""},
		{"duplicate JSON member", encodeAuraParameterFixture(t, map[string]any{"input": `{"recordId":"001D000000IqhSLIAZ","recordId":"5003000000D8cuIAAR"}`}), ""},
		{"unpaired surrogate", encodeAuraParameterFixture(t, map[string]any{"input": `{"recordId":"\ud800"}`}), ""},
		{"unsupported encoding", body, "windows-1252"},
		{"invalid base64", "invalid%payload", "base64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings := auraMessageParameterizationsChecked([]actionRef{{Kind: "aura", Action: &dsl.Action{Body: tc.body, BodyEncoding: tc.encoding}}}, nil)
			if len(got) != 0 || len(warnings) != 1 || strings.Contains(warnings[0], "001D000000IqhSLIAZ") {
				t.Fatal("ambiguous inspection produced partial candidates or unsafe diagnostics")
			}
		})
	}
}

func TestAuraParameterReviewDetailsAndInspectionWorkAreBoundedExplicitly(t *testing.T) {
	records := make([]any, 2000)
	for i := range records {
		records[i] = map[string]any{"recordId": "001D000000IqhSLIAZ"}
	}
	params := map[string]any{"records": records}
	body := encodeAuraParameterFixture(t, params)
	got, warnings := auraMessageParameterizationsChecked([]actionRef{{Kind: "aura", Action: &dsl.Action{Body: body}}}, nil)
	if len(got) == 0 || len(got) >= 2000 || len(warnings) != 1 || !strings.Contains(warnings[0], "omitted") {
		t.Fatalf("detail bound missing: candidates=%d warnings=%v", len(got), warnings)
	}
	bytes := 0
	for _, candidate := range got {
		encoded, _ := json.Marshal(candidate)
		bytes += len(encoded)
	}
	if bytes > maxAuraParamCandidateBytes {
		t.Fatal("retained candidate detail exceeded its serialized byte budget")
	}
	bounded := encodeAuraParameterFixture(t, map[string]any{"recordId": "001D000000IqhSLIAZ", "padding": strings.Repeat("x", 512<<10)})
	refs := make([]actionRef, 40)
	for i := range refs {
		refs[i] = actionRef{Kind: "aura", Action: &dsl.Action{Body: bounded}}
	}
	got, warnings = auraMessageParameterizationsChecked(refs, nil)
	if len(got) == 0 || len(got) >= len(refs) || len(warnings) != 1 || !strings.Contains(warnings[0], "aggregate work limit") {
		t.Fatalf("aggregate inspection bound not disclosed: retained=%d warnings=%v", len(got), warnings)
	}
	oversized := strings.Repeat("x", aurapayload.MaxBodyBytes+1)
	got, warnings = auraMessageParameterizationsChecked([]actionRef{{Kind: "aura", Action: &dsl.Action{Body: oversized}}}, nil)
	if len(got) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "could not inspect") {
		t.Fatal("per-body byte rejection was not disclosed separately")
	}

}
