package generatorrpc

import (
	"context"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const scenarioCaptureProtocolName = "scenario_capture_transport_test"

var (
	registerScenarioCaptureProtocol sync.Once
	scenarioCaptureMu               sync.Mutex
	scenarioCaptureSink             chan string
)

type scenarioCaptureProtocol struct{}

func (*scenarioCaptureProtocol) Name() string { return scenarioCaptureProtocolName }
func (*scenarioCaptureProtocol) Init(_ context.Context, config map[string]any) error {
	scenarioID, _ := config["scenario_id"].(string)
	scenarioCaptureMu.Lock()
	sink := scenarioCaptureSink
	scenarioCaptureMu.Unlock()
	if sink != nil {
		sink <- scenarioID
	}
	return nil
}
func (*scenarioCaptureProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*scenarioCaptureProtocol) Teardown(context.Context) error    { return nil }
func (*scenarioCaptureProtocol) HealthCheck(context.Context) error { return nil }

func TestAssignmentScenarioIDReachesGeneratorRuntime(t *testing.T) {
	registerScenarioCaptureProtocol.Do(func() {
		protocol.Register(scenarioCaptureProtocolName, func() protocol.Protocol { return &scenarioCaptureProtocol{} })
	})
	captured := make(chan string, 1)
	scenarioCaptureMu.Lock()
	scenarioCaptureSink = captured
	scenarioCaptureMu.Unlock()
	t.Cleanup(func() {
		scenarioCaptureMu.Lock()
		scenarioCaptureSink = nil
		scenarioCaptureMu.Unlock()
	})

	service := newIsolatedService(t, 10, WithExecution())
	assignment := toProtoAssignment(Assignment{
		TestID:     "scenario-run",
		ScenarioID: "managed-scenario",
		GroupName:  "group",
		ScriptID:   "artifact-script",
		ScriptContent: []byte(`{
			"id":"artifact-script",
			"name":"scenario transport",
			"protocol":"scenario_capture_transport_test",
			"config":{"base_url":"https://scenario.invalid","run_logic":{"iterations":1}},
			"actions":[{"name":"capture","type":"capture","target":"/"}]
		}`),
		VUserCount:     1,
		TargetDuration: time.Second,
	})
	ack, err := service.AssignWork(context.Background(), assignment)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", ack, err)
	}
	commit, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "scenario-run"})
	if err != nil || !commit.GetAccepted() {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}
	select {
	case got := <-captured:
		if got != "managed-scenario" {
			t.Fatalf("runtime scenario_id = %q, want managed-scenario", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not receive scenario identity")
	}
}

func TestAssignmentScenarioIDParticipatesInRetryIdentity(t *testing.T) {
	first := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", ScenarioId: "scenario-a"}
	second := &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", ScenarioId: "scenario-b"}
	if workAssignmentFingerprint(first) == workAssignmentFingerprint(second) {
		t.Fatal("scenario_id was omitted from assignment retry identity")
	}
	if got := toProtoAssignment(Assignment{ScenarioID: "scenario-a"}).GetScenarioId(); got != "scenario-a" {
		t.Fatalf("toProtoAssignment scenario_id = %q", got)
	}
}
