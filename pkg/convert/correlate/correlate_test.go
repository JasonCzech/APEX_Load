package correlate

import (
	"strings"
	"testing"
)

func TestDetectHTMLTokenAndApplyLongestFirst(t *testing.T) {
	observations := []Observation{
		{
			ID:           1,
			ResponseBody: `<form><input type="hidden" name="csrf_token" value="tok-abcdef123456"></form>`,
		},
		{
			ID:          2,
			RequestBody: "csrf_token=tok-abcdef123456&id=123456789",
		},
		{
			ID:         3,
			RequestURL: "https://example.com/api?id=123",
		},
	}

	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlation count = %d, want 1: %#v", len(correlations), correlations)
	}
	corr := correlations[0]
	if corr.Name != "corr_csrf_token" || corr.ExtractType != "regex" || corr.FoundInID != 1 {
		t.Fatalf("unexpected correlation: %#v", corr)
	}
	if len(corr.UsedInIDs) != 1 || corr.UsedInIDs[0] != 2 {
		t.Fatalf("used ids = %#v, want [2]", corr.UsedInIDs)
	}

	vars := map[string]string{
		"123":       "{{var.short}}",
		"123456789": "{{var.long}}",
	}
	if got := ApplyVars("id=123456789", vars); got != "id={{var.long}}" {
		t.Fatalf("ApplyVars longest first = %q", got)
	}
}

func TestDetectSetCookieValue(t *testing.T) {
	observations := []Observation{
		{
			ID:              1,
			ResponseHeaders: map[string]string{"Set-Cookie": "session=sid-abcdef123456; Path=/; HttpOnly"},
		},
		{
			ID:             2,
			RequestHeaders: map[string]string{"Cookie": "theme=dark; session=sid-abcdef123456"},
		},
	}

	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlation count = %d, want 1: %#v", len(correlations), correlations)
	}
	corr := correlations[0]
	if corr.Name != "corr_cookie_session" || corr.Source != "header" || corr.ExtractType != "regex" {
		t.Fatalf("unexpected cookie correlation: %#v", corr)
	}
	if corr.SampleValue != "sid-abcdef123456" {
		t.Fatalf("sample value = %q", corr.SampleValue)
	}
	if !strings.Contains(corr.Expression, "Set-Cookie") || !strings.Contains(corr.Expression, "session=") {
		t.Fatalf("unexpected expression: %q", corr.Expression)
	}
}
