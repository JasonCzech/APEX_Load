package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	validationContextVersion    = 1
	validationContextKeySetting = "security.validation_context_hmac_key.v1"
	validationContextDomain     = "apexload:validation-context:v1"
	// validationContextSecretLimit bounds how many referenced secret names
	// (sorted) enter the secret-reference component.
	validationContextSecretLimit = 256
)

type validationContext = importreview.ValidationContext

var errValidationContextUnavailable = errors.New("validation context is unavailable without storage")

// buildValidationContext computes the context a script validation result
// depends on, for the script as the caller sees it in projectID, with its
// secrets resolved through binding by resolver (the resolver the validation
// used). Every component is a keyed digest, and no secret value enters any:
// validation depends on whether a secret is configured, never on its value.
func (s *Server) buildValidationContext(ctx context.Context, projectID string, script *dsl.Script, binding scriptSecretBinding, resolver engine.SecretResolver) (*validationContext, error) {
	if s == nil || s.store == nil {
		// Without storage there is no per-deployment key to digest under.
		return nil, errValidationContextUnavailable
	}
	key, err := s.persistentDigestKey(validationContextKeySetting, "validation context")
	if err != nil {
		return nil, err
	}
	digest := func(domain string, value any) (string, error) {
		payload, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(validationContextDomain + ":" + domain))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write(payload)
		return hex.EncodeToString(mac.Sum(nil)), nil
	}
	result := &validationContext{Version: validationContextVersion, SecretReferenceLimit: validationContextSecretLimit}
	projectID = normalizeProjectID(projectID)

	// Capabilities: the controller's authoring catalog and the runtime this
	// script needs on it.
	capabilities := struct {
		Catalog        AuthoringCapabilityResponse `json:"catalog"`
		RuntimeWarning string                      `json:"runtime_warning,omitempty"`
	}{Catalog: authoringCapabilityResponse(projectID)}
	if warning := browserRuntimeWarning(script); warning != "" {
		capabilities.RuntimeWarning = warning
		result.Findings = append(result.Findings, importreview.ValidationContextFinding{Cause: "capability_unavailable", Subject: "browser_runtime", Detail: warning})
	}

	// Policy: the project's domain feature policy, its settings and the
	// system script-import policy.
	policy := struct {
		DomainRevision   string   `json:"domain_revision"`
		DisabledFeatures []string `json:"disabled_features"`
		// Only the project settings validation depends on: other settings
		// (telemetry, analysis, quotas, recording profiles) never make a
		// result stale.
		Authoring     any `json:"authoring"`
		Features      any `json:"features"`
		ScriptImports any `json:"script_imports"`
	}{}
	if featurePolicy, err := s.domainFeaturePolicyForProject(projectID); err != nil {
		return nil, fmt.Errorf("read domain feature policy: %w", err)
	} else if featurePolicy != nil {
		policy.DomainRevision = featurePolicy.Revision
		policy.DisabledFeatures = append([]string(nil), featurePolicy.DisabledFeatureIDs...)
		sort.Strings(policy.DisabledFeatures)
	}
	// A failed read is an error, never a different policy: a probe must not
	// report a change it did not observe.
	project, err := s.store.GetProject(projectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read project: %w", err)
	}
	if err == nil && project != nil {
		policy.Authoring, policy.Features = project.Settings.Authoring, project.Settings.Features
	}
	if s.runner != nil {
		policy.ScriptImports = s.runner.GetSystemSettings().ScriptImports
	}

	// Approvals: whether the script needs a Java Vuser authoring approval
	// and, for a stored script, whether its approval is still valid.
	approval := "not_required"
	if scriptRequiresJavaVuserAuthoringApproval(script) {
		approval = "required"
		if s.runner != nil && strings.TrimSpace(script.ID) != "" {
			conflict, err := s.runner.javaVuserApprovalState(script)
			if err != nil {
				return nil, fmt.Errorf("read Java Vuser approval: %w", err)
			}
			if conflict == "" {
				approval = "approved"
			} else {
				approval = "stale"
				result.Findings = append(result.Findings, importreview.ValidationContextFinding{Cause: "approval_stale", Detail: conflict})
			}
		} else {
			result.Findings = append(result.Findings, importreview.ValidationContextFinding{Cause: "approval_required", Detail: "Java Vuser scripts are added to the library through an authoring-session review and approval"})
		}
	}

	// Secret references: where the script's secrets resolve (its project, an
	// application ahead of it, or a refused assignment that resolves none)
	// and each referenced name (bounded, sorted) with its availability. A
	// rebinding or a secret becoming available or unavailable changes the
	// context; replacing a value does not, and no value is digested, so the
	// context cannot confirm a guess of one.
	if binding.Err != nil {
		return nil, fmt.Errorf("resolve secret references: %w", binding.Err)
	}
	names, truncated, missing, err := validationContextSecrets(ctx, script, resolver)
	if err != nil {
		return nil, err
	}
	// The resolver reports a provider failure as a miss; only a store that
	// answered "not found" makes a secret unavailable.
	if err := s.secretProviderFailure(ctx, binding, missing); err != nil {
		return nil, fmt.Errorf("resolve secret references: %w", err)
	}
	secrets := struct {
		Project     string      `json:"project"`
		Application string      `json:"application,omitempty"`
		Refused     bool        `json:"refused,omitempty"`
		Names       [][2]string `json:"names"`
	}{strings.TrimSpace(binding.ProjectID), binding.ApplicationID, binding.Refused, names}
	result.SecretReferencesTruncated = truncated
	unavailable := "the referenced secret is not configured for this project"
	if binding.Refused {
		unavailable = "the script's application assignment cannot supply secrets in this project"
	}
	for _, name := range missing {
		result.Findings = append(result.Findings, importreview.ValidationContextFinding{Cause: "secret_unavailable", Subject: name, Detail: unavailable})
	}

	components := []struct {
		target *string
		domain string
		value  any
	}{
		{&result.Components.Capabilities, "capabilities", capabilities},
		{&result.Components.Policy, "policy", policy},
		{&result.Components.Approvals, "approvals", approval},
		{&result.Components.SecretReferences, "secret-references", secrets},
	}
	for _, component := range components {
		value, err := digest(component.domain, component.value)
		if err != nil {
			return nil, err
		}
		*component.target = value[:32]
	}
	revision, err := digest("revision", result.Components)
	if err != nil {
		return nil, err
	}
	result.Revision = "vctx1:" + revision
	return result, nil
}

// validationContextSecrets lists the script's referenced secrets (sorted,
// at most validationContextSecretLimit) with their availability, plus the
// unavailable names.
func validationContextSecrets(ctx context.Context, script *dsl.Script, resolver engine.SecretResolver) ([][2]string, bool, []string, error) {
	if script == nil {
		return nil, false, nil, nil
	}
	required, _ := importreview.ReplaySecretRefs(script)
	names := append([]string(nil), required...)
	sort.Strings(names)
	truncated := len(names) > validationContextSecretLimit
	if truncated {
		names = names[:validationContextSecretLimit]
	}
	unresolved := names
	if resolver != nil && len(names) > 0 {
		resolveCtx, cancel := engine.WithSecretResolutionBudget(ctx)
		_, missing, err := engine.ResolveSecretNames(resolveCtx, names, resolver)
		cancel()
		if err != nil {
			// An unreachable provider is not an unconfigured secret.
			return nil, false, nil, fmt.Errorf("resolve secret references: %w", err)
		}
		unresolved = missing
	}
	unavailable := map[string]bool{}
	for _, name := range unresolved {
		unavailable[name] = true
	}
	entries := make([][2]string, 0, len(names))
	missing := make([]string, 0)
	for _, name := range names {
		if unavailable[name] {
			entries = append(entries, [2]string{name, "unavailable"})
			missing = append(missing, name)
			continue
		}
		entries = append(entries, [2]string{name, "available"})
	}
	return entries, truncated, missing, nil
}

// browserRuntimeWarning is the controller runtime warning for a browser
// script's engine, or "".
func browserRuntimeWarning(script *dsl.Script) string {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		return ""
	}
	engineName := strings.TrimSpace(script.Config.Browser.Engine)
	if profile, ok := browserprotocol.LookupDevice(strings.TrimSpace(script.Config.Browser.DeviceProfile)); ok && profile.BrowserFamily != "" && engineName == "" {
		engineName = profile.BrowserFamily
	}
	return browserprotocol.RuntimeInstallWarning(engineName)
}

// attachValidationContext adds the validation context to a response. A
// context that cannot be computed is omitted, and a client then treats the
// result as bound to its document only, as with an older controller.
func (s *Server) attachValidationContext(r *http.Request, response *scriptValidationResponse, projectID string, script *dsl.Script, binding scriptSecretBinding, resolver engine.SecretResolver) {
	validation, err := s.buildValidationContext(r.Context(), projectID, script, binding, resolver)
	if err != nil {
		return
	}
	response.ValidationContext = validation
}

// errSecretResolutionInconsistent: a managed store holds a usable value for
// a name the script's resolver reported missing, so something in the chain
// (an application or assignment read) failed rather than lacked it.
var errSecretResolutionInconsistent = errors.New("a managed secret store holds a value the resolver could not read")

// secretProviderFailure re-reads, for the names the resolver reported
// missing, the managed stores the binding resolves them from: the
// application's secrets and the project's, exactly as the resolver read them
// (a draft without a project resolved no project secrets, and a refused
// binding resolved nothing, so there is nothing to re-read). It returns an
// error when any read failed (other than "not found") or found a usable
// value: either way the secret is not known to be unconfigured. A Key Vault
// fallback reports no errors to recheck, so its outage still reads as
// unavailable.
func (s *Server) secretProviderFailure(ctx context.Context, binding scriptSecretBinding, names []string) error {
	tr := s.runner
	if tr == nil || len(names) == 0 || binding.Refused {
		return nil
	}
	type lookup func(context.Context, string) (string, error)
	lookups := make([]lookup, 0, 2)
	if binding.ApplicationID != "" && tr.applicationSecretStore != nil {
		scope := storage.ApplicationSecretScopeID(binding.ApplicationID)
		lookups = append(lookups, func(ctx context.Context, name string) (string, error) {
			value, _, err := tr.applicationSecretStore.Get(ctx, scope, name)
			return value, err
		})
	}
	if projectID := binding.ProjectID; tr.projectSecretStore != nil && strings.TrimSpace(projectID) != "" {
		lookups = append(lookups, func(ctx context.Context, name string) (string, error) {
			value, _, err := tr.projectSecretStore.Get(ctx, projectID, name)
			return value, err
		})
	}
	budget, cancel := engine.WithSecretResolutionBudget(ctx)
	defer cancel()
	for _, name := range names {
		for _, get := range lookups {
			lookupCtx, lookupCancel := context.WithTimeout(budget, 5*time.Second)
			value, err := get(lookupCtx, name)
			lookupCancel()
			switch {
			case err == nil && engine.IsUsableResolvedSecretValue(value):
				return errSecretResolutionInconsistent
			case err != nil && !errors.Is(err, secretstore.ErrNotFound):
				return err
			}
		}
	}
	return nil
}

// ScenarioValidationContext identifies the composite controller context a
// scenario validation result depends on: constituent scripts' content/revision
// fingerprints, project capabilities, selected or default telemetry/browser/
// journey/execution policy digests, Java/import approval digests, and
// constituent scripts' secret-reference availability.
type ScenarioValidationContext struct {
	Version                   int                                     `json:"version"`
	Revision                  string                                  `json:"revision"`
	Components                ScenarioValidationContextComponents     `json:"components"`
	Findings                  []importreview.ValidationContextFinding `json:"contextual_findings,omitempty"`
	SecretReferencesTruncated bool                                    `json:"secret_references_truncated,omitempty"`
	SecretReferenceLimit      int                                     `json:"secret_reference_limit"`
}

type ScenarioValidationContextComponents struct {
	Scripts          string `json:"scripts"`
	Capabilities     string `json:"capabilities"`
	Policy           string `json:"policy"`
	Approvals        string `json:"approvals"`
	SecretReferences string `json:"secret_references"`
}

func (s *Server) buildScenarioValidationContext(ctx context.Context, projectID string, req CreateScenarioRequest) (*ScenarioValidationContext, error) {
	if s == nil || s.store == nil {
		return nil, errValidationContextUnavailable
	}
	key, err := s.persistentDigestKey(validationContextKeySetting, "validation context")
	if err != nil {
		return nil, err
	}
	digest := func(domain string, value any) (string, error) {
		payload, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(validationContextDomain + ":scenario:" + domain))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write(payload)
		return hex.EncodeToString(mac.Sum(nil)), nil
	}
	result := &ScenarioValidationContext{
		Version:              validationContextVersion,
		SecretReferenceLimit: validationContextSecretLimit,
	}
	projectID = normalizeProjectID(projectID)

	type scriptComponentEntry struct {
		GroupIndex      int    `json:"group_index"`
		RequestedID     string `json:"requested_id"`
		ScriptID        string `json:"script_id,omitempty"`
		Status          string `json:"status"`
		ContentRevision string `json:"content_revision,omitempty"`
		ContentDigest   string `json:"content_digest,omitempty"`
	}
	type capabilityScriptWarning struct {
		ScriptID string `json:"script_id"`
		Warning  string `json:"warning"`
	}
	type approvalComponentEntry struct {
		ScriptID     string             `json:"script_id"`
		JavaApproval string             `json:"java_approval"`
		Metadata     *dsl.ScriptMetadata `json:"metadata,omitempty"`
	}
	type secretScopeEntry struct {
		ScriptID    string      `json:"script_id"`
		Project     string      `json:"project"`
		Application string      `json:"application,omitempty"`
		Refused     bool        `json:"refused,omitempty"`
		Names       [][2]string `json:"names"`
	}

	scriptEntries := make([]scriptComponentEntry, 0, len(req.Groups))
	capWarnings := make([]capabilityScriptWarning, 0)
	approvalEntries := make([]approvalComponentEntry, 0, len(req.Groups))
	secretEntries := make([]secretScopeEntry, 0, len(req.Groups)+1)
	anyTruncated := false

	seenScripts := map[string]*StoredScript{}
	orderedScripts := make([]*StoredScript, 0, len(req.Groups))

	for idx, group := range req.Groups {
		rawID := strings.TrimSpace(group.ScriptID)
		if rawID == "" {
			scriptEntries = append(scriptEntries, scriptComponentEntry{
				GroupIndex:  idx,
				RequestedID: "",
				Status:      "missing",
			})
			continue
		}
		var stored *StoredScript
		if s.runner != nil {
			durable, found, readErr := s.runner.GetScriptAuthoringDurable(rawID)
			if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) && !errors.Is(readErr, storage.ErrScriptAuthoringNotFound) {
				return nil, fmt.Errorf("read constituent script %q: %w", rawID, readErr)
			}
			if found && durable != nil {
				stored = durable
			} else if cached, ok := s.runner.GetScript(rawID); ok && cached != nil {
				stored = cached
			}
		}
		if stored == nil || stored.Script == nil || normalizeProjectID(stored.ProjectID) != projectID {
			scriptEntries = append(scriptEntries, scriptComponentEntry{
				GroupIndex:  idx,
				RequestedID: rawID,
				Status:      "missing",
			})
			result.Findings = append(result.Findings, importreview.ValidationContextFinding{
				Cause:   "script_unavailable",
				Subject: rawID,
				Detail:  "constituent script is no longer available in this project",
			})
			continue
		}
		status := "active"
		if stored.ArchivedAt != nil || stored.Script.ArchivedAt != nil {
			status = "archived"
			result.Findings = append(result.Findings, importreview.ValidationContextFinding{
				Cause:   "script_unavailable",
				Subject: stored.ID,
				Detail:  "constituent script is archived",
			})
		}
		contentDigest, err := scenarioConstituentScriptDigest(stored.Script)
		if err != nil {
			return nil, err
		}
		scriptEntries = append(scriptEntries, scriptComponentEntry{
			GroupIndex:      idx,
			RequestedID:     rawID,
			ScriptID:        stored.ID,
			Status:          status,
			ContentRevision: stored.ContentRevision,
			ContentDigest:   contentDigest,
		})
		if _, seen := seenScripts[stored.ID]; !seen {
			seenScripts[stored.ID] = stored
			orderedScripts = append(orderedScripts, stored)
		}
	}

	for _, stored := range orderedScripts {
		script := stored.Script
		if warning := browserRuntimeWarning(script); warning != "" {
			capWarnings = append(capWarnings, capabilityScriptWarning{ScriptID: stored.ID, Warning: warning})
			result.Findings = append(result.Findings, importreview.ValidationContextFinding{
				Cause:   "capability_unavailable",
				Subject: stored.ID,
				Detail:  warning,
			})
		}

		javaApproval := "not_required"
		if scriptRequiresJavaVuserAuthoringApproval(script) {
			javaApproval = "required"
			if s.runner != nil && strings.TrimSpace(script.ID) != "" {
				conflict, err := s.runner.javaVuserApprovalState(script)
				if err != nil {
					return nil, fmt.Errorf("read Java Vuser approval for %q: %w", script.ID, err)
				}
				if conflict == "" {
					javaApproval = "approved"
				} else {
					javaApproval = "stale"
					result.Findings = append(result.Findings, importreview.ValidationContextFinding{
						Cause:   "approval_stale",
						Subject: script.ID,
						Detail:  conflict,
					})
				}
			} else {
				result.Findings = append(result.Findings, importreview.ValidationContextFinding{
					Cause:   "approval_required",
					Subject: script.ID,
					Detail:  "Java Vuser scripts require authoring-session review and approval",
				})
			}
		}
		approvalEntries = append(approvalEntries, approvalComponentEntry{
			ScriptID:     stored.ID,
			JavaApproval: javaApproval,
			Metadata:     script.Metadata,
		})

		var binding scriptSecretBinding
		var resolver engine.SecretResolver
		if s.runner != nil {
			binding = s.runner.secretBindingForScript(script)
			resolver = s.runner.secretResolverForBinding(binding)
		} else {
			binding = scriptSecretBinding{ProjectID: projectID}
		}
		if binding.Err != nil {
			return nil, fmt.Errorf("resolve secret references for %q: %w", stored.ID, binding.Err)
		}
		names, truncated, missing, err := validationContextSecrets(ctx, script, resolver)
		if err != nil {
			return nil, err
		}
		if err := s.secretProviderFailure(ctx, binding, missing); err != nil {
			return nil, fmt.Errorf("resolve secret references for %q: %w", stored.ID, err)
		}
		if truncated {
			anyTruncated = true
		}
		unavailableDetail := "the referenced secret is not configured for this project"
		if binding.Refused {
			unavailableDetail = "the script's application assignment cannot supply secrets in this project"
		}
		for _, name := range missing {
			result.Findings = append(result.Findings, importreview.ValidationContextFinding{
				Cause:   "secret_unavailable",
				Subject: name,
				Detail:  unavailableDetail,
			})
		}
		secretEntries = append(secretEntries, secretScopeEntry{
			ScriptID:    stored.ID,
			Project:     strings.TrimSpace(binding.ProjectID),
			Application: binding.ApplicationID,
			Refused:     binding.Refused,
			Names:       names,
		})
	}

	result.SecretReferencesTruncated = anyTruncated

	capabilities := struct {
		Catalog        AuthoringCapabilityResponse `json:"catalog"`
		ScriptWarnings []capabilityScriptWarning   `json:"script_warnings,omitempty"`
	}{
		Catalog:        authoringCapabilityResponse(projectID),
		ScriptWarnings: capWarnings,
	}

	acceptancePolicy, err := s.scenarioTelemetryPolicyContextState(projectID, storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, req.TelemetryAcceptancePolicyID)
	if err != nil {
		return nil, err
	}
	budgetPolicy, err := s.scenarioTelemetryPolicyContextState(projectID, storage.AnalysisDefinitionKindTelemetryPolicy, req.TelemetryBudgetPolicyID)
	if err != nil {
		return nil, err
	}
	policy := struct {
		DomainRevision            string   `json:"domain_revision"`
		DisabledFeatures          []string `json:"disabled_features"`
		Authoring                 any      `json:"authoring"`
		Features                  any      `json:"features"`
		ScriptImports             any      `json:"script_imports"`
		TelemetryAcceptancePolicy any      `json:"telemetry_acceptance_policy"`
		TelemetryBudgetPolicy     any      `json:"telemetry_budget_policy"`
	}{
		TelemetryAcceptancePolicy: acceptancePolicy,
		TelemetryBudgetPolicy:     budgetPolicy,
	}
	if featurePolicy, err := s.domainFeaturePolicyForProject(projectID); err != nil {
		return nil, fmt.Errorf("read domain feature policy: %w", err)
	} else if featurePolicy != nil {
		policy.DomainRevision = featurePolicy.Revision
		policy.DisabledFeatures = append([]string(nil), featurePolicy.DisabledFeatureIDs...)
		sort.Strings(policy.DisabledFeatures)
	}
	project, err := s.store.GetProject(projectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read project: %w", err)
	}
	if err == nil && project != nil {
		policy.Authoring = project.Settings.Authoring
		policy.Features = project.Settings.Features
	}
	if s.runner != nil {
		policy.ScriptImports = s.runner.GetSystemSettings().ScriptImports
	}

	components := []struct {
		target *string
		domain string
		value  any
	}{
		{&result.Components.Scripts, "scripts", scriptEntries},
		{&result.Components.Capabilities, "capabilities", capabilities},
		{&result.Components.Policy, "policy", policy},
		{&result.Components.Approvals, "approvals", approvalEntries},
		{&result.Components.SecretReferences, "secret-references", secretEntries},
	}
	for _, component := range components {
		value, err := digest(component.domain, component.value)
		if err != nil {
			return nil, err
		}
		*component.target = value[:32]
	}
	revision, err := digest("revision", result.Components)
	if err != nil {
		return nil, err
	}
	result.Revision = "svctx1:" + revision
	return result, nil
}

func scenarioConstituentScriptDigest(script *dsl.Script) (string, error) {
	if script == nil {
		return "", nil
	}
	copyScript := *script
	copyScript.CreatedAt = time.Time{}
	copyScript.UpdatedAt = time.Time{}
	raw, err := json.Marshal(copyScript)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16]), nil
}

type scenarioTelemetryPolicyDigestState struct {
	Choice     string          `json:"choice"`
	Status     string          `json:"status"`
	PolicyID   string          `json:"policy_id,omitempty"`
	Definition json.RawMessage `json:"definition,omitempty"`
}

func (s *Server) scenarioTelemetryPolicyContextState(projectID, kind, choice string) (scenarioTelemetryPolicyDigestState, error) {
	normalized := normalizeTelemetryPolicyChoice(choice)
	state := scenarioTelemetryPolicyDigestState{
		Choice: normalized,
		Status: "none",
	}
	if s == nil || s.runner == nil {
		return state, nil
	}
	policyStore, ok := s.runner.testStore.(storage.AnalysisDefinitionStore)
	if !ok || policyStore == nil {
		return state, nil
	}
	var (
		found *storage.AnalysisDefinition
		err   error
	)
	if normalized == "" {
		found, err = projectDefaultPolicy(policyStore, projectID, kind)
	} else {
		found, err = projectPolicyByID(policyStore, projectID, kind, normalized)
	}
	if err != nil {
		if errors.Is(err, errRunPolicyConfiguration) {
			state.Status = "invalid"
			return state, nil
		}
		return state, fmt.Errorf("read %s: %w", kind, err)
	}
	if found == nil {
		if normalized != "" {
			state.Status = "missing"
		}
		return state, nil
	}
	state.Status = "active"
	state.PolicyID = found.ID
	state.Definition = found.Definition
	return state, nil
}

func (s *Server) attachScenarioValidationContext(r *http.Request, report *LoadModelValidationReport, projectID string, req CreateScenarioRequest) {
	if report == nil {
		return
	}
	ctx, err := s.buildScenarioValidationContext(r.Context(), projectID, req)
	if err != nil {
		return
	}
	report.ValidationContext = ctx
}
