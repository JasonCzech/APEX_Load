package dsl

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

func validateHTTPSequenceActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	mode, _, settingsErr := HTTPSequenceExecutionSettings(action)
	if settingsErr != nil {
		issues = append(issues, fmt.Sprintf("%s: %v", label, settingsErr))
		return issues
	}
	if mode == HTTPSequenceConcurrent && httpSequenceHasHTMLStep(action.Params["requests"]) {
		// Concurrent children cannot each resolve against "the current page".
		issues = append(issues, fmt.Sprintf("%s: HTML-mode steps (params.html_step) cannot run in a concurrent http_sequence", label))
	}
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
			request, ok := httpSequenceRequestMap(value)
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
	case []Action:
		if len(typed) == 0 {
			issues = append(issues, fmt.Sprintf("%s: http_sequence requires at least one child request", label))
		}
		for requestIndex, requestAction := range typed {
			request, ok := httpSequenceRequestMap(requestAction)
			if !ok {
				issues = append(issues, fmt.Sprintf("%s.params.requests[%d]: must be an object", label, requestIndex))
				continue
			}
			issues = append(issues, validateHTTPSequenceRequestIssues(label, requestIndex, request)...)
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: http_sequence params.requests must be an array", label))
	}
	if mode == HTTPSequenceConcurrent && settingsErr == nil {
		seen := make(map[string]bool)
		for requestIndex, value := range httpSequenceRawRequests(requests) {
			request, ok := httpSequenceRequestMap(value)
			if !ok {
				continue
			}
			payload, err := json.Marshal(request)
			var child Action
			if err != nil || json.Unmarshal(payload, &child) != nil {
				continue
			}
			if err := ValidateHTTPConcurrentChild(child); err != nil {
				issues = append(issues, fmt.Sprintf("%s.params.requests[%d]: %v", label, requestIndex, err))
			}
			for _, extractor := range child.Extractors {
				if seen[extractor.Name] {
					issues = append(issues, fmt.Sprintf("%s: concurrent requests require unique extractor names", label))
					break
				}
				seen[extractor.Name] = true
			}
		}
	}
	return issues
}

func httpSequenceRequestMap(value any) (map[string]any, bool) {
	if request, ok := value.(map[string]any); ok {
		return request, request != nil
	}
	switch typed := value.(type) {
	case Action:
		value = typed
	case *Action:
		if typed == nil {
			return nil, false
		}
		value = typed
	default:
		return nil, false
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil || request == nil {
		return nil, false
	}
	return request, true
}

func validateHTTPSequenceRequestIssues(label string, requestIndex int, request map[string]any) []string {
	if strings.EqualFold(strings.TrimSpace(stringAnyValue(request["type"])), "bruno_run") {
		return []string{fmt.Sprintf("%s.params.requests[%d]: %s", label, requestIndex, BrunoRunnerRetiredMessage)}
	}
	var issues []string
	if params, ok := request["params"].(map[string]any); ok {
		if step, ok := params[httpprotocol.HTMLStepParam]; ok {
			issues = append(issues, validateHTMLStepIssues(fmt.Sprintf("%s.params.requests[%d]", label, requestIndex), "http", step)...)
		}
	}
	name := stringAnyValue(request["name"])
	method := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(stringAnyValue(request["method"]), stringAnyValue(request["type"]))))
	target := stringAnyValue(request["target"])
	childLabel := fmt.Sprintf("%s.params.requests[%d] %q", label, requestIndex, name)
	if _, multipart := request["body_multipart"]; multipart {
		issues = append(issues, childLabel+": body_multipart is not supported in an http_sequence child; use a top-level request step")
	}
	payload, marshalErr := json.Marshal(request)
	var decodedChild Action
	childDecodeErr := marshalErr
	if childDecodeErr == nil {
		childDecodeErr = json.Unmarshal(payload, &decodedChild)
	}
	knownMalformedField := false
	if raw, exists := request["http_version"]; exists {
		var version OptionalHTTPVersion
		if err := decodeHTTPSequenceChildField(raw, &version); err != nil {
			knownMalformedField = true
			issues = append(issues, childLabel+".http_version must be auto, 1.1, 2, auto+h3, or 3")
		} else if err := version.Validate(); err != nil {
			issues = append(issues, childLabel+": "+err.Error())
		}
	}
	if raw, ok := request["headers"]; ok && raw != nil {
		var headers map[string]string
		if err := decodeHTTPSequenceChildField(raw, &headers); err != nil {
			// The header-contract pass emits the field-specific issue and still
			// validates any independently decodable header_list occurrences.
			knownMalformedField = true
		}
	}
	if raw, ok := request["header_list"]; ok && raw != nil {
		var headers HeaderList
		if err := decodeHTTPSequenceChildField(raw, &headers); err != nil {
			knownMalformedField = true
		}
	}
	if err := httpprotocol.ValidateMethod(method); err != nil {
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
		var extractors []Extractor
		if err := decodeHTTPSequenceChildField(raw, &extractors); err != nil {
			knownMalformedField = true
			issues = append(issues, fmt.Sprintf("%s.extractors must be a valid extractor array", childLabel))
		} else {
			// http_sequence child requests are HTTP by construction.
			issues = append(issues, validateExtractorIssues("http", childLabel, Action{Extractors: extractors})...)
		}
	}
	if raw, ok := request["assertions"]; ok && raw != nil {
		payload, err := json.Marshal(raw)
		var assertions []Assertion
		if err != nil || json.Unmarshal(payload, &assertions) != nil {
			knownMalformedField = true
			issues = append(issues, fmt.Sprintf("%s.assertions must be a valid assertion array", childLabel))
		} else {
			issues = append(issues, validateHTTPAssertionIssues(childLabel, assertions)...)
		}
	}
	if raw, ok := request["think_time"]; ok && raw != nil {
		payload, err := json.Marshal(raw)
		var think ThinkTimeConfig
		if err != nil || json.Unmarshal(payload, &think) != nil {
			knownMalformedField = true
			issues = append(issues, fmt.Sprintf("%s.think_time must be a valid think-time object", childLabel))
		} else {
			issues = append(issues, validateThinkTimeResourceIssues(childLabel+".think_time", think)...)
		}
	}
	if raw, ok := request["think_time_after"]; ok {
		if _, valid := raw.(bool); !valid {
			knownMalformedField = true
			issues = append(issues, fmt.Sprintf("%s.think_time_after must be a boolean", childLabel))
		}
	}
	if childDecodeErr != nil && !knownMalformedField {
		issues = append(issues, fmt.Sprintf("%s must be a valid HTTP request object", childLabel))
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
	capabilities := httpprotocol.Capabilities()
	maxTimeoutMS := capabilities.MaxTimeoutMS
	var issues []string
	if err := httpprotocol.ValidateExplicitResourceTemplates(action.Params); err != nil {
		issues = append(issues, fmt.Sprintf("%s: %v", label, err))
	}
	// http_sequence is catalogued for the HTTP protocol but dispatched by the
	// engine; its import representation uses the synthetic method SEQUENCE.
	// Each child request is validated separately above.
	if !isEngineActionType(action.Type) && !strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") && CanonicalActionType(action.Type) != "http_oauth2_token" {
		if err := httpprotocol.ValidateMethod(httpprotocol.ResolveMethod(action.Method, action.Type)); err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", label, err))
		}
	}
	if err := httpprotocol.ValidateTimeoutMS(action.TimeoutMs); err != nil {
		issues = append(issues, fmt.Sprintf("%s: timeout_ms must be between 0 and %d", label, maxTimeoutMS))
	}
	if err := httpprotocol.ValidateMaxRedirects(action.MaxRedirects); err != nil {
		issues = append(issues, fmt.Sprintf("%s: max_redirects must be between -1 and %d", label, capabilities.MaxRedirects))
	}
	if action.Auth != nil {
		if err := httpprotocol.ValidateAuthType(action.Auth.Type); err != nil && !hasDurableExecutionBlocker(action.Metadata) {
			issues = append(issues, fmt.Sprintf("%s: auth.type: %v", label, err))
		}
	}
	issues = append(issues, validateHTTPAssertionIssues(label, action.Assertions)...)
	if err := httpprotocol.ValidateRequestContentEncoding(action.Params); err != nil {
		issues = append(issues, fmt.Sprintf("%s: %v", label, err))
	}
	if raw, present := action.Params["stream_timeout_ms"]; present {
		value, ok := actionParamIntValue(action.Params, "stream_timeout_ms")
		if !ok || value < 0 || value > maxTimeoutMS {
			issues = append(issues, fmt.Sprintf("%s: params.stream_timeout_ms must be an integer between 0 and %d (got %v)", label, maxTimeoutMS, raw))
		}
	}
	return issues
}

func hasDurableExecutionBlocker(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	if value, ok := metadata["execution_blocker"].(string); ok && strings.TrimSpace(value) != "" {
		return true
	}
	// Bruno's native-v1 compatibility contract predates generic execution
	// blocker metadata. Its auth review gate is enforced by the existing Bruno
	// replay policy and must remain a valid saveable draft until that public
	// contract is revised.
	if value, ok := metadata["review_gate"].(string); ok &&
		strings.EqualFold(strings.TrimSpace(value), "bruno_auth") {
		return true
	}
	switch values := metadata["execution_blockers"].(type) {
	case []string:
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return true
			}
		}
	}
	return false
}

func validateHTTPAssertionIssues(label string, assertions []Assertion) []string {
	var issues []string
	if len(assertions) > MaxActionAssertions {
		issues = append(issues, fmt.Sprintf("%s: assertions must not exceed %d", label, MaxActionAssertions))
	}
	for index, assertion := range assertions {
		if err := httpprotocol.ValidateAssertionDefinition(protocol.Assertion{
			LegacyHTMLCheck: assertion.LegacyHTMLCheck,
			Type:            assertion.Type,
			Expected:        assertion.Expected,
			Value:           assertion.Value,
			Min:             assertion.Min,
			Max:             assertion.Max,
			CaseInsensitive: assertion.CaseInsensitive,
			Source:          assertion.Source,
		}); err != nil {
			issues = append(issues, fmt.Sprintf("%s: assertions[%d]: %v", label, index, err))
		}
	}
	return issues
}

// httpSequenceHasHTMLStep reports whether any child request is an HTML-mode
// step, whatever form the children are stored in.
func httpSequenceHasHTMLStep(requests any) bool {
	payload, err := json.Marshal(requests)
	if err != nil {
		return false
	}
	var children []struct {
		Params map[string]any `json:"params"`
	}
	if json.Unmarshal(payload, &children) != nil {
		return false
	}
	for _, child := range children {
		if _, ok := child.Params[httpprotocol.HTMLStepParam]; ok {
			return true
		}
	}
	return false
}
