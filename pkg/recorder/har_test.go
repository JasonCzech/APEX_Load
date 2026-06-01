package recorder

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestExportHARSkipsExcludedRequestsByDefault(t *testing.T) {
	session := &Session{
		ID:        "rec-1",
		Name:      "Checkout",
		StartedAt: time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
		Requests: []RecordedRequest{
			{
				Seq:                   1,
				Timestamp:             time.Date(2026, 5, 10, 12, 0, 1, 0, time.UTC),
				Method:                http.MethodPost,
				URL:                   "https://shop.example.com/api/cart?debug=true",
				Path:                  "/api/cart",
				QueryString:           "debug=true",
				HeadersMulti:          map[string][]string{"Content-Type": {"application/json"}, "X-Test": {"a", "b"}},
				Body:                  `{"sku":"123"}`,
				BodySizeBytes:         13,
				ContentType:           "application/json",
				StatusCode:            http.StatusCreated,
				ResponseTimeMs:        42.5,
				ResponseHeadersMulti:  map[string][]string{"Content-Type": {"application/json"}, "Location": {"/api/cart/1"}},
				ResponseBody:          `{"id":"cart-1"}`,
				ResponseBodySizeBytes: 15,
				Note:                  "primary cart request",
			},
			{
				Seq:        2,
				Timestamp:  time.Date(2026, 5, 10, 12, 0, 2, 0, time.UTC),
				Method:     http.MethodGet,
				URL:        "https://shop.example.com/analytics.gif",
				Path:       "/analytics.gif",
				StatusCode: http.StatusOK,
				Excluded:   true,
			},
		},
	}

	data, err := ExportHAR(session, false)
	if err != nil {
		t.Fatal(err)
	}

	var archive struct {
		Log struct {
			Entries []struct {
				Request struct {
					Method      string `json:"method"`
					URL         string `json:"url"`
					QueryString []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"queryString"`
					Headers []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"headers"`
					PostData *struct {
						MimeType string `json:"mimeType"`
						Text     string `json:"text"`
					} `json:"postData"`
				} `json:"request"`
				Response struct {
					Status      int    `json:"status"`
					RedirectURL string `json:"redirectURL"`
					Content     struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"response"`
				Comment string `json:"comment"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if len(archive.Log.Entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(archive.Log.Entries))
	}
	entry := archive.Log.Entries[0]
	if entry.Request.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", entry.Request.Method)
	}
	if entry.Request.QueryString[0].Name != "debug" || entry.Request.QueryString[0].Value != "true" {
		t.Fatalf("unexpected query string: %#v", entry.Request.QueryString)
	}
	if len(entry.Request.Headers) != 3 {
		t.Fatalf("header count = %d, want 3 multi-value headers", len(entry.Request.Headers))
	}
	if entry.Request.PostData == nil || entry.Request.PostData.Text != `{"sku":"123"}` {
		t.Fatalf("unexpected post data: %#v", entry.Request.PostData)
	}
	if entry.Response.Status != http.StatusCreated || entry.Response.RedirectURL != "/api/cart/1" {
		t.Fatalf("unexpected response: %#v", entry.Response)
	}
	if entry.Comment != "primary cart request" {
		t.Fatalf("comment = %q, want note", entry.Comment)
	}
}

func TestExportHARCanIncludeExcludedRequests(t *testing.T) {
	session := &Session{
		ID:        "rec-1",
		Name:      "All Requests",
		StartedAt: time.Now(),
		Requests: []RecordedRequest{
			{Seq: 1, Timestamp: time.Now(), Method: http.MethodGet, URL: "https://example.com/a", Path: "/a", StatusCode: http.StatusOK},
			{Seq: 2, Timestamp: time.Now(), Method: http.MethodGet, URL: "https://example.com/b", Path: "/b", StatusCode: http.StatusOK, Excluded: true},
		},
	}

	data, err := ExportHAR(session, true)
	if err != nil {
		t.Fatal(err)
	}
	var archive struct {
		Log struct {
			Entries []json.RawMessage `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatal(err)
	}
	if len(archive.Log.Entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(archive.Log.Entries))
	}
}

func TestExportHARPreservesBase64BodyEncoding(t *testing.T) {
	session := &Session{
		ID:        "rec-binary",
		Name:      "Binary",
		StartedAt: time.Now(),
		Requests: []RecordedRequest{
			{
				Seq:                  1,
				Timestamp:            time.Now(),
				Method:               http.MethodPost,
				URL:                  "https://example.com/upload",
				Path:                 "/upload",
				Body:                 "AP8QgA==",
				BodyEncoding:         "base64",
				BodySizeBytes:        5,
				ContentType:          "application/octet-stream",
				StatusCode:           http.StatusCreated,
				ResponseBody:         "/wAB",
				ResponseBodyEncoding: "base64",
			},
		},
	}

	data, err := ExportHAR(session, false)
	if err != nil {
		t.Fatal(err)
	}
	var archive struct {
		Log struct {
			Entries []struct {
				Request struct {
					PostData *struct {
						Text     string `json:"text"`
						Encoding string `json:"encoding"`
					} `json:"postData"`
				} `json:"request"`
				Response struct {
					Content struct {
						Text     string `json:"text"`
						Encoding string `json:"encoding"`
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatal(err)
	}
	entry := archive.Log.Entries[0]
	if entry.Request.PostData == nil || entry.Request.PostData.Text != "AP8QgA==" || entry.Request.PostData.Encoding != "base64" {
		t.Fatalf("postData = %#v, want base64 payload", entry.Request.PostData)
	}
	if entry.Response.Content.Text != "/wAB" || entry.Response.Content.Encoding != "base64" {
		t.Fatalf("response content = %#v, want base64 payload", entry.Response.Content)
	}
}
