package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func recordingDigestTestScript() *dsl.Script {
	return &dsl.Script{
		Protocol: "http",
		Actions: []dsl.Action{
			{Name: "login", Type: "http_request", Target: "/login"},
			{Name: "orders", Type: "http_request", Target: "/orders"},
		},
	}
}

func saveRecordingParent(t *testing.T, store storage.Store, id string, script *dsl.Script) *dsl.Script {
	t.Helper()
	clone := *script
	clone.ID = id
	if strings.TrimSpace(clone.Name) == "" {
		clone.Name = id
	}
	clone.ProjectID = normalizeProjectID(clone.ProjectID)
	if clone.ProjectID != storage.DefaultProjectID {
		now := time.Now().UTC()
		if err := store.SaveProject(&storage.Project{ID: clone.ProjectID, Name: clone.ProjectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", clone.ProjectID, err)
		}
	}
	if err := store.SaveScript(&clone); err != nil {
		t.Fatalf("SaveScript(%s): %v", id, err)
	}
	parent, err := store.GetScript(id)
	if err != nil {
		t.Fatalf("GetScript(%s): %v", id, err)
	}
	return parent
}

func TestScriptCorrelationActionGraphDigestIsCanonicalAndContentBound(t *testing.T) {
	left := recordingDigestTestScript()
	left.Config.BaseURL = "https://api.example.test"
	left.Config.HTTP.DefaultHeaders = map[string]string{"X-Default": "one"}
	left.Actions[0].Headers = map[string]string{"X-B": "two", "X-A": "one"}
	right := recordingDigestTestScript()
	right.ID = "display-only-id"
	right.Name = "Display-only name"
	right.ProjectID = "display-only-project"
	right.CreatedAt = time.Now().UTC()
	right.Config.BaseURL = left.Config.BaseURL
	right.Config.HTTP.DefaultHeaders = map[string]string{"X-Default": "one"}
	right.Init = []dsl.Action{}
	right.Teardown = []dsl.Action{}
	right.Actions[0].Headers = map[string]string{"X-A": "one", "X-B": "two"}

	leftDigest, err := scriptCorrelationActionGraphDigest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := scriptCorrelationActionGraphDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("equivalent action graphs produced %q and %q", leftDigest, rightDigest)
	}

	right.Config.BaseURL = "https://other.example.test"
	changedConfigDigest, err := scriptCorrelationActionGraphDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedConfigDigest == leftDigest {
		t.Fatal("base URL edit did not change the action-graph digest")
	}
	right.Config.BaseURL = left.Config.BaseURL
	right.Config.HTTP.DefaultHeaders["X-Default"] = "two"
	changedConfigDigest, err = scriptCorrelationActionGraphDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedConfigDigest == leftDigest {
		t.Fatal("HTTP default-header edit did not change the action-graph digest")
	}
	right.Config.HTTP.DefaultHeaders["X-Default"] = "one"
	right.DataSources = []dsl.DataSource{{
		Name:       "users",
		Type:       "csv",
		InlineData: "username,password\nalice,changed\n",
		Strategy:   "sequential",
	}}
	changedDataSourceDigest, err := scriptCorrelationActionGraphDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedDataSourceDigest == leftDigest {
		t.Fatal("data-source edit did not change the action-graph digest")
	}
	right.DataSources = nil
	right.Parameters = []dsl.Parameter{{
		Name:       "username",
		Type:       "file",
		DataSource: "users",
		Column:     "username",
	}}
	changedParameterDigest, err := scriptCorrelationActionGraphDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedParameterDigest == leftDigest {
		t.Fatal("parameter edit did not change the action-graph digest")
	}
	right.Parameters = nil
	right.Actions[1].Body = `{"changed":true}`
	changedDigest, err := scriptCorrelationActionGraphDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == leftDigest {
		t.Fatal("semantic action edit did not change the action-graph digest")
	}
	if err := validateRecordingActionGraph(&storage.ScriptRecordingRecord{ActionGraphDigest: leftDigest}, right); !errors.Is(err, errRecordingActionGraphDigestMismatch) {
		t.Fatalf("validate mismatch = %v", err)
	}
	if err := validateRecordingActionGraph(&storage.ScriptRecordingRecord{}, left); !errors.Is(err, errRecordingActionGraphDigestMissing) {
		t.Fatalf("validate legacy record = %v", err)
	}
}

func TestRedactBaselineObservationKeepsOnlyStructuralIdentity(t *testing.T) {
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
	if got.RequestURL != "" || got.RequestBody != "" || got.ResponseBody != "" || got.RequestHeaders != nil || got.ResponseHeaders != nil {
		t.Fatalf("persistence projection retained request/response material: %#v", got)
	}

	// The raw server-side sidecar preserves the live session/CSRF values so
	// correlation and record-vs-replay diagnosis still work.
	raw := rawBaselineObservation(obs)
	if !strings.Contains(raw.ResponseBody, "sess-keep") || !strings.Contains(raw.RequestBody, "tok-keep") {
		t.Errorf("raw correlation sidecar must preserve session/csrf: req=%q resp=%q", raw.RequestBody, raw.ResponseBody)
	}
}

func TestRedactBaselineObservationOmitsArbitraryBodyFormats(t *testing.T) {
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
			if got.ID != 1 || got.RequestURL != "" || got.RequestBody != "" {
				t.Fatalf("arbitrary %s material entered persistence projection: %#v", tc.name, got)
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
	parent := saveRecordingParent(t, store, "script-1", recordingDigestTestScript())
	err = server.persistRecordedBaseline("script-1", "browser_recording", parent, []correlate.Observation{{
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
	if persisted.ID != 1 || persisted.RequestURL != "" || persisted.RequestBody != "" || persisted.ResponseBody != "" || persisted.RequestHeaders != nil || persisted.ResponseHeaders != nil {
		t.Fatalf("persisted observation was not structural-only: %#v", persisted)
	}
	if !strings.HasPrefix(record.EncryptedCorrelationObservations, recordingBaselineCipherPrefixV3) {
		t.Fatalf("encrypted sidecar = %q, want v3", record.EncryptedCorrelationObservations)
	}
	for _, secret := range []string{"request-token", "hunter2", "response-token", "Basic dXNlcjpwdw=="} {
		if strings.Contains(persisted.RequestURL+persisted.RequestBody+persisted.ResponseBody+persisted.RequestHeaders["Authorization"]+record.EncryptedCorrelationObservations, secret) {
			t.Fatalf("persisted observation leaked %q: %#v", secret, persisted)
		}
	}
	decrypted, err := baselineObservations(record)
	if err != nil {
		t.Fatalf("baselineObservations: %v", err)
	}
	if len(decrypted) != 1 || !strings.Contains(decrypted[0].RequestURL, "request-token") || !strings.Contains(decrypted[0].RequestBody, "hunter2") || !strings.Contains(decrypted[0].ResponseBody, "response-token") {
		t.Fatalf("encrypted correlation observation lost source values: %#v", decrypted)
	}
}

func TestPersistRecordedBaselineCapsEncryptedObservationCount(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-test-key")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	observations := make([]correlate.Observation, maxRecordedBaselineObservations+25)
	for index := range observations {
		observations[index] = correlate.Observation{ID: index, ResponseBody: `{"token":"token-value-abcdef123456"}`}
	}
	server := &Server{store: store}
	parent := saveRecordingParent(t, store, "script-capped-count", recordingDigestTestScript())
	if err := server.persistRecordedBaseline("script-capped-count", "proxy_recording", parent, observations); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetScriptRecording("script-capped-count")
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := baselineObservations(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Observations) != maxRecordedBaselineObservations || len(decrypted) != maxRecordedBaselineObservations || record.RequestCount != len(observations) {
		t.Fatalf("baseline cap = redacted:%d encrypted:%d original count:%d, want retained %d/original %d", len(record.Observations), len(decrypted), record.RequestCount, maxRecordedBaselineObservations, len(observations))
	}
}

func TestPersistRecordedBaselineCapsEncryptedPlaintextSize(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-test-key")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// The conservative estimator budgets six JSON bytes per input byte. Each
	// body fits independently, but two cannot enter the encrypted sidecar.
	body := strings.Repeat("x", maxRecordedBaselinePlaintextSize/7)
	server := &Server{store: store}
	parent := saveRecordingParent(t, store, "script-capped-bytes", recordingDigestTestScript())
	if err := server.persistRecordedBaseline("script-capped-bytes", "proxy_recording", parent, []correlate.Observation{
		{ID: 0, ResponseBody: body},
		{ID: 1, ResponseBody: body},
	}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetScriptRecording("script-capped-bytes")
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := baselineObservations(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(decrypted) != 1 || record.RequestCount != 2 {
		t.Fatalf("byte-bounded baseline = encrypted:%d original count:%d, want one retained from two", len(decrypted), record.RequestCount)
	}
}

func TestPersistRecordedBaselinePreservesOriginalCountWhenNoObservationFits(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-test-key")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := &Server{store: store}
	parent := saveRecordingParent(t, store, "script-fully-byte-truncated", recordingDigestTestScript())
	// A NUL expands to six bytes in JSON. This single observation therefore
	// exceeds the authenticated plaintext ceiling before any raw evidence can
	// be retained.
	body := strings.Repeat("\x00", maxRecordedBaselinePlaintextSize/6+1)
	if err := server.persistRecordedBaseline(parent.ID, "proxy_recording", parent, []correlate.Observation{
		{ID: 0, ResponseBody: body},
	}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetScriptRecording(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record == nil {
		t.Fatal("fully truncated capture lost its durable count witness")
	}
	if record.RequestCount != 1 || len(record.Observations) != 0 || record.EncryptedCorrelationObservations != "" {
		t.Fatalf("fully truncated baseline = count:%d retained:%d encrypted:%t, want 1/0/false",
			record.RequestCount, len(record.Observations), record.EncryptedCorrelationObservations != "")
	}
}

func TestBaselineObservationsFailsClosedForUnavailableRawBaseline(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-test-key")
	tests := []struct {
		name    string
		record  *storage.ScriptRecordingRecord
		wantErr error
	}{
		{
			name: "explicitly scrubbed only",
			record: &storage.ScriptRecordingRecord{
				Sensitivity:  scrub.SensitivityScrubbed,
				Observations: []storage.ScriptRecordingObservation{{ResponseBody: `{"token":"[REDACTED]"}`}},
			},
			wantErr: errRecordingBaselineUnauthenticated,
		},
		{
			name: "legacy plaintext raw sidecar",
			record: &storage.ScriptRecordingRecord{
				CorrelationObservations: []storage.ScriptRecordingObservation{{ResponseBody: `{"token":"plaintext"}`}},
			},
			wantErr: errRecordingBaselineUnauthenticated,
		},
		{
			name: "encrypted payload missing",
			record: &storage.ScriptRecordingRecord{
				Sensitivity:  "encrypted_contains_secrets",
				Observations: []storage.ScriptRecordingObservation{{ResponseBody: `{"token":"[REDACTED]"}`}},
			},
			wantErr: errRecordingBaselineUnauthenticated,
		},
		{
			name: "legacy encrypted payload is unbound",
			record: &storage.ScriptRecordingRecord{
				Sensitivity:                      "encrypted_contains_secrets",
				EncryptedCorrelationObservations: "enc:v1:not-valid-ciphertext",
				Observations:                     []storage.ScriptRecordingObservation{{ResponseBody: `{"token":"[REDACTED]"}`}},
			},
			wantErr: errRecordingBaselineCiphertextUnbound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			observations, err := baselineObservations(tc.record)
			if err == nil {
				t.Fatalf("baselineObservations = %#v, want an explicit error", observations)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("baselineObservations error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBaselineObservationsStreamsAndRejectsAuthenticatedCountOverflow(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-test-key")
	binding := testRecordingBaselineBinding()
	raw := make([]storage.ScriptRecordingObservation, maxRecordedBaselineObservations+1)
	for index := range raw {
		raw[index].ID = index
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptRecordingBaseline(payload, binding)
	if err != nil {
		t.Fatal(err)
	}
	record := &storage.ScriptRecordingRecord{
		ScriptID:                         binding.ScriptID,
		ProjectID:                        binding.ProjectID,
		ScriptIncarnation:                binding.ScriptIncarnation,
		ActionGraphDigest:                binding.ActionGraphDigest,
		SchemaVersion:                    binding.SchemaVersion,
		EncryptedCorrelationObservations: encrypted,
	}
	if observations, err := baselineObservations(record); err == nil || observations != nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("baselineObservations overflow = %#v, %v", observations, err)
	}
}

func TestDecodeRecordingBaselineObservationsRejectsTrailingJSON(t *testing.T) {
	if observations, err := decodeRecordingBaselineObservations([]byte(`[{"id":1}] {"id":2}`)); err == nil || observations != nil {
		t.Fatalf("decode trailing JSON = %#v, %v", observations, err)
	}
}

func TestScrubRepairSuggestionsOmitsSensitiveRepairLiterals(t *testing.T) {
	suggestions := scrubRepairSuggestions([]correlate.RepairSuggestion{
		{
			// These intentionally do not look secret to heuristic scrubbers. Every
			// dynamic correlation value is sensitive at this boundary regardless.
			RecordedValue: "abc123",
			ReplayValue:   "def456",
			ReviewAction:  "auto_apply_available",
		},
		{
			RecordedValue:   "ghi789",
			ReplayValue:     "jkl012",
			RepairLocations: []correlate.RepairLocation{{Field: "request_body", Start: 5, End: 11}},
			ReviewAction:    "auto_apply_available",
		},
	})
	if len(suggestions) != 2 {
		t.Fatalf("suggestions = %#v", suggestions)
	}
	for _, got := range suggestions {
		if got.RecordedValue != "" || got.ReplayValue != "" {
			t.Fatalf("sensitive values remained repair literals: %#v", got)
		}
	}
	if suggestions[0].ReviewAction != "manual_value_replacement_required" {
		t.Fatalf("review action without locations = %q, want manual replacement", suggestions[0].ReviewAction)
	}
	if suggestions[1].ReviewAction != "auto_apply_available" || len(suggestions[1].RepairLocations) != 1 {
		t.Fatalf("structural auto-apply metadata was not preserved: %#v", suggestions[1])
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
	parent := saveRecordingParent(t, store, "script-no-key", recordingDigestTestScript())
	if err := server.persistRecordedBaseline("script-no-key", "browser_recording", parent, []correlate.Observation{{RequestBody: `{"password":"secret-value"}`}}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetScriptRecording("script-no-key")
	if err != nil {
		t.Fatal(err)
	}
	if record.Sensitivity != scrub.SensitivityScrubbed || record.EncryptedCorrelationObservations != "" || record.Observations[0].RequestURL != "" || record.Observations[0].RequestBody != "" || record.Observations[0].ResponseBody != "" || record.Observations[0].RequestHeaders != nil || record.Observations[0].ResponseHeaders != nil {
		t.Fatalf("keyless recording retained raw detail: %#v", record)
	}
}

func TestAttestRecordingBaselineCapturePersistsExactSameProjectConversion(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	proxyRecorder := recorder.New("127.0.0.1:0")
	session, err := proxyRecorder.StartSessionForProject("proxy-session", "Checkout", "project-a", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:          1,
		Method:       "POST",
		URL:          "https://shop.example.test/login",
		Body:         `{"username":"alice","password":"secret"}`,
		ResponseBody: `{"token":"recorded-token"}`,
	})

	server := &Server{store: store, recorder: proxyRecorder}
	converted := recorder.ConvertSession(session, "Checkout")
	if converted == nil || converted.Script == nil {
		t.Fatal("recording conversion produced no script")
	}
	safeScript, err := preparePersistentImportedLibraryScript(converted.Script, nil, "recorder")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := proxyRecorder.GetSession(session.ID)
	if !ok {
		t.Fatal("recording snapshot not found")
	}
	reconverted := recorder.ConvertSession(snapshot, safeScript.Name)
	safeReconverted, err := preparePersistentImportedLibraryScript(reconverted.Script, nil, "recorder")
	if err != nil {
		t.Fatal(err)
	}
	directDigest, _ := scriptCorrelationActionGraphDigest(safeScript)
	snapshotDigest, _ := scriptCorrelationActionGraphDigest(safeReconverted)
	if directDigest != snapshotDigest {
		directJSON, _ := json.Marshal(safeScript.Actions)
		snapshotJSON, _ := json.Marshal(safeReconverted.Actions)
		t.Fatalf("snapshot conversion changed action graph: direct=%s snapshot=%s", directJSON, snapshotJSON)
	}
	attested, err := server.attestRecordingBaselineCapture("project-a", safeScript, session.ID)
	if err != nil {
		t.Fatalf("attest exact conversion: %v", err)
	}
	if attested == nil || attested.source != "proxy_recording" || len(attested.observations) != 1 {
		t.Fatalf("attestation = %#v, want one proxy observation", attested)
	}
	safeScript.ProjectID = "project-a"
	parent := saveRecordingParent(t, store, "script-same-project", safeScript)
	if err := server.persistRecordedBaseline("script-same-project", attested.source, parent, attested.observations); err != nil {
		t.Fatal(err)
	}

	record, err := store.GetScriptRecording("script-same-project")
	if err != nil {
		t.Fatal(err)
	}
	if record == nil {
		t.Fatal("same-project proxy recording baseline was not persisted")
	}
	if record.Source != "proxy_recording" {
		t.Fatalf("baseline source = %q, want proxy_recording", record.Source)
	}
	if record.RequestCount != 1 || len(record.Observations) != 1 || record.Observations[0].ID != 0 {
		t.Fatalf("baseline = %#v, want one action-aligned observation", record)
	}
}

func TestAttestRecordingBaselineCaptureRejectsRequestConfigOverride(t *testing.T) {
	proxyRecorder := recorder.New("127.0.0.1:0")
	session, err := proxyRecorder.StartSessionForProject("proxy-config-session", "Checkout", "project-a", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.test"
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq: 1, Method: http.MethodGet, URL: "https://shop.example.test/cart", Path: "/cart",
	})
	converted := recorder.ConvertSession(session, "Checkout")
	if converted == nil || converted.Script == nil {
		t.Fatal("recording conversion produced no script")
	}
	safeScript, err := preparePersistentImportedLibraryScript(converted.Script, nil, "recorder")
	if err != nil {
		t.Fatal(err)
	}
	overridden := *safeScript
	overridden.Config.BaseURL = "https://unrelated.example.test"

	server := &Server{recorder: proxyRecorder}
	if _, err := server.attestRecordingBaselineCapture("project-a", &overridden, session.ID); !errors.Is(err, errRecordingCaptureUnattested) {
		t.Fatalf("config-overridden attestation error = %v, want unattested", err)
	}
}

func TestAttestRecordingBaselineCaptureRejectsCrossProjectProxySession(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	proxyRecorder := recorder.New("127.0.0.1:0")
	session, err := proxyRecorder.StartSessionForProject("proxy-session", "Checkout", "project-a", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{Seq: 1, Method: "GET", URL: "https://shop.example.test/cart"})

	server := &Server{store: store, recorder: proxyRecorder}
	converted := recorder.ConvertSession(session, "Checkout")
	if _, err := server.attestRecordingBaselineCapture("project-b", converted.Script, session.ID); !errors.Is(err, errRecordingCaptureSourceNotFound) {
		t.Fatalf("cross-project attestation error = %v, want source not found", err)
	}
}

func TestHandleCreateScriptMasksForeignRecordingSessionsAsNotFound(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	proxyRecorder := recorder.New("127.0.0.1:0")
	proxySession, err := proxyRecorder.StartSessionForProject("foreign-proxy", "Checkout", "project-b", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxySession.BaseURL = "https://shop.example.test"
	proxySession.Requests = append(proxySession.Requests, recorder.RecordedRequest{
		Seq: 1, Method: http.MethodGet, URL: "https://shop.example.test/cart", Path: "/cart",
	})
	converted := recorder.ConvertSession(proxySession, "Checkout")
	if converted == nil || converted.Script == nil {
		t.Fatal("proxy recording conversion produced no script")
	}
	converted.Script.ProjectID = "project-a"

	browserManager := NewBrowserRecordingManager(nil)
	browserManager.sessions["foreign-browser"] = &BrowserRecordingSession{
		ID:        "foreign-browser",
		Name:      "Browser",
		ProjectID: "project-b",
		Status:    BrowserRecordingStopped,
		StartedAt: time.Now(),
	}
	server := NewServer(ServerConfig{Store: store})
	server.recorder = proxyRecorder
	server.browserRecording = browserManager

	probe := func(sessionID string) (int, string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"project_id":                  "project-a",
			"source_recording_session_id": sessionID,
			"script":                      converted.Script,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp.Code, resp.Body.String()
	}

	wantStatus, wantBody := probe("missing-recording")
	if wantStatus != http.StatusNotFound {
		t.Fatalf("missing recording status = %d body=%s, want 404", wantStatus, wantBody)
	}
	for _, sessionID := range []string{proxySession.ID, "foreign-browser"} {
		status, body := probe(sessionID)
		if status != wantStatus || body != wantBody {
			t.Errorf("recording %q response = %d %q, want absent response %d %q", sessionID, status, body, wantStatus, wantBody)
		}
	}
}

func TestAttestRecordingBaselineCaptureDoesNotMapBrowserUIActionsToProxyObservations(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Deliberately create the same id in both recorder namespaces. The browser
	// session must win source resolution: UI steps do not share action indexes
	// with proxy/HAR requests, so attaching the proxy baseline would be unsafe.
	const sessionID = "shared-recording-id"
	proxyRecorder := recorder.New("127.0.0.1:0")
	proxySession, err := proxyRecorder.StartSessionForProject(sessionID, "Proxy", "project-a", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxySession.Requests = append(proxySession.Requests, recorder.RecordedRequest{Seq: 1, Method: "GET", URL: "https://shop.example.test/api/cart"})

	browserManager := NewBrowserRecordingManager(nil)
	browserManager.sessions[sessionID] = &BrowserRecordingSession{
		ID:        sessionID,
		Name:      "Browser",
		ProjectID: "project-a",
		Status:    BrowserRecordingStopped,
		StartedAt: time.Now(),
	}
	server := &Server{store: store, recorder: proxyRecorder, browserRecording: browserManager}
	if _, err := server.attestRecordingBaselineCapture("project-a", recordingDigestTestScript(), sessionID); !errors.Is(err, errRecordingCaptureUnattested) {
		t.Fatalf("browser recording attestation error = %v, want unattested", err)
	}
}

func TestAttestRecordingBaselineCaptureRejectsUnrelatedSameProjectScript(t *testing.T) {
	proxyRecorder := recorder.New("127.0.0.1:0")
	session, err := proxyRecorder.StartSessionForProject("proxy-session", "Checkout", "project-a", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.test"
	session.Requests = append(session.Requests, recorder.RecordedRequest{Seq: 1, Method: http.MethodGet, URL: "https://shop.example.test/cart", Path: "/cart"})
	server := &Server{recorder: proxyRecorder}
	if _, err := server.attestRecordingBaselineCapture("project-a", recordingDigestTestScript(), session.ID); !errors.Is(err, errRecordingCaptureUnattested) {
		t.Fatalf("unrelated script attestation error = %v, want unattested", err)
	}
}

func TestHandleCreateScriptRejectsUnattestedRecordingBindingBeforeStore(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: "project-a", Name: "Project A", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})
	proxyRecorder := recorder.New("127.0.0.1:0")
	session, err := proxyRecorder.StartSessionForProject("proxy-session", "Checkout", "project-a", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.test"
	session.Requests = append(session.Requests, recorder.RecordedRequest{Seq: 1, Method: http.MethodGet, URL: "https://shop.example.test/cart", Path: "/cart"})
	server.recorder = proxyRecorder

	converted := recorder.ConvertSession(session, "Unrelated")
	if converted == nil || converted.Script == nil || len(converted.Script.Actions) == 0 {
		t.Fatal("recording conversion produced no actions")
	}
	converted.Script.ProjectID = "project-a"
	converted.Script.Actions[0].Target = "/unrelated"
	body, err := json.Marshal(map[string]any{
		"project_id":                  "project-a",
		"source_recording_session_id": session.ID,
		"script":                      converted.Script,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "not an attested conversion") {
		t.Fatalf("body = %s, want attestation guidance", resp.Body.String())
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("stored scripts = %#v, rejected binding must not create a script", scripts)
	}
}

func TestRecordingPreviewLineageSurvivesServerBindingButRejectsClientGraphEdit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: "project-a", Name: "Project A", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})
	session, err := server.recorder.StartSessionForProject(
		"recording-lineage",
		"Login",
		"project-a",
		recorder.SessionFilters{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.test"
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:                 1,
		Method:              http.MethodPost,
		URL:                 "https://shop.example.test/login",
		Path:                "/login",
		Headers:             map[string]string{"Authorization": "Bearer retained-login-token"},
		Body:                `{"ordinary":"keep"}`,
		ResponseBody:        `{"session":"dynamic"}`,
		RequestHTTPVersion:  "HTTP/1.1",
		ResponseHTTPVersion: "HTTP/1.1",
	})

	previewRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/recorder/"+session.ID+"/convert/preview",
		bytes.NewBufferString(`{"name":"Recorded login"}`),
	)
	previewRequest.Header.Set("Content-Type", "application/json")
	previewResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	var preview struct {
		Script       dsl.Script                 `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	assertRecordedCredentialHTTPVersion(t, &preview.Script)
	if len(preview.Requirements) == 0 ||
		importDraftAttestationToken(&preview.Script) == "" {
		t.Fatalf("preview lacks credential lineage: requirements=%d attestation_present=%t", len(preview.Requirements), importDraftAttestationToken(&preview.Script) != "")
	}

	edited := cloneScriptDefinition(&preview.Script)
	edited.Actions[0].Target = "/unrelated"
	editedPayload, _ := json.Marshal(map[string]any{
		"project_id":                  "project-a",
		"source_recording_session_id": session.ID,
		"script":                      edited,
	})
	editedResponse := httptest.NewRecorder()
	editedRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts",
		bytes.NewReader(editedPayload),
	)
	editedRequest.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(editedResponse, editedRequest)
	if editedResponse.Code != http.StatusConflict {
		t.Fatalf("client-edited graph status = %d body=%s", editedResponse.Code, editedResponse.Body.String())
	}

	credentialEdited := cloneScriptDefinition(&preview.Script)
	if len(credentialEdited.Actions[0].HeaderList) > 0 {
		credentialEdited.Actions[0].HeaderList[0].Value = "Bearer client-substituted-token"
	} else {
		credentialEdited.Actions[0].Headers["Authorization"] = "Bearer client-substituted-token"
	}
	credentialPayload, _ := json.Marshal(map[string]any{
		"project_id":                  "project-a",
		"source_recording_session_id": session.ID,
		"script":                      credentialEdited,
	})
	credentialResponse := httptest.NewRecorder()
	credentialRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts",
		bytes.NewReader(credentialPayload),
	)
	credentialRequest.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(credentialResponse, credentialRequest)
	if credentialResponse.Code != http.StatusConflict {
		t.Fatalf("client-edited credential status = %d body=%s", credentialResponse.Code, credentialResponse.Body.String())
	}

	dataSourceEdited := cloneScriptDefinition(&preview.Script)
	dataSourceEdited.DataSources = append(dataSourceEdited.DataSources, dsl.DataSource{
		Name:       "client_rows",
		Type:       "csv",
		InlineData: "password\nclient-substituted-password\n",
		Strategy:   "sequential",
	})
	dataSourcePayload, _ := json.Marshal(map[string]any{
		"project_id":                  "project-a",
		"source_recording_session_id": session.ID,
		"script":                      dataSourceEdited,
	})
	dataSourceResponse := httptest.NewRecorder()
	dataSourceRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts",
		bytes.NewReader(dataSourcePayload),
	)
	dataSourceRequest.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(dataSourceResponse, dataSourceRequest)
	if dataSourceResponse.Code != http.StatusConflict {
		t.Fatalf("client-edited data source status = %d body=%s", dataSourceResponse.Code, dataSourceResponse.Body.String())
	}

	parameterEdited := cloneScriptDefinition(&preview.Script)
	parameterEdited.Parameters = append(parameterEdited.Parameters, dsl.Parameter{
		Name:   "client_password",
		Type:   "list",
		Values: []string{"client-substituted-password"},
	})
	parameterPayload, _ := json.Marshal(map[string]any{
		"project_id":                  "project-a",
		"source_recording_session_id": session.ID,
		"script":                      parameterEdited,
	})
	parameterResponse := httptest.NewRecorder()
	parameterRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts",
		bytes.NewReader(parameterPayload),
	)
	parameterRequest.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(parameterResponse, parameterRequest)
	if parameterResponse.Code != http.StatusConflict {
		t.Fatalf("client-edited parameter status = %d body=%s", parameterResponse.Code, parameterResponse.Body.String())
	}

	preserved := ensureScriptImportPreserved(&preview.Script, "recorder")
	preserved["correlation_suggestions"] = []any{
		map[string]any{"name": "session", "action_index": float64(0)},
	}
	bindings := make([]secretBindingSelection, 0, len(preview.Requirements))
	for index, requirement := range preview.Requirements {
		bindings = append(bindings, secretBindingSelection{
			RequirementID: requirement.ID,
			SecretName:    fmt.Sprintf("RECORDED_LOGIN_%d", index),
		})
	}
	bindingPayload, _ := json.Marshal(applySecretBindingsRequest{
		Script:   &preview.Script,
		Bindings: bindings,
	})
	bindingResponse := httptest.NewRecorder()
	bindingRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/secret-bindings/apply",
		bytes.NewReader(bindingPayload),
	)
	bindingRequest.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(bindingResponse, bindingRequest)
	if bindingResponse.Code != http.StatusOK {
		t.Fatalf("binding status = %d body=%s", bindingResponse.Code, bindingResponse.Body.String())
	}
	var bound struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(bindingResponse.Body.Bytes(), &bound); err != nil {
		t.Fatal(err)
	}
	if importDraftAttestationToken(&bound.Script) == "" {
		t.Fatal("server-owned binding lost recording lineage")
	}

	claimRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil)
	claim, err := server.claimImportDraftMutation(claimRequest, &bound.Script)
	if err != nil {
		t.Fatalf("claim bound recording lineage: %v", err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:          2,
		Method:       http.MethodGet,
		URL:          "https://shop.example.test/account",
		Path:         "/account",
		ResponseBody: `{"account":"changed-after-preview"}`,
	})
	if _, err := server.attestRecordingBaselineCapture(
		"project-a",
		&bound.Script,
		session.ID,
		claim,
	); !errors.Is(err, errRecordingCaptureUnattested) {
		t.Fatalf("mutable recording snapshot attestation error = %v", err)
	}
	server.releaseImportDraftAttestation(claim)
	session.Requests = session.Requests[:1]

	createPayload, _ := json.Marshal(map[string]any{
		"project_id":                  "project-a",
		"source_recording_session_id": session.ID,
		"script":                      bound.Script,
	})
	createResponse := httptest.NewRecorder()
	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts",
		bytes.NewReader(createPayload),
	)
	createRequest.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("bound recording save status = %d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var stored StoredScript
	if err := json.Unmarshal(createResponse.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stored.ID) == "" {
		t.Fatalf("stored response = %#v", stored)
	}
	baseline, err := store.GetScriptRecording(stored.ID)
	if err != nil {
		t.Fatalf("recording baseline: %v", err)
	}
	if baseline == nil || baseline.Source != "proxy_recording" ||
		baseline.RequestCount != 1 {
		t.Fatalf("recording baseline = %#v", baseline)
	}
}
