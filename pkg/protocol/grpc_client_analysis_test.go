package protocol

import (
	"math"
	"testing"
)

func TestGRPCClientAnalysisUsesWeightedCallTimeAndIndependentAvailability(t *testing.T) {
	var summary *GRPCSummary
	for _, ms := range []float64{100, 900} {
		encode, decode := 10., 5.
		MergeGRPCDiagnostic(&summary, &GRPCDiagnostics{Method: "/example.Service/Stream", Mode: "bidi_stream", CallMs: ms, EncodeMs: &encode, DecodeMs: &decode, MessagesSent: 2, MessagesReceived: 1, RequestBytes: 100, ResponseBytes: 75})
	}
	a := summary.ClientAnalysis()
	check := func(label string, v *float64, want float64) {
		t.Helper()
		if v == nil || math.Abs(*v-want) > 1e-9 {
			t.Fatalf("%s=%v want %v", label, v, want)
		}
	}
	check("sent rate", a.SentMessagesPerCallSecond, 4)
	check("received rate", a.ReceivedMessagesPerCallSecond, 2)
	check("sent bytes rate", a.SentBytesPerCallSecond, 200)
	check("received bytes rate", a.ReceivedBytesPerCallSecond, 150)
	check("mean sent payload", a.MeanSentPayloadBytes, 50)
	check("mean received payload", a.MeanReceivedPayloadBytes, 75)
	check("codec work", a.CodecWorkToCallWallPct, 3)
	summary.Decode.Count = 1
	if summary.ClientAnalysis().CodecWorkToCallWallPct != nil {
		t.Fatal("partial codec cohort was scored")
	}
	summary.Call.Count = 1
	a = summary.ClientAnalysis()
	if a.SentMessagesPerCallSecond != nil || a.CodecWorkToCallWallPct != nil || a.MeanSentPayloadBytes == nil {
		t.Fatal("independent availability was lost")
	}
}

func TestGRPCClientAnalysisPreservesZeroMissingAndOverlap(t *testing.T) {
	s := &GRPCSummary{Samples: 1, Call: DiagnosticDistribution{Count: 1, Sum: 100}, Encode: DiagnosticDistribution{Count: 1, Sum: 80}, Decode: DiagnosticDistribution{Count: 1, Sum: 80}}
	a := s.ClientAnalysis()
	if a.ReceivedMessagesPerCallSecond == nil || *a.ReceivedMessagesPerCallSecond != 0 || a.MeanReceivedPayloadBytes != nil {
		t.Fatal("zero receive count became unavailable or a payload sample")
	}
	if a.CodecWorkToCallWallPct == nil || *a.CodecWorkToCallWallPct != 160 {
		t.Fatal("overlapping codec work was clamped")
	}
	s.Call.Sum = 0
	if s.ClientAnalysis().ReceivedMessagesPerCallSecond != nil {
		t.Fatal("zero duration produced a rate")
	}
	var absent *GRPCSummary
	if absent.ClientAnalysis().SentMessagesPerCallSecond != nil {
		t.Fatal("absent evidence scored")
	}
	for _, mutate := range []func(*GRPCSummary){func(s *GRPCSummary) { s.Call.Sum = math.NaN() }, func(s *GRPCSummary) { s.Call.Sum = math.Inf(1) }, func(s *GRPCSummary) { s.MessagesSent = -1 }, func(s *GRPCSummary) { s.RequestBytes = 1 << 53 }} {
		copy := *s
		copy.Call.Sum = 100
		mutate(&copy)
		if copy.ClientAnalysis().SentBytesPerCallSecond != nil {
			t.Fatal("invalid evidence scored")
		}
	}
}
