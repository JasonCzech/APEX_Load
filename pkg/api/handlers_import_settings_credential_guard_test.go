package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// The catalog's scenario scope must advertise exactly the paths a scenario
// group's runtime_settings override_fields accepts.
func TestConfigCatalogScenarioScopeMatchesRuntimeOverridePaths(t *testing.T) {
	for _, entry := range dsl.ConfigCatalog() {
		_, err := normalizeOneRuntimeOverrideField(entry.Path)
		scenario := slices.Contains(entry.Scopes, dsl.ConfigScopeScenario)
		if scenario && err != nil {
			t.Errorf("%s: scenario-scoped but override_fields rejects it: %v", entry.Path, err)
		}
		if !scenario && err == nil && len(entry.Scopes) > 0 {
			t.Errorf("%s: override_fields accepts it but the catalog omits the scenario scope", entry.Path)
		}
	}
}

func TestImportSettingsKeepsTLSContractNarrowing(t *testing.T) {
	for _, category := range []string{"HTTP", "all", ""} {
		for _, path := range []string{"config.http.tls.min_version", "config.http.tls.max_version"} {
			if draft, errs := applyImportSettings(settingsTestScript(), category, []ImportSettingsChange{settingChange(path, `"1.0"`)}); len(errs) == 0 || draft != nil {
				t.Errorf("category %q: %s=1.0 accepted", category, path)
			}
		}
	}
	if _, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange("config.http.tls.min_version", `"1.3"`)}); len(errs) != 0 {
		t.Fatalf("TLS 1.3 rejected: %v", errs)
	}
	for _, f := range widenedImportSettingFields("") {
		if f.Path == "config.http.tls.min_version" && !slices.Equal(f.Options, []string{"1.2", "1.3"}) {
			t.Fatalf("listed TLS options %v, want [1.2 1.3]", f.Options)
		}
	}
	// The import contract may narrow catalog choices but never invent one.
	for _, f := range importSettingFields {
		meta, ok := dsl.ConfigFieldForPath(f.Path)
		if !ok || len(f.Options) == 0 || len(meta.Options) == 0 {
			continue
		}
		for _, option := range f.Options {
			if !slices.Contains(meta.Options, option) {
				t.Errorf("%s: import option %q is not a catalog option %v", f.Path, option, meta.Options)
			}
		}
	}
}

func TestImportSettingsRejectsCredentialsInWidenedPaths(t *testing.T) {
	for _, tc := range []struct {
		name, path, value string
	}{
		{"kafka jaas password", "config.kafka.properties", `{"sasl.jaas.config":"org.apache.kafka.common.security.plain.PlainLoginModule required username=\"u\" password=\"Hunter2Plain\";"}`},
		{"appium userinfo", "config.appium.server_url", `"https://user:Hunter2Plain@hub.test/wd/hub"`},
		{"event hubs SAS signature", "config.eventhubs.checkpoint_container_url", `"https://acct.blob.core.windows.net/c?sv=2020-08-04&sig=AbCdEfGh1234"`},
		{"database DSN password", "config.database.dsn_secret", `"postgres://user:Hunter2Plain@db.test/app"`},
		{"mysql DSN userinfo", "config.database.dsn_secret", `"app:Hunter2Plain@tcp(db.test:3306)/orders"`},
		{"connection string key", "config.kafka.properties", `{"conn":"Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=AbCdEfGh1234"}`},
		// Contract (import_settings_fields.json) paths get the same guard.
		{"contract PAC token", "config.http.proxy.pac_url", `"https://pac.test/p.pac?token=Hunter2Plain"`},
		{"contract PAC userinfo", "config.http.proxy.pac_url", `"https://u:Hunter2Plain@pac.test/p.pac"`},
		{"contract broker userinfo", "config.kafka.brokers", `["u:Hunter2Plain@k.test:9092"]`},
		{"contract broker URL userinfo", "config.kafka.brokers", `["sasl://u:Hunter2Plain@k.test:9092"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)})
			if draft != nil || errs[tc.path] != "Use Credentials for sensitive values" {
				t.Fatalf("plaintext credential accepted: draft=%v errs=%v", draft != nil, errs)
			}
		})
	}
	for _, tc := range []struct {
		name, path, value string
	}{
		{"jaas secret reference", "config.kafka.properties", `{"sasl.jaas.config":"org.apache.kafka.common.security.plain.PlainLoginModule required username=\"u\" password=\"{{secret.KAFKA_PASSWORD}}\";"}`},
		{"ordinary kafka properties", "config.kafka.properties", `{"request.timeout.ms":"45000","sasl.mechanism":"PLAIN"}`},
		{"plain appium URL", "config.appium.server_url", `"http://127.0.0.1:4723/wd/hub"`},
		{"secret name", "config.database.dsn_secret", `"ORDERS_DSN"`},
		{"SAS policy name only", "config.kafka.properties", `{"conn":"SharedAccessKeyName=send"}`},
		{"SAS signature reference", "config.eventhubs.checkpoint_container_url", `"https://acct.blob.core.windows.net/c?sv=2020-08-04&sig={{secret.SAS_SIG}}"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)}); len(errs) != 0 {
				t.Fatalf("ordinary value rejected: %v", errs)
			}
		})
	}
	// Contract paths: unchanged imported credential-shaped values pass, and an
	// added plain element beside them is accepted.
	contractImported := settingsTestScript()
	contractImported.Config.HTTP.Proxy.PACURL = "https://pac.test/p.pac?token=recorded"
	contractImported.Config.Kafka.Brokers = []string{"u:recorded@k.test:9092"}
	for _, change := range []ImportSettingsChange{
		settingChange("config.http.proxy.pac_url", `"https://pac.test/p.pac?token=recorded"`),
		settingChange("config.kafka.brokers", `["u:recorded@k.test:9092","k2.test:9092"]`),
	} {
		if _, errs := applyImportSettings(contractImported, "all", []ImportSettingsChange{change}); len(errs) != 0 {
			t.Errorf("%s: unchanged imported contract value rejected: %v", change.Path, errs)
		}
	}
	// A secret-typed catalog field accepts its unchanged imported plaintext
	// but not a newly typed one.
	secretImported := settingsTestScript()
	secretImported.Config.Kafka.SASLPassword = "imported-sasl-literal"
	if _, errs := applyImportSettings(secretImported, "all", []ImportSettingsChange{settingChange("config.kafka.sasl_password", `"imported-sasl-literal"`)}); len(errs) != 0 {
		t.Errorf("unchanged imported secret-typed value rejected: %v", errs)
	}
	if draft, errs := applyImportSettings(secretImported, "all", []ImportSettingsChange{settingChange("config.kafka.sasl_password", `"Hunter2Plain"`)}); draft != nil || len(errs) == 0 {
		t.Errorf("newly typed secret-typed plaintext accepted")
	}
	// Re-sending an imported (script-sourced) value unchanged is not a new secret.
	imported := settingsTestScript()
	imported.Config.Appium.ServerURL = "https://user:recorded@hub.test/wd/hub"
	if _, errs := applyImportSettings(imported, "all", []ImportSettingsChange{settingChange("config.appium.server_url", `"https://user:recorded@hub.test/wd/hub"`)}); errs["config.appium.server_url"] == "Use Credentials for sensitive values" {
		t.Fatalf("unchanged imported value rejected: %v", errs)
	}
}

func TestImportSettingsRejectsSourceOwnedJMeterFields(t *testing.T) {
	script := settingsTestScript()
	script.Config.JMeter = &dsl.JMeterCompatibility{Profile: "messaging", SourcePlanID: "plan-1", SourceGroupID: "group-1"}
	for _, change := range []ImportSettingsChange{
		settingChange("config.jmeter.source_group_id", `"group-2"`),
		settingChange("config.jmeter.source_plan_id", `"plan-2"`),
		settingChange("config.jmeter.profile", `"something"`),
		settingChange("config.jmeter.context_confirmed", `true`),
		settingChange("config.sampler_schedules", `[]`),
	} {
		if draft, errs := applyImportSettings(script, "all", []ImportSettingsChange{change}); draft != nil || errs[change.Path] != "This field is not editable in this category" {
			t.Errorf("%s: source-owned edit accepted: errs=%v", change.Path, errs)
		}
	}
}

func TestImportSettingsFieldsAcceptVersionedHTTPProtocols(t *testing.T) {
	server := NewServer(ServerConfig{})
	count := func(protocol string) int {
		w := httptest.NewRecorder()
		server.mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/scripts/import/settings/fields?protocol="+protocol, nil))
		if w.Code != 200 {
			t.Fatalf("protocol=%s status=%d", protocol, w.Code)
		}
		var resp importSettingsFieldsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return len(resp.Fields)
	}
	want := count("http")
	if want == 0 {
		t.Fatal("no http import fields")
	}
	for _, alias := range []string{"http/1.1", "http/2", "http/3"} {
		if got := count(alias); got != want {
			t.Errorf("protocol=%s: %d fields, want %d", alias, got, want)
		}
	}
}

// The credential guard is per leaf: editing a sibling of an imported
// credential-bearing entry must not re-inspect the untouched imported value,
// while changing that entry itself still must.
func TestImportSettingsCredentialGuardIsPerLeaf(t *testing.T) {
	const jaas = `org.apache.kafka.common.security.plain.PlainLoginModule required username="u" password="pw123";`
	imported := func() *dsl.Script {
		script := settingsTestScript()
		script.Config.Kafka.Properties = map[string]string{"sasl.jaas.config": jaas, "acks": "1"}
		return script
	}
	encode := func(props map[string]string) string {
		raw, err := json.Marshal(props)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if _, errs := applyImportSettings(imported(), "all", []ImportSettingsChange{settingChange("config.kafka.properties", encode(map[string]string{"sasl.jaas.config": jaas, "acks": "all"}))}); len(errs) != 0 {
		t.Fatalf("sibling edit of imported JAAS entry rejected: %v", errs)
	}
	changed := `org.apache.kafka.common.security.plain.PlainLoginModule required username="u" password="Hunter2Plain";`
	if draft, errs := applyImportSettings(imported(), "all", []ImportSettingsChange{settingChange("config.kafka.properties", encode(map[string]string{"sasl.jaas.config": changed, "acks": "1"}))}); draft != nil || errs["config.kafka.properties"] != "Use Credentials for sensitive values" {
		t.Fatalf("changed JAAS password accepted: draft=%v errs=%v", draft != nil, errs)
	}
	// An imported userinfo element survives while a newly appended plain
	// element is accepted, and a new userinfo element is not.
	if !importSettingValueCarriesCredential([]any{"https://u:recorded@a.test", "https://u:Hunter2@b.test"}, []any{"https://u:recorded@a.test"}) {
		t.Fatal("new userinfo list element not detected")
	}
	if importSettingValueCarriesCredential([]any{"https://u:recorded@a.test", "https://b.test"}, []any{"https://u:recorded@a.test"}) {
		t.Fatal("unchanged imported list element re-inspected")
	}
}

// The guard must not depend on url.Parse succeeding or on a scheme being
// present, and must exempt only exact portable references, not any {{...}}.
func TestImportSettingsCredentialGuardWithoutURLParser(t *testing.T) {
	for _, tc := range []struct {
		name, path, value string
	}{
		{"malformed port userinfo", "config.appium.server_url", `"https://user:Hunter2Plain@hub.test:badport/wd/hub"`},
		{"invalid escape userinfo", "config.appium.server_url", `"https://user:Hunt%zz2Plain@hub.test/wd/hub"`},
		{"space in URL", "config.appium.server_url", `"https://user:Hunter2Plain@hub test/wd/hub"`},
		{"user-only token after scheme", "config.appium.server_url", `"https://ghp_Hunter2Plain@hub.test/wd/hub"`},
		{"scheme-less userinfo", "config.appium.server_url", `"user:Hunter2Plain@hub.test:4444/wd/hub"`},
		{"scheme-less SAS signature", "config.eventhubs.checkpoint_container_url", `"acct.blob.core.windows.net/c?sv=2020-08-04&sig=AbCdEfGh1234"`},
		{"malformed SAS signature", "config.eventhubs.checkpoint_container_url", `"https://acct.blob.core.windows.net:bad/c?sv=2020-08-04&sig=AbCd%zzEfGh1234"`},
		{"braced literal SAS signature", "config.eventhubs.checkpoint_container_url", `"https://acct.blob.core.windows.net/c?sv=2020-08-04&sig={{AbCdEfGh1234}}"`},
		{"braced literal userinfo", "config.appium.server_url", `"https://user:{{Hunter2Plain}}@hub.test/wd/hub"`},
		{"braced literal jaas password", "config.kafka.properties", `{"sasl.jaas.config":"org.apache.kafka.common.security.plain.PlainLoginModule required username=\"u\" password=\"{{Hunter2Plain}}\";"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)})
			if draft != nil || errs[tc.path] != "Use Credentials for sensitive values" {
				t.Fatalf("plaintext credential accepted: draft=%v errs=%v", draft != nil, errs)
			}
		})
	}
	for _, tc := range []struct {
		name, path, value string
	}{
		{"userinfo references", "config.appium.server_url", `"https://{{secret.HUB_USER}}:{{secret.HUB_PASSWORD}}@hub.test/wd/hub"`},
		{"literal user with password reference", "config.appium.server_url", `"https://user:{{secret.HUB_PASSWORD}}@hub.test/wd/hub"`},
		{"at sign in path", "config.appium.server_url", `"https://hub.test:4444/@team/wd/hub"`},
		{"encoded SAS signature reference", "config.eventhubs.checkpoint_container_url", `"https://acct.blob.core.windows.net/c?sv=2020-08-04&sig=%7B%7Bsecret.SAS_SIG%7D%7D"`},
		{"scheme-less SAS reference", "config.eventhubs.checkpoint_container_url", `"acct.blob.core.windows.net/c?sv=2020-08-04&sig={{secret.SAS_SIG}}"`},
		{"email in property", "config.kafka.properties", `{"client.id":"ops@example.com"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)}); len(errs) != 0 {
				t.Fatalf("ordinary value rejected: %v", errs)
			}
		})
	}
}

// List elements compare against every imported element, not by position.
func TestImportSettingsCredentialGuardListIsOrderIndependent(t *testing.T) {
	imported := []any{"https://u:recorded@a.test", "https://u:recorded@b.test"}
	for name, value := range map[string][]any{
		"prepend": {"https://c.test", "https://u:recorded@a.test", "https://u:recorded@b.test"},
		"insert":  {"https://u:recorded@a.test", "https://c.test", "https://u:recorded@b.test"},
		"remove":  {"https://u:recorded@b.test"},
		"reorder": {"https://u:recorded@b.test", "https://u:recorded@a.test"},
	} {
		if importSettingValueCarriesCredential(value, imported) {
			t.Errorf("%s: unchanged imported elements re-inspected", name)
		}
	}
	if !importSettingValueCarriesCredential([]any{"https://u:Hunter2@c.test", "https://u:recorded@a.test"}, imported) {
		t.Fatal("prepended userinfo element not detected")
	}
	priorMaps := []any{map[string]any{"url": "https://u:recorded@a.test", "name": "a"}}
	if importSettingValueCarriesCredential([]any{map[string]any{"url": "https://plain.test"}, map[string]any{"url": "https://u:recorded@a.test", "name": "renamed"}}, priorMaps) {
		t.Fatal("edited sibling of imported map element re-inspected its credential")
	}
	if !importSettingValueCarriesCredential([]any{map[string]any{"url": "https://u:Hunter2@a.test", "name": "a"}}, priorMaps) {
		t.Fatal("changed credential inside imported map element not detected")
	}
}

// An unquoted assignment value must not swallow a later query pair: a '='
// in a path, a redirect parameter carrying a SAS URL, or url=...?sig=...
// inside a property still exposes the nested signature or key.
func TestImportSettingsCredentialGuardNestedQueryPairs(t *testing.T) {
	for _, tc := range []struct {
		name, path, value string
	}{
		{"equals in path before sig", "config.eventhubs.checkpoint_container_url", `"https://acct.blob.core.windows.net/c/a=b?sig=AbCdEfGh1234"`},
		{"redirect carrying SAS URL", "config.eventhubs.checkpoint_container_url", `"https://h.test/r?next=https://a.blob.core.windows.net/c?sig=AbCdEfGh1234"`},
		{"equals in path before api_key", "config.appium.server_url", `"https://hub.test/v1/filter=active?api_key=AbCdEfGh1234"`},
		{"fragment before api_key", "config.appium.server_url", `"https://hub.test/v1/x=y#api_key=AbCdEfGh1234"`},
		{"property url with sig", "config.kafka.properties", `{"blob":"url=https://a.blob.core.windows.net/c?sig=AbCdEfGh1234"}`},
		{"chained assignments", "config.kafka.properties", `{"blob":"a=b=sig=AbCdEfGh1234"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)})
			if draft != nil || errs[tc.path] != "Use Credentials for sensitive values" {
				t.Fatalf("plaintext credential accepted: draft=%v errs=%v", draft != nil, errs)
			}
		})
	}
	for _, tc := range []struct {
		name, path, value string
	}{
		{"redirect with sig reference", "config.eventhubs.checkpoint_container_url", `"https://h.test/r?next=https://a.blob.core.windows.net/c?sig={{secret.SAS_SIG}}"`},
		{"equals in path, plain query", "config.appium.server_url", `"https://hub.test/v1/filter=active?page=2#top"`},
		{"property url without credential", "config.kafka.properties", `{"blob":"url=https://a.blob.core.windows.net/c?sv=2020-08-04"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)}); len(errs) != 0 {
				t.Fatalf("ordinary value rejected: %v", errs)
			}
		})
	}
}

// Each list item is inspected at most once, however many imported elements
// it is compared against: re-sending many copies of an imported
// credential-bearing element against a large imported list stays cheap.
func TestImportSettingsCredentialGuardListWorkIsBounded(t *testing.T) {
	carrier := strings.Repeat("word ", 13000) + "https://a.blob.core.windows.net/c?sig=AbCdEfGh1234"
	imported := make([]any, 0, 10000)
	for i := 0; i < 9999; i++ {
		imported = append(imported, fmt.Sprintf("host-%d.test", i))
	}
	imported = append(imported, carrier)
	value := make([]any, 0, 200)
	for i := 0; i < 200; i++ {
		value = append(value, carrier)
	}
	start := time.Now()
	if importSettingValueCarriesCredential(value, imported) {
		t.Fatal("re-sent imported credential-bearing element treated as new")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("list guard took %v; each item must be inspected at most once", elapsed)
	}
	// Distinct new items are still each inspected.
	if !importSettingValueCarriesCredential(append(slices.Clone(value), carrier+"0"), imported) {
		t.Fatal("new credential-bearing element not detected")
	}
	// Map items: a leaf equal to the same key's value in any imported element
	// is script-sourced; a new leaf is inspected.
	priorMaps := make([]any, 0, 10000)
	for i := 0; i < 9999; i++ {
		priorMaps = append(priorMaps, map[string]any{"url": fmt.Sprintf("https://h%d.test", i)})
	}
	priorMaps = append(priorMaps, map[string]any{"url": carrier, "name": "x"})
	maps := make([]any, 0, 200)
	for i := 0; i < 200; i++ {
		maps = append(maps, map[string]any{"url": carrier, "name": fmt.Sprintf("n%d", i)})
	}
	start = time.Now()
	if importSettingValueCarriesCredential(maps, priorMaps) {
		t.Fatal("imported credential-bearing map leaf treated as new")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("map list guard took %v; each leaf must be inspected at most once", elapsed)
	}
}

// An imported header whose name a projection masks (client_secret, as in a real
// JMeter plan's HTTP Header Manager) is script-sourced, not a secret. The
// default_headers map stays editable: an unchanged resubmit and an added
// unrelated header pass, while a newly typed or changed credential header is
// still rejected.
func TestImportSettingsImportedCredentialHeaderKeepsMapEditable(t *testing.T) {
	imported := func() *dsl.Script {
		script := settingsTestScript()
		script.Config.HTTP.DefaultHeaders = map[string]string{
			"Accept": "application/json", "Content-Type": "application/json",
			"client_id": "imported-client", "client_secret": "imported-client-secret-value",
		}
		return script
	}
	unchanged := `{"Accept":"application/json","Content-Type":"application/json","client_id":"imported-client","client_secret":"imported-client-secret-value"}`
	withProbe := `{"Accept":"application/json","Content-Type":"application/json","client_id":"imported-client","client_secret":"imported-client-secret-value","X-Probe":"1"}`
	removed := `{"Accept":"application/json","client_id":"imported-client","client_secret":"imported-client-secret-value"}`
	for name, value := range map[string]string{"unchanged": unchanged, "added header": withProbe, "removed sibling": removed} {
		draft, errs := applyImportSettings(imported(), "all", []ImportSettingsChange{settingChange("config.http.default_headers", value)})
		if len(errs) != 0 || draft == nil {
			t.Fatalf("%s resubmit rejected: %v", name, errs)
		}
		if got := draft.Config.HTTP.DefaultHeaders["client_secret"]; got != "imported-client-secret-value" {
			t.Fatalf("%s: imported header changed to %q", name, got)
		}
	}
	for name, value := range map[string]string{
		"new credential header":     `{"Accept":"application/json","client_secret":"imported-client-secret-value","X-Api-Key":"Hunter2PlainKey"}`,
		"changed credential header": `{"Accept":"application/json","client_secret":"Hunter2PlainSecret"}`,
		"redacted placeholder":      `{"Accept":"application/json","client_secret":"[REDACTED]"}`,
	} {
		if draft, errs := applyImportSettings(imported(), "all", []ImportSettingsChange{settingChange("config.http.default_headers", value)}); draft != nil || errs["config.http.default_headers"] == "" {
			t.Errorf("%s accepted: draft=%v errs=%v", name, draft != nil, errs)
		}
	}
}

// Import contract selects do not list "": the editor renders its own Default
// entry, and a listed "" would render a second, unlabeled blank option. An
// empty selection is still accepted as unset, so an imported field that was
// never set resubmits cleanly.
func TestImportSettingsSelectEmptyIsUnsetNotAnOption(t *testing.T) {
	for _, f := range importSettingFields {
		if slices.Contains(f.Options, "") {
			t.Errorf("%s: contract options list \"\" (duplicate blank option in the editor)", f.Path)
		}
	}
	for _, path := range []string{"config.http.version", "config.http.html_parse_mode", "config.http.proxy_mode", "config.web_runtime.network_simulation.mode"} {
		if _, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange(path, `""`)}); len(errs) != 0 {
			t.Errorf("%s: empty (unset) selection rejected: %v", path, errs)
		}
	}
	if _, errs := applyImportSettings(settingsTestScript(), "all", []ImportSettingsChange{settingChange("config.http.version", `"9"`)}); len(errs) == 0 {
		t.Error("invalid select value accepted")
	}
}

// LoadRunner web imports emit network_simulation.mode "max"; re-sending it
// unchanged must pass the catalog-only select check.
func TestImportSettingsAcceptsImportedNetworkSimulationMax(t *testing.T) {
	script := settingsTestScript()
	script.Config.WebRuntime.NetworkSimulation.Mode = "max"
	if _, errs := applyImportSettings(script, "all", []ImportSettingsChange{settingChange("config.web_runtime.network_simulation.mode", `"max"`)}); len(errs) != 0 {
		t.Fatalf("imported max mode rejected: %v", errs)
	}
}

// A list element has no stable identity, so a new or changed element must not
// be checked against whatever prior element shares its index: an insert, or a
// removal beside an edit, shifts an unchanged imported credential element to a
// new index without changing it.
func TestImportSettingsListEditsBesideUnchangedImportedCredentialElements(t *testing.T) {
	const (
		headerCfg = `{"http":{"default_header_list":[{"name":"X-A","value":"1"},{"name":"Authorization","value":"Bearer AbCdEf123456ghij"},{"name":"X-C","value":"3"}]}}`
		vmCfg     = `{"java_vuser":{"additional_vm_parameters":["-Xms64m","-Djavax.net.ssl.keyStorePassword=AbCdEf123456ghij","-Xss1m"]}}`
		headers   = "config.http.default_header_list"
		vmParams  = "config.java_vuser.additional_vm_parameters"
		auth      = `{"name":"Authorization","value":"Bearer AbCdEf123456ghij"}`
		keyStore  = `"-Djavax.net.ssl.keyStorePassword=AbCdEf123456ghij"`
	)
	script := func(t *testing.T, cfg string) *dsl.Script {
		t.Helper()
		s := settingsTestScript()
		if err := json.Unmarshal([]byte(cfg), &s.Config); err != nil {
			t.Fatal(err)
		}
		s.Config.BaseURL = "https://example.test"
		return s
	}
	for _, tc := range []struct{ name, cfg, path, value string }{
		{"header list prepend", headerCfg, headers, `[{"name":"X-Probe","value":"1"},{"name":"X-A","value":"1"},` + auth + `,{"name":"X-C","value":"3"}]`},
		{"header list insert in the middle", headerCfg, headers, `[{"name":"X-A","value":"1"},{"name":"X-Probe","value":"1"},` + auth + `,{"name":"X-C","value":"3"}]`},
		{"header list remove one and edit another", headerCfg, headers, `[{"name":"X-B","value":"2"},` + auth + `]`},
		{"vm params prepend", vmCfg, vmParams, `["-Xmx512m","-Xms64m",` + keyStore + `,"-Xss1m"]`},
		{"vm params insert in the middle", vmCfg, vmParams, `["-Xms64m","-Xmx512m",` + keyStore + `,"-Xss1m"]`},
		{"vm params remove one and edit another", vmCfg, vmParams, `["-Xms128m",` + keyStore + `]`},
		// Editing another field of the element that holds the unchanged
		// imported credential, after its index moved.
		{"header list toggle credential element in place", headerCfg, headers, `[{"name":"X-A","value":"1"},{"name":"Authorization","value":"Bearer AbCdEf123456ghij","enabled":false},{"name":"X-C","value":"3"}]`},
		{"header list prepend and toggle credential element", headerCfg, headers, `[{"name":"X-P","value":"1"},{"name":"X-A","value":"1"},{"name":"Authorization","value":"Bearer AbCdEf123456ghij","enabled":false},{"name":"X-C","value":"3"}]`},
		{"header list remove first and toggle credential element", headerCfg, headers, `[{"name":"Authorization","value":"Bearer AbCdEf123456ghij","enabled":false},{"name":"X-C","value":"3"}]`},
		{"header list reorder and toggle credential element", headerCfg, headers, `[{"name":"Authorization","value":"Bearer AbCdEf123456ghij","enabled":false},{"name":"X-A","value":"1"},{"name":"X-C","value":"3"}]`},
		{"header list remove first and rename credential element", headerCfg, headers, `[{"name":"authorization","value":"Bearer AbCdEf123456ghij"},{"name":"X-C","value":"3"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft, errs := applyImportSettings(script(t, tc.cfg), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)})
			if len(errs) != 0 || draft == nil {
				t.Fatalf("benign list edit beside an unchanged imported credential element rejected: %v", errs)
			}
			raw, _ := json.Marshal(draft)
			if !strings.Contains(string(raw), "AbCdEf123456ghij") {
				t.Fatal("unchanged imported credential element was not preserved")
			}
		})
	}
	for _, tc := range []struct{ name, cfg, path, value string }{
		{"header list masked element", headerCfg, headers, `[{"name":"X-A","value":"1"},{"name":"Authorization","value":"[REDACTED]"},{"name":"X-C","value":"3"}]`},
		{"header list new literal credential", headerCfg, headers, `[{"name":"X-Api-Key","value":"Hunter2PlainKey99"},{"name":"X-A","value":"1"},` + auth + `,{"name":"X-C","value":"3"}]`},
		{"header list changed credential", headerCfg, headers, `[{"name":"X-A","value":"1"},{"name":"Authorization","value":"Bearer Hunter2Changed99"},{"name":"X-C","value":"3"}]`},
		{"header list prepend and change credential", headerCfg, headers, `[{"name":"X-P","value":"1"},{"name":"X-A","value":"1"},{"name":"Authorization","value":"Bearer Hunter2Changed99"},{"name":"X-C","value":"3"}]`},
		{"header list replace element with new credential", headerCfg, headers, `[{"name":"X-A","value":"1"},` + auth + `,{"name":"X-Api-Key","value":"Hunter2PlainKey99"}]`},
		{"vm params masked element", vmCfg, vmParams, `["-Xms64m","-Djavax.net.ssl.keyStorePassword=[REDACTED]","-Xss1m"]`},
		{"vm params new literal credential", vmCfg, vmParams, `["-Djavax.net.ssl.trustStorePassword=Hunter2PlainKey99","-Xms64m",` + keyStore + `,"-Xss1m"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if draft, errs := applyImportSettings(script(t, tc.cfg), "all", []ImportSettingsChange{settingChange(tc.path, tc.value)}); draft != nil || errs[tc.path] == "" {
				t.Fatalf("accepted: draft=%v errs=%v", draft != nil, errs)
			}
		})
	}
}
