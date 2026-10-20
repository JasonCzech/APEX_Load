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
// remain separate from measured samples. Callers must
// compare Samples to their selected action population before claiming coverage.
// It cannot establish Java object footprint or network delivery.
type JavaHTTPSummary struct {
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
	ResponseUnavailable       int64                  `json:"response_unavailable"`
	ResponseWindow            int64                  `json:"response_window"`
	ResponseDiscarded         int64                  `json:"response_discarded"`
	ObjectFootprint           string                 `json:"object_footprint"`
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
}

func (s *JavaHTTPSummary) Clone() *JavaHTTPSummary {
	if s == nil {
		return nil
	}
	out := *s
	return &out
}
