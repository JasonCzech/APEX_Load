package trueclient

// xml.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	filepath "path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

func (argument *truArgument) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	argument.Value = truArgumentValueString(raw["value"])
	argument.Param, _ = raw["param"].(bool)
	argument.EvalJavaScript, _ = raw["evalJavaScript"].(bool)
	if argument.Param {
		name := strings.TrimSpace(trimQuotedValue(argument.Value))
		name = strings.TrimPrefix(strings.TrimSuffix(name, "}"), "{")
		if !truParameterNamePattern.MatchString(name) {
			return fmt.Errorf("TruClient parameter argument requires a parameter name")
		}
		canonical := strings.ToLower(name)
		canonical = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
				return r
			}
			return '_'
		}, canonical)
		for strings.Contains(canonical, "__") {
			canonical = strings.ReplaceAll(canonical, "__", "_")
		}
		canonical = strings.Trim(canonical, "_")
		if canonical == "" {
			return fmt.Errorf("TruClient parameter argument requires a parameter name")
		}
		argument.Value = "{{param." + canonical + "}}"
	}
	return nil
}

func truArgumentValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		if encoded, err := json.Marshal(typed); err == nil {
			return string(encoded)
		}
		return fmt.Sprint(typed)
	}
}

func findDefaultXML(files map[string]string) (string, string, bool) {
	names := make([]string, 0)
	for name := range files {
		if strings.EqualFold(filepath.Base(name), "default.xml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0], files[names[0]], true
	}
	return "", "", false
}

func decodeXMLContent(content string) string {
	data := []byte(content)
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		data = data[2:]
	}
	if !strings.ContainsRune(content, '\x00') {
		return content
	}
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	words := make([]uint16, 0, len(data)/2)
	for index := 0; index+1 < len(data); index += 2 {
		words = append(words, binary.LittleEndian.Uint16(data[index:index+2]))
	}
	return string(utf16.Decode(words))
}

func normalizeXMLDeclaration(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "<?xml") {
		return content
	}
	content = strings.Replace(content, `encoding="utf-16"`, `encoding="utf-8"`, 1)
	content = strings.Replace(content, `encoding="UTF-16"`, `encoding="utf-8"`, 1)
	content = strings.Replace(content, `encoding='utf-16'`, `encoding='utf-8'`, 1)
	content = strings.Replace(content, `encoding='UTF-16'`, `encoding='utf-8'`, 1)
	return content
}

func flattenCandidateSteps(steps []truXMLStep) []truXMLStep {
	flattened := make([]truXMLStep, 0)
	var visit func(step truXMLStep)
	visit = func(step truXMLStep) {
		if step.Disabled {
			flattened = append(flattened, step)
			return
		}
		if strings.EqualFold(strings.TrimSpace(step.Type), "alternative") {
			flattened = append(flattened, step)
			return
		}
		action := strings.TrimSpace(step.Action)
		if action != "" && !isStructuralAction(action) {
			if isControlAction(action) {
				copy := step
				copy.Steps = flattenCandidateSteps(step.Steps)
				flattened = append(flattened, copy)
				return
			}
			copy := step
			copy.Steps = nil
			flattened = append(flattened, copy)
		}
		for _, child := range step.Steps {
			visit(child)
		}
	}
	for _, step := range steps {
		visit(step)
	}
	return flattened
}

func isStructuralAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "action", "topstep", "init block", "run block", "end block", "handlers", "block", "default", "call action":
		return true
	default:
		return false
	}
}

func parseArgumentValue(raw string, key string) (string, error) {
	values, err := parseArgumentValues(raw)
	if err != nil {
		return "", err
	}
	return values[key].Value, nil
}

func parseFirstArgumentValue(raw string, keys ...string) (string, error) {
	values, err := parseArgumentValues(raw)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if value, ok := values[key]; ok && strings.TrimSpace(value.Value) != "" {
			return value.Value, nil
		}
		for existingKey, existingValue := range values {
			if strings.EqualFold(existingKey, key) && strings.TrimSpace(existingValue.Value) != "" {
				return existingValue.Value, nil
			}
		}
	}
	return "", nil
}

func parseArgumentValues(raw string) (map[string]truArgument, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]truArgument{}, nil
	}
	values := make(map[string]truArgument)
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func trimQuotedValue(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "\"")
	return strings.TrimSpace(trimmed)
}
