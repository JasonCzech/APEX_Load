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
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	secretBindingsRequiredCode           = "secret_bindings_required"
	secretBindingRequirementsChangedCode = "secret_binding_requirements_changed"
)

var (
	// The portable template parser permits at most 256 characters inside {{ }}.
	// "secret." consumes seven, leaving 249 for the caller-selected name.
	logicalSecretNamePattern        = regexp.MustCompile(`^[A-Z0-9_]{1,249}$`)
	httpProvenanceActionPathPattern = regexp.MustCompile(`^/(?:init|actions|teardown)/[0-9]+(?:/control/(?:steps|else|default|(?:cases|branches)/[0-9]+/steps)/[0-9]+)*$`)
	redactedValueKeyPattern         = regexp.MustCompile(`(?i)(?:["'<]|^)([A-Za-z][A-Za-z0-9_.-]{0,63})(?:["'>])?\s*[:=]\s*["']?$`)
)

// SecretBindingRequirement identifies one exact [REDACTED] occurrence in an
// executable script field. Path is an RFC 6901 JSON Pointer to the containing
// string; Occurrence distinguishes multiple sentinels in that same string.
type SecretBindingRequirement struct {
	ID                                 string   `json:"id"`
	Path                               string   `json:"path"`
	Occurrence                         int      `json:"occurrence"`
	Label                              string   `json:"label"`
	Context                            string   `json:"context"`
	SuggestedName                      string   `json:"suggested_name"`
	Replacement                        string   `json:"replacement,omitempty"`
	ScriptIndex                        int      `json:"script_index"`
	ScriptID                           string   `json:"script_id,omitempty"`
	ScriptName                         string   `json:"script_name,omitempty"`
	SourceVariable                     string   `json:"source_variable,omitempty"`
	UnresolvedSourceVariable           bool     `json:"unresolved_source_variable,omitempty"`
	AffectedPaths                      []string `json:"affected_paths,omitempty"`
	PreservationUnavailableReason      string   `json:"preservation_unavailable_reason,omitempty"`
	PreservationUnavailableDescription string   `json:"preservation_unavailable_description,omitempty"`
	SourceValueAvailable               bool     `json:"source_value_available,omitempty"`
	// DestinationValuePreserved is server-derived occurrence evidence. It means
	// the editable destination candidate still contains the imported executable
	// value at this exact requirement path; it is independent of whether an
	// optional source-secret migration session is available.
	DestinationValuePreserved bool `json:"destination_value_preserved,omitempty"`
	// ValueGroupID identifies equal recoverable values within this response only.
	// It is an occurrence ID, never a digest of credential material.
	ValueGroupID string `json:"value_group_id,omitempty"`
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
	httpProtocol        bool
	nonWireMetadataPath string
	actionName          string
	actionType          string
	headerName          string
}

type internalSecretBindingRequirement struct {
	SecretBindingRequirement
	sourceValue string
}

type staleSecretBindingRequirementsError struct {
	requirements []SecretBindingRequirement
}

// devFakeSecretsEnv opts a local workstation into substituting a placeholder for
// every project secret. It is deliberately NOT keyed on APEXLOAD_ENV: compose.yaml
// ships APEXLOAD_ENV=local, so keying on it silently turned the shipped local
// stack into one that sends a fake credential to whatever target the script
// points at while reporting every secret as configured.
const devFakeSecretsEnv = "APEXLOAD_DEV_FAKE_SECRETS"

// secretBindingEnforcementDisabled reports whether placeholder secrets are
// allowed. It requires an explicit opt-in and is refused outright in production.
func secretBindingEnforcementDisabled() bool {
	if !parseBoolDefault(strings.TrimSpace(os.Getenv(devFakeSecretsEnv)), false) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ENV"))) {
	case "production", "prod":
		return false
	default:
		return true
	}
}

func (err *staleSecretBindingRequirementsError) Error() string {
	return "secret binding requirements changed; review the refreshed locations and retry"
}

func discoverSecretBindingRequirements(script *dsl.Script, scriptIndex int) []SecretBindingRequirement {
	// Deliberately NOT gated on secretBindingEnforcementDisabled. Discovery is
	// what turns a [REDACTED] sentinel into a guided binding; suppressing it while
	// reconcileMaskedScriptSecrets still rejects orphan sentinels left create and
	// update failing with a flat 400 and no requirement list to act on. The dev
	// bypass only needs to affect RUNTIME resolution, which it still does.
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
	registry := newSecretSuggestionRegistry()
	reserveExistingSecretNames(raw, registry)
	return discoverSecretBindingRequirementsValueWithRegistry(raw, scriptIndex, registry)
}

type secretSuggestionRegistry struct {
	used map[string]struct{}
	next map[string]int
}

func newSecretSuggestionRegistry() *secretSuggestionRegistry {
	return &secretSuggestionRegistry{
		used: make(map[string]struct{}),
		next: make(map[string]int),
	}
}

func (registry *secretSuggestionRegistry) reserve(name string) {
	if registry == nil {
		return
	}
	name, err := engine.NormalizeLogicalSecretName(name)
	if err != nil || !logicalSecretNamePattern.MatchString(name) {
		return
	}
	registry.used[name] = struct{}{}
	if registry.next[name] < 2 {
		registry.next[name] = 2
	}
}

func (registry *secretSuggestionRegistry) allocate(base string) string {
	if registry == nil {
		registry = newSecretSuggestionRegistry()
	}
	base = strings.TrimSpace(base)
	if len(base) > 249 {
		base = base[:249]
	}
	if base == "" {
		base = "IMPORTED_SECRET"
	}
	if _, exists := registry.used[base]; !exists {
		registry.used[base] = struct{}{}
		registry.next[base] = 2
		return base
	}
	for suffixNumber := registry.next[base]; ; suffixNumber++ {
		suffix := "_" + strconv.Itoa(suffixNumber)
		prefix := base
		if len(prefix)+len(suffix) > 249 {
			prefix = prefix[:249-len(suffix)]
		}
		candidate := prefix + suffix
		if _, exists := registry.used[candidate]; exists {
			continue
		}
		registry.used[candidate] = struct{}{}
		registry.next[base] = suffixNumber + 1
		return candidate
	}
}

func reserveExistingSecretNames(raw any, registry *secretSuggestionRegistry) {
	payload, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var script dsl.Script
	if err := json.Unmarshal(payload, &script); err != nil {
		return
	}
	names, err := engine.ReferencedSecretNames(&script)
	if err != nil {
		return
	}
	for _, name := range names {
		registry.reserve(name)
	}
}

func discoverSecretBindingRequirementsValueWithRegistry(raw any, scriptIndex int, registry *secretSuggestionRegistry) []SecretBindingRequirement {
	scriptMap, ok := raw.(map[string]any)
	if !ok || !isScriptJSONObject(scriptMap) {
		return []SecretBindingRequirement{}
	}
	scriptID, _ := scriptMap["id"].(string)
	scriptName, _ := scriptMap["name"].(string)
	internal := make([]internalSecretBindingRequirement, 0)
	discoverRedactedValues(scriptMap, "", redactionTraversalContext{}, scriptIndex, scriptID, scriptName, &internal)

	// Redaction destroys identity, so every occurrence in an import batch gets a
	// distinct default. Users can still explicitly choose the same logical name
	// when they know two locations intentionally share one credential.
	result := make([]SecretBindingRequirement, 0, len(internal))
	for _, requirement := range internal {
		suggestedName := registry.allocate(requirement.SuggestedName)
		if suggestedName != requirement.SuggestedName {
			requirement.SuggestedName = suggestedName
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
		if isNonExecutableReviewAction(typed) || isDurablyImportReviewGatedAction(typed) {
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
			childPath := appendJSONPointer(path, key)
			// JVM argv has no secure secret-injection equivalent. Unsafe values
			// are rejected by the write boundary and masked only for legacy read
			// projections; never turn that response sentinel into a project
			// secret requirement or a forbidden {{secret.*}} JVM argument.
			if isJavaVMParameterJSONPath(childPath) {
				continue
			}
			// A data source's inline rows are a runtime data table, not a
			// credential field. When an importer redacts credential-named
			// parameter rows it also attaches a durable execution gate whose
			// resolution is "supply an approved data source" — one project
			// secret cannot stand in for a multi-row table, so offering a
			// binding here would let an operator clear the gate without
			// restoring the data. Discovery-only, like the skips above.
			if isRuntimeDataTableJSONPath(childPath) {
				continue
			}
			// Import provenance recorded in action metadata (original_url,
			// source_transaction) documents where the action came from and is
			// never sent, so it must
			// not demand a credential binding. This skip is deliberately scoped to
			// requirement DISCOVERY: redaction reconciliation still walks metadata,
			// so a masked read-modify-write cannot overwrite the stored value.
			if isImportProvenanceMetadataPathForContext(childPath, ctx) {
				continue
			}
			if isImportCredentialLocatorMetadataPath(childPath) {
				continue
			}
			discoverRedactedValues(typed[key], childPath, ctx, scriptIndex, scriptID, scriptName, out)
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
				Label:         secretBindingLabel(path, ctx),
				Context:       secretBindingContext(path, ctx),
				SuggestedName: suggested,
				Replacement:   replacement,
				ScriptIndex:   scriptIndex,
				ScriptID:      scriptID,
				ScriptName:    scriptName,
				AffectedPaths: []string{path},
			}, sourceValue: typed})
		}
	}
}

func isImportCredentialLocatorMetadataPath(path string) bool {
	tokens, err := splitJSONPointer(path)
	if err != nil || len(tokens) < 2 {
		return false
	}
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index] == "metadata" &&
			tokens[index+1] == dsl.ImportPreservedCredentialTargetsMetadataKey {
			return true
		}
	}
	return false
}

// isRuntimeDataTableJSONPath reports whether path addresses the inline rows of
// a script data source (/data_sources/{n}/inline_data).
func isRuntimeDataTableJSONPath(path string) bool {
	tokens, err := splitJSONPointer(path)
	if err != nil || len(tokens) != 3 {
		return false
	}
	return tokens[0] == "data_sources" && tokens[2] == "inline_data"
}

func isJavaVMParameterJSONPath(path string) bool {
	tokens, err := splitJSONPointer(path)
	if err != nil || len(tokens) < 3 {
		return false
	}
	return tokens[0] == "config" &&
		tokens[1] == "java_vuser" &&
		tokens[2] == "additional_vm_parameters"
}

func redactionContextForMap(value map[string]any, path string, prior redactionTraversalContext) redactionTraversalContext {
	if path == "" {
		protocol, _ := value["protocol"].(string)
		switch strings.ToLower(strings.TrimSpace(protocol)) {
		case "http", "http2", "soap", "java_http":
			prior.httpProtocol = true
		}
	}
	// Sequence requests have no action type of their own. Recognize only an
	// immediate request of a previously established HTTP sequence action.
	if prior.httpProtocol && strings.EqualFold(prior.actionType, "http_sequence") && prior.nonWireMetadataPath != "" {
		prefix := strings.TrimSuffix(prior.nonWireMetadataPath, "/metadata") + "/params/requests/"
		if strings.HasPrefix(path, prefix) && isDecimalToken(strings.TrimPrefix(path, prefix)) {
			prior.nonWireMetadataPath = path + "/metadata"
			prior.actionType = "request"
			return prior
		}
	}

	if isHeaderListEntryJSONPath(path) {
		headerName, _ := value["name"].(string)
		if headerName = strings.TrimSpace(headerName); headerName != "" &&
			!strings.Contains(headerName, maskedScriptSecret) {
			prior.headerName = headerName
		}
		return prior
	}
	if !isActionObjectJSONPath(path) && !httpProvenanceActionPathPattern.MatchString(path) {
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
	prior.actionName = actionName
	prior.actionType = actionType
	prior.headerName = ""
	prior.nonWireMetadataPath = ""
	if prior.httpProtocol && httpProvenanceActionPathPattern.MatchString(path) {
		switch strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(actionType))) {
		case "request", "get", "post", "put", "patch", "delete", "head", "options", "trace", "connect", "http_sequence", "http_oauth2_token", "source_transaction":
			prior.nonWireMetadataPath = path + "/metadata"
		}
	}
	return prior
}

func isHeaderListEntryJSONPath(path string) bool {
	tokens, err := splitJSONPointer(path)
	if err != nil || len(tokens) < 2 || !isDecimalToken(tokens[len(tokens)-1]) {
		return false
	}
	switch normalizeScriptFieldName(tokens[len(tokens)-2]) {
	case "headerlist", "defaultheaderlist":
		return true
	default:
		return false
	}
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

// isDurablyImportReviewGatedAction recognizes only a generic source-neutral
// execution gate. A disclosure-safe blocked draft may retain [REDACTED] inside
// the affected request because transport is impossible until the user replaces
// that behavior and removes the gate. An explicit credential-management blocker
// is different: those sentinels are intentionally bindable even when another
// review gate remains on the action.
func isDurablyImportReviewGatedAction(value map[string]any) bool {
	if !isScriptActionJSONValue(value) {
		return false
	}
	metadata, _ := value["metadata"].(map[string]any)
	reviewRequired := false
	for _, blocker := range executionReviewBlockerCodes(metadata) {
		switch blocker {
		case "import_credentials_unmanaged":
			return false
		case "import_review_required":
			reviewRequired = true
		}
	}
	return reviewRequired
}

// Script metadata/import identity and explicit review-only actions are
// authoring provenance. They never participate in runtime credential use.
// isImportProvenanceMetadataPath reports whether a JSON pointer addresses an
// import-provenance entry inside action metadata, e.g.
// /actions/0/metadata/original_url or /actions/0/metadata/source_transaction.
func isImportProvenanceMetadataPath(path string) bool {
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return false
	}
	if segments[len(segments)-2] != "metadata" {
		return false
	}
	return isImportProvenanceMetadataKey(segments[len(segments)-1])
}

// Context-specific evidence exemptions must not hide outbound message metadata
// or arbitrary maps nested inside action parameters. The orphan-sentinel guard
// uses this same predicate as requirement discovery.
func isImportProvenanceMetadataPathForContext(path string, ctx redactionTraversalContext) bool {
	if isImportProvenanceMetadataPath(path) {
		return true
	}
	separator := strings.LastIndexByte(path, '/')
	if separator < 0 || ctx.nonWireMetadataPath == "" || path[:separator] != ctx.nonWireMetadataPath {
		return false
	}
	_, ok := httpImportProvenanceEvidenceKeys[normalizeImportProvenanceKey(path[separator+1:])]
	return ok
}

// isImportProvenanceMetadataKey reports whether an action-metadata key records
// where an action came from rather than a value that is sent.
//
// "original_" is NOT a safe prefix: original_request holds captured raw review
// evidence (headers, credentials) whose promotion into an executable action must
// still be rejected. Only the specific provenance keys are exempt.
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
	// Metadata on a preserved review artifact is provenance and is never sent.
	// Metadata on a real action IS executable — kafka.buildRecord and
	// jms.buildMessage copy it onto the wire — so it must stay inside redaction
	// reconciliation, or a masked read-modify-write overwrites the stored value
	// with the [REDACTED] sentinel and the orphan-sentinel guard stops firing.
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
		sourceValue, ctx.actionName, ctx.actionType, ctx.headerName,
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

func secretBindingLabel(path string, ctx redactionTraversalContext) string {
	if headerName := strings.TrimSpace(ctx.headerName); headerName != "" {
		return headerName + " request header"
	}
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
	if headerName := strings.TrimSpace(ctx.headerName); headerName != "" {
		if name := strings.TrimSpace(ctx.actionName); name != "" && !strings.Contains(name, maskedScriptSecret) {
			return fmt.Sprintf("Sensitive request header %q on action %q. Supplies the credential value sent in the %s HTTP header when this request runs.", headerName, name, headerName)
		}
		return fmt.Sprintf("Sensitive request header %q. Supplies the credential value sent in the %s HTTP header when this request runs.", headerName, headerName)
	}
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
	return applySecretBindingsWithResolver(
		script,
		scriptIndex,
		bindings,
		applyRedactedSecretBindings,
	)
}

type redactedSecretBindingResolver func(
	*dsl.Script,
	int,
	[]secretBindingSelection,
) (*dsl.Script, []SecretBindingRequirement, error)

func applyCredentialPreservingSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	bindings []secretBindingSelection,
) (*dsl.Script, []SecretBindingRequirement, error) {
	return applySecretBindingsWithResolver(
		script,
		scriptIndex,
		bindings,
		applyPreservedRedactedSecretBindings,
	)
}

func applySecretBindingsWithResolver(
	script *dsl.Script,
	scriptIndex int,
	bindings []secretBindingSelection,
	resolveRedacted redactedSecretBindingResolver,
) (*dsl.Script, []SecretBindingRequirement, error) {
	if err := validateJavaVMParameterDisclosureSafety(script); err != nil {
		return nil, nil, err
	}
	selectedScopes := selectedSecretBindingScopes(script, scriptIndex, bindings)
	protectedResolved, legacyBindings, protectedRemaining, err := applyProtectedImportCredentialBindings(script, bindings)
	if err != nil {
		return nil, nil, err
	}
	if err := validateJavaVMParameterDisclosureSafety(protectedResolved); err != nil {
		return nil, nil, err
	}
	resolved, legacyRemaining, err := resolveRedacted(protectedResolved, scriptIndex, legacyBindings)
	if err != nil {
		var stale *staleSecretBindingRequirementsError
		if errors.As(err, &stale) {
			combined := append(append([]SecretBindingRequirement(nil), protectedRemaining...), stale.requirements...)
			return nil, combined, &staleSecretBindingRequirementsError{requirements: combined}
		}
		return nil, nil, err
	}
	if err := validateJavaVMParameterDisclosureSafety(resolved); err != nil {
		return nil, nil, err
	}
	remaining := append(append([]SecretBindingRequirement(nil), protectedRemaining...), legacyRemaining...)
	sort.SliceStable(remaining, func(i, j int) bool {
		if remaining[i].ID != remaining[j].ID {
			return remaining[i].ID < remaining[j].ID
		}
		if remaining[i].Path != remaining[j].Path {
			return remaining[i].Path < remaining[j].Path
		}
		return remaining[i].Occurrence < remaining[j].Occurrence
	})
	if len(selectedScopes) > 0 {
		for _, requirement := range remaining {
			if scope := secretBindingActionScope(requirement.Path); scope != "" {
				delete(selectedScopes, scope)
			}
		}
		if descriptors, descriptorErr := loadProtectedImportCredentialDescriptors(resolved); descriptorErr == nil {
			for _, descriptor := range descriptors {
				for _, location := range descriptor.Locations {
					if scope := secretBindingActionScope(location.Path); scope != "" {
						delete(selectedScopes, scope)
					}
				}
			}
		}
		if err := clearResolvedCredentialManagementBlockers(resolved, selectedScopes); err != nil {
			return nil, nil, fmt.Errorf("clear resolved credential management blockers: %w", err)
		}
	}
	return resolved, remaining, nil
}

// clearResolvedCredentialManagementBlockers removes only the credential
// management execution code on request surfaces for which this binding
// operation resolved every detected occurrence. Other surfaces and other
// execution/review gates remain durable.
func clearResolvedCredentialManagementBlockers(script *dsl.Script, resolvedScopes map[string]struct{}) error {
	if len(resolvedScopes) == 0 {
		return nil
	}
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if _, resolved := resolvedScopes[dslWalkPathJSONPointer(ref.Path)]; !resolved {
			return nil
		}
		metadata := ref.Metadata()
		if len(metadata) == 0 {
			return nil
		}
		if blocker, ok := metadata["execution_blocker"].(string); ok &&
			strings.TrimSpace(blocker) == "import_credentials_unmanaged" {
			delete(metadata, "execution_blocker")
		}
		switch blockers := metadata["execution_blockers"].(type) {
		case []string:
			filtered := make([]string, 0, len(blockers))
			for _, blocker := range blockers {
				if strings.TrimSpace(blocker) != "import_credentials_unmanaged" {
					filtered = append(filtered, blocker)
				}
			}
			if len(filtered) == 0 {
				delete(metadata, "execution_blockers")
			} else {
				metadata["execution_blockers"] = filtered
			}
		case []any:
			filtered := make([]any, 0, len(blockers))
			for _, raw := range blockers {
				blocker, stringValue := raw.(string)
				if stringValue && strings.TrimSpace(blocker) == "import_credentials_unmanaged" {
					continue
				}
				filtered = append(filtered, raw)
			}
			if len(filtered) == 0 {
				delete(metadata, "execution_blockers")
			} else {
				metadata["execution_blockers"] = filtered
			}
		}
		ref.SetMetadata(metadata)
		return nil
	})
}

func selectedSecretBindingScopes(
	script *dsl.Script,
	scriptIndex int,
	bindings []secretBindingSelection,
) map[string]struct{} {
	if script == nil || len(bindings) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if id := strings.TrimSpace(binding.RequirementID); id != "" {
			selected[id] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return nil
	}
	scopes := make(map[string]struct{})
	if descriptors, err := loadProtectedImportCredentialDescriptors(script); err == nil {
		for _, descriptor := range descriptors {
			if _, ok := selected[descriptor.ID]; !ok {
				continue
			}
			for _, location := range descriptor.Locations {
				if scope := secretBindingActionScope(location.Path); scope != "" {
					scopes[scope] = struct{}{}
				}
			}
		}
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		return scopes
	}
	raw, err := scriptAsJSONValue(projected)
	if err != nil {
		return scopes
	}
	for _, requirement := range discoverSecretBindingRequirementsValue(raw, scriptIndex) {
		if _, ok := selected[requirement.ID]; !ok {
			continue
		}
		if scope := secretBindingActionScope(requirement.Path); scope != "" {
			scopes[scope] = struct{}{}
		}
	}
	return scopes
}

func secretBindingActionScope(path string) string {
	tokens, err := splitJSONPointer(path)
	if err != nil || len(tokens) < 2 || !isDecimalToken(tokens[1]) {
		return ""
	}
	switch tokens[0] {
	case "init", "actions", "teardown":
	default:
		return ""
	}
	scopeEnd := 2
	for scopeEnd+2 < len(tokens) {
		if tokens[scopeEnd] == "control" &&
			(tokens[scopeEnd+1] == "steps" || tokens[scopeEnd+1] == "else") &&
			isDecimalToken(tokens[scopeEnd+2]) {
			scopeEnd += 3
			continue
		}
		if tokens[scopeEnd] == "params" &&
			tokens[scopeEnd+1] == "requests" &&
			isDecimalToken(tokens[scopeEnd+2]) {
			scopeEnd += 3
		}
		break
	}
	return "/" + strings.Join(tokens[:scopeEnd], "/")
}

func dslWalkPathJSONPointer(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	var tokens []string
	for offset := 0; offset < len(path); {
		start := offset
		for offset < len(path) && path[offset] != '.' && path[offset] != '[' {
			offset++
		}
		if offset > start {
			tokens = append(tokens, path[start:offset])
		}
		if offset < len(path) && path[offset] == '.' {
			offset++
			continue
		}
		if offset < len(path) && path[offset] == '[' {
			end := strings.IndexByte(path[offset:], ']')
			if end < 0 {
				return ""
			}
			end += offset
			index := path[offset+1 : end]
			if !isDecimalToken(index) {
				return ""
			}
			tokens = append(tokens, index)
			offset = end + 1
		}
	}
	if len(tokens) < 2 {
		return ""
	}
	return "/" + strings.Join(tokens, "/")
}

func applyRedactedSecretBindings(script *dsl.Script, scriptIndex int, bindings []secretBindingSelection) (*dsl.Script, []SecretBindingRequirement, error) {
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
		name, err := engine.NormalizeLogicalSecretName(binding.SecretName)
		if err != nil || !logicalSecretNamePattern.MatchString(name) {
			return nil, nil, fmt.Errorf("secret_name for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", id)
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
	if err := reconcileWholeBodySecretBindingLocators(&result); err != nil {
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

// applyPreservedRedactedSecretBindings performs an operator-selected migration
// against the exact authenticated import draft. It replaces only the selected
// projected occurrence and leaves every other imported value byte-for-byte
// intact. When the disclosure projection cannot be uniquely aligned with the
// original scalar, the operation fails instead of canonicalizing or guessing.
func applyPreservedRedactedSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	bindings []secretBindingSelection,
) (*dsl.Script, []SecretBindingRequirement, error) {
	if script == nil {
		return nil, nil, fmt.Errorf("script is required")
	}
	projectedInput := maskScriptSecrets(script)
	if projectedInput == nil {
		return nil, nil, fmt.Errorf("failed to project script for secret binding")
	}
	rawRoot, err := scriptAsJSONValue(script)
	if err != nil {
		return nil, nil, err
	}
	projectedRoot, err := scriptAsJSONValue(projectedInput)
	if err != nil {
		return nil, nil, err
	}
	requirements := discoverSecretBindingRequirementsValue(projectedRoot, scriptIndex)
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
		name, normalizeErr := engine.NormalizeLogicalSecretName(binding.SecretName)
		if normalizeErr != nil || !logicalSecretNamePattern.MatchString(name) {
			return nil, nil, fmt.Errorf("secret_name for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", id)
		}
		selected[id] = name
	}

	replacementsByPath := make(map[string]map[int]string, len(selected))
	for id, name := range selected {
		requirement := known[id]
		if replacementsByPath[requirement.Path] == nil {
			replacementsByPath[requirement.Path] = make(map[int]string)
		}
		replacementsByPath[requirement.Path][requirement.Occurrence] =
			"{{secret." + name + "}}"
	}
	for path, replacements := range replacementsByPath {
		rawValue, rawOK := jsonPointerString(rawRoot, path)
		projectedValue, projectedOK := jsonPointerString(projectedRoot, path)
		if !rawOK || !projectedOK {
			return nil, requirements, &staleSecretBindingRequirementsError{requirements: requirements}
		}
		removed, aligned := extractLinearlyProjectedRedactions(rawValue, projectedValue)
		if !aligned {
			return nil, requirements, fmt.Errorf(
				"selected credential at %s cannot be rebound without changing imported wire bytes; preserve the destination value or re-import with a governed source reference",
				path,
			)
		}
		parts := strings.Split(projectedValue, maskedScriptSecret)
		if len(parts) != len(removed)+1 {
			return nil, requirements, &staleSecretBindingRequirementsError{requirements: requirements}
		}
		var rebuilt strings.Builder
		rebuilt.Grow(len(rawValue))
		rebuilt.WriteString(parts[0])
		for occurrence, sourceValue := range removed {
			if replacement, ok := replacements[occurrence]; ok {
				rebuilt.WriteString(replacement)
			} else {
				rebuilt.WriteString(sourceValue)
			}
			rebuilt.WriteString(parts[occurrence+1])
		}
		if !setJSONPointerStringValue(rawRoot, path, rebuilt.String()) {
			return nil, requirements, &staleSecretBindingRequirementsError{requirements: requirements}
		}
	}

	payload, err := json.Marshal(rawRoot)
	if err != nil {
		return nil, nil, err
	}
	var result dsl.Script
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, nil, err
	}
	if err := reconcileWholeBodySecretBindingLocators(&result); err != nil {
		return nil, nil, err
	}
	projectedResult := maskScriptSecrets(&result)
	if projectedResult == nil {
		return nil, nil, fmt.Errorf("failed to project resolved script")
	}
	remaining := discoverSecretBindingRequirements(projectedResult, scriptIndex)
	rawResult, err := scriptAsJSONValue(&result)
	if err != nil {
		return nil, nil, err
	}
	projectedResultRoot, err := scriptAsJSONValue(projectedResult)
	if err != nil {
		return nil, nil, err
	}
	// Alignment receives one script, while public descriptors retain the
	// original batch index. Do not interpret that batch index as a local node.
	for index := range remaining {
		remaining[index].ScriptIndex = 0
	}
	remaining = annotateSecretBindingDestinationValuesWithPolicy(remaining, rawResult, projectedResultRoot, true)
	for index := range remaining {
		remaining[index].ScriptIndex = scriptIndex
	}
	return &result, remaining, nil
}

func applySuggestedSecretBindings(script *dsl.Script, scriptIndex int) (*dsl.Script, []SecretBindingRequirement, error) {
	masked := maskScriptSecrets(script)
	if masked == nil {
		return nil, nil, fmt.Errorf("failed to project script for secret binding")
	}
	requirements := discoverSecretBindingRequirements(masked, scriptIndex)
	if len(requirements) == 0 {
		return masked, requirements, nil
	}
	bindings := make([]secretBindingSelection, 0, len(requirements))
	for _, requirement := range requirements {
		bindings = append(bindings, secretBindingSelection{
			RequirementID: requirement.ID,
			SecretName:    requirement.SuggestedName,
		})
	}
	resolved, remaining, err := applySecretBindings(masked, scriptIndex, bindings)
	if err != nil {
		return nil, requirements, err
	}
	if len(remaining) != 0 {
		return nil, requirements, fmt.Errorf("failed to apply every suggested secret binding")
	}
	return resolved, requirements, nil
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
	registry := newSecretSuggestionRegistry()
	for _, node := range nodes {
		reserveExistingSecretNames(node, registry)
	}
	for index, node := range nodes {
		result = append(result, discoverSecretBindingRequirementsValueWithRegistry(node, index, registry)...)
	}
	if result == nil {
		return []SecretBindingRequirement{}
	}
	return result
}

func annotateSecretBindingDestinationValues(
	requirements []SecretBindingRequirement,
	rawScripts any,
	projectedScripts any,
) []SecretBindingRequirement {
	return annotateSecretBindingDestinationValuesWithPolicy(requirements, rawScripts, projectedScripts, false)
}

func annotateSecretBindingDestinationValuesWithPolicy(requirements []SecretBindingRequirement, rawScripts, projectedScripts any, preserveExact bool) []SecretBindingRequirement {
	if len(requirements) == 0 {
		return requirements
	}
	rawNodes := make([]map[string]any, 0)
	projectedNodes := make([]map[string]any, 0)
	collectScriptJSONNodes(rawScripts, &rawNodes)
	collectScriptJSONNodes(projectedScripts, &projectedNodes)
	valueGroups := make(map[int]map[string]string)
	type location struct {
		script int
		path   string
	}
	alignedValues := make(map[location][]string)
	for index := range requirements {
		requirement := &requirements[index]
		requirement.ValueGroupID = ""
		requirement.DestinationValuePreserved = false
		scriptIndex := requirement.ScriptIndex
		if scriptIndex < 0 || scriptIndex >= len(rawNodes) ||
			scriptIndex >= len(projectedNodes) {
			continue
		}
		key := location{scriptIndex, requirement.Path}
		values, cached := alignedValues[key]
		if !cached {
			rawValue, rawOK := jsonPointerString(rawNodes[scriptIndex], requirement.Path)
			projectedValue, projectedOK := jsonPointerString(projectedNodes[scriptIndex], requirement.Path)
			if rawOK && projectedOK {
				values, _ = extractProjectedRedactions(rawValue, projectedValue)
			}
			alignedValues[key] = values
		}
		if requirement.Occurrence < 0 || requirement.Occurrence >= len(values) {
			continue
		}
		sourceValue := values[requirement.Occurrence]
		status := credentialpolicy.ClassifySourceCredentialValue(sourceValue)
		// An aligned empty source field remains exactly empty in the imported
		// destination. It is unavailable for migration, but requires no invented
		// credential during import. A whole structured body's scalar encoding
		// classification likewise cannot replace independently proven source
		// credential fields with a fabricated full-body reference.
		exactBody := status == credentialpolicy.SourceCredentialValueUnsafeEncoding &&
			structuredImportBodyCanRemainExact(rawNodes[scriptIndex], requirement.Path, sourceValue)
		requirement.DestinationValuePreserved = status.Available() ||
			status == credentialpolicy.SourceCredentialValueMissing || exactBody ||
			(preserveExact && sourceValue != "")
		if status.Available() {
			if valueGroups[scriptIndex] == nil {
				valueGroups[scriptIndex] = make(map[string]string)
			}
			group := valueGroups[scriptIndex][sourceValue]
			if group == "" {
				group = requirement.ID
				valueGroups[scriptIndex][sourceValue] = group
			}
			requirement.ValueGroupID = group
		}
		if !requirement.DestinationValuePreserved {
			setSecretBindingPreservationUnavailable(requirement, status)
		}
	}
	return requirements
}

// replaceUnavailableImportCredentialValues removes disclosure placeholders
// from the exact server-owned candidate before an opaque draft capability is
// issued. The placeholder is replaced with the same deterministic governed
// reference offered by review; it is never retained as executable auth data.
func replaceUnavailableImportCredentialValues(script *dsl.Script) (*dsl.Script, error) {
	if script == nil {
		return nil, errors.New("script is required")
	}
	working := cloneScriptDefinition(script)
	if working == nil {
		return nil, errors.New("failed to clone import script")
	}
	projected := maskScriptSecrets(working)
	if projected == nil {
		return nil, errors.New("failed to project import script")
	}
	rawRoot, err := scriptAsJSONValue(working)
	if err != nil {
		return nil, err
	}
	projectedRoot, err := scriptAsJSONValue(projected)
	if err != nil {
		return nil, err
	}
	requirements := discoverSecretBindingRequirementsValue(projectedRoot, 0)
	bindings := make([]secretBindingSelection, 0)
	for _, requirement := range requirements {
		rawValue, rawOK := jsonPointerString(rawRoot, requirement.Path)
		projectedValue, projectedOK := jsonPointerString(projectedRoot, requirement.Path)
		if !rawOK || !projectedOK {
			continue
		}
		values, aligned := extractProjectedRedactions(rawValue, projectedValue)
		if !aligned || requirement.Occurrence < 0 || requirement.Occurrence >= len(values) {
			continue
		}
		value := values[requirement.Occurrence]
		status := credentialpolicy.ClassifySourceCredentialValue(value)
		if status == credentialpolicy.SourceCredentialValueUnsafeEncoding &&
			structuredImportBodyCanRemainExact(rawRoot, requirement.Path, value) {
			continue
		}
		if status == credentialpolicy.SourceCredentialValueRedactedPlaceholder ||
			status == credentialpolicy.SourceCredentialValueUnsafeEncoding {
			bindings = append(bindings, secretBindingSelection{
				RequirementID: requirement.ID,
				SecretName:    requirement.SuggestedName,
			})
		}
	}
	if len(bindings) == 0 {
		return working, nil
	}
	resolved, _, err := applyPreservedRedactedSecretBindings(working, 0, bindings)
	if err != nil {
		return nil, fmt.Errorf("replace unavailable imported credential: %w", err)
	}
	return resolved, nil
}

// scriptHasUnavailableImportCredentialValues distinguishes an authored source
// placeholder from an ordinary disclosure projection. Such a draft still needs
// a one-shot server capability even though it contains no recoverable raw
// credential: the capability authorizes replacing the exact placeholder with
// a governed reference during mutation or commit.
func scriptHasUnavailableImportCredentialValues(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		return true
	}
	rawValue, err := scriptAsJSONValue(script)
	if err != nil {
		return true
	}
	projectedValue, err := scriptAsJSONValue(projected)
	if err != nil {
		return true
	}
	requirements := secretBindingRequirementsForImportScripts([]any{projectedValue})
	requirements = annotateSecretBindingDestinationValues(
		requirements,
		[]any{rawValue},
		[]any{projectedValue},
	)
	for _, requirement := range requirements {
		if requirement.PreservationUnavailableReason != "" {
			return true
		}
	}
	return false
}

func setSecretBindingPreservationUnavailable(
	requirement *SecretBindingRequirement,
	status credentialpolicy.SourceCredentialValueStatus,
) {
	if requirement == nil {
		return
	}
	switch status {
	case credentialpolicy.SourceCredentialValueRedactedPlaceholder:
		requirement.PreservationUnavailableReason = "redacted_source_value"
		requirement.PreservationUnavailableDescription = "The source contains a redaction placeholder rather than the original credential. Configure the destination secret before replay."
	case credentialpolicy.SourceCredentialValueUnsafeEncoding:
		requirement.PreservationUnavailableReason = "unsafe_source_encoding"
		requirement.PreservationUnavailableDescription = "The source credential uses an ambiguous or over-depth encoding and cannot be preserved safely. Configure the destination secret before replay."
	case credentialpolicy.SourceCredentialValueMissing:
		requirement.PreservationUnavailableReason = "source_value_unavailable"
		requirement.PreservationUnavailableDescription = "The source does not contain a recoverable credential value. Configure the destination secret before replay."
	}
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
	var mutationClaim *importDraftAttestationClaim
	if isCredentialPreservingImport(req.Script) {
		var claimErr error
		mutationClaim, claimErr = s.claimImportDraftMutation(r, req.Script)
		if claimErr != nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "credential-preserving import draft is invalid or expired; run the import again",
				"code":  "import_draft_attestation_invalid",
			})
			return
		}
		defer func() {
			if mutationClaim != nil {
				s.releaseImportDraftAttestation(mutationClaim)
			}
		}()
	}
	resolver := applySecretBindings
	mutationTarget := req.Script
	if mutationClaim != nil {
		resolver = applyCredentialPreservingSecretBindings
		if mutationClaim.Script == nil {
			writeError(w, http.StatusConflict, "credential-preserving import draft is invalid or expired; run the import again")
			return
		}
		mutationTarget = mutationClaim.Script
	}
	resolved, remaining, err := resolver(mutationTarget, req.ScriptIndex, req.Bindings)
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
	if remaining == nil {
		remaining = []SecretBindingRequirement{}
	}
	var expiresAt any
	if mutationClaim != nil {
		rotatedExpiry, rotateErr := s.completeImportDraftMutation(r, mutationClaim, resolved)
		if rotateErr != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":     "credential-preserving import draft could not be secured; retry the import",
				"code":      "import_draft_attestation_unavailable",
				"retryable": true,
			})
			return
		}
		mutationClaim = nil
		if !rotatedExpiry.IsZero() {
			expiresAt = rotatedExpiry
		}
	}
	response := map[string]any{
		"script":                      maskImportDraftForResponse(resolved),
		"secret_binding_requirements": remaining,
	}
	if expiresAt != nil {
		response["import_draft_expires_at"] = expiresAt
	}
	writeScriptImportResponse(w, http.StatusOK, response)
}

func writeSecretBindingsRequired(w http.ResponseWriter, requirements []SecretBindingRequirement) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":                       "script contains redacted secret values that require logical secret bindings",
		"code":                        secretBindingsRequiredCode,
		"secret_binding_requirements": requirements,
	})
}
