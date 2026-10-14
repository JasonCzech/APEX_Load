package credentialpolicy

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
	"golang.org/x/text/encoding/ianaindex"
)

// Disposition describes whether a projected value can be replayed without
// source-specific review. Projection functions only return disclosure-safe
// findings; source values are retained exclusively in Requirement.Value.
type Disposition string

const (
	ExecutableExact   Disposition = "executable_exact"
	ExecutableBounded Disposition = "executable_bounded"
	BlockedDraft      Disposition = "blocked_draft"
	Rejected          Disposition = "rejected"
)

// Finding is a source-neutral, disclosure-safe projection diagnostic.
type Finding struct {
	Code        string      `json:"code"`
	Source      SourceRef   `json:"source,omitempty"`
	Disposition Disposition `json:"disposition"`
}

// Limits bounds parser work for attacker-controlled import artifacts.
type Limits struct {
	MaxBytes  int
	MaxDepth  int
	MaxValues int
	MaxParts  int
}

// DefaultLimits returns conservative limits suitable for individual request
// components. Callers may lower these for their own source format.
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:  8 << 20,
		MaxDepth:  128,
		MaxValues: 100_000,
		MaxParts:  10_000,
	}
}

// Options controls one deterministic projection operation.
type Options struct {
	Mode       ProjectionMode
	Source     SourceRef
	RequestKey string
	NamePrefix string
	Registry   *NameRegistry
	Limits     Limits
	// SensitiveNames lets a source adapter project fields whose source schema
	// explicitly marks them as credentials even when their authored names are
	// not part of the shared vocabulary (for example an OpenAPI property named
	// "pin" with format: password). Keys are normalized, case-insensitive
	// source field names; values are stable requirement labels/name hints.
	SensitiveNames map[string]string
	ProvenVars     map[string]struct{}
	// SourceVariables contains source-defined literal values that an adapter
	// translated into {{var.*}} or {{param.*}} references. The map is consulted
	// only inside credential-bearing scalars so ordinary parameterization
	// remains intact. For {{var.*}}, a name present in ProvenVars wins because
	// a prior response extractor has established dynamic runtime provenance at
	// the current action.
	SourceVariables map[string]SourceVariable
	// TrustedWholeValues contains exact scalar values whose dynamic provenance
	// the source adapter has established. It is intentionally exact-match only:
	// composed values must still be governed as one credential-bearing value.
	TrustedWholeValues map[string]struct{}
	// PreGovernedHeaderOccurrences identifies ordered header occurrences whose
	// exact values were constructed by a source adapter from already-governed
	// runtime references. Only ProjectHeaders consumes this field; every other
	// request component still passes through the shared credential policy.
	PreGovernedHeaderOccurrences map[int]struct{}
	// KnownCredentialMatcher is an optional disclosure-only hook for literal
	// credentials already proven on another request surface. Structured
	// projectors invoke it only for semantic scalar values, never member names,
	// MIME boundaries, XML namespaces, or grammar tokens. A match redacts the
	// complete scalar so unrelated wire bytes retain their authored spelling.
	// Import conversion must leave this nil: executable projection is governed
	// by source fields and requirements, not cross-field value equivalence.
	KnownCredentialMatcher    func(string) bool
	TargetField               string
	multipartDepth            int
	preserveSourceExpression  bool
	knownCredentialBudget     *knownCredentialComparisonBudget
	structuredRedactionBudget *structuredRedactionBudget
	nestedJSONStringDepth     int
	jsonDepthBase             int
	nestedJSON                bool
}

type knownCredentialComparisonBudget struct {
	remainingBytes int64
}

// Result is the common sidecar returned by projection functions.
type Result struct {
	Requirements []Requirement `json:"requirements,omitempty"`
	Findings     []Finding     `json:"findings,omitempty"`
	Disposition  Disposition   `json:"disposition"`
	// PreservedTargets contains locators only, never source values. Import
	// finalizers persist these locators beside the affected executable action
	// so runtime redaction can recover literal credentials from the durable
	// request without asking the operator to bind or re-enter them.
	PreservedTargets []TargetRef `json:"-"`
}

// Merge combines result sidecars without changing encounter order.
func (result *Result) Merge(other Result) {
	if result == nil {
		return
	}
	result.Requirements = append(result.Requirements, other.Requirements...)
	result.Findings = append(result.Findings, other.Findings...)
	result.PreservedTargets = append(result.PreservedTargets, other.PreservedTargets...)
	result.Disposition = worseDisposition(result.Disposition, other.Disposition)
}

// preserveRequiresCredentialReplacement distinguishes ordinary Preserve-mode
// credentials (whose exact authored bytes intentionally remain executable)
// from disclosure placeholders or encodings that cannot supply credential
// material. Byte-preserving structured writers must still apply the generated
// secret reference for the latter; otherwise their sidecar would claim the
// source is unavailable while the executable payload retained [REDACTED].
func preserveRequiresCredentialReplacement(result Result) bool {
	for _, requirement := range result.Requirements {
		switch requirement.SourceValueStatus {
		case SourceCredentialValueRedactedPlaceholder,
			SourceCredentialValueUnsafeEncoding:
			return true
		}
	}
	return false
}

// HeaderOccurrence is one ordered HTTP header. Duplicate names and authored
// casing are intentionally retained.
type HeaderOccurrence struct {
	Name   string
	Value  string
	Source SourceRef
}

const DisclosureMultipartBoundary = "apex-redacted-boundary"

func disclosureMultipartBoundaryReplacement(boundary string) string {
	if boundary == DisclosureMultipartBoundary {
		return DisclosureMultipartBoundary + "-alternate"
	}
	return DisclosureMultipartBoundary
}

// AuthFields is the typed portion of effective request authentication.
type AuthFields struct {
	Type     string
	Username string
	Password string
	Token    string
	Domain   string
	Source   SourceRef
}

// FormField preserves ordered form occurrences and their raw encoding.
type FormField struct {
	Name       string
	Value      string
	RawName    string
	RawValue   string
	HadEquals  bool
	Occurrence int
	Source     SourceRef
}

// MultipartPart is an ordered, source-neutral multipart part.
type MultipartPart struct {
	Headers     []HeaderOccurrence
	Name        string
	FileName    string
	ContentType string
	Body        []byte
	Source      SourceRef
}

// Multipart is a structured multipart body. Boundary is source-preserved.
type Multipart struct {
	Boundary string
	Parts    []MultipartPart
}

// GraphQL is the shared GraphQL body shape. Query is preserved byte-for-byte;
// Variables is projected using the JSON policy.
type GraphQL struct {
	Query     []byte
	Variables []byte
}

// WholeTemplateKind classifies an entire scalar value. Composed templates are
// deliberately not returned as whole templates.
type WholeTemplateKind string

const (
	TemplateNone     WholeTemplateKind = ""
	TemplateSecret   WholeTemplateKind = "secret"
	TemplateEnv      WholeTemplateKind = "env"
	TemplateDynamic  WholeTemplateKind = "dynamic_var"
	TemplateUnproven WholeTemplateKind = "unproven_var"
	TemplateOther    WholeTemplateKind = "other"
)

// ClassifyWholeTemplate distinguishes governed whole-value templates from
// composed values such as "Bearer {{var.token}}".
func ClassifyWholeTemplate(value string, provenVars map[string]struct{}) (WholeTemplateKind, string) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") ||
		strings.Contains(trimmed[2:len(trimmed)-2], "{{") ||
		strings.Contains(trimmed[2:len(trimmed)-2], "}}") {
		return TemplateNone, ""
	}
	expression := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	namespace, name, found := strings.Cut(expression, ".")
	if !found || strings.TrimSpace(name) == "" {
		return TemplateOther, expression
	}
	namespace = strings.ToLower(strings.TrimSpace(namespace))
	name = strings.TrimSpace(name)
	switch namespace {
	case "secret":
		if normalized, ok := credential.ParseReference(trimmed); ok {
			return TemplateSecret, normalized
		}
		return TemplateOther, expression
	case "env":
		return TemplateEnv, name
	case "var":
		if _, ok := provenVars[name]; ok {
			return TemplateDynamic, name
		}
		return TemplateUnproven, name
	default:
		return TemplateOther, expression
	}
}

func normalizeOptions(options Options) (Options, error) {
	if options.Mode == "" {
		// Fail toward runnability. An omitted policy used to become
		// SecretReference, so any adapter that forgot to set Mode silently
		// rewrote executable credential values into {{secret.NAME}} references
		// the operator had never bound — an import that produces a script which
		// cannot replay. Import conversion always preserves what the source
		// already contained; every non-preserving consumer (API/script
		// disclosure projections use Redact, storage migration uses Audit,
		// explicit externalization uses SecretReference) states its mode
		// explicitly, and a disclosure caller that forgets to do so still fails
		// loudly through the KnownCredentialMatcher check below.
		options.Mode = Preserve
	}
	if !options.Mode.Valid() {
		return options, fmt.Errorf("unsupported credential projection mode %q", options.Mode)
	}
	if options.KnownCredentialMatcher != nil && options.Mode != Redact {
		return options, errors.New("known credential matcher requires redact projection mode")
	}
	defaults := DefaultLimits()
	if options.Limits.MaxBytes == 0 {
		options.Limits.MaxBytes = defaults.MaxBytes
	}
	if options.Limits.MaxDepth == 0 {
		options.Limits.MaxDepth = defaults.MaxDepth
	}
	if options.Limits.MaxValues == 0 {
		options.Limits.MaxValues = defaults.MaxValues
	}
	if options.Limits.MaxParts == 0 {
		options.Limits.MaxParts = defaults.MaxParts
	}
	if options.Limits.MaxBytes < 1 || options.Limits.MaxDepth < 1 ||
		options.Limits.MaxValues < 1 || options.Limits.MaxParts < 1 {
		return options, errors.New("credential projection limits must be positive")
	}
	if options.KnownCredentialMatcher != nil &&
		options.knownCredentialBudget == nil {
		remaining := int64(options.Limits.MaxBytes)
		if remaining > (1<<62)/2 {
			remaining = 1 << 62
		} else {
			remaining *= 2
		}
		options.knownCredentialBudget = &knownCredentialComparisonBudget{
			remainingBytes: remaining,
		}
	}
	if options.Mode == Redact && options.structuredRedactionBudget == nil {
		options.structuredRedactionBudget = newStructuredRedactionBudget(options.Limits)
	}
	if options.Registry == nil {
		options.Registry = NewNameRegistry()
	}
	return options, nil
}

func governedSensitiveName(name string, options Options) (string, bool) {
	if strings.Contains(name, ":") && credential.SensitiveXMLName(name) {
		// Namespace prefixes are wire syntax, not part of the stable logical
		// secret name. Retain the pre-shared-policy XML naming behavior.
		return xmlLocalName(name), true
	}
	if credential.SensitiveName(name) || credential.SensitiveXMLName(name) {
		return name, true
	}
	if len(options.SensitiveNames) == 0 {
		return "", false
	}
	normalized := strings.ToLower(strings.TrimSpace(xmlLocalName(name)))
	if normalized == "" {
		return "", false
	}
	label, ok := options.SensitiveNames[normalized]
	if !ok {
		return "", false
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = xmlLocalName(name)
	}
	return label, true
}

func exactResult() Result {
	return Result{Disposition: ExecutableExact}
}

func preserved(source SourceRef, code string) Result {
	if strings.TrimSpace(code) == "" {
		code = "import_credential_preserved"
	}
	disposition := ExecutableExact
	if code == "import_credential_review_incomplete" {
		// The value remains byte-exact, but bounded classification did not
		// inspect it completely. It must not be executable until reviewed.
		disposition = BlockedDraft
	}
	return Result{
		Disposition: disposition,
		Findings: []Finding{{
			Code:        code,
			Source:      source,
			Disposition: disposition,
		}},
	}
}

func preservedPayloadResult(payload []byte, options Options) Result {
	if containsSensitiveNameHintWithOptions(payload, options) {
		return preserved(options.Source, "import_credential_preserved")
	}
	return exactResult()
}

// preservedOpaquePayloadResult retains source bytes that a structured parser
// could not inspect safely. The opaque locator deliberately names the complete
// executable body instead of inventing a JSON/XML pointer that the runtime
// could not verify. This lets runtime disclosure redaction protect the retained
// bytes without making the operator re-enter them.
func preservedOpaquePayloadResult(payload []byte, options Options, code string) Result {
	result := blocked(options.Source, code)
	if len(payload) == 0 {
		return result
	}
	result.Merge(preservedOpaqueTargetResult(options))
	return result
}

// preservedOpaqueTargetResult marks an exact executable body whose structured
// representation was retained without complete inspection. The locator is
// deliberately emitted regardless of a bounded name-hint scan: the fallback
// exists precisely because classification was incomplete, so absence of a hint
// is not proof that no literal credential remains.
func preservedOpaqueTargetResult(options Options) Result {
	item := preserved(options.Source, "import_credential_preserved")
	field := strings.TrimSuffix(strings.TrimSpace(options.TargetField), ".")
	if field == "" {
		field = "body"
	}
	item.PreservedTargets = append(item.PreservedTargets, TargetRef{
		RequestKey: options.RequestKey,
		Field:      field + ".opaque",
	})
	return item
}

func blocked(source SourceRef, code string) Result {
	return Result{
		Disposition: BlockedDraft,
		Findings: []Finding{{
			Code:        code,
			Source:      source,
			Disposition: BlockedDraft,
		}},
	}
}

func rejected(source SourceRef, code string) Result {
	return Result{
		Disposition: Rejected,
		Findings: []Finding{{
			Code:        code,
			Source:      source,
			Disposition: Rejected,
		}},
	}
}

func worseDisposition(left, right Disposition) Disposition {
	rank := func(value Disposition) int {
		switch value {
		case Rejected:
			return 4
		case BlockedDraft:
			return 3
		case ExecutableBounded:
			return 2
		case ExecutableExact:
			return 1
		default:
			return 0
		}
	}
	if rank(right) > rank(left) {
		return right
	}
	if left == "" {
		return ExecutableExact
	}
	return left
}

func projectSensitiveScalar(value, name string, source SourceRef, target TargetRef, occurrence int, options Options) (string, Result, error) {
	target.Occurrence = occurrence
	sourceExpression := value
	value, sourceVariableNames := MaterializeSourceVariables(value, options)
	valueStatus := ClassifySourceCredentialValue(value)
	if valueStatus == SourceCredentialValueRedactedPlaceholder ||
		valueStatus == SourceCredentialValueUnsafeEncoding {
		requirement, projected, err := NewRequirement(RequirementSpec{
			Key:                 name,
			Label:               name,
			Context:             target.Field,
			NameHint:            firstNonEmpty(options.NamePrefix, options.RequestKey) + "_" + name,
			Source:              source,
			Targets:             []TargetRef{target},
			Value:               value,
			SourceVariableNames: sourceVariableNames,
			Mode:                options.Mode,
			Registry:            options.Registry,
		})
		if err != nil {
			return "", Result{}, err
		}
		result := exactResult()
		result.Requirements = append(result.Requirements, requirement)
		result.Merge(blocked(source, "import_credential_source_unavailable"))
		return projected, result, nil
	}
	if _, trusted := options.TrustedWholeValues[strings.TrimSpace(value)]; trusted {
		result := exactResult()
		if options.Mode == Preserve {
			result.PreservedTargets = append(result.PreservedTargets, target)
		}
		return value, result, nil
	}
	kind, templateName := ClassifyWholeTemplate(value, options.ProvenVars)
	switch kind {
	case TemplateSecret:
		if options.Mode == Preserve {
			// A pre-governed secret reference is already runnable and does not
			// represent a literal retained credential or a new binding.
			reference, err := credential.Reference(templateName)
			if err != nil {
				return "", Result{}, err
			}
			return reference, exactResult(), nil
		}
		requirement, projected, err := NewRequirement(RequirementSpec{
			Key:                 name,
			Label:               name,
			Context:             target.Field,
			NameHint:            firstNonEmpty(options.NamePrefix, options.RequestKey) + "_" + name,
			Source:              source,
			Targets:             []TargetRef{target},
			Value:               value,
			ExistingReference:   true,
			SourceVariableNames: sourceVariableNames,
			Mode:                options.Mode,
			Registry:            options.Registry,
		})
		if err != nil {
			return "", Result{}, err
		}
		result := exactResult()
		result.Requirements = append(result.Requirements, requirement)
		return projected, result, nil
	case TemplateEnv, TemplateDynamic:
		// Already governed environment values and response-derived variables
		// remain executable. They do not carry source migration material.
		//
		// Preserve-mode imports still need a value-free locator. The runtime
		// cannot know the resolved env/var value while the durable script is
		// inspected, and structured request bodies are not otherwise scanned
		// wholesale after interpolation. Recording the affected field lets the
		// execution boundary add the exact resolved value to its memory-only
		// redaction matcher without changing the wire request or asking the
		// operator to bind the credential again.
		result := exactResult()
		if options.Mode == Preserve {
			result.PreservedTargets = append(result.PreservedTargets, target)
		}
		return value, result, nil
	case TemplateUnproven:
		// A variable-looking token is not trusted merely because of its syntax.
		// Govern the complete value instead of leaving a potentially static
		// credential embedded in a composed scalar.
		templateName = ""
	}
	_ = templateName
	if options.Mode == Preserve {
		// A literal imported credential is already present in the operator's
		// source and must remain runnable in the destination script. It is a
		// review finding, not a binding requirement: emitting a requirement
		// here would make the UI ask the operator to re-enter a value that was
		// deliberately retained.
		result := preserved(source, "import_credential_preserved")
		result.PreservedTargets = append(result.PreservedTargets, target)
		if options.preserveSourceExpression {
			return sourceExpression, result, nil
		}
		return value, result, nil
	}
	requirement, projected, err := NewRequirement(RequirementSpec{
		Key:                 name,
		Label:               name,
		Context:             target.Field,
		NameHint:            firstNonEmpty(options.NamePrefix, options.RequestKey) + "_" + name,
		Source:              source,
		Targets:             []TargetRef{target},
		Value:               value,
		SourceVariableNames: sourceVariableNames,
		Mode:                options.Mode,
		Registry:            options.Registry,
	})
	if err != nil {
		return "", Result{}, err
	}
	if hasCredentialSourceVariable(value) {
		// A source template is not the source secret. Advertising or migrating
		// it as Requirement.Value would falsely complete binding and could
		// persist "{{param.password}}" as the project secret. Retain only the
		// protected expression so an importer that attaches parameters later can
		// reconcile it to an actual value.
		//
		// Only {{var.*}}/{{param.*}} defer: they name source material that a
		// later adapter pass can resolve. A composed value whose only templates
		// are already-governed references ({{secret.*}}, {{env.*}}) has nothing
		// left to reconcile, so its static bytes are authored credential
		// material and must stay migratable — blanking Value there would strand
		// the operator with a governed field and no way to recover the value.
		requirement.DeferredSourceExpression = sourceExpression
		requirement.Value = ""
		requirement.SourceValueAvailable = false
		requirement.SourceVariableNames = normalizeSourceVariableNames(append(
			requirement.SourceVariableNames,
			unprovenCredentialSourceVariableNames(sourceExpression, options.ProvenVars)...,
		))
	}
	result := exactResult()
	result.Requirements = append(result.Requirements, requirement)
	switch options.Mode {
	case Audit:
		result.Merge(blocked(source, "import_credentials_unmanaged"))
	}
	return projected, result, nil
}

var (
	credentialSourceVariablePattern = regexp.MustCompile(`\{\{\s*(var|param)\.([A-Za-z0-9_.-]+)\s*\}\}`)
	runtimeTemplatePattern          = regexp.MustCompile(`\{\{[^{}]*\}\}`)
)

// HasRuntimeTemplate reports whether value contains syntax the DSL runtime
// treats as a template. It is intentionally namespace-neutral: composed env,
// secret, function, var, and param expressions all require whole-value
// governance on a sensitive surface.
func HasRuntimeTemplate(value string) bool {
	return runtimeTemplatePattern.MatchString(value)
}

// hasCredentialSourceVariable reports whether value references source material a
// later adapter pass could still resolve into a literal ({{var.*}}/{{param.*}}).
// Already-governed references ({{secret.*}}, {{env.*}}) do not qualify: nothing
// reconciles them, so a value containing only those is a complete authored
// credential rather than a deferred one.
func hasCredentialSourceVariable(value string) bool {
	return credentialSourceVariablePattern.MatchString(value)
}

func unprovenCredentialSourceVariableNames(
	value string,
	provenVars map[string]struct{},
) []string {
	matches := credentialSourceVariablePattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		namespace := strings.TrimSpace(match[1])
		name := strings.TrimSpace(match[2])
		if name == "" {
			continue
		}
		if namespace == "var" {
			if _, proven := provenVars[name]; proven {
				continue
			}
		}
		names[name] = struct{}{}
	}
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// MaterializeSourceVariables resolves only importer-proven source literals
// represented by {{var.*}} or {{param.*}} inside a credential-bearing scalar.
// It deliberately performs one bounded pass: nested or cyclic source
// templates remain visible to the conservative whole-value policy instead of
// triggering unbounded substitution.
//
// The returned value may contain raw credential material. Callers must use it
// only inside protected conversion or validation paths and must never include
// it in diagnostics, findings, logs, IDs, or public artifacts.
func MaterializeSourceVariables(value string, options Options) (string, []string) {
	if len(options.SourceVariables) == 0 || !strings.Contains(value, "{{") {
		return value, nil
	}
	used := make(map[string]struct{})
	materialized := credentialSourceVariablePattern.ReplaceAllStringFunc(value, func(token string) string {
		match := credentialSourceVariablePattern.FindStringSubmatch(token)
		if len(match) != 3 {
			return token
		}
		namespace := strings.TrimSpace(match[1])
		name := strings.TrimSpace(match[2])
		if namespace == "var" {
			if _, dynamic := options.ProvenVars[name]; dynamic {
				return token
			}
		}
		sourceName, sourceVariable, exists := sourceVariableByRuntimeName(
			options.SourceVariables,
			name,
		)
		if !exists {
			return token
		}
		used[sourceName] = struct{}{}
		return sourceVariable.Value
	})
	if len(used) == 0 {
		return value, nil
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	return materialized, names
}

func sourceVariableByRuntimeName(
	variables map[string]SourceVariable,
	name string,
) (string, SourceVariable, bool) {
	var (
		matchedName  string
		matchedValue SourceVariable
		matches      int
	)
	for candidate, sourceVariable := range variables {
		if !strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			continue
		}
		matchedName = candidate
		matchedValue = sourceVariable
		matches++
	}
	if matches != 1 {
		// The runtime resolves named parameters case-insensitively. Refuse to
		// choose among case-colliding source definitions because doing so could
		// protect one value while leaving another credential value public.
		return "", SourceVariable{}, false
	}
	return matchedName, matchedValue, true
}

// ProjectHeaders projects every sensitive header occurrence independently,
// preserving order, duplicates, and casing.
func ProjectHeaders(input []HeaderOccurrence, options Options) ([]HeaderOccurrence, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, Result{}, err
	}
	if len(input) > options.Limits.MaxValues {
		if options.Mode == Preserve {
			return append([]HeaderOccurrence(nil), input...),
				preserved(options.Source, "import_credential_review_incomplete"), nil
		}
		return nil, rejected(options.Source, "credential_header_count_limit_exceeded"), nil
	}
	totalBytes := 0
	for _, header := range input {
		size := len(header.Name) + len(header.Value)
		if size > options.Limits.MaxBytes-totalBytes {
			if options.Mode == Preserve {
				return append([]HeaderOccurrence(nil), input...),
					preserved(options.Source, "import_credential_review_incomplete"), nil
			}
			return nil, rejected(options.Source, "credential_header_size_limit_exceeded"), nil
		}
		totalBytes += size
	}
	out := append([]HeaderOccurrence(nil), input...)
	result := exactResult()
	for index := range out {
		sensitiveName := out[index].Name
		if !credential.SensitiveHeader(out[index].Name) {
			governedName, sensitive := governedSensitiveName(out[index].Name, options)
			if !sensitive {
				if options.KnownCredentialMatcher != nil {
					if structuralMIMEHeader(out[index].Name) {
						out[index].Value = RedactKnownMIMEHeader(
							out[index].Name,
							out[index].Value,
							boundedKnownCredentialMatcher(options),
						)
					} else if knownCredentialScalarMatch(options, out[index].Value) {
						out[index].Value = credential.Redacted
					}
				}
				continue
			}
			sensitiveName = governedName
		}
		if _, governed := options.PreGovernedHeaderOccurrences[index]; governed {
			continue
		}
		if executableSchemeCredentialHeader(out[index].Name, out[index].Value, options.ProvenVars) {
			continue
		}
		source := chooseSource(out[index].Source, options.Source)
		projected, item, err := projectSensitiveScalar(
			out[index].Value,
			sensitiveName,
			source,
			TargetRef{RequestKey: options.RequestKey, Field: firstNonEmpty(options.TargetField, "header_list"), Index: index},
			index,
			options,
		)
		if err != nil {
			return nil, Result{}, err
		}
		out[index].Value = projected
		result.Merge(item)
	}
	return out, result, nil
}

// RedactKnownMIMEHeader applies disclosure-only matching to the value-bearing
// portions of MIME headers while retaining bounded grammar tokens. It is shared
// by ordered request-header projection and script disclosure so malformed MIME
// values cannot hide credentials behind a structural-header exemption.
func RedactKnownMIMEHeader(
	name string,
	value string,
	matcher func(string) bool,
) string {
	if matcher == nil {
		return value
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "content-type":
		return redactKnownContentTypeParameters(value, matcher)
	case "content-disposition":
		return RedactKnownMultipartFilename(value, matcher)
	case "content-transfer-encoding":
		if standardDisclosureTransferEncoding(value) ||
			privateDisclosureMIMEToken(value) {
			return value
		}
		if matcher(value) {
			return credential.Redacted
		}
		return value
	case "mime-version":
		if strings.TrimSpace(value) == "1.0" {
			return value
		}
		if matcher(value) {
			return credential.Redacted
		}
		return value
	default:
		return value
	}
}

func redactKnownContentTypeParameters(
	contentType string,
	matcher func(string) bool,
) string {
	mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		if matcher(contentType) {
			return credential.Redacted
		}
		return contentType
	}
	changed := false
	for name, value := range parameters {
		normalizedName := strings.ToLower(strings.TrimSpace(name))
		switch {
		case normalizedName == "boundary" &&
			strings.HasPrefix(strings.ToLower(mediaType), "multipart/"):
			if matcher(value) {
				parameters[name] = disclosureMultipartBoundaryReplacement(value)
				changed = true
			}
		case normalizedName == "charset" &&
			(registeredDisclosureCharset(value) ||
				privateDisclosureMIMEToken(value)):
			// Charset selects wire decoding. Resolve registered names through
			// x/text's IANA MIME index and permit syntactically valid x- private
			// extensions without maintaining a partial local registry.
			continue
		default:
			if matcher(value) {
				parameters[name] = credential.Redacted
				changed = true
			}
		}
	}
	if !changed {
		return contentType
	}
	return mime.FormatMediaType(mediaType, parameters)
}

func validDisclosureMIMEToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character <= 0x20 || character >= 0x7f ||
			strings.ContainsRune(`()<>@,;:\"/[]?=`, rune(character)) {
			return false
		}
	}
	return true
}

func standardDisclosureTransferEncoding(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "7bit", "8bit", "binary", "quoted-printable", "base64":
		return true
	default:
		return false
	}
}

func privateDisclosureMIMEToken(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) > 2 &&
		strings.EqualFold(value[:2], "x-") &&
		validDisclosureMIMEToken(value)
}

func registeredDisclosureCharset(value string) bool {
	value = strings.TrimSpace(value)
	if !validDisclosureMIMEToken(value) {
		return false
	}
	_, err := ianaindex.MIME.Encoding(value)
	return err == nil
}

// RedactKnownMultipartFilename masks credential-equal descriptive parameters
// without changing the multipart field name or disposition grammar. The
// form-data name parameter identifies the executable field and is therefore
// structural; filename, filename*, and extension parameters are disclosure
// values and must not become an escape hatch for a credential copied from
// another request surface.
func RedactKnownMultipartFilename(
	contentDisposition string,
	matcher func(string) bool,
) string {
	if matcher == nil {
		return contentDisposition
	}
	disposition, parameters, err := mime.ParseMediaType(
		strings.TrimSpace(contentDisposition),
	)
	if err != nil {
		// A malformed structural header cannot be rewritten field-by-field.
		// Fail closed only when the complete value is known to contain a
		// credential; otherwise preserve the authored disclosure spelling.
		if matcher(contentDisposition) {
			return credential.Redacted
		}
		return contentDisposition
	}
	changed := false
	for name, value := range parameters {
		if strings.EqualFold(strings.TrimSpace(name), "name") ||
			value == "" || !matcher(value) {
			continue
		}
		parameters[name] = credential.Redacted
		changed = true
	}
	if !changed {
		return contentDisposition
	}
	return mime.FormatMediaType(disposition, parameters)
}

// RedactKnownMultipartBoundary rewrites a credential-equal multipart boundary
// to the fixed disclosure boundary used by ProjectMultipartWire. It leaves all
// nonmultipart and unmatched Content-Type values unchanged.
func RedactKnownMultipartBoundary(
	contentType string,
	matcher func(string) bool,
) string {
	if matcher == nil {
		return contentType
	}
	mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		if matcher(contentType) {
			return credential.Redacted
		}
		return contentType
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return contentType
	}
	boundary := parameters["boundary"]
	if boundary == "" || !matcher(boundary) {
		return contentType
	}
	parameters["boundary"] = disclosureMultipartBoundaryReplacement(boundary)
	return mime.FormatMediaType(mediaType, parameters)
}

func structuralMIMEHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "content-type", "content-disposition", "content-transfer-encoding",
		"mime-version":
		return true
	default:
		return false
	}
}

// executableSchemeCredentialHeader reports whether a credential header is
// already executable end to end, so governing it would only break it.
//
// Authorization and Cookie carry protocol syntax around the credential:
// "Bearer <credential>", "Basic <credential>", "n=v; n=v". Because the value as
// a whole is not a single template, ClassifyWholeTemplate sees TemplateNone and
// projectSensitiveScalar governs the entire header — correctly, when the
// residue is authored credential material. But when every credential span is
// itself already executable (a response-derived {{var.*}} the importer proved,
// or an existing {{secret.*}}/{{env.*}} reference) the only static text left is
// the scheme keyword or the cookie names. Governing that produces
// "{{secret.NAME}}" in place of "Basic {{var.token}}", which drops the scheme
// AND replaces a working correlation with a binding the operator cannot
// satisfy — the credential never existed as a literal to migrate.
//
// The rule is deliberately narrow: one span of static residue that must be a
// recognized auth scheme, and every credential span executable. Anything else,
// including "Bearer sk-live-... {{secret.X}}", still governs the whole value.
func executableSchemeCredentialHeader(name, value string, provenVars map[string]struct{}) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	switch credential.NormalizeName(name) {
	case "authorization", "proxyauthorization":
		fields := strings.Fields(value)
		if len(fields) != 2 {
			return false
		}
		switch strings.ToLower(fields[0]) {
		case "bearer", "basic", "token", "digest":
			return executableCredentialSpan(fields[1], provenVars)
		default:
			return false
		}
	case "cookie", "setcookie":
		parts := strings.Split(value, ";")
		for _, part := range parts {
			pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" ||
				!executableCredentialSpan(pair[1], provenVars) {
				return false
			}
		}
		return len(parts) > 0
	default:
		return false
	}
}

// executableCredentialSpan reports whether one credential span resolves at
// runtime without a new binding.
func executableCredentialSpan(span string, provenVars map[string]struct{}) bool {
	kind, _ := ClassifyWholeTemplate(strings.TrimSpace(span), provenVars)
	switch kind {
	case TemplateSecret, TemplateEnv, TemplateDynamic:
		return true
	default:
		return false
	}
}

// ProjectAuth projects supported typed authentication fields without
// conflating username/domain identifiers with credential values.
func ProjectAuth(input AuthFields, options Options) (AuthFields, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return AuthFields{}, Result{}, err
	}
	authBytes := len(input.Type) + len(input.Username) + len(input.Password) +
		len(input.Token) + len(input.Domain)
	if authBytes > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return input, preserved(options.Source, "import_credential_review_incomplete"), nil
		}
		return AuthFields{}, rejected(options.Source, "credential_auth_size_limit_exceeded"), nil
	}
	out := input
	result := exactResult()
	source := chooseSource(input.Source, options.Source)
	fields := []struct {
		name   string
		target string
		value  *string
	}{
		{name: "password", target: "auth.password", value: &out.Password},
		{name: "token", target: "auth.token", value: &out.Token},
	}
	for occurrence, field := range fields {
		if *field.value == "" {
			continue
		}
		projected, item, err := projectSensitiveScalar(
			*field.value,
			field.name,
			source,
			TargetRef{RequestKey: options.RequestKey, Field: field.target},
			occurrence,
			options,
		)
		if err != nil {
			return AuthFields{}, Result{}, err
		}
		*field.value = projected
		result.Merge(item)
	}
	return out, result, nil
}

// ProjectRawURL preserves authored query ordering and raw escaping. Preserve
// mode retains valid, bounded URL bytes and reports credential-bearing values
// for review without requiring re-entry. Other modes govern sensitive query
// values as complete values and block userinfo or credential-like fragments
// when runtime URL parsing could alter their encoding or semantics.
func ProjectRawURL(raw string, options Options) (string, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return "", Result{}, err
	}
	if len(raw) > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return raw, preserved(options.Source, "import_credential_review_incomplete"), nil
		}
		return "", rejected(options.Source, "credential_url_limit_exceeded"), nil
	}
	if !utf8.ValidString(raw) {
		// Go strings can hold invalid UTF-8, but JSON persistence cannot round
		// trip those bytes. Returning this as an exact preserved URL would
		// silently serialize replacement runes and change wire semantics.
		return "", rejected(options.Source, "credential_url_invalid_utf8"), nil
	}
	if options.Mode == Preserve {
		options.preserveSourceExpression = true
		if !ValidRawURL(raw, options) {
			result := blocked(options.Source, "credential_url_malformed")
			if strings.Contains(raw, "@") ||
				containsSensitiveNameHintWithOptions([]byte(raw), options) {
				result.Merge(preserved(options.Source, "import_credential_preserved"))
			}
			return raw, result, nil
		}
		// Continue through the offset-preserving parser for valid URLs. In
		// Preserve mode projectSensitiveScalar leaves every byte untouched but
		// records the exact component locator required by runtime redaction.
	}
	result := exactResult()
	projected := raw

	schemeEnd := strings.Index(projected, "://")
	authorityStart := -1
	if schemeEnd >= 0 {
		authorityStart = schemeEnd + 3
	} else if strings.HasPrefix(projected, "//") {
		// RFC 3986 network-path references carry an authority without a scheme.
		// Keep raw byte offsets so userinfo projection does not normalize or
		// reorder the remainder of the URL.
		authorityStart = 2
	}
	if authorityStart >= 0 {
		authorityEnd := len(projected)
		for _, marker := range []string{"/", "?", "#"} {
			if relative := strings.Index(projected[authorityStart:], marker); relative >= 0 && authorityStart+relative < authorityEnd {
				authorityEnd = authorityStart + relative
			}
		}
		if at := strings.LastIndex(projected[authorityStart:authorityEnd], "@"); at >= 0 {
			start := authorityStart
			end := authorityStart + at
			sourceValue := projected[start:end]
			replacement, item, err := projectSensitiveScalar(
				sourceValue,
				"url_userinfo",
				options.Source,
				TargetRef{RequestKey: options.RequestKey, Field: urlComponentTarget(options, "userinfo")},
				0,
				options,
			)
			if err != nil {
				return "", Result{}, err
			}
			projected = projected[:start] + replacement + projected[end:]
			result.Merge(item)
			if options.Mode != Preserve {
				result.Merge(blocked(options.Source, "credential_url_userinfo_review_required"))
			}
		}
	}

	queryStart := strings.IndexByte(projected, '?')
	fragmentStart := strings.IndexByte(projected, '#')
	if queryStart >= 0 && (fragmentStart < 0 || queryStart < fragmentStart) {
		end := len(projected)
		if fragmentStart >= 0 {
			end = fragmentStart
		}
		query, item, err := projectEncodedPairs(
			projected[queryStart+1:end],
			options,
			urlComponentTarget(options, "query"),
		)
		if err != nil {
			return "", Result{}, err
		}
		projected = projected[:queryStart+1] + query + projected[end:]
		result.Merge(item)
	}
	if fragment := strings.IndexByte(projected, '#'); fragment >= 0 && fragment+1 < len(projected) {
		rawFragment := projected[fragment+1:]
		decoded, decodeErr := url.QueryUnescape(rawFragment)
		if decodeErr != nil {
			decoded = rawFragment
		}
		if credential.SensitiveName(decoded) || strings.Contains(decoded, "=") {
			replacement, item, err := projectSensitiveScalar(
				rawFragment,
				"url_fragment",
				options.Source,
				TargetRef{RequestKey: options.RequestKey, Field: urlComponentTarget(options, "fragment")},
				0,
				options,
			)
			if err != nil {
				return "", Result{}, err
			}
			projected = projected[:fragment+1] + replacement
			result.Merge(item)
			if options.Mode != Preserve {
				result.Merge(blocked(options.Source, "credential_url_fragment_review_required"))
			}
		}
	}
	return projected, result, nil
}

func urlComponentTarget(options Options, component string) string {
	return appendTargetField(options.TargetField, "url", component)
}

// appendTargetField composes an executable-field locator without discarding
// an enclosing structured-body path. Multipart bodies can nest arbitrarily
// (within the configured depth bound), so every child projector must retain
// the complete outer part chain for runtime redaction.
func appendTargetField(existing, fallback string, segments ...string) string {
	prefix := strings.Trim(strings.TrimSpace(existing), ".")
	if prefix == "" {
		prefix = strings.Trim(strings.TrimSpace(fallback), ".")
	}
	for _, segment := range segments {
		segment = strings.Trim(strings.TrimSpace(segment), ".")
		if segment == "" {
			continue
		}
		if prefix == "" {
			prefix = segment
		} else {
			prefix += "." + segment
		}
	}
	return prefix
}

// ParseFormURLEncoded parses an encoded form without collapsing order,
// duplicates, raw escaping, blank values, or missing equals signs.
func ParseFormURLEncoded(raw string, options Options) ([]FormField, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	if len(raw) > options.Limits.MaxBytes {
		return nil, errors.New("form payload exceeds credential projection limit")
	}
	if strings.Count(raw, "&")+1 > options.Limits.MaxValues {
		return nil, errors.New("form field count exceeds credential projection limit")
	}
	segments := strings.Split(raw, "&")
	fields := make([]FormField, 0, len(segments))
	for occurrence, segment := range segments {
		if segment == "" && len(segments) == 1 {
			continue
		}
		rawName, rawValue, hadEquals := strings.Cut(segment, "=")
		name, err := url.QueryUnescape(rawName)
		if err != nil {
			return nil, errors.New("form field name has invalid escaping")
		}
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			return nil, errors.New("form field value has invalid escaping")
		}
		fields = append(fields, FormField{
			Name:       name,
			Value:      value,
			RawName:    rawName,
			RawValue:   rawValue,
			HadEquals:  hadEquals,
			Occurrence: occurrence,
			Source:     options.Source,
		})
	}
	return fields, nil
}

// ProjectFormURLEncoded projects an encoded form while retaining exact raw
// bytes for every nonsensitive field.
func ProjectFormURLEncoded(raw string, options Options) (string, []FormField, Result, error) {
	targetField := strings.Trim(strings.TrimSpace(options.TargetField), ".")
	if targetField == "" {
		targetField = "form"
	} else {
		targetField = appendTargetField(targetField, "", "form")
	}
	return projectFormURLEncoded(raw, options, targetField)
}

func projectFormURLEncoded(raw string, options Options, targetField string) (string, []FormField, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return "", nil, Result{}, err
	}
	if options.Mode == Preserve {
		if len(raw) > options.Limits.MaxBytes ||
			strings.Count(raw, "&")+1 > options.Limits.MaxValues {
			opaqueOptions := options
			opaqueOptions.TargetField = targetField
			return raw, nil,
				preservedOpaquePayloadResult(
					[]byte(raw),
					opaqueOptions,
					"import_credential_review_incomplete",
				), nil
		}
	}
	if options.Mode == Redact && len(raw) > options.Limits.MaxBytes {
		return "", nil, rejected(options.Source, "credential_form_limit_exceeded"), nil
	}
	if !reserveStructuredRedactionBytes(options, len(raw)) {
		return "", nil, rejected(options.Source, "credential_structured_work_limit_exceeded"), nil
	}
	formValues := 0
	if raw != "" {
		formValues = strings.Count(raw, "&") + 1
	}
	if !reserveStructuredRedactionValues(options, formValues) {
		return "", nil, rejected(options.Source, "credential_structured_value_limit_exceeded"), nil
	}
	fields, err := ParseFormURLEncoded(raw, options)
	if err != nil {
		code := "credential_form_malformed"
		if options.Mode == Preserve {
			opaqueOptions := options
			opaqueOptions.TargetField = targetField
			return raw, nil, preservedOpaquePayloadResult(
				[]byte(raw),
				opaqueOptions,
				code,
			), nil
		}
		if containsSensitiveNameHintWithOptions([]byte(raw), options) {
			return "", nil, rejected(options.Source, code), nil
		}
		if options.KnownCredentialMatcher != nil {
			return "", nil,
				rejected(options.Source, "credential_form_known_value_malformed"),
				nil
		}
		return raw, nil, blocked(options.Source, code), nil
	}
	result := exactResult()
	segments := make([]string, 0, len(fields))
	outputBytes := 0
	for index := range fields {
		field := &fields[index]
		rawValue := field.RawValue
		sensitiveName, sensitive := governedSensitiveName(field.Name, options)
		queryField := options.Mode == Redact && !sensitive && strings.EqualFold(strings.TrimSpace(field.Name), "query") && strings.TrimSpace(field.Value) != credential.Redacted && !wholeStructuredRuntimeTemplate(field.Value)
		nested := options.Mode == Redact && !sensitive && (nestedJSONObjectOrArray(field.Value) || queryField)
		if !sensitive && !nested {
			sensitiveName, sensitive = composedCredentialName(field.Value, options)
		}
		if nested {
			nestedOptions := options
			nestedOptions.nestedJSON = true
			nestedOptions.TargetField = targetField
			var projected []byte
			var item Result
			var nestedErr error
			if queryField {
				queryJSON, work, classifyErr := InspectQueryValueStructure(field.Value, options.structuredRedactionBudget.remainingBytes)
				if !reserveStructuredRedactionBytes(options, work) || classifyErr != nil {
					return "", nil, rejected(options.Source, "credential_structured_work_limit_exceeded"), nil
				}
				if queryJSON {
					projected, item, nestedErr = ProjectRequestBody([]byte(field.Value), "application/json", nestedOptions)
				} else if knownCredentialScalarMatch(options, field.Value) {
					projected, item = []byte(credential.Redacted), exactResult()
				} else if !queryHasExecutableStructure(strings.TrimSpace(field.Value)) {
					projected, item = []byte(field.Value), exactResult()
				} else {
					var graphQL GraphQL
					graphQL, item, nestedErr = ProjectGraphQL(GraphQL{Query: []byte(field.Value)}, nestedOptions)
					projected = graphQL.Query
				}
			} else {
				projected, item, nestedErr = ProjectRequestBody([]byte(field.Value), "application/json", nestedOptions)
			}
			if nestedErr != nil {
				return "", nil, Result{}, nestedErr
			}
			if item.Disposition == Rejected {
				return "", nil, item, nil
			}
			if projected == nil {
				return "", nil, rejected(options.Source, "credential_nested_json_rejected"), nil
			}
			result.Merge(item)
			if string(projected) != field.Value {
				if !reserveFormEncodedRedaction(options, projected) {
					return "", nil, rejected(options.Source, "credential_structured_output_limit_exceeded"), nil
				}
				field.Value = string(projected)
				rawValue = url.QueryEscape(field.Value)
				field.RawValue = rawValue
			}
		} else if sensitive {
			projected, item, err := projectSensitiveScalar(
				field.RawValue,
				sensitiveName,
				chooseSource(field.Source, options.Source),
				TargetRef{RequestKey: options.RequestKey, Field: targetField, Index: index},
				field.Occurrence,
				options,
			)
			if err != nil {
				return "", nil, Result{}, err
			}
			field.Value = projected
			// Runtime templates must remain visible to the DSL interpolator.
			rawValue = projected
			field.RawValue = rawValue
			result.Merge(item)
		} else if knownCredentialScalarMatch(options, field.Value) {
			field.Value = credential.Redacted
			rawValue = credential.Redacted
			field.RawValue = rawValue
		}
		segment := field.RawName
		if field.HadEquals {
			segment += "=" + rawValue
		}
		separatorBytes := 0
		if len(segments) > 0 {
			separatorBytes = 1
		}
		if options.Mode == Redact && len(segment)+separatorBytes > options.Limits.MaxBytes-outputBytes {
			return "", nil, rejected(options.Source, "credential_structured_output_limit_exceeded"), nil
		}
		outputBytes += len(segment) + separatorBytes
		segments = append(segments, segment)
	}
	return strings.Join(segments, "&"), fields, result, nil
}

// composedCredentialName recognizes assignment-like credential material nested
// inside an otherwise nonsensitive pair value, for example
// config=password%3D... or a semicolon-delimited legacy query. It deliberately
// requires a credential-name token followed by ':' or '=' so ordinary paths
// such as /password/reset do not become secrets merely because they contain a
// vocabulary word.
func composedCredentialName(value string, options Options) (string, bool) {
	value = decodeCredentialScanEscapes(value)
	for index := 0; index < len(value); {
		if !composedCredentialNameByte(value[index]) {
			index++
			continue
		}
		start := index
		for index < len(value) && composedCredentialNameByte(value[index]) {
			index++
		}
		name, sensitive := governedSensitiveName(value[start:index], options)
		if !sensitive {
			continue
		}
		cursor := index
		for cursor < len(value) {
			switch value[cursor] {
			case ' ', '\t', '\r', '\n', '"', '\'':
				cursor++
			default:
				goto delimiter
			}
		}
	delimiter:
		if cursor < len(value) && (value[cursor] == '=' || value[cursor] == ':') &&
			!literalNonCredentialAt(value, cursor+1) {
			return name, true
		}
	}
	return "", false
}

// literalNonCredentialAt reports whether the value after a name's delimiter is
// a JSON true, false or null, which holds no credential: a feature flag such
// as "SingleUseRefreshToken":false names a credential without carrying one.
func literalNonCredentialAt(value string, start int) bool {
	for start < len(value) && (value[start] == ' ' || value[start] == '\t') {
		start++
	}
	for _, literal := range []string{"true", "false", "null"} {
		if strings.HasPrefix(value[start:], literal) {
			end := start + len(literal)
			return end == len(value) || !composedCredentialNameByte(value[end])
		}
	}
	return false
}

func composedCredentialNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_' || value == '-' || value == '.' ||
		value == '[' || value == ']'
}

func projectEncodedPairs(raw string, options Options, target string) (string, Result, error) {
	projected, _, result, err := projectFormURLEncoded(raw, options, target)
	return projected, result, err
}

// ProjectMultipart projects structured multipart parts. It preserves boundary,
// part order, duplicate names, header order, and all nonsensitive bytes.
func ProjectMultipart(input Multipart, options Options) (Multipart, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return Multipart{}, Result{}, err
	}
	if len(input.Parts) > options.Limits.MaxParts {
		if options.Mode == Preserve {
			result := blocked(options.Source, "import_credential_review_incomplete")
			result.Merge(preservedOpaqueTargetResult(options))
			return cloneMultipart(input), result, nil
		}
		return Multipart{}, rejected(options.Source, "credential_multipart_part_limit_exceeded"), nil
	}
	if strings.TrimSpace(input.Boundary) == "" {
		return Multipart{}, rejected(options.Source, "credential_multipart_boundary_missing"), nil
	}
	if !validMultipartBoundary(input.Boundary) {
		return Multipart{}, rejected(options.Source, "credential_multipart_boundary_invalid"), nil
	}
	totalBytes := len(input.Boundary)
	totalValues := len(input.Parts)
	for _, part := range input.Parts {
		totalValues += len(part.Headers)
		if totalValues > options.Limits.MaxValues {
			if options.Mode == Preserve {
				result := blocked(options.Source, "import_credential_review_incomplete")
				result.Merge(preservedOpaqueTargetResult(options))
				return cloneMultipart(input), result, nil
			}
			return Multipart{}, rejected(options.Source, "credential_multipart_value_limit_exceeded"), nil
		}
		size := len(part.Body)
		hasContentDisposition := false
		hasContentType := false
		for _, header := range part.Headers {
			size += len(header.Name) + len(header.Value)
			hasContentDisposition = hasContentDisposition ||
				strings.EqualFold(strings.TrimSpace(header.Name), "Content-Disposition")
			hasContentType = hasContentType ||
				strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type")
		}
		// Structured adapters may provide parsed convenience fields without
		// their serialized MIME headers. Charge those fields only when they are
		// not already represented in the ordered header bytes.
		if !hasContentDisposition {
			size += len(part.Name) + len(part.FileName)
		}
		if !hasContentType {
			size += len(part.ContentType)
		}
		if size > options.Limits.MaxBytes-totalBytes {
			if options.Mode == Preserve {
				result := blocked(options.Source, "import_credential_review_incomplete")
				result.Merge(preservedOpaqueTargetResult(options))
				return cloneMultipart(input), result, nil
			}
			return Multipart{}, rejected(options.Source, "credential_multipart_size_limit_exceeded"), nil
		}
		totalBytes += size
	}
	projectedBoundary := input.Boundary
	if knownCredentialScalarMatch(options, input.Boundary) {
		projectedBoundary = disclosureMultipartBoundaryReplacement(input.Boundary)
	}
	out := Multipart{Boundary: projectedBoundary, Parts: make([]MultipartPart, len(input.Parts))}
	result := exactResult()
	for index := range input.Parts {
		part := input.Parts[index]
		out.Parts[index] = part
		out.Parts[index].Headers = append([]HeaderOccurrence(nil), part.Headers...)
		out.Parts[index].Body = append([]byte(nil), part.Body...)
		if options.KnownCredentialMatcher != nil {
			if knownCredentialScalarMatch(options, out.Parts[index].FileName) {
				out.Parts[index].FileName = credential.Redacted
			}
			out.Parts[index].ContentType = RedactKnownMIMEHeader(
				"Content-Type",
				out.Parts[index].ContentType,
				boundedKnownCredentialMatcher(options),
			)
		}
		headerOptions := options
		headerOptions.Source = chooseSource(part.Source, options.Source)
		headerOptions.TargetField = appendTargetField(
			options.TargetField,
			"",
			fmt.Sprintf("multipart.parts[%d].headers", index),
		)
		// Request-header occurrence indexes have no relationship to indexes
		// inside an individual MIME part. Carrying this exemption into part
		// headers would let a pre-governed top-level header suppress projection
		// of an unrelated literal Authorization header in the body.
		headerOptions.PreGovernedHeaderOccurrences = nil
		headers, headerResult, err := ProjectHeaders(part.Headers, headerOptions)
		if err != nil {
			return Multipart{}, Result{}, err
		}
		out.Parts[index].Headers = headers
		result.Merge(headerResult)
		if multipartPartHasDynamicHeader(out.Parts[index]) {
			// Multipart is serialized into opaque body bytes before runtime
			// interpolation. Unlike top-level HTTP headers, those MIME fields
			// have no post-binding wire validator, so a bound CR/LF could inject
			// another part header. Keep the valid draft but require replacement
			// of that behavior before execution.
			result.Merge(blocked(
				chooseSource(part.Source, options.Source),
				"multipart_dynamic_header_unvalidated",
			))
		}
		sensitiveName, sensitive := governedSensitiveName(part.Name, options)
		if !sensitive {
			nested, nestedResult, err := projectMultipartPartBody(part, index, options)
			if err != nil {
				return Multipart{}, Result{}, err
			}
			out.Parts[index].Body = nested
			result.Merge(nestedResult)
			continue
		}
		source := chooseSource(part.Source, options.Source)
		if options.Mode != Preserve && part.FileName != "" {
			result.Merge(rejected(source, "credential_multipart_sensitive_file_unsupported"))
			continue
		}
		if options.Mode != Preserve && !utf8.Valid(part.Body) {
			result.Merge(rejected(source, "credential_multipart_sensitive_binary_unsupported"))
			continue
		}
		projected, item, err := projectSensitiveScalar(
			string(part.Body),
			sensitiveName,
			source,
			TargetRef{
				RequestKey: options.RequestKey,
				Field: appendTargetField(
					options.TargetField,
					"",
					fmt.Sprintf("multipart.parts[%d].body", index),
				),
				Index: index,
			},
			index,
			options,
		)
		if err != nil {
			return Multipart{}, Result{}, err
		}
		out.Parts[index].Body = []byte(projected)
		result.Merge(item)
	}
	if result.Disposition == Rejected {
		return Multipart{}, result, nil
	}
	return out, result, nil
}

func cloneMultipart(input Multipart) Multipart {
	out := Multipart{
		Boundary: input.Boundary,
		Parts:    make([]MultipartPart, len(input.Parts)),
	}
	for index, part := range input.Parts {
		out.Parts[index] = part
		out.Parts[index].Headers = append([]HeaderOccurrence(nil), part.Headers...)
		out.Parts[index].Body = append([]byte(nil), part.Body...)
	}
	return out
}

func multipartPartHasDynamicHeader(part MultipartPart) bool {
	if strings.Contains(part.Name, "{{") ||
		strings.Contains(part.FileName, "{{") ||
		strings.Contains(part.ContentType, "{{") {
		return true
	}
	for _, header := range part.Headers {
		if strings.Contains(header.Name, "{{") ||
			strings.Contains(header.Value, "{{") {
			return true
		}
	}
	return false
}

// projectMultipartPartBody applies the same structured credential policy to a
// nonsensitive multipart field's payload. A neutral wrapper name such as
// "payload" must not exempt credentials nested in JSON, XML, form, or GraphQL
// content. Nonsensitive bytes remain unchanged because every nested projector
// uses byte-offset rewriting.
func projectMultipartPartBody(
	part MultipartPart,
	index int,
	options Options,
) ([]byte, Result, error) {
	body := append([]byte(nil), part.Body...)
	contentType := multipartPartContentType(part.Headers)
	if contentType == "" {
		contentType = strings.TrimSpace(part.ContentType)
	}
	mediaType := ""
	if contentType != "" {
		parsed, _, err := mime.ParseMediaType(contentType)
		if err == nil {
			mediaType = strings.ToLower(strings.TrimSpace(parsed))
		} else {
			mediaType = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
		}
	}
	kind := structuredMultipartPartKind(mediaType, body)
	nestedOptions := options
	nestedOptions.Source = chooseSource(part.Source, options.Source)
	nestedOptions.Source.Pointer = appendSourcePointer(
		nestedOptions.Source.Pointer,
		"/multipart/parts/"+strconv.Itoa(index)+"/body",
	)
	nestedOptions.TargetField = appendTargetField(
		options.TargetField,
		"",
		fmt.Sprintf("multipart.parts[%d].body", index),
	)
	_, _, inferredMultipart := inferMultipartWireType(body)
	if kind == "multipart" || inferredMultipart {
		nestedOptions.multipartDepth++
	}
	return ProjectRequestBody(body, contentType, nestedOptions)
}

func multipartPartContentType(headers []HeaderOccurrence) string {
	value := ""
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
			value = strings.TrimSpace(header.Value)
		}
	}
	return value
}

func structuredMultipartPartKind(mediaType string, body []byte) string {
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		return "multipart"
	case mediaType == "application/json", strings.HasSuffix(mediaType, "+json"):
		return "json"
	case mediaType == "application/xml", mediaType == "text/xml", strings.HasSuffix(mediaType, "+xml"):
		return "xml"
	case mediaType == "application/x-www-form-urlencoded":
		return "form"
	case mediaType == "application/graphql":
		return "graphql"
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	switch trimmed[0] {
	case '{', '[':
		return "json"
	case '<':
		return "xml"
	default:
		return ""
	}
}

func appendSourcePointer(pointer, suffix string) string {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" {
		return suffix
	}
	return strings.TrimRight(pointer, "/") + suffix
}

// ProjectGraphQL preserves the query and projects only the variables object.
// Credential-looking inline literals are blocked because rewriting GraphQL
// source safely requires source-specific AST and schema evidence.
func ProjectGraphQL(input GraphQL, options Options) (GraphQL, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return GraphQL{}, Result{}, err
	}
	out := GraphQL{
		Query:     append([]byte(nil), input.Query...),
		Variables: append([]byte(nil), input.Variables...),
	}
	result := exactResult()
	if len(input.Query)+len(input.Variables) > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return out, preservedOpaquePayloadResult(
				append(append([]byte(nil), input.Query...), input.Variables...),
				options,
				"import_credential_review_incomplete",
			), nil
		}
		return GraphQL{}, rejected(options.Source, "credential_graphql_limit_exceeded"), nil
	}
	if options.Mode == Redact && !wholeStructuredRuntimeTemplate(string(input.Query)) {
		jsonLike, work, inspectErr := InspectQueryValueStructure(string(input.Query), options.structuredRedactionBudget.remainingBytes)
		if !reserveStructuredRedactionBytes(options, work) || inspectErr != nil {
			return GraphQL{}, rejected(options.Source, "credential_structured_work_limit_exceeded"), nil
		}
		if jsonLike {
			return GraphQL{}, rejected(options.Source, "credential_graphql_malformed_sensitive"), nil
		}
	}
	containsLiteral, scanCode := scanGraphQLCredentialLiteral(input.Query, options)
	if scanCode != "" {
		if options.Mode == Preserve && strings.HasSuffix(scanCode, "_limit_exceeded") {
			result.Merge(preservedOpaquePayloadResult(
				input.Query,
				options,
				"import_credential_review_incomplete",
			))
			return out, result, nil
		}
		if options.Mode == Preserve && scanCode == "credential_graphql_malformed_sensitive" {
			result.Merge(preservedOpaquePayloadResult(input.Query, options, scanCode))
			return out, result, nil
		}
		return GraphQL{}, rejected(options.Source, scanCode), nil
	}
	if containsLiteral {
		if options.Mode == Preserve {
			item := preserved(options.Source, "import_credential_preserved")
			targetField := strings.TrimSuffix(strings.TrimSpace(options.TargetField), ".")
			if targetField == "" {
				targetField = "graphql"
			}
			item.PreservedTargets = append(item.PreservedTargets, TargetRef{
				RequestKey: options.RequestKey,
				Field:      targetField + ".query",
			})
			result.Merge(item)
		} else {
			return GraphQL{}, rejected(options.Source, "credential_graphql_inline_literal_unsupported"), nil
		}
	}
	if options.KnownCredentialMatcher != nil {
		projectedQuery, knownResult, err := projectKnownGraphQLScalars(
			out.Query,
			options,
		)
		if err != nil {
			return GraphQL{}, Result{}, err
		}
		result.Merge(knownResult)
		if knownResult.Disposition == Rejected {
			return GraphQL{}, result, nil
		}
		out.Query = projectedQuery
	}
	if len(bytes.TrimSpace(input.Variables)) == 0 {
		return out, result, nil
	}
	variableOptions := options
	targetField := strings.TrimSuffix(strings.TrimSpace(options.TargetField), ".")
	if targetField == "" {
		targetField = "graphql"
	}
	variableOptions.TargetField = targetField + ".variables"
	variables, variableResult, err := ProjectJSON(input.Variables, variableOptions)
	if err != nil {
		return GraphQL{}, Result{}, err
	}
	out.Variables = variables
	result.Merge(variableResult)
	if options.Mode == Preserve &&
		containsSensitiveNameHintWithOptions(input.Query, options) &&
		len(result.Findings) == 0 {
		result.Merge(preserved(options.Source, "import_credential_preserved"))
	}
	return out, result, nil
}

func chooseSource(primary, fallback SourceRef) SourceRef {
	if primary.Format != "" || primary.Path != "" || primary.Pointer != "" || primary.ItemID != "" {
		return primary
	}
	return fallback
}

// containsSensitiveNameHint is the fail-closed fallback used only after a
// declared structured payload has failed parsing. It deliberately delegates
// classification to pkg/credential instead of maintaining a narrower shadow
// vocabulary here. Rejecting an ambiguous malformed draft is preferable to
// returning its original bytes when any credential-bearing name is visible.
func containsSensitiveNameHint(payload []byte) bool {
	scanText := decodeCredentialScanEscapes(string(payload))
	for _, candidate := range strings.FieldsFunc(scanText, func(value rune) bool {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return false
		}
		switch value {
		case '_', '-', '.', '[', ']', '%', ':':
			return false
		default:
			return true
		}
	}) {
		if credential.SensitiveName(candidate) || credential.SensitiveXMLName(candidate) {
			return true
		}
	}
	return false
}

func containsSensitiveNameHintWithOptions(payload []byte, options Options) bool {
	if containsSensitiveNameHint(payload) {
		return true
	}
	if len(options.SensitiveNames) == 0 {
		return false
	}
	scanText := strings.ToLower(decodeCredentialScanEscapes(string(payload)))
	for name := range options.SensitiveNames {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" && strings.Contains(scanText, name) {
			return true
		}
	}
	return false
}

func decodeCredentialScanEscapes(value string) string {
	if !strings.Contains(value, `\u`) {
		return value
	}
	var decoded strings.Builder
	decoded.Grow(len(value))
	for index := 0; index < len(value); {
		if index+6 <= len(value) && value[index] == '\\' && value[index+1] == 'u' {
			codepoint, err := strconv.ParseUint(value[index+2:index+6], 16, 16)
			if err == nil {
				decoded.WriteRune(rune(codepoint))
				index += 6
				continue
			}
		}
		decoded.WriteByte(value[index])
		index++
	}
	return decoded.String()
}

type replacement struct {
	start int
	end   int
	value []byte
}

func applyReplacements(payload []byte, replacements []replacement) []byte {
	if len(replacements) == 0 {
		return append([]byte(nil), payload...)
	}
	sort.SliceStable(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	var output bytes.Buffer
	cursor := 0
	for _, item := range replacements {
		output.Write(payload[cursor:item.start])
		output.Write(item.value)
		cursor = item.end
	}
	output.Write(payload[cursor:])
	return output.Bytes()
}

// ProjectXML rewrites only simple sensitive element text and attribute values.
// Surrounding bytes, namespace prefixes, quoting, and nonsensitive content are
// retained byte-for-byte.
func ProjectXML(payload []byte, options Options) ([]byte, Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, Result{}, err
	}
	if len(payload) > options.Limits.MaxBytes {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"import_credential_review_incomplete",
				), nil
		}
		return nil, rejected(options.Source, "credential_xml_limit_exceeded"), nil
	}
	if !utf8.Valid(payload) {
		if options.Mode == Preserve {
			return append([]byte(nil), payload...),
				preservedOpaquePayloadResult(
					payload,
					options,
					"credential_xml_invalid_utf8",
				), nil
		}
		return nil, rejected(options.Source, "credential_xml_invalid_utf8"), nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	seenRoot := false
	rootClosed := false
	depth := 0
	for {
		token, decodeErr := decoder.Token()
		if decodeErr == nil {
			switch typed := token.(type) {
			case xml.StartElement:
				if depth == 0 {
					if seenRoot || rootClosed {
						return malformedXMLProjection(payload, options)
					}
					seenRoot = true
				}
				depth++
			case xml.EndElement:
				if depth > 0 {
					depth--
				}
				if seenRoot && depth == 0 {
					rootClosed = true
				}
			case xml.CharData:
				if depth == 0 && len(bytes.TrimSpace(typed)) > 0 {
					return malformedXMLProjection(payload, options)
				}
			}
			continue
		}
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		return malformedXMLProjection(payload, options)
	}
	if !seenRoot || depth != 0 || !rootClosed {
		return malformedXMLProjection(payload, options)
	}
	projected, result, scanErr := scanXML(payload, options)
	if scanErr != nil {
		return nil, Result{}, scanErr
	}
	return projected, result, nil
}

func malformedXMLProjection(payload []byte, options Options) ([]byte, Result, error) {
	if options.Mode == Preserve {
		result := preservedOpaquePayloadResult(
			payload,
			options,
			"credential_xml_malformed",
		)
		return append([]byte(nil), payload...), result, nil
	}
	if containsSensitiveNameHintWithOptions(payload, options) {
		return nil, rejected(options.Source, "credential_xml_malformed"), nil
	}
	if options.KnownCredentialMatcher != nil {
		return nil, rejected(options.Source, "credential_xml_known_value_malformed"), nil
	}
	return append([]byte(nil), payload...), blocked(options.Source, "credential_xml_malformed"), nil
}

type xmlFrame struct {
	name       string
	path       string
	sensitive  bool
	suppressed bool
	hasChild   bool
	contentAt  int
	textStart  int
	textEnd    int
	hasContent bool
	source     SourceRef
	// knownTextStart indexes the disclosure-projected XML string-value buffer
	// at this element's start. The shared buffer contains descendant character
	// data exactly once, avoiding depth × payload memory while still detecting
	// a credential split across text, CDATA, and neutral child elements.
	knownTextStart int
}

func scanXML(payload []byte, options Options) ([]byte, Result, error) {
	result := exactResult()
	var replacements []replacement
	var stack []xmlFrame
	var knownLogicalText []byte
	values := 0
	targetOccurrences := make(map[string]int)
	nextTargetOccurrence := func(field, pointer string) int {
		key := field + "\x00" + pointer
		occurrence := targetOccurrences[key]
		targetOccurrences[key] = occurrence + 1
		return occurrence
	}
	for cursor := 0; cursor < len(payload); {
		openRelative := bytes.IndexByte(payload[cursor:], '<')
		if openRelative < 0 {
			logicalText := html.UnescapeString(string(payload[cursor:]))
			projectedLogicalText := logicalText
			logicalTextMatched := false
			if cursor < len(payload) &&
				!xmlProjectionStackSuppressed(stack) {
				logicalTextMatched = knownCredentialScalarMatch(
					options,
					logicalText,
				)
				if logicalTextMatched {
					projectedLogicalText = credential.Redacted
				}
				knownLogicalText = append(
					knownLogicalText,
					projectedLogicalText...,
				)
			}
			if len(stack) > 0 && cursor < len(payload) {
				frame := &stack[len(stack)-1]
				if len(bytes.TrimSpace(payload[cursor:])) > 0 {
					frame.textStart = cursor
					frame.textEnd = len(payload)
					frame.hasContent = true
				}
			}
			if logicalTextMatched {
				replacements = append(replacements, replacement{
					start: cursor,
					end:   len(payload),
					value: []byte(credential.Redacted),
				})
				values++
			}
			if values > options.Limits.MaxValues {
				return nil,
					rejected(options.Source, "credential_xml_value_limit_exceeded"),
					nil
			}
			break
		}
		open := cursor + openRelative
		logicalText := html.UnescapeString(string(payload[cursor:open]))
		if len(stack) > 0 && open > cursor && len(bytes.TrimSpace(payload[cursor:open])) > 0 {
			frame := &stack[len(stack)-1]
			if frame.textStart != 0 || frame.hasContent {
				frame.hasContent = true
			}
			frame.textStart = cursor
			frame.textEnd = open
			frame.hasContent = true
		}
		logicalTextMatched := false
		if open > cursor &&
			!xmlProjectionStackSuppressed(stack) {
			logicalTextMatched = knownCredentialScalarMatch(
				options,
				logicalText,
			)
			if logicalTextMatched {
				knownLogicalText = append(
					knownLogicalText,
					credential.Redacted...,
				)
			} else {
				knownLogicalText = append(
					knownLogicalText,
					logicalText...,
				)
			}
		}
		if logicalTextMatched {
			replacements = append(replacements, replacement{
				start: cursor,
				end:   open,
				value: []byte(credential.Redacted),
			})
			values++
		}
		closeIndex, err := xmlTagEnd(payload, open)
		if err != nil {
			return nil, Result{}, err
		}
		tag := payload[open : closeIndex+1]
		if xmlUnsupportedMarkup(tag) {
			suppressed := false
			for index := range stack {
				if stack[index].sensitive || stack[index].suppressed {
					suppressed = true
					break
				}
			}
			if len(stack) > 0 && stack[len(stack)-1].sensitive {
				stack[len(stack)-1].hasChild = true
			}
			if !suppressed && containsSensitiveNameHintWithOptions(tag, options) {
				if options.Mode == Preserve {
					item := preserved(options.Source, "import_credential_preserved")
					targetField := strings.TrimSuffix(
						strings.TrimSpace(options.TargetField),
						".",
					)
					if targetField == "" {
						targetField = "body"
					}
					// Comments, CDATA, processing instructions, and
					// directives do not expose a type-safe scalar offset.
					// Keep the exact XML executable, but register the whole
					// body as a value-free opaque runtime-redaction target.
					item.PreservedTargets = append(
						item.PreservedTargets,
						TargetRef{
							RequestKey: options.RequestKey,
							Field:      targetField + ".opaque",
						},
					)
					result.Merge(item)
					cursor = closeIndex + 1
					continue
				}
				// Comments, CDATA, processing instructions, and directives do not
				// expose schema-typed scalar offsets. Preserve a valid XML draft
				// while removing the complete ambiguous markup payload.
				replacements = append(replacements, replacement{
					start: open,
					end:   closeIndex + 1,
					value: sanitizedXMLMarkup(tag),
				})
				result.Merge(blocked(options.Source, "credential_xml_unsupported_markup_review_required"))
			} else if !suppressed && options.KnownCredentialMatcher != nil {
				scalarStart, scalarEnd, ok := xmlUnsupportedScalarSpan(
					payload,
					open,
					closeIndex+1,
				)
				logicalTextMatched := ok && knownCredentialScalarMatch(
					options,
					string(payload[scalarStart:scalarEnd]),
				)
				if logicalTextMatched {
					replacements = append(replacements, replacement{
						start: scalarStart,
						end:   scalarEnd,
						value: []byte(credential.Redacted),
					})
					values++
				}
				if ok && len(stack) > 0 &&
					bytes.HasPrefix(tag, []byte("<![CDATA[")) {
					if logicalTextMatched {
						knownLogicalText = append(
							knownLogicalText,
							credential.Redacted...,
						)
					} else {
						knownLogicalText = append(
							knownLogicalText,
							payload[scalarStart:scalarEnd]...,
						)
					}
				}
			}
			if values > options.Limits.MaxValues {
				return nil,
					rejected(options.Source, "credential_xml_value_limit_exceeded"),
					nil
			}
			cursor = closeIndex + 1
			continue
		}
		if bytes.HasPrefix(tag, []byte("</")) {
			if len(stack) == 0 {
				return nil, Result{}, errors.New("XML projection stack underflow")
			}
			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !frame.sensitive && !frame.suppressed &&
				frame.knownTextStart >= 0 &&
				frame.knownTextStart <= len(knownLogicalText) &&
				knownCredentialScalarBytesMatch(
					options,
					knownLogicalText[frame.knownTextStart:],
				) {
				return nil,
					rejected(options.Source, "credential_xml_known_value_split_content"),
					nil
			}
			if frame.sensitive {
				if frame.hasChild {
					if options.Mode == Preserve {
						// Mixed content cannot be replaced by a scalar binding
						// without changing the XML wire shape. Preserve the
						// operator's exact request, attach a value-free locator
						// for runtime redaction, and surface a review finding.
						// Credential retention by itself must not make the
						// imported destination unrunnable.
						item := preserved(
							frame.source,
							"credential_xml_sensitive_mixed_content_review_required",
						)
						targetField := strings.TrimSuffix(
							strings.TrimSpace(options.TargetField),
							".",
						)
						if targetField == "" {
							targetField = "body"
						}
						field := appendTargetField(
							targetField,
							"",
							"xml.mixed_content",
						)
						item.PreservedTargets = append(
							item.PreservedTargets,
							TargetRef{
								RequestKey: options.RequestKey,
								Field:      field,
								Pointer:    frame.path,
								Occurrence: nextTargetOccurrence(field, frame.path),
							},
						)
						result.Merge(item)
						cursor = closeIndex + 1
						continue
					}
					// Markup-bearing XML cannot be replaced by a runtime string
					// binding without changing XML structure or escaping. Keep a
					// valid disclosure-safe draft, but do not create a migratable
					// requirement whose value could not preserve the wire shape.
					replacements = append(replacements, replacement{
						start: frame.contentAt,
						end:   open,
						value: []byte(credential.Redacted),
					})
					result.Merge(blocked(frame.source, "credential_xml_sensitive_mixed_content_review_required"))
				} else if frame.hasContent {
					start, end := trimByteRange(payload, frame.textStart, frame.textEnd)
					field := appendTargetField(
						options.TargetField,
						"",
						"xml.text",
					)
					projected, item, err := projectSensitiveScalar(
						string(payload[start:end]),
						frame.name,
						frame.source,
						TargetRef{
							RequestKey: options.RequestKey,
							Field:      field,
							Pointer:    frame.path,
						},
						nextTargetOccurrence(field, frame.path),
						options,
					)
					if err != nil {
						return nil, Result{}, err
					}
					values++
					if options.Mode != Preserve ||
						preserveRequiresCredentialReplacement(item) {
						replacements = append(replacements, replacement{start: start, end: end, value: []byte(projected)})
					}
					result.Merge(item)
				}
			}
			if values > options.Limits.MaxValues {
				return nil,
					rejected(options.Source, "credential_xml_value_limit_exceeded"),
					nil
			}
			cursor = closeIndex + 1
			continue
		}
		name, attrs, selfClosing, err := parseXMLStartTag(payload, open, closeIndex+1)
		if err != nil {
			return nil, Result{}, err
		}
		if len(stack) >= options.Limits.MaxDepth {
			if options.Mode == Preserve {
				return append([]byte(nil), payload...),
					preservedOpaquePayloadResult(
						payload,
						options,
						"import_credential_review_incomplete",
					), nil
			}
			return nil, rejected(options.Source, "credential_xml_depth_limit_exceeded"), nil
		}
		if len(stack) > 0 {
			stack[len(stack)-1].hasChild = true
		}
		suppressed := false
		for index := range stack {
			if stack[index].sensitive || stack[index].suppressed {
				suppressed = true
				break
			}
		}
		source := options.Source
		elementPath := "/" + xmlLocalName(name)
		if len(stack) > 0 {
			elementPath = strings.TrimRight(stack[len(stack)-1].path, "/") + elementPath
		}
		elementSensitiveName, elementSensitive := governedSensitiveName(name, options)
		if !elementSensitive {
			elementSensitiveName, elementSensitive = xmlNamedCredential(
				payload,
				attrs,
				options,
			)
		}
		for attrIndex, attr := range attrs {
			sensitiveName, sensitive := governedSensitiveName(attr.name, options)
			if !sensitive && elementSensitive && xmlCredentialValueAttribute(attr.name) {
				sensitiveName = elementSensitiveName
				sensitive = true
			}
			if suppressed || attr.namespace {
				continue
			}
			if !sensitive {
				if knownCredentialScalarMatch(
					options,
					html.UnescapeString(string(payload[attr.valueStart:attr.valueEnd])),
				) {
					replacements = append(replacements, replacement{
						start: attr.valueStart,
						end:   attr.valueEnd,
						value: []byte(credential.Redacted),
					})
					values++
				}
				continue
			}
			field := appendTargetField(
				options.TargetField,
				"",
				"xml.attribute",
			)
			pointer := elementPath + "/@" + xmlLocalName(attr.name)
			projected, item, err := projectSensitiveScalar(
				string(payload[attr.valueStart:attr.valueEnd]),
				sensitiveName,
				source,
				TargetRef{
					RequestKey: options.RequestKey,
					Field:      field,
					Pointer:    pointer,
					Index:      attrIndex,
				},
				nextTargetOccurrence(field, pointer),
				options,
			)
			if err != nil {
				return nil, Result{}, err
			}
			values++
			if options.Mode != Preserve ||
				preserveRequiresCredentialReplacement(item) {
				replacements = append(replacements, replacement{start: attr.valueStart, end: attr.valueEnd, value: []byte(projected)})
			}
			result.Merge(item)
		}
		if values > options.Limits.MaxValues {
			if options.Mode == Preserve {
				return append([]byte(nil), payload...),
					preservedOpaquePayloadResult(
						payload,
						options,
						"import_credential_review_incomplete",
					), nil
			}
			return nil, rejected(options.Source, "credential_xml_value_limit_exceeded"), nil
		}
		if !selfClosing {
			stack = append(stack, xmlFrame{
				name:           firstNonEmpty(elementSensitiveName, xmlLocalName(name)),
				path:           elementPath,
				sensitive:      !suppressed && elementSensitive,
				suppressed:     suppressed,
				contentAt:      closeIndex + 1,
				source:         source,
				knownTextStart: len(knownLogicalText),
			})
		}
		cursor = closeIndex + 1
	}
	if result.Disposition == Rejected {
		return nil, result, nil
	}
	return applyReplacements(payload, replacements), result, nil
}

func knownCredentialScalarMatch(options Options, value string) bool {
	if options.KnownCredentialMatcher == nil {
		return false
	}
	if !reserveKnownCredentialComparison(options, len(value)) {
		return true
	}
	return options.KnownCredentialMatcher(value)
}

func knownCredentialScalarBytesMatch(options Options, value []byte) bool {
	if options.KnownCredentialMatcher == nil {
		return false
	}
	if !reserveKnownCredentialComparison(options, len(value)) {
		return true
	}
	return options.KnownCredentialMatcher(string(value))
}

func reserveKnownCredentialComparison(options Options, size int) bool {
	if options.knownCredentialBudget == nil {
		return true
	}
	if size < 0 ||
		int64(size) > options.knownCredentialBudget.remainingBytes {
		options.knownCredentialBudget.remainingBytes = 0
		return false
	}
	options.knownCredentialBudget.remainingBytes -= int64(size)
	return true
}

func boundedKnownCredentialMatcher(options Options) func(string) bool {
	if options.KnownCredentialMatcher == nil {
		return nil
	}
	return func(value string) bool {
		return knownCredentialScalarMatch(options, value)
	}
}

func xmlProjectionStackSuppressed(stack []xmlFrame) bool {
	for index := range stack {
		if stack[index].sensitive || stack[index].suppressed {
			return true
		}
	}
	return false
}

func xmlUnsupportedScalarSpan(
	payload []byte,
	start int,
	end int,
) (int, int, bool) {
	if start < 0 || end > len(payload) || end <= start {
		return 0, 0, false
	}
	tag := payload[start:end]
	switch {
	case bytes.HasPrefix(tag, []byte("<!--")) &&
		bytes.HasSuffix(tag, []byte("-->")):
		return start + 4, end - 3, true
	case bytes.HasPrefix(tag, []byte("<![CDATA[")) &&
		bytes.HasSuffix(tag, []byte("]]>")):
		return start + 9, end - 3, true
	default:
		// XML declarations, processing instructions, and DTD declarations
		// contain grammar/schema values. Cross-field equality must not rewrite
		// those structural tokens.
		return 0, 0, false
	}
}

func xmlUnsupportedMarkup(tag []byte) bool {
	return bytes.HasPrefix(tag, []byte("<!--")) ||
		bytes.HasPrefix(tag, []byte("<![CDATA[")) ||
		bytes.HasPrefix(tag, []byte("<?")) ||
		bytes.HasPrefix(tag, []byte("<!"))
}

func sanitizedXMLMarkup(tag []byte) []byte {
	switch {
	case bytes.HasPrefix(tag, []byte("<![CDATA[")):
		return []byte("<![CDATA[" + credential.Redacted + "]]>")
	case bytes.HasPrefix(tag, []byte("<?")):
		return []byte("<?redacted?>")
	default:
		return []byte("<!-- " + credential.Redacted + " -->")
	}
}

type xmlAttribute struct {
	name       string
	namespace  bool
	valueStart int
	valueEnd   int
}

func xmlNamedCredential(payload []byte, attrs []xmlAttribute, options Options) (string, bool) {
	for _, attr := range attrs {
		if attr.namespace || !xmlCredentialNameAttribute(attr.name) {
			continue
		}
		// ProjectXML's first pass has already validated all XML entities. HTML's
		// unescaper is used only to recover the logical identifier from this
		// bounded attribute span; valid XML cannot contain HTML-only named
		// entities, so it cannot broaden an accepted source document.
		logicalName := html.UnescapeString(string(payload[attr.valueStart:attr.valueEnd]))
		if sensitiveName, sensitive := governedSensitiveName(logicalName, options); sensitive {
			return sensitiveName, true
		}
	}
	return "", false
}

func xmlCredentialNameAttribute(name string) bool {
	switch strings.ToLower(strings.TrimSpace(xmlLocalName(name))) {
	case "name", "key", "field", "property":
		return true
	default:
		return false
	}
}

func xmlCredentialValueAttribute(name string) bool {
	switch strings.ToLower(strings.TrimSpace(xmlLocalName(name))) {
	case "value", "val", "default", "fixed":
		return true
	default:
		return false
	}
}

func parseXMLStartTag(payload []byte, start, end int) (string, []xmlAttribute, bool, error) {
	index := start + 1
	for index < end && isXMLSpace(payload[index]) {
		index++
	}
	nameStart := index
	for index < end && !isXMLSpace(payload[index]) && payload[index] != '/' && payload[index] != '>' {
		index++
	}
	if index == nameStart {
		return "", nil, false, errors.New("XML start tag has no name")
	}
	name := string(payload[nameStart:index])
	var attrs []xmlAttribute
	for index < end {
		for index < end && isXMLSpace(payload[index]) {
			index++
		}
		if index >= end || payload[index] == '>' || payload[index] == '/' {
			break
		}
		attrStart := index
		for index < end && !isXMLSpace(payload[index]) && payload[index] != '=' && payload[index] != '>' {
			index++
		}
		attrName := string(payload[attrStart:index])
		namespace := attrName == "xmlns" || strings.HasPrefix(attrName, "xmlns:")
		for index < end && isXMLSpace(payload[index]) {
			index++
		}
		if index >= end || payload[index] != '=' {
			return "", nil, false, errors.New("XML attribute lacks equals")
		}
		index++
		for index < end && isXMLSpace(payload[index]) {
			index++
		}
		if index >= end || (payload[index] != '"' && payload[index] != '\'') {
			return "", nil, false, errors.New("XML attribute lacks quoted value")
		}
		quote := payload[index]
		index++
		valueStart := index
		for index < end && payload[index] != quote {
			index++
		}
		if index >= end {
			return "", nil, false, errors.New("XML attribute value is unterminated")
		}
		attrs = append(attrs, xmlAttribute{name: xmlLocalName(attrName), namespace: namespace, valueStart: valueStart, valueEnd: index})
		index++
	}
	selfClosing := bytes.HasSuffix(bytes.TrimSpace(payload[start:end-1]), []byte("/"))
	return name, attrs, selfClosing, nil
}

func xmlTagEnd(payload []byte, start int) (int, error) {
	if bytes.HasPrefix(payload[start:], []byte("<!--")) {
		if relative := bytes.Index(payload[start+4:], []byte("-->")); relative >= 0 {
			return start + 4 + relative + 2, nil
		}
		return 0, errors.New("XML comment is unterminated")
	}
	if bytes.HasPrefix(payload[start:], []byte("<![CDATA[")) {
		if relative := bytes.Index(payload[start+9:], []byte("]]>")); relative >= 0 {
			return start + 9 + relative + 2, nil
		}
		return 0, errors.New("XML CDATA is unterminated")
	}
	var quote byte
	for index := start + 1; index < len(payload); index++ {
		if quote != 0 {
			if payload[index] == quote {
				quote = 0
			}
			continue
		}
		if payload[index] == '"' || payload[index] == '\'' {
			quote = payload[index]
			continue
		}
		if payload[index] == '>' {
			return index, nil
		}
	}
	return 0, errors.New("XML tag is unterminated")
}

func xmlLocalName(name string) string {
	if index := strings.LastIndexByte(name, ':'); index >= 0 {
		return name[index+1:]
	}
	return name
}

func trimByteRange(payload []byte, start, end int) (int, int) {
	for start < end && isXMLSpace(payload[start]) {
		start++
	}
	for end > start && isXMLSpace(payload[end-1]) {
		end--
	}
	return start, end
}

func isXMLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func validMultipartBoundary(boundary string) bool {
	if len(boundary) == 0 || len(boundary) > 70 || boundary[len(boundary)-1] == ' ' {
		return false
	}
	for _, value := range boundary {
		if value >= 'a' && value <= 'z' ||
			value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' ||
			strings.ContainsRune("'()+_,-./:=? ", value) {
			continue
		}
		return false
	}
	return true
}
