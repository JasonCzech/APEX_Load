package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const analysisCLITestMappingV1 = `{"version":1,"timestamp":["ts"],"timestamp_encoding":"unix_s","timezone":"UTC","measures":[{"value":["value"],"name":"latency","kind":"gauge","unit":"ms"}]}`
const analysisCLITestQuery = `{"metric":"latency","from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z","bucket_seconds":1,"aggregation":"avg"}`
const analysisCLITestComparison = `{"left":{"dataset_id":"a","metric":"latency","aggregation":"avg"},"right":{"dataset_id":"b","metric":"latency","aggregation":"avg"},"from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z","bucket_seconds":1,"max_lag_seconds":0,"mode":"overlay"}`

func analysisCLIIdentityArgs(t *testing.T, action, body, endpoint string) []string {
	t.Helper()
	args := []string{action, "--api", endpoint}
	if action == "map" || action == "query" || action == "commit" {
		args = append(args, "--id", "a")
	} else {
		args = append(args, "--project", "project")
	}
	if body != "" {
		file := filepath.Join(t.TempDir(), "request.json")
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--file", file)
	}
	return args
}

func TestAnalysisCLIIdentityRequiresPeerReceiptForAllExistingWritesAndQueries(t *testing.T) {
	mapping := strings.Replace(analysisCLITestMappingV1, `"version":1`, `"version":2`, 1)
	for _, capability := range []string{`{}`, `{"imported_identity_version":1}`, `{"imported_identity_version":null}`, `{"imported_identity_version":"2"}`, `{"imported_identity_version":2.5}`, `{"imported_identity_version":2,"Imported_Identity_Version":1}`} {
		for _, tc := range []struct{ action, body string }{
			{"map", `{"revision":7,"mapping":` + mapping + `}`},
			{"commit", ""},
			{"query", analysisCLITestQuery}, // Dataset metadata requires v2 without any selectors.
			{"compare", analysisCLITestComparison},
			{"save-mapping", `{"name":"mapping","definition":` + mapping + `}`},
			{"save-comparison", `{"name":"comparison","definition":` + analysisCLITestComparison + `}`},
		} {
			t.Run(tc.action+capability, func(t *testing.T) {
				var operations, capabilities atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/analysis/imports/") {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/v1/analysis/imports/"), "project_id": "project", "mapping": map[string]int{"version": 2}})
						return
					}
					if r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/capabilities" {
						capabilities.Add(1)
						_, _ = io.WriteString(w, capability)
						return
					}
					operations.Add(1)
					_, _ = io.WriteString(w, `{}`)
				}))
				defer server.Close()
				var output bytes.Buffer
				err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, tc.action, tc.body, server.URL), &output)
				if !errors.Is(err, errAnalysisCLIIdentityUnavailable) || operations.Load() != 0 || capabilities.Load() != 1 || output.Len() != 0 {
					t.Fatalf("old peer received operation: err=%v operations=%d capabilities=%d output=%q", err, operations.Load(), capabilities.Load(), output.String())
				}
			})
		}
	}
}

func TestAnalysisCLIIdentitySelectorPresenceRequiresReceiptOnLegacyMetadata(t *testing.T) {
	for _, extra := range []string{`"source_id":""`, `"host":"host"`, `"service":""`, `"dimensions":{"__proto__":"","Source_ID":"exact data"}`, `"identity_schema_version":2`} {
		t.Run(extra, func(t *testing.T) {
			var operations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/imports/a" {
					_, _ = io.WriteString(w, `{"id":"a","project_id":"project","mapping":{"version":1}}`)
				} else if r.URL.Path == "/api/v1/analysis/capabilities" {
					_, _ = io.WriteString(w, `{}`)
				} else {
					operations.Add(1)
				}
			}))
			defer server.Close()
			body := strings.TrimSuffix(analysisCLITestQuery, "}") + "," + extra + "}"
			if err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, "query", body, server.URL), &bytes.Buffer{}); !errors.Is(err, errAnalysisCLIIdentityUnavailable) || operations.Load() != 0 {
				t.Fatalf("legacy metadata hid explicit selector: err=%v operations=%d", err, operations.Load())
			}
		})
	}
}

func TestAnalysisCLILegacyRequestsAndManagedRunSelectorsPreserveExactBytes(t *testing.T) {
	runComparison := strings.Replace(analysisCLITestComparison, `"dataset_id":"b"`, `"run_id":"run","source_id":"monitor","host":"host","service":"service","dimensions":{"Source_ID":"data","__proto__":""}`, 1)
	for _, tc := range []struct{ action, body string }{
		{"map", `{"revision":7,"mapping":` + analysisCLITestMappingV1 + `}`}, {"commit", ""}, {"query", analysisCLITestQuery}, {"compare", analysisCLITestComparison},
		{"compare", runComparison}, {"save-mapping", `{"name":"mapping","definition":` + analysisCLITestMappingV1 + `}`},
		{"save-comparison", `{"name":"comparison","definition":` + runComparison + `}`},
	} {
		t.Run(tc.action+tc.body, func(t *testing.T) {
			var operations, capabilities atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/analysis/imports/") {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/v1/analysis/imports/"), "project_id": "project", "mapping": map[string]int{"version": 1}})
					return
				}
				if r.URL.Path == "/api/v1/analysis/capabilities" {
					capabilities.Add(1)
					_, _ = io.WriteString(w, `{}`)
					return
				}
				operations.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(body, []byte(tc.body)) {
					t.Errorf("preflight altered request: %q %v", body, err)
				}
				_, _ = io.WriteString(w, `{"ok":true}`)
			}))
			defer server.Close()
			if err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, tc.action, tc.body, server.URL), &bytes.Buffer{}); err != nil || operations.Load() != 1 || capabilities.Load() != 0 {
				t.Fatalf("legacy request refused: err=%v operations=%d capabilities=%d", err, operations.Load(), capabilities.Load())
			}
		})
	}
}

func TestAnalysisCLIClosedIdentityPreflightRefusesAliasesDuplicatesAndForeignScope(t *testing.T) {
	for _, tc := range []struct{ action, body, metadata string }{
		{"query", strings.TrimSuffix(analysisCLITestQuery, "}") + `,"Source_ID":"agent"}`, ""},
		{"query", strings.TrimSuffix(analysisCLITestQuery, "}") + `,"source_id":"agent","source_id":""}`, ""},
		{"query", strings.TrimSuffix(analysisCLITestQuery, "}") + `,"dimensions":{"a":"x","\u0061":"y"}}`, ""},
		{"query", strings.TrimSuffix(analysisCLITestQuery, "}") + `,"source_id":"\ud800"}`, ""},
		{"query", strings.TrimSuffix(analysisCLITestQuery, "}") + `,"project_id":"foreign"}`, ""},
		{"query", strings.TrimSuffix(analysisCLITestQuery, "}") + `,"dataset_id":"foreign"}`, ""},
		{"map", `{"revision":7,"mapping":` + strings.Replace(analysisCLITestMappingV1, `"version":1`, `"Version":2`, 1) + `}`, ""},
		{"compare", strings.Replace(analysisCLITestComparison, `"dataset_id":"b"`, `"dataset_id":"b","dataset_id":"a"`, 1), ""},
		{"compare", strings.Replace(analysisCLITestComparison, `"dataset_id":"b"`, `"run_id":"run","dimensions":{"a":"x","\u0061":"y"}`, 1), ""},
		{"query", analysisCLITestQuery, `{"id":"foreign","project_id":"project","mapping":{"version":2}}`},
		{"query", analysisCLITestQuery, `{"id":"a","project_id":"project","mapping":{"version":2,"version":1}}`},
		{"compare", analysisCLITestComparison, `{"id":"a","project_id":"foreign","mapping":{"version":1}}`},
	} {
		t.Run(tc.action+tc.body+tc.metadata, func(t *testing.T) {
			var operations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/analysis/imports/") {
					metadata := tc.metadata
					if metadata == "" {
						metadata = `{"id":"` + strings.TrimPrefix(r.URL.Path, "/api/v1/analysis/imports/") + `","project_id":"project","mapping":{"version":1}}`
					}
					_, _ = io.WriteString(w, metadata)
					return
				}
				if r.URL.Path == "/api/v1/analysis/capabilities" {
					_, _ = io.WriteString(w, `{"imported_identity_version":2}`)
					return
				}
				operations.Add(1)
			}))
			defer server.Close()
			if err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, tc.action, tc.body, server.URL), &bytes.Buffer{}); err == nil || operations.Load() != 0 {
				t.Fatalf("ambiguous request reached operation: err=%v operations=%d", err, operations.Load())
			}
		})
	}
}

func TestAnalysisCLIHistoricalIdentityDeclarationRequiresCapabilityEvenWithoutSelectors(t *testing.T) {
	var operations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/imports/a" {
			_, _ = io.WriteString(w, `{"id":"a","project_id":"project","mapping":{"version":1,"identity":{"source":["source"]}}}`)
		} else if r.URL.Path == "/api/v1/analysis/capabilities" {
			_, _ = io.WriteString(w, `{}`)
		} else {
			operations.Add(1)
		}
	}))
	defer server.Close()
	if err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, "query", analysisCLITestQuery, server.URL), &bytes.Buffer{}); !errors.Is(err, errAnalysisCLIIdentityUnavailable) || operations.Load() != 0 {
		t.Fatalf("historical identity declaration downgraded: %v operations=%d", err, operations.Load())
	}
}

func TestAnalysisCLIIdentityMetadataAndCapabilityErrorsRefuseOperation(t *testing.T) {
	for _, phase := range []string{"metadata", "capability"} {
		t.Run(phase, func(t *testing.T) {
			var operations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				metadata := r.URL.Path == "/api/v1/analysis/imports/a"
				capability := r.URL.Path == "/api/v1/analysis/capabilities"
				if phase == "metadata" && metadata || phase == "capability" && capability {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				} else if metadata {
					_, _ = io.WriteString(w, `{"id":"a","project_id":"project","mapping":{"version":2}}`)
				} else {
					operations.Add(1)
				}
			}))
			defer server.Close()
			if err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, "query", analysisCLITestQuery, server.URL), &bytes.Buffer{}); err == nil || operations.Load() != 0 {
				t.Fatalf("failed compatibility read admitted query: %v operations=%d", err, operations.Load())
			}
		})
	}
}

func TestAnalysisCLIIdentityScopeBoundsAndForeignExplicitProjectBeforeQuery(t *testing.T) {
	var requests, operations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/analysis/imports/a" {
			_, _ = io.WriteString(w, `{"id":"a","project_id":"foreign","mapping":{"version":1}}`)
		} else {
			operations.Add(1)
		}
	}))
	defer server.Close()
	args := analysisCLIIdentityArgs(t, "query", analysisCLITestQuery, server.URL)
	for _, scope := range [][]string{{"--project", strings.Repeat("x", 129)}, {"--id", strings.Repeat("x", 129)}, {"--project", "\u0085"}} {
		if err := executeWorkflowCommand(context.Background(), "analysis", append(append([]string{}, args...), scope...), &bytes.Buffer{}); err == nil || requests.Load() != 0 {
			t.Fatalf("scope encoding admitted before bounds: %v requests=%d", err, requests.Load())
		}
	}
	if err := executeWorkflowCommand(context.Background(), "analysis", append(args, "--project", "project"), &bytes.Buffer{}); err == nil || operations.Load() != 0 || requests.Load() != 1 {
		t.Fatalf("foreign project admitted query: %v requests=%d operations=%d", err, requests.Load(), operations.Load())
	}
}

type analysisCLIDetachedDeadline struct {
	context.Context
	at time.Time
}

func (c analysisCLIDetachedDeadline) Deadline() (time.Time, bool) { return c.at, true }
func (analysisCLIDetachedDeadline) Done() <-chan struct{}         { return nil }
func (analysisCLIDetachedDeadline) Err() error                    { return nil }

func TestAnalysisCLIIdentityLateCapabilityAndCancelledMetadataCannotEnterOperation(t *testing.T) {
	for _, phase := range []string{"metadata", "capability", "detached-capability"} {
		t.Run(phase, func(t *testing.T) {
			entered, release, handlerJoined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var operations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				metadata := r.URL.Path == "/api/v1/analysis/imports/a"
				capability := r.URL.Path == "/api/v1/analysis/capabilities"
				if phase == "metadata" && metadata || phase != "metadata" && capability {
					defer close(handlerJoined)
					close(entered)
					<-release
				}
				if metadata {
					_, _ = io.WriteString(w, `{"id":"a","project_id":"project","mapping":{"version":2}}`)
				} else if capability {
					_, _ = io.WriteString(w, `{"imported_identity_version":2}`)
				} else {
					operations.Add(1)
				}
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var requestContext context.Context = ctx
			deadline := time.Now().Add(100 * time.Millisecond)
			if phase == "detached-capability" {
				requestContext = analysisCLIDetachedDeadline{Context: context.Background(), at: deadline}
			}
			args := analysisCLIIdentityArgs(t, "query", analysisCLITestQuery, server.URL)
			outcome, joined := make(chan error, 1), make(chan struct{})
			defer func() {
				once.Do(func() { close(release) })
				cancel()
				<-joined
				server.Close()
				select {
				case <-entered:
					<-handlerJoined
				default:
				}
			}()
			go func() {
				defer close(joined)
				outcome <- executeWorkflowCommand(requestContext, "analysis", args, &bytes.Buffer{})
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("preflight barrier not entered")
			}
			if phase == "detached-capability" {
				timer := time.NewTimer(time.Until(deadline))
				<-timer.C
			} else {
				cancel()
			}
			once.Do(func() { close(release) })
			select {
			case err := <-outcome:
				if err == nil || operations.Load() != 0 {
					t.Fatalf("late authority admitted query: err=%v operations=%d", err, operations.Load())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("preflight caller failed to return")
			}
		})
	}
}

func TestAnalysisCLICommitRefusesIgnoredRequestOptionsBeforeNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	for _, body := range []string{`{"identity_schema_version":2}`, `{"allow_rejected":true}`, `{}`, `null`} {
		t.Run(body, func(t *testing.T) {
			var output bytes.Buffer
			err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, "commit", body, server.URL), &output)
			if err == nil || requests.Load() != 0 || output.Len() != 0 {
				t.Fatal("bodyless commit silently accepted request JSON", err, requests.Load(), output.String())
			}
		})
	}
}

// A dataset selection may pin its committed artifacts; the pin reaches the
// controller, and a malformed pin or a pin on a managed run is refused first.
func TestAnalysisCLIComparisonCarriesDatasetArtifactPins(t *testing.T) {
	pin := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, extra string
		run, sent   bool
	}{
		{"dataset pin", `"dataset_artifact_sha256":"` + pin + `"`, false, true},
		{"uppercase pin", `"dataset_artifact_sha256":"` + strings.ToUpper(pin) + `"`, false, false},
		{"short pin", `"dataset_artifact_sha256":"abc"`, false, false},
		{"pin on a run", `"dataset_artifact_sha256":"` + pin + `"`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/analysis/imports/") {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/v1/analysis/imports/"), "project_id": "project", "mapping": map[string]int{"version": 1}})
					return
				}
				body, _ := io.ReadAll(r.Body)
				received.Store(string(body))
				_, _ = io.WriteString(w, `{}`)
			}))
			defer server.Close()
			right := `{"dataset_id":"b","metric":"latency","aggregation":"avg",` + tc.extra + `}`
			if tc.run {
				right = `{"run_id":"run","metric":"response_time","aggregation":"avg",` + tc.extra + `}`
			}
			body := strings.Replace(analysisCLITestComparison, `"right":{"dataset_id":"b","metric":"latency","aggregation":"avg"}`, `"right":`+right, 1)
			var output bytes.Buffer
			err := executeWorkflowCommand(context.Background(), "analysis", analysisCLIIdentityArgs(t, "compare", body, server.URL), &output)
			sent, _ := received.Load().(string)
			if tc.sent {
				if err != nil || !strings.Contains(sent, pin) {
					t.Fatalf("pinned comparison: err=%v sent=%q", err, sent)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "dataset_artifact_sha256") || sent != "" {
				t.Fatalf("invalid pin: err=%v sent=%q", err, sent)
			}
		})
	}
}
