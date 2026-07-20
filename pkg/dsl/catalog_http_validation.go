package dsl

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func validateHTTPSequenceActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	requests, ok := action.Params["requests"]
	if !ok {
		return []string{fmt.Sprintf("%s: http_sequence requires params.requests", label)}
	}
	switch typed := requests.(type) {
	case []any:
		if len(typed) == 0 {
			issues = append(issues, fmt.Sprintf("%s: http_sequence requires at least one child request", label))
		}
		for requestIndex, value := range typed {
			request, ok := value.(map[string]any)
			if !ok {
				issues = append(issues, fmt.Sprintf("%s.params.requests[%d]: must be an object", label, requestIndex))
				continue
			}
			issues = append(issues, validateHTTPSequenceRequestIssues(label, requestIndex, request)...)
		}
	case []map[string]any:
		// In-memory form produced by importers before JSON serialization.
		if len(typed) == 0 {
			issues = append(issues, fmt.Sprintf("%s: http_sequence requires at least one child request", label))
		}
		for requestIndex, request := range typed {
			issues = append(issues, validateHTTPSequenceRequestIssues(label, requestIndex, request)...)
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: http_sequence params.requests must be an array", label))
	}
	return issues
}

func validateHTTPSequenceRequestIssues(label string, requestIndex int, request map[string]any) []string {
	var issues []string
	name := stringAnyValue(request["name"])
	method := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(stringAnyValue(request["method"]), stringAnyValue(request["type"]))))
	target := stringAnyValue(request["target"])
	childLabel := fmt.Sprintf("%s.params.requests[%d] %q", label, requestIndex, name)
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported HTTP method/type %q", childLabel, method))
	}
	if strings.TrimSpace(target) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing target", childLabel))
	}
	// Child requests support body_file the same way top-level actions do; the
	// engine decodes each child into a dsl.Action whose HTTP protocol uses only
	// the inline body, so an unresolved body_file would silently send an empty
	// body. Apply the same conflict + load-context checks as validateBodyFileIssues.
	issues = append(issues, validateSequenceChildBodyFileIssues(childLabel, request)...)
	// Child requests carry full extractor objects (the importer's primary
	// output shape); run the same field checks as top-level actions so typos
	// fail validation instead of silently misbehaving at runtime. The JSON
	// round-trip uniformly handles the wire form ([]any of maps) and the
	// importer's in-memory form ([]Extractor).
	if raw, ok := request["extractors"]; ok && raw != nil {
		if payload, err := json.Marshal(raw); err == nil {
			var extractors []Extractor
			if json.Unmarshal(payload, &extractors) == nil {
				// http_sequence child requests are HTTP by construction.
				issues = append(issues, validateExtractorIssues("http", childLabel, Action{Extractors: extractors})...)
			}
		}
	}
	return issues
}

// validateSequenceChildBodyFileIssues mirrors validateBodyFileIssues for the
// untyped http_sequence child-request map form. body_file is only resolvable
// when loading scripts from a local file (see ResolveBodyRefs); API uploads
// must inline the body. Rejecting it here prevents the engine from silently
// sending an empty child body at runtime.
func validateSequenceChildBodyFileIssues(childLabel string, request map[string]any) []string {
	bodyFile := strings.TrimSpace(stringAnyValue(request["body_file"]))
	if bodyFile == "" {
		return nil
	}
	issues := make([]string, 0, 2)
	if strings.TrimSpace(stringAnyValue(request["body"])) != "" {
		issues = append(issues, fmt.Sprintf("%s: body and body_file cannot both be set", childLabel))
	}
	issues = append(issues, fmt.Sprintf("%s: body_file is only supported when loading scripts from a local file; inline body is required for API uploads", childLabel))
	return issues
}

func stringAnyValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func validateHTTPActionBoundsIssues(path string, action Action) []string {
	label := actionLabel(path, action)
	maxTimeoutMS := int(httpprotocol.MaxConfiguredTimeout / time.Millisecond)
	var issues []string
	if action.TimeoutMs < 0 || action.TimeoutMs > maxTimeoutMS {
		issues = append(issues, fmt.Sprintf("%s: timeout_ms must be between 0 and %d", label, maxTimeoutMS))
	}
	if action.MaxRedirects != nil && (*action.MaxRedirects < -1 || *action.MaxRedirects > httpprotocol.MaxConfiguredRedirects) {
		issues = append(issues, fmt.Sprintf("%s: max_redirects must be between -1 and %d", label, httpprotocol.MaxConfiguredRedirects))
	}
	if raw, present := action.Params["stream_timeout_ms"]; present {
		value, ok := actionParamIntValue(action.Params, "stream_timeout_ms")
		if !ok || value < 0 || value > maxTimeoutMS {
			issues = append(issues, fmt.Sprintf("%s: params.stream_timeout_ms must be an integer between 0 and %d (got %v)", label, maxTimeoutMS, raw))
		}
	}
	return issues
}
