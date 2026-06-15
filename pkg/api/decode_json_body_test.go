package api

import (
	"encoding/json"
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
