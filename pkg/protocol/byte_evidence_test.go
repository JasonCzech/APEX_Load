package protocol

import (
	"math"
	"testing"
)

// An archived result keeps the byte evidence its adapter set, except a
// business transaction, which sums its actions; an adapter that sets none
// (another protocol, an earlier binary) leaves its bytes unavailable.
func TestByteEvidenceIsWhatTheAdapterSet(t *testing.T) {
	for _, c := range []struct {
		result *Result
		want   string
	}{
		{&Result{DependencyType: "http", ByteEvidence: ByteEvidenceHTTPBody}, ByteEvidenceHTTPBody},
		{&Result{DependencyType: "http", ByteEvidence: ByteEvidenceNone}, ByteEvidenceNone},
		{&Result{DependencyType: "grpc", ByteEvidence: ByteEvidenceGRPCUncompressedPayload}, ByteEvidenceGRPCUncompressedPayload},
		{&Result{DependencyType: "websocket", ByteEvidence: ByteEvidenceWebSocketApplicationPayload}, ByteEvidenceWebSocketApplicationPayload},
		{&Result{DependencyType: "http", ByteEvidence: ByteEvidenceHTTPBody, BusinessTransaction: true}, ""},
		{&Result{DependencyType: "http"}, ""},
		{&Result{DependencyType: "grpc"}, ""},
		{&Result{DependencyType: "websocket"}, ""},
		{nil, ""},
	} {
		if got := ByteEvidence(c.result); got != c.want {
			t.Fatalf("ByteEvidence(%+v) = %q, want %q", c.result, got, c.want)
		}
	}
	for _, ev := range []string{ByteEvidenceHTTPBody, ByteEvidenceGRPCUncompressedPayload, ByteEvidenceWebSocketApplicationPayload} {
		if !IsMeasuredByteEvidence(ev) {
			t.Fatalf("expected IsMeasuredByteEvidence(%q) = true", ev)
		}
	}
	if IsMeasuredByteEvidence(ByteEvidenceNone) || IsMeasuredByteEvidence("") {
		t.Fatal("expected unmeasured byte evidence to return false")
	}
	for _, p := range []string{"http", "grpc", "websocket"} {
		if _, ok := ExpectedByteEvidenceForProtocol(p); !ok {
			t.Fatalf("expected ExpectedByteEvidenceForProtocol(%q) = true", p)
		}
	}
	if _, ok := ExpectedByteEvidenceForProtocol("kafka"); ok {
		t.Fatal("expected unsupported protocol to return false")
	}
}

func TestMergeIntervalByteEvidenceSymmetryAndClamping(t *testing.T) {
	type input struct {
		count, samples, sent, recv int64
		ev                         string
	}
	runPair := func(a, b input) (ev string, samples, sent, recv int64) {
		MergeIntervalByteEvidence(&ev, &samples, &sent, &recv, a.count, a.ev, a.samples, a.sent, a.recv)
		MergeIntervalByteEvidence(&ev, &samples, &sent, &recv, b.count, b.ev, b.samples, b.sent, b.recv)
		return
	}
	measuredHTTP := input{count: 2, ev: ByteEvidenceHTTPBody, samples: 2, sent: 300, recv: 400}
	unmeasured := input{count: 1, ev: ByteEvidenceUnmeasured, samples: 0, sent: 100, recv: 200}
	ev1, samp1, sent1, recv1 := runPair(unmeasured, measuredHTTP)
	ev2, samp2, sent2, recv2 := runPair(measuredHTTP, unmeasured)
	if ev1 != ByteEvidenceUnmeasured || ev2 != ByteEvidenceUnmeasured || samp1 != 0 || samp2 != 0 || sent1 != 400 || sent2 != 400 || recv1 != 600 || recv2 != 600 {
		t.Fatalf("unmeasured+measured symmetry failed: (%q,%d,%d,%d) vs (%q,%d,%d,%d)", ev1, samp1, sent1, recv1, ev2, samp2, sent2, recv2)
	}

	measuredGRPC := input{count: 3, ev: ByteEvidenceGRPCUncompressedPayload, samples: 3, sent: 500, recv: 700}
	ev3, samp3, sent3, recv3 := runPair(measuredHTTP, measuredGRPC)
	ev4, samp4, sent4, recv4 := runPair(measuredGRPC, measuredHTTP)
	if ev3 != ByteEvidenceUnmeasured || ev4 != ByteEvidenceUnmeasured || samp3 != 0 || samp4 != 0 || sent3 != 800 || sent4 != 800 || recv3 != 1100 || recv4 != 1100 {
		t.Fatalf("mismatched protocol symmetry failed: (%q,%d,%d,%d) vs (%q,%d,%d,%d)", ev3, samp3, sent3, recv3, ev4, samp4, sent4, recv4)
	}

	nearMax := input{count: 1, ev: ByteEvidenceHTTPBody, samples: 1, sent: math.MaxInt64 - 10, recv: 50}
	overMax := input{count: 1, ev: ByteEvidenceHTTPBody, samples: 1, sent: 25, recv: 50}
	ev5, samp5, sent5, recv5 := runPair(nearMax, overMax)
	if ev5 != ByteEvidenceUnmeasured || samp5 != 0 || sent5 != math.MaxInt64 || recv5 != 100 {
		t.Fatalf("overflow clamping failed: got (%q,%d,%d,%d)", ev5, samp5, sent5, recv5)
	}
}
