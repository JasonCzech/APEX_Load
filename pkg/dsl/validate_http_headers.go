package dsl

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

const (
	importHeaderFidelityMetadataKey      = ImportHeaderFidelityMetadataKey
	importHTTPVersionFidelityMetadataKey = "import_http_version_fidelity"
	// ImportResponseContentFidelityMetadataKey retains a failed capture disposition.
	ImportResponseContentFidelityMetadataKey = "import_response_content_fidelity"
	importReviewRequiredBlocker              = "import_review_required"
)

// validateHTTPHeaderContractIssues applies the native transport's header
// occurrence rules to every request-shaped DSL surface. The template-aware
// capability functions validate authored literal bytes without guessing the
// value of a future {{...}} interpolation; Execute applies the strict form to
// the resolved bytes immediately before transport.
func validateHTTPHeaderContractIssues(script *Script) []string {
	if script == nil {
		return nil
	}
	switch ProtocolFamily(script.Protocol) {
	case "http", "soap", "java_http":
	default:
		return nil
	}

	version := InheritedHTTPVersion(script)
	defaultListNames := make(map[string]struct{}, len(script.Config.HTTP.DefaultHeaderList))
	for _, header := range script.Config.HTTP.DefaultHeaderList {
		if name := strings.ToLower(strings.TrimSpace(header.Name)); name != "" {
			// Ordered entries are authoritative even when disabled, so a
			// legacy compatibility-map value cannot reappear during validation.
			defaultListNames[name] = struct{}{}
		}
	}
	// Global defaults retain structural validation. Their version-dependent
	// semantics are checked after precedence at each executable request leaf.
	defaultMap := effectiveHTTPConfigHeaderMap(script.Config.HTTP, nil)
	defaultList := protocolHTTPHeaderList(script.Config.HTTP.DefaultHeaderList)
	structuralMap := httpprotocol.HeaderMapIssuesForVersionWithUnresolvedTemplates(effectiveHTTPConfigHeaderMap(script.Config.HTTP, defaultListNames), "2")
	structuralList := httpprotocol.HeaderListIssuesForVersionWithUnresolvedTemplates(defaultList, "2")
	issues := formatHTTPHeaderIssues("config.http.default_headers", structuralHTTPHeaderIssues(structuralMap))
	issues = append(issues, formatHTTPHeaderIssues("config.http.default_header_list", structuralHTTPHeaderIssues(structuralList))...)

	type pendingAction struct {
		path   string
		action Action
	}
	pending := make([]pendingAction, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	for index, action := range script.Init {
		pending = append(pending, pendingAction{path: fmt.Sprintf("init[%d]", index), action: action})
	}
	for index, action := range script.Actions {
		pending = append(pending, pendingAction{path: fmt.Sprintf("actions[%d]", index), action: action})
	}
	for index, action := range script.Teardown {
		pending = append(pending, pendingAction{path: fmt.Sprintf("teardown[%d]", index), action: action})
	}

	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		label := actionLabel(current.path, current.action)
		issues = append(issues, validateHTTPActionHeaderContractIssues(label, current.action, version, defaultMap, defaultList)...)

		if current.action.Control != nil {
			for _, branch := range current.action.Control.ActionBranches() {
				for index, nested := range branch.Actions {
					pending = append(pending, pendingAction{
						path:   fmt.Sprintf("%s.control.%s[%d]", current.path, branch.Path, index),
						action: nested,
					})
				}
			}
		}
		if strings.EqualFold(strings.TrimSpace(current.action.Type), "http_sequence") {
			issues = append(issues, validateHTTPSequenceHeaderContractIssues(label, current.action, version, defaultMap, defaultList)...)
		}
	}
	return issues
}

// effectiveHTTPConfigHeaderMap uses the runtime's shared default-header contract.
// Authored DefaultHeaders win over typed convenience fields using HTTP's
// case-insensitive name matching. Case-variant collisions authored within
// DefaultHeaders itself remain visible so validation can reject them.
func effectiveHTTPConfigHeaderMap(config HTTPConfig, shadowed map[string]struct{}) map[string]string {
	headers := httpprotocol.DefaultHeaders(map[string]any{
		"default_headers": config.DefaultHeaders,
		"user_agent":      config.UserAgent,
		"accept_encoding": config.AcceptEncoding,
		"accept_language": config.AcceptLanguage,
	})
	for name := range headers {
		if _, exists := shadowed[strings.ToLower(strings.TrimSpace(name))]; exists {
			delete(headers, name)
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func protocolHTTPHeaderList(headers HeaderList) protocol.HeaderList {
	projected := make(protocol.HeaderList, 0, len(headers))
	for _, header := range headers {
		projected = append(projected, protocol.HeaderEntry{Name: header.Name, Value: header.Value, Enabled: header.Enabled})
	}
	return projected
}

func structuralHTTPHeaderIssues(issues []httpprotocol.HeaderListIssue) []httpprotocol.HeaderListIssue {
	result := make([]httpprotocol.HeaderListIssue, 0, len(issues))
	for _, issue := range issues {
		if issue.Code != httpprotocol.HeaderListIssueHopByHopUnsupported && issue.Code != httpprotocol.HeaderListIssueExpectContinueUnsupported {
			result = append(result, issue)
		}
	}
	return result
}

func validateHTTPActionHeaderContractIssues(label string, action Action, inherited string, defaults map[string]string, defaultList protocol.HeaderList) []string {
	version, err := EffectiveHTTPVersion(action, inherited)
	if err != nil {
		version = "2"
	}
	issues := validateHTTPHeaderMapIssues(label+".headers", unshadowedHTTPHeaderMap(action.Headers, action.HeaderList), version)
	issues = append(issues, validateHTTPHeaderListIssues(label+".header_list", action.HeaderList, version)...)
	issues = append(issues, validateImportFidelityMetadataIssues(label, action.Metadata)...)
	if !isEngineActionType(action.Type) && CanonicalActionType(action.Type) != "http_oauth2_token" && !strings.EqualFold(action.Type, "http_sequence") {
		headers, mapIssues := httpprotocol.EffectiveAuthoredHeaderList(defaults, defaultList, action.Headers, protocolHTTPHeaderList(action.HeaderList))
		effectiveIssues := append(mapIssues, httpprotocol.HeaderListIssuesForVersionWithUnresolvedTemplates(headers, version)...)
		if action.Metadata[importHeaderFidelityMetadataKey] != nil && hasDurableExecutionBlocker(action.Metadata) {
			// A validated durable import marker may preserve active inherited bytes
			// that require review. Structural errors are never waived; runtime still
			// performs the strict effective-header check before any group traffic.
			effectiveIssues = structuralHTTPHeaderIssues(effectiveIssues)
		}
		issues = append(issues, formatHTTPHeaderIssues(label+".effective_headers", withoutHeaderSyntaxIssues(effectiveIssues))...)
	}
	return issues
}

// withoutHeaderSyntaxIssues drops invalid name and value issues from the
// effective (inherited plus authored) header view. Each one is already
// reported where the header is authored: the action's own headers or the
// script's default headers. Reporting it twice made one bad header read as two.
func withoutHeaderSyntaxIssues(issues []httpprotocol.HeaderListIssue) []httpprotocol.HeaderListIssue {
	result := make([]httpprotocol.HeaderListIssue, 0, len(issues))
	for _, issue := range issues {
		if issue.Code != httpprotocol.HeaderListIssueInvalidName && issue.Code != httpprotocol.HeaderListIssueInvalidValue {
			result = append(result, issue)
		}
	}
	return result
}

func unshadowedHTTPHeaderMap(headers map[string]string, headerList HeaderList) map[string]string {
	if len(headers) == 0 || len(headerList) == 0 {
		return headers
	}
	shadowed := make(map[string]struct{}, len(headerList))
	for _, header := range headerList {
		if name := strings.ToLower(strings.TrimSpace(header.Name)); name != "" {
			shadowed[name] = struct{}{}
		}
	}
	if len(shadowed) == 0 {
		return headers
	}
	filtered := make(map[string]string, len(headers))
	for name, value := range headers {
		if _, exists := shadowed[strings.ToLower(strings.TrimSpace(name))]; exists {
			continue
		}
		filtered[name] = value
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func validateHTTPHeaderMapIssues(label string, headers map[string]string, version string) []string {
	runtimeIssues := httpprotocol.HeaderMapIssuesForVersionWithUnresolvedTemplates(headers, version)
	return formatHTTPHeaderIssues(label, runtimeIssues)
}

func validateHTTPHeaderListIssues(label string, headers HeaderList, version string) []string {
	if len(headers) == 0 {
		return nil
	}
	projected := make(protocol.HeaderList, 0, len(headers))
	for _, header := range headers {
		projected = append(projected, protocol.HeaderEntry{Name: header.Name, Value: header.Value, Enabled: header.Enabled})
	}
	runtimeIssues := httpprotocol.HeaderListIssuesForVersionWithUnresolvedTemplates(projected, version)
	return formatHTTPHeaderIssues(label, runtimeIssues)
}

func formatHTTPHeaderIssues(label string, runtimeIssues []httpprotocol.HeaderListIssue) []string {
	if len(runtimeIssues) == 0 {
		return nil
	}
	issues := make([]string, 0, len(runtimeIssues))
	for _, issue := range runtimeIssues {
		issues = append(issues, fmt.Sprintf(
			"%s[%d] %q: %s (%s)",
			label,
			issue.Index,
			issue.Name,
			issue.Detail,
			issue.Code,
		))
	}
	return issues
}

func validateHTTPSequenceHeaderContractIssues(label string, action Action, version string, defaults map[string]string, defaultList protocol.HeaderList) []string {
	rawRequests, exists := action.Params["requests"]
	if !exists {
		return nil
	}

	var requests []any
	switch typed := rawRequests.(type) {
	case []any:
		requests = typed
	case []map[string]any:
		requests = make([]any, 0, len(typed))
		for _, request := range typed {
			requests = append(requests, request)
		}
	case []Action:
		requests = make([]any, 0, len(typed))
		for _, request := range typed {
			requests = append(requests, request)
		}
	default:
		return nil
	}

	var issues []string
	for index, rawRequest := range requests {
		child, shapeIssues := decodeHTTPSequenceChildHeaders(label, index, rawRequest)
		issues = append(issues, shapeIssues...)
		childLabel := fmt.Sprintf("%s.params.requests[%d] %q", label, index, child.Name)
		issues = append(issues, validateHTTPActionHeaderContractIssues(childLabel, child, version, defaults, defaultList)...)
	}
	return issues
}

func decodeHTTPSequenceChildHeaders(label string, index int, rawRequest any) (Action, []string) {
	switch typed := rawRequest.(type) {
	case Action:
		return typed, nil
	case *Action:
		if typed != nil {
			return *typed, nil
		}
	}
	request, ok := rawRequest.(map[string]any)
	if !ok {
		payload, err := json.Marshal(rawRequest)
		if err != nil {
			return Action{}, nil
		}
		if err := json.Unmarshal(payload, &request); err != nil || request == nil {
			return Action{}, nil
		}
	}

	child := Action{Name: stringAnyValue(request["name"]), Type: stringAnyValue(request["type"]), Method: stringAnyValue(request["method"])}
	childLabel := fmt.Sprintf("%s.params.requests[%d] %q", label, index, child.Name)
	var issues []string
	if raw, exists := request["http_version"]; exists {
		if err := decodeHTTPSequenceChildField(raw, &child.HTTPVersion); err != nil {
			issues = append(issues, childLabel+".http_version must be auto, 1.1, 2, auto+h3, or 3")
		} else if err := child.HTTPVersion.Validate(); err != nil {
			issues = append(issues, childLabel+": "+err.Error())
		}
	}
	if raw, exists := request["headers"]; exists && raw != nil {
		if err := decodeHTTPSequenceChildField(raw, &child.Headers); err != nil {
			issues = append(issues, childLabel+".headers must be an object with string values")
		}
	}
	if raw, exists := request["header_list"]; exists && raw != nil {
		if err := decodeHTTPSequenceChildField(raw, &child.HeaderList); err != nil {
			issues = append(issues, childLabel+".header_list must be a valid header array")
		}
	}
	if raw, exists := request["metadata"]; exists && raw != nil {
		// Metadata is included so valid importer-fidelity evidence on a child is
		// still checked even when an unrelated child field cannot decode into
		// the complete Action struct.
		_ = decodeHTTPSequenceChildField(raw, &child.Metadata)
	}
	return child, issues
}

func decodeHTTPSequenceChildField(raw any, target any) error {
	payload, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

func validateImportFidelityMetadataIssues(label string, metadata map[string]any) []string {
	if metadata == nil {
		return nil
	}
	var issues []string
	requiredReasons := make(map[string]struct{})

	if raw, exists := metadata[importHeaderFidelityMetadataKey]; exists {
		reasons, markerIssues := validateImportHeaderFidelityMarker(label, raw)
		issues = append(issues, markerIssues...)
		for _, reason := range reasons {
			requiredReasons[reason] = struct{}{}
		}
	}
	if raw, exists := metadata[importHTTPVersionFidelityMetadataKey]; exists {
		reason, markerIssues := validateImportHTTPVersionFidelityMarker(label, raw)
		issues = append(issues, markerIssues...)
		if reason != "" {
			requiredReasons[reason] = struct{}{}
		}
	}
	if raw, exists := metadata[ImportResponseContentFidelityMetadataKey]; exists {
		reason, markerIssues := validateImportResponseContentFidelityMarker(label, raw)
		issues = append(issues, markerIssues...)
		if reason != "" {
			requiredReasons[reason] = struct{}{}
		}
	}
	if raw, exists := metadata["response_content_capture"]; exists {
		capture, ok := raw.(map[string]any)
		if !ok {
			issues = append(issues, label+": response_content_capture must be an object")
		} else if rawReason, present := capture["reason_code"]; present {
			reason, reasonOK := rawReason.(string)
			if reasonOK && reason == "response_content_unavailable" && capture["captured"] == "har_decoded" && capture["stored"] == "unavailable" && capture["state"] == "unavailable" {
				// HAR permits optional response content to be absent. This does
				// not establish a failed recorder capture or require a gate.
			} else if reasonOK && supportedResponseContentFailureReason(reason) {
				requiredReasons[reason] = struct{}{}
			} else {
				issues = append(issues, label+": response_content_capture.reason_code must be a supported response content fidelity reason")
			}
		} else if capture["state"] == "unavailable" || capture["stored"] == "unavailable" {
			issues = append(issues, label+": unavailable response_content_capture requires a supported reason_code")
		}
	}
	if len(requiredReasons) == 0 {
		return issues
	}

	blockers := metadataStringSet(metadata, "execution_blockers")
	if blocker, ok := metadata["execution_blocker"].(string); ok {
		blockers[strings.TrimSpace(blocker)] = struct{}{}
	}
	if _, gated := blockers[importReviewRequiredBlocker]; !gated {
		issues = append(issues, fmt.Sprintf(
			"%s: import fidelity evidence requires the %q execution blocker",
			label,
			importReviewRequiredBlocker,
		))
	}

	review, ok := metadata["import_review"].(map[string]any)
	if !ok {
		issues = append(issues, fmt.Sprintf("%s: import fidelity evidence requires import_review metadata", label))
		return issues
	}
	reviewReasons := metadataStringSet(review, "reason_codes")
	for reason := range requiredReasons {
		if _, matched := reviewReasons[reason]; !matched {
			issues = append(issues, fmt.Sprintf(
				"%s: import_review.reason_codes must include fidelity reason %q",
				label,
				reason,
			))
		}
	}
	return issues
}

func validateImportHeaderFidelityMarker(label string, raw any) ([]string, []string) {
	marker, ok := raw.(map[string]any)
	if !ok {
		return nil, []string{fmt.Sprintf("%s: %s must be an object", label, importHeaderFidelityMetadataKey)}
	}
	var issues []string
	if !metadataVersionIsOne(marker["version"]) {
		issues = append(issues, fmt.Sprintf("%s: %s.version must be 1", label, importHeaderFidelityMetadataKey))
	}
	markerReasons := metadataStringSet(marker, "reason_codes")
	if len(markerReasons) == 0 {
		issues = append(issues, fmt.Sprintf("%s: %s.reason_codes must not be empty", label, importHeaderFidelityMetadataKey))
	}
	markerSourceRefs := metadataStringSet(marker, "source_refs")

	omitted, ok := metadataObjectSlice(marker["omitted_occurrences"])
	if !ok || len(omitted) == 0 {
		issues = append(issues, fmt.Sprintf("%s: %s.omitted_occurrences must not be empty", label, importHeaderFidelityMetadataKey))
		return sortedStringSet(markerReasons), issues
	}
	for index, occurrence := range omitted {
		occurrenceLabel := fmt.Sprintf("%s.%s.omitted_occurrences[%d]", label, importHeaderFidelityMetadataKey, index)
		if _, ok := metadataNonNegativeInteger(occurrence["source_index"]); !ok {
			issues = append(issues, occurrenceLabel+".source_index must be a non-negative integer")
		}
		if _, ok := occurrence["name"].(string); !ok {
			issues = append(issues, occurrenceLabel+".name must be a string")
		}
		reason, _ := occurrence["reason_code"].(string)
		reason = strings.TrimSpace(reason)
		if reason == "" {
			issues = append(issues, occurrenceLabel+".reason_code is required")
		} else if _, matched := markerReasons[reason]; !matched {
			issues = append(issues, fmt.Sprintf("%s.reason_code %q is missing from %s.reason_codes", occurrenceLabel, reason, importHeaderFidelityMetadataKey))
		}
		sourceRef, _ := occurrence["source_ref"].(string)
		sourceRef = strings.TrimSpace(sourceRef)
		if sourceRef != "" {
			if _, matched := markerSourceRefs[sourceRef]; !matched {
				issues = append(issues, fmt.Sprintf("%s.source_ref %q is missing from %s.source_refs", occurrenceLabel, sourceRef, importHeaderFidelityMetadataKey))
			}
		}
		nameBytes, hasNameBytes := occurrence[ImportHeaderFidelityPreservedNameBase64Key]
		valueBytes, hasValueBytes := occurrence[ImportHeaderFidelityPreservedValueBase64Key]
		if hasNameBytes != hasValueBytes {
			issues = append(issues, fmt.Sprintf(
				"%s must contain both %s and %s",
				occurrenceLabel,
				ImportHeaderFidelityPreservedNameBase64Key,
				ImportHeaderFidelityPreservedValueBase64Key,
			))
			continue
		}
		if hasNameBytes {
			issues = append(
				issues,
				validateCanonicalBase64MetadataField(
					occurrenceLabel+"."+ImportHeaderFidelityPreservedNameBase64Key,
					nameBytes,
				)...,
			)
			issues = append(
				issues,
				validateCanonicalBase64MetadataField(
					occurrenceLabel+"."+ImportHeaderFidelityPreservedValueBase64Key,
					valueBytes,
				)...,
			)
		}
	}
	return sortedStringSet(markerReasons), issues
}

func validateCanonicalBase64MetadataField(label string, raw any) []string {
	value, ok := raw.(string)
	if !ok {
		return []string{label + " must be a base64 string"}
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != value {
		return []string{label + " must use canonical base64 encoding"}
	}
	return nil
}

func validateImportHTTPVersionFidelityMarker(label string, raw any) (string, []string) {
	marker, ok := raw.(map[string]any)
	if !ok {
		return "", []string{fmt.Sprintf("%s: %s must be an object", label, importHTTPVersionFidelityMetadataKey)}
	}
	var issues []string
	if !metadataVersionIsOne(marker["version"]) {
		issues = append(issues, fmt.Sprintf("%s: %s.version must be 1", label, importHTTPVersionFidelityMetadataKey))
	}
	reason, _ := marker["reason_code"].(string)
	reason = strings.TrimSpace(reason)
	switch reason {
	case "unsupported_http_version", "http_version_not_preserved", "malformed_http_version", "http_version_unavailable", "proxy_http_version_legs_differ", "malformed_http_version_provenance":
	default:
		issues = append(issues, fmt.Sprintf(
			"%s: %s.reason_code must be a supported HTTP version fidelity reason (including %q or %q)",
			label,
			importHTTPVersionFidelityMetadataKey,
			"unsupported_http_version",
			"http_version_not_preserved",
		))
	}
	return reason, issues
}

// Response-content failures remain gated after JSON persistence, even when the
// ordinary import-review annotations are edited. Reasons are fixed enums; an
// unknown source value must not enter a validation diagnostic.
func validateImportResponseContentFidelityMarker(label string, raw any) (string, []string) {
	marker, ok := raw.(map[string]any)
	if !ok {
		return "", []string{label + ": " + ImportResponseContentFidelityMetadataKey + " must be an object"}
	}
	var issues []string
	if !metadataVersionIsOne(marker["version"]) {
		issues = append(issues, label+": "+ImportResponseContentFidelityMetadataKey+".version must be 1")
	}
	reason, _ := marker["reason_code"].(string)
	if supportedResponseContentFailureReason(reason) {
		return reason, issues
	}
	issues = append(issues, label+": "+ImportResponseContentFidelityMetadataKey+".reason_code must be a supported response content fidelity reason")
	return "", issues
}

func supportedResponseContentFailureReason(reason string) bool {
	switch reason {
	case "response_content_truncated", "unsupported_response_content_encoding",
		"invalid_response_content_encoding", "response_content_decode_limit",
		"response_content_decode_canceled", "malformed_response_content_provenance",
		"response_content_provenance_unavailable", "invalid_har_content_storage",
		"unsupported_har_content_storage", "har_content_storage_limit":
		return true
	}
	return false
}

func metadataStringSet(metadata map[string]any, key string) map[string]struct{} {
	values := make(map[string]struct{})
	if metadata == nil {
		return values
	}
	switch typed := metadata[key].(type) {
	case string:
		if value := strings.TrimSpace(typed); value != "" {
			values[value] = struct{}{}
		}
	case []string:
		for _, raw := range typed {
			if value := strings.TrimSpace(raw); value != "" {
				values[value] = struct{}{}
			}
		}
	case []any:
		for _, raw := range typed {
			if text, ok := raw.(string); ok {
				if value := strings.TrimSpace(text); value != "" {
					values[value] = struct{}{}
				}
			}
		}
	}
	return values
}

func sortedStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	// Header occurrence order is preserved elsewhere; reason-code sets are
	// sorted solely to make validation diagnostics deterministic.
	sort.Strings(out)
	return out
}

func metadataObjectSlice(raw any) ([]map[string]any, bool) {
	switch typed := raw.(type) {
	case []map[string]any:
		return typed, true
	case []any:
		objects := make([]map[string]any, 0, len(typed))
		for _, value := range typed {
			object, ok := value.(map[string]any)
			if !ok {
				return nil, false
			}
			objects = append(objects, object)
		}
		return objects, true
	default:
		return nil, false
	}
}

func metadataVersionIsOne(raw any) bool {
	switch typed := raw.(type) {
	case int:
		return typed == 1
	case int32:
		return typed == 1
	case int64:
		return typed == 1
	case float64:
		return typed == 1
	default:
		return false
	}
}

func metadataNonNegativeInteger(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case int:
		return int64(typed), typed >= 0
	case int32:
		return int64(typed), typed >= 0
	case int64:
		return typed, typed >= 0
	case float64:
		integer := int64(typed)
		return integer, typed >= 0 && float64(integer) == typed
	default:
		return 0, false
	}
}
