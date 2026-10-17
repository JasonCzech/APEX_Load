package httpcontract

import (
	"fmt"
	"mime"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func ValidateReplayCompatibilityConfig(config map[string]any) error {
	for key, max := range map[string]int64{"receive_buffer_bytes": 1048576, "max_meta_refresh": 50, "content_check_error_limit": 1000} {
		if raw := config[key]; raw != nil {
			n, err := ExactConfigInteger(raw)
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
		value, err := ExactConfigInteger(raw)
		if err != nil || value < 0 || value > MaxCacheUnusedIterations {
			return fmt.Errorf("cache_max_unused_iterations must be an integer between 0 and %d", MaxCacheUnusedIterations)
		}
	}
	if _, err := protocol.BoundedMilliseconds(config["page_timeout_ms"], 0, MaxConfiguredTimeout); err != nil {
		return fmt.Errorf("page_timeout_ms %w", err)
	}
	for _, key := range []string{"cache_excluded_mime_types", "html_excluded_mime_types"} {
		if _, err := CompatibilityMIMETypes(config[key]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

func CompatibilityMIMETypes(raw any) ([]string, error) {
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
