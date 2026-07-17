package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var cliEnvPattern = regexp.MustCompile(`\{\{\s*env\.([^{}\s]+)\s*\}\}`)

type uploadScriptWrapper struct {
	Script *dsl.Script `json:"script"`
}

func loadScriptForRun(path string, args runArgs) (*dsl.Script, error) {
	script, err := dsl.ParseFile(path)
	if err != nil {
		return nil, err
	}
	return applyRuntimeOverrides(script, args)
}

func loadScriptSnapshotForRun(path string, data []byte, args runArgs) (*dsl.Script, error) {
	script, err := dsl.ParseFileSnapshot(path, data)
	if err != nil {
		return nil, err
	}
	return applyRuntimeOverrides(script, args)
}

func applyRuntimeOverrides(script *dsl.Script, args runArgs) (*dsl.Script, error) {
	if script == nil {
		return nil, fmt.Errorf("script is required")
	}
	cloned, err := cloneScript(script)
	if err != nil {
		return nil, err
	}
	if len(args.env) > 0 {
		payload, err := json.Marshal(cloned)
		if err != nil {
			return nil, fmt.Errorf("marshal script for overrides: %w", err)
		}
		var generic any
		if err := json.Unmarshal(payload, &generic); err != nil {
			return nil, fmt.Errorf("prepare script overrides: %w", err)
		}
		generic = replaceEnvTemplates(generic, args.env)
		payload, err = json.Marshal(generic)
		if err != nil {
			return nil, fmt.Errorf("marshal overridden script: %w", err)
		}
		var overridden dsl.Script
		if err := json.Unmarshal(payload, &overridden); err != nil {
			return nil, fmt.Errorf("decode overridden script: %w", err)
		}
		cloned = &overridden
	}
	if args.baseURL != "" {
		cloned.Config.BaseURL = args.baseURL
	}
	return cloned, nil
}

func cloneScript(script *dsl.Script) (*dsl.Script, error) {
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, fmt.Errorf("clone script: %w", err)
	}
	var cloned dsl.Script
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("clone script: %w", err)
	}
	return &cloned, nil
}

func replaceEnvTemplates(value any, overrides map[string]string) any {
	switch typed := value.(type) {
	case string:
		return cliEnvPattern.ReplaceAllStringFunc(typed, func(token string) string {
			match := cliEnvPattern.FindStringSubmatch(token)
			if len(match) != 2 {
				return token
			}
			key := strings.TrimSpace(match[1])
			if replacement, ok := overrides[key]; ok {
				return replacement
			}
			return token
		})
	case []any:
		resolved := make([]any, len(typed))
		for i := range typed {
			resolved[i] = replaceEnvTemplates(typed[i], overrides)
		}
		return resolved
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved[key] = replaceEnvTemplates(item, overrides)
		}
		return resolved
	default:
		return value
	}
}

func marshalScriptForUpload(script *dsl.Script) ([]byte, error) {
	payload, err := json.Marshal(uploadScriptWrapper{Script: script})
	if err != nil {
		return nil, fmt.Errorf("marshal upload script: %w", err)
	}
	return payload, nil
}

func parseEnvAssignment(raw string) (string, string, bool) {
	key, value, ok := strings.Cut(raw, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", false
	}
	return key, value, true
}
