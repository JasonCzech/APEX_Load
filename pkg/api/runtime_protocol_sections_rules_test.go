package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A plan entry row written before the native protocol sections existed was
// validated and executed with those keys ignored. It keeps doing so: the
// sections (and the java_vuser fields added with them) are dropped at
// materialization, and override_fields that name them fail the entry as
// they did then.
func TestLegacyPlanRowIgnoresNativeProtocolSections(t *testing.T) {
	raw := json.RawMessage(`{"database":{"driver":"postgres","dsn":"postgres://app:legacy-pw-91@db.example/app"},` +
		`"kafka":{"topic":"t","sasl_password":"legacy-pw-91"},"log_level":"debug",` +
		`"java_vuser":{"jvm_sharing":"thread","startup_timeout_ms":9}}`)
	req, err := buildPlanEntryCreateRequest("plan", "proj", nil, storage.ExecutionPlanEntry{ScenarioID: "scn-1", RuntimeOverrides: raw})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	got := req.RuntimeSettings
	if got == nil || got.LogLevel != "debug" {
		t.Fatalf("legacy row lost its v1 fields: %+v", got)
	}
	if got.Database != nil || got.Kafka != nil || len(presentRuntimeProtocolSections(got)) != 0 {
		t.Fatalf("legacy row now overrides native protocol sections: %+v", got)
	}
	if got.JavaVuser == nil || got.JavaVuser.JVMSharing != "thread" || got.JavaVuser.StartupTimeoutMs != 0 {
		t.Fatalf("legacy java_vuser = %+v, want jvm_sharing kept and startup_timeout_ms ignored", got.JavaVuser)
	}

	partial := json.RawMessage(`{"override_fields":["kafka.topic"],"kafka":{"topic":"t"}}`)
	if _, err := buildPlanEntryCreateRequest("plan", "proj", nil, storage.ExecutionPlanEntry{ScenarioID: "scn-1", RuntimeOverrides: partial}); err == nil || !strings.Contains(err.Error(), "kafka.topic") {
		t.Fatalf("legacy override_fields naming a native section = %v, want a refusal naming kafka.topic", err)
	}

	// A run snapshot carries the row's schema, so resume treats it the same.
	snapshot := executionPlanRunEntrySnapshot(storage.ExecutionPlanEntry{ScenarioID: "scn-1", RuntimeOverrides: raw}, "run", "", "", nil, nil)
	resumed, err := buildPlanEntryCreateRequest("run", "proj", nil, executionPlanEntryFromRunEntry(snapshot))
	if err != nil || resumed.RuntimeSettings.Database != nil {
		t.Fatalf("resumed legacy snapshot = %+v, %v", resumed.RuntimeSettings, err)
	}
}

// A plan write records the current runtime-overrides schema, so its native
// sections apply; and materialization re-runs the reference-only rule on
// them, so a row that holds a typed credential never executes it.
func TestPlanRowNativeProtocolSectionsApplyAndStayReferenceOnly(t *testing.T) {
	server := &Server{}
	plan := &storage.ExecutionPlan{Name: "plan", Entries: []storage.ExecutionPlanEntry{
		{ScenarioID: "scn-1", RuntimeOverrides: json.RawMessage(`{"database":{"driver":"postgres","dsn":"{{secret.ORDERS_DSN}}"}}`)},
		{ScenarioID: "scn-2"},
	}}
	if status, message := server.validateExecutionPlan(plan); status != 0 {
		t.Fatalf("validate: %d %s", status, message)
	}
	if plan.Entries[0].RuntimeOverridesSchema != executionPlanRuntimeOverridesSchema {
		t.Fatalf("plan write did not record the schema: %d", plan.Entries[0].RuntimeOverridesSchema)
	}
	if plan.Entries[1].RuntimeOverridesSchema != 0 {
		t.Fatalf("entry without overrides got a schema: %d", plan.Entries[1].RuntimeOverridesSchema)
	}
	req, err := buildPlanEntryCreateRequest("plan", "proj", nil, plan.Entries[0])
	if err != nil || req.RuntimeSettings.Database == nil || req.RuntimeSettings.Database.DSN != "{{secret.ORDERS_DSN}}" {
		t.Fatalf("current-schema row = %+v, %v", req.RuntimeSettings, err)
	}
	snapshot := executionPlanRunEntrySnapshot(plan.Entries[0], "run", "", "", nil, nil)
	if snapshot.RuntimeOverridesSchema != executionPlanRuntimeOverridesSchema || executionPlanEntryFromRunEntry(snapshot).RuntimeOverridesSchema != executionPlanRuntimeOverridesSchema {
		t.Fatalf("run snapshot dropped the schema: %+v", snapshot)
	}

	// A row that reached storage with a literal (written directly, or by an
	// older writer) is refused per entry, naming each path.
	literal := storage.ExecutionPlanEntry{ScenarioID: "scn-1", RuntimeOverridesSchema: executionPlanRuntimeOverridesSchema,
		RuntimeOverrides: json.RawMessage(`{"database":{"dsn":"postgres://app:pw-77@db/app"},"kafka":{"sasl_password":"pw-77"}}`)}
	_, err = buildPlanEntryCreateRequest("plan", "proj", nil, literal)
	if err == nil || !strings.Contains(err.Error(), "database.dsn") || !strings.Contains(err.Error(), "kafka.sasl_password") || strings.Contains(err.Error(), "pw-77") {
		t.Fatalf("literal native credential at materialization = %v", err)
	}
}

// Apply-to-groups copies a protocol map the same way whether or not the
// source holds a credential key in it: keys only the target has are cleared.
func TestApplyToGroupsProtocolMapCopyClearsTargetOnlyKeys(t *testing.T) {
	run := func(props map[string]string) map[string]string {
		targetScript := dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Properties: map[string]string{"linger.ms": "5", "ssl.key.password": "{{secret.OWN_KEY}}"}}}
		source := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Topic: "t", Properties: props}}
		next := replaceRuntimeFields(nil, source, []string{"kafka"})
		next = keepTargetProtocolCredentials(next, nil, "kafka", runtimeSettingsFromScriptConfig(targetScript))
		script := &dsl.Script{Config: targetScript}
		if err := applyRuntimeOverrides(script, next); err != nil {
			t.Fatalf("apply: %v", err)
		}
		return script.Config.Kafka.Properties
	}
	plain := run(map[string]string{"acks": "all"})
	withCred := run(map[string]string{"acks": "all", "ssl.key.password": "src-key"})
	if _, kept := plain["linger.ms"]; kept {
		t.Fatalf("whole-section copy kept a target-only key: %v", plain)
	}
	if _, kept := withCred["linger.ms"]; kept || withCred["acks"] != "all" {
		t.Fatalf("copy with a credential key overlays instead of copying: %v", withCred)
	}
	if withCred["ssl.key.password"] != "{{secret.OWN_KEY}}" {
		t.Fatalf("target credential not inherited: %v", withCred)
	}
}

// A credential inside a broker list cannot be copied without copying the
// credential, so the copy is refused instead of silently keeping the
// target's brokers.
func TestApplyToGroupsRefusesCredentialBrokerList(t *testing.T) {
	source := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Brokers: []string{"src-a:9092", "u:p-77@src-b:9092"}}}
	next := replaceRuntimeFields(nil, source, []string{"kafka"})
	if got := runtimeProtocolUncopyableCredentialPaths(next, "kafka"); !slices.Equal(got, []string{"kafka.brokers"}) {
		t.Fatalf("uncopyable paths = %v, want [kafka.brokers]", got)
	}
	clean := replaceRuntimeFields(nil, &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Brokers: []string{"src-a:9092", "{{secret.U}}:{{secret.P}}@src-b:9092"}}}, []string{"kafka"})
	if got := runtimeProtocolUncopyableCredentialPaths(clean, "kafka"); len(got) != 0 {
		t.Fatalf("reference-only brokers flagged: %v", got)
	}

	server, _ := newRuntimeOverrideTestServer(t)
	sourceScript := kafkaScriptForProtocolApply("kafka-brokers-source")
	sourceScript.Config.Kafka.Brokers = []string{"src-a:9092", "u:p-77@src-b:9092"}
	targetScript := kafkaScriptForProtocolApply("kafka-brokers-target")
	targetScript.Config.Kafka.Brokers = []string{"target:9092"}
	server.runner.StoreScript(sourceScript)
	server.runner.StoreScript(targetScript)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "brokers", Duration: "1m", Groups: []CreateGroupEntry{
		{Name: "Source", ScriptID: sourceScript.ID, VUsers: 1}, {Name: "Target", ScriptID: targetScript.ID, VUsers: 1},
	}})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	_, _, err = server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"kafka"}})
	if err == nil || !strings.Contains(err.Error(), "kafka.brokers") || strings.Contains(err.Error(), "p-77") {
		t.Fatalf("apply with credential brokers = %v, want a refusal naming kafka.brokers", err)
	}
	if server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings != nil {
		t.Fatalf("refused copy changed the target: %+v", server.runner.scenarios[scenario.ID].Scripts[1].RuntimeSettings)
	}
}

// An override's secret-name field always resolves as a secret reference: a
// bare name that names no project secret never becomes the credential.
func TestOverrideSecretNameFieldsResolveOnlyAsReferences(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{
		Database: dsl.DatabaseConfig{Driver: "postgres", DSNSecret: "script_dsn"},
		Kafka:    dsl.KafkaConfig{SASLPasswordSecret: "script_kafka"},
	}}
	overrides := &RuntimeSettingsOverride{
		Database:  &dsl.DatabaseConfig{Driver: "postgres", DSNSecret: "Winter2026Pass"},
		EventHubs: &dsl.EventHubsConfig{ConnectionStringSecret: "{{secret.EH}}"},
	}
	if err := applyRuntimeOverrides(script, overrides); err != nil {
		t.Fatal(err)
	}
	if got := script.Config.Database.DSNSecret; got != "{{secret.Winter2026Pass}}" {
		t.Fatalf("override dsn_secret = %q, want a reference", got)
	}
	if got := script.Config.EventHubs.ConnectionStringSecret; got != "{{secret.EH}}" {
		t.Fatalf("override reference rewritten: %q", got)
	}
	if got := script.Config.Kafka.SASLPasswordSecret; got != "script_kafka" {
		t.Fatalf("script-sourced name changed: %q", got)
	}
	if overrides.Database.DSNSecret != "Winter2026Pass" {
		t.Fatal("apply mutated the override")
	}

	// override_fields: only the listed leaves are the override's.
	partialScript := &dsl.Script{Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{SASLPasswordSecret: "script_kafka", OAuth: &dsl.KafkaOAuthConfig{ClientSecretRef: "script_oauth"}}}}
	partial := &RuntimeSettingsOverride{OverrideFields: []string{"kafka.oauth.client_secret_ref"}, Kafka: &dsl.KafkaConfig{OAuth: &dsl.KafkaOAuthConfig{ClientSecretRef: "OAUTH_NAME"}}}
	if err := applyRuntimeOverrides(partialScript, partial); err != nil {
		t.Fatal(err)
	}
	if k := partialScript.Config.Kafka; k.OAuth.ClientSecretRef != "{{secret.OAUTH_NAME}}" || k.SASLPasswordSecret != "script_kafka" {
		t.Fatalf("partial kafka = %+v / %+v", k, k.OAuth)
	}
	if partial.Kafka.OAuth.ClientSecretRef != "OAUTH_NAME" {
		t.Fatal("partial apply mutated the override")
	}
}

// A literal in a secret-name field of the source script is a credential: the
// copy leaves it out and the target row still protects.
func TestApplyToGroupsKeepsLiteralSecretNameFieldOut(t *testing.T) {
	cfg := dsl.ScriptConfig{
		Kafka: dsl.KafkaConfig{Topic: "t", SASLMechanism: "oauthbearer",
			OAuth: &dsl.KafkaOAuthConfig{Mode: "client_credentials", TokenURL: "https://idp.example/token", ClientID: "cid", ClientSecretRef: "s3cr+t/Hunter2pw=="}},
		Database: dsl.DatabaseConfig{Driver: "postgres", DSNSecret: "postgres://u:Hunter2pw@db/app"},
	}
	effective := runtimeSettingsFromScriptConfig(cfg)
	for _, section := range []string{"kafka", "database"} {
		next := replaceRuntimeFields(nil, effective, []string{section})
		next = keepTargetProtocolCredentials(next, nil, section)
		payload, _ := json.Marshal(next)
		if strings.Contains(string(payload), "Hunter2pw") {
			t.Fatalf("%s copy carries the source literal: %s", section, payload)
		}
		if _, _, err := protectRuntimeOverrideSecrets("scn", 1, nil, next, false); err != nil {
			t.Fatalf("%s target row does not protect: %v", section, err)
		}
	}
}

// A credential URL whose userinfo is made of project secret references is
// the documented device-cloud form: it saves unchanged, is masked on read and
// is restored from an echoed mask. Mixed or reserved material is refused.
func TestRuntimeProtocolURLReferenceUserinfo(t *testing.T) {
	const appiumURL = "https://{{secret.BS_USER}}:{{secret.BS_KEY}}@hub.browserstack.com/wd/hub"
	override := &RuntimeSettingsOverride{
		Appium:    &dsl.AppiumConfig{ServerURL: appiumURL},
		Kafka:     &dsl.KafkaConfig{SchemaRegistryURL: "https://{{secret.SR_USER}}:{{secret.SR_PW}}@sr.example.test"},
		JMS:       &dsl.JMSConfig{BrokerURL: "amqps://{{secret.JMS_USER}}:{{secret.JMS_PW}}@broker:5671"},
		EventHubs: &dsl.EventHubsConfig{CheckpointContainerURL: "https://acct.blob.core.windows.net/c?sv=1&sig={{secret.SAS}}"},
	}
	protected, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, override, false)
	if err != nil || len(values) != 0 || protected.Appium.ServerURL != appiumURL || protected.JMS.BrokerURL != override.JMS.BrokerURL ||
		protected.Kafka.SchemaRegistryURL != override.Kafka.SchemaRegistryURL || protected.EventHubs.CheckpointContainerURL != override.EventHubs.CheckpointContainerURL {
		t.Fatalf("reference URLs = %+v values=%v err=%v", protected, values, err)
	}
	if got := executionPlanTypedCredentialPaths(override); len(got) != 0 {
		t.Fatalf("plan rule flags reference URLs: %v", got)
	}
	masked := maskRuntimeSettingsForEditor(override)
	if masked.Appium.ServerURL == appiumURL {
		t.Fatalf("reference userinfo shown on read: %q", masked.Appium.ServerURL)
	}
	if restored := reconcileGroupRuntimeSecrets(masked, override); restored.Appium.ServerURL != appiumURL || restored.JMS.BrokerURL != override.JMS.BrokerURL {
		t.Fatalf("masked echo restored to %q / %q", restored.Appium.ServerURL, restored.JMS.BrokerURL)
	}
	for name, bad := range map[string]string{
		"literal user beside reference": "https://bob:{{secret.BS_KEY}}@hub.example.test/wd/hub",
		"reserved reference":            "https://{{secret.BS_USER}}:{{secret." + runtimeOverrideSecretPrefix + "G1_A}}@hub.example.test/wd/hub",
	} {
		if _, _, err := protectRuntimeOverrideSecrets("scope", 0, nil, &RuntimeSettingsOverride{Appium: &dsl.AppiumConfig{ServerURL: bad}}, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// kafka.oauth.token_url and network.address carry credentials in their
// query or userinfo like any other credential URL.
func TestRuntimeProtocolTokenURLAndNetworkAddressAreCredentialURLs(t *testing.T) {
	const secret = "Hunter2pw"
	override := &RuntimeSettingsOverride{
		Kafka:   &dsl.KafkaConfig{OAuth: &dsl.KafkaOAuthConfig{TokenURL: "https://idp.example/token?client_secret=" + secret, ClientID: "c"}},
		Network: &dsl.NetworkConfig{Address: "smtp://bob:" + secret + "@mail:25"},
	}
	masked, _ := json.Marshal(maskRuntimeSettings(override))
	if strings.Contains(string(masked), secret) {
		t.Fatalf("mask leaks: %s", masked)
	}
	protected, values, err := protectRuntimeOverrideSecrets("scope", 0, nil, override, false)
	if err != nil || len(values) != 2 {
		t.Fatalf("protect: values=%v err=%v", values, err)
	}
	if payload, _ := json.Marshal(protected); strings.Contains(string(payload), secret) {
		t.Fatalf("protected override holds the credential: %s", payload)
	}
	paths := executionPlanTypedCredentialPaths(override)
	for _, want := range []string{"kafka.oauth.token_url", "network.address"} {
		if !slices.Contains(paths, want) {
			t.Errorf("plan rule misses %s: %v", want, paths)
		}
	}
	plain := &RuntimeSettingsOverride{
		Kafka:   &dsl.KafkaConfig{OAuth: &dsl.KafkaOAuthConfig{TokenURL: "https://idp.example/token", ClientID: "c"}},
		Network: &dsl.NetworkConfig{Address: "mail.example.test:25"},
	}
	if got := maskRuntimeSettings(plain); got.Kafka.OAuth.TokenURL != plain.Kafka.OAuth.TokenURL || got.Network.Address != plain.Network.Address {
		t.Fatalf("plain URLs masked: %+v %+v", got.Kafka.OAuth, got.Network)
	}
	if got := executionPlanTypedCredentialPaths(plain); len(got) != 0 {
		t.Fatalf("plain URLs flagged: %v", got)
	}
	if restored := reconcileGroupRuntimeSecrets(maskRuntimeSettings(override), override); restored.Network.Address != override.Network.Address {
		t.Fatalf("network.address echo restored to %q", restored.Network.Address)
	}
}
