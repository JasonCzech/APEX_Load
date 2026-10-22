package protocol

import "testing"

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
		{&Result{DependencyType: "http", ByteEvidence: ByteEvidenceHTTPBody, BusinessTransaction: true}, ""},
		{&Result{DependencyType: "http"}, ""},
		{&Result{DependencyType: "grpc"}, ""},
		{nil, ""},
	} {
		if got := ByteEvidence(c.result); got != c.want {
			t.Fatalf("ByteEvidence(%+v) = %q, want %q", c.result, got, c.want)
		}
	}
}
