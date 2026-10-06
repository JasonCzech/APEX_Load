package http

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const MaxCacheUnusedIterations = 1_000_000

var errPageDeadline = errors.New("page download deadline exceeded")

// ValidateReplayCompatibilityConfig is shared by authored DSL and direct protocol callers.
func ValidateReplayCompatibilityConfig(config map[string]any) error {
	for key, max := range map[string]int64{"receive_buffer_bytes": 1048576, "max_meta_refresh": 50, "content_check_error_limit": 1000} {
		if raw := config[key]; raw != nil {
			n, err := exactHTTPConfigInteger(raw)
			if err != nil || n < 0 || n > max {
				return fmt.Errorf("%s must be an integer between 0 and %d", key, max)
			}
		}
	}
	for _, field := range []struct {
		name    string
		allowed []string
	}{
		{"ip_version", []string{"", "auto", "ipv4", "ipv6"}},
		{"html_parse_mode", []string{"", "html", "text", "any"}},
		{"aura_recorded_failures", AuraRecordedFailurePolicies},
	} {
		raw := config[field.name]
		if raw == nil {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", field.name)
		}
		valid := false
		for _, allowed := range field.allowed {
			valid = valid || strings.ToLower(strings.TrimSpace(value)) == allowed
		}
		if !valid {
			return fmt.Errorf("%s has an unsupported value", field.name)
		}
	}
	if raw := config["cache_max_unused_iterations"]; raw != nil {
		value, err := exactHTTPConfigInteger(raw)
		if err != nil || value < 0 || value > MaxCacheUnusedIterations {
			return fmt.Errorf("cache_max_unused_iterations must be an integer between 0 and %d", MaxCacheUnusedIterations)
		}
	}
	if _, err := protocol.BoundedMilliseconds(config["page_timeout_ms"], 0, MaxConfiguredTimeout); err != nil {
		return fmt.Errorf("page_timeout_ms %w", err)
	}
	for _, key := range []string{"cache_excluded_mime_types", "html_excluded_mime_types"} {
		if _, err := compatibilityMIMETypes(config[key]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

func compatibilityMIMETypes(raw any) ([]string, error) {
	var values []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		values = v
	case []any:
		for _, value := range v {
			s, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("expected MIME strings")
			}
			values = append(values, s)
		}
	default:
		return nil, fmt.Errorf("expected a MIME list")
	}
	if len(values) > 64 {
		return nil, fmt.Errorf("at most 64 MIME types are supported")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		mediaType, params, err := mime.ParseMediaType(value)
		if err != nil || len(value) > 128 || len(params) != 0 || !strings.Contains(mediaType, "/") || strings.Contains(mediaType, "*") {
			return nil, fmt.Errorf("expected exact MIME types without parameters or wildcards")
		}
		result = append(result, mediaType)
	}
	return result, nil
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
// Init entries have iteration zero and survive the first main iteration.
func (h *HTTP) BeginIteration(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
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
