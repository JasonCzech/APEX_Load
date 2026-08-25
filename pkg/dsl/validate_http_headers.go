package dsl

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

const (
	importHeaderFidelityMetadataKey      = ImportHeaderFidelityMetadataKey
	importHTTPVersionFidelityMetadataKey = "import_http_version_fidelity"
	importReviewRequiredBlocker          = "import_review_required"
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

	version := script.Config.HTTP.Version
	defaultListNames := make(map[string]struct{}, len(script.Config.HTTP.DefaultHeaderList))
	for _, header := range script.Config.HTTP.DefaultHeaderList {
		if name := strings.ToLower(strings.TrimSpace(header.Name)); name != "" {
			// Ordered entries are authoritative even when disabled, so a
			// legacy compatibility-map value cannot reappear during validation.
			defaultListNames[name] = struct{}{}
		}
	}
	issues := validateHTTPHeaderMapIssues(
		"config.http.default_headers",
		effectiveHTTPConfigHeaderMap(script.Config.HTTP, defaultListNames),
		version,
	)
	issues = append(issues, validateHTTPHeaderListIssues(
		"config.http.default_header_list",
		script.Config.HTTP.DefaultHeaderList,
		version,
	)...)

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
		issues = append(issues, validateHTTPActionHeaderContractIssues(label, current.action, version)...)

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
			issues = append(issues, validateHTTPSequenceHeaderContractIssues(label, current.action, version)...)
		}
	}
	return issues
}

// effectiveHTTPConfigHeaderMap mirrors protocol/http.buildDefaultHeaders.
// Authored DefaultHeaders win over typed convenience fields using HTTP's
// case-insensitive name matching. Case-variant collisions authored within
// DefaultHeaders itself remain visible so validation can reject them.
func effectiveHTTPConfigHeaderMap(config HTTPConfig, shadowed map[string]struct{}) map[string]string {
	headers := make(map[string]string, len(config.DefaultHeaders)+3)
	for name, value := range config.DefaultHeaders {
		headers[name] = value
	}
	addTypedHTTPConfigHeader(headers, "User-Agent", config.UserAgent)
	addTypedHTTPConfigHeader(headers, "Accept-Encoding", config.AcceptEncoding)
	addTypedHTTPConfigHeader(headers, "Accept-Language", config.AcceptLanguage)
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

func addTypedHTTPConfigHeader(headers map[string]string, name, value string) {
	if value == "" {
		return
	}
	for existing := range headers {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return
		}
	}
	headers[name] = value
}

func validateHTTPActionHeaderContractIssues(label string, action Action, version string) []string {
	issues := validateHTTPHeaderMapIssues(label+".headers", unshadowedHTTPHeaderMap(action.Headers, action.HeaderList), version)
	issues = append(issues, validateHTTPHeaderListIssues(label+".header_list", action.HeaderList, version)...)
	issues = append(issues, validateImportFidelityMetadataIssues(label, action.Metadata)...)
	return issues
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

func validateHTTPSequenceHeaderContractIssues(label string, action Action, version string) []string {
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
		issues = append(issues, validateHTTPActionHeaderContractIssues(childLabel, child, version)...)
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

	child := Action{Name: stringAnyValue(request["name"])}
	childLabel := fmt.Sprintf("%s.params.requests[%d] %q", label, index, child.Name)
	var issues []string
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
	case "unsupported_http_version", "http_version_not_preserved":
	default:
		issues = append(issues, fmt.Sprintf(
			"%s: %s.reason_code must be one of %q or %q",
			label,
			importHTTPVersionFidelityMetadataKey,
			"unsupported_http_version",
			"http_version_not_preserved",
		))
	}
	return reason, issues
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
