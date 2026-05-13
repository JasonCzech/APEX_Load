package dsl

import (
	"fmt"
	"strings"
)

// ValidateScriptIssues returns all validation issues for a script definition.
func ValidateScriptIssues(script *Script) []string {
	if script == nil {
		return []string{"script is required"}
	}

	if strings.HasPrefix(strings.TrimSpace(script.Protocol), "jms-") {
		return validateJMSScriptIssues(script)
	}

	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}
	if script.Protocol != "" && script.Protocol != "browser" && strings.TrimSpace(script.Config.BaseURL) == "" {
		issues = append(issues, "config.base_url is empty")
	}

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		if strings.TrimSpace(action.Type) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
			continue
		}

		if script.Protocol == "browser" {
			switch action.Type {
			case "browser_screenshot":
				continue
			case "browser_wait":
				if action.WaitFor == nil && action.Selector == "" && action.Target == "" {
					issues = append(issues, fmt.Sprintf("actions[%d] %q: browser_wait requires wait_for, selector, or target", i, action.Name))
				}
			default:
				if action.Target == "" && action.Selector == "" {
					issues = append(issues, fmt.Sprintf("actions[%d] %q: missing target or selector", i, action.Name))
				}
			}
			continue
		}

		if action.Target == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing target", i, action.Name))
		}
	}

	if script.Protocol == "soap" {
		issues = append(issues, validateSOAPScriptIssues(script)...)
	}

	return issues
}

// ValidateScriptError returns the first validation issue as an error.
func ValidateScriptError(script *Script) error {
	issues := ValidateScriptIssues(script)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s", issues[0])
}

func validateJMSScriptIssues(script *Script) []string {
	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}

	jms := script.Config.JMS
	if !jms.Mock {
		if jms.BrokerURL == "" {
			issues = append(issues, "config.jms.broker_url is required when mock mode is disabled")
		}
		if jms.MessageVPN == "" {
			issues = append(issues, "config.jms.message_vpn is recommended when mock mode is disabled")
		}
		issues = append(issues, "real Solace JMS adapter is not available in this build; enable config.jms.mock for replay")
	}
	if jms.Destination == "" {
		usesActionDestinations := true
		for _, action := range script.Actions {
			switch action.Type {
			case "jms_publish", "jms_consume", "jms_request_reply":
				if action.Target == "" {
					usesActionDestinations = false
				}
			}
		}
		if !usesActionDestinations {
			issues = append(issues, "config.jms.destination or per-action target is required")
		}
	}

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		switch action.Type {
		case "jms_publish", "jms_consume", "jms_request_reply", "jms_ack", "jms_commit", "jms_rollback":
		case "":
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
		default:
			issues = append(issues, fmt.Sprintf("actions[%d] %q: unsupported JMS action type %q", i, action.Name, action.Type))
		}
		if (action.Type == "jms_publish" || action.Type == "jms_consume" || action.Type == "jms_request_reply") && action.Target == "" && jms.Destination == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing destination", i, action.Name))
		}
		if action.Type == "jms_request_reply" && jms.ReplyTo == "" {
			replyTo, _ := action.Params["reply_to"].(string)
			if strings.TrimSpace(replyTo) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: missing reply_to destination", i, action.Name))
			}
		}
	}

	return issues
}

func validateSOAPScriptIssues(script *Script) []string {
	var issues []string

	if version := strings.TrimSpace(script.Config.SOAP.Version); version != "" && version != "1.1" && version != "1.2" {
		issues = append(issues, "config.soap.version must be 1.1 or 1.2")
	}

	if ws := script.Config.SOAP.WSSecurity; ws != nil {
		if token := ws.UsernameToken; token != nil {
			if strings.TrimSpace(token.Username) == "" {
				issues = append(issues, "config.soap.ws_security.username_token.username is required")
			}
			if strings.TrimSpace(token.Password) == "" {
				issues = append(issues, "config.soap.ws_security.username_token.password is required")
			}
			if passwordType := strings.TrimSpace(token.PasswordType); passwordType != "" && passwordType != "text" && passwordType != "digest" {
				issues = append(issues, "config.soap.ws_security.username_token.password_type must be text or digest")
			}
		}
		if timestamp := ws.Timestamp; timestamp != nil && timestamp.TTLSeconds < 0 {
			issues = append(issues, "config.soap.ws_security.timestamp.ttl_seconds must be zero or greater")
		}
	}

	for i, action := range script.Actions {
		if action.SOAP == nil {
			continue
		}
		if bodyMode := strings.TrimSpace(action.SOAP.BodyMode); bodyMode != "" && bodyMode != "raw_xml" && bodyMode != "generated" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: soap.body_mode must be raw_xml or generated", i, action.Name))
		}
	}

	return issues
}