package generatorrpc

import (
	"context"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCommitWorkRevalidatesPersistentJavaRuntimeBeforeStart(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	service := newIsolatedService(t, 10, WithExecution())
	script := &dsl.Script{
		Name:     "combined-java",
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "action"},
		}},
	}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Duration: time.Second,
		Groups:   []engine.GroupConfig{{Name: "group", Script: script, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	service.mu.Lock()
	cachePersistentJavaScriptForDrainTest(t, service, "java-script")
	service.assignments["run"] = assignedTest{
		testID:       "run",
		lifecycle:    assignmentLifecycleStaged,
		vuserCount:   1,
		groups:       map[string]int32{"group": 1},
		activeGroups: map[string]int32{"group": 1},
		groupScripts: map[string]string{"group": "java-script"},
	}
	execution := &groupExecution{
		groupName:     "group",
		engine:        mge,
		publisherDone: make(chan struct{}),
		done:          make(chan struct{}),
	}
	service.executions["run"] = map[string]*groupExecution{"group": execution}
	service.mu.Unlock()

	ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CommitWork = (%#v, %v [%s]), want FailedPrecondition", ack, err, status.Code(err))
	}
	if execution.started.Load() {
		t.Fatal("persistent Java execution started after runtime revalidation failed")
	}
	service.mu.RLock()
	committed := service.assignments["run"].committed
	service.mu.RUnlock()
	if committed {
		t.Fatal("assignment crossed the commit boundary after runtime revalidation failed")
	}
}

func TestCommitWorkRechecksAggregatePersistentJavaCapacity(t *testing.T) {
	service := newIsolatedService(t, 10, WithExecution())
	script := []byte(`{
		"name":"combined-java",
		"protocol":"java_vuser",
		"actions":[{"type":"java_vuser_run","target":"Actions","params":{"method":"action"}}]
	}`)
	firstExecution := &groupExecution{groupName: "first", engine: &engine.MultiGroupEngine{}}
	secondExecution := &groupExecution{groupName: "second", engine: &engine.MultiGroupEngine{}}

	service.mu.Lock()
	if err := service.cacheScriptLocked("java-first", script, ""); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	if err := service.cacheScriptLocked("java-second", script, ""); err != nil {
		service.mu.Unlock()
		t.Fatal(err)
	}
	service.assignments["run"] = assignedTest{
		testID:       "run",
		lifecycle:    assignmentLifecycleStaged,
		vuserCount:   4,
		groups:       map[string]int32{"first": 2, "second": 2},
		activeGroups: map[string]int32{"first": 2, "second": 2},
		groupScripts: map[string]string{"first": "java-first", "second": "java-second"},
	}
	service.executions["run"] = map[string]*groupExecution{
		"first":  firstExecution,
		"second": secondExecution,
	}
	transition := service.lifecycleCoordinator.commitWorkValidatedLocked(
		service,
		context.Background(),
		&pb.WorkCommitRequest{TestId: "run"},
		time.Now(),
		&commitRuntimeValidation{
			executions: map[string]*groupExecution{
				"first":  firstExecution,
				"second": secondExecution,
			},
			javaCapacity:      persistentJavaCapacityObservation(3),
			javaCapacityValid: true,
		},
	)
	service.mu.Unlock()

	if status.Code(transition.err) != codes.ResourceExhausted {
		t.Fatalf("commit capacity error = %v (%s), want ResourceExhausted", transition.err, status.Code(transition.err))
	}
	if firstExecution.started.Load() || secondExecution.started.Load() {
		t.Fatal("persistent Java execution started after aggregate capacity shrank")
	}
	service.mu.RLock()
	committed := service.assignments["run"].committed
	service.mu.RUnlock()
	if committed {
		t.Fatal("assignment crossed the commit boundary after aggregate capacity rejection")
	}
}

func TestScaleUpRevalidatesPersistentJavaRuntimeBeforeReservation(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	service := newIsolatedService(t, 10, WithExecution())
	script := &dsl.Script{
		Name:     "combined-java",
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "action"},
		}},
	}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Duration: time.Second,
		Groups:   []engine.GroupConfig{{Name: "group", Script: script, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	const generation = 7
	service.mu.Lock()
	cachePersistentJavaScriptForDrainTest(t, service, "java-script")
	service.assignments["run"] = assignedTest{
		testID:              "run",
		lifecycle:           assignmentLifecycleRunning,
		committed:           true,
		executionGeneration: generation,
		vuserCount:          1,
		groups:              map[string]int32{"group": 1},
		activeGroups:        map[string]int32{"group": 1},
		groupScripts:        map[string]string{"group": "java-script"},
	}
	execution := &groupExecution{groupName: "group", engine: mge}
	execution.started.Store(true)
	service.executions["run"] = map[string]*groupExecution{"group": execution}
	service.mu.Unlock()

	err = service.revalidateScaleRuntime(context.Background(), &pb.VUserAdjustment{
		TestId: "run", GroupName: "group", TargetVusers: 2, ExecutionGeneration: generation,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("scale-up runtime revalidation error = %v (%s), want FailedPrecondition", err, status.Code(err))
	}
	if err := service.revalidateScaleRuntime(context.Background(), &pb.VUserAdjustment{
		TestId: "run", GroupName: "group", TargetVusers: 0, ExecutionGeneration: generation,
	}); err != nil {
		t.Fatalf("downscale unexpectedly required runner revalidation: %v", err)
	}
}
