package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type captureResultSink struct {
	result *protocol.Result
}

type typedSecretEvidence struct {
	SettlementToken string                     `json:"settlement_token"`
	Safe            string                     `json:"safe"`
	Events          []typedSecretEvidenceEvent `json:"events"`
}

type typedSecretEvidenceEvent struct {
	CSRFToken string `json:"csrf_token"`
	Message   string `json:"message"`
}

func (s *captureResultSink) RecordResult(result *protocol.Result) error {
	s.result = result
	return nil
}

func TestScrubSensitiveText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "redacts secrets but preserves ordinary personal data",
			in:   `{"access_token":"abc123","status":"ok","ssn":"123-45-6789"}`,
			want: `{"access_token":"[REDACTED]","ssn":"123-45-6789","status":"ok"}`,
		},
		{
			name: "redacts xml and query payloads",
			in:   `<token>abc123</token> https://example.test?session_id=secret-123`,
			want: `<token>[REDACTED]</token> https://example.test?session_id=[REDACTED]`,
		},
		{
			name: "preserves unclassified high entropy text",
			in:   `eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature`,
			want: `eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature`,
		},
		{
			name: "does not redact ordinary hostnames",
			in:   `https://archive.example.test/customers/customer-1234`,
			want: `https://archive.example.test/customers/customer-1234`,
		},
		{
			name: "does not redact long service URL paths",
			in:   `https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberContractDetails.jws`,
			want: `https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberContractDetails.jws`,
		},
		{
			name: "preserves benign payloads",
			in:   `{"status":"ok","id":"cart-1"}`,
			want: `{"status":"ok","id":"cart-1"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scrubSensitiveText(tt.in); got != tt.want {
				t.Fatalf("scrubSensitiveText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScrubSensitiveErrorRemovesEmbeddedURLCredentials(t *testing.T) {
	secret := "short-secret"
	err := scrubSensitiveError(errors.New("dial https://user:"+secret+"@example.test failed"), nil)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("scrubSensitiveError returned %q", err)
	}
}

func TestNewRedactionConfig_ScrubsCustomRules(t *testing.T) {
	config, err := NewRedactionConfig([]RedactionRule{{
		Name:    "customer id",
		Pattern: `customer-[0-9]+`,
	}})
	if err != nil {
		t.Fatalf("NewRedactionConfig returned error: %v", err)
	}
	if config == nil {
		t.Fatal("expected config")
	}
	if got := config.ScrubText(`{"customer":"customer-1234","status":"ok"}`); got != `{"customer":"[REDACTED]","status":"ok"}` {
		t.Fatalf("ScrubText() = %q, want custom id redacted", got)
	}
}

func TestNewRedactionConfig_RejectsInvalidPattern(t *testing.T) {
	if _, err := NewRedactionConfig([]RedactionRule{{Pattern: `[`}}); err == nil {
		t.Fatal("expected invalid pattern to fail")
	}
}

func TestNewRedactionConfigEnforcesRuntimePolicyBounds(t *testing.T) {
	tests := []struct {
		name  string
		rules []RedactionRule
		want  string
	}{
		{
			name:  "rule count",
			rules: make([]RedactionRule, MaxRedactionRules+1),
			want:  "rule limit",
		},
		{
			name:  "name bytes",
			rules: []RedactionRule{{Name: strings.Repeat("n", MaxRedactionRuleNameBytes+1), Pattern: "x"}},
			want:  "name exceeds",
		},
		{
			name:  "pattern bytes",
			rules: []RedactionRule{{Pattern: strings.Repeat("x", MaxRedactionPatternBytes+1)}},
			want:  "pattern exceeds",
		},
		{
			name:  "replacement bytes",
			rules: []RedactionRule{{Pattern: "x", Replacement: strings.Repeat("r", MaxRedactionReplacementBytes+1)}},
			want:  "replacement exceeds",
		},
		{
			name: "aggregate bytes",
			rules: func() []RedactionRule {
				rules := make([]RedactionRule, 65)
				for index := range rules {
					rules[index] = RedactionRule{Pattern: strings.Repeat("x", MaxRedactionPatternBytes)}
				}
				return rules
			}(),
			want: "policy exceeds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRedactionConfig(tt.rules)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewRedactionConfig error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestVUser_RecordResultScrubsSensitivePayloads(t *testing.T) {
	store := NewSnapshotStore(10)
	sink := &captureResultSink{}
	vu := &VUser{
		Group:         "Checkout",
		Metrics:       NewMetricsCollector(4),
		ResultSink:    sink,
		SnapshotStore: store,
		Variables:     map[string]string{},
	}

	result := &protocol.Result{
		Success:      false,
		ErrorMessage: "upstream rejected token=abc123&password=hunter2",
		ResponseBody: `{"access_token":"abc123","status":"ok","ssn":"123-45-6789"}`,
		PageURL:      "https://example.test/page?token=abc123",
		PageTitle:    "Account 123-45-6789",
		HTML:         `<html><body>token=AbCdEfGh1234567890ijklMN patient=123-45-6789</body></html>`,
		ConsoleEntries: []protocol.ConsoleEntry{{
			Text:   "client error access_token=console-secret",
			Source: "https://example.test/app.js?token=source-secret",
		}},
		DOMSummary: &protocol.DOMSummary{
			Title:       "Profile 123-45-6789",
			VisibleText: []string{"Authorization token=visible-secret", "ordinary text"},
		},
		NetworkEntries: []protocol.NetworkEntry{{
			URL:             "https://example.test/api?token=abc123",
			RequestHeaders:  map[string]string{"Cookie": "sid=abc123", "X-Correlation-ID": "corr-123"},
			RequestBody:     "password=hunter2",
			ResponseHeaders: map[string]string{"Set-Cookie": "sid=abc123"},
		}},
		Extractions: map[string]string{
			"session_token": "abc123",
		},
		Snapshot: &protocol.ErrorSnapshot{
			RequestURL:      `https://example.test/login?token=abc123&customer=42`,
			RequestHeaders:  map[string]string{"Authorization": "Bearer abc123", "X-Correlation-ID": "corr-123"},
			RequestBody:     `password=hunter2&customer=42`,
			ResponseHeaders: map[string]string{"Set-Cookie": "sessionid=abcdef1234567890", "Content-Type": "application/json"},
			ResponseBody:    `{"patient_id":"p-123","status":"ok"}`,
			HTML:            `<html><body>token=AbCdEfGh1234567890ijklMN patient=123-45-6789</body></html>`,
			PageURL:         "https://example.test/error?token=snapshot-secret",
			PageTitle:       "Failure 123-45-6789",
			ConsoleEntries: []protocol.ConsoleEntry{{
				Text:   "snapshot access_token=snapshot-console-secret",
				Source: "https://example.test/error.js?token=snapshot-source-secret",
			}},
			DOMSummary: &protocol.DOMSummary{
				Title:       "Snapshot 123-45-6789",
				VisibleText: []string{"token=snapshot-visible-secret"},
			},
			NetworkEntries: []protocol.NetworkEntry{{
				URL:             "https://example.test/snapshot?token=abc123",
				RequestHeaders:  map[string]string{"Authorization": "Bearer abc123"},
				ResponseHeaders: map[string]string{"Set-Cookie": "sid=abc123"},
			}},
		},
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{SnapshotOnError: true}, dsl.LogConfig{})

	if got := vu.Variables["session_token"]; got != "abc123" {
		t.Fatalf("session_token variable = %q, want raw extracted value", got)
	}
	if sink.result == nil || sink.result.Snapshot == nil {
		t.Fatal("expected scrubbed result to reach sink")
	}
	if strings.Contains(sink.result.ErrorMessage, "hunter2") || strings.Contains(sink.result.ErrorMessage, "abc123") {
		t.Fatalf("error message = %q, want secrets redacted", sink.result.ErrorMessage)
	}
	if strings.Contains(sink.result.PageURL, "abc123") || sink.result.PageTitle != "Account 123-45-6789" {
		t.Fatalf("page evidence leaked: url=%q title=%q", sink.result.PageURL, sink.result.PageTitle)
	}
	if strings.Contains(sink.result.ConsoleEntries[0].Text, "console-secret") || strings.Contains(sink.result.ConsoleEntries[0].Source, "source-secret") {
		t.Fatalf("named console secrets survived: %#v", sink.result.ConsoleEntries)
	}
	if sink.result.DOMSummary.Title != "Profile 123-45-6789" || strings.Contains(strings.Join(sink.result.DOMSummary.VisibleText, " "), "visible-secret") {
		t.Fatalf("DOM summary leaked: %#v", sink.result.DOMSummary)
	}
	if sink.result.Snapshot.RequestHeaders["Authorization"] != redactedPayloadValue {
		t.Fatalf("authorization header = %q, want %q", sink.result.Snapshot.RequestHeaders["Authorization"], redactedPayloadValue)
	}
	if sink.result.NetworkEntries[0].RequestHeaders["Cookie"] != redactedPayloadValue {
		t.Fatalf("network cookie header = %q, want %q", sink.result.NetworkEntries[0].RequestHeaders["Cookie"], redactedPayloadValue)
	}
	if sink.result.Snapshot.NetworkEntries[0].RequestHeaders["Authorization"] != redactedPayloadValue {
		t.Fatalf("snapshot network authorization header = %q, want %q", sink.result.Snapshot.NetworkEntries[0].RequestHeaders["Authorization"], redactedPayloadValue)
	}
	if sink.result.Snapshot.RequestHeaders["X-Correlation-ID"] != "corr-123" {
		t.Fatalf("correlation header = %q, want corr-123", sink.result.Snapshot.RequestHeaders["X-Correlation-ID"])
	}
	if !strings.Contains(sink.result.Snapshot.RequestURL, "token=[REDACTED]") {
		t.Fatalf("request url = %q, want token redacted", sink.result.Snapshot.RequestURL)
	}
	if !strings.Contains(sink.result.Snapshot.RequestBody, "password=[REDACTED]") {
		t.Fatalf("request body = %q, want password redacted", sink.result.Snapshot.RequestBody)
	}
	if sink.result.Snapshot.ResponseHeaders["Set-Cookie"] != redactedPayloadValue {
		t.Fatalf("set-cookie header = %q, want %q", sink.result.Snapshot.ResponseHeaders["Set-Cookie"], redactedPayloadValue)
	}
	if !strings.Contains(sink.result.Snapshot.ResponseBody, `"patient_id":"p-123"`) {
		t.Fatalf("response body = %q, want non-secret patient id preserved", sink.result.Snapshot.ResponseBody)
	}
	if !strings.Contains(sink.result.ResponseBody, `"access_token":"[REDACTED]"`) {
		t.Fatalf("result response body = %q, want access token redacted", sink.result.ResponseBody)
	}
	if !strings.Contains(sink.result.Snapshot.HTML, "123-45-6789") {
		t.Fatalf("html = %q, want ordinary evidence preserved", sink.result.Snapshot.HTML)
	}
	if strings.Contains(sink.result.Snapshot.PageURL, "snapshot-secret") || sink.result.Snapshot.PageTitle != "Failure 123-45-6789" {
		t.Fatalf("snapshot page evidence leaked: url=%q title=%q", sink.result.Snapshot.PageURL, sink.result.Snapshot.PageTitle)
	}
	if strings.Contains(sink.result.Snapshot.ConsoleEntries[0].Text, "snapshot-console-secret") ||
		strings.Contains(sink.result.Snapshot.ConsoleEntries[0].Source, "snapshot-source-secret") {
		t.Fatalf("snapshot console leaked: %#v", sink.result.Snapshot.ConsoleEntries)
	}
	if sink.result.Snapshot.DOMSummary.Title != "Snapshot 123-45-6789" ||
		strings.Contains(strings.Join(sink.result.Snapshot.DOMSummary.VisibleText, " "), "snapshot-visible-secret") {
		t.Fatalf("snapshot DOM summary leaked: %#v", sink.result.Snapshot.DOMSummary)
	}

	snapshots := store.Snapshots()
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if snapshots[0].RequestHeaders["Authorization"] != redactedPayloadValue {
		t.Fatalf("stored snapshot authorization = %q, want %q", snapshots[0].RequestHeaders["Authorization"], redactedPayloadValue)
	}
}

func TestVUserRecordResultNeverPublishesCorrelationEvidence(t *testing.T) {
	sink := &captureResultSink{}
	vu := &VUser{
		Metrics:    NewMetricsCollector(1),
		ResultSink: sink,
		Variables:  map[string]string{},
	}
	result := &protocol.Result{
		Success: true,
		CorrelationEvidence: &protocol.CorrelationEvidence{
			ResponseBody: `{"csrf_token":"raw-token-must-not-reach-sink"}`,
		},
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})

	if sink.result == nil {
		t.Fatal("result sink was not invoked")
	}
	if sink.result.CorrelationEvidence != nil {
		t.Fatal("result sink retained raw correlation evidence")
	}
}

func TestVUserRecordResultPreservesIdentityAndScrubsEvidenceMetadata(t *testing.T) {
	const (
		identity = "AbCdEfGh1234567890ijklMN"
		secret   = "customer-secret-42"
	)
	config, err := NewRedactionConfig([]RedactionRule{{Pattern: secret}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &captureResultSink{}
	vu := &VUser{
		Group:           "group-" + identity,
		Metrics:         NewMetricsCollector(2),
		ResultSink:      sink,
		Variables:       map[string]string{},
		RedactionConfig: config,
	}
	result := &protocol.Result{
		ActionName:          "transaction-" + identity,
		BusinessTransaction: true,
		ParentActionName:    "parent-" + secret,
		Region:              "region-" + secret,
		CorrelationID:       "correlation-" + secret,
		NetworkProfile:      "network-" + secret,
		DependencyType:      "dependency-" + secret,
		DependencyTarget:    "target-" + secret,
		SourceStepID:        "step-" + secret,
		SourceAction:        "source-action-" + secret,
		SourceActionName:    "source-name-" + secret,
		SourceTransaction:   "source-transaction-" + identity,
		SourceProfile:       "profile-" + secret,
		Success:             true,
		ClientMetrics: &protocol.ClientMetrics{
			DeviceProfile:  "device-" + secret,
			DevicePlatform: "platform-" + secret,
			DeviceTier:     "tier-" + secret,
			BrowserEngine:  "browser-" + secret,
		},
		ServerTelemetry: &protocol.ServerTelemetry{
			RequestSizeClass:      "request-" + secret,
			ResponseSizeClass:     "response-" + secret,
			SOAPFaultCode:         "fault-" + secret,
			SOAPFaultCategory:     "category-" + secret,
			SOAPNamespaceConflict: "namespace-" + secret,
			PartitionID:           "partition-" + secret,
		},
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if sink.result == nil {
		t.Fatal("result did not reach archive/metrics sink")
	}
	for name, value := range map[string]string{
		"action":             sink.result.ActionName,
		"group":              sink.result.Group,
		"source transaction": sink.result.SourceTransaction,
	} {
		if !strings.Contains(value, identity) || strings.Contains(value, redactedPayloadValue) {
			t.Fatalf("%s identity = %q, want authored name preserved", name, value)
		}
	}
	values := []string{
		sink.result.ParentActionName,
		sink.result.Region,
		sink.result.CorrelationID, sink.result.NetworkProfile,
		sink.result.DependencyType, sink.result.DependencyTarget,
		sink.result.SourceStepID, sink.result.SourceAction,
		sink.result.SourceActionName,
		sink.result.SourceProfile,
		sink.result.ClientMetrics.DeviceProfile,
		sink.result.ClientMetrics.DevicePlatform,
		sink.result.ClientMetrics.DeviceTier,
		sink.result.ClientMetrics.BrowserEngine,
		sink.result.ServerTelemetry.RequestSizeClass,
		sink.result.ServerTelemetry.ResponseSizeClass,
		sink.result.ServerTelemetry.SOAPFaultCode,
		sink.result.ServerTelemetry.SOAPFaultCategory,
		sink.result.ServerTelemetry.SOAPNamespaceConflict,
		sink.result.ServerTelemetry.PartitionID,
	}
	for index, value := range values {
		if strings.Contains(value, secret) || !strings.Contains(value, redactedPayloadValue) {
			t.Fatalf("metadata value[%d] = %q, want custom secret redacted", index, value)
		}
	}
}

func TestCustomPolicyDoesNotRedactIdentityDimensions(t *testing.T) {
	config, err := NewRedactionConfig([]RedactionRule{{Pattern: `customer-[0-9]+`}})
	if err != nil {
		t.Fatal(err)
	}
	result := &protocol.Result{
		ActionName:          "transaction-customer-1234",
		BusinessTransaction: true,
		Group:               "group-customer-1234",
		SourceTransaction:   "source-customer-1234",
	}

	scrubSensitiveResult(result, config)

	for name, values := range map[string][2]string{
		"transaction":        {result.ActionName, "transaction-customer-1234"},
		"group":              {result.Group, "group-customer-1234"},
		"source transaction": {result.SourceTransaction, "source-customer-1234"},
	} {
		if values[0] != values[1] {
			t.Fatalf("%s identity = %q, want %q", name, values[0], values[1])
		}
	}
}

func TestVUserRecordResultScrubsLongRuntimeSecretsBeforeEvidenceBounds(t *testing.T) {
	secret := strings.Repeat("q", maxResultErrorBytes+(8<<10))
	config := WithSecretValueRedaction(nil, []string{secret})
	sink := &captureResultSink{}
	vu := &VUser{
		Group:           "long-secret-result",
		Metrics:         NewMetricsCollector(2),
		ResultSink:      sink,
		Variables:       map[string]string{},
		RedactionConfig: config,
	}
	result := &protocol.Result{
		ActionName:       "action-" + secret,
		ErrorMessage:     "error-" + secret,
		ResponseBody:     "body-" + secret,
		Extractions:      map[string]string{"ordinary": "valid-small-extraction"},
		ProtocolEvidence: map[string]any{"ordinary": "evidence-" + secret},
		Snapshot:         &protocol.ErrorSnapshot{ResponseBody: "snapshot-" + secret},
		Success:          false,
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{SnapshotOnError: true}, dsl.LogConfig{})
	if sink.result == nil {
		t.Fatal("result did not reach archive/metrics sink")
	}
	encoded, err := json.Marshal(sink.result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(strings.Repeat("q", 256))) || !bytes.Contains(encoded, []byte(redactedPayloadValue)) {
		t.Fatalf("bounded result retained a prefix of a long runtime secret: %s", encoded)
	}
}

// Values a protocol adapter harvested from the system under test (HTTP cookie
// jar contents) are no longer registered for redaction: the operator is running
// the test against that system and needs the evidence. The two origin-
// independent layers still apply — a project-secret value stays redacted and a
// sensitively NAMED extraction is still suppressed.
func TestVUserRecordResultKeepsHarvestedEvidenceAndStillRedactsPlatformSecret(t *testing.T) {
	const (
		cookieValue    = "xy7"
		platformSecret = "vault-issued-bravo-5512"
		namedToken     = "extracted-charlie-9931"
	)
	sink := &captureResultSink{}
	vu := &VUser{
		Metrics:         NewMetricsCollector(4),
		ResultSink:      sink,
		Variables:       map[string]string{},
		RedactionConfig: WithSecretValueRedaction(nil, []string{platformSecret}),
	}
	// Phrased so the origin-independent generic scrubbers (name= patterns, JWT and
	// high-entropy detection) do not fire: this test measures the exact-value
	// matcher only.
	evidence := "upstream echoed " + cookieValue +
		" and " + platformSecret + " and " + namedToken
	first := &protocol.Result{
		Success:      false,
		ErrorMessage: evidence,
		ResponseBody: evidence,
		Extractions: map[string]string{
			"ordinary":      cookieValue,
			"session_token": namedToken,
		},
		ProtocolEvidence: map[string]any{"ordinary": cookieValue},
		Snapshot:         &protocol.ErrorSnapshot{ResponseBody: evidence},
	}

	vu.recordResult(first, dsl.ErrorHandlingConfig{SnapshotOnError: true}, dsl.LogConfig{})

	if got := vu.Variables["ordinary"]; got != cookieValue {
		t.Fatalf("raw extraction = %q, want the harvested value for later replay", got)
	}
	encoded, err := json.Marshal(sink.result)
	if err != nil {
		t.Fatal(err)
	}
	// Previously asserted the exact opposite: the harvested cookie value had to be
	// absent from every retained field. That is the behaviour the product owner
	// rejected — it blanked ordinary application state out of the operator's run.
	if !bytes.Contains(encoded, []byte(cookieValue)) {
		t.Fatalf("harvested cookie value was scrubbed out of the operator's evidence: %s", encoded)
	}
	if bytes.Contains(encoded, []byte(platformSecret)) {
		t.Fatalf("platform-managed secret leaked: %s", encoded)
	}
	if bytes.Contains(encoded, []byte(namedToken)) {
		t.Fatalf("sensitively named extraction leaked: %s", encoded)
	}

	later := &protocol.Result{
		Success:      false,
		ErrorMessage: "later echo " + cookieValue + " and " + platformSecret,
		ResponseBody: cookieValue,
		Extractions:  map[string]string{"ordinary_later": cookieValue},
	}
	vu.recordResult(later, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	encoded, err = json.Marshal(sink.result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(cookieValue)) {
		t.Fatalf("later harvested-value echo was scrubbed: %s", encoded)
	}
	if bytes.Contains(encoded, []byte(platformSecret)) {
		t.Fatalf("later platform-managed secret echo leaked: %s", encoded)
	}
}

// The stable metric dimensions used to be shielded from cookie snapshots by a
// second, session-excluding matcher. Nothing harvested reaches the matcher any
// more, so a cookie-shaped substring survives in BOTH the dimensions and the
// evidence, while a platform-managed secret is removed from both.
func TestHarvestedCookieSubstringLeavesDimensionsAndEvidenceIntact(t *testing.T) {
	const (
		cookieValue    = "Login"
		platformSecret = "vault-issued-bravo-7734"
	)
	sink := &captureResultSink{}
	vu := &VUser{
		Group:           "Login group",
		Metrics:         NewMetricsCollector(4),
		ResultSink:      sink,
		Variables:       map[string]string{},
		RedactionConfig: WithSecretValueRedaction(nil, []string{platformSecret}),
	}
	result := &protocol.Result{
		ActionName:       "Login action",
		DependencyTarget: "https://example.test/Login",
		CorrelationID:    cookieValue,
		Success:          false,
		ErrorMessage:     "payload includes " + cookieValue + " and " + platformSecret,
		ResponseBody:     "payload includes " + cookieValue,
		Extractions:      map[string]string{"echo": cookieValue},
		ProtocolEvidence: map[string]any{"echo": cookieValue},
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if sink.result == nil {
		t.Fatal("result did not reach sink")
	}
	if sink.result.ActionName != "Login action" || sink.result.Group != "Login group" ||
		sink.result.DependencyTarget != "https://example.test/Login" {
		t.Fatalf("harvested cookie value rewrote stable dimensions: action=%q group=%q target=%q",
			sink.result.ActionName, sink.result.Group, sink.result.DependencyTarget)
	}
	// Previously asserted the cookie value had been removed from every one of
	// these fields.
	if !strings.Contains(sink.result.ErrorMessage, cookieValue) ||
		!strings.Contains(sink.result.ResponseBody, cookieValue) ||
		!strings.Contains(sink.result.Extractions["echo"], cookieValue) {
		t.Fatalf("harvested cookie value was scrubbed from retained payload evidence: %#v", sink.result)
	}
	if evidence, _ := sink.result.ProtocolEvidence["echo"].(string); !strings.Contains(evidence, cookieValue) {
		t.Fatalf("harvested cookie value was scrubbed from protocol evidence: %#v", sink.result.ProtocolEvidence)
	}
	if !strings.Contains(sink.result.CorrelationID, cookieValue) {
		t.Fatalf("harvested cookie value was scrubbed from correlation evidence: %q", sink.result.CorrelationID)
	}
	if strings.Contains(sink.result.ErrorMessage, platformSecret) {
		t.Fatalf("platform-managed secret survived evidence: %q", sink.result.ErrorMessage)
	}
}

func TestPlatformManagedSecretDoesNotRedactIdentityDimensions(t *testing.T) {
	const credential = "resolved-secret-4821"
	vu := &VUser{
		Group:           "group-" + credential,
		Metrics:         NewMetricsCollector(1),
		Variables:       map[string]string{},
		RedactionConfig: WithSecretValueRedaction(nil, []string{credential}),
	}
	result := &protocol.Result{
		ActionName:          "transaction-" + credential,
		BusinessTransaction: true,
		ParentActionName:    "parent-" + credential,
		SourceTransaction:   "source-transaction-" + credential,
		DependencyTarget:    "https://example.test/" + credential,
		Success:             true,
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	for name, values := range map[string][2]string{
		"action":             {result.ActionName, "transaction-" + credential},
		"group":              {result.Group, "group-" + credential},
		"source transaction": {result.SourceTransaction, "source-transaction-" + credential},
	} {
		if values[0] != values[1] {
			t.Fatalf("%s identity = %q, want %q", name, values[0], values[1])
		}
	}
	for name, value := range map[string]string{"parent action": result.ParentActionName, "target": result.DependencyTarget} {
		if strings.Contains(value, credential) {
			t.Fatalf("%s evidence retained a platform-managed secret: %q", name, value)
		}
	}
}

func TestSensitivelyNamedExtractionDoesNotRedactGroupIdentity(t *testing.T) {
	const extracted = "harvested-bearer-3390"
	vu := &VUser{
		Group:     "group-" + extracted,
		Metrics:   NewMetricsCollector(1),
		Variables: map[string]string{},
	}
	result := &protocol.Result{
		ActionName:       "action-" + extracted,
		DependencyTarget: "https://example.test/" + extracted,
		Success:          true,
		Extractions:      map[string]string{"session_token": extracted},
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if result.Group != "group-"+extracted {
		t.Fatalf("group identity = %q, want authored name preserved", result.Group)
	}
	for name, value := range map[string]string{"action": result.ActionName, "target": result.DependencyTarget} {
		if strings.Contains(value, extracted) {
			t.Fatalf("%s evidence retained a sensitively named extraction: %q", name, value)
		}
	}
	if got := vu.Variables["session_token"]; got != extracted {
		t.Fatalf("raw sensitive extraction = %q, want the value usable by later actions", got)
	}
}

func TestRuntimeSecretRedactionRunsBeforeGenericCredentialPatterns(t *testing.T) {
	const secret = `abc"UNIQUELEAK`
	config, err := WithSecretValueRedactionChecked(nil, []string{secret})
	if err != nil {
		t.Fatal(err)
	}
	result := &protocol.Result{
		ResponseBody: `{"token":"` + jsonEscapedSecret(secret, false) + `"}`,
		ErrorMessage: `token=` + jsonEscapedSecret(secret, false),
	}

	scrubSensitiveResult(result, config)

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("UNIQUELEAK")) || bytes.Contains(encoded, []byte(secret)) {
		t.Fatalf("generic redaction mutated an encoded secret before its exact match: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(redactedPayloadValue)) {
		t.Fatalf("exact runtime secret marker missing: %s", encoded)
	}
}

// The amplification bound is a property of the matcher itself and still matters
// for PLATFORM-MANAGED secrets, which are the only values that reach it now.
// A one-byte project-secret value must be fully redacted without letting
// attacker-controlled evidence grow the retained output.
func TestRuntimeSecretRedactionDoesNotAmplifyShortMatches(t *testing.T) {
	// Stands in for a one-byte {{secret.NAME}} value resolved from the project
	// secret store. Script-sourced values never reach this matcher.
	const platformManagedSecret = "a"
	config, err := WithSecretValueRedactionChecked(nil, []string{platformManagedSecret})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat(platformManagedSecret, 1<<20)
	got := config.ScrubText(input)
	if len(got) > len(input) {
		t.Fatalf("one-byte secret expanded evidence from %d to %d bytes", len(input), len(got))
	}
	if strings.Contains(got, platformManagedSecret) {
		t.Fatal("one-byte platform-managed secret survived exact redaction")
	}
}

func TestRuntimeSecretRedactionPreservesPreexistingNULBytes(t *testing.T) {
	config, err := WithSecretValueRedactionChecked(nil, []string{"secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	input := "before\x00secret-value\x00after"
	got := config.ScrubText(input)
	if got != "before\x00"+redactedPayloadValue+"\x00after" {
		t.Fatalf("ScrubText() = %q", got)
	}
}

func TestGenericJWTTextIsPreservedWithoutSecretClassification(t *testing.T) {
	valid := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl"
	invalid := "documents.example.invalid.assets.bundle123456789"
	missingAlgorithm := "eyJ0eXAiOiJKV1QifQ.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl"
	nonStringAlgorithm := "eyJhbGciOjEyM30.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl"
	invalidSignature := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature"
	got := scrubSensitiveText(valid + " " + invalid)
	if !strings.Contains(got, valid) {
		t.Fatalf("unclassified JWT text was rewritten: %q", got)
	}
	if !strings.Contains(got, invalid) {
		t.Fatalf("non-JWT dotted token was redacted: %q", got)
	}
	for _, nonJWT := range []string{missingAlgorithm, nonStringAlgorithm, invalidSignature} {
		if classified := scrubStructurallyValidJWTs(nonJWT); classified != nonJWT {
			t.Fatalf("JWT classifier accepted header without a string alg: got %q, want %q", classified, nonJWT)
		}
	}
}

func TestReplacingUnchangedRuntimeCredentialScopeReusesMatcher(t *testing.T) {
	vu := &VUser{}
	if err := vu.replaceRuntimeCredentialScope("test", []string{"two", "one"}); err != nil {
		t.Fatal(err)
	}
	first := vu.RedactionConfig
	if err := vu.replaceRuntimeCredentialScope("test", []string{"one", "two", "one"}); err != nil {
		t.Fatal(err)
	}
	if vu.RedactionConfig != first {
		t.Fatal("unchanged normalized scope rebuilt the runtime matcher")
	}
}

func TestRuntimeSecretRedactionBoundsOptionalVariantsWithoutDroppingRawValues(t *testing.T) {
	values := make([]string, 200)
	for index := range values {
		values[index] = fmt.Sprintf("%04d-%s", index, strings.Repeat(`a/"<&'?`, 570))
	}
	config, err := WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		t.Fatalf("raw values within bounds were rejected because of optional variants: %v", err)
	}
	if config.runtimePatternBytes > maxRuntimeSecretRedactionPatternBytes {
		t.Fatalf("matcher bytes = %d, limit %d", config.runtimePatternBytes, maxRuntimeSecretRedactionPatternBytes)
	}
	for _, value := range []string{values[0], values[len(values)-1]} {
		if got := config.ScrubText("raw=" + value); strings.Contains(got, value) {
			t.Fatalf("mandatory raw value survived bounded matcher: %q", got)
		}
	}
}

func TestRuntimeSecretRedactionOverlayUnionsOverlappingMatchSpans(t *testing.T) {
	for _, test := range []struct {
		name    string
		base    string
		overlay string
	}{
		{name: "long overlay", base: "abc", overlay: "xabcx"},
		{name: "long base", base: "xabcx", overlay: "abc"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, err := WithSecretValueRedactionChecked(nil, []string{test.base})
			if err != nil {
				t.Fatal(err)
			}
			config, err := withRuntimeSecretValueOverlay(base, []string{test.overlay}, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := config.ScrubText("before=xabcx=after")
			if strings.Contains(got, "abc") || strings.Contains(got, "xabcx") {
				t.Fatalf("overlapping layered match survived: %q", got)
			}
		})
	}
}

// Cookie rotation used to grow the per-VUser matcher (bounded by a retirement
// FIFO) and scrub every rotated value out of the run's evidence. The real HTTP
// adapter drives this test so the whole harvest path is exercised: 640 rotations
// must leave the matcher byte-for-byte the platform-secret matcher it started
// with, and every rotated value must remain readable in the run evidence.
func TestVUserCookieRotationLeavesTheRuntimeMatcherUntouched(t *testing.T) {
	const (
		rotations      = 640
		platformSecret = "vault-issued-bravo-2277"
	)
	rotation := 0
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		stdhttp.SetCookie(w, &stdhttp.Cookie{
			Name:  "session",
			Value: fmt.Sprintf("rotation-delta-%04d", rotation),
			Path:  "/",
		})
		rotation++
		w.WriteHeader(stdhttp.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	proto, err := protocol.Get("http")
	if err != nil {
		t.Fatalf("protocol.Get(http): %v", err)
	}
	if err := proto.Init(ctx, map[string]any{"base_url": server.URL}); err != nil {
		t.Fatalf("http init: %v", err)
	}
	defer proto.Teardown(ctx)

	baseConfig := WithSecretValueRedaction(nil, []string{platformSecret})
	vu := &VUser{
		Metrics:         NewMetricsCollector(1),
		Variables:       map[string]string{},
		ResultSink:      &captureResultSink{},
		RedactionConfig: baseConfig,
	}
	previous := ""
	for index := 0; index < rotations; index++ {
		result, err := proto.Execute(ctx, &protocol.Action{
			Name: "rotate", Method: stdhttp.MethodGet, Target: "/rotate",
		})
		if err != nil {
			t.Fatalf("rotation %d execute: %v", index, err)
		}
		current := fmt.Sprintf("rotation-delta-%04d", index)
		evidence := "upstream returned " + current + " alongside " + platformSecret
		if previous != "" {
			evidence += " previously " + previous
		}
		result.Success = false
		result.ErrorMessage = evidence
		result.ResponseBody = evidence
		vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
		if vu.runtimeCredentialRedactionErr != nil {
			t.Fatalf("rotation %d failed redaction capacity: %v", index, vu.runtimeCredentialRedactionErr)
		}
		// Previously asserted that current and previous had both been scrubbed out
		// of the retained evidence.
		for _, value := range []string{current, previous} {
			if value != "" && (!strings.Contains(result.ErrorMessage, value) ||
				!strings.Contains(result.ResponseBody, value)) {
				t.Fatalf("rotation %d scrubbed harvested cookie %q out of %#v", index, value, result)
			}
		}
		if strings.Contains(result.ErrorMessage, platformSecret) ||
			strings.Contains(result.ResponseBody, platformSecret) {
			t.Fatalf("rotation %d leaked the platform-managed secret: %#v", index, result)
		}
		previous = current
	}
	// The bounded retirement FIFO existed only to keep the harvest path from
	// growing the matcher forever. Nothing is harvested, so the matcher is never
	// rebuilt at all.
	if vu.RedactionConfig != baseConfig {
		t.Fatal("cookie rotation rebuilt the per-VUser redaction matcher")
	}
	if got := len(allRuntimeSecretValues(vu.RedactionConfig)); got != 1 {
		t.Fatalf("matcher values after %d rotations = %d, want only the platform-managed secret", rotations, got)
	}
}

func TestRuntimeSecretRedactionRejectsDistinctAndRawValueLimitsBeforeCompilation(t *testing.T) {
	values := make([]string, maxRuntimeSecretRedactionValues+1)
	for index := range values {
		values[index] = fmt.Sprintf("runtime-credential-%04d", index)
	}
	if _, err := WithSecretValueRedactionChecked(nil, values); err == nil ||
		!strings.Contains(err.Error(), "value limit") {
		t.Fatalf("distinct-value admission error = %v", err)
	}

	if _, err := WithSecretValueRedactionChecked(
		nil,
		[]string{strings.Repeat("r", maxRuntimeSecretRedactionRawBytes+1)},
	); err == nil || !strings.Contains(err.Error(), "raw-value limit") {
		t.Fatalf("raw-value admission error = %v", err)
	}
}

func TestRuntimeSecretRedactionAcceptsMoreThanLegacyValueLimit(t *testing.T) {
	values := make([]string, 640)
	for index := range values {
		values[index] = fmt.Sprintf("imported-credential-%04d", index)
	}
	config, err := WithSecretValueRedactionChecked(nil, values)
	if err != nil {
		t.Fatalf("valid imported values exceeded legacy 512-value capacity: %v", err)
	}
	for _, value := range []string{values[0], values[511], values[len(values)-1]} {
		if got := config.ScrubText("retained=" + value); strings.Contains(got, value) {
			t.Fatalf("imported value survived runtime redaction: %q", got)
		}
	}
}

func TestRuntimeSecretRedactionAddsRotatingValuesInBoundedChunks(t *testing.T) {
	var config *RedactionConfig
	values := make([]string, maxRuntimeSecretRedactionReplacers*3)
	for index := range values {
		values[index] = fmt.Sprintf("rotating-credential-%04d-unique", index)
		var err error
		config, err = WithSecretValueRedactionChecked(config, []string{values[index]})
		if err != nil {
			t.Fatalf("add rotating value %d: %v", index, err)
		}
		if len(config.secretReplacers) > maxRuntimeSecretRedactionReplacers {
			t.Fatalf("replacer chunks = %d, limit %d", len(config.secretReplacers), maxRuntimeSecretRedactionReplacers)
		}
	}
	for _, value := range []string{values[0], values[len(values)/2], values[len(values)-1]} {
		if got := config.ScrubText("echo=" + value); strings.Contains(got, value) ||
			!strings.Contains(got, redactedPayloadValue) {
			t.Fatalf("rotating value survived bounded matcher: %q", got)
		}
	}
}

func TestRuntimeSecretRedactionReusesCompleteMatcherForDuplicateValues(t *testing.T) {
	config, err := WithSecretValueRedactionChecked(nil, []string{"stable-basic-auth-value"})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.secretReplacers) != 1 {
		t.Fatalf("secret replacers = %d, want 1", len(config.secretReplacers))
	}
	replacer := config.secretReplacers[0]

	reused, err := WithSecretValueRedactionChecked(config, []string{
		"",
		"stable-basic-auth-value",
		"stable-basic-auth-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused != config || reused.secretReplacers[0] != replacer {
		t.Fatal("duplicate runtime secret values rebuilt the exact-value matcher")
	}
}

func TestVUserRecordResultKeepsRawVariablesButScrubsNamedExtractionsAndTypedEvidence(t *testing.T) {
	sink := &captureResultSink{}
	vu := &VUser{
		Metrics:    NewMetricsCollector(2),
		ResultSink: sink,
		Variables:  map[string]string{},
	}
	result := &protocol.Result{
		Success: true,
		Extractions: map[string]string{
			"jms_settlement_token": "x",
			"client%5Fsecret":      "y",
			"soap_operation":       "CreateOrder",
		},
		ProtocolEvidence: map[string]any{
			"network": typedSecretEvidence{
				SettlementToken: "z",
				Safe:            "delivered",
				Events:          []typedSecretEvidenceEvent{{CSRFToken: "c", Message: "ok"}},
			},
		},
	}
	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if vu.Variables["jms_settlement_token"] != "x" || vu.Variables["client%5Fsecret"] != "y" {
		t.Fatalf("runtime variables lost raw values: %#v", vu.Variables)
	}
	if sink.result.Extractions["jms_settlement_token"] != redactedPayloadValue ||
		sink.result.Extractions["client%5Fsecret"] != redactedPayloadValue ||
		sink.result.Extractions["soap_operation"] != "CreateOrder" {
		t.Fatalf("projected extractions = %#v", sink.result.Extractions)
	}
	encoded, err := json.Marshal(sink.result.ProtocolEvidence)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{`"settlement_token":"z"`, `"csrf_token":"c"`} {
		if strings.Contains(text, secret) {
			t.Fatalf("typed evidence leaked %s in %s", secret, text)
		}
	}
	for _, safe := range []string{"delivered", "ok"} {
		if !strings.Contains(text, safe) {
			t.Fatalf("typed evidence lost safe value %q: %s", safe, text)
		}
	}
}

func TestVUserRecordResultDropsSuccessfulResponseBodyUnlessExtendedLoggingNeedsIt(t *testing.T) {
	vu := &VUser{
		Group:     "Checkout",
		Metrics:   NewMetricsCollector(1),
		Variables: map[string]string{},
	}

	plainSuccess := &protocol.Result{Success: true, ResponseBody: `{"access_token":"secret-token","status":"ok"}`}
	vu.recordResult(plainSuccess, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if plainSuccess.ResponseBody != "" {
		t.Fatalf("plain success response body = %q, want dropped", plainSuccess.ResponseBody)
	}

	extendedSuccess := &protocol.Result{Success: true, ResponseBody: `{"access_token":"secret-token","status":"ok"}`}
	vu.recordResult(extendedSuccess, dsl.ErrorHandlingConfig{}, dsl.LogConfig{
		Level:           "extended",
		ExtendedOptions: []string{"data_returned"},
	})
	if !strings.Contains(extendedSuccess.ResponseBody, `"access_token":"[REDACTED]"`) {
		t.Fatalf("extended success response body = %q, want retained and scrubbed", extendedSuccess.ResponseBody)
	}
}
