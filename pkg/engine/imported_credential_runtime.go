package engine

import (
	"errors"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// runtimeCredentialScriptScopePrefix marks a scope whose values are
// SCRIPT-SOURCED. Everything a producer files under this prefix is best effort:
// governed by script.Config.Log.RedactRunEvidence, dropped rather than failing a
// run when the matcher overflows, and excluded from the platform-managed
// presence test that suppresses screenshots and HAR capture.
//
// A scope without the prefix is platform-managed and behaves exactly as it did
// before the setting existed.
const runtimeCredentialScriptScopePrefix = "script:"

const (
	runtimeCredentialTemplateScope        = runtimeCredentialScriptScopePrefix + "import_templates"
	runtimeCredentialScriptActionScope    = runtimeCredentialScriptScopePrefix + "resolved_action"
	runtimeCredentialSourceVariablePrefix = runtimeCredentialScriptScopePrefix + "source_variable:"

	runtimeCredentialActionScope   = "resolved_action"
	runtimeCredentialOAuthURLScope = "oauth_token_url"
)

func isScriptSourcedRuntimeCredentialScope(name string) bool {
	return strings.HasPrefix(name, runtimeCredentialScriptScopePrefix)
}

// importedCredentialInspection is the value-free result of the retained-credential
// locator walk AND the marker that the walk already ran. It is deliberately one
// value rather than two.
//
// It used to be two VUser fields: a bool "already inspected" marker and a separate
// template-plan pointer. Every standard entry point set the marker — the walk had
// run once for the whole run — while handing over no plan, so the plan was
// permanently nil in production and both dynamic script-sourced producers
// (refreshImportedCredentialRuntimeValues and registerSourceVariableRunEvidenceValue)
// silently did nothing on every real run. Tests, which left the marker unset, got
// a lazily built plan and stayed green. Carrying the marker and the result in one
// value makes that divergence unrepresentable: telling a VUser the walk is done
// means handing it what the walk produced.
//
// The plan holds authored template EXPRESSIONS, never values, and it carries no
// origin guarantee — collectTemplateReferences records "secret.NAME" alongside
// every other namespace. Consumers re-establish origin themselves; see
// scriptSourcedTemplateExpressions.
type importedCredentialInspection struct {
	// plan is nil when the script referenced no runtime template from a
	// credential-governed executable field, and when the walk failed.
	plan *importedCredentialTemplatePlan
	// err is only ever set on the lazy per-VUser path. A standard entry point
	// fails its whole run on a corrupted import instead of building a VUser.
	err error
}

func (inspection *importedCredentialInspection) templatePlan() *importedCredentialTemplatePlan {
	if inspection == nil {
		return nil
	}
	return inspection.plan
}

// inspectImportedCredentialsForRuntime runs the retained-credential locator walk
// for ONE VUser that was not handed a run-wide result.
//
// The run entry points do not call it. runtimeSecretValuesForScript needs the same
// walk's VALUES as well, so it builds the inspection from its own single walk
// rather than repeating the walk here.
//
// The walk is unconditional: a locator that no longer matches its executable
// field signals a corrupted import, which must fail closed independently of
// redaction.
func inspectImportedCredentialsForRuntime(script *dsl.Script) *importedCredentialInspection {
	_, plan, err := inspectImportedCredentials(script)
	if err != nil {
		return &importedCredentialInspection{
			err: errors.New("retained import credential template inspection failed"),
		}
	}
	return &importedCredentialInspection{plan: plan}
}

// ensureImportedCredentialInspection performs the walk for a VUser that was not
// handed a run-wide inspection — a direct test or library VUser. Standard
// execution paths perform it once for the whole run and pass the result in, so
// this is a no-op for them.
func (vu *VUser) ensureImportedCredentialInspection() {
	if vu == nil || vu.importedCredentialInspection != nil {
		return
	}
	vu.importedCredentialInspection = inspectImportedCredentialsForRuntime(vu.Script)
	if err := vu.importedCredentialInspection.err; err != nil {
		vu.runtimeCredentialRedactionErr = err
	}
}

func (vu *VUser) ensureRuntimeCredentialRedactionState() {
	if vu == nil || vu.runtimeCredentialStateReady {
		return
	}
	vu.runtimeCredentialStateReady = true
	vu.runtimeCredentialBase = vu.RedactionConfig
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
	redactScriptSourced := scriptRedactsRunEvidence(vu.Script)
	prospective := cloneRuntimeCredentialScopes(vu.runtimeCredentialScopes)
	for name, values := range updates {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// With the setting off a script-sourced scope is never recorded at all.
		// Recording it and dropping its values later would leave the scope map
		// churning on every action and recompile the platform-managed overlay for
		// no observable change.
		if !redactScriptSourced && isScriptSourcedRuntimeCredentialScope(name) {
			delete(prospective, name)
			continue
		}
		normalized := uniqueSortedRuntimeCredentialValues(values)
		if len(normalized) == 0 {
			delete(prospective, name)
		} else {
			prospective[name] = normalized
		}
	}
	if runtimeCredentialScopesEqual(vu.runtimeCredentialScopes, prospective) {
		return nil
	}
	if err := vu.publishRuntimeCredentialRedaction(prospective); err != nil {
		return err
	}
	vu.runtimeCredentialScopes = prospective
	return nil
}

func runtimeCredentialScopesEqual(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, leftValues := range left {
		rightValues, ok := right[name]
		if !ok || len(leftValues) != len(rightValues) {
			return false
		}
		for index := range leftValues {
			if leftValues[index] != rightValues[index] {
				return false
			}
		}
	}
	return true
}

// publishRuntimeCredentialRedaction rebuilds the per-VUser matcher overlay and
// is the single funnel every dynamic producer passes through.
//
// Values arrive per named scope and each scope's producer establishes the ORIGIN
// of what it registers by the scope name it chooses. Platform-managed producers:
//
//   - "extraction:<name>" — an extraction whose NAME is sensitive
//     (scrub.SensitiveName, origin-independent), vuser_resolve.go.
//   - runtimeCredentialActionScope — the composite spellings of a per-action
//     credential surface that resolves from the project secret store,
//     resolved_imported_credential_redaction.go.
//   - runtimeCredentialOAuthURLScope — the same composites for an
//     http_oauth2_token action's token_url, vuser_source_actions.go.
//   - "oauth:<save_as>" — the access token an http_oauth2_token action fetched,
//     vuser_source_actions.go. That token is harvested from a token endpoint at
//     run time, but the operator asked for it by writing an explicit action with
//     an explicit save_as, and it is not readable in the script.
//
// Script-sourced producers, all under runtimeCredentialScriptScopePrefix:
//
//   - runtimeCredentialTemplateScope — script-sourced template namespaces used
//     from a credential-governed field; see refreshImportedCredentialRuntimeValues.
//   - runtimeCredentialScriptActionScope — the resolved credential surface of the
//     action about to execute, resolved_imported_credential_redaction.go.
//   - runtimeCredentialSourceVariablePrefix — a source-action assignment landing
//     in a variable an import reads from a credential-governed field,
//     vuser_source_actions.go.
//
// This funnel applies the two rules those producers must not each re-implement:
// the operator's script.Config.Log.RedactRunEvidence setting drops the
// script-sourced scopes, and a matcher overflow drops them too rather than
// failing the run. Platform-managed scopes keep their fail-closed behaviour
// exactly.
//
// The implicit protocol-layer cookie snapshot is the one producer that was
// removed: cookie-jar contents no longer arrive here at all. That is why the
// same matcher now governs payload evidence and stable metric dimensions alike —
// the split that used to shield action/group/dependency labels from cookie
// snapshots has no remaining input.
func (vu *VUser) publishRuntimeCredentialRedaction(scopes map[string][]string) error {
	platformManaged, scriptSourced := runtimeCredentialScopeValues(scopes)
	if !scriptRedactsRunEvidence(vu.Script) {
		scriptSourced = nil
	}
	platformManaged = filterRuntimeCredentialBaseValues(platformManaged, vu.runtimeCredentialBase, false)
	scriptSourced = filterRuntimeCredentialBaseValues(scriptSourced, vu.runtimeCredentialBase, true)

	config, err := withRuntimeSecretValueOverlay(vu.runtimeCredentialBase, platformManaged, scriptSourced)
	if err != nil && len(scriptSourced) > 0 {
		// Script-sourced values are best effort and must never convert a run that
		// passes today into a failure. Retry with the platform-managed half alone.
		config, err = withRuntimeSecretValueOverlay(vu.runtimeCredentialBase, platformManaged, nil)
		if err == nil {
			vu.noteScriptSourcedRedactionDropped()
		}
	}
	if err != nil {
		vu.runtimeCredentialRedactionErr = errors.New("runtime credential redaction capacity exceeded")
		return vu.runtimeCredentialRedactionErr
	}

	vu.RedactionConfig = config
	vu.runtimeRedaction.Store(config)
	return nil
}

// noteScriptSourcedRedactionDropped surfaces the drop once per VUser. The values
// themselves are never logged.
func (vu *VUser) noteScriptSourcedRedactionDropped() {
	if vu == nil || vu.scriptSourcedRedactionDropped {
		return
	}
	vu.scriptSourcedRedactionDropped = true
	warnScriptSourcedRedactionDropped("vuser", vu.ID, "group", vu.Group)
}

// runtimeCredentialScopeValues splits the live scopes by origin. Both halves are
// deduplicated and sorted so an unchanged scope set rebuilds nothing, and a value
// claimed by both origins stays platform-managed.
func runtimeCredentialScopeValues(scopes map[string][]string) ([]string, []string) {
	scopeNames := make([]string, 0, len(scopes))
	for name := range scopes {
		scopeNames = append(scopeNames, name)
	}
	sort.Strings(scopeNames)
	platformManaged := make([]string, 0, len(scopeNames))
	scriptSourced := make([]string, 0, len(scopeNames))
	for _, name := range scopeNames {
		if isScriptSourcedRuntimeCredentialScope(name) {
			scriptSourced = append(scriptSourced, scopes[name]...)
			continue
		}
		platformManaged = append(platformManaged, scopes[name]...)
	}
	platformManaged = uniqueSortedRuntimeCredentialValues(platformManaged)
	scriptSourced = uniqueSortedRuntimeCredentialValues(scriptSourced)
	if len(platformManaged) > 0 && len(scriptSourced) > 0 {
		covered := make(map[string]struct{}, len(platformManaged))
		for _, value := range platformManaged {
			covered[value] = struct{}{}
		}
		filtered := scriptSourced[:0]
		for _, value := range scriptSourced {
			if _, exists := covered[value]; !exists {
				filtered = append(filtered, value)
			}
		}
		scriptSourced = filtered
	}
	return platformManaged, scriptSourced
}

// filterRuntimeCredentialBaseValues drops the values the immutable shared base
// matcher already covers, so the per-VUser overlay only ever carries what the base
// does not.
//
// Coverage is ORIGIN-AWARE. The base covers a script-sourced value at full
// strength whatever origin it holds it under, but it covers a PLATFORM-MANAGED
// value only if it holds that value platform-managed too: a literal the base
// carries as script-sourced still owes its promotion to the overlay, or a
// platform-managed producer's registration would be silently discarded and
// HasRuntimeSecretMaterial would stay false for a run with live secret material.
func filterRuntimeCredentialBaseValues(
	values []string,
	base *RedactionConfig,
	scriptSourced bool,
) []string {
	covered := allRuntimeSecretValues(base)
	if len(covered) == 0 {
		return values
	}
	if !scriptSourced {
		for value := range allRuntimeScriptSourcedValues(base) {
			delete(covered, value)
		}
	}
	filtered := values[:0]
	for _, value := range values {
		if _, exists := covered[value]; !exists {
			filtered = append(filtered, value)
		}
	}
	return filtered
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

// refreshImportedCredentialRuntimeValues rebuilds the import-template scope.
//
// It resolves every {{var.*}} / {{param.*}} / {{data.*}} / {{env.*}} reference
// the import placed in a credential-governed field and registers the resolved
// value. All four of those namespaces are script-sourced — none is resolved
// through the secret resolver — so the scope is filed under
// runtimeCredentialScriptScopePrefix and the operator's setting governs it.
//
// ORIGIN IS RE-ESTABLISHED HERE, not inherited from the plan.
// collectTemplateReferences does not filter by namespace, so plan.expressions
// records "secret.NAME" alongside everything else and carries no origin
// guarantee whatsoever. scriptSourcedTemplateExpressions keeps only the
// namespaces that are script-sourced by construction; a {{secret.*}} value would
// otherwise be over-registered into a script-sourced scope, where the operator's
// setting could switch its protection off. Platform-managed values are already
// registered once, up front, by snapshotReferencedSecrets.
//
// The scope is replaced wholesale on every refresh so a VUser reused across
// iterations cannot carry a stale registration from an earlier data row.
func (vu *VUser) refreshImportedCredentialRuntimeValues() error {
	if vu == nil {
		return nil
	}
	vu.ensureImportedCredentialInspection()
	if vu.runtimeCredentialRedactionErr != nil {
		return vu.runtimeCredentialRedactionErr
	}
	return vu.replaceRuntimeCredentialScope(
		runtimeCredentialTemplateScope,
		vu.resolvedScriptSourcedTemplateValues(),
	)
}

// resolvedScriptSourcedTemplateValues resolves the plan's script-sourced
// expressions against the VUser's current variables, parameters, data row, and
// environment. An expression that does not resolve contributes nothing.
func (vu *VUser) resolvedScriptSourcedTemplateValues() []string {
	if vu == nil || !scriptRedactsRunEvidence(vu.Script) {
		return nil
	}
	expressions := scriptSourcedTemplateExpressions(vu.importedCredentialInspection.templatePlan())
	if len(expressions) == 0 {
		return nil
	}
	values := make([]string, 0, len(expressions))
	for _, expression := range expressions {
		if vu.templateResolvesToResponseExtraction(expression) {
			continue
		}
		if value, ok := vu.resolveTemplateValue(expression); ok && value != "" {
			values = append(values, value)
		}
	}
	return values
}

// templateResolvesToResponseExtraction reports that a {{var.NAME}} reference
// currently resolves to a value a protocol RESPONSE produced rather than to
// anything the script carries.
//
// The plan records only that an import referenced the variable from a
// credential-governed field, which is import provenance — and import provenance
// is deliberately not a reason to hide what the system under test returned from
// the operator who owns the import. The plan's `variables` index was deleted for
// exactly that reason (see importedCredentialTemplatePlan and mergeExtractions),
// and resolving such a reference here would reinstate it through a different
// door: the variable map the reference reads is the same map extractions land in.
//
// A response value is governed by its own NAME instead, origin-independently, by
// the platform-managed "extraction:<name>" scope mergeExtractions installs. Every
// script-sourced filler of the same variable — a data row, a parameter, a source
// action assignment, an operator-supplied replay variable — is untouched by this
// and stays registered.
func (vu *VUser) templateResolvesToResponseExtraction(expression string) bool {
	name, isVariable := strings.CutPrefix(expression, "var.")
	if !isVariable {
		return false
	}
	_, extracted := vu.extractedVariables[strings.TrimSpace(name)]
	return extracted
}

// scriptSourcedTemplateExpressions filters a value-free template plan down to the
// namespaces that are script-sourced by construction.
//
// This is the origin test the plan itself does not provide. "secret." is resolved
// through the project secret store and is platform-managed. "file." reads a
// governed data file and "fn."/"fake." are generated, so neither is a script
// literal either; both are left to their own registration paths rather than
// swept into a script-sourced scope.
func scriptSourcedTemplateExpressions(plan *importedCredentialTemplatePlan) []string {
	if plan == nil {
		return nil
	}
	expressions := make([]string, 0, len(plan.expressions))
	for expression := range plan.expressions {
		switch {
		case strings.HasPrefix(expression, "var."),
			strings.HasPrefix(expression, "param."),
			strings.HasPrefix(expression, "data."),
			strings.HasPrefix(expression, "env."):
			expressions = append(expressions, expression)
		}
	}
	sort.Strings(expressions)
	return expressions
}

func (vu *VUser) runtimeCredentialRedactionError() error {
	if vu == nil {
		return nil
	}
	vu.ensureImportedCredentialInspection()
	return vu.runtimeCredentialRedactionErr
}
