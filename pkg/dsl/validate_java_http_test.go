package dsl

import "testing"

func TestValidateScriptIssues_JavaHTTP(t *testing.T) {
	script := &Script{
		Name:     "java-http-invalid",
		Protocol: "java_http",
		Config: ScriptConfig{
			BaseURL: "https://example.com",
			JavaHTTP: JavaHTTPConfig{
				Codec:        "mystery",
				HelperMode:   "sidecar",
				BodyEncoding: "raw",
			},
		},
		Actions: []Action{{Name: "invoke", Type: "request", Target: "/call"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, "config.java_http.codec \"mystery\" is not supported")
	assertIssue(t, issues, "config.java_http.body_encoding must be base64")
}

func TestValidateScriptIssues_JavaHTTPAllowsSidecarHelperMode(t *testing.T) {
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
		Actions: []Action{{Name: "invoke", Type: "request", Target: "/call"}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
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
		Actions: []Action{{Name: "invoke", Type: "request", Target: "/call"}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}