package enterprisearchive

// extractors.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// registrationConversion is a converted save-parameter registration: the
// extractor plus everything the importer must say about the parts of the
// registration APEX does not reproduce.
type registrationConversion struct {
	extractor *dsl.Extractor
	warnings  []string
	// findings attach to the request the registration applies to.
	findings []importreview.Finding
	// filter is judged once that request is known.
	filter *pendingRegistrationFilter
}

// convertRegistration converts a save-parameter registration and reviews its
// search scope, filters and data-format extensions.
func convertRegistration(fn ParsedFunction) registrationConversion {
	ext, warnings, scope := convertExtractorWithScope(fn)
	conversion := registrationConversion{extractor: ext, warnings: warnings}
	if ext == nil {
		return conversion
	}
	conversion.findings = append(conversion.findings, scope.findings...)
	ext.Scope = sourceRegistrationExtractorScope(fn, ext.Source)
	filterFindings, filter := sourceRegistrationFilters(fn, ext.Name, ext.Source)
	conversion.findings = append(conversion.findings, filterFindings...)
	conversion.filter = filter
	return conversion
}

// convertExtractor converts a dynamic-value registration function
// (web_reg_save_param and variants) to a DSL Extractor, plus warnings for
// modifiers that cannot be reproduced exactly.
func convertExtractor(fn ParsedFunction) (*dsl.Extractor, []string) {
	ext, warnings, _ := convertExtractorWithScope(fn)
	return ext, warnings
}

func convertExtractorWithScope(fn ParsedFunction) (*dsl.Extractor, []string, registrationScope) {
	paramName := canonicalSourceParameterName(convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "ParamName"), fn.StepName))
	if paramName == "" {
		return nil, nil, registrationScope{}
	}

	warnings := extractorModifierWarnings(fn, paramName)
	ordinal, saveAll := sourceExtractorOrdinal(fn)
	mode := ""
	if saveAll || strings.EqualFold(strings.TrimSpace(firstNonEmptyArg(fn, "SelectAll")), "yes") {
		// ORD=All / Ordinal=All / SelectAll=Yes save every match into
		// {{var.name_1}}..{{var.name_N}} plus {{var.name_count}}.
		mode = "all"
		ordinal = 0
	}
	onMiss := sourceExtractorOnMiss(fn)
	saveOffset, saveLength, convert, postWarnings := sourceExtractorPostProcessing(fn, paramName)
	warnings = append(warnings, postWarnings...)

	if fn.Name == "web_reg_save_param_attrib" {
		expression, attribWarnings, ok := sourceAttribSelector(fn, paramName, mode, ordinal)
		warnings = append(warnings, attribWarnings...)
		if !ok {
			return nil, warnings, registrationScope{}
		}
		return &dsl.Extractor{
			Name:       paramName,
			Source:     "body",
			Type:       "xpath",
			Expression: expression,
			OnMiss:     onMiss,
			Mode:       mode,
			SaveOffset: saveOffset,
			SaveLength: saveLength,
			Convert:    convert,
		}, warnings, registrationScope{source: "body"}
	}

	if fn.Name == "web_reg_save_param_regexp" {
		expression := firstNonEmptyArg(fn, "RegExp")
		if untrimmed, _, ok := untrimmedSourceArgument(fn, "RegExp"); ok {
			// Whitespace in a regular expression is part of the pattern.
			expression = untrimmed
		}
		if expression == "" {
			return nil, warnings, registrationScope{}
		}
		scope := sourceRegistrationScope(fn, paramName, true)
		ext := &dsl.Extractor{
			Name:       paramName,
			Source:     scope.source,
			Type:       "regex",
			Expression: expression,
			OnMiss:     onMiss,
			Mode:       mode,
			SaveOffset: saveOffset,
			SaveLength: saveLength,
			Convert:    convert,
		}
		if mode == "" && ordinal > 1 {
			ext.Type = "regex_ordinal"
			ext.Expression = encodeRegexOrdinalExpression(ext.Expression, ordinal)
		}
		return ext, warnings, scope
	}

	if fn.Name == "web_reg_save_param_json" {
		expression := convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "QueryString"), firstNonEmptyArg(fn, "JSONPath"), firstNonEmptyArg(fn, "Query"))
		if expression == "" {
			return nil, warnings, registrationScope{}
		}
		scope := sourceRegistrationScope(fn, paramName, false)
		return &dsl.Extractor{
			Name:       paramName,
			Source:     scope.source,
			Type:       "jsonpath",
			Expression: expression,
			OnMiss:     onMiss,
			Mode:       mode,
			SaveOffset: saveOffset,
			SaveLength: saveLength,
			Convert:    convert,
		}, warnings, scope
	}

	if fn.Name == "web_reg_save_param_xpath" {
		expression := convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "QueryString"), firstNonEmptyArg(fn, "XPath"), firstNonEmptyArg(fn, "Query"))
		if expression == "" {
			return nil, warnings, registrationScope{}
		}
		scope := sourceRegistrationScope(fn, paramName, false)
		return &dsl.Extractor{
			Name:       paramName,
			Source:     scope.source,
			Type:       "xpath",
			Expression: expression,
			OnMiss:     onMiss,
			Mode:       mode,
			SaveOffset: saveOffset,
			SaveLength: saveLength,
			Convert:    convert,
		}, warnings, scope
	}

	lb, rb, caseInsensitive, flagWarnings := sourceBoundaryArguments(fn)
	warnings = append(warnings, flagWarnings...)
	scope := sourceRegistrationScope(fn, paramName, true)
	if scope.source == "header" {
		// LoadRunner's header buffer ends each line with CRLF; APEX's header
		// surface uses LF, so a recorded RB=\r\n would never match.
		lb = strings.ReplaceAll(lb, "\r\n", "\n")
		rb = strings.ReplaceAll(rb, "\r\n", "\n")
	}

	ext := &dsl.Extractor{
		Name:       paramName,
		Source:     scope.source,
		OnMiss:     onMiss,
		Mode:       mode,
		SaveOffset: saveOffset,
		SaveLength: saveLength,
		Convert:    convert,
	}

	salesforceCSHCHandoff := isSalesforceCSHCHandoffExtractor(paramName, lb, rb)
	switch {
	case salesforceCSHCHandoff:
		ext.Type = "regex"
		ext.Expression = salesforceCSHCExtractorRegex()
		if mode == "" && ordinal > 1 {
			ext.Type = "regex_ordinal"
			ext.Expression = encodeRegexOrdinalExpression(ext.Expression, ordinal)
		}
	case lb == "" && rb == "":
		// Empty boundaries on both sides save the entire response.
		ext.Type = "regex"
		ext.Expression = "(?s)^(.*)$"
	case fn.Name != "web_reg_save_param_ex" && strings.Contains(lb, "\"") && strings.Contains(rb, "\"") && !caseInsensitive:
		// Quoted-markup boundaries historically convert to an anchored regex.
		ext.Type = "regex"
		ext.Expression = regexp.QuoteMeta(lb) + "(.+?)" + regexp.QuoteMeta(rb)
		if mode == "" && ordinal > 1 {
			ext.Type = "regex_ordinal"
			ext.Expression = encodeRegexOrdinalExpression(regexp.QuoteMeta(lb)+"(.+?)"+regexp.QuoteMeta(rb), ordinal)
		}
	default:
		ext.Type = "boundary"
		ext.Expression = encodeBoundaryExpression(lb, rb, ordinal, caseInsensitive)
	}
	if salesforceCSHCHandoff {
		warnings = append(warnings, fmt.Sprintf("line %d: Salesforce login handoff extractor %q was normalized from strict boundary match (%s ... %s) to a cshc query/form regex; validate it against a current Salesforce capture or prefer HAR-backed salesforce_native import", fn.LineNumber, paramName, cleanConfigValue(lb), cleanConfigValue(rb)))
	}

	return ext, warnings, scope
}

func isSalesforceCSHCHandoffExtractor(paramName, lb, rb string) bool {
	return strings.EqualFold(paramName, "c_cshc") || (strings.Contains(strings.ToLower(lb), "cshc") && strings.Contains(strings.ToLower(rb), "display"))
}

func salesforceCSHCExtractorRegex() string {
	return `(?i)(?:^|[?&{,"\s]|&amp;)cshc["']?\s*[:=]\s*["']?([^"&',}<\s]+)`
}

// sourceExtractorPostProcessing maps the SaveOffset=/SaveLen=/Convert=
// registration attributes onto the DSL extractor post-processing fields.
func sourceExtractorPostProcessing(fn ParsedFunction, paramName string) (saveOffset, saveLength int, convert string, warnings []string) {
	saveOffset = parsePositiveInt(firstNonEmptyArg(fn, "SaveOffset"))
	saveLength = parsePositiveInt(firstNonEmptyArg(fn, "SaveLen"))
	if raw := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "SaveLen"))); raw != "" {
		// SaveLen=0 saves an empty string in the source tool, but the DSL's
		// save_length zero value means "entire remainder" — surface the
		// inversion instead of importing it silently. (-1 = entire, faithful.)
		if parsed, err := strconv.Atoi(raw); err == nil && parsed == 0 {
			warnings = append(warnings, fmt.Sprintf("line %d: parameter %q uses SaveLen=0 (the source saves an empty string); the DSL cannot express zero-length truncation, so the full value is saved", fn.LineNumber, paramName))
		}
	}

	switch normalizeEnum(firstNonEmptyArg(fn, "Convert")) {
	case "":
	case "html_to_url":
		// HTML-format to URL-format: entity-decode, then percent-encode.
		convert = "html_url_encode"
	case "html_to_text":
		convert = "html_decode"
	default:
		warnings = append(warnings, fmt.Sprintf("line %d: parameter %q uses Convert=%s, which has no DSL equivalent; the value is saved unconverted", fn.LineNumber, paramName, cleanConfigValue(firstNonEmptyArg(fn, "Convert"))))
	}
	return saveOffset, saveLength, convert, warnings
}

// sourceExtractorOnMiss maps the NotFound= registration attribute to the DSL
// extractor on_miss behavior. The source default fails the iteration when a
// registered extraction finds no match; the lenient forms (warning, and its
// older name empty) assign an empty string to the parameter and continue, so
// they map to on_miss "empty" rather than leaving the variable unset.
func sourceExtractorOnMiss(fn ParsedFunction) string {
	switch strings.ToLower(strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "NotFound")))) {
	case "warning", "warn", "empty":
		return "empty"
	default:
		return "fail"
	}
}

// sourceExtractorOrdinal reads the match-instance selector (ORD= or Ordinal=)
// from a registration function. saveAll reports the "All" form, which saves
// every match into indexed parameters.
func sourceExtractorOrdinal(fn ParsedFunction) (ordinal int, saveAll bool) {
	value := convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "ORD"), firstNonEmptyArg(fn, "Ordinal"))
	if strings.EqualFold(strings.TrimSpace(cleanConfigValue(value)), "all") {
		return 0, true
	}
	return parsePositiveInt(value), false
}

// untrimmedSourceArgument decodes a "Key=value" (or "Key/FLAGS=value") C
// string argument without trimming the value, returning the modifier flags.
// It reports false when the argument is absent, empty, or not a plain string
// literal.
func untrimmedSourceArgument(fn ParsedFunction, key string) (string, []string, bool) {
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" {
			continue
		}
		base, _, _ := strings.Cut(strings.TrimSpace(arg.Name), "/")
		if !strings.EqualFold(base, key) {
			continue
		}
		decoded, ok := decodeAdjacentCStringLiterals(arg.Raw)
		if !ok {
			return "", nil, false
		}
		name, value, found := strings.Cut(decoded, "=")
		if !found || value == "" {
			return "", nil, false
		}
		var flags []string
		if _, flagText, hasFlags := strings.Cut(strings.TrimSpace(name), "/"); hasFlags {
			flags = strings.Split(flagText, "/")
		}
		return value, flags, true
	}
	return "", nil, false
}

// sourceBoundaryArguments resolves LB=/RB= boundary values, including
// modifier-flag forms such as LB/IC= (case-insensitive). Unsupported modifier
// flags produce warnings instead of being dropped silently.
func sourceBoundaryArguments(fn ParsedFunction) (lb, rb string, caseInsensitive bool, warnings []string) {
	resolve := func(base string) (string, []string) {
		// Boundary whitespace is significant ("LB=Ref: ", "RB=\r\n"), but the
		// generic argument values are trimmed, so read the C literal itself.
		if value, flags, ok := untrimmedSourceArgument(fn, base); ok {
			return value, flags
		}
		if value := fn.Args[base]; value != "" {
			return value, nil
		}
		for name, value := range fn.Args {
			if value == "" {
				continue
			}
			key, flags, hasFlags := strings.Cut(name, "/")
			if !strings.EqualFold(key, base) {
				continue
			}
			if hasFlags {
				return value, strings.Split(flags, "/")
			}
			return value, nil
		}
		for _, raw := range fn.RawArgs {
			key, value, ok := strings.Cut(raw, "=")
			if !ok || value == "" {
				continue
			}
			base2, flags, hasFlags := strings.Cut(key, "/")
			if !strings.EqualFold(base2, base) {
				continue
			}
			if hasFlags {
				return value, strings.Split(flags, "/")
			}
			return value, nil
		}
		return "", nil
	}

	apply := func(side string, flags []string) {
		for _, flag := range flags {
			switch strings.ToUpper(strings.TrimSpace(flag)) {
			case "IC":
				caseInsensitive = true
			case "":
				continue
			default:
				warnings = append(warnings, fmt.Sprintf("line %d: boundary modifier %s/%s is not supported; the boundary text is matched literally", fn.LineNumber, side, strings.TrimSpace(flag)))
			}
		}
	}

	var lbFlags, rbFlags []string
	lb, lbFlags = resolve("LB")
	rb, rbFlags = resolve("RB")
	apply("LB", lbFlags)
	apply("RB", rbFlags)
	return lb, rb, caseInsensitive, warnings
}

// extractorModifierWarnings surfaces registration modifiers that the DSL has
// no equivalent for, so fidelity loss is visible instead of silent.
func extractorModifierWarnings(fn ParsedFunction, paramName string) []string {
	var warnings []string
	for _, modifier := range []string{"RelFrameId"} {
		if value := firstNonEmptyArg(fn, modifier); value != "" {
			warnings = append(warnings, fmt.Sprintf("line %d: parameter %q uses %s=%s, which has no DSL equivalent; the full match is extracted unmodified", fn.LineNumber, paramName, modifier, cleanConfigValue(value)))
		}
	}
	return warnings
}

// encodeBoundaryExpression renders a boundary extractor expression. The
// simple left|||right form is kept whenever it round-trips safely; the
// structured config form carries ordinals, case-insensitive matching, and
// boundary text containing the legacy separator.
func encodeBoundaryExpression(left, right string, ordinal int, caseInsensitive bool) string {
	if ordinal <= 1 && !caseInsensitive && !strings.Contains(left, "|||") && !strings.Contains(right, "|||") {
		return left + "|||" + right
	}
	config := boundaryExpressionConfig{Left: left, Right: right, CaseInsensitive: caseInsensitive}
	if ordinal > 1 {
		config.Ordinal = ordinal
	}
	payload, err := json.Marshal(config)
	if err != nil {
		return left + "|||" + right
	}
	return boundaryExpressionConfigPrefix + string(payload)
}

func convertGlobalVerification(fn ParsedFunction) (*dsl.ContentCheckConfig, string) {
	text, flags := sourceFlaggedArgument(fn, "Text")
	if text == "" {
		text = fn.StepName
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Sprintf("line %d: web_global_verification has no Text argument", fn.LineNumber)
	}
	failIfFound := strings.EqualFold(strings.TrimSpace(firstNonEmptyArg(fn, "Fail")), "Found")
	if strings.TrimSpace(firstNonEmptyArg(fn, "Fail")) == "" {
		failIfFound = true
	}
	return &dsl.ContentCheckConfig{
		Text:            convertParamReferences(text),
		Scope:           normalizeSourceContentCheckScope(convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "Search"), firstNonEmptyArg(fn, "Scope"), "any_response")),
		FailIfFound:     &failIfFound,
		CaseInsensitive: sourceFlagsInclude(flags, "IC"),
	}, ""
}

func normalizeSourceContentCheckScope(value string) string {
	scope := normalizeEnum(value)
	switch scope {
	case "", "all", "any", "body", "header", "headers", "response", "global", "any_response":
		return "any_response"
	case "main", "page", "document", "html", "main_document":
		return "main_document"
	case "resource", "resources", "embedded_resource", "embedded_resources":
		return "resource"
	default:
		if strings.Contains(scope, "resource") {
			return "resource"
		}
		if strings.Contains(scope, "document") || strings.Contains(scope, "main") {
			return "main_document"
		}
		return "any_response"
	}
}

func convertFindAssertion(fn ParsedFunction) (*dsl.Assertion, *dsl.Extractor, SourceFunctionSupportMode, string) {
	assertion, extractor, mode, warning, _ := convertFindAssertionWithFindings(fn)
	return assertion, extractor, mode, warning
}

// convertFindAssertionWithFindings converts a text check and returns the
// review findings for the parts APEX cannot check, which attach to the request
// the check applies to.
func convertFindAssertionWithFindings(fn ParsedFunction) (*dsl.Assertion, *dsl.Extractor, SourceFunctionSupportMode, string, []importreview.Finding) {
	// Text may carry modifier flags (Text/IC= is case-insensitive).
	text, flags := sourceFlaggedArgument(fn, "Text")
	if text == "" {
		finding := promotionFinding(fn, textCheckUnsupportedReasonCode,
			fmt.Sprintf("%s %q has no Text argument (prefix/suffix checks are not expressible yet), so the check is not executed.", fn.Name, fn.StepName))
		return nil, nil, SourceFunctionSupportReviewOnly, fmt.Sprintf("line %d: web_reg_find '%s' — manual review recommended because no Text argument was found", fn.LineNumber, fn.StepName), []importreview.Finding{finding}
	}

	mode := SourceFunctionSupportExecutable
	searchScope := strings.ToLower(strings.TrimSpace(firstNonEmptyArg(fn, "Search")))
	warnings := make([]string, 0, 2)
	caseInsensitive := false
	for _, flag := range flags {
		if strings.EqualFold(strings.TrimSpace(flag), "IC") {
			caseInsensitive = true
			continue
		}
		// DIG, ALNUM and BIN change what the text matches (wildcards or
		// binary escapes); checking it literally would fail where
		// LoadRunner passes, so no check is emitted.
		finding := promotionFinding(fn, textCheckUnsupportedReasonCode,
			fmt.Sprintf("%s %q uses Text/%s, which is not expressible yet, so the check is not executed.", fn.Name, fn.StepName, strings.TrimSpace(flag)))
		return nil, nil, SourceFunctionSupportReviewOnly, fmt.Sprintf("line %d: web_reg_find Text/%s has no DSL equivalent; the check was not imported and requires review", fn.LineNumber, strings.TrimSpace(flag)), []importreview.Finding{finding}
	}
	var findings []importreview.Finding
	headersOnly := searchScope == "headers" || searchScope == "header"
	if searchScope == "all" {
		// LoadRunner's ALL scope spans the main response body, response headers,
		// and downloaded resources. APEX extractors can scan one response surface
		// at a time and resource responses are not available to the parent request
		// extractor. Preserve the useful body count, but make the scope loss an
		// explicit review gate instead of claiming exact execution.
		mode = SourceFunctionSupportReviewOnly
		warnings = append(warnings, fmt.Sprintf("line %d: web_reg_find Search=ALL is replayed against the main response body only; response headers and downloaded resources require review", fn.LineNumber))
		findings = append(findings, promotionFinding(fn, textCheckNarrowedReasonCode,
			fmt.Sprintf("%s %q searches headers, body and resources (Search=ALL); APEX checks the main response body only.", fn.Name, fn.StepName)))
	}

	var assertion *dsl.Assertion
	failMode := strings.ToLower(firstNonEmptyArg(fn, "Fail"))
	saveCount := firstNonEmptyArg(fn, "SaveCount")
	// OpenText semantics are special when SaveCount is present: omitting Fail
	// makes this a non-failing count operation. Adding the default
	// body_contains assertion here caused the request to fail before imported C
	// code could inspect a zero count.
	if headersOnly && (saveCount == "" || failMode != "") {
		// A body assertion would check the wrong surface; skip it until a
		// headers assertion exists, and say so.
		mode = SourceFunctionSupportReviewOnly
		warnings = append(warnings, fmt.Sprintf("line %d: web_reg_find Search=Headers is not checked yet; the check was not imported and requires review", fn.LineNumber))
		findings = append(findings, promotionFinding(fn, textCheckScopeReasonCode,
			fmt.Sprintf("%s %q checks the response headers (Search=Headers); APEX cannot check headers for text yet, so the check is not executed.", fn.Name, fn.StepName)))
	} else if saveCount == "" || failMode != "" {
		assertionType := "body_contains"
		if failMode == "found" {
			assertionType = "body_not_contains"
		}
		assertion = &dsl.Assertion{
			Type:            assertionType,
			Value:           convertParamReferences(text),
			CaseInsensitive: caseInsensitive,
		}
	}

	var saveCountExtractor *dsl.Extractor
	if saveCount != "" {
		if name := canonicalSourceParameterName(saveCount); name != "" {
			saveCountExtractor = &dsl.Extractor{
				Name:       name,
				Source:     sourceExtractorSource(firstNonEmptyArg(fn, "Search")),
				Type:       "regex_count",
				Expression: caseInsensitiveRegexPrefix(caseInsensitive) + regexp.QuoteMeta(convertParamReferences(text)),
			}
		}
	}

	return assertion, saveCountExtractor, mode, strings.Join(warnings, "; "), findings
}

func sourceExtractorSource(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "headers", "header":
		return "header"
	default:
		return "body"
	}
}

func encodeRegexOrdinalExpression(expression string, ordinal int) string {
	payload, err := json.Marshal(regexOrdinalExpressionConfig{Expression: expression, Ordinal: ordinal})
	if err != nil {
		return expression
	}
	return regexOrdinalExpressionConfigPrefix + string(payload)
}

// rawSourceStringContent decodes text captured from raw C source between a
// literal's quotes (for example by a regular expression over a condition).
// Parser arguments (Args, RawArgs, CallArgs values, Body) are already decoded
// exactly once by cleanConfigValue and must never pass through it again: a
// second decode strips escaped-JSON boundaries such as userAccountNumber\":\".
func rawSourceStringContent(value string) string {
	decoded, ok := decodeCStringContent(value)
	if !ok {
		return value
	}
	return decoded
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(cleanConfigValue(value)))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func firstNonEmptyArg(fn ParsedFunction, key string) string {
	if value := fn.Args[key]; value != "" {
		return value
	}

	// Source scripts are inconsistent about attribute-key casing (SaveCount vs
	// Savecount, NotFound=Warning vs warning), so fall back to a
	// case-insensitive scan before giving up.
	for name, value := range fn.Args {
		if value != "" && strings.EqualFold(name, key) {
			return value
		}
	}

	prefix := key + "="
	for _, raw := range fn.RawArgs {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimPrefix(raw, prefix)
		}
		if len(raw) >= len(prefix) && strings.EqualFold(raw[:len(prefix)], prefix) {
			return raw[len(prefix):]
		}
	}

	return ""
}

func sourceFlaggedArgument(fn ParsedFunction, key string) (string, []string) {
	// Leading and trailing whitespace in check text is significant.
	if value, flags, ok := untrimmedSourceArgument(fn, key); ok && strings.TrimSpace(value) != "" {
		return value, flags
	}
	if value := firstNonEmptyArg(fn, key); value != "" {
		return value, nil
	}
	for name, value := range fn.Args {
		if strings.TrimSpace(value) == "" {
			continue
		}
		base, flags, hasFlags := strings.Cut(name, "/")
		if !hasFlags || !strings.EqualFold(base, key) {
			continue
		}
		return value, strings.Split(flags, "/")
	}
	prefix := strings.ToLower(key) + "/"
	for _, raw := range fn.RawArgs {
		argKey, value, ok := strings.Cut(raw, "=")
		if !ok || !strings.HasPrefix(strings.ToLower(argKey), prefix) {
			continue
		}
		_, flags, _ := strings.Cut(argKey, "/")
		return value, strings.Split(flags, "/")
	}
	return "", nil
}

func sourceFlagsInclude(flags []string, expected string) bool {
	for _, flag := range flags {
		if strings.EqualFold(strings.TrimSpace(flag), expected) {
			return true
		}
	}
	return false
}

func caseInsensitiveRegexPrefix(caseInsensitive bool) string {
	if caseInsensitive {
		return "(?i)"
	}
	return ""
}
