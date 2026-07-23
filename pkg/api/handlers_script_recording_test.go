package api

import (
	"context"
	"encoding/base64"
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
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/storage"
)

func recordingTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store})
	now := time.Now().UTC()
	for _, projectID := range []string{"p1", "p2"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	return server
}

type recordingStatusReadSpy struct {
	storage.Store
	fullReads            int
	statusReads          int
	fullRecordOverride   *storage.ScriptRecordingRecord
	statusRecordOverride *storage.ScriptRecordingStatusRecord
}

func (s *recordingStatusReadSpy) GetScriptRecording(scriptID string) (*storage.ScriptRecordingRecord, error) {
	s.fullReads++
	if s.fullRecordOverride != nil {
		return s.fullRecordOverride, nil
	}
	return nil, errors.New("full recording read must not serve a status poll")
}

func (s *recordingStatusReadSpy) GetScriptRecordingStatus(scriptID string) (*storage.ScriptRecordingStatusRecord, error) {
	s.statusReads++
	if s.statusRecordOverride != nil {
		return s.statusRecordOverride, nil
	}
	return s.Store.GetScriptRecordingStatus(scriptID)
}

func storeRecordingDigestTestScript(t *testing.T, server *Server, id string) *dsl.Script {
	t.Helper()
	script := recordingDigestTestScript()
	script.ID = id
	script.Name = id
	script.ProjectID = "p1"
	stored := server.runner.StoreScript(script)
	if stored == nil || stored.Script == nil {
		t.Fatalf("StoreScript(%s) returned no script", id)
	}
	return stored.Script
}

func saveEncryptedRecordingTestBaseline(t *testing.T, server *Server, scriptID, actionGraphDigest string, requestCount int, observations []storage.ScriptRecordingObservation) {
	t.Helper()
	t.Setenv(recordingBaselineKeyEnvVar, "recording-fixture-key")
	parent, err := server.store.GetScript(scriptID)
	if err != nil {
		t.Fatalf("load recording parent: %v", err)
	}
	payload, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptRecordingBaseline(payload, recordingBaselineBinding{
		ScriptID:          scriptID,
		ProjectID:         parent.ProjectID,
		ScriptIncarnation: storage.ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt),
		ActionGraphDigest: actionGraphDigest,
		SchemaVersion:     recordingBaselineSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestCount <= 0 {
		requestCount = len(observations)
	}
	if err := server.store.SaveScriptRecording(&storage.ScriptRecordingRecord{
		ScriptID:                         scriptID,
		ProjectID:                        parent.ProjectID,
		ScriptIncarnation:                storage.ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt),
		ScriptUpdatedAt:                  parent.UpdatedAt,
		SchemaVersion:                    recordingBaselineSchemaVersion,
		ActionGraphDigest:                actionGraphDigest,
		Sensitivity:                      "encrypted_contains_secrets",
		RequestCount:                     requestCount,
		Observations:                     observations,
		EncryptedCorrelationObservations: encrypted,
	}); err != nil {
		t.Fatalf("save encrypted recording baseline: %v", err)
	}
}

func saveRecordingTestRecord(t *testing.T, server *Server, record *storage.ScriptRecordingRecord) {
	t.Helper()
	parent, err := server.store.GetScript(record.ScriptID)
	if err != nil {
		t.Fatalf("load recording parent: %v", err)
	}
	record.ProjectID = parent.ProjectID
	record.ScriptIncarnation = storage.ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt)
	record.ScriptUpdatedAt = parent.UpdatedAt
	if err := server.store.SaveScriptRecording(record); err != nil {
		t.Fatalf("save recording test record: %v", err)
	}
}

func TestScriptRecordingStatusEndpoint(t *testing.T) {
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "s1")

	// Absent baseline → exists:false.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp := httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if out["exists"] != false || out["correlation_usable"] != false || out["correlation_unavailable_reason"] != "baseline_not_found" {
		t.Fatalf("expected exists:false, got %v", out)
	}

	// With a bound baseline → exists:true + request_count.
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	saveEncryptedRecordingTestBaseline(t, server, "s1", digest, 2, []storage.ScriptRecordingObservation{{ID: 0}, {ID: 1}})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp = httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	out = map[string]any{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["exists"] != true || out["request_count"].(float64) != 2 || out["correlation_usable"] != true {
		t.Fatalf("expected exists:true count:2, got %v", out)
	}
	if out["correlation_request_count"].(float64) != 2 || out["correlation_truncated"] != false {
		t.Fatalf("expected complete two-request correlation baseline, got %v", out)
	}
	if _, exists := out["correlation_unavailable_reason"]; exists {
		t.Fatalf("usable baseline unexpectedly included an unavailable reason: %v", out)
	}

	// Legacy rows have no action-graph binding. They remain visible for audit,
	// but automatic correlation must fail closed until a fresh capture binds the
	// observations to the current script.
	saveRecordingTestRecord(t, server, &storage.ScriptRecordingRecord{
		ScriptID:     "s1",
		Source:       "browser_recording",
		Observations: []storage.ScriptRecordingObservation{{ID: 0}},
	})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp = httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	out = map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["exists"] != true || out["correlation_usable"] != false || out["correlation_unavailable_reason"] != "action_graph_unbound" {
		t.Fatalf("legacy baseline did not fail closed: %v", out)
	}

	// The original count remains visible when only part of a recording was
	// retained for correlation, so callers cannot treat a partial comparison as
	// a clean bill of health.
	saveEncryptedRecordingTestBaseline(t, server, "s1", digest, 5, []storage.ScriptRecordingObservation{{ID: 0}, {ID: 1}})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp = httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	out = map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode truncated status: %v", err)
	}
	if out["request_count"].(float64) != 5 || out["correlation_request_count"].(float64) != 2 || out["correlation_truncated"] != true {
		t.Fatalf("expected partial two-of-five baseline status, got %v", out)
	}

	// v1 ciphertext is encrypted but not authenticated to this recording row.
	// It remains recognizable for recapture guidance, never automation input.
	saveRecordingTestRecord(t, server, &storage.ScriptRecordingRecord{
		ScriptID:                         "s1",
		SchemaVersion:                    recordingBaselineSchemaVersion,
		ActionGraphDigest:                digest,
		Sensitivity:                      "encrypted_contains_secrets",
		RequestCount:                     1,
		Observations:                     []storage.ScriptRecordingObservation{{ID: 0}},
		EncryptedCorrelationObservations: recordingBaselineCipherPrefixV1 + base64.RawStdEncoding.EncodeToString(make([]byte, 12+16)),
	})
	req = httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp = httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	out = map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["correlation_usable"] != false || out["correlation_unavailable_reason"] != "legacy_encryption_recapture_required" {
		t.Fatalf("v1 baseline did not require recapture: %v", out)
	}
	if out["correlation_request_count"].(float64) != 1 || out["correlation_truncated"] != false {
		t.Fatalf("v1 baseline lost its retained-count projection: %v", out)
	}
	diagnoseReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s1/diagnose-replay", strings.NewReader(`{"replay_session_id":"legacy-replay"}`))
	diagnoseReq.SetPathValue("id", "s1")
	diagnoseResp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(diagnoseResp, diagnoseReq)
	if diagnoseResp.Code != http.StatusConflict || !strings.Contains(diagnoseResp.Body.String(), "legacy encryption") || !strings.Contains(diagnoseResp.Body.String(), "capture a new recording") {
		t.Fatalf("v1 diagnose = %d body=%s, want recapture conflict", diagnoseResp.Code, diagnoseResp.Body.String())
	}
}

// TestScriptRecordingStatusDoesNotDecryptSidecar proves the polled status
// endpoint serves counts and usability from stored metadata: a v3 sidecar
// remains reportable even when the configured key can no longer decrypt it,
// which would be impossible if the poll path performed a full decrypt.
func TestScriptRecordingStatusDoesNotDecryptSidecar(t *testing.T) {
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "s1")
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	saveEncryptedRecordingTestBaseline(t, server, "s1", digest, 3, []storage.ScriptRecordingObservation{{ID: 0}, {ID: 1}})
	// Rotate the key after capture. A decrypt would now fail, but the status
	// poll must not pay for one; the diagnose path still fails closed.
	t.Setenv(recordingBaselineKeyEnvVar, "rotated-key")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	resp := httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["correlation_usable"] != true || out["correlation_request_count"].(float64) != 2 || out["request_count"].(float64) != 3 || out["correlation_truncated"] != true {
		t.Fatalf("metadata-served status = %v, want usable partial baseline", out)
	}

	// Without any configured key, a v3 sidecar is not readable anywhere, so the
	// status fails closed with the generic unavailable reason.
	t.Setenv(recordingBaselineKeyEnvVar, "")
	resp = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/recording/status", nil)
	req.SetPathValue("id", "s1")
	server.handleGetScriptRecordingStatus(resp, req)
	out = map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["correlation_usable"] != false || out["correlation_unavailable_reason"] != "raw_baseline_unavailable" {
		t.Fatalf("keyless status = %v, want raw_baseline_unavailable", out)
	}
	if out["correlation_request_count"].(float64) != 2 || out["request_count"].(float64) != 3 || out["correlation_truncated"] != true {
		t.Fatalf("keyless status lost its retained-count projection: %v", out)
	}
}

func TestScriptRecordingStatusEndpointUsesMetadataOnlyStorageRead(t *testing.T) {
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "metadata-only-status")
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	saveEncryptedRecordingTestBaseline(
		t,
		server,
		script.ID,
		digest,
		2,
		[]storage.ScriptRecordingObservation{{ID: 0}, {ID: 1}},
	)
	spy := &recordingStatusReadSpy{Store: server.store}
	server.store = spy

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/metadata-only-status/recording/status", nil)
	req.SetPathValue("id", script.ID)
	resp := httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if spy.statusReads != 1 || spy.fullReads != 0 {
		t.Fatalf("status storage reads = metadata:%d full:%d, want 1/0", spy.statusReads, spy.fullReads)
	}
	if !strings.Contains(resp.Body.String(), `"correlation_usable":true`) {
		t.Fatalf("metadata status body = %s", resp.Body.String())
	}
}

func TestScriptRecordingStatusRejectsCrossProjectReplacementSnapshot(t *testing.T) {
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "status-replacement-race")
	originalStore := server.store
	server.store = &recordingStatusReadSpy{
		Store: originalStore,
		statusRecordOverride: &storage.ScriptRecordingStatusRecord{
			ScriptID:                    "status-replacement-race",
			ProjectID:                   "p2",
			ScriptIncarnation:           "sha256:foreign-replacement",
			ActionGraphDigest:           "sha256:foreign-graph",
			Source:                      "private-foreign-source",
			Sensitivity:                 "private-foreign-sensitivity",
			RequestCount:                99,
			CorrelationObservationCount: 99,
			CorrelationSidecarVersion:   storage.ScriptRecordingSidecarV3,
			CorrelationMetadataCurrent:  true,
			UpdatedAt:                   time.Now().UTC(),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/status-replacement-race/recording/status", nil)
	req.SetPathValue("id", "status-replacement-race")
	bindAuthorizedProjectToRequest(req, "p1")
	resp := httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("replacement-race status = %d body=%s, want 404", resp.Code, resp.Body.String())
	}
	for _, leaked := range []string{"private-foreign-source", "private-foreign-sensitivity", `"request_count":99`} {
		if strings.Contains(resp.Body.String(), leaked) {
			t.Fatalf("replacement-race response leaked %q: %s", leaked, resp.Body.String())
		}
	}
}

func TestScriptRecordingDiagnosisRejectsCrossProjectReplacementSnapshot(t *testing.T) {
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "diagnosis-replacement-race")
	originalStore := server.store
	server.store = &recordingStatusReadSpy{
		Store: originalStore,
		fullRecordOverride: &storage.ScriptRecordingRecord{
			ScriptID:                         "diagnosis-replacement-race",
			ProjectID:                        "p2",
			ScriptIncarnation:                "sha256:foreign-replacement",
			ActionGraphDigest:                "sha256:foreign-graph",
			Source:                           "private-foreign-source",
			Sensitivity:                      "private-foreign-sensitivity",
			RequestCount:                     99,
			EncryptedCorrelationObservations: recordingBaselineCipherPrefixV3 + base64.RawStdEncoding.EncodeToString(make([]byte, 12+16)),
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/diagnosis-replacement-race/diagnose-replay",
		strings.NewReader(`{"replay_session_id":"foreign-session"}`),
	)
	req.SetPathValue("id", "diagnosis-replacement-race")
	bindAuthorizedProjectToRequest(req, "p1")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("replacement-race diagnosis = %d body=%s, want 404", resp.Code, resp.Body.String())
	}
	for _, leaked := range []string{"private-foreign-source", "private-foreign-sensitivity", "foreign-session"} {
		if strings.Contains(resp.Body.String(), leaked) {
			t.Fatalf("replacement-race diagnosis leaked %q: %s", leaked, resp.Body.String())
		}
	}
}

func TestBaselineSidecarStatusRejectsMalformedRecognizedPrefix(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-fixture-key")
	record := &storage.ScriptRecordingStatusRecord{
		SchemaVersion:               recordingBaselineSchemaVersion,
		CorrelationMetadataCurrent:  true,
		CorrelationObservationCount: 1,
		CorrelationSidecarVersion:   99,
	}
	retained, err := baselineSidecarStatus(record)
	if err == nil || retained != 0 || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("baselineSidecarStatus = (%d, %v), want malformed/zero", retained, err)
	}
}

func TestRecordingBaselineUnknownSchemaFailsClosed(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-fixture-key")
	const futureSchema = "apexload.recording.v999"
	binding := recordingBaselineBinding{
		ScriptID:          "future-schema-recording",
		ProjectID:         "p1",
		ScriptIncarnation: "sha256:future-incarnation",
		ActionGraphDigest: "sha256:future-graph",
		SchemaVersion:     futureSchema,
	}
	payload, err := json.Marshal([]storage.ScriptRecordingObservation{{
		ID:           0,
		ResponseBody: `{"token":"future-secret"}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptRecordingBaseline(payload, binding)
	if err != nil {
		t.Fatal(err)
	}

	statusRecord := &storage.ScriptRecordingStatusRecord{
		SchemaVersion:               futureSchema,
		CorrelationObservationCount: 1,
		CorrelationSidecarVersion:   storage.ScriptRecordingSidecarV3,
		CorrelationMetadataCurrent:  true,
	}
	if retained, err := baselineSidecarStatus(statusRecord); err == nil || retained != 0 || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("future-schema status = (%d, %v), want fail-closed schema error", retained, err)
	}

	fullRecord := &storage.ScriptRecordingRecord{
		ScriptID:                         binding.ScriptID,
		ProjectID:                        binding.ProjectID,
		ScriptIncarnation:                binding.ScriptIncarnation,
		ActionGraphDigest:                binding.ActionGraphDigest,
		SchemaVersion:                    binding.SchemaVersion,
		EncryptedCorrelationObservations: encrypted,
	}
	if observations, err := baselineObservations(fullRecord); err == nil || observations != nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("future-schema baseline = (%#v, %v), want fail-closed schema error", observations, err)
	}

	legacyStatus := &storage.ScriptRecordingStatusRecord{
		SchemaVersion:               "apexload.recording.v2",
		CorrelationObservationCount: 1,
		CorrelationSidecarVersion:   storage.ScriptRecordingSidecarV1,
		CorrelationMetadataCurrent:  true,
	}
	if retained, err := baselineSidecarStatus(legacyStatus); retained != 1 || !errors.Is(err, errRecordingBaselineCiphertextUnbound) {
		t.Fatalf("legacy-schema status = (%d, %v), want precise recapture requirement", retained, err)
	}
	legacyRecord := &storage.ScriptRecordingRecord{
		SchemaVersion: "apexload.recording.v2",
		EncryptedCorrelationObservations: recordingBaselineCipherPrefixV1 +
			base64.RawStdEncoding.EncodeToString(make([]byte, 12+16)),
	}
	if observations, err := baselineObservations(legacyRecord); observations != nil || !errors.Is(err, errRecordingBaselineCiphertextUnbound) {
		t.Fatalf("legacy-schema baseline = (%#v, %v), want precise recapture requirement", observations, err)
	}
}

func TestScriptRecordingAndDiagnosisResolveRefKeyToCanonicalID(t *testing.T) {
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "canonical-recording-script")
	stored, ok := server.runner.GetScript(script.ID)
	if !ok || stored == nil || stored.RefKey == "" {
		t.Fatalf("stored script has no reference key: %#v", stored)
	}
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	observations := []storage.ScriptRecordingObservation{{ID: 0, ResponseBody: `{"token":"recorded-token"}`}}
	saveEncryptedRecordingTestBaseline(t, server, stored.ID, digest, 1, observations)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+stored.RefKey+"/recording/status", nil)
	statusReq.SetPathValue("id", stored.RefKey)
	statusResp := httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(statusResp, statusReq)
	if statusResp.Code != http.StatusOK || !strings.Contains(statusResp.Body.String(), `"correlation_usable":true`) {
		t.Fatalf("reference-key status = %d body=%s, want canonical baseline", statusResp.Code, statusResp.Body.String())
	}

	completedAt := time.Now().UTC()
	replay := &ScriptDebugSession{
		ID:                           "canonical-recording-replay",
		ProjectID:                    script.ProjectID,
		ScriptID:                     stored.ID,
		Status:                       ScriptDebugCompleted,
		CompletedAt:                  &completedAt,
		FinalReport:                  &engine.ReplayReport{Success: true},
		correlationActionGraphDigest: digest,
		correlationObservations:      []correlate.Observation{{ID: 0, ResponseBody: `{"token":"recorded-token"}`}},
	}
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[replay.ID] = replay
	server.scriptDebug.mu.Unlock()

	diagnoseReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.RefKey+"/diagnose-replay", strings.NewReader(`{"replay_session_id":"`+replay.ID+`"}`))
	diagnoseReq.SetPathValue("id", stored.RefKey)
	diagnoseResp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(diagnoseResp, diagnoseReq)
	if diagnoseResp.Code != http.StatusOK {
		t.Fatalf("reference-key diagnosis = %d body=%s, want canonical replay/baseline match", diagnoseResp.Code, diagnoseResp.Body.String())
	}
}

func TestScriptRecordingEndpointsEnforceScriptProjectBeforeEvidenceLookup(t *testing.T) {
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "project-bound-script")

	t.Run("status cross project", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/project-bound-script/recording/status", nil)
		req.SetPathValue("id", "project-bound-script")
		bindAuthorizedProjectToRequest(req, "p2")
		resp := httptest.NewRecorder()
		server.handleGetScriptRecordingStatus(resp, req)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
		}
	})

	t.Run("diagnose cross project", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/project-bound-script/diagnose-replay", strings.NewReader(`{"recorded":[{"id":1}],"replay":[{"id":1}]}`))
		req.SetPathValue("id", "project-bound-script")
		bindAuthorizedProjectToRequest(req, "p2")
		resp := httptest.NewRecorder()
		server.handleDiagnoseScriptReplay(resp, req)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
		}
	})

	for _, endpoint := range []struct {
		name   string
		method string
		path   string
		body   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "status missing", method: http.MethodGet, path: "/api/v1/scripts/missing/recording/status", handle: server.handleGetScriptRecordingStatus},
		{name: "diagnose missing", method: http.MethodPost, path: "/api/v1/scripts/missing/diagnose-replay", body: `{}`, handle: server.handleDiagnoseScriptReplay},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
			req.SetPathValue("id", "missing")
			bindAuthorizedProjectToRequest(req, "p2")
			resp := httptest.NewRecorder()
			endpoint.handle(resp, req)
			if resp.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
			}
		})
	}
}

func TestKeylessScrubbedRecordingStatusIsNotCorrelationUsableAndDiagnoseConflicts(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "")
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "keyless-script")
	if err := server.persistRecordedBaseline("keyless-script", "proxy_recording", script, []correlate.Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-AAA-1111-recorded"}`},
		{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded"},
	}); err != nil {
		t.Fatalf("persist keyless baseline: %v", err)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/keyless-script/recording/status", nil)
	statusReq.SetPathValue("id", "keyless-script")
	statusResp := httptest.NewRecorder()
	server.handleGetScriptRecordingStatus(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(statusResp.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["exists"] != true || status["correlation_usable"] != false || status["correlation_unavailable_reason"] != "raw_baseline_unavailable" {
		t.Fatalf("keyless baseline status did not expose correlation unavailability: %v", status)
	}

	diagnoseReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/keyless-script/diagnose-replay", strings.NewReader(`{"replay_session_id":"keyless-replay"}`))
	diagnoseReq.SetPathValue("id", "keyless-script")
	diagnoseResp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(diagnoseResp, diagnoseReq)
	if diagnoseResp.Code != http.StatusConflict {
		t.Fatalf("diagnose keyless scrubbed baseline = %d, want 409; body=%s", diagnoseResp.Code, diagnoseResp.Body.String())
	}
}

func TestDiagnoseScriptReplayRejectsStoredBaselineWithClientReplay(t *testing.T) {
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "s1")

	// A stored baseline may never be paired with caller-controlled replay data,
	// regardless of whether that script currently has a baseline.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s1/diagnose-replay", strings.NewReader(`{"replay":[]}`))
	req.SetPathValue("id", "s1")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "completed replay session") {
		t.Fatalf("stored/client pairing = %d body=%s, want 400", resp.Code, resp.Body.String())
	}

	// Seed a baseline where step 1's response carries a CSRF reused by step 2.
	saveRecordingTestRecord(t, server, &storage.ScriptRecordingRecord{
		ScriptID: "s1",
		Observations: []storage.ScriptRecordingObservation{
			{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-AAA-1111-recorded"}`},
			{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded"},
		},
	})

	// A chosen replay remains rejected after a secret-bearing baseline exists;
	// otherwise structural repair offsets can be used as a value oracle.
	body := `{"replay":[{"id":1,"response_body":"{\"csrf_token\":\"TOKEN-BBB-2222-replayed\"}"},{"id":2,"request_body":"csrf=TOKEN-AAA-1111-recorded"}]}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s1/diagnose-replay", strings.NewReader(body))
	req.SetPathValue("id", "s1")
	resp = httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "completed replay session") {
		t.Fatalf("secret baseline/client replay pairing = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
}

func TestDiagnoseScriptReplayUsesPrivateSessionEvidenceWithoutDisclosure(t *testing.T) {
	const (
		recordedToken = "TOKEN-AAA-1111-recorded"
		replayToken   = "TOKEN-BBB-2222-replayed"
	)
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "s-private")
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	saveEncryptedRecordingTestBaseline(t, server, "s-private", digest, 2, []storage.ScriptRecordingObservation{
		{ID: 1, ResponseBody: `{"csrf_token":"` + recordedToken + `"}`},
		{ID: 2, RequestBody: "csrf=" + recordedToken},
	})

	completedAt := time.Now().UTC()
	session := &ScriptDebugSession{
		ID:                           "private-replay",
		ScriptID:                     "s-private",
		ProjectID:                    "p1",
		Status:                       ScriptDebugCompleted,
		CompletedAt:                  &completedAt,
		FinalReport:                  &engine.ReplayReport{Success: true},
		correlationActionGraphDigest: digest,
	}
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[session.ID] = session
	server.scriptDebug.mu.Unlock()
	controller := scriptDebugController{manager: server.scriptDebug, session: session}
	controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 1}, &engine.ReplayActionEvent{
		Phase:       "action",
		ActionIndex: 1,
		CorrelationEvidence: &protocol.CorrelationEvidence{
			ResponseBody:   `{"csrf_token":"` + replayToken + `"}`,
			ResponseStatus: http.StatusOK,
		},
	})
	controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 2}, &engine.ReplayActionEvent{
		Phase:       "action",
		ActionIndex: 2,
		CorrelationEvidence: &protocol.CorrelationEvidence{
			RequestBody: "csrf=" + recordedToken,
		},
	})

	publicSession, ok := server.scriptDebug.Get(session.ID)
	if !ok {
		t.Fatal("private replay session was not found")
	}
	publicJSON, err := json.Marshal(publicSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{recordedToken, replayToken} {
		if strings.Contains(string(publicJSON), token) {
			t.Fatalf("public debug session disclosed raw token %q: %s", token, publicJSON)
		}
	}
	exported, _, ok := server.scriptDebug.Export(session.ID, "json")
	if !ok {
		t.Fatal("debug session export was unavailable")
	}
	for _, token := range []string{recordedToken, replayToken} {
		if strings.Contains(string(exported), token) {
			t.Fatalf("debug session export disclosed raw token %q: %s", token, exported)
		}
	}

	// The compatibility replay array is intentionally duplicate and scrubbed.
	// replay_session_id selects the private evidence path, so the client array is
	// neither trusted nor allowed to block the safe server-side diagnosis.
	body := `{"replay_session_id":"private-replay","replay":[{"id":9},{"id":9}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s-private/diagnose-replay", strings.NewReader(body))
	req.SetPathValue("id", "s-private")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	for _, token := range []string{recordedToken, replayToken} {
		if strings.Contains(resp.Body.String(), token) {
			t.Fatalf("diagnose response disclosed raw token %q: %s", token, resp.Body.String())
		}
	}
	var out correlationDiagnoseResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Blocking || out.Counts.Errors != 1 || len(out.Suggestions) != 1 || out.Suggestions[0].FailingStepID != 2 {
		t.Fatalf("private replay evidence did not drive stale-token diagnosis: %#v", out)
	}
}

func TestDiagnoseScriptReplayMasksForeignSessionAsNotFound(t *testing.T) {
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "project-bound-diagnosis")
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	saveEncryptedRecordingTestBaseline(t, server, script.ID, digest, 1, []storage.ScriptRecordingObservation{{
		ID: 0, ResponseBody: `{"token":"recorded-token"}`,
	}})

	completedAt := time.Now().UTC()
	foreign := &ScriptDebugSession{
		ID:                           "foreign-replay",
		ProjectID:                    "p2",
		ScriptID:                     script.ID,
		Status:                       ScriptDebugCompleted,
		CompletedAt:                  &completedAt,
		FinalReport:                  &engine.ReplayReport{Success: true},
		correlationActionGraphDigest: digest,
		correlationObservations:      []correlate.Observation{{ID: 0, ResponseBody: `{"token":"foreign-token"}`}},
	}
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[foreign.ID] = foreign
	server.scriptDebug.mu.Unlock()

	probe := func(sessionID string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+script.ID+"/diagnose-replay", strings.NewReader(`{"replay_session_id":"`+sessionID+`"}`))
		req.SetPathValue("id", script.ID)
		bindAuthorizedProjectToRequest(req, "p1")
		resp := httptest.NewRecorder()
		server.handleDiagnoseScriptReplay(resp, req)
		return resp.Code, resp.Body.String()
	}

	wantStatus, wantBody := probe("missing-replay")
	if wantStatus != http.StatusNotFound {
		t.Fatalf("missing replay status = %d body=%s, want 404", wantStatus, wantBody)
	}
	status, body := probe(foreign.ID)
	if status != wantStatus || body != wantBody {
		t.Fatalf("foreign replay response = %d %q, want absent response %d %q", status, body, wantStatus, wantBody)
	}
}

func TestDiagnoseScriptReplayRejectsUnboundAndChangedActionGraphs(t *testing.T) {
	server := recordingTestServer(t)
	const scriptID = "graph-bound-script"
	script := storeRecordingDigestTestScript(t, server, scriptID)
	oldDigest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	observations := []storage.ScriptRecordingObservation{{ID: 0, RequestURL: "/login"}}

	// A pre-migration recording remains visible, but cannot feed an automatic
	// diagnosis because no durable action graph proves what its IDs mean.
	saveRecordingTestRecord(t, server, &storage.ScriptRecordingRecord{ScriptID: scriptID, Observations: observations})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+scriptID+"/diagnose-replay", strings.NewReader(`{"replay_session_id":"old-replay"}`))
	req.SetPathValue("id", scriptID)
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "not bound") {
		t.Fatalf("legacy graph status = %d body=%s, want bounded 409 recapture guidance", resp.Code, resp.Body.String())
	}

	saveRecordingTestRecord(t, server, &storage.ScriptRecordingRecord{
		ScriptID:          scriptID,
		ActionGraphDigest: oldDigest,
		Observations:      observations,
	})
	changed := *script
	changed.Actions = append([]dsl.Action(nil), script.Actions...)
	changed.Actions[1].Body = `{"edited":true}`
	if _, ok := server.runner.UpdateScript(scriptID, &changed); !ok {
		t.Fatal("failed to update script action graph")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+scriptID+"/diagnose-replay", strings.NewReader(`{"replay_session_id":"old-replay"}`))
	req.SetPathValue("id", scriptID)
	resp = httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "action graph changed") || !strings.Contains(resp.Body.String(), "capture a new recording") {
		t.Fatalf("changed baseline graph status = %d body=%s, want explicit bounded 409", resp.Code, resp.Body.String())
	}

	current, ok := server.runner.GetScript(scriptID)
	if !ok || current == nil || current.Script == nil {
		t.Fatal("updated script is missing")
	}
	newDigest, err := scriptCorrelationActionGraphDigest(current.Script)
	if err != nil {
		t.Fatal(err)
	}
	saveEncryptedRecordingTestBaseline(t, server, scriptID, newDigest, len(observations), observations)
	completedAt := time.Now().UTC()
	session := &ScriptDebugSession{
		ID:                           "old-replay",
		ScriptID:                     scriptID,
		ProjectID:                    "p1",
		Status:                       ScriptDebugCompleted,
		CompletedAt:                  &completedAt,
		FinalReport:                  &engine.ReplayReport{Success: true},
		correlationActionGraphDigest: oldDigest,
	}
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[session.ID] = session
	server.scriptDebug.mu.Unlock()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+scriptID+"/diagnose-replay", strings.NewReader(`{"replay_session_id":"old-replay"}`))
	req.SetPathValue("id", scriptID)
	resp = httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "action graph changed after this replay started") || !strings.Contains(resp.Body.String(), "replay it again") {
		t.Fatalf("stale replay graph status = %d body=%s, want explicit bounded 409", resp.Code, resp.Body.String())
	}
}

func TestDiagnoseScriptReplayRejectsUploadedBaselineWithPrivateSessionEvidence(t *testing.T) {
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "s-uploaded")
	session := &ScriptDebugSession{ID: "uploaded-replay", ScriptID: "s-uploaded", Status: ScriptDebugCompleted}
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[session.ID] = session
	server.scriptDebug.mu.Unlock()

	payload := map[string]any{
		"replay_session_id": session.ID,
		"recorded": []correlationStep{
			{ID: 1, ResponseBody: `{"csrf_token":"UPLOAD-AAA-1111-recorded"}`},
			{ID: 2, RequestBody: "csrf=UPLOAD-AAA-1111-recorded"},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s-uploaded/diagnose-replay", strings.NewReader(string(body)))
	req.SetPathValue("id", "s-uploaded")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "cannot be combined") {
		t.Fatalf("uploaded/private pairing = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
}

func TestDiagnoseScriptReplayAllowsUploadedBaselineWithClientReplay(t *testing.T) {
	const (
		recordedToken = "UPLOAD-AAA-1111-recorded"
		replayToken   = "UPLOAD-BBB-2222-replayed"
	)
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "s-uploaded")
	payload := map[string]any{
		"recorded": []correlationStep{
			{ID: 1, ResponseBody: `{"csrf_token":"` + recordedToken + `"}`},
			{ID: 2, RequestBody: "csrf=" + recordedToken},
		},
		"replay": []correlationStep{
			{ID: 1, ResponseBody: `{"csrf_token":"` + replayToken + `"}`},
			{ID: 2, RequestBody: "csrf=" + recordedToken},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s-uploaded/diagnose-replay", strings.NewReader(string(body)))
	req.SetPathValue("id", "s-uploaded")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	for _, token := range []string{recordedToken, replayToken} {
		if strings.Contains(resp.Body.String(), token) {
			t.Fatalf("client/client diagnosis disclosed token %q: %s", token, resp.Body.String())
		}
	}
	var out correlationDiagnoseResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Blocking || out.Counts.Errors != 1 || out.ComparedRecordedSteps != 2 || out.TotalRecordedSteps != 2 || out.Truncated {
		t.Fatalf("client/client diagnosis = %#v, want one blocking repair and complete 2/2 coverage", out)
	}
}

func TestDiagnoseScriptReplayValidatesUploadedBaseline(t *testing.T) {
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "s-uploaded")
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "empty", body: `{"recorded":[],"replay":[]}`, want: "recorded steps are required"},
		{name: "duplicate ids", body: `{"recorded":[{"id":3},{"id":3}],"replay":[]}`, want: "duplicate id 3"},
		{name: "single nested leaf", body: `{"recorded":[{"id":3}],"replay":[{"id":3,"step_path":["requests[0]"]}]}`, want: "nested replay leaf"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s-uploaded/diagnose-replay", strings.NewReader(test.body))
			req.SetPathValue("id", "s-uploaded")
			resp := httptest.NewRecorder()
			server.handleDiagnoseScriptReplay(resp, req)
			if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), test.want) {
				t.Fatalf("status = %d body=%s, want 400 containing %q", resp.Code, resp.Body.String(), test.want)
			}
		})
	}

	tooMany := make([]correlationStep, maxRecordedBaselineObservations+1)
	for index := range tooMany {
		tooMany[index] = correlationStep{ID: index}
	}
	body, err := json.Marshal(map[string]any{"recorded": tooMany, "replay": []correlationStep{}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s-uploaded/diagnose-replay", strings.NewReader(string(body)))
	req.SetPathValue("id", "s-uploaded")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized uploaded baseline status = %d body=%s, want 413", resp.Code, resp.Body.String())
	}

	tooManyReplay := make([]correlationStep, maxRecordedBaselineObservations+1)
	for index := range tooManyReplay {
		tooManyReplay[index] = correlationStep{ID: index}
	}
	body, err = json.Marshal(map[string]any{
		"recorded": []correlationStep{{ID: -1}},
		"replay":   tooManyReplay,
	})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s-uploaded/diagnose-replay", strings.NewReader(string(body)))
	req.SetPathValue("id", "s-uploaded")
	resp = httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge || !strings.Contains(resp.Body.String(), "replay correlation evidence") {
		t.Fatalf("oversized uploaded replay status = %d body=%s, want 413", resp.Code, resp.Body.String())
	}
}

func TestDiagnoseScriptReplayRejectsCandidateOverflow(t *testing.T) {
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "work-limit")
	var overflowBody strings.Builder
	overflowBody.WriteByte('{')
	for index := 0; index <= maxCorrelationCandidatesPerObservationForAPITest(); index++ {
		if index > 0 {
			overflowBody.WriteByte(',')
		}
		fmt.Fprintf(&overflowBody, `"token_%d":"token-value-%06d"`, index, index)
	}
	overflowBody.WriteByte('}')
	body, err := json.Marshal(map[string]any{
		"recorded": []correlationStep{{ID: 1}},
		"replay":   []correlationStep{{ID: 1, ResponseBody: overflowBody.String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/work-limit/diagnose-replay", strings.NewReader(string(body)))
	req.SetPathValue("id", "work-limit")
	resp := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(resp, req)
	if resp.Code != http.StatusUnprocessableEntity || !strings.Contains(resp.Body.String(), "work limits") {
		t.Fatalf("candidate overflow = status %d body=%s, want fail-closed 422 work-limit response", resp.Code, resp.Body.String())
	}
}

func TestScriptDebugCorrelationEvidenceFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		scriptID string
		add      func(scriptDebugController)
		lookupID string
		want     error
	}{
		{
			name:     "different script",
			scriptID: "script-a",
			lookupID: "script-b",
			add: func(controller scriptDebugController) {
				controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 0}, &engine.ReplayActionEvent{Phase: "action", ActionIndex: 0, CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "/"}})
			},
			want: errScriptDebugCorrelationScriptMismatch,
		},
		{
			name:     "duplicate top-level action id",
			scriptID: "script-a",
			lookupID: "script-a",
			add: func(controller scriptDebugController) {
				for range 2 {
					controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 0}, &engine.ReplayActionEvent{Phase: "action", ActionIndex: 0, CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "/"}})
				}
			},
			want: errScriptDebugCorrelationUnavailable,
		},
		{
			name:     "single nested leaf",
			scriptID: "script-a",
			lookupID: "script-a",
			add: func(controller scriptDebugController) {
				requestIndex := 0
				controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{
					Phase: "action", ActionIndex: 0, ParentActionName: "sequence", RequestIndex: &requestIndex, StepPath: []string{"requests[0]"},
				}, &engine.ReplayActionEvent{
					Phase: "action", ActionIndex: 0, ParentActionName: "sequence", RequestIndex: &requestIndex, StepPath: []string{"requests[0]"},
					CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "/nested"},
				})
			},
			want: errScriptDebugCorrelationUnavailable,
		},
		{
			name:     "unsupported phase",
			scriptID: "script-a",
			lookupID: "script-a",
			add: func(controller scriptDebugController) {
				controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "init", ActionIndex: 0}, &engine.ReplayActionEvent{Phase: "init", ActionIndex: 0, CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "/bootstrap"}})
			},
			want: errScriptDebugCorrelationUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewScriptDebugManager(nil)
			const actionGraphDigest = "sha256:test-action-graph"
			completedAt := time.Now().UTC()
			session := &ScriptDebugSession{
				ID: "session", ScriptID: test.scriptID, Status: ScriptDebugCompleted,
				CompletedAt: &completedAt, FinalReport: &engine.ReplayReport{Success: true},
				correlationActionGraphDigest: actionGraphDigest,
			}
			manager.sessions[session.ID] = session
			test.add(scriptDebugController{manager: manager, session: session})
			observations, err := manager.CorrelationObservations(session.ID, session.ProjectID, test.lookupID, actionGraphDigest)
			if !errors.Is(err, test.want) || observations != nil {
				t.Fatalf("CorrelationObservations = %#v, %v; want nil, %v", observations, err, test.want)
			}
		})
	}
}

func TestScriptDebugCorrelationEvidenceMasksProjectBeforeScriptAndGraph(t *testing.T) {
	manager := NewScriptDebugManager(nil)
	session := &ScriptDebugSession{
		ID:                           "foreign-session",
		ProjectID:                    "project-b",
		ScriptID:                     "foreign-script",
		Status:                       ScriptDebugRunning,
		correlationActionGraphDigest: "sha256:foreign-graph",
	}
	manager.sessions[session.ID] = session

	observations, err := manager.CorrelationObservations(session.ID, "project-a", "different-script", "sha256:different-graph")
	if observations != nil || !errors.Is(err, errScriptDebugCorrelationSessionNotFound) {
		t.Fatalf("CorrelationObservations = %#v, %v; want masked session-not-found", observations, err)
	}
}

func TestScriptDebugCorrelationEvidenceRequiresFinishedReplayArtifacts(t *testing.T) {
	const digest = "sha256:finished-replay"
	now := time.Now().UTC()
	tests := []struct {
		name    string
		status  string
		ended   *time.Time
		report  *engine.ReplayReport
		wantErr error
	}{
		{name: "running", status: ScriptDebugRunning, wantErr: errScriptDebugCorrelationSessionActive},
		{name: "stopped", status: ScriptDebugStopped, wantErr: errScriptDebugCorrelationUnavailable},
		{name: "completed without timestamp", status: ScriptDebugCompleted, report: &engine.ReplayReport{Success: true}, wantErr: errScriptDebugCorrelationUnavailable},
		{name: "failed without report", status: ScriptDebugFailed, ended: &now, wantErr: errScriptDebugCorrelationUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewScriptDebugManager(nil)
			session := &ScriptDebugSession{
				ID: "session", ScriptID: "script", Status: test.status, CompletedAt: test.ended, FinalReport: test.report,
				correlationActionGraphDigest: digest,
				correlationObservations:      []correlate.Observation{{ID: 0, RequestURL: "/"}},
			}
			manager.sessions[session.ID] = session
			observations, err := manager.CorrelationObservations(session.ID, session.ProjectID, session.ScriptID, digest)
			if observations != nil || !errors.Is(err, test.wantErr) {
				t.Fatalf("CorrelationObservations = %#v, %v; want nil, %v", observations, err, test.wantErr)
			}
		})
	}
}

func TestScriptDebugStoppedSessionClearsEvidenceAndRejectsInflightObserver(t *testing.T) {
	manager := NewScriptDebugManager(nil)
	const digest = "sha256:stopped-replay"
	session := &ScriptDebugSession{
		ID: "session", ScriptID: "script", Status: ScriptDebugRunning, cancel: func() {},
		correlationActionGraphDigest: digest,
	}
	manager.sessions[session.ID] = session
	controller := scriptDebugController{manager: manager, session: session}
	controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 0}, &engine.ReplayActionEvent{
		Phase: "action", ActionIndex: 0, CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "/before-stop"},
	})
	if len(session.correlationObservations) != 1 {
		t.Fatalf("pre-stop observations = %d, want 1", len(session.correlationObservations))
	}
	if _, ok := manager.Stop(session.ID); !ok {
		t.Fatal("Stop did not find session")
	}
	controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 1}, &engine.ReplayActionEvent{
		Phase: "action", ActionIndex: 1, CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "/in-flight"},
	})
	session.mu.RLock()
	observationCount := len(session.correlationObservations)
	evidenceErr := session.correlationEvidenceErr
	session.mu.RUnlock()
	if observationCount != 0 || evidenceErr == "" {
		t.Fatalf("stopped evidence = observations:%d error:%q, want cleared and rejected", observationCount, evidenceErr)
	}
	if observations, err := manager.CorrelationObservations(session.ID, session.ProjectID, session.ScriptID, digest); observations != nil || !errors.Is(err, errScriptDebugCorrelationUnavailable) {
		t.Fatalf("stopped CorrelationObservations = %#v, %v", observations, err)
	}
}

func TestScriptDebugLeaseIntegrityFailureClearsCorrelationEvidence(t *testing.T) {
	now := time.Now()
	guard := newReplaySessionLeaseSignal()
	guard.setValidUntil(now.Add(-time.Millisecond))
	manager := NewScriptDebugManager(nil)
	session := &ScriptDebugSession{
		ID:                        "lease-lost",
		ScriptID:                  "script",
		Status:                    ScriptDebugRunning,
		cancel:                    func() {},
		reservation:               guard,
		correlationObservations:   []correlate.Observation{{ID: 0, RequestURL: "/private"}},
		correlationEvidenceBytes:  128,
		correlationObservationIDs: map[int]struct{}{0: {}},
	}

	session.mu.Lock()
	manager.cancelSessionLocked(session, now, "lease expired")
	session.mu.Unlock()

	if session.Status != ScriptDebugFailed || !strings.Contains(session.Error, errReplaySessionLeaseLost.Error()) {
		t.Fatalf("session status/error = %q/%q, want lease-integrity failure", session.Status, session.Error)
	}
	if len(session.correlationObservations) != 0 || session.correlationEvidenceBytes != 0 || session.correlationEvidenceErr == "" {
		t.Fatalf("lease-lost evidence was not rejected: %#v", session)
	}
}

func TestScriptDebugUnavailableMarkerRejectsWholeSession(t *testing.T) {
	manager := NewScriptDebugManager(nil)
	const digest = "sha256:oversized-replay"
	session := &ScriptDebugSession{ID: "session", ScriptID: "script", Status: ScriptDebugRunning, correlationActionGraphDigest: digest}
	manager.sessions[session.ID] = session
	controller := scriptDebugController{manager: manager, session: session}
	controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 0}, &engine.ReplayActionEvent{
		Phase: "action", ActionIndex: 0, CorrelationEvidence: &protocol.CorrelationEvidence{RequestURL: "/retained"},
	})
	controller.ObserveCorrelationEvidence(context.Background(), engine.ReplayDebugAction{Phase: "action", ActionIndex: 1}, &engine.ReplayActionEvent{
		Phase: "action", ActionIndex: 1, CorrelationEvidence: protocol.UnavailableCorrelationEvidence(protocol.CorrelationEvidenceTooLargeReason),
	})
	now := time.Now().UTC()
	session.mu.Lock()
	session.Status = ScriptDebugFailed
	session.CompletedAt = &now
	session.FinalReport = &engine.ReplayReport{Success: false}
	session.mu.Unlock()
	if observations, err := manager.CorrelationObservations(session.ID, session.ProjectID, session.ScriptID, digest); observations != nil || !errors.Is(err, errScriptDebugCorrelationUnavailable) || !strings.Contains(err.Error(), protocol.CorrelationEvidenceTooLargeReason) {
		t.Fatalf("unavailable marker CorrelationObservations = %#v, %v", observations, err)
	}
}

func TestScriptDebugAccepted404ExtractorFailureDoesNotDiagnoseServerRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"expected miss"}`))
	}))
	defer server.Close()

	manager := NewScriptDebugManager(nil)
	script := &dsl.Script{
		ID: "accepted-404", Name: "accepted 404", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL},
		Actions: []dsl.Action{{
			Name: "missing", Type: "GET", Method: http.MethodGet, Target: "/missing",
			Assertions: []dsl.Assertion{{Type: "status_code", Expected: http.StatusNotFound}},
			Extractors: []dsl.Extractor{{Name: "token", Source: "body", Type: "regex", Expression: `"token":"([^"]+)"`, OnMiss: "fail"}},
		}},
	}
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.StartChecked(script.ID, script, ScriptDebugStartRequest{ReplaySessionID: "accepted-404-session"})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForScriptDebugStatus(t, manager, session.ID, ScriptDebugFailed)
	if finished.FinalReport == nil || len(finished.FinalReport.Events) != 1 || finished.FinalReport.Events[0].Success {
		t.Fatalf("finished replay = %#v, want extractor-failed action", finished.FinalReport)
	}
	observations, err := manager.CorrelationObservations(session.ID, script.ProjectID, script.ID, digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].ResponseSuccess == nil || !*observations[0].ResponseSuccess {
		t.Fatalf("correlation observations = %#v, want accepted response outcome", observations)
	}
	for _, suggestion := range correlate.DiagnoseReplay(nil, observations) {
		if suggestion.RuleID == "replay_rejected_unexplained" {
			t.Fatalf("accepted 404 was misdiagnosed as a server rejection: %#v", suggestion)
		}
	}
}
