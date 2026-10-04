package http

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	httpCacheMaxEntries   = 256
	httpCacheMaxBodyBytes = 2 * 1024 * 1024
	// httpCacheMaxTotalBytes bounds the aggregate size of one VUser's cache.
	// Each VUser gets its own HTTP protocol instance (hence its own cache), so
	// without a total-byte budget the entry-count × per-entry caps alone allow
	// ~512MiB per VUser (256 × 2MiB), which a fleet of VUsers caching multi-MB
	// assets can multiply into an OOM.
	httpCacheMaxTotalBytes = 32 * 1024 * 1024
	// The cache is per VUser, so its local ceiling must be paired with a process
	// ceiling or a large fleet can multiply it into hundreds of GiB.
	httpCacheMaxProcessBytes int64 = 256 << 20
)

var processHTTPCacheBudget = responseCaptureBudget{limit: httpCacheMaxProcessBytes}

type httpResponseCache struct {
	mu                sync.Mutex
	entries           map[string]httpCacheEntry
	totalBytes        int64
	memory            *responseCaptureLease
	iteration         uint64
	excludedMIMETypes []string
}

type httpCacheEntry struct {
	statusCode        int
	header            http.Header
	body              []byte
	expiresAt         time.Time
	etag              string
	lastModified      string
	storedAt          time.Time
	lastUsedIteration uint64
}

func newHTTPResponseCache() *httpResponseCache {
	return newHTTPResponseCacheWithBudget(&processHTTPCacheBudget)
}

func newHTTPResponseCacheWithBudget(budget *responseCaptureBudget) *httpResponseCache {
	return &httpResponseCache{entries: map[string]httpCacheEntry{}, memory: budget.lease()}
}

func parseHTTPCacheEnabled(config map[string]any) bool {
	enabled, ok := config["http_cache_enabled"].(bool)
	return ok && enabled
}

func (c *httpResponseCache) lookup(req *http.Request, now time.Time) (*httpCacheEntry, bool) {
	if c == nil || req == nil || req.URL == nil || req.Method != http.MethodGet {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[httpCacheKey(req)]
	if !ok {
		return nil, false
	}
	entry.lastUsedIteration = c.iteration
	c.entries[httpCacheKey(req)] = entry
	// Cache entry bodies are immutable after insertion. Return a shallow snapshot so
	// a hit does not clone a multi-MiB body; response() clones the header map and
	// bytes.Reader never mutates the shared body.
	snapshot := entry
	return &snapshot, !entry.expiresAt.IsZero() && now.Before(entry.expiresAt)
}

func (c *httpResponseCache) store(req *http.Request, resp *http.Response, body []byte, now time.Time) {
	if c == nil || req == nil || req.URL == nil || resp == nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusOK {
		return
	}
	if matchesCompatibilityMIME(resp.Header, c.excludedMIMETypes) {
		c.remove(httpCacheKey(req))
		return
	}
	if len(body) > httpCacheMaxBodyBytes || requestHasAuthorization(req) && !responseAllowsAuthenticatedCache(resp.Header) {
		return
	}
	entry, ok := cacheEntryFromResponseView(resp, body, now)
	if !ok {
		return
	}
	c.put(httpCacheKey(req), entry, true)
}

// put inserts/replaces an entry after reserving its net process-wide memory
// growth. cloneBody is true for a network response whose pooled capture buffer
// will be reused, and false for a 304 refresh that safely reuses immutable
// cached bytes.
func (c *httpResponseCache) put(key string, entry httpCacheEntry, cloneBody bool) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old, replacing := c.entries[key]
	entry.lastUsedIteration = c.iteration
	oldSize := int64(0)
	if replacing {
		oldSize = old.size()
	}
	newSize := entry.size()
	if growth := newSize - oldSize; growth > 0 && !c.memory.reserve(growth) {
		return false
	}
	if cloneBody {
		entry.body = append([]byte(nil), entry.body...)
	}
	if replacing {
		c.totalBytes -= oldSize
		delete(c.entries, key)
	}
	c.entries[key] = entry
	c.totalBytes += newSize
	if released := oldSize - newSize; released > 0 {
		c.memory.releaseBytes(released)
	}
	for len(c.entries) > 0 && (len(c.entries) > httpCacheMaxEntries || c.totalBytes > httpCacheMaxTotalBytes) {
		oldestKey := ""
		var oldest time.Time
		for k, e := range c.entries {
			if oldestKey == "" || e.storedAt.Before(oldest) {
				oldestKey, oldest = k, e.storedAt
			}
		}
		if oldestKey == "" {
			break
		}
		evictedSize := c.entries[oldestKey].size()
		c.totalBytes -= evictedSize
		delete(c.entries, oldestKey)
		c.memory.releaseBytes(evictedSize)
	}
	return true
}

func (c *httpResponseCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	released := c.totalBytes
	c.entries = make(map[string]httpCacheEntry)
	c.totalBytes = 0
	c.memory.releaseBytes(released)
	c.mu.Unlock()
}

// ClearCache implements protocol.CacheController: it discards every cached
// response the VUser has accumulated. Used by browser emulation's "simulate a
// new user on each iteration". Nil-safe when caching is disabled.
func (h *HTTP) ClearCache(_ context.Context) error {
	h.cache.clear()
	return nil
}

// isContentBearingResponse reports whether the response carries a
// content-bearing (HTML) media type — the set "Cache URLs requiring content"
// restricts cache storage to.
func isContentBearingResponse(headers http.Header) bool {
	mediaType, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "text/html", "application/xhtml+xml":
		return true
	}
	return false
}

// size estimates the memory footprint of a cache entry: the body plus the
// header key/value bytes.
func (e httpCacheEntry) size() int64 {
	size := int64(len(e.body))
	for key, values := range e.header {
		size += int64(len(key))
		for _, value := range values {
			size += int64(len(value))
		}
	}
	return size
}

func (c *httpResponseCache) refresh(req *http.Request, stale *httpCacheEntry, resp *http.Response, now time.Time) {
	if c == nil || req == nil || req.URL == nil || stale == nil || resp == nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusNotModified {
		return
	}
	header := stale.header.Clone()
	for key, values := range resp.Header {
		// Cached bodies use the decoded representation. A 304 carries no body,
		// so its wire coding/length cannot qualify those retained bytes.
		if strings.EqualFold(key, "Content-Encoding") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		header.Del(key)
		for _, value := range values {
			header.Add(key, value)
		}
	}
	// The caller consumes this private snapshot for the current 304 response,
	// even when the refreshed representation must no longer stay cached.
	stale.header = header
	synthetic := &http.Response{
		StatusCode: stale.statusCode,
		Header:     header,
	}
	if matchesCompatibilityMIME(header, c.excludedMIMETypes) {
		c.remove(httpCacheKey(req))
		return
	}
	entry, ok := cacheEntryFromResponseView(synthetic, stale.body, now)
	if !ok {
		return
	}
	c.put(httpCacheKey(req), entry, false)
}

func (e *httpCacheEntry) addConditionalHeaders(req *http.Request) {
	if e == nil || req == nil {
		return
	}
	if e.etag != "" && req.Header.Get("If-None-Match") == "" {
		req.Header.Set("If-None-Match", e.etag)
	}
	if e.lastModified != "" && req.Header.Get("If-Modified-Since") == "" {
		req.Header.Set("If-Modified-Since", e.lastModified)
	}
}

func (e *httpCacheEntry) response(req *http.Request) *http.Response {
	header := e.header.Clone()
	header.Set("X-Apexload-Cache", "hit")
	return &http.Response{
		StatusCode:    e.statusCode,
		Status:        strconv.Itoa(e.statusCode) + " " + http.StatusText(e.statusCode),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(e.body)),
		ContentLength: int64(len(e.body)),
		Request:       req,
	}
}

func cacheEntryFromResponse(resp *http.Response, body []byte, now time.Time) (httpCacheEntry, bool) {
	entry, ok := cacheEntryFromResponseView(resp, body, now)
	if ok {
		entry.body = append([]byte(nil), body...)
	}
	return entry, ok
}

func cacheEntryFromResponseView(resp *http.Response, body []byte, now time.Time) (httpCacheEntry, bool) {
	cacheControl := parseCacheControl(resp.Header.Get("Cache-Control"))
	if _, noStore := cacheControl["no-store"]; noStore {
		return httpCacheEntry{}, false
	}
	etag := strings.TrimSpace(resp.Header.Get("ETag"))
	lastModified := strings.TrimSpace(resp.Header.Get("Last-Modified"))
	expiresAt := time.Time{}
	if rawMaxAge, ok := cacheControl["max-age"]; ok {
		if seconds, err := strconv.Atoi(rawMaxAge); err == nil && seconds >= 0 {
			expiresAt = now.Add(time.Duration(seconds) * time.Second)
		}
	} else if rawExpires := strings.TrimSpace(resp.Header.Get("Expires")); rawExpires != "" {
		if expires, err := http.ParseTime(rawExpires); err == nil {
			expiresAt = expires
		}
	}
	if !expiresAt.After(now) && etag == "" && lastModified == "" {
		return httpCacheEntry{}, false
	}
	return httpCacheEntry{
		statusCode:   resp.StatusCode,
		header:       resp.Header.Clone(),
		body:         body,
		expiresAt:    expiresAt,
		etag:         etag,
		lastModified: lastModified,
		storedAt:     now,
	}, true
}

func parseCacheControl(value string) map[string]string {
	directives := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, rawValue, hasValue := strings.Cut(part, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if !hasValue {
			directives[key] = ""
			continue
		}
		directives[key] = strings.Trim(strings.TrimSpace(rawValue), `"`)
	}
	return directives
}

func requestHasAuthorization(req *http.Request) bool {
	return req != nil && strings.TrimSpace(req.Header.Get("Authorization")) != ""
}

func responseAllowsAuthenticatedCache(headers http.Header) bool {
	cacheControl := parseCacheControl(headers.Get("Cache-Control"))
	_, public := cacheControl["public"]
	_, mustRevalidate := cacheControl["must-revalidate"]
	_, sMaxAge := cacheControl["s-maxage"]
	return public || mustRevalidate || sMaxAge
}

func httpCacheKey(req *http.Request) string {
	policy, _ := req.Context().Value(httpVersionCacheKey{}).(string)
	return policy + "\x00" + req.Method + " " + req.URL.String()
}

func (c *httpResponseCache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, exists := c.entries[key]; exists {
		delete(c.entries, key)
		c.totalBytes -= old.size()
		c.memory.releaseBytes(old.size())
	}
}
