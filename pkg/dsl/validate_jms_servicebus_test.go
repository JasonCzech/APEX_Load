//go:build !jms_servicebus_real

package dsl

import (
	"testing"
)

func TestValidateJMSServiceBusRealAdapterUnavailableWithoutVPNWarning(t *testing.T) {
	issues := ValidateScriptIssues(&Script{
		Name:     "Service Bus JMS",
		Protocol: "jms-servicebus",
		Config: ScriptConfig{
			JMS: JMSConfig{
				Provider:        "servicebus",
				BrokerURL:       "orders.servicebus.windows.net",
				Destination:     "orders.in",
				DestinationType: "queue",
				Mock:            false,
			},
		},
		Actions: []Action{{
			Name: "Publish",
			Type: "jms_publish",
			Body: `{"orderId":"SB-100"}`,
		}},
	})

	if !containsIssue(issues, "real Azure Service Bus JMS adapter is not available") {
		t.Fatalf("issues = %v, want Service Bus unavailable adapter issue", issues)
	}
	if containsIssue(issues, "message_vpn") {
		t.Fatalf("issues = %v, did not expect Solace message_vpn warning", issues)
	}
}

func TestValidateJMSServiceBusTopicConsumeRequiresSubscription(t *testing.T) {
	issues := ValidateScriptIssues(&Script{
		Name:     "Service Bus Topic",
		Protocol: "jms-servicebus",
		Config: ScriptConfig{
			JMS: JMSConfig{
				Provider:        "servicebus",
				Destination:     "orders.topic",
				DestinationType: "topic",
				Mock:            true,
			},
		},
		Actions: []Action{{
			Name: "Consume",
			Type: "jms_consume",
		}},
	})

	if !containsIssue(issues, "subscription required to consume from a Service Bus topic") {
		t.Fatalf("issues = %v, want subscription-required issue", issues)
	}
}

func TestValidateJMSServiceBusTopicBrowseRequiresSubscription(t *testing.T) {
	// jms_browse on a topic requires a subscription at runtime (jms.go), so it
	// must be flagged at validation time like jms_consume — not slip through.
	issues := ValidateScriptIssues(&Script{
		Name:     "Service Bus Topic",
		Protocol: "jms-servicebus",
		Config: ScriptConfig{
			JMS: JMSConfig{
				Provider:        "servicebus",
				Destination:     "orders.topic",
				DestinationType: "topic",
				Mock:            true,
			},
		},
		Actions: []Action{{
			Name: "Browse",
			Type: "jms_browse",
		}},
	})

	if !containsIssue(issues, "subscription required to consume from a Service Bus topic") {
		t.Fatalf("issues = %v, want subscription-required issue for jms_browse", issues)
	}
}

func TestValidateJMSServiceBusTopicConsumeAcceptsSubscriptionSources(t *testing.T) {
	tests := []struct {
		name   string
		jms    JMSConfig
		action Action
	}{
		{
			name: "config subscription",
			jms: JMSConfig{
				Provider:        "servicebus",
				Destination:     "orders.topic",
				DestinationType: "topic",
				Subscription:    "perf",
				Mock:            true,
			},
			action: Action{Name: "Consume", Type: "jms_consume"},
		},
		{
			name: "action subscription",
			jms: JMSConfig{
				Provider:        "servicebus",
				Destination:     "orders.topic",
				DestinationType: "topic",
				Mock:            true,
			},
			action: Action{Name: "Consume", Type: "jms_consume", Params: map[string]any{"subscription": "perf"}},
		},
		{
			name: "destination already includes subscription",
			jms: JMSConfig{
				Provider:        "servicebus",
				Destination:     "orders.topic/subscriptions/perf",
				DestinationType: "topic",
				Mock:            true,
			},
			action: Action{Name: "Consume", Type: "jms_consume"},
		},
		{
			name: "request reply subscription",
			jms: JMSConfig{
				Provider:        "servicebus",
				Destination:     "orders.request",
				DestinationType: "topic",
				ReplyTo:         "orders.reply",
				Subscription:    "perf",
				Mock:            true,
			},
			action: Action{Name: "Request", Type: "jms_request_reply", Body: "{}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateScriptIssues(&Script{
				Name:     "Service Bus Topic",
				Protocol: "jms-servicebus",
				Config:   ScriptConfig{JMS: tt.jms},
				Actions:  []Action{tt.action},
			})
			if containsIssue(issues, "subscription required to consume from a Service Bus topic") {
				t.Fatalf("issues = %v, did not expect subscription-required issue", issues)
			}
		})
	}
}
