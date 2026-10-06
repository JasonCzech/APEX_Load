package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func acceptanceBody(result string, artifacts bool) map[string]any {
	body := map[string]any{"requirement_id": "BE-4", "feature": "browser-har", "platform": "linux-amd64", "commit_sha": "abcdef1234567", "suite": "browser-acceptance", "result": result}
	if artifacts {
		body["artifacts"] = []map[string]string{{"name": "matrix.json", "sha256": strings.Repeat("a", 64)}}
	}
	return body
}

func TestFeatureAcceptanceRecordsDriveSupportStatus(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", acceptanceBody("passed", false), ""); response.Code != http.StatusBadRequest {
		t.Fatalf("a pass without evidence was recorded: %d %s", response.Code, response.Body.String())
	}
	for _, invalid := range []map[string]any{
		{"requirement_id": "be4", "feature": "x", "commit_sha": "abcdef1", "suite": "s", "result": "failed"},
		{"requirement_id": "BE-4", "feature": "Browser HAR", "commit_sha": "abcdef1", "suite": "s", "result": "failed"},
		{"requirement_id": "BE-4", "feature": "x", "commit_sha": "main", "suite": "s", "result": "failed"},
		{"requirement_id": "BE-4", "feature": "x", "commit_sha": "abcdef1", "suite": "s", "result": "supported"},
	} {
		if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", invalid, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid record accepted %v: %d", invalid, response.Code)
		}
	}
	status := func() []FeatureSupportStatus {
		response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/system/acceptance-status?requirement=BE-4", nil, "")
		var body struct {
			Statuses []FeatureSupportStatus `json:"statuses"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Statuses
	}
	if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", acceptanceBody("passed", true), ""); response.Code != http.StatusCreated {
		t.Fatalf("pass: %d %s", response.Code, response.Body.String())
	}
	if current := status(); len(current) != 1 || !current[0].Supported {
		t.Fatalf("after pass: %#v", current)
	}
	time.Sleep(2 * time.Millisecond)
	if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", acceptanceBody("revoked", false), ""); response.Code != http.StatusCreated {
		t.Fatalf("revoke: %d %s", response.Code, response.Body.String())
	}
	current := status()
	if len(current) != 1 || current[0].Supported || current[0].Latest.Result != "revoked" {
		t.Fatalf("a revoked claim still reads as supported: %#v", current)
	}
	list := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/system/acceptance-records?requirement=BE-4", nil, "")
	if !strings.Contains(list.Body.String(), `"result":"passed"`) || !strings.Contains(list.Body.String(), `"result":"revoked"`) {
		t.Fatalf("records are not append-only history: %s", list.Body.String())
	}
}

type acceptanceStatusResponse struct {
	Statuses  []FeatureSupportStatus `json:"statuses"`
	Truncated bool                   `json:"truncated"`
}

func acceptanceStatus(t *testing.T, server *Server, query string) acceptanceStatusResponse {
	t.Helper()
	response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/system/acceptance-status?"+query, nil, "")
	var body acceptanceStatusResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("status %s: %d %s", query, response.Code, response.Body.String())
	}
	return body
}

func recordAcceptance(t *testing.T, server *Server, body map[string]any) map[string]any {
	t.Helper()
	response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", body, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("record %v: %d %s", body, response.Code, response.Body.String())
	}
	var recorded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &recorded); err != nil {
		t.Fatal(err)
	}
	return recorded
}

func TestFeatureAcceptanceStatusSemantics(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	pass := func(platform, commit string) map[string]any {
		body := acceptanceBody("passed", true)
		body["platform"], body["commit_sha"], body["binary_sha256"] = platform, commit, strings.Repeat("b", 64)
		return body
	}
	recordAcceptance(t, server, pass("linux-amd64", "aaaaaaa1111"))
	recordAcceptance(t, server, pass("darwin-arm64", "aaaaaaa1111"))
	// Subjects that differ only in platform are independent.
	if statuses := acceptanceStatus(t, server, "requirement=BE-4&platform=linux-amd64").Statuses; len(statuses) != 1 || !statuses[0].Supported || statuses[0].Platform != "linux-amd64" {
		t.Fatalf("platform filter: %#v", statuses)
	}
	// A commit or binary query is supported only by a pass for that build.
	if statuses := acceptanceStatus(t, server, "requirement=BE-4&platform=linux-amd64&commit_sha=bbbbbbb2222").Statuses; statuses[0].Supported || statuses[0].Reason != "commit_mismatch" {
		t.Fatalf("other commit: %#v", statuses)
	}
	if statuses := acceptanceStatus(t, server, "requirement=BE-4&platform=linux-amd64&commit_sha=aaaaaaa").Statuses; !statuses[0].Supported {
		t.Fatalf("abbreviated commit: %#v", statuses)
	}
	if statuses := acceptanceStatus(t, server, "requirement=BE-4&platform=linux-amd64&binary_sha256="+strings.Repeat("c", 64)).Statuses; statuses[0].Supported || statuses[0].Reason != "binary_mismatch" {
		t.Fatalf("other binary: %#v", statuses)
	}
	// A revoke without a platform withdraws every platform's claim.
	revoke := acceptanceBody("revoked", false)
	delete(revoke, "platform")
	recordAcceptance(t, server, revoke)
	for _, status := range acceptanceStatus(t, server, "requirement=BE-4").Statuses {
		if status.Supported {
			t.Fatalf("a broader revoke left %s supported", status.Platform)
		}
		if status.Platform != "" && (status.Reason != "withdrawn_by_broader_record" || status.WithdrawnBy == nil) {
			t.Fatalf("narrower subject not marked withdrawn: %#v", status)
		}
	}
	// A newer pass for one platform restores only that platform.
	recordAcceptance(t, server, pass("linux-amd64", "ccccccc3333"))
	for _, status := range acceptanceStatus(t, server, "requirement=BE-4").Statuses {
		if status.Supported != (status.Platform == "linux-amd64") {
			t.Fatalf("after a newer pass: %#v", status)
		}
	}
	// A later broad pass clears the broad subject but never restores a
	// platform whose pass the broad revoke withdrew.
	broadPass := acceptanceBody("passed", true)
	delete(broadPass, "platform")
	recordAcceptance(t, server, broadPass)
	for _, status := range acceptanceStatus(t, server, "requirement=BE-4").Statuses {
		switch status.Platform {
		case "darwin-arm64":
			if status.Supported || status.Reason != "withdrawn_by_broader_record" {
				t.Fatalf("a broad pass restored a revoked platform: %#v", status)
			}
		default:
			if !status.Supported {
				t.Fatalf("after a broad pass: %#v", status)
			}
		}
	}
	// failed and pending withdraw a claim like revoked.
	for _, result := range []string{"failed", "pending"} {
		recordAcceptance(t, server, pass("linux-amd64", "ddddddd4444"))
		body := acceptanceBody(result, false)
		recordAcceptance(t, server, body)
		if statuses := acceptanceStatus(t, server, "requirement=BE-4&platform=linux-amd64").Statuses; statuses[0].Supported {
			t.Fatalf("a newer %s left the claim supported", result)
		}
	}
	// The POST echoes stored artifacts ([] for none) and its sequence.
	recorded := recordAcceptance(t, server, acceptanceBody("failed", false))
	if artifacts, ok := recorded["artifacts"].([]any); !ok || len(artifacts) != 0 || recorded["seq"] == nil {
		t.Fatalf("recorded echo: %#v", recorded)
	}
	// Notes are bounded at 256 bytes with an acceptance-specific error.
	long := acceptanceBody("failed", false)
	long["notes"] = strings.Repeat("n", 300)
	if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", long, ""); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "acceptance record") || strings.Contains(response.Body.String(), "analytical definition") {
		t.Fatalf("long notes: %d %s", response.Code, response.Body.String())
	}
	if response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/system/acceptance-status?commit_sha=main", nil, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid commit query: %d", response.Code)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "feature_acceptance.record"})
	if err != nil || len(events) == 0 {
		t.Fatalf("acceptance records are not audited: %v", err)
	}
}

// Support follows the database sequence even when record times disagree
// with it (a controller whose clock runs ahead wrote the earlier record).
func TestFeatureSupportOrdersBySequenceNotTime(t *testing.T) {
	now := time.Now().UTC()
	record := func(id, platform, result string, seq int64, at time.Time) *storage.FeatureAcceptanceRecord {
		return &storage.FeatureAcceptanceRecord{ID: id, RequirementID: "BE-4", Feature: "browser-har", Platform: platform, CommitSHA: "abcdef1", Result: result, Seq: seq, RecordedAt: at}
	}
	pass := record("pass", "linux", storage.AcceptancePassed, 2, now.Add(-time.Minute))
	// The broad revoke has the newer time but the older sequence.
	revoke := record("revoke", "", storage.AcceptanceRevoked, 1, now)
	statuses := resolveFeatureSupport([]*storage.FeatureAcceptanceRecord{pass, revoke}, []*storage.FeatureAcceptanceRecord{revoke}, "", "")
	for _, status := range statuses {
		if status.Platform == "linux" && !status.Supported {
			t.Fatalf("a revoke with an older sequence withdrew a newer pass: %#v", status)
		}
	}
	// With the sequences swapped the revoke is newer and withdraws it.
	pass.Seq, revoke.Seq = 1, 2
	for _, status := range resolveFeatureSupport([]*storage.FeatureAcceptanceRecord{pass, revoke}, []*storage.FeatureAcceptanceRecord{revoke}, "", "") {
		if status.Platform == "linux" && status.Supported {
			t.Fatalf("a newer revoke left the pass supported: %#v", status)
		}
	}
}

// A status read that hits the subject bound says so.
func TestFeatureAcceptanceStatusReportsTruncation(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	previous := maxAcceptanceSubjects
	maxAcceptanceSubjects = 2
	t.Cleanup(func() { maxAcceptanceSubjects = previous })
	for _, platform := range []string{"linux-amd64", "darwin-arm64", "windows-amd64"} {
		body := acceptanceBody("failed", false)
		body["platform"] = platform
		recordAcceptance(t, server, body)
	}
	status := acceptanceStatus(t, server, "requirement=BE-4")
	if !status.Truncated || len(status.Statuses) != 2 {
		t.Fatalf("truncated status: %#v", status)
	}
}
