package javasidecar

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

func brokerResult(t *testing.T, out ExecutionResponse) broker.SupervisorResponse {
	t.Helper()
	data, e := json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	return broker.SupervisorResponse{InvocationID: "host-call-1", Payload: data}
}
func brokerEvent(sequence int, extra string) string {
	return `{"contract":"` + harnessEventContract + `","request_id":"host-call-1","sequence":` + strconv.Itoa(sequence) + `,"type":"runtime","name":"exit","message":"2 1"` + extra + `}`
}
func TestBrokerTranslationPreservesOutputTimingExitAndAttribution(t *testing.T) {
	first := brokerEvent(1, `,"transaction_id":"tx","duration_ms":17`)
	second := brokerEvent(2, `,"parent_transaction_id":"tx","wasted_time_ms":3`)
	original := ExecutionResponse{Contract: ContractVersion, ExecutionID: "host-call-1", Success: true, DurationMS: 31, Stdout: "user text host-call-1", Stderr: "user text host-call-1\n" + first + "\n" + second + "\n", Events: first + "\n"}
	got, e := translateBrokerResult(brokerResult(t, original), "generator-call-1", "session-1")
	if e != nil {
		t.Fatal(e)
	}
	if got.ExecutionID != "generator-call-1" || got.Stdout != original.Stdout || got.Stderr != original.Stderr || got.DurationMS != 31 {
		t.Fatal("output or timing changed", got)
	}
	lines := strings.Split(strings.TrimSpace(got.Events), "\n")
	if len(lines) != 2 {
		t.Fatal("fallback not merged/deduplicated", got.Events)
	}
	var a, b map[string]any
	if json.Unmarshal([]byte(lines[0]), &a) != nil || json.Unmarshal([]byte(lines[1]), &b) != nil {
		t.Fatal("invalid translated events")
	}
	if a["request_id"] != "generator-call-1" || a["transaction_id"] != "session-1:generator-call-1:tx" || a["duration_ms"] != float64(17) || b["parent_transaction_id"] != a["transaction_id"] || b["message"] != "2 1" {
		t.Fatal("attribution/exit/timing lost", got.Events)
	}
}
func TestBrokerTranslationRejectsUnboundAndMalformedPayloads(t *testing.T) {
	base := ExecutionResponse{Contract: ContractVersion, ExecutionID: "host-call-1", Success: true, Events: brokerEvent(1, "") + "\n"}
	for _, tc := range []struct {
		name   string
		mutate func(*ExecutionResponse)
	}{
		{"identity", func(r *ExecutionResponse) { r.ExecutionID = "other" }},
		{"contract", func(r *ExecutionResponse) { r.Contract = "unknown" }},
		{"error", func(r *ExecutionResponse) { r.ErrorCode = "session_unusable" }},
		{"missing_failure", func(r *ExecutionResponse) { r.Success = false }},
		{"negative_timing", func(r *ExecutionResponse) { r.DurationMS = -1 }},
		{"capture_limit", func(r *ExecutionResponse) { r.Stdout = strings.Repeat("x", MaxCaptureBytes+1) }},

		{"event_malformed", func(r *ExecutionResponse) { r.Events = "{bad json}\n" }},
		{"conflicting_fallback", func(r *ExecutionResponse) { r.Stderr = brokerEvent(1, `,"duration_ms":9`) + "\n" }},
		{"duration_shape", func(r *ExecutionResponse) { r.Events = brokerEvent(1, `,"duration_ms":"bad"`) + "\n" }},
		{"negative_duration", func(r *ExecutionResponse) { r.Events = brokerEvent(1, `,"duration_ms":-1`) + "\n" }},
		{"message_shape", func(r *ExecutionResponse) {
			r.Events = strings.ReplaceAll(brokerEvent(1, ""), `"message":"2 1"`, `"message":null`) + "\n"
		}},
		{"transaction_shape", func(r *ExecutionResponse) { r.Events = brokerEvent(1, `,"transaction_id":7`) + "\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := base
			tc.mutate(&out)
			if _, e := translateBrokerResult(brokerResult(t, out), "generator-call-1", "session"); !errors.Is(e, ErrBrokerSession) {
				t.Fatal("invalid result accepted", e)
			}
		})
	}
	raw := brokerResult(t, base)
	raw.Payload = append(raw.Payload, []byte(`{}`)...)
	if _, e := translateBrokerResult(raw, "call", "session"); e == nil {
		t.Fatal("trailing payload accepted")
	}
}
func TestBrokerTranslationTruncationAndUnattributedDiagnostics(t *testing.T) {
	out := ExecutionResponse{Contract: ContractVersion, ExecutionID: "host-call-1", Success: false, ErrorCode: "harness_reported_failure", Error: "failed", Events: strings.ReplaceAll(brokerEvent(2, ""), "host-call-1", "late-call") + "\n" + brokerEvent(1, "") + "\n{partial", EventsTruncated: true, Stderr: strings.ReplaceAll(brokerEvent(2, ""), "host-call-1", "late-call") + "\n"}
	got, e := translateBrokerResult(brokerResult(t, out), "call", "session")
	if e != nil || !got.EventsTruncated || got.Success || got.Error != "failed" || strings.Count(got.Events, "\n") != 1 || got.Stderr != out.Stderr {
		t.Fatal("truncation/failure/diagnostic attribution", got, e)
	}
}
func TestBrokerTranslationBoundsExpandedIdentities(t *testing.T) {
	event := brokerEvent(1, `,"transaction_id":"`+strings.Repeat("x", MaxCaptureBytes-200)+`"`) + "\n"
	if len(event) > MaxCaptureBytes {
		t.Fatal("test event oversized")
	}
	out := ExecutionResponse{Contract: ContractVersion, ExecutionID: "host-call-1", Success: true, Events: event}
	got, e := translateBrokerResult(brokerResult(t, out), strings.Repeat("a", 128), strings.Repeat("b", 128))
	if e != nil || !got.EventsTruncated || len(got.Events) > MaxCaptureBytes {
		t.Fatal("translated capture unbounded", e)
	}
}
