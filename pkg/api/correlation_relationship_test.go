package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCorrelationDiagnosisRelationshipCoverageAndIndependentAssertions(t *testing.T) {
	server := NewServer(ServerConfig{})
	failed := false
	recorded := []correlationStep{{ID: 1, ResponseBody: `{"csrf_token":"SYNTHETIC-RECORDED-123456"}`}, {ID: 2, RequestBody: "csrf=SYNTHETIC-RECORDED-123456"}, {ID: 3, RequestBody: "csrf=SYNTHETIC-RECORDED-123456"}}
	tests := []struct {
		name                      string
		recorded, replay          []correlationStep
		complete                  bool
		missing, warnings, errors int
	}{
		{"stale and no stale", recorded, []correlationStep{{ID: 1, ResponseBody: `{"csrf_token":"SYNTHETIC-REPLAYED-654321"}`}, {ID: 2, RequestBody: recorded[1].RequestBody, ResponseStatus: 200, ResponseSuccess: &failed}, {ID: 3, RequestBody: "csrf=SYNTHETIC-REPLAYED-654321", ResponseStatus: 204}}, true, 0, 1, 1},
		{"missing consumer", recorded, recorded[:2], false, 1, 1, 0},
		{"zero advice incomplete", []correlationStep{{ID: 1}, {ID: 2}}, []correlationStep{{ID: 1}}, false, 0, 0, 0},
		{"zero detected remains unverified", []correlationStep{{ID: 1}}, []correlationStep{{ID: 1}}, false, 0, 0, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(correlationDiagnoseRequest{Recorded: test.recorded, Replay: test.replay})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(payload))
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("diagnosis status=%d", response.Code)
			}
			for _, private := range []string{"SYNTHETIC-RECORDED", "SYNTHETIC-REPLAYED"} {
				if strings.Contains(response.Body.String(), private) {
					t.Fatal("diagnosis coverage disclosed private literals")
				}
			}
			var result correlationDiagnoseResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.RelationshipCoverage.Complete != test.complete || result.RelationshipCoverage.MissingConsumers != test.missing || result.Counts.Warnings != test.warnings || result.Counts.Errors != test.errors {
				t.Fatal("diagnosis hid missing evidence or independent assertion failure")
			}
			if test.errors > 0 {
				if result.RelationshipCoverage.ProducerCount != 1 || result.RelationshipCoverage.ConsumerCount != 2 || result.RelationshipCoverage.StateCounts[correlate.RelationshipStaleLiteral] != 1 || result.RelationshipCoverage.StateCounts[correlate.RelationshipNoStaleLiteral] != 1 || len(result.Suggestions) != 2 || result.Suggestions[1].RuleID != "replay_assertion_failed_unexplained" {
					t.Fatal("relationship states or manual assertion evidence were lost")
				}
			}
		})
	}
}

func TestStoredCorrelationCoverageKeepsTruncatedBaselineExplicit(t *testing.T) {
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "relationship-truncated")
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	observations := []storage.ScriptRecordingObservation{{ID: 0, ResponseBody: `{"csrf_token":"SYNTHETIC-RECORDED-123456"}`}, {ID: 1, RequestBody: "csrf=SYNTHETIC-RECORDED-123456"}}
	saveEncryptedRecordingTestBaseline(t, server, script.ID, digest, 3, observations)
	completed := time.Now().UTC()
	session := &ScriptDebugSession{ID: "relationship-replay", ProjectID: "p1", ScriptID: script.ID, Status: ScriptDebugCompleted, CompletedAt: &completed, FinalReport: &engine.ReplayReport{Success: true}, correlationActionGraphDigest: digest, correlationObservations: []correlate.Observation{{ID: 0, ResponseBody: observations[0].ResponseBody}, {ID: 1, RequestBody: observations[1].RequestBody}}}
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[session.ID] = session
	server.scriptDebug.mu.Unlock()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+script.ID+"/diagnose-replay", strings.NewReader(`{"replay_session_id":"relationship-replay"}`))
	req.SetPathValue("id", script.ID)
	bindAuthorizedProjectToRequest(req, "p1")
	response := httptest.NewRecorder()
	server.handleDiagnoseScriptReplay(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("stored diagnosis status=%d", response.Code)
	}
	var result correlationDiagnoseResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.TotalRecordedSteps != 3 || result.ComparedRecordedSteps != 2 || result.RelationshipCoverage.Complete || result.RelationshipCoverage.ProducerCount != 1 || result.RelationshipCoverage.ConsumerCount != 1 || result.RelationshipCoverage.StateCounts[correlate.RelationshipProducerUnchanged] != 1 {
		t.Fatal("stored baseline truncation disappeared behind inspected relationships")
	}
	if strings.Contains(response.Body.String(), "SYNTHETIC-RECORDED") {
		t.Fatal("stored relationship report disclosed encrypted baseline data")
	}
}

func TestCorrelationCoverageUnavailableSessionsRemainIndistinguishable(t *testing.T) {
	server := recordingTestServer(t)
	script := storeRecordingDigestTestScript(t, server, "relationship-session-scope")
	digest, err := scriptCorrelationActionGraphDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	saveEncryptedRecordingTestBaseline(t, server, script.ID, digest, 1, []storage.ScriptRecordingObservation{{ID: 0}})
	now := time.Now().UTC()
	past := now.Add(-2 * time.Minute)
	server.scriptDebug.retention.now = func() time.Time { return now }
	server.scriptDebug.retention.terminalTTL = time.Minute
	foreign := &ScriptDebugSession{ID: "relationship-foreign", ProjectID: "p2", ScriptID: script.ID, Status: ScriptDebugCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{Success: true}, correlationActionGraphDigest: digest}
	expired := &ScriptDebugSession{ID: "relationship-expired", ProjectID: "p1", ScriptID: script.ID, Status: ScriptDebugCompleted, CompletedAt: &past, FinalReport: &engine.ReplayReport{Success: true}, correlationActionGraphDigest: digest}
	wrongScript := &ScriptDebugSession{ID: "relationship-wrong-script", ProjectID: "p1", ScriptID: "another-script", Status: ScriptDebugCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{Success: true}, correlationActionGraphDigest: digest}
	unavailable := &ScriptDebugSession{ID: "relationship-unavailable", ProjectID: "p1", ScriptID: script.ID, Status: ScriptDebugCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{Success: true}, correlationActionGraphDigest: digest, correlationEvidenceErr: "synthetic unavailable raw evidence"}
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[wrongScript.ID] = wrongScript
	server.scriptDebug.sessions[unavailable.ID] = unavailable
	server.scriptDebug.sessions[foreign.ID] = foreign
	server.scriptDebug.sessions[expired.ID] = expired
	server.scriptDebug.mu.Unlock()
	var missingBody string
	for _, id := range []string{"relationship-missing", foreign.ID, expired.ID, wrongScript.ID, unavailable.ID} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+script.ID+"/diagnose-replay", strings.NewReader(`{"replay_session_id":"`+id+`"}`))
		req.SetPathValue("id", script.ID)
		bindAuthorizedProjectToRequest(req, "p1")
		response := httptest.NewRecorder()
		server.handleDiagnoseScriptReplay(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unavailable replay status=%d", response.Code)
		}
		if missingBody == "" {
			missingBody = response.Body.String()
		} else if response.Body.String() != missingBody {
			t.Fatal("relationship diagnosis exposed unavailable session identity or existence")
		}
		if !strings.Contains(response.Body.String(), "Trusted replay evidence unavailable") {
			t.Fatal("unavailable replay evidence returned differentiated diagnostics")
		}
		if strings.Contains(response.Body.String(), "relationship_coverage") {
			t.Fatal("unavailable trusted evidence yielded a partial relationship report")
		}
	}
}
