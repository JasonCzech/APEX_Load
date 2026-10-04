package generatorrpc

import (
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
