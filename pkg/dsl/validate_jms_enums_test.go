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
		{"valid persistent/auto/queue", func(c *JMSConfig) { c.DeliveryMode = "persistent"; c.Acknowledgement = "auto"; c.DestinationType = "queue" }, "", true},
		{"valid transacted/topic/non_persistent", func(c *JMSConfig) { c.Acknowledgement = "transacted"; c.DestinationType = "topic"; c.DeliveryMode = "non_persistent" }, "", true},
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
