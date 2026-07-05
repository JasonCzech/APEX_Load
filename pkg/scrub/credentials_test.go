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
	out := RedactCredentialBody("user=alice&clientSecret=hunter2&__RequestVerificationToken=csrf-keep")
	if strings.Contains(out, "hunter2") {
		t.Errorf("form password leaked: %q", out)
	}
	if !strings.Contains(out, "csrf-keep") {
		t.Errorf("CSRF token must be preserved: %q", out)
	}
}

func TestRedactCredentialBodyXML(t *testing.T) {
	body := `<soap:Envelope><soap:Body><login apiKey="static-key" data-secret="tracking"><password>hunter2</password><csrf>csrf-keep</csrf><sessionId>sid-keep</sessionId></login></soap:Body></soap:Envelope>`
	out := RedactCredentialBody(body)

	for _, secret := range []string{"hunter2", "static-key"} {
		if strings.Contains(out, secret) {
			t.Errorf("XML credential %q leaked: %q", secret, out)
		}
	}
	for _, dynamic := range []string{"csrf-keep", "sid-keep", `data-secret="tracking"`} {
		if !strings.Contains(out, dynamic) {
			t.Errorf("correlation candidate %q must be preserved: %q", dynamic, out)
		}
	}
}

func TestRedactCredentialBodyMultipart(t *testing.T) {
	body := "--b\r\n" +
		"Content-Disposition: form-data; name=\"username\"\r\n\r\nalice\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"password\"\r\n\r\nhunter2\r\n" +
		"--b\r\n" +
		"Content-Disposition: form-data; name=\"csrf\"\r\n\r\ncsrf-keep\r\n" +
		"--b--"

	out := RedactCredentialBody(body)
	if strings.Contains(out, "hunter2") {
		t.Errorf("multipart credential leaked: %q", out)
	}
	if !strings.Contains(out, "alice") || !strings.Contains(out, "csrf-keep") {
		t.Errorf("non-secret multipart fields must be preserved: %q", out)
	}
}

func TestRedactCredentialBodyLeavesUnknownBodiesAlone(t *testing.T) {
	xml := "<root><note>no credentials here</note></root>"
	if RedactCredentialBody(xml) != xml {
		t.Errorf("XML without credential fields should be returned unchanged")
	}
	if RedactCredentialBody("") != "" {
		t.Errorf("empty body should stay empty")
	}
}
