package http

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

const MaxCacheUnusedIterations = httpcontract.MaxCacheUnusedIterations

var errPageDeadline = errors.New("page download deadline exceeded")

// ValidateReplayCompatibilityConfig is shared by authored DSL and direct protocol callers.
func ValidateReplayCompatibilityConfig(config map[string]any) error {
	return httpcontract.ValidateReplayCompatibilityConfig(config)
}

func compatibilityMIMETypes(raw any) ([]string, error) {
	return httpcontract.CompatibilityMIMETypes(raw)
}

func matchesCompatibilityMIME(headers http.Header, types []string) bool {
	mediaType, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil {
		return false
	}
	for _, value := range types {
		if strings.EqualFold(mediaType, value) {
			return true
		}
	}
	return false
}

func (h *HTTP) shouldParseHTML(headers http.Header) bool {
	if matchesCompatibilityMIME(headers, h.htmlExcludedMIMETypes) {
		return false
	}
	mediaType, _, _ := mime.ParseMediaType(headers.Get("Content-Type"))
	switch h.htmlParseMode {
	case "any":
		return true
	case "text":
		return strings.HasPrefix(strings.ToLower(mediaType), "text/") && !strings.EqualFold(mediaType, "text/xml")
	case "html":
		return strings.EqualFold(mediaType, "text/html")
	default:
		return isContentBearingResponse(headers)
	}
}

// Restrict the dialer's lookup/connect family, retaining its DNS, timeout,
// local-address, proxy, and cancellation behavior. Auto keeps Go's fallback.
func familyDialContext(base func(context.Context, string, string) (net.Conn, error), family string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network == "tcp" {
			switch family {
			case "ipv4":
				network = "tcp4"
			case "ipv6":
				network = "tcp6"
			}
		}
		return base(ctx, network, address)
	}
}

// BeginIteration ages cached entries by completed, unreferenced main iterations.
// Init entries have iteration zero and survive the first main iteration. With
// alt_svc_cache: per_iteration it also forgets the auto+h3 alternatives.
func (h *HTTP) BeginIteration(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.altSvc != nil && h.quicSettings.AltSvcCache == QUICAltSvcCachePerIteration {
		h.altSvc.resetAlternatives()
	}
	if h.cache == nil {
		return nil
	}
	c := h.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	c.iteration++
	if h.cacheMaxUnusedIterations <= 0 {
		return nil
	}
	for key, entry := range c.entries {
		if c.iteration-entry.lastUsedIteration > uint64(h.cacheMaxUnusedIterations) {
			size := entry.size()
			delete(c.entries, key)
			c.totalBytes -= size
			c.memory.releaseBytes(size)
		}
	}
	return nil
}
