package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type replayTestProtocol struct {
	execute func(action *protocol.Action) (*protocol.Result, error)
}

func (p *replayTestProtocol) Name() string { return "replay-test" }
func (p *replayTestProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *replayTestProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return p.execute(action)
}
func (p *replayTestProtocol) Teardown(_ context.Context) error { return nil }
func (p *replayTestProtocol) HealthCheck(_ context.Context) error { return nil }

func registerReplayProtocol(t *testing.T, execute func(action *protocol.Action) (*protocol.Result, error)) string {
	t.Helper()
	name := "replay-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(name, func() protocol.Protocol {
		return &replayTestProtocol{execute: execute}
	})
	return name
}

func TestReplayScriptRunsAllPhasesAndInterpolatesExtractions(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		switch action.Name {
		case "login":
			return &protocol.Result{
				ActionName:   action.Name,
				Success:      true,
				StatusCode:   200,
				Extractions:  map[string]string{"token": "abc123"},
			}, nil
		case "profile":
			if action.Target != "/users/abc123" {
				return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "target not interpolated"}, nil
			}
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
		default:
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 204}, nil
		}
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Replay Script",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{},
		Init: []dsl.Action{{Name: "setup", Type: "setup", Target: "/setup"}},
		Actions: []dsl.Action{
			{Name: "login", Type: "POST", Method: "POST", Target: "/login"},
			{Name: "profile", Type: "GET", Method: "GET", Target: "/users/{{var.token}}"},
		},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "cleanup", Target: "/cleanup"}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}

	if !report.Success {
		t.Fatalf("report.Success = false, error=%q", report.Error)
	}
	if len(report.Events) != 4 {
		t.Fatalf("event count = %d, want 4", len(report.Events))
	}
	if report.Events[2].Target != "/users/abc123" {
		t.Fatalf("profile target = %q, want /users/abc123", report.Events[2].Target)
	}
	if got := report.FinalVariables["token"]; got != "abc123" {
		t.Fatalf("final token = %q, want abc123", got)
	}
	if report.Events[1].Phase != "action" || report.Events[3].Phase != "teardown" {
		t.Fatalf("unexpected phases: %#v", report.Events)
	}
}

func TestReplayScriptStopsAfterFailureWhenContinueOnErrorDisabled(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		switch action.Name {
		case "first":
			return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "boom"}, nil
		default:
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 204}, nil
		}
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Failure Script",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false}},
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
		},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "cleanup", Target: "/cleanup"}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}

	if report.Success {
		t.Fatal("report.Success = true, want false")
	}
	if len(report.Events) != 2 {
		t.Fatalf("event count = %d, want 2 (failure + teardown)", len(report.Events))
	}
	if report.Events[0].ActionName != "first" || report.Events[1].Phase != "teardown" {
		t.Fatalf("unexpected events: %#v", report.Events)
	}
	if report.Error != "boom" {
		t.Fatalf("report.Error = %q, want boom", report.Error)
	}
}

func TestReplayScriptRequiresScript(t *testing.T) {
	_, err := ReplayScript(context.Background(), nil, ReplayOptions{})
	if err == nil {
		t.Fatal("expected error for nil script")
	}
	if got := err.Error(); got != fmt.Sprintf("%s", "script is required") {
		t.Fatalf("error = %q, want script is required", got)
	}
}

func TestReplayScriptPublishesProgressEvents(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	var progress []ReplayProgressEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Progress Script",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{},
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
		},
	}, ReplayOptions{
		IgnoreThinkTime: true,
		OnProgress: func(event ReplayProgressEvent) {
			progress = append(progress, event)
		},
	})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}

	if len(progress) != 4 {
		t.Fatalf("progress count = %d, want 4", len(progress))
	}
	gotTypes := []string{progress[0].Type, progress[1].Type, progress[2].Type, progress[3].Type}
	wantTypes := []string{ReplayProgressStarted, ReplayProgressEventRun, ReplayProgressEventRun, ReplayProgressComplete}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Fatalf("progress types = %#v, want %#v", gotTypes, wantTypes)
	}
	if progress[1].Event == nil || progress[1].Event.ActionName != "first" {
		t.Fatalf("first event = %#v, want action first", progress[1].Event)
	}
	if progress[2].Event == nil || progress[2].Event.ActionName != "second" {
		t.Fatalf("second event = %#v, want action second", progress[2].Event)
	}
	if progress[3].Report == nil || len(progress[3].Report.Events) != len(report.Events) {
		t.Fatalf("completion report events = %d, want %d", len(progress[3].Report.Events), len(report.Events))
	}
	if progress[3].Report.CompletedAt.IsZero() {
		t.Fatal("completion report missing completed_at")
	}
	if progress[0].Report == nil || progress[0].Report.CompletedAt.IsZero() == false {
		t.Fatalf("start report completed_at = %v, want zero", progress[0].Report.CompletedAt)
	}
	if progress[1].EventIndex != 0 || progress[2].EventIndex != 1 {
		t.Fatalf("event indexes = %d,%d want 0,1", progress[1].EventIndex, progress[2].EventIndex)
	}
}