package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
)

func telemetryPolicyCLIFixture(t *testing.T) *api.TelemetryPolicyEvidence {
	t.Helper()
	policy := api.TelemetryBudgetPolicy{SchemaVersion: "apexload.telemetry_policy.v1", PolicyID: "system-telemetry-budgets", Revision: 1, CapturedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), CaptureBoundary: "execution-admission-before-staging", ClassificationAlgorithm: "legacy-mixed-diagnostic.v1", ExpectedControllerPublicationIntervalMs: 3000, CadenceScope: "local-controller-ticker", SustainedBreachWindowMs: 15000, CoverageRule: "retained-classifications-only;gaps-unknown", Budgets: []api.TelemetryStageBudget{{Stage: "sample_age", Enabled: true, LimitMs: 11}, {Stage: "generator_processing", Enabled: false, LimitMs: 0}, {Stage: "transport", Enabled: true, LimitMs: 750}, {Stage: "controller_fanout", Enabled: true, LimitMs: 250}, {Stage: "browser_render", Enabled: true, LimitMs: 1e-12}}}
	identity := policy
	identity.CapturedAt = time.Time{}
	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	policy.Hash = hex.EncodeToString(digest[:])
	if !api.ValidateTelemetryBudgetPolicy(&policy) {
		t.Fatal("fixture must satisfy the actual shared server validator")
	}
	return &api.TelemetryPolicyEvidence{Availability: "available", Policy: &policy}
}

func TestTelemetryPolicyCLIReadOnlyProjection(t *testing.T) {
	for _, mode := range []string{"live", "archive"} {
		t.Run(mode, func(t *testing.T) {
			evidence := telemetryPolicyCLIFixture(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				path := "/api/v1/tests/display-run/manifest"
				if mode == "archive" {
					path = "/api/v1/tests/display-run/archive/report"
				}
				if r.Method != "GET" || r.URL.Path != path || r.URL.Query().Get("project_id") != "project" {
					t.Errorf("unexpected method/scope/path")
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "archive" {
					json.NewEncoder(w).Encode(map[string]any{"telemetry_policy": evidence, "manifest": map[string]any{"test_id": "canonical-run", "provenance": map[string]any{"project_id": "project"}}, "protected_debug": "never-emit"})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"test_id": "canonical-run", "identity": map[string]any{"project_id": "project"}, "telemetry_policy": evidence, "protected_debug": "never-emit"})
				}
			}))
			defer server.Close()
			var output bytes.Buffer
			if err := executeWorkflowCommand(context.Background(), "analysis", []string{"telemetry-policy", "--api", server.URL, "--id", "display-run", "--project", "project", "--mode", mode}, &output); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || strings.Contains(output.String(), "never-emit") {
				t.Fatal("must project one read response without protected surrounding fields")
			}
			var projection struct {
				TestID          string                      `json:"test_id"`
				TelemetryPolicy api.TelemetryPolicyEvidence `json:"telemetry_policy"`
			}
			if json.Unmarshal(output.Bytes(), &projection) != nil || projection.TestID != "canonical-run" || projection.TelemetryPolicy.Policy.Budgets[1].Enabled || projection.TelemetryPolicy.Policy.Budgets[4].LimitMs != 1e-12 {
				t.Fatalf("policy or canonical identity changed: %s", output.Bytes())
			}
		})
	}
}

func TestTelemetryPolicyCLIWireAdmission(t *testing.T) {
	good, err := json.Marshal(telemetryPolicyCLIFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"missing availability": []byte(`{"policy":{}}`), "null availability": []byte(`{"availability":null}`),
		"null available policy":          []byte(`{"availability":"available","policy":null}`),
		"missing unavailable reason":     []byte(`{"availability":"unavailable"}`),
		"null unavailable reason":        []byte(`{"availability":"unavailable","reason":null}`),
		"unavailable with policy":        []byte(`{"availability":"unavailable","reason":"immutable-policy-not-retained","policy":{}}`),
		"duplicate availability":         append([]byte(`{"availability":"available",`), good[1:]...),
		"escaped duplicate availability": append([]byte(`{"\u0061vailability":"available",`), good[1:]...),
		"case alias":                     bytes.Replace(good, []byte(`"availability"`), []byte(`"Availability"`), 1),
		"unknown evidence field":         append(append([]byte{}, good[:len(good)-1]...), []byte(`,"secret":"never-emit"}`)...),
		"unknown policy field":           bytes.Replace(good, []byte(`"revision":1`), []byte(`"secret":"never-emit","revision":1`), 1),
		"missing enabled zero":           bytes.Replace(good, []byte(`"enabled":false,`), nil, 1),
		"null enabled zero":              bytes.Replace(good, []byte(`"enabled":false`), []byte(`"enabled":null`), 1),
		"missing budget zero":            bytes.Replace(good, []byte(`,"limit_ms":0`), nil, 1),
		"null budget zero":               bytes.Replace(good, []byte(`"limit_ms":0`), []byte(`"limit_ms":null`), 1),
		"missing interval":               bytes.Replace(good, []byte(`"expected_controller_publication_interval_ms":3000,`), nil, 1),
		"null interval":                  bytes.Replace(good, []byte(`"expected_controller_publication_interval_ms":3000`), []byte(`"expected_controller_publication_interval_ms":null`), 1),
		"bad policy hash":                bytes.Replace(good, []byte(`"limit_ms":11`), []byte(`"limit_ms":12`), 1),
		"trailing JSON":                  append(append([]byte{}, good...), []byte(` {}`)...), "oversize": bytes.Repeat([]byte(" "), (32<<10)+1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeTelemetryPolicyCLI(raw); err == nil {
				t.Fatal("malformed receipt accepted")
			}
		})
	}
	for _, reason := range []string{"immutable-policy-not-retained", "immutable-policy-invalid"} {
		raw := []byte(`{"availability":"unavailable","reason":"` + reason + `"}`)
		evidence, err := decodeTelemetryPolicyCLI(raw)
		if err != nil || evidence.Policy != nil || evidence.Reason != reason {
			t.Fatal("explicit unavailable receipt not retained")
		}
	}
}

func TestTelemetryPolicyCLIAdmissionBeforeIO(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	for _, args := range [][]string{{"telemetry-policy"}, {"telemetry-policy", "--id", "run", "--mode", "unknown"}, {"telemetry-policy", "--id", "run", "--file", "/must-not-open"}, {"telemetry-policy", "--id", "run", "--group", "workers"}, {"telemetry-policy", "--id", "run", "--limit", "1"}, {"telemetry-policy", "--id", "run", "--fail-on-regression"}, {"telemetry-policy", "--id", strings.Repeat("a", 129)}} {
		var out bytes.Buffer
		if err := executeWorkflowCommand(context.Background(), "analysis", append(args, "--api", server.URL), &out); err == nil || out.Len() != 0 {
			t.Fatal("unsupported command should refuse before any output")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := executeWorkflowCommand(ctx, "analysis", []string{"telemetry-policy", "--id", "run", "--api", server.URL}, &bytes.Buffer{}); err != context.Canceled {
		t.Fatal("cancelled context must refuse before request")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid read command performed network I/O")
	}
}

func TestTelemetryPolicyCLILegacyAndSafeRefusal(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		wantError  bool
	}{{"legacy", `{"test_id":"run","protected":"never-emit"}`, 200, false}, {"invalid body", `{"test_id":"run","telemetry_policy":{"availability":"healthy"},"protected":"never-emit"}`, 200, true}, {"duplicate field", `{"test_id":"run","test_id":"other"}`, 200, true}, {"alias field", `{"test_id":"run","Telemetry_Policy":{}}`, 200, true}, {"redirect", "never-emit", 302, true}, {"auth denial", "credential-never-emit", 403, true}, {"missing identity", `{"telemetry_policy":null}`, 200, true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				ioText := tc.body
				w.Write([]byte(ioText))
			}))
			defer server.Close()
			var out bytes.Buffer
			err := executeWorkflowCommand(context.Background(), "analysis", []string{"telemetry-policy", "--id", "run", "--api", server.URL}, &out)
			if (err != nil) != tc.wantError {
				t.Fatalf("wrong refusal: %v", err)
			}
			if strings.Contains(out.String(), "never-emit") || err != nil && strings.Contains(err.Error(), "never-emit") {
				t.Fatal("unsafe surrounding field/error text escaped projection")
			}
			if tc.wantError && out.Len() != 0 {
				t.Fatal("invalid response emitted partial projection")
			}
			if !tc.wantError && !strings.Contains(out.String(), "immutable-policy-not-retained") {
				t.Fatal("legacy missing receipt must stay unavailable")
			}
		})
	}
}

func TestTelemetryPolicyCLIProjectAndResponseBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"foreign project", []byte(`{"test_id":"run","identity":{"project_id":"other"}}`)},
		{"project alias", []byte(`{"test_id":"run","identity":{"Project_ID":"project"}}`)},
		{"missing project", []byte(`{"test_id":"run","identity":{}}`)},
		{"response quota", append(append([]byte(`{"test_id":"run","padding":"`), bytes.Repeat([]byte("x"), telemetryPolicyCLIResponseLimit)...), []byte(`"}`)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tc.body) }))
			defer server.Close()
			var out bytes.Buffer
			err := executeWorkflowCommand(context.Background(), "analysis", []string{"telemetry-policy", "--id", "run", "--project", "project", "--api", server.URL}, &out)
			if err == nil || out.Len() != 0 {
				t.Fatal("foreign, missing or excessive response must not emit a projection")
			}
		})
	}
}
