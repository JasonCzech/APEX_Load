package protocol

import (
	"math"
	"reflect"
	"testing"
)

func TestJavaHTTPSummaryMergeMatchesDirectObservation(t *testing.T) {
	var direct, merged *JavaHTTPSummary
	for i := 0; i < 7; i++ {
		d := javaDiagnosticFixture()
		if i == 1 {
			d.ResponseState = "unavailable"
			d.ResponseRetainedBytes = nil
			d.ResponseBase64Bytes = nil
			d.Base64EncodeMs = nil
		}
		if i == 2 {
			d = &JavaHTTPDiagnostics{}
		}
		if i == 3 {
			d = nil
		}
		var chunk *JavaHTTPSummary
		MergeJavaHTTPDiagnostic(&direct, d)
		MergeJavaHTTPDiagnostic(&chunk, d)
		MergeJavaHTTPSummary(&merged, chunk)
	}
	if !direct.Valid() || !merged.Valid() || !reflect.DeepEqual(direct, merged) {
		t.Fatal("merge changed populations", direct, merged)
	}
	copy := merged.Clone()
	copy.Base64Work.EnclosingMs = 123
	if merged.Base64Work.EnclosingMs == 123 {
		t.Fatal("summary clone aliases moments")
	}
}

func TestJavaHTTPSummaryRefusesForgedOrUnboundedPeerStatistics(t *testing.T) {
	for _, change := range []func(*JavaHTTPSummary){
		func(s *JavaHTTPSummary) { s.Version = 2 }, func(s *JavaHTTPSummary) { s.Mode = "payload-secret" },
		func(s *JavaHTTPSummary) { s.RequestDecoded.SumBytes = -1 }, func(s *JavaHTTPSummary) { s.RequestDecoded.SumBytes = javaHTTPMaxExactBytes + 1 },
		func(s *JavaHTTPSummary) { s.RequestDecoded.Count = 2 }, func(s *JavaHTTPSummary) { s.PreparedPerDecoded.Samples = 2 },
		func(s *JavaHTTPSummary) { s.Base64Decode.Buckets[0]++ }, func(s *JavaHTTPSummary) { s.Base64Decode.Sum = math.NaN() },
		func(s *JavaHTTPSummary) { s.Base64Decode.Sum = math.Inf(1) }, func(s *JavaHTTPSummary) { s.Base64Work.Base64Ms = 9 },
		func(s *JavaHTTPSummary) { s.Base64Work.EnclosingMs = 11 }, func(s *JavaHTTPSummary) { s.ResponseUnavailable = 1 },
		func(s *JavaHTTPSummary) { s.Samples = javaHTTPMaxSamples + 1 },
	} {
		var src *JavaHTTPSummary
		MergeJavaHTTPDiagnostic(&src, javaDiagnosticFixture())
		change(src)
		if src.Valid() {
			t.Fatal("forged summary admitted", src)
		}
		var dst *JavaHTTPSummary
		MergeJavaHTTPSummary(&dst, src)
		if !dst.Valid() || !dst.Incomplete || dst.Samples != 0 || dst.InvalidSamples != 0 {
			t.Fatal("unknown batch became one measured/invalid action", dst)
		}
	}
}

func TestJavaHTTPSummaryMergedByteOverflowStaysExplicit(t *testing.T) {
	d := javaDiagnosticFixture()
	d.RequestEncodedBytes = 1 << 40
	*d.RequestDecodedBytes = 3 << 38
	*d.PreparedRequestBodyBytes = *d.RequestDecodedBytes
	var s *JavaHTTPSummary
	MergeJavaHTTPDiagnostic(&s, d)
	for range 14 {
		next := s.Clone()
		MergeJavaHTTPSummary(&s, next)
	}
	if !s.Valid() || !s.RequestEncoded.Overflow || !s.DecodedPerEncoded.Overflow || s.Samples != 1<<14 {
		t.Fatal("overflow lost population or became invalid", s)
	}
	if _, ok := s.DecodedPerEncoded.Ratio(); ok {
		t.Fatal("partial sums became qualified ratio")
	}
}
