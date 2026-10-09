package enterprisearchive

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestWebRegFindTextChecks(t *testing.T) {
	tests := []struct {
		name   string
		call   string
		want   dsl.Assertion
		pass   []string
		fail   []string
		reason string
	}{
		{name: "plain body text keeps body_contains", call: `web_reg_find("Text=Welcome", LAST);`, want: dsl.Assertion{Type: "body_contains", Value: "Welcome"}},
		{name: "fail when found", call: `web_reg_find("Text=Error", "Fail=Found", LAST);`, want: dsl.Assertion{Type: "body_not_contains", Value: "Error"}},
		{name: "headers", call: `web_reg_find("Text=Cache-Control: no-store", "Search=Headers", LAST);`, want: dsl.Assertion{Type: "text_contains", Value: "Cache-Control: no-store", Source: "headers"}},
		{name: "all", call: `web_reg_find("Text/IC=welcome", "Search=ALL", LAST);`, want: dsl.Assertion{Type: "text_contains", Value: "welcome", Source: "all", CaseInsensitive: true}},
		{name: "digits", call: `web_reg_find("Text/DIG=Order ####", LAST);`, want: dsl.Assertion{Type: "text_matches", Value: `Order [0-9][0-9][0-9][0-9]`}, pass: []string{"Order 1234 ok"}, fail: []string{"Order 12a4"}},
		{name: "alphanumerics", call: `web_reg_find("Text/ALNUMLC=id-^^.", LAST);`, want: dsl.Assertion{Type: "text_matches", Value: `id-[a-z0-9][a-z0-9]\.`}, pass: []string{"id-a9."}, fail: []string{"id-A9.", "id-a9x"}},
		{name: "binary", call: `web_reg_find("Text/BIN=ok\\x01", LAST);`, want: dsl.Assertion{Type: "text_matches", Value: `ok\x01`}, pass: []string{"xok\x01y"}, fail: []string{"ok1"}},
		{name: "regexp", call: `web_reg_find("Text/REGEXP=status=(ok|done)", LAST);`, want: dsl.Assertion{Type: "text_matches", Value: `status=(ok|done)`}, pass: []string{"status=done"}, fail: []string{"status=failed"}},
		{name: "prefix and suffix", call: `web_reg_find("TextPfx=<b>", "TextSfx=</b>", LAST);`, want: dsl.Assertion{Type: "text_matches", Value: `<b>(?s:.*?)</b>`}, pass: []string{"x<b>\nhello</b>"}, fail: []string{"<b>hello"}},
		{name: "parameters stay templates", call: `web_reg_find("Text/DIG=#{Code}", LAST);`, want: dsl.Assertion{Type: "text_matches", Value: `[0-9]{{var.code}}`}},
		{name: "non-RE2 regexp", call: `web_reg_find("Text/REGEXP=(?<=a)b", LAST);`, reason: "not RE2-compatible"},
		{name: "high binary bytes", call: `web_reg_find("Text/BIN=\\xff", LAST);`, reason: "above 0x7F"},
		{name: "no text", call: `web_reg_find("Search=Body", LAST);`, reason: "no Text, TextPfx or TextSfx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion, _, _, _, findings := convertFindAssertionWithFindings(parseSingleCall(t, tt.call))
			if tt.reason != "" {
				if assertion != nil || len(findings) != 1 || findings[0].Code != textCheckUnsupportedReasonCode || !strings.Contains(findings[0].Detail, tt.reason) {
					t.Fatalf("assertion = %+v, findings = %+v; want a refusal mentioning %q", assertion, findings, tt.reason)
				}
				return
			}
			if assertion == nil || *assertion != tt.want {
				t.Fatalf("assertion = %+v, want %+v", assertion, tt.want)
			}
			if len(tt.pass)+len(tt.fail) > 0 {
				pattern := regexp.MustCompile(assertion.Value)
				for _, text := range tt.pass {
					if !pattern.MatchString(text) {
						t.Errorf("%q must match %q", assertion.Value, text)
					}
				}
				for _, text := range tt.fail {
					if pattern.MatchString(text) {
						t.Errorf("%q must not match %q", assertion.Value, text)
					}
				}
			}
		})
	}
}

func TestWebRegFindSaveCountOverHeaders(t *testing.T) {
	_, extractor, _, _, _ := convertFindAssertionWithFindings(parseSingleCall(t, `web_reg_find("Text=Set-Cookie", "Search=Headers", "SaveCount=n", LAST);`))
	if extractor == nil || extractor.Source != "header" || extractor.Type != "regex_count" || extractor.Expression != "Set-Cookie" {
		t.Fatalf("extractor = %+v", extractor)
	}
}

// A parameter in a counted text keeps its template, and its value is
// matched literally, as LoadRunner substitutes it into the text.
func TestWebRegFindSaveCountKeepsParameters(t *testing.T) {
	for _, tt := range []struct{ call, body string }{
		{`web_reg_find("Text=Order {OrderId}.", "SaveCount=n", LAST);`, "Order a+b. Order a+b. Order aab."},
		{`web_reg_find("Text/IC=Order {OrderId}", "SaveCount=n", LAST);`, "ORDER A+B order a+b"},
		{`web_reg_find("Text/DIG=#-{OrderId}", "SaveCount=n", LAST);`, "1-a+b 2-a+b x-a+b"},
	} {
		_, extractor, _, _, _ := convertFindAssertionWithFindings(parseSingleCall(t, tt.call))
		if extractor == nil || !strings.Contains(extractor.Expression, `\Q{{var.orderid}}\E`) {
			t.Fatalf("%s: extractor = %+v", tt.call, extractor)
		}
		resolved := strings.ReplaceAll(extractor.Expression, "{{var.orderid}}", "a+b")
		if got := len(regexp.MustCompile(resolved).FindAllString(tt.body, -1)); got != 2 {
			t.Errorf("%s: %q counts %d in %q, want 2", tt.call, resolved, got, tt.body)
		}
	}
}
