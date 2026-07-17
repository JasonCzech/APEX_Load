package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type streamingProtocolStub struct{}

func (*streamingProtocolStub) Name() string { return "bruno" }
func (*streamingProtocolStub) Init(context.Context, map[string]any) error {
	return nil
}
func (*streamingProtocolStub) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	panic("streaming protocols must execute through ExecuteStream")
}
func (*streamingProtocolStub) ExecuteStream(_ context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if err := emit(&protocol.Result{
		ActionName:   "Login",
		SourceStepID: "request-login",
		SourcePath:   "auth/login.yml",
		SourceUID:    "login-uid",
		RequestType:  "http",
		Success:      true,
		Duration:     5 * time.Millisecond,
	}); err != nil {
		return nil, err
	}
	if err := emit(&protocol.Result{
		ActionName:    "Response assertions",
		OperationKind: "test",
		Success:       true,
		Duration:      time.Millisecond,
	}); err != nil {
		return nil, err
	}
	return &protocol.Result{
		ActionName: action.Name,
		Success:    true,
		Duration:   8 * time.Millisecond,
	}, nil
}
func (*streamingProtocolStub) Teardown(context.Context) error    { return nil }
func (*streamingProtocolStub) HealthCheck(context.Context) error { return nil }

func TestVUserRunRecordsStreamingChildrenAndAggregateSeparately(t *testing.T) {
	sink := &sequenceCaptureSink{}
	metrics := NewMetricsCollector(16)
	vu := &VUser{
		ID:         1,
		Group:      "Bruno",
		Proto:      &streamingProtocolStub{},
		Metrics:    metrics,
		ResultSink: sink,
		Variables:  map[string]string{},
		Script: &dsl.Script{
			Name:     "Imported Bruno",
			Protocol: "bruno",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
			},
			Actions: []dsl.Action{{
				Name: "Run collection",
				Type: "bruno_run",
				Metadata: map[string]any{
					"source_step_id": "collection-root",
					"source_profile": "bruno",
				},
			}},
		},
	}

	vu.Run(context.Background())

	if len(sink.results) != 3 {
		t.Fatalf("recorded results = %d, want two children and one aggregate", len(sink.results))
	}
	if child := sink.results[0]; child.SampleKind != "child" || child.ParentActionName != "Run collection" || child.SourceStepID != "request-login" || child.SourceProfile != "bruno" {
		t.Fatalf("first child = %#v", child)
	}
	if aggregate := sink.results[2]; aggregate.SampleKind != "aggregate" || aggregate.ActionName != "Run collection" || aggregate.Attempt != 1 {
		t.Fatalf("aggregate = %#v", aggregate)
	}
	snapshot := metrics.computeSnapshot(sink.results, time.Second)
	if snapshot.ByAction["child:Login"] == nil || snapshot.ByAction["child:Response assertions"] == nil || snapshot.ByAction["aggregate:Run collection"] == nil {
		t.Fatalf("streaming dimensions = %#v", snapshot.ByAction)
	}
}
