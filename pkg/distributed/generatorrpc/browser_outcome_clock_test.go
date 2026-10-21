package generatorrpc

import (
	"context"
	pb "github.com/jasonczech/apexload/api/proto"
	"testing"
	"time"
)

func TestBrowserOutcomeClockNegotiationPreservesLegacyAndRejectsWrongAck(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		features             []string
		prefer               bool
		requested, ack, want uint32
		accepted             bool
	}{
		{"v2-peer", []string{FeaturePreparationV1, FeatureBrowserInteractionEvidenceV2}, true, 2, 2, 2, true},
		{"v3-peer", []string{FeaturePreparationV1, FeatureBrowserInteractionEvidenceV2, FeatureBrowserInteractionEvidenceV3}, true, 2, 3, 3, true},
		{"explicit-v2", []string{FeaturePreparationV1, FeatureBrowserInteractionEvidenceV2, FeatureBrowserInteractionEvidenceV3}, false, 2, 2, 2, true},
		{"missing-v3", []string{FeaturePreparationV1, FeatureBrowserInteractionEvidenceV2}, false, 3, 3, 0, false},
		{"wrong-ack", []string{FeaturePreparationV1, FeatureBrowserInteractionEvidenceV2, FeatureBrowserInteractionEvidenceV3}, true, 2, 2, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &interactionVersionRPC{ackVersion: tc.ack}
			c := &Client{rpc: rpc, connection: &sharedClientConnection{}}
			c.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: tc.features})
			result, err := c.AssignWork(context.Background(), Assignment{BrowserInteractionEvidenceVersion: tc.requested, PreferBrowserOutcomeClocks: tc.prefer})
			if (err == nil && result.Accepted) != tc.accepted {
				t.Fatal(result, err)
			}
			if tc.want == 0 {
				if rpc.request != nil {
					t.Fatal("unnegotiated request sent")
				}
			} else if rpc.request == nil || rpc.request.GetBrowserInteractionEvidenceVersion() != tc.want || !rpc.request.PrepareOnly {
				t.Fatal(rpc.request)
			}
		})
	}
	c := &Client{connection: &sharedClientConnection{}}
	c.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: []string{FeaturePreparationV1, FeatureBrowserInteractionEvidenceV2, FeatureBrowserInteractionEvidenceV3}})
	c.connection.browserInteractionObserved = time.Now().Add(-31 * time.Second)
	if c.browserOutcomeClocksNegotiated() {
		t.Fatal("stale outcome capability accepted")
	}
	c.observeBrowserInteractionVersion(nil)
	if c.browserOutcomeClocksNegotiated() {
		t.Fatal("disconnected capability accepted")
	}
}
