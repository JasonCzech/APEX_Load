package dsl

import (
	"strings"
	"testing"
)

func jmsEnumScript(mutate func(*JMSConfig)) *Script {
	cfg := JMSConfig{Provider: "solace", Destination: "orders.in", Mock: true}
	if mutate != nil {
		mutate(&cfg)
	}
	return &Script{
		Name:     "jms-enum",
		Protocol: "jms-solace",
		Config:   ScriptConfig{JMS: cfg},
		Actions:  []Action{{Name: "pub", Type: "jms_publish", Body: "{}"}},
	}
}

func TestValidateJMSConfigEnums(t *testing.T) {
	enumIssue := func(issues []string) bool {
		for _, is := range issues {
			if strings.Contains(is, "delivery_mode") || strings.Contains(is, "acknowledgement") || strings.Contains(is, "destination_type") {
				return true
			}
		}
		return false
	}
	tests := []struct {
		name    string
		mutate  func(*JMSConfig)
		wantHas string
		clean   bool
	}{
		{"valid persistent/auto/queue", func(c *JMSConfig) {
			c.DeliveryMode = "persistent"
			c.Acknowledgement = "auto"
			c.DestinationType = "queue"
		}, "", true},
		{"valid transacted/topic/non_persistent", func(c *JMSConfig) {
			c.Acknowledgement = "transacted"
			c.DestinationType = "topic"
			c.DeliveryMode = "non_persistent"
		}, "", true},
		{"bad delivery_mode", func(c *JMSConfig) { c.DeliveryMode = "carrier_pigeon" }, "config.jms.delivery_mode", false},
		{"bad acknowledgement", func(c *JMSConfig) { c.Acknowledgement = "maybe" }, "config.jms.acknowledgement", false},
		{"bad destination_type", func(c *JMSConfig) { c.DestinationType = "channel" }, "config.jms.destination_type", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateScriptIssues(jmsEnumScript(tt.mutate))
			if tt.clean {
				if enumIssue(issues) {
					t.Fatalf("unexpected enum issue for valid config: %v", issues)
				}
				return
			}
			if !hasIssueContaining(issues, tt.wantHas) {
				t.Fatalf("expected issue containing %q, got %v", tt.wantHas, issues)
			}
		})
	}
}

// jmsPublishParamsScript builds a minimal valid jms-solace script whose single
// publish action carries the given params, for P2.3 send-control validation.
func jmsPublishParamsScript(params map[string]any) *Script {
	return &Script{
		Name:     "jms-p23",
		Protocol: "jms-solace",
		Config:   ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "orders.in", Mock: true}},
		Actions:  []Action{{Name: "pub", Type: "jms_publish", Body: "{}", Params: params}},
	}
}

func TestValidateJMSSendControls(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantHas string // "" means must be clean of send-control issues
	}{
		{"valid type+priority+mode", map[string]any{"message_type": "map", "priority": 5, "delivery_mode": "non_persistent"}, ""},
		{"valid text", map[string]any{"message_type": "text"}, ""},
		{"bad message_type", map[string]any{"message_type": "video"}, "message_type"},
		{"bad delivery_mode", map[string]any{"delivery_mode": "eventually"}, "delivery_mode"},
		{"priority too high", map[string]any{"priority": 10}, "priority must be an integer 0-9"},
		{"priority negative", map[string]any{"priority": -1}, "priority must be an integer 0-9"},
		{"priority non-numeric", map[string]any{"priority": "high"}, "priority must be an integer 0-9"},
		{"priority boundary 0", map[string]any{"priority": 0}, ""},
		{"priority boundary 9", map[string]any{"priority": 9}, ""},
	}
	sendControlIssue := func(issues []string) bool {
		for _, is := range issues {
			if strings.Contains(is, "message_type") || strings.Contains(is, "delivery_mode") || strings.Contains(is, "priority") {
				return true
			}
		}
		return false
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateScriptIssues(jmsPublishParamsScript(tt.params))
			if tt.wantHas == "" {
				if sendControlIssue(issues) {
					t.Fatalf("unexpected send-control issue for %v: %v", tt.params, issues)
				}
				return
			}
			if !hasIssueContaining(issues, tt.wantHas) {
				t.Fatalf("expected issue containing %q, got %v", tt.wantHas, issues)
			}
		})
	}
}
