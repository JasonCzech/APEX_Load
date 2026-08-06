package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONBody(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
		wantName   string
		wantCount  int
	}{
		{
			name:      "valid object decodes and returns true",
			body:      `{"name":"alpha","count":3}`,
			wantOK:    true,
			wantName:  "alpha",
			wantCount: 3,
		},
		{
			name:       "malformed JSON writes 400 and returns false",
			body:       `{"name":`,
			wantOK:     false,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty body writes 400 and returns false",
			body:       ``,
			wantOK:     false,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "wrong type for field writes 400 and returns false",
			body:       `{"count":"not-an-int"}`,
			wantOK:     false,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			var got payload
			ok := decodeJSONBody(rec, req, &got)

			if ok != tt.wantOK {
				t.Fatalf("decodeJSONBody returned %v, want %v", ok, tt.wantOK)
			}

			if tt.wantOK {
				if got.Name != tt.wantName || got.Count != tt.wantCount {
					t.Fatalf("decoded %+v, want name=%q count=%d", got, tt.wantName, tt.wantCount)
				}
				// On success the helper must not write a response.
				if rec.Code != http.StatusOK { // httptest default is 200 until WriteHeader is called
					t.Fatalf("helper wrote status %d on success; expected no write (default %d)", rec.Code, http.StatusOK)
				}
				return
			}

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			// The 400 path must surface the "invalid request body" error envelope,
			// preserving the message contract the handlers historically emitted.
			var envelope map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error response was not JSON: %v (%q)", err, rec.Body.String())
			}
			if !strings.HasPrefix(envelope["error"], "invalid request body") {
				t.Fatalf("error = %q, want prefix %q", envelope["error"], "invalid request body")
			}
		})
	}
}

func TestDecodeOptionalJSONBodyRejectsConfiguredBodyLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"overflow"}`))
	rec := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(rec, req.Body, 8)

	var got struct {
		Name string `json:"name"`
	}
	ok := decodeOptionalJSONBody(rec, req, &got)
	if ok {
		t.Fatal("decodeOptionalJSONBody returned true for a capped body that should exceed the cap")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(rec.Body.String(), `"request body exceeds the configured size limit"`) {
		t.Fatalf("response body = %q, expected oversize error", rec.Body.String())
	}
}

func TestDecodeOptionalJSONBodyAcceptsLargePayloadsWithoutHelperSmallCap(t *testing.T) {
	largeReason := strings.Repeat("x", 70000)
	body := fmt.Sprintf(`{"reason":"%s"}`, largeReason)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	var payload struct {
		Reason string `json:"reason"`
	}
	if ok := decodeOptionalJSONBody(rec, req, &payload); !ok {
		t.Fatalf("decodeOptionalJSONBody returned false: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := payload.Reason; got != largeReason {
		t.Fatalf("reason length = %d, want %d", len(got), len(largeReason))
	}
}
