package protocol

import "math"

// JavaHTTPDiagnostics v1 measures the embedded adapter's opaque representations
// and base64 work. It does not deserialize Java objects or measure JVM footprint.
// Byte observations exclude framing/retries and do not establish delivery.
// Response bytes describe the retained, possibly decoded/formatted representation
// supplied to the wrapper; availability is explicit and old fields are unchanged.
type JavaHTTPDiagnostics struct {
	Version                   int      `json:"version"`
	Mode                      string   `json:"mode"`
	InputState                string   `json:"input_state"`
	RequestEncodedBytes       int64    `json:"request_encoded_bytes"`
	RequestDecodedBytes       *int64   `json:"request_decoded_bytes,omitempty"`
	PreparedRequestBodyBytes  *int64   `json:"prepared_request_body_bytes,omitempty"`
	AppliedRequestCompression string   `json:"applied_request_compression,omitempty"`
	ResponseState             string   `json:"response_state"`
	ResponseRetainedBytes     *int64   `json:"response_retained_bytes,omitempty"`
	ResponseBase64Bytes       *int64   `json:"response_base64_bytes,omitempty"`
	Base64DecodeMs            *float64 `json:"base64_decode_ms,omitempty"`
	Base64EncodeMs            *float64 `json:"base64_encode_ms,omitempty"`
	EnclosingMs               float64  `json:"enclosing_ms"`
	ObjectFootprint           string   `json:"object_footprint"`
}

func (d *JavaHTTPDiagnostics) Valid() bool {
	if d == nil || d.Version != 1 || d.Mode != "embedded_opaque_bytes" || d.ObjectFootprint != "unavailable_embedded_opaque_bytes" {
		return false
	}
	switch d.InputState {
	case "unsupported_encoding", "decode_failed", "decoded", "empty":
	default:
		return false
	}
	switch d.ResponseState {
	case "unavailable", "complete", "collection_window", "discarded":
	default:
		return false
	}
	if d.RequestEncodedBytes < 0 || d.RequestEncodedBytes > 1<<40 {
		return false
	}
	for _, n := range []*int64{d.RequestDecodedBytes, d.PreparedRequestBodyBytes, d.ResponseRetainedBytes, d.ResponseBase64Bytes} {
		if n != nil && (*n < 0 || *n > 1<<40) {
			return false
		}
	}
	validMs := func(n float64) bool { return n >= 0 && n <= 24*60*60*1000 && !math.IsNaN(n) && !math.IsInf(n, 0) }
	if !validMs(d.EnclosingMs) {
		return false
	}
	work := 0.0
	for _, ms := range []*float64{d.Base64DecodeMs, d.Base64EncodeMs} {
		if ms != nil {
			if !validMs(*ms) {
				return false
			}
			work += *ms
		}
	}
	if work > d.EnclosingMs {
		return false
	}
	if d.PreparedRequestBodyBytes == nil {
		if d.AppliedRequestCompression != "" {
			return false
		}
	} else {
		if d.RequestDecodedBytes == nil {
			return false
		}
		switch d.AppliedRequestCompression {
		case "none", "gzip", "deflate":
		default:
			return false
		}
	}
	switch d.InputState {
	case "unsupported_encoding":
		if d.RequestDecodedBytes != nil || d.Base64DecodeMs != nil {
			return false
		}
	case "decode_failed":
		if d.RequestDecodedBytes != nil || d.Base64DecodeMs == nil {
			return false
		}
	case "empty":
		if d.RequestEncodedBytes != 0 || d.RequestDecodedBytes == nil || *d.RequestDecodedBytes != 0 || d.Base64DecodeMs != nil {
			return false
		}
	case "decoded":
		if d.RequestEncodedBytes == 0 || d.RequestDecodedBytes == nil || d.Base64DecodeMs == nil || *d.RequestDecodedBytes > (d.RequestEncodedBytes/4)*3 {
			return false
		}
	}
	if d.RequestDecodedBytes == nil && (d.ResponseState != "unavailable" || d.ResponseRetainedBytes != nil || d.ResponseBase64Bytes != nil || d.Base64EncodeMs != nil) {
		return false
	}
	if d.ResponseState == "unavailable" && (d.ResponseRetainedBytes != nil || d.ResponseBase64Bytes != nil) {
		return false
	}
	if d.ResponseState != "unavailable" && d.ResponseRetainedBytes == nil {
		return false
	}
	if d.ResponseState == "discarded" && *d.ResponseRetainedBytes != 0 {
		return false
	}
	if d.ResponseBase64Bytes != nil && (d.Base64EncodeMs == nil || d.ResponseRetainedBytes == nil || *d.ResponseBase64Bytes != ((*d.ResponseRetainedBytes+2)/3)*4) {
		return false
	}
	return true
}

func (d *JavaHTTPDiagnostics) Clone() *JavaHTTPDiagnostics {
	if d == nil {
		return nil
	}
	out := *d
	out.RequestDecodedBytes = cloneJavaDiagnosticValue(d.RequestDecodedBytes)
	out.PreparedRequestBodyBytes = cloneJavaDiagnosticValue(d.PreparedRequestBodyBytes)
	out.ResponseRetainedBytes = cloneJavaDiagnosticValue(d.ResponseRetainedBytes)
	out.ResponseBase64Bytes = cloneJavaDiagnosticValue(d.ResponseBase64Bytes)
	out.Base64DecodeMs = cloneJavaDiagnosticValue(d.Base64DecodeMs)
	out.Base64EncodeMs = cloneJavaDiagnosticValue(d.Base64EncodeMs)
	return &out
}

// Retained keeps absence distinct from refused or unknown evidence, without
// retaining unvalidated peer strings or pointers. A zero-version marker is
// explicitly invalid and contributes only to invalid-sample accounting.
func (d *JavaHTTPDiagnostics) Retained() *JavaHTTPDiagnostics {
	if d == nil {
		return nil
	}
	if !d.Valid() {
		return &JavaHTTPDiagnostics{}
	}
	return d.Clone()
}
func cloneJavaDiagnosticValue[T ~int64 | ~float64](value *T) *T {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

// Base64WorkShare uses the enclosing wrapper interval, which includes both
// conversions. Missing operations or a zero denominator remain unavailable.
// It is conversion work, not Java object serialization or server processing.
func (d *JavaHTTPDiagnostics) Base64WorkShare() (float64, bool) {
	if !d.Valid() || d.EnclosingMs <= 0 || d.Base64DecodeMs == nil || d.Base64EncodeMs == nil {
		return 0, false
	}
	return (*d.Base64DecodeMs + *d.Base64EncodeMs) / d.EnclosingMs, true
}
