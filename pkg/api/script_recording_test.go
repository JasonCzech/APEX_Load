package api

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRedactBaselineObservation(t *testing.T) {
	obs := correlate.Observation{
		ID:             3,
		RequestURL:     "/api/login",
		RequestBody:    `{"username":"alice","password":"hunter2","csrf":"tok-keep"}`,
		RequestHeaders: map[string]string{"Authorization": "Basic dXNlcjpwdw==", "X-CSRF-Token": "csrf-keep"},
		ResponseBody:   `{"session_id":"sess-keep","token":"abc","user_role":"admin"}`,
	}

	got := redactBaselineObservation(obs)

	if got.ID != 3 {
		t.Errorf("ID must be preserved as the action index, got %d", got.ID)
	}
	// Credentials stripped on the request side; non-secret fields survive.
	if strings.Contains(got.RequestBody, "hunter2") {
		t.Errorf("password leaked into baseline: %q", got.RequestBody)
	}
	if !strings.Contains(got.RequestBody, "alice") {
		t.Errorf("non-secret username must be preserved in persisted view: %q", got.RequestBody)
	}
	if got.RequestHeaders["Authorization"] != "[REDACTED]" {
		t.Errorf("Basic auth must be redacted, got %q", got.RequestHeaders["Authorization"])
	}
	// Live session/CSRF tokens are redacted in the persisted (scrubbed) view —
	// including in JSON bodies — because correlation-time replay reconstruction
	// uses the raw sidecar (rawBaselineObservation) below, not this view.
	if strings.Contains(got.RequestBody, "tok-keep") {
		t.Errorf("CSRF token in body must be redacted in the persisted view: %q", got.RequestBody)
	}
	if got.RequestHeaders["X-CSRF-Token"] != "[REDACTED]" {
		t.Errorf("CSRF header must be redacted in persisted baselines, got %q", got.RequestHeaders["X-CSRF-Token"])
	}
	if strings.Contains(got.ResponseBody, `"token":"abc"`) {
		t.Errorf("response token leaked into persisted baseline: %q", got.ResponseBody)
	}
	if strings.Contains(got.ResponseBody, "sess-keep") {
		t.Errorf("session_id must be redacted in the persisted view: %q", got.ResponseBody)
	}
	if !strings.Contains(got.ResponseBody, "admin") {
		t.Errorf("non-secret response field must be preserved in persisted view: %q", got.ResponseBody)
	}

	// The raw server-side sidecar preserves the live session/CSRF values so
	// correlation and record-vs-replay diagnosis still work.
	raw := rawBaselineObservation(obs)
	if !strings.Contains(raw.ResponseBody, "sess-keep") || !strings.Contains(raw.RequestBody, "tok-keep") {
		t.Errorf("raw correlation sidecar must preserve session/csrf: req=%q resp=%q", raw.RequestBody, raw.ResponseBody)
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
			if strings.Contains(got.RequestBody, "csrf-keep") || !strings.Contains(got.RequestBody, scrub.Redacted) {
				t.Fatalf("request CSRF token was not redacted from persisted view: %q", got.RequestBody)
			}
		})
	}
}

func TestPersistRecordedBaselineStoresRedactedAndEncryptedViews(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-test-key")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	server := &Server{store: store}
	err = server.persistRecordedBaseline("script-1", "browser_recording", []correlate.Observation{{
		ID:             1,
		RequestURL:     "https://example.test/login?token=request-token",
		RequestBody:    `{"username":"alice","password":"hunter2","csrf":"tok-keep"}`,
		RequestHeaders: map[string]string{"Authorization": "Basic dXNlcjpwdw=="},
		ResponseBody:   `{"session_id":"sess-keep","token":"response-token"}`,
	}})
	if err != nil {
		t.Fatalf("persistRecordedBaseline: %v", err)
	}

	record, err := store.GetScriptRecording("script-1")
	if err != nil {
		t.Fatalf("GetScriptRecording: %v", err)
	}
	if record == nil {
		t.Fatal("recording was not persisted")
	}
	if record.Sensitivity != "encrypted_contains_secrets" {
		t.Fatalf("Sensitivity = %q, want encrypted", record.Sensitivity)
	}
	if len(record.Observations) != 1 || len(record.CorrelationObservations) != 0 || record.EncryptedCorrelationObservations == "" {
		t.Fatalf("recording observations = %#v", record)
	}
	persisted := record.Observations[0]
	for _, secret := range []string{"request-token", "hunter2", "response-token", "Basic dXNlcjpwdw=="} {
		if strings.Contains(persisted.RequestURL+persisted.RequestBody+persisted.ResponseBody+persisted.RequestHeaders["Authorization"]+record.EncryptedCorrelationObservations, secret) {
			t.Fatalf("persisted observation leaked %q: %#v", secret, persisted)
		}
	}
	decrypted := baselineObservations(record)
	if len(decrypted) != 1 || !strings.Contains(decrypted[0].RequestURL, "request-token") || !strings.Contains(decrypted[0].RequestBody, "hunter2") || !strings.Contains(decrypted[0].ResponseBody, "response-token") {
		t.Fatalf("encrypted correlation observation lost source values: %#v", decrypted)
	}
}

func TestPersistRecordedBaselineWithoutKeyKeepsOnlyScrubbedView(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &Server{store: store}
	if err := server.persistRecordedBaseline("script-no-key", "browser_recording", []correlate.Observation{{RequestBody: `{"password":"secret-value"}`}}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetScriptRecording("script-no-key")
	if err != nil {
		t.Fatal(err)
	}
	if record.Sensitivity != scrub.SensitivityScrubbed || record.EncryptedCorrelationObservations != "" || strings.Contains(record.Observations[0].RequestBody, "secret-value") {
		t.Fatalf("keyless recording retained raw detail: %#v", record)
	}
}
