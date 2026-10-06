package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type iterationNotificationProtocol struct {
	stubProtocol
	events []string
	fail   bool
}

func (p *iterationNotificationProtocol) BeginIteration(context.Context) error {
	p.events = append(p.events, "boundary")
	if p.fail {
		return errors.New("setup failed")
	}
	return nil
}
func (p *iterationNotificationProtocol) Execute(_ context.Context, a *protocol.Action) (*protocol.Result, error) {
	p.events = append(p.events, a.Name)
	return &protocol.Result{Success: true}, nil
}
func TestIterationNotificationLoadAndReplayLifecycle(t *testing.T) {
	for _, replay := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			p := &iterationNotificationProtocol{fail: fail}
			script := &dsl.Script{Name: "iteration", Protocol: "http", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 2}}, Init: []dsl.Action{{Name: "init", Type: "http_request", Method: "GET", Target: "/"}}, Actions: []dsl.Action{{Name: "action", Type: "http_request", Method: "GET", Target: "/"}}, Teardown: []dsl.Action{{Name: "end", Type: "http_request", Method: "GET", Target: "/"}}}
			want := []string{"init", "boundary", "action", "boundary", "action", "end"}
			if replay {
				script.Protocol = "iteration-notification-success"
				if fail {
					script.Protocol = "iteration-notification-failure"
				}
				protocol.Register(script.Protocol, func() protocol.Protocol { return p })
				report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
				if err != nil || report.Success == fail {
					t.Fatalf("report=%+v err=%v", report, err)
				}
				want = []string{"init", "boundary", "action", "end"}
			} else {
				vu := &VUser{ID: 1, Group: "g", Proto: p, Script: script, Metrics: NewMetricsCollector(16)}
				vu.Run(context.Background())
			}
			if fail {
				want = []string{"init", "boundary", "end"}
			}
			if !reflect.DeepEqual(p.events, want) {
				t.Fatalf("replay=%v fail=%v events=%v want=%v", replay, fail, p.events, want)
			}
		}
	}
}

func TestHTTPCompatibilityEngineConfiguration(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{PageTimeoutMs: 500000, IPVersion: "ipv6", CacheMaxUnusedIterations: 1, CacheExcludedMIMETypes: []string{"text/xml"}, HTMLParseMode: "text", HTMLExcludedMIMETypes: []string{"text/plain"}, AuraRecordedFailures: "accept_recorded"}}}
	config := map[string]any{}
	appendHTTPConfig(config, script)
	for key, want := range map[string]any{"page_timeout_ms": float64(500000), "ip_version": "ipv6", "cache_max_unused_iterations": float64(1), "cache_excluded_mime_types": []string{"text/xml"}, "html_parse_mode": "text", "html_excluded_mime_types": []string{"text/plain"}, "aura_recorded_failures": "accept_recorded"} {
		if !reflect.DeepEqual(config[key], want) {
			t.Fatalf("%s=%v want %v", key, config[key], want)
		}
	}
}
