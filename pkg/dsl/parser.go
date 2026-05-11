package dsl

import (
	"encoding/json"
	"fmt"
	"os"
)

type scriptWrapper struct {
	Script Script `json:"script"`
}

func ParseFile(path string) (*Script, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read script file: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Script, error) {
	var wrapper scriptWrapper
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse script JSON: %w", err)
	}

	script := &wrapper.Script
	if err := validate(script); err != nil {
		return nil, err
	}

	return script, nil
}

func validate(s *Script) error {
	if s.Name == "" {
		return fmt.Errorf("script name is required")
	}
	if s.Protocol == "" {
		return fmt.Errorf("script protocol is required")
	}
	if len(s.Actions) == 0 {
		return fmt.Errorf("script must have at least one action")
	}
	for i, a := range s.Actions {
		if a.Name == "" {
			return fmt.Errorf("action[%d]: name is required", i)
		}
		if a.Type == "" {
			return fmt.Errorf("action[%d] %q: type is required", i, a.Name)
		}
		if s.Protocol == "browser" {
			switch a.Type {
			case "browser_screenshot":
				continue
			case "browser_wait":
				if a.WaitFor == nil && a.Selector == "" && a.Target == "" {
					return fmt.Errorf("action[%d] %q: browser_wait requires wait_for, selector, or target", i, a.Name)
				}
			default:
				if a.Target == "" && a.Selector == "" {
					return fmt.Errorf("action[%d] %q: target or selector is required", i, a.Name)
				}
			}
			continue
		}
		if a.Target == "" {
			return fmt.Errorf("action[%d] %q: target is required", i, a.Name)
		}
	}
	return nil
}
