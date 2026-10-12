package dsl

import (
	"sort"
	"strings"
	"testing"
)

// TestActionCatalogEntriesAreWellFormed asserts every catalog entry carries
// the metadata the schema generator, docs, and editor depend on.
func TestActionCatalogEntriesAreWellFormed(t *testing.T) {
	specs := AllActionSpecs()
	if len(specs) == 0 {
		t.Fatal("action catalog is empty")
	}
	for _, spec := range specs {
		if strings.TrimSpace(spec.Type) == "" {
			t.Errorf("catalog entry with empty type: %+v", spec)
		}
		if strings.TrimSpace(spec.Summary) == "" {
			t.Errorf("%s: missing summary", spec.Type)
		}
		if len(spec.Protocols) == 0 {
			t.Errorf("%s: missing protocols", spec.Type)
		}
		declared := make(map[string]bool, len(spec.Fields))
		for _, field := range spec.Fields {
			if strings.TrimSpace(field.Name) == "" {
				t.Errorf("%s: field with empty name", spec.Type)
			}
			if field.Kind == "" {
				t.Errorf("%s.%s: missing field kind", spec.Type, field.Name)
			}
			if field.Kind == FieldKindEnum && len(field.Enum) == 0 {
				t.Errorf("%s.%s: enum field without values", spec.Type, field.Name)
			}
			if field.Kind != FieldKindEnum && len(field.Enum) > 0 {
				t.Errorf("%s.%s: enum values on non-enum kind %s", spec.Type, field.Name, field.Kind)
			}
			if field.Kind != FieldKindEnum && (field.SchemaEnum || len(field.EnumAliases) > 0 || field.EnumCaseFold || field.EnumTrimSpace || field.EnumAllowEmpty || field.EnumAllowBlank) {
				t.Errorf("%s.%s: enum schema metadata on non-enum kind %s", spec.Type, field.Name, field.Kind)
			}
			if !field.SchemaEnum && (len(field.EnumAliases) > 0 || field.EnumCaseFold || field.EnumTrimSpace || field.EnumAllowEmpty || field.EnumAllowBlank) {
				t.Errorf("%s.%s: enum normalization metadata requires schema_enum", spec.Type, field.Name)
			}
			if field.EnumAllowBlank && !field.EnumTrimSpace {
				t.Errorf("%s.%s: enum_allow_blank requires enum_trim_space", spec.Type, field.Name)
			}
			declared[field.Name] = true
		}
		for _, group := range spec.RequireOneOf {
			if len(group) < 2 {
				t.Errorf("%s: require_one_of group %v needs at least two alternatives", spec.Type, group)
			}
			for _, field := range group {
				// params.keyName is a tolerated camel-case spelling the
				// validator accepts but the catalog documents only in
				// snake_case form.
				if !declared[field] && field != "params.keyName" {
					t.Errorf("%s: require_one_of references undeclared field %q", spec.Type, field)
				}
			}
		}
	}
}

// TestActionCatalogHasNoDuplicateTypesOrAliases asserts canonical types and
// aliases are globally unique, so lookup is unambiguous.
func TestActionCatalogHasNoDuplicateTypesOrAliases(t *testing.T) {
	seen := make(map[string]string)
	for _, spec := range AllActionSpecs() {
		names := append([]string{spec.Type}, spec.Aliases...)
		for _, name := range names {
			if owner, dup := seen[name]; dup {
				t.Errorf("type/alias %q registered by both %q and %q", name, owner, spec.Type)
			}
			seen[name] = spec.Type
		}
	}
}

// TestEngineActionTypeMembership pins the engine action-type set so catalog
// drift against the pre-catalog isEngineActionType list is caught.
func TestEngineActionTypeMembership(t *testing.T) {
	expected := []string{
		"control_if", "control_switch", "control_loop", "control_parallel", "control_break", "control_continue",
		"rendezvous", "cookie_clear", "cookie_set", "http_async_start", "http_async_stop", "http_sync", "cache_clear", "http_connections_close", "error_message",
		"source_verification", "source_message", "source_runtime", "source_transaction", "source_timer",
		"source_metric", "source_parameter", "source_json", "source_xml",
		"source_database", "source_secret", "source_external", "source_review",
		"custom_call", "review_required",
		"live_table", "native_javascript",
	}
	for _, actionType := range expected {
		if !isEngineActionType(actionType) {
			t.Errorf("isEngineActionType(%q) = false, want true", actionType)
		}
	}
	// Legacy prefix resolves through CanonicalActionType.
	if !isEngineActionType("loadrunner_transaction") {
		t.Error("isEngineActionType(loadrunner_transaction) = false, want true")
	}
	for _, actionType := range []string{"", "GET", "browser_click", "jms_publish", "kafka_produce", "db_query", "ws_send", "grpc_unary", "dns_query", "http_sequence"} {
		if isEngineActionType(actionType) {
			t.Errorf("isEngineActionType(%q) = true, want false", actionType)
		}
	}

	var engineTypes []string
	for _, spec := range AllActionSpecs() {
		if spec.EngineAction() {
			engineTypes = append(engineTypes, spec.Type)
		}
	}
	sort.Strings(engineTypes)
	sort.Strings(expected)
	if len(engineTypes) != len(expected) {
		t.Fatalf("engine catalog has %d types %v, want %d %v", len(engineTypes), engineTypes, len(expected), expected)
	}
	for i := range expected {
		if engineTypes[i] != expected[i] {
			t.Fatalf("engine catalog = %v, want %v", engineTypes, expected)
		}
	}
}

// TestProtocolActionTypeMembership pins each protocol family's catalog
// membership to the type sets the pre-catalog switch validators accepted.
func TestProtocolActionTypeMembership(t *testing.T) {
	cases := []struct {
		family   string
		accepted []string
		rejected []string
	}{
		{
			family: "browser",
			accepted: []string{
				"browser_navigate", "browser_back", "browser_forward", "browser_reload",
				"browser_click", "browser_download", "browser_check", "browser_fill", "browser_select",
				"browser_scroll", "browser_tap", "browser_long_press", "browser_swipe", "browser_dblclick",
				"browser_hover", "browser_set_files", "browser_drag_and_drop",
				"browser_press_key", "browser_evaluate_js", "browser_switch_frame",
				"browser_switch_window", "browser_wait", "browser_wait_response",
				"browser_set_geolocation", "browser_screenshot",
			},
			rejected: []string{"browser_unknown", "ws_send", "rendezvous"},
		},
		{
			family:   "websocket",
			accepted: []string{"ws_connect", "ws_send", "ws_receive", "ws_close", "ws_ping"},
			rejected: []string{"ws_unknown", "browser_click"},
		},
		{
			family:   "grpc",
			accepted: []string{"grpc_unary", "grpc_server_stream", "grpc_client_stream", "grpc_bidi_stream"},
			rejected: []string{"grpc_unknown"},
		},
		{
			family: "database",
			accepted: []string{
				"db_query", "database_query", "query", "select",
				"db_exec", "database_exec", "exec", "execute", "insert", "update", "delete",
				"db_procedure", "stored_procedure",
				"db_begin", "database_begin", "begin",
				"db_commit", "database_commit", "commit",
				"db_rollback", "database_rollback", "rollback",
				"db_health", "database_health", "ping",
			},
			rejected: []string{"db_unknown", "GET"},
		},
		{
			family: "network",
			accepted: []string{
				"network_health", "dns_query", "dns_lookup",
				"tcp_request", "tcp_send", "winsock_tcp",
				"udp_request", "udp_send", "winsock_udp",
				"ftp_command", "ftp_login", "ftp_noop",
				"smtp_send", "pop3_command", "imap_command",
				"ldap_bind", "ldap_search",
			},
			rejected: []string{"network_unknown"},
		},
		{
			family:   "jms",
			accepted: []string{"jms_publish", "jms_consume", "jms_request_reply", "jms_ack", "jms_commit", "jms_rollback"},
			rejected: []string{"jms_unknown", "kafka_produce"},
		},
		{
			family:   "kafka",
			accepted: []string{"kafka_produce", "kafka_consume", "kafka_request_reply", "kafka_commit", "kafka_seek"},
			rejected: []string{"kafka_unknown", "jms_publish"},
		},
		{
			family:   "sap_gui",
			accepted: []string{"sapgui_connect", "sapgui_logon", "sapgui_set", "sapgui_press", "sapgui_grid", "sapgui_sync", "sapgui_call"},
			rejected: []string{"sapgui_unknown", "GET"},
		},
		{
			family:   "java_vuser",
			accepted: []string{"java_vuser_run"},
			rejected: []string{"java_vuser_unknown", "jms_publish"},
		},
		{
			family:   "http",
			accepted: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "http_sequence"},
			rejected: []string{"browser_click"},
		},
	}
	for _, tc := range cases {
		for _, actionType := range tc.accepted {
			if _, ok := protocolActionSpec(tc.family, actionType); !ok {
				t.Errorf("protocolActionSpec(%q, %q) not found, want supported", tc.family, actionType)
			}
		}
		for _, actionType := range tc.rejected {
			if _, ok := protocolActionSpec(tc.family, actionType); ok {
				t.Errorf("protocolActionSpec(%q, %q) found, want unsupported", tc.family, actionType)
			}
		}
	}
}

// TestSpecForActionTypeResolvesAliasesAndLegacyNames covers the global
// lookup used by the catalog API and schema generator.
func TestSpecForActionTypeResolvesAliasesAndLegacyNames(t *testing.T) {
	cases := []struct {
		lookup string
		want   string
	}{
		{"grpc_unary", "grpc_unary"},
		{"dns_lookup", "dns_query"},
		{"stored_procedure", "db_procedure"},
		{"loadrunner_transaction", "source_transaction"},
		{"loadrunner_message", "source_message"},
		{"GET", "GET"},
	}
	for _, tc := range cases {
		spec, ok := SpecForActionType(tc.lookup)
		if !ok {
			t.Errorf("SpecForActionType(%q) not found", tc.lookup)
			continue
		}
		if spec.Type != tc.want {
			t.Errorf("SpecForActionType(%q).Type = %q, want %q", tc.lookup, spec.Type, tc.want)
		}
	}
	if _, ok := SpecForActionType("definitely_not_a_type"); ok {
		t.Error("SpecForActionType(definitely_not_a_type) found, want missing")
	}
}

// TestActionSpecsForProtocol covers family resolution, the jms-* provider
// prefix, and engine-action inclusion rules.
func TestActionSpecsForProtocol(t *testing.T) {
	hasType := func(specs []ActionSpec, actionType string) bool {
		for _, spec := range specs {
			if spec.Type == actionType {
				return true
			}
		}
		return false
	}

	httpSpecs := ActionSpecsForProtocol("http")
	if !hasType(httpSpecs, "GET") || !hasType(httpSpecs, "http_sequence") {
		t.Error("http specs missing request types")
	}
	if !hasType(httpSpecs, "control_if") {
		t.Error("http specs should include engine actions")
	}
	if hasType(httpSpecs, "browser_click") {
		t.Error("http specs should not include browser actions")
	}

	jmsSpecs := ActionSpecsForProtocol("jms-solace")
	if !hasType(jmsSpecs, "jms_publish") {
		t.Error("jms-solace specs missing jms_publish")
	}
	if !hasType(jmsSpecs, "control_if") {
		t.Error("jms specs should include engine actions")
	}

	kafkaSpecs := ActionSpecsForProtocol("kafka")
	if !hasType(kafkaSpecs, "kafka_produce") {
		t.Error("kafka specs missing kafka_produce")
	}
	if !hasType(kafkaSpecs, "rendezvous") {
		t.Error("kafka specs should include engine actions")
	}

	browserSpecs := ActionSpecsForProtocol("browser")
	if !hasType(browserSpecs, "browser_click") || !hasType(browserSpecs, "rendezvous") {
		t.Error("browser specs missing browser_click or engine rendezvous")
	}
}

// TestCatalogProtocolFamilies pins the family list the API and docs expose.
func TestCatalogProtocolFamilies(t *testing.T) {
	got := CatalogProtocolFamilies()
	want := []string{"browser", "database", "eventhubs", "grpc", "http", "java_http", "java_vuser", "jms", "kafka", "network", "sap_gui", "soap", "websocket"}
	if len(got) != len(want) {
		t.Fatalf("CatalogProtocolFamilies() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CatalogProtocolFamilies() = %v, want %v", got, want)
		}
	}
}

// TestCatalogDrivenValidationMatchesLegacyMessages spot-checks that the
// catalog-driven validators still produce the exact pre-catalog issue
// strings, including alias and case normalization quirks.
func TestCatalogDrivenValidationMatchesLegacyMessages(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		action   Action
		want     []string
	}{
		{
			name:     "unsupported browser type",
			protocol: "browser",
			action:   Action{Name: "step", Type: "browser_unknown"},
			want:     []string{`actions[0] "step": unsupported browser action type "browser_unknown"`},
		},
		{
			name:     "browser fill missing locator",
			protocol: "browser",
			action:   Action{Name: "fill", Type: "browser_fill"},
			want: []string{
				`actions[0] "fill": missing target or selector`,
			},
		},
		{
			name:     "ws_send missing body",
			protocol: "websocket",
			action:   Action{Name: "send", Type: "ws_send"},
			want:     []string{`actions[0] "send": ws_send requires body`},
		},
		{
			name:     "grpc target shape",
			protocol: "grpc",
			action:   Action{Name: "call", Type: "grpc_unary", Target: "no-slash"},
			want:     []string{`actions[0] "call": grpc_unary target must be service/method`},
		},
		{
			name:     "database alias uppercase with missing sql",
			protocol: "database",
			action:   Action{Name: "q", Type: "SELECT"},
			want:     []string{`actions[0] "q": database action requires target or body SQL`},
		},
		{
			name:     "database unsupported keeps max_rows tail",
			protocol: "database",
			action:   Action{Name: "x", Type: "db_unknown", Params: map[string]any{"max_rows": -1}},
			want: []string{
				`actions[0] "x": unsupported database action type "db_unknown"`,
				`actions[0] "x": max_rows must be zero or greater`,
			},
		},
		{
			name:     "network ftp alias message uses lowercase type",
			protocol: "network",
			action:   Action{Name: "ftp", Type: "FTP_COMMAND"},
			want:     []string{`actions[0] "ftp": ftp_command requires target, value, or body`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateActionForProtocol(tc.protocol, 0, tc.action)
			if len(got) != len(tc.want) {
				t.Fatalf("issues = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("issues = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
