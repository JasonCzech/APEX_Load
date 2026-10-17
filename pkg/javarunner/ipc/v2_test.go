package ipc

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func harnessSummaryFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/harness-v2-summary.json")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestParseBoundSummaryPreservesHarnessResult(t *testing.T) {
	fixture := harnessSummaryFixture(t)
	for _, tt := range []struct {
		name, output string
		want         bool
	}{
		{"complete", fixture, true},
		{"noise and partial tail", "JVM output\n" + fixture + "\n{\"success\":", true},
		{"other invocation after result", fixture + "\n" + strings.ReplaceAll(fixture, "invocation-42", "later-invocation"), true},
		{"other invocation alone", strings.ReplaceAll(fixture, "invocation-42", "other"), false},
		{"wrong contract", strings.ReplaceAll(fixture, ContractV2, "legacy"), false},
		{"wrong frame kind", strings.ReplaceAll(fixture, "result", "capabilities"), false},
		{"missing explicit success", strings.ReplaceAll(fixture, `"success":false,`, ""), false},
		{"null success", strings.ReplaceAll(fixture, `"success":false`, `"success":null`), false},
		{"invalid duration type", strings.ReplaceAll(fixture, `"duration_ms":123`, `"duration_ms":"123"`), false},
		{"empty", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseBoundSummary(tt.output, "invocation-42")
			if ok != tt.want {
				t.Fatalf("accepted = %v, want %v", ok, tt.want)
			}
			if !ok {
				return
			}
			if got.Success == nil || *got.Success || got.Error != "iteration failed" || got.ClassName != "Actions" || got.Method != "action" || got.DurationMS != 123 || !got.EventFallbackUsed || got.EventsTruncated {
				t.Fatalf("lost harness fields: %#v", got)
			}
		})
	}
	latest := strings.ReplaceAll(fixture, `"duration_ms":123`, `"duration_ms":456`)
	got, ok := ParseBoundSummary(fixture+"\n"+latest, "invocation-42")
	if !ok || got.DurationMS != 456 {
		t.Fatalf("last result = %#v, %v", got, ok)
	}
}

func TestPersistentEnvelopeWireRoundTrip(t *testing.T) {
	// These are the fields emitted by the Harness persistent-session result, including the
	// invocation fields used by local result evidence and ignored by the sidecar.
	wire := `{"contract":"apexload.java_vuser.ipc.v2","kind":"result","request_id":"request","frame_token":"frame","success":false,"error":"failed","class_name":"Actions","method":"action","duration_ms":7,"stdout":"out","stderr":"err","stdout_truncated":true,"stderr_truncated":true,"event_fallback_used":true,"events_truncated":true,"fatal":true}`
	var envelope PersistentEnvelope
	if err := json.Unmarshal([]byte(wire), &envelope); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(wire), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire fields changed: %s", encoded)
	}
}

func TestEventCaptureCompletenessIsSharedAcrossResultModes(t *testing.T) {
	for bits := range 16 {
		captured, reported, fallback, stderr := bits&1 != 0, bits&2 != 0, bits&4 != 0, bits&8 != 0
		want := captured || reported || (fallback && stderr)
		summary := Summary{EventsTruncated: reported, EventFallbackUsed: fallback}
		envelope := PersistentEnvelope{EventsTruncated: reported, EventFallbackUsed: fallback, StderrTruncated: stderr}
		sidecarSummary := SidecarSummary{EventsTruncated: reported, EventFallbackUsed: fallback}
		sidecarEnvelope := SidecarPersistentEnvelope{EventsTruncated: reported, EventFallbackUsed: fallback, StderrTruncated: stderr}
		if summary.EventCaptureTruncated(captured, stderr) != want || envelope.EventCaptureTruncated(captured) != want || sidecarSummary.EventCaptureTruncated(captured, stderr) != want || sidecarEnvelope.EventCaptureTruncated(captured) != want {
			t.Fatalf("summary/session completeness differs for capture/report/fallback/stderr bits %04b", bits)
		}
	}
}

func TestSidecarResultsPreserveIgnoredLocalMetadata(t *testing.T) {
	fixture := harnessSummaryFixture(t)
	for _, replacement := range []struct{ old, value string }{
		{`"duration_ms":123`, `"duration_ms":"123"`},
		{`"duration_ms":123`, `"duration_ms":9223372036854775808`},
		{`"class_name":"Actions"`, `"class_name":42`},
		{`"method":"action"`, `"method":[]`},
	} {
		wire := strings.ReplaceAll(fixture, replacement.old, replacement.value)
		if _, ok := ParseBoundSummary(wire, "invocation-42"); ok {
			t.Fatalf("local metadata became permissive: %s", wire)
		}
		sidecar, ok := ParseBoundSidecarSummary(wire, "invocation-42")
		if !ok || sidecar.Success == nil || *sidecar.Success || sidecar.Error != "iteration failed" || !sidecar.EventFallbackUsed {
			t.Fatalf("sidecar stopped ignoring local metadata: %#v, %v", sidecar, ok)
		}
		var local PersistentEnvelope
		if err := json.Unmarshal([]byte(wire), &local); err == nil {
			t.Fatalf("local session accepted malformed metadata: %s", wire)
		}
		var session SidecarPersistentEnvelope
		if err := json.Unmarshal([]byte(wire), &session); err != nil || session.RequestID != "invocation-42" || session.Success || session.Error != "iteration failed" {
			t.Fatalf("sidecar session parsing changed: %#v, %v", session, err)
		}
	}
}

func TestSidecarSummaryUsesSharedResultBinding(t *testing.T) {
	fixture := harnessSummaryFixture(t)
	for _, wire := range []string{
		strings.ReplaceAll(fixture, "invocation-42", "other"),
		strings.ReplaceAll(fixture, ContractV2, "legacy"),
		strings.ReplaceAll(fixture, "result", "capabilities"),
		strings.ReplaceAll(fixture, `"success":false`, `"success":null`),
	} {
		if _, ok := ParseBoundSidecarSummary(wire, "invocation-42"); ok {
			t.Fatalf("unbound sidecar result accepted: %s", wire)
		}
	}
	got, ok := ParseBoundSidecarSummary("noise\n"+fixture+"\n{}\n{", "invocation-42")
	if !ok || got.RequestID != "invocation-42" {
		t.Fatalf("sidecar tail decoding changed: %#v, %v", got, ok)
	}
}
