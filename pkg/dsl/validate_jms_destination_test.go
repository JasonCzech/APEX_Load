package dsl

import (
	"strings"
	"testing"
)

func issuesContain(issues []string, substr string) bool {
	for _, is := range issues {
		if strings.Contains(is, substr) {
			return true
		}
	}
	return false
}

// TestValidateJMSParamsDestinationSatisfiesRequirement covers dsl-catalog-1: the
// catalog documents params.destination as an alternative to target, and the
// runtime resolves it, so a script that sets only params.destination must
// validate clean (no false "missing destination").
func TestValidateJMSParamsDestinationSatisfiesRequirement(t *testing.T) {
	for _, actionType := range []string{"jms_publish", "jms_consume", "jms_request_reply", "jms_browse"} {
		t.Run(actionType, func(t *testing.T) {
			action := Action{Name: "a", Type: actionType, Params: map[string]any{"destination": "orders.in"}}
			if actionType == "jms_request_reply" {
				action.Params["reply_to"] = "orders.reply"
			}
			if actionType == "jms_publish" {
				action.Body = "{}"
			}
			script := &Script{
				Name:     "jms-paramdest",
				Protocol: "jms-solace",
				Config:   ScriptConfig{JMS: JMSConfig{Provider: "solace", Mock: true}}, // no config destination
				Actions:  []Action{action},
			}
			issues := ValidateScriptIssues(script)
			if issuesContain(issues, "missing destination") || issuesContain(issues, "destination or per-action target is required") {
				t.Fatalf("params.destination should satisfy the destination requirement, got: %v", issues)
			}
		})
	}
}

// TestValidateJMSSelectorSubQueueNone covers dsl-catalog-2: sub_queue:"none"
// normalizes to the main queue at runtime, so it must not be flagged as
// conflicting with a selector; a real sub-queue still conflicts.
func TestValidateJMSSelectorSubQueueNone(t *testing.T) {
	validate := func(subQueue string) []string {
		script := &Script{
			Name:     "jms-selector",
			Protocol: "jms-servicebus",
			Config:   ScriptConfig{JMS: JMSConfig{Provider: "servicebus", Destination: "orders.in", Mock: true}},
			Actions: []Action{{Name: "c", Type: "jms_consume", Params: map[string]any{
				"selector":  "priority > 5",
				"sub_queue": subQueue,
			}}},
		}
		return ValidateScriptIssues(script)
	}
	if issues := validate("none"); issuesContain(issues, "selector cannot be combined") {
		t.Fatalf("selector + sub_queue:none should be clean, got: %v", issues)
	}
	if issues := validate("dead_letter"); !issuesContain(issues, "selector cannot be combined") {
		t.Fatalf("selector + sub_queue:dead_letter should conflict, got: %v", issues)
	}
}
