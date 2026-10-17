package http

import (
	bytebuffer "bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/contentdecode"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
	"golang.org/x/net/html"
)

// Browser-emulation resource download ("Download non-HTML resources") and
// prefetch/prerender simulation for the HTTP replay path. After a successful
// GET that returns HTML, the fetcher parses the document, resolves embedded
// resource URLs against the final page URL, and fetches them with browser-like
// bounded concurrency. Every fetch flows through the same simulated browser
// cache as the main request, so "Simulate browser cache" is meaningful across
// pages and iterations. Resource failures follow the configured replay outcome policy.
// MaxPageResources is the most resources one page fetches, explicit
// (params.resources) and discovered together. Importers apply the same bound.
const MaxPageResources = httpcontract.MaxPageResources

const (
	// maxPageResources bounds the resources fetched per page, including URLs
	// discovered inside fetched CSS. LoadRunner has no per-page limit and
	// recorded pages routinely list 100+ EXTRARES entries; a page that reaches
	// the bound reports resource_cap_reached instead of truncating silently.
	maxPageResources = MaxPageResources
	// maxPrefetchResources bounds the low-priority prefetch/prerender wave.
	maxPrefetchResources = 16
	// maxResourceCSSRefs bounds url(...) extraction per fetched stylesheet.
	maxResourceCSSRefs = 32
	// maxResourceConcurrency / maxResourcePerHostConcurrency mirror browser
	// connection behavior (6 per host, 8 total in flight per VUser).
	maxResourceConcurrency        = 8
	maxResourcePerHostConcurrency = 6
	// defaultMaxProcessResourceConcurrency bounds in-flight resource fetches
	// across the WHOLE generator, not just per VUser-page. The per-VUser cap
	// alone multiplies by fleet size (1k VUsers x 8 = 8k concurrent requests on
	// top of the configured load), which exhausts file descriptors and inflates
	// the offered load well past what the operator asked for. Operators can
	// raise or lower it with APEXLOAD_MAX_RESOURCE_CONCURRENCY.
	//
	// Trade-off worth knowing: because the resource phase is folded into page
	// time (LR parity), saturating this budget shows up as higher reported page
	// time. The wait is excluded from each resource's own DurationMs, but not
	// from the parent step. Size it above your expected concurrent-page count if
	// you need page time to reflect only the target.
	defaultMaxProcessResourceConcurrency = 256
	// maxResourceCountedBytes bounds both wire and decoded resource bytes.
	// Bodies up to httpCacheMaxBodyBytes are retained for cache/CSS; larger
	// bodies must still reach validated decoder EOF within this ceiling.
	maxResourceCountedBytes = 8 * 1024 * 1024
)

// processResourceSlots bounds concurrent resource fetches per generator
// process. Sized once from the operator-owned APEXLOAD_MAX_RESOURCE_CONCURRENCY
// so one generator-wide cap applies across every VUser and script.
var (
	processResourceSlotsChan chan struct{}
	processResourceSlotsOnce sync.Once
)

func processResourceSlots() chan struct{} {
	processResourceSlotsOnce.Do(func() {
		limit := defaultMaxProcessResourceConcurrency
		if raw := strings.TrimSpace(os.Getenv("APEXLOAD_MAX_RESOURCE_CONCURRENCY")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		processResourceSlotsChan = make(chan struct{}, limit)
	})
	return processResourceSlotsChan
}

type resourceRef struct {
	url          string
	referer      *string
	resourceType string // image|script|stylesheet|icon|media|document|other|prefetch|prerender|preload
}

type resourceFetcher struct {
	h           *HTTP
	download    bool
	prefetch    bool
	filterMode  string
	filterHosts []string
}

// Keep configured download filters even when only explicit resources are used.
func newResourceFetcher(h *HTTP, config map[string]any) *resourceFetcher {
	download, _ := config["http_download_non_html_resources"].(bool)
	prefetch, _ := config["http_simulate_prefetch"].(bool)
	if !download && !prefetch && len(protocol.ConfigStringSlice(config, "web_download_filters_hosts")) == 0 {
		return nil
	}
	fetcher := &resourceFetcher{h: h, download: download, prefetch: prefetch}
	if mode, ok := config["web_download_filters_mode"].(string); ok {
		fetcher.filterMode = strings.ToLower(strings.TrimSpace(mode))
	}
	// ConfigStringSlice (not a bare []string assertion) so the filter survives a
	// config that reached this generator as JSON, where []string decodes to []any.
	if hosts := protocol.ConfigStringSlice(config, "web_download_filters_hosts"); len(hosts) > 0 {
		fetcher.filterHosts = normalizeResourceFilterHosts(hosts)
	}
	return fetcher
}

func normalizeResourceFilterHosts(hosts []string) []string {
	normalized := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		candidate := strings.ToLower(strings.TrimSpace(host))
		candidate = strings.TrimPrefix(candidate, "http://")
		candidate = strings.TrimPrefix(candidate, "https://")
		candidate = strings.TrimRight(candidate, "/")
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		normalized = append(normalized, candidate)
	}
	return normalized
}

// hasDownloadFilters reports whether host download filters are configured.
func (f *resourceFetcher) hasDownloadFilters() bool {
	return (f.filterMode == "allow_list" || f.filterMode == "block_list") && len(f.filterHosts) > 0
}

// blockedByDownloadFilters applies web_runtime.download_filters host matching
// (same semantics as the browser protocol's request policy).
func (f *resourceFetcher) blockedByDownloadFilters(rawURL string) bool {
	if !f.hasDownloadFilters() {
		return false
	}
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return false
	}
	fullHost := strings.ToLower(parsed.Host)
	hostName := strings.ToLower(parsed.Hostname())
	matched := false
	for _, candidate := range f.filterHosts {
		if candidate == fullHost || candidate == hostName {
			matched = true
			break
		}
	}
	if f.filterMode == "allow_list" {
		return !matched
	}
	return matched
}

// fetchPageResources runs the resource + prefetch waves for one HTML page and
// aggregates the outcome into the parent result (bytes, NetworkEntries, and
// resource_* ProtocolEvidence counters). The caller re-stamps result.Duration.
func (f *resourceFetcher) fetchPageResources(
	ctx context.Context,
	pageURL *neturl.URL,
	body []byte,
	result *protocol.Result,
	pageHeaders http.Header,
	explicit ...ExplicitResource,
) {
	if f == nil || pageURL == nil || result == nil {
		return
	}
	resources, prefetch := extractResourceRefs(body, pageURL)
	if !f.download {
		resources = nil
	}
	if !f.prefetch {
		prefetch = nil
	}
	// Explicit source declarations take precedence over discovered duplicates,
	// including their authored referer and the bounded resource budget.
	discovered := resources
	resources = make([]resourceRef, 0, len(explicit)+len(discovered))
	for _, r := range explicit {
		u, err := neturl.Parse(r.URL)
		if err != nil {
			continue
		}
		resources = append(resources, resourceRef{url: pageURL.ResolveReference(u).String(), resourceType: "other", referer: r.Referer})
	}
	resources = append(resources, discovered...)
	resources, capped := f.filterResourceRefs(resources, pageURL, maxPageResources, nil)
	if len(resources) == 0 && len(prefetch) == 0 {
		return
	}

	stats := &resourceFetchStats{capped: capped}
	cssBodies := f.fetchResourceWave(ctx, resources, pageURL.String(), stats, true, pageHeaders)

	// Second bounded wave: url(...) references discovered in fetched CSS.
	// Depth stays 1 — CSS-derived resources are fetched but never parsed again.
	if f.download && len(cssBodies) > 0 && stats.requested() < maxPageResources {
		var cssRefs []resourceRef
		for _, css := range cssBodies {
			// Resolve url(...) against the stylesheet's own URL, not the page.
			cssBase, err := neturl.Parse(css.baseURL)
			if err != nil {
				continue
			}
			cssRefs = append(cssRefs, extractCSSResourceRefs(css.body, cssBase)...)
		}
		seen := stats.seenURLs()
		var cssCapped bool
		cssRefs, cssCapped = f.filterResourceRefs(cssRefs, pageURL, maxPageResources-stats.requested(), seen)
		stats.markCapped(cssCapped)
		f.fetchResourceWave(ctx, cssRefs, pageURL.String(), stats, false, pageHeaders)
	}

	// Low-priority prefetch/prerender wave, after all page resources. Skipped
	// when the action context is already done.
	if len(prefetch) > 0 && ctx.Err() == nil {
		prefetch, _ = f.filterResourceRefs(prefetch, pageURL, maxPrefetchResources, stats.seenURLs())
		f.fetchResourceWave(ctx, prefetch, pageURL.String(), stats, false, pageHeaders)
	}

	stats.applyTo(result)
	f.h.outcome.applyResources(ctx, result)
}

// filterResourceRefs resolves, dedupes, scheme-checks, filter-checks, and caps
// a wave of refs. seen (may be nil) carries URLs already fetched this page.
// capped reports that a fetchable ref remained when the limit was reached;
// leftover duplicates and filtered refs do not count.
func (f *resourceFetcher) filterResourceRefs(refs []resourceRef, pageURL *neturl.URL, limit int, seen map[string]struct{}) (filtered []resourceRef, capped bool) {
	if len(refs) == 0 {
		return nil, false
	}
	if seen == nil {
		seen = make(map[string]struct{}, len(refs))
	}
	page := pageURL.String()
	filtered = make([]resourceRef, 0, max(0, min(len(refs), limit)))
	for _, ref := range refs {
		if ref.url == "" || ref.url == page {
			continue
		}
		parsed, err := neturl.Parse(ref.url)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		if _, dup := seen[ref.url]; dup {
			continue
		}
		if f.blockedByDownloadFilters(ref.url) {
			continue
		}
		if len(filtered) >= limit {
			return filtered, true
		}
		seen[ref.url] = struct{}{}
		filtered = append(filtered, ref)
	}
	return filtered, false
}

// resourceFetchStats accumulates one page's resource-phase outcome.
type resourceFetchStats struct {
	mu        sync.Mutex
	entries   []protocol.NetworkEntry
	bytes     int64
	requests  int
	failures  int
	cacheHits int
	seen      map[string]struct{}
	capped    bool // the page offered more resources than maxPageResources
}

func (s *resourceFetchStats) record(entry protocol.NetworkEntry, bytes int64, failed, cacheHit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
	s.bytes += bytes
	s.requests++
	if failed {
		s.failures++
	}
	if cacheHit {
		s.cacheHits++
	}
	if s.seen == nil {
		s.seen = make(map[string]struct{})
	}
	s.seen[entry.URL] = struct{}{}
}

func (s *resourceFetchStats) requested() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *resourceFetchStats) seenURLs() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]struct{}, len(s.seen))
	for url := range s.seen {
		seen[url] = struct{}{}
	}
	return seen
}

func (s *resourceFetchStats) applyTo(result *protocol.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests == 0 {
		return
	}
	result.NetworkEntries = append(result.NetworkEntries, s.entries...)
	result.BytesReceived += s.bytes
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence["resource_requests"] = s.requests
	result.ProtocolEvidence["resource_failures"] = s.failures
	result.ProtocolEvidence["resource_cache_hits"] = s.cacheHits
	result.ProtocolEvidence["resource_bytes"] = s.bytes
	if s.capped {
		result.ProtocolEvidence["resource_cap_reached"] = true
	}
}

func (s *resourceFetchStats) markCapped(capped bool) {
	if !capped {
		return
	}
	s.mu.Lock()
	s.capped = true
	s.mu.Unlock()
}

// fetchResourceWave fetches one wave with browser-like concurrency bounds and
// returns the fetched stylesheets (body + own URL) when collectCSS is set.
func (f *resourceFetcher) fetchResourceWave(
	ctx context.Context,
	refs []resourceRef,
	pageURL string,
	stats *resourceFetchStats,
	collectCSS bool,
	pageHeaders http.Header,
) []fetchedCSS {
	if len(refs) == 0 {
		return nil
	}
	var (
		wg        sync.WaitGroup
		hostMu    sync.Mutex
		hostSems  = map[string]chan struct{}{}
		cssMu     sync.Mutex
		cssBodies []fetchedCSS
	)
	hostSemFor := func(rawURL string) chan struct{} {
		host := ""
		if parsed, err := neturl.Parse(rawURL); err == nil {
			host = strings.ToLower(parsed.Host)
		}
		hostMu.Lock()
		defer hostMu.Unlock()
		sem, ok := hostSems[host]
		if !ok {
			sem = make(chan struct{}, maxResourcePerHostConcurrency)
			hostSems[host] = sem
		}
		return sem
	}
	// Fixed worker pool rather than a goroutine per ref: a resource-dense page
	// would otherwise spawn up to maxPageResources+maxPrefetchResources (~80)
	// goroutines per VUser that immediately park on a semaphore. Worker count is
	// the per-VUser in-flight cap, so no separate global semaphore is needed.
	workers := maxResourceConcurrency
	if len(refs) < workers {
		workers = len(refs)
	}
	queue := make(chan resourceRef)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for ref := range queue {
				// Drain without fetching once the action context is done, so the
				// producer never blocks on an abandoned queue.
				if ctx.Err() != nil {
					continue
				}
				// Body in a closure so the per-host slot is released by defer:
				// releasing inline would leak the slot if a fetch panicked, and a
				// bare defer would instead hold every slot until the worker exits.
				func() {
					hostSem := hostSemFor(ref.url)
					select {
					case hostSem <- struct{}{}:
					case <-ctx.Done():
						return
					}
					defer func() { <-hostSem }()

					entry, bytes, css, failed, cacheHit := f.h.fetchResource(ctx, ref, pageURL, pageHeaders)
					stats.record(entry, bytes, failed, cacheHit)
					if collectCSS && css != nil {
						cssMu.Lock()
						if len(cssBodies) < maxResourceCSSRefs {
							cssBodies = append(cssBodies, *css)
						}
						cssMu.Unlock()
					}
				}()
			}
		}()
	}
producer:
	for _, ref := range refs {
		select {
		case queue <- ref:
		case <-ctx.Done():
			break producer
		}
	}
	close(queue)
	wg.Wait()
	return cssBodies
}

// fetchedCSS pairs a fetched stylesheet body with the URL it was served from,
// so url(...) references inside it resolve against the stylesheet — not the
// page that linked it.
type fetchedCSS struct {
	baseURL string
	body    []byte
}

// fetchResource performs one resource GET through the shared client and the
// simulated browser cache. css is non-nil only for a successfully fetched
// text/css response small enough to have been fully buffered.
func (h *HTTP) fetchResource(
	ctx context.Context,
	ref resourceRef,
	pageURL string,
	pageHeaders http.Header,
) (entry protocol.NetworkEntry, bytes int64, css *fetchedCSS, failed, cacheHit bool) {
	entry = protocol.NetworkEntry{FailureKind: "transport", URL: ref.url, Method: http.MethodGet, ResourceType: ref.resourceType}
	var captureRequest, captureResponse http.Header
	var captureBody []byte
	captureComplete := false
	defer func() {
		recordResourceCapture(ctx, entry, captureRequest, captureResponse, captureBody, cacheHit, captureComplete)
		if captureComplete {
			recordResourceResponse(ctx, entry.URL, entry.StatusCode, captureResponse, captureBody)
		}
	}()

	defer func() {
		if !failed {
			entry.FailureKind = ""
		}
	}()
	// Generator-wide admission. Acquired before the clock starts so time spent
	// queueing behind other VUsers never lands in this resource's own DurationMs.
	slots := processResourceSlots()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		entry.FailureKind = resourceFailureKind(ctx.Err())
		failed = true
		return
	}
	defer func() { <-slots }()

	start := time.Now()
	finish := func() {
		entry.DurationMs = float64(time.Since(start)) / float64(time.Millisecond)
		entry.BytesReceived = bytes
	}
	defer finish()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.url, nil)
	if err != nil {
		failed = true
		return
	}
	// Send only the browser-identity headers on resource fetches — NOT the
	// script's arbitrary default_headers, which routinely carry Authorization /
	// API keys that must never leak to third-party asset hosts. Resource fetches
	// intentionally model browser gzip support, so declare that compatibility
	// behavior explicitly and decode it through the bounded response pipeline.
	// Ordinary imported requests never acquire this default.
	req.Header.Set("Accept-Encoding", "gzip")
	if ua := pageHeaders.Get("User-Agent"); ua != "" {
		setRequestHeader(req, "User-Agent", ua)
	}
	if lang := pageHeaders.Get("Accept-Language"); lang != "" {
		setRequestHeader(req, "Accept-Language", lang)
	}
	if ref.referer != nil {
		if *ref.referer != "" {
			req.Header.Set("Referer", *ref.referer)
		}
	} else if referer := refererForResource(pageURL, ref.url); referer != "" {
		req.Header.Set("Referer", referer)
	}

	captureRequest = req.Header
	var staleEntry *httpCacheEntry
	if h.cache != nil {
		if cached, fresh := h.cache.lookup(req, time.Now()); cached != nil {
			if fresh && !h.cacheCheckNewerVersions {
				entry.StatusCode = cached.statusCode
				entry.ResourceType = ref.resourceType + "(cache)"
				cacheHit = true
				captureResponse, captureBody, captureComplete = cached.header, cached.body, true
				observeVerification(ctx, cached.header, cached.body, true, true)
				return
			}
			cached.addConditionalHeaders(req)
			staleEntry = cached
		}
	}

	// Re-check download filters on each redirect hop: the pre-fetch filter check
	// only sees the linked URL, so an allowed host redirecting to a block-listed
	// one would otherwise defeat the filter. (SSRF confinement is already
	// enforced per-hop by the target-policy round-tripper on the transport.)
	clientCopy := *h.client
	baseVerificationRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		observeVerificationRedirect(ctx, r.Response, true)
		if baseVerificationRedirect != nil {
			return baseVerificationRedirect(r, via)
		}
		return nil
	}
	client := &clientCopy
	if h.resources != nil && h.resources.hasDownloadFilters() {
		clone := *client
		base := clone.CheckRedirect
		clone.CheckRedirect = func(rr *http.Request, via []*http.Request) error {
			if h.resources.blockedByDownloadFilters(rr.URL.String()) {
				return fmt.Errorf("resource redirect to filtered host blocked: %s", rr.URL.Host)
			}
			if base != nil {
				return base(rr, via)
			}
			return nil
		}
		client = &clone
	}

	resp, err := client.Do(req)
	if err != nil {
		entry.FailureKind = resourceFailureKind(err)
		failed = true
		return
	}
	defer resp.Body.Close()
	captureResponse = resp.Header
	wireHeaders := resp.Header.Clone()
	responseView := *resp
	responseView.Header = resp.Header.Clone()

	if staleEntry != nil && resp.StatusCode == http.StatusNotModified {
		h.cache.refresh(req, staleEntry, resp, time.Now())
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		entry.StatusCode = http.StatusNotModified
		entry.ResourceType = ref.resourceType + "(revalidated)"
		cacheHit = true
		captureResponse, captureBody, captureComplete = staleEntry.header, staleEntry.body, true
		observeVerification(ctx, staleEntry.header, staleEntry.body, true, true)
		return
	}

	entry.StatusCode = resp.StatusCode
	// Validate the whole finite decoded stream. Retain only the cache-sized
	// logical representation; larger valid resources remain network successes
	// without claiming complete snapshot/cache/CSS input.
	var captured bytebuffer.Buffer
	chunkSize := 32 * 1024
	if h.receiveBufferBytes > 0 {
		chunkSize = h.receiveBufferBytes
	}
	receiveLease := processHTTPResponseCaptureBudget.lease()
	defer receiveLease.release()
	if !receiveLease.reserve(int64(chunkSize)) {
		entry.FailureKind = "body_limit"
		failed = true
		return
	}
	encodings := resp.Header.Values("Content-Encoding")
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		encodings = nil
	}
	decoder, decodeErr := contentdecode.NewReader(ctx, resp.Body, encodings, contentdecode.DefaultLimits(maxResourceCountedBytes))
	if decodeErr != nil {
		entry.FailureKind = "content_decode"
		failed = true
		return
	}
	defer decoder.Close()
	chunk := make([]byte, chunkSize)
	for {
		n, readErr := decoder.Read(chunk)
		if n > 0 {
			remaining := httpCacheMaxBodyBytes - captured.Len()
			retain := n
			if retain > remaining {
				retain = remaining
			}
			if retain > 0 {
				if _, writeErr := (&responseCaptureWriter{buffer: &captured, lease: receiveLease}).Write(chunk[:retain]); writeErr != nil {
					entry.FailureKind = "body_limit"
					failed = true
					break
				}
			}
		}
		if readErr != nil {
			if readErr != io.EOF || !decoder.Complete() {
				entry.FailureKind = "content_decode"
				failed = true
			}
			break
		}
	}
	bytes = decoder.EncodedBytes()
	logicalSize := decoder.DecodedBytes()
	buffered := captured.Bytes()
	if !failed && decoder.Decoding() {
		responseView.Header.Del("Content-Encoding")
		responseView.Header.Del("Content-Length")
		responseView.ContentLength = -1
	}
	if failed {
		buffered = nil
	}
	captureResponse = responseView.Header
	captureBody, captureComplete = buffered, !failed && logicalSize <= int64(httpCacheMaxBodyBytes)
	observeVerification(ctx, wireHeaders, buffered, true, !failed && logicalSize <= int64(httpCacheMaxBodyBytes))
	if resp.StatusCode >= 400 {
		if !failed {
			entry.FailureKind = "http"
		}
		failed = true
	}

	if !failed && h.cache != nil && logicalSize <= int64(httpCacheMaxBodyBytes) {
		if !h.cacheContentURLsOnly || isContentBearingResponse(resp.Header) {
			h.cache.store(req, &responseView, buffered, time.Now())
		}
	}
	if !failed && logicalSize <= int64(httpCacheMaxBodyBytes) {
		if mediaType, _, mimeErr := mime.ParseMediaType(resp.Header.Get("Content-Type")); mimeErr == nil && mediaType == "text/css" {
			cssBase := ref.url
			if resp.Request != nil && resp.Request.URL != nil {
				cssBase = resp.Request.URL.String()
			}
			css = &fetchedCSS{baseURL: cssBase, body: buffered}
		}
	}
	return
}

// refererForResource returns the Referer a browser would send when fetching
// resourceURL from pageURL, following the modern default referrer policy
// (strict-origin-when-cross-origin):
//
//   - same origin      -> full URL, minus userinfo and fragment
//   - cross origin     -> origin only, so page paths and query strings (which
//     routinely carry session ids and tokens) never reach third-party asset
//     hosts
//   - https -> http    -> no Referer at all
//
// Sending the full page URL everywhere, as the naive version did, leaks
// in-URL credentials to every CDN the page references.
func refererForResource(pageURL, resourceURL string) string {
	if pageURL == "" {
		return ""
	}
	page, err := neturl.Parse(pageURL)
	if err != nil || page.Scheme == "" || page.Host == "" {
		return ""
	}
	page.User = nil
	page.Fragment = ""

	target, err := neturl.Parse(resourceURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return ""
	}
	if strings.EqualFold(page.Scheme, "https") && strings.EqualFold(target.Scheme, "http") {
		return "" // never downgrade
	}
	if strings.EqualFold(page.Scheme, target.Scheme) && strings.EqualFold(page.Host, target.Host) {
		return page.String()
	}
	return page.Scheme + "://" + page.Host + "/"
}

// maxExtractedRefs caps how many raw refs the tokenizer collects before the
// per-page fetch cap is applied, so a pathologically resource-dense page cannot
// allocate unbounded URL strings per VUser. It is a safety valve well above the
// fetch cap (resources + prefetch) so normal pages are unaffected.
const maxExtractedRefs = 4096

// extractResourceRefs tokenizes an HTML document and returns embedded page
// resources plus prefetch/prerender/preload hints, in document order. URLs are
// resolved against base; unresolvable or non-http(s) refs are dropped later by
// filterResourceRefs.
func extractResourceRefs(body []byte, base *neturl.URL) (resources, prefetch []resourceRef) {
	// bytebuffer.NewReader, not strings.NewReader(string(body)): the string conversion
	// copied every HTML body on a per-page, per-VUser path.
	tokenizer := html.NewTokenizer(bytebuffer.NewReader(body))
	resolve := func(raw string) string {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(strings.ToLower(raw), "data:") || strings.HasPrefix(strings.ToLower(raw), "javascript:") {
			return ""
		}
		resolved, err := base.Parse(raw)
		if err != nil {
			return ""
		}
		resolved.Fragment = ""
		return resolved.String()
	}
	addResource := func(raw, resourceType string) {
		if url := resolve(raw); url != "" {
			resources = append(resources, resourceRef{url: url, resourceType: resourceType})
		}
	}
	addPrefetch := func(raw, resourceType string) {
		if url := resolve(raw); url != "" {
			prefetch = append(prefetch, resourceRef{url: url, resourceType: resourceType})
		}
	}
	for {
		if len(resources)+len(prefetch) >= maxExtractedRefs {
			return
		}
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			return
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		attrs := map[string]string{}
		for _, attr := range token.Attr {
			attrs[strings.ToLower(attr.Key)] = attr.Val
		}
		switch token.Data {
		case "img":
			addResource(attrs["src"], "image")
			for _, candidate := range srcsetURLs(attrs["srcset"]) {
				addResource(candidate, "image")
			}
		case "input":
			if strings.EqualFold(attrs["type"], "image") {
				addResource(attrs["src"], "image")
			}
		case "script":
			addResource(attrs["src"], "script")
		case "link":
			rels := strings.Fields(strings.ToLower(attrs["rel"]))
			href := attrs["href"]
			for _, rel := range rels {
				switch rel {
				case "stylesheet":
					addResource(href, "stylesheet")
				case "icon", "shortcut", "apple-touch-icon":
					if rel == "shortcut" {
						continue // handled via the companion "icon" rel token
					}
					addResource(href, "icon")
				case "prefetch":
					addPrefetch(href, "prefetch")
				case "prerender":
					addPrefetch(href, "prerender")
				case "preload":
					addPrefetch(href, "preload")
				}
			}
		case "source":
			addResource(attrs["src"], "media")
			for _, candidate := range srcsetURLs(attrs["srcset"]) {
				addResource(candidate, "media")
			}
		case "video", "audio":
			addResource(attrs["src"], "media")
			if token.Data == "video" {
				addResource(attrs["poster"], "image")
			}
		case "embed":
			addResource(attrs["src"], "other")
		case "object":
			addResource(attrs["data"], "other")
		case "iframe", "frame":
			// Fetched as a document but never parsed: extraction depth stays 1.
			addResource(attrs["src"], "document")
		case "body":
			addResource(attrs["background"], "image")
		}
	}
}

// srcsetURLs returns the URL of each srcset candidate ("url [descriptor]"
// entries separated by commas).
func srcsetURLs(srcset string) []string {
	if strings.TrimSpace(srcset) == "" {
		return nil
	}
	var urls []string
	for _, candidate := range strings.Split(srcset, ",") {
		fields := strings.Fields(strings.TrimSpace(candidate))
		if len(fields) > 0 {
			urls = append(urls, fields[0])
		}
	}
	return urls
}

// extractCSSResourceRefs pulls url(...) references out of a stylesheet body.
func extractCSSResourceRefs(cssBody []byte, base *neturl.URL) []resourceRef {
	var refs []resourceRef
	css := string(cssBody)
	for len(refs) < maxResourceCSSRefs {
		idx := strings.Index(css, "url(")
		if idx < 0 {
			break
		}
		css = css[idx+len("url("):]
		end := strings.Index(css, ")")
		if end < 0 {
			break
		}
		raw := strings.TrimSpace(css[:end])
		css = css[end+1:]
		raw = strings.Trim(raw, `'"`)
		if raw == "" || strings.HasPrefix(strings.ToLower(raw), "data:") {
			continue
		}
		resolved, err := base.Parse(raw)
		if err != nil {
			continue
		}
		resolved.Fragment = ""
		refs = append(refs, resourceRef{url: resolved.String(), resourceType: "css-resource"})
	}
	return refs
}
