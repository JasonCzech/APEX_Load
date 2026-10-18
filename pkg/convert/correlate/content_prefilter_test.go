package correlate

import (
	"reflect"
	"strings"
	"testing"
)

func TestContentPrefiltersRetainCandidatesAcrossMIMEHints(t *testing.T) {
	cases := []struct{ name, body, value string }{
		{"json", `{"token":"token-abcdef"}`, "token-abcdef"},
		{"embedded json", `callback({"token":"token-abcdef"});`, "token-abcdef"},
		{"HTML", `<INPUT name="csrf" value="token-abcdef">`, "token-abcdef"},
		{"ASP.NET", `<input name="__VIEWSTATE" value="state-abcdef">`, "state-abcdef"},
		{"XML element", `<s:Token>token-abcdef</s:Token>`, "token-abcdef"},
		{"XML attribute in plain text", `token="token-abcdef"`, "token-abcdef"},
		{"form", `aura.token=token-abcdef`, "token-abcdef"},
		{"Salesforce JSON", `{"FWUID":"framework-abcdef"}`, "framework-abcdef"},
		{"Salesforce Unicode fold", `{"cſhc":"handoff-abcdef"}`, "handoff-abcdef"},
		{"Salesforce nested", `{"nested":{"nextRecordsUrl":"/services/query/next"}}`, "/services/query/next"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected, _, err := candidatesCheckedWithTruncation(tc.body, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, candidate := range expected {
				found = found || candidate.Value == tc.value
			}
			if !found {
				t.Fatalf("lost candidate %q in %#v", tc.value, expected)
			}
			for _, mime := range []string{"application/json", "text/html", "application/xml", "application/octet-stream", ""} {
				got, _, err := candidatesCheckedWithTruncation(tc.body, map[string]string{"Content-Type": mime})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("MIME %q changed content evidence\ngot=%#v\nwant=%#v", mime, got, expected)
				}
			}
		})
	}
}

func TestSalesforceLiteralPrefiltersMatchRegexpSimpleFold(t *testing.T) {
	for _, body := range []string{`{"cſhc":"handoff-abcdef"}`, `{"_CONFIRMATIONTOKEN":"confirm-abcdef"}`, `{"pageſcopeId":"scope-abcdef"}`} {
		if got := salesforceRegexCandidates(body); len(got) == 0 {
			t.Fatalf("simple-fold match skipped: %s", body)
		}
	}
}

func TestXMLPrefiltersRetainOrdinalAndTruncation(t *testing.T) {
	body := `<Token>      </Token><Token>token-abcdef</Token>`
	got, truncated := xmlCandidatesWithTruncation(body)
	if truncated || len(got) != 1 || !strings.Contains(got[0].Expression, "2") {
		t.Fatalf("XML ordinal changed: %#v, truncated=%v", got, truncated)
	}
	// A missing close marker does not suppress attribute candidates.
	got, _ = xmlCandidatesWithTruncation(`<open token="token-abcdef"`)
	if len(got) != 1 || got[0].Value != "token-abcdef" {
		t.Fatalf("attribute fallback lost: %#v", got)
	}
}

func BenchmarkContentCandidatePrefilters(b *testing.B) {
	cases := map[string]string{
		"plain":  strings.Repeat("ordinary response payload ", 1<<14),
		"JSON":   `{"records":"` + strings.Repeat("ordinary response payload ", 1<<14) + `"}`,
		"script": strings.Repeat(`function ordinary(){return "payload";} `, 1<<14),
	}
	for name, body := range cases {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				if _, _, err := candidatesCheckedWithTruncation(body, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
