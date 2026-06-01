package recorder

import (
	"net/http"
	"strings"
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

func TestConvertSession_CarriesBinaryBodyEncoding(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Binary Upload",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:          1,
				Method:       "POST",
				URL:          "http://example.com/api/upload",
				Path:         "/api/upload",
				Host:         "example.com",
				Body:         "AP8QgA==",
				BodyEncoding: "base64",
				ContentType:  "application/octet-stream",
				StatusCode:   201,
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.Body != "AP8QgA==" {
		t.Fatalf("body = %q, want base64 payload", action.Body)
	}
	if action.BodyEncoding != "base64" {
		t.Fatalf("body encoding = %q, want base64", action.BodyEncoding)
	}
}

func TestConvertSession_WebSocketMessages(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Socket Flow",
		BaseURL: "ws://stream.example.com",
		Requests: []RecordedRequest{
			{
				Seq:        1,
				Method:     "GET",
				URL:        "ws://stream.example.com/orders?tenant=acme",
				Path:       "/orders",
				Host:       "stream.example.com",
				StatusCode: http.StatusSwitchingProtocols,
				Headers:    map[string]string{"Sec-WebSocket-Protocol": "json"},
				WebSocketMessages: []RecordedWebSocketMessage{
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeText, Payload: `{"type":"subscribe"}`},
					{Direction: webSocketDirectionServerToClient, Opcode: webSocketOpcodeText, Payload: `{"type":"subscribed"}`},
					{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeBinary, Payload: "AP8=", PayloadEncoding: "base64"},
				},
			},
		},
	}

	result := ConvertSession(s, "")
	if result.Script.Protocol != "websocket" {
		t.Fatalf("protocol = %q, want websocket", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "ws://stream.example.com" {
		t.Fatalf("base_url = %q, want ws origin", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 5 {
		t.Fatalf("action count = %d, want connect + 3 messages + close", len(result.Script.Actions))
	}
	connect := result.Script.Actions[0]
	if connect.Type != "ws_connect" || connect.Target != "/orders?tenant=acme" {
		t.Fatalf("connect action = %#v", connect)
	}
	send := result.Script.Actions[1]
	if send.Type != "ws_send" || send.Body != `{"type":"subscribe"}` || send.Params["type"] != "text" {
		t.Fatalf("send action = %#v", send)
	}
	receive := result.Script.Actions[2]
	if receive.Type != "ws_receive" || receive.Params["match"] != `{"type":"subscribed"}` {
		t.Fatalf("receive action = %#v", receive)
	}
	binarySend := result.Script.Actions[3]
	if binarySend.Type != "ws_send" || binarySend.Body != "AP8=" || binarySend.BodyEncoding != "base64" || binarySend.Params["type"] != "binary" {
		t.Fatalf("binary send action = %#v", binarySend)
	}
	if result.Script.Actions[4].Type != "ws_close" {
		t.Fatalf("final action type = %q, want ws_close", result.Script.Actions[4].Type)
	}
}

func TestConvertSession_CorrelatesCapturedCookies(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Cookie Flow",
		BaseURL: "http://example.com",
		Filters: SessionFilters{CaptureCookies: true},
		Requests: []RecordedRequest{
			{
				Seq:        1,
				Method:     "GET",
				URL:        "http://example.com/login",
				Path:       "/login",
				Host:       "example.com",
				StatusCode: 200,
				ResponseHeaders: map[string]string{
					"Set-Cookie": "session=sid-abcdef123456; Path=/; HttpOnly",
				},
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/account",
				Path:       "/account",
				Host:       "example.com",
				StatusCode: 200,
				Headers: map[string]string{
					"Cookie": "session=sid-abcdef123456; theme=dark",
				},
			},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %#v, want one cookie correlation", result.Correlations)
	}
	corr := result.Correlations[0]
	if corr.Name != "corr_cookie_session" || corr.Source != "header" || corr.ExtractType != "regex" {
		t.Fatalf("unexpected correlation: %#v", corr)
	}
	if len(result.Script.Actions[0].Extractors) != 1 {
		t.Fatalf("first action extractors = %#v", result.Script.Actions[0].Extractors)
	}
	if got := result.Script.Actions[1].Headers["Cookie"]; got != "session={{var.corr_cookie_session}}; theme=dark" {
		t.Fatalf("correlated cookie header = %q", got)
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
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected import metadata with recorded resources")
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved["recorded_resource_urls_available"] != true {
		t.Fatalf("recorded_resource_urls_available = %#v, want true", preserved["recorded_resource_urls_available"])
	}
	urls, ok := preserved["recorded_resource_urls"].([]string)
	if !ok || len(urls) != 2 {
		t.Fatalf("recorded_resource_urls = %#v, want two static URLs", preserved["recorded_resource_urls"])
	}
}

func TestConvertSession_ExcludedRequestsAndStepNames(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Curated Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/api/keep", Path: "/api/keep", StatusCode: 200, StepName: "Load Keep API"},
			{Seq: 2, Method: "GET", URL: "http://example.com/api/drop", Path: "/api/drop", StatusCode: 200, Excluded: true},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 1 {
		t.Fatalf("expected 1 included action, got %d", len(result.Script.Actions))
	}
	if !strings.Contains(result.Script.Actions[0].Name, "Load Keep API") {
		t.Fatalf("expected custom step name, got %q", result.Script.Actions[0].Name)
	}
}

func TestConvertSession_TransactionMetadata(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Transaction Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "POST", URL: "http://example.com/api/login", Path: "/api/login", StatusCode: 200, TransactionName: "Login"},
			{Seq: 2, Method: "GET", URL: "http://example.com/api/home", Path: "/api/home", StatusCode: 200},
		},
	}

	result := ConvertSession(s, "")
	if len(result.Script.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(result.Script.Actions))
	}
	if got := result.Script.Actions[0].Metadata["source_transaction"]; got != "Login" {
		t.Fatalf("expected source_transaction=Login on first action, got %v", got)
	}
	if result.Script.Actions[1].Metadata != nil {
		t.Fatalf("expected no metadata on action without transaction, got %v", result.Script.Actions[1].Metadata)
	}
}

func TestConvertSessionWithOptions_Parameterize(t *testing.T) {
	s := &Session{
		ID:      "test",
		Name:    "Param Test",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{
				Seq:         1,
				Method:      "POST",
				URL:         "http://example.com/login",
				Path:        "/login",
				Host:        "example.com",
				Body:        "username=demo&password=s3cret&remember=1",
				ContentType: "application/x-www-form-urlencoded",
				StatusCode:  200,
			},
			{
				Seq:        2,
				Method:     "GET",
				URL:        "http://example.com/search?query=widgets&page=2",
				Path:       "/search",
				Host:       "example.com",
				StatusCode: 200,
			},
		},
	}

	// Without parameterization the values stay literal.
	plain := ConvertSession(s, "")
	if len(plain.Script.DataSources) != 0 {
		t.Fatalf("expected no data sources without parameterization, got %d", len(plain.Script.DataSources))
	}

	result := ConvertSessionWithOptions(s, "", ConvertOptions{Parameterize: true})
	if len(result.Script.DataSources) != 1 {
		t.Fatalf("expected 1 generated data source, got %d", len(result.Script.DataSources))
	}
	ds := result.Script.DataSources[0]
	if ds.Name != "recorded_inputs" || ds.Type != "csv" || !ds.FirstRowHeader {
		t.Fatalf("unexpected data source: %+v", ds)
	}
	for _, col := range []string{"username", "password", "query"} {
		if !strings.Contains(ds.InlineData, col) {
			t.Fatalf("expected column %q in inline data %q", col, ds.InlineData)
		}
	}

	bodies := result.Script.Actions[0].Body + result.Script.Actions[1].Target
	if !strings.Contains(bodies, "{{data.recorded_inputs.username}}") {
		t.Fatalf("expected username parameter reference, got body %q", result.Script.Actions[0].Body)
	}
	if !strings.Contains(result.Script.Actions[1].Target, "{{data.recorded_inputs.query}}") {
		t.Fatalf("expected query parameter reference in target, got %q", result.Script.Actions[1].Target)
	}
	// Non-input fields are left alone.
	if strings.Contains(result.Script.Actions[0].Body, "{{data.recorded_inputs.remember}}") {
		t.Fatalf("did not expect 'remember' to be parameterized: %q", result.Script.Actions[0].Body)
	}
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "Parameterized") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected a parameterization warning, got %v", result.Warnings)
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

func TestDetectCorrelations_HTMLFormToken(t *testing.T) {
	requests := []RecordedRequest{
		{
			Seq:        1,
			Method:     "GET",
			URL:        "http://example.com/login",
			Path:       "/login",
			StatusCode: 200,
			ResponseHeaders: map[string]string{
				"Content-Type": "text/html",
			},
			ResponseBody: `<form method="post"><input type="hidden" name="csrf_token" value="tok-9f8e7d6c5b4a"><input type="text" name="user"></form>`,
		},
		{
			Seq:        2,
			Method:     "POST",
			URL:        "http://example.com/login",
			Path:       "/login",
			Body:       `csrf_token=tok-9f8e7d6c5b4a&user=demo`,
			StatusCode: 200,
		},
	}

	correlations := detectCorrelations(requests)

	var csrf *Correlation
	for i := range correlations {
		if correlations[i].Name == "corr_csrf_token" {
			csrf = &correlations[i]
		}
	}
	if csrf == nil {
		t.Fatalf("expected corr_csrf_token correlation, got %+v", correlations)
	}
	if csrf.ExtractType != "regex" || csrf.Source != "body" {
		t.Fatalf("expected regex/body extractor, got %s/%s", csrf.ExtractType, csrf.Source)
	}
	if !strings.Contains(csrf.Expression, `name="csrf_token"`) || !strings.Contains(csrf.Expression, `value="([^"]+)"`) {
		t.Fatalf("unexpected expression: %q", csrf.Expression)
	}
	if len(csrf.UsedInSeqs) == 0 {
		t.Fatal("expected the form token to be used in a later request")
	}
}

func TestApplyCorrelationVars_LongestFirst(t *testing.T) {
	// A short value that is a substring of a longer value must not clobber it.
	corrMap := map[string]string{
		"123":       "{{var.short_id}}",
		"123456789": "{{var.long_token}}",
	}
	result := applyCorrelationVars("token=123456789", corrMap)
	if result != "token={{var.long_token}}" {
		t.Fatalf("expected longest value replaced first, got %q", result)
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

	filtered := filterRecordedHeaders(headers, false)

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

	withCookies := filterRecordedHeaders(headers, true)
	if v, ok := withCookies["Cookie"]; !ok || v != "session=abc" {
		t.Fatalf("Cookie should be kept when cookie capture is enabled, got %q", v)
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
				Seq:             1,
				Timestamp:       time.Now(),
				Method:          "POST",
				URL:             "http://example.com/auth",
				Path:            "/auth",
				Host:            "example.com",
				StatusCode:      200,
				ResponseBody:    `{"token":"dynamic-token-value-12345678"}`,
				ResponseHeaders: map[string]string{},
				Headers:         map[string]string{},
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
