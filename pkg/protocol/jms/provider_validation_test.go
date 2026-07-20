package jms

import (
	"context"
	"strings"
	"testing"
)

func TestJMSInitProviderContractFailsClosed(t *testing.T) {
	tests := []struct {
		name             string
		protocolName     string
		runtimeProvider  string
		configured       string
		wantErrorMessage string
	}{
		{
			name: "runtime mismatch", protocolName: "jms-solace", runtimeProvider: "servicebus",
			wantErrorMessage: `JMS runtime provider "servicebus" does not match protocol "jms-solace"`,
		},
		{
			name: "configured mismatch", protocolName: "jms-solace", runtimeProvider: "solace", configured: "servicebus",
			wantErrorMessage: `jms_provider "servicebus" does not match protocol "jms-solace"`,
		},
		{
			name: "unknown configured provider", protocolName: "jms-solace", runtimeProvider: "solace", configured: "activemq",
			wantErrorMessage: `jms_provider "activemq" is not supported`,
		},
		{
			name: "generic protocol", protocolName: "jms", runtimeProvider: "solace", configured: "solace",
			wantErrorMessage: `generic JMS protocol "jms" is not executable`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := map[string]any{"jms_mock": true}
			if test.configured != "" {
				cfg["jms_provider"] = test.configured
			}
			instance := &JMS{name: test.protocolName, provider: test.runtimeProvider}
			err := instance.Init(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), test.wantErrorMessage) {
				t.Fatalf("Init error = %v, want substring %q", err, test.wantErrorMessage)
			}
			if instance.initialized || instance.adapter != nil {
				t.Fatalf("failed Init left runtime initialized: %#v", instance)
			}
		})
	}
}

func TestJMSInitInfersProviderFromSpecificProtocol(t *testing.T) {
	instance := &JMS{name: "jms-solace"}
	if err := instance.Init(context.Background(), map[string]any{"jms_mock": true}); err != nil {
		t.Fatalf("Init with omitted provider returned error: %v", err)
	}
	defer instance.Teardown(context.Background())
	if instance.provider != "solace" {
		t.Fatalf("provider = %q, want solace inferred from protocol", instance.provider)
	}
}
