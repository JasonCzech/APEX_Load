package dsl

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const NativeJavaScriptSourceBytes = 128 << 10
const NativeJavaScriptValueBytes = 64 << 10

type NativeJavaScriptAction struct {
	Source       string
	Inputs       map[string]any
	ResultPrefix string
	TimeoutMS    int
}

func ParseNativeJavaScriptAction(params map[string]any) (NativeJavaScriptAction, error) {
	a := NativeJavaScriptAction{TimeoutMS: 5000, Inputs: map[string]any{}}
	for key, value := range params {
		if value == nil {
			return a, fmt.Errorf("native_javascript parameters must not be null")
		}
		switch key {
		case "source":
			var ok bool
			a.Source, ok = value.(string)
			if !ok {
				return a, fmt.Errorf("native_javascript source must be a literal string")
			}
		case "inputs":
			var ok bool
			a.Inputs, ok = value.(map[string]any)
			if !ok {
				return a, fmt.Errorf("native_javascript inputs must be a JSON object")
			}
		case "result_prefix":
			var ok bool
			a.ResultPrefix, ok = value.(string)
			if !ok {
				return a, fmt.Errorf("native_javascript result_prefix must be a static string")
			}
		case "timeout_ms":
			encoded, err := json.Marshal(value)
			if err != nil || json.Unmarshal(encoded, &a.TimeoutMS) != nil {
				return a, fmt.Errorf("native_javascript timeout_ms must be an integer")
			}
		default:
			return a, fmt.Errorf("native_javascript parameter is unsupported")
		}
	}
	if a.Source == "" || !utf8.ValidString(a.Source) || len(a.Source) > NativeJavaScriptSourceBytes {
		return a, fmt.Errorf("native_javascript requires literal source of at most 128 KiB")
	}
	if a.ResultPrefix != "" && !livePrefixPattern.MatchString(a.ResultPrefix) {
		return a, fmt.Errorf("native_javascript result_prefix must be a static variable prefix")
	}
	if a.TimeoutMS < 100 || a.TimeoutMS > 120000 {
		return a, fmt.Errorf("native_javascript timeout_ms must be 100..120000")
	}
	budget := 4096
	if !boundedNativeJSON(a.Inputs, 0, &budget) {
		return a, fmt.Errorf("native_javascript inputs exceed JSON depth or entry limits")
	}
	encoded, err := json.Marshal(a.Inputs)
	if err != nil || len(encoded) > NativeJavaScriptValueBytes {
		return a, fmt.Errorf("native_javascript inputs must fit within 64 KiB")
	}
	return a, nil
}
func boundedNativeJSON(value any, depth int, budget *int) bool {
	*budget--
	if depth > 16 || *budget < 0 {
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		if len(value) > 1024 {
			return false
		}
		for key, child := range value {
			if len(key) > 4096 || !boundedNativeJSON(child, depth+1, budget) {
				return false
			}
		}
	case []any:
		if len(value) > 1024 {
			return false
		}
		for _, child := range value {
			if !boundedNativeJSON(child, depth+1, budget) {
				return false
			}
		}
	case nil, string, bool, float64, float32, int, int32, int64, uint, uint32, uint64, json.Number:
	default:
		return false
	}
	return true
}
func nativeServiceActionSpecs() []ActionSpec {
	return []ActionSpec{{Type: "native_javascript", Protocols: []string{"*"}, Summary: "Executes literal JavaScript in an isolated host worker with declared native data services.", ExecutionPosture: "supported", ExecutionNote: "Requires native_javascript_v1 on the execution host. Linux memory/CPU limits, four concurrent workers, bounded source and results. No filesystem, environment or network APIs; compose target actions in the surrounding DSL.", Fields: []FieldSpec{
		{Name: "params.source", Kind: FieldKindString, Required: true, Doc: "Literal JavaScript function body receiving input and apex. Never template-expanded. At most 128 KiB."},
		{Name: "params.inputs", Kind: FieldKindObject, Doc: "JSON object resolved once before execution, at most 64 KiB and depth 16. Guest strings remain literal."},
		{Name: "params.result_prefix", Kind: FieldKindString, Doc: "Static output variable prefix. Return a JSON object of at most 64 KiB and 128 fields; fields are stored beneath this prefix."},
		{Name: "params.timeout_ms", Kind: FieldKindInt, Maximum: 120000, Doc: "Whole-worker wall time, including service waits; default 5000 ms."},
	}, Validate: func(index int, action Action) []string {
		if _, err := ParseNativeJavaScriptAction(action.Params); err != nil {
			return []string{fmt.Sprintf("actions[%d]: %v", index, err)}
		}
		return nil
	}}}
}
