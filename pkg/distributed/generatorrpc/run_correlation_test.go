package generatorrpc

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
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

// legacyRunCorrelationFingerprint was computed by the fingerprint algorithm
// that predates correlation carriers, for the carrier-free assignment above.
const legacyRunCorrelationFingerprint = "33b4241c11a54283c915b687c75696ed22c89579b8e4e3542cddf130cd462445"
