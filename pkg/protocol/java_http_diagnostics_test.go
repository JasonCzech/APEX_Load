package protocol

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func javaDiagnosticFixture() *JavaHTTPDiagnostics {
	size := func(n int64) *int64 { return &n }
	duration := func(n float64) *float64 { return &n }
	return &JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "decoded", RequestEncodedBytes: 8, RequestDecodedBytes: size(4), PreparedRequestBodyBytes: size(4), AppliedRequestCompression: "none", ResponseState: "complete", ResponseRetainedBytes: size(3), ResponseBase64Bytes: size(4), Base64DecodeMs: duration(1), Base64EncodeMs: duration(2), EnclosingMs: 10, ObjectFootprint: "unavailable_embedded_opaque_bytes"}
}
func TestJavaHTTPDiagnosticsRejectInvalidOrInventedMeasurements(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*JavaHTTPDiagnostics)
	}{
		{"version", func(d *JavaHTTPDiagnostics) { d.Version = 2 }},
		{"mode", func(d *JavaHTTPDiagnostics) { d.Mode = "jvm" }},
		{"footprint", func(d *JavaHTTPDiagnostics) { d.ObjectFootprint = "measured" }},
		{"impossible decoded size", func(d *JavaHTTPDiagnostics) { *d.RequestDecodedBytes = 7 }},
		{"impossible encoded response", func(d *JavaHTTPDiagnostics) { *d.ResponseBase64Bytes = 3 }},
		{"negative bytes", func(d *JavaHTTPDiagnostics) { d.RequestEncodedBytes = -1 }},
		{"unbounded bytes", func(d *JavaHTTPDiagnostics) { *d.RequestDecodedBytes = 1<<40 + 1 }},
		{"nan duration", func(d *JavaHTTPDiagnostics) { *d.Base64DecodeMs = math.NaN() }},
		{"infinite enclosing", func(d *JavaHTTPDiagnostics) { d.EnclosingMs = math.Inf(1) }},
		{"conversion outside interval", func(d *JavaHTTPDiagnostics) { d.EnclosingMs = 2 }},
		{"invented response after failed decode", func(d *JavaHTTPDiagnostics) { d.InputState = "decode_failed"; d.RequestDecodedBytes = nil }},
		{"unsupported conversion timed", func(d *JavaHTTPDiagnostics) { d.InputState = "unsupported_encoding" }},
		{"unknown compression", func(d *JavaHTTPDiagnostics) { d.AppliedRequestCompression = "source-secret" }},
		{"compression without body", func(d *JavaHTTPDiagnostics) { d.PreparedRequestBodyBytes = nil }},
		{"unavailable is not zero", func(d *JavaHTTPDiagnostics) { d.ResponseState = "unavailable" }},
		{"discarded response retains bytes", func(d *JavaHTTPDiagnostics) { d.ResponseState = "discarded" }},
		{"encoded bytes without work", func(d *JavaHTTPDiagnostics) { d.Base64EncodeMs = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := javaDiagnosticFixture()
			tc.change(d)
			if d.Valid() {
				t.Fatal("invalid diagnostic admitted", d)
			}
		})
	}
}
func TestJavaHTTPDiagnosticsCloneAndOptionalZeros(t *testing.T) {
	d := javaDiagnosticFixture()
	if !d.Valid() {
		t.Fatal(d)
	}
	c := d.Clone()
	*d.RequestDecodedBytes = 999
	*d.Base64DecodeMs = 999
	if *c.RequestDecodedBytes != 4 || *c.Base64DecodeMs != 1 || !c.Valid() {
		t.Fatal("clone aliases source", c)
	}
	*c.RequestDecodedBytes = 0
	*c.PreparedRequestBodyBytes = 0
	*c.ResponseRetainedBytes = 0
	*c.ResponseBase64Bytes = 0
	*c.Base64DecodeMs = 0
	*c.Base64EncodeMs = 0
	c.EnclosingMs = 0
	if !c.Valid() {
		t.Fatal("measured zero refused", c)
	}
	if _, ok := c.Base64WorkShare(); ok {
		t.Fatal("zero enclosing interval became ratio")
	}
	raw, err := json.Marshal(c)
	if err != nil || !strings.Contains(string(raw), `"request_decoded_bytes":0`) || !strings.Contains(string(raw), `"base64_decode_ms":0`) {
		t.Fatal(string(raw), err)
	}
	c.EnclosingMs = 10
	if value, ok := c.Base64WorkShare(); !ok || value != 0 {
		t.Fatal("measured zero work lost", value, ok)
	}
	c.Base64EncodeMs = nil
	c.ResponseBase64Bytes = nil
	if _, ok := c.Base64WorkShare(); ok {
		t.Fatal("missing encode converted to zero")
	}
	c = javaDiagnosticFixture()
	if value, ok := c.Base64WorkShare(); !ok || value != 0.3 {
		t.Fatal("wrong enclosing denominator", value, ok)
	}
}
