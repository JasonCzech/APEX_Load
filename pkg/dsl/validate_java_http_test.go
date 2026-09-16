package dsl

import "testing"

func TestValidateScriptIssues_JavaHTTP(t *testing.T) {
	script := &Script{
		Name:     "java-http-invalid",
		Protocol: "java_http",
		Config: ScriptConfig{
			BaseURL: "https://example.com",
			JavaHTTP: JavaHTTPConfig{
				Codec:          "mystery",
				HelperMode:     "sidecar",
				BodyEncoding:   "raw",
				ObjectSchema:   "com.example.Order",
				RequestMapping: map[string]string{"field": "value"},
				ExtractorType:  "java",
			},
		},
		Actions: []Action{{Name: "invoke", Type: "request", Method: "GET", Target: "/call"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, "config.java_http.codec \"mystery\" is not supported")
	assertIssueContains(t, issues, "config.java_http.helper_mode must be embedded")
	assertIssue(t, issues, "config.java_http.body_encoding must be base64")
	assertIssueContains(t, issues, "config.java_http.object_schema is not supported")
	assertIssueContains(t, issues, "config.java_http.request_mapping is not supported")
	assertIssueContains(t, issues, "config.java_http.extractor_type is not supported")
}

func TestValidateScriptIssues_JavaHTTPRejectsSidecarHelperMode(t *testing.T) {
	script := &Script{
		Name:     "java-http-sidecar",
		Protocol: "java_http",
		Config: ScriptConfig{
			BaseURL: "https://example.com",
			JavaHTTP: JavaHTTPConfig{
				Codec:        "java_serialized_object_base64",
				HelperMode:   "sidecar",
				BodyEncoding: "base64",
			},
		},
		Actions: []Action{{Name: "invoke", Type: "request", Method: "GET", Target: "/call"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssueContains(t, issues, "config.java_http.helper_mode must be embedded")
}

func TestValidateScriptIssues_JavaHTTPAllowsCurrentMVP(t *testing.T) {
	script := &Script{
		Name:     "java-http-valid",
		Protocol: "java_http",
		Config: ScriptConfig{
			BaseURL: "https://example.com",
			JavaHTTP: JavaHTTPConfig{
				Codec:        "java_serialized_object_base64",
				HelperMode:   "embedded",
				BodyEncoding: "base64",
			},
		},
		Actions: []Action{{Name: "invoke", Type: "request", Method: "GET", Target: "/call"}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}
