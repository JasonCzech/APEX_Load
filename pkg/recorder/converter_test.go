package recorder

import (
	"testing"
	"time"
)

func TestConvertSession_Basic(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Login Flow",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:            1,
				Method:         "GET",
				URL:            "http://example.com/",
				Path:           "/",
				Host:           "example.com",
				StatusCode:     200,
				ResponseTimeMs: 50,
				Headers:        map[string]string{"Accept": "text/html"},
			},
			{
				Seq:            2,
				Method:         "POST",
				URL:            "http://example.com/api/login",
				Path:           "/api/login",
				Host:           "example.com",
				Body:           `{"username":"test","password":"secret"}`,
				ContentType:    "application/json",
				StatusCode:     200,
				ResponseTimeMs: 120,
				ResponseBody:   `{"token":"abc123","session_id":"sess-xyz-789"}`,
				Headers:        map[string]string{"Content-Type": "application/json"},
				ThinkTimeMs:    2000,
			},
		},
	}

	result := ConvertSession(s, "")
	if result.Script == nil {
		t.Fatal("expected script")
	}
	if result.Script.Name != "Login Flow" {
		t.Fatalf("expected Login Flow, got %s", result.Script.Name)
	}
	if result.Script.Protocol != "http" {
		t.Fatalf("expected http protocol, got %s", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "http://example.com" {
		t.Fatalf("expected base URL, got %s", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(result.Script.Actions))
	}

	a1 := result.Script.Actions[0]
	if a1.Method != "GET" {
		t.Fatalf("expected GET, got %s", a1.Method)
	}

	a2 := result.Script.Actions[1]
	if a2.Method != "POST" {
		t.Fatalf("expected POST, got %s", a2.Method)
	}
	if a2.Body == "" {
		t.Fatal("expected body on POST action")
	}
	if !a2.ThinkTimeAfter {
		t.Fatal("expected think time after second action (2000ms)")
	}
	if a2.ThinkTime.MeanMs != 2000 {
		t.Fatalf("expected think time 2000ms, got %f", a2.ThinkTime.MeanMs)
	}
}

func TestConvertSession_CustomName(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Original Name",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/", Path: "/", StatusCode: 200},
		},
	}

	result := ConvertSession(s, "Custom Script Name")
	if result.Script.Name != "Custom Script Name" {
		t.Fatalf("expected Custom Script Name, got %s", result.Script.Name)
	}
}

func TestConvertSession_StaticFiltered(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Static Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/api/data", Path: "/api/data", StatusCode: 200},
			{Seq: 2, Method: "GET", URL: "http://example.com/style.css", Path: "/style.css", StatusCode: 200, IsStatic: true},
			{Seq: 3, Method: "GET", URL: "http://example.com/logo.png", Path: "/logo.png", StatusCode: 200, IsStatic: true},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 1 {
		t.Fatalf("expected 1 non-static action, got %d", len(result.Script.Actions))
	}
}

func TestConvertSession_Empty(t *testing.T) {
	s := &Session{
		ID:       "test",
		Name:     "Empty",
		Requests: []RecordedRequest{},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 0 {
		t.Fatalf("expected 0 actions, got %d", len(result.Script.Actions))
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(result.Warnings))
	}
}

func TestConvertSession_Assertions(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Assert Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/ok", Path: "/ok", StatusCode: 200},
			{Seq: 2, Method: "POST", URL: "http://example.com/create", Path: "/create", StatusCode: 201},
		},
	}

	result := ConvertSession(s, "")
	a1 := result.Script.Actions[0]
	if len(a1.Assertions) != 1 || a1.Assertions[0].Expected != 200 {
		t.Fatalf("expected status_code assertion 200, got %+v", a1.Assertions)
	}
	a2 := result.Script.Actions[1]
	if len(a2.Assertions) != 1 || a2.Assertions[0].Expected != 201 {
		t.Fatalf("expected status_code assertion 201, got %+v", a2.Assertions)
	}
}

func TestDetectCorrelations(t *testing.T) {
	requests := []RecordedRequest{
		{
			Seq:          1,
			Method:       "POST",
			URL:          "http://example.com/login",
			Path:         "/login",
			StatusCode:   200,
			ResponseBody: `{"token":"eyJhbGciOiJIUzI1NiJ9.testpayload.signature","session_id":"sess-abcdef-123456"}`,
			ResponseHeaders: map[string]string{
				"X-Csrf-Token": "csrf-token-value-here",
			},
		},
		{
			Seq:    2,
			Method: "GET",
			URL:    "http://example.com/api/data?token=eyJhbGciOiJIUzI1NiJ9.testpayload.signature",
			Path:   "/api/data",
			Headers: map[string]string{
				"X-Csrf-Token": "csrf-token-value-here",
			},
			StatusCode:   200,
			ResponseBody: `{"items":[]}`,
		},
		{
			Seq:    3,
			Method: "POST",
			URL:    "http://example.com/api/action",
			Path:   "/api/action",
			Body:   `{"session":"sess-abcdef-123456"}`,
			Headers: map[string]string{
				"Authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.testpayload.signature",
			},
			StatusCode:   200,
			ResponseBody: `{"ok":true}`,
		},
	}

	correlations := detectCorrelations(requests)

	if len(correlations) == 0 {
		t.Fatal("expected at least one correlation detected")
	}

	foundToken := false
	for _, c := range correlations {
		if c.Name == "corr_token" || c.Name == "corr_session_id" || c.Name == "corr_x_csrf_token" {
			foundToken = true
			if len(c.UsedInSeqs) == 0 {
				t.Fatalf("correlation %s should have used_in_seqs", c.Name)
			}
		}
	}
	if !foundToken {
		t.Fatalf("expected token/session correlation, got %+v", correlations)
	}
}

func TestFilterRecordedHeaders(t *testing.T) {
	headers := map[string]string{
		"Content-Type":    "application/json",
		"Accept":          "text/html",
		"Host":            "example.com",
		"Content-Length":  "42",
		"X-Custom-Header": "custom-value",
		"Authorization":   "Bearer xyz",
		"Cookie":          "session=abc",
		"User-Agent":      "Mozilla/5.0",
	}

	filtered := filterRecordedHeaders(headers)

	if _, ok := filtered["Host"]; ok {
		t.Fatal("Host should be filtered")
	}
	if _, ok := filtered["Content-Length"]; ok {
		t.Fatal("Content-Length should be filtered")
	}
	if _, ok := filtered["Cookie"]; ok {
		t.Fatal("Cookie should be filtered")
	}
	if _, ok := filtered["User-Agent"]; ok {
		t.Fatal("User-Agent should be filtered")
	}
	if _, ok := filtered["Accept"]; ok {
		t.Fatal("Accept should be filtered")
	}
	if v, ok := filtered["Content-Type"]; !ok || v != "application/json" {
		t.Fatal("Content-Type should be kept")
	}
	if v, ok := filtered["X-Custom-Header"]; !ok || v != "custom-value" {
		t.Fatal("X-Custom-Header should be kept")
	}
	if v, ok := filtered["Authorization"]; !ok || v != "Bearer xyz" {
		t.Fatal("Authorization should be kept")
	}
}

func TestSimplifyPath(t *testing.T) {
	tests := []struct {
		path   string
		expect string
	}{
		{"/api/v1/users", "/api/v1/users"},
		{"/", "/"},
		{"/api/v1/org/123/users/456/roles", "/users/456/roles"},
	}

	for _, tt := range tests {
		got := simplifyPath(tt.path)
		if got != tt.expect {
			t.Errorf("simplifyPath(%q) = %q, want %q", tt.path, got, tt.expect)
		}
	}
}

func TestApplyCorrelationVars(t *testing.T) {
	corrMap := map[string]string{
		"abc123":  "{{var.corr_token}}",
		"sess-42": "{{var.corr_session_id}}",
	}

	input := "http://example.com/api?token=abc123&session=sess-42"
	result := applyCorrelationVars(input, corrMap)

	if result != "http://example.com/api?token={{var.corr_token}}&session={{var.corr_session_id}}" {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Fatal("short string should not be truncated")
	}
	result := truncate("this is a long string that should be truncated", 20)
	if len(result) != 23 { // 20 + "..."
		t.Fatalf("expected 23 chars, got %d: %q", len(result), result)
	}
}

func TestConvertSession_CorrelationApplied(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Corr Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:          1,
				Timestamp:    time.Now(),
				Method:       "POST",
				URL:          "http://example.com/auth",
				Path:         "/auth",
				Host:         "example.com",
				StatusCode:   200,
				ResponseBody: `{"token":"dynamic-token-value-12345678"}`,
				ResponseHeaders: map[string]string{},
				Headers: map[string]string{},
			},
			{
				Seq:        2,
				Timestamp:  time.Now(),
				Method:     "GET",
				URL:        "http://example.com/api/data?auth=dynamic-token-value-12345678",
				Path:       "/api/data",
				Host:       "example.com",
				StatusCode: 200,
				Headers:    map[string]string{},
			},
		},
	}

	result := ConvertSession(s, "")

	if len(result.Correlations) == 0 {
		t.Fatal("expected correlations to be detected")
	}

	foundExtractor := false
	for _, action := range result.Script.Actions {
		for _, ext := range action.Extractors {
			if ext.Name == "corr_token" {
				foundExtractor = true
			}
		}
	}
	if !foundExtractor {
		t.Fatal("expected extractor on the auth action")
	}

	action2 := result.Script.Actions[1]
	if action2.Target == "http://example.com/api/data?auth=dynamic-token-value-12345678" {
		t.Fatal("expected correlation var to replace dynamic value in target URL")
	}
}
