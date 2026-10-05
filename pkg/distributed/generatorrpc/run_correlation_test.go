package generatorrpc

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestAssignmentRunCorrelationOriginsRetryIdentityAndBounds(t *testing.T) {
	first := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", RunCorrelationOrigins: []string{"https://app.example"}}
	second := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", RunCorrelationOrigins: []string{"https://another.example"}}
	if workAssignmentFingerprint(first) == workAssignmentFingerprint(second) {
		t.Fatal("run origins omitted from retry identity")
	}
	origins := []string{"https://app.example"}
	encoded := toProtoAssignment(Assignment{TestID: "run", RunCorrelationOrigins: origins})
	if !reflect.DeepEqual(encoded.GetRunCorrelationOrigins(), origins) {
		t.Fatalf("origins=%v", encoded.GetRunCorrelationOrigins())
	}
	origins[0] = "https://changed.example"
	if encoded.GetRunCorrelationOrigins()[0] != "https://app.example" {
		t.Fatal("protobuf origins alias assignment")
	}
	if err := validateAssignmentMetadataBounds(first); err != nil {
		t.Fatalf("valid metadata: %v", err)
	}
	for _, origins := range [][]string{{"http://app.example"}, {"https://app.example"}, {"http://app.example:8080", "https://another.example/"}} {
		valid := &pb.WorkAssignment{TestId: "run", RunCorrelationOrigins: origins}
		if err := validateAssignmentMetadataBounds(valid); err != nil {
			t.Fatalf("approved origins=%v err=%v", origins, err)
		}
	}
	for _, origins := range [][]string{{"https://user@app.example"}, {"http://user:password@app.example"}, {"https://app.example/secret"}, {"http://app.example?x=y"}, {"https://app.example#fragment"}, {"https:app.example"}, {"ftp://app.example"}, {"https:///"}, {""}, make([]string, 33)} {
		invalid := &pb.WorkAssignment{TestId: "run", RunCorrelationOrigins: origins}
		if err := validateAssignmentMetadataBounds(invalid); err == nil || !strings.Contains(err.Error(), "correlation") {
			t.Fatalf("origins=%v err=%v", origins, err)
		}
	}
}

func TestAssignmentCorrelationCarriersIdentityAndValidation(t *testing.T) {
	legacy := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", RunCorrelationOrigins: []string{"https://app.example"}}
	withCarriers := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", RunCorrelationOrigins: []string{"https://app.example"}, CorrelationCarriers: []string{"traceparent"}}
	withMarker := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", RunCorrelationOrigins: []string{"https://app.example"}, CorrelationCarriers: []string{"traceparent", "transaction_marker"}, CorrelationTestName: "Peak"}
	if workAssignmentFingerprint(legacy) == workAssignmentFingerprint(withCarriers) || workAssignmentFingerprint(withCarriers) == workAssignmentFingerprint(withMarker) {
		t.Fatal("correlation carriers omitted from retry identity")
	}
	// Assignments without carriers keep the fingerprint older peers compute.
	if got := fmt.Sprintf("%x", workAssignmentFingerprint(legacy)); got != legacyRunCorrelationFingerprint {
		t.Fatalf("carrier-free fingerprint changed: %s", got)
	}
	encoded := toProtoAssignment(Assignment{TestID: "run", RunCorrelationOrigins: []string{"https://app.example"}, CorrelationCarriers: []string{"traceparent"}, CorrelationTestName: "Peak"})
	if !reflect.DeepEqual(encoded.GetCorrelationCarriers(), []string{"traceparent"}) || encoded.GetCorrelationTestName() != "Peak" {
		t.Fatalf("encoded carriers = %v test name = %q", encoded.GetCorrelationCarriers(), encoded.GetCorrelationTestName())
	}
	if err := validateAssignmentMetadataBounds(withMarker); err != nil {
		t.Fatalf("valid carriers rejected: %v", err)
	}
	for _, invalid := range []*pb.WorkAssignment{
		{TestId: "run", RunCorrelationOrigins: []string{"https://app.example"}, CorrelationCarriers: []string{"baggage"}},
		{TestId: "run", CorrelationCarriers: []string{"traceparent"}},
		{TestId: "run", RunCorrelationOrigins: []string{"https://app.example"}, CorrelationTestName: strings.Repeat("t", 257)},
	} {
		if err := validateAssignmentMetadataBounds(invalid); err == nil {
			t.Fatalf("assignment %+v accepted", invalid)
		}
	}
}

// legacyRunCorrelationFingerprint was computed by main's fingerprint algorithm
// before correlation carriers were added, for the carrier-free assignment above.
const legacyRunCorrelationFingerprint = "09c85e339e40e266085d2d969f56cbbaaf22b11e234062a5205fbd0f32745991"

// Field 43 was published for origins before resource claims were released.
// Raw historical wire bytes must keep their meaning after the combined merge.
func TestAssignmentPublishedRunCorrelationWireAndResourceClaimsRemainDistinct(t *testing.T) {
	fields := (&pb.WorkAssignment{}).ProtoReflect().Descriptor().Fields()
	if field := fields.ByName("run_correlation_origins"); field == nil || field.Number() != 43 {
		t.Fatal("published run correlation field changed wire identity")
	}
	if field := fields.ByName("runtime_resource_claim"); field == nil || field.Number() != 44 {
		t.Fatal("runtime resource claim overlaps a published wire field")
	}
	wire := protowire.AppendTag(nil, 43, protowire.BytesType)
	wire = protowire.AppendString(wire, "https://app.example")
	var historical pb.WorkAssignment
	if err := proto.Unmarshal(wire, &historical); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(historical.GetRunCorrelationOrigins(), []string{"https://app.example"}) || historical.GetRuntimeResourceClaim() != nil {
		t.Fatal("published origins became a resource claim")
	}
	base := Assignment{TestID: "run", RunCorrelationOrigins: []string{"https://app.example"}, RuntimeResourceClaim: &pb.RuntimeResourceClaim{
		Contract: "runtime-resource-v1", Protocol: "eventhubs", PeakVusers: 2,
		Requested: &pb.RuntimeResourceDemand{Processors: 2, Workers: 4, MaxPartitionsPerProcessor: 2},
	}}
	combined := toProtoAssignment(base)
	encoded, err := proto.Marshal(combined)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.WorkAssignment
	if err := proto.Unmarshal(encoded, &decoded); err != nil || !proto.Equal(combined, &decoded) {
		t.Fatalf("combined assignment round trip: %v", err)
	}
	originalFingerprint := workAssignmentFingerprint(combined)
	changedOrigins := proto.Clone(combined).(*pb.WorkAssignment)
	changedOrigins.RunCorrelationOrigins[0] = "https://another.example"
	changedClaim := proto.Clone(combined).(*pb.WorkAssignment)
	changedClaim.RuntimeResourceClaim.Requested.Workers++
	if originalFingerprint == workAssignmentFingerprint(changedOrigins) || originalFingerprint == workAssignmentFingerprint(changedClaim) {
		t.Fatal("combined retry identity omitted an origin or resource demand")
	}
	base.RuntimeResourceClaim.Requested.Workers++
	base.RunCorrelationOrigins[0] = "https://mutated.example"
	if combined.RuntimeResourceClaim.Requested.Workers != 4 || combined.RunCorrelationOrigins[0] != "https://app.example" {
		t.Fatal("combined assignment aliases caller-owned claims or origins")
	}
}
