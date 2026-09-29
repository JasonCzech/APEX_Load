package api

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type managedRunIDCaptureProtocol struct {
	name     string
	observed chan<- string
}

var managedRunIDCaptureSerial atomic.Uint64

func registerManagedRunIDCaptureProtocol(observed chan<- string) string {
	name := fmt.Sprintf("managed-run-id-capture-test-%d", managedRunIDCaptureSerial.Add(1))
	protocol.Register(name, func() protocol.Protocol {
		return &managedRunIDCaptureProtocol{name: name, observed: observed}
	})
	return name
}

func (p *managedRunIDCaptureProtocol) Name() string                             { return p.name }
func (*managedRunIDCaptureProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *managedRunIDCaptureProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.observed <- action.Headers["X-Test"]
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*managedRunIDCaptureProtocol) Teardown(context.Context) error    { return nil }
func (*managedRunIDCaptureProtocol) HealthCheck(context.Context) error { return nil }

func TestManagedReusableScriptUsesCurrentTestID(t *testing.T) {
	observed := make(chan string, 2)
	protocolName := registerManagedRunIDCaptureProtocol(observed)
	script := &dsl.Script{
		Name: "reusable run identity", Protocol: protocolName,
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/",
			Headers: map[string]string{"X-Test": "{{run.id}}"}}},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	for _, testID := range []string{"first-test", "second-test"} {
		test := &ManagedTest{
			ID: testID, Name: "reused scenario", ProjectID: storage.DefaultProjectID,
			Status: "INITIALIZING", Duration: time.Second, DurationStr: time.Second.String(),
			Groups: []TestGroup{{Name: "users", ScriptName: script.Name, VUsers: 1}}, TotalVUsers: 1,
			groupSpecs: []engine.GroupConfig{{Name: "users", Script: script, VUsers: 1}},
		}
		multi, err := runner.buildRunEngine(test, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err = multi.Run(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-observed:
			if got != testID {
				t.Fatalf("test %q sent run identity %q", testID, got)
			}
		default:
			t.Fatalf("test %q did not execute", testID)
		}
	}
}

func TestManagedShakeoutUsesCurrentTestID(t *testing.T) {
	observed := make(chan string, 1)
	protocolName := registerManagedRunIDCaptureProtocol(observed)
	script := &dsl.Script{
		Name: "managed shakeout identity", Protocol: protocolName,
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/",
			Headers: map[string]string{"X-Test": "{{run.id}}"}}},
	}
	test := &ManagedTest{
		ID: "shakeout-test", Name: "shakeout", ProjectID: storage.DefaultProjectID,
		Groups:     []TestGroup{{Name: "users", ScriptName: script.Name, VUsers: 1}},
		groupSpecs: []engine.GroupConfig{{Name: "users", Script: script, VUsers: 1}},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	report, err := runner.runScriptShakeoutReport(test)
	if err != nil || !report.Passed {
		t.Fatalf("managed shakeout report = %+v, err=%v", report, err)
	}
	select {
	case got := <-observed:
		if got != test.ID {
			t.Fatalf("managed shakeout identity = %q, want %q", got, test.ID)
		}
	default:
		t.Fatal("managed shakeout did not execute")
	}
}
