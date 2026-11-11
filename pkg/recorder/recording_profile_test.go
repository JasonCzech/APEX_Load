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

func TestRecordingProfileScrubSensitivePropagatesToFiltersAndConversion(t *testing.T) {
	profile := &recordingprofile.Profile{
		ID:             "scrub-on",
		Name:           "Scrub On",
		ScrubSensitive: true,
	}
	var filters FilterConfig
	filters.ApplyProfile(profile)
	if !filters.ScrubSensitive || filters.RecordingProfileID != "scrub-on" {
		t.Fatalf("ApplyProfile = %+v, want ScrubSensitive=true and RecordingProfileID=scrub-on", filters)
	}

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	session := &Session{
		ID:      "p-scrub",
		Name:    "Profile Scrub",
		BaseURL: "https://shop.test",
		Requests: []RecordedRequest{
			{
				Seq:          1,
				Timestamp:    t0,
				Method:       "POST",
				URL:          "https://shop.test/login",
				Path:         "/login",
				Host:         "shop.test",
				StatusCode:   200,
				ContentType:  "application/x-www-form-urlencoded",
				Body:         "username=ada&password=s3cret%2Bvalue",
				ResponseBody: `{"auth":{"access_token":"tok-abcdef123456"}}`,
			},
			{
				Seq:        2,
				Timestamp:  t0.Add(time.Second),
				Method:     "GET",
				URL:        "https://shop.test/account",
				Path:       "/account",
				Host:       "shop.test",
				StatusCode: 200,
				Headers:    map[string]string{"Authorization": "Bearer tok-abcdef123456"},
			},
		},
	}

	unredacted := ConvertSessionWithOptions(session, "", ConvertOptions{
		Parameterize: true,
		Profile:      &recordingprofile.Profile{ID: "default", Name: "Default"},
	})
	if len(unredacted.Correlations) != 1 || unredacted.Correlations[0].SampleValue != "tok-abcdef123456" {
		t.Fatalf("default profile correlations = %#v, want unredacted sample", unredacted.Correlations)
	}
	if _, exists := unredacted.Script.Metadata.Import.Preserved["recorded_input_secret_columns"]; exists {
		t.Fatalf("default profile unexpectedly marked recorded_input_secret_columns: %#v", unredacted.Script.Metadata.Import.Preserved)
	}

	scrubbed := ConvertSessionWithOptions(session, "", ConvertOptions{
		Parameterize: true,
		Profile:      profile,
	})
	if len(scrubbed.Correlations) != 1 || scrubbed.Correlations[0].Value != "" || scrubbed.Correlations[0].SampleValue != "" {
		t.Fatalf("scrub_sensitive profile correlations = %#v, want blanked value and sample", scrubbed.Correlations)
	}
	cols, ok := scrubbed.Script.Metadata.Import.Preserved["recorded_input_secret_columns"].([]string)
	if !ok || len(cols) != 1 || cols[0] != "password" {
		t.Fatalf("scrub_sensitive profile recorded_input_secret_columns = %#v, want [password]", scrubbed.Script.Metadata.Import.Preserved["recorded_input_secret_columns"])
	}
}

func TestWebSocketOnlySessionHonorsScrubSensitiveAndRedactHeaders(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wsSession := &Session{
		ID:      "ws-scrub",
		Name:    "WS Scrub",
		BaseURL: "https://ws.example.test",
		Filters: FilterConfig{
			RedactHeaders: []string{"X-Custom-Secret"},
		},
		Requests: []RecordedRequest{
			{
				Seq:        1,
				Timestamp:  t0,
				Method:     "GET",
				URL:        "wss://ws.example.test/realtime",
				Path:       "/realtime",
				Host:       "ws.example.test",
				StatusCode: 101,
				Headers: map[string]string{
					"Authorization":   "[redacted]",
					"X-Api-Key":       "[redacted]",
					"X-Custom-Secret": "[redacted]",
					"X-Trace":         "trace-123",
				},
				RawHeaders: map[string]string{
					"Authorization":   "Bearer ws-secret-token",
					"X-Api-Key":       "ws-api-key-123",
					"X-Custom-Secret": "custom-secret-val",
					"X-Trace":         "trace-123",
				},
				WebSocketMessages: []RecordedWebSocketMessage{
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeText, Payload: `{"ping":1}`},
				},
			},
		},
	}

	// Default (ScrubSensitive=false): preserves Authorization and X-Api-Key from RawHeaders while honoring explicit RedactHeaders.
	unredacted := ConvertSession(wsSession, "WS Default")
	wsConnect := unredacted.Script.Actions[0]
	if wsConnect.Headers["Authorization"] != "Bearer ws-secret-token" || wsConnect.Headers["X-Api-Key"] != "ws-api-key-123" {
		t.Fatalf("default WS headers = %#v, want raw Authorization and X-Api-Key", wsConnect.Headers)
	}
	if wsConnect.Headers["X-Custom-Secret"] != "[redacted]" {
		t.Fatalf("default WS headers ignored explicit RedactHeaders: %#v", wsConnect.Headers)
	}

	// ScrubSensitive=true via Profile: redacts Authorization and X-Api-Key on WebSocket-only sessions.
	scrubbedByProfile := ConvertSessionWithOptions(wsSession, "WS Profile Scrub", ConvertOptions{
		Profile: &recordingprofile.Profile{ID: "strict", Name: "Strict", ScrubSensitive: true},
	})
	scrubConnect := scrubbedByProfile.Script.Actions[0]
	if scrubConnect.Headers["Authorization"] != "[redacted]" || scrubConnect.Headers["X-Api-Key"] != "[redacted]" || scrubConnect.Headers["X-Custom-Secret"] != "[redacted]" {
		t.Fatalf("scrubbed WS headers leaked sensitive credentials: %#v", scrubConnect.Headers)
	}
	if scrubConnect.Headers["X-Trace"] != "trace-123" {
		t.Fatalf("scrubbed WS headers altered non-sensitive header: %#v", scrubConnect.Headers)
	}

	// ScrubSensitive=true on SessionFilters at capture time is also honored by default ConvertSession.
	capturedScrubSession := *wsSession
	capturedScrubSession.Filters.ScrubSensitive = true
	scrubbedByFilter := ConvertSession(&capturedScrubSession, "WS Filter Scrub")
	if scrubbedByFilter.Script.Actions[0].Headers["Authorization"] != "[redacted]" ||
		scrubbedByFilter.Script.Actions[0].Headers["X-Api-Key"] != "[redacted]" {
		t.Fatalf("session-filter ScrubSensitive WS headers leaked credentials: %#v", scrubbedByFilter.Script.Actions[0].Headers)
	}
}
