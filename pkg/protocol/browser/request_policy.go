package browser

import (
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	playwright "github.com/playwright-community/playwright-go"
)

const (
	maxBrowserResourceValidators = 1024
	maxBrowserResourceURLBytes   = browserNetworkURLLimitBytes
	maxBrowserResourceETagBytes  = 8 << 10
)

func normalizeRecordedResources(urls []string) map[string]struct{} {
	normalized := make(map[string]struct{})
	for _, rawURL := range urls {
		for _, key := range recordedResourceKeys(rawURL) {
			normalized[key] = struct{}{}
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func recordedResourceKeys(rawURL string) []string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if parsed.Host == "" {
		return []string{path}
	}
	return []string{strings.ToLower(parsed.Host) + path, path}
}

func normalizeDownloadFilterHosts(hosts []string) []string {
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

func (b *Browser) hasDownloadFilters() bool {
	return (b.downloadFilterMode == "allow_list" || b.downloadFilterMode == "block_list") && len(b.downloadFilterHosts) > 0
}

func (b *Browser) shouldBlockFilteredRequest(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	fullHost := strings.ToLower(parsed.Host)
	hostName := strings.ToLower(parsed.Hostname())
	matched := false
	for _, allowedHost := range b.downloadFilterHosts {
		if allowedHost == fullHost || allowedHost == hostName {
			matched = true
			break
		}
	}
	switch b.downloadFilterMode {
	case "allow_list":
		return !matched
	case "block_list":
		return matched
	default:
		return false
	}
}

func (b *Browser) shouldBlockUnrecordedRequest(rawURL string) bool {
	if b.downloadResources != "recorded" || !b.recordedResourcesAvailable {
		return false
	}
	if len(b.recordedResources) == 0 {
		return true
	}
	for _, key := range recordedResourceKeys(rawURL) {
		if _, ok := b.recordedResources[key]; ok {
			return false
		}
	}
	return true
}

func (b *Browser) installRequestPolicy(browserContext playwright.BrowserContext) error {
	if !b.requestPolicyEnabled() {
		return nil
	}
	return browserContext.Route("**/*", func(route playwright.Route) {
		request := route.Request()
		if b.shouldBlockRequest(request) {
			_ = route.Abort()
			return
		}
		if b.routeNetworkLatencyEnabled() {
			if !b.waitRouteNetworkLatency() {
				_ = route.Abort()
				return
			}
		}

		headers := b.requestPolicyHeaders(request)
		if len(headers) > 0 {
			_ = route.Continue(playwright.RouteContinueOptions{Headers: headers})
			return
		}
		_ = route.Continue()
	})
}

func (b *Browser) waitRouteNetworkLatency() bool {
	delay := time.Duration(b.networkLatencyMS) * time.Millisecond
	if delay <= 0 {
		return true
	}
	b.mu.Lock()
	stop := b.requestPolicyStop
	closing := b.closing
	b.mu.Unlock()
	if closing || stop == nil {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stop:
		return false
	}
}

func (b *Browser) requestPolicyEnabled() bool {
	return !b.cacheEnabled ||
		b.cacheContentURLRevalidationEnabled() ||
		b.checkNewerVersions ||
		b.downloadResources != "all" ||
		b.hasDownloadFilters() ||
		b.routeNetworkLatencyEnabled()
}

func (b *Browser) routeNetworkLatencyEnabled() bool {
	return b.networkLatencyMS > 0 && !b.supportsCDP()
}

func (b *Browser) shouldBlockRequest(request playwright.Request) bool {
	if request == nil {
		return false
	}
	return b.shouldBlockRequestInfo(request.URL(), request.ResourceType(), request.IsNavigationRequest())
}

func (b *Browser) shouldBlockRequestInfo(rawURL, resourceType string, isNavigation bool) bool {
	if isNavigation || resourceType == "document" {
		return false
	}
	if b.downloadResources == "none" && isBrowserStaticResourceType(resourceType) {
		return true
	}
	if isBrowserStaticResourceType(resourceType) && b.shouldBlockUnrecordedRequest(rawURL) {
		return true
	}
	if b.hasDownloadFilters() && isBrowserStaticResourceType(resourceType) && b.shouldBlockFilteredRequest(rawURL) {
		return true
	}
	return false
}

func (b *Browser) requestPolicyHeaders(request playwright.Request) map[string]string {
	if request == nil {
		return nil
	}
	return b.requestPolicyHeadersFor(request.URL(), request.ResourceType(), request.IsNavigationRequest(), request.Headers())
}

func (b *Browser) requestPolicyHeadersFor(rawURL, resourceType string, isNavigation bool, headers map[string]string) map[string]string {
	revalidateContentURL := b.cacheContentURLRevalidationEnabled() && shouldRevalidateBrowserRequestInfo(resourceType, isNavigation)
	if b.cacheEnabled && !b.checkNewerVersions && !revalidateContentURL {
		return nil
	}
	nextHeaders := cloneRequestHeaders(headers)
	if !b.cacheEnabled || b.checkNewerVersions || revalidateContentURL {
		nextHeaders["Cache-Control"] = "no-cache"
		nextHeaders["Pragma"] = "no-cache"
	}
	if b.checkNewerVersions && shouldRevalidateBrowserRequestInfo(resourceType, isNavigation) {
		if etag := b.resourceValidator(rawURL); etag != "" {
			nextHeaders["If-None-Match"] = etag
		}
	}
	return nextHeaders
}

func (b *Browser) cacheContentURLRevalidationEnabled() bool {
	return b.cacheEnabled && b.cacheContentURLsConfigured && !b.cacheContentURLs
}

func shouldRevalidateBrowserRequest(request playwright.Request) bool {
	if request == nil {
		return false
	}
	return shouldRevalidateBrowserRequestInfo(request.ResourceType(), request.IsNavigationRequest())
}

func shouldRevalidateBrowserRequestInfo(resourceType string, isNavigation bool) bool {
	if isNavigation {
		return false
	}
	return isBrowserStaticResourceType(resourceType)
}

func isBrowserStaticResourceType(resourceType string) bool {
	switch resourceType {
	case "stylesheet", "script", "image", "media", "font", "manifest", "texttrack":
		return true
	default:
		return false
	}
}

func cloneRequestHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(headers)+3)
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func (b *Browser) rememberResourceValidator(rawURL string, headers map[string]string) {
	rawURL = boundedtext.TruncateUTF8(strings.TrimSpace(rawURL), maxBrowserResourceURLBytes)
	etag := boundedtext.TruncateUTF8(headerValue(headers, "etag"), maxBrowserResourceETagBytes)
	if rawURL == "" || etag == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.resourceValidators == nil {
		b.resourceValidators = make(map[string]string)
	}
	if _, exists := b.resourceValidators[rawURL]; !exists {
		b.resourceValidatorKeys = append(b.resourceValidatorKeys, rawURL)
	}
	b.resourceValidators[rawURL] = etag
	for len(b.resourceValidatorKeys) > maxBrowserResourceValidators {
		evict := b.resourceValidatorKeys[0]
		copy(b.resourceValidatorKeys, b.resourceValidatorKeys[1:])
		b.resourceValidatorKeys = b.resourceValidatorKeys[:len(b.resourceValidatorKeys)-1]
		delete(b.resourceValidators, evict)
	}
}

func (b *Browser) resourceValidator(rawURL string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resourceValidators[rawURL]
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
