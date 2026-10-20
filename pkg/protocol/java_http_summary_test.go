package protocol

import (
	"encoding/json"
	"math"
	"testing"
)

func TestJavaHTTPSummaryKeepsIndependentByteAndTimingPopulations(t *testing.T) {
	var s *JavaHTTPSummary
	a := javaDiagnosticFixture()
	MergeJavaHTTPDiagnostic(&s, a)
	b := a.Clone()
	*b.RequestDecodedBytes = 2
	*b.PreparedRequestBodyBytes = 20
	b.AppliedRequestCompression = "gzip"
	*b.Base64DecodeMs, *b.Base64EncodeMs, b.EnclosingMs = 1, 1, 100
	MergeJavaHTTPDiagnostic(&s, b)
	c := a.Clone()
	c.PreparedRequestBodyBytes, c.AppliedRequestCompression = nil, ""
	c.ResponseState, c.ResponseRetainedBytes, c.ResponseBase64Bytes = "unavailable", nil, nil
	c.Base64EncodeMs, c.EnclosingMs = nil, 1000
	MergeJavaHTTPDiagnostic(&s, c)
	MergeJavaHTTPDiagnostic(&s, nil)
	invalid := a.Clone()
	invalid.Version = 2
	MergeJavaHTTPDiagnostic(&s, invalid)
	if s.Samples != 3 || s.InvalidSamples != 1 || s.ResponseUnavailable != 1 {
		t.Fatal(s)
	}
	if s.RequestDecoded.Count != 3 || s.RequestDecoded.SumBytes != 10 || s.PreparedRequestBody.Count != 2 || s.PreparedRequestBody.SumBytes != 24 || s.ResponseRetained.Count != 2 || s.ResponseRetained.SumBytes != 6 {
		t.Fatal("optional byte denominator changed", s)
	}
	if ratio, ok := s.PreparedPerDecoded.Ratio(); !ok || ratio != 4 || s.PreparedPerDecoded.Samples != 2 {
		t.Fatal("independent missing bytes diluted paired ratio", ratio, s.PreparedPerDecoded)
	}
	if share, ok := s.Base64Work.Share(); !ok || math.Abs(share-5.0/110) > 1e-15 || s.Base64Work.Samples != 2 {
		t.Fatal("share is not paired sums", share, s.Base64Work)
	}
	if s.Base64Decode.Count != 3 || s.Base64Encode.Count != 2 || s.Enclosing.Count != 3 || s.Enclosing.Sum != 1110 {
		t.Fatal("timing denominator changed", s)
	}
	clone := s.Clone()
	clone.RequestDecoded.SumBytes = 1
	clone.Base64Decode.Buckets[0] = 999
	if s.RequestDecoded.SumBytes != 10 || s.Base64Decode.Buckets[0] == 999 {
		t.Fatal("summary clone aliases source")
	}
}

func TestJavaHTTPSummaryByteBoundsDoNotWrapOrInventRatios(t *testing.T) {
	m := JavaHTTPByteMoments{Count: 1, SumBytes: javaHTTPMaxExactBytes - 1, MinBytes: 1, MaxBytes: javaHTTPMaxExactBytes - 1}
	m.add(1)
	if m.Overflow || m.SumBytes != javaHTTPMaxExactBytes {
		t.Fatal("exact sum boundary lost", m)
	}
	m.add(1)
	if !m.Overflow || m.SumBytes != javaHTTPMaxExactBytes || m.Count != 3 {
		t.Fatal("overflow not disclosed", m)
	}
	value := int64(1)
	p := JavaHTTPBytePair{Samples: 1, NumeratorBytes: javaHTTPMaxExactBytes - 1, DenominatorBytes: 1}
	p.add(&value, &value)
	if _, ok := p.Ratio(); !ok {
		t.Fatal("exact ratio boundary refused")
	}
	p.add(&value, &value)
	if _, ok := p.Ratio(); ok || !p.Overflow || p.NumeratorBytes != javaHTTPMaxExactBytes || p.DenominatorBytes != 2 || p.Samples != 3 {
		t.Fatal("overflow partially updated pair", p)
	}
	for _, pair := range []JavaHTTPBytePair{{}, {Samples: 1}, {Samples: 1, NumeratorBytes: 1}} {
		if _, ok := pair.Ratio(); ok {
			t.Fatal("zero denominator became ratio", pair)
		}
	}
	s := &JavaHTTPSummary{Samples: javaHTTPMaxSamples - 1}
	MergeJavaHTTPDiagnostic(&s, &JavaHTTPDiagnostics{})
	MergeJavaHTTPDiagnostic(&s, javaDiagnosticFixture())
	if !s.SampleLimitReached || s.Samples != javaHTTPMaxSamples-1 {
		t.Fatal("sample limit wrapped or fabricated sample", s)
	}
}

func TestJavaHTTPSummaryPreservesObservedEmptyRepresentations(t *testing.T) {
	d := javaDiagnosticFixture()
	d.InputState, d.RequestEncodedBytes = "empty", 0
	*d.RequestDecodedBytes, *d.PreparedRequestBodyBytes = 0, 0
	*d.ResponseRetainedBytes, *d.ResponseBase64Bytes = 0, 0
	d.Base64DecodeMs, d.EnclosingMs = nil, 1
	*d.Base64EncodeMs = 0
	var s *JavaHTTPSummary
	MergeJavaHTTPDiagnostic(&s, d)
	if s.Samples != 1 || s.RequestDecoded.Count != 1 || s.RequestDecoded.SumBytes != 0 || s.ResponseRetained.Count != 1 || s.ResponseRetained.SumBytes != 0 || s.Base64Decode.Count != 0 || s.Base64Encode.Count != 1 || s.Base64Work.Samples != 0 {
		t.Fatal("observed empty differs from unavailable", s)
	}
	if _, ok := s.PreparedPerDecoded.Ratio(); ok {
		t.Fatal("empty body fabricated compression ratio")
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

func TestJavaHTTPSummaryAbsentPeerDoesNotInventAJavaPopulation(t *testing.T) {
	var s *JavaHTTPSummary
	MergeJavaHTTPDiagnostic(&s, nil)
	if s != nil {
		t.Fatal("absent old peer became measured diagnostics", s)
	}
}

func TestJavaHTTPSummaryRetainsCompressionCohorts(t *testing.T) {
	zero := int64(0)
	var s *JavaHTTPSummary
	for _, compression := range []string{"none", "gzip", "deflate"} {
		MergeJavaHTTPDiagnostic(&s, &JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty", RequestDecodedBytes: &zero, PreparedRequestBodyBytes: &zero, AppliedRequestCompression: compression, ResponseState: "unavailable", EnclosingMs: 1, ObjectFootprint: "unavailable_embedded_opaque_bytes"})
	}
	if !s.Valid() || s.RequestCompressionNone != 1 || s.RequestCompressionGzip != 1 || s.RequestCompressionDeflate != 1 || s.PreparedRequestBody.Count != 3 {
		t.Fatal(s)
	}
	var merged *JavaHTTPSummary
	MergeJavaHTTPSummary(&merged, s)
	MergeJavaHTTPSummary(&merged, s)
	if !merged.Valid() || merged.RequestCompressionGzip != 2 || merged.PreparedRequestBody.Count != 6 {
		t.Fatal(merged)
	}
	malformed := s.Clone()
	malformed.RequestCompressionGzip = 0
	if malformed.Valid() || !malformed.Retained().Incomplete {
		t.Fatal("lost compression accepted")
	}
}
