package api

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func intPtr(v int) *int { return &v }

// Every native protocol section is wired through each path by the same JSON
// key: the override and ScriptConfig share it, override_fields accepts it,
// apply-to-groups and the command center know it, and the legacy field list
// names it.
func TestRuntimeProtocolSectionsAreWiredEverywhere(t *testing.T) {
	want := []string{"kafka", "jms", "eventhubs", "grpc", "database", "network", "appium", "java_http"}
	if !slices.Equal(runtimeProtocolOverrideSections, want) {
		t.Fatalf("sections = %v, want %v", runtimeProtocolOverrideSections, want)
	}
	for _, key := range want {
		if !allowedScriptConfigOverrideSections[key] {
			t.Errorf("%s: override_fields rejects the section", key)
		}
		if !runtimeOverrideFieldKeys[key] || !commandCenterRuntimeFieldKeys[key] {
			t.Errorf("%s: not an apply-to-groups / command-center field", key)
		}
		if got, err := normalizeOneRuntimeOverrideField("config." + key); err != nil || got != key {
			t.Errorf("%s: normalize = %q, %v", key, got, err)
		}
	}
	o := &RuntimeSettingsOverride{
		Kafka: &dsl.KafkaConfig{Topic: "t"}, JMS: &dsl.JMSConfig{Destination: "q"}, EventHubs: &dsl.EventHubsConfig{EventHub: "h"},
		GRPC: &dsl.GRPCConfig{Authority: "a"}, Database: &dsl.DatabaseConfig{MaxRows: 1}, Network: &dsl.NetworkConfig{Host: "h"},
		Appium: &dsl.AppiumConfig{ElementTimeoutMs: 1}, JavaHTTP: &dsl.JavaHTTPConfig{Codec: "json"},
	}
	if got := effectiveLegacyOverrideFields(o); !slices.Equal(got, want) {
		t.Errorf("legacy fields = %v, want %v", got, want)
	}
	if got := runtimeOverrideFieldNames(o); !slices.Equal(got, want) {
		t.Errorf("archive field names = %v, want %v", got, want)
	}
	if runtimeSettingsOverrideEmpty(&RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{}}) {
		t.Error("an override carrying only an empty kafka section is not empty (v1 clears the section)")
	}
	if projectRuntimeFields(o, []string{"database"}).Database == nil || projectRuntimeFields(o, []string{"database"}).Kafka != nil {
		t.Error("projectRuntimeFields must keep only the requested section")
	}
}

func TestRuntimeProtocolSectionsV1ReplaceWholeSection(t *testing.T) {
	script := &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{
		Kafka:    dsl.KafkaConfig{Topic: "a", ClientID: "keep-me", Properties: map[string]string{"x": "1"}},
		Database: dsl.DatabaseConfig{Driver: "sqlite", ReadOnly: true},
		JavaHTTP: dsl.JavaHTTPConfig{Codec: "json", Accept: "application/json"},
	}}
	overrides := &RuntimeSettingsOverride{
		Kafka:    &dsl.KafkaConfig{Topic: "b"},
		Database: &dsl.DatabaseConfig{},
	}
	original := *script
	if err := applyRuntimeOverrides(script, overrides); err != nil {
		t.Fatal(err)
	}
	if script.Config.Kafka.Topic != "b" || script.Config.Kafka.ClientID != "" || script.Config.Kafka.Properties != nil {
		t.Errorf("kafka = %+v, want whole-section replace", script.Config.Kafka)
	}
	if !reflect.DeepEqual(script.Config.Database, dsl.DatabaseConfig{}) {
		t.Errorf("an empty v1 database section must clear it, got %+v", script.Config.Database)
	}
	if script.Config.JavaHTTP.Accept != "application/json" {
		t.Errorf("an absent section must keep the script's, got %+v", script.Config.JavaHTTP)
	}
	if original.Config.Kafka.Topic != "a" || original.Config.Kafka.Properties["x"] != "1" {
		t.Error("applying overrides mutated the caller's script")
	}
	overrides.Kafka.Topic = "mutated"
	if script.Config.Kafka.Topic != "b" {
		t.Error("the applied section aliases the override")
	}
}

func TestRuntimeProtocolSectionsPartialOverrideFields(t *testing.T) {
	owner := int64(0)
	script := &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{
		Kafka:     dsl.KafkaConfig{Topic: "a", ClientID: "keep", Properties: map[string]string{"keep": "1", "drop": "2"}, Producer: &dsl.KafkaProducerConfig{Retries: intPtr(3), LingerMs: intPtr(5)}},
		Database:  dsl.DatabaseConfig{Driver: "sqlite", ReadOnly: true, MaxRows: 10},
		EventHubs: dsl.EventHubsConfig{EventHub: "h"},
	}}
	overrides := &RuntimeSettingsOverride{
		Kafka:     &dsl.KafkaConfig{Topic: "b", ClientID: "ignored", Properties: map[string]string{"new": "3"}, Producer: &dsl.KafkaProducerConfig{Retries: intPtr(0)}},
		Database:  &dsl.DatabaseConfig{ReadOnly: false, MaxRows: 99},
		EventHubs: &dsl.EventHubsConfig{OwnerLevel: &owner},
		OverrideFields: []string{
			"config.kafka.topic", "kafka.producer.retries", "kafka.properties.new", "kafka.properties.drop",
			"database.read_only", "eventhubs.owner_level",
		},
	}
	if err := applyRuntimeOverrides(script, overrides); err != nil {
		t.Fatal(err)
	}
	k := script.Config.Kafka
	if k.Topic != "b" || k.ClientID != "keep" {
		t.Errorf("kafka topic/client_id = %q/%q", k.Topic, k.ClientID)
	}
	if want := map[string]string{"keep": "1", "new": "3"}; !reflect.DeepEqual(k.Properties, want) {
		t.Errorf("properties = %v, want %v", k.Properties, want)
	}
	if k.Producer == nil || k.Producer.Retries == nil || *k.Producer.Retries != 0 || k.Producer.LingerMs == nil || *k.Producer.LingerMs != 5 {
		t.Errorf("producer = %+v, want retries 0 (explicit) and linger kept", k.Producer)
	}
	if script.Config.Database.ReadOnly || script.Config.Database.MaxRows != 10 || script.Config.Database.Driver != "sqlite" {
		t.Errorf("database = %+v, want only read_only overridden to false", script.Config.Database)
	}
	if script.Config.EventHubs.OwnerLevel == nil || *script.Config.EventHubs.OwnerLevel != 0 || script.Config.EventHubs.EventHub != "h" {
		t.Errorf("eventhubs = %+v, want explicit owner_level 0", script.Config.EventHubs)
	}
}

func TestRuntimeProtocolSectionsMergeAndClone(t *testing.T) {
	base := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Topic: "base"}, Network: &dsl.NetworkConfig{Host: "base"}}
	group := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{ClientID: "group"}}
	merged := mergeRuntimeSettings(base, group)
	if merged.Kafka.ClientID != "group" || merged.Kafka.Topic != "" || merged.Network.Host != "base" {
		t.Errorf("v1 merge = kafka %+v network %+v; group section must replace, absent section inherit", merged.Kafka, merged.Network)
	}
	merged.Kafka.ClientID = "mutated"
	if group.Kafka.ClientID != "group" {
		t.Error("merge aliases the group section")
	}

	partialBase := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Topic: "base"}, OverrideFields: []string{"kafka.topic"}}
	partialGroup := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{ClientID: "group"}, OverrideFields: []string{"kafka.client_id"}}
	merged = mergeRuntimeSettings(partialBase, partialGroup)
	if merged.Kafka == nil || merged.Kafka.Topic != "base" || merged.Kafka.ClientID != "group" {
		t.Fatalf("partial merge kafka = %+v", merged.Kafka)
	}
	if !slices.Equal(merged.OverrideFields, []string{"kafka.topic", "kafka.client_id"}) {
		t.Errorf("partial merge fields = %v", merged.OverrideFields)
	}

	original := &RuntimeSettingsOverride{Appium: &dsl.AppiumConfig{Capabilities: map[string]any{"opts": map[string]any{"k": "v"}}}, GRPC: &dsl.GRPCConfig{Metadata: map[string]string{"a": "b"}}}
	clone := cloneRuntimeSettings(original)
	clone.GRPC.Metadata["a"] = "changed"
	clone.Appium.Capabilities["opts"].(map[string]any)["k"] = "changed"
	if original.GRPC.Metadata["a"] != "b" || original.Appium.Capabilities["opts"].(map[string]any)["k"] != "v" {
		t.Error("cloneRuntimeSettings shares protocol section maps")
	}
}

// Each WebSocket leaf listed alone changes only that leaf, and a v1 empty
// websocket section still clears the whole section.
func TestRuntimeWebSocketLeavesOverrideIndividually(t *testing.T) {
	base := dsl.WebSocketConfig{CallbackIntervalMs: 10, ReadTimeoutMs: 20, WriteTimeoutMs: 30, MaxMessageSize: 40, Subprotocol: "base", Origin: "https://base.example", Compression: true, Reconnect: true}
	for _, leaf := range []string{"callback_interval_ms", "read_timeout_ms", "write_timeout_ms", "max_message_size", "subprotocol", "origin", "compression", "reconnect"} {
		script := &dsl.Script{Protocol: "websocket", Config: dsl.ScriptConfig{WebSocket: base}}
		if err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{WebSocket: &dsl.WebSocketConfig{}, OverrideFields: []string{"websocket." + leaf}}); err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(script.Config.WebSocket)
		var gotMap, baseMap map[string]any
		_ = json.Unmarshal(got, &gotMap)
		raw, _ := json.Marshal(base)
		_ = json.Unmarshal(raw, &baseMap)
		delete(baseMap, leaf)
		if !reflect.DeepEqual(gotMap, baseMap) {
			t.Errorf("websocket.%s alone: got %v, want %v", leaf, gotMap, baseMap)
		}
	}
	script := &dsl.Script{Protocol: "websocket", Config: dsl.ScriptConfig{WebSocket: base}}
	if err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{WebSocket: &dsl.WebSocketConfig{}}); err != nil {
		t.Fatal(err)
	}
	if script.Config.WebSocket != (dsl.WebSocketConfig{}) {
		t.Errorf("v1 empty websocket section = %+v, want cleared", script.Config.WebSocket)
	}
}

func TestRuntimeJavaVuserTimeoutAndBraceOverrides(t *testing.T) {
	base := dsl.JavaVuserConfig{StartupTimeoutMs: 1000, ActionTimeoutMs: 2000, ParamLeftBrace: "<", ParamRightBrace: ">", JDKHome: "/jdk"}
	// v1: non-zero wins, zero keeps the script's value.
	script := &dsl.Script{Protocol: "java_vuser", Config: dsl.ScriptConfig{JavaVuser: base}}
	if err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{ActionTimeoutMs: 5000, ParamLeftBrace: "[[", ParamRightBrace: "]]"}}); err != nil {
		t.Fatal(err)
	}
	j := script.Config.JavaVuser
	if j.StartupTimeoutMs != 1000 || j.ActionTimeoutMs != 5000 || j.ParamLeftBrace != "[[" || j.ParamRightBrace != "]]" || j.JDKHome != "/jdk" {
		t.Errorf("v1 java = %+v", j)
	}
	// Partial: listing a leaf resets it, even to zero.
	script = &dsl.Script{Protocol: "java_vuser", Config: dsl.ScriptConfig{JavaVuser: base}}
	if err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{}, OverrideFields: []string{"java_vuser.startup_timeout_ms", "java_vuser.param_left_brace", "java_vuser.param_right_brace"}}); err != nil {
		t.Fatal(err)
	}
	j = script.Config.JavaVuser
	if j.StartupTimeoutMs != 0 || j.ActionTimeoutMs != 2000 || j.ParamLeftBrace != "" || j.ParamRightBrace != "" {
		t.Errorf("partial java = %+v", j)
	}
	// Group merge (v1) is per field.
	merged := mergeRuntimeSettings(
		&RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{StartupTimeoutMs: 1, ParamLeftBrace: "<", ParamRightBrace: ">"}},
		&RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{ActionTimeoutMs: 2, ParamRightBrace: "}}"}},
	)
	if got := *merged.JavaVuser; got.StartupTimeoutMs != 1 || got.ActionTimeoutMs != 2 || got.ParamLeftBrace != "<" || got.ParamRightBrace != "}}" {
		t.Errorf("merged java = %+v", got)
	}
	// Partial merge keeps listed leaves from both layers.
	merged = mergeRuntimeSettings(
		&RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{StartupTimeoutMs: 7}, OverrideFields: []string{"java_vuser.startup_timeout_ms"}},
		&RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{ActionTimeoutMs: 9}, OverrideFields: []string{"java_vuser.action_timeout_ms"}},
	)
	if got := *merged.JavaVuser; got.StartupTimeoutMs != 7 || got.ActionTimeoutMs != 9 {
		t.Errorf("partial merged java = %+v", got)
	}
	fields := effectiveLegacyOverrideFields(&RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{StartupTimeoutMs: 1, ActionTimeoutMs: 1, ParamLeftBrace: "<", ParamRightBrace: ">"}})
	if want := []string{"java_vuser.startup_timeout_ms", "java_vuser.action_timeout_ms", "java_vuser.param_left_brace", "java_vuser.param_right_brace"}; !slices.Equal(fields, want) {
		t.Errorf("legacy java fields = %v, want %v", fields, want)
	}
	if projected := runtimeJavaVuserOverrideFromConfig(dsl.JavaVuserConfig{ActionTimeoutMs: 3}); projected == nil || projected.ActionTimeoutMs != 3 {
		t.Errorf("script projection = %+v", projected)
	}
	for _, path := range []string{"java_vuser.startup_timeout_ms", "java_vuser.action_timeout_ms", "java_vuser.param_left_brace", "java_vuser.param_right_brace"} {
		if _, err := normalizeOneRuntimeOverrideField(path); err != nil {
			t.Errorf("%s rejected: %v", path, err)
		}
	}
}

func TestRuntimeProtocolFieldApplicability(t *testing.T) {
	for _, tc := range []struct {
		field, protocol string
		want            bool
	}{
		{"kafka", "kafka", true}, {"kafka", "http", false}, {"jms", "jms-solace", true}, {"jms", "jms-servicebus", true},
		{"jms", "kafka", false}, {"eventhubs", "eventhubs", true}, {"eventhubs", "kafka", false}, {"grpc", "grpc", true},
		{"database", "database", true}, {"network", "network", true}, {"appium", "appium", true},
		{"java_http", "java_http", true}, {"java_http", "http", false}, {"database", "browser", false},
	} {
		if got := fieldApplicableForProtocol(tc.field, tc.protocol); got != tc.want {
			t.Errorf("fieldApplicableForProtocol(%q, %q) = %v, want %v", tc.field, tc.protocol, got, tc.want)
		}
	}
}

const protocolSecret = "Hunter2-protocol-secret"

func protocolSecretOverride() *RuntimeSettingsOverride {
	return &RuntimeSettingsOverride{
		Kafka: &dsl.KafkaConfig{
			SASLMechanism: "plain", SASLUsername: "kafka-user", SASLPassword: protocolSecret,
			OAuth:             &dsl.KafkaOAuthConfig{Mode: "client_credentials", ClientID: "cid", ClientSecret: protocolSecret},
			SchemaRegistryURL: "https://sr-user:" + protocolSecret + "@sr.example.test",
			Brokers:           []string{"k1.example.test:9092", "u:" + protocolSecret + "@k2.example.test:9092"},
			Properties: map[string]string{
				"sasl.jaas.config":   `org.apache.kafka.common.security.plain.PlainLoginModule required username="u" password="` + protocolSecret + `";`,
				"ssl.key.password":   protocolSecret,
				"request.timeout.ms": "45000",
			},
		},
		JMS:       &dsl.JMSConfig{Username: "jms-user", Password: protocolSecret, BrokerURL: "tcp://b:" + protocolSecret + "@broker.example.test:55555", Properties: map[string]string{"conn": "Endpoint=sb://ns/;SharedAccessKeyName=send;SharedAccessKey=" + protocolSecret}},
		EventHubs: &dsl.EventHubsConfig{ConnectionString: "Endpoint=sb://ns/;SharedAccessKey=" + protocolSecret, CheckpointContainerURL: "https://acct.blob.core.windows.net/c?sv=1&sig=" + protocolSecret},
		GRPC:      &dsl.GRPCConfig{Metadata: map[string]string{"authorization": "Bearer " + protocolSecret, "x-tenant": "east"}},
		Database:  &dsl.DatabaseConfig{Driver: "postgres", DSN: "postgres://app:" + protocolSecret + "@db.example.test/app"},
		Network:   &dsl.NetworkConfig{Username: "mail-user", Password: protocolSecret},
		Appium: &dsl.AppiumConfig{
			ServerURL:    "https://cloud-user:" + protocolSecret + "@hub.example.test/wd/hub",
			Capabilities: map[string]any{"platformName": "iOS", "cloud:options": map[string]any{"accessKey": protocolSecret, "build": "nightly"}},
		},
	}
}

func TestRuntimeProtocolSecretsAreProtectedForExecution(t *testing.T) {
	protected, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, protocolSecretOverride(), false)
	if err != nil {
		t.Fatalf("protect: %v", err)
	}
	payload, _ := json.Marshal(protected)
	for _, leaked := range []string{protocolSecret, "kafka-user", "jms-user", "mail-user"} {
		if strings.Contains(string(payload), leaked) {
			t.Errorf("protected override still holds %q: %s", leaked, payload)
		}
	}
	for _, kept := range []string{"k1.example.test:9092", `"request.timeout.ms":"45000"`, `"x-tenant":"east"`, `"build":"nightly"`, `"platformName":"iOS"`, `"client_id":"cid"`} {
		if !strings.Contains(string(payload), kept) {
			t.Errorf("non-credential value %s was protected: %s", kept, payload)
		}
	}
	found := map[string]bool{}
	for _, value := range values {
		found[value] = true
	}
	for _, want := range []string{protocolSecret, "kafka-user", "postgres://app:" + protocolSecret + "@db.example.test/app", "Bearer " + protocolSecret} {
		if !found[want] {
			t.Errorf("runtime secret values lack %q", want)
		}
	}
	for _, ref := range []string{protected.Kafka.SASLPassword, protected.Kafka.OAuth.ClientSecret, protected.JMS.Password, protected.EventHubs.ConnectionString, protected.Database.DSN, protected.Network.Password, protected.Kafka.Brokers[1]} {
		if !isReservedRuntimeOverrideSecretReference(ref) {
			t.Errorf("%q is not a controller-owned reference", ref)
		}
	}
	// A username with no active credential is an identity, not a secret.
	plain, _, err := protectRuntimeOverrideSecrets("scope", 0, nil, &RuntimeSettingsOverride{Network: &dsl.NetworkConfig{Username: "ops"}, JMS: &dsl.JMSConfig{Username: "ops"}}, false)
	if err != nil || plain.Network.Username != "ops" || plain.JMS.Username != "ops" {
		t.Errorf("inactive usernames = %+v / %+v, %v", plain.Network, plain.JMS, err)
	}
}

func TestRuntimeProtocolSecretReferencesAndNameFields(t *testing.T) {
	ok := &RuntimeSettingsOverride{
		Kafka: &dsl.KafkaConfig{
			SASLPassword: "{{secret.KAFKA_PASSWORD}}", SASLPasswordSecret: "kafka_password",
			OAuth:      &dsl.KafkaOAuthConfig{ClientSecretRef: "{{secret.OAUTH_SECRET}}"},
			Properties: map[string]string{"sasl.jaas.config": `PlainLoginModule required username="u" password="{{secret.KAFKA_PASSWORD}}";`},
		},
		Database:  &dsl.DatabaseConfig{DSNSecret: "orders_dsn"},
		EventHubs: &dsl.EventHubsConfig{ConnectionStringSecret: "EH_CONN"},
		JMS:       &dsl.JMSConfig{TLS: &dsl.JMSTLSConfig{CACertSecret: "{{secret.CA}}"}},
	}
	protected, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, ok, false)
	if err != nil || len(values) != 0 || !reflect.DeepEqual(protected.Kafka, ok.Kafka) {
		t.Fatalf("references must pass through unchanged: %+v values=%v err=%v", protected, values, err)
	}
	for name, bad := range map[string]*RuntimeSettingsOverride{
		"dsn literal in dsn_secret":           {Database: &dsl.DatabaseConfig{DSNSecret: "postgres://user:" + protocolSecret + "@db/app"}},
		"mysql dsn in dsn_secret":             {Database: &dsl.DatabaseConfig{DSNSecret: "app:" + protocolSecret + "@tcp(db:3306)/orders"}},
		"reserved name":                       {Kafka: &dsl.KafkaConfig{SASLPasswordSecret: runtimeOverrideSecretPrefix + "G1_ABC"}},
		"reserved reference":                  {EventHubs: &dsl.EventHubsConfig{ConnectionStringSecret: "{{secret." + runtimeOverrideSecretPrefix + "G1_ABC}}"}},
		"mixed literal and reference":         {Kafka: &dsl.KafkaConfig{SASLPassword: "x{{secret.A}}"}},
		"credential inside a capability list": {Appium: &dsl.AppiumConfig{Capabilities: map[string]any{"args": []any{"--token=" + protocolSecret}}}},
		"jaas literal beside reference":       {Kafka: &dsl.KafkaConfig{Properties: map[string]string{"sasl.jaas.config": `username="{{secret.U}}" password="` + protocolSecret + `";`}}},
	} {
		if _, _, err := protectRuntimeOverrideSecrets("scope", 0, nil, bad, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRuntimeProtocolSecretsAreMaskedOnRead(t *testing.T) {
	input := protocolSecretOverride()
	before, _ := json.Marshal(input)
	for name, mask := range map[string]func(*RuntimeSettingsOverride) *RuntimeSettingsOverride{
		"stored": maskRuntimeSettings, "editor": maskRuntimeSettingsForEditor,
	} {
		masked := mask(input)
		payload, _ := json.Marshal(masked)
		for _, leaked := range []string{protocolSecret, "kafka-user", "jms-user", "mail-user"} {
			if strings.Contains(string(payload), leaked) {
				t.Errorf("%s mask leaks %q: %s", name, leaked, payload)
			}
		}
		if masked.Kafka.SASLPassword != scrub.Redacted || masked.Database.DSN != scrub.Redacted || masked.GRPC.Metadata["x-tenant"] != "east" || masked.Kafka.Brokers[0] != "k1.example.test:9092" {
			t.Errorf("%s mask = kafka %+v database %+v grpc %+v", name, masked.Kafka, masked.Database, masked.GRPC)
		}
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Error("masking mutated its input")
	}
	// The protected form stays masked.
	protected, _, _ := protectRuntimeOverrideSecrets("scope", 0, nil, protocolSecretOverride(), false)
	if masked := maskRuntimeSettingsForEditor(protected); masked.Kafka.SASLPassword != scrub.Redacted || masked.Kafka.Properties["ssl.key.password"] != scrub.Redacted {
		t.Errorf("reserved references are not masked: %+v", masked.Kafka)
	}
}

func TestRuntimeProtocolSecretsReconcileMaskedEcho(t *testing.T) {
	prior := protocolSecretOverride()
	incoming := maskRuntimeSettings(prior)
	incoming.Kafka.Topic = "edited"
	reconciled := reconcileGroupRuntimeSecrets(incoming, prior)
	priorJSON, _ := json.Marshal(prior.Kafka)
	reconciled.Kafka.Topic = ""
	gotJSON, _ := json.Marshal(reconciled.Kafka)
	if string(priorJSON) != string(gotJSON) {
		t.Errorf("kafka round trip = %s, want %s", gotJSON, priorJSON)
	}
	for _, pair := range [][2]any{
		{reconciled.JMS, prior.JMS}, {reconciled.EventHubs, prior.EventHubs}, {reconciled.GRPC, prior.GRPC},
		{reconciled.Database, prior.Database}, {reconciled.Network, prior.Network}, {reconciled.Appium, prior.Appium},
	} {
		got, _ := json.Marshal(pair[0])
		want, _ := json.Marshal(pair[1])
		if string(got) != string(want) {
			t.Errorf("round trip = %s, want %s", got, want)
		}
	}
	// "" clears; a sentinel with no prior is dropped, never stored.
	cleared := reconcileGroupRuntimeSecrets(&RuntimeSettingsOverride{
		Kafka:   &dsl.KafkaConfig{SASLPassword: "", Properties: map[string]string{"ssl.key.password": scrub.Redacted}, Brokers: []string{scrub.Redacted}},
		Network: &dsl.NetworkConfig{Password: scrub.Redacted},
	}, nil)
	if cleared.Kafka.SASLPassword != "" || len(cleared.Kafka.Properties) != 0 || len(cleared.Kafka.Brokers) != 0 || cleared.Network.Password != "" {
		t.Errorf("orphan sentinels kept: kafka %+v network %+v", cleared.Kafka, cleared.Network)
	}
	// A broker list whose length changed cannot restore by position: the
	// sentinel is kept so the save is refused rather than silently dropping
	// or misplacing a credential broker.
	reordered := reconcileGroupRuntimeSecrets(&RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Brokers: []string{scrub.Redacted}}}, prior)
	if len(reordered.Kafka.Brokers) != 1 || reordered.Kafka.Brokers[0] != scrub.Redacted {
		t.Errorf("broker sentinel restored across a length change: %v", reordered.Kafka.Brokers)
	}
	if _, _, _, err := protectLegacyRuntimeOverrideSecrets("scope", 0, nil, reordered); err == nil || !strings.Contains(err.Error(), "re-enter the credential brokers") {
		t.Errorf("unmatched broker sentinel saved: %v", err)
	}
}

func TestExecutionPlanRejectsTypedProtocolCredentials(t *testing.T) {
	paths := executionPlanTypedCredentialPaths(protocolSecretOverride())
	for _, want := range []string{
		"kafka.sasl_password", "kafka.oauth.client_secret", "kafka.schema_registry_url", "kafka.brokers.1",
		"kafka.properties.sasl.jaas.config", "kafka.properties.ssl.key.password", "jms.password", "jms.broker_url",
		"jms.properties.conn", "eventhubs.connection_string", "eventhubs.checkpoint_container_url",
		"grpc.metadata.authorization", "database.dsn", "network.password", "appium.server_url",
		"appium.capabilities.cloud:options.accessKey",
	} {
		if !slices.Contains(paths, want) {
			t.Errorf("plan check misses %s (got %v)", want, paths)
		}
	}
	for _, unexpected := range []string{"kafka.sasl_username", "kafka.brokers.0", "kafka.properties.request.timeout.ms", "grpc.metadata.x-tenant", "network.username"} {
		if slices.Contains(paths, unexpected) {
			t.Errorf("plan check flags non-credential %s", unexpected)
		}
	}
	references := &RuntimeSettingsOverride{
		Kafka:    &dsl.KafkaConfig{SASLPassword: "{{secret.KAFKA}}", SASLPasswordSecret: "KAFKA", Properties: map[string]string{"sasl.jaas.config": `password="{{secret.KAFKA}}";`}},
		Database: &dsl.DatabaseConfig{DSN: "{{secret.DSN}}"},
		GRPC:     &dsl.GRPCConfig{Metadata: map[string]string{"authorization": "Bearer {{secret.TOKEN}}"}},
	}
	if got := executionPlanTypedCredentialPaths(references); len(got) != 0 {
		t.Errorf("references flagged: %v", got)
	}
	reserved := &RuntimeSettingsOverride{
		Database: &dsl.DatabaseConfig{DSN: "{{secret." + runtimeOverrideSecretPrefix + "G1_A}}", DSNSecret: "postgres://u:p@db/app"},
		Kafka:    &dsl.KafkaConfig{Properties: map[string]string{"x": "a {{secret." + runtimeOverrideSecretPrefix + "G1_A}}"}},
	}
	if got := executionPlanTypedCredentialPaths(reserved); !slices.Contains(got, "database.dsn") || !slices.Contains(got, "database.dsn_secret") || !slices.Contains(got, "kafka.properties.x") {
		t.Errorf("reserved references / literal secret names not flagged: %v", got)
	}
	// A stored row that predates the check is masked on read.
	row, _ := json.Marshal(protocolSecretOverride())
	masked, changed := maskExecutionPlanRuntimeOverridesRow(row)
	if !changed || strings.Contains(string(masked), protocolSecret) {
		t.Errorf("plan row mask leaks: changed=%v %s", changed, masked)
	}
}

// Apply-to-groups copies a section's settings but never its credentials: the
// target inherits them from its own script (override_fields leave them out),
// or keeps the credential its own prior override set.
func TestApplyToGroupsKeepsProtocolCredentialsLocal(t *testing.T) {
	source := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{
		Topic: "copied", SASLMechanism: "plain", SASLUsername: "src-user", SASLPassword: "src-secret",
		Properties: map[string]string{"ssl.key.password": "src-key", "acks": "all"},
	}}
	next := replaceRuntimeFields(nil, source, []string{"kafka"})
	next = keepTargetProtocolCredentials(next, nil, "kafka")
	if next.Kafka.SASLPassword != "" || next.Kafka.SASLUsername != "" || next.Kafka.Properties["ssl.key.password"] != "" || next.Kafka.Topic != "copied" {
		t.Fatalf("copied kafka = %+v", next.Kafka)
	}
	for _, path := range []string{"kafka.sasl_password", "kafka.sasl_username", "kafka.properties.ssl.key.password"} {
		if overrideFieldsCoverPath(next.OverrideFields, path) {
			t.Errorf("override_fields %v cover credential %s", next.OverrideFields, path)
		}
	}
	for _, path := range []string{"kafka.topic", "kafka.sasl_mechanism", "kafka.properties.acks"} {
		if !overrideFieldsCoverPath(next.OverrideFields, path) {
			t.Errorf("override_fields %v drop copied %s", next.OverrideFields, path)
		}
	}
	script := &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{SASLUsername: "own-user", SASLPassword: "{{secret.OWN}}", Properties: map[string]string{"ssl.key.password": "{{secret.OWN_KEY}}"}}}}
	if err := applyRuntimeOverrides(script, next); err != nil {
		t.Fatal(err)
	}
	if k := script.Config.Kafka; k.SASLUsername != "own-user" || k.SASLPassword != "{{secret.OWN}}" || k.Properties["ssl.key.password"] != "{{secret.OWN_KEY}}" || k.Properties["acks"] != "all" || k.Topic != "copied" {
		t.Errorf("target kafka after copy = %+v", k)
	}

	prior := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{SASLPassword: "{{secret." + runtimeOverrideSecretPrefix + "G2_OWN}}"}}
	kept := keepTargetProtocolCredentials(replaceRuntimeFields(prior, source, []string{"kafka"}), prior, "kafka")
	if kept.Kafka.SASLPassword != prior.Kafka.SASLPassword {
		t.Errorf("target's own sealed password = %q, want kept", kept.Kafka.SASLPassword)
	}
	if !overrideFieldsCoverPath(kept.OverrideFields, "kafka.sasl_password") {
		t.Errorf("kept own password is not listed: %v", kept.OverrideFields)
	}
	// A section without credentials copies as v1.
	plain := keepTargetProtocolCredentials(replaceRuntimeFields(nil, &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Topic: "t"}}, []string{"kafka"}), nil, "kafka")
	if len(plain.OverrideFields) != 0 || plain.Kafka.Topic != "t" {
		t.Errorf("credential-free copy = %+v", plain)
	}
}

// Protected references pass script validation, so overriding a credential
// never turns a valid script invalid.
func TestProtectedProtocolOverridesStillValidate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		script    *dsl.Script
		overrides *RuntimeSettingsOverride
	}{
		{
			name:      "kafka sasl",
			script:    &dsl.Script{Name: "k", Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true, Topic: "orders"}}, Actions: []dsl.Action{{Name: "produce", Type: "kafka_produce", Body: "{}"}}},
			overrides: &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Mock: true, Topic: "orders", SecurityProtocol: "sasl_ssl", SASLMechanism: "plain", SASLUsername: "u", SASLPassword: protocolSecret}},
		},
		{
			name:      "database dsn",
			script:    &dsl.Script{Name: "d", Protocol: "database", Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{Driver: "postgres", DSNSecret: "orders_dsn"}}, Actions: []dsl.Action{{Name: "ping", Type: "db_query", Target: "SELECT 1"}}},
			overrides: &RuntimeSettingsOverride{Database: &dsl.DatabaseConfig{Driver: "postgres", DSN: "postgres://app:" + protocolSecret + "@db.example.test/app"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if issues := validateStoredExecutionScript(tc.script); len(issues) != 0 {
				t.Fatalf("fixture invalid: %v", issues)
			}
			protected, values, err := protectRuntimeOverrideSecrets("scope", 0, tc.script, tc.overrides, true)
			if err != nil || len(values) == 0 {
				t.Fatalf("protect: values=%v err=%v", values, err)
			}
			script := *tc.script
			if err := applyRuntimeOverrides(&script, protected); err != nil {
				t.Fatal(err)
			}
			if issues := validateStoredExecutionScript(&script); len(issues) != 0 {
				t.Fatalf("protected override invalidates the script: %v", issues)
			}
			payload, _ := json.Marshal(script)
			if strings.Contains(string(payload), protocolSecret) {
				t.Fatalf("executed script holds the credential: %s", payload)
			}
		})
	}
}

func kafkaScriptForProtocolApply(id string) *dsl.Script {
	return &dsl.Script{
		ID: id, Name: id, Protocol: "kafka", Version: "1.0",
		Config:  dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true, Topic: "orders"}},
		Actions: []dsl.Action{{Name: "produce", Type: "kafka_produce", Body: "{}"}},
	}
}

// A scenario stores a typed Kafka SASL password sealed, returns it masked,
// reconciles an echoed mask back to the sealed reference, and apply-to-groups
// copies the section without the source group's credential.
func TestScenarioKafkaOverrideCredentialLifecycle(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	server.runner.StoreScript(kafkaScriptForProtocolApply("kafka-source"))
	server.runner.StoreScript(kafkaScriptForProtocolApply("kafka-target"))
	kafka := func(topic, password string) *RuntimeSettingsOverride {
		return &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Mock: true, Topic: topic, SecurityProtocol: "sasl_ssl", SASLMechanism: "plain", SASLUsername: "kafka-user", SASLPassword: password}}
	}
	request := CreateScenarioRequest{
		Name: "Kafka credentials", Duration: "1m",
		Groups: []CreateGroupEntry{
			{Name: "Source", ScriptID: "kafka-source", VUsers: 1, RuntimeSettings: kafka("copied-topic", "source-typed-secret")},
			{Name: "Target", ScriptID: "kafka-target", VUsers: 1, RuntimeSettings: kafka("target-topic", "target-typed-secret")},
		},
	}
	scenario, err := server.runner.StoreScenario(request)
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	stored := server.runner.scenarios[scenario.ID]
	sourceRef := stored.Scripts[0].RuntimeSettings.Kafka.SASLPassword
	targetRef := stored.Scripts[1].RuntimeSettings.Kafka.SASLPassword
	if !isReservedRuntimeOverrideSecretReference(sourceRef) || !isReservedRuntimeOverrideSecretReference(targetRef) || sourceRef == targetRef {
		t.Fatalf("typed passwords must be sealed per group: %q / %q", sourceRef, targetRef)
	}
	masked, _ := json.Marshal(maskStoredScenarioForEditor(stored))
	if strings.Contains(string(masked), "typed-secret") || strings.Contains(string(masked), runtimeOverrideSecretPrefix) {
		t.Fatalf("scenario read leaks a credential or internal reference: %s", masked)
	}

	_, ok, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"kafka"}})
	if err != nil || !ok {
		t.Fatalf("ApplyScenarioRuntimeToGroups: ok=%v err=%v", ok, err)
	}
	target := server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings
	if target == nil || target.Kafka == nil || target.Kafka.Topic != "copied-topic" {
		t.Fatalf("target kafka = %+v, want the copied topic", target)
	}
	if target.Kafka.SASLPassword != targetRef {
		t.Fatalf("target password = %q, want its own sealed %q (never the source's %q)", target.Kafka.SASLPassword, targetRef, sourceRef)
	}
}
