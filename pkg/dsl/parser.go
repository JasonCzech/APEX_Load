package dsl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jasonczech/apexload/pkg/fileasset"
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
	if err := resolveDataSourcePaths(script, filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := ValidateScriptError(script); err != nil {
		return nil, err
	}
	return script, nil
}

func resolveDataSourcePaths(script *Script, baseDir string) error {
	if script == nil || baseDir == "" {
		return nil
	}
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("resolve script directory: %w", err)
	}
	if err := fileasset.RegisterRoot(baseDir); err != nil {
		return fmt.Errorf("register script asset directory: %w", err)
	}
	for i := range script.DataSources {
		path := script.DataSources[i].Path
		if path == "" || filepath.IsAbs(path) {
			continue
		}
		resolved, err := filepath.Abs(filepath.Join(baseDir, path))
		if err != nil {
			return fmt.Errorf("resolve data source %q: %w", path, err)
		}
		script.DataSources[i].Path = resolved
	}
	return nil
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
