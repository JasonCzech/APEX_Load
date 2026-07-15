package api

// Guided secret binding turns masked import artifacts into portable scripts.
// The API never accepts or stores a secret value here: callers choose only a
// logical name and the script receives a {{secret.NAME}} reference.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	secretBindingsRequiredCode           = "secret_bindings_required"
	secretBindingRequirementsChangedCode = "secret_binding_requirements_changed"
)

var (
	// The portable template parser permits at most 256 characters inside {{ }}.
	// "secret." consumes seven, leaving 249 for the caller-selected name.
	logicalSecretNamePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_]{0,248}$`)
	redactedValueKeyPattern  = regexp.MustCompile(`(?i)(?:["'<]|^)([A-Za-z][A-Za-z0-9_.-]{0,63})(?:["'>])?\s*[:=]\s*["']?$`)
)

// SecretBindingRequirement identifies one exact [REDACTED] occurrence in an
// executable script field. Path is an RFC 6901 JSON Pointer to the containing
// string; Occurrence distinguishes multiple sentinels in that same string.
type SecretBindingRequirement struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Occurrence    int    `json:"occurrence"`
	Label         string `json:"label"`
	Context       string `json:"context"`
	SuggestedName string `json:"suggested_name"`
	Replacement   string `json:"replacement,omitempty"`
	ScriptIndex   int    `json:"script_index"`
	ScriptID      string `json:"script_id,omitempty"`
	ScriptName    string `json:"script_name,omitempty"`
}

type secretBindingSelection struct {
	RequirementID string `json:"requirement_id"`
	SecretName    string `json:"secret_name"`
}

type applySecretBindingsRequest struct {
	Script      *dsl.Script              `json:"script"`
	ScriptIndex int                      `json:"script_index,omitempty"`
	Bindings    []secretBindingSelection `json:"bindings"`
}

type redactionTraversalContext struct {
	actionName string
	actionType string
}

type internalSecretBindingRequirement struct {
	SecretBindingRequirement
	sourceValue string
}

type staleSecretBindingRequirementsError struct {
	requirements []SecretBindingRequirement
}

func (err *staleSecretBindingRequirementsError) Error() string {
	return "secret binding requirements changed; review the refreshed locations and retry"
}

func discoverSecretBindingRequirements(script *dsl.Script, scriptIndex int) []SecretBindingRequirement {
	if script == nil {
		return []SecretBindingRequirement{}
	}
	raw, err := scriptAsJSONValue(script)
	if err != nil {
		return []SecretBindingRequirement{}
	}
	return discoverSecretBindingRequirementsValue(raw, scriptIndex)
}

func discoverSecretBindingRequirementsValue(raw any, scriptIndex int) []SecretBindingRequirement {
	scriptMap, ok := raw.(map[string]any)
	if !ok || !isScriptJSONObject(scriptMap) {
		return []SecretBindingRequirement{}
	}
	scriptID, _ := scriptMap["id"].(string)
	scriptName, _ := scriptMap["name"].(string)
	internal := make([]internalSecretBindingRequirement, 0)
	discoverRedactedValues(scriptMap, "", redactionTraversalContext{}, scriptIndex, scriptID, scriptName, &internal)

	// Redaction destroys identity, so repeated suggestions must remain distinct
	// unless the user explicitly chooses to reuse a logical binding.
	seenNames := make(map[string]int, len(internal))
	result := make([]SecretBindingRequirement, 0, len(internal))
	for _, requirement := range internal {
		base := requirement.SuggestedName
		seenNames[base]++
		if seenNames[base] > 1 {
			requirement.SuggestedName = fmt.Sprintf("%s_%d", base, seenNames[base])
			requirement.Replacement = replaceRedactedOccurrence(
				requirement.sourceValue,
				requirement.Occurrence,
				"{{secret."+requirement.SuggestedName+"}}",
			)
		}
		result = append(result, requirement.SecretBindingRequirement)
	}
	return result
}

func discoverRedactedValues(value any, path string, ctx redactionTraversalContext, scriptIndex int, scriptID, scriptName string, out *[]internalSecretBindingRequirement) {
	switch typed := value.(type) {
	case map[string]any:
		if isNonExecutableReviewAction(typed) {
			return
		}
		ctx = redactionContextForMap(typed, path, ctx)
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isNonExecutableRedactionSubtree(typed, key, path) {
				continue
			}
			discoverRedactedValues(typed[key], appendJSONPointer(path, key), ctx, scriptIndex, scriptID, scriptName, out)
		}
	case []any:
		for index, child := range typed {
			discoverRedactedValues(child, appendJSONPointer(path, strconv.Itoa(index)), ctx, scriptIndex, scriptID, scriptName, out)
		}
	case string:
		count := strings.Count(typed, maskedScriptSecret)
		for occurrence := 0; occurrence < count; occurrence++ {
			suggested := suggestedSecretName(path, typed, occurrence, ctx)
			replacement := replaceRedactedOccurrence(typed, occurrence, "{{secret."+suggested+"}}")
			*out = append(*out, internalSecretBindingRequirement{SecretBindingRequirement: SecretBindingRequirement{
				ID:            secretBindingRequirementID(scriptIndex, scriptID, scriptName, path, occurrence, typed, ctx),
				Path:          path,
				Occurrence:    occurrence,
				Label:         secretBindingLabel(path),
				Context:       secretBindingContext(path, ctx),
				SuggestedName: suggested,
				Replacement:   replacement,
				ScriptIndex:   scriptIndex,
				ScriptID:      scriptID,
				ScriptName:    scriptName,
			}, sourceValue: typed})
		}
	}
}

func redactionContextForMap(value map[string]any, path string, prior redactionTraversalContext) redactionTraversalContext {
	if !isActionObjectJSONPath(path) {
		return prior
	}
	actionType, hasType := value["type"].(string)
	actionName, hasName := value["name"].(string)
	if !hasType && !hasName {
		return prior
	}
	if strings.TrimSpace(actionName) == "" {
		actionName = prior.actionName
	}
	if strings.TrimSpace(actionType) == "" {
		actionType = prior.actionType
	}
	return redactionTraversalContext{actionName: actionName, actionType: actionType}
}

func isActionObjectJSONPath(path string) bool {
	tokens, err := splitJSONPointer(path)
	if err != nil || len(tokens) < 2 || !isDecimalToken(tokens[len(tokens)-1]) {
		return false
	}
	switch tokens[len(tokens)-2] {
	case "actions", "init", "teardown", "steps", "else":
		return true
	default:
		return false
	}
}

func isScriptActionJSONValue(value map[string]any) bool {
	_, hasName := value["name"]
	_, hasType := value["type"]
	_, hasTarget := value["target"]
	return hasName && hasType && hasTarget
}

func isNonExecutableReviewAction(value map[string]any) bool {
	if !isScriptActionJSONValue(value) {
		return false
	}
	actionType, _ := value["type"].(string)
	switch strings.ToLower(strings.TrimSpace(actionType)) {
	case "source_review", "review_required":
		return true
	default:
		return false
	}
}

// Script metadata/import identity and explicit review-only actions are
// authoring provenance. They never participate in runtime credential use.
func isNonExecutableRedactionSubtree(parent map[string]any, key string, path string) bool {
	if path == "" {
		switch key {
		case "id", "name", "version", "protocol", "project_id", "folder_id", "generated_by", "generated_from", "metadata":
			return true
		}
	}
	if isScriptActionJSONValue(parent) {
		switch key {
		case "name", "type":
			return true
		}
	}
	if key == "metadata" && isNonExecutableReviewAction(parent) {
		return true
	}
	return false
}

func secretBindingRequirementID(scriptIndex int, scriptID, scriptName, path string, occurrence int, sourceValue string, ctx redactionTraversalContext) string {
	// Bind the opaque ID to both location and projected content. Reordering or
	// editing the draft (including its action context) invalidates a stale
	// selection instead of applying it to a different credential location.
	digest := sha256.New()
	for _, part := range []string{
		strconv.Itoa(scriptIndex), scriptID, scriptName, path, strconv.Itoa(occurrence),
		sourceValue, ctx.actionName, ctx.actionType,
	} {
		_, _ = fmt.Fprintf(digest, "%d:", len(part))
		_, _ = digest.Write([]byte(part))
	}
	sum := digest.Sum(nil)
	return "secret-binding-" + hex.EncodeToString(sum[:12])
}

func appendJSONPointer(path, token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return path + "/" + token
}

func splitJSONPointer(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("invalid JSON pointer %q", path)
	}
	raw := strings.Split(path[1:], "/")
	result := make([]string, len(raw))
	for index, token := range raw {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		result[index] = token
	}
	return result, nil
}

func secretBindingLabel(path string) string {
	tokens, _ := splitJSONPointer(path)
	if len(tokens) == 0 {
		return "Secret value"
	}
	key := tokens[len(tokens)-1]
	if len(tokens) > 1 {
		parent := normalizeScriptFieldName(tokens[len(tokens)-2])
		if parent == "headers" || parent == "defaultheaders" {
			return humanizeSecretLabel(key) + " header"
		}
	}
	switch normalizeScriptFieldName(key) {
	case "body":
		return "Request body"
	case "target":
		return "Request target"
	case "baseurl":
		return "Base URL"
	default:
		return humanizeSecretLabel(key)
	}
}

func humanizeSecretLabel(value string) string {
	value = strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(value))
	if value == "" {
		return "Secret value"
	}
	runes := []rune(strings.ToLower(value))
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func secretBindingContext(path string, ctx redactionTraversalContext) string {
	if name := strings.TrimSpace(ctx.actionName); name != "" && !strings.Contains(name, maskedScriptSecret) {
		return fmt.Sprintf("Action %q", name)
	}
	switch {
	case strings.HasPrefix(path, "/config/"):
		return "Script configuration"
	case strings.HasPrefix(path, "/data_sources/"):
		return "Data source"
	case strings.HasPrefix(path, "/parameters/"):
		return "Script parameter"
	default:
		return "Script"
	}
}

func suggestedSecretName(path, value string, occurrence int, ctx redactionTraversalContext) string {
	hint := redactedValueHint(value, occurrence)
	tokens, _ := splitJSONPointer(path)
	if hint == "" && len(tokens) > 0 {
		hint = tokens[len(tokens)-1]
	}
	if strings.EqualFold(hint, "authorization") {
		prefix := stringBeforeRedactedOccurrence(value, occurrence)
		switch {
		case strings.HasSuffix(strings.ToLower(strings.TrimSpace(prefix)), "bearer"):
			hint = "token"
		case strings.HasSuffix(strings.ToLower(strings.TrimSpace(prefix)), "basic"):
			hint = "credentials"
		}
	}
	parts := make([]string, 0, 4)
	if name := strings.TrimSpace(ctx.actionName); name != "" && !strings.Contains(name, maskedScriptSecret) {
		parts = append(parts, name)
	} else {
		for _, token := range tokens {
			normalized := normalizeScriptFieldName(token)
			if normalized == "" || normalized == "config" || normalized == "actions" || normalized == "init" || normalized == "teardown" ||
				normalized == "metadata" || normalized == "headers" || normalized == "defaultheaders" || isDecimalToken(token) {
				continue
			}
			parts = append(parts, token)
		}
		if len(parts) > 2 {
			parts = parts[len(parts)-2:]
		}
	}
	if hint != "" && (len(parts) == 0 || normalizeScriptFieldName(parts[len(parts)-1]) != normalizeScriptFieldName(hint)) {
		parts = append(parts, hint)
	}
	name := normalizeLogicalSecretName(strings.Join(parts, "_"))
	if name == "" {
		name = "IMPORTED_SECRET"
	}
	return name
}

func redactedValueHint(value string, occurrence int) string {
	prefix := stringBeforeRedactedOccurrence(value, occurrence)
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(prefix)), "bearer") {
		return "token"
	}
	if match := redactedValueKeyPattern.FindStringSubmatch(prefix); len(match) == 2 {
		return match[1]
	}
	return ""
}

func stringBeforeRedactedOccurrence(value string, occurrence int) string {
	offset := 0
	for current := 0; ; current++ {
		index := strings.Index(value[offset:], maskedScriptSecret)
		if index < 0 {
			return value
		}
		index += offset
		if current == occurrence {
			return value[:index]
		}
		offset = index + len(maskedScriptSecret)
	}
}

func normalizeLogicalSecretName(value string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(value) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && builder.Len() > 0 {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	result := strings.Trim(builder.String(), "_")
	if len(result) > 128 {
		result = strings.TrimRight(result[:128], "_")
	}
	if result != "" && result[0] >= '0' && result[0] <= '9' {
		result = "SECRET_" + result
	}
	return result
}

func isDecimalToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func replaceRedactedOccurrence(value string, occurrence int, replacement string) string {
	offset := 0
	for current := 0; ; current++ {
		index := strings.Index(value[offset:], maskedScriptSecret)
		if index < 0 {
			return value
		}
		index += offset
		if current == occurrence {
			return value[:index] + replacement + value[index+len(maskedScriptSecret):]
		}
		offset = index + len(maskedScriptSecret)
	}
}

func applySecretBindings(script *dsl.Script, scriptIndex int, bindings []secretBindingSelection) (*dsl.Script, []SecretBindingRequirement, error) {
	if script == nil {
		return nil, nil, fmt.Errorf("script is required")
	}
	projectedInput := maskScriptSecrets(script)
	if projectedInput == nil {
		return nil, nil, fmt.Errorf("failed to project script for secret binding")
	}
	raw, err := scriptAsJSONValue(projectedInput)
	if err != nil {
		return nil, nil, err
	}
	requirements := discoverSecretBindingRequirementsValue(raw, scriptIndex)
	known := make(map[string]SecretBindingRequirement, len(requirements))
	for _, requirement := range requirements {
		known[requirement.ID] = requirement
	}
	selected := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		id := strings.TrimSpace(binding.RequirementID)
		if id == "" {
			return nil, nil, fmt.Errorf("binding requirement_id is required")
		}
		if _, duplicate := selected[id]; duplicate {
			return nil, nil, fmt.Errorf("binding requirement_id %q is duplicated", id)
		}
		if _, ok := known[id]; !ok {
			return nil, requirements, &staleSecretBindingRequirementsError{requirements: requirements}
		}
		name := strings.TrimSpace(binding.SecretName)
		if !logicalSecretNamePattern.MatchString(name) {
			return nil, nil, fmt.Errorf("secret_name for %q must be a canonical logical name of 1-249 uppercase letters, digits, or '_'", id)
		}
		selected[id] = name
	}

	replacements := make(map[string]map[int]string, len(selected))
	for id, name := range selected {
		requirement := known[id]
		if replacements[requirement.Path] == nil {
			replacements[requirement.Path] = make(map[int]string)
		}
		replacements[requirement.Path][requirement.Occurrence] = "{{secret." + name + "}}"
	}
	resolved := applySecretBindingReplacements(raw, "", replacements)
	payload, err := json.Marshal(resolved)
	if err != nil {
		return nil, nil, err
	}
	var result dsl.Script
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, nil, err
	}
	// Compute remaining requirements from the same disclosure-safe projection
	// returned to the client. This catches any other inline credential that the
	// caller submitted unmasked and prevents a response from claiming resolution
	// while its returned script contains a newly projected sentinel.
	projected := maskScriptSecrets(&result)
	if projected == nil {
		return nil, nil, fmt.Errorf("failed to project resolved script")
	}
	remaining := discoverSecretBindingRequirements(projected, scriptIndex)
	return projected, remaining, nil
}

func applySecretBindingReplacements(value any, path string, replacements map[string]map[int]string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = applySecretBindingReplacements(child, appendJSONPointer(path, key), replacements)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = applySecretBindingReplacements(child, appendJSONPointer(path, strconv.Itoa(index)), replacements)
		}
		return out
	case string:
		byOccurrence := replacements[path]
		if len(byOccurrence) == 0 {
			return typed
		}
		var builder strings.Builder
		offset := 0
		occurrence := 0
		for {
			index := strings.Index(typed[offset:], maskedScriptSecret)
			if index < 0 {
				builder.WriteString(typed[offset:])
				break
			}
			index += offset
			builder.WriteString(typed[offset:index])
			if replacement, ok := byOccurrence[occurrence]; ok {
				builder.WriteString(replacement)
			} else {
				builder.WriteString(maskedScriptSecret)
			}
			offset = index + len(maskedScriptSecret)
			occurrence++
		}
		return builder.String()
	default:
		return value
	}
}

func secretBindingRequirementsForImportScripts(value any) []SecretBindingRequirement {
	nodes := make([]map[string]any, 0)
	collectScriptJSONNodes(value, &nodes)
	result := make([]SecretBindingRequirement, 0)
	for index, node := range nodes {
		result = append(result, discoverSecretBindingRequirementsValue(node, index)...)
	}
	if result == nil {
		return []SecretBindingRequirement{}
	}
	return result
}

func hasScriptJSONNode(value any) bool {
	nodes := make([]map[string]any, 0, 1)
	collectScriptJSONNodes(value, &nodes)
	return len(nodes) > 0
}

func collectScriptJSONNodes(value any, out *[]map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if isScriptJSONObject(typed) {
			*out = append(*out, typed)
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectScriptJSONNodes(typed[key], out)
		}
	case []any:
		for _, child := range typed {
			collectScriptJSONNodes(child, out)
		}
	}
}

func (s *Server) handleApplyScriptSecretBindings(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req applySecretBindingsRequest
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	resolved, remaining, err := applySecretBindings(req.Script, req.ScriptIndex, req.Bindings)
	if err != nil {
		var stale *staleSecretBindingRequirementsError
		if errors.As(err, &stale) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":                       stale.Error(),
				"code":                        secretBindingRequirementsChangedCode,
				"secret_binding_requirements": stale.requirements,
			})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"script":                      maskScriptResponseValue(resolved),
		"secret_binding_requirements": remaining,
	})
}

func writeSecretBindingsRequired(w http.ResponseWriter, requirements []SecretBindingRequirement) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":                       "script contains redacted secret values that require logical secret bindings",
		"code":                        secretBindingsRequiredCode,
		"secret_binding_requirements": requirements,
	})
}
