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
// depends on, for the script as the caller sees it in projectID. Every
// component is a keyed digest: a secret's value enters only as an HMAC under
// a per-deployment key, so neither the revision nor a component discloses it.
func (s *Server) buildValidationContext(ctx context.Context, projectID string, script *dsl.Script, resolver engine.SecretResolver) (*validationContext, error) {
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

	// Secret references: each referenced name (bounded, sorted) with its
	// availability and a keyed digest of its current value, so a rebinding
	// changes the context while the value stays undisclosed.
	secrets, truncated, missing, err := validationContextSecrets(ctx, script, resolver, func(name, value string) (string, error) {
		return digest("secret-value", []string{name, value})
	})
	if err != nil {
		return nil, err
	}
	// The resolver reports a provider failure as a miss; only a store that
	// answered "not found" makes a secret unavailable.
	if err := s.secretProviderFailure(ctx, script.ProjectID, script, missing); err != nil {
		return nil, fmt.Errorf("resolve secret references: %w", err)
	}
	result.SecretReferencesTruncated = truncated
	for _, name := range missing {
		result.Findings = append(result.Findings, importreview.ValidationContextFinding{Cause: "secret_unavailable", Subject: name, Detail: "the referenced secret is not configured for this project"})
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
// at most validationContextSecretLimit) with availability and a value
// digest, plus the unavailable names.
func validationContextSecrets(ctx context.Context, script *dsl.Script, resolver engine.SecretResolver, valueDigest func(name, value string) (string, error)) ([][3]string, bool, []string, error) {
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
	values := map[string]string{}
	unresolved := names
	if resolver != nil && len(names) > 0 {
		resolveCtx, cancel := engine.WithSecretResolutionBudget(ctx)
		resolved, missing, err := engine.ResolveSecretNames(resolveCtx, names, resolver)
		cancel()
		if err != nil {
			// An unreachable provider is not an unconfigured secret.
			return nil, false, nil, fmt.Errorf("resolve secret references: %w", err)
		}
		values, unresolved = resolved, missing
	}
	unavailable := map[string]bool{}
	for _, name := range unresolved {
		unavailable[name] = true
	}
	entries := make([][3]string, 0, len(names))
	missing := make([]string, 0)
	for _, name := range names {
		if unavailable[name] {
			entries = append(entries, [3]string{name, "unavailable", ""})
			missing = append(missing, name)
			continue
		}
		digest, err := valueDigest(name, values[name])
		if err != nil {
			return nil, false, nil, err
		}
		entries = append(entries, [3]string{name, "available", digest})
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
func (s *Server) attachValidationContext(r *http.Request, response *scriptValidationResponse, projectID string, script *dsl.Script, resolver engine.SecretResolver) {
	validation, err := s.buildValidationContext(r.Context(), projectID, script, resolver)
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
// missing, the managed stores that resolve the script's secrets and the
// application read that scopes them. projectID is the script's own, exactly
// as the resolver used it: a draft without one resolved no project secrets. It returns an error when any read
// failed (other than "not found") or found a usable value: either way the
// secret is not known to be unconfigured. A Key Vault fallback reports no
// errors to recheck, so its outage still reads as unavailable.
func (s *Server) secretProviderFailure(ctx context.Context, projectID string, script *dsl.Script, names []string) error {
	tr := s.runner
	if tr == nil || len(names) == 0 {
		return nil
	}
	type lookup func(context.Context, string) (string, error)
	lookups := make([]lookup, 0, 2)
	if tr.projectSecretStore != nil && strings.TrimSpace(projectID) != "" {
		lookups = append(lookups, func(ctx context.Context, name string) (string, error) {
			value, _, err := tr.projectSecretStore.Get(ctx, projectID, name)
			return value, err
		})
	}
	if tr.applicationSecretStore != nil && tr.libraryAssignmentStore != nil && script != nil && strings.TrimSpace(script.ID) != "" {
		assignments, err := tr.libraryAssignmentStore.ListLibraryAssignments("script", []string{script.ID})
		if err != nil {
			return err
		}
		if assignment := assignments[script.ID]; assignment != nil && assignment.Application != nil {
			if tr.applicationStore != nil {
				if _, err := tr.applicationStore.GetApplication(assignment.Application.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			scope := storage.ApplicationSecretScopeID(assignment.Application.ID)
			lookups = append(lookups, func(ctx context.Context, name string) (string, error) {
				value, _, err := tr.applicationSecretStore.Get(ctx, scope, name)
				return value, err
			})
		}
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
