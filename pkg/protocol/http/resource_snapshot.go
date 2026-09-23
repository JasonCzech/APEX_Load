package http

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Optional evidence must not consume the response-reader budget: exhausting
// diagnostics may omit evidence, but must never make a required fetch fail.
var processHTTPResourceEvidenceBudget = responseCaptureBudget{limit: 16 << 20}

type resourceCaptureKey struct{}
type resourceCapture struct {
	mu        sync.Mutex
	entries   []protocol.ResourceSnapshot
	omitted   int
	remaining int
	lease     *responseCaptureLease
}

func newResourceCapture(limit int64) *resourceCapture {
	if limit <= 0 {
		limit = 1 << 20
	}
	// Reserve only a fraction of the parent envelope for resources. The engine
	// performs the final aggregate projection including the main exchange.
	return &resourceCapture{remaining: int(min(limit/4, 256<<10)), lease: processHTTPResourceEvidenceBudget.lease()}
}

func resourceCaptureHeaders(headers http.Header) (map[string]string, bool) {
	if len(headers) == 0 {
		return nil, false
	}
	truncated := len(headers) > 32
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string)
	for i, key := range keys {
		if i == 32 {
			break
		}
		if len(key) > 256 {
			truncated = true
			continue
		}
		value := headers.Get(key)
		if len(value) > 4096 {
			value = "[not retained: header limit]"
			truncated = true
		}
		out[key] = value
	}
	return scrub.Headers(out, scrub.Policy{}), truncated
}

func (c *resourceCapture) record(entry protocol.NetworkEntry, request, response http.Header, body []byte, cached bool, complete bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining < 256 || len(c.entries) >= 80 {
		c.omitted++
		return
	}
	url := entry.URL
	if len(url) > 8192 {
		url = "[not retained: URL limit]"
	}
	requestHeaders, requestTruncated := resourceCaptureHeaders(request)
	responseHeaders, responseTruncated := resourceCaptureHeaders(response)
	r := protocol.ResourceSnapshot{RequestURL: scrub.ValueURL(url, scrub.Policy{}), RequestHeaders: requestHeaders, ResponseHeaders: responseHeaders, Truncated: requestTruncated || responseTruncated || len(entry.URL) > 8192, ResponseStatus: entry.StatusCode, CacheHit: cached, FailureKind: entry.FailureKind, ResponseBodyState: "not_captured"}
	// Never retain a prefix of an unredacted secret or opaque binary payload.
	// Runtime-secret scrubbing follows in the engine before final byte projection.
	if complete && len(body) <= 32<<10 && textualResourceBody(response.Get("Content-Type"), body) {
		r.ResponseBody = scrub.Value(string(body), scrub.Policy{})
		r.ResponseBodyState = "captured"
		if len(body) == 0 {
			r.ResponseBodyState = "empty"
		} else if r.ResponseBody != string(body) {
			r.ResponseBodyState = "redacted"
		}
	} else {
		r.Truncated = r.Truncated || len(body) > 0
	}
	encoded, _ := json.Marshal(r)
	if len(encoded) > c.remaining {
		r.ResponseBody = ""
		r.ResponseBodyState = "not_captured"
		r.Truncated = true
		encoded, _ = json.Marshal(r)
	}
	if len(encoded) > c.remaining || !c.lease.reserve(int64(len(encoded))) {
		c.omitted++
		return
	}
	c.remaining -= len(encoded)
	c.entries = append(c.entries, r)
}

func (c *resourceCapture) attach(snapshot *protocol.ErrorSnapshot) {
	if c == nil || snapshot == nil {
		return
	}
	// Resource workers have joined before this point. Sorting keeps diagnostics
	// stable when parallel downloads complete in a different order.
	sort.SliceStable(c.entries, func(i, j int) bool { return c.entries[i].RequestURL < c.entries[j].RequestURL })
	snapshot.Resources = c.entries
	snapshot.OmittedResources = c.omitted
}

func recordResourceCapture(ctx context.Context, entry protocol.NetworkEntry, request, response http.Header, body []byte, cached, complete bool) {
	if capture, ok := ctx.Value(resourceCaptureKey{}).(*resourceCapture); ok {
		capture.record(entry, request, response, body, cached, complete)
	}
}

func textualResourceBody(contentType string, body []byte) bool {
	if len(body) == 0 {
		return true
	}
	if !utf8.Valid(body) {
		return false
	}
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	return strings.HasPrefix(contentType, "text/") || contentType == "application/json" || contentType == "application/xml" || contentType == "application/javascript" || contentType == "application/x-javascript" || strings.HasSuffix(contentType, "+json") || strings.HasSuffix(contentType, "+xml")
}
