package generatorrpc

import (
	"context"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
)

func TestGeneratorStagesReusableScriptWithAssignedTestID(t *testing.T) {
	service := newIsolatedService(t, 10, WithExecution())
	t.Cleanup(func() { shutdownSupervisionTestService(t, service) })
	content := []byte(`{"name":"reusable","protocol":"http","config":{"base_url":"http://127.0.0.1"},"actions":[{"name":"request","type":"GET","target":"/","headers":{"X-Test":"{{run.id}}"}}]}`)
	for _, testID := range []string{"first-test", "second-test"} {
		ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
			TestId: testID, GroupName: "users", ScriptId: "script", ScriptContent: content, VuserCount: 1,
		})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("stage %s: ack=%v err=%v", testID, ack, err)
		}
		service.mu.RLock()
		execution := service.executions[testID]["users"]
		service.mu.RUnlock()
		if execution == nil || execution.stagedConfig == nil || len(execution.stagedConfig.Groups) != 1 || execution.stagedConfig.Groups[0].RunID != testID {
			t.Fatalf("stage %s lost assigned run identity", testID)
		}
	}
}
