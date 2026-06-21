package dsl

import (
	"strings"
	"testing"
)

func jmsAdvancedScript(actions []Action) *Script {
	return &Script{
		Name:     "jms-sb-advanced",
		Protocol: "jms-servicebus",
		Config:   ScriptConfig{JMS: JMSConfig{Provider: "servicebus", Destination: "orders.in", Mock: true}},
		Actions:  actions,
	}
}

func issuesContainSubstr(issues []string, substr string) bool {
	for _, is := range issues {
		if strings.Contains(is, substr) {
			return true
		}
	}
	return false
}

func TestValidateJMSSettleEnumAndToken(t *testing.T) {
	// Valid settle with all four dispositions.
	for _, disp := range []string{"complete", "abandon", "dead_letter", "defer"} {
		s := jmsAdvancedScript([]Action{{Name: "settle", Type: "jms_settle", Params: map[string]any{"settlement_token": "{{var.tok}}", "disposition": disp}}})
		if issuesContainSubstr(ValidateScriptIssues(s), "disposition") {
			t.Fatalf("disposition %q wrongly rejected", disp)
		}
	}
	// Invalid disposition.
	s := jmsAdvancedScript([]Action{{Name: "settle", Type: "jms_settle", Params: map[string]any{"settlement_token": "t", "disposition": "incinerate"}}})
	if !issuesContainSubstr(ValidateScriptIssues(s), "disposition \"incinerate\" is not supported") {
		t.Fatalf("expected disposition rejection, got %v", ValidateScriptIssues(s))
	}
	// Missing settlement_token.
	s = jmsAdvancedScript([]Action{{Name: "settle", Type: "jms_settle", Params: map[string]any{"disposition": "complete"}}})
	if !issuesContainSubstr(ValidateScriptIssues(s), "settlement_token is required") {
		t.Fatalf("expected settlement_token-required, got %v", ValidateScriptIssues(s))
	}
}

func TestValidateJMSRenewLockRequiresToken(t *testing.T) {
	s := jmsAdvancedScript([]Action{{Name: "renew", Type: "jms_renew_lock", Params: map[string]any{}}})
	if !issuesContainSubstr(ValidateScriptIssues(s), "settlement_token is required") {
		t.Fatalf("expected settlement_token-required for renew_lock, got %v", ValidateScriptIssues(s))
	}
	s = jmsAdvancedScript([]Action{{Name: "renew", Type: "jms_renew_lock", Params: map[string]any{"settlement_token": "t"}}})
	if issuesContainSubstr(ValidateScriptIssues(s), "settlement_token is required") {
		t.Fatalf("valid renew_lock wrongly rejected: %v", ValidateScriptIssues(s))
	}
}

func TestValidateJMSConsumeSubQueueEnum(t *testing.T) {
	for _, sq := range []string{"none", "dead_letter", "transfer_dead_letter"} {
		s := jmsAdvancedScript([]Action{{Name: "c", Type: "jms_consume", Target: "orders.in", Params: map[string]any{"sub_queue": sq}}})
		if issuesContainSubstr(ValidateScriptIssues(s), "sub_queue") {
			t.Fatalf("sub_queue %q wrongly rejected", sq)
		}
	}
	s := jmsAdvancedScript([]Action{{Name: "c", Type: "jms_consume", Target: "orders.in", Params: map[string]any{"sub_queue": "purgatory"}}})
	if !issuesContainSubstr(ValidateScriptIssues(s), "sub_queue \"purgatory\" is not supported") {
		t.Fatalf("expected sub_queue rejection, got %v", ValidateScriptIssues(s))
	}
}

func TestValidateJMSSettleActionTypeKnown(t *testing.T) {
	// jms_settle / jms_renew_lock are recognized action types (not "unsupported").
	s := jmsAdvancedScript([]Action{{Name: "settle", Type: "jms_settle", Params: map[string]any{"settlement_token": "t"}}})
	if issuesContainSubstr(ValidateScriptIssues(s), "unsupported JMS action type") {
		t.Fatalf("jms_settle reported as unsupported: %v", ValidateScriptIssues(s))
	}
}
