package protocol

// These limits keep JSON counters exact and every aggregate fixed-size. A
// refusal is disclosed; it is never silently wrapped or treated as zero.
const javaHTTPMaxSamples int64 = 1 << 32
const javaHTTPMaxExactBytes int64 = 1<<53 - 1

// JavaHTTPByteMoments uses byte units, not duration histogram buckets. SumBytes
// is a retained prefix when Overflow is set and must not be used for a mean.
type JavaHTTPByteMoments struct {
	Count    int64 `json:"count"`
	SumBytes int64 `json:"sum_bytes"`
	MinBytes int64 `json:"min_bytes"`
	MaxBytes int64 `json:"max_bytes"`
	Overflow bool  `json:"overflow,omitempty"`
}

func (m *JavaHTTPByteMoments) add(value int64) {
	if m.Count == 0 || value < m.MinBytes {
		m.MinBytes = value
	}
	m.MaxBytes = max(m.MaxBytes, value)
	m.Count++
	if m.Overflow || value > javaHTTPMaxExactBytes-m.SumBytes {
		m.Overflow = true
		return
	}
	m.SumBytes += value
}

// JavaHTTPBytePair retains two sums from exactly the same observations. Missing
// either value contributes to neither side. Ratio is unavailable on overflow
// or a zero denominator, including an observed pair of empty representations.
type JavaHTTPBytePair struct {
	Samples          int64 `json:"samples"`
	NumeratorBytes   int64 `json:"numerator_bytes"`
	DenominatorBytes int64 `json:"denominator_bytes"`
	Overflow         bool  `json:"overflow,omitempty"`
}

func (p *JavaHTTPBytePair) add(numerator, denominator *int64) {
	if numerator == nil || denominator == nil {
		return
	}
	p.Samples++
	if p.Overflow || *numerator > javaHTTPMaxExactBytes-p.NumeratorBytes || *denominator > javaHTTPMaxExactBytes-p.DenominatorBytes {
		p.Overflow = true
		return
	}
	p.NumeratorBytes += *numerator
	p.DenominatorBytes += *denominator
}

func (p JavaHTTPBytePair) Ratio() (float64, bool) {
	if p.Overflow || p.Samples <= 0 || p.Samples > javaHTTPMaxSamples || p.NumeratorBytes < 0 || p.NumeratorBytes > javaHTTPMaxExactBytes || p.DenominatorBytes <= 0 || p.DenominatorBytes > javaHTTPMaxExactBytes {
		return 0, false
	}
	return float64(p.NumeratorBytes) / float64(p.DenominatorBytes), true
}

// JavaHTTPWorkPair measures base64 work over the enclosing wrapper interval for
// the same samples. It is neither a mean of per-action ratios nor JVM object work.
type JavaHTTPWorkPair struct {
	Samples     int64   `json:"samples"`
	Base64Ms    float64 `json:"base64_ms"`
	EnclosingMs float64 `json:"enclosing_ms"`
}

func (p JavaHTTPWorkPair) Share() (float64, bool) {
	if p.Samples <= 0 || p.Samples > javaHTTPMaxSamples || p.EnclosingMs <= 0 || p.Base64Ms < 0 || !javaHTTPFinite(p.Base64Ms) || !javaHTTPFinite(p.EnclosingMs) || p.Base64Ms > p.EnclosingMs*(1+1e-12) {
		return 0, false
	}
	return min(1, p.Base64Ms/p.EnclosingMs), true
}

func (m JavaHTTPByteMoments) MeanBytes() (float64, bool) {
	if m.Overflow || m.Count <= 0 || m.Count > javaHTTPMaxSamples || m.SumBytes < 0 || m.SumBytes > javaHTTPMaxExactBytes {
		return 0, false
	}
	return float64(m.SumBytes) / float64(m.Count), true
}

// JavaHTTPSummary describes observed java_http action diagnostics only. Absent
// peers are not inferred from unrelated telemetry; invalid/unknown diagnostics
// remain separate from measured samples. Population, when set, is the number of
// java_http action executions the summary covers, with or without diagnostics;
// zero means a legacy peer that did not count it, and callers fall back to their
// own action count. It cannot establish Java object footprint or network delivery.
type JavaHTTPSummary struct {
	Population                int64                  `json:"population,omitempty"`
	RequestCompressionNone    int64                  `json:"request_compression_none"`
	RequestCompressionGzip    int64                  `json:"request_compression_gzip"`
	RequestCompressionDeflate int64                  `json:"request_compression_deflate"`
	Version                   int                    `json:"version"`
	Mode                      string                 `json:"mode"`
	Samples                   int64                  `json:"samples"`
	InvalidSamples            int64                  `json:"invalid_samples"`
	SampleLimitReached        bool                   `json:"sample_limit_reached,omitempty"`
	Incomplete                bool                   `json:"incomplete,omitempty"`
	RequestEncoded            JavaHTTPByteMoments    `json:"request_encoded"`
	RequestDecoded            JavaHTTPByteMoments    `json:"request_decoded"`
	PreparedRequestBody       JavaHTTPByteMoments    `json:"prepared_request_body"`
	ResponseRetained          JavaHTTPByteMoments    `json:"response_retained"`
	ResponseBase64            JavaHTTPByteMoments    `json:"response_base64"`
	DecodedPerEncoded         JavaHTTPBytePair       `json:"decoded_per_encoded"`
	PreparedPerDecoded        JavaHTTPBytePair       `json:"prepared_per_decoded"`
	Base64PerRetained         JavaHTTPBytePair       `json:"base64_per_retained"`
	Base64Decode              DiagnosticDistribution `json:"base64_decode_ms"`
	Base64Encode              DiagnosticDistribution `json:"base64_encode_ms"`
	Enclosing                 DiagnosticDistribution `json:"enclosing_ms"`
	Base64Work                JavaHTTPWorkPair       `json:"base64_work"`
	ResponseUnavailable       int64                           `json:"response_unavailable"`
	ResponseWindow            int64                           `json:"response_window"`
	ResponseDiscarded         int64                           `json:"response_discarded"`
	ObjectFootprint           string                          `json:"object_footprint"`
	ContainedObjectFootprint  *JavaHTTPObjectFootprintSummary `json:"contained_object_footprint,omitempty"`
}

// JavaHTTPObjectFootprintSummary aggregates opt-in contained JVM object-footprint
// and codec timing measurements across observed java_http actions.
type JavaHTTPObjectFootprintSummary struct {
	Samples                   int64                  `json:"samples"`
	JVMVersion                string                 `json:"jvm_version,omitempty"`
	ShallowMethod             string                 `json:"shallow_method"`
	ReachableGraphMethod      string                 `json:"reachable_graph_method"`
	ShallowAvailableSamples   int64                  `json:"shallow_available_samples"`
	UnavailableSamples        int64                  `json:"unavailable_samples"`
	UnavailableReason         string                 `json:"unavailable_reason,omitempty"`
	ShallowObjectBytes        JavaHTTPByteMoments    `json:"java_shallow_object_bytes"`
	ShallowFallbackBytes      JavaHTTPByteMoments    `json:"shallow_fallback_bytes"`
	ReachableGraphBytes       JavaHTTPByteMoments    `json:"java_reachable_graph_bytes"`
	ReachableGraphFallback    JavaHTTPByteMoments    `json:"reachable_graph_fallback_bytes"`
	UniqueObjects             JavaHTTPByteMoments    `json:"unique_objects"`
	MaxNodes                  int                    `json:"max_nodes"`
	MaxDepth                  int                    `json:"max_depth"`
	TraversalExhaustedSamples int64                  `json:"traversal_exhausted_samples"`
	RetainedHeapNote          string                 `json:"retained_heap_note"`
	CodecSamples              int64                  `json:"codec_samples"`
	SerializedBytes           JavaHTTPByteMoments    `json:"serialized_bytes"`
	JavaSerializationMs       DiagnosticDistribution `json:"java_serialization_ms"`
	JavaDeserializationMs     DiagnosticDistribution `json:"java_deserialization_ms"`
}

// JavaHTTPSerializedRepresentationVsHTTPBodySummary summarizes the paired
// decoded serialized representation bytes vs prepared HTTP body bytes on the
// wire across observed java_http actions.
type JavaHTTPSerializedRepresentationVsHTTPBodySummary struct {
	Metric                     string                 `json:"metric"`
	Available                  bool                   `json:"available"`
	UnavailableReason          string                 `json:"unavailable_reason,omitempty"`
	Compression                string                 `json:"compression,omitempty"`
	AppliedRequestCompression  string                 `json:"applied_request_compression,omitempty"`
	Samples                    int64                  `json:"samples"`
	DecodedRepresentationBytes JavaHTTPByteMoments    `json:"decoded_representation_bytes"`
	HTTPBodyBytes              JavaHTTPByteMoments    `json:"http_body_bytes"`
	BytesUnit                  string                 `json:"bytes_unit"`
	BodyPerRepresentation      JavaHTTPBytePair       `json:"body_per_representation"`
	BodyPerRepresentationRatio *float64               `json:"body_per_representation_ratio,omitempty"`
	RatioUnit                  string                 `json:"ratio_unit"`
	EnclosingMs                DiagnosticDistribution `json:"enclosing_ms"`
	DurationUnit               string                 `json:"duration_unit"`
}

// ObserveJavaHTTPAction counts one actual java_http action execution in the
// summary's own population and merges its diagnostics when present, so a
// same-named action of another protocol never enters the Java denominator.
func ObserveJavaHTTPAction(summary **JavaHTTPSummary, d *JavaHTTPDiagnostics) {
	if summary == nil {
		return
	}
	if *summary == nil {
		*summary = &JavaHTTPSummary{Version: 1, Mode: "embedded_opaque_bytes", ObjectFootprint: "unavailable_embedded_opaque_bytes"}
	}
	s := *summary
	if s.populationKnown() {
		if s.Population < javaHTTPMaxSamples {
			s.Population++
		} else {
			s.SampleLimitReached = true
		}
	}
	MergeJavaHTTPDiagnostic(summary, d)
}

// SetPopulation records a caller-counted java_http action population,
// saturating at the sample limit like ObserveJavaHTTPAction.
func (s *JavaHTTPSummary) SetPopulation(n int64) {
	if s == nil {
		return
	}
	if n > javaHTTPMaxSamples {
		n, s.SampleLimitReached = javaHTTPMaxSamples, true
	}
	s.Population = max(n, 0)
}

// ForgetPopulation marks the java_http population unknown, so callers fall
// back to their own action count and coverage stays incomplete.
func (s *JavaHTTPSummary) ForgetPopulation() {
	if s != nil {
		s.Population, s.Incomplete = 0, true
	}
}

// JavaHTTPPeerUncounted reports whether a peer that sent no Java summary for
// count executions of an action may still have run java_http actions: an older
// generator's uninstrumented ones, unless its dependency type names another
// protocol.
func JavaHTTPPeerUncounted(s *JavaHTTPSummary, count int64, dependencyType string) bool {
	return s == nil && count > 0 && (dependencyType == "" || dependencyType == "java_http" || dependencyType == "multiple")
}

// populationKnown is false for a legacy summary that observed samples without
// counting its java_http action population, and for a refused or forgotten one;
// only a fresh, complete empty summary is a known zero.
func (s *JavaHTTPSummary) populationKnown() bool {
	return s.Population > 0 || s.Samples+s.InvalidSamples == 0 && !s.Incomplete
}

func MergeJavaHTTPDiagnostic(summary **JavaHTTPSummary, d *JavaHTTPDiagnostics) {
	if summary == nil || d == nil {
		return
	}
	if *summary == nil {
		*summary = &JavaHTTPSummary{Version: 1, Mode: "embedded_opaque_bytes", ObjectFootprint: "unavailable_embedded_opaque_bytes"}
	}
	s := *summary
	if s.Samples+s.InvalidSamples >= javaHTTPMaxSamples {
		s.SampleLimitReached = true
		return
	}
	if !d.Valid() {
		s.InvalidSamples++
		return
	}
	s.Samples++
	s.RequestEncoded.add(d.RequestEncodedBytes)
	switch d.AppliedRequestCompression {
	case "none":
		s.RequestCompressionNone++
	case "gzip":
		s.RequestCompressionGzip++
	case "deflate":
		s.RequestCompressionDeflate++
	}
	for _, item := range []struct {
		value   *int64
		moments *JavaHTTPByteMoments
	}{
		{d.RequestDecodedBytes, &s.RequestDecoded}, {d.PreparedRequestBodyBytes, &s.PreparedRequestBody},
		{d.ResponseRetainedBytes, &s.ResponseRetained}, {d.ResponseBase64Bytes, &s.ResponseBase64},
	} {
		if item.value != nil {
			item.moments.add(*item.value)
		}
	}
	s.DecodedPerEncoded.add(d.RequestDecodedBytes, &d.RequestEncodedBytes)
	s.PreparedPerDecoded.add(d.PreparedRequestBodyBytes, d.RequestDecodedBytes)
	s.Base64PerRetained.add(d.ResponseBase64Bytes, d.ResponseRetainedBytes)
	if d.Base64DecodeMs != nil {
		s.Base64Decode.Add(*d.Base64DecodeMs)
	}
	if d.Base64EncodeMs != nil {
		s.Base64Encode.Add(*d.Base64EncodeMs)
	}
	s.Enclosing.Add(d.EnclosingMs)
	if _, ok := d.Base64WorkShare(); ok {
		s.Base64Work.Samples++
		s.Base64Work.Base64Ms += *d.Base64DecodeMs + *d.Base64EncodeMs
		s.Base64Work.EnclosingMs += d.EnclosingMs
	}
	switch d.ResponseState {
	case "unavailable":
		s.ResponseUnavailable++
	case "collection_window":
		s.ResponseWindow++
	case "discarded":
		s.ResponseDiscarded++
	}
	if d.ContainedObjectFootprint != nil {
		mergeContainedObjectFootprintDiagnostic(&s.ContainedObjectFootprint, d.ContainedObjectFootprint)
	}
}

func mergeContainedObjectFootprintDiagnostic(dst **JavaHTTPObjectFootprintSummary, e *JavaHTTPObjectFootprintEvidence) {
	if dst == nil || e == nil {
		return
	}
	if *dst == nil {
		*dst = &JavaHTTPObjectFootprintSummary{
			ShallowMethod:        JavaHTTPShallowObjectMethodV1,
			ReachableGraphMethod: JavaHTTPReachableGraphMethodV1,
			RetainedHeapNote:     JavaHTTPObjectFootprintRetainedNote,
		}
	}
	f := *dst
	f.Samples++
	if f.JVMVersion == "" {
		f.JVMVersion = e.JVMVersion
	} else if f.JVMVersion != e.JVMVersion {
		f.JVMVersion = "mixed"
	}
	if e.ShallowObject.Available && e.ShallowObject.Bytes != nil && e.ReachableGraph.Bytes != nil {
		f.ShallowAvailableSamples++
		f.ShallowObjectBytes.add(*e.ShallowObject.Bytes)
		f.ReachableGraphBytes.add(*e.ReachableGraph.Bytes)
	} else {
		f.UnavailableSamples++
		if f.UnavailableReason == "" {
			f.UnavailableReason = e.ShallowObject.UnavailableReason
		} else if f.UnavailableReason != e.ShallowObject.UnavailableReason {
			f.UnavailableReason = "mixed"
		}
		if e.ShallowObject.FieldLayoutFallbackBytes != nil {
			f.ShallowFallbackBytes.add(*e.ShallowObject.FieldLayoutFallbackBytes)
		}
		if e.ReachableGraph.FieldLayoutFallbackBytes != nil {
			f.ReachableGraphFallback.add(*e.ReachableGraph.FieldLayoutFallbackBytes)
		}
	}
	f.UniqueObjects.add(e.ReachableGraph.UniqueObjects)
	f.MaxNodes = max(f.MaxNodes, e.ReachableGraph.MaxNodes)
	f.MaxDepth = max(f.MaxDepth, e.ReachableGraph.MaxDepth)
	if e.ReachableGraph.TraversalExhausted {
		f.TraversalExhaustedSamples++
	}
	if e.CodecExecuted && e.SerializedBytes != nil && e.JavaSerializationMs != nil && e.JavaDeserializationMs != nil {
		f.CodecSamples++
		f.SerializedBytes.add(*e.SerializedBytes)
		f.JavaSerializationMs.Add(*e.JavaSerializationMs)
		f.JavaDeserializationMs.Add(*e.JavaDeserializationMs)
	}
}

func (s *JavaHTTPSummary) Clone() *JavaHTTPSummary {
	if s == nil {
		return nil
	}
	out := *s
	if s.ContainedObjectFootprint != nil {
		cf := *s.ContainedObjectFootprint
		out.ContainedObjectFootprint = &cf
	}
	return &out
}

// SerializedRepresentationVsHTTPBody returns the summary-level paired
// decoded serialized representation vs HTTP body comparison across observed
// java_http actions.
func (s *JavaHTTPSummary) SerializedRepresentationVsHTTPBody() JavaHTTPSerializedRepresentationVsHTTPBodySummary {
	out := JavaHTTPSerializedRepresentationVsHTTPBodySummary{
		Metric:       "serialized_representation_vs_http_body",
		BytesUnit:    "bytes",
		RatioUnit:    "ratio",
		DurationUnit: "ms",
	}
	if s == nil || !s.Valid() {
		out.UnavailableReason = "invalid_or_missing_summary"
		return out
	}
	out.EnclosingMs = s.Enclosing
	out.DecodedRepresentationBytes = s.RequestDecoded
	out.HTTPBodyBytes = s.PreparedRequestBody
	out.BodyPerRepresentation = s.PreparedPerDecoded
	out.Samples = s.PreparedPerDecoded.Samples
	applied := ""
	switch {
	case s.RequestCompressionNone > 0 && s.RequestCompressionGzip == 0 && s.RequestCompressionDeflate == 0:
		applied = "none"
	case s.RequestCompressionGzip > 0 && s.RequestCompressionNone == 0 && s.RequestCompressionDeflate == 0:
		applied = "gzip"
	case s.RequestCompressionDeflate > 0 && s.RequestCompressionNone == 0 && s.RequestCompressionGzip == 0:
		applied = "deflate"
	case s.RequestCompressionNone+s.RequestCompressionGzip+s.RequestCompressionDeflate > 0:
		applied = "mixed"
	}
	out.AppliedRequestCompression = applied
	if applied == "mixed" {
		out.Compression = "mixed"
	} else {
		out.Compression = NormalizeJavaHTTPCompressionLabel(applied)
	}
	if s.PreparedPerDecoded.Samples == 0 {
		out.UnavailableReason = "no_paired_request_representation_and_http_body"
		return out
	}
	if applied == "mixed" {
		out.UnavailableReason = "mixed_request_compression"
		return out
	}
	if ratio, ok := s.PreparedPerDecoded.Ratio(); ok {
		out.Available = true
		out.BodyPerRepresentationRatio = &ratio
	} else {
		out.Available = false
		out.UnavailableReason = "zero_or_overflow_representation_bytes"
	}
	return out
}
