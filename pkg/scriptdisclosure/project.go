// Package scriptdisclosure projects executable scripts into irreversible,
// disclosure-safe copies without changing their noncredential structure.
//
// The durable script remains the execution authority. Project is intended for
// receipts, API responses, exports, and other evidence surfaces that must retain
// enough information to describe the imported script without becoming a secret
// escrow.
package scriptdisclosure

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxProjectionDepth = 64
	maxProjectionNodes = 250_000
	// Cross-field credential equivalence is deliberately bounded before any
	// scalar comparisons begin. Above this conservative work estimate a
	// bounded multi-pattern matcher replaces secrets × nodes scans.
	maxKnownCredentialComparisonWork int64 = 128 << 20
)

var portableTemplatePattern = regexp.MustCompile(`\{\{[^{}\r\n]{1,256}\}\}`)
var portableReferenceExpressionPattern = regexp.MustCompile(`^(?:(?:env|data|var|param|file)\.[A-Za-z0-9_.:/@-]+|secret\.` + engine.LogicalSecretNameInputPattern + `)$`)

type projectionContext struct {
	transactionParams   bool
	transactionFunction string
	label               bool
	key                 string
	opaque              bool
	headers             bool
	headerList          bool
	secretBindings      bool
	parameter           bool
	dataSource          bool
	metadata            bool
	javaVMArgument      bool
	untypedMap          bool
	schemaScalar        bool
	action              bool
	actionList          bool
	sequenceParams      bool
	scriptMetadata      bool
	importMetadata      bool
	scriptName          bool
	extractor           bool
	extractorList       bool
}

type projector struct {
	nodes                    int
	knownSecrets             []string
	knownMatcher             *credentialMatcher
	forcedSecrets            map[string]struct{}
	maskAllKnownValues       bool
	knownComparisonRemaining int64
	credentialParameterNames map[string]struct{}
	sensitiveSourceColumns   map[string][]string
	textScrubCache           map[string]string
	textScrubCacheBytes      int
	// scrubMemo caches the scrub package's text functions; nil scrubs directly.
	scrubMemo *scrub.Memo
}

// Project returns a detached script whose credential material has been
// irreversibly replaced with credential.Redacted. It preserves map entries,
// ordered slices, duplicate headers, control-flow branches, assertions,
// extractors, and every ordinary scalar.
func Project(script *dsl.Script) (*dsl.Script, error) {
	if script == nil {
		return nil, nil
	}
	return ProjectWithCredentialSource(script, script)
}

// ProjectWithCredentialSource projects script while deriving literal
// credential equivalence from credentialSource. This is useful when another
// field-aware projector has already masked the primary credential field but an
// identical literal must still be removed from an otherwise neutral field.
func ProjectWithCredentialSource(script, credentialSource *dsl.Script) (*dsl.Script, error) {
	return ProjectWithCredentialSourceMemo(script, credentialSource, nil)
}

// ProjectWithCredentialSourceMemo is ProjectWithCredentialSource scrubbing text
// through memo, which a caller projecting several related values shares
// between them; nil gives this projection a memo of its own.
func ProjectWithCredentialSourceMemo(script, credentialSource *dsl.Script, memo *scrub.Memo) (*dsl.Script, error) {
	if script == nil {
		return nil, nil
	}
	if credentialSource == nil {
		credentialSource = script
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, fmt.Errorf("marshal script disclosure copy: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode script disclosure copy: %w", err)
	}

	if memo == nil {
		memo = &scrub.Memo{}
	}
	p := &projector{
		credentialParameterNames: credentialParameterNames(credentialSource),
		sensitiveSourceColumns:   sensitiveDataSourceColumns(credentialSource),
		scrubMemo:                memo,
	}
	sourcePayload, err := json.Marshal(credentialSource)
	if err != nil {
		return nil, fmt.Errorf("marshal script credential source: %w", err)
	}
	sourceDecoder := json.NewDecoder(bytes.NewReader(sourcePayload))
	sourceDecoder.UseNumber()
	var sourceRaw any
	if err := sourceDecoder.Decode(&sourceRaw); err != nil {
		return nil, fmt.Errorf("decode script credential source: %w", err)
	}
	collector := newSecretCollector(p.credentialParameterNames, p.sensitiveSourceColumns)
	collector.collectOriginalBodyValidity(credentialSource)
	collector.collect(sourceRaw, projectionContext{}, 0)
	collector.collectScriptCredentialTargets(credentialSource)
	importedValues, err := engine.ImportedCredentialValuesForDisclosure(credentialSource)
	if err != nil {
		return nil, fmt.Errorf("resolve imported credential locators for disclosure: %w", err)
	}
	if len(importedValues) > 0 {
		p.forcedSecrets = make(map[string]struct{}, len(importedValues)*2)
		for _, value := range importedValues {
			collector.add(value)
			p.forcedSecrets[value] = struct{}{}
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				p.forcedSecrets[trimmed] = struct{}{}
			}
		}
	}
	if collector.structuredBudget.incomplete {
		return nil, fmt.Errorf("script disclosure credential collection is incomplete")
	}
	p.knownSecrets = collector.sorted()
	p.configureKnownMatching(len(payload) + len(sourcePayload))

	projected, err := p.project(raw, projectionContext{}, 0)
	if err != nil {
		return nil, err
	}
	// A resource-limit fallback is not a set of detected credentials. Do not
	// publish blanket masks that import review would turn into secret bindings.
	if p.maskAllKnownValues {
		return nil, fmt.Errorf("script disclosure credential comparison exceeds limits")
	}
	projectedPayload, err := json.Marshal(projected)
	if err != nil {
		return nil, fmt.Errorf("marshal projected script disclosure copy: %w", err)
	}
	var result dsl.Script
	if err := json.Unmarshal(projectedPayload, &result); err != nil {
		return nil, fmt.Errorf("decode projected script disclosure copy: %w", err)
	}
	return &result, nil
}

func (p *projector) project(value any, ctx projectionContext, depth int) (any, error) {
	p.nodes++
	if depth > maxProjectionDepth || p.nodes > maxProjectionNodes {
		return nil, fmt.Errorf("script disclosure projection exceeds structural limits")
	}
	switch typed := value.(type) {
	case map[string]any:
		if ctx.headerList {
			return p.projectHeaderEntry(typed, depth)
		}
		if ctx.dataSource {
			typed = p.projectDataSource(typed)
		}
		httpSequence := ctx.action &&
			strings.EqualFold(strings.TrimSpace(stringValue(typed["type"])), "http_sequence")
		credentialValue := mapHasCredentialValue(typed, p.credentialParameterNames)
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			normalized := normalizeName(key)
			if ctx.metadata &&
				key == dsl.ImportPreservedCredentialTargetsMetadataKey &&
				validCredentialTargetLocators(child) {
				out[key] = child
				continue
			}
			if normalized == normalizeName(dsl.ImportHeaderFidelityPreservedNameBase64Key) ||
				normalized == normalizeName(dsl.ImportHeaderFidelityPreservedValueBase64Key) {
				out[key] = base64.StdEncoding.EncodeToString([]byte(credential.Redacted))
				continue
			}
			if ctx.metadata && isOpaqueSourcePayloadKey(key) {
				out[key] = maskOpaque(child)
				continue
			}
			if isReviewEvidenceKey(key) {
				projected, err := p.project(child, projectionContext{
					key:      key,
					opaque:   true,
					metadata: true,
				}, depth+1)
				if err != nil {
					return nil, err
				}
				out[key] = projected
				continue
			}
			if ctx.key == "sapgui" && !ctx.metadata && !ctx.untypedMap && key == "session_behavior" {
				if text, ok := child.(string); ok && dsl.IsSAPGUISessionBehavior(text) {
					out[key] = text
					continue
				}
			}
			if ctx.secretBindings {
				out[key] = projectSecretBinding(child)
				continue
			}
			if normalizeName(ctx.key) == "proxy" && normalized == "username" {
				out[key] = projectSensitiveNode(child)
				continue
			}
			if credentialValue && (normalized == "value" || normalized == "values") {
				out[key] = projectCredentialCollection(child)
				continue
			}
			if normalized == "body" {
				if text, ok := child.(string); ok {
					out[key] = p.projectBody(text, typed)
				} else {
					out[key] = credential.Redacted
				}
				continue
			}
			if ctx.headers {
				out[key] = p.projectHeaderValue(key, child)
				continue
			}

			childContext := projectionContext{
				transactionParams: key == "params" && typed["type"] == "source_transaction",
				label:             ctx.label && (key == "value" || key == "raw"),
				key:               key,
				opaque:            ctx.opaque || isOpaqueMap(key),
				headers:           isHeaderMap(key),
				headerList:        isHeaderList(key),
				secretBindings:    isSecretBindingsMap(key),
				parameter:         normalizeName(key) == "parameters",
				dataSource:        normalizeName(key) == "datasources",
				metadata:          ctx.metadata || normalized == "metadata",
				javaVMArgument:    normalized == "additionalvmparameters",
				untypedMap:        ctx.untypedMap || isUntypedDisclosureMapKey(key),
				schemaScalar: !ctx.metadata &&
					!ctx.untypedMap &&
					isDisclosureSchemaDiscriminator(key),
				actionList: !ctx.untypedMap && isDisclosureActionListKey(key),
				sequenceParams: httpSequence &&
					normalized == "params",
				extractorList: ctx.action && normalized == "extractors",
				scriptName:    depth == 0 && normalized == "name",
			}
			if ctx.transactionParams && (key == "args" || key == "call_args") {
				childContext.transactionFunction, _ = typed["function"].(string)
			}
			if depth == 0 && normalized == "metadata" {
				childContext.untypedMap = false
				childContext.scriptMetadata = true
			}
			if ctx.scriptMetadata && normalized == "import" {
				childContext.untypedMap = false
				childContext.importMetadata = true
			}
			if ctx.importMetadata {
				childContext.schemaScalar = !ctx.untypedMap &&
					(isDisclosureSchemaDiscriminator(key) ||
						normalized == "source")
			}
			if ctx.extractor && normalized == "source" {
				childContext.schemaScalar = true
			}
			if ctx.sequenceParams && normalized == "requests" {
				childContext.untypedMap = false
				childContext.actionList = true
			}
			projected, err := p.project(child, childContext, depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = projected
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			childContext := ctx
			childContext.label = credential.TransactionLabelArgument(ctx.transactionFunction, index)
			if ctx.actionList {
				childContext = projectionContext{action: true}
			}
			if ctx.parameter {
				childContext = projectionContext{parameter: true}
			}
			if ctx.dataSource {
				childContext = projectionContext{dataSource: true}
			}
			if ctx.extractorList {
				childContext = projectionContext{extractor: true}
			}
			projected, err := p.project(child, childContext, depth+1)
			if err != nil {
				return nil, err
			}
			out[index] = projected
		}
		return out, nil
	case string:
		return p.projectString(typed, ctx), nil
	default:
		if isSensitiveKey(ctx.key) && !isSecretReferenceKey(ctx.key) {
			return credential.Redacted, nil
		}
		return value, nil
	}
}

func validCredentialTargetLocators(value any) bool {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > 256<<10 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var targets []credentialpolicy.TargetRef
	if err := decoder.Decode(&targets); err != nil {
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	for _, target := range targets {
		if !validLocatorText(target.Field, 1024, false) ||
			!validLocatorText(target.Pointer, 4096, true) ||
			!validLocatorText(target.RequestKey, 1024, true) ||
			target.Index < 0 || target.Occurrence < 0 {
			return false
		}
	}
	return true
}

func validLocatorText(value string, maxBytes int, allowEmpty bool) bool {
	if (!allowEmpty && strings.TrimSpace(value) == "") || len(value) > maxBytes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (p *projector) projectHeaderEntry(entry map[string]any, depth int) (map[string]any, error) {
	out := make(map[string]any, len(entry))
	name, _ := entry["name"].(string)
	for key, value := range entry {
		if strings.EqualFold(key, "name") {
			// Header names are executable schema, just like keys in the legacy
			// header map. Do not corrupt them because a credential happens to
			// equal a standard header name.
			out[key] = value
			continue
		}
		if strings.EqualFold(key, "value") {
			out[key] = p.projectHeaderValue(name, value)
			continue
		}
		projected, err := p.project(value, projectionContext{key: key}, depth+1)
		if err != nil {
			return nil, err
		}
		out[key] = projected
	}
	return out, nil
}

func (p *projector) projectHeaderValue(name string, value any) any {
	text, ok := value.(string)
	if !ok {
		if credential.SensitiveHeader(name) {
			return credential.Redacted
		}
		return value
	}
	if credential.SensitiveHeader(name) || isSensitiveKey(name) {
		return projectSensitiveString(text)
	}
	if structuralDisclosureMIMEHeader(name) {
		return credentialpolicy.RedactKnownMIMEHeader(
			name,
			p.scrubText(text),
			func(value string) bool {
				return p.replaceKnown(value) != value
			},
		)
	}
	if strings.EqualFold(strings.TrimSpace(name), "User-Agent") ||
		strings.EqualFold(strings.TrimSpace(name), "x-dtpc") {
		if isPortableBinding(text) {
			return text
		}
		return p.replaceKnown(scrubWithTemplateProjector(text, false, func(value string) string {
			return scrub.ValueDescriptiveHeader(name, value, scrub.Policy{})
		}))
	}
	if isNonSecretIdentifierHeader(name) {
		return p.replaceKnown(projectURL(text))
	}
	return p.replaceKnown(p.scrubText(text))
}

func structuralDisclosureMIMEHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "content-type", "content-disposition", "content-transfer-encoding",
		"mime-version":
		return true
	default:
		return false
	}
}

func (p *projector) projectString(value string, ctx projectionContext) string {
	if value == "" || isPortableBinding(value) {
		return value
	}
	if ctx.schemaScalar {
		// These fields select DSL/runtime behavior. Their values come from
		// bounded vocabularies or schema identities and cannot be treated as a
		// copied credential merely because the text happens to match one.
		return value
	}
	if ctx.scriptName {
		return p.replaceKnownLabel(value)
	}
	if ctx.javaVMArgument && javarunner.ContainsSensitiveVMParameterValue(value) {
		return credential.Redacted
	}
	if ctx.metadata && isPortableProvenanceIdentifier(ctx.key, value) {
		return p.replaceKnown(scrub.ValueURL(value, scrub.Policy{}))
	}
	if isSecretReferenceKey(ctx.key) && isNamedReference(value) {
		return value
	}
	if isSensitiveKey(ctx.key) {
		return projectSensitiveString(value)
	}
	if isURLKey(ctx.key) {
		return p.replaceKnown(projectURL(value))
	}
	if ctx.label || credential.LabelField(ctx.key) {
		return p.replaceKnown(p.scrubMemo.ValueLabel(value))
	}
	if ctx.opaque || isFreeTextKey(ctx.key) {
		return p.replaceKnown(p.scrubText(value))
	}
	return p.replaceKnown(value)
}

func (p *projector) projectBody(stored string, owner map[string]any) string {
	if stored == "" || isPortableBinding(stored) {
		return stored
	}
	wire := []byte(stored)
	encoded := strings.EqualFold(strings.TrimSpace(stringValue(owner["body_encoding"])), "base64")
	if encoded {
		if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored)); err == nil {
			wire = decoded
		} else {
			return credential.Redacted
		}
	}
	var knownMatcher func(string) bool
	if len(p.knownSecrets) > 0 {
		knownMatcher = func(value string) bool {
			return p.replaceKnown(value) != value
		}
	}
	projected, result, err := credentialpolicy.ProjectRequestBody(
		wire,
		contentType(owner),
		credentialpolicy.Options{
			Mode:                   credentialpolicy.Redact,
			Source:                 credentialpolicy.SourceRef{Format: "script_disclosure"},
			RequestKey:             "script_disclosure",
			TargetField:            "body",
			TrustedWholeValues:     bodyTemplateValues(string(wire)),
			KnownCredentialMatcher: knownMatcher,
		},
	)
	if err != nil || result.Disposition == credentialpolicy.Rejected || projected == nil {
		projected = []byte(credential.Redacted)
	}
	if !encoded {
		return string(projected)
	}
	if bytes.Equal(projected, wire) {
		return stored
	}
	return base64.StdEncoding.EncodeToString(projected)
}

func (p *projector) replaceKnown(value string) string {
	if value == "" || strings.Contains(value, credential.Redacted) && len(p.knownSecrets) == 0 {
		return value
	}
	if p.maskAllKnownValues {
		return credential.Redacted
	}
	if !p.reserveKnownComparison(len(value)) {
		return credential.Redacted
	}
	if p.knownMatcher != nil {
		return p.knownMatcher.replace(value)
	}
	return p.replaceKnownValues(value)
}

func (p *projector) replaceKnownLabel(value string) string {
	if value == "" || len(p.knownSecrets) == 0 {
		return value
	}
	if p.knownMatcher != nil {
		return p.knownMatcher.replace(value)
	}
	secretCount := int64(len(p.knownSecrets))
	if secretCount <= 0 ||
		int64(len(value)) > maxKnownCredentialComparisonWork/secretCount {
		return credential.Redacted
	}
	return p.replaceKnownValues(value)
}

func (p *projector) replaceKnownValues(value string) string {
	for _, secret := range p.knownSecrets {
		if value == secret {
			return credential.Redacted
		}
		_, forced := p.forcedSecrets[secret]
		if strings.TrimSpace(secret) == "" {
			continue
		}
		if len(secret) >= 4 {
			value = strings.ReplaceAll(value, secret, credential.Redacted)
			continue
		}
		if forced {
			value = replaceShortCredentialToken(value, secret)
		}
	}
	return value
}

func (p *projector) configureKnownMatching(inputBytes int) {
	if p == nil || p.maskAllKnownValues || inputBytes <= 0 ||
		len(p.knownSecrets) == 0 {
		return
	}
	if p.knownComparisonRemaining == 0 {
		p.knownComparisonRemaining = maxKnownCredentialComparisonWork
	}
	// Each source byte can reach the ordinary scalar pass and one structured
	// body pass. Avoid multiplication overflow by comparing through division.
	secretPasses := int64(len(p.knownSecrets)) * 2
	if secretPasses <= 0 ||
		int64(inputBytes) > p.knownComparisonRemaining/secretPasses {
		// Use a single bounded scan for large scripts instead of treating
		// the cost estimate as evidence that every scalar is a credential.
		p.knownMatcher = newCredentialMatcher(p.knownSecrets, p.forcedSecrets)
		p.maskAllKnownValues = p.knownMatcher == nil
	}
}

func (p *projector) reserveKnownComparison(valueBytes int) bool {
	if p == nil || valueBytes <= 0 || len(p.knownSecrets) == 0 {
		return true
	}
	if p.knownComparisonRemaining <= 0 {
		p.maskAllKnownValues = true
		return false
	}
	secretCount := int64(len(p.knownSecrets))
	if p.knownMatcher != nil {
		secretCount = 1
	}
	if int64(valueBytes) > p.knownComparisonRemaining/secretCount {
		p.knownComparisonRemaining = 0
		p.maskAllKnownValues = true
		return false
	}
	p.knownComparisonRemaining -= int64(valueBytes) * secretCount
	return true
}

// replaceShortCredentialToken masks a locator-proven short credential without
// treating it as an unrestricted substring. A one-byte password such as "t"
// must be removed from "credential=t", but it must not corrupt unrelated
// structure such as "POST", "http", or "/status".
func replaceShortCredentialToken(value, secret string) string {
	if value == secret {
		return credential.Redacted
	}
	if value == "" || secret == "" {
		return value
	}

	tokenCredential := containsCredentialTokenRune(secret)
	var projected strings.Builder
	searchFrom := 0
	copyFrom := 0
	for searchFrom <= len(value)-len(secret) {
		relative := strings.Index(value[searchFrom:], secret)
		if relative < 0 {
			break
		}
		start := searchFrom + relative
		end := start + len(secret)
		bounded := credentialTokenBoundary(value, start, end)
		if !tokenCredential {
			bounded = credentialPunctuationBoundary(value, secret, start, end)
		}
		if bounded {
			projected.WriteString(value[copyFrom:start])
			projected.WriteString(credential.Redacted)
			copyFrom = end
		}
		searchFrom = end
	}
	if copyFrom == 0 {
		return value
	}
	projected.WriteString(value[copyFrom:])
	return projected.String()
}

func containsCredentialTokenRune(value string) bool {
	for _, character := range value {
		if credentialTokenRune(character) {
			return true
		}
	}
	return false
}

func credentialTokenBoundary(value string, start, end int) bool {
	if start > 0 {
		before, _ := utf8.DecodeLastRuneInString(value[:start])
		if credentialTokenRune(before) {
			return false
		}
	}
	if end < len(value) {
		after, _ := utf8.DecodeRuneInString(value[end:])
		if credentialTokenRune(after) {
			return false
		}
	}
	return true
}

func credentialTokenRune(character rune) bool {
	return character == '_' || unicode.IsLetter(character) || unicode.IsDigit(character)
}

func credentialPunctuationBoundary(value, secret string, start, end int) bool {
	before, hasBefore := runeBefore(value, start)
	after, hasAfter := runeAfter(value, end)
	if (!hasBefore || punctuationCredentialLeftDelimiter(before)) &&
		(!hasAfter || punctuationCredentialRightDelimiter(after)) {
		return true
	}
	// A punctuation credential can be one complete path segment. A literal
	// slash is excluded so the leading "//" in a URL is never mistaken for two
	// credential occurrences.
	return !strings.ContainsRune(secret, '/') &&
		(!hasBefore || before == '/') &&
		(!hasAfter || after == '/')
}

func punctuationCredentialLeftDelimiter(character rune) bool {
	return unicode.IsSpace(character) || strings.ContainsRune(`=,:;([{<"'`, character)
}

func punctuationCredentialRightDelimiter(character rune) bool {
	return unicode.IsSpace(character) || strings.ContainsRune(`&,;:)]}>"'?#`, character)
}

func runeBefore(value string, offset int) (rune, bool) {
	if offset <= 0 {
		return 0, false
	}
	character, _ := utf8.DecodeLastRuneInString(value[:offset])
	return character, true
}

func runeAfter(value string, offset int) (rune, bool) {
	if offset >= len(value) {
		return 0, false
	}
	character, _ := utf8.DecodeRuneInString(value[offset:])
	return character, true
}

func isDisclosureSchemaDiscriminator(key string) bool {
	switch normalizeName(key) {
	case "version", "protocol", "type", "method", "bodyencoding",
		"format", "encoding", "strategy", "mode", "scope", "operator",
		"op", "severity", "disposition", "status", "phase", "kind",
		"sourceformat", "compatibilityversion", "sandbox",
		"runnerprofile", "codec", "helpermode", "extractortype",
		"jdkselection", "proxymode", "authmode", "httpversion",
		"redirectpolicy", "retrybackoff", "bodymode", "onmiss",
		"convert", "update", "selection", "onexhaustion", "delimiter",
		"charset", "timezone", "contenttype", "generatedby",
		"generatedfrom", "createdat", "updatedat":
		return true
	default:
		return false
	}
}

func isUntypedDisclosureMapKey(key string) bool {
	switch normalizeName(key) {
	case "params", "metadata", "preserved", "labels", "requestmapping",
		"responsemapping", "namespaces", "properties",
		"additionalattributes", "options":
		return true
	default:
		return false
	}
}

func isDisclosureActionListKey(key string) bool {
	switch normalizeName(key) {
	case "init", "actions", "teardown", "steps", "else":
		return true
	default:
		return false
	}
}

func projectSensitiveNode(value any) any {
	if text, ok := value.(string); ok {
		return projectSensitiveString(text)
	}
	if value == nil {
		return nil
	}
	return credential.Redacted
}

func projectCredentialCollection(value any) any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = projectSensitiveNode(item)
		}
		return out
	default:
		return projectSensitiveNode(value)
	}
}

func projectSecretBinding(value any) any {
	text, ok := value.(string)
	if !ok {
		if value == nil {
			return nil
		}
		return credential.Redacted
	}
	if isPortableBinding(text) {
		return text
	}
	return projectSensitiveString(text)
}

func projectSensitiveString(value string) string {
	if value == "" || isPortableBinding(value) {
		return value
	}
	trimmed := strings.TrimSpace(value)
	for _, scheme := range []string{"Bearer", "Basic", "Digest"} {
		if len(trimmed) > len(scheme) &&
			strings.EqualFold(trimmed[:len(scheme)], scheme) &&
			unicode.IsSpace(rune(trimmed[len(scheme)])) {
			return trimmed[:len(scheme)] + " " + credential.Redacted
		}
	}
	return credential.Redacted
}

func projectURL(value string) string {
	value = scrubWithTemplates(value, true)
	scheme := strings.Index(value, "://")
	if scheme < 0 {
		return value
	}
	authorityStart := scheme + 3
	authorityEnd := len(value)
	if separator := strings.IndexAny(value[authorityStart:], "/?#"); separator >= 0 {
		authorityEnd = authorityStart + separator
	}
	if at := strings.LastIndex(value[authorityStart:authorityEnd], "@"); at >= 0 {
		at += authorityStart
		value = value[:authorityStart] + credential.Redacted + value[at:]
	}
	return value
}

func scrubWithTemplates(value string, urlValue bool) string {
	return scrubWithTemplateProjector(value, urlValue, nil)
}

func scrubWithTemplateProjector(value string, urlValue bool, projectText func(string) string) string {
	if value == "" {
		return value
	}
	protected := value
	var separatorReplacements map[string]string
	var bindingReplacements map[string]string
	if urlValue {
		protected, separatorReplacements = protectAdditionalURLSeparators(protected)
		protected, bindingReplacements = protectURLBindings(protected)
	}
	protected, replacements := protectTemplateReferences(protected)
	if urlValue {
		protected = scrub.ValueURL(protected, scrub.Policy{})
	} else if projectText != nil {
		protected = projectText(protected)
	} else {
		protected = scrub.Value(protected, scrub.Policy{})
	}
	protected = restoreTextPlaceholders(protected, replacements)
	for placeholder, binding := range bindingReplacements {
		protected = strings.ReplaceAll(protected, placeholder, binding)
	}
	for placeholder, separator := range separatorReplacements {
		protected = strings.ReplaceAll(protected, placeholder, separator)
	}
	return protected
}

// Protect and restore all template spans in one pass each. Source-review text
// can repeat thousands of references; copying the entire string once per
// reference makes that otherwise bounded projection quadratic.
func protectTemplateReferences(value string) (string, map[string]string) {
	matches := portableTemplatePattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value, nil
	}
	prefix := uniquePlaceholder(value, "template", 0) + "~"
	replacements := make(map[string]string, len(matches))
	var protected strings.Builder
	protected.Grow(len(value))
	cursor := 0
	for index, match := range matches {
		placeholder := prefix + strconv.Itoa(index) + "~safe"
		protected.WriteString(value[cursor:match[0]])
		protected.WriteString(placeholder)
		replacements[placeholder] = value[match[0]:match[1]]
		cursor = match[1]
	}
	protected.WriteString(value[cursor:])
	return protected.String(), replacements
}

func restoreTextPlaceholders(value string, replacements map[string]string) string {
	if len(replacements) == 0 {
		return value
	}
	pairs := make([]string, 0, len(replacements)*2)
	for placeholder, reference := range replacements {
		pairs = append(pairs, placeholder, reference)
	}
	return strings.NewReplacer(pairs...).Replace(value)
}

func protectAdditionalURLSeparators(value string) (string, map[string]string) {
	if !strings.Contains(value, "?") {
		return value, nil
	}
	replacements := make(map[string]string)
	var protected strings.Builder
	protected.Grow(len(value))
	queryStarted := false
	inFragment := false
	for offset := 0; offset < len(value); offset++ {
		switch value[offset] {
		case '#':
			inFragment = true
			protected.WriteByte(value[offset])
		case '?':
			if !queryStarted && !inFragment {
				queryStarted = true
				protected.WriteByte(value[offset])
				continue
			}
			placeholder := "&" + uniquePlaceholder(value, "separator", len(replacements)) + "=safe&"
			replacements[placeholder] = "?"
			protected.WriteString(placeholder)
		default:
			protected.WriteByte(value[offset])
		}
	}
	if len(replacements) == 0 {
		return value, nil
	}
	return protected.String(), replacements
}

func protectURLBindings(value string) (string, map[string]string) {
	replacements := make(map[string]string)
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '?' || r == '&' || r == '#'
	})
	for _, part := range parts {
		_, rawValue, ok := strings.Cut(part, "=")
		if !ok || !isPortableBinding(rawValue) {
			continue
		}
		placeholder := uniquePlaceholder(value, "binding", len(replacements)) + "=safe"
		replacements[placeholder] = part
		value = strings.Replace(value, part, placeholder, 1)
	}
	return value, replacements
}

func uniquePlaceholder(value, domain string, index int) string {
	placeholder := "apex~" + domain + "~" + strconv.Itoa(index) + "~safe"
	for strings.Contains(value, placeholder) {
		placeholder += "~"
	}
	return placeholder
}

func bodyTemplateValues(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, reference := range portableTemplatePattern.FindAllString(value, -1) {
		if isSupportedPortableTemplate(reference) {
			result[strings.TrimSpace(reference)] = struct{}{}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func contentType(owner map[string]any) string {
	if entries, ok := owner["header_list"].([]any); ok {
		value := ""
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			name, _ := entry["name"].(string)
			if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
				value, _ = entry["value"].(string)
			}
		}
		if value != "" {
			return value
		}
	}
	if headers, ok := owner["headers"].(map[string]any); ok {
		for name, raw := range headers {
			if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
				value, _ := raw.(string)
				return value
			}
		}
	}
	return ""
}

func mapHasCredentialValue(value map[string]any, proven map[string]struct{}) bool {
	if name, _ := value["name"].(string); name != "" {
		if _, ok := proven[canonicalName(name)]; ok {
			return true
		}
	}
	if _, hasType := value["type"]; !hasType {
		return false
	}
	if metadata, ok := value["metadata"].(map[string]any); ok {
		if marked, _ := metadata["source_marked_secret"].(bool); marked {
			return true
		}
		if masked, _ := metadata["source_masked_secret"].(bool); masked {
			return true
		}
	}
	var hints strings.Builder
	for _, key := range []string{"name", "type", "target", "selector", "object_ref", "column"} {
		if text, ok := value[key].(string); ok {
			hints.WriteByte(' ')
			hints.WriteString(text)
		}
	}
	normalized := normalizeName(hints.String())
	for _, marker := range []string{
		"password", "passwd", "passphrase", "secret", "token",
		"credential", "authorization", "cookie", "apikey",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func normalizeName(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func canonicalName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isSensitiveKey(key string) bool {
	normalized := normalizeName(key)
	switch normalized {
	case "tokenurl", "persistsessionacrossiterations", "credentialpolicy",
		"effectiveauth", "authmode":
		return false
	}
	if credential.SensitiveName(key) {
		return true
	}
	for _, suffix := range []string{
		"authorization", "password", "passwd", "passphrase", "token",
		"secret", "apikey", "credential", "cookie", "sessionid",
		"privatekey", "accesskey", "accesskeyid", "secretaccesskey",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func isSecretReferenceKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "client_secret_ref", "dsn_secret", "sasl_password_secret", "connection_string_secret", "ca_cert_secret",
		"trust_store_ref", "credential_ref", "storage_ref":
		return true
	default:
		return false
	}
}

func isNamedReference(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, credential.Redacted) || len(value) > 512 {
		return false
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._:/@-{}", r) {
			continue
		}
		return false
	}
	return true
}

func isPortableBinding(value string) bool {
	if strings.Contains(value, credential.Redacted) {
		return false
	}
	references := portableTemplatePattern.FindAllString(value, -1)
	if len(references) == 0 {
		return false
	}
	remainder := value
	for _, reference := range references {
		if !isSupportedPortableTemplate(reference) {
			return false
		}
		remainder = strings.Replace(remainder, reference, "", 1)
	}
	switch strings.ToLower(strings.TrimSpace(remainder)) {
	case "", "bearer", "basic", "digest":
		return true
	default:
		return false
	}
}

// Syntactic double braces are not evidence of a runtime reference. Only
// supported names/functions may bypass sensitive scalar projection; this does
// not evaluate a function or resolve a credential while deciding portability.
func isSupportedPortableTemplate(value string) bool {
	if len(value) < 5 || !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") {
		return false
	}
	expression := strings.TrimSpace(value[2 : len(value)-2])
	if expression == "run.id" || portableReferenceExpressionPattern.MatchString(expression) {
		return true
	}
	switch strings.TrimSuffix(expression, "()") {
	case "fn.uuid", "fn.uuid.v4", "fn.timestamp.unix", "fn.timestamp.unix_ms",
		"fn.timestamp.iso8601", "fn.timestamp.rfc3339", "fake.uuid", "fake.email":
		return true
	}
	if strings.HasPrefix(expression, "fn.random(") && strings.HasSuffix(expression, ")") {
		first, second, found := strings.Cut(expression[len("fn.random("):len(expression)-1], ",")
		minimum, firstErr := strconv.Atoi(strings.TrimSpace(first))
		maximum, secondErr := strconv.Atoi(strings.TrimSpace(second))
		return found && firstErr == nil && secondErr == nil && minimum <= maximum
	}
	if strings.HasPrefix(expression, "fn.random_string(") && strings.HasSuffix(expression, ")") {
		length, err := strconv.Atoi(strings.TrimSpace(expression[len("fn.random_string(") : len(expression)-1]))
		return err == nil && length > 0 && length <= dsl.MaxGeneratedParameterBytes
	}
	return false
}

func isURLKey(key string) bool {
	switch normalizeName(key) {
	case "baseurl", "url", "brokerurl", "schemaregistryurl", "server", "origin", "target", "address":
		return true
	default:
		return false
	}
}

func isFreeTextKey(key string) bool {
	switch normalizeName(key) {
	case "value", "expected", "inlinedata", "schema", "objectschema",
		"expression", "expr", "left", "right", "runnercommand":
		return true
	default:
		return false
	}
}

func isOpaqueMap(key string) bool {
	switch normalizeName(key) {
	case "params", "metadata", "preserved", "properties", "labels", "options",
		"additionalattributes", "requestmapping", "responsemapping", "namespaces":
		return true
	default:
		return false
	}
}

func isHeaderMap(key string) bool {
	switch normalizeName(key) {
	case "headers", "defaultheaders":
		return true
	default:
		return false
	}
}

func isHeaderList(key string) bool {
	switch normalizeName(key) {
	case "headerlist", "defaultheaderlist":
		return true
	default:
		return false
	}
}

func isSecretBindingsMap(key string) bool {
	return normalizeName(key) == "secretbindings"
}

func isOpaqueSourcePayloadKey(key string) bool {
	switch normalizeName(key) {
	case "jsr223sources", "k6gatedsource", "postmaneventsources",
		"credentialtraversalsourcesnapshotbase64", "devwebgatedsource",
		"jmeterjdbcsources", "enterpriseunsupportedproxysource",
		normalizeName(dsl.ImportPreservedEnterpriseJavaVMSourceKey),
		normalizeName(dsl.ImportPreservedEnterpriseJavaJDBCSourceKey):
		return true
	default:
		return false
	}
}

func isReviewEvidenceKey(key string) bool {
	switch normalizeName(key) {
	case "sourcereview", "importreview", "review", "reviewevidence":
		return true
	default:
		return false
	}
}

func isPortableProvenanceIdentifier(key, value string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "source_path", "source_step_id", "source_profile", "source_uid":
		return true
	case "diagnostic_ids", "finding_ids":
		const prefix = "bruno-native-"
		if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+24 {
			return false
		}
		for _, r := range value[len(prefix):] {
			if !strings.ContainsRune("0123456789abcdef", r) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func maskOpaque(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = maskOpaque(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = maskOpaque(child)
		}
		return out
	case nil:
		return nil
	default:
		return credential.Redacted
	}
}

func isNonSecretIdentifierHeader(name string) bool {
	normalized := normalizeName(name)
	for _, sensitive := range []string{
		"authorization", "password", "secret", "token", "cookie",
		"session", "credential", "apikey", "accesskey", "privatekey",
	} {
		if strings.Contains(normalized, sensitive) {
			return false
		}
	}
	for _, suffix := range []string{"messageid", "traceid", "correlationid", "requestid"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func sortedSecrets(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i]) == len(result[j]) {
			return result[i] < result[j]
		}
		return len(result[i]) > len(result[j])
	})
	return result
}
