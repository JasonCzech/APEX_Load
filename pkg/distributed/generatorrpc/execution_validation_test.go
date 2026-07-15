package generatorrpc

import (
	"context"
	"fmt"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestExecutionRejectsScriptThatExceedsRuntimeLimits(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("runtime-limit-validation", 10, WithExecution())
	script := []byte(fmt.Sprintf(`{
		"name":"oversized-runtime",
		"protocol":"http",
		"config":{"base_url":"https://example.test"},
		"parameters":[{"name":"payload","type":"random_string","length":%d}],
		"actions":[{"name":"request","type":"request","target":"/"}]
	}`, dsl.MaxGeneratedParameterBytes+1))

	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:        "runtime-limit-test",
		GroupName:     "group",
		ScriptId:      "runtime-limit-script",
		ScriptContent: script,
		VuserCount:    1,
	})
	if err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "length must not exceed") {
		t.Fatalf("assignment ack = %#v, want runtime-limit rejection", ack)
	}
	service.mu.RLock()
	defer service.mu.RUnlock()
	if len(service.assignments) != 0 || len(service.executions) != 0 || len(service.archiveWriters) != 0 {
		t.Fatalf("rejected execution leaked state: assignments=%d executions=%d writers=%d", len(service.assignments), len(service.executions), len(service.archiveWriters))
	}
}
