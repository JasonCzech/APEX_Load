package http

import (
	"fmt"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func evaluate(a protocol.Assertion, result *protocol.Result, body []byte) error {
	switch a.Type {
	case "status_code":
		expected, ok := toInt(a.Expected)
		if !ok {
			return fmt.Errorf("status_code assertion: expected integer, got %v", a.Expected)
		}
		if result.StatusCode != expected {
			return fmt.Errorf("expected status %d, got %d", expected, result.StatusCode)
		}

	case "response_time_ms":
		if result.Duration.Milliseconds() > int64(a.Max) {
			return fmt.Errorf("response time %dms exceeded max %dms", result.Duration.Milliseconds(), a.Max)
		}

	case "body_contains":
		if !strings.Contains(string(body), a.Value) {
			return fmt.Errorf("response body does not contain %q", a.Value)
		}

	case "body_not_contains":
		if strings.Contains(string(body), a.Value) {
			return fmt.Errorf("response body contains %q (unexpected)", a.Value)
		}

	default:
		return fmt.Errorf("unsupported assertion type: %s", a.Type)
	}
	return nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}
