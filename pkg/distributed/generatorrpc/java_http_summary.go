package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func javaHTTPSummaryToProto(s *protocol.JavaHTTPSummary) *pb.JavaHTTPSummary {
	if s == nil {
		return nil
	}
	if !s.Valid() {
		return &pb.JavaHTTPSummary{}
	}
	return &pb.JavaHTTPSummary{
		Population:             s.Population,
		RequestCompressionNone: s.RequestCompressionNone, RequestCompressionGzip: s.RequestCompressionGzip, RequestCompressionDeflate: s.RequestCompressionDeflate,
		Version:             uint32(s.Version),
		Mode:                s.Mode,
		Samples:             s.Samples,
		InvalidSamples:      s.InvalidSamples,
		SampleLimitReached:  s.SampleLimitReached,
		Incomplete:          s.Incomplete,
		RequestEncoded:      javaHTTPByteMomentsToProto(s.RequestEncoded),
		RequestDecoded:      javaHTTPByteMomentsToProto(s.RequestDecoded),
		PreparedRequestBody: javaHTTPByteMomentsToProto(s.PreparedRequestBody),
		ResponseRetained:    javaHTTPByteMomentsToProto(s.ResponseRetained),
		ResponseBase64:      javaHTTPByteMomentsToProto(s.ResponseBase64),
		DecodedPerEncoded:   javaHTTPBytePairToProto(s.DecodedPerEncoded),
		PreparedPerDecoded:  javaHTTPBytePairToProto(s.PreparedPerDecoded),
		Base64PerRetained:   javaHTTPBytePairToProto(s.Base64PerRetained),
		Base64DecodeMs:      javaHTTPDurationDistributionToProto(s.Base64Decode),
		Base64EncodeMs:      javaHTTPDurationDistributionToProto(s.Base64Encode),
		EnclosingMs:         javaHTTPDurationDistributionToProto(s.Enclosing),
		Base64Work:          javaHTTPWorkPairToProto(s.Base64Work),
		ResponseUnavailable: s.ResponseUnavailable,
		ResponseWindow:      s.ResponseWindow,
		ResponseDiscarded:   s.ResponseDiscarded,
		ObjectFootprint:     s.ObjectFootprint,
	}
}
func refusedJavaHTTPSummary() *protocol.JavaHTTPSummary {
	return (&protocol.JavaHTTPSummary{}).Retained()
}
func javaHTTPSummaryFromProto(p *pb.JavaHTTPSummary) *protocol.JavaHTTPSummary {
	if p == nil {
		return nil
	}
	if p.Version != 1 {
		return refusedJavaHTTPSummary()
	}
	if p.RequestEncoded == nil || p.RequestDecoded == nil || p.PreparedRequestBody == nil || p.ResponseRetained == nil || p.ResponseBase64 == nil || p.DecodedPerEncoded == nil || p.PreparedPerDecoded == nil || p.Base64PerRetained == nil || p.Base64DecodeMs == nil || p.Base64EncodeMs == nil || p.EnclosingMs == nil || p.Base64Work == nil || len(p.Base64DecodeMs.Buckets) != 144 || len(p.Base64EncodeMs.Buckets) != 144 || len(p.EnclosingMs.Buckets) != 144 {
		return refusedJavaHTTPSummary()
	}
	s := &protocol.JavaHTTPSummary{
		Population:             p.Population,
		RequestCompressionNone: p.RequestCompressionNone, RequestCompressionGzip: p.RequestCompressionGzip, RequestCompressionDeflate: p.RequestCompressionDeflate,
		Version:             int(p.Version),
		Mode:                p.Mode,
		Samples:             p.Samples,
		InvalidSamples:      p.InvalidSamples,
		SampleLimitReached:  p.SampleLimitReached,
		Incomplete:          p.Incomplete,
		RequestEncoded:      javaHTTPByteMomentsFromProto(p.RequestEncoded),
		RequestDecoded:      javaHTTPByteMomentsFromProto(p.RequestDecoded),
		PreparedRequestBody: javaHTTPByteMomentsFromProto(p.PreparedRequestBody),
		ResponseRetained:    javaHTTPByteMomentsFromProto(p.ResponseRetained),
		ResponseBase64:      javaHTTPByteMomentsFromProto(p.ResponseBase64),
		DecodedPerEncoded:   javaHTTPBytePairFromProto(p.DecodedPerEncoded),
		PreparedPerDecoded:  javaHTTPBytePairFromProto(p.PreparedPerDecoded),
		Base64PerRetained:   javaHTTPBytePairFromProto(p.Base64PerRetained),
		Base64Decode:        javaHTTPDurationDistributionFromProto(p.Base64DecodeMs),
		Base64Encode:        javaHTTPDurationDistributionFromProto(p.Base64EncodeMs),
		Enclosing:           javaHTTPDurationDistributionFromProto(p.EnclosingMs),
		Base64Work:          javaHTTPWorkPairFromProto(p.Base64Work),
		ResponseUnavailable: p.ResponseUnavailable,
		ResponseWindow:      p.ResponseWindow,
		ResponseDiscarded:   p.ResponseDiscarded,
		ObjectFootprint:     p.ObjectFootprint,
	}
	return s.Retained()
}
func javaHTTPByteMomentsToProto(s protocol.JavaHTTPByteMoments) *pb.JavaHTTPByteMoments {
	return &pb.JavaHTTPByteMoments{
		Count:    s.Count,
		SumBytes: s.SumBytes,
		MinBytes: s.MinBytes,
		MaxBytes: s.MaxBytes,
		Overflow: s.Overflow,
	}
}
func javaHTTPByteMomentsFromProto(p *pb.JavaHTTPByteMoments) protocol.JavaHTTPByteMoments {
	s := protocol.JavaHTTPByteMoments{
		Count:    p.Count,
		SumBytes: p.SumBytes,
		MinBytes: p.MinBytes,
		MaxBytes: p.MaxBytes,
		Overflow: p.Overflow,
	}
	return s
}
func javaHTTPBytePairToProto(s protocol.JavaHTTPBytePair) *pb.JavaHTTPBytePair {
	return &pb.JavaHTTPBytePair{
		Samples:          s.Samples,
		NumeratorBytes:   s.NumeratorBytes,
		DenominatorBytes: s.DenominatorBytes,
		Overflow:         s.Overflow,
	}
}
func javaHTTPBytePairFromProto(p *pb.JavaHTTPBytePair) protocol.JavaHTTPBytePair {
	s := protocol.JavaHTTPBytePair{
		Samples:          p.Samples,
		NumeratorBytes:   p.NumeratorBytes,
		DenominatorBytes: p.DenominatorBytes,
		Overflow:         p.Overflow,
	}
	return s
}
func javaHTTPWorkPairToProto(s protocol.JavaHTTPWorkPair) *pb.JavaHTTPWorkPair {
	return &pb.JavaHTTPWorkPair{
		Samples:     s.Samples,
		Base64Ms:    s.Base64Ms,
		EnclosingMs: s.EnclosingMs,
	}
}
func javaHTTPWorkPairFromProto(p *pb.JavaHTTPWorkPair) protocol.JavaHTTPWorkPair {
	s := protocol.JavaHTTPWorkPair{
		Samples:     p.Samples,
		Base64Ms:    p.Base64Ms,
		EnclosingMs: p.EnclosingMs,
	}
	return s
}
func javaHTTPDurationDistributionToProto(s protocol.DiagnosticDistribution) *pb.JavaHTTPDurationDistribution {
	return &pb.JavaHTTPDurationDistribution{
		Count:   s.Count,
		Sum:     s.Sum,
		Min:     s.Min,
		Max:     s.Max,
		Buckets: append([]int64(nil), s.Buckets[:]...),
	}
}
func javaHTTPDurationDistributionFromProto(p *pb.JavaHTTPDurationDistribution) protocol.DiagnosticDistribution {
	s := protocol.DiagnosticDistribution{
		Count: p.Count,
		Sum:   p.Sum,
		Min:   p.Min,
		Max:   p.Max,
	}
	copy(s.Buckets[:], p.Buckets)
	return s
}
func engineJavaHTTPSummary(s *engine.ServerTelemetrySummary) *protocol.JavaHTTPSummary {
	if s == nil {
		return nil
	}
	return s.JavaHTTP.Retained()
}

// Count is the surrounding action population, including old peers and invalid
// samples. Fewer observed diagnostics never becomes full measured coverage.
func javaHTTPSummaryForCount(s *protocol.JavaHTTPSummary, count int64) *protocol.JavaHTTPSummary {
	return s.RetainedForPopulation(count)
}
