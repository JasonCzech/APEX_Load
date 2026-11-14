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

const reservedBrowserRef = "{{secret." + runtimeOverrideSecretPrefix + "G1_ABCDEF}}"

func storedBrowserReferences() *RuntimeSettingsOverride {
	return &RuntimeSettingsOverride{BrowserConfig: &dsl.BrowserConfig{
		StorageState: "{{secret.STATE}}",
		Session: &dsl.BrowserSessionConfig{
			Identities: []dsl.BrowserIdentity{{Username: "u1", Password: "{{secret.PW1}}", StorageState: "{{secret.STATE_U1}}"}},
			JWT:        &dsl.BrowserSessionJWT{LoginURL: "https://login.salesforce.com", ClientID: "cid", PrivateKey: "{{secret.KEY}}"},
		},
	}}
}

// A browser_config stored before the session leaves were masked can hold only
// whole project-secret references there (the DSL requires it). The hash and
// export form must keep them byte-identical, so library hashes of unchanged
// content and exported bindings survive; sealed and literal values still mask.
func TestBrowserSessionReferencesKeepHashAndExportForm(t *testing.T) {
	stored := storedBrowserReferences()
	want, _ := json.Marshal(stored.BrowserConfig)
	for name, mask := range map[string]func(*RuntimeSettingsOverride) *RuntimeSettingsOverride{
		"hash": maskRuntimeSettings, "editor": maskRuntimeSettingsForEditor,
	} {
		got, _ := json.Marshal(mask(stored).BrowserConfig)
		if string(got) != string(want) {
			t.Errorf("%s mask rewrote project references:\n got %s\nwant %s", name, got, want)
		}
	}
	hashOf := func(o *RuntimeSettingsOverride) string {
		return hashLibraryScenario(&StoredScenario{Scripts: []ScenarioScriptRef{{ScriptID: "s", RuntimeSettings: o}}})
	}
	other := storedBrowserReferences()
	other.BrowserConfig.StorageState = "{{secret.OTHER}}"
	if hashOf(stored) == hashOf(other) {
		t.Error("library hash does not see the bound storage_state reference")
	}

	for name, value := range map[string]string{"reserved": reservedBrowserRef, "literal": `{"cookies":[{"name":"sid","value":"v"}]}`, "mixed": "x{{secret.A}}"} {
		sealed := storedBrowserReferences()
		sealed.BrowserConfig.StorageState = value
		sealed.BrowserConfig.Session.Identities[0].Password = value
		sealed.BrowserConfig.Session.Identities[0].StorageState = value
		sealed.BrowserConfig.Session.JWT.PrivateKey = value
		for maskName, mask := range map[string]func(*RuntimeSettingsOverride) *RuntimeSettingsOverride{
			"hash": maskRuntimeSettings, "editor": maskRuntimeSettingsForEditor,
		} {
			b := mask(sealed).BrowserConfig
			identity := b.Session.Identities[0]
			if b.StorageState != scrub.Redacted || identity.Password != scrub.Redacted || identity.StorageState != scrub.Redacted || b.Session.JWT.PrivateKey != scrub.Redacted {
				t.Errorf("%s %s value not masked: %+v %+v %+v", maskName, name, b.StorageState, identity, b.Session.JWT)
			}
		}
	}
}

// The editor projection keeps a protocol credential that only names a project
// secret readable, so a reopened scenario shows its binding; a reserved
// reference and a literal stay masked. The hash form keeps masking them.
func TestProtocolEditorMaskShowsProjectReferences(t *testing.T) {
	input := &RuntimeSettingsOverride{
		Kafka:    &dsl.KafkaConfig{SASLMechanism: "plain", SASLUsername: "{{secret.KAFKA_USER}}", SASLPassword: "{{secret.KAFKA_PW}}", OAuth: &dsl.KafkaOAuthConfig{ClientSecret: "{{secret.OAUTH}}"}, Properties: map[string]string{"ssl.key.password": "{{secret.KEYPW}}"}},
		JMS:      &dsl.JMSConfig{Password: "{{secret.JMS_PW}}"},
		Database: &dsl.DatabaseConfig{DSN: "{{secret.DSN}}"},
		GRPC:     &dsl.GRPCConfig{Metadata: map[string]string{"authorization": "{{secret.TOKEN}}"}},
		Network:  &dsl.NetworkConfig{Username: "{{secret.NET_USER}}", Password: "{{secret.NET_PW}}"},
		Appium:   &dsl.AppiumConfig{Capabilities: map[string]any{"browserstack.key": "{{secret.BS_KEY}}"}},
	}
	editor := maskRuntimeSettingsForEditor(input)
	for path, got := range map[string]string{
		"kafka.sasl_username": editor.Kafka.SASLUsername, "kafka.sasl_password": editor.Kafka.SASLPassword,
		"kafka.oauth.client_secret": editor.Kafka.OAuth.ClientSecret, "kafka.properties": editor.Kafka.Properties["ssl.key.password"],
		"jms.password": editor.JMS.Password, "database.dsn": editor.Database.DSN, "grpc.metadata": editor.GRPC.Metadata["authorization"],
		"network.username": editor.Network.Username, "network.password": editor.Network.Password,
		"appium.capabilities": editor.Appium.Capabilities["browserstack.key"].(string),
	} {
		if !strings.HasPrefix(got, "{{secret.") {
			t.Errorf("editor mask hides project reference at %s: %q", path, got)
		}
	}
	hash := maskRuntimeSettings(input)
	if hash.Kafka.SASLPassword != scrub.Redacted || hash.Database.DSN != scrub.Redacted || hash.JMS.Password != scrub.Redacted {
		t.Errorf("hash form changed: kafka %+v database %+v", hash.Kafka, hash.Database)
	}
	sealed := &RuntimeSettingsOverride{
		Kafka:    &dsl.KafkaConfig{SASLPassword: reservedBrowserRef, Properties: map[string]string{"ssl.key.password": reservedBrowserRef}},
		Database: &dsl.DatabaseConfig{DSN: "postgres://u:literal-pw@db/app"},
	}
	if masked := maskRuntimeSettingsForEditor(sealed); masked.Kafka.SASLPassword != scrub.Redacted || masked.Kafka.Properties["ssl.key.password"] != scrub.Redacted || masked.Database.DSN != scrub.Redacted {
		t.Errorf("editor mask shows sealed or literal values: kafka %+v database %+v", masked.Kafka, masked.Database)
	}
}

// A listed leaf whose parent block exists in neither layer copies that
// absence instead of materializing an empty block (which would switch Kafka
// validation to explicit OAuth and refuse the run).
func TestPartialLeafUnderNilParentKeepsBlockAbsent(t *testing.T) {
	kafkaScript := func() *dsl.Script {
		return &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{
			Mock: true, Topic: "orders", SecurityProtocol: "sasl_ssl", SASLMechanism: "plain",
			SASLUsername: "u", SASLPasswordSecret: "KPW",
		}}}
	}
	for _, path := range []string{"kafka.oauth.scope", "kafka.tls.min_version", "kafka.producer.retries"} {
		script := kafkaScript()
		before := dsl.ValidateScriptIssues(script)
		overrides := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{}, OverrideFields: []string{path}}
		if err := applyRuntimeOverrides(script, overrides); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		k := script.Config.Kafka
		if k.OAuth != nil || k.TLS != nil || k.Producer != nil {
			t.Errorf("%s created a block: oauth=%+v tls=%+v producer=%+v", path, k.OAuth, k.TLS, k.Producer)
		}
		if after := dsl.ValidateScriptIssues(script); !reflect.DeepEqual(before, after) {
			t.Errorf("%s changed validation: before=%v after=%v", path, before, after)
		}
		merged := mergeRuntimeSettingsPartial(nil, overrides)
		if merged.Kafka == nil || merged.Kafka.OAuth != nil || merged.Kafka.TLS != nil || merged.Kafka.Producer != nil {
			t.Errorf("%s merge created a block: %+v", path, merged.Kafka)
		}
		protected, _, err := protectRuntimeOverrideSecrets("scope", 0, kafkaScript(), overrides, false)
		if err != nil {
			t.Fatalf("%s protect: %v", path, err)
		}
		if protected.Kafka != nil && (protected.Kafka.OAuth != nil || protected.Kafka.TLS != nil || protected.Kafka.Producer != nil) {
			t.Errorf("%s protect stored a block: %+v", path, protected.Kafka)
		}
	}
	js := &dsl.Script{Protocol: "jms-solace", Config: dsl.ScriptConfig{JMS: dsl.JMSConfig{Provider: "solace", Mock: true, Destination: "q"}}}
	before := dsl.ValidateScriptIssues(js)
	if err := applyRuntimeOverrides(js, &RuntimeSettingsOverride{JMS: &dsl.JMSConfig{}, OverrideFields: []string{"jms.tls.insecure_skip_verify"}}); err != nil {
		t.Fatal(err)
	}
	if js.Config.JMS.TLS != nil {
		t.Errorf("jms.tls.insecure_skip_verify created a TLS block: %+v", *js.Config.JMS.TLS)
	}
	if after := dsl.ValidateScriptIssues(js); !reflect.DeepEqual(before, after) {
		t.Errorf("jms validation changed: before=%v after=%v", before, after)
	}

	// Where the script's block exists, the listed leaf still takes the
	// override's (zero) value.
	withTLS := kafkaScript()
	withTLS.Config.Kafka.TLS = &dsl.KafkaTLSConfig{MinVersion: "1.3", InsecureSkipVerify: true}
	if err := applyRuntimeOverrides(withTLS, &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{}, OverrideFields: []string{"kafka.tls.insecure_skip_verify"}}); err != nil {
		t.Fatal(err)
	}
	if tls := withTLS.Config.Kafka.TLS; tls == nil || tls.InsecureSkipVerify || tls.MinVersion != "1.3" {
		t.Errorf("existing block = %+v, want insecure cleared and min_version kept", tls)
	}
	// And a listed leaf the override sets still allocates the block.
	set := kafkaScript()
	if err := applyRuntimeOverrides(set, &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{TLS: &dsl.KafkaTLSConfig{MinVersion: "1.3"}}, OverrideFields: []string{"kafka.tls.min_version"}}); err != nil {
		t.Fatal(err)
	}
	if tls := set.Config.Kafka.TLS; tls == nil || tls.MinVersion != "1.3" {
		t.Errorf("set leaf = %+v", tls)
	}
}

// Device-cloud access keys in credential-named Appium capabilities
// (BrowserStack's legacy browserstack.key, Sauce accessKey, TestObject API
// key) are sealed for execution, masked on read and refused in plans.
func TestAppiumDeviceCloudKeyCapabilitiesAreCredentials(t *testing.T) {
	const key = "bs-access-key-9481"
	for _, capability := range []string{"browserstack.key", "testobject_api_key", "lt:options.token", "bstack:secret"} {
		input := &RuntimeSettingsOverride{Appium: &dsl.AppiumConfig{Capabilities: map[string]any{
			"browserstack.user": "alice", capability: key, "sauce:options": map[string]any{"accessKey": key, "build": "n1"},
		}}}
		protected, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, input, false)
		if err != nil {
			t.Fatalf("%s protect: %v", capability, err)
		}
		payload, _ := json.Marshal(protected)
		if strings.Contains(string(payload), key) {
			t.Errorf("%s: protected override holds the key: %s", capability, payload)
		}
		recorded := 0
		for _, value := range values {
			if value == key {
				recorded++
			}
		}
		if recorded != 2 {
			t.Errorf("%s: recorded %d runtime secret values for the key, want 2 (%v)", capability, recorded, values)
		}
		if protected.Appium.Capabilities["browserstack.user"] != "alice" {
			t.Errorf("%s: non-credential capability protected: %v", capability, protected.Appium.Capabilities)
		}
		for name, mask := range map[string]func(*RuntimeSettingsOverride) *RuntimeSettingsOverride{
			"hash": maskRuntimeSettings, "editor": maskRuntimeSettingsForEditor,
		} {
			masked, _ := json.Marshal(mask(input))
			if strings.Contains(string(masked), key) {
				t.Errorf("%s %s mask leaks the key: %s", capability, name, masked)
			}
		}
		paths := executionPlanTypedCredentialPaths(input)
		found := false
		for _, path := range paths {
			if strings.Contains(path, capability) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: plan check misses the key (got %v)", capability, paths)
		}
	}
	// Non-credential capabilities that merely end in similar words stay plain.
	plain := &RuntimeSettingsOverride{Appium: &dsl.AppiumConfig{Capabilities: map[string]any{"appium:autoAcceptAlerts": "true", "keyboard": "hw", "appium:newCommandTimeout": "60"}}}
	if _, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, plain, false); err != nil || len(values) != 0 {
		t.Errorf("plain capabilities protected: %v %v", values, err)
	}
}

// Flat and nested capability spellings that join to the same dotted text
// are distinct leaves, so each keeps its own runtime secret.
func TestAppiumCapabilityLeafPathsAreUnambiguous(t *testing.T) {
	input := &RuntimeSettingsOverride{Appium: &dsl.AppiumConfig{Capabilities: map[string]any{
		"lt:options.accessKey": "FLAT_SECRET_1",
		"lt:options":           map[string]any{"accessKey": "NESTED_SECRET_2"},
	}}}
	paths := map[string]bool{}
	for _, leaf := range runtimeProtocolCredentialLeaves(input) {
		if paths[leaf.path] {
			t.Errorf("duplicate leaf path %s", leaf.path)
		}
		paths[leaf.path] = true
	}
	protected, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, input, false)
	if err != nil {
		t.Fatal(err)
	}
	flat := protected.Appium.Capabilities["lt:options.accessKey"].(string)
	nested := protected.Appium.Capabilities["lt:options"].(map[string]any)["accessKey"].(string)
	if flat == nested {
		t.Fatalf("both capabilities share reference %s", flat)
	}
	resolved := map[string]string{}
	for name, value := range values {
		resolved["{{secret."+name+"}}"] = value
	}
	if resolved[flat] != "FLAT_SECRET_1" || resolved[nested] != "NESTED_SECRET_2" {
		t.Errorf("flat %s -> %q, nested %s -> %q (values %v)", flat, resolved[flat], nested, resolved[nested], values)
	}
	// Reconcile restores each sentinel from its own prior leaf.
	reconciled := reconcileGroupRuntimeSecrets(maskRuntimeSettings(protected), protected)
	if reconciled.Appium.Capabilities["lt:options.accessKey"] != flat || reconciled.Appium.Capabilities["lt:options"].(map[string]any)["accessKey"] != nested {
		t.Errorf("reconcile crossed leaves: %v", reconciled.Appium.Capabilities)
	}
	if !slices.Contains(executionPlanTypedCredentialPaths(input), `appium.capabilities["lt:options.accessKey"]`) {
		t.Errorf("plan path for the flat key = %v", executionPlanTypedCredentialPaths(input))
	}
}
