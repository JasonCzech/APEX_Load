package engine

import (
	"errors"
	"sort"
	"strings"
)

const (
	runtimeCredentialTemplateScope = "import_templates"
	runtimeCredentialSessionScope  = "protocol_session"
	runtimeCredentialActionScope   = "resolved_action"

	// Retired protocol-managed credentials remain matchable for a bounded
	// interval after rotation. The response that performs the rotation is always
	// scrubbed with both the old and new values; this history is defense in depth
	// for delayed adapter/log evidence without making a long test append forever.
	maxRuntimeCredentialSessionHistoryValues = 512
	maxRuntimeCredentialSessionHistoryBytes  = 1 << 20
)

func (vu *VUser) ensureImportedCredentialTemplatePlan() {
	if vu == nil || vu.credentialTemplatesInspected {
		return
	}
	vu.credentialTemplatesInspected = true
	_, plan, err := inspectImportedCredentials(vu.Script)
	if err != nil {
		vu.runtimeCredentialRedactionErr = errors.New("retained import credential template inspection failed")
		return
	}
	vu.credentialTemplates = plan
}

func (vu *VUser) importedCredentialTemplateExpression(expression string) bool {
	if vu == nil {
		return false
	}
	vu.ensureImportedCredentialTemplatePlan()
	if vu.credentialTemplates == nil {
		return false
	}
	_, exists := vu.credentialTemplates.expressions[strings.TrimSpace(expression)]
	return exists
}

func (vu *VUser) beginRuntimeCredentialActionResolution() {
	if vu == nil {
		return
	}
	vu.runtimeCredentialActionTemplates = nil
	vu.runtimeCredentialResolvingAction = true
}

func (vu *VUser) endRuntimeCredentialActionResolution() {
	if vu != nil {
		vu.runtimeCredentialResolvingAction = false
	}
}

func (vu *VUser) recordRuntimeCredentialActionTemplate(expression, value string) {
	if vu == nil || !vu.runtimeCredentialResolvingAction || value == "" ||
		!vu.importedCredentialTemplateExpression(expression) {
		return
	}
	vu.runtimeCredentialActionTemplates = append(
		vu.runtimeCredentialActionTemplates,
		value,
	)
}

func (vu *VUser) takeRuntimeCredentialActionTemplates() []string {
	if vu == nil {
		return nil
	}
	values := vu.runtimeCredentialActionTemplates
	vu.runtimeCredentialActionTemplates = nil
	return values
}

// registerRuntimeCredentialValues extends only the in-memory, per-VUser
// projection. The immutable source-import matcher remains shared with sibling
// VUsers; dynamic values live in a bounded overlay.
// It never rewrites Variables, Parameters, the durable DSL, or the resolved wire
// action. A failed matcher extension is sticky so execution can fail closed
// before an adapter observes material that cannot be scrubbed completely.
func (vu *VUser) registerRuntimeCredentialValues(values ...string) error {
	if vu == nil {
		return nil
	}
	vu.ensureRuntimeCredentialRedactionState()
	if vu.runtimeCredentialRedactionErr != nil {
		return vu.runtimeCredentialRedactionErr
	}

	prospective := cloneRuntimeCredentialValueSet(vu.runtimeCredentialValues)
	baseValues := allRuntimeSecretValues(vu.runtimeCredentialBase)
	changed := false
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := baseValues[value]; exists {
			continue
		}
		if _, exists := prospective[value]; exists {
			continue
		}
		prospective[value] = struct{}{}
		changed = true
	}
	if !changed {
		return nil
	}
	if err := vu.publishRuntimeCredentialRedaction(prospective, vu.runtimeCredentialScopes); err != nil {
		return err
	}
	vu.runtimeCredentialValues = prospective
	return nil
}

func (vu *VUser) ensureRuntimeCredentialRedactionState() {
	if vu == nil || vu.runtimeCredentialStateReady {
		return
	}
	vu.runtimeCredentialStateReady = true
	vu.runtimeCredentialBase = vu.RedactionConfig
	vu.runtimeCredentialValues = make(map[string]struct{})
	vu.runtimeCredentialScopes = make(map[string][]string)
}

func (vu *VUser) replaceRuntimeCredentialScope(name string, values []string) error {
	return vu.replaceRuntimeCredentialScopes(map[string][]string{name: values})
}

func (vu *VUser) replaceRuntimeCredentialScopes(updates map[string][]string) error {
	if vu == nil {
		return nil
	}
	vu.ensureRuntimeCredentialRedactionState()
	if vu.runtimeCredentialRedactionErr != nil {
		return vu.runtimeCredentialRedactionErr
	}
	if len(updates) == 0 {
		return nil
	}
	prospective := cloneRuntimeCredentialScopes(vu.runtimeCredentialScopes)
	for name, values := range updates {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		normalized := uniqueSortedRuntimeCredentialValues(values)
		if len(normalized) == 0 {
			delete(prospective, name)
		} else {
			prospective[name] = normalized
		}
	}
	if err := vu.publishRuntimeCredentialRedaction(vu.runtimeCredentialValues, prospective); err != nil {
		return err
	}
	vu.runtimeCredentialScopes = prospective
	return nil
}

func (vu *VUser) publishRuntimeCredentialRedaction(
	persistent map[string]struct{},
	scopes map[string][]string,
) error {
	values := make([]string, 0, len(persistent))
	for value := range persistent {
		values = append(values, value)
	}
	scopeNames := make([]string, 0, len(scopes))
	for name := range scopes {
		scopeNames = append(scopeNames, name)
	}
	sort.Strings(scopeNames)
	for _, name := range scopeNames {
		values = append(values, scopes[name]...)
	}
	values = uniqueSortedRuntimeCredentialValues(values)
	if baseValues := allRuntimeSecretValues(vu.runtimeCredentialBase); len(baseValues) > 0 {
		filtered := values[:0]
		for _, value := range values {
			if _, covered := baseValues[value]; !covered {
				filtered = append(filtered, value)
			}
		}
		values = filtered
	}
	config, err := withRuntimeSecretValueOverlay(vu.runtimeCredentialBase, values)
	if err != nil {
		vu.runtimeCredentialRedactionErr = errors.New("runtime credential redaction capacity exceeded")
		return vu.runtimeCredentialRedactionErr
	}
	vu.RedactionConfig = config
	vu.runtimeRedaction.Store(config)
	return nil
}

func cloneRuntimeCredentialValueSet(input map[string]struct{}) map[string]struct{} {
	output := make(map[string]struct{}, len(input))
	for value := range input {
		output[value] = struct{}{}
	}
	return output
}

func cloneRuntimeCredentialScopes(input map[string][]string) map[string][]string {
	output := make(map[string][]string, len(input))
	for name, values := range input {
		output[name] = append([]string(nil), values...)
	}
	return output
}

func uniqueSortedRuntimeCredentialValues(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	output := make([]string, 0, len(unique))
	for value := range unique {
		output = append(output, value)
	}
	sort.Strings(output)
	return output
}

// stageRuntimeCredentialSessionSnapshot installs a complete protocol-managed
// credential snapshot before the owning result is scrubbed. The current and
// replacement snapshots intentionally overlap until the next action boundary:
// a Set-Cookie response can contain both the cookie sent on the request and its
// newly rotated value.
func (vu *VUser) stageRuntimeCredentialSessionSnapshot(values []string) error {
	if vu == nil {
		return nil
	}
	next := uniqueSortedRuntimeCredentialValues(values)
	transitional := append([]string(nil), vu.runtimeCredentialSessionHistory...)
	transitional = append(transitional, vu.runtimeCredentialSessionLive...)
	if vu.runtimeCredentialSessionStaged {
		transitional = append(transitional, vu.runtimeCredentialSessionNext...)
	}
	transitional = append(transitional, next...)
	if err := vu.replaceRuntimeCredentialScope(runtimeCredentialSessionScope, transitional); err != nil {
		return err
	}
	vu.runtimeCredentialSessionNext = next
	vu.runtimeCredentialSessionStaged = true
	return nil
}

// commitRuntimeCredentialSessionSnapshot advances the live session only at an
// action boundary. Values retired by the prior response enter a bounded FIFO;
// they cannot grow the matcher for the lifetime of a long-running test.
func (vu *VUser) commitRuntimeCredentialSessionSnapshot() error {
	if vu == nil || !vu.runtimeCredentialSessionStaged {
		return nil
	}
	nextSet := make(map[string]struct{}, len(vu.runtimeCredentialSessionNext))
	for _, value := range vu.runtimeCredentialSessionNext {
		nextSet[value] = struct{}{}
	}
	for _, value := range vu.runtimeCredentialSessionLive {
		if _, stillLive := nextSet[value]; !stillLive {
			vu.addRuntimeCredentialSessionHistory(value)
		}
	}
	vu.runtimeCredentialSessionLive = append(
		vu.runtimeCredentialSessionLive[:0],
		vu.runtimeCredentialSessionNext...,
	)
	vu.runtimeCredentialSessionNext = nil
	vu.runtimeCredentialSessionStaged = false
	vu.removeLiveRuntimeCredentialSessionHistory(nextSet)

	active := append([]string(nil), vu.runtimeCredentialSessionHistory...)
	active = append(active, vu.runtimeCredentialSessionLive...)
	return vu.replaceRuntimeCredentialScope(runtimeCredentialSessionScope, active)
}

func (vu *VUser) addRuntimeCredentialSessionHistory(value string) {
	if vu == nil || value == "" ||
		len(value) > maxRuntimeCredentialSessionHistoryBytes {
		return
	}
	for _, existing := range vu.runtimeCredentialSessionHistory {
		if existing == value {
			return
		}
	}
	vu.runtimeCredentialSessionHistory = append(vu.runtimeCredentialSessionHistory, value)
	vu.runtimeCredentialSessionHistoryBytes += len(value)
	for len(vu.runtimeCredentialSessionHistory) > maxRuntimeCredentialSessionHistoryValues ||
		vu.runtimeCredentialSessionHistoryBytes > maxRuntimeCredentialSessionHistoryBytes {
		retired := vu.runtimeCredentialSessionHistory[0]
		vu.runtimeCredentialSessionHistory[0] = ""
		vu.runtimeCredentialSessionHistory = vu.runtimeCredentialSessionHistory[1:]
		vu.runtimeCredentialSessionHistoryBytes -= len(retired)
	}
}

func (vu *VUser) removeLiveRuntimeCredentialSessionHistory(live map[string]struct{}) {
	if vu == nil || len(vu.runtimeCredentialSessionHistory) == 0 || len(live) == 0 {
		return
	}
	filtered := vu.runtimeCredentialSessionHistory[:0]
	bytes := 0
	for _, value := range vu.runtimeCredentialSessionHistory {
		if _, active := live[value]; active {
			continue
		}
		filtered = append(filtered, value)
		bytes += len(value)
	}
	vu.runtimeCredentialSessionHistory = filtered
	vu.runtimeCredentialSessionHistoryBytes = bytes
}

func (vu *VUser) refreshImportedCredentialRuntimeValues() error {
	if vu == nil {
		return nil
	}
	vu.ensureImportedCredentialTemplatePlan()
	if vu.runtimeCredentialRedactionErr != nil {
		return vu.runtimeCredentialRedactionErr
	}
	if vu.credentialTemplates == nil || len(vu.credentialTemplates.references) == 0 {
		return vu.replaceRuntimeCredentialScope(runtimeCredentialTemplateScope, nil)
	}
	values := make([]string, 0, len(vu.credentialTemplates.references))
	for _, reference := range vu.credentialTemplates.references {
		var (
			value string
			ok    bool
		)
		switch reference.namespace {
		case "var":
			value, ok = vu.Variables[reference.name]
			if !ok {
				value, ok = currentParameterValue(vu.Parameters, reference.name)
			}
		case "data":
			value, ok = vu.Variables[reference.name]
		case "param":
			value, ok = currentParameterValue(vu.Parameters, reference.name)
		case "env":
			value, ok = lookupScriptEnv(reference.name)
		}
		if ok && value != "" {
			values = append(values, value)
		}
	}
	return vu.replaceRuntimeCredentialScope(runtimeCredentialTemplateScope, values)
}

func currentParameterValue(runtime *ParameterRuntime, name string) (string, bool) {
	if runtime == nil {
		return "", false
	}
	parameterName := name
	if _, exists := runtime.params[parameterName]; !exists {
		var ok bool
		parameterName, ok = runtime.canonicalNames[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return "", false
		}
	}
	state := runtime.states[parameterName]
	if state == nil || !state.hasCached {
		// In particular, do not generate an each_occurrence value here. The
		// authored substitution remains the sole cursor-advancing operation.
		return "", false
	}
	return state.cached, true
}

func (vu *VUser) importedCredentialVariableName(name string) bool {
	if vu == nil || name == "" {
		return false
	}
	vu.ensureImportedCredentialTemplatePlan()
	if vu.credentialTemplates == nil {
		return false
	}
	_, exists := vu.credentialTemplates.variables[name]
	return exists
}

func (vu *VUser) runtimeCredentialRedactionError() error {
	if vu == nil {
		return nil
	}
	vu.ensureImportedCredentialTemplatePlan()
	return vu.runtimeCredentialRedactionErr
}
