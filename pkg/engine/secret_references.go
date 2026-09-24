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
func ReferencedSecretNames(script *dsl.Script) ([]string, error) {
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

func collectExecutableSecretNames(value any, path string, root, dedicatedField bool, seen, dedicated map[string]struct{}) error {
	switch typed := value.(type) {
	case map[string]any:
		if isNonExecutableSecretReviewMap(typed) {
			return nil
		}
		if body, ok := typed["body"].(string); ok {
			encoding, _ := typed["body_encoding"].(string)
			if decoded, encoded, err := dsl.DecodeBodyEncoding(body, encoding, 0); encoded && err == nil {
				// The stored base64 text is not the executable credential
				// surface. Decode the wire bytes so static secret preflight and
				// redaction configuration see references before transport.
				if err := collectExecutableSecretNames(string(decoded), path+"/body", false, false, seen, dedicated); err != nil {
					return err
				}
			}
		}
		for key, child := range typed {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
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
	names, err := ReferencedSecretNames(script)
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
		return nil, nil, fmt.Errorf("required project secret(s) are not configured: %s", strings.Join(missingNames, ", "))
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
