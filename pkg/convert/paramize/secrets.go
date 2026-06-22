package paramize

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Secret-bearing body fields, lifted from the sample importer's sanitization
// pass so all importers share one definition.
var (
	secretFieldNames  = `password|passcode|token|access[_-]?token|refresh[_-]?token|id[_-]?token|session[_-]?token|bearer[_-]?token|api[_-]?token|api[_-]?key|apikey|client[_-]?secret|secret|credential|credentials`
	jsonSecretPattern = regexp.MustCompile(`(?i)("(` + secretFieldNames + `)"\s*:\s*")([^"]+)(")`)
	formSecretPattern = regexp.MustCompile(`(?i)((?:^|[&\s])(` + secretFieldNames + `)=)([^&\s]+)`)
)

// SanitizeBodySecrets redacts known secret fields in JSON or form-encoded
// bodies, preserving values that are already templated. Shared by the sample
// importer's sanitize option.
func SanitizeBodySecrets(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}
	result := jsonSecretPattern.ReplaceAllStringFunc(body, func(segment string) string {
		match := jsonSecretPattern.FindStringSubmatch(segment)
		if len(match) < 5 || isTemplated(match[3]) {
			return segment
		}
		return match[1] + "{{secret.redacted}}" + match[4]
	})
	result = formSecretPattern.ReplaceAllStringFunc(result, func(segment string) string {
		match := formSecretPattern.FindStringSubmatch(segment)
		if len(match) < 4 || isTemplated(match[3]) {
			return segment
		}
		return match[1] + "{{secret.redacted}}"
	})
	return result
}

// secretSuggestions proposes externalizing hardcoded credentials to
// {{env.KEY}}. Secrets are always high-risk: they never auto-apply, because
// the replacement only works once the operator supplies the value at runtime.
func secretSuggestions(script *dsl.Script) []Suggestion {
	collector := newSecretCollector()
	forEachAction(script, func(section string, index int, action *dsl.Action) {
		for headerKey, headerValue := range action.Headers {
			if !SensitiveHeaderKey(headerKey) || strings.TrimSpace(headerValue) == "" || isTemplated(headerValue) {
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
		collectBodySecrets(collector, section, index, action)
		collectFillSecrets(collector, section, index, action)
	})
	return collector.suggestions()
}

func collectBodySecrets(collector *secretCollector, section string, index int, action *dsl.Action) {
	if strings.TrimSpace(action.Body) == "" {
		return
	}
	for _, match := range jsonSecretPattern.FindAllStringSubmatch(action.Body, -1) {
		if len(match) < 5 || isTemplated(match[3]) {
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
	for _, match := range formSecretPattern.FindAllStringSubmatch(action.Body, -1) {
		if len(match) < 4 || isTemplated(match[3]) {
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
}

// collectFillSecrets flags browser fill actions targeting password-like inputs.
func collectFillSecrets(collector *secretCollector, section string, index int, action *dsl.Action) {
	if action.Type != "browser_fill" || strings.TrimSpace(action.Value) == "" || isTemplated(action.Value) {
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
