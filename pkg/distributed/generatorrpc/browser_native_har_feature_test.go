package generatorrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type stubBrowserNativeHARInstance struct {
	protocol.Protocol
	supported bool
}

func (s stubBrowserNativeHARInstance) SupportsBrowserNativeHAR() bool {
	return s.supported
}

func TestBrowserRuntimeFeaturesGateNativeHAROnPlatformSupport(t *testing.T) {
	supported := browserRuntimeFeatures(stubBrowserNativeHARInstance{supported: true})
	if !protocol.RuntimeFeaturesSatisfy(supported, FeatureBrowserNativeHARV1) {
		t.Fatalf("supported instance did not advertise %s: %v", FeatureBrowserNativeHARV1, supported)
	}
	unsupported := browserRuntimeFeatures(stubBrowserNativeHARInstance{supported: false})
	if protocol.RuntimeFeaturesSatisfy(unsupported, FeatureBrowserNativeHARV1) {
		t.Fatalf("unsupported instance advertised %s: %v", FeatureBrowserNativeHARV1, unsupported)
	}
}

func TestBrowserNativeHARClientNegotiatesBeforeRPCWithoutForcingPrepareOnly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		features    []string
		stale       bool
		disconnect  bool
		requireHAR  bool
		prepareOnly bool
		callsRPC    bool
		accepted    bool
		wantErr     string
	}{
		{
			name:       "older_peer_refused_before_rpc",
			features:   []string{FeatureBrowserDocumentEvidenceV2, FeaturePreparationV1},
			requireHAR: true,
			callsRPC:   false,
			accepted:   false,
			wantErr:    "browser_native_har_v1_capability_not_negotiated",
		},
		{
			name:       "stale_heartbeat_refused_before_rpc",
			features:   []string{FeatureBrowserNativeHARV1},
			stale:      true,
			requireHAR: true,
			callsRPC:   false,
			accepted:   false,
			wantErr:    "browser_native_har_v1_capability_not_negotiated",
		},
		{
			name:       "disconnected_heartbeat_refused_before_rpc",
			features:   []string{FeatureBrowserNativeHARV1},
			disconnect: true,
			requireHAR: true,
			callsRPC:   false,
			accepted:   false,
			wantErr:    "browser_native_har_v1_capability_not_negotiated",
		},
		{
			name:        "current_peer_preserves_non_prepare_only_preflight_mode",
			features:    []string{FeatureBrowserNativeHARV1},
			requireHAR:  true,
			prepareOnly: false,
			callsRPC:    true,
			accepted:    true,
		},
		{
			name:        "current_peer_preserves_prepare_only_mode",
			features:    []string{FeatureBrowserNativeHARV1},
			requireHAR:  true,
			prepareOnly: true,
			callsRPC:    true,
			accepted:    true,
		},
		{
			name:       "har_off_accepts_older_peer",
			features:   []string{FeatureBrowserDocumentEvidenceV2},
			requireHAR: false,
			callsRPC:   true,
			accepted:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &interactionVersionRPC{}
			client := &Client{rpc: rpc, connection: &sharedClientConnection{}}
			client.observeBrowserInteractionVersion(&pb.HeartbeatPong{CompatibleFeatures: tc.features})
			if tc.stale {
				client.connection.browserInteractionObserved = time.Now().Add(-31 * time.Second)
			}
			if tc.disconnect {
				client.observeBrowserInteractionVersion(nil)
			}
			result, err := client.AssignWork(context.Background(), Assignment{
				RequireBrowserNativeHAR: tc.requireHAR,
				PrepareOnly:             tc.prepareOnly,
			})
			if (rpc.request != nil) != tc.callsRPC {
				t.Fatalf("callsRPC = %v, want %v (err=%v)", rpc.request != nil, tc.callsRPC, err)
			}
			if (err == nil && result.Accepted) != tc.accepted {
				t.Fatalf("result=%+v err=%v, want accepted=%v", result, err, tc.accepted)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
			if rpc.request != nil && rpc.request.GetPrepareOnly() != tc.prepareOnly {
				t.Fatalf("PrepareOnly = %v, want %v", rpc.request.GetPrepareOnly(), tc.prepareOnly)
			}
		})
	}
}

func TestGeneratorStagingRejectsRequiredNativeHARWhenRuntimeOmitsFeature(t *testing.T) {
	clearTransportEnv(t)
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	previousFeatures := browserInstanceFeatures
	browserInstanceFeatures = func(instance protocol.Protocol) []string {
		return browserRuntimeFeatures(stubBrowserNativeHARInstance{Protocol: instance, supported: false})
	}
	t.Cleanup(func() { browserInstanceFeatures = previousFeatures })

	_, advertisedFeatures, _ := runtimeAdvertisementWithCapacities(context.Background(), false, nil, nil)
	if protocol.RuntimeFeaturesSatisfy(advertisedFeatures, FeatureBrowserNativeHARV1) {
		t.Fatalf("unsupported service still advertised %s: %v", FeatureBrowserNativeHARV1, advertisedFeatures)
	}

	service := newIsolatedService(t, 10, WithExecution())
	for _, policy := range []string{"always", "on_error", "on_sla_breach", "manual"} {
		script := fmt.Sprintf(`{"name":"har-%s","protocol":"browser","config":{"base_url":%q,"browser":{"har_capture":%q}},"actions":[{"name":"open","type":"browser_navigate","target":"/"}]}`, policy, target.URL, policy)
		ack, assignErr := service.AssignWork(context.Background(), &pb.WorkAssignment{
			TestId:        "unsupported-" + policy,
			GroupName:     "web",
			ScriptId:      "script-" + policy,
			ScriptContent: []byte(script),
			VuserCount:    1,
			PrepareOnly:   true,
		})
		if assignErr != nil || ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), dsl.FeatureBrowserNativeHARV1) {
			t.Fatalf("policy %q: ack=%+v err=%v", policy, ack, assignErr)
		}
	}
	offScript := fmt.Sprintf(`{"name":"har-off","protocol":"browser","config":{"base_url":%q,"browser":{"har_capture":"always","har_capture_policy":"off"}},"actions":[{"name":"open","type":"browser_navigate","target":"/"}]}`, target.URL)
	ack, assignErr := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:        "unsupported-off",
		GroupName:     "web",
		ScriptId:      "script-off",
		ScriptContent: []byte(offScript),
		VuserCount:    1,
		PrepareOnly:   true,
	})
	if assignErr != nil || !ack.GetAccepted() {
		t.Fatalf("har off on unsupported platform: ack=%+v err=%v", ack, assignErr)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("staging emitted %d target HTTP requests", got)
	}
}
