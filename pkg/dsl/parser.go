package dsl

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
		switch {
		case s.Protocol == "browser":
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
		case strings.HasPrefix(s.Protocol, "jms-"):
			if err := validateJMSAction(s, a, i); err != nil {
				return err
			}
			continue
		}
		if a.Target == "" {
			return fmt.Errorf("action[%d] %q: target is required", i, a.Name)
		}
	}
	return nil
}

func validateJMSAction(s *Script, a Action, index int) error {
	switch a.Type {
	case "jms_publish", "jms_consume", "jms_request_reply", "jms_ack", "jms_commit", "jms_rollback":
	default:
		return fmt.Errorf("action[%d] %q: unsupported JMS action type %q", index, a.Name, a.Type)
	}

	if a.Type == "jms_ack" || a.Type == "jms_commit" || a.Type == "jms_rollback" {
		return nil
	}

	if a.Target == "" && s.Config.JMS.Destination == "" {
		return fmt.Errorf("action[%d] %q: target or config.jms.destination is required", index, a.Name)
	}
	if a.Type == "jms_request_reply" && s.Config.JMS.ReplyTo == "" {
		if replyTo, _ := a.Params["reply_to"].(string); strings.TrimSpace(replyTo) == "" {
			return fmt.Errorf("action[%d] %q: config.jms.reply_to or params.reply_to is required", index, a.Name)
		}
	}
	return nil
}
