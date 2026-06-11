package tmpl

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

var (
	jmeterFunctionPattern = regexp.MustCompile(`\$\{(__[A-Za-z][A-Za-z0-9_]*)(?:\(([^{}]*)\))?\}`)
	postmanDynamicPattern = regexp.MustCompile(`\{\{\s*(\$[A-Za-z][A-Za-z0-9_]*)\s*\}\}`)
)

// TranslateDynamic maps source-tool dynamic variables to APEX Load template
// functions when the runtime has an equivalent.
func TranslateDynamic(value string, c *diag.Collector, location string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	value = jmeterFunctionPattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := jmeterFunctionPattern.FindStringSubmatch(token)
		if len(matches) < 2 {
			return token
		}
		replacement, ok := translateJMeterFunction(matches[1], matches[2], c, location, token)
		if !ok {
			addWarning(c, "JMeter dynamic function", location, fmt.Sprintf("%s could not be translated automatically and was left intact", token))
			return token
		}
		return replacement
	})
	value = postmanDynamicPattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := postmanDynamicPattern.FindStringSubmatch(token)
		if len(matches) < 2 {
			return token
		}
		replacement, ok := translatePostmanDynamic(matches[1], c, location)
		if !ok {
			addWarning(c, "Postman dynamic variable", location, fmt.Sprintf("%s could not be translated automatically and was left intact", token))
			return token
		}
		return replacement
	})
	return value
}

func translateJMeterFunction(name string, rawArgs string, c *diag.Collector, location string, token string) (string, bool) {
	args := splitArgs(rawArgs)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "__uuid":
		return "{{fn.uuid()}}", true
	case "__time", "__timeshift":
		if len(args) > 0 && strings.TrimSpace(args[0]) != "" && strings.TrimSpace(args[0]) != "/1000" {
			addInfo(c, "JMeter dynamic function", location, token+" format was approximated with the ISO-8601 timestamp runtime function")
			return "{{fn.timestamp.iso8601}}", true
		}
		if len(args) > 0 && strings.TrimSpace(args[0]) == "/1000" {
			return "{{fn.timestamp.unix}}", true
		}
		return "{{fn.timestamp.unix_ms}}", true
	case "__random":
		if len(args) < 2 {
			return "{{fn.random(1,100)}}", true
		}
		min := cleanNumericArg(args[0], "1")
		max := cleanNumericArg(args[1], "100")
		return "{{fn.random(" + min + "," + max + ")}}", true
	case "__randomstring":
		length := "12"
		if len(args) > 0 {
			length = cleanNumericArg(args[0], "12")
		}
		return "{{fn.random_string(" + length + ")}}", true
	case "__counter":
		key := "jmeter_counter"
		if len(args) > 1 && strings.TrimSpace(args[1]) != "" {
			key = sanitizeName(args[1])
		}
		return "{{var." + key + "}}", true
	case "__threadnum":
		return "{{var.jmeter_thread_num}}", true
	default:
		return "", false
	}
}

func translatePostmanDynamic(name string, c *diag.Collector, location string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "$guid", "$uuid", "$randomuuid":
		return "{{fn.uuid()}}", true
	case "$timestamp":
		return "{{fn.timestamp.unix}}", true
	case "$isotimestamp":
		return "{{fn.timestamp.iso8601}}", true
	case "$randomint":
		return "{{fn.random(0,1000)}}", true
	case "$randomalphanumeric", "$randomalpha", "$randomword", "$randomstring":
		return "{{fn.random_string(12)}}", true
	default:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "$random") {
			addInfo(c, "Postman dynamic variable", location, name+" was approximated with a random string runtime function")
			return "{{fn.random_string(12)}}", true
		}
		return "", false
	}
}

func splitArgs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func cleanNumericArg(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if _, err := strconv.Atoi(value); err == nil {
		return value
	}
	return fallback
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "{}")
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, ".", "_")
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "jmeter_counter"
	}
	return value
}

func addWarning(c *diag.Collector, element, location, detail string) {
	if c != nil {
		c.Add(diag.Warning, element, location, detail)
	}
}

func addInfo(c *diag.Collector, element, location, detail string) {
	if c != nil {
		c.Add(diag.Info, element, location, detail)
	}
}
