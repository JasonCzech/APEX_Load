package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

func TestRedactBaselineObservation(t *testing.T) {
	obs := correlate.Observation{
		ID:             3,
		RequestURL:     "/api/login",
		RequestBody:    `{"username":"alice","password":"hunter2","csrf":"tok-keep"}`,
		RequestHeaders: map[string]string{"Authorization": "Basic dXNlcjpwdw==", "X-CSRF-Token": "csrf-keep"},
		ResponseBody:   `{"session_id":"sess-keep","token":"abc"}`,
	}

	got := redactBaselineObservation(obs)

	if got.ID != 3 {
		t.Errorf("ID must be preserved as the action index, got %d", got.ID)
	}
	// Credentials stripped on the request side.
	if strings.Contains(got.RequestBody, "hunter2") {
		t.Errorf("password leaked into baseline: %q", got.RequestBody)
	}
	if got.RequestHeaders["Authorization"] != "[REDACTED]" {
		t.Errorf("Basic auth must be redacted, got %q", got.RequestHeaders["Authorization"])
	}
	// Body correlation candidates are preserved; request headers are scrubbed as
	// persisted artifact metadata.
	if !strings.Contains(got.RequestBody, "tok-keep") {
		t.Errorf("CSRF in body must be preserved: %q", got.RequestBody)
	}
	if got.RequestHeaders["X-CSRF-Token"] != "[REDACTED]" {
		t.Errorf("CSRF header must be redacted in persisted baselines, got %q", got.RequestHeaders["X-CSRF-Token"])
	}
	// Response is never redacted — it is the source of dynamic values.
	if got.ResponseBody != `{"session_id":"sess-keep","token":"abc"}` {
		t.Errorf("response body must be preserved verbatim, got %q", got.ResponseBody)
	}
}

func TestRedactBaselineObservationRedactsXMLAndMultipartCredentials(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "xml",
			body: `<soap:Envelope><soap:Body><login apiKey="static-key"><password>hunter2</password><csrf>csrf-keep</csrf></login></soap:Body></soap:Envelope>`,
		},
		{
			name: "multipart",
			body: "--b\r\n" +
				"Content-Disposition: form-data; name=\"password\"\r\n\r\nhunter2\r\n" +
				"--b\r\n" +
				"Content-Disposition: form-data; name=\"csrf\"\r\n\r\ncsrf-keep\r\n" +
				"--b--",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactBaselineObservation(correlate.Observation{
				ID:          1,
				RequestURL:  "/api/login",
				RequestBody: tc.body,
			})
			for _, secret := range []string{"hunter2", "static-key"} {
				if strings.Contains(got.RequestBody, secret) {
					t.Fatalf("request credential %q leaked into baseline: %q", secret, got.RequestBody)
				}
			}
			if !strings.Contains(got.RequestBody, "csrf-keep") {
				t.Fatalf("request correlation token was not preserved: %q", got.RequestBody)
			}
		})
	}
}
