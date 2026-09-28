package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type initializationProbeProtocol struct {
	stubProtocol
	entered chan struct{}
	release chan struct{}
	fail    bool
}

func (p *initializationProbeProtocol) Execute(ctx context.Context, a *protocol.Action) (*protocol.Result, error) {
	if a.Name == "init" {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if p.fail {
			return &protocol.Result{Success: false}, errors.New("init failed")
		}
	}
	return &protocol.Result{Success: true, StartTime: time.Now()}, nil
}

func TestInitializationObserverWaitsForInit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			p := &initializationProbeProtocol{entered: make(chan struct{}), release: make(chan struct{}), fail: fail}
			ready := make(chan int, 1)
			metrics := NewMetricsCollector(32)
			metrics.initializationObserver = func(id int) { ready <- id }
			metrics.Start(time.Second)
			defer metrics.Stop()
			script := &dsl.Script{Init: []dsl.Action{{Name: "init", Type: "http_request", Method: "GET", Target: "http://fixture.invalid"}}, Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}}
			vu := &VUser{ID: 17, TotalVUsers: 1, Script: script, Proto: p, Metrics: metrics}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); vu.Run(ctx) }()
			select {
			case <-p.entered:
			case <-ctx.Done():
				t.Fatal("init never entered")
			}
			select {
			case <-ready:
				t.Fatal("readiness before init completion")
			default:
			}
			close(p.release)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("VUser did not finish")
			}
			select {
			case id := <-ready:
				if fail || id != 17 {
					t.Fatalf("unexpected readiness %d", id)
				}
			default:
				if !fail {
					t.Fatal("missing readiness")
				}
			}
		})
	}
}
