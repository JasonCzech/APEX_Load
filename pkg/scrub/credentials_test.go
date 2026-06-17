package scrub

import (
	"strings"
	"testing"
)

func TestRedactCredentialHeaders(t *testing.T) {
	headers := map[string]string{
		"Authorization": "Basic dXNlcjpwYXNz",
		"X-Api-Key":     "secret-key-123",
		"X-CSRF-Token":  "csrf-keep-me",
		"Cookie":        "SESSIONID=keep-this-session",
	}
	out := RedactCredentialHeaders(headers)

	if out["Authorization"] != Redacted {
		t.Errorf("Basic auth should be redacted, got %q", out["Authorization"])
	}
	if out["X-Api-Key"] != Redacted {
		t.Errorf("api key should be redacted, got %q", out["X-Api-Key"])
	}
	// Correlation candidates must survive.
	if out["X-CSRF-Token"] != "csrf-keep-me" {
		t.Errorf("CSRF token must be preserved, got %q", out["X-CSRF-Token"])
	}
	if out["Cookie"] != "SESSIONID=keep-this-session" {
		t.Errorf("session cookie must be preserved, got %q", out["Cookie"])
	}
	// Input must not be mutated.
	if headers["Authorization"] != "Basic dXNlcjpwYXNz" {
		t.Errorf("input header map was mutated")
	}
}

func TestRedactCredentialHeadersKeepsBearer(t *testing.T) {
	out := RedactCredentialHeaders(map[string]string{"Authorization": "Bearer dynamic-token-abc"})
	if out["Authorization"] != "Bearer dynamic-token-abc" {
		t.Errorf("bearer token (possibly dynamic) must be preserved, got %q", out["Authorization"])
	}
}

func TestRedactCredentialBodyJSON(t *testing.T) {
	body := `{"username":"alice","password":"hunter2","csrf":"tok-keep","nested":{"client_secret":"s3cr3t"}}`
	out := RedactCredentialBody(body)

	if strings.Contains(out, "hunter2") {
		t.Errorf("password leaked: %q", out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("nested client_secret leaked: %q", out)
	}
	// Correlation candidate + non-secret fields preserved.
	if !strings.Contains(out, "tok-keep") {
		t.Errorf("CSRF token must be preserved: %q", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("username must be preserved: %q", out)
	}
}

func TestRedactCredentialBodyForm(t *testing.T) {
	out := RedactCredentialBody("user=alice&password=hunter2&__RequestVerificationToken=csrf-keep")
	if strings.Contains(out, "hunter2") {
		t.Errorf("form password leaked: %q", out)
	}
	if !strings.Contains(out, "csrf-keep") {
		t.Errorf("CSRF token must be preserved: %q", out)
	}
}

func TestRedactCredentialBodyLeavesUnknownBodiesAlone(t *testing.T) {
	xml := "<root><password>hunter2</password></root>"
	if RedactCredentialBody(xml) != xml {
		t.Errorf("unrecognised (XML) body should be returned unchanged")
	}
	if RedactCredentialBody("") != "" {
		t.Errorf("empty body should stay empty")
	}
}
