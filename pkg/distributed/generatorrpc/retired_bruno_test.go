package generatorrpc

import (
	"context"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
)

func TestRetiredBrunoAssignmentReturnsActionableError(t *testing.T) {
	service := newIsolatedService(t, 10, WithExecution())
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "retired-run", GroupName: "default", ScriptId: "legacy-script", VuserCount: 1,
		ScriptContent: []byte(`{"name":"Legacy","protocol":"bruno","actions":[{"name":"Run","type":"bruno_run"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "Bruno Runner has been retired") {
		t.Fatalf("assignment ack = %#v", ack)
	}
	for _, name := range supportedProtocolNames() {
		if name == "bruno" {
			t.Fatal("retired Runner is advertised")
		}
	}
}
