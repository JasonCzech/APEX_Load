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
	if !strings.Contains(strings.Join(result.Warnings, "\n"), `Recording profile "Default" left 1 request(s) out`) {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestProxyRecorderRecordsSeparateResourcesInStep(t *testing.T) {
	result := ConvertSessionWithOptions(pageOptionsSession(), "pages", ConvertOptions{Profile: &recordingprofile.Profile{ID: "r", Name: "R", ResourceMode: "separate"}})
	if result.Script == nil || len(result.Script.Actions) != 2 {
		t.Fatalf("resources must not become steps: %+v", result.Script)
	}
	if resources, _ := result.Script.Actions[0].Params["resources"].([]any); len(resources) != 1 {
		t.Fatalf("the logo must be attached to its page: %v", result.Script.Actions[0].Params["resources"])
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "cannot keep resources as separate steps") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}
