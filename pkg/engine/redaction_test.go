package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
			name: "redacts structured fields",
			in:   `{"access_token":"abc123","status":"ok","ssn":"123-45-6789"}`,
			want: `{"access_token":"[REDACTED]","status":"ok","ssn":"[REDACTED]"}`,
		},
		{
			name: "redacts xml and query payloads",
			in:   `<token>abc123</token> https://example.test?session_id=secret-123`,
			want: `<token>[REDACTED]</token> https://example.test?session_id=[REDACTED]`,
		},
		{
			name: "redacts high entropy tokens",
			in:   `eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature`,
			want: `[REDACTED]`,
		},
		{
			name: "does not redact ordinary hostnames",
			in:   `https://archive.example.test/customers/customer-1234`,
			want: `https://archive.example.test/customers/customer-1234`,
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
	if strings.Contains(sink.result.PageURL, "abc123") || strings.Contains(sink.result.PageTitle, "123-45-6789") {
		t.Fatalf("page evidence leaked: url=%q title=%q", sink.result.PageURL, sink.result.PageTitle)
	}
	if strings.Contains(sink.result.ConsoleEntries[0].Text, "console-secret") || strings.Contains(sink.result.ConsoleEntries[0].Source, "source-secret") {
		t.Fatalf("console entries leaked: %#v", sink.result.ConsoleEntries)
	}
	if strings.Contains(sink.result.DOMSummary.Title, "123-45-6789") || strings.Contains(strings.Join(sink.result.DOMSummary.VisibleText, " "), "visible-secret") {
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
	if !strings.Contains(sink.result.Snapshot.ResponseBody, `"patient_id":"[REDACTED]"`) {
		t.Fatalf("response body = %q, want patient id redacted", sink.result.Snapshot.ResponseBody)
	}
	if !strings.Contains(sink.result.ResponseBody, `"access_token":"[REDACTED]"`) {
		t.Fatalf("result response body = %q, want access token redacted", sink.result.ResponseBody)
	}
	if strings.Contains(sink.result.Snapshot.HTML, "123-45-6789") {
		t.Fatalf("html = %q, want SSN redacted", sink.result.Snapshot.HTML)
	}
	if strings.Contains(sink.result.Snapshot.PageURL, "snapshot-secret") || strings.Contains(sink.result.Snapshot.PageTitle, "123-45-6789") {
		t.Fatalf("snapshot page evidence leaked: url=%q title=%q", sink.result.Snapshot.PageURL, sink.result.Snapshot.PageTitle)
	}
	if strings.Contains(sink.result.Snapshot.ConsoleEntries[0].Text, "snapshot-console-secret") ||
		strings.Contains(sink.result.Snapshot.ConsoleEntries[0].Source, "snapshot-source-secret") {
		t.Fatalf("snapshot console leaked: %#v", sink.result.Snapshot.ConsoleEntries)
	}
	if strings.Contains(sink.result.Snapshot.DOMSummary.Title, "123-45-6789") ||
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

func TestVUserRecordResultScrubsMetricAndArchiveMetadata(t *testing.T) {
	const secret = "customer-secret-42"
	config, err := NewRedactionConfig([]RedactionRule{{Pattern: secret}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &captureResultSink{}
	vu := &VUser{
		Group:           "group-" + secret,
		Metrics:         NewMetricsCollector(2),
		ResultSink:      sink,
		Variables:       map[string]string{},
		RedactionConfig: config,
	}
	result := &protocol.Result{
		ActionName:        "action-" + secret,
		Region:            "region-" + secret,
		CorrelationID:     "correlation-" + secret,
		NetworkProfile:    "network-" + secret,
		DependencyType:    "dependency-" + secret,
		DependencyTarget:  "target-" + secret,
		SourceStepID:      "step-" + secret,
		SourceAction:      "source-action-" + secret,
		SourceActionName:  "source-name-" + secret,
		SourceTransaction: "transaction-" + secret,
		SourceProfile:     "profile-" + secret,
		Success:           true,
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
	values := []string{
		sink.result.ActionName, sink.result.Group, sink.result.Region,
		sink.result.CorrelationID, sink.result.NetworkProfile,
		sink.result.DependencyType, sink.result.DependencyTarget,
		sink.result.SourceStepID, sink.result.SourceAction,
		sink.result.SourceActionName, sink.result.SourceTransaction,
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
		Extractions:      map[string]string{"ordinary": "extraction-" + secret},
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

func TestRuntimeSecretRedactionDoesNotAmplifyShortMatches(t *testing.T) {
	config, err := WithSecretValueRedactionChecked(nil, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("a", 1<<20)
	got := config.ScrubText(input)
	if len(got) > len(input) {
		t.Fatalf("one-byte secret expanded evidence from %d to %d bytes", len(input), len(got))
	}
	if strings.Contains(got, "a") {
		t.Fatal("one-byte runtime secret survived exact redaction")
	}
}

func TestRuntimeSecretRedactionRejectsUnboundedExpandedMatchers(t *testing.T) {
	values := make([]string, maxCachedSecrets)
	for index := range values {
		values[index] = fmt.Sprintf("%04d-%s", index, strings.Repeat(`a/"<&'?`, 570))
	}
	if _, err := WithSecretValueRedactionChecked(nil, values); err == nil || !strings.Contains(err.Error(), "matcher limit") {
		t.Fatalf("expanded matcher admission error = %v", err)
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
