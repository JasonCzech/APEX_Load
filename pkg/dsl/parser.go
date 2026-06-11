package dsl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type scriptWrapper struct {
	Script Script `json:"script"`
}

func ParseFile(path string) (*Script, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read script file: %w", err)
	}
	script, err := parseScript(data)
	if err != nil {
		return nil, err
	}
	if err := ResolveBodyRefs(script, filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := ValidateScriptError(script); err != nil {
		return nil, err
	}
	return script, nil
}

func Parse(data []byte) (*Script, error) {
	script, err := parseScript(data)
	if err != nil {
		return nil, err
	}
	if err := ValidateScriptError(script); err != nil {
		return nil, err
	}

	return script, nil
}

func parseScript(data []byte) (*Script, error) {
	var wrapper scriptWrapper
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse script JSON: %w", err)
	}

	script := &wrapper.Script
	if wrapper.Script.Name == "" && wrapper.Script.Protocol == "" && len(wrapper.Script.Actions) == 0 {
		var direct Script
		if err := json.Unmarshal(data, &direct); err != nil {
			return nil, fmt.Errorf("parse script JSON: %w", err)
		}
		script = &direct
	}

	return script, nil
}
