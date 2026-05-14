package dsl

import "testing"

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
					Mode:      "preset",
					Preset:    "dialup",
					LatencyMs: -1,
				},
				Streaming: StreamingConfig{
					Mode:              "fixed",
					BufferSizeSeconds: -1,
				},
				DataFormatting: DataFormattingConfig{
					Chain: []DFETransform{{Type: ""}},
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
	assertIssue(t, issues, "config.web_runtime.streaming.buffer_size_seconds must be zero or greater")
	assertIssue(t, issues, "config.web_runtime.streaming.fixed_bitrate_kbps must be greater than zero when mode is fixed")
	assertIssue(t, issues, "config.web_runtime.data_formatting.chain[0].type is required")
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
				},
				Streaming: StreamingConfig{
					Mode:                "adaptive",
					BufferSizeSeconds:   30,
					BandwidthEstimation: "rolling",
				},
				DataFormatting: DataFormattingConfig{
					AutoDetect: true,
					Chain: []DFETransform{{
						Type: "base64",
					}},
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

func assertIssue(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if issue == want {
			return
		}
	}
	t.Fatalf("issues = %v, want %q", issues, want)
}
