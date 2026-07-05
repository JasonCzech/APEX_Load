package http

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	httpCacheMaxEntries   = 256
	httpCacheMaxBodyBytes = 2 * 1024 * 1024
)

type httpResponseCache struct {
	mu      sync.Mutex
	entries map[string]httpCacheEntry
}

type httpCacheEntry struct {
	statusCode   int
	header       http.Header
	body         []byte
	expiresAt    time.Time
	etag         string
	lastModified string
	storedAt     time.Time
}

func newHTTPResponseCache() *httpResponseCache {
	return &httpResponseCache{entries: map[string]httpCacheEntry{}}
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
	return entry.clone(), !entry.expiresAt.IsZero() && now.Before(entry.expiresAt)
}

func (c *httpResponseCache) store(req *http.Request, resp *http.Response, body []byte, now time.Time) {
	if c == nil || req == nil || req.URL == nil || resp == nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusOK {
		return
	}
	if len(body) > httpCacheMaxBodyBytes || requestHasAuthorization(req) && !responseAllowsAuthenticatedCache(resp.Header) {
		return
	}
	entry, ok := cacheEntryFromResponse(resp, body, now)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= httpCacheMaxEntries {
		for key := range c.entries {
			delete(c.entries, key)
			break
		}
	}
	c.entries[httpCacheKey(req)] = entry
}

func (c *httpResponseCache) refresh(req *http.Request, stale *httpCacheEntry, resp *http.Response, now time.Time) {
	if c == nil || req == nil || req.URL == nil || stale == nil || resp == nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusNotModified {
		return
	}
	header := stale.header.Clone()
	for key, values := range resp.Header {
		header.Del(key)
		for _, value := range values {
			header.Add(key, value)
		}
	}
	synthetic := &http.Response{
		StatusCode: stale.statusCode,
		Header:     header,
	}
	entry, ok := cacheEntryFromResponse(synthetic, stale.body, now)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[httpCacheKey(req)] = entry
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

func (e httpCacheEntry) clone() *httpCacheEntry {
	body := append([]byte(nil), e.body...)
	return &httpCacheEntry{
		statusCode:   e.statusCode,
		header:       e.header.Clone(),
		body:         body,
		expiresAt:    e.expiresAt,
		etag:         e.etag,
		lastModified: e.lastModified,
		storedAt:     e.storedAt,
	}
}

func cacheEntryFromResponse(resp *http.Response, body []byte, now time.Time) (httpCacheEntry, bool) {
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
		body:         append([]byte(nil), body...),
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
	return req.Method + " " + req.URL.String()
}
