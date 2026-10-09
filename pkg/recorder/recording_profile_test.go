package recorder

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

func TestConvertSessionAppliesRecordingProfile(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	session := &Session{ID: "p", Name: "Profile", BaseURL: "https://shop.test", Requests: []RecordedRequest{
		{Seq: 1, Timestamp: t0, Method: "GET", URL: "https://shop.test/start", Path: "/start", Host: "shop.test", StatusCode: 200,
			Headers:         map[string]string{"User-Agent": "Recorder/1", "X-Debug": "1", "Accept": "text/html"},
			ResponseHeaders: map[string]string{"Content-Type": "text/html"}, ResponseBody: `<a href="/order?order=A9F3K2&x=1">order</a>`},
		{Seq: 2, Timestamp: t0.Add(800 * time.Millisecond), ThinkTimeMs: 800, Method: "GET", URL: "https://cdn.shop.test/lib.js", Path: "/lib.js", Host: "cdn.shop.test", StatusCode: 200,
			ResponseHeaders: map[string]string{"Content-Type": "text/javascript"}},
		{Seq: 3, Timestamp: t0.Add(2 * time.Second), ThinkTimeMs: 1200, Method: "GET", URL: "https://shop.test/order?order=A9F3K2", Path: "/order", QueryString: "order=A9F3K2", Host: "shop.test", StatusCode: 200,
			Headers: map[string]string{"User-Agent": "Recorder/1"}, ResponseHeaders: map[string]string{"Content-Type": "text/html"}, ResponseBody: "<html>ok</html>"},
	}}
	for i := range session.Requests {
		req := &session.Requests[i]
		req.RawHeaders = req.Headers
		req.HeadersMulti, req.RawHeadersMulti = multiFromSingle(req.Headers), multiFromSingle(req.Headers)
		req.ResponseHeadersMulti = multiFromSingle(req.ResponseHeaders)
		req.RawResponseHeaders, req.RawResponseHeadersMulti = req.ResponseHeaders, req.ResponseHeadersMulti
	}
	profile := &recordingprofile.Profile{
		ID: "default", Name: "Default", RecordHeaders: []string{"User-Agent"}, DropHeaders: []string{"X-Debug"},
		ExcludeHosts: []string{"cdn.shop.test"}, ThinkTimeThresholdMs: 1500,
		Correlation: recordingprofile.CorrelationSettings{UserRules: []recordingprofile.UserRule{{Name: "OrderID", Type: "boundary", Left: "order=", Right: "&"}}},
	}
	result := ConvertSessionWithOptions(session, "", ConvertOptions{Profile: profile})
	actions := result.Script.Actions
	if len(actions) != 2 {
		t.Fatalf("the excluded host must not become a step: %+v", actions)
	}
	start := actions[0]
	if start.Headers["User-Agent"] != "Recorder/1" || start.Headers["X-Debug"] != "" {
		t.Fatalf("profile header lists: %v", start.Headers)
	}
	if start.ThinkTimeAfter {
		t.Fatalf("a 1.2 s pause is below the 1.5 s threshold: %+v", start.ThinkTime)
	}
	if len(start.Extractors) != 1 || start.Extractors[0].Name != "OrderID" || start.Extractors[0].Type != "boundary" {
		t.Fatalf("the project rule must extract the order id: %+v", start.Extractors)
	}
	if order := actions[1]; !strings.Contains(order.Target, "{{var.orderid}}") && !strings.Contains(order.Target, "{{var.OrderID}}") {
		t.Fatalf("the order id must be correlated: %q", order.Target)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), `recording profile "Default" left 1 request(s) out`) {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

// Converted through the shared pipeline, a proxy recording keeps resources
// as their own steps with resource_mode separate, and its replay baseline
// lines up with those steps.
func TestProxyRecorderRecordsSeparateResourcesAsSteps(t *testing.T) {
	off := false
	result := ConvertSessionWithOptions(pageOptionsSession(), "pages", ConvertOptions{AsyncScan: &off, Profile: &recordingprofile.Profile{ID: "r", Name: "R", ResourceMode: "separate"}})
	if result.Script == nil || len(result.Script.Actions) != 3 {
		t.Fatalf("the page, its logo and the poll must each be a step: %+v", result.Script)
	}
	logo := result.Script.Actions[1]
	if !strings.HasSuffix(logo.Target, "/logo.png") {
		t.Fatalf("second step = %q, want the logo", logo.Target)
	}
	if len(result.Baseline) != 3 || result.Baseline[1].ID != 1 || !strings.HasSuffix(result.Baseline[1].RequestURL, "/logo.png") {
		t.Fatalf("baseline = %+v, want one observation per step", result.Baseline)
	}
}

// A session's cookie capture (its profile's, or an explicit choice over it)
// stands when it is converted with the same profile; converting with a
// different profile applies that profile's choice.
func TestConvertSessionCookieCaptureFollowsTheSession(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	headers := map[string]string{"Cookie": "pref=dark", "Accept": "text/html"}
	session := &Session{ID: "c", Name: "Cookies", BaseURL: "https://shop.test", Filters: SessionFilters{CaptureCookies: true, RecordingProfileID: "default"}, Requests: []RecordedRequest{
		{Seq: 1, Timestamp: t0, Method: "GET", URL: "https://shop.test/start", Path: "/start", Host: "shop.test", StatusCode: 200,
			Headers: headers, RawHeaders: headers, HeadersMulti: multiFromSingle(headers), RawHeadersMulti: multiFromSingle(headers),
			ResponseHeaders: map[string]string{"Content-Type": "text/html"}, ResponseBody: "<html>ok</html>"},
	}}
	off := false
	cookie := func(profileID string) string {
		profile := &recordingprofile.Profile{ID: profileID, Name: profileID, CaptureCookies: &off}
		result := ConvertSessionWithOptions(session, "", ConvertOptions{Profile: profile})
		return result.Script.Actions[0].Headers["Cookie"]
	}
	if got := cookie("default"); got != "pref=dark" {
		t.Fatalf("same profile: Cookie = %q, want the captured cookie", got)
	}
	if got := cookie("strict"); got != "" {
		t.Fatalf("a different profile that drops cookies: Cookie = %q", got)
	}
}
