package recorder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

type harArchive struct {
	Log harLog `json:"log"`
}

type harLog struct {
	Version string     `json:"version"`
	Creator harCreator `json:"creator"`
	Pages   []harPage  `json:"pages,omitempty"`
	Entries []harEntry `json:"entries"`
}

type harCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type harPage struct {
	StartedDateTime time.Time `json:"startedDateTime"`
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	PageTimings     harTiming `json:"pageTimings"`
}

type harEntry struct {
	StartedDateTime           time.Time                            `json:"startedDateTime"`
	Time                      float64                              `json:"time"`
	Request                   harRequest                           `json:"request"`
	Response                  harResponse                          `json:"response"`
	Cache                     struct{}                             `json:"cache"`
	Timings                   harTiming                            `json:"timings"`
	PageRef                   string                               `json:"pageref,omitempty"`
	Comment                   string                               `json:"comment,omitempty"`
	WebSocketMessages         []harWebSocketMessage                `json:"_webSocketMessages,omitempty"`
	ResponseContentProvenance requestimport.ResponseContentCapture `json:"_apexload_response_content"`
	HTTPVersionProvenance     map[string]any                       `json:"_apexload_http_version,omitempty"`
}

type harRequest struct {
	Method      string       `json:"method"`
	URL         string       `json:"url"`
	HTTPVersion string       `json:"httpVersion"`
	Cookies     []harNameVal `json:"cookies"`
	Headers     []harNameVal `json:"headers"`
	QueryString []harNameVal `json:"queryString"`
	PostData    *harPostData `json:"postData,omitempty"`
	HeadersSize int          `json:"headersSize"`
	BodySize    int          `json:"bodySize"`
}

type harResponse struct {
	Status      int          `json:"status"`
	StatusText  string       `json:"statusText"`
	HTTPVersion string       `json:"httpVersion"`
	Cookies     []harNameVal `json:"cookies"`
	Headers     []harNameVal `json:"headers"`
	Content     harContent   `json:"content"`
	RedirectURL string       `json:"redirectURL"`
	HeadersSize int          `json:"headersSize"`
	BodySize    int          `json:"bodySize"`
}

type harContent struct {
	Size     int     `json:"size"`
	MimeType string  `json:"mimeType"`
	Text     *string `json:"text,omitempty"`
	Encoding string  `json:"encoding,omitempty"`
	Comment  string  `json:"comment,omitempty"`
}

type harPostData struct {
	MimeType string       `json:"mimeType"`
	Text     string       `json:"text,omitempty"`
	Encoding string       `json:"encoding,omitempty"`
	Params   []harNameVal `json:"params,omitempty"`
	Comment  string       `json:"comment,omitempty"`
}

type harTiming struct {
	Blocked float64 `json:"blocked,omitempty"`
	DNS     float64 `json:"dns,omitempty"`
	Connect float64 `json:"connect,omitempty"`
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
	SSL     float64 `json:"ssl,omitempty"`
}

type harNameVal struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harWebSocketMessage struct {
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`
	Opcode    int       `json:"opcode"`
	Data      string    `json:"data,omitempty"`
	Encoding  string    `json:"encoding,omitempty"`
	Size      int       `json:"size,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
}

func ExportHAR(session *Session, includeExcluded bool) ([]byte, error) {
	pageID := "page_" + session.ID
	archive := harArchive{
		Log: harLog{
			Version: "1.2",
			Creator: harCreator{
				Name:    "APEX Load Recorder",
				Version: "2.0",
			},
			Pages: []harPage{
				{
					StartedDateTime: session.StartedAt,
					ID:              pageID,
					Title:           session.Name,
					PageTimings:     harTiming{},
				},
			},
		},
	}

	for _, req := range session.Requests {
		if req.Excluded && !includeExcluded {
			continue
		}
		archive.Log.Entries = append(archive.Log.Entries, exportHAREntry(req, pageID))
	}

	return json.MarshalIndent(archive, "", "  ")
}

func exportHAREntry(req RecordedRequest, pageID string) harEntry {
	capture := effectiveRecordedResponseContent(req)
	body, encoding, decodedSize := req.ResponseBody, req.ResponseBodyEncoding, capture.DecodedSizeBytes
	var storedBody *string
	if capture.Complete() {
		storedBody = &body
	}
	if !capture.Complete() {
		body, encoding, decodedSize = "", "", -1
	}
	entry := harEntry{
		StartedDateTime: req.Timestamp,
		Time:            req.ResponseTimeMs,
		Request: harRequest{
			Method:      req.Method,
			URL:         req.URL,
			HTTPVersion: exportedObservedHTTPVersion(req.RequestHTTPVersion),
			Cookies:     headerCookies(req.HeadersMulti, "Cookie"),
			Headers:     harHeaders(req.HeadersMulti),
			QueryString: harQueryString(req.URL, req.QueryString),
			HeadersSize: -1,
			BodySize:    req.BodySizeBytes,
		},
		Response: harResponse{
			Status:      req.StatusCode,
			StatusText:  http.StatusText(req.StatusCode),
			HTTPVersion: exportedObservedHTTPVersion(req.ResponseHTTPVersion),
			Cookies:     responseCookies(req.ResponseHeadersMulti),
			Headers:     harHeaders(req.ResponseHeadersMulti),
			Content: harContent{
				Size:     decodedSize,
				MimeType: req.responseMimeType(),
				Text:     storedBody,
				Encoding: encoding,
			},
			RedirectURL: firstHeaderValue(req.ResponseHeadersMulti, "Location"),
			HeadersSize: -1,
			BodySize:    req.ResponseBodySizeBytes,
		},
		Timings: harTiming{
			Send:    0,
			Wait:    req.ResponseTimeMs,
			Receive: 0,
		},
		PageRef:                   pageID,
		Comment:                   req.Note,
		HTTPVersionProvenance:     requestimport.ProxyHTTPVersionProvenance(),
		ResponseContentProvenance: capture,
	}
	for _, msg := range req.WebSocketMessages {
		entry.WebSocketMessages = append(entry.WebSocketMessages, harWebSocketMessage{
			Time:      msg.Timestamp,
			Type:      harWebSocketMessageType(msg.Direction),
			Opcode:    msg.Opcode,
			Data:      msg.Payload,
			Encoding:  msg.PayloadEncoding,
			Size:      msg.PayloadSizeBytes,
			Truncated: msg.PayloadTruncated,
		})
	}
	if req.BodyTruncated {
		entry.Request.PostData = &harPostData{MimeType: req.ContentType, Text: req.Body, Encoding: req.BodyEncoding, Comment: "request body truncated by recorder capture limit"}
	} else if req.Body != "" || req.BodySizeBytes > 0 {
		entry.Request.PostData = &harPostData{MimeType: req.ContentType, Text: req.Body, Encoding: req.BodyEncoding}
	}
	if req.ResponseBodyTruncated {
		entry.Response.Content.Comment = "response body truncated by recorder capture limit"
	}
	return entry
}

func observedHTTPVersion(value string, major, minor int) string {
	if value != "" {
		return exportedObservedHTTPVersion(value)
	}
	if major > 0 && major <= 9 && minor >= 0 && minor <= 9 {
		return fmt.Sprintf("HTTP/%d.%d", major, minor)
	}
	return "unknown"
}

func exportedObservedHTTPVersion(value string) string {
	if len(value) > 256 || strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func harWebSocketMessageType(direction string) string {
	switch direction {
	case webSocketDirectionClientToServer:
		return "send"
	case webSocketDirectionServerToClient:
		return "receive"
	default:
		return direction
	}
}

func (req RecordedRequest) responseMimeType() string {
	if ct := firstHeaderValue(req.ResponseHeadersMulti, "Content-Type"); ct != "" {
		return ct
	}
	return req.ContentType
}

func harHeaders(headers map[string][]string) []harNameVal {
	if len(headers) == 0 {
		return []harNameVal{}
	}
	items := make([]harNameVal, 0)
	for name, values := range headers {
		if isRecorderControlHeader(name) {
			continue
		}
		for _, value := range values {
			items = append(items, harNameVal{Name: name, Value: value})
		}
	}
	return items
}

func harQueryString(rawURL, rawQuery string) []harNameVal {
	if rawQuery == "" {
		if parsed, err := url.Parse(rawURL); err == nil {
			rawQuery = parsed.RawQuery
		}
	}
	if rawQuery == "" {
		return []harNameVal{}
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return []harNameVal{{Name: rawQuery, Value: ""}}
	}
	items := make([]harNameVal, 0)
	for name, vals := range values {
		for _, value := range vals {
			items = append(items, harNameVal{Name: name, Value: value})
		}
	}
	return items
}

func headerCookies(headers map[string][]string, headerName string) []harNameVal {
	raw := firstHeaderValue(headers, headerName)
	if raw == "" || raw == "[redacted]" {
		return []harNameVal{}
	}
	parts := strings.Split(raw, ";")
	cookies := make([]harNameVal, 0, len(parts))
	for _, part := range parts {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && name != "" {
			cookies = append(cookies, harNameVal{Name: name, Value: value})
		}
	}
	return cookies
}

func responseCookies(headers map[string][]string) []harNameVal {
	values := headerValues(headers, "Set-Cookie")
	cookies := make([]harNameVal, 0, len(values))
	for _, raw := range values {
		if raw == "" || raw == "[redacted]" {
			continue
		}
		first := strings.Split(raw, ";")[0]
		name, value, ok := strings.Cut(strings.TrimSpace(first), "=")
		if ok && name != "" {
			cookies = append(cookies, harNameVal{Name: name, Value: value})
		}
	}
	return cookies
}

func firstHeaderValue(headers map[string][]string, name string) string {
	values := headerValues(headers, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func headerValues(headers map[string][]string, name string) []string {
	for k, values := range headers {
		if strings.EqualFold(k, name) {
			return values
		}
	}
	return nil
}
