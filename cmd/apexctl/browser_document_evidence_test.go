package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func browserDocumentCLIFixture() (*protocol.BrowserDocumentSummary, protocol.BrowserDocumentObservation) {
	now := time.Now().UTC()
	generation := strings.Repeat("a", 32)
	observation := protocol.BrowserDocumentObservation{Version: 2, SourceGeneration: generation, DocumentID: generation + "-d1", LifecycleID: generation + "-l1", FrameID: generation + "-f1", NavigationID: generation + "-n1", MeasurementID: generation + "-d1/" + generation + "-l1/inp", Revision: 2, Metric: "inp", Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: "ms", Engine: "chromium", FrameScope: "main_frame", LifecyclePhase: "navigation", State: "final", Boundary: "graceful_end", WindowStart: now.Add(-time.Second), WindowEnd: now, CapturedAt: now, UnavailableReason: "no_interaction"}
	return protocol.SummarizeBrowserDocumentObservations([]protocol.BrowserDocumentObservation{observation}, 3, true), observation
}

func TestBrowserDocumentCLIReadsDistinctLiveAndArchivePopulations(t *testing.T) {
	summary, observation := browserDocumentCLIFixture()
	if summary.Clone() == nil || !observation.Valid() {
		t.Fatal("invalid fixture")
	}
	for _, mode := range []string{"live", "archive"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer approved" {
					t.Error("missing auth")
				}
				if r.Method != http.MethodGet {
					t.Error("not a read")
				}
				data := map[string]any{"test_id": "run", "unrelated_secret": "DO-NOT-PUBLISH", "browser_document_evidence_truncated": true}
				if mode == "live" {
					if r.URL.Path != "/api/v1/tests/run/metrics" || r.URL.Query().Get("history_limit") != "1" {
						t.Errorf("bad live request %s", r.URL)
					}
					data["metrics"] = map[string]any{"browser_documents": summary, "total_tx": 999, "browser": map[string]any{"samples": 999}}
				} else {
					if r.URL.Path != "/api/v1/tests/run/archive/report" || r.URL.Query().Get("include_release_trend") != "false" {
						t.Errorf("bad archive request %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(api.RunArchiveReport{Manifest: &api.RunArchiveManifest{TestID: "run", Error: "DO-NOT-PUBLISH"}, BrowserDocuments: summary, BrowserDocumentEvidence: []protocol.BrowserDocumentObservation{observation}, BrowserDocumentEvidenceTruncated: true})
					return
				}
				_ = json.NewEncoder(w).Encode(data)
			}))
			defer server.Close()
			t.Setenv("APEXLOAD_BEARER_TOKEN", "approved")
			var out bytes.Buffer
			if err := executeWorkflowCommand(context.Background(), "analysis", []string{"browser-documents", "--id", "run", "--mode", mode, "--api", server.URL}, &out); err != nil {
				t.Fatal(err)
			}
			var projection browserDocumentCLIProjection
			if err := json.Unmarshal(out.Bytes(), &projection); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || projection.TestID != "run" || projection.EvidenceState != "present" || projection.Summary == nil || !projection.Summary.Incomplete || projection.Summary.DroppedObservations != 3 || projection.Summary.Metrics[0].UnavailableFinal != 1 || projection.Summary.Metrics[0].Distribution.Count != 0 || strings.Contains(out.String(), "DO-NOT-PUBLISH") || strings.Contains(out.String(), "total_tx") {
				t.Fatalf("wrong population %s", &out)
			}
			if mode == "live" && (!strings.Contains(projection.PublicationSemantics, "overlaps-final") || len(projection.Observations) != 0 || projection.Truncated) {
				t.Fatal("live history treated as pending/exclusive")
			}
			if mode == "archive" && (!strings.Contains(projection.PublicationSemantics, "provisional-is-pending") || len(projection.Observations) != 1 || !projection.Truncated) {
				t.Fatal("archive observation/truncation lost")
			}
		})
	}
}

func TestBrowserDocumentCLIPreservesMissingAndNullMetricCoverage(t *testing.T) {
	for _, body := range []string{`{"test_id":"run"}`, `{"test_id":"run","browser_documents":{"version":2,"metrics":null,"incomplete":true,"dropped_observations":0}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		var out bytes.Buffer
		err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		var projection browserDocumentCLIProjection
		if err := json.Unmarshal(out.Bytes(), &projection); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, "browser_documents") {
			if projection.Summary == nil || !projection.Summary.Incomplete || len(projection.Summary.Metrics) != 0 || projection.EvidenceState != "present" {
				t.Fatal("requested missing coverage lost")
			}
		} else if projection.Summary != nil || projection.EvidenceState != "absent" || projection.UnavailableReason != "document_evidence_absent" {
			t.Fatal("legacy absence became healthy zero")
		}
	}
}

func TestBrowserDocumentCLIRejectsInvalidEvidenceAndScope(t *testing.T) {
	_, observation := browserDocumentCLIFixture()
	over := make([]protocol.BrowserDocumentObservation, protocol.MaxBrowserDocumentObservations+1)
	for _, value := range []any{
		nil,
		map[string]any{"browser_documents": map[string]any{"version": 99}},
		map[string]any{"browser_documents": map[string]any{"version": 2, "dropped_observations": -1}},
		map[string]any{"browser_documents": map[string]any{"version": 2, "metrics": nil, "dropped_observations": int64(0), "incomplete": false}, "browser_document_evidence": []protocol.BrowserDocumentObservation{{}}},
		map[string]any{"browser_documents": map[string]any{"version": 2, "metrics": nil, "dropped_observations": int64(0), "incomplete": false}, "browser_document_evidence": []protocol.BrowserDocumentObservation{observation, observation}},
		map[string]any{"browser_documents": map[string]any{"version": 2, "metrics": nil, "dropped_observations": int64(0), "incomplete": false}, "browser_document_evidence": over},
		map[string]any{"browser_document_evidence": []protocol.BrowserDocumentObservation{observation}},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(value) }))
		var out bytes.Buffer
		err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out)
		server.Close()
		if err == nil || out.Len() != 0 {
			t.Fatal("invalid evidence published")
		}
	}
	for _, args := range [][]string{{"browser-documents"}, {"browser-documents", "--id", "run", "--mode", "wrong"}, {"browser-documents", "--id", strings.Repeat("a", 129)}, {"browser-documents", "--id", "run", "--group", "x"}} {
		if executeWorkflowCommand(context.Background(), "analysis", args, &bytes.Buffer{}) == nil {
			t.Fatalf("invalid scope %v", args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewApiClient("http://invalid.test").readBrowserDocuments(ctx, "run", "live", &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	if NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &bytes.Buffer{}) == nil {
		t.Fatal("denied read succeeded")
	}
}

func TestBrowserDocumentCLIRequiredWireFieldsAndExplicitZero(t *testing.T) {
	summary, observation := browserDocumentCLIFixture()
	raw, _ := json.Marshal(summary)
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"metrics", "dropped_observations", "incomplete"} {
		altered := make(map[string]json.RawMessage, len(document))
		for k, v := range document {
			altered[k] = v
		}
		delete(altered, field)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"browser_documents": altered})
		}))
		var out bytes.Buffer
		err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out)
		server.Close()
		if err == nil || out.Len() != 0 {
			t.Fatalf("missing %s became zero", field)
		}
	}
	var tuples []map[string]json.RawMessage
	if err := json.Unmarshal(document["metrics"], &tuples); err != nil {
		t.Fatal(err)
	}
	delete(tuples[0], "eligible_final")
	altered := map[string]any{"version": 2, "metrics": tuples, "dropped_observations": 0, "incomplete": true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"browser_documents": altered})
	}))
	var out bytes.Buffer
	err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out)
	server.Close()
	if err == nil || out.Len() != 0 {
		t.Fatal("missing population became observed zero")
	}
	observationRaw, _ := json.Marshal(observation)
	var observationMap map[string]json.RawMessage
	_ = json.Unmarshal(observationRaw, &observationMap)
	delete(observationMap, "available")
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"browser_documents": summary, "browser_document_evidence": []any{observationMap}})
	}))
	out.Reset()
	err = NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out)
	server.Close()
	if err == nil || out.Len() != 0 {
		t.Fatal("missing availability became measured unavailable")
	}
	zero := protocol.BrowserDocumentSummary{Version: 2, Metrics: []protocol.BrowserDocumentMetricSummary{}, DroppedObservations: 0, Incomplete: false}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"browser_documents": zero})
	}))
	defer server.Close()
	out.Reset()
	if err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out); err != nil {
		t.Fatal(err)
	}
	var projection browserDocumentCLIProjection
	_ = json.Unmarshal(out.Bytes(), &projection)
	if projection.EvidenceState != "present" || projection.Summary == nil || projection.Summary.DroppedObservations != 0 || projection.Summary.Incomplete {
		t.Fatal("explicit typed zero lost")
	}
}

func TestBrowserDocumentCLIRefusesUnsupportedFiltersBeforeNetwork(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	for _, suffix := range [][]string{{"--metric", "lcp"}, {"--from", "2026-10-01T00:00:00Z"}, {"--to", "2026-10-02T00:00:00Z"}, {"--project", "other"}, {"--limit", "1"}, {"--group", "one"}, {"--source-id", "one"}, {"--file", "/not-read.json"}, {"--fail-on-regression"}} {
		args := append([]string{"browser-documents", "--id", "run", "--api", server.URL}, suffix...)
		if executeWorkflowCommand(context.Background(), "analysis", args, &bytes.Buffer{}) == nil {
			t.Fatalf("unsupported flag accepted %v", suffix)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unsupported scope contacted controller")
	}
	t.Setenv("APEXLOAD_BEARER_TOKEN", "SHOULD-NOT-APPEAR")
	var help bytes.Buffer
	if err := executeWorkflowCommand(context.Background(), "analysis", []string{"browser-documents", "--help"}, &help); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(help.String(), "SHOULD-NOT-APPEAR") || strings.Contains(help.String(), "--project") || !strings.Contains(help.String(), "overlap finals") {
		t.Fatalf("help %s", &help)
	}
}

func TestBrowserDocumentCLIRejectsDifferentReturnedRun(t *testing.T) {
	for _, mode := range []string{"live", "archive"} {
		for _, present := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "absent", true: "present"}[present], func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					path := "/api/v1/tests/requested-run/metrics"
					if mode == "archive" {
						path = "/api/v1/tests/requested-run/archive/report"
					}
					if r.Method != http.MethodGet || r.URL.Path != path {
						t.Errorf("wrong operation: %s %s", r.Method, r.URL.Path)
					}
					data := map[string]any{"test_id": "different-run"}
					if present {
						summary, _ := browserDocumentCLIFixture()
						if mode == "live" {
							data["metrics"] = map[string]any{"browser_documents": summary}
						} else {
							data["browser_documents"] = summary
						}
					}
					_ = json.NewEncoder(w).Encode(data)
				}))
				defer server.Close()
				var out bytes.Buffer
				err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "requested-run", mode, &out)
				if err == nil || calls != 1 || out.Len() != 0 || strings.Contains(err.Error(), "different-run") {
					t.Fatalf("mismatched run was displayed or leaked: %v, calls=%d, output=%s", err, calls, &out)
				}
			})
		}
	}
}

func TestBrowserDocumentCLIAbsentReturnedIdentityKeepsLegacyFallback(t *testing.T) {
	for _, mode := range []string{"live", "archive"} {
		for _, explicit := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "omitted", true: "empty"}[explicit], func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					data := map[string]any{}
					if explicit {
						data["test_id"] = ""
					}
					_ = json.NewEncoder(w).Encode(data)
				}))
				defer server.Close()
				var out bytes.Buffer
				if err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "requested-run", mode, &out); err != nil {
					t.Fatal(err)
				}
				var projection browserDocumentCLIProjection
				if err := json.Unmarshal(out.Bytes(), &projection); err != nil || projection.TestID != "requested-run" || projection.EvidenceState != "absent" || projection.Summary != nil {
					t.Fatalf("legacy absence lost: %v, %s", err, &out)
				}
			})
		}
	}
}

func TestBrowserDocumentCLIRealArchiveManifestIdentity(t *testing.T) {
	for _, canonical := range []bool{true, false} {
		t.Run(map[bool]string{true: "matching", false: "different"}[canonical], func(t *testing.T) {
			summary, _ := browserDocumentCLIFixture()
			returned := "different-run"
			if canonical {
				returned = "run"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/api/v1/tests/run/archive/report" {
					_ = json.NewEncoder(w).Encode(api.RunArchiveReport{Manifest: &api.RunArchiveManifest{TestID: returned, Error: "DO-NOT-PUBLISH"}, BrowserDocuments: summary})
				} else if r.URL.Path == "/api/v1/tests/run" {
					_ = json.NewEncoder(w).Encode(api.ManagedTest{ID: returned, RefKey: "unrelated-refkey", ProjectID: "project-1"})
				} else {
					t.Errorf("unexpected lookup %s", r.URL.Path)
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out)
			if !canonical {
				if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "different-run") {
					t.Fatalf("actual foreign manifest was displayed/leaked: %v", err)
				}
				return
			}
			var value map[string]json.RawMessage
			if err != nil || json.Unmarshal(out.Bytes(), &value) != nil || string(value["test_id"]) != `"run"` || calls != 1 || strings.Contains(out.String(), "DO-NOT-PUBLISH") {
				t.Fatalf("actual archive shape lost canonical identity or caused extra lookup: %v %s", err, &out)
			}
		})
	}
}
func TestBrowserDocumentCLIArchiveRefKeyIdentityLookup(t *testing.T) {
	for _, suppliedProject := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-project-absent", true: "project-pinned"}[suppliedProject], func(t *testing.T) {
			summary, _ := browserDocumentCLIFixture()
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer approved" {
					t.Error("lookup changed read/auth ownership")
				}
				if r.URL.Path == "/api/v1/tests/TEST-FLOW-ALIAS/archive/report" {
					manifest := &api.RunArchiveManifest{TestID: "canonical-run"}
					if suppliedProject {
						manifest.Provenance.ProjectID = "project-1"
					}
					_ = json.NewEncoder(w).Encode(api.RunArchiveReport{Manifest: manifest, BrowserDocuments: summary})
				} else if r.URL.Path == "/api/v1/tests/TEST-FLOW-ALIAS" {
					if r.URL.RawQuery != "" {
						t.Error("lookup requested history or a guessed projection")
					}
					_ = json.NewEncoder(w).Encode(api.ManagedTest{ID: "canonical-run", RefKey: "TEST-FLOW-ALIAS", ProjectID: "project-1", Name: "DO-NOT-PUBLISH"})
				} else {
					t.Errorf("unexpected lookup %s", r.URL.Path)
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			err := NewApiClientWithAuth(server.URL, "", "approved").readBrowserDocuments(context.Background(), "TEST-FLOW-ALIAS", "archive", &out)
			var value map[string]json.RawMessage
			if err != nil || json.Unmarshal(out.Bytes(), &value) != nil || string(value["test_id"]) != `"canonical-run"` || string(value["identity_state"]) != `"canonical-ref-key-matched"` || len(calls) != 2 || strings.Contains(out.String(), "DO-NOT-PUBLISH") {
				t.Fatalf("RefKey was not pinned to actual canonical archive: %v, calls=%v output=%s", err, calls, &out)
			}
		})
	}
}
func TestBrowserDocumentCLIRejectsAmbiguousAndMalformedResponseIdentity(t *testing.T) {
	for _, mode := range []string{"live", "archive"} {
		for _, body := range []string{
			`{"test_id":null}`, `{"test_id":12}`, `{"test_id":" "}`,
			`{"test_id":"run","test_id":"run"}`, `{"test_id":"run","tes\u0074_id":"other"}`,
			`{"Test_ID":"other"}`, `{"manifest":12}`, `{"manifest":{"test_id":null}}`,
			`{"manifest":{"test_id":"run","test_id":"other"}}`,
			`{"manifest":{"Test_ID":"other"}}`, `{"test_id":"run","manifest":{"test_id":"other"}}`,
			`{"manifest":{"test_id":"run"},"manifest":{"test_id":"run"}}`,
		} {
			t.Run(mode+"/"+body, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(body)) }))
				defer server.Close()
				var out bytes.Buffer
				err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", mode, &out)
				if err == nil || out.Len() != 0 || calls != 1 || strings.Contains(err.Error(), "other") {
					t.Fatalf("malformed/ambiguous identity admitted or disclosed: %v, calls=%d", err, calls)
				}
			})
		}
	}
}
func TestBrowserDocumentCLIRejectsUnqualifiedAliasLookup(t *testing.T) {
	for _, body := range []string{
		`{"id":"other","ref_key":"ALIAS","project_id":"project-1"}`,
		`{"id":"canonical-run","ref_key":"other","project_id":"project-1"}`,
		`{"id":"canonical-run","ref_key":"ALIAS","project_id":"other"}`,
		`{"id":"canonical-run","ref_key":"ALIAS"}`,
		`{"id":null,"ref_key":"ALIAS","project_id":"project-1"}`,
		`{"id":"canonical-run","id":"canonical-run","ref_key":"ALIAS","project_id":"project-1"}`,
		`{"id":"canonical-run","ref_key":"ALIAS","project_id":12}`,
		`{"ID":"canonical-run","ref_key":"ALIAS","project_id":"project-1"}`,
		`{"id":"canonical-run","ref_key":"ALIAS","project_id":"project-1","secret":"DO-NOT-PUBLISH"}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					_ = json.NewEncoder(w).Encode(api.RunArchiveReport{Manifest: &api.RunArchiveManifest{TestID: "canonical-run", Provenance: api.RunArchiveProvenance{ProjectID: "project-1"}}})
					return
				}
				if strings.Contains(body, "secret") {
					w.WriteHeader(http.StatusForbidden)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			var out bytes.Buffer
			err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "ALIAS", "archive", &out)
			if err == nil || out.Len() != 0 || calls != 2 || strings.Contains(err.Error(), "DO-NOT-PUBLISH") || strings.Contains(err.Error(), "other") {
				t.Fatalf("unqualified alias lookup accepted/disclosed: %v, calls=%d", err, calls)
			}
		})
	}
}
func TestBrowserDocumentCLILegacyIdentityIsExplicitlyUnverified(t *testing.T) {
	for _, body := range []string{`{}`, `{"test_id":""}`, `{"manifest":null}`, `{"manifest":{}}`, `{"manifest":{"test_id":""}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		var out bytes.Buffer
		err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "run", "archive", &out)
		server.Close()
		var value map[string]json.RawMessage
		if err != nil || json.Unmarshal(out.Bytes(), &value) != nil || string(value["test_id"]) != `"run"` || string(value["identity_state"]) != `"historical-requested-unverified"` {
			t.Fatalf("historical requested identity claimed verification: %v %s", err, &out)
		}
	}
}

func TestBrowserDocumentCLILiveRefKeyEchoKeepsSingleRead(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/tests/TEST-FLOW-ALIAS/metrics" {
			t.Errorf("unexpected alias lookup %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"test_id":"TEST-FLOW-ALIAS","metrics":{"browser_documents":null}}`))
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := NewApiClient(server.URL).readBrowserDocuments(context.Background(), "TEST-FLOW-ALIAS", "live", &out); err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(out.Bytes(), &value) != nil || calls != 1 || string(value["test_id"]) != `"TEST-FLOW-ALIAS"` || string(value["identity_state"]) != `"response-matched"` {
		t.Fatalf("live path echo lost alias compatibility: %s", &out)
	}
}
func TestBrowserDocumentCLIAliasLookupHonorsCancellationAndBounds(t *testing.T) {
	for _, mode := range []string{"cancel", "oversized", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					_ = json.NewEncoder(w).Encode(api.RunArchiveReport{Manifest: &api.RunArchiveManifest{TestID: "canonical-run"}})
					return
				}
				switch mode {
				case "cancel":
					cancel()
					<-r.Context().Done()
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", 8<<20+1)))
				case "redirect":
					http.Redirect(w, r, "/must-not-follow", http.StatusFound)
				}
			}))
			defer server.Close()
			var out bytes.Buffer
			err := NewApiClient(server.URL).readBrowserDocuments(ctx, "ALIAS", "archive", &out)
			if err == nil || out.Len() != 0 || calls != 2 {
				t.Fatalf("unbounded/retried alias lookup: %v calls=%d", err, calls)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}
