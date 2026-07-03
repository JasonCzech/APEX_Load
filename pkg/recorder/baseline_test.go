package recorder

import "testing"

func TestSessionBaseline(t *testing.T) {
	session := &Session{
		ID:      "s1",
		BaseURL: "http://example.com",
		Requests: []RecordedRequest{
			{Seq: 1, Method: "GET", URL: "http://example.com/style.css", Path: "/style.css", StatusCode: 200, IsStatic: true},
			{Seq: 2, Method: "POST", URL: "http://example.com/api/login", Path: "/api/login",
				Body:         `{"username":"a","password":"secret"}`,
				ResponseBody: `{"token":"abc123","session_id":"sess-789"}`,
				Headers:      map[string]string{"Content-Type": "application/json"}},
			{Seq: 3, Method: "GET", URL: "http://example.com/api/drop", Path: "/api/drop", StatusCode: 200, Excluded: true},
			{Seq: 4, Method: "GET", URL: "http://example.com/orders?token=abc123", Path: "/orders", StatusCode: 200,
				Headers:    map[string]string{"X-Token": "[redacted]"},
				RawHeaders: map[string]string{"X-Token": "abc123"}},
		},
	}

	baseline := SessionBaseline(session)

	// Static + excluded requests are skipped; IDs are sequential action indices.
	if len(baseline) != 2 {
		t.Fatalf("expected 2 baseline observations (static + excluded skipped), got %d", len(baseline))
	}
	if baseline[0].ID != 0 || baseline[1].ID != 1 {
		t.Errorf("observation IDs must be action indices, got %d and %d", baseline[0].ID, baseline[1].ID)
	}
	// Raw recorded values (responses included) are preserved for correlation.
	if baseline[0].ResponseBody != `{"token":"abc123","session_id":"sess-789"}` {
		t.Errorf("recorded response body must be preserved, got %q", baseline[0].ResponseBody)
	}
	if baseline[1].RequestURL != "http://example.com/orders?token=abc123" {
		t.Errorf("recorded request url must be preserved, got %q", baseline[1].RequestURL)
	}
	if baseline[1].RequestHeaders["X-Token"] != "abc123" {
		t.Errorf("baseline request headers should use raw in-memory values, got %v", baseline[1].RequestHeaders)
	}
}

func TestSessionBaselineNilSession(t *testing.T) {
	if SessionBaseline(nil) != nil {
		t.Error("nil session should yield nil baseline")
	}
}
