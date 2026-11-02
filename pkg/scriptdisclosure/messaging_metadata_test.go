package scriptdisclosure

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A Kafka or JMS send delivers import_action_uid as a record header or message
// property, so a known credential there must be masked like any other header
// value. On a step that never sends its metadata the UID stays verbatim.
func TestProjectMasksKnownCredentialInSentMessagingActionUID(t *testing.T) {
	const secret = "fixture-credential-Disclosure0123456789abcdef"
	script := &dsl.Script{
		ID: "kafka-uid", Name: "Kafka UID", Version: "1", Protocol: "kafka",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Username: "operator", Password: secret},
			},
		},
		Actions: []dsl.Action{
			{
				Name: "produce", Type: "kafka_produce", Target: "orders",
				Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: secret},
			},
			{
				Name: "publish", Type: "jms_publish", Target: "queue://orders",
				Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: secret},
			},
			{
				Name: "consume", Type: "kafka_consume", Target: "orders",
				Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: secret},
			},
		},
	}
	projected, err := ProjectWithCredentialSource(script, script)
	if err != nil {
		t.Fatalf("ProjectWithCredentialSource: %v", err)
	}
	for index := 0; index < 2; index++ {
		if got := projected.Actions[index].Metadata[dsl.ImportActionUIDMetadataKey]; got == secret {
			t.Fatalf("%s import_action_uid disclosed a known credential: %#v", script.Actions[index].Type, got)
		}
	}
	if got := projected.Actions[2].Metadata[dsl.ImportActionUIDMetadataKey]; got != secret {
		t.Fatalf("unsent import_action_uid = %#v, want it kept verbatim", got)
	}
}

// Only the metadata map of an action surface (a step, an http_sequence request
// or an http_async_start request) owns a server-stamped import_action_uid. A
// nested map that is merely named metadata, such as gRPC request metadata, is
// wire data: a credential there is masked whatever its key.
func TestProjectMasksImportActionUIDOutsideActionSurfaceMetadata(t *testing.T) {
	const secret = "fixture-credential-Disclosure0123456789abcdef"
	const uid = "act-0123456789abcdef"
	script := &dsl.Script{
		ID: "grpc-uid", Name: "gRPC UID", Version: "1", Protocol: "grpc",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Username: "operator", Password: secret},
			},
			GRPC: dsl.GRPCConfig{
				Metadata: map[string]string{dsl.ImportActionUIDMetadataKey: secret},
			},
		},
		Actions: []dsl.Action{
			{
				Name: "call", Type: "grpc_unary", Target: "svc/Method",
				Params: map[string]any{
					"map_body": map[string]any{
						"metadata": map[string]any{dsl.ImportActionUIDMetadataKey: secret},
					},
				},
				Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: uid},
			},
			{
				Name: "poll", Type: "http_async_start", Target: "/jobs",
				Params: map[string]any{
					"request": map[string]any{
						"method":   "GET",
						"target":   "/jobs/1",
						"metadata": map[string]any{dsl.ImportActionUIDMetadataKey: uid + "-async"},
					},
				},
			},
			{
				Name: "switch", Type: "control_switch",
				Control: &dsl.ControlFlow{
					Default: []dsl.Action{{
						Name: "fallback", Type: "think_time",
						Metadata: map[string]any{dsl.ImportActionUIDMetadataKey: uid + "-default"},
					}},
				},
			},
		},
	}
	projected, err := ProjectWithCredentialSource(script, script)
	if err != nil {
		t.Fatalf("ProjectWithCredentialSource: %v", err)
	}
	if got := projected.Config.GRPC.Metadata[dsl.ImportActionUIDMetadataKey]; got == secret {
		t.Fatalf("gRPC metadata import_action_uid disclosed a known credential: %q", got)
	}
	body, _ := projected.Actions[0].Params["map_body"].(map[string]any)
	nested, _ := body["metadata"].(map[string]any)
	if got := nested[dsl.ImportActionUIDMetadataKey]; got == secret {
		t.Fatalf("params metadata import_action_uid disclosed a known credential: %#v", got)
	}
	if got := projected.Actions[0].Metadata[dsl.ImportActionUIDMetadataKey]; got != uid {
		t.Fatalf("action import_action_uid = %#v, want %q", got, uid)
	}
	request, _ := projected.Actions[1].Params["request"].(map[string]any)
	requestMeta, _ := request["metadata"].(map[string]any)
	if got := requestMeta[dsl.ImportActionUIDMetadataKey]; got != uid+"-async" {
		t.Fatalf("async request import_action_uid = %#v, want it kept verbatim", got)
	}
	if projected.Actions[2].Control == nil || len(projected.Actions[2].Control.Default) != 1 {
		t.Fatalf("switch default branch lost: %#v", projected.Actions[2].Control)
	}
	if got := projected.Actions[2].Control.Default[0].Metadata[dsl.ImportActionUIDMetadataKey]; got != uid+"-default" {
		t.Fatalf("switch default import_action_uid = %#v, want it kept verbatim", got)
	}
}
