package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestApplyRuntimeOverridesAppliesExtendedOverrideFields(t *testing.T) {
	headless := false
	touchMode := true
	defaultHeadless := true
	defaultTouchMode := false
	iterations := 4
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL:       "https://default.example.com",
			Browser:       dsl.BrowserConfig{Headless: &defaultHeadless, DeviceProfile: "desktop_1080p", TouchMode: &defaultTouchMode, Orientation: "landscape"},
			RunLogic:      dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "none"},
			Pacing:        dsl.PacingConfig{Type: "none"},
			ThinkTime:     dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1000},
			ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(false), SnapshotOnError: false},
			Log:           dsl.LogConfig{Level: "standard"},
		},
		Actions: []dsl.Action{{
			Name:    "runtime-step",
			Type:    "GET",
			Target:  "/namespaces/{{env.target_namespace}}/orders",
			Body:    "namespace={{env.target_namespace}}",
			Headers: map[string]string{"X-Namespace": "{{env.target_namespace}}"},
		}},
	}

	err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{
		Iterations:       &iterations,
		Pacing:           &dsl.PacingConfig{Type: "fixed_interval", IntervalMs: 2500},
		ThinkTime:        &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 750},
		Browser:          &RuntimeBrowserOverride{Headless: &headless, DeviceProfile: "iphone_se", TouchMode: &touchMode, Orientation: "portrait"},
		LogLevel:         "extended",
		AutoTransactions: "per_iteration",
		BaseURL:          "https://pr-123.example.com",
		Env:              map[string]string{"target_namespace": "pr-123"},
		ErrorHandling:    &dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true), SnapshotOnError: true},
	})
	if err != nil {
		t.Fatalf("applyRuntimeOverrides returned error: %v", err)
	}

	if script.Config.RunLogic.Iterations != 4 {
		t.Fatalf("iterations = %d, want 4", script.Config.RunLogic.Iterations)
	}
	if script.Config.BaseURL != "https://pr-123.example.com" {
		t.Fatalf("base_url = %q, want https://pr-123.example.com", script.Config.BaseURL)
	}
	if script.Config.Pacing.Type != "fixed_interval" || script.Config.Pacing.IntervalMs != 2500 {
		t.Fatalf("pacing = %#v, want fixed_interval 2500ms", script.Config.Pacing)
	}
	if script.Config.ThinkTime.Type != "fixed" || script.Config.ThinkTime.MeanMs != 750 {
		t.Fatalf("think_time = %#v, want fixed 750ms", script.Config.ThinkTime)
	}
	if script.Config.Browser.Headless == nil || *script.Config.Browser.Headless != false {
		t.Fatalf("browser.headless = %#v, want false", script.Config.Browser.Headless)
	}
	if script.Config.Browser.DeviceProfile != "iphone_se" {
		t.Fatalf("browser.device_profile = %q, want iphone_se", script.Config.Browser.DeviceProfile)
	}
	if script.Config.Browser.TouchMode == nil || *script.Config.Browser.TouchMode != true {
		t.Fatalf("browser.touch_mode = %#v, want true", script.Config.Browser.TouchMode)
	}
	if script.Config.Browser.Orientation != "portrait" {
		t.Fatalf("browser.orientation = %q, want portrait", script.Config.Browser.Orientation)
	}
	if script.Config.Log.Level != "extended" {
		t.Fatalf("log level = %q, want extended", script.Config.Log.Level)
	}
	if script.Config.RunLogic.AutoTransactions != "per_iteration" {
		t.Fatalf("auto transactions = %q, want per_iteration", script.Config.RunLogic.AutoTransactions)
	}
	if !script.Config.ErrorHandling.ContinueOnErrorEnabled() || !script.Config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error handling = %#v, want continue_on_error and snapshot_on_error", script.Config.ErrorHandling)
	}
	if len(script.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(script.Actions))
	}
	if script.Actions[0].Target != "/namespaces/pr-123/orders" {
		t.Fatalf("target = %q, want /namespaces/pr-123/orders", script.Actions[0].Target)
	}
	if script.Actions[0].Body != "namespace=pr-123" {
		t.Fatalf("body = %q, want namespace=pr-123", script.Actions[0].Body)
	}
	if script.Actions[0].Headers["X-Namespace"] != "pr-123" {
		t.Fatalf("header X-Namespace = %q, want pr-123", script.Actions[0].Headers["X-Namespace"])
	}
}

func TestCloneRuntimeSettingsDoesNotAliasMutableFields(t *testing.T) {
	iterations := 3
	headless := true
	touch := false
	source := &RuntimeSettingsOverride{
		Iterations: &iterations,
		Pacing:     &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
		Browser:    &RuntimeBrowserOverride{Headless: &headless, TouchMode: &touch, DeviceProfile: "desktop_1080p"},
		Env:        map[string]string{"namespace": "blue"},
		RedactionRules: []engine.RedactionRule{
			{Name: "token", Pattern: `token-[0-9]+`},
		},
	}

	clone := cloneRuntimeSettings(source)
	*source.Iterations = 9
	source.Pacing.DelayMs = 999
	*source.Browser.Headless = false
	*source.Browser.TouchMode = true
	source.Env["namespace"] = "green"
	source.RedactionRules[0].Pattern = `changed`

	if clone.Iterations == nil || *clone.Iterations != 3 {
		t.Fatalf("cloned iterations = %#v, want 3", clone.Iterations)
	}
	if clone.Pacing == nil || clone.Pacing.DelayMs != 100 {
		t.Fatalf("cloned pacing = %#v, want delay 100", clone.Pacing)
	}
	if clone.Browser == nil || clone.Browser.Headless == nil || *clone.Browser.Headless != true {
		t.Fatalf("cloned browser headless = %#v, want true", clone.Browser)
	}
	if clone.Browser.TouchMode == nil || *clone.Browser.TouchMode != false {
		t.Fatalf("cloned browser touch = %#v, want false", clone.Browser.TouchMode)
	}
	if clone.Env["namespace"] != "blue" {
		t.Fatalf("cloned env = %#v, want original value", clone.Env)
	}
	if clone.RedactionRules[0].Pattern != `token-[0-9]+` {
		t.Fatalf("cloned redaction pattern = %q, want original", clone.RedactionRules[0].Pattern)
	}
}

func TestMergeRuntimeSettingsPreservesBaseAndAppliesGroupPrecedence(t *testing.T) {
	baseIterations := 2
	groupIterations := 5
	baseHeadless := true
	groupTouch := true

	base := &RuntimeSettingsOverride{
		Iterations:       &baseIterations,
		BaseURL:          "https://base.example",
		LogLevel:         "standard",
		AutoTransactions: "per_iteration",
		Browser:          &RuntimeBrowserOverride{Headless: &baseHeadless, DeviceProfile: "desktop_1080p", Orientation: "landscape"},
		Env:              map[string]string{"region": "east", "namespace": "base"},
		RedactionRules:   []engine.RedactionRule{{Name: "base", Pattern: `base-[0-9]+`}},
	}
	group := &RuntimeSettingsOverride{
		Iterations:     &groupIterations,
		BaseURL:        "https://group.example",
		LogLevel:       "extended",
		Browser:        &RuntimeBrowserOverride{TouchMode: &groupTouch, DeviceProfile: "iphone_15_pro"},
		Env:            map[string]string{"namespace": "group"},
		RedactionRules: []engine.RedactionRule{{Name: "group", Pattern: `group-[0-9]+`}},
	}

	merged := mergeRuntimeSettings(base, group)
	if merged.Iterations == nil || *merged.Iterations != 5 {
		t.Fatalf("iterations = %#v, want group override 5", merged.Iterations)
	}
	if merged.BaseURL != "https://group.example" || merged.LogLevel != "extended" {
		t.Fatalf("merged URL/log = %q/%q, want group overrides", merged.BaseURL, merged.LogLevel)
	}
	if merged.AutoTransactions != "per_iteration" {
		t.Fatalf("auto transactions = %q, want base fallback", merged.AutoTransactions)
	}
	if merged.Browser.Headless == nil || *merged.Browser.Headless != true {
		t.Fatalf("headless = %#v, want base fallback true", merged.Browser.Headless)
	}
	if merged.Browser.TouchMode == nil || *merged.Browser.TouchMode != true {
		t.Fatalf("touch = %#v, want group override true", merged.Browser.TouchMode)
	}
	if merged.Browser.DeviceProfile != "iphone_15_pro" || merged.Browser.Orientation != "landscape" {
		t.Fatalf("browser = %#v, want group profile and base orientation", merged.Browser)
	}
	if merged.Env["region"] != "east" || merged.Env["namespace"] != "group" {
		t.Fatalf("env = %#v, want base plus group override", merged.Env)
	}
	if len(merged.RedactionRules) != 1 || merged.RedactionRules[0].Name != "group" {
		t.Fatalf("redaction rules = %#v, want group replacement", merged.RedactionRules)
	}

	*base.Iterations = 11
	base.Env["namespace"] = "mutated"
	group.RedactionRules[0].Pattern = "mutated"
	if *merged.Iterations != 5 || merged.Env["namespace"] != "group" || merged.RedactionRules[0].Pattern != `group-[0-9]+` {
		t.Fatalf("merged settings aliased source fields: %#v", merged)
	}
}

func TestApplyRuntimeOverridesMergesWebRuntimeWithoutClobberingSiblings(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			WebRuntime: dsl.WebRuntimeConfig{
				// A script-level web-runtime setting unrelated to IP spoofing.
				NetworkSimulation: dsl.NetworkSimulationConfig{Mode: "preset", Preset: "dsl"},
				ContentChecks:     []dsl.ContentCheckConfig{{Text: "Access Denied", Scope: "any_response"}},
			},
		},
		Actions: []dsl.Action{{Name: "home", Type: "request", Target: "/"}},
	}

	// Override supplies ONLY source_ips (IP spoofing) — it must not wipe the
	// script's network_simulation/content_checks.
	err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{
		WebRuntime: &dsl.WebRuntimeConfig{SourceIPs: []string{"10.0.0.1", "10.0.0.2"}},
	})
	if err != nil {
		t.Fatalf("applyRuntimeOverrides: %v", err)
	}

	web := script.Config.WebRuntime
	if got := web.SourceIPs; len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "10.0.0.2" {
		t.Fatalf("source_ips = %#v, want override pool", got)
	}
	if web.NetworkSimulation.Mode != "preset" || web.NetworkSimulation.Preset != "dsl" {
		t.Fatalf("network_simulation = %#v, want preserved script value", web.NetworkSimulation)
	}
	if len(web.ContentChecks) != 1 || web.ContentChecks[0].Text != "Access Denied" {
		t.Fatalf("content_checks = %#v, want preserved script value", web.ContentChecks)
	}
}

func TestMergeWebRuntimeOverlaysOnlyProvidedSections(t *testing.T) {
	base := dsl.WebRuntimeConfig{
		SourceIPs:         []string{"10.0.0.1"},
		NetworkSimulation: dsl.NetworkSimulationConfig{Mode: "preset", Preset: "t1"},
	}
	override := dsl.WebRuntimeConfig{
		SourceIPs: []string{"172.16.0.1", "172.16.0.2"},
	}

	merged := mergeWebRuntime(base, override)
	if len(merged.SourceIPs) != 2 || merged.SourceIPs[0] != "172.16.0.1" {
		t.Fatalf("source_ips = %#v, want override pool", merged.SourceIPs)
	}
	if merged.NetworkSimulation.Preset != "t1" {
		t.Fatalf("network_simulation = %#v, want base preserved", merged.NetworkSimulation)
	}

	// Mutating the override's slice must not alias into the merged result.
	override.SourceIPs[0] = "mutated"
	if merged.SourceIPs[0] != "172.16.0.1" {
		t.Fatalf("merged source_ips aliased override slice: %#v", merged.SourceIPs)
	}
}

func TestApplyRuntimeOverrides_SOAPWholeSectionAndPartialMerge(t *testing.T) {
	failOnFaultTrue := true
	failOnFaultFalse := false
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL: "https://soap.example.com",
			HTTP: dsl.HTTPConfig{
				RequestTimeoutMs:  30000,
				TLS:               dsl.TLSConfig{MinVersion: "1.2", MaxVersion: "1.3"},
				DefaultHeaders:    map[string]string{"X-Keep": "true"},
				DefaultHeaderList: dsl.HeaderList{{Name: "X-List-Keep", Value: "1"}},
			},
			ErrorHandling: dsl.ErrorHandlingConfig{
				ContinueOnError: dsl.ContinueOnErrorValue(true),
				MaxRetries:      3,
				SnapshotOnError: true,
			},
			SOAP: dsl.SOAPConfig{
				Version:    "1.1",
				Namespaces: map[string]string{"ns1": "http://example.com/ns1"},
				WSSecurity: &dsl.SOAPWSSecurityConfig{
					UsernameToken: &dsl.SOAPUsernameTokenConfig{
						Username: "script-user",
						Password: "{{secret.SCRIPT_PASS}}",
					},
					Timestamp: &dsl.SOAPTimestampConfig{TTLSeconds: 60},
				},
				FaultHandling: dsl.SOAPFaultHandlingConfig{FailOnFault: &failOnFaultTrue},
			},
		},
	}

	// Partial merge mode: only listed override_fields are applied; siblings are preserved,
	// and explicit false/0/""/[] values are honored.
	partial := &RuntimeSettingsOverride{
		BaseURL: "",
		HTTP: &dsl.HTTPConfig{
			RequestTimeoutMs:  5000,
			TLS:               dsl.TLSConfig{MaxVersion: "1.2"},
			DefaultHeaderList: dsl.HeaderList{},
		},
		ErrorHandling: &dsl.ErrorHandlingConfig{
			ContinueOnError: dsl.ContinueOnErrorValue(false),
			MaxRetries:      0,
		},
		SOAP: &dsl.SOAPConfig{
			Version:       "1.2",
			FaultHandling: dsl.SOAPFaultHandlingConfig{FailOnFault: &failOnFaultFalse},
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				Timestamp: &dsl.SOAPTimestampConfig{TTLSeconds: 300},
			},
		},
		OverrideFields: []string{
			"config.http.request_timeout_ms",
			"http.tls.max_version",
			"http.default_header_list",
			"error_handling.continue_on_error",
			"error_handling.max_retries",
			"soap.version",
			"soap.fault_handling.fail_on_fault",
			"soap.ws_security.timestamp.ttl_seconds",
		},
	}
	if err := applyRuntimeOverrides(script, partial); err != nil {
		t.Fatalf("applyRuntimeOverrides partial: %v", err)
	}

	if script.Config.BaseURL != "https://soap.example.com" {
		t.Fatalf("base_url = %q, want untouched script value", script.Config.BaseURL)
	}
	if script.Config.HTTP.RequestTimeoutMs != 5000 {
		t.Fatalf("http.request_timeout_ms = %d, want 5000", script.Config.HTTP.RequestTimeoutMs)
	}
	if script.Config.HTTP.TLS.MinVersion != "1.2" || script.Config.HTTP.TLS.MaxVersion != "1.2" {
		t.Fatalf("http.tls = %#v, want min_version=1.2 preserved and max_version=1.2 overridden", script.Config.HTTP.TLS)
	}
	if script.Config.HTTP.DefaultHeaders["X-Keep"] != "true" {
		t.Fatalf("http.default_headers = %#v, want X-Keep preserved", script.Config.HTTP.DefaultHeaders)
	}
	if len(script.Config.HTTP.DefaultHeaderList) != 0 {
		t.Fatalf("http.default_header_list = %#v, want explicitly cleared empty slice", script.Config.HTTP.DefaultHeaderList)
	}
	if script.Config.ErrorHandling.ContinueOnErrorEnabled() {
		t.Fatalf("error_handling.continue_on_error = true, want explicit false")
	}
	if script.Config.ErrorHandling.MaxRetries != 0 {
		t.Fatalf("error_handling.max_retries = %d, want explicit 0", script.Config.ErrorHandling.MaxRetries)
	}
	if !script.Config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error_handling.snapshot_on_error = false, want preserved true")
	}
	if script.Config.SOAP.Version != "1.2" {
		t.Fatalf("soap.version = %q, want 1.2", script.Config.SOAP.Version)
	}
	if script.Config.SOAP.Namespaces["ns1"] != "http://example.com/ns1" {
		t.Fatalf("soap.namespaces = %#v, want ns1 preserved", script.Config.SOAP.Namespaces)
	}
	if script.Config.SOAP.WSSecurity == nil || script.Config.SOAP.WSSecurity.UsernameToken == nil ||
		script.Config.SOAP.WSSecurity.UsernameToken.Username != "script-user" {
		t.Fatalf("soap.ws_security.username_token = %#v, want script-user preserved", script.Config.SOAP.WSSecurity)
	}
	if script.Config.SOAP.WSSecurity.Timestamp == nil || script.Config.SOAP.WSSecurity.Timestamp.TTLSeconds != 300 {
		t.Fatalf("soap.ws_security.timestamp = %#v, want ttl_seconds=300", script.Config.SOAP.WSSecurity.Timestamp)
	}
	if script.Config.SOAP.FaultHandling.FailOnFault == nil || *script.Config.SOAP.FaultHandling.FailOnFault != false {
		t.Fatalf("soap.fault_handling.fail_on_fault = %#v, want explicit false", script.Config.SOAP.FaultHandling.FailOnFault)
	}

	// Legacy v1 whole-section replacement mode (len(OverrideFields) == 0):
	wholeSOAP := &dsl.SOAPConfig{
		Version:    "1.1",
		Namespaces: map[string]string{"ns2": "http://example.com/ns2"},
	}
	if err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{SOAP: wholeSOAP}); err != nil {
		t.Fatalf("applyRuntimeOverrides whole SOAP: %v", err)
	}
	wholeSOAP.Namespaces["ns2"] = "mutated"
	if script.Config.SOAP.Version != "1.1" || script.Config.SOAP.Namespaces["ns2"] != "http://example.com/ns2" || script.Config.SOAP.WSSecurity != nil {
		t.Fatalf("whole SOAP replacement = %#v, want deep-cloned replacement without WSSecurity", script.Config.SOAP)
	}

	// Unknown override_fields path is rejected.
	if err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{OverrideFields: []string{"http.not_a_real_field"}}); err == nil {
		t.Fatal("expected error for unknown override_fields path, got nil")
	}
}

func TestMergeRuntimeSettings_PartialOverrideFieldsPreservesBaseAndScriptSiblings(t *testing.T) {
	base := &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			TLS: dsl.TLSConfig{MinVersion: "1.3"},
		},
		SOAP: &dsl.SOAPConfig{
			Version: "1.2",
		},
		OverrideFields: []string{"http.tls.min_version", "soap.version"},
	}
	group := &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			RequestTimeoutMs: 4500,
		},
		SOAP: &dsl.SOAPConfig{
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				Timestamp: &dsl.SOAPTimestampConfig{TTLSeconds: 180},
			},
		},
		OverrideFields: []string{"http.request_timeout_ms", "soap.ws_security.timestamp.ttl_seconds"},
	}

	merged := mergeRuntimeSettings(base, group)
	if merged == nil {
		t.Fatal("merged = nil")
	}
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				RequestTimeoutMs:  30000,
				TLS:               dsl.TLSConfig{MinVersion: "1.2", MaxVersion: "1.3"},
				DefaultHeaderList: dsl.HeaderList{{Name: "X-Script", Value: "preserve-me"}},
			},
			SOAP: dsl.SOAPConfig{
				Version:    "1.1",
				Namespaces: map[string]string{"ns": "urn:script"},
			},
		},
	}
	if err := applyRuntimeOverrides(script, merged); err != nil {
		t.Fatalf("applyRuntimeOverrides(merged): %v", err)
	}
	if script.Config.HTTP.TLS.MinVersion != "1.3" {
		t.Fatalf("http.tls.min_version = %q, want base override 1.3", script.Config.HTTP.TLS.MinVersion)
	}
	if script.Config.HTTP.RequestTimeoutMs != 4500 {
		t.Fatalf("http.request_timeout_ms = %d, want group override 4500", script.Config.HTTP.RequestTimeoutMs)
	}
	if len(script.Config.HTTP.DefaultHeaderList) != 1 || script.Config.HTTP.DefaultHeaderList[0].Name != "X-Script" {
		t.Fatalf("http.default_header_list = %#v, want script header preserved", script.Config.HTTP.DefaultHeaderList)
	}
	if script.Config.SOAP.Version != "1.2" {
		t.Fatalf("soap.version = %q, want base override 1.2", script.Config.SOAP.Version)
	}
	if script.Config.SOAP.WSSecurity == nil || script.Config.SOAP.WSSecurity.Timestamp == nil || script.Config.SOAP.WSSecurity.Timestamp.TTLSeconds != 180 {
		t.Fatalf("soap.ws_security.timestamp = %#v, want group override 180", script.Config.SOAP.WSSecurity)
	}
	if script.Config.SOAP.Namespaces["ns"] != "urn:script" {
		t.Fatalf("soap.namespaces = %#v, want script namespace preserved", script.Config.SOAP.Namespaces)
	}
}

func TestRuntimeSettingsHelpers_HasAndCloneSOAPAndOverrideFields(t *testing.T) {
	if hasRuntimeSettingsOverrides(nil) || hasRuntimeSettingsOverrides(&RuntimeSettingsOverride{}) {
		t.Fatal("expected empty RuntimeSettingsOverride to report false")
	}
	orig := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			Version:    "1.2",
			Namespaces: map[string]string{"ns": "urn:test"},
		},
		OverrideFields: []string{"soap.version"},
	}
	if !hasRuntimeSettingsOverrides(orig) {
		t.Fatal("expected non-empty RuntimeSettingsOverride to report true")
	}
	cloned := cloneRuntimeSettingsOverride(orig)
	orig.SOAP.Namespaces["ns"] = "mutated"
	orig.OverrideFields[0] = "mutated"
	if cloned.SOAP.Namespaces["ns"] != "urn:test" {
		t.Fatalf("cloned SOAP.Namespaces = %#v, want urn:test", cloned.SOAP.Namespaces)
	}
	if cloned.OverrideFields[0] != "soap.version" {
		t.Fatalf("cloned OverrideFields = %#v, want soap.version", cloned.OverrideFields)
	}
}

func TestApplyRuntimeOverrides_PartialDoesNotMutateShallowCopiedScriptConfig(t *testing.T) {
	originalScript := &dsl.Script{
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				DefaultHeaders: map[string]string{"X-Orig": "orig"},
			},
			SOAP: dsl.SOAPConfig{
				Version:    "1.1",
				Namespaces: map[string]string{"ns": "urn:orig"},
				WSSecurity: &dsl.SOAPWSSecurityConfig{
					UsernameToken: &dsl.SOAPUsernameTokenConfig{
						Username: "orig-user",
						Password: "{{secret.orig}}",
					},
				},
			},
			BrowserEmulation: &dsl.BrowserEmulationConfig{
				Mode:            "custom",
				CustomUserAgent: "orig-ua",
			},
			Results: dsl.ResultsConfig{
				MessageDetail: &dsl.MessageDetailConfig{
					Mode:     "errors",
					MaxItems: 1024,
				},
			},
		},
	}

	// Simulate callers that do a shallow struct copy: scriptCopy := *originalScript
	scriptCopy := *originalScript
	override := &RuntimeSettingsOverride{
		HTTP: &dsl.HTTPConfig{
			DefaultHeaders: map[string]string{"X-New": "new"},
		},
		SOAP: &dsl.SOAPConfig{
			Namespaces: map[string]string{"ns": "urn:mutated"},
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: "new-user",
				},
			},
		},
		BrowserEmulation: &dsl.BrowserEmulationConfig{
			CustomUserAgent: "new-ua",
		},
		Results: &dsl.ResultsConfig{
			MessageDetail: &dsl.MessageDetailConfig{
				MaxItems: 4096,
			},
		},
		OverrideFields: []string{
			"http.default_headers.X-New",
			"soap.namespaces.ns",
			"soap.ws_security.username_token.username",
			"browser_emulation.custom_user_agent",
			"results.message_detail.max_items",
		},
	}
	if err := applyRuntimeOverrides(&scriptCopy, override); err != nil {
		t.Fatalf("applyRuntimeOverrides: %v", err)
	}

	if _, leaked := originalScript.Config.HTTP.DefaultHeaders["X-New"]; leaked {
		t.Fatalf("originalScript HTTP.DefaultHeaders mutated: %#v", originalScript.Config.HTTP.DefaultHeaders)
	}
	if originalScript.Config.SOAP.Namespaces["ns"] != "urn:orig" {
		t.Fatalf("originalScript SOAP.Namespaces mutated: %#v", originalScript.Config.SOAP.Namespaces)
	}
	if originalScript.Config.SOAP.WSSecurity.UsernameToken.Username != "orig-user" {
		t.Fatalf("originalScript SOAP.WSSecurity.UsernameToken.Username mutated: %q", originalScript.Config.SOAP.WSSecurity.UsernameToken.Username)
	}
	if originalScript.Config.BrowserEmulation.CustomUserAgent != "orig-ua" {
		t.Fatalf("originalScript BrowserEmulation.CustomUserAgent mutated: %q", originalScript.Config.BrowserEmulation.CustomUserAgent)
	}
	if originalScript.Config.Results.MessageDetail.MaxItems != 1024 {
		t.Fatalf("originalScript Results.MessageDetail.MaxItems mutated: %d", originalScript.Config.Results.MessageDetail.MaxItems)
	}
}

func TestMergeRuntimeSettings_PartialDoesNotLeakUnselectedBaseFieldsAndPreservesBaseEnv(t *testing.T) {
	base := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			Version:    "1.2",
			Namespaces: map[string]string{"unselected": "http://unselected.example"},
		},
		Env:            map[string]string{"A": "1"},
		OverrideFields: []string{"soap.version"},
	}
	group := &RuntimeSettingsOverride{
		HTTP:           &dsl.HTTPConfig{RequestTimeoutMs: 7500},
		Env:            map[string]string{"B": "2", "UNSELECTED": "ignore"},
		OverrideFields: []string{"http.request_timeout_ms", "env.B"},
	}
	merged := mergeRuntimeSettings(base, group)
	if merged == nil {
		t.Fatal("expected merged RuntimeSettingsOverride")
	}
	if merged.SOAP == nil || merged.SOAP.Version != "1.2" {
		t.Fatalf("merged.SOAP = %#v, want version 1.2", merged.SOAP)
	}
	if len(merged.SOAP.Namespaces) != 0 {
		t.Fatalf("merged.SOAP.Namespaces = %#v, want unselected base namespaces stripped", merged.SOAP.Namespaces)
	}
	if merged.Env["A"] != "1" || merged.Env["B"] != "2" {
		t.Fatalf("merged.Env = %#v, want A=1 and B=2", merged.Env)
	}
	if _, leaked := merged.Env["UNSELECTED"]; leaked {
		t.Fatalf("merged.Env leaked unselected group key: %#v", merged.Env)
	}

	script := &dsl.Script{
		Actions: []dsl.Action{{
			Name:   "fetch",
			Type:   "http_request",
			Method: "GET",
			Target: "https://example.test/{{env.A}}/{{env.B}}",
		}},
	}
	if err := applyRuntimeOverrides(script, merged); err != nil {
		t.Fatalf("applyRuntimeOverrides: %v", err)
	}
	if script.Actions[0].Target != "https://example.test/1/2" {
		t.Fatalf("target = %q, want https://example.test/1/2", script.Actions[0].Target)
	}

	// Single-layer partial merge (base == nil or group == nil) must also filter unselected fields/env keys.
	singleGroup := mergeRuntimeSettings(nil, group)
	if singleGroup == nil || singleGroup.HTTP == nil || singleGroup.HTTP.RequestTimeoutMs != 7500 {
		t.Fatalf("singleGroup.HTTP = %#v, want 7500", singleGroup)
	}
	if _, leaked := singleGroup.Env["UNSELECTED"]; leaked || singleGroup.Env["B"] != "2" {
		t.Fatalf("singleGroup.Env = %#v, want only B=2", singleGroup.Env)
	}
	singleBase := mergeRuntimeSettings(base, nil)
	if singleBase == nil || singleBase.SOAP == nil || len(singleBase.SOAP.Namespaces) != 0 {
		t.Fatalf("singleBase.SOAP = %#v, want unselected namespaces stripped", singleBase)
	}

	// setRuntimeSettingsIteration on a partial RuntimeSettingsOverride must apply to script.Config.RunLogic.Iterations.
	partialWithIter := setRuntimeSettingsIteration(&RuntimeSettingsOverride{
		HTTP:           &dsl.HTTPConfig{RequestTimeoutMs: 4200},
		OverrideFields: []string{"http.request_timeout_ms"},
	}, 9)
	iterScript := &dsl.Script{}
	if err := applyRuntimeOverrides(iterScript, partialWithIter); err != nil {
		t.Fatalf("applyRuntimeOverrides(partialWithIter): %v", err)
	}
	if iterScript.Config.RunLogic.Iterations != 9 || iterScript.Config.HTTP.RequestTimeoutMs != 4200 {
		t.Fatalf("iterScript.Config = %#v, want Iterations=9 and RequestTimeoutMs=4200", iterScript.Config)
	}

	// replaceRuntimeFields with compact keys ("log_level", "auto_transactions", "iterations", "browser")
	// on a partial-override target must reconcile OverrideFields without clobbering non-compact browser fields.
	headless := true
	compactSource := &RuntimeSettingsOverride{
		Log:           &dsl.LogConfig{Level: "debug", ExtendedOptions: []string{"parameter_substitution"}},
		RunLogic:      &dsl.RunLogicConfig{Iterations: 4, AutoTransactions: "per_action", WorkloadModel: "open"},
		BrowserConfig: &dsl.BrowserConfig{Headless: &headless, DeviceProfile: "pixel_8", SystemLanguage: "fr-FR"},
	}
	partialTarget := &RuntimeSettingsOverride{
		HTTP:           &dsl.HTTPConfig{RequestTimeoutMs: 3000},
		BrowserConfig:  &dsl.BrowserConfig{SystemLanguage: "en-US"},
		OverrideFields: []string{"http.request_timeout_ms", "browser.system_language"},
	}
	replacedCompact := replaceRuntimeFields(partialTarget, compactSource, []string{"log_level", "auto_transactions", "iterations", "browser"})
	targetScript := &dsl.Script{
		Config: dsl.ScriptConfig{
			Log:      dsl.LogConfig{Level: "error", ExtendedOptions: []string{"keep_me"}},
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "closed"},
			Browser:  dsl.BrowserConfig{Engine: "chromium", SystemLanguage: "de-DE"},
		},
	}
	if err := applyRuntimeOverrides(targetScript, replacedCompact); err != nil {
		t.Fatalf("applyRuntimeOverrides(replacedCompact): %v", err)
	}
	if targetScript.Config.Log.Level != "debug" || len(targetScript.Config.Log.ExtendedOptions) != 1 || targetScript.Config.Log.ExtendedOptions[0] != "keep_me" {
		t.Fatalf("targetScript.Config.Log = %#v, want level=debug and ExtendedOptions preserved", targetScript.Config.Log)
	}
	if targetScript.Config.RunLogic.Iterations != 4 || targetScript.Config.RunLogic.AutoTransactions != "per_action" || targetScript.Config.RunLogic.WorkloadModel != "closed" {
		t.Fatalf("targetScript.Config.RunLogic = %#v, want Iterations=4, AutoTransactions=per_action, WorkloadModel=closed", targetScript.Config.RunLogic)
	}
	if targetScript.Config.Browser.SystemLanguage != "en-US" || targetScript.Config.Browser.DeviceProfile != "pixel_8" || targetScript.Config.Browser.Engine != "chromium" {
		t.Fatalf("targetScript.Config.Browser = %#v, want SystemLanguage=en-US, DeviceProfile=pixel_8, Engine=chromium", targetScript.Config.Browser)
	}
}
