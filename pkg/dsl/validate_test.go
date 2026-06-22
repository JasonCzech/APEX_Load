package dsl

import (
	"strings"
	"testing"
)

func TestValidateScriptIssues_RendezvousValidation(t *testing.T) {
	script := &Script{
		Name:     "rendezvous-valid",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "sync-buyers",
			Type:   "rendezvous",
			Params: map[string]any{"count": 10, "timeout_ms": 5000},
		}, {
			Name:   "checkout",
			Type:   "request",
			Target: "/checkout",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions[0].Params = map[string]any{"count": 0, "timeout_ms": -1}
	issues = ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "sync-buyers": rendezvous count must be greater than zero`)
	assertIssue(t, issues, `actions[0] "sync-buyers": rendezvous timeout_ms must be zero or greater`)
}

func TestValidateScriptIssues_ErrorMessageValidation(t *testing.T) {
	script := &Script{
		Name:     "error-message-valid",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:  "Login Error",
			Type:  "error_message",
			Value: "Login failed for {{var.userid}}",
		}, {
			Name:   "checkout",
			Type:   "request",
			Target: "/checkout",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions[0].Value = ""
	script.Actions[0].Params = map[string]any{"format": "Login failed for {{var.userid}}"}
	issues = ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues with params.format = %v, want none", issues)
	}

	script.Actions[0].Params = nil
	issues = ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "Login Error": error_message requires value or params.format`)
}

func TestValidateScriptIssues_SourceEngineActionsDoNotRequireTarget(t *testing.T) {
	script := &Script{
		Name:     "source-engine-actions",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "helper",
			Type:   "custom_call",
			Params: map[string]any{"function": "my_helper"},
		}, {
			Name:   "timer",
			Type:   "source_timer",
			Params: map[string]any{"function": "lr_start_timer"},
		}},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestCanonicalActionTypeMapsLegacyTypes(t *testing.T) {
	cases := map[string]string{
		"loadrunner_transaction": "source_transaction",
		"loadrunner_message":     "source_message",
		"loadrunner_runtime":     "source_runtime",
		"loadrunner_parameter":   "source_parameter",
		"source_transaction":     "source_transaction",
		"http_sequence":          "http_sequence",
		"request":                "request",
		"":                       "",
	}
	for in, want := range cases {
		if got := CanonicalActionType(in); got != want {
			t.Errorf("CanonicalActionType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateScriptIssues_LegacyLoadRunnerActionTypesStillValidate(t *testing.T) {
	// A script persisted before the enterprise-archive rename carries
	// loadrunner_* engine action types; they must remain recognized engine
	// actions and not be rejected as unknown.
	script := &Script{
		Name:     "legacy-source-actions",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "txn",
			Type: "loadrunner_transaction",
		}, {
			Name: "timer",
			Type: "loadrunner_timer",
		}},
	}

	if !isEngineActionType("loadrunner_transaction") {
		t.Fatal("isEngineActionType should recognize legacy loadrunner_transaction")
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none for legacy loadrunner_* action types", issues)
	}
}

func TestValidateScriptIssues_BrowserRuntimeActionParity(t *testing.T) {
	script := &Script{
		Name:     "browser-runtime-actions",
		Protocol: "browser",
		Actions: []Action{
			{Name: "hover", Type: "browser_hover", Selector: "#menu"},
			{Name: "double click", Type: "browser_dblclick", Selector: "#quantity"},
			{Name: "drag", Type: "browser_drag_and_drop", Selector: "#source", Params: map[string]any{"target": "#target"}},
			{Name: "upload", Type: "browser_set_files", Selector: "#upload", Value: "/tmp/upload.txt"},
			{Name: "press enter", Type: "browser_press_key", Selector: "#search", Value: "Enter"},
			{Name: "evaluate", Type: "browser_evaluate_js", Target: "window.app.ready", Metadata: map[string]any{"review_required": true}},
			{Name: "sync click", Type: "browser_click", Selector: "#save", Sync: &BrowserSyncPolicy{Type: "network_idle", TimeoutMs: 5000}},
		},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions = []Action{{
		Name: "bad control",
		Type: "control_loop",
		Control: &ControlFlow{
			Count:         -1,
			MaxIterations: -2,
			Condition:     &Condition{Left: "{{var.count}}", Op: "roughly", Right: "5"},
			Steps:         []Action{{Name: "bad hover", Type: "browser_hover"}},
		},
	}}
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "bad control": control count must be zero or greater`)
	assertIssue(t, issues, `actions[0] "bad control": control max_iterations must be zero or greater`)
	assertIssue(t, issues, `actions[0] "bad control": condition op "roughly" is not supported`)
	assertIssue(t, issues, `actions[0].control.steps[0] "bad hover": missing target or selector`)
}

func TestValidateScriptIssues_ControlConditionCELExpr(t *testing.T) {
	script := &Script{
		Name:     "cel-control",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "branch",
			Type: "control_if",
			Control: &ControlFlow{
				Condition: &Condition{Expr: `var.role == "admin" && int(var.count) >= 2`},
				Steps:     []Action{{Name: "home", Type: "GET", Target: "/"}},
			},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions[0].Control.Condition = &Condition{Expr: `var.role ==`}
	issues := ValidateScriptIssues(script)
	assertIssueContains(t, issues, `actions[0] "branch": condition expr is invalid`)

	script.Actions[0].Control.Condition = &Condition{Expr: `var.role`}
	issues = ValidateScriptIssues(script)
	assertIssueContains(t, issues, `actions[0] "branch": condition expr is invalid`)
	assertIssueContains(t, issues, `expression must return bool`)
}

func TestValidateScriptIssues_ControlFlowLifecyclePaths(t *testing.T) {
	script := &Script{
		Name:     "browser-lifecycle-control",
		Protocol: "browser",
		Init: []Action{{
			Name: "init branch",
			Type: "control_if",
			Control: &ControlFlow{
				Condition: &Condition{Left: "{{var.phase}}", Op: "exists"},
				Steps: []Action{{
					Name: "bad init hover",
					Type: "browser_hover",
				}},
			},
		}},
		Actions: []Action{{
			Name:   "open",
			Type:   "browser_navigate",
			Target: "/",
		}},
		Teardown: []Action{{
			Name: "teardown loop",
			Type: "control_loop",
			Control: &ControlFlow{
				Count: -1,
				Steps: []Action{{
					Name: "bad teardown click",
					Type: "browser_click",
				}},
			},
		}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `init[0].control.steps[0] "bad init hover": missing target or selector`)
	assertIssue(t, issues, `teardown[0] "teardown loop": control count must be zero or greater`)
	assertIssue(t, issues, `teardown[0].control.steps[0] "bad teardown click": missing target or selector`)
}

func TestValidateScriptIssues_RunLogicGlobalGoals(t *testing.T) {
	script := &Script{
		Name:     "invalid-global-goals",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL:  "https://example.test",
			RunLogic: RunLogicConfig{GlobalIterations: -1, MaxTransactions: -1},
		},
		Actions: []Action{{Name: "home", Type: "request", Target: "/"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, "config.run_logic.global_iterations must be zero or greater")
	assertIssue(t, issues, "config.run_logic.max_transactions must be zero or greater")
}

func TestValidateScriptIssues_BodyFileRequiresLocalFileLoad(t *testing.T) {
	script := &Script{
		Name:     "body-file-upload",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:     "create",
			Type:     "POST",
			Target:   "/orders",
			BodyFile: "payloads/order.json",
		}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "create": body_file is only supported when loading scripts from a local file; inline body is required for API uploads`)

	script.Actions[0].Body = "{}"
	issues = ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "create": body and body_file cannot both be set`)
}

func TestValidateScriptIssues_SOAPValidation(t *testing.T) {
	script := &Script{
		Name:     "soap-invalid",
		Protocol: "soap",
		Config: ScriptConfig{
			BaseURL: "https://soap.example.com",
			SOAP: SOAPConfig{
				Version: "1.3",
				WSSecurity: &SOAPWSSecurityConfig{
					UsernameToken: &SOAPUsernameTokenConfig{PasswordType: "binary"},
					Timestamp:     &SOAPTimestampConfig{TTLSeconds: -1},
				},
			},
		},
		Actions: []Action{{
			Name:   "login",
			Type:   "request",
			Target: "/AuthService",
			SOAP:   &SOAPActionConfig{BodyMode: "templated"},
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.soap.version must be 1.1 or 1.2")
	assertIssue(t, issues, "config.soap.ws_security.username_token.username is required")
	assertIssue(t, issues, "config.soap.ws_security.username_token.password is required")
	assertIssue(t, issues, "config.soap.ws_security.username_token.password_type must be text or digest")
	assertIssue(t, issues, "config.soap.ws_security.timestamp.ttl_seconds must be zero or greater")
	assertIssue(t, issues, "actions[0] \"login\": soap.body_mode must be raw_xml or generated")
}

func TestValidateScriptIssues_SOAPValidationAllowsSupportedFields(t *testing.T) {
	script := &Script{
		Name:     "soap-valid",
		Protocol: "soap",
		Config: ScriptConfig{
			BaseURL: "https://soap.example.com",
			SOAP: SOAPConfig{
				Version: "1.1",
				WSSecurity: &SOAPWSSecurityConfig{
					UsernameToken: &SOAPUsernameTokenConfig{
						Username:     "svc-user",
						Password:     "svc-pass",
						PasswordType: "digest",
					},
					Timestamp: &SOAPTimestampConfig{TTLSeconds: 60},
				},
			},
		},
		Actions: []Action{{
			Name:   "login",
			Type:   "request",
			Target: "/AuthService",
			SOAP:   &SOAPActionConfig{BodyMode: "generated"},
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_WebRuntimeValidation(t *testing.T) {
	script := &Script{
		Name:     "web-invalid",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			RunLogic: RunLogicConfig{
				BlockScheduling: "weighted",
				ActionSequence:  []string{"init", "middle", "teardown"},
			},
			Log: LogConfig{
				ExtendedOptions: []string{"advanced_trace", "wiretap"},
			},
			HTTP: HTTPConfig{
				ProxyMode: "corp",
			},
			Browser: BrowserConfig{
				DownloadResources: "css_only",
			},
			WebRuntime: WebRuntimeConfig{
				AdditionalAttributes: map[string]string{"": "bad"},
				DownloadFilters: DownloadFiltersConfig{
					Mode: "allow_list",
				},
				ContentChecks: []ContentCheckConfig{{
					Text:  "",
					Scope: "global",
				}},
				NetworkSimulation: NetworkSimulationConfig{
					Mode:          "preset",
					Preset:        "dialup",
					LatencyMs:     -1,
					JitterMs:      -5,
					PacketLossPct: 120,
				},
				Streaming: StreamingConfig{
					Mode:              "fixed",
					BufferSizeSeconds: -1,
				},
				DataFormatting: DataFormattingConfig{
					Chain: []DFETransform{{Type: ""}, {Type: "custom_dfe"}},
				},
			},
			Observability: ObservabilityConfig{
				ExternalMetrics: []ExternalMetricConfig{{
					URL:        "ftp://metrics.example.test/metrics",
					Format:     "json",
					MetricName: "",
				}},
			},
		},
		Actions: []Action{{
			Name:   "home",
			Type:   "request",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.run_logic.block_scheduling must be sequential, random, or percentage")
	assertIssue(t, issues, "config.run_logic.action_sequence[1] must be init, action, teardown, vuser_init, or vuser_end")
	assertIssue(t, issues, "config.log.extended_options[1] must be parameter_substitution, data_returned, or advanced_trace")
	assertIssue(t, issues, "config.http.proxy_mode must be none, system, or custom")
	assertIssue(t, issues, "config.browser.download_resources must be none, recorded, or all")
	assertIssue(t, issues, "config.web_runtime.additional_attributes keys must be non-empty")
	assertIssue(t, issues, "config.web_runtime.download_filters.hosts is required when download filter mode is allow_list or block_list")
	assertIssue(t, issues, "config.web_runtime.content_checks[0].text is required")
	assertIssue(t, issues, "config.web_runtime.content_checks[0].scope must be any_response, main_document, or resource")
	assertIssue(t, issues, "config.web_runtime.network_simulation.preset must be 56k, dsl, t1, or 10mbps when mode is preset")
	assertIssue(t, issues, "config.web_runtime.network_simulation.latency_ms must be zero or greater")
	assertIssue(t, issues, "config.web_runtime.network_simulation.jitter_ms must be zero or greater")
	assertIssue(t, issues, "config.web_runtime.network_simulation.packet_loss_pct must be between 0 and 100")
	assertIssue(t, issues, "config.web_runtime.streaming.buffer_size_seconds must be zero or greater")
	assertIssue(t, issues, "config.web_runtime.streaming.fixed_bitrate_kbps must be greater than zero when mode is fixed")
	assertIssue(t, issues, "config.observability.external_metrics[0].name is required")
	assertIssue(t, issues, "config.observability.external_metrics[0].url must be a valid http or https URL")
	assertIssue(t, issues, "config.observability.external_metrics[0].format must be prometheus")
	assertIssue(t, issues, "config.observability.external_metrics[0].metric_name is required")
	assertIssue(t, issues, "config.web_runtime.data_formatting.chain[0].type is required")
	assertIssue(t, issues, "config.web_runtime.data_formatting.chain[1].type \"custom_dfe\" is not supported")
}

func TestValidateScriptIssues_WebRuntimeValidationAllowsSupportedFields(t *testing.T) {
	customProxy := "http://proxy.example.test:8080"
	failIfFound := true
	cacheEnabled := true
	cacheContentURLs := true
	checkNewerVersions := true
	newUserPerIter := true

	script := &Script{
		Name:     "web-valid",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			RunLogic: RunLogicConfig{
				Iterations:       10,
				AutoTransactions: "per_action",
				BlockScheduling:  "sequential",
				ActionSequence:   []string{"init", "action", "teardown"},
			},
			Log: LogConfig{
				Level:           "extended",
				OnErrorOnly:     true,
				ExtendedOptions: []string{"parameter_substitution", "advanced_trace"},
			},
			HTTP: HTTPConfig{
				ProxyMode: "custom",
				Proxy: ProxyConfig{
					URL: customProxy,
				},
			},
			Browser: BrowserConfig{
				CacheEnabled:       &cacheEnabled,
				CacheContentURLs:   &cacheContentURLs,
				CheckNewerVersions: &checkNewerVersions,
				NewUserPerIter:     &newUserPerIter,
				DownloadResources:  "recorded",
			},
			WebRuntime: WebRuntimeConfig{
				AdditionalAttributes: map[string]string{"env": "perf", "region": "east"},
				DownloadFilters: DownloadFiltersConfig{
					Mode:  "block_list",
					Hosts: []string{"metrics.example.test", "cdn.example.test"},
				},
				ContentChecks: []ContentCheckConfig{{
					Text:        "Access Denied",
					Scope:       "any_response",
					FailIfFound: &failIfFound,
				}},
				NetworkSimulation: NetworkSimulationConfig{
					Mode:          "custom",
					BandwidthKbps: 2048,
					LatencyMs:     75,
					JitterMs:      12,
					PacketLossPct: 0.5,
				},
				Streaming: StreamingConfig{
					Mode:                "adaptive",
					BufferSizeSeconds:   30,
					BandwidthEstimation: "rolling",
				},
				DataFormatting: DataFormattingConfig{
					AutoDetect: true,
					Chain: []DFETransform{
						{Type: "base64"},
						{Type: "gzip"},
						{Type: "json_to_xml"},
						{Type: "xml_to_json"},
						{Type: "url_decode"},
						{Type: "url_encode"},
						{Type: "html_unescape"},
						{Type: "html_escape"},
					},
				},
			},
		},
		Actions: []Action{{
			Name:   "home",
			Type:   "request",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_WebRuntimeSourceIPs(t *testing.T) {
	makeScript := func(ips []string) *Script {
		return &Script{
			Name:     "spoof",
			Protocol: "http",
			Config: ScriptConfig{
				BaseURL:    "https://example.test",
				WebRuntime: WebRuntimeConfig{SourceIPs: ips},
			},
			Actions: []Action{{Name: "home", Type: "request", Target: "/"}},
		}
	}
	hasIssue := func(issues []string, substr string) bool {
		for _, issue := range issues {
			if strings.Contains(issue, substr) {
				return true
			}
		}
		return false
	}

	t.Run("valid IPv4 and IPv6 pass", func(t *testing.T) {
		issues := ValidateScriptIssues(makeScript([]string{"10.0.0.1", "192.168.1.5", "::1", "fe80::1"}))
		if hasIssue(issues, "source_ips") {
			t.Fatalf("unexpected source_ips issue: %v", issues)
		}
	})

	t.Run("invalid IPs rejected", func(t *testing.T) {
		issues := ValidateScriptIssues(makeScript([]string{"10.0.0.1", "10.0.0.300", "not-an-ip"}))
		if !hasIssue(issues, "source_ips[1]") || !hasIssue(issues, "source_ips[2]") {
			t.Fatalf("expected invalid source_ips issues, got %v", issues)
		}
		if hasIssue(issues, "source_ips[0]") {
			t.Fatalf("valid first IP should not be flagged, got %v", issues)
		}
	})

	t.Run("blank entry rejected", func(t *testing.T) {
		issues := ValidateScriptIssues(makeScript([]string{"   "}))
		if !hasIssue(issues, "source_ips[0] must not be empty") {
			t.Fatalf("expected blank source_ips issue, got %v", issues)
		}
	})
}

func TestValidateScriptIssues_WebRuntimeCustomNetworkSimulationAllowsLossOnly(t *testing.T) {
	script := &Script{
		Name:     "web-loss-only",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			WebRuntime: WebRuntimeConfig{
				NetworkSimulation: NetworkSimulationConfig{
					Mode:          "custom",
					PacketLossPct: 5,
				},
			},
		},
		Actions: []Action{{
			Name:   "home",
			Type:   "request",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_BrowserValidation(t *testing.T) {
	script := &Script{
		Name:     "browser-invalid",
		Protocol: "browser",
		Config: ScriptConfig{
			Browser: BrowserConfig{
				Engine:        "gecko",
				DeviceProfile: "unknown_phone",
				Orientation:   "diagonal",
				Viewport: BrowserViewport{
					Width:  -1,
					Height: -2,
				},
			},
		},
		Actions: []Action{
			{
				Name:   "bad type",
				Type:   "browser_scroll",
				Target: "#app",
			},
			{
				Name:   "clear fill",
				Type:   "browser_fill",
				Target: "#name",
			},
			{
				Name:   "missing select value",
				Type:   "browser_select",
				Target: "#country",
			},
			{
				Name:   "bad swipe",
				Type:   "browser_swipe",
				Target: "#carousel",
				Params: map[string]any{
					"direction": "northwest",
					"distance":  0,
				},
			},
			{
				Name:   "bad long press",
				Type:   "browser_long_press",
				Target: "#hold",
				Params: map[string]any{
					"duration_ms": 0,
				},
			},
			{
				Name:   "missing upload file",
				Type:   "browser_set_files",
				Target: "#upload",
			},
			{
				Name: "missing key",
				Type: "browser_press_key",
			},
		},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.browser.engine must be chromium or webkit")
	assertIssue(t, issues, `config.browser.device_profile "unknown_phone" is not recognized`)
	assertIssue(t, issues, "config.browser.orientation must be portrait or landscape")
	assertIssue(t, issues, "config.browser.viewport.width must be zero or greater")
	assertIssue(t, issues, "config.browser.viewport.height must be zero or greater")
	assertIssue(t, issues, `actions[0] "bad type": unsupported browser action type "browser_scroll"`)
	assertIssue(t, issues, `actions[2] "missing select value": browser_select requires value`)
	assertIssue(t, issues, `actions[3] "bad swipe": browser_swipe direction must be left, right, up, or down`)
	assertIssue(t, issues, `actions[3] "bad swipe": browser_swipe distance must be greater than zero`)
	assertIssue(t, issues, `actions[4] "bad long press": browser_long_press duration_ms must be greater than zero`)
	assertIssue(t, issues, `actions[5] "missing upload file": browser_set_files requires value, params.file, params.path, or params.files`)
	assertIssue(t, issues, `actions[6] "missing key": browser_press_key requires value, params.key, or params.key_name`)
}

func TestValidateScriptIssues_WebSocketValidation(t *testing.T) {
	script := &Script{
		Name:     "websocket-invalid",
		Protocol: "websocket",
		Config: ScriptConfig{
			BaseURL: "wss://example.test",
			WebSocket: WebSocketConfig{
				ReadTimeoutMs:  -1,
				WriteTimeoutMs: -2,
				MaxMessageSize: -3,
			},
		},
		Actions: []Action{{
			Name: "missing target",
			Type: "ws_connect",
		}, {
			Name: "missing body",
			Type: "ws_send",
		}, {
			Name:   "bad type",
			Type:   "ws_send",
			Body:   "hello",
			Params: map[string]any{"type": "stream"},
		}, {
			Name:   "bad timeout",
			Type:   "ws_receive",
			Params: map[string]any{"timeout_ms": -1},
		}, {
			Name: "unsupported",
			Type: "ws_subscribe",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.websocket.read_timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.websocket.write_timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.websocket.max_message_size must be zero or greater")
	assertIssue(t, issues, `actions[0] "missing target": missing target`)
	assertIssue(t, issues, `actions[1] "missing body": ws_send requires body`)
	assertIssue(t, issues, `actions[2] "bad type": ws_send type must be text or binary`)
	assertIssue(t, issues, `actions[3] "bad timeout": ws_receive timeout_ms must be zero or greater`)
	assertIssue(t, issues, `actions[4] "unsupported": unsupported websocket action type "ws_subscribe"`)
}

func TestValidateScriptIssues_WebSocketValidationAllowsSupportedFields(t *testing.T) {
	script := &Script{
		Name:     "websocket-valid",
		Protocol: "websocket",
		Config: ScriptConfig{
			BaseURL: "wss://example.test",
			WebSocket: WebSocketConfig{
				ReadTimeoutMs:  5000,
				WriteTimeoutMs: 2500,
				MaxMessageSize: 65536,
				Subprotocol:    "graphql-ws",
				Origin:         "https://app.example.test",
				Compression:    true,
			},
		},
		Actions: []Action{{
			Name:   "connect",
			Type:   "ws_connect",
			Target: "/ws",
		}, {
			Name:   "send",
			Type:   "ws_send",
			Body:   `{"type":"ping"}`,
			Params: map[string]any{"type": "text"},
		}, {
			Name:   "receive",
			Type:   "ws_receive",
			Params: map[string]any{"timeout_ms": 1000},
		}, {
			Name: "ping",
			Type: "ws_ping",
		}, {
			Name: "close",
			Type: "ws_close",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_GRPCValidation(t *testing.T) {
	script := &Script{
		Name:     "grpc-invalid",
		Protocol: "grpc",
		Config: ScriptConfig{
			BaseURL: "grpc.example.test:443",
			GRPC: GRPCConfig{
				ProtoFiles:     []string{""},
				MaxRecvMsgSize: -1,
				TimeoutMs:      -2,
			},
		},
		Actions: []Action{{
			Name: "missing target",
			Type: "grpc_unary",
		}, {
			Name:   "bad target",
			Type:   "grpc_unary",
			Target: "apexload.v1.GeneratorControl",
		}, {
			Name:   "bad timeout",
			Type:   "grpc_unary",
			Target: "apexload.v1.GeneratorControl/DistributeScript",
			Params: map[string]any{"timeout_ms": -1},
		}, {
			Name:   "streaming",
			Type:   "grpc_server_stream",
			Target: "apexload.v1.GeneratorControl/StreamMetrics",
			Params: map[string]any{"max_messages": -1},
		}, {
			Name: "unsupported",
			Type: "grpc_batch",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.grpc.max_recv_msg_size must be zero or greater")
	assertIssue(t, issues, "config.grpc.timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.grpc.proto_files[0] must be non-empty")
	assertIssue(t, issues, `actions[0] "missing target": missing target`)
	assertIssue(t, issues, `actions[1] "bad target": grpc_unary target must be service/method`)
	assertIssue(t, issues, `actions[2] "bad timeout": grpc_unary timeout_ms must be zero or greater`)
	assertIssue(t, issues, `actions[3] "streaming": grpc_server_stream max_messages must be zero or greater`)
	assertIssue(t, issues, `actions[4] "unsupported": unsupported grpc action type "grpc_batch"`)
}

func TestValidateScriptIssues_GRPCValidationAllowsUnaryReflection(t *testing.T) {
	script := &Script{
		Name:     "grpc-valid",
		Protocol: "grpc",
		Config: ScriptConfig{
			BaseURL: "grpc.example.test:443",
			GRPC: GRPCConfig{
				UseReflection:  true,
				PlainText:      true,
				MaxRecvMsgSize: 1048576,
				TimeoutMs:      5000,
				Metadata:       map[string]string{"x-env": "perf"},
				Authority:      "orders.example.test",
			},
		},
		Actions: []Action{{
			Name:   "cache",
			Type:   "grpc_unary",
			Target: "apexload.v1.GeneratorControl/DistributeScript",
			Body:   `{"scriptId":"script-1"}`,
			Params: map[string]any{"timeout_ms": 1000},
		}, {
			Name:   "heartbeat",
			Type:   "grpc_bidi_stream",
			Target: "apexload.v1.GeneratorControl/Heartbeat",
			Params: map[string]any{"max_messages": 1, "messages": []any{map[string]any{"generatorId": "gen-1"}}},
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_BrowserValidationAllowsSupportedFields(t *testing.T) {
	touchMode := true
	clientMetrics := true
	script := &Script{
		Name:     "browser-valid",
		Protocol: "browser",
		Config: ScriptConfig{
			Browser: BrowserConfig{
				Engine:        "webkit",
				DeviceProfile: "iphone_se",
				TouchMode:     &touchMode,
				Orientation:   "portrait",
				ClientMetrics: &clientMetrics,
				Viewport: BrowserViewport{
					Width:  375,
					Height: 667,
				},
			},
		},
		Actions: []Action{
			{
				Name:   "open",
				Type:   "browser_navigate",
				Target: "/",
			},
			{
				Name:   "tap",
				Type:   "browser_tap",
				Target: "#menu",
			},
			{
				Name:   "swipe",
				Type:   "browser_swipe",
				Target: "#carousel",
				Params: map[string]any{
					"direction": "left",
					"distance":  180,
				},
			},
			{
				Name:   "press",
				Type:   "browser_long_press",
				Target: "#hold",
				Params: map[string]any{
					"duration_ms": 350,
				},
			},
			{
				Name:   "clear fill",
				Type:   "browser_fill",
				Target: "#query",
				Value:  "",
			},
			{
				Name:   "select",
				Type:   "browser_select",
				Target: "#country",
				Value:  "US",
			},
		},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_BrowserValidationRejectsMismatchedDeviceProfileEngine(t *testing.T) {
	script := &Script{
		Name:     "browser-mismatch",
		Protocol: "browser",
		Config: ScriptConfig{
			Browser: BrowserConfig{
				Engine:        "chromium",
				DeviceProfile: "iphone_se",
			},
		},
		Actions: []Action{{
			Name:   "open",
			Type:   "browser_navigate",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, `config.browser.engine "chromium" is incompatible with device_profile "iphone_se" (requires webkit)`)
}

func TestValidateScriptIssues_DatabaseProtocolRequiresConfigAndSQL(t *testing.T) {
	script := &Script{
		Name:     "DB",
		Protocol: "database",
		Config: ScriptConfig{
			Database: DatabaseConfig{Driver: "sqlite", DSN: ":memory:", MaxRows: -1},
		},
		Actions: []Action{{Name: "Query", Type: "db_query"}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, `actions[0] "Query": database action requires target or body SQL`)
	assertIssue(t, issues, "config.database.max_rows must be zero or greater")
	for _, issue := range issues {
		if issue == "config.base_url is empty" {
			t.Fatalf("issues = %v, database should not require base_url", issues)
		}
	}
}

func TestValidateScriptIssues_DatabaseProtocolAllowsTransactionActionsWithoutTarget(t *testing.T) {
	script := &Script{
		Name:     "DB",
		Protocol: "database",
		Config:   ScriptConfig{Database: DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}},
		Actions:  []Action{{Name: "Begin", Type: "db_begin"}, {Name: "Query", Type: "db_query", Target: "select 1"}},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %#v, want none", issues)
	}
}

func TestValidateScriptIssues_NetworkProtocolValidatesBundleActions(t *testing.T) {
	script := &Script{
		Name:     "Network",
		Protocol: "network",
		Config: ScriptConfig{
			Network: NetworkConfig{Subprotocol: "smtp", Port: -1, TimeoutMs: -1, To: []string{""}},
		},
		Actions: []Action{
			{Name: "Lookup", Type: "dns_query", Params: map[string]any{"record_type": "SRV"}},
			{Name: "Socket", Type: "tcp_request", Params: map[string]any{"read_bytes": -1}},
			{Name: "Directory", Type: "ldap_search", Params: map[string]any{"max_entries": -1}},
		},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, `actions[0] "Lookup": dns action requires target host`)
	assertIssue(t, issues, `actions[0] "Lookup": dns record_type must be A, AAAA, CNAME, MX, NS, or TXT`)
	assertIssue(t, issues, `actions[1] "Socket": socket action requires target address`)
	assertIssue(t, issues, `actions[1] "Socket": read_bytes must be zero or greater`)
	assertIssue(t, issues, `actions[2] "Directory": ldap_search requires target base DN`)
	assertIssue(t, issues, `actions[2] "Directory": max_entries must be zero or greater`)
	assertIssue(t, issues, "config.network.port must be zero or greater")
	assertIssue(t, issues, "config.network.timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.network.to[0] must be non-empty")
	for _, issue := range issues {
		if issue == "config.base_url is empty" {
			t.Fatalf("issues = %v, network should not require base_url", issues)
		}
	}
}

func TestValidateScriptIssues_NetworkProtocolAllowsSupportedActions(t *testing.T) {
	script := &Script{
		Name:     "Network",
		Protocol: "network",
		Config:   ScriptConfig{Network: NetworkConfig{Subprotocol: "dns", TimeoutMs: 1000}},
		Actions: []Action{
			{Name: "Lookup", Type: "dns_query", Target: "localhost", Params: map[string]any{"record_type": "A"}},
			{Name: "Socket", Type: "tcp_request", Target: "127.0.0.1:7", Body: "ping", Params: map[string]any{"read_bytes": 4}},
			{Name: "Bind", Type: "ldap_bind"},
		},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %#v, want none", issues)
	}
}

func assertIssue(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if issue == want {
			return
		}
	}
	t.Fatalf("issues = %v, want %q", issues, want)
}

func assertIssueContains(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return
		}
	}
	t.Fatalf("issues = %v, want issue containing %q", issues, want)
}

func TestValidateExtractorIssuesInSequenceChildren(t *testing.T) {
	script := &Script{
		Name:     "seq-child-extractors",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "seq",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Target: "/a",
			Params: map[string]any{"requests": []any{
				map[string]any{
					"name": "child", "type": "GET", "method": "GET", "target": "/a",
					"extractors": []any{map[string]any{
						"name": "tok", "source": "body", "type": "boundary", "expression": "a|||b",
						"on_miss": "error", "mode": "ALL", "convert": "url-encode",
					}},
				},
			}},
		}},
	}

	issues := ValidateScriptIssues(script)
	var onMiss, mode, convert bool
	for _, issue := range issues {
		if strings.Contains(issue, "on_miss") {
			onMiss = true
		}
		if strings.Contains(issue, "mode must be") {
			mode = true
		}
		if strings.Contains(issue, "convert must be") {
			convert = true
		}
	}
	if !onMiss || !mode || !convert {
		t.Errorf("sequence-child extractor typos must fail validation; issues = %v", issues)
	}
}

func TestValidateScriptIssues_SequenceChildBodyFile(t *testing.T) {
	newScript := func(child map[string]any) *Script {
		return &Script{
			Name:     "seq-child-body-file",
			Protocol: "http",
			Config:   ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{
				Name:   "seq",
				Type:   "http_sequence",
				Method: "SEQUENCE",
				Target: "/a",
				Params: map[string]any{"requests": []any{child}},
			}},
		}
	}

	tests := []struct {
		name  string
		child map[string]any
		want  string
	}{
		{
			name:  "body_file alone is rejected for API uploads",
			child: map[string]any{"name": "create", "type": "POST", "method": "POST", "target": "/orders", "body_file": "payloads/order.json"},
			want:  `actions[0] "seq".params.requests[0] "create": body_file is only supported when loading scripts from a local file; inline body is required for API uploads`,
		},
		{
			name:  "body and body_file conflict",
			child: map[string]any{"name": "create", "type": "POST", "method": "POST", "target": "/orders", "body": "{}", "body_file": "payloads/order.json"},
			want:  `actions[0] "seq".params.requests[0] "create": body and body_file cannot both be set`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateScriptIssues(newScript(tc.child))
			assertIssue(t, issues, tc.want)
		})
	}
}

func TestValidateScriptIssues_SequenceChildWithoutBodyFileValid(t *testing.T) {
	script := &Script{
		Name:     "seq-child-ok",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "seq",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Target: "/a",
			Params: map[string]any{"requests": []any{
				map[string]any{"name": "create", "type": "POST", "method": "POST", "target": "/orders", "body": "{}"},
			}},
		}},
	}
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "body_file") {
			t.Fatalf("unexpected body_file issue for inline-body child: %q", issue)
		}
	}
}

func TestValidateExtractorIssuesAcceptsNewEnums(t *testing.T) {
	script := &Script{
		Name:     "extractor-enums",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "a", Type: "GET", Method: "GET", Target: "/a",
			Extractors: []Extractor{
				{Name: "x", Source: "body", Type: "boundary", Expression: "a|||b", OnMiss: "empty", Convert: "html_url_encode"},
				{Name: "y", Source: "body", Type: "regex", Expression: "v=(.+?);", Mode: "all", OnMiss: "fail"},
			},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) > 0 {
		t.Errorf("valid enum values rejected: %v", issues)
	}
}

// TestValidateScriptIssues_NestedJMSHonorsConfigDestination locks in that a JMS
// action nested inside a control block is NOT falsely flagged "missing target"
// when config.jms.destination supplies the destination.
func TestValidateScriptIssues_NestedJMSHonorsConfigDestination(t *testing.T) {
	script := &Script{
		Name:     "jms-control",
		Protocol: "jms-solace",
		Config:   ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "orders.in", Mock: true}},
		Actions: []Action{{
			Name: "loop",
			Type: "control_loop",
			Control: &ControlFlow{
				Count: 1,
				Steps: []Action{{Name: "pub", Type: "jms_publish"}},
			},
		}},
	}
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "missing target") {
			t.Fatalf("nested jms_publish with config destination flagged missing target: %v", ValidateScriptIssues(script))
		}
	}
}
