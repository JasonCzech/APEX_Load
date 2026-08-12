package generatorrpc

import (
	"context"
	"sync/atomic"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	gproto "google.golang.org/protobuf/proto"
)

type countingRuntimeAdvertisementProtocol struct {
	capabilityCalls *atomic.Int32
	healthCalls     *atomic.Int32
	panicCapability bool
}

func (*countingRuntimeAdvertisementProtocol) Name() string {
	return "runtime_advertisement_counting_test"
}
func (*countingRuntimeAdvertisementProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*countingRuntimeAdvertisementProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*countingRuntimeAdvertisementProtocol) Teardown(context.Context) error    { return nil }
func (*countingRuntimeAdvertisementProtocol) HealthCheck(context.Context) error { return nil }
func (p *countingRuntimeAdvertisementProtocol) RuntimeCapabilities() protocol.RuntimeCapabilities {
	p.capabilityCalls.Add(1)
	if p.panicCapability {
		panic("resolved-capability-secret")
	}
	return protocol.RuntimeCapabilities{
		Available: true,
		Features:  []string{"runtime_advertisement_feature_test"},
		MaxVUsers: 7,
	}
}
func (p *countingRuntimeAdvertisementProtocol) RuntimeHealthy(context.Context) bool {
	p.healthCalls.Add(1)
	return true
}

func TestRuntimeAdvertisementUsesOneConsistentObservationPerProtocol(t *testing.T) {
	var capabilityCalls atomic.Int32
	var healthCalls atomic.Int32
	protocol.Register("runtime_advertisement_counting_test", func() protocol.Protocol {
		return &countingRuntimeAdvertisementProtocol{
			capabilityCalls: &capabilityCalls,
			healthCalls:     &healthCalls,
		}
	})

	protocols, features, capacities := runtimeAdvertisementWithCapacities(
		context.Background(),
		true,
		map[string]int64{"runtime_advertisement_counting_test": 2},
		map[string]string{"runtime_advertisement_counting_test": RuntimeOccupancyModePersistent},
	)
	if !containsProtocolName(protocols, "runtime_advertisement_counting_test") {
		t.Fatalf("protocols = %v, want counting protocol", protocols)
	}
	if !containsProtocolName(features, "runtime_advertisement_feature_test") {
		t.Fatalf("features = %v, want capability feature", features)
	}
	if got := capabilityCalls.Load(); got != 1 {
		t.Fatalf("capability calls = %d, want one", got)
	}
	if got := healthCalls.Load(); got != 1 {
		t.Fatalf("health calls = %d, want one", got)
	}
	var advertised *pb.ProtocolRuntimeCapacity
	for _, capacity := range capacities {
		if capacity.GetProtocol() == "runtime_advertisement_counting_test" {
			advertised = capacity
			break
		}
	}
	if advertised == nil {
		t.Fatalf("runtime capacities = %#v, want counting protocol", capacities)
	}
	if !advertised.GetAvailable() || advertised.GetMaxVusers() != 7 || advertised.GetAvailableVusers() != 5 {
		t.Fatalf("runtime capacity = %#v, want available 5 of 7", advertised)
	}
	if advertised.GetOccupancyMode() != RuntimeOccupancyModePersistent {
		t.Fatalf("runtime occupancy mode = %q, want persistent", advertised.GetOccupancyMode())
	}
	if !containsProtocolName(advertised.GetFeatures(), "runtime_advertisement_feature_test") {
		t.Fatalf("runtime capacity features = %v, want provider feature", advertised.GetFeatures())
	}
}

func TestRuntimeAdvertisementFailsClosedOnCapabilityPanic(t *testing.T) {
	var capabilityCalls atomic.Int32
	var healthCalls atomic.Int32
	protocol.Register("runtime_advertisement_panic_test", func() protocol.Protocol {
		return &countingRuntimeAdvertisementProtocol{
			capabilityCalls: &capabilityCalls,
			healthCalls:     &healthCalls,
			panicCapability: true,
		}
	})

	protocols, features := runtimeAdvertisement(context.Background(), true)
	if containsProtocolName(protocols, "runtime_advertisement_panic_test") ||
		containsProtocolName(features, "runtime_advertisement_panic_test") {
		t.Fatalf("panic-capability runtime was advertised: protocols=%v features=%v", protocols, features)
	}
	if got := capabilityCalls.Load(); got != 1 {
		t.Fatalf("capability calls = %d, want one", got)
	}
	if got := healthCalls.Load(); got != 0 {
		t.Fatalf("health calls = %d, want zero after capability failure", got)
	}
}

func TestJavaVuserRuntimeContainmentContract(t *testing.T) {
	tests := []struct {
		name     string
		features []string
		want     bool
	}{
		{name: "contained local process", features: []string{"java_vuser_local_process_containment_v1"}, want: true},
		{name: "leased owned external runtime", features: []string{"java_vuser_external_runtime_v1", "runner_owner_lease_v1", "java_vuser_runtime_lease_v1"}, want: true},
		{name: "external runtime missing runtime lease", features: []string{"java_vuser_external_runtime_v1", "runner_owner_lease_v1"}},
		{name: "generic owner lease alone", features: []string{"runner_owner_lease_v1"}},
		{name: "external runtime without owner lease", features: []string{"java_vuser_external_runtime_v1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := javaVuserRuntimeContained(test.features); got != test.want {
				t.Fatalf("javaVuserRuntimeContained(%v) = %v, want %v", test.features, got, test.want)
			}
		})
	}
}

func TestBrunoRuntimeContainmentContract(t *testing.T) {
	tests := []struct {
		name     string
		features []string
		want     bool
	}{
		{name: "owner and session leases", features: []string{"runner_owner_lease_v1", "bruno_session_lease_v1"}, want: true},
		{name: "owner lease only", features: []string{"runner_owner_lease_v1"}},
		{name: "session lease only", features: []string{"bruno_session_lease_v1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := brunoRuntimeContained(test.features); got != test.want {
				t.Fatalf("brunoRuntimeContained(%v) = %v, want %v", test.features, got, test.want)
			}
		})
	}
}

func TestProtocolRuntimeCapacitiesFromProtoClonesFeatures(t *testing.T) {
	featureValues := []string{"persistent"}
	values := []*pb.ProtocolRuntimeCapacity{{
		Protocol:        "java_vuser",
		Available:       true,
		MaxVusers:       25,
		AvailableVusers: 9,
		Features:        featureValues,
		OccupancyMode:   RuntimeOccupancyModeLegacy,
	}}
	got := protocolRuntimeCapacitiesFromProto(values)
	if len(got) != 1 || got[0].Protocol != "java_vuser" || !got[0].Available ||
		got[0].MaxVUsers != 25 || got[0].AvailableVUsers != 9 ||
		len(got[0].Features) != 1 || got[0].Features[0] != "persistent" ||
		got[0].OccupancyMode != RuntimeOccupancyModeLegacy {
		t.Fatalf("converted capacities = %#v", got)
	}
	featureValues[0] = "mutated"
	if got[0].Features[0] != "persistent" {
		t.Fatalf("converted features alias protobuf input: %#v", got)
	}
}

func TestProtocolRuntimeCapacityOccupancyModeWireRoundTrip(t *testing.T) {
	encoded, err := gproto.Marshal(&pb.ProtocolRuntimeCapacity{
		Protocol:      "java_vuser",
		OccupancyMode: RuntimeOccupancyModePersistent,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.ProtocolRuntimeCapacity
	if err := gproto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetProtocol() != "java_vuser" ||
		decoded.GetOccupancyMode() != RuntimeOccupancyModePersistent {
		t.Fatalf("decoded runtime capacity = %#v", &decoded)
	}
}
