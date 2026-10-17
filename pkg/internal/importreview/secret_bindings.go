package importreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/engine"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	SecretBindingsRequiredCode           = "secret_bindings_required"
	SecretBindingRequirementsChangedCode = "secret_binding_requirements_changed"
	SecretBindingRebindUnavailableCode   = "secret_binding_rebind_unavailable"

	SecretBindingRebindUnavailableReason      = "destination_bytes_unaligned"
	SecretBindingRebindUnavailableDescription = "APEX can't swap this credential for a project-secret reference without changing other recorded bytes in the same field, so the field keeps its recorded value. To use a project secret here, edit the request after saving."
)

var (
	// The portable template parser permits at most 256 characters inside {{ }}.
	// "secret." consumes seven, leaving 249 for the caller-selected name.
	LogicalSecretNamePattern        = regexp.MustCompile(`^[A-Z0-9_]{1,249}$`)
	HttpProvenanceActionPathPattern = regexp.MustCompile(`^/(?:init|actions|teardown)/[0-9]+(?:/control/(?:steps|else|default|(?:cases|branches)/[0-9]+/steps)/[0-9]+)*$`)
	RedactedValueKeyPattern         = regexp.MustCompile(`(?i)(?:["'<]|^)([A-Za-z][A-Za-z0-9_.-]{0,63})(?:["'>])?\s*[:=]\s*["']?$`)
)

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
	// RebindUnavailableReason is set when the occurrence cannot be swapped for
	// a {{secret.NAME}} reference without changing other imported bytes in the
	// same field. The binding route refuses such a selection, so the review
	// keeps the recorded value instead of offering a project secret.
	RebindUnavailableReason      string `json:"rebind_unavailable_reason,omitempty"`
	RebindUnavailableDescription string `json:"rebind_unavailable_description,omitempty"`
	// ValueGroupID identifies equal recoverable values within this response only.
	// It is an occurrence ID, never a digest of credential material.
	ValueGroupID string `json:"value_group_id,omitempty"`
}

type SecretBindingSelection struct {
	RequirementID string `json:"requirement_id"`
	SecretName    string `json:"secret_name"`
}

type RedactionTraversalContext struct {
	httpProtocol        bool
	nonWireMetadataPath string
	actionName          string
	actionType          string
	headerName          string
}

type InternalSecretBindingRequirement struct {
	SecretBindingRequirement
	sourceValue string
}

type StaleSecretBindingRequirementsError struct {
	requirements []SecretBindingRequirement
}

type SecretBindingRebindUnavailableError struct {
	requirement SecretBindingRequirement
	location    string
	// requirements is the refreshed, annotated list, so a review that predates
	// the rebind flag can update itself instead of offering the choice again.
	requirements []SecretBindingRequirement
}

func (err *SecretBindingRebindUnavailableError) Error() string {
	label := strings.TrimSpace(err.requirement.Label)
	if label == "" {
		label = "Credential"
	}
	return fmt.Sprintf(
		"%s in %s can't use a project secret: APEX can't swap it in without changing other recorded bytes in that field. Keep its recorded value instead.",
		label,
		err.location,
	)
}

func (err *StaleSecretBindingRequirementsError) Error() string {
	return "secret binding requirements changed; review the refreshed locations and retry"
}

func DiscoverSecretBindingRequirements(script *dsl.Script, scriptIndex int) []SecretBindingRequirement {
	// Deliberately NOT gated on secretBindingEnforcementDisabled. Discovery is
	// what turns a [REDACTED] sentinel into a guided binding; suppressing it while
	// reconcileMaskedScriptSecrets still rejects orphan sentinels left create and
	// update failing with a flat 400 and no requirement list to act on. The dev
	// bypass only needs to affect RUNTIME resolution, which it still does.
	if script == nil {
		return []SecretBindingRequirement{}
	}
	raw, err := ScriptAsJSONValue(script)
	if err != nil {
		return []SecretBindingRequirement{}
	}
	return DiscoverSecretBindingRequirementsValue(raw, scriptIndex)
}

func DiscoverSecretBindingRequirementsValue(raw any, scriptIndex int) []SecretBindingRequirement {
	registry := NewSecretSuggestionRegistry()
	ReserveExistingSecretNames(raw, registry)
	return DiscoverSecretBindingRequirementsValueWithRegistry(raw, scriptIndex, registry)
}

type SecretSuggestionRegistry struct {
	used map[string]struct{}
	next map[string]int
}

func NewSecretSuggestionRegistry() *SecretSuggestionRegistry {
	return &SecretSuggestionRegistry{
		used: make(map[string]struct{}),
		next: make(map[string]int),
	}
}

func (registry *SecretSuggestionRegistry) Reserve(name string) {
	if registry == nil {
		return
	}
	name, err := engine.NormalizeLogicalSecretName(name)
	if err != nil || !LogicalSecretNamePattern.MatchString(name) {
		return
	}
	registry.used[name] = struct{}{}
	if registry.next[name] < 2 {
		registry.next[name] = 2
	}
}

func (registry *SecretSuggestionRegistry) Allocate(base string) string {
	if registry == nil {
		registry = NewSecretSuggestionRegistry()
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

func ReserveExistingSecretNames(raw any, registry *SecretSuggestionRegistry) {
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
		registry.Reserve(name)
	}
}

func DiscoverSecretBindingRequirementsValueWithRegistry(raw any, scriptIndex int, registry *SecretSuggestionRegistry) []SecretBindingRequirement {
	scriptMap, ok := raw.(map[string]any)
	if !ok || !IsScriptJSONObject(scriptMap) {
		return []SecretBindingRequirement{}
	}
	scriptID, _ := scriptMap["id"].(string)
	scriptName, _ := scriptMap["name"].(string)
	internal := make([]InternalSecretBindingRequirement, 0)
	DiscoverRedactedValues(scriptMap, "", RedactionTraversalContext{}, scriptIndex, scriptID, scriptName, &internal)

	// Redaction destroys identity, so every occurrence in an import batch gets a
	// distinct default. Users can still explicitly choose the same logical name
	// when they know two locations intentionally share one credential.
	result := make([]SecretBindingRequirement, 0, len(internal))
	for _, requirement := range internal {
		suggestedName := registry.Allocate(requirement.SuggestedName)
		if suggestedName != requirement.SuggestedName {
			requirement.SuggestedName = suggestedName
			requirement.Replacement = ReplaceRedactedOccurrence(
				requirement.sourceValue,
				requirement.Occurrence,
				"{{secret."+requirement.SuggestedName+"}}",
			)
		}
		result = append(result, requirement.SecretBindingRequirement)
	}
	return result
}

func DiscoverRedactedValues(value any, path string, ctx RedactionTraversalContext, scriptIndex int, scriptID, scriptName string, out *[]InternalSecretBindingRequirement) {
	switch typed := value.(type) {
	case map[string]any:
		if IsNonExecutableReviewAction(typed) || IsDurablyImportReviewGatedAction(typed) {
			return
		}
		ctx = RedactionContextForMap(typed, path, ctx)
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if IsNonExecutableRedactionSubtree(typed, key, path) {
				continue
			}
			childPath := AppendJSONPointer(path, key)
			// JVM argv has no secure secret-injection equivalent. Unsafe values
			// are rejected by the write boundary and masked only for legacy read
			// projections; never turn that response sentinel into a project
			// secret requirement or a forbidden {{secret.*}} JVM argument.
			if IsJavaVMParameterJSONPath(childPath) {
				continue
			}
			// A data source's inline rows are a runtime data table, not a
			// credential field. When an importer redacts credential-named
			// parameter rows it also attaches a durable execution gate whose
			// resolution is "supply an approved data source" — one project
			// secret cannot stand in for a multi-row table, so offering a
			// binding here would let an operator clear the gate without
			// restoring the data. Discovery-only, like the skips above.
			if IsRuntimeDataTableJSONPath(childPath) {
				continue
			}
			// Import provenance recorded in action metadata (original_url,
			// source_transaction) documents where the action came from and is
			// never sent, so it must
			// not demand a credential binding. This skip is deliberately scoped to
			// requirement DISCOVERY: redaction reconciliation still walks metadata,
			// so a masked read-modify-write cannot overwrite the stored value.
			if IsImportProvenanceMetadataPathForContext(childPath, ctx) {
				continue
			}
			if IsImportCredentialLocatorMetadataPath(childPath) {
				continue
			}
			DiscoverRedactedValues(typed[key], childPath, ctx, scriptIndex, scriptID, scriptName, out)
		}
	case []any:
		for index, child := range typed {
			DiscoverRedactedValues(child, AppendJSONPointer(path, strconv.Itoa(index)), ctx, scriptIndex, scriptID, scriptName, out)
		}
	case string:
		count := strings.Count(typed, MaskedScriptSecret)
		for occurrence := 0; occurrence < count; occurrence++ {
			suggested := SuggestedSecretName(path, typed, occurrence, ctx)
			replacement := ReplaceRedactedOccurrence(typed, occurrence, "{{secret."+suggested+"}}")
			*out = append(*out, InternalSecretBindingRequirement{SecretBindingRequirement: SecretBindingRequirement{
				ID:            SecretBindingRequirementID(scriptIndex, scriptID, scriptName, path, occurrence, typed, ctx),
				Path:          path,
				Occurrence:    occurrence,
				Label:         SecretBindingLabel(path, ctx),
				Context:       SecretBindingContext(path, ctx),
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

func IsImportCredentialLocatorMetadataPath(path string) bool {
	tokens, err := SplitJSONPointer(path)
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

func IsRuntimeDataTableJSONPath(path string) bool {
	tokens, err := SplitJSONPointer(path)
	if err != nil || len(tokens) != 3 {
		return false
	}
	return tokens[0] == "data_sources" && tokens[2] == "inline_data"
}

func IsJavaVMParameterJSONPath(path string) bool {
	tokens, err := SplitJSONPointer(path)
	if err != nil || len(tokens) < 3 {
		return false
	}
	return tokens[0] == "config" &&
		tokens[1] == "java_vuser" &&
		tokens[2] == "additional_vm_parameters"
}

func RedactionContextForMap(value map[string]any, path string, prior RedactionTraversalContext) RedactionTraversalContext {
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
		if strings.HasPrefix(path, prefix) && IsDecimalToken(strings.TrimPrefix(path, prefix)) {
			prior.nonWireMetadataPath = path + "/metadata"
			prior.actionType = "request"
			return prior
		}
	}

	if IsHeaderListEntryJSONPath(path) {
		headerName, _ := value["name"].(string)
		if headerName = strings.TrimSpace(headerName); headerName != "" &&
			!strings.Contains(headerName, MaskedScriptSecret) {
			prior.headerName = headerName
		}
		return prior
	}
	if !IsActionObjectJSONPath(path) && !HttpProvenanceActionPathPattern.MatchString(path) {
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
	if HttpProvenanceActionPathPattern.MatchString(path) {
		switch canonical := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(actionType))); {
		case prior.httpProtocol && IsHTTPProvenanceActionType(canonical):
			prior.nonWireMetadataPath = path + "/metadata"
		case IsEngineProvenanceActionType(canonical):
			// Engine actions (imported source_* steps, custom_call, control
			// flow) run inside the engine; no protocol ever sends their
			// metadata, whatever the script's protocol.
			prior.nonWireMetadataPath = path + "/metadata"
		}
	}
	return prior
}

func IsHTTPProvenanceActionType(canonical string) bool {
	switch canonical {
	case "request", "get", "post", "put", "patch", "delete", "head", "options", "trace", "connect", "http_sequence", "http_oauth2_token", "source_transaction":
		return true
	}
	return false
}

func IsEngineProvenanceActionType(canonical string) bool {
	spec, ok := dsl.SpecForActionType(canonical)
	return ok && spec.EngineAction()
}

func IsHeaderListEntryJSONPath(path string) bool {
	tokens, err := SplitJSONPointer(path)
	if err != nil || len(tokens) < 2 || !IsDecimalToken(tokens[len(tokens)-1]) {
		return false
	}
	switch NormalizeScriptFieldName(tokens[len(tokens)-2]) {
	case "headerlist", "defaultheaderlist":
		return true
	default:
		return false
	}
}

func IsActionObjectJSONPath(path string) bool {
	tokens, err := SplitJSONPointer(path)
	if err != nil || len(tokens) < 2 || !IsDecimalToken(tokens[len(tokens)-1]) {
		return false
	}
	switch tokens[len(tokens)-2] {
	case "actions", "init", "teardown", "steps", "else":
		return true
	default:
		return false
	}
}

func IsScriptActionJSONValue(value map[string]any) bool {
	_, hasName := value["name"]
	_, hasType := value["type"]
	_, hasTarget := value["target"]
	return hasName && hasType && hasTarget
}

func IsNonExecutableReviewAction(value map[string]any) bool {
	if !IsScriptActionJSONValue(value) {
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

func IsDurablyImportReviewGatedAction(value map[string]any) bool {
	if !IsScriptActionJSONValue(value) {
		return false
	}
	metadata, _ := value["metadata"].(map[string]any)
	reviewRequired := false
	for _, blocker := range ExecutionReviewBlockerCodes(metadata) {
		switch blocker {
		case "import_credentials_unmanaged":
			return false
		case "import_review_required":
			reviewRequired = true
		}
	}
	return reviewRequired
}

func IsImportProvenanceMetadataPath(path string) bool {
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return false
	}
	if segments[len(segments)-2] != "metadata" {
		return false
	}
	return IsImportProvenanceMetadataKey(segments[len(segments)-1])
}

func IsImportProvenanceMetadataPathForContext(path string, ctx RedactionTraversalContext) bool {
	if IsImportProvenanceMetadataPath(path) {
		return true
	}
	separator := strings.LastIndexByte(path, '/')
	if separator < 0 || ctx.nonWireMetadataPath == "" || path[:separator] != ctx.nonWireMetadataPath {
		return false
	}
	_, ok := HttpImportProvenanceEvidenceKeys[NormalizeImportProvenanceKey(path[separator+1:])]
	return ok
}

func IsNonExecutableRedactionSubtree(parent map[string]any, key string, path string) bool {
	if path == "" {
		switch key {
		case "id", "name", "version", "protocol", "project_id", "folder_id", "generated_by", "generated_from", "metadata":
			return true
		}
	}
	if IsScriptActionJSONValue(parent) {
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
	if key == "metadata" && IsNonExecutableReviewAction(parent) {
		return true
	}
	return false
}

func SecretBindingRequirementID(scriptIndex int, scriptID, scriptName, path string, occurrence int, sourceValue string, ctx RedactionTraversalContext) string {
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

func AppendJSONPointer(path, token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return path + "/" + token
}

func SplitJSONPointer(path string) ([]string, error) {
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

func SecretBindingLabel(path string, ctx RedactionTraversalContext) string {
	if headerName := strings.TrimSpace(ctx.headerName); headerName != "" {
		return headerName + " request header"
	}
	tokens, _ := SplitJSONPointer(path)
	if len(tokens) == 0 {
		return "Secret value"
	}
	key := tokens[len(tokens)-1]
	if len(tokens) > 1 {
		parent := NormalizeScriptFieldName(tokens[len(tokens)-2])
		if parent == "headers" || parent == "defaultheaders" {
			return HumanizeSecretLabel(key) + " header"
		}
	}
	switch NormalizeScriptFieldName(key) {
	case "body":
		return "Request body"
	case "target":
		return "Request target"
	case "baseurl":
		return "Base URL"
	default:
		return HumanizeSecretLabel(key)
	}
}

func HumanizeSecretLabel(value string) string {
	value = strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(value))
	if value == "" {
		return "Secret value"
	}
	runes := []rune(strings.ToLower(value))
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func SecretBindingContext(path string, ctx RedactionTraversalContext) string {
	if headerName := strings.TrimSpace(ctx.headerName); headerName != "" {
		if name := strings.TrimSpace(ctx.actionName); name != "" && !strings.Contains(name, MaskedScriptSecret) {
			return fmt.Sprintf("Sensitive request header %q on action %q. Supplies the credential value sent in the %s HTTP header when this request runs.", headerName, name, headerName)
		}
		return fmt.Sprintf("Sensitive request header %q. Supplies the credential value sent in the %s HTTP header when this request runs.", headerName, headerName)
	}
	if name := strings.TrimSpace(ctx.actionName); name != "" && !strings.Contains(name, MaskedScriptSecret) {
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

func SuggestedSecretName(path, value string, occurrence int, ctx RedactionTraversalContext) string {
	hint := RedactedValueHint(value, occurrence)
	tokens, _ := SplitJSONPointer(path)
	if hint == "" && len(tokens) > 0 {
		hint = tokens[len(tokens)-1]
	}
	if strings.EqualFold(hint, "authorization") {
		prefix := StringBeforeRedactedOccurrence(value, occurrence)
		switch {
		case strings.HasSuffix(strings.ToLower(strings.TrimSpace(prefix)), "bearer"):
			hint = "token"
		case strings.HasSuffix(strings.ToLower(strings.TrimSpace(prefix)), "basic"):
			hint = "credentials"
		}
	}
	parts := make([]string, 0, 4)
	if name := strings.TrimSpace(ctx.actionName); name != "" && !strings.Contains(name, MaskedScriptSecret) {
		parts = append(parts, name)
	} else {
		for _, token := range tokens {
			normalized := NormalizeScriptFieldName(token)
			if normalized == "" || normalized == "config" || normalized == "actions" || normalized == "init" || normalized == "teardown" ||
				normalized == "metadata" || normalized == "headers" || normalized == "defaultheaders" || IsDecimalToken(token) {
				continue
			}
			parts = append(parts, token)
		}
		if len(parts) > 2 {
			parts = parts[len(parts)-2:]
		}
	}
	if hint != "" && (len(parts) == 0 || NormalizeScriptFieldName(parts[len(parts)-1]) != NormalizeScriptFieldName(hint)) {
		parts = append(parts, hint)
	}
	name := NormalizeLogicalSecretName(strings.Join(parts, "_"))
	if name == "" {
		name = "IMPORTED_SECRET"
	}
	return name
}

func RedactedValueHint(value string, occurrence int) string {
	prefix := StringBeforeRedactedOccurrence(value, occurrence)
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(prefix)), "bearer") {
		return "token"
	}
	if match := RedactedValueKeyPattern.FindStringSubmatch(prefix); len(match) == 2 {
		return match[1]
	}
	return ""
}

func StringBeforeRedactedOccurrence(value string, occurrence int) string {
	offset := 0
	for current := 0; ; current++ {
		index := strings.Index(value[offset:], MaskedScriptSecret)
		if index < 0 {
			return value
		}
		index += offset
		if current == occurrence {
			return value[:index]
		}
		offset = index + len(MaskedScriptSecret)
	}
}

func NormalizeLogicalSecretName(value string) string {
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

func IsDecimalToken(value string) bool {
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

func ReplaceRedactedOccurrence(value string, occurrence int, replacement string) string {
	offset := 0
	for current := 0; ; current++ {
		index := strings.Index(value[offset:], MaskedScriptSecret)
		if index < 0 {
			return value
		}
		index += offset
		if current == occurrence {
			return value[:index] + replacement + value[index+len(MaskedScriptSecret):]
		}
		offset = index + len(MaskedScriptSecret)
	}
}

func ApplySecretBindings(script *dsl.Script, scriptIndex int, bindings []SecretBindingSelection) (*dsl.Script, []SecretBindingRequirement, error) {
	return ApplySecretBindingsWithResolver(
		script,
		scriptIndex,
		bindings,
		ApplyRedactedSecretBindings,
	)
}

type RedactedSecretBindingResolver func(
	*dsl.Script,
	int,
	[]SecretBindingSelection,
) (*dsl.Script, []SecretBindingRequirement, error)

func ApplyCredentialPreservingSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	bindings []SecretBindingSelection,
) (*dsl.Script, []SecretBindingRequirement, error) {
	return ApplySecretBindingsWithResolver(
		script,
		scriptIndex,
		bindings,
		ApplyPreservedRedactedSecretBindings,
	)
}

func ApplySecretBindingsWithResolver(
	script *dsl.Script,
	scriptIndex int,
	bindings []SecretBindingSelection,
	resolveRedacted RedactedSecretBindingResolver,
) (*dsl.Script, []SecretBindingRequirement, error) {
	if err := ValidateJavaVMParameterDisclosureSafety(script); err != nil {
		return nil, nil, err
	}
	selectedScopes := SelectedSecretBindingScopes(script, scriptIndex, bindings)
	protectedResolved, legacyBindings, protectedRemaining, err := ApplyProtectedImportCredentialBindings(script, bindings)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateJavaVMParameterDisclosureSafety(protectedResolved); err != nil {
		return nil, nil, err
	}
	resolved, legacyRemaining, err := resolveRedacted(protectedResolved, scriptIndex, legacyBindings)
	if err != nil {
		var stale *StaleSecretBindingRequirementsError
		if errors.As(err, &stale) {
			combined := append(append([]SecretBindingRequirement(nil), protectedRemaining...), stale.requirements...)
			return nil, combined, &StaleSecretBindingRequirementsError{requirements: combined}
		}
		var unavailable *SecretBindingRebindUnavailableError
		if errors.As(err, &unavailable) {
			// Like a stale refusal, the refreshed list is the whole review.
			combined := append(append([]SecretBindingRequirement(nil), protectedRemaining...), unavailable.requirements...)
			refusal := *unavailable
			refusal.requirements = combined
			return nil, combined, &refusal
		}
		return nil, nil, err
	}
	if err := ValidateJavaVMParameterDisclosureSafety(resolved); err != nil {
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
			if scope := SecretBindingActionScope(requirement.Path); scope != "" {
				delete(selectedScopes, scope)
			}
		}
		if descriptors, descriptorErr := LoadProtectedImportCredentialDescriptors(resolved); descriptorErr == nil {
			for _, descriptor := range descriptors {
				for _, location := range descriptor.Locations {
					if scope := SecretBindingActionScope(location.Path); scope != "" {
						delete(selectedScopes, scope)
					}
				}
			}
		}
		if err := ClearResolvedCredentialManagementBlockers(resolved, selectedScopes); err != nil {
			return nil, nil, fmt.Errorf("clear resolved credential management blockers: %w", err)
		}
	}
	return resolved, remaining, nil
}

func ClearResolvedCredentialManagementBlockers(script *dsl.Script, resolvedScopes map[string]struct{}) error {
	if len(resolvedScopes) == 0 {
		return nil
	}
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if _, resolved := resolvedScopes[DslWalkPathJSONPointer(ref.Path)]; !resolved {
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
				blocker, StringValue := raw.(string)
				if StringValue && strings.TrimSpace(blocker) == "import_credentials_unmanaged" {
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

func SelectedSecretBindingScopes(
	script *dsl.Script,
	scriptIndex int,
	bindings []SecretBindingSelection,
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
	if descriptors, err := LoadProtectedImportCredentialDescriptors(script); err == nil {
		for _, descriptor := range descriptors {
			if _, ok := selected[descriptor.ID]; !ok {
				continue
			}
			for _, location := range descriptor.Locations {
				if scope := SecretBindingActionScope(location.Path); scope != "" {
					scopes[scope] = struct{}{}
				}
			}
		}
	}
	projected := MaskScriptSecrets(script)
	if projected == nil {
		return scopes
	}
	raw, err := ScriptAsJSONValue(projected)
	if err != nil {
		return scopes
	}
	for _, requirement := range DiscoverSecretBindingRequirementsValue(raw, scriptIndex) {
		if _, ok := selected[requirement.ID]; !ok {
			continue
		}
		if scope := SecretBindingActionScope(requirement.Path); scope != "" {
			scopes[scope] = struct{}{}
		}
	}
	return scopes
}

func SecretBindingActionScope(path string) string {
	tokens, err := SplitJSONPointer(path)
	if err != nil || len(tokens) < 2 || !IsDecimalToken(tokens[1]) {
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
			IsDecimalToken(tokens[scopeEnd+2]) {
			scopeEnd += 3
			continue
		}
		if tokens[scopeEnd] == "params" &&
			tokens[scopeEnd+1] == "requests" &&
			IsDecimalToken(tokens[scopeEnd+2]) {
			scopeEnd += 3
		}
		break
	}
	return "/" + strings.Join(tokens[:scopeEnd], "/")
}

func DslWalkPathJSONPointer(path string) string {
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
			if !IsDecimalToken(index) {
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

func ApplyRedactedSecretBindings(script *dsl.Script, scriptIndex int, bindings []SecretBindingSelection) (*dsl.Script, []SecretBindingRequirement, error) {
	if script == nil {
		return nil, nil, fmt.Errorf("script is required")
	}
	projectedInput := MaskScriptSecrets(script)
	if projectedInput == nil {
		return nil, nil, fmt.Errorf("failed to project script for secret binding")
	}
	raw, err := ScriptAsJSONValue(projectedInput)
	if err != nil {
		return nil, nil, err
	}
	requirements := DiscoverSecretBindingRequirementsValue(raw, scriptIndex)
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
			return nil, requirements, &StaleSecretBindingRequirementsError{requirements: requirements}
		}
		name, err := engine.NormalizeLogicalSecretName(binding.SecretName)
		if err != nil || !LogicalSecretNamePattern.MatchString(name) {
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
	resolved := ApplySecretBindingReplacements(raw, "", replacements)
	payload, err := json.Marshal(resolved)
	if err != nil {
		return nil, nil, err
	}
	var result dsl.Script
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, nil, err
	}
	if err := ReconcileWholeBodySecretBindingLocators(&result); err != nil {
		return nil, nil, err
	}
	// Compute remaining requirements from the same disclosure-safe projection
	// returned to the client. This catches any other inline credential that the
	// caller submitted unmasked and prevents a response from claiming resolution
	// while its returned script contains a newly projected sentinel.
	projected := MaskScriptSecrets(&result)
	if projected == nil {
		return nil, nil, fmt.Errorf("failed to project resolved script")
	}
	remaining := DiscoverSecretBindingRequirements(projected, scriptIndex)
	return projected, remaining, nil
}

func ApplyPreservedRedactedSecretBindings(
	script *dsl.Script,
	scriptIndex int,
	bindings []SecretBindingSelection,
) (*dsl.Script, []SecretBindingRequirement, error) {
	if script == nil {
		return nil, nil, fmt.Errorf("script is required")
	}
	projectedInput := MaskScriptSecrets(script)
	if projectedInput == nil {
		return nil, nil, fmt.Errorf("failed to project script for secret binding")
	}
	rawRoot, err := ScriptAsJSONValue(script)
	if err != nil {
		return nil, nil, err
	}
	projectedRoot, err := ScriptAsJSONValue(projectedInput)
	if err != nil {
		return nil, nil, err
	}
	requirements := DiscoverSecretBindingRequirementsValue(projectedRoot, scriptIndex)
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
			return nil, requirements, &StaleSecretBindingRequirementsError{requirements: requirements}
		}
		name, normalizeErr := engine.NormalizeLogicalSecretName(binding.SecretName)
		if normalizeErr != nil || !LogicalSecretNamePattern.MatchString(name) {
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
	paths := make([]string, 0, len(replacementsByPath))
	for path := range replacementsByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		replacements := replacementsByPath[path]
		rawValue, rawOK := JsonPointerString(rawRoot, path)
		projectedValue, projectedOK := JsonPointerString(projectedRoot, path)
		if !rawOK || !projectedOK {
			return nil, requirements, &StaleSecretBindingRequirementsError{requirements: requirements}
		}
		// The field is rebuilt from the projection's text around each
		// sentinel, so that text must hold the recorded message IDs, not the
		// UUID template review shows in their place.
		projectedValue = ProjectedMessageIDsAsRecorded(rawValue, projectedValue)
		removed, aligned := ExtractLinearlyProjectedRedactions(rawValue, projectedValue)
		if !aligned {
			refreshed := AnnotateSingleScriptSecretBindingRequirements(
				append([]SecretBindingRequirement(nil), requirements...), scriptIndex, rawRoot, projectedRoot, false,
			)
			return nil, refreshed, &SecretBindingRebindUnavailableError{
				requirement:  FirstSelectedRequirementAt(path, selected, known),
				location:     SecretBindingLocation(projectedRoot, path),
				requirements: refreshed,
			}
		}
		parts := strings.Split(projectedValue, MaskedScriptSecret)
		if len(parts) != len(removed)+1 {
			return nil, requirements, &StaleSecretBindingRequirementsError{requirements: requirements}
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
		if !SetJSONPointerStringValue(rawRoot, path, rebuilt.String()) {
			return nil, requirements, &StaleSecretBindingRequirementsError{requirements: requirements}
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
	if err := ReconcileWholeBodySecretBindingLocators(&result); err != nil {
		return nil, nil, err
	}
	projectedResult := MaskScriptSecrets(&result)
	if projectedResult == nil {
		return nil, nil, fmt.Errorf("failed to project resolved script")
	}
	remaining := DiscoverSecretBindingRequirements(projectedResult, scriptIndex)
	rawResult, err := ScriptAsJSONValue(&result)
	if err != nil {
		return nil, nil, err
	}
	projectedResultRoot, err := ScriptAsJSONValue(projectedResult)
	if err != nil {
		return nil, nil, err
	}
	remaining = AnnotateSingleScriptSecretBindingRequirements(remaining, scriptIndex, rawResult, projectedResultRoot, true)
	return &result, remaining, nil
}

func AnnotateSingleScriptSecretBindingRequirements(
	requirements []SecretBindingRequirement,
	scriptIndex int,
	raw, projected any,
	preserveExact bool,
) []SecretBindingRequirement {
	for index := range requirements {
		requirements[index].ScriptIndex = 0
	}
	requirements = AnnotateSecretBindingDestinationValuesWithPolicy(requirements, raw, projected, preserveExact)
	for index := range requirements {
		requirements[index].ScriptIndex = scriptIndex
	}
	return requirements
}

func FirstSelectedRequirementAt(
	path string,
	selected map[string]string,
	known map[string]SecretBindingRequirement,
) SecretBindingRequirement {
	var first SecretBindingRequirement
	found := false
	for id := range selected {
		requirement := known[id]
		if requirement.Path != path {
			continue
		}
		if !found || requirement.Occurrence < first.Occurrence ||
			(requirement.Occurrence == first.Occurrence && requirement.ID < first.ID) {
			first, found = requirement, true
		}
	}
	return first
}

func SecretBindingLocation(root any, path string) string {
	tokens, err := SplitJSONPointer(path)
	if err == nil && len(tokens) >= 2 && IsDecimalToken(tokens[1]) {
		section := ""
		switch tokens[0] {
		case "init":
			section = "init action"
		case "actions":
			section = "action"
		case "teardown":
			section = "teardown action"
		}
		if index, convErr := strconv.Atoi(tokens[1]); section != "" && convErr == nil {
			location := fmt.Sprintf("%s %d", section, index+1)
			name, _ := JsonPointerString(root, "/"+tokens[0]+"/"+tokens[1]+"/name")
			if name = strings.TrimSpace(name); name != "" && !strings.Contains(name, MaskedScriptSecret) {
				location += fmt.Sprintf(" (%q)", name)
			}
			// A field inside a control-flow step or a batched request names
			// that step too, so the operator can find it in the action.
			nestedNames := map[string]string{
				"steps": "step", "else": "else step", "default": "default step",
				"cases": "case", "branches": "branch", "requests": "request",
			}
			for at := 2; at+1 < len(tokens); at++ {
				noun, nested := nestedNames[tokens[at]]
				if !nested || !IsDecimalToken(tokens[at+1]) {
					continue
				}
				position, _ := strconv.Atoi(tokens[at+1])
				location += fmt.Sprintf(", %s %d", noun, position+1)
				at++
			}
			return location
		}
	}
	switch {
	case strings.HasPrefix(path, "/config/"):
		return "the script configuration"
	case strings.HasPrefix(path, "/data_sources/"):
		return "a data source"
	case strings.HasPrefix(path, "/parameters/"):
		return "a script parameter"
	default:
		return "the script"
	}
}

func ApplySecretBindingReplacements(value any, path string, replacements map[string]map[int]string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = ApplySecretBindingReplacements(child, AppendJSONPointer(path, key), replacements)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = ApplySecretBindingReplacements(child, AppendJSONPointer(path, strconv.Itoa(index)), replacements)
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
			index := strings.Index(typed[offset:], MaskedScriptSecret)
			if index < 0 {
				builder.WriteString(typed[offset:])
				break
			}
			index += offset
			builder.WriteString(typed[offset:index])
			if replacement, ok := byOccurrence[occurrence]; ok {
				builder.WriteString(replacement)
			} else {
				builder.WriteString(MaskedScriptSecret)
			}
			offset = index + len(MaskedScriptSecret)
			occurrence++
		}
		return builder.String()
	default:
		return value
	}
}

func SecretBindingRequirementsForImportScripts(value any) []SecretBindingRequirement {
	nodes := make([]map[string]any, 0)
	CollectScriptJSONNodes(value, &nodes)
	result := make([]SecretBindingRequirement, 0)
	registry := NewSecretSuggestionRegistry()
	for _, node := range nodes {
		ReserveExistingSecretNames(node, registry)
	}
	for index, node := range nodes {
		result = append(result, DiscoverSecretBindingRequirementsValueWithRegistry(node, index, registry)...)
	}
	if result == nil {
		return []SecretBindingRequirement{}
	}
	return result
}

func AnnotateSecretBindingDestinationValues(
	requirements []SecretBindingRequirement,
	rawScripts any,
	projectedScripts any,
) []SecretBindingRequirement {
	return AnnotateSecretBindingDestinationValuesWithPolicy(requirements, rawScripts, projectedScripts, false)
}

func AnnotateSecretBindingDestinationValuesWithPolicy(requirements []SecretBindingRequirement, rawScripts, projectedScripts any, preserveExact bool) []SecretBindingRequirement {
	if len(requirements) == 0 {
		return requirements
	}
	rawNodes := make([]map[string]any, 0)
	projectedNodes := make([]map[string]any, 0)
	CollectScriptJSONNodes(rawScripts, &rawNodes)
	CollectScriptJSONNodes(projectedScripts, &projectedNodes)
	valueGroups := make(map[int]map[string]string)
	type location struct {
		script int
		path   string
	}
	// One field's alignment, shared by its occurrences. linear reports the
	// byte-exact alignment that binding splices with; values come from it or
	// from the structural JSON fallback.
	type fieldAlignment struct {
		found       bool
		linear      bool
		values      []string
		placeholder bool
	}
	alignments := make(map[location]fieldAlignment)
	annotate := func(requirement *SecretBindingRequirement) {
		scriptIndex := requirement.ScriptIndex
		if scriptIndex < 0 || scriptIndex >= len(rawNodes) ||
			scriptIndex >= len(projectedNodes) {
			return
		}
		key := location{scriptIndex, requirement.Path}
		alignment, cached := alignments[key]
		if !cached {
			rawValue, rawOK := JsonPointerString(rawNodes[scriptIndex], requirement.Path)
			projectedValue, projectedOK := JsonPointerString(projectedNodes[scriptIndex], requirement.Path)
			if rawOK && projectedOK {
				alignment.found = true
				alignment.values, alignment.linear = ExtractLinearlyProjectedRedactions(rawValue, projectedValue)
				if !alignment.linear {
					alignment.values, _ = ExtractStructurallyProjectedJSONRedactions(rawValue, projectedValue)
				}
				alignment.placeholder = len(alignment.values) == 0 &&
					RecordedPlaceholdersCoverProjection(rawValue, projectedValue)
			}
			alignments[key] = alignment
		}
		if !alignment.found {
			return
		}
		if !alignment.linear {
			requirement.RebindUnavailableReason = SecretBindingRebindUnavailableReason
			requirement.RebindUnavailableDescription = SecretBindingRebindUnavailableDescription
		}
		if len(alignment.values) == 0 {
			// No exact alignment isolates this occurrence, but the field still
			// holds the imported bytes verbatim, so it stays as recorded. A
			// field whose masked values were all recorded placeholders has no
			// usable value.
			if alignment.placeholder {
				SetSecretBindingPreservationUnavailable(requirement, credentialpolicy.SourceCredentialValueRedactedPlaceholder)
			} else {
				requirement.DestinationValuePreserved = true
			}
			return
		}
		values := alignment.values
		if requirement.Occurrence < 0 || requirement.Occurrence >= len(values) {
			return
		}
		sourceValue := values[requirement.Occurrence]
		status := credentialpolicy.ClassifySourceCredentialValue(sourceValue)
		// An aligned empty source field remains exactly empty in the imported
		// destination. It is unavailable for migration, but requires no invented
		// credential during import. A whole structured body's scalar encoding
		// classification likewise cannot replace independently proven source
		// credential fields with a fabricated full-body reference.
		exactBody := status == credentialpolicy.SourceCredentialValueUnsafeEncoding &&
			StructuredImportBodyCanRemainExact(rawNodes[scriptIndex], requirement.Path, sourceValue)
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
			SetSecretBindingPreservationUnavailable(requirement, status)
		}
	}
	for index := range requirements {
		requirement := &requirements[index]
		requirement.ValueGroupID = ""
		requirement.DestinationValuePreserved = false
		requirement.RebindUnavailableReason = ""
		requirement.RebindUnavailableDescription = ""
		annotate(requirement)
		if SecretBindingInProvenance(requirement.Path) {
			// Recorded provenance (an action's or the script's own metadata,
			// such as a HAR's response cookies) is never sent and cannot be
			// rebound without changing imported bytes, so it keeps its value;
			// equal values still group together.
			requirement.DestinationValuePreserved = true
			requirement.PreservationUnavailableReason = ""
			requirement.PreservationUnavailableDescription = ""
		}
	}
	return requirements
}

func RecordedPlaceholdersCoverProjection(raw, projected string) bool {
	count := func(value string) int {
		return strings.Count(strings.ToLower(value), strings.ToLower(MaskedScriptSecret))
	}
	recorded := count(raw)
	if pairs, ok := RewrittenMessageIDs(raw, projected); ok {
		for _, pair := range pairs {
			recorded -= count(raw[pair[2]:pair[3]])
		}
	}
	return recorded > 0 && recorded >= count(projected)
}

var SecretBindingProvenancePath = regexp.MustCompile(`^/(?:(?:init|actions|teardown)/\d+/)?metadata(?:/|$)`)

func SecretBindingInProvenance(path string) bool {
	return SecretBindingProvenancePath.MatchString(path)
}

func ScriptHasUnavailableImportCredentialValuesFromProjection(script *dsl.Script, projection ImportDraftProjection) bool {
	if script == nil {
		return false
	}
	if projection.script == nil {
		return true
	}
	rawValue := projection.rawJSON
	if rawValue == nil {
		var err error
		if rawValue, err = ScriptAsJSONValue(script); err != nil {
			return true
		}
	}
	projectedValue := projection.projectedJSON
	if projectedValue == nil {
		var err error
		if projectedValue, err = ScriptAsJSONValue(projection.script); err != nil {
			return true
		}
	}
	requirements := SecretBindingRequirementsForImportScripts([]any{projectedValue})
	requirements = AnnotateSecretBindingDestinationValues(
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

func SetSecretBindingPreservationUnavailable(
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

func HasScriptJSONNode(value any) bool {
	nodes := make([]map[string]any, 0, 1)
	CollectScriptJSONNodes(value, &nodes)
	return len(nodes) > 0
}

func CollectScriptJSONNodes(value any, out *[]map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if IsScriptJSONObject(typed) {
			*out = append(*out, typed)
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			CollectScriptJSONNodes(typed[key], out)
		}
	case []any:
		for _, child := range typed {
			CollectScriptJSONNodes(child, out)
		}
	}
}
