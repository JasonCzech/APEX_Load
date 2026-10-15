package bruno

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// bruPlainVarsBlockPattern is a .bru environment's vars block, not its
// vars:secret list, whose values Bruno keeps out of the file.
var bruPlainVarsBlockPattern = regexp.MustCompile(`(?ms)\bvars\s*\{(.*?)\}`)

// environmentVariableValues reads the values an environment file defines, so
// a review can offer them for the variables a collection leaves unset. Only
// plain values cross: a variable marked secret or disabled, a value the
// import scrubbed (a {{secret.*}} reference or the redaction marker), and a
// value built from other templates are left out. A .env file feeds
// process.env, typically with credentials, so none of its values are read.
func environmentVariableValues(data []byte, extension string) map[string]string {
	values := map[string]string{}
	add := func(name, value string) {
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if name == "" || value == "" || strings.Contains(value, "{{") || containsBrunoCredentialRedactionSentinel(value) {
			return
		}
		values[name] = value
	}
	switch extension {
	case ".env":
		return nil
	case ".bru":
		for _, block := range bruPlainVarsBlockPattern.FindAllSubmatch(data, -1) {
			if len(block) < 2 {
				continue
			}
			for _, field := range bruFieldPattern.FindAllSubmatch(block[1], -1) {
				if len(field) >= 3 {
					add(string(field[1]), string(field[2]))
				}
			}
		}
	default:
		var document any
		if yaml.Unmarshal(data, &document) != nil {
			return nil
		}
		var walk func(any)
		walk = func(current any) {
			switch typed := current.(type) {
			case map[string]any:
				name := anyString(typed["name"])
				raw, hasValue := typed["value"]
				secret, _ := typed["secret"].(bool)
				enabled, hasEnabled := typed["enabled"].(bool)
				if name != "" && hasValue && !secret && (!hasEnabled || enabled) {
					switch value := raw.(type) {
					case string:
						add(name, value)
					case int, int64, float64, bool:
						add(name, fmt.Sprint(value))
					}
				}
				for _, child := range typed {
					walk(child)
				}
			case []any:
				for _, child := range typed {
					walk(child)
				}
			}
		}
		walk(document)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}
