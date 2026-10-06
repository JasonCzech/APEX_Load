package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/api"
)

func resourceCLITestPage() api.BrowserResourcePage {
	zero := int64(0)
	return api.BrowserResourcePage{Version: 1, TestID: "run-1", ProjectID: "project-1", SourceFormat: "jsonl", SourceRevision: strings.Repeat("a", 64), SourceIntegrity: "legacy_scan_fingerprint", SourceTimestampPrecision: "unavailable", Population: api.BrowserResourcePagePopulation{Availability: "retained_observations", TransactionRecords: 1, RetainedEntryObservations: &zero, MatchedRetainedObservations: &zero}, CaptureCoverage: api.BrowserResourcePageCoverage{Availability: "unavailable", Reason: "retained_observations_are_not_capture_coverage"}, Items: []api.BrowserResourcePageItem{}}
}
func TestBrowserResourceCLIClosedProjectionAndScope(t *testing.T) {
	page := resourceCLITestPage()
	raw, _ := json.Marshal(page)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/tests/run-1/archive/browser-resources" || r.URL.Query().Get("project_id") != "project-1" {
			t.Error("unexpected read scope")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := runBrowserResourcesCommand(context.Background(), []string{"--api", server.URL, "--id", "run-1", "--project", "project-1"}, &out); err != nil {
		t.Fatal(err)
	}
	decoded, err := api.DecodeBrowserResourcePage(out.Bytes())
	if err != nil || decoded.Population.RetainedEntryObservations == nil || *decoded.Population.RetainedEntryObservations != 0 {
		t.Fatalf("observed empty retained scan lost: %v", err)
	}
	page.TestID = "other"
	raw, _ = json.Marshal(page)
	out.Reset()
	if err := NewApiClient(server.URL).readBrowserResources(context.Background(), "run-1", "project-1", url.Values{"project_id": {"project-1"}}, &out); err == nil || out.Len() != 0 {
		t.Fatal("foreign run was emitted")
	}
}
func TestBrowserResourceCLIInvalidFlagsNeverPerformIO(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, args := range [][]string{{"--file", "secret.json"}, {"--id", "another"}, {"--sort-by", "capture_order", "--sort-by", "duration_ms"}, {"--sort-by", "sql"}, {"--limit", "101"}, {"--state", "unknown"}, {"--cursor", "not-a-cursor"}, {"--action-name", ""}, {"extra"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			all := append([]string{"--api", server.URL, "--id", "run-1"}, args...)
			if err := runBrowserResourcesCommand(context.Background(), all, io.Discard); err == nil {
				t.Fatal("invalid flags admitted")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid flags contacted controller%d times", requests.Load())
	}
}
func TestBrowserResourceCLIRefusesRawErrorsRedirectsOversizeAndMissingFields(t *testing.T) {
	valid, _ := json.Marshal(resourceCLITestPage())
	missing := bytes.Replace(valid, []byte(`"transaction_records":1,`), nil, 1)
	for name, response := range map[string]struct {
		status   int
		body     []byte
		location string
	}{"secret_error": {http.StatusForbidden, []byte("password=private-secret"), ""}, "redirect": {http.StatusFound, valid, "http://127.0.0.1:1/credentials"}, "oversize": {http.StatusOK, bytes.Repeat([]byte(" "), api.MaxBrowserResourcePageBytes+1), ""}, "missing_required": {http.StatusOK, missing, ""}, "unknown": {http.StatusOK, bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"raw_headers":"secret"`), 1), ""}} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if response.location != "" {
					w.Header().Set("Location", response.location)
				}
				w.WriteHeader(response.status)
				_, _ = w.Write(response.body)
			}))
			defer server.Close()
			var out bytes.Buffer
			err := NewApiClient(server.URL).readBrowserResources(context.Background(), "run-1", "", nil, &out)
			if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unbounded or unsafe response emitted: %v", err)
			}
		})
	}
}
func TestBrowserResourceCLIContextBeforeNetwork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runBrowserResourcesCommand(ctx, []string{"--api", "http://127.0.0.1:1", "--id", "run-1"}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost preflight cancel: %v", err)
	}
}

func TestBrowserResourceCLIAuthenticationDefaultsAndExplicitOverrides(t *testing.T) {
	raw, _ := json.Marshal(resourceCLITestPage())
	var expectedKey, expectedBearer string
	var expectedMu sync.Mutex
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		expectedMu.Lock()
		key, bearer := expectedKey, expectedBearer
		expectedMu.Unlock()
		if r.Header.Get("X-APEXLoad-API-Key") != key || r.Header.Get("Authorization") != bearer {
			t.Error("controller authentication differs from admitted CLI inputs")
		}
		if r.URL.Query().Get("api-key") != "" || r.URL.Query().Get("bearer-token") != "" {
			t.Error("credentials became resource query filters")
		}
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	t.Setenv("APEXLOAD_API", server.URL)
	t.Setenv("APEXLOAD_API_KEY", "environment-key")
	t.Setenv("APEXLOAD_BEARER_TOKEN", "environment-token")
	for _, tc := range []struct {
		name        string
		args        []string
		key, bearer string
	}{
		{"environment", nil, "environment-key", "Bearer environment-token"},
		{"explicit", []string{"--api-key", "explicit-key", "--bearer-token", "explicit-token"}, "explicit-key", "Bearer explicit-token"},
		{"explicit_empty_removes_environment", []string{"--api-key=", "--bearer-token="}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectedMu.Lock()
			expectedKey, expectedBearer = tc.key, tc.bearer
			expectedMu.Unlock()
			args := append([]string{"--id", "run-1", "--project", "project-1"}, tc.args...)
			if err := runBrowserResourcesCommand(context.Background(), args, io.Discard); err != nil {
				t.Fatal(err)
			}
		})
	}
	if requests.Load() != 3 {
		t.Fatal("authenticated reads did not use the configured controller")
	}
}

func TestBrowserResourceCLIHelpNeverDisclosesCredentialOrEndpointDefaults(t *testing.T) {
	t.Setenv("APEXLOAD_API", "https://private-controller.example")
	t.Setenv("APEXLOAD_API_KEY", "private-key-must-not-appear")
	t.Setenv("APEXLOAD_BEARER_TOKEN", "private-token-must-not-appear")
	for _, arg := range []string{"--help", "-h"} {
		var output bytes.Buffer
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := runBrowserResourcesCommand(ctx, []string{arg}, &output); err != nil {
			t.Fatal(err)
		}
		text := output.String()
		for _, required := range []string{"Usage:", "--api-key", "--bearer-token", "APEXLOAD_API", "retained observations", "--document-id"} {
			if !strings.Contains(text, required) {
				t.Fatalf("help omitted safe usage information %q", required)
			}
		}
		for _, private := range []string{"private-controller", "private-key", "private-token"} {
			if strings.Contains(text, private) {
				t.Fatal("help disclosed a configured endpoint or credential value")
			}
		}
	}
}

type browserResourceCountingTransport struct{ requests atomic.Int64 }

func (transport *browserResourceCountingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.requests.Add(1)
	return nil, errors.New("unexpected transport invocation")
}

func TestBrowserResourceCLIControllerAndDirectProjectAdmissionBeforeIO(t *testing.T) {
	for _, endpoint := range []string{"", "relative-controller", "file:///private/key", "https://operator:private-secret@example.test", "https://example.test?api_key=private-secret", "https://example.test#private-secret"} {
		t.Run(endpoint, func(t *testing.T) {
			transport := &browserResourceCountingTransport{}
			client := NewApiClientWithAuth(endpoint, "", "")
			client.Client.Transport = transport
			err := client.readBrowserResources(context.Background(), "run-1", "project-1", nil, io.Discard)
			if err == nil || transport.requests.Load() != 0 || strings.Contains(err.Error(), "private-secret") {
				t.Fatal("unsafe controller URL reached transport or error disclosure")
			}
			var out bytes.Buffer
			if err := runBrowserResourcesCommand(context.Background(), []string{"--api", endpoint, "--id", "run-1"}, &out); err == nil || out.Len() != 0 || strings.Contains(err.Error(), "private-secret") {
				t.Fatal("unsafe command URL admitted or disclosed")
			}
		})
	}
	transport := &browserResourceCountingTransport{}
	client := NewApiClientWithAuth("http://controller.example", "", "")
	client.Client.Transport = transport
	if err := client.readBrowserResources(context.Background(), "run-1", "project-1", url.Values{"project_id": {"other-project"}}, io.Discard); err == nil || transport.requests.Load() != 0 {
		t.Fatal("conflicting direct query scope reached transport")
	}
}

func TestBrowserResourceCLIDirectExplicitQueryScopeStillFencesResponse(t *testing.T) {
	page := resourceCLITestPage()
	page.ProjectID = "other-project"
	raw, _ := json.Marshal(page)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
	defer server.Close()
	var out bytes.Buffer
	if err := NewApiClientWithAuth(server.URL, "", "").readBrowserResources(context.Background(), "run-1", "", url.Values{"project_id": {"project-1"}}, &out); err == nil || out.Len() != 0 {
		t.Fatal("explicit query project did not fence the decoded response")
	}
}

func TestBrowserResourceCLIRefusesValidShapeBeyondImmutableRequestedLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		count   int
		query   url.Values
		refused bool
	}{
		{"exact_requested_limit", 25, url.Values{"limit": {"25"}}, false},
		{"above_requested_limit", 26, url.Values{"limit": {"25"}}, true},
		{"above_default_limit", 51, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := resourceCLITestPage()
			count := int64(tc.count)
			page.Population.RetainedEntryObservations = &count
			page.Population.MatchedRetainedObservations = &count
			for index := 0; index < tc.count; index++ {
				page.Items = append(page.Items, api.BrowserResourcePageItem{Locator: api.BrowserResourcePageLocator{TransactionOrdinal: 1, ResourceOrdinal: int64(index + 1), Branch: "network_entries"}, TimestampPrecision: "unavailable", EvidenceState: "not_captured"})
			}
			raw, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := api.DecodeBrowserResourcePage(raw); err != nil {
				t.Fatal("fixture did not retain otherwise valid closed page shape")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
			defer server.Close()
			var output bytes.Buffer
			err = NewApiClient(server.URL).readBrowserResources(context.Background(), "run-1", "project-1", tc.query, &output)
			if tc.refused {
				if err == nil || output.Len() != 0 {
					t.Fatal("valid-shaped over-limit page reached CLI output")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			admitted, err := api.DecodeBrowserResourcePage(output.Bytes())
			if err != nil || len(admitted.Items) != tc.count {
				t.Fatal("exact requested limit was not preserved")
			}
		})
	}
}
