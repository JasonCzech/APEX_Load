package protocol

import "math"

// JavaHTTPDiagnostics v1 measures the embedded adapter's opaque representations
// and base64 work. It does not deserialize Java objects or measure JVM footprint.
// Byte observations exclude framing/retries and do not establish delivery.
// Response bytes describe the retained, possibly decoded/formatted representation
// supplied to the wrapper; availability is explicit and old fields are unchanged.
type JavaHTTPDiagnostics struct {
	Version                   int                              `json:"version"`
	Mode                      string                           `json:"mode"`
	InputState                string                           `json:"input_state"`
	RequestEncodedBytes       int64                            `json:"request_encoded_bytes"`
	RequestDecodedBytes       *int64                           `json:"request_decoded_bytes,omitempty"`
	PreparedRequestBodyBytes  *int64                           `json:"prepared_request_body_bytes,omitempty"`
	AppliedRequestCompression string                           `json:"applied_request_compression,omitempty"`
	ResponseState             string                           `json:"response_state"`
	ResponseRetainedBytes     *int64                           `json:"response_retained_bytes,omitempty"`
	ResponseBase64Bytes       *int64                           `json:"response_base64_bytes,omitempty"`
	Base64DecodeMs            *float64                         `json:"base64_decode_ms,omitempty"`
	Base64EncodeMs            *float64                         `json:"base64_encode_ms,omitempty"`
	EnclosingMs               float64                          `json:"enclosing_ms"`
	ObjectFootprint           string                           `json:"object_footprint"`
	ContainedObjectFootprint  *JavaHTTPObjectFootprintEvidence `json:"contained_object_footprint,omitempty"`
}

const (
	JavaHTTPShallowObjectMethodV1       = "jvm_instrumentation_shallow.v1"
	JavaHTTPReachableGraphMethodV1      = "jvm_bounded_graph.v1"
	JavaHTTPFieldLayoutFallbackMethodV1 = "jvm_field_layout_fallback.v1"
	JavaHTTPObjectFootprintRetainedNote = "neither java_shallow_object_bytes nor java_reachable_graph_bytes is retained heap size"
)

// JavaHTTPSerializedRepresentationVsHTTPBody pairs the decoded serialized
// representation bytes against the actual HTTP request body bytes on the wire,
// keeping compression/encoding, units, availability, and the outer conversion
// interval explicit.
type JavaHTTPSerializedRepresentationVsHTTPBody struct {
	Metric                     string   `json:"metric"`
	Available                  bool     `json:"available"`
	UnavailableReason          string   `json:"unavailable_reason,omitempty"`
	Compression                string   `json:"compression,omitempty"`
	AppliedRequestCompression  string   `json:"applied_request_compression,omitempty"`
	DecodedRepresentationBytes *int64   `json:"decoded_representation_bytes,omitempty"`
	HTTPBodyBytes              *int64   `json:"http_body_bytes,omitempty"`
	DeltaBytes                 *int64   `json:"delta_bytes,omitempty"`
	BytesUnit                  string   `json:"bytes_unit"`
	BodyPerRepresentationRatio *float64 `json:"body_per_representation_ratio,omitempty"`
	RatioUnit                  string   `json:"ratio_unit"`
	EnclosingMs                *float64 `json:"enclosing_ms,omitempty"`
	DurationUnit               string   `json:"duration_unit"`
}

// JavaHTTPShallowObjectFootprint reports the shallow object size from the
// opt-in contained JVM helper.
type JavaHTTPShallowObjectFootprint struct {
	Metric                    string `json:"metric"`
	Method                    string `json:"method"`
	JVMVersion                string `json:"jvm_version,omitempty"`
	Available                 bool   `json:"available"`
	UnavailableReason         string `json:"unavailable_reason,omitempty"`
	Bytes                     *int64 `json:"bytes,omitempty"`
	Unit                      string `json:"unit"`
	FieldLayoutFallbackBytes  *int64 `json:"field_layout_fallback_bytes,omitempty"`
	FieldLayoutFallbackMethod string `json:"field_layout_fallback_method,omitempty"`
	RetainedHeapNote          string `json:"retained_heap_note"`
}

// JavaHTTPReachableGraphFootprint reports the bounded reachable object graph
// size deduplicated by object identity from the opt-in contained JVM helper.
type JavaHTTPReachableGraphFootprint struct {
	Metric                    string `json:"metric"`
	Method                    string `json:"method"`
	JVMVersion                string `json:"jvm_version,omitempty"`
	Available                 bool   `json:"available"`
	UnavailableReason         string `json:"unavailable_reason,omitempty"`
	Bytes                     *int64 `json:"bytes,omitempty"`
	Unit                      string `json:"unit"`
	UniqueObjects             int64  `json:"unique_objects"`
	MaxNodes                  int    `json:"max_nodes"`
	MaxDepth                  int    `json:"max_depth"`
	TraversalExhausted        bool   `json:"traversal_exhausted"`
	FieldLayoutFallbackBytes  *int64 `json:"field_layout_fallback_bytes,omitempty"`
	FieldLayoutFallbackMethod string `json:"field_layout_fallback_method,omitempty"`
	RetainedHeapNote          string `json:"retained_heap_note"`
}

// JavaHTTPObjectFootprintEvidence carries opt-in contained JVM object-footprint
// and codec timing evidence for an approved Java fixture/codec execution.
type JavaHTTPObjectFootprintEvidence struct {
	FixtureKind           string                          `json:"fixture_kind"`
	JVMVersion            string                          `json:"jvm_version,omitempty"`
	ShallowObject         JavaHTTPShallowObjectFootprint  `json:"java_shallow_object_bytes"`
	ReachableGraph        JavaHTTPReachableGraphFootprint `json:"java_reachable_graph_bytes"`
	CodecExecuted         bool                            `json:"codec_executed"`
	SerializedBytes       *int64                          `json:"serialized_bytes,omitempty"`
	JavaSerializationMs   *float64                        `json:"java_serialization_ms,omitempty"`
	JavaDeserializationMs *float64                        `json:"java_deserialization_ms,omitempty"`
}

func (e *JavaHTTPObjectFootprintEvidence) Valid() bool {
	if e == nil {
		return true
	}
	if e.FixtureKind == "" || len(e.FixtureKind) > 128 || e.JVMVersion == "" || len(e.JVMVersion) > 128 {
		return false
	}
	s := e.ShallowObject
	if s.Metric != "java_shallow_object_bytes" || s.Method != JavaHTTPShallowObjectMethodV1 || s.Unit != "bytes" || s.RetainedHeapNote != JavaHTTPObjectFootprintRetainedNote {
		return false
	}
	if s.Available {
		if s.Bytes == nil || *s.Bytes <= 0 || *s.Bytes > 1<<40 || s.UnavailableReason != "" {
			return false
		}
	} else {
		if s.Bytes != nil || s.UnavailableReason == "" {
			return false
		}
		if s.FieldLayoutFallbackBytes != nil && (*s.FieldLayoutFallbackBytes <= 0 || *s.FieldLayoutFallbackBytes > 1<<40 || s.FieldLayoutFallbackMethod != JavaHTTPFieldLayoutFallbackMethodV1) {
			return false
		}
	}
	g := e.ReachableGraph
	if g.Metric != "java_reachable_graph_bytes" || g.Method != JavaHTTPReachableGraphMethodV1 || g.Unit != "bytes" || g.RetainedHeapNote != JavaHTTPObjectFootprintRetainedNote {
		return false
	}
	if g.UniqueObjects < 1 || g.MaxNodes < 1 || g.MaxNodes > 65536 || g.MaxDepth < 1 || g.MaxDepth > 256 || g.UniqueObjects > int64(g.MaxNodes) {
		return false
	}
	if g.Available != s.Available {
		return false
	}
	if g.Available {
		if g.Bytes == nil || *g.Bytes < *s.Bytes || *g.Bytes > 1<<40 || g.UnavailableReason != "" {
			return false
		}
	} else {
		if g.Bytes != nil || g.UnavailableReason == "" {
			return false
		}
		if g.FieldLayoutFallbackBytes != nil && (*g.FieldLayoutFallbackBytes <= 0 || *g.FieldLayoutFallbackBytes > 1<<40 || g.FieldLayoutFallbackMethod != JavaHTTPFieldLayoutFallbackMethodV1) {
			return false
		}
	}
	validMs := func(n float64) bool { return n >= 0 && n <= 24*60*60*1000 && !math.IsNaN(n) && !math.IsInf(n, 0) }
	if e.CodecExecuted {
		if e.SerializedBytes == nil || *e.SerializedBytes <= 0 || *e.SerializedBytes > 1<<40 || e.JavaSerializationMs == nil || e.JavaDeserializationMs == nil || !validMs(*e.JavaSerializationMs) || !validMs(*e.JavaDeserializationMs) {
			return false
		}
	} else if e.SerializedBytes != nil || e.JavaSerializationMs != nil || e.JavaDeserializationMs != nil {
		return false
	}
	return true
}

func (e *JavaHTTPObjectFootprintEvidence) Clone() *JavaHTTPObjectFootprintEvidence {
	if e == nil {
		return nil
	}
	out := *e
	out.ShallowObject.Bytes = cloneJavaDiagnosticValue(e.ShallowObject.Bytes)
	out.ShallowObject.FieldLayoutFallbackBytes = cloneJavaDiagnosticValue(e.ShallowObject.FieldLayoutFallbackBytes)
	out.ReachableGraph.Bytes = cloneJavaDiagnosticValue(e.ReachableGraph.Bytes)
	out.ReachableGraph.FieldLayoutFallbackBytes = cloneJavaDiagnosticValue(e.ReachableGraph.FieldLayoutFallbackBytes)
	out.SerializedBytes = cloneJavaDiagnosticValue(e.SerializedBytes)
	out.JavaSerializationMs = cloneJavaDiagnosticValue(e.JavaSerializationMs)
	out.JavaDeserializationMs = cloneJavaDiagnosticValue(e.JavaDeserializationMs)
	return &out
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
	if !d.ContainedObjectFootprint.Valid() {
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
	out.ContainedObjectFootprint = d.ContainedObjectFootprint.Clone()
	return &out
}

// SerializedRepresentationVsHTTPBody returns the explicit byte and ratio
// comparison between decoded request representation bytes and prepared HTTP
// body bytes on the wire, preserving compression/encoding, units, availability,
// and the outer wrapper conversion duration.
func (d *JavaHTTPDiagnostics) SerializedRepresentationVsHTTPBody() JavaHTTPSerializedRepresentationVsHTTPBody {
	out := JavaHTTPSerializedRepresentationVsHTTPBody{
		Metric:       "serialized_representation_vs_http_body",
		BytesUnit:    "bytes",
		RatioUnit:    "ratio",
		DurationUnit: "ms",
	}
	if d == nil || !d.Valid() {
		out.UnavailableReason = "invalid_or_missing_diagnostics"
		return out
	}
	enclosing := d.EnclosingMs
	out.EnclosingMs = &enclosing
	if d.RequestDecodedBytes == nil || d.PreparedRequestBodyBytes == nil {
		out.UnavailableReason = "missing_request_representation_or_http_body"
		return out
	}
	out.AppliedRequestCompression = d.AppliedRequestCompression
	out.Compression = NormalizeJavaHTTPCompressionLabel(d.AppliedRequestCompression)
	decoded := *d.RequestDecodedBytes
	body := *d.PreparedRequestBodyBytes
	delta := body - decoded
	out.DecodedRepresentationBytes = &decoded
	out.HTTPBodyBytes = &body
	out.DeltaBytes = &delta
	if decoded > 0 {
		out.Available = true
		ratio := float64(body) / float64(decoded)
		out.BodyPerRepresentationRatio = &ratio
	} else {
		out.Available = false
		out.UnavailableReason = "zero_decoded_representation_bytes"
	}
	return out
}

// NormalizeJavaHTTPCompressionLabel returns the canonical compression/encoding
// label ("identity" for "none", or "gzip" / "deflate").
func NormalizeJavaHTTPCompressionLabel(applied string) string {
	switch applied {
	case "none":
		return "identity"
	case "gzip", "deflate":
		return applied
	default:
		return ""
	}
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
