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

func TestValidateJMSBrowseAction(t *testing.T) {
	// Browse with a destination (config.jms.destination) validates clean.
	clean := &Script{
		Name: "jms-browse", Protocol: "jms-solace",
		Config:  ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "orders.in", Mock: true}},
		Actions: []Action{{Name: "browse", Type: "jms_browse", Params: map[string]any{"selector": "region = 'eu'", "max": 5}}},
	}
	if issues := ValidateScriptIssues(clean); len(issues) > 0 {
		t.Fatalf("clean jms_browse script has issues: %v", issues)
	}
	// Browse with no destination anywhere is flagged.
	noDest := &Script{
		Name: "jms-browse", Protocol: "jms-solace",
		Config:  ScriptConfig{JMS: JMSConfig{Provider: "solace", Mock: true}},
		Actions: []Action{{Name: "browse", Type: "jms_browse"}},
	}
	if !hasIssueContaining(ValidateScriptIssues(noDest), "missing destination") {
		t.Fatalf("expected missing-destination issue for jms_browse without a destination")
	}
}

func TestValidateJMSSelectorConflicts(t *testing.T) {
	script := func(params map[string]any) *Script {
		return &Script{
			Name: "jms-sel", Protocol: "jms-solace",
			Config:  ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "q", Mock: true}},
			Actions: []Action{{Name: "c", Type: "jms_consume", Params: params}},
		}
	}
	// selector alone is fine.
	if issues := ValidateScriptIssues(script(map[string]any{"selector": "a = 'b'"})); hasIssueContaining(issues, "selector cannot be combined") {
		t.Fatalf("selector alone should be valid: %v", issues)
	}
	// selector + peek_lock conflicts.
	if !hasIssueContaining(ValidateScriptIssues(script(map[string]any{"selector": "a = 'b'", "peek_lock": true})), "selector cannot be combined") {
		t.Fatal("expected selector+peek_lock conflict issue")
	}
}

func TestValidateJMSSubscriptionMode(t *testing.T) {
	script := func(mode string) *Script {
		return &Script{
			Name: "jms-sub", Protocol: "jms-solace",
			Config:  ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "t", DestinationType: "topic", Mock: true}},
			Actions: []Action{{Name: "c", Type: "jms_consume", Params: map[string]any{"subscription": "s", "subscription_mode": mode}}},
		}
	}
	if issues := ValidateScriptIssues(script("durable")); hasIssueContaining(issues, "subscription_mode") {
		t.Fatalf("durable should be valid: %v", issues)
	}
	if !hasIssueContaining(ValidateScriptIssues(script("forever")), "subscription_mode") {
		t.Fatal("expected subscription_mode enum issue for an invalid mode")
	}
}

func TestValidateJMSTopicRequiresSubscription(t *testing.T) {
	// Topic consume on a non-Service-Bus provider with no subscription is flagged.
	noSub := &Script{
		Name: "jms-topic", Protocol: "jms-solace",
		Config:  ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "t", DestinationType: "topic", Mock: true}},
		Actions: []Action{{Name: "c", Type: "jms_consume"}},
	}
	if !hasIssueContaining(ValidateScriptIssues(noSub), "topic requires a subscription") {
		t.Fatalf("expected topic-requires-subscription issue, got %v", ValidateScriptIssues(noSub))
	}
	// With a subscription it validates clean.
	withSub := &Script{
		Name: "jms-topic", Protocol: "jms-solace",
		Config:  ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "t", DestinationType: "topic", Subscription: "s", Mock: true}},
		Actions: []Action{{Name: "c", Type: "jms_consume"}},
	}
	if hasIssueContaining(ValidateScriptIssues(withSub), "topic requires a subscription") {
		t.Fatalf("topic consume with a subscription should be clean: %v", ValidateScriptIssues(withSub))
	}
}
