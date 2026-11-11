package bruno

import (
	"reflect"
	"testing"
)

func TestEnvironmentVariableValuesIncludeSecretsDotEnvAndPlainValues(t *testing.T) {
	yamlEnvironment := []byte(`name: dev
variables:
  - name: subscriberNumber
    value: "5551234"
  - name: port
    value: 8443
  - name: apiKey
    value: hidden
    secret: true
  - name: legacyHost
    value: old.example.test
    enabled: false
  - name: url
    value: "https://{{host}}/v1"
  - name: partnerPassword
    value: "{{secret.PARTNER_PASSWORD}}"
  - name: scrubbed
    value: "[REDACTED]"
`)
	want := map[string]string{
		"subscriberNumber": "5551234",
		"port":             "8443",
		"apiKey":           "hidden",
		"scrubbed":         "[REDACTED]",
	}
	if got := environmentVariableValues(yamlEnvironment, ".yml"); !reflect.DeepEqual(got, want) {
		t.Errorf("yaml values = %v, want %v", got, want)
	}

	bruEnvironment := []byte(`vars {
  url: https://{{host}}/v1
  password: "p}w"
  _token: under-tok
  auth.token: dot-tok
  subscriberNumber: 5551234
}
vars:secret {
  authHeader: Bearer {{rawToken}}
  rawToken: literal-secret-123
}
vars:secret [
  subscriberNumber
]
`)
	wantBru := map[string]string{
		"password":         "p}w",
		"_token":           "under-tok",
		"auth.token":       "dot-tok",
		"subscriberNumber": "5551234",
		"rawToken":         "literal-secret-123",
	}
	if got := environmentVariableValues(bruEnvironment, ".bru"); !reflect.DeepEqual(got, wantBru) {
		t.Errorf("bru values = %v, want %v", got, wantBru)
	}
	wantNames := []string{"_token", "auth.token", "authHeader", "password", "rawToken", "subscriberNumber", "url"}
	if gotNames := environmentVariableNames(bruEnvironment, ".bru"); !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("bru variable names = %v, want %v", gotNames, wantNames)
	}

	inlineBru := []byte(`vars { url: https://{{host}}/v1 }` + "\n" + `vars:secret { inlineSecret: "s}ec" }` + "\n")
	if gotInline := environmentVariableValues(inlineBru, ".bru"); !reflect.DeepEqual(gotInline, map[string]string{"inlineSecret": "s}ec"}) {
		t.Errorf("inline bru values = %v, want map[inlineSecret:s}ec]", gotInline)
	}

	commentApostropheBru := []byte("vars { # team's env\n  label: team's api\n} # end vars\nvars:secret { # secret's block\n  secretKey: top-secret-val\n} # end secret\nvars { inlineLabel: owner's gateway }\n")
	wantCommentBru := map[string]string{
		"label":       "team's api",
		"secretKey":   "top-secret-val",
		"inlineLabel": "owner's gateway",
	}
	if gotCommentBru := environmentVariableValues(commentApostropheBru, ".bru"); !reflect.DeepEqual(gotCommentBru, wantCommentBru) {
		t.Errorf("comment/apostrophe bru values = %v, want %v", gotCommentBru, wantCommentBru)
	}
	if gotFlagged := nativeBaseEnvironmentVariables([]sourceFile{{Path: "environments/prod.bru", Data: commentApostropheBru}}); gotFlagged["secretKey"].Value != "top-secret-val" || gotFlagged["label"].Value != "" {
		t.Errorf("flagged bru vars after apostrophe comment = %#v, want only secretKey=top-secret-val", gotFlagged)
	}

	dotEnv := []byte("# comment\nsubscriberNumber=5551234\nexport API_TOKEN=\"abc\"\nQUOTED='xyz'\n")
	wantDotEnv := map[string]string{
		"subscriberNumber": "5551234",
		"API_TOKEN":        "abc",
		"QUOTED":           "xyz",
	}
	if got := environmentVariableValues(dotEnv, ".env"); !reflect.DeepEqual(got, wantDotEnv) {
		t.Errorf(".env values = %v, want %v", got, wantDotEnv)
	}
}
