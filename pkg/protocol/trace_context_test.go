package protocol

import "testing"

func TestParseTraceparent(t *testing.T) {
	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "sampled version 00", value: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", ok: true},
		{name: "uppercase is normalized", value: "00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-00", ok: true},
		{name: "future version may append fields", value: "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra", ok: true},
		{name: "version 00 with extra field", value: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra"},
		{name: "invalid version ff", value: "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{name: "all-zero trace id", value: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
		{name: "all-zero span id", value: "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"},
		{name: "short trace id", value: "00-4bf92f3577b34da6-00f067aa0ba902b7-01"},
		{name: "non-hex", value: "00-4bf92f3577b34da6a3ce929d0e0e473z-00f067aa0ba902b7-01"},
		{name: "gateway request id", value: "resp-corr-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			traceID, spanID, flags, ok := ParseTraceparent(tt.value)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && (traceID != "4bf92f3577b34da6a3ce929d0e0e4736" || spanID != "00f067aa0ba902b7" || len(flags) != 2) {
				t.Fatalf("parsed = %q %q %q", traceID, spanID, flags)
			}
		})
	}
}

func TestNewSentTraceContext(t *testing.T) {
	if got := NewSentTraceContext("  ", TraceSourceGenerated, true); got != nil {
		t.Fatalf("empty header = %#v, want nil", got)
	}
	sent := NewSentTraceContext("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", TraceSourceGenerated, true)
	if sent == nil || !sent.Sent || sent.Source != TraceSourceGenerated || sent.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || !sent.Sampled() {
		t.Fatalf("sent context = %#v", sent)
	}
	if unsampled := NewSentTraceContext("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02", TraceSourceAuthored, true); unsampled.Sampled() {
		t.Fatal("flags 02 reported as sampled")
	}
	// A malformed authored header keeps its source but can never be looked up.
	malformed := NewSentTraceContext("not-a-traceparent", TraceSourceAuthored, true)
	if malformed == nil || malformed.Source != TraceSourceAuthored || malformed.TraceID != "" || malformed.SpanID != "" {
		t.Fatalf("malformed context = %#v", malformed)
	}
}
