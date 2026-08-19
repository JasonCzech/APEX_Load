package sharedvalue

import (
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// field is one addressable string on a request surface, paired with the writer
// that puts a rewritten value back. Detect and Apply both go through this so a
// surface reachable for detection is always reachable for replacement.
type field struct {
	path  string
	name  string
	value string
	set   func(string)
}

// surfaceFields enumerates the template-bearing strings of one surface,
// covering both representations dslwalk yields: a typed *dsl.Action, and an
// http_sequence child stored in its canonical JSON map form.
func surfaceFields(ref dslwalk.SurfaceRef) []field {
	if ref.Action != nil {
		return actionFields(ref.Path, ref.Action)
	}
	if ref.Request != nil {
		return requestFields(ref.Path, ref.Request)
	}
	return nil
}

func actionFields(path string, action *dsl.Action) []field {
	fields := make([]field, 0, 4+len(action.Headers))
	if action.Target != "" {
		fields = append(fields, field{path: path, name: FieldTarget, value: action.Target,
			set: func(next string) { action.Target = next }})
	}
	if action.Body != "" {
		fields = append(fields, field{path: path, name: FieldBody, value: action.Body,
			set: func(next string) { action.Body = next }})
	}
	if action.Value != "" {
		fields = append(fields, field{path: path, name: FieldValue, value: action.Value,
			set: func(next string) { action.Value = next }})
	}
	for _, key := range sortedKeys(action.Headers) {
		key := key
		fields = append(fields, field{path: path, name: FieldHeaderPrefix + key, value: action.Headers[key],
			set: func(next string) { action.Headers[key] = next }})
	}
	// HeaderList preserves authored order/duplicates; rewrite it alongside the
	// legacy map so the two do not drift apart after an apply.
	for i := range action.HeaderList {
		i := i
		fields = append(fields, field{path: path, name: FieldHeaderPrefix + action.HeaderList[i].Name,
			value: action.HeaderList[i].Value,
			set:   func(next string) { action.HeaderList[i].Value = next }})
	}
	for _, key := range sortedKeys(action.Params) {
		key := key
		text, ok := action.Params[key].(string)
		if !ok || text == "" {
			continue
		}
		fields = append(fields, field{path: path, name: FieldParamPrefix + key, value: text,
			set: func(next string) { action.Params[key] = next }})
	}
	return fields
}

func requestFields(path string, request map[string]any) []field {
	fields := make([]field, 0, 3)
	for _, key := range []string{"target", "body"} {
		key := key
		text, ok := request[key].(string)
		if !ok || text == "" {
			continue
		}
		name := FieldTarget
		if key == "body" {
			name = FieldBody
		}
		fields = append(fields, field{path: path, name: name, value: text,
			set: func(next string) { request[key] = next }})
	}
	headers, ok := request["headers"].(map[string]any)
	if ok {
		for _, key := range sortedKeys(headers) {
			key := key
			text, isText := headers[key].(string)
			if !isText || text == "" {
				continue
			}
			fields = append(fields, field{path: path, name: FieldHeaderPrefix + key, value: text,
				set: func(next string) { headers[key] = next }})
		}
	}
	return fields
}

// walkFields visits every addressable string in a script in deterministic
// order, script-level config first so a base URL candidate always sorts ahead
// of the action targets that repeat it.
func walkFields(script *dsl.Script, visit func(field) bool) error {
	if script == nil {
		return nil
	}
	if strings.TrimSpace(script.Config.BaseURL) != "" {
		stop := !visit(field{path: "config", name: FieldBaseURL, value: script.Config.BaseURL,
			set: func(next string) { script.Config.BaseURL = next }})
		if stop {
			return nil
		}
	}
	stopped := false
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if stopped {
			return nil
		}
		for _, entry := range surfaceFields(ref) {
			if !visit(entry) {
				stopped = true
				return nil
			}
		}
		return nil
	})
	return err
}

func sortedKeys[V any](values map[string]V) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
