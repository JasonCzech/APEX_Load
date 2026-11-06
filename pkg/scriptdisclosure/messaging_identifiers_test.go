package scriptdisclosure

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Messaging imports carry identifiers the reviewer must read before saving: the
// OAuth token endpoint, an Event Hubs hub and SAS policy name, and the names
// and locator paths of the secrets a run needs. Name and shape heuristics hid
// them all as credentials (preview-over-redaction), and the token endpoint was
// then collected as a known credential, masking every copy of it.
func TestProjectKeepsMessagingIdentifiersVisible(t *testing.T) {
	const (
		tokenURL   = "https://login.example.test/tenant-0001/oauth2/v2.0/token"
		hub        = "hub-q7Xk2Lm9Pz4Rw8Tn3Vb6Yc1Hd5Jf0Gs7Ke2Ma9Nq4Ur8Wt3Zx6"
		policy     = "send-policy"
		secretName = "JMETER_KAFKA_Q7XK2LM9PZ4RW8TN3VB6YC1HD5JF0GS7KE2MA9NQ4UR8WT3ZX6_KAFKA_CLIENTID"
		pointer    = "/actions/0/metadata/jmeter_kafka/native_config/oauth/client_id"
		password   = "fixture-SaslPassword-0123456789abcdef"
	)
	script := &dsl.Script{
		ID: "messaging", Name: "Messaging", Version: "1", Protocol: "kafka",
		Config: dsl.ScriptConfig{
			Kafka: dsl.KafkaConfig{
				SASLPassword: password,
				OAuth: &dsl.KafkaOAuthConfig{
					Mode: "client_credentials", TokenURL: tokenURL,
					ClientID: "{{secret." + secretName + "}}",
				},
			},
		},
		Actions: []dsl.Action{
			{
				Name: "produce", Type: "kafka_produce", Target: "orders",
				Metadata: map[string]any{"jmeter_kafka": map[string]any{
					"effective_settings": map[string]any{"sasl.oauthbearer.token.endpoint.url": tokenURL},
				}},
			},
			{
				Name: "send", Type: "eventhubs_send", Target: hub,
				Params: map[string]any{"event_hub": hub},
				Metadata: map[string]any{"jmeter_eventhubs": map[string]any{
					"shared_access_key_name": policy,
					"native_config":          map[string]any{"event_hub": hub},
				}},
			},
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
			protectedCredentialRequirementsMetadataKey: []any{map[string]any{
				"suggested_name": secretName,
				"affected_paths": []any{pointer},
				"locations":      []any{map[string]any{"path": pointer}},
			}},
			"replay_preflight": map[string]any{
				"required_secrets": []any{secretName},
				"missing_secrets":  []any{secretName},
			},
		}}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Config.Kafka.OAuth.TokenURL; got != tokenURL {
		t.Fatalf("config token_url = %q, want it visible", got)
	}
	settings := projected.Actions[0].Metadata["jmeter_kafka"].(map[string]any)["effective_settings"].(map[string]any)
	if got := settings["sasl.oauthbearer.token.endpoint.url"]; got != tokenURL {
		t.Fatalf("effective token endpoint = %#v, want it visible", got)
	}
	if got := projected.Actions[1].Params["event_hub"]; got != hub {
		t.Fatalf("params.event_hub = %#v, want it visible", got)
	}
	eventHubs := projected.Actions[1].Metadata["jmeter_eventhubs"].(map[string]any)
	if got := eventHubs["shared_access_key_name"]; got != policy {
		t.Fatalf("shared_access_key_name = %#v, want the SAS policy name visible", got)
	}
	if got := eventHubs["native_config"].(map[string]any)["event_hub"]; got != hub {
		t.Fatalf("native_config.event_hub = %#v, want it visible", got)
	}
	preserved := projected.Metadata.Import.Preserved
	requirement := preserved[protectedCredentialRequirementsMetadataKey].([]any)[0].(map[string]any)
	if got := requirement["suggested_name"]; got != secretName {
		t.Fatalf("suggested_name = %#v, want the secret name visible", got)
	}
	if got := requirement["affected_paths"].([]any)[0]; got != pointer {
		t.Fatalf("affected_paths[0] = %#v, want the locator visible", got)
	}
	if got := requirement["locations"].([]any)[0].(map[string]any)["path"]; got != pointer {
		t.Fatalf("locations[0].path = %#v, want the locator visible", got)
	}
	preflight := preserved["replay_preflight"].(map[string]any)
	for _, key := range []string{"required_secrets", "missing_secrets"} {
		if got := preflight[key].([]any)[0]; got != secretName {
			t.Fatalf("%s[0] = %#v, want the secret name visible", key, got)
		}
	}
	// Real credentials still mask.
	if got := projected.Config.Kafka.SASLPassword; got != credential.Redacted {
		t.Fatalf("sasl_password = %q, want it masked", got)
	}
}

// The token endpoint is an address, so a credential in its userinfo still
// masks, and the same identifier keys still mask a known credential value.
func TestProjectMasksCredentialsInsideMessagingIdentifiers(t *testing.T) {
	const password = "fixture-TokenPassword-0123456789abcdef"
	tokenURL := "https://client:" + password + "@login.example.test/oauth2/token"
	script := &dsl.Script{
		ID: "messaging", Name: "Messaging", Version: "1", Protocol: "kafka",
		Config: dsl.ScriptConfig{
			Kafka: dsl.KafkaConfig{
				SASLPassword: password,
				OAuth:        &dsl.KafkaOAuthConfig{Mode: "client_credentials", TokenURL: tokenURL},
			},
		},
		Actions: []dsl.Action{{
			Name: "send", Type: "eventhubs_send", Target: "hub",
			Params: map[string]any{"event_hub": "hub-" + password},
			Metadata: map[string]any{"jmeter_eventhubs": map[string]any{
				"shared_access_key_name": password,
			}},
		}},
	}
	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Config.Kafka.OAuth.TokenURL; strings.Contains(got, password) {
		t.Fatalf("token_url = %q disclosed its userinfo password", got)
	}
	if got := projected.Actions[0].Params["event_hub"].(string); strings.Contains(got, password) {
		t.Fatalf("event_hub = %q disclosed a known credential", got)
	}
	if got := projected.Actions[0].Metadata["jmeter_eventhubs"].(map[string]any)["shared_access_key_name"]; got == password {
		t.Fatalf("shared_access_key_name disclosed a known credential")
	}
}

// The identifier exemption skips only the random-looking-text heuristic. A
// portal connection string pasted where a hub or SAS policy name belongs is not
// a known credential, but its SharedAccessKey must still mask, as it does under
// any other key.
func TestProjectMasksConnectionStringUnderMessagingIdentifierKeys(t *testing.T) {
	const sasKey = "Zq8vN3kLp0xR7tY2wE5uSyntheticFixture="
	cs := "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=" + sasKey + ";EntityPath=hub1"
	script := &dsl.Script{
		ID: "messaging", Name: "Messaging", Version: "1", Protocol: "eventhubs",
		Actions: []dsl.Action{{
			Name: "send", Type: "eventhubs_send", Target: "hub1",
			Params: map[string]any{"event_hub": cs, "other": cs},
			Metadata: map[string]any{"jmeter_eventhubs": map[string]any{
				"event_hub":              cs,
				"shared_access_key_name": cs,
				"native_config":          map[string]any{"event_hub": cs},
			}},
		}},
	}
	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	eventHubs := projected.Actions[0].Metadata["jmeter_eventhubs"].(map[string]any)
	for name, got := range map[string]any{
		"params.event_hub":                         projected.Actions[0].Params["event_hub"],
		"params.other":                             projected.Actions[0].Params["other"],
		"jmeter_eventhubs.event_hub":               eventHubs["event_hub"],
		"jmeter_eventhubs.shared_access_key_name":  eventHubs["shared_access_key_name"],
		"jmeter_eventhubs.native_config.event_hub": eventHubs["native_config"].(map[string]any)["event_hub"],
	} {
		text, _ := got.(string)
		if strings.Contains(text, sasKey) {
			t.Fatalf("%s disclosed the SharedAccessKey: %q", name, text)
		}
		if !strings.Contains(text, "example.servicebus.windows.net") {
			t.Fatalf("%s = %q, want only the key masked", name, text)
		}
	}
}
