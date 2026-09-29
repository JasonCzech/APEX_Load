package api

import (
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func testJavaHostCaps() *pb.JavaHostCapabilities {
	return &pb.JavaHostCapabilities{Contract: generatorrpc.JavaHostSelectionContract, Mode: "dedicated", Containment: "execution_pod", MaxHosts: 25, MaxVusers: 25, MaxMemoryBytes: 1 << 30, Profiles: []*pb.JavaHostProfile{{ScriptSha256: strings.Repeat("a", 64), ProfileId: "approved", ConfigurationSha256: strings.Repeat("b", 64), PolicySha256: strings.Repeat("c", 64), MemoryBytes: 1 << 28, PreparationMs: 120000, MaxRunMs: 3600000, InvocationMs: 30000}}}
}

func TestJavaHostCapabilitySummaryRequiresNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		caps     *pb.JavaHostCapabilities
		features []string
		want     bool
	}{
		{"missing", nil, nil, false},
		{"old peer", testJavaHostCaps(), nil, false},
		{"negotiated", testJavaHostCaps(), []string{generatorrpc.FeatureJavaHostBrokerV1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := javaHostCapabilitySummary(tc.caps, tc.features)
			if (got != nil) != tc.want {
				t.Fatalf("summary = %#v", got)
			}
		})
	}
	caps := testJavaHostCaps()
	caps.Profiles[0].MemoryBytes = 0
	if got := javaHostCapabilitySummary(caps, []string{generatorrpc.FeatureJavaHostBrokerV1}); got != nil {
		t.Fatal("invalid capability retained")
	}
}

func TestJavaHostCapabilitySummaryDoesNotAliasPeerOrArchive(t *testing.T) {
	caps := testJavaHostCaps()
	summary := javaHostCapabilitySummary(caps, []string{generatorrpc.FeatureJavaHostBrokerV1})
	caps.Profiles[0].ProfileId = "changed"
	cloned := cloneGeneratorInventoryEntries([]GeneratorInventoryEntry{{JavaHost: summary}})
	cloned[0].JavaHost.Profiles[0].ProfileID = "archive changed"
	cloned[0].JavaHost.MaxHosts = 1
	if summary.Profiles[0].ProfileID != "approved" || summary.MaxHosts != 25 {
		t.Fatal("capability snapshot aliased")
	}
	if cloneJavaHostCapabilitySummary(nil) != nil {
		t.Fatal("nil snapshot invented")
	}
}
