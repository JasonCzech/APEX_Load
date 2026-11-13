package dsl

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/protocol/browsercontract"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

func catalogEntry(t *testing.T, path string) ConfigFieldMeta {
	t.Helper()
	meta, ok := ConfigFieldForPath(path)
	if !ok {
		t.Fatalf("missing catalog entry %s", path)
	}
	return meta
}

func flattenDefaultConfig(prefix string, node any, out map[string]any) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			flattenDefaultConfig(prefix+"."+key, value, out)
		}
	default:
		out[prefix] = typed
	}
}

// Every concrete value DefaultScriptConfig states (the runtime's own defaults,
// served by /api/v1/dsl/config-defaults) must be the catalog default too.
func TestConfigCatalogDefaultsMatchDefaultScriptConfig(t *testing.T) {
	raw, err := json.Marshal(DefaultScriptConfig())
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	leaves := map[string]any{}
	flattenDefaultConfig("config", tree, leaves)
	checked := 0
	for path, want := range leaves {
		meta, ok := ConfigFieldForPath(path)
		// An empty string is the unset zero value, not a stated default. An
		// unset orientation follows the device profile, so landscape is only
		// the fallback when no profile names one.
		if !ok || want == "" || path == "config.browser.orientation" {
			continue
		}
		checked++
		if fmt.Sprint(meta.Default) != fmt.Sprint(want) {
			t.Errorf("%s: catalog Default %v, DefaultScriptConfig %v", path, meta.Default, want)
		}
	}
	if checked < 10 {
		t.Fatalf("compared only %d DefaultScriptConfig leaves", checked)
	}
}

func TestConfigCatalogDefaultsMatchRuntimeBehavior(t *testing.T) {
	for _, tc := range []struct {
		path string
		want any
	}{
		{"config.http.max_idle_conns", httpcontract.DefaultMaxIdleConns},
		{"config.http.traceparent_sampled", true},
		{"config.soap.fault_handling.fail_on_fault", false},
		{"config.browser.viewport.width", browsercontract.DefaultViewportWidth},
		{"config.browser.viewport.height", browsercontract.DefaultViewportHeight},
		// Unset means a behaviour no explicit value reproduces, so the catalog
		// must not state one: automatic HTTP version, proxy URL routing,
		// content-bearing HTML discovery, headers the runtime never sends, and
		// the scenario's continue-on-error policy.
		{"config.http.version", nil},
		{"config.http.proxy_mode", nil},
		{"config.http.html_parse_mode", nil},
		{"config.http.user_agent", nil},
		{"config.http.accept_encoding", nil},
		{"config.http.accept_language", nil},
		{"config.error_handling.continue_on_error", nil},
		// A plain bool the runtime leaves off unless the script sets it.
		{"config.error_handling.snapshot_on_error", false},
	} {
		if got := catalogEntry(t, tc.path).Default; got != tc.want {
			t.Errorf("%s: Default = %#v, want %#v", tc.path, got, tc.want)
		}
	}
	for _, path := range []string{"config.http.version", "config.http.proxy_mode", "config.http.html_parse_mode"} {
		if !slices.Contains(catalogEntry(t, path).Options, "") {
			t.Errorf("%s: the unset (automatic) mode must be selectable", path)
		}
	}
	for _, entry := range ConfigCatalog() {
		if entry.Type == ConfigFieldTypeSelect && entry.Default != nil && !slices.Contains(entry.Options, fmt.Sprint(entry.Default)) {
			t.Errorf("%s: Default %v is not one of its Options %v", entry.Path, entry.Default, entry.Options)
		}
	}
}

// catalogSelectUnprefixedIssues lists, for validators whose message does not
// start with the field path, a phrase that identifies a rejection of that
// path. TestConfigCatalogSelectOptionsPassValidators self-checks that every
// validator rejecting an invalid sentinel is attributed, so a new unprefixed
// message fails the test until it is listed here.
var catalogSelectUnprefixedIssues = map[string]string{
	"config.http.ip_version":                   "config.http: ip_version ",
	"config.http.html_parse_mode":              "config.http: html_parse_mode ",
	"config.http.aura_recorded_failures":       "config.http: aura_recorded_failures ",
	"config.kafka.oauth.authentication_method": "Kafka OAuth authentication_method",
	// min_version and max_version share one message; a probe sets only one.
	"config.kafka.tls.min_version":      "Kafka TLS version must be",
	"config.kafka.tls.max_version":      "Kafka TLS version must be",
	"config.eventhubs.consumer_mode":    "eventhubs consumer_mode must be",
	"config.run_logic.termination_mode": "termination_mode must be",
}

// catalogIssueRejectsPath reports whether a validator issue rejects path.
func catalogIssueRejectsPath(issue, path string) bool {
	if strings.HasPrefix(issue, path+" ") || strings.HasPrefix(issue, path+":") {
		return true
	}
	phrase, ok := catalogSelectUnprefixedIssues[path]
	return ok && strings.Contains(issue, phrase)
}

// catalogProbeIssues validates a script of protocol whose config carries
// value at path and returns every validator issue.
func catalogProbeIssues(t *testing.T, protocol, path, value string) []string {
	t.Helper()
	segments := strings.Split(path, ".")
	root := map[string]any{"name": "catalog-probe", "protocol": protocol}
	node := root
	for _, segment := range segments[:len(segments)-1] {
		child, ok := node[segment].(map[string]any)
		if !ok {
			child = map[string]any{}
			node[segment] = child
		}
		node = child
	}
	node[segments[len(segments)-1]] = value
	raw, _ := json.Marshal(root)
	var script Script
	if err := json.Unmarshal(raw, &script); err != nil {
		t.Fatalf("%s=%q for %s: %v", path, value, protocol, err)
	}
	return ValidateScriptIssues(&script)
}

// catalogSelectIssues validates a script of protocol whose config carries
// value at path, and returns the issues that reject that path: messages that
// start with the field path, or that carry the path's phrase in
// catalogSelectUnprefixedIssues.
func catalogSelectIssues(t *testing.T, protocol, path, value string) []string {
	t.Helper()
	var rejected []string
	for _, issue := range catalogProbeIssues(t, protocol, path, value) {
		if !catalogIssueRejectsPath(issue, path) {
			continue
		}
		// A value valid in itself but needing another field or context
		// (a schema registry, a TLS listener, a lower min_version, the
		// protocol variant, mock mode) is not a vocabulary rejection.
		if catalogCrossFieldIssue(issue) {
			continue
		}
		rejected = append(rejected, issue)
	}
	return rejected
}

const catalogInvalidSentinel = "zz_invalid_catalog_probe"

func catalogCrossFieldIssue(issue string) bool {
	for _, phrase := range []string{" requires ", "must not be lower than", "does not match protocol", "is not supported by real adapters"} {
		if strings.Contains(issue, phrase) {
			return true
		}
	}
	return false
}

// Every select entry's Options and Default must pass the script validator for
// every protocol the entry applies to, so the catalog an import-settings or
// Author editor offers cannot drift from validate.go. Where the accepted
// vocabulary is closed, the catalog must also offer every accepted value.
func TestConfigCatalogSelectOptionsPassValidators(t *testing.T) {
	checked := 0
	for _, entry := range ConfigCatalog() {
		if entry.Type != ConfigFieldTypeSelect || !strings.HasPrefix(entry.Path, "config.") || len(entry.Scopes) == 0 {
			continue
		}
		protocols := entry.Protocols
		if len(protocols) == 0 {
			protocols = []string{"http"}
		}
		values := slices.Clone(entry.Options)
		if entry.Default != nil {
			values = append(values, fmt.Sprint(entry.Default))
		}
		for _, protocol := range protocols {
			baseline := ""
			for _, value := range values {
				if value == "" {
					continue
				}
				if baseline == "" {
					baseline = value
				}
				checked++
				if issues := catalogSelectIssues(t, protocol, entry.Path, value); len(issues) > 0 {
					t.Errorf("%s (%s): catalog value %q rejected: %v", entry.Path, protocol, value, issues)
				}
			}
			// Self-check the filter: when the validator rejects an invalid
			// sentinel at all (an issue not raised for a catalog value), the
			// filter must attribute that rejection to this path, or a bad
			// catalog option would pass unseen. A validator that accepts any
			// value has nothing to detect.
			if baseline == "" {
				continue
			}
			baseIssues := catalogProbeIssues(t, protocol, entry.Path, baseline)
			var sentinelOnly []string
			for _, issue := range catalogProbeIssues(t, protocol, entry.Path, catalogInvalidSentinel) {
				if !slices.Contains(baseIssues, issue) && !catalogCrossFieldIssue(issue) {
					sentinelOnly = append(sentinelOnly, issue)
				}
			}
			if len(sentinelOnly) > 0 && len(catalogSelectIssues(t, protocol, entry.Path, catalogInvalidSentinel)) == 0 {
				t.Errorf("%s (%s): the validator rejects %q with %q, but the parity filter does not attribute it to the path; add a phrase to catalogSelectUnprefixedIssues", entry.Path, protocol, catalogInvalidSentinel, sentinelOnly)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("checked only %d select values", checked)
	}
	for _, tc := range []struct {
		path, protocol string
		candidates     []string
	}{
		{"config.java_http.codec", "java_http", []string{"java_serialized_object_base64", "raw", "xml", "json", "java_serialized", "hessian"}},
		{"config.java_http.helper_mode", "java_http", []string{"embedded", "sidecar", "none", "passthrough", "mapped"}},
		{"config.java_http.body_encoding", "java_http", []string{"base64", "text", "hex"}},
		{"config.browser.engine", "browser", []string{"chromium", "webkit", "firefox"}},
		{"config.browser.download_resources", "browser", []string{"none", "recorded", "all", "html_only", "critical"}},
		{"config.sapgui.session_behavior", "sap_gui", []string{"attach", "new_session", "reuse", "reset_per_iteration"}},
		{"config.database.driver", "database", []string{"sqlite", "sqlite3", "postgres", "mysql", "odbc", "oracle", "sqlserver", "mssql"}},
		{"config.jms.acknowledgement", "jms-solace", []string{"auto", "dups_ok", "client", "transacted", "none"}},
	} {
		options := catalogEntry(t, tc.path).Options
		for _, candidate := range tc.candidates {
			accepted := len(catalogSelectIssues(t, tc.protocol, tc.path, candidate)) == 0
			if accepted != slices.Contains(options, candidate) {
				t.Errorf("%s: validator accepts %q = %v, but catalog options %v", tc.path, candidate, accepted, options)
			}
		}
	}
}

func TestConfigCatalogOptionsMatchValidators(t *testing.T) {
	for _, option := range catalogEntry(t, "config.run_logic.termination_mode").Options {
		for _, issue := range ValidateWorkloadSettingsIssues(RunLogicConfig{TerminationMode: option, Iterations: 1, WorkloadModel: "closed"}, PacingConfig{}) {
			if strings.Contains(issue, "termination_mode") {
				t.Errorf("termination_mode option %q rejected: %s", option, issue)
			}
		}
	}
	for _, option := range catalogEntry(t, "config.http.auth.type").Options {
		if err := httpcontract.ValidateAuthType(option); err != nil {
			t.Errorf("auth.type option %q rejected: %v", option, err)
		}
	}
	for _, option := range catalogEntry(t, "config.http.version").Options {
		if err := httpcontract.ValidateVersion(option); err != nil {
			t.Errorf("http.version option %q rejected: %v", option, err)
		}
	}
	thinkOptions := catalogEntry(t, "config.think_time.type").Options
	for _, option := range thinkOptions {
		if issues := validateThinkTimeResourceIssues("config.think_time", ThinkTimeConfig{Type: option}); len(issues) > 0 {
			t.Errorf("think_time.type option %q rejected: %v", option, issues)
		}
	}
	for _, accepted := range []string{"none", "fixed", "recorded", "random_percentage", "gaussian", "normal", "uniform", "exponential"} {
		if !slices.Contains(thinkOptions, accepted) {
			t.Errorf("think_time.type omits validator-accepted %q", accepted)
		}
	}
	// network_simulation.mode: LoadRunner web imports emit "max" (the default
	// "Use maximum bandwidth"), so the catalog must offer every mode the
	// validator accepts and nothing it rejects.
	networkOptions := catalogEntry(t, "config.web_runtime.network_simulation.mode").Options
	networkModeRejected := func(mode string) []string {
		script := &Script{}
		script.Config.WebRuntime.NetworkSimulation = NetworkSimulationConfig{Mode: mode, Preset: "dsl", BandwidthKbps: 512}
		var rejected []string
		for _, issue := range validateWebRuntimeIssues(script) {
			if strings.Contains(issue, "network_simulation.mode") {
				rejected = append(rejected, issue)
			}
		}
		return rejected
	}
	for _, option := range networkOptions {
		if issues := networkModeRejected(option); len(issues) > 0 {
			t.Errorf("network_simulation.mode option %q rejected: %v", option, issues)
		}
	}
	for _, candidate := range []string{"max", "preset", "custom", "disabled", "none", "off", "unlimited"} {
		if len(networkModeRejected(candidate)) == 0 && !slices.Contains(networkOptions, candidate) {
			t.Errorf("network_simulation.mode omits validator-accepted %q (options %v)", candidate, networkOptions)
		}
	}
	// Web runtime string enums are selects offering exactly the values
	// validateWebRuntimeIssues accepts, plus the unset value.
	webRuntimeEnums := []struct {
		path       string
		accepted   []string
		candidates []string
		probe      func(value string) *Script
	}{
		{"config.web_runtime.network_simulation.preset", []string{"56k", "dsl", "t1", "10mbps"}, []string{"56k", "dsl", "t1", "10mbps", "3g", "lte", "cable"}, func(value string) *Script {
			script := &Script{}
			script.Config.WebRuntime.NetworkSimulation = NetworkSimulationConfig{Mode: "preset", Preset: value}
			return script
		}},
		{"config.web_runtime.streaming.mode", []string{"adaptive", "fixed"}, []string{"adaptive", "fixed", "abr", "constant"}, func(value string) *Script {
			script := &Script{}
			script.Config.WebRuntime.Streaming.Mode = value
			script.Config.WebRuntime.Streaming.FixedBitrateKbps = 512
			return script
		}},
		{"config.web_runtime.streaming.bandwidth_estimation", []string{"rolling", "instant"}, []string{"rolling", "instant", "ewma", "average"}, func(value string) *Script {
			script := &Script{}
			script.Config.WebRuntime.Streaming.BandwidthEstimation = value
			return script
		}},
	}
	for _, tc := range webRuntimeEnums {
		meta := catalogEntry(t, tc.path)
		if meta.Type != ConfigFieldTypeSelect {
			t.Errorf("%s: type %q, want select", tc.path, meta.Type)
		}
		if want := append([]string{""}, tc.accepted...); !slices.Equal(meta.Options, want) {
			t.Errorf("%s: options %v, want %v", tc.path, meta.Options, want)
		}
		for _, candidate := range tc.candidates {
			accepted := true
			for _, issue := range validateWebRuntimeIssues(tc.probe(candidate)) {
				if strings.HasPrefix(issue, tc.path+" ") {
					accepted = false
				}
			}
			if accepted != slices.Contains(meta.Options, candidate) {
				t.Errorf("%s: validator accepts %q = %v, but catalog options %v", tc.path, candidate, accepted, meta.Options)
			}
		}
	}
}

// config.think_time.force is honoured only on an action's think_time (the
// replay loop reads action.ThinkTime.Force), so the catalog must say so.
func TestConfigCatalogThinkTimeForceIsActionLevel(t *testing.T) {
	description := catalogEntry(t, "config.think_time.force").Description
	if !strings.Contains(strings.ToLower(description), "action-level think time") {
		t.Errorf("config.think_time.force description %q must say it only applies to action-level think time", description)
	}
}

func TestConfigCatalogScenarioScopeFollowsOverrideSections(t *testing.T) {
	for _, path := range []string{"config.kafka.brokers", "config.jms.broker_url", "config.java_vuser.runner_command", "config.java_vuser.harness_jar", "config.java_vuser.enabled", "config.observability.trace_carrier"} {
		meta := catalogEntry(t, path)
		if slices.Contains(meta.Scopes, ConfigScopeScenario) {
			t.Errorf("%s: a scenario override cannot carry this field", path)
		}
		if !slices.Contains(meta.Scopes, ConfigScopeAuthor) {
			t.Errorf("%s: dropping scenario must keep the other scopes, got %v", path, meta.Scopes)
		}
	}
	for _, path := range []string{"config.http.version", "config.java_vuser.jdk_home", "config.soap.fault_handling.fail_on_fault", "config.base_url"} {
		if !slices.Contains(catalogEntry(t, path).Scopes, ConfigScopeScenario) {
			t.Errorf("%s: scenario-overridable field lost the scenario scope", path)
		}
	}
	for _, entry := range ConfigCatalogForFilter("kafka", ConfigScopeScenario) {
		if strings.HasPrefix(entry.Path, "config.kafka.") {
			t.Errorf("kafka scenario catalog lists %s", entry.Path)
		}
	}
}

func TestConfigCatalogSourceOwnedFieldsAreNotImportEditable(t *testing.T) {
	for _, entry := range ConfigCatalog() {
		if strings.HasPrefix(entry.Path, "config.jmeter.") || entry.Path == "config.sampler_schedules" {
			if slices.Contains(entry.Scopes, ConfigScopeImport) || slices.Contains(entry.Scopes, ConfigScopeScenario) {
				t.Errorf("%s: source-owned field has scopes %v", entry.Path, entry.Scopes)
			}
		}
	}
}

func TestConfigCatalogForFilterNormalizesVersionedHTTPProtocols(t *testing.T) {
	want := ConfigCatalogForFilter("http", ConfigScopeImport)
	if len(want) == 0 {
		t.Fatal("no http import fields")
	}
	for _, alias := range []string{"http/1.1", "http/2", "http/3", " HTTP/2 "} {
		got := ConfigCatalogForFilter(alias, ConfigScopeImport)
		if len(got) != len(want) {
			t.Errorf("protocol %q: %d fields, want %d (same as http)", alias, len(got), len(want))
		}
	}
	if got := ConfigCatalogForFilter("jms-solace", ""); len(got) == 0 {
		t.Error("provider-suffixed JMS protocol lost its fields")
	}
}

// Credential-bearing fields must be Secret-flagged: import settings rely on the
// flag to send plaintext to Credentials. Fields naming a stored secret
// (*_secret, *_ref), endpoint addresses, retired fields and durations or
// behaviours of a session are not credentials.
func TestConfigCatalogCredentialFieldsAreSecret(t *testing.T) {
	for _, entry := range ConfigCatalog() {
		leaf := entry.Path[strings.LastIndex(entry.Path, ".")+1:]
		if len(entry.Scopes) == 0 || credential.NonCredentialName(leaf) ||
			strings.HasSuffix(leaf, "_ref") || (strings.HasSuffix(leaf, "_secret") && leaf != "client_secret") ||
			strings.HasSuffix(leaf, "_ms") || leaf == "session_behavior" {
			continue
		}
		if credential.SensitiveName(leaf) && !entry.Secret {
			t.Errorf("%s: credential-named field is not Secret-flagged", entry.Path)
		}
	}
}
