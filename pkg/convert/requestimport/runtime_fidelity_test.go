package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestFinalizeActionRuntimeHeaderFidelity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		version     string
		headers     HeaderList
		wantHeaders dsl.HeaderList
		wantSummary map[string]string
		wantReasons []string
		wantElement string
		wantExact   bool
	}{
		{
			name:    "ordinary duplicate extension headers remain exact",
			version: "HTTP/1.1",
			headers: HeaderList{
				{Name: "X-Trace", Value: "first"},
				{Name: "x-trace", Value: "second"},
			},
			wantHeaders: dsl.HeaderList{
				{Name: "X-Trace", Value: "first"},
				{Name: "x-trace", Value: "second"},
			},
			wantSummary: map[string]string{"x-trace": "second"},
			wantExact:   true,
		},
		{
			name:    "duplicate singleton is omitted and gated",
			version: "HTTP/1.1",
			headers: HeaderList{
				{Name: "Host", Value: "first.example.test"},
				{Name: "host", Value: "second.example.test"},
			},
			wantHeaders: dsl.HeaderList{{Name: "Host", Value: "first.example.test"}},
			wantSummary: map[string]string{"Host": "first.example.test"},
			wantReasons: []string{"http_header_duplicate_singleton_header"},
			wantElement: "Host",
		},
		{
			name:    "templated host and templated extension name are import-valid",
			version: "HTTP/1.1",
			headers: HeaderList{
				{Name: "Host", Value: "{{var.host}}"},
				{Name: "X-{{var.header_name}}", Value: "{{var.value}}"},
			},
			wantHeaders: dsl.HeaderList{
				{Name: "Host", Value: "{{var.host}}"},
				{Name: "X-{{var.header_name}}", Value: "{{var.value}}"},
			},
			wantSummary: map[string]string{
				"Host":                  "{{var.host}}",
				"X-{{var.header_name}}": "{{var.value}}",
			},
			wantExact: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			action, report, err := FinalizeAction(EffectiveRequest{
				Key:         test.name,
				Method:      "GET",
				Target:      "/",
				HTTPVersion: test.version,
				Headers:     test.headers,
				Source: SourceRef{
					Format:  "fixture",
					Path:    "collection.json",
					Pointer: "/requests/0",
				},
			}, FinalizeOptions{ActionPath: "actions[0]"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(action.HeaderList, test.wantHeaders) {
				t.Fatalf("header_list = %#v, want %#v", action.HeaderList, test.wantHeaders)
			}
			if !reflect.DeepEqual(action.Headers, test.wantSummary) {
				t.Fatalf("headers = %#v, want %#v", action.Headers, test.wantSummary)
			}
			if test.wantExact {
				if report.Disposition != ExecutableExact ||
					action.Metadata[ImportHeaderFidelityMetadataKey] != nil {
					t.Fatalf("action/report = %#v / %#v", action, report)
				}
				return
			}
			if report.Disposition != BlockedDraft ||
				action.Metadata["execution_blocker"] != importReviewRequiredBlocker {
				t.Fatalf("action/report = %#v / %#v", action, report)
			}
			marker := headerFidelityMarker(t, action)
			if !reflect.DeepEqual(marker["reason_codes"], test.wantReasons) {
				t.Fatalf("reason_codes = %#v", marker["reason_codes"])
			}
			occurrences, _ := marker["omitted_occurrences"].([]map[string]any)
			if len(occurrences) != 1 || occurrences[0]["name"] != test.wantElement {
				t.Fatalf("occurrences = %#v", marker["omitted_occurrences"])
			}
		})
	}
}

func TestFinalizeScriptSanitizesActionHeaderMapWithRuntimePrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		headers     map[string]string
		headerList  dsl.HeaderList
		wantHeaders map[string]string
		wantList    dsl.HeaderList
		wantReason  string
	}{
		{
			name:        "map-only transport-owned header",
			headers:     map[string]string{"Content-Length": "7", "X-Safe": "yes"},
			wantHeaders: map[string]string{"X-Safe": "yes"},
			wantReason:  "http_header_transport_owned_header",
		},
		{
			name:        "map case collision chooses deterministic survivor",
			headers:     map[string]string{"X-Test": "first", "x-test": "second"},
			wantHeaders: map[string]string{"X-Test": "first"},
			wantReason:  "http_header_ambiguous_header_map",
		},
		{
			name:        "list shadows compatibility map and safe map-only field survives",
			headers:     map[string]string{"Content-Length": "compatibility", "X-Safe": "yes"},
			headerList:  dsl.HeaderList{{Name: "Content-Length", Value: "authored"}},
			wantHeaders: map[string]string{"X-Safe": "yes"},
			wantReason:  "http_header_transport_owned_header",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := validBaseScript()
			script.Config.HTTP.Version = "1.1"
			script.Actions = []dsl.Action{{
				Name:       "request",
				Type:       "request",
				Method:     "GET",
				Target:     "/",
				Headers:    test.headers,
				HeaderList: test.headerList,
			}}
			result, err := FinalizeScript(script, nil, FinalizeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Script.Actions[0]
			if !reflect.DeepEqual(action.Headers, test.wantHeaders) ||
				(len(action.HeaderList) != 0 || len(test.wantList) != 0) &&
					!reflect.DeepEqual(action.HeaderList, test.wantList) {
				t.Fatalf("sanitized action = %#v", action)
			}
			if len(result.ValidationIssues) != 0 ||
				result.Disposition != BlockedDraft ||
				result.Readiness.ReplayReady {
				t.Fatalf("result = %#v", result)
			}
			marker := headerFidelityMarker(t, action)
			if !reflect.DeepEqual(marker["reason_codes"], []string{test.wantReason}) {
				t.Fatalf("marker = %#v", marker)
			}
		})
	}
}

func TestRuntimeHeaderFidelityDoesNotDiscloseAuthoredCanaryName(t *testing.T) {
	t.Parallel()
	const canary = "CANARY-do-not-disclose"
	action, report, err := FinalizeAction(EffectiveRequest{
		Method: "GET",
		Target: "/",
		Headers: HeaderList{{
			Name:  "X " + canary,
			Value: "value",
		}},
	}, FinalizeOptions{ActionPath: "actions[0]"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Metadata map[string]any
		Findings []importreview.Finding
	}{
		Metadata: action.Metadata,
		Findings: report.Findings,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("fidelity evidence disclosed authored header name: %s", encoded)
	}
	if len(action.HeaderList) != 0 || headerFidelityOccurrenceName(t, action) != "<invalid-header>" {
		t.Fatalf("action = %#v", action)
	}
}

func TestFinalizeActionPreserveModeRetainsInvalidHeaderBytesInInertEnvelope(t *testing.T) {
	t.Parallel()
	const (
		headerName  = "Authorization"
		headerValue = "Bearer preserve-header-canary-8f31\r\nX-Injected: yes"
	)
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:    "preserved-invalid-header",
		Name:   "Preserved invalid header",
		Method: "GET",
		Target: "/",
		Headers: HeaderList{{
			Name:  headerName,
			Value: headerValue,
			Source: SourceRef{
				Format:  "fixture",
				Path:    "collection.json",
				Pointer: "/requests/0/headers/0",
			},
		}},
	}, FinalizeOptions{
		ActionPath: "actions[0]",
		Credentials: &CredentialProjectionOptions{
			Mode: credentialpolicy.Preserve,
		},
	})
	if err != nil {
		t.Fatalf("FinalizeAction: %v", err)
	}
	if len(action.HeaderList) != 0 || len(action.Headers) != 0 {
		t.Fatalf("invalid header remained transport-active: list %#v map %#v", action.HeaderList, action.Headers)
	}
	if action.Metadata["execution_blocker"] != importReviewRequiredBlocker ||
		report.Disposition != BlockedDraft ||
		len(report.Requirements) != 0 {
		t.Fatalf("action/report = %#v / %#v", action, report)
	}
	nameBytes, valueBytes := preservedHeaderOccurrenceBytes(t, action, 0)
	if string(nameBytes) != headerName || string(valueBytes) != headerValue {
		t.Fatalf("preserved bytes = %q: %q, want %q: %q", nameBytes, valueBytes, headerName, headerValue)
	}
	foundFidelityMapping := false
	for _, mapping := range report.SourceMappings {
		foundFidelityMapping = foundFidelityMapping || mapping.Target ==
			"actions[0].metadata.import_header_fidelity.omitted_occurrences[0]"
	}
	if !foundFidelityMapping {
		t.Fatalf("source mappings = %#v", report.SourceMappings)
	}
	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("blocked inert draft validation issues = %v", issues)
	}
	encodedReport, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedReport), "preserve-header-canary-8f31") {
		t.Fatalf("public report disclosed preserved header: %s", encodedReport)
	}
}

func TestFinalizeScriptPreservePolicyRetainsInvalidDefaultHeaderBytes(t *testing.T) {
	t.Parallel()
	const (
		headerName  = "Authorization"
		headerValue = "Bearer default-header-canary-a34d\r\nX-Injected: yes"
	)
	script := validBaseScript()
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "fixture"}}
	script.Config.HTTP.DefaultHeaders = map[string]string{headerName: headerValue}
	script.Actions = []dsl.Action{{
		Name: "request", Type: "request", Method: "GET", Target: "/",
	}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if len(result.Script.Config.HTTP.DefaultHeaders) != 0 {
		t.Fatalf("invalid default remained transport-active: %#v", result.Script.Config.HTTP.DefaultHeaders)
	}
	action := result.Script.Actions[0]
	nameBytes, valueBytes := preservedHeaderOccurrenceBytes(t, action, 0)
	if string(nameBytes) != headerName || string(valueBytes) != headerValue {
		t.Fatalf("preserved default bytes = %q: %q, want %q: %q", nameBytes, valueBytes, headerName, headerValue)
	}
	if result.Disposition != BlockedDraft || result.Readiness.ReplayReady ||
		len(result.ValidationIssues) != 0 {
		t.Fatalf("finalized result = %#v", result)
	}
}

func TestFinalizeScriptSanitizesAndGatesEffectiveDefaultHeaders(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Config.HTTP.Version = "1.1"
	script.Config.HTTP.DefaultHeaders = map[string]string{
		"Content-Length": "7",
		"X-Safe":         "yes",
	}
	script.Config.HTTP.UserAgent = "first"
	script.Config.HTTP.DefaultHeaders["user-agent"] = "second"
	script.Actions = []dsl.Action{
		{Name: "first", Type: "request", Method: "GET", Target: "/first"},
		{Name: "second", Type: "request", Method: "GET", Target: "/second"},
	}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Script.Config.HTTP.DefaultHeaders, map[string]string{"X-Safe": "yes"}) ||
		result.Script.Config.HTTP.UserAgent != "first" {
		t.Fatalf("http config = %#v", result.Script.Config.HTTP)
	}
	for index, action := range result.Script.Actions {
		if action.Metadata["execution_blocker"] != importReviewRequiredBlocker {
			t.Fatalf("action %d metadata = %#v", index, action.Metadata)
		}
		marker := headerFidelityMarker(t, action)
		if !reflect.DeepEqual(marker["reason_codes"], []string{
			"http_header_ambiguous_header_map",
			"http_header_duplicate_singleton_header",
			"http_header_transport_owned_header",
		}) {
			t.Fatalf("action %d marker = %#v", index, marker)
		}
	}
	if len(result.ValidationIssues) != 0 || result.Disposition != BlockedDraft {
		t.Fatalf("result = %#v", result)
	}
}

func TestFinalizeScriptRemovesConvenienceScalarsShadowedByExactDefaultHeaders(t *testing.T) {
	t.Parallel()
	const (
		userAgentCanary      = "shadowed-user-agent-a51f"
		acceptEncodingCanary = "shadowed-encoding-b62a"
		acceptLanguageCanary = "shadowed-language-c73b"
	)
	script := validBaseScript()
	script.Config.HTTP.UserAgent = userAgentCanary + "\r\nInjected: yes"
	script.Config.HTTP.AcceptEncoding = acceptEncodingCanary + "\r\nInjected: yes"
	script.Config.HTTP.AcceptLanguage = acceptLanguageCanary + "\r\nInjected: yes"
	script.Config.HTTP.DefaultHeaders = map[string]string{
		"User-Agent":      "Safe-Agent/1.0",
		"Accept-Encoding": "gzip",
		"Accept-Language": "en-US",
	}
	script.Actions = []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if result.Script.Config.HTTP.UserAgent != "" ||
		result.Script.Config.HTTP.AcceptEncoding != "" ||
		result.Script.Config.HTTP.AcceptLanguage != "" {
		t.Fatal("semantically dead convenience scalar survived its exact map override")
	}
	if !reflect.DeepEqual(result.Script.Config.HTTP.DefaultHeaders, map[string]string{
		"User-Agent":      "Safe-Agent/1.0",
		"Accept-Encoding": "gzip",
		"Accept-Language": "en-US",
	}) {
		t.Fatalf("effective default headers changed: %#v", result.Script.Config.HTTP.DefaultHeaders)
	}
	encoded, err := json.Marshal(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{userAgentCanary, acceptEncodingCanary, acceptLanguageCanary} {
		if strings.Contains(string(encoded), canary) {
			t.Fatal("shadowed convenience scalar leaked into the stored draft")
		}
	}
	if len(result.ValidationIssues) != 0 || result.Disposition != ExecutableExact {
		t.Fatalf("finalized result = disposition %q, issues %#v", result.Disposition, result.ValidationIssues)
	}
}

func TestFinalizeScriptCreatesReviewPlaceholderForInvalidDefaultsWithoutRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare func(*dsl.Script)
		reason  string
	}{
		{
			name: "invalid default header",
			prepare: func(script *dsl.Script) {
				script.Config.HTTP.UserAgent = "bad\r\nvalue"
			},
			reason: "http_header_invalid_header_value",
		},
		{
			name: "unsupported global version",
			prepare: func(script *dsl.Script) {
				script.Config.HTTP.Version = "HTTP/1.0"
			},
			reason: reasonUnsupportedHTTPVersion,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := validBaseScript()
			test.prepare(script)
			result, err := FinalizeScript(script, nil, FinalizeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Script.Config.HTTP.UserAgent != "" ||
				result.Script.Config.HTTP.Version != "" ||
				len(result.Script.Actions) != 1 ||
				result.Script.Actions[0].Type != "review_required" {
				t.Fatalf("script = %#v", result.Script)
			}
			if len(result.ValidationIssues) != 0 ||
				result.Script.Actions[0].Metadata["execution_blocker"] != importReviewRequiredBlocker {
				t.Fatalf("result = %#v", result)
			}
			review, _ := result.Script.Actions[0].Metadata["import_review"].(map[string]any)
			reasons, _ := review["reason_codes"].([]string)
			if !sliceContains(reasons, test.reason) {
				t.Fatalf("review = %#v", review)
			}
		})
	}
}

func TestFinalizeScriptMapsCombinedActionAndDefaultFidelityEvidence(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(EffectiveRequest{
		Key:     "request",
		Method:  "GET",
		Target:  "/",
		Headers: HeaderList{{Name: "Content-Length", Value: "7"}},
		Source: SourceRef{
			Format:  "fixture",
			Path:    "request.json",
			Pointer: "/headers/0",
		},
	}, FinalizeOptions{ActionPath: "actions[0]"})
	if err != nil {
		t.Fatal(err)
	}
	script := validBaseScript()
	script.Config.HTTP.Version = "1.1"
	script.Config.HTTP.DefaultHeaders = map[string]string{"Trailer": "Digest"}
	script.Actions = []dsl.Action{action}

	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	marker := headerFidelityMarker(t, result.Script.Actions[0])
	occurrences, _ := marker["omitted_occurrences"].([]map[string]any)
	if len(occurrences) != 2 {
		t.Fatalf("occurrences = %#v", marker["omitted_occurrences"])
	}
	var targets []string
	for _, mapping := range result.SourceMappings {
		if strings.Contains(mapping.Target, ".metadata."+ImportHeaderFidelityMetadataKey+".omitted_occurrences") {
			targets = append(targets, mapping.Target)
		}
	}
	if !reflect.DeepEqual(targets, []string{
		"actions[0].metadata.import_header_fidelity.omitted_occurrences[0]",
		"actions[0].metadata.import_header_fidelity.omitted_occurrences[1]",
	}) {
		t.Fatalf("source mapping targets = %#v", targets)
	}
}

func TestSanitizeMapRequestHeadersPreservesLegacyListAndGatesMalformedShapes(t *testing.T) {
	t.Parallel()
	source := SourceRef{Format: "fixture", Path: "sequence.json", Pointer: "/requests/0"}
	request := map[string]any{
		"headers": []any{
			map[string]any{"name": "X-Safe", "value": "yes"},
			map[string]any{"name": "Content-Length", "value": "7"},
			42,
		},
	}
	omissions := sanitizeMapRequestHeaders(request, "1.1", source, 0)
	if len(omissions) != 2 {
		t.Fatalf("omissions = %#v", omissions)
	}
	if !reflect.DeepEqual(request["headers"], map[string]string{"X-Safe": "yes"}) {
		t.Fatalf("headers = %#v", request["headers"])
	}
	headerList, _ := request["header_list"].([]map[string]any)
	if !reflect.DeepEqual(headerList, []map[string]any{{"name": "X-Safe", "value": "yes"}}) {
		t.Fatalf("header_list = %#v", request["header_list"])
	}

	malformedMap := map[string]any{
		"headers": map[string]any{
			"X-Safe":   "yes",
			"X-Canary": 42,
		},
	}
	omissions = sanitizeMapRequestHeaders(malformedMap, "1.1", source, 0)
	if len(omissions) != 1 ||
		omissions[0].ReasonCode != "http_header_malformed_shape" ||
		!reflect.DeepEqual(malformedMap["headers"], map[string]string{"X-Safe": "yes"}) {
		t.Fatalf("malformed map projection = %#v / %#v", malformedMap, omissions)
	}
}

func TestFinalizeScriptReconcilesRequestHTTPVersions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		configVersion     string
		requestVersions   []string
		wantConfigVersion string
		wantGated         []bool
		wantReason        string
	}{
		{
			name:              "uniform h2 promotes exact script mode",
			requestVersions:   []string{"HTTP/2", "2"},
			wantConfigVersion: "2",
			wantGated:         []bool{false, false},
		},
		{
			name:            "mixed exact versions remain auto and gated",
			requestVersions: []string{"HTTP/1.1", "HTTP/2"},
			wantGated:       []bool{true, true},
			wantReason:      reasonHTTPVersionNotPreserved,
		},
		{
			name:              "configured exact mismatch gates source request",
			configVersion:     "1.1",
			requestVersions:   []string{"HTTP/2"},
			wantConfigVersion: "1.1",
			wantGated:         []bool{true},
			wantReason:        reasonHTTPVersionNotPreserved,
		},
		{
			name:            "declared plus unspecified cannot promote",
			requestVersions: []string{"HTTP/2", ""},
			wantGated:       []bool{true, false},
			wantReason:      reasonHTTPVersionNotPreserved,
		},
		{
			name:            "unsupported request version is a blocked draft",
			requestVersions: []string{"HTTP/1.0"},
			wantGated:       []bool{true},
			wantReason:      reasonUnsupportedHTTPVersion,
		},
		{
			name:              "unsupported script version clears to auto and gates every request",
			configVersion:     "HTTP/1.0",
			requestVersions:   []string{"", ""},
			wantConfigVersion: "",
			wantGated:         []bool{true, true},
			wantReason:        reasonUnsupportedHTTPVersion,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := validBaseScript()
			script.Config.HTTP.Version = test.configVersion
			for _, version := range test.requestVersions {
				metadata := map[string]any{}
				if version != "" {
					metadata["http_version"] = version
				}
				script.Actions = append(script.Actions, dsl.Action{
					Name:     "request",
					Type:     "request",
					Method:   "GET",
					Target:   "/",
					Metadata: metadata,
				})
			}
			result, err := FinalizeScript(script, nil, FinalizeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Script.Config.HTTP.Version != test.wantConfigVersion {
				t.Fatalf("config version = %q, want %q", result.Script.Config.HTTP.Version, test.wantConfigVersion)
			}
			for index, wantGated := range test.wantGated {
				action := result.Script.Actions[index]
				gated := action.Metadata["execution_blocker"] == importReviewRequiredBlocker
				if gated != wantGated {
					t.Fatalf("action %d gated = %t, want %t; metadata %#v", index, gated, wantGated, action.Metadata)
				}
				if !wantGated {
					continue
				}
				marker, _ := action.Metadata[ImportHTTPVersionFidelityMetadataKey].(map[string]any)
				if marker["reason_code"] != test.wantReason {
					t.Fatalf("action %d marker = %#v", index, marker)
				}
			}
			if len(result.ValidationIssues) != 0 {
				t.Fatalf("validation issues = %#v", result.ValidationIssues)
			}
		})
	}
}

func headerFidelityMarker(t *testing.T, action dsl.Action) map[string]any {
	t.Helper()
	marker, ok := action.Metadata[ImportHeaderFidelityMetadataKey].(map[string]any)
	if !ok {
		t.Fatalf("missing %s in metadata %#v", ImportHeaderFidelityMetadataKey, action.Metadata)
	}
	return marker
}

func headerFidelityOccurrenceName(t *testing.T, action dsl.Action) string {
	t.Helper()
	marker := headerFidelityMarker(t, action)
	occurrences, ok := marker["omitted_occurrences"].([]map[string]any)
	if !ok || len(occurrences) != 1 {
		t.Fatalf("omitted occurrences = %#v", marker["omitted_occurrences"])
	}
	name, _ := occurrences[0]["name"].(string)
	return name
}

func preservedHeaderOccurrenceBytes(t *testing.T, action dsl.Action, index int) ([]byte, []byte) {
	t.Helper()
	marker := headerFidelityMarker(t, action)
	occurrences := metadataObjectValues(marker["omitted_occurrences"])
	if index < 0 || index >= len(occurrences) {
		t.Fatalf("omitted occurrences = %#v, want index %d", marker["omitted_occurrences"], index)
	}
	name, ok := occurrences[index][dsl.ImportHeaderFidelityPreservedNameBase64Key].(string)
	if !ok {
		t.Fatalf("missing preserved name bytes in %#v", occurrences[index])
	}
	value, ok := occurrences[index][dsl.ImportHeaderFidelityPreservedValueBase64Key].(string)
	if !ok {
		t.Fatalf("missing preserved value bytes in %#v", occurrences[index])
	}
	nameBytes, err := base64.StdEncoding.DecodeString(name)
	if err != nil {
		t.Fatalf("decode preserved name: %v", err)
	}
	valueBytes, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode preserved value: %v", err)
	}
	return nameBytes, valueBytes
}

func sliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
