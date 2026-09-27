package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
)

func archiveEvidenceFixture() (archiveEvidenceExpectation, map[string]any) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 123456789, time.UTC)
	end := start.Add(time.Minute)
	expected := archiveEvidenceExpectation{TestID: "run-1", ProjectID: "project-1", CreatedAt: start, Status: "COMPLETE", MetadataBackend: "sqlite"}
	hash := strings.Repeat("a", 64)
	return expected, map[string]any{
		"run": map[string]any{"id": expected.TestID, "project_id": expected.ProjectID, "created_at": start, "ended_at": end, "status": "COMPLETE",
			"archive": map[string]any{"test_id": expected.TestID, "manifest_sha256": hash}, "error": "SECRET"},
		"manifest": map[string]any{"schema_version": "apexload.archive.v4", "test_id": expected.TestID, "status": "COMPLETE", "completed_at": end,
			"provenance":      map[string]any{"project_id": expected.ProjectID, "requested_at": start, "controller_env": map[string]string{"secret": "SECRET"}},
			"source_coverage": map[string]any{"mode": "required", "sources": []any{map[string]any{"target": "SECRET-target", "collected": true}}}},
		"integrity": &api.RunArchiveIntegrityReport{SchemaVersion: "apexload.archive_integrity.v1", TestID: expected.TestID,
			Status: "verified", Verified: true, VerifiedAt: time.Now().UTC(), Total: 2, Matched: 2, Summary: "SECRET",
			Artifacts: []api.RunArchiveIntegrityArtifact{{Name: "manifest.json", Status: "match", ExpectedSHA256: hash, ActualSHA256: hash},
				{Name: "SECRET.jsonl", Status: "match", ExpectedSHA256: hash, ActualSHA256: hash, SizeBytes: 12, Detail: "SECRET"}}},
	}
}

func evidenceServer(t *testing.T, data map[string]any, hook func(*http.Request, int)) (*ApiClient, *[]string) {
	t.Helper()
	calls := []string{}
	runReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer credential" {
			t.Errorf("missing auth")
		}
		if r.URL.Path == "/api/v1/tests/run-1" {
			runReads++
		}
		if hook != nil {
			hook(r, runReads)
		}
		key := "run"
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			key = "manifest"
		}
		if strings.HasSuffix(r.URL.Path, "/verify") {
			key = "integrity"
		}
		_ = json.NewEncoder(w).Encode(data[key])
	}))
	t.Cleanup(server.Close)
	return NewApiClientWithAuth(server.URL, "", "credential"), &calls
}

func TestArchiveEvidenceVerifiedSnapshot(t *testing.T) {
	expected, data := archiveEvidenceFixture()
	client, calls := evidenceServer(t, data, nil)
	report := collectArchiveEvidence(context.Background(), client, expected)
	if !report.Passed || !report.IdentityMatched || !report.IntegrityVerified || report.Sources != 1 || report.VerifiedArtifacts != 2 {
		t.Fatalf("report = %+v", report)
	}
	if len(*calls) != 4 {
		t.Fatalf("reads = %v", *calls)
	}
	encoded, err := json.Marshal(report)
	if err != nil || strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), "credential") {
		t.Fatalf("unsafe projection: %s, %v", encoded, err)
	}
}

func TestArchiveEvidenceRejectsUnknownManifestSchemaAndInvalidExpectations(t *testing.T) {
	expected, data := archiveEvidenceFixture()
	data["manifest"].(map[string]any)["schema_version"] = "future"
	client, calls := evidenceServer(t, data, nil)
	report := collectArchiveEvidence(context.Background(), client, expected)
	if report.Passed || report.Reason != "unsupported_manifest_schema" {
		t.Fatalf("report = %+v", report)
	}
	count := len(*calls)
	expected.MetadataBackend = "unknown"
	report = collectArchiveEvidence(context.Background(), client, expected)
	if report.Reason != "invalid_expectation" || len(*calls) != count {
		t.Fatal("invalid expectation performed API reads")
	}
}

func TestArchiveEvidencePassDoesNotImplyRunSucceeded(t *testing.T) {
	expected, data := archiveEvidenceFixture()
	expected.Status = "FAILED"
	data["run"].(map[string]any)["status"] = "FAILED"
	data["manifest"].(map[string]any)["status"] = "FAILED"
	client, _ := evidenceServer(t, data, nil)
	report := collectArchiveEvidence(context.Background(), client, expected)
	if !report.Passed || report.Expected.Status != "FAILED" {
		t.Fatalf("report = %+v", report)
	}
}

func TestArchiveEvidenceRejectsStaleOrFutureVerification(t *testing.T) {
	for _, offset := range []time.Duration{-time.Minute, time.Minute} {
		expected, data := archiveEvidenceFixture()
		data["integrity"].(*api.RunArchiveIntegrityReport).VerifiedAt = time.Now().Add(offset)
		client, _ := evidenceServer(t, data, nil)
		report := collectArchiveEvidence(context.Background(), client, expected)
		if report.Passed || report.Reason != "stale_integrity_response" {
			t.Fatalf("offset %s: %+v", offset, report)
		}
	}
}

func TestArchiveEvidenceFailClosedCases(t *testing.T) {
	cases := []struct {
		name, reason string
		mutate       func(map[string]any)
	}{
		{"wrong project", "identity_mismatch", func(d map[string]any) { d["run"].(map[string]any)["project_id"] = "other" }},
		{"reused id", "identity_mismatch", func(d map[string]any) { d["run"].(map[string]any)["created_at"] = time.Now() }},
		{"running", "unexpected_run_status", func(d map[string]any) { d["run"].(map[string]any)["status"] = "RUNNING" }},
		{"no end", "invalid_terminal_time", func(d map[string]any) { delete(d["run"].(map[string]any), "ended_at") }},
		{"no archive", "archive_missing", func(d map[string]any) { delete(d["run"].(map[string]any), "archive") }},
		{"observed failure", "archive_collection_failed", func(d map[string]any) {
			r := d["run"].(map[string]any)
			delete(r, "archive")
			r["archive_failure"] = map[string]any{"reason": "remote_collection_failed"}
		}},
		{"unknown failure", "archive_missing", func(d map[string]any) {
			r := d["run"].(map[string]any)
			delete(r, "archive")
			r["archive_failure"] = map[string]any{"reason": "SECRET"}
		}},
		{"no catalog anchor", "invalid_archive_identity", func(d map[string]any) { d["run"].(map[string]any)["archive"].(map[string]any)["manifest_sha256"] = "" }},
		{"wrong manifest", "manifest_identity_mismatch", func(d map[string]any) { d["manifest"].(map[string]any)["test_id"] = "other" }},
		{"legacy provenance", "manifest_identity_mismatch", func(d map[string]any) {
			delete(d["manifest"].(map[string]any)["provenance"].(map[string]any), "requested_at")
		}},
		{"unknown sources", "source_coverage_not_complete", func(d map[string]any) { delete(d["manifest"].(map[string]any), "source_coverage") }},
		{"best effort", "source_coverage_not_complete", func(d map[string]any) {
			d["manifest"].(map[string]any)["source_coverage"].(map[string]any)["mode"] = "best_effort"
		}},
		{"missing source", "source_coverage_not_complete", func(d map[string]any) {
			d["manifest"].(map[string]any)["source_coverage"].(map[string]any)["sources"].([]any)[0].(map[string]any)["collected"] = false
		}},
		{"empty sources", "invalid_source_coverage", func(d map[string]any) {
			d["manifest"].(map[string]any)["source_coverage"].(map[string]any)["sources"] = []any{}
		}},
		{"duplicate sources", "invalid_source_coverage", func(d map[string]any) {
			c := d["manifest"].(map[string]any)["source_coverage"].(map[string]any)
			s := c["sources"].([]any)
			c["sources"] = append(s, s[0])
		}},
		{"tampered", "integrity_not_verified", func(d map[string]any) {
			r := d["integrity"].(*api.RunArchiveIntegrityReport)
			r.Status = "tampered"
			r.Verified = false
		}},
		{"lying verified", "integrity_not_verified", func(d map[string]any) {
			d["integrity"].(*api.RunArchiveIntegrityReport).Artifacts[1].ActualSHA256 = strings.Repeat("b", 64)
		}},
		{"wrong verifier run", "integrity_not_verified", func(d map[string]any) { d["integrity"].(*api.RunArchiveIntegrityReport).TestID = "other" }},
		{"wrong root anchor", "integrity_not_verified", func(d map[string]any) {
			r := d["integrity"].(*api.RunArchiveIntegrityReport)
			r.Artifacts[0].ExpectedSHA256 = strings.Repeat("b", 64)
			r.Artifacts[0].ActualSHA256 = strings.Repeat("b", 64)
		}},
		{"missing entries", "integrity_not_verified", func(d map[string]any) { d["integrity"].(*api.RunArchiveIntegrityReport).Artifacts = nil }},
		{"duplicate entries", "integrity_not_verified", func(d map[string]any) {
			r := d["integrity"].(*api.RunArchiveIntegrityReport)
			r.Artifacts[1] = r.Artifacts[0]
		}},
		{"bad counts", "integrity_not_verified", func(d map[string]any) { d["integrity"].(*api.RunArchiveIntegrityReport).Matched = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected, data := archiveEvidenceFixture()
			tc.mutate(data)
			client, _ := evidenceServer(t, data, nil)
			report := collectArchiveEvidence(context.Background(), client, expected)
			if report.Passed || report.Reason != tc.reason {
				t.Fatalf("report = %+v", report)
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "SECRET") {
				t.Fatalf("unsafe report: %s", encoded)
			}
		})
	}
}

func TestArchiveEvidenceRechecksRunAndCatalog(t *testing.T) {
	for _, field := range []string{"created_at", "status", "ended_at", "archive"} {
		t.Run(field, func(t *testing.T) {
			expected, data := archiveEvidenceFixture()
			client, _ := evidenceServer(t, data, func(r *http.Request, n int) {
				if n == 2 {
					data["run"].(map[string]any)[field] = nil
				}
			})
			report := collectArchiveEvidence(context.Background(), client, expected)
			if report.Passed || report.IdentityMatched || report.Reason != "run_changed_during_collection" {
				t.Fatalf("report=%+v", report)
			}
		})
	}
}

func TestArchiveEvidenceTimestampPrecision(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			expected, data := archiveEvidenceFixture()
			expected.MetadataBackend = backend
			data["run"].(map[string]any)["created_at"] = expected.CreatedAt.Truncate(time.Microsecond)
			client, _ := evidenceServer(t, data, nil)
			report := collectArchiveEvidence(context.Background(), client, expected)
			if report.Passed != (backend == "postgres") {
				t.Fatalf("report=%+v", report)
			}
			data["run"].(map[string]any)["created_at"] = expected.CreatedAt.Truncate(time.Microsecond).Add(time.Microsecond)
			if collectArchiveEvidence(context.Background(), client, expected).Passed {
				t.Fatal("accepted different persisted timestamp")
			}
		})
	}
}

func TestArchiveEvidenceBoundedReadsAndRedactedErrors(t *testing.T) {
	for _, mode := range []string{"status", "malformed", "oversize", "timeout", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			expected, _ := archiveEvidenceFixture()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "status":
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, "SECRET")
				case "malformed":
					_, _ = io.WriteString(w, "{SECRET")
				case "oversize":
					_, _ = io.WriteString(w, strings.Repeat("x", archiveEvidenceMaxResponse+1))
				case "timeout":
					<-r.Context().Done()
				case "redirect":
					http.Redirect(w, r, "http://127.0.0.1:1/SECRET", http.StatusFound)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			report := collectArchiveEvidence(ctx, NewApiClient(server.URL), expected)
			if report.Passed {
				t.Fatal("unexpected pass")
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "SECRET") {
				t.Fatalf("unsafe error: %s", encoded)
			}
		})
	}
}
