package httpcontract

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func validateAuthType(authType string) error {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "", "none", "noauth", "inherit", "basic", "bearer", "digest", "ntlm", "negotiate", "negotiate_any":
		return nil
	case "kerberos":
		return fmt.Errorf("unsupported HTTP auth type %q: Kerberos tickets are not supported; use negotiate (Negotiate with NTLM) or an external auth flow", authType)
	default:
		return fmt.Errorf("unsupported HTTP auth type %q", authType)
	}
}

func ExactConfigInteger(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case int32:
		return int64(typed), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(typed), nil
	case float32:
		converted := float64(typed)
		if math.IsNaN(converted) || math.IsInf(converted, 0) || math.Trunc(converted) != converted {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("not an integer")
	}
}

func ResolveNetworkProfile(mode, preset string, latencyMS, bandwidthKbps int) (int, int) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "preset":
		switch strings.ToLower(strings.TrimSpace(preset)) {
		case "56k":
			return 120, 56
		case "dsl":
			return 50, 1536
		case "t1":
			return 20, 1544
		case "10mbps":
			return 5, 10240
		}
	case "custom":
		return latencyMS, bandwidthKbps
	}
	return 0, 0
}

func DefaultHeaders(config map[string]any) map[string]string {
	headers := make(map[string]string)
	if values, ok := config["default_headers"].(map[string]string); ok {
		for key, value := range values {
			headers[key] = value
		}
	}
	AddTypedHeaderUnlessOverridden(headers, "User-Agent", config["user_agent"])
	AddTypedHeaderUnlessOverridden(headers, "Accept-Encoding", config["accept_encoding"])
	AddTypedHeaderUnlessOverridden(headers, "Accept-Language", config["accept_language"])
	return headers
}

func TypedDefaultHeaders(config map[string]any) map[string]string {
	headers := make(map[string]string)
	AddTypedHeaderUnlessOverridden(headers, "User-Agent", config["user_agent"])
	AddTypedHeaderUnlessOverridden(headers, "Accept-Encoding", config["accept_encoding"])
	AddTypedHeaderUnlessOverridden(headers, "Accept-Language", config["accept_language"])
	return headers
}

func MergeDefaultHeaders(typed, authored map[string]string) map[string]string {
	headers := make(map[string]string, len(typed)+len(authored))
	for name, value := range authored {
		headers[name] = value
	}
	for name, value := range typed {
		AddTypedHeaderUnlessOverridden(headers, name, value)
	}
	return headers
}

func AddTypedHeaderUnlessOverridden(headers map[string]string, name string, raw any) {
	value, ok := raw.(string)
	if !ok || value == "" {
		return
	}
	for existing := range headers {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return
		}
	}
	headers[name] = value
}
