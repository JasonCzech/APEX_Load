package generatorrpc

import (
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTPVersionMixSumsGroupsAndRoundTrips(t *testing.T) {
	assignment := &assignedTest{}
	setGroupHTTPVersions(assignment, "buyers", &protocol.HTTPVersionMix{Actions: 3, ByVersion: map[string]int64{"3": 2, "2": 1}, UpgradeEligible: 3, FellBack: 1, QUICAttempted: 3, QUICFailed: 1, ByFallback: map[string]int64{"quic_connect_failed": 1}, Attempts: 4})
	setGroupHTTPVersions(assignment, "browsers", &protocol.HTTPVersionMix{Actions: 2, ByVersion: map[string]int64{"2": 2}})
	setGroupHTTPVersions(assignment, "browsers", nil) // a snapshot without a mix keeps the counts
	wire := httpVersionMixToProto(assignment.groupHTTPVersions)
	got := httpVersionMixFromProto(wire)
	if got == nil || got.Actions != 5 || got.ByVersion["2"] != 3 || got.ByVersion["3"] != 2 || got.FellBack != 1 || got.Attempts != 4 || got.ByFallback["quic_connect_failed"] != 1 {
		t.Fatalf("round trip = %+v", got)
	}
	if httpVersionMixToProto(nil) != nil || httpVersionMixFromProto(nil) != nil {
		t.Fatal("no HTTP actions means no mix")
	}
}

func TestHTTPVersionMixFromProtoRejectsMalformedCounts(t *testing.T) {
	valid := func() *pb.HTTPVersionMix {
		return &pb.HTTPVersionMix{Actions: 4, ByVersion: map[string]int64{"3": 4}, UpgradeEligible: 4, FellBack: 1, QuicAttempted: 4, QuicFailed: 1}
	}
	if httpVersionMixFromProto(valid()) == nil {
		t.Fatal("valid mix rejected")
	}
	tooMany := map[string]int64{}
	for i := 0; i < maxHTTPVersionMixKeys+1; i++ {
		tooMany[string(rune('a'+i%26))+string(rune('a'+i/26))] = 0
	}
	for name, mutate := range map[string]func(*pb.HTTPVersionMix){
		"negative count":         func(m *pb.HTTPVersionMix) { m.Attempts = -1 },
		"fell back > eligible":   func(m *pb.HTTPVersionMix) { m.FellBack = 5 },
		"failed > attempted":     func(m *pb.HTTPVersionMix) { m.QuicFailed = 5 },
		"breakdown > actions":    func(m *pb.HTTPVersionMix) { m.ByVersion["2"] = 1 },
		"negative breakdown":     func(m *pb.HTTPVersionMix) { m.BySelection = map[string]int64{"upgraded": -1} },
		"oversized map":          func(m *pb.HTTPVersionMix) { m.ByQuicError = tooMany },
		"empty key":              func(m *pb.HTTPVersionMix) { m.ByFallback = map[string]int64{"": 1} },
		"policies exceed action": func(m *pb.HTTPVersionMix) { m.Http3Policies = 5 },
	} {
		mix := valid()
		mutate(mix)
		if got := httpVersionMixFromProto(mix); got != nil {
			t.Fatalf("%s accepted: %+v", name, got)
		}
	}
}
