package dsl

import (
	"reflect"
	"slices"
	"testing"
)

func TestCatalogResidualValidatorsDriveProtocolValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		family string
		action Action
		want   []string
	}{
		{
			name:   "http sequence",
			family: "http",
			action: Action{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{}}},
			want:   []string{`actions[0] "sequence": http_sequence requires at least one child request`},
		},
		{
			name:   "browser",
			family: "browser",
			action: Action{Name: "click", Type: "browser_click"},
			want:   []string{`actions[0] "click": missing target or selector`},
		},
		{
			name:   "websocket",
			family: "websocket",
			action: Action{Name: "connect", Type: "ws_connect"},
			want:   []string{`actions[0] "connect": missing target`},
		},
		{
			name:   "grpc",
			family: "grpc",
			action: Action{Name: "call", Type: "grpc_unary"},
			want:   []string{`actions[0] "call": missing target`},
		},
		{
			name:   "database",
			family: "database",
			action: Action{Name: "query", Type: "db_query"},
			want:   []string{`actions[0] "query": database action requires target or body SQL`},
		},
		{
			name:   "network",
			family: "network",
			action: Action{Name: "dial", Type: "tcp_request"},
			want:   []string{`actions[0] "dial": socket action requires target address`},
		},
		{
			name:   "sap gui",
			family: "sap_gui",
			action: Action{Name: "press", Type: "sapgui_press"},
			want:   []string{`actions[0] "press": sapgui_press requires target or params.vkey`},
		},
		{
			name:   "java vuser",
			family: "java_vuser",
			action: Action{Name: "run", Type: "java_vuser_run"},
			want:   []string{`actions[0] "run": java_vuser_run requires target class name`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec, ok := protocolActionSpec(tt.family, tt.action.Type)
			if !ok {
				t.Fatalf("catalog is missing %s action %q", tt.family, tt.action.Type)
			}
			if spec.Validate == nil {
				t.Fatalf("catalog action %q has no residual validator", tt.action.Type)
			}
			if got := spec.Validate(0, tt.action); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("catalog validation = %#v, want %#v", got, tt.want)
			}
			if got := validateActionForProtocol(tt.family, 0, tt.action); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("protocol validation = %#v, want catalog result %#v", got, tt.want)
			}
		})
	}
}

func TestMessagingCatalogOwnsConfigAwareResidualValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		family string
		script *Script
		want   string
	}{
		{
			name:   "jms reply destination",
			family: "jms",
			script: &Script{
				Name:     "jms",
				Protocol: "jms-solace",
				Config: ScriptConfig{JMS: JMSConfig{
					Mock:        true,
					Provider:    "solace",
					Destination: "requests",
				}},
				Actions: []Action{{Name: "request", Type: "jms_request_reply", Target: "requests"}},
			},
			want: `actions[0] "request": missing reply_to destination`,
		},
		{
			name:   "kafka transaction",
			family: "kafka",
			script: &Script{
				Name:     "kafka",
				Protocol: "kafka",
				Config:   ScriptConfig{Kafka: KafkaConfig{Mock: true}},
				Actions:  []Action{{Name: "begin", Type: "kafka_begin_transaction"}},
			},
			want: `actions[0] "begin": kafka_begin_transaction requires config.kafka.transactional_id`,
		},
		{
			name:   "event hubs routing",
			family: "eventhubs",
			script: &Script{
				Name:     "event hubs",
				Protocol: "eventhubs",
				Config:   ScriptConfig{EventHubs: EventHubsConfig{Mock: true, EventHub: "events"}},
				Actions: []Action{{
					Name:   "produce",
					Type:   "eventhubs_produce",
					Target: "events",
					Params: map[string]any{"partition_key": "account", "partition_id": "1"},
				}},
			},
			want: `actions[0] "produce": partition_key and partition_id are mutually exclusive`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			action := tt.script.Actions[0]
			spec, ok := protocolActionSpec(tt.family, action.Type)
			if !ok {
				t.Fatalf("catalog is missing %s action %q", tt.family, action.Type)
			}
			if spec.validateInScript == nil {
				t.Fatalf("catalog action %q has no configuration-aware residual validator", action.Type)
			}
			direct, handled := validateCatalogActionInScript(tt.family, "actions[0]", action, tt.script)
			if !handled {
				t.Fatalf("catalog did not handle %s action %q", tt.family, action.Type)
			}
			if !slices.Contains(direct, tt.want) {
				t.Fatalf("catalog validation = %#v, want issue %q", direct, tt.want)
			}
			if got := ValidateScriptIssues(tt.script); !slices.Contains(got, tt.want) {
				t.Fatalf("script validation = %#v, want catalog issue %q", got, tt.want)
			}
		})
	}
}
