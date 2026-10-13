package multiproject

// Pure helpers the integration suite uses. They carry no build tag, so their
// unit tests (helpers_unit_test.go) run in the ordinary `go test ./...`.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func envBool(raw string) bool {
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type apiError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, truncate(e.Body, 600))
}

func statusOf(err error) int {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// retryableUnavailable recognises the 503s that precede any mutation: the
// router has no admitted leader (its body is not the API's JSON), a follower
// refused an execution mutation, or start admission asked for a retry.
func retryableUnavailable(raw []byte) bool {
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Error == "" {
		return true
	}
	return body.Code == "start_admission_temporarily_unavailable" ||
		strings.Contains(body.Error, "not the active execution leader")
}

func allRunning(running map[string]bool, ids []string) bool {
	for _, id := range ids {
		if !running[id] {
			return false
		}
	}
	return true
}

// reportRequestCounts reads the request population: metric_populations.action
// counts requests only, which is what the target receives.
func reportRequestCounts(report map[string]any) (*int64, *int64, []string) {
	var requests, errorsCount *int64
	if populations, ok := report["metric_populations"].(map[string]any); ok {
		if action, ok := populations["action"].(map[string]any); ok {
			if value, ok := action["transactions"].(float64); ok {
				v := int64(value)
				requests = &v
			}
			if value, ok := action["errors"].(float64); ok {
				v := int64(value)
				errorsCount = &v
			}
		}
	}
	var names []string
	for _, key := range []string{"by_request", "by_action"} {
		if group, ok := report[key].(map[string]any); ok {
			for name := range group {
				names = append(names, name)
			}
		}
	}
	if requests == nil {
		if group, ok := report["by_request"].(map[string]any); ok && len(group) > 0 {
			var total, failed int64
			for _, raw := range group {
				stats, _ := raw.(map[string]any)
				tx, _ := stats["transactions"].(float64)
				errs, _ := stats["errors"].(float64)
				total += int64(tx)
				failed += int64(errs)
			}
			requests, errorsCount = &total, &failed
		}
	}
	sort.Strings(names)
	return requests, errorsCount, names
}
