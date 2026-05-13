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

func assertIssue(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if issue == want {
			return
		}
	}
	t.Fatalf("issues = %v, want %q", issues, want)
}