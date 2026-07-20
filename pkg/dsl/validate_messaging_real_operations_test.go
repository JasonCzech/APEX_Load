package dsl

import "testing"

func TestValidateIBMRealModeFailsClosed(t *testing.T) {
	if IbmMqJMSRealAdapterAvailable() {
		t.Fatal("IbmMqJMSRealAdapterAvailable() = true without a linked MQI adapter")
	}
	script := &Script{
		Name:     "ibm-mq-real",
		Protocol: "jms-ibmmq",
		Config: ScriptConfig{JMS: JMSConfig{
			Provider:    "ibmmq",
			BrokerURL:   "tcp://broker.example.com:1414",
			Destination: "orders.in",
		}},
		Actions: []Action{{Name: "publish", Type: "jms_publish", Body: "{}"}},
	}
	issues := ValidateScriptIssues(script)
	if !hasIssueContaining(issues, "real IBM MQ JMS adapter is not available") {
		t.Fatalf("issues = %v, want IBM MQ adapter availability issue", issues)
	}
	if hasIssueContaining(issues, "message_vpn") {
		t.Fatalf("issues = %v, IBM MQ must not require a Solace message VPN", issues)
	}

	script.Config.JMS.Mock = true
	issues = ValidateScriptIssues(script)
	if hasIssueContaining(issues, "real IBM MQ JMS adapter is not available") {
		t.Fatalf("mock script issues = %v, did not want IBM MQ availability issue", issues)
	}
}

func TestValidateSolaceSidecarDoesNotRequireCompiledAdapterOrMessageVPN(t *testing.T) {
	script := &Script{
		Name: "solace-sidecar", Protocol: "jms-solace",
		Config: ScriptConfig{JMS: JMSConfig{
			Provider: "solace", BrokerURL: "amqps://broker.example.com:5671",
			Destination: "orders.in", Properties: map[string]string{"adapter": "sidecar"},
		}},
		Actions: []Action{{Name: "publish", Type: "jms_publish", Body: "{}"}},
	}
	issues := ValidateScriptIssues(script)
	for _, unwanted := range []string{"real Solace JMS adapter is not available", "message_vpn"} {
		if hasIssueContaining(issues, unwanted) {
			t.Fatalf("valid sidecar issues = %v, did not want %q", issues, unwanted)
		}
	}
}

func TestValidateSolaceSidecarRejectsUnsupportedActionCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		want   string
	}{
		{name: "browse", action: Action{Name: "browse", Type: "jms_browse"}, want: "jms_browse is not supported by the Solace sidecar adapter"},
		{name: "consume selector", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"selector": "priority > 5"}}, want: "params.selector is not supported by the Solace sidecar adapter"},
		{name: "destination type override", action: Action{Name: "publish", Type: "jms_publish", Params: map[string]any{"destination_type": "topic"}}, want: "action-level params.destination_type is not supported"},
		{name: "subscription override", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"subscription": "perf"}}, want: "action-level params.subscription is not supported"},
		{name: "subscription mode", action: Action{Name: "consume", Type: "jms_consume", Params: map[string]any{"subscription_mode": "durable"}}, want: "action-level params.subscription_mode is not supported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &Script{
				Name: "solace-sidecar", Protocol: "jms-solace",
				Config: ScriptConfig{JMS: JMSConfig{
					Provider: "solace", BrokerURL: "amqps://broker.example.com:5671",
					Destination: "orders.in", Properties: map[string]string{"adapter": "sidecar"},
				}},
				Actions: []Action{test.action},
			}
			issues := ValidateScriptIssues(script)
			if !hasIssueContaining(issues, test.want) {
				t.Fatalf("issues = %v, want %q", issues, test.want)
			}
		})
	}
}

func TestValidateRealJMSSettlementFailsClosedAcrossActionTree(t *testing.T) {
	script := &Script{
		Name:     "real-jms-settlement",
		Protocol: "jms-solace",
		Config: ScriptConfig{JMS: JMSConfig{
			Provider:        "solace",
			BrokerURL:       "amqp://broker.example.com:5672",
			MessageVPN:      "default",
			Destination:     "orders.in",
			Acknowledgement: "client",
		}},
		Init: []Action{{Name: "ack-init", Type: "jms_ack"}},
		Actions: []Action{{
			Name: "loop",
			Type: "control_loop",
			Control: &ControlFlow{Count: 1, Steps: []Action{{
				Name: "commit-nested", Type: "jms_commit",
			}}},
		}},
		Teardown: []Action{{Name: "rollback-teardown", Type: "jms_rollback"}},
	}
	issues := ValidateScriptIssues(script)
	for _, want := range []string{
		"config.jms.acknowledgement \"client\" is not supported by real adapters",
		"init[0] \"ack-init\": jms_ack is supported only",
		"actions[0].control.steps[0] \"commit-nested\": jms_commit is supported only",
		"teardown[0] \"rollback-teardown\": jms_rollback is supported only",
	} {
		if !hasIssueContaining(issues, want) {
			t.Errorf("issues = %v, want issue containing %q", issues, want)
		}
	}

	script.Config.JMS.Mock = true
	issues = ValidateScriptIssues(script)
	if hasIssueContaining(issues, "supported only with config.jms.mock") || hasIssueContaining(issues, "not supported by real adapters") {
		t.Fatalf("mock settlement script has real-adapter issue: %v", issues)
	}
}

func TestValidateKafkaSeekIsMockOnlyAcrossActionTree(t *testing.T) {
	script := kafkaScript(func(script *Script) {
		script.Config.Kafka.Mock = false
		script.Config.Kafka.Brokers = []string{"broker.example.com:9092"}
		script.Actions = []Action{{
			Name: "loop",
			Type: "control_loop",
			Control: &ControlFlow{Count: 1, Steps: []Action{{
				Name: "seek-nested", Type: "kafka_seek", Params: map[string]any{"offset": "earliest"},
			}}},
		}}
	})
	issues := ValidateScriptIssues(script)
	if !hasIssueContaining(issues, "actions[0].control.steps[0] \"seek-nested\": kafka_seek is supported only with config.kafka.mock") {
		t.Fatalf("issues = %v, want real Kafka seek issue", issues)
	}

	script.Config.Kafka.Mock = true
	issues = ValidateScriptIssues(script)
	if hasIssueContaining(issues, "kafka_seek is supported only with config.kafka.mock") {
		t.Fatalf("mock Kafka seek script has real-adapter issue: %v", issues)
	}
}
