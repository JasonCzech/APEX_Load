package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const startOffsetProtocolName = "start-offset-test"

var startOffsetTestState struct {
	sync.Mutex
	action chan time.Time
}

type startOffsetProtocol struct{}

func (*startOffsetProtocol) Name() string                               { return startOffsetProtocolName }
func (*startOffsetProtocol) Init(context.Context, map[string]any) error { return nil }
func (*startOffsetProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	startOffsetTestState.Lock()
	sink := startOffsetTestState.action
	startOffsetTestState.Unlock()
	if sink != nil {
		select {
		case sink <- time.Now():
		default:
		}
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*startOffsetProtocol) Teardown(context.Context) error    { return nil }
func (*startOffsetProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register(startOffsetProtocolName, func() protocol.Protocol { return &startOffsetProtocol{} })
}

func startOffsetScript() *dsl.Script {
	return &dsl.Script{
		Name:     "offset",
		Protocol: startOffsetProtocolName,
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
}

func TestMultiGroupStartOffsetUsesActiveTimeAcrossPauseResume(t *testing.T) {
	actions := make(chan time.Time, 1)
	startOffsetTestState.Lock()
	startOffsetTestState.action = actions
	startOffsetTestState.Unlock()
	t.Cleanup(func() {
		startOffsetTestState.Lock()
		startOffsetTestState.action = nil
		startOffsetTestState.Unlock()
	})

	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "later", Script: startOffsetScript(), VUsers: 1, StartOffset: 180 * time.Millisecond}},
		Duration: 600 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	<-mge.Ready()
	if err := mge.Pause(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-actions:
		t.Fatal("group activated while the run was paused")
	case <-time.After(220 * time.Millisecond):
	}
	resumedAt := time.Now()
	if err := mge.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case activatedAt := <-actions:
		if elapsed := activatedAt.Sub(resumedAt); elapsed < 120*time.Millisecond {
			t.Fatalf("group activated after %s; pause time consumed the 180ms active offset", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("offset group did not activate after resume")
	}
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish")
	}
}

func TestNewMultiGroupRejectsStartOffsetAfterDeadline(t *testing.T) {
	_, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "late", Script: startOffsetScript(), VUsers: 1, StartOffset: 2 * time.Second}},
		Duration: time.Second,
	})
	if err == nil {
		t.Fatal("expected start offset deadline validation error")
	}
}
