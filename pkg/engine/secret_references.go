package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

const (
	MaxLogicalSecretNameBytes = 249
	// LogicalSecretNameInputPattern is the compatibility grammar accepted in
	// script references and secret-management APIs. Dots and hyphens are legacy
	// aliases for underscores; NormalizeLogicalSecretName returns the canonical
	// uppercase/underscore storage name.
	LogicalSecretNameInputPattern = `[A-Za-z0-9_.-]{1,249}`
)

var logicalSecretNameInputPattern = regexp.MustCompile(`^` + LogicalSecretNameInputPattern + `$`)
var scriptTemplateReferencePattern = regexp.MustCompile(`\{\{([^{}\r\n]+)\}\}`)
var dedicatedLogicalSecretFields = map[string]struct{}{
	"client_secret_ref":        {},
	"dsn_secret":               {},
	"sasl_password_secret":     {},
	"connection_string_secret": {},
	"ca_cert_secret":           {},
}
var nonExecutableRootSecretFields = map[string]struct{}{
	"id":             {},
	"ref_key":        {},
	"name":           {},
	"version":        {},
	"protocol":       {},
	"project_id":     {},
	"folder_id":      {},
	"generated_by":   {},
	"generated_from": {},
	"metadata":       {},
	"created_at":     {},
	"updated_at":     {},
}

// NormalizeLogicalSecretName validates a script-facing logical name and
// returns the canonical project-store identity. Treating dots and hyphens as
// underscore aliases preserves existing runtime references while avoiding
// provider-specific naming collisions.
func NormalizeLogicalSecretName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !logicalSecretNameInputPattern.MatchString(name) {
		return "", errors.New("secret name must contain 1-249 letters, digits, underscores, dots, or hyphens")
	}
	name = strings.ToUpper(name)
	return strings.NewReplacer(".", "_", "-", "_").Replace(name), nil
}

// ValidLogicalSecretName reports whether raw is accepted by the shared
// script/API/runtime logical-name grammar.
func ValidLogicalSecretName(raw string) bool {
	_, err := NormalizeLogicalSecretName(raw)
	return err == nil
}

type runSecretSnapshot struct {
	values   map[string]string
	missing  map[string]struct{}
	fallback SecretResolver
}

func (r runSecretSnapshot) Resolve(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if value, ok := r.values[name]; ok {
		return value, true
	}
	if _, ok := r.missing[name]; ok {
		return "", false
	}
	if r.fallback == nil {
		return "", false
	}
	return r.fallback.Resolve(name)
}

// ReferencedSecretNames returns the unique logical secret names used by
// executable script content, including protocol configuration and nested
// action parameters. Authoring provenance and non-executable review actions
// are deliberately excluded so runtime preflight, CI, and distributed delivery
// agree on which values can actually be consumed. It never resolves or returns
// secret values.
//
// Listing, binding dialogs, reservations and redaction use this full list.
// Blocking checks (missing-secret preflight, test start, distributed and CI
// delivery, the runtime snapshot) use RequiredSecretNames instead.
func ReferencedSecretNames(script *dsl.Script) ([]string, error) {
	return executableSecretNames(script, nil)
}

// RequiredSecretNames returns the referenced secret names an execution
// actually consumes: ReferencedSecretNames minus names whose only references
// are proxy credential fields the runtime never sends for the script's current
// proxy settings (dsl.InactiveProxyCredentialPaths). An unbound secret behind
// a No proxy, System or PAC HTTP proxy, or behind a browser proxy without a
// server, therefore never blocks. A name also used anywhere else stays
// required.
func RequiredSecretNames(script *dsl.Script) ([]string, error) {
	return executableSecretNames(script, dsl.InactiveProxyCredentialPaths(script))
}

// nonProxySecretNames returns the referenced names used outside every proxy
// credential field, active or not.
func nonProxySecretNames(script *dsl.Script) ([]string, error) {
	return executableSecretNames(script, []string{
		dsl.HTTPProxyUsernamePath, dsl.HTTPProxyPasswordPath,
		dsl.BrowserProxyUsernamePath, dsl.BrowserProxyPasswordPath,
	})
}

// executableSecretNames collects the executable secret names with the
// excluded slash paths (as the walk spells them) removed first.
func executableSecretNames(script *dsl.Script, excludedPaths []string) ([]string, error) {
	if script == nil {
		return nil, nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var definition any
	if err := json.Unmarshal(payload, &definition); err != nil {
		return nil, err
	}
	for _, excluded := range excludedPaths {
		removeDefinitionPath(definition, excluded)
	}
	seen := make(map[string]struct{})
	if err := collectExecutableSecretNames(definition, "", true, false, seen, nil); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// removeDefinitionPath deletes the object member a slash path names from a
// decoded script definition. The script's JSON members are lowercase, as the
// walk spells its paths.
func removeDefinitionPath(definition any, slashPath string) {
	segments := strings.Split(strings.Trim(slashPath, "/"), "/")
	current, _ := definition.(map[string]any)
	for index, segment := range segments {
		if current == nil {
			return
		}
		if index == len(segments)-1 {
			delete(current, segment)
			return
		}
		current, _ = current[segment].(map[string]any)
	}
}

// MissingSecretProxyBlockers splits missing required secret names into the
// proxy-specific blocker messages (dsl.ProxyCredentialSecretReference
// BlockerMessage) for names used only by an active proxy credential field, and
// the remaining names, which callers report with their own wording.
func MissingSecretProxyBlockers(script *dsl.Script, missing []string) (proxyMessages []string, others []string) {
	if len(missing) == 0 {
		return nil, nil
	}
	nonProxy := map[string]struct{}{}
	if names, err := nonProxySecretNames(script); err == nil {
		for _, name := range names {
			nonProxy[name] = struct{}{}
		}
	}
	activeRefs := map[string][]dsl.ProxyCredentialSecretReference{}
	for _, ref := range dsl.ProxyCredentialSecretReferences(script) {
		if ref.Active {
			activeRefs[ref.Name] = append(activeRefs[ref.Name], ref)
		}
	}
	for _, name := range missing {
		refs := activeRefs[strings.TrimSpace(name)]
		if _, elsewhere := nonProxy[strings.TrimSpace(name)]; elsewhere || len(refs) == 0 {
			others = append(others, name)
			continue
		}
		for _, ref := range refs {
			proxyMessages = append(proxyMessages, ref.BlockerMessage())
		}
	}
	return proxyMessages, others
}

// MissingSecretsError formats the shared fail-closed error for missing
// required secrets: the proxy-specific blocker for a name only an active proxy
// credential uses, and prefix plus the remaining names otherwise.
func MissingSecretsError(script *dsl.Script, prefix string, missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	proxyMessages, others := MissingSecretProxyBlockers(script, missing)
	parts := append([]string(nil), proxyMessages...)
	if len(others) > 0 {
		parts = append(parts, prefix+strings.Join(others, ", "))
	}
	return errors.New(strings.Join(parts, "; "))
}

func collectExecutableSecretNames(value any, path string, root, dedicatedField bool, seen, dedicated map[string]struct{}) error {
	switch typed := value.(type) {
	case map[string]any:
		if isNonExecutableSecretReviewMap(typed) {
			return nil
		}
		structuredBody := false
		if body, ok := typed["body"].(string); ok {
			encoding, _ := typed["body_encoding"].(string)
			metadata, _ := typed["metadata"].(map[string]any)
			mode, _ := metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey].(string)
			if metadata[dsl.ImportAuraTemplateContractMetadataKey] != nil && strings.TrimSpace(mode) != dsl.ImportAuraJSONTemplateValueEncoding {
				return &aurapayload.Error{Code: aurapayload.InvalidTemplateContract}
			}
			if strings.TrimSpace(mode) == dsl.ImportAuraJSONTemplateValueEncoding {
				structuredBody = true
				var referenceErr error
				remaining := aurapayload.MaxTemplateOccurrences
				_, _, _, codecErr := aurapayload.ResolveContextualWithWorkAccounting(body, encoding, metadata[dsl.ImportAuraTemplateContractMetadataKey], func(value string, templateContext aurapayload.TemplateContext) (string, string) {
					if referenceErr == nil {
						// Apply the selected runtime occurrence cap before the legacy
						// reference collector allocates match details for this leaf.
						matches := templatePattern.FindAllStringIndex(value, remaining+1)
						if len(matches) > remaining {
							referenceErr = &aurapayload.Error{Code: aurapayload.TokenLimit}
						} else {
							remaining -= len(matches)
							for index, match := range matches {
								if !templateContext.IsLiteralToken(index) {
									referenceErr = collectExecutableSecretNames(value[match[0]:match[1]], path+"/body", false, false, seen, dedicated)
									if referenceErr != nil {
										break
									}
								}
							}
						}
					}
					return value, value
				}, aurapayload.MaxWorkBytes)
				if codecErr != nil {
					return fmt.Errorf("script structured body is invalid: %w", codecErr)
				}
				if referenceErr != nil {
					return referenceErr
				}
			} else if decoded, encoded, err := dsl.DecodeBodyEncoding(body, encoding, 0); encoded && err == nil {
				// The stored base64 text is not the executable credential
				// surface. Decode the wire bytes so static secret preflight and
				// redaction configuration see references before transport.
				if err := collectExecutableSecretNames(string(decoded), path+"/body", false, false, seen, dedicated); err != nil {
					return err
				}
			}
		}
		actionType, _ := typed["type"].(string)
		for key, child := range typed {
			if key == "params" && dsl.CanonicalActionType(actionType) == "native_javascript" {
				if params, ok := child.(map[string]any); ok {
					resolved := make(map[string]any, len(params))
					for name, value := range params {
						if name != "source" {
							resolved[name] = value
						}
					}
					child = resolved
				}
			}
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			// The selected codec already visited every executable decoded leaf.
			// Rescanning the encoded body would miss form references and invent
			// executable references in fixed JSON member names.
			if structuredBody && normalizedKey == "body" {
				continue
			}
			if root {
				if _, skip := nonExecutableRootSecretFields[normalizedKey]; skip {
					continue
				}
			}
			// Java VM parameters are deliberately never secret-resolved: argv is
			// observable to sibling processes. Current authoring/DSL validation
			// rejects templates there, while this skip keeps a legacy row from
			// creating a misleading project-secret requirement that runtime
			// would preserve literally and the Java runner would reject.
			if path == "/config/java_vuser" && normalizedKey == "additional_vm_parameters" {
				continue
			}
			// NOTHING in action metadata is exempt here. ReferencedSecretNames
			// drives the runtime RedactionConfig and the fail-closed missing-secret
			// preflight, and kafka.buildRecord / jms.buildMessage copy metadata
			// entries onto the wire as headers/properties — including original_url
			// and source_transaction (see TestJMSPublishWithMetadataAndNoPropertiesConfig,
			// which asserts source_transaction is delivered as a JMS property).
			// Exempting them here would leave a resolved value absent from the
			// redaction set: plaintext in logs, evidence bundles, and archives.
			// Suppressing the "configure this secret" PROMPT is a UI concern and is
			// handled separately in pkg/api (isImportProvenanceMetadataPath).
			_, childDedicated := dedicatedLogicalSecretFields[normalizedKey]
			if err := collectExecutableSecretNames(child, path+"/"+normalizedKey, false, childDedicated, seen, dedicated); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := collectExecutableSecretNames(child, path, false, dedicatedField, seen, dedicated); err != nil {
				return err
			}
		}
	case string:
		for _, match := range scriptTemplateReferencePattern.FindAllStringSubmatch(typed, -1) {
			if len(match) < 2 {
				continue
			}
			expression := strings.TrimSpace(match[1])
			if !strings.HasPrefix(expression, "secret.") {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(expression, "secret."))
			if _, err := NormalizeLogicalSecretName(name); err != nil {
				return fmt.Errorf("script contains an invalid logical secret name")
			}
			seen[name] = struct{}{}
			if dedicated != nil && dedicatedField {
				dedicated[name] = struct{}{}
			}
		}
		if dedicatedField && strings.TrimSpace(typed) != "" && !strings.Contains(typed, "{{") {
			name := strings.TrimSpace(typed)
			if ValidLogicalSecretName(name) {
				// Dedicated fields also support provider/file references and
				// inline PEM material. Only logical-name-shaped values are
				// controller-managed project secret references.
				seen[name] = struct{}{}
				if dedicated != nil {
					dedicated[name] = struct{}{}
				}
			}
		}
	}
	return nil
}

func isNonExecutableSecretReviewMap(value map[string]any) bool {
	if _, hasName := value["name"]; !hasName {
		return false
	}
	if _, hasTarget := value["target"]; !hasTarget {
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

func dedicatedLogicalSecretNames(script *dsl.Script) (map[string]struct{}, error) {
	if script == nil {
		return map[string]struct{}{}, nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var definition any
	if err := json.Unmarshal(payload, &definition); err != nil {
		return nil, err
	}
	names := make(map[string]struct{})
	allNames := make(map[string]struct{})
	if err := collectExecutableSecretNames(definition, "", true, false, allNames, names); err != nil {
		return nil, err
	}
	return names, nil
}

// snapshotReferencedSecrets resolves every static secret reference once and
// returns an immutable resolver for the lifetime of one execution. Missing
// names are snapshotted too, so a deferred/missing secret cannot trigger a
// provider request on every VUser iteration. Unknown dynamically introduced
// names fall through to the bounded cache.
func snapshotReferencedSecrets(script *dsl.Script, resolver SecretResolver) (SecretResolver, []string, error) {
	ctx, cancel := WithSecretResolutionBudget(context.Background())
	defer cancel()
	return snapshotReferencedSecretsContext(ctx, script, resolver)
}

func snapshotReferencedSecretsContext(ctx context.Context, script *dsl.Script, resolver SecretResolver) (SecretResolver, []string, error) {
	if resolver == nil {
		return nil, nil, nil
	}
	// Only required names are snapshotted: a proxy credential the runtime
	// never sends (appendHTTPSecurityConfig / appendBrowserConfig drop it) is
	// neither resolved nor allowed to fail the run when it is unbound.
	names, err := RequiredSecretNames(script)
	if err != nil {
		return nil, nil, err
	}
	if len(names) > maxCachedSecrets {
		return nil, nil, fmt.Errorf("script references more than %d logical secrets", maxCachedSecrets)
	}
	cached, resolvedValues, missingNames, err := resolveSecretNames(ctx, names, resolver)
	if err != nil {
		return nil, nil, err
	}
	snapshot := runSecretSnapshot{
		values:   make(map[string]string, len(names)),
		missing:  make(map[string]struct{}),
		fallback: cached,
	}
	values := make([]string, 0, len(names))
	snapshotBytes := 0
	for _, name := range names {
		if len(name) > maxSecretNameBytes {
			return nil, nil, fmt.Errorf("script contains an invalid logical secret name")
		}
		if value, ok := resolvedValues[name]; ok && value != "" {
			if len(name)+len(value) > maxCachedSecretBytes-snapshotBytes {
				return nil, nil, fmt.Errorf("resolved script secrets exceed the runtime snapshot limit")
			}
			snapshotBytes += len(name) + len(value)
			snapshot.values[name] = value
			values = append(values, value)
		} else {
			if len(name) > maxCachedSecretBytes-snapshotBytes {
				return nil, nil, fmt.Errorf("resolved script secrets exceed the runtime snapshot limit")
			}
			snapshotBytes += len(name)
			snapshot.missing[name] = struct{}{}
		}
	}
	if len(missingNames) > 0 {
		return nil, nil, MissingSecretsError(script, "required project secret(s) are not configured: ", missingNames)
	}
	return snapshot, values, nil
}

// WithSecretResolutionBudget applies the shared overall provider budget used
// by execution, replay, CI, preflight, and distributed delivery. A shorter
// caller deadline remains authoritative.
func WithSecretResolutionBudget(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, defaultSecretResolutionBudget)
}

// ResolveSecretNames resolves a bounded name set under one shared deadline.
// Values are returned only to the runtime caller and are never included in an
// error, count, length, or other diagnostic metadata.
func ResolveSecretNames(ctx context.Context, names []string, resolver SecretResolver) (map[string]string, []string, error) {
	_, values, missing, err := resolveSecretNames(ctx, names, resolver)
	return values, missing, err
}

func resolveSecretNames(ctx context.Context, names []string, resolver SecretResolver) (SecretResolver, map[string]string, []string, error) {
	if len(names) > maxCachedSecrets {
		return nil, nil, nil, fmt.Errorf("secret resolution request exceeds the supported name limit")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cached := CacheSecretResolverForRun(resolver)
	values := make(map[string]string, len(names))
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if name == "" || len(name) > maxSecretNameBytes {
			return nil, nil, nil, fmt.Errorf("secret resolution request contains an invalid logical name")
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, fmt.Errorf("project secret resolution exceeded its overall deadline: %w", err)
		}
		value, ok := resolveSecretWithContext(ctx, cached, name)
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, fmt.Errorf("project secret resolution exceeded its overall deadline: %w", err)
		}
		if !ok || value == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = value
	}
	return cached, values, missing, nil
}
