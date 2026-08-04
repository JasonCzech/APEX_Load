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

func TestVUserRecordResultRegistersProtocolManagedCredentialsBeforePublishing(t *testing.T) {
	const cookieValue = "xy7"
	sink := &captureResultSink{}
	vu := &VUser{
		Metrics:    NewMetricsCollector(4),
		ResultSink: sink,
		Variables:  map[string]string{},
	}
	first := &protocol.Result{
		Success:                 false,
		ErrorMessage:            "upstream echoed " + cookieValue,
		ResponseBody:            "neutral=" + cookieValue,
		RuntimeCredentialValues: []string{cookieValue},
		Extractions:             map[string]string{"ordinary": cookieValue},
		ProtocolEvidence:        map[string]any{"ordinary": cookieValue},
		Snapshot:                &protocol.ErrorSnapshot{ResponseBody: cookieValue},
	}

	vu.recordResult(first, dsl.ErrorHandlingConfig{SnapshotOnError: true}, dsl.LogConfig{})

	if got := vu.Variables["ordinary"]; got != cookieValue {
		t.Fatalf("raw extraction = %q, want protocol-managed credential for later replay", got)
	}
	if first.RuntimeCredentialValues != nil {
		t.Fatalf("memory-only runtime credentials reached retained result: %#v", first.RuntimeCredentialValues)
	}
	encoded, err := json.Marshal(sink.result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(cookieValue)) {
		t.Fatalf("same-response protocol-managed credential leaked: %s", encoded)
	}

	later := &protocol.Result{
		Success:      false,
		ErrorMessage: "later echo " + cookieValue,
		ResponseBody: cookieValue,
		Extractions:  map[string]string{"ordinary_later": cookieValue},
	}
	vu.recordResult(later, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	encoded, err = json.Marshal(sink.result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(cookieValue)) {
		t.Fatalf("later protocol-managed credential echo leaked: %s", encoded)
	}
}

func TestProtocolSessionCookieDoesNotRewriteStableMetricDimensions(t *testing.T) {
	const cookieValue = "Login"
	sink := &captureResultSink{}
	vu := &VUser{
		Group:      "Login group",
		Metrics:    NewMetricsCollector(4),
		ResultSink: sink,
		Variables:  map[string]string{},
	}
	result := &protocol.Result{
		ActionName:                      "Login action",
		DependencyTarget:                "https://example.test/Login",
		CorrelationID:                   cookieValue,
		Success:                         false,
		ErrorMessage:                    "cookie=" + cookieValue,
		ResponseBody:                    "payload=" + cookieValue,
		Extractions:                     map[string]string{"echo": cookieValue},
		ProtocolEvidence:                map[string]any{"echo": cookieValue},
		RuntimeCredentialValues:         []string{cookieValue},
		RuntimeCredentialValuesSnapshot: true,
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if sink.result == nil {
		t.Fatal("result did not reach sink")
	}
	if sink.result.ActionName != "Login action" || sink.result.Group != "Login group" ||
		sink.result.DependencyTarget != "https://example.test/Login" {
		t.Fatalf("session cookie rewrote stable dimensions: action=%q group=%q target=%q",
			sink.result.ActionName, sink.result.Group, sink.result.DependencyTarget)
	}
	if strings.Contains(sink.result.ErrorMessage, cookieValue) ||
		strings.Contains(sink.result.ResponseBody, cookieValue) ||
		strings.Contains(sink.result.Extractions["echo"], cookieValue) {
		t.Fatalf("session cookie survived retained payload evidence: %#v", sink.result)
	}
	if evidence, _ := sink.result.ProtocolEvidence["echo"].(string); strings.Contains(evidence, cookieValue) {
		t.Fatalf("session cookie survived protocol evidence: %#v", sink.result.ProtocolEvidence)
	}
	if strings.Contains(sink.result.CorrelationID, cookieValue) {
		t.Fatalf("session cookie survived correlation evidence: %q", sink.result.CorrelationID)
	}
}

func TestNonSessionRuntimeCredentialStillRedactsStableDimensions(t *testing.T) {
	const credential = "resolved-secret-4821"
	vu := &VUser{
		Group:     "group-" + credential,
		Metrics:   NewMetricsCollector(1),
		Variables: map[string]string{},
	}
	result := &protocol.Result{
		ActionName:              "action-" + credential,
		DependencyTarget:        "https://example.test/" + credential,
		Success:                 true,
		RuntimeCredentialValues: []string{credential},
	}

	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	for name, value := range map[string]string{
		"action": result.ActionName,
		"group":  result.Group,
		"target": result.DependencyTarget,
	} {
		if strings.Contains(value, credential) {
			t.Fatalf("%s dimension retained non-session runtime credential: %q", name, value)
		}
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

func TestGenericJWTRedactionRequiresStructuredHeaderAndPayload(t *testing.T) {
	valid := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl"
	invalid := "documents.example.invalid.assets.bundle123456789"
	missingAlgorithm := "eyJ0eXAiOiJKV1QifQ.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl"
	nonStringAlgorithm := "eyJhbGciOjEyM30.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJl"
	invalidSignature := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature"
	got := scrubSensitiveText(valid + " " + invalid)
	if strings.Contains(got, valid) {
		t.Fatalf("valid JWT survived: %q", got)
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
			config, err := withRuntimeSecretValueOverlay(base, []string{test.overlay})
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

func TestVUserCookieRotationBeyondLegacyLimitRemainsBoundedAndScrubbed(t *testing.T) {
	vu := &VUser{
		Metrics:    NewMetricsCollector(1),
		Variables:  map[string]string{},
		ResultSink: &captureResultSink{},
	}
	const rotations = 640
	previous := ""
	for index := 0; index < rotations; index++ {
		if err := vu.commitRuntimeCredentialSessionSnapshot(); err != nil {
			t.Fatalf("commit rotation %d: %v", index, err)
		}
		current := fmt.Sprintf("cookie-rotation-%04d-opaque", index)
		evidence := "new=" + current
		if previous != "" {
			evidence += ";old=" + previous
		}
		result := &protocol.Result{
			Success:                         false,
			ErrorMessage:                    evidence,
			ResponseBody:                    evidence,
			RuntimeCredentialValues:         []string{current},
			RuntimeCredentialValuesSnapshot: true,
		}
		vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
		if vu.runtimeCredentialRedactionErr != nil {
			t.Fatalf("rotation %d failed redaction capacity: %v", index, vu.runtimeCredentialRedactionErr)
		}
		for _, value := range []string{current, previous} {
			if value != "" && (strings.Contains(result.ErrorMessage, value) ||
				strings.Contains(result.ResponseBody, value)) {
				t.Fatalf("rotation %d retained credential %q in %#v", index, value, result)
			}
		}
		if result.RuntimeCredentialValues != nil || result.RuntimeCredentialValuesSnapshot {
			t.Fatalf("rotation %d retained protocol-only credential side channel", index)
		}
		previous = current
	}
	if err := vu.commitRuntimeCredentialSessionSnapshot(); err != nil {
		t.Fatal(err)
	}
	if got := len(vu.runtimeCredentialSessionHistory); got > maxRuntimeCredentialSessionHistoryValues {
		t.Fatalf("retired session values = %d, limit %d", got, maxRuntimeCredentialSessionHistoryValues)
	}
	if got := vu.runtimeCredentialSessionHistoryBytes; got > maxRuntimeCredentialSessionHistoryBytes {
		t.Fatalf("retired session bytes = %d, limit %d", got, maxRuntimeCredentialSessionHistoryBytes)
	}
	if got := len(vu.RedactionConfig.runtimeSecretValues); got > maxRuntimeCredentialSessionHistoryValues+1 {
		t.Fatalf("session matcher values = %d, want bounded history plus live value", got)
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
