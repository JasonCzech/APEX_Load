package dsl

import (
	"strings"
	"testing"
)

func httpContractScript(version string, action Action) *Script {
	if action.Name == "" {
		action.Name = "request"
	}
	if action.Type == "" {
		action.Type = "request"
	}
	if action.Target == "" {
		action.Target = "/"
	}
	return &Script{
		Name:     "HTTP contract",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			HTTP:    HTTPConfig{Version: version},
		},
		Actions: []Action{action},
	}
}

func TestValidateScriptIssues_HTTPVersionUsesRuntimeContract(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		wantIssue string
	}{
		{name: "automatic", version: ""},
		{name: "exact HTTP 1.1", version: "1.1"},
		{name: "required HTTP 2", version: "2"},
		{name: "HTTP 1.0 unsupported", version: "1.0", wantIssue: "http version 1.0 is not supported by the native runtime"},
		{name: "unknown version", version: "3", wantIssue: `unsupported HTTP version "3"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := ValidateScriptIssues(httpContractScript(test.version, Action{}))
			joined := strings.Join(issues, "\n")
			if test.wantIssue == "" {
				if strings.Contains(joined, "config.http.version") {
					t.Fatalf("version issues = %v, want none", issues)
				}
				return
			}
			if !strings.Contains(joined, "config.http.version: "+test.wantIssue) {
				t.Fatalf("issues = %v, want version issue containing %q", issues, test.wantIssue)
			}
		})
	}
}

func TestValidateScriptIssues_HTTPHeaderOccurrenceContract(t *testing.T) {
	t.Run("ordinary duplicates retain logical occurrences", func(t *testing.T) {
		script := httpContractScript("", Action{HeaderList: HeaderList{
			{Name: "X-Trace", Value: "first"},
			{Name: "x-trace", Value: "second"},
		}})
		if issues := ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("issues = %v, want none", issues)
		}
	})

	t.Run("case-insensitive compatibility map collision is deterministic", func(t *testing.T) {
		script := httpContractScript("", Action{Headers: map[string]string{
			"X-A": "first",
			"x-a": "second",
		}})
		var first string
		for iteration := 0; iteration < 25; iteration++ {
			joined := strings.Join(ValidateScriptIssues(script), "\n")
			if !strings.Contains(joined, "ambiguous_header_map") {
				t.Fatalf("issues = %q, want ambiguous_header_map", joined)
			}
			if iteration == 0 {
				first = joined
			} else if joined != first {
				t.Fatalf("iteration %d issues changed:\nfirst: %s\nnext: %s", iteration, first, joined)
			}
		}
	})

	t.Run("header list shadows compatibility map before validation", func(t *testing.T) {
		script := httpContractScript("", Action{
			Headers: map[string]string{"x-a": "bad\r\nvalue"},
			HeaderList: HeaderList{{
				Name: "X-A", Value: "valid",
			}},
		})
		if issues := ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("shadowed compatibility-map issues = %v, want none", issues)
		}

		script.Actions[0].Headers["X-B"] = "also\r\ninvalid"
		joined := strings.Join(ValidateScriptIssues(script), "\n")
		if !strings.Contains(joined, `"X-B": header value is not valid`) {
			t.Fatalf("issues = %q, want unshadowed X-B value issue", joined)
		}
		if strings.Contains(joined, `"x-a":`) {
			t.Fatalf("issues = %q, shadowed x-a must not be validated", joined)
		}
	})

	t.Run("special header semantics follow exact version", func(t *testing.T) {
		auto := httpContractScript("", Action{HeaderList: HeaderList{{Name: "Connection", Value: "close"}}})
		if issues := strings.Join(ValidateScriptIssues(auto), "\n"); !strings.Contains(issues, "hop_by_hop_header") {
			t.Fatalf("automatic-mode issues = %q, want hop_by_hop_header", issues)
		}
		exact11 := httpContractScript("1.1", Action{HeaderList: HeaderList{{Name: "Connection", Value: "close"}}})
		if issues := ValidateScriptIssues(exact11); len(issues) != 0 {
			t.Fatalf("exact HTTP/1.1 issues = %v, want none", issues)
		}

		duplicateHost := httpContractScript("1.1", Action{HeaderList: HeaderList{
			{Name: "Host", Value: "one.example.test"},
			{Name: "host", Value: "two.example.test"},
		}})
		if issues := strings.Join(ValidateScriptIssues(duplicateHost), "\n"); !strings.Contains(issues, "duplicate_singleton_header") {
			t.Fatalf("duplicate Host issues = %q, want duplicate_singleton_header", issues)
		}

		framing := httpContractScript("1.1", Action{Headers: map[string]string{"Content-Length": "4"}})
		if issues := strings.Join(ValidateScriptIssues(framing), "\n"); !strings.Contains(issues, "transport_owned_header") {
			t.Fatalf("framing issues = %q, want transport_owned_header", issues)
		}
	})

	t.Run("unresolved values remain authorable without masking literal invalid bytes", func(t *testing.T) {
		templateHost := httpContractScript("", Action{HeaderList: HeaderList{{Name: "Host", Value: "{{var.host}}"}}})
		if issues := ValidateScriptIssues(templateHost); len(issues) != 0 {
			t.Fatalf("template Host issues = %v, want none", issues)
		}

		invalid := httpContractScript("", Action{HeaderList: HeaderList{{Name: "X-Value", Value: "bad\r\n{{var.value}}"}}})
		joined := strings.Join(ValidateScriptIssues(invalid), "\n")
		if !strings.Contains(joined, "invalid_header_value") {
			t.Fatalf("issues = %q, want invalid_header_value", joined)
		}
		if strings.Contains(joined, "bad\r\n") {
			t.Fatalf("validation diagnostic disclosed a header value: %q", joined)
		}
	})

	t.Run("default headers and nested request surfaces are checked", func(t *testing.T) {
		script := httpContractScript("", Action{
			Name: "branch",
			Type: "control_if",
			Control: &ControlFlow{
				Condition: &Condition{Left: "{{var.enabled}}", Op: "exists"},
				Steps: []Action{{
					Name: "nested", Type: "request", Target: "/nested",
					HeaderList: HeaderList{{Name: "Transfer-Encoding", Value: "chunked"}},
				}},
			},
		})
		script.Config.HTTP.DefaultHeaders = map[string]string{"Trailer": "Digest"}
		joined := strings.Join(ValidateScriptIssues(script), "\n")
		if !strings.Contains(joined, "config.http.default_headers") ||
			!strings.Contains(joined, `actions[0].control.steps[0] "nested".header_list`) {
			t.Fatalf("issues = %q, want default and nested header paths", joined)
		}
	})

	t.Run("named config header values use the runtime header contract", func(t *testing.T) {
		tests := []struct {
			name   string
			apply  func(*HTTPConfig)
			header string
		}{
			{
				name:   "user agent",
				apply:  func(config *HTTPConfig) { config.UserAgent = "bad\r\nagent" },
				header: "User-Agent",
			},
			{
				name:   "accept encoding",
				apply:  func(config *HTTPConfig) { config.AcceptEncoding = "gzip\r\nbr" },
				header: "Accept-Encoding",
			},
			{
				name:   "accept language",
				apply:  func(config *HTTPConfig) { config.AcceptLanguage = "en-US\r\nfr" },
				header: "Accept-Language",
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				script := httpContractScript("", Action{})
				test.apply(&script.Config.HTTP)
				joined := strings.Join(ValidateScriptIssues(script), "\n")
				if !strings.Contains(joined, "config.http.default_headers") ||
					!strings.Contains(joined, `"`+test.header+`"`) ||
					!strings.Contains(joined, "invalid_header_value") {
					t.Fatalf("issues = %q, want invalid effective %s header", joined, test.header)
				}
			})
		}
	})

	t.Run("named and default config headers retain runtime map precedence", func(t *testing.T) {
		script := httpContractScript("", Action{})
		script.Config.HTTP.UserAgent = "named"
		script.Config.HTTP.DefaultHeaders = map[string]string{"user-agent": "default"}
		joined := strings.Join(ValidateScriptIssues(script), "\n")
		if !strings.Contains(joined, "config.http.default_headers") ||
			!strings.Contains(joined, "ambiguous_header_map") {
			t.Fatalf("case-variant issues = %q, want effective-map collision", joined)
		}

		script.Config.HTTP.UserAgent = "bad\r\nagent"
		script.Config.HTTP.DefaultHeaders = map[string]string{"User-Agent": "valid override"}
		if issues := ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("exact-key override issues = %v, want none", issues)
		}
	})

	t.Run("HTTP sequence child headers are checked", func(t *testing.T) {
		requestShapes := map[string]any{
			"map": []any{map[string]any{
				"name": "child", "method": "GET", "target": "/child",
				"header_list": []any{
					map[string]any{"name": "User-Agent", "value": "one"},
					map[string]any{"name": "user-agent", "value": "two"},
				},
			}},
			"typed": []Action{{
				Name: "child", Method: "GET", Target: "/child",
				HeaderList: HeaderList{
					{Name: "User-Agent", Value: "one"},
					{Name: "user-agent", Value: "two"},
				},
			}},
		}
		for name, requests := range requestShapes {
			t.Run(name, func(t *testing.T) {
				script := httpContractScript("", Action{
					Name:   "sequence",
					Type:   "http_sequence",
					Params: map[string]any{"requests": requests},
				})
				joined := strings.Join(ValidateScriptIssues(script), "\n")
				if !strings.Contains(joined, `params.requests[0] "child".header_list`) ||
					!strings.Contains(joined, "duplicate_singleton_header") {
					t.Fatalf("issues = %q, want sequence duplicate-singleton issue", joined)
				}
			})
		}
	})

	t.Run("malformed HTTP sequence child header shapes are rejected", func(t *testing.T) {
		tests := []struct {
			name    string
			request map[string]any
			want    string
		}{
			{
				name: "header map value",
				request: map[string]any{
					"name": "child", "method": "GET", "target": "/child",
					"headers": map[string]any{"X-Trace": 123},
				},
				want: `.headers must be an object with string values`,
			},
			{
				name: "header list value",
				request: map[string]any{
					"name": "child", "method": "GET", "target": "/child",
					"header_list": []any{map[string]any{"name": "X-Trace", "value": 123}},
				},
				want: `.header_list must be a valid header array`,
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				script := httpContractScript("", Action{
					Name: "sequence", Type: "http_sequence",
					Params: map[string]any{"requests": []any{test.request}},
				})
				joined := strings.Join(ValidateScriptIssues(script), "\n")
				if !strings.Contains(joined, test.want) {
					t.Fatalf("issues = %q, want %q", joined, test.want)
				}
			})
		}
	})
}

func TestValidateScriptIssues_ImportFidelityEvidenceRequiresMatchingDurableGate(t *testing.T) {
	headerReason := "http_header_transport_owned_header"
	versionReason := "unsupported_http_version"
	action := Action{Metadata: map[string]any{
		"execution_blocker":  importReviewRequiredBlocker,
		"execution_blockers": []string{importReviewRequiredBlocker},
		"import_review": map[string]any{
			"version":      1,
			"reason_codes": []string{headerReason, versionReason},
		},
		importHeaderFidelityMetadataKey: map[string]any{
			"version":      1,
			"reason_codes": []string{headerReason},
			"source_refs":  []string{"collection.json#/item/0/header/0"},
			"omitted_occurrences": []map[string]any{{
				"source_index": 0,
				"name":         "Content-Length",
				"reason_code":  headerReason,
				"source_ref":   "collection.json#/item/0/header/0",
			}},
		},
		importHTTPVersionFidelityMetadataKey: map[string]any{
			"version":     1,
			"reason_code": versionReason,
			"source_refs": []string{"collection.json#/item/0/protocol"},
		},
	}}
	script := httpContractScript("", action)
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("valid blocked-draft evidence issues = %v, want none", issues)
	}

	delete(script.Actions[0].Metadata, "execution_blocker")
	delete(script.Actions[0].Metadata, "execution_blockers")
	if issues := strings.Join(ValidateScriptIssues(script), "\n"); !strings.Contains(issues, "requires the \"import_review_required\" execution blocker") {
		t.Fatalf("missing-gate issues = %q", issues)
	}

	script.Actions[0].Metadata["execution_blocker"] = importReviewRequiredBlocker
	script.Actions[0].Metadata["import_review"] = map[string]any{
		"version":      1,
		"reason_codes": []string{versionReason},
	}
	if issues := strings.Join(ValidateScriptIssues(script), "\n"); !strings.Contains(issues, `must include fidelity reason "`+headerReason+`"`) {
		t.Fatalf("mismatched-reason issues = %q", issues)
	}
}

func TestValidateScriptIssues_HTTPVersionFidelityReasonContract(t *testing.T) {
	tests := []struct {
		name      string
		reason    string
		wantIssue bool
	}{
		{name: "unsupported source version", reason: "unsupported_http_version"},
		{name: "script-wide mode cannot preserve request version", reason: "http_version_not_preserved"},
		{name: "unknown reason fails closed", reason: "future_http_version_reason", wantIssue: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := Action{Metadata: map[string]any{
				"execution_blocker": importReviewRequiredBlocker,
				"import_review": map[string]any{
					"version":      1,
					"reason_codes": []string{test.reason},
				},
				importHTTPVersionFidelityMetadataKey: map[string]any{
					"version":     1,
					"reason_code": test.reason,
				},
			}}
			joined := strings.Join(ValidateScriptIssues(httpContractScript("", action)), "\n")
			hasReasonIssue := strings.Contains(joined, importHTTPVersionFidelityMetadataKey+".reason_code")
			if hasReasonIssue != test.wantIssue {
				t.Fatalf("issues = %q, reason issue = %v, want %v", joined, hasReasonIssue, test.wantIssue)
			}
		})
	}
}

func TestHTTPActionCatalogDocumentsLogicalHeaderContract(t *testing.T) {
	spec, ok := SpecForActionType("GET")
	if !ok {
		t.Fatal("GET action spec not found")
	}
	fields := make(map[string]FieldSpec, len(spec.Fields))
	for _, field := range spec.Fields {
		fields[field.Name] = field
	}
	headerList, ok := fields["header_list"]
	if !ok || headerList.Kind != FieldKindArray {
		t.Fatalf("header_list field = %#v, want array field", headerList)
	}
	for _, phrase := range []string{"logical header occurrences", "retains casing, order", "durable import review gate"} {
		if !strings.Contains(headerList.Doc, phrase) {
			t.Fatalf("header_list doc = %q, want phrase %q", headerList.Doc, phrase)
		}
	}
	headers, ok := fields["headers"]
	if !ok || !strings.Contains(headers.Doc, "unique case-insensitively") {
		t.Fatalf("headers field = %#v, want compatibility-map uniqueness contract", headers)
	}
}
