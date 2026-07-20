package dsl

import (
	"fmt"
	"testing"
)

func jmsProviderValidationScript(protocolName, provider string, action Action) *Script {
	return &Script{
		Name:     "jms-provider-capability",
		Protocol: protocolName,
		Config: ScriptConfig{JMS: JMSConfig{
			Provider:    provider,
			Destination: "orders.in",
			ReplyTo:     "orders.reply",
			Mock:        true,
		}},
		Actions: []Action{action},
	}
}

func TestJMSProviderCapabilityMatrixCoversCatalog(t *testing.T) {
	specs := jmsActionSpecs()
	if len(jmsProviderCapabilities) != len(specs) {
		t.Fatalf("provider capability matrix has %d actions, catalog has %d", len(jmsProviderCapabilities), len(specs))
	}
	for _, spec := range specs {
		if _, ok := jmsProviderCapabilities[spec.Type]; !ok {
			t.Errorf("provider capability matrix is missing catalog action %q", spec.Type)
		}
	}
}

func TestJMSConsumeCatalogExposesServiceBusSessionToken(t *testing.T) {
	spec, ok := protocolActionSpec("jms", "jms_consume")
	if !ok {
		t.Fatal("jms_consume is missing from the action catalog")
	}
	for _, field := range spec.Fields {
		if field.Name == "params.session_token" && field.Kind == FieldKindString {
			return
		}
	}
	t.Fatal("jms_consume catalog does not expose params.session_token")
}

func TestJMSAdvancedCatalogMatchesRuntimeFields(t *testing.T) {
	expected := map[string][]string{
		"jms_request_reply": {
			"target", "body", "body_file", "headers", "params.reply_to", "params.subscription", "params.timeout_ms",
			"params.destination", "params.destination_type", "params.correlation_id", "params.dead_letter", "params.message_id",
			"params.session_id", "params.partition_key", "params.scheduled_enqueue_ms", "params.time_to_live_ms", "params.message_type",
			"params.map_body", "params.stream_body", "params.delivery_mode", "params.priority",
		},
		"jms_settle": {
			"params.settlement_token", "params.disposition", "params.dead_letter_reason", "params.dead_letter_description", "params.timeout_ms",
		},
		"jms_renew_lock": {
			"params.settlement_token", "params.timeout_ms",
		},
		"jms_session_accept": {
			"target", "params.destination", "params.destination_type", "params.session_id", "params.subscription", "params.timeout_ms",
		},
		"jms_session_state": {
			"params.session_token", "params.session_state", "params.timeout_ms",
		},
		"jms_session_close": {
			"params.session_token", "params.timeout_ms",
		},
		"jms_receive_deferred": {
			"target", "params.destination", "params.destination_type", "params.subscription", "params.sequence_numbers", "params.timeout_ms",
		},
		"jms_cancel_scheduled": {
			"target", "params.destination", "params.sequence_numbers", "params.timeout_ms",
		},
	}

	for actionType, wantFields := range expected {
		t.Run(actionType, func(t *testing.T) {
			spec, ok := protocolActionSpec("jms", actionType)
			if !ok {
				t.Fatalf("%s is missing from the JMS action catalog", actionType)
			}
			got := make(map[string]int, len(spec.Fields))
			for _, field := range spec.Fields {
				got[field.Name]++
			}
			if len(spec.Fields) != len(wantFields) {
				t.Fatalf("%s fields = %v, want exactly %v", actionType, got, wantFields)
			}
			for _, field := range wantFields {
				if got[field] != 1 {
					t.Errorf("%s field %q count = %d, want 1 (all fields: %v)", actionType, field, got[field], got)
				}
			}
		})
	}
}

func TestJMSAdvancedCatalogFieldKindsMatchRuntimeInputShapes(t *testing.T) {
	tests := []struct {
		actionType string
		field      string
		kind       FieldKind
	}{
		{actionType: "jms_request_reply", field: "params.session_id", kind: FieldKindString},
		{actionType: "jms_request_reply", field: "params.partition_key", kind: FieldKindString},
		{actionType: "jms_request_reply", field: "params.scheduled_enqueue_ms", kind: FieldKindInt},
		{actionType: "jms_session_accept", field: "params.destination_type", kind: FieldKindEnum},
		{actionType: "jms_receive_deferred", field: "params.sequence_numbers", kind: FieldKindAny},
		{actionType: "jms_cancel_scheduled", field: "params.sequence_numbers", kind: FieldKindAny},
	}
	for _, test := range tests {
		t.Run(test.actionType+"/"+test.field, func(t *testing.T) {
			spec, ok := protocolActionSpec("jms", test.actionType)
			if !ok {
				t.Fatalf("%s is missing from the JMS action catalog", test.actionType)
			}
			for _, field := range spec.Fields {
				if field.Name == test.field {
					if field.Kind != test.kind {
						t.Fatalf("%s %s kind = %q, want %q", test.actionType, test.field, field.Kind, test.kind)
					}
					return
				}
			}
			t.Fatalf("%s is missing field %s", test.actionType, test.field)
		})
	}
}

func TestValidateJMSRejectsServiceBusOnlyActionsForOtherProviders(t *testing.T) {
	actions := []Action{
		{Name: "settle", Type: "jms_settle", Params: map[string]any{"settlement_token": "token"}},
		{Name: "renew", Type: "jms_renew_lock", Params: map[string]any{"settlement_token": "token"}},
		{Name: "accept session", Type: "jms_session_accept"},
		{Name: "session state", Type: "jms_session_state", Params: map[string]any{"session_token": "session"}},
		{Name: "close session", Type: "jms_session_close", Params: map[string]any{"session_token": "session"}},
		{Name: "receive deferred", Type: "jms_receive_deferred", Params: map[string]any{"sequence_numbers": []any{float64(1)}}},
		{Name: "cancel scheduled", Type: "jms_cancel_scheduled", Params: map[string]any{"sequence_numbers": []any{float64(1)}}},
	}
	providers := []struct {
		protocol string
		provider string
	}{
		{protocol: "jms-solace", provider: "solace"},
		{protocol: "jms-ibmmq", provider: "ibmmq"},
	}

	for _, provider := range providers {
		for _, action := range actions {
			t.Run(provider.provider+"/"+action.Type, func(t *testing.T) {
				issues := ValidateScriptIssues(jmsProviderValidationScript(provider.protocol, provider.provider, action))
				want := fmt.Sprintf("%s is supported only by Azure Service Bus", action.Type)
				if !hasIssueContaining(issues, want) {
					t.Fatalf("issues = %v, want issue containing %q", issues, want)
				}
			})
		}
	}
}

func TestValidateJMSRejectsServiceBusOnlyParamsForOtherProviders(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		param  string
	}{
		{name: "publish session", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"session_id": "session-1"}}, param: "session_id"},
		{name: "publish partition", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"partition_key": "tenant-1"}}, param: "partition_key"},
		{name: "publish scheduled", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"scheduled_enqueue_ms": 100}}, param: "scheduled_enqueue_ms"},
		{name: "publish scheduled text", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"scheduled_enqueue_ms": "100"}}, param: "scheduled_enqueue_ms"},
		{name: "consume subqueue", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"sub_queue": "dead_letter"}}, param: "sub_queue"},
		{name: "consume peek lock", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"peek_lock": true}}, param: "peek_lock"},
		{name: "consume session", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"session_token": "session-token"}}, param: "session_token"},
		{name: "request session", action: Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"session_id": "session-1"}}, param: "session_id"},
		{name: "request partition", action: Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"partition_key": "tenant-1"}}, param: "partition_key"},
		{name: "request scheduled", action: Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"scheduled_enqueue_ms": 100}}, param: "scheduled_enqueue_ms"},
		{name: "request subscription", action: Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"subscription": "reply-sub"}}, param: "subscription"},
	}

	for _, provider := range []struct {
		protocol string
		provider string
	}{
		{protocol: "jms-solace", provider: "solace"},
		{protocol: "jms-ibmmq", provider: "ibmmq"},
	} {
		for _, test := range tests {
			t.Run(provider.provider+"/"+test.name, func(t *testing.T) {
				issues := ValidateScriptIssues(jmsProviderValidationScript(provider.protocol, provider.provider, test.action))
				want := fmt.Sprintf("params.%s is supported only by Azure Service Bus", test.param)
				if !hasIssueContaining(issues, want) {
					t.Fatalf("issues = %v, want issue containing %q", issues, want)
				}
			})
		}
	}
}

func TestValidateJMSProviderCapabilitiesAcrossLifecycleTree(t *testing.T) {
	script := &Script{
		Name:     "jms-provider-tree",
		Protocol: "jms-solace",
		Config: ScriptConfig{JMS: JMSConfig{
			Provider:    "solace",
			Destination: "orders.in",
			Mock:        true,
		}},
		Init: []Action{{Name: "settle init", Type: "jms_settle", Params: map[string]any{"settlement_token": "token"}}},
		Actions: []Action{
			{Name: "publish", Type: "jms_publish", Params: map[string]any{"partition_key": "tenant-1"}},
			{Name: "loop", Type: "control_loop", Control: &ControlFlow{Count: 1, Steps: []Action{
				{Name: "nested consume", Type: "jms_consume", Params: map[string]any{"peek_lock": true}},
			}}},
			{Name: "branch", Type: "control_if", Control: &ControlFlow{
				Condition: &Condition{Left: "x", Op: "eq", Right: "y"},
				Steps:     []Action{{Name: "valid publish", Type: "jms_publish"}},
				Else:      []Action{{Name: "nested close", Type: "jms_session_close", Params: map[string]any{"session_token": "session"}}},
			}},
		},
		Teardown: []Action{{Name: "renew teardown", Type: "jms_renew_lock", Params: map[string]any{"settlement_token": "token"}}},
	}

	issues := ValidateScriptIssues(script)
	for _, want := range []string{
		`init[0] "settle init": jms_settle is supported only by Azure Service Bus`,
		`actions[0] "publish": params.partition_key is supported only by Azure Service Bus`,
		`actions[1].control.steps[0] "nested consume": params.peek_lock is supported only by Azure Service Bus`,
		`actions[2].control.else[0] "nested close": jms_session_close is supported only by Azure Service Bus`,
		`teardown[0] "renew teardown": jms_renew_lock is supported only by Azure Service Bus`,
	} {
		if !hasIssueContaining(issues, want) {
			t.Errorf("issues = %v, want issue containing %q", issues, want)
		}
	}
}

func TestValidateJMSAllowsServiceBusSendCapabilitiesWithRealAdapter(t *testing.T) {
	script := &Script{
		Name:     "servicebus-capabilities",
		Protocol: "jms-servicebus",
		Config: ScriptConfig{JMS: JMSConfig{
			Provider:    "servicebus",
			BrokerURL:   "orders.servicebus.windows.net",
			Destination: "orders.in",
			ReplyTo:     "orders.reply",
			Mock:        false,
			Properties: map[string]string{
				"servicebus_max_clients":  "4",
				"servicebus_receive_mode": "peek_lock",
			},
		}},
		Init: []Action{{Name: "scheduled publish", Type: "jms_publish", Params: map[string]any{
			"session_id": "session-1", "partition_key": "tenant-1", "scheduled_enqueue_ms": 100,
		}}},
		Actions: []Action{
			{Name: "request", Type: "jms_request_reply", Params: map[string]any{
				"subscription": "reply-sub", "session_id": "session-1", "partition_key": "tenant-1", "scheduled_enqueue_ms": 100,
			}},
			{Name: "consume defaults", Type: "jms_consume", Params: map[string]any{
				"sub_queue": "none", "peek_lock": false, "session_token": "",
			}},
		},
	}

	issues := ValidateScriptIssues(script)
	for _, unwanted := range []string{"supported only by Azure Service Bus", "params.session_id requires", "params.partition_key requires", "params.scheduled_enqueue_ms requires"} {
		if hasIssueContaining(issues, unwanted) {
			t.Fatalf("real Service Bus capability script has issue %q: %v", unwanted, issues)
		}
	}
}

func TestValidateJMSServiceBusMockRejectsStatefulCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		want   string
	}{
		{name: "settle", action: Action{Name: "settle", Type: "jms_settle", Params: map[string]any{"settlement_token": "token"}}, want: "jms_settle requires the real Azure Service Bus adapter"},
		{name: "renew", action: Action{Name: "renew", Type: "jms_renew_lock", Params: map[string]any{"settlement_token": "token"}}, want: "jms_renew_lock requires the real Azure Service Bus adapter"},
		{name: "accept", action: Action{Name: "accept", Type: "jms_session_accept"}, want: "jms_session_accept requires the real Azure Service Bus adapter"},
		{name: "state", action: Action{Name: "state", Type: "jms_session_state", Params: map[string]any{"session_token": "session"}}, want: "jms_session_state requires the real Azure Service Bus adapter"},
		{name: "close", action: Action{Name: "close", Type: "jms_session_close", Params: map[string]any{"session_token": "session"}}, want: "jms_session_close requires the real Azure Service Bus adapter"},
		{name: "deferred", action: Action{Name: "deferred", Type: "jms_receive_deferred", Params: map[string]any{"sequence_numbers": []any{float64(1)}}}, want: "jms_receive_deferred requires the real Azure Service Bus adapter"},
		{name: "cancel", action: Action{Name: "cancel", Type: "jms_cancel_scheduled", Params: map[string]any{"sequence_numbers": []any{float64(1)}}}, want: "jms_cancel_scheduled requires the real Azure Service Bus adapter"},
		{name: "peek lock", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"peek_lock": true}}, want: "params.peek_lock requires the real Azure Service Bus adapter"},
		{name: "subqueue", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"sub_queue": "dead_letter"}}, want: "params.sub_queue requires the real Azure Service Bus adapter"},
		{name: "session consume", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"session_token": "session"}}, want: "params.session_token requires the real Azure Service Bus adapter"},
		{name: "publish session", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"session_id": "session"}}, want: "params.session_id requires the real Azure Service Bus adapter"},
		{name: "publish partition", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"partition_key": "tenant"}}, want: "params.partition_key requires the real Azure Service Bus adapter"},
		{name: "publish scheduled", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"scheduled_enqueue_ms": "100"}}, want: "params.scheduled_enqueue_ms requires the real Azure Service Bus adapter"},
		{name: "request session", action: Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"session_id": "session"}}, want: "params.session_id requires the real Azure Service Bus adapter"},
		{name: "request partition", action: Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"partition_key": "tenant"}}, want: "params.partition_key requires the real Azure Service Bus adapter"},
		{name: "request scheduled", action: Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"scheduled_enqueue_ms": 100}}, want: "params.scheduled_enqueue_ms requires the real Azure Service Bus adapter"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := ValidateScriptIssues(jmsProviderValidationScript("jms-servicebus", "servicebus", test.action))
			if !hasIssueContaining(issues, test.want) {
				t.Fatalf("issues = %v, want issue containing %q", issues, test.want)
			}
		})
	}
}

func TestValidateJMSProviderGatesIgnoreNoOpDefaults(t *testing.T) {
	for _, provider := range []struct {
		protocol string
		provider string
	}{
		{protocol: "jms-solace", provider: "solace"},
		{protocol: "jms-ibmmq", provider: "ibmmq"},
		{protocol: "jms-servicebus", provider: "servicebus"},
	} {
		t.Run(provider.provider, func(t *testing.T) {
			actions := []Action{
				{Name: "consume", Type: "jms_consume", Params: map[string]any{"peek_lock": false, "sub_queue": "none", "session_token": ""}},
				{Name: "publish", Type: "jms_publish", Params: map[string]any{"session_id": "", "partition_key": "", "scheduled_enqueue_ms": 0}},
				{Name: "request", Type: "jms_request_reply", Params: map[string]any{"subscription": "", "session_id": "", "partition_key": "", "scheduled_enqueue_ms": "0"}},
			}
			for _, action := range actions {
				issues := ValidateScriptIssues(jmsProviderValidationScript(provider.protocol, provider.provider, action))
				if hasIssueContaining(issues, "supported only by Azure Service Bus") || hasIssueContaining(issues, "requires the real Azure Service Bus adapter") {
					t.Fatalf("%s no-op defaults have provider capability issues: %v", action.Type, issues)
				}
			}
		})
	}
}

func TestValidateJMSRejectsServiceBusConfigPropertiesForOtherProviders(t *testing.T) {
	for _, property := range serviceBusOnlyJMSConfigProperties {
		t.Run(property, func(t *testing.T) {
			script := jmsProviderValidationScript("jms-solace", "solace", Action{Name: "publish", Type: "jms_publish"})
			script.Config.JMS.Properties = map[string]string{property: "1"}
			issues := ValidateScriptIssues(script)
			if !hasIssueContaining(issues, "config.jms.properties."+property+" is supported only by Azure Service Bus") {
				t.Fatalf("issues = %v, want Service Bus-only config property issue", issues)
			}
		})
	}
}

func TestValidateJMSAdvancedActionsRequireRuntimeInputs(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		want   string
	}{
		{name: "settle token", action: Action{Name: "settle", Type: "jms_settle"}, want: "settlement_token is required"},
		{name: "renew token", action: Action{Name: "renew", Type: "jms_renew_lock"}, want: "settlement_token is required"},
		{name: "accept destination", action: Action{Name: "accept", Type: "jms_session_accept"}, want: "missing destination"},
		{name: "state token", action: Action{Name: "state", Type: "jms_session_state"}, want: "session_token is required"},
		{name: "close token", action: Action{Name: "close", Type: "jms_session_close"}, want: "session_token is required"},
		{name: "deferred destination", action: Action{Name: "deferred", Type: "jms_receive_deferred", Params: map[string]any{"sequence_numbers": []any{float64(1)}}}, want: "missing destination"},
		{name: "cancel destination", action: Action{Name: "cancel", Type: "jms_cancel_scheduled", Params: map[string]any{"sequence_numbers": []any{float64(1)}}}, want: "missing destination"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := jmsProviderValidationScript("jms-servicebus", "servicebus", test.action)
			script.Config.JMS.Destination = ""
			issues := ValidateScriptIssues(script)
			if !hasIssueContaining(issues, test.want) {
				t.Fatalf("issues = %v, want issue containing %q", issues, test.want)
			}
		})
	}
}

func TestValidateJMSDeferredAndCancelRejectEmptySequenceNumbers(t *testing.T) {
	for _, actionType := range []string{"jms_receive_deferred", "jms_cancel_scheduled"} {
		for name, sequenceNumbers := range map[string]any{
			"empty array":  []any{},
			"empty string": "",
			"nil":          nil,
		} {
			t.Run(actionType+"/"+name, func(t *testing.T) {
				action := Action{Name: "advanced", Type: actionType, Params: map[string]any{"sequence_numbers": sequenceNumbers}}
				issues := ValidateScriptIssues(jmsProviderValidationScript("jms-servicebus", "servicebus", action))
				if !hasIssueContaining(issues, "sequence_numbers is required") {
					t.Fatalf("issues = %v, want sequence_numbers-required issue", issues)
				}
			})
		}
	}
}

func TestValidateJMSAdvancedTopicConsumesRequireSubscription(t *testing.T) {
	for _, actionType := range []string{"jms_session_accept", "jms_receive_deferred"} {
		t.Run(actionType, func(t *testing.T) {
			action := Action{Name: "advanced", Type: actionType}
			if actionType == "jms_receive_deferred" {
				action.Params = map[string]any{"sequence_numbers": []any{float64(1)}}
			}
			script := jmsProviderValidationScript("jms-servicebus", "servicebus", action)
			script.Config.JMS.DestinationType = "topic"
			issues := ValidateScriptIssues(script)
			if !hasIssueContaining(issues, "subscription required to consume from a Service Bus topic") {
				t.Fatalf("issues = %v, want subscription-required issue", issues)
			}
		})
	}
}

func TestValidateJMSAdvancedActionsAcceptCataloguedDestinationOverrides(t *testing.T) {
	tests := []Action{
		{Name: "accept", Type: "jms_session_accept", Params: map[string]any{
			"destination": "orders.topic", "destination_type": "topic", "subscription": "perf", "session_id": "session-1", "timeout_ms": 100,
		}},
		{Name: "deferred", Type: "jms_receive_deferred", Params: map[string]any{
			"destination": "orders.topic", "destination_type": "topic", "subscription": "perf", "sequence_numbers": "1,2", "timeout_ms": 100,
		}},
		{Name: "cancel", Type: "jms_cancel_scheduled", Params: map[string]any{
			"destination": "orders.topic", "sequence_numbers": float64(1), "timeout_ms": 100,
		}},
	}

	for _, action := range tests {
		t.Run(action.Type, func(t *testing.T) {
			script := jmsProviderValidationScript("jms-servicebus", "servicebus", action)
			script.Config.JMS.Mock = false
			script.Config.JMS.BrokerURL = "orders.servicebus.windows.net"
			script.Config.JMS.Destination = ""
			issues := ValidateScriptIssues(script)
			for _, unwanted := range []string{
				"missing destination",
				"subscription required to consume from a Service Bus topic",
				"sequence_numbers is required",
				"supported only by Azure Service Bus",
			} {
				if hasIssueContaining(issues, unwanted) {
					t.Errorf("issues = %v, did not want %q", issues, unwanted)
				}
			}
		})
	}
}

func TestValidateJMSSelectorsFailClosedWhenRuntimeCannotApplyThem(t *testing.T) {
	t.Run("real Service Bus consume", func(t *testing.T) {
		action := Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"selector": "priority > 5"}}
		script := jmsProviderValidationScript("jms-servicebus", "servicebus", action)
		script.Config.JMS.Mock = false
		script.Config.JMS.BrokerURL = "orders.servicebus.windows.net"
		issues := ValidateScriptIssues(script)
		if !hasIssueContaining(issues, "selector is not supported by Azure Service Bus jms_consume") {
			t.Fatalf("issues = %v, want Service Bus consume selector issue", issues)
		}
	})

	for _, provider := range []struct {
		protocol string
		name     string
		mock     bool
	}{
		{protocol: "jms-solace", name: "solace", mock: true},
		{protocol: "jms-ibmmq", name: "ibmmq", mock: true},
		{protocol: "jms-servicebus", name: "servicebus", mock: true},
	} {
		t.Run(provider.name+" request reply", func(t *testing.T) {
			action := Action{Name: "request", Type: "jms_request_reply", Params: map[string]any{"selector": "priority > 5"}}
			script := jmsProviderValidationScript(provider.protocol, provider.name, action)
			script.Config.JMS.Mock = provider.mock
			issues := ValidateScriptIssues(script)
			if !hasIssueContaining(issues, "selector is not supported by jms_request_reply") {
				t.Fatalf("issues = %v, want request/reply selector issue", issues)
			}
		})
	}
}
