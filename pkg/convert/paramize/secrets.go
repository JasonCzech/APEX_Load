package paramize

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Secret-bearing body fields, lifted from the sample importer's sanitization
// pass so all importers share one definition.
var (
	secretFieldNames         = `password|passcode|token|access[_-]?token|refresh[_-]?token|id[_-]?token|session[_-]?token|bearer[_-]?token|api[_-]?token|api[_-]?key|apikey|client[_-]?secret|secret|credential|credentials`
	jsonSecretPattern        = regexp.MustCompile(`(?i)("(` + secretFieldNames + `)"\s*:\s*")((?:[^"\\]|\\.)+)(")`)
	jsonNumericSecretPattern = regexp.MustCompile(`(?i)("(` + secretFieldNames + `)"\s*:\s*)(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)(\s*(?:[,}\]]|$))`)
	// The value class mirrors pkg/scrub's sensitiveParamPattern: quotes and pair
	// separators end the credential. A permissive class swallows the rest of a
	// multipart or XML body — the replacement drops the matched value, so every
	// unrelated byte after the credential is deleted from the sanitized body and
	// from the literal an accepted suggestion rewrites.
	formSecretPattern = regexp.MustCompile(`(?i)((?:^|[&\s])(` + secretFieldNames + `)=)([^&\s;#"']+)`)
	// Quoted pairs — XML/SOAP attributes and quoted form pairs — end at the
	// closing quote, which formSecretPattern's value class deliberately excludes,
	// so without these the credential would be left in the body untouched. RE2 has
	// no backreference, so each quote style is its own pattern (the same split
	// pkg/scrub uses for xmlNamespaceDouble/SingleAttr) rather than one ["']
	// class, which would end `token="it's"` at the apostrophe and corrupt the tail.
	quotedSecretRules = []quotedSecretRule{
		{quote: `"`, pattern: regexp.MustCompile(`(?i)((?:^|[&\s])(` + secretFieldNames + `)\s*=\s*")([^"\r\n]+)(")`)},
		{quote: `'`, pattern: regexp.MustCompile(`(?i)((?:^|[&\s])(` + secretFieldNames + `)\s*=\s*')([^'\r\n]+)(')`)},
	}
	secretTemplateEnvelope    = regexp.MustCompile(`^\{\{\s*([^{}\r\n]{1,256}?)\s*\}\}$`)
	secretTemplateReference   = regexp.MustCompile(`^(?:var|param|data|env|secret|file)\.[A-Za-z0-9_.-]+$`)
	authorizationSchemeSyntax = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._~+-]*$`)
)

// quotedSecretRule pairs a quoted-pair pattern with the quote byte that gates
// it, so bodies without that quote skip the scan (and its work budget) entirely.
type quotedSecretRule struct {
	quote   string
	pattern *regexp.Regexp
}

// SanitizeBodySecrets redacts known secret fields in JSON or form-encoded
// bodies, preserving values whose entire credential is already supplied by a
// governed template. Merely containing template syntax is insufficient because
// a value such as "{{var.password}}residual-secret" still carries a literal
// credential. Shared by the sample importer's sanitize option.
func SanitizeBodySecrets(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}
	result := jsonSecretPattern.ReplaceAllStringFunc(body, func(segment string) string {
		match := jsonSecretPattern.FindStringSubmatch(segment)
		if len(match) < 5 || isWholeSecretTemplate(match[3]) {
			return segment
		}
		return match[1] + "{{secret.redacted}}" + match[4]
	})
	result = jsonNumericSecretPattern.ReplaceAllStringFunc(result, func(segment string) string {
		match := jsonNumericSecretPattern.FindStringSubmatch(segment)
		if len(match) < 5 {
			return segment
		}
		return match[1] + `"{{secret.redacted}}"` + match[4]
	})
	if bodyLooksJSON(trimmed) {
		return result
	}
	result = formSecretPattern.ReplaceAllStringFunc(result, func(segment string) string {
		match := formSecretPattern.FindStringSubmatch(segment)
		if len(match) < 4 || isWholeSecretTemplate(match[3]) {
			return segment
		}
		return match[1] + "{{secret.redacted}}"
	})
	for _, rule := range quotedSecretRules {
		if !strings.Contains(result, rule.quote) {
			continue
		}
		pattern := rule.pattern
		result = pattern.ReplaceAllStringFunc(result, func(segment string) string {
			match := pattern.FindStringSubmatch(segment)
			if len(match) < 5 || isWholeSecretTemplate(match[3]) {
				return segment
			}
			return match[1] + "{{secret.redacted}}" + match[4]
		})
	}
	return result
}

// secretSuggestions proposes externalizing hardcoded credentials to
// {{env.KEY}}. Secrets are always high-risk: they never auto-apply, because
// the replacement only works once the operator supplies the value at runtime.
func secretSuggestions(script *dsl.Script) ([]Suggestion, error) {
	collector := newSecretCollector()
	budget := &suggestionRegexBudget{}
	var scanErr error
	forEachAction(script, func(section string, index int, action *dsl.Action) {
		if scanErr != nil {
			return
		}
		for _, header := range sensitiveActionHeaders(action) {
			headerKey, headerValue := header.name, header.value
			if isGovernedSecretHeaderValue(headerKey, headerValue) {
				continue
			}
			envKey := EnvKey(headerKey)
			replace := schemePreservingEnvTemplate(headerValue, envKey)
			collector.add(headerKey, headerValue, "header", Location{
				Section:     section,
				ActionIndex: index,
				ActionName:  action.Name,
				Field:       "headers." + headerKey,
				SearchText:  headerValue,
				ReplaceText: replace,
			}, replace)
		}
		scanErr = collectBodySecrets(collector, budget, section, index, action)
		if scanErr != nil {
			return
		}
		collectFillSecrets(collector, section, index, action)
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return collector.suggestions(), nil
}

func collectBodySecrets(collector *secretCollector, budget *suggestionRegexBudget, section string, index int, action *dsl.Action) error {
	if strings.TrimSpace(action.Body) == "" {
		return nil
	}
	jsonMatches, err := budget.findAllSubmatches(jsonSecretPattern, action.Body)
	if err != nil {
		return err
	}
	for _, match := range jsonMatches {
		if len(match) < 5 || isWholeSecretTemplate(match[3]) {
			continue
		}
		fieldName, value := match[2], match[3]
		envKey := EnvKey(fieldName)
		collector.add(fieldName, value, "body field", Location{
			Section:     section,
			ActionIndex: index,
			ActionName:  action.Name,
			Field:       "body",
			SearchText:  match[0],
			ReplaceText: match[1] + "{{env." + envKey + "}}" + match[4],
		}, "{{env."+envKey+"}}")
	}
	numericMatches, err := budget.findAllSubmatches(jsonNumericSecretPattern, action.Body)
	if err != nil {
		return err
	}
	for _, match := range numericMatches {
		if len(match) < 5 {
			continue
		}
		fieldName, value := match[2], match[3]
		envKey := EnvKey(fieldName)
		replacement := "{{env." + envKey + "}}"
		collector.add(fieldName, value, "body field", Location{
			Section:     section,
			ActionIndex: index,
			ActionName:  action.Name,
			Field:       "body",
			SearchText:  match[0],
			ReplaceText: match[1] + `"` + replacement + `"` + match[4],
		}, replacement)
	}
	if bodyLooksJSON(strings.TrimSpace(action.Body)) {
		return nil
	}
	formMatches, err := budget.findAllSubmatches(formSecretPattern, action.Body)
	if err != nil {
		return err
	}
	for _, match := range formMatches {
		if len(match) < 4 || isWholeSecretTemplate(match[3]) {
			continue
		}
		fieldName, value := match[2], match[3]
		envKey := EnvKey(fieldName)
		collector.add(fieldName, value, "form field", Location{
			Section:     section,
			ActionIndex: index,
			ActionName:  action.Name,
			Field:       "body",
			SearchText:  match[0],
			ReplaceText: match[1] + "{{env." + envKey + "}}",
		}, "{{env."+envKey+"}}")
	}
	for _, rule := range quotedSecretRules {
		if !strings.Contains(action.Body, rule.quote) {
			continue
		}
		quotedMatches, err := budget.findAllSubmatches(rule.pattern, action.Body)
		if err != nil {
			return err
		}
		for _, match := range quotedMatches {
			if len(match) < 5 || isWholeSecretTemplate(match[3]) {
				continue
			}
			fieldName, value := match[2], match[3]
			envKey := EnvKey(fieldName)
			collector.add(fieldName, value, "form field", Location{
				Section:     section,
				ActionIndex: index,
				ActionName:  action.Name,
				Field:       "body",
				SearchText:  match[0],
				ReplaceText: match[1] + "{{env." + envKey + "}}" + match[4],
			}, "{{env."+envKey+"}}")
		}
	}
	return nil
}

// bodyLooksJSON intentionally uses the leading token instead of requiring a
// successful unmarshal. Import-time secret handling must fail closed for a
// partially-authored JSON document too: treating malformed JSON as a form body
// can consume quotes, commas, and closing braces as credential bytes.
func bodyLooksJSON(trimmed string) bool {
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// collectFillSecrets flags browser fill actions targeting password-like inputs.
func collectFillSecrets(collector *secretCollector, section string, index int, action *dsl.Action) {
	if action.Type != "browser_fill" || strings.TrimSpace(action.Value) == "" || isWholeSecretTemplate(action.Value) {
		return
	}
	hint := SelectorFieldHint(action.Selector)
	if hint == "" || !strings.Contains(strings.ToLower(hint), "password") {
		return
	}
	envKey := EnvKey(hint)
	collector.add(hint, action.Value, "browser fill", Location{
		Section:     section,
		ActionIndex: index,
		ActionName:  action.Name,
		Field:       "value",
		SearchText:  action.Value,
		ReplaceText: "{{env." + envKey + "}}",
	}, "{{env."+envKey+"}}")
}

type actionHeader struct {
	name  string
	value string
}

// sensitiveActionHeaders lists the credential headers of an action.
func sensitiveActionHeaders(action *dsl.Action) []actionHeader {
	return actionHeaders(action, func(name, _ string) bool { return SensitiveHeaderKey(name) })
}

// actionHeaders lists an action's non-empty headers that keep accepts (all of
// them when keep is nil). It scans both representations. HeaderList is the
// authoritative wire representation and can carry duplicate values that the
// compatibility map omits; the map can still contain a value when an older
// producer did not populate HeaderList.
func actionHeaders(action *dsl.Action, keep func(name, value string) bool) []actionHeader {
	if action == nil {
		return nil
	}
	headers := make([]actionHeader, 0, len(action.Headers)+len(action.HeaderList))
	seen := make(map[string]struct{}, len(action.Headers)+len(action.HeaderList))
	headerListNames := make(map[string]struct{}, len(action.HeaderList))
	for _, header := range action.HeaderList {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		if name != "" {
			headerListNames[name] = struct{}{}
		}
	}
	add := func(name, value string) {
		if strings.TrimSpace(value) == "" || (keep != nil && !keep(name, value)) {
			return
		}
		identity := strings.ToLower(strings.TrimSpace(name)) + "\x00" + value
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		headers = append(headers, actionHeader{name: name, value: value})
	}

	keys := make([]string, 0, len(action.Headers))
	for name := range action.Headers {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if _, shadowed := headerListNames[strings.ToLower(strings.TrimSpace(name))]; shadowed {
			continue
		}
		add(name, action.Headers[name])
	}
	for _, header := range action.HeaderList {
		add(header.Name, header.Value)
	}
	return headers
}

func isWholeSecretTemplate(value string) bool {
	return IsGovernedTemplate(value)
}

// IsGovernedTemplate reports whether the whole value is a runtime template
// that the engine can resolve without retaining literal credential material.
// Keeping this grammar in the shared parameterization package prevents the
// sanitizer and suggestion paths from disagreeing about supported builtins.
func IsGovernedTemplate(value string) bool {
	match := secretTemplateEnvelope.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 {
		return false
	}
	expression := strings.TrimSpace(match[1])
	if secretTemplateReference.MatchString(expression) {
		return true
	}
	switch expression {
	case "fn.uuid()", "fn.uuid", "fn.uuid.v4",
		"fake.uuid", "fake.uuid()", "fake.email", "fake.email()",
		"fn.timestamp.unix", "fn.timestamp.unix_ms",
		"fn.timestamp.iso8601", "fn.timestamp.rfc3339":
		return true
	}
	if arguments, ok := governedBuiltinArguments(expression, "fn.random"); ok {
		if len(arguments) != 2 {
			return false
		}
		minimum, err := strconv.Atoi(arguments[0])
		if err != nil {
			return false
		}
		maximum, err := strconv.Atoi(arguments[1])
		return err == nil && minimum <= maximum
	}
	if arguments, ok := governedBuiltinArguments(expression, "fn.random_string"); ok {
		if len(arguments) != 1 {
			return false
		}
		length, err := strconv.Atoi(arguments[0])
		return err == nil && length > 0 && length <= dsl.MaxGeneratedParameterBytes
	}
	return false
}

func governedBuiltinArguments(expression, name string) ([]string, bool) {
	prefix := name + "("
	if !strings.HasPrefix(expression, prefix) || !strings.HasSuffix(expression, ")") {
		return nil, false
	}
	raw := expression[len(prefix) : len(expression)-1]
	parts := strings.Split(raw, ",")
	arguments := make([]string, len(parts))
	for index, part := range parts {
		arguments[index] = strings.TrimSpace(part)
		if arguments[index] == "" {
			return nil, true
		}
	}
	return arguments, true
}

// isGovernedSecretHeaderValue exempts only values whose complete
// credential-bearing surface is templated. Authorization schemes and cookie
// names are non-secret syntax and may remain literal.
func isGovernedSecretHeaderValue(name, value string) bool {
	trimmed := strings.TrimSpace(value)
	if isWholeSecretTemplate(trimmed) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization":
		fields := strings.Fields(trimmed)
		return len(fields) == 2 &&
			authorizationSchemeSyntax.MatchString(fields[0]) &&
			isWholeSecretTemplate(fields[1])
	case "cookie", "set-cookie":
		parts := strings.Split(trimmed, ";")
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" || !isWholeSecretTemplate(pair[1]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// schemePreservingEnvTemplate keeps recognizable auth schemes visible so the
// runtime env value only needs to hold the credential itself.
func schemePreservingEnvTemplate(headerValue, envKey string) string {
	for _, scheme := range []string{"Bearer ", "Basic "} {
		if strings.HasPrefix(headerValue, scheme) {
			return scheme + "{{env." + envKey + "}}"
		}
	}
	return "{{env." + envKey + "}}"
}

// secretCollector groups occurrences of the same secret value under one
// suggestion so selecting it externalizes every occurrence at once.
type secretCollector struct {
	order   []string
	grouped map[string]*Suggestion
}

func newSecretCollector() *secretCollector {
	return &secretCollector{grouped: map[string]*Suggestion{}}
}

func (c *secretCollector) add(name, value, kind string, location Location, replacement string) {
	key := name + "|" + value
	if existing, ok := c.grouped[key]; ok {
		existing.Locations = append(existing.Locations, location)
		return
	}
	c.grouped[key] = &Suggestion{
		Kind:         KindSecret,
		VarName:      EnvKey(name),
		Replacement:  replacement,
		LiteralValue: value,
		SampleValue:  truncateValue(value),
		Locations:    []Location{location},
		Confidence:   0.9,
		RuleID:       "secret." + strings.ReplaceAll(kind, " ", "_"),
		Rationale: fmt.Sprintf(
			"The %s %q carries a credential. Externalize it to {{env.%s}} and supply the value at runtime; the script will not run until the environment variable is set.",
			kind, name, EnvKey(name)),
		HighRisk: true,
	}
	c.order = append(c.order, key)
}

func (c *secretCollector) suggestions() []Suggestion {
	out := make([]Suggestion, 0, len(c.order))
	for _, key := range c.order {
		out = append(out, *c.grouped[key])
	}
	return out
}
